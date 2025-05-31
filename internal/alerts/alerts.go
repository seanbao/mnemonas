// Package alerts provides storage space monitoring and alerting
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Config holds alerting configuration
type Config struct {
	Enabled        bool          `toml:"enabled"`
	CheckInterval  time.Duration `toml:"check_interval"`  // How often to check (default 1h)
	ThresholdPct   float64       `toml:"threshold_pct"`   // Alert when usage exceeds this % (default 90)
	CriticalPct    float64       `toml:"critical_pct"`    // Critical alert threshold (default 95)
	MinFreeBytes   uint64        `toml:"min_free_bytes"`  // Alert when free space < this (default 10GB)
	CooldownPeriod time.Duration `toml:"cooldown_period"` // Min time between alerts (default 4h)
	WebhookURL     string        `toml:"webhook_url"`     // Webhook URL for notifications
	WebhookMethod  string        `toml:"webhook_method"`  // POST or GET (default POST)
	WebhookHeaders []string      `toml:"webhook_headers"` // Additional headers (key:value format)
}

// DefaultConfig returns default alerting configuration
func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		CheckInterval:  1 * time.Hour,
		ThresholdPct:   90.0,
		CriticalPct:    95.0,
		MinFreeBytes:   10 * 1024 * 1024 * 1024, // 10GB
		CooldownPeriod: 4 * time.Hour,
		WebhookMethod:  "POST",
	}
}

// AlertLevel represents the severity of an alert
type AlertLevel string

const (
	AlertLevelNone     AlertLevel = "none"
	AlertLevelWarning  AlertLevel = "warning"
	AlertLevelCritical AlertLevel = "critical"
)

// StorageStats holds storage statistics
type StorageStats struct {
	Path       string     `json:"path"`
	TotalBytes uint64     `json:"total_bytes"`
	FreeBytes  uint64     `json:"free_bytes"`
	UsedBytes  uint64     `json:"used_bytes"`
	UsedPct    float64    `json:"used_pct"`
	Level      AlertLevel `json:"level"`
	CheckedAt  time.Time  `json:"checked_at"`
}

func cloneStorageStats(stats *StorageStats) *StorageStats {
	if stats == nil {
		return nil
	}
	clone := *stats
	return &clone
}

func cloneConfig(cfg Config) Config {
	clone := cfg
	if cfg.WebhookHeaders != nil {
		clone.WebhookHeaders = append([]string(nil), cfg.WebhookHeaders...)
	}
	return clone
}

// AlertPayload is the webhook payload
type AlertPayload struct {
	Type      string       `json:"type"`
	Level     AlertLevel   `json:"level"`
	Message   string       `json:"message"`
	Stats     StorageStats `json:"stats"`
	Timestamp time.Time    `json:"timestamp"`
	Hostname  string       `json:"hostname"`
}

// Monitor monitors storage space and sends alerts
type Monitor struct {
	cfg     Config
	logger  zerolog.Logger
	dataDir string
	baseCtx context.Context

	lifecycleMu    sync.Mutex
	mu             sync.Mutex
	lastAlert      time.Time
	lastLevel      AlertLevel
	lastAlertLevel AlertLevel
	lastStats      *StorageStats

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var onAlertMonitorLoopStart = func(context.Context) {}

// NewMonitor creates a new storage monitor
func NewMonitor(cfg Config, dataDir string, logger zerolog.Logger) *Monitor {
	return &Monitor{
		cfg:            cloneConfig(cfg),
		logger:         logger,
		dataDir:        dataDir,
		lastLevel:      AlertLevelNone,
		lastAlertLevel: AlertLevelNone,
	}
}

// Start begins the monitoring loop
func (m *Monitor) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.mu.Lock()
	m.baseCtx = ctx
	cfg := cloneConfig(m.cfg)
	m.mu.Unlock()

	m.restartLocked(cfg)
}

// Stop stops the monitoring loop
func (m *Monitor) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stopLoopLocked()
	m.wg.Wait()
}

// UpdateConfig replaces the monitor configuration and applies it immediately.
func (m *Monitor) UpdateConfig(cfg Config) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.restartLocked(cfg)
}

