package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

func TestManager_RunJobFailsClosedWhenRunningStateCannotPersist(t *testing.T) {
	tests := []struct {
		name       string
		buildJob   func(t *testing.T, source, destination string) (config.BackupJobConfig, string)
		assertIdle func(t *testing.T, destination, commandLog string)
	}{
		{
			name: "local snapshot",
			buildJob: func(_ *testing.T, source, destination string) (config.BackupJobConfig, string) {
				return config.BackupJobConfig{
					ID:          "home",
					Name:        "Home backup",
					Type:        JobTypeLocal,
					Source:      source,
					Destination: destination,
				}, ""
			},
			assertIdle: func(t *testing.T, destination, _ string) {
				t.Helper()
				if _, err := os.Stat(filepath.Join(destination, "home", "snapshots")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("snapshot root stat error = %v, want not exist", err)
				}
			},
		},
		{
			name: "restic command",
			buildJob: func(t *testing.T, source, _ string) (config.BackupJobConfig, string) {
				commandPath, commandLog := newRecordingCommand(t, 0, "")
				passwordFile := filepath.Join(secureBackupTestTempDir(t), "restic.pass")
				mustWriteFile(t, passwordFile, "secret")
				return config.BackupJobConfig{
					ID:           "home",
					Name:         "Home backup",
					Type:         JobTypeRestic,
					Source:       source,
					Repository:   "rest:http://backup.example/repo",
					Command:      commandPath,
					PasswordFile: passwordFile,
				}, commandLog
			},
			assertIdle: func(t *testing.T, _ string, commandLog string) {
				t.Helper()
				if data, err := os.ReadFile(commandLog); err == nil && len(data) != 0 {
					t.Fatalf("external command log = %q, want no calls", data)
				} else if err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("read external command log: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			destination := filepath.Join(tmpDir, "backups")
			mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
			job, commandLog := tt.buildJob(t, source, destination)
			notifier := &recordingNotifier{}
			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state"),
				StorageRoot: source,
				Jobs:        []config.BackupJobConfig{job},
				Notifier:    notifier,
			})
			if err != nil {
				t.Fatalf("NewManager() error: %v", err)
			}

			originalWriteBackupStateFile := writeBackupStateFile
			t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
			persistErr := errors.New("injected running state persistence failure")
			writeBackupStateFile = func(*backupStateLock, string, any, os.FileMode) error { return persistErr }

			result, err := manager.RunJob(context.Background(), job.ID)
			if !errors.Is(err, persistErr) {
				t.Fatalf("RunJob() error = %v, want %v", err, persistErr)
			}
			if result == nil || result.Status != StatusFailed {
				t.Fatalf("RunJob() result = %+v, want failed result", result)
			}
			tt.assertIdle(t, destination, commandLog)
			if events := notifier.Events(); len(events) != 0 {
				t.Fatalf("notification events = %+v, want none", events)
			}
			view, viewErr := manager.GetJob(job.ID)
			if viewErr != nil {
				t.Fatalf("GetJob() error: %v", viewErr)
			}
			if view.Running || view.LastRun != nil {
				t.Fatalf("job view after failed running state persistence = %+v", view)
			}
		})
	}
}

func TestManager_RunJobFailsClosedAfterRunningStateDirectorySyncWarning(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
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
	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected state directory sync failure")
	syncBackupJSONDir = func(*os.File, string) error { return syncErr }

	result, err := manager.RunJob(context.Background(), "home")
	if !errors.Is(err, syncErr) || !isBackupPersistenceWarning(err) {
		t.Fatalf("RunJob() error = %v, want persistence warning wrapping %v", err, syncErr)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed", result)
	}
	if _, statErr := os.Stat(filepath.Join(destination, "home", "snapshots")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("snapshot root stat error = %v, want not exist", statErr)
	}
	view, viewErr := manager.GetJob("home")
	if viewErr != nil {
		t.Fatalf("GetJob() error: %v", viewErr)
	}
	if view.LastRun == nil || view.LastRun.Status != StatusFailed {
		t.Fatalf("LastRun = %+v, want visible failed terminal state", view.LastRun)
	}
	data, readErr := os.ReadFile(filepath.Join(stateRoot, stateFileName))
	if readErr != nil {
		t.Fatalf("ReadFile(status) error: %v", readErr)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(status) error: %v", err)
	}
	if state.Jobs["home"].LastRun == nil || state.Jobs["home"].LastRun.Status != StatusFailed {
		t.Fatalf("persisted LastRun = %+v, want failed", state.Jobs["home"].LastRun)
	}
}

func TestManager_RunJobExcludesAnotherStateRootAtSameLocalTarget(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	first, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state-a"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager(first) error: %v", err)
	}
	second, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state-b"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager(second) error: %v", err)
	}

	targetLock, err := first.acquireJobTargetLock(job)
	if err != nil {
		t.Fatalf("acquireJobTargetLock() error: %v", err)
	}
	result, runErr := second.RunJob(context.Background(), job.ID)
	if !errors.Is(runErr, ErrBackupTargetLockHeld) {
		t.Fatalf("RunJob() error = %v, want %v", runErr, ErrBackupTargetLockHeld)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed", result)
	}
	if snapshots, globErr := filepath.Glob(filepath.Join(destination, job.ID, "snapshots", "*")); globErr != nil {
		t.Fatalf("Glob(snapshots) error: %v", globErr)
	} else if len(snapshots) != 0 {
		t.Fatalf("contending run created snapshots: %#v", snapshots)
	}
	if err := targetLock.Close(); err != nil {
		t.Fatalf("target lock Close() error: %v", err)
	}

	result, runErr = second.RunJob(context.Background(), job.ID)
	if runErr != nil {
		t.Fatalf("RunJob() after target unlock error: %v", runErr)
	}
	if result == nil || result.Status != StatusCompleted {
		t.Fatalf("RunJob() after target unlock result = %+v, want completed", result)
	}
}

