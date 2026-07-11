package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/config"
)

type recordingNotifier struct {
	mu     sync.Mutex
	events []NotificationEvent
}

func (n *recordingNotifier) NotifyBackupEvent(_ context.Context, event NotificationEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	return nil
}

func (n *recordingNotifier) Events() []NotificationEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NotificationEvent(nil), n.events...)
}

func assertNoBackupTargetSecrets(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"repo-pass", "remote-token", "AKIASECRET", "secret-access-key", "source-secret", "destination-secret", "restore-secret", "state-secret", "config-secret", "user:repo-pass", "token=remote-token", "access_key_id=AKIASECRET", "secret_access_key=secret-access-key"} {
		if strings.Contains(value, secret) {
			t.Fatalf("backup target value leaked %q: %q", secret, value)
		}
	}
}

func assertBackupNotificationEventOmitsLocationAndMessageText(t *testing.T, event NotificationEvent) {
	t.Helper()
	if event.JobName != "" {
		t.Fatalf("notification event included job name: %+v", event)
	}
	for field, value := range map[string]string{
		"source":        event.Source,
		"destination":   event.Destination,
		"target_path":   event.TargetPath,
		"snapshot_path": event.SnapshotPath,
		"manifest_path": event.ManifestPath,
		"error_message": event.ErrorMessage,
	} {
		if strings.TrimSpace(value) != "" {
			t.Fatalf("notification event included %s: %+v", field, event)
		}
	}
	if len(event.Warnings) > 0 {
		t.Fatalf("notification event included warning text: %+v", event)
	}
	assertNoBackupTargetSecrets(t, event.Message)
}

func TestSanitizeBackupTargetForAPIRedactsURLUserinfoWithAtSign(t *testing.T) {
	raw := "rest:https://user:leak@secret@backup.example/repo?token=remote-token"

	sanitized := sanitizeBackupTargetForAPI(raw)
	for _, secret := range []string{"user:leak", "leak", "secret@backup", "remote-token"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupTargetForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	if !strings.Contains(sanitized, "https://"+redactedBackupSecretValue+"@backup.example/repo") {
		t.Fatalf("sanitizeBackupTargetForAPI() = %q, want redacted userinfo", sanitized)
	}
	if !strings.Contains(sanitized, "token="+redactedBackupSecretValue) {
		t.Fatalf("sanitizeBackupTargetForAPI() = %q, want redacted token", sanitized)
	}
}

func TestSanitizeBackupTargetForAPIPreservesHostWhenQueryContainsAtSign(t *testing.T) {
	raw := "rest:https://user:repo-pass@backup.example?token=alpha@omega"

	sanitized := sanitizeBackupTargetForAPI(raw)
	for _, secret := range []string{"user:repo-pass", "repo-pass", "alpha", "omega"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupTargetForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	if !strings.Contains(sanitized, "https://"+redactedBackupSecretValue+"@backup.example?token="+redactedBackupSecretValue) {
		t.Fatalf("sanitizeBackupTargetForAPI() = %q, want host preserved and token redacted", sanitized)
	}
}

func TestSanitizeBackupTargetForAPIRedactsSensitivePathSegments(t *testing.T) {
	raw := "/restore/token=restore-token/secret_access_key=restore-secret/docs"

	sanitized := sanitizeBackupTargetForAPI(raw)
	for _, secret := range []string{"restore-token", "restore-secret"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupTargetForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	if !strings.Contains(sanitized, "/token="+redactedBackupSecretValue+"/secret_access_key="+redactedBackupSecretValue+"/") {
		t.Fatalf("sanitizeBackupTargetForAPI() = %q, want sensitive path segments redacted", sanitized)
	}
}

func TestSanitizeBackupTargetForAPIRedactsPercentEncodedSensitiveNames(t *testing.T) {
	raw := "rest:https://backup.example/repo?access%5Fkey=AKIASECRET&secret%2Dkey=secret-key&region=us secret%5Faccess%5Fkey=inline-secret"

	sanitized := sanitizeBackupTargetForAPI(raw)
	for _, secret := range []string{"AKIASECRET", "secret-key", "inline-secret"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupTargetForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	for _, want := range []string{
		"access%5Fkey=" + redactedBackupSecretValue,
		"secret%2Dkey=" + redactedBackupSecretValue,
		"region=us",
		"secret%5Faccess%5Fkey=" + redactedBackupSecretValue,
	} {
		if !strings.Contains(sanitized, want) {
			t.Fatalf("sanitizeBackupTargetForAPI() = %q, want %q", sanitized, want)
		}
	}
}

func TestSanitizeBackupMessageForAPIRedactsSensitiveFlagValues(t *testing.T) {
	raw := `restic failed: --password repo-pass --secret-access-key=secret-value --token remote-token --api-key "quoted token" secret='spaced secret' Authorization: Bearer "bearer secret" X-Auth-Token: header-token X-Api-Key: "header quoted token" {"access_key_id":"json-akia","secret_access_key":"json secret","authorization":"Bearer json bearer"}`

	sanitized := sanitizeBackupMessageForAPI(raw)
	for _, secret := range []string{"repo-pass", "secret-value", "remote-token", "quoted token", "spaced secret", "bearer secret", "header-token", "header quoted token", "json-akia", "json secret", "json bearer"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupMessageForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	for _, want := range []string{
		"--password " + redactedBackupSecretValue,
		"--secret-access-key=" + redactedBackupSecretValue,
		"--token " + redactedBackupSecretValue,
		`--api-key "` + redactedBackupSecretValue + `"`,
		"secret='" + redactedBackupSecretValue + "'",
		`Authorization: Bearer "` + redactedBackupSecretValue + `"`,
		"X-Auth-Token: " + redactedBackupSecretValue,
		`X-Api-Key: "` + redactedBackupSecretValue + `"`,
		`"access_key_id":"` + redactedBackupSecretValue + `"`,
		`"secret_access_key":"` + redactedBackupSecretValue + `"`,
		`"authorization":"` + redactedBackupSecretValue + `"`,
	} {
		if !strings.Contains(sanitized, want) {
			t.Fatalf("sanitizeBackupMessageForAPI() = %q, want %q", sanitized, want)
		}
	}
}

func TestSanitizeBackupMessageForAPIRedactsProviderEnvironmentSecrets(t *testing.T) {
	raw := `restic failed: B2_ACCOUNT_KEY=b2-secret RCLONE_CONFIG_REMOTE_PASS=rclone-pass AZURE_ACCOUNT_KEY: azure-secret RCLONE_CONFIG_REMOTE%5FPASS=encoded-pass pass=plain-pass {"B2_ACCOUNT_KEY":"json-b2"} compass=navigation bypass=allowed`

	sanitized := sanitizeBackupMessageForAPI(raw)
	for _, secret := range []string{"b2-secret", "rclone-pass", "azure-secret", "encoded-pass", "plain-pass", "json-b2"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitizeBackupMessageForAPI() = %q, leaked %q", sanitized, secret)
		}
	}
	for _, want := range []string{
		"B2_ACCOUNT_KEY=" + redactedBackupSecretValue,
		"RCLONE_CONFIG_REMOTE_PASS=" + redactedBackupSecretValue,
		"AZURE_ACCOUNT_KEY: " + redactedBackupSecretValue,
		"RCLONE_CONFIG_REMOTE%5FPASS=" + redactedBackupSecretValue,
		"pass=" + redactedBackupSecretValue,
		`"B2_ACCOUNT_KEY":"` + redactedBackupSecretValue + `"`,
		"compass=navigation",
		"bypass=allowed",
	} {
		if !strings.Contains(sanitized, want) {
			t.Fatalf("sanitizeBackupMessageForAPI() = %q, want %q", sanitized, want)
		}
	}
}

func protectedSystemDirectoryForTest(t *testing.T) string {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("protected Unix system directory test requires slash-separated paths")
	}
	return "/etc"
}

func TestNewManagerRejectsSymlinkStateRoot(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	realRoot := filepath.Join(tmpDir, "real-state")
	linkRoot := filepath.Join(tmpDir, "state-link")
	if err := os.Mkdir(realRoot, 0700); err != nil {
		t.Fatalf("Mkdir(realRoot) error: %v", err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("Symlink(stateRoot) error: %v", err)
	}

	_, err := newBackupTestManager(t, ManagerConfig{
		Root:        linkRoot,
		StorageRoot: filepath.Join(tmpDir, "source"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
}

func TestNewManagerRejectsSymlinkStateRootParent(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	realParent := filepath.Join(tmpDir, "real-parent")
	linkParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Mkdir(realParent, 0700); err != nil {
		t.Fatalf("Mkdir(realParent) error: %v", err)
	}
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Fatalf("Symlink(linkParent) error: %v", err)
	}

	_, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(linkParent, "state"),
		StorageRoot: filepath.Join(tmpDir, "source"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "state")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root created through symlink parent, stat error = %v", statErr)
	}
}

func TestNewManagerRejectsSymlinkStateFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	outsideState := filepath.Join(tmpDir, "outside-status.json")
	if err := os.Mkdir(stateRoot, 0700); err != nil {
		t.Fatalf("Mkdir(stateRoot) error: %v", err)
	}
	if err := os.WriteFile(outsideState, []byte(`{"jobs":{}}`), 0600); err != nil {
		t.Fatalf("WriteFile(outsideState) error: %v", err)
	}
	if err := os.Symlink(outsideState, filepath.Join(stateRoot, stateFileName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: filepath.Join(tmpDir, "source"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
}

func TestNewManagerRejectsUnsafeJobID(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatalf("Mkdir(destination) error: %v", err)
	}

	for i, id := range []string{"../escape", "nested/job", ".", strings.Repeat("a", 65)} {
		t.Run(id, func(t *testing.T) {
			_, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state-"+strconv.Itoa(i)),
				StorageRoot: source,
				Jobs: []config.BackupJobConfig{{
					ID:          id,
					Name:        "Unsafe",
					Type:        JobTypeLocal,
					Source:      source,
					Destination: destination,
				}},
			})
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
			}
		})
	}
}

func TestNewManagerRejectsDuplicateJobID(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatalf("Mkdir(destination) error: %v", err)
	}

	_, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{
			{
				ID:          "home",
				Name:        "Home",
				Type:        JobTypeLocal,
				Source:      source,
				Destination: destination,
			},
			{
				ID:          "HOME",
				Name:        "Duplicate",
				Type:        JobTypeLocal,
				Source:      source,
				Destination: filepath.Join(tmpDir, "other-backups"),
			},
		},
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NewManager() error = %v, want %v", err, ErrUnsafePath)
	}
}

func TestManagerAddJobMakesLocalJobRunnableWithoutRestart(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	configPath := filepath.Join(tmpDir, "config.toml")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "hot added")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		ConfigPath:  configPath,
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	job := config.BackupJobConfig{
		ID:                "external-disk",
		Name:              "External disk",
		Type:              JobTypeLocal,
		Destination:       destination,
		ScheduleInterval:  24 * time.Hour,
		MaxSnapshots:      7,
		IncludeConfig:     true,
		VerifyAfterBackup: true,
	}
	if err := manager.ValidateNewJob(job); err != nil {
		t.Fatalf("ValidateNewJob() error: %v", err)
	}
	view, err := manager.AddJob(job)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	if view.ID != job.ID || view.Source != source || view.Destination != destination {
		t.Fatalf("AddJob() view = %+v", view)
	}

	result, err := manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted || result.FileCount != 2 {
		t.Fatalf("RunJob() result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(result.SnapshotPath, "data", "docs", "note.txt")); err != nil {
		t.Fatalf("backup snapshot stat error: %v", err)
	}
}

func TestManagerAddJobRejectsCaseInsensitiveDuplicate(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "External-Disk",
			Name:        "External disk",
			Type:        JobTypeLocal,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	duplicate := config.BackupJobConfig{
		ID:          "external-disk",
		Name:        "Duplicate",
		Type:        JobTypeLocal,
		Destination: filepath.Join(tmpDir, "other-backups"),
	}
	if err := manager.ValidateNewJob(duplicate); !errors.Is(err, ErrJobAlreadyExists) {
		t.Fatalf("ValidateNewJob() error = %v, want %v", err, ErrJobAlreadyExists)
	}
	if _, err := manager.AddJob(duplicate); !errors.Is(err, ErrJobAlreadyExists) {
		t.Fatalf("AddJob() error = %v, want %v", err, ErrJobAlreadyExists)
	}
	if jobs := manager.ListJobs(); len(jobs) != 1 || jobs[0].ID != "External-Disk" {
		t.Fatalf("ListJobs() = %+v", jobs)
	}
}

func TestManagerValidateNewJobRejectsExistingFileDestination(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backup-file")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.WriteFile(destination, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile(destination) error: %v", err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	err = manager.ValidateNewJob(config.BackupJobConfig{
		ID:          "external-disk",
		Name:        "External disk",
		Type:        JobTypeLocal,
		Destination: destination,
	})
	if !errors.Is(err, ErrUnsafePath) || !strings.Contains(err.Error(), "destination must be a directory") {
		t.Fatalf("ValidateNewJob() error = %v, want unsafe directory error", err)
	}
}

func TestWriteJSONFileIgnoresPredictableTempSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, "state", stateFileName)
	outsideState := filepath.Join(tmpDir, "outside-status.json")
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		t.Fatalf("MkdirAll(state) error: %v", err)
	}
	if err := os.WriteFile(outsideState, []byte("original"), 0600); err != nil {
		t.Fatalf("WriteFile(outsideState) error: %v", err)
	}
	if err := os.Symlink(outsideState, filePath+".tmp"); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0600)
	if err != nil {
		t.Fatalf("writeJSONFile() error: %v", err)
	}
	assertFileContent(t, outsideState, "original")
	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("ReadFile(state) error: %v", readErr)
	}
	if !strings.Contains(string(data), `"status": "updated"`) {
		t.Fatalf("state file = %q, want updated JSON", data)
	}
	if info, statErr := os.Lstat(filePath + ".tmp"); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("predictable temp symlink was changed: info=%v error=%v", info, statErr)
	}
}

func TestWriteJSONFileRejectsFinalSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, "state", stateFileName)
	outsideState := filepath.Join(tmpDir, "outside-status.json")
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		t.Fatalf("MkdirAll(state) error: %v", err)
	}
	if err := os.WriteFile(outsideState, []byte("original"), 0600); err != nil {
		t.Fatalf("WriteFile(outsideState) error: %v", err)
	}
	if err := os.Symlink(outsideState, filePath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0600)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("writeJSONFile() error = %v, want %v", err, ErrUnsafePath)
	}
	assertFileContent(t, outsideState, "original")
	if _, statErr := os.Lstat(filePath + ".tmp"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temp state file stat error = %v, want not exist", statErr)
	}
}

func TestWriteJSONFileReturnsParentDirectorySyncFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, "state", stateFileName)
	originalSyncBackupJSONDir := syncBackupJSONDir
	t.Cleanup(func() { syncBackupJSONDir = originalSyncBackupJSONDir })
	syncErr := errors.New("injected directory sync failure")
	var syncedDir string
	syncBackupJSONDir = func(_ *os.File, dir string) error {
		syncedDir = dir
		return syncErr
	}

	err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0o600)
	if !errors.Is(err, syncErr) {
		t.Fatalf("writeJSONFile() error = %v, want %v", err, syncErr)
	}
	if !isBackupPersistenceWarning(err) {
		t.Fatalf("writeJSONFile() error = %v, want post-replace persistence warning", err)
	}
	if syncedDir != filepath.Dir(filePath) {
		t.Fatalf("synced directory = %q, want %q", syncedDir, filepath.Dir(filePath))
	}
	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("ReadFile(replaced state) error: %v", readErr)
	}
	if !strings.Contains(string(data), `"status": "updated"`) {
		t.Fatalf("replaced state content = %q, want updated JSON", data)
	}
	if _, statErr := os.Lstat(filePath + ".tmp"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temp state file stat error = %v, want not exist", statErr)
	}
}

func TestWriteJSONFileReconcilesCommittedRenameError(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, "state", stateFileName)
	originalRename := renameBackupJSONFile
	t.Cleanup(func() { renameBackupJSONFile = originalRename })
	renameErr := errors.New("injected ambiguous backup json rename error")
	renameBackupJSONFile = func(root *os.Root, oldName, newName string) error {
		if err := originalRename(root, oldName, newName); err != nil {
			return err
		}
		return renameErr
	}

	if err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0o600); err != nil {
		t.Fatalf("writeJSONFile() error after committed rename: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error: %v", err)
	}
	if !strings.Contains(string(data), `"status": "updated"`) {
		t.Fatalf("state file = %q, want updated JSON", data)
	}
}

func TestManagerStateKeepsCommittedCandidateAfterPostRenameIdentityWarning(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	movedStateRoot := filepath.Join(tmpDir, "state-moved")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalAfterRename := afterRenameBackupJSONFile
	t.Cleanup(func() { afterRenameBackupJSONFile = originalAfterRename })
	afterRenameBackupJSONFile = func(filePath string) {
		if filePath != manager.statePath() {
			return
		}
		if err := os.Rename(stateRoot, movedStateRoot); err != nil {
			t.Fatalf("Rename(state root) error: %v", err)
		}
		if err := os.Mkdir(stateRoot, 0o700); err != nil {
			t.Fatalf("Mkdir(replacement state root) error: %v", err)
		}
	}

	result := &RunResult{
		ID:        "committed-run",
		JobID:     "home",
		Status:    StatusRunning,
		StartedAt: time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC),
	}
	err = manager.updateLastRun(result)
	if !isBackupPersistenceWarning(err) || !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrBackupStateNamespaceChanged) {
		t.Fatalf("updateLastRun() error = %v, want post-replace namespace warning", err)
	}
	manager.mu.Lock()
	lastRun := cloneRunResultRaw(manager.state.Jobs["home"].LastRun)
	healthy := manager.statePersistenceHealthy
	manager.mu.Unlock()
	if lastRun == nil || lastRun.ID != result.ID || lastRun.Status != StatusRunning {
		t.Fatalf("in-memory LastRun = %+v, want committed candidate", lastRun)
	}
	if healthy {
		t.Fatal("state persistence health = true, want false after identity warning")
	}
	if manager.Available() {
		t.Fatal("Available() = true after state namespace changed")
	}
	data, readErr := os.ReadFile(filepath.Join(movedStateRoot, stateFileName))
	if readErr != nil {
		t.Fatalf("ReadFile(committed state) error: %v", readErr)
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(committed state) error: %v", err)
	}
	if persisted.Jobs["home"].LastRun == nil || persisted.Jobs["home"].LastRun.ID != result.ID {
		t.Fatalf("persisted LastRun = %+v, want committed candidate", persisted.Jobs["home"].LastRun)
	}

	next := cloneRunResultRaw(result)
	next.ID = "must-not-write"
	if err := manager.updateLastRun(next); !errors.Is(err, ErrBackupStateNamespaceChanged) || isBackupPersistenceWarning(err) {
		t.Fatalf("second updateLastRun() error = %v, want non-warning %v", err, ErrBackupStateNamespaceChanged)
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, stateFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement state root status error = %v, want not exist", err)
	}

	replacementManager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager(replacement state root) error: %v", err)
	}
	if !replacementManager.Available() {
		t.Fatal("replacement manager is unavailable")
	}
}

func TestManagerStateRejectsReplacementRootBetweenWrites(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	movedStateRoot := filepath.Join(tmpDir, "state-moved")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	first := &RunResult{
		ID:        "first-run",
		JobID:     "home",
		Status:    StatusRunning,
		StartedAt: time.Date(2026, 7, 15, 6, 30, 0, 0, time.UTC),
	}
	if err := manager.updateLastRun(first); err != nil {
		t.Fatalf("first updateLastRun() error: %v", err)
	}
	if err := os.Rename(stateRoot, movedStateRoot); err != nil {
		t.Fatalf("Rename(state root) error: %v", err)
	}
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement state root) error: %v", err)
	}
	if manager.Available() {
		t.Fatal("Available() = true after state root replacement")
	}

	second := cloneRunResultRaw(first)
	second.ID = "must-not-write"
	err = manager.updateLastRun(second)
	if !errors.Is(err, ErrBackupStateNamespaceChanged) || !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("second updateLastRun() error = %v, want namespace replacement failure", err)
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, stateFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement state root status error = %v, want not exist", err)
	}

	data, err := os.ReadFile(filepath.Join(movedStateRoot, stateFileName))
	if err != nil {
		t.Fatalf("ReadFile(original state) error: %v", err)
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(original state) error: %v", err)
	}
	if persisted.Jobs["home"].LastRun == nil || persisted.Jobs["home"].LastRun.ID != first.ID {
		t.Fatalf("persisted LastRun = %+v, want first committed state", persisted.Jobs["home"].LastRun)
	}

	replacementManager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager(replacement state root) error: %v", err)
	}
	t.Cleanup(func() { _ = replacementManager.Close() })
	if !replacementManager.Available() {
		t.Fatal("replacement manager is unavailable")
	}
}

func TestManagerStateWriterNeverFollowsReplacementRoot(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	movedStateRoot := filepath.Join(tmpDir, "state-moved")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	replaced := false
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		if !replaced {
			replaced = true
			if err := os.Rename(stateRoot, movedStateRoot); err != nil {
				t.Fatalf("Rename(state root) error: %v", err)
			}
			if err := os.Mkdir(stateRoot, 0o700); err != nil {
				t.Fatalf("Mkdir(replacement state root) error: %v", err)
			}
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	result := &RunResult{
		ID:        "must-not-commit",
		JobID:     "home",
		Status:    StatusRunning,
		StartedAt: time.Date(2026, 7, 15, 6, 40, 0, 0, time.UTC),
	}
	err = manager.updateLastRun(result)
	if !errors.Is(err, ErrBackupStateNamespaceChanged) || isBackupPersistenceWarning(err) {
		t.Fatalf("updateLastRun() error = %v, want non-warning namespace failure", err)
	}
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatalf("ReadDir(replacement state root) error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("replacement state root entries = %+v, want no status or temp files", entries)
	}
	movedEntries, err := os.ReadDir(movedStateRoot)
	if err != nil {
		t.Fatalf("ReadDir(original state root) error: %v", err)
	}
	for _, entry := range movedEntries {
		if entry.Name() == stateFileName || strings.HasPrefix(entry.Name(), "."+stateFileName+".") {
			t.Fatalf("original locked state root retained uncommitted file %q", entry.Name())
		}
	}
}

func TestManagerRunJobSurfacesStateNamespaceChangeAsRestartWarning(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	movedStateRoot := filepath.Join(tmpDir, "state-moved")
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalAfterRename := afterRenameBackupJSONFile
	t.Cleanup(func() { afterRenameBackupJSONFile = originalAfterRename })
	stateWriteCount := 0
	afterRenameBackupJSONFile = func(filePath string) {
		if filePath != manager.statePath() {
			return
		}
		stateWriteCount++
		if stateWriteCount != 2 {
			return
		}
		if err := os.Rename(stateRoot, movedStateRoot); err != nil {
			t.Fatalf("Rename(state root) error: %v", err)
		}
		if err := os.Mkdir(stateRoot, 0o700); err != nil {
			t.Fatalf("Mkdir(replacement state root) error: %v", err)
		}
	}

	result, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error = %v, want completed result with restart warning", err)
	}
	if result == nil || result.Status != StatusCompleted || !result.Warning {
		t.Fatalf("RunJob() result = %+v, want completed warning", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != stateNamespaceChangedWarning {
		t.Fatalf("RunJob() warnings = %#v, want explicit restart warning", result.Warnings)
	}
	if manager.Available() {
		t.Fatal("Available() = true after state namespace changed")
	}
	if info, err := os.Stat(result.SnapshotPath); err != nil || !info.IsDir() {
		t.Fatalf("completed snapshot status = %v, error = %v", info, err)
	}
}

func TestManagerStateQuarantinesMissingRootAfterCommittedWrite(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	stateRoot := filepath.Join(tmpDir, "state")
	movedStateRoot := filepath.Join(tmpDir, "state-moved")
	manager, err := newBackupTestManager(t, ManagerConfig{Root: stateRoot})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalAfterRename := afterRenameBackupJSONFile
	t.Cleanup(func() { afterRenameBackupJSONFile = originalAfterRename })
	afterRenameBackupJSONFile = func(filePath string) {
		if filePath != manager.statePath() {
			return
		}
		if err := os.Rename(stateRoot, movedStateRoot); err != nil {
			t.Fatalf("Rename(state root) error: %v", err)
		}
	}

	result := &RunResult{
		ID:        "committed-run",
		JobID:     "home",
		Status:    StatusRunning,
		StartedAt: time.Date(2026, 7, 15, 6, 45, 0, 0, time.UTC),
	}
	err = manager.updateLastRun(result)
	if !isBackupPersistenceWarning(err) || !errors.Is(err, ErrBackupStateNamespaceChanged) {
		t.Fatalf("updateLastRun() error = %v, want committed namespace warning", err)
	}
	if manager.Available() {
		t.Fatal("Available() = true after state root disappeared")
	}
	data, err := os.ReadFile(filepath.Join(movedStateRoot, stateFileName))
	if err != nil {
		t.Fatalf("ReadFile(committed state) error: %v", err)
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(committed state) error: %v", err)
	}
	if persisted.Jobs["home"].LastRun == nil || persisted.Jobs["home"].LastRun.ID != result.ID {
		t.Fatalf("persisted LastRun = %+v, want committed candidate", persisted.Jobs["home"].LastRun)
	}
}

func TestWriteJSONFileRetriesUniqueTempNameCollision(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, stateFileName)
	collisionName := "." + stateFileName + "." + strings.Repeat("00", 16) + ".tmp"
	collisionPath := filepath.Join(tmpDir, collisionName)
	if err := os.WriteFile(collisionPath, []byte("collision"), 0o600); err != nil {
		t.Fatalf("WriteFile(collision) error: %v", err)
	}

	originalRandomRead := backupJSONRandomRead
	t.Cleanup(func() { backupJSONRandomRead = originalRandomRead })
	calls := 0
	backupJSONRandomRead = func(buffer []byte) (int, error) {
		calls++
		for index := range buffer {
			buffer[index] = byte(calls - 1)
		}
		return len(buffer), nil
	}
	if err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0o600); err != nil {
		t.Fatalf("writeJSONFile() error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("random read calls = %d, want 2", calls)
	}
	assertFileContent(t, collisionPath, "collision")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error: %v", err)
	}
	if !strings.Contains(string(data), `"status": "updated"`) {
		t.Fatalf("state file = %q, want updated JSON", data)
	}
}

