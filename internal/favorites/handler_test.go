package favorites

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestHandler_JSON_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()

	handler.json(rec, http.StatusOK, map[string]any{
		"bad": make(chan int),
	})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if rec.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", rec.Body.String())
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

	tests := []string{"", "   ", "/"}
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

func TestHandler_AddFavorite_PreservesWhitespaceInPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/report.pdf ","note":"note"}`))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.AddFavorite(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !store.IsFavorite("user-123", "/docs/report.pdf ") {
		t.Fatal("expected favorite to preserve trailing whitespace in path")
	}
	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected trimmed sibling path to remain unfavorited")
	}
}

func TestHandler_AddFavorite_RejectsUnknownFields(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	body := strings.NewReader(`{"path":"/docs/report.pdf","unexpected":true}`)
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
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Success || payload.Error == nil || payload.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST error, got %s", rec.Body.String())
	}
	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected favorite creation to be rejected before persistence")
	}
}

func TestHandler_AddFavorite_RejectsOversizedRequestBody(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	body := bytes.NewReader(bytes.Repeat([]byte{'x'}, defaultJSONRequestBodyLimit+1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", body)
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.AddFavorite(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
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
	if payload.Success || payload.Error == nil || payload.Error.Code != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("expected PAYLOAD_TOO_LARGE error, got %s", rec.Body.String())
	}
	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected oversized favorite request to be rejected before persistence")
	}
}

func TestHandler_AddFavorite_RejectsTraversalPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	for _, favoritePath := range []string{"../docs/report.pdf", `..\\docs\\report.pdf`, "/docs/./report.pdf", "./docs/report.pdf", "."} {
		body := strings.NewReader(`{"path":"` + favoritePath + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", body)
		req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
		rec := httptest.NewRecorder()

		handler.AddFavorite(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q expected status 400, got %d: %s", favoritePath, rec.Code, rec.Body.String())
		}

		var payload struct {
			Success bool `json:"success"`
			Error   *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("path %q failed to decode response: %v", favoritePath, err)
		}
		if payload.Success || payload.Error == nil || payload.Error.Code != "INVALID_PATH" {
			t.Fatalf("path %q expected INVALID_PATH error, got %s", favoritePath, rec.Body.String())
		}
	}

	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected traversal-like favorite request to be rejected before persistence")
	}
}

