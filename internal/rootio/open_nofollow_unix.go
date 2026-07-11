//go:build unix

package rootio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var beforeCheckedRemovalIsolation = func(string) error { return nil }

// OpenFileNoFollow opens name relative to root without following symlinks in
// any path component.
func OpenFileNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	return openNoFollow(root, name, flag, perm)
}

// OpenRegularFileNoFollow opens a regular file for reading without blocking on
// FIFOs or other special files.
func OpenRegularFileNoFollow(root *os.Root, name string) (*os.File, error) {
	file, err := openNoFollow(root, name, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, rootPathError("openat", name, unix.EINVAL)
	}
	return file, nil
}

// OpenDirNoFollow opens name as a directory relative to root without following
// symlinks in any path component.
func OpenDirNoFollow(root *os.Root, name string) (*os.File, error) {
	return openNoFollow(root, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
}

// RenameLeafNoReplace atomically renames a leaf entry relative to root without
// following symlinks in either parent path and without replacing an existing
// target. The source leaf itself is moved as a directory entry, so it may be a
// symlink or another special file.
func RenameLeafNoReplace(root *os.Root, sourceName, targetName string) error {
	return RenameLeafBetweenRootsNoReplace(root, sourceName, root, targetName)
}

// RenameLeafBetweenRootsNoReplace atomically renames a leaf entry between two
// open roots without following symlinks in either parent path and without
// replacing an existing target. Both entries must reside on the same file
// system; cross-device errors are returned unchanged.
// The source leaf itself is moved as a directory entry, so it may be a symlink
// or another special file.
func RenameLeafBetweenRootsNoReplace(
	sourceRoot *os.Root,
	sourceName string,
	targetRoot *os.Root,
	targetName string,
) error {
	sourceParent, sourceBase, err := splitRelativeParent(sourceName)
	if err != nil {
		return rootPathError("renameat", sourceName, err)
	}
	targetParent, targetBase, err := splitRelativeParent(targetName)
	if err != nil {
		return rootPathError("renameat", targetName, err)
	}

	sourceDir, err := openDirFDFromRootNoFollow(sourceRoot, sourceParent, sourceName)
	if err != nil {
		return err
	}
	defer sourceDir.Close()

	targetDir, err := openDirFDFromRootNoFollow(targetRoot, targetParent, targetName)
	if err != nil {
		return err
	}
	defer targetDir.Close()

	if err := renameNoReplaceAt(sourceDir.fd, sourceBase, targetDir.fd, targetBase); err != nil {
		if err == unix.EXDEV {
			return err
		}
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", targetName, os.ErrExist)
		}
		if err == unix.ENOENT {
			return rootPathError("renameat", sourceName, err)
		}
		return rootPathError("renameat", targetName, err)
	}
	return nil
}

// RenameLeafIntoDirNoReplace atomically moves a leaf from root into an already
// opened directory without following the source parent path or replacing the
// target leaf.
func RenameLeafIntoDirNoReplace(sourceRoot *os.Root, sourceName string, targetDir *os.File, targetName string) error {
	if targetDir == nil {
		return rootPathError("renameat", targetName, unix.EBADF)
	}
	sourceParent, sourceBase, err := splitRelativeParent(sourceName)
	if err != nil {
		return rootPathError("renameat", sourceName, err)
	}
	parts, err := splitRelativeName(targetName)
	if err != nil || len(parts) != 1 || parts[0] == "." {
		if err == nil {
			err = errEscape
		}
		return rootPathError("renameat", targetName, err)
	}

	sourceDir, err := openDirFDFromRootNoFollow(sourceRoot, sourceParent, sourceName)
	if err != nil {
		return err
	}
	defer sourceDir.Close()

	if err := renameNoReplaceAt(sourceDir.fd, sourceBase, int(targetDir.Fd()), targetName); err != nil {
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", targetName, os.ErrExist)
		}
		if err == unix.ENOENT {
			return rootPathError("renameat", sourceName, err)
		}
		return rootPathError("renameat", targetName, err)
	}
	return nil
}

