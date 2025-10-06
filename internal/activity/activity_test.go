package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeActivityFixture(t *testing.T, path string, entries []Entry) {
	t.Helper()

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("failed to marshal activity fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatalf("failed to write activity fixture: %v", err)
	}
}

func TestCleanupActivityTempPath_JoinsRemoveError(t *testing.T) {
	tmpDir := t.TempDir()
	busyDir := filepath.Join(tmpDir, "busy")
	if err := os.Mkdir(busyDir, 0700); err != nil {
		t.Fatalf("failed to create busy temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busyDir, "child"), []byte("data"), 0600); err != nil {
		t.Fatalf("failed to create busy temp child: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to open root: %v", err)
	}
	defer root.Close()

	operationErr := errors.New("append failed")
	err = cleanupActivityTempPath(root, "busy", operationErr)
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected joined error to include operation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cleanup temp activity file busy") {
		t.Fatalf("expected cleanup context in error, got %v", err)
	}
}

func TestWriteActivityLogFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")

	originalSyncActivityLogRootDir := syncActivityLogRootDir
	syncActivityLogRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncActivityLogRootDir = originalSyncActivityLogRootDir
	}()

	err := writeActivityLogFile(logPath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeActivityLogFile() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync activity log directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("expected activity log to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected activity log content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(logPath)
	if statErr != nil {
		t.Fatalf("expected activity log file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("expected activity log permissions 0640, got %o", info.Mode().Perm())
	}
	if _, _, ok, rootErr := registeredActivityLogDirRoot(logPath); rootErr != nil {
		t.Fatalf("registeredActivityLogDirRoot() error: %v", rootErr)
	} else if ok {
		t.Fatal("expected failed first write to release the activity log directory root")
	}
}

func TestWriteActivityLogFile_CleansCreatedDirectoryWhenTempNameFails(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "nested", "activity")
	logPath := filepath.Join(logDir, "activity.json")

	originalActivityRandomRead := activityRandomRead
	activityRandomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	defer func() {
		activityRandomRead = originalActivityRandomRead
	}()

	err := writeActivityLogFile(logPath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeActivityLogFile() to fail when temp file naming fails")
	}
	if !strings.Contains(err.Error(), "entropy unavailable") {
		t.Fatalf("expected entropy failure, got %v", err)
	}
	if _, _, ok, rootErr := registeredActivityLogDirRoot(logPath); rootErr != nil {
		t.Fatalf("registeredActivityLogDirRoot() error: %v", rootErr)
	} else if ok {
		t.Fatal("expected failed first write to release the activity log directory root")
	}
	if _, statErr := os.Stat(logDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected newly-created activity log directory to be removed, got %v", statErr)
	}
}

func TestWriteActivityLogFile_ReplacesExistingFileAndCleansTemp(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte(`[{"id":"old"}]`), 0600); err != nil {
		t.Fatalf("WriteFile(existing activity log) error: %v", err)
	}

	if err := writeActivityLogFile(logPath, []byte(`[{"id":"new"}]`)); err != nil {
		t.Fatalf("writeActivityLogFile() error: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(activity log) error: %v", err)
	}
	if string(data) != `[{"id":"new"}]` {
		t.Fatalf("activity log content = %q, want new content", string(data))
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat(activity log) error: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("activity log permissions = %o, want 0640", info.Mode().Perm())
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir(tmpDir) error: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".activity-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary activity log file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store == nil {
		t.Fatal("NewStore() returned nil")
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries, got %d", store.Count())
	}
}

func TestNewStore_ReturnsErrorWhenActivityDirectorySyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "nested", "activity")

	originalSyncActivityLogDir := syncActivityLogDir
	syncActivityLogDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncActivityLogDir = originalSyncActivityLogDir
	}()

	if _, err := NewStore(root); err == nil {
		t.Fatal("expected NewStore() to fail when activity directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync activity directory tree") {
		t.Fatalf("expected activity directory tree sync failure, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "activity.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no activity log file to be created, got %v", statErr)
	}
}

func TestEnsureActivityDir_SyncsCreatedDirectoriesDeepestParentFirst(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "nested", "activity", "logs")

	originalSyncActivityLogDir := syncActivityLogDir
	var synced []string
	syncActivityLogDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	defer func() {
		syncActivityLogDir = originalSyncActivityLogDir
	}()

	if err := ensureActivityDir(targetDir, 0700); err != nil {
		t.Fatalf("ensureActivityDir() error: %v", err)
	}

	want := []string{
		filepath.Join(tmpDir, "nested", "activity"),
		filepath.Join(tmpDir, "nested"),
		tmpDir,
	}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced directories = %v, want %v", synced, want)
	}
}

func TestNewStore_RecoversFromCorruptLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte("{invalid json"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store.Count() != 0 {
		t.Fatalf("Expected recovered store to start empty, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created")
	}

	if err := store.Log(ActionUpload, "/recovered.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() after recovery error: %v", err)
	}

	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("Expected recovered store to persist new entries, got %d", reloaded.Count())
	}
}

func TestNewStore_RecoversFromTrailingDataAfterArray(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte(`[{"id":"entry-1","timestamp":"2026-04-20T10:30:00Z","action":"upload","path":"/docs/file.txt","user":"user"}] {}`), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("Expected recovered store to start empty after trailing data recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for trailing data")
	}
}

func TestNewStore_RecoversFromUnknownAction(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:        "unknown-action",
		Timestamp: time.Now(),
		Action:    ActionType("unknown_action"),
		Path:      "/docs/file.txt",
		User:      "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected recovered store to start empty after unknown action recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for unknown action")
	}
}

func TestNewStore_RecoversFromDuplicateActivityID(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{
		{
			ID:        "duplicate-id",
			Timestamp: time.Now(),
			Action:    ActionUpload,
			Path:      "/docs/a.txt",
			User:      "admin",
		},
		{
			ID:        "duplicate-id",
			Timestamp: time.Now(),
			Action:    ActionDelete,
			Path:      "/docs/b.txt",
			User:      "admin",
		},
	})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected recovered store to start empty after duplicate ID recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for duplicate ID")
	}
}

func TestNewStore_RecoversFromEmptyActivityID(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "/docs/file.txt",
		User:      "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected recovered store to start empty after empty ID recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for empty ID")
	}
}

func TestNewStore_RecoversFromActivityIDWithSurroundingWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:        " activity-id ",
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "/docs/file.txt",
		User:      "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected recovered store to start empty after noncanonical ID recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for noncanonical ID")
	}
}

func TestNewStore_RecoversFromZeroTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:     "zero-timestamp",
		Action: ActionUpload,
		Path:   "/docs/file.txt",
		User:   "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected recovered store to start empty after zero timestamp recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for zero timestamp")
	}
}

func TestNewStore_RecoversFromTruncatedEntryInArray(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte(`[{"id":"entry-1","timestamp":"2026-04-20T10:30:00Z","action":"upload","path":"/docs/file.txt","user":"user"`), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("Expected recovered store to start empty after truncated entry recovery, got %d entries", store.Count())
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}

	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity log backup to be created for truncated entry")
	}
}