func TestManager_LocalTargetLockExcludesRestoreAndRetentionOperations(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	owner, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state-owner"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager(owner) error: %v", err)
	}
	contender, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state-contender"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager(contender) error: %v", err)
	}
	targetLock, err := owner.acquireJobTargetLock(job)
	if err != nil {
		t.Fatalf("acquireJobTargetLock() error: %v", err)
	}
	defer targetLock.Close()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "restore drill",
			run: func() error {
				_, err := contender.RunRestoreDrill(context.Background(), job.ID, RestoreDrillOptions{})
				return err
			},
		},
		{
			name: "restore preview",
			run: func() error {
				_, err := contender.RunRestorePreview(context.Background(), job.ID, RestorePreviewOptions{TargetPath: filepath.Join(tmpDir, "preview")})
				return err
			},
		},
		{
			name: "restore verify",
			run: func() error {
				_, err := contender.RunRestoreVerify(context.Background(), job.ID, RestoreVerifyOptions{TargetPath: filepath.Join(tmpDir, "verify")})
				return err
			},
		},
		{
			name: "retention check",
			run: func() error {
				_, err := contender.RunRetentionCheck(context.Background(), job.ID)
				return err
			},
		},
		{
			name: "restore",
			run: func() error {
				_, err := contender.RunRestore(context.Background(), job.ID, RestoreOptions{TargetPath: filepath.Join(tmpDir, "restore")})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, ErrBackupTargetLockHeld) {
				t.Fatalf("operation error = %v, want %v", err, ErrBackupTargetLockHeld)
			}
		})
	}
}

func TestManager_RunJobReportsTargetLockReleaseFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: filepath.Join(tmpDir, "backups"),
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: job.Source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalClose := closeBackupTargetLock
	t.Cleanup(func() { closeBackupTargetLock = originalClose })
	closeErr := errors.New("injected target lock release failure")
	closeBackupTargetLock = func(lock *backupStateLock) error {
		return errors.Join(lock.Close(), closeErr)
	}

	result, err := manager.RunJob(context.Background(), job.ID)
	if !errors.Is(err, closeErr) || !errors.Is(err, ErrBackupTargetLockRelease) {
		t.Fatalf("RunJob() error = %v, want typed target lock release failure", err)
	}
	if result == nil || result.Status != StatusCompleted {
		t.Fatalf("RunJob() result = %+v, want completed backup details", result)
	}
	if _, statErr := os.Stat(result.SnapshotPath); statErr != nil {
		t.Fatalf("completed snapshot stat error: %v", statErr)
	}
}

func TestManager_RunJobKeepsPreviousSnapshotWhenNewSuccessCannotPersist(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	sourceFile := filepath.Join(source, "note.txt")
	mustWriteFile(t, sourceFile, "v1")
	job := config.BackupJobConfig{
		ID:           "home",
		Name:         "Home backup",
		Type:         JobTypeLocal,
		Source:       source,
		Destination:  destination,
		MaxSnapshots: 1,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	mustWriteFile(t, sourceFile, "v2")
	now = now.Add(time.Minute)
	secondRunID := formatRunID(now)

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	commitErr := errors.New("injected completed state persistence failure")
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		state, ok := value.(persistedState)
		if ok {
			lastRun := state.Jobs[job.ID].LastRun
			if lastRun != nil && lastRun.ID == secondRunID && lastRun.Status == StatusCompleted {
				return commitErr
			}
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	second, err := manager.RunJob(context.Background(), job.ID)
	if !errors.Is(err, commitErr) {
		t.Fatalf("second RunJob() error = %v, want %v", err, commitErr)
	}
	if second == nil || second.Status != StatusFailed {
		t.Fatalf("second RunJob() result = %+v, want failed result", second)
	}
	if _, statErr := os.Stat(second.SnapshotPath); statErr != nil {
		t.Fatalf("second finalized snapshot stat error: %v", statErr)
	}
	if _, statErr := os.Stat(first.SnapshotPath); statErr != nil {
		t.Fatalf("previous successful snapshot was pruned before state commit: %v", statErr)
	}
	view, err := manager.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if view.LastSuccessfulRun == nil || view.LastSuccessfulRun.ID != first.ID {
		t.Fatalf("active LastSuccessfulRun = %+v, want %s", view.LastSuccessfulRun, first.ID)
	}

	writeBackupStateFile = originalWriteBackupStateFile
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	reloaded, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	reloadedView, err := reloaded.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() reload error: %v", err)
	}
	if reloadedView.LastSuccessfulRun == nil || reloadedView.LastSuccessfulRun.ID != first.ID {
		t.Fatalf("reloaded LastSuccessfulRun = %+v, want %s", reloadedView.LastSuccessfulRun, first.ID)
	}
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	preview, err := reloaded.RunRestorePreview(context.Background(), job.ID, RestorePreviewOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.SnapshotPath != first.SnapshotPath {
		t.Fatalf("preview snapshot = %q, want %q", preview.SnapshotPath, first.SnapshotPath)
	}
	restore, err := reloaded.RunRestore(context.Background(), job.ID, RestoreOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted {
		t.Fatalf("RunRestore() result = %+v, want completed", restore)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "note.txt"), "v1")
}

func TestManager_RunJobPersistsCompletedStateDirectorySyncWarning(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected completed state directory sync failure")
	stateSyncCount := 0
	syncBackupJSONDir = func(handle *os.File, dir string) error {
		if filepath.Clean(dir) == filepath.Clean(stateRoot) {
			stateSyncCount++
			if stateSyncCount > 1 {
				return syncErr
			}
		}
		return originalSyncBackupJSONDir(handle, dir)
	}

	result, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("RunJob() error = %v, want completed warning", err)
	}
	if result == nil || result.Status != StatusCompleted || !result.Warning {
		t.Fatalf("RunJob() result = %+v, want completed warning", result)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "状态目录同步失败") {
		t.Fatalf("RunJob() warnings = %#v", result.Warnings)
	}
	view, viewErr := manager.GetJob(job.ID)
	if viewErr != nil {
		t.Fatalf("GetJob() error: %v", viewErr)
	}
	if view.LastRun == nil || !view.LastRun.Warning || view.LastSuccessfulRun == nil || !view.LastSuccessfulRun.Warning {
		t.Fatalf("job view lost completed warning: %+v", view)
	}

	syncBackupJSONDir = originalSyncBackupJSONDir
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	reloaded, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	reloadedView, err := reloaded.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() reload error: %v", err)
	}
	if reloadedView.LastRun == nil || !reloadedView.LastRun.Warning || reloadedView.LastSuccessfulRun == nil || !reloadedView.LastSuccessfulRun.Warning {
		t.Fatalf("reloaded job view lost completed warning: %+v", reloadedView)
	}
}

