package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/config"
)

func expectedRESTRouteContracts() []string {
	return []string{
		"GET /api/v1/activity/",
		"GET /api/v1/activity/reviews",
		"PATCH /api/v1/activity/reviews/{id}",
		"POST /api/v1/activity/reviews",
		"GET /api/v1/activity/stats",
		"DELETE /api/v1/activity/",
		"GET /api/v1/admin/users/",
		"POST /api/v1/admin/users/",
		"PUT /api/v1/admin/users/{id}",
		"DELETE /api/v1/admin/users/{id}",
		"POST /api/v1/admin/users/{id}/reset-password",
		"POST /api/v1/admin/users/{id}/revoke-sessions",
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
		"POST /api/v1/maintenance/backups",
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
		"POST /api/v1/settings/access-check",
		"POST /api/v1/settings/access-preview",
		"POST /api/v1/settings/access-report",
		"DELETE /api/v1/settings/access-reviews",
		"GET /api/v1/settings/access-reviews",
		"POST /api/v1/settings/access-reviews",
		"POST /api/v1/settings/alerts/test",
		"GET /api/v1/settings/security-check",
		"PUT /api/v1/settings/",
		"GET /api/v1/settings/webdav-credentials",
		"GET /api/v1/setup/",
		"GET /api/v1/setup/readiness",
		"POST /api/v1/setup/acknowledge",
		"POST /api/v1/setup/defer",
		"GET /api/v1/shares/",
		"GET /api/v1/shares/policy",
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
		"HEAD /health",
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

func TestServer_RouteContract_AllRoutesAreDocumented(t *testing.T) {
	assertAPIRoutesDocumented(t, expectedRESTRouteContracts())
}

func TestServer_RouteContract_ShareMutationFieldsAreDocumented(t *testing.T) {
	createFields := []string{"path", "type", "password", "expires_in", "permission", "max_access", "description"}
	updateFields := []string{"enabled", "password", "expires_in", "permission", "max_access", "description"}

	for _, docPath := range []string{
		filepath.Join("..", "..", "docs", "api-reference.md"),
		filepath.Join("..", "..", "docs", "api-reference.en.md"),
	} {
		t.Run(filepath.Base(docPath), func(t *testing.T) {
			data, err := os.ReadFile(docPath)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			doc := string(data)
			createSection := apiReferenceSectionBetweenAny(t, doc,
				[]string{"POST /api/v1/shares", "Create request:", "创建请求："},
				[]string{"GET /api/v1/shares", "### 列出分享", "Update request:", "Public endpoints:"},
			)
			assertAPIDocFields(t, createSection, createFields)
			updateSection := apiReferenceSectionBetweenAny(t, doc,
				[]string{"PUT /api/v1/shares/{id}", "Update request:", "更新请求："},
				[]string{"DELETE /api/v1/shares/{id}", "### 删除分享", "Public endpoints:"},
			)
			assertAPIDocFields(t, updateSection, updateFields)
		})
	}
}

func TestServer_RouteContract_FavoriteActionResponsesDocumentNullData(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"## 收藏"},
			endMarkers:   []string{"## 活动日志"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"## Favorites"},
			endMarkers:   []string{"## Activity Log"},
		},
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			if count := strings.Count(section, `"data": null`); count < 2 {
				t.Fatalf("favorite action response docs should include null data for remove and note update, got %d examples", count)
			}
		})
	}
}

func TestServer_RouteContract_SearchResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"## 搜索"},
			endMarkers:   []string{"## 分享链接"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"## Search"},
			endMarkers:   []string{"## Share Links"},
		},
	}
	fields := []string{"q", "limit", "query", "results", "count", "name", "path", "isDir", "size", "modTime"}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
		})
	}
}

func TestServer_RouteContract_ActivityResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"## 活动日志"},
			endMarkers:   []string{"## 设置"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"## Activity Log"},
			endMarkers:   []string{"## Settings"},
		},
	}
	fields := []string{
		"limit", "offset", "action", "user",
		"items", "id", "timestamp", "path", "ip", "details", "total",
		"today", "by_action", "by_user", "message",
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
		})
	}
}

func TestServer_RouteContract_AdminUserResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"## 管理员用户端点"},
			endMarkers:   []string{"## 系统端点"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"## Admin User Endpoints"},
			endMarkers:   []string{"## System Endpoints"},
		},
	}
	fields := []string{
		"id", "username", "email", "role", "groups", "disabled", "home_dir",
		"created_at", "updated_at", "last_login_at", "quota_bytes", "used_bytes",
		"user", "users", "total", "quota_history_available", "quota_history",
		"captured_at", "total_count", "active_count", "limited_count",
		"warning_count", "exceeded_count", "attention_count", "limited_used_bytes",
		"revoked", "data",
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
			if count := strings.Count(section, `"data": null`); count < 2 {
				t.Fatalf("admin user action docs should include null data for delete and password reset, got %d examples", count)
			}
		})
	}
}

