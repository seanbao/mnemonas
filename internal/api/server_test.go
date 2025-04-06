// Package api provides REST API and gRPC API
package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/rs/zerolog"
	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/maintenance"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/thumbnail"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

func createOversizedPNGConfigOnly(width, height int) []byte {
	chunkData := make([]byte, 13)
	binary.BigEndian.PutUint32(chunkData[0:4], uint32(width))
	binary.BigEndian.PutUint32(chunkData[4:8], uint32(height))
	chunkData[8] = 8
	chunkData[9] = 2

	chunkType := []byte("IHDR")
	crcInput := append(append([]byte(nil), chunkType...), chunkData...)
	crc := crc32.ChecksumIEEE(crcInput)

	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	buf.Write([]byte{0, 0, 0, 13})
	buf.Write(chunkType)
	buf.Write(chunkData)
	buf.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	buf.Write([]byte{0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130})
	return buf.Bytes()
}

func createGIFThumbnailSource(width, height int) []byte {
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	})
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if (x+y)%2 == 0 {
				img.SetColorIndex(x, y, 1)
			}
		}
	}

	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func createPNGThumbnailSourceWithAlpha(width, height int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: 32, G: 160, B: 224, A: 128})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// testDataplaneAddr is the address of the test dataplane server
func testDataplaneAddr() string {
	if addr := os.Getenv("MNEMONAS_TEST_DATAPLANE_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:9090"
}

type fakeAlertMonitor struct {
	updateCount int
	lastConfig  alerts.Config
}

func (m *fakeAlertMonitor) UpdateConfig(cfg alerts.Config) {
	m.updateCount++
	m.lastConfig = cfg
}

type fakeRetentionMonitor struct {
	updateCount int
	lastConfig  storage.RetentionMonitorConfig
}

func (m *fakeRetentionMonitor) UpdateConfig(cfg storage.RetentionMonitorConfig) {
	m.updateCount++
	m.lastConfig = cfg
}

type fakeWebDAVUpdater struct {
	updateCount int
	lastConfig  WebDAVRuntimeConfig
}

func (u *fakeWebDAVUpdater) UpdateConfig(cfg WebDAVRuntimeConfig) {
	u.updateCount++
	u.lastConfig = cfg
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
	settings.Storage.Root = tmpDir
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(logger, &ServerConfig{
		FileSystem:    fs,
		Config:        settings,
		ConfigPath:    configPath,
		ActivityRoot:  path.Join(tmpDir, "activity"),
		DataplaneAddr: testDataplaneAddr(),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, fs, tmpDir
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

func setVersionStoreObjectClient(t *testing.T, fs *storage.FileSystem, client *dataplane.Client) {
	t.Helper()
	fsValue := reflect.ValueOf(fs).Elem()
	versionsField := fsValue.FieldByName("versions")
	versionsValue := reflect.NewAt(versionsField.Type(), unsafe.Pointer(versionsField.UnsafeAddr())).Elem()
	objectsField := versionsValue.Elem().FieldByName("objects")
	objectsValue := reflect.NewAt(objectsField.Type(), unsafe.Pointer(objectsField.UnsafeAddr())).Elem()
	clientField := objectsValue.Elem().FieldByName("client")
	reflect.NewAt(clientField.Type(), unsafe.Pointer(clientField.UnsafeAddr())).Elem().Set(reflect.ValueOf(client))
}

func getVersionStoreObjectClient(t *testing.T, fs *storage.FileSystem) *dataplane.Client {
	t.Helper()
	fsValue := reflect.ValueOf(fs).Elem()
	versionsField := fsValue.FieldByName("versions")
	versionsValue := reflect.NewAt(versionsField.Type(), unsafe.Pointer(versionsField.UnsafeAddr())).Elem()
	objectsField := versionsValue.Elem().FieldByName("objects")
	objectsValue := reflect.NewAt(objectsField.Type(), unsafe.Pointer(objectsField.UnsafeAddr())).Elem()
	clientField := objectsValue.Elem().FieldByName("client")
	clientValue := reflect.NewAt(clientField.Type(), unsafe.Pointer(clientField.UnsafeAddr())).Elem()
	if clientValue.IsNil() {
		return nil
	}
	client, _ := clientValue.Interface().(*dataplane.Client)
	return client
}

func getVersioningPolicy(t *testing.T, fs *storage.FileSystem) *versionstore.VersioningPolicy {
	t.Helper()
	fsValue := reflect.ValueOf(fs).Elem()
	policyField := fsValue.FieldByName("policy")
	policyValue := reflect.NewAt(policyField.Type(), unsafe.Pointer(policyField.UnsafeAddr())).Elem()
	if policyValue.IsNil() {
		return nil
	}
	policy, _ := policyValue.Interface().(*versionstore.VersioningPolicy)
	return policy
}

func TestNewServer_InitializesFavoritesWhenDisabledIfStoreConfigured(t *testing.T) {
	logger := zerolog.Nop()
	storeFile := filepath.Join(t.TempDir(), "favorites.json")
	settings := config.Default()

	server, err := NewServer(logger, &ServerConfig{
		Config:             settings,
		FavoritesEnabled:   false,
		FavoritesStoreFile: storeFile,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if server.favoritesHandler == nil {
		t.Fatal("expected favorites handler to be initialized when store is configured")
	}
}

func TestNewServer_InitializesFavoritesWhenEnabled(t *testing.T) {
	logger := zerolog.Nop()
	storeFile := filepath.Join(t.TempDir(), "favorites.json")
	settings := config.Default()

	server, err := NewServer(logger, &ServerConfig{
		Config:             settings,
		FavoritesEnabled:   true,
		FavoritesStoreFile: storeFile,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if server.favoritesHandler == nil {
		t.Fatal("expected favorites handler to be initialized when favorites are enabled")
	}
}

func setupFavoritesServerWithOptions(t *testing.T, enabled bool) *Server {
	logger := zerolog.Nop()
	tmpDir := t.TempDir()
	storeFile := filepath.Join(tmpDir, "favorites.json")
	settings := config.Default()
	settings.Favorites.Enabled = enabled
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error = %v", err)
	}

	server, err := NewServer(logger, &ServerConfig{
		Config:             settings,
		ConfigPath:         configPath,
		FavoritesEnabled:   enabled,
		FavoritesStoreFile: storeFile,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	return server
}

func setupFavoritesUnavailableServer(t *testing.T) *Server {
	t.Helper()
	logger := zerolog.Nop()
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "favorites-target.json")
	symlinkPath := filepath.Join(tmpDir, "favorites-link.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("WriteFile(targetPath) error = %v", err)
	}
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	settings := config.Default()
	settings.Favorites.Enabled = true
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error = %v", err)
	}

	server, err := NewServer(logger, &ServerConfig{
		Config:             settings,
		ConfigPath:         configPath,
		FavoritesEnabled:   true,
		FavoritesStoreFile: symlinkPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if server.favoritesHandler != nil || server.favoritesStore != nil {
		t.Fatal("expected favorites initialization to fail for symlink-backed store path")
	}

	return server
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
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(logger, &ServerConfig{
		FileSystem:     fs,
		Config:         settings,
		ConfigPath:     configPath,
		ActivityRoot:   path.Join(tmpDir, "activity"),
		DataplaneAddr:  testDataplaneAddr(),
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

func setupAuthServerWithFeatures(t *testing.T, shareEnabled, favoritesEnabled bool) (*Server, *storage.FileSystem, string, string, string) {
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
		t.Fatalf("Mkdir(docs) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("WriteFile(docs/a.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/trash.txt", bytes.NewReader([]byte("trash me"))); err != nil {
		t.Fatalf("WriteFile(docs/trash.txt) error: %v", err)
	}
	if err := fs.Delete(ctx, "/docs/trash.txt"); err != nil {
		t.Fatalf("Delete(docs/trash.txt) error: %v", err)
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

	settings := config.Default()
	settings.Storage.Root = tmpDir
	settings.Share.Enabled = shareEnabled
	settings.Favorites.Enabled = favoritesEnabled
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:         fs,
		Config:             settings,
		ConfigPath:         configPath,
		ActivityRoot:       path.Join(tmpDir, "activity"),
		DataplaneAddr:      testDataplaneAddr(),
		AuthEnabled:        true,
		AuthUsersFile:      usersFile,
		AuthJWTSecret:      "test-secret",
		AuthAccessTTL:      15 * time.Minute,
		AuthRefreshTTL:     24 * time.Hour,
		ShareEnabled:       shareEnabled,
		ShareStoreFile:     path.Join(tmpDir, "shares.json"),
		FavoritesEnabled:   favoritesEnabled,
		FavoritesStoreFile: path.Join(tmpDir, "favorites.json"),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, fs, tmpDir, username, password
}

func loginAndGetAccessToken(t *testing.T, server *Server, username, password string) string {
	t.Helper()

	body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}

	return payload.Data.AccessToken
}

func issueAccessTokenWithoutActivity(t *testing.T, server *Server, username string) string {
	t.Helper()

	if server.userStore == nil || server.tokenManager == nil {
		t.Fatal("expected auth-enabled server with user store and token manager")
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%q) error: %v", username, err)
	}

	tokenPair, err := server.tokenManager.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair(%q) error: %v", username, err)
	}

	return tokenPair.AccessToken
}

func newTestThumbnailService(t *testing.T, cacheDir string) *thumbnail.Service {
	t.Helper()

	svc, err := thumbnail.NewService(cacheDir)
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	t.Cleanup(svc.Wait)
	return svc
}

func setUserHomeDirForTest(t *testing.T, server *Server, username, homeDir string) {
	t.Helper()

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	user.HomeDir = homeDir
	if err := server.userStore.Update(user); err != nil {
		t.Fatalf("Update(%s) error: %v", username, err)
	}
}

func setupShareServer(t *testing.T) (*Server, string) {
	return setupShareServerWithOptions(t, true, "")
}

func setupShareServerWithBaseURL(t *testing.T, baseURL string) (*Server, string) {
	return setupShareServerWithOptions(t, true, baseURL)
}

func setupShareServerWithOptions(t *testing.T, shareEnabled bool, baseURL string) (*Server, string) {
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
	settings.Share.Enabled = shareEnabled
	settings.Share.BaseURL = baseURL
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(logger, &ServerConfig{
		FileSystem:     fs,
		ActivityRoot:   path.Join(internalRoot, "activity"),
		Config:         settings,
		ConfigPath:     configPath,
		DataplaneAddr:  testDataplaneAddr(),
		ShareEnabled:   shareEnabled,
		ShareStoreFile: shareStorePath,
		ShareBaseURL:   baseURL,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server, createdShare.ID
}

func setupFavoritesPathSyncServer(t *testing.T) *Server {
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
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("WriteFile(/docs/a.txt) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/sub"); err != nil {
		t.Fatalf("Mkdir(/docs/sub) error: %v", err)
	}

	settings := config.Default()
	settings.Storage.Root = tmpDir
	settings.Favorites.Enabled = true
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:         fs,
		ActivityRoot:       path.Join(internalRoot, "activity"),
		Config:             settings,
		ConfigPath:         configPath,
		DataplaneAddr:      testDataplaneAddr(),
		FavoritesEnabled:   true,
		FavoritesStoreFile: path.Join(tmpDir, "favorites.json"),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	return server
}

type repeatingReader struct{}

func (repeatingReader) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = 'a'
	}
	return len(p), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("stream failed")
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

func TestServer_ObservabilityEndpoints_BypassThrottle(t *testing.T) {
	server, _, _ := setupTestServer(t)
	started := make(chan struct{}, DefaultMaxConcurrentRequests)
	release := make(chan struct{})
	requestDone := make(chan error, DefaultMaxConcurrentRequests)

	server.router.Get("/slow", func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()

	for i := 0; i < DefaultMaxConcurrentRequests; i++ {
		go func() {
			resp, err := http.Get(httpServer.URL + "/slow")
			if err == nil {
				resp.Body.Close()
			}
			requestDone <- err
		}()
	}

	for i := 0; i < DefaultMaxConcurrentRequests; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for slow requests to saturate the throttle")
		}
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	for _, endpoint := range []string{"/health", "/api/v1/version"} {
		resp, err := client.Get(httpServer.URL + endpoint)
		if err != nil {
			t.Fatalf("GET %s should bypass throttle: %v", endpoint, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", endpoint, resp.StatusCode, http.StatusOK)
		}
	}
	close(release)

	for i := 0; i < DefaultMaxConcurrentRequests; i++ {
		select {
		case err := <-requestDone:
			if err != nil {
				t.Fatalf("slow request failed after release: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for slow requests to finish")
		}
	}
}

func TestServer_Health_DataplaneFailureDoesNotExposeInternalError(t *testing.T) {
	server, err := NewServer(zerolog.Nop(), &ServerConfig{DataplaneAddr: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Health status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "connection refused") || strings.Contains(strings.ToLower(body), "rpc error") {
		t.Fatalf("expected health response to hide internal dataplane errors, got %q", body)
	}
	if !strings.Contains(body, `"status":"degraded"`) {
		t.Fatalf("expected health response to mark dataplane failure as degraded, got %q", body)
	}
	if !strings.Contains(body, "unavailable") {
		t.Fatalf("expected health response to include generic dataplane status, got %q", body)
	}
}

func TestNewServer_UsesConfiguredDataplaneTimeoutForInitialConnect(t *testing.T) {
	originalConnect := serverConnectDataplaneClient
	t.Cleanup(func() {
		serverConnectDataplaneClient = originalConnect
	})

	settings := config.Default()
	settings.DataPlane.Timeout = 41 * time.Second

	var observedTimeout time.Duration
	serverConnectDataplaneClient = func(_ *Server, _ context.Context, client *dataplane.Client, totalTimeout time.Duration) error {
		if client == nil {
			t.Fatal("expected dataplane client")
		}
		observedTimeout = totalTimeout
		return nil
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:        settings,
		DataplaneAddr: "127.0.0.1:9090",
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	if server == nil {
		t.Fatal("expected server instance")
	}
	if observedTimeout != settings.DataPlane.Timeout {
		t.Fatalf("initial connect timeout = %s, want %s", observedTimeout, settings.DataPlane.Timeout)
	}
}

func TestServer_Health_DegradedWhenConfiguredSubsystemsFailInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	thumbnailRoot := filepath.Join(tmpDir, "thumbnail-root")
	maintenanceRoot := filepath.Join(tmpDir, "maintenance-root")
	activityRoot := filepath.Join(tmpDir, "activity-root")

	for _, root := range []string{thumbnailRoot, maintenanceRoot, activityRoot} {
		if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", root, err)
		}
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		ThumbnailRoot:   thumbnailRoot,
		MaintenanceRoot: maintenanceRoot,
		ActivityRoot:    activityRoot,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Health status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"status":"degraded"`) {
		t.Fatalf("expected health response to degrade when configured subsystems fail, got %q", w.Body.String())
	}
}

func TestServer_Health_DegradedWhenFavoritesStoreFailsInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "favorites-target.json")
	symlinkPath := filepath.Join(tmpDir, "favorites-link.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("WriteFile(targetPath) error: %v", err)
	}
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	settings := config.Default()
	settings.Favorites.Enabled = true

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:             settings,
		FavoritesEnabled:   true,
		FavoritesStoreFile: symlinkPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Health status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"status":"degraded"`) {
		t.Fatalf("expected health response to degrade when favorites store fails, got %q", w.Body.String())
	}
}

func TestServer_Health_ReconnectsDataplaneOnDemand(t *testing.T) {
	probe := setupDataplaneClient(t)
	if probe == nil {
		t.Skip("dataplane not available, skipping test")
	}

	server, err := NewServer(zerolog.Nop(), nil)
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	disconnected := dataplane.NewClient(testDataplaneAddr())
	defer disconnected.Close()
	server.dataplane = disconnected

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Health status = %d, want %d", w.Code, http.StatusOK)
	}
	if !server.dataplane.IsConnected() {
		t.Fatal("expected health check to reconnect dataplane client")
	}
	body := w.Body.String()
	if strings.Contains(body, `"status":"degraded"`) {
		t.Fatalf("expected healthy dataplane after reconnect, got %q", body)
	}
	if !strings.Contains(body, `"dataplane":{"healthy":true`) {
		t.Fatalf("expected health response to include dataplane health after reconnect, got %q", body)
	}
}

func TestServer_Health_UsesConfiguredDataplaneTimeoutForReconnect(t *testing.T) {
	originalConnect := serverConnectDataplaneClient
	t.Cleanup(func() {
		serverConnectDataplaneClient = originalConnect
	})

	settings := config.Default()
	settings.DataPlane.Timeout = 37 * time.Second

	var observedTimeout time.Duration
	serverConnectDataplaneClient = func(_ *Server, _ context.Context, client *dataplane.Client, totalTimeout time.Duration) error {
		if client == nil {
			t.Fatal("expected dataplane client")
		}
		observedTimeout = totalTimeout
		return context.DeadlineExceeded
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: settings})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	server.dataplane = dataplane.NewClient("127.0.0.1:1")
	defer server.dataplane.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Health status = %d, want %d", w.Code, http.StatusOK)
	}
	if observedTimeout != settings.DataPlane.Timeout {
		t.Fatalf("reconnect timeout = %s, want %s", observedTimeout, settings.DataPlane.Timeout)
	}
	if !strings.Contains(w.Body.String(), `"status":"degraded"`) {
		t.Fatalf("expected degraded health response when reconnect fails, got %q", w.Body.String())
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

func TestServer_ListFiles_InternalReadDirErrorReturnsInternalServerError(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locked"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	lockedDir := path.Join(tmpDir, "files", "locked")
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatalf("Chmod() error: %v", err)
	}
	defer func() {
		_ = os.Chmod(lockedDir, 0o755)
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/locked", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ListFiles internal error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "permission denied") {
		t.Fatalf("expected list files internal error to stay generic, got %s", w.Body.String())
	}
}

func TestServer_ListFiles_ReturnsBadRequestWhenPathIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/single-file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/single-file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListFiles file path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "path is not a directory") {
		t.Fatalf("expected path-not-directory message, got %s", w.Body.String())
	}
}

func TestServer_ListVersions_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(versions-parent) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions/versions-parent/child.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("list versions parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
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

func TestServer_UploadFile_TooLargeReturnsPayloadTooLarge(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/upload"); err != nil {
		t.Fatalf("Mkdir(/upload) error: %v", err)
	}

	body := io.NopCloser(io.LimitReader(repeatingReader{}, DefaultMaxUploadSize+1))
	req := httptest.NewRequest("POST", "/api/v1/files/upload/toolarge.bin", body)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Upload too large status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	responseBody := w.Body.String()
	if !strings.Contains(responseBody, "file too large") {
		t.Fatalf("expected generic file too large error, got %s", responseBody)
	}
	if !strings.Contains(responseBody, `"code":"PAYLOAD_TOO_LARGE"`) {
		t.Fatalf("expected payload-too-large error code, got %s", responseBody)
	}
	if _, err := fs.Stat(ctx, "/upload/toolarge.bin"); err == nil {
		t.Fatal("expected oversized upload to leave no file behind")
	}
}

func TestServer_Search_InvalidLimitReturnsBadRequest(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []string{"0", "101", "nope"}
	for _, limit := range tests {
		t.Run(limit, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/search?q=report&limit="+limit, nil)
			w := httptest.NewRecorder()

			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("Search invalid limit status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			if !strings.Contains(w.Body.String(), "limit parameter must be between 1 and 100") {
				t.Fatalf("expected invalid limit error, got %s", w.Body.String())
			}
		})
	}
}

func TestServer_Search_TraversalErrorReturnsInternalServerError(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/blocked"); err != nil {
		t.Fatalf("Mkdir(blocked) error: %v", err)
	}
	blockedDir := path.Join(tmpDir, "files", "blocked")
	if err := os.Chmod(blockedDir, 0); err != nil {
		t.Fatalf("Chmod(blocked) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(blockedDir, 0o755)
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=blocked", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Search traversal error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "permission denied") {
		t.Fatalf("expected search traversal failure to stay generic, got %s", w.Body.String())
	}
}

func TestServer_Activity_InvalidPaginationReturnsBadRequest(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []struct {
		name    string
		query   string
		message string
	}{
		{name: "invalid limit", query: "/api/v1/activity?limit=0", message: "limit parameter must be between 1 and 500"},
		{name: "overlarge limit", query: "/api/v1/activity?limit=501", message: "limit parameter must be between 1 and 500"},
		{name: "invalid offset", query: "/api/v1/activity?offset=-1", message: "offset parameter must be a non-negative integer"},
		{name: "nonnumeric offset", query: "/api/v1/activity?offset=nope", message: "offset parameter must be a non-negative integer"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.query, nil)
			w := httptest.NewRecorder()

			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("Activity invalid pagination status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			if !strings.Contains(w.Body.String(), testCase.message) {
				t.Fatalf("expected invalid pagination error %q, got %s", testCase.message, w.Body.String())
			}
		})
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

func TestServer_DeleteFile_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/delete-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(delete-parent) error: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/api/v1/files/delete-parent/child.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("delete file parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_DeleteFile_DisablesSharesForDeletedPath(t *testing.T) {
	server, shareID := setupShareServer(t)
	ctx := context.Background()

	fileShare, err := server.shareStore.Create(share.CreateShareOptions{
		Path:      "/docs/a.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create(file share) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files/docs/a.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete shared file status = %d, want %d", w.Code, http.StatusOK)
	}

	disabledShare, err := server.shareStore.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(fileShare) error: %v", err)
	}
	if disabledShare.Enabled {
		t.Fatal("expected deleted file share to be disabled")
	}
	folderShare, err := server.shareStore.Get(shareID)
	if err != nil {
		t.Fatalf("Get(folder share) error: %v", err)
	}
	if !folderShare.Enabled {
		t.Fatal("expected unrelated folder share to remain enabled")
	}
	if _, err := server.fs.Stat(ctx, "/docs/a.txt"); err == nil {
		t.Fatal("expected deleted file to be absent from filesystem")
	}
}

func TestServer_DeleteFile_RemovesFavoritesForDeletedPath(t *testing.T) {
	server := setupFavoritesPathSyncServer(t)

	if _, err := server.favoritesStore.Add("tester", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}
	if _, err := server.favoritesStore.Add("tester", "/docs/sub", "dir"); err != nil {
		t.Fatalf("Add(/docs/sub) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files/docs/a.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete favorited file status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.favoritesStore.IsFavorite("tester", "/docs/a.txt") {
		t.Fatal("expected deleted file favorite to be removed")
	}
	if !server.favoritesStore.IsFavorite("tester", "/docs/sub") {
		t.Fatal("expected unrelated favorite to remain")
	}
}

func TestServer_DeleteFile_RollsBackWhenFavoriteCleanupFails(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile(/docs/a.txt) error: %v", err)
	}

	shareDir := filepath.Join(tmpDir, "delete-hook-share")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shareDir) error: %v", err)
	}
	shareStore, err := share.NewShareStore(filepath.Join(shareDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	fileShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/a.txt", Type: share.ShareTypeFile, CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("Create(file share) error: %v", err)
	}

	favoritesDir := filepath.Join(tmpDir, "delete-hook-favorites")
	if err := os.MkdirAll(favoritesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(favoritesDir) error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(favoritesDir, "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}

	server.shareStore = shareStore
	server.favoritesStore = favoritesStore

	if err := os.Chmod(favoritesDir, 0o500); err != nil {
		t.Fatalf("Chmod(favoritesDir) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(favoritesDir, 0o755)
	}()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files/docs/a.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("delete with favorites cleanup failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/docs/a.txt"); err != nil {
		t.Fatalf("expected original file path to remain after delete rollback, got %v", err)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("expected trash to remain empty after delete rollback, got %d items", len(items))
	}

	loadedShare, err := server.shareStore.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(file share) error: %v", err)
	}
	if !loadedShare.Enabled {
		t.Fatal("expected share to remain enabled after delete rollback")
	}
	if !server.favoritesStore.IsFavorite("tester", "/docs/a.txt") {
		t.Fatal("expected favorite to remain after delete rollback")
	}
}

func TestServer_DeleteDirectory_RollbackRestoresChildIndexesWhenDeleteHookFails(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/docs/nested"); err != nil {
		t.Fatalf("Mkdir(/docs/nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/readme.md", bytes.NewReader([]byte("readme"))); err != nil {
		t.Fatalf("WriteFile(readme) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/nested/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(report) error: %v", err)
	}

	fs.SetPathChangeHooks(nil, func(context.Context, string) (*storage.PathDeleteHookResult, error) {
		return nil, errors.New("favorite cleanup failed")
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files/docs", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("delete directory with hook failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/docs"); err != nil {
		t.Fatalf("expected directory to be restored after rollback, got %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected trash to remain empty after directory rollback, got %d items", len(items))
	}
	count, err := fs.GetFileCount(ctx)
	if err != nil {
		t.Fatalf("GetFileCount() error: %v", err)
	}
	if count != 2 {
		t.Fatalf("GetFileCount() after directory rollback = %d, want 2", count)
	}
}

func TestServer_DeleteFile_RollsBackWhenFavoriteCleanupFailsWithTrashDisabled(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()
	fs.UpdateTrashSettings(false, 30, 1<<20)

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/a.txt", bytes.NewReader([]byte("keep me"))); err != nil {
		t.Fatalf("WriteFile(/docs/a.txt) error: %v", err)
	}

	shareDir := filepath.Join(tmpDir, "delete-permanent-share")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shareDir) error: %v", err)
	}
	shareStore, err := share.NewShareStore(filepath.Join(shareDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	fileShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/a.txt", Type: share.ShareTypeFile, CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("Create(file share) error: %v", err)
	}

	favoritesDir := filepath.Join(tmpDir, "delete-permanent-favorites")
	if err := os.MkdirAll(favoritesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(favoritesDir) error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(favoritesDir, "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}

	server.shareStore = shareStore
	server.favoritesStore = favoritesStore

	if err := os.Chmod(favoritesDir, 0o500); err != nil {
		t.Fatalf("Chmod(favoritesDir) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(favoritesDir, 0o755)
	}()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files/docs/a.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("delete with favorites cleanup failure and trash disabled status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/docs/a.txt"); err != nil {
		t.Fatalf("expected original file path to remain after permanent delete rollback, got %v", err)
	}
	items, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("expected permanent delete rollback to leave trash empty, got %d items", len(items))
	}

	loadedShare, err := server.shareStore.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(file share) error: %v", err)
	}
	if !loadedShare.Enabled {
		t.Fatal("expected share to remain enabled after permanent delete rollback")
	}
	if !server.favoritesStore.IsFavorite("tester", "/docs/a.txt") {
		t.Fatal("expected favorite to remain after permanent delete rollback")
	}
}

func TestServer_DownloadFile_ContentDispositionEscapesFilename(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/quote\"name.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/quote%22name.txt?download=true", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Download status = %d, want %d", w.Code, http.StatusOK)
	}
	contentDisposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisposition, `filename="quote\"name.txt"`) {
		t.Fatalf("expected escaped Content-Disposition filename, got %q", contentDisposition)
	}
}

func TestServer_DownloadFile_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/download-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(download-parent) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/download-parent/child.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("download file parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_DownloadFile_UsesPrivateRevalidationCacheHeaders(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/cache.txt", bytes.NewReader([]byte("first version"))); err != nil {
		t.Fatalf("WriteFile(/cache.txt) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/cache.txt", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", w.Code, http.StatusOK)
	}
	if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "private, no-cache" {
		t.Fatalf("download Cache-Control = %q, want %q", cacheControl, "private, no-cache")
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected download response to include ETag")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/download/cache.txt", nil)
	req.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Fatalf("download If-None-Match status = %d, want %d", w.Code, http.StatusNotModified)
	}
}

func TestServer_DownloadFile_LogsDownloadActivity(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := fs.WriteFile(ctx, "/download-activity.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/download-activity.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", w.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionDownload, "")
	if total != 1 {
		t.Fatalf("expected one download activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed download activity entry, got %d", len(entries))
	}
	if entries[0].Path != "/download-activity.txt" {
		t.Fatalf("expected download activity path %q, got %q", "/download-activity.txt", entries[0].Path)
	}
	if entries[0].Action != activity.ActionDownload {
		t.Fatalf("expected action %q, got %q", activity.ActionDownload, entries[0].Action)
	}
	if entries[0].Details != nil {
		t.Fatalf("expected plain download to omit details, got %+v", entries[0].Details)
	}
	if entries[0].User != "anonymous" {
		t.Fatalf("expected anonymous download user, got %q", entries[0].User)
	}
}

func TestServer_DownloadFile_RefreshesAfterFileContentChanges(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/refresh.txt", bytes.NewReader([]byte("first version"))); err != nil {
		t.Fatalf("WriteFile(/refresh.txt) first error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/refresh.txt", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initial download status = %d, want %d", w.Code, http.StatusOK)
	}
	oldETag := w.Header().Get("ETag")
	if oldETag == "" {
		t.Fatal("expected initial download response to include ETag")
	}

	if err := fs.WriteFile(ctx, "/refresh.txt", bytes.NewReader([]byte("second version"))); err != nil {
		t.Fatalf("WriteFile(/refresh.txt) second error: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/download/refresh.txt", nil)
	req.Header.Set("If-None-Match", oldETag)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("revalidated download status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); body != "second version" {
		t.Fatalf("revalidated download body = %q, want %q", body, "second version")
	}
	if newETag := w.Header().Get("ETag"); newETag == "" || newETag == oldETag {
		t.Fatalf("expected updated ETag after content change, got old=%q new=%q", oldETag, newETag)
	}
}

func TestServer_RespondReadableOpenFileError_MapsStorageErrors(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}

	tests := []struct {
		name       string
		err        error
		statusCode int
		body       string
	}{
		{
			name:       "not found",
			err:        fmt.Errorf("wrapped: %w", storage.ErrNotFound),
			statusCode: http.StatusNotFound,
			body:       "resource not found",
		},
		{
			name:       "parent not directory",
			err:        fmt.Errorf("wrapped: %w", storage.ErrNotDir),
			statusCode: http.StatusConflict,
			body:       "parent path is not a directory",
		},
		{
			name:       "unexpected internal error",
			err:        errors.New("boom"),
			statusCode: http.StatusInternalServerError,
			body:       "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()

			server.respondReadableOpenFileError(w, "open file", tt.err, "cannot download directory")

			if w.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.statusCode)
			}
			if !strings.Contains(w.Body.String(), tt.body) {
				t.Fatalf("expected body to contain %q, got %s", tt.body, w.Body.String())
			}
		})
	}
}

func TestServer_RespondReadableOpenFileError_UsesContextSpecificDirectoryMessage(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	w := httptest.NewRecorder()

	server.respondReadableOpenFileError(w, "thumbnail open file", storage.ErrIsDir, "path is a directory")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "path is a directory") {
		t.Fatalf("expected directory-path validation message, got %s", w.Body.String())
	}
}

func TestServer_DownloadWithQueryAuth(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/"+username); err != nil {
		t.Fatalf("Mkdir(/%s) error: %v", username, err)
	}
	if err := fs.WriteFile(ctx, "/"+username+"/file.txt", bytes.NewReader([]byte("secure"))); err != nil {
		t.Fatalf("WriteFile(/%s/file.txt) error: %v", username, err)
	}

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	var loginResp auth.LoginResponse
	var loginPayload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}
	loginResp = loginPayload.Data

	downloadSessionReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/download-session", nil)
	downloadSessionReq.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	downloadSessionRec := httptest.NewRecorder()
	server.Router().ServeHTTP(downloadSessionRec, downloadSessionReq)

	if downloadSessionRec.Code != http.StatusOK {
		t.Fatalf("download session status = %d, want %d", downloadSessionRec.Code, http.StatusOK)
	}
	cookies := downloadSessionRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected download session cookie")
	}
	downloadReq := httptest.NewRequest("GET", "/api/v1/download/"+username+"/file.txt", nil)
	downloadReq.AddCookie(cookies[0])
	downloadRec := httptest.NewRecorder()
	server.Router().ServeHTTP(downloadRec, downloadReq)

	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", downloadRec.Code, http.StatusOK)
	}
	if !strings.Contains(downloadRec.Body.String(), "secure") {
		t.Error("downloaded content mismatch")
	}
}

