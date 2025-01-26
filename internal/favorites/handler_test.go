package favorites

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
)

func TestGetUserIDFromClaims(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	claims := &auth.TokenClaims{UserID: "user-123"}
	req = req.WithContext(auth.WithClaimsContext(req.Context(), claims))

	if got := getUserID(req); got != "user-123" {
		t.Fatalf("expected user-123, got %s", got)
	}
}

func TestGetUserIDAnonymous(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if got := getUserID(req); got != "anonymous" {
		t.Fatalf("expected anonymous, got %s", got)
	}
}

func TestHandler_ListFavorites_WrapsResponse(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.ListFavorites(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Favorites []Favorite `json:"favorites"`
			Count     int        `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", rec.Body.String())
	}
	if payload.Data.Count != 0 {
		t.Fatalf("expected empty favorites count, got %d", payload.Data.Count)
	}
}

func TestHandler_AddFavorite_WrapsConflictError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	body := strings.NewReader(`{"path":"/docs/report.pdf"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()
	handler.AddFavorite(rec, req)

	body = strings.NewReader(`{"path":"/docs/report.pdf"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/favorites", body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec = httptest.NewRecorder()
	handler.AddFavorite(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", rec.Code)
	}

	var payload struct {
		Success bool `json:"success"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "ALREADY_FAVORITED" {
		t.Fatalf("expected wrapped conflict error, got %s", rec.Body.String())
	}
}
