// Package diskhealth checks configured disks with smartctl and reports health.
package diskhealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/alerts"
)

const (
	StatusDisabled    = "disabled"
	StatusOK          = "ok"
	StatusWarning     = "warning"
	StatusCritical    = "critical"
	StatusUnavailable = "unavailable"
)

const (
	defaultCommand              = "smartctl"
	defaultCheckInterval        = time.Hour
	defaultProbeTimeout         = 15 * time.Second
	defaultCooldownPeriod       = 4 * time.Hour
	defaultTemperatureWarningC  = 50
	defaultTemperatureCriticalC = 60
	defaultMediaWearWarningPct  = 80
	defaultMediaWearCriticalPct = 100
)

// Config controls disk health probing and alerting.
type Config struct {
	Enabled              bool           `toml:"enabled"`
	CheckInterval        time.Duration  `toml:"check_interval"`
	ProbeTimeout         time.Duration  `toml:"probe_timeout"`
	CooldownPeriod       time.Duration  `toml:"cooldown_period"`
	Command              string         `toml:"command"`
	TemperatureWarningC  int            `toml:"temperature_warning_c"`
	TemperatureCriticalC int            `toml:"temperature_critical_c"`
	MediaWearWarningPct  int            `toml:"media_wear_warning_percent"`
	MediaWearCriticalPct int            `toml:"media_wear_critical_percent"`
	Devices              []DeviceConfig `toml:"devices"`
}

// DeviceConfig describes one disk device to check.
type DeviceConfig struct {
	Name                 string `toml:"name" json:"name,omitempty"`
	Path                 string `toml:"path" json:"path"`
	Type                 string `toml:"type" json:"type,omitempty"`
	Serial               string `toml:"serial" json:"expected_serial,omitempty"`
	TemperatureWarningC  int    `toml:"temperature_warning_c,omitempty" json:"temperature_warning_c,omitempty"`
	TemperatureCriticalC int    `toml:"temperature_critical_c,omitempty" json:"temperature_critical_c,omitempty"`
}

// Report is a point-in-time disk health result.
type Report struct {
	Enabled   bool           `json:"enabled"`
	Status    string         `json:"status"`
	CheckedAt time.Time      `json:"checked_at"`
	Devices   []DeviceStatus `json:"devices"`
	Warnings  []string       `json:"warnings,omitempty"`
	Message   string         `json:"message,omitempty"`
}

// DeviceStatus is the health result for one configured device.
type DeviceStatus struct {
	Name                 string `json:"name,omitempty"`
	Path                 string `json:"path"`
	Type                 string `json:"type,omitempty"`
	ExpectedSerial       string `json:"expected_serial,omitempty"`
	Serial               string `json:"serial,omitempty"`
	Model                string `json:"model,omitempty"`
	Present              bool   `json:"present"`
	SMARTAvailable       bool   `json:"smart_available"`
	SMARTPassed          *bool  `json:"smart_passed,omitempty"`
	TemperatureC         *int   `json:"temperature_c,omitempty"`
	PowerOnHours         *int64 `json:"power_on_hours,omitempty"`
	WearPercentUsed      *int   `json:"wear_percent_used,omitempty"`
	AvailableSparePct    *int   `json:"available_spare_percent,omitempty"`
	SpareThresholdPct    *int   `json:"available_spare_threshold_percent,omitempty"`
	MediaErrors          *int64 `json:"media_errors,omitempty"`
	NVMeCriticalWarning  *int   `json:"nvme_critical_warning,omitempty"`
	Status               string `json:"status"`
	Message              string `json:"message,omitempty"`
	TemperatureWarningC  int    `json:"temperature_warning_c,omitempty"`
	TemperatureCriticalC int    `json:"temperature_critical_c,omitempty"`
}

// CommandResult captures stdout and stderr from a probe command.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// CommandRunner runs a command without shell interpolation.
type CommandRunner func(ctx context.Context, command string, args ...string) (CommandResult, error)

