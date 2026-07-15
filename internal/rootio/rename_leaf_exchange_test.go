//go:build unix

package rootio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExchangeLeavesBetweenRoots(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "source")
	targetPath := filepath.Join(base, "target")
	if err := os.Mkdir(sourcePath, 0o700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.Mkdir(targetPath, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "new.bin"), []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetPath, "old.bin"), []byte("old"), 0o640); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	sourceRoot, err := os.OpenRoot(sourcePath)
	if err != nil {
		t.Fatalf("OpenRoot(source) error: %v", err)
	}
	defer sourceRoot.Close()
	targetRoot, err := os.OpenRoot(targetPath)
	if err != nil {
		t.Fatalf("OpenRoot(target) error: %v", err)
	}
	defer targetRoot.Close()

	if err := ExchangeLeavesBetweenRoots(sourceRoot, "new.bin", targetRoot, "old.bin"); err != nil {
		t.Fatalf("ExchangeLeavesBetweenRoots() error: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(sourcePath, "new.bin")); err != nil || string(data) != "old" {
		t.Fatalf("source after exchange = %q, %v; want old", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(targetPath, "old.bin")); err != nil || string(data) != "new" {
		t.Fatalf("target after exchange = %q, %v; want new", data, err)
	}
}

func TestExchangeLeavesBetweenRootsFailsClosedForMissingEntry(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "source")
	targetPath := filepath.Join(base, "target")
	if err := os.Mkdir(sourcePath, 0o700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.Mkdir(targetPath, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "new.bin"), []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	sourceRoot, err := os.OpenRoot(sourcePath)
	if err != nil {
		t.Fatalf("OpenRoot(source) error: %v", err)
	}
	defer sourceRoot.Close()
	targetRoot, err := os.OpenRoot(targetPath)
	if err != nil {
		t.Fatalf("OpenRoot(target) error: %v", err)
	}
	defer targetRoot.Close()

	err = ExchangeLeavesBetweenRoots(sourceRoot, "new.bin", targetRoot, "missing.bin")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ExchangeLeavesBetweenRoots(missing) error = %v, want os.ErrNotExist", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(sourcePath, "new.bin")); readErr != nil || string(data) != "new" {
		t.Fatalf("source after rejected exchange = %q, %v; want new", data, readErr)
	}
}

func TestExchangeLeavesBetweenRootsFailsClosedForMissingSource(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "target.bin"), []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	err = ExchangeLeavesBetweenRoots(root, "missing.bin", root, "target.bin")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ExchangeLeavesBetweenRoots(missing source) error = %v, want os.ErrNotExist", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(rootPath, "target.bin")); readErr != nil || string(data) != "target" {
		t.Fatalf("target after rejected exchange = %q, %v; want target", data, readErr)
	}
}

func TestExchangeLeavesBetweenRootsRejectsSameEntry(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "item.bin"), []byte("stable"), 0o600); err != nil {
		t.Fatalf("WriteFile(item) error: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	if err := ExchangeLeavesBetweenRoots(root, "item.bin", root, "item.bin"); err == nil {
		t.Fatal("ExchangeLeavesBetweenRoots(same entry) error = nil, want rejection")
	}
	if data, readErr := os.ReadFile(filepath.Join(rootPath, "item.bin")); readErr != nil || string(data) != "stable" {
		t.Fatalf("same entry after rejected exchange = %q, %v; want stable", data, readErr)
	}

	secondRoot, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot(second) error: %v", err)
	}
	defer secondRoot.Close()
	if err := ExchangeLeavesBetweenRoots(root, "item.bin", secondRoot, "item.bin"); err == nil {
		t.Fatal("ExchangeLeavesBetweenRoots(same entry via distinct roots) error = nil, want rejection")
	}
	if data, readErr := os.ReadFile(filepath.Join(rootPath, "item.bin")); readErr != nil || string(data) != "stable" {
		t.Fatalf("same entry after distinct-root rejection = %q, %v; want stable", data, readErr)
	}
}

func TestExchangeLeavesBetweenRootsRejectsEscapeAndSymlinkParents(t *testing.T) {
	rootPath := t.TempDir()
	outsidePath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "real"), 0o700); err != nil {
		t.Fatalf("Mkdir(real) error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "link")); err != nil {
		t.Fatalf("Symlink(link) error: %v", err)
	}
	for _, filePath := range []string{
		filepath.Join(rootPath, "real", "source.bin"),
		filepath.Join(rootPath, "real", "target.bin"),
		filepath.Join(outsidePath, "outside.bin"),
	} {
		if err := os.WriteFile(filePath, []byte("stable"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", filePath, err)
		}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot() error: %v", err)
	}
	defer root.Close()

	for _, test := range []struct {
		name   string
		source string
		target string
	}{
		{name: "source escape", source: "../outside.bin", target: "real/target.bin"},
		{name: "target escape", source: "real/source.bin", target: "../outside.bin"},
		{name: "source symlink parent", source: "link/outside.bin", target: "real/target.bin"},
		{name: "target symlink parent", source: "real/source.bin", target: "link/outside.bin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ExchangeLeavesBetweenRoots(root, test.source, root, test.target); err == nil {
				t.Fatal("ExchangeLeavesBetweenRoots() error = nil, want rejection")
			}
			if data, readErr := os.ReadFile(filepath.Join(rootPath, "real", "source.bin")); readErr != nil || string(data) != "stable" {
				t.Fatalf("source after rejected exchange = %q, %v; want stable", data, readErr)
			}
			if data, readErr := os.ReadFile(filepath.Join(rootPath, "real", "target.bin")); readErr != nil || string(data) != "stable" {
				t.Fatalf("target after rejected exchange = %q, %v; want stable", data, readErr)
			}
			if data, readErr := os.ReadFile(filepath.Join(outsidePath, "outside.bin")); readErr != nil || string(data) != "stable" {
				t.Fatalf("outside after rejected exchange = %q, %v; want stable", data, readErr)
			}
		})
	}
}
