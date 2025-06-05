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