// Option customizes a Checker.
type Option func(*Checker)

// WithRunner injects a command runner, mainly for tests.
func WithRunner(runner CommandRunner) Option {
	return func(c *Checker) {
		if runner != nil {
			c.runner = runner
		}
	}
}

// Checker performs disk health probes.
type Checker struct {
	logger zerolog.Logger

	mu     sync.RWMutex
	cfg    Config
	runner CommandRunner
	now    func() time.Time
}

// NewChecker creates a disk health checker.
func NewChecker(cfg Config, logger zerolog.Logger, opts ...Option) *Checker {
	checker := &Checker{
		cfg:    NormalizeConfig(cfg),
		logger: logger,
		runner: defaultRunner,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(checker)
	}
	return checker
}

// NormalizeConfig fills runtime defaults and trims user-provided values.
func NormalizeConfig(cfg Config) Config {
	cfg.Command = strings.TrimSpace(cfg.Command)
	if cfg.Command == "" {
		cfg.Command = defaultCommand
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = defaultCheckInterval
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = defaultProbeTimeout
	}
	if cfg.CooldownPeriod == 0 {
		cfg.CooldownPeriod = defaultCooldownPeriod
	}
	if cfg.TemperatureWarningC == 0 {
		cfg.TemperatureWarningC = defaultTemperatureWarningC
	}
	if cfg.TemperatureCriticalC == 0 {
		cfg.TemperatureCriticalC = defaultTemperatureCriticalC
	}
	if cfg.MediaWearWarningPct == 0 {
		cfg.MediaWearWarningPct = defaultMediaWearWarningPct
	}
	if cfg.MediaWearCriticalPct == 0 {
		cfg.MediaWearCriticalPct = defaultMediaWearCriticalPct
	}
	cfg.Devices = cloneDeviceConfigs(cfg.Devices)
	for i := range cfg.Devices {
		cfg.Devices[i].Name = strings.TrimSpace(cfg.Devices[i].Name)
		cfg.Devices[i].Path = strings.TrimSpace(cfg.Devices[i].Path)
		if cfg.Devices[i].Path != "" {
			cfg.Devices[i].Path = filepath.Clean(cfg.Devices[i].Path)
		}
		cfg.Devices[i].Type = strings.TrimSpace(cfg.Devices[i].Type)
		cfg.Devices[i].Serial = strings.TrimSpace(cfg.Devices[i].Serial)
	}
	return cfg
}

// UpdateConfig replaces the checker configuration.
func (c *Checker) UpdateConfig(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = NormalizeConfig(cfg)
}

// Config returns a copy of the current configuration.
func (c *Checker) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneConfig(c.cfg)
}

// Check probes all configured devices.
func (c *Checker) Check(ctx context.Context) (*Report, error) {
	cfg := c.Config()
	report := &Report{
		Enabled:   cfg.Enabled,
		Status:    StatusDisabled,
		CheckedAt: c.now().UTC(),
	}

	if !cfg.Enabled {
		report.Message = "disk health checks are disabled"
		return report, nil
	}
	if len(cfg.Devices) == 0 {
		report.Status = StatusUnavailable
		report.Message = "disk health checks are enabled but no devices are configured"
		report.Warnings = []string{report.Message}
		return report, nil
	}

	overall := StatusOK
	for i, device := range cfg.Devices {
		status := c.checkDevice(ctx, cfg, device)
		report.Devices = append(report.Devices, status)
		overall = maxStatus(overall, status.Status)
		if status.Status != StatusOK && status.Message != "" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", deviceLabel(status, i), status.Message))
		}
	}

	report.Status = overall
	report.Message = reportMessage(report)
	return report, nil
}

