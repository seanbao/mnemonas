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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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

	NotificationTypeBackupRun     = "backup_run"
	NotificationTypeRestoreDrill  = "backup_restore_drill"
	NotificationTypeRetention     = "backup_retention_check"
	NotificationTypeRestore       = "backup_restore"
	NotificationTypeRestoreVerify = "backup_restore_verify"
	NotificationTriggerReminder   = "restore_drill_reminder"

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
	runIDTimeLayout               = "20060102T150405.000000000Z"
	restoreHistoryLimit           = 20
	restoreDrillHistoryLimit      = 20
	restorePreviewLimit           = 10
	restoreVerifyWarningLimit     = 8
	defaultRestoreDrillStaleAfter = 30 * 24 * time.Hour
	restoreDrillReminderCooldown  = 24 * time.Hour
	interruptedStatusMessage      = "任务在服务重启或进程退出前中断"
	restoreDrillCleanupWarning    = "恢复演练已完成，但临时恢复目录清理失败；请检查备份目标中的 restore-drills 目录。"

	defaultSchedulerPollInterval = time.Minute
	externalCommandStderrLimit   = 4096
	externalCommandStdoutLimit   = 4 * 1024 * 1024

	redactedBackupSecretValue        = "<redacted>"
	backupSensitiveNamePartPattern   = `(?:[A-Za-z0-9_.-]|%[0-9A-Fa-f]{2})*`
	backupSensitiveKeySeparator      = `(?:[_-]|%5f|%2d)?`
	backupSensitiveNamePattern       = `(?:` + backupSensitiveNamePartPattern + `(?:password|passwd|secret|token|credential|access` + backupSensitiveKeySeparator + `key|secret` + backupSensitiveKeySeparator + `key|api` + backupSensitiveKeySeparator + `key|authorization|signature)` + backupSensitiveNamePartPattern + `|pass|auth|sig|user|username)`
	backupSensitiveHeaderNamePattern = `(?:` + backupSensitiveNamePartPattern + `(?:password|passwd|secret|token|credential|access` + backupSensitiveKeySeparator + `key|secret` + backupSensitiveKeySeparator + `key|api` + backupSensitiveKeySeparator + `key|signature)` + backupSensitiveNamePartPattern + `|pass|sig|user|username)`
)

var restoreAvailableBytesFunc = restoreAvailableBytes
var afterCopyHostFileLstat = func(string) {}
var afterCopyOpenFileBeforeMetadata = func(string) {}
var afterValidateLocalBackupDestination = func(string) {}

var (
	ErrJobNotFound           = errors.New("backup job not found")
	ErrJobAlreadyRunning     = errors.New("backup job already running")
	ErrJobDisabled           = errors.New("backup job disabled")
	ErrNoSnapshots           = errors.New("backup job has no completed snapshots")
	ErrUnsupportedJobType    = errors.New("unsupported backup job type")
	ErrUnsafePath            = errors.New("unsafe backup path")
	ErrInvalidRestoreRequest = errors.New("invalid restore request")
	ErrRestoreTargetExists   = errors.New("restore target already exists")
	ErrSourceContainsSymlink = errors.New("backup source contains a symlink")
	ErrUnsupportedFileType   = errors.New("backup source contains an unsupported file type")

	backupURLUserinfoPattern                     = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^\s/?#]*@)`)
	backupSensitivePathDoubleQuotedAssignPattern = regexp.MustCompile(`(?i)([/\\])(-{0,2}` + backupSensitiveNamePattern + `=)"([^"/\\]*)"`)
	backupSensitivePathSingleQuotedAssignPattern = regexp.MustCompile(`(?i)([/\\])(-{0,2}` + backupSensitiveNamePattern + `=)'([^'/\\]*)'`)
	backupSensitivePathAssignmentPattern         = regexp.MustCompile(`(?i)([/\\])(-{0,2}` + backupSensitiveNamePattern + `=)([^/\\\s?&;,:\"']+)`)
	backupSensitiveDoubleQuotedAssignPattern     = regexp.MustCompile(`(?i)(^|[\s?&;,:])(-{0,2}` + backupSensitiveNamePattern + `=)"([^"]*)"`)
	backupSensitiveSingleQuotedAssignPattern     = regexp.MustCompile(`(?i)(^|[\s?&;,:])(-{0,2}` + backupSensitiveNamePattern + `=)'([^']*)'`)
	backupSensitiveAssignmentPattern             = regexp.MustCompile(`(?i)(^|[\s?&;,:])(-{0,2}` + backupSensitiveNamePattern + `=)([^\s?&;,:\"']+)`)
	backupSensitiveDoubleQuotedKVPattern         = regexp.MustCompile(`(?i)(^|[{\s,;])("` + backupSensitiveNamePattern + `"\s*:\s*)"([^"]*)"`)
	backupSensitiveSingleQuotedKVPattern         = regexp.MustCompile(`(?i)(^|[{\s,;])('` + backupSensitiveNamePattern + `'\s*:\s*)'([^']*)'`)
	backupSensitiveDoubleQuotedAuthPattern       = regexp.MustCompile(`(?i)(^|[\s,;])((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)"([^"]*)"`)
	backupSensitiveSingleQuotedAuthPattern       = regexp.MustCompile(`(?i)(^|[\s,;])((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)'([^']*)'`)
	backupSensitiveAuthorizationPattern          = regexp.MustCompile(`(?i)(^|[\s,;])((?:proxy-)?authorization\s*:\s*(?:bearer|basic|token)\s+)([^\s,;\"']+)`)
	backupSensitiveDoubleQuotedHeaderPattern     = regexp.MustCompile(`(?i)(^|[\s,;])(` + backupSensitiveHeaderNamePattern + `\s*:\s*)"([^"]*)"`)
	backupSensitiveSingleQuotedHeaderPattern     = regexp.MustCompile(`(?i)(^|[\s,;])(` + backupSensitiveHeaderNamePattern + `\s*:\s*)'([^']*)'`)
	backupSensitiveHeaderPattern                 = regexp.MustCompile(`(?i)(^|[\s,;])(` + backupSensitiveHeaderNamePattern + `\s*:\s*)([^\s,;\"']+)`)
	backupSensitiveDoubleQuotedFlagPattern       = regexp.MustCompile(`(?i)(^|[\s,;:])(-{1,2}` + backupSensitiveNamePattern + `)(\s+)"([^"]*)"`)
	backupSensitiveSingleQuotedFlagPattern       = regexp.MustCompile(`(?i)(^|[\s,;:])(-{1,2}` + backupSensitiveNamePattern + `)(\s+)'([^']*)'`)
	backupSensitiveFlagPattern                   = regexp.MustCompile(`(?i)(^|[\s,;:])(-{1,2}` + backupSensitiveNamePattern + `)(\s+)([^\s,;:\"']+)`)
)

type invalidRestoreRequestError struct {
	err error
}

func (e invalidRestoreRequestError) Error() string {
	if e.err == nil {
		return ErrInvalidRestoreRequest.Error()
	}
	return e.err.Error()
}

func (e invalidRestoreRequestError) Unwrap() error {
	return e.err
}

func (e invalidRestoreRequestError) Is(target error) bool {
	return target == ErrInvalidRestoreRequest
}

func markInvalidRestoreRequest(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidRestoreRequest) {
		return err
	}
	return invalidRestoreRequestError{err: err}
}

func invalidRestoreRequestErrorf(format string, args ...any) error {
	return markInvalidRestoreRequest(fmt.Errorf(format, args...))
}

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
	LastMatchingRestoreVerify  *RestoreVerifyResult  `json:"last_matching_restore_verify,omitempty"`
	RestoreReportFindings      []string              `json:"restore_report_findings,omitempty"`
	RestoreHistory             []*RestoreResult      `json:"restore_history,omitempty"`
	LastRetentionCheck         *RetentionCheckResult `json:"last_retention_check,omitempty"`
}

// RestoreReport is an exportable audit summary for one backup job.
type RestoreReport struct {
	GeneratedAt               time.Time             `json:"generated_at"`
	Job                       JobView               `json:"job"`
	LastRun                   *RunResult            `json:"last_run,omitempty"`
	LastSuccessfulRun         *RunResult            `json:"last_successful_run,omitempty"`
	LastRetentionCheck        *RetentionCheckResult `json:"last_retention_check,omitempty"`
	LastRestoreDrill          *RestoreDrillResult   `json:"last_restore_drill,omitempty"`
	RestoreDrillHistory       []*RestoreDrillResult `json:"restore_drill_history,omitempty"`
	RestoreDrillStats         *RestoreDrillStats    `json:"restore_drill_stats,omitempty"`
	LastRestore               *RestoreResult        `json:"last_restore,omitempty"`
	LastRestoreVerify         *RestoreVerifyResult  `json:"last_restore_verify,omitempty"`
	LastMatchingRestoreVerify *RestoreVerifyResult  `json:"last_matching_restore_verify,omitempty"`
	RestoreHistory            []*RestoreResult      `json:"restore_history,omitempty"`
	Findings                  []string              `json:"findings,omitempty"`
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
	TargetPath          string     `json:"target_path,omitempty"`
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
	WarningCount        int        `json:"warning_count,omitempty"`
	ErrorMessagePresent bool       `json:"error_message_present,omitempty"`
	LocationOmitted     bool       `json:"location_details_omitted,omitempty"`
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
	Warning         bool       `json:"warning,omitempty"`
	Warnings        []string   `json:"warnings,omitempty"`
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
	SnapshotPath         string     `json:"snapshot_path,omitempty"`
	ManifestPath         string     `json:"manifest_path,omitempty"`
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
	if err := ensureBackupStateRoot(root); err != nil {
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
	seenJobIDs := make(map[string]struct{}, len(cfg.Jobs))
	for _, job := range cfg.Jobs {
		normalized := normalizeJob(job, cfg.StorageRoot)
		if normalized.ID == "" {
			continue
		}
		if !isSafeManagerJobID(normalized.ID) {
			return nil, fmt.Errorf("%w: backup job id is unsafe: %q", ErrUnsafePath, normalized.ID)
		}
		jobIDKey := strings.ToLower(normalized.ID)
		if _, ok := seenJobIDs[jobIDKey]; ok {
			return nil, fmt.Errorf("%w: duplicate backup job id: %q", ErrUnsafePath, normalized.ID)
		}
		seenJobIDs[jobIDKey] = struct{}{}
		m.jobs[normalized.ID] = normalized
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

func isSafeManagerJobID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func ensureBackupStateRoot(root string) error {
	if isBackupFilesystemRoot(root) {
		return fmt.Errorf("%w: backup state root must not be filesystem root", ErrUnsafePath)
	}
	if isProtectedBackupSystemDirectory(root) {
		return fmt.Errorf("%w: backup state root must not be protected system directory", ErrUnsafePath)
	}
	if _, err := rootio.MkdirAllPathNoFollowTracked(root, 0700); err != nil {
		if rootio.IsSymlinkError(err) {
			return fmt.Errorf("%w: backup state root must not contain symlink", ErrUnsafePath)
		}
		return err
	}
	return nil
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
	targetPath, err := normalizeRestoreTargetPathSyntax(opts.TargetPath)
	if err != nil {
		return nil, err
	}
	result := &RestoreVerifyResult{
		ID:          formatRunID(startedAt),
		JobID:       job.ID,
		Status:      StatusRunning,
		StartedAt:   startedAt,
		Source:      effectiveSource(job, m.storageRoot),
		Destination: backupTarget(job),
		TargetPath:  targetPath,
	}
	_ = m.updateLastRestoreVerify(result)

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
	m.notifyRestoreVerify(ctx, job, result)
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
	targetPath, err := normalizeRestoreTargetPathSyntax(opts.TargetPath)
	if err != nil {
		return nil, err
	}
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
		m.notifyRestore(ctx, job, result)
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
	m.notifyRestore(ctx, job, result)
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
	message := "backup run completed with warnings"
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = "backup run failed"
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:                NotificationTypeBackupRun,
		Level:               level,
		Message:             message,
		JobID:               job.ID,
		JobType:             job.Type,
		RunID:               result.ID,
		Trigger:             result.Trigger,
		Status:              result.Status,
		StartedAt:           result.StartedAt,
		FinishedAt:          result.FinishedAt,
		FileCount:           result.FileCount,
		TotalBytes:          result.TotalBytes,
		PrunedSnapshots:     result.PrunedSnapshots,
		WarningCount:        len(result.Warnings),
		ErrorMessagePresent: strings.TrimSpace(result.ErrorMessage) != "",
		LocationOmitted:     notificationLocationDetailsOmitted(result.Source, result.Destination, result.SnapshotPath, result.ManifestPath),
		Timestamp:           m.now().UTC(),
	})
}

func (m *Manager) notifyRestoreDrill(ctx context.Context, job config.BackupJobConfig, result *RestoreDrillResult) {
	if m.notifier == nil || result == nil {
		return
	}
	if result.Status != StatusFailed && !result.Warning {
		return
	}

	level := NotificationLevelWarning
	message := "backup restore drill completed with warnings"
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = "backup restore drill failed"
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:                NotificationTypeRestoreDrill,
		Level:               level,
		Message:             message,
		JobID:               job.ID,
		JobType:             job.Type,
		RunID:               result.ID,
		Status:              result.Status,
		StartedAt:           result.StartedAt,
		FinishedAt:          result.FinishedAt,
		FileCount:           result.FileCount,
		VerifiedBytes:       result.VerifiedBytes,
		WarningCount:        len(result.Warnings),
		ErrorMessagePresent: strings.TrimSpace(result.ErrorMessage) != "",
		LocationOmitted:     notificationLocationDetailsOmitted(result.SnapshotPath, result.ManifestPath),
		FailureCategory:     result.FailureCategory,
		Timestamp:           m.now().UTC(),
	})
}

