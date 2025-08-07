package api

import (
	"net/http"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/config"
)

func expectedRESTRouteContracts() []string {
	return []string{
		"GET /api/v1/activity/",
		"GET /api/v1/activity/stats",
		"DELETE /api/v1/activity/",
		"GET /api/v1/admin/users/",
		"POST /api/v1/admin/users/",
		"PUT /api/v1/admin/users/{id}",
		"DELETE /api/v1/admin/users/{id}",
		"POST /api/v1/admin/users/{id}/reset-password",
		"PUT /api/v1/admin/users/{id}/status",
		"POST /api/v1/auth/download-session",
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/logout",
		"GET /api/v1/auth/me",
		"POST /api/v1/auth/password",
		"POST /api/v1/auth/refresh",
		"GET /api/v1/diagnostics",
		"GET /api/v1/diagnostics-export",
		"POST /api/v1/directories/*",
		"GET /api/v1/download/*",
		"GET /api/v1/favorites/",
		"POST /api/v1/favorites/",
		"GET /api/v1/favorites/check",
		"POST /api/v1/favorites/check-batch",
		"DELETE /api/v1/favorites/*",
		"PATCH /api/v1/favorites/*",
		"GET /api/v1/files/*",
		"POST /api/v1/files/*",
		"DELETE /api/v1/files/*",
		"POST /api/v1/files-copy",
		"POST /api/v1/files-move",
		"GET /api/v1/maintenance/backups",
		"GET /api/v1/maintenance/backups/{id}",
		"GET /api/v1/maintenance/backups/{id}/restore-report",
		"POST /api/v1/maintenance/backups/batch-restore",
		"POST /api/v1/maintenance/backups/batch-restore-preview",
		"POST /api/v1/maintenance/backups/{id}/retention-check",
		"POST /api/v1/maintenance/backups/{id}/restore",
		"POST /api/v1/maintenance/backups/{id}/restore-drill",
		"POST /api/v1/maintenance/backups/{id}/restore-preview",
		"POST /api/v1/maintenance/backups/{id}/restore-verify",
		"POST /api/v1/maintenance/backups/{id}/run",
		"GET /api/v1/maintenance/disk-health",
		"GET /api/v1/maintenance/objects",
		"POST /api/v1/maintenance/gc",
		"GET /api/v1/maintenance/scrub",
		"POST /api/v1/maintenance/scrub",
		"GET /api/v1/metrics",
		"GET /api/v1/public/shares/{id}",
		"POST /api/v1/public/shares/{id}/access",
		"GET /api/v1/public/shares/{id}/download",
		"GET /api/v1/public/shares/{id}/download/*",
		"GET /api/v1/public/shares/{id}/items",
		"GET /api/v1/search",
		"GET /api/v1/settings/",
		"GET /api/v1/settings/security-check",
		"PUT /api/v1/settings/",
		"GET /api/v1/settings/webdav-credentials",
		"GET /api/v1/setup/",
		"POST /api/v1/setup/acknowledge",
		"GET /api/v1/shares/",
		"POST /api/v1/shares/",
		"GET /api/v1/shares/{id}",
		"PUT /api/v1/shares/{id}",
		"DELETE /api/v1/shares/{id}",
		"GET /api/v1/stats",
		"GET /api/v1/thumbnails/*",
		"GET /api/v1/trash/",
		"DELETE /api/v1/trash/",
		"GET /api/v1/trash/{id}",
		"DELETE /api/v1/trash/{id}",
		"POST /api/v1/trash/{id}/restore",
		"GET /api/v1/version",
		"GET /api/v1/versions/*",
		"POST /api/v1/versions/{hash}/restore",
		"GET /health",
		"GET /s/{id}",
		"POST /s/{id}",
		"GET /s/{id}/download",
		"GET /s/{id}/download/*",
		"GET /s/{id}/items",
	}
}

func TestServer_RouteContract_CoversAllRESTEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Root = tmpDir
	cfg.Share.Enabled = true
	cfg.Favorites.Enabled = true

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		Config:             cfg,
		ConfigPath:         filepath.Join(tmpDir, "config.toml"),
		AuthEnabled:        true,
		AuthUsersFile:      filepath.Join(tmpDir, "users.json"),
		AuthJWTSecret:      "route-contract-secret",
		AuthAccessTTL:      15 * time.Minute,
		AuthRefreshTTL:     24 * time.Hour,
		ShareEnabled:       true,
		ShareStoreFile:     filepath.Join(tmpDir, "shares.json"),
		FavoritesEnabled:   true,
		FavoritesStoreFile: filepath.Join(tmpDir, "favorites.json"),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	actual := make(map[string]struct{})
	if err := chi.Walk(server.router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		actual[method+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walking routes: %v", err)
	}

	expected := expectedRESTRouteContracts()

	expectedSet := make(map[string]struct{}, len(expected))
	for _, route := range expected {
		expectedSet[route] = struct{}{}
		if _, ok := actual[route]; !ok {
			t.Errorf("missing route %s", route)
		}
	}

	var unexpected []string
	for route := range actual {
		if _, ok := expectedSet[route]; !ok {
			unexpected = append(unexpected, route)
		}
	}
	sort.Strings(unexpected)
	for _, route := range unexpected {
		t.Errorf("unexpected route %s", route)
	}
}
