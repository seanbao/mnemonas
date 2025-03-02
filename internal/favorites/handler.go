package favorites

import (
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
	Message string       `json:"message,omitempty"`
	Error   *errorDetail `json:"error,omitempty"`
}

type errorDetail struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

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

func normalizeFavoritePath(rawPath string) string {
	return path.Clean("/" + strings.TrimSpace(rawPath))
}

func decodeJSONBodyStrict(r *http.Request, dst any) error {
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

// ListFavorites handles GET /api/v1/favorites
func (h *Handler) ListFavorites(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	favorites := h.store.List(userID)

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
		h.error(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	// Validate path
	cleanPath := normalizeFavoritePath(req.Path)
	if cleanPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	fav, err := h.store.Add(userID, cleanPath, req.Note)
	if err != nil {
		if err == ErrAlreadyFavorited {
			h.error(w, http.StatusConflict, "already favorited", "ALREADY_FAVORITED")
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
	if favPath == "" || favPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	cleanPath := normalizeFavoritePath(favPath)

	if err := h.store.Remove(userID, cleanPath); err != nil {
		if err == ErrFavoriteNotFound {
			h.error(w, http.StatusNotFound, "favorite not found", "FAVORITE_NOT_FOUND")
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

	if checkPath == "" {
		h.error(w, http.StatusBadRequest, "path query parameter is required", "MISSING_PATH")
		return
	}

	cleanPath := normalizeFavoritePath(checkPath)
	if cleanPath == "/" {
		h.error(w, http.StatusBadRequest, "path query parameter is required", "MISSING_PATH")
		return
	}
	isFavorite := h.store.IsFavorite(userID, cleanPath)

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
		h.error(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	// Clean paths
	cleanPaths := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		cleanPaths[i] = normalizeFavoritePath(p)
		if cleanPaths[i] == "/" {
			h.error(w, http.StatusBadRequest, "paths must not contain empty values", "MISSING_PATH")
			return
		}
	}

	result := h.store.CheckPaths(userID, cleanPaths)

	h.success(w, http.StatusOK, map[string]any{
		"favorites": result,
	}, "")
}

// UpdateNote handles PATCH /api/v1/favorites/*
func (h *Handler) UpdateNote(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	// Extract path from URL
	favPath := strings.TrimPrefix(r.URL.Path, "/api/v1/favorites")
	if favPath == "" || favPath == "/" {
		h.error(w, http.StatusBadRequest, "path is required", "MISSING_PATH")
		return
	}

	cleanPath := normalizeFavoritePath(favPath)

	var req struct {
		Note string `json:"note"`
	}

	if err := decodeJSONBodyStrict(r, &req); err != nil {
		h.error(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if err := h.store.UpdateNote(userID, cleanPath, req.Note); err != nil {
		if err == ErrFavoriteNotFound {
			h.error(w, http.StatusNotFound, "favorite not found", "FAVORITE_NOT_FOUND")
			return
		}
		h.logger.Error().Err(err).Str("path", cleanPath).Msg("Failed to update note")
		h.error(w, http.StatusInternalServerError, "internal server error", "UPDATE_NOTE_FAILED")
		return
	}

	h.success(w, http.StatusOK, nil, "favorite note updated successfully")
}

func (h *Handler) json(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) success(w http.ResponseWriter, status int, data any, message string) {
	h.json(w, status, responseEnvelope{
		Success: true,
		Data:    data,
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
