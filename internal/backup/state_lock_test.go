package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

const backupStateLockHelperRootEnv = "MNEMONAS_TEST_BACKUP_STATE_LOCK_ROOT"

func TestMapBackupLockHeldErrorUsesOperationSpecificConflict(t *testing.T) {
	wrapped := fmt.Errorf("open lock handle: %w", ErrBackupStateLockHeld)
	if got := mapBackupLockHeldError(wrapped, ErrBackupTargetLockHeld); !errors.Is(got, ErrBackupTargetLockHeld) || errors.Is(got, ErrBackupStateLockHeld) {
		t.Fatalf("mapBackupLockHeldError() = %v, want only target-lock conflict", got)
	}
	other := errors.New("open failed")
	if got := mapBackupLockHeldError(other, ErrBackupTargetLockHeld); !errors.Is(got, other) {
		t.Fatalf("mapBackupLockHeldError(other) = %v, want original error", got)
	}
}

func TestManagerStateLockLifecycle(t *testing.T) {
	root := filepath.Join(secureBackupTestTempDir(t), "state")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	contender, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if contender != nil {
		_ = contender.Close()
		t.Fatal("second NewManager() acquired an already held state root")
	}
	if !errors.Is(err, ErrBackupStateLockHeld) {
		t.Fatalf("second NewManager() error = %v, want %v", err, ErrBackupStateLockHeld)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}

	reacquired, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if err != nil {
		t.Fatalf("NewManager() after Close error: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close() error: %v", err)
	}
}

func TestManagerStateLockRejectsCrossProcessContender(t *testing.T) {
	root := filepath.Join(secureBackupTestTempDir(t), "state")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	cmd := exec.Command(os.Args[0], "-test.run=^TestBackupStateLockHelperProcess$")
	cmd.Env = append(os.Environ(), backupStateLockHelperRootEnv+"="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper process error: %v\n%s", err, output)
	}
}

func TestBackupStateLockHelperProcess(t *testing.T) {
	root := os.Getenv(backupStateLockHelperRootEnv)
	if root == "" {
		return
	}
	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if manager != nil {
		_ = manager.Close()
		t.Fatal("helper unexpectedly acquired the backup state lock")
	}
	if !errors.Is(err, ErrBackupStateLockHeld) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrBackupStateLockHeld)
	}
}

func TestManagerStateLockRejectsSymlink(t *testing.T) {
	root := filepath.Join(secureBackupTestTempDir(t), "state")
	if err := ensureBackupStateRoot(root); err != nil {
		t.Fatalf("ensureBackupStateRoot() error: %v", err)
	}
	outside := filepath.Join(secureBackupTestTempDir(t), "outside.lock")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, backupStateLockFileName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{Root: root})
	if manager != nil {
		_ = manager.Close()
		t.Fatal("NewManager() accepted a symlink state lock")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("ReadFile(outside) error: %v", readErr)
	}
	if string(data) != "outside" {
		t.Fatalf("outside lock content = %q, want unchanged", data)
	}
}

func TestManagerCloseWaitsForActiveOperationAndRejectsNewWork(t *testing.T) {
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
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs:        []config.BackupJobConfig{job},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalAfterValidate := afterValidateLocalBackupDestination
	t.Cleanup(func() { afterValidateLocalBackupDestination = originalAfterValidate })
	entered := make(chan struct{})
	release := make(chan struct{})
	afterValidateLocalBackupDestination = func(string) {
		close(entered)
		<-release
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := manager.RunJob(context.Background(), job.ID)
		runDone <- runErr
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("backup operation did not reach the blocking hook")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before active operation completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if _, err := manager.RunJob(context.Background(), job.ID); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("RunJob() after Close error = %v, want %v", err, ErrManagerClosed)
	}
	if _, err := manager.AddJob(config.BackupJobConfig{ID: "other", Type: JobTypeLocal, Source: source, Destination: filepath.Join(tmpDir, "other")}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("AddJob() after Close error = %v, want %v", err, ErrManagerClosed)
	}
	if results := manager.RunDueJobs(context.Background()); len(results) != 0 {
		t.Fatalf("RunDueJobs() after Close = %+v, want no work", results)
	}
}

func TestManagerCloseDoesNotAdvanceUnstartedScheduledJobs(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	alphaDestination := filepath.Join(tmpDir, "alpha-backups")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{
			{ID: "alpha", Name: "Alpha", Type: JobTypeLocal, Source: source, Destination: alphaDestination, ScheduleInterval: time.Hour},
			{ID: "beta", Name: "Beta", Type: JobTypeLocal, Source: source, Destination: filepath.Join(tmpDir, "beta-backups"), ScheduleInterval: time.Hour},
		},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	originalAfterValidate := afterValidateLocalBackupDestination
	t.Cleanup(func() { afterValidateLocalBackupDestination = originalAfterValidate })
	entered := make(chan struct{})
	release := make(chan struct{})
	afterValidateLocalBackupDestination = func(destination string) {
		if destination != alphaDestination {
			return
		}
		close(entered)
		<-release
	}
	runDone := make(chan []ScheduledRunResult, 1)
	go func() { runDone <- manager.RunDueJobs(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first scheduled job did not reach blocking hook")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		manager.mu.Lock()
		closed := manager.closed
		manager.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close() did not mark manager closed")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	results := <-runDone
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if len(results) != 2 || results[0].Result == nil || results[0].Result.Status != StatusCompleted || !strings.Contains(results[1].Error, ErrManagerClosed.Error()) {
		t.Fatalf("RunDueJobs() results = %+v, want completed alpha and rejected beta", results)
	}
	manager.mu.Lock()
	alpha := manager.state.Jobs["alpha"]
	beta := manager.state.Jobs["beta"]
	manager.mu.Unlock()
	if alpha.LastScheduledRunAt == nil || alpha.LastRun == nil || alpha.LastRun.Status != StatusCompleted {
		t.Fatalf("alpha state = %+v, want completed scheduled run", alpha)
	}
	if beta.LastScheduledRunAt != nil || beta.LastRun != nil {
		t.Fatalf("beta state = %+v, want no marker for unstarted job", beta)
	}
}

func TestManagerCloseCancelsBlockingNotification(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	if err := os.Symlink("note.txt", filepath.Join(source, "note-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	started := make(chan struct{})
	stopped := make(chan struct{})
	notifier := NotifierFunc(func(ctx context.Context, _ NotificationEvent) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
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

	runDone := make(chan error, 1)
	go func() {
		_, runErr := manager.RunJob(context.Background(), "home")
		runDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("backup notification did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not cancel the blocking notification")
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if err := <-runDone; !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want %v", err, ErrSourceContainsSymlink)
	}
}
