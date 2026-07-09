//go:build linux || darwin

package rootio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type failingRootIOCloser struct {
	err    error
	closed bool
}

func (c *failingRootIOCloser) Close() error {
	c.closed = true
	return c.err
}

func TestRenameLeafNoReplaceMovesFile(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "stage"), 0755); err != nil {
		t.Fatalf("Mkdir(stage) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RenameLeafNoReplace(root, "source.txt", filepath.Join("stage", "item")); err != nil {
		t.Fatalf("RenameLeafNoReplace() error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "source.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source Lstat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(rootPath, "stage", "item"))
	if err != nil {
		t.Fatalf("ReadFile(staged item) error: %v", err)
	}
	if string(data) != "source" {
		t.Fatalf("staged content = %q, want source", data)
	}
}

func TestRenameLeafNoReplaceMovesDirectory(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "stage"), 0755); err != nil {
		t.Fatalf("Mkdir(stage) error: %v", err)
	}
	sourcePath := filepath.Join(rootPath, "source")
	if err := os.Mkdir(sourcePath, 0755); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RenameLeafNoReplace(root, "source", filepath.Join("stage", "item")); err != nil {
		t.Fatalf("RenameLeafNoReplace() error: %v", err)
	}
	if _, err := os.Lstat(sourcePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source Lstat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(rootPath, "stage", "item", "child.txt"))
	if err != nil {
		t.Fatalf("ReadFile(staged child) error: %v", err)
	}
	if string(data) != "child" {
		t.Fatalf("staged child content = %q, want child", data)
	}
}

func TestRenameLeafNoReplaceRejectsExistingTarget(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = RenameLeafNoReplace(root, "source.txt", "target.txt")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("RenameLeafNoReplace() error = %v, want ErrExist", err)
	}
	for name, want := range map[string]string{
		"source.txt": "source",
		"target.txt": "target",
	} {
		data, readErr := os.ReadFile(filepath.Join(rootPath, name))
		if readErr != nil {
			t.Fatalf("ReadFile(%s) error: %v", name, readErr)
		}
		if string(data) != want {
			t.Fatalf("%s content = %q, want %q", name, data, want)
		}
	}
}

func TestRenameLeafNoReplaceRejectsSymlinkParent(t *testing.T) {
	for _, pathUnderSymlink := range []string{"source", "target"} {
		t.Run(pathUnderSymlink, func(t *testing.T) {
			rootPath := t.TempDir()
			outsidePath := t.TempDir()
			if err := os.Mkdir(filepath.Join(rootPath, "real"), 0755); err != nil {
				t.Fatalf("Mkdir(real) error: %v", err)
			}
			if err := os.WriteFile(filepath.Join(rootPath, "real", "source.txt"), []byte("source"), 0644); err != nil {
				t.Fatalf("WriteFile(source) error: %v", err)
			}
			if err := os.Symlink(outsidePath, filepath.Join(rootPath, "linked")); err != nil {
				t.Fatalf("Symlink(linked) error: %v", err)
			}

			sourceName := filepath.Join("linked", "source.txt")
			targetName := filepath.Join("real", "target.txt")
			if pathUnderSymlink == "target" {
				sourceName = filepath.Join("real", "source.txt")
				targetName = filepath.Join("linked", "target.txt")
			}

			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatalf("OpenRoot() error: %v", err)
			}
			defer root.Close()

			err = RenameLeafNoReplace(root, sourceName, targetName)
			if !IsSymlinkError(err) {
				t.Fatalf("RenameLeafNoReplace() error = %v, want ErrSymlink", err)
			}
			data, err := os.ReadFile(filepath.Join(rootPath, "real", "source.txt"))
			if err != nil {
				t.Fatalf("ReadFile(source) error: %v", err)
			}
			if string(data) != "source" {
				t.Fatalf("source content = %q, want source", data)
			}
			if _, err := os.Lstat(filepath.Join(outsidePath, "target.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outside target Lstat error = %v, want not exist", err)
			}
		})
	}
}

