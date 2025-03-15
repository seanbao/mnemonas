// Package api provides REST API and gRPC API
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/maintenance"
	"github.com/seanbao/mnemonas/internal/metrics"
	"github.com/seanbao/mnemonas/internal/requestip"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/thumbnail"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

const maxObjectsCursorLength = 256

const scrubFailurePublicMessage = "scrub failed; check server logs for details"

var saveScrubResult = func(store *maintenance.HistoryStore, result *maintenance.ScrubResult) error {
	return store.SaveScrubResult(result)
}

var deleteGCChunk = func(client *dataplane.Client, ctx context.Context, hash string) (bool, error) {
	return client.DeleteChunk(ctx, hash)
}

var getTrashStats = func(fs *storage.FileSystem, ctx context.Context) (int, int64, error) {
	return fs.GetTrashStats(ctx)
}

var getFileCount = func(fs *storage.FileSystem, ctx context.Context) (int, error) {
	return fs.GetFileCount(ctx)
}

type prependReadCloser struct {
	io.Reader
	io.Closer
}

// Server is the API server
type Server struct {
	router                *chi.Mux
	logger                zerolog.Logger
	dataplane             *dataplane.Client
	fs                    *storage.FileSystem
	thumbnail             *thumbnail.Service
	maintenance           *maintenance.HistoryStore
	activity              *activity.Store
	thumbnailConfigured   bool
	maintenanceConfigured bool
	activityConfigured    bool
	startTime             time.Time
	// Auth components
	userStore          *auth.UserStore
	tokenManager       *auth.TokenManager
	authHandler        *auth.Handler
	authMw             *auth.Middleware
	authEnabled        bool
	initialWebPassword string // Set when admin user is first created
	// Share components
	shareStore       *share.ShareStore
	shareHandler     *share.Handler
	alertMonitor     AlertMonitor
	retentionMonitor RetentionMonitor
	// Favorites components
	favoritesStore      *favorites.Store
	favoritesHandler    *favorites.Handler
	favoritesConfigured bool
	// Config
	config       *config.Config
	configMu     sync.RWMutex
	configPath   string
	activeWebDAV WebDAVRuntimeConfig
	webdavMu     sync.RWMutex
	updateWebDAV func(WebDAVRuntimeConfig)
}

type WebDAVRuntimeConfig struct {
	Enabled             bool
	Prefix              string
	ReadOnly            bool
	AuthType            string
	Username            string
	Password            string
	PasswordIsGenerated bool
}

func cloneConfigSnapshot(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	return &cloned
}

func (s *Server) currentConfig() *config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config
}

func (s *Server) storeConfig(cfg *config.Config) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.config = cloneConfigSnapshot(cfg)
}

func (s *Server) currentActiveWebDAV() WebDAVRuntimeConfig {
	s.webdavMu.RLock()
	defer s.webdavMu.RUnlock()
	return s.activeWebDAV
}

func (s *Server) storeActiveWebDAV(cfg WebDAVRuntimeConfig) {
	s.webdavMu.Lock()
	defer s.webdavMu.Unlock()
	s.activeWebDAV = cfg
}

type AlertMonitor interface {
	UpdateConfig(cfg alerts.Config)
}

type RetentionMonitor interface {
	UpdateConfig(cfg storage.RetentionMonitorConfig)
}

func formatSettingsDuration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	return d.String()
}

const (
	auditStatusHeaderName  = "X-Mnemonas-Audit-Status"
	auditStatusFailedValue = "failed"
	auditWarningHeader     = `199 MnemoNAS "activity log persistence failed"`
)

func decodeJSONBodyWithLimit(r *http.Request, dst any, limit int64) error {
	if _, err := readBufferedRequestBody(r, limit); err != nil {
		return err
	}

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

func decodeJSONBody(r *http.Request, dst any) error {
	return decodeJSONBodyWithLimit(r, dst, DefaultJSONRequestBodyLimit)
}

func readBufferedRequestBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func writeLimitedJSONBodyError(w http.ResponseWriter, err error, limit int64) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		respondPayloadTooLarge(w, fmt.Sprintf("request body too large (max %d bytes)", limit))
		return
	}

	BadRequest(w, "invalid request body")
}

func requestHasBody(r *http.Request) (bool, error) {
	if r.Body == nil {
		return false, nil
	}
	if r.ContentLength > 0 {
		return true, nil
	}
	if r.ContentLength == 0 {
		return false, nil
	}

	var firstByte [1]byte
	n, err := r.Body.Read(firstByte[:])
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}

	originalBody := r.Body
	r.Body = &prependReadCloser{
		Reader: io.MultiReader(bytes.NewReader(firstByte[:n]), originalBody),
		Closer: originalBody,
	}

	return true, nil
}

func (s *Server) resolveWebDAVRuntimeConfig(cfg config.Config) WebDAVRuntimeConfig {
	runtimeCfg := WebDAVRuntimeConfig{
		Enabled:  cfg.WebDAV.Enabled,
		Prefix:   cfg.WebDAV.Prefix,
		ReadOnly: cfg.WebDAV.ReadOnly,
		AuthType: cfg.WebDAV.AuthType,
		Username: cfg.WebDAV.Username,
		Password: cfg.WebDAV.Password,
	}

	if !runtimeCfg.Enabled || !strings.EqualFold(runtimeCfg.AuthType, "basic") {
		return runtimeCfg
	}

	if strings.TrimSpace(runtimeCfg.Username) == "" {
		runtimeCfg.Username = "admin"
	}

	if strings.TrimSpace(runtimeCfg.Password) != "" || cfg.Storage.Root == "" {
		return runtimeCfg
	}

	secrets, err := config.LoadSecrets(cfg.Storage.Root)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load secrets for WebDAV runtime config")
		return runtimeCfg
	}
	if secrets == nil || strings.TrimSpace(secrets.WebDAVPassword) == "" {
		return runtimeCfg
	}

	runtimeCfg.Password = secrets.WebDAVPassword
	runtimeCfg.PasswordIsGenerated = true
	return runtimeCfg
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
	ShareEnabled     bool
	ShareStoreFile   string
	ShareBaseURL     string
	AlertMonitor     AlertMonitor
	RetentionMonitor RetentionMonitor
	// Favorites configuration
	FavoritesEnabled   bool
	FavoritesStoreFile string
	// Config (for settings API)
	Config       *config.Config
	ConfigPath   string
	ActiveWebDAV *WebDAVRuntimeConfig
	UpdateWebDAV func(WebDAVRuntimeConfig)
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
	// Keep observability endpoints reachable even when the request concurrency
	// budget is saturated by slow data operations.
	r.Use(throttleExceptPaths(DefaultMaxConcurrentRequests, "/health", "/api/v1/version"))

	s := &Server{
		router:    r,
		logger:    logger,
		startTime: time.Now(),
	}
	if cfg != nil {
		s.updateWebDAV = cfg.UpdateWebDAV
	}

	// Store config early so runtime dataplane connection settings are available
	// during initial client setup.
	if cfg != nil && cfg.Config != nil {
		s.storeConfig(cfg.Config)
		s.configPath = cfg.ConfigPath
		s.storeActiveWebDAV(s.resolveWebDAVRuntimeConfig(*cfg.Config))
	}
	if cfg != nil && cfg.ActiveWebDAV != nil {
		s.storeActiveWebDAV(*cfg.ActiveWebDAV)
	}

	// Initialize data plane client if address provided
	if cfg != nil && cfg.DataplaneAddr != "" {
		s.dataplane = dataplane.NewClient(cfg.DataplaneAddr)
		if err := s.connectDataplaneClient(context.Background(), s.dataplane, DefaultDataplaneConnectTimeout*time.Second); err != nil {
			logger.Warn().Err(err).Msg("Failed to connect to data plane, will retry later")
		} else {
			logger.Info().Str("addr", cfg.DataplaneAddr).Msg("Connected to data plane")
		}
	}

	// Initialize filesystem (from pre-created instance)
	if cfg != nil && cfg.FileSystem != nil {
		s.fs = cfg.FileSystem
	}
	if cfg != nil {
		s.alertMonitor = cfg.AlertMonitor
		s.retentionMonitor = cfg.RetentionMonitor
	}

	// Initialize thumbnail service
	if cfg != nil && cfg.ThumbnailRoot != "" {
		s.thumbnailConfigured = true
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
		s.maintenanceConfigured = true
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
		s.activityConfigured = true
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

	// Initialize share handler whenever a share store is configured so runtime
	// settings can enable or disable public access without rebuilding routes.
	if cfg != nil && cfg.ShareStoreFile != "" {
		shareStore, err := share.NewShareStore(cfg.ShareStoreFile)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize share store: %w", err)
		}
		s.shareStore = shareStore
		if s.fs != nil {
			s.fs.SetPathChangeHooks(
				func(ctx context.Context, oldPath, newPath string) {
					if err := shareStore.UpdatePathReferences(oldPath, newPath); err != nil {
						logger.Error().Err(err).Str("old_path", oldPath).Str("new_path", newPath).Msg("failed to sync share paths after rename")
					}
				},
				func(ctx context.Context, path string) {
					if err := shareStore.DisableSharesUnderPath(path); err != nil {
						logger.Error().Err(err).Str("path", path).Msg("failed to disable shares after delete")
					}
				},
			)
		}
		// Pass filesystem adapter to share handler
		var fsAdapter share.FileOpener
		if s.fs != nil {
			fsAdapter = &fileSystemAdapter{fs: s.fs}
		}
		s.shareHandler = share.NewHandler(shareStore, fsAdapter)
		if cfg.ShareBaseURL != "" {
			s.shareHandler.SetBaseURL(cfg.ShareBaseURL)
		}
		if cfg.ShareEnabled {
			logger.Info().Msg("File sharing enabled")
		} else {
			logger.Info().Msg("File sharing configured but currently disabled")
		}
	}

	// Initialize favorites handler whenever a store is configured so runtime
	// settings can enable or disable the feature without rebuilding routes.
	if cfg != nil && cfg.FavoritesStoreFile != "" {
		s.favoritesConfigured = true
		favStore, err := favorites.NewStore(cfg.FavoritesStoreFile)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize favorites store")
		} else {
			s.favoritesStore = favStore
			s.favoritesHandler = favorites.NewHandler(favStore, logger)
			if cfg.FavoritesEnabled {
				logger.Info().Msg("Favorites feature enabled")
			} else {
				logger.Info().Msg("Favorites feature configured but currently disabled")
			}
		}
	}

	s.setupRoutes()
	return s, nil
}

func throttleExceptPaths(limit int, bypassPaths ...string) func(http.Handler) http.Handler {
	bypass := make(map[string]struct{}, len(bypassPaths))
	for _, bypassPath := range bypassPaths {
		bypass[bypassPath] = struct{}{}
	}
	throttle := middleware.Throttle(limit)

	return func(next http.Handler) http.Handler {
		throttled := throttle(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := bypass[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			throttled.ServeHTTP(w, r)
		})
	}
}

