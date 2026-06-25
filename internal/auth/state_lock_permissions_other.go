//go:build !windows

package auth

import "os"

func secureAuthStateLockFilePermissions(file *os.File) error {
	return file.Chmod(0o600)
}
