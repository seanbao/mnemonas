//go:build !linux && !darwin

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDeleteIdentityIsEmptyOnUnsupportedPlatform(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := os.WriteFile(filepath.Join(root, "item.bin"), []byte("item"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	info, err := w.Stat(context.Background(), "/item.bin")
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if info.DeleteIdentityToken != "" {
		t.Fatalf("unsupported delete identity token = %q, want empty", info.DeleteIdentityToken)
	}
}