func (s *Server) setupRoutes() {
	// Public endpoints (no auth required)
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/api/v1/version", s.handleVersion)

	// Setup status (public, for showing credentials on first run)
	s.router.Route("/api/v1/setup", func(r chi.Router) {
		r.Get("/", s.handleGetSetupStatus)
		if s.authEnabled {
			r.With(s.authMw.RequireAuth, s.authMw.RequireRole(auth.RoleAdmin)).Post("/acknowledge", s.handleAcknowledgeSetup)
			return
		}
		r.Post("/acknowledge", s.handleAcknowledgeSetup)
	})

	// Auth endpoints (public)
	if s.authEnabled {
		s.router.Route("/api/v1/auth", func(r chi.Router) {
			r.Post("/login", s.handleLoginWithActivity)
			r.Post("/refresh", s.authHandler.HandleRefresh)
			r.With(s.authMw.RequireAuth).Post("/logout", s.handleLogoutWithActivity)
			r.With(s.authMw.RequireAuth).Get("/me", s.authHandler.HandleMe)
			r.With(s.authMw.RequireAuth).Post("/password", s.authHandler.HandleChangePassword)
			r.With(s.authMw.RequireAuth).Post("/download-session", s.authHandler.HandleCreateDownloadSession)
		})
	}

	// Public share access (no auth required)
	if s.shareHandler != nil {
		s.router.Route("/s", func(r chi.Router) {
			r.Get("/{id}", s.handleAccessShare)
			r.Post("/{id}", s.handleAccessShareWithPassword)
			r.Get("/{id}/items", s.handleListShareItems)
			r.Get("/{id}/download", s.handleDownloadShare)
			r.Get("/{id}/download/*", s.handleDownloadShareFile)
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

		var requireWriteAccess func(http.Handler) http.Handler
		if s.authEnabled {
			requireWriteAccess = s.authMw.RequireRole(auth.RoleAdmin, auth.RoleUser)
		}

		// Share endpoints (require auth)
		if s.shareHandler != nil {
			r.Route("/shares", func(r chi.Router) {
				r.Get("/", s.handleListShares)
				if s.authEnabled {
					r.With(requireWriteAccess).Post("/", s.handleCreateShareWithActivity)
				} else {
					r.Post("/", s.handleCreateShareWithActivity)
				}
				r.Get("/{id}", s.handleGetShare)
				if s.authEnabled {
					r.With(requireWriteAccess).Put("/{id}", s.handleUpdateShare)
					r.With(requireWriteAccess).Delete("/{id}", s.handleDeleteShareWithActivity)
				} else {
					r.Put("/{id}", s.handleUpdateShare)
					r.Delete("/{id}", s.handleDeleteShareWithActivity)
				}
			})
		}

		// Favorites endpoints
		if s.favoritesHandler != nil || s.favoritesConfigured || (s.config != nil && s.config.Favorites.Enabled) {
			r.Route("/favorites", func(r chi.Router) {
				r.Get("/", s.handleListFavorites)
				if s.authEnabled {
					r.With(requireWriteAccess).Post("/", s.handleAddFavorite)
				} else {
					r.Post("/", s.handleAddFavorite)
				}
				r.Get("/check", s.handleCheckFavorite)
				r.Post("/check-batch", s.handleCheckFavorites)
				if s.authEnabled {
					r.With(requireWriteAccess).Delete("/*", s.handleRemoveFavorite)
					r.With(requireWriteAccess).Patch("/*", s.handleUpdateFavoriteNote)
				} else {
					r.Delete("/*", s.handleRemoveFavorite)
					r.Patch("/*", s.handleUpdateFavoriteNote)
				}
			})
		}

		// File operations
		r.Route("/files", func(r chi.Router) {
			r.Get("/*", s.handleListFiles)
			if s.authEnabled {
				r.With(requireWriteAccess).Post("/*", s.handleUploadFile)
				r.With(requireWriteAccess).Delete("/*", s.handleDeleteFile)
			} else {
				r.Post("/*", s.handleUploadFile)
				r.Delete("/*", s.handleDeleteFile)
			}
		})

		// File operations requiring bodies
		if s.authEnabled {
			r.With(requireWriteAccess).Post("/files-move", s.handleMoveFile)
			r.With(requireWriteAccess).Post("/files-copy", s.handleCopyFile)
		} else {
			r.Post("/files-move", s.handleMoveFile)
			r.Post("/files-copy", s.handleCopyFile)
		}

		// File download/preview (authenticated, no Basic Auth popup)
		r.Get("/download/*", s.handleDownloadFile)

		// Directory operations
		r.Route("/directories", func(r chi.Router) {
			if s.authEnabled {
				r.With(requireWriteAccess).Post("/*", s.handleCreateDirectory)
			} else {
				r.Post("/*", s.handleCreateDirectory)
			}
		})

		// Thumbnail operations
		r.Get("/thumbnails/*", s.handleThumbnail)

		// Version history
		r.Route("/versions", func(r chi.Router) {
			r.Get("/*", s.handleListVersions)
			if s.authEnabled {
				r.With(s.authMw.RequireRole(auth.RoleAdmin)).Post("/{hash}/restore", s.handleRestoreVersion)
				return
			}
			r.Post("/{hash}/restore", s.handleRestoreVersion)
		})

		// Trash/Recycle bin operations
		r.Route("/trash", func(r chi.Router) {
			r.Get("/", s.handleListTrash)
			r.Get("/{id}", s.handleGetTrashItem)
			if s.authEnabled {
				r.With(requireWriteAccess).Delete("/", s.handleEmptyTrash)
				r.With(requireWriteAccess).Post("/{id}/restore", s.handleRestoreFromTrash)
				r.With(requireWriteAccess).Delete("/{id}", s.handleDeleteFromTrash)
			} else {
				r.Delete("/", s.handleEmptyTrash)
				r.Post("/{id}/restore", s.handleRestoreFromTrash)
				r.Delete("/{id}", s.handleDeleteFromTrash)
			}
		})

		// System info
		r.Get("/stats", s.handleStats)
		if s.authEnabled {
			r.With(s.authMw.RequireRole(auth.RoleAdmin)).Get("/diagnostics", s.handleDiagnostics)
			r.With(s.authMw.RequireRole(auth.RoleAdmin)).Get("/diagnostics-export", s.handleDiagnosticsExport)
		} else {
			r.Get("/diagnostics", s.handleDiagnostics)
			r.Get("/diagnostics-export", s.handleDiagnosticsExport)
		}
		r.Get("/metrics", s.handleMetrics)

		// Search
		r.Get("/search", s.handleSearch)

		// Activity log
		r.Route("/activity", func(r chi.Router) {
			r.Get("/", s.handleListActivity)
			r.Get("/stats", s.handleActivityStats)
			if s.authEnabled {
				r.With(s.authMw.RequireRole(auth.RoleAdmin)).Delete("/", s.handleClearActivity)
				return
			}
			r.Delete("/", s.handleClearActivity)
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
	normalized := strings.ReplaceAll(filePath, "\\", "/")

	// Clean the path first
	cleaned := path.Clean("/" + normalized)

	// Reject any path with .. segments while allowing legal names like foo..txt.
	if hasTraversalSegment(normalized) {
		return "", errors.New("invalid path")
	}

	// Reject paths outside root
	if cleaned != "/" && !strings.HasPrefix(cleaned, "/") {
		return "", errors.New("invalid path")
	}

	return cleaned, nil
}

func hasTraversalSegment(filePath string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func pathContainsDescendant(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return targetPath != "/"
	}
	return strings.HasPrefix(targetPath, basePath+"/")
}

var errPathOutsideHomeDir = errors.New("path outside user home directory")
var errWebDAVUsernameMatchesNonAdmin = errors.New("webdav.username must not match a non-admin user when auth is enabled")

func pathWithinBase(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func (s *Server) currentUserHomeDir(ctx context.Context) (string, bool, error) {
	if !s.authEnabled || auth.IsAdmin(ctx) {
		return "", false, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil {
		return "", false, nil
	}

	homeDir, err := validatePath(user.HomeDir)
	if err != nil {
		return "", true, errPathOutsideHomeDir
	}

	return homeDir, true, nil
}

func (s *Server) authorizeUserPath(ctx context.Context, targetPath string) error {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return nil
	}
	if !pathWithinBase(homeDir, targetPath) {
		return errPathOutsideHomeDir
	}
	return nil
}

func (s *Server) filterSearchResultsByHomeDir(ctx context.Context, results []*storage.SearchResult) ([]*storage.SearchResult, error) {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return results, nil
	}

	filtered := make([]*storage.SearchResult, 0, len(results))
	for _, result := range results {
		if result != nil && pathWithinBase(homeDir, result.Path) {
			filtered = append(filtered, result)
		}
	}
	return filtered, nil
}

func (s *Server) filterTrashItemsByHomeDir(ctx context.Context, items []*storage.TrashItem) ([]*storage.TrashItem, error) {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return items, nil
	}

	filtered := make([]*storage.TrashItem, 0, len(items))
	for _, item := range items {
		if item != nil && pathWithinBase(homeDir, item.OriginalPath) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Server) filterFavoritesByHomeDir(ctx context.Context, items []*favorites.Favorite) ([]*favorites.Favorite, error) {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return items, nil
	}

	filtered := make([]*favorites.Favorite, 0, len(items))
	for _, item := range items {
		if item != nil && pathWithinBase(homeDir, item.Path) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Server) filterSharesByHomeDir(ctx context.Context, items []*share.Share) ([]*share.Share, error) {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return items, nil
	}

	filtered := make([]*share.Share, 0, len(items))
	for _, item := range items {
		if item != nil && pathWithinBase(homeDir, item.Path) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// validateHash validates a BLAKE3 hash string (64 hex characters).
func validateHash(hash string) error {
	if len(hash) != 64 {
		return errors.New("invalid hash")
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return errors.New("invalid hash")
		}
	}
	return nil
}

func badRequestInvalidPath(w http.ResponseWriter) {
	BadRequest(w, "invalid path")
}

func badRequestInvalidHash(w http.ResponseWriter) {
	BadRequest(w, "invalid hash")
}

func badRequestInvalidSourcePath(w http.ResponseWriter) {
	BadRequest(w, "invalid source path")
}

func badRequestInvalidDestinationPath(w http.ResponseWriter) {
	BadRequest(w, "invalid destination path")
}

func forbiddenPathOutsideHome(w http.ResponseWriter) {
	Forbidden(w, "path is outside the assigned home directory")
}

func shouldSkipGCObjectByGrace(obj dataplane.ObjectInfo, graceCutoff time.Time) bool {
	if obj.CreatedAt.IsZero() {
		return true
	}

	return obj.CreatedAt.After(graceCutoff)
}

func (s *Server) dataplaneConnectRetries() int {
	cfg := s.currentConfig()
	if cfg == nil || cfg.DataPlane.MaxRetries < 0 {
		return 0
	}
	return cfg.DataPlane.MaxRetries
}

func (s *Server) connectDataplaneClient(parent context.Context, client *dataplane.Client, totalTimeout time.Duration) error {
	if client == nil {
		return fmt.Errorf("dataplane not configured")
	}

	totalCtx, cancelTotal := context.WithTimeout(parent, totalTimeout)
	defer cancelTotal()

	retries := s.dataplaneConnectRetries()
	attempts := retries + 1
	attemptBudget := totalTimeout
	if attempts > 1 {
		attemptBudget = totalTimeout / time.Duration(attempts)
		if attemptBudget <= 0 {
			attemptBudget = totalTimeout
		}
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := totalCtx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		budget := attemptBudget
		if deadline, ok := totalCtx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			if budget > remaining {
				budget = remaining
			}
		}

		attemptCtx, cancelAttempt := context.WithTimeout(totalCtx, budget)
		lastErr = client.Connect(attemptCtx)
		cancelAttempt()
		if lastErr == nil {
			return nil
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return totalCtx.Err()
}

func (s *Server) ensureDataplaneConnected(ctx context.Context, timeout time.Duration) bool {
	if s.dataplane == nil {
		return false
	}
	if s.dataplane.IsConnected() {
		return true
	}

	if err := s.connectDataplaneClient(ctx, s.dataplane, timeout); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to connect to data plane")
		return false
	}

	return true
}

// === Handlers ===

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    time.Since(s.startTime).String(),
	}

	// Reflect dataplane availability in the overall health status when configured.
	if s.dataplane != nil {
		if !s.ensureDataplaneConnected(r.Context(), DefaultHealthCheckTimeout*time.Second) {
			health["status"] = "degraded"
			health["dataplane"] = map[string]any{
				"healthy": false,
				"status":  "unavailable",
			}
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), DefaultHealthCheckTimeout*time.Second)
			defer cancel()
			if dpHealth, err := s.dataplane.Health(ctx); err == nil {
				health["dataplane"] = map[string]any{
					"healthy": dpHealth.Healthy,
					"version": dpHealth.Version,
					"uptime":  dpHealth.UptimeSecs,
				}
				if !dpHealth.Healthy {
					health["status"] = "degraded"
				}
			} else {
				s.logger.Warn().Err(err).Msg("dataplane health check failed")
				health["status"] = "degraded"
				health["dataplane"] = map[string]any{
					"healthy": false,
					"status":  "unavailable",
				}
			}
		}
	}

	if (s.thumbnailConfigured && s.thumbnail == nil) ||
		(s.maintenanceConfigured && s.maintenance == nil) ||
		(s.activityConfigured && s.activity == nil) ||
		s.favoritesConfiguredButUnavailable() {
		health["status"] = "degraded"
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
	return errors.Is(err, storage.ErrAlreadyExists) || errors.Is(err, storage.ErrNotDir)
}

func (s *Server) respondNotFound(w http.ResponseWriter, operation string, err error) {
	s.logger.Debug().Err(err).Str("operation", operation).Msg("resource not found")
	NotFound(w, "resource not found")
}

func (s *Server) respondInternalError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error().Err(err).Str("operation", operation).Msg("request failed")
	InternalError(w, "internal server error")
}

func (s *Server) respondReadableOpenFileError(w http.ResponseWriter, operation string, err error, directoryMessage string) {
	if isStorageNotFound(err) {
		s.respondNotFound(w, operation, err)
		return
	}
	if errors.Is(err, storage.ErrNotDir) {
		Conflict(w, "parent path is not a directory")
		return
	}
	if errors.Is(err, storage.ErrIsDir) {
		BadRequest(w, directoryMessage)
		return
	}
	s.respondInternalError(w, operation, err)
}

func respondPayloadTooLarge(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodePayloadTooLarge, message).Write(w, http.StatusRequestEntityTooLarge)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	filePath := chi.URLParam(r, "*")
	if filePath == "" {
		filePath = "/"
	}

	// REM-5 fix: Validate path to prevent traversal attacks
	filePath, err := validatePath(filePath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Get directory listing
	files, err := s.fs.ReadDir(r.Context(), filePath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "list files", err)
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			BadRequest(w, "path is not a directory")
			return
		}

		s.respondInternalError(w, "list files", err)
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
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, DefaultMaxUploadSize)

	if err := s.fs.WriteFile(r.Context(), filePath, r.Body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) || errors.Is(err, storage.ErrFileTooLarge) {
			respondPayloadTooLarge(w, fmt.Sprintf("file too large (max %d bytes)", DefaultMaxUploadSize))
			return
		}
		if errors.Is(err, storage.ErrIsDir) {
			BadRequest(w, "cannot upload to directory")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		s.respondInternalError(w, "upload file", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionUpload, filePath, nil)

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file uploaded successfully").Write(w, http.StatusCreated)
}

