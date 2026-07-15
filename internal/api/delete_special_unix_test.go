//go:build unix

package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestServer_DeleteIntentHandlersRejectFIFOWithoutBlocking(t *testing.T) {
	t.Run("prepare intent", func(t *testing.T) {
		server, fs, tmpDir := setupTestServer(t)
		hostPath := filepath.Join(tmpDir, "files", "delete-intent.fifo")
		if err := unix.Mkfifo(hostPath, 0o600); err != nil {
			t.Fatalf("Mkfifo() error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", strings.NewReader(string(observedDeleteIntentRequestBody(t, fs, "/delete-intent.fifo"))))
		req.Header.Set("Content-Type", "application/json")
		w := serveRequestWithTimeout(t, server, req, hostPath)
		assertSpecialDeleteTargetConflict(t, w)
		assertFIFOExists(t, hostPath)
	})

	t.Run("delete revalidation", func(t *testing.T) {
		server, fs, tmpDir := setupTestServer(t)
		ctx := context.Background()
		targetPath := "/replace-with-fifo.bin"
		if err := fs.WriteFile(ctx, targetPath, strings.NewReader("regular")); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		if err != nil {
			t.Fatalf("PrepareDeleteIntents() error: %v", err)
		}
		hostPath := filepath.Join(tmpDir, "files", "replace-with-fifo.bin")
		if err := os.Remove(hostPath); err != nil {
			t.Fatalf("Remove(regular target) error: %v", err)
		}
		if err := unix.Mkfifo(hostPath, 0o600); err != nil {
			t.Fatalf("Mkfifo(replacement) error: %v", err)
		}

		expectedPolicy := storage.DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}
		req := httptest.NewRequest(http.MethodDelete, deleteFileRequestURLWithTokens(t, "/api/v1/files"+targetPath, expectedPolicy, intent.Targets[0].Token), nil)
		w := serveRequestWithTimeout(t, server, req, hostPath)
		assertSpecialDeleteTargetConflict(t, w)
		assertFIFOExists(t, hostPath)
		if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
			t.Fatalf("trash after rejected FIFO replacement = %+v, %v; want empty", items, err)
		}
	})
}

func TestServer_DeleteIntentHandlersRejectUnixSocketAsConflict(t *testing.T) {
	t.Run("prepare intent", func(t *testing.T) {
		server, fs, tmpDir := setupTestServer(t)
		hostPath := filepath.Join(tmpDir, "files", "delete-intent.sock")
		listenUnixSocketAtAPIPath(t, hostPath)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", strings.NewReader(string(observedDeleteIntentRequestBody(t, fs, "/delete-intent.sock"))))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Router().ServeHTTP(w, req)
		assertSpecialDeleteTargetConflict(t, w)
		assertUnixSocketExists(t, hostPath)
	})

	t.Run("delete revalidation", func(t *testing.T) {
		server, fs, tmpDir := setupTestServer(t)
		ctx := context.Background()
		targetPath := "/replace-with-socket.bin"
		if err := fs.WriteFile(ctx, targetPath, strings.NewReader("regular")); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		if err != nil {
			t.Fatalf("PrepareDeleteIntents() error: %v", err)
		}
		hostPath := filepath.Join(tmpDir, "files", "replace-with-socket.bin")
		if err := os.Remove(hostPath); err != nil {
			t.Fatalf("Remove(regular target) error: %v", err)
		}
		listenUnixSocketAtAPIPath(t, hostPath)

		expectedPolicy := storage.DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}
		req := httptest.NewRequest(http.MethodDelete, deleteFileRequestURLWithTokens(t, "/api/v1/files"+targetPath, expectedPolicy, intent.Targets[0].Token), nil)
		w := httptest.NewRecorder()
		server.Router().ServeHTTP(w, req)
		assertSpecialDeleteTargetConflict(t, w)
		assertUnixSocketExists(t, hostPath)
		if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
			t.Fatalf("trash after rejected socket replacement = %+v, %v; want empty", items, err)
		}
	})
}

