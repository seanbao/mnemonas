//go:build unix

package rootio

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenRegularFilePathNoFollowRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo() error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		file, err := OpenRegularFilePathNoFollow(path)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("OpenRegularFilePathNoFollow() accepted a FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("OpenRegularFilePathNoFollow() blocked while opening a FIFO")
	}

	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Lstat(FIFO) error: %v", err)
	}
}
