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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		versions, err := fs.ListVersions(ctx, "/retention-monitor.txt")
		if err != nil {
			t.Fatalf("ListVersions() error: %v", err)
		}
		if len(versions) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	versions, err := fs.ListVersions(ctx, "/retention-monitor.txt")
	if err != nil {
		t.Fatalf("ListVersions() final error: %v", err)
	}
	t.Fatalf("expected current version plus one retained historical version after periodic sweep, got %d versions", len(versions))
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