func TestNewStore_ReturnsErrorWhenCorruptLogBackupDirectorySyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.json")
	if err := os.WriteFile(logPath, []byte("{invalid json"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}

	originalSyncActivityLogDir := syncActivityLogDir
	syncFailed := false
	originalSyncActivityLogRootDir := syncActivityLogRootDir
	syncActivityLogRootDir = func(root *os.Root) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("sync dir failed")
		}
		return nil
	}
	t.Cleanup(func() {
		syncActivityLogDir = originalSyncActivityLogDir
		syncActivityLogRootDir = originalSyncActivityLogRootDir
	})

	if _, err := NewStore(tmpDir); err == nil {
		t.Fatal("expected NewStore() to fail when corrupt backup directory sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt activity log directory") {
		t.Fatalf("expected directory sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Fatalf("expected original corrupt log to remain after rollback, got %v", statErr)
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
	}
}

func TestNewStore_RejectsSymlinkLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "real-activity.json")
	logPath := filepath.Join(tmpDir, "activity.json")

	if err := os.WriteFile(targetPath, []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(real-activity.json) error: %v", err)
	}
	if err := os.Symlink(targetPath, logPath); err != nil {
		t.Fatalf("Symlink(activity.json) error: %v", err)
	}

	_, err := NewStore(tmpDir)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestNewStore_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realRoot := filepath.Join(tmpDir, "real-activity-root")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(real-root) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "activity.json"), []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(activity.json) error: %v", err)
	}
	linkedRoot := filepath.Join(tmpDir, "linked-activity-root")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("Symlink(linked-root) error: %v", err)
	}

	_, err := NewStore(linkedRoot)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
}

func TestNewStore_DoesNotCreateRootThroughSymlinkParent(t *testing.T) {
	tmpDir := t.TempDir()
	realParent := filepath.Join(tmpDir, "real-parent")
	if err := os.MkdirAll(realParent, 0755); err != nil {
		t.Fatalf("MkdirAll(real-parent) error: %v", err)
	}
	linkedParent := filepath.Join(tmpDir, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("Symlink(linked-parent) error: %v", err)
	}

	activityRoot := filepath.Join(linkedParent, "activity")
	if _, err := NewStore(activityRoot); !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "activity")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("activity root created through symlink parent, stat error = %v", err)
	}
}

func TestNewStore_Load_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	activityDir := filepath.Join(baseDir, "activity")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(activityDir, 0755); err != nil {
		t.Fatalf("failed to create activity dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	writeActivityFixture(t, filepath.Join(activityDir, "activity.json"), []Entry{{
		ID:        "original",
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "/docs/original.txt",
		User:      "user1",
	}})
	writeActivityFixture(t, filepath.Join(outsideDir, "activity.json"), []Entry{{
		ID:        "outside",
		Timestamp: time.Now(),
		Action:    ActionDelete,
		Path:      "/docs/outside.txt",
		User:      "user2",
	}})

	originalHook := afterValidateActivityLogPath
	var hookErr error
	swapped := false
	afterValidateActivityLogPath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "activity-backup")
		if err := os.Rename(activityDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, activityDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateActivityLogPath = originalHook
	}()

	store, err := NewStore(activityDir)
	if hookErr != nil {
		t.Fatalf("afterValidateActivityLogPath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected load to stay bound to the original directory, got %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 || entries[0].ID != "original" {
		t.Fatalf("expected original activity log to be loaded, got total=%d entries=%+v", total, entries)
	}
}

func TestNewStore_LoadRejectsLogSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	activityDir := filepath.Join(baseDir, "activity")
	if err := os.MkdirAll(activityDir, 0755); err != nil {
		t.Fatalf("failed to create activity dir: %v", err)
	}
	logPath := filepath.Join(activityDir, "activity.json")
	writeActivityFixture(t, logPath, []Entry{{
		ID:        "original",
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "/docs/original.txt",
		User:      "user1",
	}})
	linkedTarget := filepath.Join(activityDir, "linked.json")
	writeActivityFixture(t, linkedTarget, []Entry{{
		ID:        "linked",
		Timestamp: time.Now(),
		Action:    ActionDelete,
		Path:      "/docs/linked.txt",
		User:      "user2",
	}})

	originalHook := afterValidateActivityLogPath
	var hookErr error
	swapped := false
	afterValidateActivityLogPath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		if err := os.Remove(logPath); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(filepath.Base(linkedTarget), logPath)
	}
	defer func() {
		afterValidateActivityLogPath = originalHook
	}()

	_, err := NewStore(activityDir)
	if hookErr != nil {
		t.Fatalf("afterValidateActivityLogPath hook error: %v", hookErr)
	}
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected activity log symlink rejection, got %v", err)
	}
}

func TestStore_Log_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	activityDir := filepath.Join(baseDir, "activity")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	writeActivityFixture(t, filepath.Join(outsideDir, "activity.json"), []Entry{})

	store, err := NewStore(activityDir)
	if err != nil {
		t.Fatalf("failed to create activity store: %v", err)
	}

	originalHook := afterValidateActivityLogPath
	var hookErr error
	swapped := false
	afterValidateActivityLogPath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "activity-backup")
		if err := os.Rename(activityDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, activityDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateActivityLogPath = originalHook
	}()

	err = store.Log(ActionUpload, "/docs/file.txt", "user1", "127.0.0.1", nil)
	if hookErr != nil {
		t.Fatalf("afterValidateActivityLogPath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected log write to stay bound to the original directory, got %v", err)
	}

	outsideStore, err := NewStore(outsideDir)
	if err != nil {
		t.Fatalf("failed to reload outside activity store: %v", err)
	}
	if outsideStore.Count() != 0 {
		entries, total := outsideStore.List(10, 0, "", "")
		t.Fatalf("expected outside activity log to remain unchanged, got total=%d entries=%+v", total, entries)
	}

	backupStore, err := NewStore(filepath.Join(baseDir, "activity-backup"))
	if err != nil {
		t.Fatalf("failed to reload original activity directory inode: %v", err)
	}
	entries, total := backupStore.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 || entries[0].Path != "/docs/file.txt" {
		t.Fatalf("expected logged entry to persist in original directory inode, got total=%d entries=%+v", total, entries)
	}
}

func TestNewStore_RecoverCorruptLog_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	activityDir := filepath.Join(baseDir, "activity")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(activityDir, 0755); err != nil {
		t.Fatalf("failed to create activity dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activityDir, "activity.json"), []byte("{invalid json"), 0640); err != nil {
		t.Fatalf("failed to seed corrupt activity log: %v", err)
	}
	writeActivityFixture(t, filepath.Join(outsideDir, "activity.json"), []Entry{})

	originalHook := afterValidateActivityLogPath
	var hookErr error
	swapped := false
	afterValidateActivityLogPath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "activity-backup")
		if err := os.Rename(activityDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, activityDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateActivityLogPath = originalHook
	}()

	store, err := NewStore(activityDir)
	if hookErr != nil {
		t.Fatalf("afterValidateActivityLogPath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected corrupt recovery to stay bound to the original directory, got %v", err)
	}
	if store.Count() != 0 {
		entries, total := store.List(10, 0, "", "")
		t.Fatalf("expected recovered activity store to be empty, got total=%d entries=%+v", total, entries)
	}

	entries, err := os.ReadDir(filepath.Join(baseDir, "activity-backup"))
	if err != nil {
		t.Fatalf("failed to read backup activity directory: %v", err)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "activity.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt activity backup to remain in original directory inode")
	}

	outsideStore, err := NewStore(outsideDir)
	if err != nil {
		t.Fatalf("failed to reload outside activity store: %v", err)
	}
	if outsideStore.Count() != 0 {
		entries, total := outsideStore.List(10, 0, "", "")
		t.Fatalf("expected outside activity log to remain unchanged, got total=%d entries=%+v", total, entries)
	}
}

