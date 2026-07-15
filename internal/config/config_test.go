// Package config provides configuration management for MnemoNAS
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/versionstore"
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

func TestConfigSaveRetriesTempNameCollision(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	collisionPath := filepath.Join(tmpDir, ".config-collision.tmp")
	if err := os.WriteFile(collisionPath, []byte("busy"), 0600); err != nil {
		t.Fatalf("WriteFile(collision) error: %v", err)
	}

	originalTempName := managedTempName
	calls := 0
	managedTempName = func(pattern string) (string, error) {
		calls++
		if calls == 1 {
			return ".config-collision.tmp", nil
		}
		return ".config-success.tmp", nil
	}
	defer func() {
		managedTempName = originalTempName
	}()

	cfg := Default()
	cfg.Server.Port = 18080
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error after temp-name collision: %v", err)
	}
	if calls != 2 {
		t.Fatalf("managedTempName calls = %d, want 2", calls)
	}
	if _, err := os.Stat(collisionPath); err != nil {
		t.Fatalf("collision temp file should remain untouched: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load(saved config) error: %v", err)
	}
	if loaded.Server.Port != 18080 {
		t.Fatalf("loaded server port = %d, want 18080", loaded.Server.Port)
	}
}

func TestLoad_NormalizesWhitespaceOnlyJWTSecret(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	cfg := Default()
	cfg.Auth.JWTSecret = " \t "
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Auth.JWTSecret != "" {
		t.Fatalf("Auth.JWTSecret = %q, want empty generated-secret sentinel", loaded.Auth.JWTSecret)
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
	if len(cfg.Server.TrustedProxyCIDRs) != 0 {
		t.Errorf("Default trusted proxy cidrs = %v, want empty", cfg.Server.TrustedProxyCIDRs)
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
	if cfg.SMB.Enabled {
		t.Error("Default SMB should be disabled")
	}
	if cfg.SMB.Listen != "127.0.0.1:1445" {
		t.Errorf("Default SMB listen = %s, want 127.0.0.1:1445", cfg.SMB.Listen)
	}
	if cfg.SMB.ServerName != "mnemonas" {
		t.Errorf("Default SMB server name = %s, want mnemonas", cfg.SMB.ServerName)
	}
	if !cfg.SMB.SigningRequired {
		t.Error("Default SMB signing should be required")
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
	if cfg.Share.DefaultExpiresIn != 7*24*time.Hour {
		t.Errorf("Default share expiry = %s, want 168h", cfg.Share.DefaultExpiresIn)
	}
	if cfg.Share.DefaultMaxAccess != 0 {
		t.Errorf("Default share max access = %d, want 0", cfg.Share.DefaultMaxAccess)
	}
	if cfg.Favorites.StoreFile != filepath.Join(internalRoot, "favorites.json") {
		t.Errorf("Default favorites store = %s, want %s", cfg.Favorites.StoreFile, filepath.Join(internalRoot, "favorites.json"))
	}
	if cfg.SMB.GatewaySocket != filepath.Join(internalRoot, "run", "smb-gateway.sock") {
		t.Errorf("Default SMB gateway socket = %s, want %s", cfg.SMB.GatewaySocket, filepath.Join(internalRoot, "run", "smb-gateway.sock"))
	}
	if cfg.SMB.CredentialFile != filepath.Join(internalRoot, "smb-credentials.json") {
		t.Errorf("Default SMB credential file = %s, want %s", cfg.SMB.CredentialFile, filepath.Join(internalRoot, "smb-credentials.json"))
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
	if cfg.Alerts.SMTPPort != 587 {
		t.Errorf("Default alerts SMTP port = %d, want 587", cfg.Alerts.SMTPPort)
	}
	if cfg.Alerts.SMTPTo == nil {
		t.Error("Default alerts SMTP recipients should be initialized to an empty slice")
	}
	if cfg.Alerts.TelegramEnabled {
		t.Error("Default Telegram alerts should be disabled")
	}
	if cfg.Alerts.TelegramBotToken != "" || cfg.Alerts.TelegramChatID != "" {
		t.Errorf("Default Telegram alert credentials should be empty, got token=%q chat=%q", cfg.Alerts.TelegramBotToken, cfg.Alerts.TelegramChatID)
	}
	if cfg.Alerts.DingTalkEnabled || cfg.Alerts.DingTalkWebhookURL != "" {
		t.Errorf("Default DingTalk alerts should be disabled with an empty webhook URL, got enabled=%v url=%q", cfg.Alerts.DingTalkEnabled, cfg.Alerts.DingTalkWebhookURL)
	}
	if cfg.Backup.Jobs == nil {
		t.Error("Default backup jobs should be initialized to an empty slice")
	}
	if cfg.Maintenance.Scrub.ScheduleInterval != 7*24*time.Hour {
		t.Errorf("Default maintenance scrub schedule interval = %s, want 168h", cfg.Maintenance.Scrub.ScheduleInterval)
	}
	if cfg.Maintenance.Scrub.RetryInterval != time.Hour {
		t.Errorf("Default maintenance scrub retry interval = %s, want 1h", cfg.Maintenance.Scrub.RetryInterval)
	}
	if cfg.Maintenance.Scrub.MaxRetries != 1 {
		t.Errorf("Default maintenance scrub max retries = %d, want 1", cfg.Maintenance.Scrub.MaxRetries)
	}
	if cfg.DiskHealth.Command != "smartctl" {
		t.Errorf("Default disk health command = %s, want smartctl", cfg.DiskHealth.Command)
	}
	if cfg.DiskHealth.CheckInterval != time.Hour {
		t.Errorf("Default disk health check interval = %s, want 1h", cfg.DiskHealth.CheckInterval)
	}
	if cfg.DiskHealth.Devices == nil {
		t.Error("Default disk health devices should be initialized to an empty slice")
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
			name:    "Invalid server host unicode control character",
			modify:  func(c *Config) { c.Server.Host = "127.0.0.1\u0081bad" },
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
			name:    "Invalid trusted proxy CIDR",
			modify:  func(c *Config) { c.Server.TrustedProxyCIDRs = []string{"not-a-cidr"} },
			wantErr: true,
		},
		{
			name: "Invalid TLS certificate pair missing key",
			modify: func(c *Config) {
				c.Server.TLS.Enabled = true
				c.Server.TLS.CertFile = filepath.Join(t.TempDir(), "server.crt")
				c.Server.TLS.KeyFile = ""
			},
			wantErr: true,
		},
		{
			name: "Invalid TLS certificate pair same path",
			modify: func(c *Config) {
				c.Server.TLS.Enabled = true
				certFile := filepath.Join(t.TempDir(), "server.pem")
				c.Server.TLS.CertFile = certFile
				c.Server.TLS.KeyFile = certFile
			},
			wantErr: true,
		},
		{
			name: "Invalid TLS without auto-generate and no certificate source",
			modify: func(c *Config) {
				c.Server.TLS.Enabled = true
				c.Server.TLS.AutoGenerate = false
				c.Server.TLS.CertFile = ""
				c.Server.TLS.KeyFile = ""
				c.Server.TLS.CertDir = ""
			},
			wantErr: true,
		},
		{
			name: "Valid TLS explicit distinct pair without auto-generate",
			modify: func(c *Config) {
				c.Server.TLS.Enabled = true
				c.Server.TLS.AutoGenerate = false
				certDir := t.TempDir()
				c.Server.TLS.CertFile = filepath.Join(certDir, "server.crt")
				c.Server.TLS.KeyFile = filepath.Join(certDir, "server.key")
			},
			wantErr: false,
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
			name:    "Versioning max size exceeds object contract",
			modify:  func(c *Config) { c.Storage.Versioning.MaxVersionedSize = versionstore.MaxVersionObjectSize + 1 },
			wantErr: true,
		},
		{
			name:    "Invalid WebDAV auth type",
			modify:  func(c *Config) { c.WebDAV.AuthType = "token" },
			wantErr: true,
		},
		{
			name:    "WebDAV users auth type is valid when app auth is enabled",
			modify:  func(c *Config) { c.WebDAV.AuthType = "users" },
			wantErr: false,
		},
		{
			name: "WebDAV users auth type requires app auth",
			modify: func(c *Config) {
				c.Server.Host = "127.0.0.1"
				c.Auth.Enabled = false
				c.WebDAV.AuthType = "users"
			},
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
			name:    "Invalid WebDAV prefix with unicode control character",
			modify:  func(c *Config) { c.WebDAV.Prefix = "/dav\u0081files" },
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
			name:    "Invalid SMB listen",
			modify:  func(c *Config) { c.SMB.Listen = "localhost:not-a-port" },
			wantErr: true,
		},
		{
			name:    "Invalid SMB server name",
			modify:  func(c *Config) { c.SMB.ServerName = "bad/name" },
			wantErr: true,
		},
		{
			name:    "Invalid SMB server name unicode control character",
			modify:  func(c *Config) { c.SMB.ServerName = "mnemonas\u0081nas" },
			wantErr: true,
		},
		{
			name:    "Invalid SMB gateway socket relative path",
			modify:  func(c *Config) { c.SMB.GatewaySocket = "run/smb.sock" },
			wantErr: true,
		},
		{
			name:    "Invalid SMB credential file relative path",
			modify:  func(c *Config) { c.SMB.CredentialFile = "smb-credentials.json" },
			wantErr: true,
		},
		{
			name:    "Enabled SMB requires shares",
			modify:  func(c *Config) { c.SMB.Enabled = true },
			wantErr: true,
		},
		{
			name: "Valid SMB share",
			modify: func(c *Config) {
				c.SMB.Enabled = true
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "homes",
					Path:         "/",
					AllowedRoles: []string{"admin", "user"},
				}}
			},
			wantErr: false,
		},
		{
			name: "Invalid SMB share path traversal",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "bad",
					Path:         "/home/../secret",
					AllowedRoles: []string{"admin"},
				}}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB share name unicode control character",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "docs\u0081private",
					Path:         "/docs",
					AllowedRoles: []string{"admin"},
				}}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB share path unicode control character",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "docs",
					Path:         "/docs\u0081private",
					AllowedRoles: []string{"admin"},
				}}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB share user unicode control character",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "docs",
					Path:         "/docs",
					AllowedUsers: []string{"alice\u0081hidden"},
				}}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB duplicate share name",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{
					{Name: "docs", Path: "/", AllowedRoles: []string{"admin"}},
					{Name: "DOCS", Path: "/docs", AllowedRoles: []string{"admin"}},
				}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB share without allow list",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{Name: "open", Path: "/"}}
			},
			wantErr: true,
		},
		{
			name: "Invalid SMB share role",
			modify: func(c *Config) {
				c.SMB.Shares = []SMBShareConfig{{
					Name:         "docs",
					Path:         "/docs",
					AllowedRoles: []string{"owner"},
				}}
			},
			wantErr: true,
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
			name:    "Invalid share base URL userinfo",
			modify:  func(c *Config) { c.Share.BaseURL = "https://operator@nas.example.com" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL query",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com?token=secret" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL empty query",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com?" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL fragment",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com#share" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL empty fragment",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com#" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL escaped query marker path",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares%3Ftoken" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL escaped fragment marker path",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares%23section" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL empty host label",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas..example.com" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL multiple trailing host dots",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com.." },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL host character",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas_example.com" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL unbracketed IPv6 host",
			modify:  func(c *Config) { c.Share.BaseURL = "https://2001:db8::1" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL bracketed IPv6 host",
			modify:  func(c *Config) { c.Share.BaseURL = "https://[::::]" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL duplicate path slash",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares//team" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL escaped duplicate path slash",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares%2F%2Fteam" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL dot segment path",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares/./team" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL escaped dot segment path",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares/%2e%2e/team" },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL backslash path",
			modify:  func(c *Config) { c.Share.BaseURL = `https://nas.example.com/shares\team` },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL host-relative backslash path",
			modify:  func(c *Config) { c.Share.BaseURL = `https://nas.example.com\shares` },
			wantErr: true,
		},
		{
			name:    "Invalid share base URL escaped backslash path",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com/shares%5Cteam" },
			wantErr: true,
		},
		{
			name:    "Invalid negative share default expiry",
			modify:  func(c *Config) { c.Share.DefaultExpiresIn = -time.Hour },
			wantErr: true,
		},
		{
			name:    "Invalid negative share default max access",
			modify:  func(c *Config) { c.Share.DefaultMaxAccess = -1 },
			wantErr: true,
		},
		{
			name: "Valid share policy rule",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{
					Path:            "/Family",
					RequirePassword: true,
					MaxExpiresIn:    24 * time.Hour,
					MaxAccess:       10,
					AllowedUsers:    []string{"alice"},
					AllowedGroups:   []string{"family"},
					AllowedRoles:    []string{"user"},
				}}
			},
			wantErr: false,
		},
		{
			name: "Valid share policy principal-only rule",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{
					Path:          "/Family",
					AllowedUsers:  []string{"alice"},
					AllowedGroups: []string{"family"},
					AllowedRoles:  []string{"user"},
				}}
			},
			wantErr: false,
		},
		{
			name: "Invalid share policy relative path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "Family", RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy backslash path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: `\Family`, RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy query path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family?token=secret", RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy fragment path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family#secret", RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy control character path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family\nPrivate", RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy unicode control character path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family\u0081Private", RequirePassword: true}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy duplicate path",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{
					{Path: "/Family", RequirePassword: true},
					{Path: "/Family", MaxAccess: 10},
				}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy negative max expiry",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family", MaxExpiresIn: -time.Hour}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy negative max access",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family", MaxAccess: -1}}
			},
			wantErr: true,
		},
		{
			name: "Invalid empty share policy constraint",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family"}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy allowed user",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family", AllowedUsers: []string{"Alice"}}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy allowed group",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family", AllowedGroups: []string{"family/team"}}}
			},
			wantErr: true,
		},
		{
			name: "Invalid share policy allowed role",
			modify: func(c *Config) {
				c.Share.PolicyRules = []SharePolicyRuleConfig{{Path: "/Family", AllowedRoles: []string{"owner"}}}
			},
			wantErr: true,
		},
		{
			name: "Valid Telegram alerts",
			modify: func(c *Config) {
				c.Alerts.TelegramEnabled = true
				c.Alerts.TelegramBotToken = "123456:ABC_def-ghi"
				c.Alerts.TelegramChatID = "-1001234567890"
			},
			wantErr: false,
		},
		{
			name: "Enabled Telegram alerts require token",
			modify: func(c *Config) {
				c.Alerts.TelegramEnabled = true
				c.Alerts.TelegramChatID = "-1001234567890"
			},
			wantErr: true,
		},
		{
			name: "Invalid Telegram token path character",
			modify: func(c *Config) {
				c.Alerts.TelegramBotToken = "123456/bad"
			},
			wantErr: true,
		},
		{
			name: "Invalid Telegram chat whitespace",
			modify: func(c *Config) {
				c.Alerts.TelegramChatID = "bad chat"
			},
			wantErr: true,
		},
		{
			name:    "Valid share base URL",
			modify:  func(c *Config) { c.Share.BaseURL = "https://nas.example.com" },
			wantErr: false,
		},
		{
			name:    "Valid share base URL trailing dot",
			modify:  func(c *Config) { c.Share.BaseURL = "https://NAS.EXAMPLE.COM." },
			wantErr: false,
		},
		{
			name:    "Valid share base URL bracketed IPv6 host",
			modify:  func(c *Config) { c.Share.BaseURL = "https://[2001:db8::1]" },
			wantErr: false,
		},
		{
			name:    "Invalid auth access token ttl",
			modify:  func(c *Config) { c.Auth.AccessTokenTTL = 0 },
			wantErr: true,
		},
		{
			name:    "Invalid auth access token ttl below refresh rotation floor",
			modify:  func(c *Config) { c.Auth.AccessTokenTTL = minimumAuthAccessTokenTTL - time.Second },
			wantErr: true,
		},
		{
			name:    "Valid auth access token ttl at refresh rotation floor",
			modify:  func(c *Config) { c.Auth.AccessTokenTTL = minimumAuthAccessTokenTTL },
			wantErr: false,
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
			name:    "Invalid whitespace-padded short JWT secret",
			modify:  func(c *Config) { c.Auth.JWTSecret = strings.Repeat(" ", 31) + "x" },
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
			name: "Invalid alerts webhook URL empty host label",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "https://hooks..example.com/storage"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook URL port",
			modify: func(c *Config) {
				c.Alerts.WebhookURL = "https://hooks.example.com:port/storage"
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
			name: "Invalid alerts WeCom URL scheme",
			modify: func(c *Config) {
				c.Alerts.WeComWebhookURL = "file:///tmp/wecom"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts WeCom URL host",
			modify: func(c *Config) {
				c.Alerts.WeComWebhookURL = "https://qyapi..weixin.qq.com/cgi-bin/webhook/send?key=secret-key"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts WeCom enabled without URL",
			modify: func(c *Config) {
				c.Alerts.WeComEnabled = true
			},
			wantErr: true,
		},
		{
			name: "Valid alerts WeCom URL",
			modify: func(c *Config) {
				c.Alerts.WeComEnabled = true
				c.Alerts.WeComWebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key"
			},
			wantErr: false,
		},
		{
			name: "Invalid alerts DingTalk URL scheme",
			modify: func(c *Config) {
				c.Alerts.DingTalkWebhookURL = "file:///tmp/dingtalk"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts DingTalk URL host",
			modify: func(c *Config) {
				c.Alerts.DingTalkWebhookURL = "https://oapi..dingtalk.com/robot/send?access_token=secret-token"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts DingTalk enabled without URL",
			modify: func(c *Config) {
				c.Alerts.DingTalkEnabled = true
			},
			wantErr: true,
		},
		{
			name: "Valid alerts DingTalk URL",
			modify: func(c *Config) {
				c.Alerts.DingTalkEnabled = true
				c.Alerts.DingTalkWebhookURL = "https://oapi.dingtalk.com/robot/send?access_token=secret-token"
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
			name: "Invalid alerts webhook header value unicode control character",
			modify: func(c *Config) {
				c.Alerts.WebhookHeaders = []string{"X-MnemoNAS: ok\u0081hidden"}
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts webhook duplicate header name",
			modify: func(c *Config) {
				c.Alerts.WebhookHeaders = []string{"Authorization: Bearer one", "authorization: Bearer two"}
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts email missing recipients",
			modify: func(c *Config) {
				c.Alerts.EmailEnabled = true
				c.Alerts.SMTPHost = "smtp.example.com"
				c.Alerts.SMTPFrom = "alerts@example.com"
			},
			wantErr: true,
		},
		{
			name: "Invalid alerts email recipient",
			modify: func(c *Config) {
				c.Alerts.SMTPTo = []string{"not an email"}
			},
			wantErr: true,
		},
		{
			name: "Valid alerts email",
			modify: func(c *Config) {
				c.Alerts.EmailEnabled = true
				c.Alerts.SMTPHost = "smtp.example.com"
				c.Alerts.SMTPPort = 587
				c.Alerts.SMTPFrom = "MnemoNAS <alerts@example.com>"
				c.Alerts.SMTPTo = []string{"admin@example.com"}
			},
			wantErr: false,
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
			name: "Invalid disk health command with shell words",
			modify: func(c *Config) {
				c.DiskHealth.Command = "smartctl --json"
			},
			wantErr: true,
		},
		{
			name: "Invalid disk health check interval",
			modify: func(c *Config) {
				c.DiskHealth.CheckInterval = 0
			},
			wantErr: true,
		},
		{
			name: "Invalid disk health critical temperature below warning",
			modify: func(c *Config) {
				c.DiskHealth.TemperatureWarningC = 55
				c.DiskHealth.TemperatureCriticalC = 50
			},
			wantErr: true,
		},
		{
			name: "Invalid disk health critical media wear below warning",
			modify: func(c *Config) {
				c.DiskHealth.MediaWearWarningPct = 90
				c.DiskHealth.MediaWearCriticalPct = 80
			},
			wantErr: true,
		},
		{
			name: "Invalid disk health relative device path",
			modify: func(c *Config) {
				c.DiskHealth.Devices = []DiskHealthDeviceConfig{{Path: "sda"}}
			},
			wantErr: true,
		},
		{
			name: "Valid disk health device",
			modify: func(c *Config) {
				c.DiskHealth.Enabled = true
				c.DiskHealth.Devices = []DiskHealthDeviceConfig{{
					Name:   "Data disk",
					Path:   "/dev/disk/by-id/test",
					Type:   "sat",
					Serial: "SER123",
				}}
			},
			wantErr: false,
		},
		{
			name: "Invalid maintenance scrub schedule interval",
			modify: func(c *Config) {
				c.Maintenance.Scrub.ScheduleInterval = 0
			},
			wantErr: true,
		},
		{
			name: "Invalid maintenance scrub retry interval",
			modify: func(c *Config) {
				c.Maintenance.Scrub.RetryInterval = 0
			},
			wantErr: true,
		},
		{
			name: "Invalid maintenance scrub max retries",
			modify: func(c *Config) {
				c.Maintenance.Scrub.MaxRetries = -1
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

func TestConfig_ValidateBackupJobs(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "destination")
	resticPasswordFile := filepath.Join(tmpDir, "restic.pass")
	rcloneConfigFile := filepath.Join(tmpDir, "rclone.conf")
	realCredentialDir := filepath.Join(tmpDir, "real-credentials")
	linkedCredentialDir := filepath.Join(tmpDir, "linked-credentials")
	linkedParentCredentialFile := filepath.Join(linkedCredentialDir, "restic.pass")
	if err := os.WriteFile(resticPasswordFile, []byte("test-password"), 0600); err != nil {
		t.Fatalf("WriteFile(resticPasswordFile) error: %v", err)
	}
	if err := os.WriteFile(rcloneConfigFile, []byte("[b2]\ntype = local\n"), 0600); err != nil {
		t.Fatalf("WriteFile(rcloneConfigFile) error: %v", err)
	}
	if err := os.Mkdir(realCredentialDir, 0700); err != nil {
		t.Fatalf("Mkdir(realCredentialDir) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realCredentialDir, "restic.pass"), []byte("linked-parent-password"), 0600); err != nil {
		t.Fatalf("WriteFile(linked parent credential) error: %v", err)
	}
	if err := os.Symlink(realCredentialDir, linkedCredentialDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name: "valid local backup job",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:                  "home",
					Name:                "Home backup",
					Type:                "local",
					Source:              source,
					Destination:         destination,
					ScheduleWindowStart: "02:00",
					ScheduleWindowEnd:   "05:30",
				}}
			},
			wantErr: false,
		},
		{
			name: "valid restic backup job",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					Command:      "restic",
					PasswordFile: resticPasswordFile,
					ExtraArgs:    []string{"--compression", "max"},
				}}
			},
			wantErr: false,
		},
		{
			name: "valid rclone backup job",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:         "rclone-remote",
					Name:       "Rclone remote",
					Type:       "rclone",
					Source:     source,
					Remote:     "b2:mnemonas/backups",
					Command:    "rclone",
					ConfigFile: rcloneConfigFile,
					ExtraArgs:  []string{"--fast-list"},
				}}
			},
			wantErr: false,
		},
		{
			name: "duplicate backup job id",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{
					{ID: "home", Name: "A", Type: "local", Source: source, Destination: destination},
					{ID: "HOME", Name: "B", Type: "local", Source: source, Destination: filepath.Join(tmpDir, "destination-2")},
				}
			},
			wantErr: true,
		},
		{
			name: "destination inside source",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:          "home",
					Name:        "Home backup",
					Type:        "local",
					Source:      source,
					Destination: filepath.Join(source, "backup"),
				}}
			},
			wantErr: true,
		},
		{
			name: "destination inside storage root",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:          "home",
					Name:        "Home backup",
					Type:        "local",
					Source:      filepath.Join(tmpDir, "snapshot"),
					Destination: filepath.Join(source, "backup"),
				}}
			},
			wantErr: true,
		},
		{
			name: "unsupported type",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:          "home",
					Name:        "Home backup",
					Type:        "s3",
					Source:      source,
					Destination: destination,
				}}
			},
			wantErr: true,
		},
		{
			name: "restic requires password file",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:         "restic-remote",
					Name:       "Restic remote",
					Type:       "restic",
					Source:     source,
					Repository: "rest:http://backup.example/repo",
				}}
			},
			wantErr: true,
		},
		{
			name: "rclone requires remote",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:     "rclone-remote",
					Name:   "Rclone remote",
					Type:   "rclone",
					Source: source,
				}}
			},
			wantErr: true,
		},
		{
			name: "command cannot contain shell words",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					Command:      "restic --repo",
					PasswordFile: resticPasswordFile,
				}}
			},
			wantErr: true,
		},
		{
			name: "extra args cannot normalize to empty",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					PasswordFile: resticPasswordFile,
					ExtraArgs:    []string{"  "},
				}}
			},
			wantErr: true,
		},
		{
			name: "extra args reject unicode control characters",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					PasswordFile: resticPasswordFile,
					ExtraArgs:    []string{"--tag=nightly\u0081hidden"},
				}}
			},
			wantErr: true,
		},
		{
			name: "retention policy rejects control characters",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:              "restic-remote",
					Name:            "Restic remote",
					Type:            "restic",
					Source:          source,
					Repository:      "rest:http://backup.example/repo",
					PasswordFile:    resticPasswordFile,
					RetentionPolicy: "external: restic forget\n--prune",
				}}
			},
			wantErr: true,
		},
		{
			name: "exclude pattern rejects control characters",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:          "home",
					Name:        "Home backup",
					Type:        "local",
					Source:      source,
					Destination: destination,
					Exclude:     []string{"cache\x7fsecret"},
				}}
			},
			wantErr: true,
		},
		{
			name: "credential file cannot be inside source",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					PasswordFile: filepath.Join(source, "restic.pass"),
				}}
			},
			wantErr: true,
		},
		{
			name: "credential file must exist",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					PasswordFile: filepath.Join(tmpDir, "missing-restic.pass"),
				}}
			},
			wantErr: true,
		},
		{
			name: "credential file rejects symlink parent",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "restic-remote",
					Name:         "Restic remote",
					Type:         "restic",
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					PasswordFile: linkedParentCredentialFile,
				}}
			},
			wantErr: true,
		},
		{
			name: "negative schedule interval",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:               "home",
					Name:             "Home backup",
					Type:             "local",
					Source:           source,
					Destination:      destination,
					ScheduleInterval: -time.Hour,
				}}
			},
			wantErr: true,
		},
		{
			name: "partial schedule window",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:                  "home",
					Name:                "Home backup",
					Type:                "local",
					Source:              source,
					Destination:         destination,
					ScheduleWindowStart: "02:00",
				}}
			},
			wantErr: true,
		},
		{
			name: "invalid schedule window clock",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:                  "home",
					Name:                "Home backup",
					Type:                "local",
					Source:              source,
					Destination:         destination,
					ScheduleWindowStart: "25:00",
					ScheduleWindowEnd:   "05:00",
				}}
			},
			wantErr: true,
		},
		{
			name: "equal schedule window bounds",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:                  "home",
					Name:                "Home backup",
					Type:                "local",
					Source:              source,
					Destination:         destination,
					ScheduleWindowStart: "02:00",
					ScheduleWindowEnd:   "02:00",
				}}
			},
			wantErr: true,
		},
		{
			name: "negative retention",
			modify: func(c *Config) {
				c.Backup.Jobs = []BackupJobConfig{{
					ID:           "home",
					Name:         "Home backup",
					Type:         "local",
					Source:       source,
					Destination:  destination,
					MaxSnapshots: -1,
				}}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Storage.Root = source
			tt.modify(cfg)
			cfg.Backup.Jobs = normalizeBackupJobs(cfg.Backup.Jobs)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRcloneConfigEvidenceFileRequiresStaticNamedRemote(t *testing.T) {
	tests := []struct {
		name        string
		remote      string
		content     string
		wantErrText string
	}{
		{
			name:        "connection string remote",
			remote:      ":s3,provider=AWS:mnemonas/backups",
			content:     "[backup]\ntype = s3\n",
			wantErrText: "named config_file section",
		},
		{
			name:        "undefined named section",
			remote:      "missing:mnemonas/backups",
			content:     "[backup]\ntype = s3\n",
			wantErrText: "is not defined",
		},
		{
			name:        "empty type",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype =\nprovider = AWS\n",
			wantErrText: "has no type",
		},
		{
			name:        "token",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = drive\ntoken = static-token\n",
			wantErrText: "token-refreshing",
		},
		{
			name:        "environment authentication",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = s3\nenv_auth = true\n",
			wantErrText: "cannot enable env_auth",
		},
		{
			name:        "environment expansion",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = local\nremote = ${BACKUP_ROOT}\n",
			wantErrText: "cannot expand environment-dependent paths",
		},
		{
			name:        "external file",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = s3\nservice_account_file = /run/secrets/account.json\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "external path",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = local\nroot_path = /srv/archive\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "password command",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\npassword_command = secret-tool lookup backup mnemonas\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "ssh agent",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\nssh_agent = true\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "ssh helper",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\nssh = /usr/bin/ssh\n",
			wantErrText: "external runtime input",
		},
		{
			name:    "static named remote",
			remote:  "backup:mnemonas/backups",
			content: "[backup]\ntype = s3\nprovider = AWS\naccess_key_id = static-id\nsecret_access_key = static-secret\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "rclone.conf")
			if err := os.WriteFile(configPath, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("WriteFile(config) error: %v", err)
			}

			err := validateRcloneConfigEvidenceFile(configPath, "remote", tt.remote)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("validateRcloneConfigEvidenceFile() error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("validateRcloneConfigEvidenceFile() error = %v, want text %q", err, tt.wantErrText)
			}
		})
	}
}

func TestValidateBackupIdentityOverrideArgAllowsOnlyRcloneFastList(t *testing.T) {
	tests := []struct {
		arg     string
		wantErr bool
	}{
		{arg: "--fast-list"},
		{arg: " --fast-list "},
		{arg: "--fast-list=true", wantErr: true},
		{arg: "--transfers=4", wantErr: true},
		{arg: "--config=/tmp/rclone.conf", wantErr: true},
		{arg: "--password-command=secret-tool", wantErr: true},
		{arg: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			err := validateBackupIdentityOverrideArg("rclone", tt.arg, "rclone extra_args")
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBackupIdentityOverrideArg(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
		})
	}
}

func TestValidateResticRepositoryBoundaryAllowsOnlyExplicitCredentialModels(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	storageRoot := filepath.Join(root, "storage")
	tests := []struct {
		name       string
		repository string
		wantErr    bool
	}{
		{name: "absolute local", repository: filepath.Join(root, "repository")},
		{name: "rest https", repository: "rest:https://backup.example/repository"},
		{name: "rest http", repository: "rest:http://backup.example/repository"},
		{name: "rest missing host", repository: "rest:https:///repository", wantErr: true},
		{name: "rest unsupported scheme", repository: "rest:ftp://backup.example/repository", wantErr: true},
		{name: "inside source", repository: filepath.Join(source, "repository"), wantErr: true},
		{name: "inside storage root", repository: filepath.Join(storageRoot, "repository"), wantErr: true},
		{name: "relative local", repository: "backups/repository", wantErr: true},
		{name: "s3 environment credentials", repository: "s3:s3.example/bucket", wantErr: true},
		{name: "sftp agent credentials", repository: "sftp:user@backup.example:/repository", wantErr: true},
		{name: "rclone external config", repository: "rclone:backup:repository", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticRepositoryBoundary(tt.repository, "restic", source, storageRoot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateResticRepositoryBoundary(%q) error = %v, wantErr %v", tt.repository, err, tt.wantErr)
			}
		})
	}
}