func TestManager_RunJobKeepsCommittedSnapshotWhenCleanupStateCannotPersist(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	sourceFile := filepath.Join(source, "note.txt")
	mustWriteFile(t, sourceFile, "v1")
	job := config.BackupJobConfig{
		ID:           "home",
		Name:         "Home backup",
		Type:         JobTypeLocal,
		Source:       source,
		Destination:  destination,
		MaxSnapshots: 1,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Date(2026, 7, 15, 1, 30, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	mustWriteFile(t, sourceFile, "v2")
	now = now.Add(time.Minute)
	secondRunID := formatRunID(now)

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	cleanupStateErr := errors.New("injected cleanup state persistence failure")
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		state, ok := value.(persistedState)
		if ok {
			lastRun := state.Jobs[job.ID].LastRun
			if lastRun != nil && lastRun.ID == secondRunID && lastRun.Status == StatusCompleted && lastRun.PrunedSnapshots == 1 {
				return cleanupStateErr
			}
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	second, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("second RunJob() error = %v, want completed warning", err)
	}
	if second == nil || second.Status != StatusCompleted || !second.Warning || second.PrunedSnapshots != 1 {
		t.Fatalf("second RunJob() result = %+v, want completed cleanup warning", second)
	}
	if !strings.Contains(strings.Join(second.Warnings, "\n"), "保留策略结果未能可靠保存") {
		t.Fatalf("second RunJob() warnings = %#v", second.Warnings)
	}
	if _, statErr := os.Stat(first.SnapshotPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("first snapshot stat error = %v, want pruned", statErr)
	}
	if _, statErr := os.Stat(second.SnapshotPath); statErr != nil {
		t.Fatalf("second snapshot stat error: %v", statErr)
	}
	view, viewErr := manager.GetJob(job.ID)
	if viewErr != nil {
		t.Fatalf("GetJob() error: %v", viewErr)
	}
	if view.LastSuccessfulRun == nil || view.LastSuccessfulRun.ID != second.ID {
		t.Fatalf("LastSuccessfulRun = %+v, want committed %s", view.LastSuccessfulRun, second.ID)
	}
	if !view.LastSuccessfulRun.Warning || !strings.Contains(strings.Join(view.LastSuccessfulRun.Warnings, "\n"), "保留策略结果未能可靠保存") {
		t.Fatalf("LastSuccessfulRun lost cleanup persistence warning: %+v", view.LastSuccessfulRun)
	}

	writeBackupStateFile = originalWriteBackupStateFile
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	reloaded, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot, StorageRoot: source, Jobs: []config.BackupJobConfig{job}})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	reloadedView, err := reloaded.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() reload error: %v", err)
	}
	if reloadedView.LastSuccessfulRun == nil || !reloadedView.LastSuccessfulRun.Warning || !strings.Contains(strings.Join(reloadedView.LastSuccessfulRun.Warnings, "\n"), "保留策略结果未能可靠保存") {
		t.Fatalf("reloaded LastSuccessfulRun lost cleanup persistence warning: %+v", reloadedView.LastSuccessfulRun)
	}
	restoreTarget := filepath.Join(tmpDir, "cleanup-restore-target")
	preview, err := reloaded.RunRestorePreview(context.Background(), job.ID, RestorePreviewOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.SnapshotPath != second.SnapshotPath {
		t.Fatalf("preview snapshot = %q, want %q", preview.SnapshotPath, second.SnapshotPath)
	}
	restore, err := reloaded.RunRestore(context.Background(), job.ID, RestoreOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted {
		t.Fatalf("RunRestore() result = %+v, want completed", restore)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "note.txt"), "v2")
}

func TestManager_ApplyRetentionRejectsReplacedSnapshotIdentity(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v2")
	second, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}

	originalRemove := removeLocalBackupSnapshotEntry
	t.Cleanup(func() { removeLocalBackupSnapshotEntry = originalRemove })
	removeLocalBackupSnapshotEntry = func(parent *os.File, parentPath, name string, expected os.FileInfo) error {
		selectedPath := filepath.Join(parentPath, name)
		if filepath.Clean(selectedPath) == filepath.Clean(first.SnapshotPath) {
			if err := os.Rename(selectedPath, selectedPath+".selected"); err != nil {
				t.Fatalf("Rename(selected snapshot) error: %v", err)
			}
			if err := os.Mkdir(selectedPath, 0o700); err != nil {
				t.Fatalf("Mkdir(replacement snapshot) error: %v", err)
			}
			mustWriteFile(t, filepath.Join(selectedPath, "replacement.txt"), "keep")
		}
		return originalRemove(parent, parentPath, name, expected)
	}
	retentionJob := job
	retentionJob.MaxSnapshots = 1
	pruned, warnings := manager.applyRetention(context.Background(), retentionJob, second.SnapshotPath)
	if pruned != 0 {
		t.Fatalf("applyRetention() pruned = %d, want 0", pruned)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "changed before removal") {
		t.Fatalf("applyRetention() warnings = %#v, want identity warning", warnings)
	}
	assertFileContent(t, filepath.Join(first.SnapshotPath, "replacement.txt"), "keep")
}

