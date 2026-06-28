//go:build unix

package storage

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileSystem_PrepareDeleteIntentsRejectsFIFOWithoutBlocking(t *testing.T) {
	fs := setupFileSystem(t)
	fifoPath := filepath.Join(fs.workspace.Root(), "delete-intent.fifo")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo() error: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := fs.PrepareDeleteIntents(context.Background(), []string{"/delete-intent.fifo"}, nil)
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("PrepareDeleteIntents(FIFO) error = %v, want ErrNotRegular", err)
		}
	case <-time.After(time.Second):
		// Unblock a regressed blocking reader before failing the test.
		if fd, err := unix.Open(fifoPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
			_ = unix.Close(fd)
		}
		t.Fatal("PrepareDeleteIntents(FIFO) blocked waiting for a writer")
	}

	info, err := os.Lstat(fifoPath)
	if err != nil {
		t.Fatalf("Lstat(FIFO) error: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO mode = %v, want ModeNamedPipe", info.Mode())
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(context.Background(), "/after-fifo.txt", strings.NewReader("ok"))
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteFile() after rejected FIFO intent error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("filesystem mutation lock remained blocked after rejected FIFO intent")
	}
}

func TestFileSystem_PrepareDeleteIntentsAuthorizesFIFOBeforeRejectingType(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	hostFIFOPath := filepath.Join(fs.workspace.Root(), "tree", "special.fifo")
	if err := unix.Mkfifo(hostFIFOPath, 0o600); err != nil {
		t.Fatalf("Mkfifo() error: %v", err)
	}

	errDenied := errors.New("denied FIFO descendant")
	var authorized []string
	hashCalls := 0
	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		hashCalls++
		return "", errors.New("unexpected hash")
	}
	_, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		if targetPath == "/tree/special.fifo" {
			return errDenied
		}
		return nil
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("PrepareDeleteIntents(denied FIFO) error = %v, want access denial", err)
	}
	if got, want := strings.Join(authorized, "|"), "/tree|/tree/special.fifo"; got != want {
		t.Fatalf("authorized paths = %q, want %q", got, want)
	}
	if hashCalls != 0 {
		t.Fatalf("denied FIFO hash calls = %d, want 0", hashCalls)
	}
	if info, statErr := os.Lstat(hostFIFOPath); statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO after denied intent mode = %v, error = %v", infoMode(info), statErr)
	}
}

func TestFileSystem_PrepareDeleteIntentsHashesRegularFileWithSafeOpen(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/regular-target.bin", strings.NewReader("regular content")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/regular-target.bin"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].Snapshot.Root.ContentHash == "" || len(intent.Targets[0].Token) != 64 {
		t.Fatalf("regular target intent = %+v", intent.Targets)
	}
}

func TestFileSystem_DeleteRejectsFIFOWithoutBlockingOrMutation(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		targetPath string
		fifoPath   string
		prepare    func(*testing.T, *FileSystem)
	}{
		{
			name:       "root",
			targetPath: "/special.fifo",
			fifoPath:   "special.fifo",
			prepare:    func(*testing.T, *FileSystem) {},
		},
		{
			name:       "descendant",
			targetPath: "/tree",
			fifoPath:   filepath.Join("tree", "special.fifo"),
			prepare: func(t *testing.T, fs *FileSystem) {
				if err := fs.Mkdir(context.Background(), "/tree"); err != nil {
					t.Fatalf("Mkdir(tree) error: %v", err)
				}
				if err := fs.WriteFile(context.Background(), "/tree/keep.txt", strings.NewReader("keep")); err != nil {
					t.Fatalf("WriteFile(keep) error: %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fs := setupFileSystem(t)
			testCase.prepare(t, fs)
			hostFIFOPath := filepath.Join(fs.workspace.Root(), testCase.fifoPath)
			if err := unix.Mkfifo(hostFIFOPath, 0o600); err != nil {
				t.Fatalf("Mkfifo() error: %v", err)
			}

			deleteDone := make(chan error, 1)
			go func() {
				deleteDone <- fs.Delete(context.Background(), testCase.targetPath)
			}()
			select {
			case err := <-deleteDone:
				if !errors.Is(err, ErrNotRegular) {
					t.Fatalf("Delete(FIFO %s) error = %v, want ErrNotRegular", testCase.name, err)
				}
			case <-time.After(time.Second):
				if fd, err := unix.Open(hostFIFOPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					_ = unix.Close(fd)
				}
				t.Fatal("Delete(FIFO) blocked waiting for a writer")
			}

			if info, err := os.Lstat(hostFIFOPath); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("FIFO after rejected deletion mode = %v, error = %v", infoMode(info), err)
			}
			if testCase.targetPath == "/tree" {
				if _, err := fs.Stat(context.Background(), "/tree/keep.txt"); err != nil {
					t.Fatalf("tree changed after rejected FIFO deletion: %v", err)
				}
			}
			if items, err := fs.ListTrash(context.Background()); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected FIFO deletion = %+v, %v; want empty", items, err)
			}

			writeDone := make(chan error, 1)
			go func() {
				writeDone <- fs.WriteFile(context.Background(), "/after-fifo-delete.txt", strings.NewReader("ok"))
			}()
			select {
			case err := <-writeDone:
				if err != nil {
					t.Fatalf("WriteFile() after rejected FIFO deletion error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("filesystem mutation lock remained blocked after rejected FIFO deletion")
			}
		})
	}
}

func TestFileSystem_PrepareDeleteIntentsRejectsUnixSocket(t *testing.T) {
	fs := setupFileSystem(t)
	socketPath := filepath.Join(fs.workspace.Root(), "delete-intent.sock")
	listenUnixSocketAtStoragePath(t, socketPath)

	_, err := fs.PrepareDeleteIntents(context.Background(), []string{"/delete-intent.sock"}, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("PrepareDeleteIntents(socket) error = %v, want ErrNotRegular", err)
	}
	if info, statErr := os.Lstat(socketPath); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket after rejected intent mode = %v, error = %v", infoMode(info), statErr)
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fs.WriteFile(context.Background(), "/after-socket.txt", strings.NewReader("ok"))
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteFile() after rejected socket intent error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("filesystem mutation lock remained blocked after rejected socket intent")
	}
}

