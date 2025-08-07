// Package backup provides local backup jobs, scheduling, retention, and restore drills.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/rootio"
)

const (
	JobTypeLocal  = "local"
	JobTypeRestic = "restic"
	JobTypeRclone = "rclone"

	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	RestorePreflightPassed  = "passed"
	RestorePreflightWarning = "warning"
	RestorePreflightFailed  = "failed"

	NotificationLevelWarning  = "warning"
	NotificationLevelCritical = "critical"

	NotificationTypeBackupRun    = "backup_run"
	NotificationTypeRestoreDrill = "backup_restore_drill"
	NotificationTypeRetention    = "backup_retention_check"
	NotificationTriggerReminder  = "restore_drill_reminder"

	FailureCategoryNoSnapshot         = "no_snapshot"
	FailureCategoryUnsupportedJobType = "unsupported_job_type"
	FailureCategoryUnsafePath         = "unsafe_path"
	FailureCategoryIntegrityCheck     = "integrity_check"
	FailureCategoryExternalCommand    = "external_command"
	FailureCategoryCancelled          = "cancelled"
	FailureCategoryIO                 = "io"
	FailureCategoryUnknown            = "unknown"

	stateFileName                 = "status.json"
	manifestFileName              = "manifest.json"
	manifestVersion               = 1
	restoreHistoryLimit           = 20
	restoreDrillHistoryLimit      = 20
	restorePreviewLimit           = 10
	restoreVerifyWarningLimit     = 8
	defaultRestoreDrillStaleAfter = 30 * 24 * time.Hour
	restoreDrillReminderCooldown  = 24 * time.Hour

	defaultSchedulerPollInterval = time.Minute
	externalCommandStderrLimit   = 4096
	externalCommandStdoutLimit   = 4 * 1024 * 1024
)

var restoreAvailableBytesFunc = restoreAvailableBytes

var (
	ErrJobNotFound           = errors.New("backup job not found")
	ErrJobAlreadyRunning     = errors.New("backup job already running")
	ErrJobDisabled           = errors.New("backup job disabled")
	ErrNoSnapshots           = errors.New("backup job has no completed snapshots")
	ErrUnsupportedJobType    = errors.New("unsupported backup job type")
	ErrUnsafePath            = errors.New("unsafe backup path")
	ErrRestoreTargetExists   = errors.New("restore target already exists")
	ErrSourceContainsSymlink = errors.New("backup source contains a symlink")
	ErrUnsupportedFileType   = errors.New("backup source contains an unsupported file type")
)

var execCommandContext = exec.CommandContext

// ManagerConfig configures a backup manager instance.
type ManagerConfig struct {
	Root                  string
	StorageRoot           string
	ConfigPath            string
	Jobs                  []config.BackupJobConfig
	Notifier              Notifier
	SchedulerPollInterval time.Duration
}

// Notifier receives backup events that should be surfaced outside the manager.
type Notifier interface {
	NotifyBackupEvent(ctx context.Context, event NotificationEvent) error
}

// NotifierFunc adapts a function to Notifier.
type NotifierFunc func(ctx context.Context, event NotificationEvent) error

// NotifyBackupEvent calls f(ctx, event).
func (f NotifierFunc) NotifyBackupEvent(ctx context.Context, event NotificationEvent) error {
	return f(ctx, event)
}

// Manager runs configured backup jobs and records their latest status.
type Manager struct {
	root        string
	storageRoot string
	configPath  string
	jobs        map[string]config.BackupJobConfig
	notifier    Notifier

	mu      sync.Mutex
	running map[string]struct{}
	state   persistedState
	now     func() time.Time

	schedulerStarted bool
	schedulerPoll    time.Duration
}

type persistedState struct {
	Jobs map[string]JobState `json:"jobs"`
}

// JobState contains the latest persisted status for a configured job.
type JobState struct {
	LastRun                    *RunResult            `json:"last_run,omitempty"`
	LastSuccessfulRun          *RunResult            `json:"last_successful_run,omitempty"`
	LastRestoreDrill           *RestoreDrillResult   `json:"last_restore_drill,omitempty"`
	RestoreDrillHistory        []*RestoreDrillResult `json:"restore_drill_history,omitempty"`
	LastRestore                *RestoreResult        `json:"last_restore,omitempty"`
	LastRestoreVerify          *RestoreVerifyResult  `json:"last_restore_verify,omitempty"`
	RestoreHistory             []*RestoreResult      `json:"restore_history,omitempty"`
	LastRetentionCheck         *RetentionCheckResult `json:"last_retention_check,omitempty"`
	LastScheduledRunAt         *time.Time            `json:"last_scheduled_run_at,omitempty"`
	LastRestoreDrillReminderAt *time.Time            `json:"last_restore_drill_reminder_at,omitempty"`
}

// JobView is returned by the API for configured backup jobs.
type JobView struct {
	ID                         string                `json:"id"`
	Name                       string                `json:"name"`
	Type                       string                `json:"type"`
	Source                     string                `json:"source"`
	Destination                string                `json:"destination"`
	Repository                 string                `json:"repository,omitempty"`
	Remote                     string                `json:"remote,omitempty"`
	Command                    string                `json:"command,omitempty"`
	Disabled                   bool                  `json:"disabled"`
	ScheduleInterval           string                `json:"schedule_interval,omitempty"`
	ScheduleWindowStart        string                `json:"schedule_window_start,omitempty"`
	ScheduleWindowEnd          string                `json:"schedule_window_end,omitempty"`
	NextRunAt                  *time.Time            `json:"next_run_at,omitempty"`
	StaleAfter                 string                `json:"stale_after,omitempty"`
	RestoreDrillStaleAfter     string                `json:"restore_drill_stale_after,omitempty"`
	MaxSnapshots               int                   `json:"max_snapshots,omitempty"`
	MaxAge                     string                `json:"max_age,omitempty"`
	RetentionPolicy            string                `json:"retention_policy,omitempty"`
	RetentionStatus            string                `json:"retention_status"`
	RetentionMessage           string                `json:"retention_message,omitempty"`
	HealthStatus               string                `json:"health_status"`
	HealthMessage              string                `json:"health_message,omitempty"`
	RestoreDrillStatus         string                `json:"restore_drill_status"`
	RestoreDrillMessage        string                `json:"restore_drill_message,omitempty"`
	LastRestoreDrillReminderAt *time.Time            `json:"last_restore_drill_reminder_at,omitempty"`
	RestoreDrillStats          *RestoreDrillStats    `json:"restore_drill_stats,omitempty"`
	IncludeConfig              bool                  `json:"include_config"`
	VerifyAfterBackup          bool                  `json:"verify_after_backup"`
	Exclude                    []string              `json:"exclude"`
	Running                    bool                  `json:"running"`
	LastRun                    *RunResult            `json:"last_run,omitempty"`
	LastSuccessfulRun          *RunResult            `json:"last_successful_run,omitempty"`
	LastRestoreDrill           *RestoreDrillResult   `json:"last_restore_drill,omitempty"`
	RestoreDrillHistory        []*RestoreDrillResult `json:"restore_drill_history,omitempty"`
	LastRestore                *RestoreResult        `json:"last_restore,omitempty"`
	LastRestoreVerify          *RestoreVerifyResult  `json:"last_restore_verify,omitempty"`
	RestoreHistory             []*RestoreResult      `json:"restore_history,omitempty"`
	LastRetentionCheck         *RetentionCheckResult `json:"last_retention_check,omitempty"`
}

// RestoreReport is an exportable audit summary for one backup job.
type RestoreReport struct {
	GeneratedAt         time.Time             `json:"generated_at"`
	Job                 JobView               `json:"job"`
	LastRun             *RunResult            `json:"last_run,omitempty"`
	LastSuccessfulRun   *RunResult            `json:"last_successful_run,omitempty"`
	LastRetentionCheck  *RetentionCheckResult `json:"last_retention_check,omitempty"`
	LastRestoreDrill    *RestoreDrillResult   `json:"last_restore_drill,omitempty"`
	RestoreDrillHistory []*RestoreDrillResult `json:"restore_drill_history,omitempty"`
	RestoreDrillStats   *RestoreDrillStats    `json:"restore_drill_stats,omitempty"`
	LastRestore         *RestoreResult        `json:"last_restore,omitempty"`
	LastRestoreVerify   *RestoreVerifyResult  `json:"last_restore_verify,omitempty"`
	RestoreHistory      []*RestoreResult      `json:"restore_history,omitempty"`
	Findings            []string              `json:"findings,omitempty"`
}

