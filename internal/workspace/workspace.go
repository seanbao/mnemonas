// Package workspace provides native file system operations for MnemoNAS
package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var readDirEntryInfo = func(root *os.Root, name string, entry os.DirEntry) (os.FileInfo, error) {
	return root.Lstat(name)
}

var copyWorkspaceData = copyWithContext
var finalizeWorkspaceCopyTemp = func(root *os.Root, tmpPath string) error {
	return root.Remove(tmpPath)
}
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

func cleanupWorkspaceCreatedDirs(rootPath string, root *os.Root, createdDirs []string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		relDir, err := filepath.Rel(rootPath, dir)
		if err != nil {
			return errors.Join(rollbackErr, fmt.Errorf("cleanup created directory %s: %w", dir, err))
		}
		if relDir == "." {
			continue
		}
		if removeErr := root.Remove(relDir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created directory %s: %w", dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncWorkspaceParentDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
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

func syncCreatedWorkspaceDirs(createdDirs []string) error {
	return syncCreatedWorkspaceDirsWith(createdDirs, syncWorkspaceDir)
}

func syncCreatedWorkspaceDirsWith(createdDirs []string, syncDir func(string) error) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncDir(filepath.Dir(createdDirs[i])); err != nil {
			return err
		}
	}
	return nil
}

func ensureWorkspaceDirsTracked(rootPath string, root *os.Root, relDir string, perm os.FileMode) ([]string, error) {
	cleanRel := path.Clean(strings.ReplaceAll(relDir, "\\", "/"))
	if cleanRel == "." || cleanRel == "/" {
		return nil, nil
	}
	if strings.HasPrefix(cleanRel, "../") || cleanRel == ".." {
		return nil, ErrNotFound
	}

	createdDirs := make([]string, 0)
	currentRel := "."
	for _, part := range strings.Split(cleanRel, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil, cleanupWorkspaceCreatedDirs(rootPath, root, createdDirs, ErrNotFound)
		}
		if currentRel == "." {
			currentRel = part
		} else {
			currentRel = path.Join(currentRel, part)
		}

		info, err := root.Lstat(currentRel)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, cleanupWorkspaceCreatedDirs(rootPath, root, createdDirs, ErrNotFound)
			}
			if !info.IsDir() {
				return nil, cleanupWorkspaceCreatedDirs(rootPath, root, createdDirs, ErrNotDir)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			mappedErr := mapWorkspaceRootPathError(err)
			return nil, cleanupWorkspaceCreatedDirs(rootPath, root, createdDirs, mappedErr)
		}

		if err := rootio.MkdirNoFollow(root, currentRel, perm); err != nil {
			if errors.Is(err, os.ErrExist) {
				info, statErr := root.Lstat(currentRel)
				if statErr == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
					continue
				}
				if statErr == nil {
					if info.Mode()&os.ModeSymlink != 0 {
						err = ErrNotFound
					} else {
						err = ErrNotDir
					}
				} else {
					err = statErr
				}
			}
			return nil, cleanupWorkspaceCreatedDirs(rootPath, root, createdDirs, mapWorkspaceRootPathError(err))
		}

		absDir := filepath.Join(rootPath, filepath.FromSlash(currentRel))
		createdDirs = append([]string{absDir}, createdDirs...)
	}

	return createdDirs, nil
}

func normalizeWorkspaceRootPath(root string) (string, error) {
	cleaned := filepath.Clean(root)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}

	absRoot, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return absRoot, nil
}

func validateWorkspaceRootComponent(path string) error {
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
		return errWorkspaceRootSymlink
	}
	if !info.IsDir() {
		return ErrNotDir
	}
	return nil
}