// RenameLeafFromDirNoReplace atomically moves a leaf from an already opened
// directory into root without following the target parent path or replacing
// the target leaf.
func RenameLeafFromDirNoReplace(sourceDir *os.File, sourceName string, targetRoot *os.Root, targetName string) error {
	if sourceDir == nil {
		return rootPathError("renameat", sourceName, unix.EBADF)
	}
	parts, err := splitRelativeName(sourceName)
	if err != nil || len(parts) != 1 || parts[0] == "." {
		if err == nil {
			err = errEscape
		}
		return rootPathError("renameat", sourceName, err)
	}
	targetParent, targetBase, err := splitRelativeParent(targetName)
	if err != nil {
		return rootPathError("renameat", targetName, err)
	}
	targetDir, err := openDirFDFromRootNoFollow(targetRoot, targetParent, targetName)
	if err != nil {
		return err
	}
	defer targetDir.Close()

	if err := renameNoReplaceAt(int(sourceDir.Fd()), sourceName, targetDir.fd, targetBase); err != nil {
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", targetName, os.ErrExist)
		}
		if err == unix.ENOENT {
			return rootPathError("renameat", sourceName, err)
		}
		return rootPathError("renameat", targetName, err)
	}
	return nil
}

// OpenDirEntryNoFollow opens one leaf relative to an already opened directory
// without following a symbolic link.
func OpenDirEntryNoFollow(dir *os.File, name string) (*os.File, error) {
	if dir == nil {
		return nil, rootPathError("openat", name, unix.EBADF)
	}
	parts, err := splitRelativeName(name)
	if err != nil || len(parts) != 1 || parts[0] == "." {
		if err == nil {
			err = errEscape
		}
		return nil, rootPathError("openat", name, err)
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, noFollowPathError("openat", name, int(dir.Fd()), name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

// RemoveAllFromDirNoFollowChecked removes one tree relative to an already
// opened directory. verify is called for every exact entry immediately before
// it is removed or traversed.
func RemoveAllFromDirNoFollowChecked(dir *os.File, name string, verify func(string, os.FileInfo) error) error {
	if dir == nil {
		return rootPathError("unlinkat", name, unix.EBADF)
	}
	parts, err := splitRelativeName(name)
	if err != nil || len(parts) != 1 || parts[0] == "." {
		if err == nil {
			err = errEscape
		}
		return rootPathError("unlinkat", name, err)
	}
	return removeAllAtNoFollowChecked(int(dir.Fd()), name, name, verify)
}

// RemoveAllFromDirNoFollowCheckedInPlace removes one tree relative to an
// already opened directory without first renaming entries to private isolation
// names. It is intended for crash-recovery flows that have a durable journal
// and can continue an interrupted removal. When non-nil, verify is called for
// every exact entry before mutation, and each opened entry is matched against
// its current directory entry immediately before unlink or traversal.
func RemoveAllFromDirNoFollowCheckedInPlace(dir *os.File, name string, verify func(string, os.FileInfo) error) error {
	if dir == nil {
		return rootPathError("unlinkat", name, unix.EBADF)
	}
	parts, err := splitRelativeName(name)
	if err != nil || len(parts) != 1 || parts[0] == "." {
		if err == nil {
			err = errEscape
		}
		return rootPathError("unlinkat", name, err)
	}
	return removeAllAtNoFollowCheckedInPlace(int(dir.Fd()), name, name, verify)
}

// RenameNoFollow renames sourceName to targetName relative to root without
// following symlinks in either path and without replacing an existing target.
func RenameNoFollow(root *os.Root, sourceName, targetName string) error {
	sourceParent, sourceBase, err := splitRelativeParent(sourceName)
	if err != nil {
		return rootPathError("renameat", sourceName, err)
	}
	targetParent, targetBase, err := splitRelativeParent(targetName)
	if err != nil {
		return rootPathError("renameat", targetName, err)
	}

	sourceDir, err := openDirFDFromRootNoFollow(root, sourceParent, sourceName)
	if err != nil {
		return err
	}
	defer sourceDir.Close()

	targetDir, err := openDirFDFromRootNoFollow(root, targetParent, targetName)
	if err != nil {
		return err
	}
	defer targetDir.Close()

	var stat unix.Stat_t
	if err := unix.Fstatat(sourceDir.fd, sourceBase, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return noFollowPathError("fstatat", sourceName, sourceDir.fd, sourceBase, err)
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return symlinkPathError("renameat", sourceName)
	}

	if err := unix.Fstatat(targetDir.fd, targetBase, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return symlinkPathError("renameat", targetName)
		}
		return rootPathError("renameat", targetName, os.ErrExist)
	} else if err != unix.ENOENT {
		return noFollowPathError("fstatat", targetName, targetDir.fd, targetBase, err)
	}

	if err := renameNoReplaceAt(sourceDir.fd, sourceBase, targetDir.fd, targetBase); err != nil {
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", targetName, os.ErrExist)
		}
		return rootPathError("renameat", targetName, err)
	}
	return nil
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

// RemoveAllNoFollow removes name and all children relative to root without
// following symlinks in any parent path component. Symlink entries inside the
// tree are removed as links.
func RemoveAllNoFollow(root *os.Root, name string) error {
	parts, err := splitRelativeName(name)
	if err != nil {
		return rootPathError("unlinkat", name, err)
	}
	if len(parts) == 1 && parts[0] == "." {
		return rootPathError("unlinkat", name, errEscape)
	}

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer rootDir.Close()

	parentFD := int(rootDir.Fd())
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			if err == unix.ENOENT {
				return nil
			}
			return noFollowPathError("openat", name, parentFD, part, err)
		}
		if closeParent {
			_ = unix.Close(parentFD)
		}
		parentFD = nextFD
		closeParent = true
	}

	return removeAllAtNoFollow(parentFD, parts[len(parts)-1], name)
}