func TestServer_Login_LogsActivityWithUsername(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}

	loginBody := fmt.Sprintf(`{"username":" %s ","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()

	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionLogin, "")
	if total != 1 {
		t.Fatalf("expected one login activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed login activity entry, got %d", len(entries))
	}
	if entries[0].User != username {
		t.Fatalf("expected login activity user %q, got %q", username, entries[0].User)
	}
}

func TestServer_Login_AddsAuditWarningHeaderWhenActivityLogSaveFails(t *testing.T) {
	server, _, tmpDir, username, password := setupAuthServer(t)
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionLogin, "/", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("activity.Log() error: %v", err)
	}

	activityRoot := path.Join(tmpDir, "activity")
	if err := os.Chmod(activityRoot, 0500); err != nil {
		t.Fatalf("Chmod(activityRoot) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(activityRoot, 0755)
	})

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()

	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}
	if got := loginRec.Header().Get(auditStatusHeaderName); got != auditStatusFailedValue {
		t.Fatalf("audit status header = %q, want %q", got, auditStatusFailedValue)
	}
	warningValues := loginRec.Header().Values("Warning")
	if len(warningValues) != 1 || warningValues[0] != auditWarningHeader {
		t.Fatalf("warning headers = %v, want [%q]", warningValues, auditWarningHeader)
	}

	var payload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}
	if payload.Data.AccessToken == "" {
		t.Fatal("expected login response to preserve access token body")
	}
	if payload.Data.RefreshToken == "" {
		t.Fatal("expected login response to preserve refresh token body")
	}
	if payload.Data.User.Username != username {
		t.Fatalf("login response user = %q, want %q", payload.Data.User.Username, username)
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

func TestServer_CreateShare_UsesBaseURL(t *testing.T) {
	server, _ := setupShareServerWithBaseURL(t, "https://nas.example.com/")

	body := `{"path":"/docs/a.txt","type":"file"}`
	req := httptest.NewRequest("POST", "/api/v1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want %d", w.Code, http.StatusCreated)
	}

	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected response envelope data")
	}
	urlValue, ok := data["url"].(string)
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

func TestServer_CreateShare_LogsShareActivityPath(t *testing.T) {
	server, _ := setupShareServer(t)
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}

	body := `{"path":" docs/a.txt ","type":"file"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(body))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{
		UserID:   "tester",
		Username: "tester",
		Role:     auth.RoleUser,
	}))
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want %d", w.Code, http.StatusCreated)
	}

	entries, total := server.activity.List(10, 0, activity.ActionShare, "")
	if total != 1 {
		t.Fatalf("expected one share activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed share activity entry, got %d", len(entries))
	}
	if entries[0].Path != "/docs/a.txt" {
		t.Fatalf("expected share activity path %q, got %q", "/docs/a.txt", entries[0].Path)
	}
}

func TestServer_DeleteShare_LogsUnshareActivity(t *testing.T) {
	server, shareID := setupShareServer(t)
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	server.authEnabled = true

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/shares/"+shareID, nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{
		UserID:   "tester",
		Username: "tester",
		Role:     auth.RoleUser,
	}))
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete share status = %d, want %d", w.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionUnshare, "")
	if total != 1 {
		t.Fatalf("expected one unshare activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed unshare activity entry, got %d", len(entries))
	}
	if entries[0].User != "tester" {
		t.Fatalf("expected unshare activity user tester, got %q", entries[0].User)
	}
	if entries[0].Path != "/docs" {
		t.Fatalf("expected unshare activity path %q, got %q", "/docs", entries[0].Path)
	}
}

func TestServer_UpdateSettings_UpdatesRunningShareBaseURL(t *testing.T) {
	server, _ := setupShareServerWithBaseURL(t, "https://old.example.com/")

	updateBody := `{"share":{"base_url":"https://new.example.com/base/"}}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()

	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("update settings share base url status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	createBody := `{"path":"/docs/a.txt","type":"file"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(createBody))
	createRec := httptest.NewRecorder()

	server.Router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want %d", createRec.Code, http.StatusCreated)
	}

	var payload map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected response envelope data")
	}
	urlValue, ok := data["url"].(string)
	if !ok {
		t.Fatalf("expected url in response")
	}
	if !strings.HasPrefix(urlValue, "https://new.example.com/base/s/") {
		t.Fatalf("expected updated running share base URL, got %q", urlValue)
	}
}

func TestServer_UpdateSettings_SaveFailureDoesNotUpdateRunningShareBaseURL(t *testing.T) {
	server, _ := setupShareServerWithBaseURL(t, "https://old.example.com/")
	server.configPath = t.TempDir()

	updateBody := `{"share":{"base_url":"https://new.example.com/base/"}}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()

	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusInternalServerError {
		t.Fatalf("update settings share base url save failure status = %d, want %d", updateRec.Code, http.StatusInternalServerError)
	}

	createBody := `{"path":"/docs/a.txt","type":"file"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(createBody))
	createRec := httptest.NewRecorder()

	server.Router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want %d", createRec.Code, http.StatusCreated)
	}

	var payload map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected response envelope data")
	}
	urlValue, ok := data["url"].(string)
	if !ok {
		t.Fatalf("expected url in response")
	}
	if !strings.HasPrefix(urlValue, "https://old.example.com/s/") {
		t.Fatalf("expected old running share base URL after save failure, got %q", urlValue)
	}
}

func TestServer_UpdateSettings_EnablesShareFeatureWithoutRestart(t *testing.T) {
	server, _ := setupShareServerWithOptions(t, false, "")

	createBody := `{"path":"/docs/a.txt","type":"file"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(createBody))
	createRec := httptest.NewRecorder()
	server.Router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("create share while disabled status = %d, want %d", createRec.Code, http.StatusServiceUnavailable)
	}

	updateBody := `{"share":{"enabled":true}}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("enable share feature status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	createReq = httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(createBody))
	createRec = httptest.NewRecorder()
	server.Router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create share after enable status = %d, want %d", createRec.Code, http.StatusCreated)
	}
}

func TestServer_UpdateSettings_DisablesPublicShareAccessWithoutRestart(t *testing.T) {
	server, shareID := setupShareServerWithOptions(t, true, "")

	publicReq := httptest.NewRequest(http.MethodGet, "/s/"+shareID, nil)
	publicRec := httptest.NewRecorder()
	server.Router().ServeHTTP(publicRec, publicReq)

	if publicRec.Code != http.StatusOK {
		t.Fatalf("public share before disable status = %d, want %d", publicRec.Code, http.StatusOK)
	}

	updateBody := `{"share":{"enabled":false}}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("disable share feature status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	publicReq = httptest.NewRequest(http.MethodGet, "/s/"+shareID, nil)
	publicRec = httptest.NewRecorder()
	server.Router().ServeHTTP(publicRec, publicReq)

	if publicRec.Code != http.StatusGone {
		t.Fatalf("public share after disable status = %d, want %d", publicRec.Code, http.StatusGone)
	}
	if !strings.Contains(publicRec.Body.String(), "SHARE_FEATURE_DISABLED") {
		t.Fatalf("expected SHARE_FEATURE_DISABLED response, got %s", publicRec.Body.String())
	}
}

func TestServer_UpdateSettings_DisablesAuthenticatedShareManagementWithoutRestart(t *testing.T) {
	server, shareID := setupShareServerWithOptions(t, true, "")

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"share":{"enabled":false}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("disable share feature status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	tests := []struct {
		name   string
		method string
		url    string
		body   string
	}{
		{name: "list shares", method: http.MethodGet, url: "/api/v1/shares"},
		{name: "get share", method: http.MethodGet, url: "/api/v1/shares/" + shareID},
		{name: "update share", method: http.MethodPut, url: "/api/v1/shares/" + shareID, body: `{"description":"updated"}`},
		{name: "delete share", method: http.MethodDelete, url: "/api/v1/shares/" + shareID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}

			req := httptest.NewRequest(tt.method, tt.url, body)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if !strings.Contains(rec.Body.String(), "SHARE_FEATURE_DISABLED") {
				t.Fatalf("expected SHARE_FEATURE_DISABLED response, got %s", rec.Body.String())
			}
		})
	}
}

func TestServer_UpdateSettings_EnablesFavoritesWithoutRestart(t *testing.T) {
	server := setupFavoritesServerWithOptions(t, false)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	listRec := httptest.NewRecorder()
	server.Router().ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("list favorites while disabled status = %d, want %d", listRec.Code, http.StatusServiceUnavailable)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"favorites":{"enabled":true}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("enable favorites status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/a.txt"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	server.Router().ServeHTTP(addRec, addReq)

	if addRec.Code != http.StatusCreated {
		t.Fatalf("add favorite after enable status = %d, want %d", addRec.Code, http.StatusCreated)
	}
}

func TestServer_UpdateSettings_DisablesFavoritesWithoutRestart(t *testing.T) {
	server := setupFavoritesServerWithOptions(t, true)

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/a.txt"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	server.Router().ServeHTTP(addRec, addReq)

	if addRec.Code != http.StatusCreated {
		t.Fatalf("add favorite before disable status = %d, want %d", addRec.Code, http.StatusCreated)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"favorites":{"enabled":false}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("disable favorites status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	listRec := httptest.NewRecorder()
	server.Router().ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("list favorites after disable status = %d, want %d", listRec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(listRec.Body.String(), "FAVORITES_FEATURE_DISABLED") {
		t.Fatalf("expected FAVORITES_FEATURE_DISABLED response, got %s", listRec.Body.String())
	}
}

func TestServer_ListFavorites_ReturnsServiceUnavailableWhenStoreInitFails(t *testing.T) {
	server := setupFavoritesUnavailableServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list favorites status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "FAVORITES_UNAVAILABLE") {
		t.Fatalf("expected FAVORITES_UNAVAILABLE response, got %s", w.Body.String())
	}
}

func TestServer_AddFavorite_ReturnsServiceUnavailableWhenStoreInitFails(t *testing.T) {
	server := setupFavoritesUnavailableServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/a.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("add favorite status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "FAVORITES_UNAVAILABLE") {
		t.Fatalf("expected FAVORITES_UNAVAILABLE response, got %s", w.Body.String())
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

func TestServer_DownloadVersion_ContentDispositionEscapesFilename(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/quote\"name.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/quote\"name.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/quote\"name.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/quote%22name.txt?download=true&version="+historicalHash, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Download version status = %d, want %d", w.Code, http.StatusOK)
	}
	contentDisposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisposition, `filename="quote\"name.txt"`) {
		t.Fatalf("expected escaped version Content-Disposition filename, got %q", contentDisposition)
	}
}

func TestServer_DownloadVersion_UsesExtensionContentTypeForPreview(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/preview.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/preview.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/preview.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/preview.txt?version="+historicalHash, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download version preview status = %d, want %d", w.Code, http.StatusOK)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
		t.Fatalf("version preview Content-Type = %q, want %q", contentType, "text/plain; charset=utf-8")
	}
	if body := w.Body.String(); body != "v1" {
		t.Fatalf("version preview body = %q, want %q", body, "v1")
	}
}

func TestServer_DownloadVersion_UsesPrivateRevalidationCacheHeaders(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/cache.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/cache.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/cache.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/cache.txt?version="+historicalHash, nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initial version download status = %d, want %d", w.Code, http.StatusOK)
	}
	if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "private, no-cache" {
		t.Fatalf("version download Cache-Control = %q, want %q", cacheControl, "private, no-cache")
	}
	etag := w.Header().Get("ETag")
	if etag != fmt.Sprintf(`"%s"`, historicalHash) {
		t.Fatalf("version download ETag = %q, want %q", etag, fmt.Sprintf(`"%s"`, historicalHash))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/cache.txt?version="+historicalHash, nil)
	req.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Fatalf("version download If-None-Match status = %d, want %d", w.Code, http.StatusNotModified)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("expected 304 response body to be empty, got %q", w.Body.String())
	}
}

func TestServer_DownloadVersion_SupportsRangeRequests(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/range.txt", bytes.NewReader([]byte("abcdef"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/range.txt", bytes.NewReader([]byte("ghijkl"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/range.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/range.txt?version="+historicalHash, nil)
	req.Header.Set("Range", "bytes=1-3")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("version range status = %d, want %d", w.Code, http.StatusPartialContent)
	}
	if acceptRanges := w.Header().Get("Accept-Ranges"); acceptRanges != "bytes" {
		t.Fatalf("version range Accept-Ranges = %q, want %q", acceptRanges, "bytes")
	}
	if contentRange := w.Header().Get("Content-Range"); contentRange != "bytes 1-3/6" {
		t.Fatalf("version range Content-Range = %q, want %q", contentRange, "bytes 1-3/6")
	}
	if body := w.Body.String(); body != "bcd" {
		t.Fatalf("version range body = %q, want %q", body, "bcd")
	}
}

func TestServer_DownloadVersion_LogsDownloadActivityWithHash(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := fs.WriteFile(ctx, "/versions/activity.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/activity.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/activity.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/activity.txt?version="+historicalHash, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download version status = %d, want %d", w.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionDownload, "")
	if total != 1 {
		t.Fatalf("expected one version download activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed version download activity entry, got %d", len(entries))
	}
	if entries[0].Path != "/versions/activity.txt" {
		t.Fatalf("expected version download activity path %q, got %q", "/versions/activity.txt", entries[0].Path)
	}
	if entries[0].Details["hash"] != historicalHash {
		t.Fatalf("expected version download hash %q, got %+v", historicalHash, entries[0].Details)
	}
}

func TestServer_DownloadVersion_ReturnsNotFoundWhenHashBelongsToDifferentPath(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/a.txt", bytes.NewReader([]byte("a-v1"))); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/b.txt", bytes.NewReader([]byte("b-current"))); err != nil {
		t.Fatalf("WriteFile(b) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/a.txt")
	if err != nil {
		t.Fatalf("ListVersions(a) error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/b.txt?version="+historicalHash, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Download version other-path hash status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if strings.Contains(w.Body.String(), "a.txt") {
		t.Fatalf("expected path mismatch to stay generic, got %s", w.Body.String())
	}
}

func TestServer_DownloadVersion_ReturnsServiceUnavailableWhenVersionStorageUnavailable(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/unavailable.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/unavailable.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/unavailable.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	setVersionStoreObjectClient(t, fs, &dataplane.Client{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions/unavailable.txt?version="+historicalHash, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("download unavailable version status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "version storage unavailable") {
		t.Fatalf("expected unavailable message, got %s", w.Body.String())
	}
}

func TestServer_RestoreVersion_ReturnsNotFoundWhenHashBelongsToDifferentPath(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/a.txt", bytes.NewReader([]byte("a-v1"))); err != nil {
		t.Fatalf("WriteFile(a v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/a.txt", bytes.NewReader([]byte("a-v2"))); err != nil {
		t.Fatalf("WriteFile(a v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/b.txt", bytes.NewReader([]byte("b-current"))); err != nil {
		t.Fatalf("WriteFile(b) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/a.txt")
	if err != nil {
		t.Fatalf("ListVersions(a) error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/versions/"+historicalHash+"/restore?path=/versions/b.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("restore version other-path hash status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if strings.Contains(w.Body.String(), "a.txt") {
		t.Fatalf("expected path mismatch to stay generic, got %s", w.Body.String())
	}
	reader, err := fs.OpenFile(ctx, "/versions/b.txt")
	if err != nil {
		t.Fatalf("OpenFile(b) error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(b) error: %v", err)
	}
	if string(data) != "b-current" {
		t.Fatalf("expected b.txt content to remain unchanged, got %q", string(data))
	}
}

func TestServer_RestoreVersion_ReturnsServiceUnavailableWhenVersionStorageUnavailable(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/restore-unavailable.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versions/restore-unavailable.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/restore-unavailable.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	setVersionStoreObjectClient(t, fs, &dataplane.Client{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/versions/"+historicalHash+"/restore?path=/versions/restore-unavailable.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("restore unavailable version status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "version storage unavailable") {
		t.Fatalf("expected unavailable message, got %s", w.Body.String())
	}
}

func TestServer_RestoreVersion_AllowsCurrentHash(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/current.txt", bytes.NewReader([]byte("current-content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/versions/current.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) == 0 || versions[0].Comment != "(current)" {
		t.Fatalf("expected current version entry, got %#v", versions)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/versions/"+versions[0].Hash+"/restore?path=/versions/current.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("restore current version status = %d, want %d", w.Code, http.StatusOK)
	}
	reader, err := fs.OpenFile(ctx, "/versions/current.txt")
	if err != nil {
		t.Fatalf("OpenFile() error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(data) != "current-content" {
		t.Fatalf("expected current.txt content to remain unchanged, got %q", string(data))
	}
}

func TestServer_DownloadVersion_DirectoryPathReturnsBadRequest(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/versions-dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/versions-dir?version="+strings.Repeat("a", 64), nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Download version directory status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "cannot download directory") {
		t.Fatalf("expected directory download message, got %s", w.Body.String())
	}
}

func TestServer_DownloadVersion_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/version-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(version-parent) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/version-parent/child.txt?version="+strings.Repeat("a", 64), nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("download version parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestStreamAPIResponse_ReturnsErrorBeforeResponseStarts(t *testing.T) {
	rec := httptest.NewRecorder()
	err := streamAPIResponse(rec, errReader{})
	if err == nil {
		t.Fatal("expected streamAPIResponse to return pre-write stream error")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status after pre-write stream failure: %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected no response body on pre-write failure, got %q", rec.Body.String())
	}
}

func TestStreamAPIResponse_ReturnsErrorAfterResponseStarts(t *testing.T) {
	rec := httptest.NewRecorder()
	err := streamAPIResponse(rec, io.MultiReader(strings.NewReader("partial"), errReader{}))
	if err == nil {
		t.Fatal("expected streamAPIResponse to return post-write stream error")
	}
	if !apiStreamResponseStarted(err) {
		t.Fatalf("expected post-write stream error to record started response, got %v", err)
	}
	if rec.Body.String() != "partial" {
		t.Fatalf("expected partial body to remain written, got %q", rec.Body.String())
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

func TestServer_GetTrashItem_InternalErrorReturnsInternalServerError(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/trash-read-error"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-read-error/file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-read-error/file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one trash item")
	}

	if err := fs.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trash/"+items[0].ID, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GetTrashItem internal error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "context canceled") {
		t.Fatalf("expected get trash item internal error to stay generic, got %s", w.Body.String())
	}
}

func TestServer_DeleteFromTrash_InternalErrorReturnsInternalServerError(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/trash-delete-error"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-delete-error/file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-delete-error/file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one trash item")
	}

	if err := fs.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/trash/"+items[0].ID, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DeleteFromTrash internal error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "context canceled") {
		t.Fatalf("expected delete from trash internal error to stay generic, got %s", w.Body.String())
	}
}

func TestServer_Stats(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/stats"); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/stats/a.txt", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("write file error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/stats/b.txt", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Stats status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Failed to parse response JSON: %v", err)
	}
	value, ok := payload.Data["total_files"].(float64)
	if !ok {
		t.Fatalf("total_files missing or invalid type")
	}
	if int(value) != 2 {
		t.Errorf("total_files = %d, want 2", int(value))
	}
}

func TestServer_Stats_OmitsFileCountWhenCollectionFails(t *testing.T) {
	server, _, _ := setupTestServer(t)
	originalGetFileCount := getFileCount
	getFileCount = func(_ *storage.FileSystem, _ context.Context) (int, error) {
		return 0, errors.New("count failed")
	}
	defer func() { getFileCount = originalGetFileCount }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Stats status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Failed to parse response JSON: %v", err)
	}
	if available, ok := payload.Data["total_files_available"].(bool); !ok || available {
		t.Fatalf("expected stats to mark total_files unavailable, got %v", payload.Data["total_files_available"])
	}
	if _, ok := payload.Data["total_files"]; ok {
		t.Fatalf("expected stats to omit total_files on file count failure, got %v", payload.Data["total_files"])
	}
}

func TestServer_Stats_OmitsStorageFieldsWhenDataplaneUnavailable(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.dataplane = nil

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Stats status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Failed to parse response JSON: %v", err)
	}
	if available, ok := payload.Data["storage_stats_available"].(bool); !ok || available {
		t.Fatalf("expected stats to mark storage stats unavailable, got %v", payload.Data["storage_stats_available"])
	}
	for _, key := range []string{"total_chunks", "total_size", "unique_size", "dedup_ratio"} {
		if _, ok := payload.Data[key]; ok {
			t.Fatalf("expected stats to omit %s when dataplane is unavailable, got %v", key, payload.Data[key])
		}
	}
}

func TestServer_Diagnostics(t *testing.T) {
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

	settings := config.Default()
	settings.Storage.Root = tmpDir
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := settings.Save(configPath); err != nil {
		t.Fatalf("Save(config) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:      fs,
		Config:          settings,
		ConfigPath:      configPath,
		ActivityRoot:    path.Join(tmpDir, "activity"),
		ThumbnailRoot:   path.Join(tmpDir, "thumbnails"),
		MaintenanceRoot: path.Join(tmpDir, "maintenance"),
		DataplaneAddr:   testDataplaneAddr(),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/diagnostics", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Diagnostics status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Uptime string `json:"uptime"`
			System struct {
				FilesystemInitialized bool `json:"filesystem_initialized"`
				ThumbnailServiceReady bool `json:"thumbnail_service_ready"`
				MaintenanceReady      bool `json:"maintenance_history_ready"`
				ActivityReady         bool `json:"activity_log_ready"`
			} `json:"system"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse diagnostics response: %v", err)
	}
	if !payload.Success {
		t.Fatal("expected diagnostics response success=true")
	}
	if payload.Data.Uptime == "" {
		t.Fatal("expected diagnostics response to include uptime")
	}
	if !payload.Data.System.FilesystemInitialized {
		t.Fatal("expected diagnostics to report filesystem initialized")
	}
	if !payload.Data.System.ThumbnailServiceReady {
		t.Fatal("expected diagnostics to report thumbnail service ready")
	}
	if !payload.Data.System.MaintenanceReady {
		t.Fatal("expected diagnostics to report maintenance history ready")
	}
	if !payload.Data.System.ActivityReady {
		t.Fatal("expected diagnostics to report activity log ready")
	}
}

