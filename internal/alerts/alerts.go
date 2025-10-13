// Package alerts provides storage space monitoring and alerting
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rs/zerolog"
)

// Config holds alerting configuration
type Config struct {
	Enabled            bool          `toml:"enabled"`
	CheckInterval      time.Duration `toml:"check_interval"`       // How often to check (default 1h)
	ThresholdPct       float64       `toml:"threshold_pct"`        // Alert when usage exceeds this % (default 90)
	CriticalPct        float64       `toml:"critical_pct"`         // Critical alert threshold (default 95)
	MinFreeBytes       uint64        `toml:"min_free_bytes"`       // Alert when free space < this (default 10GB)
	CooldownPeriod     time.Duration `toml:"cooldown_period"`      // Min time between alerts (default 4h)
	WebhookURL         string        `toml:"webhook_url"`          // Webhook URL for notifications
	WebhookMethod      string        `toml:"webhook_method"`       // POST or GET (default POST)
	WebhookHeaders     []string      `toml:"webhook_headers"`      // Additional headers (key:value format)
	TelegramEnabled    bool          `toml:"telegram_enabled"`     // Send Telegram notifications
	TelegramBotToken   string        `toml:"telegram_bot_token"`   // Telegram bot token
	TelegramChatID     string        `toml:"telegram_chat_id"`     // Telegram chat ID or @channel
	WeComEnabled       bool          `toml:"wecom_enabled"`        // Send WeCom group robot notifications
	WeComWebhookURL    string        `toml:"wecom_webhook_url"`    // WeCom group robot webhook URL
	DingTalkEnabled    bool          `toml:"dingtalk_enabled"`     // Send DingTalk group robot notifications
	DingTalkWebhookURL string        `toml:"dingtalk_webhook_url"` // DingTalk group robot webhook URL
	EmailEnabled       bool          `toml:"email_enabled"`        // Send email notifications
	SMTPHost           string        `toml:"smtp_host"`            // SMTP host without port
	SMTPPort           int           `toml:"smtp_port"`            // SMTP port
	SMTPUsername       string        `toml:"smtp_username"`        // SMTP username
	SMTPPassword       string        `toml:"smtp_password"`        // SMTP password
	SMTPFrom           string        `toml:"smtp_from"`            // Sender address
	SMTPTo             []string      `toml:"smtp_to"`              // Recipient addresses
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
		SMTPPort:       587,
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
	PathScope  string     `json:"path_scope,omitempty"`
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
	if cfg.SMTPTo != nil {
		clone.SMTPTo = append([]string(nil), cfg.SMTPTo...)
	}
	return clone
}

var (
	errStorageStatsInvalidBlockSize = errors.New("filesystem reported invalid block size")
	errStorageStatsCapacityOverflow = errors.New("filesystem capacity exceeds uint64")
)

func storageStatsFromStatfsBlocks(path string, blocks, availableBlocks uint64, blockSize int64, checkedAt time.Time) (StorageStats, error) {
	if blockSize <= 0 {
		return StorageStats{}, errStorageStatsInvalidBlockSize
	}

	totalBytes, err := statfsBytes(blocks, uint64(blockSize))
	if err != nil {
		return StorageStats{}, err
	}
	freeBytes, err := statfsBytes(availableBlocks, uint64(blockSize))
	if err != nil {
		return StorageStats{}, err
	}

	return storageStatsFromUsage(path, totalBytes, freeBytes, checkedAt), nil
}

func statfsBytes(blocks, blockSize uint64) (uint64, error) {
	hi, lo := bits.Mul64(blocks, blockSize)
	if hi != 0 {
		return 0, errStorageStatsCapacityOverflow
	}
	return lo, nil
}

func storageStatsFromUsage(path string, totalBytes, freeBytes uint64, checkedAt time.Time) StorageStats {
	if freeBytes > totalBytes {
		freeBytes = totalBytes
	}
	usedBytes := totalBytes - freeBytes
	usedPct := 0.0
	if totalBytes > 0 {
		usedPct = float64(usedBytes) / float64(totalBytes) * 100
	}
	return StorageStats{
		Path:       path,
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
		UsedPct:    usedPct,
		CheckedAt:  checkedAt.UTC(),
	}
}

