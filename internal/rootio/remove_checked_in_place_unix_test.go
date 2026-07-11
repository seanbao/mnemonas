//go:build unix

package rootio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveAllFromDirNoFollowCheckedInPlaceRemovesVerifiedTreeWithoutIsolation(t *testing.T) {
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

	wantIsolationErr := errors.New("isolation hook must not run")
	originalHook := beforeCheckedRemovalIsolation
	beforeCheckedRemovalIsolation = func(string) error { return wantIsolationErr }
	t.Cleanup(func() { beforeCheckedRemovalIsolation = originalHook })

	verified := make(map[string]bool)
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "tree", func(name string, info os.FileInfo) error {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unexpected symlink")
		}
		verified[filepath.ToSlash(name)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error: %v", err)
	}
	for _, name := range []string{"tree", "tree/nested", "tree/nested/child.txt"} {
		if !verified[name] {
			t.Fatalf("entry %q was not verified", name)
		}
	}
	if _, err := os.Lstat(treePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tree Lstat error = %v, want not exist", err)
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatalf("ReadDir(root) error: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mnemonas-remove-") {
			t.Fatalf("unexpected checked-removal isolation residue %q", entry.Name())
		}
	}
}

func TestRemoveAllFromDirNoFollowCheckedInPlaceRejectsInitiallyMissingEntry(t *testing.T) {
	rootPath := t.TempDir()
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	verifyCalled := false
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "missing.txt", func(string, os.FileInfo) error {
		verifyCalled = true
		return nil
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want ErrEntryChanged", err)
	}
	if verifyCalled {
		t.Fatal("verify called for an initially missing entry")
	}
}

func TestRemoveAllFromDirNoFollowCheckedInPlaceVerifiesRootBeforeMutation(t *testing.T) {
	rootPath := t.TempDir()
	treePath := filepath.Join(rootPath, "tree")
	if err := os.Mkdir(treePath, 0o700); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(treePath, "child.txt"), []byte("child"), 0o600); err != nil {
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

	wantErr := errors.New("journal manifest mismatch")
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "tree", func(name string, _ os.FileInfo) error {
		if name != "tree" {
			t.Fatalf("first verified entry = %q, want tree", name)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want manifest mismatch", err)
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

func TestRemoveAllFromDirNoFollowCheckedInPlaceRejectsSymlink(t *testing.T) {
	rootPath := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(rootPath, "target")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink(target) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	verifyCalled := false
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "target", func(string, os.FileInfo) error {
		verifyCalled = true
		return nil
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want ErrEntryChanged", err)
	}
	if verifyCalled {
		t.Fatal("verify called for a rejected symlink")
	}
	if info, statErr := os.Lstat(linkPath); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target symlink after rejected removal = %+v, %v", info, statErr)
	}
	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil || string(data) != "outside" {
		t.Fatalf("outside content = %q, %v; want outside", data, readErr)
	}
}

func TestRemoveAllFromDirNoFollowCheckedInPlaceRejectsFileReplacementAfterVerification(t *testing.T) {
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
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "target.txt", func(string, os.FileInfo) error {
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
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want ErrEntryChanged", err)
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

func TestRemoveAllFromDirNoFollowCheckedInPlaceDoesNotRemoveReplacedDirectory(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "tree")
	originalPath := filepath.Join(rootPath, "original")
	replacementPath := filepath.Join(rootPath, "replacement")
	if err := os.Mkdir(targetPath, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetPath, "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	if err := os.Mkdir(replacementPath, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replacementPath, "unknown.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement child) error: %v", err)
	}
	rootDir, err := os.Open(rootPath)
	if err != nil {
		t.Fatalf("Open(root) error: %v", err)
	}
	defer rootDir.Close()

	replaced := false
	err = RemoveAllFromDirNoFollowCheckedInPlace(rootDir, "tree", func(name string, _ os.FileInfo) error {
		if filepath.ToSlash(name) != "tree/child.txt" || replaced {
			return nil
		}
		replaced = true
		if err := os.Rename(targetPath, originalPath); err != nil {
			return err
		}
		return os.Rename(replacementPath, targetPath)
	})
	if !errors.Is(err, ErrEntryChanged) {
		t.Fatalf("RemoveAllFromDirNoFollowCheckedInPlace() error = %v, want ErrEntryChanged", err)
	}
	if !replaced {
		t.Fatal("test did not replace the directory during traversal")
	}
	data, readErr := os.ReadFile(filepath.Join(targetPath, "unknown.txt"))
	if readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement content = %q, %v; want replacement", data, readErr)
	}
	if _, statErr := os.Stat(originalPath); statErr != nil {
		t.Fatalf("Stat(original) error: %v", statErr)
	}
}
