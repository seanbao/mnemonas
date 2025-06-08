package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

type recordingNotifier struct {
	mu     sync.Mutex
	events []NotificationEvent
}

func (n *recordingNotifier) NotifyBackupEvent(_ context.Context, event NotificationEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	return nil
}

func (n *recordingNotifier) Events() []NotificationEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NotificationEvent(nil), n.events...)
}

func TestManager_RunJobAndRestoreDrill(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")

	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "hello backup")
	mustWriteFile(t, filepath.Join(source, "cache", "skip.txt"), "skip me")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")

	manager, err := NewManager(ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:                "home",
			Name:              "Home backup",
			Type:              JobTypeLocal,
			Source:            source,
			Destination:       destination,
			IncludeConfig:     true,
			VerifyAfterBackup: true,
			Exclude:           []string{"cache"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if jobs := manager.ListJobs(); len(jobs) != 1 || jobs[0].Command != "" {
		t.Fatalf("local job command should be hidden in API view: %+v", jobs)
	}

	result, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("RunJob status = %q, want %q", result.Status, StatusCompleted)
	}
	if result.FileCount != 2 {
		t.Fatalf("RunJob file count = %d, want 2", result.FileCount)
	}
	assertFileContent(t, filepath.Join(result.SnapshotPath, "data", "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(result.SnapshotPath, "config", "config.toml"), "[server]\nport = 8080\n")
	if _, err := os.Stat(filepath.Join(result.SnapshotPath, "data", "cache", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded file stat error = %v, want not exist", err)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted {
		t.Fatalf("RunRestoreDrill status = %q, want %q", drill.Status, StatusCompleted)
	}
	if drill.FileCount != result.FileCount {
		t.Fatalf("RunRestoreDrill file count = %d, want %d", drill.FileCount, result.FileCount)
	}
	assertFileContent(t, filepath.Join(drill.RestoredPath, "data", "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(drill.RestoredPath, "config", "config.toml"), "[server]\nport = 8080\n")

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath:    restoreTarget,
		IncludeConfig: true,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.FileCount != result.FileCount || !preview.ConfigAvailable || !preview.ConfigIncluded {
		t.Fatalf("unexpected RunRestorePreview result: %+v", preview)
	}
	if len(preview.SamplePaths) != 2 || preview.SamplePaths[0] != "docs/note.txt" || preview.SamplePaths[1] != ".mnemonas-restore/config.toml" {
		t.Fatalf("preview sample paths = %#v", preview.SamplePaths)
	}
	if len(preview.PreflightChecks) == 0 || len(preview.CutoverChecklist) == 0 || len(preview.RollbackChecklist) == 0 {
		t.Fatalf("preview missing preflight or checklists: %+v", preview)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{
		TargetPath:    restoreTarget,
		IncludeConfig: true,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted {
		t.Fatalf("RunRestore status = %q, want %q", restore.Status, StatusCompleted)
	}
	if restore.TargetPath != restoreTarget || restore.FileCount != result.FileCount || !restore.ConfigRestored {
		t.Fatalf("unexpected RunRestore result: %+v", restore)
	}
	if len(restore.PreflightChecks) == 0 || len(restore.CutoverChecklist) == 0 || len(restore.RollbackChecklist) == 0 {
		t.Fatalf("restore missing persisted preflight or checklists: %+v", restore)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml"), "[server]\nport = 8080\n")
	if _, err := os.Stat(filepath.Join(restoreTarget, "cache", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded restored file stat error = %v, want not exist", err)
	}
	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected RunRestoreVerify result: %+v", verify)
	}
	if !verify.ConfigFound || verify.ConfigPath != filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml") {
		t.Fatalf("RunRestoreVerify config result = %+v", verify)
	}
	if verify.LooksLikeStorageRoot {
		t.Fatalf("RunRestoreVerify should not classify this test source as a storage root: %+v", verify)
	}

	reloaded, err := NewManager(ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	jobs := reloaded.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRun == nil || jobs[0].LastRestoreDrill == nil || jobs[0].LastRestore == nil || jobs[0].LastRestoreVerify == nil {
		t.Fatalf("reloaded jobs missing persisted status: %+v", jobs)
	}
	if jobs[0].LastRestore.TargetPath != restoreTarget || jobs[0].LastRestore.Status != StatusCompleted {
		t.Fatalf("reloaded last restore = %+v, want completed restore to %s", jobs[0].LastRestore, restoreTarget)
	}
	if len(jobs[0].RestoreHistory) != 1 || jobs[0].RestoreHistory[0].ID != restore.ID {
		t.Fatalf("reloaded restore history = %+v, want restore %s", jobs[0].RestoreHistory, restore.ID)
	}
	if jobs[0].LastRestoreVerify.TargetPath != restoreTarget || jobs[0].LastRestoreVerify.Status != StatusCompleted {
		t.Fatalf("reloaded last restore verify = %+v, want completed verify for %s", jobs[0].LastRestoreVerify, restoreTarget)
	}
	report, err := reloaded.BuildRestoreReport("home")
	if err != nil {
		t.Fatalf("BuildRestoreReport() error: %v", err)
	}
	if report.Job.ID != "home" || report.LastRestore == nil || report.LastRestoreVerify == nil || len(report.Findings) == 0 {
		t.Fatalf("unexpected restore report: %+v", report)
	}
}

func TestManager_RunRestoreDrillWithoutSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
	if !errors.Is(err, ErrNoSnapshots) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrNoSnapshots", err)
	}
}

func TestManager_RunRestoreRejectsUnsafeTarget(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	sourceTarget := filepath.Join(source, "restored")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: sourceTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() source target error = %v, want ErrUnsafePath", err)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || job.LastRestore.TargetPath != sourceTarget {
		t.Fatalf("failed restore was not persisted: %+v", job.LastRestore)
	}
	if len(job.RestoreHistory) != 1 || job.RestoreHistory[0].Status != StatusFailed {
		t.Fatalf("failed restore history = %+v, want one failed restore", job.RestoreHistory)
	}

	existingTarget := filepath.Join(tmpDir, "existing")
	mustWriteFile(t, filepath.Join(existingTarget, "old.txt"), "old")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: existingTarget})
	if !errors.Is(err, ErrRestoreTargetExists) {
		t.Fatalf("RunRestore() existing target error = %v, want ErrRestoreTargetExists", err)
	}
	job, err = manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() after existing target error: %v", err)
	}
	if len(job.RestoreHistory) != 2 || job.RestoreHistory[0].TargetPath != existingTarget {
		t.Fatalf("restore history after second failure = %+v, want latest existing target failure", job.RestoreHistory)
	}

	missingTarget := filepath.Join(tmpDir, "missing")
	_, err = manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: missingTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreVerify() missing target error = %v, want ErrUnsafePath", err)
	}
	job, err = manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() after verify error: %v", err)
	}
	if len(job.RestoreHistory) != 2 {
		t.Fatalf("restore verify should not write restore history: %+v", job.RestoreHistory)
	}
	if job.LastRestoreVerify == nil || job.LastRestoreVerify.Status != StatusFailed || job.LastRestoreVerify.TargetPath != missingTarget {
		t.Fatalf("failed restore verify was not persisted separately: %+v", job.LastRestoreVerify)
	}
}

