package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
	"github.com/seanbao/mnemonas/internal/rootio"
)

// ReadinessSnapshot summarizes backup and restore evidence needed by setup readiness checks.
type ReadinessSnapshot struct {
	Available                  bool       `json:"available"`
	EnabledJobCount            int        `json:"enabled_job_count"`
	EnabledScheduledJobCount   int        `json:"enabled_scheduled_job_count"`
	LastSuccessfulBackupAt     *time.Time `json:"last_successful_backup_at,omitempty"`
	HasCurrentHealthyBackup    bool       `json:"has_current_healthy_backup"`
	LastValidRestoreEvidenceAt *time.Time `json:"last_valid_restore_evidence_at,omitempty"`
	HasCurrentRestoreEvidence  bool       `json:"has_current_restore_evidence"`
}

// ReadinessSnapshot returns a read-only point-in-time view of backup readiness.
func (m *Manager) ReadinessSnapshot() ReadinessSnapshot {
	return m.ReadinessSnapshotContext(context.Background())
}

// ReadinessSnapshotContext returns a read-only point-in-time view of backup
// readiness and stops waiting when ctx is cancelled.
func (m *Manager) ReadinessSnapshotContext(ctx context.Context) ReadinessSnapshot {
	if m == nil {
		return ReadinessSnapshot{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.readinessGate == nil {
		m.readinessGate = make(chan struct{}, 1)
	}
	gate := m.readinessGate
	m.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ReadinessSnapshot{}
	}

	m.mu.Lock()
	if !m.statePersistenceHealthy {
		m.mu.Unlock()
		return ReadinessSnapshot{}
	}
	now := m.now().UTC()
	storageRoot := m.storageRoot
	configPath := m.configPath
	entries := make([]readinessJobEntry, 0, len(m.jobs))
	for id, job := range m.jobs {
		entries = append(entries, readinessJobEntry{
			job:   cloneJob(job),
			state: cloneReadinessJobState(m.state.Jobs[id]),
		})
	}
	m.mu.Unlock()

	snapshot := ReadinessSnapshot{Available: true}
	for _, entry := range entries {
		job := entry.job
		if job.Disabled {
			continue
		}

		snapshot.EnabledJobCount++
		if job.ScheduleInterval > 0 {
			snapshot.EnabledScheduledJobCount++
		}

		state := entry.state
		expectedBinding, bindingErr := jobConfigEvidenceBindingContext(ctx, job, storageRoot, configPath)
		if bindingErr != nil {
			continue
		}
		expectedSource := effectiveSource(job, storageRoot)
		successfulAt, hasSuccessfulCompletion := completedRunAt(state.LastSuccessfulRun)
		hasSuccessfulEvidence := hasSuccessfulCompletion && !successfulAt.After(now) &&
			m.readinessSuccessfulRunMatchesJob(ctx, job, expectedBinding, expectedSource, state.LastSuccessfulRun)
		if hasSuccessfulEvidence {
			snapshot.LastSuccessfulBackupAt = laterReadinessTime(snapshot.LastSuccessfulBackupAt, successfulAt)
			latestRunFailed := state.LastRun != nil && state.LastRun.Status == StatusFailed && readinessRunBindingMatchesJob(job, expectedBinding, expectedSource, state.LastRun)
			if !latestRunFailed && readinessTimeIsCurrent(now, successfulAt, effectiveStaleAfter(job)) {
				snapshot.HasCurrentHealthyBackup = true
			}
		}

		for _, drill := range restoreDrillEvidenceHistory(state) {
			if !readinessRestoreDrillBindingMatchesJob(job, expectedBinding, drill) {
				continue
			}
			completedAt, ok := completedRestoreDrillAt(drill)
			if !ok || completedAt.After(now) {
				continue
			}
			snapshot.LastValidRestoreEvidenceAt = laterReadinessTime(snapshot.LastValidRestoreEvidenceAt, completedAt)
		}

		if completedAt, ok := completedRestoreDrillAt(state.LastRestoreDrill); ok && !completedAt.After(now) && readinessRestoreDrillBindingMatchesJob(job, expectedBinding, state.LastRestoreDrill) {
			hasRequiredBackup := job.Type != JobTypeLocal
			if hasSuccessfulEvidence {
				hasRequiredBackup = true
			}
			if hasRequiredBackup && readinessTimeIsCurrent(now, completedAt, effectiveRestoreDrillStaleAfter(job)) {
				snapshot.HasCurrentRestoreEvidence = true
			}
		}

		if verify := validMatchingRestoreVerify(job, expectedBinding, state); verify != nil {
			completedAt := restoreVerifyFinishedAt(verify)
			if !completedAt.IsZero() && !completedAt.After(now) {
				snapshot.LastValidRestoreEvidenceAt = laterReadinessTime(snapshot.LastValidRestoreEvidenceAt, completedAt)
				if readinessTimeIsCurrent(now, completedAt, effectiveRestoreDrillStaleAfter(job)) {
					snapshot.HasCurrentRestoreEvidence = true
				}
			}
		}
	}

	return snapshot
}

type readinessJobEntry struct {
	job   config.BackupJobConfig
	state JobState
}

type readinessManifestCacheEntry struct {
	path             string
	changeToken      string
	resultID         string
	binding          string
	source           string
	size             int64
	modTime          int64
	fileCount        int64
	totalBytes       int64
	configIncluded   bool
	manifestFileSize int64
	manifestDigest   string
	valid            bool
}

func cloneReadinessJobState(state JobState) JobState {
	return JobState{
		LastRun:             cloneRunResultRaw(state.LastRun),
		LastSuccessfulRun:   cloneRunResultRaw(state.LastSuccessfulRun),
		LastRestoreDrill:    cloneRestoreDrillResultRaw(state.LastRestoreDrill),
		RestoreDrillHistory: cloneRestoreDrillResultsRaw(state.RestoreDrillHistory),
		LastRestore:         cloneRestoreResultRaw(state.LastRestore),
		LastRestoreVerify:   cloneRestoreVerifyResultRaw(state.LastRestoreVerify),
	}
}

func (m *Manager) readinessSuccessfulRunMatchesJob(ctx context.Context, job config.BackupJobConfig, expectedBinding string, expectedSource string, result *RunResult) bool {
	if !readinessRunBindingMatchesJob(job, expectedBinding, expectedSource, result) || result.Status != StatusCompleted {
		return false
	}
	if job.Type != JobTypeLocal {
		return true
	}
	if err := validateSnapshotManifestLocation(job, result.ID, result.SnapshotPath, result.ManifestPath); err != nil {
		return false
	}
	if err := validatePathComponentsNoSymlink(result.ManifestPath, "backup manifest"); err != nil {
		return false
	}
	manifestFile, err := rootio.OpenRegularFilePathNoFollow(result.ManifestPath)
	if err != nil {
		m.readinessManifestCache.Delete(job.ID)
		return false
	}
	info, statErr := manifestFile.Stat()
	closeErr := manifestFile.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		m.readinessManifestCache.Delete(job.ID)
		return false
	}
	if result.ManifestSize <= 0 || result.ManifestDigest == "" || info.Size() != result.ManifestSize {
		m.readinessManifestCache.Delete(job.ID)
		return false
	}
	changeToken, cacheable := readinessFileChangeToken(info)
	signature := readinessManifestCacheEntry{
		path:             result.ManifestPath,
		changeToken:      changeToken,
		resultID:         result.ID,
		binding:          result.JobConfigBinding,
		source:           result.Source,
		size:             info.Size(),
		modTime:          info.ModTime().UnixNano(),
		fileCount:        result.FileCount,
		totalBytes:       result.TotalBytes,
		configIncluded:   result.ConfigIncluded,
		manifestFileSize: result.ManifestSize,
		manifestDigest:   result.ManifestDigest,
	}
	if cacheable {
		if cached, ok := m.readinessManifestCache.Load(job.ID); ok {
			entry := cached.(readinessManifestCacheEntry)
			if entry.path == signature.path &&
				entry.changeToken == signature.changeToken &&
				entry.resultID == signature.resultID &&
				entry.binding == signature.binding &&
				entry.source == signature.source &&
				entry.size == signature.size &&
				entry.modTime == signature.modTime &&
				entry.fileCount == signature.fileCount &&
				entry.totalBytes == signature.totalBytes &&
				entry.configIncluded == signature.configIncluded &&
				entry.manifestFileSize == signature.manifestFileSize &&
				entry.manifestDigest == signature.manifestDigest {
				return entry.valid
			}
		}
	} else {
		m.readinessManifestCache.Delete(job.ID)
	}
	digest, observation, err := manifestEvidenceDigest(ctx, result.ManifestPath, result.ManifestSize, result)
	valid := err == nil && digest == result.ManifestDigest
	if err == nil {
		signature.size = observation.size
		signature.modTime = observation.modTime
		signature.changeToken = observation.changeToken
		cacheable = observation.cacheable
	}
	if cacheable {
		signature.valid = valid
		m.readinessManifestCache.Store(job.ID, signature)
	} else {
		m.readinessManifestCache.Delete(job.ID)
	}
	return valid
}

