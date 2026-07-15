package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/seanbao/mnemonas/internal/api"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	quotareservation "github.com/seanbao/mnemonas/internal/quota"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/webdav"
)

func TestResolveLogOutput_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	_, closer, _, err := resolveLogOutput(filepath.Join(linkedParent, "mnemonas.log"))
	if closer != nil {
		_ = closer.Close()
	}
	if !errors.Is(err, errLogOutputSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestWithSecurityHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	withSecurityHeaders(next).ServeHTTP(w, req)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := w.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=(), payment=()" {
		t.Fatalf("Permissions-Policy = %q, want browser capability restrictions", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q, want frame-ancestors none", got)
	}
}

func TestWithSecurityHeadersDoesNotOverrideStricterCSP(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/file.html", nil)

	withSecurityHeaders(next).ServeHTTP(w, req)

	if got := w.Header().Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
		t.Fatalf("Content-Security-Policy = %q, want stricter downstream policy", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q, want downstream SAMEORIGIN policy", got)
	}
}

func TestResolveLogOutput_DoesNotCreateLogFileThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	logPath := filepath.Join(linkedParent, "mnemonas.log")
	_, closer, _, err := resolveLogOutput(logPath)
	if closer != nil {
		_ = closer.Close()
	}
	if !errors.Is(err, errLogOutputSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "mnemonas.log")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("log file created through symlink parent, stat error = %v", statErr)
	}
}

func TestValidateConfigOnly_SucceedsWithoutCreatingStorageDirs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	storageRoot := filepath.Join(tmpDir, "custom-root")

	if err := os.WriteFile(configPath, []byte("[storage]\nroot = \""+storageRoot+"\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	var output bytes.Buffer
	if err := validateConfigOnly(configPath, &output); err != nil {
		t.Fatalf("validateConfigOnly() error: %v", err)
	}
	if !strings.Contains(output.String(), configPath) {
		t.Fatalf("expected output to mention config path, got %q", output.String())
	}
	if _, err := os.Stat(storageRoot); !os.IsNotExist(err) {
		t.Fatalf("expected validateConfigOnly() to avoid creating storage root, stat err = %v", err)
	}
}

func TestValidateConfigOnly_ReturnsErrorForMissingExplicitConfigPath(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.toml")

	err := validateConfigOnly(missingPath, io.Discard)
	if err == nil {
		t.Fatal("expected missing explicit config path to return error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing config error, got %v", err)
	}
}

func TestValidateConfigOnly_PrintsSecurityWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	storageRoot := filepath.Join(tmpDir, "storage")
	configData := []byte(`
[server]
host = "0.0.0.0"

[storage]
root = "` + storageRoot + `"

[dataplane]
grpc_address = "0.0.0.0:9090"

[webdav]
enabled = true
auth_type = "none"

	[auth]
	enabled = false

	[security]
	allow_unsafe_no_auth = true
	`)

	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	var output bytes.Buffer
	if err := validateConfigOnly(configPath, &output); err != nil {
		t.Fatalf("validateConfigOnly() error: %v", err)
	}
	text := output.String()
	for _, want := range []string{
		"warning: auth.enabled=false while server.host listens beyond loopback",
		"warning: webdav.auth_type=none while server.host listens beyond loopback",
		"warning: dataplane.grpc_address listens beyond loopback",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected output to contain %q, got %q", want, text)
		}
	}
}

func TestConfigWarnings_AllowsLoopbackOnlyDevConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Host = "127.0.0.1"
	cfg.Auth.Enabled = false
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "none"
	cfg.DataPlane.GRPCAddress = "[::1]:9090"

	if warnings := configWarnings(cfg); len(warnings) != 0 {
		t.Fatalf("expected no loopback-only warnings, got %v", warnings)
	}
}

func TestConfigWarnings_ReportsSMBPreviewRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.SMB.Enabled = true
	cfg.SMB.Shares = []config.SMBShareConfig{{
		Name:         "homes",
		Path:         "/",
		AllowedRoles: []string{"admin"},
	}}

	warnings := configWarnings(cfg)
	var found bool
	for _, warning := range warnings {
		if strings.Contains(warning, "does not start an SMB/Samba listener") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SMB preview warning, got %v", warnings)
	}
}

func TestHostFromTCPAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{name: "ipv4 with port", address: "127.0.0.1:9090", want: "127.0.0.1"},
		{name: "ipv6 with port", address: "[::1]:9090", want: "::1"},
		{name: "bare ipv6", address: "::1", want: "::1"},
		{name: "host with port", address: "localhost:9090", want: "localhost"},
		{name: "empty host port", address: ":9090", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostFromTCPAddress(tt.address); got != tt.want {
				t.Fatalf("hostFromTCPAddress(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

func TestListensBeyondLoopback(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "", want: true},
		{host: "*", want: true},
		{host: "localhost", want: false},
		{host: "127.0.0.1", want: false},
		{host: "127.10.20.30", want: false},
		{host: "::1", want: false},
		{host: "0:0:0:0:0:0:0:1", want: false},
		{host: "::ffff:127.0.0.1", want: false},
		{host: "0.0.0.0", want: true},
		{host: "::", want: true},
		{host: "192.168.1.10", want: true},
		{host: "mnemonas.local", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := listensBeyondLoopback(tt.host); got != tt.want {
				t.Fatalf("listensBeyondLoopback(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestLoadConfig_ReturnsErrorForMissingExplicitConfigPath(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.toml")

	_, resolvedPath, err := loadConfig(missingPath)
	if err == nil {
		t.Fatal("expected loadConfig() to fail for missing explicit config path")
	}
	if resolvedPath != missingPath {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, missingPath)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing config error, got %v", err)
	}
}

func TestLoadConfig_UsesBuiltInDefaultsWhenNoConfigFileExists(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	cfg, resolvedPath, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig(empty) error: %v", err)
	}
	if resolvedPath != "" {
		t.Fatalf("resolvedPath = %q, want empty", resolvedPath)
	}
	if cfg == nil {
		t.Fatal("expected non-nil default config")
	}
	if cfg.Server.Port != config.Default().Server.Port {
		t.Fatalf("server port = %d, want default %d", cfg.Server.Port, config.Default().Server.Port)
	}
}

func TestAcquireRuntimeAuthStateLockSkipsDisabledAuthentication(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.Enabled = false
	cfg.Auth.UsersFile = filepath.Join(t.TempDir(), "missing", "users.json")

	lock, err := acquireRuntimeAuthStateLock(cfg)
	if err != nil {
		t.Fatalf("acquireRuntimeAuthStateLock() error: %v", err)
	}
	if lock != nil {
		t.Fatal("acquireRuntimeAuthStateLock() returned a lock with authentication disabled")
	}
	if _, err := os.Stat(filepath.Dir(cfg.Auth.UsersFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled authentication created state directory, stat error = %v", err)
	}
}

func TestAcquireRuntimeAuthStateLockExcludesAnotherWriter(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.Enabled = true
	cfg.Auth.UsersFile = filepath.Join(privateRecoveryAuthStateTestDir(t), "auth", "users.json")

	lock, err := acquireRuntimeAuthStateLock(cfg)
	if err != nil {
		t.Fatalf("acquireRuntimeAuthStateLock() error: %v", err)
	}
	if lock == nil {
		t.Fatal("acquireRuntimeAuthStateLock() returned nil lock with authentication enabled")
	}
	defer lock.Close()

	contender, err := auth.AcquireStateLock(cfg.Auth.UsersFile)
	if contender != nil {
		_ = contender.Close()
		t.Fatal("second authentication writer acquired the runtime state lock")
	}
	if !errors.Is(err, auth.ErrAuthStateLockHeld) {
		t.Fatalf("second writer error = %v, want ErrAuthStateLockHeld", err)
	}
}

func TestLoadConfig_UsesDefaultHomeConfigPath(t *testing.T) {
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".mnemonas")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDir) error: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	storageRoot := filepath.Join(homeDir, "storage")
	if err := os.WriteFile(configPath, []byte("[storage]\nroot = \""+storageRoot+"\"\n[server]\nport = 18080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}
	t.Setenv("HOME", homeDir)

	cfg, resolvedPath, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig(empty) error: %v", err)
	}
	if resolvedPath != configPath {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, configPath)
	}
	if cfg.Server.Port != 18080 {
		t.Fatalf("server port = %d, want 18080", cfg.Server.Port)
	}
	if cfg.Storage.Root != storageRoot {
		t.Fatalf("storage root = %q, want %q", cfg.Storage.Root, storageRoot)
	}
}

func TestHasFrontendIndex_RejectsViteSourceIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<script type="module" src="/src/main.tsx"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error: %v", err)
	}

	if hasFrontendIndex(dir) {
		t.Fatal("expected Vite source index to be rejected")
	}
}

func TestHasFrontendIndex_AcceptsBuiltIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<script type="module" src="/assets/index.js"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error: %v", err)
	}

	if !hasFrontendIndex(dir) {
		t.Fatal("expected built frontend index to be accepted")
	}
}

func TestHasFrontendIndex_RejectsSymlinkedIndex(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(outside, []byte(`<script type="module" src="/assets/index.js"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(outside index.html) error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "index.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if hasFrontendIndex(dir) {
		t.Fatal("expected symlinked frontend index to be rejected")
	}
}

func TestDiscoverFrontendAssets_PrefersExplicitBuiltDirectory(t *testing.T) {
	envDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(envDir, "index.html"), []byte(`<script type="module" src="/assets/index.js"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error: %v", err)
	}
	fallbackRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fallbackRoot, "web", "dist"), 0o755); err != nil {
		t.Fatalf("MkdirAll(web/dist) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fallbackRoot, "web", "dist", "index.html"), []byte(`<script type="module" src="/assets/fallback.js"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(fallback index.html) error: %v", err)
	}

	t.Setenv("MNEMONAS_WEB_DIR", envDir)
	t.Chdir(fallbackRoot)

	if got := discoverFrontendAssets(); got != envDir {
		t.Fatalf("discoverFrontendAssets() = %q, want env dir %q", got, envDir)
	}
}

