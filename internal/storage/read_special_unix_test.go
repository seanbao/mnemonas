//go:build unix

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileSystem_ReadOpenersRejectFIFOWithoutBlocking(t *testing.T) {
	tests := []struct {
		name string
		open func(*FileSystem, context.Context, string) (*os.File, error)
	}{
		{
			name: "OpenFile",
			open: func(fs *FileSystem, ctx context.Context, filePath string) (*os.File, error) {
				return fs.OpenFile(ctx, filePath)
			},
		},
		{
			name: "OpenFileSnapshotMetadata",
			open: func(fs *FileSystem, ctx context.Context, filePath string) (*os.File, error) {
				file, _, err := fs.OpenFileSnapshotMetadata(ctx, filePath)
				return file, err
			},
		},
		{
			name: "OpenFileSnapshot",
			open: func(fs *FileSystem, ctx context.Context, filePath string) (*os.File, error) {
				file, _, err := fs.OpenFileSnapshot(ctx, filePath)
				return file, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := setupStandaloneFileSystem(t)
			const logicalPath = "/read-special.fifo"
			hostPath := filepath.Join(fs.workspace.Root(), "read-special.fifo")
			if err := unix.Mkfifo(hostPath, 0o600); err != nil {
				t.Fatalf("Mkfifo() error: %v", err)
			}

			result := make(chan error, 1)
			go func() {
				file, err := tt.open(fs, context.Background(), logicalPath)
				if file != nil {
					_ = file.Close()
				}
				result <- err
			}()

			select {
			case err := <-result:
				if !errors.Is(err, ErrNotRegular) {
					t.Fatalf("%s(FIFO) error = %v, want ErrNotRegular", tt.name, err)
				}
			case <-time.After(time.Second):
				if fd, err := unix.Open(hostPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					_ = unix.Close(fd)
				}
				t.Fatalf("%s(FIFO) blocked waiting for a writer", tt.name)
			}
		})
	}
}
