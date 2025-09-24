package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
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
	if cfg.WeComEnabled || cfg.WeComWebhookURL != "" {
		t.Fatalf("unexpected WeCom defaults: enabled=%v url=%q", cfg.WeComEnabled, cfg.WeComWebhookURL)
	}
	if cfg.DingTalkEnabled || cfg.DingTalkWebhookURL != "" {
		t.Fatalf("unexpected DingTalk defaults: enabled=%v url=%q", cfg.DingTalkEnabled, cfg.DingTalkWebhookURL)
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
	originalOnAlertMonitorLoopStart := onAlertMonitorLoopStart
	defer func() {
		onAlertMonitorLoopStart = originalOnAlertMonitorLoopStart
	}()
	started := make(chan struct{}, 1)
	onAlertMonitorLoopStart = func(context.Context) {
		started <- struct{}{}
	}

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
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for monitor loop start")
	}
	monitor.Stop()
}

func captureAlertMonitorChecks(t *testing.T) <-chan *StorageStats {
	t.Helper()

	checked := make(chan *StorageStats, 4)
	originalOnAlertMonitorCheckComplete := onAlertMonitorCheckComplete
	onAlertMonitorCheckComplete = func(_ context.Context, stats *StorageStats) {
		select {
		case checked <- stats:
		default:
		}
	}
	t.Cleanup(func() {
		onAlertMonitorCheckComplete = originalOnAlertMonitorCheckComplete
	})
	return checked
}

func waitForAlertMonitorCheck(t *testing.T, checked <-chan *StorageStats) *StorageStats {
	t.Helper()

	select {
	case stats := <-checked:
		if stats == nil {
			t.Fatal("expected alert monitor check stats, got nil")
		}
		return stats
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for alert monitor check")
		return nil
	}
}

func TestUpdateConfig_StartsMonitorAfterEnable(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	checked := captureAlertMonitorChecks(t)
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

	if stats := waitForAlertMonitorCheck(t, checked); stats == nil {
		t.Fatal("expected monitor to collect stats after enabling via UpdateConfig")
	}
}

func TestStart_IgnoresNonPositiveIntervalAndRecoversAfterUpdate(t *testing.T) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	checked := captureAlertMonitorChecks(t)
	monitor := NewMonitor(Config{
		Enabled:       true,
		CheckInterval: 0,
		ThresholdPct:  99.9,
		CriticalPct:   99.99,
		MinFreeBytes:  1,
	}, "/tmp", logger)
	monitor.Start(context.Background())
	t.Cleanup(func() { monitor.Stop() })

	monitor.mu.Lock()
	cancel := monitor.cancel
	monitor.mu.Unlock()
	if cancel != nil {
		t.Fatal("expected non-positive interval not to start monitor loop")
	}
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

	if stats := waitForAlertMonitorCheck(t, checked); stats == nil {
		t.Fatal("expected monitor to recover after updating to a positive interval")
	}
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
		if req.query["path"] != storageAlertPathOmitted {
			t.Fatalf("path query = %q, want %q", req.query["path"], storageAlertPathOmitted)
		}
		if req.query["path_scope"] != storageAlertPathScopeConfiguredRoot {
			t.Fatalf("path_scope query = %q, want %q", req.query["path_scope"], storageAlertPathScopeConfiguredRoot)
		}
		if strings.Contains(req.query["path"], "/srv/mnemonas") {
			t.Fatalf("GET webhook path query leaked storage path: %q", req.query["path"])
		}
		if req.query["used_pct"] != "90" {
			t.Fatalf("used_pct query = %q, want %q", req.query["used_pct"], "90")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for GET webhook request")
	}
}

