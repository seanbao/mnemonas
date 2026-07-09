package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func secureBackupAPITestTempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod(test temp dir) error: %v", err)
	}
	return dir
}

func newBackupAPITestServer(t testing.TB, logger zerolog.Logger, cfg *ServerConfig) (*Server, error) {
	t.Helper()
	server, err := NewServer(logger, cfg)
	if err == nil {
		t.Cleanup(func() {
			if closeErr := server.Close(); closeErr != nil {
				t.Errorf("Close(test API server) error: %v", closeErr)
			}
		})
	}
	return server, err
}

func (r *backupEventRecorder) UpdateConfig(alerts.Config) {}

func (r *backupEventRecorder) SendEvent(_ context.Context, event alerts.EventPayload) error {
	r.events = append(r.events, event)
	return nil
}

func setupMutableBackupServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	tmpDir := secureBackupAPITestTempDir(t)
	storageRoot := filepath.Join(tmpDir, "storage")
	if err := os.MkdirAll(storageRoot, 0700); err != nil {
		t.Fatalf("MkdirAll(storageRoot) error: %v", err)
	}
	mustWriteAPITestFile(t, filepath.Join(storageRoot, "docs", "note.txt"), "backup source")

	cfg := config.Default()
	cfg.Storage.Root = storageRoot
	cfg.Backup.Jobs = nil
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save(configPath) error: %v", err)
	}
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "backup-state"),
		StorageRoot: storageRoot,
		BackupJobs:  cfg.Backup.Jobs,
		Config:      cfg,
		ConfigPath:  configPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	return server, configPath, storageRoot
}

func disableBackupSchedulerForTest(t *testing.T) {
	t.Helper()
	previous := startBackupScheduler
	startBackupScheduler = func(*backup.Manager) bool { return false }
	t.Cleanup(func() {
		startBackupScheduler = previous
	})
}