func removeAllAtNoFollow(parentFD int, name, display string) error {
	return removeAllAtNoFollowChecked(parentFD, name, display, nil)
}

func removeAllAtNoFollowChecked(parentFD int, name, display string, verify func(string, os.FileInfo) error) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			if verify != nil {
				return rootPathError("fstatat", display, errors.Join(ErrEntryChanged, err))
			}
			return nil
		}
		return noFollowPathError("fstatat", display, parentFD, name, err)
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		if verify == nil {
			return finishRemoveAllFileUnlink(nil, display, false, unix.Unlinkat(parentFD, name, 0))
		}
		return removeVerifiedFileAt(parentFD, name, display, verify)
	}

	dirFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if verify != nil {
			err = checkedRemovalLookupError(err)
		}
		return noFollowPathError("openat", display, parentFD, name, err)
	}
	dirFile := os.NewFile(uintptr(dirFD), display)
	closeDir := true
	defer func() {
		if closeDir {
			_ = dirFile.Close()
		}
	}()
	var openedDirInfo os.FileInfo
	isolatedName := ""
	abortCheckedDirectoryRemoval := func(cause error) error {
		if isolatedName == "" {
			return cause
		}
		rollbackErr := rollbackCheckedRemovalIsolation(parentFD, name, isolatedName, display)
		isolatedName = ""
		return errors.Join(cause, rollbackErr)
	}
	if verify != nil {
		info, err := dirFile.Stat()
		if err != nil {
			return rootPathError("fstat", display, err)
		}
		if err := verify(display, info); err != nil {
			return err
		}
		openedDirInfo = info
		if err := verifyCurrentEntryMatchesOpened(parentFD, name, display, openedDirInfo, true, verify); err != nil {
			return err
		}
		if err := beforeCheckedRemovalIsolation(display); err != nil {
			return err
		}
		isolatedName, err = isolateCheckedRemovalAt(parentFD, name, display)
		if err != nil {
			return err
		}
		if err := verifyCurrentEntryMatchesOpened(parentFD, isolatedName, display, openedDirInfo, true, nil); err != nil {
			return abortCheckedDirectoryRemoval(err)
		}
	}
	if err := unix.Fchmod(dirFD, uint32((stat.Mode&07777)|0700)); err != nil {
		return abortCheckedDirectoryRemoval(rootPathError("chmod", display, err))
	}

	for {
		entries, err := dirFile.ReadDir(100)
		for _, entry := range entries {
			childName := entry.Name()
			if childName == "." || childName == ".." {
				continue
			}
			if err := removeAllAtNoFollowChecked(dirFD, childName, filepath.Join(display, childName), verify); err != nil {
				return abortCheckedDirectoryRemoval(err)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return abortCheckedDirectoryRemoval(rootPathError("readdir", display, err))
			}
			break
		}
		if len(entries) == 0 {
			break
		}
	}
	if verify != nil {
		if err := verifyCurrentEntryMatchesOpened(parentFD, isolatedName, display, openedDirInfo, true, nil); err != nil {
			return abortCheckedDirectoryRemoval(err)
		}
	}

	removeName := name
	if isolatedName != "" {
		removeName = isolatedName
	}
	if err := unix.Unlinkat(parentFD, removeName, unix.AT_REMOVEDIR); err != nil {
		if err == unix.ENOENT {
			if verify != nil {
				isolatedName = ""
				return rootPathError("unlinkat", checkedRemovalIsolationDisplay(display, removeName), errors.Join(ErrEntryChanged, err))
			}
			return nil
		}
		return abortCheckedDirectoryRemoval(rootPathError("unlinkat", checkedRemovalIsolationDisplay(display, removeName), checkedRemovalMutationError(err)))
	}
	isolatedName = ""
	_ = dirFile.Close()
	closeDir = false
	return nil
}