func TestManager_ApplyRetentionReportsNoCommittedCountWhenParentSyncFails(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")
	job := config.BackupJobConfig{
		ID:          "home",
		Name:        "Home backup",
		Type:        JobTypeLocal,
		Source:      source,
		Destination: destination,
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 7, 15, 4, 30, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v2")
	second, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}

	originalSync := syncLocalBackupDirectoryHandle
	t.Cleanup(func() { syncLocalBackupDirectoryHandle = originalSync })
	syncErr := errors.New("injected retention parent sync failure")
	syncLocalBackupDirectoryHandle = func(*os.File) error { return syncErr }
	retentionJob := job
	retentionJob.MaxSnapshots = 1
	pruned, warnings := manager.applyRetention(context.Background(), retentionJob, second.SnapshotPath)
	if pruned != 0 {
		t.Fatalf("applyRetention() pruned = %d, want conservative 0", pruned)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), syncErr.Error()) {
		t.Fatalf("applyRetention() warnings = %#v, want sync failure", warnings)
	}
	if _, statErr := os.Stat(first.SnapshotPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("removed snapshot stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunJobTerminalPersistenceFailureKeepsVolatileFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
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

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	terminalErr := errors.New("injected terminal state persistence failure")
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		state, ok := value.(persistedState)
		if ok {
			lastRun := state.Jobs["home"].LastRun
			if lastRun != nil && lastRun.Status == StatusFailed {
				return terminalErr
			}
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	result, err := manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) || !errors.Is(err, terminalErr) {
		t.Fatalf("RunJob() error = %v, want source and terminal persistence failures", err)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed", result)
	}
	view, viewErr := manager.GetJob("home")
	if viewErr != nil {
		t.Fatalf("GetJob() error: %v", viewErr)
	}
	if view.Running || view.LastRun == nil || view.LastRun.Status != StatusFailed {
		t.Fatalf("job view after terminal persistence failure = %+v", view)
	}
	events := notifier.Events()
	if len(events) != 1 || events[0].Status != StatusFailed || events[0].Level != NotificationLevelCritical {
		t.Fatalf("notification events = %+v, want one critical failure", events)
	}
}

func TestManager_TerminalPersistenceFailureKeepsVolatileMaintenanceStateVisible(t *testing.T) {
	tests := []struct {
		name           string
		expectedStatus string
		eventType      string
		candidate      func(JobState) string
		run            func(*Manager, string) (string, error)
		assertView     func(*testing.T, *JobView)
	}{
		{
			name:           "restore drill",
			expectedStatus: StatusFailed,
			eventType:      NotificationTypeRestoreDrill,
			candidate: func(state JobState) string {
				if state.LastRestoreDrill == nil {
					return ""
				}
				return state.LastRestoreDrill.Status
			},
			run: func(manager *Manager, _ string) (string, error) {
				result, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
				return result.Status, err
			},
			assertView: func(t *testing.T, view *JobView) {
				t.Helper()
				if view.LastRestoreDrill == nil || view.LastRestoreDrill.Status != StatusFailed || len(view.RestoreDrillHistory) != 1 || view.RestoreDrillHistory[0].Status != StatusFailed {
					t.Fatalf("restore drill state/history = %+v/%+v, want one visible failed terminal result", view.LastRestoreDrill, view.RestoreDrillHistory)
				}
			},
		},
		{
			name:           "restore verify",
			expectedStatus: StatusFailed,
			eventType:      NotificationTypeRestoreVerify,
			candidate: func(state JobState) string {
				if state.LastRestoreVerify == nil {
					return ""
				}
				return state.LastRestoreVerify.Status
			},
			run: func(manager *Manager, tmpDir string) (string, error) {
				result, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: filepath.Join(tmpDir, "missing-restore")})
				return result.Status, err
			},
			assertView: func(t *testing.T, view *JobView) {
				t.Helper()
				if view.LastRestoreVerify == nil || view.LastRestoreVerify.Status != StatusFailed {
					t.Fatalf("restore verify state = %+v, want visible failed terminal result", view.LastRestoreVerify)
				}
			},
		},
		{
			name:           "retention check",
			expectedStatus: StatusCompleted,
			eventType:      NotificationTypeRetention,
			candidate: func(state JobState) string {
				if state.LastRetentionCheck == nil {
					return ""
				}
				return state.LastRetentionCheck.Status
			},
			run: func(manager *Manager, _ string) (string, error) {
				result, err := manager.RunRetentionCheck(context.Background(), "home")
				return result.Status, err
			},
			assertView: func(t *testing.T, view *JobView) {
				t.Helper()
				if view.LastRetentionCheck == nil || view.LastRetentionCheck.Status != StatusCompleted || !view.LastRetentionCheck.Warning {
					t.Fatalf("retention check state = %+v, want visible completed terminal result with warning", view.LastRetentionCheck)
				}
			},
		},
		{
			name:           "restore",
			expectedStatus: StatusFailed,
			eventType:      NotificationTypeRestore,
			candidate: func(state JobState) string {
				if state.LastRestore == nil {
					return ""
				}
				return state.LastRestore.Status
			},
			run: func(manager *Manager, tmpDir string) (string, error) {
				result, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "restore-target")})
				return result.Status, err
			},
			assertView: func(t *testing.T, view *JobView) {
				t.Helper()
				if view.LastRestore == nil || view.LastRestore.Status != StatusFailed || len(view.RestoreHistory) != 1 || view.RestoreHistory[0].Status != StatusFailed {
					t.Fatalf("restore state/history = %+v/%+v, want one visible failed terminal result", view.LastRestore, view.RestoreHistory)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			destination := filepath.Join(tmpDir, "backups")
			mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
			notifier := &recordingNotifier{}
			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state"),
				StorageRoot: source,
				Notifier:    notifier,
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

			originalWriteBackupStateFile := writeBackupStateFile
			t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
			terminalErr := errors.New("injected maintenance terminal state persistence failure")
			writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
				state, ok := value.(persistedState)
				if ok {
					candidateStatus := tt.candidate(state.Jobs["home"])
					if candidateStatus != "" && candidateStatus != StatusRunning {
						return terminalErr
					}
				}
				return originalWriteBackupStateFile(lock, path, value, perm)
			}

			status, err := tt.run(manager, tmpDir)
			if !errors.Is(err, terminalErr) {
				t.Fatalf("operation error = %v, want %v", err, terminalErr)
			}
			if status != tt.expectedStatus {
				t.Fatalf("operation status = %q, want %q", status, tt.expectedStatus)
			}
			view, viewErr := manager.GetJob("home")
			if viewErr != nil {
				t.Fatalf("GetJob() error: %v", viewErr)
			}
			if view.Running {
				t.Fatalf("job view remains running after terminal persistence failure: %+v", view)
			}
			tt.assertView(t, view)
			events := notifier.Events()
			if len(events) != 1 || events[0].Type != tt.eventType || events[0].Status != tt.expectedStatus {
				t.Fatalf("notification events = %+v, want one %s event with status %s", events, tt.eventType, tt.expectedStatus)
			}

			data, readErr := os.ReadFile(filepath.Join(tmpDir, "state", stateFileName))
			if readErr != nil {
				t.Fatalf("ReadFile(state) error: %v", readErr)
			}
			var persisted persistedState
			if unmarshalErr := json.Unmarshal(data, &persisted); unmarshalErr != nil {
				t.Fatalf("Unmarshal(state) error: %v", unmarshalErr)
			}
			if persistedStatus := tt.candidate(persisted.Jobs["home"]); persistedStatus != StatusRunning {
				t.Fatalf("persisted operation status = %q, want running state left by injected terminal write failure", persistedStatus)
			}
		})
	}
}