func TestServerCreateLocalBackupPersistsAndActivatesJob(t *testing.T) {
	disableBackupSchedulerForTest(t)
	server, configPath, storageRoot := setupMutableBackupServer(t)
	destination := filepath.Join(filepath.Dir(storageRoot), "external-backup")
	body, err := json.Marshal(map[string]any{
		"name":        "External disk",
		"destination": destination,
	})
	if err != nil {
		t.Fatalf("Marshal(request) error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create backup status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var response struct {
		Success bool           `json:"success"`
		Data    backup.JobView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	created := response.Data
	if !response.Success || !strings.HasPrefix(created.ID, "local-") {
		t.Fatalf("create response = %+v", response)
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/maintenance/backups/"+created.ID {
		t.Fatalf("Location = %q", got)
	}
	if created.Type != backup.JobTypeLocal || created.Source != storageRoot || created.Destination != destination {
		t.Fatalf("created job = %+v", created)
	}
	if created.ScheduleInterval != "24h0m0s" || created.MaxSnapshots != 7 || !created.IncludeConfig || !created.VerifyAfterBackup {
		t.Fatalf("created defaults = %+v", created)
	}

	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(configPath) error: %v", err)
	}
	if len(persisted.Backup.Jobs) != 1 {
		t.Fatalf("persisted backup jobs = %+v", persisted.Backup.Jobs)
	}
	persistedJob := persisted.Backup.Jobs[0]
	if persistedJob.ID != created.ID || persistedJob.Source != "" || persistedJob.Destination != destination || persistedJob.ScheduleInterval != 24*time.Hour || persistedJob.MaxSnapshots != 7 || !persistedJob.IncludeConfig || !persistedJob.VerifyAfterBackup {
		t.Fatalf("persisted backup job = %+v", persistedJob)
	}
	if jobs := server.backupManager.ListJobs(); len(jobs) != 1 || jobs[0].ID != created.ID {
		t.Fatalf("running backup jobs = %+v", jobs)
	}

	runResult := runBackupAPIRequest[backup.RunResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/"+created.ID+"/run", nil, http.StatusOK)
	if runResult.Status != backup.StatusCompleted || runResult.FileCount == 0 {
		t.Fatalf("run created backup result = %+v", runResult)
	}
}

func TestServerCreateLocalBackupAllowsManualSchedule(t *testing.T) {
	disableBackupSchedulerForTest(t)
	server, configPath, storageRoot := setupMutableBackupServer(t)
	destination := filepath.Join(filepath.Dir(storageRoot), "manual-backup")
	body, _ := json.Marshal(map[string]any{
		"name":                "Manual backup",
		"destination":         destination,
		"schedule_interval":   "0",
		"max_snapshots":       3,
		"include_config":      false,
		"verify_after_backup": false,
	})

	created := runBackupAPIRequest[backup.JobView](t, server, http.MethodPost, "/api/v1/maintenance/backups", body, http.StatusCreated)
	if created.ScheduleInterval != "" || created.MaxSnapshots != 3 || created.IncludeConfig || created.VerifyAfterBackup {
		t.Fatalf("manual backup job = %+v", created)
	}
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(configPath) error: %v", err)
	}
	if len(persisted.Backup.Jobs) != 1 || persisted.Backup.Jobs[0].ScheduleInterval != 0 {
		t.Fatalf("persisted manual backup job = %+v", persisted.Backup.Jobs)
	}
}

func TestServerCreateLocalBackupRejectsUnsafeOrInvalidRequests(t *testing.T) {
	disableBackupSchedulerForTest(t)
	tests := []struct {
		name      string
		buildBody func(t *testing.T, storageRoot string) []byte
	}{
		{
			name: "relative destination",
			buildBody: func(t *testing.T, _ string) []byte {
				return []byte(`{"name":"Backup","destination":"relative"}`)
			},
		},
		{
			name: "destination inside storage root",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(storageRoot, "backup")})
				return body
			},
		},
		{
			name: "existing file destination",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				destination := filepath.Join(filepath.Dir(storageRoot), "backup-file")
				if err := os.WriteFile(destination, []byte("file"), 0600); err != nil {
					t.Fatalf("WriteFile(destination) error: %v", err)
				}
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": destination})
				return body
			},
		},
		{
			name: "symlink destination component",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				realParent := filepath.Join(filepath.Dir(storageRoot), "real-parent")
				linkParent := filepath.Join(filepath.Dir(storageRoot), "link-parent")
				if err := os.MkdirAll(realParent, 0700); err != nil {
					t.Fatalf("MkdirAll(realParent) error: %v", err)
				}
				if err := os.Symlink(realParent, linkParent); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(linkParent, "backup")})
				return body
			},
		},
		{
			name: "invalid schedule",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(filepath.Dir(storageRoot), "backup"), "schedule_interval": "never"})
				return body
			},
		},
		{
			name: "negative schedule",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(filepath.Dir(storageRoot), "backup"), "schedule_interval": "-1h"})
				return body
			},
		},
		{
			name: "zero retention",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(filepath.Dir(storageRoot), "backup"), "max_snapshots": 0})
				return body
			},
		},
		{
			name: "unknown field",
			buildBody: func(t *testing.T, storageRoot string) []byte {
				body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": filepath.Join(filepath.Dir(storageRoot), "backup"), "type": "restic"})
				return body
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, configPath, storageRoot := setupMutableBackupServer(t)
			body := tt.buildBody(t, storageRoot)
			runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups", body, http.StatusBadRequest)
			persisted, err := config.Load(configPath)
			if err != nil {
				t.Fatalf("Load(configPath) error: %v", err)
			}
			if len(persisted.Backup.Jobs) != 0 || len(server.backupManager.ListJobs()) != 0 {
				t.Fatalf("rejected request changed backup jobs: persisted=%+v running=%+v", persisted.Backup.Jobs, server.backupManager.ListJobs())
			}
		})
	}
}

func TestServerCreateLocalBackupSaveFailureDoesNotActivateJob(t *testing.T) {
	disableBackupSchedulerForTest(t)
	server, configPath, storageRoot := setupMutableBackupServer(t)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Remove(configPath) error: %v", err)
	}
	if err := os.Mkdir(configPath, 0700); err != nil {
		t.Fatalf("Mkdir(configPath) error: %v", err)
	}
	destination := filepath.Join(filepath.Dir(storageRoot), "external-backup")
	body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": destination})

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups", body, http.StatusInternalServerError)
	if jobs := server.backupManager.ListJobs(); len(jobs) != 0 {
		t.Fatalf("running backup jobs = %+v, want empty", jobs)
	}
	if cfg := server.currentConfig(); cfg == nil || len(cfg.Backup.Jobs) != 0 {
		t.Fatalf("running config backup jobs = %+v", cfg)
	}
}

