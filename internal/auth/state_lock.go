package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const authStateLockFileName = "auth-state.lock"

var (
	// ErrAuthStateLockHeld reports that another process owns the auth state lock.
	ErrAuthStateLockHeld = errors.New("authentication state lock is already held; another MnemoNAS process may be running")
	// ErrAuthStateLockUnsafePath reports a symlink or non-regular lock path.
	ErrAuthStateLockUnsafePath = errors.New("authentication state lock path must not contain symlinks or special files")
	// ErrAuthStateLockUnsafeDirectory reports a directory where another local
	// account could replace the lock file while it is held.
	ErrAuthStateLockUnsafeDirectory = errors.New("authentication state lock directory must not be writable by group or other users")
	// ErrAuthStateLockUnsafeAncestor reports an authentication-state path that
	// another local account could rename or replace through an ancestor.
	ErrAuthStateLockUnsafeAncestor = errors.New("authentication state lock path must have trusted owners and no replaceable writable ancestors")
	// ErrAuthStateLockUnsupported reports a platform without process file locks.
	ErrAuthStateLockUnsupported = errors.New("authentication state locking is unsupported on this platform")
	// ErrAuthStateLockRequired reports that an operation was not given the
	// currently held lock for its authentication-state directory.
	ErrAuthStateLockRequired = errors.New("a held authentication state lock for the selected users file is required")
)

// StateLock owns the process-wide authentication state writer lock.
//
// The lock file remains on disk after Close. Removing a lock file while it is
// held can create a second inode and break cross-process exclusion. Copies of
// StateLock share one ownership state; closing any copy invalidates all copies.
type StateLock struct {
	state *stateLockState
}

type stateLockState struct {
	mu         sync.Mutex
	file       *os.File
	pathGuards []*os.File
	path       string
	closeErr   error
}

// AcquireStateLock acquires a non-blocking, cross-process writer lock for the
// authentication state stored alongside usersFile.
func AcquireStateLock(usersFile string) (*StateLock, error) {
	normalizedUsersPath, err := normalizeAuthFilePath(usersFile)
	if err != nil {
		return nil, fmt.Errorf("resolve authentication users file: %w", err)
	}
	if err := validateAuthFilePath(normalizedUsersPath, ErrAuthStateLockUnsafePath); err != nil {
		return nil, fmt.Errorf("validate authentication users file path: %w", err)
	}

	lockPath := filepath.Join(filepath.Dir(normalizedUsersPath), authStateLockFileName)
	if err := validateAuthFilePath(lockPath, ErrAuthStateLockUnsafePath); err != nil {
		return nil, fmt.Errorf("validate authentication state lock path: %w", err)
	}
	normalizedLockPath, registeredRoot, _, err := ensureAuthDirRootWithState(lockPath, ErrAuthStateLockUnsafePath, "authentication state lock", true)
	if err != nil {
		return nil, fmt.Errorf("prepare authentication state lock directory: %w", err)
	}
	releaseRootOnError := registeredRoot != nil
	defer func() {
		if releaseRootOnError {
			releaseRegisteredAuthDirRoot(filepath.Dir(normalizedLockPath), registeredRoot)
		}
	}()

	root := registeredRoot
	if root == nil {
		var ok bool
		root, normalizedLockPath, ok, err = registeredAuthDirRoot(normalizedLockPath)
		if err != nil {
			return nil, fmt.Errorf("resolve authentication state lock root: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("open authentication state lock directory: %w", os.ErrNotExist)
		}
	}
	if err := validateAuthStateLockDirectory(root, filepath.Dir(normalizedLockPath)); err != nil {
		return nil, fmt.Errorf("validate authentication state lock directory: %w", err)
	}

	file, pathGuards, err := openAuthStateLockFiles(root, normalizedLockPath)
	if err != nil {
		return nil, fmt.Errorf("open authentication state lock: %w", mapAuthRootPathError(err, ErrAuthStateLockUnsafePath))
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
			_ = closeAuthStatePathGuards(pathGuards)
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect authentication state lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("inspect authentication state lock: %w", ErrAuthStateLockUnsafePath)
	}
	if err := secureAuthStateLockFilePermissions(file); err != nil {
		return nil, fmt.Errorf("set authentication state lock permissions: %w", err)
	}
	if err := tryLockAuthStateFile(file); err != nil {
		return nil, fmt.Errorf("acquire authentication state lock %s: %w", normalizedLockPath, err)
	}

	closeOnError = false
	releaseRootOnError = false
	return &StateLock{state: &stateLockState{file: file, pathGuards: pathGuards, path: normalizedLockPath}}, nil
}

// Path returns the absolute lock file path.
func (l *StateLock) Path() string {
	if l == nil || l.state == nil {
		return ""
	}
	return l.state.path
}

// Close releases the authentication state lock. It is safe to call more than
// once; later calls return the result of the first close.
func (l *StateLock) Close() error {
	if l == nil || l.state == nil {
		return nil
	}

	state := l.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.file == nil {
		return state.closeErr
	}

	file := state.file
	pathGuards := state.pathGuards
	state.file = nil
	state.pathGuards = nil
	unlockErr := unlockAuthStateFile(file)
	closeErr := file.Close()
	guardCloseErr := closeAuthStatePathGuards(pathGuards)
	state.closeErr = errors.Join(
		wrapStateLockCloseError("release authentication state lock", unlockErr),
		wrapStateLockCloseError("close authentication state lock", closeErr),
		wrapStateLockCloseError("close authentication state path guard", guardCloseErr),
	)
	return state.closeErr
}

func closeAuthStatePathGuards(guards []*os.File) error {
	var closeErrors []error
	for index := len(guards) - 1; index >= 0; index-- {
		if err := guards[index].Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

func wrapStateLockCloseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func authStateLockPathError(err error, path string) error {
	return fmt.Errorf("%w: %s", err, path)
}