func TestManager_RestoreDrillPersistenceWarningsKeepSingleTerminalHistoryEntry(t *testing.T) {
	tests := []struct {
		name                 string
		warnOnRunningState   bool
		expectedPersistedRun string
	}{
		{
			name:                 "running warning followed by terminal hard failure",
			warnOnRunningState:   true,
			expectedPersistedRun: StatusRunning,
		},
		{
			name:                 "terminal warning after durable running state",
			expectedPersistedRun: StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
			manager, err := newBackupTestManager(t, ManagerConfig{
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

			originalWriteBackupStateFile := writeBackupStateFile
			t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
			warningErr := errors.New("injected post-replace persistence warning")
			terminalErr := errors.New("injected terminal persistence failure")
			writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
				state, ok := value.(persistedState)
				if !ok || state.Jobs["home"].LastRestoreDrill == nil {
					return originalWriteBackupStateFile(lock, path, value, perm)
				}
				status := state.Jobs["home"].LastRestoreDrill.Status
				if status == StatusRunning && tt.warnOnRunningState {
					if err := originalWriteBackupStateFile(lock, path, value, perm); err != nil {
						return err
					}
					return wrapBackupPersistenceWarning(warningErr)
				}
				if status != StatusRunning {
					if tt.warnOnRunningState {
						return terminalErr
					}
					if err := originalWriteBackupStateFile(lock, path, value, perm); err != nil {
						return err
					}
					return wrapBackupPersistenceWarning(warningErr)
				}
				return originalWriteBackupStateFile(lock, path, value, perm)
			}

			result, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
			if !errors.Is(err, warningErr) {
				t.Fatalf("RunRestoreDrill() error = %v, want warning %v", err, warningErr)
			}
			if tt.warnOnRunningState && !errors.Is(err, terminalErr) {
				t.Fatalf("RunRestoreDrill() error = %v, want terminal failure %v", err, terminalErr)
			}
			if result == nil || result.Status != StatusFailed {
				t.Fatalf("RunRestoreDrill() result = %+v, want failed", result)
			}
			if !tt.warnOnRunningState {
				if !errors.Is(err, ErrNoSnapshots) {
					t.Fatalf("RunRestoreDrill() error = %v, want business failure %v", err, ErrNoSnapshots)
				}
				if !result.Warning || !strings.Contains(strings.Join(result.Warnings, "\n"), restoreDrillStateWarning) {
					t.Fatalf("RunRestoreDrill() warnings = %+v, want persistence warning", result.Warnings)
				}
			}
			view, viewErr := manager.GetJob("home")
			if viewErr != nil {
				t.Fatalf("GetJob() error: %v", viewErr)
			}
			if view.LastRestoreDrill == nil || view.LastRestoreDrill.Status != StatusFailed || len(view.RestoreDrillHistory) != 1 {
				t.Fatalf("restore drill state/history = %+v/%+v, want one failed terminal entry", view.LastRestoreDrill, view.RestoreDrillHistory)
			}

			data, readErr := os.ReadFile(filepath.Join(tmpDir, "state", stateFileName))
			if readErr != nil {
				t.Fatalf("ReadFile(state) error: %v", readErr)
			}
			var persisted persistedState
			if unmarshalErr := json.Unmarshal(data, &persisted); unmarshalErr != nil {
				t.Fatalf("Unmarshal(state) error: %v", unmarshalErr)
			}
			persistedDrill := persisted.Jobs["home"].LastRestoreDrill
			if persistedDrill == nil || persistedDrill.Status != tt.expectedPersistedRun {
				t.Fatalf("persisted restore drill = %+v, want status %s", persistedDrill, tt.expectedPersistedRun)
			}
			if tt.expectedPersistedRun == StatusFailed && len(persisted.Jobs["home"].RestoreDrillHistory) != 1 {
				t.Fatalf("persisted restore drill history = %+v, want one terminal entry", persisted.Jobs["home"].RestoreDrillHistory)
			}
		})
	}
}

