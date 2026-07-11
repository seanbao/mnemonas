// Package backup provides local backup jobs, scheduling, retention, and restore drills.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
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

	stateFileName                   = "status.json"
	manifestFileName                = "manifest.json"
	manifestVersion                 = 2
	jobConfigEvidenceBindingVersion = 2
	runIDTimeLayout                 = "20060102T150405.000000000Z"
	restoreHistoryLimit             = 20
	restoreDrillHistoryLimit        = 20
	restorePreviewLimit             = 10
	restoreVerifyWarningLimit       = 8
	defaultRestoreDrillStaleAfter   = 30 * 24 * time.Hour
	restoreDrillReminderCooldown    = 24 * time.Hour
	interruptedStatusMessage        = "任务在服务重启或进程退出前中断"
	restoreDrillCleanupWarning      = "恢复演练已完成，但临时恢复目录清理失败；请检查备份目标中的 restore-drills 目录。"

	defaultSchedulerPollInterval = time.Minute
	defaultNotificationTimeout   = 10 * time.Second
	externalCommandStderrLimit   = 4096
	externalCommandStdoutLimit   = 4 * 1024 * 1024
	credentialEvidenceSizeLimit  = 4 * 1024 * 1024
	credentialSnapshotDirPrefix  = "mnemonas-backup-credential-"
	maxBackupJSONTempAttempts    = 32
	restoreDrillStateWarning     = "恢复演练结果已生成，但状态目录同步失败"
	restoreVerifyStateWarning    = "恢复校验结果已生成，但状态目录同步失败"
	restoreStateWarning          = "恢复结果已生成，但状态目录同步失败"
	retentionCheckStateWarning   = "保留策略检查结果已生成，但状态目录同步失败"
	stateNamespaceChangedWarning = "备份状态目录身份发生变化；当前结果已生成，但备份服务已停止后续操作。请检查状态目录并重启服务"
	specialPermissionBits        = os.ModeSetuid | os.ModeSetgid | os.ModeSticky

	redactedBackupSecretValue        = "<redacted>"
	backupSensitiveNamePartPattern   = `(?:[A-Za-z0-9_.-]|%[0-9A-Fa-f]{2})*`
	backupSensitiveKeySeparator      = `(?:[_-]|%5f|%2d)?`
	backupSensitiveWordSeparator     = `(?:[_-]|%5f|%2d)`
	backupSensitiveNamePattern       = `(?:` + backupSensitiveNamePartPattern + `(?:password|passwd|secret|token|credential|access` + backupSensitiveKeySeparator + `key|secret` + backupSensitiveKeySeparator + `key|account` + backupSensitiveKeySeparator + `key|api` + backupSensitiveKeySeparator + `key|authorization|signature|` + backupSensitiveWordSeparator + `pass)` + backupSensitiveNamePartPattern + `|pass|auth|sig|user|username)`
	backupSensitiveHeaderNamePattern = `(?:` + backupSensitiveNamePartPattern + `(?:password|passwd|secret|token|credential|access` + backupSensitiveKeySeparator + `key|secret` + backupSensitiveKeySeparator + `key|account` + backupSensitiveKeySeparator + `key|api` + backupSensitiveKeySeparator + `key|signature|` + backupSensitiveWordSeparator + `pass)` + backupSensitiveNamePartPattern + `|pass|sig|user|username)`
)

var restoreAvailableBytesFunc = restoreAvailableBytes
var afterCopyHostFileLstat = func(string) {}
var afterCopyOpenFileBeforeMetadata = func(string) {}
var afterValidateLocalBackupDestination = func(string) {}
var afterFinalizeLocalBackupSnapshot = func(string) {}
var syncBackupJSONDir = syncBackupJSONDirectoryHandle
var backupJSONRandomRead = rand.Read
var writeBackupStateFile = writeBackupJSONFile
var renameBackupJSONFile = func(root *os.Root, oldName, newName string) error {
	return root.Rename(oldName, newName)
}
var afterRenameBackupJSONFile = func(string) {}
var syncLocalBackupSnapshotTree = syncOpenedBackupSnapshotDirectoryTree
var renameLocalBackupSnapshot = renameBackupSnapshotNoReplace
var syncLocalBackupDirectory = syncBackupDirectory
var syncLocalBackupDirectoryHandle = syncBackupDirectoryHandle
var removeLocalBackupSnapshotEntry = removeLocalSnapshotEntryNoFollowChecked