func (s *Server) handleCreateDirectory(w http.ResponseWriter, r *http.Request) {
	dirPath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	dirPath, err := validatePath(dirPath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), dirPath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}
	if info, err := s.fs.Stat(r.Context(), dirPath); err == nil {
		if info.IsDir {
			NewAPIResponse(map[string]any{
				"path": dirPath,
			}).WithMessage("directory already exists").Write(w, http.StatusOK)
			return
		}
		Conflict(w, "resource already exists")
		return
	} else if errors.Is(err, storage.ErrNotDir) {
		Conflict(w, "parent path is not a directory")
		return
	} else if !isStorageNotFound(err) {
		s.respondInternalError(w, "stat directory", err)
		return
	}

	if err := s.fs.Mkdir(r.Context(), dirPath); err != nil {
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		// Check if already exists
		if errors.Is(err, storage.ErrAlreadyExists) {
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
	s.LogActivityWithWarning(w, r, activity.ActionCreate, dirPath, map[string]string{"type": "directory"})

	NewAPIResponse(map[string]any{
		"path": dirPath,
	}).WithMessage("directory created successfully").Write(w, http.StatusCreated)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	if err := s.fs.Delete(r.Context(), filePath); err != nil {
		if errors.Is(err, storage.ErrDirNotEmpty) {
			Conflict(w, "directory not empty")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		// Check if it's a "not found" error
		if isStorageNotFound(err) {
			s.respondNotFound(w, "delete file", err)
			return
		}
		s.respondInternalError(w, "delete file", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionDelete, filePath, nil)

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file deleted successfully").Write(w, http.StatusOK)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
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
			badRequestInvalidHash(w)
			return
		}

		reader, err := s.fs.GetVersion(r.Context(), filePath, versionHash)
		if err != nil {
			if errors.Is(err, versionstore.ErrUnavailable) {
				ServiceUnavailable(w, "version storage unavailable")
				return
			}
			if errors.Is(err, storage.ErrIsDir) {
				BadRequest(w, "cannot download directory")
				return
			}
			if errors.Is(err, storage.ErrNotDir) {
				Conflict(w, "parent path is not a directory")
				return
			}
			if isStorageNotFound(err) {
				s.respondNotFound(w, "download file version", err)
				return
			}
			s.respondInternalError(w, "download file version", err)
			return
		}
		defer reader.Close()

		if forceDownload {
			w.Header().Set("Content-Disposition", formatAttachmentHeader(path.Base(filePath)))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if err := streamAPIResponse(w, reader); err != nil {
			if apiStreamResponseStarted(err) {
				return
			}
			s.respondInternalError(w, "download file version", err)
			return
		}
		return
	}

	// Get file info
	info, err := s.fs.Stat(r.Context(), filePath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "stat file", err)
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
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
		s.respondReadableOpenFileError(w, "open file", err, "cannot download directory")
		return
	}
	defer file.Close()

	// Set cache headers
	if info.ContentHash != "" {
		w.Header().Set("ETag", fmt.Sprintf(`"%s"`, info.ContentHash))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if forceDownload {
		w.Header().Set("Content-Disposition", formatAttachmentHeader(path.Base(filePath)))
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
	if err := decodeJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	fromPath, err := validatePath(req.From)
	if err != nil {
		badRequestInvalidSourcePath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), fromPath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	toPath, err := validatePath(req.To)
	if err != nil {
		badRequestInvalidDestinationPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), toPath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}
	if fromPath == toPath {
		Conflict(w, "source and destination must differ")
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}
	if info, err := s.fs.Stat(r.Context(), fromPath); err == nil {
		if info.IsDir && pathContainsDescendant(fromPath, toPath) {
			Conflict(w, "destination cannot be inside source directory")
			return
		}
	} else if errors.Is(err, storage.ErrNotDir) {
		Conflict(w, "parent path is not a directory")
		return
	} else if !isStorageNotFound(err) {
		s.respondInternalError(w, "stat move source", err)
		return
	}

	if err := s.fs.Rename(r.Context(), fromPath, toPath); err != nil {
		if isStorageConflict(err) {
			if errors.Is(err, storage.ErrNotDir) {
				Conflict(w, "parent path is not a directory")
				return
			}
			Conflict(w, "resource already exists")
			return
		}
		if isStorageNotFound(err) {
			s.respondNotFound(w, "move file", err)
			return
		}
		s.respondInternalError(w, "move file", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionMove, fromPath, map[string]string{"to": toPath})

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
	if err := decodeJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	fromPath, err := validatePath(req.From)
	if err != nil {
		badRequestInvalidSourcePath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), fromPath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	toPath, err := validatePath(req.To)
	if err != nil {
		badRequestInvalidDestinationPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), toPath); err != nil {
		forbiddenPathOutsideHome(w)
		return
	}
	if fromPath == toPath {
		Conflict(w, "source and destination must differ")
		return
	}

	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}

	srcInfo, err := s.fs.Stat(r.Context(), fromPath)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "copy resource", err)
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		s.respondInternalError(w, "stat copy source", err)
		return
	}
	if srcInfo.IsDir && pathContainsDescendant(fromPath, toPath) {
		Conflict(w, "destination cannot be inside source directory")
		return
	}

	if _, err := s.fs.Stat(r.Context(), toPath); err == nil {
		Conflict(w, "resource already exists")
		return
	} else if errors.Is(err, storage.ErrNotDir) {
		Conflict(w, "parent path is not a directory")
		return
	} else if !isStorageNotFound(err) {
		s.respondInternalError(w, "stat copy destination", err)
		return
	}
	if err := s.ensureCopyDestinationParent(r.Context(), toPath); err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "copy resource", err)
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		s.respondInternalError(w, "stat copy destination parent", err)
		return
	}
	if err := s.copyResource(r.Context(), fromPath, toPath); err != nil {
		if isStorageConflict(err) {
			if errors.Is(err, storage.ErrNotDir) {
				Conflict(w, "parent path is not a directory")
				return
			}
			Conflict(w, "resource already exists")
			return
		}
		if isStorageNotFound(err) {
			s.respondNotFound(w, "copy resource", err)
			return
		}
		s.respondInternalError(w, "copy resource", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionCopy, fromPath, map[string]string{"to": toPath})

	NewAPIResponse(map[string]any{
		"from": fromPath,
		"to":   toPath,
	}).WithMessage("resource copied successfully").Write(w, http.StatusCreated)
}

func (s *Server) ensureCopyDestinationParent(ctx context.Context, targetPath string) error {
	parentPath := path.Dir(targetPath)
	info, err := s.fs.Stat(ctx, parentPath)
	if err != nil {
		return err
	}
	if !info.IsDir {
		return storage.ErrNotDir
	}
	return nil
}

func (s *Server) copyResource(ctx context.Context, srcPath, dstPath string) error {
	info, err := s.fs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		if err := s.fs.Mkdir(ctx, dstPath); err != nil {
			return err
		}

		children, err := s.fs.ReadDir(ctx, srcPath)
		if err != nil {
			return s.rollbackCopiedDirectory(dstPath, err)
		}

		for _, child := range children {
			childName := path.Base(child.Path)
			if err := s.copyResource(ctx, child.Path, path.Join(dstPath, childName)); err != nil {
				return s.rollbackCopiedDirectory(dstPath, err)
			}
		}
		return nil
	}

	reader, err := s.fs.OpenFile(ctx, srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	return s.fs.WriteFile(ctx, dstPath, reader)
}

func (s *Server) rollbackCopiedDirectory(dstPath string, copyErr error) error {
	if rollbackErr := s.removeCopiedTree(context.Background(), dstPath); rollbackErr != nil && !errors.Is(rollbackErr, storage.ErrNotFound) {
		return errors.Join(copyErr, fmt.Errorf("rollback copied directory %s: %w", dstPath, rollbackErr))
	}
	return copyErr
}