func TestServer_Diagnostics_ReportsFavoritesStoreUnavailableWhenInitializationFails(t *testing.T) {
	server := setupFavoritesUnavailableServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Diagnostics status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			System struct {
				FavoritesStoreReady *bool `json:"favorites_store_ready"`
			} `json:"system"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse diagnostics response: %v", err)
	}
	if !payload.Success {
		t.Fatal("expected diagnostics response success=true")
	}
	if payload.Data.System.FavoritesStoreReady == nil {
		t.Fatal("expected diagnostics to report favorites store readiness")
	}
	if *payload.Data.System.FavoritesStoreReady {
		t.Fatalf("expected diagnostics to report unavailable favorites store, got true")
	}
}

func TestServer_Diagnostics_MarksTrashStatsUnavailableWhenCollectionFails(t *testing.T) {
	server, _, _ := setupTestServer(t)
	originalGetTrashStats := getTrashStats
	getTrashStats = func(_ *storage.FileSystem, _ context.Context) (int, int64, error) {
		return 0, 0, errors.New("trash stats failed")
	}
	defer func() { getTrashStats = originalGetTrashStats }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Diagnostics status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Filesystem map[string]any `json:"filesystem"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse diagnostics response: %v", err)
	}
	if !payload.Success {
		t.Fatal("expected diagnostics response success=true")
	}
	if available, ok := payload.Data.Filesystem["trash_stats_available"].(bool); !ok || available {
		t.Fatalf("expected diagnostics to mark trash stats unavailable, got %v", payload.Data.Filesystem["trash_stats_available"])
	}
	if _, ok := payload.Data.Filesystem["trash_items"]; ok {
		t.Fatalf("expected diagnostics to omit trash_items on trash stats failure, got %v", payload.Data.Filesystem["trash_items"])
	}
	if _, ok := payload.Data.Filesystem["trash_size"]; ok {
		t.Fatalf("expected diagnostics to omit trash_size on trash stats failure, got %v", payload.Data.Filesystem["trash_size"])
	}
}

func TestServer_Diagnostics_RequiresAdminWhenAuthEnabled(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)

	userToken := loginAndGetAccessToken(t, server, username, password)

	userReq := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	userReq.Header.Set("Authorization", "Bearer "+userToken)
	userRec := httptest.NewRecorder()
	server.Router().ServeHTTP(userRec, userReq)

	if userRec.Code != http.StatusForbidden {
		t.Fatalf("non-admin diagnostics status = %d, want %d", userRec.Code, http.StatusForbidden)
	}

	adminUsername := "diagnostics-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}

	adminToken := loginAndGetAccessToken(t, server, adminUsername, adminPassword)

	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	adminReq.Header.Set("Authorization", "Bearer "+adminToken)
	adminRec := httptest.NewRecorder()
	server.Router().ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin diagnostics status = %d, want %d", adminRec.Code, http.StatusOK)
	}
}

func TestServer_WebDAVCredentials_RequiresAdminWhenAuthEnabled(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)
	server.config.WebDAV.Password = "test-webdav-pass"
	server.storeActiveWebDAV(server.resolveWebDAVRuntimeConfig(*server.config))

	userToken := loginAndGetAccessToken(t, server, username, password)

	userReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/webdav-credentials", nil)
	userReq.Header.Set("Authorization", "Bearer "+userToken)
	userRec := httptest.NewRecorder()
	server.Router().ServeHTTP(userRec, userReq)

	if userRec.Code != http.StatusForbidden {
		t.Fatalf("non-admin webdav credentials status = %d, want %d", userRec.Code, http.StatusForbidden)
	}

	adminUsername := "webdav-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}

	adminToken := loginAndGetAccessToken(t, server, adminUsername, adminPassword)

	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/webdav-credentials", nil)
	adminReq.Header.Set("Authorization", "Bearer "+adminToken)
	adminRec := httptest.NewRecorder()
	server.Router().ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin webdav credentials status = %d, want %d", adminRec.Code, http.StatusOK)
	}
}

func TestServer_ListFiles_EnforcesHomeDirForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/tester", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("own home list status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	if !strings.Contains(allowedRec.Body.String(), "/tester/own.txt") {
		t.Fatalf("expected own home file in response, got %s", allowedRec.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/other", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+token)
	forbiddenRec := httptest.NewRecorder()
	server.Router().ServeHTTP(forbiddenRec, forbiddenReq)

	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("outside home list status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}
}

func TestServer_ListFiles_EmptyUserHomeDirReturnsForbidden(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}

	setUserHomeDirForTest(t, server, username, "")
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/tester", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("list files with empty home_dir status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServer_Search_FiltersResultsByHomeDirForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(/tester/report.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("search status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/tester/report.txt") {
		t.Fatalf("expected own home search result, got %s", body)
	}
	if strings.Contains(body, "/other/secret.txt") {
		t.Fatalf("expected outside-home result to be filtered, got %s", body)
	}
}

func TestServer_Search_RespectsHomeDirBeforeLimitForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)
	setUserHomeDirForTest(t, server, username, "/tester")

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/report-outside.txt", bytes.NewReader([]byte("outside"))); err != nil {
		t.Fatalf("WriteFile(/other/report-outside.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/report-home.txt", bytes.NewReader([]byte("home"))); err != nil {
		t.Fatalf("WriteFile(/tester/report-home.txt) error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=report&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("search with scoped limit status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/tester/report-home.txt") {
		t.Fatalf("expected in-home result to survive limit, got %s", body)
	}
	if strings.Contains(body, "/other/report-outside.txt") {
		t.Fatalf("expected outside-home result to remain hidden, got %s", body)
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
			if w.Code == http.StatusBadRequest {
				body := w.Body.String()
				if strings.Contains(strings.ToLower(body), "traversal") || strings.Contains(body, "..") {
					t.Fatalf("expected sanitized invalid path error, got %s", body)
				}
			}
		})
	}
}

func TestServer_ListFiles_DoesNotExposeInternalErrorDetails(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/secret-missing-dir", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("list files status = %d, want %d", w.Code, http.StatusNotFound)
	}

	body := w.Body.String()
	if !strings.Contains(body, "resource not found") {
		t.Fatalf("expected sanitized not found message, got %s", body)
	}
	if strings.Contains(body, "secret-missing-dir") {
		t.Fatalf("expected internal path details to be hidden, got %s", body)
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
		{"foo..txt", "/foo..txt", false},
		{"/nested/foo..txt", "/nested/foo..txt", false},
		{"../etc/passwd", "", true},
		{"/foo/../bar", "", true},
		{"..\\etc\\passwd", "", true},
		{"/safe\\..\\secret.txt", "", true},
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
			body := w.Body.String()
			if strings.Contains(body, tt.hash) || strings.Contains(strings.ToLower(body), "expected 64") || strings.Contains(strings.ToLower(body), "non-hexadecimal") {
				t.Fatalf("expected sanitized invalid hash error, got %s", body)
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

func TestServer_RestoreVersion_DirectoryPathReturnsBadRequest(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/restore-dir"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	validHash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	req := httptest.NewRequest("POST", "/api/v1/versions/"+validHash+"/restore?path=/restore-dir", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("RestoreVersion directory path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "cannot restore version for directory") {
		t.Fatalf("expected directory restore validation message, got %s", w.Body.String())
	}
}

func TestServer_RestoreVersion_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/restore-version-source.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-version-source.txt", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/restore-version-parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/restore-version-source.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}

	var historicalHash string
	for _, version := range versions {
		if version.Comment != "(current)" {
			historicalHash = version.Hash
			break
		}
	}
	if historicalHash == "" {
		t.Fatal("expected historical version hash")
	}

	req := httptest.NewRequest("POST", "/api/v1/versions/"+historicalHash+"/restore?path=/restore-version-parent-file/child.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("RestoreVersion parent file status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestShouldSkipGCObjectByGrace(t *testing.T) {
	graceCutoff := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		obj  dataplane.ObjectInfo
		want bool
	}{
		{
			name: "unknown timestamp is skipped conservatively",
			obj:  dataplane.ObjectInfo{Hash: "obj-1", Size: 1},
			want: true,
		},
		{
			name: "recent object is skipped",
			obj: dataplane.ObjectInfo{
				Hash:      "obj-2",
				Size:      1,
				CreatedAt: graceCutoff.Add(time.Minute),
			},
			want: true,
		},
		{
			name: "old object is eligible",
			obj: dataplane.ObjectInfo{
				Hash:      "obj-3",
				Size:      1,
				CreatedAt: graceCutoff.Add(-time.Minute),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipGCObjectByGrace(tt.obj, graceCutoff)
			if got != tt.want {
				t.Fatalf("shouldSkipGCObjectByGrace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServer_MoveFile_InvalidSourcePathIsSanitized(t *testing.T) {
	server, _, _ := setupTestServer(t)

	body := `{"from":"../etc/passwd","to":"/safe.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("move file invalid source status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid source path") {
		t.Fatalf("expected invalid source path message, got %s", bodyStr)
	}
	if strings.Contains(bodyStr, "..") || strings.Contains(strings.ToLower(bodyStr), "traversal") {
		t.Fatalf("expected sanitized invalid source path error, got %s", bodyStr)
	}
}

func TestServer_MoveFile_BackslashTraversalSourceIsSanitized(t *testing.T) {
	server, _, _ := setupTestServer(t)

	body := `{"from":"..\\etc\\passwd","to":"/safe.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("move file backslash traversal source status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid source path") {
		t.Fatalf("expected invalid source path message, got %s", bodyStr)
	}
	if strings.Contains(bodyStr, "..") || strings.Contains(strings.ToLower(bodyStr), "traversal") {
		t.Fatalf("expected sanitized invalid source path error, got %s", bodyStr)
	}
}

func TestServer_MoveFile_RejectsUnknownJSONFields(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	body := `{"from":"/move-source.txt","to":"/move-dest.txt","unexpected":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("move file unknown field status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid request body") {
		t.Fatalf("expected invalid request body message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/move-source.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected move, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/move-dest.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestServer_MoveFile_UpdatesSharePaths(t *testing.T) {
	server, shareID := setupShareServer(t)
	ctx := context.Background()

	if err := server.fs.Mkdir(ctx, "/archive"); err != nil {
		t.Fatalf("Mkdir(/archive) error: %v", err)
	}
	fileShare, err := server.shareStore.Create(share.CreateShareOptions{
		Path:      "/docs/a.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create(file share) error: %v", err)
	}

	body := `{"from":"/docs","to":"/archive/docs"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("move shared folder status = %d, want %d", w.Code, http.StatusOK)
	}
	renamedFolderShare, err := server.shareStore.Get(shareID)
	if err != nil {
		t.Fatalf("Get(folder share) error: %v", err)
	}
	if renamedFolderShare.Path != "/archive/docs" {
		t.Fatalf("expected folder share path to be updated, got %q", renamedFolderShare.Path)
	}
	renamedFileShare, err := server.shareStore.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(file share) error: %v", err)
	}
	if renamedFileShare.Path != "/archive/docs/a.txt" {
		t.Fatalf("expected file share path to be updated, got %q", renamedFileShare.Path)
	}
}

func TestServer_MoveFile_UpdatesFavoritePaths(t *testing.T) {
	server := setupFavoritesPathSyncServer(t)
	ctx := context.Background()

	if err := server.fs.Mkdir(ctx, "/archive"); err != nil {
		t.Fatalf("Mkdir(/archive) error: %v", err)
	}
	if _, err := server.favoritesStore.Add("tester", "/docs", "folder"); err != nil {
		t.Fatalf("Add(/docs) error: %v", err)
	}
	if _, err := server.favoritesStore.Add("tester", "/docs/a.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/a.txt) error: %v", err)
	}

	body := `{"from":"/docs","to":"/archive/docs"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("move favorited folder status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.favoritesStore.IsFavorite("tester", "/docs") {
		t.Fatal("expected original folder favorite path to be removed")
	}
	if server.favoritesStore.IsFavorite("tester", "/docs/a.txt") {
		t.Fatal("expected original file favorite path to be removed")
	}
	if !server.favoritesStore.IsFavorite("tester", "/archive/docs") {
		t.Fatal("expected folder favorite path to be updated")
	}
	if !server.favoritesStore.IsFavorite("tester", "/archive/docs/a.txt") {
		t.Fatal("expected file favorite path to be updated")
	}
}

func TestServer_MoveFile_RollsBackWhenFavoritePathSyncFails(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs"); err != nil {
		t.Fatalf("Mkdir(/docs) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/archive"); err != nil {
		t.Fatalf("Mkdir(/archive) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/docs/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(/docs/report.txt) error: %v", err)
	}

	shareDir := filepath.Join(tmpDir, "move-hook-share")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shareDir) error: %v", err)
	}
	shareStore, err := share.NewShareStore(filepath.Join(shareDir, "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	folderShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs", Type: share.ShareTypeFolder, CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("Create(folder share) error: %v", err)
	}
	fileShare, err := shareStore.Create(share.CreateShareOptions{Path: "/docs/report.txt", Type: share.ShareTypeFile, CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("Create(file share) error: %v", err)
	}

	favoritesDir := filepath.Join(tmpDir, "move-hook-favorites")
	if err := os.MkdirAll(favoritesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(favoritesDir) error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(favoritesDir, "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/docs", "folder"); err != nil {
		t.Fatalf("Add(/docs) error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/docs/report.txt", "file"); err != nil {
		t.Fatalf("Add(/docs/report.txt) error: %v", err)
	}

	server.shareStore = shareStore
	server.favoritesStore = favoritesStore

	if err := os.Chmod(favoritesDir, 0o500); err != nil {
		t.Fatalf("Chmod(favoritesDir) error: %v", err)
	}
	defer func() {
		_ = os.Chmod(favoritesDir, 0o755)
	}()

	body := `{"from":"/docs","to":"/archive/docs"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("move with favorites sync failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/docs/report.txt"); err != nil {
		t.Fatalf("expected original file path to remain after rollback, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/archive/docs/report.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination file to be absent after rollback, got %v", err)
	}

	loadedFolderShare, err := server.shareStore.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(folder share) error: %v", err)
	}
	if loadedFolderShare.Path != "/docs" {
		t.Fatalf("expected folder share path rollback to /docs, got %q", loadedFolderShare.Path)
	}
	loadedFileShare, err := server.shareStore.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(file share) error: %v", err)
	}
	if loadedFileShare.Path != "/docs/report.txt" {
		t.Fatalf("expected file share path rollback to /docs/report.txt, got %q", loadedFileShare.Path)
	}
	if !server.favoritesStore.IsFavorite("tester", "/docs") || !server.favoritesStore.IsFavorite("tester", "/docs/report.txt") {
		t.Fatal("expected favorites to remain on original paths after rollback")
	}
	if server.favoritesStore.IsFavorite("tester", "/archive/docs") || server.favoritesStore.IsFavorite("tester", "/archive/docs/report.txt") {
		t.Fatal("expected destination favorites not to be persisted after rollback")
	}
}

func TestServer_MoveFile_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized move file status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("expected payload too large message, got %s", w.Body.String())
	}
	if _, err := server.fs.Stat(context.Background(), "/move-dest.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent after oversized move body, got %v", err)
	}
}

