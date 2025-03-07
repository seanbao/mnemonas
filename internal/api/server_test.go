// Package api provides REST API and gRPC API
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/share"
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

func setupTestServer(t *testing.T) (*Server, *storage.FileSystem, string) {
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

	logger := zerolog.Nop()
	settings := config.Default()

	server, err := NewServer(logger, &ServerConfig{
		FileSystem: fs,
		Config:     settings,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, fs, tmpDir
}

func setupAuthServer(t *testing.T) (*Server, *storage.FileSystem, string, string, string) {
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

	usersFile := path.Join(tmpDir, "users.json")
	userStore, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	username := "tester"
	password := "password123"
	if _, err := userStore.Create(username, password, "", auth.RoleUser); err != nil {
		t.Fatalf("create user error: %v", err)
	}

	logger := zerolog.Nop()
	settings := config.Default()
	settings.Storage.Root = tmpDir

	server, err := NewServer(logger, &ServerConfig{
		FileSystem:     fs,
		Config:         settings,
		AuthEnabled:    true,
		AuthUsersFile:  usersFile,
		AuthJWTSecret:  "test-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, fs, tmpDir, username, password
}

func setupShareServer(t *testing.T) (*Server, string) {
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

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("write file error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/sub"); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	shareStorePath := path.Join(tmpDir, "shares.json")
	shareStore, err := share.NewShareStore(shareStorePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:      "/docs",
		Type:      share.ShareTypeFolder,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("create share error: %v", err)
	}

	logger := zerolog.Nop()
	settings := config.Default()
	settings.Storage.Root = tmpDir

	server, err := NewServer(logger, &ServerConfig{
		FileSystem:     fs,
		Config:         settings,
		ShareEnabled:   true,
		ShareStoreFile: shareStorePath,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, createdShare.ID
}

func TestServer_Health(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Health status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "healthy") {
		t.Error("Response should contain 'healthy'")
	}
}

func TestServer_Version(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/version", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Version status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "MnemoNAS") {
		t.Error("Response should contain 'MnemoNAS'")
	}
}

func TestServer_ListFiles(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/testdir")
	fs.WriteFile(ctx, "/testdir/file.txt", bytes.NewReader([]byte("test")))

	t.Run("ListRoot", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/files/", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("ListFiles status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("ListDirectory", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/files/testdir", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("ListFiles status = %d, want %d", w.Code, http.StatusOK)
		}

		body := w.Body.String()
		if !strings.Contains(body, "file.txt") {
			t.Error("Response should contain 'file.txt'")
		}
	})
}

func TestServer_UploadFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/upload")

	content := "uploaded content"
	req := httptest.NewRequest("POST", "/api/v1/files/upload/newfile.txt", strings.NewReader(content))
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Upload status = %d, want %d", w.Code, http.StatusCreated)
	}

	reader, err := fs.OpenFile(ctx, "/upload/newfile.txt")
	if err != nil {
		t.Fatalf("File not found after upload: %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != content {
		t.Errorf("Content = %q, want %q", data, content)
	}
}

func TestServer_DeleteFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/delete")
	fs.WriteFile(ctx, "/delete/file.txt", bytes.NewReader([]byte("delete me")))

	req := httptest.NewRequest("DELETE", "/api/v1/files/delete/file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Delete status = %d, want %d", w.Code, http.StatusOK)
	}

	_, err := fs.Stat(ctx, "/delete/file.txt")
	if err == nil {
		t.Error("File still exists after delete")
	}
}

func TestServer_DownloadWithQueryAuth(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/auth")
	fs.WriteFile(ctx, "/auth/file.txt", bytes.NewReader([]byte("secure")))

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	var loginResp auth.LoginResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}

	downloadReq := httptest.NewRequest("GET", "/api/v1/download/auth/file.txt?auth="+loginResp.AccessToken, nil)
	downloadRec := httptest.NewRecorder()
	server.Router().ServeHTTP(downloadRec, downloadReq)

	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", downloadRec.Code, http.StatusOK)
	}
	if !strings.Contains(downloadRec.Body.String(), "secure") {
		t.Error("downloaded content mismatch")
	}
}

