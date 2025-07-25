// Package config provides configuration management for MnemoNAS
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupManagedTempPath_ReturnsOperationError(t *testing.T) {
	tmpDir := t.TempDir()
	busyDir := filepath.Join(tmpDir, "busy")
	if err := os.Mkdir(busyDir, 0700); err != nil {
		t.Fatalf("failed to create busy temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busyDir, "child"), []byte("data"), 0600); err != nil {
		t.Fatalf("failed to create busy temp child: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to open root: %v", err)
	}
	defer root.Close()

	operationErr := errors.New("write failed")
	if got := cleanupManagedTempPath(root, "busy", operationErr); got != operationErr {
		t.Fatalf("cleanupManagedTempPath() = %v, want original operation error", got)
	}
	if _, err := os.Stat(busyDir); err != nil {
		t.Fatalf("expected busy temp path to remain after ignored cleanup error: %v", err)
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Server.Port != 8080 {
		t.Errorf("Default port = %d, want 8080", cfg.Server.Port)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Default host = %s, want 0.0.0.0", cfg.Server.Host)
	}

	if cfg.Server.TrustedProxyHops != 0 {
		t.Errorf("Default trusted proxy hops = %d, want 0", cfg.Server.TrustedProxyHops)
	}

	if cfg.Storage.Root == "" {
		t.Error("Default storage.root should not be empty")
	}

	if cfg.DataPlane.GRPCAddress != "127.0.0.1:9090" {
		t.Errorf("Default gRPC address = %s, want 127.0.0.1:9090", cfg.DataPlane.GRPCAddress)
	}

	if cfg.WebDAV.Prefix != "/dav" {
		t.Errorf("Default WebDAV prefix = %s, want /dav", cfg.WebDAV.Prefix)
	}

	internalRoot := filepath.Join(cfg.Storage.Root, ".mnemonas")
	if cfg.Server.TLS.CertDir != filepath.Join(internalRoot, "certs") {
		t.Errorf("Default cert dir = %s, want %s", cfg.Server.TLS.CertDir, filepath.Join(internalRoot, "certs"))
	}
	if cfg.Auth.UsersFile != filepath.Join(internalRoot, "users.json") {
		t.Errorf("Default users file = %s, want %s", cfg.Auth.UsersFile, filepath.Join(internalRoot, "users.json"))
	}
	if cfg.Share.StoreFile != filepath.Join(internalRoot, "shares.json") {
		t.Errorf("Default share store = %s, want %s", cfg.Share.StoreFile, filepath.Join(internalRoot, "shares.json"))
	}
	if cfg.Favorites.StoreFile != filepath.Join(internalRoot, "favorites.json") {
		t.Errorf("Default favorites store = %s, want %s", cfg.Favorites.StoreFile, filepath.Join(internalRoot, "favorites.json"))
	}
	if cfg.Storage.Versioning.AutoVersionedExtensions == nil {
		t.Error("Default versioning extensions should be initialized to an empty or populated slice")
	}
	if cfg.Storage.Versioning.AutoVersionedFilenames == nil {
		t.Error("Default versioning filenames should be initialized to an empty or populated slice")
	}
	if cfg.Alerts.WebhookHeaders == nil {
		t.Error("Default alerts webhook headers should be initialized to an empty slice")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "Default is valid",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "Invalid port zero",
			modify:  func(c *Config) { c.Server.Port = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid port negative",
			modify:  func(c *Config) { c.Server.Port = -1 },
			wantErr: true,
		},
		{
			name:    "Invalid port too large",
			modify:  func(c *Config) { c.Server.Port = 70000 },
			wantErr: true,
		},
		{
			name:    "Invalid server host whitespace",
			modify:  func(c *Config) { c.Server.Host = "127.0.0.1\nbad" },
			wantErr: true,
		},
		{
			name:    "Invalid server host with port",
			modify:  func(c *Config) { c.Server.Host = "[::1]:8080" },
			wantErr: true,
		},
		{
			name:    "Valid server host wildcard",
			modify:  func(c *Config) { c.Server.Host = "*" },
			wantErr: false,
		},
		{
			name:    "Valid server host IPv6",
			modify:  func(c *Config) { c.Server.Host = "::1" },
			wantErr: false,
		},
		{
			name:    "Invalid read timeout",
			modify:  func(c *Config) { c.Server.ReadTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid write timeout",
			modify:  func(c *Config) { c.Server.WriteTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid idle timeout",
			modify:  func(c *Config) { c.Server.IdleTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid trusted proxy hops",
			modify:  func(c *Config) { c.Server.TrustedProxyHops = -1 },
			wantErr: true,
		},
		{
			name:    "Empty storage.root",
			modify:  func(c *Config) { c.Storage.Root = "" },
			wantErr: true,
		},
		{
			name:    "Filesystem root storage.root",
			modify:  func(c *Config) { c.Storage.Root = string(os.PathSeparator) },
			wantErr: true,
		},
		{
			name:    "Protected storage.root",
			modify:  func(c *Config) { c.Storage.Root = "/srv" },
			wantErr: true,
		},
		{
			name:    "Protected storage.root with trailing separator",
			modify:  func(c *Config) { c.Storage.Root = "/tmp/" },
			wantErr: true,
		},
		{
			name:    "Negative trash retention days",
			modify:  func(c *Config) { c.Storage.Trash.RetentionDays = -1 },
			wantErr: true,
		},
		{
			name:    "Negative retention max versions",
			modify:  func(c *Config) { c.Storage.Retention.MaxVersions = -1 },
			wantErr: true,
		},
		{
			name:    "Negative retention max age",
			modify:  func(c *Config) { c.Storage.Retention.MaxAge = -1 * time.Hour },
			wantErr: true,
		},
		{
			name:    "Negative retention gc interval",
			modify:  func(c *Config) { c.Storage.Retention.GCInterval = -1 * time.Hour },
			wantErr: true,
		},
		{
			name:    "Invalid trash max size",
			modify:  func(c *Config) { c.Storage.Trash.MaxSize = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid versioning max size",
			modify:  func(c *Config) { c.Storage.Versioning.MaxVersionedSize = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV auth type",
			modify:  func(c *Config) { c.WebDAV.AuthType = "token" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV root prefix",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV API prefix",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/api/v1" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV public share prefix",
			modify:  func(c *Config) { c.WebDAV.Prefix = "s/files" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV health prefix",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/health" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV prefix with backslash",
			modify:  func(c *Config) { c.WebDAV.Prefix = `/dav\files` },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV prefix with query",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/dav?mount" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV prefix with fragment",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/dav#mount" },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV prefix with control character",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/dav\nfiles" },
			wantErr: true,
		},
		{
			name: "Disabled WebDAV may keep a reserved inactive prefix",
			modify: func(c *Config) {
				c.WebDAV.Enabled = false
				c.WebDAV.Prefix = "/api/v1"
			},
			wantErr: false,
		},
		{
			name:    "Invalid share base URL scheme",
			modify:  func(c *Config) { c.Share.BaseURL = "javascript:alert(1)" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL relative",
			modify:  func(c *Config) { c.Share.BaseURL = "/s/base" },
			wantErr: true,
		},
		{
			name:    "Valid share base URL",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com" },
			wantErr: false,
		},
		{
			name:    "Invalid auth access token ttl",
			modify:  func(c *Config) { c.Auth.AccessTokenTTL = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid auth refresh token ttl",
			modify:  func(c *Config) { c.Auth.RefreshTokenTTL = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid short explicit JWT secret",
			modify:  func(c *Config) { c.Auth.JWTSecret = "short-secret" },
			wantErr: true,
		},
		{
			name:    "Valid explicit JWT secret",
			modify:  func(c *Config) { c.Auth.JWTSecret = strings.Repeat("a", 32) },
			wantErr: false,
		},
		{
			name: "Auth disabled beyond loopback requires explicit unsafe override",
			modify: func(c *Config) {
				c.Server.Host = "0.0.0.0"
				c.Auth.Enabled = false
			},
			wantErr: true,
		},
		{
			name: "Auth disabled on loopback is valid for local development",
			modify: func(c *Config) {
				c.Server.Host = "127.0.0.1"
				c.Auth.Enabled = false
			},
			wantErr: false,
		},
		{
			name: "WebDAV without auth beyond loopback requires explicit unsafe override",
			modify: func(c *Config) {
				c.Server.Host = "0.0.0.0"
				c.WebDAV.Enabled = true
				c.WebDAV.AuthType = "none"
			},
			wantErr: true,
		},
		{
			name: "Unsafe no-auth override allows explicit public unauthenticated bind",
			modify: func(c *Config) {
				c.Server.Host = "0.0.0.0"
				c.Auth.Enabled = false
				c.WebDAV.Enabled = true
				c.WebDAV.AuthType = "none"
				c.Security.AllowUnsafeNoAuth = true
			},
			wantErr: false,
		},
		{
			name: "Invalid versioning extension entry",
			modify: func(c *Config) {
				c.Storage.Versioning.AutoVersionedExtensions = []string{"txt"}
			},
			wantErr: true,
		},
		{
			name: "Invalid versioning filename entry",
			modify: func(c *Config) {
				c.Storage.Versioning.AutoVersionedFilenames = []string{"README", "   "}
			},
			wantErr: true,
		},
		{
			name:    "Empty gRPC address",
			modify:  func(c *Config) { c.DataPlane.GRPCAddress = "" },
			wantErr: true,
		},
		{
			name:    "Invalid dataplane timeout",
			modify:  func(c *Config) { c.DataPlane.Timeout = 0 },
			wantErr: true,
		},
		{
			name:    "Negative dataplane max retries",
			modify:  func(c *Config) { c.DataPlane.MaxRetries = -1 },
			wantErr: true,
		},
		{
			name: "Invalid CDC zero min",
			modify: func(c *Config) {
				c.DataPlane.CDC.MinChunkSize = 0
			},
			wantErr: true,
		},
		{
			name: "Invalid CDC min >= avg",
			modify: func(c *Config) {
				c.DataPlane.CDC.MinChunkSize = 1024 * 1024
				c.DataPlane.CDC.AvgChunkSize = 512 * 1024
			},
			wantErr: true,
		},
		{
			name: "Invalid CDC min below safety floor",
			modify: func(c *Config) {
				c.DataPlane.CDC.MinChunkSize = MinCDCChunkSize - 1
				c.DataPlane.CDC.AvgChunkSize = 256 * 1024
				c.DataPlane.CDC.MaxChunkSize = 1024 * 1024
			},
			wantErr: true,
		},
		{
			name: "Invalid CDC avg >= max",
			modify: func(c *Config) {
				c.DataPlane.CDC.AvgChunkSize = 5 * 1024 * 1024
				c.DataPlane.CDC.MaxChunkSize = 4 * 1024 * 1024
			},
			wantErr: true,
		},
		{
			name: "Invalid CDC max exceeds memory safety cap",
			modify: func(c *Config) {
				c.DataPlane.CDC.MinChunkSize = 16 * 1024 * 1024
				c.DataPlane.CDC.AvgChunkSize = 32 * 1024 * 1024
				c.DataPlane.CDC.MaxChunkSize = MaxCDCChunkSize + 1
			},
			wantErr: true,
		},
		{
			name: "Invalid dataplane gRPC address missing port",
			modify: func(c *Config) {
				c.DataPlane.GRPCAddress = "127.0.0.1"
			},
			wantErr: true,
		},
		{
			name: "Invalid dataplane gRPC address control character",
			modify: func(c *Config) {
				c.DataPlane.GRPCAddress = "127.0.0.1:9090\nlog_level=debug"
			},
			wantErr: true,
		},
		{
			name: "Invalid dataplane gRPC address port",
			modify: func(c *Config) {
				c.DataPlane.GRPCAddress = "127.0.0.1:70000"
			},
			wantErr: true,
		},
		{
			name: "Invalid dataplane gRPC address host",
			modify: func(c *Config) {
				c.DataPlane.GRPCAddress = "bad/host:9090"
			},
			wantErr: true,
		},
		{
			name: "Valid dataplane gRPC hostname",
			modify: func(c *Config) {
				c.DataPlane.GRPCAddress = "localhost:9090"
			},
			wantErr: false,
		},
		{
			name: "Invalid alerts webhook method",
			modify: func(c *Config) {
				c.Alerts.WebhookMethod = "PATCH"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook URL scheme",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "file:///tmp/alert"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook URL host",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "https:///alert"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook URL whitespace",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "https://hooks.example.com/alert\n"
			},
			wantErr: true,
		},
		{
			name: "Valid alerts webhook URL",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "https://hooks.example.com/storage"
			},
			wantErr: false,
		},
		{
			name: "Invalid alerts webhook header",
			modify: func(c *Config) {
				c.Alerts.WebhookHeaders = []string{"Authorization"}
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook header name",
			modify: func(c *Config) {
				c.Alerts.WebhookHeaders = []string{"Bad Header: value"}
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook header value control character",
			modify: func(c *Config) {
				c.Alerts.WebhookHeaders = []string{"X-MnemoNAS: ok\r\nX-Evil: injected"}
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts critical threshold below warning",
			modify: func(c *Config) {
				c.Alerts.ThresholdPct = 90
				c.Alerts.CriticalPct = 80
			},
			wantErr: true,
		},
		{
			name:    "Invalid log level",
			modify:  func(c *Config) { c.Log.Level = "trace" },
			wantErr: true,
		},
		{
			name:    "Invalid log format",
			modify:  func(c *Config) { c.Log.Format = "text" },
			wantErr: true,
		},
		{
			name:    "Invalid empty log output",
			modify:  func(c *Config) { c.Log.Output = "   " },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeWebDAVPrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "adds leading slash", input: "dav", expected: "/dav"},
		{name: "trims trailing slash", input: "/dav/", expected: "/dav"},
		{name: "empty defaults to root", input: "", expected: "/"},
		{name: "root stays root", input: "/", expected: "/"},
		{name: "trims whitespace", input: " /dav ", expected: "/dav"},
		{name: "trims whitespace after clean", input: "./0/0//0 /", expected: "/0/0/0"},
		{name: "cleans after trimming path segment whitespace", input: "00/ /", expected: "/00"},
		{name: "iterates until stable after clean", input: "0 / /", expected: "/0"},
		{name: "collapses repeated slashes", input: "//dav//files///", expected: "/dav/files"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWebDAVPrefix(tt.input); got != tt.expected {
				t.Fatalf("NormalizeWebDAVPrefix(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestLoad_NormalizesWebDAVPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[webdav]
prefix = "dav/"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.WebDAV.Prefix != "/dav" {
		t.Fatalf("expected normalized prefix /dav, got %q", cfg.WebDAV.Prefix)
	}
}

func TestLoad_ParsesDurationStrings(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[server]
read_timeout = "45s"
write_timeout = "90s"
idle_timeout = "3m"

[storage.retention]
max_age = "720h"
gc_interval = "12h"

[dataplane]
timeout = "45s"

[auth]
access_token_ttl = "20m"
refresh_token_ttl = "240h"

[alerts]
check_interval = "2h"
cooldown_period = "6h"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.ReadTimeout != 45*time.Second {
		t.Fatalf("expected read timeout 45s, got %s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 90*time.Second {
		t.Fatalf("expected write timeout 90s, got %s", cfg.Server.WriteTimeout)
	}
	if cfg.Server.IdleTimeout != 3*time.Minute {
		t.Fatalf("expected idle timeout 3m, got %s", cfg.Server.IdleTimeout)
	}
	if cfg.Storage.Retention.MaxAge != 720*time.Hour {
		t.Fatalf("expected max age 720h, got %s", cfg.Storage.Retention.MaxAge)
	}
	if cfg.Storage.Retention.GCInterval != 12*time.Hour {
		t.Fatalf("expected GC interval 12h, got %s", cfg.Storage.Retention.GCInterval)
	}
	if cfg.DataPlane.Timeout != 45*time.Second {
		t.Fatalf("expected dataplane timeout 45s, got %s", cfg.DataPlane.Timeout)
	}
	if cfg.Auth.AccessTokenTTL != 20*time.Minute {
		t.Fatalf("expected access token ttl 20m, got %s", cfg.Auth.AccessTokenTTL)
	}
	if cfg.Auth.RefreshTokenTTL != 240*time.Hour {
		t.Fatalf("expected refresh token ttl 240h, got %s", cfg.Auth.RefreshTokenTTL)
	}
	if cfg.Alerts.CheckInterval != 2*time.Hour {
		t.Fatalf("expected alerts check interval 2h, got %s", cfg.Alerts.CheckInterval)
	}
	if cfg.Alerts.CooldownPeriod != 6*time.Hour {
		t.Fatalf("expected alerts cooldown 6h, got %s", cfg.Alerts.CooldownPeriod)
	}
}

func TestLoad_ParsesTrustedProxyHops(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[server]
trusted_proxy_hops = 2
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.TrustedProxyHops != 2 {
		t.Fatalf("trusted proxy hops = %d, want 2", cfg.Server.TrustedProxyHops)
	}
}

func TestLoad_NormalizesOptionalURLFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[share]
base_url = " https://nas.example.com/base/ "

[alerts]
webhook_url = " https://hooks.example.com/storage "
webhook_method = "post"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Share.BaseURL != "https://nas.example.com/base/" {
		t.Fatalf("share base URL = %q, want trimmed URL", cfg.Share.BaseURL)
	}
	if cfg.Alerts.WebhookURL != "https://hooks.example.com/storage" {
		t.Fatalf("alerts webhook URL = %q, want trimmed URL", cfg.Alerts.WebhookURL)
	}
	if cfg.Alerts.WebhookMethod != "POST" {
		t.Fatalf("alerts webhook method = %q, want POST", cfg.Alerts.WebhookMethod)
	}
}

func TestLoad_ExpandsHomeDirectoryInStorageRootAndDerivedPaths(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := []byte(`
[storage]
root = "~/.mnemonas-custom"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	wantRoot := filepath.Join(homeDir, ".mnemonas-custom")
	wantInternal := filepath.Join(wantRoot, ".mnemonas")
	if cfg.Storage.Root != wantRoot {
		t.Fatalf("storage root = %q, want %q", cfg.Storage.Root, wantRoot)
	}
	if cfg.Server.TLS.CertDir != filepath.Join(wantInternal, "certs") {
		t.Fatalf("cert dir = %q, want %q", cfg.Server.TLS.CertDir, filepath.Join(wantInternal, "certs"))
	}
	if cfg.Auth.UsersFile != filepath.Join(wantInternal, "users.json") {
		t.Fatalf("users file = %q, want %q", cfg.Auth.UsersFile, filepath.Join(wantInternal, "users.json"))
	}
	if cfg.Share.StoreFile != filepath.Join(wantInternal, "shares.json") {
		t.Fatalf("share store = %q, want %q", cfg.Share.StoreFile, filepath.Join(wantInternal, "shares.json"))
	}
	if cfg.Favorites.StoreFile != filepath.Join(wantInternal, "favorites.json") {
		t.Fatalf("favorites store = %q, want %q", cfg.Favorites.StoreFile, filepath.Join(wantInternal, "favorites.json"))
	}
}

func TestLoad_ExpandsExplicitHomeRelativePaths(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := []byte(`
[server.tls]
cert_dir = "~/tls"
cert_file = "~/tls/server.crt"
key_file = "~/tls/server.key"

[auth]
users_file = "~/data/users.json"

[share]
store_file = "~/data/shares.json"

[favorites]
store_file = "~/data/favorites.json"

[log]
output = "~/logs/mnemonas.log"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.TLS.CertDir != filepath.Join(homeDir, "tls") {
		t.Fatalf("cert dir = %q, want %q", cfg.Server.TLS.CertDir, filepath.Join(homeDir, "tls"))
	}
	if cfg.Server.TLS.CertFile != filepath.Join(homeDir, "tls", "server.crt") {
		t.Fatalf("cert file = %q, want %q", cfg.Server.TLS.CertFile, filepath.Join(homeDir, "tls", "server.crt"))
	}
	if cfg.Server.TLS.KeyFile != filepath.Join(homeDir, "tls", "server.key") {
		t.Fatalf("key file = %q, want %q", cfg.Server.TLS.KeyFile, filepath.Join(homeDir, "tls", "server.key"))
	}
	if cfg.Auth.UsersFile != filepath.Join(homeDir, "data", "users.json") {
		t.Fatalf("users file = %q, want %q", cfg.Auth.UsersFile, filepath.Join(homeDir, "data", "users.json"))
	}
	if cfg.Share.StoreFile != filepath.Join(homeDir, "data", "shares.json") {
		t.Fatalf("share store = %q, want %q", cfg.Share.StoreFile, filepath.Join(homeDir, "data", "shares.json"))
	}
	if cfg.Favorites.StoreFile != filepath.Join(homeDir, "data", "favorites.json") {
		t.Fatalf("favorites store = %q, want %q", cfg.Favorites.StoreFile, filepath.Join(homeDir, "data", "favorites.json"))
	}
	if cfg.Log.Output != filepath.Join(homeDir, "logs", "mnemonas.log") {
		t.Fatalf("log output = %q, want %q", cfg.Log.Output, filepath.Join(homeDir, "logs", "mnemonas.log"))
	}
}

func TestLoad_ExampleConfig(t *testing.T) {
	examplePath := filepath.Join("..", "..", "mnemonas.example.toml")
	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("failed to read example config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "mnemonas.example.toml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("failed to copy example config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Fatalf("expected read timeout 30s, got %s", cfg.Server.ReadTimeout)
	}
	if cfg.Storage.Retention.MaxAge != 2160*time.Hour {
		t.Fatalf("expected max age 2160h, got %s", cfg.Storage.Retention.MaxAge)
	}
	if cfg.Auth.AccessTokenTTL != 15*time.Minute {
		t.Fatalf("expected access token ttl 15m, got %s", cfg.Auth.AccessTokenTTL)
	}
	if cfg.Alerts.CheckInterval != time.Hour {
		t.Fatalf("expected alerts check interval 1h, got %s", cfg.Alerts.CheckInterval)
	}
}

func TestConfig_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config", "config.toml")

	cfg := Default()
	cfg.Server.Port = 9999
	cfg.Log.Level = "debug"

	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}
	stat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("failed to stat config file: %v", err)
	}
	if got := stat.Mode().Perm(); got != configFileMode {
		t.Fatalf("saved config mode = %v, want %v", got, configFileMode)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Server.Port != 9999 {
		t.Errorf("Loaded port = %d, want 9999", loaded.Server.Port)
	}

	if loaded.Log.Level != "debug" {
		t.Errorf("Loaded log level = %s, want debug", loaded.Log.Level)
	}
}

func TestLoad_TightensConfigFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	cfg := Default()
	cfg.Server.Port = 9091
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err := os.Chmod(configPath, 0644); err != nil {
		t.Fatalf("failed to loosen config permissions: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Server.Port != 9091 {
		t.Fatalf("loaded server port = %d, want 9091", loaded.Server.Port)
	}
	stat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("failed to stat config file: %v", err)
	}
	if got := stat.Mode().Perm(); got != configFileMode {
		t.Fatalf("loaded config mode = %v, want %v", got, configFileMode)
	}
}

func TestConfig_Save_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	originalSyncManagedRootDir := syncManagedRootDir
	syncManagedRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncManagedRootDir = originalSyncManagedRootDir
	}()

	cfg := Default()
	cfg.Server.Port = 9999
	err := cfg.Save(configPath)
	if err == nil {
		t.Fatal("expected Save() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync config directory") {
		t.Fatalf("expected config directory sync error, got %v", err)
	}

	loaded, loadErr := Load(configPath)
	if loadErr != nil {
		t.Fatalf("expected config file to remain readable after sync failure, got %v", loadErr)
	}
	if loaded.Server.Port != 9999 {
		t.Fatalf("expected saved config to persist despite sync failure, got port %d", loaded.Server.Port)
	}
}

func TestConfig_Save_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nested", "config", "config.toml")

	originalSyncManagedDir := syncManagedDir
	syncManagedDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncManagedDir = originalSyncManagedDir
	}()

	cfg := Default()
	if err := cfg.Save(configPath); err == nil {
		t.Fatal("expected Save() to fail when directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync managed directory tree") {
		t.Fatalf("expected managed directory tree sync error, got %v", err)
	}

	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no config file to be created, got %v", statErr)
	}
}

func TestConfig_Save_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nested", "config", "config.toml")
	configDir := filepath.Dir(configPath)

	originalHook := afterValidateManagedFilePath
	var hookErr error
	hookApplied := false
	afterValidateManagedFilePath = func() {
		if hookApplied || hookErr != nil {
			return
		}
		hookApplied = true
		hookErr = os.Chmod(configDir, 0500)
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
		_ = os.Chmod(configDir, 0755)
	}()

	cfg := Default()
	err := cfg.Save(configPath)
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err == nil {
		t.Fatal("expected Save() to fail when temp file creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create temp config file") {
		t.Fatalf("expected temp create error, got %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no config file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(configDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created config directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "nested")); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent directory to be removed, got %v", statErr)
	}

	afterValidateManagedFilePath = originalHook
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("expected retry after failed save cleanup to succeed, got %v", err)
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load() should not error for non-existent file: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("Port = %d, want default 8080", cfg.Server.Port)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.toml")

	if err := os.WriteFile(configPath, []byte("this is not valid [toml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Load() should error for invalid TOML")
	}
}

func TestConfig_Save_RejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target.toml")
	configPath := filepath.Join(tmpDir, "config.toml")

	if err := os.WriteFile(targetPath, []byte("keep = 'original'\n"), 0644); err != nil {
		t.Fatalf("failed to seed target config: %v", err)
	}
	if err := os.Symlink(targetPath, configPath); err != nil {
		t.Fatalf("failed to create config symlink: %v", err)
	}

	err := Default().Save(configPath)
	if !errors.Is(err, errConfigFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target config: %v", err)
	}
	if string(data) != "keep = 'original'\n" {
		t.Fatalf("expected target config to remain unchanged, got %q", string(data))
	}
}

func TestLoad_RejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target.toml")
	configPath := filepath.Join(tmpDir, "config.toml")

	if err := os.WriteFile(targetPath, []byte("[server]\nport = 8081\n"), 0644); err != nil {
		t.Fatalf("failed to seed target config: %v", err)
	}
	if err := os.Symlink(targetPath, configPath); err != nil {
		t.Fatalf("failed to create config symlink: %v", err)
	}

	_, err := Load(configPath)
	if !errors.Is(err, errConfigFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestConfig_Save_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-config")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real config dir: %v", err)
	}
	targetPath := filepath.Join(realDir, "config.toml")
	if err := os.WriteFile(targetPath, []byte("keep = 'original'\n"), 0644); err != nil {
		t.Fatalf("failed to seed target config: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-config")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create config dir symlink: %v", err)
	}

	err := Default().Save(filepath.Join(linkedDir, "config.toml"))
	if !errors.Is(err, errConfigFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target config: %v", err)
	}
	if string(data) != "keep = 'original'\n" {
		t.Fatalf("expected target config to remain unchanged, got %q", string(data))
	}
}

func TestLoad_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-config")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real config dir: %v", err)
	}
	targetPath := filepath.Join(realDir, "config.toml")
	if err := os.WriteFile(targetPath, []byte("[server]\nport = 8081\n"), 0644); err != nil {
		t.Fatalf("failed to seed target config: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-config")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create config dir symlink: %v", err)
	}

	_, err := Load(filepath.Join(linkedDir, "config.toml"))
	if !errors.Is(err, errConfigFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
}

func TestLoad_RejectsSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(config) error: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nport = 18080\n"), 0600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}
	linkedTarget := filepath.Join(configDir, "linked.toml")
	if err := os.WriteFile(linkedTarget, []byte("[server]\nport = 18081\n"), 0600); err != nil {
		t.Fatalf("WriteFile(linked) error: %v", err)
	}

	originalHook := afterValidateManagedFilePath
	var hookErr error
	swapped := false
	afterValidateManagedFilePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		if err := os.Remove(configPath); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(filepath.Base(linkedTarget), configPath)
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
	}()

	_, err := Load(configPath)
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if !errors.Is(err, errConfigFileSymlink) {
		t.Fatalf("expected config symlink rejection, got %v", err)
	}
}

func TestConfig_Save_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	configDir := filepath.Join(baseDir, "config")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	outsidePath := filepath.Join(outsideDir, "config.toml")
	if err := os.WriteFile(outsidePath, []byte("keep = 'outside'\n"), 0644); err != nil {
		t.Fatalf("failed to seed outside config: %v", err)
	}

	originalHook := afterValidateManagedFilePath
	var hookErr error
	afterValidateManagedFilePath = func() {
		if hookErr != nil {
			return
		}
		backupDir := filepath.Join(baseDir, "config-backup")
		if err := os.Rename(configDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, configDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
	}()

	cfg := Default()
	cfg.Server.Port = 9090
	err := cfg.Save(configPath)
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected save to stay bound to the original directory, got %v", err)
	}

	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("failed to read outside config: %v", readErr)
	}
	if string(data) != "keep = 'outside'\n" {
		t.Fatalf("expected outside config to remain unchanged, got %q", string(data))
	}

	loaded, loadErr := Load(filepath.Join(baseDir, "config-backup", "config.toml"))
	if loadErr != nil {
		t.Fatalf("failed to load config written through original directory root: %v", loadErr)
	}
	if loaded.Server.Port != 9090 {
		t.Fatalf("expected saved config to remain bound to original directory, got port %d", loaded.Server.Port)
	}
}

func TestLoad_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	configDir := filepath.Join(baseDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nport = 9090\n"), 0644); err != nil {
		t.Fatalf("failed to seed config: %v", err)
	}
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "config.toml"), []byte("[server]\nport = 8081\n"), 0644); err != nil {
		t.Fatalf("failed to seed outside config: %v", err)
	}

	originalHook := afterValidateManagedFilePath
	var hookErr error
	afterValidateManagedFilePath = func() {
		if hookErr != nil {
			return
		}
		backupDir := filepath.Join(baseDir, "config-backup")
		if err := os.Rename(configDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, configDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
	}()

	loaded, err := Load(configPath)
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected load to stay bound to the original directory, got %v", err)
	}
	if loaded.Server.Port != 9090 {
		t.Fatalf("expected config load to ignore the swapped symlink target, got port %d", loaded.Server.Port)
	}
}

