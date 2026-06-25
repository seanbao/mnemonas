//go:build !windows

package auth

import (
	"os"
	"path/filepath"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func openAuthStateLockFiles(root *os.Root, normalizedLockPath string) (*os.File, []*os.File, error) {
	file, err := rootio.OpenFileNoFollow(root, filepath.Base(normalizedLockPath), os.O_CREATE|os.O_RDWR, 0o600)
	return file, nil, err
}