func TestLogAndList(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log entries
	err := store.Log(ActionUpload, "/file1.txt", "user1", "192.168.1.1", nil)
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	err = store.Log(ActionDelete, "/file2.txt", "user2", "192.168.1.2", map[string]string{"reason": "cleanup"})
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	// List entries
	entries, total := store.List(10, 0, "", "")
	if total != 2 {
		t.Errorf("Expected total 2, got %d", total)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// First entry should be most recent (delete)
	if entries[0].Action != ActionDelete {
		t.Errorf("Expected first entry to be delete, got %s", entries[0].Action)
	}
	if entries[0].User != "user2" {
		t.Errorf("Expected user2, got %s", entries[0].User)
	}
}

func TestRecordReviewPersistsAndReloads(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	fixedNow := time.Date(2026, time.May, 1, 10, 10, 0, 0, time.UTC)
	originalNow := activityTimeNow
	activityTimeNow = func() time.Time { return fixedNow }
	defer func() {
		activityTimeNow = originalNow
	}()

	record, err := store.RecordReview(ReviewRecordInput{
		Reviewer:          " admin ",
		Note:              " 已确认误删文件已恢复 ",
		ScopeLabel:        "集中窗口",
		FilterSummary:     "分组 高风险变更",
		DispositionStatus: ReviewDispositionRestored,
		ActionCounts: map[ActionType]int{
			ActionDelete: 1,
			ActionMove:   1,
		},
		ReviewCount:      2,
		TotalCount:       3,
		PathCount:        2,
		UserCount:        1,
		PathSamples:      []string{" docs/deleted.txt ", "/docs/moved.txt"},
		UserSamples:      []string{" user1 "},
		ActivityEntryIDs: []string{"delete-1", "move-1"},
	})
	if err != nil {
		t.Fatalf("RecordReview() error: %v", err)
	}
	if record.Reviewer != "admin" || record.Note != "已确认误删文件已恢复" {
		t.Fatalf("record was not normalized: %+v", record)
	}
	if !record.ReviewedAt.Equal(fixedNow) {
		t.Fatalf("ReviewedAt = %s, want %s", record.ReviewedAt, fixedNow)
	}
	if record.DispositionStatus != ReviewDispositionRestored {
		t.Fatalf("DispositionStatus = %q, want %q", record.DispositionStatus, ReviewDispositionRestored)
	}
	if record.ActionCounts[ActionDelete] != 1 || record.ActionCounts[ActionMove] != 1 {
		t.Fatalf("ActionCounts were not persisted: %+v", record.ActionCounts)
	}
	if got, want := record.PathSamples, []string{"/docs/deleted.txt", "/docs/moved.txt"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("PathSamples = %v, want %v", got, want)
	}
	if got, want := record.UserSamples, []string{"user1"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("UserSamples = %v, want %v", got, want)
	}

	records, total := store.ListReviewRecords(10, 0)
	if total != 1 || len(records) != 1 {
		t.Fatalf("expected one review record, got total=%d records=%+v", total, records)
	}
	records[0].ActionCounts[ActionDelete] = 99
	records[0].PathSamples[0] = "/mutated"
	records[0].UserSamples[0] = "mutated"
	records[0].ActivityEntryIDs[0] = "mutated"
	records, _ = store.ListReviewRecords(10, 0)
	if records[0].ActionCounts[ActionDelete] != 1 {
		t.Fatalf("ListReviewRecords returned mutable action count map: %+v", records[0].ActionCounts)
	}
	if records[0].PathSamples[0] != "/docs/deleted.txt" {
		t.Fatalf("ListReviewRecords returned mutable path samples: %+v", records[0].PathSamples)
	}
	if records[0].UserSamples[0] != "user1" {
		t.Fatalf("ListReviewRecords returned mutable user samples: %+v", records[0].UserSamples)
	}
	if records[0].ActivityEntryIDs[0] != "delete-1" {
		t.Fatalf("ListReviewRecords returned mutable internal slice: %+v", records[0].ActivityEntryIDs)
	}

	reloaded, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	reloadedRecords, reloadedTotal := reloaded.ListReviewRecords(10, 0)
	if reloadedTotal != 1 || len(reloadedRecords) != 1 || reloadedRecords[0].ID != record.ID {
		t.Fatalf("expected reloaded review record %q, got total=%d records=%+v", record.ID, reloadedTotal, reloadedRecords)
	}
	if reloadedRecords[0].DispositionStatus != ReviewDispositionRestored || reloadedRecords[0].ActionCounts[ActionDelete] != 1 {
		t.Fatalf("reloaded review lost structured disposition fields: %+v", reloadedRecords[0])
	}
}

func TestRecordReviewRejectsInvalidInput(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	_, err = store.RecordReview(ReviewRecordInput{
		Reviewer:         "admin",
		Note:             "",
		ScopeLabel:       "当前页",
		ReviewCount:      0,
		TotalCount:       0,
		PathCount:        0,
		UserCount:        0,
		ActivityEntryIDs: []string{},
	})
	if !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("RecordReview() error = %v, want %v", err, ErrInvalidReviewRecord)
	}
	records, total := store.ListReviewRecords(10, 0)
	if total != 0 || len(records) != 0 {
		t.Fatalf("invalid review should not persist, got total=%d records=%+v", total, records)
	}

	_, err = store.RecordReview(ReviewRecordInput{
		Reviewer:          "admin",
		Note:              "已处理",
		ScopeLabel:        "当前页",
		DispositionStatus: "unknown",
		ReviewCount:       1,
		TotalCount:        1,
		PathCount:         1,
		UserCount:         1,
		ActivityEntryIDs:  []string{"delete-1"},
	})
	if !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("RecordReview(invalid disposition) error = %v, want %v", err, ErrInvalidReviewRecord)
	}

	_, err = store.RecordReview(ReviewRecordInput{
		Reviewer:          "admin",
		Note:              "已处理",
		ScopeLabel:        "当前页",
		DispositionStatus: ReviewDispositionDocumented,
		ActionCounts: map[ActionType]int{
			ActionDelete: 2,
		},
		ReviewCount:      1,
		TotalCount:       1,
		PathCount:        1,
		UserCount:        1,
		ActivityEntryIDs: []string{"delete-1"},
	})
	if !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("RecordReview(invalid action counts) error = %v, want %v", err, ErrInvalidReviewRecord)
	}
}

func TestUpdateReviewRecordDispositionPersistsAndReloads(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalNow := activityTimeNow
	defer func() {
		activityTimeNow = originalNow
	}()
	createdAt := time.Date(2026, time.May, 1, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, time.May, 1, 11, 0, 0, 0, time.UTC)
	activityTimeNow = func() time.Time { return createdAt }
	record, err := store.RecordReview(ReviewRecordInput{
		Reviewer:          "admin",
		Note:              "分享需要跟进",
		ScopeLabel:        "当前页",
		DispositionStatus: ReviewDispositionNeedsFollowUp,
		ReviewCount:       1,
		TotalCount:        1,
		PathCount:         1,
		UserCount:         1,
		PathSamples:       []string{"/docs/report.pdf"},
		ActivityEntryIDs:  []string{"share-1"},
	})
	if err != nil {
		t.Fatalf("RecordReview() error: %v", err)
	}

	activityTimeNow = func() time.Time { return updatedAt }
	nextNote := "分享链接已关闭，访问入口已核对"
	updated, err := store.UpdateReviewRecordDisposition(record.ID, " owner ", ReviewDispositionDisabled, &nextNote)
	if err != nil {
		t.Fatalf("UpdateReviewRecordDisposition() error: %v", err)
	}
	if updated.DispositionStatus != ReviewDispositionDisabled {
		t.Fatalf("DispositionStatus = %q, want %q", updated.DispositionStatus, ReviewDispositionDisabled)
	}
	if updated.Reviewer != "owner" {
		t.Fatalf("Reviewer = %q, want owner", updated.Reviewer)
	}
	if !updated.ReviewedAt.Equal(updatedAt) {
		t.Fatalf("ReviewedAt = %s, want %s", updated.ReviewedAt, updatedAt)
	}
	if updated.Note != nextNote || updated.ActivityEntryIDs[0] != "share-1" || updated.PathSamples[0] != "/docs/report.pdf" {
		t.Fatalf("update should preserve review record context: %+v", updated)
	}

	filtered, total := store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{DispositionStatus: ReviewDispositionNeedsFollowUp})
	if total != 0 || len(filtered) != 0 {
		t.Fatalf("updated review should leave needs-follow-up filter, got total=%d records=%+v", total, filtered)
	}
	filtered, total = store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{DispositionStatus: ReviewDispositionDisabled})
	if total != 1 || len(filtered) != 1 || filtered[0].ID != record.ID {
		t.Fatalf("updated review should match disabled filter, got total=%d records=%+v", total, filtered)
	}

	reloaded, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	reloadedRecords, reloadedTotal := reloaded.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{DispositionStatus: ReviewDispositionDisabled})
	if reloadedTotal != 1 || len(reloadedRecords) != 1 || reloadedRecords[0].ID != record.ID {
		t.Fatalf("expected reloaded disabled review, got total=%d records=%+v", reloadedTotal, reloadedRecords)
	}
}