func (s *Server) removeCopiedTree(ctx context.Context, targetPath string) error {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		return err
	}

	if info.IsDir {
		children, err := s.fs.ReadDir(ctx, targetPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := s.removeCopiedTree(ctx, child.Path); err != nil {
				return err
			}
		}
	}

	return s.fs.PermanentDelete(ctx, targetPath)
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	filePath := "/" + chi.URLParam(r, "*")

	// REM-5 fix: Validate path
	filePath, err := validatePath(filePath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
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
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		if isStorageNotFound(err) {
			s.respondNotFound(w, "list versions", err)
			return
		}

		s.respondInternalError(w, "list versions", err)
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
		badRequestInvalidHash(w)
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
		badRequestInvalidPath(w)
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
		if errors.Is(err, versionstore.ErrUnavailable) {
			ServiceUnavailable(w, "version storage unavailable")
			return
		}
		if errors.Is(err, storage.ErrIsDir) {
			BadRequest(w, "cannot restore version for directory")
			return
		}
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		if isStorageNotFound(err) {
			s.respondNotFound(w, "restore version", err)
			return
		}
		s.respondInternalError(w, "restore version", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionRestore, filePath, map[string]string{"hash": hash})

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
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 || l > 100 {
			BadRequest(w, "limit parameter must be between 1 and 100")
			return
		}
		limit = l
	}

	results, err := s.fs.Search(r.Context(), query, limit)
	if err != nil {
		s.respondInternalError(w, "search files", err)
		return
	}
	results, err = s.filterSearchResultsByHomeDir(r.Context(), results)
	if err != nil {
		forbiddenPathOutsideHome(w)
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
	stats := map[string]any{}

	if s.fs != nil {
		if count, err := getFileCount(s.fs, r.Context()); err == nil {
			stats["total_files"] = count
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect file count for stats")
		}
	}

	// Get stats from data plane if connected
	if s.ensureDataplaneConnected(r.Context(), DefaultStatsTimeout*time.Second) {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		defer cancel()
		if dpStats, err := s.dataplane.Stats(ctx); err == nil {
			stats["total_chunks"] = dpStats.TotalChunks
			stats["total_size"] = dpStats.TotalSize
			stats["unique_size"] = dpStats.UniqueSize
			stats["dedup_ratio"] = dpStats.DedupRatio
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect dataplane stats")
		}
	}

	NewAPIResponse(stats).Write(w, http.StatusOK)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	dataplaneConnected := s.ensureDataplaneConnected(r.Context(), DefaultStatsTimeout*time.Second)

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
	systemStatus := map[string]any{
		"filesystem_initialized":    s.fs != nil,
		"dataplane_connected":       dataplaneConnected,
		"thumbnail_service_ready":   s.thumbnail != nil,
		"maintenance_history_ready": s.maintenance != nil,
		"activity_log_ready":        s.activity != nil,
	}
	if s.favoritesConfigured {
		systemStatus["favorites_store_ready"] = !s.favoritesConfiguredButUnavailable()
	}
	diag["system"] = systemStatus

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
		if trashCount, trashSize, err := getTrashStats(s.fs, r.Context()); err == nil {
			fsStats["trash_items"] = trashCount
			fsStats["trash_size"] = trashSize
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect trash stats for diagnostics")
		}

		diag["filesystem"] = fsStats
	}

	// Data plane stats
	if dataplaneConnected {
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
	// M3 fix: Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, DefaultScrubRequestBodyLimit)

	// Parse optional hashes from request body
	var req struct {
		Hashes []string `json:"hashes,omitempty"`
	}
	hasBody, err := requestHasBody(r)
	if err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if hasBody {
		if err := decodeJSONBodyWithLimit(r, &req, DefaultScrubRequestBodyLimit); err != nil {
			writeLimitedJSONBodyError(w, err, DefaultScrubRequestBodyLimit)
			return
		}
	}

	if !s.ensureDataplaneConnected(r.Context(), DefaultDataplaneConnectTimeout*time.Second) {
		ServiceUnavailable(w, "dataplane not connected")
		return
	}

	// Check if scrub is already running
	if s.maintenance != nil && s.maintenance.ScrubIsRunning() {
		Conflict(w, "scrub is already running")
		return
	}

	// Mark scrub as started
	var scrubRecord *maintenance.ScrubResult
	if s.maintenance != nil {
		var err error
		scrubRecord, err = s.maintenance.StartScrub()
		if err != nil {
			s.respondInternalError(w, "persist scrub start", err)
			return
		}
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
			scrubRecord.ErrorMessage = scrubFailurePublicMessage
			scrubRecord.DurationMs = uint64(time.Since(scrubRecord.StartTime).Milliseconds())
			if saveErr := saveScrubResult(s.maintenance, scrubRecord); saveErr != nil {
				s.logger.Error().Err(saveErr).Msg("failed to persist failed scrub result")
			}
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
		if err := saveScrubResult(s.maintenance, scrubRecord); err != nil {
			s.respondInternalError(w, "persist scrub result", err)
			return
		}
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

	if !s.ensureDataplaneConnected(r.Context(), DefaultDataplaneConnectTimeout*time.Second) {
		ServiceUnavailable(w, "dataplane not connected")
		return
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
	gracePeriod := 24 * time.Hour
	if gpStr := r.URL.Query().Get("grace_period_hours"); gpStr != "" {
		hours, err := strconv.Atoi(gpStr)
		if err != nil || hours < 0 {
			BadRequest(w, "grace_period_hours must be a non-negative integer")
			return
		}
		gracePeriod = time.Duration(hours) * time.Hour
	}

	if !s.ensureDataplaneConnected(r.Context(), DefaultDataplaneConnectTimeout*time.Second) {
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
	graceCutoff := time.Now().Add(-gracePeriod) // NEW-2 fix: actual cutoff time

	// Step 1: Block storage mutations for the duration of GC and snapshot referenced hashes.
	referencedHashes, releaseGCLock, err := s.fs.AcquireGCLock(ctx)
	if err != nil {
		s.respondInternalError(w, "gc referenced hashes", err)
		return
	}
	defer releaseGCLock()

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
			// Keep grace protection active even when the dataplane could not provide a timestamp.
			if shouldSkipGCObjectByGrace(obj, graceCutoff) {
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
	deleteFailures := make([]map[string]any, 0)
	if !dryRun {
		for _, hash := range unreferenced {
			deleted, err := deleteGCChunk(s.dataplane, ctx, hash)
			if err != nil {
				s.logger.Warn().Err(err).Str("hash", hash).Msg("failed to delete chunk")
				deleteFailures = append(deleteFailures, map[string]any{
					"hash":    hash,
					"message": "failed to delete chunk",
				})
				continue
			}
			if deleted {
				deletedCount++
			}
		}
	}

	resp := map[string]any{
		"dry_run":            dryRun,
		"grace_period_hours": int(gracePeriod.Hours()),
		"total_objects":      len(allObjects),
		"referenced":         len(referencedHashes),
		"unreferenced":       len(unreferenced),
		"unreferenced_size":  unreferencedSize,
		"skipped_by_grace":   skippedByGrace,
		"deleted_count":      deletedCount,
	}
	if !dryRun {
		resp["failed_count"] = len(deleteFailures)
		resp["delete_failures"] = deleteFailures
	}

	NewAPIResponse(resp).Write(w, http.StatusOK)
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
		"error_message":     sanitizeScrubErrorMessage(result.ErrorMessage),
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
		if trashCount, trashSize, err := getTrashStats(s.fs, r.Context()); err == nil {
			fsStats["trash_count"] = trashCount
			fsStats["trash_size"] = trashSize
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect trash stats for diagnostics export")
		}

		export["filesystem"] = fsStats
	}

	// Dataplane stats (sanitized)
	if s.ensureDataplaneConnected(r.Context(), DefaultStatsTimeout*time.Second) {
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
			if scrubError := sanitizeScrubErrorMessage(result.ErrorMessage); scrubError != "" {
				scrubInfo["error_message"] = scrubError
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

func sanitizeScrubErrorMessage(message string) string {
	if message == "" {
		return ""
	}
	return scrubFailurePublicMessage
}

func formatAttachmentHeader(filename string) string {
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); value != "" {
		return value
	}
	return "attachment"
}

func streamAPIResponse(w http.ResponseWriter, reader io.Reader) error {
	trackingWriter := &apiDownloadResponseWriter{ResponseWriter: w}
	_, err := io.Copy(trackingWriter, reader)
	if err != nil {
		return &apiStreamResponseError{err: err, responseStarted: trackingWriter.started}
	}
	return nil
}

type apiStreamResponseError struct {
	err             error
	responseStarted bool
}

func (e *apiStreamResponseError) Error() string {
	return e.err.Error()
}

func (e *apiStreamResponseError) Unwrap() error {
	return e.err
}

func apiStreamResponseStarted(err error) bool {
	var streamErr *apiStreamResponseError
	return errors.As(err, &streamErr) && streamErr.responseStarted
}

type apiDownloadResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *apiDownloadResponseWriter) WriteHeader(statusCode int) {
	w.started = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *apiDownloadResponseWriter) Write(p []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(p)
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
	payload, err := json.Marshal(data)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
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
	items, err = s.filterTrashItemsByHomeDir(r.Context(), items)
	if err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	count := len(items)
	var totalSize int64
	for _, item := range items {
		totalSize += item.Size
	}

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
	if cfg := s.currentConfig(); cfg != nil {
		response["retentionDays"] = cfg.Storage.Trash.RetentionDays
		response["retentionEnabled"] = cfg.Storage.Trash.Enabled
		response["retentionMaxSize"] = cfg.Storage.Trash.MaxSize
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
		if isStorageNotFound(err) {
			s.respondNotFound(w, "get trash item", err)
			return
		}

		s.respondInternalError(w, "get trash item", err)
		return
	}
	if err := s.authorizeUserPath(r.Context(), item.OriginalPath); err != nil {
		s.respondNotFound(w, "get trash item", storage.ErrNotFound)
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
	item, err := s.fs.GetTrashItem(r.Context(), id)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "restore from trash", err)
			return
		}

		s.respondInternalError(w, "restore from trash", err)
		return
	}
	if err := s.authorizeUserPath(r.Context(), item.OriginalPath); err != nil {
		s.respondNotFound(w, "restore from trash", storage.ErrNotFound)
		return
	}

	// Check if custom restore path is provided
	newPath := r.URL.Query().Get("path")
	activityPath := s.lookupTrashOriginalPath(r.Context(), id)

	if newPath != "" {
		// Restore to custom path
		newPath, err = validatePath(newPath)
		if err != nil {
			badRequestInvalidPath(w)
			return
		}
		if err := s.authorizeUserPath(r.Context(), newPath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
		activityPath = newPath
		err = s.fs.RestoreFromTrashTo(r.Context(), id, newPath)
	} else {
		// Restore to original path
		err = s.fs.RestoreFromTrash(r.Context(), id)
	}

	if err != nil {
		if errors.Is(err, storage.ErrNotDir) {
			Conflict(w, "parent path is not a directory")
			return
		}
		if errors.Is(err, storage.ErrAlreadyExists) {
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
	s.LogActivityWithWarning(w, r, activity.ActionTrashRestore, activityPath, nil)

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
	item, err := s.fs.GetTrashItem(r.Context(), id)
	if err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "delete from trash", err)
			return
		}

		s.respondInternalError(w, "delete from trash", err)
		return
	}
	if err := s.authorizeUserPath(r.Context(), item.OriginalPath); err != nil {
		s.respondNotFound(w, "delete from trash", storage.ErrNotFound)
		return
	}
	activityPath := s.lookupTrashOriginalPath(r.Context(), id)

	if err := s.fs.DeleteFromTrash(r.Context(), id); err != nil {
		if isStorageNotFound(err) {
			s.respondNotFound(w, "delete from trash", err)
			return
		}

		s.respondInternalError(w, "delete from trash", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionTrashDelete, activityPath, nil)

	NewAPIResponse(map[string]any{
		"id":      id,
		"deleted": true,
	}).WithMessage("item permanently deleted").Write(w, http.StatusOK)
}

func (s *Server) lookupTrashOriginalPath(ctx context.Context, id string) string {
	if s.fs == nil || id == "" {
		return ""
	}

	items, err := s.fs.ListTrash(ctx)
	if err != nil {
		return ""
	}
	for _, item := range items {
		if item.ID == id {
			return item.OriginalPath
		}
	}

	return ""
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if s.fs == nil {
		ServiceUnavailable(w, "filesystem not initialized")
		return
	}
	homeDir, scoped, err := s.currentUserHomeDir(r.Context())
	if err != nil {
		forbiddenPathOutsideHome(w)
		return
	}
	if scoped {
		items, err := s.fs.ListTrash(r.Context())
		if err != nil {
			s.respondInternalError(w, "empty trash", err)
			return
		}

		deletedCount := 0
		for _, item := range items {
			if item == nil || !pathWithinBase(homeDir, item.OriginalPath) {
				continue
			}
			if err := s.fs.DeleteFromTrash(r.Context(), item.ID); err != nil {
				if deletedCount > 0 {
					s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", map[string]string{
						"count":   strconv.Itoa(deletedCount),
						"partial": "true",
					})
					NewAPIResponse(map[string]any{
						"deleted_count": deletedCount,
						"partial":       true,
					}).WithMessage("trash emptied partially").Write(w, http.StatusOK)
					return
				}
				s.respondInternalError(w, "empty trash", err)
				return
			}
			deletedCount++
		}

		s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", map[string]string{"count": strconv.Itoa(deletedCount)})
		NewAPIResponse(map[string]any{
			"deleted_count": deletedCount,
			"partial":       false,
		}).WithMessage("trash emptied successfully").Write(w, http.StatusOK)
		return
	}

	count, err := s.fs.EmptyTrash(r.Context())
	if err != nil {
		if count > 0 {
			s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", map[string]string{
				"count":   strconv.Itoa(count),
				"partial": "true",
			})
			NewAPIResponse(map[string]any{
				"deleted_count": count,
				"partial":       true,
			}).WithMessage("trash emptied partially").Write(w, http.StatusOK)
			return
		}
		s.respondInternalError(w, "empty trash", err)
		return
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", map[string]string{"count": strconv.Itoa(count)})

	NewAPIResponse(map[string]any{
		"deleted_count": count,
		"partial":       false,
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
		badRequestInvalidPath(w)
		return
	}
	if err := s.authorizeUserPath(r.Context(), filePath); err != nil {
		forbiddenPathOutsideHome(w)
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
		s.respondReadableOpenFileError(w, "thumbnail open file", err, "path is a directory")
		return
	}
	defer reader.Close()

	// Generate or retrieve cached thumbnail
	data, err := s.thumbnail.GetThumbnail(r.Context(), filePath, size, reader)
	if err != nil {
		if errors.Is(err, thumbnail.ErrThumbnailSourceTooLarge) {
			BadRequest(w, "source image too large to thumbnail")
			return
		}
		s.respondInternalError(w, "generate thumbnail", err)
		return
	}

	// Set appropriate headers
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleListActivity returns recent activity log entries
func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	actionFilter := r.URL.Query().Get("action")
	userFilter := r.URL.Query().Get("user")
	if currentUserFilter := s.currentActivityUserFilter(r); currentUserFilter != "" {
		userFilter = currentUserFilter
	}

	limit := 50
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 || l > 500 {
			BadRequest(w, "limit parameter must be between 1 and 500")
			return
		}
		limit = l
	}

	offset := 0
	if offsetStr != "" {
		o, err := strconv.Atoi(offsetStr)
		if err != nil || o < 0 {
			BadRequest(w, "offset parameter must be a non-negative integer")
			return
		}
		offset = o
	}

	if s.activityConfiguredButUnavailable() {
		ServiceUnavailable(w, "activity log unavailable")
		return
	}

	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"items":  []any{},
			"total":  0,
			"limit":  limit,
			"offset": offset,
		}).Write(w, http.StatusOK)
		return
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
	if s.activityConfiguredButUnavailable() {
		ServiceUnavailable(w, "activity log unavailable")
		return
	}

	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"total":     0,
			"today":     0,
			"by_action": map[string]int{},
			"by_user":   map[string]int{},
		}).Write(w, http.StatusOK)
		return
	}

	if currentUserFilter := s.currentActivityUserFilter(r); currentUserFilter != "" {
		entries, _ := s.activity.List(s.activity.Count(), 0, "", currentUserFilter)
		NewAPIResponse(buildActivityStats(entries)).Write(w, http.StatusOK)
		return
	}

	stats := s.activity.Statistics()
	NewAPIResponse(stats).Write(w, http.StatusOK)
}