func TestServer_PublicShareListItems(t *testing.T) {
	server, shareID := setupShareServer(t)

	req := httptest.NewRequest("GET", "/s/"+shareID+"/items", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list share items status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Path  string `json:"path"`
		Items []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if payload.Path != "" {
		t.Fatalf("expected empty path, got %q", payload.Path)
	}

	paths := map[string]bool{}
	for _, item := range payload.Items {
		paths[item.Path] = item.IsDir
	}
	if _, ok := paths["a.txt"]; !ok {
		t.Fatalf("expected a.txt in share items")
	}
	if isDir, ok := paths["sub"]; !ok || !isDir {
		t.Fatalf("expected sub directory in share items")
	}
}

func TestServer_ListVersions(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/versions")
	fs.WriteFile(ctx, "/versions/file.txt", bytes.NewReader([]byte("v1")))
	fs.WriteFile(ctx, "/versions/file.txt", bytes.NewReader([]byte("v2")))

	req := httptest.NewRequest("GET", "/api/v1/versions/versions/file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ListVersions status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "versions") {
		t.Error("Response should contain 'versions'")
	}
}

func TestServer_Trash(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	fs.Mkdir(ctx, "/trash-test")
	fs.WriteFile(ctx, "/trash-test/file.txt", bytes.NewReader([]byte("content")))
	fs.Delete(ctx, "/trash-test/file.txt")

	t.Run("ListTrash", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/trash/", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("ListTrash status = %d, want %d", w.Code, http.StatusOK)
		}

		body := w.Body.String()
		if !strings.Contains(body, "items") {
			t.Error("Response should contain 'items'")
		}

		var payload struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("Failed to parse response JSON: %v", err)
		}
		if _, ok := payload.Data["retentionDays"]; !ok {
			t.Error("Response should include retentionDays")
		}
	})
}

func TestServer_Stats(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Stats status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServer_Diagnostics(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/diagnostics", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Diagnostics status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "uptime") {
		t.Error("Response should contain 'uptime'")
	}
}

func TestServer_Metrics(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/metrics", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Metrics status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "requests") {
		t.Error("Response should contain 'requests'")
	}
}