func TestHandler_AddFavorite_RejectsNULPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/report\u0000.pdf"}`))
	req = req.WithContext(auth.WithClaimsContext(req.Context(), &auth.TokenClaims{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	handler.AddFavorite(rec, req)

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
	if payload.Success || payload.Error == nil || payload.Error.Code != "INVALID_PATH" {
		t.Fatalf("expected INVALID_PATH error, got %s", rec.Body.String())
	}
	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected NUL favorite request to be rejected before persistence")
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

func TestHandler_MutationPersistenceWarningsReturnSuccess(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	originalSyncFavoritesStoreRootDir := syncFavoritesStoreRootDir
	syncFavoritesStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncFavoritesStoreRootDir = originalSyncFavoritesStoreRootDir
	}()

	assertWarningSuccess := func(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantMessage string) {
		t.Helper()
		if rec.Code != wantStatus {
			t.Fatalf("expected status %d, got %d: %s", wantStatus, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Warning") != favoritesPersistenceWarningHeader {
			t.Fatalf("expected Warning header %q, got %q", favoritesPersistenceWarningHeader, rec.Header().Get("Warning"))
		}
		var payload struct {
			Success bool   `json:"success"`
			Warning bool   `json:"warning"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !payload.Success || !payload.Warning {
			t.Fatalf("expected warning success response, got %s", rec.Body.String())
		}
		if payload.Message != wantMessage {
			t.Fatalf("expected message %q, got %q", wantMessage, payload.Message)
		}
	}

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/new.pdf"}`))
	addReq = addReq.WithContext(auth.WithClaimsContext(addReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	addRec := httptest.NewRecorder()
	handler.AddFavorite(addRec, addReq)
	assertWarningSuccess(t, addRec, http.StatusCreated, "favorite added with persistence warning")
	if !store.IsFavorite("user-123", "/docs/new.pdf") {
		t.Fatal("expected warned add to persist in handler store")
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/docs/report.pdf", strings.NewReader(`{"note":"updated"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	updateRec := httptest.NewRecorder()
	handler.UpdateNote(updateRec, updateReq)
	assertWarningSuccess(t, updateRec, http.StatusOK, "favorite note updated with persistence warning")
	updated := false
	for _, favorite := range store.List("user-123") {
		if favorite.Path == "/docs/report.pdf" && favorite.Note == "updated" {
			updated = true
		}
	}
	if !updated {
		t.Fatal("expected warned update to persist in handler store")
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/docs/report.pdf", nil)
	removeReq = removeReq.WithContext(auth.WithClaimsContext(removeReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	removeRec := httptest.NewRecorder()
	handler.RemoveFavorite(removeRec, removeReq)
	assertWarningSuccess(t, removeRec, http.StatusOK, "favorite removed with persistence warning")
	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatal("expected warned remove to persist in handler store")
	}
}

func TestHandler_RemoveFavoriteAndUpdateNote_NormalizeRoutePath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/report.pdf","note":"first"}`))
	addReq = addReq.WithContext(auth.WithClaimsContext(addReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	addRec := httptest.NewRecorder()
	handler.AddFavorite(addRec, addReq)
	if addRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", addRec.Code, addRec.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/docs/report.pdf", strings.NewReader(`{"note":"updated"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	updateRec := httptest.NewRecorder()
	handler.UpdateNote(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	favorites := store.List("user-123")
	if len(favorites) != 1 {
		t.Fatalf("expected 1 favorite after update, got %d", len(favorites))
	}
	if favorites[0].Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized favorite path, got %q", favorites[0].Path)
	}
	if favorites[0].Note != "updated" {
		t.Fatalf("expected updated note, got %q", favorites[0].Note)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/docs/report.pdf", nil)
	removeReq = removeReq.WithContext(auth.WithClaimsContext(removeReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	removeRec := httptest.NewRecorder()
	handler.RemoveFavorite(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", removeRec.Code, removeRec.Body.String())
	}

	if store.IsFavorite("user-123", "/docs/report.pdf") {
		t.Fatalf("expected favorite to be removed")
	}
}

func TestHandler_RemoveFavoriteAndUpdateNote_DecodeEscapedRoutePath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	if _, err := store.Add("user-123", "/docs/my file.txt", "first"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/docs/my%20file.txt", strings.NewReader(`{"note":"updated"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	updateRec := httptest.NewRecorder()
	handler.UpdateNote(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	favorites := store.List("user-123")
	if len(favorites) != 1 {
		t.Fatalf("expected 1 favorite after update, got %d", len(favorites))
	}
	if favorites[0].Note != "updated" {
		t.Fatalf("expected decoded route update to change note, got %q", favorites[0].Note)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/docs/my%20file.txt", nil)
	removeReq = removeReq.WithContext(auth.WithClaimsContext(removeReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	removeRec := httptest.NewRecorder()
	handler.RemoveFavorite(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", removeRec.Code, removeRec.Body.String())
	}

	if store.IsFavorite("user-123", "/docs/my file.txt") {
		t.Fatal("expected decoded route favorite to be removed")
	}
}

func TestHandler_RemoveFavoriteAndUpdateNote_IncludeNullDataOnSuccess(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/favorites", strings.NewReader(`{"path":"/docs/report.pdf","note":"first"}`))
	addReq = addReq.WithContext(auth.WithClaimsContext(addReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	addRec := httptest.NewRecorder()
	handler.AddFavorite(addRec, addReq)
	if addRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", addRec.Code, addRec.Body.String())
	}

	assertNullData := func(t *testing.T, body []byte) {
		t.Helper()
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		data, ok := payload["data"]
		if !ok {
			t.Fatalf("expected success response to include data field, got %s", string(body))
		}
		if string(data) != "null" {
			t.Fatalf("expected success response data to be null, got %s", string(data))
		}
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/favorites/docs/report.pdf", strings.NewReader(`{"note":"updated"}`))
	updateReq = updateReq.WithContext(auth.WithClaimsContext(updateReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	updateRec := httptest.NewRecorder()
	handler.UpdateNote(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	assertNullData(t, updateRec.Body.Bytes())

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/docs/report.pdf", nil)
	removeReq = removeReq.WithContext(auth.WithClaimsContext(removeReq.Context(), &auth.TokenClaims{UserID: "user-123"}))
	removeRec := httptest.NewRecorder()
	handler.RemoveFavorite(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", removeRec.Code, removeRec.Body.String())
	}
	assertNullData(t, removeRec.Body.Bytes())
}

func TestHandler_CheckFavorite_PreservesWhitespaceInPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf ", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites/check?path=%2Fdocs%2Freport.pdf%20", nil)
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
		t.Fatalf("expected whitespace-preserving path to match favorite, got %s", rec.Body.String())
	}
	if payload.Data.Path != "/docs/report.pdf " {
		t.Fatalf("expected whitespace-preserving path, got %q", payload.Data.Path)
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

func TestHandler_CheckFavorites_PreservesWhitespaceInPaths(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Add("user-123", "/docs/report.pdf ", "note"); err != nil {
		t.Fatalf("seed favorite error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/docs/report.pdf ","/docs/report.pdf"]}`))
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
	if !payload.Data.Favorites["/docs/report.pdf "] {
		t.Fatalf("expected whitespace-preserving favorite path to match, got %#v", payload.Data.Favorites)
	}
	if payload.Data.Favorites["/docs/report.pdf"] {
		t.Fatalf("expected trimmed sibling path to remain false, got %#v", payload.Data.Favorites)
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

func TestHandler_CheckFavorites_RejectsUnknownFields(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	handler := NewHandler(store, zerolog.Nop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites/check-batch", strings.NewReader(`{"paths":["/docs/report.pdf"],"unexpected":true}`))
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
	if payload.Success || payload.Error == nil || payload.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST error, got %s", rec.Body.String())
	}
}
