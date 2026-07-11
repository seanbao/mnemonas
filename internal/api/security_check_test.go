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
	"github.com/seanbao/mnemonas/internal/share"
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

func TestSecurityCheckErrorDetailRedactsSensitiveValues(t *testing.T) {
	err := &os.PathError{
		Op:   "open",
		Path: "/srv/mnemonas/token=restore-secret/secret_access_key=object-secret/config.toml",
		Err:  os.ErrPermission,
	}

	detail := securityCheckErrorDetail(err)

	for _, leaked := range []string{"restore-secret", "object-secret"} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("securityCheckErrorDetail() = %q, leaked %q", detail, leaked)
		}
	}
	for _, want := range []string{"token=<redacted>", "secret_access_key=<redacted>"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("securityCheckErrorDetail() = %q, want %q", detail, want)
		}
	}
	if got := securityCheckErrorDetail(nil); got != "" {
		t.Fatalf("securityCheckErrorDetail(nil) = %q, want empty", got)
	}
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

func TestSecurityPublicShareBoundaryCheck_PrioritizesBoundaryBlockOverHTTPSWarning(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.MetadataVaryCookie = false

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "http", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "公开分享浏览器边界异常" {
		t.Fatalf("title = %q, want boundary block title", check.Title)
	}
	if got := check.Details["password_cookie_secure"]; got != false {
		t.Fatalf("password_cookie_secure = %#v, want false", got)
	}
	if got := check.Details["metadata_vary_cookie"]; got != false {
		t.Fatalf("metadata_vary_cookie = %#v, want false", got)
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksWeakMetadataHeaders(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true

	tests := []struct {
		name          string
		mutate        func(*share.PublicShareAccessPolicy)
		detailKey     string
		detailWant    any
		cachePrivate  bool
		cacheNoCache  bool
		referrerValid bool
	}{
		{
			name: "public cache",
			mutate: func(policy *share.PublicShareAccessPolicy) {
				policy.MetadataCacheControl = "public, max-age=3600"
			},
			detailKey:     "metadata_cache_private",
			detailWant:    false,
			cachePrivate:  false,
			cacheNoCache:  false,
			referrerValid: true,
		},
		{
			name: "missing no-cache",
			mutate: func(policy *share.PublicShareAccessPolicy) {
				policy.MetadataCacheControl = "private"
			},
			detailKey:     "metadata_cache_no_cache",
			detailWant:    false,
			cachePrivate:  true,
			cacheNoCache:  false,
			referrerValid: true,
		},
		{
			name: "weak referrer policy",
			mutate: func(policy *share.PublicShareAccessPolicy) {
				policy.MetadataReferrerPolicy = "origin"
			},
			detailKey:     "metadata_referrer_no_referrer",
			detailWant:    false,
			cachePrivate:  true,
			cacheNoCache:  true,
			referrerValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := share.PublicShareAccessPolicySnapshot()
			tt.mutate(&policy)

			check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

			if check.Status != securityCheckBlock {
				t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
			}
			if check.Title != "公开分享浏览器边界异常" {
				t.Fatalf("title = %q, want boundary block title", check.Title)
			}
			if got := check.Details[tt.detailKey]; got != tt.detailWant {
				t.Fatalf("%s = %#v, want %#v", tt.detailKey, got, tt.detailWant)
			}
			if got := check.Details["metadata_cache_private"]; got != tt.cachePrivate {
				t.Fatalf("metadata_cache_private = %#v, want %#v", got, tt.cachePrivate)
			}
			if got := check.Details["metadata_cache_no_cache"]; got != tt.cacheNoCache {
				t.Fatalf("metadata_cache_no_cache = %#v, want %#v", got, tt.cacheNoCache)
			}
			if got := check.Details["metadata_referrer_no_referrer"]; got != tt.referrerValid {
				t.Fatalf("metadata_referrer_no_referrer = %#v, want %#v", got, tt.referrerValid)
			}
		})
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksBroadCookiePaths(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.CookiePaths = []string{"/"}

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["password_cookie_paths_scoped"]; got != false {
		t.Fatalf("password_cookie_paths_scoped = %#v, want false", got)
	}
	if got := check.Details["password_cookie_paths"]; got == nil {
		t.Fatal("password_cookie_paths detail missing")
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksUnboundedPasswordAttempts(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.PasswordAttemptCapacity = 0

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["password_rate_limit_enabled"]; got != false {
		t.Fatalf("password_rate_limit_enabled = %#v, want false", got)
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksUnboundedGlobalPasswordAttempts(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.PasswordGlobalAttemptCapacity = 0

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["password_rate_limit_enabled"]; got != false {
		t.Fatalf("password_rate_limit_enabled = %#v, want false", got)
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksMissingPasswordBcryptGate(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.PasswordBcryptConcurrency = 0

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["password_rate_limit_enabled"]; got != false {
		t.Fatalf("password_rate_limit_enabled = %#v, want false", got)
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksInvalidDownloadTicketBinderCookie(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.DownloadTicketCookiePrefix = ""

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["download_ticket_binder_cookie_valid"]; got != false {
		t.Fatalf("download_ticket_binder_cookie_valid = %#v, want false", got)
	}
}

func TestSecurityPublicShareBoundaryCheck_BlocksMissingArchiveConcurrencyGate(t *testing.T) {
	cfg := config.Default()
	cfg.Share.Enabled = true
	policy := share.PublicShareAccessPolicySnapshot()
	policy.PublicArchiveConcurrency = 0

	check := securityPublicShareBoundaryCheckWithPolicy(cfg, "https", policy)

	if check.Status != securityCheckBlock {
		t.Fatalf("status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["public_archive_concurrency"]; got != 0 {
		t.Fatalf("public_archive_concurrency = %#v, want 0", got)
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
	if check := securityCheckByID(t, payload.Data.Checks, "admin_accounts"); check.Status != securityCheckWarning {
		t.Fatalf("admin_accounts status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	} else if check.Title != "管理员账号检查不可用" {
		t.Fatalf("admin_accounts title = %q, want unavailable title", check.Title)
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

func TestServer_GetSettingsSecurityCheck_WarnsForLoopbackUnsafeNoAuthOverride(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.Enabled = false
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.Security.AllowUnsafeNoAuth = true
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "none"
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

	check := securityCheckByID(t, payload.Data.Checks, "unsafe_no_auth_override")
	if check.Status != securityCheckWarning {
		t.Fatalf("unsafe_no_auth_override status = %q, want %q; check=%#v", check.Status, securityCheckWarning, check)
	}
	if strings.Contains(check.Message, "非本机地址") {
		t.Fatalf("unsafe_no_auth_override warning message = %q, should not imply non-loopback exposure", check.Message)
	}
	if !strings.Contains(check.Message, "公网访问前请关闭该例外") {
		t.Fatalf("unsafe_no_auth_override warning message = %q, want public-access guidance", check.Message)
	}
	if got := check.Details["auth_enabled"]; got != false {
		t.Fatalf("auth_enabled detail = %#v, want false", got)
	}
	if got := check.Details["webdav_auth_type"]; got != "none" {
		t.Fatalf("webdav_auth_type detail = %#v, want none", got)
	}
}

func TestSecurityConfigFileAccessCheck_ClassifiesPathKinds(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		path       string
		setup      func(t *testing.T, path string)
		wantStatus securityCheckStatus
		wantKind   string
	}{
		{
			name: "empty path",
			path: "",
			setup: func(t *testing.T, path string) {
				t.Helper()
			},
			wantStatus: securityCheckWarning,
		},
		{
			name: "missing file",
			path: filepath.Join(tmpDir, "missing", "config.toml"),
			setup: func(t *testing.T, path string) {
				t.Helper()
			},
			wantStatus: securityCheckWarning,
			wantKind:   "missing",
		},
		{
			name: "private regular file",
			path: filepath.Join(tmpDir, "private", "config.toml"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("mkdir config dir: %v", err)
				}
				if err := os.WriteFile(path, []byte("[server]\nport = 8080\n"), 0o600); err != nil {
					t.Fatalf("write config file: %v", err)
				}
			},
			wantStatus: securityCheckPass,
			wantKind:   "regular",
		},
		{
			name: "broad regular file",
			path: filepath.Join(tmpDir, "broad", "config.toml"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("mkdir config dir: %v", err)
				}
				if err := os.WriteFile(path, []byte("[server]\nport = 8080\n"), 0o644); err != nil {
					t.Fatalf("write config file: %v", err)
				}
			},
			wantStatus: securityCheckWarning,
			wantKind:   "regular",
		},
		{
			name: "symlink file",
			path: filepath.Join(tmpDir, "symlink", "config.toml"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("mkdir config dir: %v", err)
				}
				targetPath := filepath.Join(tmpDir, "symlink-target.toml")
				if err := os.WriteFile(targetPath, []byte("[server]\nport = 8080\n"), 0o600); err != nil {
					t.Fatalf("write target config file: %v", err)
				}
				if err := os.Symlink(targetPath, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink",
		},
		{
			name: "parent symlink component",
			path: filepath.Join(tmpDir, "linked-config", "config.toml"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				realDir := filepath.Join(tmpDir, "real-config")
				if err := os.MkdirAll(realDir, 0o700); err != nil {
					t.Fatalf("mkdir real config dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(realDir, "config.toml"), []byte("[server]\nport = 8080\n"), 0o600); err != nil {
					t.Fatalf("write real config file: %v", err)
				}
				if err := os.Symlink(realDir, filepath.Dir(path)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink_component",
		},
		{
			name: "not regular",
			path: filepath.Join(tmpDir, "directory-config"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatalf("mkdir config path: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "not_regular",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t, tt.path)
			check := securityConfigFileAccessCheck(tt.path)
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

func TestSecurityConfigFileAccessCheckRedactsErrorDetail(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "token=config-secret", "config.toml") + "\x00"

	check := securityConfigFileAccessCheck(configPath)

	detail, ok := check.Details["error"].(string)
	if !ok || detail == "" {
		t.Fatalf("error detail = %#v, want non-empty string; check=%#v", check.Details["error"], check)
	}
	if strings.Contains(detail, "config-secret") {
		t.Fatalf("error detail = %q, leaked config token", detail)
	}
	if !strings.Contains(detail, "token=<redacted>") {
		t.Fatalf("error detail = %q, want redacted token path segment", detail)
	}
}

func TestSecuritySecretsFileAccessCheck_ClassifiesGeneratedWebDAVPathKinds(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		dataRoot   string
		mutate     func(*config.Config)
		setup      func(t *testing.T, path string)
		wantStatus securityCheckStatus
		wantKind   string
	}{
		{
			name:     "not required",
			dataRoot: filepath.Join(tmpDir, "not-required"),
			mutate: func(cfg *config.Config) {
				cfg.WebDAV.Password = "StrongWebDAVPassword123!"
			},
			setup: func(t *testing.T, path string) {
				t.Helper()
			},
			wantStatus: securityCheckPass,
		},
		{
			name:     "missing file",
			dataRoot: filepath.Join(tmpDir, "missing"),
			setup: func(t *testing.T, path string) {
				t.Helper()
			},
			wantStatus: securityCheckBlock,
			wantKind:   "missing",
		},
		{
			name:     "private regular file",
			dataRoot: filepath.Join(tmpDir, "private"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := config.SaveSecrets(filepath.Dir(path), &config.Secrets{JWTSecret: "jwt", WebDAVPassword: "GeneratedWebDAVPassword123!"}); err != nil {
					t.Fatalf("save secrets: %v", err)
				}
			},
			wantStatus: securityCheckPass,
			wantKind:   "regular",
		},
		{
			name:     "broad regular file",
			dataRoot: filepath.Join(tmpDir, "broad"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("mkdir secrets dir: %v", err)
				}
				if err := os.WriteFile(path, []byte(`{"jwt_secret":"jwt","webdav_password":"GeneratedWebDAVPassword123!"}`), 0o644); err != nil {
					t.Fatalf("write secrets file: %v", err)
				}
			},
			wantStatus: securityCheckWarning,
			wantKind:   "regular",
		},
		{
			name:     "symlink file",
			dataRoot: filepath.Join(tmpDir, "symlink"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("mkdir secrets dir: %v", err)
				}
				targetPath := filepath.Join(tmpDir, "symlink-secrets-target.json")
				if err := os.WriteFile(targetPath, []byte(`{"jwt_secret":"jwt","webdav_password":"GeneratedWebDAVPassword123!"}`), 0o600); err != nil {
					t.Fatalf("write target secrets file: %v", err)
				}
				if err := os.Symlink(targetPath, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink",
		},
		{
			name:     "parent symlink component",
			dataRoot: filepath.Join(tmpDir, "linked-secrets"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				realDir := filepath.Join(tmpDir, "real-secrets")
				if err := os.MkdirAll(realDir, 0o700); err != nil {
					t.Fatalf("mkdir real secrets dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(realDir, config.SecretsFile), []byte(`{"jwt_secret":"jwt","webdav_password":"GeneratedWebDAVPassword123!"}`), 0o600); err != nil {
					t.Fatalf("write real secrets file: %v", err)
				}
				if err := os.Symlink(realDir, filepath.Dir(path)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink_component",
		},
		{
			name:     "not regular",
			dataRoot: filepath.Join(tmpDir, "directory-secrets"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatalf("mkdir secrets path: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "not_regular",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretsPath := filepath.Join(tt.dataRoot, config.SecretsFile)
			tt.setup(t, secretsPath)
			cfg := config.Default()
			cfg.Storage.Root = tt.dataRoot
			cfg.WebDAV.Enabled = true
			cfg.WebDAV.AuthType = "basic"
			cfg.WebDAV.Password = ""
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			check := securitySecretsFileAccessCheck(cfg, config.NormalizeWebDAVAuthType(cfg.WebDAV.AuthType))
			if check.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; check=%#v", check.Status, tt.wantStatus, check)
			}
			if tt.wantKind != "" {
				if got := check.Details["path_kind"]; got != tt.wantKind {
					t.Fatalf("path_kind = %#v, want %q", got, tt.wantKind)
				}
			}
			if _, ok := check.Details["webdav_password"]; ok {
				t.Fatalf("secrets_file_access details must not expose password: %#v", check.Details)
			}
		})
	}
}

func TestSecurityBackupLocalDestinationsCheck_ClassifiesTargets(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "storage")
	sourceRoot := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(storageRoot, 0o700); err != nil {
		t.Fatalf("mkdir storage root: %v", err)
	}
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatalf("mkdir source root: %v", err)
	}

	tests := []struct {
		name       string
		setup      func(t *testing.T) config.BackupJobConfig
		wantStatus securityCheckStatus
		wantKind   string
	}{
		{
			name: "private directory",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(tmpDir, "backup-private")
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatalf("mkdir backup destination: %v", err)
				}
				return config.BackupJobConfig{ID: "local-private", Name: "Local Private", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckPass,
		},
		{
			name: "missing directory",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				return config.BackupJobConfig{ID: "local-missing", Name: "Local Missing", Type: "local", Source: sourceRoot, Destination: filepath.Join(tmpDir, "backup-missing")}
			},
			wantStatus: securityCheckWarning,
			wantKind:   "missing",
		},
		{
			name: "inside storage root",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(storageRoot, "backups")
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatalf("mkdir storage backup destination: %v", err)
				}
				return config.BackupJobConfig{ID: "local-storage", Name: "Local Storage", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "inside_storage_root",
		},
		{
			name: "inside default storage source",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(storageRoot, "default-source-backups")
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatalf("mkdir default source backup destination: %v", err)
				}
				return config.BackupJobConfig{ID: "local-default-source", Name: "Local Default Source", Type: "local", Destination: destination}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "inside_storage_root",
		},
		{
			name: "inside source",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(sourceRoot, "backups")
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatalf("mkdir source backup destination: %v", err)
				}
				return config.BackupJobConfig{ID: "local-source", Name: "Local Source", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "inside_source",
		},
		{
			name: "symlink directory",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				target := filepath.Join(tmpDir, "backup-symlink-target")
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatalf("mkdir symlink backup target: %v", err)
				}
				destination := filepath.Join(tmpDir, "backup-symlink")
				if err := os.Symlink(target, destination); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				return config.BackupJobConfig{ID: "local-symlink", Name: "Local Symlink", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink",
		},
		{
			name: "parent symlink component",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				target := filepath.Join(tmpDir, "backup-component-target")
				destination := filepath.Join(target, "backup")
				if err := os.MkdirAll(destination, 0o700); err != nil {
					t.Fatalf("mkdir symlink component target: %v", err)
				}
				linked := filepath.Join(tmpDir, "backup-component-link")
				if err := os.Symlink(target, linked); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				return config.BackupJobConfig{ID: "local-component", Name: "Local Component", Type: "local", Source: sourceRoot, Destination: filepath.Join(linked, "backup")}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink_component",
		},
		{
			name: "not directory",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(tmpDir, "backup-file")
				if err := os.WriteFile(destination, []byte("not a directory"), 0o600); err != nil {
					t.Fatalf("write backup file path: %v", err)
				}
				return config.BackupJobConfig{ID: "local-file", Name: "Local File", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "not_directory",
		},
		{
			name: "not writable",
			setup: func(t *testing.T) config.BackupJobConfig {
				t.Helper()
				destination := filepath.Join(tmpDir, "backup-readonly")
				if err := os.MkdirAll(destination, 0o555); err != nil {
					t.Fatalf("mkdir read-only backup destination: %v", err)
				}
				return config.BackupJobConfig{ID: "local-readonly", Name: "Local Readonly", Type: "local", Source: sourceRoot, Destination: destination}
			},
			wantStatus: securityCheckWarning,
			wantKind:   "not_writable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Storage.Root = storageRoot
			cfg.Backup.Jobs = []config.BackupJobConfig{tt.setup(t)}
			check := securityBackupLocalDestinationsCheck(cfg)
			if check.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; check=%#v", check.Status, tt.wantStatus, check)
			}
			if tt.wantKind != "" {
				if got := check.Details["destination_kind"]; got != tt.wantKind {
					t.Fatalf("destination_kind = %#v, want %q", got, tt.wantKind)
				}
			}
		})
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
		{
			name: "parent symlink component",
			setup: func(t *testing.T, path string) {
				parent := filepath.Dir(path)
				target := parent + "-target"
				if err := os.Rename(parent, target); err != nil {
					t.Fatalf("rename initial password parent: %v", err)
				}
				if err := os.Symlink(target, parent); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantStatus: securityCheckBlock,
			wantKind:   "symlink_component",
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
			name:    "escaped query marker path",
			baseURL: "https://nas.example.test/shares%3Ftoken",
		},
		{
			name:    "escaped fragment marker path",
			baseURL: "https://nas.example.test/shares%23section",
		},
		{
			name:    "duplicate path slashes",
			baseURL: "https://nas.example.test/shares//team",
		},
		{
			name:    "dot segment path",
			baseURL: "https://nas.example.test/shares/./team",
		},
		{
			name:    "escaped dot segment path",
			baseURL: "https://nas.example.test/shares/%2e%2e/team",
		},
		{
			name:    "backslash path",
			baseURL: `https://nas.example.test/shares\team`,
		},
		{
			name:    "escaped backslash path",
			baseURL: "https://nas.example.test/shares%5Cteam",
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

func TestServer_GetSettingsSecurityCheck_BlocksShareBaseURLDuplicatePathSlashes(t *testing.T) {
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
	cfg.Share.BaseURL = "https://nas.example.test/shares%2F%2Fteam"
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
	if check.Status != securityCheckBlock {
		t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "分享基础 URL 路径包含重复斜杠" {
		t.Fatalf("share_base_url title = %q, want duplicate slash warning", check.Title)
	}
	if got := check.Details["base_url_path"]; got != "/shares//team" {
		t.Fatalf("base_url_path = %#v, want /shares//team", got)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksShareBaseURLHostRelativeBackslashPath(t *testing.T) {
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
	cfg.Share.BaseURL = `https://nas.example.test\shares`
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
	if check.Status != securityCheckBlock {
		t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "分享基础 URL 路径包含反斜杠" {
		t.Fatalf("share_base_url title = %q, want backslash warning", check.Title)
	}
	if got := check.Details["base_url_path"]; got != `\shares` {
		t.Fatalf("base_url_path = %#v, want \\shares", got)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksShareBaseURLDotSegments(t *testing.T) {
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
	cfg.Share.BaseURL = "https://nas.example.test/shares/%2e%2e/team"
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
	if check.Status != securityCheckBlock {
		t.Fatalf("share_base_url status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if check.Title != "分享基础 URL 路径包含点段" {
		t.Fatalf("share_base_url title = %q, want dot segment warning", check.Title)
	}
	if got := check.Details["base_url_path"]; got != "/shares/../team" {
		t.Fatalf("base_url_path = %#v, want /shares/../team", got)
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
		{
			name:    "escaped base path share route",
			baseURL: "https://nas.example.test/base%2Fs/",
			path:    "/base/s/",
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
			name:           "unicode control character",
			prefix:         "/dav\u0081files",
			wantNormalized: "/dav\u0081files",
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
	secretsCheck := securityCheckByID(t, payload.Data.Checks, "secrets_file_access")
	if secretsCheck.Status != securityCheckBlock {
		t.Fatalf("secrets_file_access status = %q, want %q; check=%#v", secretsCheck.Status, securityCheckBlock, secretsCheck)
	}
	if got := secretsCheck.Details["path_kind"]; got != "missing" {
		t.Fatalf("secrets_file_access path_kind = %#v, want missing", got)
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
	configPath := filepath.Join(tmpDir, "config", "config.toml")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("save config: %v", err)
	}

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
		ConfigPath:     configPath,
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
	} else if got := check.Details["active_admins"]; got != float64(2) && got != 2 {
		t.Fatalf("admin_accounts active_admins = %#v, want 2", got)
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
		if got := check.Details["credential_check_limit"]; got != float64(12) && got != 12 {
			t.Fatalf("login_rate_limit credential_check_limit = %#v, want 12", got)
		}
		if got := check.Details["credential_check_scope"]; got != "client_ip" {
			t.Fatalf("login_rate_limit credential_check_scope = %#v, want client_ip", got)
		}
		if _, ok := check.Details["password"]; ok {
			t.Fatalf("login_rate_limit details must not expose passwords: %#v", check.Details)
		}
	}
	if check := securityCheckByID(t, payload.Data.Checks, "browser_session_boundary"); check.Status != securityCheckPass {
		t.Fatalf("browser_session_boundary status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	} else {
		if got := check.Details["session_cookie_host_prefix"]; got != true {
			t.Fatalf("session_cookie_host_prefix = %#v, want true", got)
		}
		if got := check.Details["session_cookie_name_prefix"]; got != "__Host-" {
			t.Fatalf("session_cookie_name_prefix = %#v, want __Host-", got)
		}
		if got := check.Details["session_cookie_path"]; got != "/" {
			t.Fatalf("session_cookie_path = %#v, want /", got)
		}
	}
	if check := securityCheckByID(t, payload.Data.Checks, "public_share_boundary"); check.Status != securityCheckPass {
		t.Fatalf("public_share_boundary status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	} else if got := check.Details["share_enabled"]; got != false {
		t.Fatalf("public_share_boundary share_enabled = %#v, want false", got)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "share_default_policy"); check.Status != securityCheckPass {
		t.Fatalf("share_default_policy status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "backup_local_destinations"); check.Status != securityCheckPass {
		t.Fatalf("backup_local_destinations status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "config_file_access"); check.Status != securityCheckPass {
		t.Fatalf("config_file_access status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "users_file_access"); check.Status != securityCheckPass {
		t.Fatalf("users_file_access status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "webdav_prefix"); check.Status != securityCheckPass {
		t.Fatalf("webdav_prefix status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "secrets_file_access"); check.Status != securityCheckPass {
		t.Fatalf("secrets_file_access status = %q, want %q; check=%#v", check.Status, securityCheckPass, check)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "smb_preview"); check.Status != securityCheckPass {
		t.Fatalf("smb_preview status = %q, want %q", check.Status, securityCheckPass)
	}
	if check := securityCheckByID(t, payload.Data.Checks, "initial_password_file"); check.Status != securityCheckPass {
		t.Fatalf("initial_password_file status = %q, want %q", check.Status, securityCheckPass)
	} else {
		wantPath := filepath.Join(filepath.Dir(cfg.Auth.UsersFile), "initial-password.txt")
		if got := check.Details["path"]; got != wantPath {
			t.Fatalf("initial_password_file path = %#v, want %q", got, wantPath)
		}
		if got := check.Details["path_kind"]; got != "missing" {
			t.Fatalf("initial_password_file path_kind = %#v, want missing", got)
		}
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
	if got := check.Details["session_cookie_host_prefix"]; got != false {
		t.Fatalf("session_cookie_host_prefix = %#v, want false", got)
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
			wantTitle:  "新分享默认不会过期且下载次数不限制",
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
			wantTitle:  "新分享默认下载次数不限制",
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

func TestServer_GetSettingsSecurityCheck_BlocksSymlinkUsersFileParentComponent(t *testing.T) {
	tmpDir := t.TempDir()
	authRoot := filepath.Join(tmpDir, "auth-root")
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Auth.UsersFile = filepath.Join(authRoot, "users", "users.json")
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
		AuthJWTSecret:  "security-check-users-file-parent-symlink-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	symlinkTarget := filepath.Join(tmpDir, "auth-target")
	if err := os.Rename(authRoot, symlinkTarget); err != nil {
		t.Fatalf("rename auth root: %v", err)
	}
	if err := os.Symlink(symlinkTarget, authRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
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
	if got := check.Details["dir_kind"]; got != "symlink_component" {
		t.Fatalf("dir_kind = %#v, want symlink_component", got)
	}
	if got := check.Details["symlink_component"]; got != authRoot {
		t.Fatalf("symlink_component = %#v, want %q", got, authRoot)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksSymlinkConfigFileParentComponent(t *testing.T) {
	tmpDir := t.TempDir()
	configRoot := filepath.Join(tmpDir, "config-root")
	configPath := filepath.Join(configRoot, "config.toml")
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
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("save config: %v", err)
	}

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
		AuthJWTSecret:  "security-check-config-file-parent-symlink-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
		ConfigPath:     configPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	symlinkTarget := filepath.Join(tmpDir, "config-target")
	if err := os.Rename(configRoot, symlinkTarget); err != nil {
		t.Fatalf("rename config root: %v", err)
	}
	if err := os.Symlink(symlinkTarget, configRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
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
	check := securityCheckByID(t, payload.Data.Checks, "config_file_access")
	if check.Status != securityCheckBlock {
		t.Fatalf("config_file_access status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["path"]; got != configPath {
		t.Fatalf("path = %#v, want %q", got, configPath)
	}
	if got := check.Details["path_kind"]; got != "symlink_component" {
		t.Fatalf("path_kind = %#v, want symlink_component", got)
	}
	if got := check.Details["symlink_component"]; got != configRoot {
		t.Fatalf("symlink_component = %#v, want %q", got, configRoot)
	}
}

func TestServer_GetSettingsSecurityCheck_BlocksSymlinkSecretsFileParentComponent(t *testing.T) {
	tmpDir := t.TempDir()
	storageRoot := filepath.Join(tmpDir, "storage-root")
	cfg := config.Default()
	cfg.Storage.Root = storageRoot
	cfg.Auth.UsersFile = filepath.Join(tmpDir, ".mnemonas", "users.json")
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TrustedProxyHops = 1
	cfg.DataPlane.GRPCAddress = "127.0.0.1:9090"
	cfg.WebDAV.Enabled = true
	cfg.WebDAV.AuthType = "basic"
	cfg.WebDAV.Password = ""
	cfg.Share.Enabled = false
	t.Setenv("DATAPLANE_HTTP_ADDR", "127.0.0.1:9091")
	saveSecurityCheckGeneratedWebDAVSecret(t, storageRoot)

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
		AuthJWTSecret:  "security-check-secrets-file-parent-symlink-secret",
		AuthAccessTTL:  15 * time.Minute,
		AuthRefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	token := loginAndGetAccessToken(t, server, "admin", password)
	symlinkTarget := filepath.Join(tmpDir, "storage-target")
	if err := os.Rename(storageRoot, symlinkTarget); err != nil {
		t.Fatalf("rename storage root: %v", err)
	}
	if err := os.Symlink(symlinkTarget, storageRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
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
	check := securityCheckByID(t, payload.Data.Checks, "secrets_file_access")
	if check.Status != securityCheckBlock {
		t.Fatalf("secrets_file_access status = %q, want %q; check=%#v", check.Status, securityCheckBlock, check)
	}
	if got := check.Details["path"]; got != filepath.Join(storageRoot, config.SecretsFile) {
		t.Fatalf("path = %#v, want %q", got, filepath.Join(storageRoot, config.SecretsFile))
	}
	if got := check.Details["path_kind"]; got != "symlink_component" {
		t.Fatalf("path_kind = %#v, want symlink_component", got)
	}
	if got := check.Details["symlink_component"]; got != storageRoot {
		t.Fatalf("symlink_component = %#v, want %q", got, storageRoot)
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
	} else if got := check.Details["active_admins"]; got != float64(1) && got != 1 {
		t.Fatalf("admin_accounts active_admins = %#v, want 1", got)
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