func TestWriteJSONFileRandomFailurePreservesExistingState(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, stateFileName)
	if err := os.WriteFile(filePath, []byte("previous"), 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error: %v", err)
	}

	originalRandomRead := backupJSONRandomRead
	t.Cleanup(func() { backupJSONRandomRead = originalRandomRead })
	randomErr := errors.New("injected random source failure")
	backupJSONRandomRead = func([]byte) (int, error) { return 0, randomErr }
	err := writeJSONFile(filePath, map[string]string{"status": "updated"}, 0o600)
	if !errors.Is(err, randomErr) {
		t.Fatalf("writeJSONFile() error = %v, want %v", err, randomErr)
	}
	assertFileContent(t, filePath, "previous")
	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	if len(entries) != 1 || entries[0].Name() != stateFileName {
		t.Fatalf("state directory entries = %#v, want only %s", entries, stateFileName)
	}
}

func TestWriteJSONFileConcurrentWritersLeaveCompleteDocument(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	filePath := filepath.Join(tmpDir, stateFileName)
	type document struct {
		Writer int    `json:"writer"`
		Value  string `json:"value"`
	}
	const writerCount = 16
	start := make(chan struct{})
	errorsByWriter := make(chan error, writerCount)
	for writer := range writerCount {
		go func() {
			<-start
			errorsByWriter <- writeJSONFile(filePath, document{Writer: writer, Value: strings.Repeat("x", 2048)}, 0o600)
		}()
	}
	close(start)
	for range writerCount {
		if err := <-errorsByWriter; err != nil {
			t.Fatalf("concurrent writeJSONFile() error: %v", err)
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error: %v", err)
	}
	var persisted document
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal(state) error: %v; data=%q", err, data)
	}
	if persisted.Writer < 0 || persisted.Writer >= writerCount || persisted.Value != strings.Repeat("x", 2048) {
		t.Fatalf("persisted document = %+v, want one complete writer payload", persisted)
	}
}

func TestJobConfigEvidenceBindingPersistsButIsOmittedFromAPIResults(t *testing.T) {
	binding := strings.Repeat("a", 64)
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	state := persistedState{Jobs: map[string]JobState{
		"job": {
			LastRun:          &RunResult{JobConfigBinding: binding, ManifestSize: 1234, ManifestDigest: manifestDigest},
			LastRestoreDrill: &RestoreDrillResult{JobConfigBinding: binding},
			LastRestore:      &RestoreResult{JobConfigBinding: binding},
			LastRestoreVerify: &RestoreVerifyResult{
				JobConfigBinding: binding,
			},
		},
	}}
	persistedPayload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal(persisted state) error: %v", err)
	}
	if got := strings.Count(string(persistedPayload), `"job_config_binding"`); got != 4 {
		t.Fatalf("persisted binding count = %d, want 4; payload=%s", got, persistedPayload)
	}
	if !strings.Contains(string(persistedPayload), `"manifest_size":1234`) {
		t.Fatalf("persisted payload omitted manifest size: %s", persistedPayload)
	}
	if !strings.Contains(string(persistedPayload), `"manifest_digest":"`+manifestDigest+`"`) {
		t.Fatalf("persisted payload omitted manifest digest: %s", persistedPayload)
	}

	apiPayload, err := json.Marshal(struct {
		Run     *RunResult           `json:"run"`
		Drill   *RestoreDrillResult  `json:"drill"`
		Restore *RestoreResult       `json:"restore"`
		Verify  *RestoreVerifyResult `json:"verify"`
	}{
		Run:     cloneRunResult(state.Jobs["job"].LastRun),
		Drill:   cloneRestoreDrillResult(state.Jobs["job"].LastRestoreDrill),
		Restore: cloneRestoreResult(state.Jobs["job"].LastRestore),
		Verify:  cloneRestoreVerifyResult(state.Jobs["job"].LastRestoreVerify),
	})
	if err != nil {
		t.Fatalf("Marshal(API results) error: %v", err)
	}
	if strings.Contains(string(apiPayload), "job_config_binding") || strings.Contains(string(apiPayload), "manifest_size") ||
		strings.Contains(string(apiPayload), "manifest_digest") || strings.Contains(string(apiPayload), binding) ||
		strings.Contains(string(apiPayload), manifestDigest) {
		t.Fatalf("API payload exposed job config binding: %s", apiPayload)
	}
}

func TestReadManifestRejectsUnsafeEntries(t *testing.T) {
	validDigest := strings.Repeat("a", 64)

	tests := []struct {
		name  string
		entry ManifestEntry
	}{
		{
			name: "unsafe archive path",
			entry: ManifestEntry{
				ArchivePath: "data/../escape.txt",
				Size:        1,
				SHA256:      validDigest,
			},
		},
		{
			name: "control character archive path",
			entry: ManifestEntry{
				ArchivePath: "data/report\n2026.txt",
				Size:        1,
				SHA256:      validDigest,
			},
		},
		{
			name: "backslash archive path",
			entry: ManifestEntry{
				ArchivePath: `data\report.txt`,
				Size:        1,
				SHA256:      validDigest,
			},
		},
		{
			name: "unicode control character archive path",
			entry: ManifestEntry{
				ArchivePath: "data/report\u00812026.txt",
				Size:        1,
				SHA256:      validDigest,
			},
		},
		{
			name: "negative size",
			entry: ManifestEntry{
				ArchivePath: "data/note.txt",
				Size:        -1,
				SHA256:      validDigest,
			},
		},
		{
			name: "unsupported archive path",
			entry: ManifestEntry{
				ArchivePath: "metadata/checksum.txt",
				Size:        1,
				SHA256:      validDigest,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
			manifest := Manifest{
				Version:     manifestVersion,
				FileCount:   1,
				TotalBytes:  1,
				Directories: testManifestDirectories(),
				Entries: []ManifestEntry{
					tt.entry,
				},
			}
			if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
				t.Fatalf("writeJSONFile(manifest) error: %v", err)
			}

			_, err := readManifest(manifestPath)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestReadManifestRejectsDuplicateArchivePaths(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   2,
		TotalBytes:  2,
		Directories: testManifestDirectories(),
		Entries: []ManifestEntry{
			{
				ArchivePath: "data/note.txt",
				Size:        1,
				SHA256:      validDigest,
			},
			{
				ArchivePath: "data/note.txt",
				Size:        1,
				SHA256:      validDigest,
			},
		},
	}
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	_, err := readManifest(manifestPath)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestAddManifestEntrySizeRejectsOverflow(t *testing.T) {
	const maxInt64 = int64(1<<63 - 1)

	got, err := addManifestEntrySize(maxInt64-1, 1)
	if err != nil {
		t.Fatalf("addManifestEntrySize() error: %v", err)
	}
	if got != maxInt64 {
		t.Fatalf("addManifestEntrySize() = %d, want %d", got, maxInt64)
	}

	_, err = addManifestEntrySize(maxInt64, 1)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("addManifestEntrySize() error = %v, want ErrUnsafePath", err)
	}
}

func TestReadManifestRejectsInconsistentSummary(t *testing.T) {
	const maxInt64 = int64(1<<63 - 1)
	validDigest := strings.Repeat("a", 64)

	baseEntries := []ManifestEntry{
		{
			ArchivePath: "config/config.toml",
			Size:        3,
			SHA256:      validDigest,
		},
		{
			ArchivePath: "data/note.txt",
			Size:        2,
			SHA256:      validDigest,
		},
	}
	baseDirectories := testManifestDirectories()
	tests := []struct {
		name     string
		manifest Manifest
	}{
		{
			name: "negative file count",
			manifest: Manifest{
				Version:     manifestVersion,
				FileCount:   -1,
				TotalBytes:  5,
				Entries:     baseEntries,
				Directories: baseDirectories,
			},
		},
		{
			name: "negative total bytes",
			manifest: Manifest{
				Version:     manifestVersion,
				FileCount:   2,
				TotalBytes:  -1,
				Entries:     baseEntries,
				Directories: baseDirectories,
			},
		},
		{
			name: "file count mismatch",
			manifest: Manifest{
				Version:     manifestVersion,
				FileCount:   1,
				TotalBytes:  5,
				Entries:     baseEntries,
				Directories: baseDirectories,
			},
		},
		{
			name: "total bytes mismatch",
			manifest: Manifest{
				Version:     manifestVersion,
				FileCount:   2,
				TotalBytes:  4,
				Entries:     baseEntries,
				Directories: baseDirectories,
			},
		},
		{
			name: "total bytes overflow",
			manifest: Manifest{
				Version:     manifestVersion,
				FileCount:   2,
				TotalBytes:  maxInt64,
				Directories: baseDirectories,
				Entries: []ManifestEntry{
					{
						ArchivePath: "data/a.bin",
						Size:        maxInt64,
						SHA256:      validDigest,
					},
					{
						ArchivePath: "data/b.bin",
						Size:        1,
						SHA256:      validDigest,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
			if err := writeJSONFile(manifestPath, tt.manifest, 0600); err != nil {
				t.Fatalf("writeJSONFile(manifest) error: %v", err)
			}

			_, err := readManifest(manifestPath)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestReadManifestRejectsInvalidSHA256(t *testing.T) {
	tests := []struct {
		name   string
		digest string
	}{
		{name: "empty", digest: ""},
		{name: "short", digest: strings.Repeat("a", 63)},
		{name: "non hex", digest: strings.Repeat("a", 63) + "g"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
			manifest := Manifest{
				Version:     manifestVersion,
				FileCount:   1,
				TotalBytes:  1,
				Directories: testManifestDirectories(),
				Entries: []ManifestEntry{
					{
						ArchivePath: "data/note.txt",
						Size:        1,
						SHA256:      tt.digest,
					},
				},
			}
			if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
				t.Fatalf("writeJSONFile(manifest) error: %v", err)
			}

			_, err := readManifest(manifestPath)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestReadManifestRejectsInvalidMode(t *testing.T) {
	manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   1,
		TotalBytes:  1,
		Directories: testManifestDirectories(),
		Entries: []ManifestEntry{
			{
				ArchivePath: "data/note.txt",
				Size:        1,
				Mode:        01000,
				SHA256:      strings.Repeat("a", 64),
			},
		},
	}
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	_, err := readManifest(manifestPath)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestReadManifestRejectsDirectory(t *testing.T) {
	manifestPath := filepath.Join(secureBackupTestTempDir(t), "manifest.json")
	if err := os.Mkdir(manifestPath, 0700); err != nil {
		t.Fatalf("Mkdir(manifestPath) error: %v", err)
	}

	_, err := readManifest(manifestPath)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("readManifest() error = %v, want ErrUnsafePath", err)
	}
}

func TestVerifyManifestFilesRejectsInconsistentSummary(t *testing.T) {
	root := secureBackupTestTempDir(t)
	filePath := filepath.Join(root, "data", "note.txt")
	mustWriteFile(t, filePath, "verified")

	size, digest, mode, err := hashFile(context.Background(), filePath)
	if err != nil {
		t.Fatalf("hashFile() error: %v", err)
	}
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   0,
		TotalBytes:  size,
		Directories: testManifestDirectories(),
		Entries: []ManifestEntry{
			{
				ArchivePath: "data/note.txt",
				SourcePath:  "note.txt",
				Size:        size,
				Mode:        uint32(mode),
				SHA256:      digest,
			},
		},
	}

	_, _, err = verifyManifestFiles(context.Background(), root, manifest)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
	}
}

func TestVerifyManifestFilesRejectsUnmanifestedFiles(t *testing.T) {
	root := secureBackupTestTempDir(t)
	filePath := filepath.Join(root, "data", "note.txt")
	mustWriteFile(t, filePath, "verified")
	mustWriteFile(t, filepath.Join(root, "data", "extra.txt"), "unexpected")

	size, digest, mode, err := hashFile(context.Background(), filePath)
	if err != nil {
		t.Fatalf("hashFile() error: %v", err)
	}
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   1,
		TotalBytes:  size,
		Directories: testManifestDirectories(),
		Entries: []ManifestEntry{
			{
				ArchivePath: "data/note.txt",
				SourcePath:  "note.txt",
				Size:        size,
				Mode:        uint32(mode),
				SHA256:      digest,
			},
		},
	}
	if err := writeJSONFile(filepath.Join(root, manifestFileName), manifest, 0o600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	_, _, err = verifyManifestFiles(context.Background(), root, manifest)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
	}
}

func TestVerifyManifestFilesRejectsUnexpectedTopLevelDirectory(t *testing.T) {
	root := secureBackupTestTempDir(t)
	filePath := filepath.Join(root, "data", "note.txt")
	mustWriteFile(t, filePath, "verified")
	if err := os.Mkdir(filepath.Join(root, "unexpected"), 0700); err != nil {
		t.Fatalf("Mkdir(unexpected) error: %v", err)
	}

	size, digest, mode, err := hashFile(context.Background(), filePath)
	if err != nil {
		t.Fatalf("hashFile() error: %v", err)
	}
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   1,
		TotalBytes:  size,
		Directories: testManifestDirectories(),
		Entries: []ManifestEntry{
			{
				ArchivePath: "data/note.txt",
				SourcePath:  "note.txt",
				Size:        size,
				Mode:        uint32(mode),
				SHA256:      digest,
			},
		},
	}
	if err := writeJSONFile(filepath.Join(root, manifestFileName), manifest, 0o600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	_, _, err = verifyManifestFiles(context.Background(), root, manifest)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
	}
}

func TestVerifyManifestFilesRejectsMissingDataDirectory(t *testing.T) {
	root := secureBackupTestTempDir(t)
	manifest := Manifest{
		Version:     manifestVersion,
		FileCount:   0,
		TotalBytes:  0,
		Directories: testManifestDirectories(),
	}
	if err := writeJSONFile(filepath.Join(root, manifestFileName), manifest, 0o600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	_, _, err := verifyManifestFiles(context.Background(), root, manifest)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("verifyManifestFiles() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_JobViewUsesEmptyExcludeArray(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	jobs := manager.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("ListJobs() length = %d, want 1", len(jobs))
	}
	if jobs[0].Exclude == nil {
		t.Fatal("ListJobs()[0].Exclude is nil, want empty array")
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.Exclude == nil {
		t.Fatal("GetJob().Exclude is nil, want empty array")
	}
}

func TestManager_JobViewRedactsRemoteTargetSecrets(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	mustWriteFile(t, passwordFile, "secret")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{
			{
				ID:           "restic-remote",
				Name:         "Restic remote",
				Type:         JobTypeRestic,
				Source:       source,
				Repository:   "rest:https://user:repo-pass@backup.example/repo?token=remote-token&region=us",
				PasswordFile: passwordFile,
			},
			{
				ID:     "rclone-remote",
				Name:   "Rclone remote",
				Type:   JobTypeRclone,
				Source: source,
				Remote: ":s3,access_key_id=AKIASECRET,secret_access_key=secret-access-key:bucket/path",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	jobs := manager.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("ListJobs() length = %d, want 2", len(jobs))
	}
	for _, job := range jobs {
		assertNoBackupTargetSecrets(t, job.Destination)
		assertNoBackupTargetSecrets(t, job.Repository)
		assertNoBackupTargetSecrets(t, job.Remote)
		switch job.ID {
		case "restic-remote":
			if !strings.Contains(job.Repository, redactedBackupSecretValue) {
				t.Fatalf("restic repository = %q, want redacted marker", job.Repository)
			}
		case "rclone-remote":
			if !strings.Contains(job.Remote, redactedBackupSecretValue) {
				t.Fatalf("rclone remote = %q, want redacted marker", job.Remote)
			}
		}
	}
}

func TestManager_PublicResultsRedactSensitiveLocalPathSegments(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source", "token=source-secret")
	destination := filepath.Join(tmpDir, "backups", "token=destination-secret")
	restoreTarget := filepath.Join(tmpDir, "restore", "token=restore-secret")
	stateRoot := filepath.Join(tmpDir, "state", "token=state-secret")
	configPath := filepath.Join(tmpDir, "config", "token=config-secret", "mnemonas.toml")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "redact local paths")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")
	if err := os.MkdirAll(filepath.Dir(restoreTarget), 0700); err != nil {
		t.Fatalf("MkdirAll(restore parent) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:            "home",
			Name:          "Home backup",
			Type:          JobTypeLocal,
			Source:        source,
			Destination:   destination,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	assertNoBackupTargetSecrets(t, run.Source)
	assertNoBackupTargetSecrets(t, run.Destination)
	assertNoBackupTargetSecrets(t, run.SnapshotPath)
	assertNoBackupTargetSecrets(t, run.ManifestPath)

	drill, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	assertNoBackupTargetSecrets(t, drill.SnapshotPath)
	assertNoBackupTargetSecrets(t, drill.ManifestPath)
	assertNoBackupTargetSecrets(t, drill.RestoredPath)

	preview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget, IncludeConfig: true})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	assertNoBackupTargetSecrets(t, preview.Source)
	assertNoBackupTargetSecrets(t, preview.Destination)
	assertNoBackupTargetSecrets(t, preview.TargetPath)
	assertNoBackupTargetSecrets(t, preview.SnapshotPath)
	assertNoBackupTargetSecrets(t, preview.ManifestPath)

	restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget, IncludeConfig: true})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	assertNoBackupTargetSecrets(t, restore.TargetPath)
	assertNoBackupTargetSecrets(t, restore.SnapshotPath)
	assertNoBackupTargetSecrets(t, restore.ManifestPath)
	assertNoBackupTargetSecrets(t, restore.ConfigPath)

	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	assertNoBackupTargetSecrets(t, verify.Source)
	assertNoBackupTargetSecrets(t, verify.Destination)
	assertNoBackupTargetSecrets(t, verify.TargetPath)
	assertNoBackupTargetSecrets(t, verify.SnapshotPath)
	assertNoBackupTargetSecrets(t, verify.ManifestPath)
	assertNoBackupTargetSecrets(t, verify.ConfigPath)

	batchTarget := filepath.Join(tmpDir, "batch-restore", "token=restore-secret")
	if err := os.MkdirAll(filepath.Dir(batchTarget), 0700); err != nil {
		t.Fatalf("MkdirAll(batch restore parent) error: %v", err)
	}
	batchPreview, err := manager.RunBatchRestorePreview(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{{
			JobID:         "home",
			TargetPath:    batchTarget,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("RunBatchRestorePreview() error: %v", err)
	}
	if batchPreview.Status != StatusCompleted || len(batchPreview.Items) != 1 || batchPreview.Items[0].Preview == nil {
		t.Fatalf("unexpected RunBatchRestorePreview result: %+v", batchPreview)
	}
	assertNoBackupTargetSecrets(t, batchPreview.Items[0].TargetPath)
	assertNoBackupTargetSecrets(t, batchPreview.Items[0].Preview.TargetPath)
	assertNoBackupTargetSecrets(t, batchPreview.Items[0].Preview.SnapshotPath)
	assertNoBackupTargetSecrets(t, batchPreview.Items[0].Preview.ManifestPath)

	batchRestore, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{{
			JobID:         "home",
			TargetPath:    batchTarget,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() error: %v", err)
	}
	if batchRestore.Status != StatusCompleted || len(batchRestore.Items) != 1 || batchRestore.Items[0].Restore == nil || batchRestore.Items[0].Verify == nil {
		t.Fatalf("unexpected RunBatchRestore result: %+v", batchRestore)
	}
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].TargetPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Restore.TargetPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Restore.SnapshotPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Restore.ManifestPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Restore.ConfigPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Verify.TargetPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Verify.SnapshotPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Verify.ManifestPath)
	assertNoBackupTargetSecrets(t, batchRestore.Items[0].Verify.ConfigPath)

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	for _, value := range []string{
		job.Source,
		job.Destination,
		job.LastRun.Source,
		job.LastRun.Destination,
		job.LastRun.SnapshotPath,
		job.LastRun.ManifestPath,
		job.LastRestoreDrill.SnapshotPath,
		job.LastRestoreDrill.ManifestPath,
		job.LastRestoreDrill.RestoredPath,
		job.LastRestore.TargetPath,
		job.LastRestore.SnapshotPath,
		job.LastRestore.ManifestPath,
		job.LastRestore.ConfigPath,
		job.LastRestoreVerify.Source,
		job.LastRestoreVerify.Destination,
		job.LastRestoreVerify.TargetPath,
		job.LastRestoreVerify.SnapshotPath,
		job.LastRestoreVerify.ManifestPath,
		job.LastRestoreVerify.ConfigPath,
	} {
		assertNoBackupTargetSecrets(t, value)
	}
}

func TestManager_RunJobAndRestoreDrill(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	stateRoot := filepath.Join(tmpDir, "state")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")

	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "hello backup")
	mustWriteFile(t, filepath.Join(source, "cache", "skip.txt"), "skip me")
	mustWriteFile(t, filepath.Join(source, "locked", "note.txt"), "locked backup")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")
	if err := os.MkdirAll(filepath.Join(source, "albums", "empty"), 0700); err != nil {
		t.Fatalf("MkdirAll(source empty dir) error: %v", err)
	}
	if err := os.Chmod(filepath.Join(source, "albums", "empty"), 0777); err != nil {
		t.Fatalf("Chmod(source empty dir) error: %v", err)
	}
	if err := os.Chmod(filepath.Join(source, "locked"), 0500); err != nil {
		t.Fatalf("Chmod(source locked dir) error: %v", err)
	}
	if err := os.Chmod(source, 0750); err != nil {
		t.Fatalf("Chmod(source root) error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(source, 0700)
		_ = os.Chmod(filepath.Join(source, "locked"), 0700)
	})

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:                "home",
			Name:              "Home backup",
			Type:              JobTypeLocal,
			Source:            source,
			Destination:       destination,
			IncludeConfig:     true,
			VerifyAfterBackup: true,
			Exclude:           []string{"cache"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if jobs := manager.ListJobs(); len(jobs) != 1 || jobs[0].Command != "" {
		t.Fatalf("local job command should be hidden in API view: %+v", jobs)
	}

	result, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("RunJob status = %q, want %q", result.Status, StatusCompleted)
	}
	if result.FileCount != 3 {
		t.Fatalf("RunJob file count = %d, want 3", result.FileCount)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(result.SnapshotPath, "data", "locked"), 0700)
	})
	assertFileContent(t, filepath.Join(result.SnapshotPath, "data", "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(result.SnapshotPath, "data", "locked", "note.txt"), "locked backup")
	assertFileContent(t, filepath.Join(result.SnapshotPath, "config", "config.toml"), "[server]\nport = 8080\n")
	assertDirectoryExists(t, filepath.Join(result.SnapshotPath, "data", "albums", "empty"))
	assertPathMode(t, filepath.Join(result.SnapshotPath, "data"), 0750)
	assertPathMode(t, filepath.Join(result.SnapshotPath, "data", "albums", "empty"), 0777)
	assertPathMode(t, filepath.Join(result.SnapshotPath, "data", "locked"), 0500)
	if _, err := os.Stat(filepath.Join(result.SnapshotPath, "data", "cache", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded file stat error = %v, want not exist", err)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted {
		t.Fatalf("RunRestoreDrill status = %q, want %q", drill.Status, StatusCompleted)
	}
	if drill.FileCount != result.FileCount {
		t.Fatalf("RunRestoreDrill file count = %d, want %d", drill.FileCount, result.FileCount)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(drill.RestoredPath, "data", "locked"), 0700)
	})
	assertFileContent(t, filepath.Join(drill.RestoredPath, "data", "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(drill.RestoredPath, "data", "locked", "note.txt"), "locked backup")
	assertFileContent(t, filepath.Join(drill.RestoredPath, "config", "config.toml"), "[server]\nport = 8080\n")
	assertDirectoryExists(t, filepath.Join(drill.RestoredPath, "data", "albums", "empty"))
	assertPathMode(t, filepath.Join(drill.RestoredPath, "data"), 0750)
	assertPathMode(t, filepath.Join(drill.RestoredPath, "data", "albums", "empty"), 0777)
	assertPathMode(t, filepath.Join(drill.RestoredPath, "data", "locked"), 0500)

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath:    restoreTarget,
		IncludeConfig: true,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.FileCount != result.FileCount || !preview.ConfigAvailable || !preview.ConfigIncluded {
		t.Fatalf("unexpected RunRestorePreview result: %+v", preview)
	}
	if len(preview.SamplePaths) != 3 || preview.SamplePaths[0] != "docs/note.txt" || preview.SamplePaths[1] != "locked/note.txt" || preview.SamplePaths[2] != ".mnemonas-restore/config.toml" {
		t.Fatalf("preview sample paths = %#v", preview.SamplePaths)
	}
	if len(preview.PreflightChecks) == 0 || len(preview.CutoverChecklist) == 0 || len(preview.RollbackChecklist) == 0 {
		t.Fatalf("preview missing preflight or checklists: %+v", preview)
	}
	targetStateCheck := restorePreflightCheckByID(t, preview.PreflightChecks, "target_state")
	if !strings.Contains(targetStateCheck.Detail, "尚不存在") {
		t.Fatalf("preview target_state detail = %q, want missing target detail", targetStateCheck.Detail)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	emptyRestoreTarget := filepath.Join(tmpDir, "empty-restore-target")
	if err := os.Mkdir(emptyRestoreTarget, 0700); err != nil {
		t.Fatalf("Mkdir(emptyRestoreTarget) error: %v", err)
	}
	emptyPreview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: emptyRestoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() empty target error: %v", err)
	}
	emptyTargetStateCheck := restorePreflightCheckByID(t, emptyPreview.PreflightChecks, "target_state")
	if !strings.Contains(emptyTargetStateCheck.Detail, "已存在且为空") {
		t.Fatalf("preview empty target_state detail = %q, want empty target detail", emptyTargetStateCheck.Detail)
	}

	restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{
		TargetPath:    restoreTarget,
		IncludeConfig: true,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted {
		t.Fatalf("RunRestore status = %q, want %q", restore.Status, StatusCompleted)
	}
	if restore.TargetPath != restoreTarget || restore.FileCount != result.FileCount || !restore.ConfigRestored {
		t.Fatalf("unexpected RunRestore result: %+v", restore)
	}
	if restore.ConfigPath != filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml") {
		t.Fatalf("RunRestore config path = %q, want installed target config path", restore.ConfigPath)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(restoreTarget, "locked"), 0700)
	})
	if len(restore.PreflightChecks) == 0 || len(restore.CutoverChecklist) == 0 || len(restore.RollbackChecklist) == 0 {
		t.Fatalf("restore missing persisted preflight or checklists: %+v", restore)
	}
	assertPathMode(t, restoreTarget, 0750)
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "hello backup")
	assertFileContent(t, filepath.Join(restoreTarget, "locked", "note.txt"), "locked backup")
	assertFileContent(t, filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml"), "[server]\nport = 8080\n")
	assertDirectoryExists(t, filepath.Join(restoreTarget, "albums", "empty"))
	assertPathMode(t, filepath.Join(restoreTarget, "albums", "empty"), 0777)
	assertPathMode(t, filepath.Join(restoreTarget, "locked"), 0500)
	if _, err := os.Stat(filepath.Join(restoreTarget, "cache", "skip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded restored file stat error = %v, want not exist", err)
	}
	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected RunRestoreVerify result: %+v", verify)
	}
	if !verify.ConfigFound || verify.ConfigPath != filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml") {
		t.Fatalf("RunRestoreVerify config result = %+v", verify)
	}
	if verify.LooksLikeStorageRoot {
		t.Fatalf("RunRestoreVerify should not classify this test source as a storage root: %+v", verify)
	}
	assertWarningsNotContain(t, verify.Warnings, "恢复目标根目录权限不匹配")
	if result.JobConfigBinding != "" || result.ManifestSize != 0 || result.ManifestDigest != "" ||
		drill.JobConfigBinding != "" || restore.JobConfigBinding != "" || restore.ManifestSize != 0 || restore.ManifestDigest != "" ||
		verify.JobConfigBinding != "" {
		t.Fatalf("public backup results exposed internal readiness evidence")
	}
	expectedBinding, err := jobConfigEvidenceBinding(manager.jobs["home"], manager.storageRoot, manager.configPath)
	if err != nil {
		t.Fatalf("jobConfigEvidenceBinding() error: %v", err)
	}
	manager.mu.Lock()
	persistedEvidence := []struct {
		name    string
		binding string
	}{
		{name: "backup run", binding: manager.state.Jobs["home"].LastRun.JobConfigBinding},
		{name: "restore drill", binding: manager.state.Jobs["home"].LastRestoreDrill.JobConfigBinding},
		{name: "explicit restore", binding: manager.state.Jobs["home"].LastRestore.JobConfigBinding},
		{name: "restore verify", binding: manager.state.Jobs["home"].LastRestoreVerify.JobConfigBinding},
	}
	persistedManifestSize := manager.state.Jobs["home"].LastRun.ManifestSize
	persistedManifestDigest := manager.state.Jobs["home"].LastRun.ManifestDigest
	persistedRestoreManifestSize := manager.state.Jobs["home"].LastRestore.ManifestSize
	persistedRestoreManifestDigest := manager.state.Jobs["home"].LastRestore.ManifestDigest
	manager.mu.Unlock()
	if persistedManifestSize <= 0 {
		t.Fatal("persisted backup run omitted manifest size")
	}
	if persistedManifestDigest == "" {
		t.Fatal("persisted backup run omitted manifest digest")
	}
	if persistedRestoreManifestSize <= 0 {
		t.Fatal("persisted restore omitted manifest size")
	}
	if persistedRestoreManifestDigest == "" {
		t.Fatal("persisted restore omitted manifest digest")
	}
	for _, evidence := range persistedEvidence {
		if evidence.binding != expectedBinding {
			t.Fatalf("%s binding = %q, want %q", evidence.name, evidence.binding, expectedBinding)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	reloaded, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	jobs := reloaded.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRun == nil || jobs[0].LastRestoreDrill == nil || jobs[0].LastRestore == nil || jobs[0].LastRestoreVerify == nil {
		t.Fatalf("reloaded jobs missing persisted status: %+v", jobs)
	}
	if jobs[0].LastRun.ManifestSize != 0 || jobs[0].LastRun.ManifestDigest != "" {
		t.Fatalf("reloaded public job exposed manifest evidence: %+v", jobs[0].LastRun)
	}
	if len(jobs[0].RestoreDrillHistory) != 1 || jobs[0].RestoreDrillHistory[0].ID != drill.ID {
		t.Fatalf("reloaded restore drill history = %+v, want drill %s", jobs[0].RestoreDrillHistory, drill.ID)
	}
	if jobs[0].RestoreDrillStats == nil || jobs[0].RestoreDrillStats.TotalRuns != 1 || jobs[0].RestoreDrillStats.SuccessfulRuns != 1 || jobs[0].RestoreDrillStats.ConsecutiveSuccesses != 1 {
		t.Fatalf("reloaded restore drill stats = %+v, want one successful drill", jobs[0].RestoreDrillStats)
	}
	if jobs[0].LastRestore.TargetPath != restoreTarget || jobs[0].LastRestore.Status != StatusCompleted {
		t.Fatalf("reloaded last restore = %+v, want completed restore to %s", jobs[0].LastRestore, restoreTarget)
	}
	if jobs[0].LastRestore.ManifestSize != 0 || jobs[0].LastRestore.ManifestDigest != "" {
		t.Fatalf("reloaded public restore exposed manifest evidence: %+v", jobs[0].LastRestore)
	}
	if len(jobs[0].RestoreHistory) != 1 || jobs[0].RestoreHistory[0].ID != restore.ID {
		t.Fatalf("reloaded restore history = %+v, want restore %s", jobs[0].RestoreHistory, restore.ID)
	}
	if jobs[0].LastRestoreVerify.TargetPath != restoreTarget || jobs[0].LastRestoreVerify.Status != StatusCompleted {
		t.Fatalf("reloaded last restore verify = %+v, want completed verify for %s", jobs[0].LastRestoreVerify, restoreTarget)
	}
	if jobs[0].LastMatchingRestoreVerify == nil || jobs[0].LastMatchingRestoreVerify.ID != verify.ID {
		t.Fatalf("reloaded matching restore verify = %+v, want verify %s", jobs[0].LastMatchingRestoreVerify, verify.ID)
	}
	if len(jobs[0].RestoreReportFindings) == 0 {
		t.Fatalf("reloaded job restore report findings are empty: %+v", jobs[0])
	}
	report, err := reloaded.BuildRestoreReport("home")
	if err != nil {
		t.Fatalf("BuildRestoreReport() error: %v", err)
	}
	if report.Job.ID != "home" || report.LastRestore == nil || report.LastRestoreVerify == nil || report.LastMatchingRestoreVerify == nil || len(report.RestoreDrillHistory) != 1 || report.RestoreDrillStats == nil || len(report.Findings) == 0 {
		t.Fatalf("unexpected restore report: %+v", report)
	}
	if !reflect.DeepEqual(report.Job.RestoreReportFindings, report.Findings) {
		t.Fatalf("report job findings = %+v, want report findings %+v", report.Job.RestoreReportFindings, report.Findings)
	}
	if report.LastMatchingRestoreVerify.ID != verify.ID {
		t.Fatalf("report matching restore verify = %+v, want verify %s", report.LastMatchingRestoreVerify, verify.ID)
	}
}

func TestManager_RunRestoreDrillWarnsWhenArtifactCleanupFails(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restore drill cleanup")
	notifier := &recordingNotifier{}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	manager.notifier = notifier

	previousRemove := removeRestoreDrillArtifact
	var cleanupPath string
	removeRestoreDrillArtifact = func(targetPath, label string) error {
		if label == "restore drill" {
			cleanupPath = targetPath
			return errors.New("cleanup failed with token=restore-secret")
		}
		return previousRemove(targetPath, label)
	}
	t.Cleanup(func() {
		removeRestoreDrillArtifact = previousRemove
	})

	drill, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || !drill.Warning || !drill.ArtifactKept || drill.RestoredPath == "" {
		t.Fatalf("RunRestoreDrill result = %+v, want completed warning with retained artifact", drill)
	}
	assertWarningsContain(t, drill.Warnings, "临时恢复目录清理失败")
	assertNoBackupTargetSecrets(t, strings.Join(drill.Warnings, "\n"))
	if cleanupPath == "" {
		t.Fatal("restore drill cleanup path was not attempted")
	}
	assertFileContent(t, filepath.Join(drill.RestoredPath, "data", "docs", "note.txt"), "restore drill cleanup")

	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRestoreDrill == nil || !jobs[0].LastRestoreDrill.Warning || len(jobs[0].RestoreDrillHistory) != 1 || !jobs[0].RestoreDrillHistory[0].Warning {
		t.Fatalf("persisted restore drill warning missing: %+v", jobs)
	}
	assertWarningsContain(t, jobs[0].RestoreReportFindings, "恢复演练警告")
	report, err := manager.BuildRestoreReport("home")
	if err != nil {
		t.Fatalf("BuildRestoreReport() error: %v", err)
	}
	assertWarningsContain(t, report.Findings, "恢复演练警告")

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeRestoreDrill || event.Level != NotificationLevelWarning || event.Message != "backup restore drill completed with warnings" {
		t.Fatalf("unexpected notification event: %+v", event)
	}
	if event.Status != StatusCompleted || event.WarningCount != 1 || event.ErrorMessagePresent || !event.LocationOmitted {
		t.Fatalf("notification summary markers = %+v, want completed warning without raw error", event)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, event)
}

func TestRestoreReportFindingsIgnoreStaleRestoreVerify(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreStarted.Add(-time.Hour)
	verifyFinished := verifyStarted.Add(time.Second)

	findings := restoreReportFindings(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:    "ok",
		RestoreDrillStatus: "ok",
		LastRestore: &RestoreResult{
			Status:     StatusCompleted,
			StartedAt:  restoreStarted,
			FinishedAt: &restoreFinished,
			TargetPath: "/restore/new",
		},
		LastRestoreVerify: &RestoreVerifyResult{
			Status:     StatusCompleted,
			StartedAt:  verifyStarted,
			FinishedAt: &verifyFinished,
			TargetPath: "/restore/old",
			Warnings:   []string{"stale verify warning"},
		},
	})

	assertWarningsContain(t, findings, "最近一次显式恢复尚未完成匹配的只读校验")
	assertWarningsContain(t, findings, "最近一次只读校验目标不属于当前恢复目标")
	assertWarningsNotContain(t, findings, "stale verify warning")
	if matching := matchingRestoreVerifyForRestore(&RestoreResult{
		Status:     StatusCompleted,
		StartedAt:  restoreStarted,
		FinishedAt: &restoreFinished,
		TargetPath: "/restore/new",
	}, &RestoreVerifyResult{
		Status:     StatusCompleted,
		StartedAt:  verifyStarted,
		FinishedAt: &verifyFinished,
		TargetPath: "/restore/old",
	}); matching != nil {
		t.Fatalf("matchingRestoreVerifyForRestore() = %+v, want nil for stale verify", matching)
	}
	verifyAfterFailedRestore := restoreFinished.Add(time.Minute)
	if matching := matchingRestoreVerifyForRestore(&RestoreResult{
		Status:     StatusFailed,
		StartedAt:  restoreStarted,
		FinishedAt: &restoreFinished,
		TargetPath: "/restore/new",
	}, &RestoreVerifyResult{
		Status:     StatusCompleted,
		StartedAt:  verifyAfterFailedRestore,
		FinishedAt: &verifyAfterFailedRestore,
		TargetPath: "/restore/new",
	}); matching != nil {
		t.Fatalf("matchingRestoreVerifyForRestore() = %+v, want nil for failed restore", matching)
	}
	matchingTime := restoreFinished.Add(time.Minute)
	if matching := matchingRestoreVerifyForRestore(&RestoreResult{
		Status:     StatusCompleted,
		StartedAt:  restoreStarted,
		FinishedAt: &restoreFinished,
		TargetPath: "/restore/parent/../new",
	}, &RestoreVerifyResult{
		Status:     StatusCompleted,
		StartedAt:  matchingTime,
		FinishedAt: &matchingTime,
		TargetPath: "/restore/new",
	}); matching != nil {
		t.Fatalf("matchingRestoreVerifyForRestore() = %+v, want nil for noncanonical target mismatch", matching)
	}
}

func TestRestoreReportFindingsIgnoreOverlappingRestoreVerify(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreFinished.Add(-500 * time.Millisecond)
	verifyFinished := restoreFinished.Add(time.Second)

	restore := &RestoreResult{
		Status:     StatusCompleted,
		StartedAt:  restoreStarted,
		FinishedAt: &restoreFinished,
		TargetPath: "/restore/current",
	}
	verify := &RestoreVerifyResult{
		Status:     StatusCompleted,
		StartedAt:  verifyStarted,
		FinishedAt: &verifyFinished,
		TargetPath: "/restore/current",
		Warnings:   []string{"overlapping verify warning"},
	}

	if matching := matchingRestoreVerifyForRestore(restore, verify); matching != nil {
		t.Fatalf("matchingRestoreVerifyForRestore() = %+v, want nil for verify started before restore finished", matching)
	}

	findings := restoreReportFindings(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:    "ok",
		RestoreDrillStatus: "ok",
		LastRestore:        restore,
		LastRestoreVerify:  verify,
	})

	assertWarningsContain(t, findings, "最近一次显式恢复尚未完成匹配的只读校验")
	assertWarningsContain(t, findings, "最近一次只读校验早于恢复完成")
	assertWarningsNotContain(t, findings, "overlapping verify warning")
}

func TestRestoreReportFindingsReportUnusableRestoreVerifyStatus(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreFinished.Add(time.Minute)

	findings := restoreReportFindings(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:    "ok",
		RestoreDrillStatus: "ok",
		LastRestore: &RestoreResult{
			Status:     StatusCompleted,
			StartedAt:  restoreStarted,
			FinishedAt: &restoreFinished,
			TargetPath: "/restore/current",
		},
		LastRestoreVerify: &RestoreVerifyResult{
			Status:     "queued",
			StartedAt:  verifyStarted,
			TargetPath: "/restore/current",
			Warnings:   []string{"queued verify warning"},
		},
	})

	assertWarningsContain(t, findings, "最近一次显式恢复尚未完成匹配的只读校验")
	assertWarningsContain(t, findings, "最近一次只读校验状态不能作为当前恢复目标的校验证据")
	assertWarningsNotContain(t, findings, "queued verify warning")
}

func TestRestoreReportFindingsAttachRunningRestoreVerify(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreFinished.Add(time.Minute)

	restore := &RestoreResult{
		Status:     StatusCompleted,
		StartedAt:  restoreStarted,
		FinishedAt: &restoreFinished,
		TargetPath: "/restore/current",
	}
	verify := &RestoreVerifyResult{
		Status:     StatusRunning,
		StartedAt:  verifyStarted,
		TargetPath: "/restore/current",
	}

	matching := matchingRestoreVerifyForRestore(restore, verify)
	if matching == nil || matching.Status != StatusRunning {
		t.Fatalf("matchingRestoreVerifyForRestore() = %+v, want running verify", matching)
	}

	findings := restoreReportFindings(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:    "ok",
		RestoreDrillStatus: "ok",
		LastRestore:        restore,
		LastRestoreVerify:  verify,
	})

	assertWarningsContain(t, findings, "最近一次恢复目录校验仍在运行")
	assertWarningsNotContain(t, findings, "最近一次显式恢复尚未完成匹配的只读校验")
}