func (c *Checker) checkDevice(ctx context.Context, cfg Config, device DeviceConfig) DeviceStatus {
	warnC, criticalC := deviceTemperatureThresholds(cfg, device)
	status := DeviceStatus{
		Name:                 device.Name,
		Path:                 device.Path,
		Type:                 device.Type,
		ExpectedSerial:       device.Serial,
		TemperatureWarningC:  warnC,
		TemperatureCriticalC: criticalC,
		Status:               StatusOK,
	}

	if device.Path == "" {
		status.Status = StatusCritical
		status.Message = "device path is not configured"
		return status
	}
	if _, err := os.Lstat(device.Path); err != nil {
		status.Present = false
		if errors.Is(err, os.ErrNotExist) {
			status.Status = StatusCritical
			status.Message = "device is missing"
			return status
		}
		status.Status = StatusWarning
		status.Message = fmt.Sprintf("device stat failed: %s", err)
		return status
	}
	status.Present = true

	probeCtx, cancel := context.WithTimeout(ctx, cfg.ProbeTimeout)
	defer cancel()

	args := []string{"--json", "--all"}
	if device.Type != "" {
		args = append(args, "--device", device.Type)
	}
	args = append(args, device.Path)

	result, runErr := c.runner(probeCtx, cfg.Command, args...)
	if len(bytes.TrimSpace(result.Stdout)) == 0 {
		status.Status = StatusUnavailable
		if runErr != nil {
			status.Message = fmt.Sprintf("smart probe failed: %s", sanitizeCommandError(runErr))
		} else {
			status.Message = "smart probe returned no JSON"
		}
		return status
	}

	parsed, err := parseSMARTJSON(result.Stdout)
	if err != nil {
		status.Status = StatusUnavailable
		status.Message = fmt.Sprintf("smart probe returned invalid JSON: %s", err)
		return status
	}

	status.Model = parsed.Model
	status.Serial = parsed.Serial
	status.SMARTAvailable = parsed.SMARTPassed != nil
	status.SMARTPassed = parsed.SMARTPassed
	status.TemperatureC = parsed.TemperatureC
	status.PowerOnHours = parsed.PowerOnHours
	status.WearPercentUsed = parsed.WearPercentUsed
	status.AvailableSparePct = parsed.AvailableSparePct
	status.SpareThresholdPct = parsed.SpareThresholdPct
	status.MediaErrors = parsed.MediaErrors
	status.NVMeCriticalWarning = parsed.NVMeCriticalWarning

	status.Status, status.Message = evaluateDeviceStatus(device, status, runErr, warnC, criticalC, cfg.MediaWearWarningPct, cfg.MediaWearCriticalPct)
	return status
}

func evaluateDeviceStatus(device DeviceConfig, status DeviceStatus, runErr error, warnC, criticalC, wearWarnPct, wearCriticalPct int) (string, string) {
	if device.Serial != "" && status.Serial != "" && !strings.EqualFold(device.Serial, status.Serial) {
		return StatusCritical, "device serial does not match configured serial"
	}
	if device.Serial != "" && status.Serial == "" {
		return StatusWarning, "device serial is unavailable"
	}
	if status.SMARTPassed != nil && !*status.SMARTPassed {
		return StatusCritical, "SMART self-assessment failed"
	}
	if status.NVMeCriticalWarning != nil && *status.NVMeCriticalWarning != 0 {
		return StatusCritical, fmt.Sprintf("NVMe critical warning bitmask 0x%x reported", *status.NVMeCriticalWarning)
	}
	if status.AvailableSparePct != nil && status.SpareThresholdPct != nil && *status.AvailableSparePct <= *status.SpareThresholdPct {
		return StatusCritical, fmt.Sprintf("available spare %d%% is at or below threshold %d%%", *status.AvailableSparePct, *status.SpareThresholdPct)
	}
	if status.TemperatureC != nil {
		switch {
		case criticalC > 0 && *status.TemperatureC >= criticalC:
			return StatusCritical, fmt.Sprintf("temperature %d C reached critical threshold %d C", *status.TemperatureC, criticalC)
		case warnC > 0 && *status.TemperatureC >= warnC:
			return StatusWarning, fmt.Sprintf("temperature %d C reached warning threshold %d C", *status.TemperatureC, warnC)
		}
	}
	if status.WearPercentUsed != nil {
		switch {
		case wearCriticalPct > 0 && *status.WearPercentUsed >= wearCriticalPct:
			return StatusCritical, fmt.Sprintf("media wear used %d%% reached critical threshold %d%%", *status.WearPercentUsed, wearCriticalPct)
		case wearWarnPct > 0 && *status.WearPercentUsed >= wearWarnPct:
			return StatusWarning, fmt.Sprintf("media wear used %d%% reached warning threshold %d%%", *status.WearPercentUsed, wearWarnPct)
		}
	}
	if status.MediaErrors != nil && *status.MediaErrors > 0 {
		return StatusWarning, fmt.Sprintf("media error count is %d", *status.MediaErrors)
	}
	if status.SMARTPassed == nil {
		return StatusWarning, "SMART self-assessment is unavailable"
	}
	if runErr != nil {
		return StatusWarning, fmt.Sprintf("smart probe returned warning status: %s", sanitizeCommandError(runErr))
	}
	return StatusOK, "device is healthy"
}

