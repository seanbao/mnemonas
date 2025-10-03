package diskhealth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/alerts"
)

func testLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

func testDevicePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, []byte("device"), 0600); err != nil {
		t.Fatalf("failed to create fake device: %v", err)
	}
	return path
}

func smartJSON(passed bool, temp int, serial string) []byte {
	return []byte(fmt.Sprintf(`{
		"model_name": "TestDisk",
		"serial_number": %q,
		"smart_status": {"passed": %t},
		"temperature": {"current": %d},
		"power_on_time": {"hours": 1234}
	}`, serial, passed, temp))
}

func TestCheckerDisabled(t *testing.T) {
	checker := NewChecker(Config{}, testLogger())

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusDisabled {
		t.Fatalf("status = %q, want %q", report.Status, StatusDisabled)
	}
}

func TestCheckerReportsHealthySmartDevice(t *testing.T) {
	devicePath := testDevicePath(t)
	var gotArgs []string
	checker := NewChecker(Config{
		Enabled: true,
		Devices: []DeviceConfig{{
			Name:   "data-disk",
			Path:   devicePath,
			Type:   "sat",
			Serial: "SER123",
		}},
	}, testLogger(), WithRunner(func(ctx context.Context, command string, args ...string) (CommandResult, error) {
		gotArgs = append([]string(nil), args...)
		return CommandResult{Stdout: smartJSON(true, 42, "SER123")}, nil
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusOK {
		t.Fatalf("status = %q, want %q; report=%+v", report.Status, StatusOK, report)
	}
	if len(report.Devices) != 1 {
		t.Fatalf("device count = %d, want 1", len(report.Devices))
	}
	device := report.Devices[0]
	if !device.Present || !device.SMARTAvailable || device.SMARTPassed == nil || !*device.SMARTPassed {
		t.Fatalf("unexpected SMART fields: %+v", device)
	}
	if device.TemperatureC == nil || *device.TemperatureC != 42 {
		t.Fatalf("temperature = %v, want 42", device.TemperatureC)
	}
	if strings.Join(gotArgs, " ") != "--json --all --device sat "+devicePath {
		t.Fatalf("smartctl args = %q", strings.Join(gotArgs, " "))
	}
}

func TestCheckerReportsTemperatureWarning(t *testing.T) {
	devicePath := testDevicePath(t)
	checker := NewChecker(Config{
		Enabled:              true,
		TemperatureWarningC:  45,
		TemperatureCriticalC: 60,
		Devices:              []DeviceConfig{{Path: devicePath}},
	}, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: smartJSON(true, 51, "SER123")}, nil
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusWarning {
		t.Fatalf("status = %q, want %q; warnings=%v", report.Status, StatusWarning, report.Warnings)
	}
}

func TestCheckerWarningsHideUnnamedDevicePath(t *testing.T) {
	devicePath := filepath.Join(t.TempDir(), "ata-Samsung_SECRET123")
	if err := os.WriteFile(devicePath, []byte("device"), 0600); err != nil {
		t.Fatalf("failed to create fake device: %v", err)
	}
	checker := NewChecker(Config{
		Enabled: true,
		Devices: []DeviceConfig{{
			Path: devicePath,
		}},
	}, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: smartJSON(false, 40, "SER123")}, nil
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusCritical {
		t.Fatalf("status = %q, want %q", report.Status, StatusCritical)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if strings.Contains(warnings, devicePath) || strings.Contains(warnings, "SECRET123") {
		t.Fatalf("warnings leaked unnamed device path: %q", warnings)
	}
	if !strings.Contains(warnings, "device 1") {
		t.Fatalf("warnings = %q, want generic device label", warnings)
	}
}

func TestCheckerReportsCriticalForNVMeMediaWear(t *testing.T) {
	devicePath := testDevicePath(t)
	checker := NewChecker(Config{
		Enabled:              true,
		MediaWearWarningPct:  80,
		MediaWearCriticalPct: 95,
		Devices:              []DeviceConfig{{Path: devicePath}},
	}, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: []byte(`{
			"model_name": "NVMe",
			"serial_number": "NVME123",
			"smart_status": {"passed": true},
			"nvme_smart_health_information_log": {
				"critical_warning": 0,
				"available_spare": 90,
				"available_spare_threshold": 10,
				"percentage_used": 101,
				"media_errors": 0
			}
		}`)}, nil
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusCritical {
		t.Fatalf("status = %q, want %q; warnings=%v", report.Status, StatusCritical, report.Warnings)
	}
	device := report.Devices[0]
	if device.WearPercentUsed == nil || *device.WearPercentUsed != 101 {
		t.Fatalf("wear percent = %v, want 101", device.WearPercentUsed)
	}
	if !strings.Contains(device.Message, "media wear") {
		t.Fatalf("expected media wear message, got %q", device.Message)
	}
}

func TestCheckerReportsCriticalForMissingDevice(t *testing.T) {
	checker := NewChecker(Config{
		Enabled: true,
		Devices: []DeviceConfig{{
			Name: "missing",
			Path: filepath.Join(t.TempDir(), "missing"),
		}},
	}, testLogger())

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusCritical {
		t.Fatalf("status = %q, want %q", report.Status, StatusCritical)
	}
	if report.Devices[0].Present {
		t.Fatalf("expected missing device to be marked not present")
	}
}

func TestCheckerReportsCriticalForSerialMismatch(t *testing.T) {
	devicePath := testDevicePath(t)
	checker := NewChecker(Config{
		Enabled: true,
		Devices: []DeviceConfig{{
			Path:   devicePath,
			Serial: "EXPECTED",
		}},
	}, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: smartJSON(true, 40, "ACTUAL")}, nil
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusCritical {
		t.Fatalf("status = %q, want %q", report.Status, StatusCritical)
	}
	if !strings.Contains(report.Devices[0].Message, "serial") {
		t.Fatalf("expected serial mismatch message, got %q", report.Devices[0].Message)
	}
}

func TestParseSMARTJSONFallsBackToATAAttributes(t *testing.T) {
	parsed, err := parseSMARTJSON([]byte(`{
		"model_name": "SATA",
		"serial_number": "SATA123",
		"smart_status": {"passed": true},
		"ata_smart_attributes": {
			"table": [
				{"id": 9, "name": "Power_On_Hours", "raw": {"value": 987}},
				{"id": 194, "name": "Temperature_Celsius", "raw": {"string": "37 Min/Max 20/50"}}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("parseSMARTJSON() error: %v", err)
	}
	if parsed.TemperatureC == nil || *parsed.TemperatureC != 37 {
		t.Fatalf("temperature = %v, want 37", parsed.TemperatureC)
	}
	if parsed.PowerOnHours == nil || *parsed.PowerOnHours != 987 {
		t.Fatalf("power hours = %v, want 987", parsed.PowerOnHours)
	}
}

func TestParseSMARTJSONReadsATAWearFromNormalizedValue(t *testing.T) {
	parsed, err := parseSMARTJSON([]byte(`{
		"model_name": "SATA",
		"serial_number": "SATA123",
		"smart_status": {"passed": true},
		"ata_smart_attributes": {
			"table": [
				{"id": 233, "name": "Media_Wearout_Indicator", "value": 12, "raw": {"value": 0}}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("parseSMARTJSON() error: %v", err)
	}
	if parsed.WearPercentUsed == nil || *parsed.WearPercentUsed != 88 {
		t.Fatalf("wear percent = %v, want 88", parsed.WearPercentUsed)
	}
}

type fakeAlertSender struct {
	events []alerts.EventPayload
}

func (s *fakeAlertSender) SendEvent(_ context.Context, event alerts.EventPayload) error {
	s.events = append(s.events, event)
	return nil
}

func TestMonitorSendsAlertOnCriticalAndRespectsCooldown(t *testing.T) {
	devicePath := testDevicePath(t)
	sender := &fakeAlertSender{}
	var recorded []*Report
	monitor := NewMonitor(Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		CooldownPeriod: time.Hour,
		Devices:        []DeviceConfig{{Path: devicePath}},
	}, sender, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: smartJSON(false, 40, "SER123")}, nil
	}))
	monitor.SetActivityRecorder(func(_ context.Context, report *Report) error {
		recorded = append(recorded, report)
		return nil
	})

	monitor.checkAndAlert(context.Background())
	monitor.checkAndAlert(context.Background())

	if len(sender.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sender.events))
	}
	if len(recorded) != 1 {
		t.Fatalf("activity records = %d, want 1", len(recorded))
	}
	if recorded[0].Status != StatusCritical {
		t.Fatalf("recorded status = %q, want %q", recorded[0].Status, StatusCritical)
	}
	if sender.events[0].Level != alerts.AlertLevelCritical {
		t.Fatalf("event level = %q, want critical", sender.events[0].Level)
	}
}

func TestMonitorAlertWarningsHideUnnamedDevicePath(t *testing.T) {
	devicePath := filepath.Join(t.TempDir(), "nvme-Backup_SECRET999")
	if err := os.WriteFile(devicePath, []byte("device"), 0600); err != nil {
		t.Fatalf("failed to create fake device: %v", err)
	}
	sender := &fakeAlertSender{}
	monitor := NewMonitor(Config{
		Enabled:        true,
		CheckInterval:  time.Hour,
		CooldownPeriod: time.Hour,
		Devices:        []DeviceConfig{{Path: devicePath}},
	}, sender, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: smartJSON(false, 40, "SER123")}, nil
	}))

	monitor.checkAndAlert(context.Background())

	if len(sender.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sender.events))
	}
	warnings, ok := sender.events[0].Details["warnings"].([]string)
	if !ok {
		t.Fatalf("warnings detail = %#v, want []string", sender.events[0].Details["warnings"])
	}
	joined := strings.Join(warnings, "\n")
	if strings.Contains(joined, devicePath) || strings.Contains(joined, "SECRET999") {
		t.Fatalf("alert warnings leaked unnamed device path: %q", joined)
	}
}

func TestCheckerMarksInvalidSmartOutputUnavailable(t *testing.T) {
	devicePath := testDevicePath(t)
	checker := NewChecker(Config{
		Enabled: true,
		Devices: []DeviceConfig{{Path: devicePath}},
	}, testLogger(), WithRunner(func(context.Context, string, ...string) (CommandResult, error) {
		return CommandResult{Stdout: []byte("{")}, errors.New("exit status 2")
	}))

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if report.Status != StatusUnavailable {
		t.Fatalf("status = %q, want %q", report.Status, StatusUnavailable)
	}
}