func TestConfig_DirectoryQuotas(t *testing.T) {
	cfg := Default()
	cfg.Storage.DirectoryQuotas = []DirectoryQuotaConfig{
		{Path: "/team", QuotaBytes: 1024},
		{Path: "/", QuotaBytes: 4096},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		quota  DirectoryQuotaConfig
		errSub string
	}{
		{name: "relative path", quota: DirectoryQuotaConfig{Path: "team", QuotaBytes: 1024}, errSub: "must be absolute"},
		{name: "dirty path", quota: DirectoryQuotaConfig{Path: "/team/../other", QuotaBytes: 1024}, errSub: "must be clean"},
		{name: "backslash path", quota: DirectoryQuotaConfig{Path: `/team\private`, QuotaBytes: 1024}, errSub: "must be a clean MnemoNAS path"},
		{name: "query path", quota: DirectoryQuotaConfig{Path: "/team?token=secret", QuotaBytes: 1024}, errSub: "must be a clean MnemoNAS path"},
		{name: "fragment path", quota: DirectoryQuotaConfig{Path: "/team#secret", QuotaBytes: 1024}, errSub: "must be a clean MnemoNAS path"},
		{name: "control character path", quota: DirectoryQuotaConfig{Path: "/team\nprivate", QuotaBytes: 1024}, errSub: "must not contain control characters"},
		{name: "unicode control character path", quota: DirectoryQuotaConfig{Path: "/team\u0081private", QuotaBytes: 1024}, errSub: "must not contain control characters"},
		{name: "negative bytes", quota: DirectoryQuotaConfig{Path: "/team", QuotaBytes: -1}, errSub: "quota_bytes must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Storage.DirectoryQuotas = []DirectoryQuotaConfig{tt.quota}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.errSub)
			}
		})
	}
}

func TestLoad_NormalizesDirectoryQuotaPaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	data := []byte(`
[storage]
root = "` + filepath.ToSlash(filepath.Join(tmpDir, "data")) + `"
directory_quotas = [
  { path = "/team/", quota_bytes = 1048576 },
]
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Storage.DirectoryQuotas) != 1 {
		t.Fatalf("directory quotas count = %d, want 1", len(cfg.Storage.DirectoryQuotas))
	}
	if got := cfg.Storage.DirectoryQuotas[0].Path; got != "/team" {
		t.Fatalf("directory quota path = %q, want /team", got)
	}
}

func TestLoad_RejectsDirectoryQuotaDotSegmentPaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	data := []byte(`
[storage]
root = "` + filepath.ToSlash(filepath.Join(tmpDir, "data")) + `"
directory_quotas = [
  { path = "/team/../other", quota_bytes = 1048576 },
]
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "storage.directory_quotas[0].path must be clean") {
		t.Fatalf("Load() error = %v, want dirty directory quota path rejection", err)
	}
}

func TestConfig_DirectoryAccessRules(t *testing.T) {
	cfg := Default()
	cfg.Storage.DirectoryAccessRules = []DirectoryAccessRuleConfig{
		{
			Path:        "/team",
			ReadGroups:  []string{"family"},
			WriteGroups: []string{"editors"},
			ReadRoles:   []string{"user"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		rule   DirectoryAccessRuleConfig
		errSub string
	}{
		{name: "relative path", rule: DirectoryAccessRuleConfig{Path: "team", ReadUsers: []string{"alice"}}, errSub: "must be absolute"},
		{name: "dirty path", rule: DirectoryAccessRuleConfig{Path: "/team/../other", ReadUsers: []string{"alice"}}, errSub: "must be clean"},
		{name: "backslash path", rule: DirectoryAccessRuleConfig{Path: `/team\private`, ReadUsers: []string{"alice"}}, errSub: "must be a clean MnemoNAS path"},
		{name: "query path", rule: DirectoryAccessRuleConfig{Path: "/team?token=secret", ReadUsers: []string{"alice"}}, errSub: "must be a clean MnemoNAS path"},
		{name: "fragment path", rule: DirectoryAccessRuleConfig{Path: "/team#secret", ReadUsers: []string{"alice"}}, errSub: "must be a clean MnemoNAS path"},
		{name: "control character path", rule: DirectoryAccessRuleConfig{Path: "/team\nprivate", ReadUsers: []string{"alice"}}, errSub: "must not contain control characters"},
		{name: "unicode control character path", rule: DirectoryAccessRuleConfig{Path: "/team\u0081private", ReadUsers: []string{"alice"}}, errSub: "must not contain control characters"},
		{name: "missing principals", rule: DirectoryAccessRuleConfig{Path: "/team"}, errSub: "must grant at least one"},
		{name: "invalid principal", rule: DirectoryAccessRuleConfig{Path: "/team", ReadGroups: []string{"family/team"}}, errSub: "contains invalid characters"},
		{name: "invalid role", rule: DirectoryAccessRuleConfig{Path: "/team", ReadRoles: []string{"owner"}}, errSub: "must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Storage.DirectoryAccessRules = []DirectoryAccessRuleConfig{tt.rule}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.errSub)
			}
		})
	}
}