func (m *Monitor) restartLocked(cfg Config) {
	m.stopLoopLocked()
	m.wg.Wait()

	cfg = cloneConfig(cfg)

	m.mu.Lock()
	m.cfg = cfg
	baseCtx := m.baseCtx
	m.mu.Unlock()

	if baseCtx == nil {
		return
	}
	if !cfg.Enabled {
		m.logger.Info().Msg("Storage alerting disabled")
		return
	}
	if cfg.CheckInterval <= 0 {
		m.logger.Warn().Dur("interval", cfg.CheckInterval).Msg("Storage alerting disabled due to non-positive interval")
		return
	}

	loopCtx, cancel := context.WithCancel(baseCtx)

	m.mu.Lock()
	m.cancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go func(cfg Config) {
		defer m.wg.Done()
		onAlertMonitorLoopStart(loopCtx)

		// Initial check
		m.check(loopCtx)

		ticker := time.NewTicker(cfg.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				m.check(loopCtx)
			}
		}
	}(cfg)

	m.logger.Info().
		Dur("interval", cfg.CheckInterval).
		Float64("threshold_pct", cfg.ThresholdPct).
		Msg("Storage monitoring started")
}

func (m *Monitor) stopLoopLocked() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Check performs a manual check and returns current stats
func (m *Monitor) Check() (*StorageStats, error) {
	stats, err := m.getStats()
	if err != nil {
		return nil, err
	}
	stats.Level = m.determineLevel(stats, m.currentConfig())
	return stats, nil
}

// LastStats returns the last checked stats
func (m *Monitor) LastStats() *StorageStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneStorageStats(m.lastStats)
}

func (m *Monitor) currentConfig() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneConfig(m.cfg)
}