var (
	ErrJobNotFound                 = errors.New("backup job not found")
	ErrJobAlreadyExists            = errors.New("backup job already exists")
	ErrJobAlreadyRunning           = errors.New("backup job already running")
	ErrManagerClosed               = errors.New("backup manager is closed")
	ErrBackupStatePersistence      = errors.New("backup state persistence failed")
	ErrBackupStateNamespaceChanged = errors.New("backup state namespace changed; restart the backup manager")
	ErrJobDisabled                 = errors.New("backup job disabled")
	ErrNoSnapshots                 = errors.New("backup job has no completed snapshots")
	ErrUnsupportedJobType          = errors.New("unsupported backup job type")
	ErrUnsafePath                  = errors.New("unsafe backup path")
	ErrInvalidRestoreRequest       = errors.New("invalid restore request")
	ErrRestoreTargetExists         = errors.New("restore target already exists")
	ErrSourceContainsSymlink       = errors.New("backup source contains a symlink")
	ErrUnsupportedFileType         = errors.New("backup source contains an unsupported file type")

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

type backupPersistenceWarningError struct {
	err error
}

type backupStatePersistenceError struct {
	err error
}

func (e *backupStatePersistenceError) Error() string {
	return ErrBackupStatePersistence.Error() + ": " + e.err.Error()
}

func (e *backupStatePersistenceError) Unwrap() []error {
	return []error{ErrBackupStatePersistence, e.err}
}

func (e *backupPersistenceWarningError) Error() string {
	return e.err.Error()
}

func (e *backupPersistenceWarningError) Unwrap() error {
	return e.err
}

func wrapBackupPersistenceWarning(err error) error {
	if err == nil {
		return nil
	}
	var warning *backupPersistenceWarningError
	if errors.As(err, &warning) {
		return err
	}
	return &backupPersistenceWarningError{err: err}
}

func isBackupPersistenceWarning(err error) bool {
	var warning *backupPersistenceWarningError
	return errors.As(err, &warning)
}

func backupStateWarningMessage(err error, fallback string) string {
	if errors.Is(err, ErrBackupStateNamespaceChanged) {
		return stateNamespaceChangedWarning
	}
	return fallback
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
	stateLock   *backupStateLock

	mu                      sync.Mutex
	running                 map[string]struct{}
	state                   persistedState
	now                     func() time.Time
	statePersistenceHealthy bool
	stateNamespaceUnsafe    bool
	stateNamespaceErr       error
	readinessGate           chan struct{}
	readinessManifestCache  sync.Map
	reminderMu              sync.Mutex

	schedulerStarted bool
	schedulerPoll    time.Duration
	schedulerCancel  context.CancelFunc
	schedulerDone    chan struct{}
	operations       sync.WaitGroup
	closed           bool
	closeOnce        sync.Once
	closeErr         error
	shutdownCtx      context.Context
	shutdownCancel   context.CancelFunc
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
	ID               string     `json:"id"`
	JobID            string     `json:"job_id"`
	JobConfigBinding string     `json:"job_config_binding,omitempty"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMs       int64      `json:"duration_ms"`
	Source           string     `json:"source"`
	Destination      string     `json:"destination"`
	SnapshotPath     string     `json:"snapshot_path,omitempty"`
	ManifestPath     string     `json:"manifest_path,omitempty"`
	ManifestSize     int64      `json:"manifest_size,omitempty"`
	ManifestDigest   string     `json:"manifest_digest,omitempty"`
	FileCount        int64      `json:"file_count"`
	TotalBytes       int64      `json:"total_bytes"`
	ConfigIncluded   bool       `json:"config_included"`
	Trigger          string     `json:"trigger,omitempty"`
	Warning          bool       `json:"warning,omitempty"`
	Warnings         []string   `json:"warnings,omitempty"`
	PrunedSnapshots  int        `json:"pruned_snapshots,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}

// RestoreDrillOptions controls restore drill behavior.
type RestoreDrillOptions struct {
	KeepArtifact bool `json:"keep_artifact"`
}

// RestoreDrillResult records one non-destructive restore verification.
type RestoreDrillResult struct {
	ID               string     `json:"id"`
	JobID            string     `json:"job_id"`
	JobConfigBinding string     `json:"job_config_binding,omitempty"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMs       int64      `json:"duration_ms"`
	SnapshotPath     string     `json:"snapshot_path,omitempty"`
	ManifestPath     string     `json:"manifest_path,omitempty"`
	RestoredPath     string     `json:"restored_path,omitempty"`
	ArtifactKept     bool       `json:"artifact_kept"`
	FileCount        int64      `json:"file_count"`
	VerifiedBytes    int64      `json:"verified_bytes"`
	Warning          bool       `json:"warning,omitempty"`
	Warnings         []string   `json:"warnings,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	FailureCategory  string     `json:"failure_category,omitempty"`
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
	JobConfigBinding     string     `json:"job_config_binding,omitempty"`
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
	ID               string     `json:"id"`
	JobID            string     `json:"job_id"`
	JobConfigBinding string     `json:"job_config_binding,omitempty"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMs       int64      `json:"duration_ms"`
	SnapshotPath     string     `json:"snapshot_path,omitempty"`
	ManifestPath     string     `json:"manifest_path,omitempty"`
	// ManifestSize and ManifestDigest are persisted evidence and are removed from public clones.
	ManifestSize      int64                   `json:"manifest_size,omitempty"`
	ManifestDigest    string                  `json:"manifest_digest,omitempty"`
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
	Version     int                 `json:"version"`
	JobID       string              `json:"job_id"`
	RunID       string              `json:"run_id"`
	Source      string              `json:"source"`
	CreatedAt   time.Time           `json:"created_at"`
	FileCount   int64               `json:"file_count"`
	TotalBytes  int64               `json:"total_bytes"`
	Entries     []ManifestEntry     `json:"entries"`
	Directories []ManifestDirectory `json:"directories"`
	ConfigPath  string              `json:"config_path,omitempty"`
	Description string              `json:"description,omitempty"`
}

// ManifestEntry is one file stored in a local snapshot.
type ManifestEntry struct {
	ArchivePath string `json:"archive_path"`
	SourcePath  string `json:"source_path"`
	Size        int64  `json:"size"`
	Mode        uint32 `json:"mode"`
	SHA256      string `json:"sha256"`
}

// ManifestDirectory is one directory in the restorable data tree.
type ManifestDirectory struct {
	ArchivePath string `json:"archive_path"`
	Mode        uint32 `json:"mode"`
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
	stateLock, err := acquireBackupStateLock(root)
	if err != nil {
		return nil, err
	}
	keepStateLock := false
	defer func() {
		if !keepStateLock {
			_ = stateLock.Close()
		}
	}()

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	m := &Manager{
		root:           root,
		storageRoot:    cfg.StorageRoot,
		configPath:     cfg.ConfigPath,
		jobs:           make(map[string]config.BackupJobConfig, len(cfg.Jobs)),
		notifier:       cfg.Notifier,
		stateLock:      stateLock,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
		running:        map[string]struct{}{},
		state: persistedState{
			Jobs: map[string]JobState{},
		},
		now:                     time.Now,
		statePersistenceHealthy: true,
		readinessGate:           make(chan struct{}, 1),
		schedulerPoll:           cfg.SchedulerPollInterval,
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
	if err := m.verifyStateNamespaceLocked(); err != nil {
		return nil, m.quarantineStateNamespaceLocked(err, err)
	}
	keepStateLock = true
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
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(root, 0700)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return fmt.Errorf("%w: backup state root must not contain symlink", ErrUnsafePath)
		}
		return err
	}
	if err := syncCreatedBackupDirectories(createdDirs, syncBackupDirectory); err != nil {
		return fmt.Errorf("sync backup state directory tree: %w", err)
	}
	return nil
}

// ListJobs returns all configured backup jobs with latest status.
// Available reports whether the manager can safely accept new operations.
func (m *Manager) Available() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if err := m.verifyStateNamespaceLocked(); err != nil {
		_ = m.quarantineStateNamespaceLocked(err, err)
		return false
	}
	return true
}

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

// ValidateNewJob verifies that a job can be added without mutating manager state.
func (m *Manager) ValidateNewJob(job config.BackupJobConfig) error {
	normalized, err := m.prepareNewJob(job)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}
	return m.validateNewJobIDLocked(normalized.ID)
}

// AddJob validates and atomically adds a job to the running manager.
func (m *Manager) AddJob(job config.BackupJobConfig) (JobView, error) {
	normalized, err := m.prepareNewJob(job)
	if err != nil {
		return JobView{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return JobView{}, ErrManagerClosed
	}
	if err := m.validateNewJobIDLocked(normalized.ID); err != nil {
		return JobView{}, err
	}
	m.jobs[normalized.ID] = cloneJob(normalized)
	return m.jobViewLocked(normalized.ID, normalized), nil
}

func (m *Manager) prepareNewJob(job config.BackupJobConfig) (config.BackupJobConfig, error) {
	normalized := normalizeJob(job, m.storageRoot)
	if !isSafeManagerJobID(normalized.ID) {
		return config.BackupJobConfig{}, fmt.Errorf("%w: backup job id is unsafe: %q", ErrUnsafePath, normalized.ID)
	}
	if normalized.Type != JobTypeLocal {
		return config.BackupJobConfig{}, ErrUnsupportedJobType
	}
	source := effectiveSource(normalized, m.storageRoot)
	if err := validateSourceDirectory(source); err != nil {
		return config.BackupJobConfig{}, err
	}
	if err := validateDestination(source, normalized.Destination, m.storageRoot); err != nil {
		return config.BackupJobConfig{}, err
	}
	return normalized, nil
}

func (m *Manager) validateNewJobIDLocked(id string) error {
	for existingID := range m.jobs {
		if strings.EqualFold(existingID, id) {
			return ErrJobAlreadyExists
		}
	}
	return nil
}

// RunJob runs a configured backup job synchronously.
func (m *Manager) RunJob(ctx context.Context, id string) (*RunResult, error) {
	return m.runJobWithTrigger(ctx, id, "manual")
}

func (m *Manager) runJobWithTrigger(ctx context.Context, id string, trigger string) (returnedResult *RunResult, returnedErr error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	binding, bindingErr := jobConfigEvidenceBindingContext(ctx, job, m.storageRoot, m.configPath)
	result := &RunResult{
		ID:               formatRunID(startedAt),
		JobID:            job.ID,
		JobConfigBinding: binding,
		Status:           StatusRunning,
		StartedAt:        startedAt,
		Source:           effectiveSource(job, m.storageRoot),
		Destination:      backupTarget(job),
		ConfigIncluded:   job.IncludeConfig && strings.TrimSpace(m.configPath) != "",
		Trigger:          trigger,
	}
	var scheduledAt *time.Time
	if trigger == "scheduled" {
		scheduledAt = cloneTime(&startedAt)
	}
	if saveErr := m.updateLastRunWithScheduledAt(result, scheduledAt); saveErr != nil {
		finishBackupRun(result, StatusFailed, fmt.Errorf("persist running backup state: %w", saveErr), m.now().UTC())
		if isBackupPersistenceWarning(saveErr) {
			if terminalSaveErr := m.updateLastRun(result); terminalSaveErr != nil {
				if !isBackupPersistenceWarning(terminalSaveErr) {
					m.recordVolatileLastRun(result)
				}
				saveErr = errors.Join(saveErr, terminalSaveErr)
			}
		}
		return cloneRunResult(result), saveErr
	}

	targetLock, targetLockErr := m.acquireJobTargetLockForBackup(job)
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
	}
	err = targetLockErr
	if err == nil {
		err = bindingErr
	}
	if err == nil {
		err = m.runBackup(ctx, job, result)
	}
	if err == nil {
		err = m.ensureJobConfigEvidenceUnchanged(ctx, job, result.JobConfigBinding)
	}
	if err != nil {
		finishBackupRun(result, StatusFailed, err, m.now().UTC())
		if saveErr := m.updateLastRun(result); saveErr != nil {
			m.recordVolatileLastRun(result)
			m.notifyRun(ctx, job, result)
			return cloneRunResult(result), errors.Join(err, saveErr)
		}
		m.notifyRun(ctx, job, result)
		return cloneRunResult(result), err
	}

	finishBackupRun(result, StatusCompleted, nil, m.now().UTC())
	if saveErr := m.updateLastRun(result); saveErr != nil {
		if isBackupPersistenceWarning(saveErr) {
			result.Warning = true
			result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, "备份快照已完成，但备份状态目录同步失败；已保留旧快照并跳过清理"))
			if warningSaveErr := m.updateLastRun(result); warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
				m.recordVolatileLastRun(result)
			}
			m.notifyRun(ctx, job, result)
			return cloneRunResult(result), nil
		}
		commitErr := fmt.Errorf("persist completed backup state: %w", saveErr)
		finishBackupRun(result, StatusFailed, commitErr, m.now().UTC())
		if terminalSaveErr := m.updateLastRun(result); terminalSaveErr != nil {
			commitErr = errors.Join(commitErr, terminalSaveErr)
			m.recordVolatileLastRun(result)
		}
		m.notifyRun(ctx, job, result)
		return cloneRunResult(result), commitErr
	}
	committedResult := cloneRunResultRaw(result)

	if job.Type == JobTypeLocal {
		pruned, warnings := m.applyRetention(ctx, job, result.SnapshotPath)
		result.PrunedSnapshots = pruned
		if len(warnings) > 0 {
			result.Warning = true
			result.Warnings = append(result.Warnings, warnings...)
		}
	}
	retentionCheck, checkErr := m.runRetentionCheckForJob(ctx, job, false, false)
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
	finishBackupRun(result, StatusCompleted, nil, m.now().UTC())
	if saveErr := m.updateLastRun(result); saveErr != nil {
		result.Warning = true
		result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, "备份已完成，但保留策略结果未能可靠保存"))
		warningState := cloneRunResultRaw(committedResult)
		warningState.Warning = true
		warningState.Warnings = cloneStringSlice(result.Warnings)
		if warningSaveErr := m.updateLastRun(warningState); warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
			m.recordVolatileLastRun(result)
		}
		m.notifyRun(ctx, job, result)
		return cloneRunResult(result), nil
	}
	m.notifyRun(ctx, job, result)
	return cloneRunResult(result), nil
}

func finishBackupRun(result *RunResult, status string, runErr error, finishedAt time.Time) {
	result.Status = status
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	if result.DurationMs < 0 {
		result.DurationMs = 0
	}
	if runErr == nil {
		result.ErrorMessage = ""
		return
	}
	result.ErrorMessage = runErr.Error()
}

// RunRestoreDrill restores the latest completed snapshot to a temporary
// directory and verifies every file against the snapshot manifest.
func (m *Manager) RunRestoreDrill(ctx context.Context, id string, opts RestoreDrillOptions) (returnedResult *RestoreDrillResult, returnedErr error) {
	job, err := m.beginJob(id)
	if err != nil {
		return nil, err
	}
	defer m.endJob(id)

	startedAt := m.now().UTC()
	binding, bindingErr := jobConfigEvidenceBindingContext(ctx, job, m.storageRoot, m.configPath)
	result := &RestoreDrillResult{
		ID:               formatRunID(startedAt),
		JobID:            job.ID,
		JobConfigBinding: binding,
		Status:           StatusRunning,
		StartedAt:        startedAt,
		ArtifactKept:     opts.KeepArtifact,
	}
	if saveErr := m.updateLastRestoreDrill(result, false); saveErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = fmt.Sprintf("persist running restore drill state: %v", saveErr)
		result.FailureCategory = FailureCategoryIO
		if isBackupPersistenceWarning(saveErr) {
			if terminalErr := m.updateLastRestoreDrill(result, true); terminalErr != nil {
				if !isBackupPersistenceWarning(terminalErr) {
					m.recordVolatileLastRestoreDrill(result, true)
				}
				saveErr = errors.Join(saveErr, terminalErr)
			}
		}
		return cloneRestoreDrillResult(result), saveErr
	}
	targetLock, lockErr := m.acquireJobTargetLock(job)
	if lockErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = lockErr.Error()
		result.FailureCategory = classifyRestoreDrillFailure(lockErr)
		if saveErr := m.updateLastRestoreDrill(result, true); saveErr != nil {
			if isBackupPersistenceWarning(saveErr) {
				result.Warning = true
				result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreDrillStateWarning))
				warningSaveErr := m.updateLastRestoreDrill(result, true)
				if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
					m.recordVolatileLastRestoreDrill(result, true)
				}
				m.notifyRestoreDrill(ctx, job, result)
				return cloneRestoreDrillResult(result), errors.Join(lockErr, saveErr, warningSaveErr)
			}
			if !isBackupPersistenceWarning(saveErr) {
				m.recordVolatileLastRestoreDrill(result, true)
			}
			lockErr = errors.Join(lockErr, saveErr)
		}
		m.notifyRestoreDrill(ctx, job, result)
		return cloneRestoreDrillResult(result), lockErr
	}
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
	}

	err = bindingErr
	if err == nil {
		err = m.runRestoreDrill(ctx, job, opts, result)
	}
	if err == nil {
		err = m.ensureJobConfigEvidenceUnchanged(ctx, job, result.JobConfigBinding)
	}
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
		if isBackupPersistenceWarning(saveErr) {
			result.Warning = true
			result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreDrillStateWarning))
			warningSaveErr := m.updateLastRestoreDrill(result, true)
			if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
				m.recordVolatileLastRestoreDrill(result, true)
			}
			m.notifyRestoreDrill(ctx, job, result)
			if err != nil {
				return cloneRestoreDrillResult(result), errors.Join(err, saveErr, warningSaveErr)
			}
			return cloneRestoreDrillResult(result), nil
		}
		if !isBackupPersistenceWarning(saveErr) {
			m.recordVolatileLastRestoreDrill(result, true)
		}
		m.notifyRestoreDrill(ctx, job, result)
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
func (m *Manager) RunRestorePreview(ctx context.Context, id string, opts RestorePreviewOptions) (returnedResult *RestorePreviewResult, returnedErr error) {
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
	targetLock, lockErr := m.acquireJobTargetLock(job)
	if lockErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = lockErr.Error()
		return cloneRestorePreviewResult(result), lockErr
	}
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
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
func (m *Manager) RunRestoreVerify(ctx context.Context, id string, opts RestoreVerifyOptions) (returnedResult *RestoreVerifyResult, returnedErr error) {
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
	binding, bindingErr := jobConfigEvidenceBindingContext(ctx, job, m.storageRoot, m.configPath)
	result := &RestoreVerifyResult{
		ID:               formatRunID(startedAt),
		JobID:            job.ID,
		JobConfigBinding: binding,
		Status:           StatusRunning,
		StartedAt:        startedAt,
		Source:           effectiveSource(job, m.storageRoot),
		Destination:      backupTarget(job),
		TargetPath:       targetPath,
	}
	if saveErr := m.updateLastRestoreVerify(result); saveErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = fmt.Sprintf("persist running restore verification state: %v", saveErr)
		if isBackupPersistenceWarning(saveErr) {
			if terminalErr := m.updateLastRestoreVerify(result); terminalErr != nil {
				if !isBackupPersistenceWarning(terminalErr) {
					m.recordVolatileLastRestoreVerify(result)
				}
				saveErr = errors.Join(saveErr, terminalErr)
			}
		}
		return cloneRestoreVerifyResult(result), saveErr
	}
	targetLock, lockErr := m.acquireJobTargetLock(job)
	if lockErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = lockErr.Error()
		if saveErr := m.updateLastRestoreVerify(result); saveErr != nil {
			if isBackupPersistenceWarning(saveErr) {
				result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreVerifyStateWarning))
				warningSaveErr := m.updateLastRestoreVerify(result)
				if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
					m.recordVolatileLastRestoreVerify(result)
				}
				m.notifyRestoreVerify(ctx, job, result)
				return cloneRestoreVerifyResult(result), errors.Join(lockErr, saveErr, warningSaveErr)
			}
			if !isBackupPersistenceWarning(saveErr) {
				m.recordVolatileLastRestoreVerify(result)
			}
			lockErr = errors.Join(lockErr, saveErr)
		}
		m.notifyRestoreVerify(ctx, job, result)
		return cloneRestoreVerifyResult(result), lockErr
	}
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
	}

	err = bindingErr
	if err == nil {
		err = m.runRestoreVerify(ctx, job, opts, result)
	}
	if err == nil {
		err = m.ensureJobConfigEvidenceUnchanged(ctx, job, result.JobConfigBinding)
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
	if saveErr := m.updateLastRestoreVerify(result); saveErr != nil {
		if isBackupPersistenceWarning(saveErr) {
			result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreVerifyStateWarning))
			warningSaveErr := m.updateLastRestoreVerify(result)
			if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
				m.recordVolatileLastRestoreVerify(result)
			}
			m.notifyRestoreVerify(ctx, job, result)
			if err != nil {
				return cloneRestoreVerifyResult(result), errors.Join(err, saveErr, warningSaveErr)
			}
			return cloneRestoreVerifyResult(result), nil
		}
		if !isBackupPersistenceWarning(saveErr) {
			m.recordVolatileLastRestoreVerify(result)
		}
		m.notifyRestoreVerify(ctx, job, result)
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

	return m.runRetentionCheckForJob(ctx, job, true, true)
}

// RunRestore restores a completed backup into a caller-chosen target directory.
// The target is created atomically from a partial sibling and must be outside
// the live source, storage root, and any local backup destination.
func (m *Manager) RunRestore(ctx context.Context, id string, opts RestoreOptions) (returnedResult *RestoreResult, returnedErr error) {
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
	binding, bindingErr := jobConfigEvidenceBindingContext(ctx, job, m.storageRoot, m.configPath)
	result := &RestoreResult{
		ID:               formatRunID(startedAt),
		JobID:            job.ID,
		JobConfigBinding: binding,
		Status:           StatusRunning,
		StartedAt:        startedAt,
		TargetPath:       targetPath,
	}
	if saveErr := m.updateLastRestore(result, false); saveErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = fmt.Sprintf("persist running restore state: %v", saveErr)
		if isBackupPersistenceWarning(saveErr) {
			if terminalErr := m.updateLastRestore(result, true); terminalErr != nil {
				if !isBackupPersistenceWarning(terminalErr) {
					m.recordVolatileLastRestore(result, true)
				}
				saveErr = errors.Join(saveErr, terminalErr)
			}
		}
		return cloneRestoreResult(result), saveErr
	}
	targetLock, lockErr := m.acquireJobTargetLock(job)
	if lockErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = lockErr.Error()
		if saveErr := m.updateLastRestore(result, true); saveErr != nil {
			if isBackupPersistenceWarning(saveErr) {
				result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreStateWarning))
				warningSaveErr := m.updateLastRestore(result, true)
				if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
					m.recordVolatileLastRestore(result, true)
				}
				m.notifyRestore(ctx, job, result)
				return cloneRestoreResult(result), errors.Join(lockErr, saveErr, warningSaveErr)
			}
			if !isBackupPersistenceWarning(saveErr) {
				m.recordVolatileLastRestore(result, true)
			}
			lockErr = errors.Join(lockErr, saveErr)
		}
		m.notifyRestore(ctx, job, result)
		return cloneRestoreResult(result), lockErr
	}
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
	}

	preflightErr := bindingErr
	if preflightErr == nil {
		preflightErr = m.runRestorePreflight(ctx, job, opts, result)
	}
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
			if isBackupPersistenceWarning(saveErr) {
				result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreStateWarning))
				warningSaveErr := m.updateLastRestore(result, true)
				if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
					m.recordVolatileLastRestore(result, true)
				}
				m.notifyRestore(ctx, job, result)
				return cloneRestoreResult(result), errors.Join(preflightErr, saveErr, warningSaveErr)
			}
			if !isBackupPersistenceWarning(saveErr) {
				m.recordVolatileLastRestore(result, true)
			}
			m.notifyRestore(ctx, job, result)
			return cloneRestoreResult(result), errors.Join(preflightErr, saveErr)
		}
		m.notifyRestore(ctx, job, result)
		return cloneRestoreResult(result), preflightErr
	}

	err = m.runRestore(ctx, job, opts, result)
	if err == nil {
		err = m.ensureJobConfigEvidenceUnchanged(ctx, job, result.JobConfigBinding)
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
	if saveErr := m.updateLastRestore(result, true); saveErr != nil {
		if isBackupPersistenceWarning(saveErr) {
			result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, restoreStateWarning))
			warningSaveErr := m.updateLastRestore(result, true)
			if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
				m.recordVolatileLastRestore(result, true)
			}
			m.notifyRestore(ctx, job, result)
			if err != nil {
				return cloneRestoreResult(result), errors.Join(err, saveErr, warningSaveErr)
			}
			return cloneRestoreResult(result), nil
		}
		if !isBackupPersistenceWarning(saveErr) {
			m.recordVolatileLastRestore(result, true)
		}
		m.notifyRestore(ctx, job, result)
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

	notificationCtx, cancel := m.notificationContext(ctx)
	defer cancel()
	_ = m.notifier.NotifyBackupEvent(notificationCtx, NotificationEvent{
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

	notificationCtx, cancel := m.notificationContext(ctx)
	defer cancel()
	_ = m.notifier.NotifyBackupEvent(notificationCtx, NotificationEvent{
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

	notificationCtx, cancel := m.notificationContext(ctx)
	defer cancel()
	_ = m.notifier.NotifyBackupEvent(notificationCtx, NotificationEvent{
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

	notificationCtx, cancel := m.notificationContext(ctx)
	defer cancel()
	_ = m.notifier.NotifyBackupEvent(notificationCtx, NotificationEvent{
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

	notificationCtx, cancel := m.notificationContext(ctx)
	defer cancel()
	_ = m.notifier.NotifyBackupEvent(notificationCtx, NotificationEvent{
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
func (m *Manager) SendRestoreDrillReminders(ctx context.Context) ([]NotificationEvent, error) {
	if m.notifier == nil {
		return nil, nil
	}
	if !m.beginManagerOperation() {
		return nil, ErrManagerClosed
	}
	defer m.endManagerOperation()
	m.reminderMu.Lock()
	defer m.reminderMu.Unlock()

	now := m.now().UTC()
	events, err := m.restoreDrillReminderEvents(now)
	if err != nil {
		return nil, err
	}
	sent := make([]NotificationEvent, 0, len(events))
	var reminderErrs []error
	for _, event := range events {
		if ctx != nil && ctx.Err() != nil {
			reminderErrs = append(reminderErrs, ctx.Err())
			break
		}
		notificationCtx, cancel := m.notificationContext(ctx)
		if err := m.notifier.NotifyBackupEvent(notificationCtx, event); err != nil {
			reminderErrs = append(reminderErrs, fmt.Errorf("send restore drill reminder notification: %s", sanitizeBackupMessageForAPI(err.Error())))
			cancel()
			continue
		}
		cancel()
		sent = append(sent, event)
		if err := m.recordRestoreDrillReminder(event.JobID, now); err != nil {
			reminderErrs = append(reminderErrs, err)
			if errors.Is(err, ErrManagerClosed) || errors.Is(err, ErrBackupStateNamespaceChanged) {
				break
			}
		}
	}
	return sent, errors.Join(reminderErrs...)
}

func (m *Manager) restoreDrillReminderEvents(now time.Time) ([]NotificationEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateNamespaceUnsafe {
		return nil, m.stateNamespaceErrorLocked()
	}
	if m.closed {
		return nil, ErrManagerClosed
	}

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
		events = append(events, event)
	}
	return events, nil
}

func (m *Manager) recordRestoreDrillReminder(jobID string, remindedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateNamespaceUnsafe {
		return m.stateNamespaceErrorLocked()
	}
	if m.closed {
		return ErrManagerClosed
	}
	if _, ok := m.jobs[jobID]; !ok {
		return ErrJobNotFound
	}
	candidate := clonePersistedState(m.state)
	state := candidate.Jobs[jobID]
	remindedAt = remindedAt.UTC()
	state.LastRestoreDrillReminderAt = &remindedAt
	candidate.Jobs[jobID] = state
	return m.persistStateCandidateLocked(candidate)
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

	if m.stateNamespaceUnsafe {
		return config.BackupJobConfig{}, m.stateNamespaceErrorLocked()
	}
	if m.closed {
		return config.BackupJobConfig{}, ErrManagerClosed
	}
	if err := m.verifyStateNamespaceLocked(); err != nil {
		return config.BackupJobConfig{}, m.quarantineStateNamespaceLocked(err, err)
	}
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
	m.operations.Add(1)
	return cloneJob(job), nil
}

func (m *Manager) endJob(id string) {
	m.mu.Lock()
	delete(m.running, id)
	m.mu.Unlock()
	m.operations.Done()
}

func (m *Manager) jobViewLocked(id string, job config.BackupJobConfig) JobView {
	state := m.state.Jobs[id]
	_, running := m.running[id]
	nextRunAt := m.nextRunAtLocked(job, state, m.now())
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
	rawMatchingRestoreVerify := matchingRestoreVerifyForRestore(state.LastRestore, state.LastRestoreVerify)
	rawRestoreVerifyMismatchFinding := restoreVerifyMismatchFinding(state.LastRestore, state.LastRestoreVerify)
	matchingRestoreVerify := cloneRestoreVerifyResult(rawMatchingRestoreVerify)
	view.LastMatchingRestoreVerify = matchingRestoreVerify
	view.RestoreReportFindings = restoreReportFindingsWithMatchingVerifyAndMismatch(view, matchingRestoreVerify, rawRestoreVerifyMismatchFinding)
	return view
}

func (m *Manager) runBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) error {
	switch job.Type {
	case JobTypeLocal:
		return m.runLocalBackup(ctx, job, result)
	case JobTypeRestic:
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runResticBackup(commandCtx, executionJob, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	case JobTypeRclone:
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runRcloneBackup(commandCtx, executionJob, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) ensureJobConfigEvidenceUnchanged(ctx context.Context, job config.BackupJobConfig, expected string) error {
	current, err := jobConfigEvidenceBindingContext(ctx, job, m.storageRoot, m.configPath)
	if err != nil {
		return fmt.Errorf("recheck backup job evidence: %w", err)
	}
	if expected == "" || current != expected {
		return fmt.Errorf("%w: backup job inputs changed while the operation was running", ErrUnsafePath)
	}
	return nil
}

func (m *Manager) runLocalBackup(ctx context.Context, job config.BackupJobConfig, result *RunResult) (runErr error) {
	if err := requireLocalManifestModeSemantics(); err != nil {
		return err
	}
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
	createdSnapshotDirs, err := rootio.MkdirAllPathNoFollowTracked(snapshotRoot, 0700)
	if err != nil {
		return fmt.Errorf("create snapshot directory: %w", mapBackupNoFollowError(err, "destination"))
	}
	if err := syncCreatedBackupDirectories(createdSnapshotDirs, syncLocalBackupDirectory); err != nil {
		return fmt.Errorf("sync snapshot directory tree: %w", mapBackupNoFollowError(err, "destination"))
	}
	snapshotRootHandle, snapshotRootDir, snapshotRootInfo, err := openLocalSnapshotRoot(snapshotRoot)
	if err != nil {
		return fmt.Errorf("open snapshot directory: %w", err)
	}
	defer snapshotRootHandle.Close()
	defer snapshotRootDir.Close()

	partialName := filepath.Base(partialPath)
	finalName := filepath.Base(finalPath)
	if err := rootio.MkdirNoFollow(snapshotRootHandle, partialName, 0700); err != nil {
		return fmt.Errorf("create partial snapshot: %w", mapBackupNoFollowError(err, "destination"))
	}
	partialDir, err := rootio.OpenDirNoFollow(snapshotRootHandle, partialName)
	if err != nil {
		return fmt.Errorf("open partial snapshot: %w", mapBackupNoFollowError(err, "destination"))
	}
	defer partialDir.Close()
	partialInfo, err := partialDir.Stat()
	if err != nil {
		return fmt.Errorf("inspect partial snapshot: %w", err)
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			cleanupErr := removeLocalBackupSnapshotEntry(snapshotRootDir, snapshotRoot, partialName, partialInfo)
			if cleanupErr == nil {
				cleanupErr = syncLocalBackupDirectoryHandle(snapshotRootDir)
			}
			if cleanupErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("clean partial snapshot: %w", cleanupErr))
			}
		}
	}()

	dataPath := filepath.Join(partialPath, "data")
	entries, directories, totalBytes, err := copySourceTree(ctx, source, dataPath, job.Exclude)
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
	sort.Slice(directories, func(i, j int) bool {
		return directories[i].ArchivePath < directories[j].ArchivePath
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
		Directories: directories,
		ConfigPath:  includedConfigPath(job.IncludeConfig, m.configPath),
		Description: "MnemoNAS local directory snapshot",
	}
	result.SnapshotPath = finalPath
	result.ManifestPath = filepath.Join(finalPath, manifestFileName)
	result.FileCount = manifest.FileCount
	result.TotalBytes = manifest.TotalBytes
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup manifest: %w", err)
	}
	trustedDigest, err := manifestEvidenceDigestBytes(manifestData, result)
	if err != nil {
		return fmt.Errorf("capture backup manifest evidence: %w", err)
	}
	partialManifestPath := filepath.Join(partialPath, manifestFileName)
	if err := writeJSONData(partialManifestPath, manifestData, 0600); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if err := verifyManifestTreeContents(ctx, partialPath, manifest); err != nil {
		return fmt.Errorf("verify backup snapshot structure: %w", err)
	}
	if job.VerifyAfterBackup {
		if _, _, err := verifyManifestFiles(ctx, partialPath, manifest); err != nil {
			return fmt.Errorf("verify backup snapshot: %w", err)
		}
	}
	if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
		return err
	}
	if err := verifyLocalSnapshotEntryIdentity(snapshotRootHandle, partialName, partialInfo); err != nil {
		return err
	}
	if err := syncLocalBackupSnapshotTree(partialDir, partialPath); err != nil {
		return fmt.Errorf("sync partial backup snapshot: %w", mapBackupNoFollowError(err, "destination"))
	}
	if err := verifyLocalSnapshotEntryIdentity(snapshotRootHandle, partialName, partialInfo); err != nil {
		return err
	}
	renameErr := renameLocalBackupSnapshot(snapshotRootHandle, partialName, finalName)
	renamed, reconcileErr := reconcileLocalSnapshotRename(snapshotRootHandle, partialName, finalName, partialInfo)
	if !renamed {
		if renameErr == nil {
			renameErr = fmt.Errorf("%w: local snapshot rename was not committed", ErrUnsafePath)
		}
		return errors.Join(fmt.Errorf("finalize backup snapshot: %w", mapBackupNoFollowError(renameErr, "destination")), reconcileErr)
	}
	cleanupPartial = false
	if err := syncLocalBackupDirectoryHandle(snapshotRootDir); err != nil {
		return fmt.Errorf("sync finalized backup snapshot directory: %w", mapBackupNoFollowError(err, "destination"))
	}
	if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
		return err
	}
	afterFinalizeLocalBackupSnapshot(finalPath)

	digest, observation, err := manifestEvidenceDigest(ctx, result.ManifestPath, int64(len(manifestData)), result)
	if err != nil {
		captureErr := fmt.Errorf("verify finalized backup manifest evidence: %w", err)
		if cleanupErr := removeLocalSnapshotEntryDurably(snapshotRootDir, snapshotRoot, finalName, partialInfo); cleanupErr != nil {
			return errors.Join(captureErr, cleanupErr)
		}
		return captureErr
	}
	if digest != trustedDigest {
		captureErr := fmt.Errorf("%w: finalized backup manifest differs from generated manifest", ErrUnsafePath)
		if cleanupErr := removeLocalSnapshotEntryDurably(snapshotRootDir, snapshotRoot, finalName, partialInfo); cleanupErr != nil {
			return errors.Join(captureErr, cleanupErr)
		}
		return captureErr
	}
	if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
		return err
	}
	if err := verifyLocalSnapshotEntryIdentity(snapshotRootHandle, finalName, partialInfo); err != nil {
		return err
	}
	result.ManifestSize = observation.size
	result.ManifestDigest = trustedDigest
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
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runResticRestoreDrill(commandCtx, executionJob, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	}
	if job.Type == JobTypeRclone {
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runRcloneRestoreDrill(commandCtx, executionJob, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	}
	if job.Type != JobTypeLocal {
		return ErrUnsupportedJobType
	}
	source := effectiveSource(job, m.storageRoot)
	if err := validateDestination(source, job.Destination, m.storageRoot); err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, _, _, err := m.latestManifest(ctx, job)
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

	directoryModes, err := restoreManifestDirectories(ctx, filepath.Join(restoredPath, "data"), manifest.Directories)
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

	fileCount, verifiedBytes, err := verifyRestoredManifestFiles(ctx, restoredPath, manifest)
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
		_, err := m.withJobCredentialSnapshot(ctx, job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runResticRestorePreview(commandCtx, executionJob, opts, result)
		})
		return err
	case JobTypeRclone:
		_, err := m.withJobCredentialSnapshot(ctx, job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runRcloneRestorePreview(commandCtx, executionJob, opts, result)
		})
		return err
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRestorePreview(ctx context.Context, job config.BackupJobConfig, opts RestorePreviewOptions, result *RestorePreviewResult) error {
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, _, _, err := m.latestManifest(ctx, job)
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
	if job.Type == JobTypeLocal {
		if err := requireLocalManifestModeSemantics(); err != nil {
			return err
		}
	}
	targetPath, err := validateRestoreVerificationTarget(effectiveSource(job, m.storageRoot), backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}

	fileCount, verifiedBytes, warnings, err := summarizeRestoreVerificationTree(ctx, targetPath)
	if err != nil {
		return err
	}

	var matchingLocalRestore *RestoreResult
	if job.Type == JobTypeLocal {
		matchingLocalRestore = m.latestCompletedRestoreForTarget(job.ID, targetPath)
		if matchingLocalRestore != nil {
			result.TargetPath = strings.TrimSpace(matchingLocalRestore.TargetPath)
		}
	}
	configPath := filepath.Join(targetPath, ".mnemonas-restore", "config.toml")
	configFound := false
	if matchingLocalRestore == nil || matchingLocalRestore.ConfigRestored {
		configFound, err = regularFileExistsStrictNoFollow(configPath)
		if err != nil {
			warnings = appendRestoreVerificationWarning(warnings, fmt.Sprintf("检查配置文件失败: %v", err))
		}
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
		warnings, compareErr = m.appendLocalRestoreSnapshotWarnings(ctx, job, targetPath, matchingLocalRestore, result, warnings)
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

	if matchingLocalRestore == nil {
		result.TargetPath = targetPath
	}
	result.FileCount = fileCount
	result.VerifiedBytes = verifiedBytes
	if configFound {
		result.ConfigPath = filepath.Join(result.TargetPath, ".mnemonas-restore", "config.toml")
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
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runResticRestore(commandCtx, executionJob, opts, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	case JobTypeRclone:
		binding, err := m.withJobCredentialSnapshot(ctx, job, result.JobConfigBinding, func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runRcloneRestore(commandCtx, executionJob, opts, result)
		})
		if binding != "" {
			result.JobConfigBinding = binding
		}
		return err
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRestore(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	targetPath, err := validateRestoreTarget(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	snapshotPath, manifestPath, manifest, trustedRun, manifestDigest, err := m.latestManifest(ctx, job)
	if err != nil {
		return err
	}
	partial, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = removeRestoreStagingTarget(partial, "restore staging target")
		}
	}()

	fileCount, verifiedBytes, configRestored, configPath, err := restoreManifestToTarget(ctx, snapshotPath, partial.Path, manifest, opts.IncludeConfig)
	if err != nil {
		return err
	}
	rootMode := manifestDirectoryMode(manifest.Directories[0])
	if err := installRestoreTargetWithFinalMode(partial, targetPath, &rootMode); err != nil {
		return err
	}
	cleanupPartial = false
	if configRestored {
		configPath = filepath.Join(targetPath, ".mnemonas-restore", "config.toml")
	}

	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
	result.ManifestSize = trustedRun.ManifestSize
	result.ManifestDigest = manifestDigest
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
	targetPath, err := validateRestoreTarget(source, backupTarget(job), m.storageRoot, opts.TargetPath)
	if err != nil {
		return err
	}
	partial, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	cleanupPartial := true
	defer func() {
		if cleanupPartial {
			_ = removeRestoreStagingTarget(partial, "restore staging target")
		}
	}()

	args := append(rcloneBaseArgs(job), "copy", job.Remote, partial.Path, "--create-empty-src-dirs")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return fmt.Errorf("restore rclone remote: %w", err)
	}

	args = append(rcloneBaseArgs(job), "check", job.Remote, partial.Path, "--one-way")
	for _, pattern := range job.Exclude {
		args = append(args, "--exclude", pattern)
	}
	if err := runExternalCommand(ctx, backupCommand(job, JobTypeRclone), args...); err != nil {
		return fmt.Errorf("verify restored rclone remote: %w", err)
	}

	if err := verifyRestoreStagingIdentity(partial); err != nil {
		return err
	}
	fileCount, restoredBytes, err := summarizeRestoredTree(ctx, partial.Path)
	if err != nil {
		return err
	}
	if err := installRestoreTarget(partial, targetPath); err != nil {
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
	partial, err := createPartialRestoreTarget(targetPath, result.ID)
	if err != nil {
		return err
	}
	raw, err := createNamedRestoreTarget(targetPath, ".restic-"+result.ID)
	if err != nil {
		_ = removeRestoreStagingTarget(partial, "restore staging target")
		return err
	}
	cleanupPartial := true
	cleanupRaw := true
	defer func() {
		if cleanupRaw {
			_ = removeRestoreStagingTarget(raw, "restic restore staging target")
		}
		if cleanupPartial {
			_ = removeRestoreStagingTarget(partial, "restore staging target")
		}
	}()

	args := []string{
		"-r", job.Repository,
		"--password-file", job.PasswordFile,
		"restore", "latest",
		"--target", raw.Path,
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

	if err := verifyRestoreStagingIdentity(raw); err != nil {
		return err
	}
	fileCount, restoredBytes, err := moveResticRestoredSource(ctx, raw.Path, partial.Path, source)
	if err != nil {
		return err
	}
	if err := removeRestoreStagingTarget(raw, "restic restore staging target"); err != nil {
		return fmt.Errorf("cleanup restic restore staging: %w", err)
	}
	cleanupRaw = false

	if err := installRestoreTarget(partial, targetPath); err != nil {
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

func (m *Manager) runRetentionCheckForJob(ctx context.Context, job config.BackupJobConfig, notify, acquireTargetLock bool) (returnedResult *RetentionCheckResult, returnedErr error) {
	startedAt := m.now().UTC()
	result := &RetentionCheckResult{
		ID:        formatRunID(startedAt),
		JobID:     job.ID,
		Status:    StatusRunning,
		StartedAt: startedAt,
		Target:    backupTarget(job),
		Policy:    job.RetentionPolicy,
	}
	if saveErr := m.updateLastRetentionCheck(result); saveErr != nil {
		finishedAt := m.now().UTC()
		result.FinishedAt = &finishedAt
		result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
		result.Status = StatusFailed
		result.ErrorMessage = fmt.Sprintf("persist running retention check state: %v", saveErr)
		if isBackupPersistenceWarning(saveErr) {
			if terminalErr := m.updateLastRetentionCheck(result); terminalErr != nil {
				if !isBackupPersistenceWarning(terminalErr) {
					m.recordVolatileLastRetentionCheck(result)
				}
				saveErr = errors.Join(saveErr, terminalErr)
			}
		}
		if notify {
			m.notifyRetentionCheck(ctx, job, result)
		}
		return cloneRetentionCheckResult(result), saveErr
	}
	var targetLock *backupStateLock
	if acquireTargetLock {
		var lockErr error
		targetLock, lockErr = m.acquireJobTargetLock(job)
		if lockErr != nil {
			finishedAt := m.now().UTC()
			result.FinishedAt = &finishedAt
			result.DurationMs = max(finishedAt.Sub(result.StartedAt).Milliseconds(), 0)
			result.Status = StatusFailed
			result.ErrorMessage = lockErr.Error()
			if saveErr := m.updateLastRetentionCheck(result); saveErr != nil {
				if isBackupPersistenceWarning(saveErr) {
					result.Warning = true
					result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, retentionCheckStateWarning))
					warningSaveErr := m.updateLastRetentionCheck(result)
					if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
						m.recordVolatileLastRetentionCheck(result)
					}
					if notify {
						m.notifyRetentionCheck(ctx, job, result)
					}
					return cloneRetentionCheckResult(result), errors.Join(lockErr, saveErr, warningSaveErr)
				}
				if !isBackupPersistenceWarning(saveErr) {
					m.recordVolatileLastRetentionCheck(result)
				}
				lockErr = errors.Join(lockErr, saveErr)
			}
			if notify {
				m.notifyRetentionCheck(ctx, job, result)
			}
			return cloneRetentionCheckResult(result), lockErr
		}
	}
	if targetLock != nil {
		defer appendBackupTargetLockCloseError(targetLock, &returnedErr)
	}

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
	if isBackupPersistenceWarning(saveErr) {
		result.Warning = true
		result.Warnings = append(result.Warnings, backupStateWarningMessage(saveErr, retentionCheckStateWarning))
		warningSaveErr := m.updateLastRetentionCheck(result)
		if warningSaveErr != nil && !isBackupPersistenceWarning(warningSaveErr) {
			m.recordVolatileLastRetentionCheck(result)
		}
		if notify {
			m.notifyRetentionCheck(ctx, job, result)
		}
		if err != nil {
			return cloneRetentionCheckResult(result), errors.Join(err, saveErr, warningSaveErr)
		}
		return cloneRetentionCheckResult(result), nil
	}
	if saveErr != nil && !isBackupPersistenceWarning(saveErr) {
		m.recordVolatileLastRetentionCheck(result)
	}
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
		_, err := m.withJobCredentialSnapshot(ctx, job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runResticRetentionCheck(commandCtx, executionJob, result)
		})
		return err
	case JobTypeRclone:
		_, err := m.withJobCredentialSnapshot(ctx, job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
			return m.runRcloneRetentionCheck(commandCtx, executionJob, result)
		})
		return err
	default:
		return ErrUnsupportedJobType
	}
}

func (m *Manager) runLocalRetentionCheck(ctx context.Context, job config.BackupJobConfig, result *RetentionCheckResult) error {
	if err := requireLocalManifestModeSemantics(); err != nil {
		return err
	}
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
	currentSnapshots := currentManifestSnapshots(snapshots)
	legacyCount := legacyManifestSnapshotCount(snapshots)
	fillRetentionSnapshotRange(result, snapshotTimes(currentSnapshots))
	result.SnapshotCount = len(snapshots)
	if len(snapshots) == 0 {
		result.Warnings = append(result.Warnings, "尚无本地快照，无法确认保留策略是否有效")
	}
	if legacyCount > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("检测到 %d 个 v1 本地快照；这些快照不会用于恢复或自动清理，需要人工确认后处置", legacyCount))
	}
	if job.MaxSnapshots <= 0 && job.MaxAge <= 0 {
		result.Warnings = append(result.Warnings, "本地快照未配置 max_snapshots 或 max_age，旧快照不会自动清理")
	}
	if job.MaxSnapshots > 0 && len(currentSnapshots) > job.MaxSnapshots {
		result.Warnings = append(result.Warnings, fmt.Sprintf("v2 本地快照数量 %d 已超过 max_snapshots=%d", len(currentSnapshots), job.MaxSnapshots))
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

func (m *Manager) latestManifest(ctx context.Context, job config.BackupJobConfig) (string, string, Manifest, *RunResult, string, error) {
	if err := requireLocalManifestModeSemantics(); err != nil {
		return "", "", Manifest{}, nil, "", err
	}
	if err := validateDestination(effectiveSource(job, m.storageRoot), job.Destination, m.storageRoot); err != nil {
		return "", "", Manifest{}, nil, "", err
	}

	m.mu.Lock()
	state := m.state.Jobs[job.ID]
	lastRun := cloneRunResultRaw(state.LastSuccessfulRun)
	m.mu.Unlock()
	if lastRun == nil || lastRun.Status != StatusCompleted || lastRun.ManifestPath == "" {
		return "", "", Manifest{}, nil, "", ErrNoSnapshots
	}
	if err := validateSnapshotManifestLocation(job, lastRun.ID, lastRun.SnapshotPath, lastRun.ManifestPath); err != nil {
		return "", "", Manifest{}, nil, "", err
	}
	manifest, restoreDigest, err := readTrustedRunManifest(ctx, job, lastRun)
	if err != nil {
		return "", "", Manifest{}, nil, "", err
	}
	return lastRun.SnapshotPath, lastRun.ManifestPath, manifest, lastRun, restoreDigest, nil
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
	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	snapshotRootHandle, snapshotDir, snapshotRootInfo, err := openLocalSnapshotRoot(snapshotRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, []string{fmt.Sprintf("apply retention: %v", err)}
	}
	defer snapshotRootHandle.Close()
	defer snapshotDir.Close()

	snapshots, err := listLocalSnapshotsFromRoot(ctx, job, snapshotRoot, snapshotRootHandle, snapshotDir, snapshotRootInfo)
	if err != nil {
		return 0, []string{fmt.Sprintf("apply retention: %v", err)}
	}
	snapshots = currentManifestSnapshots(snapshots)
	if len(snapshots) <= 1 {
		return 0, nil
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})

	currentClean := filepath.Clean(currentSnapshotPath)
	latestSnapshot := filepath.Clean(snapshots[0].Path)
	restoreReferences := m.restoreReferencedSnapshotPaths(job)
	cutoff := m.now().UTC().Add(-job.MaxAge)
	deleteSet := make([]snapshotInfo, 0)
	for index, snapshot := range snapshots {
		snapshotPath := filepath.Clean(snapshot.Path)
		if snapshotPath == currentClean || snapshotPath == latestSnapshot {
			continue
		}
		if _, referenced := restoreReferences[snapshotPath]; referenced {
			continue
		}
		deleteForCount := job.MaxSnapshots > 0 && index >= job.MaxSnapshots
		deleteForAge := job.MaxAge > 0 && snapshot.CreatedAt.Before(cutoff)
		if deleteForCount || deleteForAge {
			deleteSet = append(deleteSet, snapshot)
		}
	}
	sort.Slice(deleteSet, func(i, j int) bool {
		if deleteSet[i].CreatedAt.Equal(deleteSet[j].CreatedAt) {
			return deleteSet[i].Name < deleteSet[j].Name
		}
		return deleteSet[i].CreatedAt.Before(deleteSet[j].CreatedAt)
	})

	var warnings []string
	removed := 0
	for _, snapshot := range deleteSet {
		if err := ctx.Err(); err != nil {
			warnings = append(warnings, fmt.Sprintf("retention interrupted: %v", err))
			break
		}
		if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop retention because snapshot root changed: %v", err))
			break
		}
		if err := removeLocalBackupSnapshotEntry(snapshotDir, snapshotRoot, snapshot.Name, snapshot.Identity); err != nil {
			warnings = append(warnings, fmt.Sprintf("remove snapshot %s: %v", snapshot.Name, err))
			continue
		}
		removed++
	}
	if removed > 0 {
		if err := syncLocalBackupDirectoryHandle(snapshotDir); err != nil {
			warnings = append(warnings, fmt.Sprintf("sync snapshot retention changes: %v", err))
			return 0, warnings
		}
		if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
			warnings = append(warnings, fmt.Sprintf("snapshot root changed after retention: %v", err))
			return 0, warnings
		}
	}
	return removed, warnings
}

func currentManifestSnapshots(snapshots []snapshotInfo) []snapshotInfo {
	current := make([]snapshotInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !snapshot.LegacyV1 {
			current = append(current, snapshot)
		}
	}
	return current
}

func legacyManifestSnapshotCount(snapshots []snapshotInfo) int {
	count := 0
	for _, snapshot := range snapshots {
		if snapshot.LegacyV1 {
			count++
		}
	}
	return count
}

func (m *Manager) restoreReferencedSnapshotPaths(job config.BackupJobConfig) map[string]struct{} {
	m.mu.Lock()
	state := m.state.Jobs[job.ID]
	restores := append(cloneRestoreResultsRaw(state.RestoreHistory), cloneRestoreResultRaw(state.LastRestore))
	m.mu.Unlock()

	references := make(map[string]struct{}, len(restores))
	for _, restore := range restores {
		if restore == nil || restore.Status != StatusCompleted || strings.TrimSpace(restore.SnapshotPath) == "" {
			continue
		}
		snapshotPath := filepath.Clean(restore.SnapshotPath)
		snapshotName := filepath.Base(snapshotPath)
		if err := validateSnapshotManifestLocation(job, snapshotName, snapshotPath, filepath.Join(snapshotPath, manifestFileName)); err != nil {
			continue
		}
		expectedSnapshotPath := filepath.Clean(filepath.Join(job.Destination, job.ID, "snapshots", snapshotName))
		references[expectedSnapshotPath] = struct{}{}
	}
	return references
}

type snapshotInfo struct {
	Name      string
	Path      string
	CreatedAt time.Time
	Identity  os.FileInfo
	LegacyV1  bool
}

func listLocalSnapshots(ctx context.Context, job config.BackupJobConfig) ([]snapshotInfo, error) {
	snapshotRoot := filepath.Join(job.Destination, job.ID, "snapshots")
	snapshotRootHandle, snapshotDir, snapshotRootInfo, err := openLocalSnapshotRoot(snapshotRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer snapshotRootHandle.Close()
	defer snapshotDir.Close()
	return listLocalSnapshotsFromRoot(ctx, job, snapshotRoot, snapshotRootHandle, snapshotDir, snapshotRootInfo)
}

func listLocalSnapshotsFromRoot(
	ctx context.Context,
	job config.BackupJobConfig,
	snapshotRoot string,
	snapshotRootHandle *os.Root,
	snapshotDir *os.File,
	snapshotRootInfo os.FileInfo,
) ([]snapshotInfo, error) {
	entries, err := snapshotDir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
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
		if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
			return nil, err
		}
		snapshotEntry, err := openLocalSnapshotEntry(snapshotDir, snapshotRoot, name)
		if err != nil {
			return nil, fmt.Errorf("open snapshot %s: %w", name, mapBackupNoFollowError(err, "backup snapshot"))
		}
		snapshotIdentity, statErr := snapshotEntry.Stat()
		closeErr := snapshotEntry.Close()
		if statErr != nil {
			return nil, fmt.Errorf("inspect snapshot %s: %w", name, statErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close snapshot %s: %w", name, closeErr)
		}
		if !snapshotIdentity.IsDir() {
			return nil, fmt.Errorf("%w: backup snapshot %s is not a directory", ErrUnsafePath, name)
		}
		snapshotPath := filepath.Join(snapshotRoot, name)
		createdAt := parseSnapshotTime(name)
		_, manifest, err := readManifestDataWithExpectedSizeMode(ctx, filepath.Join(snapshotPath, manifestFileName), 0, true)
		if err != nil {
			return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
		}
		if err := validateSnapshotManifestIdentity(job.ID, name, manifest); err != nil {
			return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
		}
		legacyV1 := manifest.Version == 1
		if !legacyV1 {
			if err := verifyManifestTreeContents(ctx, snapshotPath, manifest); err != nil {
				return nil, fmt.Errorf("read snapshot manifest %s: %w", name, err)
			}
		}
		if err := verifyLocalSnapshotRootIdentity(snapshotRoot, snapshotRootInfo); err != nil {
			return nil, err
		}
		if err := verifyLocalSnapshotEntryIdentity(snapshotRootHandle, name, snapshotIdentity); err != nil {
			return nil, fmt.Errorf("recheck snapshot %s: %w", name, err)
		}
		if !manifest.CreatedAt.IsZero() {
			createdAt = manifest.CreatedAt
		}
		snapshots = append(snapshots, snapshotInfo{
			Name:      name,
			Path:      snapshotPath,
			CreatedAt: createdAt.UTC(),
			Identity:  snapshotIdentity,
			LegacyV1:  legacyV1,
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

func copySourceTree(ctx context.Context, source, destination string, excludes []string) ([]ManifestEntry, []ManifestDirectory, int64, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("open backup source: %w", err)
	}
	defer root.Close()

	var entries []ManifestEntry
	var totalBytes int64
	var directoryModes []directoryMode
	if err := copySourceTreeEntry(ctx, root, ".", destination, excludes, &entries, &totalBytes, &directoryModes); err != nil {
		return nil, nil, 0, err
	}
	if err := applyDirectoryModesNoFollow(destination, directoryModes, "destination"); err != nil {
		return nil, nil, 0, err
	}
	directories := make([]ManifestDirectory, 0, len(directoryModes))
	for _, directory := range directoryModes {
		archivePath := "data"
		if directory.RelPath != "." {
			archivePath = path.Join(archivePath, filepath.ToSlash(directory.RelPath))
		}
		directories = append(directories, ManifestDirectory{
			ArchivePath: archivePath,
			Mode:        uint32(directory.Mode.Perm()),
		})
	}
	return entries, directories, totalBytes, nil
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
		dir, err := rootio.OpenDirNoFollow(root, relPath)
		if err != nil {
			return mapSourceRootError(err)
		}
		openedInfo, statErr := dir.Stat()
		if statErr != nil || !openedInfo.IsDir() || openedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, openedInfo) {
			_ = dir.Close()
			return errors.Join(ErrUnsafePath, statErr, errors.New("backup source directory changed while opening"))
		}
		if err := validateSourceDirectoryMode(relPath, openedInfo.Mode()); err != nil {
			_ = dir.Close()
			return err
		}
		mode := openedInfo.Mode().Perm()
		if relPath != "." {
			destinationPath := filepath.Join(destination, relPath)
			if err := rootio.MkdirAllPathNoFollow(destinationPath, writableDirectoryMode(mode)); err != nil {
				_ = dir.Close()
				return fmt.Errorf("create backup directory %s: %w", relPath, mapBackupNoFollowError(err, "destination"))
			}
			*directoryModes = append(*directoryModes, directoryMode{RelPath: relPath, Mode: mode})
		} else {
			if err := rootio.MkdirAllPathNoFollow(destination, writableDirectoryMode(mode)); err != nil {
				_ = dir.Close()
				return fmt.Errorf("create backup data directory: %w", mapBackupNoFollowError(err, "destination"))
			}
			*directoryModes = append(*directoryModes, directoryMode{RelPath: relPath, Mode: mode})
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
		afterInfo, err := root.Lstat(relPath)
		if err != nil || !afterInfo.IsDir() || !os.SameFile(openedInfo, afterInfo) || afterInfo.Mode()&specialPermissionBits != 0 || afterInfo.Mode().Perm() != mode {
			return errors.Join(ErrUnsafePath, err, errors.New("backup source directory changed while copying"))
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
	openedInfo, err := sourceFile.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return errors.Join(ErrUnsafePath, err, errors.New("backup source file changed while opening"))
	}

	archivePath := path.Join("data", filepath.ToSlash(relPath))
	entry, err := copyOpenFileWithHash(ctx, sourceFile, openedInfo, filepath.Join(destination, relPath), archivePath, filepath.ToSlash(relPath))
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
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
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
	if sourceInfo.Mode()&specialPermissionBits != 0 {
		return nil, fmt.Errorf("%w: backup source file has unsupported special permission bits: %s", ErrUnsafePath, cleanPreviewSamplePath(sourceLabel))
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
	destinationInfo, err := destination.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat backup file after setting mode %s: %w", archivePath, err)
	}
	if destinationInfo.Mode()&specialPermissionBits != 0 || destinationInfo.Mode().Perm() != sourceInfo.Mode().Perm() {
		return nil, fmt.Errorf("%w: backup file mode could not be preserved: %s", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
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

func mapBackupRegularFileNoFollowError(err error, label string) error {
	if errors.Is(err, fs.ErrInvalid) || errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("%w: %s must be a regular file", ErrUnsafePath, label)
	}
	return mapBackupNoFollowError(err, label)
}

func verifyManifestFiles(ctx context.Context, root string, manifest Manifest) (int64, int64, error) {
	return verifyManifestFilesWithLayout(ctx, root, manifest, true)
}

func verifyRestoredManifestFiles(ctx context.Context, root string, manifest Manifest) (int64, int64, error) {
	return verifyManifestFilesWithLayout(ctx, root, manifest, false)
}

func verifyManifestFilesWithLayout(ctx context.Context, root string, manifest Manifest, includeManifestFile bool) (int64, int64, error) {
	if err := validateManifestEntries(manifest); err != nil {
		return 0, 0, err
	}
	if err := verifyManifestTreeContentsWithLayout(ctx, root, manifest, includeManifestFile); err != nil {
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
	return verifyManifestTreeContentsWithLayout(ctx, root, manifest, true)
}

func verifyManifestTreeContentsWithLayout(ctx context.Context, root string, manifest Manifest, includeManifestFile bool) error {
	if err := validateManifestEntries(manifest); err != nil {
		return err
	}
	if includeManifestFile {
		observedManifest, err := readManifest(filepath.Join(root, manifestFileName))
		if err != nil {
			return errors.Join(ErrUnsafePath, fmt.Errorf("verify backup snapshot manifest: %w", err))
		}
		expectedDigest, err := manifestSemanticDigest(manifest)
		if err != nil {
			return err
		}
		observedDigest, err := manifestSemanticDigest(observedManifest)
		if err != nil {
			return err
		}
		if observedDigest != expectedDigest {
			return fmt.Errorf("%w: backup snapshot manifest content changed", ErrUnsafePath)
		}
	}
	state := manifestTreeVerification{
		expectedFiles:       make(map[string]struct{}, len(manifest.Entries)+1),
		seenFiles:           make(map[string]struct{}, len(manifest.Entries)+1),
		expectedDirectories: make(map[string]os.FileMode, len(manifest.Directories)+1),
		seenDirectories:     make(map[string]struct{}, len(manifest.Directories)+1),
	}
	if includeManifestFile {
		state.expectedFiles[manifestFileName] = struct{}{}
	}
	for _, directory := range manifest.Directories {
		state.expectedDirectories[directory.ArchivePath] = manifestDirectoryMode(directory)
	}
	for _, entry := range manifest.Entries {
		archivePath := entry.ArchivePath
		state.expectedFiles[archivePath] = struct{}{}
		if archivePath == "config/config.toml" {
			state.expectedDirectories["config"] = 0700
		}
	}

	if err := verifyManifestTreeEntry(ctx, root, ".", &state); err != nil {
		return fmt.Errorf("verify backup snapshot tree: %w", err)
	}
	for expected := range state.expectedFiles {
		if _, ok := state.seenFiles[expected]; !ok {
			return fmt.Errorf("%w: backup snapshot is missing manifest file %q", ErrUnsafePath, cleanPreviewSamplePath(expected))
		}
	}
	for expected := range state.expectedDirectories {
		if _, ok := state.seenDirectories[expected]; !ok {
			return fmt.Errorf("%w: backup snapshot is missing manifest directory %q", ErrUnsafePath, cleanPreviewSamplePath(expected))
		}
	}
	return nil
}

func verifyExplicitRestoreTreeContents(ctx context.Context, root string, manifest Manifest, includeConfig bool) error {
	if len(manifest.Directories) == 0 {
		return fmt.Errorf("%w: backup manifest has no data root directory", ErrUnsafePath)
	}
	return verifyExplicitRestoreTreeContentsWithRootMode(ctx, root, manifest, includeConfig, manifestDirectoryMode(manifest.Directories[0]))
}

func verifyExplicitRestoreTreeContentsWithRootMode(ctx context.Context, root string, manifest Manifest, includeConfig bool, rootMode os.FileMode) error {
	if err := validateManifestEntries(manifest); err != nil {
		return err
	}
	if err := validateExplicitConfigRestoreNamespace(manifest, includeConfig); err != nil {
		return err
	}
	state := manifestTreeVerification{
		expectedFiles:       make(map[string]struct{}, len(manifest.Entries)),
		seenFiles:           make(map[string]struct{}, len(manifest.Entries)),
		expectedDirectories: make(map[string]os.FileMode, len(manifest.Directories)),
		seenDirectories:     make(map[string]struct{}, len(manifest.Directories)),
		expectedRootMode:    &rootMode,
	}
	for _, directory := range manifest.Directories {
		mode := manifestDirectoryMode(directory)
		if directory.ArchivePath == "data" {
			continue
		}
		relPath := strings.TrimPrefix(directory.ArchivePath, "data/")
		state.expectedDirectories[relPath] = mode
	}
	for _, entry := range manifest.Entries {
		if relPath, ok := strings.CutPrefix(entry.ArchivePath, "data/"); ok {
			state.expectedFiles[relPath] = struct{}{}
			continue
		}
		if includeConfig && entry.ArchivePath == "config/config.toml" {
			state.expectedDirectories[".mnemonas-restore"] = 0700
			state.expectedFiles[".mnemonas-restore/config.toml"] = struct{}{}
		}
	}

	if err := verifyManifestTreeEntry(ctx, root, ".", &state); err != nil {
		return fmt.Errorf("verify explicit restore tree: %w", err)
	}
	for expected := range state.expectedFiles {
		if _, ok := state.seenFiles[expected]; !ok {
			return fmt.Errorf("%w: explicit restore is missing file %q", ErrUnsafePath, cleanPreviewSamplePath(expected))
		}
	}
	for expected := range state.expectedDirectories {
		if _, ok := state.seenDirectories[expected]; !ok {
			return fmt.Errorf("%w: explicit restore is missing directory %q", ErrUnsafePath, cleanPreviewSamplePath(expected))
		}
	}
	return nil
}

type manifestTreeVerification struct {
	expectedFiles       map[string]struct{}
	seenFiles           map[string]struct{}
	expectedDirectories map[string]os.FileMode
	seenDirectories     map[string]struct{}
	expectedRootMode    *os.FileMode
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
		if relPath == "." && state.expectedRootMode != nil {
			if info.Mode()&specialPermissionBits != 0 || info.Mode().Perm() != *state.expectedRootMode {
				return fmt.Errorf("%w: manifest tree root mode mismatch: got %04o, want %04o", ErrUnsafePath, info.Mode().Perm(), *state.expectedRootMode)
			}
		} else if relPath != "." {
			expectedMode, ok := state.expectedDirectories[relSlash]
			if !ok {
				return fmt.Errorf("%w: backup snapshot contains unmanifested directory %q", ErrUnsafePath, cleanPreviewSamplePath(relSlash))
			}
			if info.Mode()&specialPermissionBits != 0 || info.Mode().Perm() != expectedMode {
				return fmt.Errorf("%w: backup snapshot directory mode mismatch for %q: got %04o, want %04o", ErrUnsafePath, cleanPreviewSamplePath(relSlash), info.Mode().Perm(), expectedMode)
			}
			state.seenDirectories[relSlash] = struct{}{}
		}
		dir, err := rootio.OpenDirPathNoFollow(entryPath)
		if err != nil {
			return fmt.Errorf("open backup snapshot directory %s: %w", relSlash, mapBackupNoFollowError(err, "backup snapshot"))
		}
		openedInfo, statErr := dir.Stat()
		if statErr != nil || !openedInfo.IsDir() || openedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, openedInfo) {
			_ = dir.Close()
			return errors.Join(ErrUnsafePath, statErr, fmt.Errorf("backup snapshot directory changed while opening: %s", relSlash))
		}
		if relPath == "." && state.expectedRootMode != nil {
			if openedInfo.Mode()&specialPermissionBits != 0 || openedInfo.Mode().Perm() != *state.expectedRootMode {
				_ = dir.Close()
				return fmt.Errorf("%w: manifest tree root mode changed while opening", ErrUnsafePath)
			}
		} else if relPath != "." {
			expectedMode := state.expectedDirectories[relSlash]
			if openedInfo.Mode()&specialPermissionBits != 0 || openedInfo.Mode().Perm() != expectedMode {
				_ = dir.Close()
				return fmt.Errorf("%w: backup snapshot directory mode changed while opening for %q", ErrUnsafePath, cleanPreviewSamplePath(relSlash))
			}
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
	state.seenFiles[relSlash] = struct{}{}
	return nil
}

func hashFile(ctx context.Context, filePath string) (int64, string, os.FileMode, error) {
	file, info, err := openRegularFileNoFollow(filePath, filePath)
	if err != nil {
		return 0, "", 0, err
	}
	defer file.Close()
	if info.Mode()&specialPermissionBits != 0 {
		return 0, "", 0, fmt.Errorf("%w: backup file has unsupported special permission bits: %s", ErrUnsafePath, cleanPreviewSamplePath(filePath))
	}

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
	return m.updateLastRunWithScheduledAt(result, nil)
}

func (m *Manager) updateLastRunWithScheduledAt(result *RunResult, scheduledAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateJobStateLocked(result.JobID, func(state *JobState) {
		state.LastRun = cloneRunResultRaw(result)
		if scheduledAt != nil {
			state.LastScheduledRunAt = cloneTime(scheduledAt)
		}
		if result.Status == StatusCompleted {
			state.LastSuccessfulRun = cloneRunResultRaw(result)
		}
	})
}

func (m *Manager) updateLastRestoreDrill(result *RestoreDrillResult, appendHistory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateJobStateLocked(result.JobID, func(state *JobState) {
		state.LastRestoreDrill = cloneRestoreDrillResultRaw(result)
		if appendHistory {
			state.RestoreDrillHistory = prependRestoreDrillHistory(state.RestoreDrillHistory, result)
		}
	})
}

func (m *Manager) updateLastRestore(result *RestoreResult, appendHistory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateJobStateLocked(result.JobID, func(state *JobState) {
		state.LastRestore = cloneRestoreResultRaw(result)
		if appendHistory {
			state.RestoreHistory = prependRestoreHistory(state.RestoreHistory, result)
		}
	})
}

func (m *Manager) updateLastRestoreVerify(result *RestoreVerifyResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateJobStateLocked(result.JobID, func(state *JobState) {
		state.LastRestoreVerify = cloneRestoreVerifyResultRaw(result)
	})
}

func (m *Manager) updateLastRetentionCheck(result *RetentionCheckResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateJobStateLocked(result.JobID, func(state *JobState) {
		state.LastRetentionCheck = cloneRetentionCheckResultRaw(result)
	})
}

func (m *Manager) updateJobStateLocked(jobID string, update func(*JobState)) error {
	candidate := clonePersistedState(m.state)
	state := candidate.Jobs[jobID]
	update(&state)
	candidate.Jobs[jobID] = state
	return m.persistStateCandidateLocked(candidate)
}

func (m *Manager) recordVolatileLastRun(result *RunResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRun = cloneRunResultRaw(result)
	if result.Status == StatusCompleted {
		state.LastSuccessfulRun = cloneRunResultRaw(result)
	}
	m.state.Jobs[result.JobID] = state
}

func (m *Manager) recordVolatileLastRestoreDrill(result *RestoreDrillResult, appendHistory bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestoreDrill = cloneRestoreDrillResultRaw(result)
	if appendHistory {
		state.RestoreDrillHistory = prependRestoreDrillHistory(state.RestoreDrillHistory, result)
	}
	m.state.Jobs[result.JobID] = state
}

func (m *Manager) recordVolatileLastRestore(result *RestoreResult, appendHistory bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestore = cloneRestoreResultRaw(result)
	if appendHistory {
		state.RestoreHistory = prependRestoreHistory(state.RestoreHistory, result)
	}
	m.state.Jobs[result.JobID] = state
}

func (m *Manager) recordVolatileLastRestoreVerify(result *RestoreVerifyResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRestoreVerify = cloneRestoreVerifyResultRaw(result)
	m.state.Jobs[result.JobID] = state
}

func (m *Manager) recordVolatileLastRetentionCheck(result *RetentionCheckResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state.Jobs[result.JobID]
	state.LastRetentionCheck = cloneRetentionCheckResultRaw(result)
	m.state.Jobs[result.JobID] = state
}

func clonePersistedState(state persistedState) persistedState {
	jobs := make(map[string]JobState, len(state.Jobs))
	for jobID, jobState := range state.Jobs {
		jobs[jobID] = jobState
	}
	return persistedState{Jobs: jobs}
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
	return m.persistStateCandidateLocked(clonePersistedState(m.state))
}

func (m *Manager) persistStateCandidateLocked(candidate persistedState) error {
	if candidate.Jobs == nil {
		candidate.Jobs = map[string]JobState{}
	}
	if m.stateNamespaceUnsafe {
		m.statePersistenceHealthy = false
		return m.stateNamespaceErrorLocked()
	}
	if err := m.verifyStateNamespaceLocked(); err != nil {
		return m.quarantineStateNamespaceLocked(err, err)
	}

	err := writeBackupStateFile(m.stateLock, m.statePath(), candidate, 0600)
	identityErr := m.verifyStateNamespaceLocked()
	if identityErr != nil {
		switch {
		case err == nil:
			err = wrapBackupPersistenceWarning(identityErr)
		case isBackupPersistenceWarning(err):
			err = errors.Join(err, wrapBackupPersistenceWarning(identityErr))
		default:
			err = errors.Join(&backupStatePersistenceError{err: err}, identityErr)
		}
	}
	m.statePersistenceHealthy = err == nil
	if err == nil || isBackupPersistenceWarning(err) {
		m.state = candidate
	}
	if identityErr != nil {
		return m.quarantineStateNamespaceLocked(identityErr, err)
	}
	if errors.Is(err, ErrUnsafePath) {
		return m.quarantineStateNamespaceLocked(backupPersistenceWarningCause(err), err)
	}
	if err != nil && !isBackupPersistenceWarning(err) {
		if errors.Is(err, ErrBackupStatePersistence) {
			return err
		}
		return &backupStatePersistenceError{err: err}
	}
	return err
}

func (m *Manager) verifyStateNamespaceLocked() error {
	if m.stateLock == nil {
		return fmt.Errorf("%w: backup state lock is unavailable", ErrUnsafePath)
	}
	if err := m.stateLock.verifyDirectoryIdentity(m.root); err != nil {
		return fmt.Errorf("verify backup state namespace: %w", err)
	}
	return nil
}

func (m *Manager) quarantineStateNamespaceLocked(cause, returnedErr error) error {
	m.statePersistenceHealthy = false
	m.stateNamespaceUnsafe = true
	if m.stateNamespaceErr == nil {
		m.stateNamespaceErr = backupPersistenceWarningCause(cause)
	}
	m.closed = true
	return errors.Join(ErrBackupStateNamespaceChanged, returnedErr)
}

func (m *Manager) stateNamespaceErrorLocked() error {
	return errors.Join(ErrBackupStateNamespaceChanged, m.stateNamespaceErr)
}

func backupPersistenceWarningCause(err error) error {
	var warning *backupPersistenceWarningError
	if errors.As(err, &warning) {
		return warning.err
	}
	return err
}

func (m *Manager) statePath() string {
	return filepath.Join(m.root, stateFileName)
}

func writeBackupJSONFile(lock *backupStateLock, filePath string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if lock == nil {
		return fmt.Errorf("%w: backup state lock is unavailable", ErrUnsafePath)
	}
	return lock.writeJSONData(filePath, data, perm)
}

func writeJSONFile(filePath string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeJSONData(filePath, data, perm)
}

func writeJSONData(filePath string, data []byte, perm os.FileMode) error {
	if err := rootio.MkdirAllPathNoFollow(filepath.Dir(filePath), 0700); err != nil {
		return mapBackupNoFollowError(err, "backup json")
	}
	parentPath := filepath.Dir(filePath)
	parentRoot, parentDir, parentInfo, err := openBackupJSONParent(parentPath)
	if err != nil {
		return err
	}
	defer parentRoot.Close()
	defer parentDir.Close()
	return writeJSONDataInParent(parentRoot, parentDir, parentInfo, parentPath, filepath.Base(filePath), data, perm)
}

func (l *backupStateLock) writeJSONData(filePath string, data []byte, perm os.FileMode) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.root == nil {
		return fmt.Errorf("%w: backup state lock directory is closed", ErrUnsafePath)
	}
	parentPath := filepath.Dir(filePath)
	if filepath.Clean(parentPath) != filepath.Clean(filepath.Dir(l.path)) {
		return fmt.Errorf("%w: backup state path is outside the locked directory", ErrUnsafePath)
	}
	parentDir, err := rootio.OpenDirNoFollow(l.root, ".")
	if err != nil {
		return mapBackupNoFollowError(err, "locked backup json parent")
	}
	defer parentDir.Close()
	parentInfo, err := parentDir.Stat()
	if err != nil {
		return err
	}
	rootInfo, err := l.root.Stat(".")
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() || !os.SameFile(parentInfo, rootInfo) {
		return fmt.Errorf("%w: locked backup json parent identity changed", ErrUnsafePath)
	}
	if err := verifyBackupJSONParentIdentity(parentPath, parentInfo); err != nil {
		return err
	}
	return writeJSONDataInParent(l.root, parentDir, parentInfo, parentPath, filepath.Base(filePath), data, perm)
}

func writeJSONDataInParent(
	parentRoot *os.Root,
	parentDir *os.File,
	parentInfo os.FileInfo,
	parentPath string,
	targetName string,
	data []byte,
	perm os.FileMode,
) (writeErr error) {

	tmpFile, tmpName, err := createBackupJSONTempFile(parentRoot, targetName, perm)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			if cleanupErr := parentRoot.Remove(tmpName); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				writeErr = errors.Join(writeErr, fmt.Errorf("cleanup backup json temp file: %w", cleanupErr))
			}
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
	tmpInfo, err := tmpFile.Stat()
	if err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := validateBackupJSONTarget(parentRoot, targetName); err != nil {
		return err
	}
	if err := verifyBackupJSONParentIdentity(parentPath, parentInfo); err != nil {
		return err
	}
	renameErr := renameBackupJSONFile(parentRoot, tmpName, targetName)
	renamed := renameErr == nil
	var reconcileErr error
	if renameErr != nil {
		renamed, reconcileErr = reconcileBackupJSONRename(parentRoot, tmpName, targetName, tmpInfo)
	}
	if !renamed {
		return errors.Join(mapBackupNoFollowError(renameErr, "backup json"), reconcileErr)
	}
	cleanup = false
	afterRenameBackupJSONFile(filepath.Join(parentPath, targetName))
	if err := verifyBackupJSONParentIdentity(parentPath, parentInfo); err != nil {
		return wrapBackupPersistenceWarning(fmt.Errorf("verify backup json parent after replacement: %w", err))
	}
	if err := syncBackupJSONDir(parentDir, parentPath); err != nil {
		if identityErr := verifyBackupJSONParentIdentity(parentPath, parentInfo); identityErr != nil {
			return wrapBackupPersistenceWarning(errors.Join(fmt.Errorf("sync backup json parent directory: %w", err), identityErr))
		}
		return wrapBackupPersistenceWarning(fmt.Errorf("sync backup json parent directory: %w", err))
	}
	if err := verifyBackupJSONParentIdentity(parentPath, parentInfo); err != nil {
		return wrapBackupPersistenceWarning(fmt.Errorf("verify backup json parent after sync: %w", err))
	}
	return nil
}

func reconcileBackupJSONRename(root *os.Root, tmpName, targetName string, expected os.FileInfo) (bool, error) {
	tmpInfo, tmpErr := root.Lstat(tmpName)
	targetInfo, targetErr := root.Lstat(targetName)
	tmpMissing := errors.Is(tmpErr, os.ErrNotExist)
	targetMissing := errors.Is(targetErr, os.ErrNotExist)

	if tmpMissing && targetErr == nil && targetInfo.Mode().IsRegular() && os.SameFile(targetInfo, expected) {
		return true, nil
	}
	if tmpErr == nil && tmpInfo.Mode().IsRegular() && os.SameFile(tmpInfo, expected) {
		return false, nil
	}
	if tmpErr != nil && !tmpMissing {
		return false, fmt.Errorf("inspect backup json temp after rename: %w", tmpErr)
	}
	if targetErr != nil && !targetMissing {
		return false, fmt.Errorf("inspect backup json target after rename: %w", targetErr)
	}
	return false, fmt.Errorf("%w: backup json rename result is ambiguous", ErrUnsafePath)
}

func openBackupJSONParent(parentPath string) (*os.Root, *os.File, os.FileInfo, error) {
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, nil, nil, mapBackupNoFollowError(err, "backup json parent")
	}
	closeRoot := true
	defer func() {
		if closeRoot {
			_ = root.Close()
		}
	}()
	dir, err := rootio.OpenDirNoFollow(root, ".")
	if err != nil {
		return nil, nil, nil, mapBackupNoFollowError(err, "backup json parent")
	}
	info, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, nil, nil, err
	}
	if err := verifyBackupJSONParentIdentity(parentPath, info); err != nil {
		_ = dir.Close()
		return nil, nil, nil, err
	}
	closeRoot = false
	return root, dir, info, nil
}

func verifyBackupJSONParentIdentity(parentPath string, expected os.FileInfo) error {
	info, err := os.Lstat(parentPath)
	if err != nil {
		return fmt.Errorf("inspect backup json parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(info, expected) {
		return fmt.Errorf("%w: backup json parent changed during persistence", ErrUnsafePath)
	}
	return nil
}

func validateBackupJSONTarget(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: backup json target must be a regular file", ErrUnsafePath)
	}
	return nil
}

func createBackupJSONTempFile(root *os.Root, fileName string, perm os.FileMode) (*os.File, string, error) {
	for range maxBackupJSONTempAttempts {
		randomPart := make([]byte, 16)
		if _, err := backupJSONRandomRead(randomPart); err != nil {
			return nil, "", fmt.Errorf("generate backup json temp name: %w", err)
		}
		tmpName := "." + fileName + "." + hex.EncodeToString(randomPart) + ".tmp"
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapBackupNoFollowError(err, "backup json")
	}
	return nil, "", errors.New("allocate unique backup json temp file")
}

func syncBackupJSONDirectoryHandle(dir *os.File, _ string) error {
	return syncBackupDirectoryHandle(dir)
}

func syncBackupDirectory(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return mapBackupNoFollowError(err, "backup directory")
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func syncCreatedBackupDirectories(createdDirs []string, syncDir func(string) error) error {
	for _, createdDir := range createdDirs {
		if err := syncDir(filepath.Dir(createdDir)); err != nil {
			return err
		}
	}
	return nil
}

func readManifest(filePath string) (Manifest, error) {
	return readManifestWithExpectedSize(filePath, 0)
}

type regularFileEvidenceObservation struct {
	size        int64
	mode        os.FileMode
	modTime     int64
	changeToken string
	cacheable   bool
}

type manifestEvidenceSummary struct {
	JobID            string    `json:"job_id"`
	RunID            string    `json:"run_id"`
	JobConfigBinding string    `json:"job_config_binding"`
	StartedAt        time.Time `json:"started_at"`
	Source           string    `json:"source"`
	Destination      string    `json:"destination"`
	FileCount        int64     `json:"file_count"`
	TotalBytes       int64     `json:"total_bytes"`
	ConfigIncluded   bool      `json:"config_included"`
}

func manifestEvidenceDigest(ctx context.Context, filePath string, expectedSize int64, result *RunResult) (string, regularFileEvidenceObservation, error) {
	summary, err := manifestEvidenceSummaryData(result)
	if err != nil {
		return "", regularFileEvidenceObservation{}, err
	}
	return hashRegularFileEvidence(
		ctx,
		filePath,
		expectedSize,
		0,
		[]byte("mnemonas-manifest-evidence-v2\x00"),
		summary,
		"backup manifest",
	)
}

func manifestEvidenceDigestBytes(data []byte, result *RunResult) (string, error) {
	summary, err := manifestEvidenceSummaryData(result)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-manifest-evidence-v2\x00"))
	_, _ = hasher.Write(data)
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(summary)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func manifestEvidenceSummaryData(result *RunResult) ([]byte, error) {
	if result == nil {
		return nil, errors.New("backup result is required for manifest evidence")
	}
	summary, err := json.Marshal(manifestEvidenceSummary{
		JobID:            result.JobID,
		RunID:            result.ID,
		JobConfigBinding: result.JobConfigBinding,
		StartedAt:        result.StartedAt.UTC(),
		Source:           result.Source,
		Destination:      result.Destination,
		FileCount:        result.FileCount,
		TotalBytes:       result.TotalBytes,
		ConfigIncluded:   result.ConfigIncluded,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal manifest evidence summary: %w", err)
	}
	return summary, nil
}

func manifestSemanticDigest(manifest Manifest) (string, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal restore manifest evidence: %w", err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-manifest-semantic-v1\x00"))
	_, _ = hasher.Write(data)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func restoreManifestEvidenceDigestBytes(data []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-restore-manifest-evidence-v2\x00"))
	_, _ = hasher.Write(data)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func hashRegularFileEvidence(ctx context.Context, filePath string, expectedSize, maxSize int64, prefix, suffix []byte, label string) (string, regularFileEvidenceObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", regularFileEvidenceObservation{}, err
	}
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
	if err != nil {
		return "", regularFileEvidenceObservation{}, fmt.Errorf("open %s: %w", label, mapBackupRegularFileNoFollowError(err, label))
	}
	closeWithError := func(current error) error {
		if closeErr := file.Close(); closeErr != nil && current == nil {
			return fmt.Errorf("close %s: %w", label, closeErr)
		}
		return current
	}

	info, err := file.Stat()
	if err != nil {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("stat %s: %w", label, err))
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("%w: %s must be a regular file", ErrUnsafePath, label))
	}
	if info.Size() < 0 || expectedSize > 0 && info.Size() != expectedSize {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("%w: %s size mismatch", ErrUnsafePath, label))
	}
	if maxSize > 0 && info.Size() > maxSize {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("%w: %s exceeds the %d-byte evidence limit", ErrUnsafePath, label, maxSize))
	}
	size := info.Size()
	changeToken, cacheable := readinessFileChangeToken(info)
	hasher := sha256.New()
	_, _ = hasher.Write(prefix)
	written, err := io.CopyN(hasher, contextReader{ctx: ctx, reader: file}, size)
	if err != nil {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("read %s: copied %d of %d bytes: %w", label, written, size, err))
	}
	var extra [1]byte
	if err := ctx.Err(); err != nil {
		return "", regularFileEvidenceObservation{}, closeWithError(err)
	}
	if n, readErr := io.ReadFull(file, extra[:]); n != 0 || !errors.Is(readErr, io.EOF) {
		if readErr == nil {
			readErr = errors.New("file grew while reading")
		}
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("%w: %s size changed while reading: %v", ErrUnsafePath, label, readErr))
	}
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(suffix)

	after, err := file.Stat()
	if err != nil {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("stat %s after reading: %w", label, err))
	}
	afterToken, afterCacheable := readinessFileChangeToken(after)
	if after.Size() != size || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) ||
		cacheable != afterCacheable || cacheable && changeToken != afterToken {
		return "", regularFileEvidenceObservation{}, closeWithError(fmt.Errorf("%w: %s changed while reading", ErrUnsafePath, label))
	}
	if err := closeWithError(nil); err != nil {
		return "", regularFileEvidenceObservation{}, err
	}

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), regularFileEvidenceObservation{
		size:        size,
		mode:        info.Mode(),
		modTime:     info.ModTime().UnixNano(),
		changeToken: changeToken,
		cacheable:   cacheable,
	}, nil
}

func readManifestWithExpectedSize(filePath string, expectedSize int64) (Manifest, error) {
	_, manifest, err := readManifestDataWithExpectedSize(context.Background(), filePath, expectedSize)
	return manifest, err
}

func readManifestDataWithExpectedSize(ctx context.Context, filePath string, expectedSize int64) ([]byte, Manifest, error) {
	return readManifestDataWithExpectedSizeMode(ctx, filePath, expectedSize, false)
}

func readManifestDataWithExpectedSizeMode(ctx context.Context, filePath string, expectedSize int64, allowLegacyV1 bool) ([]byte, Manifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
	if err != nil {
		return nil, Manifest{}, fmt.Errorf("read backup manifest: %w", mapBackupRegularFileNoFollowError(err, "backup manifest"))
	}
	closeWithError := func(current error) error {
		if closeErr := file.Close(); closeErr != nil && current == nil {
			return fmt.Errorf("close backup manifest: %w", closeErr)
		}
		return current
	}
	info, err := file.Stat()
	if err != nil {
		return nil, Manifest{}, closeWithError(fmt.Errorf("stat backup manifest: %w", err))
	}
	if !info.Mode().IsRegular() {
		return nil, Manifest{}, closeWithError(fmt.Errorf("%w: backup manifest must be a regular file", ErrUnsafePath))
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return nil, Manifest{}, closeWithError(fmt.Errorf("%w: backup manifest size mismatch: got %d, want %d", ErrUnsafePath, info.Size(), expectedSize))
	}
	changeToken, cacheable := readinessFileChangeToken(info)
	reader := io.Reader(file)
	if expectedSize > 0 {
		if expectedSize == int64(^uint64(0)>>1) {
			return nil, Manifest{}, closeWithError(fmt.Errorf("%w: backup manifest is too large", ErrUnsafePath))
		}
		reader = io.LimitReader(file, expectedSize+1)
	}
	data, err := io.ReadAll(contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return nil, Manifest{}, closeWithError(fmt.Errorf("read backup manifest: %w", err))
	}
	if int64(len(data)) != info.Size() || expectedSize > 0 && int64(len(data)) != expectedSize {
		return nil, Manifest{}, closeWithError(fmt.Errorf("%w: backup manifest size changed while reading", ErrUnsafePath))
	}
	after, err := file.Stat()
	if err != nil {
		return nil, Manifest{}, closeWithError(fmt.Errorf("stat backup manifest after reading: %w", err))
	}
	afterToken, afterCacheable := readinessFileChangeToken(after)
	if after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) ||
		cacheable != afterCacheable || cacheable && changeToken != afterToken {
		return nil, Manifest{}, closeWithError(fmt.Errorf("%w: backup manifest changed while reading", ErrUnsafePath))
	}
	if err := closeWithError(nil); err != nil {
		return nil, Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, Manifest{}, fmt.Errorf("parse backup manifest: %w", err)
	}
	if manifest.Version == 1 && allowLegacyV1 {
		return data, manifest, nil
	}
	if manifest.Version != manifestVersion {
		return nil, Manifest{}, fmt.Errorf("unsupported backup manifest version: %d", manifest.Version)
	}
	if err := validateManifestEntries(manifest); err != nil {
		return nil, Manifest{}, err
	}
	return data, manifest, nil
}

func readTrustedRunManifest(ctx context.Context, job config.BackupJobConfig, result *RunResult) (Manifest, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if result == nil || result.ManifestSize <= 0 || result.ManifestDigest == "" {
		return Manifest{}, "", fmt.Errorf("%w: backup run is missing trusted manifest evidence", ErrUnsafePath)
	}
	data, manifest, err := readManifestDataWithExpectedSize(ctx, result.ManifestPath, result.ManifestSize)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, "", fmt.Errorf("%w: trusted backup manifest is missing", ErrUnsafePath)
		}
		return Manifest{}, "", err
	}
	digest, err := manifestEvidenceDigestBytes(data, result)
	if err != nil {
		return Manifest{}, "", err
	}
	if digest != result.ManifestDigest {
		return Manifest{}, "", fmt.Errorf("%w: trusted backup manifest digest mismatch", ErrUnsafePath)
	}
	if err := validateSnapshotManifestIdentity(job.ID, result.ID, manifest); err != nil {
		return Manifest{}, "", err
	}
	if filepath.Clean(manifest.Source) != filepath.Clean(result.Source) ||
		manifest.FileCount != result.FileCount || manifest.TotalBytes != result.TotalBytes ||
		(strings.TrimSpace(manifest.ConfigPath) != "") != result.ConfigIncluded {
		return Manifest{}, "", fmt.Errorf("%w: trusted backup manifest summary mismatch", ErrUnsafePath)
	}
	return manifest, restoreManifestEvidenceDigestBytes(data), nil
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
	if !backupPathMatchesExpectedLocation(snapshotPath, expectedSnapshotPath) {
		return fmt.Errorf("%w: backup state snapshot path does not match configured destination", ErrUnsafePath)
	}
	expectedManifestPath := filepath.Join(expectedSnapshotPath, manifestFileName)
	if !backupPathMatchesExpectedLocation(manifestPath, expectedManifestPath) {
		return fmt.Errorf("%w: backup state manifest path does not match configured destination", ErrUnsafePath)
	}
	return nil
}

func backupPathMatchesExpectedLocation(actualPath string, expectedPath string) bool {
	actualPath = strings.TrimSpace(actualPath)
	expectedPath = strings.TrimSpace(expectedPath)
	if actualPath == "" || expectedPath == "" {
		return false
	}
	if actualPath == expectedPath {
		return true
	}
	return sameExistingBackupPath(actualPath, expectedPath)
}

func sameExistingBackupPath(leftPath string, rightPath string) bool {
	if !isCanonicalAbsoluteBackupPath(leftPath) || !isCanonicalAbsoluteBackupPath(rightPath) {
		return false
	}
	if err := validatePathComponentsNoSymlink(leftPath, "recorded backup path"); err != nil {
		return false
	}
	if err := validatePathComponentsNoSymlink(rightPath, "current backup path"); err != nil {
		return false
	}
	leftInfo, leftErr := os.Lstat(leftPath)
	rightInfo, rightErr := os.Lstat(rightPath)
	if leftErr != nil || rightErr != nil || leftInfo.Mode()&os.ModeSymlink != 0 || rightInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

func isCanonicalAbsoluteBackupPath(candidate string) bool {
	return filepath.IsAbs(candidate) && filepath.Clean(candidate) == candidate && !restoreTargetHasDotSegment(candidate)
}

func validateManifestEntries(manifest Manifest) error {
	if manifest.FileCount < 0 {
		return fmt.Errorf("%w: backup manifest has negative file count", ErrUnsafePath)
	}
	if manifest.TotalBytes < 0 {
		return fmt.Errorf("%w: backup manifest has negative total bytes", ErrUnsafePath)
	}
	if len(manifest.Directories) == 0 || manifest.Directories[0].ArchivePath != "data" {
		return fmt.Errorf("%w: backup manifest must start its directory list with data", ErrUnsafePath)
	}
	directoryPaths := make(map[string]struct{}, len(manifest.Directories))
	previousDirectory := ""
	for _, directory := range manifest.Directories {
		archivePath := directory.ArchivePath
		if err := validateRestoreManifestDirectoryEntry(archivePath, directory.Mode); err != nil {
			return err
		}
		if previousDirectory != "" && previousDirectory >= archivePath {
			return fmt.Errorf("%w: backup manifest directories are not strictly sorted at %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
		if archivePath != "data" {
			parent := path.Dir(archivePath)
			if _, ok := directoryPaths[parent]; !ok {
				return fmt.Errorf("%w: backup manifest directory %q is missing parent %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath), cleanPreviewSamplePath(parent))
			}
		}
		directoryPaths[archivePath] = struct{}{}
		previousDirectory = archivePath
	}

	seenArchivePaths := make(map[string]struct{}, len(manifest.Entries))
	previousEntry := ""
	hasConfigEntry := false
	var totalBytes int64
	for _, entry := range manifest.Entries {
		archivePath := entry.ArchivePath
		if err := validateRestoreManifestFileEntry(archivePath, entry.Size); err != nil {
			return err
		}
		if err := validateRestoreManifestMode(archivePath, entry.Mode); err != nil {
			return err
		}
		if err := validateRestoreManifestDigest(archivePath, entry.SHA256); err != nil {
			return err
		}
		if previousEntry != "" && previousEntry >= archivePath {
			return fmt.Errorf("%w: backup manifest entries are not strictly sorted at %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
		if _, ok := directoryPaths[archivePath]; ok {
			return fmt.Errorf("%w: backup manifest path is both a file and directory: %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
		if strings.HasPrefix(archivePath, "data/") {
			parent := path.Dir(archivePath)
			if _, ok := directoryPaths[parent]; !ok {
				return fmt.Errorf("%w: backup manifest file %q is missing directory %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath), cleanPreviewSamplePath(parent))
			}
		}
		if archivePath == "config/config.toml" {
			hasConfigEntry = true
		}
		seenArchivePaths[archivePath] = struct{}{}
		previousEntry = archivePath
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
	if (strings.TrimSpace(manifest.ConfigPath) != "") != hasConfigEntry {
		return fmt.Errorf("%w: backup manifest config path and config entry disagree", ErrUnsafePath)
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

func manifestDirectoryMode(directory ManifestDirectory) os.FileMode {
	return os.FileMode(directory.Mode) & os.ModePerm
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
		default:
			return fmt.Errorf("%w: backup manifest has invalid sha256 for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
		}
	}
	return nil
}

func validateRestoreManifestDirectoryEntry(archivePath string, mode uint32) error {
	if err := validatePortableManifestPath(archivePath); err != nil {
		return err
	}
	if archivePath != "data" && !strings.HasPrefix(archivePath, "data/") {
		return fmt.Errorf("%w: backup manifest has unsupported directory path %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	if err := validateRestoreManifestMode(archivePath, mode); err != nil {
		return fmt.Errorf("%w: backup manifest has invalid directory mode for %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	return nil
}

func validatePortableManifestPath(archivePath string) error {
	if archivePath == "" || strings.ContainsRune(archivePath, '\\') || strings.IndexFunc(archivePath, unicode.IsControl) >= 0 || path.IsAbs(archivePath) || path.Clean(archivePath) != archivePath {
		return fmt.Errorf("%w: backup manifest has unsafe archive path %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
	}
	for _, segment := range strings.Split(archivePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: backup manifest has unsafe archive path %q", ErrUnsafePath, cleanPreviewSamplePath(archivePath))
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

type jobConfigEvidenceBindingInput struct {
	Version            int      `json:"version"`
	JobID              string   `json:"job_id"`
	Type               string   `json:"type"`
	Source             string   `json:"source"`
	Destination        string   `json:"destination"`
	Repository         string   `json:"repository"`
	Remote             string   `json:"remote"`
	Command            string   `json:"command"`
	PasswordFile       string   `json:"password_file"`
	PasswordFileState  string   `json:"password_file_state"`
	ConfigFile         string   `json:"config_file"`
	ConfigFileState    string   `json:"config_file_state"`
	CommandEnvironment string   `json:"command_environment"`
	ExtraArgs          []string `json:"extra_args"`
	IncludeConfig      bool     `json:"include_config"`
	IncludedConfigPath string   `json:"included_config_path"`
	VerifyAfterBackup  bool     `json:"verify_after_backup"`
	Exclude            []string `json:"exclude"`
}

// jobConfigEvidenceBinding fingerprints backup inputs, execution settings, and
// targets. Scheduling, retention, and individual snapshot IDs are intentionally
// excluded because they do not change whether existing restore evidence applies.
func jobConfigEvidenceBinding(job config.BackupJobConfig, storageRoot string, configPath string) (string, error) {
	return jobConfigEvidenceBindingContext(context.Background(), job, storageRoot, configPath)
}

func jobConfigEvidenceBindingContext(ctx context.Context, job config.BackupJobConfig, storageRoot string, configPath string) (string, error) {
	normalized := normalizeJob(job, storageRoot)
	if err := validateJobEvidenceInputs(normalized); err != nil {
		return "", err
	}
	passwordFileState, err := credentialFileEvidenceState(ctx, normalized.PasswordFile)
	if err != nil {
		return "", fmt.Errorf("capture password_file evidence: %w", err)
	}
	configFileState := ""
	if normalized.Type == JobTypeRclone {
		configFileState, err = rcloneConfigFileEvidenceState(ctx, normalized.ConfigFile, normalized.Remote)
	} else {
		configFileState, err = credentialFileEvidenceState(ctx, normalized.ConfigFile)
	}
	if err != nil {
		return "", fmt.Errorf("capture config_file evidence: %w", err)
	}
	environmentState, err := remoteCommandEnvironmentEvidenceState(normalized.Type, os.Environ())
	if err != nil {
		return "", fmt.Errorf("capture command environment evidence: %w", err)
	}
	return jobConfigEvidenceBindingWithStates(normalized, storageRoot, configPath, passwordFileState, configFileState, environmentState)
}

func jobConfigEvidenceBindingWithStates(normalized config.BackupJobConfig, storageRoot, configPath, passwordFileState, configFileState, commandEnvironment string) (string, error) {
	includedConfigPath := ""
	if normalized.IncludeConfig {
		includedConfigPath = strings.TrimSpace(configPath)
		if includedConfigPath != "" && !filepath.IsAbs(includedConfigPath) {
			if absolutePath, err := filepath.Abs(includedConfigPath); err == nil {
				includedConfigPath = absolutePath
			}
		}
		if includedConfigPath != "" {
			includedConfigPath = filepath.Clean(includedConfigPath)
		}
	}
	input := jobConfigEvidenceBindingInput{
		Version:            jobConfigEvidenceBindingVersion,
		JobID:              normalized.ID,
		Type:               normalized.Type,
		Source:             effectiveSource(normalized, storageRoot),
		Destination:        normalized.Destination,
		Repository:         normalized.Repository,
		Remote:             normalized.Remote,
		Command:            normalized.Command,
		PasswordFile:       normalized.PasswordFile,
		PasswordFileState:  passwordFileState,
		ConfigFile:         normalized.ConfigFile,
		ConfigFileState:    configFileState,
		CommandEnvironment: commandEnvironment,
		ExtraArgs:          cloneStringSlice(normalized.ExtraArgs),
		IncludeConfig:      normalized.IncludeConfig,
		IncludedConfigPath: includedConfigPath,
		VerifyAfterBackup:  normalized.VerifyAfterBackup,
		Exclude:            cloneStringSlice(normalized.Exclude),
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal backup job evidence binding: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateJobEvidenceInputs(job config.BackupJobConfig) error {
	switch job.Type {
	case JobTypeRestic:
		if strings.TrimSpace(job.PasswordFile) == "" {
			return fmt.Errorf("%w: restic password_file is required for evidence", ErrUnsafePath)
		}
		if err := validateResticRepositoryForm(job.Repository); err != nil {
			return err
		}
	case JobTypeRclone:
		if strings.TrimSpace(job.ConfigFile) == "" {
			return fmt.Errorf("%w: rclone config_file is required for evidence", ErrUnsafePath)
		}
		if _, err := rcloneRemoteName(job.Remote); err != nil {
			return err
		}
		for _, arg := range job.ExtraArgs {
			if strings.TrimSpace(arg) != "--fast-list" {
				return fmt.Errorf("%w: rclone extra_args currently allow only --fast-list", ErrUnsafePath)
			}
		}
	}
	for _, arg := range job.ExtraArgs {
		lower := strings.ToLower(strings.TrimSpace(arg))
		var forbidden []string
		switch job.Type {
		case JobTypeRestic:
			forbidden = []string{"-r", "--repo", "--repository-file", "--password-file", "--password-command"}
		case JobTypeRclone:
			forbidden = []string{"--config", "--password-command"}
		}
		for _, flag := range forbidden {
			if lower == flag || strings.HasPrefix(lower, flag+"=") || flag == "-r" && strings.HasPrefix(lower, "-r") {
				return fmt.Errorf("%w: extra_args cannot override backup identity option %q", ErrUnsafePath, flag)
			}
		}
	}
	return nil
}

func credentialFileEvidenceState(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest, observation, err := hashRegularFileEvidence(ctx, path, 0, credentialEvidenceSizeLimit, []byte("mnemonas-credential-evidence-v1\x00"), nil, "credential file")
	if err != nil {
		return "", err
	}
	return credentialEvidenceState(observation, digest), nil
}

func credentialEvidenceState(observation regularFileEvidenceObservation, digest string) string {
	_ = observation
	return "v4:" + digest
}

func credentialEvidenceStateForData(data []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-credential-evidence-v1\x00"))
	_, _ = hasher.Write(data)
	_, _ = hasher.Write([]byte{0})
	return "v4:sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func rcloneConfigFileEvidenceState(ctx context.Context, path, remote string) (string, error) {
	data, err := readCredentialFileData(ctx, path)
	if err != nil {
		return "", err
	}
	if err := validateRcloneConfigEvidenceData(data, remote); err != nil {
		return "", err
	}
	return credentialEvidenceStateForData(data), nil
}

func readCredentialFileData(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	file, err := rootio.OpenRegularFilePathNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("open credential file: %w", mapBackupRegularFileNoFollowError(err, "credential file"))
	}
	closeWithError := func(current error) error {
		if closeErr := file.Close(); closeErr != nil && current == nil {
			return fmt.Errorf("close credential file: %w", closeErr)
		}
		return current
	}
	info, err := file.Stat()
	if err != nil {
		return nil, closeWithError(fmt.Errorf("stat credential file: %w", err))
	}
	if info.Size() < 0 || info.Size() > credentialEvidenceSizeLimit {
		return nil, closeWithError(fmt.Errorf("%w: credential file exceeds the %d-byte evidence limit", ErrUnsafePath, credentialEvidenceSizeLimit))
	}
	changeToken, cacheable := readinessFileChangeToken(info)
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, credentialEvidenceSizeLimit+1))
	if err != nil {
		return nil, closeWithError(fmt.Errorf("read credential file: %w", err))
	}
	if int64(len(data)) != info.Size() {
		return nil, closeWithError(fmt.Errorf("%w: credential file size changed while reading", ErrUnsafePath))
	}
	after, err := file.Stat()
	if err != nil {
		return nil, closeWithError(fmt.Errorf("stat credential file after reading: %w", err))
	}
	afterToken, afterCacheable := readinessFileChangeToken(after)
	if after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) ||
		cacheable != afterCacheable || cacheable && changeToken != afterToken {
		return nil, closeWithError(fmt.Errorf("%w: credential file changed while reading", ErrUnsafePath))
	}
	if err := closeWithError(nil); err != nil {
		return nil, err
	}
	return data, nil
}

func validateRcloneConfigEvidenceData(data []byte, remote string) error {
	remoteName, err := rcloneRemoteName(remote)
	if err != nil {
		return err
	}
	if bytes.Contains(data, []byte("${")) {
		return fmt.Errorf("%w: rclone config cannot expand environment-dependent paths", ErrUnsafePath)
	}
	sections := make(map[string]map[string]string)
	currentSection := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), credentialEvidenceSizeLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if currentSection == "" {
				return fmt.Errorf("%w: rclone config contains an empty remote section", ErrUnsafePath)
			}
			if _, exists := sections[currentSection]; !exists {
				sections[currentSection] = make(map[string]string)
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || currentSection == "" {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		sections[currentSection][key] = value
		if key == "env_auth" && strings.EqualFold(value, "true") {
			return fmt.Errorf("%w: rclone config cannot enable env_auth", ErrUnsafePath)
		}
		if strings.Contains(key, "_file") || strings.Contains(key, "_path") ||
			strings.Contains(key, "command") || strings.Contains(key, "agent") || key == "ssh" {
			return fmt.Errorf("%w: rclone config option %q depends on an external runtime input", ErrUnsafePath, key)
		}
		if strings.Contains(key, "token") {
			return fmt.Errorf("%w: token-refreshing rclone configs require a managed writable credential store", ErrUnsafePath)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("parse rclone config evidence: %w", err)
	}
	remoteSection, exists := sections[remoteName]
	if !exists {
		return fmt.Errorf("%w: rclone remote %q is not defined in config_file", ErrUnsafePath, remoteName)
	}
	if strings.TrimSpace(remoteSection["type"]) == "" {
		return fmt.Errorf("%w: rclone remote %q has no type", ErrUnsafePath, remoteName)
	}
	return nil
}

func rcloneRemoteName(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	separator := strings.IndexByte(remote, ':')
	if separator <= 0 || strings.HasPrefix(remote, ":") {
		return "", fmt.Errorf("%w: rclone remote must reference a named config_file section", ErrUnsafePath)
	}
	name := strings.TrimSpace(remote[:separator])
	if name == "" || strings.ContainsAny(name, `/\\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: rclone remote name is invalid", ErrUnsafePath)
	}
	return name, nil
}

func validateResticRepositoryForm(repository string) error {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return fmt.Errorf("%w: restic repository is empty", ErrUnsafePath)
	}
	if strings.IndexFunc(repository, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: restic repository contains a control character", ErrUnsafePath)
	}
	if filepath.IsAbs(repository) {
		return nil
	}
	lower := strings.ToLower(repository)
	if !strings.HasPrefix(lower, "rest:") {
		return fmt.Errorf("%w: restic repository must be an absolute local path or an explicit REST server URL", ErrUnsafePath)
	}
	endpoint, err := url.Parse(repository[len("rest:"):])
	if err != nil || endpoint.Host == "" || endpoint.Fragment != "" ||
		!strings.EqualFold(endpoint.Scheme, "http") && !strings.EqualFold(endpoint.Scheme, "https") {
		return fmt.Errorf("%w: restic REST repository URL is invalid", ErrUnsafePath)
	}
	return nil
}