func TestRestoreReportFindingsRedactsSensitiveMessages(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreFinished.Add(time.Minute)
	verifyFinished := verifyStarted.Add(time.Second)
	targetPath := "/restore/token=restore-secret"
	matchingVerify := &RestoreVerifyResult{
		Status:       StatusFailed,
		StartedAt:    verifyStarted,
		FinishedAt:   &verifyFinished,
		TargetPath:   targetPath,
		ErrorMessage: "verify failed for secret_access_key=secret-access-key",
		Warnings:     []string{"restore warning for token=restore-secret"},
	}

	findings := restoreReportFindingsWithMatchingVerify(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:     "failed",
		RetentionMessage:    "retention failed at /backups/token=destination-secret",
		RestoreDrillStatus:  "failed",
		RestoreDrillMessage: "restore drill failed: --password repo-pass",
		LastRestore: &RestoreResult{
			Status:     StatusCompleted,
			StartedAt:  restoreStarted,
			FinishedAt: &restoreFinished,
			TargetPath: targetPath,
		},
		LastRestoreVerify: matchingVerify,
	}, matchingVerify)

	joined := strings.Join(findings, "\n")
	assertNoBackupTargetSecrets(t, joined)
	for _, want := range []string{"token=" + redactedBackupSecretValue, "--password " + redactedBackupSecretValue, "secret_access_key=" + redactedBackupSecretValue} {
		if !strings.Contains(joined, want) {
			t.Fatalf("restore report findings = %q, want %q", joined, want)
		}
	}
}

func TestRestoreReportMatchingUsesRawTargetsBeforeRedaction(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore report")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	backupStarted := time.Date(2026, 5, 9, 4, 0, 0, 0, time.UTC)
	backupFinished := backupStarted.Add(time.Second)
	restoreStarted := backupFinished.Add(time.Minute)
	restoreFinished := restoreStarted.Add(time.Second)
	verifyStarted := restoreFinished.Add(time.Minute)
	verifyFinished := verifyStarted.Add(time.Second)
	restoreTarget := filepath.Join(tmpDir, "restore", "token=restore-secret-one")
	verifyTarget := filepath.Join(tmpDir, "restore", "token=restore-secret-two")

	manager.mu.Lock()
	manager.state.Jobs["home"] = JobState{
		LastSuccessfulRun: &RunResult{
			ID:         "backup-run",
			JobID:      "home",
			Status:     StatusCompleted,
			StartedAt:  backupStarted,
			FinishedAt: &backupFinished,
		},
		LastRestore: &RestoreResult{
			ID:         "restore-run",
			JobID:      "home",
			Status:     StatusCompleted,
			StartedAt:  restoreStarted,
			FinishedAt: &restoreFinished,
			TargetPath: restoreTarget,
		},
		LastRestoreVerify: &RestoreVerifyResult{
			ID:         "verify-run",
			JobID:      "home",
			Status:     StatusCompleted,
			StartedAt:  verifyStarted,
			FinishedAt: &verifyFinished,
			TargetPath: verifyTarget,
			Warnings:   []string{"wrong target warning"},
		},
	}
	manager.mu.Unlock()

	jobs := manager.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("ListJobs() length = %d, want 1", len(jobs))
	}
	assertWarningsContain(t, jobs[0].RestoreReportFindings, "最近一次显式恢复尚未完成匹配的只读校验")
	assertWarningsContain(t, jobs[0].RestoreReportFindings, "最近一次只读校验目标不属于当前恢复目标")
	assertWarningsNotContain(t, jobs[0].RestoreReportFindings, "wrong target warning")
	if jobs[0].LastMatchingRestoreVerify != nil {
		t.Fatalf("LastMatchingRestoreVerify = %+v, want nil for different raw targets", jobs[0].LastMatchingRestoreVerify)
	}

	report, err := manager.BuildRestoreReport("home")
	if err != nil {
		t.Fatalf("BuildRestoreReport() error: %v", err)
	}
	if report.LastMatchingRestoreVerify != nil {
		t.Fatalf("LastMatchingRestoreVerify = %+v, want nil for different raw targets", report.LastMatchingRestoreVerify)
	}
	assertWarningsContain(t, report.Findings, "最近一次显式恢复尚未完成匹配的只读校验")
	assertWarningsContain(t, report.Findings, "最近一次只读校验目标不属于当前恢复目标")
	assertWarningsNotContain(t, report.Findings, "wrong target warning")
	assertNoBackupTargetSecrets(t, report.LastRestore.TargetPath)
	assertNoBackupTargetSecrets(t, report.LastRestoreVerify.TargetPath)
}

func TestRestoreReportFindingsReportRunningRestore(t *testing.T) {
	restoreStarted := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	oldVerifyFinished := restoreStarted.Add(-time.Minute)

	findings := restoreReportFindings(JobView{
		LastSuccessfulRun: &RunResult{
			Status: StatusCompleted,
		},
		RetentionStatus:    "ok",
		RestoreDrillStatus: "ok",
		LastRestore: &RestoreResult{
			Status:     StatusRunning,
			StartedAt:  restoreStarted,
			TargetPath: "/restore/new",
		},
		LastRestoreVerify: &RestoreVerifyResult{
			Status:     StatusCompleted,
			StartedAt:  oldVerifyFinished,
			FinishedAt: &oldVerifyFinished,
			TargetPath: "/restore/new",
			Warnings:   []string{"old verify warning"},
		},
	})

	assertWarningsContain(t, findings, "最近一次显式恢复仍在运行")
	assertWarningsNotContain(t, findings, "old verify warning")
	assertWarningsNotContain(t, findings, "最近一次显式恢复尚未完成匹配的只读校验")
}

