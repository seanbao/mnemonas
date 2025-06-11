// Package workspace provides native file system operations for MnemoNAS
package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
var afterValidateWorkspacePaths = func() error { return nil }

const workspaceRootEscapeError = "path escapes from parent"

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

func cleanupWorkspaceTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
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
	root       string
	rootHandle *os.Root
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

	rootHandle, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, err
	}

	return &Workspace{root: absRoot, rootHandle: rootHandle}, nil
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

func workspaceRootRelativeName(name string) string {
	cleanName := strings.TrimPrefix(CleanPath(name), "/")
	if cleanName == "" {
		return "."
	}
	return cleanName
}

func workspaceParentRelativeName(name string) string {
	return workspaceRootRelativeName(path.Dir(CleanPath(name)))
}

func isWorkspaceRootEscapeError(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	return pathErr.Err != nil && pathErr.Err.Error() == workspaceRootEscapeError
}

func mapWorkspaceRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if isWorkspaceRootEscapeError(err) || errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}
	return err
}

func (w *Workspace) mapWorkspaceCreatePathError(fullPath string, err error) error {
	if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
		return mappedErr
	}
	if errors.Is(err, os.ErrExist) {
		if validateErr := w.validatePath(fullPath); validateErr != nil {
			return validateErr
		}
	}
	return err
}

func newWorkspaceTempName(parentName string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	tempName := ".workspace-" + hex.EncodeToString(suffix[:]) + ".tmp"
	if parentName == "." {
		return tempName, nil
	}
	return path.Join(parentName, tempName), nil
}

func createWorkspaceTempFile(root *os.Root, parentName string) (*os.File, string, error) {
	for range 32 {
		tempName, err := newWorkspaceTempName(parentName)
		if err != nil {
			return nil, "", err
		}
		tempFile, err := root.OpenFile(tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tempFile, tempName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapWorkspaceRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp file")
}

// Close releases the workspace root handle.
func (w *Workspace) Close() error {
	if w == nil || w.rootHandle == nil {
		return nil
	}
	err := w.rootHandle.Close()
	w.rootHandle = nil
	return err
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

func validateWorkspaceName(name string) error {
	normalized := strings.ReplaceAll(name, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return ErrNotFound
		}
	}
	return nil
}

// Stat returns file information
func (w *Workspace) Stat(ctx context.Context, name string) (*FileInfo, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	info, err := w.rootHandle.Stat(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
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
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	dirHandle, err := w.rootHandle.Open(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	defer dirHandle.Close()

	dirInfo, err := dirHandle.Stat()
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() {
		return nil, ErrNotDir
	}

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
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
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	fileHandle, err := w.rootHandle.Open(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}

	info, err := fileHandle.Stat()
	if err != nil {
		_ = fileHandle.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = fileHandle.Close()
		return nil, ErrIsDir
	}

	return fileHandle, nil
}

// ReadFile reads entire file content
func (w *Workspace) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := validateWorkspaceName(name); err != nil {
		return nil, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return nil, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return nil, err
	}

	fileHandle, err := w.rootHandle.Open(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	defer fileHandle.Close()

	info, err := fileHandle.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrIsDir
	}

	return io.ReadAll(fileHandle)
}

// WriteFile writes data to a file, creating parent directories as needed
func (w *Workspace) WriteFile(ctx context.Context, name string, data []byte) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)
	parentName := workspaceParentRelativeName(name)

	// Ensure parent directory exists
	if err := w.rootHandle.MkdirAll(parentName, 0755); err != nil {
		return w.mapWorkspaceCreatePathError(filepath.Dir(fullPath), err)
	}

	// Atomic write: write to temp file then rename
	tmpFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, parentName)
	if err != nil {
		return err
	}
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}

	_, writeErr := tmpFile.Write(data)
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()

	if writeErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, writeErr)
	}
	if syncErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, closeErr)
	}

	if err := w.rootHandle.Rename(tmpPath, rootName); err != nil {
		if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, mappedErr)
		}
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// WriteFileFromReader writes data from a reader to a file
func (w *Workspace) WriteFileFromReader(ctx context.Context, name string, r io.Reader) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)
	parentName := workspaceParentRelativeName(name)

	// Ensure parent directory exists
	if err := w.rootHandle.MkdirAll(parentName, 0755); err != nil {
		return w.mapWorkspaceCreatePathError(filepath.Dir(fullPath), err)
	}

	// Atomic write: write to temp file then rename
	tmpFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, parentName)
	if err != nil {
		return err
	}
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}

	_, copyErr := copyWorkspaceData(ctx, tmpFile, r)
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()

	if copyErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, copyErr)
	}
	if syncErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, closeErr)
	}

	if err := w.rootHandle.Rename(tmpPath, rootName); err != nil {
		if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, mappedErr)
		}
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// Mkdir creates a directory
func (w *Workspace) Mkdir(ctx context.Context, name string) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	// Check if already exists
	info, err := w.rootHandle.Stat(rootName)
	if err == nil {
		if info.IsDir() {
			return ErrAlreadyExists
		}
		return ErrNotDir
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return ErrNotDir
	}
	if isWorkspaceRootEscapeError(err) {
		return ErrNotFound
	}

	createdDirs, err := collectMissingWorkspaceDirs(fullPath)
	if err != nil {
		return err
	}

	if err := w.rootHandle.MkdirAll(rootName, 0755); err != nil {
		return w.mapWorkspaceCreatePathError(fullPath, err)
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}

