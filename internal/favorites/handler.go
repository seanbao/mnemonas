package favorites

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/auth"
)

// Handler handles favorites HTTP requests
type Handler struct {
	store  *Store
	logger zerolog.Logger
}

type responseEnvelope struct {
	Success bool         `json:"success"`
	Data    interface{}  `json:"data,omitempty"`
	Warning bool         `json:"warning,omitempty"`
	Message string       `json:"message,omitempty"`
	Error   *errorDetail `json:"error,omitempty"`
}

type errorDetail struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

const defaultJSONRequestBodyLimit = 1 * 1024 * 1024

const favoritesPersistenceWarningHeader = `199 MnemoNAS "favorites persistence incomplete"`

// NewHandler creates a new favorites handler
func NewHandler(store *Store, logger zerolog.Logger) *Handler {
	return &Handler{
		store:  store,
		logger: logger,
	}
}

// getUserID extracts user ID from request context
// This should be set by auth middleware
func getUserID(r *http.Request) string {
	if claims := auth.GetClaimsFromContext(r.Context()); claims != nil && claims.UserID != "" {
		return claims.UserID
	}
	// Fallback for when auth is disabled
	return "anonymous"
}

func getLegacyUserIdentifiers(r *http.Request) []string {
	claims := auth.GetClaimsFromContext(r.Context())
	if claims == nil {
		return nil
	}
	if strings.TrimSpace(claims.Username) == "" || claims.Username == claims.UserID {
		return nil
	}
	return []string{claims.Username}
}

func normalizeFavoritePath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if hasFavoriteTraversalSegment(normalized) {
		return "", errors.New("invalid path")
	}
	return path.Clean("/" + normalized), nil
}

func hasFavoriteTraversalSegment(filePath string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func decodeJSONBodyStrict(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, defaultJSONRequestBodyLimit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > defaultJSONRequestBodyLimit {
		return &http.MaxBytesError{Limit: defaultJSONRequestBodyLimit}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing data")
		}
		return err
	}

	return nil
}

func (h *Handler) writeJSONBodyError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		h.error(w, http.StatusRequestEntityTooLarge, "request body too large", "PAYLOAD_TOO_LARGE")
		return
	}

	h.error(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
}

// ListFavorites handles GET /api/v1/favorites
func (h *Handler) ListFavorites(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	favorites := h.store.List(userID, getLegacyUserIdentifiers(r)...)

	h.success(w, http.StatusOK, map[string]any{
		"favorites": favorites,
		"count":     len(favorites),
	}, "")
}