func readinessRunBindingMatchesJob(job config.BackupJobConfig, expectedBinding string, expectedSource string, result *RunResult) bool {
	if result == nil || !readinessEvidenceBindingMatchesJob(job, expectedBinding, result.JobID, result.JobConfigBinding) {
		return false
	}
	expectedSource = strings.TrimSpace(expectedSource)
	expectedDestination := strings.TrimSpace(backupTarget(job))
	actualSource := strings.TrimSpace(result.Source)
	actualDestination := strings.TrimSpace(result.Destination)
	if job.Type == JobTypeLocal {
		return filepath.Clean(actualSource) == filepath.Clean(expectedSource) &&
			filepath.Clean(actualDestination) == filepath.Clean(expectedDestination)
	}
	return actualSource == expectedSource && actualDestination == expectedDestination
}

func readinessRestoreDrillBindingMatchesJob(job config.BackupJobConfig, expectedBinding string, result *RestoreDrillResult) bool {
	return result != nil && readinessEvidenceBindingMatchesJob(job, expectedBinding, result.JobID, result.JobConfigBinding)
}

func readinessEvidenceBindingMatchesJob(job config.BackupJobConfig, expectedBinding string, jobID string, binding string) bool {
	return jobID == job.ID && binding != "" && binding == expectedBinding
}