type parsedSMART struct {
	Model               string
	Serial              string
	SMARTPassed         *bool
	TemperatureC        *int
	PowerOnHours        *int64
	WearPercentUsed     *int
	AvailableSparePct   *int
	SpareThresholdPct   *int
	MediaErrors         *int64
	NVMeCriticalWarning *int
}

type smartctlJSON struct {
	ModelName    string `json:"model_name"`
	ModelFamily  string `json:"model_family"`
	SerialNumber string `json:"serial_number"`
	Device       struct {
		ModelName string `json:"model_name"`
		Name      string `json:"name"`
	} `json:"device"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	NVMESmartHealth *struct {
		CriticalWarning       int   `json:"critical_warning"`
		AvailableSpare        int   `json:"available_spare"`
		AvailableSpareThresh  int   `json:"available_spare_threshold"`
		PercentageUsed        int   `json:"percentage_used"`
		MediaErrors           int64 `json:"media_errors"`
		UnsafeShutdowns       int64 `json:"unsafe_shutdowns"`
		NumErrLogEntries      int64 `json:"num_err_log_entries"`
		WarningCompositeTemp  int   `json:"warning_temp_time"`
		CriticalCompositeTemp int   `json:"critical_comp_time"`
	} `json:"nvme_smart_health_information_log"`
	ATAAttributes *struct {
		Table []struct {
			ID    int    `json:"id"`
			Name  string `json:"name"`
			Value int    `json:"value"`
			Raw   struct {
				Value  int64  `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
}