// RestoreDrillStats summarizes recent restore drill reliability.
type RestoreDrillStats struct {
	TotalRuns            int        `json:"total_runs"`
	SuccessfulRuns       int        `json:"successful_runs"`
	FailedRuns           int        `json:"failed_runs"`
	SuccessRate          float64    `json:"success_rate"`
	ConsecutiveSuccesses int        `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int        `json:"consecutive_failures,omitempty"`
	LatestSuccessAt      *time.Time `json:"latest_success_at,omitempty"`
	LatestFailureAt      *time.Time `json:"latest_failure_at,omitempty"`
	LastFailureMessage   string     `json:"last_failure_message,omitempty"`
	LastFailureCategory  string     `json:"last_failure_category,omitempty"`
}

// ScheduledRunResult describes one due job handled by the scheduler loop.
type ScheduledRunResult struct {
	JobID  string     `json:"job_id"`
	DueAt  time.Time  `json:"due_at"`
	Result *RunResult `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// NotificationEvent describes a backup warning or failure notification.
type NotificationEvent struct {
	Type                string     `json:"type"`
	Level               string     `json:"level"`
	Message             string     `json:"message"`
	JobID               string     `json:"job_id"`
	JobName             string     `json:"job_name"`
	JobType             string     `json:"job_type"`
	RunID               string     `json:"run_id"`
	Trigger             string     `json:"trigger,omitempty"`
	Status              string     `json:"status"`
	StartedAt           time.Time  `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	Source              string     `json:"source,omitempty"`
	Destination         string     `json:"destination,omitempty"`
	SnapshotPath        string     `json:"snapshot_path,omitempty"`
	ManifestPath        string     `json:"manifest_path,omitempty"`
	FileCount           int64      `json:"file_count,omitempty"`
	TotalBytes          int64      `json:"total_bytes,omitempty"`
	VerifiedBytes       int64      `json:"verified_bytes,omitempty"`
	SnapshotCount       int        `json:"snapshot_count,omitempty"`
	LastSuccessfulRunAt *time.Time `json:"last_successful_run_at,omitempty"`
	LastRestoreDrillAt  *time.Time `json:"last_restore_drill_at,omitempty"`
	StaleAfter          string     `json:"stale_after,omitempty"`
	ReminderCooldown    string     `json:"reminder_cooldown,omitempty"`
	PrunedSnapshots     int        `json:"pruned_snapshots,omitempty"`
	Warnings            []string   `json:"warnings,omitempty"`
	ErrorMessage        string     `json:"error_message,omitempty"`
	FailureCategory     string     `json:"failure_category,omitempty"`
	Timestamp           time.Time  `json:"timestamp"`
}

// RunResult records one backup execution.
type RunResult struct {
	ID              string     `json:"id"`
	JobID           string     `json:"job_id"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMs      int64      `json:"duration_ms"`
	Source          string     `json:"source"`
	Destination     string     `json:"destination"`
	SnapshotPath    string     `json:"snapshot_path,omitempty"`
	ManifestPath    string     `json:"manifest_path,omitempty"`
	FileCount       int64      `json:"file_count"`
	TotalBytes      int64      `json:"total_bytes"`
	ConfigIncluded  bool       `json:"config_included"`
	Trigger         string     `json:"trigger,omitempty"`
	Warning         bool       `json:"warning,omitempty"`
	Warnings        []string   `json:"warnings,omitempty"`
	PrunedSnapshots int        `json:"pruned_snapshots,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
}

// RestoreDrillOptions controls restore drill behavior.
type RestoreDrillOptions struct {
	KeepArtifact bool `json:"keep_artifact"`
}

// RestoreDrillResult records one non-destructive restore verification.
type RestoreDrillResult struct {
	ID              string     `json:"id"`
	JobID           string     `json:"job_id"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMs      int64      `json:"duration_ms"`
	SnapshotPath    string     `json:"snapshot_path,omitempty"`
	ManifestPath    string     `json:"manifest_path,omitempty"`
	RestoredPath    string     `json:"restored_path,omitempty"`
	ArtifactKept    bool       `json:"artifact_kept"`
	FileCount       int64      `json:"file_count"`
	VerifiedBytes   int64      `json:"verified_bytes"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	FailureCategory string     `json:"failure_category,omitempty"`
}

// RestoreOptions controls an explicit snapshot restore.
type RestoreOptions struct {
	TargetPath    string `json:"target_path"`
	IncludeConfig bool   `json:"include_config"`
}

// RestorePreviewOptions controls a non-destructive explicit restore preview.
type RestorePreviewOptions struct {
	TargetPath    string `json:"target_path"`
	IncludeConfig bool   `json:"include_config"`
}

// RestoreVerifyOptions controls a read-only check of a restored target.
type RestoreVerifyOptions struct {
	TargetPath string `json:"target_path"`
}

// RetentionCheckResult records one retention-policy inspection for a backup job.
type RetentionCheckResult struct {
	ID               string     `json:"id"`
	JobID            string     `json:"job_id"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMs       int64      `json:"duration_ms"`
	Target           string     `json:"target"`
	Policy           string     `json:"policy,omitempty"`
	SnapshotCount    int        `json:"snapshot_count,omitempty"`
	FileCount        int64      `json:"file_count,omitempty"`
	TotalBytes       int64      `json:"total_bytes,omitempty"`
	OldestSnapshotAt *time.Time `json:"oldest_snapshot_at,omitempty"`
	LatestSnapshotAt *time.Time `json:"latest_snapshot_at,omitempty"`
	Warning          bool       `json:"warning,omitempty"`
	Warnings         []string   `json:"warnings,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}

// RestorePreflightCheck describes one safety gate before an explicit restore.
type RestorePreflightCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// RestorePreviewResult records a non-destructive estimate for an explicit restore.
type RestorePreviewResult struct {
	ID                string                  `json:"id"`
	JobID             string                  `json:"job_id"`
	Status            string                  `json:"status"`
	StartedAt         time.Time               `json:"started_at"`
	FinishedAt        *time.Time              `json:"finished_at,omitempty"`
	DurationMs        int64                   `json:"duration_ms"`
	Source            string                  `json:"source"`
	Destination       string                  `json:"destination"`
	SnapshotPath      string                  `json:"snapshot_path,omitempty"`
	ManifestPath      string                  `json:"manifest_path,omitempty"`
	TargetPath        string                  `json:"target_path"`
	FileCount         int64                   `json:"file_count"`
	TotalBytes        int64                   `json:"total_bytes"`
	ConfigAvailable   bool                    `json:"config_available"`
	ConfigIncluded    bool                    `json:"config_included"`
	SamplePaths       []string                `json:"sample_paths,omitempty"`
	PreflightChecks   []RestorePreflightCheck `json:"preflight_checks,omitempty"`
	Warnings          []string                `json:"warnings,omitempty"`
	CutoverChecklist  []string                `json:"cutover_checklist,omitempty"`
	RollbackChecklist []string                `json:"rollback_checklist,omitempty"`
	ErrorMessage      string                  `json:"error_message,omitempty"`
}

// RestoreVerifyResult records a read-only verification of an explicit restore target.
type RestoreVerifyResult struct {
	ID                   string     `json:"id"`
	JobID                string     `json:"job_id"`
	Status               string     `json:"status"`
	StartedAt            time.Time  `json:"started_at"`
	FinishedAt           *time.Time `json:"finished_at,omitempty"`
	DurationMs           int64      `json:"duration_ms"`
	Source               string     `json:"source"`
	Destination          string     `json:"destination"`
	TargetPath           string     `json:"target_path"`
	FileCount            int64      `json:"file_count"`
	VerifiedBytes        int64      `json:"verified_bytes"`
	ConfigPath           string     `json:"config_path,omitempty"`
	ConfigFound          bool       `json:"config_found"`
	FilesDirFound        bool       `json:"files_dir_found"`
	InternalDirFound     bool       `json:"internal_dir_found"`
	IndexFound           bool       `json:"index_found"`
	ObjectsDirFound      bool       `json:"objects_dir_found"`
	LooksLikeStorageRoot bool       `json:"looks_like_storage_root"`
	Warnings             []string   `json:"warnings,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
}

// RestoreResult records one explicit snapshot restore.
type RestoreResult struct {
	ID                string                  `json:"id"`
	JobID             string                  `json:"job_id"`
	Status            string                  `json:"status"`
	StartedAt         time.Time               `json:"started_at"`
	FinishedAt        *time.Time              `json:"finished_at,omitempty"`
	DurationMs        int64                   `json:"duration_ms"`
	SnapshotPath      string                  `json:"snapshot_path,omitempty"`
	ManifestPath      string                  `json:"manifest_path,omitempty"`
	TargetPath        string                  `json:"target_path"`
	ConfigRestored    bool                    `json:"config_restored"`
	ConfigPath        string                  `json:"config_path,omitempty"`
	FileCount         int64                   `json:"file_count"`
	VerifiedBytes     int64                   `json:"verified_bytes"`
	PreflightChecks   []RestorePreflightCheck `json:"preflight_checks,omitempty"`
	Warnings          []string                `json:"warnings,omitempty"`
	CutoverChecklist  []string                `json:"cutover_checklist,omitempty"`
	RollbackChecklist []string                `json:"rollback_checklist,omitempty"`
	ErrorMessage      string                  `json:"error_message,omitempty"`
}

// Manifest describes a completed local snapshot.
type Manifest struct {
	Version     int             `json:"version"`
	JobID       string          `json:"job_id"`
	RunID       string          `json:"run_id"`
	Source      string          `json:"source"`
	CreatedAt   time.Time       `json:"created_at"`
	FileCount   int64           `json:"file_count"`
	TotalBytes  int64           `json:"total_bytes"`
	Entries     []ManifestEntry `json:"entries"`
	ConfigPath  string          `json:"config_path,omitempty"`
	Description string          `json:"description,omitempty"`
}

// ManifestEntry is one file stored in a local snapshot.
type ManifestEntry struct {
	ArchivePath string `json:"archive_path"`
	SourcePath  string `json:"source_path"`
	Size        int64  `json:"size"`
	Mode        uint32 `json:"mode"`
	SHA256      string `json:"sha256"`
}

// NewManager initializes a backup manager and loads persisted state.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		return nil, errors.New("backup root cannot be empty")
	}
	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve backup root: %w", err)
		}
		root = absRoot
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create backup state directory: %w", err)
	}

	m := &Manager{
		root:        root,
		storageRoot: cfg.StorageRoot,
		configPath:  cfg.ConfigPath,
		jobs:        make(map[string]config.BackupJobConfig, len(cfg.Jobs)),
		notifier:    cfg.Notifier,
		running:     map[string]struct{}{},
		state: persistedState{
			Jobs: map[string]JobState{},
		},
		now:           time.Now,
		schedulerPoll: cfg.SchedulerPollInterval,
	}
	if m.schedulerPoll <= 0 {
		m.schedulerPoll = defaultSchedulerPollInterval
	}
	for _, job := range cfg.Jobs {
		normalized := normalizeJob(job, cfg.StorageRoot)
		if normalized.ID == "" {
			continue
		}
		m.jobs[normalized.ID] = normalized
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

// ListJobs returns all configured backup jobs with latest status.
func (m *Manager) ListJobs() []JobView {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	views := make([]JobView, 0, len(ids))
	for _, id := range ids {
		views = append(views, m.jobViewLocked(id, m.jobs[id]))
	}
	return views
}

// GetJob returns one configured backup job with latest status.
func (m *Manager) GetJob(id string) (*JobView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	view := m.jobViewLocked(id, job)
	return &view, nil
}

// RunJob runs a configured backup job synchronously.
func (m *Manager) RunJob(ctx context.Context, id string) (*RunResult, error) {
	return m.runJobWithTrigger(ctx, id, "manual")
}

func (m *Manager) runJobWithTrigger(ctx context.Context, id string, trigger string) (*RunResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	result := &RunResult{
		ID:             formatRunID(startedAt),
		JobID:          job.ID,
		Status:         StatusRunning,
		StartedAt:      startedAt,
		Source:         effectiveSource(job, m.storageRoot),
		Destination:    backupTarget(job),
		ConfigIncluded: job.IncludeConfig && strings.TrimSpace(m.configPath) != "",
		Trigger:        trigger,
	}
	_ = m.updateLastRun(result)

	err = m.runBackup(ctx, job, result)
	if err == nil && job.Type == JobTypeLocal {
		pruned, warnings := m.applyRetention(ctx, job, result.SnapshotPath)
		result.PrunedSnapshots = pruned
		if len(warnings) > 0 {
			result.Warning = true
			result.Warnings = append(result.Warnings, warnings...)
		}
	}
	if err == nil {
		retentionCheck, checkErr := m.runRetentionCheckForJob(ctx, job, false)
		if retentionCheck != nil && (retentionCheck.Warning || retentionCheck.Status == StatusFailed) {
			result.Warning = true
			if retentionCheck.ErrorMessage != "" {
				result.Warnings = append(result.Warnings, "保留策略检测失败: "+retentionCheck.ErrorMessage)
			}
			result.Warnings = append(result.Warnings, retentionCheck.Warnings...)
		} else if checkErr != nil {
			result.Warning = true
			result.Warnings = append(result.Warnings, "保留策略检测失败: "+checkErr.Error())
		}
	}
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
	} else {
		result.Status = StatusCompleted
	}
	if saveErr := m.updateLastRun(result); saveErr != nil {
		if err != nil {
			return cloneRunResult(result), errors.Join(err, saveErr)
		}
		return cloneRunResult(result), saveErr
	}
	m.notifyRun(ctx, job, result)
	return cloneRunResult(result), err
}

// RunRestoreDrill restores the latest completed snapshot to a temporary
// directory and verifies every file against the snapshot manifest.
func (m *Manager) RunRestoreDrill(ctx context.Context, id string, opts RestoreDrillOptions) (*RestoreDrillResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	result := &RestoreDrillResult{
		ID:           formatRunID(startedAt),
		JobID:        job.ID,
		Status:       StatusRunning,
		StartedAt:    startedAt,
		ArtifactKept: opts.KeepArtifact,
	}
	_ = m.updateLastRestoreDrill(result, false)

	err = m.runRestoreDrill(ctx, job, opts, result)
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
		result.FailureCategory = classifyRestoreDrillFailure(err)
	} else {
		result.Status = StatusCompleted
	}
	if saveErr := m.updateLastRestoreDrill(result, true); saveErr != nil {
		if err != nil {
			return cloneRestoreDrillResult(result), errors.Join(err, saveErr)
		}
		return cloneRestoreDrillResult(result), saveErr
	}
	m.notifyRestoreDrill(ctx, job, result)
	return cloneRestoreDrillResult(result), err
}

// RunRestorePreview validates an explicit restore request and returns a
// non-destructive snapshot summary. It does not persist restore history.
func (m *Manager) RunRestorePreview(ctx context.Context, id string, opts RestorePreviewOptions) (*RestorePreviewResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	result := &RestorePreviewResult{
		ID:          formatRunID(startedAt),
		JobID:       job.ID,
		Status:      StatusRunning,
		StartedAt:   startedAt,
		Source:      effectiveSource(job, m.storageRoot),
		Destination: backupTarget(job),
		TargetPath:  strings.TrimSpace(opts.TargetPath),
	}

	err = m.runRestorePreview(ctx, job, opts, result)
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
	} else {
		result.Status = StatusCompleted
	}
	return cloneRestorePreviewResult(result), err
}

// RunRestoreVerify performs a read-only verification of a restored target
// directory. It does not persist restore history or modify target data.
func (m *Manager) RunRestoreVerify(ctx context.Context, id string, opts RestoreVerifyOptions) (*RestoreVerifyResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	result := &RestoreVerifyResult{
		ID:          formatRunID(startedAt),
		JobID:       job.ID,
		Status:      StatusRunning,
		StartedAt:   startedAt,
		Source:      effectiveSource(job, m.storageRoot),
		Destination: backupTarget(job),
		TargetPath:  strings.TrimSpace(opts.TargetPath),
	}

	err = m.runRestoreVerify(ctx, job, opts, result)
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
	} else {
		result.Status = StatusCompleted
	}
	if saveErr := m.updateLastRestoreVerify(result); saveErr != nil {
		if err != nil {
			return cloneRestoreVerifyResult(result), errors.Join(err, saveErr)
		}
		return cloneRestoreVerifyResult(result), saveErr
	}
	return cloneRestoreVerifyResult(result), err
}

// RunRetentionCheck inspects the configured retention boundary for a backup job.
func (m *Manager) RunRetentionCheck(ctx context.Context, id string) (*RetentionCheckResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	return m.runRetentionCheckForJob(ctx, job, true)
}

// RunRestore restores a completed backup into a caller-chosen target directory.
// The target is created atomically from a partial sibling and must be outside
// the live source, storage root, and any local backup destination.
func (m *Manager) RunRestore(ctx context.Context, id string, opts RestoreOptions) (*RestoreResult, error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	targetPath := strings.TrimSpace(opts.TargetPath)
	result := &RestoreResult{
		ID:         formatRunID(startedAt),
		JobID:      job.ID,
		Status:     StatusRunning,
		StartedAt:  startedAt,
		TargetPath: targetPath,
	}
	_ = m.updateLastRestore(result, false)

	preflightErr := m.runRestorePreflight(ctx, job, opts, result)
	if preflightErr == nil {
		preflightErr = firstFailedRestorePreflight(result.PreflightChecks)
	}
	if preflightErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
		result.Status = StatusFailed
		result.ErrorMessage = preflightErr.Error()
		if saveErr := m.updateLastRestore(result, true); saveErr != nil {
			return cloneRestoreResult(result), errors.Join(preflightErr, saveErr)
		}
		return cloneRestoreResult(result), preflightErr
	}

	err = m.runRestore(ctx, job, opts, result)
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
	} else {
		result.Status = StatusCompleted
	}
	if saveErr := m.updateLastRestore(result, true); saveErr != nil {
		if err != nil {
			return cloneRestoreResult(result), errors.Join(err, saveErr)
		}
		return cloneRestoreResult(result), saveErr
	}
	return cloneRestoreResult(result), err
}

