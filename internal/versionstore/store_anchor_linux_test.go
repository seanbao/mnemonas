//go:build linux

package versionstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchoredVersionStorePathLinuxTracksDirectoryRename(t *testing.T) {
	parent := t.TempDir()
	dbDir := filepath.Join(parent, "db")
	if err := os.Mkdir(dbDir, 0o700); err != nil {
		t.Fatalf("Mkdir(db) error: %v", err)
	}
	dirHandle, err := os.Open(dbDir)
	if err != nil {
		t.Fatalf("Open(db) error: %v", err)
	}
	defer dirHandle.Close()

	anchoredPath, err := anchoredVersionStorePath(dirHandle, "index.db")
	if err != nil {
		t.Fatalf("anchoredVersionStorePath() error: %v", err)
	}
	if !strings.HasPrefix(anchoredPath, "/proc/self/fd/") {
		t.Fatalf("anchored path = %q, want Linux procfs path", anchoredPath)
	}

	heldDir := dbDir + "-held"
	if err := os.Rename(dbDir, heldDir); err != nil {
		t.Fatalf("Rename(db) error: %v", err)
	}
	if err := os.Mkdir(dbDir, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement db) error: %v", err)
	}
	if err := os.WriteFile(anchoredPath, []byte("anchored"), 0o600); err != nil {
		t.Fatalf("WriteFile(anchored path) error: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(heldDir, "index.db")); err != nil || string(data) != "anchored" {
		t.Fatalf("held database = %q, %v; want anchored", data, err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "index.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement directory received database, stat error = %v", err)
	}
}

func TestAnchoredVersionStorePathLinuxRejectsClosedHandle(t *testing.T) {
	dirHandle, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(temp dir) error: %v", err)
	}
	if err := dirHandle.Close(); err != nil {
		t.Fatalf("Close(temp dir) error: %v", err)
	}

	if _, err := anchoredVersionStorePath(dirHandle, "index.db"); err == nil {
		t.Fatal("anchoredVersionStorePath() unexpectedly accepted a closed handle")
	}
}