func TestUpdateReviewRecordDispositionRejectsInvalidInput(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	record, err := store.RecordReview(ReviewRecordInput{
		Reviewer:         "admin",
		Note:             "待跟进",
		ScopeLabel:       "当前页",
		ReviewCount:      1,
		TotalCount:       1,
		PathCount:        1,
		UserCount:        1,
		ActivityEntryIDs: []string{"delete-1"},
	})
	if err != nil {
		t.Fatalf("RecordReview() error: %v", err)
	}

	if _, err := store.UpdateReviewRecordDisposition(record.ID, "admin", "unknown", nil); !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("UpdateReviewRecordDisposition(invalid status) error = %v, want %v", err, ErrInvalidReviewRecord)
	}
	if _, err := store.UpdateReviewRecordDisposition(" missing ", "admin", ReviewDispositionRestored, nil); !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("UpdateReviewRecordDisposition(invalid id) error = %v, want %v", err, ErrInvalidReviewRecord)
	}
	if _, err := store.UpdateReviewRecordDisposition("missing", "admin", ReviewDispositionRestored, nil); !errors.Is(err, ErrReviewRecordNotFound) {
		t.Fatalf("UpdateReviewRecordDisposition(missing) error = %v, want %v", err, ErrReviewRecordNotFound)
	}
	emptyNote := " "
	if _, err := store.UpdateReviewRecordDisposition(record.ID, "admin", ReviewDispositionRestored, &emptyNote); !errors.Is(err, ErrInvalidReviewRecord) {
		t.Fatalf("UpdateReviewRecordDisposition(empty note) error = %v, want %v", err, ErrInvalidReviewRecord)
	}

	records, total := store.ListReviewRecords(10, 0)
	if total != 1 || len(records) != 1 || records[0].DispositionStatus != ReviewDispositionDocumented {
		t.Fatalf("invalid update should not mutate records, got total=%d records=%+v", total, records)
	}
}

func TestLoadLegacyReviewRecordsDefaultsDisposition(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := NewStore(tmpDir); err != nil {
		t.Fatalf("NewStore() setup error: %v", err)
	}
	legacy := `[{
		"id": "legacy-review",
		"reviewed_at": "2026-05-01T10:10:00Z",
		"reviewer": "admin",
		"note": "legacy note",
		"scope_label": "当前页",
		"filter_summary": "未筛选",
		"review_count": 1,
		"total_count": 1,
		"path_count": 1,
		"user_count": 1,
		"activity_entry_ids": ["delete-1"]
	}]`
	if err := os.WriteFile(filepath.Join(tmpDir, "activity_reviews.json"), []byte(legacy), 0640); err != nil {
		t.Fatalf("failed to write legacy review record fixture: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	records, total := store.ListReviewRecords(10, 0)
	if total != 1 || len(records) != 1 {
		t.Fatalf("expected one legacy review record, got total=%d records=%+v", total, records)
	}
	if records[0].DispositionStatus != ReviewDispositionDocumented {
		t.Fatalf("DispositionStatus = %q, want %q", records[0].DispositionStatus, ReviewDispositionDocumented)
	}
}

func TestListReviewRecordsFiltered(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	first := time.Date(2026, time.May, 1, 10, 0, 0, 0, time.UTC)
	second := time.Date(2026, time.May, 2, 10, 0, 0, 0, time.UTC)
	originalNow := activityTimeNow
	defer func() {
		activityTimeNow = originalNow
	}()

	activityTimeNow = func() time.Time { return first }
	if _, err := store.RecordReview(ReviewRecordInput{
		Reviewer:         "admin",
		Note:             "older review",
		ScopeLabel:       "当前页",
		ReviewCount:      1,
		TotalCount:       1,
		PathCount:        1,
		UserCount:        1,
		ActivityEntryIDs: []string{"delete-1"},
	}); err != nil {
		t.Fatalf("RecordReview(first) error: %v", err)
	}

	activityTimeNow = func() time.Time { return second }
	if _, err := store.RecordReview(ReviewRecordInput{
		Reviewer:          "owner",
		Note:              "newer review",
		ScopeLabel:        "集中窗口",
		DispositionStatus: ReviewDispositionNeedsFollowUp,
		ReviewCount:       2,
		TotalCount:        3,
		PathCount:         2,
		UserCount:         1,
		ActivityEntryIDs:  []string{"delete-2", "share-1"},
	}); err != nil {
		t.Fatalf("RecordReview(second) error: %v", err)
	}

	records, total := store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{
		Reviewer:          "owner",
		ActivityEntryID:   "share-1",
		DispositionStatus: ReviewDispositionNeedsFollowUp,
		Since:             &second,
	})
	if total != 1 || len(records) != 1 {
		t.Fatalf("expected one filtered review, got total=%d records=%+v", total, records)
	}
	if records[0].Reviewer != "owner" || records[0].Note != "newer review" {
		t.Fatalf("unexpected filtered review: %+v", records[0])
	}

	records, total = store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{ActivityEntryID: "missing"})
	if total != 0 || len(records) != 0 {
		t.Fatalf("expected no missing-entry reviews, got total=%d records=%+v", total, records)
	}

	records, total = store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{DispositionStatus: ReviewDispositionDocumented})
	if total != 1 || len(records) != 1 || records[0].Reviewer != "admin" {
		t.Fatalf("expected one default documented review, got total=%d records=%+v", total, records)
	}

	records, total = store.ListReviewRecordsFiltered(10, 0, ReviewRecordFilter{DispositionStatus: ReviewDispositionDisabled})
	if total != 0 || len(records) != 0 {
		t.Fatalf("expected no disabled reviews, got total=%d records=%+v", total, records)
	}
}

func TestLogNormalizesActivityPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, " docs\\report.txt/ ", "user1", "192.168.1.1", nil); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one activity entry, got total=%d entries=%+v", total, entries)
	}
	if entries[0].Path != "/docs/report.txt" {
		t.Fatalf("activity path = %q, want /docs/report.txt", entries[0].Path)
	}

	reloaded, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	reloadedEntries, _ := reloaded.List(10, 0, "", "")
	if len(reloadedEntries) != 1 || reloadedEntries[0].Path != "/docs/report.txt" {
		t.Fatalf("reloaded activity path = %+v, want /docs/report.txt", reloadedEntries)
	}
}

func TestLogRejectsUnsafeActivityPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, "/docs/./report.txt", "user1", "192.168.1.1", nil); !errors.Is(err, errInvalidActivityPath) {
		t.Fatalf("Log() error = %v, want %v", err, errInvalidActivityPath)
	}
	if store.Count() != 0 {
		t.Fatalf("expected rejected activity path not to be stored, got %d entries", store.Count())
	}
}

func TestLogNormalizesActivityDetailPaths(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionMove, "/docs/source.txt", "user1", "192.168.1.1", map[string]string{
		"to":         " docs\\target.txt/ ",
		"from":       "/docs/./unsafe.txt",
		"quota_path": "/quota/..\x00/secret",
		"note":       "keep ../text",
	}); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one activity entry, got total=%d entries=%+v", total, entries)
	}
	details := entries[0].Details
	if details["to"] != "/docs/target.txt" {
		t.Fatalf("to detail = %q, want /docs/target.txt", details["to"])
	}
	if details["from"] != "" {
		t.Fatalf("unsafe from detail = %q, want hidden empty path", details["from"])
	}
	if details["quota_path"] != "" {
		t.Fatalf("unsafe quota_path detail = %q, want hidden empty path", details["quota_path"])
	}
	if details["note"] != "keep ../text" {
		t.Fatalf("non-path detail changed to %q", details["note"])
	}
}