func validateResticRepositoryBoundary(repository, source, storageRoot string) error {
	if err := validateResticRepositoryForm(repository); err != nil {
		return err
	}
	repository = strings.TrimSpace(repository)
	if !filepath.IsAbs(repository) {
		return nil
	}
	repository = filepath.Clean(repository)
	for label, protected := range map[string]string{
		"backup source": source,
		"storage.root":  storageRoot,
	} {
		protected = strings.TrimSpace(protected)
		if protected == "" {
			continue
		}
		if !filepath.IsAbs(protected) {
			absoluteProtected, err := filepath.Abs(protected)
			if err != nil {
				return fmt.Errorf("resolve %s for restic repository validation: %w", label, err)
			}
			protected = absoluteProtected
		}
		if pathContainsOrEquals(protected, repository) {
			return fmt.Errorf("%w: restic repository must not be inside %s", ErrUnsafePath, label)
		}
	}
	return nil
}

type backupCommandEnvironmentContextKey struct{}

func (m *Manager) withJobCredentialSnapshot(
	ctx context.Context,
	job config.BackupJobConfig,
	expectedBinding string,
	run func(context.Context, config.BackupJobConfig) error,
) (binding string, operationErr error) {
	if run == nil {
		return "", errors.New("backup credential snapshot callback is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := normalizeJob(job, m.storageRoot)
	if err := validateJobEvidenceInputs(normalized); err != nil {
		return "", err
	}
	source := effectiveSource(normalized, m.storageRoot)
	if normalized.Type == JobTypeRestic {
		if err := validateResticRepositoryBoundary(normalized.Repository, source, m.storageRoot); err != nil {
			return "", err
		}
	}
	if err := validateRemoteCredentialFiles(normalized, source, m.storageRoot); err != nil {
		return "", err
	}
	tempRoot, err := resolvedCredentialSnapshotTempRoot()
	if err != nil {
		return "", err
	}
	if pathContainsOrEquals(source, tempRoot) || filepath.IsAbs(m.storageRoot) && pathContainsOrEquals(m.storageRoot, tempRoot) {
		return "", fmt.Errorf("%w: system credential snapshot directory overlaps backup data", ErrUnsafePath)
	}
	tempDir, err := os.MkdirTemp(tempRoot, credentialSnapshotDirPrefix)
	if err != nil {
		return "", fmt.Errorf("create private credential snapshot directory: %w", err)
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		_ = removeAllBackupPath(tempDir, "credential snapshot")
		return "", fmt.Errorf("secure private credential snapshot directory: %w", err)
	}
	defer func() {
		if cleanupErr := removeAllBackupPath(tempDir, "credential snapshot"); cleanupErr != nil {
			operationErr = errors.Join(operationErr, fmt.Errorf("remove private credential snapshot: %w", cleanupErr))
		}
	}()
	if pathContainsOrEquals(source, tempDir) || filepath.IsAbs(m.storageRoot) && pathContainsOrEquals(m.storageRoot, tempDir) {
		return "", fmt.Errorf("%w: private credential snapshot directory overlaps backup data", ErrUnsafePath)
	}

	executionJob := cloneJob(normalized)
	passwordFileState := ""
	configFileState := ""
	switch normalized.Type {
	case JobTypeRestic:
		snapshotPath := filepath.Join(tempDir, "password")
		passwordFileState, err = copyCredentialSnapshot(ctx, normalized.PasswordFile, snapshotPath)
		executionJob.PasswordFile = snapshotPath
	case JobTypeRclone:
		snapshotPath := filepath.Join(tempDir, "rclone.conf")
		configFileState, err = copyCredentialSnapshot(ctx, normalized.ConfigFile, snapshotPath)
		executionJob.ConfigFile = snapshotPath
	default:
		return "", ErrUnsupportedJobType
	}
	if err != nil {
		return "", err
	}
	if normalized.Type == JobTypeRclone {
		snapshotState, stateErr := rcloneConfigFileEvidenceState(ctx, executionJob.ConfigFile, executionJob.Remote)
		if stateErr != nil {
			return "", stateErr
		}
		if snapshotState != configFileState {
			return "", fmt.Errorf("%w: rclone config snapshot differs from its captured evidence", ErrUnsafePath)
		}
	}
	environmentState, err := remoteCommandEnvironmentEvidenceState(normalized.Type, os.Environ())
	if err != nil {
		return "", err
	}
	capturedBinding, err := jobConfigEvidenceBindingWithStates(normalized, m.storageRoot, m.configPath, passwordFileState, configFileState, environmentState)
	if err != nil {
		return "", err
	}
	if expectedBinding != "" && capturedBinding != expectedBinding {
		return "", fmt.Errorf("%w: credential file changed before its private snapshot was captured", ErrUnsafePath)
	}

	commandCtx := context.WithValue(ctx, backupCommandEnvironmentContextKey{}, sanitizedRemoteCommandEnvironment(normalized.Type, os.Environ(), tempDir))
	runErr := run(commandCtx, executionJob)
	binding = capturedBinding
	reconcileCtx := context.WithoutCancel(ctx)
	var snapshotState string
	var reconcileErr error
	if normalized.Type == JobTypeRclone {
		snapshotState, reconcileErr = rcloneConfigFileEvidenceState(reconcileCtx, executionJob.ConfigFile, executionJob.Remote)
		if reconcileErr == nil && snapshotState != configFileState {
			reconcileErr = fmt.Errorf("%w: rclone modified its read-only credential snapshot", ErrUnsafePath)
		}
	} else {
		snapshotState, reconcileErr = credentialFileEvidenceState(reconcileCtx, executionJob.PasswordFile)
		if reconcileErr == nil && snapshotState != passwordFileState {
			reconcileErr = fmt.Errorf("%w: restic modified its read-only credential snapshot", ErrUnsafePath)
		}
	}
	operationErr = errors.Join(runErr, reconcileErr)
	return binding, operationErr
}

func resolvedCredentialSnapshotTempRoot() (string, error) {
	tempRoot := strings.TrimSpace(os.TempDir())
	if tempRoot == "" {
		return "", fmt.Errorf("%w: system temporary directory is empty", ErrUnsafePath)
	}
	absoluteRoot, err := filepath.Abs(tempRoot)
	if err != nil {
		return "", fmt.Errorf("resolve system temporary directory: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("resolve system temporary directory symlinks: %w", err)
	}
	if err := validatePathComponentsNoSymlink(resolvedRoot, "system temporary directory"); err != nil {
		return "", err
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("stat system temporary directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: system temporary path is not a directory", ErrUnsafePath)
	}
	return filepath.Clean(resolvedRoot), nil
}

func copyCredentialSnapshot(ctx context.Context, sourcePath, snapshotPath string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source, err := rootio.OpenRegularFilePathNoFollow(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open credential file for snapshot: %w", mapBackupRegularFileNoFollowError(err, "credential file"))
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("stat credential file for snapshot: %w", err)
	}
	if info.Size() < 0 || info.Size() > credentialEvidenceSizeLimit {
		return "", fmt.Errorf("%w: credential file exceeds the %d-byte evidence limit", ErrUnsafePath, credentialEvidenceSizeLimit)
	}
	changeToken, cacheable := readinessFileChangeToken(info)

	snapshot, err := rootio.OpenFilePathNoFollow(snapshotPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create private credential snapshot: %w", mapBackupNoFollowError(err, "credential snapshot"))
	}
	cleanupSnapshot := true
	defer func() {
		if cleanupSnapshot {
			_ = snapshot.Close()
			_ = rootio.RemoveAllPathNoFollow(snapshotPath)
		}
	}()
	if err := snapshot.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure private credential snapshot: %w", err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("mnemonas-credential-evidence-v1\x00"))
	written, err := io.CopyN(io.MultiWriter(snapshot, hasher), contextReader{ctx: ctx, reader: source}, info.Size())
	if err != nil {
		return "", fmt.Errorf("copy credential snapshot: copied %d of %d bytes: %w", written, info.Size(), err)
	}
	var extra [1]byte
	if n, readErr := io.ReadFull(source, extra[:]); n != 0 || !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("%w: credential file size changed while snapshotting", ErrUnsafePath)
	}
	_, _ = hasher.Write([]byte{0})
	after, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("stat credential file after snapshot: %w", err)
	}
	afterToken, afterCacheable := readinessFileChangeToken(after)
	if after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) ||
		cacheable != afterCacheable || cacheable && changeToken != afterToken {
		return "", fmt.Errorf("%w: credential file changed while snapshotting", ErrUnsafePath)
	}
	if err := snapshot.Sync(); err != nil {
		return "", fmt.Errorf("sync private credential snapshot: %w", err)
	}
	if err := snapshot.Close(); err != nil {
		return "", fmt.Errorf("close private credential snapshot: %w", err)
	}
	cleanupSnapshot = false

	observation := regularFileEvidenceObservation{
		size:        info.Size(),
		mode:        info.Mode(),
		modTime:     info.ModTime().UnixNano(),
		changeToken: changeToken,
		cacheable:   cacheable,
	}
	digest := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	return credentialEvidenceState(observation, digest), nil
}

