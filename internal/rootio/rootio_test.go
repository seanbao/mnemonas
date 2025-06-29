package rootio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFileNoFollowRejectsFinalSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target.txt) error: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(rootPath, "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	file, err := OpenFileNoFollow(root, "linked.txt", os.O_RDONLY, 0)
	if !IsSymlinkError(err) {
		t.Fatalf("OpenFileNoFollow() error = %v, want ErrSymlink", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for symlink")
	}
}

func TestOpenFileNoFollowRejectsFinalSymlinkWhenCreating(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target.txt) error: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(rootPath, "linked.txt")); err != nil {
		t.Fatalf("Symlink(linked.txt) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	file, err := OpenFileNoFollow(root, "linked.txt", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if !IsSymlinkError(err) {
		t.Fatalf("OpenFileNoFollow(create) error = %v, want ErrSymlink", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for symlink")
	}
}

func TestOpenFileNoFollowRejectsParentSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "real"), 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "real", "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(real/target.txt) error: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	file, err := OpenFileNoFollow(root, filepath.Join("linked", "target.txt"), os.O_RDONLY, 0)
	if !IsSymlinkError(err) {
		t.Fatalf("OpenFileNoFollow() error = %v, want ErrSymlink", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for symlink parent")
	}
}

func TestOpenFileNoFollowRejectsParentSegmentBeforeCleaning(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "dir"), 0755); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target.txt) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	file, err := OpenFileNoFollow(root, "dir"+string(filepath.Separator)+".."+string(filepath.Separator)+"target.txt", os.O_RDONLY, 0)
	if !errors.Is(err, errEscape) {
		t.Fatalf("OpenFileNoFollow() error = %v, want errEscape", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for path with parent segment")
	}
}

func TestOpenDirNoFollowOpensRealDirectory(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "dir"), 0755); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	dir, err := OpenDirNoFollow(root, "dir")
	if err != nil {
		t.Fatalf("OpenDirNoFollow() error: %v", err)
	}
	defer dir.Close()
	if _, err := dir.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadDir() error: %v", err)
	}
}

func TestMkdirAllNoFollowRejectsParentSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "real"), 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = MkdirAllNoFollow(root, filepath.Join("linked", "child"), 0755)
	if !IsSymlinkError(err) {
		t.Fatalf("MkdirAllNoFollow() error = %v, want ErrSymlink", err)
	}
	if _, statErr := os.Stat(filepath.Join(rootPath, "real", "child")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected symlink target child not to be created, got %v", statErr)
	}
}

func TestMkdirAllNoFollowRejectsParentSegmentBeforeCleaning(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "dir"), 0755); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = MkdirAllNoFollow(root, "dir"+string(filepath.Separator)+".."+string(filepath.Separator)+"created", 0755)
	if !errors.Is(err, errEscape) {
		t.Fatalf("MkdirAllNoFollow() error = %v, want errEscape", err)
	}
	if _, statErr := os.Stat(filepath.Join(rootPath, "created")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected parent-cleaned directory not to be created, got %v", statErr)
	}
}

func TestMkdirNoFollowCreatesDirectoryInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "parent"), 0755); err != nil {
		t.Fatalf("Mkdir(parent) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := MkdirNoFollow(root, filepath.Join("parent", "child"), 0750); err != nil {
		t.Fatalf("MkdirNoFollow() error: %v", err)
	}
	info, err := os.Stat(filepath.Join(rootPath, "parent", "child"))
	if err != nil {
		t.Fatalf("Stat(child) error: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected child to be a directory")
	}
}

func TestMkdirNoFollowRejectsParentSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "real"), 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = MkdirNoFollow(root, filepath.Join("linked", "child"), 0755)
	if !IsSymlinkError(err) {
		t.Fatalf("MkdirNoFollow() error = %v, want ErrSymlink", err)
	}
	if _, statErr := os.Stat(filepath.Join(rootPath, "real", "child")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected symlink target child not to be created, got %v", statErr)
	}
}

func TestMkdirNoFollowRejectsFinalSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "real"), 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = MkdirNoFollow(root, "linked", 0755)
	if !IsSymlinkError(err) {
		t.Fatalf("MkdirNoFollow() error = %v, want ErrSymlink", err)
	}
}

