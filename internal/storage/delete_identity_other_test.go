//go:build !linux && !darwin

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

func TestFileSystemObservedDeleteIdentityIsUnavailableOnUnsupportedPlatform(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/item.bin", bytes.NewReader([]byte("item"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	info, err := fs.Stat(ctx, "/item.bin")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if info.DeleteIdentityToken != "" {
		t.Fatalf("unsupported delete identity token = %q, want empty", info.DeleteIdentityToken)
	}
	_, err = fs.PrepareObservedDeleteIntents(ctx, []ObservedDeleteTarget{{
		Path:                  "/item.bin",
		ObservedIdentityToken: strings.Repeat("a", sha256.Size*2),
	}}, nil)
	if !errors.Is(err, ErrDeleteIdentityUnavailable) {
		t.Fatalf("PrepareObservedDeleteIntents() error = %v, want ErrDeleteIdentityUnavailable", err)
	}
}

func TestFileSystemTrashPurgeIdentityFailurePrecedesJournalAndBusinessMutation(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	item := &versionstore.TrashItem{
		ID:           "unsupported-purge",
		OriginalPath: "/unsupported-purge.txt",
		Size:         int64(len("content")),
		DeletedAt:    time.Unix(1_700_000_000, 0),
		ExpiresAt:    time.Unix(1_700_000_000, 0).Add(24 * time.Hour),
	}
	itemRoot := filepath.Join(fs.trashRoot, item.ID)
	if err := os.MkdirAll(itemRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(Trash item) error: %v", err)
	}
	contentPath := filepath.Join(itemRoot, "content")
	if err := os.WriteFile(contentPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile(Trash content) error: %v", err)
	}
	if err := fs.versions.AddToTrash(ctx, item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}
	fs.readDeleteMountPoints = func() ([]string, error) { return nil, nil }

	if _, err := fs.prepareTrashPurge(ctx, item); !errors.Is(err, ErrDeleteIdentityUnavailable) {
		t.Fatalf("prepareTrashPurge() error = %v, want ErrDeleteIdentityUnavailable", err)
	}
	if data, err := os.ReadFile(contentPath); err != nil || string(data) != "content" {
		t.Fatalf("Trash content after rejected purge = %q, %v; want content", data, err)
	}
	if _, err := fs.versions.GetTrashItem(ctx, item.ID); err != nil {
		t.Fatalf("GetTrashItem() after rejected purge error: %v", err)
	}
	journalEntries, err := os.ReadDir(filepath.Join(fs.trashRoot, trashPurgeJournalDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadDir(Trash purge journal) error: %v", err)
	}
	if len(journalEntries) != 0 {
		t.Fatalf("Trash purge journal entries after identity rejection = %v, want none", journalEntries)
	}
}