func parseSMARTJSON(data []byte) (parsedSMART, error) {
	var raw smartctlJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return parsedSMART{}, err
	}

	parsed := parsedSMART{
		Model:  firstNonEmpty(raw.ModelName, raw.Device.ModelName, raw.ModelFamily, raw.Device.Name),
		Serial: strings.TrimSpace(raw.SerialNumber),
	}
	if raw.SmartStatus != nil {
		passed := raw.SmartStatus.Passed
		parsed.SMARTPassed = &passed
	}
	if raw.Temperature != nil && raw.Temperature.Current != 0 {
		temp := raw.Temperature.Current
		parsed.TemperatureC = &temp
	}
	if raw.PowerOnTime != nil && raw.PowerOnTime.Hours != 0 {
		hours := raw.PowerOnTime.Hours
		parsed.PowerOnHours = &hours
	}
	if raw.NVMESmartHealth != nil {
		criticalWarning := raw.NVMESmartHealth.CriticalWarning
		parsed.NVMeCriticalWarning = &criticalWarning
		availableSpare := raw.NVMESmartHealth.AvailableSpare
		parsed.AvailableSparePct = &availableSpare
		spareThreshold := raw.NVMESmartHealth.AvailableSpareThresh
		parsed.SpareThresholdPct = &spareThreshold
		wear := raw.NVMESmartHealth.PercentageUsed
		parsed.WearPercentUsed = &wear
		mediaErrors := raw.NVMESmartHealth.MediaErrors
		parsed.MediaErrors = &mediaErrors
	}
	if raw.ATAAttributes != nil {
		for _, attr := range raw.ATAAttributes.Table {
			normalized := strings.ToLower(strings.TrimSpace(attr.Name))
			if parsed.TemperatureC == nil && (attr.ID == 190 || attr.ID == 194 || strings.Contains(normalized, "temperature")) {
				if value, ok := parseAttributeRawValue(attr.Raw.Value, attr.Raw.String); ok {
					temp := int(value)
					parsed.TemperatureC = &temp
				}
			}
			if parsed.PowerOnHours == nil && (attr.ID == 9 || strings.Contains(normalized, "power_on")) {
				if value, ok := parseAttributeRawValue(attr.Raw.Value, attr.Raw.String); ok {
					hours := value
					parsed.PowerOnHours = &hours
				}
			}
			if parsed.WearPercentUsed == nil {
				if wear, ok := parseATAWearPercentUsed(attr.ID, normalized, attr.Value, attr.Raw.Value, attr.Raw.String); ok {
					parsed.WearPercentUsed = &wear
				}
			}
			if parsed.MediaErrors == nil && (attr.ID == 187 || strings.Contains(normalized, "reported_uncorrect") || strings.Contains(normalized, "media_error")) {
				if value, ok := parseAttributeRawValue(attr.Raw.Value, attr.Raw.String); ok {
					mediaErrors := value
					parsed.MediaErrors = &mediaErrors
				}
			}
		}
	}

	return parsed, nil
}

func parseATAWearPercentUsed(id int, normalizedName string, normalizedValue int, rawValue int64, rawString string) (int, bool) {
	if strings.Contains(normalizedName, "percentage_used") || strings.Contains(normalizedName, "percent_lifetime_used") {
		if value, ok := parseAttributeRawValue(rawValue, rawString); ok {
			return int(value), true
		}
	}
	if strings.Contains(normalizedName, "percent_lifetime_remain") ||
		strings.Contains(normalizedName, "media_wearout_indicator") ||
		strings.Contains(normalizedName, "wear_leveling_count") ||
		id == 177 || id == 231 || id == 233 {
		if normalizedValue > 0 && normalizedValue <= 100 {
			return 100 - normalizedValue, true
		}
	}
	return 0, false
}

func parseAttributeRawValue(rawValue int64, rawString string) (int64, bool) {
	if rawValue != 0 {
		return rawValue, true
	}
	fields := strings.Fields(rawString)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func deviceTemperatureThresholds(cfg Config, device DeviceConfig) (int, int) {
	warnC := cfg.TemperatureWarningC
	criticalC := cfg.TemperatureCriticalC
	if device.TemperatureWarningC != 0 {
		warnC = device.TemperatureWarningC
	}
	if device.TemperatureCriticalC != 0 {
		criticalC = device.TemperatureCriticalC
	}
	return warnC, criticalC
}

func reportMessage(report *Report) string {
	switch report.Status {
	case StatusOK:
		return "all configured disks are healthy"
	case StatusCritical:
		return "one or more disks require immediate attention"
	case StatusWarning:
		return "one or more disks need attention"
	case StatusUnavailable:
		return "disk health status is unavailable"
	default:
		return report.Status
	}
}

func maxStatus(current, candidate string) string {
	if statusRank(candidate) > statusRank(current) {
		return candidate
	}
	return current
}

func statusRank(status string) int {
	switch status {
	case StatusCritical:
		return 4
	case StatusWarning:
		return 3
	case StatusUnavailable:
		return 2
	case StatusOK:
		return 1
	default:
		return 0
	}
}

func deviceLabel(status DeviceStatus, index int) string {
	if status.Name != "" {
		return status.Name
	}
	return fmt.Sprintf("device %d", index+1)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func sanitizeCommandError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe timed out"
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("exit status %d", exitErr.ExitCode())
	}
	return err.Error()
}