func TestMkdirAllPathNoFollowRejectsParentSymlink(t *testing.T) {
	rootPath := t.TempDir()
	realParent := filepath.Join(rootPath, "real")
	if err := os.Mkdir(realParent, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedParent := filepath.Join(rootPath, "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	err := MkdirAllPathNoFollow(filepath.Join(linkedParent, "child"), 0755)
	if !IsSymlinkError(err) {
		t.Fatalf("MkdirAllPathNoFollow() error = %v, want ErrSymlink", err)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "child")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected symlink target child not to be created, got %v", statErr)
	}
}

func TestMkdirAllPathNoFollowTrackedReturnsOnlyCreatedDirectories(t *testing.T) {
	rootPath := t.TempDir()
	existing := filepath.Join(rootPath, "existing")
	if err := os.Mkdir(existing, 0755); err != nil {
		t.Fatalf("Mkdir(existing) error: %v", err)
	}

	created, err := MkdirAllPathNoFollowTracked(filepath.Join(existing, "parent", "child"), 0750)
	if err != nil {
		t.Fatalf("MkdirAllPathNoFollowTracked() error: %v", err)
	}

	want := []string{
		filepath.Join(existing, "parent", "child"),
		filepath.Join(existing, "parent"),
	}
	if len(created) != len(want) {
		t.Fatalf("created dirs = %v, want %v", created, want)
	}
	for i := range want {
		if created[i] != want[i] {
			t.Fatalf("created dirs = %v, want %v", created, want)
		}
	}
	if _, err := os.Stat(want[0]); err != nil {
		t.Fatalf("Stat(child) error: %v", err)
	}
}

func TestMkdirAllPathNoFollowTrackedRejectsParentSymlink(t *testing.T) {
	rootPath := t.TempDir()
	realParent := filepath.Join(rootPath, "real")
	if err := os.Mkdir(realParent, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedParent := filepath.Join(rootPath, "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	created, err := MkdirAllPathNoFollowTracked(filepath.Join(linkedParent, "child"), 0755)
	if !IsSymlinkError(err) {
		t.Fatalf("MkdirAllPathNoFollowTracked() error = %v, want ErrSymlink", err)
	}
	if len(created) != 0 {
		t.Fatalf("created dirs = %v, want none", created)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "child")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected symlink target child not to be created, got %v", statErr)
	}
}

func TestOpenFilePathNoFollowRejectsFinalSymlink(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	linkedPath := filepath.Join(rootPath, "linked.txt")
	if err := os.Symlink(targetPath, linkedPath); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	file, err := OpenFilePathNoFollow(linkedPath, os.O_RDONLY, 0)
	if !IsSymlinkError(err) {
		t.Fatalf("OpenFilePathNoFollow() error = %v, want ErrSymlink", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for symlink")
	}
}

func TestOpenFilePathNoFollowRejectsParentSegmentBeforeCleaning(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "dir"), 0755); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}
	targetPath := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(targetPath, []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target.txt) error: %v", err)
	}

	pathWithParent := rootPath + string(filepath.Separator) + "dir" + string(filepath.Separator) + ".." + string(filepath.Separator) + "target.txt"
	file, err := OpenFilePathNoFollow(pathWithParent, os.O_RDONLY, 0)
	if !errors.Is(err, errEscape) {
		t.Fatalf("OpenFilePathNoFollow() error = %v, want errEscape", err)
	}
	if file != nil {
		_ = file.Close()
		t.Fatal("expected no file handle for path with parent segment")
	}
}

