// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
)

// testDataplaneAddr is the address of the test dataplane server
const testDataplaneAddr = "127.0.0.1:9090"

// setupDataplaneClient creates a dataplane client for testing
// Returns nil if dataplane is not available
func setupDataplaneClient(t *testing.T) *dataplane.Client {
	client := dataplane.NewClient(testDataplaneAddr)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		return nil
	}

	// Check if healthy
	if _, err := client.Health(ctx); err != nil {
		client.Close()
		return nil
	}

	t.Cleanup(func() { client.Close() })
	return client
}

func setupTestHandler(t *testing.T) (*Handler, *storage.FileSystem, string) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	filesRoot := path.Join(tmpDir, "files")
	internalRoot := path.Join(tmpDir, ".mnemonas")

	fs, err := storage.New(&storage.Config{
		FilesRoot:          filesRoot,
		InternalRoot:       internalRoot,
		TrashRoot:          path.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          client,
	})
	if err != nil {
		t.Skipf("storage.New() error (CGO may be disabled): %v", err)
	}

	handler := NewHandler(Config{
		FileSystem: fs,
		Prefix:     "/dav",
		ReadOnly:   false,
		AuthType:   "none",
	})

	return handler, fs, tmpDir
}

func TestHandler_OPTIONS(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	req := httptest.NewRequest("OPTIONS", "/dav/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want %d", w.Code, http.StatusOK)
	}

	allow := w.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
		t.Errorf("Allow header = %q, missing expected methods", allow)
	}

	dav := w.Header().Get("DAV")
	if dav == "" {
		t.Error("DAV header is missing")
	}
}

func TestHandler_MKCOL(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	req := httptest.NewRequest("MKCOL", "/dav/testdir", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("MKCOL status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandler_PUT_GET(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	// Create directory first
	req := httptest.NewRequest("MKCOL", "/dav/files", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	content := "Hello, WebDAV!"

	// PUT file
	req = httptest.NewRequest("PUT", "/dav/files/test.txt", strings.NewReader(content))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("PUT status = %d, want %d", w.Code, http.StatusCreated)
	}

	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Error("PUT should return ETag header")
	}

	// GET file
	req = httptest.NewRequest("GET", "/dav/files/test.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusOK)
	}

	if w.Body.String() != content {
		t.Errorf("GET body = %q, want %q", w.Body.String(), content)
	}

	if w.Header().Get("ETag") == "" {
		t.Error("GET should return ETag header")
	}
}

func TestHandler_ConditionalGET(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/cond")
	fs.WriteFile(ctx, "/cond/file.txt", bytes.NewReader([]byte("content")))
	info, _ := fs.Stat(ctx, "/cond/file.txt")
	etag := `"` + info.ContentHash + `"`

	t.Run("If-None-Match_Hit", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNotModified {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotModified)
		}
	})

	t.Run("If-None-Match_Miss", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-None-Match", `"different-etag"`)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("If-Match_Success", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Match", etag)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("If-Match_Fail", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Errorf("status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
	})
}

func TestHandler_DELETE(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/deltest")
	fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("delete me")))

	req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify file is deleted
	_, err := fs.Stat(ctx, "/deltest/file.txt")
	if err == nil {
		t.Error("File still exists after DELETE")
	}
}