func defaultRunner(ctx context.Context, command string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	}
	return result, err
}

func cloneConfig(cfg Config) Config {
	cfg.Devices = cloneDeviceConfigs(cfg.Devices)
	return cfg
}

func cloneDeviceConfigs(devices []DeviceConfig) []DeviceConfig {
	if devices == nil {
		return nil
	}
	return append([]DeviceConfig(nil), devices...)
}

func cloneReport(report *Report) *Report {
	if report == nil {
		return nil
	}
	clone := *report
	clone.Devices = append([]DeviceStatus(nil), report.Devices...)
	clone.Warnings = append([]string(nil), report.Warnings...)
	return &clone
}

// AlertSender sends disk health events through the global alert channel.
type AlertSender interface {
	SendEvent(ctx context.Context, event alerts.EventPayload) error
}

// Recorder persists disk health events to a local audit trail.
type Recorder func(ctx context.Context, report *Report) error

// Monitor periodically checks disks and sends warning/critical alert events.
type Monitor struct {
	checker *Checker
	sender  AlertSender
	logger  zerolog.Logger

	lifecycleMu sync.Mutex
	mu          sync.Mutex
	baseCtx     context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	lastReport      *Report
	lastAlert       time.Time
	lastAlertStatus string

	recorder                 Recorder
	lastActivityRecord       time.Time
	lastActivityRecordStatus string
}

// NewMonitor creates a disk health monitor.
func NewMonitor(cfg Config, sender AlertSender, logger zerolog.Logger, opts ...Option) *Monitor {
	return &Monitor{
		checker: NewChecker(cfg, logger, opts...),
		sender:  sender,
		logger:  logger,
	}
}

// Start begins periodic disk health checks.
func (m *Monitor) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.mu.Lock()
	m.baseCtx = ctx
	cfg := m.checker.Config()
	m.mu.Unlock()

	m.restartLocked(cfg)
}

// Stop stops periodic disk health checks.
func (m *Monitor) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stopLoopLocked()
	m.wg.Wait()
}

// UpdateConfig replaces monitor configuration and restarts the loop.
func (m *Monitor) UpdateConfig(cfg Config) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.restartLocked(NormalizeConfig(cfg))
}

// SetActivityRecorder registers a local audit sink for warning and critical disk health events.
func (m *Monitor) SetActivityRecorder(recorder Recorder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recorder = recorder
}

// Check performs an immediate disk health check.
func (m *Monitor) Check(ctx context.Context) (*Report, error) {
	report, err := m.checker.Check(ctx)
	if err != nil {
		return nil, err
	}
	m.storeReport(report)
	return report, nil
}

// LastReport returns the most recent periodic or manual report.
func (m *Monitor) LastReport() *Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneReport(m.lastReport)
}

func (m *Monitor) restartLocked(cfg Config) {
	m.stopLoopLocked()
	m.wg.Wait()

	cfg = NormalizeConfig(cfg)
	m.checker.UpdateConfig(cfg)

	m.mu.Lock()
	baseCtx := m.baseCtx
	m.mu.Unlock()

	if baseCtx == nil {
		return
	}
	if !cfg.Enabled {
		m.logger.Info().Msg("Disk health monitoring disabled")
		return
	}
	if cfg.CheckInterval <= 0 {
		m.logger.Warn().Dur("interval", cfg.CheckInterval).Msg("Disk health monitoring disabled due to non-positive interval")
		return
	}

	loopCtx, cancel := context.WithCancel(baseCtx)
	m.mu.Lock()
	m.cancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go func(interval time.Duration) {
		defer m.wg.Done()
		m.checkAndAlert(loopCtx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				m.checkAndAlert(loopCtx)
			}
		}
	}(cfg.CheckInterval)

	m.logger.Info().
		Dur("interval", cfg.CheckInterval).
		Int("devices", len(cfg.Devices)).
		Msg("Disk health monitoring started")
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