func TestDiscoverFrontendAssets_FallsBackToWebDistAndSkipsSourceIndex(t *testing.T) {
	root := t.TempDir()
	webDir := filepath.Join(root, "web")
	distDir := filepath.Join(webDir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(web/dist) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(`<script type="module" src="/src/main.tsx"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(source index.html) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte(`<script type="module" src="/assets/index.js"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(dist index.html) error: %v", err)
	}

	t.Setenv("MNEMONAS_WEB_DIR", "")
	t.Chdir(root)

	if got := discoverFrontendAssets(); filepath.Clean(got) != filepath.Join("web", "dist") {
		t.Fatalf("discoverFrontendAssets() = %q, want web/dist", got)
	}
}

func TestDiscoverFrontendAssets_ReturnsEmptyWithoutBuiltIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatalf("MkdirAll(web) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "index.html"), []byte(`<script type="module" src="/src/main.tsx"></script>`), 0o644); err != nil {
		t.Fatalf("WriteFile(source index.html) error: %v", err)
	}

	t.Setenv("MNEMONAS_WEB_DIR", "")
	t.Chdir(root)

	if got := discoverFrontendAssets(); got != "" {
		t.Fatalf("discoverFrontendAssets() = %q, want empty", got)
	}
}

func TestBuildWebDAVHandler(t *testing.T) {
	serverCfg := config.ServerConfig{
		ReadTimeout:  time.Minute,
		WriteTimeout: 2 * time.Minute,
	}
	quotaCoordinator := quotareservation.NewCoordinator()
	prefix, handler := buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{Enabled: false, Prefix: "/dav"}, serverCfg, quotaCoordinator, nil)
	if prefix != "" || handler != nil {
		t.Fatalf("disabled buildWebDAVHandler() = (%q, %#v), want empty and nil", prefix, handler)
	}

	prefix, handler = buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		AuthType: "basic",
		Username: "admin",
		Password: "secret",
	}, serverCfg, quotaCoordinator, nil)
	if prefix != "/dav" {
		t.Fatalf("enabled prefix = %q, want /dav", prefix)
	}
	if handler == nil {
		t.Fatal("expected enabled WebDAV handler")
	}
	closeWebDAVHandler(handler)

	prefix, handler = buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		AuthType: "basic",
		Password: "secret",
	}, serverCfg, quotaCoordinator, nil)
	if prefix != "/dav" || handler == nil {
		t.Fatalf("basic auth default username buildWebDAVHandler() = (%q, %#v), want /dav and handler", prefix, handler)
	}
	req := httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin Basic Auth status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	req = httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.SetBasicAuth("", "secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("blank username Basic Auth status = %d, want %d; body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	closeWebDAVHandler(handler)

	prefix, handler = buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "dav/",
		AuthType: "none",
	}, serverCfg, quotaCoordinator, nil)
	if prefix != "/dav" {
		t.Fatalf("normalized prefix = %q, want /dav", prefix)
	}
	if handler == nil {
		t.Fatal("expected handler for normalized prefix")
	}
	closeWebDAVHandler(handler)

	userStore, _, err := auth.NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	davUser, err := userStore.Create("davuser", "password123", "", auth.RoleUser)
	if err != nil {
		t.Fatalf("Create(davuser) error: %v", err)
	}
	prefix, handler = buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{
		Enabled:   true,
		Prefix:    "/dav",
		AuthType:  "users",
		UserStore: userStore,
	}, serverCfg, quotaCoordinator, nil)
	if prefix != "/dav" || handler == nil {
		t.Fatalf("users auth buildWebDAVHandler() = (%q, %#v), want /dav and handler", prefix, handler)
	}
	req = httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.SetBasicAuth("davuser", "password123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("users auth OPTIONS status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if err := userStore.ResetPassword(davUser.ID, "temporary-password"); err != nil {
		t.Fatalf("ResetPassword(davuser) error: %v", err)
	}
	req = httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.SetBasicAuth("davuser", "temporary-password")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("required-change WebDAV status = %d, want %d; body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	if err := userStore.ChangePassword(davUser.ID, "temporary-password", "changed-password"); err != nil {
		t.Fatalf("ChangePassword(davuser) error: %v", err)
	}
	req = httptest.NewRequest("OPTIONS", "/dav/", nil)
	req.SetBasicAuth("davuser", "changed-password")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("changed-password WebDAV status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	closeWebDAVHandler(handler)
}

func TestConnectStartupDataplane_UsesTimeoutContext(t *testing.T) {
	originalStartupDataplaneContext := startupDataplaneContext
	originalStartupDataplaneConnect := startupDataplaneConnect
	t.Cleanup(func() {
		startupDataplaneContext = originalStartupDataplaneContext
		startupDataplaneConnect = originalStartupDataplaneConnect
	})

	var observedTimeout time.Duration
	expectedTimeout := 42 * time.Second
	startupDataplaneContext = func(timeout time.Duration) (context.Context, context.CancelFunc) {
		observedTimeout = timeout
		return context.WithTimeout(context.Background(), timeout)
	}

	startupDataplaneConnect = func(client *dataplane.Client, ctx context.Context) error {
		if client == nil {
			t.Fatal("expected dataplane client")
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected startup dataplane context to have a deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("expected positive remaining timeout, got %s", remaining)
		}
		if observedTimeout != expectedTimeout {
			t.Fatalf("observed timeout = %s, want %s", observedTimeout, expectedTimeout)
		}
		if remaining > observedTimeout {
			t.Fatalf("remaining timeout = %s, want <= %s", remaining, observedTimeout)
		}
		return nil
	}

	client, err := connectStartupDataplane("127.0.0.1:9090", expectedTimeout)
	if err != nil {
		t.Fatalf("connectStartupDataplane() error: %v", err)
	}
	if client == nil {
		t.Fatal("expected connected dataplane client")
	}
	_ = client.Close()
}

func TestFrontendHandlerServesAssetsAndSPAFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<div id=\"root\"></div>"), 0o600); err != nil {
		t.Fatalf("WriteFile(index.html) error: %v", err)
	}
	assetsDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assets) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatalf("WriteFile(asset) error: %v", err)
	}

	handler := newFrontendHandler(root)

	tests := []struct {
		name       string
		path       string
		accept     string
		wantStatus int
		wantBody   string
		wantCache  string
	}{
		{name: "root", path: "/", accept: "text/html", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route", path: "/files/photos", accept: "text/html", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route accepts missing accept", path: "/files/photos", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route rejects html q zero", path: "/files/photos", accept: "application/json, text/html;q=0", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "spa route accepts positive html q", path: "/files/photos", accept: "application/json, text/html;q=0.5", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route accepts wildcard", path: "/files/photos", accept: "*/*", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route rejects wildcard with explicit html q zero", path: "/files/photos", accept: "*/*, text/html;q=0", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "spa route rejects text wildcard q zero over broad wildcard", path: "/files/photos", accept: "text/*;q=0, */*;q=1", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "spa route rejects text wildcard q zero after broad wildcard", path: "/files/photos", accept: "*/*;q=1, text/*;q=0", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "spa route accepts positive text wildcard", path: "/files/photos", accept: "application/json, text/*;q=0.5", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "spa route accepts html over rejected text wildcard", path: "/files/photos", accept: "text/*;q=0, text/html;q=0.5", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`, wantCache: "no-cache"},
		{name: "asset", path: "/assets/app.js", accept: "*/*", wantStatus: http.StatusOK, wantBody: "console.log('ok')"},
		{name: "missing asset", path: "/assets/missing.js", accept: "application/javascript", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "missing asset with wildcard", path: "/assets/missing.js", accept: "*/*", wantStatus: http.StatusNotFound, wantBody: "404"},
		{name: "asset dot segment does not fall back", path: "/assets/../files", accept: "text/html", wantStatus: http.StatusNotFound, wantBody: "404"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Accept", tt.accept)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want to contain %q", rec.Body.String(), tt.wantBody)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
		})
	}
}

func TestFrontendHandlerRejectsSymlinkedWebFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<div id=\"root\"></div>"), 0o600); err != nil {
		t.Fatalf("WriteFile(index.html) error: %v", err)
	}
	assetsDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assets) error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(assetsDir, "linked.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	handler := newFrontendHandler(root)
	req := httptest.NewRequest(http.MethodGet, "/assets/linked.txt", nil)
	req.Header.Set("Accept", "*/*")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "outside secret") {
		t.Fatal("symlink target content was served")
	}
}