func TestServer_DeleteIntentHandlersRejectSymlinkDescendantWithoutMutation(t *testing.T) {
	for _, stage := range []string{"prepare intent", "delete revalidation"} {
		t.Run(stage, func(t *testing.T) {
			server, fs, tmpDir := setupTestServer(t)
			ctx := context.Background()
			if err := fs.Mkdir(ctx, "/tree"); err != nil {
				t.Fatalf("Mkdir(tree) error: %v", err)
			}
			if err := fs.WriteFile(ctx, "/tree/keep.txt", strings.NewReader("keep")); err != nil {
				t.Fatalf("WriteFile(keep) error: %v", err)
			}

			var expectedPolicy storage.DeletePolicyExpectation
			var targetToken string
			if stage == "delete revalidation" {
				intent, err := fs.PrepareDeleteIntents(ctx, []string{"/tree"}, nil)
				if err != nil {
					t.Fatalf("PrepareDeleteIntents(clean tree) error: %v", err)
				}
				expectedPolicy = storage.DeletePolicyExpectation{Mode: intent.Policy.Mode, Token: intent.Policy.Token}
				targetToken = intent.Targets[0].Token
			}

			outsidePath := filepath.Join(t.TempDir(), "outside.txt")
			if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
				t.Fatalf("WriteFile(outside) error: %v", err)
			}
			linkPath := filepath.Join(tmpDir, "files", "tree", "linked.txt")
			if err := os.Symlink(outsidePath, linkPath); err != nil {
				t.Fatalf("Symlink() error: %v", err)
			}

			var req *http.Request
			if stage == "prepare intent" {
				req = httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", strings.NewReader(string(observedDeleteIntentRequestBody(t, fs, "/tree"))))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(http.MethodDelete, deleteFileRequestURLWithTokens(t, "/api/v1/files/tree", expectedPolicy, targetToken), nil)
			}
			w := httptest.NewRecorder()
			server.Router().ServeHTTP(w, req)
			assertSpecialDeleteTargetConflict(t, w)

			if _, err := fs.Stat(ctx, "/tree/keep.txt"); err != nil {
				t.Fatalf("tree changed after rejected %s: %v", stage, err)
			}
			if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("symlink after rejected %s mode = %v, error = %v", stage, fileInfoMode(info), err)
			}
			if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
				t.Fatalf("outside target after rejected %s = %q, %v", stage, data, err)
			}
			if items, err := fs.ListTrash(ctx); err != nil || len(items) != 0 {
				t.Fatalf("trash after rejected %s = %+v, %v; want empty", stage, items, err)
			}
		})
	}
}

func TestServer_PrepareDeleteIntentRejectsRootSymlinkAsConflict(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "files", "linked.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", strings.NewReader(`{"targets":[{"path":"/linked.txt","observedIdentityToken":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	assertSpecialDeleteTargetConflict(t, w)
	if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("root symlink after rejected intent mode = %v, error = %v", fileInfoMode(info), err)
	}
	if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside" {
		t.Fatalf("outside target after rejected intent = %q, %v", data, err)
	}
}

func serveRequestWithTimeout(t *testing.T, server *Server, req *http.Request, fifoPath string) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		server.Router().ServeHTTP(w, req)
		done <- w
	}()

	select {
	case w := <-done:
		return w
	case <-time.After(time.Second):
		// Unblock a regressed blocking reader before failing the test.
		if fd, err := unix.Open(fifoPath, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
			_ = unix.Close(fd)
		}
		t.Fatal("delete target handler blocked while opening a FIFO")
		return nil
	}
}

func assertSpecialDeleteTargetConflict(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("special delete target status = %d, want %d; body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode special delete target error: %v", err)
	}
	if apiErr.Code != ErrCodeConflict || apiErr.Message != "delete target is not a regular file or directory" {
		t.Fatalf("special delete target error = %+v", apiErr)
	}
}

func assertFIFOExists(t *testing.T, hostPath string) {
	t.Helper()
	info, err := os.Lstat(hostPath)
	if err != nil {
		t.Fatalf("Lstat(FIFO) error: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("target mode = %v, want ModeNamedPipe", info.Mode())
	}
}

func assertUnixSocketExists(t *testing.T, hostPath string) {
	t.Helper()
	info, err := os.Lstat(hostPath)
	if err != nil {
		t.Fatalf("Lstat(socket) error: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("target mode = %v, want ModeSocket", info.Mode())
	}
}

func fileInfoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func listenUnixSocketAtAPIPath(t *testing.T, targetPath string) {
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
