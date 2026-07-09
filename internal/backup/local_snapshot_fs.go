package backup

import (
	"errors"
	"fmt"
	"os"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func openLocalSnapshotRoot(snapshotRoot string) (*os.Root, *os.File, os.FileInfo, error) {
	root, err := os.OpenRoot(snapshotRoot)
	if err != nil {
		return nil, nil, nil, mapBackupNoFollowError(err, "snapshot root")
	}
	closeRoot := true
	defer func() {
		if closeRoot {
			_ = root.Close()
		}
	}()

	dir, err := rootio.OpenDirNoFollow(root, ".")
	if err != nil {
		return nil, nil, nil, mapBackupNoFollowError(err, "snapshot root")
	}
	closeDir := true
	defer func() {
		if closeDir {
			_ = dir.Close()
		}
	}()

	info, err := dir.Stat()
	if err != nil {
		return nil, nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, nil, fmt.Errorf("%w: snapshot root is not a directory", ErrUnsafePath)
	}
	if err := verifyLocalSnapshotRootIdentity(snapshotRoot, info); err != nil {
		return nil, nil, nil, err
	}

	closeRoot = false
	closeDir = false
	return root, dir, info, nil
}

func verifyLocalSnapshotRootIdentity(snapshotRoot string, expected os.FileInfo) error {
	info, err := os.Lstat(snapshotRoot)
	if err != nil {
		return fmt.Errorf("inspect snapshot root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(info, expected) {
		return fmt.Errorf("%w: snapshot root changed during backup", ErrUnsafePath)
	}
	return nil
}

func localSnapshotEntryInfo(root *os.Root, name string) (os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: local snapshot entry is not a directory", ErrUnsafePath)
	}
	return info, nil
}

func verifyLocalSnapshotEntryIdentity(root *os.Root, name string, expected os.FileInfo) error {
	info, err := localSnapshotEntryInfo(root, name)
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected) {
		return fmt.Errorf("%w: local snapshot entry changed during backup", ErrUnsafePath)
	}
	return nil
}

func reconcileLocalSnapshotRename(root *os.Root, partialName, finalName string, expected os.FileInfo) (bool, error) {
	partialInfo, partialErr := localSnapshotEntryInfo(root, partialName)
	finalInfo, finalErr := localSnapshotEntryInfo(root, finalName)
	partialMissing := errors.Is(partialErr, os.ErrNotExist)
	finalMissing := errors.Is(finalErr, os.ErrNotExist)

	if partialMissing && finalErr == nil && os.SameFile(finalInfo, expected) {
		return true, nil
	}
	if finalMissing && partialErr == nil && os.SameFile(partialInfo, expected) {
		return false, nil
	}
	if partialErr != nil && !partialMissing {
		return false, fmt.Errorf("inspect partial snapshot after rename: %w", partialErr)
	}
	if finalErr != nil && !finalMissing {
		return false, fmt.Errorf("inspect final snapshot after rename: %w", finalErr)
	}
	return false, fmt.Errorf("%w: local snapshot rename result is ambiguous", ErrUnsafePath)
}

func removeLocalSnapshotEntryDurably(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	if err := removeLocalSnapshotEntryNoFollowChecked(parent, parentPath, name, expected); err != nil {
		return err
	}
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync local snapshot parent after removal: %w", err)
	}
	return nil
}

func syncBackupDirectoryHandle(dir *os.File) error {
	if dir == nil {
		return os.ErrInvalid
	}
	return dir.Sync()
}

func syncBackupSnapshotDirectoryTree(root string) error {
	dir, err := rootio.OpenDirPathNoFollow(root)
	if err != nil {
		return mapBackupNoFollowError(err, "backup snapshot")
	}
	defer dir.Close()
	return syncOpenedBackupSnapshotDirectoryTree(dir, root)
}
