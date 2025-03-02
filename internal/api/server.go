// Package api provides REST API and gRPC API
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/maintenance"
	"github.com/seanbao/mnemonas/internal/metrics"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/thumbnail"
)

const maxObjectsCursorLength = 256

// Server is the API server
type Server struct {
	router      *chi.Mux
	logger      zerolog.Logger
	dataplane   *dataplane.Client
	fs          *storage.FileSystem
	thumbnail   *thumbnail.Service
	maintenance *maintenance.HistoryStore
	activity    *activity.Store
	startTime   time.Time
	// Auth components
	userStore          *auth.UserStore
	tokenManager       *auth.TokenManager
	authHandler        *auth.Handler
	authMw             *auth.Middleware
	authEnabled        bool
	initialWebPassword string // Set when admin user is first created
	// Share components
	shareStore   *share.ShareStore
	shareHandler *share.Handler
	// Favorites components
	favoritesStore   *favorites.Store
	favoritesHandler *favorites.Handler
	// Config
	config     *config.Config
	configPath string
}

// ServerConfig holds server configuration
type ServerConfig struct {
	DataplaneAddr string
	// New storage configuration
	FileSystem *storage.FileSystem
	// Storage service roots
	ThumbnailRoot   string
	MaintenanceRoot string
	ActivityRoot    string
	// Auth configuration
	AuthEnabled    bool
	AuthUsersFile  string
	AuthJWTSecret  string
	AuthAccessTTL  time.Duration
	AuthRefreshTTL time.Duration
	// Share configuration
	ShareEnabled   bool
	ShareStoreFile string
	ShareBaseURL   string
	// Favorites configuration
	FavoritesStoreFile string
	// Config (for settings API)
	Config     *config.Config
	ConfigPath string
}

// NewServer creates a new API server
func NewServer(logger zerolog.Logger, cfg *ServerConfig) (*Server, error) {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(metrics.MetricsMiddleware) // Collect request metrics
	r.Use(zerologMiddleware(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(DefaultRequestTimeout * time.Second))
	// REM-4 fix: Add rate limiting
	r.Use(middleware.Throttle(DefaultMaxConcurrentRequests))

	s := &Server{
		router:    r,
		logger:    logger,
		startTime: time.Now(),
	}

	// Initialize data plane client if address provided
	if cfg != nil && cfg.DataplaneAddr != "" {
		s.dataplane = dataplane.NewClient(cfg.DataplaneAddr)
		ctx, cancel := context.WithTimeout(context.Background(), DefaultDataplaneConnectTimeout*time.Second)
		defer cancel()
		if err := s.dataplane.Connect(ctx); err != nil {
			logger.Warn().Err(err).Msg("Failed to connect to data plane, will retry later")
		} else {
			logger.Info().Str("addr", cfg.DataplaneAddr).Msg("Connected to data plane")
		}
	}

	// Initialize filesystem (from pre-created instance)
	if cfg != nil && cfg.FileSystem != nil {
		s.fs = cfg.FileSystem
	}

	// Initialize thumbnail service
	if cfg != nil && cfg.ThumbnailRoot != "" {
		thumb, err := thumbnail.NewService(cfg.ThumbnailRoot)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize thumbnail service")
		} else {
			s.thumbnail = thumb
			logger.Info().Str("cache", cfg.ThumbnailRoot).Msg("Thumbnail service initialized")
		}
	}

	// Initialize maintenance history store
	if cfg != nil && cfg.MaintenanceRoot != "" {
		maint, err := maintenance.NewHistoryStore(cfg.MaintenanceRoot)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize maintenance history store")
		} else {
			s.maintenance = maint
			logger.Info().Str("path", cfg.MaintenanceRoot).Msg("Maintenance history store initialized")
		}
	}

	// Initialize activity log store
	if cfg != nil && cfg.ActivityRoot != "" {
		actStore, err := activity.NewStore(cfg.ActivityRoot)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize activity store")
		} else {
			s.activity = actStore
			logger.Info().Str("path", cfg.ActivityRoot).Msg("Activity store initialized")
		}
	}

	// Initialize auth if enabled
	if cfg != nil && cfg.AuthEnabled {
		s.authEnabled = true

		// Initialize user store
		userStore, initialPassword, err := auth.NewUserStore(cfg.AuthUsersFile)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize user store: %w", err)
		}
		s.userStore = userStore
		s.initialWebPassword = initialPassword

		// Save initial web password to secrets.json for retrieval in Setup API
		if initialPassword != "" && cfg.Config != nil {
			secrets, err := config.LoadSecrets(cfg.Config.Storage.Root)
			if err == nil && secrets != nil {
				secrets.WebPassword = initialPassword
				if err := config.SaveSecrets(cfg.Config.Storage.Root, secrets); err != nil {
					logger.Warn().Err(err).Msg("failed to save web password to secrets")
				}
			}
		}

		// Initialize token manager
		accessTTL := cfg.AuthAccessTTL
		if accessTTL == 0 {
			accessTTL = 15 * time.Minute
		}
		refreshTTL := cfg.AuthRefreshTTL
		if refreshTTL == 0 {
			refreshTTL = 7 * 24 * time.Hour
		}
		s.tokenManager = auth.NewTokenManager(cfg.AuthJWTSecret, accessTTL, refreshTTL)

		// Initialize auth handler and middleware
		s.authHandler = auth.NewHandler(s.userStore, s.tokenManager)
		s.authMw = auth.NewMiddleware(s.userStore, s.tokenManager)

		logger.Info().Msg("Authentication enabled")
	}

	// Initialize share if enabled
	if cfg != nil && cfg.ShareEnabled && cfg.ShareStoreFile != "" {
		shareStore, err := share.NewShareStore(cfg.ShareStoreFile)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize share store: %w", err)
		}
		s.shareStore = shareStore
		// Pass filesystem adapter to share handler
		var fsAdapter share.FileOpener
		if s.fs != nil {
			fsAdapter = &fileSystemAdapter{fs: s.fs}
		}
		s.shareHandler = share.NewHandler(shareStore, fsAdapter)
		if cfg.ShareBaseURL != "" {
			s.shareHandler.SetBaseURL(cfg.ShareBaseURL)
		}
		logger.Info().Msg("File sharing enabled")
	}

	// Initialize favorites store
	if cfg != nil && cfg.FavoritesStoreFile != "" {
		favStore, err := favorites.NewStore(cfg.FavoritesStoreFile)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize favorites store")
		} else {
			s.favoritesStore = favStore
			s.favoritesHandler = favorites.NewHandler(favStore, logger)
			logger.Info().Msg("Favorites feature enabled")
		}
	}

	// Store config for settings API
	if cfg != nil && cfg.Config != nil {
		s.config = cfg.Config
		s.configPath = cfg.ConfigPath
	}

	s.setupRoutes()
	return s, nil
}