func (m *Manager) notifyRestore(ctx context.Context, job config.BackupJobConfig, result *RestoreResult) {
	if m.notifier == nil || result == nil {
		return
	}
	if result.Status != StatusFailed && len(result.Warnings) == 0 {
		return
	}

	level := NotificationLevelWarning
	message := "backup restore completed with warnings"
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = "backup restore failed"
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:                NotificationTypeRestore,
		Level:               level,
		Message:             message,
		JobID:               job.ID,
		JobType:             job.Type,
		RunID:               result.ID,
		Status:              result.Status,
		StartedAt:           result.StartedAt,
		FinishedAt:          result.FinishedAt,
		FileCount:           result.FileCount,
		VerifiedBytes:       result.VerifiedBytes,
		WarningCount:        len(result.Warnings),
		ErrorMessagePresent: strings.TrimSpace(result.ErrorMessage) != "",
		LocationOmitted:     notificationLocationDetailsOmitted(effectiveSource(job, m.storageRoot), backupTarget(job), result.TargetPath, result.SnapshotPath, result.ManifestPath),
		Timestamp:           m.now().UTC(),
	})
}

func (m *Manager) notifyRestoreVerify(ctx context.Context, job config.BackupJobConfig, result *RestoreVerifyResult) {
	if m.notifier == nil || result == nil {
		return
	}
	if result.Status != StatusFailed && len(result.Warnings) == 0 {
		return
	}

	level := NotificationLevelWarning
	message := "backup restore verification completed with warnings"
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = "backup restore verification failed"
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:                NotificationTypeRestoreVerify,
		Level:               level,
		Message:             message,
		JobID:               job.ID,
		JobType:             job.Type,
		RunID:               result.ID,
		Status:              result.Status,
		StartedAt:           result.StartedAt,
		FinishedAt:          result.FinishedAt,
		FileCount:           result.FileCount,
		VerifiedBytes:       result.VerifiedBytes,
		WarningCount:        len(result.Warnings),
		ErrorMessagePresent: strings.TrimSpace(result.ErrorMessage) != "",
		LocationOmitted:     notificationLocationDetailsOmitted(effectiveSource(job, m.storageRoot), backupTarget(job), result.TargetPath, result.SnapshotPath, result.ManifestPath),
		Timestamp:           m.now().UTC(),
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
	message := "backup retention check completed with warnings"
	if result.Status == StatusFailed {
		level = NotificationLevelCritical
		message = "backup retention check failed"
	}

	_ = m.notifier.NotifyBackupEvent(context.WithoutCancel(ctx), NotificationEvent{
		Type:                NotificationTypeRetention,
		Level:               level,
		Message:             message,
		JobID:               job.ID,
		JobType:             job.Type,
		RunID:               result.ID,
		Status:              result.Status,
		StartedAt:           result.StartedAt,
		FinishedAt:          result.FinishedAt,
		SnapshotCount:       result.SnapshotCount,
		FileCount:           result.FileCount,
		TotalBytes:          result.TotalBytes,
		WarningCount:        len(result.Warnings),
		ErrorMessagePresent: strings.TrimSpace(result.ErrorMessage) != "",
		LocationOmitted:     notificationLocationDetailsOmitted(result.Target),
		Timestamp:           m.now().UTC(),
	})
}