func (m *Manager) notifyRun(ctx context.Context, job config.BackupJobConfig, result *RunResult) {
	if m.notifier == nil || result == nil {
		return
	}
	if result.Status != StatusFailed && !result.Warning {
		return
	}

	level := NotificationLevelWarning
	message := fmt.Sprintf("备份任务 %s 完成但存在警告", job.Name)
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = fmt.Sprintf("备份任务 %s 执行失败", job.Name)
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:            NotificationTypeBackupRun,
		Level:           level,
		Message:         message,
		JobID:           job.ID,
		JobName:         job.Name,
		JobType:         job.Type,
		RunID:           result.ID,
		Trigger:         result.Trigger,
		Status:          result.Status,
		StartedAt:       result.StartedAt,
		FinishedAt:      result.FinishedAt,
		Source:          result.Source,
		Destination:     result.Destination,
		SnapshotPath:    result.SnapshotPath,
		ManifestPath:    result.ManifestPath,
		FileCount:       result.FileCount,
		TotalBytes:      result.TotalBytes,
		PrunedSnapshots: result.PrunedSnapshots,
		Warnings:        append([]string(nil), result.Warnings...),
		ErrorMessage:    result.ErrorMessage,
		Timestamp:       time.Now().UTC(),
	})
}

func (m *Manager) notifyRestoreDrill(ctx context.Context, job config.BackupJobConfig, result *RestoreDrillResult) {
	if m.notifier == nil || result == nil || result.Status != StatusFailed {
		return
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:            NotificationTypeRestoreDrill,
		Level:           NotificationLevelCritical,
		Message:         fmt.Sprintf("备份任务 %s 恢复演练失败", job.Name),
		JobID:           job.ID,
		JobName:         job.Name,
		JobType:         job.Type,
		RunID:           result.ID,
		Status:          result.Status,
		StartedAt:       result.StartedAt,
		FinishedAt:      result.FinishedAt,
		SnapshotPath:    result.SnapshotPath,
		ManifestPath:    result.ManifestPath,
		FileCount:       result.FileCount,
		VerifiedBytes:   result.VerifiedBytes,
		ErrorMessage:    result.ErrorMessage,
		FailureCategory: result.FailureCategory,
		Timestamp:       time.Now().UTC(),
	})
}

func (m *Manager) notifyRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) {
	if m.notifier == nil || result == nil {
		return
	}
	if result.Status != StatusFailed && !result.Warning {
		return
	}

	level := NotificationLevelWarning
	message := fmt.Sprintf("备份任务 %s 保留策略需要关注", job.Name)
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = fmt.Sprintf("备份任务 %s 保留策略检测失败", job.Name)
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:          NotificationTypeRetention,
		Level:         level,
		Message:       message,
		JobID:         job.ID,
		JobName:       job.Name,
		JobType:       job.Type,
		RunID:         result.ID,
		Status:        result.Status,
		StartedAt:     result.StartedAt,
		FinishedAt:    result.FinishedAt,
		Destination:   result.Target,
		SnapshotCount: result.SnapshotCount,
		FileCount:     result.FileCount,
		TotalBytes:    result.TotalBytes,
		Warnings:      append([]string(nil), result.Warnings...),
		ErrorMessage:  result.ErrorMessage,
		Timestamp:     time.Now().UTC(),
	})
}

// SendRestoreDrillReminders emits warning notifications for jobs whose restore
// drills are missing or stale. Reminder timestamps are persisted to avoid
// repeating the same warning on every scheduler tick.
func (m *Manager) SendRestoreDrillReminders(ctx context.Context) []NotificationEvent {
	if m.notifier == nil {
		return nil
	}

	events := m.restoreDrillReminderEvents(m.now().UTC())
	for _, event := range events {
		_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), event)
	}
	return events
}

func (m *Manager) restoreDrillReminderEvents(now time.Time) []NotificationEvent {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	events := make([]NotificationEvent, 0)
	for _, id := range ids {
		job := m.jobs[id]
		state := m.state.Jobs[id]
		event, ok := m.restoreDrillReminderEventLocked(job, state, now)
		if !ok {
			continue
		}
		remindedAt := now.UTC()
		state.LastRestoreDrillReminderAt = &remindedAt
		m.state.Jobs[id] = state
		events = append(events, event)
	}
	if len(events) > 0 {
		_ = m.saveStateLocked()
	}
	return events
}

func (m *Manager) restoreDrillReminderEventLocked(job config.BackupJobConfig, state JobState, now time.Time) (NotificationEvent, bool) {
	if job.Disabled || state.LastSuccessfulRun == nil {
		return NotificationEvent{}, false
	}
	if state.LastRestoreDrillReminderAt != nil && now.Sub(*state.LastRestoreDrillReminderAt) < restoreDrillReminderCooldown {
		return NotificationEvent{}, false
	}

	staleAfter := effectiveRestoreDrillStaleAfter(job)
	if staleAfter <= 0 {
		return NotificationEvent{}, false
	}

	lastSuccessfulRunAt := runFinishedAt(state.LastSuccessfulRun)
	lastSuccessfulRunAtPtr := lastSuccessfulRunAt
	base := NotificationEvent{
		Type:                NotificationTypeRestoreDrill,
		Level:               NotificationLevelWarning,
		JobID:               job.ID,
		JobName:             job.Name,
		JobType:             job.Type,
		Trigger:             NotificationTriggerReminder,
		Source:              effectiveSource(job, m.storageRoot),
		Destination:         backupTarget(job),
		LastSuccessfulRunAt: &lastSuccessfulRunAtPtr,
		StaleAfter:          formatDurationForAPI(staleAfter),
		ReminderCooldown:    formatDurationForAPI(restoreDrillReminderCooldown),
		Timestamp:           now.UTC(),
	}

	if state.LastRestoreDrill == nil {
		if now.Sub(lastSuccessfulRunAt) <= staleAfter {
			return NotificationEvent{}, false
		}
		base.Message = fmt.Sprintf("备份任务 %s 尚未完成恢复演练", job.Name)
		base.Status = "due"
		base.StartedAt = lastSuccessfulRunAt
		return base, true
	}

	if state.LastRestoreDrill.Status == StatusFailed || state.LastRestoreDrill.Status == StatusRunning {
		return NotificationEvent{}, false
	}

	lastRestoreDrillAt := restoreDrillFinishedAt(state.LastRestoreDrill)
	if now.Sub(lastRestoreDrillAt) <= staleAfter {
		return NotificationEvent{}, false
	}
	lastRestoreDrillAtPtr := lastRestoreDrillAt
	base.Message = fmt.Sprintf("备份任务 %s 恢复演练已过期", job.Name)
	base.RunID = state.LastRestoreDrill.ID
	base.Status = "stale"
	base.StartedAt = state.LastRestoreDrill.StartedAt
	base.FinishedAt = cloneTime(state.LastRestoreDrill.FinishedAt)
	base.SnapshotPath = state.LastRestoreDrill.SnapshotPath
	base.ManifestPath = state.LastRestoreDrill.ManifestPath
	base.FileCount = state.LastRestoreDrill.FileCount
	base.VerifiedBytes = state.LastRestoreDrill.VerifiedBytes
	base.LastRestoreDrillAt = &lastRestoreDrillAtPtr
	return base, true
}

func (m *Manager) beginJob(id string) (config.BackupJobConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return config.BackupJobConfig{}, ErrJobNotFound
	}
	if job.Disabled {
		return config.BackupJobConfig{}, ErrJobDisabled
	}
	if _, ok := m.running[id]; ok {
		return config.BackupJobConfig{}, ErrJobAlreadyRunning
	}
	m.running[id] = struct{}{}
	return cloneJob(job), nil
}

func (m *Manager) endJob(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, id)
}

func (m *Manager) jobViewLocked(id string, job config.BackupJobConfig) JobView {
	state := m.state.Jobs[id]
	_, running := m.running[id]
	nextRunAt := m.nextRunAtLocked(job, state)
	healthStatus, healthMessage := m.healthLocked(job, state, running, m.now().UTC())
	restoreDrillStatus, restoreDrillMessage := m.restoreDrillHealthLocked(job, state, m.now().UTC())
	retentionStatus, retentionMessage := retentionHealth(job, state)
	restoreDrillHistory := restoreDrillHistoryForView(state)
	restoreDrillStats := buildRestoreDrillStats(restoreDrillHistory)
	return JobView{
		ID:                         job.ID,
		Name:                       job.Name,
		Type:                       job.Type,
		Source:                     effectiveSource(job, m.storageRoot),
		Destination:                backupTarget(job),
		Repository:                 job.Repository,
		Remote:                     job.Remote,
		Command:                    backupViewCommand(job),
		Disabled:                   job.Disabled,
		ScheduleInterval:           formatDurationForAPI(job.ScheduleInterval),
		ScheduleWindowStart:        job.ScheduleWindowStart,
		ScheduleWindowEnd:          job.ScheduleWindowEnd,
		NextRunAt:                  nextRunAt,
		StaleAfter:                 formatDurationForAPI(effectiveStaleAfter(job)),
		RestoreDrillStaleAfter:     formatDurationForAPI(effectiveRestoreDrillStaleAfter(job)),
		MaxSnapshots:               job.MaxSnapshots,
		MaxAge:                     formatDurationForAPI(job.MaxAge),
		RetentionPolicy:            job.RetentionPolicy,
		RetentionStatus:            retentionStatus,
		RetentionMessage:           retentionMessage,
		HealthStatus:               healthStatus,
		HealthMessage:              healthMessage,
		RestoreDrillStatus:         restoreDrillStatus,
		RestoreDrillMessage:        restoreDrillMessage,
		LastRestoreDrillReminderAt: cloneTime(state.LastRestoreDrillReminderAt),
		RestoreDrillStats:          cloneRestoreDrillStats(restoreDrillStats),
		IncludeConfig:              job.IncludeConfig,
		VerifyAfterBackup:          job.VerifyAfterBackup,
		Exclude:                    append([]string(nil), job.Exclude...),
		Running:                    running,
		LastRun:                    cloneRunResult(state.LastRun),
		LastSuccessfulRun:          cloneRunResult(state.LastSuccessfulRun),
		LastRestoreDrill:           cloneRestoreDrillResult(state.LastRestoreDrill),
		RestoreDrillHistory:        cloneRestoreDrillResults(restoreDrillHistory),
		LastRestore:                cloneRestoreResult(state.LastRestore),
		LastRestoreVerify:          cloneRestoreVerifyResult(state.LastRestoreVerify),
		RestoreHistory:             cloneRestoreResults(state.RestoreHistory),
		LastRetentionCheck:         cloneRetentionCheckResult(state.LastRetentionCheck),
	}
}

func (m *Manager) runBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) error {
	switch job.Type {
	case JobTypeLocal:
		return m.runLocalBackup(ctx, job, result)
	case JobTypeRestic:
		return m.runResticBackup(ctx, job, result)
	case JobTypeRclone:
		return m.runRcloneBackup(ctx, job, result)
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) error {
	source := effectiveSource(job, m.storageRoot)
	if err := validateSourceDirectory(source); err != nil {
		return err
	}
	if err := validateDestination(source, job.Destination, m.storageRoot); err != nil {
		return err
	}

	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	partialPath := filepath.Join(snapshotRoot, result.ID+".partial")
	finalPath := filepath.Join(snapshotRoot, result.ID)
	if err := os.MkdirAll(snapshotRoot, 0700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := os.RemoveAll(partialPath); err != nil {
		return fmt.Errorf("cleanup partial snapshot: %w", err)
	}
	if err := os.Mkdir(partialPath, 0700); err != nil {
		return fmt.Errorf("create partial snapshot: %w", err)
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = os.RemoveAll(partialPath)
		}
	}()

	dataPath := filepath.Join(partialPath, "data")
	entries, totalBytes, err := copySourceTree(ctx, source, dataPath, job.Exclude)
	if err != nil {
		return err
	}
	if job.IncludeConfig {
		entry, err := copyConfigFile(ctx, m.configPath, filepath.Join(partialPath, "config", "config.toml"))
		if err != nil {
			return err
		}
		entries = append(entries, *entry)
		totalBytes += entry.Size
		result.ConfigIncluded = true
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ArchivePath < entries[j].ArchivePath
	})

	manifest := Manifest{
		Version:     manifestVersion,
		JobID:       job.ID,
		RunID:       result.ID,
		Source:      source,
		CreatedAt:   result.StartedAt,
		FileCount:   int64(len(entries)),
		TotalBytes:  totalBytes,
		Entries:     entries,
		ConfigPath:  includedConfigPath(job.IncludeConfig, m.configPath),
		Description: "MnemoNAS local directory snapshot",
	}
	partialManifestPath := filepath.Join(partialPath, manifestFileName)
	if err := writeJSONFile(partialManifestPath, manifest, 0600); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if job.VerifyAfterBackup {
		if _, _, err := verifyManifestFiles(ctx, partialPath, manifest); err != nil {
			return fmt.Errorf("verify backup snapshot: %w", err)
		}
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		return fmt.Errorf("finalize backup snapshot: %w", err)
	}
	cleanupPartial = false

	result.SnapshotPath = finalPath
	result.ManifestPath = filepath.Join(finalPath, manifestFileName)
	result.FileCount = manifest.FileCount
	result.TotalBytes = manifest.TotalBytes
	return nil
}