func TestNewManagerMarksInterruptedJobStateFailed(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	root := filepath.Join(tmpDir, "state")
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	startedAt := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("MkdirAll(root) error: %v", err)
	}
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	state := persistedState{Jobs: map[string]JobState{
		"home": {
			LastRun: &RunResult{
				ID:        "backup-run",
				JobID:     "home",
				Status:    StatusRunning,
				StartedAt: startedAt,
			},
			LastSuccessfulRun: &RunResult{
				ID:        "previous-success",
				JobID:     "home",
				Status:    StatusCompleted,
				StartedAt: startedAt.Add(-time.Hour),
			},
			LastRestoreDrill: &RestoreDrillResult{
				ID:        "restore-drill",
				JobID:     "home",
				Status:    StatusRunning,
				StartedAt: startedAt,
			},
			LastRestore: &RestoreResult{
				ID:         "restore-run",
				JobID:      "home",
				Status:     StatusRunning,
				StartedAt:  startedAt,
				TargetPath: filepath.Join(tmpDir, "restore-target"),
			},
			LastRestoreVerify: &RestoreVerifyResult{
				ID:         "restore-verify",
				JobID:      "home",
				Status:     StatusRunning,
				StartedAt:  startedAt,
				TargetPath: filepath.Join(tmpDir, "restore-target"),
			},
			LastRetentionCheck: &RetentionCheckResult{
				ID:        "retention-check",
				JobID:     "home",
				Status:    StatusRunning,
				StartedAt: startedAt,
				Target:    destination,
			},
		},
	}}
	if err := writeJSONFile(filepath.Join(root, stateFileName), state, 0600); err != nil {
		t.Fatalf("writeJSONFile(state) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        root,
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.Running {
		t.Fatalf("job running = true, want false after reload")
	}
	if job.LastRun == nil || job.LastRun.Status != StatusFailed || job.LastRun.ErrorMessage != interruptedStatusMessage || job.LastRun.FinishedAt == nil {
		t.Fatalf("LastRun = %+v, want interrupted failed result", job.LastRun)
	}
	if job.LastSuccessfulRun == nil || job.LastSuccessfulRun.ID != "previous-success" {
		t.Fatalf("LastSuccessfulRun = %+v, want previous success preserved", job.LastSuccessfulRun)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryCancelled || len(job.RestoreDrillHistory) != 1 {
		t.Fatalf("LastRestoreDrill/history = %+v/%+v, want interrupted failed drill in history", job.LastRestoreDrill, job.RestoreDrillHistory)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || len(job.RestoreHistory) != 1 {
		t.Fatalf("LastRestore/history = %+v/%+v, want interrupted failed restore in history", job.LastRestore, job.RestoreHistory)
	}
	if job.LastRestoreVerify == nil || job.LastRestoreVerify.Status != StatusFailed || job.LastRestoreVerify.ErrorMessage != interruptedStatusMessage {
		t.Fatalf("LastRestoreVerify = %+v, want interrupted failed verify", job.LastRestoreVerify)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.LastRetentionCheck.ErrorMessage != interruptedStatusMessage {
		t.Fatalf("LastRetentionCheck = %+v, want interrupted failed retention check", job.LastRetentionCheck)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	reloaded, err := newBackupTestManager(t, ManagerConfig{
		Root:        root,
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() reload error: %v", err)
	}
	reloadedJob, err := reloaded.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() reload error: %v", err)
	}
	if reloadedJob.LastRun == nil || reloadedJob.LastRun.Status != StatusFailed || reloadedJob.LastRun.ErrorMessage != interruptedStatusMessage {
		t.Fatalf("reloaded LastRun = %+v, want persisted interrupted failure", reloadedJob.LastRun)
	}
	if len(reloadedJob.RestoreDrillHistory) != 1 || len(reloadedJob.RestoreHistory) != 1 {
		t.Fatalf("reloaded histories = %+v/%+v, want one interrupted entry each", reloadedJob.RestoreDrillHistory, reloadedJob.RestoreHistory)
	}
}

func TestManager_RunRestoreDrillWithoutSnapshot(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
	if !errors.Is(err, ErrNoSnapshots) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrNoSnapshots", err)
	}
	job, getErr := manager.GetJob("home")
	if getErr != nil {
		t.Fatalf("GetJob() error: %v", getErr)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryNoSnapshot {
		t.Fatalf("failed restore drill was not persisted: %+v", job.LastRestoreDrill)
	}
	if len(job.RestoreDrillHistory) != 1 || job.RestoreDrillHistory[0].Status != StatusFailed {
		t.Fatalf("restore drill history = %+v, want one failed drill", job.RestoreDrillHistory)
	}
	if job.RestoreDrillStats == nil || job.RestoreDrillStats.TotalRuns != 1 || job.RestoreDrillStats.FailedRuns != 1 || job.RestoreDrillStats.ConsecutiveFailures != 1 || job.RestoreDrillStats.LastFailureMessage == "" || job.RestoreDrillStats.LastFailureCategory != FailureCategoryNoSnapshot {
		t.Fatalf("restore drill stats = %+v, want one failed drill with failure message", job.RestoreDrillStats)
	}
}

func TestManager_RestoreDrillHistoryCapsLatestResults(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	base := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	for i := 0; i < restoreDrillHistoryLimit+5; i++ {
		startedAt := base.Add(time.Duration(i) * time.Minute)
		finishedAt := startedAt.Add(time.Second)
		result := &RestoreDrillResult{
			ID:            formatRunID(startedAt),
			JobID:         "home",
			Status:        StatusCompleted,
			StartedAt:     startedAt,
			FinishedAt:    &finishedAt,
			DurationMs:    1000,
			FileCount:     int64(i),
			VerifiedBytes: int64(i * 100),
		}
		if err := manager.updateLastRestoreDrill(result, true); err != nil {
			t.Fatalf("updateLastRestoreDrill(%d) error: %v", i, err)
		}
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if len(job.RestoreDrillHistory) != restoreDrillHistoryLimit {
		t.Fatalf("restore drill history length = %d, want %d", len(job.RestoreDrillHistory), restoreDrillHistoryLimit)
	}
	if job.RestoreDrillStats == nil || job.RestoreDrillStats.TotalRuns != restoreDrillHistoryLimit || job.RestoreDrillStats.SuccessRate != 1 || job.RestoreDrillStats.ConsecutiveSuccesses != restoreDrillHistoryLimit {
		t.Fatalf("restore drill stats = %+v, want all retained drills successful", job.RestoreDrillStats)
	}
	if job.RestoreDrillHistory[0].ID != formatRunID(base.Add(time.Duration(restoreDrillHistoryLimit+4)*time.Minute)) {
		t.Fatalf("latest restore drill history entry = %+v", job.RestoreDrillHistory[0])
	}
	if job.RestoreDrillHistory[len(job.RestoreDrillHistory)-1].ID != formatRunID(base.Add(5*time.Minute)) {
		t.Fatalf("oldest retained restore drill history entry = %+v", job.RestoreDrillHistory[len(job.RestoreDrillHistory)-1])
	}
}

func TestManager_RunRestoreRejectsUnsafeTarget(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	sourceTarget := filepath.Join(source, "restored")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: sourceTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() source target error = %v, want ErrUnsafePath", err)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || job.LastRestore.TargetPath != sourceTarget {
		t.Fatalf("failed restore was not persisted: %+v", job.LastRestore)
	}
	if len(job.RestoreHistory) != 1 || job.RestoreHistory[0].Status != StatusFailed {
		t.Fatalf("failed restore history = %+v, want one failed restore", job.RestoreHistory)
	}

	existingTarget := filepath.Join(tmpDir, "existing")
	mustWriteFile(t, filepath.Join(existingTarget, "old.txt"), "old")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: existingTarget})
	if !errors.Is(err, ErrRestoreTargetExists) {
		t.Fatalf("RunRestore() existing target error = %v, want ErrRestoreTargetExists", err)
	}
	job, err = manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() after existing target error: %v", err)
	}
	if len(job.RestoreHistory) != 2 || job.RestoreHistory[0].TargetPath != existingTarget {
		t.Fatalf("restore history after second failure = %+v, want latest existing target failure", job.RestoreHistory)
	}

	missingTarget := filepath.Join(tmpDir, "missing")
	_, err = manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: missingTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreVerify() missing target error = %v, want ErrUnsafePath", err)
	}
	job, err = manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() after verify error: %v", err)
	}
	if len(job.RestoreHistory) != 2 {
		t.Fatalf("restore verify should not write restore history: %+v", job.RestoreHistory)
	}
	if job.LastRestoreVerify == nil || job.LastRestoreVerify.Status != StatusFailed || job.LastRestoreVerify.TargetPath != missingTarget {
		t.Fatalf("failed restore verify was not persisted separately: %+v", job.LastRestoreVerify)
	}

	previousRestoreID := job.LastRestore.ID
	previousRestoreVerifyID := job.LastRestoreVerify.ID
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: "relative"})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() relative target error = %v, want ErrUnsafePath", err)
	}
	_, err = manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: `C:\restore`})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreVerify() backslash target error = %v, want ErrUnsafePath", err)
	}
	job, err = manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() after invalid syntax errors: %v", err)
	}
	if len(job.RestoreHistory) != 2 || job.LastRestore == nil || job.LastRestore.ID != previousRestoreID {
		t.Fatalf("invalid restore syntax should not overwrite restore history/status: history=%+v last=%+v", job.RestoreHistory, job.LastRestore)
	}
	if job.LastRestoreVerify == nil || job.LastRestoreVerify.ID != previousRestoreVerifyID {
		t.Fatalf("invalid restore verify syntax should not overwrite last restore verify: %+v", job.LastRestoreVerify)
	}
}

func TestManager_RunRestoreVerifyWarnsWhenLocalTargetDiffersFromManifest(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "original")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")
	if err := os.MkdirAll(filepath.Join(source, "empty-missing"), 0700); err != nil {
		t.Fatalf("MkdirAll(empty-missing) error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, "empty-mode"), 0700); err != nil {
		t.Fatalf("MkdirAll(empty-mode) error: %v", err)
	}
	if err := os.Chmod(filepath.Join(source, "empty-mode"), 0777); err != nil {
		t.Fatalf("Chmod(source empty-mode) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:            "home",
			Name:          "Home backup",
			Type:          JobTypeLocal,
			Source:        source,
			Destination:   destination,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget, IncludeConfig: true})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if err := os.Chmod(restoreTarget, 0755); err != nil {
		t.Fatalf("Chmod(restore target root) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(restoreTarget, "docs", "note.txt"), []byte("changed"), 0600); err != nil {
		t.Fatalf("WriteFile(restored note) error: %v", err)
	}
	mustWriteFile(t, filepath.Join(restoreTarget, "extra.txt"), "extra")
	if err := os.Remove(filepath.Join(restoreTarget, "empty-missing")); err != nil {
		t.Fatalf("Remove(empty-missing) error: %v", err)
	}
	if err := os.Chmod(filepath.Join(restoreTarget, "empty-mode"), 0700); err != nil {
		t.Fatalf("Chmod(restored empty-mode) error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(restoreTarget, "extra-empty"), 0700); err != nil {
		t.Fatalf("Mkdir(extra-empty) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml"), []byte("[server]\nport = 9090\n"), 0600); err != nil {
		t.Fatalf("WriteFile(restored config) error: %v", err)
	}

	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted {
		t.Fatalf("RunRestoreVerify status = %q, want %q", verify.Status, StatusCompleted)
	}
	if verify.SnapshotPath != restore.SnapshotPath || verify.ManifestPath != restore.ManifestPath {
		t.Fatalf("RunRestoreVerify reference = (%q, %q), want restore reference (%q, %q)", verify.SnapshotPath, verify.ManifestPath, restore.SnapshotPath, restore.ManifestPath)
	}
	assertWarningsContain(t, verify.Warnings, "恢复目标文件校验失败: docs/note.txt")
	assertWarningsContain(t, verify.Warnings, "恢复目标包含对照备份未登记的文件: extra.txt")
	assertWarningsContain(t, verify.Warnings, "恢复目标包含对照备份未登记的目录: extra-empty")
	assertWarningsContain(t, verify.Warnings, "恢复目标缺少对照备份目录: empty-missing")
	assertWarningsContain(t, verify.Warnings, "恢复目标根目录权限不匹配")
	assertWarningsContain(t, verify.Warnings, "恢复目标目录权限不匹配: empty-mode")
	assertWarningsContain(t, verify.Warnings, "恢复目标配置文件校验失败: .mnemonas-restore/config.toml")

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	assertWarningsContain(t, job.LastRestoreVerify.Warnings, "恢复目标文件校验失败: docs/note.txt")
}

func TestManager_RunRestoreVerifyWarnsWhenLocalTargetContainsSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	outsideFile := filepath.Join(tmpDir, "outside.txt")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "original")
	mustWriteFile(t, outsideFile, "outside")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget}); err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(restoreTarget, "docs", "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	assertWarningsContain(t, verify.Warnings, "恢复目标包含符号链接: docs/outside-link")
}

func TestManager_RunRestoreVerifyUsesRestoreSnapshotWhenNewerBackupExists(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "first")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 20, 1, 2, 3, 0, time.UTC)
	manager.now = func() time.Time { return now }

	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	restore, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.SnapshotPath != firstRun.SnapshotPath {
		t.Fatalf("restore snapshot = %q, want first run snapshot %q", restore.SnapshotPath, firstRun.SnapshotPath)
	}
	if restore.ManifestSize != 0 || restore.ManifestDigest != "" {
		t.Fatalf("public restore exposed manifest evidence: %+v", restore)
	}
	manager.mu.Lock()
	recordedRestore := cloneRestoreResultRaw(manager.state.Jobs["home"].LastRestore)
	manager.mu.Unlock()
	if recordedRestore == nil || recordedRestore.ManifestPath != firstRun.ManifestPath || recordedRestore.ManifestSize <= 0 || recordedRestore.ManifestDigest == "" {
		t.Fatalf("persisted restore lacks trusted first-run manifest evidence: %+v", recordedRestore)
	}

	now = now.Add(time.Minute)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "second")
	secondRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}
	if secondRun.SnapshotPath == firstRun.SnapshotPath {
		t.Fatalf("second RunJob reused first snapshot path %q", secondRun.SnapshotPath)
	}

	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted {
		t.Fatalf("RunRestoreVerify status = %q, want %q", verify.Status, StatusCompleted)
	}
	if verify.SnapshotPath != firstRun.SnapshotPath || verify.ManifestPath != firstRun.ManifestPath {
		t.Fatalf("RunRestoreVerify reference = (%q, %q), want first run reference (%q, %q)", verify.SnapshotPath, verify.ManifestPath, firstRun.SnapshotPath, firstRun.ManifestPath)
	}
	assertWarningsNotContain(t, verify.Warnings, "恢复目标文件校验失败: docs/note.txt")
}

func TestManager_LatestCompletedRestoreForTargetRequiresCanonicalTarget(t *testing.T) {
	manager := &Manager{
		state: persistedState{
			Jobs: map[string]JobState{
				"home": {
					LastRestore: &RestoreResult{
						Status:       StatusCompleted,
						TargetPath:   "/restore/parent/../new",
						SnapshotPath: "/backups/snapshots/20260520",
					},
				},
			},
		},
	}

	if restore := manager.latestCompletedRestoreForTarget("home", "/restore/new"); restore != nil {
		t.Fatalf("latestCompletedRestoreForTarget() = %+v, want nil for noncanonical target mismatch", restore)
	}
}

func TestManager_RunRestoreVerifyWarnsWhenRestoredConfigPathIsDirectory(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	configPath := filepath.Join(tmpDir, "mnemonas.toml")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restore")
	mustWriteFile(t, configPath, "[server]\nport = 8080\n")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		ConfigPath:  configPath,
		Jobs: []config.BackupJobConfig{{
			ID:            "home",
			Name:          "Home backup",
			Type:          JobTypeLocal,
			Source:        source,
			Destination:   destination,
			IncludeConfig: true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget, IncludeConfig: true}); err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	restoredConfigPath := filepath.Join(restoreTarget, ".mnemonas-restore", "config.toml")
	if err := os.Remove(restoredConfigPath); err != nil {
		t.Fatalf("Remove(restored config) error: %v", err)
	}
	if err := os.Mkdir(restoredConfigPath, 0700); err != nil {
		t.Fatalf("Mkdir(restored config path) error: %v", err)
	}

	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.ConfigFound {
		t.Fatalf("RunRestoreVerify config found = true, want false for directory path")
	}
	assertWarningsContain(t, verify.Warnings, "检查配置文件失败")
	assertWarningsContain(t, verify.Warnings, "not a regular file")
}

func TestManager_RunRestorePreviewRejectsUnsafeManifestArchivePath(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestArchivePath(t, run.ManifestPath, "data/../escape.txt")

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore-target"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsUnsafeManifestSize(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestEntrySize(t, run.ManifestPath, -1)

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore-target"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsMismatchedManifestJobID(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestIdentity(t, run.ManifestPath, "other-job", run.ID)

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore-target"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsStateManifestOutsideDestination(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")

	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "hello backup")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	outsideSnapshot := filepath.Join(tmpDir, "outside-snapshot")
	if err := os.MkdirAll(outsideSnapshot, 0700); err != nil {
		t.Fatalf("MkdirAll(outsideSnapshot) error: %v", err)
	}
	manifestData, err := os.ReadFile(run.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	outsideManifest := filepath.Join(outsideSnapshot, manifestFileName)
	if err := os.WriteFile(outsideManifest, manifestData, 0600); err != nil {
		t.Fatalf("WriteFile(outside manifest) error: %v", err)
	}

	manager.mu.Lock()
	state := manager.state.Jobs["home"]
	state.LastSuccessfulRun.SnapshotPath = outsideSnapshot
	state.LastSuccessfulRun.ManifestPath = outsideManifest
	manager.state.Jobs["home"] = state
	manager.mu.Unlock()

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsBrokenSymlinkManifest(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}
	if _, err := os.Stat(firstRun.ManifestPath); err != nil {
		t.Fatalf("Stat(first manifest) error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := os.Remove(run.ManifestPath); err != nil {
		t.Fatalf("Remove(manifest) error: %v", err)
	}
	if err := os.Symlink(filepath.Join(tmpDir, "missing-manifest.json"), run.ManifestPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsSymlinkedLatestSnapshotWithoutFallback(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	outside := filepath.Join(tmpDir, "outside-empty")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatalf("Mkdir(outside) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := os.RemoveAll(run.SnapshotPath); err != nil {
		t.Fatalf("RemoveAll(snapshot) error: %v", err)
	}
	if err := os.Symlink(outside, run.SnapshotPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{
		TargetPath: filepath.Join(tmpDir, "restore"),
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunRestorePreviewRejectsUnmanifestedSnapshotFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(run.SnapshotPath, "data", "extra.txt"), "unexpected")

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestorePreviewIgnoresUntrustedSnapshotRootEntry(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(destination, "home", "snapshots", "unexpected.txt"), "unexpected")

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	if _, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget}); err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestorePreviewIgnoresUntrustedSnapshotRootDirectory(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(destination, "home", "snapshots", "bad-run"), 0700); err != nil {
		t.Fatalf("Mkdir(bad snapshot) error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	if _, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget}); err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestorePreviewRejectsLatestSnapshotMissingManifestWithoutFallback(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := os.Remove(run.ManifestPath); err != nil {
		t.Fatalf("Remove(manifest) error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestorePreviewRejectsMissingLatestSnapshotWithoutFallback(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := removeAllBackupPath(run.SnapshotPath, "backup snapshot"); err != nil {
		t.Fatalf("removeAllBackupPath(snapshot) error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestoreRejectsMissingLatestSnapshotWithoutFallback(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := removeAllBackupPath(run.SnapshotPath, "backup snapshot"); err != nil {
		t.Fatalf("removeAllBackupPath(snapshot) error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || job.LastRestore.TargetPath != restoreTarget {
		t.Fatalf("failed restore was not persisted: %+v", job.LastRestore)
	}
}

func TestManager_RunRestoreDrillRejectsMissingLatestSnapshotWithoutFallback(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	firstRun, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() first error: %v", err)
	}

	now = now.Add(time.Second)
	mustWriteFile(t, filepath.Join(source, "newer.txt"), "newer")
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() second error: %v", err)
	}
	if run.ID == firstRun.ID {
		t.Fatalf("second run id = first run id %q", run.ID)
	}
	if err := removeAllBackupPath(run.SnapshotPath, "backup snapshot"); err != nil {
		t.Fatalf("removeAllBackupPath(snapshot) error: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrUnsafePath", err)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryUnsafePath {
		t.Fatalf("failed restore drill was not persisted as unsafe path: %+v", job.LastRestoreDrill)
	}
}

func TestManager_RunRestoreRejectsUnsafeManifestSize(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestEntrySize(t, run.ManifestPath, -1)

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestoreRejectsUnsafeManifestArchivePath(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestArchivePath(t, run.ManifestPath, "data/../escape.txt")

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || job.LastRestore.TargetPath != restoreTarget {
		t.Fatalf("failed restore was not persisted: %+v", job.LastRestore)
	}
}

func TestManager_RunRestoreDrillRejectsUnsafeManifestSize(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestEntrySize(t, run.ManifestPath, -1)

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrUnsafePath", err)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryUnsafePath {
		t.Fatalf("failed restore drill was not persisted as unsafe path: %+v", job.LastRestoreDrill)
	}
}

func TestManager_RunRestoreDrillRejectsUnsafeManifestArchivePath(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestArchivePath(t, run.ManifestPath, "data/../escape.txt")

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrUnsafePath", err)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryUnsafePath {
		t.Fatalf("failed restore drill was not persisted as unsafe path: %+v", job.LastRestoreDrill)
	}
}

func TestManager_RunRestoreDrillRejectsSnapshotModeMismatch(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if err := os.Chmod(filepath.Join(run.SnapshotPath, "data", "note.txt"), 0644); err != nil {
		t.Fatalf("Chmod(snapshot file) error: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if err == nil || !strings.Contains(err.Error(), "mode mismatch") {
		t.Fatalf("RunRestoreDrill() error = %v, want mode mismatch", err)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryIntegrityCheck {
		t.Fatalf("failed restore drill was not persisted as integrity check: %+v", job.LastRestoreDrill)
	}
}

func TestManager_RunRestoreDrillRejectsUnmanifestedSnapshotFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(run.SnapshotPath, "data", "extra.txt"), "unexpected")

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrUnsafePath", err)
	}

	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestoreDrill == nil || job.LastRestoreDrill.Status != StatusFailed || job.LastRestoreDrill.FailureCategory != FailureCategoryUnsafePath {
		t.Fatalf("failed restore drill was not persisted as unsafe path: %+v", job.LastRestoreDrill)
	}
}

func TestManager_RunRestoreRejectsUnmanifestedSnapshotFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(run.SnapshotPath, "data", "extra.txt"), "unexpected")

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestValidateRestoreTargetPathRejectsProtectedSystemDirectory(t *testing.T) {
	protectedDir := protectedSystemDirectoryForTest(t)
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}

	_, err := validateRestoreTargetPath(source, destination, source, protectedDir)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateRestoreTargetPath() error = %v, want ErrUnsafePath", err)
	}
}

func TestValidateRestoreTargetPathRejectsControlCharacters(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	storageRoot := filepath.Join(tmpDir, "storage")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "ascii control character",
			target: filepath.Join(tmpDir, "restore\nsecret"),
		},
		{
			name:   "unicode control character",
			target: filepath.Join(tmpDir, "restore\u0081secret"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateRestoreTargetPath(source, destination, storageRoot, tt.target)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("validateRestoreTargetPath() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestValidateRestoreTargetPathRejectsDotSegments(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	storageRoot := filepath.Join(tmpDir, "storage")
	for _, target := range []string{
		filepath.Join(tmpDir, "restore-parent") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "restore",
		filepath.Join(tmpDir, "restore") + string(os.PathSeparator) + "." + string(os.PathSeparator) + "target",
	} {
		_, err := validateRestoreTargetPath(source, destination, storageRoot, target)
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("validateRestoreTargetPath(%q) error = %v, want ErrUnsafePath", target, err)
		}
	}
}

func TestValidateRestoreTargetPathRejectsBackslashTargets(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	storageRoot := filepath.Join(tmpDir, "storage")
	for _, target := range []string{
		filepath.Join(tmpDir, `restore\windows`),
		`C:\restore\mnemonas`,
		`\\server\share\restore`,
	} {
		_, err := validateRestoreTargetPath(source, destination, storageRoot, target)
		if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrInvalidRestoreRequest) {
			t.Fatalf("validateRestoreTargetPath(%q) error = %v, want ErrUnsafePath and ErrInvalidRestoreRequest", target, err)
		}
	}
}

func TestManager_RunRestoreRejectsTargetSymlinkAncestor(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	realParent := filepath.Join(tmpDir, "real-parent")
	linkParent := filepath.Join(tmpDir, "restore-link")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")
	if err := os.MkdirAll(filepath.Join(realParent, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	target := filepath.Join(linkParent, "nested", "restore-target")
	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: target})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestCreateNamedRestoreTargetRejectsSwappedSymlinkParent(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	parent := filepath.Join(tmpDir, "restore-parent")
	originalParent := filepath.Join(tmpDir, "restore-parent-original")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, originalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	target := filepath.Join(parent, "restore-target")
	_, err := createNamedRestoreTarget(target, ".partial-test")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("createNamedRestoreTarget() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "restore-target.partial-test")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside staging stat error = %v, want not exist", statErr)
	}
}

func TestInstallRestoreTargetRejectsSwappedSymlinkParentBeforeRemovingTarget(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	parent := filepath.Join(tmpDir, "restore-parent")
	originalParent := filepath.Join(tmpDir, "restore-parent-original")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}

	partial := filepath.Join(parent, "restore-target.partial-test")
	if err := os.Mkdir(partial, 0700); err != nil {
		t.Fatal(err)
	}
	partialIdentity, err := os.Lstat(partial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, originalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	outsideTarget := filepath.Join(outside, "restore-target")
	if err := os.Mkdir(outsideTarget, 0700); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(parent, "restore-target")
	err = installRestoreTarget(restoreStagingTarget{Path: partial, Identity: partialIdentity}, target)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("installRestoreTarget() error = %v, want ErrUnsafePath", err)
	}
	if info, statErr := os.Stat(outsideTarget); statErr != nil || !info.IsDir() {
		t.Fatalf("outside target was altered, stat = (%v, %v)", info, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(originalParent, "restore-target.partial-test")); statErr != nil {
		t.Fatalf("original partial stat error = %v", statErr)
	}
}