func TestFrontendHandlerRejectsSymlinkedIndexFallback(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-index.html")
	if err := os.WriteFile(outside, []byte("<div id=\"root\">outside</div>"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside-index) error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "index.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	handler := newFrontendHandler(root)
	req := httptest.NewRequest(http.MethodGet, "/files/photos", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "outside") {
		t.Fatal("symlinked index content was served")
	}
}

func TestShouldServeFrontendPreservesBackendRoutes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		accept string
		want   bool
	}{
		{name: "api", method: http.MethodGet, path: "/api/v1/files/", accept: "text/html", want: false},
		{name: "health", method: http.MethodGet, path: "/health", accept: "text/html", want: false},
		{name: "share json compatibility", method: http.MethodGet, path: "/s/share-1", accept: "application/json", want: false},
		{name: "share missing accept compatibility", method: http.MethodGet, path: "/s/share-1", accept: "", want: false},
		{name: "share wildcard compatibility", method: http.MethodGet, path: "/s/share-1", accept: "*/*", want: false},
		{name: "share spa page", method: http.MethodGet, path: "/s/share-1", accept: "text/html", want: true},
		{name: "share spa page rejects html q zero", method: http.MethodGet, path: "/s/share-1", accept: "application/json, text/html;q=0", want: false},
		{name: "share spa page accepts positive html q", method: http.MethodGet, path: "/s/share-1", accept: "application/json, text/html;q=0.5", want: true},
		{name: "share rejects text wildcard compatibility", method: http.MethodGet, path: "/s/share-1", accept: "text/*", want: false},
		{name: "share download", method: http.MethodGet, path: "/s/share-1/download", accept: "text/html", want: false},
		{name: "api dot segment stays backend", method: http.MethodGet, path: "/api/../settings", accept: "text/html", want: false},
		{name: "encoded api dot segment stays backend", method: http.MethodGet, path: "/api/%2e%2e/settings", accept: "text/html", want: false},
		{name: "share dot segment stays backend", method: http.MethodGet, path: "/s/../settings", accept: "text/html", want: false},
		{name: "nested share dot segment stays backend", method: http.MethodGet, path: "/s/share-1/../settings", accept: "text/html", want: false},
		{name: "spa route", method: http.MethodGet, path: "/settings", accept: "text/html", want: true},
		{name: "mutation", method: http.MethodPost, path: "/settings", accept: "text/html", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Accept", tt.accept)

			if got := shouldServeFrontend(req); got != tt.want {
				t.Fatalf("shouldServeFrontend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func restoreGlobalLoggerState(t *testing.T) {
	t.Helper()
	previousLogger := log.Logger
	previousLevel := zerolog.GlobalLevel()
	previousTimeFormat := zerolog.TimeFieldFormat
	t.Cleanup(func() {
		log.Logger = previousLogger
		zerolog.SetGlobalLevel(previousLevel)
		zerolog.TimeFieldFormat = previousTimeFormat
	})
}

func TestInitLogger_ConfiguresInfoConsoleLogger(t *testing.T) {
	restoreGlobalLoggerState(t)

	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	initLogger()

	if got := zerolog.GlobalLevel(); got != zerolog.InfoLevel {
		t.Fatalf("global log level = %s, want %s", got, zerolog.InfoLevel)
	}
}

func TestApplyLoggerConfig_WritesJSONLogsToConfiguredFile(t *testing.T) {
	restoreGlobalLoggerState(t)

	logPath := filepath.Join(t.TempDir(), "mnemonas.log")
	closer, err := applyLoggerConfig(config.LogConfig{
		Level:      "debug",
		Format:     "json",
		Output:     logPath,
		TimeFormat: time.RFC3339,
	})
	if err != nil {
		t.Fatalf("applyLoggerConfig() error: %v", err)
	}
	defer func() {
		if closer != nil {
			_ = closer.Close()
		}
	}()

	log.Debug().Msg("debug message")
	if closer != nil {
		_ = closer.Close()
		closer = nil
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.Contains(content, `"level":"debug"`) {
		t.Fatalf("expected debug level in JSON log, got %s", content)
	}
	if !strings.Contains(content, `"message":"debug message"`) {
		t.Fatalf("expected debug message in JSON log, got %s", content)
	}
	if !strings.Contains(content, `"time":`) {
		t.Fatalf("expected timestamp field in JSON log, got %s", content)
	}
}

func TestApplyLoggerConfig_RespectsConfiguredLevel(t *testing.T) {
	restoreGlobalLoggerState(t)

	logPath := filepath.Join(t.TempDir(), "mnemonas.log")
	closer, err := applyLoggerConfig(config.LogConfig{
		Level:      "warn",
		Format:     "json",
		Output:     logPath,
		TimeFormat: time.RFC3339,
	})
	if err != nil {
		t.Fatalf("applyLoggerConfig() error: %v", err)
	}
	defer func() {
		if closer != nil {
			_ = closer.Close()
		}
	}()

	log.Info().Msg("suppressed info")
	log.Warn().Msg("warn message")
	if closer != nil {
		_ = closer.Close()
		closer = nil
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if strings.Contains(content, "suppressed info") {
		t.Fatalf("expected info log to be filtered out, got %s", content)
	}
	if !strings.Contains(content, `"message":"warn message"`) {
		t.Fatalf("expected warn message in log output, got %s", content)
	}
}

func TestApplyLoggerConfig_ConsoleRFC3339UsesTimeLayout(t *testing.T) {
	restoreGlobalLoggerState(t)

	logPath := filepath.Join(t.TempDir(), "mnemonas.log")
	closer, err := applyLoggerConfig(config.LogConfig{
		Level:      "info",
		Format:     "console",
		Output:     logPath,
		TimeFormat: "RFC3339",
	})
	if err != nil {
		t.Fatalf("applyLoggerConfig() error: %v", err)
	}
	defer func() {
		if closer != nil {
			_ = closer.Close()
		}
	}()

	log.Info().Msg("console message")
	if closer != nil {
		_ = closer.Close()
		closer = nil
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.Contains(content, "console message") {
		t.Fatalf("expected console message in log output, got %s", content)
	}
	if strings.Contains(content, "RFC") {
		t.Fatalf("console log timestamp used the literal time format name, got %s", content)
	}
}

func TestResolveLogOutput_StdoutAndStderr(t *testing.T) {
	writer, closer, _, err := resolveLogOutput(" stdout ")
	if err != nil {
		t.Fatalf("resolveLogOutput(stdout) error: %v", err)
	}
	if writer != os.Stdout {
		t.Fatalf("resolveLogOutput(stdout) writer = %#v, want stdout", writer)
	}
	if closer != nil {
		t.Fatalf("resolveLogOutput(stdout) closer = %#v, want nil", closer)
	}

	writer, closer, _, err = resolveLogOutput("STDERR")
	if err != nil {
		t.Fatalf("resolveLogOutput(stderr) error: %v", err)
	}
	if writer != os.Stderr {
		t.Fatalf("resolveLogOutput(stderr) writer = %#v, want stderr", writer)
	}
	if closer != nil {
		t.Fatalf("resolveLogOutput(stderr) closer = %#v, want nil", closer)
	}
}

func TestResolveLogOutput_CreatesParentDirectoriesAndAppends(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "nested", "mnemonas.log")

	writer, closer, noColor, err := resolveLogOutput(logPath)
	if err != nil {
		t.Fatalf("resolveLogOutput(file) error: %v", err)
	}
	if !noColor {
		t.Fatal("file log output should disable color")
	}
	if closer == nil {
		t.Fatal("expected file log output to return a closer")
	}
	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write(first) error: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close(first) error: %v", err)
	}

	writer, closer, _, err = resolveLogOutput(logPath)
	if err != nil {
		t.Fatalf("resolveLogOutput(file second) error: %v", err)
	}
	if _, err := writer.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write(second) error: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close(second) error: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(logPath) error: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("log file content = %q, want appended writes", string(data))
	}
}

func TestResolveLogOutput_RejectsSymlinkInsertedAfterParentOpen(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(logs) error: %v", err)
	}
	outsidePath := filepath.Join(tmpDir, "outside.log")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	logPath := filepath.Join(logDir, "mnemonas.log")

	var hookErr error
	originalHook := afterOpenLogOutputParent
	afterOpenLogOutputParent = func() {
		if hookErr != nil {
			return
		}
		hookErr = os.Symlink(outsidePath, logPath)
	}
	t.Cleanup(func() {
		afterOpenLogOutputParent = originalHook
	})

	_, closer, _, err := resolveLogOutput(logPath)
	if closer != nil {
		_ = closer.Close()
	}
	if hookErr != nil {
		t.Fatalf("hook symlink error: %v", hookErr)
	}
	if !errors.Is(err, errLogOutputSymlink) {
		t.Fatalf("expected symlink inserted after parent open to be rejected, got %v", err)
	}

	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("ReadFile(outside) error: %v", readErr)
	}
	if string(data) != "outside\n" {
		t.Fatalf("outside log content = %q, want unchanged", string(data))
	}
}

func TestResolveLogOutput_DoesNotFollowParentSymlinkInsertedAfterParentOpen(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(logs) error: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(outside) error: %v", err)
	}
	logPath := filepath.Join(logDir, "mnemonas.log")
	renamedLogDir := filepath.Join(tmpDir, "logs-renamed")
	outsideLogPath := filepath.Join(outsideDir, "mnemonas.log")

	var hookErr error
	originalHook := afterOpenLogOutputParent
	afterOpenLogOutputParent = func() {
		if hookErr != nil {
			return
		}
		if err := os.Rename(logDir, renamedLogDir); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(outsideDir, logDir)
	}
	t.Cleanup(func() {
		afterOpenLogOutputParent = originalHook
	})

	writer, closer, _, err := resolveLogOutput(logPath)
	if err != nil {
		t.Fatalf("resolveLogOutput(file) error: %v", err)
	}
	if hookErr != nil {
		if closer != nil {
			_ = closer.Close()
		}
		t.Fatalf("hook replace parent error: %v", hookErr)
	}
	if _, err := writer.Write([]byte("inside\n")); err != nil {
		_ = closer.Close()
		t.Fatalf("Write(log) error: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close(log) error: %v", err)
	}

	if _, err := os.Stat(outsideLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside log was created through replaced parent, stat error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(renamedLogDir, "mnemonas.log"))
	if err != nil {
		t.Fatalf("ReadFile(renamed log) error: %v", err)
	}
	if string(data) != "inside\n" {
		t.Fatalf("renamed log content = %q, want write through anchored parent", string(data))
	}
}

func TestNormalizeLogOutputPath(t *testing.T) {
	tmpDir := t.TempDir()
	absPath := filepath.Join(tmpDir, "mnemonas.log")

	got, err := normalizeLogOutputPath(" " + absPath + " ")
	if err != nil {
		t.Fatalf("normalizeLogOutputPath(abs) error: %v", err)
	}
	if got != absPath {
		t.Fatalf("normalizeLogOutputPath(abs) = %q, want %q", got, absPath)
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir(tmpDir) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousDir)
	})

	got, err = normalizeLogOutputPath(" logs/mnemonas.log ")
	if err != nil {
		t.Fatalf("normalizeLogOutputPath(rel) error: %v", err)
	}
	want := filepath.Join(tmpDir, "logs", "mnemonas.log")
	if got != want {
		t.Fatalf("normalizeLogOutputPath(rel) = %q, want %q", got, want)
	}
}