func TestManager_RunRestoreBlocksFailedPreflight(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "large.bin"), strings.Repeat("x", 32))

	oldAvailableBytesFunc := restoreAvailableBytesFunc
	restoreAvailableBytesFunc = func(string) (int64, error) {
		return 1, nil
	}
	defer func() {
		restoreAvailableBytesFunc = oldAvailableBytesFunc
	}()

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if firstFailedRestorePreflight(preview.PreflightChecks) == nil {
		t.Fatalf("preview preflight did not fail capacity check: %+v", preview.PreflightChecks)
	}

	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || firstFailedRestorePreflight(job.LastRestore.PreflightChecks) == nil {
		t.Fatalf("failed preflight restore was not persisted: %+v", job.LastRestore)
	}
}

func TestManager_RunBatchRestorePreviewAndRestore(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "batch restore")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreA := filepath.Join(tmpDir, "restore-a")
	preview, err := manager.RunBatchRestorePreview(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreA},
			{JobID: "home", TargetPath: filepath.Join(restoreA, "nested")},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || !preview.Warning || len(preview.Items) != 2 {
		t.Fatalf("unexpected batch preview outcome: %+v", preview)
	}
	if preview.Items[0].Status != StatusCompleted || preview.Items[0].Preview == nil {
		t.Fatalf("first batch preview item = %+v, want completed preview", preview.Items[0])
	}
	if preview.Items[1].Status != StatusFailed || !strings.Contains(preview.Items[1].ErrorMessage, "conflicts") {
		t.Fatalf("second batch preview item = %+v, want target conflict", preview.Items[1])
	}

	restoreB := filepath.Join(tmpDir, "restore-b")
	restore, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreA},
			{JobID: "home", TargetPath: restoreB},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || len(restore.Items) != 2 {
		t.Fatalf("unexpected batch restore outcome: %+v", restore)
	}
	for _, item := range restore.Items {
		if item.Status != StatusCompleted || item.Restore == nil || item.Verify == nil {
			t.Fatalf("batch restore item = %+v, want completed restore and verify", item)
		}
		assertFileContent(t, filepath.Join(item.TargetPath, "docs", "note.txt"), "batch restore")
	}

	restoreC := filepath.Join(tmpDir, "restore-c")
	partial, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreC},
			{JobID: "home", TargetPath: filepath.Join(restoreC, "nested")},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() partial error: %v", err)
	}
	if partial.Status != StatusCompleted || !partial.Warning || len(partial.Items) != 2 {
		t.Fatalf("unexpected partial batch restore outcome: %+v", partial)
	}
	if partial.Items[0].Status != StatusCompleted || partial.Items[0].Restore == nil || partial.Items[0].Verify == nil {
		t.Fatalf("first partial batch item = %+v, want completed restore and verify", partial.Items[0])
	}
	if partial.Items[1].Status != StatusFailed || !strings.Contains(partial.Items[1].ErrorMessage, "conflicts") {
		t.Fatalf("second partial batch item = %+v, want target conflict", partial.Items[1])
	}
	assertFileContent(t, filepath.Join(restoreC, "docs", "note.txt"), "batch restore")
}

