package api

import (
	"encoding/json"
	"fmt"
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

type routeSmokeSession struct {
	accessToken  string
	refreshToken string
	password     string
	userID       string
}

func TestServer_RouteContract_SmokeRequestsDoNot500(t *testing.T) {
	server, session := newRouteSmokeServer(t)

	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, session)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if routeSmokeNeedsBearer(contract) {
				token := session.accessToken
				if contract == "POST /api/v1/auth/logout" {
					token = loginRouteSmokeAdmin(t, server, session.password).accessToken
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("%s returned 500: %s", contract, rec.Body.String())
			}
			if rec.Code >= http.StatusInternalServerError && rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s returned unexpected 5xx status %d: %s", contract, rec.Code, rec.Body.String())
			}
			if rec.Body.Len() > 0 {
				contentType := rec.Header().Get("Content-Type")
				if !strings.HasPrefix(contentType, "application/json") {
					t.Fatalf("%s returned non-JSON response Content-Type %q with status %d and body %q", contract, contentType, rec.Code, rec.Body.String())
				}
				if !json.Valid(rec.Body.Bytes()) {
					t.Fatalf("%s returned invalid JSON with status %d: %q", contract, rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestServer_RouteContract_PublicRoutesDoNotRequireAuth(t *testing.T) {
	server, session := newRouteSmokeServer(t)

	checked := 0
	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		if routeSmokeRequiresAuth(contract) {
			continue
		}
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, session)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("%s without auth status = %d, want public route not to require auth; body=%s", contract, rec.Code, rec.Body.String())
			}
		})
		checked++
	}
	if checked < 15 {
		t.Fatalf("public route contract checked %d routes, want at least 15", checked)
	}
}

func TestServer_RouteContract_AdminOnlyRoutesRejectNonAdmin(t *testing.T) {
	server, adminSession := newRouteSmokeServer(t)

	const username = "route-user"
	const password = "route-smoke-user-password"
	if _, err := server.userStore.Create(username, password, "", auth.RoleUser); err != nil {
		t.Fatalf("creating route smoke user: %v", err)
	}
	userSession := loginRouteSmokeUser(t, server, username, password)

	checked := 0
	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		if !routeSmokeRequiresAdmin(contract) {
			continue
		}
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, adminSession)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Bearer "+userSession.accessToken)

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s as non-admin status = %d, want %d; body=%s", contract, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if got := routeSmokeErrorCode(t, rec); got != "INSUFFICIENT_PERMISSIONS" {
				t.Fatalf("%s as non-admin error code = %q, want %q; body=%s", contract, got, "INSUFFICIENT_PERMISSIONS", rec.Body.String())
			}
		})
		checked++
	}
	if checked < 30 {
		t.Fatalf("admin-only route contract checked %d routes, want at least 30", checked)
	}
}

func TestServer_RouteContract_ProtectedRoutesRejectAnonymous(t *testing.T) {
	server, session := newRouteSmokeServer(t)

	checked := 0
	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		if !routeSmokeRequiresAuth(contract) {
			continue
		}
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, session)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s without auth status = %d, want %d; body=%s", contract, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			if got := routeSmokeErrorCode(t, rec); got != "MISSING_AUTH_HEADER" {
				t.Fatalf("%s without auth error code = %q, want %q; body=%s", contract, got, "MISSING_AUTH_HEADER", rec.Body.String())
			}
		})
		checked++
	}
	if checked < 70 {
		t.Fatalf("authenticated route contract checked %d routes, want at least 70", checked)
	}
}

