package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

func securityCheckByID(t *testing.T, checks []securityCheckItem, id string) securityCheckItem {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("security check %q not found in %#v", id, checks)
	return securityCheckItem{}
}

func TestServer_GetSettingsSecurityCheck_ReportsPublicDeploymentRisks(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.TrustedProxyHops = 0
	cfg.Server.TLS.Enabled = false
	cfg.Security.AllowUnsafeNoAuth = true
	cfg.DataPlane.GRPCAddress = "0.0.0.0:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "none"
	cfg.SMB.Enabled = true
	cfg.SMB.Listen = "0.0.0.0:1445"
	cfg.SMB.Shares = []config.SMBShareConfig{{
		Name:         "homes",
		Path:         "/",
		AllowedRoles: []string{"admin"},
	}}
	cfg.Share.Enabled = true
	cfg.Share.BaseURL = "http://nas.example.test"
	t.Setenv("DATAPLANE_HTTP_ADDR", "0.0.0.0:9091")

	if err := os.MkdirAll(filepath.Dir(cfg.Auth.UsersFile), 0o700); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	initialPasswordFile := filepath.Join(filepath.Dir(cfg.Auth.UsersFile), "initial-password.txt")
	if err := os.WriteFile(initialPasswordFile, []byte("initial password"), 0o600); err != nil {
		t.Fatalf("write initial password file: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("security check status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Success bool                  `json:"success"`
		Data    securityCheckResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode security check response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response: %s", rec.Body.String())
	}
	if payload.Data.Status != securityCheckBlock {
		t.Fatalf("overall status = %q, want %q", payload.Data.Status, securityCheckBlock)
	}

	if check := securityCheckByID(t, payload.Data.Checks, "auth_enabled"); check.Status != securityCheckBlock {
		t.Fatalf("auth_enabled status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "unsafe_no_auth_override"); check.Status != securityCheckBlock {
		t.Fatalf("unsafe_no_auth_override status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "public_http_exposure"); check.Status != securityCheckBlock {
		t.Fatalf("public_http_exposure status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "server_listen"); check.Status != securityCheckBlock {
		t.Fatalf("server_listen status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "dataplane_listen"); check.Status != securityCheckBlock {
		t.Fatalf("dataplane_listen status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "dataplane_http_listen"); check.Status != securityCheckBlock {
		t.Fatalf("dataplane_http_listen status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "webdav_auth"); check.Status != securityCheckBlock {
		t.Fatalf("webdav_auth status = %q, want %q", check.Status, securityCheckBlock)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "smb_preview"); check.Status != securityCheckWarning {
		t.Fatalf("smb_preview status = %q, want %q", check.Status, securityCheckWarning)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "initial_password_file"); check.Status != securityCheckBlock {
		t.Fatalf("initial_password_file status = %q, want %q", check.Status, securityCheckBlock)
	}
}

func TestServer_GetSettingsSecurityCheck_PassesTrustedProxyLoopbackSetup(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

	store, password, err := auth.NewUserStore(cfg.Auth.UsersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if password == "" {
		t.Fatal("expected bootstrap admin password")
	}
	if _, err := store.Create("backup-admin", "password123", "backup-admin@test.local", auth.RoleAdmin); err != nil {
		t.Fatalf("create backup admin: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:         cfg,
		AuthEnabled:    true,
		AuthUsersFile:  cfg.Auth.UsersFile,
		AuthJWTSecret:  "security-check-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("security check status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Success bool                  `json:"success"`
		Data    securityCheckResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode security check response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response: %s", rec.Body.String())
	}
	if payload.Data.Status != securityCheckPass {
		t.Fatalf("overall status = %q, want %q; checks=%#v", payload.Data.Status, securityCheckPass, payload.Data.Checks)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "https_request"); check.Status != securityCheckPass {
		t.Fatalf("https_request status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "public_http_exposure"); check.Status != securityCheckPass {
		t.Fatalf("public_http_exposure status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "forwarded_proto_trust"); check.Status != securityCheckPass {
		t.Fatalf("forwarded_proto_trust status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "admin_accounts"); check.Status != securityCheckPass {
		t.Fatalf("admin_accounts status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "smb_preview"); check.Status != securityCheckPass {
		t.Fatalf("smb_preview status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "initial_password_file"); check.Status != securityCheckPass {
		t.Fatalf("initial_password_file status = %q, want %q", check.Status, securityCheckPass)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsWhenOnlyOneAdminExists(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

	_, password, err := auth.NewUserStore(cfg.Auth.UsersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:         cfg,
		AuthEnabled:    true,
		AuthUsersFile:  cfg.Auth.UsersFile,
		AuthJWTSecret:  "security-check-one-admin-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("security check status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Success bool                  `json:"success"`
		Data    securityCheckResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode security check response: %v", err)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "admin_accounts"); check.Status != securityCheckWarning {
		t.Fatalf("admin_accounts status = %q, want %q", check.Status, securityCheckWarning)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksUntrustedForwardedProtoSource(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.Enabled = false
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("security check status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Success bool                  `json:"success"`
		Data    securityCheckResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode security check response: %v", err)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "forwarded_proto_trust"); check.Status != securityCheckBlock {
		t.Fatalf("forwarded_proto_trust status = %q, want %q", check.Status, securityCheckBlock)
	}
}