func (s *Server) currentActivityUserFilter(r *http.Request) string {
	if !s.authEnabled || auth.IsAdmin(r.Context()) {
		return ""
	}
	claims := auth.GetClaimsFromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.Username
}

func buildActivityStats(entries []activity.Entry) map[string]any {
	stats := map[string]any{
		"total":     len(entries),
		"by_action": map[activity.ActionType]int{},
		"by_user":   map[string]int{},
	}

	actionCounts := make(map[activity.ActionType]int)
	userCounts := make(map[string]int)
	today := time.Now().Truncate(24 * time.Hour)
	todayCount := 0

	for _, entry := range entries {
		actionCounts[entry.Action]++
		if entry.User != "" {
			userCounts[entry.User]++
		}
		if entry.Timestamp.After(today) {
			todayCount++
		}
	}

	stats["today"] = todayCount
	stats["by_action"] = actionCounts
	stats["by_user"] = userCounts
	return stats
}

func (s *Server) activityConfiguredButUnavailable() bool {
	return s.activity == nil && s.activityConfigured
}

// handleClearActivity clears all activity log entries
func (s *Server) handleClearActivity(w http.ResponseWriter, r *http.Request) {
	if s.activityConfiguredButUnavailable() {
		ServiceUnavailable(w, "activity log unavailable")
		return
	}

	if s.activity == nil {
		NewAPIResponse(map[string]any{
			"message": "Activity log not configured",
		}).Write(w, http.StatusOK)
		return
	}

	if err := s.activity.Clear(); err != nil {
		s.respondInternalError(w, "clear activity log", err)
		return
	}

	NewAPIResponse(map[string]any{
		"message": "Activity log cleared",
	}).Write(w, http.StatusOK)
}

// LogActivity is a helper to log user activity
func (s *Server) LogActivity(r *http.Request, action activity.ActionType, path string, details map[string]string) {
	if err := s.logActivity(r, action, path, details); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to log activity")
	}
}

func (s *Server) LogActivityWithWarning(w http.ResponseWriter, r *http.Request, action activity.ActionType, path string, details map[string]string) {
	if err := s.logActivity(r, action, path, details); err != nil {
		markAuditFailureHeaders(w)
		s.logger.Warn().Err(err).Msg("Failed to log activity")
	}
}

func (s *Server) logActivity(r *http.Request, action activity.ActionType, path string, details map[string]string) error {
	user := "anonymous"
	if s.authEnabled {
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}
	}

	ip := requestip.ClientIP(r)
	return s.logActivityEntry(action, path, user, ip, details)
}

func (s *Server) logActivityEntry(action activity.ActionType, path, user, ip string, details map[string]string) error {
	if s.activity == nil {
		return nil
	}
	return s.activity.Log(action, path, user, ip, details)
}

func markAuditFailureHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	if headers.Get(auditStatusHeaderName) == "" {
		headers.Set(auditStatusHeaderName, auditStatusFailedValue)
	}
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == auditWarningHeader {
			return
		}
	}
	headers.Add("Warning", auditWarningHeader)
}

// handleLoginWithActivity wraps auth login to log activity
func (s *Server) handleLoginWithActivity(w http.ResponseWriter, r *http.Request) {
	user := "unknown"
	if s.activity != nil {
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		} else {
			loginUser, err := readLoginUsername(r)
			if err != nil {
				writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
				return
			}
			if loginUser != "" {
				user = loginUser
			}
		}
	}

	rec := newBufferedResponseRecorder()
	s.authHandler.HandleLogin(rec, r)

	if rec.statusCode == http.StatusOK && s.activity != nil {
		ip := requestip.ClientIP(r)
		if err := s.logActivityEntry(activity.ActionLogin, "", user, ip, nil); err != nil {
			markAuditFailureHeaders(rec)
			s.logger.Warn().Err(err).Msg("Failed to log activity")
		}
	}

	rec.FlushTo(w)
}

func readLoginUsername(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}

	body, err := readBufferedRequestBody(r, DefaultJSONRequestBodyLimit)
	if err != nil {
		return "", err
	}

	var req auth.LoginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}

	return strings.TrimSpace(req.Username), nil
}

// handleLogoutWithActivity wraps auth logout to log activity
func (s *Server) handleLogoutWithActivity(w http.ResponseWriter, r *http.Request) {
	user := "unknown"
	if s.activity != nil {
		if claims := auth.GetClaimsFromContext(r.Context()); claims != nil {
			user = claims.Username
		}
	}
	ip := requestip.ClientIP(r)

	rec := newBufferedResponseRecorder()
	s.authHandler.HandleLogout(rec, r)
	if rec.statusCode >= http.StatusOK && rec.statusCode < http.StatusMultipleChoices && s.activity != nil {
		if err := s.logActivityEntry(activity.ActionLogout, "", user, ip, nil); err != nil {
			markAuditFailureHeaders(rec)
			s.logger.Warn().Err(err).Msg("Failed to log activity")
		}
	}
	rec.FlushTo(w)
}

// handleGetSettings returns current settings
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil {
		ServiceUnavailable(w, "settings not available")
		return
	}

	settings := map[string]interface{}{
		"server": map[string]interface{}{
			"host":          cfg.Server.Host,
			"port":          cfg.Server.Port,
			"read_timeout":  cfg.Server.ReadTimeout.String(),
			"write_timeout": cfg.Server.WriteTimeout.String(),
			"idle_timeout":  cfg.Server.IdleTimeout.String(),
			"tls": map[string]interface{}{
				"enabled":       cfg.Server.TLS.Enabled,
				"cert_file":     cfg.Server.TLS.CertFile,
				"key_file":      cfg.Server.TLS.KeyFile,
				"auto_generate": cfg.Server.TLS.AutoGenerate,
				"cert_dir":      cfg.Server.TLS.CertDir,
			},
		},
		"storage": map[string]interface{}{
			"root": cfg.Storage.Root,
		},
		"trash": map[string]interface{}{
			"enabled":        cfg.Storage.Trash.Enabled,
			"retention_days": cfg.Storage.Trash.RetentionDays,
			"max_size":       cfg.Storage.Trash.MaxSize,
		},
		"retention": map[string]interface{}{
			"max_versions":   cfg.Storage.Retention.MaxVersions,
			"max_age":        formatSettingsDuration(cfg.Storage.Retention.MaxAge),
			"min_free_space": cfg.Storage.Retention.MinFreeSpace,
			"gc_interval":    formatSettingsDuration(cfg.Storage.Retention.GCInterval),
		},
		"versioning": map[string]interface{}{
			"auto_versioned_extensions": cfg.Storage.Versioning.AutoVersionedExtensions,
			"auto_versioned_filenames":  cfg.Storage.Versioning.AutoVersionedFilenames,
			"max_versioned_size":        cfg.Storage.Versioning.MaxVersionedSize,
		},
		"webdav": map[string]interface{}{
			"enabled":   cfg.WebDAV.Enabled,
			"prefix":    cfg.WebDAV.Prefix,
			"read_only": cfg.WebDAV.ReadOnly,
			"auth_type": cfg.WebDAV.AuthType,
			"username":  cfg.WebDAV.Username,
		},
		"share": map[string]interface{}{
			"enabled":  cfg.Share.Enabled,
			"base_url": cfg.Share.BaseURL,
		},
		"favorites": map[string]interface{}{
			"enabled": cfg.Favorites.Enabled,
		},
		"alerts": map[string]interface{}{
			"enabled":         cfg.Alerts.Enabled,
			"check_interval":  cfg.Alerts.CheckInterval.String(),
			"threshold_pct":   cfg.Alerts.ThresholdPct,
			"critical_pct":    cfg.Alerts.CriticalPct,
			"min_free_bytes":  cfg.Alerts.MinFreeBytes,
			"cooldown_period": cfg.Alerts.CooldownPeriod.String(),
			"webhook_url":     cfg.Alerts.WebhookURL,
			"webhook_method":  cfg.Alerts.WebhookMethod,
			"webhook_headers": cfg.Alerts.WebhookHeaders,
		},
		"dataplane": map[string]interface{}{
			"grpc_address": cfg.DataPlane.GRPCAddress,
			"timeout":      cfg.DataPlane.Timeout.String(),
			"max_retries":  cfg.DataPlane.MaxRetries,
		},
		"cdc": map[string]interface{}{
			"min_chunk_size": cfg.DataPlane.CDC.MinChunkSize,
			"avg_chunk_size": cfg.DataPlane.CDC.AvgChunkSize,
			"max_chunk_size": cfg.DataPlane.CDC.MaxChunkSize,
		},
	}

	NewAPIResponse(settings).Write(w, http.StatusOK)
}