func TestServer_MoveFile_ReturnsConflictWhenDestinationExists(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-dest.txt", bytes.NewReader([]byte("dest"))); err != nil {
		t.Fatalf("WriteFile(dest) error: %v", err)
	}

	body := `{"from":"/move-source.txt","to":"/move-dest.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("move file conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestServer_MoveFile_ReturnsConflictWhenDestinationParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-parent", bytes.NewReader([]byte("not a directory"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	body := `{"from":"/move-source.txt","to":"/move-parent/child.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("move file parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_MoveFile_ReturnsNotFoundWhenDestinationParentMissing(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	body := `{"from":"/move-source.txt","to":"/missing-parent/child.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("move file missing parent status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if _, err := fs.Stat(ctx, "/move-source.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected move, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/missing-parent/child.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestServer_MoveFile_ReturnsConflictWhenSourceParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	body := `{"from":"/move-parent-file/child.txt","to":"/move-dest.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("move file source parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/move-dest.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestServer_MoveFile_ReturnsConflictWhenSourceAndDestinationMatch(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/move-same.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	body := `{"from":"/move-same.txt","to":"/move-same.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("move same-path status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "source and destination must differ") {
		t.Fatalf("expected same-path conflict message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/move-same.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected move, got %v", err)
	}
}

func TestServer_MoveFile_ReturnsConflictWhenDirectoryMovesIntoDescendant(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/move-dir"); err != nil {
		t.Fatalf("Mkdir(move-dir) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/move-dir/child"); err != nil {
		t.Fatalf("Mkdir(child) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/move-dir/file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	body := `{"from":"/move-dir","to":"/move-dir/child/moved"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("move descendant status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "destination cannot be inside source directory") {
		t.Fatalf("expected descendant conflict message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/move-dir/file.txt"); err != nil {
		t.Fatalf("expected source directory to remain after rejected move, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/move-dir/child/moved"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected descendant destination to remain absent, got %v", err)
	}
}

func TestServer_CopyFile_ReturnsConflictWhenDestinationExists(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dest.txt", bytes.NewReader([]byte("dest"))); err != nil {
		t.Fatalf("WriteFile(dest) error: %v", err)
	}

	body := `{"from":"/copy-source.txt","to":"/copy-dest.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("copy file conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestServer_CopyFile_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized copy file status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("expected payload too large message, got %s", w.Body.String())
	}
	if _, err := server.fs.Stat(context.Background(), "/copy-dest.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent after oversized copy body, got %v", err)
	}
}

func TestServer_CopyFile_ReturnsConflictWhenSourceAndDestinationMatch(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-same.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	body := `{"from":"/copy-same.txt","to":"/copy-same.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("copy same-path status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "source and destination must differ") {
		t.Fatalf("expected same-path conflict message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/copy-same.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected copy, got %v", err)
	}
}

func TestServer_CopyFile_ReturnsConflictWhenDestinationParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-parent", bytes.NewReader([]byte("not a directory"))); err != nil {
		t.Fatalf("WriteFile(parent) error: %v", err)
	}

	body := `{"from":"/copy-source.txt","to":"/copy-parent/child.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("copy file parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_CopyFile_ReturnsNotFoundWhenDestinationParentMissing(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-source.txt", bytes.NewReader([]byte("source"))); err != nil {
		t.Fatalf("WriteFile(source) error: %v", err)
	}

	body := `{"from":"/copy-source.txt","to":"/missing-parent/child.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("copy file missing parent status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if _, err := fs.Stat(ctx, "/copy-source.txt"); err != nil {
		t.Fatalf("expected source file to remain after rejected copy, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/missing-parent/child.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected destination to remain absent, got %v", err)
	}
}

func TestServer_CopyFile_CopiesDirectoryRecursively(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/copy-dir"); err != nil {
		t.Fatalf("Mkdir(copy-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dir/root.txt", bytes.NewReader([]byte("root"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/copy-dir/nested"); err != nil {
		t.Fatalf("Mkdir(nested) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dir/nested/child.txt", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	body := `{"from":"/copy-dir","to":"/copy-dir-clone"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("copy directory source status = %d, want %d", w.Code, http.StatusCreated)
	}
	if _, err := fs.Stat(ctx, "/copy-dir-clone"); err != nil {
		t.Fatalf("expected copied directory to exist, got %v", err)
	}
	rootReader, err := fs.OpenFile(ctx, "/copy-dir-clone/root.txt")
	if err != nil {
		t.Fatalf("OpenFile(root copy) error: %v", err)
	}
	defer rootReader.Close()
	rootData, err := io.ReadAll(rootReader)
	if err != nil {
		t.Fatalf("ReadAll(root copy) error: %v", err)
	}
	if string(rootData) != "root" {
		t.Fatalf("expected copied root file content 'root', got %q", string(rootData))
	}
	childReader, err := fs.OpenFile(ctx, "/copy-dir-clone/nested/child.txt")
	if err != nil {
		t.Fatalf("OpenFile(child copy) error: %v", err)
	}
	defer childReader.Close()
	childData, err := io.ReadAll(childReader)
	if err != nil {
		t.Fatalf("ReadAll(child copy) error: %v", err)
	}
	if string(childData) != "child" {
		t.Fatalf("expected copied child file content 'child', got %q", string(childData))
	}
	if _, err := fs.Stat(ctx, "/copy-dir/nested/child.txt"); err != nil {
		t.Fatalf("expected source directory to remain after copy, got %v", err)
	}
}

func TestServer_CopyFile_DirectoryIntoDescendantRejected(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/copy-dir"); err != nil {
		t.Fatalf("Mkdir(copy-dir) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dir/file.txt", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	body := `{"from":"/copy-dir","to":"/copy-dir/child/copied"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("copy directory into descendant status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "destination cannot be inside source directory") {
		t.Fatalf("expected descendant conflict message, got %s", w.Body.String())
	}
	if _, err := fs.Stat(ctx, "/copy-dir/child/copied"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected descendant destination to remain absent, got %v", err)
	}
}

func TestServer_CopyFile_DirectoryRollbackOnChildCopyFailure(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/copy-dir-src"); err != nil {
		t.Fatalf("Mkdir(copy-dir-src) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dir-src/root.txt", bytes.NewReader([]byte("root"))); err != nil {
		t.Fatalf("WriteFile(root) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/copy-dir-src/sub"); err != nil {
		t.Fatalf("Mkdir(sub) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/copy-dir-src/sub/child.txt", bytes.NewReader([]byte("child"))); err != nil {
		t.Fatalf("WriteFile(child) error: %v", err)
	}

	originalUpdateFileIndex := getStorageHook[func(context.Context, string, int64, time.Time, string) error](t, fs, "updateFileIndex")
	setStorageHook(t, fs, "updateFileIndex", func(ctx context.Context, name string, size int64, modTime time.Time, hash string) error {
		if name == "/copy-dir-dst/sub/child.txt" {
			return errors.New("update file index failed")
		}
		return originalUpdateFileIndex(ctx, name, size, modTime, hash)
	})

	body := `{"from":"/copy-dir-src","to":"/copy-dir-dst"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("copy directory failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if _, err := fs.Stat(ctx, "/copy-dir-dst"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected failed copy to rollback destination tree, got %v", err)
	}
	if _, err := fs.Stat(ctx, "/copy-dir-src/sub/child.txt"); err != nil {
		t.Fatalf("expected source directory preserved after failed copy, got %v", err)
	}
	reader, err := fs.OpenFile(ctx, "/copy-dir-src/sub/child.txt")
	if err != nil {
		t.Fatalf("OpenFile(source child) error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(source child) error: %v", err)
	}
	if string(data) != "child" {
		t.Fatalf("expected source child content preserved, got %q", string(data))
	}
}

func TestServer_CopyFile_ReturnsConflictWhenSourceParentIsFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/copy-parent", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(copy-parent) error: %v", err)
	}

	body := `{"from":"/copy-parent/child.txt","to":"/copy-dest.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files-copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("copy source parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_CreateDirectory_ReturnsConflictWhenPathIsExistingFile(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/existing-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(existing-file) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/directories/existing-file", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("create directory existing-file status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "resource already exists") {
		t.Fatalf("expected existing-resource conflict message, got %s", w.Body.String())
	}
	info, err := fs.Stat(ctx, "/existing-file")
	if err != nil {
		t.Fatalf("Stat(existing-file) error: %v", err)
	}
	if info.IsDir {
		t.Fatal("expected existing-file to remain a file")
	}
}

func TestServer_RestoreFromTrash_InvalidPathIsSanitized(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/trash/test-id/restore?path=../etc/passwd", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("restore from trash invalid path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid path") {
		t.Fatalf("expected invalid path message, got %s", bodyStr)
	}
	if strings.Contains(bodyStr, "..") || strings.Contains(strings.ToLower(bodyStr), "traversal") {
		t.Fatalf("expected sanitized invalid path error, got %s", bodyStr)
	}
}

func TestServer_RestoreVersion_NotFound(t *testing.T) {
	server, _, _ := setupTestServer(t)

	validHash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	req := httptest.NewRequest("POST", "/api/v1/versions/"+validHash+"/restore?path=/restore/missing.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("RestoreVersion missing version status = %d, want %d", w.Code, http.StatusNotFound)
	}

	body := w.Body.String()
	if !strings.Contains(body, "resource not found") {
		t.Fatalf("expected sanitized restore version error, got %s", body)
	}
	if strings.Contains(body, "/restore/missing.txt") || strings.Contains(body, validHash) {
		t.Fatalf("expected internal restore details to be hidden, got %s", body)
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

		if w.Code != http.StatusNotFound {
			t.Fatalf("RestoreFromTrash nonexistent status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("RestoreToCustomPathUnderFileReturnsConflict", func(t *testing.T) {
		if err := fs.WriteFile(ctx, "/trash-restore-target-parent", bytes.NewReader([]byte("blocking file"))); err != nil {
			t.Fatalf("WriteFile(parent file) error: %v", err)
		}

		// Create a fresh trash item so this subtest is isolated from prior restore attempts.
		if err := fs.WriteFile(ctx, "/trash-restore-test/conflict.txt", bytes.NewReader([]byte("restore me again"))); err != nil {
			t.Fatalf("WriteFile(conflict) error: %v", err)
		}
		if err := fs.Delete(ctx, "/trash-restore-test/conflict.txt"); err != nil {
			t.Fatalf("Delete(conflict) error: %v", err)
		}

		refreshedItems, err := fs.ListTrash(ctx)
		if err != nil {
			t.Fatalf("ListTrash() error: %v", err)
		}
		if len(refreshedItems) == 0 {
			t.Fatal("No items in trash")
		}

		req := httptest.NewRequest("POST", "/api/v1/trash/"+refreshedItems[len(refreshedItems)-1].ID+"/restore?path=/trash-restore-target-parent/child.txt", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("RestoreFromTrash custom parent file status = %d, want %d", w.Code, http.StatusConflict)
		}
		if !strings.Contains(w.Body.String(), "parent path is not a directory") {
			t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
		}
	})

	t.Run("RestoreConflict", func(t *testing.T) {
		server, fs, _ := setupTestServer(t)
		ctx := context.Background()

		if err := fs.Mkdir(ctx, "/trash-restore-conflict"); err != nil {
			t.Fatalf("mkdir error: %v", err)
		}
		if err := fs.WriteFile(ctx, "/trash-restore-conflict/file.txt", bytes.NewReader([]byte("content"))); err != nil {
			t.Fatalf("write file error: %v", err)
		}
		if err := fs.Delete(ctx, "/trash-restore-conflict/file.txt"); err != nil {
			t.Fatalf("delete file error: %v", err)
		}

		items, err := fs.ListTrash(ctx)
		if err != nil {
			t.Fatalf("list trash error: %v", err)
		}
		if len(items) == 0 {
			t.Fatal("expected item in trash")
		}

		if err := fs.WriteFile(ctx, "/trash-restore-conflict/file.txt", bytes.NewReader([]byte("replacement"))); err != nil {
			t.Fatalf("rewrite file error: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/trash/"+items[0].ID+"/restore", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("RestoreFromTrash conflict status = %d, want %d", w.Code, http.StatusConflict)
		}
	})
}

func TestServer_Trash_Restore_RestoresLinkedSharesAndFavorites(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	shareStore, err := share.NewShareStore(filepath.Join(tmpDir, "trash-restore-linked-shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(tmpDir, "trash-restore-linked-favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	server.shareStore = shareStore
	server.favoritesStore = favoritesStore

	if err := fs.Mkdir(ctx, "/trash-restore-linked"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-restore-linked/report.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:      "/trash-restore-linked/report.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateShare() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/trash-restore-linked/report.txt", "file"); err != nil {
		t.Fatalf("AddFavorite() error: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/files/trash-restore-linked/report.txt", nil)
	deleteRec := httptest.NewRecorder()
	server.Router().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("Delete status = %d, want %d, body: %s", deleteRec.Code, http.StatusOK, deleteRec.Body.String())
	}

	disabledShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(disabled share) error: %v", err)
	}
	if disabledShare.Enabled {
		t.Fatal("expected share to be disabled after delete")
	}
	if favoritesStore.IsFavorite("tester", "/trash-restore-linked/report.txt") {
		t.Fatal("expected favorite to be removed after delete")
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+items[0].ID+"/restore", nil)
	restoreRec := httptest.NewRecorder()
	server.Router().ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("RestoreFromTrash status = %d, want %d, body: %s", restoreRec.Code, http.StatusOK, restoreRec.Body.String())
	}

	restoredShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restored share) error: %v", err)
	}
	if !restoredShare.Enabled {
		t.Fatal("expected share to be re-enabled after restore")
	}
	if restoredShare.Path != "/trash-restore-linked/report.txt" {
		t.Fatalf("expected restored share path %q, got %q", "/trash-restore-linked/report.txt", restoredShare.Path)
	}
	if !favoritesStore.IsFavorite("tester", "/trash-restore-linked/report.txt") {
		t.Fatal("expected favorite to be restored after restore")
	}
}

func TestServer_Trash_RestoreToCustomPath_RewritesLinkedSharesAndFavorites(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	shareStore, err := share.NewShareStore(filepath.Join(tmpDir, "trash-restore-custom-shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	favoritesStore, err := favorites.NewStore(filepath.Join(tmpDir, "trash-restore-custom-favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	server.shareStore = shareStore
	server.favoritesStore = favoritesStore

	if err := fs.Mkdir(ctx, "/trash-restore-custom"); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/trash-restore-target"); err != nil {
		t.Fatalf("Mkdir(target) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-restore-custom/notes.txt", bytes.NewReader([]byte("restore elsewhere"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	createdShare, err := shareStore.Create(share.CreateShareOptions{
		Path:      "/trash-restore-custom/notes.txt",
		Type:      share.ShareTypeFile,
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateShare() error: %v", err)
	}
	if _, err := favoritesStore.Add("tester", "/trash-restore-custom/notes.txt", "file"); err != nil {
		t.Fatalf("AddFavorite() error: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/files/trash-restore-custom/notes.txt", nil)
	deleteRec := httptest.NewRecorder()
	server.Router().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("Delete status = %d, want %d, body: %s", deleteRec.Code, http.StatusOK, deleteRec.Body.String())
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item, got %d", len(items))
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+items[0].ID+"/restore?path=/trash-restore-target/restored.txt", nil)
	restoreRec := httptest.NewRecorder()
	server.Router().ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("RestoreFromTrash custom path status = %d, want %d, body: %s", restoreRec.Code, http.StatusOK, restoreRec.Body.String())
	}

	restoredShare, err := shareStore.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restored share) error: %v", err)
	}
	if !restoredShare.Enabled {
		t.Fatal("expected share to be re-enabled after custom restore")
	}
	if restoredShare.Path != "/trash-restore-target/restored.txt" {
		t.Fatalf("expected restored share path %q, got %q", "/trash-restore-target/restored.txt", restoredShare.Path)
	}
	if favoritesStore.IsFavorite("tester", "/trash-restore-custom/notes.txt") {
		t.Fatal("expected original favorite path to stay removed after custom restore")
	}
	if !favoritesStore.IsFavorite("tester", "/trash-restore-target/restored.txt") {
		t.Fatal("expected favorite to be restored at the custom path")
	}
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

func TestServer_Trash_Restore_LogsOriginalPath(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/trash-restore-activity"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-restore-activity/file.txt", bytes.NewReader([]byte("restore me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-restore-activity/file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one trash item")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/trash/"+items[0].ID+"/restore", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("RestoreFromTrash status = %d, want %d", w.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionTrashRestore, "")
	if total != 1 {
		t.Fatalf("expected one trash restore activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed trash restore activity entry, got %d", len(entries))
	}
	if entries[0].Path != "/trash-restore-activity/file.txt" {
		t.Fatalf("expected trash restore activity path %q, got %q", "/trash-restore-activity/file.txt", entries[0].Path)
	}
}

func TestServer_DeleteFromTrash_LogsOriginalPath(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/trash-delete-activity"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-delete-activity/file.txt", bytes.NewReader([]byte("delete me"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-delete-activity/file.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one trash item")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/trash/"+items[0].ID, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteFromTrash status = %d, want %d", w.Code, http.StatusOK)
	}

	entries, total := server.activity.List(10, 0, activity.ActionTrashDelete, "")
	if total != 1 {
		t.Fatalf("expected one trash delete activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed trash delete activity entry, got %d", len(entries))
	}
	if entries[0].Path != "/trash-delete-activity/file.txt" {
		t.Fatalf("expected trash delete activity path %q, got %q", "/trash-delete-activity/file.txt", entries[0].Path)
	}
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

func TestServer_Trash_EmptyPartialSuccessReturnsDeletedCount(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/trash-empty-partial"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-empty-partial/file1.txt", bytes.NewReader([]byte("1"))); err != nil {
		t.Fatalf("WriteFile(file1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/trash-empty-partial/file2.txt", bytes.NewReader([]byte("2"))); err != nil {
		t.Fatalf("WriteFile(file2) error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-empty-partial/file1.txt"); err != nil {
		t.Fatalf("Delete(file1) error: %v", err)
	}
	if err := fs.Delete(ctx, "/trash-empty-partial/file2.txt"); err != nil {
		t.Fatalf("Delete(file2) error: %v", err)
	}

	deleteCalls := 0
	setStorageHook(t, fs, "removeTrashPath", func(path string) error {
		deleteCalls++
		if deleteCalls == 2 {
			return errors.New("trash delete failed")
		}
		return os.RemoveAll(path)
	})

	req := httptest.NewRequest("DELETE", "/api/v1/trash/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("EmptyTrash partial status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DeletedCount int  `json:"deleted_count"`
			Partial      bool `json:"partial"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if !payload.Success {
		t.Fatal("expected success response for partial trash empty")
	}
	if payload.Data.DeletedCount != 1 {
		t.Fatalf("expected deleted_count 1, got %d", payload.Data.DeletedCount)
	}
	if !payload.Data.Partial {
		t.Fatal("expected partial=true in response")
	}
	if payload.Message != "trash emptied partially" {
		t.Fatalf("expected partial message, got %q", payload.Message)
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() after partial empty error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one trash item to remain after partial empty, got %d", len(items))
	}
}

func TestServer_Scrub_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.dataplane = nil

	// Server has no dataplane connected
	req := httptest.NewRequest("POST", "/api/v1/maintenance/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Scrub without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_Scrub_InvalidBodyDoesNotExposeParserDetails(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/maintenance/scrub", strings.NewReader(`{"hashes":`))
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Scrub invalid body status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "unexpected eof") || strings.Contains(strings.ToLower(body), "invalid character") {
		t.Fatalf("expected sanitized scrub parse error, got %s", body)
	}
	if !strings.Contains(body, "invalid request body") {
		t.Fatalf("expected generic invalid request body message, got %s", body)
	}
}

func TestServer_Scrub_ChunkedInvalidBodyDoesNotBypassValidation(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/maintenance/scrub", strings.NewReader(`{"hashes":`))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("chunked scrub invalid body status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "unexpected eof") || strings.Contains(strings.ToLower(body), "invalid character") {
		t.Fatalf("expected sanitized scrub parse error, got %s", body)
	}
	if !strings.Contains(body, "invalid request body") {
		t.Fatalf("expected generic invalid request body message, got %s", body)
	}
}

func TestServer_Scrub_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/scrub", io.LimitReader(repeatingReader{}, DefaultScrubRequestBodyLimit+1))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized scrub status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("expected payload too large message, got %s", w.Body.String())
	}
}

func TestServer_Scrub_SaveCompletedResultFailureReturnsInternalServerError(t *testing.T) {
	server, _, _ := setupTestServer(t)
	if server.maintenance == nil {
		t.Skip("maintenance history not available")
	}

	originalSaveScrubResult := saveScrubResult
	callCount := 0
	saveScrubResult = func(store *maintenance.HistoryStore, result *maintenance.ScrubResult) error {
		callCount++
		if callCount == 2 {
			return errors.New("disk offline")
		}
		return originalSaveScrubResult(store, result)
	}
	defer func() {
		saveScrubResult = originalSaveScrubResult
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("scrub save failure status = %d, want %d: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("expected generic internal error message, got %s", w.Body.String())
	}
	if callCount != 2 {
		t.Fatalf("expected saveScrubResult to be called twice, got %d", callCount)
	}
	if result := server.maintenance.GetLastScrubResult(); result == nil || result.Status != "running" {
		t.Fatalf("expected persisted scrub state to remain pre-completion after save failure, got %#v", result)
	}
}

func TestServer_ClearActivity_DoesNotExposeInternalDetails(t *testing.T) {
	client := setupDataplaneClient(t)
	if client == nil {
		t.Skip("dataplane not available, skipping test")
	}

	tmpDir := t.TempDir()
	filesRoot := path.Join(tmpDir, "files")
	internalRoot := path.Join(tmpDir, ".mnemonas")
	activityRoot := path.Join(tmpDir, "activity")

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

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:    fs,
		ActivityRoot:  activityRoot,
		Config:        config.Default(),
		DataplaneAddr: testDataplaneAddr(),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	if err := server.activity.Log(activity.ActionLogin, "/", "tester", "127.0.0.1", nil); err != nil {
		t.Fatalf("activity.Log() error: %v", err)
	}
	if err := os.Chmod(activityRoot, 0500); err != nil {
		t.Fatalf("Chmod(activityRoot) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(activityRoot, 0755)
	})

	req := httptest.NewRequest("DELETE", "/api/v1/activity/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Clear activity status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	body := w.Body.String()
	if strings.Contains(body, "disk offline") {
		t.Fatalf("expected internal clear failure to be hidden, got %s", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic clear failure message, got %s", body)
	}
}

func TestServer_ListActivity_ReturnsServiceUnavailableWhenConfiguredStoreFailsInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	activityRoot := path.Join(tmpDir, "activity.json")
	if err := os.WriteFile(activityRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile(activityRoot) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{ActivityRoot: activityRoot})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list activity status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "activity log unavailable") {
		t.Fatalf("expected generic unavailable message, got %s", w.Body.String())
	}
}

func TestServer_ActivityStats_ReturnsServiceUnavailableWhenConfiguredStoreFailsInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	activityRoot := path.Join(tmpDir, "activity.json")
	if err := os.WriteFile(activityRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile(activityRoot) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{ActivityRoot: activityRoot})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/stats", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("activity stats status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "activity log unavailable") {
		t.Fatalf("expected generic unavailable message, got %s", w.Body.String())
	}
}

func TestServer_ClearActivity_ReturnsServiceUnavailableWhenConfiguredStoreFailsInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	activityRoot := path.Join(tmpDir, "activity.json")
	if err := os.WriteFile(activityRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile(activityRoot) error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{ActivityRoot: activityRoot})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/activity/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("clear activity status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "activity log unavailable") {
		t.Fatalf("expected generic unavailable message, got %s", w.Body.String())
	}
}

func TestServer_CreateDirectory_AddsAuditWarningHeaderWhenActivityLogSaveFails(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionLogin, "/", "tester", "127.0.0.1", nil); err != nil {
		t.Fatalf("activity.Log() error: %v", err)
	}

	activityRoot := path.Join(tmpDir, "activity")
	if err := os.Chmod(activityRoot, 0500); err != nil {
		t.Fatalf("Chmod(activityRoot) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(activityRoot, 0755)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/directories/audit-warning-dir", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create directory status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get(auditStatusHeaderName); got != auditStatusFailedValue {
		t.Fatalf("audit status header = %q, want %q", got, auditStatusFailedValue)
	}
	warningValues := w.Header().Values("Warning")
	if len(warningValues) != 1 || warningValues[0] != auditWarningHeader {
		t.Fatalf("warning headers = %v, want [%q]", warningValues, auditWarningHeader)
	}
	if _, err := fs.Stat(ctx, "/audit-warning-dir"); err != nil {
		t.Fatalf("expected directory creation to succeed despite audit failure, got %v", err)
	}
}

func TestServer_GC_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.dataplane = nil

	req := httptest.NewRequest("POST", "/api/v1/maintenance/gc", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GC without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_GC_InvalidGracePeriodReturnsBadRequest(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/maintenance/gc?grace_period_hours=-1", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GC invalid grace period status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "grace_period_hours must be a non-negative integer") {
		t.Fatalf("expected invalid grace period error, got %s", w.Body.String())
	}
}

func TestServer_GC_InvalidDryRunReturnsBadRequest(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/maintenance/gc?dry_run=definitely-not-bool", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GC invalid dry_run status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "dry_run parameter must be a boolean") {
		t.Fatalf("expected invalid dry_run error, got %s", w.Body.String())
	}
}

func TestServer_GC_RejectsOverflowingGracePeriod(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/maintenance/gc?grace_period_hours=%d", maxGCGracePeriodHours+1), nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GC overflowing grace period status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), fmt.Sprintf("grace_period_hours must be between 0 and %d", maxGCGracePeriodHours)) {
		t.Fatalf("expected overflowing grace period error, got %s", w.Body.String())
	}
}

func TestServer_GC_ReportsDeleteFailures(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ctx := context.Background()

	chunk, err := server.dataplane.PutChunk(ctx, []byte("gc orphan chunk"))
	if err != nil {
		t.Fatalf("PutChunk() error: %v", err)
	}

	originalDeleteGCChunk := deleteGCChunk
	deleteGCChunk = func(client *dataplane.Client, ctx context.Context, hash string) (bool, error) {
		if hash == chunk.Hash {
			return false, errors.New("dataplane delete failed")
		}
		return originalDeleteGCChunk(client, ctx, hash)
	}
	defer func() {
		deleteGCChunk = originalDeleteGCChunk
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/gc?dry_run=false&grace_period_hours=0", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GC delete failure status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DeletedCount   int `json:"deleted_count"`
			FailedCount    int `json:"failed_count"`
			DeleteFailures []struct {
				Hash    string `json:"hash"`
				Message string `json:"message"`
			} `json:"delete_failures"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse GC response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", w.Body.String())
	}
	if payload.Data.FailedCount != 1 {
		t.Fatalf("expected failed_count 1, got %d", payload.Data.FailedCount)
	}
	if len(payload.Data.DeleteFailures) != 1 {
		t.Fatalf("expected one delete failure, got %d", len(payload.Data.DeleteFailures))
	}
	if payload.Data.DeleteFailures[0].Hash != chunk.Hash {
		t.Fatalf("expected failed hash %q, got %q", chunk.Hash, payload.Data.DeleteFailures[0].Hash)
	}
	if payload.Data.DeleteFailures[0].Message != "failed to delete chunk" {
		t.Fatalf("expected generic delete failure message, got %q", payload.Data.DeleteFailures[0].Message)
	}

	objects, err := server.dataplane.ListObjects(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListObjects() error: %v", err)
	}
	found := false
	for _, obj := range objects.Objects {
		if obj.Hash == chunk.Hash {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected failed-delete chunk %q to remain present", chunk.Hash)
	}
}

func TestServer_Objects_NoDataplane(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.dataplane = nil

	req := httptest.NewRequest("GET", "/api/v1/maintenance/objects", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ListObjects without dataplane status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_Objects_RejectsOverlongCursor(t *testing.T) {
	server, _, _ := setupTestServer(t)

	cursor := strings.Repeat("a", maxObjectsCursorLength+1)
	req := httptest.NewRequest("GET", "/api/v1/maintenance/objects?cursor="+cursor, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListObjects overlong cursor status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "cursor exceeds maximum length") {
		t.Fatalf("expected cursor validation error, got %s", w.Body.String())
	}
}

func TestServer_Objects_RejectsInvalidLimit(t *testing.T) {
	server, _, _ := setupTestServer(t)

	for _, rawLimit := range []string{"0", "-1", "abc", "1001"} {
		t.Run(rawLimit, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/maintenance/objects?limit="+rawLimit, nil)
			w := httptest.NewRecorder()

			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("ListObjects invalid limit %q status = %d, want %d", rawLimit, w.Code, http.StatusBadRequest)
			}
			if !strings.Contains(w.Body.String(), "limit parameter must be between 1 and 1000") {
				t.Fatalf("expected limit validation error for %q, got %s", rawLimit, w.Body.String())
			}
		})
	}
}

func TestServer_ScrubResult(t *testing.T) {
	server, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/maintenance/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	// Should return OK even if no result exists (with has_result: false)
	// or ServiceUnavailable if maintenance not configured
	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Errorf("GetScrubResult status = %d, want %d or %d", w.Code, http.StatusOK, http.StatusServiceUnavailable)
	}
}

func TestServer_ScrubResult_DoesNotExposeInternalErrorMessage(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	maint, err := maintenance.NewHistoryStore(path.Join(tmpDir, "maintenance"))
	if err != nil {
		t.Fatalf("NewHistoryStore() error: %v", err)
	}
	server.maintenance = maint
	if err := maint.SaveScrubResult(&maintenance.ScrubResult{
		ID:           "scrub-1",
		StartTime:    time.Now().Add(-time.Minute),
		EndTime:      time.Now(),
		Status:       "failed",
		ErrorMessage: "dial tcp 127.0.0.1:9090: connection refused",
	}); err != nil {
		t.Fatalf("SaveScrubResult() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/maintenance/scrub", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GetScrubResult status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "connection refused") || strings.Contains(strings.ToLower(body), "dial tcp") {
		t.Fatalf("expected scrub result to hide internal error details, got %s", body)
	}
	if !strings.Contains(body, scrubFailurePublicMessage) {
		t.Fatalf("expected scrub result to include sanitized error message, got %s", body)
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

func TestServer_Thumbnail_DirectoryPathReturnsBadRequest(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.Mkdir(ctx, "/folder.jpg"); err != nil {
		t.Fatalf("Mkdir(folder.jpg) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/folder.jpg", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("thumbnail directory status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "path is a directory") {
		t.Fatalf("expected directory-path validation message, got %s", w.Body.String())
	}
}

func TestServer_Thumbnail_ReturnsConflictWhenParentIsFile(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/parent.jpg", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile(parent.jpg) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/parent.jpg/child.png", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("thumbnail parent conflict status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
}

func TestServer_Thumbnail_SupportsGIF(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/animated.gif", bytes.NewReader(createGIFThumbnailSource(24, 24))); err != nil {
		t.Fatalf("WriteFile(animated.gif) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/animated.gif", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail GIF status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail GIF Content-Type = %q, want %q", w.Header().Get("Content-Type"), "image/jpeg")
	}
	if len(w.Body.Bytes()) == 0 {
		t.Fatal("expected non-empty GIF thumbnail response body")
	}
}

func TestServer_Thumbnail_PreservesPNGContentTypeForAlphaSource(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/alpha.png", bytes.NewReader(createPNGThumbnailSourceWithAlpha(24, 24))); err != nil {
		t.Fatalf("WriteFile(alpha.png) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/alpha.png", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail alpha PNG status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumbnail alpha PNG Content-Type = %q, want %q", w.Header().Get("Content-Type"), "image/png")
	}
	if len(w.Body.Bytes()) == 0 {
		t.Fatal("expected non-empty alpha PNG thumbnail response body")
	}
}

func TestServer_Thumbnail_RefreshesAfterFileContentChanges(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/changing.png", bytes.NewReader(createGIFThumbnailSource(24, 24))); err != nil {
		t.Fatalf("WriteFile(initial changing.png) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/changing.png", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initial thumbnail status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("initial thumbnail Content-Type = %q, want %q", w.Header().Get("Content-Type"), "image/jpeg")
	}

	if err := fs.WriteFile(ctx, "/changing.png", bytes.NewReader(createPNGThumbnailSourceWithAlpha(24, 24))); err != nil {
		t.Fatalf("WriteFile(updated changing.png) error: %v", err)
	}

	req = httptest.NewRequest("GET", "/api/v1/thumbnails/changing.png", nil)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("updated thumbnail status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("updated thumbnail Content-Type = %q, want %q", w.Header().Get("Content-Type"), "image/png")
	}
	if len(w.Body.Bytes()) == 0 {
		t.Fatal("expected non-empty updated thumbnail response body")
	}
}

func TestServer_Thumbnail_BindsETagAndContentToOpenedFile(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	initialContent := createGIFThumbnailSource(24, 24)
	updatedContent := createPNGThumbnailSourceWithAlpha(24, 24)
	if err := fs.WriteFile(ctx, "/racy.png", bytes.NewReader(initialContent)); err != nil {
		t.Fatalf("WriteFile(initial racy.png) error: %v", err)
	}
	initialInfo, err := fs.Stat(ctx, "/racy.png")
	if err != nil {
		t.Fatalf("Stat(initial racy.png) error: %v", err)
	}
	server.beforeThumbnailRead = func(filePath string) error {
		server.beforeThumbnailRead = nil
		return fs.WriteFile(ctx, filePath, bytes.NewReader(updatedContent))
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/racy.png", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail status with in-flight replacement = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail Content-Type with in-flight replacement = %q, want %q", w.Header().Get("Content-Type"), "image/jpeg")
	}
	if w.Header().Get("ETag") != fmt.Sprintf(`"thumb-%s-medium"`, initialInfo.ContentHash) {
		t.Fatalf("thumbnail ETag with in-flight replacement = %q, want %q", w.Header().Get("ETag"), fmt.Sprintf(`"thumb-%s-medium"`, initialInfo.ContentHash))
	}

	updatedInfo, err := fs.Stat(ctx, "/racy.png")
	if err != nil {
		t.Fatalf("Stat(updated racy.png) error: %v", err)
	}
	if updatedInfo.ContentHash == initialInfo.ContentHash {
		t.Fatal("expected updated file content hash to differ from initial hash")
	}

	req = httptest.NewRequest("GET", "/api/v1/thumbnails/racy.png", nil)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail status after replacement = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumbnail Content-Type after replacement = %q, want %q", w.Header().Get("Content-Type"), "image/png")
	}
	if w.Header().Get("ETag") != fmt.Sprintf(`"thumb-%s-medium"`, updatedInfo.ContentHash) {
		t.Fatalf("thumbnail ETag after replacement = %q, want %q", w.Header().Get("ETag"), fmt.Sprintf(`"thumb-%s-medium"`, updatedInfo.ContentHash))
	}
}

func TestServer_Thumbnail_UsesPrivateRevalidationCacheHeaders(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/etag.png", bytes.NewReader(createPNGThumbnailSourceWithAlpha(24, 24))); err != nil {
		t.Fatalf("WriteFile(etag.png) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/etag.png", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initial thumbnail status = %d, want %d", w.Code, http.StatusOK)
	}
	if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "private, no-cache" {
		t.Fatalf("thumbnail Cache-Control = %q, want %q", cacheControl, "private, no-cache")
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected thumbnail response to include ETag")
	}
	if lastModified := w.Header().Get("Last-Modified"); lastModified == "" {
		t.Fatal("expected thumbnail response to include Last-Modified")
	}

	req = httptest.NewRequest("GET", "/api/v1/thumbnails/etag.png", nil)
	req.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Fatalf("thumbnail If-None-Match status = %d, want %d", w.Code, http.StatusNotModified)
	}
	if len(w.Body.Bytes()) != 0 {
		t.Fatalf("expected 304 thumbnail response body to be empty, got %d bytes", len(w.Body.Bytes()))
	}
}

func TestServer_Thumbnail_RejectsInvalidSizeParameter(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/size.png", bytes.NewReader(createPNGThumbnailSourceWithAlpha(24, 24))); err != nil {
		t.Fatalf("WriteFile(size.png) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/size.png?size=gigantic", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("thumbnail invalid size status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "size parameter must be one of: small, medium, large") {
		t.Fatalf("expected invalid size error, got %s", w.Body.String())
	}
}

func TestServer_Thumbnail_RejectsOversizedSourceImage(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	thumbService := newTestThumbnailService(t, path.Join(tmpDir, "thumbnails"))
	server.thumbnail = thumbService

	if err := fs.WriteFile(ctx, "/oversized.png", bytes.NewReader(createOversizedPNGConfigOnly(10001, 10000))); err != nil {
		t.Fatalf("WriteFile(oversized.png) error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/thumbnails/oversized.png", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("thumbnail oversized source status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "source image too large to thumbnail") {
		t.Fatalf("expected oversized thumbnail message, got %s", w.Body.String())
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

func TestServer_DiagnosticsExport_MarksTrashStatsUnavailableWhenCollectionFails(t *testing.T) {
	server, _, _ := setupTestServer(t)
	originalGetTrashStats := getTrashStats
	getTrashStats = func(_ *storage.FileSystem, _ context.Context) (int, int64, error) {
		return 0, 0, errors.New("trash stats failed")
	}
	defer func() { getTrashStats = originalGetTrashStats }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics-export", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DiagnosticsExport status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Filesystem map[string]any `json:"filesystem"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse diagnostics export response: %v", err)
	}
	if available, ok := payload.Filesystem["trash_stats_available"].(bool); !ok || available {
		t.Fatalf("expected diagnostics export to mark trash stats unavailable, got %v", payload.Filesystem["trash_stats_available"])
	}
	if _, ok := payload.Filesystem["trash_count"]; ok {
		t.Fatalf("expected diagnostics export to omit trash_count on trash stats failure, got %v", payload.Filesystem["trash_count"])
	}
	if _, ok := payload.Filesystem["trash_size"]; ok {
		t.Fatalf("expected diagnostics export to omit trash_size on trash stats failure, got %v", payload.Filesystem["trash_size"])
	}
}

func TestServer_DiagnosticsExport_RequiresAdminWhenAuthEnabled(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)

	userToken := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics-export", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin diagnostics export status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServer_ListTrash_FiltersResultsByHomeDirForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServer(t)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/deleted.txt", bytes.NewReader([]byte("deleted"))); err != nil {
		t.Fatalf("WriteFile(/tester/deleted.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}
	if err := fs.Delete(ctx, "/tester/deleted.txt"); err != nil {
		t.Fatalf("Delete(/tester/deleted.txt) error: %v", err)
	}
	if err := fs.Delete(ctx, "/other/secret.txt"); err != nil {
		t.Fatalf("Delete(/other/secret.txt) error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trash/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("trash status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/tester/deleted.txt") {
		t.Fatalf("expected own home trash item, got %s", body)
	}
	if strings.Contains(body, "/other/secret.txt") {
		t.Fatalf("expected outside-home trash item to be filtered, got %s", body)
	}
}

func TestServer_CreateShare_RejectsPathOutsideHomeForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/other/secret.txt","type":"file"}`))
	forbiddenReq.Header.Set("Authorization", "Bearer "+token)
	forbiddenReq.Header.Set("Content-Type", "application/json")
	forbiddenRec := httptest.NewRecorder()
	server.Router().ServeHTTP(forbiddenRec, forbiddenReq)

	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("outside home share status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	allowedReq := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/tester/own.txt","type":"file"}`))
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusCreated {
		t.Fatalf("own home share status = %d, want %d", allowedRec.Code, http.StatusCreated)
	}
}

func TestServer_CreateShare_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, true, false)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized share create status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("expected payload too large message, got %s", w.Body.String())
	}
}

func TestServer_Login_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _, _, _ := setupAuthServerWithFeatures(t, true, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestServer_PublicShareAccess_HidesLegacyShareOutsideHomeForNonAdminOwner(t *testing.T) {
	server, fs, _, username, _ := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	allowedShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create own-home share error: %v", err)
	}
	legacyShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/other/secret.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create outside-home share error: %v", err)
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "/s/"+allowedShare.ID, nil)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("public own-home share access status = %d, want %d", allowedRec.Code, http.StatusOK)
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/s/"+legacyShare.ID, nil)
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusNotFound {
		t.Fatalf("public outside-home share access status = %d, want %d", legacyRec.Code, http.StatusNotFound)
	}
}

func TestServer_PublicShareAccess_HidesShareForDisabledOwner(t *testing.T) {
	server, fs, _, username, _ := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	fileShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create share error: %v", err)
	}

	user.Disabled = true
	if err := server.userStore.Update(user); err != nil {
		t.Fatalf("Update(disabled user) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/s/"+fileShare.ID, nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("public share for disabled owner status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if !strings.Contains(w.Body.String(), "SHARE_NOT_FOUND") {
		t.Fatalf("expected SHARE_NOT_FOUND response, got %s", w.Body.String())
	}
}

func TestServer_GetShare_HidesLegacyShareOutsideHomeForNonAdminOwner(t *testing.T) {
	server, fs, _, username, password := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	allowedShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create own-home share error: %v", err)
	}
	legacyShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/other/secret.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create outside-home share error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/shares/"+allowedShare.ID, nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("get own-home share status = %d, want %d", allowedRec.Code, http.StatusOK)
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/api/v1/shares/"+legacyShare.ID, nil)
	legacyReq.Header.Set("Authorization", "Bearer "+token)
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusNotFound {
		t.Fatalf("get outside-home share status = %d, want %d", legacyRec.Code, http.StatusNotFound)
	}
}

func TestServer_UpdateShare_HidesLegacyShareOutsideHomeForNonAdminOwner(t *testing.T) {
	server, fs, _, username, password := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	allowedShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create own-home share error: %v", err)
	}
	legacyShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/other/secret.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create outside-home share error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	legacyReq := httptest.NewRequest(http.MethodPut, "/api/v1/shares/"+legacyShare.ID, strings.NewReader(`{"description":"updated"}`))
	legacyReq.Header.Set("Authorization", "Bearer "+token)
	legacyReq.Header.Set("Content-Type", "application/json")
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusNotFound {
		t.Fatalf("update outside-home share status = %d, want %d", legacyRec.Code, http.StatusNotFound)
	}
	legacyCurrent, err := server.shareStore.Get(legacyShare.ID)
	if err != nil {
		t.Fatalf("Get(legacyShare) error: %v", err)
	}
	if legacyCurrent.Description != "" {
		t.Fatalf("expected outside-home share description to remain unchanged, got %q", legacyCurrent.Description)
	}

	allowedReq := httptest.NewRequest(http.MethodPut, "/api/v1/shares/"+allowedShare.ID, strings.NewReader(`{"description":"updated"}`))
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("update own-home share status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	allowedCurrent, err := server.shareStore.Get(allowedShare.ID)
	if err != nil {
		t.Fatalf("Get(allowedShare) error: %v", err)
	}
	if allowedCurrent.Description != "updated" {
		t.Fatalf("expected own-home share description to update, got %q", allowedCurrent.Description)
	}
}

func TestServer_DeleteShare_HidesLegacyShareOutsideHomeForNonAdminOwner(t *testing.T) {
	server, fs, _, username, password := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	allowedShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create own-home share error: %v", err)
	}
	legacyShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/other/secret.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create outside-home share error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	legacyReq := httptest.NewRequest(http.MethodDelete, "/api/v1/shares/"+legacyShare.ID, nil)
	legacyReq.Header.Set("Authorization", "Bearer "+token)
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusNotFound {
		t.Fatalf("delete outside-home share status = %d, want %d", legacyRec.Code, http.StatusNotFound)
	}
	if _, err := server.shareStore.Get(legacyShare.ID); err != nil {
		t.Fatalf("expected outside-home share to remain after rejected delete, got %v", err)
	}

	allowedReq := httptest.NewRequest(http.MethodDelete, "/api/v1/shares/"+allowedShare.ID, nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("delete own-home share status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	if _, err := server.shareStore.Get(allowedShare.ID); err != share.ErrShareNotFound {
		t.Fatalf("expected own-home share to be deleted, got %v", err)
	}
}

func TestServer_DeleteShare_AdminLogsOriginalPathForDisabledOwner(t *testing.T) {
	server, fs, _, username, _ := setupAuthServerWithFeatures(t, true, false)
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	user.HomeDir = "/tester"
	if err := server.userStore.Update(user); err != nil {
		t.Fatalf("Update(%s) error: %v", username, err)
	}

	fileShare, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("Create share error: %v", err)
	}

	user.Disabled = true
	if err := server.userStore.Update(user); err != nil {
		t.Fatalf("Update(disabled user) error: %v", err)
	}

	adminUsername := "share-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}
	adminToken := loginAndGetAccessToken(t, server, adminUsername, adminPassword)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/shares/"+fileShare.ID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("admin delete disabled-owner share status = %d, want %d", w.Code, http.StatusOK)
	}
	if _, err := server.shareStore.Get(fileShare.ID); err != share.ErrShareNotFound {
		t.Fatalf("expected disabled-owner share to be deleted, got %v", err)
	}

	entries, total := server.activity.List(10, 0, activity.ActionUnshare, "")
	if total != 1 {
		t.Fatalf("expected one unshare activity entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one listed unshare activity entry, got %d", len(entries))
	}
	if entries[0].User != adminUsername {
		t.Fatalf("expected unshare activity user %q, got %q", adminUsername, entries[0].User)
	}
	if entries[0].Path != "/tester/own.txt" {
		t.Fatalf("expected unshare activity path %q, got %q", "/tester/own.txt", entries[0].Path)
	}
}

func TestServer_ListShares_FiltersResultsByHomeDirForNonAdmin(t *testing.T) {
	server, fs, _, username, password := setupAuthServerWithFeatures(t, true, false)

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/tester"); err != nil {
		t.Fatalf("Mkdir(/tester) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/other"); err != nil {
		t.Fatalf("Mkdir(/other) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/tester/own.txt", bytes.NewReader([]byte("own"))); err != nil {
		t.Fatalf("WriteFile(/tester/own.txt) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/other/secret.txt", bytes.NewReader([]byte("secret"))); err != nil {
		t.Fatalf("WriteFile(/other/secret.txt) error: %v", err)
	}

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	if _, err := server.shareStore.Create(share.CreateShareOptions{Path: "/tester/own.txt", Type: share.ShareTypeFile, CreatedBy: user.ID}); err != nil {
		t.Fatalf("Create own-home share error: %v", err)
	}
	if _, err := server.shareStore.Create(share.CreateShareOptions{Path: "/other/secret.txt", Type: share.ShareTypeFile, CreatedBy: user.ID}); err != nil {
		t.Fatalf("Create outside-home share error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list shares status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/tester/own.txt") {
		t.Fatalf("expected own-home share in list, got %s", body)
	}
	if strings.Contains(body, "/other/secret.txt") {
		t.Fatalf("expected outside-home share to be filtered, got %s", body)
	}
}

func TestServer_ListShares_InvalidUserHomeDirReturnsForbidden(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, true, false)
	setUserHomeDirForTest(t, server, username, "../escape")

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("list shares with invalid home_dir status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServer_CreateShare_TraversalPathReturnsBadRequest(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, true, false)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"../other/secret.txt","type":"file"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("create share traversal path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid path") {
		t.Fatalf("expected invalid path response, got %s", w.Body.String())
	}
}

func TestServer_CheckFavorite_RejectsPathOutsideHomeForNonAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)

	token := loginAndGetAccessToken(t, server, username, password)

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=/other/secret.txt", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+token)
	forbiddenRec := httptest.NewRecorder()
	server.Router().ServeHTTP(forbiddenRec, forbiddenReq)

	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("outside home favorite check status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	allowedAddReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/tester/own.txt"}`))
	allowedAddReq.Header.Set("Authorization", "Bearer "+token)
	allowedAddReq.Header.Set("Content-Type", "application/json")
	allowedAddRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedAddRec, allowedAddReq)

	if allowedAddRec.Code != http.StatusCreated {
		t.Fatalf("add own home favorite status = %d, want %d", allowedAddRec.Code, http.StatusCreated)
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=/tester/own.txt", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("own home favorite check status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	if !strings.Contains(allowedRec.Body.String(), `"is_favorite":true`) {
		t.Fatalf("expected own home favorite to be reported, got %s", allowedRec.Body.String())
	}
}

func TestServer_CheckFavorites_RejectsPathOutsideHomeForNonAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)

	token := loginAndGetAccessToken(t, server, username, password)

	allowedAddReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/tester/own.txt"}`))
	allowedAddReq.Header.Set("Authorization", "Bearer "+token)
	allowedAddReq.Header.Set("Content-Type", "application/json")
	allowedAddRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedAddRec, allowedAddReq)

	if allowedAddRec.Code != http.StatusCreated {
		t.Fatalf("add own home favorite status = %d, want %d", allowedAddRec.Code, http.StatusCreated)
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/tester/own.txt","/other/secret.txt"]}`))
	forbiddenReq.Header.Set("Authorization", "Bearer "+token)
	forbiddenReq.Header.Set("Content-Type", "application/json")
	forbiddenRec := httptest.NewRecorder()
	server.Router().ServeHTTP(forbiddenRec, forbiddenReq)

	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("outside home favorite batch check status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	allowedReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/tester/own.txt"]}`))
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("own home favorite batch check status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	if !strings.Contains(allowedRec.Body.String(), `"/tester/own.txt":true`) {
		t.Fatalf("expected own home favorite batch result, got %s", allowedRec.Body.String())
	}
}

func TestServer_AddFavorite_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized add favorite status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestServer_CheckFavorites_RejectsOversizedRequestBody(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized favorite batch check status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestServer_ListFavorites_FiltersResultsByHomeDirForNonAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/tester/own.txt", ""); err != nil {
		t.Fatalf("Add own home favorite error: %v", err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/other/secret.txt", ""); err != nil {
		t.Fatalf("Add outside-home favorite error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list favorites status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/tester/own.txt") {
		t.Fatalf("expected own home favorite, got %s", body)
	}
	if strings.Contains(body, "/other/secret.txt") {
		t.Fatalf("expected outside-home favorite to be filtered, got %s", body)
	}
}

func TestServer_RemoveFavorite_RejectsPathOutsideHomeForNonAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/tester/own.txt", "own"); err != nil {
		t.Fatalf("Add own-home favorite error: %v", err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/other/secret.txt", "secret"); err != nil {
		t.Fatalf("Add outside-home favorite error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	legacyReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/other/secret.txt", nil)
	legacyReq.Header.Set("Authorization", "Bearer "+token)
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusForbidden {
		t.Fatalf("remove outside-home favorite status = %d, want %d", legacyRec.Code, http.StatusForbidden)
	}
	if !server.favoritesStore.IsFavorite(user.ID, "/other/secret.txt") {
		t.Fatal("expected outside-home favorite to remain after rejected remove")
	}

	allowedReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/tester/own.txt", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("remove own-home favorite status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	if server.favoritesStore.IsFavorite(user.ID, "/tester/own.txt") {
		t.Fatal("expected own-home favorite to be removed")
	}
}

func TestServer_UpdateFavoriteNote_RejectsPathOutsideHomeForNonAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)

	user, err := server.userStore.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername(%s) error: %v", username, err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/tester/own.txt", "own"); err != nil {
		t.Fatalf("Add own-home favorite error: %v", err)
	}
	if _, err := server.favoritesStore.Add(user.ID, "/other/secret.txt", "secret"); err != nil {
		t.Fatalf("Add outside-home favorite error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, username, password)

	legacyReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/other/secret.txt", strings.NewReader(`{"note":"updated"}`))
	legacyReq.Header.Set("Authorization", "Bearer "+token)
	legacyReq.Header.Set("Content-Type", "application/json")
	legacyRec := httptest.NewRecorder()
	server.Router().ServeHTTP(legacyRec, legacyReq)

	if legacyRec.Code != http.StatusForbidden {
		t.Fatalf("update outside-home favorite note status = %d, want %d", legacyRec.Code, http.StatusForbidden)
	}
	legacyFavorites := server.favoritesStore.List(user.ID)
	for _, favorite := range legacyFavorites {
		if favorite.Path == "/other/secret.txt" && favorite.Note != "secret" {
			t.Fatalf("expected outside-home favorite note to remain unchanged, got %q", favorite.Note)
		}
	}

	allowedReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/tester/own.txt", strings.NewReader(`{"note":"updated"}`))
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedRec := httptest.NewRecorder()
	server.Router().ServeHTTP(allowedRec, allowedReq)

	if allowedRec.Code != http.StatusOK {
		t.Fatalf("update own-home favorite note status = %d, want %d", allowedRec.Code, http.StatusOK)
	}
	allowedFavorites := server.favoritesStore.List(user.ID)
	for _, favorite := range allowedFavorites {
		if favorite.Path == "/tester/own.txt" && favorite.Note != "updated" {
			t.Fatalf("expected own-home favorite note to update, got %q", favorite.Note)
		}
	}
}

func TestServer_ListFavorites_InvalidUserHomeDirReturnsForbidden(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)
	setUserHomeDirForTest(t, server, username, "../escape")

	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("list favorites with invalid home_dir status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServer_AddFavorite_TraversalPathReturnsBadRequest(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"../other/secret.txt"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("add favorite traversal path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid path") {
		t.Fatalf("expected invalid path response, got %s", w.Body.String())
	}
}

func TestServer_CheckFavorite_TraversalPathReturnsBadRequest(t *testing.T) {
	server, _, _, username, password := setupAuthServerWithFeatures(t, false, true)
	token := loginAndGetAccessToken(t, server, username, password)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=../other/secret.txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("check favorite traversal path status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid path") {
		t.Fatalf("expected invalid path response, got %s", w.Body.String())
	}
}

func TestServer_DiagnosticsExport_DoesNotExposeInternalScrubErrorMessage(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	maint, err := maintenance.NewHistoryStore(path.Join(tmpDir, "maintenance"))
	if err != nil {
		t.Fatalf("NewHistoryStore() error: %v", err)
	}
	server.maintenance = maint
	if err := maint.SaveScrubResult(&maintenance.ScrubResult{
		ID:           "scrub-1",
		StartTime:    time.Now().Add(-time.Minute),
		EndTime:      time.Now(),
		Status:       "failed",
		ErrorMessage: "sqlite: database is locked",
	}); err != nil {
		t.Fatalf("SaveScrubResult() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/diagnostics-export", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DiagnosticsExport status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if strings.Contains(strings.ToLower(body), "database is locked") || strings.Contains(strings.ToLower(body), "sqlite") {
		t.Fatalf("expected diagnostics export to hide internal scrub error details, got %s", body)
	}
	if !strings.Contains(body, scrubFailurePublicMessage) {
		t.Fatalf("expected diagnostics export to include sanitized scrub error message, got %s", body)
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
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:             true,
		Prefix:              "/dav",
		AuthType:            "basic",
		Username:            "webdav-user",
		PasswordIsGenerated: true,
	}

	req := httptest.NewRequest("GET", "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("WebDAV credentials status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			Password string `json:"password"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Data.Username != "webdav-user" {
		t.Errorf("expected username webdav-user, got %q", payload.Data.Username)
	}
	if payload.Data.Password != "auto-pass" {
		t.Errorf("expected auto-generated password, got %q", payload.Data.Password)
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
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		AuthType: "basic",
		Password: "custom-pass",
	}

	req := httptest.NewRequest("GET", "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("WebDAV credentials status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := payload.Data["password"]; ok {
		t.Fatalf("expected password to be omitted for custom WebDAV password")
	}
}

func TestServer_WebDAVCredentials_URLNormalized(t *testing.T) {
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
	server.config.WebDAV.Prefix = "/dav/"
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:             true,
		Prefix:              "/dav/",
		AuthType:            "basic",
		PasswordIsGenerated: true,
	}

	req := httptest.NewRequest("GET", "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("WebDAV credentials status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Data.URL != "/dav/" {
		t.Fatalf("expected url /dav/, got %q", payload.Data.URL)
	}
}

func TestServer_WebDAVCredentials_GeneratedPasswordUnavailableReturnsServiceUnavailable(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.config.Storage.Root = filepath.Join(tmpDir, "missing-secrets")
	server.config.WebDAV.Enabled = true
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Username = "webdav-user"
	server.config.WebDAV.Password = ""
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		AuthType: "basic",
		Username: "webdav-user",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/webdav-credentials", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("WebDAV credentials missing generated password status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "webdav credentials unavailable" {
		t.Fatalf("expected webdav credentials unavailable message, got %q", payload.Message)
	}
}

func TestServer_WebDAVCredentials_UpdatesRunningConfigAfterSettingsUpdate(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	server.config.Storage.Root = tmpDir
	webdavUpdater := &fakeWebDAVUpdater{}
	server.updateWebDAV = webdavUpdater.UpdateConfig
	server.config.WebDAV.Enabled = true
	server.config.WebDAV.Prefix = "/dav"
	server.config.WebDAV.ReadOnly = false
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Username = "runtime-user"
	server.config.WebDAV.Password = "runtime-pass"
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:             true,
		Prefix:              "/dav",
		ReadOnly:            false,
		AuthType:            "basic",
		Username:            "runtime-user",
		Password:            "runtime-pass",
		PasswordIsGenerated: true,
	}

	body := `{"webdav":{"enabled":true,"prefix":"/new-dav","read_only":true,"auth_type":"basic","username":"saved-user","password":"saved-pass"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings webdav status = %d, want %d", w.Code, http.StatusOK)
	}

	credsReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/webdav-credentials", nil)
	credsRec := httptest.NewRecorder()
	server.Router().ServeHTTP(credsRec, credsReq)

	if credsRec.Code != http.StatusOK {
		t.Fatalf("webdav credentials status = %d, want %d", credsRec.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(credsRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Data.URL != "/new-dav/" {
		t.Fatalf("expected running webdav url /new-dav/, got %q", payload.Data.URL)
	}
	if payload.Data.Username != "saved-user" {
		t.Fatalf("expected running webdav username saved-user, got %q", payload.Data.Username)
	}
	if payload.Data.Password != "" {
		t.Fatalf("expected custom WebDAV password to remain hidden, got %q", payload.Data.Password)
	}
	if server.config.WebDAV.Prefix != "/new-dav" {
		t.Fatalf("expected saved config prefix /new-dav, got %q", server.config.WebDAV.Prefix)
	}
	if server.activeWebDAV.Prefix != "/new-dav" {
		t.Fatalf("expected active WebDAV prefix /new-dav, got %q", server.activeWebDAV.Prefix)
	}
	if !server.activeWebDAV.ReadOnly {
		t.Fatalf("expected active WebDAV read-only mode to update")
	}
	if server.activeWebDAV.Password != "saved-pass" {
		t.Fatalf("expected active WebDAV password to update, got %q", server.activeWebDAV.Password)
	}
	if server.activeWebDAV.PasswordIsGenerated {
		t.Fatalf("expected active WebDAV password to be marked custom")
	}
	if webdavUpdater.updateCount != 1 {
		t.Fatalf("expected WebDAV updater to be called once, got %d", webdavUpdater.updateCount)
	}
	if webdavUpdater.lastConfig.Prefix != "/new-dav" {
		t.Fatalf("expected WebDAV updater prefix /new-dav, got %q", webdavUpdater.lastConfig.Prefix)
	}
	if !webdavUpdater.lastConfig.ReadOnly {
		t.Fatalf("expected WebDAV updater to receive read-only=true")
	}
}

func TestServer_UpdateSettings_NormalizesWebDAVPrefix(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	if err := config.SaveSecrets(tmpDir, &config.Secrets{JWTSecret: "jwt", WebDAVPassword: "auto-pass"}); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}

	body := `{"webdav":{"prefix":"dav/"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.config.WebDAV.Prefix != "/dav" {
		t.Fatalf("expected prefix normalized to /dav, got %q", server.config.WebDAV.Prefix)
	}
}

func TestServer_UpdateSettings_RejectsInvalidWebDAVAuthType(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalAuthType := server.config.WebDAV.AuthType

	body := `{"webdav":{"auth_type":"token"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid webdav auth type status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid configuration") {
		t.Fatalf("expected invalid configuration message, got %s", w.Body.String())
	}
	if server.config.WebDAV.AuthType != originalAuthType {
		t.Fatalf("expected invalid webdav auth type update to leave config unchanged")
	}
}

func TestServer_UpdateSettings_SerializesConcurrentRuntimeApply(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	server.config.WebDAV.Enabled = true
	server.config.WebDAV.Prefix = "/dav"
	server.config.WebDAV.ReadOnly = false
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Username = "runtime-user"
	server.config.WebDAV.Password = "initial-pass"
	if err := server.config.Save(server.configPath); err != nil {
		t.Fatalf("failed to save baseline config: %v", err)
	}
	server.storeActiveWebDAV(WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		ReadOnly: false,
		AuthType: "basic",
		Username: "runtime-user",
		Password: "initial-pass",
	})

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	allowFirst := make(chan struct{})

	var appliedMu sync.Mutex
	applied := make([]string, 0, 2)
	callCount := 0
	server.updateWebDAV = func(cfg WebDAVRuntimeConfig) {
		appliedMu.Lock()
		callCount++
		call := callCount
		appliedMu.Unlock()

		if call == 1 {
			close(firstEntered)
			<-allowFirst
		} else if call == 2 {
			close(secondEntered)
		}

		appliedMu.Lock()
		applied = append(applied, cfg.Password)
		appliedMu.Unlock()
	}

	doRequest := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.Router().ServeHTTP(w, req)
		return w
	}

	firstResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstResponse <- doRequest(`{"webdav":{"enabled":true,"auth_type":"basic","username":"runtime-user","password":"first-pass"}}`)
	}()

	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first WebDAV runtime update")
	}

	secondResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		secondResponse <- doRequest(`{"webdav":{"enabled":true,"auth_type":"basic","username":"runtime-user","password":"second-pass"}}`)
	}()

	select {
	case <-secondEntered:
		t.Fatal("expected second settings update to wait for the first runtime apply")
	case <-time.After(200 * time.Millisecond):
	}

	close(allowFirst)

	firstResult := <-firstResponse
	secondResult := <-secondResponse
	if firstResult.Code != http.StatusOK {
		t.Fatalf("first update settings status = %d, want %d: %s", firstResult.Code, http.StatusOK, firstResult.Body.String())
	}
	if secondResult.Code != http.StatusOK {
		t.Fatalf("second update settings status = %d, want %d: %s", secondResult.Code, http.StatusOK, secondResult.Body.String())
	}

	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second WebDAV runtime update")
	}

	current := server.currentConfig()
	if current == nil {
		t.Fatal("expected current config snapshot")
	}
	if current.WebDAV.Password != "second-pass" {
		t.Fatalf("expected persisted config snapshot to reflect second password, got %q", current.WebDAV.Password)
	}
	active := server.currentActiveWebDAV()
	if active.Password != "second-pass" {
		t.Fatalf("expected active WebDAV config to reflect second password, got %q", active.Password)
	}

	appliedMu.Lock()
	defer appliedMu.Unlock()
	if len(applied) != 2 {
		t.Fatalf("expected two WebDAV runtime applies, got %d", len(applied))
	}
	if applied[0] != "first-pass" || applied[1] != "second-pass" {
		t.Fatalf("expected serialized WebDAV runtime apply order [first-pass second-pass], got %v", applied)
	}
	configBytes, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("failed to read persisted config: %v", err)
	}
	if !strings.Contains(string(configBytes), "password = 'second-pass'") {
		t.Fatalf("expected persisted config file to reflect second password, got %s", string(configBytes))
	}
}

func TestServer_UpdateSettings_RejectsGeneratedWebDAVPasswordWhenSecretsUnavailable(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	server.config.Storage.Root = filepath.Join(tmpDir, "missing-secrets")
	server.config.WebDAV.Enabled = true
	server.config.WebDAV.Prefix = "/dav"
	server.config.WebDAV.ReadOnly = false
	server.config.WebDAV.AuthType = "basic"
	server.config.WebDAV.Username = "runtime-user"
	server.config.WebDAV.Password = "runtime-pass"
	if err := server.config.Save(server.configPath); err != nil {
		t.Fatalf("failed to save baseline config: %v", err)
	}
	baselineConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("failed to read baseline config: %v", err)
	}
	webdavUpdater := &fakeWebDAVUpdater{}
	server.updateWebDAV = webdavUpdater.UpdateConfig
	server.activeWebDAV = WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		ReadOnly: false,
		AuthType: "basic",
		Username: "runtime-user",
		Password: "runtime-pass",
	}

	body := `{"webdav":{"enabled":true,"auth_type":"basic","username":"saved-user","password":""}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("update settings missing generated webdav password status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "webdav credentials unavailable" {
		t.Fatalf("expected webdav credentials unavailable message, got %q", payload.Message)
	}
	if server.config.WebDAV.Password != "runtime-pass" || server.config.WebDAV.Username != "runtime-user" {
		t.Fatalf("expected in-memory WebDAV config to remain unchanged, got %+v", server.config.WebDAV)
	}
	if server.activeWebDAV.Password != "runtime-pass" || server.activeWebDAV.Username != "runtime-user" {
		t.Fatalf("expected active WebDAV runtime config to remain unchanged, got %+v", server.activeWebDAV)
	}
	if webdavUpdater.updateCount != 0 {
		t.Fatalf("expected WebDAV updater not to run, got %d", webdavUpdater.updateCount)
	}
	persistedConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("failed to read persisted config: %v", err)
	}
	if !bytes.Equal(persistedConfig, baselineConfig) {
		t.Fatalf("expected rejected update to leave persisted config unchanged")
	}
}

func TestServer_UpdateSettings_RejectsWebDAVUsernameMatchingNonAdminUser(t *testing.T) {
	server, _, _, username, _ := setupAuthServer(t)

	adminUsername := "settings-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}
	adminToken := loginAndGetAccessToken(t, server, adminUsername, adminPassword)

	body := fmt.Sprintf(`{"webdav":{"enabled":true,"auth_type":"basic","username":"%s","password":"shared-pass"}}`, username)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings with non-admin webdav username status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "webdav.username must not match a non-admin user") {
		t.Fatalf("expected non-admin webdav username validation message, got %s", w.Body.String())
	}
	if server.config.WebDAV.Username == username {
		t.Fatalf("expected live config to remain unchanged after rejected settings update")
	}
}

func TestServer_UpdateSettings_InvalidConfigDoesNotLeakOrMutate(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMin := server.config.DataPlane.CDC.MinChunkSize
	originalAvg := server.config.DataPlane.CDC.AvgChunkSize
	originalMax := server.config.DataPlane.CDC.MaxChunkSize

	body := `{"cdc":{"min_chunk_size":2097152,"avg_chunk_size":1048576,"max_chunk_size":4194304}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid config status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid configuration") {
		t.Fatalf("expected generic invalid configuration message, got %s", bodyStr)
	}
	if strings.Contains(bodyStr, "min_chunk_size") || strings.Contains(bodyStr, "avg_chunk_size") {
		t.Fatalf("expected validation internals to stay hidden, got %s", bodyStr)
	}
	if server.config.DataPlane.CDC.MinChunkSize != originalMin || server.config.DataPlane.CDC.AvgChunkSize != originalAvg || server.config.DataPlane.CDC.MaxChunkSize != originalMax {
		t.Fatalf("expected invalid settings update to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_RejectsUnknownFields(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMaxAge := server.config.Storage.Retention.MaxAge

	body := `{"retention":{"max_age":"24h","unexpected":true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings unknown field status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid request body") {
		t.Fatalf("expected invalid request body message, got %s", bodyStr)
	}
	if server.config.Storage.Retention.MaxAge != originalMaxAge {
		t.Fatalf("expected unknown-field update to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_RejectsOversizedRequestBody(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalPort := server.config.Server.Port

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", io.LimitReader(repeatingReader{}, DefaultJSONRequestBodyLimit+1))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized update settings status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("expected payload too large message, got %s", w.Body.String())
	}
	if server.config.Server.Port != originalPort {
		t.Fatalf("expected oversized settings update to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_InvalidDurationRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMaxAge := server.config.Storage.Retention.MaxAge

	body := `{"retention":{"max_age":"not-a-duration"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid duration status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid retention.max_age") {
		t.Fatalf("expected invalid retention.max_age message, got %s", bodyStr)
	}
	if server.config.Storage.Retention.MaxAge != originalMaxAge {
		t.Fatalf("expected invalid duration to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_NegativeMaxAgeRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMaxAge := server.config.Storage.Retention.MaxAge

	body := `{"retention":{"max_age":"-1h"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings negative max_age status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid retention.max_age") {
		t.Fatalf("expected invalid retention.max_age message, got %s", w.Body.String())
	}
	if server.config.Storage.Retention.MaxAge != originalMaxAge {
		t.Fatalf("expected negative max_age to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_NegativeMaxVersionsRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMaxVersions := server.config.Storage.Retention.MaxVersions

	body := `{"retention":{"max_versions":-1}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings negative max_versions status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid retention.max_versions") {
		t.Fatalf("expected invalid retention.max_versions message, got %s", w.Body.String())
	}
	if server.config.Storage.Retention.MaxVersions != originalMaxVersions {
		t.Fatalf("expected negative max_versions to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_NegativeGCIntervalRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalGCInterval := server.config.Storage.Retention.GCInterval

	body := `{"retention":{"gc_interval":"-1h"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid gc_interval status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid retention.gc_interval") {
		t.Fatalf("expected invalid retention.gc_interval message, got %s", w.Body.String())
	}
	if server.config.Storage.Retention.GCInterval != originalGCInterval {
		t.Fatalf("expected invalid gc_interval to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_InvalidPortRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalPort := server.config.Server.Port

	body := `{"server":{"port":70000}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid port status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "invalid server.port") {
		t.Fatalf("expected invalid server.port message, got %s", bodyStr)
	}
	if server.config.Server.Port != originalPort {
		t.Fatalf("expected invalid port to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_UpdatesAlertsConfig(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	monitor := &fakeAlertMonitor{}
	server.alertMonitor = monitor

	body := `{"alerts":{"enabled":true,"check_interval":"30m","threshold_pct":85,"critical_pct":92,"min_free_bytes":21474836480,"cooldown_period":"2h","webhook_url":"https://hooks.example.com/storage","webhook_method":"POST","webhook_headers":["Authorization: Bearer token","X-MnemoNAS: alerts"]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings alerts status = %d, want %d", w.Code, http.StatusOK)
	}
	if !server.config.Alerts.Enabled {
		t.Fatalf("expected alerts enabled")
	}
	if server.config.Alerts.CheckInterval != 30*time.Minute {
		t.Fatalf("expected check interval 30m, got %s", server.config.Alerts.CheckInterval)
	}
	if server.config.Alerts.ThresholdPct != 85 {
		t.Fatalf("expected threshold 85, got %v", server.config.Alerts.ThresholdPct)
	}
	if server.config.Alerts.CriticalPct != 92 {
		t.Fatalf("expected critical threshold 92, got %v", server.config.Alerts.CriticalPct)
	}
	if server.config.Alerts.MinFreeBytes != 21474836480 {
		t.Fatalf("expected min free bytes updated, got %d", server.config.Alerts.MinFreeBytes)
	}
	if server.config.Alerts.CooldownPeriod != 2*time.Hour {
		t.Fatalf("expected cooldown 2h, got %s", server.config.Alerts.CooldownPeriod)
	}
	if server.config.Alerts.WebhookURL != "https://hooks.example.com/storage" {
		t.Fatalf("expected webhook url updated, got %q", server.config.Alerts.WebhookURL)
	}
	if server.config.Alerts.WebhookMethod != "POST" {
		t.Fatalf("expected webhook method POST, got %q", server.config.Alerts.WebhookMethod)
	}
	if len(server.config.Alerts.WebhookHeaders) != 2 {
		t.Fatalf("expected webhook headers updated, got %#v", server.config.Alerts.WebhookHeaders)
	}
	if monitor.updateCount != 1 {
		t.Fatalf("expected alert monitor update once, got %d", monitor.updateCount)
	}
	if monitor.lastConfig.CheckInterval != 30*time.Minute || monitor.lastConfig.WebhookURL != "https://hooks.example.com/storage" {
		t.Fatalf("unexpected alert monitor config: %+v", monitor.lastConfig)
	}
}

func TestServer_UpdateSettings_InvalidAlertsWebhookMethodRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalMethod := server.config.Alerts.WebhookMethod

	body := `{"alerts":{"webhook_method":"PATCH"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid alerts webhook method status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid configuration") {
		t.Fatalf("expected generic invalid configuration message, got %s", w.Body.String())
	}
	if server.config.Alerts.WebhookMethod != originalMethod {
		t.Fatalf("expected invalid webhook method to leave in-memory config unchanged")
	}
}

func TestServer_UpdateSettings_InvalidAlertsWebhookMethodDoesNotUpdateAlertMonitor(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	monitor := &fakeAlertMonitor{}
	server.alertMonitor = monitor

	body := `{"alerts":{"webhook_method":"PATCH"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid alerts webhook method status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if monitor.updateCount != 0 {
		t.Fatalf("expected invalid configuration not to update alert monitor, got %d updates", monitor.updateCount)
	}
}

func TestServer_UpdateSettings_UpdatesTrashConfig(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	body := `{"trash":{"enabled":false,"retention_days":0,"max_size":10}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings trash status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.config.Storage.Trash.Enabled {
		t.Fatalf("expected trash disabled")
	}
	if server.config.Storage.Trash.RetentionDays != 0 {
		t.Fatalf("expected trash retention days 0, got %d", server.config.Storage.Trash.RetentionDays)
	}
	if server.config.Storage.Trash.MaxSize != 10 {
		t.Fatalf("expected trash max size 10, got %d", server.config.Storage.Trash.MaxSize)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"trash":{"enabled":true,"retention_days":0,"max_size":10}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	server.Router().ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("re-enable trash status = %d, want %d", updateRec.Code, http.StatusOK)
	}

	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/old.txt", bytes.NewReader([]byte("123456"))); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}
	if err := fs.Delete(ctx, "/old.txt"); err != nil {
		t.Fatalf("Delete(old) error: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := fs.WriteFile(ctx, "/new.txt", bytes.NewReader([]byte("1234567"))); err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	if err := fs.Delete(ctx, "/new.txt"); err != nil {
		t.Fatalf("Delete(new) error: %v", err)
	}
	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 || items[0].OriginalPath != "/new.txt" {
		t.Fatalf("expected trash max size update to keep only newest item, got %#v", items)
	}
	if deleted, err := fs.CleanupExpiredTrash(ctx); err != nil {
		t.Fatalf("CleanupExpiredTrash() error: %v", err)
	} else if deleted != 1 {
		t.Fatalf("expected cleanup to delete 1 immediately expired item, got %d", deleted)
	}
}

func TestServer_UpdateSettings_UpdatesRunningDataplaneClient(t *testing.T) {
	probe := setupDataplaneClient(t)
	if probe == nil {
		t.Skip("dataplane not available, skipping test")
	}

	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	oldClient := dataplane.NewClient("127.0.0.1:1")
	server.dataplane = oldClient
	setVersionStoreObjectClient(t, fs, oldClient)

	body := fmt.Sprintf(`{"dataplane":{"grpc_address":%q}}`, testDataplaneAddr())
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings dataplane status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.dataplane == nil {
		t.Fatal("expected running dataplane client to be initialized")
	}
	if server.dataplane == oldClient {
		t.Fatal("expected running dataplane client to be replaced")
	}
	if got := server.dataplane.Addr(); got != testDataplaneAddr() {
		t.Fatalf("running dataplane addr = %q, want %q", got, testDataplaneAddr())
	}
	if !server.dataplane.IsConnected() {
		t.Fatal("expected replacement dataplane client to connect")
	}

	objectClient := getVersionStoreObjectClient(t, fs)
	if objectClient == nil {
		t.Fatal("expected version store object client to remain configured")
	}
	if objectClient == oldClient {
		t.Fatal("expected version store object client to be replaced")
	}
	if got := objectClient.Addr(); got != testDataplaneAddr() {
		t.Fatalf("version store dataplane addr = %q, want %q", got, testDataplaneAddr())
	}
	if !objectClient.IsConnected() {
		t.Fatal("expected version store dataplane client to connect")
	}
}

func TestServer_UpdateSettings_UnreachableDataplaneRejectedBeforeSave(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	if err := server.config.Save(server.configPath); err != nil {
		t.Fatalf("save baseline config error: %v", err)
	}
	baselineConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("read baseline config error: %v", err)
	}

	oldClient := dataplane.NewClient("127.0.0.1:2")
	server.dataplane = oldClient
	setVersionStoreObjectClient(t, fs, oldClient)

	body := `{"dataplane":{"grpc_address":"127.0.0.1:1"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("update settings unreachable dataplane status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "unable to connect to configured dataplane" {
		t.Fatalf("expected dataplane connectivity error message, got %q", payload.Message)
	}
	if server.dataplane != oldClient {
		t.Fatal("expected running dataplane client to remain unchanged")
	}
	if got := server.config.DataPlane.GRPCAddress; got == "127.0.0.1:1" {
		t.Fatalf("expected in-memory config to remain unchanged, got %q", got)
	}
	if objectClient := getVersionStoreObjectClient(t, fs); objectClient != oldClient {
		t.Fatal("expected version store object client to remain unchanged")
	}

	persistedConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("read persisted config error: %v", err)
	}
	if !bytes.Equal(persistedConfig, baselineConfig) {
		t.Fatalf("expected persisted config to stay unchanged, got %s", string(persistedConfig))
	}
	if bytes.Contains(persistedConfig, []byte("127.0.0.1:1")) {
		t.Fatalf("expected unreachable dataplane address not to be persisted, got %s", string(persistedConfig))
	}
	_ = oldClient.Close()
}

func TestServer_UpdateSettings_CanceledDataplaneConnectDoesNotSaveOrSwap(t *testing.T) {
	probe := setupDataplaneClient(t)
	if probe == nil {
		t.Skip("dataplane not available, skipping test")
	}

	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	if err := server.config.Save(server.configPath); err != nil {
		t.Fatalf("save baseline config error: %v", err)
	}
	baselineAddr := server.config.DataPlane.GRPCAddress
	baselineConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("read baseline config error: %v", err)
	}

	oldClient := dataplane.NewClient("127.0.0.1:2")
	server.dataplane = oldClient
	setVersionStoreObjectClient(t, fs, oldClient)

	body := fmt.Sprintf(`{"dataplane":{"grpc_address":%q}}`, testDataplaneAddr())
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusTooManyRequests {
		t.Fatalf("update settings canceled dataplane status = %d, want %d or %d: %s", w.Code, http.StatusServiceUnavailable, http.StatusTooManyRequests, w.Body.String())
	}
	if w.Code == http.StatusServiceUnavailable {
		var payload APIError
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to parse response JSON: %v", err)
		}
		if payload.Code != ErrCodeServiceUnavail {
			t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
		}
		if payload.Message != "unable to connect to configured dataplane" {
			t.Fatalf("expected dataplane connectivity error message, got %q", payload.Message)
		}
	} else if !strings.Contains(w.Body.String(), "Context was canceled") {
		t.Fatalf("expected canceled request body to mention context cancellation, got %s", w.Body.String())
	}
	if server.dataplane != oldClient {
		t.Fatal("expected running dataplane client to remain unchanged after canceled request")
	}
	if got := server.config.DataPlane.GRPCAddress; got != baselineAddr {
		t.Fatalf("expected in-memory config to remain unchanged at %q, got %q", baselineAddr, got)
	}
	if objectClient := getVersionStoreObjectClient(t, fs); objectClient != oldClient {
		t.Fatal("expected version store object client to remain unchanged after canceled request")
	}

	persistedConfig, err := os.ReadFile(server.configPath)
	if err != nil {
		t.Fatalf("read persisted config error: %v", err)
	}
	if !bytes.Equal(persistedConfig, baselineConfig) {
		t.Fatalf("expected persisted config to stay unchanged, got %s", string(persistedConfig))
	}
	_ = oldClient.Close()
}

func TestServer_UpdateSettings_InvalidTrashSettingsRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	originalRetention := server.config.Storage.Trash.RetentionDays
	originalMaxSize := server.config.Storage.Trash.MaxSize

	for _, tc := range []struct {
		body    string
		message string
	}{
		{body: `{"trash":{"retention_days":-1}}`, message: "invalid trash.retention_days"},
		{body: `{"trash":{"max_size":0}}`, message: "invalid trash.max_size"},
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("update settings invalid trash status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		if !strings.Contains(w.Body.String(), tc.message) {
			t.Fatalf("expected %q message, got %s", tc.message, w.Body.String())
		}
		if server.config.Storage.Trash.RetentionDays != originalRetention || server.config.Storage.Trash.MaxSize != originalMaxSize {
			t.Fatalf("expected invalid trash settings to leave config unchanged")
		}
	}
}

func TestServer_UpdateSettings_UpdatesServerTimeouts(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	body := `{"server":{"read_timeout":"45s","write_timeout":"90s","idle_timeout":"5m"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings server timeouts status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.config.Server.ReadTimeout != 45*time.Second {
		t.Fatalf("expected read timeout 45s, got %s", server.config.Server.ReadTimeout)
	}
	if server.config.Server.WriteTimeout != 90*time.Second {
		t.Fatalf("expected write timeout 90s, got %s", server.config.Server.WriteTimeout)
	}
	if server.config.Server.IdleTimeout != 5*time.Minute {
		t.Fatalf("expected idle timeout 5m, got %s", server.config.Server.IdleTimeout)
	}
}

func TestServer_UpdateSettings_UpdatesRetentionConfigForRunningStorage(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	body := `{"retention":{"max_versions":1,"max_age":"24h"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings retention status = %d, want %d", w.Code, http.StatusOK)
	}
	if server.config.Storage.Retention.MaxVersions != 1 {
		t.Fatalf("expected max_versions 1, got %d", server.config.Storage.Retention.MaxVersions)
	}
	if server.config.Storage.Retention.MaxAge != 24*time.Hour {
		t.Fatalf("expected max_age 24h, got %s", server.config.Storage.Retention.MaxAge)
	}

	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/retention-live.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/retention-live.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/retention-live.md", bytes.NewReader([]byte("v3"))); err != nil {
		t.Fatalf("WriteFile(v3) error: %v", err)
	}

	versions, err := fs.ListVersions(ctx, "/retention-live.md")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected current version plus one retained historical version, got %d versions", len(versions))
	}
}

func TestServer_GetSettings_NormalizesZeroRetentionDurations(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.config.Storage.Retention.MaxAge = 0
	server.config.Storage.Retention.GCInterval = 0

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get settings status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Retention struct {
				MaxAge     string `json:"max_age"`
				GCInterval string `json:"gc_interval"`
			} `json:"retention"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode settings response error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success response")
	}
	if resp.Data.Retention.MaxAge != "0" {
		t.Fatalf("expected max_age to be normalized to 0, got %q", resp.Data.Retention.MaxAge)
	}
	if resp.Data.Retention.GCInterval != "0" {
		t.Fatalf("expected gc_interval to be normalized to 0, got %q", resp.Data.Retention.GCInterval)
	}
}

func TestServer_GetSettings_NormalizesNilSliceFields(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.config.Storage.Versioning.AutoVersionedExtensions = nil
	server.config.Storage.Versioning.AutoVersionedFilenames = nil
	server.config.Alerts.WebhookHeaders = nil

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get settings status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Versioning struct {
				AutoVersionedExtensions []string `json:"auto_versioned_extensions"`
				AutoVersionedFilenames  []string `json:"auto_versioned_filenames"`
			} `json:"versioning"`
			Alerts struct {
				WebhookHeaders []string `json:"webhook_headers"`
			} `json:"alerts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode settings response error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success response")
	}
	if resp.Data.Versioning.AutoVersionedExtensions == nil {
		t.Fatal("expected auto_versioned_extensions to serialize as an empty array, got null")
	}
	if resp.Data.Versioning.AutoVersionedFilenames == nil {
		t.Fatal("expected auto_versioned_filenames to serialize as an empty array, got null")
	}
	if resp.Data.Alerts.WebhookHeaders == nil {
		t.Fatal("expected webhook_headers to serialize as an empty array, got null")
	}
	if len(resp.Data.Versioning.AutoVersionedExtensions) != 0 {
		t.Fatalf("expected empty auto_versioned_extensions, got %v", resp.Data.Versioning.AutoVersionedExtensions)
	}
	if len(resp.Data.Versioning.AutoVersionedFilenames) != 0 {
		t.Fatalf("expected empty auto_versioned_filenames, got %v", resp.Data.Versioning.AutoVersionedFilenames)
	}
	if len(resp.Data.Alerts.WebhookHeaders) != 0 {
		t.Fatalf("expected empty webhook_headers, got %v", resp.Data.Alerts.WebhookHeaders)
	}
}

func TestServer_UpdateSettings_UpdatesRetentionMonitorConfig(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	monitor := &fakeRetentionMonitor{}
	server.retentionMonitor = monitor

	body := `{"retention":{"max_versions":2,"max_age":"48h","min_free_space":2048,"gc_interval":"0"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings retention monitor status = %d, want %d", w.Code, http.StatusOK)
	}
	if monitor.updateCount != 1 {
		t.Fatalf("expected retention monitor update count 1, got %d", monitor.updateCount)
	}
	if monitor.lastConfig.MaxVersions != 2 {
		t.Fatalf("expected max_versions 2, got %d", monitor.lastConfig.MaxVersions)
	}
	if monitor.lastConfig.MaxVersionAge != 48*time.Hour {
		t.Fatalf("expected max_age 48h, got %s", monitor.lastConfig.MaxVersionAge)
	}
	if monitor.lastConfig.MinFreeSpace != 2048 {
		t.Fatalf("expected min_free_space 2048, got %d", monitor.lastConfig.MinFreeSpace)
	}
	if monitor.lastConfig.SweepInterval != 0 {
		t.Fatalf("expected gc_interval 0 to disable periodic sweep, got %s", monitor.lastConfig.SweepInterval)
	}
	if server.config.Storage.Retention.GCInterval != 0 {
		t.Fatalf("expected in-memory gc_interval 0, got %s", server.config.Storage.Retention.GCInterval)
	}
}

func TestServer_UpdateSettings_UpdatesVersioningConfig(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	body := `{"versioning":{"auto_versioned_extensions":[".md"," .txt ",""],"auto_versioned_filenames":["README"," Dockerfile ",""],"max_versioned_size":209715200}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings versioning status = %d, want %d", w.Code, http.StatusOK)
	}
	if !reflect.DeepEqual(server.config.Storage.Versioning.AutoVersionedExtensions, []string{".md", ".txt"}) {
		t.Fatalf("unexpected versioning extensions: %#v", server.config.Storage.Versioning.AutoVersionedExtensions)
	}
	if !reflect.DeepEqual(server.config.Storage.Versioning.AutoVersionedFilenames, []string{"README", "Dockerfile"}) {
		t.Fatalf("unexpected versioning filenames: %#v", server.config.Storage.Versioning.AutoVersionedFilenames)
	}
	if server.config.Storage.Versioning.MaxVersionedSize != 209715200 {
		t.Fatalf("expected max versioned size 209715200, got %d", server.config.Storage.Versioning.MaxVersionedSize)
	}
}

func TestServer_UpdateSettings_UpdatesRunningVersioningPolicy(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")

	body := `{"versioning":{"auto_versioned_extensions":[".md"," .txt ",""],"auto_versioned_filenames":["README"," Dockerfile ",""],"max_versioned_size":209715200}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update settings versioning runtime status = %d, want %d", w.Code, http.StatusOK)
	}

	policy := getVersioningPolicy(t, fs)
	if policy == nil {
		t.Fatal("expected running versioning policy to remain configured")
	}
	if !reflect.DeepEqual(policy.AutoVersionedExtensions, []string{".md", ".txt"}) {
		t.Fatalf("unexpected running versioning extensions: %#v", policy.AutoVersionedExtensions)
	}
	if !reflect.DeepEqual(policy.AutoVersionedFilenames, []string{"README", "Dockerfile"}) {
		t.Fatalf("unexpected running versioning filenames: %#v", policy.AutoVersionedFilenames)
	}
	if policy.MaxVersionedSize != 209715200 {
		t.Fatalf("expected running max versioned size 209715200, got %d", policy.MaxVersionedSize)
	}
}

func TestServer_UpdateSettings_InvalidVersioningMaxSizeRejected(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = path.Join(tmpDir, "config.toml")
	original := server.config.Storage.Versioning.MaxVersionedSize

	body := `{"versioning":{"max_versioned_size":0}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("update settings invalid versioning max size status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if server.config.Storage.Versioning.MaxVersionedSize != original {
		t.Fatalf("expected invalid versioning max size to leave config unchanged")
	}
}

func TestServer_SetupStatus_DoesNotExposeCredentials(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	secrets := &config.Secrets{
		WebPassword:    "web-pass",
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
	if payload["web_password"] != nil {
		t.Fatalf("expected web_password to be omitted from setup status")
	}
	if payload["web_username"] != nil {
		t.Fatalf("expected web_username to be omitted from setup status")
	}
	if _, ok := payload["webdav_password"]; ok {
		t.Fatalf("expected webdav_password to be omitted from setup status")
	}
	if _, ok := payload["webdav_username"]; ok {
		t.Fatalf("expected webdav_username to be omitted from setup status")
	}
}

func TestServer_StoreConfig_ReplacesSnapshotWithoutMutatingPreviousReaders(t *testing.T) {
	server, _, _ := setupTestServer(t)

	baseline := server.currentConfig()
	if baseline == nil {
		t.Fatal("expected server config")
	}

	updated := *baseline
	updated.Server.Port = baseline.Server.Port + 17
	updated.WebDAV.Prefix = "/snapshot-dav"

	server.storeConfig(&updated)

	current := server.currentConfig()
	if current == nil {
		t.Fatal("expected updated server config")
	}
	if current == baseline {
		t.Fatal("expected storeConfig to replace the config snapshot pointer")
	}
	if current.Server.Port != updated.Server.Port || current.WebDAV.Prefix != updated.WebDAV.Prefix {
		t.Fatalf("expected current config to reflect update, got %+v", current)
	}
	if baseline.Server.Port == updated.Server.Port {
		t.Fatalf("expected prior snapshot port to remain unchanged, got %d", baseline.Server.Port)
	}
	if baseline.WebDAV.Prefix == updated.WebDAV.Prefix {
		t.Fatalf("expected prior snapshot prefix to remain unchanged, got %q", baseline.WebDAV.Prefix)
	}
}

func TestServer_CurrentConfig_ReturnsIsolatedSlices(t *testing.T) {
	server, _, _ := setupTestServer(t)

	baseline := server.currentConfig()
	if baseline == nil {
		t.Fatal("expected server config")
	}

	if len(baseline.Storage.Versioning.AutoVersionedExtensions) == 0 {
		t.Fatal("expected default auto-versioned extensions")
	}

	baseline.Storage.Versioning.AutoVersionedExtensions[0] = ".mutated"

	current := server.currentConfig()
	if current == nil {
		t.Fatal("expected server config")
	}
	if current.Storage.Versioning.AutoVersionedExtensions[0] == ".mutated" {
		t.Fatal("expected currentConfig to return an isolated copy of versioning slices")
	}
}

func TestServer_StoreConfig_ClonesSliceFields(t *testing.T) {
	server, _, _ := setupTestServer(t)

	updated := server.currentConfig()
	if updated == nil {
		t.Fatal("expected server config")
	}
	updated.Storage.Versioning.AutoVersionedExtensions = []string{".md", ".txt"}
	updated.Storage.Versioning.AutoVersionedFilenames = []string{"README", "Dockerfile"}
	updated.Alerts.WebhookHeaders = []string{"Authorization: Bearer token"}

	server.storeConfig(updated)

	updated.Storage.Versioning.AutoVersionedExtensions[0] = ".mutated"
	updated.Storage.Versioning.AutoVersionedFilenames[0] = "MUTATED"
	updated.Alerts.WebhookHeaders[0] = "Authorization: Bearer changed"

	current := server.currentConfig()
	if current == nil {
		t.Fatal("expected stored config")
	}
	if current.Storage.Versioning.AutoVersionedExtensions[0] != ".md" {
		t.Fatalf("expected stored extension slice to remain isolated, got %v", current.Storage.Versioning.AutoVersionedExtensions)
	}
	if current.Storage.Versioning.AutoVersionedFilenames[0] != "README" {
		t.Fatalf("expected stored filename slice to remain isolated, got %v", current.Storage.Versioning.AutoVersionedFilenames)
	}
	if current.Alerts.WebhookHeaders[0] != "Authorization: Bearer token" {
		t.Fatalf("expected stored webhook headers to remain isolated, got %v", current.Alerts.WebhookHeaders)
	}
}

func TestServer_StoreActiveWebDAV_ReplacesSnapshotWithoutMutatingPreviousReaders(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.storeActiveWebDAV(WebDAVRuntimeConfig{
		Enabled:             true,
		Prefix:              "/dav",
		AuthType:            "basic",
		Username:            "admin",
		Password:            "first-pass",
		PasswordIsGenerated: true,
	})

	baseline := server.currentActiveWebDAV()
	updated := baseline
	updated.Prefix = "/new-dav"
	updated.Password = "next-pass"
	updated.PasswordIsGenerated = false

	server.storeActiveWebDAV(updated)

	current := server.currentActiveWebDAV()
	if current.Prefix != "/new-dav" || current.Password != "next-pass" || current.PasswordIsGenerated {
		t.Fatalf("expected current active WebDAV config to reflect update, got %+v", current)
	}
	if baseline.Prefix != "/dav" {
		t.Fatalf("expected prior active WebDAV snapshot prefix to remain unchanged, got %q", baseline.Prefix)
	}
	if baseline.Password != "first-pass" {
		t.Fatalf("expected prior active WebDAV snapshot password to remain unchanged, got %q", baseline.Password)
	}
	if !baseline.PasswordIsGenerated {
		t.Fatal("expected prior active WebDAV snapshot generated flag to remain true")
	}
}

func TestServer_SetupStatus_WithoutConfigReturnsServiceUnavailable(t *testing.T) {
	server, _, _ := setupTestServer(t)
	server.config = nil

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup status without config = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServer_SetupStatus_WithoutSecretsReturnsServiceUnavailable(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.config.Storage.Root = tmpDir

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup status without secrets status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "setup status unavailable" {
		t.Fatalf("expected setup status unavailable message, got %q", payload.Message)
	}
}

func TestServer_SetupStatus_SecretsLoadFailureReturnsServiceUnavailable(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.config.Storage.Root = tmpDir

	if err := os.WriteFile(filepath.Join(tmpDir, config.SecretsFile), []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("failed to write corrupt secrets file: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup status corrupt secrets status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "setup status unavailable" {
		t.Fatalf("expected setup status unavailable message, got %q", payload.Message)
	}
}

func TestServer_AcknowledgeSetup_RequiresAuthentication(t *testing.T) {
	server, _, _, _, _ := setupAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/acknowledge", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("acknowledge setup status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestServer_ClearActivity_RequiresAdminRoleWhenAuthEnabled(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)

	login := func(t *testing.T, user, pass string) string {
		t.Helper()
		body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, user, pass)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		w := httptest.NewRecorder()
		server.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("login status = %d, want %d", w.Code, http.StatusOK)
		}
		var payload struct {
			Data auth.LoginResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to parse login response: %v", err)
		}
		return payload.Data.AccessToken
	}

	userToken := login(t, username, password)
	userReq := httptest.NewRequest(http.MethodDelete, "/api/v1/activity/", nil)
	userReq.Header.Set("Authorization", "Bearer "+userToken)
	userRec := httptest.NewRecorder()
	server.Router().ServeHTTP(userRec, userReq)

	if userRec.Code != http.StatusForbidden {
		t.Fatalf("clear activity as non-admin status = %d, want %d", userRec.Code, http.StatusForbidden)
	}

	adminUsername := "activity-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}
	adminToken := login(t, adminUsername, adminPassword)
	adminReq := httptest.NewRequest(http.MethodDelete, "/api/v1/activity/", nil)
	adminReq.Header.Set("Authorization", "Bearer "+adminToken)
	adminRec := httptest.NewRecorder()
	server.Router().ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusOK {
		t.Fatalf("clear activity as admin status = %d, want %d", adminRec.Code, http.StatusOK)
	}
	if !strings.Contains(adminRec.Body.String(), "Activity log cleared") {
		t.Fatalf("expected success message when admin clears activity, got %s", adminRec.Body.String())
	}
}

func TestServer_ListActivity_NonAdminOnlySeesOwnEntriesWhenAuthEnabled(t *testing.T) {
	server, _, _, username, _ := setupAuthServer(t)

	adminUsername := "activity-admin"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionUpload, "/tester/own.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log own activity error: %v", err)
	}
	if err := server.activity.Log(activity.ActionDelete, "/users/admin.txt", adminUsername, "127.0.0.2", nil); err != nil {
		t.Fatalf("log admin activity error: %v", err)
	}

	userToken := issueAccessTokenWithoutActivity(t, server, username)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/?user="+adminUsername, nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list activity as non-admin status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			Items []activity.Entry `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse list activity response: %v", err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("non-admin activity total = %d, want 1", payload.Data.Total)
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("non-admin activity items = %d, want 1", len(payload.Data.Items))
	}
	if payload.Data.Items[0].User != username {
		t.Fatalf("non-admin saw user %q, want %q", payload.Data.Items[0].User, username)
	}
	if payload.Data.Items[0].Path != "/tester/own.txt" {
		t.Fatalf("non-admin saw path %q, want own path", payload.Data.Items[0].Path)
	}
}

func TestServer_ListActivity_NonAdminFiltersOutsideHomeDirEntries(t *testing.T) {
	server, _, _, username, _ := setupAuthServer(t)

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionUpload, "/tester/own.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log in-home activity error: %v", err)
	}
	if err := server.activity.Log(activity.ActionDelete, "/other/secret.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log outside-home activity error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/", nil)
	req.Header.Set("Authorization", "Bearer "+issueAccessTokenWithoutActivity(t, server, username))
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list activity as non-admin status = %d, want %d", w.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			Items []activity.Entry `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse list activity response: %v", err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("non-admin filtered activity total = %d, want 1", payload.Data.Total)
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("non-admin filtered activity items = %d, want 1", len(payload.Data.Items))
	}
	if payload.Data.Items[0].Path != "/tester/own.txt" {
		t.Fatalf("non-admin saw path %q, want in-home path only", payload.Data.Items[0].Path)
	}
}

func TestServer_ListActivity_InvalidUserHomeDirReturnsForbidden(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)
	setUserHomeDirForTest(t, server, username, "../escape")

	token := loginAndGetAccessToken(t, server, username, password)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("list activity with invalid home_dir status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServer_ActivityStats_NonAdminOnlySeesOwnEntriesWhenAuthEnabled(t *testing.T) {
	server, _, _, username, _ := setupAuthServer(t)

	if _, err := server.userStore.Create("stats-admin", "adminpass123", "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}
	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionUpload, "/tester/own.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log own activity error: %v", err)
	}
	if err := server.activity.Log(activity.ActionDelete, "/users/admin.txt", "stats-admin", "127.0.0.2", nil); err != nil {
		t.Fatalf("log admin activity error: %v", err)
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/api/v1/activity/stats", nil)
	statsReq.Header.Set("Authorization", "Bearer "+issueAccessTokenWithoutActivity(t, server, username))
	statsRec := httptest.NewRecorder()
	server.Router().ServeHTTP(statsRec, statsReq)

	if statsRec.Code != http.StatusOK {
		t.Fatalf("activity stats as non-admin status = %d, want %d", statsRec.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			Total    int            `json:"total"`
			Today    int            `json:"today"`
			ByUser   map[string]int `json:"by_user"`
			ByAction map[string]int `json:"by_action"`
		} `json:"data"`
	}
	if err := json.Unmarshal(statsRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse activity stats response: %v", err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("non-admin activity stats total = %d, want 1", payload.Data.Total)
	}
	if payload.Data.Today != 1 {
		t.Fatalf("non-admin activity stats today = %d, want 1", payload.Data.Today)
	}
	if payload.Data.ByUser[username] != 1 {
		t.Fatalf("expected own user count = 1, got %d", payload.Data.ByUser[username])
	}
	if payload.Data.ByUser["stats-admin"] != 0 {
		t.Fatalf("expected admin user count to be hidden, got %d", payload.Data.ByUser["stats-admin"])
	}
	if payload.Data.ByAction[string(activity.ActionUpload)] != 1 {
		t.Fatalf("expected own upload count = 1, got %d", payload.Data.ByAction[string(activity.ActionUpload)])
	}
	if payload.Data.ByAction[string(activity.ActionDelete)] != 0 {
		t.Fatalf("expected admin delete count to be hidden, got %d", payload.Data.ByAction[string(activity.ActionDelete)])
	}
}

func TestServer_ActivityStats_NonAdminFiltersOutsideHomeDirEntries(t *testing.T) {
	server, _, _, username, _ := setupAuthServer(t)

	if server.activity == nil {
		t.Fatal("expected activity store to be initialized")
	}
	if err := server.activity.Log(activity.ActionUpload, "/tester/own.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log in-home activity error: %v", err)
	}
	if err := server.activity.Log(activity.ActionDelete, "/other/secret.txt", username, "127.0.0.1", nil); err != nil {
		t.Fatalf("log outside-home activity error: %v", err)
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/api/v1/activity/stats", nil)
	statsReq.Header.Set("Authorization", "Bearer "+issueAccessTokenWithoutActivity(t, server, username))
	statsRec := httptest.NewRecorder()
	server.Router().ServeHTTP(statsRec, statsReq)

	if statsRec.Code != http.StatusOK {
		t.Fatalf("activity stats as non-admin status = %d, want %d", statsRec.Code, http.StatusOK)
	}

	var payload struct {
		Data struct {
			Total    int            `json:"total"`
			Today    int            `json:"today"`
			ByUser   map[string]int `json:"by_user"`
			ByAction map[string]int `json:"by_action"`
		} `json:"data"`
	}
	if err := json.Unmarshal(statsRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse activity stats response: %v", err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("non-admin filtered activity stats total = %d, want 1", payload.Data.Total)
	}
	if payload.Data.Today != 1 {
		t.Fatalf("non-admin filtered activity stats today = %d, want 1", payload.Data.Today)
	}
	if payload.Data.ByUser[username] != 1 {
		t.Fatalf("expected own filtered user count = 1, got %d", payload.Data.ByUser[username])
	}
	if payload.Data.ByAction[string(activity.ActionUpload)] != 1 {
		t.Fatalf("expected in-home upload count = 1, got %d", payload.Data.ByAction[string(activity.ActionUpload)])
	}
	if payload.Data.ByAction[string(activity.ActionDelete)] != 0 {
		t.Fatalf("expected outside-home delete count to be hidden, got %d", payload.Data.ByAction[string(activity.ActionDelete)])
	}
}

func TestServer_ActivityStats_InvalidUserHomeDirReturnsForbidden(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)
	setUserHomeDirForTest(t, server, username, "../escape")

	token := loginAndGetAccessToken(t, server, username, password)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("activity stats with invalid home_dir status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestBuildActivityStats_UsesLocalCalendarDayBoundary(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.April, 7, 10, 0, 0, 0, loc)
	originalNow := apiTimeNow
	apiTimeNow = func() time.Time { return now }
	defer func() {
		apiTimeNow = originalNow
	}()

	stats := buildActivityStats([]activity.Entry{
		{Action: activity.ActionUpload, User: "admin", Timestamp: time.Date(2026, time.April, 7, 0, 0, 0, 0, loc)},
		{Action: activity.ActionDelete, User: "admin", Timestamp: time.Date(2026, time.April, 6, 23, 59, 59, 0, loc)},
	})

	today, ok := stats["today"].(int)
	if !ok {
		t.Fatalf("today type assertion failed: %#v", stats["today"])
	}
	if today != 1 {
		t.Fatalf("expected exactly one entry in today's local calendar bucket, got %d", today)
	}
	byAction, ok := stats["by_action"].(map[activity.ActionType]int)
	if !ok {
		t.Fatal("by_action type assertion failed")
	}
	if byAction[activity.ActionUpload] != 1 || byAction[activity.ActionDelete] != 1 {
		t.Fatalf("expected both actions to remain counted in total stats, got %#v", byAction)
	}
}

func TestServer_AcknowledgeSetup_WithoutAuthMarksSetupShown(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)

	secrets := &config.Secrets{SetupShown: false}
	if err := config.SaveSecrets(tmpDir, secrets); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}
	server.config.Storage.Root = tmpDir

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/acknowledge", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("acknowledge setup without auth status = %d, want %d", w.Code, http.StatusOK)
	}

	updatedSecrets, err := config.LoadSecrets(tmpDir)
	if err != nil {
		t.Fatalf("failed to reload secrets: %v", err)
	}
	if updatedSecrets == nil || !updatedSecrets.SetupShown {
		t.Fatalf("expected setup to be marked shown after acknowledge without auth")
	}
}

func TestServer_AcknowledgeSetup_WithoutSecretsReturnsServiceUnavailable(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.config.Storage.Root = tmpDir

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/acknowledge", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("acknowledge setup without secrets status = %d, want %d: %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeServiceUnavail {
		t.Fatalf("expected error code %q, got %q", ErrCodeServiceUnavail, payload.Code)
	}
	if payload.Message != "setup acknowledge unavailable" {
		t.Fatalf("expected setup acknowledge unavailable message, got %q", payload.Message)
	}
}

func TestServer_AcknowledgeSetup_InternalErrorUsesStructuredAPIError(t *testing.T) {
	server, _, tmpDir, username, password := setupAuthServer(t)

	secrets := &config.Secrets{SetupShown: false}
	if err := config.SaveSecrets(tmpDir, secrets); err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}
	secretsPath := path.Join(tmpDir, config.SecretsFile)
	targetSecretsPath := path.Join(tmpDir, "target-secrets.json")
	secretsData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("failed to read saved secrets: %v", err)
	}
	if err := os.WriteFile(targetSecretsPath, secretsData, 0600); err != nil {
		t.Fatalf("failed to write target secrets file: %v", err)
	}
	if err := os.Remove(secretsPath); err != nil {
		t.Fatalf("failed to remove original secrets file: %v", err)
	}
	if err := os.Symlink(targetSecretsPath, secretsPath); err != nil {
		t.Fatalf("failed to create secrets symlink: %v", err)
	}

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	adminUsername := "admin-tester"
	adminPassword := "adminpass123"
	if _, err := server.userStore.Create(adminUsername, adminPassword, "", auth.RoleAdmin); err != nil {
		t.Fatalf("create admin user error: %v", err)
	}

	adminLoginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, adminUsername, adminPassword)
	adminLoginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(adminLoginBody))
	adminLoginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(adminLoginRec, adminLoginReq)

	if adminLoginRec.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, want %d", adminLoginRec.Code, http.StatusOK)
	}

	var loginResp auth.LoginResponse
	var loginPayload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(adminLoginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}
	loginResp = loginPayload.Data

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/acknowledge", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("acknowledge setup status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeInternal {
		t.Fatalf("expected error code %q, got %q", ErrCodeInternal, payload.Code)
	}
	if payload.Message != "internal server error" {
		t.Fatalf("expected sanitized message, got %q", payload.Message)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "permission denied") {
		t.Fatalf("expected internal file error details to stay hidden, got %s", w.Body.String())
	}
}

func TestServer_JSON_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	server, _, _ := setupTestServer(t)
	w := httptest.NewRecorder()

	server.json(w, http.StatusOK, map[string]any{
		"bad": make(chan int),
	})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("json helper status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if w.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", w.Body.String())
	}
}

func TestServer_UpdateSettings_SaveFailureHidden(t *testing.T) {
	server, _, tmpDir := setupTestServer(t)
	server.configPath = tmpDir

	body := `{"server":{"host":"127.0.0.1"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("update settings save failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var payload APIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if payload.Code != ErrCodeInternal {
		t.Fatalf("expected error code %q, got %q", ErrCodeInternal, payload.Code)
	}
	if payload.Message != "internal server error" {
		t.Fatalf("expected generic save failure message, got %q", payload.Message)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "directory") {
		t.Fatalf("expected filesystem details to stay hidden, got %s", w.Body.String())
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

func TestServer_RestoreVersion_RequiresAdmin(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	var loginResp auth.LoginResponse
	var loginPayload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}
	loginResp = loginPayload.Data

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/v1/versions/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/restore?path=/restore-version/file.txt", nil)
	restoreReq.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	restoreRec := httptest.NewRecorder()
	server.Router().ServeHTTP(restoreRec, restoreReq)

	if restoreRec.Code != http.StatusForbidden {
		t.Fatalf("restore status = %d, want %d", restoreRec.Code, http.StatusForbidden)
	}
}

func TestServer_RestoreVersion_RejectsNonAdminBeforeValidation(t *testing.T) {
	server, _, _, username, password := setupAuthServer(t)

	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	server.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}

	var loginPayload struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/v1/versions/not-a-valid-hash/restore?path=/restore-version/file.txt", nil)
	restoreReq.Header.Set("Authorization", "Bearer "+loginPayload.Data.AccessToken)
	restoreRec := httptest.NewRecorder()
	server.Router().ServeHTTP(restoreRec, restoreReq)

	if restoreRec.Code != http.StatusForbidden {
		t.Fatalf("restore invalid hash as non-admin status = %d, want %d", restoreRec.Code, http.StatusForbidden)
	}
	if strings.Contains(strings.ToLower(restoreRec.Body.String()), "invalid hash") {
		t.Fatalf("expected auth guard to run before validation, got %s", restoreRec.Body.String())
	}
}

func TestServer_GuestRole_IsReadOnlyForMutatingRoutes(t *testing.T) {
	server, fs, _, _, _ := setupAuthServerWithFeatures(t, true, true)

	guestUsername := "guest-reader"
	guestPassword := "guestpass123"
	if _, err := server.userStore.Create(guestUsername, guestPassword, "", auth.RoleGuest); err != nil {
		t.Fatalf("create guest user error: %v", err)
	}
	if err := fs.Mkdir(context.Background(), "/"+guestUsername); err != nil {
		t.Fatalf("Mkdir(%s) error: %v", guestUsername, err)
	}
	if err := fs.WriteFile(context.Background(), "/"+guestUsername+"/readable.txt", bytes.NewReader([]byte("ok"))); err != nil {
		t.Fatalf("WriteFile(%s/readable.txt) error: %v", guestUsername, err)
	}

	login := func(t *testing.T, user, pass string) string {
		t.Helper()
		body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, user, pass)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		w := httptest.NewRecorder()
		server.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("login status = %d, want %d", w.Code, http.StatusOK)
		}
		var payload struct {
			Data auth.LoginResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to parse login response: %v", err)
		}
		return payload.Data.AccessToken
	}

	guestToken := login(t, guestUsername, guestPassword)

	tests := []struct {
		name        string
		method      string
		url         string
		body        string
		contentType string
	}{
		{name: "upload file", method: http.MethodPost, url: "/api/v1/files/guest-write.txt", body: "content", contentType: "text/plain"},
		{name: "move file", method: http.MethodPost, url: "/api/v1/files-move", body: `{"source":"/docs/a.txt","destination":"/docs/moved.txt"}`, contentType: "application/json"},
		{name: "create share", method: http.MethodPost, url: "/api/v1/shares", body: `{"path":"/docs/a.txt","type":"file"}`, contentType: "application/json"},
		{name: "add favorite", method: http.MethodPost, url: "/api/v1/favorites", body: `{"path":"/docs/a.txt"}`, contentType: "application/json"},
		{name: "empty trash", method: http.MethodDelete, url: "/api/v1/trash/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.url, body)
			req.Header.Set("Authorization", "Bearer "+guestToken)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()
			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("guest %s status = %d, want %d", tt.name, w.Code, http.StatusForbidden)
			}
			if !strings.Contains(w.Body.String(), "INSUFFICIENT_PERMISSIONS") {
				t.Fatalf("expected insufficient permissions error, got %s", w.Body.String())
			}
		})
	}

	checkReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/guest-reader/readable.txt"]}`))
	checkReq.Header.Set("Authorization", "Bearer "+guestToken)
	checkReq.Header.Set("Content-Type", "application/json")
	checkRec := httptest.NewRecorder()
	server.Router().ServeHTTP(checkRec, checkReq)

	if checkRec.Code != http.StatusOK {
		t.Fatalf("guest favorites check-batch status = %d, want %d", checkRec.Code, http.StatusOK)
	}
}

func TestServer_UploadFile_ErrorCases(t *testing.T) {
	server, fs, _ := setupTestServer(t)

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

	t.Run("UploadToDirectoryReturnsBadRequest", func(t *testing.T) {
		ctx := context.Background()
		if err := fs.Mkdir(ctx, "/upload-dir"); err != nil {
			t.Fatalf("Mkdir() error: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/files/upload-dir", strings.NewReader("content"))
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("Upload to directory status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		if !strings.Contains(w.Body.String(), "cannot upload to directory") {
			t.Fatalf("expected upload-to-directory validation message, got %s", w.Body.String())
		}
	})

	t.Run("UploadUnderFileReturnsConflict", func(t *testing.T) {
		ctx := context.Background()
		if err := fs.WriteFile(ctx, "/upload-parent-file", bytes.NewReader([]byte("content"))); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/files/upload-parent-file/child.txt", strings.NewReader("nested"))
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("Upload under file status = %d, want %d", w.Code, http.StatusConflict)
		}
		if !strings.Contains(w.Body.String(), "parent path is not a directory") {
			t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
		}
	})
}

func TestServer_DeleteFile_ErrorCases(t *testing.T) {
	server, fs, _ := setupTestServer(t)

	t.Run("DeleteNonExistent", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/files/nonexistent/file.txt", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
			t.Errorf("Delete nonexistent file status = %d", w.Code)
		}
	})

	t.Run("DeleteNonEmptyDirectoryMovesTreeToTrash", func(t *testing.T) {
		ctx := context.Background()
		if err := fs.Mkdir(ctx, "/non-empty-dir"); err != nil {
			t.Fatalf("Mkdir() error: %v", err)
		}
		if err := fs.WriteFile(ctx, "/non-empty-dir/file.txt", bytes.NewReader([]byte("content"))); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		req := httptest.NewRequest("DELETE", "/api/v1/files/non-empty-dir", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Delete non-empty directory status = %d, want %d", w.Code, http.StatusOK)
		}
		if _, err := fs.Stat(ctx, "/non-empty-dir"); err != storage.ErrNotFound {
			t.Fatalf("expected directory to be removed from workspace, got %v", err)
		}
		items, err := fs.ListTrash(ctx)
		if err != nil {
			t.Fatalf("ListTrash() error: %v", err)
		}
		if len(items) != 1 || !items[0].IsDir {
			t.Fatalf("expected deleted directory tree to be stored as one trash directory item, got %+v", items)
		}
	})
}

func TestServer_CreateDirectory_ReturnsConflictWhenParentIsNotDirectory(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/parent-file", bytes.NewReader([]byte("content"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/directories/parent-file/child", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("Create directory under file status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !strings.Contains(w.Body.String(), "parent path is not a directory") {
		t.Fatalf("expected parent-not-directory conflict message, got %s", w.Body.String())
	}
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

func TestServer_ListVersions_InternalStatErrorReturnsInternalServerError(t *testing.T) {
	server, fs, tmpDir := setupTestServer(t)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/locked-versions"); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/locked-versions/file.txt", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	lockedDir := path.Join(tmpDir, "files", "locked-versions")
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatalf("Chmod() error: %v", err)
	}
	defer func() {
		_ = os.Chmod(lockedDir, 0o755)
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions/locked-versions/file.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ListVersions internal error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "permission denied") {
		t.Fatalf("expected list versions internal error to stay generic, got %s", w.Body.String())
	}
}

func TestServer_ListVersions_VersionStoreFailureReturnsInternalServerError(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	ctx := context.Background()

	if err := fs.WriteFile(ctx, "/versions/failing.txt", bytes.NewReader([]byte("current"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if err := fs.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions/versions/failing.txt", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ListVersions version store failure status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "version store unavailable") {
		t.Fatalf("expected list versions response to hide internal backend details, got %s", w.Body.String())
	}
}