func TestManager_SuccessfulMaintenancePersistenceWarningReturnsWarningResult(t *testing.T) {
	type observation struct {
		status     string
		warning    bool
		warnings   []string
		historyLen int
	}
	tests := []struct {
		name          string
		eventType     string
		warningText   string
		secondaryHard bool
		prepare       func(*testing.T, *Manager, string)
		run           func(*Manager, string) (observation, error)
		view          func(*JobView) observation
		state         func(JobState) observation
	}{
		{
			name:        "restore drill",
			eventType:   NotificationTypeRestoreDrill,
			warningText: restoreDrillStateWarning,
			run: func(manager *Manager, _ string) (observation, error) {
				result, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
				if result == nil {
					return observation{}, err
				}
				return observation{status: result.Status, warning: result.Warning, warnings: result.Warnings}, err
			},
			view: func(view *JobView) observation {
				if view.LastRestoreDrill == nil {
					return observation{}
				}
				return observation{status: view.LastRestoreDrill.Status, warning: view.LastRestoreDrill.Warning, warnings: view.LastRestoreDrill.Warnings, historyLen: len(view.RestoreDrillHistory)}
			},
			state: func(state JobState) observation {
				if state.LastRestoreDrill == nil {
					return observation{}
				}
				return observation{status: state.LastRestoreDrill.Status, warning: state.LastRestoreDrill.Warning, warnings: state.LastRestoreDrill.Warnings, historyLen: len(state.RestoreDrillHistory)}
			},
		},
		{
			name:          "restore verify",
			eventType:     NotificationTypeRestoreVerify,
			warningText:   restoreVerifyStateWarning,
			secondaryHard: true,
			prepare: func(t *testing.T, manager *Manager, tmpDir string) {
				t.Helper()
				if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "verify-target")}); err != nil {
					t.Fatalf("RunRestore(verify setup) error: %v", err)
				}
			},
			run: func(manager *Manager, tmpDir string) (observation, error) {
				result, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: filepath.Join(tmpDir, "verify-target")})
				if result == nil {
					return observation{}, err
				}
				return observation{status: result.Status, warning: len(result.Warnings) > 0, warnings: result.Warnings}, err
			},
			view: func(view *JobView) observation {
				if view.LastRestoreVerify == nil {
					return observation{}
				}
				return observation{status: view.LastRestoreVerify.Status, warning: len(view.LastRestoreVerify.Warnings) > 0, warnings: view.LastRestoreVerify.Warnings}
			},
			state: func(state JobState) observation {
				if state.LastRestoreVerify == nil {
					return observation{}
				}
				return observation{status: state.LastRestoreVerify.Status, warning: len(state.LastRestoreVerify.Warnings) > 0, warnings: state.LastRestoreVerify.Warnings}
			},
		},
		{
			name:          "restore",
			eventType:     NotificationTypeRestore,
			warningText:   restoreStateWarning,
			secondaryHard: true,
			run: func(manager *Manager, tmpDir string) (observation, error) {
				result, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(tmpDir, "restore-target")})
				if result == nil {
					return observation{}, err
				}
				return observation{status: result.Status, warning: len(result.Warnings) > 0, warnings: result.Warnings}, err
			},
			view: func(view *JobView) observation {
				if view.LastRestore == nil {
					return observation{}
				}
				return observation{status: view.LastRestore.Status, warning: len(view.LastRestore.Warnings) > 0, warnings: view.LastRestore.Warnings, historyLen: len(view.RestoreHistory)}
			},
			state: func(state JobState) observation {
				if state.LastRestore == nil {
					return observation{}
				}
				return observation{status: state.LastRestore.Status, warning: len(state.LastRestore.Warnings) > 0, warnings: state.LastRestore.Warnings, historyLen: len(state.RestoreHistory)}
			},
		},
		{
			name:        "retention check",
			eventType:   NotificationTypeRetention,
			warningText: retentionCheckStateWarning,
			run: func(manager *Manager, _ string) (observation, error) {
				result, err := manager.RunRetentionCheck(context.Background(), "home")
				if result == nil {
					return observation{}, err
				}
				return observation{status: result.Status, warning: result.Warning, warnings: result.Warnings}, err
			},
			view: func(view *JobView) observation {
				if view.LastRetentionCheck == nil {
					return observation{}
				}
				return observation{status: view.LastRetentionCheck.Status, warning: view.LastRetentionCheck.Warning, warnings: view.LastRetentionCheck.Warnings}
			},
			state: func(state JobState) observation {
				if state.LastRetentionCheck == nil {
					return observation{}
				}
				return observation{status: state.LastRetentionCheck.Status, warning: state.LastRetentionCheck.Warning, warnings: state.LastRetentionCheck.Warnings}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			mustWriteFile(t, filepath.Join(source, "files", "note.txt"), "backup")
			mustWriteFile(t, filepath.Join(source, ".mnemonas", "index.db"), "index")
			mustWriteFile(t, filepath.Join(source, ".mnemonas", "objects", "object"), "object")
			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state"),
				StorageRoot: source,
				Jobs: []config.BackupJobConfig{{
					ID:           "home",
					Name:         "Home backup",
					Type:         JobTypeLocal,
					Source:       source,
					Destination:  filepath.Join(tmpDir, "backups"),
					MaxSnapshots: 2,
				}},
			})
			if err != nil {
				t.Fatalf("NewManager() error: %v", err)
			}
			if _, err := manager.RunJob(context.Background(), "home"); err != nil {
				t.Fatalf("RunJob(setup) error: %v", err)
			}
			if tt.prepare != nil {
				tt.prepare(t, manager, tmpDir)
			}
			notifier := &recordingNotifier{}
			manager.notifier = notifier

			originalWriteBackupStateFile := writeBackupStateFile
			t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
			warningErr := errors.New("injected post-replace maintenance persistence warning")
			secondaryErr := errors.New("injected warning result persistence failure")
			terminalWrites := 0
			writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
				state, ok := value.(persistedState)
				if !ok || tt.state(state.Jobs["home"]).status != StatusCompleted {
					return originalWriteBackupStateFile(lock, path, value, perm)
				}
				terminalWrites++
				if terminalWrites == 1 {
					if err := originalWriteBackupStateFile(lock, path, value, perm); err != nil {
						return err
					}
					return wrapBackupPersistenceWarning(warningErr)
				}
				if terminalWrites == 2 && tt.secondaryHard {
					return secondaryErr
				}
				return originalWriteBackupStateFile(lock, path, value, perm)
			}

			result, err := tt.run(manager, tmpDir)
			if err != nil {
				t.Fatalf("operation error = %v, want nil after successful business result", err)
			}
			if result.status != StatusCompleted || !result.warning || !strings.Contains(strings.Join(result.warnings, "\n"), tt.warningText) {
				t.Fatalf("operation result = %+v, want completed persistence warning %q", result, tt.warningText)
			}
			if terminalWrites != 2 {
				t.Fatalf("terminal state writes = %d, want initial terminal write and warning result write", terminalWrites)
			}
			view, viewErr := manager.GetJob("home")
			if viewErr != nil {
				t.Fatalf("GetJob() error: %v", viewErr)
			}
			visible := tt.view(view)
			if visible.status != StatusCompleted || !visible.warning || !strings.Contains(strings.Join(visible.warnings, "\n"), tt.warningText) {
				t.Fatalf("visible state = %+v, want completed persistence warning %q", visible, tt.warningText)
			}
			if (tt.name == "restore drill" || tt.name == "restore") && visible.historyLen != 1 {
				t.Fatalf("visible history length = %d, want one deduplicated terminal result", visible.historyLen)
			}
			events := notifier.Events()
			if len(events) != 1 || events[0].Type != tt.eventType || events[0].Level != NotificationLevelWarning || events[0].Status != StatusCompleted || events[0].WarningCount == 0 {
				t.Fatalf("notification events = %+v, want one completed warning event", events)
			}

			data, readErr := os.ReadFile(filepath.Join(tmpDir, "state", stateFileName))
			if readErr != nil {
				t.Fatalf("ReadFile(state) error: %v", readErr)
			}
			var persisted persistedState
			if unmarshalErr := json.Unmarshal(data, &persisted); unmarshalErr != nil {
				t.Fatalf("Unmarshal(state) error: %v", unmarshalErr)
			}
			persistedResult := tt.state(persisted.Jobs["home"])
			if persistedResult.status != StatusCompleted {
				t.Fatalf("persisted result = %+v, want completed", persistedResult)
			}
			if tt.secondaryHard {
				if strings.Contains(strings.Join(persistedResult.warnings, "\n"), tt.warningText) {
					t.Fatalf("persisted result unexpectedly contains warning after injected secondary hard failure: %+v", persistedResult)
				}
			} else if !persistedResult.warning || !strings.Contains(strings.Join(persistedResult.warnings, "\n"), tt.warningText) {
				t.Fatalf("persisted result = %+v, want durable persistence warning %q", persistedResult, tt.warningText)
			}
			if (tt.name == "restore drill" || tt.name == "restore") && persistedResult.historyLen != 1 {
				t.Fatalf("persisted history length = %d, want one deduplicated terminal result", persistedResult.historyLen)
			}
		})
	}
}