func TestConfig_EnsureDirs(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Default()
	// Set the storage root to use the temp directory
	cfg.Storage.Root = tmpDir

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs() error: %v", err)
	}

	// Check new directory structure
	dirs := []string{
		filepath.Join(tmpDir, "files"),
		filepath.Join(tmpDir, ".mnemonas"),
		filepath.Join(tmpDir, ".mnemonas", "objects"),
		filepath.Join(tmpDir, ".mnemonas", "trash"),
		filepath.Join(tmpDir, ".mnemonas", "thumbnails"),
		filepath.Join(tmpDir, ".mnemonas", "maintenance"),
		filepath.Join(tmpDir, ".mnemonas", "activity"),
		filepath.Join(tmpDir, ".mnemonas", "tmp"),
	}

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}

	wantModes := map[string]os.FileMode{
		tmpDir:                                 0750,
		filepath.Join(tmpDir, "files"):         0750,
		filepath.Join(tmpDir, ".mnemonas"):     0700,
		filepath.Join(tmpDir, ".mnemonas/tmp"): 0700,
	}
	for dir, wantMode := range wantModes {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Fatalf("mode for %s = %o, want %o", dir, got, wantMode)
		}
	}
}

func TestConfig_EnsureDirs_RejectsProtectedStorageRoot(t *testing.T) {
	cfg := Default()
	cfg.Storage.Root = "/tmp/"

	err := cfg.EnsureDirs()
	if err == nil || !strings.Contains(err.Error(), "protected system directory") {
		t.Fatalf("EnsureDirs() error = %v, want protected system directory rejection", err)
	}
}

