//go:build windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openAuthStateLockFiles(root *os.Root, normalizedLockPath string) (*os.File, []*os.File, error) {
	directoryPath := filepath.Dir(normalizedLockPath)
	directoryPaths := windowsStateLockDirectoryPaths(directoryPath)
	pathGuards := make([]*os.File, 0, len(directoryPaths))
	for _, path := range directoryPaths {
		pathGuard, err := openWindowsStateLockPath(path, true)
		if err != nil {
			_ = closeAuthStatePathGuards(pathGuards)
			return nil, nil, fmt.Errorf("guard authentication state directory %s: %w", path, err)
		}
		pathGuards = append(pathGuards, pathGuard)
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		_ = closeAuthStatePathGuards(pathGuards)
		return nil, nil, err
	}
	guardInfo, err := pathGuards[len(pathGuards)-1].Stat()
	if err != nil || !os.SameFile(rootInfo, guardInfo) {
		_ = closeAuthStatePathGuards(pathGuards)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, authStateLockPathError(ErrAuthStateLockUnsafeAncestor, directoryPath)
	}
	file, err := openWindowsStateLockPath(normalizedLockPath, false)
	if err != nil {
		_ = closeAuthStatePathGuards(pathGuards)
		return nil, nil, fmt.Errorf("open authentication state lock file: %w", err)
	}
	return file, pathGuards, nil
}

func windowsStateLockDirectoryPaths(directoryPath string) []string {
	paths := []string{filepath.Clean(directoryPath)}
	for {
		parent := filepath.Dir(paths[len(paths)-1])
		if parent == paths[len(paths)-1] {
			break
		}
		paths = append(paths, parent)
	}
	for left, right := 0, len(paths)-1; left < right; left, right = left+1, right-1 {
		paths[left], paths[right] = paths[right], paths[left]
	}
	return paths
}

func openWindowsStateLockPath(path string, directory bool) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	shareMode := uint32(windows.FILE_SHARE_READ)
	creation := uint32(windows.OPEN_ALWAYS)
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		access = windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
		shareMode = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
		creation = windows.OPEN_EXISTING
		attributes = windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := windows.CreateFile(
		pathPtr,
		access,
		shareMode,
		nil,
		creation,
		attributes,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrAuthStateLockHeld
		}
		return nil, err
	}
	closeHandle := true
	defer func() {
		if closeHandle {
			_ = windows.CloseHandle(handle)
		}
	}()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, ErrAuthStateLockUnsafePath
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != directory {
		return nil, ErrAuthStateLockUnsafePath
	}

	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		return nil, errors.New("convert authentication state handle")
	}
	closeHandle = false
	return file, nil
}