func sanitizedRemoteCommandEnvironment(jobType string, environment []string, privateDir string) []string {
	inherited := inheritedRemoteCommandEnvironment(environment)
	if jobType != JobTypeRestic && jobType != JobTypeRclone {
		return inherited
	}
	privateDir = filepath.Clean(privateDir)
	return append(inherited,
		"HOME="+privateDir,
		"TMPDIR="+privateDir,
		"TMP="+privateDir,
		"TEMP="+privateDir,
	)
}

func remoteCommandEnvironmentEvidenceState(jobType string, environment []string) (string, error) {
	if jobType != JobTypeRestic && jobType != JobTypeRclone {
		return "", nil
	}
	lookupEnvironment := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(name)) {
		case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC":
			lookupEnvironment = append(lookupEnvironment, entry)
		}
	}
	sort.Strings(lookupEnvironment)
	encoded, err := json.Marshal(lookupEnvironment)
	if err != nil {
		return "", fmt.Errorf("marshal remote command environment evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "v1:sha256:" + hex.EncodeToString(digest[:]), nil
}

func inheritedRemoteCommandEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(name))
		if upper == "PATH" || upper == "LANG" || upper == "LANGUAGE" || upper == "TZ" ||
			upper == "SYSTEMROOT" || upper == "WINDIR" || upper == "COMSPEC" || upper == "PATHEXT" ||
			strings.HasPrefix(upper, "LC_") {
			filtered = append(filtered, entry)
		}
	}
	sort.Strings(filtered)
	return filtered
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
		clone.JobConfigBinding = ""
		clone.ManifestSize = 0
		clone.ManifestDigest = ""
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
		clone.JobConfigBinding = ""
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
		clone.JobConfigBinding = ""
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
		clone.JobConfigBinding = ""
		clone.ManifestSize = 0
		clone.ManifestDigest = ""
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
	cmd.Env = backupCommandEnvironment(ctx)
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
	cmd.Env = backupCommandEnvironment(ctx)
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