func TestRenameLeafNoReplaceMovesSourceSymlink(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "stage"), 0755); err != nil {
		t.Fatalf("Mkdir(stage) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(rootPath, "source-link")); err != nil {
		t.Fatalf("Symlink(source-link) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RenameLeafNoReplace(root, "source-link", filepath.Join("stage", "item")); err != nil {
		t.Fatalf("RenameLeafNoReplace() error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "source-link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source symlink Lstat error = %v, want not exist", err)
	}
	stagedPath := filepath.Join(rootPath, "stage", "item")
	info, err := os.Lstat(stagedPath)
	if err != nil {
		t.Fatalf("Lstat(staged symlink) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("staged mode = %v, want symlink", info.Mode())
	}
	linkTarget, err := os.Readlink(stagedPath)
	if err != nil {
		t.Fatalf("Readlink(staged symlink) error: %v", err)
	}
	if linkTarget != "target.txt" {
		t.Fatalf("staged link target = %q, want target.txt", linkTarget)
	}
}

func TestRenameLeafNoReplaceMovesSpecialLeaf(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "stage"), 0755); err != nil {
		t.Fatalf("Mkdir(stage) error: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "source-fifo"), 0600); err != nil {
		t.Fatalf("Mkfifo(source-fifo) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RenameLeafNoReplace(root, "source-fifo", filepath.Join("stage", "item")); err != nil {
		t.Fatalf("RenameLeafNoReplace() error: %v", err)
	}
	info, err := os.Lstat(filepath.Join(rootPath, "stage", "item"))
	if err != nil {
		t.Fatalf("Lstat(staged FIFO) error: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("staged mode = %v, want named pipe", info.Mode())
	}
}

func TestRenameLeafBetweenRootsNoReplaceMovesFile(t *testing.T) {
	parentPath := t.TempDir()
	sourceRootPath := filepath.Join(parentPath, "source-root")
	targetRootPath := filepath.Join(parentPath, "target-root")
	if err := os.Mkdir(sourceRootPath, 0755); err != nil {
		t.Fatalf("Mkdir(source root) error: %v", err)
	}
	if err := os.Mkdir(targetRootPath, 0755); err != nil {
		t.Fatalf("Mkdir(target root) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRootPath, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	sourceRoot, err := os.OpenRoot(sourceRootPath)
	if err != nil {
		t.Fatalf("OpenRoot(source) error: %v", err)
	}
	defer sourceRoot.Close()
	targetRoot, err := os.OpenRoot(targetRootPath)
	if err != nil {
		t.Fatalf("OpenRoot(target) error: %v", err)
	}
	defer targetRoot.Close()

	if err := RenameLeafBetweenRootsNoReplace(sourceRoot, "source.txt", targetRoot, "item"); err != nil {
		t.Fatalf("RenameLeafBetweenRootsNoReplace() error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(sourceRootPath, "source.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source Lstat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(targetRootPath, "item"))
	if err != nil {
		t.Fatalf("ReadFile(target item) error: %v", err)
	}
	if string(data) != "source" {
		t.Fatalf("target content = %q, want source", data)
	}
}

func TestRenameLeafBetweenRootsNoReplaceUsesAnchoredRootHandles(t *testing.T) {
	parentPath := t.TempDir()
	sourceRootPath := filepath.Join(parentPath, "source-root")
	targetRootPath := filepath.Join(parentPath, "target-root")
	sourceBackupPath := filepath.Join(parentPath, "source-backup")
	targetBackupPath := filepath.Join(parentPath, "target-backup")
	outsideSourcePath := filepath.Join(parentPath, "outside-source")
	outsideTargetPath := filepath.Join(parentPath, "outside-target")
	for _, path := range []string{sourceRootPath, targetRootPath, outsideSourcePath, outsideTargetPath} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceRootPath, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	sourceRoot, err := os.OpenRoot(sourceRootPath)
	if err != nil {
		t.Fatalf("OpenRoot(source) error: %v", err)
	}
	defer sourceRoot.Close()
	targetRoot, err := os.OpenRoot(targetRootPath)
	if err != nil {
		t.Fatalf("OpenRoot(target) error: %v", err)
	}
	defer targetRoot.Close()

	if err := os.Rename(sourceRootPath, sourceBackupPath); err != nil {
		t.Fatalf("Rename(source root) error: %v", err)
	}
	if err := os.Rename(targetRootPath, targetBackupPath); err != nil {
		t.Fatalf("Rename(target root) error: %v", err)
	}
	if err := os.Symlink(outsideSourcePath, sourceRootPath); err != nil {
		t.Fatalf("Symlink(source root) error: %v", err)
	}
	if err := os.Symlink(outsideTargetPath, targetRootPath); err != nil {
		t.Fatalf("Symlink(target root) error: %v", err)
	}

	if err := RenameLeafBetweenRootsNoReplace(sourceRoot, "source.txt", targetRoot, "item"); err != nil {
		t.Fatalf("RenameLeafBetweenRootsNoReplace() error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(sourceBackupPath, "source.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchored source Lstat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(targetBackupPath, "item"))
	if err != nil {
		t.Fatalf("ReadFile(anchored target) error: %v", err)
	}
	if string(data) != "source" {
		t.Fatalf("anchored target content = %q, want source", data)
	}
	if _, err := os.Lstat(filepath.Join(outsideTargetPath, "item")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside target Lstat error = %v, want not exist", err)
	}
}

func TestHeldDirectoryRenameAndCheckedRemoveStayAnchored(t *testing.T) {
	rootPath := t.TempDir()
	quarantinePath := filepath.Join(rootPath, "quarantine")
	quarantineBackupPath := filepath.Join(rootPath, "quarantine-backup")
	outsidePath := t.TempDir()
	if err := os.Mkdir(quarantinePath, 0o700); err != nil {
		t.Fatalf("Mkdir(quarantine) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()
	quarantine, err := os.Open(quarantinePath)
	if err != nil {
		t.Fatalf("Open(quarantine) error: %v", err)
	}
	defer quarantine.Close()

	if err := RenameLeafIntoDirNoReplace(root, "source.txt", quarantine, "content"); err != nil {
		t.Fatalf("RenameLeafIntoDirNoReplace() error: %v", err)
	}
	if err := os.Rename(quarantinePath, quarantineBackupPath); err != nil {
		t.Fatalf("Rename(quarantine) error: %v", err)
	}
	if err := os.Symlink(outsidePath, quarantinePath); err != nil {
		t.Fatalf("Symlink(replacement quarantine) error: %v", err)
	}

	content, err := OpenDirEntryNoFollow(quarantine, "content")
	if err != nil {
		t.Fatalf("OpenDirEntryNoFollow() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(quarantineBackupPath, "content"))
	if closeErr := content.Close(); err == nil {
		err = closeErr
	}
	if err != nil || string(data) != "source" {
		t.Fatalf("anchored content = %q, %v", data, err)
	}
	if err := RenameLeafFromDirNoReplace(quarantine, "content", root, "restored.txt"); err != nil {
		t.Fatalf("RenameLeafFromDirNoReplace() error: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(rootPath, "restored.txt"))
	if err != nil || string(data) != "source" {
		t.Fatalf("restored content = %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(outsidePath, "content")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside content Lstat error = %v, want not exist", err)
	}
}

func TestRemoveAllFromDirNoFollowCheckedVerifiesBeforeMutation(t *testing.T) {
	rootPath := t.TempDir()
	treePath := filepath.Join(rootPath, "tree")
	if err := os.Mkdir(treePath, 0o700); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(treePath, "child.txt"), []byte("child"), 0o400); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	if err := os.Chmod(treePath, 0o500); err != nil {
		t.Fatalf("Chmod(tree) error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(treePath, 0o700) })
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()
	wantErr := errors.New("identity mismatch")
	err = RemoveAllFromDirNoFollowChecked(rootDir, "tree", func(name string, _ os.FileInfo) error {
		if name != "tree" {
			t.Fatalf("first verified entry = %q, want tree", name)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want identity mismatch", err)
	}
	info, err := os.Stat(treePath)
	if err != nil {
		t.Fatalf("Stat(tree) error: %v", err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("tree mode = %o, want unchanged 500", info.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(treePath, "child.txt"))
	if err != nil || string(data) != "child" {
		t.Fatalf("child after rejected removal = %q, %v", data, err)
	}
}

func TestRemoveAllFromDirNoFollowCheckedRejectsInitiallyMissingEntry(t *testing.T) {
	rootPath := t.TempDir()
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	verifyCalled := false
	err = RemoveAllFromDirNoFollowChecked(rootDir, "missing.txt", func(string, os.FileInfo) error {
		verifyCalled = true
		return nil
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	if verifyCalled {
		t.Fatal("verify called for an initially missing entry")
	}
}

func TestRemoveAllFromDirNoFollowCheckedRejectsEntryRemovedAfterFinalReopen(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	verifyCalls := 0
	err = RemoveAllFromDirNoFollowChecked(rootDir, "target.txt", func(string, os.FileInfo) error {
		verifyCalls++
		if verifyCalls == 2 {
			return os.Remove(targetPath)
		}
		return nil
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	if verifyCalls != 2 {
		t.Fatalf("verify calls = %d, want 2", verifyCalls)
	}
}

func TestRemoveAllFromDirNoFollowCheckedRejectsReplacementAfterFinalVerifier(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "target.txt")
	originalPath := filepath.Join(rootPath, "original.txt")
	replacementPath := filepath.Join(rootPath, "replacement.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	verifyCalls := 0
	err = RemoveAllFromDirNoFollowChecked(rootDir, "target.txt", func(string, os.FileInfo) error {
		verifyCalls++
		if verifyCalls != 2 {
			return nil
		}
		if err := os.Rename(targetPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, targetPath)
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	if verifyCalls != 2 {
		t.Fatalf("verify calls = %d, want 2", verifyCalls)
	}
	for name, want := range map[string]string{"original.txt": "target", "target.txt": "replacement"} {
		data, readErr := os.ReadFile(filepath.Join(rootPath, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s content = %q, %v; want %q", name, data, readErr, want)
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedRestoresReplacementCapturedDuringIsolation(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "target.txt")
	originalPath := filepath.Join(rootPath, "original.txt")
	replacementPath := filepath.Join(rootPath, "replacement.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	originalHook := beforeCheckedRemovalIsolation
	beforeCheckedRemovalIsolation = func(name string) error {
		if name != "target.txt" {
			return nil
		}
		if err := os.Rename(targetPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, targetPath)
	}
	t.Cleanup(func() { beforeCheckedRemovalIsolation = originalHook })

	err = RemoveAllFromDirNoFollowChecked(rootDir, "target.txt", func(string, os.FileInfo) error { return nil })
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	for name, want := range map[string]string{"original.txt": "target", "target.txt": "replacement"} {
		data, readErr := os.ReadFile(filepath.Join(rootPath, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s content = %q, %v; want %q", name, data, readErr, want)
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedRestoresDirectoryReplacementCapturedDuringIsolation(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "tree")
	originalPath := filepath.Join(rootPath, "original")
	replacementPath := filepath.Join(rootPath, "replacement")
	if err := os.Mkdir(targetPath, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.Mkdir(replacementPath, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replacementPath, "unknown.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	originalHook := beforeCheckedRemovalIsolation
	beforeCheckedRemovalIsolation = func(name string) error {
		if name != "tree" {
			return nil
		}
		if err := os.Rename(targetPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, targetPath)
	}
	t.Cleanup(func() { beforeCheckedRemovalIsolation = originalHook })

	err = RemoveAllFromDirNoFollowChecked(rootDir, "tree", func(string, os.FileInfo) error { return nil })
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	if _, statErr := os.Stat(originalPath); statErr != nil {
		t.Fatalf("Stat(original) error: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(targetPath, "unknown.txt"))
	if readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement content = %q, %v; want replacement", data, readErr)
	}
}

func TestRemoveAllFromDirNoFollowCheckedReverifiesSameInodeMetadata(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	wantErr := errors.New("same inode metadata changed")
	verifyCalls := 0
	var initialSystemInfo string
	err = RemoveAllFromDirNoFollowChecked(rootDir, "target.txt", func(_ string, info os.FileInfo) error {
		verifyCalls++
		currentSystemInfo := fmt.Sprintf("%#v", info.Sys())
		if verifyCalls > 1 {
			if currentSystemInfo == initialSystemInfo {
				return errors.New("final verification did not observe metadata drift")
			}
			return wantErr
		}

		initialSystemInfo = currentSystemInfo
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err := os.Chmod(targetPath, 0o400); err != nil {
				return err
			}
			if err := os.Chmod(targetPath, info.Mode().Perm()); err != nil {
				return err
			}
			changed, err := os.Stat(targetPath)
			if err != nil {
				return err
			}
			if changed.Mode() != info.Mode() || changed.Size() != info.Size() || !changed.ModTime().Equal(info.ModTime()) {
				return errors.New("test mutation changed visible file metadata")
			}
			if fmt.Sprintf("%#v", changed.Sys()) != initialSystemInfo {
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("failed to advance file status-change identity")
			}
			time.Sleep(time.Millisecond)
		}
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want same-inode metadata error", err)
	}
	if verifyCalls != 2 {
		t.Fatalf("verify calls = %d, want 2", verifyCalls)
	}
	if _, statErr := os.Stat(targetPath); statErr != nil {
		t.Fatalf("Stat(target) after rejected removal error: %v", statErr)
	}
}

func TestFinishRemoveAllFileUnlinkIgnoresCloseErrorAfterSuccessfulUnlink(t *testing.T) {
	wantErr := errors.New("close failed")
	closer := &failingRootIOCloser{err: wantErr}
	if err := finishRemoveAllFileUnlink(closer, "target.txt", true, nil); err != nil {
		t.Fatalf("finishRemoveAllFileUnlink() error = %v, want nil", err)
	}
	if !closer.closed {
		t.Fatal("finishRemoveAllFileUnlink() did not close verification handle")
	}
}

func TestCheckedRemovalLookupErrorClassifiesOnlyEntryDrift(t *testing.T) {
	for _, err := range []error{unix.ENOENT, unix.ENOTDIR, unix.ELOOP, unix.ENXIO, unix.ENODEV, unix.ESTALE} {
		if mapped := checkedRemovalLookupError(err); !errors.Is(mapped, ErrEntryChanged) || !errors.Is(mapped, err) {
			t.Fatalf("checkedRemovalLookupError(%v) = %v, want entry change retaining cause", err, mapped)
		}
	}
	for _, err := range []error{unix.EIO, unix.EMFILE, unix.EROFS, unix.ENOTSUP} {
		if mapped := checkedRemovalLookupError(err); errors.Is(mapped, ErrEntryChanged) || !errors.Is(mapped, err) {
			t.Fatalf("checkedRemovalLookupError(%v) = %v, want operational error", err, mapped)
		}
	}
	for _, err := range []error{unix.EISDIR, unix.ENOTEMPTY, unix.EEXIST} {
		if mapped := checkedRemovalMutationError(err); !errors.Is(mapped, ErrEntryChanged) || !errors.Is(mapped, err) {
			t.Fatalf("checkedRemovalMutationError(%v) = %v, want entry change retaining cause", err, mapped)
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedRejectsFileReplacementAfterVerification(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("intended"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "replacement.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	err = RemoveAllFromDirNoFollowChecked(rootDir, "target.txt", func(name string, _ os.FileInfo) error {
		if name != "target.txt" {
			t.Fatalf("verified entry = %q, want target.txt", name)
		}
		if err := os.Rename(filepath.Join(rootPath, "target.txt"), filepath.Join(rootPath, "original.txt")); err != nil {
			return err
		}
		return os.Rename(filepath.Join(rootPath, "replacement.txt"), filepath.Join(rootPath, "target.txt"))
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	for name, want := range map[string]string{"original.txt": "intended", "target.txt": "replacement"} {
		data, readErr := os.ReadFile(filepath.Join(rootPath, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s content = %q, %v; want %q", name, data, readErr, want)
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedRejectsDirectoryReplacementAfterVerification(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "tree"), 0o700); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "tree", "intended.txt"), []byte("intended"), 0o600); err != nil {
		t.Fatalf("WriteFile(intended) error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "replacement"), 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "replacement", "unknown.txt"), []byte("unknown"), 0o600); err != nil {
		t.Fatalf("WriteFile(unknown) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	err = RemoveAllFromDirNoFollowChecked(rootDir, "tree", func(name string, _ os.FileInfo) error {
		if name != "tree" {
			t.Fatalf("verified entry = %q, want tree", name)
		}
		if err := os.Rename(filepath.Join(rootPath, "tree"), filepath.Join(rootPath, "original")); err != nil {
			return err
		}
		return os.Rename(filepath.Join(rootPath, "replacement"), filepath.Join(rootPath, "tree"))
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error = %v, want ErrEntryChanged", err)
	}
	for name, want := range map[string]string{
		filepath.Join("original", "intended.txt"): "intended",
		filepath.Join("tree", "unknown.txt"):      "unknown",
	} {
		data, readErr := os.ReadFile(filepath.Join(rootPath, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s content = %q, %v; want %q", name, data, readErr, want)
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedRemovesVerifiedTree(t *testing.T) {
	rootPath := t.TempDir()
	treePath := filepath.Join(rootPath, "tree")
	if err := os.MkdirAll(filepath.Join(treePath, "nested"), 0o700); err != nil {
		t.Fatalf("MkdirAll(tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(treePath, "nested", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()
	verified := make(map[string]bool)
	if err := RemoveAllFromDirNoFollowChecked(rootDir, "tree", func(name string, info os.FileInfo) error {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unexpected symlink")
		}
		verified[filepath.ToSlash(name)] = true
		return nil
	}); err != nil {
		t.Fatalf("RemoveAllFromDirNoFollowChecked() error: %v", err)
	}
	for _, name := range []string{"tree", "tree/nested", "tree/nested/child.txt"} {
		if !verified[name] {
			t.Fatalf("entry %q was not verified", name)
		}
	}
	if _, err := os.Lstat(treePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tree Lstat error = %v, want not exist", err)
	}
}
