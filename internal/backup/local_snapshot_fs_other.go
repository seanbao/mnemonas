//go:build !unix

package backup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func syncOpenedBackupSnapshotDirectoryTree(dir *os.File, displayPath string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(displayPath, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(entryPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: backup snapshot contains a symlink", ErrUnsafePath)
		}
		if info.IsDir() {
			directories = append(directories, entryPath)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: backup snapshot contains an unsupported file type", ErrUnsupportedFileType)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index > 0; index-- {
		if err := syncBackupDirectory(directories[index]); err != nil {
			return err
		}
	}
	return dir.Sync()
}

func renameBackupSnapshotNoReplace(root *os.Root, partialName, finalName string) error {
	if _, err := root.Lstat(finalName); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	return root.Rename(partialName, finalName)
}

func removeLocalSnapshotEntryNoFollowChecked(_ *os.File, parentPath, name string, expected os.FileInfo) error {
	targetPath := filepath.Join(parentPath, name)
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(info, expected) {
		return fmt.Errorf("%w: local snapshot entry changed before removal", ErrUnsafePath)
	}
	return removeAllBackupPath(targetPath, "local snapshot entry")
}

func openLocalSnapshotEntry(_ *os.File, parentPath, name string) (*os.File, error) {
	return rootio.OpenDirPathNoFollow(filepath.Join(parentPath, name))
}
