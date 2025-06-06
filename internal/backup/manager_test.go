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
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml"), "[server]\nport = 8080\n")
	if _, err := os.Stat(filepath.Join(restoreTarget, "cache", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded restored file stat error = %v, want not exist", err)
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
	if len(jobs) != 1 || jobs[0].LastRun == nil || jobs[0].LastRestoreDrill == nil {
		t.Fatalf("reloaded jobs missing persisted status: %+v", jobs)
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

	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: filepath.Join(source, "restored")})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() source target error = %v, want ErrUnsafePath", err)
	}

	existingTarget := filepath.Join(tmpDir, "existing")
	mustWriteFile(t, filepath.Join(existingTarget, "old.txt"), "old")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: existingTarget})
	if !errors.Is(err, ErrRestoreTargetExists) {
		t.Fatalf("RunRestore() existing target error = %v, want ErrRestoreTargetExists", err)
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
	if result.Destination != "rest:http://backup.example/repo" || result.PrunedSnapshots != 0 || result.Warning {
		t.Fatalf("unexpected RunJob result: %+v", result)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "restic-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "restic-restore-target")
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

	calls := readCommandCalls(t, logPath)
	if len(calls) != 4 {
		t.Fatalf("command call count = %d, want 4: %#v", len(calls), calls)
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
	assertCommandArgs(t, calls[2], calls[1])
	assertCommandArgs(t, calls[3], []string{
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
	commandPath, logPath := newRecordingCommand(t, 0, "")
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

	drill, err := manager.RunRestoreDrill(context.Background(), "rclone-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "rclone-restore-target")
	restore, err := manager.RunRestore(context.Background(), "rclone-remote", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || restore.TargetPath != restoreTarget || restore.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore result: %+v", restore)
	}
	if _, err := os.Stat(restoreTarget); err != nil {
		t.Fatalf("restored target stat error: %v", err)
	}

	calls := readCommandCalls(t, logPath)
	if len(calls) != 5 {
		t.Fatalf("command call count = %d, want 5: %#v", len(calls), calls)
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
	assertCommandArgs(t, calls[2], calls[1])
	assertCommandArgs(t, calls[3], []string{
		"--config", configFile,
		"copy", "backup:mnemonas/source", restoreTarget + ".partial-" + restore.ID,
		"--create-empty-src-dirs",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[4], []string{
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
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n" +
		"restore_target=''\n" +
		"prev=''\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = '--target' ]; then restore_target=$arg; fi\n" +
		"  prev=$arg\n" +
		"done\n" +
		"if [ -n \"$restore_target\" ]; then\n" +
		"  mkdir -p \"$restore_target\"/" + shellQuote(restoredDir) + "\n" +
		"  printf '%s' 'restic restored' > \"$restore_target\"/" + shellQuote(restoredFile) + "\n" +
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