func TestManager_UpdateLastRunDirectorySyncWarningKeepsVisibleState(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	manager, err := newBackupTestManager(t, ManagerConfig{Root: filepath.Join(tmpDir, "state")})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	startedAt := time.Date(2026, 7, 15, 1, 45, 0, 0, time.UTC)
	first := &RunResult{ID: "first", JobID: "home", Status: StatusCompleted, StartedAt: startedAt}
	if err := manager.updateLastRun(first); err != nil {
		t.Fatalf("updateLastRun(first) error: %v", err)
	}

	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected state directory sync failure")
	syncBackupJSONDir = func(*os.File, string) error { return syncErr }
	second := &RunResult{ID: "second", JobID: "home", Status: StatusCompleted, StartedAt: startedAt.Add(time.Minute)}
	err = manager.updateLastRun(second)
	if !errors.Is(err, syncErr) || !isBackupPersistenceWarning(err) {
		t.Fatalf("updateLastRun(second) error = %v, want persistence warning wrapping %v", err, syncErr)
	}
	manager.mu.Lock()
	inMemory := cloneRunResultRaw(manager.state.Jobs["home"].LastSuccessfulRun)
	healthy := manager.statePersistenceHealthy
	manager.mu.Unlock()
	if inMemory == nil || inMemory.ID != second.ID {
		t.Fatalf("in-memory LastSuccessfulRun = %+v, want %s", inMemory, second.ID)
	}
	if healthy {
		t.Fatal("state persistence health = true, want false after directory sync warning")
	}
	data, readErr := os.ReadFile(manager.statePath())
	if readErr != nil {
		t.Fatalf("ReadFile(status) error: %v", readErr)
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(status) error: %v", err)
	}
	if persisted.Jobs["home"].LastSuccessfulRun == nil || persisted.Jobs["home"].LastSuccessfulRun.ID != second.ID {
		t.Fatalf("persisted LastSuccessfulRun = %+v, want %s", persisted.Jobs["home"].LastSuccessfulRun, second.ID)
	}
}

func TestBackupPersistenceWarningWrapsAndUnwraps(t *testing.T) {
	cause := errors.New("directory sync failed")
	warning := wrapBackupPersistenceWarning(cause)
	if !isBackupPersistenceWarning(warning) {
		t.Fatalf("warning = %v, want recognized persistence warning", warning)
	}
	if !errors.Is(warning, cause) {
		t.Fatalf("warning = %v, want to unwrap %v", warning, cause)
	}
	if again := wrapBackupPersistenceWarning(warning); again != warning {
		t.Fatal("wrapping an existing persistence warning changed its identity")
	}
	if wrapBackupPersistenceWarning(nil) != nil {
		t.Fatal("wrapBackupPersistenceWarning(nil) != nil")
	}
}

