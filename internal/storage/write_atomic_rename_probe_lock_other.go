//go:build !unix

package storage

import (
	"errors"
	"os"
)

var errAtomicWriteRenameProbeJournalLocked = errors.New("atomic write rename probe journal locking is unavailable")

func tryLockAtomicWriteRenameProbeJournal(*os.File) error {
	return errors.ErrUnsupported
}

func unlockAtomicWriteRenameProbeJournal(*os.File) error {
	return nil
}