// Delete removes a file or empty directory
func (w *Workspace) Delete(ctx context.Context, name string) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	info, err := w.rootHandle.Stat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}

	if info.IsDir() {
		if err := w.rootHandle.Remove(rootName); err != nil {
			return mapWorkspaceRootPathError(err)
		}
		if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
			return fmt.Errorf("failed to sync directory: %w", err)
		}
		return nil
	}

	if err := w.rootHandle.Remove(rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// DeleteAll removes a file or directory recursively
func (w *Workspace) DeleteAll(ctx context.Context, name string) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	rootName := workspaceRootRelativeName(name)

	_, err := w.rootHandle.Stat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}

	if err := w.rootHandle.RemoveAll(rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// Rename moves or renames a file or directory
func (w *Workspace) Rename(ctx context.Context, oldName, newName string) error {
	if err := validateWorkspaceName(oldName); err != nil {
		return err
	}
	if err := validateWorkspaceName(newName); err != nil {
		return err
	}
	oldPath := w.FullPath(oldName)
	newPath := w.FullPath(newName)
	if err := w.validatePath(oldPath); err != nil {
		return err
	}
	if err := w.validatePath(newPath); err != nil {
		return err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	oldRootName := workspaceRootRelativeName(oldName)
	newRootName := workspaceRootRelativeName(newName)

	// Check source exists
	_, err := w.rootHandle.Stat(oldRootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}

	if _, err := w.rootHandle.Stat(newRootName); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) && !isWorkspaceRootEscapeError(err) {
		return err
	}

	parentInfo, err := w.rootHandle.Stat(workspaceParentRelativeName(newName))
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}

	if err := w.rootHandle.Rename(oldRootName, newRootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceRenameDirs(oldPath, newPath); err != nil {
		if rollbackErr := w.rootHandle.Rename(newRootName, oldRootName); rollbackErr != nil {
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
	if err := validateWorkspaceName(srcName); err != nil {
		return err
	}
	if err := validateWorkspaceName(dstName); err != nil {
		return err
	}
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
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	srcRootName := workspaceRootRelativeName(srcName)
	dstRootName := workspaceRootRelativeName(dstName)
	dstParentName := workspaceParentRelativeName(dstName)

	// Check source exists and is a file
	srcFile, err := w.rootHandle.Open(srcRootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}
	if srcInfo.IsDir() {
		return ErrIsDir
	}

	parentInfo, err := w.rootHandle.Stat(dstParentName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if !parentInfo.IsDir() {
		return ErrNotDir
	}

	// Copy file
	dstFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, dstParentName)
	if err != nil {
		return err
	}
	if err := dstFile.Chmod(0644); err != nil {
		_ = dstFile.Close()
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}

	_, copyErr := copyWorkspaceData(ctx, dstFile, srcFile)
	syncErr := dstFile.Sync()
	closeErr := dstFile.Close()

	if copyErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, copyErr)
	}
	if syncErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, syncErr)
	}
	if closeErr != nil {
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, closeErr)
	}

	if err := w.rootHandle.Link(tmpPath, dstRootName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, ErrAlreadyExists)
		}
		if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
			return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, mappedErr)
		}
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err)
	}
	if err := w.rootHandle.Remove(tmpPath); err != nil {
		rollbackErr := w.rootHandle.Remove(dstRootName)
		if rollbackErr != nil && !errors.Is(rollbackErr, os.ErrNotExist) {
			return errors.Join(
				fmt.Errorf("failed to finalize copied file: %w", err),
				fmt.Errorf("failed to rollback copied file: %w", rollbackErr),
			)
		}
		return cleanupWorkspaceTempPath(w.rootHandle, tmpPath, fmt.Errorf("failed to finalize copied file: %w", err))
	}
	if err := syncWorkspaceDir(filepath.Dir(dstPath)); err != nil {
		if rollbackErr := w.rootHandle.Remove(dstRootName); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync parent directory: %w", err),
				fmt.Errorf("failed to rollback copied file: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncWorkspaceDir(filepath.Dir(dstPath)); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync parent directory: %w", err),
				fmt.Errorf("failed to sync rollback copy removal: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync parent directory: %w", err)
	}

	return nil
}

// WalkFunc is the type of function called by Walk
type WalkFunc func(path string, info *FileInfo) error

// Walk walks the file tree rooted at root, calling fn for each file or directory
func (w *Workspace) Walk(ctx context.Context, root string, fn WalkFunc) error {
	if err := validateWorkspaceName(root); err != nil {
		return err
	}
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
	if err := validateWorkspaceName(name); err != nil {
		return false
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return false
	}
	_, err := w.rootHandle.Stat(workspaceRootRelativeName(name))
	return err == nil
}

// IsDir checks if a path is a directory
func (w *Workspace) IsDir(ctx context.Context, name string) bool {
	if err := validateWorkspaceName(name); err != nil {
		return false
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return false
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return false
	}
	info, err := w.rootHandle.Stat(workspaceRootRelativeName(name))
	return err == nil && info.IsDir()
}