func TestOpenDirPathNoFollowRejectsFinalSymlink(t *testing.T) {
	rootPath := t.TempDir()
	realDir := filepath.Join(rootPath, "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	linkedDir := filepath.Join(rootPath, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	dir, err := OpenDirPathNoFollow(linkedDir)
	if !IsSymlinkError(err) {
		t.Fatalf("OpenDirPathNoFollow() error = %v, want ErrSymlink", err)
	}
	if dir != nil {
		_ = dir.Close()
		t.Fatal("expected no directory handle for symlink")
	}
}

func TestOpenDirPathNoFollowRejectsParentSymlink(t *testing.T) {
	rootPath := t.TempDir()
	realParent := filepath.Join(rootPath, "real")
	if err := os.MkdirAll(filepath.Join(realParent, "child"), 0755); err != nil {
		t.Fatalf("MkdirAll(real/child) error: %v", err)
	}
	linkedParent := filepath.Join(rootPath, "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	dir, err := OpenDirPathNoFollow(filepath.Join(linkedParent, "child"))
	if !IsSymlinkError(err) {
		t.Fatalf("OpenDirPathNoFollow() error = %v, want ErrSymlink", err)
	}
	if dir != nil {
		_ = dir.Close()
		t.Fatal("expected no directory handle for symlink parent")
	}
}

func TestOpenDirPathNoFollowOpensRealDirectory(t *testing.T) {
	rootPath := t.TempDir()
	dirPath := filepath.Join(rootPath, "real")
	if err := os.Mkdir(dirPath, 0755); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}

	dir, err := OpenDirPathNoFollow(dirPath)
	if err != nil {
		t.Fatalf("OpenDirPathNoFollow() error: %v", err)
	}
	defer dir.Close()
	if _, err := dir.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadDir() error: %v", err)
	}
}

func TestReplaceEmptyDirPathNoFollowRejectsOldSymlinkBeforeRemovingTarget(t *testing.T) {
	rootPath := t.TempDir()
	realOld := filepath.Join(rootPath, "real-old")
	oldLink := filepath.Join(rootPath, "old-link")
	target := filepath.Join(rootPath, "target")
	if err := os.Mkdir(realOld, 0755); err != nil {
		t.Fatalf("Mkdir(real-old) error: %v", err)
	}
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.Symlink(realOld, oldLink); err != nil {
		t.Fatalf("Symlink(old-link) error: %v", err)
	}

	err := ReplaceEmptyDirPathNoFollow(oldLink, target)
	if !IsSymlinkError(err) {
		t.Fatalf("ReplaceEmptyDirPathNoFollow() error = %v, want ErrSymlink", err)
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		t.Fatalf("target was altered, stat = (%v, %v)", info, statErr)
	}
}

func TestReplaceFilePathNoFollowRejectsOldSymlink(t *testing.T) {
	rootPath := t.TempDir()
	realOld := filepath.Join(rootPath, "real-old.txt")
	oldLink := filepath.Join(rootPath, "old-link.txt")
	target := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(realOld, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile(real-old) error: %v", err)
	}
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	if err := os.Symlink(realOld, oldLink); err != nil {
		t.Fatalf("Symlink(old-link) error: %v", err)
	}

	err := ReplaceFilePathNoFollow(oldLink, target)
	if !IsSymlinkError(err) {
		t.Fatalf("ReplaceFilePathNoFollow() error = %v, want ErrSymlink", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target) error: %v", err)
	}
	if string(data) != "target" {
		t.Fatalf("target content = %q, want target", data)
	}
}

func TestReplaceFilePathNoFollowRejectsNewSymlink(t *testing.T) {
	rootPath := t.TempDir()
	oldPath := filepath.Join(rootPath, "old.txt")
	realTarget := filepath.Join(rootPath, "real-target.txt")
	targetLink := filepath.Join(rootPath, "target-link.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}
	if err := os.WriteFile(realTarget, []byte("target"), 0644); err != nil {
		t.Fatalf("WriteFile(real-target) error: %v", err)
	}
	if err := os.Symlink(realTarget, targetLink); err != nil {
		t.Fatalf("Symlink(target-link) error: %v", err)
	}

	err := ReplaceFilePathNoFollow(oldPath, targetLink)
	if !IsSymlinkError(err) {
		t.Fatalf("ReplaceFilePathNoFollow() error = %v, want ErrSymlink", err)
	}
	data, err := os.ReadFile(realTarget)
	if err != nil {
		t.Fatalf("ReadFile(real-target) error: %v", err)
	}
	if string(data) != "target" {
		t.Fatalf("real target content = %q, want target", data)
	}
	if _, statErr := os.Lstat(oldPath); statErr != nil {
		t.Fatalf("old file was moved on failure: %v", statErr)
	}
}

func TestRenamePathIntoDirNoFollowMovesDirectory(t *testing.T) {
	rootPath := t.TempDir()
	sourceDir := filepath.Join(rootPath, "source")
	targetDir := filepath.Join(rootPath, "target")
	sourceEntry := filepath.Join(sourceDir, "docs")
	if err := os.MkdirAll(filepath.Join(sourceEntry, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(source entry) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceEntry, "nested", "note.txt"), []byte("note"), 0644); err != nil {
		t.Fatalf("WriteFile(note) error: %v", err)
	}
	if err := os.Mkdir(targetDir, 0755); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}

	if err := RenamePathIntoDirNoFollow(sourceEntry, targetDir, "docs"); err != nil {
		t.Fatalf("RenamePathIntoDirNoFollow() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "docs", "nested", "note.txt")); err != nil {
		t.Fatalf("moved note stat error: %v", err)
	}
	if _, err := os.Stat(sourceEntry); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source entry stat error = %v, want not exist", err)
	}
}

