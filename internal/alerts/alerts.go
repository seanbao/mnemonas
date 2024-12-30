// Package alerts provides storage space monitoring and alerting
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

// Config holds alerting configuration
type Config struct {
	Enabled         bool          `toml:"enabled"`
	CheckInterval   time.Duration `toml:"check_interval"`   // How often to check (default 1h)
	ThresholdPct    float64       `toml:"threshold_pct"`    // Alert when usage exceeds this % (default 90)
	CriticalPct     float64       `toml:"critical_pct"`     // Critical alert threshold (default 95)
	MinFreeBytes    uint64        `toml:"min_free_bytes"`   // Alert when free space < this (default 10GB)
	CooldownPeriod  time.Duration `toml:"cooldown_period"`  // Min time between alerts (default 4h)
	WebhookURL      string        `toml:"webhook_url"`      // Webhook URL for notifications
	WebhookMethod   string        `toml:"webhook_method"`   // POST or GET (default POST)
	WebhookHeaders  []string      `toml:"webhook_headers"`  // Additional headers (key:value format)
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
	Path       string    `json:"path"`
	TotalBytes uint64    `json:"total_bytes"`
	FreeBytes  uint64    `json:"free_bytes"`
	UsedBytes  uint64    `json:"used_bytes"`
	UsedPct    float64   `json:"used_pct"`
	Level      AlertLevel `json:"level"`
	CheckedAt  time.Time `json:"checked_at"`
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
	cfg       Config
	logger    zerolog.Logger
	dataDir   string
	
	mu            sync.Mutex
	lastAlert     time.Time
	lastLevel     AlertLevel
	lastStats     *StorageStats
	
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewMonitor creates a new storage monitor
func NewMonitor(cfg Config, dataDir string, logger zerolog.Logger) *Monitor {
	return &Monitor{
		cfg:     cfg,
		logger:  logger,
		dataDir: dataDir,
	}
}

// Start begins the monitoring loop
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		m.logger.Info().Msg("Storage alerting disabled")
		return
	}

	ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(1)
	
	go func() {
		defer m.wg.Done()
		
		// Initial check
		m.check(ctx)
		
		ticker := time.NewTicker(m.cfg.CheckInterval)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.check(ctx)
			}
		}
	}()
	
	m.logger.Info().
		Dur("interval", m.cfg.CheckInterval).
		Float64("threshold_pct", m.cfg.ThresholdPct).
		Msg("Storage monitoring started")
}

// Stop stops the monitoring loop
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// Check performs a manual check and returns current stats
func (m *Monitor) Check() (*StorageStats, error) {
	return m.getStats()
}

// LastStats returns the last checked stats
func (m *Monitor) LastStats() *StorageStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastStats
}

func (m *Monitor) check(ctx context.Context) {
	stats, err := m.getStats()
	if err != nil {
		m.logger.Error().Err(err).Msg("Failed to get storage stats")
		return
	}
	
	m.mu.Lock()
	m.lastStats = stats
	m.mu.Unlock()
	
	level := m.determineLevel(stats)
	stats.Level = level
	
	if level == AlertLevelNone {
		m.mu.Lock()
		m.lastLevel = level
		m.mu.Unlock()
		return
	}
	
	// Check cooldown
	m.mu.Lock()
	shouldAlert := m.shouldSendAlert(level)
	if shouldAlert {
		m.lastAlert = time.Now()
		m.lastLevel = level
	}
	m.mu.Unlock()
	
	if shouldAlert {
		m.sendAlert(ctx, stats)
	}
}

func (m *Monitor) getStats() (*StorageStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.dataDir, &stat); err != nil {
		return nil, fmt.Errorf("statfs failed: %w", err)
	}
	
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes
	usedPct := float64(usedBytes) / float64(totalBytes) * 100
	
	return &StorageStats{
		Path:       m.dataDir,
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
		UsedPct:    usedPct,
		CheckedAt:  time.Now(),
	}, nil
}

func (m *Monitor) determineLevel(stats *StorageStats) AlertLevel {
	// Check percentage thresholds
	if stats.UsedPct >= m.cfg.CriticalPct {
		return AlertLevelCritical
	}
	if stats.UsedPct >= m.cfg.ThresholdPct {
		return AlertLevelWarning
	}
	
	// Check absolute free space
	if stats.FreeBytes < m.cfg.MinFreeBytes {
		if stats.UsedPct >= m.cfg.CriticalPct-5 {
			return AlertLevelCritical
		}
		return AlertLevelWarning
	}
	
	return AlertLevelNone
}

func (m *Monitor) shouldSendAlert(level AlertLevel) bool {
	// Always alert on critical level change
	if level == AlertLevelCritical && m.lastLevel != AlertLevelCritical {
		return true
	}
	
	// Check cooldown for repeated alerts
	if time.Since(m.lastAlert) < m.cfg.CooldownPeriod {
		return false
	}
	
	return true
}

func (m *Monitor) sendAlert(ctx context.Context, stats *StorageStats) {
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
	if m.cfg.WebhookURL != "" {
		payload := AlertPayload{
			Type:      "storage_alert",
			Level:     stats.Level,
			Message:   message,
			Stats:     *stats,
			Timestamp: time.Now(),
			Hostname:  hostname,
		}
		
		if err := m.sendWebhook(ctx, payload); err != nil {
			m.logger.Error().Err(err).Msg("Failed to send webhook")
		}
	}
}

func (m *Monitor) sendWebhook(ctx context.Context, payload AlertPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	
	method := m.cfg.WebhookMethod
	if method == "" {
		method = "POST"
	}
	
	req, err := http.NewRequestWithContext(ctx, method, m.cfg.WebhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MnemoNAS-Alert/1.0")
	
	// Add custom headers
	for _, h := range m.cfg.WebhookHeaders {
		// Parse "key:value" format
		for i := 0; i < len(h); i++ {
			if h[i] == ':' {
				req.Header.Set(h[:i], h[i+1:])
				break
			}
		}
	}
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	
	m.logger.Info().
		Str("url", m.cfg.WebhookURL).
		Int("status", resp.StatusCode).
		Msg("Webhook sent successfully")
	
	return nil
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
