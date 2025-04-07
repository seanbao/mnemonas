// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/workspace"
)

func setDeleteVersionObjectHook(t *testing.T, fs *storage.FileSystem, fn func(context.Context, string) error) {
	t.Helper()
	field := reflect.ValueOf(fs).Elem().FieldByName("deleteVersionObject")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}

func setStorageHook[T any](t *testing.T, fs *storage.FileSystem, fieldName string, fn T) {
	t.Helper()
	field := reflect.ValueOf(fs).Elem().FieldByName(fieldName)
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}

func getStorageHook[T any](t *testing.T, fs *storage.FileSystem, fieldName string) T {
	t.Helper()
	field := reflect.ValueOf(fs).Elem().FieldByName(fieldName)
	value := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
	hook, ok := value.(T)
	if !ok {
		t.Fatalf("storage hook %s has unexpected type %T", fieldName, value)
	}
	return hook
}

// testDataplaneAddr is the address of the test dataplane server
func testDataplaneAddr() string {
	if addr := os.Getenv("MNEMONAS_TEST_DATAPLANE_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:9090"
}

// setupDataplaneClient creates a dataplane client for testing
// Returns nil if dataplane is not available
func setupDataplaneClient(t *testing.T) *dataplane.Client {
	client := dataplane.NewClient(testDataplaneAddr())
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
	t.Cleanup(func() { handler.Close() })

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

func TestHandler_MKCOL_ReturnsCreatedWithWarningWhenDirectorySyncFailsAfterVisibleCreate(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	originalMkdir := getStorageHook[func(context.Context, string) error](t, fs, "mkdirWorkspacePath")
	setStorageHook(t, fs, "mkdirWorkspacePath", func(ctx context.Context, name string) error {
		if err := originalMkdir(ctx, name); err != nil {
			return err
		}
		if name == "/warning-dir" {
			return workspace.WrapVisibleMutationWarning(errors.New("sync dir failed"))
		}
		return nil
	})

	req := httptest.NewRequest("MKCOL", "/dav/warning-dir", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("MKCOL warning status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("Warning"); got != webdavWorkspaceMutationWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavWorkspaceMutationWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/warning-dir"); err != nil {
		t.Fatalf("expected MKCOL warning directory to exist, got %v", err)
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

func TestRequestHasBody_PreservesProbeByteForUnknownLengthBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/dav/testdir", io.NopCloser(strings.NewReader("body")))
	req.ContentLength = -1

	if !requestHasBody(req) {
		t.Fatal("expected requestHasBody to detect an unknown-length body")
	}

	remaining, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll(body) error: %v", err)
	}
	if string(remaining) != "body" {
		t.Fatalf("expected probed body to remain intact, got %q", string(remaining))
	}
}

func TestHandler_MKCOL_ExistingDirectoryReturnsMethodNotAllowed(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/testdir"); err != nil {
		t.Fatalf("Mkdir(testdir) error: %v", err)
	}

	req := httptest.NewRequest("MKCOL", "/dav/testdir", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("MKCOL existing directory status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_MKCOL_ExistingFileReturnsMethodNotAllowed(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/existing.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(existing.txt) error: %v", err)
	}

	req := httptest.NewRequest("MKCOL", "/dav/existing.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("MKCOL existing file status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_MKCOL_ConcurrentCreateReturnsMethodNotAllowed(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/race-dir"

	handler.pathLock.Lock(targetPath)

	mkcolDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("MKCOL", "/dav/race-dir", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		mkcolDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[targetPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock(targetPath)
			t.Fatal("timed out waiting for MKCOL to block on path lock")
		}
	}

	if err := fs.Mkdir(ctx, targetPath); err != nil {
		handler.pathLock.Unlock(targetPath)
		t.Fatalf("Mkdir(race-dir) error: %v", err)
	}

	handler.pathLock.Unlock(targetPath)
	w := <-mkcolDone
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("MKCOL concurrent create status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("expected concurrently created collection to remain, got %v", err)
	}
}

func TestHandler_PUT_BlocksOnAncestorPathLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	ancestorPath := "/locked-parent"
	targetPath := "/locked-parent/child.txt"

	if err := fs.Mkdir(ctx, ancestorPath); err != nil {
		t.Fatalf("Mkdir(locked-parent) error: %v", err)
	}

	handler.pathLock.Lock(ancestorPath)

	putDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPut, "/dav/locked-parent/child.txt", strings.NewReader("new content"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		putDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[ancestorPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock(ancestorPath)
			t.Fatal("timed out waiting for PUT to block on ancestor path lock")
		}
	}

	handler.pathLock.Unlock(ancestorPath)
	w := <-putDone
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT after ancestor path unlock status = %d, want %d", w.Code, http.StatusCreated)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("expected PUT target to exist after ancestor lock release, got %v", err)
	}
}

func TestHandler_PUT_BlocksOnRootPathLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/root-locked-child.txt"

	handler.pathLock.Lock("/")

	putDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPut, "/dav/root-locked-child.txt", strings.NewReader("new content"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		putDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks["/"]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock("/")
			t.Fatal("timed out waiting for PUT to block on root path lock")
		}
	}

	handler.pathLock.Unlock("/")
	w := <-putDone
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT after root path unlock status = %d, want %d", w.Code, http.StatusCreated)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("expected PUT target to exist after root lock release, got %v", err)
	}
}

func TestHandler_PUT_ValidatesWriteLockAfterHierarchyLockWait(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/lock-race/file.txt"

	if err := fs.Mkdir(ctx, "/lock-race"); err != nil {
		t.Fatalf("Mkdir(lock-race) error: %v", err)
	}
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("original"))); err != nil {
		t.Fatalf("WriteFile(original) error: %v", err)
	}

	handler.pathLock.RLock(targetPath)

	putDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPut, "/dav/lock-race/file.txt", strings.NewReader("updated"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		putDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[targetPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.RUnlock(targetPath)
			t.Fatal("timed out waiting for PUT to block on hierarchy write lock")
		}
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-race/file.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		handler.pathLock.RUnlock(targetPath)
		t.Fatalf("LOCK during PUT wait status = %d, want %d", lockW.Code, http.StatusOK)
	}

	handler.pathLock.RUnlock(targetPath)
	w := <-putDone
	if w.Code != http.StatusLocked {
		t.Fatalf("PUT after concurrent LOCK status = %d, want %d", w.Code, http.StatusLocked)
	}

	f, err := fs.OpenFile(ctx, targetPath)
	if err != nil {
		t.Fatalf("OpenFile(lock-race/file.txt) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(lock-race/file.txt) error: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("expected file content to remain unchanged after late LOCK, got %q", string(data))
	}
}

