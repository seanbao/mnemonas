package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

type fakeShareFS struct {
	statInfo       *storage.FileInfo
	dirItems       []*storage.FileInfo
	dirItemsByPath map[string][]*storage.FileInfo
	openByPath     map[string]FileReader
	openErrByPath  map[string]error
	readDirErr     error
}

type failingReadCloser struct {
	err error
}

func (f *failingReadCloser) Read(p []byte) (int, error) {
	return 0, f.err
}

func (f *failingReadCloser) Close() error {
	return nil
}

func (f *fakeShareFS) OpenFile(ctx context.Context, filePath string) (FileReader, error) {
	if f.openErrByPath != nil {
		if err, ok := f.openErrByPath[filePath]; ok {
			return nil, err
		}
	}
	if f.openByPath != nil {
		if reader, ok := f.openByPath[filePath]; ok {
			return reader, nil
		}
	}
	return nil, nil
}

func (f *fakeShareFS) Stat(ctx context.Context, filePath string) (*storage.FileInfo, error) {
	if f.statInfo == nil {
		return nil, storage.ErrNotFound
	}
	return f.statInfo, nil
}

func (f *fakeShareFS) ReadDir(ctx context.Context, filePath string) ([]*storage.FileInfo, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	if f.dirItemsByPath != nil {
		if items, ok := f.dirItemsByPath[filePath]; ok {
			return items, nil
		}
	}
	return f.dirItems, nil
}

func newRouteRequest(method, target, id string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func decodeResponseBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return payload
}

func decodeEnvelopeData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode envelope: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success envelope, got %s", recorder.Body.String())
	}
	return payload.Data
}

func TestCreateShare_UsesBaseURL(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.SetBaseURL("https://nas.example.com/")

	body, err := json.Marshal(CreateShareRequest{
		Path: "/docs/report.pdf",
		Type: "file",
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()
	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", recorder.Code)
	}

	payload := decodeEnvelopeData(t, recorder)
	urlValue, ok := payload["url"].(string)
	if !ok {
		t.Fatalf("expected url in response")
	}
	if !strings.HasPrefix(urlValue, "https://nas.example.com/s/") {
		t.Fatalf("expected base URL applied, got %q", urlValue)
	}
	if strings.Contains(urlValue, "https://nas.example.com//s/") {
		t.Fatalf("expected trimmed base URL, got %q", urlValue)
	}
}

func TestCreateShare_InvalidNegativeExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","expires_in":"-1h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_NegativeMaxAccessReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","max_access":-1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_MAX_ACCESS" {
		t.Fatalf("expected INVALID_MAX_ACCESS code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_WhitespaceOnlyPathReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"   ","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "MISSING_PATH" {
		t.Fatalf("expected MISSING_PATH code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_NormalizesRelativePath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"docs/report.pdf","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeEnvelopeData(t, recorder)
	pathValue, ok := payload["path"].(string)
	if !ok {
		t.Fatalf("expected path in response, got %v", payload)
	}
	if pathValue != "/docs/report.pdf" {
		t.Fatalf("expected normalized absolute path, got %q", pathValue)
	}
}

func TestCreateShare_InvalidTypeReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"symlink"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_SHARE_TYPE" {
		t.Fatalf("expected INVALID_SHARE_TYPE code, got %v", errorPayload["code"])
	}
}

func TestCreateShare_InvalidPermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"path":"/docs/report.pdf","type":"file","permission":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.CreateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION code, got %v", errorPayload["code"])
	}
}

func TestListShares_WrapsResponseData(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	_, err = store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.ListShares(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", recorder.Body.String())
	}
	if len(payload.Data) != 1 {
		t.Fatalf("expected 1 share, got %d", len(payload.Data))
	}
}

func TestUpdateShare_InvalidExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"expires_in":"not-a-duration"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid expires_in format" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}
}

func TestUpdateShare_InvalidPermissionReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"permission":"admin"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_PERMISSION" {
		t.Fatalf("expected INVALID_PERMISSION, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid permission" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}
}

func TestUpdateShare_NonPositiveExpiresInReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"expires_in":"0s"}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != "INVALID_EXPIRES_IN" {
		t.Fatalf("expected INVALID_EXPIRES_IN error, got %s", recorder.Body.String())
	}
}