func (m *Manager) runResticBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) error {
	source := effectiveSource(job, m.storageRoot)
	if err := validateSourceDirectory(source); err != nil {
		return err
	}
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}

	args := []string{"-r", job.Repository, "--password-file", job.PasswordFile, "backup", source, "--tag", "mnemonas", "--tag", "job:" + job.ID}
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	args = append(args, job.ExtraArgs...)
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRestic), args...); err != nil {
		return err
	}

	if job.VerifyAfterBackup {
		if err := runExternalCommand(ctx, backupCommand(job, JobTypeRestic), "-r", job.Repository, "--password-file", job.PasswordFile, "check"); err != nil {
			return fmt.Errorf("verify restic repository: %w", err)
		}
	}
	return nil
}

func (m *Manager) runRcloneBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) error {
	source := effectiveSource(job, m.storageRoot)
	if err := validateSourceDirectory(source); err != nil {
		return err
	}
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}

	args := append(rcloneBaseArgs(job), "sync", source, job.Remote, "--create-empty-src-dirs")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	args = append(args, job.ExtraArgs...)
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return err
	}

	if job.VerifyAfterBackup {
		args := append(rcloneBaseArgs(job), "check", source, job.Remote, "--one-way")
		for _, pattern := range job.Exclude {
			args = append(args, "--exclude", pattern)
		}
		if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
			return fmt.Errorf("verify rclone remote: %w", err)
		}
	}
	return nil
}

func (m *Manager) runRestoreDrill(ctx context.Context, job config.BackupJobConfig, opts RestoreDrillOptions, result *RestoreDrillResult) error {
	if job.Type == JobTypeRestic {
		return m.runResticRestoreDrill(ctx, job, result)
	}
	if job.Type == JobTypeRclone {
		return m.runRcloneRestoreDrill(ctx, job, result)
	}
	if job.Type != JobTypeLocal {
		return ErrUnsupportedJobType
	}
	snapshotPath, manifestPath, manifest, err := m.latestManifest(job)
	if err != nil {
		return err
	}

	drillRoot := filepath.Join(job.Destination, job.ID, "restore-drills", result.ID)
	restoredPath := filepath.Join(drillRoot, "restored")
	if err := os.MkdirAll(restoredPath, 0700); err != nil {
		return fmt.Errorf("create restore drill directory: %w", err)
	}
	cleanupDrill := !opts.KeepArtifact
	defer func() {
		if cleanupDrill {
			_ = os.RemoveAll(drillRoot)
		}
	}()

	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath, err := safeJoin(snapshotPath, entry.ArchivePath)
		if err != nil {
			return err
		}
		destinationPath, err := safeJoin(restoredPath, entry.ArchivePath)
		if err != nil {
			return err
		}
		if _, err := copyHostFileWithHash(ctx, sourcePath, destinationPath, entry.ArchivePath, entry.SourcePath); err != nil {
			return fmt.Errorf("restore %s: %w", entry.ArchivePath, err)
		}
	}

	fileCount, verifiedBytes, err := verifyManifestFiles(ctx, restoredPath, manifest)
	if err != nil {
		return fmt.Errorf("verify restored snapshot: %w", err)
	}

	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
	result.FileCount = fileCount
	result.VerifiedBytes = verifiedBytes
	if opts.KeepArtifact {
		result.RestoredPath = restoredPath
	}
	return nil
}

func (m *Manager) runRestorePreview(ctx context.Context, job config.BackupJobConfig, opts RestorePreviewOptions, result *RestorePreviewResult) error {
	switch job.Type {
	case JobTypeLocal:
		return m.runLocalRestorePreview(ctx, job, opts, result)
	case JobTypeRestic:
		return m.runResticRestorePreview(ctx, job, opts, result)
	case JobTypeRclone:
		return m.runRcloneRestorePreview(ctx, job, opts, result)
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRestorePreview(ctx context.Context, job config.BackupJobConfig, opts RestorePreviewOptions, result *RestorePreviewResult) error {
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, err := m.latestManifest(job)
	if err != nil {
		return err
	}
	fileCount, totalBytes, configAvailable, configIncluded, samples, err := summarizeManifestRestorePreview(ctx, manifest, opts.IncludeConfig)
	if err != nil {
		return err
	}

	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.TotalBytes = totalBytes
	result.ConfigAvailable = configAvailable
	result.ConfigIncluded = configIncluded
	result.SamplePaths = samples
	attachRestorePreflight(job, targetPath, result)
	return nil
}

func (m *Manager) runRcloneRestorePreview(ctx context.Context, job config.BackupJobConfig, opts RestorePreviewOptions, result *RestorePreviewResult) error {
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}

	args := append(rcloneBaseArgs(job), "lsjson", job.Remote, "--recursive", "--files-only")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	var fileCount int64
	var totalBytes int64
	var samples []string
	err = runExternalCommandReader(ctx, backupCommand(job, JobTypeRclone), func(stdout io.Reader) error {
		var parseErr error
		fileCount, totalBytes, samples, parseErr = parseRcloneLSJSONReader(stdout)
		return parseErr
	}, args...)
	if err != nil {
		return fmt.Errorf("preview rclone remote: %w", err)
	}

	result.ManifestPath = job.Remote
	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.TotalBytes = totalBytes
	result.SamplePaths = samples
	attachRestorePreflight(job, targetPath, result)
	return nil
}

func (m *Manager) runResticRestorePreview(ctx context.Context, job config.BackupJobConfig, opts RestorePreviewOptions, result *RestorePreviewResult) error {
	source := filepath.Clean(effectiveSource(job, m.storageRoot))
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	targetPath, err := validateRestoreTarget(source, backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}

	args := []string{
		"-r", job.Repository,
		"--password-file", job.PasswordFile,
		"ls", "latest",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:" + job.ID,
		"--path", source,
	}
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	var fileCount int64
	var totalBytes int64
	var samples []string
	err = runExternalCommandReader(ctx, backupCommand(job, JobTypeRestic), func(stdout io.Reader) error {
		var parseErr error
		fileCount, totalBytes, samples, parseErr = parseResticLSJSONReader(stdout, source)
		return parseErr
	}, args...)
	if err != nil {
		return fmt.Errorf("preview restic repository: %w", err)
	}

	result.ManifestPath = job.Repository
	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.TotalBytes = totalBytes
	result.SamplePaths = samples
	attachRestorePreflight(job, targetPath, result)
	return nil
}

func (m *Manager) runRestoreVerify(ctx context.Context, job config.BackupJobConfig, opts RestoreVerifyOptions, result *RestoreVerifyResult) error {
	targetPath, err := validateRestoreVerificationTarget(effectiveSource(job, m.storageRoot), backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}

	fileCount, verifiedBytes, warnings, err := summarizeRestoreVerificationTree(ctx, targetPath)
	if err != nil {
		return err
	}

	configPath := filepath.Join(targetPath, ".mnemonas-restore", "config.toml")
	configFound, err := regularFileExistsNoFollow(configPath)
	if err != nil {
		warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查配置文件失败: %v", err))
	}
	filesDirFound, err := dirExistsNoFollow(filepath.Join(targetPath, "files"))
	if err != nil {
		warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查 files 目录失败: %v", err))
	}
	internalDirFound, err := dirExistsNoFollow(filepath.Join(targetPath, ".mnemonas"))
	if err != nil {
		warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查 .mnemonas 目录失败: %v", err))
	}
	indexFound, err := regularFileExistsNoFollow(filepath.Join(targetPath, ".mnemonas", "index.db"))
	if err != nil {
		warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查索引文件失败: %v", err))
	}
	objectsDirFound, err := dirExistsNoFollow(filepath.Join(targetPath, ".mnemonas", "objects"))
	if err != nil {
		warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查对象目录失败: %v", err))
	}

	if fileCount == 0 {
		warnings = appendRestoreVerificationWarning(warnings, "目标目录未发现常规文件")
	}
	looksLikeStorageRoot := filesDirFound && internalDirFound
	if internalDirFound && !indexFound {
		warnings = appendRestoreVerificationWarning(warnings, ".mnemonas/index.db 缺失，切换前需要确认索引可重建或已经包含")
	}
	if internalDirFound && !objectsDirFound {
		warnings = appendRestoreVerificationWarning(warnings, ".mnemonas/objects 缺失，切换前需要确认对象数据完整")
	}
	if !looksLikeStorageRoot {
		warnings = appendRestoreVerificationWarning(warnings, "未同时检测到 files/ 和 .mnemonas/，仅在恢复的是子目录时才适合直接切换 storage.root")
	}

	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.VerifiedBytes = verifiedBytes
	if configFound {
		result.ConfigPath = configPath
	}
	result.ConfigFound = configFound
	result.FilesDirFound = filesDirFound
	result.InternalDirFound = internalDirFound
	result.IndexFound = indexFound
	result.ObjectsDirFound = objectsDirFound
	result.LooksLikeStorageRoot = looksLikeStorageRoot
	result.Warnings = warnings
	return nil
}

func (m *Manager) runRestore(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	switch job.Type {
	case JobTypeLocal:
		return m.runLocalRestore(ctx, job, opts, result)
	case JobTypeRestic:
		return m.runResticRestore(ctx, job, opts, result)
	case JobTypeRclone:
		return m.runRcloneRestore(ctx, job, opts, result)
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRestore(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, err := m.latestManifest(job)
	if err != nil {
		return err
	}
	partialPath, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = os.RemoveAll(partialPath)
		}
	}()

	fileCount, verifiedBytes, configRestored, configPath, err := restoreManifestToTarget(ctx, snapshotPath, partialPath, manifest, opts.IncludeConfig)
	if err != nil {
		return err
	}
	if err := installRestoreTarget(partialPath, targetPath); err != nil {
		return err
	}
	cleanupPartial = false

	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
	result.TargetPath = targetPath
	result.ConfigRestored = configRestored
	result.ConfigPath = configPath
	result.FileCount = fileCount
	result.VerifiedBytes = verifiedBytes
	return nil
}

func (m *Manager) runRcloneRestore(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	partialPath, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = os.RemoveAll(partialPath)
		}
	}()

	args := append(rcloneBaseArgs(job), "copy", job.Remote, partialPath, "--create-empty-src-dirs")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return fmt.Errorf("restore rclone remote: %w", err)
	}

	args = append(rcloneBaseArgs(job), "check", job.Remote, partialPath, "--one-way")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return fmt.Errorf("verify restored rclone remote: %w", err)
	}

	fileCount, restoredBytes, err := summarizeRestoredTree(ctx, partialPath)
	if err != nil {
		return err
	}
	if err := installRestoreTarget(partialPath, targetPath); err != nil {
		return err
	}
	cleanupPartial = false

	result.ManifestPath = job.Remote
	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.VerifiedBytes = restoredBytes
	return nil
}

func (m *Manager) runResticRestore(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	source := filepath.Clean(effectiveSource(job, m.storageRoot))
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	targetPath, err := validateRestoreTarget(source, backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	partialPath, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	rawPath, err := createNamedRestoreTarget(targetPath, ".restic-"+result.ID)
	if err != nil {
		_ = os.RemoveAll(partialPath)
		return err
	}
	cleanupPartial := true
	cleanupRaw := true
	defer func() {
		if cleanupRaw {
			_ = os.RemoveAll(rawPath)
		}
		if cleanupPartial {
			_ = os.RemoveAll(partialPath)
		}
	}()

	args := []string{
		"-r", job.Repository,
		"--password-file", job.PasswordFile,
		"restore", "latest",
		"--target", rawPath,
		"--tag", "mnemonas",
		"--tag", "job:" + job.ID,
		"--path", source,
	}
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRestic), args...); err != nil {
		return fmt.Errorf("restore restic repository: %w", err)
	}

	fileCount, restoredBytes, err := moveResticRestoredSource(ctx, rawPath, partialPath, source)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(rawPath); err != nil {
		return fmt.Errorf("cleanup restic restore staging: %w", err)
	}
	cleanupRaw = false

	if err := installRestoreTarget(partialPath, targetPath); err != nil {
		return err
	}
	cleanupPartial = false

	result.ManifestPath = job.Repository
	result.TargetPath = targetPath
	result.FileCount = fileCount
	result.VerifiedBytes = restoredBytes
	return nil
}

func (m *Manager) runResticRestoreDrill(ctx context.Context, job config.BackupJobConfig, result *RestoreDrillResult) error {
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRestic), "-r", job.Repository, "--password-file", job.PasswordFile, "check"); err != nil {
		return err
	}
	result.ManifestPath = job.Repository
	return nil
}

func (m *Manager) runRcloneRestoreDrill(ctx context.Context, job config.BackupJobConfig, result *RestoreDrillResult) error {
	source := effectiveSource(job, m.storageRoot)
	if err := validateSourceDirectory(source); err != nil {
		return err
	}
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	args := append(rcloneBaseArgs(job), "check", source, job.Remote, "--one-way")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return err
	}
	result.ManifestPath = job.Remote
	return nil
}