func removeAllAtNoFollowCheckedInPlace(parentFD int, name, display string, verify func(string, os.FileInfo) error) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return rootPathError("fstatat", display, errors.Join(ErrEntryChanged, err))
		}
		return noFollowPathError("fstatat", display, parentFD, name, err)
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return removeVerifiedFileAtInPlace(parentFD, name, display, verify)
	}

	dirFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return noFollowPathError("openat", display, parentFD, name, checkedRemovalLookupError(err))
	}
	dirFile := os.NewFile(uintptr(dirFD), display)
	closeDir := true
	defer func() {
		if closeDir {
			_ = dirFile.Close()
		}
	}()

	openedInfo, err := dirFile.Stat()
	if err != nil {
		return rootPathError("fstat", display, err)
	}
	if verify != nil {
		if err := verify(display, openedInfo); err != nil {
			return err
		}
	}
	if err := verifyCurrentEntryMatchesOpened(parentFD, name, display, openedInfo, true, verify); err != nil {
		return err
	}
	if err := unix.Fchmod(dirFD, uint32((stat.Mode&07777)|0700)); err != nil {
		return rootPathError("chmod", display, err)
	}

	for {
		entries, readErr := dirFile.ReadDir(100)
		for _, entry := range entries {
			childName := entry.Name()
			if childName == "." || childName == ".." {
				continue
			}
			if err := removeAllAtNoFollowCheckedInPlace(dirFD, childName, filepath.Join(display, childName), verify); err != nil {
				return err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return rootPathError("readdir", display, readErr)
			}
			break
		}
		if len(entries) == 0 {
			break
		}
	}

	if err := verifyCurrentEntryMatchesOpened(parentFD, name, display, openedInfo, true, nil); err != nil {
		return err
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		return rootPathError("unlinkat", display, checkedRemovalMutationError(err))
	}
	_ = dirFile.Close()
	closeDir = false
	return nil
}

func removeVerifiedFileAtInPlace(parentFD int, name, display string, verify func(string, os.FileInfo) error) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return noFollowPathError("openat", display, parentFD, name, checkedRemovalLookupError(err))
	}
	opened := os.NewFile(uintptr(fd), display)
	openedInfo, statErr := opened.Stat()
	if statErr != nil {
		_ = opened.Close()
		return rootPathError("fstat", display, statErr)
	}
	if verify != nil {
		if err := verify(display, openedInfo); err != nil {
			_ = opened.Close()
			return err
		}
	}
	if err := verifyCurrentEntryMatchesOpened(parentFD, name, display, openedInfo, false, verify); err != nil {
		_ = opened.Close()
		return err
	}
	unlinkErr := unix.Unlinkat(parentFD, name, 0)
	_ = opened.Close()
	if unlinkErr == nil {
		return nil
	}
	return rootPathError("unlinkat", display, checkedRemovalMutationError(unlinkErr))
}

