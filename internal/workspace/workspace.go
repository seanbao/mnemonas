// Package workspace provides native file system operations for MnemoNAS
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var readDirEntryInfo = func(entry os.DirEntry) (os.FileInfo, error) {
	return entry.Info()
}

var copyWorkspaceData = copyWithContext
var syncWorkspaceDir = syncWorkspaceParentDir

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		readBytes, readErr := src.Read(buffer)
		if readBytes > 0 {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			writeBytes, writeErr := dst.Write(buffer[:readBytes])
			written += int64(writeBytes)
			if writeErr != nil {
				return written, writeErr
			}
			if writeBytes != readBytes {
				return written, io.ErrShortWrite
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func cleanupTempPath(tmpPath string, operationErr error) error {
	if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func syncWorkspaceParentDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncWorkspaceRenameDirs(oldPath, newPath string) error {
	oldDir := filepath.Dir(oldPath)
	newDir := filepath.Dir(newPath)
	if oldDir == newDir {
		return syncWorkspaceDir(oldDir)
	}
	if err := syncWorkspaceDir(newDir); err != nil {
		return err
	}
	return syncWorkspaceDir(oldDir)
}

func collectMissingWorkspaceDirs(fullPath string) ([]string, error) {
	missing := make([]string, 0)
	current := fullPath
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, ErrNotDir
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			if errors.Is(err, syscall.ENOTDIR) {
				return nil, ErrNotDir
			}
			return nil, err
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return missing, nil
}

func syncCreatedWorkspaceDirs(createdDirs []string) error {
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := syncWorkspaceDir(filepath.Dir(createdDirs[i])); err != nil {
			return err
		}
	}
	return nil
}

// Common errors
var (
	ErrNotFound             = errors.New("not found")
	ErrAlreadyExists        = errors.New("already exists")
	ErrNotDir               = errors.New("not a directory")
	ErrIsDir                = errors.New("is a directory")
	errWorkspaceRootSymlink = errors.New("workspace root must not be a symlink")
)

// FileInfo represents file metadata
type FileInfo struct {
	Path    string
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// Workspace provides native file operations on a root directory
type Workspace struct {
	root string
}

// New creates a new Workspace with the given root directory
func New(root string) (*Workspace, error) {
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errWorkspaceRootSymlink
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// Ensure root exists
	createdDirs, err := collectMissingWorkspaceDirs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return nil, fmt.Errorf("failed to sync directory: %w", err)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(absRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errWorkspaceRootSymlink
	} else if err != nil {
		return nil, err
	}

	return &Workspace{root: absRoot}, nil
}

func (w *Workspace) validatePath(fullPath string) error {
	cleanRoot := filepath.Clean(w.root)
	cleanPath := filepath.Clean(fullPath)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return ErrNotFound
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ErrNotFound
	}

	current := cleanRoot
	if err := validateWorkspaceComponent(current); err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := validateWorkspaceComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkspaceComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}
	return nil
}

// Root returns the workspace root directory
func (w *Workspace) Root() string {
	return w.root
}

// FullPath returns the full filesystem path for a workspace path
func (w *Workspace) FullPath(name string) string {
	name = CleanPath(name)
	if name == "/" {
		return w.root
	}
	return filepath.Join(w.root, name)
}

// CleanPath normalizes a path for use within the workspace
func CleanPath(name string) string {
	// Normalize path separators
	name = strings.ReplaceAll(name, "\\", "/")

	// Keep paths rooted inside the workspace while preserving valid names like foo..txt.
	return path.Clean("/" + name)
}

// Stat returns file information
func (w *Workspace) Stat(ctx context.Context, name string) (*FileInfo, error) {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}

	return &FileInfo{
		Path:    CleanPath(name),
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// ReadDir lists directory contents
func (w *Workspace) ReadDir(ctx context.Context, name string) ([]*FileInfo, error) {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}

	result := make([]*FileInfo, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := readDirEntryInfo(e)
		if err != nil {
			return nil, err
		}

		childPath := path.Join(CleanPath(name), e.Name())
		result = append(result, &FileInfo{
			Path:    childPath,
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}

	return result, nil
}

// OpenFile opens a file for reading
func (w *Workspace) OpenFile(ctx context.Context, name string) (*os.File, error) {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}

	if info.IsDir() {
		return nil, ErrIsDir
	}

	return os.Open(fullPath)
}

// ReadFile reads entire file content
func (w *Workspace) ReadFile(ctx context.Context, name string) ([]byte, error) {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}

	if info.IsDir() {
		return nil, ErrIsDir
	}

	return os.ReadFile(fullPath)
}

// WriteFile writes data to a file, creating parent directories as needed
func (w *Workspace) WriteFile(ctx context.Context, name string, data []byte) error {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	// Atomic write: write to temp file then rename
	tmpFile, err := os.CreateTemp(filepath.Dir(fullPath), ".workspace-*.tmp")
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return cleanupTempPath(tmpPath, err)
	}

	_, writeErr := tmpFile.Write(data)
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()

	if writeErr != nil {
		return cleanupTempPath(tmpPath, writeErr)
	}
	if syncErr != nil {
		return cleanupTempPath(tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupTempPath(tmpPath, closeErr)
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return cleanupTempPath(tmpPath, err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// WriteFileFromReader writes data from a reader to a file
func (w *Workspace) WriteFileFromReader(ctx context.Context, name string, r io.Reader) error {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	// Atomic write: write to temp file then rename
	tmpFile, err := os.CreateTemp(filepath.Dir(fullPath), ".workspace-*.tmp")
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return cleanupTempPath(tmpPath, err)
	}

	_, copyErr := copyWorkspaceData(ctx, tmpFile, r)
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()

	if copyErr != nil {
		return cleanupTempPath(tmpPath, copyErr)
	}
	if syncErr != nil {
		return cleanupTempPath(tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupTempPath(tmpPath, closeErr)
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return cleanupTempPath(tmpPath, err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// Mkdir creates a directory
func (w *Workspace) Mkdir(ctx context.Context, name string) error {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}

	// Check if already exists
	info, err := os.Stat(fullPath)
	if err == nil {
		if info.IsDir() {
			return ErrAlreadyExists
		}
		return ErrNotDir
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}

	createdDirs, err := collectMissingWorkspaceDirs(fullPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

// Delete removes a file or empty directory
func (w *Workspace) Delete(ctx context.Context, name string) error {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	if info.IsDir() {
		if err := os.Remove(fullPath); err != nil {
			return err
		}
		if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
			return fmt.Errorf("failed to sync directory: %w", err)
		}
		return nil
	}

	if err := os.Remove(fullPath); err != nil {
		return err
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// DeleteAll removes a file or directory recursively
func (w *Workspace) DeleteAll(ctx context.Context, name string) error {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}

	_, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	if err := os.RemoveAll(fullPath); err != nil {
		return err
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// Rename moves or renames a file or directory
func (w *Workspace) Rename(ctx context.Context, oldName, newName string) error {
	oldPath := w.FullPath(oldName)
	newPath := w.FullPath(newName)
	if err := w.validatePath(oldPath); err != nil {
		return err
	}
	if err := w.validatePath(newPath); err != nil {
		return err
	}

	// Check source exists
	_, err := os.Stat(oldPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	if _, err := os.Stat(newPath); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		return err
	}

	parentInfo, err := os.Stat(filepath.Dir(newPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if err := syncWorkspaceRenameDirs(oldPath, newPath); err != nil {
		if rollbackErr := os.Rename(newPath, oldPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync directory: %w", err),
				fmt.Errorf("failed to rollback renamed path: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncWorkspaceRenameDirs(newPath, oldPath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("failed to sync directory: %w", err),
				fmt.Errorf("failed to sync rollback directory: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

// Copy copies a file
func (w *Workspace) Copy(ctx context.Context, srcName, dstName string) error {
	srcPath := w.FullPath(srcName)
	dstPath := w.FullPath(dstName)
	if err := w.validatePath(srcPath); err != nil {
		return err
	}
	if err := w.validatePath(dstPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Check source exists and is a file
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}

	if srcInfo.IsDir() {
		return ErrIsDir
	}

	parentInfo, err := os.Stat(filepath.Dir(dstPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return ErrNotDir
		}
		return err
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}

	// Copy file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.CreateTemp(filepath.Dir(dstPath), ".workspace-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := dstFile.Name()
	if err := dstFile.Chmod(0644); err != nil {
		_ = dstFile.Close()
		return cleanupTempPath(tmpPath, err)
	}

	_, copyErr := copyWorkspaceData(ctx, dstFile, srcFile)
	syncErr := dstFile.Sync()
	closeErr := dstFile.Close()

	if copyErr != nil {
		return cleanupTempPath(tmpPath, copyErr)
	}
	if syncErr != nil {
		return cleanupTempPath(tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupTempPath(tmpPath, closeErr)
	}

	if err := os.Rename(tmpPath, dstPath); err != nil {
		return cleanupTempPath(tmpPath, err)
	}
	if err := syncWorkspaceDir(filepath.Dir(dstPath)); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// WalkFunc is the type of function called by Walk
type WalkFunc func(path string, info *FileInfo) error

// Walk walks the file tree rooted at root, calling fn for each file or directory
func (w *Workspace) Walk(ctx context.Context, root string, fn WalkFunc) error {
	rootPath := w.FullPath(root)
	rootClean := CleanPath(root)
	if err := w.validatePath(rootPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return filepath.Walk(rootPath, func(absPath string, info os.FileInfo, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}

		// Compute relative path
		relPath, err := filepath.Rel(w.root, absPath)
		if err != nil {
			return err
		}

		cleanPath := "/" + filepath.ToSlash(relPath)
		if cleanPath == "/." {
			cleanPath = rootClean
		}

		return fn(cleanPath, &FileInfo{
			Path:    cleanPath,
			Name:    info.Name(),
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	})
}

// CleanupStaging removes incomplete workspace staging files.
func (w *Workspace) CleanupStaging(ctx context.Context) (files int, bytes int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	err = filepath.WalkDir(w.root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || !isWorkspaceStagingFile(entry.Name()) {
			return nil
		}

		info, err := readDirEntryInfo(entry)
		if err != nil {
			return err
		}
		if rmErr := os.Remove(path); rmErr == nil {
			files++
			bytes += info.Size()
		}

		return nil
	})

	return files, bytes, err
}

func isWorkspaceStagingFile(name string) bool {
	return strings.HasPrefix(name, ".workspace-") && strings.HasSuffix(name, ".tmp")
}

// Exists checks if a path exists
func (w *Workspace) Exists(ctx context.Context, name string) bool {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	_, err := os.Stat(fullPath)
	return err == nil
}

// IsDir checks if a path is a directory
func (w *Workspace) IsDir(ctx context.Context, name string) bool {
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	info, err := os.Stat(fullPath)
	return err == nil && info.IsDir()
}