func TestServerCreateLocalBackupEntropyFailureDoesNotPersistJob(t *testing.T) {
	disableBackupSchedulerForTest(t)
	server, configPath, storageRoot := setupMutableBackupServer(t)
	previousRandomRead := readLocalBackupJobRandom
	readLocalBackupJobRandom = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	t.Cleanup(func() {
		readLocalBackupJobRandom = previousRandomRead
	})
	destination := filepath.Join(filepath.Dir(storageRoot), "external-backup")
	body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": destination})

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups", body, http.StatusInternalServerError)
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(configPath) error: %v", err)
	}
	if len(persisted.Backup.Jobs) != 0 || len(server.backupManager.ListJobs()) != 0 {
		t.Fatalf("entropy failure changed backup jobs: persisted=%+v running=%+v", persisted.Backup.Jobs, server.backupManager.ListJobs())
	}
}

func TestServerCreateLocalBackupAddFailureRollsBackConfig(t *testing.T) {
	disableBackupSchedulerForTest(t)
	server, configPath, storageRoot := setupMutableBackupServer(t)
	destination := filepath.Join(filepath.Dir(storageRoot), "external-backup")
	previousHook := beforeAddLocalBackupJob
	beforeAddLocalBackupJob = func(config.BackupJobConfig) {
		if err := os.WriteFile(destination, []byte("replaced by file"), 0600); err != nil {
			t.Fatalf("WriteFile(destination) error: %v", err)
		}
	}
	t.Cleanup(func() {
		beforeAddLocalBackupJob = previousHook
	})
	body, _ := json.Marshal(map[string]any{"name": "Backup", "destination": destination})

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups", body, http.StatusBadRequest)
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(configPath) error: %v", err)
	}
	if len(persisted.Backup.Jobs) != 0 || len(server.backupManager.ListJobs()) != 0 {
		t.Fatalf("failed activation was not rolled back: persisted=%+v running=%+v", persisted.Backup.Jobs, server.backupManager.ListJobs())
	}
	if cfg := server.currentConfig(); cfg == nil || len(cfg.Backup.Jobs) != 0 {
		t.Fatalf("running config backup jobs = %+v", cfg)
	}
}