func TestMoveResticRestoredSourceRejectsSwappedPartialSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	rawPath := filepath.Join(tmpDir, "raw")
	restoredSourcePath, err := resticRestoredSourcePath(rawPath, source)
	if err != nil {
		t.Fatalf("resticRestoredSourcePath() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(restoredSourcePath, "docs", "note.txt"), "restored")

	partialPath := filepath.Join(tmpDir, "restore-target.partial-test")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.Mkdir(partialPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(partialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, partialPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, _, err = moveResticRestoredSource(context.Background(), rawPath, partialPath, source)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("moveResticRestoredSource() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "docs")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside docs stat error = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(restoredSourcePath, "docs", "note.txt")); statErr != nil {
		t.Fatalf("restic restored source was moved on failure: %v", statErr)
	}
}

func TestMoveResticRestoredSourceRejectsRestoredSymlinkBeforeMove(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	rawPath := filepath.Join(tmpDir, "raw")
	restoredSourcePath, err := resticRestoredSourcePath(rawPath, source)
	if err != nil {
		t.Fatalf("resticRestoredSourcePath() error: %v", err)
	}
	if err := os.MkdirAll(restoredSourcePath, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmpDir, "outside.txt")
	mustWriteFile(t, outside, "outside")
	linkPath := filepath.Join(restoredSourcePath, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	partialPath := filepath.Join(tmpDir, "restore-target.partial-test")
	if err := os.Mkdir(partialPath, 0700); err != nil {
		t.Fatal(err)
	}

	_, _, err = moveResticRestoredSource(context.Background(), rawPath, partialPath, source)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("moveResticRestoredSource() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Lstat(filepath.Join(partialPath, "outside-link")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial symlink stat error = %v, want not exist", statErr)
	}
	if _, statErr := os.Lstat(linkPath); statErr != nil {
		t.Fatalf("restic restored source symlink was moved on failure: %v", statErr)
	}
}

func TestMoveResticRestoredSourceRejectsUnsafeEntryNames(t *testing.T) {
	tests := []struct {
		name      string
		entryName string
	}{
		{name: "backslash", entryName: "nested\\report.txt"},
		{name: "newline", entryName: "report\n2026.txt"},
		{name: "delete-control", entryName: "report\x7f.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			rawPath := filepath.Join(tmpDir, "raw")
			restoredSourcePath, err := resticRestoredSourcePath(rawPath, source)
			if err != nil {
				t.Fatalf("resticRestoredSourcePath() error: %v", err)
			}
			if err := os.MkdirAll(restoredSourcePath, 0700); err != nil {
				t.Fatal(err)
			}
			unsafePath := filepath.Join(restoredSourcePath, tt.entryName)
			if err := os.WriteFile(unsafePath, []byte("restored"), 0600); err != nil {
				t.Skipf("platform does not support unsafe filename %q: %v", tt.entryName, err)
			}
			partialPath := filepath.Join(tmpDir, "restore-target.partial-test")
			if err := os.Mkdir(partialPath, 0700); err != nil {
				t.Fatal(err)
			}

			_, _, err = moveResticRestoredSource(context.Background(), rawPath, partialPath, source)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("moveResticRestoredSource() error = %v, want ErrUnsafePath", err)
			}
			if _, statErr := os.Stat(unsafePath); statErr != nil {
				t.Fatalf("unsafe restored source entry was moved on failure: %v", statErr)
			}
			if entries, readErr := os.ReadDir(partialPath); readErr != nil {
				t.Fatalf("ReadDir(partialPath) error: %v", readErr)
			} else if len(entries) != 0 {
				t.Fatalf("expected empty partial target after unsafe entry failure, got %d entries", len(entries))
			}
		})
	}
}

func TestManager_RunRestoreCleanupRejectsSwappedSymlinkParent(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	restoreParent := filepath.Join(tmpDir, "restore-parent")
	originalParent := filepath.Join(tmpDir, "restore-parent-original")
	outside := filepath.Join(tmpDir, "outside")
	restoreTime := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	restoreID := formatRunID(restoreTime)
	restoreTarget := filepath.Join(restoreParent, "restore-target")
	outsidePartial := filepath.Join(outside, "restore-target.partial-"+restoreID)
	outsideSentinel := filepath.Join(outsidePartial, "sentinel.txt")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")
	mustWriteFile(t, outsideSentinel, "keep")
	if err := os.MkdirAll(restoreParent, 0700); err != nil {
		t.Fatal(err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	backupTime := restoreTime.Add(-time.Minute)
	manager.now = func() time.Time { return backupTime }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	manager.now = func() time.Time { return restoreTime }

	oldHook := afterCopyOpenFileBeforeMetadata
	var hookErr error
	hookRan := false
	afterCopyOpenFileBeforeMetadata = func(path string) {
		if hookRan || !strings.HasPrefix(path, restoreTarget+".partial-"+restoreID) {
			return
		}
		hookRan = true
		if err := os.Rename(restoreParent, originalParent); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(outside, restoreParent)
	}
	defer func() {
		afterCopyOpenFileBeforeMetadata = oldHook
	}()

	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if hookErr != nil {
		t.Skipf("symlink unavailable: %v", hookErr)
	}
	if !hookRan {
		t.Fatal("restore copy hook did not run")
	}
	if err == nil {
		t.Fatal("RunRestore() error = nil, want failure after parent swap")
	}
	if data, readErr := os.ReadFile(outsideSentinel); readErr != nil || string(data) != "keep" {
		t.Fatalf("outside partial sentinel was altered, read = (%q, %v)", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(originalParent, "restore-target.partial-"+restoreID)); statErr != nil {
		t.Fatalf("original partial stat error = %v", statErr)
	}
}

func TestManager_RunRestoreBlocksFailedPreflight(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "large.bin"), strings.Repeat("x", 32))

	oldAvailableBytesFunc := restoreAvailableBytesFunc
	restoreAvailableBytesFunc = func(string) (int64, error) {
		return 1, nil
	}
	defer func() {
		restoreAvailableBytesFunc = oldAvailableBytesFunc
	}()

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if firstFailedRestorePreflight(preview.PreflightChecks) == nil {
		t.Fatalf("preview preflight did not fail capacity check: %+v", preview.PreflightChecks)
	}

	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestore() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore == nil || job.LastRestore.Status != StatusFailed || firstFailedRestorePreflight(job.LastRestore.PreflightChecks) == nil {
		t.Fatalf("failed preflight restore was not persisted: %+v", job.LastRestore)
	}
}

func TestManager_RunRestorePreviewChecksCapacityOnExistingEmptyTarget(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	oldAvailableBytesFunc := restoreAvailableBytesFunc
	var probedPaths []string
	restoreAvailableBytesFunc = func(path string) (int64, error) {
		probedPaths = append(probedPaths, path)
		return 1 << 30, nil
	}
	t.Cleanup(func() {
		restoreAvailableBytesFunc = oldAvailableBytesFunc
	})

	missingTarget := filepath.Join(tmpDir, "missing-target")
	if _, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: missingTarget}); err != nil {
		t.Fatalf("RunRestorePreview() missing target error: %v", err)
	}
	if got, want := probedPaths[len(probedPaths)-1], filepath.Dir(missingTarget); got != want {
		t.Fatalf("missing target capacity probe path = %q, want %q", got, want)
	}

	existingTarget := filepath.Join(tmpDir, "existing-empty-target")
	if err := os.Mkdir(existingTarget, 0700); err != nil {
		t.Fatalf("Mkdir(existingTarget) error: %v", err)
	}
	if _, err := manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: existingTarget}); err != nil {
		t.Fatalf("RunRestorePreview() existing target error: %v", err)
	}
	if got := probedPaths[len(probedPaths)-1]; got != existingTarget {
		t.Fatalf("existing target capacity probe path = %q, want %q", got, existingTarget)
	}
}

func TestManager_RunBatchRestorePreviewAndRestore(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "batch restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreA := filepath.Join(tmpDir, "restore-a")
	restoreARaw := restoreA + string(os.PathSeparator)
	preview, err := manager.RunBatchRestorePreview(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreARaw},
			{JobID: "home", TargetPath: filepath.Join(restoreA, "nested")},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || !preview.Warning || len(preview.Items) != 2 {
		t.Fatalf("unexpected batch preview outcome: %+v", preview)
	}
	if preview.Items[0].Status != StatusCompleted || preview.Items[0].Preview == nil {
		t.Fatalf("first batch preview item = %+v, want completed preview", preview.Items[0])
	}
	if preview.Items[1].Status != StatusFailed || !strings.Contains(preview.Items[1].ErrorMessage, "conflicts") {
		t.Fatalf("second batch preview item = %+v, want target conflict", preview.Items[1])
	}
	if preview.Items[0].TargetPath != restoreA || preview.Items[0].Preview.TargetPath != restoreA {
		t.Fatalf("first batch preview target paths = %q/%q, want normalized path %q", preview.Items[0].TargetPath, preview.Items[0].Preview.TargetPath, restoreA)
	}

	restoreB := filepath.Join(tmpDir, "restore-b")
	restoreBRaw := restoreB + string(os.PathSeparator)
	restore, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreA},
			{JobID: "home", TargetPath: restoreBRaw},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || len(restore.Items) != 2 {
		t.Fatalf("unexpected batch restore outcome: %+v", restore)
	}
	for _, item := range restore.Items {
		if item.Status != StatusCompleted || item.Restore == nil || item.Verify == nil {
			t.Fatalf("batch restore item = %+v, want completed restore and verify", item)
		}
		assertFileContent(t, filepath.Join(item.TargetPath, "docs", "note.txt"), "batch restore")
	}
	if restore.Items[1].TargetPath != restoreB {
		t.Fatalf("second batch restore target path = %q, want normalized path %q", restore.Items[1].TargetPath, restoreB)
	}
	if restore.Items[1].Restore.TargetPath != restoreB || restore.Items[1].Verify.TargetPath != restoreB {
		t.Fatalf("second batch restore nested target paths = %q/%q, want %q", restore.Items[1].Restore.TargetPath, restore.Items[1].Verify.TargetPath, restoreB)
	}

	restoreC := filepath.Join(tmpDir, "restore-c")
	preflightFailed, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreC},
			{JobID: "home", TargetPath: filepath.Join(restoreC, "nested")},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() preflight error: %v", err)
	}
	if preflightFailed.Status != StatusFailed || !preflightFailed.Warning || len(preflightFailed.Items) != 2 {
		t.Fatalf("unexpected preflight-failed batch restore outcome: %+v", preflightFailed)
	}
	if preflightFailed.Items[0].Status != StatusFailed || preflightFailed.Items[0].Restore != nil || preflightFailed.Items[0].Verify != nil || !strings.Contains(preflightFailed.Items[0].ErrorMessage, "preflight failed before this item started") {
		t.Fatalf("first preflight-failed batch item = %+v, want not-started failure", preflightFailed.Items[0])
	}
	if preflightFailed.Items[1].Status != StatusFailed || !strings.Contains(preflightFailed.Items[1].ErrorMessage, "conflicts") {
		t.Fatalf("second preflight-failed batch item = %+v, want target conflict", preflightFailed.Items[1])
	}
	if _, err := os.Stat(restoreC); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight-failed batch restore wrote target before all items passed: stat error = %v", err)
	}
}

func TestManager_BatchRestorePropagatesTargetLockReleaseFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "batch restore")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	originalClose := closeBackupTargetLock
	t.Cleanup(func() { closeBackupTargetLock = originalClose })
	closeErr := errors.New("injected batch target lock release failure")
	closeBackupTargetLock = func(lock *backupStateLock) error {
		return errors.Join(lock.Close(), closeErr)
	}
	opts := BatchRestoreOptions{Items: []BatchRestoreItemOptions{{
		JobID:      "home",
		TargetPath: filepath.Join(tmpDir, "restore"),
	}}}

	preview, err := manager.RunBatchRestorePreview(context.Background(), opts)
	if !errors.Is(err, ErrNoSnapshots) || !errors.Is(err, ErrBackupTargetLockRelease) || !errors.Is(err, closeErr) {
		t.Fatalf("RunBatchRestorePreview() error = %v, want business and infrastructure causes", err)
	}
	if preview == nil || preview.Status != StatusFailed || len(preview.Items) != 1 || preview.Items[0].Status != StatusFailed {
		t.Fatalf("RunBatchRestorePreview() result = %+v, want failed item details", preview)
	}

	result, err := manager.RunBatchRestore(context.Background(), opts)
	if !errors.Is(err, ErrBackupTargetLockRelease) || !errors.Is(err, closeErr) {
		t.Fatalf("RunBatchRestore() error = %v, want target lock release failure", err)
	}
	if result == nil || result.Status != StatusFailed || len(result.Items) != 1 || result.Items[0].Status != StatusFailed {
		t.Fatalf("RunBatchRestore() result = %+v, want failed preflight details", result)
	}
}

func TestManager_RunBatchRestoreReportsTargetCreatedAfterPreflight(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "batch restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreA := filepath.Join(tmpDir, "restore-a")
	restoreB := filepath.Join(tmpDir, "restore-b", "token=restore-secret")
	if err := os.MkdirAll(filepath.Dir(restoreB), 0700); err != nil {
		t.Fatalf("MkdirAll(restoreB parent) error: %v", err)
	}
	oldHook := afterBatchRestorePreflightPassed
	hookRan := false
	afterBatchRestorePreflightPassed = func(preview *BatchRestorePreviewResult) {
		if hookRan || preview == nil || preview.Status != StatusCompleted {
			return
		}
		hookRan = true
		mustWriteFile(t, filepath.Join(restoreB, "occupied.txt"), "already here")
	}
	defer func() {
		afterBatchRestorePreflightPassed = oldHook
	}()

	restore, err := manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreA},
			{JobID: "home", TargetPath: restoreB},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() error: %v", err)
	}
	if !hookRan {
		t.Fatal("batch restore preflight hook did not run")
	}
	if restore.Status != StatusCompleted || !restore.Warning || restore.ErrorMessage != "1 of 2 batch restore items failed" {
		t.Fatalf("unexpected batch restore outcome: %+v", restore)
	}
	if len(restore.Items) != 2 {
		t.Fatalf("batch restore item count = %d, want 2", len(restore.Items))
	}
	if restore.Items[0].Status != StatusCompleted || restore.Items[0].Restore == nil || restore.Items[0].Verify == nil {
		t.Fatalf("first batch restore item = %+v, want completed restore and verify", restore.Items[0])
	}
	assertFileContent(t, filepath.Join(restoreA, "docs", "note.txt"), "batch restore")
	if restore.Items[1].Status != StatusFailed || restore.Items[1].Restore == nil || restore.Items[1].Verify != nil || !strings.Contains(restore.Items[1].ErrorMessage, ErrRestoreTargetExists.Error()) {
		t.Fatalf("second batch restore item = %+v, want target-exists failure without verify", restore.Items[1])
	}
	assertNoBackupTargetSecrets(t, restore.Items[1].TargetPath)
	assertNoBackupTargetSecrets(t, restore.Items[1].ErrorMessage)
	assertNoBackupTargetSecrets(t, restore.Items[1].Restore.TargetPath)
	for _, warning := range restore.Warnings {
		assertNoBackupTargetSecrets(t, warning)
	}
	assertFileContent(t, filepath.Join(restoreB, "occupied.txt"), "already here")
	if _, statErr := os.Stat(filepath.Join(restoreB, "docs", "note.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("second restore target was overwritten after preflight race: stat error = %v", statErr)
	}
}

func TestManager_BatchRestoreRejectsDotSegmentTargets(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "batch restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	dotDotTarget := filepath.Join(tmpDir, "restore-parent") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "restore-a"
	_, err = manager.RunBatchRestorePreview(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{{JobID: "home", TargetPath: dotDotTarget}},
	})
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrInvalidRestoreRequest) {
		t.Fatalf("RunBatchRestorePreview() error = %v, want ErrUnsafePath and ErrInvalidRestoreRequest", err)
	}

	dotTarget := filepath.Join(tmpDir, "restore-b") + string(os.PathSeparator) + "." + string(os.PathSeparator) + "target"
	_, err = manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{{JobID: "home", TargetPath: dotTarget}},
	})
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrInvalidRestoreRequest) {
		t.Fatalf("RunBatchRestore() error = %v, want ErrUnsafePath and ErrInvalidRestoreRequest", err)
	}
}

func TestManager_BatchRestoreRejectsBackslashTargetsBeforeRunning(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "batch restore")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	restoreTarget := filepath.Join(tmpDir, "restore-valid")
	backslashTarget := filepath.Join(tmpDir, `restore\windows`)
	_, err = manager.RunBatchRestore(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: restoreTarget},
			{JobID: "home", TargetPath: backslashTarget},
		},
	})
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrInvalidRestoreRequest) {
		t.Fatalf("RunBatchRestore() error = %v, want ErrUnsafePath and ErrInvalidRestoreRequest", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestFinishBatchRestoreAggregatesPostRestoreVerifyTotals(t *testing.T) {
	startedAt := time.Date(2026, 5, 9, 4, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	result := &BatchRestoreResult{
		Status:        StatusRunning,
		StartedAt:     startedAt,
		TotalFiles:    99,
		VerifiedBytes: 99,
		Items: []BatchRestoreItemResult{
			{
				Index:   0,
				Status:  StatusCompleted,
				Restore: &RestoreResult{FileCount: 12, VerifiedBytes: 4096},
				Verify:  &RestoreVerifyResult{FileCount: 13, VerifiedBytes: 8192},
			},
			{
				Index:        1,
				Status:       StatusFailed,
				Restore:      &RestoreResult{FileCount: 30, VerifiedBytes: 16384},
				Verify:       &RestoreVerifyResult{FileCount: 31, VerifiedBytes: 32768},
				ErrorMessage: "restore failed",
			},
			{
				Index:   2,
				Status:  StatusCompleted,
				Restore: &RestoreResult{FileCount: 1, VerifiedBytes: 128},
				Verify:  &RestoreVerifyResult{FileCount: 2, VerifiedBytes: 512},
			},
		},
	}

	finishBatchRestore(result, finishedAt)

	if result.Status != StatusCompleted || !result.Warning || result.ErrorMessage != "1 of 3 batch restore items failed" {
		t.Fatalf("unexpected batch outcome: %+v", result)
	}
	if result.TotalFiles != 15 || result.VerifiedBytes != 8704 {
		t.Fatalf("batch totals = (%d files, %d bytes), want post-restore verify totals (15 files, 8704 bytes)", result.TotalFiles, result.VerifiedBytes)
	}
}

func TestBatchRestoreResultFromFailedPreflightUsesProvidedClock(t *testing.T) {
	now := time.Date(2026, 5, 13, 8, 0, 0, 0, time.UTC)

	result := batchRestoreResultFromFailedPreflight(nil, now)

	if result.ID != formatRunID(now) || !result.StartedAt.Equal(now) {
		t.Fatalf("failed preflight result time = id %q started %s, want %q/%s", result.ID, result.StartedAt, formatRunID(now), now)
	}
	if result.FinishedAt == nil || !result.FinishedAt.Equal(now) {
		t.Fatalf("failed preflight finished_at = %v, want %s", result.FinishedAt, now)
	}
}

func TestFinishBatchRestoreRejectsOverflowingSummaryTotals(t *testing.T) {
	startedAt := time.Date(2026, 5, 9, 4, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	result := &BatchRestoreResult{
		Status:    StatusRunning,
		StartedAt: startedAt,
		Items: []BatchRestoreItemResult{
			{
				Index:  0,
				Status: StatusCompleted,
				Verify: &RestoreVerifyResult{
					FileCount:     1<<63 - 1,
					VerifiedBytes: 128,
				},
			},
			{
				Index:  1,
				Status: StatusCompleted,
				Verify: &RestoreVerifyResult{
					FileCount:     1,
					VerifiedBytes: 256,
				},
			},
		},
	}

	finishBatchRestore(result, finishedAt)

	if result.Status != StatusCompleted || !result.Warning || result.ErrorMessage != "1 of 2 batch restore items failed" {
		t.Fatalf("unexpected batch outcome: %+v", result)
	}
	if result.Items[1].Status != StatusFailed || !strings.Contains(result.Items[1].ErrorMessage, "verified file count overflows int64") {
		t.Fatalf("overflowing item = %+v, want failed overflow item", result.Items[1])
	}
	if result.TotalFiles != 1<<63-1 || result.VerifiedBytes != 128 {
		t.Fatalf("batch totals = (%d files, %d bytes), want first item totals preserved", result.TotalFiles, result.VerifiedBytes)
	}
}

func TestFinishBatchRestorePreviewRejectsOverflowingSummaryTotals(t *testing.T) {
	startedAt := time.Date(2026, 5, 9, 4, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	result := &BatchRestorePreviewResult{
		Status:     StatusRunning,
		StartedAt:  startedAt,
		TotalFiles: 99,
		TotalBytes: 99,
		Items: []BatchRestorePreviewItemResult{
			{
				Index:  0,
				Status: StatusCompleted,
				Preview: &RestorePreviewResult{
					FileCount:  12,
					TotalBytes: 1<<63 - 1,
				},
			},
			{
				Index:  1,
				Status: StatusCompleted,
				Preview: &RestorePreviewResult{
					FileCount:  1,
					TotalBytes: 1,
				},
			},
		},
	}

	finishBatchRestorePreview(result, finishedAt)

	if result.Status != StatusCompleted || !result.Warning || result.ErrorMessage != "1 of 2 batch restore items failed" {
		t.Fatalf("unexpected batch preview outcome: %+v", result)
	}
	if result.Items[1].Status != StatusFailed || !strings.Contains(result.Items[1].ErrorMessage, "preview total bytes overflows int64") {
		t.Fatalf("overflowing preview item = %+v, want failed overflow item", result.Items[1])
	}
	if result.TotalFiles != 12 || result.TotalBytes != 1<<63-1 {
		t.Fatalf("batch preview totals = (%d files, %d bytes), want first item totals preserved", result.TotalFiles, result.TotalBytes)
	}
}

func TestManager_RunBatchRestoreCancelledContextDoesNotPersistSkippedRestores(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := manager.RunBatchRestore(ctx, BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{
			{JobID: "home", TargetPath: filepath.Join(tmpDir, "restore-a")},
			{JobID: "home", TargetPath: filepath.Join(tmpDir, "restore-b")},
		},
	})
	if err != nil {
		t.Fatalf("RunBatchRestore() error: %v", err)
	}
	if result.Status != StatusFailed || len(result.Items) != 2 {
		t.Fatalf("unexpected canceled batch restore result: %+v", result)
	}
	for _, item := range result.Items {
		if item.Status != StatusFailed || item.Restore != nil || item.Verify != nil || item.ErrorMessage != context.Canceled.Error() {
			t.Fatalf("canceled batch item = %+v, want failed item without restore side effects", item)
		}
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRestore != nil {
		t.Fatalf("LastRestore = %+v, want nil because canceled batch items were not started", job.LastRestore)
	}
}

func TestManager_BatchRestorePreviewRedactsExternalCommandErrors(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	commandPath := filepath.Join(tmpDir, "failing-rclone")
	remote := ":s3,access_key_id=AKIASECRET,secret_access_key=secret-access-key:bucket/path"
	stderr := "lsjson failed for " + remote + " token=remote-token"
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nprintf '%s\\n' "+shellQuote(stderr)+" >&2\nexit 1\n"), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:      "rclone-remote",
			Name:    "Rclone remote",
			Type:    JobTypeRclone,
			Source:  source,
			Remote:  remote,
			Command: commandPath,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	preview, err := manager.RunBatchRestorePreview(context.Background(), BatchRestoreOptions{
		Items: []BatchRestoreItemOptions{{
			JobID:      "rclone-remote",
			TargetPath: filepath.Join(tmpDir, "restore-target"),
		}},
	})
	if err != nil {
		t.Fatalf("RunBatchRestorePreview() error: %v", err)
	}
	if preview.Status != StatusFailed || len(preview.Items) != 1 || preview.Items[0].Status != StatusFailed {
		t.Fatalf("unexpected batch preview outcome: %+v", preview)
	}
	assertNoBackupTargetSecrets(t, preview.Items[0].ErrorMessage)
	for _, warning := range preview.Warnings {
		assertNoBackupTargetSecrets(t, warning)
	}
	if preview.Items[0].Preview == nil {
		t.Fatal("batch preview item Preview is nil")
	}
	assertNoBackupTargetSecrets(t, preview.Items[0].Preview.ErrorMessage)
}

func TestManager_JobViewRestoreDrillAndRetentionHealth(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{
			{
				ID:                     "local-unbounded",
				Name:                   "Local unbounded",
				Type:                   JobTypeLocal,
				Source:                 source,
				Destination:            filepath.Join(tmpDir, "backups"),
				ScheduleInterval:       24 * time.Hour,
				RestoreDrillStaleAfter: 24 * time.Hour,
			},
			{
				ID:              "restic-retained",
				Name:            "Restic retained",
				Type:            JobTypeRestic,
				Source:          source,
				Repository:      "rest:http://backup.example/repo",
				PasswordFile:    filepath.Join(tmpDir, "restic-password"),
				RetentionPolicy: "external: restic forget --keep-daily 7 --prune",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }

	lastBackupFinished := now.Add(-2 * time.Hour)
	staleDrillFinished := now.Add(-25 * time.Hour)
	manager.mu.Lock()
	manager.state.Jobs["local-unbounded"] = JobState{
		LastSuccessfulRun: &RunResult{
			ID:         "backup",
			JobID:      "local-unbounded",
			Status:     StatusCompleted,
			StartedAt:  lastBackupFinished.Add(-time.Minute),
			FinishedAt: &lastBackupFinished,
		},
		LastRestoreDrill: &RestoreDrillResult{
			ID:         "drill",
			JobID:      "local-unbounded",
			Status:     StatusCompleted,
			StartedAt:  staleDrillFinished.Add(-time.Minute),
			FinishedAt: &staleDrillFinished,
		},
	}
	manager.mu.Unlock()

	jobs := manager.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("job count = %d, want 2", len(jobs))
	}
	localJob := jobs[0]
	if localJob.ID != "local-unbounded" || localJob.RetentionStatus != "warning" || localJob.RestoreDrillStatus != "stale" {
		t.Fatalf("unexpected local job policy state: %+v", localJob)
	}
	if localJob.RestoreDrillStaleAfter != "24h0m0s" {
		t.Fatalf("restore drill stale after = %q, want 24h0m0s", localJob.RestoreDrillStaleAfter)
	}
	remoteJob := jobs[1]
	if remoteJob.ID != "restic-retained" || remoteJob.RetentionStatus != "ok" || remoteJob.RestoreDrillStatus != "due" {
		t.Fatalf("unexpected remote job policy state: %+v", remoteJob)
	}
	if remoteJob.RetentionPolicy != "external: restic forget --keep-daily 7 --prune" {
		t.Fatalf("retention policy = %q", remoteJob.RetentionPolicy)
	}
	if remoteJob.RestoreDrillStaleAfter != "720h0m0s" {
		t.Fatalf("default restore drill stale after = %q, want 720h0m0s", remoteJob.RestoreDrillStaleAfter)
	}
}

func TestManager_RunJobRejectsDestinationInsideSource(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(source, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
	}
}

func TestValidateDestinationRejectsFilesystemRoot(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}

	err := validateDestination(source, string(filepath.Separator), source)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateDestination() error = %v, want ErrUnsafePath", err)
	}
}

func TestValidateDestinationRejectsProtectedSystemDirectory(t *testing.T) {
	protectedDir := protectedSystemDirectoryForTest(t)
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}

	err := validateDestination(source, protectedDir, source)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateDestination() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunJobRejectsDestinationSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	realDestination := filepath.Join(tmpDir, "real-destination")
	destinationLink := filepath.Join(tmpDir, "destination-link")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	if err := os.MkdirAll(realDestination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDestination, destinationLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destinationLink,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
	}
}

func TestManager_RunJobRejectsDestinationSymlinkInsertedAfterValidation(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	outside := filepath.Join(tmpDir, "outside")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}

	oldHook := afterValidateLocalBackupDestination
	var hookErr error
	afterValidateLocalBackupDestination = func(validatedDestination string) {
		if hookErr != nil || validatedDestination != destination {
			return
		}
		hookErr = os.Symlink(outside, destination)
	}
	defer func() {
		afterValidateLocalBackupDestination = oldHook
	}()

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if hookErr != nil {
		t.Skipf("symlink unavailable: %v", hookErr)
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "home")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected outside destination to remain untouched, got %v", statErr)
	}
}

func TestCopySourceTreeRejectsDestinationSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	outside := filepath.Join(tmpDir, "outside")
	destinationLink := filepath.Join(tmpDir, "data-link")
	if err := os.MkdirAll(filepath.Join(source, "empty-dir"), 0700); err != nil {
		t.Fatalf("MkdirAll(source/empty-dir) error: %v", err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatalf("Mkdir(outside) error: %v", err)
	}
	if err := os.Symlink(outside, destinationLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, _, _, err := copySourceTree(context.Background(), source, destinationLink, nil)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("copySourceTree() error = %v, want %v", err, ErrUnsafePath)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "empty-dir")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside directory stat error = %v, want not exist", statErr)
	}
}