func removeVerifiedFileAt(parentFD int, name, display string, verify func(string, os.FileInfo) error) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return noFollowPathError("openat", display, parentFD, name, checkedRemovalLookupError(err))
	}
	opened := os.NewFile(uintptr(fd), display)
	openedInfo, statErr := opened.Stat()
	if statErr != nil {
		_ = opened.Close()
		return rootPathError("fstat", display, statErr)
	}
	if err := verify(display, openedInfo); err != nil {
		_ = opened.Close()
		return err
	}
	if err := verifyCurrentEntryMatchesOpened(parentFD, name, display, openedInfo, false, verify); err != nil {
		_ = opened.Close()
		return err
	}
	if err := beforeCheckedRemovalIsolation(display); err != nil {
		_ = opened.Close()
		return err
	}

	isolatedName, err := isolateCheckedRemovalAt(parentFD, name, display)
	if err != nil {
		_ = opened.Close()
		return err
	}
	rollback := func(cause error) error {
		rollbackErr := rollbackCheckedRemovalIsolation(parentFD, name, isolatedName, display)
		_ = opened.Close()
		return errors.Join(cause, rollbackErr)
	}
	if err := verifyCurrentEntryMatchesOpened(parentFD, isolatedName, display, openedInfo, false, nil); err != nil {
		return rollback(err)
	}
	unlinkErr := unix.Unlinkat(parentFD, isolatedName, 0)
	if unlinkErr != nil {
		if unlinkErr == unix.ENOENT {
			_ = opened.Close()
			return rootPathError("unlinkat", checkedRemovalIsolationDisplay(display, isolatedName), errors.Join(ErrEntryChanged, unlinkErr))
		}
		return rollback(rootPathError("unlinkat", checkedRemovalIsolationDisplay(display, isolatedName), checkedRemovalMutationError(unlinkErr)))
	}
	_ = opened.Close()
	return nil
}

func isolateCheckedRemovalAt(parentFD int, name, display string) (string, error) {
	for range 32 {
		var suffix [16]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		isolatedName := ".mnemonas-remove-" + hex.EncodeToString(suffix[:])
		if err := renameNoReplaceAt(parentFD, name, parentFD, isolatedName); err == nil {
			return isolatedName, nil
		} else if err == unix.EEXIST || err == unix.ENOTEMPTY {
			continue
		} else {
			return "", rootPathError("renameat", display, checkedRemovalLookupError(err))
		}
	}
	return "", rootPathError("renameat", display, errors.New("failed to allocate checked removal isolation name"))
}

func rollbackCheckedRemovalIsolation(parentFD int, name, isolatedName, display string) error {
	if isolatedName == "" {
		return nil
	}
	if err := renameNoReplaceAt(parentFD, isolatedName, parentFD, name); err != nil {
		return rootPathError("renameat", checkedRemovalIsolationDisplay(display, isolatedName), err)
	}
	return nil
}

func checkedRemovalIsolationDisplay(display, isolatedName string) string {
	parent := filepath.Dir(display)
	if parent == "." {
		return isolatedName
	}
	return filepath.Join(parent, isolatedName)
}

func checkedRemovalLookupError(err error) error {
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) ||
		errors.Is(err, unix.ENXIO) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ESTALE) {
		return errors.Join(ErrEntryChanged, err)
	}
	return err
}

func checkedRemovalMutationError(err error) error {
	if errors.Is(err, unix.EISDIR) || errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
		return errors.Join(ErrEntryChanged, err)
	}
	return checkedRemovalLookupError(err)
}

func finishRemoveAllFileUnlink(opened io.Closer, display string, checked bool, unlinkErr error) error {
	if opened != nil {
		// The unlink has already committed when unlinkErr is nil. A close error
		// from this read-only verification handle must not turn that committed
		// removal into a rollback signal for callers.
		_ = opened.Close()
	}
	if unlinkErr == nil {
		return nil
	}
	if unlinkErr == unix.ENOENT {
		if checked {
			return rootPathError("unlinkat", display, errors.Join(ErrEntryChanged, unlinkErr))
		}
		return nil
	}
	return rootPathError("unlinkat", display, unlinkErr)
}