func validateWorkspaceRootPath(root string) error {
	current := filepath.VolumeName(root) + string(filepath.Separator)
	trimmed := strings.TrimPrefix(root, current)
	if trimmed == "" {
		return validateWorkspaceRootComponent(root)
	}

	if err := validateWorkspaceRootComponent(current); err != nil {
		return err
	}
	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := validateWorkspaceRootComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func ensureWorkspaceRoot(root string) (string, *os.Root, error) {
	normalizedRoot, err := normalizeWorkspaceRootPath(root)
	if err != nil {
		return "", nil, err
	}
	if err := validateWorkspaceRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}

	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(normalizedRoot, 0755)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return "", nil, errWorkspaceRootSymlink
		}
		return "", nil, err
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return "", nil, fmt.Errorf("failed to sync directory: %w", err)
	}
	if err := validateWorkspaceRootPath(normalizedRoot); err != nil {
		return "", nil, err
	}

	rootHandle, err := os.OpenRoot(normalizedRoot)
	if err != nil {
		return "", nil, err
	}
	return normalizedRoot, rootHandle, nil
}

// Common errors
var (
	ErrNotFound             = errors.New("not found")
	ErrAlreadyExists        = errors.New("already exists")
	ErrNotDir               = errors.New("not a directory")
	ErrIsDir                = errors.New("is a directory")
	ErrFileTooLarge         = errors.New("file too large")
	errWorkspaceRootSymlink = errors.New("workspace root must not be a symlink")
)

type WriteFileOptions struct {
	MaxBytes    int64
	SyncParent  func(string) error
	CreatedDirs *[]string
}

// VisibleMutationWarningError reports that a workspace mutation is already
// externally visible, but the final directory fsync did not complete.
type VisibleMutationWarningError struct {
	err error
}

func (e *VisibleMutationWarningError) Error() string {
	return e.err.Error()
}

func (e *VisibleMutationWarningError) Unwrap() error {
	return e.err
}

func WrapVisibleMutationWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *VisibleMutationWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &VisibleMutationWarningError{err: err}
}

func IsVisibleMutationWarning(err error) bool {
	var warningErr *VisibleMutationWarningError
	return errors.As(err, &warningErr)
}

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
	absRoot, rootHandle, err := ensureWorkspaceRoot(root)
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