func TestLoad_RejectsDirectoryAccessRuleDotSegmentPaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	data := []byte(`
[storage]
root = "` + filepath.ToSlash(filepath.Join(tmpDir, "data")) + `"
directory_access_rules = [
  { path = "/team/./private", read_users = ["alice"] },
]
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "storage.directory_access_rules[0].path must be clean") {
		t.Fatalf("Load() error = %v, want dirty directory access rule path rejection", err)
	}
}

func TestLoad_NormalizesDirectoryAccessRules(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	data := []byte(`
[storage]
root = "` + filepath.ToSlash(filepath.Join(tmpDir, "data")) + `"
directory_access_rules = [
  { path = "/team/", read_users = ["Alice", "alice"], write_groups = ["Editors", "editors"], read_roles = ["User"] },
]
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Storage.DirectoryAccessRules) != 1 {
		t.Fatalf("directory access rules count = %d, want 1", len(cfg.Storage.DirectoryAccessRules))
	}
	rule := cfg.Storage.DirectoryAccessRules[0]
	if rule.Path != "/team" {
		t.Fatalf("directory access path = %q, want /team", rule.Path)
	}
	if got, want := strings.Join(rule.ReadUsers, ","), "alice"; got != want {
		t.Fatalf("read users = %q, want %q", got, want)
	}
	if got, want := strings.Join(rule.WriteGroups, ","), "editors"; got != want {
		t.Fatalf("write groups = %q, want %q", got, want)
	}
	if got, want := strings.Join(rule.ReadRoles, ","), "user"; got != want {
		t.Fatalf("read roles = %q, want %q", got, want)
	}
}