func completedRunAt(result *RunResult) (time.Time, bool) {
	if result == nil || result.Status != StatusCompleted || result.FinishedAt == nil || result.StartedAt.IsZero() {
		return time.Time{}, false
	}
	completedAt := result.FinishedAt.UTC()
	return completedAt, !completedAt.IsZero() && !completedAt.Before(result.StartedAt.UTC())
}

func completedRestoreDrillAt(result *RestoreDrillResult) (time.Time, bool) {
	if result == nil || result.Status != StatusCompleted || result.FinishedAt == nil || result.StartedAt.IsZero() {
		return time.Time{}, false
	}
	completedAt := result.FinishedAt.UTC()
	return completedAt, !completedAt.IsZero() && !completedAt.Before(result.StartedAt.UTC())
}

func restoreDrillEvidenceHistory(state JobState) []*RestoreDrillResult {
	if len(state.RestoreDrillHistory) == 0 {
		return []*RestoreDrillResult{state.LastRestoreDrill}
	}
	return append([]*RestoreDrillResult{state.LastRestoreDrill}, state.RestoreDrillHistory...)
}

func validMatchingRestoreVerify(job config.BackupJobConfig, expectedBinding string, state JobState) *RestoreVerifyResult {
	restore := state.LastRestore
	verify := state.LastRestoreVerify
	if restore == nil || verify == nil ||
		restore.FinishedAt == nil || restore.StartedAt.IsZero() || restore.FinishedAt.Before(restore.StartedAt) ||
		verify.FinishedAt == nil || verify.StartedAt.IsZero() || verify.FinishedAt.Before(verify.StartedAt) ||
		!readinessEvidenceBindingMatchesJob(job, expectedBinding, restore.JobID, restore.JobConfigBinding) ||
		!readinessEvidenceBindingMatchesJob(job, expectedBinding, verify.JobID, verify.JobConfigBinding) {
		return nil
	}
	verify = matchingRestoreVerifyForRestore(restore, verify)
	if verify == nil || verify.Status != StatusCompleted || len(verify.Warnings) > 0 || strings.TrimSpace(verify.ErrorMessage) != "" {
		return nil
	}
	return verify
}

func restoreVerifyFinishedAt(result *RestoreVerifyResult) time.Time {
	if result == nil || result.FinishedAt == nil {
		return time.Time{}
	}
	return result.FinishedAt.UTC()
}

func readinessTimeIsCurrent(now, completedAt time.Time, staleAfter time.Duration) bool {
	return !completedAt.After(now) && (staleAfter <= 0 || !now.After(completedAt.Add(staleAfter)))
}

func laterReadinessTime(current *time.Time, candidate time.Time) *time.Time {
	candidate = candidate.UTC()
	if candidate.IsZero() {
		return current
	}
	if current != nil && !candidate.After(*current) {
		return current
	}
	value := candidate
	return &value
}