func TestValidateLogOutputPath(t *testing.T) {
	tmpDir := t.TempDir()
	regularPath := filepath.Join(tmpDir, "mnemonas.log")
	if err := os.WriteFile(regularPath, []byte("log"), 0o600); err != nil {
		t.Fatalf("WriteFile(regular) error: %v", err)
	}
	if err := validateLogOutputPath(regularPath); err != nil {
		t.Fatalf("validateLogOutputPath(regular) error: %v", err)
	}

	missingPath := filepath.Join(tmpDir, "missing", "mnemonas.log")
	if err := validateLogOutputPath(missingPath); err != nil {
		t.Fatalf("validateLogOutputPath(missing) error: %v", err)
	}

	targetPath := filepath.Join(tmpDir, "target.log")
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}
	symlinkPath := filepath.Join(tmpDir, "linked.log")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("Symlink(log) error: %v", err)
	}
	if err := validateLogOutputPath(symlinkPath); !errors.Is(err, errLogOutputSymlink) {
		t.Fatalf("validateLogOutputPath(symlink) error = %v, want %v", err, errLogOutputSymlink)
	}
}

func TestResolveLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    zerolog.Level
		wantErr bool
	}{
		{name: "empty defaults info", input: "", want: zerolog.InfoLevel},
		{name: "trimmed debug", input: " debug ", want: zerolog.DebugLevel},
		{name: "warn", input: "WARN", want: zerolog.WarnLevel},
		{name: "error", input: "error", want: zerolog.ErrorLevel},
		{name: "invalid", input: "trace", want: zerolog.InfoLevel, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLogLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveLogLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("resolveLogLevel(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveLogTimeFormats(t *testing.T) {
	if got := resolveConsoleLogTimeFormat(""); got != time.RFC3339 {
		t.Fatalf("default console time format = %q, want %q", got, time.RFC3339)
	}
	if got := resolveConsoleLogTimeFormat("rfc3339"); got != time.RFC3339 {
		t.Fatalf("named console RFC3339 time format = %q, want %q", got, time.RFC3339)
	}
	if got := resolveConsoleLogTimeFormat("rfc3339nano"); got != time.RFC3339Nano {
		t.Fatalf("named console RFC3339Nano time format = %q, want %q", got, time.RFC3339Nano)
	}
	if got := resolveConsoleLogTimeFormat(" 15:04 "); got != "15:04" {
		t.Fatalf("custom console time format = %q, want 15:04", got)
	}
	if got := resolveConsoleLogTimeFieldFormat("unixms"); got != zerolog.TimeFormatUnixMs {
		t.Fatalf("console time field format = %q, want %q", got, zerolog.TimeFormatUnixMs)
	}
	if formatter := resolveConsoleLogTimestampFormatter("unix"); formatter == nil || formatter("1234567890") != "1234567890" {
		t.Fatalf("unix console timestamp formatter did not preserve raw timestamp")
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", in: "", want: time.RFC3339},
		{name: "rfc3339", in: "rfc3339", want: time.RFC3339},
		{name: "rfc3339 nano", in: "RFC3339Nano", want: time.RFC3339Nano},
		{name: "unix", in: "UNIX", want: zerolog.TimeFormatUnix},
		{name: "unix milliseconds", in: "unixms", want: zerolog.TimeFormatUnixMs},
		{name: "unix microseconds", in: "UNIXMICRO", want: zerolog.TimeFormatUnixMicro},
		{name: "unix nanoseconds", in: "UNIXNANO", want: zerolog.TimeFormatUnixNano},
		{name: "custom", in: " 2006-01-02 ", want: "2006-01-02"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveJSONLogTimeFormat(tt.in); got != tt.want {
				t.Fatalf("resolveJSONLogTimeFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLogWebDAVCredentialStatus_DoesNotWritePasswordField(t *testing.T) {
	restoreGlobalLoggerState(t)

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf)

	logWebDAVCredentialStatus("/srv/mnemonas", "admin", true, true)

	output := buf.String()
	if !strings.Contains(output, "WebDAV credentials were auto-generated") {
		t.Fatalf("expected generated credentials guidance, got %s", output)
	}
	if !strings.Contains(output, `"/srv/mnemonas/secrets.json"`) {
		t.Fatalf("expected secrets file path in log output, got %s", output)
	}
	if strings.Contains(output, `"password":`) {
		t.Fatalf("expected log output not to include a password field, got %s", output)
	}
}

func TestApplyStartupWebDAVCredentials_TreatsWhitespacePasswordAsGenerated(t *testing.T) {
	cfg := config.Default()
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.WebDAV.Username = " \t "
	cfg.WebDAV.Password = " \t "
	secrets := &config.Secrets{WebDAVPassword: "generated-pass"}

	generated := applyStartupWebDAVCredentials(cfg, secrets)

	if !generated {
		t.Fatal("expected whitespace WebDAV password to use generated secret")
	}
	if cfg.WebDAV.Username != "admin" {
		t.Fatalf("expected default username admin, got %q", cfg.WebDAV.Username)
	}
	if cfg.WebDAV.Password != "generated-pass" {
		t.Fatalf("expected generated WebDAV password, got %q", cfg.WebDAV.Password)
	}
}

func TestApplyStartupJWTSecret_TreatsWhitespaceAsGenerated(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.JWTSecret = " \t "
	secrets := &config.Secrets{JWTSecret: "persistent-jwt-secret-at-least-32-bytes"}

	applyStartupJWTSecret(cfg, secrets)

	if cfg.Auth.JWTSecret != secrets.JWTSecret {
		t.Fatalf("Auth.JWTSecret = %q, want persistent generated secret", cfg.Auth.JWTSecret)
	}
}

func TestApplyStartupJWTSecret_PreservesCustomSecret(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.JWTSecret = " custom-jwt-secret-at-least-32-bytes "
	secrets := &config.Secrets{JWTSecret: "persistent-jwt-secret-at-least-32-bytes"}

	applyStartupJWTSecret(cfg, secrets)

	if cfg.Auth.JWTSecret != " custom-jwt-secret-at-least-32-bytes " {
		t.Fatalf("Auth.JWTSecret = %q, want explicit custom secret unchanged", cfg.Auth.JWTSecret)
	}
}

func TestApplyStartupWebDAVCredentials_PreservesCustomPassword(t *testing.T) {
	cfg := config.Default()
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.WebDAV.Username = ""
	cfg.WebDAV.Password = " custom-pass "
	secrets := &config.Secrets{WebDAVPassword: "generated-pass"}

	generated := applyStartupWebDAVCredentials(cfg, secrets)

	if generated {
		t.Fatal("expected custom WebDAV password to be preserved")
	}
	if cfg.WebDAV.Username != "admin" {
		t.Fatalf("expected default username admin, got %q", cfg.WebDAV.Username)
	}
	if cfg.WebDAV.Password != " custom-pass " {
		t.Fatalf("expected custom WebDAV password to be preserved, got %q", cfg.WebDAV.Password)
	}
}

type testGracefulHTTPServer struct {
	shutdownCalls atomic.Int32
	closeCalls    atomic.Int32
	shutdown      func(context.Context) error
	close         func() error
}

func (s *testGracefulHTTPServer) Shutdown(ctx context.Context) error {
	s.shutdownCalls.Add(1)
	if s.shutdown != nil {
		return s.shutdown(ctx)
	}
	return nil
}

func (s *testGracefulHTTPServer) Close() error {
	s.closeCalls.Add(1)
	if s.close != nil {
		return s.close()
	}
	return nil
}

func TestRunMainAndExitExitsOnlyAfterRunCleanup(t *testing.T) {
	var order []string
	run := func() (exitCode int) {
		defer func() {
			order = append(order, "cleanup")
		}()
		order = append(order, "run")
		return 23
	}

	runMainAndExit(run, func(exitCode int) {
		order = append(order, fmt.Sprintf("exit:%d", exitCode))
	})

	want := []string{"run", "cleanup", "exit:23"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("run/exit order = %v, want %v", order, want)
	}
}

func TestRunMainAndExitDoesNotExitOnSuccess(t *testing.T) {
	exitCalled := false
	runMainAndExit(func() int {
		return 0
	}, func(int) {
		exitCalled = true
	})
	if exitCalled {
		t.Fatal("successful run unexpectedly invoked process exit")
	}
}

func TestRunHTTPServerLifecycle_WaitsForGracefulShutdownBeforeResourceRelease(t *testing.T) {
	requestStarted := make(chan struct{})
	requestRelease := make(chan struct{})
	defer func() {
		select {
		case <-requestRelease:
		default:
			close(requestRelease)
		}
	}()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-requestRelease
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	t.Cleanup(func() {
		_ = server.Close()
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	shutdownRequest := make(chan struct{})
	shutdownObserved := make(chan struct{})
	var resourcesClosed atomic.Int32
	resultDone := make(chan httpServerLifecycleResult, 1)
	go func() {
		result := runHTTPServerLifecycle(
			server,
			func() error {
				return server.Serve(listener)
			},
			shutdownRequest,
			time.Second,
			func() {
				close(shutdownObserved)
			},
		)
		resourcesClosed.Add(1)
		resultDone <- result
	}()

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   2 * time.Second,
	}
	t.Cleanup(client.CloseIdleConnections)
	clientDone := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("http://" + listener.Addr().String() + "/slow")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				requestErr = fmt.Errorf("response status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
		}
		clientDone <- requestErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the slow HTTP request")
	}

	close(shutdownRequest)
	select {
	case <-shutdownObserved:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the shutdown request to be observed")
	}
	if got := resourcesClosed.Load(); got != 0 {
		t.Fatalf("resources closed while a request was draining: %d", got)
	}
	select {
	case result := <-resultDone:
		t.Fatalf("server lifecycle returned before the request drained: %#v", result)
	default:
	}

	close(requestRelease)
	var result httpServerLifecycleResult
	select {
	case result = <-resultDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for graceful server shutdown")
	}
	if err := result.Err(); err != nil {
		t.Fatalf("runHTTPServerLifecycle() error: %v", err)
	}
	if !result.ShutdownRequested {
		t.Fatal("expected the lifecycle result to record a shutdown request")
	}
	if got := resourcesClosed.Load(); got != 1 {
		t.Fatalf("resources closed after shutdown = %d, want 1", got)
	}
	select {
	case requestErr := <-clientDone:
		if requestErr != nil {
			t.Fatalf("slow client request error: %v", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the slow client request")
	}
}

type permanentAcceptErrorListener struct {
	net.Listener
	fail      atomic.Bool
	acceptErr error
}

func (l *permanentAcceptErrorListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.fail.Load() {
		_ = conn.Close()
		return nil, l.acceptErr
	}
	return conn, nil
}

func (l *permanentAcceptErrorListener) FailNextAccept() error {
	l.fail.Store(true)
	conn, err := net.DialTimeout(l.Addr().Network(), l.Addr().String(), time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

func TestRunHTTPServerLifecycle_ShutdownTimeoutForcesCloseAndReturnsError(t *testing.T) {
	forceClosed := make(chan struct{})
	var shutdownRequestObserved atomic.Bool
	var shutdownOrderFailure atomic.Bool
	server := &testGracefulHTTPServer{
		shutdown: func(ctx context.Context) error {
			if !shutdownRequestObserved.Load() {
				shutdownOrderFailure.Store(true)
			}
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			close(forceClosed)
			return nil
		},
	}
	shutdownRequest := make(chan struct{})
	close(shutdownRequest)

	result := runHTTPServerLifecycle(
		server,
		func() error {
			<-forceClosed
			return http.ErrServerClosed
		},
		shutdownRequest,
		20*time.Millisecond,
		func() {
			shutdownRequestObserved.Store(true)
		},
	)

	if !result.ShutdownRequested {
		t.Fatal("expected timeout path to record a shutdown request")
	}
	if !errors.Is(result.ShutdownErr, context.DeadlineExceeded) {
		t.Fatalf("ShutdownErr = %v, want context deadline exceeded", result.ShutdownErr)
	}
	if result.CloseErr != nil {
		t.Fatalf("CloseErr = %v, want nil", result.CloseErr)
	}
	if result.Err() == nil {
		t.Fatal("expected shutdown timeout to return an error")
	}
	if got := server.shutdownCalls.Load(); got != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", got)
	}
	if got := server.closeCalls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
	if shutdownOrderFailure.Load() {
		t.Fatal("Shutdown ran before the shutdown request callback completed")
	}
}

func TestRunHTTPServerLifecycle_ShutdownAndCloseErrorsAreAggregated(t *testing.T) {
	shutdownErr := errors.New("shutdown failed")
	closeErr := errors.New("close failed")
	forceClosed := make(chan struct{})
	server := &testGracefulHTTPServer{
		shutdown: func(context.Context) error {
			return shutdownErr
		},
		close: func() error {
			close(forceClosed)
			return closeErr
		},
	}
	shutdownRequest := make(chan struct{})
	close(shutdownRequest)

	result := runHTTPServerLifecycle(
		server,
		func() error {
			<-forceClosed
			return http.ErrServerClosed
		},
		shutdownRequest,
		time.Second,
		nil,
	)

	if !errors.Is(result.Err(), shutdownErr) {
		t.Fatalf("lifecycle error = %v, want shutdown error %v", result.Err(), shutdownErr)
	}
	if !errors.Is(result.Err(), closeErr) {
		t.Fatalf("lifecycle error = %v, want close error %v", result.Err(), closeErr)
	}
	if got := server.shutdownCalls.Load(); got != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", got)
	}
	if got := server.closeCalls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
}

func TestRunHTTPServerLifecycle_ConcurrentSignalAndServeExitShutDownOnce(t *testing.T) {
	const attempts = 100
	serveErr := errors.New("serve failed concurrently")
	for attempt := 0; attempt < attempts; attempt++ {
		server := &testGracefulHTTPServer{}
		shutdownRequest := make(chan struct{})
		close(shutdownRequest)
		var shutdownStarts atomic.Int32

		result := runHTTPServerLifecycle(
			server,
			func() error {
				return serveErr
			},
			shutdownRequest,
			time.Second,
			func() {
				shutdownStarts.Add(1)
			},
		)

		if !result.ShutdownRequested {
			t.Fatalf("attempt %d did not record the concurrent shutdown request", attempt)
		}
		if !errors.Is(result.ServeErr, serveErr) {
			t.Fatalf("attempt %d ServeErr = %v, want %v", attempt, result.ServeErr, serveErr)
		}
		if got := server.shutdownCalls.Load(); got != 1 {
			t.Fatalf("attempt %d Shutdown calls = %d, want 1", attempt, got)
		}
		if got := server.closeCalls.Load(); got != 0 {
			t.Fatalf("attempt %d Close calls = %d, want 0", attempt, got)
		}
		if got := shutdownStarts.Load(); got != 1 {
			t.Fatalf("attempt %d shutdown starts = %d, want 1", attempt, got)
		}
	}
}

func TestRunHTTPServerLifecycle_PermanentAcceptErrorDrainsActiveRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	requestRelease := make(chan struct{})
	defer func() {
		select {
		case <-requestRelease:
		default:
			close(requestRelease)
		}
	}()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-requestRelease
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	t.Cleanup(func() {
		_ = server.Close()
	})
	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}
	serveErr := errors.New("permanent listener accept failure")
	listener := &permanentAcceptErrorListener{
		Listener:  baseListener,
		acceptErr: serveErr,
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	shutdownRequest := make(chan struct{})
	shutdownStarted := make(chan struct{})
	var resourcesClosed atomic.Int32
	resultDone := make(chan httpServerLifecycleResult, 1)
	go func() {
		result := runHTTPServerLifecycle(
			server,
			func() error {
				return server.Serve(listener)
			},
			shutdownRequest,
			time.Second,
			func() {
				close(shutdownStarted)
			},
		)
		resourcesClosed.Add(1)
		resultDone <- result
	}()

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   3 * time.Second,
	}
	t.Cleanup(client.CloseIdleConnections)
	clientDone := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("http://" + listener.Addr().String() + "/slow")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				requestErr = fmt.Errorf("response status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
		}
		clientDone <- requestErr
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the active request")
	}
	if err := listener.FailNextAccept(); err != nil {
		t.Fatalf("FailNextAccept() error: %v", err)
	}
	select {
	case <-shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shutdown after the permanent accept error")
	}
	if got := resourcesClosed.Load(); got != 0 {
		t.Fatalf("resources closed while the active request was draining: %d", got)
	}
	select {
	case result := <-resultDone:
		t.Fatalf("server lifecycle returned before the active request drained: %#v", result)
	default:
	}

	close(requestRelease)
	var result httpServerLifecycleResult
	select {
	case result = <-resultDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for abnormal server shutdown")
	}
	if result.ShutdownRequested {
		t.Fatal("permanent accept error recorded a nonexistent external shutdown request")
	}
	if !errors.Is(result.ServeErr, serveErr) {
		t.Fatalf("ServeErr = %v, want %v", result.ServeErr, serveErr)
	}
	if result.ShutdownErr != nil {
		t.Fatalf("ShutdownErr = %v, want nil", result.ShutdownErr)
	}
	if result.CloseErr != nil {
		t.Fatalf("CloseErr = %v, want nil", result.CloseErr)
	}
	if got := resourcesClosed.Load(); got != 1 {
		t.Fatalf("resources closed after drain = %d, want 1", got)
	}
	select {
	case requestErr := <-clientDone:
		if requestErr != nil {
			t.Fatalf("slow client request error: %v", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the slow client request")
	}
}

func TestRunHTTPServerLifecycle_NilOrErrServerClosedWithoutRequestStillShutsDown(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		serveErr error
	}{
		{name: "nil", serveErr: nil},
		{name: "ErrServerClosed", serveErr: http.ErrServerClosed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := &testGracefulHTTPServer{}
			shutdownRequest := make(chan struct{})
			var shutdownStarts atomic.Int32

			result := runHTTPServerLifecycle(
				server,
				func() error {
					return testCase.serveErr
				},
				shutdownRequest,
				time.Second,
				func() {
					shutdownStarts.Add(1)
				},
			)

			if result.ShutdownRequested {
				t.Fatal("unexpected server exit recorded a nonexistent shutdown request")
			}
			if !errors.Is(result.ServeErr, errHTTPServerExitedWithoutShutdown) {
				t.Fatalf("ServeErr = %v, want %v", result.ServeErr, errHTTPServerExitedWithoutShutdown)
			}
			if got := server.shutdownCalls.Load(); got != 1 {
				t.Fatalf("Shutdown calls = %d, want 1", got)
			}
			if got := server.closeCalls.Load(); got != 0 {
				t.Fatalf("Close calls = %d, want 0", got)
			}
			if got := shutdownStarts.Load(); got != 1 {
				t.Fatalf("shutdown starts = %d, want 1", got)
			}
		})
	}
}

func TestSwitchableWebDAVHandler_ServeIfMatchesUsesLatestPrefixAndHandler(t *testing.T) {
	firstCalls := 0
	secondCalls := 0

	switcher := newSwitchableWebDAVHandler("/dav", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls++
		w.WriteHeader(http.StatusCreated)
	}))

	firstReq := httptest.NewRequest(http.MethodGet, "/dav/files/report.txt", nil)
	firstRec := httptest.NewRecorder()
	if !switcher.ServeIfMatches(firstRec, firstReq) {
		t.Fatalf("expected initial WebDAV handler to match request")
	}
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("initial handler status = %d, want %d", firstRec.Code, http.StatusCreated)
	}
	if firstCalls != 1 {
		t.Fatalf("expected initial handler to be called once, got %d", firstCalls)
	}

	switcher.Update("/new-dav", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	oldPrefixReq := httptest.NewRequest(http.MethodGet, "/dav/files/report.txt", nil)
	oldPrefixRec := httptest.NewRecorder()
	if switcher.ServeIfMatches(oldPrefixRec, oldPrefixReq) {
		t.Fatalf("expected old WebDAV prefix to stop matching after update")
	}

	newPrefixReq := httptest.NewRequest(http.MethodGet, "/new-dav/files/report.txt", nil)
	newPrefixRec := httptest.NewRecorder()
	if !switcher.ServeIfMatches(newPrefixRec, newPrefixReq) {
		t.Fatalf("expected updated WebDAV prefix to match request")
	}
	if newPrefixRec.Code != http.StatusNoContent {
		t.Fatalf("updated handler status = %d, want %d", newPrefixRec.Code, http.StatusNoContent)
	}
	if secondCalls != 1 {
		t.Fatalf("expected updated handler to be called once, got %d", secondCalls)
	}

	switcher.Update("", nil)
	disabledReq := httptest.NewRequest(http.MethodGet, "/new-dav/files/report.txt", nil)
	disabledRec := httptest.NewRecorder()
	if switcher.ServeIfMatches(disabledRec, disabledReq) {
		t.Fatalf("expected disabled WebDAV handler to stop matching requests")
	}
}

type closeTrackingWebDAVHandler struct {
	serve  func(http.ResponseWriter, *http.Request)
	closed chan struct{}
}

func (h *closeTrackingWebDAVHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.serve != nil {
		h.serve(w, r)
	}
}

func (h *closeTrackingWebDAVHandler) Close() {
	close(h.closed)
}

type lifecycleIDWebDAVHandler struct {
	id         uint64
	closeCalls atomic.Int32
	status     int
}

func (h *lifecycleIDWebDAVHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	status := h.status
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func (h *lifecycleIDWebDAVHandler) Close() {
	h.closeCalls.Add(1)
}

func (h *lifecycleIDWebDAVHandler) WebDAVLifecycleID() uint64 {
	return h.id
}

func waitForHandlerClose(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the retired WebDAV handler to close")
	}
}

func TestSwitchableWebDAVHandler_UpdateCallsCloseWithoutErrorResult(t *testing.T) {
	previous := &closeTrackingWebDAVHandler{closed: make(chan struct{})}
	switcher := newSwitchableWebDAVHandler("/dav", previous)

	switcher.Update("", nil)
	waitForHandlerClose(t, previous.closed)
}

type blockingCloseWebDAVHandler struct {
	closeStarted chan struct{}
	closeRelease chan struct{}
}

func (h *blockingCloseWebDAVHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (h *blockingCloseWebDAVHandler) Close() {
	close(h.closeStarted)
	<-h.closeRelease
}

func TestSwitchableWebDAVHandler_UpdateDoesNotWaitForRetiredHandlerClose(t *testing.T) {
	previous := &blockingCloseWebDAVHandler{
		closeStarted: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	switcher := newSwitchableWebDAVHandler("/dav", previous)

	updateDone := make(chan struct{})
	go func() {
		switcher.Update("/next-dav", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		close(updateDone)
	}()
	select {
	case <-updateDone:
	case <-time.After(time.Second):
		t.Fatal("Update waited for the retired handler's Close method")
	}
	select {
	case <-previous.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("retired handler Close did not start asynchronously")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/next-dav/file.txt", nil)
	if !switcher.ServeIfMatches(recorder, request) {
		t.Fatal("replacement handler was unavailable while retired Close was blocked")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("replacement handler status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected republishing a retiring handler to panic")
			}
		}()
		switcher.Update("/reused-dav", previous)
	}()
	close(previous.closeRelease)
}

func TestSwitchableWebDAVHandler_RejectsRepublishingHandlerAfterCloseCompletes(t *testing.T) {
	previous := &closeTrackingWebDAVHandler{closed: make(chan struct{})}
	switcher := newSwitchableWebDAVHandler("/dav", previous)

	switcher.Update("/next-dav", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	waitForHandlerClose(t, previous.closed)

	defer func() {
		if recover() == nil {
			t.Fatal("expected republishing a closed handler to panic")
		}
	}()
	switcher.Update("/reused-dav", previous)
}

func TestSwitchableWebDAVHandler_AllowsRepublishingNonClosableHandler(t *testing.T) {
	reusable := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	switcher := newSwitchableWebDAVHandler("/dav", reusable)

	switcher.Update("/next-dav", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	switcher.Update("/reused-dav", reusable)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/reused-dav/file.txt", nil)
	if !switcher.ServeIfMatches(recorder, request) {
		t.Fatal("republished non-closable handler did not match")
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("republished non-closable handler status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
}

func TestSwitchableWebDAVHandler_RejectsZeroOrDuplicateLifecycleID(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected zero lifecycle ID to panic")
			}
		}()
		_ = newSwitchableWebDAVHandler("/dav", &lifecycleIDWebDAVHandler{id: 0})
	}()

	first := &lifecycleIDWebDAVHandler{id: 41, status: http.StatusAccepted}
	duplicate := &lifecycleIDWebDAVHandler{id: 41}
	switcher := newSwitchableWebDAVHandler("/dav", first)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected duplicate lifecycle ID to panic")
			}
		}()
		switcher.Update("/duplicate-dav", duplicate)
	}()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/dav/file.txt", nil)
	if !switcher.ServeIfMatches(recorder, request) {
		t.Fatal("duplicate lifecycle rejection replaced the current handler")
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("current handler status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if got := first.closeCalls.Load(); got != 0 {
		t.Fatalf("current handler close calls = %d, want 0", got)
	}
	if got := duplicate.closeCalls.Load(); got != 0 {
		t.Fatalf("duplicate handler close calls = %d, want 0", got)
	}

	unique := &lifecycleIDWebDAVHandler{id: 42}
	switcher.Update("/unique-dav", unique)
}

func TestSwitchableWebDAVHandler_ProductionLifecycleTombstonesAreLightweightAndConstantLookup(t *testing.T) {
	const handlerCount = 1000
	runtimeState := webdav.NewRuntimeLockState()
	t.Cleanup(runtimeState.Close)
	switcher := newSwitchableWebDAVHandler("", nil)
	handlerType := reflect.TypeOf((*webdav.Handler)(nil))

	for index := 0; index < handlerCount; index++ {
		handler := webdav.NewHandler(webdav.Config{
			AuthType:         "none",
			RuntimeLockState: runtimeState,
		})
		switcher.Update(fmt.Sprintf("/dav-%d", index), handler)
	}
	switcher.Update("", nil)

	if got := len(switcher.registeredLifecycleIDs); got != handlerCount {
		t.Fatalf("registered lifecycle IDs = %d, want %d", got, handlerCount)
	}
	if got := len(switcher.fallbackClosers); got != 0 {
		t.Fatalf("fallback closers = %d, want 0 production handler references", got)
	}
	for key := range switcher.registeredLifecycleIDs {
		if key.handlerType != handlerType {
			t.Fatalf("lifecycle tombstone type = %v, want %v", key.handlerType, handlerType)
		}
		if key.id == 0 {
			t.Fatal("lifecycle tombstone retained a zero ID")
		}
	}
}

func TestSwitchableWebDAVHandler_ProductionLifecycleRejectsReuseButAllowsNewInstance(t *testing.T) {
	runtimeState := webdav.NewRuntimeLockState()
	t.Cleanup(runtimeState.Close)
	first := webdav.NewHandler(webdav.Config{AuthType: "none", RuntimeLockState: runtimeState})
	second := webdav.NewHandler(webdav.Config{AuthType: "none", RuntimeLockState: runtimeState})
	third := webdav.NewHandler(webdav.Config{AuthType: "none", RuntimeLockState: runtimeState})
	switcher := newSwitchableWebDAVHandler("/dav", first)
	switcher.Update("/second-dav", second)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected republishing a production handler lifecycle to panic")
			}
		}()
		switcher.Update("/reused-dav", first)
	}()

	switcher.Update("/third-dav", third)
	if first.WebDAVLifecycleID() == second.WebDAVLifecycleID() ||
		first.WebDAVLifecycleID() == third.WebDAVLifecycleID() ||
		second.WebDAVLifecycleID() == third.WebDAVLifecycleID() {
		t.Fatal("new production handlers reused a lifecycle ID")
	}
}

