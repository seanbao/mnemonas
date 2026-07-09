// Package rootio provides stricter file operations for os.Root.
package rootio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrSymlink reports that a Root-relative path resolved through a symlink.
	ErrSymlink = errors.New("root path resolves through a symlink")
	// ErrEntryChanged reports that a checked directory entry no longer names
	// the object opened for verification.
	ErrEntryChanged = errors.New("root path entry changed")
	errEscape       = errors.New("root path escapes root")
)

// IsSymlinkError reports whether err was returned because a no-follow root
// operation encountered a symlink.
func IsSymlinkError(err error) bool {
	return errors.Is(err, ErrSymlink)
}

func splitRelativeName(name string) ([]string, error) {
	if name == "" || filepath.IsAbs(name) {
		return nil, errEscape
	}

	if hasParentSegment(name) {
		return nil, errEscape
	}

	cleanName := filepath.Clean(name)
	parts := strings.Split(cleanName, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == ".." {
			return nil, errEscape
		}
	}
	return parts, nil
}

func splitRelativeParent(name string) (string, string, error) {
	parts, err := splitRelativeName(name)
	if err != nil {
		return "", "", err
	}
	base := parts[len(parts)-1]
	if base == "." || base == ".." {
		return "", "", errEscape
	}
	if len(parts) == 1 {
		return "", base, nil
	}
	return filepath.Join(parts[:len(parts)-1]...), base, nil
}

func hasParentSegment(path string) bool {
	for _, part := range strings.FieldsFunc(path, isPathSeparator) {
		if part == ".." {
			return true
		}
	}
	return false
}

func isPathSeparator(r rune) bool {
	if r == filepath.Separator {
		return true
	}
	return filepath.Separator == '\\' && r == '/'
}

func symlinkPathError(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: ErrSymlink}
}

func rootPathError(op, name string, err error) error {
	return &os.PathError{Op: op, Path: name, Err: err}
}

// MkdirAllPathNoFollow creates path and missing parents without following
// symlinks in any path component.
func MkdirAllPathNoFollow(path string, perm os.FileMode) error {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return err
	}
	if relPath == "" {
		return nil
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	return MkdirAllNoFollow(root, relPath, perm)
}

// MkdirAllPathNoFollowTracked creates path and missing parents without
// following symlinks in any path component. It returns only directories created
// by this call, deepest first for rollback.
func MkdirAllPathNoFollowTracked(path string, perm os.FileMode) ([]string, error) {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return nil, err
	}
	if relPath == "" {
		return nil, nil
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	createdRel, err := MkdirAllNoFollowTracked(root, relPath, perm)
	created := make([]string, len(createdRel))
	for i, rel := range createdRel {
		created[i] = filepath.Join(rootPath, rel)
	}
	return created, err
}

// MkdirPathNoFollow creates one directory without following symlinks in any
// parent path component.
func MkdirPathNoFollow(path string, perm os.FileMode) error {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return err
	}
	if relPath == "" {
		return rootPathError("mkdir", path, os.ErrExist)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	return MkdirNoFollow(root, relPath, perm)
}

// ReplaceEmptyDirPathNoFollow renames oldPath to newPath after removing
// newPath when it is an empty directory. Both paths must be siblings. Symlinks
// in parent components or at newPath are rejected.
func ReplaceEmptyDirPathNoFollow(oldPath, newPath string) error {
	oldClean := filepath.Clean(oldPath)
	newClean := filepath.Clean(newPath)
	if oldClean == newClean || filepath.Dir(oldClean) != filepath.Dir(newClean) {
		return rootPathError("rename", newPath, errEscape)
	}

	oldName := filepath.Base(oldClean)
	newName := filepath.Base(newClean)
	if oldName == "." || oldName == ".." || newName == "." || newName == ".." {
		return rootPathError("rename", newPath, errEscape)
	}

	rootPath, relParent, err := splitHostPath(filepath.Dir(newClean))
	if err != nil {
		return err
	}
	return replaceEmptyDirPathNoFollow(rootPath, relParent, oldName, newName, oldClean, newClean)
}

// ReplaceFilePathNoFollow renames oldPath to newPath after rejecting symlinks
// in parent components and at either file path. Both paths must be siblings.
func ReplaceFilePathNoFollow(oldPath, newPath string) error {
	oldClean := filepath.Clean(oldPath)
	newClean := filepath.Clean(newPath)
	if oldClean == newClean || filepath.Dir(oldClean) != filepath.Dir(newClean) {
		return rootPathError("rename", newPath, errEscape)
	}

	oldName := filepath.Base(oldClean)
	newName := filepath.Base(newClean)
	if oldName == "." || oldName == ".." || newName == "." || newName == ".." {
		return rootPathError("rename", newPath, errEscape)
	}

	rootPath, relParent, err := splitHostPath(filepath.Dir(newClean))
	if err != nil {
		return err
	}
	return replaceFilePathNoFollow(rootPath, relParent, oldName, newName, oldClean, newClean)
}