func (m *Manager) runRetentionCheckForJob(ctx context.Context, job config.BackupJobConfig, notify bool) (*RetentionCheckResult, error) {
	startedAt := m.now().UTC()
	result := &RetentionCheckResult{
		ID:        formatRunID(startedAt),
		JobID:     job.ID,
		Status:    StatusRunning,
		StartedAt: startedAt,
		Target:    backupTarget(job),
		Policy:    job.RetentionPolicy,
	}
	_ = m.updateLastRetentionCheck(result)

	err := m.runRetentionCheck(ctx, job, result)
	finishedAt := m.now().UTC()
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.ErrorMessage = err.Error()
	} else {
		result.Status = StatusCompleted
	}
	if len(result.Warnings) > 0 {
		result.Warning = true
	}
	saveErr := m.updateLastRetentionCheck(result)
	if notify {
		m.notifyRetentionCheck(ctx, job, result)
	}
	if saveErr != nil {
		if err != nil {
			return cloneRetentionCheckResult(result), errors.Join(err, saveErr)
		}
		return cloneRetentionCheckResult(result), saveErr
	}
	return cloneRetentionCheckResult(result), err
}

func (m *Manager) runRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) error {
	switch job.Type {
	case JobTypeLocal:
		return m.runLocalRetentionCheck(ctx, job, result)
	case JobTypeRestic:
		return m.runResticRetentionCheck(ctx, job, result)
	case JobTypeRclone:
		return m.runRcloneRetentionCheck(ctx, job, result)
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDestination(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot); err != nil {
		return err
	}
	snapshots, err := listLocalSnapshots(job)
	if err != nil {
		return err
	}
	fillRetentionSnapshotRange(result, snapshotTimes(snapshots))
	result.SnapshotCount = len(snapshots)
	if len(snapshots) == 0 {
		result.Warnings = append(result.Warnings, "尚无本地快照，无法确认保留策略是否有效")
	}
	if job.MaxSnapshots <= 0 && job.MaxAge <= 0 {
		result.Warnings = append(result.Warnings, "本地快照未配置 max_snapshots 或 max_age，旧快照不会自动清理")
	}
	if job.MaxSnapshots > 0 && len(snapshots) > job.MaxSnapshots {
		result.Warnings = append(result.Warnings, fmt.Sprintf("本地快照数量 %d 已超过 max_snapshots=%d", len(snapshots), job.MaxSnapshots))
	}
	if job.MaxAge > 0 && result.OldestSnapshotAt != nil {
		cutoff := m.now().UTC().Add(-job.MaxAge)
		if result.OldestSnapshotAt.Before(cutoff) {
			result.Warnings = append(result.Warnings, "检测到超过 max_age 的本地快照，下一次成功备份后应被清理")
		}
	}
	return nil
}

func (m *Manager) runResticRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) error {
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	args := []string{
		"-r", job.Repository,
		"--password-file", job.PasswordFile,
		"snapshots",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:" + job.ID,
	}
	var times []time.Time
	err := runExternalCommandReader(ctx, backupCommand(job, JobTypeRestic), func(stdout io.Reader) error {
		var parseErr error
		times, parseErr = parseResticSnapshotsJSONReader(stdout)
		return parseErr
	}, args...)
	if err != nil {
		return fmt.Errorf("check restic retention: %w", err)
	}

	result.SnapshotCount = len(times)
	fillRetentionSnapshotRange(result, times)
	if len(times) == 0 {
		result.Warnings = append(result.Warnings, "restic 仓库没有匹配 mnemonas/job 标签的快照")
	}
	if strings.TrimSpace(job.RetentionPolicy) == "" {
		result.Warnings = append(result.Warnings, "restic 远端未配置 retention_policy，无法确认 forget/prune 策略")
	}
	if len(times) == 1 {
		result.Warnings = append(result.Warnings, "restic 当前只有 1 个快照，历史回滚深度有限")
	}
	return nil
}

func (m *Manager) runRcloneRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) error {
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	args := append(rcloneBaseArgs(job), "lsjson", job.Remote, "--recursive", "--files-only")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	var latest *time.Time
	err := runExternalCommandReader(ctx, backupCommand(job, JobTypeRclone), func(stdout io.Reader) error {
		fileCount, totalBytes, latestAt, parseErr := parseRcloneRetentionLSJSONReader(stdout)
		result.FileCount = fileCount
		result.TotalBytes = totalBytes
		latest = latestAt
		return parseErr
	}, args...)
	if err != nil {
		return fmt.Errorf("check rclone retention: %w", err)
	}
	if latest != nil {
		result.LatestSnapshotAt = latest
	}
	if result.FileCount == 0 {
		result.Warnings = append(result.Warnings, "rclone 远端未发现文件，无法确认可恢复内容")
	}
	if strings.TrimSpace(job.RetentionPolicy) == "" {
		result.Warnings = append(result.Warnings, "rclone 远端未配置 retention_policy；MnemoNAS 只能确认当前副本，无法自动确认云端版本保留")
	}
	return nil
}

func (m *Manager) latestManifest(job config.BackupJobConfig) (string, string, Manifest, error) {
	m.mu.Lock()
	state := m.state.Jobs[job.ID]
	lastRun := cloneRunResult(state.LastRun)
	m.mu.Unlock()

	if lastRun != nil && lastRun.Status == StatusCompleted && lastRun.ManifestPath != "" && fileExists(lastRun.ManifestPath) {
		manifest, err := readManifest(lastRun.ManifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		return lastRun.SnapshotPath, lastRun.ManifestPath, manifest, nil
	}

	snapshotsPath := filepath.Join(job.Destination, job.ID, "snapshots")
	entries, err := os.ReadDir(snapshotsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", Manifest{}, ErrNoSnapshots
		}
		return "", "", Manifest{}, fmt.Errorf("list backup snapshots: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasSuffix(entry.Name(), ".partial") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		snapshotPath := filepath.Join(snapshotsPath, name)
		manifestPath := filepath.Join(snapshotPath, manifestFileName)
		if !fileExists(manifestPath) {
			continue
		}
		manifest, err := readManifest(manifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		return snapshotPath, manifestPath, manifest, nil
	}
	return "", "", Manifest{}, ErrNoSnapshots
}

func (m *Manager) healthLocked(job config.BackupJobConfig, state JobState, running bool, now time.Time) (string, string) {
	switch {
	case job.Disabled:
		return "disabled", "任务已禁用"
	case running:
		return "running", "任务正在运行"
	case job.ScheduleInterval <= 0:
		if state.LastRun != nil && state.LastRun.Status == StatusFailed {
			return "failed", "最近一次手动备份失败"
		}
		return "manual", "仅手动执行"
	case state.LastRun != nil && state.LastRun.Status == StatusFailed:
		return "failed", "最近一次计划备份失败"
	case state.LastSuccessfulRun == nil:
		return "due", "尚无成功备份"
	default:
		lastSuccess := state.LastSuccessfulRun.StartedAt
		if state.LastSuccessfulRun.FinishedAt != nil {
			lastSuccess = *state.LastSuccessfulRun.FinishedAt
		}
		staleAfter := effectiveStaleAfter(job)
		if staleAfter > 0 && now.Sub(lastSuccess) > staleAfter {
			return "stale", "最近一次成功备份已过期"
		}
		return "ok", "最近一次成功备份仍在预期窗口内"
	}
}

func (m *Manager) restoreDrillHealthLocked(job config.BackupJobConfig, state JobState, now time.Time) (string, string) {
	if job.Disabled {
		return "disabled", "任务已禁用"
	}
	if job.Type == JobTypeLocal && state.LastSuccessfulRun == nil {
		return "due", "尚无成功备份，先完成备份再执行恢复演练"
	}
	if state.LastRestoreDrill == nil {
		return "due", "尚未完成恢复演练"
	}
	if state.LastRestoreDrill.Status == StatusFailed {
		return "failed", "最近一次恢复演练失败"
	}
	if state.LastRestoreDrill.Status == StatusRunning {
		return "running", "恢复演练正在运行"
	}
	lastDrill := state.LastRestoreDrill.StartedAt
	if state.LastRestoreDrill.FinishedAt != nil {
		lastDrill = *state.LastRestoreDrill.FinishedAt
	}
	staleAfter := effectiveRestoreDrillStaleAfter(job)
	if staleAfter > 0 && now.Sub(lastDrill) > staleAfter {
		return "stale", "恢复演练已过期"
	}
	return "ok", "恢复演练仍在预期窗口内"
}

func retentionHealth(job config.BackupJobConfig, state JobState) (string, string) {
	if job.Disabled {
		return "disabled", "任务已禁用"
	}
	if state.LastRetentionCheck != nil {
		check := state.LastRetentionCheck
		if check.Status == StatusFailed {
			return "failed", "保留策略检测失败"
		}
		if check.Warning {
			if len(check.Warnings) > 0 {
				return "warning", check.Warnings[0]
			}
			return "warning", "保留策略检测存在警告"
		}
		if check.Status == StatusCompleted {
			switch job.Type {
			case JobTypeLocal, JobTypeRestic:
				return "ok", fmt.Sprintf("最近检测到 %d 个快照", check.SnapshotCount)
			case JobTypeRclone:
				return "ok", fmt.Sprintf("最近检测到 %d 个文件，%s", check.FileCount, formatBytesForMessage(check.TotalBytes))
			}
		}
	}
	switch job.Type {
	case JobTypeLocal:
		if job.MaxSnapshots > 0 || job.MaxAge > 0 {
			return "ok", "本地快照自动清理已配置"
		}
		return "warning", "本地快照未配置自动清理"
	case JobTypeRestic, JobTypeRclone:
		if strings.TrimSpace(job.RetentionPolicy) != "" {
			return "ok", "远端保留策略已标记为外部管理"
		}
		return "warning", "远端保留策略需要在外部工具中确认"
	default:
		return "warning", "未知备份类型，无法判断保留策略"
	}
}

func effectiveStaleAfter(job config.BackupJobConfig) time.Duration {
	if job.StaleAfter > 0 {
		return job.StaleAfter
	}
	if job.ScheduleInterval > 0 {
		return job.ScheduleInterval * 2
	}
	return 0
}

func effectiveRestoreDrillStaleAfter(job config.BackupJobConfig) time.Duration {
	if job.RestoreDrillStaleAfter > 0 {
		return job.RestoreDrillStaleAfter
	}
	return defaultRestoreDrillStaleAfter
}

func runFinishedAt(result *RunResult) time.Time {
	if result == nil {
		return time.Time{}
	}
	if result.FinishedAt != nil {
		return result.FinishedAt.UTC()
	}
	return result.StartedAt.UTC()
}

func restoreDrillFinishedAt(result *RestoreDrillResult) time.Time {
	if result == nil {
		return time.Time{}
	}
	if result.FinishedAt != nil {
		return result.FinishedAt.UTC()
	}
	return result.StartedAt.UTC()
}

func classifyRestoreDrillFailure(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrNoSnapshots):
		return FailureCategoryNoSnapshot
	case errors.Is(err, ErrUnsupportedJobType):
		return FailureCategoryUnsupportedJobType
	case errors.Is(err, ErrUnsafePath), errors.Is(err, ErrSourceContainsSymlink), errors.Is(err, ErrUnsupportedFileType):
		return FailureCategoryUnsafePath
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return FailureCategoryCancelled
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "run restic"), strings.Contains(message, "run rclone"):
		return FailureCategoryExternalCommand
	case strings.Contains(message, "checksum"), strings.Contains(message, "size mismatch"), strings.Contains(message, "verify"):
		return FailureCategoryIntegrityCheck
	case strings.Contains(message, "stat "), strings.Contains(message, "read "), strings.Contains(message, "create "), strings.Contains(message, "copy "), strings.Contains(message, "permission"), strings.Contains(message, "no such file"):
		return FailureCategoryIO
	default:
		return FailureCategoryUnknown
	}
}

func formatDurationForAPI(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func formatBytesForMessage(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.2f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.2f EB", value/unit)
}

func (m *Manager) applyRetention(ctx context.Context, job config.BackupJobConfig, currentSnapshotPath string) (int, []string) {
	if job.MaxSnapshots <= 0 && job.MaxAge <= 0 {
		return 0, nil
	}
	snapshots, err := listLocalSnapshots(job)
	if err != nil {
		return 0, []string{fmt.Sprintf("apply retention: %v", err)}
	}
	if len(snapshots) <= 1 {
		return 0, nil
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})

	currentClean := filepath.Clean(currentSnapshotPath)
	latestSnapshot := filepath.Clean(snapshots[0].Path)
	cutoff := m.now().UTC().Add(-job.MaxAge)
	deleteSet := map[string]snapshotInfo{}
	for index, snapshot := range snapshots {
		snapshotPath := filepath.Clean(snapshot.Path)
		if snapshotPath == currentClean || snapshotPath == latestSnapshot {
			continue
		}
		if job.MaxSnapshots > 0 && index >= job.MaxSnapshots {
			deleteSet[snapshotPath] = snapshot
			continue
		}
		if job.MaxAge > 0 && snapshot.CreatedAt.Before(cutoff) {
			deleteSet[snapshotPath] = snapshot
		}
	}

	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	var warnings []string
	pruned := 0
	for _, snapshot := range deleteSet {
		if err := ctx.Err(); err != nil {
			warnings = append(warnings, fmt.Sprintf("retention interrupted: %v", err))
			break
		}
		if !pathContainsOrEquals(snapshotRoot, snapshot.Path) {
			warnings = append(warnings, fmt.Sprintf("skip unsafe snapshot path %s", snapshot.Path))
			continue
		}
		if err := os.RemoveAll(snapshot.Path); err != nil {
			warnings = append(warnings, fmt.Sprintf("remove snapshot %s: %v", snapshot.Name, err))
			continue
		}
		pruned++
	}
	return pruned, warnings
}