func TestCopySourceTreeRejectsUnsafeSourceEntryNames(t *testing.T) {
	tests := []struct {
		name      string
		entryName string
	}{
		{name: "backslash", entryName: "nested\\report.txt"},
		{name: "newline", entryName: "report\n2026.txt"},
		{name: "delete-control", entryName: "report\x7f.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := secureBackupTestTempDir(t)
			source := filepath.Join(tmpDir, "source")
			destination := filepath.Join(tmpDir, "data")
			if err := os.MkdirAll(source, 0700); err != nil {
				t.Fatalf("MkdirAll(source) error: %v", err)
			}
			if err := os.WriteFile(filepath.Join(source, tt.entryName), []byte("backup"), 0600); err != nil {
				t.Skipf("platform does not support unsafe filename %q: %v", tt.entryName, err)
			}

			_, _, _, err := copySourceTree(context.Background(), source, destination, nil)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("copySourceTree() error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestManager_RunRestoreDrillRejectsDestinationSymlinkInsertedAfterBackup(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	redirectedDestination := filepath.Join(tmpDir, "redirected-backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	if err := os.Rename(destination, redirectedDestination); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirectedDestination, destination); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{KeepArtifact: true})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(redirectedDestination, "home", "restore-drills")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("redirected restore drill directory stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRestorePreviewRejectsDestinationSymlinkInsertedAfterBackup(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	redirectedDestination := filepath.Join(tmpDir, "redirected-backups")
	restoreTarget := filepath.Join(tmpDir, "restore-target")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}

	if err := os.Rename(destination, redirectedDestination); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirectedDestination, destination); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err = manager.RunRestorePreview(context.Background(), "home", RestorePreviewOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRestorePreview() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(restoreTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("restore target stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunJobRejectsSourceSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
	}
}

func TestManager_RunJobCleansPartialSnapshotOnFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	runAt := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	runID := formatRunID(runAt)
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return runAt }

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
	}

	partialPath := filepath.Join(destination, "home", "snapshots", runID+".partial")
	if _, statErr := os.Stat(partialPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial snapshot stat error = %v, want not exist", statErr)
	}
}

func TestManager_RunRemoteBackupRejectsSourceSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	rcloneConfigFile := filepath.Join(tmpDir, "rclone.conf")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	mustWriteFile(t, passwordFile, "secret")
	mustWriteFile(t, rcloneConfigFile, "[backup]\ntype = local\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	commandPath, _ := newRecordingCommand(t, 0, "")

	tests := []struct {
		name string
		job  config.BackupJobConfig
	}{
		{
			name: "restic",
			job: config.BackupJobConfig{
				ID:           "restic-remote",
				Name:         "Restic remote",
				Type:         JobTypeRestic,
				Source:       source,
				Repository:   "rest:http://backup.example/repo",
				Command:      commandPath,
				PasswordFile: passwordFile,
			},
		},
		{
			name: "rclone",
			job: config.BackupJobConfig{
				ID:         "rclone-remote",
				Name:       "Rclone remote",
				Type:       JobTypeRclone,
				Source:     source,
				Remote:     "backup:mnemonas/source",
				Command:    commandPath,
				ConfigFile: rcloneConfigFile,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state-"+tt.name),
				StorageRoot: source,
				Jobs:        []config.BackupJobConfig{tt.job},
			})
			if err != nil {
				t.Fatalf("NewManager() error: %v", err)
			}

			_, err = manager.RunJob(context.Background(), tt.job.ID)
			if !errors.Is(err, ErrSourceContainsSymlink) {
				t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
			}
		})
	}
}

func TestManager_RunRemoteBackupRejectsCredentialFileInsideSource(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	resticPasswordFile := filepath.Join(source, "restic.pass")
	rcloneConfigFile := filepath.Join(source, "rclone.conf")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "backup")
	mustWriteFile(t, resticPasswordFile, "secret")
	mustWriteFile(t, rcloneConfigFile, "[remote]\ntype = local\n")
	commandPath, logPath := newRecordingCommand(t, 0, "")

	tests := []struct {
		name string
		job  config.BackupJobConfig
	}{
		{
			name: "restic password file",
			job: config.BackupJobConfig{
				ID:           "restic-remote",
				Name:         "Restic remote",
				Type:         JobTypeRestic,
				Source:       source,
				Repository:   "rest:http://backup.example/repo",
				Command:      commandPath,
				PasswordFile: resticPasswordFile,
			},
		},
		{
			name: "rclone config file",
			job: config.BackupJobConfig{
				ID:         "rclone-remote",
				Name:       "Rclone remote",
				Type:       JobTypeRclone,
				Source:     source,
				Remote:     "backup:mnemonas/source",
				Command:    commandPath,
				ConfigFile: rcloneConfigFile,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := newBackupTestManager(t, ManagerConfig{
				Root:        filepath.Join(tmpDir, "state-"+tt.name),
				StorageRoot: source,
				Jobs:        []config.BackupJobConfig{tt.job},
			})
			if err != nil {
				t.Fatalf("NewManager() error: %v", err)
			}

			_, err = manager.RunJob(context.Background(), tt.job.ID)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("RunJob() error = %v, want ErrUnsafePath", err)
			}
		})
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) > 0 {
		t.Fatalf("external command log = %q, want none", data)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile(command log) error: %v", err)
	}
}

func TestCopyHostFileWithHashRejectsSymlinkInsertedAfterStat(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	sourcePath := filepath.Join(tmpDir, "mnemonas.toml")
	outsidePath := filepath.Join(tmpDir, "outside.toml")
	destinationPath := filepath.Join(tmpDir, "snapshot", "config", "config.toml")
	mustWriteFile(t, sourcePath, "safe config")
	mustWriteFile(t, outsidePath, "outside config")
	probeLink := filepath.Join(tmpDir, "probe-link")
	if err := os.Symlink(outsidePath, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(probeLink); err != nil {
		t.Fatal(err)
	}

	oldHook := afterCopyHostFileLstat
	afterCopyHostFileLstat = func(path string) {
		if path != sourcePath {
			return
		}
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("Remove(sourcePath) error: %v", err)
		}
		if err := os.Symlink(outsidePath, sourcePath); err != nil {
			t.Fatalf("Symlink(sourcePath) error: %v", err)
		}
	}
	defer func() {
		afterCopyHostFileLstat = oldHook
	}()

	_, err := copyHostFileWithHash(context.Background(), sourcePath, destinationPath, "config/config.toml", "mnemonas.toml")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("copyHostFileWithHash() error = %v, want ErrSourceContainsSymlink", err)
	}
	if _, statErr := os.Stat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination stat error = %v, want ErrNotExist", statErr)
	}
}

func TestCopyOpenFileWithHashRejectsDestinationReplacedBeforeMetadata(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	sourcePath := filepath.Join(tmpDir, "source.txt")
	destinationPath := filepath.Join(tmpDir, "snapshot", "data", "source.txt")
	outsidePath := filepath.Join(tmpDir, "outside.txt")
	mustWriteFile(t, sourcePath, "source")
	mustWriteFile(t, outsidePath, "outside")
	probeLink := filepath.Join(tmpDir, "probe-link")
	if err := os.Symlink(outsidePath, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(probeLink); err != nil {
		t.Fatal(err)
	}

	source, sourceInfo, err := openRegularFileNoFollow(sourcePath, "source.txt")
	if err != nil {
		t.Fatalf("openRegularFileNoFollow() error: %v", err)
	}
	defer source.Close()

	oldHook := afterCopyOpenFileBeforeMetadata
	afterCopyOpenFileBeforeMetadata = func(path string) {
		if path != destinationPath {
			return
		}
		if err := os.Remove(destinationPath); err != nil {
			t.Fatalf("Remove(destinationPath) error: %v", err)
		}
		if err := os.Symlink(outsidePath, destinationPath); err != nil {
			t.Fatalf("Symlink(destinationPath) error: %v", err)
		}
	}
	defer func() {
		afterCopyOpenFileBeforeMetadata = oldHook
	}()

	_, err = copyOpenFileWithHash(context.Background(), source, sourceInfo, destinationPath, "data/source.txt", "source.txt")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("copyOpenFileWithHash() error = %v, want ErrUnsafePath", err)
	}
	if _, statErr := os.Lstat(destinationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination lstat error = %v, want ErrNotExist after cleanup", statErr)
	}
	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("ReadFile(outsidePath) error: %v", readErr)
	}
	if string(data) != "outside" {
		t.Fatalf("outside content = %q, want outside", data)
	}
}

func TestCopyOpenFileWithHashCleanupDoesNotFollowReplacedParentSymlink(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	sourcePath := filepath.Join(tmpDir, "source.txt")
	destinationParent := filepath.Join(tmpDir, "snapshot", "data")
	destinationPath := filepath.Join(destinationParent, "source.txt")
	originalParent := filepath.Join(tmpDir, "snapshot", "data-original")
	outsideParent := filepath.Join(tmpDir, "outside-data")
	outsidePath := filepath.Join(outsideParent, "source.txt")
	mustWriteFile(t, sourcePath, "source")
	mustWriteFile(t, outsidePath, "outside")
	probeLink := filepath.Join(tmpDir, "probe-link")
	if err := os.Symlink(outsideParent, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(probeLink); err != nil {
		t.Fatal(err)
	}

	source, sourceInfo, err := openRegularFileNoFollow(sourcePath, "source.txt")
	if err != nil {
		t.Fatalf("openRegularFileNoFollow() error: %v", err)
	}
	defer source.Close()

	oldHook := afterCopyOpenFileBeforeMetadata
	afterCopyOpenFileBeforeMetadata = func(path string) {
		if path != destinationPath {
			return
		}
		if err := os.Rename(destinationParent, originalParent); err != nil {
			t.Fatalf("Rename(destination parent) error: %v", err)
		}
		if err := os.Symlink(outsideParent, destinationParent); err != nil {
			t.Fatalf("Symlink(destination parent) error: %v", err)
		}
	}
	defer func() {
		afterCopyOpenFileBeforeMetadata = oldHook
	}()

	_, err = copyOpenFileWithHash(context.Background(), source, sourceInfo, destinationPath, "data/source.txt", "source.txt")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("copyOpenFileWithHash() error = %v, want ErrUnsafePath", err)
	}
	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("ReadFile(outsidePath) error: %v", readErr)
	}
	if string(data) != "outside" {
		t.Fatalf("outside content = %q, want outside", data)
	}
}

func TestManager_RunDueJobsRunsScheduledBackup(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "scheduled")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:               "home",
			Name:             "Home backup",
			Type:             JobTypeLocal,
			Source:           source,
			Destination:      destination,
			ScheduleInterval: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 {
		t.Fatalf("RunDueJobs() result count = %d, want 1", len(results))
	}
	if results[0].Error != "" {
		t.Fatalf("RunDueJobs() error = %s", results[0].Error)
	}
	if results[0].Result == nil || results[0].Result.Trigger != "scheduled" || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() result = %+v", results[0].Result)
	}

	results = manager.RunDueJobs(context.Background())
	if len(results) != 0 {
		t.Fatalf("RunDueJobs() immediate second result count = %d, want 0", len(results))
	}

	now = now.Add(time.Hour)
	results = manager.RunDueJobs(context.Background())
	if len(results) != 1 {
		t.Fatalf("RunDueJobs() after interval result count = %d, want 1", len(results))
	}
}

func TestManager_RunDueJobsUsesOneTickTimeForFirstRun(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "scheduled")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:               "home",
			Name:             "Home backup",
			Type:             JobTypeLocal,
			Source:           source,
			Destination:      filepath.Join(tmpDir, "backups"),
			ScheduleInterval: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	firstTick := time.Date(2026, 7, 15, 7, 30, 0, 0, time.UTC)
	clockReads := 0
	manager.now = func() time.Time {
		value := firstTick.Add(time.Duration(clockReads) * time.Nanosecond)
		clockReads++
		return value
	}

	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 || results[0].Error != "" || results[0].Result == nil || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() first-run result = %+v, want one completed job", results)
	}
	if !results[0].DueAt.Equal(firstTick) {
		t.Fatalf("RunDueJobs() due_at = %v, want first tick %v", results[0].DueAt, firstTick)
	}
}

func TestManager_RunDueJobsRetriesAfterScheduleMarkerPersistenceFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "scheduled")
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:               "home",
			Name:             "Home backup",
			Type:             JobTypeLocal,
			Source:           source,
			Destination:      destination,
			ScheduleInterval: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	persistErr := errors.New("injected schedule marker persistence failure")
	failMarker := true
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		state, ok := value.(persistedState)
		if ok && failMarker {
			jobState := state.Jobs["home"]
			if jobState.LastScheduledRunAt != nil && jobState.LastRun != nil && jobState.LastRun.Status == StatusRunning && jobState.LastRun.Trigger == "scheduled" {
				return persistErr
			}
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 || results[0].Error == "" || results[0].Result == nil || results[0].Result.Status != StatusFailed {
		t.Fatalf("RunDueJobs() failed marker result = %+v, want surfaced error without backup side effect", results)
	}
	manager.mu.Lock()
	lastScheduled := cloneTime(manager.state.Jobs["home"].LastScheduledRunAt)
	manager.mu.Unlock()
	if lastScheduled != nil {
		t.Fatalf("LastScheduledRunAt = %v after failed persistence, want nil", lastScheduled)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup destination status error = %v, want no scheduled side effect", err)
	}

	failMarker = false
	results = manager.RunDueJobs(context.Background())
	if len(results) != 1 || results[0].Error != "" || results[0].Result == nil || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() retry result = %+v, want completed backup", results)
	}
}

func TestManager_RunDueJobsRespectsScheduleWindow(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "windowed")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                  "home",
			Name:                "Home backup",
			Type:                JobTypeLocal,
			Source:              source,
			Destination:         destination,
			ScheduleInterval:    time.Hour,
			ScheduleWindowStart: "02:00",
			ScheduleWindowEnd:   "03:00",
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 1, 30, 0, 0, time.Local)
	manager.now = func() time.Time { return now }

	if results := manager.RunDueJobs(context.Background()); len(results) != 0 {
		t.Fatalf("RunDueJobs() before window result count = %d, want 0", len(results))
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].ScheduleWindowStart != "02:00" || jobs[0].ScheduleWindowEnd != "03:00" {
		t.Fatalf("unexpected job window view: %+v", jobs)
	}
	if jobs[0].NextRunAt == nil || jobs[0].NextRunAt.In(time.Local).Hour() != 2 {
		t.Fatalf("next run before window = %v, want 02:00 local", jobs[0].NextRunAt)
	}

	now = time.Date(2026, 5, 9, 2, 15, 0, 0, time.Local)
	results := manager.RunDueJobs(context.Background())
	if len(results) != 1 || results[0].Result == nil || results[0].Result.Status != StatusCompleted {
		t.Fatalf("RunDueJobs() inside window result = %+v", results)
	}

	now = time.Date(2026, 5, 9, 3, 30, 0, 0, time.Local)
	if results := manager.RunDueJobs(context.Background()); len(results) != 0 {
		t.Fatalf("RunDueJobs() after window result count = %d, want 0", len(results))
	}
	jobs = manager.ListJobs()
	if jobs[0].NextRunAt == nil || jobs[0].NextRunAt.In(time.Local).Day() != 10 || jobs[0].NextRunAt.In(time.Local).Hour() != 2 {
		t.Fatalf("next run after window = %v, want next day 02:00 local", jobs[0].NextRunAt)
	}
}

