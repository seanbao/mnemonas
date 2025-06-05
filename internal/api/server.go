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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/zeebo/blake3"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/backup"
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
	"github.com/seanbao/mnemonas/internal/workspace"
)

const maxObjectsCursorLength = 256
const maxListObjectsLimit = 1000
const maxGCGracePeriodHours = int64((1<<63 - 1) / int64(time.Hour))
const defaultMaxThumbnailSourceBytes int64 = 100 * 1024 * 1024

const scrubFailurePublicMessage = "scrub failed; check server logs for details"
const scrubObjectCorruptedPublicMessage = "object failed integrity verification"
const scrubObjectMissingPublicMessage = "object is missing"
const scrubObjectReadPublicMessage = "object could not be read"
const scrubObjectUnknownPublicMessage = "object verification failed"
const untrustedDownloadContentSecurityPolicy = "sandbox; default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'unsafe-inline'"

var startScrub = func(store *maintenance.HistoryStore) (*maintenance.ScrubResult, error) {
	return store.StartScrub()
}

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

var getDiskStats = func(fs *storage.FileSystem) (*storage.DiskStats, error) {
	return fs.DiskStats()
}

var serverConnectDataplaneClient = func(s *Server, parent context.Context, client *dataplane.Client, totalTimeout time.Duration) error {
	return s.connectDataplaneClient(parent, client, totalTimeout)
}

var apiTimeNow = time.Now
var maxThumbnailSourceBytes = defaultMaxThumbnailSourceBytes

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
	backupManager         *backup.Manager
	activity              *activity.Store
	thumbnailConfigured   bool
	maintenanceConfigured bool
	backupConfigured      bool
	activityConfigured    bool
	startTime             time.Time
	appVersion            string
	buildTime             string
	// Auth components
	userStore    *auth.UserStore
	tokenManager *auth.TokenManager
	authHandler  *auth.Handler
	authMw       *auth.Middleware
	authEnabled  bool
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
	config              *config.Config
	configMu            sync.RWMutex
	settingsMu          sync.Mutex
	configPath          string
	activeWebDAV        WebDAVRuntimeConfig
	webdavMu            sync.RWMutex
	updateWebDAV        func(WebDAVRuntimeConfig)
	afterPathRenamed    func(oldPath, newPath string)
	afterPathDeleted    func(path string) *storage.PathDeleteHookResult
	beforeThumbnailRead func(string) error
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
	cloned.Storage.Versioning.AutoVersionedExtensions = append([]string(nil), cfg.Storage.Versioning.AutoVersionedExtensions...)
	cloned.Storage.Versioning.AutoVersionedFilenames = append([]string(nil), cfg.Storage.Versioning.AutoVersionedFilenames...)
	cloned.Alerts.WebhookHeaders = append([]string(nil), cfg.Alerts.WebhookHeaders...)
	cloned.Backup.Jobs = append([]config.BackupJobConfig(nil), cfg.Backup.Jobs...)
	for i := range cloned.Backup.Jobs {
		cloned.Backup.Jobs[i].Exclude = append([]string(nil), cfg.Backup.Jobs[i].Exclude...)
	}
	return &cloned
}

func (s *Server) currentConfig() *config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return cloneConfigSnapshot(s.config)
}

func (s *Server) storeConfig(cfg *config.Config) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.config = cloneConfigSnapshot(cfg)
	if cfg != nil {
		requestip.SetTrustedProxyHops(cfg.Server.TrustedProxyHops)
	}
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

func webDAVUsesGeneratedPassword(cfg config.Config) bool {
	return cfg.WebDAV.Enabled && strings.EqualFold(cfg.WebDAV.AuthType, "basic") && strings.TrimSpace(cfg.WebDAV.Password) == ""
}

func (s *Server) webDAVConfiguredButUnavailable() bool {
	cfg := s.currentConfig()
	if cfg == nil || !webDAVUsesGeneratedPassword(*cfg) {
		return false
	}
	runtimeCfg := s.currentActiveWebDAV()
	return !runtimeCfg.Enabled || !strings.EqualFold(runtimeCfg.AuthType, "basic") || strings.TrimSpace(runtimeCfg.Password) == ""
}

func (s *Server) handlePathRenamed(_ context.Context, oldPath, newPath string) error {
	var renamedShares []*share.Share
	var renameWarning error
	if s.shareStore != nil {
		var err error
		renamedShares, err = s.shareStore.UpdatePathReferencesWithRestore(oldPath, newPath)
		if err != nil {
			if share.IsPersistenceWarning(err) {
				renameWarning = errors.Join(renameWarning, workspace.WrapVisibleMutationWarning(fmt.Errorf("sync share paths after rename: %w", err)))
			} else {
				return fmt.Errorf("sync share paths after rename: %w", err)
			}
		}
	}
	if s.favoritesStore != nil {
		if err := s.favoritesStore.UpdatePathReferences(oldPath, newPath); err != nil {
			if favorites.IsPersistenceWarning(err) {
				renameWarning = errors.Join(renameWarning, workspace.WrapVisibleMutationWarning(fmt.Errorf("sync favorite paths after rename: %w", err)))
			} else {
				if len(renamedShares) > 0 {
					if rollbackErr := s.shareStore.RestoreMovedSharesPreservingCurrent(renamedShares); rollbackErr != nil && !share.IsPersistenceWarning(rollbackErr) {
						return errors.Join(
							fmt.Errorf("sync favorite paths after rename: %w", err),
							fmt.Errorf("rollback share paths after rename: %w", rollbackErr),
						)
					}
				}
				return fmt.Errorf("sync favorite paths after rename: %w", err)
			}
		}
	}
	if s.afterPathRenamed != nil {
		s.afterPathRenamed(oldPath, newPath)
	}

	return renameWarning
}

type deletedPathRestoreState struct {
	Shares    []*share.Share        `json:"shares,omitempty"`
	Favorites []*favorites.Favorite `json:"favorites,omitempty"`
}

type deletedPathShareRestoreSnapshot struct {
	shares    []*share.Share
	absentIDs []string
}

func relocateDeletedPathRestorePath(sourcePath, restoredPath, currentPath string) (string, bool) {
	sourcePath = normalizeDeletedPathRestoreStatePath(sourcePath)
	restoredPath = normalizeDeletedPathRestoreStatePath(restoredPath)
	currentPath = normalizeDeletedPathRestoreStatePath(currentPath)

	if currentPath == sourcePath {
		return restoredPath, true
	}

	if sourcePath == "/" {
		if !strings.HasPrefix(currentPath, "/") {
			return "", false
		}
		return path.Join(restoredPath, strings.TrimPrefix(currentPath, "/")), true
	}

	prefix := sourcePath + "/"
	if !strings.HasPrefix(currentPath, prefix) {
		return "", false
	}

	return path.Join(restoredPath, strings.TrimPrefix(currentPath, prefix)), true
}

func normalizeDeletedPathRestoreStatePath(targetPath string) string {
	normalized := strings.ReplaceAll(targetPath, "\\", "/")
	return path.Clean("/" + strings.TrimPrefix(normalized, "/"))
}

func relocateDeletedPathRestoreState(state deletedPathRestoreState, sourcePath, restoredPath string) deletedPathRestoreState {
	if sourcePath == restoredPath {
		return state
	}

	relocated := deletedPathRestoreState{}
	if len(state.Shares) > 0 {
		relocated.Shares = make([]*share.Share, 0, len(state.Shares))
		for _, item := range state.Shares {
			if item == nil {
				continue
			}
			updatedPath, ok := relocateDeletedPathRestorePath(sourcePath, restoredPath, item.Path)
			if !ok {
				continue
			}
			clone := *item
			clone.Path = updatedPath
			relocated.Shares = append(relocated.Shares, &clone)
		}
	}
	if len(state.Favorites) > 0 {
		relocated.Favorites = make([]*favorites.Favorite, 0, len(state.Favorites))
		for _, item := range state.Favorites {
			if item == nil {
				continue
			}
			updatedPath, ok := relocateDeletedPathRestorePath(sourcePath, restoredPath, item.Path)
			if !ok {
				continue
			}
			clone := *item
			clone.Path = updatedPath
			relocated.Favorites = append(relocated.Favorites, &clone)
		}
	}

	return relocated
}

func (s *Server) snapshotDeletedPathShareRestoreState(shares []*share.Share) (deletedPathShareRestoreSnapshot, error) {
	if len(shares) == 0 || s.shareStore == nil {
		return deletedPathShareRestoreSnapshot{}, nil
	}

	snapshot := deletedPathShareRestoreSnapshot{}
	seenIDs := make(map[string]struct{}, len(shares))
	for _, item := range shares {
		if item == nil {
			continue
		}
		if _, seen := seenIDs[item.ID]; seen {
			continue
		}
		seenIDs[item.ID] = struct{}{}

		current, err := s.shareStore.Get(item.ID)
		if err != nil {
			if errors.Is(err, share.ErrShareNotFound) {
				snapshot.absentIDs = append(snapshot.absentIDs, item.ID)
				continue
			}
			return deletedPathShareRestoreSnapshot{}, err
		}
		snapshotShare := *current
		snapshotShare.Enabled = false
		snapshot.shares = append(snapshot.shares, &snapshotShare)
	}

	return snapshot, nil
}

func (s *Server) rollbackDeletedPathShareRestoreState(snapshot deletedPathShareRestoreSnapshot) error {
	if s.shareStore == nil {
		return nil
	}

	var rollbackErr error
	if len(snapshot.shares) > 0 {
		if err := s.shareStore.RestoreDisabledSharesPreservingCurrent(snapshot.shares); err != nil {
			if !share.IsPersistenceWarning(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore share snapshot after trash metadata failure: %w", err))
			}
		}
	}
	for _, id := range snapshot.absentIDs {
		if err := s.shareStore.Delete(id); err != nil && !errors.Is(err, share.ErrShareNotFound) {
			if !share.IsPersistenceWarning(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete restored share after trash metadata failure: %w", err))
			}
		}
	}

	return rollbackErr
}

func (s *Server) restoreDeletedPathState(sourcePath, restoredPath string, restoreData []byte) error {
	if len(restoreData) == 0 {
		return nil
	}

	var state deletedPathRestoreState
	if err := json.Unmarshal(restoreData, &state); err != nil {
		return fmt.Errorf("decode trash restore metadata: %w", err)
	}

	state = relocateDeletedPathRestoreState(state, sourcePath, restoredPath)

	if len(state.Shares) > 0 && s.shareStore == nil {
		return errors.New("share store unavailable for trash restore metadata")
	}
	if len(state.Favorites) > 0 && s.favoritesStore == nil {
		return errors.New("favorites store unavailable for trash restore metadata")
	}

	shareSnapshot := deletedPathShareRestoreSnapshot{}
	var restoreWarning error
	if len(state.Shares) > 0 && len(state.Favorites) > 0 {
		var err error
		shareSnapshot, err = s.snapshotDeletedPathShareRestoreState(state.Shares)
		if err != nil {
			return fmt.Errorf("snapshot shares before trash metadata restore: %w", err)
		}
	}
	if len(state.Shares) > 0 && s.shareStore != nil {
		if err := s.shareStore.RestoreSharesPreservingCurrent(state.Shares); err != nil {
			if share.IsPersistenceWarning(err) {
				restoreWarning = errors.Join(restoreWarning, fmt.Errorf("restore shares from trash metadata: %w", err))
			} else {
				return fmt.Errorf("restore shares from trash metadata: %w", err)
			}
		}
	}
	if len(state.Favorites) > 0 && s.favoritesStore != nil {
		if err := s.favoritesStore.RestoreFavoritesIfMissing(state.Favorites); err != nil {
			if favorites.IsPersistenceWarning(err) {
				restoreWarning = errors.Join(restoreWarning, fmt.Errorf("restore favorites from trash metadata: %w", err))
				return restoreWarning
			}
			if len(state.Shares) > 0 {
				if rollbackErr := s.rollbackDeletedPathShareRestoreState(shareSnapshot); rollbackErr != nil {
					return errors.Join(
						fmt.Errorf("restore favorites from trash metadata: %w", err),
						fmt.Errorf("rollback shares after trash metadata failure: %w", rollbackErr),
					)
				}
			}
			return fmt.Errorf("restore favorites from trash metadata: %w", err)
		}
	}

	return restoreWarning
}