func TestFileSystem_DeleteRevalidationRejectsUnixSocket(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	targetPath := "/replace-with-socket.bin"
	if err := fs.WriteFile(ctx, targetPath, strings.NewReader("regular")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", err)
	}
	hostPath := fs.workspace.FullPath(targetPath)
	if err := os.Remove(hostPath); err != nil {
		t.Fatalf("Remove(regular target) error: %v", err)
	}
	listenUnixSocketAtStoragePath(t, hostPath)

	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, targetPath, DeletePolicyExpectation{
		Mode:  intent.Policy.Mode,
		Token: intent.Policy.Token,
	}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(socket) error = %v, want ErrNotRegular", err)
	}
	if info, statErr := os.Lstat(hostPath); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket after rejected deletion mode = %v, error = %v", infoMode(info), statErr)
	}
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected socket deletion = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystem_DeleteIntentRejectsSymlinkDescendantWithoutMutation(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatalf("WriteFile(keep) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(fs.workspace.Root(), "tree", "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	errDenied := errors.New("denied symlink descendant")
	var authorized []string
	_, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, func(targetPath string) error {
		authorized = append(authorized, targetPath)
		if targetPath == "/tree/linked.txt" {
			return errDenied
		}
		return nil
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("PrepareDeleteIntents(denied symlink) error = %v, want access denial", err)
	}
	if got, want := strings.Join(authorized, "|"), "/tree|/tree/keep.txt|/tree/linked.txt"; got != want {
		t.Fatalf("authorized paths = %q, want %q", got, want)
	}

	_, err = fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("PrepareDeleteIntents(symlink tree) error = %v, want ErrNotRegular", err)
	}
	assertDeleteIntentSpecialTreeUnchanged(t, fs, linkPath, outsidePath)
}

func TestFileSystem_PrepareDeleteIntentsRejectsRootSymlink(t *testing.T) {
	fs := setupFileSystem(t)
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(fs.workspace.Root(), "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	_, err := fs.PrepareDeleteIntents(context.Background(), []string{"/linked.txt"}, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("PrepareDeleteIntents(root symlink) error = %v, want ErrNotRegular", err)
	}
	if info, statErr := os.Lstat(linkPath); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("root symlink after rejected intent mode = %v, error = %v", infoMode(info), statErr)
	}
	if data, readErr := os.ReadFile(outsidePath); readErr != nil || string(data) != "outside" {
		t.Fatalf("outside target after rejected root symlink intent = %q, %v", data, readErr)
	}
}

func TestFileSystem_DeleteToTrashRejectsSymlinkDescendantWithoutMutation(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatalf("WriteFile(keep) error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(fs.workspace.Root(), "tree", "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	err := fs.Delete(ctx, "/tree")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Delete(symlink tree) error = %v, want ErrNotRegular", err)
	}
	assertDeleteIntentSpecialTreeUnchanged(t, fs, linkPath, outsidePath)
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected symlink tree deletion = %+v, %v; want empty", items, listErr)
	}
}

func TestFileSystem_DeleteRevalidationRejectsAddedSymlinkWithoutMutation(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tree"); err != nil {
		t.Fatalf("Mkdir(tree) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatalf("WriteFile(keep) error: %v", err)
	}
	intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(clean tree) error: %v", err)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(fs.workspace.Root(), "tree", "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	err = fs.DeleteWithExpectedPolicyAndTarget(ctx, "/tree", DeletePolicyExpectation{
		Mode:  intent.Policy.Mode,
		Token: intent.Policy.Token,
	}, intent.Targets[0].Token, nil)
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("DeleteWithExpectedPolicyAndTarget(symlink tree) error = %v, want ErrNotRegular", err)
	}
	assertDeleteIntentSpecialTreeUnchanged(t, fs, linkPath, outsidePath)
	if items, listErr := fs.ListTrash(ctx); listErr != nil || len(items) != 0 {
		t.Fatalf("trash after rejected symlink tree deletion = %+v, %v; want empty", items, listErr)
	}
}

func assertDeleteIntentSpecialTreeUnchanged(t *testing.T, fs *FileSystem, linkPath, outsidePath string) {
	t.Helper()
	if _, err := fs.Stat(context.Background(), "/tree/keep.txt"); err != nil {
		t.Fatalf("tree changed after rejected deletion intent: %v", err)
	}
	if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink after rejected deletion intent mode = %v, error = %v", infoMode(info), err)
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside target after rejected deletion intent = %q, %v", data, err)
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func listenUnixSocketAtStoragePath(t *testing.T, targetPath string) {
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