func TestLoad_NormalizesBackupJobDurationFields(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "data")
	backupRoot := filepath.Join(tmpDir, "backup")
	configPath := filepath.Join(tmpDir, "config.toml")
	content := fmt.Sprintf(`
[storage]
root = %q

[[backup.jobs]]
id = "external"
name = "External Backup"
type = "local"
destination = %q
schedule_interval = "1h"
stale_after = "3h"
restore_drill_stale_after = "168h"
retention_policy = "external: restic forget --keep-daily 7 --prune"
max_age = "24h"
`, storageRoot, backupRoot)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Backup.Jobs) != 1 {
		t.Fatalf("backup job count = %d, want 1", len(cfg.Backup.Jobs))
	}
	job := cfg.Backup.Jobs[0]
	if job.ScheduleInterval != time.Hour {
		t.Fatalf("schedule interval = %s, want 1h", job.ScheduleInterval)
	}
	if job.StaleAfter != 3*time.Hour {
		t.Fatalf("stale after = %s, want 3h", job.StaleAfter)
	}
	if job.RestoreDrillStaleAfter != 168*time.Hour {
		t.Fatalf("restore drill stale after = %s, want 168h", job.RestoreDrillStaleAfter)
	}
	if job.RetentionPolicy != "external: restic forget --keep-daily 7 --prune" {
		t.Fatalf("retention policy = %q", job.RetentionPolicy)
	}
	if job.MaxAge != 24*time.Hour {
		t.Fatalf("max age = %s, want 24h", job.MaxAge)
	}
}