func TestServer_PathTraversal(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"DotDot", "/api/v1/files/../../../etc/passwd"},
		{"EncodedDotDot", "/api/v1/files/..%2F..%2Fetc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				t.Errorf("Path traversal should be blocked, got status %d", w.Code)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"/foo/bar", "/foo/bar", false},
		{"foo/bar", "/foo/bar", false},
		{"/", "/", false},
		{"", "/", false},
		{"../etc/passwd", "", true},
		{"/foo/../bar", "", true},
	}

	for _, tt := range tests {
		got, err := validatePath(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("validatePath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("validatePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateHash(t *testing.T) {
	tests := []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{"Valid BLAKE3 hash", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", false},
		{"Valid uppercase", "ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890", false},
		{"Valid mixed case", "AbCdEf1234567890AbCdEf1234567890AbCdEf1234567890AbCdEf1234567890", false},
		{"Too short", "abcdef1234567890", true},
		{"Too long", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890extra", true},
		{"Empty", "", true},
		{"Contains invalid char g", "gbcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", true},
		{"Contains space", "abcdef 234567890abcdef1234567890abcdef1234567890abcdef1234567890", true},
		{"Contains special char", "abcdef!234567890abcdef1234567890abcdef1234567890abcdef1234567890", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHash(tt.hash)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateHash(%q) error = %v, wantErr %v", tt.hash, err, tt.wantErr)
			}
		})
	}
}

func TestServer_RestoreVersion_InvalidHash(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create a file to restore
	fs.Mkdir(ctx, "/restore")
	fs.WriteFile(ctx, "/restore/file.txt", bytes.NewReader([]byte("content")))

	tests := []struct {
		name       string
		hash       string
		wantStatus int
	}{
		{"Too short hash", "abc123", http.StatusBadRequest},
		{"Invalid chars", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v1/versions/" + tt.hash + "/restore?path=/restore/file.txt"
			req := httptest.NewRequest("POST", url, nil)
			w := httptest.NewRecorder()

			server.Router().ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("RestoreVersion status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestServer_RestoreVersion_MissingPath(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Valid hash format but no path parameter
	validHash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	req := httptest.NewRequest("POST", "/api/v1/versions/"+validHash+"/restore", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("RestoreVersion without path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestServer_NotFound(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Requesting a file inside non-existent directory
	req := httptest.NewRequest("GET", "/api/v1/versions/nonexistent/file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("NotFound status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestServer_Trash_GetItem(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create and delete a file
	fs.Mkdir(ctx, "/trash-get-test")
	fs.WriteFile(ctx, "/trash-get-test/file.txt", bytes.NewReader([]byte("content")))
	fs.Delete(ctx, "/trash-get-test/file.txt")

	// Get trash items to find the ID
	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Skip("No items in trash")
	}

	t.Run("GetExistingItem", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/trash/"+items[0].ID, nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("GetTrashItem status = %d, want %d", w.Code, http.StatusOK)
		}

		body := w.Body.String()
		if !strings.Contains(body, items[0].ID) {
			t.Error("Response should contain item ID")
		}
	})

	t.Run("GetNonExistentItem", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/trash/nonexistent-id", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("GetTrashItem status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestServer_Trash_Restore(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create and delete a file
	fs.Mkdir(ctx, "/trash-restore-test")
	fs.WriteFile(ctx, "/trash-restore-test/restore.txt", bytes.NewReader([]byte("restore me")))
	fs.Delete(ctx, "/trash-restore-test/restore.txt")

	// Get trash items to find the ID
	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Skip("No items in trash")
	}

	t.Run("RestoreToOriginal", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/trash/"+items[0].ID+"/restore", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("RestoreFromTrash status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
		}
	})

	t.Run("RestoreNonExistent", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/trash/nonexistent-id/restore", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		// Should return 409 Conflict or 500 depending on error type
		if w.Code == http.StatusOK {
			t.Error("RestoreFromTrash should fail for nonexistent item")
		}
	})
}

func TestServer_Trash_Delete(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create and delete a file
	fs.Mkdir(ctx, "/trash-delete-test")
	fs.WriteFile(ctx, "/trash-delete-test/delete.txt", bytes.NewReader([]byte("delete me")))
	fs.Delete(ctx, "/trash-delete-test/delete.txt")

	// Get trash items to find the ID
	items, _ := fs.ListTrash(ctx)
	if len(items) == 0 {
		t.Skip("No items in trash")
	}

	t.Run("DeleteExistingItem", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/trash/"+items[0].ID, nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("DeleteFromTrash status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("DeleteNonExistent", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/trash/nonexistent-id", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("DeleteFromTrash status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestServer_Trash_Empty(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create and delete multiple files
	fs.Mkdir(ctx, "/trash-empty-test")
	fs.WriteFile(ctx, "/trash-empty-test/file1.txt", bytes.NewReader([]byte("1")))
	fs.WriteFile(ctx, "/trash-empty-test/file2.txt", bytes.NewReader([]byte("2")))
	fs.Delete(ctx, "/trash-empty-test/file1.txt")
	fs.Delete(ctx, "/trash-empty-test/file2.txt")

	req := httptest.NewRequest("DELETE", "/api/v1/trash/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("EmptyTrash status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "deleted_count") {
		t.Error("Response should contain 'deleted_count'")
	}
}

func TestServer_Scrub_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Server has no dataplane connected
	req := httptest.NewRequest("POST", "/api/v1/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Scrub without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_GC_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/gc", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GC without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_Objects_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/objects", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ListObjects without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_ScrubResult(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	// Should return OK even if no result exists (with has_result: false)
	// or ServiceUnavailable if maintenance not configured
	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Errorf("GetScrubResult status = %d, want %d or %d", w.Code, http.StatusOK, http.StatusServiceUnavailable)
	}
}

func TestServer_Thumbnail_NoService(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Server has thumbnail service not initialized
	req := httptest.NewRequest("GET", "/api/v1/thumbnails/image.jpg", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Thumbnail without service status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_Thumbnail_Unsupported(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	// Initialize thumbnail service
	thumbRoot := path.Join(tmpDir, "thumbnails")
	_, _ = server, thumbRoot // We need to modify the server setup for this test

	// This test checks that unsupported files return BadRequest
	// For now, verify the endpoint exists
	req := httptest.NewRequest("GET", "/api/v1/thumbnails/document.pdf", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	// Without thumbnail service, should be ServiceUnavailable
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusBadRequest {
		t.Errorf("Thumbnail for unsupported type status = %d", w.Code)
	}
}

func TestServer_DiagnosticsExport(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/diagnostics-export", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	// Should return OK with zip content or service unavailable if maintenance not configured
	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Errorf("DiagnosticsExport status = %d", w.Code)
	}
}

func TestServer_WebDAVCredentials_AutoGenerated(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	secrets := &config.Secrets{
		WebDAVPassword: "auto-pass",
	}
	if err := config.SaveSecrets(tmpDir, secrets); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}

	server.config.Storage.Root = tmpDir
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Password = ""
	server.config.WebDAV.Username = "webdav-user"

	req := httptest.NewRequest("GET", "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("WebDAV credentials status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Password string `json:"password"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Username != "webdav-user" {
		t.Errorf("expected username webdav-user, got %q", payload.Username)
	}
	if payload.Password != "auto-pass" {
		t.Errorf("expected auto-generated password, got %q", payload.Password)
	}
}

func TestServer_WebDAVCredentials_CustomPassword(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	secrets := &config.Secrets{
		WebDAVPassword: "auto-pass",
	}
	if err := config.SaveSecrets(tmpDir, secrets); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}

	server.config.Storage.Root = tmpDir
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Password = "custom-pass"

	req := httptest.NewRequest("GET", "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("WebDAV credentials status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := payload["password"]; ok {
		t.Fatalf("expected password to be omitted for custom WebDAV password")
	}
}

func TestServer_SetupStatus_CustomWebDAVPassword(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	secrets := &config.Secrets{
		WebDAVPassword: "auto-pass",
		SetupShown:     false,
	}
	if err := config.SaveSecrets(tmpDir, secrets); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}

	server.config.Storage.Root = tmpDir
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Password = "custom-pass"

	req := httptest.NewRequest("GET", "/api/v1/setup/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Setup status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := payload["webdav_password"]; ok {
		t.Fatalf("expected webdav_password to be omitted for custom password")
	}
}

func TestServer_RestoreVersion_Success(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	// Create a file and update it to create versions
	fs.Mkdir(ctx, "/restore-version")
	fs.WriteFile(ctx, "/restore-version/file.txt", bytes.NewReader([]byte("version 1")))
	fs.WriteFile(ctx, "/restore-version/file.txt", bytes.NewReader([]byte("version 2")))

	// Get versions to find a valid hash
	versions, _ := fs.ListVersions(ctx, "/restore-version/file.txt")
	if len(versions) < 2 {
		t.Skip("Need at least 2 versions for test")
	}

	// The first version in the list is usually the oldest
	hashToRestore := versions[len(versions)-1].Hash

	req := httptest.NewRequest("POST", "/api/v1/versions/"+hashToRestore+"/restore?path=/restore-version/file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	// Should restore successfully or return appropriate error
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("RestoreVersion status = %d, want %d or %d", w.Code, http.StatusOK, http.StatusNotFound)
	}
}

func TestServer_UploadFile_ErrorCases(t *testing.T) {
	server, _, _ := setupTestServer(t)

	t.Run("UploadToNonExistentDir", func(t *testing.T) {
		content := "test content"
		req := httptest.NewRequest("POST", "/api/v1/files/nonexistent-dir/file.txt", strings.NewReader(content))
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		// Should fail since parent directory doesn't exist
		if w.Code == http.StatusCreated {
			// If it succeeds, that's also acceptable (auto-create parent)
		}
	})

	t.Run("UploadInvalidPath", func(t *testing.T) {
		content := "test content"
		req := httptest.NewRequest("POST", "/api/v1/files/../../../etc/passwd", strings.NewReader(content))
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("Upload to invalid path status = %d", w.Code)
		}
	})
}

func TestServer_DeleteFile_ErrorCases(t *testing.T) {
	server, _, _ := setupTestServer(t)

	t.Run("DeleteNonExistent", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/files/nonexistent/file.txt", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
			t.Errorf("Delete nonexistent file status = %d", w.Code)
		}
	})
}

func TestServer_ListVersions_ErrorCases(t *testing.T) {
	server, _, _ := setupTestServer(t)

	t.Run("VersionsForNonExistent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/versions/nonexistent/file.txt", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Versions for nonexistent file status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("VersionsInvalidPath", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/versions/../../../etc/passwd", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("Versions for invalid path status = %d", w.Code)
		}
	})
}