type snapshotInfo struct {
	Name      string
	Path      string
	CreatedAt time.Time
}

func listLocalSnapshots(job config.BackupJobConfig) ([]snapshotInfo, error) {
	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	entries, err := os.ReadDir(snapshotRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	snapshots := make([]snapshotInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasSuffix(entry.Name(), ".partial") {
			continue
		}
		snapshotPath := filepath.Join(snapshotRoot, entry.Name())
		createdAt := parseSnapshotTime(entry.Name())
		manifest, err := readManifest(filepath.Join(snapshotPath, manifestFileName))
		if err == nil && !manifest.CreatedAt.IsZero() {
			createdAt = manifest.CreatedAt
		}
		snapshots = append(snapshots, snapshotInfo{
			Name:      entry.Name(),
			Path:      snapshotPath,
			CreatedAt: createdAt.UTC(),
		})
	}
	return snapshots, nil
}

func snapshotTimes(snapshots []snapshotInfo) []time.Time {
	times := make([]time.Time, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !snapshot.CreatedAt.IsZero() {
			times = append(times, snapshot.CreatedAt.UTC())
		}
	}
	return times
}

func fillRetentionSnapshotRange(result *RetentionCheckResult, times []time.Time) {
	if result == nil || len(times) == 0 {
		return
	}
	oldest := times[0].UTC()
	latest := times[0].UTC()
	for _, timestamp := range times[1:] {
		timestamp = timestamp.UTC()
		if timestamp.Before(oldest) {
			oldest = timestamp
		}
		if timestamp.After(latest) {
			latest = timestamp
		}
	}
	result.OldestSnapshotAt = &oldest
	result.LatestSnapshotAt = &latest
}

func parseSnapshotTime(name string) time.Time {
	parsed, err := time.Parse("20060102T150405.000000000Z", name)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func copySourceTree(ctx context.Context, source, destination string, excludes []string) ([]ManifestEntry, int64, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, 0, fmt.Errorf("open backup source: %w", err)
	}
	defer root.Close()

	var entries []ManifestEntry
	var totalBytes int64
	if err := copySourceTreeEntry(ctx, root, ".", destination, excludes, &entries, &totalBytes); err != nil {
		return nil, 0, err
	}
	return entries, totalBytes, nil
}

func copySourceTreeEntry(ctx context.Context, root *os.Root, relPath, destination string, excludes []string, entries *[]ManifestEntry, totalBytes *int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := root.Lstat(relPath)
	if err != nil {
		return mapSourceRootError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSourceContainsSymlink, relPath)
	}

	if relPath != "." {
		relSlash := filepath.ToSlash(relPath)
		if shouldExclude(relSlash, excludes) {
			if info.IsDir() {
				return nil
			}
			return nil
		}
	}

	if info.IsDir() {
		if relPath != "." {
			if err := os.MkdirAll(filepath.Join(destination, relPath), info.Mode().Perm()); err != nil {
				return fmt.Errorf("create backup directory %s: %w", relPath, err)
			}
		} else if err := os.MkdirAll(destination, 0700); err != nil {
			return fmt.Errorf("create backup data directory: %w", err)
		}

		dir, err := rootio.OpenDirNoFollow(root, relPath)
		if err != nil {
			return mapSourceRootError(err)
		}
		children, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if readErr != nil {
			return fmt.Errorf("read backup source directory %s: %w", relPath, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close backup source directory %s: %w", relPath, closeErr)
		}
		sort.Slice(children, func(i, j int) bool {
			return children[i].Name() < children[j].Name()
		})
		for _, child := range children {
			if err := copySourceTreeEntry(ctx, root, childRelPath(relPath, child.Name()), destination, excludes, entries, totalBytes); err != nil {
				return err
			}
		}
		return nil
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrUnsupportedFileType, relPath)
	}

	sourceFile, err := rootio.OpenFileNoFollow(root, relPath, os.O_RDONLY, 0)
	if err != nil {
		return mapSourceRootError(err)
	}
	defer sourceFile.Close()

	archivePath := path.Join("data", filepath.ToSlash(relPath))
	entry, err := copyOpenFileWithHash(ctx, sourceFile, info, filepath.Join(destination, relPath), archivePath, filepath.ToSlash(relPath))
	if err != nil {
		return err
	}
	*entries = append(*entries, *entry)
	*totalBytes += entry.Size
	return nil
}

func copyConfigFile(ctx context.Context, configPath, destination string) (*ManifestEntry, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, errors.New("backup job includes config but config path is unavailable")
	}
	return copyHostFileWithHash(ctx, configPath, destination, "config/config.toml", filepath.Base(configPath))
}

func copyHostFileWithHash(ctx context.Context, sourcePath, destinationPath, archivePath, sourceLabel string) (*ManifestEntry, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("stat source file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrSourceContainsSymlink, sourceLabel)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFileType, sourceLabel)
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open source file: %w", err)
	}
	defer sourceFile.Close()
	return copyOpenFileWithHash(ctx, sourceFile, info, destinationPath, archivePath, sourceLabel)
}

func copyOpenFileWithHash(ctx context.Context, source *os.File, sourceInfo os.FileInfo, destinationPath, archivePath, sourceLabel string) (*ManifestEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0700); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sourceInfo.Mode().Perm())
	if err != nil {
		return nil, fmt.Errorf("create backup file %s: %w", archivePath, err)
	}
	cleanup := true
	defer func() {
		_ = destination.Close()
		if cleanup {
			_ = os.Remove(destinationPath)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), contextReader{ctx: ctx, reader: source})
	if err != nil {
		return nil, fmt.Errorf("copy backup file %s: %w", archivePath, err)
	}
	if err := destination.Sync(); err != nil {
		return nil, fmt.Errorf("sync backup file %s: %w", archivePath, err)
	}
	if err := destination.Close(); err != nil {
		return nil, fmt.Errorf("close backup file %s: %w", archivePath, err)
	}
	if err := os.Chtimes(destinationPath, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		return nil, fmt.Errorf("preserve backup file time %s: %w", archivePath, err)
	}
	cleanup = false

	return &ManifestEntry{
		ArchivePath: archivePath,
		SourcePath:  sourceLabel,
		Size:        written,
		Mode:        uint32(sourceInfo.Mode().Perm()),
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func verifyManifestFiles(ctx context.Context, root string, manifest Manifest) (int64, int64, error) {
	var fileCount int64
	var totalBytes int64
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return fileCount, totalBytes, err
		}
		filePath, err := safeJoin(root, entry.ArchivePath)
		if err != nil {
			return fileCount, totalBytes, err
		}
		size, digest, err := hashFile(ctx, filePath)
		if err != nil {
			return fileCount, totalBytes, fmt.Errorf("hash %s: %w", entry.ArchivePath, err)
		}
		if size != entry.Size {
			return fileCount, totalBytes, fmt.Errorf("size mismatch for %s: got %d, want %d", entry.ArchivePath, size, entry.Size)
		}
		if digest != entry.SHA256 {
			return fileCount, totalBytes, fmt.Errorf("checksum mismatch for %s", entry.ArchivePath)
		}
		fileCount++
		totalBytes += size
	}
	return fileCount, totalBytes, nil
}

func hashFile(ctx context.Context, filePath string) (int64, string, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return 0, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, "", ErrSourceContainsSymlink
	}
	if !info.Mode().IsRegular() {
		return 0, "", ErrUnsupportedFileType
	}
	file, err := os.Open(filePath)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, contextReader{ctx: ctx, reader: file})
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (m *Manager) updateLastRun(result *RunResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRun = cloneRunResult(result)
	if result.Status == StatusCompleted {
		state.LastSuccessfulRun = cloneRunResult(result)
	}
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRestoreDrill(result *RestoreDrillResult, appendHistory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestoreDrill = cloneRestoreDrillResult(result)
	if appendHistory {
		state.RestoreDrillHistory = prependRestoreDrillHistory(state.RestoreDrillHistory, result)
	}
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRestore(result *RestoreResult, appendHistory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestore = cloneRestoreResult(result)
	if appendHistory {
		state.RestoreHistory = prependRestoreHistory(state.RestoreHistory, result)
	}
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRestoreVerify(result *RestoreVerifyResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestoreVerify = cloneRestoreVerifyResult(result)
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRetentionCheck(result *RetentionCheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRetentionCheck = cloneRetentionCheckResult(result)
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) loadState() error {
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read backup state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse backup state: %w", err)
	}
	if state.Jobs == nil {
		state.Jobs = map[string]JobState{}
	}
	m.state = state
	return nil
}

func (m *Manager) saveStateLocked() error {
	if m.state.Jobs == nil {
		m.state.Jobs = map[string]JobState{}
	}
	return writeJSONFile(m.statePath(), m.state, 0600)
}

func (m *Manager) statePath() string {
	return filepath.Join(m.root, stateFileName)
}

func writeJSONFile(filePath string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		return err
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readManifest(filePath string) (Manifest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse backup manifest: %w", err)
	}
	if manifest.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("unsupported backup manifest version: %d", manifest.Version)
	}
	return manifest, nil
}

func normalizeJob(job config.BackupJobConfig, storageRoot string) config.BackupJobConfig {
	job.ID = strings.TrimSpace(job.ID)
	job.Name = strings.TrimSpace(job.Name)
	job.Type = strings.ToLower(strings.TrimSpace(job.Type))
	if job.Type == "" {
		job.Type = JobTypeLocal
	}
	job.Source = strings.TrimSpace(job.Source)
	if job.Source == "" {
		job.Source = storageRoot
	}
	if job.Source != "" && !filepath.IsAbs(job.Source) {
		if absSource, err := filepath.Abs(job.Source); err == nil {
			job.Source = absSource
		}
	}
	job.Destination = strings.TrimSpace(job.Destination)
	if job.Destination != "" && !filepath.IsAbs(job.Destination) {
		if absDestination, err := filepath.Abs(job.Destination); err == nil {
			job.Destination = absDestination
		}
	}
	job.Repository = strings.TrimSpace(job.Repository)
	job.Remote = strings.TrimSpace(job.Remote)
	job.Command = strings.TrimSpace(job.Command)
	if job.Command != "" && !filepath.IsAbs(job.Command) && (strings.ContainsRune(job.Command, filepath.Separator) || strings.ContainsRune(job.Command, '/')) {
		if absCommand, err := filepath.Abs(job.Command); err == nil {
			job.Command = absCommand
		}
	}
	job.PasswordFile = strings.TrimSpace(job.PasswordFile)
	if job.PasswordFile != "" && !filepath.IsAbs(job.PasswordFile) {
		if absPasswordFile, err := filepath.Abs(job.PasswordFile); err == nil {
			job.PasswordFile = absPasswordFile
		}
	}
	job.ConfigFile = strings.TrimSpace(job.ConfigFile)
	if job.ConfigFile != "" && !filepath.IsAbs(job.ConfigFile) {
		if absConfigFile, err := filepath.Abs(job.ConfigFile); err == nil {
			job.ConfigFile = absConfigFile
		}
	}
	job.ScheduleWindowStart = strings.TrimSpace(job.ScheduleWindowStart)
	job.ScheduleWindowEnd = strings.TrimSpace(job.ScheduleWindowEnd)
	job.RetentionPolicy = strings.TrimSpace(job.RetentionPolicy)
	job.ExtraArgs = append([]string(nil), job.ExtraArgs...)
	for i := range job.ExtraArgs {
		job.ExtraArgs[i] = strings.TrimSpace(job.ExtraArgs[i])
	}
	job.Exclude = append([]string(nil), job.Exclude...)
	for i := range job.Exclude {
		job.Exclude[i] = strings.TrimSpace(filepath.ToSlash(job.Exclude[i]))
	}
	return job
}

func cloneJob(job config.BackupJobConfig) config.BackupJobConfig {
	job.ExtraArgs = append([]string(nil), job.ExtraArgs...)
	job.Exclude = append([]string(nil), job.Exclude...)
	return job
}

func cloneRunResult(result *RunResult) *RunResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	return &clone
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneRestoreDrillStats(stats *RestoreDrillStats) *RestoreDrillStats {
	if stats == nil {
		return nil
	}
	clone := *stats
	clone.LatestSuccessAt = cloneTime(stats.LatestSuccessAt)
	clone.LatestFailureAt = cloneTime(stats.LatestFailureAt)
	return &clone
}

func cloneRestoreDrillResult(result *RestoreDrillResult) *RestoreDrillResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	return &clone
}

func cloneRestoreDrillResults(results []*RestoreDrillResult) []*RestoreDrillResult {
	if len(results) == 0 {
		return nil
	}
	clones := make([]*RestoreDrillResult, 0, len(results))
	for _, result := range results {
		if clone := cloneRestoreDrillResult(result); clone != nil {
			clones = append(clones, clone)
		}
	}
	return clones
}

func cloneRestorePreviewResult(result *RestorePreviewResult) *RestorePreviewResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.SamplePaths) > 0 {
		clone.SamplePaths = append([]string(nil), result.SamplePaths...)
	}
	clone.PreflightChecks = cloneRestorePreflightChecks(result.PreflightChecks)
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	if len(result.CutoverChecklist) > 0 {
		clone.CutoverChecklist = append([]string(nil), result.CutoverChecklist...)
	}
	if len(result.RollbackChecklist) > 0 {
		clone.RollbackChecklist = append([]string(nil), result.RollbackChecklist...)
	}
	return &clone
}