func TestHandler_MKCOL_ReturnsConflictWhenParentPathIsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent-file) error: %v", err)
	}

	req := httptest.NewRequest("MKCOL", "/dav/parent-file/child", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MKCOL parent-file conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/parent-file/child"); !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, storage.ErrNotDir) {
		t.Fatalf("expected child collection to remain absent, got %v", err)
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

func TestHandler_HEAD_PreservesFileContentType(t *testing.T) {
	handler, _, _ := setupTestHandler(t)

	req := httptest.NewRequest("MKCOL", "/dav/files", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	req = httptest.NewRequest("PUT", "/dav/files/test.txt", strings.NewReader("Hello, WebDAV!"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	req = httptest.NewRequest("GET", "/dav/files/test.txt", nil)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, req)

	req = httptest.NewRequest("HEAD", "/dav/files/test.txt", nil)
	headW := httptest.NewRecorder()
	handler.ServeHTTP(headW, req)

	if headW.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", headW.Code, http.StatusOK)
	}
	if headW.Header().Get("Content-Type") == "" {
		t.Fatal("HEAD should return Content-Type header for files")
	}
	if headW.Header().Get("Content-Type") != getW.Header().Get("Content-Type") {
		t.Fatalf("HEAD Content-Type = %q, want GET Content-Type %q", headW.Header().Get("Content-Type"), getW.Header().Get("Content-Type"))
	}
	if headW.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", headW.Body.Len())
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

func TestHandler_PUT_ReturnsConflictWhenParentChainContainsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/files"); err != nil {
		t.Fatalf("Mkdir(/files) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/files/blocked", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(blocked) error: %v", err)
	}

	req := httptest.NewRequest("PUT", "/dav/files/blocked/child/file.txt", strings.NewReader("content"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("PUT nested under file status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory message, got %q", w.Body.String())
	}
}

func TestHandler_PUT_ParentStatUnexpectedErrorReturnsInternalServerError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based stat failures are unreliable as root")
	}

	handler, _, tmpDir := setupTestHandler(t)

	blockedPath := filepath.Join(tmpDir, "files", "blocked")
	if err := os.MkdirAll(blockedPath, 0755); err != nil {
		t.Fatalf("MkdirAll(blocked) error: %v", err)
	}
	if err := os.Chmod(blockedPath, 0); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer os.Chmod(blockedPath, 0755)

	req := httptest.NewRequest("PUT", "/dav/blocked/sub/file.txt", strings.NewReader("content"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("PUT parent stat error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(w.Body.String(), "parent directory not found") {
		t.Fatalf("expected unexpected parent stat failure to avoid false not-found message, got %q", w.Body.String())
	}
}

func TestHandler_PUT_TargetStatUnexpectedErrorWithIfMatchReturnsInternalServerError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based stat failures are unreliable as root")
	}

	handler, _, tmpDir := setupTestHandler(t)

	blockedPath := filepath.Join(tmpDir, "files", "loop-target")
	if err := os.MkdirAll(blockedPath, 0755); err != nil {
		t.Fatalf("MkdirAll(loop-target) error: %v", err)
	}
	if err := os.Chmod(blockedPath, 0); err != nil {
		t.Fatalf("Chmod(loop-target) error: %v", err)
	}
	defer os.Chmod(blockedPath, 0755)

	req := httptest.NewRequest("PUT", "/dav/loop-target/file.txt", strings.NewReader("content"))
	req.Header.Set("If-Match", "*")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("PUT target stat error with If-Match status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
		t.Fatalf("expected target stat failure to avoid false precondition response, got %q", w.Body.String())
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
	if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
		t.Fatalf("expected precondition failed message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/files/missing.txt"); err == nil {
		t.Fatal("expected missing file to remain absent after failed conditional PUT")
	}
}

func TestHandler_PUT_IfNoneMatchMatchingETagFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/files"); err != nil {
		t.Fatalf("Mkdir(/files) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/files/existing.txt", strings.NewReader("initial")); err != nil {
		t.Fatalf("WriteFile(existing.txt) error: %v", err)
	}
	info, err := fs.Stat(ctx, "/files/existing.txt")
	if err != nil {
		t.Fatalf("Stat(existing.txt) error: %v", err)
	}
	etag := `"` + info.ContentHash + `"`

	req := httptest.NewRequest("PUT", "/dav/files/existing.txt", strings.NewReader("updated"))
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("PUT If-None-Match matching ETag status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}
	if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
		t.Fatalf("expected precondition failed message, got %q", w.Body.String())
	}

	reader, err := fs.OpenFile(ctx, "/files/existing.txt")
	if err != nil {
		t.Fatalf("OpenFile(existing.txt) error: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(existing.txt) error: %v", err)
	}
	if string(body) != "initial" {
		t.Fatalf("expected existing file content to remain unchanged, got %q", string(body))
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
		if got := w.Header().Get("ETag"); got != etag {
			t.Fatalf("ETag on 304 = %q, want %q", got, etag)
		}
		if got := w.Header().Get("Last-Modified"); got != info.ModTime.UTC().Format(http.TimeFormat) {
			t.Fatalf("Last-Modified on 304 = %q, want %q", got, info.ModTime.UTC().Format(http.TimeFormat))
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
		if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
			t.Fatalf("expected precondition failed message, got %q", w.Body.String())
		}
	})

	t.Run("If-Match_Precedes_If-None-Match", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
		if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
			t.Fatalf("expected precondition failed message, got %q", w.Body.String())
		}
	})

	t.Run("If-Modified-Since_NotModified", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Modified-Since", info.ModTime.Add(time.Minute).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotModified)
		}
		if got := w.Header().Get("ETag"); got != etag {
			t.Fatalf("ETag on If-Modified-Since 304 = %q, want %q", got, etag)
		}
		if got := w.Header().Get("Last-Modified"); got != info.ModTime.UTC().Format(http.TimeFormat) {
			t.Fatalf("Last-Modified on If-Modified-Since 304 = %q, want %q", got, info.ModTime.UTC().Format(http.TimeFormat))
		}
	})

	t.Run("If-Unmodified-Since_PreconditionFailed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Unmodified-Since", info.ModTime.Add(-time.Minute).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
		if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
			t.Fatalf("expected precondition failed message, got %q", w.Body.String())
		}
	})

	t.Run("If-Unmodified-Since_Precedes_If-Modified-Since", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/dav/cond/file.txt", nil)
		req.Header.Set("If-Unmodified-Since", info.ModTime.Add(-time.Minute).UTC().Format(http.TimeFormat))
		req.Header.Set("If-Modified-Since", info.ModTime.Add(time.Minute).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
		if !strings.Contains(w.Body.String(), errPreconditionFailed.Error()) {
			t.Fatalf("expected precondition failed message, got %q", w.Body.String())
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

func TestHandler_DELETE_ReturnsNoContentWithWarningWhenPermanentDeleteSyncFailsAfterVisibleDelete(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.UpdateTrashSettings(false, 30, 1<<20)
	if err := fs.Mkdir(ctx, "/deltest"); err != nil {
		t.Fatalf("Mkdir(deltest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile(file.txt) error: %v", err)
	}

	originalDelete := getStorageHook[func(context.Context, string) error](t, fs, "deleteWorkspacePath")
	setStorageHook(t, fs, "deleteWorkspacePath", func(ctx context.Context, name string) error {
		if err := originalDelete(ctx, name); err != nil {
			return err
		}
		if name == "/deltest/file.txt" {
			return workspace.WrapVisibleMutationWarning(errors.New("sync dir failed"))
		}
		return nil
	})

	req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE warning status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Warning"); got != webdavWorkspaceMutationWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavWorkspaceMutationWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/deltest/file.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected file to remain deleted after warning, got %v", err)
	}
}

func TestHandler_DELETE_ReturnsNoContentWithWarningWhenTrashCapacityCleanupFailsAfterVisibleDelete(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.UpdateTrashSettings(true, 30, 10)
	if err := fs.WriteFile(ctx, "/old-trash.txt", bytes.NewReader([]byte("123456"))); err != nil {
		t.Fatalf("WriteFile(old-trash.txt) error: %v", err)
	}
	if err := fs.Delete(ctx, "/old-trash.txt"); err != nil {
		t.Fatalf("Delete(old-trash.txt) error: %v", err)
	}
	setStorageHook(t, fs, "removeTrashMetadata", func(ctx context.Context, id string) error {
		return errors.New("metadata delete failed")
	})
	if err := fs.Mkdir(ctx, "/deltest"); err != nil {
		t.Fatalf("Mkdir(deltest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(file.txt) error: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE cleanup warning status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Warning"); got != webdavTrashDeleteCleanupWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavTrashDeleteCleanupWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/deltest/file.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected file to remain deleted after cleanup warning, got %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected both trash items to remain after cleanup warning, got %d", len(items))
	}
}

func TestHandler_DELETE_ReturnsNoContentWithWarningWhenPermanentDeleteCleanupFailsAfterVisibleDelete(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	fs.UpdateTrashSettings(false, 30, 0)
	if err := fs.Mkdir(ctx, "/deltest"); err != nil {
		t.Fatalf("Mkdir(deltest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	setStorageHook(t, fs, "deleteVersionObject", func(ctx context.Context, hash string) error {
		return errors.New("version object cleanup failed")
	})

	req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE cleanup warning status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Warning"); got != webdavDeleteCleanupWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavDeleteCleanupWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/deltest/file.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected file to remain deleted after cleanup warning, got %v", err)
	}
}

func TestHandler_DELETE_ConditionalHeaders(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/deltest"); err != nil {
		t.Fatalf("Mkdir(deltest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest/file.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile(file.txt) error: %v", err)
	}
	info, err := fs.Stat(ctx, "/deltest/file.txt")
	if err != nil {
		t.Fatalf("Stat(file.txt) error: %v", err)
	}
	etag := `"` + info.ContentHash + `"`

	t.Run("IfMatchMismatchPreventsDelete", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("DELETE If-Match mismatch status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
		if _, err := fs.Stat(ctx, "/deltest/file.txt"); err != nil {
			t.Fatalf("expected file to remain after failed conditional DELETE, got %v", err)
		}
	})

	t.Run("IfNoneMatchHitPreventsDelete", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/dav/deltest/file.txt", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("DELETE If-None-Match hit status = %d, want %d", w.Code, http.StatusPreconditionFailed)
		}
		if _, err := fs.Stat(ctx, "/deltest/file.txt"); err != nil {
			t.Fatalf("expected file to remain after failed If-None-Match DELETE, got %v", err)
		}
	})
}

