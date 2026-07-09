package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/rootio"
)

const backupTargetLockFileName = ".mnemonas-target.lock"

var closeBackupTargetLock = func(lock *backupStateLock) error { return lock.Close() }

var (
	// ErrBackupTargetLockHeld reports a local backup target used by another manager.
	ErrBackupTargetLockHeld = errors.New("backup target lock is already held; another backup operation may be running")
	// ErrBackupTargetLockUnsafeDirectory reports a target directory whose lock file can be replaced by another account.
	ErrBackupTargetLockUnsafeDirectory = errors.New("backup target lock directory must not be writable by group or other users")
	// ErrBackupTargetLockUnsafeAncestor reports a replaceable target-lock ancestor.
	ErrBackupTargetLockUnsafeAncestor = errors.New("backup target lock path must have trusted owners and no replaceable writable ancestors")
	// ErrBackupTargetLockRelease reports that an operation could not confirm target-lock release.
	ErrBackupTargetLockRelease = errors.New("backup target lock release failed")
)

func (m *Manager) acquireJobTargetLock(job config.BackupJobConfig) (*backupStateLock, error) {
	if job.Type != JobTypeLocal {
		return nil, nil
	}
	source := effectiveSource(job, m.storageRoot)
	if err := validateDestination(source, job.Destination, m.storageRoot); err != nil {
		return nil, err
	}

	jobRoot := filepath.Join(job.Destination, job.ID)
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(jobRoot, 0o700)
	if err != nil {
		return nil, fmt.Errorf("create backup target lock directory: %w", mapBackupNoFollowError(err, "backup target"))
	}
	if err := syncCreatedBackupDirectories(createdDirs, syncLocalBackupDirectory); err != nil {
		return nil, fmt.Errorf("sync backup target lock directory tree: %w", err)
	}
	return acquireBackupFileLock(jobRoot, backupTargetLockFileName, "backup target", ErrBackupTargetLockHeld, validateBackupTargetLockDirectory)
}

func (m *Manager) acquireJobTargetLockForBackup(job config.BackupJobConfig) (*backupStateLock, error) {
	if job.Type == JobTypeLocal {
		if err := validateSourceDirectory(effectiveSource(job, m.storageRoot)); err != nil {
			return nil, err
		}
	}
	return m.acquireJobTargetLock(job)
}

func validateBackupTargetLockDirectory(root *os.Root, directoryPath string) error {
	rootInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return fmt.Errorf("%w: backup target lock directory changed", ErrUnsafePath)
	}
	return validateBackupTargetLockDirectorySecurity(root, directoryPath)
}

func appendBackupTargetLockCloseError(lock *backupStateLock, returnedErr *error) {
	if lock == nil || returnedErr == nil {
		return
	}
	if err := closeBackupTargetLock(lock); err != nil {
		*returnedErr = errors.Join(*returnedErr, ErrBackupTargetLockRelease, fmt.Errorf("release backup target lock: %w", err))
	}
}
