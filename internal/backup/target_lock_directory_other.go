//go:build !unix

package backup

import "os"

func validateBackupTargetLockDirectorySecurity(_ *os.Root, _ string) error {
	return nil
}