func TestLoad_AcceptsEmptyBackupJobScheduleInterval(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "data")
	backupRoot := filepath.Join(tmpDir, "backup")
	configPath := filepath.Join(tmpDir, "config.toml")
	content := fmt.Sprintf(`
[storage]
root = %q

[[backup.jobs]]
id = "manual-local"
name = "Manual local backup"
type = "local"
destination = %q
schedule_interval = ""
`, storageRoot, backupRoot)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Backup.Jobs) != 1 {
		t.Fatalf("backup job count = %d, want 1", len(cfg.Backup.Jobs))
	}
	if cfg.Backup.Jobs[0].ScheduleInterval != 0 {
		t.Fatalf("schedule interval = %s, want 0", cfg.Backup.Jobs[0].ScheduleInterval)
	}
}

func TestLoad_AcceptsEmptyBackupJobRestoreDrillStaleAfter(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "data")
	backupRoot := filepath.Join(tmpDir, "backup")
	configPath := filepath.Join(tmpDir, "config.toml")
	content := fmt.Sprintf(`
[storage]
root = %q

[[backup.jobs]]
id = "manual-local"
name = "Manual local backup"
type = "local"
destination = %q
restore_drill_stale_after = ""
`, storageRoot, backupRoot)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Backup.Jobs) != 1 {
		t.Fatalf("backup job count = %d, want 1", len(cfg.Backup.Jobs))
	}
	if cfg.Backup.Jobs[0].RestoreDrillStaleAfter != 0 {
		t.Fatalf("restore drill stale after = %s, want 0", cfg.Backup.Jobs[0].RestoreDrillStaleAfter)
	}
}

func TestLoad_RejectsEmptyBackupJobNonScheduleDurations(t *testing.T) {
	for _, field := range []string{"stale_after", "max_age"} {
		t.Run(field, func(t *testing.T) {
			tmpDir := t.TempDir()
			storageRoot := filepath.Join(tmpDir, "data")
			backupRoot := filepath.Join(tmpDir, "backup")
			configPath := filepath.Join(tmpDir, "config.toml")
			content := fmt.Sprintf(`
[storage]
root = %q

[[backup.jobs]]
id = "manual-local"
name = "Manual local backup"
type = "local"
destination = %q
%s = ""
`, storageRoot, backupRoot, field)
			if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
				t.Fatalf("WriteFile(config) error: %v", err)
			}

			_, err := Load(configPath)
			if err == nil {
				t.Fatalf("Load() error = nil, want invalid %s duration", field)
			}
			if !strings.Contains(err.Error(), "invalid backup.jobs[0]."+field+" duration") {
				t.Fatalf("Load() error = %v, want invalid %s duration", err, field)
			}
		})
	}
}