func TestHandler_HandleError_DoesNotLeakInternalDetails(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})
	w := httptest.NewRecorder()

	handler.handleError(w, errors.New("sensitive backend detail"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	body := w.Body.String()
	if strings.Contains(body, "sensitive backend detail") {
		t.Fatalf("expected internal error details to be hidden, got %q", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic internal error message, got %q", body)
	}
}

func TestHandler_COPY(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/src")
	fs.Mkdir(ctx, "/dst")
	fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me")))

	req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/dst/copied.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("COPY status = %d, want %d", w.Code, http.StatusCreated)
	}

	// Verify source still exists
	_, err := fs.Stat(ctx, "/src/file.txt")
	if err != nil {
		t.Error("Source file should still exist after COPY")
	}

	// Verify destination exists
	info, err := fs.Stat(ctx, "/dst/copied.txt")
	if err != nil {
		t.Errorf("Destination file should exist: %v", err)
	}
	if info.Size != int64(len("copy me")) {
		t.Error("Copied file has wrong size")
	}
}

func TestHandler_COPY_OverwriteFalse(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/dst/copied.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(dst) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/dst/copied.txt")
	req.Header.Set("Overwrite", "F")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("COPY overwrite=false status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}

	f, err := fs.OpenFile(ctx, "/dst/copied.txt")
	if err != nil {
		t.Fatalf("OpenFile(dst) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(dst) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("Expected destination content unchanged, got %q", string(data))
	}
}

func TestHandler_COPY_InvalidDestinationPrefix(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
	req.Header.Set("Destination", "http://localhost/other/copied.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("COPY invalid destination status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if _, err := fs.Stat(ctx, "/other/copied.txt"); err == nil {
		t.Fatal("Expected destination outside WebDAV prefix to be rejected")
	}
}

func TestHandler_MOVE(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/movetest")
	fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me")))

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/movetest/moved.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("MOVE status = %d, want %d", w.Code, http.StatusCreated)
	}

	// Verify source is gone
	_, err := fs.Stat(ctx, "/movetest/orig.txt")
	if err == nil {
		t.Error("Source file should not exist after MOVE")
	}

	// Verify destination exists
	_, err = fs.Stat(ctx, "/movetest/moved.txt")
	if err != nil {
		t.Errorf("Destination should exist: %v", err)
	}
}

func TestHandler_MOVE_OverwriteFalse(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(existing) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/movetest/existing.txt")
	req.Header.Set("Overwrite", "F")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("MOVE overwrite=false status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}

	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("Expected source file to remain after failed MOVE, got %v", err)
	}
	f, err := fs.OpenFile(ctx, "/movetest/existing.txt")
	if err != nil {
		t.Fatalf("OpenFile(existing) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(existing) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("Expected destination content unchanged, got %q", string(data))
	}
}

func TestHandler_MOVE_InvalidDestinationPrefix(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://localhost/other/moved.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("MOVE invalid destination status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("Expected source file to remain after rejected MOVE, got %v", err)
	}
}

func TestHandler_LOCK(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locktest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/locktest/file.txt", bytes.NewReader([]byte("lock me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	t.Run("ExistingResource", func(t *testing.T) {
		req := httptest.NewRequest("LOCK", "/dav/locktest/file.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("LOCK status = %d, want %d", w.Code, http.StatusOK)
		}
		if lockToken := w.Header().Get("Lock-Token"); lockToken == "" {
			t.Fatal("LOCK should return Lock-Token header")
		}
	})

	t.Run("MissingResource", func(t *testing.T) {
		req := httptest.NewRequest("LOCK", "/dav/locktest/missing.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("LOCK missing status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestHandler_UNLOCK(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/unlocktest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/unlocktest/file.txt", bytes.NewReader([]byte("unlock me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	t.Run("ExistingResourceWithToken", func(t *testing.T) {
		req := httptest.NewRequest("UNLOCK", "/dav/unlocktest/file.txt", nil)
		req.Header.Set("Lock-Token", "<opaquelocktoken:test>")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("UNLOCK status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("MissingToken", func(t *testing.T) {
		req := httptest.NewRequest("UNLOCK", "/dav/unlocktest/file.txt", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("UNLOCK missing token status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("MissingResource", func(t *testing.T) {
		req := httptest.NewRequest("UNLOCK", "/dav/unlocktest/missing.txt", nil)
		req.Header.Set("Lock-Token", "<opaquelocktoken:test>")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("UNLOCK missing resource status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestHandler_PROPFIND(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/proptest")
	fs.WriteFile(ctx, "/proptest/a.txt", bytes.NewReader([]byte("aaa")))
	fs.WriteFile(ctx, "/proptest/b.txt", bytes.NewReader([]byte("bbb")))

	t.Run("Depth0", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/dav/proptest", nil)
		req.Header.Set("Depth", "0")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusMultiStatus {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMultiStatus)
		}

		body := w.Body.String()
		if !strings.Contains(body, "proptest") {
			t.Error("Response should contain directory name")
		}
	})

	t.Run("Depth1", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/dav/proptest", nil)
		req.Header.Set("Depth", "1")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusMultiStatus {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMultiStatus)
		}

		body := w.Body.String()
		if !strings.Contains(body, "a.txt") || !strings.Contains(body, "b.txt") {
			t.Error("Response should contain child files")
		}
	})
}

func TestHandler_LOCK_UNLOCK(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	// LOCK
	req := httptest.NewRequest("LOCK", "/dav/locktest", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("LOCK status = %d, want %d", w.Code, http.StatusOK)
	}

	lockToken := w.Header().Get("Lock-Token")
	if lockToken == "" {
		t.Error("LOCK should return Lock-Token header")
	}

	// UNLOCK
	req = httptest.NewRequest("UNLOCK", "/dav/locktest", nil)
	req.Header.Set("Lock-Token", lockToken)
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("UNLOCK status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandler_ReadOnlyMode(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	filesRoot := path.Join(tmpDir, "files")
	internalRoot := path.Join(tmpDir, ".mnemonas")
	fs, err := storage.New(&storage.Config{
		FilesRoot:          filesRoot,
		InternalRoot:       internalRoot,
		TrashRoot:          path.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          client,
	})
	if err != nil {
		t.Skipf("storage.New() error (CGO may be disabled): %v", err)
	}

	handler := NewHandler(Config{
		FileSystem: fs,
		Prefix:     "/dav",
		ReadOnly:   true,
	})

	tests := []struct {
		method     string
		wantStatus int
	}{
		{"GET", http.StatusNotFound},      // 404 because no file
		{"OPTIONS", http.StatusOK},        // Read allowed
		{"PROPFIND", http.StatusNotFound}, // Read allowed (404 because no file)
		{"PUT", http.StatusForbidden},     // Write blocked
		{"DELETE", http.StatusForbidden},  // Write blocked
		{"MKCOL", http.StatusForbidden},   // Write blocked
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			var body io.Reader
			if tt.method == "PUT" {
				body = strings.NewReader("test")
			}
			req := httptest.NewRequest(tt.method, "/dav/test", body)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("%s status = %d, want %d", tt.method, w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandler_BasicAuth(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	filesRoot := path.Join(tmpDir, "files")
	internalRoot := path.Join(tmpDir, ".mnemonas")
	fs, err := storage.New(&storage.Config{
		FilesRoot:          filesRoot,
		InternalRoot:       internalRoot,
		TrashRoot:          path.Join(internalRoot, "trash"),
		TrashRetentionDays: 30,
		Dataplane:          client,
	})
	if err != nil {
		t.Skipf("storage.New() error (CGO may be disabled): %v", err)
	}

	handler := NewHandler(Config{
		FileSystem: fs,
		Prefix:     "/dav",
		AuthType:   "basic",
		Username:   "testuser",
		Password:   "testpass",
	})

	t.Run("NoAuth", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/dav/", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}

		wwwAuth := w.Header().Get("WWW-Authenticate")
		if !strings.Contains(wwwAuth, "Basic") {
			t.Error("Should return WWW-Authenticate header")
		}
	})

	t.Run("WrongAuth", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/dav/", nil)
		req.SetBasicAuth("wrong", "wrong")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("CorrectAuth", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/dav/", nil)
		req.SetBasicAuth("testuser", "testpass")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestHandler_PathTraversal(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	tests := []string{
		"/dav/../etc/passwd",
		"/dav/test/../../etc/passwd",
		"/dav/..%2F..%2Fetc/passwd",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Should either return 400 Bad Request or sanitized 404
			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				t.Errorf("Path traversal should be blocked, got status %d", w.Code)
			}
		})
	}
}

func TestHandler_HeadRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/head")
	fs.WriteFile(ctx, "/head/file.txt", bytes.NewReader([]byte("test content")))

	req := httptest.NewRequest("HEAD", "/dav/head/file.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want %d", w.Code, http.StatusOK)
	}

	if w.Body.Len() != 0 {
		t.Error("HEAD response should have no body")
	}

	contentLen := w.Header().Get("Content-Length")
	if contentLen != "12" {
		t.Errorf("Content-Length = %s, want 12", contentLen)
	}
}

func TestHandler_DirectoryListing(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/listing")
	fs.WriteFile(ctx, "/listing/file1.txt", bytes.NewReader([]byte("a")))
	fs.WriteFile(ctx, "/listing/file2.txt", bytes.NewReader([]byte("b")))

	req := httptest.NewRequest("GET", "/dav/listing/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET dir status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "file1.txt") || !strings.Contains(body, "file2.txt") {
		t.Error("Directory listing should contain file names")
	}

	if !strings.Contains(body, "Index of") {
		t.Error("Directory listing should have title")
	}
}

func TestMatchETag(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		condition string
		etag      string
		want      bool
	}{
		{`"abc123"`, `"abc123"`, true},
		{`"abc123"`, `"xyz789"`, false},
		{`*`, `"anything"`, true},
		{`"a", "b", "c"`, `"b"`, true},
		{`"a", "b", "c"`, `"d"`, false},
		{`W/"abc"`, `"abc"`, true},
		{`"abc"`, `W/"abc"`, true},
	}

	for _, tt := range tests {
		got := h.matchETag(tt.condition, tt.etag)
		if got != tt.want {
			t.Errorf("matchETag(%q, %q) = %v, want %v", tt.condition, tt.etag, got, tt.want)
		}
	}
}
