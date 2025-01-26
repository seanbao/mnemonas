package share

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

type fakeShareFS struct {
	statInfo       *storage.FileInfo
	dirItems       []*storage.FileInfo
	dirItemsByPath map[string][]*storage.FileInfo
	openByPath     map[string]FileReader
}

func (f *fakeShareFS) OpenFile(ctx context.Context, filePath string) (FileReader, error) {
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
	if f.dirItemsByPath != nil {
		if items, ok := f.dirItemsByPath[filePath]; ok {
			return items, nil
		}
	}
	return f.dirItems, nil
}

func newRouteRequest(method, target, id string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
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

	payload := decodeResponseBody(t, recorder)
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
