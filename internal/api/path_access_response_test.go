package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
)

func TestServerSearchReturnsAccessDeniedForInvalidUserContext(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	server.authEnabled = true

	if err := fs.WriteFile(t.Context(), "/report.txt", bytes.NewReader([]byte("report"))); err != nil {
		t.Fatalf("WriteFile(/report.txt) error: %v", err)
	}

	tests := []struct {
		name string
		user *auth.User
	}{
		{name: "missing user context"},
		{
			name: "disabled user context",
			user: &auth.User{
				ID:       "u1",
				Username: "tester",
				Role:     auth.RoleUser,
				HomeDir:  "/tester",
				Disabled: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=report", nil)
			if tt.user != nil {
				req = req.WithContext(contextWithAPIUser(tt.user))
			}
			w := httptest.NewRecorder()

			server.handleSearch(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("search status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
			}
			assertPathAccessDeniedResponse(t, w.Body.String())
		})
	}
}

func TestServerEmptyTrashReturnsAccessDeniedForInvalidUserContext(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	server.authEnabled = true

	if err := fs.WriteFile(t.Context(), "/deleted.txt", bytes.NewReader([]byte("deleted"))); err != nil {
		t.Fatalf("WriteFile(/deleted.txt) error: %v", err)
	}
	if err := fs.Delete(t.Context(), "/deleted.txt"); err != nil {
		t.Fatalf("Delete(/deleted.txt) error: %v", err)
	}
	items, err := fs.ListTrash(t.Context())
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("trash item count = %d, want 1", len(items))
	}

	tests := []struct {
		name string
		user *auth.User
	}{
		{name: "missing user context"},
		{
			name: "disabled user context",
			user: &auth.User{
				ID:       "u1",
				Username: "tester",
				Role:     auth.RoleUser,
				HomeDir:  "/tester",
				Disabled: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"ids":["` + items[0].ID + `"]}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trash/empty", strings.NewReader(body))
			if tt.user != nil {
				req = req.WithContext(contextWithAPIUser(tt.user))
			}
			w := httptest.NewRecorder()

			server.handleEmptyTrash(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("empty trash status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
			}
			assertPathAccessDeniedResponse(t, w.Body.String())
		})
	}
}

func assertPathAccessDeniedResponse(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "path access denied by directory access rule") {
		t.Fatalf("expected directory access denial response, got %s", body)
	}
	if strings.Contains(body, "outside the assigned home directory") {
		t.Fatalf("expected access rule denial, got outside-home response: %s", body)
	}
}