func TestSendWebhook_PostOmitsStoragePath(t *testing.T) {
	reqCh := make(chan AlertPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload AlertPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(webhook request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendWebhook(context.Background(), AlertPayload{
		Type:      "storage_alert",
		Level:     AlertLevelCritical,
		Message:   "disk almost full",
		Timestamp: time.Unix(1710000000, 123).UTC(),
		Hostname:  "mnemonas-host",
		Stats: StorageStats{
			Path:       "/srv/mnemonas/private-project",
			TotalBytes: 100,
			FreeBytes:  10,
			UsedBytes:  90,
			UsedPct:    90,
			CheckedAt:  time.Unix(1710000001, 456).UTC(),
		},
	}, Config{
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodPost,
	})
	if err != nil {
		t.Fatalf("sendWebhook() error: %v", err)
	}

	select {
	case payload := <-reqCh:
		if payload.Stats.Path != storageAlertPathOmitted {
			t.Fatalf("webhook payload path = %q, want %q", payload.Stats.Path, storageAlertPathOmitted)
		}
		if payload.Stats.PathScope != storageAlertPathScopeConfiguredRoot {
			t.Fatalf("webhook payload path_scope = %q, want %q", payload.Stats.PathScope, storageAlertPathScopeConfiguredRoot)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal webhook payload: %v", err)
		}
		if strings.Contains(string(payloadJSON), "/srv/mnemonas") || strings.Contains(string(payloadJSON), "private-project") {
			t.Fatalf("webhook payload leaked storage path: %s", payloadJSON)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook request")
	}
}

func TestSendEvent_PostsGenericWebhookPayload(t *testing.T) {
	reqCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll(request body) error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reqCh <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:       true,
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodPost,
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_run",
		Level:     AlertLevelCritical,
		Message:   "backup failed",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
			"status": "failed",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case body := <-reqCh:
		var payload EventPayload
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("decode event payload: %v; body=%s", err, body)
		}
		if payload.Type != "backup_run" || payload.Level != AlertLevelCritical || payload.Message != "backup failed" {
			t.Fatalf("unexpected event payload: %+v", payload)
		}
		if payload.Details["job_id"] != "external-disk" {
			t.Fatalf("job_id detail = %v, want external-disk", payload.Details["job_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event webhook request")
	}
}

func TestSendEvent_DefaultsTimestampToUTC(t *testing.T) {
	reqCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll(request body) error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reqCh <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:       true,
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodPost,
	}, t.TempDir(), zerolog.Nop())

	if err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"}); err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case body := <-reqCh:
		var payload EventPayload
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("decode event payload: %v; body=%s", err, body)
		}
		if payload.Timestamp.IsZero() || payload.Timestamp.Location() != time.UTC {
			t.Fatalf("payload timestamp = %v (%v), want non-zero UTC", payload.Timestamp, payload.Timestamp.Location())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event webhook request")
	}
}