func TestConfig_EnsureDirs_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("failed to create real parent: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("failed to create linked parent: %v", err)
	}

	cfg := Default()
	cfg.Storage.Root = filepath.Join(linkedParent, "storage-root")

	err := cfg.EnsureDirs()
	if !errors.Is(err, errManagedDirectorySymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestConfig_EnsureDirs_DoesNotCreateDirectoriesThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("failed to create real parent: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("failed to create linked parent: %v", err)
	}

	cfg := Default()
	cfg.Storage.Root = filepath.Join(linkedParent, "storage-root")

	err := cfg.EnsureDirs()
	if !errors.Is(err, errManagedDirectorySymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "storage-root")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("storage root created through symlink parent, stat error = %v", statErr)
	}
}

func TestConfig_Address(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "IPv4", host: "192.168.1.1", port: 3000, want: "192.168.1.1:3000"},
		{name: "IPv6", host: "::1", port: 3000, want: "[::1]:3000"},
		{name: "bracketed IPv6", host: "[::1]", port: 3000, want: "[::1]:3000"},
		{name: "wildcard IPv6", host: "::", port: 3000, want: "[::]:3000"},
		{name: "wildcard alias", host: "*", port: 3000, want: ":3000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Server.Host = tt.host
			cfg.Server.Port = tt.port

			if addr := cfg.Address(); addr != tt.want {
				t.Errorf("Address() = %s, want %s", addr, tt.want)
			}
		})
	}
}

