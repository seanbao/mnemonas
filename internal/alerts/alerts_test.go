package alerts

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
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
		ThresholdPct:  150,
		CriticalPct:   200,
		MinFreeBytes:  ^uint64(0),
	}

	monitor := NewMonitor(cfg, "/tmp", logger)

	stats, err := monitor.Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	if stats.TotalBytes == 0 {
		t.Error("expected non-zero total bytes")
	}
	if stats.Level != AlertLevelWarning {
		t.Fatalf("expected Check() to populate warning level, got %s", stats.Level)
	}
}

func TestLastStatsReturnsCopy(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	monitor := NewMonitor(Config{}, "/tmp", logger)
	monitor.lastStats = &StorageStats{
		Path:       "/tmp",
		TotalBytes: 100,
		FreeBytes:  50,
		UsedBytes:  50,
		UsedPct:    50,
		Level:      AlertLevelWarning,
		CheckedAt:  time.Now(),
	}

	stats := monitor.LastStats()
	if stats == nil {
		t.Fatal("expected LastStats() to return stats")
	}
	stats.Level = AlertLevelCritical
	stats.Path = "/mutated"

	again := monitor.LastStats()
	if again.Level != AlertLevelWarning {
		t.Fatalf("expected stored level to remain warning, got %s", again.Level)
	}
	if again.Path != "/tmp" {
		t.Fatalf("expected stored path to remain /tmp, got %s", again.Path)
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

func TestStart_IgnoresNonPositiveIntervalAndRecoversAfterUpdate(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	monitor := NewMonitor(Config{
		Enabled:       true,
		CheckInterval: 0,
		ThresholdPct:  99.9,
		CriticalPct:   99.99,
		MinFreeBytes:  1,
	}, "/tmp", logger)
	monitor.Start(context.Background())
	t.Cleanup(func() { monitor.Stop() })

	time.Sleep(100 * time.Millisecond)
	if stats := monitor.LastStats(); stats != nil {
		t.Fatalf("expected no stats to be collected for non-positive interval, got %+v", stats)
	}

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

	t.Fatal("expected monitor to recover after updating to a positive interval")
}

func TestUpdateConfig_SerializesConcurrentRestarts(t *testing.T) {
	originalOnAlertMonitorLoopStart := onAlertMonitorLoopStart
	defer func() {
		onAlertMonitorLoopStart = originalOnAlertMonitorLoopStart
	}()

	started := make(chan struct{}, 3)
	var activeLoops atomic.Int32
	var maxActiveLoops atomic.Int32
	onAlertMonitorLoopStart = func(ctx context.Context) {
		started <- struct{}{}
		current := activeLoops.Add(1)
		for {
			previous := maxActiveLoops.Load()
			if current <= previous || maxActiveLoops.CompareAndSwap(previous, current) {
				break
			}
		}
		<-ctx.Done()
		activeLoops.Add(-1)
	}

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	cfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   99.9,
		CriticalPct:    99.99,
		MinFreeBytes:   1,
		CooldownPeriod: time.Second,
	}

	monitor := NewMonitor(cfg, "/tmp", logger)
	monitor.Start(context.Background())
	defer monitor.Stop()

	for range 1 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for initial monitor loop start")
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
			t.Fatal("timed out waiting for restarted monitor loops")
		}
	}

	if got := maxActiveLoops.Load(); got != 1 {
		t.Fatalf("expected concurrent UpdateConfig calls to keep only one alert monitor loop active at a time, got max %d", got)
	}
}

func TestCheck_ConcurrentConfigUpdates(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	monitor := NewMonitor(Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   99.9,
		CriticalPct:    99.99,
		MinFreeBytes:   1,
		CooldownPeriod: time.Second,
		WebhookURL:     "https://example.invalid/alerts",
		WebhookMethod:  "POST",
		WebhookHeaders: []string{"X-Test: value"},
	}, "/tmp", logger)

	errCh := make(chan error, 32)
	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			if _, err := monitor.Check(); err != nil {
				errCh <- err
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			monitor.UpdateConfig(Config{
				Enabled:        true,
				CheckInterval:  time.Hour,
				ThresholdPct:   80 + float64(i%10),
				CriticalPct:    90 + float64(i%10),
				MinFreeBytes:   uint64(i + 1),
				CooldownPeriod: time.Second,
				WebhookURL:     "https://example.invalid/alerts",
				WebhookMethod:  "POST",
				WebhookHeaders: []string{"X-Test: value"},
			})
		}
	}()

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected concurrent Check()/UpdateConfig() error: %v", err)
	}
}