func backupCommandEnvironment(ctx context.Context) []string {
	if ctx != nil {
		if environment, ok := ctx.Value(backupCommandEnvironmentContextKey{}).([]string); ok {
			return cloneStringSlice(environment)
		}
	}
	return os.Environ()
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
	if err := validateExplicitConfigRestoreNamespace(manifest, includeConfig); err != nil {
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

func validateExplicitConfigRestoreNamespace(manifest Manifest, includeConfig bool) error {
	if !includeConfig {
		return nil
	}
	hasConfig := false
	for _, entry := range manifest.Entries {
		if entry.ArchivePath == "config/config.toml" {
			hasConfig = true
			break
		}
	}
	if !hasConfig {
		return nil
	}
	const reservedArchivePath = "data/.mnemonas-restore"
	for _, directory := range manifest.Directories {
		if manifestPathWithinFold(directory.ArchivePath, reservedArchivePath) {
			return fmt.Errorf("%w: restored data conflicts with the reserved config restore directory", ErrUnsafePath)
		}
	}
	for _, entry := range manifest.Entries {
		if manifestPathWithinFold(entry.ArchivePath, reservedArchivePath) {
			return fmt.Errorf("%w: restored data conflicts with the reserved config restore directory", ErrUnsafePath)
		}
	}
	return nil
}

func manifestPathWithinFold(candidate string, root string) bool {
	if len(candidate) < len(root) || !strings.EqualFold(candidate[:len(root)], root) {
		return false
	}
	return len(candidate) == len(root) || candidate[len(root)] == '/'
}

func validateRestoreManifestFileEntry(archivePath string, size int64) error {
	if err := validatePortableManifestPath(archivePath); err != nil {
		return err
	}
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

func requireLocalManifestModeSemantics() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("%w: local backup manifest v2 requires POSIX rwx permission semantics", ErrUnsafePath)
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
	info, err := os.Lstat(destinationClean)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%w: destination must be a directory", ErrUnsafePath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat backup destination: %w", err)
	}
	return nil
}