func TestManager_JobViewRestoreDrillAndRetentionHealth(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{
			{
				ID:                     "local-unbounded",
				Name:                   "Local unbounded",
				Type:                   JobTypeLocal,
				Source:                 source,
				Destination:            filepath.Join(tmpDir, "backups"),
				ScheduleInterval:       24 * time.Hour,
				RestoreDrillStaleAfter: 24 * time.Hour,
			},
			{
				ID:              "restic-retained",
				Name:            "Restic retained",
				Type:            JobTypeRestic,
				Source:          source,
				Repository:      "rest:http://backup.example/repo",
				PasswordFile:    filepath.Join(tmpDir, "restic-password"),
				RetentionPolicy: "external: restic forget --keep-daily 7 --prune",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }

	lastBackupFinished := now.Add(-2 * time.Hour)
	staleDrillFinished := now.Add(-25 * time.Hour)
	manager.mu.Lock()
	manager.state.Jobs["local-unbounded"] = JobState{
		LastSuccessfulRun: &RunResult{
			ID:         "backup",
			JobID:      "local-unbounded",
			Status:     StatusCompleted,
			StartedAt:  lastBackupFinished.Add(-time.Minute),
			FinishedAt: &lastBackupFinished,
		},
		LastRestoreDrill: &RestoreDrillResult{
			ID:         "drill",
			JobID:      "local-unbounded",
			Status:     StatusCompleted,
			StartedAt:  staleDrillFinished.Add(-time.Minute),
			FinishedAt: &staleDrillFinished,
		},
	}
	manager.mu.Unlock()

	jobs := manager.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("job count = %d, want 2", len(jobs))
	}
	localJob := jobs[0]
	if localJob.ID != "local-unbounded" || localJob.RetentionStatus != "warning" || localJob.RestoreDrillStatus != "stale" {
		t.Fatalf("unexpected local job policy state: %+v", localJob)
	}
	if localJob.RestoreDrillStaleAfter != "24h0m0s" {
		t.Fatalf("restore drill stale after = %q, want 24h0m0s", localJob.RestoreDrillStaleAfter)
	}
	remoteJob := jobs[1]
	if remoteJob.ID != "restic-retained" || remoteJob.RetentionStatus != "ok" || remoteJob.RestoreDrillStatus != "due" {
		t.Fatalf("unexpected remote job policy state: %+v", remoteJob)
	}
	if remoteJob.RetentionPolicy != "external: restic forget --keep-daily 7 --prune" {
		t.Fatalf("retention policy = %q", remoteJob.RetentionPolicy)
	}
	if remoteJob.RestoreDrillStaleAfter != "720h0m0s" {
		t.Fatalf("default restore drill stale after = %q, want 720h0m0s", remoteJob.RestoreDrillStaleAfter)
	}
}

