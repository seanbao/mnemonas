//go:build cgo
// +build cgo

package storage

import (
	"bytes"
	"context"
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
