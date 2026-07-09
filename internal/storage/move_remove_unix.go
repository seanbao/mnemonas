//go:build unix

package storage

import (
	"os"
	"path/filepath"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func removeCopiedMoveSource(root *os.Root, rel, _ string, expected os.FileInfo) error {
	parent, err := rootio.OpenDirNoFollow(root, filepath.Dir(rel))
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	return rootio.RemoveAllFromDirNoFollowChecked(parent, filepath.Base(rel), func(_ string, current os.FileInfo) error {
		if !sameStorageCopySource(expected, current) {
			return ErrDeleteTargetChanged
		}
		return nil
	})
}
