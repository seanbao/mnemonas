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

func TestHandler_AddFavorite_EmptyPathReturnsBadRequest(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	tests := []string{"", "   ", "/", "."}
	for _, favoritePath := range tests {
		t.Run(favoritePath, func(t *testing.T) {
			body := strings.NewReader(`{"path":"` + favoritePath + `"}`)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", body)
			req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
			rec := httptest.NewRecorder()

			handler.AddFavorite(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
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
			if payload.Success || payload.Error == nil {
				t.Fatalf("expected wrapped validation error, got %s", rec.Body.String())
			}
			if payload.Error.Code != "MISSING_PATH" {
				t.Fatalf("expected MISSING_PATH error, got %s", payload.Error.Code)
			}
		})
	}
}

func TestHandler_InternalErrorsHideOperationDetails(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	store.filePath = t.TempDir()
	handler := NewHandler(store, zerolog.Nop())

	assertInternal := func(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
		t.Helper()
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
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
		if payload.Success || payload.Error == nil {
			t.Fatalf("expected wrapped internal error, got %s", rec.Body.String())
		}
		if payload.Error.Code != wantCode {
			t.Fatalf("expected error code %s, got %s", wantCode, payload.Error.Code)
		}
		if payload.Error.Message != "internal server error" {
			t.Fatalf("expected generic internal message, got %q", payload.Error.Message)
		}
	}

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/new.pdf"}`))
	addReq = addReq.WithContext(auth.WithClaimsContext(addReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	addRec := httptest.NewRecorder()
	handler.AddFavorite(addRec, addReq)
	assertInternal(t, addRec, "ADD_FAVORITE_FAILED")

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/docs/report.pdf", nil)
	removeReq = removeReq.WithContext(auth.WithClaimsContext(removeReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	removeRec := httptest.NewRecorder()
	handler.RemoveFavorite(removeRec, removeReq)
	assertInternal(t, removeRec, "REMOVE_FAVORITE_FAILED")

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/docs/report.pdf", strings.NewReader(`{"note":"updated"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	updateRec := httptest.NewRecorder()
	handler.UpdateNote(updateRec, updateReq)
	assertInternal(t, updateRec, "UPDATE_NOTE_FAILED")
}

func TestHandler_CheckFavorite_TrimsSurroundingWhitespace(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=%20/docs/report.pdf%20", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.CheckFavorite(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Path       string `json:"path"`
			IsFavorite bool   `json:"is_favorite"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success || !payload.Data.IsFavorite {
		t.Fatalf("expected trimmed path to match favorite, got %s", rec.Body.String())
	}
	if payload.Data.Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized path, got %q", payload.Data.Path)
	}
}

func TestHandler_CheckFavorite_WhitespaceOnlyPathReturnsBadRequest(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=%20%20%20", nil)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.CheckFavorite(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "MISSING_PATH" {
		t.Fatalf("expected MISSING_PATH error, got %s", rec.Body.String())
	}
}

func TestHandler_CheckFavorites_TrimsSurroundingWhitespace(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":[" /docs/report.pdf ","/docs/missing.pdf"]}`))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.CheckFavorites(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Favorites map[string]bool `json:"favorites"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %s", rec.Body.String())
	}
	if !payload.Data.Favorites["/docs/report.pdf"] {
		t.Fatalf("expected trimmed favorite path to match, got %#v", payload.Data.Favorites)
	}
	if payload.Data.Favorites["/docs/missing.pdf"] {
		t.Fatalf("expected missing path to remain false, got %#v", payload.Data.Favorites)
	}
}

func TestHandler_CheckFavorites_RejectsWhitespaceOnlyPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/docs/report.pdf","   "]}`))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.CheckFavorites(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "MISSING_PATH" {
		t.Fatalf("expected MISSING_PATH error, got %s", rec.Body.String())
	}
}