// AddFavorite handles POST /api/v1/favorites
func (h *Handler) AddFavorite(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	var req struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}

	if err := decodeJSONBodyStrict(r, &req); err != nil {
		h.writeJSONBodyError(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	// Validate path
	cleanPath, err := normalizeFavoritePath(req.Path)
	if err != nil {
		h.error(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}
	if cleanPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	fav, err := h.store.Add(userID, cleanPath, req.Note, getLegacyUserIdentifiers(r)...)
	if err != nil {
		if err == ErrAlreadyFavorited {
			h.error(w, http.StatusConflict, "already favorited", "ALREADY_FAVORITED")
			return
		}
		if IsPersistenceWarning(err) {
			h.logger.Warn().Err(err).Str("path", cleanPath).Msg("Add favorite completed with persistence warning")
			h.successWithWarning(w, http.StatusCreated, fav, "favorite added with persistence warning")
			return
		}
		h.logger.Error().Err(err).Str("path", cleanPath).Msg("Failed to add favorite")
		h.error(w, http.StatusInternalServerError, "internal server error", "ADD_FAVORITE_FAILED")
		return
	}

	h.success(w, http.StatusCreated, fav, "")
}

// RemoveFavorite handles DELETE /api/v1/favorites/*
func (h *Handler) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	// Extract path from URL
	favPath := strings.TrimPrefix(r.URL.Path, "/api/v1/favorites")
	trimmedPath := strings.TrimSpace(favPath)
	if trimmedPath == "" || trimmedPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	cleanPath, err := normalizeFavoritePath(favPath)
	if err != nil {
		h.error(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	if err := h.store.Remove(userID, cleanPath, getLegacyUserIdentifiers(r)...); err != nil {
		if err == ErrFavoriteNotFound {
			h.error(w, http.StatusNotFound, "favorite not found", "FAVORITE_NOT_FOUND")
			return
		}
		if IsPersistenceWarning(err) {
			h.logger.Warn().Err(err).Str("path", cleanPath).Msg("Remove favorite completed with persistence warning")
			h.successWithWarning(w, http.StatusOK, nil, "favorite removed with persistence warning")
			return
		}
		h.logger.Error().Err(err).Str("path", cleanPath).Msg("Failed to remove favorite")
		h.error(w, http.StatusInternalServerError, "internal server error", "REMOVE_FAVORITE_FAILED")
		return
	}

	h.success(w, http.StatusOK, nil, "favorite removed successfully")
}

// CheckFavorite handles GET /api/v1/favorites/check?path=...
func (h *Handler) CheckFavorite(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	checkPath := r.URL.Query().Get("path")

	if strings.TrimSpace(checkPath) == "" {
		h.error(w, http.StatusBadRequest, "path query parameter is required", "MISSING_PATH")
		return
	}

	cleanPath, err := normalizeFavoritePath(checkPath)
	if err != nil {
		h.error(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}
	if cleanPath == "/" {
		h.error(w, http.StatusBadRequest, "path query parameter is required", "MISSING_PATH")
		return
	}
	isFavorite := h.store.IsFavorite(userID, cleanPath, getLegacyUserIdentifiers(r)...)

	h.success(w, http.StatusOK, map[string]any{
		"path":        cleanPath,
		"is_favorite": isFavorite,
	}, "")
}

// CheckFavorites handles POST /api/v1/favorites/check-batch
// Body: {"paths": ["/path1", "/path2"]}
func (h *Handler) CheckFavorites(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	var req struct {
		Paths []string `json:"paths"`
	}

	if err := decodeJSONBodyStrict(r, &req); err != nil {
		h.writeJSONBodyError(w, err)
		return
	}

	// Clean paths
	cleanPaths := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		if strings.TrimSpace(p) == "" {
			h.error(w, http.StatusBadRequest, "paths must not contain empty values", "MISSING_PATH")
			return
		}
		cleanPath, err := normalizeFavoritePath(p)
		if err != nil {
			h.error(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
			return
		}
		cleanPaths[i] = cleanPath
		if cleanPaths[i] == "/" {
			h.error(w, http.StatusBadRequest, "paths must not contain empty values", "MISSING_PATH")
			return
		}
	}

	result := h.store.CheckPaths(userID, cleanPaths, getLegacyUserIdentifiers(r)...)

	h.success(w, http.StatusOK, map[string]any{
		"favorites": result,
	}, "")
}

// UpdateNote handles PATCH /api/v1/favorites/*
func (h *Handler) UpdateNote(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	// Extract path from URL
	favPath := strings.TrimPrefix(r.URL.Path, "/api/v1/favorites")
	trimmedPath := strings.TrimSpace(favPath)
	if trimmedPath == "" || trimmedPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	cleanPath, err := normalizeFavoritePath(favPath)
	if err != nil {
		h.error(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
		return
	}

	var req struct {
		Note string `json:"note"`
	}

	if err := decodeJSONBodyStrict(r, &req); err != nil {
		h.writeJSONBodyError(w, err)
		return
	}

	if err := h.store.UpdateNote(userID, cleanPath, req.Note, getLegacyUserIdentifiers(r)...); err != nil {
		if err == ErrFavoriteNotFound {
			h.error(w, http.StatusNotFound, "favorite not found", "FAVORITE_NOT_FOUND")
			return
		}
		if IsPersistenceWarning(err) {
			h.logger.Warn().Err(err).Str("path", cleanPath).Msg("Update favorite note completed with persistence warning")
			h.successWithWarning(w, http.StatusOK, nil, "favorite note updated with persistence warning")
			return
		}
		h.logger.Error().Err(err).Str("path", cleanPath).Msg("Failed to update note")
		h.error(w, http.StatusInternalServerError, "internal server error", "UPDATE_NOTE_FAILED")
		return
	}

	h.success(w, http.StatusOK, nil, "favorite note updated successfully")
}

func (h *Handler) json(w http.ResponseWriter, status int, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func (h *Handler) success(w http.ResponseWriter, status int, data any, message string) {
	h.writeSuccess(w, status, data, message, false)
}

func (h *Handler) successWithWarning(w http.ResponseWriter, status int, data any, message string) {
	markFavoritesPersistenceWarningHeaders(w)
	h.writeSuccess(w, status, data, message, true)
}

func markFavoritesPersistenceWarningHeaders(w http.ResponseWriter) {
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == favoritesPersistenceWarningHeader {
			return
		}
	}
	headers.Add("Warning", favoritesPersistenceWarningHeader)
}

func (h *Handler) writeSuccess(w http.ResponseWriter, status int, data any, message string, warning bool) {
	if data == nil {
		data = json.RawMessage("null")
	}
	h.json(w, status, responseEnvelope{
		Success: true,
		Data:    data,
		Warning: warning,
		Message: message,
	})
}

func (h *Handler) error(w http.ResponseWriter, status int, message, code string) {
	h.json(w, status, responseEnvelope{
		Success: false,
		Error: &errorDetail{
			Code:    code,
			Message: message,
		},
	})
}