func TestServer_RouteContract_ProtectedRoutesRejectInvalidAuthorizationHeader(t *testing.T) {
	server, session := newRouteSmokeServer(t)

	checked := 0
	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		if !routeSmokeRequiresAuth(contract) {
			continue
		}
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, session)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Basic route-smoke")

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s with invalid auth header status = %d, want %d; body=%s", contract, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			if got := routeSmokeErrorCode(t, rec); got != "INVALID_AUTH_HEADER" {
				t.Fatalf("%s with invalid auth header error code = %q, want %q; body=%s", contract, got, "INVALID_AUTH_HEADER", rec.Body.String())
			}
		})
		checked++
	}
	if checked < 70 {
		t.Fatalf("invalid auth header route contract checked %d routes, want at least 70", checked)
	}
}

func TestServer_RouteContract_WriteRoutesRejectGuest(t *testing.T) {
	server, adminSession := newRouteSmokeServer(t)

	const username = "route-guest"
	const password = "route-smoke-guest-password"
	if _, err := server.userStore.Create(username, password, "", auth.RoleGuest); err != nil {
		t.Fatalf("creating route smoke guest: %v", err)
	}
	guestSession := loginRouteSmokeUser(t, server, username, password)

	checked := 0
	for _, contract := range expectedRESTRouteContracts() {
		contract := contract
		if !routeSmokeRequiresWriteRole(contract) {
			continue
		}
		t.Run(contract, func(t *testing.T) {
			method, routePattern, ok := strings.Cut(contract, " ")
			if !ok {
				t.Fatalf("invalid route contract %q", contract)
			}

			body := routeSmokeRequestBody(contract, adminSession)
			req := httptest.NewRequest(method, routeSmokePath(routePattern), strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Bearer "+guestSession.accessToken)

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s as guest status = %d, want %d; body=%s", contract, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if got := routeSmokeErrorCode(t, rec); got != "INSUFFICIENT_PERMISSIONS" {
				t.Fatalf("%s as guest error code = %q, want %q; body=%s", contract, got, "INSUFFICIENT_PERMISSIONS", rec.Body.String())
			}
		})
		checked++
	}
	if checked < 10 {
		t.Fatalf("write route contract checked %d routes, want at least 10", checked)
	}
}

func newRouteSmokeServer(t *testing.T) (*Server, routeSmokeSession) {
	t.Helper()

	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	internalRoot := filepath.Join(dataRoot, ".mnemonas")
	if err := os.MkdirAll(internalRoot, 0o700); err != nil {
		t.Fatalf("creating route smoke directories: %v", err)
	}
	if _, _, err := config.LoadOrCreateSecrets(dataRoot); err != nil {
		t.Fatalf("creating route smoke secrets: %v", err)
	}

	cfg := config.Default()
	cfg.Storage.Root = dataRoot
	cfg.WebDAV.Enabled = false
	cfg.Auth.Enabled = true
	cfg.Auth.UsersFile = filepath.Join(internalRoot, "users.json")
	cfg.Share.Enabled = true
	cfg.Share.StoreFile = filepath.Join(internalRoot, "shares.json")
	cfg.Favorites.Enabled = true
	cfg.Favorites.StoreFile = filepath.Join(internalRoot, "favorites.json")

	_, password, err := auth.NewUserStore(cfg.Auth.UsersFile)
	if err != nil {
		t.Fatalf("creating route smoke user store: %v", err)
	}
	if strings.TrimSpace(password) == "" {
		t.Fatal("route smoke user store did not create an initial admin password")
	}

	configPath := filepath.Join(root, "config.toml")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("saving route smoke config: %v", err)
	}

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:             cfg,
		ConfigPath:         configPath,
		AuthEnabled:        true,
		AuthUsersFile:      cfg.Auth.UsersFile,
		AuthJWTSecret:      "route-smoke-secret",
		AuthAccessTTL:      15 * time.Minute,
		AuthRefreshTTL:     24 * time.Hour,
		ShareEnabled:       true,
		ShareStoreFile:     cfg.Share.StoreFile,
		FavoritesEnabled:   true,
		FavoritesStoreFile: cfg.Favorites.StoreFile,
		MaintenanceRoot:    filepath.Join(internalRoot, "maintenance"),
		ActivityRoot:       filepath.Join(internalRoot, "activity"),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	admin, err := server.userStore.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	if err := server.userStore.ResetOwnPassword(admin.ID, password); err != nil {
		t.Fatalf("ResetOwnPassword(admin) error: %v", err)
	}

	session := loginRouteSmokeAdmin(t, server, password)
	session.password = password
	return server, session
}