func validateRemoteCredentialFiles(job config.BackupJobConfig, source, storageRoot string) error {
	switch job.Type {
	case JobTypeRestic:
		if strings.TrimSpace(job.PasswordFile) == "" {
			return fmt.Errorf("%w: password_file cannot be empty for restic", ErrUnsafePath)
		}
		return validateRemoteCredentialFile(job.PasswordFile, "password_file", source, storageRoot)
	case JobTypeRclone:
		if strings.TrimSpace(job.ConfigFile) == "" {
			return fmt.Errorf("%w: config_file cannot be empty for rclone", ErrUnsafePath)
		}
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

type restoreStagingTarget struct {
	Path     string
	Identity os.FileInfo
}

func createPartialRestoreTarget(targetPath, runID string) (restoreStagingTarget, error) {
	return createNamedRestoreTarget(targetPath, ".partial-"+runID)
}

func createNamedRestoreTarget(targetPath, suffix string) (restoreStagingTarget, error) {
	namedPath := targetPath + suffix
	if err := rootio.MkdirPathNoFollow(namedPath, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return restoreStagingTarget{}, ErrRestoreTargetExists
		}
		return restoreStagingTarget{}, fmt.Errorf("create restore staging target: %w", mapRestorePathError(err, "restore staging target path must not contain symlink"))
	}
	root, err := os.OpenRoot(namedPath)
	if err != nil {
		return restoreStagingTarget{}, fmt.Errorf("open restore staging target: %w", mapRestorePathError(err, "restore staging target changed after creation"))
	}
	identity, statErr := root.Stat(".")
	securityErr := validateBackupTargetLockDirectorySecurity(root, namedPath)
	if securityErr != nil {
		securityErr = errors.Join(ErrUnsafePath, securityErr)
	}
	closeErr := root.Close()
	stage := restoreStagingTarget{Path: namedPath, Identity: identity}
	if err := errors.Join(statErr, securityErr, closeErr); err != nil {
		cleanupErr := removeRestoreStagingTarget(stage, "restore staging target")
		return restoreStagingTarget{}, errors.Join(fmt.Errorf("validate restore staging target: %w", err), cleanupErr)
	}
	if err := verifyRestoreStagingIdentity(stage); err != nil {
		cleanupErr := removeRestoreStagingTarget(stage, "restore staging target")
		return restoreStagingTarget{}, errors.Join(err, cleanupErr)
	}
	return stage, nil
}

func verifyRestoreStagingIdentity(stage restoreStagingTarget) error {
	if strings.TrimSpace(stage.Path) == "" || stage.Identity == nil {
		return fmt.Errorf("%w: restore staging target identity is unavailable", ErrUnsafePath)
	}
	info, err := os.Lstat(stage.Path)
	if err != nil {
		return errors.Join(ErrUnsafePath, fmt.Errorf("verify restore staging target identity: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(stage.Identity, info) {
		return fmt.Errorf("%w: restore staging target identity changed", ErrUnsafePath)
	}
	return nil
}

func removeRestoreStagingTarget(stage restoreStagingTarget, label string) error {
	if err := verifyRestoreStagingIdentity(stage); err != nil {
		return err
	}
	return removeAllBackupPath(stage.Path, label)
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

func (m *Manager) appendLocalRestoreSnapshotWarnings(ctx context.Context, job config.BackupJobConfig, targetPath string, restore *RestoreResult, result *RestoreVerifyResult, warnings []string) ([]string, error) {
	if restore != nil {
		snapshotPath, manifestPath, manifest, err := localRestoreSnapshotManifest(job, restore)
		if err != nil {
			return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法对照恢复时使用的本地备份快照: %v", err)), nil
		}
		if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
			return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法校验恢复时使用的本地备份快照: %v", err)), nil
		}
		setRestoreVerifySnapshotReference(result, snapshotPath, manifestPath)
		return appendLocalRestoreSnapshotComparisonWarnings(ctx, targetPath, manifest, restore.ConfigRestored, restore.ConfigRestored, warnings)
	}

	snapshotPath, manifestPath, manifest, _, _, err := m.latestManifest(ctx, job)
	if err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法对照最新本地备份 manifest: %v", err)), nil
	}
	if _, _, err := verifyManifestFiles(ctx, snapshotPath, manifest); err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("无法校验最新本地备份快照: %v", err)), nil
	}
	setRestoreVerifySnapshotReference(result, snapshotPath, manifestPath)
	return appendLocalRestoreSnapshotComparisonWarnings(ctx, targetPath, manifest, true, false, warnings)
}

