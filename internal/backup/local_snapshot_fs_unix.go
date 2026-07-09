//go:build unix

package backup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func syncOpenedBackupSnapshotDirectoryTree(dir *os.File, displayPath string) error {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child, err := rootio.OpenDirEntryNoFollow(dir, entry.Name())
		if err != nil {
			return mapBackupNoFollowError(err, "backup snapshot")
		}
		info, statErr := child.Stat()
		if statErr != nil {
			_ = child.Close()
			return statErr
		}
		switch {
		case info.IsDir():
			if err := syncOpenedBackupSnapshotDirectoryTree(child, filepath.Join(displayPath, entry.Name())); err != nil {
				_ = child.Close()
				return err
			}
		case info.Mode().IsRegular():
		default:
			_ = child.Close()
			return fmt.Errorf("%w: backup snapshot contains an unsupported file type", ErrUnsupportedFileType)
		}
		if err := child.Close(); err != nil {
			return err
		}
	}
	return dir.Sync()
}

func renameBackupSnapshotNoReplace(root *os.Root, partialName, finalName string) error {
	return rootio.RenameLeafNoReplace(root, partialName, finalName)
}

func removeLocalSnapshotEntryNoFollowChecked(parent *os.File, _ string, name string, expected os.FileInfo) error {
	return rootio.RemoveAllFromDirNoFollowChecked(parent, name, func(entryPath string, info os.FileInfo) error {
		if entryPath == name && !os.SameFile(info, expected) {
			return fmt.Errorf("%w: local snapshot entry changed before removal", ErrUnsafePath)
		}
		return nil
	})
}

func openLocalSnapshotEntry(parent *os.File, _ string, name string) (*os.File, error) {
	return rootio.OpenDirEntryNoFollow(parent, name)
}
