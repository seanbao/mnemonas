package alerts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Enabled {
		t.Fatal("default alerts config should be disabled")
	}
	if cfg.CheckInterval != time.Hour {
		t.Fatalf("CheckInterval = %v, want %v", cfg.CheckInterval, time.Hour)
	}
	if cfg.ThresholdPct != 90.0 || cfg.CriticalPct != 95.0 {
		t.Fatalf("unexpected threshold defaults: warning=%v critical=%v", cfg.ThresholdPct, cfg.CriticalPct)
	}
	if cfg.MinFreeBytes != 10*1024*1024*1024 {
		t.Fatalf("MinFreeBytes = %d, want 10 GiB", cfg.MinFreeBytes)
	}
	if cfg.CooldownPeriod != 4*time.Hour {
		t.Fatalf("CooldownPeriod = %v, want %v", cfg.CooldownPeriod, 4*time.Hour)
	}
	if cfg.WebhookMethod != "POST" {
		t.Fatalf("WebhookMethod = %q, want POST", cfg.WebhookMethod)
	}
}

func TestFormatBytes(t *testing.T) {
	for name, tt := range map[string]struct {
		bytes uint64
		want  string
	}{
		"bytes":     {bytes: 512, want: "512 B"},
		"kilobytes": {bytes: 1536, want: "1.5 KB"},
		"megabytes": {bytes: 3 * 1024 * 1024, want: "3.0 MB"},
		"gigabytes": {bytes: 5 * 1024 * 1024 * 1024, want: "5.0 GB"},
		"terabytes": {bytes: 2 * 1024 * 1024 * 1024 * 1024, want: "2.0 TB"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := formatBytes(tt.bytes); got != tt.want {
				t.Fatalf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

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

func TestCheck_DoesNotAdvanceCooldownWhenWebhookFails(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   150,
		CriticalPct:    200,
		MinFreeBytes:   ^uint64(0),
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}, t.TempDir(), zerolog.Nop())

	monitor.check(context.Background())
	monitor.check(context.Background())

	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected webhook failures to bypass cooldown suppression, got %d requests", got)
	}
}

func TestCheck_ResolvedAlertResetsCooldown(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	warningCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   150,
		CriticalPct:    200,
		MinFreeBytes:   ^uint64(0),
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}
	noneCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   150,
		CriticalPct:    200,
		MinFreeBytes:   0,
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}

	monitor := NewMonitor(warningCfg, t.TempDir(), zerolog.Nop())
	monitor.check(context.Background())
	monitor.UpdateConfig(noneCfg)
	monitor.check(context.Background())
	monitor.UpdateConfig(warningCfg)
	monitor.check(context.Background())

	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected alert after resolution to bypass cooldown, got %d requests", got)
	}
	if stats := monitor.LastStats(); stats == nil || stats.Level != AlertLevelWarning {
		t.Fatalf("expected last stats level warning after re-alert, got %+v", stats)
	}
}

func TestCheck_CriticalRealertAfterSuppressedWarningTransition(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	criticalCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   0,
		CriticalPct:    0,
		MinFreeBytes:   0,
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}
	warningCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   0,
		CriticalPct:    200,
		MinFreeBytes:   0,
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}

	monitor := NewMonitor(criticalCfg, t.TempDir(), zerolog.Nop())
	monitor.check(context.Background())
	monitor.UpdateConfig(warningCfg)
	monitor.check(context.Background())
	monitor.UpdateConfig(criticalCfg)
	monitor.check(context.Background())

	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected critical re-alert after suppressed warning transition, got %d requests", got)
	}
}

func TestCheck_CriticalRetryAfterFailedCriticalSendBypassesWarningCooldown(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	warningCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   150,
		CriticalPct:    200,
		MinFreeBytes:   ^uint64(0),
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}
	criticalCfg := Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		ThresholdPct:   0,
		CriticalPct:    0,
		MinFreeBytes:   0,
		CooldownPeriod: time.Hour,
		WebhookURL:     server.URL,
		WebhookMethod:  http.MethodPost,
	}

	monitor := NewMonitor(warningCfg, t.TempDir(), zerolog.Nop())
	monitor.check(context.Background())
	monitor.UpdateConfig(criticalCfg)
	monitor.check(context.Background())
	monitor.check(context.Background())

	if got := requestCount.Load(); got != 3 {
		t.Fatalf("expected failed critical alert to retry immediately despite prior warning cooldown, got %d requests", got)
	}
}

