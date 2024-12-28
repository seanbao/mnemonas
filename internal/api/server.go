// Package api provides REST API and gRPC API
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/maintenance"
	"github.com/seanbao/mnemonas/internal/metrics"
	"github.com/seanbao/mnemonas/internal/thumbnail"
	"github.com/seanbao/mnemonas/internal/webdavcas"
)

// Server is the API server
type Server struct {
	router      *chi.Mux
	logger      zerolog.Logger
	dataplane   *dataplane.Client
	fs          *webdavcas.FileSystem
	thumbnail   *thumbnail.Service
	maintenance *maintenance.HistoryStore
	startTime   time.Time
}

// ServerConfig holds server configuration
type ServerConfig struct {
	DataplaneAddr   string
	CASRoot         string
	MetadataRoot    string
	ThumbnailRoot   string
	MaintenanceRoot string
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

	// Initialize filesystem if CAS root provided
	if cfg != nil && cfg.CASRoot != "" && cfg.MetadataRoot != "" {
		fs, err := webdavcas.NewFileSystem(cfg.CASRoot, cfg.MetadataRoot)
		if err != nil {
			return nil, err
		}
		s.fs = fs
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

	s.setupRoutes()
	return s, nil
}

func (s *Server) setupRoutes() {
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/api/v1/version", s.handleVersion)

	// API v1
	s.router.Route("/api/v1", func(r chi.Router) {
		// File operations
		r.Route("/files", func(r chi.Router) {
			r.Get("/*", s.handleListFiles)
			r.Post("/*", s.handleUploadFile)
			r.Delete("/*", s.handleDeleteFile)
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

		// Maintenance operations
		r.Get("/scrub", s.handleGetScrubResult)
		r.Post("/scrub", s.handleScrub)
		r.Get("/objects", s.handleListObjects)
		r.Post("/gc", s.handleGC)
		r.Get("/diagnostics-export", s.handleDiagnosticsExport)
		r.Get("/metrics", s.handleMetrics)
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
	s.json(w, http.StatusOK, map[string]any{
		"name":    AppName,
		"version": AppVersion,
		"go":      runtime.Version(),
	})
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
		NotFound(w, err.Error())
		return
	}

	// Convert to API response format
	items := make([]map[string]any, 0, len(files))
	for _, f := range files {
		item := map[string]any{
			"name":     f.Path,
			"is_dir":   f.IsDir,
			"size":     f.Size,
			"mod_time": f.ModTime.Format(time.RFC3339),
		}
		if !f.IsDir && f.ContentHash != "" {
			item["hash"] = f.ContentHash
			item["versions"] = len(f.Versions) + 1
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
		InternalError(w, err.Error())
		return
	}

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file uploaded successfully").Write(w, http.StatusCreated)
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
		if strings.Contains(err.Error(), "not found") {
			NotFound(w, err.Error())
			return
		}
		InternalError(w, err.Error())
		return
	}

	NewAPIResponse(map[string]any{
		"path": filePath,
	}).WithMessage("file deleted successfully").Write(w, http.StatusOK)
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
		NotFound(w, err.Error())
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

	if err := s.fs.RestoreVersion(r.Context(), filePath, hash); err != nil {
		InternalError(w, err.Error())
		return
	}

	NewAPIResponse(map[string]any{
		"path":     filePath,
		"restored": hash,
	}).WithMessage("version restored successfully").Write(w, http.StatusOK)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]any{
		"total_files":  0,
		"total_size":   0,
		"unique_size":  0,
		"dedup_ratio":  0.0,
		"total_chunks": 0,
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

	s.json(w, http.StatusOK, stats)
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

	s.json(w, http.StatusOK, diag)
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
		InternalError(w, err.Error())
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
		InternalError(w, err.Error())
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
		InternalError(w, "failed to get referenced hashes: "+err.Error())
		return
	}

	// Step 2: Get all CAS objects
	var allObjects []dataplane.ObjectInfo
	var cursor string
	for {
		result, err := s.dataplane.ListObjects(ctx, cursor, 1000)
		if err != nil {
			InternalError(w, "failed to list objects: "+err.Error())
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
		InternalError(w, err.Error())
		return
	}

	// Get stats
	count, totalSize, _ := s.fs.GetTrashStats(r.Context())

	// Convert to API response format
	apiItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		apiItem := map[string]any{
			"id":            item.ID,
			"original_path": item.OriginalPath,
			"deleted_at":    item.DeletedAt.Format(time.RFC3339),
			"name":          path.Base(item.OriginalPath),
			"is_dir":        item.FileInfo.IsDir,
			"size":          item.FileInfo.Size,
		}
		if !item.FileInfo.IsDir && item.FileInfo.ContentHash != "" {
			apiItem["hash"] = item.FileInfo.ContentHash
			apiItem["versions"] = len(item.FileInfo.Versions) + 1
		}
		apiItems = append(apiItems, apiItem)
	}

	NewAPIResponse(map[string]any{
		"items":      apiItems,
		"count":      count,
		"total_size": totalSize,
	}).Write(w, http.StatusOK)
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
		NotFound(w, err.Error())
		return
	}

	NewAPIResponse(map[string]any{
		"id":            item.ID,
		"original_path": item.OriginalPath,
		"deleted_at":    item.DeletedAt.Format(time.RFC3339),
		"name":          path.Base(item.OriginalPath),
		"is_dir":        item.FileInfo.IsDir,
		"size":          item.FileInfo.Size,
		"hash":          item.FileInfo.ContentHash,
		"versions":      len(item.FileInfo.Versions) + 1,
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
		// Check if it's a conflict error
		if strings.Contains(err.Error(), "already exists") ||
			strings.Contains(err.Error(), "does not exist") {
			Conflict(w, err.Error())
			return
		}
		InternalError(w, err.Error())
		return
	}

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
		NotFound(w, err.Error())
		return
	}

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
		InternalError(w, err.Error())
		return
	}

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
		NotFound(w, err.Error())
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