func TestNewStoreNormalizesLegacyActivityPath(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:        "legacy-relative-path",
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "legacy\\report.txt/",
		User:      "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one legacy activity entry, got total=%d entries=%+v", total, entries)
	}
	if entries[0].Path != "/legacy/report.txt" {
		t.Fatalf("legacy activity path = %q, want /legacy/report.txt", entries[0].Path)
	}
}

func TestNewStoreHidesUnsafeLegacyActivityPath(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:        "legacy-unsafe-path",
		Timestamp: time.Now(),
		Action:    ActionUpload,
		Path:      "/legacy/../secret.txt",
		User:      "admin",
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one legacy activity entry, got total=%d entries=%+v", total, entries)
	}
	if entries[0].Path != "" {
		t.Fatalf("unsafe legacy activity path = %q, want hidden empty path", entries[0].Path)
	}
}

func TestNewStoreNormalizesLegacyActivityDetailPaths(t *testing.T) {
	tmpDir := t.TempDir()
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), []Entry{{
		ID:        "legacy-detail-paths",
		Timestamp: time.Now(),
		Action:    ActionMove,
		Path:      "/legacy/source.txt",
		User:      "admin",
		Details: map[string]string{
			"to":   "legacy\\target.txt/",
			"from": "/legacy/./unsafe.txt",
			"type": "file",
		},
	}})

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one legacy activity entry, got total=%d entries=%+v", total, entries)
	}
	details := entries[0].Details
	if details["to"] != "/legacy/target.txt" {
		t.Fatalf("legacy to detail = %q, want /legacy/target.txt", details["to"])
	}
	if details["from"] != "" {
		t.Fatalf("unsafe legacy from detail = %q, want hidden empty path", details["from"])
	}
	if details["type"] != "file" {
		t.Fatalf("legacy non-path detail changed to %q", details["type"])
	}
}

func TestLogRejectsUnknownAction(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	err = store.Log(ActionType("unknown_action"), "/docs/file.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("expected unknown action to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown activity action") {
		t.Fatalf("expected unknown action error, got %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected rejected unknown action not to be stored, got %d entries", store.Count())
	}

	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if reloaded.Count() != 0 {
		t.Fatalf("expected rejected unknown action not to persist, got %d entries", reloaded.Count())
	}
}

func TestLogRejectsZeroTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalNow := activityTimeNow
	activityTimeNow = func() time.Time { return time.Time{} }
	t.Cleanup(func() {
		activityTimeNow = originalNow
	})

	err = store.Log(ActionUpload, "/docs/file.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("expected zero timestamp to be rejected")
	}
	if !strings.Contains(err.Error(), "activity timestamp must not be zero") {
		t.Fatalf("expected zero timestamp error, got %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected rejected zero timestamp not to be stored, got %d entries", store.Count())
	}
}

func TestLogRejectsEmptyGeneratedActivityID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalIDGenerator := activityIDGenerator
	activityIDGenerator = func() (string, error) {
		return "", nil
	}
	t.Cleanup(func() {
		activityIDGenerator = originalIDGenerator
	})

	err = store.Log(ActionUpload, "/docs/file.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("expected empty generated activity ID to be rejected")
	}
	if !strings.Contains(err.Error(), "activity ID must not be empty") {
		t.Fatalf("expected empty activity ID error, got %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected rejected empty generated ID not to be stored, got %d entries", store.Count())
	}
}

func TestLogRetriesEmptyGeneratedActivityID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalIDGenerator := activityIDGenerator
	var calls int32
	activityIDGenerator = func() (string, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "", nil
		}
		return "valid-id", nil
	}
	t.Cleanup(func() {
		activityIDGenerator = originalIDGenerator
	})

	if err := store.Log(ActionUpload, "/docs/file.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("expected Log() to retry an empty generated ID, got %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one logged entry after retry, got total=%d entries=%+v", total, entries)
	}
	if entries[0].ID != "valid-id" {
		t.Fatalf("expected retried activity ID valid-id, got %q", entries[0].ID)
	}
}

func TestLogRetriesDuplicateActivityID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalIDGenerator := activityIDGenerator
	var calls int32
	activityIDGenerator = func() (string, error) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			return "duplicate-id", nil
		case 2:
			return "duplicate-id", nil
		default:
			return "unique-id", nil
		}
	}
	defer func() {
		activityIDGenerator = originalIDGenerator
	}()

	if err := store.Log(ActionUpload, "/first.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("first Log() error: %v", err)
	}
	if err := store.Log(ActionDelete, "/second.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("second Log() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected two activity entries, got total=%d len=%d", total, len(entries))
	}
	if entries[0].ID != "unique-id" {
		t.Fatalf("expected most recent entry to use retried unique ID, got %q", entries[0].ID)
	}
	if entries[1].ID != "duplicate-id" {
		t.Fatalf("expected first entry to keep original ID, got %q", entries[1].ID)
	}
}

func TestLogReturnsActivityIDGenerationFailure(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, "/original.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("initial Log() error: %v", err)
	}

	originalIDGenerator := activityIDGenerator
	activityIDGenerator = func() (string, error) {
		return "", errors.New("entropy unavailable")
	}
	defer func() {
		activityIDGenerator = originalIDGenerator
	}()

	err = store.Log(ActionDelete, "/should-not-persist.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("expected Log() to fail when activity ID generation fails")
	}
	if !strings.Contains(err.Error(), "generate activity ID") {
		t.Fatalf("expected activity ID generation error, got %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected original entries to remain after activity ID failure, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/original.txt" {
		t.Fatalf("expected original entry to remain after failed log, got %s", entries[0].Path)
	}
}

func TestLogRollsBackWhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/original.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Initial Log() error: %v", err)
	}

	originalRoot := store.root
	blockedPath := filepath.Join(tmpDir, "blocked-root")
	if err := os.WriteFile(blockedPath, []byte("blocked"), 0644); err != nil {
		t.Fatalf("WriteFile(blocked-root) error: %v", err)
	}
	store.root = filepath.Join(blockedPath, "nested")

	err := store.Log(ActionDelete, "/should-not-persist.txt", "user", "127.0.0.1", nil)
	if err == nil {
		t.Fatal("Expected Log() to fail when save path is invalid")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected original entries to remain after failed log, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/original.txt" {
		t.Fatalf("Expected original log entry to remain after rollback, got %s", entries[0].Path)
	}

	store.root = originalRoot
	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	reloadedEntries, reloadedTotal := reloaded.List(10, 0, "", "")
	if reloadedTotal != 1 || len(reloadedEntries) != 1 {
		t.Fatalf("Expected persisted entries to remain unchanged after failed log, got total=%d len=%d", reloadedTotal, len(reloadedEntries))
	}
	if reloadedEntries[0].Path != "/original.txt" {
		t.Fatalf("Expected persisted original entry after failed log, got %s", reloadedEntries[0].Path)
	}
}

func TestLogRollsBackWhenLogPathIsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, "/original.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Initial Log() error: %v", err)
	}

	symlinkRoot := filepath.Join(tmpDir, "symlink-root")
	if err := os.MkdirAll(symlinkRoot, 0755); err != nil {
		t.Fatalf("MkdirAll(symlink-root) error: %v", err)
	}
	targetPath := filepath.Join(tmpDir, "real-activity.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0640); err != nil {
		t.Fatalf("WriteFile(real-activity.json) error: %v", err)
	}
	if err := os.Symlink(targetPath, filepath.Join(symlinkRoot, "activity.json")); err != nil {
		t.Fatalf("Symlink(activity.json) error: %v", err)
	}

	originalRoot := store.root
	store.root = symlinkRoot

	err = store.Log(ActionDelete, "/should-not-persist.txt", "user", "127.0.0.1", nil)
	if !errors.Is(err, errActivityLogSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected original entries after rollback, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/original.txt" {
		t.Fatalf("expected original entry after rollback, got %s", entries[0].Path)
	}

	store.root = originalRoot
}