func notificationLocationDetailsOmitted(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
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
		JobType:             job.Type,
		Trigger:             NotificationTriggerReminder,
		LastSuccessfulRunAt: &lastSuccessfulRunAtPtr,
		StaleAfter:          formatDurationForAPI(staleAfter),
		ReminderCooldown:    formatDurationForAPI(restoreDrillReminderCooldown),
		LocationOmitted:     notificationLocationDetailsOmitted(effectiveSource(job, m.storageRoot), backupTarget(job)),
		Timestamp:           now.UTC(),
	}

	if state.LastRestoreDrill == nil {
		if now.Sub(lastSuccessfulRunAt) <= staleAfter {
			return NotificationEvent{}, false
		}
		base.Message = "backup restore drill is due"
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
	base.Message = "backup restore drill is stale"
	base.RunID = state.LastRestoreDrill.ID
	base.Status = "stale"
	base.StartedAt = state.LastRestoreDrill.StartedAt
	base.FinishedAt = cloneTime(state.LastRestoreDrill.FinishedAt)
	base.FileCount = state.LastRestoreDrill.FileCount
	base.VerifiedBytes = state.LastRestoreDrill.VerifiedBytes
	base.LastRestoreDrillAt = &lastRestoreDrillAtPtr
	base.LocationOmitted = notificationLocationDetailsOmitted(effectiveSource(job, m.storageRoot), backupTarget(job), state.LastRestoreDrill.SnapshotPath, state.LastRestoreDrill.ManifestPath)
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
	view := JobView{
		ID:                         job.ID,
		Name:                       job.Name,
		Type:                       job.Type,
		Source:                     sanitizeBackupTargetForAPI(effectiveSource(job, m.storageRoot)),
		Destination:                backupTargetForAPI(job),
		Repository:                 sanitizeBackupTargetForAPI(job.Repository),
		Remote:                     sanitizeBackupTargetForAPI(job.Remote),
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
		Exclude:                    cloneStringSlice(job.Exclude),
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
	matchingRestoreVerify := cloneRestoreVerifyResult(matchingRestoreVerifyForRestore(state.LastRestore, state.LastRestoreVerify))
	view.LastMatchingRestoreVerify = matchingRestoreVerify
	view.RestoreReportFindings = restoreReportFindingsWithMatchingVerify(view, matchingRestoreVerify)
	return view
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
	afterValidateLocalBackupDestination(job.Destination)

	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	partialPath := filepath.Join(snapshotRoot, result.ID+".partial")
	finalPath := filepath.Join(snapshotRoot, result.ID)
	if err := rootio.MkdirAllPathNoFollow(snapshotRoot, 0700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", mapBackupNoFollowError(err, "destination"))
	}
	if err := rootio.MkdirPathNoFollow(partialPath, 0700); err != nil {
		return fmt.Errorf("create partial snapshot: %w", mapBackupNoFollowError(err, "destination"))
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = removeAllBackupPath(partialPath, "partial snapshot")
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
		nextTotalBytes, err := addManifestEntrySize(totalBytes, entry.Size)
		if err != nil {
			return err
		}
		entries = append(entries, *entry)
		totalBytes = nextTotalBytes
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
	if err := rootio.ReplaceEmptyDirPathNoFollow(partialPath, finalPath); err != nil {
		return fmt.Errorf("finalize backup snapshot: %w", mapBackupNoFollowError(err, "destination"))
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
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
	}
	if err := validateSourceTreeNoSymlinks(ctx, source); err != nil {
		return err
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
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
	}
	if err := validateSourceTreeNoSymlinks(ctx, source); err != nil {
		return err
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

var removeRestoreDrillArtifact = removeAllBackupPath

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
	source := effectiveSource(job, m.storageRoot)
	if err := validateDestination(source, job.Destination, m.storageRoot); err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, err := m.latestManifest(job)
	if err != nil {
		return err
	}

	if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
		return fmt.Errorf("verify backup snapshot: %w", err)
	}

	drillRoot := filepath.Join(job.Destination, job.ID, "restore-drills", result.ID)
	restoredPath := filepath.Join(drillRoot, "restored")
	if err := rootio.MkdirAllPathNoFollow(restoredPath, 0700); err != nil {
		return fmt.Errorf("create restore drill directory: %w", mapBackupNoFollowError(err, "restore drill"))
	}
	cleanupDrill := !opts.KeepArtifact
	defer func() {
		if cleanupDrill {
			_ = removeRestoreDrillArtifact(drillRoot, "restore drill")
		}
	}()

	directoryModes, err := restoreSnapshotDirectories(ctx, snapshotPath, "data", filepath.Join(restoredPath, "data"))
	if err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if err := validateRestoreManifestFileEntry(archivePath, entry.Size); err != nil {
			return err
		}
		sourcePath, err := safeJoin(snapshotPath, archivePath)
		if err != nil {
			return err
		}
		destinationPath, err := safeJoin(restoredPath, archivePath)
		if err != nil {
			return err
		}
		if _, err := copyHostFileWithHash(ctx, sourcePath, destinationPath, archivePath, entry.SourcePath); err != nil {
			return fmt.Errorf("restore %s: %w", archivePath, err)
		}
	}
	if err := applyDirectoryModesNoFollow(filepath.Join(restoredPath, "data"), directoryModes, "restore target"); err != nil {
		return err
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
		return nil
	}
	cleanupDrill = false
	if err := removeRestoreDrillArtifact(drillRoot, "restore drill"); err != nil {
		result.Warning = true
		result.Warnings = append(result.Warnings, restoreDrillCleanupWarning)
		result.ArtifactKept = true
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
	if err := verifyManifestTreeContents(ctx, snapshotPath, manifest); err != nil {
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
	source := effectiveSource(job, m.storageRoot)
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
	}
	targetPath, err := validateRestoreTarget(source, backupTarget(job), m.storageRoot, opts.TargetPath)
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
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
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
	configFound, err := regularFileExistsStrictNoFollow(configPath)
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

	if job.Type == JobTypeLocal {
		var compareErr error
		warnings, compareErr = m.appendLocalRestoreSnapshotWarnings(ctx, job, targetPath, result, warnings)
		if compareErr != nil {
			return compareErr
		}
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
			_ = removeAllBackupPath(partialPath, "restore staging target")
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
	source := effectiveSource(job, m.storageRoot)
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
	}
	targetPath, err := validateRestoreTarget(source, backupTarget(job), m.storageRoot, opts.TargetPath)
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
			_ = removeAllBackupPath(partialPath, "restore staging target")
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
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
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
		_ = removeAllBackupPath(partialPath, "restore staging target")
		return err
	}
	cleanupPartial := true
	cleanupRaw := true
	defer func() {
		if cleanupRaw {
			_ = removeAllBackupPath(rawPath, "restic restore staging target")
		}
		if cleanupPartial {
			_ = removeAllBackupPath(partialPath, "restore staging target")
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
	if err := removeAllBackupPath(rawPath, "restic restore staging target"); err != nil {
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
	source := effectiveSource(job, m.storageRoot)
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
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
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
	}
	if err := validateSourceTreeNoSymlinks(ctx, source); err != nil {
		return err
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
	snapshots, err := listLocalSnapshots(ctx, job)
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
	source := effectiveSource(job, m.storageRoot)
	if strings.TrimSpace(job.Repository) == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.TrimSpace(job.PasswordFile) == "" {
		return fmt.Errorf("%w: restic password_file is empty", ErrUnsafePath)
	}
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
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
	source := effectiveSource(job, m.storageRoot)
	if strings.TrimSpace(job.Remote) == "" {
		return fmt.Errorf("%w: rclone remote is empty", ErrUnsafePath)
	}
	if err := validateRemoteCredentialFiles(job, source, m.storageRoot); err != nil {
		return err
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
	if err := validateDestination(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot); err != nil {
		return "", "", Manifest{}, err
	}
	snapshotsPath := filepath.Join(job.Destination, job.ID, "snapshots")
	snapshotDir, err := rootio.OpenDirPathNoFollow(snapshotsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", Manifest{}, ErrNoSnapshots
		}
		return "", "", Manifest{}, fmt.Errorf("list backup snapshots: %w", mapBackupNoFollowError(err, "backup snapshots"))
	}
	entries, readErr := snapshotDir.ReadDir(-1)
	closeErr := snapshotDir.Close()
	if readErr != nil {
		return "", "", Manifest{}, fmt.Errorf("list backup snapshots: %w", readErr)
	}
	if closeErr != nil {
		return "", "", Manifest{}, fmt.Errorf("close backup snapshots: %w", closeErr)
	}
	names, err := localSnapshotDirectoryNames(entries)
	if err != nil {
		return "", "", Manifest{}, err
	}

	m.mu.Lock()
	state := m.state.Jobs[job.ID]
	lastRun := cloneRunResultRaw(state.LastRun)
	m.mu.Unlock()

	if lastRun != nil && lastRun.Status == StatusCompleted && lastRun.ManifestPath != "" {
		if err := validateSnapshotManifestLocation(job, lastRun.ID, lastRun.SnapshotPath, lastRun.ManifestPath); err != nil {
			return "", "", Manifest{}, err
		}
		exists, err := backupManifestFileExistsNoFollow(lastRun.ManifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		if !exists {
			return "", "", Manifest{}, fmt.Errorf("%w: latest backup snapshot %q is missing manifest", ErrUnsafePath, cleanPreviewSamplePath(lastRun.ID))
		}
		manifest, err := readManifest(lastRun.ManifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		if err := validateSnapshotManifestIdentity(job.ID, lastRun.ID, manifest); err != nil {
			return "", "", Manifest{}, err
		}
		return lastRun.SnapshotPath, lastRun.ManifestPath, manifest, nil
	}

	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		snapshotPath := filepath.Join(snapshotsPath, name)
		manifestPath := filepath.Join(snapshotPath, manifestFileName)
		exists, err := backupManifestFileExistsNoFollow(manifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		if !exists {
			return "", "", Manifest{}, fmt.Errorf("%w: backup snapshot %q is missing manifest", ErrUnsafePath, cleanPreviewSamplePath(name))
		}
		manifest, err := readManifest(manifestPath)
		if err != nil {
			return "", "", Manifest{}, err
		}
		if err := validateSnapshotManifestIdentity(job.ID, name, manifest); err != nil {
			return "", "", Manifest{}, err
		}
		return snapshotPath, manifestPath, manifest, nil
	}
	return "", "", Manifest{}, ErrNoSnapshots
}

func localSnapshotDirectoryNames(entries []fs.DirEntry) ([]string, error) {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".partial") {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("%w: backup snapshots directory contains non-snapshot entry %q", ErrUnsafePath, cleanPreviewSamplePath(entry.Name()))
		}
		if _, err := parseRunID(entry.Name()); err != nil {
			return nil, fmt.Errorf("%w: backup snapshots directory contains non-snapshot directory %q", ErrUnsafePath, cleanPreviewSamplePath(entry.Name()))
		}
		names = append(names, entry.Name())
	}
	return names, nil
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
	snapshots, err := listLocalSnapshots(ctx, job)
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
		if err := removeAllBackupPath(snapshot.Path, "backup snapshot"); err != nil {
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

func listLocalSnapshots(ctx context.Context, job config.BackupJobConfig) ([]snapshotInfo, error) {
	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	snapshotDir, err := rootio.OpenDirPathNoFollow(snapshotRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list snapshots: %w", mapBackupNoFollowError(err, "backup snapshots"))
	}
	entries, readErr := snapshotDir.ReadDir(-1)
	closeErr := snapshotDir.Close()
	if readErr != nil {
		return nil, fmt.Errorf("list snapshots: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close snapshots: %w", closeErr)
	}

	names, err := localSnapshotDirectoryNames(entries)
	if err != nil {
		return nil, err
	}

	snapshots := make([]snapshotInfo, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshotPath := filepath.Join(snapshotRoot, name)
		createdAt := parseSnapshotTime(name)
		manifest, err := readManifest(filepath.Join(snapshotPath, manifestFileName))
		if err != nil {
			return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
		}
		if err := validateSnapshotManifestIdentity(job.ID, name, manifest); err != nil {
			return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
		}
		if err := verifyManifestTreeContents(ctx, snapshotPath, manifest); err != nil {
			return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
		}
		if !manifest.CreatedAt.IsZero() {
			createdAt = manifest.CreatedAt
		}
		snapshots = append(snapshots, snapshotInfo{
			Name:      name,
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
	parsed, err := parseRunID(name)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func copySourceTree(ctx context.Context, source, destination string, excludes []string) ([]ManifestEntry, int64, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, 0, fmt.Errorf("open backup source: %w", err)
	}
	defer root.Close()

	var entries []ManifestEntry
	var totalBytes int64
	var directoryModes []directoryMode
	if err := copySourceTreeEntry(ctx, root, ".", destination, excludes, &entries, &totalBytes, &directoryModes); err != nil {
		return nil, 0, err
	}
	if err := applyDirectoryModesNoFollow(destination, directoryModes, "destination"); err != nil {
		return nil, 0, err
	}
	return entries, totalBytes, nil
}

func validateSourceTreeNoSymlinks(ctx context.Context, source string) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("open backup source: %w", err)
	}
	defer root.Close()

	return validateSourceTreeEntryNoSymlinks(ctx, root, ".")
}

func validateSourceTreeEntryNoSymlinks(ctx context.Context, root *os.Root, relPath string) error {
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
	if !info.IsDir() {
		return nil
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
	for _, child := range children {
		childPath, err := childRelPath(relPath, child.Name())
		if err != nil {
			return fmt.Errorf("%w: backup source contains unsafe entry name %q", err, cleanPreviewSamplePath(child.Name()))
		}
		if err := validateSourceTreeEntryNoSymlinks(ctx, root, childPath); err != nil {
			return err
		}
	}
	return nil
}

func copySourceTreeEntry(ctx context.Context, root *os.Root, relPath, destination string, excludes []string, entries *[]ManifestEntry, totalBytes *int64, directoryModes *[]directoryMode) error {
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
			destinationPath := filepath.Join(destination, relPath)
			if err := rootio.MkdirAllPathNoFollow(destinationPath, writableDirectoryMode(info.Mode().Perm())); err != nil {
				return fmt.Errorf("create backup directory %s: %w", relPath, mapBackupNoFollowError(err, "destination"))
			}
			*directoryModes = append(*directoryModes, directoryMode{RelPath: relPath, Mode: info.Mode().Perm()})
		} else {
			if err := rootio.MkdirAllPathNoFollow(destination, writableDirectoryMode(info.Mode().Perm())); err != nil {
				return fmt.Errorf("create backup data directory: %w", mapBackupNoFollowError(err, "destination"))
			}
			*directoryModes = append(*directoryModes, directoryMode{RelPath: relPath, Mode: info.Mode().Perm()})
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
			childPath, err := childRelPath(relPath, child.Name())
			if err != nil {
				return fmt.Errorf("%w: backup source contains unsafe entry name %q", err, cleanPreviewSamplePath(child.Name()))
			}
			if err := copySourceTreeEntry(ctx, root, childPath, destination, excludes, entries, totalBytes, directoryModes); err != nil {
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
	nextTotalBytes, err := addManifestEntrySize(*totalBytes, entry.Size)
	if err != nil {
		return err
	}
	*entries = append(*entries, *entry)
	*totalBytes = nextTotalBytes
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

	afterCopyHostFileLstat(sourcePath)
	sourceFile, openedInfo, err := openRegularFileNoFollow(sourcePath, sourceLabel)
	if err != nil {
		return nil, fmt.Errorf("open source file: %w", err)
	}
	defer sourceFile.Close()
	return copyOpenFileWithHash(ctx, sourceFile, openedInfo, destinationPath, archivePath, sourceLabel)
}

func openRegularFileNoFollow(filePath, sourceLabel string) (*os.File, os.FileInfo, error) {
	file, err := rootio.OpenFilePathNoFollow(filePath, os.O_RDONLY, 0)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return nil, nil, fmt.Errorf("%w: %s", ErrSourceContainsSymlink, sourceLabel)
		}
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%w: %s", ErrUnsupportedFileType, sourceLabel)
	}
	return file, info, nil
}

func copyOpenFileWithHash(ctx context.Context, source *os.File, sourceInfo os.FileInfo, destinationPath, archivePath, sourceLabel string) (*ManifestEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := rootio.MkdirAllPathNoFollow(filepath.Dir(destinationPath), 0700); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", mapBackupNoFollowError(err, "destination"))
	}
	destination, err := rootio.OpenFilePathNoFollow(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sourceInfo.Mode().Perm())
	if err != nil {
		return nil, fmt.Errorf("create backup file %s: %w", archivePath, mapBackupNoFollowError(err, "destination"))
	}
	cleanup := true
	defer func() {
		_ = destination.Close()
		if cleanup {
			_ = rootio.RemoveAllPathNoFollow(destinationPath)
		}
	}()
	if err := destination.Chmod(sourceInfo.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("set backup file mode %s: %w", archivePath, err)
	}

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), contextReader{ctx: ctx, reader: source})
	if err != nil {
		return nil, fmt.Errorf("copy backup file %s: %w", archivePath, err)
	}
	afterCopyOpenFileBeforeMetadata(destinationPath)
	if err := setOpenFileTimes(destination, destinationPath, sourceInfo.ModTime()); err != nil {
		return nil, fmt.Errorf("preserve backup file time %s: %w", archivePath, err)
	}
	if err := ensureOpenFileStillAtPath(destination, destinationPath, archivePath); err != nil {
		return nil, err
	}
	if err := destination.Sync(); err != nil {
		return nil, fmt.Errorf("sync backup file %s: %w", archivePath, err)
	}
	if err := destination.Close(); err != nil {
		return nil, fmt.Errorf("close backup file %s: %w", archivePath, err)
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

func ensureOpenFileStillAtPath(file *os.File, destinationPath, archivePath string) error {
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened backup file %s: %w", archivePath, err)
	}
	pathInfo, err := os.Lstat(destinationPath)
	if err != nil {
		return fmt.Errorf("stat backup file %s: %w", archivePath, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: destination path changed to a symlink: %s", ErrUnsafePath, archivePath)
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("%w: destination path changed to an unsupported file type: %s", ErrUnsupportedFileType, archivePath)
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%w: destination path changed while writing backup file: %s", ErrUnsafePath, archivePath)
	}
	return nil
}

func mapBackupNoFollowError(err error, label string) error {
	if rootio.IsSymlinkError(err) {
		return fmt.Errorf("%w: %s path must not contain symlink", ErrUnsafePath, label)
	}
	return err
}

func verifyManifestFiles(ctx context.Context, root string, manifest Manifest) (int64, int64, error) {
	if err := validateManifestEntries(manifest); err != nil {
		return 0, 0, err
	}
	if err := verifyManifestTreeContents(ctx, root, manifest); err != nil {
		return 0, 0, err
	}

	var fileCount int64
	var totalBytes int64
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return fileCount, totalBytes, err
		}
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if err := validateRestoreManifestFileEntry(archivePath, entry.Size); err != nil {
			return fileCount, totalBytes, err
		}
		filePath, err := safeJoin(root, archivePath)
		if err != nil {
			return fileCount, totalBytes, err
		}
		size, digest, mode, err := hashFile(ctx, filePath)
		if err != nil {
			return fileCount, totalBytes, fmt.Errorf("hash %s: %w", archivePath, err)
		}
		if size != entry.Size {
			return fileCount, totalBytes, fmt.Errorf("size mismatch for %s: got %d, want %d", archivePath, size, entry.Size)
		}
		if digest != entry.SHA256 {
			return fileCount, totalBytes, fmt.Errorf("checksum mismatch for %s", archivePath)
		}
		if mode != manifestEntryMode(entry) {
			return fileCount, totalBytes, fmt.Errorf("mode mismatch for %s: got %04o, want %04o", archivePath, mode, manifestEntryMode(entry))
		}
		nextTotalBytes, err := addManifestEntrySize(totalBytes, size)
		if err != nil {
			return fileCount, totalBytes, err
		}
		fileCount++
		totalBytes = nextTotalBytes
	}
	return fileCount, totalBytes, nil
}

func verifyManifestTreeContents(ctx context.Context, root string, manifest Manifest) error {
	state := manifestTreeVerification{
		expectedFiles: make(map[string]struct{}, len(manifest.Entries)+1),
		seenFiles:     make(map[string]struct{}, len(manifest.Entries)),
	}
	state.expectedFiles[manifestFileName] = struct{}{}
	for _, entry := range manifest.Entries {
		archivePath := filepath.ToSlash(entry.ArchivePath)
		state.expectedFiles[archivePath] = struct{}{}
		if strings.HasPrefix(archivePath, "config/") {
			state.allowConfigDir = true
		}
	}

	if err := verifyManifestTreeEntry(ctx, root, ".", &state); err != nil {
		return fmt.Errorf("verify backup snapshot tree: %w", err)
	}
	if !state.seenDataRoot {
		return fmt.Errorf("%w: backup snapshot is missing data directory", ErrUnsafePath)
	}
	for _, entry := range manifest.Entries {
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if _, ok := state.seenFiles[archivePath]; !ok {
			return fmt.Errorf("%w: backup snapshot is missing manifest file %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
	}
	return nil
}

type manifestTreeVerification struct {
	expectedFiles  map[string]struct{}
	seenFiles      map[string]struct{}
	allowConfigDir bool
	seenDataRoot   bool
}

func verifyManifestTreeEntry(ctx context.Context, root string, relPath string, state *manifestTreeVerification) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	entryPath := root
	if relPath != "." {
		entryPath = filepath.Join(root, relPath)
	}
	relSlash := filepath.ToSlash(relPath)
	info, err := os.Lstat(entryPath)
	if err != nil {
		return fmt.Errorf("stat backup snapshot entry %s: %w", relSlash, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: backup snapshot contains a symlink: %s", ErrUnsafePath, relSlash)
	}
	if info.IsDir() {
		if relPath != "." {
			if relSlash == "data" {
				state.seenDataRoot = true
			}
			if !isAllowedManifestSnapshotDirectory(relSlash, state.allowConfigDir) {
				return fmt.Errorf("%w: backup snapshot contains unmanifested directory %q", ErrUnsafePath, cleanPreviewSamplePath(relSlash))
			}
		}
		dir, err := rootio.OpenDirPathNoFollow(entryPath)
		if err != nil {
			return fmt.Errorf("open backup snapshot directory %s: %w", relSlash, mapBackupNoFollowError(err, "backup snapshot"))
		}
		children, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if readErr != nil {
			return fmt.Errorf("read backup snapshot directory %s: %w", relSlash, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close backup snapshot directory %s: %w", relSlash, closeErr)
		}
		sort.Slice(children, func(i, j int) bool {
			return children[i].Name() < children[j].Name()
		})
		for _, child := range children {
			childPath, err := childRelPath(relPath, child.Name())
			if err != nil {
				return fmt.Errorf("%w: backup snapshot contains unsafe entry name %q", err, cleanPreviewSamplePath(child.Name()))
			}
			if err := verifyManifestTreeEntry(ctx, root, childPath, state); err != nil {
				return err
			}
		}
		return nil
	}
	if relPath == "." {
		return fmt.Errorf("%w: backup snapshot root is not a directory", ErrUnsafePath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: backup snapshot contains unsupported file type: %s", ErrUnsupportedFileType, relSlash)
	}
	if _, ok := state.expectedFiles[relSlash]; !ok {
		return fmt.Errorf("%w: backup snapshot contains unmanifested file %q", ErrUnsafePath, cleanPreviewSamplePath(relSlash))
	}
	if relSlash != manifestFileName {
		state.seenFiles[relSlash] = struct{}{}
	}
	return nil
}

func isAllowedManifestSnapshotDirectory(relPath string, allowConfigDir bool) bool {
	return relPath == "data" || strings.HasPrefix(relPath, "data/") || (allowConfigDir && relPath == "config")
}

func hashFile(ctx context.Context, filePath string) (int64, string, os.FileMode, error) {
	file, info, err := openRegularFileNoFollow(filePath, filePath)
	if err != nil {
		return 0, "", 0, err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, contextReader{ctx: ctx, reader: file})
	if err != nil {
		return 0, "", 0, err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), info.Mode().Perm(), nil
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
	state.LastRun = cloneRunResultRaw(result)
	if result.Status == StatusCompleted {
		state.LastSuccessfulRun = cloneRunResultRaw(result)
	}
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRestoreDrill(result *RestoreDrillResult, appendHistory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestoreDrill = cloneRestoreDrillResultRaw(result)
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
	state.LastRestore = cloneRestoreResultRaw(result)
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
	state.LastRestoreVerify = cloneRestoreVerifyResultRaw(result)
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) updateLastRetentionCheck(result *RetentionCheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRetentionCheck = cloneRetentionCheckResultRaw(result)
	m.state.Jobs[result.JobID] = state
	return m.saveStateLocked()
}

func (m *Manager) loadState() error {
	file, err := rootio.OpenFilePathNoFollow(m.statePath(), os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if rootio.IsSymlinkError(err) {
			return fmt.Errorf("%w: backup state path must not contain symlink", ErrUnsafePath)
		}
		return fmt.Errorf("read backup state: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
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
	if m.recoverInterruptedJobStates(m.now().UTC()) {
		return m.saveStateLocked()
	}
	return nil
}

func (m *Manager) recoverInterruptedJobStates(finishedAt time.Time) bool {
	changed := false
	for jobID, state := range m.state.Jobs {
		if recoverInterruptedRunResult(state.LastRun, finishedAt) {
			changed = true
		}
		if recoverInterruptedRestoreDrillResult(state.LastRestoreDrill, finishedAt) {
			state.RestoreDrillHistory = prependRestoreDrillHistory(state.RestoreDrillHistory, state.LastRestoreDrill)
			changed = true
		}
		if recoverInterruptedRestoreResult(state.LastRestore, finishedAt) {
			state.RestoreHistory = prependRestoreHistory(state.RestoreHistory, state.LastRestore)
			changed = true
		}
		if recoverInterruptedRestoreVerifyResult(state.LastRestoreVerify, finishedAt) {
			changed = true
		}
		if recoverInterruptedRetentionCheckResult(state.LastRetentionCheck, finishedAt) {
			changed = true
		}
		m.state.Jobs[jobID] = state
	}
	return changed
}

func recoverInterruptedRunResult(result *RunResult, finishedAt time.Time) bool {
	if result == nil || result.Status != StatusRunning {
		return false
	}
	result.Status = StatusFailed
	result.ErrorMessage = interruptedStatusMessage
	finishInterruptedTask(&result.FinishedAt, &result.DurationMs, result.StartedAt, finishedAt)
	return true
}

func recoverInterruptedRestoreDrillResult(result *RestoreDrillResult, finishedAt time.Time) bool {
	if result == nil || result.Status != StatusRunning {
		return false
	}
	result.Status = StatusFailed
	result.ErrorMessage = interruptedStatusMessage
	result.FailureCategory = FailureCategoryCancelled
	finishInterruptedTask(&result.FinishedAt, &result.DurationMs, result.StartedAt, finishedAt)
	return true
}

func recoverInterruptedRestoreResult(result *RestoreResult, finishedAt time.Time) bool {
	if result == nil || result.Status != StatusRunning {
		return false
	}
	result.Status = StatusFailed
	result.ErrorMessage = interruptedStatusMessage
	finishInterruptedTask(&result.FinishedAt, &result.DurationMs, result.StartedAt, finishedAt)
	return true
}

func recoverInterruptedRestoreVerifyResult(result *RestoreVerifyResult, finishedAt time.Time) bool {
	if result == nil || result.Status != StatusRunning {
		return false
	}
	result.Status = StatusFailed
	result.ErrorMessage = interruptedStatusMessage
	finishInterruptedTask(&result.FinishedAt, &result.DurationMs, result.StartedAt, finishedAt)
	return true
}

func recoverInterruptedRetentionCheckResult(result *RetentionCheckResult, finishedAt time.Time) bool {
	if result == nil || result.Status != StatusRunning {
		return false
	}
	result.Status = StatusFailed
	result.ErrorMessage = interruptedStatusMessage
	finishInterruptedTask(&result.FinishedAt, &result.DurationMs, result.StartedAt, finishedAt)
	return true
}

func finishInterruptedTask(finishedAt **time.Time, durationMs *int64, startedAt time.Time, fallbackFinishedAt time.Time) {
	if *finishedAt == nil {
		finished := fallbackFinishedAt
		*finishedAt = &finished
	}
	if *durationMs <= 0 && !startedAt.IsZero() {
		*durationMs = (*finishedAt).Sub(startedAt).Milliseconds()
		if *durationMs < 0 {
			*durationMs = 0
		}
	}
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
	if err := rootio.MkdirAllPathNoFollow(filepath.Dir(filePath), 0700); err != nil {
		return mapBackupNoFollowError(err, "backup json")
	}
	tmpPath := filePath + ".tmp"
	tmpFile, err := rootio.OpenFilePathNoFollow(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return mapBackupNoFollowError(err, "backup json")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = rootio.RemoveAllPathNoFollow(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := rootio.ReplaceFilePathNoFollow(tmpPath, filePath); err != nil {
		return mapBackupNoFollowError(err, "backup json")
	}
	cleanup = false
	return nil
}

func readManifest(filePath string) (Manifest, error) {
	exists, err := backupManifestFileExistsNoFollow(filePath)
	if err != nil {
		return Manifest{}, err
	}
	if !exists {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", os.ErrNotExist)
	}

	file, err := rootio.OpenFilePathNoFollow(filePath, os.O_RDONLY, 0)
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", mapBackupNoFollowError(err, "backup manifest"))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Manifest{}, fmt.Errorf("stat backup manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("%w: backup manifest must be a regular file", ErrUnsafePath)
	}
	data, err := io.ReadAll(file)
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
	if err := validateManifestEntries(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateSnapshotManifestIdentity(jobID string, snapshotName string, manifest Manifest) error {
	snapshotTime, err := parseRunID(snapshotName)
	if err != nil {
		return fmt.Errorf("%w: backup snapshot run id is invalid: %q", ErrUnsafePath, snapshotName)
	}
	if manifest.JobID != jobID {
		return fmt.Errorf("%w: backup manifest job id mismatch: got %q, want %q", ErrUnsafePath, manifest.JobID, jobID)
	}
	if manifest.RunID != snapshotName {
		return fmt.Errorf("%w: backup manifest run id mismatch: got %q, want %q", ErrUnsafePath, manifest.RunID, snapshotName)
	}
	if !manifest.CreatedAt.IsZero() && !manifest.CreatedAt.UTC().Equal(snapshotTime) {
		return fmt.Errorf("%w: backup manifest created_at mismatch for run id %q", ErrUnsafePath, snapshotName)
	}
	return nil
}

func validateSnapshotManifestLocation(job config.BackupJobConfig, snapshotName string, snapshotPath string, manifestPath string) error {
	if _, err := parseRunID(snapshotName); err != nil {
		return fmt.Errorf("%w: backup snapshot run id is invalid: %q", ErrUnsafePath, snapshotName)
	}
	expectedSnapshotPath := filepath.Join(job.Destination, job.ID, "snapshots", snapshotName)
	if filepath.Clean(snapshotPath) != filepath.Clean(expectedSnapshotPath) {
		return fmt.Errorf("%w: backup state snapshot path does not match configured destination", ErrUnsafePath)
	}
	expectedManifestPath := filepath.Join(expectedSnapshotPath, manifestFileName)
	if filepath.Clean(manifestPath) != filepath.Clean(expectedManifestPath) {
		return fmt.Errorf("%w: backup state manifest path does not match configured destination", ErrUnsafePath)
	}
	return nil
}

func validateManifestEntries(manifest Manifest) error {
	if manifest.FileCount < 0 {
		return fmt.Errorf("%w: backup manifest has negative file count", ErrUnsafePath)
	}
	if manifest.TotalBytes < 0 {
		return fmt.Errorf("%w: backup manifest has negative total bytes", ErrUnsafePath)
	}
	seenArchivePaths := make(map[string]struct{}, len(manifest.Entries))
	var totalBytes int64
	for _, entry := range manifest.Entries {
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if err := validateRestoreManifestFileEntry(archivePath, entry.Size); err != nil {
			return err
		}
		if err := validateRestoreManifestMode(archivePath, entry.Mode); err != nil {
			return err
		}
		if err := validateRestoreManifestDigest(archivePath, entry.SHA256); err != nil {
			return err
		}
		if _, ok := seenArchivePaths[archivePath]; ok {
			return fmt.Errorf("%w: backup manifest has duplicate archive path %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
		seenArchivePaths[archivePath] = struct{}{}
		nextTotalBytes, err := addManifestEntrySize(totalBytes, entry.Size)
		if err != nil {
			return err
		}
		totalBytes = nextTotalBytes
	}
	fileCount := int64(len(manifest.Entries))
	if manifest.FileCount != fileCount {
		return fmt.Errorf("%w: backup manifest file count mismatch: got %d, want %d", ErrUnsafePath, manifest.FileCount, fileCount)
	}
	if manifest.TotalBytes != totalBytes {
		return fmt.Errorf("%w: backup manifest total bytes mismatch: got %d, want %d", ErrUnsafePath, manifest.TotalBytes, totalBytes)
	}
	return nil
}

func addManifestEntrySize(total int64, size int64) (int64, error) {
	if size > 0 && total > (1<<63-1)-size {
		return 0, fmt.Errorf("%w: backup manifest total size overflows int64", ErrUnsafePath)
	}
	return total + size, nil
}

func manifestEntryMode(entry ManifestEntry) os.FileMode {
	return os.FileMode(entry.Mode) & os.ModePerm
}

func validateRestoreManifestMode(archivePath string, mode uint32) error {
	if mode&^uint32(os.ModePerm) != 0 {
		return fmt.Errorf("%w: backup manifest has invalid file mode for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	return nil
}

func validateRestoreManifestDigest(archivePath string, digest string) error {
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf("%w: backup manifest has invalid sha256 for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	for _, r := range digest {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return fmt.Errorf("%w: backup manifest has invalid sha256 for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
	}
	return nil
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

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func cloneRunResult(result *RunResult) *RunResult {
	return cloneRunResultWithMode(result, true)
}

func cloneRunResultRaw(result *RunResult) *RunResult {
	return cloneRunResultWithMode(result, false)
}

func cloneRunResultWithMode(result *RunResult, sanitize bool) *RunResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.Warnings) > 0 {
		if sanitize {
			clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
		} else {
			clone.Warnings = cloneStringSlice(result.Warnings)
		}
	}
	if sanitize {
		clone.Source = sanitizeBackupTargetForAPI(clone.Source)
		clone.Destination = sanitizeBackupTargetForAPI(clone.Destination)
		clone.SnapshotPath = sanitizeBackupTargetForAPI(clone.SnapshotPath)
		clone.ManifestPath = sanitizeBackupTargetForAPI(clone.ManifestPath)
		clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
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
	clone.LastFailureMessage = sanitizeBackupMessageForAPI(clone.LastFailureMessage)
	return &clone
}

func cloneRestoreDrillResult(result *RestoreDrillResult) *RestoreDrillResult {
	return cloneRestoreDrillResultWithMode(result, true)
}

func cloneRestoreDrillResultRaw(result *RestoreDrillResult) *RestoreDrillResult {
	return cloneRestoreDrillResultWithMode(result, false)
}

func cloneRestoreDrillResultWithMode(result *RestoreDrillResult, sanitize bool) *RestoreDrillResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.Warnings) > 0 {
		if sanitize {
			clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
		} else {
			clone.Warnings = cloneStringSlice(result.Warnings)
		}
	}
	if sanitize {
		clone.SnapshotPath = sanitizeBackupTargetForAPI(clone.SnapshotPath)
		clone.ManifestPath = sanitizeBackupTargetForAPI(clone.ManifestPath)
		clone.RestoredPath = sanitizeBackupTargetForAPI(clone.RestoredPath)
		clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
	}
	return &clone
}

func cloneRestoreDrillResults(results []*RestoreDrillResult) []*RestoreDrillResult {
	return cloneRestoreDrillResultsWithMode(results, true)
}

func cloneRestoreDrillResultsRaw(results []*RestoreDrillResult) []*RestoreDrillResult {
	return cloneRestoreDrillResultsWithMode(results, false)
}

func cloneRestoreDrillResultsWithMode(results []*RestoreDrillResult, sanitize bool) []*RestoreDrillResult {
	if len(results) == 0 {
		return nil
	}
	clones := make([]*RestoreDrillResult, 0, len(results))
	for _, result := range results {
		if clone := cloneRestoreDrillResultWithMode(result, sanitize); clone != nil {
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
	clone.PreflightChecks = cloneRestorePreflightChecksForAPI(result.PreflightChecks)
	if len(result.Warnings) > 0 {
		clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
	}
	if len(result.CutoverChecklist) > 0 {
		clone.CutoverChecklist = append([]string(nil), result.CutoverChecklist...)
	}
	if len(result.RollbackChecklist) > 0 {
		clone.RollbackChecklist = append([]string(nil), result.RollbackChecklist...)
	}
	clone.Destination = sanitizeBackupTargetForAPI(clone.Destination)
	clone.Source = sanitizeBackupTargetForAPI(clone.Source)
	clone.TargetPath = sanitizeBackupTargetForAPI(clone.TargetPath)
	clone.SnapshotPath = sanitizeBackupTargetForAPI(clone.SnapshotPath)
	clone.ManifestPath = sanitizeBackupTargetForAPI(clone.ManifestPath)
	clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
	return &clone
}

func cloneRestoreVerifyResult(result *RestoreVerifyResult) *RestoreVerifyResult {
	return cloneRestoreVerifyResultWithMode(result, true)
}

func cloneRestoreVerifyResultRaw(result *RestoreVerifyResult) *RestoreVerifyResult {
	return cloneRestoreVerifyResultWithMode(result, false)
}

func cloneRestoreVerifyResultWithMode(result *RestoreVerifyResult, sanitize bool) *RestoreVerifyResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if len(result.Warnings) > 0 {
		if sanitize {
			clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
		} else {
			clone.Warnings = cloneStringSlice(result.Warnings)
		}
	}
	if sanitize {
		clone.Source = sanitizeBackupTargetForAPI(clone.Source)
		clone.Destination = sanitizeBackupTargetForAPI(clone.Destination)
		clone.TargetPath = sanitizeBackupTargetForAPI(clone.TargetPath)
		clone.SnapshotPath = sanitizeBackupTargetForAPI(clone.SnapshotPath)
		clone.ManifestPath = sanitizeBackupTargetForAPI(clone.ManifestPath)
		clone.ConfigPath = sanitizeBackupTargetForAPI(clone.ConfigPath)
		clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
	}
	return &clone
}

func cloneRestoreResult(result *RestoreResult) *RestoreResult {
	return cloneRestoreResultWithMode(result, true)
}

func cloneRestoreResultRaw(result *RestoreResult) *RestoreResult {
	return cloneRestoreResultWithMode(result, false)
}

func cloneRestoreResultWithMode(result *RestoreResult, sanitize bool) *RestoreResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.FinishedAt != nil {
		finishedAt := *result.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if sanitize {
		clone.PreflightChecks = cloneRestorePreflightChecksForAPI(result.PreflightChecks)
	} else {
		clone.PreflightChecks = cloneRestorePreflightChecks(result.PreflightChecks)
	}
	if len(result.Warnings) > 0 {
		if sanitize {
			clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
		} else {
			clone.Warnings = cloneStringSlice(result.Warnings)
		}
	}
	if len(result.CutoverChecklist) > 0 {
		clone.CutoverChecklist = append([]string(nil), result.CutoverChecklist...)
	}
	if len(result.RollbackChecklist) > 0 {
		clone.RollbackChecklist = append([]string(nil), result.RollbackChecklist...)
	}
	if sanitize {
		clone.TargetPath = sanitizeBackupTargetForAPI(clone.TargetPath)
		clone.SnapshotPath = sanitizeBackupTargetForAPI(clone.SnapshotPath)
		clone.ManifestPath = sanitizeBackupTargetForAPI(clone.ManifestPath)
		clone.ConfigPath = sanitizeBackupTargetForAPI(clone.ConfigPath)
		clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
	}
	return &clone
}

func cloneRestorePreflightChecks(checks []RestorePreflightCheck) []RestorePreflightCheck {
	if len(checks) == 0 {
		return nil
	}
	return append([]RestorePreflightCheck(nil), checks...)
}

func cloneRestorePreflightChecksForAPI(checks []RestorePreflightCheck) []RestorePreflightCheck {
	clones := cloneRestorePreflightChecks(checks)
	for i := range clones {
		clones[i].Detail = sanitizeBackupMessageForAPI(clones[i].Detail)
	}
	return clones
}

func cloneRetentionCheckResult(result *RetentionCheckResult) *RetentionCheckResult {
	return cloneRetentionCheckResultWithMode(result, true)
}

func cloneRetentionCheckResultRaw(result *RetentionCheckResult) *RetentionCheckResult {
	return cloneRetentionCheckResultWithMode(result, false)
}

func cloneRetentionCheckResultWithMode(result *RetentionCheckResult, sanitize bool) *RetentionCheckResult {
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
		if sanitize {
			clone.Warnings = sanitizeBackupMessagesForAPI(result.Warnings)
		} else {
			clone.Warnings = cloneStringSlice(result.Warnings)
		}
	}
	if sanitize {
		clone.Target = sanitizeBackupTargetForAPI(clone.Target)
		clone.ErrorMessage = sanitizeBackupMessageForAPI(clone.ErrorMessage)
	}
	return &clone
}

func cloneRestoreResults(results []*RestoreResult) []*RestoreResult {
	return cloneRestoreResultsWithMode(results, true)
}

func cloneRestoreResultsRaw(results []*RestoreResult) []*RestoreResult {
	return cloneRestoreResultsWithMode(results, false)
}

func cloneRestoreResultsWithMode(results []*RestoreResult, sanitize bool) []*RestoreResult {
	if len(results) == 0 {
		return nil
	}
	clones := make([]*RestoreResult, 0, len(results))
	for _, result := range results {
		if clone := cloneRestoreResultWithMode(result, sanitize); clone != nil {
			clones = append(clones, clone)
		}
	}
	return clones
}

func prependRestoreHistory(history []*RestoreResult, result *RestoreResult) []*RestoreResult {
	if result == nil {
		return cloneRestoreResultsRaw(history)
	}
	next := []*RestoreResult{cloneRestoreResultRaw(result)}
	for _, entry := range history {
		if entry == nil || entry.ID == result.ID {
			continue
		}
		next = append(next, cloneRestoreResultRaw(entry))
		if len(next) >= restoreHistoryLimit {
			break
		}
	}
	return next
}

func prependRestoreDrillHistory(history []*RestoreDrillResult, result *RestoreDrillResult) []*RestoreDrillResult {
	if result == nil {
		return cloneRestoreDrillResultsRaw(history)
	}
	next := []*RestoreDrillResult{cloneRestoreDrillResultRaw(result)}
	for _, entry := range history {
		if entry == nil || entry.ID == result.ID {
			continue
		}
		next = append(next, cloneRestoreDrillResultRaw(entry))
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

func backupTargetForAPI(job config.BackupJobConfig) string {
	return sanitizeBackupTargetForAPI(backupTarget(job))
}

// SanitizeNotificationText redacts credentials and secret-like values before
// backup notification data leaves the process boundary.
func SanitizeNotificationText(value string) string {
	return sanitizeBackupTargetForAPI(value)
}

func sanitizeBackupTargetForAPI(target string) string {
	if strings.TrimSpace(target) == "" {
		return target
	}
	redacted := backupURLUserinfoPattern.ReplaceAllString(target, "${1}"+redactedBackupSecretValue+"@")
	redacted = backupSensitivePathDoubleQuotedAssignPattern.ReplaceAllString(redacted, "${1}${2}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitivePathSingleQuotedAssignPattern.ReplaceAllString(redacted, "${1}${2}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitivePathAssignmentPattern.ReplaceAllString(redacted, "${1}${2}"+redactedBackupSecretValue)
	redacted = backupSensitiveDoubleQuotedAssignPattern.ReplaceAllString(redacted, "${1}${2}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitiveSingleQuotedAssignPattern.ReplaceAllString(redacted, "${1}${2}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitiveAssignmentPattern.ReplaceAllString(redacted, "${1}${2}"+redactedBackupSecretValue)
	redacted = backupSensitiveDoubleQuotedKVPattern.ReplaceAllString(redacted, "${1}${2}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitiveSingleQuotedKVPattern.ReplaceAllString(redacted, "${1}${2}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitiveDoubleQuotedAuthPattern.ReplaceAllString(redacted, "${1}${2}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitiveSingleQuotedAuthPattern.ReplaceAllString(redacted, "${1}${2}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitiveAuthorizationPattern.ReplaceAllString(redacted, "${1}${2}"+redactedBackupSecretValue)
	redacted = backupSensitiveDoubleQuotedHeaderPattern.ReplaceAllString(redacted, "${1}${2}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitiveSingleQuotedHeaderPattern.ReplaceAllString(redacted, "${1}${2}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitiveHeaderPattern.ReplaceAllString(redacted, "${1}${2}"+redactedBackupSecretValue)
	redacted = backupSensitiveDoubleQuotedFlagPattern.ReplaceAllString(redacted, "${1}${2}${3}\""+redactedBackupSecretValue+"\"")
	redacted = backupSensitiveSingleQuotedFlagPattern.ReplaceAllString(redacted, "${1}${2}${3}'"+redactedBackupSecretValue+"'")
	redacted = backupSensitiveFlagPattern.ReplaceAllString(redacted, "${1}${2}${3}"+redactedBackupSecretValue)
	return redacted
}

func sanitizeBackupMessageForAPI(message string) string {
	return sanitizeBackupTargetForAPI(message)
}

func sanitizeBackupMessagesForAPI(messages []string) []string {
	if len(messages) == 0 {
		return nil
	}
	sanitized := make([]string, len(messages))
	for i, message := range messages {
		sanitized[i] = sanitizeBackupMessageForAPI(message)
	}
	return sanitized
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
		detail := sanitizeExternalCommandDetail(&stderr)
		if detail != "" {
			return fmt.Errorf("run %s: %w: %s", filepath.Base(command), err, detail)
		}
		return fmt.Errorf("run %s: %w", filepath.Base(command), err)
	}
	return nil
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
	detail := sanitizeExternalCommandDetail(stderr)
	if detail != "" {
		return fmt.Errorf("run %s: %w: %s", filepath.Base(command), err, detail)
	}
	return fmt.Errorf("run %s: %w", filepath.Base(command), err)
}

func sanitizeExternalCommandDetail(stderr *limitedBuffer) string {
	if stderr != nil {
		return sanitizeBackupMessageForAPI(strings.TrimSpace(stderr.String()))
	}
	return ""
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
	if err := validateManifestEntries(manifest); err != nil {
		return 0, 0, false, false, nil, err
	}

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
			if err := validateRestoreManifestPath(archivePath); err != nil {
				return fileCount, totalBytes, configAvailable, configIncluded, samples, err
			}
			if err := validateRestoreManifestPath(relPath); err != nil {
				return fileCount, totalBytes, configAvailable, configIncluded, samples, err
			}
			nextTotal, err := addListedFileSize(totalBytes, entry.Size, relPath)
			if err != nil {
				return fileCount, totalBytes, configAvailable, configIncluded, samples, err
			}
			fileCount++
			totalBytes = nextTotal
			samples = appendPreviewSample(samples, relPath)
			continue
		}
		if archivePath == "config/config.toml" {
			if err := validateRestoreManifestPath(archivePath); err != nil {
				return fileCount, totalBytes, configAvailable, configIncluded, samples, err
			}
			configAvailable = true
			entryCopy := entry
			configEntry = &entryCopy
		}
	}

	if includeConfig && configEntry != nil {
		if err := ctx.Err(); err != nil {
			return fileCount, totalBytes, configAvailable, configIncluded, samples, err
		}
		nextTotal, err := addListedFileSize(totalBytes, configEntry.Size, ".mnemonas-restore/config.toml")
		if err != nil {
			return fileCount, totalBytes, configAvailable, configIncluded, samples, err
		}
		configIncluded = true
		fileCount++
		totalBytes = nextTotal
		samples = appendPreviewSample(samples, ".mnemonas-restore/config.toml")
	}

	return fileCount, totalBytes, configAvailable, configIncluded, samples, nil
}

func validateRestoreManifestPath(archivePath string) error {
	_, err := safeJoin(".", archivePath)
	return err
}

func validateRestoreManifestFileEntry(archivePath string, size int64) error {
	if err := validateRestoreManifestPath(archivePath); err != nil {
		return err
	}
	if !isSupportedRestoreManifestArchivePath(archivePath) {
		return fmt.Errorf("%w: backup manifest has unsupported archive path %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	if size < 0 {
		return fmt.Errorf("%w: backup manifest has negative file size for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	return nil
}

func isSupportedRestoreManifestArchivePath(archivePath string) bool {
	if archivePath == "config/config.toml" {
		return true
	}
	relPath, ok := strings.CutPrefix(archivePath, "data/")
	if !ok {
		return false
	}
	return validateRestoreManifestPath(relPath) == nil
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
		samplePath, err := relativeResticPreviewPath(source, entryPath)
		if err != nil {
			return 0, 0, nil, err
		}
		nextTotal, err := addListedFileSize(totalBytes, entry.Size, samplePath)
		if err != nil {
			return 0, 0, nil, err
		}
		fileCount++
		totalBytes = nextTotal
		samples = appendPreviewSample(samples, samplePath)
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
		entryPath := entry.Path
		if entryPath == "" {
			entryPath = entry.Name
		}
		samplePath, err := validateRemoteRelativeListingPath(entryPath)
		if err != nil {
			return 0, 0, nil, err
		}
		nextTotal, err := addListedFileSize(totalBytes, entry.Size, samplePath)
		if err != nil {
			return 0, 0, nil, err
		}
		fileCount++
		totalBytes = nextTotal
		samples = appendPreviewSample(samples, samplePath)
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
		entryPath := entry.Path
		if entryPath == "" {
			entryPath = entry.Name
		}
		samplePath, err := validateRemoteRelativeListingPath(entryPath)
		if err != nil {
			return 0, 0, nil, err
		}
		nextTotal, err := addListedFileSize(totalBytes, entry.Size, samplePath)
		if err != nil {
			return 0, 0, nil, err
		}
		fileCount++
		totalBytes = nextTotal
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

func addListedFileSize(total int64, size int64, entryPath string) (int64, error) {
	if size < 0 {
		return 0, fmt.Errorf("%w: backup listing has negative file size for %q", ErrUnsafePath, cleanPreviewSamplePath(entryPath))
	}
	if size > 0 && total > (1<<63-1)-size {
		return 0, fmt.Errorf("%w: backup listing total size overflows int64", ErrUnsafePath)
	}
	return total + size, nil
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

func relativeResticPreviewPath(source, entryPath string) (string, error) {
	if err := validateRemoteListingPathSegments(entryPath); err != nil {
		return "", err
	}
	cleaned := filepath.Clean(entryPath)
	if cleaned == "" || cleaned == "." {
		return "", unsafeBackupListingPathError(entryPath)
	}

	if filepath.IsAbs(cleaned) {
		sourceClean := filepath.Clean(source)
		if !pathContainsOrEquals(sourceClean, cleaned) {
			return "", unsafeBackupListingPathError(entryPath)
		}
		rel, err := filepath.Rel(sourceClean, cleaned)
		if err != nil {
			return "", fmt.Errorf("parse restic preview output: %w", err)
		}
		return validateRemoteRelativeListingPath(rel)
	}

	return validateRemoteRelativeListingPath(cleaned)
}

func validateRemoteListingPathSegments(entryPath string) error {
	if strings.Contains(entryPath, "\\") {
		return unsafeBackupListingPathError(entryPath)
	}
	slashPath := filepath.ToSlash(entryPath)
	volume := filepath.ToSlash(filepath.VolumeName(entryPath))
	if volume != "" {
		slashPath = strings.TrimPrefix(slashPath, volume)
	}
	for strings.HasPrefix(slashPath, "/") {
		slashPath = strings.TrimPrefix(slashPath, "/")
	}
	if slashPath == "" {
		return unsafeBackupListingPathError(entryPath)
	}
	for _, segment := range strings.Split(slashPath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return unsafeBackupListingPathError(entryPath)
		}
	}
	return nil
}

func validateRemoteRelativeListingPath(entryPath string) (string, error) {
	if entryPath == "" {
		return "", unsafeBackupListingPathError(entryPath)
	}
	if err := validateRemoteListingPathSegments(entryPath); err != nil {
		return "", err
	}
	if _, err := safeJoin(".", entryPath); err != nil {
		return "", unsafeBackupListingPathError(entryPath)
	}
	return cleanPreviewSamplePath(entryPath), nil
}

func unsafeBackupListingPathError(entryPath string) error {
	entryPath = strings.TrimSpace(entryPath)
	if entryPath == "" {
		return fmt.Errorf("%w: backup listing has empty file path", ErrUnsafePath)
	}
	return fmt.Errorf("%w: backup listing contains unsafe file path %q", ErrUnsafePath, entryPath)
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
	cleaned := path.Clean("/" + filepath.ToSlash(value))
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
	if err := validatePathComponentsNoSymlink(destination, "destination"); err != nil {
		return err
	}
	sourceClean := filepath.Clean(source)
	destinationClean := filepath.Clean(destination)
	if isBackupFilesystemRoot(destinationClean) {
		return fmt.Errorf("%w: destination must not be filesystem root", ErrUnsafePath)
	}
	if isProtectedBackupSystemDirectory(destinationClean) {
		return fmt.Errorf("%w: destination must not be protected system directory", ErrUnsafePath)
	}
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

func validateRemoteCredentialFiles(job config.BackupJobConfig, source, storageRoot string) error {
	switch job.Type {
	case JobTypeRestic:
		return validateRemoteCredentialFile(job.PasswordFile, "password_file", source, storageRoot)
	case JobTypeRclone:
		return validateRemoteCredentialFile(job.ConfigFile, "config_file", source, storageRoot)
	default:
		return nil
	}
}

func validateRemoteCredentialFile(filePath, field, source, storageRoot string) error {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil
	}
	if !filepath.IsAbs(filePath) {
		return fmt.Errorf("%w: %s must be absolute", ErrUnsafePath, field)
	}
	if strings.IndexFunc(filePath, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s contains invalid control characters", ErrUnsafePath, field)
	}
	if err := validatePathComponentsNoSymlink(filePath, field); err != nil {
		return err
	}
	for label, protected := range map[string]string{
		"backup source": source,
		"storage.root":  storageRoot,
	} {
		if protected == "" {
			continue
		}
		if !filepath.IsAbs(protected) {
			absProtected, err := filepath.Abs(protected)
			if err != nil {
				continue
			}
			protected = absProtected
		}
		if pathContainsOrEquals(protected, filePath) {
			return fmt.Errorf("%w: %s must not be inside %s", ErrUnsafePath, field, label)
		}
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s does not exist", ErrUnsafePath, field)
		}
		return fmt.Errorf("stat %s: %w", field, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s must not be a symlink", ErrUnsafePath, field)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s must be a regular file", ErrUnsafePath, field)
	}
	return nil
}

func isBackupFilesystemRoot(path string) bool {
	cleanPath := filepath.Clean(path)
	volume := filepath.VolumeName(cleanPath)
	if volume != "" {
		return cleanPath == volume+string(filepath.Separator)
	}
	return cleanPath == string(filepath.Separator)
}

func isProtectedBackupSystemDirectory(path string) bool {
	if isBackupFilesystemRoot(path) {
		return true
	}

	cleanPath := filepath.ToSlash(filepath.Clean(path))
	switch cleanPath {
	case "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/media", "/mnt",
		"/opt", "/proc", "/root", "/run", "/sbin", "/srv", "/sys", "/tmp", "/usr",
		"/usr/local", "/usr/local/bin", "/usr/local/share", "/var":
		return true
	default:
		return false
	}
}

func validatePathComponentsNoSymlink(targetPath, label string) error {
	cleanPath := filepath.Clean(targetPath)
	root := filepath.VolumeName(cleanPath) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleanPath, root)
	if trimmed == "" {
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("stat backup %s path: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s path must not contain symlink", ErrUnsafePath, label)
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
		return "", markInvalidRestoreRequest(fmt.Errorf("stat restore target: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", invalidRestoreRequestErrorf("%w: restore target must not be a symlink", ErrUnsafePath)
	}
	if !info.IsDir() {
		return "", invalidRestoreRequestErrorf("%w: restore target must be a directory", ErrUnsafePath)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", markInvalidRestoreRequest(fmt.Errorf("read restore target: %w", err))
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
			return "", invalidRestoreRequestErrorf("%w: restore verification target does not exist", ErrUnsafePath)
		}
		return "", markInvalidRestoreRequest(fmt.Errorf("stat restore verification target: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", invalidRestoreRequestErrorf("%w: restore verification target must not be a symlink", ErrUnsafePath)
	}
	if !info.IsDir() {
		return "", invalidRestoreRequestErrorf("%w: restore verification target must be a directory", ErrUnsafePath)
	}
	return target, nil
}

func validateRestoreTargetPath(source, destination, storageRoot, targetPath string) (string, error) {
	target, err := normalizeRestoreTargetPathSyntax(targetPath)
	if err != nil {
		return "", err
	}
	if err := validatePathComponentsNoSymlink(target, "restore target"); err != nil {
		return "", markInvalidRestoreRequest(err)
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
			return "", invalidRestoreRequestErrorf("%w: restore target must be outside %s", ErrUnsafePath, label)
		}
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", markInvalidRestoreRequest(fmt.Errorf("stat restore target parent: %w", err))
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", invalidRestoreRequestErrorf("%w: restore target parent must not be a symlink", ErrUnsafePath)
	}
	if !parentInfo.IsDir() {
		return "", invalidRestoreRequestErrorf("%w: restore target parent is not a directory", ErrUnsafePath)
	}
	return target, nil
}

func normalizeRestoreTargetPathSyntax(targetPath string) (string, error) {
	if strings.IndexFunc(targetPath, unicode.IsControl) >= 0 {
		return "", invalidRestoreRequestErrorf("%w: restore target contains invalid control characters", ErrUnsafePath)
	}
	target := strings.TrimSpace(targetPath)
	if target == "" {
		return "", invalidRestoreRequestErrorf("%w: restore target is empty", ErrUnsafePath)
	}
	if strings.Contains(target, "\\") {
		return "", invalidRestoreRequestErrorf("%w: restore target must not contain backslashes", ErrUnsafePath)
	}
	if !strings.HasPrefix(target, "/") {
		return "", invalidRestoreRequestErrorf("%w: restore target must be absolute", ErrUnsafePath)
	}
	if restoreTargetHasDotSegment(target) {
		return "", invalidRestoreRequestErrorf("%w: restore target must not contain dot path segments", ErrUnsafePath)
	}
	target = filepath.Clean(target)
	if isBackupFilesystemRoot(target) {
		return "", invalidRestoreRequestErrorf("%w: restore target must not be filesystem root", ErrUnsafePath)
	}
	if isProtectedBackupSystemDirectory(target) {
		return "", invalidRestoreRequestErrorf("%w: restore target must not be protected system directory", ErrUnsafePath)
	}
	return target, nil
}

func restoreTargetHasDotSegment(targetPath string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(targetPath), "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func createPartialRestoreTarget(targetPath, runID string) (string, error) {
	return createNamedRestoreTarget(targetPath, ".partial-"+runID)
}

func createNamedRestoreTarget(targetPath, suffix string) (string, error) {
	namedPath := targetPath + suffix
	if err := rootio.MkdirPathNoFollow(namedPath, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", ErrRestoreTargetExists
		}
		return "", fmt.Errorf("create restore staging target: %w", mapRestorePathError(err, "restore staging target path must not contain symlink"))
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
		entryName, err := childRelPath(".", entry.Name())
		if err != nil {
			return 0, 0, fmt.Errorf("%w: restic restored source contains unsafe entry name %q", err, cleanPreviewSamplePath(entry.Name()))
		}
		sourceEntry := filepath.Join(restoredSourcePath, entryName)
		if err := rootio.RenamePathIntoDirNoFollow(sourceEntry, partialPath, entryName); err != nil {
			if errors.Is(err, os.ErrExist) {
				return 0, 0, ErrRestoreTargetExists
			}
			return 0, 0, fmt.Errorf("move restored entry %s: %w", cleanPreviewSamplePath(entryName), mapRestorePathError(err, "restore staging target path must not contain symlink"))
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

func restoreWalkRelativePath(root, filePath, label string) (string, error) {
	relPath, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", fmt.Errorf("%w: %s contains entry outside root", ErrUnsafePath, label)
	}
	relPath = filepath.ToSlash(relPath)
	if _, err := safeJoin(root, relPath); err != nil {
		return "", fmt.Errorf("%w: %s contains unsafe entry path %q", err, label, cleanPreviewSamplePath(relPath))
	}
	return relPath, nil
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
		relPath, err := restoreWalkRelativePath(root, filePath, "restored tree")
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat restored entry: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: restored entry must not be a symlink: %s", ErrUnsafePath, relPath)
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() {
			nextTotal, err := addListedFileSize(totalBytes, info.Size(), relPath)
			if err != nil {
				return err
			}
			fileCount++
			totalBytes = nextTotal
			return nil
		}
		return fmt.Errorf("%w: restored entry has unsupported file type: %s", ErrUnsafePath, relPath)
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
		relPath, err := restoreWalkRelativePath(root, filePath, "restore verification tree")
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			warnings = appendRestoreVerificationWarning(warnings, "发现符号链接，切换前请确认不会指向当前生产目录: "+relPath)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() {
			nextTotal, err := addListedFileSize(totalBytes, info.Size(), relPath)
			if err != nil {
				return err
			}
			fileCount++
			totalBytes = nextTotal
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

func (m *Manager) appendLocalRestoreSnapshotWarnings(ctx context.Context, job config.BackupJobConfig, targetPath string, result *RestoreVerifyResult, warnings []string) ([]string, error) {
	if restore := m.latestCompletedRestoreForTarget(job.ID, targetPath); restore != nil {
		snapshotPath, manifestPath, manifest, err := localRestoreSnapshotManifest(job, restore)
		if err != nil {
			return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法对照恢复时使用的本地备份快照: %v", err)), nil
		}
		if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
			return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法校验恢复时使用的本地备份快照: %v", err)), nil
		}
		setRestoreVerifySnapshotReference(result, snapshotPath, manifestPath)
		return appendLocalRestoreSnapshotComparisonWarnings(ctx, snapshotPath, targetPath, manifest, warnings)
	}

	snapshotPath, manifestPath, manifest, err := m.latestManifest(job)
	if err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法对照最新本地备份 manifest: %v", err)), nil
	}
	if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法校验最新本地备份快照: %v", err)), nil
	}
	setRestoreVerifySnapshotReference(result, snapshotPath, manifestPath)
	return appendLocalRestoreSnapshotComparisonWarnings(ctx, snapshotPath, targetPath, manifest, warnings)
}

func setRestoreVerifySnapshotReference(result *RestoreVerifyResult, snapshotPath string, manifestPath string) {
	if result == nil {
		return
	}
	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
}

func appendLocalRestoreSnapshotComparisonWarnings(ctx context.Context, snapshotPath string, targetPath string, manifest Manifest, warnings []string) ([]string, error) {
	var compareErr error
	warnings, compareErr = appendRestoreDirectoryComparisonWarnings(ctx, snapshotPath, targetPath, warnings)
	if compareErr != nil {
		return warnings, compareErr
	}
	return appendRestoreFileComparisonWarnings(ctx, targetPath, manifest, warnings)
}

func (m *Manager) latestCompletedRestoreForTarget(jobID string, targetPath string) *RestoreResult {
	targetPath = strings.TrimSpace(targetPath)
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[jobID]
	for _, restore := range state.RestoreHistory {
		if restoreMatchesTargetSnapshot(restore, targetPath) {
			return cloneRestoreResultRaw(restore)
		}
	}
	if restoreMatchesTargetSnapshot(state.LastRestore, targetPath) {
		return cloneRestoreResultRaw(state.LastRestore)
	}
	return nil
}

func restoreMatchesTargetSnapshot(restore *RestoreResult, targetPath string) bool {
	if restore == nil || restore.Status != StatusCompleted {
		return false
	}
	if strings.TrimSpace(restore.SnapshotPath) == "" {
		return false
	}
	return strings.TrimSpace(restore.TargetPath) == targetPath
}

func localRestoreSnapshotManifest(job config.BackupJobConfig, restore *RestoreResult) (string, string, Manifest, error) {
	snapshotPath := filepath.Clean(strings.TrimSpace(restore.SnapshotPath))
	if snapshotPath == "." || snapshotPath == "" {
		return "", "", Manifest{}, fmt.Errorf("%w: restore record is missing snapshot path", ErrUnsafePath)
	}
	snapshotName := filepath.Base(snapshotPath)
	manifestPath := filepath.Join(snapshotPath, manifestFileName)
	if err := validateSnapshotManifestLocation(job, snapshotName, snapshotPath, manifestPath); err != nil {
		return "", "", Manifest{}, err
	}
	exists, err := backupManifestFileExistsNoFollow(manifestPath)
	if err != nil {
		return "", "", Manifest{}, err
	}
	if !exists {
		return "", "", Manifest{}, fmt.Errorf("%w: restored backup snapshot %q is missing manifest", ErrUnsafePath, cleanPreviewSamplePath(snapshotName))
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return "", "", Manifest{}, err
	}
	if err := validateSnapshotManifestIdentity(job.ID, snapshotName, manifest); err != nil {
		return "", "", Manifest{}, err
	}
	return snapshotPath, manifestPath, manifest, nil
}

func appendRestoreDirectoryComparisonWarnings(ctx context.Context, snapshotPath string, targetPath string, warnings []string) ([]string, error) {
	sourceRootPath, err := safeJoin(snapshotPath, "data")
	if err != nil {
		return warnings, err
	}
	sourceRootInfo, err := os.Lstat(sourceRootPath)
	if err != nil {
		return warnings, fmt.Errorf("stat backup snapshot data root: %w", err)
	}
	if sourceRootInfo.Mode()&os.ModeSymlink != 0 || !sourceRootInfo.IsDir() {
		return warnings, fmt.Errorf("%w: backup snapshot data root must be a directory", ErrUnsafePath)
	}
	targetRootInfo, err := os.Lstat(targetPath)
	if err != nil {
		return warnings, fmt.Errorf("stat restore target root: %w", err)
	}
	if targetRootInfo.Mode()&os.ModeSymlink != 0 || !targetRootInfo.IsDir() {
		warnings = appendRestoreVerificationWarning(warnings, "恢复目标根目录类型不匹配")
	} else if targetRootInfo.Mode().Perm() != sourceRootInfo.Mode().Perm() {
		warnings = appendRestoreVerificationWarning(warnings, "恢复目标根目录权限不匹配")
	}
	expectedDirs := make(map[string]struct{})
	err = filepath.WalkDir(sourceRootPath, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if sourcePath == sourceRootPath || !entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fmt.Errorf("stat backup snapshot directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		relPath, err := restoreWalkRelativePath(sourceRootPath, sourcePath, "backup snapshot data tree")
		if err != nil {
			return err
		}
		expectedDirs[relPath] = struct{}{}
		targetDirPath, err := safeJoin(targetPath, relPath)
		if err != nil {
			return err
		}
		targetInfo, err := os.Lstat(targetDirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标缺少对照备份目录: "+relPath)
				return nil
			}
			return fmt.Errorf("stat restore target directory: %w", err)
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
			warnings = appendRestoreVerificationWarning(warnings, "恢复目标目录类型不匹配: "+relPath)
			return nil
		}
		if targetInfo.Mode().Perm() != info.Mode().Perm() {
			warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("恢复目标目录权限不匹配: %s", relPath))
		}
		return nil
	})
	if err != nil {
		return warnings, fmt.Errorf("compare restored directories: %w", err)
	}
	err = filepath.WalkDir(targetPath, func(targetDirPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if targetDirPath == targetPath || !entry.IsDir() {
			return nil
		}
		relPath, err := restoreWalkRelativePath(targetPath, targetDirPath, "restore target directory tree")
		if err != nil {
			return err
		}
		if relPath == ".mnemonas-restore" || strings.HasPrefix(relPath, ".mnemonas-restore/") {
			return filepath.SkipDir
		}
		if _, ok := expectedDirs[relPath]; !ok {
			warnings = appendRestoreVerificationWarning(warnings, "恢复目标包含对照备份未登记的目录: "+relPath)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return warnings, fmt.Errorf("compare restored directories: %w", err)
	}
	return warnings, nil
}

func appendRestoreFileComparisonWarnings(ctx context.Context, targetPath string, manifest Manifest, warnings []string) ([]string, error) {
	expectedFiles := make(map[string]ManifestEntry, len(manifest.Entries))
	var configEntry *ManifestEntry
	for _, entry := range manifest.Entries {
		archivePath := filepath.ToSlash(entry.ArchivePath)
		if relPath, ok := strings.CutPrefix(archivePath, "data/"); ok {
			expectedFiles[relPath] = entry
			continue
		}
		if archivePath == "config/config.toml" {
			entryCopy := entry
			configEntry = &entryCopy
		}
	}
	for relPath, entry := range expectedFiles {
		if err := ctx.Err(); err != nil {
			return warnings, err
		}
		targetFilePath, err := safeJoin(targetPath, relPath)
		if err != nil {
			return warnings, err
		}
		size, digest, mode, err := hashFile(ctx, targetFilePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标缺少对照备份文件: "+relPath)
				continue
			}
			warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("恢复目标文件校验失败: %s", relPath))
			continue
		}
		if size != entry.Size || digest != entry.SHA256 || mode != manifestEntryMode(entry) {
			warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("恢复目标文件校验失败: %s", relPath))
		}
	}
	warnings = appendRestoreConfigComparisonWarnings(ctx, targetPath, configEntry, warnings)

	err := filepath.WalkDir(targetPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == targetPath || entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("stat restore target entry: %w", err)
		}
		relPath, err := restoreWalkRelativePath(targetPath, filePath, "restore target file tree")
		if err != nil {
			return err
		}
		if relPath == ".mnemonas-restore/config.toml" {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			warnings = appendRestoreVerificationWarning(warnings, "恢复目标包含符号链接: "+relPath)
			return nil
		}
		if !info.Mode().IsRegular() {
			warnings = appendRestoreVerificationWarning(warnings, "恢复目标包含不支持的文件类型: "+relPath)
			return nil
		}
		if _, ok := expectedFiles[relPath]; !ok {
			warnings = appendRestoreVerificationWarning(warnings, "恢复目标包含对照备份未登记的文件: "+relPath)
		}
		return nil
	})
	if err != nil {
		return warnings, fmt.Errorf("compare restored files: %w", err)
	}
	return warnings, nil
}

func appendRestoreConfigComparisonWarnings(ctx context.Context, targetPath string, configEntry *ManifestEntry, warnings []string) []string {
	configPath := filepath.Join(targetPath, ".mnemonas-restore", "config.toml")
	exists, err := regularFileExistsNoFollow(configPath)
	if err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("恢复目标配置文件校验失败: %v", err))
	}
	if !exists {
		return warnings
	}
	if configEntry == nil {
		return appendRestoreVerificationWarning(warnings, "恢复目标包含对照备份未登记的配置文件: .mnemonas-restore/config.toml")
	}
	size, digest, mode, err := hashFile(ctx, configPath)
	if err != nil {
		return appendRestoreVerificationWarning(warnings, "恢复目标配置文件校验失败: .mnemonas-restore/config.toml")
	}
	if size != configEntry.Size || digest != configEntry.SHA256 || mode != manifestEntryMode(*configEntry) {
		return appendRestoreVerificationWarning(warnings, "恢复目标配置文件校验失败: .mnemonas-restore/config.toml")
	}
	return warnings
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
	if err := validatePathComponentsNoSymlink(filepath.Dir(path), "restore verification"); err != nil {
		return false, err
	}
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

func regularFileExistsStrictNoFollow(path string) (bool, error) {
	if err := validatePathComponentsNoSymlink(filepath.Dir(path), "restore verification"); err != nil {
		return false, err
	}
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
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	return true, nil
}

func dirExistsNoFollow(path string) (bool, error) {
	if err := validatePathComponentsNoSymlink(filepath.Dir(path), "restore verification"); err != nil {
		return false, err
	}
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
	if err := validatePathComponentsNoSymlink(targetPath, "restore target"); err != nil {
		return err
	}
	if err := validatePathComponentsNoSymlink(partialPath, "restore staging target"); err != nil {
		return err
	}
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat restore target before install: %w", err)
	}
	if err := rootio.ReplaceEmptyDirPathNoFollow(partialPath, targetPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrRestoreTargetExists
		}
		return fmt.Errorf("install restore target: %w", mapRestorePathError(err, "restore target changed before install"))
	}
	return nil
}

func removeAllBackupPath(targetPath, label string) error {
	if err := validatePathComponentsNoSymlink(targetPath, label); err != nil {
		return err
	}
	if err := rootio.RemoveAllPathNoFollow(targetPath); err != nil {
		if rootio.IsSymlinkError(err) {
			return fmt.Errorf("%w: %s path must not contain symlink", ErrUnsafePath, label)
		}
		return err
	}
	return nil
}

func mapRestorePathError(err error, message string) error {
	if rootio.IsSymlinkError(err) {
		return fmt.Errorf("%w: %s", ErrUnsafePath, message)
	}
	return err
}

func restoreManifestToTarget(ctx context.Context, snapshotPath, targetPath string, manifest Manifest, includeConfig bool) (int64, int64, bool, string, error) {
	if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
		return 0, 0, false, "", fmt.Errorf("verify backup snapshot: %w", err)
	}

	var fileCount int64
	var verifiedBytes int64
	var configRestored bool
	var configPath string
	directoryModes, err := restoreSnapshotDirectories(ctx, snapshotPath, "data", targetPath)
	if err != nil {
		return fileCount, verifiedBytes, configRestored, configPath, err
	}
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
		if err := validateRestoreManifestFileEntry(archivePath, entry.Size); err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, err
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
		size, digest, mode, err := hashFile(ctx, destinationPath)
		if err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("verify restored %s: %w", archivePath, err)
		}
		if size != entry.Size {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("size mismatch for restored %s: got %d, want %d", archivePath, size, entry.Size)
		}
		if digest != entry.SHA256 {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("checksum mismatch for restored %s", archivePath)
		}
		if mode != manifestEntryMode(entry) {
			return fileCount, verifiedBytes, configRestored, configPath, fmt.Errorf("mode mismatch for restored %s: got %04o, want %04o", archivePath, mode, manifestEntryMode(entry))
		}
		nextVerifiedBytes, err := addManifestEntrySize(verifiedBytes, size)
		if err != nil {
			return fileCount, verifiedBytes, configRestored, configPath, err
		}
		fileCount++
		verifiedBytes = nextVerifiedBytes
		if !isData {
			configRestored = true
			configPath = destinationPath
		}
	}
	if err := applyDirectoryModesNoFollow(targetPath, directoryModes, "restore target"); err != nil {
		return fileCount, verifiedBytes, configRestored, configPath, err
	}
	return fileCount, verifiedBytes, configRestored, configPath, nil
}

func restoreSnapshotDirectories(ctx context.Context, snapshotPath string, archiveRoot string, targetRoot string) ([]directoryMode, error) {
	sourceRootPath, err := safeJoin(snapshotPath, archiveRoot)
	if err != nil {
		return nil, err
	}
	sourceRootInfo, err := os.Lstat(sourceRootPath)
	if err != nil {
		return nil, fmt.Errorf("stat backup snapshot directory root: %w", mapBackupNoFollowError(err, "backup snapshot"))
	}
	if sourceRootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: snapshot directory root changed to a symlink: %s", ErrUnsafePath, archiveRoot)
	}
	if !sourceRootInfo.IsDir() {
		return nil, fmt.Errorf("%w: snapshot directory root is not a directory: %s", ErrUnsafePath, archiveRoot)
	}
	if err := rootio.MkdirAllPathNoFollow(targetRoot, writableDirectoryMode(sourceRootInfo.Mode().Perm())); err != nil {
		return nil, fmt.Errorf("create restore directory root: %w", mapRestorePathError(err, "restore target path must not contain symlink"))
	}
	directoryModes := []directoryMode{{RelPath: ".", Mode: sourceRootInfo.Mode().Perm()}}
	if err := restoreSnapshotDirectoryEntry(ctx, sourceRootPath, ".", targetRoot, &directoryModes); err != nil {
		return nil, err
	}
	return directoryModes, nil
}

func restoreSnapshotDirectoryEntry(ctx context.Context, sourceRootPath string, relPath string, targetRoot string, directoryModes *[]directoryMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourcePath := sourceRootPath
	if relPath != "." {
		sourcePath = filepath.Join(sourceRootPath, relPath)
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat backup snapshot directory %s: %w", relPath, mapBackupNoFollowError(err, "backup snapshot"))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: snapshot directory changed to a symlink: %s", ErrUnsafePath, relPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: snapshot directory root is not a directory: %s", ErrUnsafePath, relPath)
	}

	dir, err := rootio.OpenDirPathNoFollow(sourcePath)
	if err != nil {
		return fmt.Errorf("open backup snapshot directory %s: %w", relPath, mapBackupNoFollowError(err, "backup snapshot"))
	}
	children, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil {
		return fmt.Errorf("read backup snapshot directory %s: %w", relPath, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close backup snapshot directory %s: %w", relPath, closeErr)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	for _, child := range children {
		childPath, err := childRelPath(relPath, child.Name())
		if err != nil {
			return fmt.Errorf("%w: backup snapshot contains unsafe entry name %q", err, cleanPreviewSamplePath(child.Name()))
		}
		childSourcePath := filepath.Join(sourceRootPath, childPath)
		childInfo, err := os.Lstat(childSourcePath)
		if err != nil {
			return fmt.Errorf("stat backup snapshot entry %s: %w", childPath, mapBackupNoFollowError(err, "backup snapshot"))
		}
		if childInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: snapshot directory entry changed to a symlink: %s", ErrUnsafePath, childPath)
		}
		if childInfo.IsDir() {
			destinationPath := filepath.Join(targetRoot, childPath)
			if err := rootio.MkdirAllPathNoFollow(destinationPath, writableDirectoryMode(childInfo.Mode().Perm())); err != nil {
				return fmt.Errorf("create restored directory %s: %w", childPath, mapRestorePathError(err, "restore target path must not contain symlink"))
			}
			*directoryModes = append(*directoryModes, directoryMode{RelPath: childPath, Mode: childInfo.Mode().Perm()})
			if err := restoreSnapshotDirectoryEntry(ctx, sourceRootPath, childPath, targetRoot, directoryModes); err != nil {
				return err
			}
			continue
		}
		if childInfo.Mode().IsRegular() {
			continue
		}
		return fmt.Errorf("%w: snapshot directory entry has unsupported file type: %s", ErrUnsupportedFileType, childPath)
	}
	return nil
}

type directoryMode struct {
	RelPath string
	Mode    os.FileMode
}

func writableDirectoryMode(mode os.FileMode) os.FileMode {
	return mode.Perm() | 0700
}

func applyDirectoryModesNoFollow(root string, directoryModes []directoryMode, label string) error {
	sort.SliceStable(directoryModes, func(i, j int) bool {
		leftDepth := directoryModeDepth(directoryModes[i].RelPath)
		rightDepth := directoryModeDepth(directoryModes[j].RelPath)
		return leftDepth > rightDepth
	})
	for _, directoryMode := range directoryModes {
		dirPath := filepath.Join(root, directoryMode.RelPath)
		if err := chmodDirectoryPathNoFollow(dirPath, directoryMode.Mode, label); err != nil {
			return fmt.Errorf("set directory mode %s: %w", directoryMode.RelPath, err)
		}
	}
	return nil
}

func directoryModeDepth(relPath string) int {
	cleaned := filepath.ToSlash(filepath.Clean(relPath))
	if cleaned == "." || cleaned == "" {
		return 0
	}
	return strings.Count(cleaned, "/") + 1
}

func chmodDirectoryPathNoFollow(dirPath string, mode os.FileMode, label string) error {
	dir, err := rootio.OpenDirPathNoFollow(dirPath)
	if err != nil {
		return mapBackupNoFollowError(err, label)
	}
	if err := dir.Chmod(mode.Perm()); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
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

func childRelPath(parent, name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(normalized, "/") || strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", ErrUnsafePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", ErrUnsafePath
		}
	}
	if parent == "." {
		return name, nil
	}
	return filepath.Join(parent, name), nil
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
	if archivePath == "" || filepath.IsAbs(archivePath) || strings.Contains(archivePath, "\\") || strings.IndexFunc(archivePath, unicode.IsControl) >= 0 {
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

func backupManifestFileExistsNoFollow(filePath string) (bool, error) {
	if err := validatePathComponentsNoSymlink(filePath, "backup manifest"); err != nil {
		return false, err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat backup manifest: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: backup manifest path must not contain symlink", ErrUnsafePath)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: backup manifest must be a regular file", ErrUnsafePath)
	}
	return true, nil
}

func parseRunID(runID string) (time.Time, error) {
	parsed, err := time.Parse(runIDTimeLayout, runID)
	if err != nil {
		return time.Time{}, err
	}
	if formatRunID(parsed) != runID {
		return time.Time{}, fmt.Errorf("non-canonical run id")
	}
	return parsed.UTC(), nil
}

func formatRunID(t time.Time) string {
	return t.UTC().Format(runIDTimeLayout)
}
