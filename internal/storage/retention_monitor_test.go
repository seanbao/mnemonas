package storage

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestRetentionMonitor_UpdateConfig_AppliesPeriodicSweep(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	swept := make(chan error, 1)
	originalOnRetentionMonitorSweepComplete := onRetentionMonitorSweepComplete
	onRetentionMonitorSweepComplete = func(_ context.Context, err error) {
		select {
		case swept <- err:
		default:
		}
	}
	t.Cleanup(func() {
		onRetentionMonitorSweepComplete = originalOnRetentionMonitorSweepComplete
	})

	for _, content := range []string{"v1", "v2", "v3", "v4"} {
		if err := fs.WriteFile(ctx, "/retention-monitor.txt", bytes.NewReader([]byte(content))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", content, err)
		}
	}

	monitor := NewRetentionMonitor(fs, RetentionMonitorConfig{
		MaxVersions:   10,
		MaxVersionAge: 365 * 24 * time.Hour,
		MinFreeSpace:  0,
		SweepInterval: time.Hour,
	}, zerolog.Nop())
	monitor.Start(ctx)
	defer monitor.Stop()

	monitor.UpdateConfig(RetentionMonitorConfig{
		MaxVersions:   1,
		MaxVersionAge: 365 * 24 * time.Hour,
		MinFreeSpace:  0,
		SweepInterval: 20 * time.Millisecond,
	})

	select {
	case err := <-swept:
		if err != nil {
			t.Fatalf("retention sweep error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retention sweep")
	}

	versions, err := fs.ListVersions(ctx, "/retention-monitor.txt")
	if err != nil {
		t.Fatalf("ListVersions() error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected current version plus one retained historical version after periodic sweep, got %d versions", len(versions))
	}
}

func TestRetentionMonitor_UpdateConfigCancelsActiveSweepBeforePublishingPolicy(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweepEntered := make(chan struct{})
	var sweepEnteredOnce sync.Once
	fs.listVersionPaths = func(ctx context.Context) ([]string, error) {
		sweepEnteredOnce.Do(func() { close(sweepEntered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}

	monitor := NewRetentionMonitor(fs, RetentionMonitorConfig{
		MaxVersions:   10,
		MaxVersionAge: 30 * 24 * time.Hour,
		SweepInterval: 10 * time.Millisecond,
	}, zerolog.Nop())
	monitor.Start(ctx)
	defer monitor.Stop()

	select {
	case <-sweepEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active retention sweep")
	}

	updateDone := make(chan struct{})
	go func() {
		monitor.UpdateConfig(RetentionMonitorConfig{
			MaxVersions:   3,
			MaxVersionAge: 7 * 24 * time.Hour,
			SweepInterval: 0,
		})
		close(updateDone)
	}()

	select {
	case <-updateDone:
	case <-time.After(time.Second):
		t.Fatal("UpdateConfig deadlocked waiting for a sweep that required cancellation")
	}
	policy := fs.CurrentDeletePolicy()
	if policy.TrashAutoCleanupEnabled || policy.RetentionSweepInterval != 0 {
		t.Fatalf("delete policy after disabling retention monitor = %+v", policy)
	}
}

func TestRetentionMonitor_UpdateConfigAndRuntimePolicyCancelsActiveSweepBeforePublishingTrashOnlyChange(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sweepEntered := make(chan context.Context, 4)
	fs.listVersionPaths = func(ctx context.Context) ([]string, error) {
		sweepEntered <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}

	cfg := RetentionMonitorConfig{
		MaxVersions:   10,
		MaxVersionAge: 30 * 24 * time.Hour,
		MinFreeSpace:  1024,
		SweepInterval: 10 * time.Millisecond,
	}
	basePolicy := fs.CurrentDeletePolicy()
	fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
		MaxVersions:        cfg.MaxVersions,
		MaxVersionAge:      cfg.MaxVersionAge,
		MinFreeSpace:       cfg.MinFreeSpace,
		SweepInterval:      cfg.SweepInterval,
		TrashEnabled:       basePolicy.Mode == DeleteModeTrash,
		TrashRetentionDays: basePolicy.TrashRetentionDays,
		MaxTrashSize:       basePolicy.MaxTrashSize,
	})
	before := fs.CurrentDeletePolicy()
	monitor := NewRetentionMonitor(fs, cfg, zerolog.Nop())
	monitor.Start(ctx)
	t.Cleanup(monitor.Stop)

	select {
	case <-sweepEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active retention sweep")
	}

	policy := RuntimePolicySettings{
		MaxVersions:        cfg.MaxVersions,
		MaxVersionAge:      cfg.MaxVersionAge,
		MinFreeSpace:       cfg.MinFreeSpace,
		SweepInterval:      cfg.SweepInterval,
		TrashEnabled:       true,
		TrashRetentionDays: before.TrashRetentionDays + 1,
		MaxTrashSize:       before.MaxTrashSize + 4096,
	}
	updateDone := make(chan struct{})
	go func() {
		monitor.UpdateConfigAndRuntimePolicy(cfg, policy)
		close(updateDone)
	}()

	select {
	case <-updateDone:
	case <-time.After(time.Second):
		t.Fatal("UpdateConfigAndRuntimePolicy deadlocked waiting for a sweep that required cancellation")
	}
	after := fs.CurrentDeletePolicy()
	if after.Mode != DeleteModeTrash ||
		after.TrashRetentionDays != policy.TrashRetentionDays ||
		!after.TrashAutoCleanupEnabled ||
		after.RetentionSweepInterval != cfg.SweepInterval ||
		after.MaxTrashSize != policy.MaxTrashSize ||
		after.Token == before.Token {
		t.Fatalf("delete policy after transactional trash-only update = %+v, before=%+v", after, before)
	}

	select {
	case <-sweepEntered:
	case <-time.After(time.Second):
		t.Fatal("replacement retention loop did not run")
	}

	stopDone := make(chan struct{})
	go func() {
		monitor.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("replacement retention loop did not stop after cancellation")
	}
}

func TestRetentionMonitor_PeriodicSweepCleansExpiredTrash(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.UpdateTrashSettings(true, 0, 1<<20)
	if err := fs.WriteFile(ctx, "/expired-trash.txt", bytes.NewReader([]byte("expired"))); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := fs.Delete(ctx, "/expired-trash.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	swept := make(chan error, 1)
	originalOnRetentionMonitorSweepComplete := onRetentionMonitorSweepComplete
	onRetentionMonitorSweepComplete = func(_ context.Context, err error) {
		select {
		case swept <- err:
		default:
		}
	}
	t.Cleanup(func() {
		onRetentionMonitorSweepComplete = originalOnRetentionMonitorSweepComplete
	})

	monitor := NewRetentionMonitor(fs, RetentionMonitorConfig{
		MaxVersions:   10,
		MaxVersionAge: 365 * 24 * time.Hour,
		SweepInterval: 20 * time.Millisecond,
	}, zerolog.Nop())
	monitor.Start(ctx)
	defer monitor.Stop()
	if policy := fs.CurrentDeletePolicy(); !policy.TrashAutoCleanupEnabled {
		t.Fatalf("delete policy after monitor start = %+v, want automatic trash cleanup enabled", policy)
	}

	select {
	case err := <-swept:
		if err != nil {
			t.Fatalf("retention sweep error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retention sweep")
	}

	items, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash items = %d, want 0 after retention sweep", len(items))
	}
}

func TestRetentionMonitor_UpdateConfig_SerializesConcurrentRestarts(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	originalOnRetentionMonitorLoopStart := onRetentionMonitorLoopStart
	defer func() {
		onRetentionMonitorLoopStart = originalOnRetentionMonitorLoopStart
	}()

	started := make(chan struct{}, 3)
	var activeLoops atomic.Int32
	var maxActiveLoops atomic.Int32
	onRetentionMonitorLoopStart = func(loopCtx context.Context) {
		started <- struct{}{}
		current := activeLoops.Add(1)
		for {
			previous := maxActiveLoops.Load()
			if current <= previous || maxActiveLoops.CompareAndSwap(previous, current) {
				break
			}
		}
		<-loopCtx.Done()
		activeLoops.Add(-1)
	}

	cfg := RetentionMonitorConfig{
		MaxVersions:   10,
		MaxVersionAge: 365 * 24 * time.Hour,
		MinFreeSpace:  0,
		SweepInterval: time.Hour,
	}
	monitor := NewRetentionMonitor(fs, cfg, zerolog.Nop())
	monitor.Start(ctx)
	defer monitor.Stop()

	for range 1 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for initial retention monitor loop start")
		}
	}

	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			<-release
			monitor.UpdateConfig(cfg)
		}()
	}
	close(release)
	wg.Wait()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for restarted retention monitor loops")
		}
	}

	if got := maxActiveLoops.Load(); got != 1 {
		t.Fatalf("expected concurrent UpdateConfig calls to keep only one retention monitor loop active at a time, got max %d", got)
	}
}
