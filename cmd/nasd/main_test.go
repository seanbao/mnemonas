package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/seanbao/mnemonas/internal/api"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
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
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/download/file.html", nil)

	withSecurityHeaders(next).ServeHTTP(w, req)

	if got := w.Header().Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
		t.Fatalf("Content-Security-Policy = %q, want stricter downstream policy", got)
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
	prefix, handler := buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{Enabled: false, Prefix: "/dav"})
	if prefix != "" || handler != nil {
		t.Fatalf("disabled buildWebDAVHandler() = (%q, %#v), want empty and nil", prefix, handler)
	}

	prefix, handler = buildWebDAVHandler(nil, api.WebDAVRuntimeConfig{
		Enabled:  true,
		Prefix:   "/dav",
		AuthType: "basic",
		Username: "admin",
		Password: "secret",
	})
	if prefix != "/dav" {
		t.Fatalf("enabled prefix = %q, want /dav", prefix)
	}
	if handler == nil {
		t.Fatal("expected enabled WebDAV handler")
	}
	if closer, ok := handler.(io.Closer); ok {
		_ = closer.Close()
	}
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
	}{
		{name: "root", path: "/", accept: "text/html", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`},
		{name: "spa route", path: "/files/photos", accept: "text/html", wantStatus: http.StatusOK, wantBody: `<div id="root"></div>`},
		{name: "asset", path: "/assets/app.js", accept: "*/*", wantStatus: http.StatusOK, wantBody: "console.log('ok')"},
		{name: "missing asset", path: "/assets/missing.js", accept: "application/javascript", wantStatus: http.StatusNotFound, wantBody: "404"},
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
		})
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
		{name: "share spa page", method: http.MethodGet, path: "/s/share-1", accept: "text/html", want: true},
		{name: "share download", method: http.MethodGet, path: "/s/share-1/download", accept: "text/html", want: false},
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
	if got := resolveConsoleLogTimeFormat(" 15:04 "); got != "15:04" {
		t.Fatalf("custom console time format = %q, want 15:04", got)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", in: "", want: time.RFC3339},
		{name: "rfc3339", in: "rfc3339", want: time.RFC3339},
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
	closeCalls int
}

func (h *testClosableWebDAVHandler) Close() error {
	h.closeCalls++
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

func TestSwitchableWebDAVHandler_UpdateClosesReplacedHandler(t *testing.T) {
	first := &testClosableWebDAVHandler{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	second := &testClosableWebDAVHandler{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}

	switcher := newSwitchableWebDAVHandler("/dav", first)
	if first.closeCalls != 0 {
		t.Fatalf("expected initial handler not to be closed during construction, got %d closes", first.closeCalls)
	}

	switcher.Update("/new-dav", second)
	if first.closeCalls != 1 {
		t.Fatalf("expected replaced handler to be closed once, got %d closes", first.closeCalls)
	}
	if second.closeCalls != 0 {
		t.Fatalf("expected active handler not to be closed, got %d closes", second.closeCalls)
	}

	switcher.Update("", nil)
	if second.closeCalls != 1 {
		t.Fatalf("expected disabled handler to be closed once, got %d closes", second.closeCalls)
	}

	switcher.Update("", nil)
	if second.closeCalls != 1 {
		t.Fatalf("expected nil update not to close the old handler again, got %d closes", second.closeCalls)
	}
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