// RenamePathIntoDirNoFollow renames sourcePath to targetName inside targetDir
// without following symlinks in either parent path or at the source/target leaf.
func RenamePathIntoDirNoFollow(sourcePath, targetDir, targetName string) error {
	if sourcePath == "" || targetDir == "" || targetName == "" ||
		hasParentSegment(sourcePath) || hasParentSegment(targetDir) || hasParentSegment(targetName) ||
		filepath.IsAbs(targetName) || strings.IndexFunc(targetName, isPathSeparator) >= 0 {
		return rootPathError("rename", filepath.Join(targetDir, targetName), errEscape)
	}

	sourceClean := filepath.Clean(sourcePath)
	targetDirClean := filepath.Clean(targetDir)
	targetName = filepath.Clean(targetName)
	if targetName == "." || targetName == ".." {
		return rootPathError("rename", filepath.Join(targetDirClean, targetName), errEscape)
	}

	sourceName := filepath.Base(sourceClean)
	if sourceName == "." || sourceName == ".." {
		return rootPathError("rename", sourcePath, errEscape)
	}

	sourceRootPath, sourceRelParent, err := splitHostPath(filepath.Dir(sourceClean))
	if err != nil {
		return err
	}
	targetRootPath, targetRelDir, err := splitHostPath(targetDirClean)
	if err != nil {
		return err
	}
	targetDisplay := filepath.Join(targetDirClean, targetName)
	return renamePathIntoDirNoFollow(
		sourceRootPath,
		sourceRelParent,
		sourceName,
		targetRootPath,
		targetRelDir,
		targetName,
		sourceClean,
		targetDisplay,
	)
}

// RemoveAllPathNoFollow removes path and all children without following
// symlinks in any parent path component. Symlink entries inside the tree are
// removed as links.
func RemoveAllPathNoFollow(path string) error {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return err
	}
	if relPath == "" {
		return rootPathError("removeall", path, errEscape)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	return RemoveAllNoFollow(root, relPath)
}

// MkdirAllNoFollowTracked creates name and missing parents relative to root
// without following symlinks in any path component. It returns only directories
// created by this call, deepest first for rollback.
func MkdirAllNoFollowTracked(root *os.Root, name string, perm os.FileMode) ([]string, error) {
	parts, err := splitRelativeName(name)
	if err != nil {
		return nil, rootPathError("mkdir", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return nil, nil
	}

	created := make([]string, 0)
	current := ""
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := MkdirNoFollow(root, current, perm); err == nil {
			created = append([]string{current}, created...)
			continue
		} else if !errors.Is(err, os.ErrExist) {
			return created, err
		}

		dir, err := OpenDirNoFollow(root, current)
		if err != nil {
			return created, err
		}
		if closeErr := dir.Close(); closeErr != nil {
			return created, closeErr
		}
	}

	return created, nil
}

// OpenFilePathNoFollow opens path without following symlinks in any path
// component.
func OpenFilePathNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return nil, err
	}
	if relPath == "" {
		return nil, rootPathError("open", path, errEscape)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return OpenFileNoFollow(root, relPath, flag, perm)
}

// OpenRegularFilePathNoFollow opens a regular file for reading without
// following symlinks in any path component. Special files are rejected without
// waiting for a writer on platforms that support non-blocking opens.
func OpenRegularFilePathNoFollow(path string) (*os.File, error) {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return nil, err
	}
	if relPath == "" {
		return nil, rootPathError("open", path, errEscape)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return OpenRegularFileNoFollow(root, relPath)
}

// OpenDirPathNoFollow opens a host path as a directory without following
// symlinks in any path component.
func OpenDirPathNoFollow(path string) (*os.File, error) {
	rootPath, relPath, err := splitHostPath(path)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	if relPath == "" {
		return root.Open(".")
	}
	return OpenDirNoFollow(root, relPath)
}

func splitHostPath(path string) (string, string, error) {
	if path == "" || hasParentSegment(path) {
		return "", "", rootPathError("path", path, errEscape)
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			return "", "", err
		}
		cleanPath = absPath
	}

	rootPath := filepath.VolumeName(cleanPath) + string(filepath.Separator)
	relPath := strings.TrimPrefix(cleanPath, rootPath)
	return rootPath, relPath, nil
}