func loginRouteSmokeAdmin(t *testing.T, server *Server, password string) routeSmokeSession {
	t.Helper()

	return loginRouteSmokeUser(t, server, "admin", password)
}

func loginRouteSmokeUser(t *testing.T, server *Server, username, password string) routeSmokeSession {
	t.Helper()

	reqBody := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("route smoke login status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var envelope struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			User         struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode route smoke login response: %v", err)
	}
	if envelope.Data.AccessToken == "" || envelope.Data.RefreshToken == "" || envelope.Data.User.ID == "" {
		t.Fatalf("route smoke login did not return access token, refresh token, and user id: %s", rec.Body.String())
	}

	return routeSmokeSession{
		accessToken:  envelope.Data.AccessToken,
		refreshToken: envelope.Data.RefreshToken,
		userID:       envelope.Data.User.ID,
	}
}

func routeSmokePath(routePattern string) string {
	path := strings.ReplaceAll(routePattern, "{id}", "smoke-id")
	path = strings.ReplaceAll(path, "{hash}", "smoke-hash")
	path = strings.ReplaceAll(path, "*", "smoke.txt")
	if path == "/api/v1/favorites/check" {
		return path + "?path=/smoke.txt"
	}
	return path
}

func routeSmokeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode route smoke error response: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Error == nil {
		t.Fatalf("route smoke response missing error envelope: %s", rec.Body.String())
	}
	return envelope.Error.Code
}

func routeSmokeNeedsBearer(contract string) bool {
	if contract == "POST /api/v1/auth/logout" {
		return true
	}
	return routeSmokeRequiresAuth(contract)
}

func routeSmokeRequiresAuth(contract string) bool {
	_, routePattern, ok := strings.Cut(contract, " ")
	if !ok {
		return false
	}
	if contract == "POST /api/v1/auth/login" || contract == "POST /api/v1/auth/refresh" || contract == "POST /api/v1/auth/logout" {
		return false
	}
	if routePattern == "/health" || routePattern == "/api/v1/version" || routePattern == "/api/v1/setup/" {
		return false
	}
	if strings.HasPrefix(routePattern, "/s/") || strings.HasPrefix(routePattern, "/api/v1/public/shares/") {
		return false
	}
	return true
}

func routeSmokeRequiresAdmin(contract string) bool {
	method, routePattern, ok := strings.Cut(contract, " ")
	if !ok {
		return false
	}

	switch {
	case routePattern == "/api/v1/setup/readiness" || routePattern == "/api/v1/setup/acknowledge" || routePattern == "/api/v1/setup/defer":
		return true
	case strings.HasPrefix(routePattern, "/api/v1/admin/users"):
		return true
	case routePattern == "/api/v1/diagnostics" || routePattern == "/api/v1/diagnostics-export" || routePattern == "/api/v1/metrics":
		return true
	case method == http.MethodDelete && routePattern == "/api/v1/activity/":
		return true
	case strings.HasPrefix(routePattern, "/api/v1/activity/reviews"):
		return true
	case strings.HasPrefix(routePattern, "/api/v1/settings"):
		return true
	case strings.HasPrefix(routePattern, "/api/v1/maintenance"):
		return true
	case contract == "POST /api/v1/versions/{hash}/restore":
		return true
	default:
		return false
	}
}