func TestSwitchableWebDAVHandler_UpdatePublishesBeforeInFlightRequestDrains(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	previous := &closeTrackingWebDAVHandler{
		closed: make(chan struct{}),
		serve: func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusCreated)
		},
	}
	switcher := newSwitchableWebDAVHandler("/dav", previous)

	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/dav/slow.txt", nil)
		if !switcher.ServeIfMatches(recorder, request) {
			t.Error("expected slow WebDAV request to match")
		}
		requestDone <- recorder
	}()
	<-started

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	switcher.Update("/next-dav", next)

	nextRecorder := httptest.NewRecorder()
	nextRequest := httptest.NewRequest(http.MethodGet, "/next-dav/after.txt", nil)
	if !switcher.ServeIfMatches(nextRecorder, nextRequest) {
		t.Fatal("expected request to match the replacement handler")
	}
	if nextRecorder.Code != http.StatusNoContent {
		t.Fatalf("replacement handler status = %d, want %d", nextRecorder.Code, http.StatusNoContent)
	}
	select {
	case <-previous.closed:
		t.Fatal("retired handler closed before its in-flight request drained")
	default:
	}

	close(release)
	select {
	case recorder := <-requestDone:
		if recorder.Code != http.StatusCreated {
			t.Fatalf("slow request status = %d, want %d", recorder.Code, http.StatusCreated)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the slow request")
	}
	waitForHandlerClose(t, previous.closed)
}