func TestServer_BackupEndpoints_RunAndRestoreDrill(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")
	mustWriteAPITestFile(t, filepath.Join(source, "docs", "note.txt"), "hello")
	mustWriteAPITestFile(t, configPath, "[server]\nport = 8080\n")

	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
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
	if len(previewResult.PreflightChecks) == 0 || len(previewResult.CutoverChecklist) == 0 || len(previewResult.RollbackChecklist) == 0 {
		t.Fatalf("restore preview missing preflight/checklists: %+v", previewResult)
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
	if len(restoreResult.PreflightChecks) == 0 || len(restoreResult.CutoverChecklist) == 0 || len(restoreResult.RollbackChecklist) == 0 {
		t.Fatalf("restore result missing preflight/checklists: %+v", restoreResult)
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
	if verifyResult.SnapshotPath != restoreResult.SnapshotPath || verifyResult.ManifestPath != restoreResult.ManifestPath {
		t.Fatalf("restore verify reference = (%q, %q), want restore reference (%q, %q)", verifyResult.SnapshotPath, verifyResult.ManifestPath, restoreResult.SnapshotPath, restoreResult.ManifestPath)
	}
	retentionResult := runBackupAPIRequest[backup.RetentionCheckResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/retention-check", nil, http.StatusOK)
	if retentionResult.Status != backup.StatusCompleted || retentionResult.SnapshotCount != 1 || !retentionResult.Warning {
		t.Fatalf("unexpected retention check result: %+v", retentionResult)
	}

	jobView := runBackupAPIRequest[backup.JobView](t, server, http.MethodGet, "/api/v1/maintenance/backups/home", nil, http.StatusOK)
	if jobView.Exclude == nil {
		t.Fatal("backup job exclude array is nil, want empty array")
	}
	if jobView.LastRestore == nil || jobView.LastRestore.ID != restoreResult.ID || jobView.LastRestore.TargetPath != restoreTarget {
		t.Fatalf("backup job missing latest restore record: %+v", jobView.LastRestore)
	}
	if jobView.LastRestoreVerify == nil || jobView.LastRestoreVerify.ID != verifyResult.ID || jobView.LastRestoreVerify.TargetPath != restoreTarget {
		t.Fatalf("backup job missing latest restore verify report: %+v", jobView.LastRestoreVerify)
	}
	if jobView.LastMatchingRestoreVerify == nil || jobView.LastMatchingRestoreVerify.ID != verifyResult.ID {
		t.Fatalf("backup job matching restore verify = %+v, want %s", jobView.LastMatchingRestoreVerify, verifyResult.ID)
	}
	if len(jobView.RestoreDrillHistory) != 1 || jobView.RestoreDrillHistory[0].ID != drillResult.ID {
		t.Fatalf("backup job restore drill history = %+v, want latest drill", jobView.RestoreDrillHistory)
	}
	if jobView.RestoreDrillStats == nil || jobView.RestoreDrillStats.TotalRuns != 1 || jobView.RestoreDrillStats.SuccessfulRuns != 1 {
		t.Fatalf("backup job restore drill stats = %+v, want one successful drill", jobView.RestoreDrillStats)
	}
	if jobView.LastRetentionCheck == nil || jobView.LastRetentionCheck.ID != retentionResult.ID {
		t.Fatalf("backup job missing latest retention check: %+v", jobView.LastRetentionCheck)
	}
	if len(jobView.RestoreHistory) != 1 || jobView.RestoreHistory[0].ID != restoreResult.ID {
		t.Fatalf("backup job restore history = %+v, want latest restore", jobView.RestoreHistory)
	}
	if len(jobView.RestoreReportFindings) == 0 {
		t.Fatalf("backup job restore report findings are empty: %+v", jobView)
	}

	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-verify", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	jobViewAfterBadRequest := runBackupAPIRequest[backup.JobView](t, server, http.MethodGet, "/api/v1/maintenance/backups/home", nil, http.StatusOK)
	if jobViewAfterBadRequest.LastRestore == nil || jobViewAfterBadRequest.LastRestore.ID != restoreResult.ID || jobViewAfterBadRequest.LastRestore.TargetPath != restoreTarget {
		t.Fatalf("invalid restore request overwrote latest restore record: %+v", jobViewAfterBadRequest.LastRestore)
	}
	if jobViewAfterBadRequest.LastRestoreVerify == nil || jobViewAfterBadRequest.LastRestoreVerify.ID != verifyResult.ID || jobViewAfterBadRequest.LastRestoreVerify.TargetPath != restoreTarget {
		t.Fatalf("invalid restore verify request overwrote latest restore verify report: %+v", jobViewAfterBadRequest.LastRestoreVerify)
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/api/v1/maintenance/backups/home/restore-report", nil)
	reportResp := httptest.NewRecorder()
	server.Router().ServeHTTP(reportResp, reportReq)
	if reportResp.Code != http.StatusOK {
		t.Fatalf("restore report status = %d, body = %s", reportResp.Code, reportResp.Body.String())
	}
	if contentDisposition := reportResp.Header().Get("Content-Disposition"); !strings.Contains(contentDisposition, "mnemonas-restore-summary-home-") {
		t.Fatalf("restore report Content-Disposition = %q", contentDisposition)
	}
	if cacheControl := reportResp.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("restore report Cache-Control = %q, want no-store", cacheControl)
	}
	if pragma := reportResp.Header().Get("Pragma"); pragma != "no-cache" {
		t.Fatalf("restore report Pragma = %q, want no-cache", pragma)
	}
	if nosniff := reportResp.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("restore report X-Content-Type-Options = %q, want nosniff", nosniff)
	}
	if referrerPolicy := reportResp.Header().Get("Referrer-Policy"); referrerPolicy != "no-referrer" {
		t.Fatalf("restore report Referrer-Policy = %q, want no-referrer", referrerPolicy)
	}
	var report backup.RestoreReport
	if err := json.NewDecoder(reportResp.Body).Decode(&report); err != nil {
		t.Fatalf("decode restore report: %v", err)
	}
	if report.Job.ID != "home" || report.LastRestore == nil || report.LastRestoreVerify == nil || report.LastMatchingRestoreVerify == nil || len(report.RestoreDrillHistory) != 1 || report.RestoreDrillStats == nil || len(report.Findings) == 0 {
		t.Fatalf("unexpected restore report: %+v", report)
	}
	if strings.Join(report.Job.RestoreReportFindings, "\n") != strings.Join(report.Findings, "\n") {
		t.Fatalf("restore report job findings = %+v, want report findings %+v", report.Job.RestoreReportFindings, report.Findings)
	}
	if report.LastMatchingRestoreVerify.ID != verifyResult.ID {
		t.Fatalf("restore report matching verify = %+v, want %s", report.LastMatchingRestoreVerify, verifyResult.ID)
	}

	batchA := filepath.Join(tmpDir, "batch-a")
	batchB := filepath.Join(tmpDir, "batch-b")
	batchBody := []byte(`{"items":[{"job_id":"home","target_path":` + strconv.Quote(batchA) + `,"include_config":true},{"job_id":"home","target_path":` + strconv.Quote(batchB) + `,"include_config":false}]}`)
	batchPreview := runBackupAPIRequest[backup.BatchRestorePreviewResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore-preview", batchBody, http.StatusOK)
	if batchPreview.Status != backup.StatusCompleted || len(batchPreview.Items) != 2 || batchPreview.TotalFiles == 0 {
		t.Fatalf("unexpected batch restore preview: %+v", batchPreview)
	}
	batchRestore := runBackupAPIRequest[backup.BatchRestoreResult](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore", batchBody, http.StatusOK)
	if batchRestore.Status != backup.StatusCompleted || len(batchRestore.Items) != 2 || batchRestore.VerifiedBytes == 0 {
		t.Fatalf("unexpected batch restore result: %+v", batchRestore)
	}
	if _, err := os.Stat(filepath.Join(batchA, "docs", "note.txt")); err != nil {
		t.Fatalf("batch restored file A stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(batchB, "docs", "note.txt")); err != nil {
		t.Fatalf("batch restored file B stat error: %v", err)
	}
}

func TestServer_BackupEndpoints_RunReturnsWarningContract(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteAPITestFile(t, filepath.Join(source, "docs", "note.txt"), "hello")

	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups/home/run", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("run backup status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Warning"); got != backupRunWarningHeader {
		t.Fatalf("warning header = %q, want %q", got, backupRunWarningHeader)
	}

	var response struct {
		Success bool             `json:"success"`
		Message string           `json:"message"`
		Data    backup.RunResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode run response: %v; body=%s", err, rec.Body.String())
	}
	if !response.Success || response.Message != "backup completed with warnings" {
		t.Fatalf("run response success/message = %t/%q", response.Success, response.Message)
	}
	if response.Data.Status != backup.StatusCompleted || !response.Data.Warning || len(response.Data.Warnings) == 0 {
		t.Fatalf("run response data = %+v, want completed warning result", response.Data)
	}
}

func TestServer_BackupEndpoints_RunWithoutWarningKeepsSuccessContract(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteAPITestFile(t, filepath.Join(source, "docs", "note.txt"), "hello")

	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		BackupJobs: []config.BackupJobConfig{{
			ID:           "home",
			Name:         "Home backup",
			Type:         backup.JobTypeLocal,
			Source:       source,
			Destination:  filepath.Join(tmpDir, "backups"),
			MaxSnapshots: 2,
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups/home/run", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("run backup status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Warning"); got != "" {
		t.Fatalf("warning header = %q, want empty", got)
	}

	var response struct {
		Success bool             `json:"success"`
		Message string           `json:"message"`
		Data    backup.RunResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode run response: %v; body=%s", err, rec.Body.String())
	}
	if !response.Success || response.Message != "backup completed" {
		t.Fatalf("run response success/message = %t/%q", response.Success, response.Message)
	}
	if response.Data.Status != backup.StatusCompleted || response.Data.Warning {
		t.Fatalf("run response data = %+v, want completed result without warning", response.Data)
	}
}

func TestServer_BackupEndpoints_ErrorMapping(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
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
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodGet, "/api/v1/maintenance/backups/missing/restore-report", nil, http.StatusNotFound)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/missing/retention-check", nil, http.StatusNotFound)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore-preview", []byte(`{"items":[]}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-drill", nil, http.StatusConflict)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	backslashTarget := filepath.Join(tmpDir, `restore\windows`)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", []byte(`{"target_path":`+strconv.Quote(backslashTarget)+`}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-verify", []byte(`{"target_path":`+strconv.Quote(backslashTarget)+`}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore", []byte(`{"target_path":`+strconv.Quote(backslashTarget)+`}`), http.StatusBadRequest)
	batchBackslashBody := []byte(`{"items":[{"job_id":"home","target_path":` + strconv.Quote(backslashTarget) + `}]}`)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore-preview", batchBackslashBody, http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore", batchBackslashBody, http.StatusBadRequest)
	dotSegmentTarget := filepath.Join(tmpDir, "restore-parent") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "restore"
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", []byte(`{"target_path":`+strconv.Quote(dotSegmentTarget)+`}`), http.StatusBadRequest)
	batchDotSegmentBody := []byte(`{"items":[{"job_id":"home","target_path":` + strconv.Quote(dotSegmentTarget) + `}]}`)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore-preview", batchDotSegmentBody, http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/batch-restore", batchDotSegmentBody, http.StatusBadRequest)
	missingParentTarget := filepath.Join(tmpDir, "missing-parent", "restore")
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", []byte(`{"target_path":`+strconv.Quote(missingParentTarget)+`}`), http.StatusBadRequest)
	secretMissingParentTarget := filepath.Join(tmpDir, "token=restore-secret", "missing-parent", "restore")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups/home/restore-preview", strings.NewReader(`{"target_path":`+strconv.Quote(secretMissingParentTarget)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("secret restore target status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "restore-secret") || strings.Contains(rec.Body.String(), "token=restore-secret") {
		t.Fatalf("bad request leaked restore target secret: %s", rec.Body.String())
	}
	var secretPathError APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &secretPathError); err != nil {
		t.Fatalf("decode secret restore target error: %v; body=%s", err, rec.Body.String())
	}
	if !strings.Contains(secretPathError.Message, "token=<redacted>") {
		t.Fatalf("bad request message = %q, want redacted restore target token", secretPathError.Message)
	}
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore-verify", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)
	runBackupAPIRequest[json.RawMessage](t, server, http.MethodPost, "/api/v1/maintenance/backups/home/restore", []byte(`{"target_path":"relative"}`), http.StatusBadRequest)

	disabledServer, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
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
	runBackupAPIRequest[json.RawMessage](t, disabledServer, http.MethodPost, "/api/v1/maintenance/backups/disabled/retention-check", nil, http.StatusConflict)
}

