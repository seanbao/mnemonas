//go:build !windows

package backup

import (
	"os"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func openBackupLockFiles(root *os.Root, _ string, lockName string) (*os.File, []*os.File, error) {
	file, err := rootio.OpenFileNoFollow(root, lockName, os.O_CREATE|os.O_RDWR, 0o600)
	return file, nil, err
}

func secureBackupLockFilePermissions(file *os.File) error {
	return file.Chmod(0o600)
}