func childWorkspaceRootName(parent, child string) string {
	if parent == "." {
		return child
	}
	return path.Join(parent, child)
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
	if isWorkspaceRootEscapeError(err) || rootio.IsSymlinkError(err) || errors.Is(err, os.ErrNotExist) {
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
		tempFile, err := rootio.OpenFileNoFollow(root, tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
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
	if err := w.rootHandle.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
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

func validateWorkspaceName(name string) error {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return ErrNotFound
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return ErrNotFound
		}
	}
	return nil
}

func validateWorkspaceMutationName(name string) error {
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	if CleanPath(name) == "/" {
		return ErrNotFound
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

	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	if err != nil {
		return nil, mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrNotFound
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

	dirHandle, err := rootio.OpenDirNoFollow(w.rootHandle, workspaceRootRelativeName(name))
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
		childRootName := childWorkspaceRootName(workspaceRootRelativeName(name), e.Name())
		info, err := readDirEntryInfo(w.rootHandle, childRootName, e)
		if err != nil {
			return nil, mapWorkspaceRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		childPath := path.Join(CleanPath(name), e.Name())
		result = append(result, &FileInfo{
			Path:    childPath,
			Name:    e.Name(),
			IsDir:   info.IsDir(),
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

	fileHandle, err := rootio.OpenFileNoFollow(w.rootHandle, workspaceRootRelativeName(name), os.O_RDONLY, 0)
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

	fileHandle, err := rootio.OpenFileNoFollow(w.rootHandle, workspaceRootRelativeName(name), os.O_RDONLY, 0)
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
	_, err := w.WriteFileFromReaderWithOptions(ctx, name, bytes.NewReader(data), WriteFileOptions{})
	return err
}

func (w *Workspace) WriteFileFromReaderWithOptions(ctx context.Context, name string, r io.Reader, options WriteFileOptions) (int64, error) {
	if err := validateWorkspaceName(name); err != nil {
		return 0, err
	}
	fullPath := w.FullPath(name)
	if err := w.validatePath(fullPath); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := afterValidateWorkspacePaths(); err != nil {
		return 0, err
	}

	rootName := workspaceRootRelativeName(name)
	parentName := workspaceParentRelativeName(name)

	// Ensure parent directory exists
	createdDirs, err := ensureWorkspaceDirsTracked(w.root, w.rootHandle, parentName, 0755)
	if err != nil {
		return 0, w.mapWorkspaceCreatePathError(filepath.Dir(fullPath), err)
	}
	if options.CreatedDirs != nil {
		*options.CreatedDirs = append((*options.CreatedDirs)[:0], createdDirs...)
	}

	// Atomic write: write to temp file then rename
	tmpFile, tmpPath, err := createWorkspaceTempFile(w.rootHandle, parentName)
	if err != nil {
		return 0, err
	}
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return 0, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err))
	}

	copyReader := r
	if options.MaxBytes > 0 {
		copyReader = &io.LimitedReader{R: r, N: options.MaxBytes + 1}
	}

	written, writeErr := copyWorkspaceData(ctx, tmpFile, copyReader)
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()

	if writeErr != nil {
		return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, writeErr))
	}
	if syncErr != nil {
		return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, syncErr))
	}
	if closeErr != nil {
		return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, closeErr))
	}
	if options.MaxBytes > 0 && written > options.MaxBytes {
		return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, ErrFileTooLarge))
	}

	if err := w.rootHandle.Rename(tmpPath, rootName); err != nil {
		if mappedErr := mapWorkspaceRootPathError(err); mappedErr != err {
			return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, mappedErr))
		}
		return written, cleanupWorkspaceCreatedDirs(w.root, w.rootHandle, createdDirs, cleanupWorkspaceTempPath(w.rootHandle, tmpPath, err))
	}
	syncParent := options.SyncParent
	if syncParent == nil {
		syncParent = syncWorkspaceDir
	}
	if err := syncParent(filepath.Dir(fullPath)); err != nil {
		return written, WrapVisibleMutationWarning(fmt.Errorf("sync parent directory: %w", err))
	}
	if err := syncCreatedWorkspaceDirsWith(createdDirs, syncParent); err != nil {
		return written, WrapVisibleMutationWarning(fmt.Errorf("sync created directory tree: %w", err))
	}

	return written, nil
}

// WriteFileFromReader writes data from a reader to a file
func (w *Workspace) WriteFileFromReader(ctx context.Context, name string, r io.Reader) error {
	_, err := w.WriteFileFromReaderWithOptions(ctx, name, r, WriteFileOptions{})
	return err
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
	info, err := w.rootHandle.Lstat(rootName)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrNotFound
		}
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

	createdRelDirs, err := rootio.MkdirAllNoFollowTracked(w.rootHandle, rootName, 0755)
	if err != nil {
		return w.mapWorkspaceCreatePathError(fullPath, err)
	}
	createdDirs := make([]string, len(createdRelDirs))
	for i, relDir := range createdRelDirs {
		createdDirs[i] = filepath.Join(w.root, relDir)
	}
	if err := syncCreatedWorkspaceDirs(createdDirs); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}

	return nil
}

// Delete removes a file or empty directory
func (w *Workspace) Delete(ctx context.Context, name string) error {
	if err := validateWorkspaceMutationName(name); err != nil {
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

	info, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if info.IsDir() {
		if err := w.rootHandle.Remove(rootName); err != nil {
			return mapWorkspaceRootPathError(err)
		}
		if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
			return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
		}
		return nil
	}

	if err := w.rootHandle.Remove(rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}
	return nil
}