func TestServer_BackupEndpoints_RunUnsafeConfiguredPathReturnsInternalDetails(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		BackupJobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        backup.JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(source, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups/home/run", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("run unsafe configured path status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	var response struct {
		Code    string           `json:"code"`
		Details backup.RunResult `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, rec.Body.String())
	}
	if response.Code != ErrCodeInternal {
		t.Fatalf("error code = %q, want %q", response.Code, ErrCodeInternal)
	}
	if response.Details.JobID != "home" || response.Details.Status != backup.StatusFailed {
		t.Fatalf("error details = %+v, want failed home run", response.Details)
	}
	if !strings.Contains(response.Details.ErrorMessage, "destination must not be inside source") {
		t.Fatalf("error details message = %q, want unsafe configured destination", response.Details.ErrorMessage)
	}
}

func TestServer_BackupErrorPrioritizesStatePersistenceFailureOverTargetLockConflict(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	result := backup.RunResult{JobID: "home", Status: backup.StatusFailed}
	rec := httptest.NewRecorder()

	server.writeBackupError(
		rec,
		"run backup job",
		errors.Join(backup.ErrBackupTargetLockHeld, backup.ErrBackupStatePersistence),
		result,
	)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var response struct {
		Code    string           `json:"code"`
		Details backup.RunResult `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != ErrCodeInternal || response.Details.JobID != result.JobID {
		t.Fatalf("response = %+v, want internal error with backup details", response)
	}
}

func TestServer_BackupErrorPrioritizesTargetLockReleaseFailureOverBusinessConflict(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	result := backup.RestorePreviewResult{JobID: "home", Status: backup.StatusFailed}
	rec := httptest.NewRecorder()

	server.writeBackupError(
		rec,
		"preview backup restore",
		errors.Join(backup.ErrNoSnapshots, backup.ErrBackupTargetLockRelease),
		result,
	)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var response struct {
		Code    string                      `json:"code"`
		Message string                      `json:"message"`
		Details backup.RestorePreviewResult `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != ErrCodeInternal || response.Message != "backup operation finalization failed" || response.Details.JobID != result.JobID {
		t.Fatalf("response = %+v, want internal finalization error with backup details", response)
	}
}

func TestServer_BatchRestoreInfrastructureFailureReturnsBatchDetails(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	result := backup.BatchRestoreResult{
		ID:     "batch-restore",
		Status: backup.StatusFailed,
		Items: []backup.BatchRestoreItemResult{{
			JobID:  "home",
			Status: backup.StatusFailed,
		}},
	}
	rec := httptest.NewRecorder()

	server.writeBackupError(
		rec,
		"run batch backup restore",
		errors.Join(backup.ErrNoSnapshots, backup.ErrBackupTargetLockRelease),
		result,
	)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var response struct {
		Code    string                    `json:"code"`
		Details backup.BatchRestoreResult `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != ErrCodeInternal || response.Details.ID != result.ID || len(response.Details.Items) != 1 {
		t.Fatalf("response = %+v, want internal error with batch details", response)
	}
}

func TestServer_BackupEndpoints_ClosedManagerReturnsServiceUnavailable(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
		BackupRoot: filepath.Join(tmpDir, "state"),
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	if err := server.backupManager.Close(); err != nil {
		t.Fatalf("backup manager Close() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/maintenance/backups", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestServer_BackupEndpoints_RedactsExternalCommandSecretsFromErrorLogs(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath := filepath.Join(tmpDir, "failing-restic")
	repository := "rest:https://user:repo-pass@backup.example/repo?token=remote-token&region=us"
	stderr := `failed repository ` + repository + ` with access_key_id=AKIASECRET secret_access_key=secret-access-key --password flag-repo-pass --token flag-remote-token --api-key "quoted token" secret='spaced secret' Authorization: Bearer "bearer secret" X-Auth-Token: header-token X-Api-Key: "header quoted token" {"access_key_id":"json-akia","secret_access_key":"json secret","authorization":"Bearer json bearer"}`
	mustWriteAPITestFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteAPITestFile(t, passwordFile, "secret")
	script := "#!/bin/sh\ncat >&2 <<'EOF'\n" + stderr + "\nEOF\nexit 1\n"
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}

	var logBuffer bytes.Buffer
	server, err := newBackupAPITestServer(t, zerolog.New(&logBuffer), &ServerConfig{
		BackupRoot:  filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		BackupJobs: []config.BackupJobConfig{{
			ID:           "restic-remote",
			Name:         "Restic remote",
			Type:         backup.JobTypeRestic,
			Source:       source,
			Repository:   repository,
			Command:      commandPath,
			PasswordFile: passwordFile,
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/maintenance/backups/restic-remote/run", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("run remote backup status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	assertNoBackupAPITestSecrets(t, rec.Body.String())
	assertNoBackupAPITestSecrets(t, logBuffer.String())
	if !strings.Contains(logBuffer.String(), "<redacted>") {
		t.Fatalf("backup error log = %q, want redacted marker", logBuffer.String())
	}
}

func TestServer_BackupEndpoints_FailureSendsAlertEvent(t *testing.T) {
	tmpDir := secureBackupAPITestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	recorder := &backupEventRecorder{}
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{
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
	if _, ok := event.Details["target_path"]; ok {
		t.Fatalf("backup run alert included empty target_path detail: %+v", event.Details)
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
		TargetPath:          "/restore/mnemonas",
		LastSuccessfulRunAt: &lastSuccess,
		LastRestoreDrillAt:  &lastDrill,
		StaleAfter:          "720h0m0s",
		ReminderCooldown:    "24h0m0s",
		FailureCategory:     backup.FailureCategoryNoSnapshot,
		Timestamp:           time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyBackupEvent() error: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("alert event count = %d, want 1", len(recorder.events))
	}
	if recorder.events[0].Message != "backup restore drill is stale" {
		t.Fatalf("alert message = %q, want backup restore drill is stale", recorder.events[0].Message)
	}
	details := recorder.events[0].Details
	if details["trigger"] != backup.NotificationTriggerReminder || details["status"] != "stale" {
		t.Fatalf("unexpected reminder details: %+v", details)
	}
	for _, key := range []string{"job_name", "source", "destination", "target_path", "snapshot_path", "manifest_path", "warnings", "error_message"} {
		if _, ok := details[key]; ok {
			t.Fatalf("reminder details included sensitive field %q: %+v", key, details)
		}
	}
	if details["location_details_omitted"] != true {
		t.Fatalf("location_details_omitted = %#v, want true", details["location_details_omitted"])
	}
	if details["stale_after"] != "720h0m0s" || details["reminder_cooldown"] != "24h0m0s" {
		t.Fatalf("missing reminder timing details: %+v", details)
	}
	if details["failure_category"] != backup.FailureCategoryNoSnapshot {
		t.Fatalf("failure_category detail = %#v, want %q", details["failure_category"], backup.FailureCategoryNoSnapshot)
	}
	if _, ok := details["last_successful_run_at"].(*time.Time); !ok {
		t.Fatalf("last_successful_run_at detail = %#v, want *time.Time", details["last_successful_run_at"])
	}
	if _, ok := details["last_restore_drill_at"].(*time.Time); !ok {
		t.Fatalf("last_restore_drill_at detail = %#v, want *time.Time", details["last_restore_drill_at"])
	}
	if _, ok := details["snapshot_count"]; ok {
		t.Fatalf("reminder details included zero snapshot_count: %+v", details)
	}
	if _, ok := details["error_message_present"]; ok {
		t.Fatalf("reminder details included empty error marker: %+v", details)
	}
}

func TestBackupAlertNotifier_OmitsSensitiveLocationAndMessageText(t *testing.T) {
	recorder := &backupEventRecorder{}
	notifier := newBackupAlertNotifier(recorder, zerolog.Nop())
	if notifier == nil {
		t.Fatal("newBackupAlertNotifier() returned nil")
	}

	err := notifier.NotifyBackupEvent(context.Background(), backup.NotificationEvent{
		Type:         backup.NotificationTypeRestore,
		Level:        backup.NotificationLevelCritical,
		Message:      "restore failed token=message-secret",
		JobID:        "home",
		JobName:      "Home backup",
		JobType:      backup.JobTypeLocal,
		Status:       backup.StatusFailed,
		Source:       "/srv/source/token=source-secret",
		Destination:  "rest:https://user:repo-pass@backup.example/repo?token=remote-token",
		TargetPath:   "/restore/token=restore-secret",
		ManifestPath: "/state/secret_access_key=manifest-secret/manifest.json",
		Warnings: []string{
			"restore warning token=warning-secret",
			`Authorization: Bearer "warning bearer"`,
		},
		ErrorMessage: `restic failed --password repo-pass X-Auth-Token: header-secret`,
		Timestamp:    time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyBackupEvent() error: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("alert event count = %d, want 1", len(recorder.events))
	}

	event := recorder.events[0]
	if event.Message != "backup restore failed" {
		t.Fatalf("alert message = %q, want backup restore failed", event.Message)
	}
	assertBackupAlertStringNoSecrets(t, event.Message)
	for _, key := range []string{"job_name", "source", "destination", "target_path", "snapshot_path", "manifest_path", "warnings", "error_message"} {
		if _, ok := event.Details[key]; ok {
			t.Fatalf("backup alert details included sensitive field %q: %+v", key, event.Details)
		}
	}
	if event.Details["warning_count"] != 2 || event.Details["error_message_present"] != true || event.Details["location_details_omitted"] != true {
		t.Fatalf("unexpected backup alert summary details: %+v", event.Details)
	}
	detailsJSON, err := json.Marshal(event.Details)
	if err != nil {
		t.Fatalf("marshal backup alert details: %v", err)
	}
	assertBackupAlertStringNoSecrets(t, string(detailsJSON))
	for _, leaked := range []string{"/srv/source", "backup.example", "/restore", "/state/", "restore warning", "restic failed"} {
		if strings.Contains(string(detailsJSON), leaked) {
			t.Fatalf("backup alert details leaked location or message text %q: %s", leaked, detailsJSON)
		}
	}
}

func assertBackupAlertStringNoSecrets(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{
		"message-secret",
		"source-secret",
		"repo-pass",
		"remote-token",
		"restore-secret",
		"manifest-secret",
		"warning-secret",
		"warning bearer",
		"header-secret",
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("backup alert text leaked %q: %q", secret, value)
		}
	}
}

func TestServer_BackupEndpoints_Uninitialized(t *testing.T) {
	server, err := newBackupAPITestServer(t, zerolog.Nop(), &ServerConfig{})
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

func assertNoBackupAPITestSecrets(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"repo-pass", "remote-token", "AKIASECRET", "secret-access-key", "flag-repo-pass", "flag-remote-token", "quoted token", "spaced secret", "bearer secret", "header-token", "header quoted token", "json-akia", "json secret", "json bearer", "user:repo-pass", "token=remote-token", "access_key_id=AKIASECRET", "secret_access_key=secret-access-key"} {
		if strings.Contains(value, secret) {
			t.Fatalf("backup API value leaked %q: %q", secret, value)
		}
	}
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