func (s *Server) setupRoutes() {
	// Public endpoints (no auth required)
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/api/v1/version", s.handleVersion)

	// Setup status (public, for showing credentials on first run)
	s.router.Route("/api/v1/setup", func(r chi.Router) {
		r.Get("/", s.handleGetSetupStatus)
	})

	// Auth endpoints (public)
	if s.authEnabled {
		s.router.Route("/api/v1/auth", func(r chi.Router) {
			r.Post("/login", s.handleLoginWithActivity)
			r.Post("/refresh", s.authHandler.HandleRefresh)
		})
	}

	// Public share access (no auth required)
	if s.shareHandler != nil {
		s.router.Route("/s", func(r chi.Router) {
			s.shareHandler.PublicRoutes(r)
		})
	}

	// API v1 - protected routes
	s.router.Route("/api/v1", func(r chi.Router) {
		// Apply auth middleware if enabled
		if s.authEnabled {
			r.Use(s.authMw.RequireAuth)
		}

		// Auth endpoints (require auth)
		if s.authEnabled {
			r.Post("/auth/logout", s.handleLogoutWithActivity)
			r.Get("/auth/me", s.authHandler.HandleMe)
			r.Post("/auth/password", s.authHandler.HandleChangePassword)
			r.Post("/auth/download-session", s.authHandler.HandleCreateDownloadSession)

			r.Route("/setup", func(r chi.Router) {
				r.Use(s.authMw.RequireRole(auth.RoleAdmin))
				r.Post("/acknowledge", s.handleAcknowledgeSetup)
			})

			// Admin user management
			r.Route("/admin/users", func(r chi.Router) {
				r.Use(s.authMw.RequireRole(auth.RoleAdmin))
				r.Get("/", s.authHandler.HandleListUsers)
				r.Post("/", s.authHandler.HandleCreateUser)
				r.Delete("/{id}", func(w http.ResponseWriter, req *http.Request) {
					s.authHandler.HandleDeleteUser(w, req, chi.URLParam(req, "id"))
				})
				r.Post("/{id}/reset-password", func(w http.ResponseWriter, req *http.Request) {
					s.authHandler.HandleResetUserPassword(w, req, chi.URLParam(req, "id"))
				})
				r.Put("/{id}/status", func(w http.ResponseWriter, req *http.Request) {
					s.authHandler.HandleToggleUserStatus(w, req, chi.URLParam(req, "id"))
				})
			})
		}

		// Share endpoints (require auth)
		if s.shareHandler != nil {
			r.Route("/shares", func(r chi.Router) {
				r.Get("/", s.shareHandler.ListShares)
				r.Post("/", s.handleCreateShareWithActivity)
				r.Get("/{id}", s.shareHandler.GetShare)
				r.Put("/{id}", s.shareHandler.UpdateShare)
				r.Delete("/{id}", s.handleDeleteShareWithActivity)
			})
		}

		// Favorites endpoints
		if s.favoritesHandler != nil {
			r.Route("/favorites", func(r chi.Router) {
				r.Get("/", s.favoritesHandler.ListFavorites)
				r.Post("/", s.favoritesHandler.AddFavorite)
				r.Get("/check", s.favoritesHandler.CheckFavorite)
				r.Post("/check-batch", s.favoritesHandler.CheckFavorites)
				r.Delete("/*", s.favoritesHandler.RemoveFavorite)
				r.Patch("/*", s.favoritesHandler.UpdateNote)
			})
		}

		// File operations
		r.Route("/files", func(r chi.Router) {
			r.Get("/*", s.handleListFiles)
			r.Post("/*", s.handleUploadFile)
			r.Delete("/*", s.handleDeleteFile)
		})

		// File operations requiring bodies
		r.Post("/files-move", s.handleMoveFile)
		r.Post("/files-copy", s.handleCopyFile)

		// File download/preview (authenticated, no Basic Auth popup)
		r.Get("/download/*", s.handleDownloadFile)

		// Directory operations
		r.Route("/directories", func(r chi.Router) {
			r.Post("/*", s.handleCreateDirectory)
		})

		// Thumbnail operations
		r.Get("/thumbnails/*", s.handleThumbnail)

		// Version history
		r.Route("/versions", func(r chi.Router) {
			r.Get("/*", s.handleListVersions)
			r.Post("/{hash}/restore", s.handleRestoreVersion)
		})

		// Trash/Recycle bin operations
		r.Route("/trash", func(r chi.Router) {
			r.Get("/", s.handleListTrash)
			r.Delete("/", s.handleEmptyTrash)
			r.Get("/{id}", s.handleGetTrashItem)
			r.Post("/{id}/restore", s.handleRestoreFromTrash)
			r.Delete("/{id}", s.handleDeleteFromTrash)
		})

		// System info
		r.Get("/stats", s.handleStats)
		r.Get("/diagnostics", s.handleDiagnostics)
		r.Get("/diagnostics-export", s.handleDiagnosticsExport)
		r.Get("/metrics", s.handleMetrics)

		// Search
		r.Get("/search", s.handleSearch)

		// Activity log
		r.Route("/activity", func(r chi.Router) {
			r.Get("/", s.handleListActivity)
			r.Get("/stats", s.handleActivityStats)
			r.Delete("/", s.handleClearActivity) // Admin only in production
		})

		// Settings (admin only when auth enabled)
		r.Route("/settings", func(r chi.Router) {
			if s.authEnabled {
				r.Use(s.authMw.RequireRole(auth.RoleAdmin))
			}
			r.Get("/", s.handleGetSettings)
			r.Put("/", s.handleUpdateSettings)
			r.Get("/webdav-credentials", s.handleGetWebDAVCredentials)
		})

		// Maintenance operations (admin only when auth enabled)
		r.Route("/maintenance", func(r chi.Router) {
			if s.authEnabled {
				r.Use(s.authMw.RequireRole(auth.RoleAdmin))
			}
			r.Get("/scrub", s.handleGetScrubResult)
			r.Post("/scrub", s.handleScrub)
			r.Get("/objects", s.handleListObjects)
			r.Post("/gc", s.handleGC)
		})

	})
}

// Router returns the HTTP router
func (s *Server) Router() http.Handler {
	return s.router
}

// validatePath validates and cleans a file path, preventing path traversal attacks.
func validatePath(filePath string) (string, error) {
	// Clean the path first
	cleaned := path.Clean("/" + filePath)

	// Reject any path with .. components
	if strings.Contains(filePath, "..") {
		return "", fmt.Errorf("path traversal attempt detected")
	}

	// Reject paths outside root
	if cleaned != "/" && !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("invalid path: must start with /")
	}

	return cleaned, nil
}

// validateHash validates a BLAKE3 hash string (64 hex characters).
func validateHash(hash string) error {
	if len(hash) != 64 {
		return fmt.Errorf("invalid hash length: expected 64 characters, got %d", len(hash))
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("invalid hash: contains non-hexadecimal character")
		}
	}
	return nil
}

// === Handlers ===

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    time.Since(s.startTime).String(),
	}

	// Check data plane health if connected
	if s.dataplane != nil && s.dataplane.IsConnected() {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultHealthCheckTimeout*time.Second)
		defer cancel()
		if dpHealth, err := s.dataplane.Health(ctx); err == nil {
			health["dataplane"] = map[string]any{
				"healthy": dpHealth.Healthy,
				"version": dpHealth.Version,
				"uptime":  dpHealth.UptimeSecs,
			}
		} else {
			health["dataplane"] = map[string]any{
				"healthy": false,
				"error":   err.Error(),
			}
		}
	}

	s.json(w, http.StatusOK, health)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	NewAPIResponse(map[string]any{
		"name":    AppName,
		"version": AppVersion,
		"go":      runtime.Version(),
	}).Write(w, http.StatusOK)
}

func isStorageNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrVersionNotFound)
}

func isStorageConflict(err error) bool {
	return errors.Is(err, storage.ErrAlreadyExists)
}

func (s *Server) respondNotFound(w http.ResponseWriter, operation string, err error) {
	s.logger.Debug().Err(err).Str("operation", operation).Msg("resource not found")
	NotFound(w, "resource not found")
}

func (s *Server) respondInternalError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error().Err(err).Str("operation", operation).Msg("request failed")
	InternalError(w, "internal server error")
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	filePath := chi.URLParam(r, "*")
	if filePath == "" {
		filePath = "/"
	}

	// REM-5 fix: Validate path to prevent traversal attacks
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Get directory listing
	files, err := s.fs.ReadDir(r.Context(), filePath)
	if err != nil {
		s.respondNotFound(w, "list files", err)
		return
	}

	// Convert to API response format
	items := make([]map[string]any, 0, len(files))
	for _, f := range files {
		item := map[string]any{
			"name":    path.Base(f.Path),
			"path":    f.Path,
			"isDir":   f.IsDir,
			"size":    f.Size,
			"modTime": f.ModTime.Format(time.RFC3339),
		}
		if !f.IsDir && f.ContentHash != "" {
			item["hash"] = f.ContentHash
			item["versioned"] = f.Versioned
		}
		items = append(items, item)
	}

	NewAPIResponse(map[string]any{
		"path":  filePath,
		"files": items,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, DefaultMaxUploadSize)

	if err := s.fs.WriteFile(r.Context(), filePath, r.Body); err != nil {
		s.respondInternalError(w, "upload file", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionUpload, filePath, nil)

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file uploaded successfully").Write(w, http.StatusCreated)
}

func (s *Server) handleCreateDirectory(w http.ResponseWriter, r *http.Request) {
	dirPath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	dirPath, err := validatePath(dirPath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if err := s.fs.Mkdir(r.Context(), dirPath); err != nil {
		// Check if already exists
		if isStorageConflict(err) {
			// Return success for idempotent behavior
			NewAPIResponse(map[string]any{
				"path": dirPath,
			}).WithMessage("directory already exists").Write(w, http.StatusOK)
			return
		}
		s.respondInternalError(w, "create directory", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionCreate, dirPath, map[string]string{"type": "directory"})

	NewAPIResponse(map[string]any{
		"path": dirPath,
	}).WithMessage("directory created successfully").Write(w, http.StatusCreated)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if err := s.fs.Delete(r.Context(), filePath); err != nil {
		// Check if it's a "not found" error
		if isStorageNotFound(err) {
			s.respondNotFound(w, "delete file", err)
			return
		}
		s.respondInternalError(w, "delete file", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionDelete, filePath, nil)

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file deleted successfully").Write(w, http.StatusOK)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	versionHash := r.URL.Query().Get("version")
	forceDownload := r.URL.Query().Get("download") == "true"

	if versionHash != "" {
		if err := validateHash(versionHash); err != nil {
			BadRequest(w, err.Error())
			return
		}

		reader, err := s.fs.GetVersion(r.Context(), filePath, versionHash)
		if err != nil {
			if isStorageNotFound(err) {
				s.respondNotFound(w, "download file version", err)
				return
			}
			s.respondInternalError(w, "download file version", err)
			return
		}
		defer reader.Close()

		if forceDownload {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", path.Base(filePath)))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, reader)
		return
	}

	// Get file info
	info, err := s.fs.Stat(r.Context(), filePath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "stat file", err)
			return
		}
		s.respondInternalError(w, "stat file", err)
		return
	}

	if info.IsDir {
		BadRequest(w, "cannot download directory")
		return
	}

	// Open file
	file, err := s.fs.OpenFile(r.Context(), filePath)
	if err != nil {
		s.respondInternalError(w, "open file", err)
		return
	}
	defer file.Close()

	// Set cache headers
	if info.ContentHash != "" {
		w.Header().Set("ETag", fmt.Sprintf(`"%s"`, info.ContentHash))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if forceDownload {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", path.Base(filePath)))
	}

	// Use http.ServeContent for proper Range support and content type detection
	http.ServeContent(w, r, path.Base(filePath), info.ModTime, file)
}

// MoveRequest represents a move/rename request
type MoveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleMoveFile(w http.ResponseWriter, r *http.Request) {
	var req MoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	fromPath, err := validatePath(req.From)
	if err != nil {
		BadRequest(w, "invalid source path: "+err.Error())
		return
	}

	toPath, err := validatePath(req.To)
	if err != nil {
		BadRequest(w, "invalid destination path: "+err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if err := s.fs.Rename(r.Context(), fromPath, toPath); err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "move file", err)
			return
		}
		s.respondInternalError(w, "move file", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionMove, fromPath, map[string]string{"to": toPath})

	NewAPIResponse(map[string]any{
		"from": fromPath,
		"to":   toPath,
	}).WithMessage("file moved successfully").Write(w, http.StatusOK)
}

// CopyRequest represents a copy request
type CopyRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleCopyFile(w http.ResponseWriter, r *http.Request) {
	var req CopyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	fromPath, err := validatePath(req.From)
	if err != nil {
		BadRequest(w, "invalid source path: "+err.Error())
		return
	}

	toPath, err := validatePath(req.To)
	if err != nil {
		BadRequest(w, "invalid destination path: "+err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Open source file
	reader, err := s.fs.OpenFile(r.Context(), fromPath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "copy file", err)
			return
		}
		s.respondInternalError(w, "copy file", err)
		return
	}
	defer reader.Close()

	// Write to destination
	if err := s.fs.WriteFile(r.Context(), toPath, reader); err != nil {
		s.respondInternalError(w, "copy file", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionCopy, fromPath, map[string]string{"to": toPath})

	NewAPIResponse(map[string]any{
		"from": fromPath,
		"to":   toPath,
	}).WithMessage("file copied successfully").Write(w, http.StatusCreated)
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	versions, err := s.fs.ListVersions(r.Context(), filePath)
	if err != nil {
		if errors.Is(err, storage.ErrIsDir) {
			BadRequest(w, "cannot list versions for directory")
			return
		}
		s.respondNotFound(w, "list versions", err)
		return
	}

	// Convert to API response format
	items := make([]map[string]any, 0, len(versions))
	for i, v := range versions {
		item := map[string]any{
			"version":   i + 1,
			"hash":      v.Hash,
			"size":      v.Size,
			"timestamp": v.Timestamp.Format(time.RFC3339),
		}
		if v.Comment != "" {
			item["comment"] = v.Comment
		}
		items = append(items, item)
	}

	NewAPIResponse(map[string]any{
		"path":     filePath,
		"versions": items,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	// Validate hash format (BLAKE3 = 64 hex chars)
	if err := validateHash(hash); err != nil {
		BadRequest(w, err.Error())
		return
	}

	// Get path from query parameter
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		BadRequest(w, "path parameter is required")
		return
	}

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if s.authEnabled && !auth.IsAdmin(r.Context()) {
		Forbidden(w, "admin access required to restore versions")
		return
	}

	if err := s.fs.RestoreVersion(r.Context(), filePath, hash); err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "restore version", err)
			return
		}
		s.respondInternalError(w, "restore version", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionRestore, filePath, map[string]string{"hash": hash})

	NewAPIResponse(map[string]any{
		"path":     filePath,
		"restored": hash,
	}).WithMessage("version restored successfully").Write(w, http.StatusOK)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		BadRequest(w, "query parameter 'q' is required")
		return
	}

	// Limit query length to prevent abuse
	if len(query) > 100 {
		BadRequest(w, "query too long (max 100 characters)")
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Get optional limit parameter (default 50)
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	results, err := s.fs.Search(r.Context(), query, limit)
	if err != nil {
		s.respondInternalError(w, "search files", err)
		return
	}

	// Convert to API response format
	items := make([]map[string]any, 0, len(results))
	for _, r := range results {
		item := map[string]any{
			"name":    r.Name,
			"path":    r.Path,
			"isDir":   r.IsDir,
			"size":    r.Size,
			"modTime": r.ModTime.Format(time.RFC3339),
		}
		if r.ContentHash != "" {
			item["hash"] = r.ContentHash
		}
		items = append(items, item)
	}

	NewAPIResponse(map[string]any{
		"query":   query,
		"results": items,
		"count":   len(items),
	}).Write(w, http.StatusOK)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]any{
		"total_files":  0,
		"total_size":   0,
		"unique_size":  0,
		"dedup_ratio":  0.0,
		"total_chunks": 0,
	}

	if s.fs != nil {
		if count, err := s.fs.GetFileCount(r.Context()); err == nil {
			stats["total_files"] = count
		}
	}

	// Get stats from data plane if connected
	if s.dataplane != nil && s.dataplane.IsConnected() {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		defer cancel()
		if dpStats, err := s.dataplane.Stats(ctx); err == nil {
			stats["total_chunks"] = dpStats.TotalChunks
			stats["total_size"] = dpStats.TotalSize
			stats["unique_size"] = dpStats.UniqueSize
			stats["dedup_ratio"] = dpStats.DedupRatio
		}
	}

	NewAPIResponse(stats).Write(w, http.StatusOK)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	// Collect diagnostic information
	diag := map[string]any{
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"uptime":      time.Since(s.startTime).String(),
		"uptime_secs": int64(time.Since(s.startTime).Seconds()),
		"version": map[string]any{
			"name":    AppName,
			"version": AppVersion,
			"go":      runtime.Version(),
		},
	}

	// System status
	diag["system"] = map[string]any{
		"filesystem_initialized":  s.fs != nil,
		"dataplane_connected":     s.dataplane != nil && s.dataplane.IsConnected(),
		"thumbnail_service_ready": s.thumbnail != nil,
	}

	// Memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	diag["memory"] = map[string]any{
		"alloc_mb":       memStats.Alloc / 1024 / 1024,
		"total_alloc_mb": memStats.TotalAlloc / 1024 / 1024,
		"sys_mb":         memStats.Sys / 1024 / 1024,
		"num_gc":         memStats.NumGC,
	}

	// Goroutine count
	diag["goroutines"] = runtime.NumGoroutine()

	// File system stats (local CAS)
	if s.fs != nil {
		fsStats := map[string]any{}

		// Get trash stats
		trashCount, trashSize, _ := s.fs.GetTrashStats(r.Context())
		fsStats["trash_items"] = trashCount
		fsStats["trash_size"] = trashSize

		diag["filesystem"] = fsStats
	}

	// Data plane stats
	if s.dataplane != nil && s.dataplane.IsConnected() {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		defer cancel()
		if dpStats, err := s.dataplane.Stats(ctx); err == nil {
			diag["storage"] = map[string]any{
				"total_chunks": dpStats.TotalChunks,
				"total_size":   dpStats.TotalSize,
				"unique_size":  dpStats.UniqueSize,
				"dedup_ratio":  dpStats.DedupRatio,
			}
		}
		if dpHealth, err := s.dataplane.Health(ctx); err == nil {
			diag["dataplane"] = map[string]any{
				"healthy":    dpHealth.Healthy,
				"version":    dpHealth.Version,
				"uptime_sec": dpHealth.UptimeSecs,
			}
		}
	}

	NewAPIResponse(diag).Write(w, http.StatusOK)
}

func (s *Server) handleScrub(w http.ResponseWriter, r *http.Request) {
	if s.dataplane == nil || !s.dataplane.IsConnected() {
		ServiceUnavailable(w, "dataplane not connected")
		return
	}

	// Check if scrub is already running
	if s.maintenance != nil && s.maintenance.ScrubIsRunning() {
		Conflict(w, "scrub is already running")
		return
	}

	// M3 fix: Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, DefaultScrubRequestBodyLimit)

	// Parse optional hashes from request body
	var req struct {
		Hashes []string `json:"hashes,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			BadRequest(w, "invalid request body: "+err.Error())
			return
		}
	}

	// Mark scrub as started
	var scrubRecord *maintenance.ScrubResult
	if s.maintenance != nil {
		scrubRecord = s.maintenance.StartScrub()
	}

	// Scrub may take a long time, extend timeout
	ctx, cancel := context.WithTimeout(r.Context(), DefaultScrubTimeout*time.Second)
	defer cancel()

	result, err := s.dataplane.Scrub(ctx, req.Hashes)
	if err != nil {
		// Save failed result
		if s.maintenance != nil && scrubRecord != nil {
			scrubRecord.Status = "failed"
			scrubRecord.EndTime = time.Now()
			scrubRecord.ErrorMessage = err.Error()
			scrubRecord.DurationMs = uint64(time.Since(scrubRecord.StartTime).Milliseconds())
			_ = s.maintenance.SaveScrubResult(scrubRecord)
		}
		s.respondInternalError(w, "run scrub", err)
		return
	}

	// Convert errors to API format and maintenance format
	errors := make([]map[string]any, 0, len(result.Errors))
	maintErrors := make([]maintenance.ScrubError, 0, len(result.Errors))
	for _, e := range result.Errors {
		errors = append(errors, map[string]any{
			"hash":       e.Hash,
			"error_type": e.ErrorType,
			"message":    e.Message,
		})
		maintErrors = append(maintErrors, maintenance.ScrubError{
			Hash:      e.Hash,
			ErrorType: e.ErrorType,
			Message:   e.Message,
		})
	}

	// Save completed result
	if s.maintenance != nil && scrubRecord != nil {
		scrubRecord.Status = "completed"
		scrubRecord.EndTime = time.Now()
		scrubRecord.TotalObjects = result.TotalObjects
		scrubRecord.ValidObjects = result.ValidObjects
		scrubRecord.CorruptedObjects = result.CorruptedObjects
		scrubRecord.MissingObjects = result.MissingObjects
		scrubRecord.TotalSize = result.TotalSize
		scrubRecord.DurationMs = result.DurationMs
		scrubRecord.Errors = maintErrors
		_ = s.maintenance.SaveScrubResult(scrubRecord)
	}

	NewAPIResponse(map[string]any{
		"total_objects":     result.TotalObjects,
		"valid_objects":     result.ValidObjects,
		"corrupted_objects": result.CorruptedObjects,
		"missing_objects":   result.MissingObjects,
		"total_size":        result.TotalSize,
		"duration_ms":       result.DurationMs,
		"errors":            errors,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	if s.dataplane == nil || !s.dataplane.IsConnected() {
		ServiceUnavailable(w, "dataplane not connected")
		return
	}

	// Parse pagination parameters
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maxObjectsCursorLength {
		BadRequest(w, fmt.Sprintf("cursor exceeds maximum length (%d bytes)", maxObjectsCursorLength))
		return
	}
	limitStr := r.URL.Query().Get("limit")
	var limit uint32 = 1000
	if limitStr != "" {
		if l, err := parseUint32(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), DefaultListObjectsTimeout*time.Second)
	defer cancel()

	result, err := s.dataplane.ListObjects(ctx, cursor, limit)
	if err != nil {
		s.respondInternalError(w, "list objects", err)
		return
	}

	objects := make([]map[string]any, 0, len(result.Objects))
	for _, o := range result.Objects {
		objects = append(objects, map[string]any{
			"hash": o.Hash,
			"size": o.Size,
		})
	}

	resp := map[string]any{
		"objects": objects,
		"count":   len(objects),
	}
	if result.NextCursor != "" {
		resp["next_cursor"] = result.NextCursor
	}

	NewAPIResponse(resp).Write(w, http.StatusOK)
}

func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	if s.dataplane == nil || !s.dataplane.IsConnected() {
		ServiceUnavailable(w, "dataplane not connected")
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Check if GC is already running
	if !maintenance.StartGC() {
		Conflict(w, "garbage collection is already running")
		return
	}
	defer maintenance.FinishGC()

	ctx, cancel := context.WithTimeout(r.Context(), DefaultGCTimeout*time.Second)
	defer cancel()

	// I3 fix: Grace period - skip objects created in the last 24 hours
	// This prevents deleting chunks from in-progress uploads
	gracePeriod := 24 * time.Hour
	if gpStr := r.URL.Query().Get("grace_period_hours"); gpStr != "" {
		if hours, err := strconv.Atoi(gpStr); err == nil && hours >= 0 {
			gracePeriod = time.Duration(hours) * time.Hour
		}
	}
	graceCutoff := time.Now().Add(-gracePeriod) // NEW-2 fix: actual cutoff time

	// Step 1: Get all referenced hashes from metadata
	referencedHashes, err := s.fs.GetAllReferencedHashes(ctx)
	if err != nil {
		s.respondInternalError(w, "gc referenced hashes", err)
		return
	}

	// Step 2: Get all CAS objects
	var allObjects []dataplane.ObjectInfo
	var cursor string
	for {
		result, err := s.dataplane.ListObjects(ctx, cursor, 1000)
		if err != nil {
			s.respondInternalError(w, "gc list objects", err)
			return
		}
		allObjects = append(allObjects, result.Objects...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}

	// Step 3: Find unreferenced objects (excluding recently created)
	referencedSet := make(map[string]struct{}, len(referencedHashes))
	for _, h := range referencedHashes {
		referencedSet[h] = struct{}{}
	}

	var unreferenced []string
	var unreferencedSize uint64
	var skippedByGrace int
	for _, obj := range allObjects {
		if _, ok := referencedSet[obj.Hash]; !ok {
			// NEW-2 fix: Check object creation time against grace period
			if !obj.CreatedAt.IsZero() && obj.CreatedAt.After(graceCutoff) {
				skippedByGrace++
				continue
			}
			unreferenced = append(unreferenced, obj.Hash)
			unreferencedSize += obj.Size
		}
	}

	// Step 4: Delete unreferenced objects (dry-run by default)
	dryRun := r.URL.Query().Get("dry_run") != "false"

	var deletedCount int
	if !dryRun {
		for _, hash := range unreferenced {
			deleted, err := s.dataplane.DeleteChunk(ctx, hash)
			if err != nil {
				s.logger.Warn().Err(err).Str("hash", hash).Msg("failed to delete chunk")
				continue
			}
			if deleted {
				deletedCount++
			}
		}
	}

	NewAPIResponse(map[string]any{
		"dry_run":            dryRun,
		"grace_period_hours": int(gracePeriod.Hours()),
		"total_objects":      len(allObjects),
		"referenced":         len(referencedHashes),
		"unreferenced":       len(unreferenced),
		"unreferenced_size":  unreferencedSize,
		"skipped_by_grace":   skippedByGrace,
		"deleted":            deletedCount,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleGetScrubResult(w http.ResponseWriter, r *http.Request) {
	if s.maintenance == nil {
		ServiceUnavailable(w, "maintenance history not initialized")
		return
	}

	result := s.maintenance.GetLastScrubResult()
	if result == nil {
		NewAPIResponse(map[string]any{
			"has_result": false,
			"message":    "no scrub has been run yet",
		}).Write(w, http.StatusOK)
		return
	}

	// Convert errors to API format
	errors := make([]map[string]any, 0, len(result.Errors))
	for _, e := range result.Errors {
		errors = append(errors, map[string]any{
			"hash":       e.Hash,
			"error_type": e.ErrorType,
			"message":    e.Message,
		})
	}

	NewAPIResponse(map[string]any{
		"has_result":        true,
		"id":                result.ID,
		"start_time":        result.StartTime.Format(time.RFC3339),
		"end_time":          result.EndTime.Format(time.RFC3339),
		"status":            result.Status,
		"total_objects":     result.TotalObjects,
		"valid_objects":     result.ValidObjects,
		"corrupted_objects": result.CorruptedObjects,
		"missing_objects":   result.MissingObjects,
		"total_size":        result.TotalSize,
		"duration_ms":       result.DurationMs,
		"errors":            errors,
		"error_message":     result.ErrorMessage,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleDiagnosticsExport(w http.ResponseWriter, r *http.Request) {
	export := map[string]any{
		"export_time": time.Now().Format(time.RFC3339),
		"version":     AppVersion,
	}

	// System info
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	export["system"] = map[string]any{
		"go_version":    runtime.Version(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"num_cpu":       runtime.NumCPU(),
		"num_goroutine": runtime.NumGoroutine(),
		"memory": map[string]any{
			"alloc_mb":       m.Alloc / 1024 / 1024,
			"total_alloc_mb": m.TotalAlloc / 1024 / 1024,
			"sys_mb":         m.Sys / 1024 / 1024,
			"num_gc":         m.NumGC,
		},
		"uptime_sec": int64(time.Since(s.startTime).Seconds()),
	}

	// Filesystem stats (sanitized)
	if s.fs != nil {
		fsStats := map[string]any{}

		// Trash stats
		trashCount, trashSize, _ := s.fs.GetTrashStats(r.Context())
		fsStats["trash_count"] = trashCount
		fsStats["trash_size"] = trashSize

		export["filesystem"] = fsStats
	}

	// Dataplane stats (sanitized)
	if s.dataplane != nil && s.dataplane.IsConnected() {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		dpInfo := map[string]any{"status": "connected"}
		if dpHealth, err := s.dataplane.Health(ctx); err == nil {
			dpInfo["uptime_sec"] = dpHealth.UptimeSecs
		}
		if dpStats, err := s.dataplane.Stats(ctx); err == nil {
			dpInfo["chunk_count"] = dpStats.TotalChunks
			dpInfo["storage_size"] = dpStats.TotalSize
			dpInfo["dedup_ratio"] = dpStats.DedupRatio
		}
		cancel()
		export["dataplane"] = dpInfo
	}

	// Last scrub result
	if s.maintenance != nil {
		if result := s.maintenance.GetLastScrubResult(); result != nil {
			scrubInfo := map[string]any{
				"id":                result.ID,
				"status":            result.Status,
				"start_time":        result.StartTime.Format(time.RFC3339),
				"total_objects":     result.TotalObjects,
				"valid_objects":     result.ValidObjects,
				"corrupted_objects": result.CorruptedObjects,
				"duration_ms":       result.DurationMs,
			}
			if !result.EndTime.IsZero() {
				scrubInfo["end_time"] = result.EndTime.Format(time.RFC3339)
			}
			if result.ErrorMessage != "" {
				scrubInfo["error_message"] = result.ErrorMessage
			}
			if len(result.Errors) > 0 {
				scrubInfo["error_count"] = len(result.Errors)
			}
			export["last_scrub"] = scrubInfo
		}
	}

	// Set filename for download
	filename := fmt.Sprintf("mnemonas-diagnostics-%s.json", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	s.json(w, http.StatusOK, export)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := metrics.Global().GetStats()

	// Add cache stats if available
	response := map[string]any{
		"requests": map[string]any{
			"total":      stats.TotalRequests,
			"by_method":  stats.MethodCounts,
			"count_2xx":  stats.Count2xx,
			"count_4xx":  stats.Count4xx,
			"count_5xx":  stats.Count5xx,
			"error_rate": stats.ErrorRate,
		},
		"latency": map[string]any{
			"avg_ms": stats.AvgLatencyMs,
			"max_ms": stats.MaxLatencyMs,
		},
		"throughput": map[string]any{
			"bytes_in":  stats.BytesIn,
			"bytes_out": stats.BytesOut,
			"mb_per_s":  stats.ThroughputMBs,
		},
		"uptime_secs":   stats.UptimeSecs,
		"slow_requests": stats.SlowRequests,
	}

	NewAPIResponse(response).Write(w, http.StatusOK)
}

// === Helper functions ===

func (s *Server) json(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func parseUint32(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err
}

// fileSystemAdapter wraps FileSystem to implement share.FileOpener
type fileSystemAdapter struct {
	fs *storage.FileSystem
}

func (a *fileSystemAdapter) OpenFile(ctx context.Context, filePath string) (share.FileReader, error) {
	return a.fs.OpenFile(ctx, filePath)
}

func (a *fileSystemAdapter) Stat(ctx context.Context, filePath string) (*storage.FileInfo, error) {
	return a.fs.Stat(ctx, filePath)
}

func (a *fileSystemAdapter) ReadDir(ctx context.Context, filePath string) ([]*storage.FileInfo, error) {
	return a.fs.ReadDir(ctx, filePath)
}

// zerologMiddleware is a request logging middleware using zerolog
func zerologMiddleware(logger zerolog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				logger.Info().
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", ww.Status()).
					Int("bytes", ww.BytesWritten()).
					Dur("duration", time.Since(start)).
					Str("remote", r.RemoteAddr).
					Msg("request")
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// === Trash Handlers ===

func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	items, err := s.fs.ListTrash(r.Context())
	if err != nil {
		s.respondInternalError(w, "list trash", err)
		return
	}

	// Get stats
	count, totalSize, _ := s.fs.GetTrashStats(r.Context())

	// Convert to API response format
	apiItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		apiItem := map[string]any{
			"id":           item.ID,
			"originalPath": item.OriginalPath,
			"deletedAt":    item.DeletedAt.Format(time.RFC3339),
			"name":         path.Base(item.OriginalPath),
			"isDir":        item.IsDir,
			"size":         item.Size,
			"hadVersions":  item.HadVersions,
		}
		apiItems = append(apiItems, apiItem)
	}

	response := map[string]any{
		"items":     apiItems,
		"count":     count,
		"totalSize": totalSize,
	}
	if s.config != nil {
		response["retentionDays"] = s.config.Storage.Trash.RetentionDays
		response["retentionEnabled"] = s.config.Storage.Trash.Enabled
		response["retentionMaxSize"] = s.config.Storage.Trash.MaxSize
	}
	NewAPIResponse(response).Write(w, http.StatusOK)
}

func (s *Server) handleGetTrashItem(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		BadRequest(w, "id is required")
		return
	}

	item, err := s.fs.GetTrashItem(r.Context(), id)
	if err != nil {
		s.respondNotFound(w, "get trash item", err)
		return
	}

	NewAPIResponse(map[string]any{
		"id":           item.ID,
		"originalPath": item.OriginalPath,
		"deletedAt":    item.DeletedAt.Format(time.RFC3339),
		"name":         path.Base(item.OriginalPath),
		"isDir":        item.IsDir,
		"size":         item.Size,
		"hadVersions":  item.HadVersions,
	}).Write(w, http.StatusOK)
}

func (s *Server) handleRestoreFromTrash(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		BadRequest(w, "id is required")
		return
	}

	// Check if custom restore path is provided
	newPath := r.URL.Query().Get("path")

	var err error
	if newPath != "" {
		// Restore to custom path
		newPath, err = validatePath(newPath)
		if err != nil {
			BadRequest(w, err.Error())
			return
		}
		err = s.fs.RestoreFromTrashTo(r.Context(), id, newPath)
	} else {
		// Restore to original path
		err = s.fs.RestoreFromTrash(r.Context(), id)
	}

	if err != nil {
		if isStorageConflict(err) {
			Conflict(w, "resource already exists")
			return
		}
		if isStorageNotFound(err) {
			s.respondNotFound(w, "restore from trash", err)
			return
		}
		s.respondInternalError(w, "restore from trash", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionTrashRestore, id, nil)

	NewAPIResponse(map[string]any{
		"id":       id,
		"restored": true,
	}).WithMessage("file restored successfully").Write(w, http.StatusOK)
}

func (s *Server) handleDeleteFromTrash(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		BadRequest(w, "id is required")
		return
	}

	if err := s.fs.DeleteFromTrash(r.Context(), id); err != nil {
		s.respondNotFound(w, "delete from trash", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionTrashDelete, id, nil)

	NewAPIResponse(map[string]any{
		"id":      id,
		"deleted": true,
	}).WithMessage("item permanently deleted").Write(w, http.StatusOK)
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	count, err := s.fs.EmptyTrash(r.Context())
	if err != nil {
		s.respondInternalError(w, "empty trash", err)
		return
	}

	// Log activity
	s.LogActivity(r, activity.ActionTrashEmpty, "", map[string]string{"count": strconv.Itoa(count)})

	NewAPIResponse(map[string]any{
		"deleted_count": count,
	}).WithMessage("trash emptied successfully").Write(w, http.StatusOK)
}

// === Thumbnail Handler ===

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if s.thumbnail == nil {
		ServiceUnavailable(w, "thumbnail service not initialized")
		return
	}

	filePath := "/" + chi.URLParam(r, "*")

	// Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	// Check if file is a supported image
	if !thumbnail.IsSupportedImage(filePath) {
		BadRequest(w, "file is not a supported image type")
		return
	}

	// Get size parameter
	sizeParam := r.URL.Query().Get("size")
	var size thumbnail.Size
	switch sizeParam {
	case "small", "s":
		size = thumbnail.SizeSmall
	case "large", "l":
		size = thumbnail.SizeLarge
	default:
		size = thumbnail.SizeMedium
	}

	// Open original file
	reader, err := s.fs.OpenFile(r.Context(), filePath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "thumbnail open file", err)
			return
		}
		s.respondInternalError(w, "thumbnail open file", err)
		return
	}
	defer reader.Close()

	// Generate or retrieve cached thumbnail
	data, err := s.thumbnail.GetThumbnail(r.Context(), filePath, size, reader)
	if err != nil {
		// Log the error but return a generic message
		s.logger.Warn().Err(err).Str("path", filePath).Msg("Failed to generate thumbnail")
		InternalError(w, "failed to generate thumbnail")
		return
	}

	// Set appropriate headers
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleListActivity returns recent activity log entries
func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"items": []any{},
			"total": 0,
		}).Write(w, http.StatusOK)
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	actionFilter := r.URL.Query().Get("action")
	userFilter := r.URL.Query().Get("user")

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	offset := 0
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	entries, total := s.activity.List(limit, offset, activity.ActionType(actionFilter), userFilter)

	NewAPIResponse(map[string]any{
		"items":  entries,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}).Write(w, http.StatusOK)
}

// handleActivityStats returns activity statistics
func (s *Server) handleActivityStats(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"total":     0,
			"today":     0,
			"by_action": map[string]int{},
			"by_user":   map[string]int{},
		}).Write(w, http.StatusOK)
		return
	}

	stats := s.activity.Statistics()
	NewAPIResponse(stats).Write(w, http.StatusOK)
}

// handleClearActivity clears all activity log entries
func (s *Server) handleClearActivity(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"message": "Activity log not configured",
		}).Write(w, http.StatusOK)
		return
	}

	if err := s.activity.Clear(); err != nil {
		InternalError(w, "failed to clear activity log: "+err.Error())
		return
	}

	NewAPIResponse(map[string]any{
		"message": "Activity log cleared",
	}).Write(w, http.StatusOK)
}

// LogActivity is a helper to log user activity
func (s *Server) LogActivity(r *http.Request, action activity.ActionType, path string, details map[string]string) {
	if s.activity == nil {
		return
	}

	user := "anonymous"
	if s.authEnabled {
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}
	}

	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ip = realIP
	}

	if err := s.activity.Log(action, path, user, ip, details); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to log activity")
	}
}

// handleLoginWithActivity wraps auth login to log activity
func (s *Server) handleLoginWithActivity(w http.ResponseWriter, r *http.Request) {
	// Create a response recorder to capture the response
	rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

	// Call the actual login handler
	s.authHandler.HandleLogin(rec, r)

	// If login was successful (status 200), log the activity
	if rec.statusCode == http.StatusOK && s.activity != nil {
		// Try to extract username from request (best effort)
		user := "unknown"
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}

		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = realIP
		}

		s.activity.Log(activity.ActionLogin, "", user, ip, nil)
	}
}

// handleLogoutWithActivity wraps auth logout to log activity
func (s *Server) handleLogoutWithActivity(w http.ResponseWriter, r *http.Request) {
	// Log activity before logout (while we still have the user context)
	if s.activity != nil {
		user := "unknown"
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}

		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = realIP
		}

		s.activity.Log(activity.ActionLogout, "", user, ip, nil)
	}

	// Call the actual logout handler
	s.authHandler.HandleLogout(w, r)
}

// handleGetSettings returns current settings
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.config == nil {
		ServiceUnavailable(w, "settings not available")
		return
	}

	settings := map[string]interface{}{
		"server": map[string]interface{}{
			"host": s.config.Server.Host,
			"port": s.config.Server.Port,
		},
		"storage": map[string]interface{}{
			"root": s.config.Storage.Root,
		},
		"retention": map[string]interface{}{
			"max_versions":   s.config.Storage.Retention.MaxVersions,
			"max_age":        s.config.Storage.Retention.MaxAge.String(),
			"min_free_space": s.config.Storage.Retention.MinFreeSpace,
			"gc_interval":    s.config.Storage.Retention.GCInterval.String(),
		},
		"webdav": map[string]interface{}{
			"enabled":   s.config.WebDAV.Enabled,
			"prefix":    s.config.WebDAV.Prefix,
			"read_only": s.config.WebDAV.ReadOnly,
			"auth_type": s.config.WebDAV.AuthType,
			"username":  s.config.WebDAV.Username,
		},
		"dataplane": map[string]interface{}{
			"grpc_address": s.config.DataPlane.GRPCAddress,
			"timeout":      s.config.DataPlane.Timeout.String(),
			"max_retries":  s.config.DataPlane.MaxRetries,
		},
		"cdc": map[string]interface{}{
			"min_chunk_size": s.config.DataPlane.CDC.MinChunkSize,
			"avg_chunk_size": s.config.DataPlane.CDC.AvgChunkSize,
			"max_chunk_size": s.config.DataPlane.CDC.MaxChunkSize,
		},
	}

	NewAPIResponse(settings).Write(w, http.StatusOK)
}

// UpdateSettingsRequest represents settings update request
type UpdateSettingsRequest struct {
	Server    *ServerSettingsUpdate    `json:"server,omitempty"`
	Retention *RetentionSettingsUpdate `json:"retention,omitempty"`
	CDC       *CDCSettingsUpdate       `json:"cdc,omitempty"`
	WebDAV    *WebDAVSettingsUpdate    `json:"webdav,omitempty"`
}

type ServerSettingsUpdate struct {
	Host *string `json:"host,omitempty"`
	Port *int    `json:"port,omitempty"`
}

type RetentionSettingsUpdate struct {
	MaxVersions  *int    `json:"max_versions,omitempty"`
	MaxAge       *string `json:"max_age,omitempty"`
	MinFreeSpace *uint64 `json:"min_free_space,omitempty"`
	GCInterval   *string `json:"gc_interval,omitempty"`
}

type WebDAVSettingsUpdate struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	Prefix   *string `json:"prefix,omitempty"`
	ReadOnly *bool   `json:"read_only,omitempty"`
	AuthType *string `json:"auth_type,omitempty"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

type CDCSettingsUpdate struct {
	MinChunkSize *uint32 `json:"min_chunk_size,omitempty"`
	AvgChunkSize *uint32 `json:"avg_chunk_size,omitempty"`
	MaxChunkSize *uint32 `json:"max_chunk_size,omitempty"`
}

// handleUpdateSettings updates settings
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.config == nil || s.configPath == "" {
		ServiceUnavailable(w, "settings not available or no config file path")
		return
	}

	var req UpdateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	// Apply updates
	if req.Server != nil {
		if req.Server.Host != nil {
			s.config.Server.Host = *req.Server.Host
		}
		if req.Server.Port != nil {
			s.config.Server.Port = *req.Server.Port
		}
	}

	if req.Retention != nil {
		if req.Retention.MaxVersions != nil {
			s.config.Storage.Retention.MaxVersions = *req.Retention.MaxVersions
		}
		if req.Retention.MaxAge != nil {
			if d, err := time.ParseDuration(*req.Retention.MaxAge); err == nil {
				s.config.Storage.Retention.MaxAge = d
			}
		}
		if req.Retention.MinFreeSpace != nil {
			s.config.Storage.Retention.MinFreeSpace = *req.Retention.MinFreeSpace
		}
		if req.Retention.GCInterval != nil {
			if d, err := time.ParseDuration(*req.Retention.GCInterval); err == nil {
				s.config.Storage.Retention.GCInterval = d
			}
		}
	}

	if req.CDC != nil {
		if req.CDC.MinChunkSize != nil {
			s.config.DataPlane.CDC.MinChunkSize = *req.CDC.MinChunkSize
		}
		if req.CDC.AvgChunkSize != nil {
			s.config.DataPlane.CDC.AvgChunkSize = *req.CDC.AvgChunkSize
		}
		if req.CDC.MaxChunkSize != nil {
			s.config.DataPlane.CDC.MaxChunkSize = *req.CDC.MaxChunkSize
		}
	}

	if req.WebDAV != nil {
		if req.WebDAV.Enabled != nil {
			s.config.WebDAV.Enabled = *req.WebDAV.Enabled
		}
		if req.WebDAV.Prefix != nil {
			s.config.WebDAV.Prefix = *req.WebDAV.Prefix
		}
		if req.WebDAV.ReadOnly != nil {
			s.config.WebDAV.ReadOnly = *req.WebDAV.ReadOnly
		}
		if req.WebDAV.AuthType != nil {
			s.config.WebDAV.AuthType = *req.WebDAV.AuthType
		}
		if req.WebDAV.Username != nil {
			s.config.WebDAV.Username = *req.WebDAV.Username
		}
		if req.WebDAV.Password != nil {
			s.config.WebDAV.Password = *req.WebDAV.Password
		}
		s.config.WebDAV.Prefix = config.NormalizeWebDAVPrefix(s.config.WebDAV.Prefix)
	}

	// Validate config
	if err := s.config.Validate(); err != nil {
		BadRequest(w, "invalid configuration: "+err.Error())
		return
	}

	// Save to file
	if err := s.config.Save(s.configPath); err != nil {
		s.logger.Error().Err(err).Msg("failed to save config")
		InternalError(w, "failed to save settings")
		return
	}

	s.logger.Info().Msg("settings updated and saved")

	NewAPIResponse(nil).WithMessage("settings updated, some changes may require restart").Write(w, http.StatusOK)
}

// WebDAVCredentialsResponse represents WebDAV credentials for authenticated users
type WebDAVCredentialsResponse struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	AuthType string `json:"auth_type"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// handleGetWebDAVCredentials returns WebDAV credentials for authenticated users
func (s *Server) handleGetWebDAVCredentials(w http.ResponseWriter, r *http.Request) {
	if s.config == nil {
		ServiceUnavailable(w, "configuration not available")
		return
	}

	resp := WebDAVCredentialsResponse{
		Enabled:  s.config.WebDAV.Enabled,
		URL:      formatWebDAVPrefix(s.config.WebDAV.Prefix),
		AuthType: s.config.WebDAV.AuthType,
	}

	// Only include credentials if WebDAV is enabled and using basic auth
	if s.config.WebDAV.Enabled && s.config.WebDAV.AuthType == "basic" {
		resp.Username = s.config.WebDAV.Username
		if resp.Username == "" {
			resp.Username = "admin"
		}
		if s.config.WebDAV.Password == "" {
			// Get password from secrets (auto-generated only)
			secrets, err := config.LoadSecrets(s.config.Storage.Root)
			if err == nil && secrets != nil {
				resp.Password = secrets.WebDAVPassword
			}
		}
	}

	NewAPIResponse(resp).Write(w, http.StatusOK)
}

func formatWebDAVPrefix(prefix string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(prefix), "/")
	if trimmed == "" || trimmed == "/" {
		return "/"
	}
	return trimmed + "/"
}

// SetupStatusResponse represents the setup status response
type SetupStatusResponse struct {
	Success        bool   `json:"success"`
	IsFirstRun     bool   `json:"is_first_run"`
	AuthEnabled    bool   `json:"auth_enabled"`
	WebDAVEnabled  bool   `json:"webdav_enabled"`
	WebDAVAuthType string `json:"webdav_auth_type"`
}

// handleGetSetupStatus returns setup status for first run.
// Initial credentials are intentionally only exposed through server-side logs.
func (s *Server) handleGetSetupStatus(w http.ResponseWriter, r *http.Request) {
	secrets, err := config.LoadSecrets(s.config.Storage.Root)
	if err != nil {
		// Error reading secrets file
		s.logger.Error().Err(err).Msg("failed to load secrets")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SetupStatusResponse{
			Success:    true,
			IsFirstRun: false,
		})
		return
	}

	// No secrets file means not first run or something went wrong
	if secrets == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SetupStatusResponse{
			Success:    true,
			IsFirstRun: false,
		})
		return
	}

	resp := SetupStatusResponse{
		Success:        true,
		IsFirstRun:     !secrets.SetupShown,
		AuthEnabled:    s.authEnabled,
		WebDAVEnabled:  s.config.WebDAV.Enabled,
		WebDAVAuthType: s.config.WebDAV.AuthType,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAcknowledgeSetup marks the setup as shown
func (s *Server) handleAcknowledgeSetup(w http.ResponseWriter, r *http.Request) {
	if err := config.MarkSetupShown(s.config.Storage.Root); err != nil {
		s.logger.Error().Err(err).Msg("failed to mark setup as shown")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "failed to acknowledge setup",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "setup acknowledged",
	})
}

// responseRecorder wraps http.ResponseWriter to capture status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// handleCreateShareWithActivity wraps share creation to log activity
func (s *Server) handleCreateShareWithActivity(w http.ResponseWriter, r *http.Request) {
	rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	s.shareHandler.CreateShare(rec, r)

	// If share was created (status 201), log the activity
	if rec.statusCode == http.StatusCreated && s.activity != nil {
		user := "unknown"
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}

		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = realIP
		}

		s.activity.Log(activity.ActionShare, "", user, ip, nil)
	}
}

// handleDeleteShareWithActivity wraps share deletion to log activity
func (s *Server) handleDeleteShareWithActivity(w http.ResponseWriter, r *http.Request) {
	rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	s.shareHandler.DeleteShare(rec, r)

	// If share was deleted (status 204), log the activity
	if rec.statusCode == http.StatusNoContent && s.activity != nil {
		user := "unknown"
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}

		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = realIP
		}

		s.activity.Log(activity.ActionUnshare, "", user, ip, nil)
	}
}