func TestManager_RunJobRejectsDestinationInsideSource(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(source, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunJobRejectsSourceSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
	}
}

func TestManager_RunDueJobsRunsScheduledBackup(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "scheduled")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:               "home",
			Name:             "Home backup",
			Type:             JobTypeLocal,
			Source:           source,
			Destination:      destination,
			ScheduleInterval: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 {
		t.Fatalf("RunDueJobs() result count = %d, want 1", len(results))
	}
	if results[0].Error != "" {
		t.Fatalf("RunDueJobs() error = %s", results[0].Error)
	}
	if results[0].Result == nil || results[0].Result.Trigger != "scheduled" || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() result = %+v", results[0].Result)
	}

	results = manager.RunDueJobs(context.Background())
	if len(results) != 0 {
		t.Fatalf("RunDueJobs() immediate second result count = %d, want 0", len(results))
	}

	now = now.Add(time.Hour)
	results = manager.RunDueJobs(context.Background())
	if len(results) != 1 {
		t.Fatalf("RunDueJobs() after interval result count = %d, want 1", len(results))
	}
}

func TestManager_RunDueJobsRespectsScheduleWindow(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "windowed")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                  "home",
			Name:                "Home backup",
			Type:                JobTypeLocal,
			Source:              source,
			Destination:         destination,
			ScheduleInterval:    time.Hour,
			ScheduleWindowStart: "02:00",
			ScheduleWindowEnd:   "03:00",
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 1, 30, 0, 0, time.Local)
	manager.now = func() time.Time { return now }

	if results := manager.RunDueJobs(context.Background()); len(results) != 0 {
		t.Fatalf("RunDueJobs() before window result count = %d, want 0", len(results))
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].ScheduleWindowStart != "02:00" || jobs[0].ScheduleWindowEnd != "03:00" {
		t.Fatalf("unexpected job window view: %+v", jobs)
	}
	if jobs[0].NextRunAt == nil || jobs[0].NextRunAt.In(time.Local).Hour() != 2 {
		t.Fatalf("next run before window = %v, want 02:00 local", jobs[0].NextRunAt)
	}

	now = time.Date(2026, 5, 9, 2, 15, 0, 0, time.Local)
	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 || results[0].Result == nil || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() inside window result = %+v", results)
	}

	now = time.Date(2026, 5, 9, 3, 30, 0, 0, time.Local)
	if results := manager.RunDueJobs(context.Background()); len(results) != 0 {
		t.Fatalf("RunDueJobs() after window result count = %d, want 0", len(results))
	}
	jobs = manager.ListJobs()
	if jobs[0].NextRunAt == nil || jobs[0].NextRunAt.In(time.Local).Day() != 10 || jobs[0].NextRunAt.In(time.Local).Hour() != 2 {
		t.Fatalf("next run after window = %v, want next day 02:00 local", jobs[0].NextRunAt)
	}
}