func TestListWithFilters(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log various entries
	store.Log(ActionUpload, "/file1.txt", "admin", "192.168.1.1", nil)
	store.Log(ActionUpload, "/file2.txt", "user1", "192.168.1.2", nil)
	store.Log(ActionDelete, "/file3.txt", "admin", "192.168.1.1", nil)
	store.Log(ActionLogin, "", "user1", "192.168.1.2", nil)

	// Filter by action
	_, total := store.List(10, 0, ActionUpload, "")
	if total != 2 {
		t.Errorf("Expected 2 upload entries, got %d", total)
	}

	// Filter by user
	_, total = store.List(10, 0, "", "admin")
	if total != 2 {
		t.Errorf("Expected 2 admin entries, got %d", total)
	}

	// Filter by both
	entries, total := store.List(10, 0, ActionUpload, "admin")
	if total != 1 {
		t.Errorf("Expected 1 admin upload entry, got %d", total)
	}
	if entries[0].Path != "/file1.txt" {
		t.Errorf("Expected /file1.txt, got %s", entries[0].Path)
	}
}

func TestListFilteredByTimeRange(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalNow := activityTimeNow
	t.Cleanup(func() {
		activityTimeNow = originalNow
	})

	first := time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC)
	second := time.Date(2026, time.May, 2, 9, 0, 0, 0, time.UTC)
	third := time.Date(2026, time.May, 3, 9, 0, 0, 0, time.UTC)

	activityTimeNow = func() time.Time { return first }
	if err := store.Log(ActionUpload, "/old.txt", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(old) error: %v", err)
	}
	activityTimeNow = func() time.Time { return second }
	if err := store.Log(ActionDelete, "/middle.txt", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(middle) error: %v", err)
	}
	activityTimeNow = func() time.Time { return third }
	if err := store.Log(ActionUpload, "/new.txt", "user1", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(new) error: %v", err)
	}

	since := second
	until := third.Add(-time.Nanosecond)
	entries, total := store.ListFiltered(10, 0, ListFilter{
		Since: &since,
		Until: &until,
	})
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one activity entry in time range, got total=%d len=%d entries=%+v", total, len(entries), entries)
	}
	if entries[0].Path != "/middle.txt" {
		t.Fatalf("expected middle entry in time range, got %s", entries[0].Path)
	}

	entries, total = store.ListFiltered(10, 0, ListFilter{
		Action: ActionUpload,
		Since:  &second,
	})
	if total != 1 || len(entries) != 1 || entries[0].Path != "/new.txt" {
		t.Fatalf("expected upload entries since second timestamp to include only /new.txt, got total=%d entries=%+v", total, entries)
	}
}

func TestListFilteredByPathPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionUpload, "/photos/2026/a.jpg", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(photo upload) error: %v", err)
	}
	if err := store.Log(ActionDelete, "/photos", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(photo directory delete) error: %v", err)
	}
	if err := store.Log(ActionMove, "/incoming/a.jpg", "admin", "127.0.0.1", map[string]string{"to": "/photos/2026/moved.jpg"}); err != nil {
		t.Fatalf("Log(move into photos) error: %v", err)
	}
	if err := store.Log(ActionUpload, "/photos-archive/a.jpg", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(archive upload) error: %v", err)
	}

	entries, total := store.ListFiltered(10, 0, ListFilter{Path: "/photos"})
	if total != 3 || len(entries) != 3 {
		t.Fatalf("expected three entries under /photos, got total=%d entries=%+v", total, entries)
	}
	for _, entry := range entries {
		if entry.Path == "/photos-archive/a.jpg" {
			t.Fatalf("path-prefix filter matched sibling directory: %+v", entries)
		}
	}

	entries, total = store.ListFiltered(10, 0, ListFilter{
		Action: ActionMove,
		Path:   "/photos",
	})
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected move detail path to match /photos filter, got total=%d entries=%+v", total, entries)
	}
	if entries[0].Details["to"] != "/photos/2026/moved.jpg" {
		t.Fatalf("expected move target detail to remain visible, got %+v", entries[0].Details)
	}
}

func TestListFilteredByActionGroup(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := store.Log(ActionShare, "/team/report.pdf", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(share) error: %v", err)
	}
	if err := store.Log(ActionUnshare, "/team/report.pdf", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(unshare) error: %v", err)
	}
	if err := store.Log(ActionUpload, "/team/report.pdf", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(upload) error: %v", err)
	}
	if err := store.Log(ActionRestore, "/team/report.pdf", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(restore) error: %v", err)
	}
	if err := store.Log(ActionTrashRestore, "/team/report.pdf", "admin", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(trash_restore) error: %v", err)
	}

	shareActions, ok := ActionsForGroup(ActionGroupShare)
	if !ok {
		t.Fatal("expected share action group to exist")
	}
	entries, total := store.ListFiltered(10, 0, ListFilter{Actions: shareActions})
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected two share-group entries, got total=%d entries=%+v", total, entries)
	}
	for _, entry := range entries {
		if entry.Action != ActionShare && entry.Action != ActionUnshare {
			t.Fatalf("share action group included unexpected action: %+v", entries)
		}
	}

	entries, total = store.ListFiltered(10, 0, ListFilter{
		Action:  ActionUpload,
		Actions: shareActions,
	})
	if total != 0 || len(entries) != 0 {
		t.Fatalf("expected action and action group filters to intersect, got total=%d entries=%+v", total, entries)
	}

	riskActions, ok := ActionsForGroup(ActionGroupRisk)
	if !ok {
		t.Fatal("expected risk action group to exist")
	}
	entries, total = store.ListFiltered(10, 0, ListFilter{Actions: riskActions})
	if total != 4 || len(entries) != 4 {
		t.Fatalf("expected four risk-group entries, got total=%d entries=%+v", total, entries)
	}
	seenRisk := map[ActionType]bool{}
	for _, entry := range entries {
		seenRisk[entry.Action] = true
		if entry.Action == ActionUpload {
			t.Fatalf("risk action group included upload: %+v", entries)
		}
	}
	for _, action := range []ActionType{ActionShare, ActionUnshare, ActionRestore, ActionTrashRestore} {
		if !seenRisk[action] {
			t.Fatalf("risk action group did not include %q: %+v", action, entries)
		}
	}
}

func TestIsKnownActionRecognizesPublicActions(t *testing.T) {
	publicActions := []ActionType{
		ActionUpload,
		ActionDownload,
		ActionDelete,
		ActionRename,
		ActionMove,
		ActionCopy,
		ActionCreate,
		ActionRestore,
		ActionShare,
		ActionUnshare,
		ActionFavorite,
		ActionUnfavorite,
		ActionFavoriteNote,
		ActionLogin,
		ActionLogout,
		ActionTrashRestore,
		ActionTrashDelete,
		ActionTrashEmpty,
		ActionDiskHealth,
		ActionScrub,
	}
	seen := make(map[ActionType]bool, len(publicActions))
	for _, action := range publicActions {
		if !IsKnownAction(action) {
			t.Fatalf("expected action %q to be known", action)
		}
		if seen[action] {
			t.Fatalf("duplicate public action %q", action)
		}
		seen[action] = true
	}

	for _, action := range []ActionType{"", "risk", "unknown", "UPLOAD"} {
		if IsKnownAction(action) {
			t.Fatalf("expected action %q to be unknown", action)
		}
	}
}

