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
	errEscape  = errors.New("root path escapes root")
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

	for _, part := range strings.Split(name, string(filepath.Separator)) {
		if part == ".." {
			return nil, errEscape
		}
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
