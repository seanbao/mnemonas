//go:build unix

package workspace

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspace_OpenRegularFileRejectsUnixSocket(t *testing.T) {
	w := setupWorkspace(t)
	socketPath := filepath.Join(w.Root(), "special.sock")
	listenUnixSocketAtWorkspacePath(t, socketPath)

	file, err := w.OpenRegularFile(context.Background(), "/special.sock")
	if file != nil {
		_ = file.Close()
		t.Fatal("OpenRegularFile() accepted a Unix socket")
	}
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("OpenRegularFile() error = %v, want ErrNotRegular", err)
	}
}

func TestWorkspace_WalkStrictRejectsUnixSocketRootAfterCallback(t *testing.T) {
	w := setupWorkspace(t)
	socketPath := filepath.Join(w.Root(), "special.sock")
	listenUnixSocketAtWorkspacePath(t, socketPath)

	callbackCalls := 0
	err := w.WalkStrict(context.Background(), "/special.sock", func(string, *FileInfo) error {
		callbackCalls++
		return nil
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("WalkStrict(socket root) error = %v, want ErrNotRegular", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("WalkStrict(socket root) callback calls = %d, want 1", callbackCalls)
	}
}

func listenUnixSocketAtWorkspacePath(t *testing.T, targetPath string) {
	t.Helper()
	shortDir, err := os.MkdirTemp("", "mns-")
	if err != nil {
		t.Fatalf("MkdirTemp(socket) error: %v", err)
	}
	shortPath := filepath.Join(shortDir, "s")
	listener, err := net.Listen("unix", shortPath)
	if err != nil {
		_ = os.RemoveAll(shortDir)
		t.Fatalf("Listen(unix) error: %v", err)
	}
	if err := os.Rename(shortPath, targetPath); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(shortDir)
		t.Fatalf("Rename(socket) error: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(targetPath)
		_ = os.RemoveAll(shortDir)
	})
}