func TestSendWebhook_TrimsConfiguredHeaders(t *testing.T) {
	reqCh := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCh <- r.Clone(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendWebhook(context.Background(), AlertPayload{Type: "storage_alert"}, Config{
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodPost,
		WebhookHeaders: []string{
			"Authorization: Bearer token",
			" X-MnemoNAS : alerts ",
			"InvalidHeader",
			"Bad Header: skipped",
			"X-Injected: ok\r\nX-Evil: injected",
		},
	})
	if err != nil {
		t.Fatalf("sendWebhook() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization header = %q, want %q", got, "Bearer token")
		}
		if got := req.Header.Get("X-MnemoNAS"); got != "alerts" {
			t.Fatalf("X-MnemoNAS header = %q, want %q", got, "alerts")
		}
		if got := req.Header.Get("InvalidHeader"); got != "" {
			t.Fatalf("InvalidHeader = %q, want empty", got)
		}
		if got := req.Header.Get("Bad Header"); got != "" {
			t.Fatalf("Bad Header = %q, want empty", got)
		}
		if got := req.Header.Get("X-Injected"); got != "" {
			t.Fatalf("X-Injected = %q, want empty", got)
		}
		if got := req.Header.Get("X-Evil"); got != "" {
			t.Fatalf("X-Evil = %q, want empty", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook request")
	}
}

func TestSendWebhook_GETEncodesPayloadInQueryWithoutBody(t *testing.T) {
	type webhookRequest struct {
		method string
		query  map[string]string
		body   string
	}

	reqCh := make(chan webhookRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll(request body) error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		query := make(map[string]string)
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		reqCh <- webhookRequest{
			method: r.Method,
			query:  query,
			body:   string(body),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	payload := AlertPayload{
		Type:      "storage_alert",
		Level:     AlertLevelCritical,
		Message:   "disk almost full",
		Timestamp: time.Unix(1710000000, 123).UTC(),
		Hostname:  "mnemonas-host",
		Stats: StorageStats{
			Path:       "/srv/mnemonas",
			TotalBytes: 100,
			FreeBytes:  10,
			UsedBytes:  90,
			UsedPct:    90,
			CheckedAt:  time.Unix(1710000001, 456).UTC(),
		},
	}

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendWebhook(context.Background(), payload, Config{
		WebhookURL:    server.URL + "?existing=1",
		WebhookMethod: http.MethodGet,
	})
	if err != nil {
		t.Fatalf("sendWebhook() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.method != http.MethodGet {
			t.Fatalf("method = %q, want %q", req.method, http.MethodGet)
		}
		if req.body != "" {
			t.Fatalf("expected GET webhook body to be empty, got %q", req.body)
		}
		if req.query["existing"] != "1" {
			t.Fatalf("existing query = %q, want %q", req.query["existing"], "1")
		}
		if req.query["type"] != payload.Type {
			t.Fatalf("type query = %q, want %q", req.query["type"], payload.Type)
		}
		if req.query["level"] != string(payload.Level) {
			t.Fatalf("level query = %q, want %q", req.query["level"], payload.Level)
		}
		if req.query["message"] != payload.Message {
			t.Fatalf("message query = %q, want %q", req.query["message"], payload.Message)
		}
		if req.query["path"] != payload.Stats.Path {
			t.Fatalf("path query = %q, want %q", req.query["path"], payload.Stats.Path)
		}
		if req.query["used_pct"] != "90" {
			t.Fatalf("used_pct query = %q, want %q", req.query["used_pct"], "90")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for GET webhook request")
	}
}

func TestRedactWebhookURLForLogDropsCredentialsPathAndQuery(t *testing.T) {
	got := redactWebhookURLForLog("https://user:pass@hooks.example.com/services/token/secret?key=value")
	if got != "https://hooks.example.com" {
		t.Fatalf("redactWebhookURLForLog() = %q, want %q", got, "https://hooks.example.com")
	}

	if got := redactWebhookURLForLog("not a url"); got != "configured" {
		t.Fatalf("redactWebhookURLForLog(invalid) = %q, want configured", got)
	}
}

func TestSendWebhookErrorDoesNotExposeConfiguredURLSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendWebhook(ctx, AlertPayload{
		Type:      "storage_alert",
		Level:     AlertLevelCritical,
		Message:   "disk full",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Stats: StorageStats{
			Path:      "/srv/mnemonas/private-project",
			UsedPct:   99,
			CheckedAt: time.Unix(1710000001, 0).UTC(),
		},
	}, Config{
		WebhookURL:    "https://user:pass@hooks.example.com/services/token/secret?key=value",
		WebhookMethod: http.MethodGet,
	})
	if err == nil {
		t.Fatal("expected sendWebhook() to return the canceled request error")
	}

	text := err.Error()
	for _, leaked := range []string{
		"user:pass",
		"/services/token/secret",
		"key=value",
		"private-project",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("sendWebhook() error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "https://hooks.example.com") {
		t.Fatalf("sendWebhook() error should retain sanitized target host, got %s", text)
	}
}