func TestLoad_NormalizesMaintenanceScrubDurationFields(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "data")
	configPath := filepath.Join(tmpDir, "config.toml")
	content := fmt.Sprintf(`
[storage]
root = %q

[maintenance.scrub]
enabled = true
schedule_interval = "12h"
retry_interval = "30m"
max_retries = 2
`, storageRoot)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Maintenance.Scrub.Enabled {
		t.Fatal("expected maintenance scrub schedule to be enabled")
	}
	if cfg.Maintenance.Scrub.ScheduleInterval != 12*time.Hour {
		t.Fatalf("schedule interval = %s, want 12h", cfg.Maintenance.Scrub.ScheduleInterval)
	}
	if cfg.Maintenance.Scrub.RetryInterval != 30*time.Minute {
		t.Fatalf("retry interval = %s, want 30m", cfg.Maintenance.Scrub.RetryInterval)
	}
	if cfg.Maintenance.Scrub.MaxRetries != 2 {
		t.Fatalf("max retries = %d, want 2", cfg.Maintenance.Scrub.MaxRetries)
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
		{name: "preserves non-edge spaces inside path segments", input: "/team name /dav", expected: "/team name /dav"},
		{name: "trims only path edge spaces after clean", input: "/team /sub ", expected: "/team /sub"},
		{name: "preserves non-edge leading spaces inside path segments", input: "/ team/sub", expected: "/ team/sub"},
		{name: "preserves non-empty padded middle segment", input: "/team/ spaced /file", expected: "/team/ spaced /file"},
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

func TestLoad_NormalizesWebDAVAuthType(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[webdav]
auth_type = " BASIC "
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.WebDAV.AuthType != "basic" {
		t.Fatalf("expected normalized auth type basic, got %q", cfg.WebDAV.AuthType)
	}
}

func TestNormalizeWebDAVAuthType_DefaultsBlankToBasic(t *testing.T) {
	tests := []string{"", " \t "}

	for _, input := range tests {
		if got := NormalizeWebDAVAuthType(input); got != "basic" {
			t.Fatalf("NormalizeWebDAVAuthType(%q) = %q, want basic", input, got)
		}
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

[disk_health]
check_interval = "3h"
probe_timeout = "20s"
cooldown_period = "8h"
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
	if cfg.DiskHealth.CheckInterval != 3*time.Hour {
		t.Fatalf("expected disk health check interval 3h, got %s", cfg.DiskHealth.CheckInterval)
	}
	if cfg.DiskHealth.ProbeTimeout != 20*time.Second {
		t.Fatalf("expected disk health probe timeout 20s, got %s", cfg.DiskHealth.ProbeTimeout)
	}
	if cfg.DiskHealth.CooldownPeriod != 8*time.Hour {
		t.Fatalf("expected disk health cooldown 8h, got %s", cfg.DiskHealth.CooldownPeriod)
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

func TestLoad_ParsesTrustedProxyCIDRs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[server]
trusted_proxy_cidrs = ["10.0.0.0/8", " 192.168.1.10 "]
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.10"}
	if !reflect.DeepEqual(cfg.Server.TrustedProxyCIDRs, want) {
		t.Fatalf("trusted proxy cidrs = %v, want %v", cfg.Server.TrustedProxyCIDRs, want)
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
wecom_webhook_url = " https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key "
dingtalk_webhook_url = " https://oapi.dingtalk.com/robot/send?access_token=secret-token "
smtp_host = " smtp.example.com "
smtp_username = " alerts "
smtp_from = " MnemoNAS <alerts@example.com> "
smtp_to = [" admin@example.com ", " ops@example.com "]
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
	if cfg.Alerts.WeComWebhookURL != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key" {
		t.Fatalf("alerts WeCom webhook URL = %q, want trimmed URL with query", cfg.Alerts.WeComWebhookURL)
	}
	if cfg.Alerts.DingTalkWebhookURL != "https://oapi.dingtalk.com/robot/send?access_token=secret-token" {
		t.Fatalf("alerts DingTalk webhook URL = %q, want trimmed URL with query", cfg.Alerts.DingTalkWebhookURL)
	}
	if cfg.Alerts.SMTPHost != "smtp.example.com" || cfg.Alerts.SMTPUsername != "alerts" {
		t.Fatalf("alerts SMTP fields were not normalized: %+v", cfg.Alerts)
	}
	if cfg.Alerts.SMTPFrom != "MnemoNAS <alerts@example.com>" {
		t.Fatalf("alerts SMTP sender = %q, want trimmed sender", cfg.Alerts.SMTPFrom)
	}
	if got := strings.Join(cfg.Alerts.SMTPTo, ","); got != "admin@example.com,ops@example.com" {
		t.Fatalf("alerts SMTP recipients = %q, want trimmed recipients", got)
	}
}

func TestLoad_NormalizesSharePolicyRules(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[share]

[[share.policy_rules]]
path = " /Family/Photos/ "
require_password = true
max_expires_in = "24h"
max_access = 12
allowed_users = ["Alice", "alice"]
allowed_groups = ["Family"]
allowed_roles = ["User"]
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Share.PolicyRules) != 1 {
		t.Fatalf("share policy rules = %d, want 1", len(cfg.Share.PolicyRules))
	}
	rule := cfg.Share.PolicyRules[0]
	if rule.Path != "/Family/Photos" {
		t.Fatalf("policy rule path = %q, want clean path", rule.Path)
	}
	if !rule.RequirePassword {
		t.Fatal("policy rule require_password = false, want true")
	}
	if rule.MaxExpiresIn != 24*time.Hour {
		t.Fatalf("policy rule max expiry = %s, want 24h", rule.MaxExpiresIn)
	}
	if rule.MaxAccess != 12 {
		t.Fatalf("policy rule max access = %d, want 12", rule.MaxAccess)
	}
	if got, want := rule.AllowedUsers, []string{"alice"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("policy rule allowed users = %q, want %q", got, want)
	}
	if got, want := rule.AllowedGroups, []string{"family"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("policy rule allowed groups = %q, want %q", got, want)
	}
	if got, want := rule.AllowedRoles, []string{"user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("policy rule allowed roles = %q, want %q", got, want)
	}
}

func TestLoad_RejectsSharePolicyRuleDotSegmentPaths(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[share]

[[share.policy_rules]]
path = "/Family/../Private"
require_password = true
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "share.policy_rules[0].path must be clean") {
		t.Fatalf("Load() error = %v, want dirty share policy path rejection", err)
	}
}

func TestLoad_AcceptsZeroDurationStringSentinels(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[storage.retention]
gc_interval = "0"

[share]
default_expires_in = "0"

[[share.policy_rules]]
path = "/Family"
require_password = true
max_expires_in = "0"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Storage.Retention.GCInterval != 0 {
		t.Fatalf("retention gc interval = %s, want 0", cfg.Storage.Retention.GCInterval)
	}
	if cfg.Share.DefaultExpiresIn != 0 {
		t.Fatalf("share default expiry = %s, want 0", cfg.Share.DefaultExpiresIn)
	}
	if len(cfg.Share.PolicyRules) != 1 {
		t.Fatalf("share policy rules = %d, want 1", len(cfg.Share.PolicyRules))
	}
	if cfg.Share.PolicyRules[0].MaxExpiresIn != 0 {
		t.Fatalf("share policy max expiry = %s, want 0", cfg.Share.PolicyRules[0].MaxExpiresIn)
	}
}

func TestLoad_AcceptsEmptyShareDefaultExpiresIn(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[share]
default_expires_in = ""
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Share.DefaultExpiresIn != 0 {
		t.Fatalf("share default expiry = %s, want 0", cfg.Share.DefaultExpiresIn)
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
	if cfg.SMB.GatewaySocket != filepath.Join(wantInternal, "run", "smb-gateway.sock") {
		t.Fatalf("SMB gateway socket = %q, want %q", cfg.SMB.GatewaySocket, filepath.Join(wantInternal, "run", "smb-gateway.sock"))
	}
	if cfg.SMB.CredentialFile != filepath.Join(wantInternal, "smb-credentials.json") {
		t.Fatalf("SMB credential file = %q, want %q", cfg.SMB.CredentialFile, filepath.Join(wantInternal, "smb-credentials.json"))
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

[smb]
gateway_socket = "~/run/smb-gateway.sock"
credential_file = "~/data/smb-credentials.json"

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
	if cfg.SMB.GatewaySocket != filepath.Join(homeDir, "run", "smb-gateway.sock") {
		t.Fatalf("SMB gateway socket = %q, want %q", cfg.SMB.GatewaySocket, filepath.Join(homeDir, "run", "smb-gateway.sock"))
	}
	if cfg.SMB.CredentialFile != filepath.Join(homeDir, "data", "smb-credentials.json") {
		t.Fatalf("SMB credential file = %q, want %q", cfg.SMB.CredentialFile, filepath.Join(homeDir, "data", "smb-credentials.json"))
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

	assertExampleConfigMatchesDefaults(t, cfg)
	assertStorageRootDerivedInternalPaths(t, cfg)
	assertDefaultVersioningPolicy(t, cfg)
}

func TestLoad_LegacyMinimalConfigBackfillsCurrentDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "data")
	configPath := filepath.Join(tmpDir, "config.toml")
	content := fmt.Sprintf(`
[server]
host = "127.0.0.1"
port = 18080

[storage]
root = %q

[webdav]
enabled = true
prefix = "/dav"
auth_type = "basic"

[auth]
enabled = true

[share]
enabled = false

[log]
level = "info"
format = "console"
output = "stdout"
`, storageRoot)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Port != 18080 {
		t.Fatalf("server port = %d, want legacy override 18080", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 30*time.Second || cfg.Server.WriteTimeout != time.Minute || cfg.Server.IdleTimeout != 2*time.Minute {
		t.Fatalf("server timeouts were not backfilled: %+v", cfg.Server)
	}
	if cfg.DataPlane.GRPCAddress != "127.0.0.1:9090" || cfg.DataPlane.Timeout != 30*time.Second || cfg.DataPlane.MaxRetries != 3 {
		t.Fatalf("dataplane defaults were not backfilled: %+v", cfg.DataPlane)
	}
	if cfg.Storage.Retention.MaxVersions != 50 || cfg.Storage.Retention.GCInterval != 24*time.Hour || cfg.Storage.Trash.RetentionDays != 30 {
		t.Fatalf("storage retention or trash defaults were not backfilled: %+v", cfg.Storage)
	}
	assertStorageRootDerivedInternalPaths(t, cfg)
	assertDefaultVersioningPolicy(t, cfg)
	if cfg.SMB.Enabled || cfg.SMB.Listen != "127.0.0.1:1445" || !cfg.SMB.SigningRequired {
		t.Fatalf("SMB defaults were not backfilled: %+v", cfg.SMB)
	}
	if cfg.Backup.Jobs == nil || len(cfg.Backup.Jobs) != 0 {
		t.Fatalf("backup jobs defaults were not backfilled as an empty slice: %+v", cfg.Backup.Jobs)
	}
	if cfg.Auth.AccessTokenTTL != 15*time.Minute || cfg.Auth.RefreshTokenTTL != 7*24*time.Hour {
		t.Fatalf("auth TTL defaults were not backfilled: %+v", cfg.Auth)
	}
	if cfg.Share.DefaultExpiresIn != 7*24*time.Hour || cfg.Share.DefaultMaxAccess != 0 {
		t.Fatalf("share defaults were not backfilled: %+v", cfg.Share)
	}
	if !cfg.Favorites.Enabled {
		t.Fatalf("favorites default was not backfilled: %+v", cfg.Favorites)
	}
	if cfg.Alerts.WebhookMethod != "POST" || cfg.Alerts.SMTPPort != 587 || cfg.Alerts.SMTPTo == nil {
		t.Fatalf("alert defaults were not backfilled: %+v", cfg.Alerts)
	}
	if cfg.DiskHealth.Command != "smartctl" || cfg.DiskHealth.CheckInterval != time.Hour || cfg.DiskHealth.Devices == nil {
		t.Fatalf("disk health defaults were not backfilled: %+v", cfg.DiskHealth)
	}
	if cfg.Maintenance.Scrub.ScheduleInterval != 7*24*time.Hour || cfg.Maintenance.Scrub.RetryInterval != time.Hour || cfg.Maintenance.Scrub.MaxRetries != 1 {
		t.Fatalf("maintenance scrub defaults were not backfilled: %+v", cfg.Maintenance.Scrub)
	}
	if cfg.Security.AllowUnsafeNoAuth {
		t.Fatalf("unsafe auth override default was not preserved: %+v", cfg.Security)
	}
}

func TestLoad_DocumentationConfigExamples(t *testing.T) {
	for _, docPath := range []string{
		filepath.Join("..", "..", "docs", "configuration.md"),
		filepath.Join("..", "..", "docs", "configuration.en.md"),
	} {
		t.Run(filepath.Base(docPath), func(t *testing.T) {
			data, err := os.ReadFile(docPath)
			if err != nil {
				t.Fatalf("failed to read documentation config example: %v", err)
			}
			configText := extractFirstTomlBlock(t, string(data))
			configPath := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
				t.Fatalf("failed to write documentation config example: %v", err)
			}
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			assertExampleConfigMatchesDefaults(t, cfg)
			assertStorageRootDerivedInternalPaths(t, cfg)
			assertDefaultVersioningPolicy(t, cfg)
		})
	}
}

func assertExampleConfigMatchesDefaults(t *testing.T, cfg *Config) {
	t.Helper()

	want := Default()
	// The example pins the runtime Basic Auth username default explicitly so the
	// config remains copyable for WebDAV clients.
	want.WebDAV.Username = "admin"

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{name: "server", got: cfg.Server, want: want.Server},
		{name: "storage", got: cfg.Storage, want: want.Storage},
		{name: "dataplane", got: cfg.DataPlane, want: want.DataPlane},
		{name: "webdav", got: cfg.WebDAV, want: want.WebDAV},
		{name: "smb", got: cfg.SMB, want: want.SMB},
		{name: "backup", got: cfg.Backup, want: want.Backup},
		{name: "auth", got: cfg.Auth, want: want.Auth},
		{name: "share", got: cfg.Share, want: want.Share},
		{name: "favorites", got: cfg.Favorites, want: want.Favorites},
		{name: "alerts", got: cfg.Alerts, want: want.Alerts},
		{name: "disk_health", got: cfg.DiskHealth, want: want.DiskHealth},
		{name: "maintenance", got: cfg.Maintenance, want: want.Maintenance},
		{name: "security", got: cfg.Security, want: want.Security},
		{name: "log", got: cfg.Log, want: want.Log},
	}

	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Fatalf("example %s config = %#v, want default %#v", check.name, check.got, check.want)
		}
	}
}

