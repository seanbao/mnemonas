//go:build unix

package rootio

import (
	"os"

	"golang.org/x/sys/unix"
)

// OpenFileNoFollow opens name relative to root without following symlinks in
// any path component.
func OpenFileNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	return openNoFollow(root, name, flag, perm)
}

// OpenDirNoFollow opens name as a directory relative to root without following
// symlinks in any path component.
func OpenDirNoFollow(root *os.Root, name string) (*os.File, error) {
	return openNoFollow(root, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
}

// MkdirNoFollow creates a single directory relative to root without following
// symlinks in any parent path component.
func MkdirNoFollow(root *os.Root, name string, perm os.FileMode) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("mkdirat", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return rootPathError("mkdirat", name, os.ErrExist)
	}

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer rootDir.Close()

	rootFD := int(rootDir.Fd())
	parentFD := rootFD
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return noFollowPathError("openat", name, parentFD, part, err)
		}
		if closeParent {
			_ = unix.Close(parentFD)
		}
		parentFD = nextFD
		closeParent = true
	}

	if err := unix.Mkdirat(parentFD, parts[len(parts)-1], uint32(perm.Perm())); err != nil {
		return noFollowPathError("mkdirat", name, parentFD, parts[len(parts)-1], err)
	}
	return nil
}

// MkdirAllNoFollow creates name and missing parents relative to root without
// following symlinks in any path component.
func MkdirAllNoFollow(root *os.Root, name string, perm os.FileMode) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("mkdirat", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return nil
	}

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer rootDir.Close()

	rootFD := int(rootDir.Fd())
	parentFD := rootFD
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	for _, part := range parts {
		if part == "." {
			continue
		}
		if err := unix.Mkdirat(parentFD, part, uint32(perm.Perm())); err != nil && err != unix.EEXIST {
			return noFollowPathError("mkdirat", name, parentFD, part, err)
		}
		nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return noFollowPathError("openat", name, parentFD, part, err)
		}
		if closeParent {
			_ = unix.Close(parentFD)
		}
		parentFD = nextFD
		closeParent = true
	}

	return nil
}

func openNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	parts, err := splitRelativeName(name)
	if err != nil {
		return nil, rootPathError("openat", name, err)
	}

	rootDir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer rootDir.Close()

	rootFD := int(rootDir.Fd())
	parentFD := rootFD
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, noFollowPathError("openat", name, parentFD, part, err)
		}
		if closeParent {
			_ = unix.Close(parentFD)
		}
		parentFD = nextFD
		closeParent = true
	}

	fd, err := unix.Openat(parentFD, parts[len(parts)-1], flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm))
	if err != nil {
		return nil, noFollowPathError("openat", name, parentFD, parts[len(parts)-1], err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func noFollowPathError(op, fullName string, parentFD int, name string, err error) error {
	if err == unix.ELOOP || ((err == unix.ENOTDIR || err == unix.EEXIST) && isSymlinkAt(parentFD, name)) {
		return symlinkPathError(op, fullName)
	}
	return rootPathError(op, fullName, err)
}

func isSymlinkAt(parentFD int, name string) bool {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFLNK
}
