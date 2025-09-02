//go:build !unix

package rootio

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// OpenFileNoFollow opens name relative to root without following a symlink
// observed at the final component. Platforms without openat support retain a
// best-effort fallback.
func OpenFileNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	if err := checkPathNoFollow(root, name, flag&os.O_CREATE != 0, false); err != nil {
		return nil, err
	}
	return root.OpenFile(name, flag, perm)
}

// OpenDirNoFollow opens name as a directory relative to root without following
// a symlink observed at the final component.
func OpenDirNoFollow(root *os.Root, name string) (*os.File, error) {
	if err := checkPathNoFollow(root, name, false, true); err != nil {
		return nil, err
	}
	return root.Open(name)
}

// MkdirNoFollow creates a single directory relative to root. Platforms without
// openat support retain a best-effort fallback.
func MkdirNoFollow(root *os.Root, name string, perm os.FileMode) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("mkdir", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return rootPathError("mkdir", name, os.ErrExist)
	}

	current := "."
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return symlinkPathError("mkdir", name)
		}
		if !info.IsDir() {
			return rootPathError("mkdir", name, syscall.ENOTDIR)
		}
	}
	cleanName := filepath.Join(parts...)
	if err := root.Mkdir(cleanName, perm); err != nil {
		if os.IsExist(err) {
			info, statErr := root.Lstat(cleanName)
			if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return symlinkPathError("mkdir", name)
			}
		}
		return err
	}
	return nil
}

func checkPathNoFollow(root *os.Root, name string, allowMissingLeaf, leafMustBeDir bool) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("lstat", name, err)
	}

	current := "."
	for index, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			if allowMissingLeaf && index == len(parts)-1 && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return symlinkPathError("open", name)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return rootPathError("open", name, syscall.ENOTDIR)
		}
		if index == len(parts)-1 && leafMustBeDir && !info.IsDir() {
			return rootPathError("open", name, syscall.ENOTDIR)
		}
	}

	return nil
}

// MkdirAllNoFollow creates name and missing parents relative to root. Platforms
// without openat support retain a best-effort fallback.
func MkdirAllNoFollow(root *os.Root, name string, perm os.FileMode) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("mkdir", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return nil
	}

	current := "."
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return symlinkPathError("mkdir", name)
			}
			if !info.IsDir() {
				return rootPathError("mkdir", name, syscall.ENOTDIR)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := root.Mkdir(current, perm); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}