func TestServer_RouteContract_AuthResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"## 认证端点"},
			endMarkers:   []string{"## 管理员用户端点"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"## Auth Endpoints"},
			endMarkers:   []string{"## Admin User Endpoints"},
		},
	}
	fields := []string{
		"username", "password", "refresh_token", "access_token", "expires_at", "token_type",
		"user", "id", "email", "role", "groups", "home_dir", "expected_user_id", "old_password", "new_password", "data",
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
			if count := strings.Count(section, `"data": null`); count < 3 {
				t.Fatalf("auth action docs should include null data for logout, download-session, and password change, got %d examples", count)
			}
		})
	}
}

func TestServer_RouteContract_SettingsAccessResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"Access-check 响应："},
			endMarkers:   []string{"### 公网访问安全自检"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"`POST /api/v1/settings/access-check`"},
			endMarkers:   []string{"### Public-Access Security Self-Check"},
		},
	}
	fields := []string{
		"username", "user_id", "role", "groups", "home_dir", "path",
		"read", "write", "mode", "allowed", "source", "message", "matched_rule",
		"summary", "users", "read_allowed", "read_denied", "write_allowed", "write_denied",
		"related_shares", "active_related_shares", "password_protected_shares",
		"rule_effects", "index", "user_samples",
		"shares", "id", "type", "created_by", "relation", "enabled", "active",
		"has_password", "access_count", "max_access", "url", "preview", "directory_access_rules",
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
		})
	}
}

func TestServer_RouteContract_SettingsWebDAVCredentialsFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"WebDAV 凭据响应："},
			endMarkers:   []string{"### 公网访问安全自检"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"WebDAV credentials response:"},
			endMarkers:   []string{"### Public-Access Security Self-Check"},
		},
	}
	fields := []string{"enabled", "url", "auth_type", "username", "password"}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
		})
	}
}

func TestServer_RouteContract_APIReferenceMarkdownFencesAreBalanced(t *testing.T) {
	docs := []string{
		filepath.Join("..", "..", "docs", "api-reference.md"),
		filepath.Join("..", "..", "docs", "api-reference.en.md"),
	}

	for _, docPath := range docs {
		t.Run(filepath.Base(docPath), func(t *testing.T) {
			data, err := os.ReadFile(docPath)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}

			inFence := false
			fenceStartLine := 0
			for lineNumber, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(line, "```") {
					continue
				}
				if inFence {
					inFence = false
					continue
				}
				inFence = true
				fenceStartLine = lineNumber + 1
			}
			if inFence {
				t.Fatalf("unclosed markdown code fence starting at line %d", fenceStartLine)
			}
		})
	}
}

func TestServer_RouteContract_BackupBatchRestoreResponseFieldsAreDocumented(t *testing.T) {
	docs := []struct {
		path         string
		startMarkers []string
		endMarkers   []string
	}{
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.md"),
			startMarkers: []string{"批量预览响应："},
			endMarkers:   []string{"维护端点面向管理员"},
		},
		{
			path:         filepath.Join("..", "..", "docs", "api-reference.en.md"),
			startMarkers: []string{"/api/v1/maintenance/backups/batch-restore-preview"},
			endMarkers:   []string{"`POST /restore` supports"},
		},
	}
	fields := []string{
		"items", "index", "job_id", "target_path", "include_config", "status",
		"preview", "restore", "verify", "total_files", "total_bytes", "verified_bytes",
		"warning", "warnings", "error_message",
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			section := apiReferenceSectionBetweenAny(t, string(data), doc.startMarkers, doc.endMarkers)
			assertAPIDocFields(t, section, fields)
		})
	}
}

func TestServer_RouteContract_BackupRestorePreflightTargetStateIsDocumented(t *testing.T) {
	docs := []struct {
		path     string
		expected []string
	}{
		{
			path: filepath.Join("..", "..", "docs", "api-reference.md"),
			expected: []string{
				"`target_state`",
				"目标目录尚不存在",
				"目标目录已存在且为空",
				"`status = \"warning\"`",
				"`status = \"failed\"`",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "api-reference.en.md"),
			expected: []string{
				"`target_state`",
				"target directory does not exist",
				"target directory already exists and is empty",
				"`status = \"warning\"`",
				"`status = \"failed\"`",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "backup-guide.md"),
			expected: []string{
				"`target_state`",
				"目标目录尚不存在",
				"目标目录已存在且为空",
				"`status = \"warning\"`",
				"`status = \"failed\"`",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "backup-guide.en.md"),
			expected: []string{
				"`target_state`",
				"target directory does not exist",
				"target directory already exists and is empty",
				"`status = \"warning\"`",
				"`status = \"failed\"`",
			},
		},
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read backup restore documentation: %v", err)
			}
			for _, expected := range doc.expected {
				if !strings.Contains(string(data), expected) {
					t.Errorf("backup restore preflight documentation does not contain %q", expected)
				}
			}
		})
	}
}

