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

func TestHandler_MKCOL_RejectsUnknownLengthBody(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	req := httptest.NewRequest("MKCOL", "/dav/testdir", io.NopCloser(strings.NewReader("body")))
	req.ContentLength = -1
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("MKCOL unknown-length body status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
	if !strings.Contains(w.Body.String(), "MKCOL does not allow request body") {
		t.Fatalf("expected MKCOL body rejection message, got %q", w.Body.String())
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

func TestHandler_PUT_ToExistingDirectoryReturnsConflict(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/files"); err != nil {
		t.Fatalf("Mkdir(/files) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/files/existing-dir"); err != nil {
		t.Fatalf("Mkdir(existing-dir) error: %v", err)
	}

	req := httptest.NewRequest("PUT", "/dav/files/existing-dir", strings.NewReader("content"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("PUT to directory status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandler_PUT_IfMatchStarOnMissingFileFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/files"); err != nil {
		t.Fatalf("Mkdir(/files) error: %v", err)
	}

	req := httptest.NewRequest("PUT", "/dav/files/missing.txt", strings.NewReader("content"))
	req.Header.Set("If-Match", "*")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("PUT If-Match:* on missing file status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}
	if _, err := fs.Stat(ctx, "/files/missing.txt"); err == nil {
		t.Fatal("expected missing file to remain absent after failed conditional PUT")
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

func TestHandler_HandleError_MapsLockedAndTypeConflicts(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})

	t.Run("Locked", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.handleError(w, storage.ErrFileLocked)
		if w.Code != http.StatusLocked {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("IsDir", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.handleError(w, storage.ErrIsDir)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})
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

func TestHandler_COPY_OverwriteTrueReturnsNoContent(t *testing.T) {
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
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("COPY overwrite=true status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandler_COPY_DirectoryRecursive(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/srcdir"); err != nil {
		t.Fatalf("Mkdir(srcdir) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/srcdir/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/srcdir/root.txt", bytes.NewReader([]byte("root"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/srcdir/nested/child.txt", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://localhost/dav/dst/copied-dir")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("COPY directory status = %d, want %d", w.Code, http.StatusCreated)
	}

	if _, err := fs.Stat(ctx, "/dst/copied-dir"); err != nil {
		t.Fatalf("Expected copied directory to exist, got %v", err)
	}

	rootFile, err := fs.OpenFile(ctx, "/dst/copied-dir/root.txt")
	if err != nil {
		t.Fatalf("OpenFile(root copy) error: %v", err)
	}
	defer rootFile.Close()
	rootData, err := io.ReadAll(rootFile)
	if err != nil {
		t.Fatalf("ReadAll(root copy) error: %v", err)
	}
	if string(rootData) != "root" {
		t.Fatalf("Expected root file content, got %q", string(rootData))
	}

	childFile, err := fs.OpenFile(ctx, "/dst/copied-dir/nested/child.txt")
	if err != nil {
		t.Fatalf("OpenFile(child copy) error: %v", err)
	}
	defer childFile.Close()
	childData, err := io.ReadAll(childFile)
	if err != nil {
		t.Fatalf("ReadAll(child copy) error: %v", err)
	}
	if string(childData) != "child" {
		t.Fatalf("Expected child file content, got %q", string(childData))
	}
}

func TestHandler_COPY_DirectoryIntoDescendantRejected(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/srcdir"); err != nil {
		t.Fatalf("Mkdir(srcdir) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/srcdir/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/srcdir/root.txt", bytes.NewReader([]byte("root"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://localhost/dav/srcdir/nested/copied-dir")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("COPY directory into descendant status = %d, want %d", w.Code, http.StatusConflict)
	}
	if _, err := fs.Stat(ctx, "/srcdir/nested/copied-dir"); err == nil {
		t.Fatal("Expected descendant destination to remain absent after rejected COPY")
	}
}

func TestHandler_COPY_DirectoryOverwriteExistingDestinationRejected(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/srcdir"); err != nil {
		t.Fatalf("Mkdir(srcdir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/srcdir/root.txt", bytes.NewReader([]byte("root"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/dst/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(existing) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://localhost/dav/dst")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("COPY directory overwrite existing destination status = %d, want %d", w.Code, http.StatusConflict)
	}

	f, err := fs.OpenFile(ctx, "/dst/existing.txt")
	if err != nil {
		t.Fatalf("OpenFile(existing) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(existing) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("Expected existing destination content unchanged, got %q", string(data))
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

func TestHandler_COPY_SameSourceAndDestinationRejected(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/src/file.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("COPY same source/destination status = %d, want %d", w.Code, http.StatusConflict)
	}

	f, err := fs.OpenFile(ctx, "/src/file.txt")
	if err != nil {
		t.Fatalf("OpenFile(src) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(src) error: %v", err)
	}
	if string(data) != "copy me" {
		t.Fatalf("Expected source content unchanged, got %q", string(data))
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

func TestHandler_MOVE_OverwriteTrueReturnsNoContent(t *testing.T) {
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
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("MOVE overwrite=true status = %d, want %d", w.Code, http.StatusNoContent)
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

func TestHandler_MOVE_SameSourceAndDestinationRejected(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/movetest/orig.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE same source/destination status = %d, want %d", w.Code, http.StatusConflict)
	}

	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("Expected source file to remain after rejected MOVE, got %v", err)
	}
}

func TestHandler_MOVE_DirectoryIntoDescendantRejected(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir(movetest) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/movetest/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest", nil)
	req.Header.Set("Destination", "http://localhost/dav/movetest/nested/moved")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE directory into descendant status = %d, want %d", w.Code, http.StatusConflict)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("Expected source directory to remain after rejected MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/movetest/nested/moved"); err == nil {
		t.Fatal("Expected descendant destination to remain absent after rejected MOVE")
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
		lockReq := httptest.NewRequest("LOCK", "/dav/unlocktest/file.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
		lockW := httptest.NewRecorder()
		handler.ServeHTTP(lockW, lockReq)
		if lockW.Code != http.StatusOK {
			t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
		}

		req := httptest.NewRequest("UNLOCK", "/dav/unlocktest/file.txt", nil)
		req.Header.Set("Lock-Token", lockW.Header().Get("Lock-Token"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("UNLOCK status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("MismatchedToken", func(t *testing.T) {
		lockReq := httptest.NewRequest("LOCK", "/dav/unlocktest/file.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
		lockW := httptest.NewRecorder()
		handler.ServeHTTP(lockW, lockReq)
		if lockW.Code != http.StatusOK {
			t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
		}

		req := httptest.NewRequest("UNLOCK", "/dav/unlocktest/file.txt", nil)
		req.Header.Set("Lock-Token", "<opaquelocktoken:wrong>")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("UNLOCK mismatched token status = %d, want %d", w.Code, http.StatusConflict)
		}

		unlockReq := httptest.NewRequest("UNLOCK", "/dav/unlocktest/file.txt", nil)
		unlockReq.Header.Set("Lock-Token", lockW.Header().Get("Lock-Token"))
		unlockW := httptest.NewRecorder()
		handler.ServeHTTP(unlockW, unlockReq)
		if unlockW.Code != http.StatusNoContent {
			t.Fatalf("UNLOCK cleanup status = %d, want %d", unlockW.Code, http.StatusNoContent)
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
	fs.Mkdir(ctx, "/proptest/nested")
	fs.WriteFile(ctx, "/proptest/nested/c.txt", bytes.NewReader([]byte("ccc")))

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
		if strings.Contains(body, "c.txt") {
			t.Error("Depth 1 should not contain nested child files")
		}
	})

	t.Run("DepthInfinity", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/dav/proptest", nil)
		req.Header.Set("Depth", "infinity")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusMultiStatus {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMultiStatus)
		}

		body := w.Body.String()
		if !strings.Contains(body, "nested/") || !strings.Contains(body, "c.txt") {
			t.Error("Depth infinity should contain nested resources")
		}
	})

	t.Run("InvalidDepth", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/dav/proptest", nil)
		req.Header.Set("Depth", "2")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		if !strings.Contains(w.Body.String(), errInvalidDepthHeader.Error()) {
			t.Fatalf("expected invalid depth sentinel message, got %q", w.Body.String())
		}
	})
}

func TestHandler_LOCK_UNLOCK(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/locktest", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(locktest) error: %v", err)
	}

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

	req = httptest.NewRequest("UNLOCK", "/dav/locktest", nil)
	req.Header.Set("Lock-Token", lockToken)
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("UNLOCK after release status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandler_ExpiredLockIsIgnoredAndCleanedUp(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/expired-lock.txt", bytes.NewReader([]byte("initial"))); err != nil {
		t.Fatalf("WriteFile(expired-lock.txt) error: %v", err)
	}

	handler.locksMu.Lock()
	handler.locks["/expired-lock.txt"] = webdavLock{
		token:     "opaquelocktoken:expired",
		expiresAt: time.Now().Add(-time.Minute),
	}
	handler.locksMu.Unlock()

	putReq := httptest.NewRequest("PUT", "/dav/expired-lock.txt", strings.NewReader("updated"))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)

	if putW.Code != http.StatusNoContent {
		t.Fatalf("PUT with expired lock status = %d, want %d", putW.Code, http.StatusNoContent)
	}

	handler.locksMu.Lock()
	_, exists := handler.locks["/expired-lock.txt"]
	handler.locksMu.Unlock()
	if exists {
		t.Fatal("expected expired lock to be cleaned up")
	}
}

func TestHandler_LOCK_ReplacesExpiredLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-replace.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(lock-replace.txt) error: %v", err)
	}

	handler.locksMu.Lock()
	handler.locks["/lock-replace.txt"] = webdavLock{
		token:     "opaquelocktoken:expired",
		expiresAt: time.Now().Add(-time.Minute),
	}
	handler.locksMu.Unlock()

	req := httptest.NewRequest("LOCK", "/dav/lock-replace.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("LOCK with expired existing lock status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Lock-Token") == "<opaquelocktoken:expired>" {
		t.Fatal("expected a new lock token to be issued after expired lock cleanup")
	}
}

func TestHandler_LockedResourceBlocksWritesWithoutMatchingToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locked"); err != nil {
		t.Fatalf("Mkdir(locked) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/locked/file.txt", bytes.NewReader([]byte("initial"))); err != nil {
		t.Fatalf("WriteFile(file) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/locked-dst"); err != nil {
		t.Fatalf("Mkdir(locked-dst) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/locked/file.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	t.Run("PutRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked/file.txt", strings.NewReader("updated"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("PUT without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("DeleteRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/dav/locked/file.txt", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("DELETE without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("MoveRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("MOVE", "/dav/locked/file.txt", nil)
		req.Header.Set("Destination", "http://localhost/dav/locked-dst/file.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MOVE without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("PutWithMatchingTokenSucceeds", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked/file.txt", strings.NewReader("updated"))
		req.Header.Set("Lock-Token", lockToken)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT with token status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
}

func TestHandler_LockedCollectionBlocksDescendantWritesWithoutMatchingToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locked-dir"); err != nil {
		t.Fatalf("Mkdir(locked-dir) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src/file.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/locked-dir", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	t.Run("PutChildRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked-dir/new.txt", strings.NewReader("new content"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("PUT child without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("MkcolChildRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("MKCOL", "/dav/locked-dir/new-folder", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MKCOL child without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("MoveIntoLockedCollectionRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("MOVE", "/dav/src/file.txt", nil)
		req.Header.Set("Destination", "http://localhost/dav/locked-dir/moved.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MOVE into locked collection without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("PutChildWithMatchingTokenSucceeds", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked-dir/new.txt", strings.NewReader("new content"))
		req.Header.Set("Lock-Token", lockToken)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("PUT child with token status = %d, want %d", w.Code, http.StatusCreated)
		}
	})
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

func TestHandler_LegalDoubleDotFilenameAllowed(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/dots"); err != nil {
		t.Fatalf("Mkdir(dots) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/dots/foo..txt", bytes.NewReader([]byte("ok"))); err != nil {
		t.Fatalf("WriteFile(foo..txt) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/dav/dots/foo..txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET legal double-dot filename status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ok" {
		t.Fatalf("GET legal double-dot filename body = %q, want %q", w.Body.String(), "ok")
	}
}

func TestHandler_GetDestination_AllowsDoubleDotFilename(t *testing.T) {
	handler := NewHandler(Config{Prefix: "/dav", AuthType: "none"})
	req := httptest.NewRequest("COPY", "/dav/src.txt", nil)
	req.Header.Set("Destination", "http://localhost/dav/dst/foo..txt")

	dst := handler.getDestination(req)

	if dst != "/dst/foo..txt" {
		t.Fatalf("getDestination() = %q, want %q", dst, "/dst/foo..txt")
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

func TestHandler_HeadDirectoryRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/head-dir"); err != nil {
		t.Fatalf("Mkdir(/head-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/head-dir/file.txt", bytes.NewReader([]byte("test content"))); err != nil {
		t.Fatalf("WriteFile(/head-dir/file.txt) error: %v", err)
	}

	req := httptest.NewRequest("HEAD", "/dav/head-dir/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("HEAD directory status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("HEAD directory response should have no body, got %q", w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("HEAD directory content type = %q, want %q", contentType, "text/html; charset=utf-8")
	}
}

func TestHandler_DirectoryListing(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/listing")
	fs.Mkdir(ctx, "/listing/nested")
	fs.WriteFile(ctx, "/listing/file1.txt", bytes.NewReader([]byte("a")))
	fs.WriteFile(ctx, "/listing/file2.txt", bytes.NewReader([]byte("b")))

	req := httptest.NewRequest("GET", "/dav/listing/nested/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET dir status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, `<a href="/dav/listing">..</a>`) {
		t.Error("Directory listing parent link should preserve WebDAV prefix")
	}

	if !strings.Contains(body, "Index of") {
		t.Error("Directory listing should have title")
	}
}

func TestHandler_WriteExpectedWebDAVError_SanitizesUnexpectedError(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})
	w := httptest.NewRecorder()

	matched := handler.writeExpectedWebDAVError(w, errors.New("sensitive backend detail"), http.StatusConflict, errDestinationInsideSourceDirectory)

	if matched {
		t.Fatal("expected unexpected error not to be treated as client error")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected recorder to remain untouched, got status %d", w.Code)
	}
}

func TestHandler_WriteExpectedWebDAVError_UsesSentinelMessageForWrappedError(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})
	w := httptest.NewRecorder()

	err := errors.Join(errors.New("sensitive backend detail"), errDestinationInsideSourceDirectory)
	matched := handler.writeExpectedWebDAVError(w, err, http.StatusConflict, errDestinationInsideSourceDirectory)

	if !matched {
		t.Fatal("expected wrapped sentinel error to be handled")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d", http.StatusConflict, w.Code)
	}
	if strings.Contains(w.Body.String(), "sensitive backend detail") {
		t.Fatalf("expected wrapped internal detail to be hidden, got %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errDestinationInsideSourceDirectory.Error()) {
		t.Fatalf("expected sentinel message in response, got %q", w.Body.String())
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