func (m *Monitor) checkAndAlert(ctx context.Context) {
	report, err := m.checker.Check(ctx)
	if err != nil {
		m.logger.Warn().Err(err).Msg("failed to check disk health")
		return
	}
	m.storeReport(report)
	if !shouldAlertForReport(report) {
		if report.Status == StatusOK {
			m.mu.Lock()
			m.lastAlertStatus = ""
			m.lastAlert = time.Time{}
			m.lastActivityRecordStatus = ""
			m.lastActivityRecord = time.Time{}
			m.mu.Unlock()
		}
		return
	}

	cfg := m.checker.Config()
	if !m.shouldSendAlert(report.Status, cfg.CooldownPeriod) {
		return
	}
	m.recordActivityIfDue(ctx, report, cfg.CooldownPeriod)
	if m.sender == nil {
		m.logger.Warn().Str("status", report.Status).Msg("disk health alert triggered without alert sender")
		m.recordAlert(report.Status)
		return
	}

	event := alerts.EventPayload{
		Type:      "disk_health",
		Level:     alertLevelForStatus(report.Status),
		Message:   report.Message,
		Timestamp: report.CheckedAt,
		Details:   diskHealthAlertDetails(report),
	}
	if err := m.sender.SendEvent(ctx, event); err != nil {
		m.logger.Warn().Err(err).Str("status", report.Status).Msg("failed to send disk health alert")
		return
	}
	m.recordAlert(report.Status)
}

func (m *Monitor) storeReport(report *Report) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastReport = cloneReport(report)
}

func (m *Monitor) shouldSendAlert(status string, cooldown time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status != m.lastAlertStatus {
		return true
	}
	if cooldown <= 0 {
		return true
	}
	return time.Since(m.lastAlert) >= cooldown
}

func (m *Monitor) recordAlert(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAlertStatus = status
	m.lastAlert = time.Now()
}

func (m *Monitor) activityRecorder() Recorder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recorder
}

func (m *Monitor) shouldRecordActivity(status string, cooldown time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status != m.lastActivityRecordStatus {
		return true
	}
	if cooldown <= 0 {
		return true
	}
	return time.Since(m.lastActivityRecord) >= cooldown
}

func (m *Monitor) recordActivity(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastActivityRecordStatus = status
	m.lastActivityRecord = time.Now()
}

func (m *Monitor) recordActivityIfDue(ctx context.Context, report *Report, cooldown time.Duration) {
	recorder := m.activityRecorder()
	if recorder == nil || report == nil || !m.shouldRecordActivity(report.Status, cooldown) {
		return
	}
	if err := recorder(ctx, cloneReport(report)); err != nil {
		m.logger.Warn().Err(err).Str("status", report.Status).Msg("failed to record disk health activity")
		return
	}
	m.recordActivity(report.Status)
}

func shouldAlertForReport(report *Report) bool {
	if report == nil {
		return false
	}
	return report.Status == StatusWarning || report.Status == StatusCritical || report.Status == StatusUnavailable
}

func alertLevelForStatus(status string) alerts.AlertLevel {
	if status == StatusCritical {
		return alerts.AlertLevelCritical
	}
	return alerts.AlertLevelWarning
}

func diskHealthAlertDetails(report *Report) map[string]any {
	if report == nil {
		return map[string]any{
			"status": "unknown",
		}
	}
	warningDeviceCount := 0
	criticalDeviceCount := 0
	unavailableDeviceCount := 0
	for _, device := range report.Devices {
		switch device.Status {
		case StatusWarning:
			warningDeviceCount++
		case StatusCritical:
			criticalDeviceCount++
		case StatusUnavailable:
			unavailableDeviceCount++
		}
	}
	details := map[string]any{
		"status":                   report.Status,
		"checked_at":               report.CheckedAt.UTC().Format(time.RFC3339),
		"device_count":             len(report.Devices),
		"warning_count":            len(report.Warnings),
		"warning_device_count":     warningDeviceCount,
		"critical_device_count":    criticalDeviceCount,
		"unavailable_device_count": unavailableDeviceCount,
	}
	return details
}