func TestUpdateShare_NegativeMaxAccessReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	body := []byte(`{"max_access":-1}`)
	req := newRouteRequest(http.MethodPut, "/api/v1/shares/"+share.ID, share.ID, body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user1"}))
	recorder := httptest.NewRecorder()

	handler.UpdateShare(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}

	var payload responseEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error == nil {
		t.Fatalf("expected error payload, got %s", recorder.Body.String())
	}
	if payload.Error.Code != "INVALID_MAX_ACCESS" {
		t.Fatalf("expected INVALID_MAX_ACCESS, got %q", payload.Error.Code)
	}
	if payload.Error.Message != "invalid max_access" {
		t.Fatalf("unexpected error message: %q", payload.Error.Message)
	}

	updated, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if updated.MaxAccess != 0 {
		t.Fatalf("expected max_access to remain unchanged, got %d", updated.MaxAccess)
	}
}

func TestAccessShare_PublicInfoFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:  share.Path,
			Name:  "report.pdf",
			Size:  1234,
			IsDir: false,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "report.pdf" {
		t.Fatalf("expected file_name report.pdf, got %v", payload["file_name"])
	}
	if payload["file_size"] != float64(1234) {
		t.Fatalf("expected file_size 1234, got %v", payload["file_size"])
	}
}

func TestAccessShare_WithPasswordDoesNotExposeInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{
			Path:  share.Path,
			Name:  "secret.pdf",
			Size:  4321,
			IsDir: false,
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if _, exists := payload["file_name"]; exists {
		t.Fatalf("expected file_name to be omitted for password-protected share")
	}
	if _, exists := payload["file_size"]; exists {
		t.Fatalf("expected file_size to be omitted for password-protected share")
	}
}

func TestAccessShareWithPassword_FolderInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItems: []*storage.FileInfo{
			{Path: "/docs/a.txt", Name: "a.txt"},
			{Path: "/docs/b.txt", Name: "b.txt"},
		},
	})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "docs" {
		t.Fatalf("expected file_name docs, got %v", payload["file_name"])
	}
	if payload["folder_items"] != float64(2) {
		t.Fatalf("expected folder_items 2, got %v", payload["folder_items"])
	}
	if len(recorder.Result().Cookies()) != 1 {
		t.Fatalf("expected access cookie to be set")
	}
}

func TestAccessShareWithPassword_IgnoresSpoofedForwardedProtoForCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{statInfo: &storage.FileInfo{Name: "docs", IsDir: true}})
	body := []byte(`{"password":"secret"}`)
	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()

	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("expected spoofed forwarded proto to leave share cookie insecure")
	}
}

func TestAccessShareWithPassword_DoesNotIncrementAccessCount(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	current, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to load share: %v", err)
	}
	if current.AccessCount != 0 {
		t.Fatalf("expected access_count 0, got %d", current.AccessCount)
	}
}