func TestDocumentationEnvironmentOverridesAreMarkedUnsupported(t *testing.T) {
	docs := []struct {
		path          string
		unsupported   string
		forbiddenText []string
	}{
		{
			path:        filepath.Join("..", "..", "docs", "configuration.md"),
			unsupported: "环境变量配置覆盖尚未支持",
			forbiddenText: []string{
				"MNEMONAS_SERVER_PORT",
				"MNEMONAS_LOG_LEVEL",
				"MNEMONAS_WEBDAV_ENABLED",
			},
		},
		{
			path:        filepath.Join("..", "..", "docs", "configuration.en.md"),
			unsupported: "Environment-variable config overrides are planned but not currently supported",
			forbiddenText: []string{
				"MNEMONAS_SERVER_PORT",
				"MNEMONAS_LOG_LEVEL",
				"MNEMONAS_WEBDAV_ENABLED",
			},
		},
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read configuration documentation: %v", err)
			}
			text := string(data)
			if !strings.Contains(text, doc.unsupported) {
				t.Fatalf("configuration documentation should state unsupported environment overrides with %q", doc.unsupported)
			}
			for _, forbidden := range doc.forbiddenText {
				if strings.Contains(text, forbidden) {
					t.Errorf("configuration documentation still contains unsupported environment override example %q", forbidden)
				}
			}
		})
	}
}

func TestDocumentationBackupJobFieldsCoverConfigTags(t *testing.T) {
	expectedFields := []string{"`[[backup.jobs]]`"}
	backupJobType := reflect.TypeOf(BackupJobConfig{})
	for i := 0; i < backupJobType.NumField(); i++ {
		field := backupJobType.Field(i)
		name := strings.Split(field.Tag.Get("toml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		expectedFields = append(expectedFields, "`"+name+"`")
	}

	for _, docPath := range []string{
		filepath.Join("..", "..", "docs", "configuration.md"),
		filepath.Join("..", "..", "docs", "configuration.en.md"),
	} {
		t.Run(filepath.Base(docPath), func(t *testing.T) {
			data, err := os.ReadFile(docPath)
			if err != nil {
				t.Fatalf("failed to read configuration documentation: %v", err)
			}
			section := documentationSectionBetween(t, string(data), "## `[backup]`", "## `[auth]`")
			for _, expected := range expectedFields {
				if !strings.Contains(section, expected) {
					t.Errorf("backup configuration section does not document %s", expected)
				}
			}
			for _, jobType := range []string{"`local`", "`restic`", "`rclone`"} {
				if !strings.Contains(section, jobType) {
					t.Errorf("backup configuration section does not document job type %s", jobType)
				}
			}
		})
	}
}

func TestDocumentationPublicWebDAVPrefersUserAuth(t *testing.T) {
	docs := []struct {
		path        string
		start       string
		end         string
		mustContain []string
	}{
		{
			path:  filepath.Join("..", "..", "docs", "public-server-quickstart.md"),
			start: "## 4. WebDAV 地址",
			end:   "## 5. 上线前清单",
			mustContain: []string{
				"auth_type = \"users\"",
				"Web UI 用户账号",
				"secrets.json",
			},
		},
		{
			path:  filepath.Join("..", "..", "docs", "public-server-quickstart.en.md"),
			start: "## 4. WebDAV URL",
			end:   "## 5. Go-Live Checklist",
			mustContain: []string{
				"auth_type = \"users\"",
				"Web UI account credentials",
				"secrets.json",
			},
		},
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read public server quickstart: %v", err)
			}
			section := documentationSectionBetween(t, string(data), doc.start, doc.end)
			for _, expected := range doc.mustContain {
				if !strings.Contains(section, expected) {
					t.Errorf("public WebDAV section does not contain %q", expected)
				}
			}
		})
	}
}

func assertDefaultVersioningPolicy(t *testing.T, cfg *Config) {
	t.Helper()

	want := Default().Storage.Versioning
	if !reflect.DeepEqual(cfg.Storage.Versioning.AutoVersionedExtensions, want.AutoVersionedExtensions) {
		t.Fatalf("auto versioned extensions = %#v, want default %#v", cfg.Storage.Versioning.AutoVersionedExtensions, want.AutoVersionedExtensions)
	}
	if !reflect.DeepEqual(cfg.Storage.Versioning.AutoVersionedFilenames, want.AutoVersionedFilenames) {
		t.Fatalf("auto versioned filenames = %#v, want default %#v", cfg.Storage.Versioning.AutoVersionedFilenames, want.AutoVersionedFilenames)
	}
	if cfg.Storage.Versioning.MaxVersionedSize != want.MaxVersionedSize {
		t.Fatalf("max versioned size = %d, want default %d", cfg.Storage.Versioning.MaxVersionedSize, want.MaxVersionedSize)
	}
}

func assertStorageRootDerivedInternalPaths(t *testing.T, cfg *Config) {
	t.Helper()

	internalRoot := filepath.Join(cfg.Storage.Root, ".mnemonas")
	expected := map[string]string{
		"server.tls.cert_dir":  filepath.Join(internalRoot, "certs"),
		"auth.users_file":      filepath.Join(internalRoot, "users.json"),
		"share.store_file":     filepath.Join(internalRoot, "shares.json"),
		"favorites.store_file": filepath.Join(internalRoot, "favorites.json"),
		"smb.gateway_socket":   filepath.Join(internalRoot, "run", "smb-gateway.sock"),
		"smb.credential_file":  filepath.Join(internalRoot, "smb-credentials.json"),
	}
	actual := map[string]string{
		"server.tls.cert_dir":  cfg.Server.TLS.CertDir,
		"auth.users_file":      cfg.Auth.UsersFile,
		"share.store_file":     cfg.Share.StoreFile,
		"favorites.store_file": cfg.Favorites.StoreFile,
		"smb.gateway_socket":   cfg.SMB.GatewaySocket,
		"smb.credential_file":  cfg.SMB.CredentialFile,
	}

	for field, want := range expected {
		if got := actual[field]; got != want {
			t.Fatalf("%s = %q, want %q", field, got, want)
		}
	}
}

func extractFirstTomlBlock(t *testing.T, markdown string) string {
	t.Helper()
	const marker = "```toml\n"
	start := strings.Index(markdown, marker)
	if start < 0 {
		t.Fatal("documentation config example does not contain a TOML block")
	}
	start += len(marker)
	end := strings.Index(markdown[start:], "\n```")
	if end < 0 {
		t.Fatal("documentation config example TOML block is not closed")
	}
	return markdown[start : start+end]
}

func documentationSectionBetween(t *testing.T, markdown, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(markdown, startMarker)
	if start < 0 {
		t.Fatalf("documentation does not contain start marker %q", startMarker)
	}
	endOffset := start + len(startMarker)
	end := strings.Index(markdown[endOffset:], endMarker)
	if end < 0 {
		t.Fatalf("documentation section %q does not contain end marker %q", startMarker, endMarker)
	}
	return markdown[start : endOffset+end]
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
		filepath.Join(tmpDir, ".mnemonas", "run"),
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
		filepath.Join(tmpDir, ".mnemonas/run"): 0700,
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
