//go:build unix

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileSystem_PermanentDeleteRejectsSpecialRootWithoutBlockingOrMutation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		suffix string
		create func(*testing.T, string)
		mode   os.FileMode
	}{
		{
			name:   "FIFO",
			suffix: "fifo",
			create: func(t *testing.T, hostPath string) {
				t.Helper()
				if err := unix.Mkfifo(hostPath, 0o600); err != nil {
					t.Fatalf("Mkfifo() error: %v", err)
				}
			},
			mode: os.ModeNamedPipe,
		},
		{
			name:   "Unix socket",
			suffix: "sock",
			create: func(t *testing.T, hostPath string) {
				t.Helper()
				listenUnixSocketAtStoragePath(t, hostPath)
			},
			mode: os.ModeSocket,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			ctx := context.Background()
			targetPath := "/permanent-special." + testCase.suffix
			hostPath := filepath.Join(fs.workspace.Root(), strings.TrimPrefix(targetPath, "/"))
			testCase.create(t, hostPath)

			indexCalls := 0
			originalDeleteIndex := fs.deleteFileIndex
			fs.deleteFileIndex = func(ctx context.Context, path string) error {
				indexCalls++
				return originalDeleteIndex(ctx, path)
			}
			hookCalls := 0
			fs.SetPathChangeHooks(nil, func(context.Context, string) (*PathDeleteHookResult, error) {
				hookCalls++
				return nil, nil
			})

			deleteDone := make(chan error, 1)
			go func() {
				deleteDone <- fs.PermanentDelete(ctx, targetPath)
			}()
			select {
			case err := <-deleteDone:
				if !errors.Is(err, ErrNotRegular) {
					t.Fatalf("PermanentDelete(%s) error = %v, want ErrNotRegular", testCase.name, err)
				}
			case <-time.After(time.Second):
				if testCase.mode == os.ModeNamedPipe {
					if fd, err := unix.Open(hostPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
						_ = unix.Close(fd)
					}
				}
				t.Fatalf("PermanentDelete(%s) blocked while reading a special file", testCase.name)
			}

			if indexCalls != 0 || hookCalls != 0 {
				t.Fatalf("rejected PermanentDelete(%s) side effects: index=%d hook=%d", testCase.name, indexCalls, hookCalls)
			}
			if info, err := os.Lstat(hostPath); err != nil || info.Mode()&testCase.mode == 0 {
				t.Fatalf("special target after rejected PermanentDelete(%s) mode = %v, error = %v", testCase.name, storageTestFileMode(info), err)
			}
			if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected PermanentDelete(%s) = %+v, %v; want empty", testCase.name, items, err)
			}

			writeDone := make(chan error, 1)
			go func() {
				writeDone <- fs.WriteFile(ctx, "/after-permanent-special.txt", strings.NewReader("ok"))
			}()
			select {
			case err := <-writeDone:
				if err != nil {
					t.Fatalf("WriteFile() after rejected PermanentDelete(%s) error: %v", testCase.name, err)
				}
			case <-time.After(time.Second):
				t.Fatalf("filesystem mutation lock remained blocked after rejected PermanentDelete(%s)", testCase.name)
			}
		})
	}
}

func storageTestFileMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}