func verifyCurrentEntryMatchesOpened(parentFD int, name, display string, openedInfo os.FileInfo, directory bool, verify func(string, os.FileInfo) error) error {
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(parentFD, name, flags, 0)
	if err != nil {
		return noFollowPathError("openat", display, parentFD, name, checkedRemovalLookupError(err))
	}
	current := os.NewFile(uintptr(fd), display)
	currentInfo, statErr := current.Stat()
	if statErr != nil {
		closeErr := current.Close()
		return rootPathError("fstat", display, errors.Join(statErr, closeErr))
	}
	if openedInfo == nil || !os.SameFile(openedInfo, currentInfo) || openedInfo.IsDir() != currentInfo.IsDir() {
		_ = current.Close()
		return rootPathError("unlinkat", display, ErrEntryChanged)
	}
	if !directory && (openedInfo.Mode() != currentInfo.Mode() || openedInfo.Size() != currentInfo.Size() || !openedInfo.ModTime().Equal(currentInfo.ModTime())) {
		_ = current.Close()
		return rootPathError("unlinkat", display, ErrEntryChanged)
	}
	if verify != nil {
		if err := verify(display, currentInfo); err != nil {
			_ = current.Close()
			return err
		}
		if closeErr := current.Close(); closeErr != nil {
			return rootPathError("close", display, closeErr)
		}
		return verifyCurrentEntryMatchesOpened(parentFD, name, display, openedInfo, directory, nil)
	}
	if closeErr := current.Close(); closeErr != nil {
		return rootPathError("close", display, closeErr)
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

func replaceEmptyDirPathNoFollow(rootPath, relParent, oldName, newName, oldDisplay, newDisplay string) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer rootDir.Close()

	parentFD := int(rootDir.Fd())
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	if relParent != "" {
		parts, err := splitRelativeName(relParent)
		if err != nil {
			return rootPathError("openat", newDisplay, err)
		}
		for _, part := range parts {
			if part == "." {
				continue
			}
			nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return noFollowPathError("openat", newDisplay, parentFD, part, err)
			}
			if closeParent {
				_ = unix.Close(parentFD)
			}
			parentFD = nextFD
			closeParent = true
		}
	}

	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, oldName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return noFollowPathError("fstatat", oldDisplay, parentFD, oldName, err)
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return symlinkPathError("renameat", oldDisplay)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return rootPathError("renameat", oldDisplay, unix.ENOTDIR)
	}

	if err := unix.Fstatat(parentFD, newName, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			return symlinkPathError("renameat", newDisplay)
		case unix.S_IFDIR:
			if err := unix.Unlinkat(parentFD, newName, unix.AT_REMOVEDIR); err != nil {
				if err == unix.ENOTEMPTY || err == unix.EEXIST {
					return rootPathError("unlinkat", newDisplay, os.ErrExist)
				}
				return rootPathError("unlinkat", newDisplay, err)
			}
		default:
			return rootPathError("renameat", newDisplay, unix.ENOTDIR)
		}
	} else if err != unix.ENOENT {
		return noFollowPathError("fstatat", newDisplay, parentFD, newName, err)
	}

	if err := renameNoReplaceAt(parentFD, oldName, parentFD, newName); err != nil {
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", newDisplay, os.ErrExist)
		}
		return rootPathError("renameat", newDisplay, err)
	}
	return nil
}

func replaceFilePathNoFollow(rootPath, relParent, oldName, newName, oldDisplay, newDisplay string) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer rootDir.Close()

	parentFD := int(rootDir.Fd())
	closeParent := false
	defer func() {
		if closeParent {
			_ = unix.Close(parentFD)
		}
	}()

	if relParent != "" {
		parts, err := splitRelativeName(relParent)
		if err != nil {
			return rootPathError("openat", newDisplay, err)
		}
		for _, part := range parts {
			if part == "." {
				continue
			}
			nextFD, err := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return noFollowPathError("openat", newDisplay, parentFD, part, err)
			}
			if closeParent {
				_ = unix.Close(parentFD)
			}
			parentFD = nextFD
			closeParent = true
		}
	}

	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, oldName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return noFollowPathError("fstatat", oldDisplay, parentFD, oldName, err)
	}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return symlinkPathError("renameat", oldDisplay)
	case unix.S_IFREG:
	default:
		return rootPathError("renameat", oldDisplay, unix.EINVAL)
	}

	if err := unix.Fstatat(parentFD, newName, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			return symlinkPathError("renameat", newDisplay)
		case unix.S_IFREG:
		default:
			return rootPathError("renameat", newDisplay, unix.EINVAL)
		}
	} else if err != unix.ENOENT {
		return noFollowPathError("fstatat", newDisplay, parentFD, newName, err)
	}

	if err := unix.Renameat(parentFD, oldName, parentFD, newName); err != nil {
		return rootPathError("renameat", newDisplay, err)
	}
	return nil
}

type noFollowDirFD struct {
	root        *os.Root
	rootDir     *os.File
	fd          int
	closeParent bool
}

type noFollowRootDirFD struct {
	rootDir     *os.File
	fd          int
	closeParent bool
}