// DeleteAll removes a file or directory recursively
func (w *Workspace) DeleteAll(ctx context.Context, name string) error {
	if err := validateWorkspaceMutationName(name); err != nil {
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

	info, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if err := w.rootHandle.RemoveAll(rootName); err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if err := syncWorkspaceDir(filepath.Dir(fullPath)); err != nil {
		return WrapVisibleMutationWarning(fmt.Errorf("failed to sync directory: %w", err))
	}
	return nil
}

// Rename moves or renames a file or directory
func (w *Workspace) Rename(ctx context.Context, oldName, newName string) error {
	if err := validateWorkspaceMutationName(oldName); err != nil {
		return err
	}
	if err := validateWorkspaceMutationName(newName); err != nil {
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
	oldInfo, err := w.rootHandle.Lstat(oldRootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if oldInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	if newInfo, err := w.rootHandle.Lstat(newRootName); err == nil {
		if newInfo.Mode()&os.ModeSymlink != 0 {
			return ErrNotFound
		}
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) && !isWorkspaceRootEscapeError(err) {
		return err
	}

	parentInfo, err := w.rootHandle.Lstat(workspaceParentRelativeName(newName))
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
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
	if err := validateWorkspaceMutationName(srcName); err != nil {
		return err
	}
	if err := validateWorkspaceMutationName(dstName); err != nil {
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
	srcFile, err := rootio.OpenFileNoFollow(w.rootHandle, srcRootName, os.O_RDONLY, 0)
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

	parentInfo, err := w.rootHandle.Lstat(dstParentName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
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
	if err := finalizeWorkspaceCopyTemp(w.rootHandle, tmpPath); err != nil {
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

type workspaceWalkInternalFunc func(rootName, cleanPath string, info os.FileInfo) error

func (w *Workspace) walkWithRootHandle(ctx context.Context, rootName, cleanPath string, fn workspaceWalkInternalFunc) error {
	entryInfo, err := w.rootHandle.Lstat(rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	if entryInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotFound
	}

	return w.walkWorkspaceEntry(ctx, rootName, cleanPath, entryInfo, fn)
}

func (w *Workspace) walkWorkspaceEntry(ctx context.Context, rootName, cleanPath string, info os.FileInfo, fn workspaceWalkInternalFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(rootName, cleanPath, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	dirHandle, err := rootio.OpenDirNoFollow(w.rootHandle, rootName)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	defer dirHandle.Close()

	entries, err := dirHandle.ReadDir(-1)
	if err != nil {
		return mapWorkspaceRootPathError(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		childRootName := childWorkspaceRootName(rootName, entry.Name())
		entryInfo, err := readDirEntryInfo(w.rootHandle, childRootName, entry)
		if err != nil {
			return mapWorkspaceRootPathError(err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}

		childCleanPath := path.Join(cleanPath, entry.Name())
		if err := w.walkWorkspaceEntry(ctx, childRootName, childCleanPath, entryInfo, fn); err != nil {
			return err
		}
	}

	return nil
}

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
	if err := afterValidateWorkspacePaths(); err != nil {
		return err
	}

	return w.walkWithRootHandle(ctx, workspaceRootRelativeName(root), rootClean, func(_ string, cleanPath string, info os.FileInfo) error {
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
	if err := afterValidateWorkspacePaths(); err != nil {
		return 0, 0, err
	}

	type stagingFile struct {
		rootName string
		size     int64
	}
	stagingFiles := make([]stagingFile, 0)
	err = w.walkWithRootHandle(ctx, ".", "/", func(rootName, _ string, info os.FileInfo) error {
		if info.IsDir() || !isWorkspaceStagingFile(info.Name()) {
			return nil
		}
		stagingFiles = append(stagingFiles, stagingFile{rootName: rootName, size: info.Size()})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	for _, stagingFile := range stagingFiles {
		if err := ctx.Err(); err != nil {
			return files, bytes, err
		}
		if rmErr := w.rootHandle.Remove(stagingFile.rootName); rmErr == nil {
			files++
			bytes += stagingFile.size
		}
	}

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
	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	return err == nil && info.Mode()&os.ModeSymlink == 0
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
	info, err := w.rootHandle.Lstat(workspaceRootRelativeName(name))
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir()
}