func cloneRestoreVerifyResult(result *RestoreVerifyResult) *RestoreVerifyResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	return &clone
}

func cloneRestoreResult(result *RestoreResult) *RestoreResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	clone.PreflightChecks = cloneRestorePreflightChecks(result.PreflightChecks)
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	if len(result.CutoverChecklist) > 0 {
		clone.CutoverChecklist = append([]string(nil), result.CutoverChecklist...)
	}
	if len(result.RollbackChecklist) > 0 {
		clone.RollbackChecklist = append([]string(nil), result.RollbackChecklist...)
	}
	return &clone
}

func cloneRestorePreflightChecks(checks []RestorePreflightCheck) []RestorePreflightCheck {
	if len(checks) == 0 {
		return nil
	}
	return append([]RestorePreflightCheck(nil), checks...)
}

func cloneRetentionCheckResult(result *RetentionCheckResult) *RetentionCheckResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if result.OldestSnapshotAt != nil {
		oldest := *result.OldestSnapshotAt
		clone.OldestSnapshotAt = &oldest
	}
	if result.LatestSnapshotAt != nil {
		latest := *result.LatestSnapshotAt
		clone.LatestSnapshotAt = &latest
	}
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	return &clone
}

func cloneRestoreResults(results []*RestoreResult) []*RestoreResult {
	if len(results) == 0 {
		return nil
	}
	clones := make([]*RestoreResult, 0, len(results))
	for _, result := range results {
		if clone := cloneRestoreResult(result); clone != nil {
			clones = append(clones, clone)
		}
	}
	return clones
}

func prependRestoreHistory(history []*RestoreResult, result *RestoreResult) []*RestoreResult {
	if result == nil {
		return cloneRestoreResults(history)
	}
	next := []*RestoreResult{cloneRestoreResult(result)}
	for _, entry := range history {
		if entry == nil || entry.ID == result.ID {
			continue
		}
		next = append(next, cloneRestoreResult(entry))
		if len(next) >= restoreHistoryLimit {
			break
		}
	}
	return next
}

func prependRestoreDrillHistory(history []*RestoreDrillResult, result *RestoreDrillResult) []*RestoreDrillResult {
	if result == nil {
		return cloneRestoreDrillResults(history)
	}
	next := []*RestoreDrillResult{cloneRestoreDrillResult(result)}
	for _, entry := range history {
		if entry == nil || entry.ID == result.ID {
			continue
		}
		next = append(next, cloneRestoreDrillResult(entry))
		if len(next) >= restoreDrillHistoryLimit {
			break
		}
	}
	return next
}

func restoreDrillHistoryForView(state JobState) []*RestoreDrillResult {
	if len(state.RestoreDrillHistory) > 0 {
		return state.RestoreDrillHistory
	}
	if state.LastRestoreDrill == nil || state.LastRestoreDrill.Status == StatusRunning {
		return nil
	}
	return []*RestoreDrillResult{state.LastRestoreDrill}
}

func buildRestoreDrillStats(history []*RestoreDrillResult) *RestoreDrillStats {
	if len(history) == 0 {
		return nil
	}

	stats := &RestoreDrillStats{}
	var streakStatus string
	for _, result := range history {
		if result == nil || result.Status == StatusRunning {
			continue
		}
		stats.TotalRuns++
		finishedAt := restoreDrillFinishedAt(result)
		switch result.Status {
		case StatusCompleted:
			stats.SuccessfulRuns++
			if stats.LatestSuccessAt == nil {
				value := finishedAt
				stats.LatestSuccessAt = &value
			}
			if streakStatus == "" || streakStatus == StatusCompleted {
				streakStatus = StatusCompleted
				stats.ConsecutiveSuccesses++
			}
		case StatusFailed:
			stats.FailedRuns++
			if stats.LatestFailureAt == nil {
				value := finishedAt
				stats.LatestFailureAt = &value
				stats.LastFailureMessage = result.ErrorMessage
				stats.LastFailureCategory = result.FailureCategory
			}
			if streakStatus == "" || streakStatus == StatusFailed {
				streakStatus = StatusFailed
				stats.ConsecutiveFailures++
			}
		}
		if streakStatus == StatusCompleted && result.Status != StatusCompleted {
			stats.ConsecutiveFailures = 0
		}
		if streakStatus == StatusFailed && result.Status != StatusFailed {
			stats.ConsecutiveSuccesses = 0
		}
	}
	if stats.TotalRuns == 0 {
		return nil
	}
	stats.SuccessRate = float64(stats.SuccessfulRuns) / float64(stats.TotalRuns)
	return stats
}

func effectiveSource(job config.BackupJobConfig, storageRoot string) string {
	if strings.TrimSpace(job.Source) != "" {
		return job.Source
	}
	return storageRoot
}

func backupTarget(job config.BackupJobConfig) string {
	switch job.Type {
	case JobTypeRestic:
		return job.Repository
	case JobTypeRclone:
		return job.Remote
	default:
		return job.Destination
	}
}

func backupCommand(job config.BackupJobConfig, fallback string) string {
	if strings.TrimSpace(job.Command) != "" {
		return job.Command
	}
	return fallback
}

func backupViewCommand(job config.BackupJobConfig) string {
	if job.Type != JobTypeRestic && job.Type != JobTypeRclone {
		return ""
	}
	return backupCommand(job, job.Type)
}

func rcloneBaseArgs(job config.BackupJobConfig) []string {
	if strings.TrimSpace(job.ConfigFile) == "" {
		return nil
	}
	return []string{"--config", job.ConfigFile}
}

func runExternalCommand(ctx context.Context, command string, args ...string) error {
	if strings.TrimSpace(command) == "" {
		return ErrUnsupportedJobType
	}
	stderr := limitedBuffer{limit: externalCommandStderrLimit}
	cmd := execCommandContext(ctx, command, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("run %s: %w: %s", filepath.Base(command), err, detail)
		}
		return fmt.Errorf("run %s: %w", filepath.Base(command), err)
	}
	return nil
}

func runExternalCommandOutput(ctx context.Context, command string, args ...string) ([]byte, error) {
	if strings.TrimSpace(command) == "" {
		return nil, ErrUnsupportedJobType
	}
	stdout := limitedBuffer{limit: externalCommandStdoutLimit}
	stderr := limitedBuffer{limit: externalCommandStderrLimit}
	cmd := execCommandContext(ctx, command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("run %s: %w: %s", filepath.Base(command), err, detail)
		}
		return nil, fmt.Errorf("run %s: %w", filepath.Base(command), err)
	}
	if stdout.Truncated() {
		return nil, fmt.Errorf("run %s: stdout exceeded %d bytes", filepath.Base(command), externalCommandStdoutLimit)
	}
	return stdout.Bytes(), nil
}

func runExternalCommandReader(ctx context.Context, command string, readStdout func(io.Reader) error, args ...string) error {
	if strings.TrimSpace(command) == "" {
		return ErrUnsupportedJobType
	}
	if readStdout == nil {
		return errors.New("external command stdout reader is nil")
	}
	stderr := limitedBuffer{limit: externalCommandStderrLimit}
	cmd := execCommandContext(ctx, command, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open %s stdout: %w", filepath.Base(command), err)
	}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(command), err)
	}

	readErr := readStdout(stdout)
	if readErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		if waitErr != nil {
			return errors.Join(readErr, externalCommandError(command, waitErr, &stderr))
		}
		return readErr
	}
	if waitErr != nil {
		return externalCommandError(command, waitErr, &stderr)
	}
	return nil
}

func externalCommandError(command string, err error, stderr *limitedBuffer) error {
	detail := ""
	if stderr != nil {
		detail = strings.TrimSpace(stderr.String())
	}
	if detail != "" {
		return fmt.Errorf("run %s: %w: %s", filepath.Base(command), err, detail)
	}
	return fmt.Errorf("run %s: %w", filepath.Base(command), err)
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	limit := b.limit
	if limit <= 0 {
		limit = externalCommandStderrLimit
	}
	if b.buffer.Len() < limit {
		remaining := limit - b.buffer.Len()
		if len(p) > remaining {
			_, _ = b.buffer.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
}

func includedConfigPath(include bool, configPath string) string {
	if include {
		return configPath
	}
	return ""
}

func summarizeManifestRestorePreview(ctx context.Context, manifest Manifest, includeConfig bool) (int64, int64, bool, bool, []string, error) {
	var fileCount int64
	var totalBytes int64
	var configAvailable bool
	var configIncluded bool
	var configEntry *ManifestEntry
	samples := make([]string, 0, restorePreviewLimit)

	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return fileCount, totalBytes, configAvailable, configIncluded, samples, err
		}
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if relPath, ok := strings.CutPrefix(archivePath, "data/"); ok {
			fileCount++
			totalBytes += entry.Size
			samples = appendPreviewSample(samples, relPath)
			continue
		}
		if archivePath == "config/config.toml" {
			configAvailable = true
			entryCopy := entry
			configEntry = &entryCopy
		}
	}

	if includeConfig && configEntry != nil {
		if err := ctx.Err(); err != nil {
			return fileCount, totalBytes, configAvailable, configIncluded, samples, err
		}
		configIncluded = true
		fileCount++
		totalBytes += configEntry.Size
		samples = appendPreviewSample(samples, ".mnemonas-restore/config.toml")
	}

	return fileCount, totalBytes, configAvailable, configIncluded, samples, nil
}

type resticLSJSONEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type resticSnapshotJSONEntry struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func parseResticSnapshotsJSON(data []byte) ([]time.Time, error) {
	return parseResticSnapshotsJSONReader(bytes.NewReader(data))
}

func parseResticSnapshotsJSONReader(reader io.Reader) ([]time.Time, error) {
	var snapshots []resticSnapshotJSONEntry
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&snapshots); err != nil {
		return nil, fmt.Errorf("parse restic snapshots output: %w", err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("parse restic snapshots output: unexpected trailing data")
		}
		return nil, fmt.Errorf("parse restic snapshots output: %w", err)
	}
	times := make([]time.Time, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.Time.IsZero() {
			continue
		}
		times = append(times, snapshot.Time.UTC())
	}
	return times, nil
}

func parseResticLSJSON(data []byte, source string) (int64, int64, []string, error) {
	return parseResticLSJSONReader(bytes.NewReader(data), source)
}

func parseResticLSJSONReader(reader io.Reader, source string) (int64, int64, []string, error) {
	var fileCount int64
	var totalBytes int64
	samples := make([]string, 0, restorePreviewLimit)

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), externalCommandStdoutLimit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry resticLSJSONEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return 0, 0, nil, fmt.Errorf("parse restic preview output: %w", err)
		}
		if strings.ToLower(entry.Type) != "file" {
			continue
		}
		entryPath := entry.Path
		if entryPath == "" {
			entryPath = entry.Name
		}
		fileCount++
		totalBytes += entry.Size
		samples = appendPreviewSample(samples, relativeResticPreviewPath(source, entryPath))
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, nil, fmt.Errorf("read restic preview output: %w", err)
	}
	return fileCount, totalBytes, samples, nil
}

type rcloneLSJSONEntry struct {
	Path    string `json:"Path"`
	Name    string `json:"Name"`
	Size    int64  `json:"Size"`
	IsDir   bool   `json:"IsDir"`
	ModTime string `json:"ModTime"`
}

func parseRcloneLSJSON(data []byte) (int64, int64, []string, error) {
	return parseRcloneLSJSONReader(bytes.NewReader(data))
}

func parseRcloneLSJSONReader(reader io.Reader) (int64, int64, []string, error) {
	var fileCount int64
	var totalBytes int64
	samples := make([]string, 0, restorePreviewLimit)

	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("parse rclone preview output: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return 0, 0, nil, errors.New("parse rclone preview output: expected JSON array")
	}
	for decoder.More() {
		var entry rcloneLSJSONEntry
		if err := decoder.Decode(&entry); err != nil {
			return 0, 0, nil, fmt.Errorf("parse rclone preview output: %w", err)
		}
		if entry.IsDir {
			continue
		}
		fileCount++
		totalBytes += entry.Size
		entryPath := entry.Path
		if entryPath == "" {
			entryPath = entry.Name
		}
		samples = appendPreviewSample(samples, entryPath)
	}
	token, err = decoder.Token()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("parse rclone preview output: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return 0, 0, nil, errors.New("parse rclone preview output: expected JSON array end")
	}
	return fileCount, totalBytes, samples, nil
}

func parseRcloneRetentionLSJSON(data []byte) (int64, int64, *time.Time, error) {
	return parseRcloneRetentionLSJSONReader(bytes.NewReader(data))
}