func TestScheduleWindowSupportsCrossMidnight(t *testing.T) {
	job := config.BackupJobConfig{
		ScheduleWindowStart: "22:00",
		ScheduleWindowEnd:   "06:00",
	}
	for _, tt := range []struct {
		name string
		when time.Time
		want bool
	}{
		{name: "late evening", when: time.Date(2026, 5, 9, 23, 30, 0, 0, time.Local), want: true},
		{name: "early morning", when: time.Date(2026, 5, 10, 5, 30, 0, 0, time.Local), want: true},
		{name: "midday", when: time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithinScheduleWindow(job, tt.when); got != tt.want {
				t.Fatalf("isWithinScheduleWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_RunJobPrunesOldSnapshots(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")
	mustWriteFile(t, filepath.Join(source, "locked", "note.txt"), "locked")
	if err := os.Chmod(filepath.Join(source, "locked"), 0500); err != nil {
		t.Fatalf("Chmod(source locked dir) error: %v", err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(tmpDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || !entry.IsDir() || entry.Name() != "locked" {
				return nil
			}
			_ = os.Chmod(path, 0700)
			return nil
		})
	})

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:           "home",
			Name:         "Home backup",
			Type:         JobTypeLocal,
			Source:       source,
			Destination:  destination,
			MaxSnapshots: 2,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	first, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	second, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	third, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("third RunJob() error: %v", err)
	}
	if third.PrunedSnapshots != 1 {
		t.Fatalf("third pruned snapshots = %d, want 1", third.PrunedSnapshots)
	}
	if _, err := os.Stat(first.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first snapshot stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(second.SnapshotPath); err != nil {
		t.Fatalf("second snapshot stat error = %v", err)
	}
	if _, err := os.Stat(third.SnapshotPath); err != nil {
		t.Fatalf("third snapshot stat error = %v", err)
	}
}

func TestManager_RunRetentionCheckLocalReportsSnapshotRange(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "v1")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:           "home",
			Name:         "Home backup",
			Type:         JobTypeLocal,
			Source:       source,
			Destination:  destination,
			MaxSnapshots: 3,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("first RunJob() error: %v", err)
	}
	now = now.Add(time.Hour)
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("second RunJob() error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || check.Warning || check.SnapshotCount != 2 {
		t.Fatalf("unexpected retention check: %+v", check)
	}
	if check.OldestSnapshotAt == nil || check.LatestSnapshotAt == nil || !check.LatestSnapshotAt.After(*check.OldestSnapshotAt) {
		t.Fatalf("snapshot range not populated: %+v", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.ID != check.ID || job.RetentionStatus != "ok" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsUnsafeManifest(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestEntrySize(t, run.ManifestPath, -1)

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.RetentionStatus != "failed" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsSymlinkManifest(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	manifestData, err := os.ReadFile(run.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error: %v", err)
	}
	outsideManifest := filepath.Join(tmpDir, "manifest-outside.json")
	if err := os.WriteFile(outsideManifest, manifestData, 0600); err != nil {
		t.Fatalf("WriteFile(outside manifest) error: %v", err)
	}
	if err := os.Remove(run.ManifestPath); err != nil {
		t.Fatalf("Remove(manifest) error: %v", err)
	}
	if err := os.Symlink(outsideManifest, run.ManifestPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.RetentionStatus != "failed" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsMismatchedManifestRunID(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestIdentity(t, run.ManifestPath, "home", "other-run")

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.RetentionStatus != "failed" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsMismatchedManifestCreatedAt(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	corruptManifestCreatedAt(t, run.ManifestPath, run.StartedAt.Add(24*time.Hour))

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.RetentionStatus != "failed" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsUnmanifestedSnapshotFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	run, err := manager.RunJob(context.Background(), "home")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(run.SnapshotPath, "data", "extra.txt"), "unexpected")

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
	job, err := manager.GetJob("home")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRetentionCheck == nil || job.LastRetentionCheck.Status != StatusFailed || job.RetentionStatus != "failed" {
		t.Fatalf("job retention view = %+v", job)
	}
}

func TestManager_RunRetentionCheckLocalRejectsUnexpectedSnapshotRootEntry(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(destination, "home", "snapshots", "unexpected.txt"), "unexpected")

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
}

func TestManager_RunRetentionCheckLocalRejectsPartialSnapshotFile(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(destination, "home", "snapshots", "unexpected.partial"), "unexpected")

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
}

func TestManager_RunRetentionCheckLocalRejectsInvalidSnapshotRunID(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "retention")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	snapshotPath := filepath.Join(destination, "home", "snapshots", "bad-run")
	if err := os.MkdirAll(snapshotPath, 0700); err != nil {
		t.Fatalf("MkdirAll(bad snapshot) error: %v", err)
	}
	manifest := Manifest{
		Version:     manifestVersion,
		JobID:       "home",
		RunID:       "bad-run",
		FileCount:   0,
		TotalBytes:  0,
		Directories: testManifestDirectories(),
	}
	if err := writeJSONFile(filepath.Join(snapshotPath, manifestFileName), manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "home")
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("RunRetentionCheck() error = %v, want ErrUnsafePath", err)
	}
	if check == nil || check.Status != StatusFailed {
		t.Fatalf("retention check = %+v, want failed result", check)
	}
}

func TestManager_RemoteRunResultAndNotificationRedactTargetSecrets(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath := filepath.Join(tmpDir, "failing-restic")
	repository := "rest:https://user:repo-pass@backup.example/repo?token=remote-token&region=us"
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteFile(t, passwordFile, "secret")
	stderr := "failed repository " + repository + " with access_key_id=AKIASECRET"
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nprintf '%s\\n' "+shellQuote(stderr)+" >&2\nexit 1\n"), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	notifier := &recordingNotifier{}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:           "restic-remote",
			Name:         "Restic remote",
			Type:         JobTypeRestic,
			Source:       source,
			Repository:   repository,
			Command:      commandPath,
			PasswordFile: passwordFile,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	result, err := manager.RunJob(context.Background(), "restic-remote")
	if err == nil {
		t.Fatal("RunJob() error = nil, want failing command error")
	}
	assertNoBackupTargetSecrets(t, err.Error())
	if result == nil {
		t.Fatal("RunJob() result is nil")
	}
	assertNoBackupTargetSecrets(t, result.Destination)
	assertNoBackupTargetSecrets(t, result.ErrorMessage)
	if !strings.Contains(result.Destination, redactedBackupSecretValue) {
		t.Fatalf("run destination = %q, want redacted marker", result.Destination)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification event count = %d, want 1", len(events))
	}
	if events[0].Message != "backup run failed" || !events[0].ErrorMessagePresent || !events[0].LocationOmitted {
		t.Fatalf("unexpected backup failure notification summary: %+v", events[0])
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, events[0])
	job, err := manager.GetJob("restic-remote")
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if job.LastRun == nil {
		t.Fatal("GetJob().LastRun is nil")
	}
	assertNoBackupTargetSecrets(t, job.LastRun.ErrorMessage)
}

func TestManager_RunRetentionCheckResticParsesSnapshotsAndWarnsMissingPolicy(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath, _ := newRecordingResticCommand(t, source)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteFile(t, passwordFile, "secret")
	notifier := &recordingNotifier{}

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:           "restic-remote",
			Name:         "Restic remote",
			Type:         JobTypeRestic,
			Source:       source,
			Repository:   "rest:http://backup.example/repo",
			Command:      commandPath,
			PasswordFile: passwordFile,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	check, err := manager.RunRetentionCheck(context.Background(), "restic-remote")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || !check.Warning || check.SnapshotCount != 1 {
		t.Fatalf("unexpected restic retention check: %+v", check)
	}
	if check.OldestSnapshotAt == nil || !strings.Contains(strings.Join(check.Warnings, "\n"), "retention_policy") {
		t.Fatalf("restic retention warnings/range = %+v", check)
	}
	events := notifier.Events()
	if len(events) != 1 || events[0].Type != NotificationTypeRetention || events[0].Level != NotificationLevelWarning {
		t.Fatalf("retention notification = %+v", events)
	}
	if events[0].Message != "backup retention check completed with warnings" || events[0].WarningCount == 0 || events[0].ErrorMessagePresent || !events[0].LocationOmitted {
		t.Fatalf("unexpected retention notification summary: %+v", events[0])
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("retention notification timestamp = %s, want %s", events[0].Timestamp, now)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, events[0])
}

func TestManager_RunRetentionCheckRcloneParsesRemoteFiles(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	configFile := filepath.Join(tmpDir, "rclone.conf")
	commandPath, _ := newRecordingRcloneCommand(t)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "rclone")
	mustWriteFile(t, configFile, "[backup]\ntype = local\n")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:              "rclone-remote",
			Name:            "Rclone remote",
			Type:            JobTypeRclone,
			Source:          source,
			Remote:          "backup:mnemonas/source",
			Command:         commandPath,
			ConfigFile:      configFile,
			RetentionPolicy: "external: cloud lifecycle keeps 30 versions",
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	check, err := manager.RunRetentionCheck(context.Background(), "rclone-remote")
	if err != nil {
		t.Fatalf("RunRetentionCheck() error: %v", err)
	}
	if check.Status != StatusCompleted || check.Warning || check.FileCount != 1 || check.TotalBytes != 6 {
		t.Fatalf("unexpected rclone retention check: %+v", check)
	}
	if check.LatestSnapshotAt == nil {
		t.Fatalf("rclone latest timestamp not parsed: %+v", check)
	}
}

func TestManager_RunResticBackupUsesExternalCommand(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	passwordFile := filepath.Join(tmpDir, "restic.pass")
	commandPath, logPath := newRecordingResticCommand(t, source)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "restic")
	mustWriteFile(t, passwordFile, "secret")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                "restic-remote",
			Name:              "Restic remote",
			Type:              JobTypeRestic,
			Source:            source,
			Repository:        "rest:http://backup.example/repo",
			Command:           commandPath,
			PasswordFile:      passwordFile,
			VerifyAfterBackup: true,
			MaxSnapshots:      1,
			Exclude:           []string{"cache/**"},
			ExtraArgs:         []string{"--compression", "max"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if jobs := manager.ListJobs(); len(jobs) != 1 || jobs[0].Command != commandPath || jobs[0].Repository != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restic job view: %+v", jobs)
	}

	result, err := manager.RunJob(context.Background(), "restic-remote")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("RunJob status = %q, want %q", result.Status, StatusCompleted)
	}
	if result.Destination != "rest:http://backup.example/repo" || result.PrunedSnapshots != 0 || !result.Warning {
		t.Fatalf("unexpected RunJob result: %+v", result)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "retention_policy") {
		t.Fatalf("RunJob retention warnings = %#v, want retention_policy warning", result.Warnings)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "restic-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "restic-restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "restic-remote", RestorePreviewOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore preview result: %+v", preview)
	}
	if preview.FileCount != 1 || preview.TotalBytes != int64(len("restic restored")) || len(preview.SamplePaths) != 1 || preview.SamplePaths[0] != "docs/note.txt" {
		t.Fatalf("unexpected restore preview metrics: %+v", preview)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	restore, err := manager.RunRestore(context.Background(), "restic-remote", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || restore.TargetPath != restoreTarget || restore.ManifestPath != "rest:http://backup.example/repo" {
		t.Fatalf("unexpected restore result: %+v", restore)
	}
	if restore.FileCount != 1 || restore.VerifiedBytes != int64(len("restic restored")) {
		t.Fatalf("unexpected restore metrics: %+v", restore)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "restic restored")
	verify, err := manager.RunRestoreVerify(context.Background(), "restic-remote", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected restore verify result: %+v", verify)
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRestore == nil || jobs[0].LastRestore.ID != restore.ID || len(jobs[0].RestoreHistory) != 1 {
		t.Fatalf("restic restore audit was not recorded: %+v", jobs)
	}

	calls := readCommandCalls(t, logPath)
	if len(calls) != 7 {
		t.Fatalf("command call count = %d, want 7: %#v", len(calls), calls)
	}
	assertCommandArgs(t, calls[0], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"backup", source,
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--exclude", "cache/**",
		"--compression", "max",
	})
	assertCommandArgs(t, calls[1], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"check",
	})
	assertCommandArgs(t, calls[2], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"snapshots",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
	})
	assertCommandArgs(t, calls[3], calls[1])
	assertCommandArgs(t, calls[4], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"ls", "latest",
		"--json",
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--path", source,
		"--exclude", "cache/**",
	})
	assertCommandArgs(t, calls[5], calls[4])
	assertCommandArgs(t, calls[6], []string{
		"-r", "rest:http://backup.example/repo",
		"--password-file", passwordFile,
		"restore", "latest",
		"--target", restoreTarget + ".restic-" + restore.ID,
		"--tag", "mnemonas",
		"--tag", "job:restic-remote",
		"--path", source,
		"--exclude", "cache/**",
	})
}

func TestManager_RunRcloneBackupUsesExternalCommand(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	configFile := filepath.Join(tmpDir, "rclone.conf")
	commandPath, logPath := newRecordingRcloneCommand(t)
	mustWriteFile(t, filepath.Join(source, "docs", "note.txt"), "rclone")
	mustWriteFile(t, configFile, "[backup]\ntype = local\n")

	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                "rclone-remote",
			Name:              "Rclone remote",
			Type:              JobTypeRclone,
			Source:            source,
			Remote:            "backup:mnemonas/source",
			Command:           commandPath,
			ConfigFile:        configFile,
			VerifyAfterBackup: true,
			Exclude:           []string{"tmp/**"},
			ExtraArgs:         []string{"--fast-list"},
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	result, err := manager.RunJob(context.Background(), "rclone-remote")
	if err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if result.Status != StatusCompleted || result.Destination != "backup:mnemonas/source" {
		t.Fatalf("unexpected RunJob result: %+v", result)
	}
	if !result.Warning || len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "retention_policy") {
		t.Fatalf("RunJob retention warnings = %#v, want retention_policy warning", result.Warnings)
	}

	drill, err := manager.RunRestoreDrill(context.Background(), "rclone-remote", RestoreDrillOptions{})
	if err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if drill.Status != StatusCompleted || drill.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore drill result: %+v", drill)
	}

	restoreTarget := filepath.Join(tmpDir, "rclone-restore-target")
	preview, err := manager.RunRestorePreview(context.Background(), "rclone-remote", RestorePreviewOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestorePreview() error: %v", err)
	}
	if preview.Status != StatusCompleted || preview.TargetPath != restoreTarget || preview.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore preview result: %+v", preview)
	}
	if preview.FileCount != 1 || preview.TotalBytes != 6 || len(preview.SamplePaths) != 1 || preview.SamplePaths[0] != "docs/note.txt" {
		t.Fatalf("unexpected restore preview metrics: %+v", preview)
	}
	if _, err := os.Stat(restoreTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RunRestorePreview target stat error = %v, want not exist", err)
	}

	restore, err := manager.RunRestore(context.Background(), "rclone-remote", RestoreOptions{
		TargetPath: restoreTarget,
	})
	if err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	if restore.Status != StatusCompleted || restore.TargetPath != restoreTarget || restore.ManifestPath != "backup:mnemonas/source" {
		t.Fatalf("unexpected restore result: %+v", restore)
	}
	if restore.FileCount != 1 || restore.VerifiedBytes != int64(len("rclone")) {
		t.Fatalf("unexpected restore metrics: %+v", restore)
	}
	if _, err := os.Stat(restoreTarget); err != nil {
		t.Fatalf("restored target stat error: %v", err)
	}
	assertFileContent(t, filepath.Join(restoreTarget, "docs", "note.txt"), "rclone")
	verify, err := manager.RunRestoreVerify(context.Background(), "rclone-remote", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	if verify.Status != StatusCompleted || verify.TargetPath != restoreTarget || verify.FileCount != restore.FileCount || verify.VerifiedBytes != restore.VerifiedBytes {
		t.Fatalf("unexpected restore verify result: %+v", verify)
	}
	jobs := manager.ListJobs()
	if len(jobs) != 1 || jobs[0].LastRestore == nil || jobs[0].LastRestore.ID != restore.ID || len(jobs[0].RestoreHistory) != 1 {
		t.Fatalf("rclone restore audit was not recorded: %+v", jobs)
	}

	calls := readCommandCalls(t, logPath)
	if len(calls) != 8 {
		t.Fatalf("command call count = %d, want 8: %#v", len(calls), calls)
	}
	assertCommandArgs(t, calls[0], []string{
		"--config", configFile,
		"sync", source, "backup:mnemonas/source",
		"--create-empty-src-dirs",
		"--exclude", "tmp/**",
		"--fast-list",
	})
	assertCommandArgs(t, calls[1], []string{
		"--config", configFile,
		"check", source, "backup:mnemonas/source",
		"--one-way",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[2], []string{
		"--config", configFile,
		"lsjson", "backup:mnemonas/source",
		"--recursive",
		"--files-only",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[3], calls[1])
	assertCommandArgs(t, calls[4], []string{
		"--config", configFile,
		"lsjson", "backup:mnemonas/source",
		"--recursive",
		"--files-only",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[5], calls[4])
	assertCommandArgs(t, calls[6], []string{
		"--config", configFile,
		"copy", "backup:mnemonas/source", restoreTarget + ".partial-" + restore.ID,
		"--create-empty-src-dirs",
		"--exclude", "tmp/**",
	})
	assertCommandArgs(t, calls[7], []string{
		"--config", configFile,
		"check", "backup:mnemonas/source", restoreTarget + ".partial-" + restore.ID,
		"--one-way",
		"--exclude", "tmp/**",
	})
}

func TestRunExternalCommandIncludesStderrOnFailure(t *testing.T) {
	commandPath, _ := newRecordingCommand(t, 23, "remote denied")

	err := runExternalCommand(context.Background(), commandPath, "sync")
	if err == nil {
		t.Fatal("runExternalCommand() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "remote denied") {
		t.Fatalf("runExternalCommand() error = %v, want stderr detail", err)
	}
}

func TestManager_DisabledJobCannotRun(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
			Disabled:    true,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrJobDisabled) {
		t.Fatalf("RunJob() error = %v, want ErrJobDisabled", err)
	}
}

func TestManager_RunJobFailureNotifies(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source", "token=source-secret")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	_, err = manager.RunJob(context.Background(), "home")
	if !errors.Is(err, ErrSourceContainsSymlink) {
		t.Fatalf("RunJob() error = %v, want ErrSourceContainsSymlink", err)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeBackupRun || event.Level != NotificationLevelCritical {
		t.Fatalf("notification type/level = %s/%s, want backup_run/critical", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusFailed || event.Message != "backup run failed" {
		t.Fatalf("unexpected notification event: %+v", event)
	}
	if !event.Timestamp.Equal(now) {
		t.Fatalf("notification timestamp = %s, want %s", event.Timestamp, now)
	}
	if !event.ErrorMessagePresent || !event.LocationOmitted {
		t.Fatalf("notification summary markers = error:%v location:%v, want both true", event.ErrorMessagePresent, event.LocationOmitted)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, event)
}

func TestValidateRemoteCredentialFilesRejectsSymlinkParent(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	realCredentialDir := filepath.Join(tmpDir, "real-credentials")
	linkedCredentialDir := filepath.Join(tmpDir, "linked-credentials")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.Mkdir(realCredentialDir, 0700); err != nil {
		t.Fatalf("Mkdir(realCredentialDir) error: %v", err)
	}
	credentialPath := filepath.Join(realCredentialDir, "restic.pass")
	if err := os.WriteFile(credentialPath, []byte("secret"), 0600); err != nil {
		t.Fatalf("WriteFile(credentialPath) error: %v", err)
	}
	if err := os.Symlink(realCredentialDir, linkedCredentialDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := validateRemoteCredentialFiles(config.BackupJobConfig{
		Type:         JobTypeRestic,
		PasswordFile: filepath.Join(linkedCredentialDir, "restic.pass"),
	}, source, source)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateRemoteCredentialFiles() error = %v, want %v", err, ErrUnsafePath)
	}
}

func TestValidateRemoteCredentialFileRejectsUnicodeControlCharacters(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	storageRoot := filepath.Join(tmpDir, "storage")
	credentialPath := filepath.Join(tmpDir, "restic\u0081.pass")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatalf("MkdirAll(source) error: %v", err)
	}
	if err := os.MkdirAll(storageRoot, 0700); err != nil {
		t.Fatalf("MkdirAll(storageRoot) error: %v", err)
	}
	if err := os.WriteFile(credentialPath, []byte("secret"), 0600); err != nil {
		t.Fatalf("WriteFile(credentialPath) error: %v", err)
	}

	err := validateRemoteCredentialFile(credentialPath, "password_file", source, storageRoot)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validateRemoteCredentialFile() error = %v, want %v", err, ErrUnsafePath)
	}
}

func TestValidateRcloneConfigEvidenceDataRequiresStaticNamedRemote(t *testing.T) {
	tests := []struct {
		name        string
		remote      string
		content     string
		wantErrText string
	}{
		{
			name:        "connection string remote",
			remote:      ":s3,provider=AWS:mnemonas/backups",
			content:     "[backup]\ntype = s3\n",
			wantErrText: "named config_file section",
		},
		{
			name:        "undefined named section",
			remote:      "missing:mnemonas/backups",
			content:     "[backup]\ntype = s3\n",
			wantErrText: "is not defined",
		},
		{
			name:        "empty type",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype =\nprovider = AWS\n",
			wantErrText: "has no type",
		},
		{
			name:        "token",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = drive\ntoken = static-token\n",
			wantErrText: "token-refreshing",
		},
		{
			name:        "environment authentication",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = s3\nenv_auth = true\n",
			wantErrText: "cannot enable env_auth",
		},
		{
			name:        "environment expansion",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = local\nremote = ${BACKUP_ROOT}\n",
			wantErrText: "cannot expand environment-dependent paths",
		},
		{
			name:        "external file",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = s3\nservice_account_file = /run/secrets/account.json\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "external path",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = local\nroot_path = /srv/archive\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "password command",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\npassword_command = secret-tool lookup backup mnemonas\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "ssh agent",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\nssh_agent = true\n",
			wantErrText: "external runtime input",
		},
		{
			name:        "ssh helper",
			remote:      "backup:mnemonas/backups",
			content:     "[backup]\ntype = sftp\nssh = /usr/bin/ssh\n",
			wantErrText: "external runtime input",
		},
		{
			name:    "static named remote",
			remote:  "backup:mnemonas/backups",
			content: "[backup]\ntype = s3\nprovider = AWS\naccess_key_id = static-id\nsecret_access_key = static-secret\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRcloneConfigEvidenceData([]byte(tt.content), tt.remote)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("validateRcloneConfigEvidenceData() error: %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrUnsafePath) || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("validateRcloneConfigEvidenceData() error = %v, want ErrUnsafePath with text %q", err, tt.wantErrText)
			}
		})
	}
}

func TestValidateJobEvidenceInputsAllowsOnlyRcloneFastList(t *testing.T) {
	tests := []struct {
		arg     string
		wantErr bool
	}{
		{arg: "--fast-list"},
		{arg: " --fast-list "},
		{arg: "--fast-list=true", wantErr: true},
		{arg: "--transfers=4", wantErr: true},
		{arg: "--config=/tmp/rclone.conf", wantErr: true},
		{arg: "--password-command=secret-tool", wantErr: true},
		{arg: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			err := validateJobEvidenceInputs(config.BackupJobConfig{
				Type:       JobTypeRclone,
				Remote:     "backup:mnemonas/backups",
				ConfigFile: "/tmp/rclone.conf",
				ExtraArgs:  []string{tt.arg},
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateJobEvidenceInputs(%q) error = %v, wantErr %v", tt.arg, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("validateJobEvidenceInputs(%q) error = %v, want ErrUnsafePath", tt.arg, err)
			}
		})
	}
}

func TestSanitizedRemoteCommandEnvironmentUsesPrivateAllowlist(t *testing.T) {
	privateDir := filepath.Join(secureBackupTestTempDir(t), "private")
	environment := []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"TZ=UTC",
		"HOME=/home/operator",
		"TMPDIR=/tmp/shared",
		"TMP=/tmp/shared",
		"TEMP=/tmp/shared",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"AZURE_STORAGE_KEY=azure-secret",
		"B2_ACCOUNT_KEY=b2-secret",
		"RESTIC_PASSWORD=restic-secret",
		"RCLONE_CONFIG_BACKUP_PASS=rclone-secret",
		"SSH_AUTH_SOCK=/run/user/1000/ssh-agent",
		"HTTP_PROXY=http://proxy.example",
		"HTTPS_PROXY=https://proxy.example",
		"ALL_PROXY=socks5://proxy.example",
		"NO_PROXY=localhost",
	}

	for _, jobType := range []string{JobTypeRestic, JobTypeRclone} {
		t.Run(jobType, func(t *testing.T) {
			got := sanitizedRemoteCommandEnvironment(jobType, environment, privateDir)
			values := make(map[string]string, len(got))
			for _, entry := range got {
				name, value, found := strings.Cut(entry, "=")
				if !found {
					t.Fatalf("environment entry has no separator: %q", entry)
				}
				if _, exists := values[name]; exists {
					t.Fatalf("environment contains duplicate key %q: %#v", name, got)
				}
				values[name] = value
			}

			want := map[string]string{
				"PATH":   "/usr/bin:/bin",
				"LANG":   "en_US.UTF-8",
				"LC_ALL": "C",
				"TZ":     "UTC",
				"HOME":   privateDir,
				"TMPDIR": privateDir,
				"TMP":    privateDir,
				"TEMP":   privateDir,
			}
			if !reflect.DeepEqual(values, want) {
				t.Fatalf("sanitized environment = %#v, want %#v", values, want)
			}
		})
	}
}

func TestManager_RemoteChildProcessDoesNotReceiveCredentialEnvironment(t *testing.T) {
	for name, value := range map[string]string{
		"AWS_SECRET_ACCESS_KEY":         "aws-secret",
		"AZURE_STORAGE_KEY":             "azure-secret",
		"B2_ACCOUNT_KEY":                "b2-secret",
		"RESTIC_PASSWORD":               "restic-secret",
		"RESTIC_REPOSITORY":             "restic-repository",
		"RCLONE_CONFIG":                 "/tmp/untrusted-rclone.conf",
		"RCLONE_CONFIG_BACKUP_PASS":     "rclone-secret",
		"SSH_AUTH_SOCK":                 "/run/user/1000/ssh-agent",
		"HTTP_PROXY":                    "http://proxy.example",
		"HTTPS_PROXY":                   "https://proxy.example",
		"ALL_PROXY":                     "socks5://proxy.example",
		"NO_PROXY":                      "localhost",
		"aws_access_key_id":             "lower-aws-secret",
		"https_proxy":                   "http://lower-proxy.example",
		"rclone_config_backup_password": "lower-rclone-secret",
	} {
		t.Setenv(name, value)
	}

	baseDir := secureBackupTestTempDir(t)
	source := filepath.Join(baseDir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	commandPath := filepath.Join(baseDir, "record-environment")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nenv > \"$1\"\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}

	tests := []struct {
		name       string
		job        config.BackupJobConfig
		credential string
		content    string
	}{
		{
			name: "restic",
			job: config.BackupJobConfig{
				ID: "restic", Type: JobTypeRestic, Source: source,
				Repository: "rest:http://backup.example/repo",
			},
			credential: filepath.Join(baseDir, "restic.pass"),
			content:    "restic-file-secret",
		},
		{
			name: "rclone",
			job: config.BackupJobConfig{
				ID: "rclone", Type: JobTypeRclone, Source: source,
				Remote: "backup:mnemonas/backups",
			},
			credential: filepath.Join(baseDir, "rclone.conf"),
			content:    "[backup]\ntype = s3\naccess_key_id = static-id\nsecret_access_key = static-secret\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(tt.credential, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("WriteFile(credential) error: %v", err)
			}
			job := tt.job
			if job.Type == JobTypeRestic {
				job.PasswordFile = tt.credential
			} else {
				job.ConfigFile = tt.credential
			}
			manager := &Manager{storageRoot: source}
			environmentLog := filepath.Join(baseDir, tt.name+"-environment.log")
			privateDir := ""

			_, err := manager.withJobCredentialSnapshot(context.Background(), job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
				if executionJob.Type == JobTypeRestic {
					privateDir = filepath.Dir(executionJob.PasswordFile)
				} else {
					privateDir = filepath.Dir(executionJob.ConfigFile)
				}
				return runExternalCommand(commandCtx, commandPath, environmentLog)
			})
			if err != nil {
				t.Fatalf("withJobCredentialSnapshot() error: %v", err)
			}
			data, err := os.ReadFile(environmentLog)
			if err != nil {
				t.Fatalf("ReadFile(environment log) error: %v", err)
			}
			childEnvironment := string(data)
			for _, forbidden := range []string{
				"AWS_", "AZURE_", "B2_", "RESTIC_", "RCLONE_", "SSH_AUTH_SOCK=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=",
				"aws_", "https_proxy=", "rclone_",
			} {
				if strings.Contains(childEnvironment, forbidden) {
					t.Fatalf("child environment exposed %q: %s", forbidden, childEnvironment)
				}
			}
			for _, name := range []string{"HOME", "TMPDIR", "TMP", "TEMP"} {
				if !strings.Contains(childEnvironment, name+"="+privateDir+"\n") {
					t.Fatalf("child environment omitted private %s: %s", name, childEnvironment)
				}
			}
			if _, err := os.Stat(privateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("private credential directory stat error = %v, want not exist", err)
			}
		})
	}
}

func TestManager_RemoteCommandCannotModifyCredentialSnapshot(t *testing.T) {
	baseDir := secureBackupTestTempDir(t)
	source := filepath.Join(baseDir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}

	tests := []struct {
		name       string
		job        config.BackupJobConfig
		credential string
		original   string
		mutated    string
	}{
		{
			name: "restic",
			job: config.BackupJobConfig{
				ID: "restic", Type: JobTypeRestic, Source: source,
				Repository: "rest:http://backup.example/repo",
			},
			credential: filepath.Join(baseDir, "restic.pass"),
			original:   "restic-file-secret",
			mutated:    "changed-restic-secret",
		},
		{
			name: "rclone",
			job: config.BackupJobConfig{
				ID: "rclone", Type: JobTypeRclone, Source: source,
				Remote: "backup:mnemonas/backups",
			},
			credential: filepath.Join(baseDir, "rclone.conf"),
			original:   "[backup]\ntype = local\n",
			mutated:    "[backup]\ntype = local\ndescription = changed\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(tt.credential, []byte(tt.original), 0o600); err != nil {
				t.Fatalf("WriteFile(credential) error: %v", err)
			}
			commandPath := filepath.Join(baseDir, "mutate-"+tt.name)
			script := "#!/bin/sh\nprintf '%s' " + shellQuote(tt.mutated) + " > \"$1\"\n"
			if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
				t.Fatalf("WriteFile(command) error: %v", err)
			}
			job := tt.job
			if job.Type == JobTypeRestic {
				job.PasswordFile = tt.credential
			} else {
				job.ConfigFile = tt.credential
			}
			manager := &Manager{storageRoot: source}
			snapshotPath := ""

			_, err := manager.withJobCredentialSnapshot(context.Background(), job, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
				if executionJob.Type == JobTypeRestic {
					snapshotPath = executionJob.PasswordFile
				} else {
					snapshotPath = executionJob.ConfigFile
				}
				return runExternalCommand(commandCtx, commandPath, snapshotPath)
			})
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("withJobCredentialSnapshot() error = %v, want ErrUnsafePath", err)
			}
			data, readErr := os.ReadFile(tt.credential)
			if readErr != nil {
				t.Fatalf("ReadFile(original credential) error: %v", readErr)
			}
			if string(data) != tt.original {
				t.Fatalf("original credential changed to %q, want %q", data, tt.original)
			}
			if _, statErr := os.Stat(snapshotPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("private credential snapshot stat error = %v, want not exist", statErr)
			}
		})
	}
}

func TestManager_CredentialSnapshotResolvesSymlinkedTempDir(t *testing.T) {
	baseDir := secureBackupTestTempDir(t)
	realTempDir := filepath.Join(baseDir, "real-temp")
	linkedTempDir := filepath.Join(baseDir, "linked-temp")
	if err := os.Mkdir(realTempDir, 0o700); err != nil {
		t.Fatalf("Mkdir(real temp) error: %v", err)
	}
	if err := os.Symlink(realTempDir, linkedTempDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("TMPDIR", linkedTempDir)

	resolvedRoot, err := resolvedCredentialSnapshotTempRoot()
	if err != nil {
		t.Fatalf("resolvedCredentialSnapshotTempRoot() error: %v", err)
	}
	if resolvedRoot != realTempDir {
		t.Fatalf("resolved temp root = %q, want %q", resolvedRoot, realTempDir)
	}

	source := filepath.Join(baseDir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatalf("Mkdir(source) error: %v", err)
	}
	passwordFile := filepath.Join(baseDir, "restic.pass")
	if err := os.WriteFile(passwordFile, []byte("restic-secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(password) error: %v", err)
	}
	commandPath := filepath.Join(baseDir, "succeed")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	manager := &Manager{storageRoot: source}
	snapshotPath := ""

	_, err = manager.withJobCredentialSnapshot(context.Background(), config.BackupJobConfig{
		ID: "restic", Type: JobTypeRestic, Source: source,
		Repository: "rest:http://backup.example/repo", PasswordFile: passwordFile,
	}, "", func(commandCtx context.Context, executionJob config.BackupJobConfig) error {
		snapshotPath = executionJob.PasswordFile
		return runExternalCommand(commandCtx, commandPath)
	})
	if err != nil {
		t.Fatalf("withJobCredentialSnapshot() error: %v", err)
	}
	relativeSnapshot, err := filepath.Rel(realTempDir, snapshotPath)
	if err != nil {
		t.Fatalf("Rel(snapshot) error: %v", err)
	}
	if relativeSnapshot == "." || relativeSnapshot == ".." || strings.HasPrefix(relativeSnapshot, ".."+string(filepath.Separator)) {
		t.Fatalf("snapshot path %q is outside resolved temp root %q", snapshotPath, realTempDir)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(snapshotPath)), credentialSnapshotDirPrefix) {
		t.Fatalf("snapshot directory = %q, want prefix %q", filepath.Dir(snapshotPath), credentialSnapshotDirPrefix)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private credential snapshot stat error = %v, want not exist", err)
	}
}

func TestManager_RestoreDrillFailureNotifies(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 13, 4, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	_, err = manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{})
	if !errors.Is(err, ErrNoSnapshots) {
		t.Fatalf("RunRestoreDrill() error = %v, want ErrNoSnapshots", err)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeRestoreDrill || event.Level != NotificationLevelCritical {
		t.Fatalf("notification type/level = %s/%s, want backup_restore_drill/critical", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusFailed || event.Message != "backup restore drill failed" || event.FailureCategory != FailureCategoryNoSnapshot {
		t.Fatalf("unexpected notification event: %+v", event)
	}
	if !event.Timestamp.Equal(now) {
		t.Fatalf("notification timestamp = %s, want %s", event.Timestamp, now)
	}
	if !event.ErrorMessagePresent || event.LocationOmitted {
		t.Fatalf("notification summary markers = error:%v location:%v, want error only", event.ErrorMessagePresent, event.LocationOmitted)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, event)
}

func TestManager_RestoreFailureNotifies(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	restoreTarget := filepath.Join(tmpDir, "restore-target", "token=restore-secret")
	if err := os.MkdirAll(filepath.Dir(restoreTarget), 0700); err != nil {
		t.Fatalf("MkdirAll(restore target parent) error: %v", err)
	}
	stateRoot := filepath.Join(tmpDir, "state", "token=state-secret")
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        stateRoot,
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: filepath.Join(tmpDir, "backups"),
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 13, 5, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	_, err = manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget})
	if !errors.Is(err, ErrNoSnapshots) {
		t.Fatalf("RunRestore() error = %v, want ErrNoSnapshots", err)
	}

	events := notifier.Events()
	if len(events) != 1 {
		t.Fatalf("notification count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != NotificationTypeRestore || event.Level != NotificationLevelCritical {
		t.Fatalf("notification type/level = %s/%s, want backup_restore/critical", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusFailed || event.Message != "backup restore failed" {
		t.Fatalf("unexpected notification event: %+v", event)
	}
	if !event.Timestamp.Equal(now) {
		t.Fatalf("notification timestamp = %s, want %s", event.Timestamp, now)
	}
	if !event.ErrorMessagePresent || !event.LocationOmitted {
		t.Fatalf("notification summary markers = error:%v location:%v, want both true", event.ErrorMessagePresent, event.LocationOmitted)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, event)
}

func TestManager_RestoreVerifyWarningsNotify(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups", "token=destination-secret")
	mustWriteFile(t, filepath.Join(source, "files", "docs", "note.txt"), "restore verify")
	mustWriteFile(t, filepath.Join(source, ".mnemonas", "index.db"), "index")
	if err := os.MkdirAll(filepath.Join(source, ".mnemonas", "objects"), 0700); err != nil {
		t.Fatalf("MkdirAll(objects) error: %v", err)
	}
	restoreTarget := filepath.Join(tmpDir, "restore-target", "token=restore-secret")
	if err := os.MkdirAll(filepath.Dir(restoreTarget), 0700); err != nil {
		t.Fatalf("MkdirAll(restore target parent) error: %v", err)
	}
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:          "home",
			Name:        "Home backup",
			Type:        JobTypeLocal,
			Source:      source,
			Destination: destination,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	now := time.Date(2026, 5, 13, 6, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := manager.RunRestore(context.Background(), "home", RestoreOptions{TargetPath: restoreTarget}); err != nil {
		t.Fatalf("RunRestore() error: %v", err)
	}
	mustWriteFile(t, filepath.Join(restoreTarget, "unexpected.txt"), "unexpected")

	now = now.Add(time.Minute)
	verify, err := manager.RunRestoreVerify(context.Background(), "home", RestoreVerifyOptions{TargetPath: restoreTarget})
	if err != nil {
		t.Fatalf("RunRestoreVerify() error: %v", err)
	}
	assertWarningsContain(t, verify.Warnings, "恢复目标包含对照备份未登记的文件")

	events := notifier.Events()
	verifyEvents := make([]NotificationEvent, 0, len(events))
	for _, event := range events {
		if event.Type == NotificationTypeRestoreVerify {
			verifyEvents = append(verifyEvents, event)
		}
	}
	if len(verifyEvents) != 1 {
		t.Fatalf("restore verify notification count = %d in %+v, want 1", len(verifyEvents), events)
	}
	event := verifyEvents[0]
	if event.Type != NotificationTypeRestoreVerify || event.Level != NotificationLevelWarning {
		t.Fatalf("notification type/level = %s/%s, want backup_restore_verify/warning", event.Type, event.Level)
	}
	if event.JobID != "home" || event.Status != StatusCompleted || event.Message != "backup restore verification completed with warnings" || event.WarningCount == 0 {
		t.Fatalf("unexpected notification event: %+v", event)
	}
	if !event.Timestamp.Equal(now) {
		t.Fatalf("notification timestamp = %s, want %s", event.Timestamp, now)
	}
	if event.ErrorMessagePresent || !event.LocationOmitted {
		t.Fatalf("notification summary markers = error:%v location:%v, want location only", event.ErrorMessagePresent, event.LocationOmitted)
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, event)
}

func TestManager_RestoreDrillReminderNotifiesWhenDueAndStale(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups", "token=destination-secret")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore reminder")

	now := time.Date(2026, 5, 10, 2, 0, 0, 0, time.UTC)
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:                     "home",
			Name:                   "Home backup",
			Type:                   JobTypeLocal,
			Source:                 source,
			Destination:            destination,
			MaxSnapshots:           7,
			RestoreDrillStaleAfter: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }

	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	if events, err := manager.SendRestoreDrillReminders(context.Background()); err != nil || len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() immediate result = (%+v, %v), want no events or error", events, err)
	}

	now = now.Add(2 * time.Hour)
	events, err := manager.SendRestoreDrillReminders(context.Background())
	if err != nil {
		t.Fatalf("SendRestoreDrillReminders() due error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("SendRestoreDrillReminders() due event count = %d, want 1", len(events))
	}
	if events[0].Type != NotificationTypeRestoreDrill || events[0].Level != NotificationLevelWarning || events[0].Status != "due" {
		t.Fatalf("due reminder event = %+v", events[0])
	}
	if events[0].Trigger != NotificationTriggerReminder || events[0].LastSuccessfulRunAt == nil || events[0].StaleAfter == "" || events[0].Message != "backup restore drill is due" {
		t.Fatalf("due reminder missing metadata: %+v", events[0])
	}
	if !events[0].LocationOmitted {
		t.Fatalf("due reminder location marker = false, want true: %+v", events[0])
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, events[0])
	if len(notifier.Events()) != 1 {
		t.Fatalf("notifier event count after due reminder = %d, want 1", len(notifier.Events()))
	}

	now = now.Add(time.Hour)
	if events, err := manager.SendRestoreDrillReminders(context.Background()); err != nil || len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() cooldown result = (%+v, %v), want no events or error", events, err)
	}

	now = now.Add(24 * time.Hour)
	if events, err := manager.SendRestoreDrillReminders(context.Background()); err != nil || len(events) != 1 || events[0].Status != "due" {
		t.Fatalf("SendRestoreDrillReminders() after cooldown = (%+v, %v), want due reminder", events, err)
	}

	now = now.Add(time.Minute)
	if _, err := manager.RunRestoreDrill(context.Background(), "home", RestoreDrillOptions{}); err != nil {
		t.Fatalf("RunRestoreDrill() error: %v", err)
	}
	if events, err := manager.SendRestoreDrillReminders(context.Background()); err != nil || len(events) != 0 {
		t.Fatalf("SendRestoreDrillReminders() after fresh drill = (%+v, %v), want no events or error", events, err)
	}

	now = now.Add(25 * time.Hour)
	events, err = manager.SendRestoreDrillReminders(context.Background())
	if err != nil {
		t.Fatalf("SendRestoreDrillReminders() stale error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("SendRestoreDrillReminders() stale event count = %d, want 1", len(events))
	}
	if events[0].Status != "stale" || events[0].RunID == "" || events[0].LastRestoreDrillAt == nil || events[0].Message != "backup restore drill is stale" {
		t.Fatalf("stale reminder event = %+v", events[0])
	}
	if !events[0].LocationOmitted {
		t.Fatalf("stale reminder location marker = false, want true: %+v", events[0])
	}
	assertBackupNotificationEventOmitsLocationAndMessageText(t, events[0])
}

func TestManager_RestoreDrillReminderRetriesAfterMarkerPersistenceFailure(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	destination := filepath.Join(tmpDir, "backups")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore reminder")
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	notifier := &recordingNotifier{}
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Notifier:    notifier,
		Jobs: []config.BackupJobConfig{{
			ID:                     "home",
			Name:                   "Home backup",
			Type:                   JobTypeLocal,
			Source:                 source,
			Destination:            destination,
			RestoreDrillStaleAfter: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	initialEventCount := len(notifier.Events())
	now = now.Add(2 * time.Hour)

	originalWriteBackupStateFile := writeBackupStateFile
	t.Cleanup(func() { writeBackupStateFile = originalWriteBackupStateFile })
	persistErr := errors.New("injected reminder marker persistence failure")
	failMarker := true
	writeBackupStateFile = func(lock *backupStateLock, path string, value any, perm os.FileMode) error {
		state, ok := value.(persistedState)
		if ok && failMarker && state.Jobs["home"].LastRestoreDrillReminderAt != nil {
			return persistErr
		}
		return originalWriteBackupStateFile(lock, path, value, perm)
	}

	events, err := manager.SendRestoreDrillReminders(context.Background())
	if len(events) != 1 || !errors.Is(err, persistErr) || !errors.Is(err, ErrBackupStatePersistence) {
		t.Fatalf("SendRestoreDrillReminders() failed marker result = (%+v, %v)", events, err)
	}
	if len(notifier.Events()) != initialEventCount+1 {
		t.Fatalf("notifier events after failed marker = %+v, want delivered reminder pending marker retry", notifier.Events())
	}
	manager.mu.Lock()
	remindedAt := cloneTime(manager.state.Jobs["home"].LastRestoreDrillReminderAt)
	manager.mu.Unlock()
	if remindedAt != nil {
		t.Fatalf("LastRestoreDrillReminderAt = %v after failed persistence, want nil", remindedAt)
	}

	failMarker = false
	events, err = manager.SendRestoreDrillReminders(context.Background())
	if err != nil || len(events) != 1 || len(notifier.Events()) != initialEventCount+2 {
		t.Fatalf("SendRestoreDrillReminders() retry result = (%+v, %v), notifications = %+v", events, err, notifier.Events())
	}
}

func TestManager_RestoreDrillReminderPersistsMarkerOnlyAfterDelivery(t *testing.T) {
	tmpDir := secureBackupTestTempDir(t)
	source := filepath.Join(tmpDir, "source")
	mustWriteFile(t, filepath.Join(source, "note.txt"), "restore reminder")
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	manager, err := newBackupTestManager(t, ManagerConfig{
		Root:        filepath.Join(tmpDir, "state"),
		StorageRoot: source,
		Jobs: []config.BackupJobConfig{{
			ID:                     "home",
			Name:                   "Home backup",
			Type:                   JobTypeLocal,
			Source:                 source,
			Destination:            filepath.Join(tmpDir, "backups"),
			RestoreDrillStaleAfter: time.Hour,
		}},
	})
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	manager.now = func() time.Time { return now }
	if _, err := manager.RunJob(context.Background(), "home"); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	now = now.Add(2 * time.Hour)
	deliveryErr := errors.New("injected reminder delivery failure")
	failDelivery := true
	deliveries := 0
	manager.notifier = NotifierFunc(func(_ context.Context, _ NotificationEvent) error {
		deliveries++
		if failDelivery {
			return deliveryErr
		}
		return nil
	})

	events, err := manager.SendRestoreDrillReminders(context.Background())
	if len(events) != 0 || err == nil || deliveries != 1 {
		t.Fatalf("SendRestoreDrillReminders() failed delivery result = (%+v, %v), deliveries = %d", events, err, deliveries)
	}
	manager.mu.Lock()
	remindedAt := cloneTime(manager.state.Jobs["home"].LastRestoreDrillReminderAt)
	manager.mu.Unlock()
	if remindedAt != nil {
		t.Fatalf("LastRestoreDrillReminderAt = %v after failed delivery, want nil", remindedAt)
	}

	failDelivery = false
	events, err = manager.SendRestoreDrillReminders(context.Background())
	if err != nil || len(events) != 1 || deliveries != 2 {
		t.Fatalf("SendRestoreDrillReminders() retry result = (%+v, %v), deliveries = %d", events, err, deliveries)
	}
	manager.mu.Lock()
	remindedAt = cloneTime(manager.state.Jobs["home"].LastRestoreDrillReminderAt)
	manager.mu.Unlock()
	if remindedAt == nil || !remindedAt.Equal(now) {
		t.Fatalf("LastRestoreDrillReminderAt = %v after delivery, want %v", remindedAt, now)
	}
}

func TestSafeJoinRejectsTraversalManifestPaths(t *testing.T) {
	for _, archivePath := range []string{"../secret", "data/../secret", "data//secret", "./data/secret", `data\secret`, `data/report\2026.txt`, "data/report\n2026.txt", "data/report\x7f.txt"} {
		if _, err := safeJoin(secureBackupTestTempDir(t), archivePath); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("safeJoin(%q) error = %v, want ErrUnsafePath", archivePath, err)
		}
	}
}

func TestSummarizeRestoredTreeRejectsSymlink(t *testing.T) {
	root := secureBackupTestTempDir(t)
	mustWriteFile(t, filepath.Join(root, "docs", "note.txt"), "restored")
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "docs", "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, _, err := summarizeRestoredTree(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("summarizeRestoredTree() error = %v, want ErrUnsafePath", err)
	}
}

func TestSummarizeRestoredTreeRejectsUnsafeEntryName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a path separator on this platform")
	}
	root := secureBackupTestTempDir(t)
	mustWriteFile(t, filepath.Join(root, `docs\secret.txt`), "restored")

	_, _, err := summarizeRestoredTree(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("summarizeRestoredTree() error = %v, want ErrUnsafePath", err)
	}
}

func TestSummarizeRestoreVerificationTreeRejectsUnsafeEntryName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a path separator on this platform")
	}
	root := secureBackupTestTempDir(t)
	mustWriteFile(t, filepath.Join(root, `docs\secret.txt`), "restored")

	_, _, _, err := summarizeRestoreVerificationTree(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("summarizeRestoreVerificationTree() error = %v, want ErrUnsafePath", err)
	}
}

func TestAppendRestoreDirectoryComparisonWarningsRejectsUnsafeTargetEntryName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a path separator on this platform")
	}
	targetPath := filepath.Join(secureBackupTestTempDir(t), "target")
	if err := os.MkdirAll(filepath.Join(targetPath, `docs\secret`), 0700); err != nil {
		t.Fatalf("MkdirAll(target unsafe dir) error: %v", err)
	}

	manifest := Manifest{Directories: testManifestDirectories()}
	_, err := appendRestoreDirectoryComparisonWarnings(context.Background(), targetPath, manifest, true, nil)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("appendRestoreDirectoryComparisonWarnings() error = %v, want ErrUnsafePath", err)
	}
}

func TestAppendRestoreFileComparisonWarningsRejectsUnsafeTargetEntryName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a path separator on this platform")
	}
	targetPath := secureBackupTestTempDir(t)
	mustWriteFile(t, filepath.Join(targetPath, `docs\secret.txt`), "restored")

	manifest := Manifest{Directories: testManifestDirectories()}
	_, err := appendRestoreFileComparisonWarnings(context.Background(), targetPath, manifest, true, true, nil)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("appendRestoreFileComparisonWarnings() error = %v, want ErrUnsafePath", err)
	}
}

func TestRestoreVerificationExistsHelpersRejectParentSymlink(t *testing.T) {
	root := secureBackupTestTempDir(t)
	target := filepath.Join(root, "target")
	outside := filepath.Join(root, "outside", ".mnemonas")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("MkdirAll(target) error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "objects"), 0700); err != nil {
		t.Fatalf("MkdirAll(outside objects) error: %v", err)
	}
	mustWriteFile(t, filepath.Join(outside, "index.db"), "outside-index")
	if err := os.Symlink(outside, filepath.Join(target, ".mnemonas")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := regularFileExistsNoFollow(filepath.Join(target, ".mnemonas", "index.db")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("regularFileExistsNoFollow() error = %v, want ErrUnsafePath", err)
	}
	if _, err := dirExistsNoFollow(filepath.Join(target, ".mnemonas", "objects")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("dirExistsNoFollow() error = %v, want ErrUnsafePath", err)
	}
}

func TestRemoteRestorePreviewRejectsNegativeFileSizes(t *testing.T) {
	t.Run("restic", func(t *testing.T) {
		_, _, _, err := parseResticLSJSON([]byte(`{"type":"file","path":"/srv/source/docs/bad.txt","size":-1}`+"\n"), "/srv/source")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseResticLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone preview", func(t *testing.T) {
		_, _, _, err := parseRcloneLSJSON([]byte(`[{"Path":"docs/bad.txt","Size":-1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone retention", func(t *testing.T) {
		_, _, _, err := parseRcloneRetentionLSJSON([]byte(`[{"Path":"docs/bad.txt","Size":-1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneRetentionLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})
}

func TestRemoteRestorePreviewRejectsUnsafeListingPaths(t *testing.T) {
	t.Run("restic parent segment", func(t *testing.T) {
		_, _, _, err := parseResticLSJSON([]byte(`{"type":"file","path":"/srv/source/docs/../escape.txt","size":1}`+"\n"), "/srv/source")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseResticLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("restic outside source", func(t *testing.T) {
		_, _, _, err := parseResticLSJSON([]byte(`{"type":"file","path":"/srv/other/secret.txt","size":1}`+"\n"), "/srv/source")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseResticLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone preview", func(t *testing.T) {
		_, _, _, err := parseRcloneLSJSON([]byte(`[{"Path":"../secret.txt","Size":1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone retention", func(t *testing.T) {
		_, _, _, err := parseRcloneRetentionLSJSON([]byte(`[{"Path":"/secret.txt","Size":1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneRetentionLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("restic backslash path", func(t *testing.T) {
		_, _, _, err := parseResticLSJSON([]byte(`{"type":"file","path":"/srv/source/docs\\secret.txt","size":1}`+"\n"), "/srv/source")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseResticLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone preview windows drive path", func(t *testing.T) {
		_, _, _, err := parseRcloneLSJSON([]byte(`[{"Path":"C:\\restore\\secret.txt","Size":1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone retention unc path", func(t *testing.T) {
		_, _, _, err := parseRcloneRetentionLSJSON([]byte(`[{"Path":"\\\\server\\share\\secret.txt","Size":1,"IsDir":false}]`))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneRetentionLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})
}

func TestRemoteRestorePreviewRejectsFileSizeOverflow(t *testing.T) {
	tooLarge := strconv.FormatInt(1<<63-1, 10)

	t.Run("restic", func(t *testing.T) {
		input := []byte(
			`{"type":"file","path":"/srv/source/docs/a.bin","size":` + tooLarge + `}` + "\n" +
				`{"type":"file","path":"/srv/source/docs/b.bin","size":1}` + "\n",
		)
		_, _, _, err := parseResticLSJSON(input, "/srv/source")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseResticLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone preview", func(t *testing.T) {
		input := []byte(`[{"Path":"docs/a.bin","Size":` + tooLarge + `,"IsDir":false},{"Path":"docs/b.bin","Size":1,"IsDir":false}]`)
		_, _, _, err := parseRcloneLSJSON(input)
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("rclone retention", func(t *testing.T) {
		input := []byte(`[{"Path":"docs/a.bin","Size":` + tooLarge + `,"IsDir":false},{"Path":"docs/b.bin","Size":1,"IsDir":false}]`)
		_, _, _, err := parseRcloneRetentionLSJSON(input)
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parseRcloneRetentionLSJSON() error = %v, want ErrUnsafePath", err)
		}
	})
}

func TestRemoteRestorePreviewPreservesWhitespaceInFileNames(t *testing.T) {
	t.Run("restic", func(t *testing.T) {
		input := []byte(
			`{"type":"file","path":"/srv/source/docs/ leading.txt","size":1}` + "\n" +
				`{"type":"file","path":"/srv/source/docs/trailing.txt ","size":2}` + "\n",
		)
		fileCount, totalBytes, samples, err := parseResticLSJSON(input, "/srv/source")
		if err != nil {
			t.Fatalf("parseResticLSJSON() error: %v", err)
		}
		if fileCount != 2 || totalBytes != 3 {
			t.Fatalf("parseResticLSJSON() count/bytes = %d/%d, want 2/3", fileCount, totalBytes)
		}
		want := []string{"docs/ leading.txt", "docs/trailing.txt "}
		if !reflect.DeepEqual(samples, want) {
			t.Fatalf("parseResticLSJSON() samples = %#v, want %#v", samples, want)
		}
	})

	t.Run("rclone preview", func(t *testing.T) {
		input := []byte(`[` +
			`{"Path":"docs/ leading.txt","Size":1,"IsDir":false},` +
			`{"Path":"docs/trailing.txt ","Size":2,"IsDir":false}` +
			`]`)
		fileCount, totalBytes, samples, err := parseRcloneLSJSON(input)
		if err != nil {
			t.Fatalf("parseRcloneLSJSON() error: %v", err)
		}
		if fileCount != 2 || totalBytes != 3 {
			t.Fatalf("parseRcloneLSJSON() count/bytes = %d/%d, want 2/3", fileCount, totalBytes)
		}
		want := []string{"docs/ leading.txt", "docs/trailing.txt "}
		if !reflect.DeepEqual(samples, want) {
			t.Fatalf("parseRcloneLSJSON() samples = %#v, want %#v", samples, want)
		}
	})
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll(%s) error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, string(data), want)
	}
}

func assertDirectoryExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(%s) is not a directory", path)
	}
}

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("Stat(%s) mode = %04o, want %04o", path, got, want)
	}
}

func assertWarningsContain(t *testing.T, warnings []string, want string) {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return
		}
	}
	t.Fatalf("warnings = %#v, want entry containing %q", warnings, want)
}

func assertWarningsNotContain(t *testing.T, warnings []string, unwanted string) {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, unwanted) {
			t.Fatalf("warnings = %#v, want no entry containing %q", warnings, unwanted)
		}
	}
}

func restorePreflightCheckByID(t *testing.T, checks []RestorePreflightCheck, id string) RestorePreflightCheck {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("preflight check %q not found in %+v", id, checks)
	return RestorePreflightCheck{}
}

func corruptManifestArchivePath(t *testing.T, manifestPath string, archivePath string) {
	t.Helper()
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("readManifest() error: %v", err)
	}
	if len(manifest.Entries) == 0 {
		t.Fatal("manifest has no entries")
	}
	manifest.Entries[0].ArchivePath = archivePath
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}
}

func corruptManifestEntrySize(t *testing.T, manifestPath string, size int64) {
	t.Helper()
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("readManifest() error: %v", err)
	}
	if len(manifest.Entries) == 0 {
		t.Fatal("manifest has no entries")
	}
	manifest.Entries[0].Size = size
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}
}

func corruptManifestIdentity(t *testing.T, manifestPath string, jobID string, runID string) {
	t.Helper()
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("readManifest() error: %v", err)
	}
	manifest.JobID = jobID
	manifest.RunID = runID
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}
}

func corruptManifestCreatedAt(t *testing.T, manifestPath string, createdAt time.Time) {
	t.Helper()
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("readManifest() error: %v", err)
	}
	manifest.CreatedAt = createdAt
	if err := writeJSONFile(manifestPath, manifest, 0600); err != nil {
		t.Fatalf("writeJSONFile(manifest) error: %v", err)
	}
}

func newRecordingCommand(t *testing.T, exitCode int, stderr string) (string, string) {
	t.Helper()
	dir := secureBackupTestTempDir(t)
	commandPath := filepath.Join(dir, "mnemonas-test-command")
	logPath := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n"
	if stderr != "" {
		script += "printf '%s\\n' " + shellQuote(stderr) + " >&2\n"
	}
	if exitCode != 0 {
		script += "exit " + strconv.Itoa(exitCode) + "\n"
	}
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func newRecordingResticCommand(t *testing.T, source string) (string, string) {
	t.Helper()
	dir := secureBackupTestTempDir(t)
	commandPath := filepath.Join(dir, "mnemonas-test-restic")
	logPath := filepath.Join(dir, "args.log")
	sourceRel := filepath.Clean(source)
	if volume := filepath.VolumeName(sourceRel); volume != "" {
		sourceRel = strings.TrimPrefix(sourceRel, volume)
	}
	sourceRel = strings.TrimPrefix(sourceRel, string(filepath.Separator))
	sourceRel = filepath.ToSlash(sourceRel)
	restoredDir := sourceRel + "/docs"
	restoredFile := restoredDir + "/note.txt"
	resticPreviewPath := filepath.ToSlash(filepath.Join(source, "docs", "note.txt"))
	snapshotJSON := `[{"time":"2026-05-09T10:00:00Z","id":"abc123","tags":["mnemonas","job:restic-remote"]}]`
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n" +
		"mode=''\n" +
		"restore_target=''\n" +
		"prev=''\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = 'ls' ]; then mode='ls'; fi\n" +
		"  if [ \"$arg\" = 'snapshots' ]; then mode='snapshots'; fi\n" +
		"  if [ \"$prev\" = '--target' ]; then restore_target=$arg; fi\n" +
		"  prev=$arg\n" +
		"done\n" +
		"if [ \"$mode\" = 'snapshots' ]; then\n" +
		"  printf '%s\\n' " + shellQuote(snapshotJSON) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$mode\" = 'ls' ]; then\n" +
		"  printf '%s\\n' " + shellQuote(`{"path":"`+resticPreviewPath+`","type":"file","size":15}`) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ -n \"$restore_target\" ]; then\n" +
		"  mkdir -p \"$restore_target\"/" + shellQuote(restoredDir) + "\n" +
		"  printf '%s' 'restic restored' > \"$restore_target\"/" + shellQuote(restoredFile) + "\n" +
		"fi\n"
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func newRecordingRcloneCommand(t *testing.T) (string, string) {
	t.Helper()
	dir := secureBackupTestTempDir(t)
	commandPath := filepath.Join(dir, "mnemonas-test-rclone")
	logPath := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"{\n" +
		"  printf '%s\\n' '__CALL__'\n" +
		"  for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n" +
		"} >> " + shellQuote(logPath) + "\n" +
		"mode=''\n" +
		"copy_target=''\n" +
		"after_copy=0\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = 'lsjson' ]; then\n" +
		"    printf '%s\\n' " + shellQuote(`[{"Path":"docs/note.txt","Size":6,"IsDir":false,"ModTime":"2026-05-09T10:00:00Z"}]`) + "\n" +
		"    exit 0\n" +
		"  fi\n" +
		"  if [ \"$arg\" = 'copy' ]; then mode='copy'; after_copy=1; continue; fi\n" +
		"  if [ \"$after_copy\" = '1' ]; then after_copy=2; continue; fi\n" +
		"  if [ \"$after_copy\" = '2' ]; then copy_target=$arg; after_copy=0; fi\n" +
		"done\n" +
		"if [ \"$mode\" = 'copy' ] && [ -n \"$copy_target\" ]; then\n" +
		"  mkdir -p \"$copy_target/docs\"\n" +
		"  printf '%s' 'rclone' > \"$copy_target/docs/note.txt\"\n" +
		"fi\n"
	if err := os.WriteFile(commandPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(command) error: %v", err)
	}
	return commandPath, logPath
}

func readCommandCalls(t *testing.T, logPath string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(command log) error: %v", err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "__CALL__" {
			calls = append(calls, []string{})
			continue
		}
		if len(calls) == 0 {
			t.Fatalf("command log has argument before call separator: %q", line)
		}
		calls[len(calls)-1] = append(calls[len(calls)-1], line)
	}
	return calls
}

func assertCommandArgs(t *testing.T, got []string, want []string) {
	t.Helper()
	normalized := append([]string(nil), got...)
	for i := 1; i < len(normalized) && i < len(want); i++ {
		if normalized[i] == want[i] || got[i-1] != "--password-file" && got[i-1] != "--config" {
			continue
		}
		dir := filepath.Base(filepath.Dir(normalized[i]))
		base := filepath.Base(normalized[i])
		if !strings.HasPrefix(dir, "mnemonas-backup-credential-") || base != "password" && base != "rclone.conf" {
			continue
		}
		if _, err := os.Stat(normalized[i]); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private credential snapshot %q was not removed: %v", normalized[i], err)
		}
		normalized[i] = want[i]
	}
	if strings.Join(normalized, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command args = %#v, want %#v", got, want)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