func TestRenamePathIntoDirNoFollowRejectsTargetDirSymlink(t *testing.T) {
	rootPath := t.TempDir()
	sourceDir := filepath.Join(rootPath, "source")
	sourceEntry := filepath.Join(sourceDir, "docs")
	outside := filepath.Join(rootPath, "outside")
	targetLink := filepath.Join(rootPath, "target-link")
	if err := os.MkdirAll(sourceEntry, 0755); err != nil {
		t.Fatalf("MkdirAll(source entry) error: %v", err)
	}
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatalf("Mkdir(outside) error: %v", err)
	}
	if err := os.Symlink(outside, targetLink); err != nil {
		t.Fatalf("Symlink(target-link) error: %v", err)
	}

	err := RenamePathIntoDirNoFollow(sourceEntry, targetLink, "docs")
	if !IsSymlinkError(err) {
		t.Fatalf("RenamePathIntoDirNoFollow() error = %v, want ErrSymlink", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "docs")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside docs stat error = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(sourceEntry); statErr != nil {
		t.Fatalf("source entry was moved on failure: %v", statErr)
	}
}

func TestRenamePathIntoDirNoFollowRejectsExistingTarget(t *testing.T) {
	rootPath := t.TempDir()
	sourceDir := filepath.Join(rootPath, "source")
	targetDir := filepath.Join(rootPath, "target")
	sourceEntry := filepath.Join(sourceDir, "docs")
	targetEntry := filepath.Join(targetDir, "docs")
	if err := os.MkdirAll(sourceEntry, 0755); err != nil {
		t.Fatalf("MkdirAll(source entry) error: %v", err)
	}
	if err := os.MkdirAll(targetEntry, 0755); err != nil {
		t.Fatalf("MkdirAll(target entry) error: %v", err)
	}

	err := RenamePathIntoDirNoFollow(sourceEntry, targetDir, "docs")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("RenamePathIntoDirNoFollow() error = %v, want ErrExist", err)
	}
	if _, statErr := os.Stat(sourceEntry); statErr != nil {
		t.Fatalf("source entry was moved on failure: %v", statErr)
	}
	if _, statErr := os.Stat(targetEntry); statErr != nil {
		t.Fatalf("target entry was altered on failure: %v", statErr)
	}
}

func TestRenameNoFollowRejectsExistingTarget(t *testing.T) {
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

	err = RenameNoFollow(root, "source.txt", "target.txt")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("RenameNoFollow() error = %v, want ErrExist", err)
	}
	sourceData, err := os.ReadFile(filepath.Join(rootPath, "source.txt"))
	if err != nil {
		t.Fatalf("ReadFile(source) error: %v", err)
	}
	if string(sourceData) != "source" {
		t.Fatalf("source content = %q, want source", sourceData)
	}
	targetData, err := os.ReadFile(filepath.Join(rootPath, "target.txt"))
	if err != nil {
		t.Fatalf("ReadFile(target) error: %v", err)
	}
	if string(targetData) != "target" {
		t.Fatalf("target content = %q, want target", targetData)
	}
}