// UpdateSettingsRequest represents settings update request
type UpdateSettingsRequest struct {
	Server     *ServerSettingsUpdate     `json:"server,omitempty"`
	Trash      *TrashSettingsUpdate      `json:"trash,omitempty"`
	Retention  *RetentionSettingsUpdate  `json:"retention,omitempty"`
	Versioning *VersioningSettingsUpdate `json:"versioning,omitempty"`
	DataPlane  *DataPlaneSettingsUpdate  `json:"dataplane,omitempty"`
	CDC        *CDCSettingsUpdate        `json:"cdc,omitempty"`
	Share      *ShareSettingsUpdate      `json:"share,omitempty"`
	Favorites  *FavoritesSettingsUpdate  `json:"favorites,omitempty"`
	Alerts     *AlertsSettingsUpdate     `json:"alerts,omitempty"`
	WebDAV     *WebDAVSettingsUpdate     `json:"webdav,omitempty"`
}

type ServerSettingsUpdate struct {
	Host         *string                  `json:"host,omitempty"`
	Port         *int                     `json:"port,omitempty"`
	ReadTimeout  *string                  `json:"read_timeout,omitempty"`
	WriteTimeout *string                  `json:"write_timeout,omitempty"`
	IdleTimeout  *string                  `json:"idle_timeout,omitempty"`
	TLS          *ServerTLSSettingsUpdate `json:"tls,omitempty"`
}

type ServerTLSSettingsUpdate struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	CertFile     *string `json:"cert_file,omitempty"`
	KeyFile      *string `json:"key_file,omitempty"`
	AutoGenerate *bool   `json:"auto_generate,omitempty"`
	CertDir      *string `json:"cert_dir,omitempty"`
}

type RetentionSettingsUpdate struct {
	MaxVersions  *int    `json:"max_versions,omitempty"`
	MaxAge       *string `json:"max_age,omitempty"`
	MinFreeSpace *uint64 `json:"min_free_space,omitempty"`
	GCInterval   *string `json:"gc_interval,omitempty"`
}

type VersioningSettingsUpdate struct {
	AutoVersionedExtensions *[]string `json:"auto_versioned_extensions,omitempty"`
	AutoVersionedFilenames  *[]string `json:"auto_versioned_filenames,omitempty"`
	MaxVersionedSize        *int64    `json:"max_versioned_size,omitempty"`
}

type TrashSettingsUpdate struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	RetentionDays *int   `json:"retention_days,omitempty"`
	MaxSize       *int64 `json:"max_size,omitempty"`
}

type DataPlaneSettingsUpdate struct {
	GRPCAddress *string `json:"grpc_address,omitempty"`
	Timeout     *string `json:"timeout,omitempty"`
	MaxRetries  *int    `json:"max_retries,omitempty"`
}

type ShareSettingsUpdate struct {
	Enabled *bool   `json:"enabled,omitempty"`
	BaseURL *string `json:"base_url,omitempty"`
}

type FavoritesSettingsUpdate struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type AlertsSettingsUpdate struct {
	Enabled        *bool     `json:"enabled,omitempty"`
	CheckInterval  *string   `json:"check_interval,omitempty"`
	ThresholdPct   *float64  `json:"threshold_pct,omitempty"`
	CriticalPct    *float64  `json:"critical_pct,omitempty"`
	MinFreeBytes   *uint64   `json:"min_free_bytes,omitempty"`
	CooldownPeriod *string   `json:"cooldown_period,omitempty"`
	WebhookURL     *string   `json:"webhook_url,omitempty"`
	WebhookMethod  *string   `json:"webhook_method,omitempty"`
	WebhookHeaders *[]string `json:"webhook_headers,omitempty"`
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

func (s *Server) validateWebDAVIdentity(cfg config.Config) error {
	if !s.authEnabled || s.userStore == nil || !cfg.WebDAV.Enabled || !strings.EqualFold(cfg.WebDAV.AuthType, "basic") {
		return nil
	}

	username := strings.TrimSpace(cfg.WebDAV.Username)
	if username == "" {
		return nil
	}

	user, err := s.userStore.GetByUsername(username)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil
		}
		return err
	}
	if user.Role != auth.RoleAdmin {
		return errWebDAVUsernameMatchesNonAdmin
	}

	return nil
}

// handleUpdateSettings updates settings
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	currentConfig := s.currentConfig()
	if currentConfig == nil || s.configPath == "" {
		ServiceUnavailable(w, "settings not available or no config file path")
		return
	}

	var req UpdateSettingsRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	updatedConfig := *currentConfig

	// Apply updates
	if req.Server != nil {
		if req.Server.Host != nil {
			updatedConfig.Server.Host = *req.Server.Host
		}
		if req.Server.Port != nil {
			if *req.Server.Port < 1 || *req.Server.Port > 65535 {
				BadRequest(w, "invalid server.port")
				return
			}
			updatedConfig.Server.Port = *req.Server.Port
		}
		if req.Server.ReadTimeout != nil {
			d, err := time.ParseDuration(*req.Server.ReadTimeout)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid server.read_timeout")
				return
			}
			updatedConfig.Server.ReadTimeout = d
		}
		if req.Server.WriteTimeout != nil {
			d, err := time.ParseDuration(*req.Server.WriteTimeout)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid server.write_timeout")
				return
			}
			updatedConfig.Server.WriteTimeout = d
		}
		if req.Server.IdleTimeout != nil {
			d, err := time.ParseDuration(*req.Server.IdleTimeout)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid server.idle_timeout")
				return
			}
			updatedConfig.Server.IdleTimeout = d
		}
		if req.Server.TLS != nil {
			if req.Server.TLS.Enabled != nil {
				updatedConfig.Server.TLS.Enabled = *req.Server.TLS.Enabled
			}
			if req.Server.TLS.CertFile != nil {
				updatedConfig.Server.TLS.CertFile = *req.Server.TLS.CertFile
			}
			if req.Server.TLS.KeyFile != nil {
				updatedConfig.Server.TLS.KeyFile = *req.Server.TLS.KeyFile
			}
			if req.Server.TLS.AutoGenerate != nil {
				updatedConfig.Server.TLS.AutoGenerate = *req.Server.TLS.AutoGenerate
			}
			if req.Server.TLS.CertDir != nil {
				updatedConfig.Server.TLS.CertDir = *req.Server.TLS.CertDir
			}
		}
	}

	if req.Retention != nil {
		if req.Retention.MaxVersions != nil {
			updatedConfig.Storage.Retention.MaxVersions = *req.Retention.MaxVersions
		}
		if req.Retention.MaxAge != nil {
			d, err := time.ParseDuration(*req.Retention.MaxAge)
			if err != nil || d < 0 {
				BadRequest(w, "invalid retention.max_age")
				return
			}
			updatedConfig.Storage.Retention.MaxAge = d
		}
		if req.Retention.MinFreeSpace != nil {
			updatedConfig.Storage.Retention.MinFreeSpace = *req.Retention.MinFreeSpace
		}
		if req.Retention.GCInterval != nil {
			d, err := time.ParseDuration(*req.Retention.GCInterval)
			if err != nil || d < 0 {
				BadRequest(w, "invalid retention.gc_interval")
				return
			}
			updatedConfig.Storage.Retention.GCInterval = d
		}
	}

	if req.Versioning != nil {
		if req.Versioning.AutoVersionedExtensions != nil {
			cleaned := make([]string, 0, len(*req.Versioning.AutoVersionedExtensions))
			for _, ext := range *req.Versioning.AutoVersionedExtensions {
				trimmed := strings.TrimSpace(ext)
				if trimmed == "" {
					continue
				}
				cleaned = append(cleaned, trimmed)
			}
			updatedConfig.Storage.Versioning.AutoVersionedExtensions = cleaned
		}
		if req.Versioning.AutoVersionedFilenames != nil {
			cleaned := make([]string, 0, len(*req.Versioning.AutoVersionedFilenames))
			for _, name := range *req.Versioning.AutoVersionedFilenames {
				trimmed := strings.TrimSpace(name)
				if trimmed == "" {
					continue
				}
				cleaned = append(cleaned, trimmed)
			}
			updatedConfig.Storage.Versioning.AutoVersionedFilenames = cleaned
		}
		if req.Versioning.MaxVersionedSize != nil {
			if *req.Versioning.MaxVersionedSize <= 0 {
				BadRequest(w, "invalid versioning.max_versioned_size")
				return
			}
			updatedConfig.Storage.Versioning.MaxVersionedSize = *req.Versioning.MaxVersionedSize
		}
	}

	if req.Trash != nil {
		if req.Trash.Enabled != nil {
			updatedConfig.Storage.Trash.Enabled = *req.Trash.Enabled
		}
		if req.Trash.RetentionDays != nil {
			if *req.Trash.RetentionDays < 0 {
				BadRequest(w, "invalid trash.retention_days")
				return
			}
			updatedConfig.Storage.Trash.RetentionDays = *req.Trash.RetentionDays
		}
		if req.Trash.MaxSize != nil {
			if *req.Trash.MaxSize <= 0 {
				BadRequest(w, "invalid trash.max_size")
				return
			}
			updatedConfig.Storage.Trash.MaxSize = *req.Trash.MaxSize
		}
	}

	if req.DataPlane != nil {
		if req.DataPlane.GRPCAddress != nil {
			updatedConfig.DataPlane.GRPCAddress = *req.DataPlane.GRPCAddress
		}
		if req.DataPlane.Timeout != nil {
			d, err := time.ParseDuration(*req.DataPlane.Timeout)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid dataplane.timeout")
				return
			}
			updatedConfig.DataPlane.Timeout = d
		}
		if req.DataPlane.MaxRetries != nil {
			if *req.DataPlane.MaxRetries < 0 {
				BadRequest(w, "invalid dataplane.max_retries")
				return
			}
			updatedConfig.DataPlane.MaxRetries = *req.DataPlane.MaxRetries
		}
	}

	if req.Share != nil {
		if req.Share.Enabled != nil {
			updatedConfig.Share.Enabled = *req.Share.Enabled
		}
		if req.Share.BaseURL != nil {
			updatedConfig.Share.BaseURL = *req.Share.BaseURL
		}
	}

	if req.Favorites != nil {
		if req.Favorites.Enabled != nil {
			updatedConfig.Favorites.Enabled = *req.Favorites.Enabled
		}
	}

	if req.Alerts != nil {
		if req.Alerts.Enabled != nil {
			updatedConfig.Alerts.Enabled = *req.Alerts.Enabled
		}
		if req.Alerts.CheckInterval != nil {
			d, err := time.ParseDuration(*req.Alerts.CheckInterval)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid alerts.check_interval")
				return
			}
			updatedConfig.Alerts.CheckInterval = d
		}
		if req.Alerts.ThresholdPct != nil {
			updatedConfig.Alerts.ThresholdPct = *req.Alerts.ThresholdPct
		}
		if req.Alerts.CriticalPct != nil {
			updatedConfig.Alerts.CriticalPct = *req.Alerts.CriticalPct
		}
		if req.Alerts.MinFreeBytes != nil {
			updatedConfig.Alerts.MinFreeBytes = *req.Alerts.MinFreeBytes
		}
		if req.Alerts.CooldownPeriod != nil {
			d, err := time.ParseDuration(*req.Alerts.CooldownPeriod)
			if err != nil || d <= 0 {
				BadRequest(w, "invalid alerts.cooldown_period")
				return
			}
			updatedConfig.Alerts.CooldownPeriod = d
		}
		if req.Alerts.WebhookURL != nil {
			updatedConfig.Alerts.WebhookURL = *req.Alerts.WebhookURL
		}
		if req.Alerts.WebhookMethod != nil {
			updatedConfig.Alerts.WebhookMethod = strings.ToUpper(strings.TrimSpace(*req.Alerts.WebhookMethod))
		}
		if req.Alerts.WebhookHeaders != nil {
			cleaned := make([]string, 0, len(*req.Alerts.WebhookHeaders))
			for _, header := range *req.Alerts.WebhookHeaders {
				trimmed := strings.TrimSpace(header)
				if trimmed == "" {
					continue
				}
				cleaned = append(cleaned, trimmed)
			}
			updatedConfig.Alerts.WebhookHeaders = cleaned
		}
	}

	if req.CDC != nil {
		if req.CDC.MinChunkSize != nil {
			updatedConfig.DataPlane.CDC.MinChunkSize = *req.CDC.MinChunkSize
		}
		if req.CDC.AvgChunkSize != nil {
			updatedConfig.DataPlane.CDC.AvgChunkSize = *req.CDC.AvgChunkSize
		}
		if req.CDC.MaxChunkSize != nil {
			updatedConfig.DataPlane.CDC.MaxChunkSize = *req.CDC.MaxChunkSize
		}
	}

	if req.WebDAV != nil {
		if req.WebDAV.Enabled != nil {
			updatedConfig.WebDAV.Enabled = *req.WebDAV.Enabled
		}
		if req.WebDAV.Prefix != nil {
			updatedConfig.WebDAV.Prefix = *req.WebDAV.Prefix
		}
		if req.WebDAV.ReadOnly != nil {
			updatedConfig.WebDAV.ReadOnly = *req.WebDAV.ReadOnly
		}
		if req.WebDAV.AuthType != nil {
			updatedConfig.WebDAV.AuthType = *req.WebDAV.AuthType
		}
		if req.WebDAV.Username != nil {
			updatedConfig.WebDAV.Username = *req.WebDAV.Username
		}
		if req.WebDAV.Password != nil {
			updatedConfig.WebDAV.Password = *req.WebDAV.Password
		}
		updatedConfig.WebDAV.Prefix = config.NormalizeWebDAVPrefix(updatedConfig.WebDAV.Prefix)
	}

	// Validate config
	if err := updatedConfig.Validate(); err != nil {
		s.logger.Warn().Err(err).Msg("invalid settings update rejected")
		BadRequest(w, "invalid configuration")
		return
	}
	if err := s.validateWebDAVIdentity(updatedConfig); err != nil {
		if errors.Is(err, errWebDAVUsernameMatchesNonAdmin) {
			BadRequest(w, err.Error())
			return
		}
		s.respondInternalError(w, "validate webdav identity", err)
		return
	}

	preparedDataplane, err := s.prepareDataplaneReplacement(r.Context(), req, updatedConfig)
	if err != nil {
		ServiceUnavailable(w, "unable to connect to configured dataplane")
		return
	}
	if preparedDataplane != nil {
		defer func() {
			if preparedDataplane != nil {
				_ = preparedDataplane.Close()
			}
		}()
	}

	// Save to file
	if err := updatedConfig.Save(s.configPath); err != nil {
		s.respondInternalError(w, "save settings", err)
		return
	}

	s.storeConfig(&updatedConfig)
	s.applyRuntimeSettings(r.Context(), req, updatedConfig, preparedDataplane)
	preparedDataplane = nil

	s.logger.Info().Msg("settings updated and saved")

	NewAPIResponse(nil).WithMessage("settings updated, some changes may require restart").Write(w, http.StatusOK)
}

