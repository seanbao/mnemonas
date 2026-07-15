//go:build unix

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errAtomicWriteRenameProbeJournalLocked = errors.New("atomic write rename probe journal is locked by another process")

func tryLockAtomicWriteRenameProbeJournal(dir *os.File) error {
	if dir == nil {
		return errors.New("atomic write rename probe journal directory is unavailable")
	}
	err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errors.Join(errAtomicWriteRenameProbeJournalLocked, err)
	}
	return err
}

func unlockAtomicWriteRenameProbeJournal(dir *os.File) error {
	if dir == nil {
		return nil
	}
	return unix.Flock(int(dir.Fd()), unix.LOCK_UN)
}