func TestRenameNoFollowUsesAnchoredRootHandleAfterPathSwap(t *testing.T) {
	tmpDir := t.TempDir()
	rootPath := filepath.Join(tmpDir, "root")
	backupRoot := filepath.Join(tmpDir, "root-backup")
	outsideRoot := filepath.Join(tmpDir, "outside")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatalf("Mkdir(root) error: %v", err)
	}
	if err := os.Mkdir(outsideRoot, 0755); err != nil {
		t.Fatalf("Mkdir(outside) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := os.Rename(rootPath, backupRoot); err != nil {
		t.Fatalf("Rename(root backup) error: %v", err)
	}
	if err := os.Symlink(outsideRoot, rootPath); err != nil {
		t.Fatalf("Symlink(root) error: %v", err)
	}

	if err := RenameNoFollow(root, "source.txt", "target.txt"); err != nil {
		t.Fatalf("RenameNoFollow() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, "source.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source in anchored root stat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(backupRoot, "target.txt"))
	if err != nil {
		t.Fatalf("ReadFile(anchored target) error: %v", err)
	}
	if string(data) != "source" {
		t.Fatalf("anchored target content = %q, want source", data)
	}
	if _, err := os.Stat(filepath.Join(outsideRoot, "target.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside target stat error = %v, want not exist", err)
	}
}

func TestRemoveAllNoFollowRejectsParentSymlinkInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	realParent := filepath.Join(rootPath, "real")
	if err := os.MkdirAll(filepath.Join(realParent, "target"), 0755); err != nil {
		t.Fatalf("MkdirAll(real/target) error: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = RemoveAllNoFollow(root, filepath.Join("linked", "target"))
	if !IsSymlinkError(err) {
		t.Fatalf("RemoveAllNoFollow() error = %v, want ErrSymlink", err)
	}
	if info, statErr := os.Stat(filepath.Join(realParent, "target")); statErr != nil || !info.IsDir() {
		t.Fatalf("real target was altered, stat = (%v, %v)", info, statErr)
	}
}

func TestRemoveAllPathNoFollowRejectsParentSymlink(t *testing.T) {
	rootPath := t.TempDir()
	outside := filepath.Join(rootPath, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "target"), 0755); err != nil {
		t.Fatalf("MkdirAll(outside/target) error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("Symlink(linked) error: %v", err)
	}

	err := RemoveAllPathNoFollow(filepath.Join(rootPath, "linked", "target"))
	if !IsSymlinkError(err) {
		t.Fatalf("RemoveAllPathNoFollow() error = %v, want ErrSymlink", err)
	}
	if info, statErr := os.Stat(filepath.Join(outside, "target")); statErr != nil || !info.IsDir() {
		t.Fatalf("outside target was altered, stat = (%v, %v)", info, statErr)
	}
}

func TestRemoveAllPathNoFollowRejectsParentSegmentBeforeCleaning(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "dir"), 0755); err != nil {
		t.Fatalf("Mkdir(dir) error: %v", err)
	}
	target := filepath.Join(rootPath, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}

	pathWithParent := rootPath + string(filepath.Separator) + "dir" + string(filepath.Separator) + ".." + string(filepath.Separator) + "target"
	err := RemoveAllPathNoFollow(pathWithParent)
	if !errors.Is(err, errEscape) {
		t.Fatalf("RemoveAllPathNoFollow() error = %v, want errEscape", err)
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		t.Fatalf("target was altered, stat = (%v, %v)", info, statErr)
	}
}

func TestRemoveAllNoFollowRemovesChildSymlinkWithoutFollowing(t *testing.T) {
	rootPath := t.TempDir()
	targetDir := filepath.Join(rootPath, "target")
	if err := os.MkdirAll(filepath.Join(targetDir, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(target/nested) error: %v", err)
	}
	outsideFile := filepath.Join(rootPath, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0644); err != nil {
		t.Fatalf("WriteFile(outside.txt) error: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(targetDir, "nested", "outside-link")); err != nil {
		t.Fatalf("Symlink(outside-link) error: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RemoveAllNoFollow(root, "target"); err != nil {
		t.Fatalf("RemoveAllNoFollow() error: %v", err)
	}
	if _, statErr := os.Stat(targetDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target stat error = %v, want not exist", statErr)
	}
	data, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatalf("ReadFile(outside.txt) error: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside file content = %q, want outside", data)
	}
}

func TestRemoveAllNoFollowIgnoresMissingPath(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := RemoveAllNoFollow(root, filepath.Join("missing", "target")); err != nil {
		t.Fatalf("RemoveAllNoFollow(missing target) error: %v", err)
	}
}