func (s *Server) prepareDataplaneReplacement(ctx context.Context, req UpdateSettingsRequest, cfg config.Config) (*dataplane.Client, error) {
	if req.DataPlane == nil || req.DataPlane.GRPCAddress == nil {
		return nil, nil
	}
	if s.dataplane != nil && s.dataplane.Addr() == cfg.DataPlane.GRPCAddress {
		return nil, nil
	}

	replacement := dataplane.NewClient(cfg.DataPlane.GRPCAddress)
	if err := s.connectDataplaneClient(ctx, replacement, cfg.DataPlane.Timeout); err != nil {
		_ = replacement.Close()
		return nil, err
	}

	return replacement, nil
}

func (s *Server) applyRuntimeSettings(ctx context.Context, req UpdateSettingsRequest, cfg config.Config, preparedDataplane *dataplane.Client) {
	if req.DataPlane != nil && req.DataPlane.GRPCAddress != nil {
		swapDataplane := s.dataplane == nil || s.dataplane.Addr() != cfg.DataPlane.GRPCAddress
		if swapDataplane {
			replacement := preparedDataplane
			if replacement == nil {
				replacement = dataplane.NewClient(cfg.DataPlane.GRPCAddress)
				if err := s.connectDataplaneClient(ctx, replacement, cfg.DataPlane.Timeout); err != nil {
					s.logger.Warn().Err(err).Str("addr", cfg.DataPlane.GRPCAddress).Msg("Failed to connect updated data plane address")
					_ = replacement.Close()
					return
				}
			}

			previous := s.dataplane
			s.dataplane = replacement
			if s.fs != nil {
				s.fs.SetDataplaneClient(replacement)
			}
			if previous != nil {
				_ = previous.Close()
			}
		}
	}

	if req.Retention != nil {
		if s.retentionMonitor != nil {
			s.retentionMonitor.UpdateConfig(storage.RetentionMonitorConfig{
				MaxVersions:   cfg.Storage.Retention.MaxVersions,
				MaxVersionAge: cfg.Storage.Retention.MaxAge,
				MinFreeSpace:  cfg.Storage.Retention.MinFreeSpace,
				SweepInterval: cfg.Storage.Retention.GCInterval,
			})
		} else if s.fs != nil {
			s.fs.UpdateRetentionSettings(
				cfg.Storage.Retention.MaxVersions,
				cfg.Storage.Retention.MaxAge,
				cfg.Storage.Retention.MinFreeSpace,
			)
		}
	}

	if req.Trash != nil && s.fs != nil {
		s.fs.UpdateTrashSettings(
			cfg.Storage.Trash.Enabled,
			cfg.Storage.Trash.RetentionDays,
			cfg.Storage.Trash.MaxSize,
		)
	}

	if req.Versioning != nil && s.fs != nil {
		s.fs.UpdateVersioningSettings(
			cfg.Storage.Versioning.AutoVersionedExtensions,
			cfg.Storage.Versioning.AutoVersionedFilenames,
			cfg.Storage.Versioning.MaxVersionedSize,
		)
	}

	if req.Share != nil && req.Share.BaseURL != nil && s.shareHandler != nil {
		s.shareHandler.SetBaseURL(cfg.Share.BaseURL)
	}

	if req.WebDAV != nil {
		runtimeCfg := s.resolveWebDAVRuntimeConfig(cfg)
		s.storeActiveWebDAV(runtimeCfg)
		if s.updateWebDAV != nil {
			s.updateWebDAV(runtimeCfg)
		}
	}

	if req.Alerts != nil && s.alertMonitor != nil {
		s.alertMonitor.UpdateConfig(alerts.Config{
			Enabled:        cfg.Alerts.Enabled,
			CheckInterval:  cfg.Alerts.CheckInterval,
			ThresholdPct:   cfg.Alerts.ThresholdPct,
			CriticalPct:    cfg.Alerts.CriticalPct,
			MinFreeBytes:   cfg.Alerts.MinFreeBytes,
			CooldownPeriod: cfg.Alerts.CooldownPeriod,
			WebhookURL:     cfg.Alerts.WebhookURL,
			WebhookMethod:  cfg.Alerts.WebhookMethod,
			WebhookHeaders: cfg.Alerts.WebhookHeaders,
		})
	}
}