func setRestoreVerifySnapshotReference(result *RestoreVerifyResult, snapshotPath string, manifestPath string) {
	if result == nil {
		return
	}
	result.SnapshotPath = snapshotPath
	result.ManifestPath = manifestPath
}

func appendLocalRestoreSnapshotComparisonWarnings(ctx context.Context, targetPath string, manifest Manifest, compareConfig bool, requireConfig bool, warnings []string) ([]string, error) {
	var compareErr error
	warnings, compareErr = appendRestoreDirectoryComparisonWarnings(ctx, targetPath, manifest, compareConfig, warnings)
	if compareErr != nil {
		return warnings, compareErr
	}
	return appendRestoreFileComparisonWarnings(ctx, targetPath, manifest, compareConfig, requireConfig, warnings)
}

func (m *Manager) latestCompletedRestoreForTarget(jobID string, targetPath string) *RestoreResult {
	targetPath = strings.TrimSpace(targetPath)
	m.mu.Lock()
	state := m.state.Jobs[jobID]
	restores := append(cloneRestoreResultsRaw(state.RestoreHistory), cloneRestoreResultRaw(state.LastRestore))
	m.mu.Unlock()

	for _, restore := range restores {
		if restoreMatchesTargetSnapshot(restore, targetPath) {
			return restore
		}
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
	return restoreTargetPathsMatch(restore.TargetPath, targetPath)
}

func restoreTargetPathsMatch(leftPath string, rightPath string) bool {
	leftPath = strings.TrimSpace(leftPath)
	rightPath = strings.TrimSpace(rightPath)
	if leftPath == "" || rightPath == "" {
		return false
	}
	if !isCanonicalAbsoluteBackupPath(leftPath) || !isCanonicalAbsoluteBackupPath(rightPath) {
		return false
	}
	if leftPath == rightPath {
		return true
	}
	return sameExistingBackupPath(leftPath, rightPath)
}

func localRestoreSnapshotManifest(job config.BackupJobConfig, restore *RestoreResult) (string, string, Manifest, error) {
	if restore == nil || restore.ManifestSize <= 0 || strings.TrimSpace(restore.ManifestDigest) == "" {
		return "", "", Manifest{}, fmt.Errorf("%w: restore record is missing trusted manifest evidence", ErrUnsafePath)
	}
	snapshotPath := filepath.Clean(strings.TrimSpace(restore.SnapshotPath))
	if snapshotPath == "." || snapshotPath == "" {
		return "", "", Manifest{}, fmt.Errorf("%w: restore record is missing snapshot path", ErrUnsafePath)
	}
	snapshotName := filepath.Base(snapshotPath)
	manifestPath := filepath.Join(snapshotPath, manifestFileName)
	if filepath.Clean(strings.TrimSpace(restore.ManifestPath)) != filepath.Clean(manifestPath) {
		return "", "", Manifest{}, fmt.Errorf("%w: restore record manifest path does not match its snapshot", ErrUnsafePath)
	}
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
	data, manifest, err := readManifestDataWithExpectedSize(context.Background(), manifestPath, restore.ManifestSize)
	if err != nil {
		return "", "", Manifest{}, err
	}
	digest := restoreManifestEvidenceDigestBytes(data)
	if digest != restore.ManifestDigest {
		return "", "", Manifest{}, fmt.Errorf("%w: restored backup manifest digest mismatch", ErrUnsafePath)
	}
	if err := validateSnapshotManifestIdentity(job.ID, snapshotName, manifest); err != nil {
		return "", "", Manifest{}, err
	}
	return snapshotPath, manifestPath, manifest, nil
}

func appendRestoreDirectoryComparisonWarnings(ctx context.Context, targetPath string, manifest Manifest, compareConfig bool, warnings []string) ([]string, error) {
	if err := validateManifestEntries(manifest); err != nil {
		return warnings, err
	}
	expectedDirs := make(map[string]os.FileMode, len(manifest.Directories))
	if compareConfig {
		configDirPath := filepath.Join(targetPath, ".mnemonas-restore")
		if info, err := os.Lstat(configDirPath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标配置目录类型不匹配: .mnemonas-restore")
			} else if info.Mode()&specialPermissionBits != 0 || info.Mode().Perm() != 0700 {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标配置目录权限不匹配: .mnemonas-restore")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return warnings, fmt.Errorf("stat restore config directory: %w", err)
		}
	}
	for _, directory := range manifest.Directories {
		if err := ctx.Err(); err != nil {
			return warnings, err
		}
		relPath := "."
		if directory.ArchivePath != "data" {
			relPath = strings.TrimPrefix(directory.ArchivePath, "data/")
		}
		expectedMode := manifestDirectoryMode(directory)
		expectedDirs[relPath] = expectedMode
		targetDirPath := targetPath
		if relPath != "." {
			var err error
			targetDirPath, err = safeJoin(targetPath, relPath)
			if err != nil {
				return warnings, err
			}
		}
		targetInfo, err := os.Lstat(targetDirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if relPath == "." {
					warnings = appendRestoreVerificationWarning(warnings, "恢复目标根目录缺失")
				} else {
					warnings = appendRestoreVerificationWarning(warnings, "恢复目标缺少对照备份目录: "+relPath)
				}
				continue
			}
			return warnings, fmt.Errorf("stat restore target directory: %w", err)
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
			if relPath == "." {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标根目录类型不匹配")
			} else {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标目录类型不匹配: "+relPath)
			}
			continue
		}
		if targetInfo.Mode()&specialPermissionBits != 0 || targetInfo.Mode().Perm() != expectedMode {
			if relPath == "." {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标根目录权限不匹配")
			} else {
				warnings = appendRestoreVerificationWarning(warnings, "恢复目标目录权限不匹配: "+relPath)
			}
		}
	}
	err := filepath.WalkDir(targetPath, func(targetDirPath string, entry fs.DirEntry, walkErr error) error {
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
		if relPath == ".mnemonas-restore" {
			if _, restoredDataDirectory := expectedDirs[relPath]; !restoredDataDirectory {
				if compareConfig {
					return filepath.SkipDir
				}
			}
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

func appendRestoreFileComparisonWarnings(ctx context.Context, targetPath string, manifest Manifest, compareConfig bool, requireConfig bool, warnings []string) ([]string, error) {
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
	if compareConfig {
		warnings = appendRestoreConfigComparisonWarnings(ctx, targetPath, configEntry, requireConfig, warnings)
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
		if compareConfig && relPath == ".mnemonas-restore/config.toml" {
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

func appendRestoreConfigComparisonWarnings(ctx context.Context, targetPath string, configEntry *ManifestEntry, required bool, warnings []string) []string {
	configPath := filepath.Join(targetPath, ".mnemonas-restore", "config.toml")
	exists, err := regularFileExistsNoFollow(configPath)
	if err != nil {
		return appendRestoreVerificationWarning(warnings, fmt.Sprintf("恢复目标配置文件校验失败: %v", err))
	}
	if !exists {
		if required {
			return appendRestoreVerificationWarning(warnings, "恢复目标缺少对照备份配置文件: .mnemonas-restore/config.toml")
		}
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

func installRestoreTarget(stage restoreStagingTarget, targetPath string) error {
	return installRestoreTargetWithFinalMode(stage, targetPath, nil)
}

func installRestoreTargetWithFinalMode(stage restoreStagingTarget, targetPath string, finalMode *os.FileMode) error {
	if err := verifyRestoreStagingIdentity(stage); err != nil {
		return err
	}
	if err := validatePathComponentsNoSymlink(targetPath, "restore target"); err != nil {
		return err
	}
	if err := validatePathComponentsNoSymlink(stage.Path, "restore staging target"); err != nil {
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
	if err := verifyRestoreStagingIdentity(stage); err != nil {
		return err
	}
	var stageDir *os.File
	if finalMode != nil {
		var err error
		stageDir, err = rootio.OpenDirPathNoFollow(stage.Path)
		if err != nil {
			return fmt.Errorf("open restore staging target before install: %w", mapRestorePathError(err, "restore staging target changed before install"))
		}
		stageInfo, statErr := stageDir.Stat()
		if statErr != nil || !stageInfo.IsDir() || !os.SameFile(stage.Identity, stageInfo) || stageInfo.Mode()&specialPermissionBits != 0 || stageInfo.Mode().Perm() != 0o700 {
			_ = stageDir.Close()
			return errors.Join(ErrUnsafePath, statErr, fmt.Errorf("restore staging target identity or private mode changed before install"))
		}
	}
	if err := rootio.ReplaceEmptyDirPathNoFollow(stage.Path, targetPath); err != nil {
		if stageDir != nil {
			_ = stageDir.Close()
		}
		if errors.Is(err, os.ErrExist) {
			return ErrRestoreTargetExists
		}
		return fmt.Errorf("install restore target: %w", mapRestorePathError(err, "restore target changed before install"))
	}
	if stageDir != nil {
		if err := stageDir.Chmod(finalMode.Perm()); err != nil {
			_ = stageDir.Close()
			return fmt.Errorf("set installed restore target mode: %w", err)
		}
		installedInfo, statErr := stageDir.Stat()
		pathInfo, pathErr := os.Lstat(targetPath)
		closeErr := stageDir.Close()
		if statErr != nil || pathErr != nil || !installedInfo.IsDir() || !pathInfo.IsDir() ||
			!os.SameFile(stage.Identity, installedInfo) || !os.SameFile(stage.Identity, pathInfo) ||
			installedInfo.Mode()&specialPermissionBits != 0 || installedInfo.Mode().Perm() != finalMode.Perm() ||
			pathInfo.Mode()&specialPermissionBits != 0 || pathInfo.Mode().Perm() != finalMode.Perm() {
			return errors.Join(ErrUnsafePath, statErr, pathErr, closeErr, fmt.Errorf("installed restore target identity or mode mismatch"))
		}
		if closeErr != nil {
			return fmt.Errorf("close installed restore target: %w", closeErr)
		}
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
	if err := validateExplicitConfigRestoreNamespace(manifest, includeConfig); err != nil {
		return 0, 0, false, "", err
	}

	var fileCount int64
	var verifiedBytes int64
	var configRestored bool
	var configPath string
	directoryModes, err := restoreManifestDirectories(ctx, targetPath, manifest.Directories)
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
	if err := applyNestedDirectoryModesNoFollow(targetPath, directoryModes, "restore target"); err != nil {
		return fileCount, verifiedBytes, configRestored, configPath, err
	}
	if err := verifyExplicitRestoreTreeContentsWithRootMode(ctx, targetPath, manifest, includeConfig, 0o700); err != nil {
		return fileCount, verifiedBytes, configRestored, configPath, err
	}
	return fileCount, verifiedBytes, configRestored, configPath, nil
}

func restoreManifestDirectories(ctx context.Context, targetRoot string, directories []ManifestDirectory) ([]directoryMode, error) {
	directoryModes := make([]directoryMode, 0, len(directories))
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateRestoreManifestDirectoryEntry(directory.ArchivePath, directory.Mode); err != nil {
			return nil, err
		}
		relPath := "."
		if directory.ArchivePath != "data" {
			relPath = filepath.FromSlash(strings.TrimPrefix(directory.ArchivePath, "data/"))
		}
		destinationPath := filepath.Join(targetRoot, relPath)
		mode := manifestDirectoryMode(directory)
		if err := rootio.MkdirAllPathNoFollow(destinationPath, writableDirectoryMode(mode)); err != nil {
			return nil, fmt.Errorf("create restored directory %s: %w", relPath, mapRestorePathError(err, "restore target path must not contain symlink"))
		}
		directoryModes = append(directoryModes, directoryMode{RelPath: relPath, Mode: mode})
	}
	return directoryModes, nil
}

type directoryMode struct {
	RelPath string
	Mode    os.FileMode
}

func writableDirectoryMode(mode os.FileMode) os.FileMode {
	return mode.Perm() | 0700
}

func validateSourceDirectoryMode(relPath string, mode os.FileMode) error {
	if mode&specialPermissionBits != 0 {
		return fmt.Errorf("%w: backup source directory has unsupported special permission bits: %s", ErrUnsafePath, cleanPreviewSamplePath(filepath.ToSlash(relPath)))
	}
	return nil
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

func applyNestedDirectoryModesNoFollow(root string, directoryModes []directoryMode, label string) error {
	nestedModes := make([]directoryMode, 0, len(directoryModes))
	for _, mode := range directoryModes {
		if filepath.Clean(mode.RelPath) != "." {
			nestedModes = append(nestedModes, mode)
		}
	}
	return applyDirectoryModesNoFollow(root, nestedModes, label)
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
	info, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return err
	}
	if !info.IsDir() || info.Mode()&specialPermissionBits != 0 || info.Mode().Perm() != mode.Perm() {
		_ = dir.Close()
		return fmt.Errorf("%w: %s directory mode could not be preserved", ErrUnsafePath, label)
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