func TestHandler_DELETE_DirectoryInvalidDepthReturnsBadRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/deltest-depth"); err != nil {
		t.Fatalf("Mkdir(deltest-depth) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/deltest-depth/file.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile(file.txt) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/dav/deltest-depth", nil)
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE directory invalid Depth status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), errInvalidDepthHeader.Error()) {
		t.Fatalf("expected invalid Depth error message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/deltest-depth"); err != nil {
		t.Fatalf("expected directory to remain after invalid Depth DELETE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/deltest-depth/file.txt"); err != nil {
		t.Fatalf("expected child file to remain after invalid Depth DELETE, got %v", err)
	}
}

func TestHandler_DELETE_IfMatchValidatesCurrentETagUnderLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/deltest-race/file.txt"

	if err := fs.Mkdir(ctx, "/deltest-race"); err != nil {
		t.Fatalf("Mkdir(deltest-race) error: %v", err)
	}
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	info, err := fs.Stat(ctx, targetPath)
	if err != nil {
		t.Fatalf("Stat(v1) error: %v", err)
	}
	staleETag := `"` + info.ContentHash + `"`

	handler.pathLock.Lock(targetPath)

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/dav/deltest-race/file.txt", nil)
		req.Header.Set("If-Match", staleETag)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		deleteDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[targetPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock(targetPath)
			t.Fatal("timed out waiting for DELETE to block on path lock")
		}
	}

	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("v2"))); err != nil {
		handler.pathLock.Unlock(targetPath)
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	handler.pathLock.Unlock(targetPath)
	w := <-deleteDone
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("DELETE stale If-Match after concurrent write status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}
	if _, err := fs.Stat(ctx, targetPath); err != nil {
		t.Fatalf("expected file to remain after stale If-Match DELETE, got %v", err)
	}
	currentInfo, err := fs.Stat(ctx, targetPath)
	if err != nil {
		t.Fatalf("Stat(current) error: %v", err)
	}
	if currentInfo.ContentHash == info.ContentHash {
		t.Fatal("expected concurrent write to change the file hash")
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
	req.Header.Set("Destination", "http://example.com/dav/dst/copied.txt")
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
	req.Header.Set("Destination", "http://example.com/dav/dst/copied.txt")
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

func TestHandler_COPY_OverwriteFalseValidatesDestinationUnderLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/dst/race.txt"

	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src/file.txt) error: %v", err)
	}

	handler.pathLock.Lock(targetPath)

	copyDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/dst/race.txt")
		req.Header.Set("Overwrite", "F")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		copyDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[targetPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock(targetPath)
			t.Fatal("timed out waiting for COPY to block on destination lock")
		}
	}

	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("existing"))); err != nil {
		handler.pathLock.Unlock(targetPath)
		t.Fatalf("WriteFile(dst/race.txt) error: %v", err)
	}

	handler.pathLock.Unlock(targetPath)
	w := <-copyDone
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("COPY stale Overwrite:F after concurrent destination create status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}

	f, err := fs.OpenFile(ctx, targetPath)
	if err != nil {
		t.Fatalf("OpenFile(dst/race.txt) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(dst/race.txt) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("Expected destination content unchanged after concurrent create, got %q", string(data))
	}
	if _, err := fs.Stat(ctx, "/src/file.txt"); err != nil {
		t.Fatalf("expected source file to remain after COPY rejection, got %v", err)
	}
}

func TestHandler_COPY_OverwriteFalseDoesNotOverwriteFileCreatedAfterPrecheck(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/dst/race-after-precheck.txt"

	if err := fs.Mkdir(ctx, "/src"); err != nil {
		t.Fatalf("Mkdir(src) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/src/file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src/file.txt) error: %v", err)
	}

	hookCalled := false
	handler.beforeCopyFile = func(srcPath, dstPath string) error {
		if hookCalled || dstPath != targetPath {
			return nil
		}
		hookCalled = true
		return fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("existing")))
	}

	req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/dst/race-after-precheck.txt")
	req.Header.Set("Overwrite", "F")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !hookCalled {
		t.Fatal("expected beforeCopyFile hook to run")
	}
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("COPY overwrite=false post-precheck destination create status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}

	f, err := fs.OpenFile(ctx, targetPath)
	if err != nil {
		t.Fatalf("OpenFile(dst/race-after-precheck.txt) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(dst/race-after-precheck.txt) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("expected destination content unchanged after post-precheck create, got %q", string(data))
	}
	if _, err := fs.Stat(ctx, "/src/file.txt"); err != nil {
		t.Fatalf("expected source file to remain after COPY rejection, got %v", err)
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
	req.Header.Set("Destination", "http://example.com/dav/dst/copied.txt")
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
	req.Header.Set("Destination", "http://example.com/dav/dst/copied-dir")
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

func TestHandler_COPY_DirectoryRecursiveReturnsCreatedWithWarningWhenDestinationCreateSyncFails(t *testing.T) {
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

	originalMkdir := getStorageHook[func(context.Context, string) error](t, fs, "mkdirWorkspacePath")
	setStorageHook(t, fs, "mkdirWorkspacePath", func(ctx context.Context, name string) error {
		if err := originalMkdir(ctx, name); err != nil {
			return err
		}
		if name == "/dst/copied-dir" {
			return workspace.WrapVisibleMutationWarning(errors.New("sync dir failed"))
		}
		return nil
	})

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://example.com/dav/dst/copied-dir")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("COPY directory warning status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("Warning"); got != webdavWorkspaceMutationWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavWorkspaceMutationWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/dst/copied-dir"); err != nil {
		t.Fatalf("expected copied directory to exist, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/dst/copied-dir/root.txt"); err != nil {
		t.Fatalf("expected copied root file to exist, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/dst/copied-dir/nested/child.txt"); err != nil {
		t.Fatalf("expected copied child file to exist, got %v", err)
	}
}

func TestHandler_COPY_DirectoryDepthZeroCopiesOnlyCollection(t *testing.T) {
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
	req.Header.Set("Destination", "http://example.com/dav/dst/shallow-copy")
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("COPY directory Depth:0 status = %d, want %d", w.Code, http.StatusCreated)
	}
	if _, err := fs.Stat(ctx, "/dst/shallow-copy"); err != nil {
		t.Fatalf("expected shallow copied directory to exist, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/dst/shallow-copy/root.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected child file to remain absent for Depth:0 copy, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/dst/shallow-copy/nested"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected nested directory to remain absent for Depth:0 copy, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/srcdir/root.txt"); err != nil {
		t.Fatalf("expected source directory to remain unchanged, got %v", err)
	}
}

func TestHandler_COPY_DirectoryInvalidDepthReturnsBadRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/srcdir"); err != nil {
		t.Fatalf("Mkdir(srcdir) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://example.com/dav/dst/invalid-depth-copy")
	req.Header.Set("Depth", "1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("COPY directory invalid Depth status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), errInvalidDepthHeader.Error()) {
		t.Fatalf("expected invalid Depth error message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/dst/invalid-depth-copy"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent after invalid Depth copy, got %v", err)
	}
}

func TestHandler_COPY_DirectoryRollbackOnChildCopyFailure(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst/copied-dir"); err != nil {
		t.Fatalf("Mkdir(copied-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/dst/copied-dir/a.txt", bytes.NewReader([]byte("partial"))); err != nil {
		t.Fatalf("WriteFile(partial) error: %v", err)
	}

	copyErr := errors.New("copy child failed")
	if err := handler.rollbackCopiedDirectory("/dst/copied-dir", copyErr); !errors.Is(err, copyErr) {
		t.Fatalf("rollbackCopiedDirectory() error = %v, want %v", err, copyErr)
	}
	if _, err := fs.Stat(ctx, "/dst/copied-dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected partial destination tree to be removed, got %v", err)
	}
}

func TestHandler_COPY_DirectoryRequestRollsBackPartialTreeOnWriteFailure(t *testing.T) {
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

	updateFileIndexField := reflect.ValueOf(fs).Elem().FieldByName("updateFileIndex")
	originalUpdateFileIndex := reflect.NewAt(updateFileIndexField.Type(), unsafe.Pointer(updateFileIndexField.UnsafeAddr())).Elem().Interface().(func(context.Context, string, int64, time.Time, string) error)
	setStorageHook(t, fs, "updateFileIndex", func(ctx context.Context, name string, size int64, modTime time.Time, hash string) error {
		if name == "/dst/copied-dir/nested/child.txt" {
			return errors.New("index update failed")
		}
		return originalUpdateFileIndex(ctx, name, size, modTime, hash)
	})

	req := httptest.NewRequest("COPY", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://example.com/dav/dst/copied-dir")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("COPY directory failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/dst/copied-dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected failed COPY request to rollback destination tree, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/srcdir"); err != nil {
		t.Fatalf("expected source directory preserved after failed COPY, got %v", err)
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
	req.Header.Set("Destination", "http://example.com/dav/srcdir/nested/copied-dir")
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
	req.Header.Set("Destination", "http://example.com/dav/dst")
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

func TestHandler_COPY_ReturnsConflictWhenSourceParentIsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(copy-parent-file) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/copy-parent-file/child.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/copied.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("COPY source parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/copied.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestHandler_COPY_ReturnsConflictWhenDestinationParentIsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/src-file.txt", bytes.NewReader([]byte("copy me"))); err != nil {
		t.Fatalf("WriteFile(src-file.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(copy-parent) error: %v", err)
	}

	req := httptest.NewRequest("COPY", "/dav/src-file.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/copy-parent/child.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("COPY destination parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/copy-parent/child.txt"); !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, storage.ErrNotDir) {
		t.Fatalf("expected destination to remain absent, got %v", err)
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
	req.Header.Set("Destination", "http://example.com/other/copied.txt")
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
	req.Header.Set("Destination", "http://example.com/dav/src/file.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("COPY same source/destination status = %d, want %d", w.Code, http.StatusForbidden)
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
	req.Header.Set("Destination", "http://example.com/dav/movetest/moved.txt")
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

func TestHandler_MOVE_DirectoryInvalidDepthReturnsBadRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/srcdir"); err != nil {
		t.Fatalf("Mkdir(srcdir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/srcdir/root.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/dst"); err != nil {
		t.Fatalf("Mkdir(dst) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/srcdir", nil)
	req.Header.Set("Destination", "http://example.com/dav/dst/moved-dir")
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("MOVE directory invalid Depth status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), errInvalidDepthHeader.Error()) {
		t.Fatalf("expected invalid Depth error message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/srcdir"); err != nil {
		t.Fatalf("expected source directory to remain after rejected MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/dst/moved-dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent after invalid Depth MOVE, got %v", err)
	}
}

func TestHandler_MOVE_UpdatesSharePathsWhenFilesystemHooksConfigured(t *testing.T) {
	handler, fs, tmpDir := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}

	shareStore, err := share.NewShareStore(filepath.Join(tmpDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:      "/movetest/orig.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	fs.SetPathChangeHooks(func(ctx context.Context, oldPath, newPath string) error {
		return shareStore.UpdatePathReferences(oldPath, newPath)
	}, nil)

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/movetest/moved.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("MOVE status = %d, want %d", w.Code, http.StatusCreated)
	}
	renamedShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if renamedShare.Path != "/movetest/moved.txt" {
		t.Fatalf("expected share path to be updated, got %q", renamedShare.Path)
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
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing.txt")
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

func TestHandler_MOVE_OverwriteFalseValidatesDestinationUnderLock(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()
	targetPath := "/movetest/z-race.txt"

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}

	handler.pathLock.Lock(targetPath)

	moveDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/movetest/z-race.txt")
		req.Header.Set("Overwrite", "F")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		moveDone <- w
	}()

	deadline := time.Now().Add(time.Second)
	for {
		handler.pathLock.mu.Lock()
		entry := handler.pathLock.locks[targetPath]
		var refCount int32
		if entry != nil {
			refCount = entry.refCount
		}
		handler.pathLock.mu.Unlock()
		if refCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			handler.pathLock.Unlock(targetPath)
			t.Fatal("timed out waiting for MOVE to block on destination lock")
		}
	}

	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("existing"))); err != nil {
		handler.pathLock.Unlock(targetPath)
		t.Fatalf("WriteFile(z-race.txt) error: %v", err)
	}

	handler.pathLock.Unlock(targetPath)
	w := <-moveDone
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("MOVE stale Overwrite:F after concurrent destination create status = %d, want %d", w.Code, http.StatusPreconditionFailed)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("expected source file to remain after MOVE rejection, got %v", err)
	}
	f, err := fs.OpenFile(ctx, targetPath)
	if err != nil {
		t.Fatalf("OpenFile(z-race.txt) error: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(z-race.txt) error: %v", err)
	}
	if string(data) != "existing" {
		t.Fatalf("Expected destination content unchanged after concurrent create, got %q", string(data))
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
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing.txt")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("MOVE overwrite=true status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandler_MOVE_FileOverwriteExistingDirectoryReturnsConflict(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir(movetest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/movetest/existing-dir"); err != nil {
		t.Fatalf("Mkdir(existing-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing-dir/child.txt", bytes.NewReader([]byte("keep dir"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing-dir")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE file overwrite existing directory status = %d, want %d", w.Code, http.StatusConflict)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected type-conflict MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/movetest/existing-dir"); err != nil {
		t.Fatalf("expected destination directory to remain after rejected type-conflict MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/movetest/existing-dir/child.txt"); err != nil {
		t.Fatalf("expected destination directory contents to remain after rejected type-conflict MOVE, got %v", err)
	}
}

func TestHandler_MOVE_DirectoryOverwriteExistingFileReturnsConflict(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir(movetest) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/movetest/orig-dir"); err != nil {
		t.Fatalf("Mkdir(orig-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig-dir/child.txt", bytes.NewReader([]byte("keep dir"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing.txt", bytes.NewReader([]byte("keep file"))); err != nil {
		t.Fatalf("WriteFile(existing.txt) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig-dir", nil)
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing.txt")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE directory overwrite existing file status = %d, want %d", w.Code, http.StatusConflict)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig-dir"); err != nil {
		t.Fatalf("expected source directory to remain after rejected type-conflict MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig-dir/child.txt"); err != nil {
		t.Fatalf("expected source directory contents to remain after rejected type-conflict MOVE, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/movetest/existing.txt"); err != nil {
		t.Fatalf("expected destination file to remain after rejected type-conflict MOVE, got %v", err)
	}
}

func TestHandler_MOVE_OverwriteFailureRestoresExistingDestination(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(existing) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/missing.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing.txt")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("MOVE missing source status = %d, want %d", w.Code, http.StatusNotFound)
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
		t.Fatalf("Expected destination content preserved after failed overwrite MOVE, got %q", string(data))
	}
	entries, err := fs.ReadDir(ctx, "/movetest")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name, ".webdav-move-backup-") {
			t.Fatalf("unexpected leftover MOVE backup path: %s", entry.Name)
		}
	}
}

func TestHandler_MOVE_OverwriteCleanupFailureReturnsNoContentWithWarning(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(existing v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/existing.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(existing v2) error: %v", err)
	}

	setDeleteVersionObjectHook(t, fs, func(_ context.Context, hash string) error {
		return errors.New("delete version object failed")
	})

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/movetest/existing.txt")
	req.Header.Set("Overwrite", "T")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("MOVE overwrite cleanup failure status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Warning"); got != webdavDeleteCleanupWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, webdavDeleteCleanupWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected source path removed after committed MOVE, got %v", err)
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
	if string(data) != "move me" {
		t.Fatalf("expected destination content updated after committed MOVE, got %q", string(data))
	}
	entries, err := fs.ReadDir(ctx, "/movetest")
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name, ".webdav-move-backup-") {
			t.Fatalf("unexpected leftover MOVE backup path: %s", entry.Name)
		}
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
	req.Header.Set("Destination", "http://example.com/other/moved.txt")
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
	req.Header.Set("Destination", "http://example.com/dav/movetest/orig.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("MOVE same source/destination status = %d, want %d", w.Code, http.StatusForbidden)
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
	req.Header.Set("Destination", "http://example.com/dav/movetest/nested/moved")
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

func TestHandler_MOVE_ReturnsConflictWhenSourceParentIsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(move-parent-file) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/move-parent-file/child.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/moved.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE source parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/moved.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestHandler_MOVE_ReturnsConflictWhenDestinationParentIsFile(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/movetest"); err != nil {
		t.Fatalf("Mkdir(movetest) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/movetest/orig.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(orig) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(move-parent) error: %v", err)
	}

	req := httptest.NewRequest("MOVE", "/dav/movetest/orig.txt", nil)
	req.Header.Set("Destination", "http://example.com/dav/move-parent/child.txt")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("MOVE destination parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %q", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/movetest/orig.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected move, got %v", err)
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

	t.Run("Depth0WithWhitespace", func(t *testing.T) {
		req := httptest.NewRequest("PROPFIND", "/dav/proptest", nil)
		req.Header.Set("Depth", " 0 ")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusMultiStatus {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusMultiStatus)
		}

		body := w.Body.String()
		if !strings.Contains(body, "proptest") {
			t.Fatal("Response should contain directory name")
		}
		if strings.Contains(body, "a.txt") || strings.Contains(body, "b.txt") || strings.Contains(body, "c.txt") {
			t.Fatal("Depth 0 should not contain child resources when header has surrounding whitespace")
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

func TestHandler_PROPFIND_InvalidatesAncestorCacheAfterNestedWrite(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/cachetest"); err != nil {
		t.Fatalf("Mkdir(cachetest) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/cachetest/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("/cachetest/file-%02d.txt", i)
		if err := fs.WriteFile(ctx, name, bytes.NewReader([]byte("seed"))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", name, err)
		}
	}
	if err := fs.WriteFile(ctx, "/cachetest/nested/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(existing nested) error: %v", err)
	}

	firstReq := httptest.NewRequest("PROPFIND", "/dav/cachetest", nil)
	firstReq.Header.Set("Depth", "infinity")
	firstW := httptest.NewRecorder()
	handler.ServeHTTP(firstW, firstReq)

	if firstW.Code != http.StatusMultiStatus {
		t.Fatalf("first PROPFIND status = %d, want %d", firstW.Code, http.StatusMultiStatus)
	}
	if !strings.Contains(firstW.Body.String(), "existing.txt") {
		t.Fatalf("expected initial PROPFIND to include existing nested file, got %q", firstW.Body.String())
	}
	if strings.Contains(firstW.Body.String(), "new.txt") {
		t.Fatalf("initial PROPFIND unexpectedly contains new file, got %q", firstW.Body.String())
	}

	putReq := httptest.NewRequest("PUT", "/dav/cachetest/nested/new.txt", strings.NewReader("new content"))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)

	if putW.Code != http.StatusCreated {
		t.Fatalf("nested PUT status = %d, want %d", putW.Code, http.StatusCreated)
	}

	secondReq := httptest.NewRequest("PROPFIND", "/dav/cachetest", nil)
	secondReq.Header.Set("Depth", "infinity")
	secondW := httptest.NewRecorder()
	handler.ServeHTTP(secondW, secondReq)

	if secondW.Code != http.StatusMultiStatus {
		t.Fatalf("second PROPFIND status = %d, want %d", secondW.Code, http.StatusMultiStatus)
	}
	if !strings.Contains(secondW.Body.String(), "new.txt") {
		t.Fatalf("expected nested write to invalidate ancestor PROPFIND cache, got %q", secondW.Body.String())
	}
}

func TestHandler_PROPFIND_DoesNotServeStaleCacheWhilePutIsInFlight(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/cache-race"); err != nil {
		t.Fatalf("Mkdir(/cache-race) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/cache-race/existing.txt", bytes.NewReader([]byte("existing"))); err != nil {
		t.Fatalf("WriteFile(/cache-race/existing.txt) error: %v", err)
	}

	initialReq := httptest.NewRequest("PROPFIND", "/dav/cache-race", nil)
	initialReq.Header.Set("Depth", "1")
	initialW := httptest.NewRecorder()
	handler.ServeHTTP(initialW, initialReq)

	if initialW.Code != http.StatusMultiStatus {
		t.Fatalf("initial PROPFIND status = %d, want %d", initialW.Code, http.StatusMultiStatus)
	}
	if !strings.Contains(initialW.Body.String(), "existing.txt") {
		t.Fatalf("expected initial PROPFIND to include existing file, got %q", initialW.Body.String())
	}
	if strings.Contains(initialW.Body.String(), "new.txt") {
		t.Fatalf("initial PROPFIND unexpectedly contains new file, got %q", initialW.Body.String())
	}

	originalUpdateFileIndex := getStorageHook[func(ctx context.Context, path string, size int64, modTime time.Time, hash string) error](t, fs, "updateFileIndex")
	updateStarted := make(chan struct{})
	continueUpdate := make(chan struct{})
	setStorageHook(t, fs, "updateFileIndex", func(ctx context.Context, name string, size int64, modTime time.Time, hash string) error {
		if name == "/cache-race/new.txt" {
			select {
			case <-updateStarted:
			default:
				close(updateStarted)
			}
			<-continueUpdate
		}
		return originalUpdateFileIndex(ctx, name, size, modTime, hash)
	})
	t.Cleanup(func() {
		setStorageHook(t, fs, "updateFileIndex", originalUpdateFileIndex)
	})

	putReq := httptest.NewRequest("PUT", "/dav/cache-race/new.txt", strings.NewReader("new content"))
	putW := httptest.NewRecorder()
	putDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(putW, putReq)
		close(putDone)
	}()

	select {
	case <-updateStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PUT to reach updateFileIndex hook")
	}

	concurrentReq := httptest.NewRequest("PROPFIND", "/dav/cache-race", nil)
	concurrentReq.Header.Set("Depth", "1")
	concurrentW := httptest.NewRecorder()
	handler.ServeHTTP(concurrentW, concurrentReq)

	if concurrentW.Code != http.StatusMultiStatus {
		t.Fatalf("concurrent PROPFIND status = %d, want %d", concurrentW.Code, http.StatusMultiStatus)
	}
	if !strings.Contains(concurrentW.Body.String(), "new.txt") {
		t.Fatalf("expected concurrent PROPFIND to avoid stale cache after PUT content is written, got %q", concurrentW.Body.String())
	}

	close(continueUpdate)
	select {
	case <-putDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PUT to complete")
	}

	if putW.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want %d", putW.Code, http.StatusCreated)
	}
}

func TestHandler_PROPFIND_InvalidatesDeletedDirectoryCache(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/cached-dir"); err != nil {
		t.Fatalf("Mkdir(cached-dir) error: %v", err)
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("/cached-dir/file-%02d.txt", i)
		if err := fs.WriteFile(ctx, name, bytes.NewReader([]byte("seed"))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", name, err)
		}
	}
	if err := fs.WriteFile(ctx, "/cached-dir/file-10.txt", bytes.NewReader([]byte("seed"))); err != nil {
		t.Fatalf("WriteFile(file-10.txt) error: %v", err)
	}

	firstReq := httptest.NewRequest("PROPFIND", "/dav/cached-dir", nil)
	firstReq.Header.Set("Depth", "infinity")
	firstW := httptest.NewRecorder()
	handler.ServeHTTP(firstW, firstReq)

	if firstW.Code != http.StatusMultiStatus {
		t.Fatalf("first PROPFIND status = %d, want %d", firstW.Code, http.StatusMultiStatus)
	}

	deleteReq := httptest.NewRequest("DELETE", "/dav/cached-dir", nil)
	deleteW := httptest.NewRecorder()
	handler.ServeHTTP(deleteW, deleteReq)

	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("DELETE cached directory status = %d, want %d", deleteW.Code, http.StatusNoContent)
	}

	secondReq := httptest.NewRequest("PROPFIND", "/dav/cached-dir", nil)
	secondReq.Header.Set("Depth", "infinity")
	secondW := httptest.NewRecorder()
	handler.ServeHTTP(secondW, secondReq)

	if secondW.Code != http.StatusNotFound {
		t.Fatalf("second PROPFIND status = %d, want %d", secondW.Code, http.StatusNotFound)
	}
}

func TestHandler_DELETE_NonEmptyDirectoryMovesWholeTreeToTrash(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(report) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/readme.md", bytes.NewReader([]byte("readme"))); err != nil {
		t.Fatalf("WriteFile(readme) error: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/dav/docs", nil)
	req.Header.Set("Depth", "infinity")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE non-empty directory status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if _, err := fs.Stat(ctx, "/docs"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected deleted directory to be absent, got %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || !items[0].IsDir {
		t.Fatalf("expected one directory trash item, got %+v", items)
	}
}

func TestHandler_PROPFIND_InfinityDepthLimitExceeded(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	currentPath := "/deep"
	if err := fs.Mkdir(ctx, currentPath); err != nil {
		t.Fatalf("Mkdir(/deep) error: %v", err)
	}
	for i := 0; i <= maxPropfindTraversalDepth; i++ {
		currentPath = path.Join(currentPath, fmt.Sprintf("level-%02d", i))
		if err := fs.Mkdir(ctx, currentPath); err != nil {
			t.Fatalf("Mkdir(%s) error: %v", currentPath, err)
		}
	}

	req := httptest.NewRequest("PROPFIND", "/dav/deep", nil)
	req.Header.Set("Depth", "infinity")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("PROPFIND infinity over recursion limit status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if !strings.Contains(w.Body.String(), errPropfindDepthLimitExceeded.Error()) {
		t.Fatalf("expected recursion limit sentinel message, got %q", w.Body.String())
	}
}

func TestHandler_PROPFIND_EscapesHrefSpecialCharacters(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/propfind-special"); err != nil {
		t.Fatalf("Mkdir(/propfind-special) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/propfind-special/hash #file?.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(/propfind-special/hash #file?.txt) error: %v", err)
	}

	req := httptest.NewRequest("PROPFIND", "/dav/propfind-special", nil)
	req.Header.Set("Depth", "1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMultiStatus)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<href>/dav/propfind-special/hash%20%23file%3F.txt</href>`) {
		t.Fatalf("expected PROPFIND href to be percent-encoded, got %q", body)
	}
}

func TestHandler_PROPPATCH_MissingResourceReturnsNotFound(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	req := httptest.NewRequest("PROPPATCH", "/dav/missing-proppatch.txt", strings.NewReader(`<?xml version="1.0"?><propertyupdate xmlns="DAV:"/>`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandler_PROPPATCH_SetsXMLContentTypeAndEscapesHref(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/prop-patch"); err != nil {
		t.Fatalf("Mkdir(/prop-patch) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/prop-patch/hash #file?.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(/prop-patch/hash #file?.txt) error: %v", err)
	}

	req := httptest.NewRequest("PROPPATCH", "/dav/prop-patch/hash%20%23file%3F.txt", strings.NewReader(`<?xml version="1.0"?><propertyupdate xmlns="DAV:"/>`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMultiStatus)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", contentType, "application/xml; charset=utf-8")
	}
	body := w.Body.String()
	if !strings.Contains(body, `<D:href>/dav/prop-patch/hash%20%23file%3F.txt</D:href>`) {
		t.Fatalf("expected PROPPATCH href to be percent-encoded, got %q", body)
	}
}

func TestHandler_PROPPATCH_DirectoryHrefPreservesTrailingSlash(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/prop-patch-dir"); err != nil {
		t.Fatalf("Mkdir(/prop-patch-dir) error: %v", err)
	}

	req := httptest.NewRequest("PROPPATCH", "/dav/prop-patch-dir", strings.NewReader(`<?xml version="1.0"?><propertyupdate xmlns="DAV:"/>`))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMultiStatus)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<D:href>/dav/prop-patch-dir/</D:href>`) {
		t.Fatalf("expected PROPPATCH directory href to preserve trailing slash, got %q", body)
	}
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

func TestHandler_LockCleanupLoopRemovesExpiredLocksWithoutNewRequests(t *testing.T) {
	handler := NewHandler(Config{AuthType: "none"})
	handler.lockCleanupInterval = 10 * time.Millisecond
	t.Cleanup(func() { handler.Close() })

	handler.locksMu.Lock()
	handler.locks["/expired-background-lock.txt"] = webdavLock{
		token:     "opaquelocktoken:expired-background",
		expiresAt: time.Now().Add(-time.Minute),
	}
	handler.locksMu.Unlock()

	handler.startLockCleanupLoop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		handler.locksMu.Lock()
		_, exists := handler.locks["/expired-background-lock.txt"]
		handler.locksMu.Unlock()
		if !exists {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected background lock cleanup loop to remove expired lock")
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

func TestHandler_LOCK_RetriesDuplicateGeneratedToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-duplicate-a.txt", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("WriteFile(lock-duplicate-a.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/lock-duplicate-b.txt", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteFile(lock-duplicate-b.txt) error: %v", err)
	}

	handler.newLockToken = func() (string, error) {
		return "opaquelocktoken:duplicate", nil
	}

	firstReq := httptest.NewRequest("LOCK", "/dav/lock-duplicate-a.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	firstW := httptest.NewRecorder()
	handler.ServeHTTP(firstW, firstReq)
	if firstW.Code != http.StatusOK {
		t.Fatalf("first LOCK status = %d, want %d", firstW.Code, http.StatusOK)
	}

	secondReq := httptest.NewRequest("LOCK", "/dav/lock-duplicate-b.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	secondW := httptest.NewRecorder()
	handler.ServeHTTP(secondW, secondReq)
	if secondW.Code != http.StatusInternalServerError {
		t.Fatalf("second LOCK with exhausted duplicate token attempts status = %d, want %d", secondW.Code, http.StatusInternalServerError)
	}

	sequence := []string{"opaquelocktoken:duplicate", "opaquelocktoken:unique"}
	callIndex := 0
	handler.newLockToken = func() (string, error) {
		token := sequence[callIndex]
		if callIndex < len(sequence)-1 {
			callIndex++
		}
		return token, nil
	}

	thirdReq := httptest.NewRequest("LOCK", "/dav/lock-duplicate-b.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	thirdW := httptest.NewRecorder()
	handler.ServeHTTP(thirdW, thirdReq)
	if thirdW.Code != http.StatusOK {
		t.Fatalf("LOCK with duplicate retry status = %d, want %d", thirdW.Code, http.StatusOK)
	}
	if thirdW.Header().Get("Lock-Token") != "<opaquelocktoken:unique>" {
		t.Fatalf("expected retried lock token to use unique token, got %q", thirdW.Header().Get("Lock-Token"))
	}
}

func TestHandler_LOCK_ReturnsServerErrorWhenTokenGenerationFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-rand-fail.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(lock-rand-fail.txt) error: %v", err)
	}

	handler.newLockToken = func() (string, error) {
		return "", errors.New("entropy failure")
	}

	req := httptest.NewRequest("LOCK", "/dav/lock-rand-fail.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("LOCK with token generation failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if lockToken := w.Header().Get("Lock-Token"); lockToken != "" {
		t.Fatalf("expected failed LOCK not to emit a lock token, got %q", lockToken)
	}

	handler.locksMu.Lock()
	_, exists := handler.locks["/lock-rand-fail.txt"]
	handler.locksMu.Unlock()
	if exists {
		t.Fatal("expected failed LOCK not to persist lock state")
	}
}

func TestHandler_LOCK_InvalidDepthReturnsBadRequest(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/lock-depth"); err != nil {
		t.Fatalf("Mkdir(lock-depth) error: %v", err)
	}

	req := httptest.NewRequest("LOCK", "/dav/lock-depth", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	req.Header.Set("Depth", "1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("LOCK invalid depth status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), errInvalidDepthHeader.Error()) {
		t.Fatalf("expected invalid depth error body, got %q", w.Body.String())
	}

	handler.locksMu.Lock()
	_, exists := handler.locks["/lock-depth"]
	handler.locksMu.Unlock()
	if exists {
		t.Fatal("expected invalid LOCK depth request not to persist lock state")
	}
}

func TestHandler_LOCK_RefreshWithMatchingIfToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-refresh.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(lock-refresh.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-refresh.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("initial LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	handler.locksMu.Lock()
	before := handler.locks["/lock-refresh.txt"].expiresAt
	handler.locksMu.Unlock()

	refreshReq := httptest.NewRequest("LOCK", "/dav/lock-refresh.txt", nil)
	refreshReq.Header.Set("If", "</dav/lock-refresh.txt> ("+lockToken+")")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusOK {
		t.Fatalf("refresh LOCK status = %d, want %d", refreshW.Code, http.StatusOK)
	}
	if refreshW.Header().Get("Lock-Token") != "" {
		t.Fatalf("expected refresh response not to emit Lock-Token header, got %q", refreshW.Header().Get("Lock-Token"))
	}
	if !strings.Contains(refreshW.Body.String(), "<D:depth>infinity</D:depth>") {
		t.Fatalf("expected refresh response body to report infinity depth, got %q", refreshW.Body.String())
	}

	handler.locksMu.Lock()
	after := handler.locks["/lock-refresh.txt"].expiresAt
	handler.locksMu.Unlock()
	if !after.After(before) {
		t.Fatalf("expected refreshed expiry to move forward: before=%v after=%v", before, after)
	}
}

func TestHandler_LOCK_RefreshWithNegatedIfTokenFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-refresh-negated.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(lock-refresh-negated.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-negated.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("initial LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	refreshReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-negated.txt", nil)
	refreshReq.Header.Set("If", "(Not <"+strings.Trim(lockToken, "<>")+">)")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusPreconditionFailed {
		t.Fatalf("refresh LOCK with negated If token status = %d, want %d", refreshW.Code, http.StatusPreconditionFailed)
	}
	if !strings.Contains(refreshW.Body.String(), errLockTokenMatchesRequestURI.Error()) {
		t.Fatalf("expected lock-token-matches-request-uri failure, got %q", refreshW.Body.String())
	}
}

func TestHandler_LOCK_RefreshWithoutTokenFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/lock-refresh-missing-token.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(lock-refresh-missing-token.txt) error: %v", err)
	}

	req := httptest.NewRequest("LOCK", "/dav/lock-refresh-missing-token.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("LOCK refresh without token status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), errLockRefreshRequiresToken.Error()) {
		t.Fatalf("expected missing refresh token error body, got %q", w.Body.String())
	}

	handler.locksMu.Lock()
	_, exists := handler.locks["/lock-refresh-missing-token.txt"]
	handler.locksMu.Unlock()
	if exists {
		t.Fatal("expected bodyless LOCK without token not to create a new lock")
	}
}

func TestHandler_LOCK_RefreshWithinCollectionScopeWithMatchingIfToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/lock-refresh-scope"); err != nil {
		t.Fatalf("Mkdir(lock-refresh-scope) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/lock-refresh-scope/child.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-scope", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("initial collection LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	handler.locksMu.Lock()
	before := handler.locks["/lock-refresh-scope"].expiresAt
	handler.locksMu.Unlock()

	refreshReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-scope/child.txt", nil)
	refreshReq.Header.Set("If", "</dav/lock-refresh-scope/child.txt> ("+lockToken+")")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusOK {
		t.Fatalf("scope refresh LOCK status = %d, want %d", refreshW.Code, http.StatusOK)
	}
	if refreshW.Header().Get("Lock-Token") != "" {
		t.Fatalf("expected scope refresh response not to emit Lock-Token header, got %q", refreshW.Header().Get("Lock-Token"))
	}
	if !strings.Contains(refreshW.Body.String(), "<D:depth>infinity</D:depth>") {
		t.Fatalf("expected scope refresh response body to report infinity depth, got %q", refreshW.Body.String())
	}

	handler.locksMu.Lock()
	after := handler.locks["/lock-refresh-scope"].expiresAt
	handler.locksMu.Unlock()
	if !after.After(before) {
		t.Fatalf("expected scope refresh expiry to move forward: before=%v after=%v", before, after)
	}
}

func TestHandler_LOCK_RefreshDepthZeroCollectionOutsideScopeFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/lock-refresh-depth-zero"); err != nil {
		t.Fatalf("Mkdir(lock-refresh-depth-zero) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/lock-refresh-depth-zero/child.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-depth-zero", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockReq.Header.Set("Depth", "0")
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("initial depth-zero collection LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	handler.locksMu.Lock()
	before := handler.locks["/lock-refresh-depth-zero"].expiresAt
	handler.locksMu.Unlock()

	refreshReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-depth-zero/child.txt", nil)
	refreshReq.Header.Set("If", "</dav/lock-refresh-depth-zero/child.txt> ("+lockToken+")")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusPreconditionFailed {
		t.Fatalf("depth-zero collection refresh outside scope status = %d, want %d", refreshW.Code, http.StatusPreconditionFailed)
	}
	if !strings.Contains(refreshW.Body.String(), errLockTokenMatchesRequestURI.Error()) {
		t.Fatalf("expected lock-token-matches-request-uri failure, got %q", refreshW.Body.String())
	}

	handler.locksMu.Lock()
	after := handler.locks["/lock-refresh-depth-zero"].expiresAt
	handler.locksMu.Unlock()
	if !after.Equal(before) {
		t.Fatalf("expected failed out-of-scope refresh not to move expiry: before=%v after=%v", before, after)
	}
}

func TestHandler_LOCK_RefreshWithMismatchedTaggedIfURIFails(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/lock-refresh-tagged"); err != nil {
		t.Fatalf("Mkdir(lock-refresh-tagged) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/lock-refresh-tagged/child.txt", bytes.NewReader([]byte("lock target"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other-refresh-target.txt", bytes.NewReader([]byte("other target"))); err != nil {
		t.Fatalf("WriteFile(other-refresh-target.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-tagged", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("initial collection LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	refreshReq := httptest.NewRequest("LOCK", "/dav/lock-refresh-tagged/child.txt", nil)
	refreshReq.Header.Set("If", "</dav/other-refresh-target.txt> ("+lockToken+")")
	refreshW := httptest.NewRecorder()
	handler.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusPreconditionFailed {
		t.Fatalf("refresh LOCK with mismatched tagged URI status = %d, want %d", refreshW.Code, http.StatusPreconditionFailed)
	}
	if !strings.Contains(refreshW.Body.String(), errLockTokenMatchesRequestURI.Error()) {
		t.Fatalf("expected lock-token-matches-request-uri failure, got %q", refreshW.Body.String())
	}
}

func TestHandler_LOCK_DepthInfinityConflictsWithLockedDescendant(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/lock-conflict-parent"); err != nil {
		t.Fatalf("Mkdir(lock-conflict-parent) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/lock-conflict-parent/child.txt", bytes.NewReader([]byte("locked child"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}

	childLockReq := httptest.NewRequest("LOCK", "/dav/lock-conflict-parent/child.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	childLockW := httptest.NewRecorder()
	handler.ServeHTTP(childLockW, childLockReq)
	if childLockW.Code != http.StatusOK {
		t.Fatalf("child LOCK status = %d, want %d", childLockW.Code, http.StatusOK)
	}

	parentLockReq := httptest.NewRequest("LOCK", "/dav/lock-conflict-parent", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	parentLockW := httptest.NewRecorder()
	handler.ServeHTTP(parentLockW, parentLockReq)

	if parentLockW.Code != http.StatusLocked {
		t.Fatalf("parent LOCK with locked descendant status = %d, want %d", parentLockW.Code, http.StatusLocked)
	}
	if parentLockW.Header().Get("Lock-Token") != "" {
		t.Fatalf("expected conflicting LOCK not to emit Lock-Token header, got %q", parentLockW.Header().Get("Lock-Token"))
	}

	handler.locksMu.Lock()
	_, exists := handler.locks["/lock-conflict-parent"]
	handler.locksMu.Unlock()
	if exists {
		t.Fatal("expected conflicting collection LOCK not to persist lock state")
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
		req.Header.Set("Destination", "http://example.com/dav/locked-dst/file.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MOVE without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("CopyFromLockedResourceDoesNotRequireToken", func(t *testing.T) {
		req := httptest.NewRequest("COPY", "/dav/locked/file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/locked-dst/copied.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("COPY from locked source status = %d, want %d", w.Code, http.StatusCreated)
		}
		if _, err := fs.Stat(ctx, "/locked/file.txt"); err != nil {
			t.Fatalf("expected locked source file to remain after COPY, got %v", err)
		}
		if _, err := fs.Stat(ctx, "/locked-dst/copied.txt"); err != nil {
			t.Fatalf("expected copied destination file to exist, got %v", err)
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

	t.Run("PutWithIfHeaderTokenSucceeds", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked/file.txt", strings.NewReader("updated via if"))
		req.Header.Set("If", "(<"+strings.Trim(lockToken, "<>")+">)")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT with If token status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("PutWithNegatedIfHeaderTokenFails", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/locked/file.txt", strings.NewReader("updated via negated if"))
		req.Header.Set("If", "(Not <"+strings.Trim(lockToken, "<>")+">)")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("PUT with negated If token status = %d, want %d", w.Code, http.StatusLocked)
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
		req.Header.Set("Destination", "http://example.com/dav/locked-dir/moved.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MOVE into locked collection without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("CopyIntoLockedCollectionRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("COPY", "/dav/src/file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/locked-dir/copied.txt")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("COPY into locked collection without token status = %d, want %d", w.Code, http.StatusLocked)
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

	t.Run("MoveIntoLockedCollectionWithIfHeaderTokenSucceeds", func(t *testing.T) {
		req := httptest.NewRequest("MOVE", "/dav/src/file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/locked-dir/if-header.txt")
		req.Header.Set("If", "</dav/locked-dir> (<"+strings.Trim(lockToken, "<>")+">)")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("MOVE with If token status = %d, want %d", w.Code, http.StatusCreated)
		}
	})

	t.Run("CopyIntoLockedCollectionWithIfHeaderTokenSucceeds", func(t *testing.T) {
		if err := fs.WriteFile(ctx, "/src/copy-file.txt", bytes.NewReader([]byte("copy me too"))); err != nil {
			t.Fatalf("WriteFile(src/copy-file.txt) error: %v", err)
		}

		req := httptest.NewRequest("COPY", "/dav/src/copy-file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/locked-dir/copied-if.txt")
		req.Header.Set("If", "</dav/locked-dir> (<"+strings.Trim(lockToken, "<>")+">)")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("COPY with If token status = %d, want %d", w.Code, http.StatusCreated)
		}
	})

	t.Run("MoveIntoLockedCollectionWithMismatchedTaggedIfTokenFails", func(t *testing.T) {
		req := httptest.NewRequest("MOVE", "/dav/src/file.txt", nil)
		req.Header.Set("Destination", "http://example.com/dav/locked-dir/tagged-mismatch.txt")
		req.Header.Set("If", "</dav/src/file.txt> (<"+strings.Trim(lockToken, "<>")+">)")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("MOVE with mismatched If tag status = %d, want %d", w.Code, http.StatusLocked)
		}
	})
}

func TestHandler_LOCK_DepthZeroCollectionHonorsNamespaceSemantics(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/depth-zero-dir"); err != nil {
		t.Fatalf("Mkdir(depth-zero-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/depth-zero-dir/existing.txt", bytes.NewReader([]byte("initial"))); err != nil {
		t.Fatalf("WriteFile(existing.txt) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/depth-zero-dir/subdir"); err != nil {
		t.Fatalf("Mkdir(subdir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/depth-zero-dir/subdir/existing.txt", bytes.NewReader([]byte("nested"))); err != nil {
		t.Fatalf("WriteFile(subdir/existing.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/depth-zero-dir", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockReq.Header.Set("Depth", "0")
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK depth-zero collection status = %d, want %d", lockW.Code, http.StatusOK)
	}
	if !strings.Contains(lockW.Body.String(), "<D:depth>0</D:depth>") {
		t.Fatalf("expected depth-zero lock response body, got %q", lockW.Body.String())
	}
	lockToken := lockW.Header().Get("Lock-Token")

	t.Run("PutExistingDirectChildDoesNotRequireToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/depth-zero-dir/existing.txt", strings.NewReader("updated"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT existing direct child without token status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("PutNewDirectChildRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/depth-zero-dir/new.txt", strings.NewReader("new content"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("PUT new direct child without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("DeleteDirectChildRequiresToken", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/dav/depth-zero-dir/existing.txt", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusLocked {
			t.Fatalf("DELETE direct child without token status = %d, want %d", w.Code, http.StatusLocked)
		}
	})

	t.Run("PutNestedExistingDescendantDoesNotRequireToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/depth-zero-dir/subdir/existing.txt", strings.NewReader("nested update"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT nested descendant without token status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("PutNestedCreateDoesNotRequireToken", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/dav/depth-zero-dir/subdir/new.txt", strings.NewReader("nested create"))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("PUT nested create without token status = %d, want %d", w.Code, http.StatusCreated)
		}
	})

	t.Run("MkcolDirectChildWithTokenSucceeds", func(t *testing.T) {
		req := httptest.NewRequest("MKCOL", "/dav/depth-zero-dir/with-token", nil)
		req.Header.Set("Lock-Token", lockToken)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("MKCOL direct child with token status = %d, want %d", w.Code, http.StatusCreated)
		}
	})
}

func TestHandler_DeleteLockedCollectionRequiresDescendantToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locked-parent"); err != nil {
		t.Fatalf("Mkdir(locked-parent) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/locked-parent/child.txt", bytes.NewReader([]byte("locked child"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/locked-parent/child.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK child status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/dav/locked-parent", nil)
	deleteW := httptest.NewRecorder()
	handler.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusLocked {
		t.Fatalf("DELETE locked parent without descendant token status = %d, want %d", deleteW.Code, http.StatusLocked)
	}

	deleteReq = httptest.NewRequest(http.MethodDelete, "/dav/locked-parent", nil)
	deleteReq.Header.Set("Lock-Token", lockToken)
	deleteW = httptest.NewRecorder()
	handler.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("DELETE locked parent with descendant token status = %d, want %d", deleteW.Code, http.StatusNoContent)
	}

	if _, err := fs.Stat(ctx, "/locked-parent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected locked parent directory to be deleted, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/locked-parent/child.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected locked child to be deleted with parent, got %v", err)
	}

	handler.locksMu.Lock()
	_, stillLocked := handler.locks["/locked-parent/child.txt"]
	handler.locksMu.Unlock()
	if stillLocked {
		t.Fatal("expected descendant lock to be cleared after deleting the locked collection")
	}
}

func TestHandler_MoveLockedCollectionRequiresDescendantToken(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/move-locked-parent"); err != nil {
		t.Fatalf("Mkdir(move-locked-parent) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/move-locked-dst"); err != nil {
		t.Fatalf("Mkdir(move-locked-dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-locked-parent/child.txt", bytes.NewReader([]byte("locked child"))); err != nil {
		t.Fatalf("WriteFile(child.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/move-locked-parent/child.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK child status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	moveReq := httptest.NewRequest("MOVE", "/dav/move-locked-parent", nil)
	moveReq.Header.Set("Destination", "http://example.com/dav/move-locked-dst/moved")
	moveW := httptest.NewRecorder()
	handler.ServeHTTP(moveW, moveReq)
	if moveW.Code != http.StatusLocked {
		t.Fatalf("MOVE locked parent without descendant token status = %d, want %d", moveW.Code, http.StatusLocked)
	}

	moveReq = httptest.NewRequest("MOVE", "/dav/move-locked-parent", nil)
	moveReq.Header.Set("Destination", "http://example.com/dav/move-locked-dst/moved")
	moveReq.Header.Set("Lock-Token", lockToken)
	moveW = httptest.NewRecorder()
	handler.ServeHTTP(moveW, moveReq)
	if moveW.Code != http.StatusCreated {
		t.Fatalf("MOVE locked parent with descendant token status = %d, want %d", moveW.Code, http.StatusCreated)
	}

	if _, err := fs.Stat(ctx, "/move-locked-parent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected source directory to be moved away, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/move-locked-dst/moved/child.txt"); err != nil {
		t.Fatalf("expected moved child to exist, got %v", err)
	}

	handler.locksMu.Lock()
	_, oldLocked := handler.locks["/move-locked-parent/child.txt"]
	_, newLocked := handler.locks["/move-locked-dst/moved/child.txt"]
	handler.locksMu.Unlock()
	if oldLocked {
		t.Fatal("expected descendant lock to move away from source subtree")
	}
	if !newLocked {
		t.Fatal("expected descendant lock to transfer with moved subtree")
	}
}

func TestHandler_DeleteWithMatchingTokenClearsLockState(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-locked.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile(delete-locked.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/delete-locked.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/dav/delete-locked.txt", nil)
	deleteReq.Header.Set("Lock-Token", lockToken)
	deleteW := httptest.NewRecorder()
	handler.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("DELETE with token status = %d, want %d", deleteW.Code, http.StatusNoContent)
	}

	handler.locksMu.Lock()
	_, stillLocked := handler.locks["/delete-locked.txt"]
	handler.locksMu.Unlock()
	if stillLocked {
		t.Fatal("expected file lock to be cleared after successful DELETE")
	}

	putReq := httptest.NewRequest(http.MethodPut, "/dav/delete-locked.txt", strings.NewReader("recreated"))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusCreated {
		t.Fatalf("PUT after deleting locked file status = %d, want %d", putW.Code, http.StatusCreated)
	}
}

func TestHandler_MoveWithMatchingTokenTransfersLockToDestination(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/move-lock-dst"); err != nil {
		t.Fatalf("Mkdir(move-lock-dst) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-lock-src.txt", bytes.NewReader([]byte("move me"))); err != nil {
		t.Fatalf("WriteFile(move-lock-src.txt) error: %v", err)
	}

	lockReq := httptest.NewRequest("LOCK", "/dav/move-lock-src.txt", strings.NewReader(`<?xml version="1.0"?><lockinfo/>`))
	lockW := httptest.NewRecorder()
	handler.ServeHTTP(lockW, lockReq)
	if lockW.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, want %d", lockW.Code, http.StatusOK)
	}
	lockToken := lockW.Header().Get("Lock-Token")

	moveReq := httptest.NewRequest("MOVE", "/dav/move-lock-src.txt", nil)
	moveReq.Header.Set("Destination", "http://example.com/dav/move-lock-dst/moved.txt")
	moveReq.Header.Set("Lock-Token", lockToken)
	moveW := httptest.NewRecorder()
	handler.ServeHTTP(moveW, moveReq)
	if moveW.Code != http.StatusCreated {
		t.Fatalf("MOVE with token status = %d, want %d", moveW.Code, http.StatusCreated)
	}

	handler.locksMu.Lock()
	_, oldLocked := handler.locks["/move-lock-src.txt"]
	movedLock, newLocked := handler.locks["/move-lock-dst/moved.txt"]
	handler.locksMu.Unlock()
	if oldLocked {
		t.Fatal("expected source lock to be removed after MOVE")
	}
	if !newLocked {
		t.Fatal("expected lock to transfer to destination after MOVE")
	}
	if movedLock.token != strings.Trim(lockToken, "<>") {
		t.Fatalf("expected transferred lock token %q, got %q", strings.Trim(lockToken, "<>"), movedLock.token)
	}

	putOldReq := httptest.NewRequest(http.MethodPut, "/dav/move-lock-src.txt", strings.NewReader("new source"))
	putOldW := httptest.NewRecorder()
	handler.ServeHTTP(putOldW, putOldReq)
	if putOldW.Code != http.StatusCreated {
		t.Fatalf("PUT old source path after MOVE status = %d, want %d", putOldW.Code, http.StatusCreated)
	}

	putMovedReq := httptest.NewRequest(http.MethodPut, "/dav/move-lock-dst/moved.txt", strings.NewReader("overwrite moved"))
	putMovedW := httptest.NewRecorder()
	handler.ServeHTTP(putMovedW, putMovedReq)
	if putMovedW.Code != http.StatusLocked {
		t.Fatalf("PUT moved destination without token status = %d, want %d", putMovedW.Code, http.StatusLocked)
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
		"/dav/..%5Csecret.txt",
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
	req.Host = "localhost"
	req.Header.Set("Destination", "http://localhost/dav/dst/foo..txt")

	dst := handler.getDestination(req)

	if dst != "/dst/foo..txt" {
		t.Fatalf("getDestination() = %q, want %q", dst, "/dst/foo..txt")
	}
}

func TestHandler_GetDestination_RejectsCrossHostDestination(t *testing.T) {
	handler := NewHandler(Config{Prefix: "/dav", AuthType: "none"})
	req := httptest.NewRequest("COPY", "/dav/src.txt", nil)
	req.Host = "localhost"
	req.Header.Set("Destination", "http://evil.example/dav/dst/foo.txt")

	if dst := handler.getDestination(req); dst != "" {
		t.Fatalf("getDestination() = %q, want empty string for cross-host destination", dst)
	}
}

func TestHandler_GetDestination_RejectsPercentEncodedTraversal(t *testing.T) {
	handler := NewHandler(Config{Prefix: "/dav", AuthType: "none"})
	req := httptest.NewRequest("COPY", "/dav/src.txt", nil)
	req.Host = "localhost"
	req.Header.Set("Destination", "http://localhost/dav/%2e%2e/secret.txt")

	if dst := handler.getDestination(req); dst != "" {
		t.Fatalf("getDestination() = %q, want empty string for encoded traversal destination", dst)
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

func TestHandler_DirectoryListing_EscapesHrefSpecialCharacters(t *testing.T) {
	handler, fs, _ := setupTestHandler(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/listing-special"); err != nil {
		t.Fatalf("Mkdir(/listing-special) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/listing-special/hash #file?.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(/listing-special/hash #file?.txt) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/dav/listing-special/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET dir status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, `<a href="/dav/listing-special/hash%20%23file%3F.txt">hash #file?.txt</a>`) {
		t.Fatalf("directory listing should percent-encode special characters in href, got %q", body)
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