// WebDAVCredentialsResponse represents WebDAV credentials for admin users.
type WebDAVCredentialsResponse struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	AuthType string `json:"auth_type"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// handleGetWebDAVCredentials returns WebDAV credentials for admin users.
func (s *Server) handleGetWebDAVCredentials(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil {
		ServiceUnavailable(w, "configuration not available")
		return
	}

	runtimeCfg := s.currentActiveWebDAV()
	resp := WebDAVCredentialsResponse{
		Enabled:  runtimeCfg.Enabled,
		URL:      formatWebDAVPrefix(runtimeCfg.Prefix),
		AuthType: runtimeCfg.AuthType,
	}

	// Only include credentials if WebDAV is enabled and using basic auth
	if runtimeCfg.Enabled && runtimeCfg.AuthType == "basic" {
		resp.Username = runtimeCfg.Username
		if resp.Username == "" {
			resp.Username = "admin"
		}
		if runtimeCfg.PasswordIsGenerated && runtimeCfg.Password != "" {
			resp.Password = runtimeCfg.Password
		} else if runtimeCfg.Password == "" {
			// Get password from secrets (auto-generated only)
			secrets, err := config.LoadSecrets(cfg.Storage.Root)
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
	cfg := s.currentConfig()
	if cfg == nil {
		ServiceUnavailable(w, "configuration not available")
		return
	}

	secrets, err := config.LoadSecrets(cfg.Storage.Root)
	if err != nil {
		s.logger.Error().Err(err).Msg("failed to load secrets")
		ServiceUnavailable(w, "setup status unavailable")
		return
	}

	// No secrets file means not first run or something went wrong
	if secrets == nil {
		s.logger.Error().Msg("setup status requested without runtime secrets")
		ServiceUnavailable(w, "setup status unavailable")
		return
	}

	resp := SetupStatusResponse{
		Success:        true,
		IsFirstRun:     !secrets.SetupShown,
		AuthEnabled:    s.authEnabled,
		WebDAVEnabled:  cfg.WebDAV.Enabled,
		WebDAVAuthType: cfg.WebDAV.AuthType,
	}

	s.json(w, http.StatusOK, resp)
}

// handleAcknowledgeSetup marks the setup as shown
func (s *Server) handleAcknowledgeSetup(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil {
		ServiceUnavailable(w, "configuration not available")
		return
	}
	if err := config.MarkSetupShown(cfg.Storage.Root); err != nil {
		if errors.Is(err, config.ErrSecretsNotFound) {
			s.logger.Error().Err(err).Msg("failed to acknowledge setup")
			ServiceUnavailable(w, "setup acknowledge unavailable")
			return
		}
		s.respondInternalError(w, "acknowledge setup", err)
		return
	}

	s.json(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "setup acknowledged",
	})
}

// bufferedResponseRecorder captures the response until callers decide to flush it.
type bufferedResponseRecorder struct {
	headers     http.Header
	body        bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func newBufferedResponseRecorder() *bufferedResponseRecorder {
	return &bufferedResponseRecorder{
		headers:    make(http.Header),
		statusCode: http.StatusOK,
	}
}

func (r *bufferedResponseRecorder) Header() http.Header {
	return r.headers
}

func (r *bufferedResponseRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.body.Write(data)
}

func (r *bufferedResponseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.statusCode = code
	r.wroteHeader = true
}

func (r *bufferedResponseRecorder) FlushTo(w http.ResponseWriter) {
	for key, values := range r.headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(r.statusCode)
	_, _ = w.Write(r.body.Bytes())
}

func writeShareErrorResponse(w http.ResponseWriter, status int, message, code string) {
	writeRawJSON(w, status, map[string]any{
		"success": false,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeFavoritesErrorResponse(w http.ResponseWriter, status int, message, code string) {
	writeRawJSON(w, status, map[string]any{
		"success": false,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeRawJSON(w http.ResponseWriter, status int, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func (s *Server) isShareFeatureEnabled() bool {
	cfg := s.currentConfig()
	return cfg != nil && cfg.Share.Enabled
}

func (s *Server) isFavoritesFeatureEnabled() bool {
	cfg := s.currentConfig()
	return cfg != nil && cfg.Favorites.Enabled
}

func (s *Server) favoritesConfiguredButUnavailable() bool {
	return s.favoritesConfigured && (s.favoritesStore == nil || s.favoritesHandler == nil)
}

func (s *Server) writeShareFeatureDisabled(w http.ResponseWriter, status int) {
	writeShareErrorResponse(w, status, "share feature disabled", "SHARE_FEATURE_DISABLED")
}

func (s *Server) buildShareURL(id string) string {
	baseURL := ""
	if cfg := s.currentConfig(); cfg != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(cfg.Share.BaseURL), "/")
	}
	if baseURL != "" {
		return baseURL + "/s/" + id
	}
	return "/s/" + id
}

func (s *Server) ensureShareWithinOwnerHome(id string) (*share.Share, error) {
	if s.shareStore == nil {
		return nil, share.ErrShareNotFound
	}

	shareInfo, err := s.shareStore.Get(id)
	if err != nil {
		return nil, err
	}

	if !s.authEnabled || s.userStore == nil {
		return shareInfo, nil
	}

	owner, err := s.userStore.GetByID(shareInfo.CreatedBy)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, share.ErrShareNotFound
		}
		return nil, err
	}
	if owner.Role == auth.RoleAdmin {
		return shareInfo, nil
	}

	homeDir, err := validatePath(owner.HomeDir)
	if err != nil {
		return nil, err
	}
	if !pathWithinBase(homeDir, shareInfo.Path) {
		return nil, share.ErrShareNotFound
	}

	return shareInfo, nil
}

func (s *Server) handleAccessShare(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusGone)
		return
	}
	if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, share.ErrShareNotFound) {
			writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		s.respondInternalError(w, "authorize public share access", err)
		return
	}
	s.shareHandler.AccessShare(w, r)
}

func (s *Server) handleAccessShareWithPassword(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusGone)
		return
	}
	if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, share.ErrShareNotFound) {
			writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		s.respondInternalError(w, "authorize public share access", err)
		return
	}
	s.shareHandler.AccessShareWithPassword(w, r)
}

func (s *Server) handleDownloadShare(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusGone)
		return
	}
	if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, share.ErrShareNotFound) {
			writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		s.respondInternalError(w, "authorize public share download", err)
		return
	}
	s.shareHandler.DownloadShare(w, r)
}

func (s *Server) handleDownloadShareFile(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusGone)
		return
	}
	if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, share.ErrShareNotFound) {
			writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		s.respondInternalError(w, "authorize public share download", err)
		return
	}
	s.shareHandler.DownloadShareFile(w, r)
}

func (s *Server) handleListShareItems(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusGone)
		return
	}
	if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, share.ErrShareNotFound) {
			writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
			return
		}
		s.respondInternalError(w, "authorize public share listing", err)
		return
	}
	s.shareHandler.ListShareItems(w, r)
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusServiceUnavailable)
		return
	}
	if s.shareStore == nil {
		s.shareHandler.ListShares(w, r)
		return
	}

	userID := "anonymous"
	if claims := auth.GetClaimsFromContext(r.Context()); claims != nil && claims.UserID != "" {
		userID = claims.UserID
	}

	var sharesList []*share.Share
	if auth.IsAdmin(r.Context()) && r.URL.Query().Get("all") == "true" {
		sharesList = s.shareStore.ListAll()
	} else {
		sharesList = s.shareStore.ListByUser(userID)
	}

	if s.authEnabled {
		var err error
		sharesList, err = s.filterSharesByHomeDir(r.Context(), sharesList)
		if err != nil {
			s.respondInternalError(w, "filter shares by home directory", err)
			return
		}
	}

	infos := make([]*share.ShareInfo, len(sharesList))
	for i, item := range sharesList {
		infos[i] = item.ToInfo()
		infos[i].URL = s.buildShareURL(item.ID)
	}

	NewAPIResponse(infos).Write(w, http.StatusOK)
}

func (s *Server) handleGetShare(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusServiceUnavailable)
		return
	}
	if !auth.IsAdmin(r.Context()) {
		if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
			if errors.Is(err, share.ErrShareNotFound) {
				writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
				return
			}
			s.respondInternalError(w, "authorize share access", err)
			return
		}
	}
	s.shareHandler.GetShare(w, r)
}

func (s *Server) handleUpdateShare(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusServiceUnavailable)
		return
	}
	if !auth.IsAdmin(r.Context()) {
		if _, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err != nil {
			if errors.Is(err, share.ErrShareNotFound) {
				writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
				return
			}
			s.respondInternalError(w, "authorize share update", err)
			return
		}
	}
	s.shareHandler.UpdateShare(w, r)
}

func (s *Server) handleListFavorites(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	if s.favoritesStore == nil {
		s.favoritesHandler.ListFavorites(w, r)
		return
	}

	userID := "anonymous"
	if claims := auth.GetClaimsFromContext(r.Context()); claims != nil && claims.UserID != "" {
		userID = claims.UserID
	}

	favoritesList := s.favoritesStore.List(userID)
	if s.authEnabled {
		var err error
		favoritesList, err = s.filterFavoritesByHomeDir(r.Context(), favoritesList)
		if err != nil {
			s.respondInternalError(w, "filter favorites by home directory", err)
			return
		}
	}

	NewAPIResponse(map[string]any{
		"favorites": favoritesList,
		"count":     len(favoritesList),
	}).Write(w, http.StatusOK)
}

func (s *Server) handleAddFavorite(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePath, err := readFavoriteBodyPath(r)
	if err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	if favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	s.favoritesHandler.AddFavorite(w, r)
}

func (s *Server) handleCheckFavorite(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	if favoritePath := readFavoriteQueryPath(r); favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	s.favoritesHandler.CheckFavorite(w, r)
}

func (s *Server) handleCheckFavorites(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePaths, err := readFavoriteBatchPaths(r)
	if err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	for _, favoritePath := range favoritePaths {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	s.favoritesHandler.CheckFavorites(w, r)
}

func (s *Server) handleRemoveFavorite(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	if favoritePath := readFavoriteRoutePath(r); favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	s.favoritesHandler.RemoveFavorite(w, r)
}

func (s *Server) handleUpdateFavoriteNote(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if s.favoritesConfiguredButUnavailable() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	if favoritePath := readFavoriteRoutePath(r); favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	s.favoritesHandler.UpdateNote(w, r)
}

// handleCreateShareWithActivity wraps share creation to log activity
func (s *Server) handleCreateShareWithActivity(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusServiceUnavailable)
		return
	}

	sharePath := ""
	parsedPath, err := readCreateSharePath(r)
	if err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	sharePath = parsedPath
	if sharePath != "" {
		if err := s.authorizeUserPath(r.Context(), sharePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}

	rec := newBufferedResponseRecorder()
	s.shareHandler.CreateShare(rec, r)

	// If share was created (status 201), log the activity
	if rec.statusCode == http.StatusCreated {
		s.LogActivityWithWarning(rec, r, activity.ActionShare, sharePath, nil)
	}
	rec.FlushTo(w)
}

// handleDeleteShareWithActivity wraps share deletion to log activity
func (s *Server) handleDeleteShareWithActivity(w http.ResponseWriter, r *http.Request) {
	if !s.isShareFeatureEnabled() {
		s.writeShareFeatureDisabled(w, http.StatusServiceUnavailable)
		return
	}

	sharePath := ""
	if s.shareStore != nil {
		if shareInfo, err := s.ensureShareWithinOwnerHome(chi.URLParam(r, "id")); err == nil {
			sharePath = shareInfo.Path
		} else if !auth.IsAdmin(r.Context()) {
			if errors.Is(err, share.ErrShareNotFound) {
				writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
				return
			}
			s.respondInternalError(w, "authorize share delete", err)
			return
		}
	}

	rec := newBufferedResponseRecorder()
	s.shareHandler.DeleteShare(rec, r)

	// Log successful share deletions regardless of whether the handler responds 200 or 204.
	if rec.statusCode >= http.StatusOK && rec.statusCode < http.StatusMultipleChoices {
		s.LogActivityWithWarning(rec, r, activity.ActionUnshare, sharePath, nil)
	}
	rec.FlushTo(w)
}

func readCreateSharePath(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}

	body, err := readBufferedRequestBody(r, DefaultJSONRequestBodyLimit)
	if err != nil {
		return "", err
	}

	var req share.CreateShareRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}

	cleanPath := strings.TrimSpace(req.Path)
	if cleanPath == "" {
		return "", nil
	}

	return path.Clean("/" + cleanPath), nil
}

func readFavoriteBodyPath(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}

	body, err := readBufferedRequestBody(r, DefaultJSONRequestBodyLimit)
	if err != nil {
		return "", err
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}

	cleanPath := strings.TrimSpace(req.Path)
	if cleanPath == "" {
		return "", nil
	}

	return path.Clean("/" + cleanPath), nil
}

func readFavoriteBatchPaths(r *http.Request) ([]string, error) {
	if r.Body == nil {
		return nil, nil
	}

	body, err := readBufferedRequestBody(r, DefaultJSONRequestBodyLimit)
	if err != nil {
		return nil, err
	}

	var req struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	cleanPaths := make([]string, 0, len(req.Paths))
	for _, favoritePath := range req.Paths {
		trimmedPath := strings.TrimSpace(favoritePath)
		if trimmedPath == "" {
			continue
		}
		cleanPaths = append(cleanPaths, path.Clean("/"+trimmedPath))
	}

	return cleanPaths, nil
}

func readFavoriteQueryPath(r *http.Request) string {
	cleanPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if cleanPath == "" {
		return ""
	}
	return path.Clean("/" + cleanPath)
}

func readFavoriteRoutePath(r *http.Request) string {
	cleanPath := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/favorites"))
	if cleanPath == "" || cleanPath == "/" {
		return ""
	}
	return path.Clean("/" + cleanPath)
}
