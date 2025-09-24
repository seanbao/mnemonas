package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func saveSecurityCheckGeneratedWebDAVSecret(t *testing.T, dataRoot string) {
	t.Helper()
	if err := config.SaveSecrets(dataRoot, &config.Secrets{
		JWTSecret:      "security-check-jwt-secret",
		WebDAVPassword: "GeneratedWebDAVPassword123!",
	}); err != nil {
		t.Fatalf("save generated WebDAV secret: %v", err)
	}
}

func TestSecurityTCPAddressHost_ParsesBracketedIPv6Loopback(t *testing.T) {
	host := securityTCPAddressHost("[::1]:9091")
	if host != "::1" {
		t.Fatalf("securityTCPAddressHost() = %q, want %q", host, "::1")
	}
	if !securityListenHostIsLoopback(host) {
		t.Fatalf("expected %q to be treated as loopback", host)
	}
}

func TestSecurityTCPAddressHost_RejectsBareHostWithoutPort(t *testing.T) {
	if host := securityTCPAddressHost("127.0.0.1"); host != "" {
		t.Fatalf("securityTCPAddressHost() = %q, want empty host for malformed TCP address", host)
	}
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
	if check := securityCheckByID(t, payload.Data.Checks, "login_rate_limit"); check.Status != securityCheckWarning {
		t.Fatalf("login_rate_limit status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
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

func TestSecurityInitialPasswordFileCheck_ClassifiesPathKinds(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		setup      func(t *testing.T, path string)
		wantStatus securityCheckStatus
		wantKind   string
	}{
		{
			name:       "absent",
			setup:      func(t *testing.T, path string) {},
			wantStatus: securityCheckPass,
		},
		{
			name: "regular file",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("initial password"), 0o600); err != nil {
					t.Fatalf("write initial password file: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "regular",
		},
		{
			name: "directory",
			setup: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("mkdir initial password path: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "not_regular",
		},
		{
			name: "broken symlink",
			setup: func(t *testing.T, path string) {
				if err := os.Symlink(filepath.Join(filepath.Dir(path), "missing-password"), path); err != nil {
					t.Fatalf("symlink initial password path: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, strings.ReplaceAll(tt.name, " ", "-"), "initial-password.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("mkdir case dir: %v", err)
			}
			tt.setup(t, path)

			check := securityInitialPasswordFileCheck(path)
			if check.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; check=%#v", check.Status, tt.wantStatus, check)
			}
			if tt.wantKind != "" {
				if got := check.Details["path_kind"]; got != tt.wantKind {
					t.Fatalf("path_kind = %#v, want %q", got, tt.wantKind)
				}
			}
		})
	}
}

func TestSecurityInitialPasswordFileCheck_BlocksEmptyPath(t *testing.T) {
	check := securityInitialPasswordFileCheck("  ")
	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "初始管理员密码路径无法确定" {
		t.Fatalf("title = %q, want empty path title", check.Title)
	}
	if got := check.Details["path"]; got != "" {
		t.Fatalf("path = %#v, want empty string", got)
	}
}

func TestSecurityInitialPasswordFilePath_EmptyUsersFileDoesNotUseWorkingDirectory(t *testing.T) {
	if got := securityInitialPasswordFilePath(""); got != "" {
		t.Fatalf("path = %q, want empty string", got)
	}
	if got := securityInitialPasswordFilePath("   "); got != "" {
		t.Fatalf("blank path = %q, want empty string", got)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksUnsafeShareBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
	}{
		{
			name:    "http",
			baseURL: "http://nas.example.test",
		},
		{
			name:    "non-default HTTPS port",
			baseURL: "https://nas.example.test:8443",
		},
		{
			name:    "userinfo",
			baseURL: "https://operator@nas.example.test",
		},
		{
			name:    "query",
			baseURL: "https://nas.example.test?token=secret",
		},
		{
			name:    "empty query",
			baseURL: "https://nas.example.test?",
		},
		{
			name:    "fragment",
			baseURL: "https://nas.example.test#share",
		},
		{
			name:    "empty fragment",
			baseURL: "https://nas.example.test#",
		},
		{
			name:    "empty host label",
			baseURL: "https://nas..example.test",
		},
		{
			name:    "multiple trailing host dots",
			baseURL: "https://nas.example.test..",
		},
		{
			name:    "invalid host character",
			baseURL: "https://nas_example.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.Storage.Root = tmpDir
			cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
			cfg.Server.Host = "127.0.0.1"
			cfg.Server.TrustedProxyHops = 1
			cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.AuthType = "basic"
			cfg.Share.Enabled = true
			cfg.Share.BaseURL = tt.baseURL
			t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
			saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

			server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
			if err != nil {
				t.Fatalf("NewServer() error: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
			req.RemoteAddr = "127.0.0.1:1234"
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

			check := securityCheckByID(t, payload.Data.Checks, "share_base_url")
			if check.Status != securityCheckBlock {
				t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
			}
		})
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsWhenShareBaseURLHostDiffersFromRequestHost(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.Share.Enabled = true
	cfg.Share.BaseURL = "https://share.example.test"
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.Host = "nas.example.test"
	req.RemoteAddr = "127.0.0.1:1234"
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

	check := securityCheckByID(t, payload.Data.Checks, "share_base_url")
	if check.Status != securityCheckWarning {
		t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsWhenShareBaseURLEndsWithShareRoute(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
	}{
		{
			name:    "root share route",
			baseURL: "https://nas.example.test/s/",
			path:    "/s/",
		},
		{
			name:    "base path share route",
			baseURL: "https://nas.example.test/base/s",
			path:    "/base/s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.Storage.Root = tmpDir
			cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
			cfg.Server.Host = "127.0.0.1"
			cfg.Server.TrustedProxyHops = 1
			cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.AuthType = "basic"
			cfg.Share.Enabled = true
			cfg.Share.BaseURL = tt.baseURL
			t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
			saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

			server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
			if err != nil {
				t.Fatalf("NewServer() error: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
			req.Host = "nas.example.test"
			req.RemoteAddr = "127.0.0.1:1234"
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

			check := securityCheckByID(t, payload.Data.Checks, "share_base_url")
			if check.Status != securityCheckWarning {
				t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
			}
			if check.Title != "分享基础 URL 包含分享路由" {
				t.Fatalf("share_base_url title = %q, want share route warning", check.Title)
			}
			if got := check.Details["base_url_path"]; got != tt.path {
				t.Fatalf("base_url_path = %#v, want %q", got, tt.path)
			}
		})
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsForWeakWebDAVBasicPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		risk     string
	}{
		{
			name:     "placeholder",
			password: "change-this-webdav-password",
			risk:     "placeholder",
		},
		{
			name:     "too short",
			password: "short-pass",
			risk:     "too_short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.Storage.Root = tmpDir
			cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
			cfg.Server.Host = "127.0.0.1"
			cfg.Server.TrustedProxyHops = 1
			cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.AuthType = "basic"
			cfg.WebDAV.Password = tt.password
			cfg.Share.Enabled = false
			t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

			server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
			if err != nil {
				t.Fatalf("NewServer() error: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
			req.RemoteAddr = "127.0.0.1:1234"
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

			check := securityCheckByID(t, payload.Data.Checks, "webdav_auth")
			if check.Status != securityCheckWarning {
				t.Fatalf("webdav_auth status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
			}
			if check.Title != "WebDAV Basic 密码需要更换" {
				t.Fatalf("webdav_auth title = %q, want weak password warning", check.Title)
			}
			if got := check.Details["password_risk"]; got != tt.risk {
				t.Fatalf("password_risk = %#v, want %q", got, tt.risk)
			}
			if _, ok := check.Details["password"]; ok {
				t.Fatalf("webdav_auth details must not expose password: %#v", check.Details)
			}
		})
	}
}

func TestSecurityWebDAVPrefixRisk_ClassifiesPublicPrefixRisks(t *testing.T) {
	tests := []struct {
		name           string
		prefix         string
		wantNormalized string
		wantRisk       string
	}{
		{
			name:           "valid default",
			prefix:         "/dav",
			wantNormalized: "/dav",
		},
		{
			name:           "valid relative",
			prefix:         "files/team",
			wantNormalized: "/files/team",
		},
		{
			name:           "empty",
			prefix:         "",
			wantNormalized: "/",
			wantRisk:       "empty",
		},
		{
			name:           "root",
			prefix:         "/",
			wantNormalized: "/",
			wantRisk:       "root",
		},
		{
			name:           "reserved route child",
			prefix:         "/api/v1",
			wantNormalized: "/api/v1",
			wantRisk:       "reserved_route",
		},
		{
			name:           "invalid characters",
			prefix:         "/dav?token",
			wantNormalized: "/dav?token",
			wantRisk:       "invalid_characters",
		},
		{
			name:           "similar reserved prefix is valid",
			prefix:         "/api-files",
			wantNormalized: "/api-files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, risk := securityWebDAVPrefixRisk(tt.prefix)
			if normalized != tt.wantNormalized {
				t.Fatalf("normalized = %q, want %q", normalized, tt.wantNormalized)
			}
			if risk != tt.wantRisk {
				t.Fatalf("risk = %q, want %q", risk, tt.wantRisk)
			}
		})
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksUnsafeWebDAVPrefix(t *testing.T) {
	tests := []struct {
		name           string
		prefix         string
		wantTitle      string
		wantRisk       string
		wantNormalized string
	}{
		{
			name:           "empty",
			prefix:         "",
			wantTitle:      "WebDAV 前缀为空",
			wantRisk:       "empty",
			wantNormalized: "/",
		},
		{
			name:           "reserved route",
			prefix:         "/api/v1",
			wantTitle:      "WebDAV 前缀占用保留路由",
			wantRisk:       "reserved_route",
			wantNormalized: "/api/v1",
		},
		{
			name:           "invalid characters",
			prefix:         "/dav#files",
			wantTitle:      "WebDAV 前缀格式无效",
			wantRisk:       "invalid_characters",
			wantNormalized: "/dav#files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.Storage.Root = tmpDir
			cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
			cfg.Server.Host = "127.0.0.1"
			cfg.Server.TrustedProxyHops = 1
			cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.Prefix = tt.prefix
			cfg.WebDAV.AuthType = "basic"
			cfg.WebDAV.Password = "StrongWebDAVPassword123!"
			cfg.Share.Enabled = false
			t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

			server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
			if err != nil {
				t.Fatalf("NewServer() error: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
			req.RemoteAddr = "127.0.0.1:1234"
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

			check := securityCheckByID(t, payload.Data.Checks, "webdav_prefix")
			if check.Status != securityCheckBlock {
				t.Fatalf("webdav_prefix status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
			}
			if check.Title != tt.wantTitle {
				t.Fatalf("webdav_prefix title = %q, want %q", check.Title, tt.wantTitle)
			}
			if got := check.Details["prefix_risk"]; got != tt.wantRisk {
				t.Fatalf("prefix_risk = %#v, want %q", got, tt.wantRisk)
			}
			if got := check.Details["normalized_prefix"]; got != tt.wantNormalized {
				t.Fatalf("normalized_prefix = %#v, want %q", got, tt.wantNormalized)
			}
		})
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksUnavailableGeneratedWebDAVPassword(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = filepath.Join(tmpDir, "missing-secrets")
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.WebDAV.Password = ""
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

	if err := os.MkdirAll(filepath.Dir(cfg.Auth.UsersFile), 0o700); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
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

	check := securityCheckByID(t, payload.Data.Checks, "webdav_auth")
	if check.Status != securityCheckBlock {
		t.Fatalf("webdav_auth status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "自动 WebDAV 密码不可用" {
		t.Fatalf("webdav_auth title = %q, want unavailable generated password", check.Title)
	}
	if got := check.Details["password_source"]; got != "generated" {
		t.Fatalf("password_source = %#v, want generated", got)
	}
	if got := check.Details["generated_password_available"]; got != false {
		t.Fatalf("generated_password_available = %#v, want false", got)
	}
	if _, ok := check.Details["password"]; ok {
		t.Fatalf("webdav_auth details must not expose password: %#v", check.Details)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsForWeakGeneratedWebDAVPassword(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.WebDAV.Password = ""
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")

	if err := os.MkdirAll(filepath.Dir(cfg.Auth.UsersFile), 0o700); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := config.SaveSecrets(tmpDir, &config.Secrets{JWTSecret: "jwt-secret", WebDAVPassword: "password123"}); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
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

	check := securityCheckByID(t, payload.Data.Checks, "webdav_auth")
	if check.Status != securityCheckWarning {
		t.Fatalf("webdav_auth status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
	if check.Title != "自动 WebDAV 密码需要更换" {
		t.Fatalf("webdav_auth title = %q, want weak generated password", check.Title)
	}
	if got := check.Details["password_source"]; got != "generated" {
		t.Fatalf("password_source = %#v, want generated", got)
	}
	if got := check.Details["password_risk"]; got != "placeholder" {
		t.Fatalf("password_risk = %#v, want placeholder", got)
	}
	if _, ok := check.Details["password"]; ok {
		t.Fatalf("webdav_auth details must not expose password: %#v", check.Details)
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
	if check := securityCheckByID(t, payload.Data.Checks, "session_token_ttl"); check.Status != securityCheckPass {
		t.Fatalf("session_token_ttl status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "login_rate_limit"); check.Status != securityCheckPass {
		t.Fatalf("login_rate_limit status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	} else {
		if got := check.Details["failure_limit"]; got != float64(5) && got != 5 {
			t.Fatalf("login_rate_limit failure_limit = %#v, want 5", got)
		}
		if got := check.Details["key_scope"]; got != "username_and_client_ip" {
			t.Fatalf("login_rate_limit key_scope = %#v, want username_and_client_ip", got)
		}
		if _, ok := check.Details["password"]; ok {
			t.Fatalf("login_rate_limit details must not expose passwords: %#v", check.Details)
		}
	}
	if check := securityCheckByID(t, payload.Data.Checks, "browser_session_boundary"); check.Status != securityCheckPass {
		t.Fatalf("browser_session_boundary status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "public_share_boundary"); check.Status != securityCheckPass {
		t.Fatalf("public_share_boundary status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	} else if got := check.Details["share_enabled"]; got != false {
		t.Fatalf("public_share_boundary share_enabled = %#v, want false", got)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "share_default_policy"); check.Status != securityCheckPass {
		t.Fatalf("share_default_policy status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "users_file_access"); check.Status != securityCheckPass {
		t.Fatalf("users_file_access status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "webdav_prefix"); check.Status != securityCheckPass {
		t.Fatalf("webdav_prefix status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "smb_preview"); check.Status != securityCheckPass {
		t.Fatalf("smb_preview status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "initial_password_file"); check.Status != securityCheckPass {
		t.Fatalf("initial_password_file status = %q, want %q", check.Status, securityCheckPass)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsWhenBrowserSessionCookieWouldBeInsecure(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 0
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = false
	cfg.Share.Enabled = true
	cfg.Share.BaseURL = "https://nas.example.test"
	cfg.Share.DefaultExpiresIn = 168 * time.Hour
	cfg.Share.DefaultMaxAccess = 20
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
		AuthJWTSecret:  "security-check-browser-session-secret",
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

	check := securityCheckByID(t, payload.Data.Checks, "browser_session_boundary")
	if check.Status != securityCheckWarning {
		t.Fatalf("browser_session_boundary status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
	if check.Title != "浏览器会话 cookie 未使用 Secure" {
		t.Fatalf("browser_session_boundary title = %q, want insecure cookie warning", check.Title)
	}
	if got := check.Details["session_cookie_secure"]; got != false {
		t.Fatalf("session_cookie_secure = %#v, want false", got)
	}
	if got := check.Details["same_origin_browser_write_protection"]; got != true {
		t.Fatalf("same_origin_browser_write_protection = %#v, want true", got)
	}
	if _, ok := check.Details["access_token"]; ok {
		t.Fatalf("browser_session_boundary details must not expose tokens: %#v", check.Details)
	}

	publicShareCheck := securityCheckByID(t, payload.Data.Checks, "public_share_boundary")
	if publicShareCheck.Status != securityCheckWarning {
		t.Fatalf("public_share_boundary status = %q, want %q; check=%#v", publicShareCheck.Status, securityCheckWarning, publicShareCheck)
	}
	if publicShareCheck.Title != "公开分享访问 cookie 未使用 Secure" {
		t.Fatalf("public_share_boundary title = %q, want insecure cookie warning", publicShareCheck.Title)
	}
	if got := publicShareCheck.Details["password_cookie_secure"]; got != false {
		t.Fatalf("password_cookie_secure = %#v, want false", got)
	}
	if got := publicShareCheck.Details["password_cookie_same_site"]; got != "Strict" {
		t.Fatalf("password_cookie_same_site = %#v, want Strict", got)
	}
	if got := publicShareCheck.Details["metadata_vary_cookie"]; got != true {
		t.Fatalf("metadata_vary_cookie = %#v, want true", got)
	}
	if _, ok := publicShareCheck.Details["password"]; ok {
		t.Fatalf("public_share_boundary details must not expose passwords: %#v", publicShareCheck.Details)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsForLongSessionTokenTTL(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Auth.AccessTokenTTL = 2 * time.Hour
	cfg.Auth.RefreshTokenTTL = 45 * 24 * time.Hour
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
		AuthJWTSecret:  "security-check-long-session-ttl-secret",
		AuthAccessTTL:  cfg.Auth.AccessTokenTTL,
		AuthRefreshTTL: cfg.Auth.RefreshTokenTTL,
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
	if payload.Data.Status != securityCheckWarning {
		t.Fatalf("overall status = %q, want %q; checks=%#v", payload.Data.Status, securityCheckWarning, payload.Data.Checks)
	}
	check := securityCheckByID(t, payload.Data.Checks, "session_token_ttl")
	if check.Status != securityCheckWarning {
		t.Fatalf("session_token_ttl status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
	if check.Title != "会话有效期偏长" {
		t.Fatalf("session_token_ttl title = %q, want long TTL warning", check.Title)
	}
	if got := check.Details["access_token_ttl_too_long"]; got != true {
		t.Fatalf("access_token_ttl_too_long = %#v, want true", got)
	}
	if got := check.Details["refresh_token_ttl_too_long"]; got != true {
		t.Fatalf("refresh_token_ttl_too_long = %#v, want true", got)
	}
	if _, ok := check.Details["access_token"]; ok {
		t.Fatalf("session_token_ttl details must not expose tokens: %#v", check.Details)
	}
	if _, ok := check.Details["refresh_token"]; ok {
		t.Fatalf("session_token_ttl details must not expose tokens: %#v", check.Details)
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsForUnsafeShareDefaultPolicy(t *testing.T) {
	tests := []struct {
		name       string
		expiresIn  time.Duration
		maxAccess  int64
		wantTitle  string
		wantDetail string
	}{
		{
			name:       "no expiry and unlimited max access",
			expiresIn:  0,
			maxAccess:  0,
			wantTitle:  "新分享默认不会过期且访问次数不限制",
			wantDetail: "default_expires_in_unlimited",
		},
		{
			name:       "no expiry",
			expiresIn:  0,
			maxAccess:  20,
			wantTitle:  "新分享默认不会过期",
			wantDetail: "default_expires_in_unlimited",
		},
		{
			name:       "too long",
			expiresIn:  45 * 24 * time.Hour,
			maxAccess:  20,
			wantTitle:  "新分享默认有效期偏长",
			wantDetail: "default_expires_in_too_long",
		},
		{
			name:       "unlimited max access",
			expiresIn:  168 * time.Hour,
			maxAccess:  0,
			wantTitle:  "新分享默认访问次数不限制",
			wantDetail: "default_max_access_unlimited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.Storage.Root = tmpDir
			cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
			cfg.Server.Host = "127.0.0.1"
			cfg.Server.TrustedProxyHops = 1
			cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.AuthType = "basic"
			cfg.Share.Enabled = true
			cfg.Share.BaseURL = "https://nas.example.test"
			cfg.Share.DefaultExpiresIn = tt.expiresIn
			cfg.Share.DefaultMaxAccess = tt.maxAccess
			t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
			saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
				AuthJWTSecret:  "security-check-share-default-policy-secret",
				AuthAccessTTL:  15 * time.Minute,
				AuthRefreshTTL: 24 * time.Hour,
			})
			if err != nil {
				t.Fatalf("NewServer() error: %v", err)
			}

			token := loginAndGetAccessToken(t, server, "admin", password)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
			req.Host = "nas.example.test"
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
			if payload.Data.Status != securityCheckWarning {
				t.Fatalf("overall status = %q, want %q; checks=%#v", payload.Data.Status, securityCheckWarning, payload.Data.Checks)
			}
			check := securityCheckByID(t, payload.Data.Checks, "share_default_policy")
			if check.Status != securityCheckWarning {
				t.Fatalf("share_default_policy status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
			}
			if check.Title != tt.wantTitle {
				t.Fatalf("share_default_policy title = %q, want %q", check.Title, tt.wantTitle)
			}
			if got := check.Details[tt.wantDetail]; got != true {
				t.Fatalf("%s = %#v, want true; details=%#v", tt.wantDetail, got, check.Details)
			}
			wantUnlimitedMaxAccess := tt.maxAccess == 0
			if got := check.Details["default_max_access_unlimited"]; got != wantUnlimitedMaxAccess {
				t.Fatalf("default_max_access_unlimited = %#v, want %v", got, wantUnlimitedMaxAccess)
			}
		})
	}
}

func TestServer_GetSettingsSecurityCheck_WarnsForOpenUsersFileAccess(t *testing.T) {
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
		AuthJWTSecret:  "security-check-users-file-warning-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	if err := os.Chmod(filepath.Dir(cfg.Auth.UsersFile), 0o755); err != nil {
		t.Fatalf("chmod users dir: %v", err)
	}
	if err := os.Chmod(cfg.Auth.UsersFile, 0o644); err != nil {
		t.Fatalf("chmod users file: %v", err)
	}

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
	if payload.Data.Status != securityCheckWarning {
		t.Fatalf("overall status = %q, want %q; checks=%#v", payload.Data.Status, securityCheckWarning, payload.Data.Checks)
	}
	check := securityCheckByID(t, payload.Data.Checks, "users_file_access")
	if check.Status != securityCheckWarning {
		t.Fatalf("users_file_access status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
	if got := check.Details["path"]; got != cfg.Auth.UsersFile {
		t.Fatalf("path = %#v, want %q", got, cfg.Auth.UsersFile)
	}
	if got := check.Details["dir"]; got != filepath.Dir(cfg.Auth.UsersFile) {
		t.Fatalf("dir = %#v, want %q", got, filepath.Dir(cfg.Auth.UsersFile))
	}
	if got := check.Details["dir_mode"]; got != "0755" {
		t.Fatalf("dir_mode = %#v, want 0755", got)
	}
	if got := check.Details["file_mode"]; got != "0644" {
		t.Fatalf("file_mode = %#v, want 0644", got)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksSymlinkUsersFile(t *testing.T) {
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
		AuthJWTSecret:  "security-check-users-file-symlink-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	symlinkTarget := filepath.Join(tmpDir, "linked-users.json")
	if err := os.Rename(cfg.Auth.UsersFile, symlinkTarget); err != nil {
		t.Fatalf("rename users file: %v", err)
	}
	if err := os.Symlink(symlinkTarget, cfg.Auth.UsersFile); err != nil {
		t.Fatalf("symlink users file: %v", err)
	}

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
	check := securityCheckByID(t, payload.Data.Checks, "users_file_access")
	if check.Status != securityCheckBlock {
		t.Fatalf("users_file_access status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["path"]; got != cfg.Auth.UsersFile {
		t.Fatalf("path = %#v, want %q", got, cfg.Auth.UsersFile)
	}
	if got := check.Details["dir"]; got != filepath.Dir(cfg.Auth.UsersFile) {
		t.Fatalf("dir = %#v, want %q", got, filepath.Dir(cfg.Auth.UsersFile))
	}
	if got := check.Details["file_kind"]; got != "symlink" {
		t.Fatalf("file_kind = %#v, want symlink", got)
	}
}

func TestServer_GetSettingsSecurityCheck_UsesRuntimeAuthUsersFileForInitialPassword(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(tmpDir, "config", "users.json")
	runtimeUsersFile := filepath.Join(tmpDir, "runtime", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

	_, password, err := auth.NewUserStore(runtimeUsersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:         cfg,
		AuthEnabled:    true,
		AuthUsersFile:  runtimeUsersFile,
		AuthJWTSecret:  "security-check-runtime-users-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	if password == "" {
		t.Fatal("expected bootstrap admin password")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	server.handleGetSecurityCheck(rec, req)

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

	check := securityCheckByID(t, payload.Data.Checks, "initial_password_file")
	if check.Status != securityCheckBlock {
		t.Fatalf("initial_password_file status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	wantPath := filepath.Join(filepath.Dir(runtimeUsersFile), "initial-password.txt")
	if got := check.Details["path"]; got != wantPath {
		t.Fatalf("initial_password_file path = %#v, want %q", got, wantPath)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksSymlinkInitialPasswordPath(t *testing.T) {
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
		AuthJWTSecret:  "security-check-initial-password-symlink-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	initialPasswordPath := filepath.Join(filepath.Dir(cfg.Auth.UsersFile), "initial-password.txt")
	if err := os.Symlink(filepath.Join(tmpDir, "missing-initial-password.txt"), initialPasswordPath); err != nil {
		t.Fatalf("symlink initial password path: %v", err)
	}

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
	check := securityCheckByID(t, payload.Data.Checks, "initial_password_file")
	if check.Status != securityCheckBlock {
		t.Fatalf("initial_password_file status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["path"]; got != initialPasswordPath {
		t.Fatalf("path = %#v, want %q", got, initialPasswordPath)
	}
	if got := check.Details["path_kind"]; got != "symlink" {
		t.Fatalf("path_kind = %#v, want symlink", got)
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

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

func TestServer_GetSettingsSecurityCheck_BlocksPrivateForwardedProtoWithoutTrustedCIDR(t *testing.T) {
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "10.0.0.2:1234"
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

func TestServer_GetSettingsSecurityCheck_WarnsForTrustedForwardedProtoHTTP(t *testing.T) {
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
	saveSecurityCheckGeneratedWebDAVSecret(t, tmpDir)

	server, err := NewServer(zerolog.Nop(), &ServerConfig{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/security-check", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "http")
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
	if check := securityCheckByID(t, payload.Data.Checks, "forwarded_proto_trust"); check.Status != securityCheckWarning {
		t.Fatalf("forwarded_proto_trust status = %q, want %q", check.Status, securityCheckWarning)
	}
}