func TestActionGroupsOnlyContainKnownActions(t *testing.T) {
	for _, group := range []ActionGroup{ActionGroupShare, ActionGroupRisk} {
		actions, ok := ActionsForGroup(group)
		if !ok {
			t.Fatalf("expected action group %q to exist", group)
		}
		if len(actions) == 0 {
			t.Fatalf("expected action group %q to contain actions", group)
		}

		seen := make(map[ActionType]bool, len(actions))
		for _, action := range actions {
			if !IsKnownAction(action) {
				t.Fatalf("action group %q contains unknown action %q", group, action)
			}
			if seen[action] {
				t.Fatalf("action group %q contains duplicate action %q", group, action)
			}
			seen[action] = true
		}

		actions[0] = ActionType("mutated")
		freshActions, ok := ActionsForGroup(group)
		if !ok {
			t.Fatalf("expected action group %q to remain available", group)
		}
		if freshActions[0] == ActionType("mutated") {
			t.Fatalf("action group %q returned internal slice", group)
		}
	}

	if actions, ok := ActionsForGroup(ActionGroup("unknown")); ok || actions != nil {
		t.Fatalf("expected unknown action group to return nil, false; got actions=%+v ok=%v", actions, ok)
	}
}

func TestListPagination(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	// Log 5 entries
	for i := 0; i < 5; i++ {
		store.Log(ActionUpload, "/file"+string(rune('0'+i))+".txt", "user", "127.0.0.1", nil)
	}

	// Get page 1 (limit 2)
	entries, total := store.List(2, 0, "", "")
	if total != 5 {
		t.Errorf("Expected total 5, got %d", total)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// Get page 2
	entries, _ = store.List(2, 2, "", "")
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// Get page 3
	entries, _ = store.List(2, 4, "", "")
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(entries))
	}
}

func TestList_ClampsNegativeOffset(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/first.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(first) error: %v", err)
	}
	if err := store.Log(ActionDelete, "/second.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log(second) error: %v", err)
	}

	entries, total := store.List(1, -3, "", "")
	if total != 2 {
		t.Fatalf("Expected total 2, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "/second.txt" {
		t.Fatalf("Expected clamped negative offset to return most recent entry, got %s", entries[0].Path)
	}
}

func TestList_NonPositiveLimitReturnsEmptyPage(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/only.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, total := store.List(-1, 0, "", "")
	if total != 1 {
		t.Fatalf("Expected total 1, got %d", total)
	}
	if len(entries) != 0 {
		t.Fatalf("Expected empty page for negative limit, got %d entries", len(entries))
	}

	entries, total = store.List(0, 0, "", "")
	if total != 1 {
		t.Fatalf("Expected total 1 for zero limit, got %d", total)
	}
	if len(entries) != 0 {
		t.Fatalf("Expected empty page for zero limit, got %d entries", len(entries))
	}
}

func TestClear(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil)
	if store.Count() != 1 {
		t.Errorf("Expected 1 entry, got %d", store.Count())
	}

	err := store.Clear()
	if err != nil {
		t.Fatalf("Clear() error: %v", err)
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", store.Count())
	}
}

func TestClearRollsBackWhenSaveFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	originalRoot := store.root
	blockedPath := filepath.Join(tmpDir, "blocked-root")
	if err := os.WriteFile(blockedPath, []byte("blocked"), 0644); err != nil {
		t.Fatalf("WriteFile(blocked-root) error: %v", err)
	}
	store.root = filepath.Join(blockedPath, "nested")

	err := store.Clear()
	if err == nil {
		t.Fatal("Expected Clear() to fail when save path is invalid")
	}

	if store.Count() != 1 {
		t.Fatalf("Expected in-memory entries to be restored after clear failure, got %d", store.Count())
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected 1 entry after rollback, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/test.txt" {
		t.Fatalf("Expected rolled back entry path /test.txt, got %s", entries[0].Path)
	}

	store.root = originalRoot
	reloaded, reloadErr := NewStore(tmpDir)
	if reloadErr != nil {
		t.Fatalf("NewStore() reload error: %v", reloadErr)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("Expected persisted activity log to remain unchanged after failed clear, got %d", reloaded.Count())
	}
}

func TestListDoesNotBlockWhileLogPersists(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalWriter := activityLogWriter
	writerStarted := make(chan struct{})
	writerRelease := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	activityLogWriter = func(path string, data []byte) error {
		once.Do(func() {
			close(writerStarted)
		})
		<-writerRelease
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		activityLogWriter = originalWriter
		releaseOnce.Do(func() {
			close(writerRelease)
		})
	})

	logDone := make(chan error, 1)
	go func() {
		logDone <- store.Log(ActionUpload, "/slow.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-writerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activity log write to start")
	}

	listDone := make(chan struct{})
	go func() {
		entries, total := store.List(10, 0, "", "")
		if total != 0 || len(entries) != 0 {
			t.Errorf("expected reads during pending persist to observe committed state only, got total=%d len=%d", total, len(entries))
		}
		close(listDone)
	}()

	select {
	case <-listDone:
	case <-time.After(time.Second):
		t.Fatal("List() blocked on an in-flight activity log save")
	}

	releaseOnce.Do(func() {
		close(writerRelease)
	})

	select {
	case err := <-logDone:
		if err != nil {
			t.Fatalf("Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Log() did not finish after releasing writer")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected committed entry after save, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Path != "/slow.txt" {
		t.Fatalf("expected /slow.txt after save, got %s", entries[0].Path)
	}
}

func TestLogSerializesConcurrentPersists(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	originalWriter := activityLogWriter
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var onceFirst sync.Once
	var onceRelease sync.Once
	var onceSecond sync.Once
	var callCount int32
	activityLogWriter = func(path string, data []byte) error {
		call := atomic.AddInt32(&callCount, 1)
		switch call {
		case 1:
			onceFirst.Do(func() {
				close(firstStarted)
			})
			<-firstRelease
		case 2:
			onceSecond.Do(func() {
				close(secondStarted)
			})
		}
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		activityLogWriter = originalWriter
		onceFirst.Do(func() {
			close(firstStarted)
		})
		onceSecond.Do(func() {
			close(secondStarted)
		})
		onceRelease.Do(func() {
			close(firstRelease)
		})
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.Log(ActionUpload, "/first.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first activity log persist to start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.Log(ActionUpload, "/second.txt", "user", "127.0.0.1", nil)
	}()

	select {
	case <-secondStarted:
		t.Fatal("second activity persist started before first persist completed")
	case <-time.After(100 * time.Millisecond):
	}

	onceRelease.Do(func() {
		close(firstRelease)
	})

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Log() did not finish after releasing first persist")
	}

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second activity persist to start")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Log() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Log() did not finish")
	}

	entries, total := store.List(10, 0, "", "")
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected two committed activity entries, got total=%d len=%d", total, len(entries))
	}

	reloaded, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() reload error: %v", err)
	}
	reloadedEntries, reloadedTotal := reloaded.List(10, 0, "", "")
	if reloadedTotal != 2 || len(reloadedEntries) != 2 {
		t.Fatalf("expected two persisted activity entries, got total=%d len=%d", reloadedTotal, len(reloadedEntries))
	}
	if reloadedEntries[0].Path != "/second.txt" || reloadedEntries[1].Path != "/first.txt" {
		t.Fatalf("expected persisted entries [/second.txt /first.txt], got [%s %s]", reloadedEntries[0].Path, reloadedEntries[1].Path)
	}
}

func TestGetByID(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/test.txt", "user", "127.0.0.1", nil)

	entries, _ := store.List(1, 0, "", "")
	if len(entries) != 1 {
		t.Fatal("Expected 1 entry")
	}

	entry, err := store.GetByID(entries[0].ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if entry.Path != "/test.txt" {
		t.Errorf("Expected /test.txt, got %s", entry.Path)
	}

	// Test not found
	_, err = store.GetByID("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent ID")
	}
}