type blockingPathChangeWebDAVHandler struct {
	closed  chan struct{}
	started chan struct{}
	release chan struct{}
}

func (h *blockingPathChangeWebDAVHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (h *blockingPathChangeWebDAVHandler) Close() {
	close(h.closed)
}

func (h *blockingPathChangeWebDAVHandler) OnPathRenamed(string, string) {
	close(h.started)
	<-h.release
}

func (h *blockingPathChangeWebDAVHandler) OnPathDeleted(string) *storage.PathDeleteHookResult {
	close(h.started)
	<-h.release
	return &storage.PathDeleteHookResult{}
}

func TestSwitchableWebDAVHandler_UpdatePublishesBeforePathChangeCallbackDrains(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		invoke func(*switchableWebDAVHandler)
	}{
		{
			name: "rename",
			invoke: func(switcher *switchableWebDAVHandler) {
				switcher.OnPathRenamed("/old", "/new")
			},
		},
		{
			name: "delete",
			invoke: func(switcher *switchableWebDAVHandler) {
				_ = switcher.OnPathDeleted("/old")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			previous := &blockingPathChangeWebDAVHandler{
				closed:  make(chan struct{}),
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			switcher := newSwitchableWebDAVHandler("/dav", previous)

			callbackDone := make(chan struct{})
			go func() {
				testCase.invoke(switcher)
				close(callbackDone)
			}()
			<-previous.started

			switcher.Update("/next-dav", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			select {
			case <-previous.closed:
				t.Fatal("retired handler closed before the path-change callback drained")
			default:
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/next-dav/file.txt", nil)
			if !switcher.ServeIfMatches(recorder, request) {
				t.Fatal("replacement handler was not published while the callback was active")
			}
			close(previous.release)

			select {
			case <-callbackDone:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for the path-change callback")
			}
			waitForHandlerClose(t, previous.closed)
		})
	}
}

type reentrantPathChangeWebDAVHandler struct {
	switcher        *switchableWebDAVHandler
	serveStarted    chan struct{}
	runPathCallback chan struct{}
	callbackEntered chan struct{}
	closed          chan struct{}
}

func (h *reentrantPathChangeWebDAVHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	close(h.serveStarted)
	<-h.runPathCallback
	h.switcher.OnPathRenamed("/old", "/new")
	w.WriteHeader(http.StatusNoContent)
}

func (h *reentrantPathChangeWebDAVHandler) Close() {
	close(h.closed)
}

func (h *reentrantPathChangeWebDAVHandler) OnPathRenamed(string, string) {
	close(h.callbackEntered)
}

func (h *reentrantPathChangeWebDAVHandler) OnPathDeleted(string) *storage.PathDeleteHookResult {
	return nil
}

func TestSwitchableWebDAVHandler_InFlightRequestCanEnterPathCallbackWhileUpdateWaits(t *testing.T) {
	previous := &reentrantPathChangeWebDAVHandler{
		serveStarted:    make(chan struct{}),
		runPathCallback: make(chan struct{}),
		callbackEntered: make(chan struct{}),
		closed:          make(chan struct{}),
	}
	switcher := newSwitchableWebDAVHandler("/dav", previous)
	previous.switcher = switcher

	requestDone := make(chan struct{})
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/dav/file.txt", strings.NewReader("updated"))
		if !switcher.ServeIfMatches(recorder, request) {
			t.Error("expected reentrant WebDAV request to match")
		}
		if recorder.Code != http.StatusNoContent {
			t.Errorf("reentrant WebDAV status = %d, want %d", recorder.Code, http.StatusNoContent)
		}
		close(requestDone)
	}()
	<-previous.serveStarted

	updateDone := make(chan struct{})
	go func() {
		switcher.Update("", nil)
		close(updateDone)
	}()
	select {
	case <-updateDone:
	case <-time.After(time.Second):
		t.Fatal("Update did not publish while the request was active")
	}
	close(previous.runPathCallback)

	select {
	case <-previous.callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("path callback deadlocked behind the waiting update")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight request did not finish after its path callback")
	}
	waitForHandlerClose(t, previous.closed)
}

func TestApplicationHandlerRejectsCrossOriginWebDAVMutationBeforeDispatch(t *testing.T) {
	webdavCalls := 0
	routerCalls := 0
	switcher := newSwitchableWebDAVHandler("/dav", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webdavCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	handler := buildApplicationHandler(switcher, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routerCalls++
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("MKCOL", "https://nas.example.test/dav/files", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin WebDAV mutation status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if webdavCalls != 0 || routerCalls != 0 {
		t.Fatalf("cross-origin WebDAV mutation reached handlers: webdav=%d router=%d", webdavCalls, routerCalls)
	}

	basicAuthReq := httptest.NewRequest("MKCOL", "https://nas.example.test/dav/files", nil)
	basicAuthReq.SetBasicAuth("user", "pass")
	basicAuthReq.Header.Set("Origin", "https://evil.example.test")
	basicAuthRec := httptest.NewRecorder()
	handler.ServeHTTP(basicAuthRec, basicAuthReq)

	if basicAuthRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin WebDAV mutation with Basic auth status = %d, want %d; body=%s", basicAuthRec.Code, http.StatusForbidden, basicAuthRec.Body.String())
	}
	if webdavCalls != 0 || routerCalls != 0 {
		t.Fatalf("cross-origin Basic-auth WebDAV mutation reached handlers: webdav=%d router=%d", webdavCalls, routerCalls)
	}

	sameOriginReq := httptest.NewRequest("MKCOL", "https://nas.example.test/dav/files", nil)
	sameOriginReq.Header.Set("Origin", "https://nas.example.test")
	sameOriginRec := httptest.NewRecorder()
	handler.ServeHTTP(sameOriginRec, sameOriginReq)

	if sameOriginRec.Code != http.StatusNoContent {
		t.Fatalf("same-origin WebDAV mutation status = %d, want %d", sameOriginRec.Code, http.StatusNoContent)
	}
	if webdavCalls != 1 || routerCalls != 0 {
		t.Fatalf("same-origin WebDAV mutation dispatch mismatch: webdav=%d router=%d", webdavCalls, routerCalls)
	}
}

type testPathChangeWebDAVHandler struct {
	http.Handler
	renameCalls [][2]string
	deleteCalls []string
	deleteHook  *storage.PathDeleteHookResult
}

func (h *testPathChangeWebDAVHandler) OnPathRenamed(oldPath, newPath string) {
	h.renameCalls = append(h.renameCalls, [2]string{oldPath, newPath})
}

func (h *testPathChangeWebDAVHandler) OnPathDeleted(path string) *storage.PathDeleteHookResult {
	h.deleteCalls = append(h.deleteCalls, path)
	return h.deleteHook
}

type testClosableWebDAVHandler struct {
	http.Handler
	closeCalls atomic.Int32
}

func (h *testClosableWebDAVHandler) Close() error {
	h.closeCalls.Add(1)
	return nil
}

func TestSwitchableWebDAVHandler_PathChangeHooksUseLatestHandler(t *testing.T) {
	first := &testPathChangeWebDAVHandler{
		Handler:    http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		deleteHook: &storage.PathDeleteHookResult{},
	}
	second := &testPathChangeWebDAVHandler{
		Handler:    http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		deleteHook: &storage.PathDeleteHookResult{},
	}

	switcher := newSwitchableWebDAVHandler("/dav", first)
	switcher.OnPathRenamed("/docs/a.txt", "/docs/b.txt")
	if got := switcher.OnPathDeleted("/docs/b.txt"); got != first.deleteHook {
		t.Fatal("expected delete hook to be routed to initial WebDAV handler")
	}
	if len(first.renameCalls) != 1 || first.renameCalls[0] != ([2]string{"/docs/a.txt", "/docs/b.txt"}) {
		t.Fatalf("initial rename calls = %v, want [[/docs/a.txt /docs/b.txt]]", first.renameCalls)
	}
	if len(first.deleteCalls) != 1 || first.deleteCalls[0] != "/docs/b.txt" {
		t.Fatalf("initial delete calls = %v, want [/docs/b.txt]", first.deleteCalls)
	}

	switcher.Update("/new-dav", second)
	switcher.OnPathRenamed("/docs/b.txt", "/archive/b.txt")
	if got := switcher.OnPathDeleted("/archive/b.txt"); got != second.deleteHook {
		t.Fatal("expected delete hook to be routed to updated WebDAV handler")
	}
	if len(second.renameCalls) != 1 || second.renameCalls[0] != ([2]string{"/docs/b.txt", "/archive/b.txt"}) {
		t.Fatalf("updated rename calls = %v, want [[/docs/b.txt /archive/b.txt]]", second.renameCalls)
	}
	if len(second.deleteCalls) != 1 || second.deleteCalls[0] != "/archive/b.txt" {
		t.Fatalf("updated delete calls = %v, want [/archive/b.txt]", second.deleteCalls)
	}

	switcher.Update("", nil)
	switcher.OnPathRenamed("/archive/b.txt", "/archive/c.txt")
	if got := switcher.OnPathDeleted("/archive/c.txt"); got != nil {
		t.Fatalf("expected disabled switcher to return nil delete hook, got %#v", got)
	}
	if len(second.renameCalls) != 1 {
		t.Fatalf("disabled switcher should not forward more rename calls, got %v", second.renameCalls)
	}
}

func TestSwitchableWebDAVHandler_PathChangesReachEveryLiveGeneration(t *testing.T) {
	serveStarted := make(chan struct{})
	serveRelease := make(chan struct{})
	var firstRollbackCalls atomic.Int32
	var secondRollbackCalls atomic.Int32
	var rollbackMu sync.Mutex
	var rollbackOrder []string
	first := &testPathChangeWebDAVHandler{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(serveStarted)
			<-serveRelease
		}),
		deleteHook: &storage.PathDeleteHookResult{
			Rollback: func() error {
				firstRollbackCalls.Add(1)
				rollbackMu.Lock()
				rollbackOrder = append(rollbackOrder, "first")
				rollbackMu.Unlock()
				return nil
			},
		},
	}
	second := &testPathChangeWebDAVHandler{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		deleteHook: &storage.PathDeleteHookResult{
			Rollback: func() error {
				secondRollbackCalls.Add(1)
				rollbackMu.Lock()
				rollbackOrder = append(rollbackOrder, "second")
				rollbackMu.Unlock()
				return nil
			},
		},
	}
	switcher := newSwitchableWebDAVHandler("/dav", first)

	requestDone := make(chan struct{})
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/dav/file.txt", nil)
		if !switcher.ServeIfMatches(recorder, request) {
			t.Error("expected initial request to match")
		}
		close(requestDone)
	}()
	<-serveStarted
	switcher.Update("/next-dav", second)

	switcher.OnPathRenamed("/docs/a.txt", "/docs/b.txt")
	deleteResult := switcher.OnPathDeleted("/docs/b.txt")
	if deleteResult == nil || deleteResult.Rollback == nil {
		t.Fatal("expected combined delete rollback from live generations")
	}
	if err := deleteResult.Rollback(); err != nil {
		t.Fatalf("combined delete rollback error = %v", err)
	}

	if len(first.renameCalls) != 1 || len(second.renameCalls) != 1 {
		t.Fatalf("rename calls = first:%v second:%v, want one per live generation", first.renameCalls, second.renameCalls)
	}
	if len(first.deleteCalls) != 1 || len(second.deleteCalls) != 1 {
		t.Fatalf("delete calls = first:%v second:%v, want one per live generation", first.deleteCalls, second.deleteCalls)
	}
	if firstRollbackCalls.Load() != 1 || secondRollbackCalls.Load() != 1 {
		t.Fatalf(
			"rollback calls = first:%d second:%d, want one per live generation",
			firstRollbackCalls.Load(),
			secondRollbackCalls.Load(),
		)
	}
	rollbackMu.Lock()
	gotRollbackOrder := append([]string(nil), rollbackOrder...)
	rollbackMu.Unlock()
	if len(gotRollbackOrder) != 2 || gotRollbackOrder[0] != "first" || gotRollbackOrder[1] != "second" {
		t.Fatalf("rollback order = %v, want [first second]", gotRollbackOrder)
	}

	close(serveRelease)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the retired generation request")
	}
}