func TestListShareItems_PublicFolder(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 12, IsDir: false},
				{Path: "/docs/sub", Name: "sub", Size: 0, IsDir: true},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload struct {
		Path  string           `json:"path"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Path != "" {
		t.Fatalf("expected empty path, got %q", payload.Path)
	}

	paths := map[string]bool{}
	for _, item := range payload.Items {
		pathValue, _ := item["path"].(string)
		isDirValue, _ := item["is_dir"].(bool)
		paths[pathValue] = isDirValue
	}
	if _, ok := paths["a.txt"]; !ok {
		t.Fatalf("expected a.txt entry in list")
	}
	if isDir, ok := paths["sub"]; !ok || !isDir {
		t.Fatalf("expected sub directory entry in list")
	}
}

func TestListShareItems_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=../secret", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}

func TestListShareItems_RequiresPassword(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", recorder.Code)
	}
}

func TestListShareItems_ExpiredProtectedShareReturnsGoneWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestAccessShare_WithValidCookieExposesInfo(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 42, IsDir: false},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	payload := decodeResponseBody(t, recorder)
	if payload["file_name"] != "secret.pdf" {
		t.Fatalf("expected file_name secret.pdf, got %v", payload["file_name"])
	}
}

func TestAccessShare_ExpiredProtectedShareWithCookieReturnsGone(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		statInfo: &storage.FileInfo{Path: share.Path, Name: "secret.pdf", Size: 42, IsDir: false},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_UsesCookieAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	req.AddCookie(&http.Cookie{Name: shareAccessCookieName(share.ID), Value: handler.shareAccessToken(share)})
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
}

func TestDownloadShareFile_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/../secret", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.DownloadShareFile(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_NilReaderReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShare(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("expected FILE_NOT_FOUND code, got %v", errorPayload["code"])
	}
}

func TestDownloadShare_OpenFileInternalErrorReturnsInternalServerError(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			"/docs/report.pdf": errors.New("backend offline"),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShare(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}

func TestDownloadShare_EscapesQuotedFilenameInContentDisposition(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report\"2026.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report\"2026.pdf": io.NopCloser(strings.NewReader("ok")),
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShare(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("expected valid Content-Disposition header, got %v", err)
	}
	if mediaType != "attachment" {
		t.Fatalf("expected attachment disposition, got %q", mediaType)
	}
	if params["filename"] != "report\"2026.pdf" {
		t.Fatalf("expected preserved filename, got %q", params["filename"])
	}
}

func TestDownloadShare_ReturnsInternalErrorWhenStreamingFailsBeforeWrite(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			"/docs/report.pdf": &failingReadCloser{err: errors.New("stream failed")},
		},
	})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShare(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}

func TestDownloadShare_ExpiredProtectedShareReturnsGoneWithoutCookie(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if err := store.Update(share.ID, func(s *Share) error {
		s.ExpiresAt = &expiredAt
		return nil
	}); err != nil {
		t.Fatalf("failed to expire share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_EXPIRED" {
		t.Fatalf("expected SHARE_EXPIRED code, got %v", errorPayload["code"])
	}
}

func TestDownloadShareFile_PreservesWhitespaceInPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/ report .txt"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("ok")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/%20report%20.txt", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShareFile(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
}

func TestDownloadShareFile_EscapesQuotedFilenameInContentDisposition(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report\"2026.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: io.NopCloser(strings.NewReader("ok")),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report%222026.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShareFile(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("expected valid Content-Disposition header, got %v", err)
	}
	if mediaType != "attachment" {
		t.Fatalf("expected attachment disposition, got %q", mediaType)
	}
	if params["filename"] != "report\"2026.pdf" {
		t.Fatalf("expected preserved filename, got %q", params["filename"])
	}
}

func TestDownloadShareFile_ReturnsInternalErrorWhenStreamingFailsBeforeWrite(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openByPath: map[string]FileReader{
			targetPath: &failingReadCloser{err: errors.New("stream failed")},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShareFile(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}

func TestDownloadShareFile_OpenFileInternalErrorReturnsInternalServerError(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	targetPath := "/docs/report.pdf"
	handler := NewHandler(store, &fakeShareFS{
		openErrByPath: map[string]error{
			targetPath: errors.New("backend offline"),
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/download/report.pdf", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.DownloadShareFile(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "DOWNLOAD_SHARE_FAILED" {
		t.Fatalf("expected DOWNLOAD_SHARE_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}

func TestListShareItems_PreservesWhitespaceInPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs/ report ": {
				{Path: "/docs/ report /a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=%20report%20", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Path != " report " {
		t.Fatalf("expected whitespace path to be preserved, got %q", payload.Path)
	}
}

func TestListShareItems_NormalizesReturnedPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/docs/a.txt", Name: "a.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items?path=sub/..", share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Path != "" {
		t.Fatalf("expected normalized root path, got %q", payload.Path)
	}
}

func TestAccessShare_AccessLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/report.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	err = store.Update(share.ID, func(s *Share) error {
		s.AccessCount = 1
		return nil
	})
	if err != nil {
		t.Fatalf("failed to update share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID, share.ID, nil)
	recorder := httptest.NewRecorder()
	handler.AccessShare(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
}

func TestAccessShareWithPassword_AccessLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	err = store.Update(share.ID, func(s *Share) error {
		s.AccessCount = 1
		return nil
	})
	if err != nil {
		t.Fatalf("failed to update share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})

	body, err := json.Marshal(map[string]string{"password": "secret"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status 410, got %d", recorder.Code)
	}
}

func TestAccessShareWithPassword_RateLimitedAfterRepeatedFailures(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0

	body, err := json.Marshal(map[string]string{"password": "wrong"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	for attempt := 1; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
		recorder := httptest.NewRecorder()

		handler.AccessShareWithPassword(recorder, req)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected status 401, got %d", attempt, recorder.Code)
		}
	}

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 on lock, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "SHARE_PASSWORD_RATE_LIMITED" {
		t.Fatalf("expected SHARE_PASSWORD_RATE_LIMITED code, got %v", errorPayload["code"])
	}

	lockedReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	lockedRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(lockedRecorder, lockedReq)

	if lockedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 while locked, got %d", lockedRecorder.Code)
	}
}

func TestAccessShareWithPassword_LockExpiresAndSuccessResetsFailures(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0
	handler.passwordLockDuration = time.Minute

	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	handler.passwordAttempts.now = func() time.Time { return now }

	wrongBody := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, req)
	}

	now = now.Add(handler.passwordLockDuration + time.Second)

	successReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	successRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(successRecorder, successReq)

	if successRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200 after lock expiry, got %d", successRecorder.Code)
	}

	postSuccessReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
	postSuccessRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(postSuccessRecorder, postSuccessReq)

	if postSuccessRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 after successful reset, got %d", postSuccessRecorder.Code)
	}
}

func TestAccessShareWithPassword_StaleFailuresExpire(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0
	handler.passwordFailureWindow = time.Minute
	handler.passwordLockDuration = time.Minute

	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	handler.passwordAttempts.now = func() time.Time { return now }

	wrongBody := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit-1; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
		recorder := httptest.NewRecorder()
		handler.AccessShareWithPassword(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", attempt+1, recorder.Code)
		}
	}

	now = now.Add(handler.passwordFailureWindow + time.Second)

	req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, wrongBody)
	recorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected stale failures to expire and return 401, got %d", recorder.Code)
	}

	lockedReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	lockedRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(lockedRecorder, lockedReq)

	if lockedRecorder.Code != http.StatusOK {
		t.Fatalf("expected valid password after stale failures expiry, got %d", lockedRecorder.Code)
	}
}

func TestPasswordAttemptTracker_PrunesStaleEntriesOnNewFailure(t *testing.T) {
	tracker := newPasswordAttemptTracker()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	tracker.attempts["stale"] = passwordAttemptState{failures: 1, lastFailure: now.Add(-2 * time.Minute)}

	tracker.recordFailure("fresh", 5, time.Minute, time.Minute)

	if _, ok := tracker.attempts["stale"]; ok {
		t.Fatal("expected stale tracker entry to be pruned")
	}
	if _, ok := tracker.attempts["fresh"]; !ok {
		t.Fatal("expected fresh tracker entry to remain")
	}
}

func TestClientIdentifier_IgnoresSpoofedForwardedHeadersFromUntrustedSource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/s/share-1", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := clientIdentifier(req); got != "203.0.113.5" {
		t.Fatalf("clientIdentifier() = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientIdentifier_UsesForwardedHeadersFromTrustedProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/s/share-1", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 127.0.0.1")

	if got := clientIdentifier(req); got != "198.51.100.20" {
		t.Fatalf("clientIdentifier() = %q, want %q", got, "198.51.100.20")
	}
}

func TestAccessShareWithPassword_SpoofedForwardedHeaderDoesNotBypassRateLimit(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs/secret.pdf",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{})
	handler.passwordFailureDelay = 0

	body := []byte(`{"password":"wrong"}`)
	for attempt := 0; attempt < handler.passwordFailureLimit; attempt++ {
		req := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, body)
		req.Header.Set("X-Forwarded-For", net.IPv4(198, 51, 100, byte(20+attempt)).String())
		recorder := httptest.NewRecorder()

		handler.AccessShareWithPassword(recorder, req)
	}

	bypassReq := newRouteRequest(http.MethodPost, "/s/"+share.ID, share.ID, []byte(`{"password":"secret"}`))
	bypassReq.Header.Set("X-Forwarded-For", "198.51.100.250")
	bypassRecorder := httptest.NewRecorder()
	handler.AccessShareWithPassword(bypassRecorder, bypassReq)

	if bypassRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed forwarded header to stay rate limited, got %d", bypassRecorder.Code)
	}
}

func TestListShareItems_DoesNotLeakInternalErrors(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{readDirErr: errors.New("database offline")})
	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	body := recorder.Body.String()
	if strings.Contains(body, "database offline") {
		t.Fatalf("expected internal error details to be hidden, got %q", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic public error message, got %q", body)
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "LIST_SHARE_ITEMS_FAILED" {
		t.Fatalf("expected LIST_SHARE_ITEMS_FAILED code, got %v", errorPayload["code"])
	}
}

func TestListShareItems_RejectsEntriesOutsideShareRoot(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	handler := NewHandler(store, &fakeShareFS{
		dirItemsByPath: map[string][]*storage.FileInfo{
			"/docs": {
				{Path: "/other/secret.txt", Name: "secret.txt", Size: 1, IsDir: false},
			},
		},
	})

	req := newRouteRequest(http.MethodGet, "/s/"+share.ID+"/items", share.ID, nil)
	recorder := httptest.NewRecorder()

	handler.ListShareItems(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}
	payload := decodeResponseBody(t, recorder)
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %v", payload)
	}
	if errorPayload["code"] != "LIST_SHARE_ITEMS_FAILED" {
		t.Fatalf("expected LIST_SHARE_ITEMS_FAILED code, got %v", errorPayload["code"])
	}
	if errorPayload["message"] != "internal server error" {
		t.Fatalf("expected generic message, got %v", errorPayload["message"])
	}
}