var sendSMTPMail = smtp.SendMail
var telegramAPIBaseURL = "https://api.telegram.org"

const storageAlertPathOmitted = "<omitted>"
const storageAlertPathScopeConfiguredRoot = "configured_storage_root"

// AlertPayload is the webhook payload
type AlertPayload struct {
	Type      string       `json:"type"`
	Level     AlertLevel   `json:"level"`
	Message   string       `json:"message"`
	Stats     StorageStats `json:"stats"`
	Timestamp time.Time    `json:"timestamp"`
	Hostname  string       `json:"hostname"`
}

// EventPayload is a generic webhook event payload for non-storage alerts.
type EventPayload struct {
	Type      string         `json:"type"`
	Level     AlertLevel     `json:"level"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Hostname  string         `json:"hostname"`
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
	now            func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var onAlertMonitorLoopStart = func(context.Context) {}
var onAlertMonitorCheckComplete = func(context.Context, *StorageStats) {}

// NewMonitor creates a new storage monitor
func NewMonitor(cfg Config, dataDir string, logger zerolog.Logger) *Monitor {
	return &Monitor{
		cfg:            cloneConfig(cfg),
		logger:         logger,
		dataDir:        dataDir,
		lastLevel:      AlertLevelNone,
		lastAlertLevel: AlertLevelNone,
		now:            time.Now,
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

// SendEvent sends a generic alert event through configured notification channels.
func (m *Monitor) SendEvent(ctx context.Context, event EventPayload) error {
	cfg := m.currentConfig()
	if !cfg.Enabled || !hasNotificationChannel(cfg) {
		return nil
	}
	if strings.TrimSpace(event.Type) == "" {
		event.Type = "event"
	}
	if strings.TrimSpace(event.Message) == "" {
		event.Message = event.Type
	}
	if event.Level == "" {
		event.Level = AlertLevelWarning
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = m.currentTime().UTC()
	}
	if strings.TrimSpace(event.Hostname) == "" {
		hostname, _ := os.Hostname()
		event.Hostname = hostname
	}

	m.logger.Warn().
		Str("type", event.Type).
		Str("level", string(event.Level)).
		Msg("Alert event triggered")

	var sendErr error
	if strings.TrimSpace(cfg.WebhookURL) != "" {
		sendErr = errors.Join(sendErr, m.sendEventWebhook(ctx, event, cfg))
	}
	if cfg.TelegramEnabled {
		sendErr = errors.Join(sendErr, m.sendEventTelegram(ctx, event, cfg))
	}
	if cfg.WeComEnabled {
		sendErr = errors.Join(sendErr, m.sendEventWeCom(ctx, event, cfg))
	}
	if cfg.DingTalkEnabled {
		sendErr = errors.Join(sendErr, m.sendEventDingTalk(ctx, event, cfg))
	}
	if cfg.EmailEnabled {
		sendErr = errors.Join(sendErr, m.sendEventEmail(ctx, event, cfg))
	}
	return sendErr
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
	onAlertMonitorCheckComplete(ctx, cloneStorageStats(stats))

	level := stats.Level
	checkedAt := m.statsCheckedAt(stats)

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
	shouldAlert := m.shouldSendAlert(level, cfg, checkedAt)
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
		m.lastAlert = checkedAt
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

func (m *Monitor) shouldSendAlert(level AlertLevel, cfg Config, now time.Time) bool {
	if m.lastLevel == AlertLevelNone {
		return true
	}

	// Always alert on critical level change
	if level == AlertLevelCritical && (m.lastLevel != AlertLevelCritical || m.lastAlertLevel != AlertLevelCritical) {
		return true
	}

	// Check cooldown for repeated alerts
	if cfg.CooldownPeriod > 0 && !m.lastAlert.IsZero() && now.Sub(m.lastAlert) < cfg.CooldownPeriod {
		return false
	}

	return true
}

func (m *Monitor) currentTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Monitor) statsCheckedAt(stats *StorageStats) time.Time {
	if stats != nil && !stats.CheckedAt.IsZero() {
		return stats.CheckedAt.UTC()
	}
	return m.currentTime().UTC()
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

	var sendErr error
	if cfg.WebhookURL != "" {
		payload := AlertPayload{
			Type:      "storage_alert",
			Level:     stats.Level,
			Message:   message,
			Stats:     *stats,
			Timestamp: m.statsCheckedAt(stats),
			Hostname:  hostname,
		}

		if err := m.sendWebhook(ctx, payload, cfg); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}
	if cfg.EmailEnabled {
		if err := m.sendStorageEmail(ctx, stats, cfg, message, hostname); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}
	if cfg.TelegramEnabled {
		if err := m.sendStorageTelegram(ctx, stats, cfg, message, hostname); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}
	if cfg.WeComEnabled {
		if err := m.sendStorageWeCom(ctx, stats, cfg, message, hostname); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}
	if cfg.DingTalkEnabled {
		if err := m.sendStorageDingTalk(ctx, stats, cfg, message, hostname); err != nil {
			sendErr = errors.Join(sendErr, err)
		}
	}

	return sendErr
}

func (m *Monitor) sendWebhook(ctx context.Context, payload AlertPayload, cfg Config) error {
	publicPayload := storageAlertPayloadForNotification(payload)
	return m.sendWebhookRequest(ctx, publicPayload, cfg, func(query url.Values) error {
		setCommonWebhookQuery(query, publicPayload.Type, publicPayload.Level, publicPayload.Message, publicPayload.Hostname, publicPayload.Timestamp)
		query.Set("path", publicPayload.Stats.Path)
		if publicPayload.Stats.PathScope != "" {
			query.Set("path_scope", publicPayload.Stats.PathScope)
		}
		query.Set("total_bytes", strconv.FormatUint(publicPayload.Stats.TotalBytes, 10))
		query.Set("free_bytes", strconv.FormatUint(publicPayload.Stats.FreeBytes, 10))
		query.Set("used_bytes", strconv.FormatUint(publicPayload.Stats.UsedBytes, 10))
		query.Set("used_pct", strconv.FormatFloat(publicPayload.Stats.UsedPct, 'f', -1, 64))
		query.Set("checked_at", publicPayload.Stats.CheckedAt.UTC().Format(time.RFC3339Nano))
		return nil
	})
}

func storageAlertPayloadForNotification(payload AlertPayload) AlertPayload {
	payload.Stats = storageAlertStatsForNotification(payload.Stats)
	return payload
}

func storageAlertStatsForNotification(stats StorageStats) StorageStats {
	if strings.TrimSpace(stats.Path) != "" {
		stats.Path = storageAlertPathOmitted
		stats.PathScope = storageAlertPathScopeConfiguredRoot
	}
	return stats
}

func storageAlertPathScope(stats *StorageStats) string {
	if stats != nil && strings.TrimSpace(stats.Path) != "" {
		return storageAlertPathScopeConfiguredRoot
	}
	return "unknown"
}

func (m *Monitor) sendEventWebhook(ctx context.Context, payload EventPayload, cfg Config) error {
	return m.sendWebhookRequest(ctx, payload, cfg, func(query url.Values) error {
		setCommonWebhookQuery(query, payload.Type, payload.Level, payload.Message, payload.Hostname, payload.Timestamp)
		if len(payload.Details) > 0 {
			details, err := json.Marshal(payload.Details)
			if err != nil {
				return fmt.Errorf("marshal event details: %w", err)
			}
			query.Set("details", string(details))
		}
		return nil
	})
}

func (m *Monitor) sendWebhookRequest(ctx context.Context, payload any, cfg Config, encodeGET func(url.Values) error) error {
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
		if encodeGET != nil {
			if err := encodeGET(query); err != nil {
				return err
			}
		}
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

func hasNotificationChannel(cfg Config) bool {
	return strings.TrimSpace(cfg.WebhookURL) != "" ||
		cfg.EmailEnabled ||
		cfg.TelegramEnabled ||
		(cfg.WeComEnabled && strings.TrimSpace(cfg.WeComWebhookURL) != "") ||
		(cfg.DingTalkEnabled && strings.TrimSpace(cfg.DingTalkWebhookURL) != "")
}

type telegramMessagePayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

func (m *Monitor) sendStorageTelegram(ctx context.Context, stats *StorageStats, cfg Config, message, hostname string) error {
	if stats == nil {
		return nil
	}
	lines := []string{
		"[MnemoNAS] storage " + string(stats.Level),
		message,
		"",
		"Host: " + hostname,
		"Path scope: " + storageAlertPathScope(stats),
		"Used: " + formatBytes(stats.UsedBytes) + " (" + strconv.FormatFloat(stats.UsedPct, 'f', 1, 64) + "%)",
		"Free: " + formatBytes(stats.FreeBytes),
		"Checked at: " + stats.CheckedAt.UTC().Format(time.RFC3339),
	}
	return m.sendTelegram(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendEventTelegram(ctx context.Context, event EventPayload, cfg Config) error {
	lines := []string{
		"[MnemoNAS] " + event.Type + " " + string(event.Level),
		event.Message,
		"",
		"Host: " + event.Hostname,
		"Timestamp: " + event.Timestamp.UTC().Format(time.RFC3339),
	}
	if len(event.Details) > 0 {
		details, err := json.MarshalIndent(event.Details, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal event details for telegram: %w", err)
		}
		lines = append(lines, "", "Details:", string(details))
	}
	return m.sendTelegram(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendTelegram(ctx context.Context, cfg Config, text string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !cfg.TelegramEnabled {
		return nil
	}
	token := strings.TrimSpace(cfg.TelegramBotToken)
	chatID := strings.TrimSpace(cfg.TelegramChatID)
	if token == "" || chatID == "" {
		return errors.New("telegram alert missing bot token or chat id")
	}
	if strings.ContainsAny(token, "/?#") || strings.IndexFunc(token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return errors.New("telegram alert bot token contains invalid characters")
	}

	apiBase := strings.TrimRight(strings.TrimSpace(telegramAPIBaseURL), "/")
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	endpoint := apiBase + "/bot" + token + "/sendMessage"

	payload := telegramMessagePayload{
		ChatID:                chatID,
		Text:                  truncateTelegramText(text),
		DisableWebPagePreview: true,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MnemoNAS-Alert/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram request returned status %d", resp.StatusCode)
	}

	m.logger.Info().
		Str("channel", "telegram").
		Msg("Telegram alert sent successfully")
	return nil
}

func truncateTelegramText(text string) string {
	const maxTelegramMessageRunes = 4096
	runes := []rune(text)
	if len(runes) <= maxTelegramMessageRunes {
		return text
	}
	return string(runes[:maxTelegramMessageRunes-1]) + "…"
}

type weComTextPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

type weComAPIResponse struct {
	ErrCode int `json:"errcode"`
}

type dingTalkTextPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

type dingTalkAPIResponse struct {
	ErrCode int `json:"errcode"`
}

func (m *Monitor) sendStorageWeCom(ctx context.Context, stats *StorageStats, cfg Config, message, hostname string) error {
	if stats == nil {
		return nil
	}
	lines := []string{
		"[MnemoNAS] storage " + string(stats.Level),
		message,
		"",
		"Host: " + hostname,
		"Path scope: " + storageAlertPathScope(stats),
		"Used: " + formatBytes(stats.UsedBytes) + " (" + strconv.FormatFloat(stats.UsedPct, 'f', 1, 64) + "%)",
		"Free: " + formatBytes(stats.FreeBytes),
		"Checked at: " + stats.CheckedAt.UTC().Format(time.RFC3339),
	}
	return m.sendWeCom(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendEventWeCom(ctx context.Context, event EventPayload, cfg Config) error {
	lines := []string{
		"[MnemoNAS] " + event.Type + " " + string(event.Level),
		event.Message,
		"",
		"Host: " + event.Hostname,
		"Timestamp: " + event.Timestamp.UTC().Format(time.RFC3339),
	}
	if len(event.Details) > 0 {
		details, err := json.MarshalIndent(event.Details, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal event details for wecom: %w", err)
		}
		lines = append(lines, "", "Details:", string(details))
	}
	return m.sendWeCom(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendWeCom(ctx context.Context, cfg Config, text string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !cfg.WeComEnabled {
		return nil
	}
	webhookURL := strings.TrimSpace(cfg.WeComWebhookURL)
	if webhookURL == "" {
		return errors.New("wecom alert missing webhook URL")
	}

	payload := weComTextPayload{MsgType: "text"}
	payload.Text.Content = truncateWeComText(text)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal wecom payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return sanitizedWeComWebhookRequestError("create request", webhookURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MnemoNAS-Alert/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return sanitizedWeComWebhookRequestError("send request", webhookURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("wecom webhook request to %s returned status %d", redactWebhookURLForLog(webhookURL), resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read wecom webhook response from %s: %w", redactWebhookURLForLog(webhookURL), err)
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		var result weComAPIResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("decode wecom webhook response from %s: invalid response", redactWebhookURLForLog(webhookURL))
		}
		if result.ErrCode != 0 {
			return fmt.Errorf("wecom webhook request to %s returned errcode %d", redactWebhookURLForLog(webhookURL), result.ErrCode)
		}
	}

	m.logger.Info().
		Str("url", redactWebhookURLForLog(webhookURL)).
		Msg("WeCom alert sent successfully")
	return nil
}

func truncateWeComText(text string) string {
	const maxWeComMessageRunes = 2048
	runes := []rune(text)
	if len(runes) <= maxWeComMessageRunes {
		return text
	}
	return string(runes[:maxWeComMessageRunes-1]) + "…"
}

func sanitizedWeComWebhookRequestError(action, rawURL string, err error) error {
	target := redactWebhookURLForLog(rawURL)
	if err == nil {
		return fmt.Errorf("wecom webhook %s failed for %s", action, target)
	}
	if detail := sanitizedWebhookErrorDetail(err); detail != "" {
		return fmt.Errorf("wecom webhook %s failed for %s: %s", action, target, detail)
	}
	return fmt.Errorf("wecom webhook %s failed for %s", action, target)
}

func (m *Monitor) sendStorageDingTalk(ctx context.Context, stats *StorageStats, cfg Config, message, hostname string) error {
	if stats == nil {
		return nil
	}
	lines := []string{
		"[MnemoNAS] storage " + string(stats.Level),
		message,
		"",
		"Host: " + hostname,
		"Path scope: " + storageAlertPathScope(stats),
		"Used: " + formatBytes(stats.UsedBytes) + " (" + strconv.FormatFloat(stats.UsedPct, 'f', 1, 64) + "%)",
		"Free: " + formatBytes(stats.FreeBytes),
		"Checked at: " + stats.CheckedAt.UTC().Format(time.RFC3339),
	}
	return m.sendDingTalk(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendEventDingTalk(ctx context.Context, event EventPayload, cfg Config) error {
	lines := []string{
		"[MnemoNAS] " + event.Type + " " + string(event.Level),
		event.Message,
		"",
		"Host: " + event.Hostname,
		"Timestamp: " + event.Timestamp.UTC().Format(time.RFC3339),
	}
	if len(event.Details) > 0 {
		details, err := json.MarshalIndent(event.Details, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal event details for dingtalk: %w", err)
		}
		lines = append(lines, "", "Details:", string(details))
	}
	return m.sendDingTalk(ctx, cfg, strings.Join(lines, "\n"))
}

func (m *Monitor) sendDingTalk(ctx context.Context, cfg Config, text string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !cfg.DingTalkEnabled {
		return nil
	}
	webhookURL := strings.TrimSpace(cfg.DingTalkWebhookURL)
	if webhookURL == "" {
		return errors.New("dingtalk alert missing webhook URL")
	}

	payload := dingTalkTextPayload{MsgType: "text"}
	payload.Text.Content = truncateDingTalkText(text)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal dingtalk payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return sanitizedDingTalkWebhookRequestError("create request", webhookURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MnemoNAS-Alert/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return sanitizedDingTalkWebhookRequestError("send request", webhookURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("dingtalk webhook request to %s returned status %d", redactWebhookURLForLog(webhookURL), resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read dingtalk webhook response from %s: %w", redactWebhookURLForLog(webhookURL), err)
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		var result dingTalkAPIResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("decode dingtalk webhook response from %s: invalid response", redactWebhookURLForLog(webhookURL))
		}
		if result.ErrCode != 0 {
			return fmt.Errorf("dingtalk webhook request to %s returned errcode %d", redactWebhookURLForLog(webhookURL), result.ErrCode)
		}
	}

	m.logger.Info().
		Str("url", redactWebhookURLForLog(webhookURL)).
		Msg("DingTalk alert sent successfully")
	return nil
}

func truncateDingTalkText(text string) string {
	const maxDingTalkMessageRunes = 4096
	runes := []rune(text)
	if len(runes) <= maxDingTalkMessageRunes {
		return text
	}
	return string(runes[:maxDingTalkMessageRunes-1]) + "…"
}

func sanitizedDingTalkWebhookRequestError(action, rawURL string, err error) error {
	target := redactWebhookURLForLog(rawURL)
	if err == nil {
		return fmt.Errorf("dingtalk webhook %s failed for %s", action, target)
	}
	if detail := sanitizedWebhookErrorDetail(err); detail != "" {
		return fmt.Errorf("dingtalk webhook %s failed for %s: %s", action, target, detail)
	}
	return fmt.Errorf("dingtalk webhook %s failed for %s", action, target)
}

func (m *Monitor) sendStorageEmail(ctx context.Context, stats *StorageStats, cfg Config, message, hostname string) error {
	if stats == nil {
		return nil
	}
	body := strings.Join([]string{
		message,
		"",
		"Type: storage_alert",
		"Level: " + string(stats.Level),
		"Host: " + hostname,
		"Path scope: " + storageAlertPathScope(stats),
		"Used: " + formatBytes(stats.UsedBytes) + " (" + strconv.FormatFloat(stats.UsedPct, 'f', 1, 64) + "%)",
		"Free: " + formatBytes(stats.FreeBytes),
		"Checked at: " + stats.CheckedAt.UTC().Format(time.RFC3339),
	}, "\n")
	return m.sendEmail(ctx, cfg, "[MnemoNAS] storage "+string(stats.Level), body)
}

func (m *Monitor) sendEventEmail(ctx context.Context, event EventPayload, cfg Config) error {
	lines := []string{
		event.Message,
		"",
		"Type: " + event.Type,
		"Level: " + string(event.Level),
		"Host: " + event.Hostname,
		"Timestamp: " + event.Timestamp.UTC().Format(time.RFC3339),
	}
	if len(event.Details) > 0 {
		details, err := json.MarshalIndent(event.Details, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal event details for email: %w", err)
		}
		lines = append(lines, "", "Details:", string(details))
	}
	return m.sendEmail(ctx, cfg, "[MnemoNAS] "+event.Type+" "+string(event.Level), strings.Join(lines, "\n"))
}

func (m *Monitor) sendEmail(ctx context.Context, cfg Config, subject, body string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !cfg.EmailEnabled {
		return nil
	}
	recipients := cleanedSMTPRecipients(cfg.SMTPTo)
	if len(recipients) == 0 {
		return errors.New("email alert has no recipients configured")
	}

	addr := net.JoinHostPort(strings.TrimSpace(cfg.SMTPHost), strconv.Itoa(cfg.SMTPPort))
	from := strings.TrimSpace(cfg.SMTPFrom)
	var auth smtp.Auth
	if strings.TrimSpace(cfg.SMTPUsername) != "" {
		auth = smtp.PlainAuth("", strings.TrimSpace(cfg.SMTPUsername), cfg.SMTPPassword, strings.TrimSpace(cfg.SMTPHost))
	}
	message := buildEmailMessage(from, recipients, subject, body)
	if err := sendSMTPMail(addr, auth, from, recipients, message); err != nil {
		return errors.New("send email alert failed")
	}
	m.logger.Info().
		Str("channel", "email").
		Int("recipients", len(recipients)).
		Msg("Email alert sent successfully")
	return nil
}

func cleanedSMTPRecipients(values []string) []string {
	recipients := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			recipients = append(recipients, trimmed)
		}
	}
	return recipients
}

func buildEmailMessage(from string, to []string, subject, body string) []byte {
	encodedSubject := mime.QEncoding.Encode("UTF-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(subject))
	headers := []string{
		"From: " + strings.TrimSpace(from),
		"To: " + strings.Join(to, ", "),
		"Subject: " + encodedSubject,
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n")
}

func setCommonWebhookQuery(query url.Values, eventType string, level AlertLevel, message string, hostname string, timestamp time.Time) {
	query.Set("type", eventType)
	query.Set("level", string(level))
	query.Set("message", message)
	query.Set("hostname", hostname)
	query.Set("timestamp", timestamp.UTC().Format(time.RFC3339Nano))
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