type sharedPathChangeTestHandler struct {
	http.Handler
	runtimeState       *webdav.RuntimeLockState
	renameMutations    atomic.Int32
	deleteMutations    atomic.Int32
	renameInvalidation atomic.Int32
	deleteInvalidation atomic.Int32
	rollbackCalls      atomic.Int32
}

func (h *sharedPathChangeTestHandler) PathChangeRuntimeState() *webdav.RuntimeLockState {
	return h.runtimeState
}

func (h *sharedPathChangeTestHandler) OnPathRenamed(oldPath, newPath string) {
	h.renameMutations.Add(1)
	h.InvalidateRenamedPathCache(oldPath, newPath)
}

func (h *sharedPathChangeTestHandler) OnPathDeleted(path string) *storage.PathDeleteHookResult {
	h.deleteMutations.Add(1)
	h.InvalidateDeletedPathCache(path)
	return &storage.PathDeleteHookResult{
		Rollback: func() error {
			h.rollbackCalls.Add(1)
			return nil
		},
	}
}

func (h *sharedPathChangeTestHandler) InvalidateRenamedPathCache(string, string) {
	h.renameInvalidation.Add(1)
}

func (h *sharedPathChangeTestHandler) InvalidateDeletedPathCache(string) {
	h.deleteInvalidation.Add(1)
}

