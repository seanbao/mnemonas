//go:build darwin

package versionstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/seanbao/mnemonas/internal/dataplane"
)

func TestAnchoredVersionStorePathDarwinTracksDirectoryRename(t *testing.T) {
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

	heldInfo, err := dirHandle.Stat()
	if err != nil {
		t.Fatalf("Stat(db handle) error: %v", err)
	}
	stat, ok := heldInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("directory stat does not expose syscall.Stat_t")
	}

	anchoredPath, err := anchoredVersionStorePath(dirHandle, "index.db")
	if err != nil {
		t.Fatalf("anchoredVersionStorePath() error: %v", err)
	}
	wantPrefix := filepath.Join(
		"/.vol",
		fmt.Sprintf("%d", uint64(uint32(stat.Dev))),
		fmt.Sprintf("%d", stat.Ino),
	)
	if !strings.HasPrefix(anchoredPath, wantPrefix+string(filepath.Separator)) {
		t.Fatalf("anchored path = %q, want prefix %q", anchoredPath, wantPrefix)
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

func TestNewDarwinDBRootKeepsWALSidecarsOnAnchoredDirectory(t *testing.T) {
	parent := t.TempDir()
	dbDir := filepath.Join(parent, "db")
	if err := os.Mkdir(dbDir, 0o700); err != nil {
		t.Fatalf("Mkdir(db) error: %v", err)
	}
	root, err := os.OpenRoot(dbDir)
	if err != nil {
		t.Fatalf("OpenRoot(db) error: %v", err)
	}
	defer root.Close()

	heldDir := dbDir + "-held"
	if err := os.Rename(dbDir, heldDir); err != nil {
		t.Fatalf("Rename(db) error: %v", err)
	}
	if err := os.Mkdir(dbDir, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement db) error: %v", err)
	}

	store, err := New(Config{
		DBRoot:    root,
		DBName:    "index.db",
		Dataplane: dataplane.NewClient("unused"),
	})
	if err != nil {
		t.Fatalf("New(DBRoot) error: %v", err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TABLE darwin_anchor_canary (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create canary table error: %v", err)
	}

	for _, name := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		if _, err := os.Stat(filepath.Join(heldDir, name)); err != nil {
			t.Fatalf("anchored %s stat error: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dbDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("replacement directory received %s, stat error = %v", name, err)
		}
	}
}