func (s *Server) handlePathDeleted(_ context.Context, targetPath string) (*storage.PathDeleteHookResult, error) {
	var disabledShares []*share.Share
	var deleteHookWarning error
	if s.shareStore != nil {
		var err error
		disabledShares, err = s.shareStore.DisableSharesUnderPathWithRestore(targetPath)
		if err != nil {
			if share.IsPersistenceWarning(err) {
				deleteHookWarning = errors.Join(deleteHookWarning, workspace.WrapVisibleMutationWarning(fmt.Errorf("disable shares after delete: %w", err)))
			} else {
				return nil, fmt.Errorf("disable shares after delete: %w", err)
			}
		}
	}

	var removedFavorites []*favorites.Favorite
	if s.favoritesStore != nil {
		var err error
		removedFavorites, err = s.favoritesStore.RemoveFavoritesUnderPathWithRestore(targetPath)
		if err != nil {
			if favorites.IsPersistenceWarning(err) {
				deleteHookWarning = errors.Join(deleteHookWarning, workspace.WrapVisibleMutationWarning(fmt.Errorf("remove favorites after delete: %w", err)))
			} else {
				if s.shareStore != nil {
					if rollbackErr := s.shareStore.RestoreDisabledSharesPreservingCurrent(disabledShares); rollbackErr != nil && !share.IsPersistenceWarning(rollbackErr) {
						return nil, errors.Join(
							fmt.Errorf("remove favorites after delete: %w", err),
							fmt.Errorf("rollback shares after delete: %w", rollbackErr),
						)
					}
				}
				return nil, fmt.Errorf("remove favorites after delete: %w", err)
			}
		}
	}

	rollback := func() error {
		var rollbackErr error
		if len(removedFavorites) > 0 {
			if err := s.favoritesStore.RestoreFavoritesIfMissing(removedFavorites); err != nil {
				if !favorites.IsPersistenceWarning(err) {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore favorites after delete rollback: %w", err))
				}
			}
		}
		if len(disabledShares) > 0 {
			if err := s.shareStore.RestoreDisabledSharesPreservingCurrent(disabledShares); err != nil {
				if !share.IsPersistenceWarning(err) {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore shares after delete rollback: %w", err))
				}
			}
		}
		return rollbackErr
	}

	var hookResult *storage.PathDeleteHookResult
	if len(disabledShares) > 0 || len(removedFavorites) > 0 {
		restoreData, err := json.Marshal(deletedPathRestoreState{
			Shares:    disabledShares,
			Favorites: removedFavorites,
		})
		if err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return nil, errors.Join(
					fmt.Errorf("encode delete restore metadata: %w", err),
					fmt.Errorf("rollback delete hooks after metadata encode failure: %w", rollbackErr),
				)
			}
			return nil, fmt.Errorf("encode delete restore metadata: %w", err)
		}

		hookResult = &storage.PathDeleteHookResult{
			Rollback:    rollback,
			RestoreData: restoreData,
		}
	}

	var afterHookResult *storage.PathDeleteHookResult
	if s.afterPathDeleted != nil {
		afterHookResult = s.afterPathDeleted(targetPath)
	}

	combinedHookResult, err := combineDeleteHookResults(hookResult, afterHookResult)
	if err != nil {
		rollbackErr := errors.Join(runDeleteHookRollback(afterHookResult), runDeleteHookRollback(hookResult))
		if rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback delete hooks after listener merge failure: %w", rollbackErr))
		}
		return nil, err
	}

	return combinedHookResult, deleteHookWarning
}

func runDeleteHookRollback(result *storage.PathDeleteHookResult) error {
	if result == nil || result.Rollback == nil {
		return nil
	}
	return result.Rollback()
}

func combineDeleteHookResults(primary, secondary *storage.PathDeleteHookResult) (*storage.PathDeleteHookResult, error) {
	if primary == nil {
		return secondary, nil
	}
	if secondary == nil {
		return primary, nil
	}
	if len(primary.RestoreData) > 0 && len(secondary.RestoreData) > 0 {
		return nil, errors.New("multiple delete hooks returned restore metadata")
	}

	combined := &storage.PathDeleteHookResult{}
	if len(primary.RestoreData) > 0 {
		combined.RestoreData = primary.RestoreData
	} else {
		combined.RestoreData = secondary.RestoreData
	}
	if primary.Rollback != nil || secondary.Rollback != nil {
		combined.Rollback = func() error {
			var rollbackErr error
			if secondary.Rollback != nil {
				rollbackErr = errors.Join(rollbackErr, secondary.Rollback())
			}
			if primary.Rollback != nil {
				rollbackErr = errors.Join(rollbackErr, primary.Rollback())
			}
			return rollbackErr
		}
	}

	return combined, nil
}

type AlertMonitor interface {
	UpdateConfig(cfg alerts.Config)
}

type AlertEventSender interface {
	SendEvent(ctx context.Context, event alerts.EventPayload) error
}

type alertStatsProvider interface {
	LastStats() *alerts.StorageStats
}

type RetentionMonitor interface {
	UpdateConfig(cfg storage.RetentionMonitorConfig)
}

func newBackupAlertNotifier(monitor AlertMonitor, logger zerolog.Logger) backup.Notifier {
	sender, ok := monitor.(AlertEventSender)
	if !ok || sender == nil {
		return nil
	}
	return backup.NotifierFunc(func(ctx context.Context, event backup.NotificationEvent) error {
		alertEvent := alerts.EventPayload{
			Type:      event.Type,
			Level:     backupNotificationLevel(event.Level),
			Message:   event.Message,
			Timestamp: event.Timestamp,
			Details: map[string]any{
				"job_id":           event.JobID,
				"job_name":         event.JobName,
				"job_type":         event.JobType,
				"run_id":           event.RunID,
				"trigger":          event.Trigger,
				"status":           event.Status,
				"started_at":       event.StartedAt,
				"finished_at":      event.FinishedAt,
				"source":           event.Source,
				"destination":      event.Destination,
				"snapshot_path":    event.SnapshotPath,
				"manifest_path":    event.ManifestPath,
				"file_count":       event.FileCount,
				"total_bytes":      event.TotalBytes,
				"verified_bytes":   event.VerifiedBytes,
				"pruned_snapshots": event.PrunedSnapshots,
				"warnings":         event.Warnings,
				"error_message":    event.ErrorMessage,
			},
		}
		if err := sender.SendEvent(ctx, alertEvent); err != nil {
			logger.Warn().
				Err(err).
				Str("backup_job", event.JobID).
				Str("event_type", event.Type).
				Msg("failed to send backup alert event")
			return err
		}
		return nil
	})
}

func backupNotificationLevel(level string) alerts.AlertLevel {
	if level == backup.NotificationLevelCritical {
		return alerts.AlertLevelCritical
	}
	return alerts.AlertLevelWarning
}

func formatSettingsDuration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	return d.String()
}

func settingsStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

const (
	auditStatusHeaderName               = "X-Mnemonas-Audit-Status"
	auditStatusFailedValue              = "failed"
	auditWarningHeader                  = `199 MnemoNAS "activity log persistence failed"`
	workspaceMutationWarningHeader      = `199 MnemoNAS "workspace mutation persistence incomplete"`
	scrubResultPersistenceWarningHeader = `199 MnemoNAS "scrub result persistence incomplete"`
	trashRestoreMetadataWarningHeader   = `199 MnemoNAS "trash restore metadata reconciliation failed"`
	deleteCleanupWarningHeader          = `199 MnemoNAS "delete cleanup incomplete"`
	trashDeleteCleanupWarningHeader     = `199 MnemoNAS "trash delete cleanup incomplete"`
)

func decodeJSONBodyWithLimit(r *http.Request, dst any, limit int64) error {
	body, err := readBufferedRequestBody(r, limit)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	decoder := json.NewDecoder(bytes.NewReader(body))
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

func resetJSONRequestBody(r *http.Request, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))
	return nil
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

	NewAPIError("INVALID_REQUEST", "invalid request body").Write(w, http.StatusBadRequest)
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
	if n > 0 {
		originalBody := r.Body
		r.Body = &prependReadCloser{
			Reader: io.MultiReader(bytes.NewReader(firstByte[:n]), originalBody),
			Closer: originalBody,
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return true, err
		}
		return true, nil
	}
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

	password, err := loadGeneratedWebDAVPassword(cfg.Storage.Root)
	if err != nil {
		runtimeCfg.Enabled = false
		s.logger.Warn().Err(err).Msg("disabling WebDAV runtime config because generated password is unavailable")
		return runtimeCfg
	}

	runtimeCfg.Password = password
	runtimeCfg.PasswordIsGenerated = true
	return runtimeCfg
}

func loadGeneratedWebDAVPassword(dataRoot string) (string, error) {
	secrets, err := config.LoadSecrets(dataRoot)
	if err != nil {
		return "", err
	}
	if secrets == nil || strings.TrimSpace(secrets.WebDAVPassword) == "" {
		return "", config.ErrSecretsNotFound
	}
	return secrets.WebDAVPassword, nil
}

func prepareWebDAVRuntimeConfig(cfg config.Config) (*WebDAVRuntimeConfig, error) {
	runtimeCfg := WebDAVRuntimeConfig{
		Enabled:  cfg.WebDAV.Enabled,
		Prefix:   cfg.WebDAV.Prefix,
		ReadOnly: cfg.WebDAV.ReadOnly,
		AuthType: cfg.WebDAV.AuthType,
		Username: cfg.WebDAV.Username,
		Password: cfg.WebDAV.Password,
	}

	if !runtimeCfg.Enabled || !strings.EqualFold(runtimeCfg.AuthType, "basic") {
		return &runtimeCfg, nil
	}

	if strings.TrimSpace(runtimeCfg.Username) == "" {
		runtimeCfg.Username = "admin"
	}

	if strings.TrimSpace(runtimeCfg.Password) != "" {
		return &runtimeCfg, nil
	}

	password, err := loadGeneratedWebDAVPassword(cfg.Storage.Root)
	if err != nil {
		return nil, err
	}

	runtimeCfg.Password = password
	runtimeCfg.PasswordIsGenerated = true
	return &runtimeCfg, nil
}

