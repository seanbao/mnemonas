//go:build !unix && !windows

package auth

import "os"

func validateAuthStateLockDirectory(_ *os.Root, _ string) error {
	return nil
}
