package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const backupStateLockFileName = "backup-state.lock"

var (
	// ErrBackupStateLockHeld reports that another backup manager owns the state root.
	ErrBackupStateLockHeld = errors.New("backup state lock is already held; another MnemoNAS process may be running")
	// ErrBackupStateLockUnsupported reports a platform without process file locks.
	ErrBackupStateLockUnsupported = errors.New("backup state locking is unsupported on this platform")
	// ErrBackupStateLockUnsafeDirectory reports a state root writable by other local accounts.
	ErrBackupStateLockUnsafeDirectory = errors.New("backup state lock directory must not be writable by group or other users")
	// ErrBackupStateLockUnsafeAncestor reports a replaceable state-root ancestor.
	ErrBackupStateLockUnsafeAncestor = errors.New("backup state lock path must have trusted owners and no replaceable writable ancestors")
)

// backupStateLock owns the process-wide writer lock for one backup state root.
//
// The lock file remains after Close. Removing it while held could create a
// second inode and break cross-process exclusion.
type backupStateLock struct {
	mu       sync.Mutex
	file     *os.File
	root     *os.Root
	guards   []*os.File
	path     string
	closeErr error
}

func acquireBackupStateLock(root string) (*backupStateLock, error) {
	return acquireBackupFileLock(root, backupStateLockFileName, "backup state", ErrBackupStateLockHeld, validateBackupStateLockDirectory)
}

func acquireBackupFileLock(
	root string,
	lockName string,
	label string,
	heldError error,
	validateDirectory func(*os.Root, string) error,
) (*backupStateLock, error) {
	lockPath := filepath.Join(root, lockName)
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %s lock directory: %w", label, err)
	}
	closeRootOnError := true
	defer func() {
		if closeRootOnError {
			_ = rootHandle.Close()
		}
	}()
	if err := validateDirectory(rootHandle, root); err != nil {
		return nil, fmt.Errorf("validate %s lock directory: %w", label, err)
	}

	file, guards, err := openBackupLockFiles(rootHandle, lockPath, lockName)
	if err != nil {
		return nil, fmt.Errorf("open %s lock: %w", label, mapBackupNoFollowError(mapBackupLockHeldError(err, heldError), label+" lock"))
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
			_ = closeBackupLockPathGuards(guards)
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s lock: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("inspect %s lock: %w", label, ErrUnsafePath)
	}
	if err := secureBackupLockFilePermissions(file); err != nil {
		return nil, fmt.Errorf("set %s lock permissions: %w", label, err)
	}
	if err := tryLockBackupStateFile(file); err != nil {
		err = mapBackupLockHeldError(err, heldError)
		return nil, fmt.Errorf("acquire %s lock %s: %w", label, lockPath, err)
	}

	closeOnError = false
	closeRootOnError = false
	return &backupStateLock{file: file, root: rootHandle, guards: guards, path: lockPath}, nil
}

func mapBackupLockHeldError(err, heldError error) error {
	if errors.Is(err, ErrBackupStateLockHeld) {
		return heldError
	}
	return err
}

func (l *backupStateLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return l.closeErr
	}

	file := l.file
	root := l.root
	guards := l.guards
	l.file = nil
	l.root = nil
	l.guards = nil
	l.closeErr = errors.Join(
		wrapBackupStateLockCloseError("release backup state lock", unlockBackupStateFile(file)),
		wrapBackupStateLockCloseError("close backup state lock", file.Close()),
		wrapBackupStateLockCloseError("close backup state lock path guard", closeBackupLockPathGuards(guards)),
		wrapBackupStateLockCloseError("close backup state lock directory", root.Close()),
	)
	return l.closeErr
}

func (l *backupStateLock) verifyDirectoryIdentity(directoryPath string) error {
	if l == nil {
		return fmt.Errorf("%w: backup state lock is unavailable", ErrUnsafePath)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.root == nil {
		return fmt.Errorf("%w: backup state lock directory is closed", ErrUnsafePath)
	}

	rootInfo, err := l.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect locked backup state directory: %w", err)
	}
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return fmt.Errorf("inspect backup state directory path: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return fmt.Errorf("%w: backup state directory no longer names the locked directory", ErrUnsafePath)
	}
	return nil
}

func closeBackupLockPathGuards(guards []*os.File) error {
	var closeErrors []error
	for index := len(guards) - 1; index >= 0; index-- {
		if err := guards[index].Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

func wrapBackupStateLockCloseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