func routeSmokeRequiresWriteRole(contract string) bool {
	method, routePattern, ok := strings.Cut(contract, " ")
	if !ok {
		return false
	}

	switch {
	case method == http.MethodPost && routePattern == "/api/v1/shares/":
		return true
	case (method == http.MethodPut || method == http.MethodDelete) && routePattern == "/api/v1/shares/{id}":
		return true
	case method == http.MethodPost && routePattern == "/api/v1/favorites/":
		return true
	case (method == http.MethodDelete || method == http.MethodPatch) && routePattern == "/api/v1/favorites/*":
		return true
	case (method == http.MethodPost || method == http.MethodDelete) && routePattern == "/api/v1/files/*":
		return true
	case method == http.MethodPost && (routePattern == "/api/v1/files-copy" || routePattern == "/api/v1/files-move"):
		return true
	case method == http.MethodPost && routePattern == "/api/v1/directories/*":
		return true
	case method == http.MethodDelete && routePattern == "/api/v1/trash/":
		return true
	case (method == http.MethodPost || method == http.MethodDelete) && routePattern == "/api/v1/trash/{id}":
		return true
	case method == http.MethodPost && routePattern == "/api/v1/trash/{id}/restore":
		return true
	default:
		return false
	}
}

func routeSmokeRequestBody(contract string, session routeSmokeSession) string {
	switch contract {
	case "POST /api/v1/auth/login":
		return fmt.Sprintf(`{"username":"admin","password":%q}`, session.password)
	case "POST /api/v1/auth/refresh":
		return fmt.Sprintf(`{"refresh_token":%q}`, session.refreshToken)
	case "POST /api/v1/auth/password":
		return fmt.Sprintf(`{"expected_user_id":%q,"old_password":"not-the-current-password","new_password":"route-smoke-password"}`, session.userID)
	case "POST /api/v1/admin/users/":
		return `{"username":"admin","password":"route-smoke-password","role":"admin"}`
	case "POST /api/v1/admin/users/{id}/reset-password":
		return `{"new_password":"route-smoke-password"}`
	case "PUT /api/v1/admin/users/{id}/status":
		return `{"disabled":false}`
	case "POST /api/v1/directories/*":
		return `{}`
	case "POST /api/v1/files/*":
		return `{}`
	case "POST /api/v1/files-copy", "POST /api/v1/files-move":
		return `{"from":"/missing.txt","to":"/target.txt"}`
	case "POST /api/v1/favorites/":
		return `{"path":"/smoke.txt"}`
	case "POST /api/v1/favorites/check-batch":
		return `{"paths":["/smoke.txt"]}`
	case "PATCH /api/v1/favorites/*":
		return `{"note":"route smoke"}`
	case "POST /api/v1/public/shares/{id}/access", "POST /s/{id}":
		return `{"password":"route-smoke"}`
	case "PUT /api/v1/settings/":
		return `{}`
	case "POST /api/v1/setup/defer":
		return `{"remind_in_days":7}`
	case "POST /api/v1/settings/access-check":
		return `{"username":"admin","path":"/"}`
	case "POST /api/v1/settings/access-preview":
		return `{"path":"/","directory_access_rules":[]}`
	case "POST /api/v1/settings/access-report":
		return `{"path":"/"}`
	case "POST /api/v1/shares/":
		return `{"path":"/missing.txt","type":"file"}`
	case "PUT /api/v1/shares/{id}":
		return `{"enabled":false}`
	default:
		if strings.HasPrefix(contract, "POST ") || strings.HasPrefix(contract, "PUT ") || strings.HasPrefix(contract, "PATCH ") {
			return `{}`
		}
		return ""
	}
}

func TestRouteSmokeRequestBodiesAreValidJSON(t *testing.T) {
	session := routeSmokeSession{
		accessToken:  "access",
		refreshToken: "refresh",
		password:     "password",
		userID:       "user-id",
	}
	for _, contract := range expectedRESTRouteContracts() {
		body := routeSmokeRequestBody(contract, session)
		if body == "" {
			continue
		}
		if !json.Valid([]byte(body)) {
			t.Fatalf("%s route smoke body is invalid JSON: %s", contract, body)
		}
	}
}
