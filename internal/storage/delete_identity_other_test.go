//go:build !linux && !darwin

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
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
