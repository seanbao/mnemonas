//go:build unix

package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileSystem_DeleteRejectsFileReplacedByFIFOBeforeTrashCopy(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	targetPath := "/replace-before-trash-copy.txt"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("regular")); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	hostPath := fs.workspace.FullPath(targetPath)

	originalAfterSourceStat := afterStorageCopySourceStat
	swapped := false
	afterStorageCopySourceStat = func(sourcePath string) error {
		if swapped || sourcePath != hostPath {
			return nil
		}
		swapped = true
		if err := os.Remove(hostPath); err != nil {
			return err
		}
		return unix.Mkfifo(hostPath, 0o600)
	}
	t.Cleanup(func() { afterStorageCopySourceStat = originalAfterSourceStat })

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
		deleteDone <- fs.Delete(ctx, targetPath)
	}()
	select {
	case err := <-deleteDone:
		if !errors.Is(err, ErrNotRegular) || !errors.Is(err, ErrTrashRecoveryRequired) {
			t.Fatalf("Delete(FIFO replacement) error = %v, want ErrNotRegular and recovery gate", err)
		}
		requireJournaledTrashRecovery(t, fs, err)
	case <-time.After(time.Second):
		if fd, err := unix.Open(hostPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
			_ = unix.Close(fd)
		}
		t.Fatal("Delete(FIFO replacement) blocked during trash copy")
	}
	if !swapped {
		t.Fatal("trash copy source was not replaced after Lstat")
	}
	if indexCalls != 0 || hookCalls != 0 {
		t.Fatalf("rejected FIFO replacement side effects: index=%d hook=%d", indexCalls, hookCalls)
	}
	if info, err := os.Lstat(hostPath); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("source FIFO after rejected deletion mode = %v, error = %v", storageTestFileMode(info), err)
	}
	if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
		t.Fatalf("trash metadata after rejected FIFO replacement = %+v, %v; want empty", items, err)
	}
	if entries, err := os.ReadDir(fs.trashRoot); err != nil || len(entries) != 1 || entries[0].Name() != trashTransferJournalDir || !entries[0].IsDir() {
		t.Fatalf("trash content after rejected FIFO replacement = %+v, %v; want only transaction evidence", entries, err)
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(ctx, "/after-trash-copy-special.txt", strings.NewReader("ok"))
	}()
	select {
	case err := <-writeDone:
		if !errors.Is(err, ErrTrashRecoveryRequired) {
			t.Fatalf("WriteFile() after rejected FIFO replacement error = %v, want recovery gate", err)
		}
	case <-time.After(time.Second):
		t.Fatal("filesystem mutation lock remained blocked after rejected FIFO replacement")
	}
}
