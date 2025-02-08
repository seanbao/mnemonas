package alerts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNewMonitor(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	cfg := Config{
		Enabled:       true,
		CheckInterval: 1 * time.Hour,
		ThresholdPct:  90.0,
		CriticalPct:   95.0,
		MinFreeBytes:  10 * 1024 * 1024 * 1024,
	}

	monitor := NewMonitor(cfg, "/tmp", logger)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
	}
}

func TestMonitorDisabled(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	cfg := Config{
		Enabled: false,
	}

	monitor := NewMonitor(cfg, "/tmp", logger)
	if monitor == nil {
		t.Fatal("expected monitor to be created even when disabled")
	}
}

func TestCheck(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	cfg := Config{
		Enabled:       true,
		CheckInterval: 1 * time.Hour,
		ThresholdPct:  99.9,
		CriticalPct:   99.99,
		MinFreeBytes:  1,
	}

	monitor := NewMonitor(cfg, "/tmp", logger)

	stats, err := monitor.Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	if stats.TotalBytes == 0 {
		t.Error("expected non-zero total bytes")
	}

	if stats.Level != AlertLevelNone {
		t.Logf("alert level: %s, stats: %+v", stats.Level, stats)
	}
}

func TestAlertLevel(t *testing.T) {
	if AlertLevelNone != "none" {
		t.Errorf("AlertLevelNone = %q, want %q", AlertLevelNone, "none")
	}
	if AlertLevelWarning != "warning" {
		t.Errorf("AlertLevelWarning = %q, want %q", AlertLevelWarning, "warning")
	}
	if AlertLevelCritical != "critical" {
		t.Errorf("AlertLevelCritical = %q, want %q", AlertLevelCritical, "critical")
	}
}

func TestStartStop(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	cfg := Config{
		Enabled:       true,
		CheckInterval: 100 * time.Millisecond,
		ThresholdPct:  99.9,
		CriticalPct:   99.99,
		MinFreeBytes:  1,
	}

	monitor := NewMonitor(cfg, "/tmp", logger)

	ctx := context.Background()
	monitor.Start(ctx)
	time.Sleep(150 * time.Millisecond)
	monitor.Stop()
}

func TestUpdateConfig_StartsMonitorAfterEnable(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	monitor := NewMonitor(Config{Enabled: false}, "/tmp", logger)
	monitor.Start(context.Background())
	t.Cleanup(func() { monitor.Stop() })

	monitor.UpdateConfig(Config{
		Enabled:        true,
		CheckInterval:  50 * time.Millisecond,
		ThresholdPct:   99.9,
		CriticalPct:    99.99,
		MinFreeBytes:   1,
		CooldownPeriod: time.Second,
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if stats := monitor.LastStats(); stats != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("expected monitor to collect stats after enabling via UpdateConfig")
}
