//go:build windows

package auth

import "os"

func secureAuthStateLockFilePermissions(_ *os.File) error {
	// Windows access is inherited from the authentication-state directory ACL.
	// Chmod is unsupported and cannot express the required DACL.
	return nil
}