func openDirFDFromRootNoFollow(root *os.Root, relPath, display string) (*noFollowRootDirFD, error) {
	rootDir, err := root.Open(".")
	if err != nil {
		return nil, err
	}

	handle := &noFollowRootDirFD{
		rootDir: rootDir,
		fd:      int(rootDir.Fd()),
	}
	if relPath == "" || relPath == "." {
		return handle, nil
	}

	parts, err := splitRelativeName(relPath)
	if err != nil {
		handle.Close()
		return nil, rootPathError("openat", display, err)
	}
	for _, part := range parts {
		if part == "." {
			continue
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(handle.fd, part, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			handle.Close()
			return nil, noFollowPathError("fstatat", display, handle.fd, part, err)
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			handle.Close()
			return nil, symlinkPathError("openat", display)
		case unix.S_IFDIR:
		default:
			handle.Close()
			return nil, rootPathError("openat", display, unix.ENOTDIR)
		}
		nextFD, err := unix.Openat(handle.fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			handle.Close()
			return nil, noFollowPathError("openat", display, handle.fd, part, err)
		}
		if handle.closeParent {
			_ = unix.Close(handle.fd)
		}
		handle.fd = nextFD
		handle.closeParent = true
	}
	return handle, nil
}

func (h *noFollowRootDirFD) Close() {
	if h == nil {
		return
	}
	if h.closeParent {
		_ = unix.Close(h.fd)
	}
	if h.rootDir != nil {
		_ = h.rootDir.Close()
	}
}

func openDirFDNoFollow(rootPath, relPath, display string) (*noFollowDirFD, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}

	rootDir, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}

	handle := &noFollowDirFD{
		root:    root,
		rootDir: rootDir,
		fd:      int(rootDir.Fd()),
	}
	if relPath == "" {
		return handle, nil
	}

	parts, err := splitRelativeName(relPath)
	if err != nil {
		handle.Close()
		return nil, rootPathError("openat", display, err)
	}
	for _, part := range parts {
		if part == "." {
			continue
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(handle.fd, part, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			handle.Close()
			return nil, noFollowPathError("fstatat", display, handle.fd, part, err)
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			handle.Close()
			return nil, symlinkPathError("openat", display)
		case unix.S_IFDIR:
		default:
			handle.Close()
			return nil, rootPathError("openat", display, unix.ENOTDIR)
		}
		nextFD, err := unix.Openat(handle.fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			handle.Close()
			return nil, noFollowPathError("openat", display, handle.fd, part, err)
		}
		if handle.closeParent {
			_ = unix.Close(handle.fd)
		}
		handle.fd = nextFD
		handle.closeParent = true
	}
	return handle, nil
}

func (h *noFollowDirFD) Close() {
	if h == nil {
		return
	}
	if h.closeParent {
		_ = unix.Close(h.fd)
	}
	if h.rootDir != nil {
		_ = h.rootDir.Close()
	}
	if h.root != nil {
		_ = h.root.Close()
	}
}

func renamePathIntoDirNoFollow(sourceRootPath, sourceRelParent, sourceName, targetRootPath, targetRelDir, targetName, sourceDisplay, targetDisplay string) error {
	sourceParent, err := openDirFDNoFollow(sourceRootPath, sourceRelParent, sourceDisplay)
	if err != nil {
		return err
	}
	defer sourceParent.Close()

	targetDir, err := openDirFDNoFollow(targetRootPath, targetRelDir, targetDisplay)
	if err != nil {
		return err
	}
	defer targetDir.Close()

	var stat unix.Stat_t
	if err := unix.Fstatat(sourceParent.fd, sourceName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return noFollowPathError("fstatat", sourceDisplay, sourceParent.fd, sourceName, err)
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return symlinkPathError("renameat", sourceDisplay)
	}

	if err := unix.Fstatat(targetDir.fd, targetName, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return symlinkPathError("renameat", targetDisplay)
		}
		return rootPathError("renameat", targetDisplay, os.ErrExist)
	} else if err != unix.ENOENT {
		return noFollowPathError("fstatat", targetDisplay, targetDir.fd, targetName, err)
	}

	if err := renameNoReplaceAt(sourceParent.fd, sourceName, targetDir.fd, targetName); err != nil {
		if err == unix.EEXIST || err == unix.ENOTEMPTY {
			return rootPathError("renameat", targetDisplay, os.ErrExist)
		}
		return rootPathError("renameat", targetDisplay, err)
	}
	return nil
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