func TestSendEventSendsEmailWhenWebhookIsNotConfigured(t *testing.T) {
	originalSendSMTPMail := sendSMTPMail
	defer func() { sendSMTPMail = originalSendSMTPMail }()

	type smtpRequest struct {
		addr string
		auth smtp.Auth
		from string
		to   []string
		msg  string
	}
	reqCh := make(chan smtpRequest, 1)
	sendSMTPMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		reqCh <- smtpRequest{
			addr: addr,
			auth: auth,
			from: from,
			to:   append([]string(nil), to...),
			msg:  string(msg),
		}
		return nil
	}

	monitor := NewMonitor(Config{
		Enabled:      true,
		EmailEnabled: true,
		SMTPHost:     "smtp.example.com",
		SMTPPort:     587,
		SMTPUsername: "alerts",
		SMTPPassword: "secret",
		SMTPFrom:     "MnemoNAS <alerts@example.com>",
		SMTPTo:       []string{"admin@example.com"},
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_run",
		Level:     AlertLevelCritical,
		Message:   "backup failed",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
			"status": "failed",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.addr != "smtp.example.com:587" {
			t.Fatalf("smtp addr = %q, want smtp.example.com:587", req.addr)
		}
		if req.from != "MnemoNAS <alerts@example.com>" {
			t.Fatalf("from = %q, want configured sender", req.from)
		}
		if len(req.to) != 1 || req.to[0] != "admin@example.com" {
			t.Fatalf("recipients = %#v, want admin@example.com", req.to)
		}
		if !strings.Contains(req.msg, "Subject:") || !strings.Contains(req.msg, "backup failed") || !strings.Contains(req.msg, "external-disk") {
			t.Fatalf("email message did not include expected content:\n%s", req.msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for email alert")
	}
}

func TestEmailErrorDoesNotExposeSMTPSettings(t *testing.T) {
	originalSendSMTPMail := sendSMTPMail
	defer func() { sendSMTPMail = originalSendSMTPMail }()

	sendSMTPMail = func(string, smtp.Auth, string, []string, []byte) error {
		return errors.New("smtp.example.com rejected alerts@example.com with smtp-password for admin@example.com")
	}

	monitor := NewMonitor(Config{
		Enabled:      true,
		EmailEnabled: true,
		SMTPHost:     "smtp.example.com",
		SMTPPort:     587,
		SMTPUsername: "alerts@example.com",
		SMTPPassword: "smtp-password",
		SMTPFrom:     "MnemoNAS <alerts@example.com>",
		SMTPTo:       []string{"admin@example.com"},
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"})
	if err == nil {
		t.Fatal("expected email send error")
	}
	text := err.Error()
	for _, leaked := range []string{
		"smtp.example.com",
		"alerts@example.com",
		"smtp-password",
		"admin@example.com",
		"rejected",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("email error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "send email alert failed") {
		t.Fatalf("email error should retain generic failure summary, got %s", text)
	}
}

func TestSendEventSendsTelegramWhenConfigured(t *testing.T) {
	originalTelegramAPIBaseURL := telegramAPIBaseURL
	defer func() { telegramAPIBaseURL = originalTelegramAPIBaseURL }()

	type telegramRequest struct {
		path    string
		payload telegramMessagePayload
	}
	reqCh := make(chan telegramRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload telegramMessagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(telegram request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- telegramRequest{path: r.URL.Path, payload: payload}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	telegramAPIBaseURL = server.URL

	monitor := NewMonitor(Config{
		Enabled:          true,
		TelegramEnabled:  true,
		TelegramBotToken: "123456:secret-token",
		TelegramChatID:   "-1001234567890",
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_run",
		Level:     AlertLevelCritical,
		Message:   "backup failed",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
			"status": "failed",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.path != "/bot123456:secret-token/sendMessage" {
			t.Fatalf("telegram path = %q, want bot sendMessage path", req.path)
		}
		if req.payload.ChatID != "-1001234567890" {
			t.Fatalf("chat_id = %q, want configured chat", req.payload.ChatID)
		}
		if !strings.Contains(req.payload.Text, "backup failed") || !strings.Contains(req.payload.Text, "external-disk") {
			t.Fatalf("telegram text missing event details:\n%s", req.payload.Text)
		}
		if !req.payload.DisableWebPagePreview {
			t.Fatal("expected Telegram link previews to be disabled")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Telegram alert")
	}
}

func TestTelegramErrorDoesNotExposeBotToken(t *testing.T) {
	originalTelegramAPIBaseURL := telegramAPIBaseURL
	defer func() { telegramAPIBaseURL = originalTelegramAPIBaseURL }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	telegramAPIBaseURL = server.URL

	monitor := NewMonitor(Config{
		Enabled:          true,
		TelegramEnabled:  true,
		TelegramBotToken: "123456:secret-token",
		TelegramChatID:   "-1001234567890",
	}, t.TempDir(), zerolog.Nop())
	err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"})
	if err == nil {
		t.Fatal("expected Telegram send error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "123456") {
		t.Fatalf("Telegram error leaked bot token: %s", err)
	}
}

func TestSendEventSendsWeComWhenConfigured(t *testing.T) {
	type weComRequest struct {
		path    string
		key     string
		payload weComTextPayload
	}
	reqCh := make(chan weComRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload weComTextPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(wecom request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- weComRequest{
			path:    r.URL.Path,
			key:     r.URL.Query().Get("key"),
			payload: payload,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:         true,
		WeComEnabled:    true,
		WeComWebhookURL: server.URL + "/cgi-bin/webhook/send?key=secret-key",
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_run",
		Level:     AlertLevelCritical,
		Message:   "backup failed",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
			"status": "failed",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.path != "/cgi-bin/webhook/send" || req.key != "secret-key" {
			t.Fatalf("wecom target = %q key=%q, want webhook path and key", req.path, req.key)
		}
		if req.payload.MsgType != "text" {
			t.Fatalf("wecom msgtype = %q, want text", req.payload.MsgType)
		}
		if !strings.Contains(req.payload.Text.Content, "backup failed") || !strings.Contains(req.payload.Text.Content, "external-disk") {
			t.Fatalf("wecom text missing event details:\n%s", req.payload.Text.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WeCom alert")
	}
}

func TestSendStorageWeComPostsTextPayload(t *testing.T) {
	reqCh := make(chan weComTextPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload weComTextPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(wecom request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer server.Close()

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendStorageWeCom(context.Background(), &StorageStats{
		Path:      "/srv/mnemonas",
		UsedBytes: 90,
		FreeBytes: 10,
		UsedPct:   90,
		Level:     AlertLevelWarning,
		CheckedAt: time.Unix(1710000001, 0).UTC(),
	}, Config{
		WeComEnabled:    true,
		WeComWebhookURL: server.URL + "?key=secret-key",
	}, "存储空间告警：使用率 90.0%，剩余 10 B", "mnemonas-host")
	if err != nil {
		t.Fatalf("sendStorageWeCom() error: %v", err)
	}

	select {
	case payload := <-reqCh:
		if payload.MsgType != "text" {
			t.Fatalf("wecom msgtype = %q, want text", payload.MsgType)
		}
		for _, expected := range []string{"storage warning", "mnemonas-host", "Path scope: configured_storage_root", "90.0%"} {
			if !strings.Contains(payload.Text.Content, expected) {
				t.Fatalf("wecom storage text missing %q:\n%s", expected, payload.Text.Content)
			}
		}
		if strings.Contains(payload.Text.Content, "/srv/mnemonas") {
			t.Fatalf("wecom storage text leaked storage path:\n%s", payload.Text.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WeCom storage alert")
	}
}

func TestWeComErrorDoesNotExposeWebhookKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"invalid key"}`))
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:         true,
		WeComEnabled:    true,
		WeComWebhookURL: server.URL + "/cgi-bin/webhook/send?key=secret-key",
	}, t.TempDir(), zerolog.Nop())
	err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"})
	if err == nil {
		t.Fatal("expected WeCom send error")
	}
	text := err.Error()
	for _, leaked := range []string{"secret-key", "/cgi-bin/webhook/send", "invalid key"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("WeCom error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, server.URL) || !strings.Contains(text, "errcode 93000") {
		t.Fatalf("WeCom error should retain sanitized host and errcode, got %s", text)
	}
}

func TestSendEventSendsDingTalkWhenConfigured(t *testing.T) {
	type dingTalkRequest struct {
		path        string
		accessToken string
		payload     dingTalkTextPayload
	}
	reqCh := make(chan dingTalkRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload dingTalkTextPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(dingtalk request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- dingTalkRequest{
			path:        r.URL.Path,
			accessToken: r.URL.Query().Get("access_token"),
			payload:     payload,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:            true,
		DingTalkEnabled:    true,
		DingTalkWebhookURL: server.URL + "/robot/send?access_token=secret-token",
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_run",
		Level:     AlertLevelCritical,
		Message:   "backup failed",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
			"status": "failed",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.path != "/robot/send" || req.accessToken != "secret-token" {
			t.Fatalf("dingtalk target = %q access_token=%q, want webhook path and token", req.path, req.accessToken)
		}
		if req.payload.MsgType != "text" {
			t.Fatalf("dingtalk msgtype = %q, want text", req.payload.MsgType)
		}
		if !strings.Contains(req.payload.Text.Content, "backup failed") || !strings.Contains(req.payload.Text.Content, "external-disk") {
			t.Fatalf("dingtalk text missing event details:\n%s", req.payload.Text.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DingTalk alert")
	}
}

func TestSendStorageDingTalkPostsTextPayload(t *testing.T) {
	reqCh := make(chan dingTalkTextPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload dingTalkTextPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Decode(dingtalk request) error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reqCh <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer server.Close()

	monitor := NewMonitor(Config{}, t.TempDir(), zerolog.Nop())
	err := monitor.sendStorageDingTalk(context.Background(), &StorageStats{
		Path:      "/srv/mnemonas",
		UsedBytes: 90,
		FreeBytes: 10,
		UsedPct:   90,
		Level:     AlertLevelWarning,
		CheckedAt: time.Unix(1710000001, 0).UTC(),
	}, Config{
		DingTalkEnabled:    true,
		DingTalkWebhookURL: server.URL + "?access_token=secret-token",
	}, "存储空间告警：使用率 90.0%，剩余 10 B", "mnemonas-host")
	if err != nil {
		t.Fatalf("sendStorageDingTalk() error: %v", err)
	}

	select {
	case payload := <-reqCh:
		if payload.MsgType != "text" {
			t.Fatalf("dingtalk msgtype = %q, want text", payload.MsgType)
		}
		for _, expected := range []string{"storage warning", "mnemonas-host", "Path scope: configured_storage_root", "90.0%"} {
			if !strings.Contains(payload.Text.Content, expected) {
				t.Fatalf("dingtalk storage text missing %q:\n%s", expected, payload.Text.Content)
			}
		}
		if strings.Contains(payload.Text.Content, "/srv/mnemonas") {
			t.Fatalf("dingtalk storage text leaked storage path:\n%s", payload.Text.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DingTalk storage alert")
	}
}

func TestDingTalkErrorDoesNotExposeWebhookToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"invalid token"}`))
	}))
	defer server.Close()
	monitor := NewMonitor(Config{
		Enabled:            true,
		DingTalkEnabled:    true,
		DingTalkWebhookURL: server.URL + "/robot/send?access_token=secret-token",
	}, t.TempDir(), zerolog.Nop())
	err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"})
	if err == nil {
		t.Fatal("expected DingTalk send error")
	}
	text := err.Error()
	for _, leaked := range []string{"secret-token", "/robot/send", "invalid token"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("DingTalk error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, server.URL) || !strings.Contains(text, "errcode 310000") {
		t.Fatalf("DingTalk error should retain sanitized host and errcode, got %s", text)
	}
}

func TestSendEvent_GETEncodesDetailsInQuery(t *testing.T) {
	reqCh := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := make(map[string]string)
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		reqCh <- query
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:       true,
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodGet,
	}, t.TempDir(), zerolog.Nop())

	err := monitor.SendEvent(context.Background(), EventPayload{
		Type:      "backup_restore_drill",
		Level:     AlertLevelWarning,
		Message:   "restore drill warning",
		Timestamp: time.Unix(1710000000, 0).UTC(),
		Hostname:  "mnemonas-host",
		Details: map[string]any{
			"job_id": "external-disk",
		},
	})
	if err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}

	select {
	case query := <-reqCh:
		if query["type"] != "backup_restore_drill" {
			t.Fatalf("type query = %q, want backup_restore_drill", query["type"])
		}
		if query["level"] != string(AlertLevelWarning) {
			t.Fatalf("level query = %q, want warning", query["level"])
		}
		if !strings.Contains(query["details"], "external-disk") {
			t.Fatalf("details query = %q, want job id", query["details"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event webhook request")
	}
}

func TestSendEventDisabledDoesNotSend(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := NewMonitor(Config{
		Enabled:       false,
		WebhookURL:    server.URL,
		WebhookMethod: http.MethodPost,
	}, t.TempDir(), zerolog.Nop())

	if err := monitor.SendEvent(context.Background(), EventPayload{Type: "backup_run"}); err != nil {
		t.Fatalf("SendEvent() error: %v", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("disabled SendEvent sent %d requests, want 0", got)
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