func TestLogCopiesDetailsMap(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	details := map[string]string{"reason": "original"}
	if err := store.Log(ActionDelete, "/file.txt", "user", "127.0.0.1", details); err != nil {
		t.Fatalf("Log() error: %v", err)
	}
	details["reason"] = "mutated"

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got total=%d len=%d", total, len(entries))
	}
	if entries[0].Details["reason"] != "original" {
		t.Fatalf("Expected stored details to remain original, got %q", entries[0].Details["reason"])
	}
}

func TestLogUsesInjectedClock(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 20, 10, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	originalNow := activityTimeNow
	activityTimeNow = func() time.Time { return fixedNow }
	defer func() {
		activityTimeNow = originalNow
	}()

	if err := store.Log(ActionUpload, "/clock.txt", "user", "127.0.0.1", nil); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, total := store.List(10, 0, "", "")
	if total != 1 || len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got total=%d len=%d", total, len(entries))
	}
	if !entries[0].Timestamp.Equal(fixedNow) {
		t.Fatalf("expected logged timestamp %s, got %s", fixedNow, entries[0].Timestamp)
	}
}

func TestListAndGetByIDReturnDetachedDetails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	if err := store.Log(ActionDelete, "/file.txt", "user", "127.0.0.1", map[string]string{"reason": "original"}); err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	entries, _ := store.List(10, 0, "", "")
	entries[0].Details["reason"] = "mutated-via-list"

	reloadedEntries, _ := store.List(10, 0, "", "")
	if reloadedEntries[0].Details["reason"] != "original" {
		t.Fatalf("Expected list mutation to stay detached, got %q", reloadedEntries[0].Details["reason"])
	}

	entry, err := store.GetByID(reloadedEntries[0].ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	entry.Details["reason"] = "mutated-via-get"

	finalEntries, _ := store.List(10, 0, "", "")
	if finalEntries[0].Details["reason"] != "original" {
		t.Fatalf("Expected GetByID mutation to stay detached, got %q", finalEntries[0].Details["reason"])
	}
}

func TestStatistics(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	store.Log(ActionUpload, "/file1.txt", "admin", "127.0.0.1", nil)
	store.Log(ActionUpload, "/file2.txt", "user1", "127.0.0.1", nil)
	store.Log(ActionDelete, "/file3.txt", "admin", "127.0.0.1", nil)

	stats := store.Statistics()

	total, ok := stats["total"].(int)
	if !ok || total != 3 {
		t.Errorf("Expected total 3, got %v", stats["total"])
	}

	today, ok := stats["today"].(int)
	if !ok || today != 3 {
		t.Errorf("Expected today 3, got %v", stats["today"])
	}

	byAction, ok := stats["by_action"].(map[ActionType]int)
	if !ok {
		t.Fatal("by_action type assertion failed")
	}
	if byAction[ActionUpload] != 2 {
		t.Errorf("Expected 2 uploads, got %d", byAction[ActionUpload])
	}
	if byAction[ActionDelete] != 1 {
		t.Errorf("Expected 1 delete, got %d", byAction[ActionDelete])
	}

	byUser, ok := stats["by_user"].(map[string]int)
	if !ok {
		t.Fatal("by_user type assertion failed")
	}
	if byUser["admin"] != 2 {
		t.Errorf("Expected 2 for admin, got %d", byUser["admin"])
	}
}

func TestStatistics_UsesLocalCalendarDayBoundary(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.April, 7, 10, 0, 0, 0, loc)
	originalNow := activityTimeNow
	activityTimeNow = func() time.Time { return now }
	defer func() {
		activityTimeNow = originalNow
	}()

	store := &Store{
		entries: []Entry{
			{Action: ActionUpload, User: "admin", Timestamp: time.Date(2026, time.April, 7, 0, 0, 0, 0, loc)},
			{Action: ActionDelete, User: "admin", Timestamp: time.Date(2026, time.April, 6, 23, 59, 59, 0, loc)},
		},
	}

	stats := store.Statistics()
	today, ok := stats["today"].(int)
	if !ok {
		t.Fatalf("today type assertion failed: %#v", stats["today"])
	}
	if today != 1 {
		t.Fatalf("expected exactly one entry in today's local calendar bucket, got %d", today)
	}
	byAction, ok := stats["by_action"].(map[ActionType]int)
	if !ok {
		t.Fatal("by_action type assertion failed")
	}
	if byAction[ActionUpload] != 1 || byAction[ActionDelete] != 1 {
		t.Fatalf("expected both actions to remain counted in totals, got %#v", byAction)
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and log entry
	store1, _ := NewStore(tmpDir)
	store1.Log(ActionUpload, "/persistent.txt", "user", "127.0.0.1", nil)

	// Create new store instance - should load existing data
	store2, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if store2.Count() != 1 {
		t.Errorf("Expected 1 entry after reload, got %d", store2.Count())
	}

	entries, _ := store2.List(1, 0, "", "")
	if entries[0].Path != "/persistent.txt" {
		t.Errorf("Expected /persistent.txt, got %s", entries[0].Path)
	}
}

func TestMaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	store.maxSize = 5 // Set small max size for test

	// Log more entries than max size
	for i := 0; i < 10; i++ {
		store.Log(ActionUpload, "/file"+string(rune('0'+i))+".txt", "user", "127.0.0.1", nil)
		time.Sleep(time.Millisecond) // Ensure unique timestamps
	}

	if store.Count() != 5 {
		t.Errorf("Expected 5 entries (max size), got %d", store.Count())
	}

	// Verify most recent entries are kept
	entries, _ := store.List(10, 0, "", "")
	// Most recent entry should be file9.txt
	if entries[0].Path != "/file9.txt" {
		t.Errorf("Expected most recent /file9.txt, got %s", entries[0].Path)
	}
}

func TestNewStore_LoadTrimsEntriesToDefaultMaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	entries := make([]Entry, 0, 10005)
	base := time.Now()
	for i := 0; i < 10005; i++ {
		entries = append(entries, Entry{
			ID:        fmt.Sprintf("id-%05d", i),
			Timestamp: base.Add(-time.Duration(i) * time.Second),
			Action:    ActionUpload,
			Path:      fmt.Sprintf("/file%d.txt", i),
			User:      "user",
		})
	}
	writeActivityFixture(t, filepath.Join(tmpDir, "activity.json"), entries)

	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Count() != 10000 {
		t.Fatalf("Expected trimmed default max size 10000, got %d", store.Count())
	}

	loaded, total := store.List(10005, 0, "", "")
	if total != 10000 || len(loaded) != 10000 {
		t.Fatalf("Expected 10000 visible entries after load trim, got total=%d len=%d", total, len(loaded))
	}
	if loaded[0].Path != "/file0.txt" {
		t.Fatalf("Expected newest retained entry /file0.txt, got %s", loaded[0].Path)
	}
	if loaded[len(loaded)-1].Path != "/file9999.txt" {
		t.Fatalf("Expected oldest retained entry /file9999.txt, got %s", loaded[len(loaded)-1].Path)
	}
}

func TestLogFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)

	expectedPath := filepath.Join(tmpDir, "activity.json")
	if store.logFilePath() != expectedPath {
		t.Errorf("Expected %s, got %s", expectedPath, store.logFilePath())
	}
}

func TestEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	activityDir := filepath.Join(tmpDir, "activity")

	// Ensure directory doesn't exist before test
	os.RemoveAll(activityDir)

	store, err := NewStore(activityDir)
	if err != nil {
		t.Fatalf("NewStore() should create directory: %v", err)
	}

	// Directory should be created
	if _, err := os.Stat(activityDir); os.IsNotExist(err) {
		t.Error("Expected activity directory to be created")
	}

	if store.Count() != 0 {
		t.Errorf("Expected 0 entries in new store, got %d", store.Count())
	}
}