func TestManager_RunJobTreatsManifestDirectorySyncWarningAsFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	runAt := time.Date(2026, 7, 15, 1, 50, 0, 0, time.UTC)
	runID := formatRunID(runAt)
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
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
	manager.now = func() time.Time { return runAt }

	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected manifest directory sync failure")
	syncBackupJSONDir = func(handle *os.File, dir string) error {
		if strings.HasSuffix(filepath.Base(dir), ".partial") {
			return syncErr
		}
		return originalSyncBackupJSONDir(handle, dir)
	}

	result, err := manager.RunJob(context.Background(), "home")
	if !errors.Is(err, syncErr) {
		t.Fatalf("RunJob() error = %v, want %v", err, syncErr)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed", result)
	}
	finalPath := filepath.Join(destination, "home", "snapshots", runID)
	if _, statErr := os.Stat(finalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final snapshot stat error = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(finalPath + ".partial"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial snapshot stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunJobPublishesLocalSnapshotAfterDurabilityBarriers(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
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

	snapshotRoot := filepath.Join(destination, "home", "snapshots")
	originalSyncTree := syncLocalBackupSnapshotTree
	originalRename := renameLocalBackupSnapshot
	originalSyncDir := syncLocalBackupDirectory
	originalSyncDirHandle := syncLocalBackupDirectoryHandle
	originalAfterFinalize := afterFinalizeLocalBackupSnapshot
	t.Cleanup(func() {
		syncLocalBackupSnapshotTree = originalSyncTree
		renameLocalBackupSnapshot = originalRename
		syncLocalBackupDirectory = originalSyncDir
		syncLocalBackupDirectoryHandle = originalSyncDirHandle
		afterFinalizeLocalBackupSnapshot = originalAfterFinalize
	})
	var events []string
	syncLocalBackupSnapshotTree = func(dir *os.File, path string) error {
		events = append(events, "tree:"+path)
		return originalSyncTree(dir, path)
	}
	renameLocalBackupSnapshot = func(root *os.Root, oldName, newName string) error {
		events = append(events, "rename:"+oldName+"->"+newName)
		return originalRename(root, oldName, newName)
	}
	syncLocalBackupDirectory = func(path string) error {
		events = append(events, "dir:"+path)
		return originalSyncDir(path)
	}
	syncLocalBackupDirectoryHandle = func(dir *os.File) error {
		events = append(events, "dir:"+snapshotRoot)
		return originalSyncDirHandle(dir)
	}
	afterFinalizeLocalBackupSnapshot = func(path string) {
		events = append(events, "evidence:"+path)
	}

	result, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	partialPath := result.SnapshotPath + ".partial"
	treeIndex := eventIndex(events, "tree:"+partialPath)
	renameIndex := eventPrefixIndex(events, "rename:"+filepath.Base(partialPath)+"->"+filepath.Base(result.SnapshotPath))
	parentSyncIndex := eventIndex(events, "dir:"+snapshotRoot)
	evidenceIndex := eventIndex(events, "evidence:"+result.SnapshotPath)
	if treeIndex < 0 || renameIndex < 0 || parentSyncIndex < 0 || evidenceIndex < 0 {
		t.Fatalf("durability events = %#v, want staging tree, rename, parent sync, and evidence", events)
	}
	if !(treeIndex < renameIndex && renameIndex < parentSyncIndex && parentSyncIndex < evidenceIndex) {
		t.Fatalf("durability event order = %#v", events)
	}
	createdAncestorSyncIndex := eventIndex(events, "dir:"+filepath.Dir(snapshotRoot))
	if createdAncestorSyncIndex < 0 || createdAncestorSyncIndex > treeIndex {
		t.Fatalf("created ancestor sync order = %#v", events)
	}
}

func TestManager_RunJobPropagatesLocalSnapshotDurabilityFailures(t *testing.T) {
	tests := []struct {
		name              string
		installFailure    func(t *testing.T, snapshotRoot string, injected error, renamed, parentSynced *bool)
		wantFinalSnapshot bool
		wantRenamed       bool
		wantParentSync    bool
	}{
		{
			name: "staging tree sync",
			installFailure: func(t *testing.T, _ string, injected error, _, _ *bool) {
				original := syncLocalBackupSnapshotTree
				t.Cleanup(func() { syncLocalBackupSnapshotTree = original })
				syncLocalBackupSnapshotTree = func(*os.File, string) error { return injected }
			},
		},
		{
			name: "snapshot rename",
			installFailure: func(t *testing.T, _ string, injected error, renamed, _ *bool) {
				original := renameLocalBackupSnapshot
				t.Cleanup(func() { renameLocalBackupSnapshot = original })
				renameLocalBackupSnapshot = func(*os.Root, string, string) error {
					*renamed = true
					return injected
				}
			},
			wantRenamed: true,
		},
		{
			name: "rename parent sync",
			installFailure: func(t *testing.T, _ string, injected error, renamed, parentSynced *bool) {
				originalRename := renameLocalBackupSnapshot
				originalSyncDirHandle := syncLocalBackupDirectoryHandle
				t.Cleanup(func() {
					renameLocalBackupSnapshot = originalRename
					syncLocalBackupDirectoryHandle = originalSyncDirHandle
				})
				renameLocalBackupSnapshot = func(root *os.Root, oldName, newName string) error {
					*renamed = true
					return originalRename(root, oldName, newName)
				}
				syncLocalBackupDirectoryHandle = func(*os.File) error {
					*parentSynced = true
					return injected
				}
			},
			wantFinalSnapshot: true,
			wantRenamed:       true,
			wantParentSync:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			destination := filepath.Join(tmpDir, "backups")
			runAt := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
			runID := formatRunID(runAt)
			snapshotRoot := filepath.Join(destination, "home", "snapshots")
			finalPath := filepath.Join(snapshotRoot, runID)
			mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
			manager, err := newBackupTestManager(t, ManagerConfig{
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
			manager.now = func() time.Time { return runAt }
			injected := errors.New("injected local snapshot durability failure")
			renamed := false
			parentSynced := false
			tt.installFailure(t, snapshotRoot, injected, &renamed, &parentSynced)

			result, err := manager.RunJob(context.Background(), "home")
			if !errors.Is(err, injected) {
				t.Fatalf("RunJob() error = %v, want %v", err, injected)
			}
			if result == nil || result.Status != StatusFailed {
				t.Fatalf("RunJob() result = %+v, want failed", result)
			}
			if renamed != tt.wantRenamed || parentSynced != tt.wantParentSync {
				t.Fatalf("rename/parent sync = %v/%v, want %v/%v", renamed, parentSynced, tt.wantRenamed, tt.wantParentSync)
			}
			_, statErr := os.Stat(finalPath)
			if tt.wantFinalSnapshot && statErr != nil {
				t.Fatalf("final snapshot stat error = %v, want present", statErr)
			}
			if !tt.wantFinalSnapshot && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("final snapshot stat error = %v, want not exist", statErr)
			}
			if _, partialErr := os.Stat(finalPath + ".partial"); !errors.Is(partialErr, os.ErrNotExist) {
				t.Fatalf("partial snapshot stat error = %v, want not exist", partialErr)
			}
			view, viewErr := manager.GetJob("home")
			if viewErr != nil {
				t.Fatalf("GetJob() error: %v", viewErr)
			}
			if view.LastSuccessfulRun != nil {
				t.Fatalf("LastSuccessfulRun = %+v, want nil", view.LastSuccessfulRun)
			}
			_, previewErr := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: filepath.Join(tmpDir, "restore")})
			if !errors.Is(previewErr, ErrNoSnapshots) {
				t.Fatalf("RunRestorePreview() error = %v, want %v", previewErr, ErrNoSnapshots)
			}
		})
	}
}

func TestManager_RunJobReconcilesCommittedRenameError(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
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

	originalRename := renameLocalBackupSnapshot
	t.Cleanup(func() { renameLocalBackupSnapshot = originalRename })
	injected := errors.New("injected ambiguous rename error")
	renameLocalBackupSnapshot = func(root *os.Root, partialName, finalName string) error {
		if err := originalRename(root, partialName, finalName); err != nil {
			return err
		}
		return injected
	}

	result, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error after committed rename: %v", err)
	}
	if result == nil || result.Status != StatusCompleted {
		t.Fatalf("RunJob() result = %+v, want completed", result)
	}
	if _, statErr := os.Stat(result.SnapshotPath); statErr != nil {
		t.Fatalf("final snapshot stat error: %v", statErr)
	}
	if _, statErr := os.Stat(result.SnapshotPath + ".partial"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial snapshot stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunJobPreservesReplacementAtInvalidFinalCleanup(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
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

	originalAfterFinalize := afterFinalizeLocalBackupSnapshot
	t.Cleanup(func() { afterFinalizeLocalBackupSnapshot = originalAfterFinalize })
	var replacementPath string
	afterFinalizeLocalBackupSnapshot = func(finalPath string) {
		replacementPath = finalPath
		movedPath := finalPath + ".moved"
		if err := os.Rename(finalPath, movedPath); err != nil {
			t.Fatalf("Rename(final snapshot) error: %v", err)
		}
		if err := os.Mkdir(finalPath, 0o700); err != nil {
			t.Fatalf("Mkdir(replacement snapshot) error: %v", err)
		}
		mustWriteFile(t, filepath.Join(finalPath, "replacement.txt"), "keep")
	}

	result, err := manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want %v", err, ErrUnsafePath)
	}
	if result == nil || result.Status != StatusFailed {
		t.Fatalf("RunJob() result = %+v, want failed", result)
	}
	if replacementPath == "" {
		t.Fatal("finalize hook did not run")
	}
	assertFileContent(t, filepath.Join(replacementPath, "replacement.txt"), "keep")
}

func eventIndex(events []string, want string) int {
	for index, event := range events {
		if event == want {
			return index
		}
	}
	return -1
}

func eventPrefixIndex(events []string, want string) int {
	for index, event := range events {
		if strings.HasPrefix(event, want) {
			return index
		}
	}
	return -1
}
