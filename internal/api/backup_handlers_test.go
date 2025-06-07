package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/backup"
	"github.com/seanbao/mnemonas/internal/config"
)

type backupEventRecorder struct {
	events []alerts.EventPayload
}

func (r *backupEventRecorder) UpdateConfig(alerts.Config) {}

func (r *backupEventRecorder) SendEvent(_ context.Context, event alerts.EventPayload) error {
	r.events = append(r.events, event)
	return nil
}

func TestServer_BackupEndpoints_RunAndRestoreDrill(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")
	mustWriteAPITestFile(t, filepath.Join(source, "docs", "note.txt"), "hello")
	mustWriteAPITestFile(t, configPath, "[server]\nport = 8080\n")

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		ConfigPath:  configPath,
		BackupJobs: []config.BackupJobConfig{{
			ID:                "home",
			Name:              "Home backup",
			Type:              backup.JobTypeLocal,
			Source:            source,
			Destination:       destination,
			IncludeConfig:     true,
			VerifyAfterBackup: true,
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	runResult := runBackupAPIRequest[backup.RunResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/run", nil, http.StatusOK)
	if runResult.Status != backup.StatusCompleted {
		t.Fatalf("backup status = %q, want %q", runResult.Status, backup.StatusCompleted)
	}
	if runResult.FileCount != 2 {
		t.Fatalf("backup file count = %d, want 2", runResult.FileCount)
	}

	drillResult := runBackupAPIRequest[backup.RestoreDrillResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-drill", []byte(`{"keep_artifact":true}`), http.StatusOK)
	if drillResult.Status != backup.StatusCompleted {
		t.Fatalf("restore drill status = %q, want %q", drillResult.Status, backup.StatusCompleted)
	}
	if drillResult.RestoredPath == "" {
		t.Fatal("restore drill did not keep artifact path")
	}
	if _, err := os.Stat(filepath.Join(drillResult.RestoredPath, "data", "docs", "note.txt")); err != nil {
		t.Fatalf("restored file stat error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restored")
	previewBody := []byte(`{"target_path":` + strconv.Quote(restoreTarget) + `,"include_config":true}`)
	previewResult := runBackupAPIRequest[backup.RestorePreviewResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", previewBody, http.StatusOK)
	if previewResult.Status != backup.StatusCompleted {
		t.Fatalf("restore preview status = %q, want %q", previewResult.Status, backup.StatusCompleted)
	}
	if previewResult.TargetPath != restoreTarget || previewResult.FileCount != 2 || !previewResult.ConfigAvailable || !previewResult.ConfigIncluded {
		t.Fatalf("unexpected restore preview result: %+v", previewResult)
	}
	if len(previewResult.SamplePaths) != 2 || previewResult.SamplePaths[0] != "docs/note.txt" || previewResult.SamplePaths[1] != ".mnemonas-restore/config.toml" {
		t.Fatalf("restore preview samples = %#v", previewResult.SamplePaths)
	}
	if _, err := os.Stat(restoreTarget); !os.IsNotExist(err) {
		t.Fatalf("restore preview target stat error = %v, want not exist", err)
	}

	restoreBody := []byte(`{"target_path":` + strconv.Quote(restoreTarget) + `,"include_config":true}`)
	restoreResult := runBackupAPIRequest[backup.RestoreResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore", restoreBody, http.StatusOK)
	if restoreResult.Status != backup.StatusCompleted {
		t.Fatalf("restore status = %q, want %q", restoreResult.Status, backup.StatusCompleted)
	}
	if restoreResult.TargetPath != restoreTarget || !restoreResult.ConfigRestored {
		t.Fatalf("unexpected restore result: %+v", restoreResult)
	}
	if _, err := os.Stat(filepath.Join(restoreTarget, "docs", "note.txt")); err != nil {
		t.Fatalf("explicit restore file stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml")); err != nil {
		t.Fatalf("explicit restore config stat error: %v", err)
	}
	verifyResult := runBackupAPIRequest[backup.RestoreVerifyResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-verify", []byte(`{"target_path":`+strconv.Quote(restoreTarget)+`}`), http.StatusOK)
	if verifyResult.Status != backup.StatusCompleted {
		t.Fatalf("restore verify status = %q, want %q", verifyResult.Status, backup.StatusCompleted)
	}
	if verifyResult.TargetPath != restoreTarget || verifyResult.FileCount != restoreResult.FileCount || verifyResult.VerifiedBytes != restoreResult.VerifiedBytes || !verifyResult.ConfigFound {
		t.Fatalf("unexpected restore verify result: %+v", verifyResult)
	}

	jobView := runBackupAPIRequest[backup.JobView](t, server, http.MethodGet, "/api/v1/maintenance/backups/home", nil, http.StatusOK)
	if jobView.LastRestore == nil || jobView.LastRestore.ID != restoreResult.ID || jobView.LastRestore.TargetPath != restoreTarget {
		t.Fatalf("backup job missing latest restore audit: %+v", jobView.LastRestore)
	}
	if len(jobView.RestoreHistory) != 1 || jobView.RestoreHistory[0].ID != restoreResult.ID {
		t.Fatalf("backup job restore history = %+v, want latest restore", jobView.RestoreHistory)
	}
}

func TestServer_BackupEndpoints_ErrorMapping(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		BackupJobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        backup.JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodGet, "/api/v1/maintenance/backups/missing", nil, http.StatusNotFound)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-drill", nil, http.StatusConflict)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-verify", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)

	disabledServer, err := NewServer(zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "disabled-state"),
		StorageRoot: source,
		BackupJobs: []config.BackupJobConfig{{
			ID:          "disabled",
			Name:        "Disabled backup",
			Type:        backup.JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "disabled-backups"),
			Disabled:    true,
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() disabled error: %v", err)
	}
	runBackupAPIRequest[json.RawMessage](t, disabledServer, http.MethodPost, "/api/v1/maintenance/backups/disabled/run", nil, http.StatusConflict)
}

func TestServer_BackupEndpoints_FailureSendsAlertEvent(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	recorder := &backupEventRecorder{}
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		BackupRoot:   filepath.Join(tmpDir, "state"),
		StorageRoot:  source,
		AlertMonitor: recorder,
		BackupJobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        backup.JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/run", nil, http.StatusInternalServerError)

	if len(recorder.events) != 1 {
		t.Fatalf("backup alert event count = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Type != backup.NotificationTypeBackupRun || event.Level != alerts.AlertLevelCritical {
		t.Fatalf("backup alert event type/level = %s/%s, want backup_run/critical", event.Type, event.Level)
	}
	if event.Details["job_id"] != "home" || event.Details["status"] != backup.StatusFailed {
		t.Fatalf("unexpected backup alert details: %+v", event.Details)
	}
}

func TestBackupAlertNotifier_MapsRestoreDrillReminderMetadata(t *testing.T) {
	recorder := &backupEventRecorder{}
	notifier := newBackupAlertNotifier(recorder, zerolog.Nop())
	if notifier == nil {
		t.Fatal("newBackupAlertNotifier() returned nil")
	}
	lastSuccess := time.Date(2026, 5, 9, 2, 3, 4, 0, time.UTC)
	lastDrill := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
	err := notifier.NotifyBackupEvent(context.Background(), backup.NotificationEvent{
		Type:                backup.NotificationTypeRestoreDrill,
		Level:               backup.NotificationLevelWarning,
		Message:             "restore drill stale",
		JobID:               "home",
		JobName:             "Home backup",
		JobType:             backup.JobTypeLocal,
		RunID:               "20260501T030000Z",
		Trigger:             backup.NotificationTriggerReminder,
		Status:              "stale",
		StartedAt:           lastDrill,
		LastSuccessfulRunAt: &lastSuccess,
		LastRestoreDrillAt:  &lastDrill,
		StaleAfter:          "720h0m0s",
		ReminderCooldown:    "24h0m0s",
		Timestamp:           time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyBackupEvent() error: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("alert event count = %d, want 1", len(recorder.events))
	}
	details := recorder.events[0].Details
	if details["trigger"] != backup.NotificationTriggerReminder || details["status"] != "stale" {
		t.Fatalf("unexpected reminder details: %+v", details)
	}
	if details["stale_after"] != "720h0m0s" || details["reminder_cooldown"] != "24h0m0s" {
		t.Fatalf("missing reminder timing details: %+v", details)
	}
	if _, ok := details["last_successful_run_at"].(*time.Time); !ok {
		t.Fatalf("last_successful_run_at detail = %#v, want *time.Time", details["last_successful_run_at"])
	}
	if _, ok := details["last_restore_drill_at"].(*time.Time); !ok {
		t.Fatalf("last_restore_drill_at detail = %#v, want *time.Time", details["last_restore_drill_at"])
	}
}

func TestServer_BackupEndpoints_Uninitialized(t *testing.T) {
	server, err := NewServer(zerolog.Nop(), &ServerConfig{})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodGet, "/api/v1/maintenance/backups", nil, http.StatusServiceUnavailable)
}

func runBackupAPIRequest[T any](t *testing.T, server *Server, method string, target string, body []byte, wantStatus int) T {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, target, rec.Code, wantStatus, rec.Body.String())
	}

	var response struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	var out T
	if response.Success && len(response.Data) > 0 {
		if err := json.Unmarshal(response.Data, &out); err != nil {
			t.Fatalf("decode response data: %v; data=%s", err, string(response.Data))
		}
	}
	return out
}

func mustWriteAPITestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll(%s) error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
}