// ServerConfig holds server configuration
type ServerConfig struct {
	DataplaneAddr string
	// New storage configuration
	FileSystem *storage.FileSystem
	// Additional path change listeners run after the built-in share/favorites sync.
	AfterPathRenamed func(oldPath, newPath string)
	AfterPathDeleted func(path string) *storage.PathDeleteHookResult
	// Storage service roots
	ThumbnailRoot   string
	MaintenanceRoot string
	BackupRoot      string
	ActivityRoot    string
	StorageRoot     string
	BackupJobs      []config.BackupJobConfig
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
	AppVersion   string
	BuildTime    string
	ActiveWebDAV *WebDAVRuntimeConfig
	UpdateWebDAV func(WebDAVRuntimeConfig)
}

func serverBuildMetadata(cfg *ServerConfig) (string, string) {
	appVersion := AppVersion
	buildTime := AppBuildTime
	if cfg != nil {
		if strings.TrimSpace(cfg.AppVersion) != "" {
			appVersion = strings.TrimSpace(cfg.AppVersion)
		}
		if strings.TrimSpace(cfg.BuildTime) != "" {
			buildTime = strings.TrimSpace(cfg.BuildTime)
		}
	}
	return appVersion, buildTime
}

// NewServer creates a new API server
func NewServer(logger zerolog.Logger, cfg *ServerConfig) (*Server, error) {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(metrics.MetricsMiddleware) // Collect request metrics
	r.Use(zerologMiddleware(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(DefaultRequestTimeout * time.Second))
	// Keep observability endpoints reachable even when the request concurrency
	// budget is saturated by slow data operations.
	r.Use(throttleExceptPaths(DefaultMaxConcurrentRequests, "/health", "/api/v1/version", "/api/v1/metrics"))

	appVersion, buildTime := serverBuildMetadata(cfg)
	s := &Server{
		router:     r,
		logger:     logger,
		startTime:  time.Now(),
		appVersion: appVersion,
		buildTime:  buildTime,
	}
	requestip.SetTrustedProxyHops(config.Default().Server.TrustedProxyHops)
	if cfg != nil {
		s.afterPathRenamed = cfg.AfterPathRenamed
		s.afterPathDeleted = cfg.AfterPathDeleted
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
		if err := serverConnectDataplaneClient(s, context.Background(), s.dataplane, s.dataplaneConnectTimeout()); err != nil {
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

	// Initialize backup manager
	if cfg != nil && cfg.BackupRoot != "" {
		s.backupConfigured = true
		storageRoot := cfg.StorageRoot
		if storageRoot == "" && cfg.Config != nil {
			storageRoot = cfg.Config.Storage.Root
		}
		backupManager, err := backup.NewManager(backup.ManagerConfig{
			Root:        cfg.BackupRoot,
			StorageRoot: storageRoot,
			ConfigPath:  cfg.ConfigPath,
			Jobs:        cfg.BackupJobs,
			Notifier:    newBackupAlertNotifier(cfg.AlertMonitor, logger),
		})
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize backup manager")
		} else {
			s.backupManager = backupManager
			logger.Info().Str("path", cfg.BackupRoot).Msg("Backup manager initialized")
			if backupManager.StartScheduler(context.Background()) {
				logger.Info().Msg("Backup scheduler started")
			}
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
		userStore, _, err := auth.NewUserStore(cfg.AuthUsersFile)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize user store: %w", err)
		}
		s.userStore = userStore

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
		tokenRevocationStoreFile := filepath.Join(filepath.Dir(cfg.AuthUsersFile), "token-revocations.json")
		if err := s.tokenManager.EnablePersistence(tokenRevocationStoreFile); err != nil {
			return nil, fmt.Errorf("failed to initialize token revocation store: %w", err)
		}

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
		// Pass filesystem adapter to share handler
		var fsAdapter share.FileOpener
		if s.fs != nil {
			fsAdapter = &fileSystemAdapter{fs: s.fs}
		}
		s.shareHandler = share.NewHandler(shareStore, fsAdapter)
		if s.userStore != nil {
			s.shareHandler.SetUserStore(s.userStore)
		}
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
	if s.fs != nil {
		s.fs.SetPathChangeHooks(s.handlePathRenamed, s.handlePathDeleted)
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

func RejectCrossOriginUnsafeRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeHTTPMethod(r.Method) &&
			!hasBearerAuthorization(r) &&
			!sameOriginBrowserMetadata(r) {
			Forbidden(w, "cross-origin request rejected")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func hasBearerAuthorization(r *http.Request) bool {
	fields := strings.Fields(r.Header.Get("Authorization"))
	return len(fields) >= 2 && strings.EqualFold(fields[0], "Bearer")
}

func isUnsafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		"MKCOL", "COPY", "MOVE", "PROPPATCH", "LOCK", "UNLOCK":
		return true
	default:
		return false
	}
}

func sameOriginBrowserMetadata(r *http.Request) bool {
	if fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); fetchSite != "" {
		switch fetchSite {
		case "same-origin", "none":
		default:
			return false
		}
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return requestMetadataOriginMatches(r, origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return requestMetadataOriginMatches(r, referer)
	}
	return true
}

func requestMetadataOriginMatches(r *http.Request, rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	scheme := requestScheme(r)
	requestHost := normalizeHTTPHostForScheme(scheme, r.Host)
	sourceHost := normalizeHTTPHostForScheme(parsed.Scheme, parsed.Host)
	return requestHost != "" &&
		sourceHost != "" &&
		requestHost == sourceHost &&
		strings.EqualFold(parsed.Scheme, scheme)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if requestip.TrustedProxyHops() > 0 &&
		requestip.IsTrustedForwardedSource(requestip.RemoteIP(r.RemoteAddr)) &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return "https"
	}
	return "http"
}

func normalizeHTTPHostForScheme(scheme, rawHost string) string {
	host := strings.ToLower(strings.TrimSpace(rawHost))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return ""
	}

	if splitHost, port, err := net.SplitHostPort(host); err == nil {
		splitHost = strings.Trim(strings.TrimSuffix(strings.ToLower(splitHost), "."), "[]")
		if isDefaultPortForScheme(scheme, port) {
			return splitHost
		}
		return net.JoinHostPort(splitHost, port)
	}

	return strings.Trim(host, "[]")
}

func isDefaultPortForScheme(scheme, port string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return port == "80"
	case "https":
		return port == "443"
	default:
		return false
	}
}

func (s *Server) setupRoutes() {
	s.router.Use(RejectCrossOriginUnsafeRequests)

	// Public endpoints (no auth required)
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/api/v1/version", s.handleVersion)

	// Setup status (public, for first-run guidance without exposing passwords)
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
			r.With(s.authMw.OptionalAuth).Post("/logout", s.handleLogoutWithActivity)
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

		s.router.Route("/api/v1/public/shares", func(r chi.Router) {
			r.Get("/{id}", s.handleAccessShare)
			r.Post("/{id}/access", s.handleAccessShareWithPassword)
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
			r.With(s.authMw.RequireRole(auth.RoleAdmin)).Get("/metrics", s.handleMetrics)
		} else {
			r.Get("/diagnostics", s.handleDiagnostics)
			r.Get("/diagnostics-export", s.handleDiagnosticsExport)
			r.Get("/metrics", s.handleMetrics)
		}

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
			r.Get("/security-check", s.handleGetSecurityCheck)
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
			r.Get("/backups", s.handleListBackups)
			r.Get("/backups/{id}", s.handleGetBackup)
			r.Post("/backups/{id}/run", s.handleRunBackup)
			r.Post("/backups/{id}/restore", s.handleRunBackupRestore)
			r.Post("/backups/{id}/restore-drill", s.handleRunBackupRestoreDrill)
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
	if strings.ContainsRune(normalized, '\x00') {
		return "", errInvalidPath
	}

	// Clean the path first
	cleaned := path.Clean("/" + normalized)

	// Reject any path with .. segments while allowing legal names like foo..txt.
	if hasTraversalSegment(normalized) {
		return "", errInvalidPath
	}

	// Reject paths outside root
	if cleaned != "/" && !strings.HasPrefix(cleaned, "/") {
		return "", errInvalidPath
	}

	return cleaned, nil
}

func routePathAfterPrefix(r *http.Request, routePrefix string) (string, error) {
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, routePrefix) {
		return "", errInvalidPath
	}

	encodedPath := strings.TrimPrefix(escapedPath, routePrefix)
	if encodedPath == "" {
		return "/", nil
	}
	if !strings.HasPrefix(encodedPath, "/") {
		return "", errInvalidPath
	}

	decodedPath, err := url.PathUnescape(encodedPath)
	if err != nil {
		return "", errInvalidPath
	}
	if decodedPath == "" {
		return "/", nil
	}
	return decodedPath, nil
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
var errInvalidPath = errors.New("invalid path")

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
	if strings.TrimSpace(user.HomeDir) == "" {
		return "", true, errPathOutsideHomeDir
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

func (s *Server) filterActivityEntriesByHomeDir(ctx context.Context, entries []activity.Entry) ([]activity.Entry, error) {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return entries, nil
	}

	user := auth.GetUserFromContext(ctx)
	filtered := make([]activity.Entry, 0, len(entries))
	for _, entry := range entries {
		if user != nil && !user.CreatedAt.IsZero() && entry.Timestamp.Before(user.CreatedAt) {
			continue
		}
		if entry.Path != "" && !pathWithinBase(homeDir, entry.Path) {
			continue
		}
		filtered = append(filtered, sanitizeActivityEntryForHomeDir(entry, homeDir))
	}
	return filtered, nil
}

func sanitizeActivityEntryForHomeDir(entry activity.Entry, homeDir string) activity.Entry {
	if len(entry.Details) == 0 {
		return entry
	}

	sanitized := entry
	sanitized.Details = make(map[string]string, len(entry.Details))
	for key, value := range entry.Details {
		sanitized.Details[key] = value
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || (!strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, "\\")) {
			continue
		}
		detailPath, err := validatePath(trimmed)
		if err != nil || !pathWithinBase(homeDir, detailPath) {
			sanitized.Details[key] = ""
		}
	}
	return sanitized
}

func paginateActivityEntries(entries []activity.Entry, limit, offset int) []activity.Entry {
	if offset >= len(entries) {
		return []activity.Entry{}
	}

	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}

	page := make([]activity.Entry, end-offset)
	copy(page, entries[offset:end])
	return page
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

func normalizeHash(hash string) (string, error) {
	if err := validateHash(hash); err != nil {
		return "", err
	}
	return strings.ToLower(hash), nil
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

func isMutationRootPath(targetPath string) bool {
	return path.Clean(targetPath) == "/"
}

func forbiddenPathOutsideHome(w http.ResponseWriter) {
	Forbidden(w, "path is outside the assigned home directory")
}

func (s *Server) respondHomeDirFilterError(w http.ResponseWriter, action string, err error) {
	if errors.Is(err, errPathOutsideHomeDir) {
		forbiddenPathOutsideHome(w)
		return
	}
	s.respondInternalError(w, action, err)
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

func (s *Server) dataplaneConnectTimeout() time.Duration {
	cfg := s.currentConfig()
	if cfg == nil || cfg.DataPlane.Timeout <= 0 {
		return DefaultDataplaneConnectTimeout * time.Second
	}
	return cfg.DataPlane.Timeout
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

func (s *Server) ensureDataplaneConnected(ctx context.Context) bool {
	if s.dataplane == nil {
		return false
	}
	if s.dataplane.IsConnected() {
		return true
	}

	if err := serverConnectDataplaneClient(s, ctx, s.dataplane, s.dataplaneConnectTimeout()); err != nil {
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
		"version":   s.appVersion,
	}

	// Reflect dataplane availability in the overall health status when configured.
	if s.dataplane != nil {
		if !s.ensureDataplaneConnected(r.Context()) {
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
		s.favoritesConfiguredButUnavailable() ||
		s.webDAVConfiguredButUnavailable() {
		health["status"] = "degraded"
	}

	s.json(w, http.StatusOK, health)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	NewAPIResponse(map[string]any{
		"name":       AppName,
		"version":    s.appVersion,
		"build_time": s.buildTime,
		"go":         runtime.Version(),
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
	filePath, err := routePathAfterPrefix(r, "/api/v1/files")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path to prevent traversal attacks
	filePath, err = validatePath(filePath)
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
	filePath, err := routePathAfterPrefix(r, "/api/v1/files")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path
	filePath, err = validatePath(filePath)
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
	dirPath, err := routePathAfterPrefix(r, "/api/v1/directories")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path
	dirPath, err = validatePath(dirPath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if isMutationRootPath(dirPath) {
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
		if errors.As(err, new(*storage.VisibleMutationWarningError)) {
			markWorkspaceMutationWarningHeaders(w)
			s.LogActivityWithWarning(w, r, activity.ActionCreate, dirPath, map[string]string{
				"type":                "directory",
				"persistence_warning": "true",
			})
			NewAPIResponse(map[string]any{
				"path":    dirPath,
				"warning": true,
			}).WithMessage("directory created with persistence warning").Write(w, http.StatusCreated)
			return
		}
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
	filePath, err := routePathAfterPrefix(r, "/api/v1/files")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path
	filePath, err = validatePath(filePath)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if isMutationRootPath(filePath) {
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
		deleteWarningDetails := map[string]string{}
		hasWarning := false
		message := ""
		if errors.As(err, new(*storage.TrashDeleteWarningError)) {
			markTrashDeleteCleanupWarningHeaders(w)
			deleteWarningDetails["trash_cleanup_warning"] = "true"
			hasWarning = true
			message = "file deleted with trash cleanup warning"
		}
		if errors.As(err, new(*storage.DeleteCleanupWarningError)) {
			markDeleteCleanupWarningHeaders(w)
			deleteWarningDetails["cleanup_warning"] = "true"
			hasWarning = true
			if message == "" {
				message = "file deleted with cleanup warning"
			}
		}
		if errors.As(err, new(*storage.VisibleMutationWarningError)) {
			markWorkspaceMutationWarningHeaders(w)
			deleteWarningDetails["persistence_warning"] = "true"
			hasWarning = true
			if message == "" {
				message = "file deleted with persistence warning"
			}
		}
		if hasWarning {
			s.LogActivityWithWarning(w, r, activity.ActionDelete, filePath, deleteWarningDetails)
			NewAPIResponse(map[string]any{
				"path":    filePath,
				"warning": true,
			}).WithMessage(message).Write(w, http.StatusOK)
			return
		}
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
	filePath, err := routePathAfterPrefix(r, "/api/v1/download")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path
	filePath, err = validatePath(filePath)
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
		normalizedHash, err := normalizeHash(versionHash)
		if err != nil {
			badRequestInvalidHash(w)
			return
		}
		versionHash = normalizedHash

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

		versionETag := fmt.Sprintf(`"%s"`, versionHash)
		setUntrustedDownloadHeaders(w)
		w.Header().Set("ETag", versionETag)
		w.Header().Set("Cache-Control", "private, no-cache")
		if apiETagMatch(r.Header.Get("If-None-Match"), versionETag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if forceDownload {
			w.Header().Set("Content-Disposition", formatAttachmentHeader(path.Base(filePath)))
		}

		versionModTime := time.Time{}
		if info.ContentHash == versionHash {
			versionModTime = info.ModTime
		}

		trackingWriter := &apiDownloadResponseWriter{ResponseWriter: w}
		http.ServeContent(trackingWriter, r, path.Base(filePath), versionModTime, reader)
		if trackingWriter.writeErr == nil && trackingWriter.statusCode != http.StatusNotModified && trackingWriter.statusCode < http.StatusBadRequest {
			s.LogActivity(r, activity.ActionDownload, filePath, map[string]string{"hash": versionHash})
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

	// Open a snapshot so headers and bytes come from the same file handle.
	file, snapshotInfo, err := s.fs.OpenFileSnapshot(r.Context(), filePath)
	if err != nil {
		s.respondReadableOpenFileError(w, "open file", err, "cannot download directory")
		return
	}
	defer file.Close()

	// Set cache headers
	setUntrustedDownloadHeaders(w)
	if snapshotInfo.ContentHash != "" {
		w.Header().Set("ETag", fmt.Sprintf(`"%s"`, snapshotInfo.ContentHash))
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	if forceDownload {
		w.Header().Set("Content-Disposition", formatAttachmentHeader(path.Base(filePath)))
	}

	// Use http.ServeContent for proper Range support and content type detection
	trackingWriter := &apiDownloadResponseWriter{ResponseWriter: w}
	http.ServeContent(trackingWriter, r, path.Base(filePath), snapshotInfo.ModTime, file)
	if trackingWriter.writeErr == nil && trackingWriter.statusCode != http.StatusNotModified && trackingWriter.statusCode < http.StatusBadRequest {
		s.LogActivity(r, activity.ActionDownload, filePath, nil)
	}
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
	if isMutationRootPath(fromPath) {
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
	if isMutationRootPath(toPath) {
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
		if errors.As(err, new(*storage.VisibleMutationWarningError)) {
			markWorkspaceMutationWarningHeaders(w)
		} else {
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
	}

	action := activity.ActionMove
	if path.Dir(fromPath) == path.Dir(toPath) {
		action = activity.ActionRename
	}

	// Log activity
	s.LogActivityWithWarning(w, r, action, fromPath, map[string]string{"to": toPath})

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
	if isMutationRootPath(fromPath) {
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
	if isMutationRootPath(toPath) {
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
		if errors.As(err, new(*storage.VisibleMutationWarningError)) {
			markWorkspaceMutationWarningHeaders(w)
			s.LogActivityWithWarning(w, r, activity.ActionCopy, fromPath, map[string]string{
				"to":                  toPath,
				"persistence_warning": "true",
			})
			NewAPIResponse(map[string]any{
				"from":    fromPath,
				"to":      toPath,
				"warning": true,
			}).WithMessage("resource copied with persistence warning").Write(w, http.StatusCreated)
			return
		}
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
		var copyWarning error
		if err := s.fs.Mkdir(ctx, dstPath); err != nil {
			if errors.As(err, new(*storage.VisibleMutationWarningError)) {
				copyWarning = mergeCopyWarning(copyWarning, err)
			} else {
				return err
			}
		}

		children, err := s.fs.ReadDir(ctx, srcPath)
		if err != nil {
			return s.rollbackCopiedDirectory(dstPath, err)
		}

		for _, child := range children {
			childName := path.Base(child.Path)
			if err := s.copyResource(ctx, child.Path, path.Join(dstPath, childName)); err != nil {
				if errors.As(err, new(*storage.VisibleMutationWarningError)) {
					copyWarning = mergeCopyWarning(copyWarning, err)
					continue
				}
				return s.rollbackCopiedDirectory(dstPath, err)
			}
		}
		return copyWarning
	}

	return s.fs.Copy(ctx, srcPath, dstPath)
}

func mergeCopyWarning(existing, err error) error {
	if err == nil {
		return existing
	}
	if existing == nil {
		return err
	}
	return errors.Join(existing, err)
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
	filePath, err := routePathAfterPrefix(r, "/api/v1/versions")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// REM-5 fix: Validate path
	filePath, err = validatePath(filePath)
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
	normalizedHash, err := normalizeHash(hash)
	if err != nil {
		badRequestInvalidHash(w)
		return
	}
	hash = normalizedHash

	// Get path from query parameter
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		BadRequest(w, "path parameter is required")
		return
	}

	// REM-5 fix: Validate path
	filePath, err = validatePath(filePath)
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
		if errors.As(err, new(*storage.VisibleMutationWarningError)) {
			markWorkspaceMutationWarningHeaders(w)
			s.LogActivityWithWarning(w, r, activity.ActionRestore, filePath, map[string]string{"hash": hash, "persistence_warning": "true"})
			NewAPIResponse(map[string]any{
				"path":     filePath,
				"restored": hash,
				"warning":  true,
			}).WithMessage("version restored with persistence warning").Write(w, http.StatusOK)
			return
		}
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
	query := strings.TrimSpace(r.URL.Query().Get("q"))
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

	homeDir, scoped, err := s.currentUserHomeDir(r.Context())
	if err != nil {
		forbiddenPathOutsideHome(w)
		return
	}

	var results []*storage.SearchResult
	if scoped {
		results, err = s.fs.SearchWithinBase(r.Context(), homeDir, query, limit)
	} else {
		results, err = s.fs.Search(r.Context(), query, limit)
	}
	if err != nil {
		s.respondInternalError(w, "search files", err)
		return
	}
	if !scoped {
		results, err = s.filterSearchResultsByHomeDir(r.Context(), results)
		if err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
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
		"total_files_available":   false,
		"storage_stats_available": false,
		"disk_stats_available":    false,
	}

	if _, scoped, err := s.currentUserHomeDir(r.Context()); err != nil || scoped {
		NewAPIResponse(stats).Write(w, http.StatusOK)
		return
	}

	if s.fs != nil {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		defer cancel()
		if count, err := getFileCount(s.fs, ctx); err == nil {
			stats["total_files_available"] = true
			stats["total_files"] = count
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect file count for stats")
		}

		if diskStats, err := getDiskStats(s.fs); err == nil {
			stats["disk_stats_available"] = true
			addDiskStatsToMap(stats, diskStats)
		} else {
			s.logger.Warn().Err(err).Msg("failed to collect disk stats")
		}
	}

	// Get stats from data plane if connected
	if s.ensureDataplaneConnected(r.Context()) {
		ctx, cancel := context.WithTimeout(r.Context(), DefaultStatsTimeout*time.Second)
		defer cancel()
		if dpStats, err := s.dataplane.Stats(ctx); err == nil {
			stats["storage_stats_available"] = true
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

func addDiskStatsToMap(target map[string]any, stats *storage.DiskStats) {
	if stats == nil {
		return
	}
	target["disk_total"] = stats.TotalBytes
	target["disk_free"] = stats.FreeBytes
	target["disk_available"] = stats.AvailableBytes
	target["disk_used"] = stats.UsedBytes
	target["disk_usage_ratio"] = stats.UsageRatio
	if stats.FileSystemType != "" {
		target["disk_filesystem_type"] = stats.FileSystemType
	}
	target["disk_native_data_checksum_support"] = stats.NativeDataChecksumSupport
}

func (s *Server) alertsDiagnostics(cfg *config.Config) map[string]any {
	info := map[string]any{
		"enabled":            false,
		"runtime_available":  s.alertMonitor != nil,
		"webhook_configured": false,
	}
	if cfg != nil {
		info["enabled"] = cfg.Alerts.Enabled
		info["check_interval"] = cfg.Alerts.CheckInterval.String()
		info["threshold_pct"] = cfg.Alerts.ThresholdPct
		info["critical_pct"] = cfg.Alerts.CriticalPct
		info["min_free_bytes"] = cfg.Alerts.MinFreeBytes
		info["cooldown_period"] = cfg.Alerts.CooldownPeriod.String()
		info["webhook_configured"] = strings.TrimSpace(cfg.Alerts.WebhookURL) != ""
		method := strings.ToUpper(strings.TrimSpace(cfg.Alerts.WebhookMethod))
		if method == "" {
			method = http.MethodPost
		}
		info["webhook_method"] = method
	}

	if provider, ok := s.alertMonitor.(alertStatsProvider); ok {
		if stats := provider.LastStats(); stats != nil {
			info["last_level"] = string(stats.Level)
			info["last_checked_at"] = stats.CheckedAt.UTC().Format(time.RFC3339)
			info["last_used_pct"] = stats.UsedPct
			info["last_free_bytes"] = stats.FreeBytes
		}
	}

	return info
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	dataplaneConnected := s.ensureDataplaneConnected(r.Context())
	cfg := s.currentConfig()

	// Collect diagnostic information
	diag := map[string]any{
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"uptime":      time.Since(s.startTime).String(),
		"uptime_secs": int64(time.Since(s.startTime).Seconds()),
		"version": map[string]any{
			"name":       AppName,
			"version":    s.appVersion,
			"build_time": s.buildTime,
			"go":         runtime.Version(),
		},
	}

	// System status
	systemStatus := map[string]any{
		"filesystem_initialized":    s.fs != nil,
		"dataplane_connected":       dataplaneConnected,
		"thumbnail_service_ready":   s.thumbnail != nil,
		"maintenance_history_ready": s.maintenance != nil,
		"backup_manager_ready":      s.backupManager != nil,
		"activity_log_ready":        s.activity != nil,
	}
	if cfg != nil && cfg.WebDAV.Enabled {
		systemStatus["webdav_runtime_ready"] = !s.webDAVConfiguredButUnavailable()
	}
	if s.favoritesConfigured {
		systemStatus["favorites_store_ready"] = !s.favoritesConfiguredButUnavailable()
	}
	diag["system"] = systemStatus
	diag["alerts"] = s.alertsDiagnostics(cfg)

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
			fsStats["trash_stats_available"] = true
			fsStats["trash_items"] = trashCount
			fsStats["trash_size"] = trashSize
		} else {
			fsStats["trash_stats_available"] = false
			s.logger.Warn().Err(err).Msg("failed to collect trash stats for diagnostics")
		}

		if diskStats, err := getDiskStats(s.fs); err == nil {
			fsStats["disk_stats_available"] = true
			addDiskStatsToMap(fsStats, diskStats)
		} else {
			fsStats["disk_stats_available"] = false
			s.logger.Warn().Err(err).Msg("failed to collect disk stats for diagnostics")
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
	for i, hash := range req.Hashes {
		normalizedHash, err := normalizeHash(hash)
		if err != nil {
			badRequestInvalidHash(w)
			return
		}
		req.Hashes[i] = normalizedHash
	}

	if !s.ensureDataplaneConnected(r.Context()) {
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
		scrubRecord, err = startScrub(s.maintenance)
		if err != nil {
			if errors.Is(err, maintenance.ErrScrubAlreadyRunning) {
				Conflict(w, "scrub is already running")
				return
			}
			if scrubRecord == nil {
				s.respondInternalError(w, "persist scrub start", err)
				return
			}
			s.logger.Warn().Err(err).Msg("scrub start persistence warning")
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
		if e.Message != "" {
			s.logger.Warn().
				Str("hash", e.Hash).
				Str("error_type", e.ErrorType).
				Str("scrub_error", e.Message).
				Msg("scrub reported object error")
		}
		message := publicScrubObjectErrorMessage(e.ErrorType)
		errors = append(errors, map[string]any{
			"hash":       e.Hash,
			"error_type": e.ErrorType,
			"message":    message,
		})
		maintErrors = append(maintErrors, maintenance.ScrubError{
			Hash:      e.Hash,
			ErrorType: e.ErrorType,
			Message:   message,
		})
	}

	scrubPersistenceWarning := false

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
			scrubPersistenceWarning = true
			s.logger.Warn().Err(err).Msg("failed to persist completed scrub result")
		}
	}

	response := map[string]any{
		"total_objects":     result.TotalObjects,
		"valid_objects":     result.ValidObjects,
		"corrupted_objects": result.CorruptedObjects,
		"missing_objects":   result.MissingObjects,
		"total_size":        result.TotalSize,
		"duration_ms":       result.DurationMs,
		"errors":            errors,
	}
	apiResponse := NewAPIResponse(response)
	if scrubPersistenceWarning {
		markScrubResultPersistenceWarningHeaders(w)
		response["warning"] = true
		apiResponse = apiResponse.WithMessage("scrub completed with persistence warning")
	}
	apiResponse.Write(w, http.StatusOK)
}

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maxObjectsCursorLength {
		BadRequest(w, fmt.Sprintf("cursor exceeds maximum length (%d bytes)", maxObjectsCursorLength))
		return
	}
	if cursor != "" {
		normalizedCursor, err := normalizeHash(cursor)
		if err != nil {
			BadRequest(w, "cursor must be a 64-character hex object hash")
			return
		}
		cursor = normalizedCursor
	}
	limitStr := r.URL.Query().Get("limit")
	var limit uint32 = maxListObjectsLimit
	if limitStr != "" {
		l, err := parseUint32(limitStr)
		if err != nil || l == 0 || l > maxListObjectsLimit {
			BadRequest(w, fmt.Sprintf("limit parameter must be between 1 and %d", maxListObjectsLimit))
			return
		}
		limit = l
	}

	if !s.ensureDataplaneConnected(r.Context()) {
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
		hours, err := strconv.ParseInt(gpStr, 10, 64)
		if err != nil || hours < 0 {
			BadRequest(w, "grace_period_hours must be a non-negative integer")
			return
		}
		if hours > maxGCGracePeriodHours {
			BadRequest(w, fmt.Sprintf("grace_period_hours must be between 0 and %d", maxGCGracePeriodHours))
			return
		}
		gracePeriod = time.Duration(hours) * time.Hour
	}

	// Delete unreferenced objects only when dry_run is explicitly false.
	dryRun := true
	if rawDryRun := r.URL.Query().Get("dry_run"); rawDryRun != "" {
		parsedDryRun, err := strconv.ParseBool(rawDryRun)
		if err != nil {
			BadRequest(w, "dry_run parameter must be a boolean")
			return
		}
		dryRun = parsedDryRun
	}

	if !s.ensureDataplaneConnected(r.Context()) {
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
			"message":    publicScrubObjectErrorMessage(e.ErrorType),
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
	cfg := s.currentConfig()
	export := map[string]any{
		"export_time": time.Now().Format(time.RFC3339),
		"version":     s.appVersion,
		"build_time":  s.buildTime,
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
	export["alerts"] = s.alertsDiagnostics(cfg)

	// Filesystem stats (sanitized)
	if s.fs != nil {
		fsStats := map[string]any{}

		// Trash stats
		if trashCount, trashSize, err := getTrashStats(s.fs, r.Context()); err == nil {
			fsStats["trash_stats_available"] = true
			fsStats["trash_count"] = trashCount
			fsStats["trash_size"] = trashSize
		} else {
			fsStats["trash_stats_available"] = false
			s.logger.Warn().Err(err).Msg("failed to collect trash stats for diagnostics export")
		}

		if diskStats, err := getDiskStats(s.fs); err == nil {
			fsStats["disk_stats_available"] = true
			addDiskStatsToMap(fsStats, diskStats)
		} else {
			fsStats["disk_stats_available"] = false
			s.logger.Warn().Err(err).Msg("failed to collect disk stats for diagnostics export")
		}

		export["filesystem"] = fsStats
	}

	// Dataplane stats (sanitized)
	if s.ensureDataplaneConnected(r.Context()) {
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
	w.Header().Set("Content-Disposition", formatAttachmentHeader(filename))
	s.json(w, http.StatusOK, export)
}

func sanitizeScrubErrorMessage(message string) string {
	if message == "" {
		return ""
	}
	return scrubFailurePublicMessage
}

func publicScrubObjectErrorMessage(errorType string) string {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "corrupted":
		return scrubObjectCorruptedPublicMessage
	case "missing":
		return scrubObjectMissingPublicMessage
	case "io_error":
		return scrubObjectReadPublicMessage
	default:
		return scrubObjectUnknownPublicMessage
	}
}

func formatAttachmentHeader(filename string) string {
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); value != "" {
		return value
	}
	return "attachment"
}

func setUntrustedDownloadHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", untrustedDownloadContentSecurityPolicy)
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
	started    bool
	statusCode int
	writeErr   error
}

func (w *apiDownloadResponseWriter) WriteHeader(statusCode int) {
	w.started = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *apiDownloadResponseWriter) Write(p []byte) (int, error) {
	w.started = true
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writeErr = err
	}
	return n, err
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

	newPath := r.URL.Query().Get("path")
	if newPath != "" {
		var err error
		newPath, err = validatePath(newPath)
		if err != nil {
			badRequestInvalidPath(w)
			return
		}
		if err := s.authorizeUserPath(r.Context(), newPath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
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

	activityPath := item.OriginalPath

	if newPath != "" {
		// Restore to custom path
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

	activityDetails := map[string]string(nil)
	message := "file restored successfully"
	responseData := map[string]any{
		"id":       id,
		"restored": true,
	}
	if err := s.restoreDeletedPathState(item.OriginalPath, activityPath, item.RestoreData); err != nil {
		markTrashRestoreMetadataWarningHeaders(w)
		s.logger.Warn().Err(err).Str("path", activityPath).Msg("failed to restore trash-linked metadata")
		activityDetails = map[string]string{"metadata_restore": "failed"}
		message = "file restored with metadata warning"
		responseData["warning"] = true
	}

	// Log activity
	s.LogActivityWithWarning(w, r, activity.ActionTrashRestore, activityPath, activityDetails)

	NewAPIResponse(responseData).WithMessage(message).Write(w, http.StatusOK)
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
	activityPath := item.OriginalPath

	if err := s.fs.DeleteFromTrash(r.Context(), id); err != nil {
		var warningErr *storage.TrashDeleteWarningError
		if errors.As(err, &warningErr) {
			markTrashDeleteCleanupWarningHeaders(w)
			s.LogActivityWithWarning(w, r, activity.ActionTrashDelete, activityPath, map[string]string{"cleanup_warning": "true"})
			NewAPIResponse(map[string]any{
				"id":      id,
				"deleted": true,
				"warning": true,
			}).WithMessage("item permanently deleted with cleanup warning").Write(w, http.StatusOK)
			return
		}
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
		cleanupWarning := false
		for _, item := range items {
			if item == nil || !pathWithinBase(homeDir, item.OriginalPath) {
				continue
			}
			if err := s.fs.DeleteFromTrash(r.Context(), item.ID); err != nil {
				var warningErr *storage.TrashDeleteWarningError
				if errors.As(err, &warningErr) {
					deletedCount++
					cleanupWarning = true
					continue
				}
				if deletedCount > 0 {
					details := map[string]string{
						"count":   strconv.Itoa(deletedCount),
						"partial": "true",
					}
					response := map[string]any{
						"deleted_count": deletedCount,
						"partial":       true,
					}
					message := "trash emptied partially"
					if cleanupWarning {
						markTrashDeleteCleanupWarningHeaders(w)
						details["cleanup_warning"] = "true"
						response["warning"] = true
						message = "trash emptied partially with cleanup warning"
					}
					s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", details)
					NewAPIResponse(response).WithMessage(message).Write(w, http.StatusOK)
					return
				}
				s.respondInternalError(w, "empty trash", err)
				return
			}
			deletedCount++
		}

		details := map[string]string{"count": strconv.Itoa(deletedCount)}
		response := map[string]any{
			"deleted_count": deletedCount,
			"partial":       false,
		}
		message := "trash emptied successfully"
		if cleanupWarning {
			markTrashDeleteCleanupWarningHeaders(w)
			details["cleanup_warning"] = "true"
			response["warning"] = true
			message = "trash emptied with cleanup warning"
		}
		s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", details)
		NewAPIResponse(response).WithMessage(message).Write(w, http.StatusOK)
		return
	}

	count, err := s.fs.EmptyTrash(r.Context())
	if err != nil {
		var warningErr *storage.TrashDeleteWarningError
		if errors.As(err, &warningErr) {
			markTrashDeleteCleanupWarningHeaders(w)
			details := map[string]string{
				"count":           strconv.Itoa(count),
				"cleanup_warning": "true",
			}
			response := map[string]any{
				"deleted_count": count,
				"warning":       true,
			}
			message := "trash emptied with cleanup warning"
			if warningErr.Partial() {
				details["partial"] = "true"
				response["partial"] = true
				message = "trash emptied partially with cleanup warning"
			} else {
				response["partial"] = false
			}
			s.LogActivityWithWarning(w, r, activity.ActionTrashEmpty, "", details)
			NewAPIResponse(response).WithMessage(message).Write(w, http.StatusOK)
			return
		}
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

	filePath, err := routePathAfterPrefix(r, "/api/v1/thumbnails")
	if err != nil {
		badRequestInvalidPath(w)
		return
	}

	// Validate path
	filePath, err = validatePath(filePath)
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
	size := thumbnail.SizeMedium
	if sizeParam != "" {
		switch sizeParam {
		case "small", "s":
			size = thumbnail.SizeSmall
		case "medium", "m":
			size = thumbnail.SizeMedium
		case "large", "l":
			size = thumbnail.SizeLarge
		default:
			BadRequest(w, "size parameter must be one of: small, medium, large")
			return
		}
	}

	reader, err := s.fs.OpenFile(r.Context(), filePath)
	if err != nil {
		s.respondReadableOpenFileError(w, "thumbnail open file", err, "path is a directory")
		return
	}
	defer reader.Close()
	info, err := reader.Stat()
	if err != nil {
		s.respondInternalError(w, "thumbnail stat open file", err)
		return
	}
	if maxThumbnailSourceBytes > 0 && info.Size() > maxThumbnailSourceBytes {
		BadRequest(w, "source image too large to thumbnail")
		return
	}
	if s.beforeThumbnailRead != nil {
		if err := s.beforeThumbnailRead(filePath); err != nil {
			s.respondInternalError(w, "prepare thumbnail read", err)
			return
		}
	}
	contentHash, err := hashReadSeeker(reader)
	if err != nil {
		s.respondInternalError(w, "hash thumbnail source", err)
		return
	}
	thumbnailETag := fmt.Sprintf(`"thumb-%s-%s"`, contentHash, size)
	setUntrustedDownloadHeaders(w)
	w.Header().Set("ETag", thumbnailETag)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "private, no-cache")
	if apiETagMatch(r.Header.Get("If-None-Match"), thumbnailETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Generate or retrieve cached thumbnail
	data, err := s.thumbnail.GetThumbnailVersioned(r.Context(), filePath, contentHash, size, reader)
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
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func apiETagMatch(headerValue, currentETag string) bool {
	for _, candidate := range strings.Split(headerValue, ",") {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "*" || trimmed == currentETag {
			return true
		}
	}
	return false
}

func hashReadSeeker(reader io.ReadSeeker) (string, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := blake3.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
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

	if currentUserFilter := s.currentActivityUserFilter(r); currentUserFilter != "" {
		entries, _ := s.activity.List(s.activity.Count(), 0, activity.ActionType(actionFilter), currentUserFilter)
		entries, err := s.filterActivityEntriesByHomeDir(r.Context(), entries)
		if err != nil {
			s.respondHomeDirFilterError(w, "filter activity by home directory", err)
			return
		}

		NewAPIResponse(map[string]any{
			"items":  paginateActivityEntries(entries, limit, offset),
			"total":  len(entries),
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
		entries, err := s.filterActivityEntriesByHomeDir(r.Context(), entries)
		if err != nil {
			s.respondHomeDirFilterError(w, "filter activity stats by home directory", err)
			return
		}
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
	currentTime := apiTimeNow()
	today := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
	todayCount := 0

	for _, entry := range entries {
		actionCounts[entry.Action]++
		if entry.User != "" {
			userCounts[entry.User]++
		}
		if !entry.Timestamp.Before(today) {
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

func markTrashRestoreMetadataWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == trashRestoreMetadataWarningHeader {
			return
		}
	}
	headers.Add("Warning", trashRestoreMetadataWarningHeader)
}

func markScrubResultPersistenceWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == scrubResultPersistenceWarningHeader {
			return
		}
	}
	headers.Add("Warning", scrubResultPersistenceWarningHeader)
}

func markWorkspaceMutationWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == workspaceMutationWarningHeader {
			return
		}
	}
	headers.Add("Warning", workspaceMutationWarningHeader)
}

func markTrashDeleteCleanupWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == trashDeleteCleanupWarningHeader {
			return
		}
	}
	headers.Add("Warning", trashDeleteCleanupWarningHeader)
}

func markDeleteCleanupWarningHeaders(w http.ResponseWriter) {
	if w == nil {
		return
	}
	headers := w.Header()
	for _, warningValue := range headers.Values("Warning") {
		if warningValue == deleteCleanupWarningHeader {
			return
		}
	}
	headers.Add("Warning", deleteCleanupWarningHeader)
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

type securityCheckStatus string

const (
	securityCheckPass    securityCheckStatus = "pass"
	securityCheckWarning securityCheckStatus = "warning"
	securityCheckBlock   securityCheckStatus = "block"
)

type securityCheckItem struct {
	ID      string                 `json:"id"`
	Status  securityCheckStatus    `json:"status"`
	Title   string                 `json:"title"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

type securityCheckResponse struct {
	Status      securityCheckStatus    `json:"status"`
	GeneratedAt time.Time              `json:"generated_at"`
	Checks      []securityCheckItem    `json:"checks"`
	Request     map[string]interface{} `json:"request"`
	Config      map[string]interface{} `json:"config"`
}

func securityCheckOverallStatus(checks []securityCheckItem) securityCheckStatus {
	status := securityCheckPass
	for _, check := range checks {
		switch check.Status {
		case securityCheckBlock:
			return securityCheckBlock
		case securityCheckWarning:
			status = securityCheckWarning
		}
	}
	return status
}

func securityListenHostIsLoopback(host string) bool {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" || trimmed == "*" {
		return false
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
	}
	trimmed = strings.TrimSuffix(trimmed, ".")
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	if parsed := net.ParseIP(trimmed); parsed != nil {
		return parsed.IsLoopback()
	}
	return false
}

func securityTCPAddressHost(address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.Count(trimmed, ":") == 0 {
		return trimmed
	}
	return ""
}

func securityShareBaseURLUsesHTTPS(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	return err == nil && strings.EqualFold(parsed.Scheme, "https")
}

func (s *Server) handleGetSecurityCheck(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil {
		ServiceUnavailable(w, "settings not available")
		return
	}

	requestSchemeValue := requestScheme(r)
	remoteIP := requestip.RemoteIP(r.RemoteAddr)
	trustedForwardedSource := requestip.IsTrustedForwardedSource(remoteIP)
	serverLoopback := securityListenHostIsLoopback(cfg.Server.Host)
	dataplaneHost := securityTCPAddressHost(cfg.DataPlane.GRPCAddress)
	dataplaneLoopback := securityListenHostIsLoopback(dataplaneHost)
	initialPasswordFile := filepath.Join(filepath.Dir(cfg.Auth.UsersFile), "initial-password.txt")

	checks := make([]securityCheckItem, 0, 8)

	if s.authEnabled {
		checks = append(checks, securityCheckItem{
			ID:      "auth_enabled",
			Status:  securityCheckPass,
			Title:   "Web 登录认证已启用",
			Message: "管理界面需要账号登录。",
		})
	} else {
		checks = append(checks, securityCheckItem{
			ID:      "auth_enabled",
			Status:  securityCheckBlock,
			Title:   "Web 登录认证未启用",
			Message: "公网访问前必须启用账号登录。",
		})
	}

	if requestSchemeValue == "https" {
		checks = append(checks, securityCheckItem{
			ID:      "https_request",
			Status:  securityCheckPass,
			Title:   "当前访问已使用 HTTPS",
			Message: "浏览器请求已通过 TLS 或受信代理转发为 HTTPS。",
			Details: map[string]interface{}{
				"direct_tls":               r.TLS != nil,
				"forwarded_proto":          strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
				"trusted_forwarded_source": trustedForwardedSource,
			},
		})
	} else {
		checks = append(checks, securityCheckItem{
			ID:      "https_request",
			Status:  securityCheckWarning,
			Title:   "当前访问不是 HTTPS",
			Message: "公网访问前应通过内置 TLS 或受信反向代理提供 HTTPS。",
			Details: map[string]interface{}{
				"direct_tls":               r.TLS != nil,
				"forwarded_proto":          strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
				"trusted_forwarded_source": trustedForwardedSource,
			},
		})
	}

	if cfg.Server.TLS.Enabled || cfg.Server.TrustedProxyHops > 0 {
		checks = append(checks, securityCheckItem{
			ID:      "trusted_proxy_or_tls",
			Status:  securityCheckPass,
			Title:   "HTTPS 信任边界已配置",
			Message: "已启用内置 TLS，或已配置受信代理层数用于识别反向代理转发头。",
			Details: map[string]interface{}{
				"tls_enabled":           cfg.Server.TLS.Enabled,
				"trusted_proxy_hops":    cfg.Server.TrustedProxyHops,
				"trusted_remote_source": trustedForwardedSource,
			},
		})
	} else {
		checks = append(checks, securityCheckItem{
			ID:      "trusted_proxy_or_tls",
			Status:  securityCheckWarning,
			Title:   "未配置 HTTPS 信任边界",
			Message: "如果通过反向代理发布公网，请将受信代理层数设为实际代理层数；如果直接提供 HTTPS，请启用 TLS。",
			Details: map[string]interface{}{
				"tls_enabled":        cfg.Server.TLS.Enabled,
				"trusted_proxy_hops": cfg.Server.TrustedProxyHops,
			},
		})
	}

	if serverLoopback {
		checks = append(checks, securityCheckItem{
			ID:      "server_listen",
			Status:  securityCheckPass,
			Title:   "Web 服务仅监听本机地址",
			Message: "适合放在反向代理后方，仅由本机代理转发公网流量。",
			Details: map[string]interface{}{
				"host": cfg.Server.Host,
				"port": cfg.Server.Port,
			},
		})
	} else {
		status := securityCheckWarning
		message := "Web 服务当前监听非本机地址；公网部署时建议只监听 127.0.0.1 或 ::1，并由反向代理对外暴露。"
		if !s.authEnabled {
			status = securityCheckBlock
			message = "Web 服务监听非本机地址且登录认证未启用，公网访问前必须修复。"
		}
		checks = append(checks, securityCheckItem{
			ID:      "server_listen",
			Status:  status,
			Title:   "Web 服务监听范围偏宽",
			Message: message,
			Details: map[string]interface{}{
				"host": cfg.Server.Host,
				"port": cfg.Server.Port,
			},
		})
	}

	if dataplaneLoopback {
		checks = append(checks, securityCheckItem{
			ID:      "dataplane_listen",
			Status:  securityCheckPass,
			Title:   "数据面 gRPC 仅监听本机",
			Message: "数据面接口不会直接暴露到外部网络。",
			Details: map[string]interface{}{
				"grpc_address": cfg.DataPlane.GRPCAddress,
			},
		})
	} else {
		checks = append(checks, securityCheckItem{
			ID:      "dataplane_listen",
			Status:  securityCheckBlock,
			Title:   "数据面 gRPC 不应暴露外网",
			Message: "请将 dataplane.grpc_address 绑定到 127.0.0.1 或 ::1，并通过 Web 控制面访问文件能力。",
			Details: map[string]interface{}{
				"grpc_address": cfg.DataPlane.GRPCAddress,
			},
		})
	}

	switch {
	case !cfg.WebDAV.Enabled:
		checks = append(checks, securityCheckItem{
			ID:      "webdav_auth",
			Status:  securityCheckPass,
			Title:   "WebDAV 未启用",
			Message: "当前没有额外的 WebDAV 暴露面。",
		})
	case strings.EqualFold(cfg.WebDAV.AuthType, "basic"):
		checks = append(checks, securityCheckItem{
			ID:      "webdav_auth",
			Status:  securityCheckPass,
			Title:   "WebDAV 已启用认证",
			Message: "WebDAV 入口使用 Basic 认证。",
			Details: map[string]interface{}{
				"prefix":    cfg.WebDAV.Prefix,
				"auth_type": cfg.WebDAV.AuthType,
				"read_only": cfg.WebDAV.ReadOnly,
			},
		})
	case strings.EqualFold(cfg.WebDAV.AuthType, "none") && serverLoopback:
		checks = append(checks, securityCheckItem{
			ID:      "webdav_auth",
			Status:  securityCheckWarning,
			Title:   "WebDAV 当前无独立认证",
			Message: "当前 Web 服务仅监听本机；如经反向代理公开 WebDAV，请在代理层或 WebDAV 配置中启用认证。",
			Details: map[string]interface{}{
				"prefix":    cfg.WebDAV.Prefix,
				"auth_type": cfg.WebDAV.AuthType,
				"read_only": cfg.WebDAV.ReadOnly,
			},
		})
	default:
		checks = append(checks, securityCheckItem{
			ID:      "webdav_auth",
			Status:  securityCheckBlock,
			Title:   "WebDAV 暴露面缺少认证",
			Message: "WebDAV 已启用但认证方式不是 basic，公网访问前必须启用认证或关闭 WebDAV。",
			Details: map[string]interface{}{
				"prefix":    cfg.WebDAV.Prefix,
				"auth_type": cfg.WebDAV.AuthType,
				"read_only": cfg.WebDAV.ReadOnly,
			},
		})
	}

	switch {
	case !cfg.Share.Enabled:
		checks = append(checks, securityCheckItem{
			ID:      "share_base_url",
			Status:  securityCheckPass,
			Title:   "分享功能未启用",
			Message: "当前不会生成公开分享链接。",
		})
	case securityShareBaseURLUsesHTTPS(cfg.Share.BaseURL):
		checks = append(checks, securityCheckItem{
			ID:      "share_base_url",
			Status:  securityCheckPass,
			Title:   "分享基础 URL 使用 HTTPS",
			Message: "新分享链接会使用 HTTPS 基础地址。",
			Details: map[string]interface{}{
				"base_url": cfg.Share.BaseURL,
			},
		})
	case strings.TrimSpace(cfg.Share.BaseURL) == "" && requestSchemeValue == "https":
		checks = append(checks, securityCheckItem{
			ID:      "share_base_url",
			Status:  securityCheckWarning,
			Title:   "分享基础 URL 未固定",
			Message: "当前访问是 HTTPS，但建议配置固定的公网基础 URL，避免从内网地址生成分享链接。",
		})
	default:
		checks = append(checks, securityCheckItem{
			ID:      "share_base_url",
			Status:  securityCheckWarning,
			Title:   "分享基础 URL 未使用 HTTPS",
			Message: "公开分享链接应使用 HTTPS 基础地址。",
			Details: map[string]interface{}{
				"base_url": cfg.Share.BaseURL,
			},
		})
	}

	if _, err := os.Stat(initialPasswordFile); err == nil {
		checks = append(checks, securityCheckItem{
			ID:      "initial_password_file",
			Status:  securityCheckBlock,
			Title:   "初始管理员密码文件仍存在",
			Message: "请完成首次登录并确认该文件已被删除；公网访问前不要保留初始密码文件。",
			Details: map[string]interface{}{
				"path": initialPasswordFile,
			},
		})
	} else if errors.Is(err, os.ErrNotExist) {
		checks = append(checks, securityCheckItem{
			ID:      "initial_password_file",
			Status:  securityCheckPass,
			Title:   "未发现初始密码文件",
			Message: "初始管理员密码文件已不存在。",
		})
	} else {
		checks = append(checks, securityCheckItem{
			ID:      "initial_password_file",
			Status:  securityCheckWarning,
			Title:   "无法确认初始密码文件状态",
			Message: "请在服务器上确认 initial-password.txt 不存在。",
			Details: map[string]interface{}{
				"path":  initialPasswordFile,
				"error": err.Error(),
			},
		})
	}

	resp := securityCheckResponse{
		Status:      securityCheckOverallStatus(checks),
		GeneratedAt: apiTimeNow(),
		Checks:      checks,
		Request: map[string]interface{}{
			"scheme":                   requestSchemeValue,
			"direct_tls":               r.TLS != nil,
			"host":                     r.Host,
			"remote_ip":                remoteIP,
			"trusted_forwarded_source": trustedForwardedSource,
			"forwarded_proto":          strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
		},
		Config: map[string]interface{}{
			"auth_enabled":        s.authEnabled,
			"server_host":         cfg.Server.Host,
			"server_port":         cfg.Server.Port,
			"tls_enabled":         cfg.Server.TLS.Enabled,
			"trusted_proxy_hops":  cfg.Server.TrustedProxyHops,
			"dataplane_grpc_addr": cfg.DataPlane.GRPCAddress,
			"webdav_enabled":      cfg.WebDAV.Enabled,
			"webdav_auth_type":    cfg.WebDAV.AuthType,
			"share_enabled":       cfg.Share.Enabled,
		},
	}

	NewAPIResponse(resp).Write(w, http.StatusOK)
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
			"host":               cfg.Server.Host,
			"port":               cfg.Server.Port,
			"read_timeout":       cfg.Server.ReadTimeout.String(),
			"write_timeout":      cfg.Server.WriteTimeout.String(),
			"idle_timeout":       cfg.Server.IdleTimeout.String(),
			"trusted_proxy_hops": cfg.Server.TrustedProxyHops,
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
			"auto_versioned_extensions": settingsStringSlice(cfg.Storage.Versioning.AutoVersionedExtensions),
			"auto_versioned_filenames":  settingsStringSlice(cfg.Storage.Versioning.AutoVersionedFilenames),
			"max_versioned_size":        cfg.Storage.Versioning.MaxVersionedSize,
		},
		"webdav": map[string]interface{}{
			"enabled":         cfg.WebDAV.Enabled,
			"runtime_enabled": s.currentActiveWebDAV().Enabled,
			"prefix":          cfg.WebDAV.Prefix,
			"read_only":       cfg.WebDAV.ReadOnly,
			"auth_type":       cfg.WebDAV.AuthType,
			"username":        cfg.WebDAV.Username,
		},
		"share": map[string]interface{}{
			"enabled":  cfg.Share.Enabled,
			"base_url": cfg.Share.BaseURL,
		},
		"favorites": map[string]interface{}{
			"enabled":           cfg.Favorites.Enabled,
			"runtime_available": cfg.Favorites.Enabled && s.favoritesRuntimeReady(),
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
			"webhook_headers": settingsStringSlice(cfg.Alerts.WebhookHeaders),
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
	Host             *string                  `json:"host,omitempty"`
	Port             *int                     `json:"port,omitempty"`
	ReadTimeout      *string                  `json:"read_timeout,omitempty"`
	WriteTimeout     *string                  `json:"write_timeout,omitempty"`
	IdleTimeout      *string                  `json:"idle_timeout,omitempty"`
	TrustedProxyHops *int                     `json:"trusted_proxy_hops,omitempty"`
	TLS              *ServerTLSSettingsUpdate `json:"tls,omitempty"`
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

func settingsUpdateMayRequireRestart(req UpdateSettingsRequest, currentConfig, updatedConfig config.Config) bool {
	if req.Server != nil {
		if req.Server.Host != nil && currentConfig.Server.Host != updatedConfig.Server.Host {
			return true
		}
		if req.Server.Port != nil && currentConfig.Server.Port != updatedConfig.Server.Port {
			return true
		}
		if req.Server.ReadTimeout != nil && currentConfig.Server.ReadTimeout != updatedConfig.Server.ReadTimeout {
			return true
		}
		if req.Server.WriteTimeout != nil && currentConfig.Server.WriteTimeout != updatedConfig.Server.WriteTimeout {
			return true
		}
		if req.Server.IdleTimeout != nil && currentConfig.Server.IdleTimeout != updatedConfig.Server.IdleTimeout {
			return true
		}
		if req.Server.TLS != nil && serverTLSUpdateMayRequireRestart(*req.Server.TLS, currentConfig, updatedConfig) {
			return true
		}
	}

	if req.CDC != nil && cdcUpdateMayRequireRestart(*req.CDC, currentConfig, updatedConfig) {
		return true
	}

	return false
}

func serverTLSUpdateMayRequireRestart(req ServerTLSSettingsUpdate, currentConfig, updatedConfig config.Config) bool {
	return (req.Enabled != nil && currentConfig.Server.TLS.Enabled != updatedConfig.Server.TLS.Enabled) ||
		(req.CertFile != nil && currentConfig.Server.TLS.CertFile != updatedConfig.Server.TLS.CertFile) ||
		(req.KeyFile != nil && currentConfig.Server.TLS.KeyFile != updatedConfig.Server.TLS.KeyFile) ||
		(req.AutoGenerate != nil && currentConfig.Server.TLS.AutoGenerate != updatedConfig.Server.TLS.AutoGenerate) ||
		(req.CertDir != nil && currentConfig.Server.TLS.CertDir != updatedConfig.Server.TLS.CertDir)
}

func cdcUpdateMayRequireRestart(req CDCSettingsUpdate, currentConfig, updatedConfig config.Config) bool {
	return (req.MinChunkSize != nil && currentConfig.DataPlane.CDC.MinChunkSize != updatedConfig.DataPlane.CDC.MinChunkSize) ||
		(req.AvgChunkSize != nil && currentConfig.DataPlane.CDC.AvgChunkSize != updatedConfig.DataPlane.CDC.AvgChunkSize) ||
		(req.MaxChunkSize != nil && currentConfig.DataPlane.CDC.MaxChunkSize != updatedConfig.DataPlane.CDC.MaxChunkSize)
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
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

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
		if req.Server.TrustedProxyHops != nil {
			if *req.Server.TrustedProxyHops < 0 {
				BadRequest(w, "invalid server.trusted_proxy_hops")
				return
			}
			updatedConfig.Server.TrustedProxyHops = *req.Server.TrustedProxyHops
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
			if *req.Retention.MaxVersions < 0 {
				BadRequest(w, "invalid retention.max_versions")
				return
			}
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
				cleaned = append(cleaned, strings.ToLower(trimmed))
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
			updatedConfig.Share.BaseURL = strings.TrimSpace(*req.Share.BaseURL)
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
			updatedConfig.Alerts.WebhookURL = strings.TrimSpace(*req.Alerts.WebhookURL)
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

	var preparedWebDAV *WebDAVRuntimeConfig
	if req.WebDAV != nil {
		resolvedWebDAV, resolveErr := prepareWebDAVRuntimeConfig(updatedConfig)
		if resolveErr != nil {
			s.logger.Error().Err(resolveErr).Msg("failed to resolve generated WebDAV password for updated config")
			ServiceUnavailable(w, "webdav credentials unavailable")
			return
		}
		preparedWebDAV = resolvedWebDAV
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
	s.applyRuntimeSettings(r.Context(), req, updatedConfig, preparedDataplane, preparedWebDAV)
	preparedDataplane = nil

	s.logger.Info().Msg("settings updated and saved")

	message := "settings updated"
	if settingsUpdateMayRequireRestart(req, *currentConfig, updatedConfig) {
		message = "settings updated, some changes may require restart"
	}

	NewAPIResponse(nil).WithMessage(message).Write(w, http.StatusOK)
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

func (s *Server) applyRuntimeSettings(ctx context.Context, req UpdateSettingsRequest, cfg config.Config, preparedDataplane *dataplane.Client, preparedWebDAV *WebDAVRuntimeConfig) {
	if req.Server != nil && req.Server.TrustedProxyHops != nil {
		requestip.SetTrustedProxyHops(cfg.Server.TrustedProxyHops)
	}

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
		if preparedWebDAV != nil {
			runtimeCfg = *preparedWebDAV
		}
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
	if webDAVUsesGeneratedPassword(*cfg) {
		preparedRuntimeCfg, err := prepareWebDAVRuntimeConfig(*cfg)
		if err != nil {
			s.logger.Error().Err(err).Msg("failed to load generated WebDAV password for credentials response")
			ServiceUnavailable(w, "webdav credentials unavailable")
			return
		}
		runtimeCfg = *preparedRuntimeCfg
	}

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
	ShareEnabled   bool   `json:"share_enabled"`
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
		ShareEnabled:   cfg.Share.Enabled,
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

func (s *Server) favoritesRuntimeReady() bool {
	return s.favoritesStore != nil && s.favoritesHandler != nil
}

func (s *Server) favoritesConfiguredButUnavailable() bool {
	return s.favoritesConfigured && !s.favoritesRuntimeReady()
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

	owner, err := s.resolveShareOwner(shareInfo.CreatedBy)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, share.ErrShareNotFound
		}
		return nil, err
	}
	if owner.Disabled {
		return nil, share.ErrShareNotFound
	}
	if owner.Role == auth.RoleAdmin {
		return shareInfo, nil
	}
	if strings.TrimSpace(owner.HomeDir) == "" {
		return nil, share.ErrShareNotFound
	}

	homeDir, err := validatePath(owner.HomeDir)
	if err != nil {
		return nil, share.ErrShareNotFound
	}
	if !pathWithinBase(homeDir, shareInfo.Path) {
		return nil, share.ErrShareNotFound
	}

	return shareInfo, nil
}

func (s *Server) resolveShareOwner(ownerRef string) (*auth.User, error) {
	if s.userStore == nil || strings.TrimSpace(ownerRef) == "" {
		return nil, auth.ErrUserNotFound
	}

	owner, err := s.userStore.GetByID(ownerRef)
	if err == nil {
		return owner, nil
	}
	if !errors.Is(err, auth.ErrUserNotFound) {
		return nil, err
	}

	owner, err = s.userStore.GetByUsername(ownerRef)
	if err != nil {
		return nil, err
	}
	return owner, nil
}

func getShareOwnerIdentifiersFromRequest(ctx context.Context) []string {
	claims := auth.GetClaimsFromContext(ctx)
	if claims == nil {
		return nil
	}

	identifiers := make([]string, 0, 2)
	appendUnique := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range identifiers {
			if existing == trimmed {
				return
			}
		}
		identifiers = append(identifiers, trimmed)
	}

	appendUnique(claims.UserID)
	appendUnique(claims.Username)
	return identifiers
}

func getFavoriteUserIdentifiersFromRequest(ctx context.Context) (string, []string) {
	claims := auth.GetClaimsFromContext(ctx)
	if claims == nil {
		return "anonymous", nil
	}
	primary := strings.TrimSpace(claims.UserID)
	if primary == "" {
		primary = "anonymous"
	}
	legacy := strings.TrimSpace(claims.Username)
	if legacy == "" || legacy == primary {
		return primary, nil
	}
	return primary, []string{legacy}
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

	var sharesList []*share.Share
	if !s.authEnabled {
		sharesList = s.shareStore.ListAll()
	} else if auth.IsAdmin(r.Context()) && r.URL.Query().Get("all") == "true" {
		sharesList = s.shareStore.ListAll()
	} else {
		sharesList = s.shareStore.ListByUser(getShareOwnerIdentifiersFromRequest(r.Context())...)
	}

	if s.authEnabled {
		var err error
		sharesList, err = s.filterSharesByHomeDir(r.Context(), sharesList)
		if err != nil {
			s.respondHomeDirFilterError(w, "filter shares by home directory", err)
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
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}

	userID, legacyUserIDs := getFavoriteUserIdentifiersFromRequest(r.Context())
	favoritesList := s.favoritesStore.List(userID, legacyUserIDs...)
	if s.authEnabled {
		var err error
		favoritesList, err = s.filterFavoritesByHomeDir(r.Context(), favoritesList)
		if err != nil {
			s.respondHomeDirFilterError(w, "filter favorites by home directory", err)
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
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePath, err := readFavoriteBodyPath(r)
	if err != nil {
		if errors.Is(err, errInvalidPath) {
			badRequestInvalidPath(w)
			return
		}
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	if favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	rec := newBufferedResponseRecorder()
	s.favoritesHandler.AddFavorite(rec, r)
	if rec.statusCode == http.StatusCreated {
		s.LogActivityWithWarning(rec, r, activity.ActionFavorite, favoritePath, nil)
	}
	rec.FlushTo(w)
}

func (s *Server) handleCheckFavorite(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePath, err := readFavoriteQueryPath(r)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if favoritePath != "" {
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
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePaths, err := readFavoriteBatchPaths(r)
	if err != nil {
		if errors.Is(err, errInvalidPath) {
			badRequestInvalidPath(w)
			return
		}
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
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePath, err := readFavoriteRoutePath(r)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	rec := newBufferedResponseRecorder()
	s.favoritesHandler.RemoveFavorite(rec, r)
	if rec.statusCode >= http.StatusOK && rec.statusCode < http.StatusMultipleChoices {
		s.LogActivityWithWarning(rec, r, activity.ActionUnfavorite, favoritePath, nil)
	}
	rec.FlushTo(w)
}

func (s *Server) handleUpdateFavoriteNote(w http.ResponseWriter, r *http.Request) {
	if !s.isFavoritesFeatureEnabled() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature disabled", "FAVORITES_FEATURE_DISABLED")
		return
	}
	if !s.favoritesRuntimeReady() {
		writeFavoritesErrorResponse(w, http.StatusServiceUnavailable, "favorites feature unavailable", "FAVORITES_UNAVAILABLE")
		return
	}
	favoritePath, err := readFavoriteRoutePath(r)
	if err != nil {
		badRequestInvalidPath(w)
		return
	}
	if favoritePath != "" {
		if err := s.authorizeUserPath(r.Context(), favoritePath); err != nil {
			forbiddenPathOutsideHome(w)
			return
		}
	}
	rec := newBufferedResponseRecorder()
	s.favoritesHandler.UpdateNote(rec, r)
	if rec.statusCode >= http.StatusOK && rec.statusCode < http.StatusMultipleChoices {
		s.LogActivityWithWarning(rec, r, activity.ActionFavoriteNote, favoritePath, nil)
	}
	rec.FlushTo(w)
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
		if errors.Is(err, errInvalidPath) {
			badRequestInvalidPath(w)
			return
		}
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
		shareID := chi.URLParam(r, "id")
		if auth.IsAdmin(r.Context()) {
			if shareInfo, err := s.shareStore.Get(shareID); err == nil {
				sharePath = shareInfo.Path
			} else if errors.Is(err, share.ErrShareNotFound) {
				writeShareErrorResponse(w, http.StatusNotFound, "share not found", "SHARE_NOT_FOUND")
				return
			} else {
				s.respondInternalError(w, "load share for delete", err)
				return
			}
		} else if shareInfo, err := s.ensureShareWithinOwnerHome(shareID); err == nil {
			sharePath = shareInfo.Path
		} else {
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

	var req share.CreateShareRequest
	if err := decodeJSONBody(r, &req); err != nil {
		return "", err
	}

	trimmedPath := strings.TrimSpace(req.Path)
	if trimmedPath == "" {
		return "", nil
	}

	cleanPath, err := validatePath(trimmedPath)
	if err != nil {
		return "", err
	}
	if cleanPath != req.Path {
		req.Path = cleanPath
		if err := resetJSONRequestBody(r, &req); err != nil {
			return "", err
		}
	}

	return cleanPath, nil
}

func readFavoriteBodyPath(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}

	var req struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		return "", err
	}

	trimmedPath := strings.TrimSpace(req.Path)
	if trimmedPath == "" {
		return "", nil
	}

	cleanPath, err := validatePath(req.Path)
	if err != nil {
		return "", err
	}
	if cleanPath == "/" {
		return "", nil
	}

	return cleanPath, nil
}

func readFavoriteBatchPaths(r *http.Request) ([]string, error) {
	if r.Body == nil {
		return nil, nil
	}

	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		return nil, err
	}

	cleanPaths := make([]string, 0, len(req.Paths))
	for _, favoritePath := range req.Paths {
		if strings.TrimSpace(favoritePath) == "" {
			continue
		}
		cleanPath, err := validatePath(favoritePath)
		if err != nil {
			return nil, err
		}
		cleanPaths = append(cleanPaths, cleanPath)
	}

	return cleanPaths, nil
}

func readFavoriteQueryPath(r *http.Request) (string, error) {
	queryPath := r.URL.Query().Get("path")
	if strings.TrimSpace(queryPath) == "" {
		return "", nil
	}
	return validatePath(queryPath)
}

func readFavoriteRoutePath(r *http.Request) (string, error) {
	routePath := strings.TrimPrefix(r.URL.Path, "/api/v1/favorites")
	trimmedPath := strings.TrimSpace(routePath)
	if trimmedPath == "" || trimmedPath == "/" {
		return "", nil
	}
	return validatePath(routePath)
}