func parseRcloneRetentionLSJSONReader(reader io.Reader) (int64, int64, *time.Time, error) {
	entries, err := decodeRcloneLSJSONEntries(reader)
	if err != nil {
		return 0, 0, nil, err
	}
	var fileCount int64
	var totalBytes int64
	var latest *time.Time
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		fileCount++
		totalBytes += entry.Size
		if strings.TrimSpace(entry.ModTime) == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, entry.ModTime)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("parse rclone file timestamp: %w", err)
		}
		parsed = parsed.UTC()
		if latest == nil || parsed.After(*latest) {
			value := parsed
			latest = &value
		}
	}
	return fileCount, totalBytes, latest, nil
}

func decodeRcloneLSJSONEntries(reader io.Reader) ([]rcloneLSJSONEntry, error) {
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("parse rclone preview output: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, errors.New("parse rclone preview output: expected JSON array")
	}
	entries := make([]rcloneLSJSONEntry, 0)
	for decoder.More() {
		var entry rcloneLSJSONEntry
		if err := decoder.Decode(&entry); err != nil {
			return nil, fmt.Errorf("parse rclone preview output: %w", err)
		}
		entries = append(entries, entry)
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("parse rclone preview output: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return nil, errors.New("parse rclone preview output: expected JSON array end")
	}
	return entries, nil
}

func relativeResticPreviewPath(source, entryPath string) string {
	cleaned := filepath.ToSlash(filepath.Clean(entryPath))
	sourceClean := filepath.ToSlash(filepath.Clean(source))
	if strings.HasPrefix(cleaned, sourceClean+"/") {
		return cleanPreviewSamplePath(strings.TrimPrefix(cleaned, sourceClean+"/"))
	}

	cleanedNoRoot := strings.TrimPrefix(cleaned, "/")
	sourceNoRoot := strings.TrimPrefix(sourceClean, "/")
	if strings.HasPrefix(cleanedNoRoot, sourceNoRoot+"/") {
		return cleanPreviewSamplePath(strings.TrimPrefix(cleanedNoRoot, sourceNoRoot+"/"))
	}
	return cleanPreviewSamplePath(cleanedNoRoot)
}

func appendPreviewSample(samples []string, value string) []string {
	if len(samples) >= restorePreviewLimit {
		return samples
	}
	cleaned := cleanPreviewSamplePath(value)
	if cleaned == "" {
		return samples
	}
	return append(samples, cleaned)
}

func cleanPreviewSamplePath(value string) string {
	cleaned := path.Clean("/" + filepath.ToSlash(strings.TrimSpace(value)))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func validateSourceDirectory(source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("%w: source is empty", ErrUnsafePath)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("stat backup source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: source root", ErrSourceContainsSymlink)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: source is not a directory", ErrUnsafePath)
	}
	return nil
}

func validateDestination(source, destination string, storageRoot string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: destination is empty", ErrUnsafePath)
	}
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("%w: destination must be absolute", ErrUnsafePath)
	}
	sourceClean := filepath.Clean(source)
	destinationClean := filepath.Clean(destination)
	if sourceClean == destinationClean {
		return fmt.Errorf("%w: destination must not equal source", ErrUnsafePath)
	}
	rel, err := filepath.Rel(sourceClean, destinationClean)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: destination must not be inside source", ErrUnsafePath)
	}
	if filepath.IsAbs(storageRoot) {
		storageClean := filepath.Clean(storageRoot)
		rel, err := filepath.Rel(storageClean, destinationClean)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: destination must not be inside storage.root", ErrUnsafePath)
		}
	}
	return nil
}

func validateRestoreTarget(source, destination, storageRoot, targetPath string) (string, error) {
	target, err := validateRestoreTargetPath(source, destination, storageRoot, targetPath)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return target, nil
		}
		return "", fmt.Errorf("stat restore target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: restore target must not be a symlink", ErrUnsafePath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: restore target must be a directory", ErrUnsafePath)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", fmt.Errorf("read restore target: %w", err)
	}
	if len(entries) > 0 {
		return "", ErrRestoreTargetExists
	}
	return target, nil
}

func validateRestoreVerificationTarget(source, destination, storageRoot, targetPath string) (string, error) {
	target, err := validateRestoreTargetPath(source, destination, storageRoot, targetPath)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: restore verification target does not exist", ErrUnsafePath)
		}
		return "", fmt.Errorf("stat restore verification target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: restore verification target must not be a symlink", ErrUnsafePath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: restore verification target must be a directory", ErrUnsafePath)
	}
	return target, nil
}

func validateRestoreTargetPath(source, destination, storageRoot, targetPath string) (string, error) {
	target := strings.TrimSpace(targetPath)
	if target == "" {
		return "", fmt.Errorf("%w: restore target is empty", ErrUnsafePath)
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("%w: restore target must be absolute", ErrUnsafePath)
	}
	target = filepath.Clean(target)
	if target == string(filepath.Separator) {
		return "", fmt.Errorf("%w: restore target must not be filesystem root", ErrUnsafePath)
	}
	for label, protected := range map[string]string{
		"source":       source,
		"storage.root": storageRoot,
		"destination":  destination,
	} {
		if protected == "" || !filepath.IsAbs(protected) {
			continue
		}
		if pathContainsOrEquals(protected, target) || pathContainsOrEquals(target, protected) {
			return "", fmt.Errorf("%w: restore target must be outside %s", ErrUnsafePath, label)
		}
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("stat restore target parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: restore target parent must not be a symlink", ErrUnsafePath)
	}
	if !parentInfo.IsDir() {
		return "", fmt.Errorf("%w: restore target parent is not a directory", ErrUnsafePath)
	}
	return target, nil
}

func createPartialRestoreTarget(targetPath, runID string) (string, error) {
	return createNamedRestoreTarget(targetPath, ".partial-"+runID)
}

func createNamedRestoreTarget(targetPath, suffix string) (string, error) {
	namedPath := targetPath + suffix
	if _, err := os.Lstat(namedPath); err == nil {
		return "", ErrRestoreTargetExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat restore staging target: %w", err)
	}
	if err := os.Mkdir(namedPath, 0700); err != nil {
		return "", fmt.Errorf("create restore staging target: %w", err)
	}
	return namedPath, nil
}

func moveResticRestoredSource(ctx context.Context, rawPath, partialPath, source string) (int64, int64, error) {
	restoredSourcePath, err := resticRestoredSourcePath(rawPath, source)
	if err != nil {
		return 0, 0, err
	}
	info, err := os.Lstat(restoredSourcePath)
	if err != nil {
		return 0, 0, fmt.Errorf("stat restic restored source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, 0, fmt.Errorf("%w: restic restored source must not be a symlink", ErrUnsafePath)
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("%w: restic restored source must be a directory", ErrUnsafePath)
	}

	entries, err := os.ReadDir(restoredSourcePath)
	if err != nil {
		return 0, 0, fmt.Errorf("read restic restored source: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		sourceEntry := filepath.Join(restoredSourcePath, entry.Name())
		targetEntry := filepath.Join(partialPath, entry.Name())
		if _, err := os.Lstat(targetEntry); err == nil {
			return 0, 0, ErrRestoreTargetExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, 0, fmt.Errorf("stat restored target entry: %w", err)
		}
		if err := os.Rename(sourceEntry, targetEntry); err != nil {
			return 0, 0, fmt.Errorf("move restored entry %s: %w", entry.Name(), err)
		}
	}

	fileCount, totalBytes, err := summarizeRestoredTree(ctx, partialPath)
	if err != nil {
		return 0, 0, err
	}
	return fileCount, totalBytes, nil
}

func resticRestoredSourcePath(rawPath, source string) (string, error) {
	source = filepath.Clean(source)
	volume := filepath.VolumeName(source)
	if volume != "" {
		source = strings.TrimPrefix(source, volume)
	}
	source = strings.TrimPrefix(source, string(filepath.Separator))
	if source == "" || source == "." {
		return "", fmt.Errorf("%w: restic restore source must not be filesystem root", ErrUnsafePath)
	}
	return safeJoin(rawPath, source)
}

func summarizeRestoredTree(ctx context.Context, root string) (int64, int64, error) {
	var fileCount int64
	var totalBytes int64
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat restored entry: %w", err)
		}
		if info.Mode().IsRegular() {
			fileCount++
			totalBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("summarize restored tree: %w", err)
	}
	return fileCount, totalBytes, nil
}

func summarizeRestoreVerificationTree(ctx context.Context, root string) (int64, int64, []string, error) {
	var fileCount int64
	var totalBytes int64
	var warnings []string
	err := filepath.WalkDir(root, func(filePath string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == root {
			return nil
		}

		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("stat restored entry: %w", err)
		}
		relPath, err := filepath.Rel(root, filePath)
		if err != nil {
			relPath = filePath
		}
		relPath = filepath.ToSlash(relPath)
		if info.Mode()&os.ModeSymlink != 0 {
			warnings = appendRestoreVerificationWarning(warnings, "发现符号链接，切换前请确认不会指向当前生产目录: "+relPath)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() {
			fileCount++
			totalBytes += info.Size()
			return nil
		}
		warnings = appendRestoreVerificationWarning(warnings, "发现非常规文件，切换前请确认服务是否需要该条目: "+relPath)
		return nil
	})
	if err != nil {
		return 0, 0, nil, fmt.Errorf("summarize restored tree: %w", err)
	}
	return fileCount, totalBytes, warnings, nil
}

func appendRestoreVerificationWarning(warnings []string, warning string) []string {
	if len(warnings) < restoreVerifyWarningLimit {
		return append(warnings, warning)
	}
	if len(warnings) == restoreVerifyWarningLimit {
		return append(warnings, "存在更多校验警告，已截断显示")
	}
	return warnings
}

func regularFileExistsNoFollow(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a symlink", path)
	}
	return info.Mode().IsRegular(), nil
}

func dirExistsNoFollow(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a symlink", path)
	}
	return info.IsDir(), nil
}

func installRestoreTarget(partialPath, targetPath string) error {
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w: restore target changed before install", ErrUnsafePath)
		}
		entries, err := os.ReadDir(targetPath)
		if err != nil {
			return fmt.Errorf("read restore target before install: %w", err)
		}
		if len(entries) > 0 {
			return ErrRestoreTargetExists
		}
		if err := os.Remove(targetPath); err != nil {
			return fmt.Errorf("remove empty restore target: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat restore target before install: %w", err)
	}
	if err := os.Rename(partialPath, targetPath); err != nil {
		return fmt.Errorf("install restore target: %w", err)
	}
	return nil
}

func restoreManifestToTarget(ctx context.Context, snapshotPath, targetPath string, manifest Manifest, includeConfig bool) (int64, int64, bool, string, error) {
	var fileCount int64
	var verifiedBytes int64
	var configRestored bool
	var configPath string
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, err
		}
		archivePath := filepath.ToSlash(entry.ArchivePath)
		relPath, isData := strings.CutPrefix(archivePath, "data/")
		if !isData {
			if includeConfig && archivePath == "config/config.toml" {
				relPath = ".mnemonas-restore/config.toml"
			} else {
				continue
			}
		}
		sourcePath, err := safeJoin(snapshotPath, archivePath)
		if err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, err
		}
		destinationPath, err := safeJoin(targetPath, relPath)
		if err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, err
		}
		if _, err := copyHostFileWithHash(ctx, sourcePath, destinationPath, archivePath, entry.SourcePath); err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("restore %s: %w", archivePath, err)
		}
		size, digest, err := hashFile(ctx, destinationPath)
		if err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("verify restored %s: %w", archivePath, err)
		}
		if size != entry.Size {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("size mismatch for restored %s: got %d, want %d", archivePath, size, entry.Size)
		}
		if digest != entry.SHA256 {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("checksum mismatch for restored %s", archivePath)
		}
		fileCount++
		verifiedBytes += size
		if !isData {
			configRestored = true
			configPath = destinationPath
		}
	}
	return fileCount, verifiedBytes, configRestored, configPath, nil
}

func pathContainsOrEquals(parent, child string) bool {
	parentClean := filepath.Clean(parent)
	childClean := filepath.Clean(child)
	if parentClean == childClean {
		return true
	}
	rel, err := filepath.Rel(parentClean, childClean)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mapSourceRootError(err error) error {
	if err == nil {
		return nil
	}
	if rootio.IsSymlinkError(err) {
		return ErrSourceContainsSymlink
	}
	return err
}

func childRelPath(parent, name string) string {
	if parent == "." {
		return name
	}
	return filepath.Join(parent, name)
}

func shouldExclude(relSlash string, excludes []string) bool {
	relSlash = strings.TrimPrefix(path.Clean("/"+relSlash), "/")
	for _, raw := range excludes {
		pattern := strings.Trim(strings.TrimSpace(filepath.ToSlash(raw)), "/")
		if pattern == "" {
			continue
		}
		if relSlash == pattern || strings.HasPrefix(relSlash, pattern+"/") {
			return true
		}
		if ok, _ := path.Match(pattern, relSlash); ok {
			return true
		}
	}
	return false
}

func safeJoin(root, archivePath string) (string, error) {
	if archivePath == "" || filepath.IsAbs(archivePath) || strings.Contains(archivePath, "\x00") {
		return "", ErrUnsafePath
	}
	slashPath := filepath.ToSlash(archivePath)
	for _, segment := range strings.Split(slashPath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrUnsafePath
		}
	}
	rel := path.Clean(slashPath)
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", ErrUnsafePath
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func formatRunID(t time.Time) string {
	return t.UTC().Format("20060102T150405.000000000Z")
}