func TestConfig_DerivedStoragePaths(t *testing.T) {
	cfg := Default()
	cfg.Storage.Root = filepath.Join(t.TempDir(), "storage")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "files", got: cfg.FilesDir(), want: filepath.Join(cfg.Storage.Root, "files")},
		{name: "internal", got: cfg.InternalDir(), want: filepath.Join(cfg.Storage.Root, ".mnemonas")},
		{name: "index", got: cfg.IndexDBPath(), want: filepath.Join(cfg.Storage.Root, ".mnemonas", "index.db")},
		{name: "objects", got: cfg.ObjectsDir(), want: filepath.Join(cfg.Storage.Root, ".mnemonas", "objects")},
		{name: "trash", got: cfg.TrashDir(), want: filepath.Join(cfg.Storage.Root, ".mnemonas", "trash")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("path = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestDataPlaneConfig_Address(t *testing.T) {
	cfg := Default()
	cfg.DataPlane.GRPCAddress = "custom:1234"

	addr := cfg.DataPlane.Address()
	if addr != "custom:1234" {
		t.Errorf("DataPlane.Address() = %s, want custom:1234", addr)
	}
}

func TestConfig_TimeoutValues(t *testing.T) {
	cfg := Default()

	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", cfg.Server.ReadTimeout)
	}

	if cfg.Server.WriteTimeout != 60*time.Second {
		t.Errorf("WriteTimeout = %v, want 60s", cfg.Server.WriteTimeout)
	}

	if cfg.Storage.Retention.GCInterval != 24*time.Hour {
		t.Errorf("GCInterval = %v, want 24h", cfg.Storage.Retention.GCInterval)
	}
}

func TestConfig_RetentionDefaults(t *testing.T) {
	cfg := Default()

	if cfg.Storage.Retention.MaxVersions != 50 {
		t.Errorf("MaxVersions = %d, want 50", cfg.Storage.Retention.MaxVersions)
	}

	if cfg.Storage.Retention.MinFreeSpace != 10*1024*1024*1024 {
		t.Errorf("MinFreeSpace = %d, want 10GB", cfg.Storage.Retention.MinFreeSpace)
	}
}

func TestConfig_CDCDefaults(t *testing.T) {
	cfg := Default()

	if cfg.DataPlane.CDC.MinChunkSize != 256*1024 {
		t.Errorf("MinChunkSize = %d, want 256KB", cfg.DataPlane.CDC.MinChunkSize)
	}

	if cfg.DataPlane.CDC.AvgChunkSize != 1024*1024 {
		t.Errorf("AvgChunkSize = %d, want 1MB", cfg.DataPlane.CDC.AvgChunkSize)
	}

	if cfg.DataPlane.CDC.MaxChunkSize != 4*1024*1024 {
		t.Errorf("MaxChunkSize = %d, want 4MB", cfg.DataPlane.CDC.MaxChunkSize)
	}
}
