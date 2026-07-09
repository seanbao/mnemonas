//go:build windows

package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openBackupLockFiles(root *os.Root, lockPath, _ string) (*os.File, []*os.File, error) {
	directoryPath := filepath.Dir(lockPath)
	directoryPaths := windowsBackupLockDirectoryPaths(directoryPath)
	guards := make([]*os.File, 0, len(directoryPaths))
	for _, path := range directoryPaths {
		guard, err := openWindowsBackupLockPath(path, true)
		if err != nil {
			_ = closeBackupLockPathGuards(guards)
			return nil, nil, fmt.Errorf("guard backup lock directory %s: %w", path, err)
		}
		guards = append(guards, guard)
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		_ = closeBackupLockPathGuards(guards)
		return nil, nil, err
	}
	guardInfo, err := guards[len(guards)-1].Stat()
	if err != nil || !os.SameFile(rootInfo, guardInfo) {
		_ = closeBackupLockPathGuards(guards)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, ErrBackupStateLockUnsafeAncestor
	}
	file, err := openWindowsBackupLockPath(lockPath, false)
	if err != nil {
		_ = closeBackupLockPathGuards(guards)
		return nil, nil, err
	}
	return file, guards, nil
}

func windowsBackupLockDirectoryPaths(directoryPath string) []string {
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

func openWindowsBackupLockPath(path string, directory bool) (*os.File, error) {
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
	handle, err := windows.CreateFile(pathPtr, access, shareMode, nil, creation, attributes, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBackupStateLockHeld
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
		return nil, ErrUnsafePath
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != directory {
		return nil, ErrUnsafePath
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		return nil, errors.New("convert backup lock handle")
	}
	closeHandle = false
	return file, nil
}

func secureBackupLockFilePermissions(_ *os.File) error {
	return nil
}