func TestScheduleWindowSupportsCrossMidnight(t *testing.T) {
	job := config.BackupJobConfig{
		ScheduleWindowStart: "22:00",
		ScheduleWindowEnd:   "06:00",
	}
	for _, tt := range []struct {
		name string
		when time.Time
		want bool
	}{
		{name: "late evening", when: time.Date(2026, 5, 9, 23, 30, 0, 0, time.Local), want: true},
		{name: "early morning", when: time.Date(2026, 5, 10, 5, 30, 0, 0, time.Local), want: true},
		{name: "midday", when: time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithinScheduleWindow(job, tt.when); got != tt.want {
				t.Fatalf("isWithinScheduleWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_RunJobPrunesOldSnapshots(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:           "home",
			Name:         "Home backup",
			Type:         JobTypeLocal,
			Source:       source,
			Destination:  destination,
			MaxSnapshots: 2,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	second, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	third, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("third RunJob() error: %v", err)
	}
	if third.PrunedSnapshots != 1 {
		t.Fatalf("third pruned snapshots = %d, want 1", third.PrunedSnapshots)
	}
	if _, err := os.Stat(first.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first snapshot stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(second.SnapshotPath); err != nil {
		t.Fatalf("second snapshot stat error = %v", err)
	}
	if _, err := os.Stat(third.SnapshotPath); err != nil {
		t.Fatalf("third snapshot stat error = %v", err)
	}
}

func TestManager_RunRetentionCheckLocalReportsSnapshotRange(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:           "home",
			Name:         "Home backup",
			Type:         JobTypeLocal,
			Source:       source,
			Destination:  destination,
			MaxSnapshots: 3,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Hour)
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || check.Warning || check.SnapshotCount != 2 {
		t.Fatalf("unexpected retention check: %+v", check)
	}
	if check.OldestSnapshotAt == nil || check.LatestSnapshotAt == nil || !check.LatestSnapshotAt.After(*check.OldestSnapshotAt) {
		t.Fatalf("snapshot range not populated: %+v", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.ID != check.ID || job.RetentionStatus != "ok" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckResticParsesSnapshotsAndWarnsMissingPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath, _ := newRecordingResticCommand(t, source)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteFile(t, passwordFile, "secret")
	notifier := &recordingNotifier{}

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:           "restic-remote",
			Name:         "Restic remote",
			Type:         JobTypeRestic,
			Source:       source,
			Repository:   "rest:http://backup.example/repo",
			Command:      commandPath,
			PasswordFile: passwordFile,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "restic-remote")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || !check.Warning || check.SnapshotCount != 1 {
		t.Fatalf("unexpected restic retention check: %+v", check)
	}
	if check.OldestSnapshotAt == nil || !strings.Contains(strings.Join(check.Warnings, "\n"), "retention_policy") {
		t.Fatalf("restic retention warnings/range = %+v", check)
	}
	events := notifier.Events()
	if len(events) != 1 || events[0].Type != NotificationTypeRetention || events[0].Level != NotificationLevelWarning {
		t.Fatalf("retention notification = %+v", events)
	}
}

func TestManager_RunRetentionCheckRcloneParsesRemoteFiles(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	configFile := filepath.Join(tmpDir, "rclone.conf")
	commandPath, _ := newRecordingRcloneCommand(t)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "rclone")
	mustWriteFile(t, configFile, "[remote]\ntype = local\n")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:              "rclone-remote",
			Name:            "Rclone remote",
			Type:            JobTypeRclone,
			Source:          source,
			Remote:          "backup:mnemonas/source",
			Command:         commandPath,
			ConfigFile:      configFile,
			RetentionPolicy: "external: cloud lifecycle keeps 30 versions",
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "rclone-remote")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || check.Warning || check.FileCount != 1 || check.TotalBytes != 6 {
		t.Fatalf("unexpected rclone retention check: %+v", check)
	}
	if check.LatestSnapshotAt == nil {
		t.Fatalf("rclone latest timestamp not parsed: %+v", check)
	}
}

func TestManager_RunResticBackupUsesExternalCommand(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath, logPath := newRecordingResticCommand(t, source)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteFile(t, passwordFile, "secret")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                "restic-remote",
			Name:              "Restic remote",
			Type:              JobTypeRestic,
			Source:            source,
			Repository:        "rest:http://backup.example/repo",
			Command:           commandPath,
			PasswordFile:      passwordFile,
			VerifyAfterBackup: true,
			MaxSnapshots:      1,
			Exclude:           []string{"cache/**"},
			ExtraArgs:         []string{"--compression", "max"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if jobs := manager.ListJobs(); len(jobs) != 1 || jobs[0].Command != commandPath || jobs[0].Repository != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restic job view: %+v", jobs)
	}

	result, err := manager.RunJob(context.Background(), "restic-remote")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("RunJob status = %q, want %q", result.Status, StatusCompleted)
	}
	if result.Destination != "rest:http://backup.example/repo" || result.PrunedSnapshots != 0 || !result.Warning {
		t.Fatalf("unexpected RunJob result: %+v", result)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "retention_policy") {
		t.Fatalf("RunJob retention warnings = %#v, want retention_policy warning", result.Warnings)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "restic-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "restic-restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "restic-remote", RestorePreviewOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore preview result: %+v", preview)
	}
	if preview.FileCount != 1 || preview.TotalBytes != int64(len("restic restored")) || len(preview.SamplePaths) != 1 || preview.SamplePaths[0] != "docs/note.txt" {
		t.Fatalf("unexpected restore preview metrics: %+v", preview)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	restore, err := manager.RunRestore(context.Background(), "restic-remote", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || restore.TargetPath != restoreTarget || restore.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore result: %+v", restore)
	}
	if restore.FileCount != 1 || restore.VerifiedBytes != int64(len("restic restored")) {
		t.Fatalf("unexpected restore metrics: %+v", restore)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "restic restored")
	verify, err := manager.RunRestoreVerify(context.Background(), "restic-remote", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected restore verify result: %+v", verify)
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRestore == nil || jobs[0].LastRestore.ID != restore.ID || len(jobs[0].RestoreHistory) != 1 {
		t.Fatalf("restic restore audit was not recorded: %+v", jobs)
	}

	calls := readCommandCalls(t, logPath)
	if len(calls) != 7 {
		t.Fatalf("command call count = %d, want 7: %#v", len(calls), calls)
	}
	assertCommandArgs(t, calls[0], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"backup", source,
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--exclude", "cache/**",
		"--compression", "max",
	})
	assertCommandArgs(t, calls[1], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"check",
	})
	assertCommandArgs(t, calls[2], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"snapshots",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
	})
	assertCommandArgs(t, calls[3], calls[1])
	assertCommandArgs(t, calls[4], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"ls", "latest",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--path", source,
		"--exclude", "cache/**",
	})
	assertCommandArgs(t, calls[5], calls[4])
	assertCommandArgs(t, calls[6], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"restore", "latest",
		"--target", restoreTarget + ".restic-" + restore.ID,
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--path", source,
		"--exclude", "cache/**",
	})
}

func TestManager_RunRcloneBackupUsesExternalCommand(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	configFile := filepath.Join(tmpDir, "rclone.conf")
	commandPath, logPath := newRecordingRcloneCommand(t)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "rclone")
	mustWriteFile(t, configFile, "[remote]\ntype = local\n")

	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                "rclone-remote",
			Name:              "Rclone remote",
			Type:              JobTypeRclone,
			Source:            source,
			Remote:            "backup:mnemonas/source",
			Command:           commandPath,
			ConfigFile:        configFile,
			VerifyAfterBackup: true,
			Exclude:           []string{"tmp/**"},
			ExtraArgs:         []string{"--fast-list"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	result, err := manager.RunJob(context.Background(), "rclone-remote")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted || result.Destination != "backup:mnemonas/source" {
		t.Fatalf("unexpected RunJob result: %+v", result)
	}
	if !result.Warning || len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "retention_policy") {
		t.Fatalf("RunJob retention warnings = %#v, want retention_policy warning", result.Warnings)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "rclone-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "rclone-restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "rclone-remote", RestorePreviewOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore preview result: %+v", preview)
	}
	if preview.FileCount != 1 || preview.TotalBytes != 6 || len(preview.SamplePaths) != 1 || preview.SamplePaths[0] != "docs/note.txt" {
		t.Fatalf("unexpected restore preview metrics: %+v", preview)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	restore, err := manager.RunRestore(context.Background(), "rclone-remote", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || restore.TargetPath != restoreTarget || restore.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore result: %+v", restore)
	}
	if restore.FileCount != 1 || restore.VerifiedBytes != int64(len("rclone")) {
		t.Fatalf("unexpected restore metrics: %+v", restore)
	}
	if _, err := os.Stat(restoreTarget); err != nil {
		t.Fatalf("restored target stat error: %v", err)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "rclone")
	verify, err := manager.RunRestoreVerify(context.Background(), "rclone-remote", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected restore verify result: %+v", verify)
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRestore == nil || jobs[0].LastRestore.ID != restore.ID || len(jobs[0].RestoreHistory) != 1 {
		t.Fatalf("rclone restore audit was not recorded: %+v", jobs)
	}

	calls := readCommandCalls(t, logPath)
	if len(calls) != 8 {
		t.Fatalf("command call count = %d, want 8: %#v", len(calls), calls)
	}
	assertCommandArgs(t, calls[0], []string{
		"--config", configFile,
		"sync", source, "backup:mnemonas/source",
		"--create-empty-src-dirs",
		"--exclude", "tmp/**",
		"--fast-list",
	})
	assertCommandArgs(t, calls[1], []string{
		"--config", configFile,
		"check", source, "backup:mnemonas/source",
		"--one-way",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[2], []string{
		"--config", configFile,
		"lsjson", "backup:mnemonas/source",
		"--recursive",
		"--files-only",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[3], calls[1])
	assertCommandArgs(t, calls[4], []string{
		"--config", configFile,
		"lsjson", "backup:mnemonas/source",
		"--recursive",
		"--files-only",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[5], calls[4])
	assertCommandArgs(t, calls[6], []string{
		"--config", configFile,
		"copy", "backup:mnemonas/source", restoreTarget + ".partial-" + restore.ID,
		"--create-empty-src-dirs",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[7], []string{
		"--config", configFile,
		"check", "backup:mnemonas/source", restoreTarget + ".partial-" + restore.ID,
		"--one-way",
		"--exclude", "tmp/**",
	})
}

func TestRunExternalCommandIncludesStderrOnFailure(t *testing.T) {
	commandPath, _ := newRecordingCommand(t, 23, "remote denied")

	err := runExternalCommand(context.Background(), commandPath, "sync")
	if err == nil {
		t.Fatal("runExternalCommand() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "remote denied") {
		t.Fatalf("runExternalCommand() error = %v, want stderr detail", err)
	}
}

func TestManager_DisabledJobCannotRun(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
			Disabled:    true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrJobDisabled) {
		t.Fatalf("RunJob() error = %v, want ErrJobDisabled", err)
	}
}

func TestManager_RunJobFailureNotifies(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	notifier := &recordingNotifier{}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeBackupRun || event.Level != NotificationLevelCritical {
		t.Fatalf("notification type/level = %s/%s, want backup_run/critical", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusFailed || event.ErrorMessage == "" {
		t.Fatalf("unexpected notification event: %+v", event)
	}
}

func TestManager_RestoreDrillFailureNotifies(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	notifier := &recordingNotifier{}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
	if !errors.Is(err, ErrNoSnapshots) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrNoSnapshots", err)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeRestoreDrill || event.Level != NotificationLevelCritical {
		t.Fatalf("notification type/level = %s/%s, want backup_restore_drill/critical", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusFailed || event.ErrorMessage == "" {
		t.Fatalf("unexpected notification event: %+v", event)
	}
}

func TestManager_RestoreDrillReminderNotifiesWhenDueAndStale(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore reminder")

	now := time.Date(2026, 5, 10, 2, 0, 0, 0, time.UTC)
	notifier := &recordingNotifier{}
	manager, err := NewManager(ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:                     "home",
			Name:                   "Home backup",
			Type:                   JobTypeLocal,
			Source:                 source,
			Destination:            destination,
			MaxSnapshots:           7,
			RestoreDrillStaleAfter: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }

	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if events := manager.SendRestoreDrillReminders(context.Background()); len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() immediate events = %+v, want none", events)
	}

	now = now.Add(2 * time.Hour)
	events := manager.SendRestoreDrillReminders(context.Background())
	if len(events) != 1 {
		t.Fatalf("SendRestoreDrillReminders() due event count = %d, want 1", len(events))
	}
	if events[0].Type != NotificationTypeRestoreDrill || events[0].Level != NotificationLevelWarning || events[0].Status != "due" {
		t.Fatalf("due reminder event = %+v", events[0])
	}
	if events[0].Trigger != NotificationTriggerReminder || events[0].LastSuccessfulRunAt == nil || events[0].StaleAfter == "" {
		t.Fatalf("due reminder missing metadata: %+v", events[0])
	}
	if len(notifier.Events()) != 1 {
		t.Fatalf("notifier event count after due reminder = %d, want 1", len(notifier.Events()))
	}

	now = now.Add(time.Hour)
	if events := manager.SendRestoreDrillReminders(context.Background()); len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() cooldown events = %+v, want none", events)
	}

	now = now.Add(24 * time.Hour)
	if events := manager.SendRestoreDrillReminders(context.Background()); len(events) != 1 || events[0].Status != "due" {
		t.Fatalf("SendRestoreDrillReminders() after cooldown = %+v, want due reminder", events)
	}

	now = now.Add(time.Minute)
	if _, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{}); err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if events := manager.SendRestoreDrillReminders(context.Background()); len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() after fresh drill = %+v, want none", events)
	}

	now = now.Add(25 * time.Hour)
	events = manager.SendRestoreDrillReminders(context.Background())
	if len(events) != 1 {
		t.Fatalf("SendRestoreDrillReminders() stale event count = %d, want 1", len(events))
	}
	if events[0].Status != "stale" || events[0].RunID == "" || events[0].LastRestoreDrillAt == nil {
		t.Fatalf("stale reminder event = %+v", events[0])
	}
}

func TestSafeJoinRejectsTraversalManifestPaths(t *testing.T) {
	for _, archivePath := range []string{"../secret", "data/../secret", "data//secret", "./data/secret"} {
		if _, err := safeJoin(t.TempDir(), archivePath); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("safeJoin(%q) error = %v, want ErrUnsafePath", archivePath, err)
		}
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll(%s) error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, string(data), want)
	}
}

func newRecordingCommand(t *testing.T, exitCode int, stderr string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "mnemonas-test-command")
	logPath := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n"
	if stderr != "" {
		script += "printf '%s\\n' " + shellQuote(stderr) + " >&2\n"
	}
	if exitCode != 0 {
		script += "exit " + strconv.Itoa(exitCode) + "\n"
	}
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func newRecordingResticCommand(t *testing.T, source string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "mnemonas-test-restic")
	logPath := filepath.Join(dir, "args.log")
	sourceRel := filepath.Clean(source)
	if volume := filepath.VolumeName(sourceRel); volume != "" {
		sourceRel = strings.TrimPrefix(sourceRel, volume)
	}
	sourceRel = strings.TrimPrefix(sourceRel, string(filepath.Separator))
	sourceRel = filepath.ToSlash(sourceRel)
	restoredDir := sourceRel + "/docs"
	restoredFile := restoredDir + "/note.txt"
	resticPreviewPath := filepath.ToSlash(filepath.Join(source, "docs", "note.txt"))
	snapshotJSON := `[{"time":"2026-05-09T10:00:00Z","id":"abc123","tags":["mnemonas","job:restic-remote"]}]`
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n" +
		"mode=''\n" +
		"restore_target=''\n" +
		"prev=''\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = 'ls' ]; then mode='ls'; fi\n" +
		"  if [ \"$arg\" = 'snapshots' ]; then mode='snapshots'; fi\n" +
		"  if [ \"$prev\" = '--target' ]; then restore_target=$arg; fi\n" +
		"  prev=$arg\n" +
		"done\n" +
		"if [ \"$mode\" = 'snapshots' ]; then\n" +
		"  printf '%s\\n' " + shellQuote(snapshotJSON) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$mode\" = 'ls' ]; then\n" +
		"  printf '%s\\n' " + shellQuote(`{"path":"`+resticPreviewPath+`","type":"file","size":15}`) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ -n \"$restore_target\" ]; then\n" +
		"  mkdir -p \"$restore_target\"/" + shellQuote(restoredDir) + "\n" +
		"  printf '%s' 'restic restored' > \"$restore_target\"/" + shellQuote(restoredFile) + "\n" +
		"fi\n"
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func newRecordingRcloneCommand(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "mnemonas-test-rclone")
	logPath := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n" +
		"mode=''\n" +
		"copy_target=''\n" +
		"after_copy=0\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = 'lsjson' ]; then\n" +
		"    printf '%s\\n' " + shellQuote(`[{"Path":"docs/note.txt","Size":6,"IsDir":false,"ModTime":"2026-05-09T10:00:00Z"}]`) + "\n" +
		"    exit 0\n" +
		"  fi\n" +
		"  if [ \"$arg\" = 'copy' ]; then mode='copy'; after_copy=1; continue; fi\n" +
		"  if [ \"$after_copy\" = '1' ]; then after_copy=2; continue; fi\n" +
		"  if [ \"$after_copy\" = '2' ]; then copy_target=$arg; after_copy=0; fi\n" +
		"done\n" +
		"if [ \"$mode\" = 'copy' ] && [ -n \"$copy_target\" ]; then\n" +
		"  mkdir -p \"$copy_target/docs\"\n" +
		"  printf '%s' 'rclone' > \"$copy_target/docs/note.txt\"\n" +
		"fi\n"
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func readCommandCalls(t *testing.T, logPath string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(command log) error: %v", err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "__CALL__" {
			calls = append(calls, []string{})
			continue
		}
		if len(calls) == 0 {
			t.Fatalf("command log has argument before call separator: %q", line)
		}
		calls[len(calls)-1] = append(calls[len(calls)-1], line)
	}
	return calls
}

func assertCommandArgs(t *testing.T, got []string, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command args = %#v, want %#v", got, want)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