func (m *Monitor) check(ctx context.Context) {
	stats, err := m.getStats()
	if err != nil {
		m.logger.Error().Err(err).Msg("Failed to get storage stats")
		return
	}
	cfg := m.currentConfig()
	stats.Level = m.determineLevel(stats, cfg)

	m.mu.Lock()
	m.lastStats = cloneStorageStats(stats)
	m.mu.Unlock()

	level := stats.Level

	if level == AlertLevelNone {
		m.mu.Lock()
		m.lastAlert = time.Time{}
		m.lastLevel = level
		m.lastAlertLevel = AlertLevelNone
		m.mu.Unlock()
		return
	}

	// Check cooldown
	m.mu.Lock()
	shouldAlert := m.shouldSendAlert(level, cfg)
	m.mu.Unlock()

	if shouldAlert {
		if err := m.sendAlert(ctx, stats, cfg); err != nil {
			m.mu.Lock()
			m.lastLevel = level
			m.mu.Unlock()
			m.logger.Error().Err(err).Msg("Failed to send alert")
			return
		}

		m.mu.Lock()
		m.lastAlert = time.Now()
		m.lastLevel = level
		m.lastAlertLevel = level
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.lastLevel = level
	m.mu.Unlock()
}

func (m *Monitor) determineLevel(stats *StorageStats, cfg Config) AlertLevel {
	// Check percentage thresholds
	if stats.UsedPct >= cfg.CriticalPct {
		return AlertLevelCritical
	}
	if stats.UsedPct >= cfg.ThresholdPct {
		return AlertLevelWarning
	}

	// Check absolute free space
	if stats.FreeBytes < cfg.MinFreeBytes {
		if stats.UsedPct >= cfg.CriticalPct-5 {
			return AlertLevelCritical
		}
		return AlertLevelWarning
	}

	return AlertLevelNone
}

func (m *Monitor) shouldSendAlert(level AlertLevel, cfg Config) bool {
	if m.lastLevel == AlertLevelNone {
		return true
	}

	// Always alert on critical level change
	if level == AlertLevelCritical && (m.lastLevel != AlertLevelCritical || m.lastAlertLevel != AlertLevelCritical) {
		return true
	}

	// Check cooldown for repeated alerts
	if time.Since(m.lastAlert) < cfg.CooldownPeriod {
		return false
	}

	return true
}

func (m *Monitor) sendAlert(ctx context.Context, stats *StorageStats, cfg Config) error {
	hostname, _ := os.Hostname()

	var message string
	if stats.Level == AlertLevelCritical {
		message = fmt.Sprintf("存储空间严重不足！使用率 %.1f%%，剩余 %s",
			stats.UsedPct, formatBytes(stats.FreeBytes))
	} else {
		message = fmt.Sprintf("存储空间告警：使用率 %.1f%%，剩余 %s",
			stats.UsedPct, formatBytes(stats.FreeBytes))
	}

	m.logger.Warn().
		Str("level", string(stats.Level)).
		Float64("used_pct", stats.UsedPct).
		Str("free", formatBytes(stats.FreeBytes)).
		Msg("Storage alert triggered")

	// Send webhook if configured
	if cfg.WebhookURL != "" {
		payload := AlertPayload{
			Type:      "storage_alert",
			Level:     stats.Level,
			Message:   message,
			Stats:     *stats,
			Timestamp: time.Now(),
			Hostname:  hostname,
		}

		if err := m.sendWebhook(ctx, payload, cfg); err != nil {
			return err
		}
	}

	return nil
}

func (m *Monitor) sendWebhook(ctx context.Context, payload AlertPayload, cfg Config) error {
	method := strings.ToUpper(strings.TrimSpace(cfg.WebhookMethod))
	if method == "" {
		method = http.MethodPost
	}

	requestURL := cfg.WebhookURL
	var body io.Reader
	if method == http.MethodGet {
		parsedURL, err := url.Parse(cfg.WebhookURL)
		if err != nil {
			return sanitizedWebhookRequestError("parse URL", cfg.WebhookURL, err)
		}
		query := parsedURL.Query()
		query.Set("type", payload.Type)
		query.Set("level", string(payload.Level))
		query.Set("message", payload.Message)
		query.Set("hostname", payload.Hostname)
		query.Set("timestamp", payload.Timestamp.UTC().Format(time.RFC3339Nano))
		query.Set("path", payload.Stats.Path)
		query.Set("total_bytes", strconv.FormatUint(payload.Stats.TotalBytes, 10))
		query.Set("free_bytes", strconv.FormatUint(payload.Stats.FreeBytes, 10))
		query.Set("used_bytes", strconv.FormatUint(payload.Stats.UsedBytes, 10))
		query.Set("used_pct", strconv.FormatFloat(payload.Stats.UsedPct, 'f', -1, 64))
		query.Set("checked_at", payload.Stats.CheckedAt.UTC().Format(time.RFC3339Nano))
		parsedURL.RawQuery = query.Encode()
		requestURL = parsedURL.String()
	} else {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return sanitizedWebhookRequestError("create request", cfg.WebhookURL, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "MnemoNAS-Alert/1.0")

	// Add custom headers
	for _, h := range cfg.WebhookHeaders {
		key, value, ok := parseWebhookHeader(h)
		if !ok {
			continue
		}
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return sanitizedWebhookRequestError("send request", cfg.WebhookURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook request to %s returned status %d", redactWebhookURLForLog(cfg.WebhookURL), resp.StatusCode)
	}

	m.logger.Info().
		Str("url", redactWebhookURLForLog(cfg.WebhookURL)).
		Int("status", resp.StatusCode).
		Msg("Webhook sent successfully")

	return nil
}

func sanitizedWebhookRequestError(action, rawURL string, err error) error {
	target := redactWebhookURLForLog(rawURL)
	if err == nil {
		return fmt.Errorf("webhook %s failed for %s", action, target)
	}
	if detail := sanitizedWebhookErrorDetail(err); detail != "" {
		return fmt.Errorf("webhook %s failed for %s: %s", action, target, detail)
	}
	return fmt.Errorf("webhook %s failed for %s", action, target)
}

func sanitizedWebhookErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context deadline exceeded"
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		op := strings.TrimSpace(urlErr.Op)
		if strings.EqualFold(op, "parse") {
			return "invalid URL"
		}
		detail := sanitizedWebhookErrorDetail(urlErr.Err)
		if op != "" && detail != "" {
			return op + ": " + detail
		}
		if op != "" {
			return op
		}
		return detail
	}

	type timeout interface {
		Timeout() bool
	}
	if timeoutErr, ok := err.(timeout); ok && timeoutErr.Timeout() {
		return "timeout"
	}

	return "transport error"
}

func redactWebhookURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "configured"
	}
	return parsed.Scheme + "://" + parsed.Host
}

func parseWebhookHeader(header string) (string, string, bool) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return "", "", false
	}

	key, value, ok := strings.Cut(trimmed, ":")
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if !ok || key == "" || value == "" {
		return "", "", false
	}
	if !isValidHTTPHeaderToken(key) || hasInvalidHTTPHeaderValueControl(value) {
		return "", "", false
	}
	return key, value, true
}

func isValidHTTPHeaderToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isHTTPTokenChar(value[i]) {
			return false
		}
	}
	return true
}

func isHTTPTokenChar(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func hasInvalidHTTPHeaderValueControl(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 0x7f || (value[i] < 0x20 && value[i] != '\t') {
			return true
		}
	}
	return false
}

func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.1f TB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