func TestSwitchableWebDAVHandler_PathChangesMutateSharedStateOnceAndInvalidateEveryGeneration(t *testing.T) {
	runtimeState := webdav.NewRuntimeLockState()
	t.Cleanup(runtimeState.Close)
	serveStarted := make(chan struct{})
	serveRelease := make(chan struct{})
	first := &sharedPathChangeTestHandler{
		runtimeState: runtimeState,
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(serveStarted)
			<-serveRelease
		}),
	}
	second := &sharedPathChangeTestHandler{
		runtimeState: runtimeState,
		Handler:      http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
	switcher := newSwitchableWebDAVHandler("/dav", first)

	requestDone := make(chan struct{})
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/dav/file.txt", nil)
		if !switcher.ServeIfMatches(recorder, request) {
			t.Error("expected initial request to match")
		}
		close(requestDone)
	}()
	<-serveStarted
	switcher.Update("/next-dav", second)

	switcher.OnPathRenamed("/docs/a.txt", "/docs/b.txt")
	deleteResult := switcher.OnPathDeleted("/docs/b.txt")
	if deleteResult == nil || deleteResult.Rollback == nil {
		t.Fatal("expected shared-state delete rollback")
	}
	if err := deleteResult.Rollback(); err != nil {
		t.Fatalf("shared-state delete rollback error = %v", err)
	}

	if got := first.renameMutations.Load() + second.renameMutations.Load(); got != 1 {
		t.Fatalf("shared rename mutations = %d, want 1", got)
	}
	if got := first.deleteMutations.Load() + second.deleteMutations.Load(); got != 1 {
		t.Fatalf("shared delete mutations = %d, want 1", got)
	}
	if got := first.renameInvalidation.Load() + second.renameInvalidation.Load(); got != 2 {
		t.Fatalf("rename cache invalidations = %d, want 2", got)
	}
	if got := first.deleteInvalidation.Load() + second.deleteInvalidation.Load(); got != 2 {
		t.Fatalf("delete cache invalidations = %d, want 2", got)
	}
	if got := first.rollbackCalls.Load() + second.rollbackCalls.Load(); got != 1 {
		t.Fatalf("shared delete rollback calls = %d, want 1", got)
	}

	close(serveRelease)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the shared-state retired request")
	}
}

func TestSwitchableWebDAVHandler_UpdateClosesReplacedHandler(t *testing.T) {
	first := &testClosableWebDAVHandler{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	second := &testClosableWebDAVHandler{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}

	switcher := newSwitchableWebDAVHandler("/dav", first)
	if first.closeCalls.Load() != 0 {
		t.Fatalf("expected initial handler not to be closed during construction, got %d closes", first.closeCalls.Load())
	}

	switcher.Update("/new-dav", second)
	deadline := time.Now().Add(time.Second)
	for first.closeCalls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if first.closeCalls.Load() != 1 {
		t.Fatalf("expected replaced handler to be closed once, got %d closes", first.closeCalls.Load())
	}
	if second.closeCalls.Load() != 0 {
		t.Fatalf("expected active handler not to be closed, got %d closes", second.closeCalls.Load())
	}

	switcher.Update("", nil)
	deadline = time.Now().Add(time.Second)
	for second.closeCalls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if second.closeCalls.Load() != 1 {
		t.Fatalf("expected disabled handler to be closed once, got %d closes", second.closeCalls.Load())
	}

	switcher.Update("", nil)
	if second.closeCalls.Load() != 1 {
		t.Fatalf("expected nil update not to close the old handler again, got %d closes", second.closeCalls.Load())
	}
}

type concurrentSwitchableWebDAVHandler struct {
	id                uint64
	closeCalls        atomic.Int32
	activeCallbacks   atomic.Int32
	lifecycleFailures atomic.Int32
}

func (h *concurrentSwitchableWebDAVHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (h *concurrentSwitchableWebDAVHandler) WebDAVLifecycleID() uint64 {
	return h.id
}

func (h *concurrentSwitchableWebDAVHandler) OnPathRenamed(string, string) {
	h.runPathCallback()
}

func (h *concurrentSwitchableWebDAVHandler) OnPathDeleted(string) *storage.PathDeleteHookResult {
	h.runPathCallback()
	return &storage.PathDeleteHookResult{
		Rollback: func() error {
			return nil
		},
	}
}

func (h *concurrentSwitchableWebDAVHandler) runPathCallback() {
	h.activeCallbacks.Add(1)
	if h.closeCalls.Load() != 0 {
		h.lifecycleFailures.Add(1)
	}
	time.Sleep(50 * time.Microsecond)
	if h.closeCalls.Load() != 0 {
		h.lifecycleFailures.Add(1)
	}
	h.activeCallbacks.Add(-1)
}

func (h *concurrentSwitchableWebDAVHandler) Close() error {
	if h.activeCallbacks.Load() != 0 {
		h.lifecycleFailures.Add(1)
	}
	h.closeCalls.Add(1)
	return nil
}

func TestSwitchableWebDAVHandler_ConcurrentUpdatesAndPathCallbacksRetireExactlyOnce(t *testing.T) {
	const handlerCount = 32
	handlers := make([]*concurrentSwitchableWebDAVHandler, 0, handlerCount+2)
	initial := &concurrentSwitchableWebDAVHandler{id: 1}
	handlers = append(handlers, initial)
	switcher := newSwitchableWebDAVHandler("/dav", initial)

	var wg sync.WaitGroup
	for i := 0; i < handlerCount; i++ {
		handler := &concurrentSwitchableWebDAVHandler{id: uint64(i + 2)}
		handlers = append(handlers, handler)
		wg.Add(1)
		go func(index int, replacement *concurrentSwitchableWebDAVHandler) {
			defer wg.Done()
			switcher.Update(fmt.Sprintf("/dav-%d", index), replacement)
		}(i, handler)
	}
	for i := 0; i < handlerCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			switcher.OnPathRenamed(
				fmt.Sprintf("/old/%d", index),
				fmt.Sprintf("/new/%d", index),
			)
			if result := switcher.OnPathDeleted(fmt.Sprintf("/new/%d", index)); result != nil && result.Rollback != nil {
				_ = result.Rollback()
			}
		}(i)
	}
	wg.Wait()

	final := &concurrentSwitchableWebDAVHandler{id: handlerCount + 2}
	handlers = append(handlers, final)
	switcher.Update("/final-dav", final)
	switcher.Update("", nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allClosed := true
		for _, handler := range handlers {
			if handler.closeCalls.Load() != 1 {
				allClosed = false
				break
			}
		}
		if allClosed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for i, handler := range handlers {
		if got := handler.closeCalls.Load(); got != 1 {
			t.Fatalf("handler %d close calls = %d, want 1", i, got)
		}
		if failures := handler.lifecycleFailures.Load(); failures != 0 {
			t.Fatalf("handler %d lifecycle failures = %d, want 0", i, failures)
		}
	}
}

func TestCombineWebDAVPathDeleteHookResultsRejectsRestoreMetadata(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected WebDAV restore metadata invariant violation to panic")
		}
	}()
	_ = combineWebDAVPathDeleteHookResults([]*storage.PathDeleteHookResult{{
		RestoreData: []byte(`{"unexpected":true}`),
	}})
}

func TestMatchesWebDAVPrefix(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		requestPath string
		want        bool
	}{
		{name: "exact prefix matches", prefix: "/dav", requestPath: "/dav", want: true},
		{name: "child path matches", prefix: "/dav", requestPath: "/dav/files/readme.md", want: true},
		{name: "similar prefix does not match", prefix: "/dav", requestPath: "/dav2", want: false},
		{name: "similar child prefix does not match", prefix: "/dav", requestPath: "/dav-extra/file.txt", want: false},
		{name: "root prefix matches everything", prefix: "/", requestPath: "/api/v1/files", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesWebDAVPrefix(tt.prefix, tt.requestPath); got != tt.want {
				t.Fatalf("matchesWebDAVPrefix(%q, %q) = %v, want %v", tt.prefix, tt.requestPath, got, tt.want)
			}
		})
	}
}