func TestServer_RouteContract_BackupRestoreStagingBehaviorIsDocumented(t *testing.T) {
	docs := []struct {
		path     string
		expected []string
	}{
		{
			path: filepath.Join("..", "..", "docs", "api-reference.md"),
			expected: []string{
				"`rclone copy <remote> <临时目录>`",
				"`rclone check <remote> <临时目录> --one-way`",
				"再把临时目录安装到 `target_path`",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "api-reference.en.md"),
			expected: []string{
				"`rclone copy <remote> <staging>`",
				"`rclone check <remote> <staging> --one-way`",
				"installs the staging directory into the target path",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "backup-guide.md"),
			expected: []string{
				"`rclone copy <remote> <临时目录>`",
				"`rclone check <remote> <临时目录> --one-way`",
				"再把临时目录安装到 `target_path`",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "backup-guide.en.md"),
			expected: []string{
				"`rclone copy <remote> <staging>`",
				"`rclone check <remote> <staging> --one-way`",
				"installs the staging directory into the target path",
			},
		},
	}

	for _, doc := range docs {
		t.Run(filepath.Base(doc.path), func(t *testing.T) {
			data, err := os.ReadFile(doc.path)
			if err != nil {
				t.Fatalf("failed to read backup restore documentation: %v", err)
			}
			for _, expected := range doc.expected {
				if !strings.Contains(string(data), expected) {
					t.Errorf("backup restore documentation does not contain %q", expected)
				}
			}
		})
	}
}

func apiReferenceSectionBetweenAny(t *testing.T, doc string, startMarkers, endMarkers []string) string {
	t.Helper()
	start, startMarker := apiReferenceFirstMarker(doc, startMarkers, 0)
	if start < 0 {
		t.Fatalf("API reference does not contain any of %v", startMarkers)
	}
	end, _ := apiReferenceFirstMarker(doc, endMarkers, start+len(startMarker))
	if end < 0 {
		return doc[start:]
	}
	return doc[start:end]
}

func apiReferenceFirstMarker(doc string, markers []string, offset int) (int, string) {
	firstIndex := -1
	firstMarker := ""
	for _, marker := range markers {
		index := strings.Index(doc[offset:], marker)
		if index < 0 {
			continue
		}
		index += offset
		if firstIndex < 0 || index < firstIndex {
			firstIndex = index
			firstMarker = marker
		}
	}
	return firstIndex, firstMarker
}

func assertAPIDocFields(t *testing.T, section string, fields []string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(section, `"`+field+`"`) && !strings.Contains(section, "`"+field+"`") {
			t.Errorf("API reference section does not document %q", field)
		}
	}
}

func assertAPIRoutesDocumented(t *testing.T, routes []string) {
	t.Helper()
	for _, docPath := range []string{
		filepath.Join("..", "..", "docs", "api-reference.md"),
		filepath.Join("..", "..", "docs", "api-reference.en.md"),
	} {
		t.Run(filepath.Base(docPath), func(t *testing.T) {
			data, err := os.ReadFile(docPath)
			if err != nil {
				t.Fatalf("failed to read API reference: %v", err)
			}
			doc := string(data)
			for _, route := range routes {
				if !apiReferenceContainsRoute(doc, route) {
					t.Errorf("API reference does not document %s", route)
				}
			}
		})
	}
}

func apiReferenceContainsRoute(doc, route string) bool {
	method, path, ok := strings.Cut(route, " ")
	if !ok {
		return false
	}
	for _, candidate := range apiReferencePathCandidates(path) {
		if apiReferenceContainsPlainRoute(doc, method, candidate) ||
			strings.Contains(doc, "`"+method+"` | `"+candidate+"`") ||
			strings.Contains(doc, "`"+method+"` | `"+candidate+"?") {
			return true
		}
	}
	return false
}

func apiReferenceContainsPlainRoute(doc, method, path string) bool {
	needle := method + " " + path
	offset := 0
	for {
		index := strings.Index(doc[offset:], needle)
		if index < 0 {
			return false
		}
		end := offset + index + len(needle)
		if end == len(doc) || apiReferenceRouteBoundary(doc[end]) {
			return true
		}
		offset = end
	}
}

func apiReferenceRouteBoundary(char byte) bool {
	switch char {
	case '\n', '\r', '\t', ' ', '`', '|', '?', '<':
		return true
	default:
		return false
	}
}

func apiReferencePathCandidates(path string) []string {
	candidates := make([]string, 0)
	queue := []string{path}
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		candidate := queue[0]
		queue = queue[1:]
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
		if strings.HasSuffix(candidate, "/") && candidate != "/" {
			queue = append(queue, strings.TrimSuffix(candidate, "/"))
		}
		if strings.HasSuffix(candidate, "/*") {
			queue = append(queue, strings.TrimSuffix(candidate, "/*")+"/{path}")
		}
		if apiReferencePublicShareRoute(candidate) {
			queue = append(queue, strings.ReplaceAll(candidate, "{id}", "{share_id}"))
		}
	}
	return candidates
}

func apiReferencePublicShareRoute(path string) bool {
	return strings.Contains(path, "/api/v1/public/shares/{id}") ||
		strings.HasPrefix(path, "/s/{id}")
}
