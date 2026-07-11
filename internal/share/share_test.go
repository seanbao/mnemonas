package share

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeShareFixture(t *testing.T, path string, shares []*Share) {
	t.Helper()

	data := marshalShareFixture(t, shares)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("failed to write share fixture: %v", err)
	}
}

func marshalShareFixture(t *testing.T, shares []*Share) []byte {
	t.Helper()

	data, err := json.Marshal(shareStoreFile{
		Version:                shareStoreFormatVersion,
		Shares:                 shares,
		TrashDeleteOperations:  map[string]*shareTrashDeleteOperation{},
		TrashRestoreOperations: map[string]*shareTrashRestoreOperation{},
	})
	if err != nil {
		t.Fatalf("failed to marshal share fixture: %v", err)
	}
	return data
}

func decodePersistedShareFixture(t *testing.T, data []byte) []*Share {
	t.Helper()

	var stored shareStoreFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to unmarshal persisted shares: %v", err)
	}
	if stored.Version != shareStoreFormatVersion {
		t.Fatalf("persisted share store version = %d, want %d", stored.Version, shareStoreFormatVersion)
	}
	return stored.Shares
}

func TestWriteShareStoreFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	err := writeShareStoreFile(storePath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeShareStoreFile() to fail when directory sync fails")
	}
	if !IsPersistenceWarning(err) {
		t.Fatalf("expected persistence warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to sync shares directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(storePath)
	if readErr != nil {
		t.Fatalf("expected shares file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected shares content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(storePath)
	if statErr != nil {
		t.Fatalf("expected shares file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected shares file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestWriteShareStoreFile_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "nested", "state", "shares.json")

	originalSyncShareStoreDir := syncShareStoreDir
	syncShareStoreDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreDir = originalSyncShareStoreDir
	}()

	err := writeShareStoreFile(storePath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeShareStoreFile() to fail when directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync shares directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(storePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no shares file to be created, got %v", statErr)
	}
}

func TestWriteShareStoreFileAtomicallyWithRoot_ReplacesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("old"), 0600); err != nil {
		t.Fatalf("failed to write existing store file: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to open root: %v", err)
	}
	defer root.Close()

	if err := writeShareStoreFileAtomicallyWithRoot(root, storePath, []byte("[]")); err != nil {
		t.Fatalf("writeShareStoreFileAtomicallyWithRoot() error: %v", err)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("failed to read replaced store file: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("store file contents = %q, want []", string(data))
	}
	matches, err := filepath.Glob(filepath.Join(tmpDir, ".shares-*.tmp"))
	if err != nil {
		t.Fatalf("failed to scan temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no temp shares files after atomic replacement, got %v", matches)
	}
}

func TestWriteShareStoreFile_ReplacesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("old"), 0600); err != nil {
		t.Fatalf("failed to write existing store file: %v", err)
	}

	if err := writeShareStoreFile(storePath, []byte("[]")); err != nil {
		t.Fatalf("writeShareStoreFile() error: %v", err)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("failed to read replaced store file: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("store file contents = %q, want []", string(data))
	}
	matches, err := filepath.Glob(filepath.Join(tmpDir, ".shares-*.tmp"))
	if err != nil {
		t.Fatalf("failed to scan temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no temp shares files after atomic replacement, got %v", matches)
	}
}

func TestCleanupShareTempPath_JoinsRemoveError(t *testing.T) {
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

	operationErr := errors.New("operation failed")
	err = cleanupShareTempPath(root, "busy", operationErr)
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected joined error to include operation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cleanup temp shares file busy") {
		t.Fatalf("expected cleanup context in error, got %v", err)
	}
}

func TestWriteShareStoreFile_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "nested", "state", "shares.json")
	storeDir := filepath.Dir(storePath)

	originalHook := afterValidateShareStorePath
	var hookErr error
	hookApplied := false
	afterValidateShareStorePath = func() {
		if hookApplied || hookErr != nil {
			return
		}
		hookApplied = true
		hookErr = os.Chmod(storeDir, 0500)
	}
	defer func() {
		afterValidateShareStorePath = originalHook
		_ = os.Chmod(storeDir, 0755)
	}()

	err := writeShareStoreFile(storePath, []byte("[]"))
	if hookErr != nil {
		t.Fatalf("afterValidateShareStorePath hook error: %v", hookErr)
	}
	if err == nil {
		t.Fatal("expected writeShareStoreFile() to fail when temp file creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create temp shares file") {
		t.Fatalf("expected temp create error, got %v", err)
	}
	if _, statErr := os.Stat(storePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no shares file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(storeDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created share store directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "nested")); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent directory to be removed, got %v", statErr)
	}

	afterValidateShareStorePath = originalHook
	if err := writeShareStoreFile(storePath, []byte("[]")); err != nil {
		t.Fatalf("expected retry after failed write cleanup to succeed, got %v", err)
	}
	data, readErr := os.ReadFile(storePath)
	if readErr != nil {
		t.Fatalf("expected shares file after retry, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected shares content after retry, got %q", string(data))
	}
}

func TestNewShareStore_RecoversFromCorruptSharesFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	if len(store.ListAll()) != 0 {
		t.Fatalf("expected recovered share store to start empty, got %d shares", len(store.ListAll()))
	}
	if len(store.trashDeleteOperations) != 0 || len(store.trashRestoreOperations) != 0 {
		t.Fatalf(
			"expected recovered operation maps to start empty, got delete=%d restore=%d",
			len(store.trashDeleteOperations),
			len(store.trashRestoreOperations),
		)
	}
	if !store.RecoveredFromCorruption() {
		t.Fatal("expected recovered store to report corruption recovery")
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "shares.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt shares backup to be created")
	}
	if _, statErr := os.Stat(shareStoreRecoveryMarkerPath(storePath)); statErr != nil {
		t.Fatalf("expected durable recovery marker, got %v", statErr)
	}

	share, createErr := store.Create(CreateShareOptions{Path: "/docs/file.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if createErr != nil {
		t.Fatalf("Create() after recovery error: %v", createErr)
	}
	reloaded, reloadErr := NewShareStore(storePath)
	if reloadErr != nil {
		t.Fatalf("NewShareStore() reload error: %v", reloadErr)
	}
	if _, getErr := reloaded.Get(share.ID); getErr != nil {
		t.Fatalf("expected recovered share store to persist new shares, got %v", getErr)
	}
	if !reloaded.RecoveredFromCorruption() {
		t.Fatal("reload lost durable corruption recovery state")
	}
	if err := os.Remove(shareStoreRecoveryMarkerPath(storePath)); err != nil {
		t.Fatalf("Remove(recovery marker) error: %v", err)
	}
	cleared, clearErr := NewShareStore(storePath)
	if clearErr != nil {
		t.Fatalf("NewShareStore() after marker removal error: %v", clearErr)
	}
	if cleared.RecoveredFromCorruption() {
		t.Fatal("store remained recovery-blocked after explicit marker removal")
	}
}

func TestNewShareStore_RecoversFromTruncatedJSONWithDurableMarker(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	truncated := []byte(`{"version":`)
	if err := os.WriteFile(storePath, truncated, 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	if !store.RecoveredFromCorruption() {
		t.Fatal("truncated store did not report corruption recovery")
	}
	if _, err := os.Stat(shareStoreRecoveryMarkerPath(storePath)); err != nil {
		t.Fatalf("durable recovery marker missing: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	foundBackup := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "shares.json.corrupt.") {
			continue
		}
		foundBackup = true
		backup, err := os.ReadFile(filepath.Join(tmpDir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(corrupt backup) error: %v", err)
		}
		if !bytes.Equal(backup, truncated) {
			t.Fatalf("corrupt backup = %q, want %q", backup, truncated)
		}
	}
	if !foundBackup {
		t.Fatal("truncated shares file was not backed up")
	}
}

func TestNewShareStore_ReturnsErrorWhenCorruptSharesBackupSyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	originalSyncShareStoreDir := syncShareStoreDir
	syncFailed := false
	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncShareStoreDir = originalSyncShareStoreDir
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	if _, err := NewShareStore(storePath); err == nil {
		t.Fatal("expected NewShareStore() to fail when recovery marker sync fails")
	} else if !strings.Contains(err.Error(), "persist share store recovery marker") {
		t.Fatalf("expected recovery marker sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(storePath); statErr != nil {
		t.Fatalf("expected original corrupt shares file to remain after rollback, got %v", statErr)
	}
	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "shares.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
	}
}

func TestShareStore_SaveShareState_PersistsCanonicalOrder(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "shares.json")
	createdAt := time.Unix(1700000000, 0)
	shares := map[string]*Share{
		"share-c": {
			ID:        "share-c",
			Path:      "/docs/c.txt",
			CreatedBy: "user-b",
			CreatedAt: createdAt,
		},
		"share-b": {
			ID:        "share-b",
			Path:      "/docs/b.txt",
			CreatedBy: "user-a",
			CreatedAt: createdAt,
		},
		"share-a": {
			ID:        "share-a",
			Path:      "/docs/a.txt",
			CreatedBy: "user-a",
			CreatedAt: createdAt,
		},
	}

	expected := []struct {
		id   string
		path string
		user string
	}{
		{id: "share-a", path: "/docs/a.txt", user: "user-a"},
		{id: "share-b", path: "/docs/b.txt", user: "user-a"},
		{id: "share-c", path: "/docs/c.txt", user: "user-b"},
	}

	for i := 0; i < 64; i++ {
		if err := saveShareState(storePath, shares); err != nil {
			t.Fatalf("saveShareState() error: %v", err)
		}

		data, err := os.ReadFile(storePath)
		if err != nil {
			t.Fatalf("ReadFile(shares.json) error: %v", err)
		}

		persisted := decodePersistedShareFixture(t, data)
		if len(persisted) != len(expected) {
			t.Fatalf("persisted share count = %d, want %d", len(persisted), len(expected))
		}
		for index, want := range expected {
			if persisted[index].ID != want.id || persisted[index].Path != want.path || persisted[index].CreatedBy != want.user {
				t.Fatalf("persisted order at iteration %d = [%s:%s:%s %s:%s:%s %s:%s:%s], want [%s:%s:%s %s:%s:%s %s:%s:%s]",
					i,
					persisted[0].ID,
					persisted[0].CreatedBy,
					persisted[0].Path,
					persisted[1].ID,
					persisted[1].CreatedBy,
					persisted[1].Path,
					persisted[2].ID,
					persisted[2].CreatedBy,
					persisted[2].Path,
					expected[0].id,
					expected[0].user,
					expected[0].path,
					expected[1].id,
					expected[1].user,
					expected[1].path,
					expected[2].id,
					expected[2].user,
					expected[2].path,
				)
			}
		}
	}
}

func TestShareStore_Create(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if share.ID == "" {
		t.Error("share ID should not be empty")
	}
	if share.Path != "/test/file.txt" {
		t.Errorf("expected path /test/file.txt, got %s", share.Path)
	}
	if share.Permission != PermissionRead {
		t.Errorf("expected default permission read, got %s", share.Permission)
	}
	if !share.Enabled {
		t.Error("share should be enabled by default")
	}
}

func TestShareStore_Create_NormalizesPathIndex(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      `docs\\report.pdf`,
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if share.Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized share path, got %q", share.Path)
	}

	shares := store.GetByPath("/docs/report.pdf")
	if len(shares) != 1 || shares[0].ID != share.ID {
		t.Fatalf("expected normalized path index lookup to return created share, got %+v", shares)
	}
}

func TestShareStore_CreateRejectsInvalidInvariants(t *testing.T) {
	testCases := []struct {
		name    string
		opts    CreateShareOptions
		wantErr error
	}{
		{
			name:    "empty path",
			opts:    CreateShareOptions{Path: "   ", Type: ShareTypeFile, CreatedBy: "user1"},
			wantErr: errInvalidSharePath,
		},
		{
			name:    "traversal path",
			opts:    CreateShareOptions{Path: "../escape.txt", Type: ShareTypeFile, CreatedBy: "user1"},
			wantErr: errInvalidSharePath,
		},
		{
			name:    "dot segment path",
			opts:    CreateShareOptions{Path: "/docs/./report.pdf", Type: ShareTypeFile, CreatedBy: "user1"},
			wantErr: errInvalidSharePath,
		},
		{
			name:    "control character path",
			opts:    CreateShareOptions{Path: "/docs\a/report.pdf", Type: ShareTypeFile, CreatedBy: "user1"},
			wantErr: errInvalidSharePath,
		},
		{
			name:    "delete control character path",
			opts:    CreateShareOptions{Path: "/docs\x7f/report.pdf", Type: ShareTypeFile, CreatedBy: "user1"},
			wantErr: errInvalidSharePath,
		},
		{
			name:    "negative max access",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareTypeFile, CreatedBy: "user1", MaxAccess: -1},
			wantErr: errInvalidMaxAccess,
		},
		{
			name:    "invalid share type",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareType("device"), CreatedBy: "user1"},
			wantErr: errInvalidShareType,
		},
		{
			name:    "unsupported permission",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareTypeFile, CreatedBy: "user1", Permission: PermissionReadWrite},
			wantErr: errInvalidSharePermission,
		},
		{
			name:    "overlong password",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareTypeFile, CreatedBy: "user1", Password: strings.Repeat("a", maxSharePasswordBytes+1)},
			wantErr: errSharePasswordLong,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			storePath := filepath.Join(tempDir, "shares.json")

			store, err := NewShareStore(storePath)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			share, err := store.Create(tc.opts)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tc.wantErr)
			}
			if share != nil {
				t.Fatalf("expected failed create to return nil share, got %+v", share)
			}
			if got := len(store.ListAll()); got != 0 {
				t.Fatalf("expected no persisted shares after failed create, got %d", got)
			}
		})
	}
}

func TestNewShareStore_LoadPreservesWhitespaceInPath(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	createdAt := time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC)
	targetPath := "/docs/report.pdf "

	writeShareFixture(t, storePath, []*Share{{
		ID:         "share-with-space",
		Path:       targetPath,
		Type:       ShareTypeFile,
		CreatedBy:  "user1",
		CreatedAt:  createdAt,
		Permission: PermissionRead,
		Enabled:    true,
	}})

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to load share store: %v", err)
	}

	loaded, err := store.Get("share-with-space")
	if err != nil {
		t.Fatalf("failed to get loaded share: %v", err)
	}
	if loaded.Path != targetPath {
		t.Fatalf("expected loaded share path %q, got %q", targetPath, loaded.Path)
	}

	byPath := store.GetByPath(targetPath)
	if len(byPath) != 1 || byPath[0].ID != "share-with-space" {
		t.Fatalf("expected whitespace-preserving path index, got %+v", byPath)
	}

	reloadedData, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("failed to read shares file after load: %v", err)
	}
	reloaded, err := decodeShareStoreFile(reloadedData)
	if err != nil {
		t.Fatalf("failed to decode shares file after load: %v", err)
	}
	if len(reloaded.Shares) != 1 || reloaded.Shares[0].Path != targetPath {
		t.Fatalf("expected shares file to preserve trailing whitespace path, got %s", string(reloadedData))
	}
}

func TestShareStore_CreateWithExpiration(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	duration := 24 * time.Hour
	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		ExpiresIn: &duration,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if share.ExpiresAt == nil {
		t.Error("expiration should be set")
	}
	if !share.ExpiresAt.After(time.Now()) {
		t.Error("expiration should be in the future")
	}
}

func TestShareStore_PrepareCreateStartsLifetimeAtCommit(t *testing.T) {
	store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}
	duration := 24 * time.Hour
	prepared, err := store.PrepareCreate(CreateShareOptions{
		Path:      "/test/prepared.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		ExpiresIn: &duration,
	})
	if err != nil {
		t.Fatalf("PrepareCreate() error: %v", err)
	}
	if !prepared.share.CreatedAt.IsZero() || prepared.share.ExpiresAt != nil {
		t.Fatalf("prepared share consumed its lifetime before commit: %+v", prepared.share)
	}

	share, err := store.CommitPreparedCreate(prepared)
	if err != nil {
		t.Fatalf("CommitPreparedCreate() error: %v", err)
	}
	if share.CreatedAt.IsZero() || share.ExpiresAt == nil {
		t.Fatalf("committed share lifetime is incomplete: %+v", share)
	}
	if got := share.ExpiresAt.Sub(share.CreatedAt); got != duration {
		t.Fatalf("committed expiration interval = %s, want %s", got, duration)
	}
}

func TestNewShareStore_LoadNormalizesValidSharesAndDropsInvalidEntries(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	legacy := []*Share{
		{
			ID:         "valid",
			Path:       `docs\\report.pdf`,
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionReadWrite,
			Enabled:    true,
		},
		{
			ID:         "legacy-dot",
			Path:       "/docs/./dot.pdf",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
		{
			ID:         "invalid-path",
			Path:       "../escape.txt",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
		{
			ID:         "invalid-nul",
			Path:       "/docs/report\x00.pdf",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
		{
			ID:         "invalid/id",
			Path:       "/docs/bad-id.txt",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
		{
			ID:         "invalid-max-access",
			Path:       "/docs/limit.txt",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
			MaxAccess:  -1,
		},
		{
			ID:         "invalid-type",
			Path:       "/docs/invalid-type.txt",
			Type:       ShareType("device"),
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
		{
			ID:         "blank-path",
			Path:       "   ",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
	}
	data := marshalShareFixture(t, legacy)
	if err := os.WriteFile(storePath, data, 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	share, err := store.Get("valid")
	if err != nil {
		t.Fatalf("Get(valid) error: %v", err)
	}
	if share.Path != "/docs/report.pdf" {
		t.Fatalf("expected valid share path to normalize on load, got %q", share.Path)
	}
	if share.Permission != PermissionRead {
		t.Fatalf("expected unsupported permission to normalize to read, got %q", share.Permission)
	}
	legacyDot, err := store.Get("legacy-dot")
	if err != nil {
		t.Fatalf("Get(legacy-dot) error: %v", err)
	}
	if legacyDot.Path != "/docs/dot.pdf" {
		t.Fatalf("expected legacy dot-segment share path to normalize on load, got %q", legacyDot.Path)
	}

	if _, err := store.Get("invalid-path"); err != ErrShareNotFound {
		t.Fatalf("expected invalid path share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("invalid-nul"); err != ErrShareNotFound {
		t.Fatalf("expected invalid NUL share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("invalid/id"); err != ErrShareNotFound {
		t.Fatalf("expected invalid ID share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("invalid-max-access"); err != ErrShareNotFound {
		t.Fatalf("expected invalid max_access share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("invalid-type"); err != ErrShareNotFound {
		t.Fatalf("expected invalid type share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("blank-path"); err != ErrShareNotFound {
		t.Fatalf("expected blank path share to be dropped on load, got %v", err)
	}
	if shares := store.GetByPath("/docs/report.pdf"); len(shares) != 1 || shares[0].ID != "valid" {
		t.Fatalf("expected only normalized valid share in path index, got %+v", shares)
	}
	if shares := store.GetByPath("/docs/dot.pdf"); len(shares) != 1 || shares[0].ID != "legacy-dot" {
		t.Fatalf("expected normalized legacy-dot share in path index, got %+v", shares)
	}

	data, err = os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(shares.json) error: %v", err)
	}
	persisted := decodePersistedShareFixture(t, data)
	if len(persisted) != 2 {
		t.Fatalf("expected normalized shares file to contain two entries, got %d", len(persisted))
	}
	persistedByID := make(map[string]*Share, len(persisted))
	for _, share := range persisted {
		persistedByID[share.ID] = share
	}
	validPersisted, ok := persistedByID["valid"]
	if !ok {
		t.Fatalf("expected valid share to be persisted, got %+v", persistedByID)
	}
	if validPersisted.Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized valid share path to be persisted, got %q", validPersisted.Path)
	}
	if validPersisted.Permission != PermissionRead {
		t.Fatalf("expected normalized share permission to be persisted as read, got %q", validPersisted.Permission)
	}
	legacyDotPersisted, ok := persistedByID["legacy-dot"]
	if !ok {
		t.Fatalf("expected legacy-dot share to be persisted, got %+v", persistedByID)
	}
	if legacyDotPersisted.Path != "/docs/dot.pdf" {
		t.Fatalf("expected normalized legacy-dot share path to be persisted, got %q", legacyDotPersisted.Path)
	}
}

func TestNewShareStore_LoadRebuildsPathIndexAfterDuplicateIDs(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	createdAt := time.Date(2026, time.May, 21, 9, 0, 0, 0, time.UTC)

	writeShareFixture(t, storePath, []*Share{
		{
			ID:          "duplicate",
			Path:        "/old/report.pdf",
			Type:        ShareTypeFile,
			CreatedBy:   "user1",
			CreatedAt:   createdAt,
			Permission:  PermissionRead,
			Enabled:     true,
			Description: "old",
		},
		{
			ID:          "duplicate",
			Path:        "/docs/report.pdf",
			Type:        ShareTypeFile,
			CreatedBy:   "user1",
			CreatedAt:   createdAt.Add(time.Minute),
			Permission:  PermissionRead,
			Enabled:     true,
			Description: "middle",
		},
		{
			ID:          "duplicate",
			Path:        `docs\\report.pdf`,
			Type:        ShareTypeFile,
			CreatedBy:   "user1",
			CreatedAt:   createdAt.Add(2 * time.Minute),
			Permission:  PermissionReadWrite,
			Enabled:     true,
			Description: "latest",
		},
		{
			ID:         "other",
			Path:       "/docs/report.pdf",
			Type:       ShareTypeFile,
			CreatedBy:  "user2",
			CreatedAt:  createdAt.Add(3 * time.Minute),
			Permission: PermissionRead,
			Enabled:    true,
		},
	})

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	loaded, err := store.Get("duplicate")
	if err != nil {
		t.Fatalf("Get(duplicate) error: %v", err)
	}
	if loaded.Path != "/docs/report.pdf" || loaded.Description != "latest" || loaded.Permission != PermissionRead {
		t.Fatalf("expected duplicate share to keep final normalized entry, got %+v", loaded)
	}
	if shares := store.GetByPath("/old/report.pdf"); len(shares) != 0 {
		t.Fatalf("expected old duplicate path to be removed from path index, got %+v", shares)
	}

	shares := store.GetByPath("/docs/report.pdf")
	if len(shares) != 2 {
		t.Fatalf("expected two unique shares at normalized path, got %+v", shares)
	}
	seen := map[string]int{}
	for _, item := range shares {
		seen[item.ID]++
	}
	if seen["duplicate"] != 1 || seen["other"] != 1 {
		t.Fatalf("expected path index to contain each final share once, got counts %+v from %+v", seen, shares)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(shares.json) error: %v", err)
	}
	persisted := decodePersistedShareFixture(t, data)
	if len(persisted) != 2 {
		t.Fatalf("expected normalized shares file to contain two unique shares, got %d", len(persisted))
	}
}

func TestNewShareStore_LoadNormalizationIgnoresPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	legacy := []*Share{{
		ID:         "valid",
		Path:       `docs\\report.pdf`,
		Type:       ShareTypeFile,
		CreatedBy:  "user1",
		CreatedAt:  time.Now(),
		Permission: PermissionReadWrite,
		Enabled:    true,
	}}
	data := marshalShareFixture(t, legacy)
	if err := os.WriteFile(storePath, data, 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("expected NewShareStore() to tolerate normalization persistence warning, got %v", err)
	}

	share, err := store.Get("valid")
	if err != nil {
		t.Fatalf("Get(valid) error: %v", err)
	}
	if share.Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized share path after warning, got %q", share.Path)
	}
	if share.Permission != PermissionRead {
		t.Fatalf("expected normalized share permission after warning, got %q", share.Permission)
	}

	persistedData, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(shares.json) error: %v", err)
	}
	persisted := decodePersistedShareFixture(t, persistedData)
	if len(persisted) != 1 {
		t.Fatalf("expected normalized shares file to contain one entry after warning, got %d", len(persisted))
	}
	if persisted[0].Path != "/docs/report.pdf" {
		t.Fatalf("expected normalized share path to persist after warning, got %q", persisted[0].Path)
	}
	if persisted[0].Permission != PermissionRead {
		t.Fatalf("expected normalized share permission to persist after warning, got %q", persisted[0].Permission)
	}
}

func TestNewShareStore_RejectsNullShareEntry(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")
	if err := os.WriteFile(storePath, []byte(`{"version":1,"shares":[null],"trash_delete_operations":{},"trash_restore_operations":{}}`), 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	if _, err := NewShareStore(storePath); err == nil {
		t.Fatal("expected NewShareStore() to reject null share entries")
	} else if !strings.Contains(err.Error(), "null entry") {
		t.Fatalf("expected null entry error, got %v", err)
	}
}

func TestShareStore_Load_NormalizesUnsupportedPermissionToRead(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	legacy := []*Share{{
		ID:         "share-1",
		Path:       "/test/file.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user1",
		CreatedAt:  time.Now(),
		Permission: PermissionReadWrite,
		Enabled:    true,
	}}
	data := marshalShareFixture(t, legacy)
	if err := os.WriteFile(storePath, data, 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	share, err := store.Get("share-1")
	if err != nil {
		t.Fatalf("Get(share-1) error: %v", err)
	}
	if share.Permission != PermissionRead {
		t.Fatalf("expected legacy unsupported permission to normalize to read, got %q", share.Permission)
	}
}

func TestShareStore_CreateWithPassword(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret123",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if !share.HasPassword() {
		t.Error("share should have password")
	}
	if !share.CheckPassword("secret123") {
		t.Error("correct password should be accepted")
	}
	if share.CheckPassword("wrongpass") {
		t.Error("wrong password should be rejected")
	}
}

func TestShareStore_Get(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	created, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})

	share, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("failed to get share: %v", err)
	}

	if share.ID != created.ID {
		t.Errorf("expected ID %s, got %s", created.ID, share.ID)
	}
}

func TestShareStore_GetNotFound(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	_, err = store.Get("nonexistent")
	if err != ErrShareNotFound {
		t.Errorf("expected ErrShareNotFound, got %v", err)
	}
}

func TestShareStore_ListByUser_SortsNewestFirst(t *testing.T) {
	now := time.Now()
	store := &ShareStore{
		shares: map[string]*Share{
			"older": {
				ID:        "older",
				Path:      "/docs/older.txt",
				CreatedBy: "user1",
				CreatedAt: now.Add(-time.Hour),
			},
			"newer": {
				ID:        "newer",
				Path:      "/docs/newer.txt",
				CreatedBy: "user1",
				CreatedAt: now,
			},
			"other-user": {
				ID:        "other-user",
				Path:      "/docs/other.txt",
				CreatedBy: "user2",
				CreatedAt: now.Add(time.Hour),
			},
		},
		pathIdx: map[string][]string{},
	}

	shares := store.ListByUser("user1")
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares, got %d", len(shares))
	}
	if shares[0].ID != "newer" || shares[1].ID != "older" {
		t.Fatalf("expected newest-first ordering, got %q then %q", shares[0].ID, shares[1].ID)
	}
}

func TestShareStore_ListAll_SortsDeterministicallyWhenTimestampsMatch(t *testing.T) {
	createdAt := time.Now()
	store := &ShareStore{
		shares: map[string]*Share{
			"share-b": {
				ID:        "share-b",
				Path:      "/docs/b.txt",
				CreatedBy: "user1",
				CreatedAt: createdAt,
			},
			"share-a": {
				ID:        "share-a",
				Path:      "/docs/a.txt",
				CreatedBy: "user2",
				CreatedAt: createdAt,
			},
		},
		pathIdx: map[string][]string{},
	}

	shares := store.ListAll()
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares, got %d", len(shares))
	}
	if shares[0].ID != "share-a" || shares[1].ID != "share-b" {
		t.Fatalf("expected ID tie-break ordering, got %q then %q", shares[0].ID, shares[1].ID)
	}
}

func TestShareStore_GetByPath_SortsNewestFirst(t *testing.T) {
	now := time.Now()
	store := &ShareStore{
		shares: map[string]*Share{
			"older": {
				ID:        "older",
				Path:      "/docs/shared.txt",
				CreatedBy: "user1",
				CreatedAt: now.Add(-time.Hour),
			},
			"newer": {
				ID:        "newer",
				Path:      "/docs/shared.txt",
				CreatedBy: "user2",
				CreatedAt: now,
			},
		},
		pathIdx: map[string][]string{
			"/docs/shared.txt": {"older", "newer"},
		},
	}

	shares := store.GetByPath("/docs/shared.txt")
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares, got %d", len(shares))
	}
	if shares[0].ID != "newer" || shares[1].ID != "older" {
		t.Fatalf("expected newest-first path share ordering, got %q then %q", shares[0].ID, shares[1].ID)
	}
}

func TestShareStore_Delete(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})

	err = store.Delete(share.ID)
	if err != nil {
		t.Fatalf("failed to delete share: %v", err)
	}

	_, err = store.Get(share.ID)
	if err != ErrShareNotFound {
		t.Error("share should not exist after deletion")
	}
}

func TestShareStore_ListByUser(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.Create(CreateShareOptions{Path: "/file1.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	store.Create(CreateShareOptions{Path: "/file2.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	store.Create(CreateShareOptions{Path: "/file3.txt", Type: ShareTypeFile, CreatedBy: "user2"})

	user1Shares := store.ListByUser("user1")
	if len(user1Shares) != 2 {
		t.Errorf("expected 2 shares for user1, got %d", len(user1Shares))
	}

	user2Shares := store.ListByUser("user2")
	if len(user2Shares) != 1 {
		t.Errorf("expected 1 share for user2, got %d", len(user2Shares))
	}
}

func TestShareStore_Persistence(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store1, _ := NewShareStore(storePath)
	share, _ := store1.Create(CreateShareOptions{
		Path:        "/test/file.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Description: "Test share",
	})

	store2, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create second store: %v", err)
	}

	loaded, err := store2.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to get share from new store: %v", err)
	}

	if loaded.Description != "Test share" {
		t.Errorf("expected description 'Test share', got '%s'", loaded.Description)
	}
}

func TestShareStore_RejectsSymlinkPathOnLoad(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "real-shares.json")
	symlinkPath := filepath.Join(tempDir, "shares.json")

	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to write target store: %v", err)
	}
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := NewShareStore(symlinkPath)
	if !errors.Is(err, errShareStoreSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestShareStore_RejectsSymlinkParentDirectoryOnLoad(t *testing.T) {
	tempDir := t.TempDir()
	realDir := filepath.Join(tempDir, "real-shares")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real share dir: %v", err)
	}
	targetPath := filepath.Join(realDir, "shares.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to seed share store: %v", err)
	}
	linkedDir := filepath.Join(tempDir, "linked-shares")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create share dir symlink: %v", err)
	}

	_, err := NewShareStore(filepath.Join(linkedDir, "shares.json"))
	if !errors.Is(err, errShareStoreSymlink) {
		t.Fatalf("expected parent-directory symlink error, got %v", err)
	}
}

func TestNewShareStore_LoadRejectsStoreSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	sharesDir := filepath.Join(baseDir, "shares")
	if err := os.MkdirAll(sharesDir, 0755); err != nil {
		t.Fatalf("failed to create shares dir: %v", err)
	}
	storePath := filepath.Join(sharesDir, "shares.json")
	writeShareFixture(t, storePath, []*Share{{
		ID:         "original",
		Path:       "/docs/original.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user1",
		CreatedAt:  time.Now(),
		Permission: PermissionRead,
		Enabled:    true,
	}})
	linkedTarget := filepath.Join(sharesDir, "linked.json")
	writeShareFixture(t, linkedTarget, []*Share{{
		ID:         "linked",
		Path:       "/docs/linked.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user2",
		CreatedAt:  time.Now(),
		Permission: PermissionRead,
		Enabled:    true,
	}})

	originalHook := afterValidateShareStorePath
	var hookErr error
	swapped := false
	afterValidateShareStorePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		if err := os.Remove(storePath); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(filepath.Base(linkedTarget), storePath)
	}
	defer func() {
		afterValidateShareStorePath = originalHook
	}()

	_, err := NewShareStore(storePath)
	if hookErr != nil {
		t.Fatalf("afterValidateShareStorePath hook error: %v", hookErr)
	}
	if !errors.Is(err, errShareStoreSymlink) {
		t.Fatalf("expected share store symlink rejection, got %v", err)
	}
}

func TestNewShareStore_Load_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	sharesDir := filepath.Join(baseDir, "shares")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(sharesDir, 0755); err != nil {
		t.Fatalf("failed to create shares dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}

	writeShareFixture(t, filepath.Join(sharesDir, "shares.json"), []*Share{{
		ID:         "original",
		Path:       "/docs/original.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user1",
		CreatedAt:  time.Now(),
		Permission: PermissionRead,
		Enabled:    true,
	}})
	writeShareFixture(t, filepath.Join(outsideDir, "shares.json"), []*Share{{
		ID:         "outside",
		Path:       "/docs/outside.txt",
		Type:       ShareTypeFile,
		CreatedBy:  "user2",
		CreatedAt:  time.Now(),
		Permission: PermissionRead,
		Enabled:    true,
	}})

	originalHook := afterValidateShareStorePath
	var hookErr error
	swapped := false
	afterValidateShareStorePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "shares-backup")
		if err := os.Rename(sharesDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, sharesDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateShareStorePath = originalHook
	}()

	store, err := NewShareStore(filepath.Join(sharesDir, "shares.json"))
	if hookErr != nil {
		t.Fatalf("afterValidateShareStorePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected load to stay bound to the original directory, got %v", err)
	}

	shares := store.ListAll()
	if len(shares) != 1 || shares[0].ID != "original" {
		t.Fatalf("expected original shares file to be loaded, got %+v", shares)
	}
}

func TestShareStore_Create_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	sharesDir := filepath.Join(baseDir, "shares")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(sharesDir, 0755); err != nil {
		t.Fatalf("failed to create shares dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	writeShareFixture(t, filepath.Join(outsideDir, "shares.json"), []*Share{})

	store, err := NewShareStore(filepath.Join(sharesDir, "shares.json"))
	if err != nil {
		t.Fatalf("failed to create share store: %v", err)
	}

	originalHook := afterValidateShareStorePath
	var hookErr error
	swapped := false
	afterValidateShareStorePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "shares-backup")
		if err := os.Rename(sharesDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, sharesDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateShareStorePath = originalHook
	}()

	created, err := store.Create(CreateShareOptions{
		Path:      "/docs/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if hookErr != nil {
		t.Fatalf("afterValidateShareStorePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected create to stay bound to the original directory, got %v", err)
	}

	outsideStore, err := NewShareStore(filepath.Join(outsideDir, "shares.json"))
	if err != nil {
		t.Fatalf("failed to reload outside share store: %v", err)
	}
	if len(outsideStore.ListAll()) != 0 {
		t.Fatalf("expected outside share store to remain unchanged, got %+v", outsideStore.ListAll())
	}

	backupStore, err := NewShareStore(filepath.Join(baseDir, "shares-backup", "shares.json"))
	if err != nil {
		t.Fatalf("failed to reload original share store inode: %v", err)
	}
	loaded, err := backupStore.Get(created.ID)
	if err != nil {
		t.Fatalf("expected created share to persist in original directory inode, got %v", err)
	}
	if loaded.Path != "/docs/file.txt" {
		t.Fatalf("expected created share path to persist, got %q", loaded.Path)
	}
}

func TestNewShareStore_RecoverCorruptShares_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	sharesDir := filepath.Join(baseDir, "shares")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(sharesDir, 0755); err != nil {
		t.Fatalf("failed to create shares dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	storePath := filepath.Join(sharesDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("failed to seed corrupt shares file: %v", err)
	}
	writeShareFixture(t, filepath.Join(outsideDir, "shares.json"), []*Share{})

	originalHook := afterValidateShareStorePath
	var hookErr error
	swapped := false
	afterValidateShareStorePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		backupDir := filepath.Join(baseDir, "shares-backup")
		if err := os.Rename(sharesDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, sharesDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateShareStorePath = originalHook
	}()

	store, err := NewShareStore(storePath)
	if hookErr != nil {
		t.Fatalf("afterValidateShareStorePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected corrupt recovery to stay bound to the original directory, got %v", err)
	}
	if len(store.ListAll()) != 0 {
		t.Fatalf("expected recovered share store to be empty, got %+v", store.ListAll())
	}

	entries, err := os.ReadDir(filepath.Join(baseDir, "shares-backup"))
	if err != nil {
		t.Fatalf("failed to read backup directory: %v", err)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "shares.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt backup to remain in original directory inode")
	}

	outsideStore, err := NewShareStore(filepath.Join(outsideDir, "shares.json"))
	if err != nil {
		t.Fatalf("failed to reload outside share store: %v", err)
	}
	if len(outsideStore.ListAll()) != 0 {
		t.Fatalf("expected outside share store to remain unchanged, got %+v", outsideStore.ListAll())
	}
}

func TestShare_IsExpired(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	share := &Share{ExpiresAt: &future}
	if share.IsExpired() {
		t.Error("share with future expiration should not be expired")
	}

	past := time.Now().Add(-24 * time.Hour)
	share.ExpiresAt = &past
	if !share.IsExpired() {
		t.Error("share with past expiration should be expired")
	}

	share.ExpiresAt = nil
	if share.IsExpired() {
		t.Error("share with no expiration should not be expired")
	}
}

func TestShare_IsExpiredAtTreatsExactExpirationAsExpired(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	share := &Share{ExpiresAt: &now}

	if !share.isExpiredAt(now) {
		t.Fatal("share should be expired at its exact expiration instant")
	}
	if share.isExpiredAt(now.Add(-time.Nanosecond)) {
		t.Fatal("share should remain active before its expiration instant")
	}
}

func TestShare_CanAccess(t *testing.T) {
	share := &Share{Enabled: true}

	if err := share.CanAccess(); err != nil {
		t.Errorf("enabled share should be accessible: %v", err)
	}

	share.Enabled = false
	if err := share.CanAccess(); err != ErrShareDisabled {
		t.Errorf("disabled share should return ErrShareDisabled: %v", err)
	}

	share.Enabled = true
	past := time.Now().Add(-1 * time.Hour)
	share.ExpiresAt = &past
	if err := share.CanAccess(); err != ErrShareExpired {
		t.Errorf("expired share should return ErrShareExpired: %v", err)
	}

	share.ExpiresAt = nil
	share.MaxAccess = 1
	share.AccessCount = 1
	if err := share.CanAccess(); err != ErrShareAccessLimit {
		t.Errorf("access limited share should return ErrShareAccessLimit: %v", err)
	}
}

func TestShare_RiskIgnoresShareAtExactExpiration(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	share := &Share{
		Path:      "/docs",
		Type:      ShareTypeFolder,
		CreatedAt: now.Add(-31 * 24 * time.Hour),
		ExpiresAt: &now,
		Enabled:   true,
	}

	risk := share.Risk(now)
	if risk.Level != ShareRiskLevelNone || len(risk.Reasons) != 0 {
		t.Fatalf("expected no risk for exactly expired share, got %+v", risk)
	}
}

func TestShare_RiskReportsActiveWideOpenShareConcerns(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	share := &Share{
		Path:      "/",
		Type:      ShareTypeFolder,
		CreatedBy: "user1",
		CreatedAt: now.Add(-31 * 24 * time.Hour),
		Enabled:   true,
	}

	risk := share.Risk(now)
	if risk.Level != ShareRiskLevelHigh {
		t.Fatalf("expected high risk, got %+v", risk)
	}
	for _, code := range []string{"root_folder", "no_password", "no_expiration", "unlimited_access", "unused_enabled"} {
		if !shareRiskHasCode(risk, code) {
			t.Fatalf("expected risk code %q in %+v", code, risk)
		}
	}
}

func TestShare_RiskIgnoresInactiveShares(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)
	tests := []struct {
		name  string
		share Share
	}{
		{
			name: "disabled",
			share: Share{
				Path:    "/docs",
				Type:    ShareTypeFolder,
				Enabled: false,
			},
		},
		{
			name: "expired",
			share: Share{
				Path:      "/docs",
				Type:      ShareTypeFolder,
				Enabled:   true,
				ExpiresAt: &expiredAt,
			},
		},
		{
			name: "access limit reached",
			share: Share{
				Path:        "/docs",
				Type:        ShareTypeFolder,
				Enabled:     true,
				MaxAccess:   1,
				AccessCount: 1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			risk := tc.share.Risk(now)
			if risk.Level != ShareRiskLevelNone || len(risk.Reasons) != 0 {
				t.Fatalf("expected no risk for inactive share, got %+v", risk)
			}
		})
	}
}

func TestShare_RiskReportsStaleEnabledShareAsLow(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	lastAccess := now.Add(-91 * 24 * time.Hour)
	share := &Share{
		Path:         "/docs/report.pdf",
		Type:         ShareTypeFile,
		CreatedAt:    now.Add(-100 * 24 * time.Hour),
		ExpiresAt:    &expiresAt,
		PasswordHash: "configured",
		Enabled:      true,
		MaxAccess:    10,
		LastAccess:   &lastAccess,
	}

	risk := share.Risk(now)
	if risk.Level != ShareRiskLevelLow || !shareRiskHasCode(risk, "stale_enabled") || !shareRiskHasCode(risk, "expiring_soon") {
		t.Fatalf("expected stale low risk, got %+v", risk)
	}
}

func shareRiskHasCode(risk ShareRisk, code string) bool {
	for _, reason := range risk.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func TestShareStore_Access(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)

	share, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		Password:  "secret",
	})

	accessed, err := store.Access(share.ID, "secret")
	if err != nil {
		t.Fatalf("access with correct password failed: %v", err)
	}
	if accessed.AccessCount != 1 {
		t.Errorf("expected access count 1, got %d", accessed.AccessCount)
	}

	_, err = store.Access(share.ID, "wrong")
	if err != ErrInvalidPassword {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}
}

func TestShareStore_RecordAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := store.RecordAccess(share.ID); err != nil {
		t.Fatalf("RecordAccess() error: %v", err)
	}
	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get() after RecordAccess() error: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("AccessCount = %d, want 1", loaded.AccessCount)
	}
	if loaded.LastAccess == nil {
		t.Fatal("expected LastAccess to be recorded")
	}

	if err := store.RecordAccess(share.ID); err != ErrShareAccessLimit {
		t.Fatalf("second RecordAccess() error = %v, want %v", err, ErrShareAccessLimit)
	}
	if err := store.RecordAccess("missing"); err != ErrShareNotFound {
		t.Fatalf("missing RecordAccess() error = %v, want %v", err, ErrShareNotFound)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"1h", time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"", 0, false},
		{"invalid", 0, true},
		{"0d", 0, true},
		{"-106751992d", 0, true},
		{"106751992d", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestShareStore_Update(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)

	share, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})

	err := store.Update(share.ID, func(s *Share) error {
		s.Enabled = false
		s.Description = "Updated"
		return nil
	})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	updated, _ := store.Get(share.ID)
	if updated.Enabled {
		t.Error("share should be disabled")
	}
	if updated.Description != "Updated" {
		t.Errorf("expected description 'Updated', got '%s'", updated.Description)
	}
}

func TestShareStore_UpdateRejectsIDMutation(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore() error: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	updateErr := store.Update(share.ID, func(s *Share) error {
		s.ID = "replacement"
		s.Path = "/test/other.txt"
		return nil
	})
	if !errors.Is(updateErr, errShareIDImmutable) {
		t.Fatalf("Update() error = %v, want %v", updateErr, errShareIDImmutable)
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get(original) error: %v", err)
	}
	if loaded.ID != share.ID || loaded.Path != "/test/file.txt" {
		t.Fatalf("expected original share to remain unchanged, got %+v", loaded)
	}
	if _, err := store.Get("replacement"); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("Get(replacement) error = %v, want %v", err, ErrShareNotFound)
	}
	if shares := store.GetByPath("/test/other.txt"); len(shares) != 0 {
		t.Fatalf("expected rejected ID mutation not to create path index entry, got %+v", shares)
	}
}

func TestShareStore_GetByPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)

	store.Create(CreateShareOptions{Path: "/file.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	store.Create(CreateShareOptions{Path: "/file.txt", Type: ShareTypeFile, CreatedBy: "user2"})
	store.Create(CreateShareOptions{Path: "/other.txt", Type: ShareTypeFile, CreatedBy: "user1"})

	shares := store.GetByPath("/file.txt")
	if len(shares) != 2 {
		t.Errorf("expected 2 shares for path, got %d", len(shares))
	}
}

func TestShareStore_GetByPath_NormalizesInput(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)
	share, _ := store.Create(CreateShareOptions{Path: "/docs/report.txt", Type: ShareTypeFile, CreatedBy: "user1"})

	shares := store.GetByPath(`docs\\report.txt`)
	if len(shares) != 1 || shares[0].ID != share.ID {
		t.Fatalf("expected normalized lookup to find created share, got %+v", shares)
	}
}

func TestShare_ToInfo(t *testing.T) {
	now := time.Now()
	exp := now.Add(24 * time.Hour)
	share := &Share{
		ID:           "abc123",
		Path:         "/test.txt",
		Type:         ShareTypeFile,
		CreatedBy:    "user1",
		CreatedAt:    now,
		ExpiresAt:    &exp,
		PasswordHash: "somehash",
		Permission:   PermissionRead,
		Enabled:      true,
		AccessCount:  5,
		Description:  "Test",
	}

	info := share.ToInfo()

	if info.ID != share.ID {
		t.Errorf("ID mismatch")
	}
	if !info.HasPassword {
		t.Error("HasPassword should be true")
	}
}

func TestShareStore_AccessLimitReached(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)

	share, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 2,
	})

	_, err := store.Access(share.ID, "")
	if err != nil {
		t.Fatalf("first access failed: %v", err)
	}

	_, err = store.Access(share.ID, "")
	if err != nil {
		t.Fatalf("second access failed: %v", err)
	}

	_, err = store.Access(share.ID, "")
	if err != ErrShareAccessLimit {
		t.Errorf("expected ErrShareAccessLimit after max access, got %v", err)
	}
}

func TestShareStore_RecordAuthorizedAccess_EnforcesLimitAtomically(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, _ := NewShareStore(storePath)

	share, _ := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
		MaxAccess: 1,
	})

	accessed, err := store.RecordAuthorizedAccess(share.ID)
	if err != nil {
		t.Fatalf("first authorized access failed: %v", err)
	}
	if accessed.AccessCount != 1 {
		t.Fatalf("expected access count 1 after first access, got %d", accessed.AccessCount)
	}

	_, err = store.RecordAuthorizedAccess(share.ID)
	if err != ErrShareAccessLimit {
		t.Fatalf("expected ErrShareAccessLimit on second access, got %v", err)
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("expected access_count to stay at limit boundary, got %d", loaded.AccessCount)
	}
}

func TestShareStore_UpdateRollbackOnSaveFailure(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:        "/test/file.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Description: "original",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := os.Chmod(tempDir, 0500); err != nil {
		t.Fatalf("failed to set dir permissions: %v", err)
	}
	defer func() {
		_ = os.Chmod(tempDir, 0700)
	}()

	updateErr := store.Update(share.ID, func(s *Share) error {
		s.Description = "updated"
		return nil
	})
	if updateErr == nil {
		t.Fatalf("expected update to fail")
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to get share: %v", err)
	}
	if loaded.Description != "original" {
		t.Fatalf("expected description to roll back, got %q", loaded.Description)
	}
}

func TestShareStore_UpdatePathMaintainsIndex(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := store.Update(share.ID, func(s *Share) error {
		s.Path = "/test/renamed.txt"
		return nil
	}); err != nil {
		t.Fatalf("failed to update share path: %v", err)
	}

	if shares := store.GetByPath("/test/file.txt"); len(shares) != 0 {
		t.Fatalf("expected old path index to be empty, got %d shares", len(shares))
	}
	shares := store.GetByPath("/test/renamed.txt")
	if len(shares) != 1 {
		t.Fatalf("expected new path index to contain share, got %d shares", len(shares))
	}
	if shares[0].ID != share.ID {
		t.Fatalf("expected renamed share id %s, got %s", share.ID, shares[0].ID)
	}
}

func TestShareStore_UpdateRejectsInvalidInvariantsAndPreservesState(t *testing.T) {
	testCases := []struct {
		name      string
		mutate    func(*Share)
		wantErr   error
		checkPath bool
	}{
		{
			name: "invalid path",
			mutate: func(s *Share) {
				s.Path = "../escape.txt"
			},
			wantErr:   errInvalidSharePath,
			checkPath: true,
		},
		{
			name: "negative max access",
			mutate: func(s *Share) {
				s.MaxAccess = -1
			},
			wantErr: errInvalidMaxAccess,
		},
		{
			name: "invalid share type",
			mutate: func(s *Share) {
				s.Type = ShareType("device")
			},
			wantErr: errInvalidShareType,
		},
		{
			name: "unsupported permission",
			mutate: func(s *Share) {
				s.Permission = PermissionReadWrite
			},
			wantErr: errInvalidSharePermission,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			storePath := filepath.Join(tempDir, "shares.json")

			store, err := NewShareStore(storePath)
			if err != nil {
				t.Fatalf("failed to create store: %v", err)
			}

			share, err := store.Create(CreateShareOptions{
				Path:      "/docs/report.txt",
				Type:      ShareTypeFile,
				CreatedBy: "user1",
			})
			if err != nil {
				t.Fatalf("failed to create share: %v", err)
			}

			err = store.Update(share.ID, func(s *Share) error {
				tc.mutate(s)
				return nil
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Update() error = %v, want %v", err, tc.wantErr)
			}

			loaded, err := store.Get(share.ID)
			if err != nil {
				t.Fatalf("failed to reload share: %v", err)
			}
			if loaded.Path != "/docs/report.txt" {
				t.Fatalf("expected path to remain unchanged, got %q", loaded.Path)
			}
			if loaded.MaxAccess != 0 {
				t.Fatalf("expected max_access to remain unchanged, got %d", loaded.MaxAccess)
			}
			if loaded.Permission != PermissionRead {
				t.Fatalf("expected permission to remain read, got %q", loaded.Permission)
			}
			if tc.checkPath {
				if shares := store.GetByPath("/docs/report.txt"); len(shares) != 1 || shares[0].ID != share.ID {
					t.Fatalf("expected original path index to remain intact, got %+v", shares)
				}
				if shares := store.GetByPath("/escape.txt"); len(shares) != 0 {
					t.Fatalf("expected invalid path update not to create normalized index entries, got %d", len(shares))
				}
			}
		})
	}
}

func TestShareStore_UpdatePathReferences_RenamesDescendantShares(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	folderShare, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create folder share: %v", err)
	}
	fileShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}
	otherShare, err := store.Create(CreateShareOptions{Path: "/other.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create unrelated share: %v", err)
	}

	if err := store.UpdatePathReferences("/docs", "/archive/docs"); err != nil {
		t.Fatalf("UpdatePathReferences() error: %v", err)
	}

	if shares := store.GetByPath("/docs"); len(shares) != 0 {
		t.Fatalf("expected old folder path index to be empty, got %d shares", len(shares))
	}
	if shares := store.GetByPath("/docs/a.txt"); len(shares) != 0 {
		t.Fatalf("expected old file path index to be empty, got %d shares", len(shares))
	}

	renamedFolder, err := store.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(folderShare) error: %v", err)
	}
	if renamedFolder.Path != "/archive/docs" {
		t.Fatalf("expected folder share path to be rewritten, got %q", renamedFolder.Path)
	}
	renamedFile, err := store.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(fileShare) error: %v", err)
	}
	if renamedFile.Path != "/archive/docs/a.txt" {
		t.Fatalf("expected file share path to be rewritten, got %q", renamedFile.Path)
	}
	unaffected, err := store.Get(otherShare.ID)
	if err != nil {
		t.Fatalf("Get(otherShare) error: %v", err)
	}
	if unaffected.Path != "/other.txt" {
		t.Fatalf("expected unrelated share path to remain unchanged, got %q", unaffected.Path)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedFile, err := reloaded.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(reloaded fileShare) error: %v", err)
	}
	if reloadedFile.Path != "/archive/docs/a.txt" {
		t.Fatalf("expected rewritten path to persist, got %q", reloadedFile.Path)
	}
}

func TestShareStore_UpdatePathReferences_NormalizesInputPaths(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := store.UpdatePathReferences(`docs`, `archive\\docs`); err != nil {
		t.Fatalf("UpdatePathReferences() error: %v", err)
	}

	updated, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get(share) error: %v", err)
	}
	if updated.Path != "/archive/docs/a.txt" {
		t.Fatalf("expected normalized rewritten path, got %q", updated.Path)
	}
}

func TestShareStore_UpdatePathReferencesWithRestore_RestoresOnlyMovedShares(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	movedShare, err := store.Create(CreateShareOptions{Path: "/docs/report.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create moved share: %v", err)
	}
	unrelatedShare, err := store.Create(CreateShareOptions{Path: "/archive/docs/report.txt", Type: ShareTypeFile, CreatedBy: "user2"})
	if err != nil {
		t.Fatalf("failed to create unrelated share: %v", err)
	}

	originalShares, err := store.UpdatePathReferencesWithRestore("/docs/report.txt", "/archive/docs/report.txt")
	if err != nil {
		t.Fatalf("UpdatePathReferencesWithRestore() error: %v", err)
	}
	if len(originalShares) != 1 {
		t.Fatalf("expected one moved share in restore state, got %d", len(originalShares))
	}
	if originalShares[0].ID != movedShare.ID {
		t.Fatalf("expected restore state for moved share %q, got %q", movedShare.ID, originalShares[0].ID)
	}

	if err := store.RestoreShares(originalShares); err != nil {
		t.Fatalf("RestoreShares() error: %v", err)
	}

	restoredMovedShare, err := store.Get(movedShare.ID)
	if err != nil {
		t.Fatalf("Get(movedShare) error: %v", err)
	}
	if restoredMovedShare.Path != "/docs/report.txt" {
		t.Fatalf("expected moved share path rollback to /docs/report.txt, got %q", restoredMovedShare.Path)
	}
	restoredUnrelatedShare, err := store.Get(unrelatedShare.ID)
	if err != nil {
		t.Fatalf("Get(unrelatedShare) error: %v", err)
	}
	if restoredUnrelatedShare.Path != "/archive/docs/report.txt" {
		t.Fatalf("expected unrelated destination share path to remain unchanged, got %q", restoredUnrelatedShare.Path)
	}
	if shares := store.GetByPath("/docs/report.txt"); len(shares) != 1 || shares[0].ID != movedShare.ID {
		t.Fatalf("expected original path index to contain only moved share after restore, got %+v", shares)
	}
	if shares := store.GetByPath("/archive/docs/report.txt"); len(shares) != 1 || shares[0].ID != unrelatedShare.ID {
		t.Fatalf("expected destination path index to contain only unrelated share after restore, got %+v", shares)
	}
}

func TestShareStore_UpdatePathReferencesWithRestore_CommitsOnPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/report.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	originalShares, err := store.UpdatePathReferencesWithRestore("/docs/report.txt", "/archive/docs/report.txt")
	if !IsPersistenceWarning(err) {
		t.Fatalf("expected persistence warning, got %v", err)
	}
	if len(originalShares) != 1 || originalShares[0].ID != createdShare.ID {
		t.Fatalf("expected restore state for moved share, got %+v", originalShares)
	}

	updated, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(updated share) error: %v", err)
	}
	if updated.Path != "/archive/docs/report.txt" {
		t.Fatalf("expected moved share path after warning, got %q", updated.Path)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if reloadedShare.Path != "/archive/docs/report.txt" {
		t.Fatalf("expected persisted moved share path after warning, got %q", reloadedShare.Path)
	}
}

func TestShareStore_DisableSharesUnderPath_DisablesExactAndDescendantShares(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	folderShare, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create folder share: %v", err)
	}
	fileShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}
	otherShare, err := store.Create(CreateShareOptions{Path: "/other.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create unrelated share: %v", err)
	}

	if err := store.DisableSharesUnderPath("/docs"); err != nil {
		t.Fatalf("DisableSharesUnderPath() error: %v", err)
	}

	disabledFolder, err := store.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(folderShare) error: %v", err)
	}
	if disabledFolder.Enabled {
		t.Fatal("expected folder share to be disabled")
	}
	disabledFile, err := store.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(fileShare) error: %v", err)
	}
	if disabledFile.Enabled {
		t.Fatal("expected descendant file share to be disabled")
	}
	unaffected, err := store.Get(otherShare.ID)
	if err != nil {
		t.Fatalf("Get(otherShare) error: %v", err)
	}
	if !unaffected.Enabled {
		t.Fatal("expected unrelated share to remain enabled")
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedFolder, err := reloaded.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(reloaded folderShare) error: %v", err)
	}
	if reloadedFolder.Enabled {
		t.Fatal("expected disabled state to persist for folder share")
	}
}

func TestShareStore_DisableSharesUnderPathWithRestore_RestoresDisabledShares(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	folderShare, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create folder share: %v", err)
	}
	fileShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	disabled, err := store.DisableSharesUnderPathWithRestore("/docs")
	if err != nil {
		t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
	}
	if len(disabled) != 2 {
		t.Fatalf("expected two disabled shares in rollback state, got %d", len(disabled))
	}

	if err := store.RestoreShares(disabled); err != nil {
		t.Fatalf("RestoreShares() error: %v", err)
	}

	restoredFolder, err := store.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(restored folderShare) error: %v", err)
	}
	if !restoredFolder.Enabled {
		t.Fatal("expected folder share to be re-enabled after restore")
	}
	restoredFile, err := store.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(restored fileShare) error: %v", err)
	}
	if !restoredFile.Enabled {
		t.Fatal("expected file share to be re-enabled after restore")
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedFolder, err := reloaded.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(reloaded folderShare) error: %v", err)
	}
	if !reloadedFolder.Enabled {
		t.Fatal("expected restored enabled state to persist for folder share")
	}
}

func TestShareStore_DisableSharesUnderPathWithRestore_CommitsOnPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	folderShare, err := store.Create(CreateShareOptions{Path: "/docs", Type: ShareTypeFolder, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create folder share: %v", err)
	}
	fileShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	disabled, err := store.DisableSharesUnderPathWithRestore("/docs")
	if !IsPersistenceWarning(err) {
		t.Fatalf("expected persistence warning, got %v", err)
	}
	if len(disabled) != 2 {
		t.Fatalf("expected two disabled shares in restore state, got %d", len(disabled))
	}

	updatedFolder, err := store.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(updated folderShare) error: %v", err)
	}
	if updatedFolder.Enabled {
		t.Fatal("expected folder share to be disabled after warning")
	}
	updatedFile, err := store.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(updated fileShare) error: %v", err)
	}
	if updatedFile.Enabled {
		t.Fatal("expected file share to be disabled after warning")
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedFolder, err := reloaded.Get(folderShare.ID)
	if err != nil {
		t.Fatalf("Get(reloaded folderShare) error: %v", err)
	}
	if reloadedFolder.Enabled {
		t.Fatal("expected persisted folder share to remain disabled after warning")
	}
	reloadedFile, err := reloaded.Get(fileShare.ID)
	if err != nil {
		t.Fatalf("Get(reloaded fileShare) error: %v", err)
	}
	if reloadedFile.Enabled {
		t.Fatal("expected persisted file share to remain disabled after warning")
	}
}

func TestShareStore_DisableSharesUnderPathWithRestore_ReturnsCanonicalOrder(t *testing.T) {
	expectedPaths := []string{"/docs/a.txt", "/docs/b.txt", "/docs/c.txt"}

	for i := 0; i < 32; i++ {
		store, err := NewShareStore(filepath.Join(t.TempDir(), "shares.json"))
		if err != nil {
			t.Fatalf("NewShareStore() error: %v", err)
		}

		if _, err := store.Create(CreateShareOptions{Path: "/docs/c.txt", Type: ShareTypeFile, CreatedBy: "user-b"}); err != nil {
			t.Fatalf("Create(/docs/c.txt) error: %v", err)
		}
		if _, err := store.Create(CreateShareOptions{Path: "/docs/b.txt", Type: ShareTypeFile, CreatedBy: "user-a"}); err != nil {
			t.Fatalf("Create(/docs/b.txt) error: %v", err)
		}
		if _, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user-a"}); err != nil {
			t.Fatalf("Create(/docs/a.txt) error: %v", err)
		}

		disabled, err := store.DisableSharesUnderPathWithRestore("/docs")
		if err != nil {
			t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
		}
		if len(disabled) != len(expectedPaths) {
			t.Fatalf("disabled share count = %d, want %d", len(disabled), len(expectedPaths))
		}
		for index, expectedPath := range expectedPaths {
			if disabled[index].Path != expectedPath {
				t.Fatalf("disabled order at iteration %d = [%s %s %s], want [%s %s %s]",
					i,
					disabled[0].Path,
					disabled[1].Path,
					disabled[2].Path,
					expectedPaths[0],
					expectedPaths[1],
					expectedPaths[2],
				)
			}
		}
	}
}

func TestShareStore_RestoreDisabledSharesPreservingCurrent_PreservesNewerMetadata(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1", Description: "original"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	disabled, err := store.DisableSharesUnderPathWithRestore("/docs/a.txt")
	if err != nil {
		t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
	}
	if len(disabled) != 1 {
		t.Fatalf("expected one disabled share in restore state, got %d", len(disabled))
	}

	if err := store.Update(createdShare.ID, func(updated *Share) error {
		updated.Description = "newer"
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	if err := store.RestoreDisabledSharesPreservingCurrent(disabled); err != nil {
		t.Fatalf("RestoreDisabledSharesPreservingCurrent() error: %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if !restoredShare.Enabled {
		t.Fatal("expected share to be re-enabled after rollback restore")
	}
	if restoredShare.Description != "newer" {
		t.Fatalf("expected newer description to be preserved, got %q", restoredShare.Description)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if !reloadedShare.Enabled {
		t.Fatal("expected persisted share to stay enabled after reload")
	}
	if reloadedShare.Description != "newer" {
		t.Fatalf("expected persisted newer description to be preserved, got %q", reloadedShare.Description)
	}
}

func TestShareStore_RestoreDisabledSharesPreservingCurrent_CommitsOnPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	disabled, err := store.DisableSharesUnderPathWithRestore("/docs/a.txt")
	if err != nil {
		t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
	}
	if len(disabled) != 1 {
		t.Fatalf("expected one disabled share in restore state, got %d", len(disabled))
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	if err := store.RestoreDisabledSharesPreservingCurrent(disabled); !IsPersistenceWarning(err) {
		t.Fatalf("expected persistence warning, got %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if !restoredShare.Enabled {
		t.Fatal("expected share to be re-enabled after warning rollback restore")
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if !reloadedShare.Enabled {
		t.Fatal("expected persisted share to stay enabled after warning rollback restore")
	}
}

func TestShareStore_RestoreShares_RewritesPathsFromRestoreState(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	disabled, err := store.DisableSharesUnderPathWithRestore("/docs/a.txt")
	if err != nil {
		t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
	}
	if len(disabled) != 1 {
		t.Fatalf("expected one disabled share in restore state, got %d", len(disabled))
	}

	relocated := copyShare(disabled[0])
	relocated.Path = "/restored/a.txt"
	if err := store.RestoreShares([]*Share{relocated}); err != nil {
		t.Fatalf("RestoreShares() error: %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if !restoredShare.Enabled {
		t.Fatal("expected restored share to be enabled")
	}
	if restoredShare.Path != "/restored/a.txt" {
		t.Fatalf("expected restored share path %q, got %q", "/restored/a.txt", restoredShare.Path)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if reloadedShare.Path != "/restored/a.txt" {
		t.Fatalf("expected reloaded share path %q, got %q", "/restored/a.txt", reloadedShare.Path)
	}
}

func TestShareStore_RestoreShares_NormalizesRestoreStateInvariants(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	restoreState, err := store.DisableSharesUnderPathWithRestore("/docs/a.txt")
	if err != nil {
		t.Fatalf("DisableSharesUnderPathWithRestore() error: %v", err)
	}
	if len(restoreState) != 1 {
		t.Fatalf("expected one disabled share in restore state, got %d", len(restoreState))
	}

	legacy := copyShare(restoreState[0])
	legacy.Path = `docs\restored.txt`
	legacy.Permission = PermissionReadWrite

	if err := store.RestoreShares([]*Share{legacy}); err != nil {
		t.Fatalf("RestoreShares() error: %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if restoredShare.Path != "/docs/restored.txt" {
		t.Fatalf("expected restored share path %q, got %q", "/docs/restored.txt", restoredShare.Path)
	}
	if restoredShare.Permission != PermissionRead {
		t.Fatalf("expected restored share permission %q, got %q", PermissionRead, restoredShare.Permission)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if reloadedShare.Path != "/docs/restored.txt" {
		t.Fatalf("expected reloaded share path %q, got %q", "/docs/restored.txt", reloadedShare.Path)
	}
	if reloadedShare.Permission != PermissionRead {
		t.Fatalf("expected reloaded share permission %q, got %q", PermissionRead, reloadedShare.Permission)
	}
}

func TestShareStore_RestoreMovedSharesPreservingCurrent_PreservesNewerMetadata(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1", Description: "original"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	originalShares, err := store.UpdatePathReferencesWithRestore("/docs/a.txt", "/archive/a.txt")
	if err != nil {
		t.Fatalf("UpdatePathReferencesWithRestore() error: %v", err)
	}
	if len(originalShares) != 1 {
		t.Fatalf("expected one moved share in restore state, got %d", len(originalShares))
	}

	if err := store.Update(createdShare.ID, func(updated *Share) error {
		updated.Description = "newer"
		return nil
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	if err := store.RestoreMovedSharesPreservingCurrent(originalShares); err != nil {
		t.Fatalf("RestoreMovedSharesPreservingCurrent() error: %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if restoredShare.Path != "/docs/a.txt" {
		t.Fatalf("expected restored share path %q, got %q", "/docs/a.txt", restoredShare.Path)
	}
	if restoredShare.Description != "newer" {
		t.Fatalf("expected newer description to be preserved, got %q", restoredShare.Description)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if reloadedShare.Path != "/docs/a.txt" {
		t.Fatalf("expected persisted share path %q, got %q", "/docs/a.txt", reloadedShare.Path)
	}
	if reloadedShare.Description != "newer" {
		t.Fatalf("expected persisted newer description to be preserved, got %q", reloadedShare.Description)
	}
}

func TestShareStore_RestoreMovedSharesPreservingCurrent_CommitsOnPersistenceWarning(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	createdShare, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create file share: %v", err)
	}

	originalShares, err := store.UpdatePathReferencesWithRestore("/docs/a.txt", "/archive/a.txt")
	if err != nil {
		t.Fatalf("UpdatePathReferencesWithRestore() error: %v", err)
	}
	if len(originalShares) != 1 {
		t.Fatalf("expected one moved share in restore state, got %d", len(originalShares))
	}

	originalSyncShareStoreRootDir := syncShareStoreRootDir
	syncShareStoreRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreRootDir = originalSyncShareStoreRootDir
	}()

	if err := store.RestoreMovedSharesPreservingCurrent(originalShares); !IsPersistenceWarning(err) {
		t.Fatalf("expected persistence warning, got %v", err)
	}

	restoredShare, err := store.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(restoredShare) error: %v", err)
	}
	if restoredShare.Path != "/docs/a.txt" {
		t.Fatalf("expected share path restored after warning, got %q", restoredShare.Path)
	}

	reloaded, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("NewShareStore(reload) error: %v", err)
	}
	reloadedShare, err := reloaded.Get(createdShare.ID)
	if err != nil {
		t.Fatalf("Get(reloadedShare) error: %v", err)
	}
	if reloadedShare.Path != "/docs/a.txt" {
		t.Fatalf("expected persisted share path restored after warning, got %q", reloadedShare.Path)
	}
}

func TestShareStore_RestoreSharesPreservingCurrent_PreservesMutableMetadata(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	current, err := store.Create(CreateShareOptions{
		Path:        "/current/path.txt",
		Type:        ShareTypeFile,
		CreatedBy:   "user1",
		Description: "current description",
	})
	if err != nil {
		t.Fatalf("Create(current) error: %v", err)
	}
	restoreState := copyShare(current)
	restoreState.Path = "/restored/path.txt"
	restoreState.Enabled = false
	restoreState.Description = "old description"

	missing, err := store.Create(CreateShareOptions{
		Path:      "/missing/path.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("Create(missing) error: %v", err)
	}
	if err := store.Delete(missing.ID); err != nil {
		t.Fatalf("Delete(missing) error: %v", err)
	}

	if err := store.RestoreSharesPreservingCurrent([]*Share{restoreState, missing}); err != nil {
		t.Fatalf("RestoreSharesPreservingCurrent() error: %v", err)
	}

	loaded, err := store.Get(current.ID)
	if err != nil {
		t.Fatalf("Get(current) error: %v", err)
	}
	if loaded.Path != "/restored/path.txt" {
		t.Fatalf("Path = %q, want restored path", loaded.Path)
	}
	if loaded.Enabled {
		t.Fatal("expected enabled state to be restored to false")
	}
	if loaded.Description != "current description" {
		t.Fatalf("Description = %q, want current metadata preserved", loaded.Description)
	}
	if shares := store.GetByPath("/current/path.txt"); len(shares) != 0 {
		t.Fatalf("old path index still contains share: %+v", shares)
	}
	if shares := store.GetByPath("/restored/path.txt"); len(shares) != 1 || shares[0].ID != current.ID {
		t.Fatalf("restored path index = %+v", shares)
	}
	if _, err := store.Get(missing.ID); err != nil {
		t.Fatalf("missing restore state should be recreated: %v", err)
	}
}

func TestShareStore_DisableSharesUnderPath_NormalizesInputPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{Path: "/docs/a.txt", Type: ShareTypeFile, CreatedBy: "user1"})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := store.DisableSharesUnderPath(`docs`); err != nil {
		t.Fatalf("DisableSharesUnderPath() error: %v", err)
	}

	disabled, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("Get(share) error: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("expected normalized target path to disable descendant share")
	}
}

func TestShareStore_CreateRollbackOnSaveFailure(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.filePath = tempDir
	created, createErr := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if createErr == nil {
		t.Fatalf("expected create to fail")
	}
	if created != nil {
		t.Fatalf("expected failed create to return nil share")
	}
	if len(store.shares) != 0 {
		t.Fatalf("expected shares map to remain empty, got %d entries", len(store.shares))
	}
	if ids := store.pathIdx["/test/file.txt"]; len(ids) != 0 {
		t.Fatalf("expected path index rollback to remove stale ids, got %v", ids)
	}
	if shares := store.GetByPath("/test/file.txt"); len(shares) != 0 {
		t.Fatalf("expected no shares by path after rollback, got %d", len(shares))
	}
}

func TestShareStore_Create_DoesNotFollowSymlinkPath(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	targetPath := filepath.Join(tempDir, "real-shares.json")
	if err := os.WriteFile(targetPath, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to write target store: %v", err)
	}
	symlinkPath := filepath.Join(tempDir, "shares-link.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	store.filePath = symlinkPath

	created, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("expected create to stay within the original directory root, got %v", err)
	}
	if created == nil {
		t.Fatal("expected successful create to return a share")
	}
	targetBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read symlink target: %v", err)
	}
	if string(targetBytes) != "[]" {
		t.Fatalf("expected symlink target to remain unchanged, got %q", string(targetBytes))
	}
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("failed to stat replaced symlink path: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected symlink path to be replaced with a regular file inside the original root")
	}
	reloaded, err := NewShareStore(symlinkPath)
	if err != nil {
		t.Fatalf("failed to reload replaced share store path: %v", err)
	}
	if _, err := reloaded.Get(created.ID); err != nil {
		t.Fatalf("expected created share to persist at replaced symlink path, got %v", err)
	}
}

func TestShareStore_AccessRollbackOnSaveFailure(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := os.Chmod(tempDir, 0500); err != nil {
		t.Fatalf("failed to set dir permissions: %v", err)
	}
	defer func() {
		_ = os.Chmod(tempDir, 0700)
	}()

	_, accessErr := store.Access(share.ID, "")
	if accessErr == nil {
		t.Fatalf("expected access to fail")
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to get share: %v", err)
	}
	if loaded.AccessCount != 0 {
		t.Fatalf("expected access_count to roll back, got %d", loaded.AccessCount)
	}
}

func TestShareStore_RollbackAuthorizedAccess_RestoresPreviousState(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	first, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	accessed, err := store.RecordAuthorizedAccess(first.ID)
	if err != nil {
		t.Fatalf("failed to record initial access: %v", err)
	}
	if accessed.LastAccess == nil {
		t.Fatal("expected initial access to set LastAccess")
	}
	previousLastAccess := *accessed.LastAccess

	_, reservation, err := store.reserveAuthorizedAccess(first.ID)
	if err != nil {
		t.Fatalf("failed to reserve authorized access: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected access reservation")
	}

	if err := store.rollbackAuthorizedAccess(reservation); err != nil {
		t.Fatalf("failed to rollback authorized access: %v", err)
	}

	loaded, err := store.Get(first.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("expected access_count to restore to 1, got %d", loaded.AccessCount)
	}
	if loaded.LastAccess == nil || !loaded.LastAccess.Equal(previousLastAccess) {
		t.Fatalf("expected last_access to restore to %v, got %v", previousLastAccess, loaded.LastAccess)
	}
}

func TestShareStore_RollbackAuthorizedAccess_PreservesNewerLastAccess(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	_, firstReservation, err := store.reserveAuthorizedAccess(share.ID)
	if err != nil {
		t.Fatalf("failed to reserve first access: %v", err)
	}
	if firstReservation == nil {
		t.Fatal("expected first access reservation")
	}
	secondAccess, err := store.RecordAuthorizedAccess(share.ID)
	if err != nil {
		t.Fatalf("failed to record second access: %v", err)
	}

	if err := store.rollbackAuthorizedAccess(firstReservation); err != nil {
		t.Fatalf("failed to rollback first reservation: %v", err)
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("expected one surviving access after rollback, got %d", loaded.AccessCount)
	}
	if loaded.LastAccess == nil || secondAccess.LastAccess == nil || !loaded.LastAccess.Equal(*secondAccess.LastAccess) {
		t.Fatalf("expected newer last_access to be preserved, got %v want %v", loaded.LastAccess, secondAccess.LastAccess)
	}
}

func TestShareStore_RollbackAuthorizedAccess_FailsClosedWhenSaveFails(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	_, reservation, err := store.reserveAuthorizedAccess(share.ID)
	if err != nil {
		t.Fatalf("failed to reserve authorized access: %v", err)
	}
	if reservation == nil {
		t.Fatal("expected access reservation")
	}

	if err := os.Chmod(tempDir, 0500); err != nil {
		t.Fatalf("failed to set dir permissions: %v", err)
	}
	defer func() {
		_ = os.Chmod(tempDir, 0700)
	}()

	rollbackErr := store.rollbackAuthorizedAccess(reservation)
	if rollbackErr == nil {
		t.Fatal("expected rollbackAuthorizedAccess to fail when save fails")
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("expected reserved access to remain in memory when rollback save fails, got access_count %d", loaded.AccessCount)
	}
	if loaded.LastAccess == nil || !loaded.LastAccess.Equal(reservation.currentLastAccess) {
		t.Fatalf("expected last_access to remain at reserved access time %v, got %v", reservation.currentLastAccess, loaded.LastAccess)
	}
}

func TestShareStore_DeleteRollbackOnSaveFailure(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	if err := os.Chmod(tempDir, 0500); err != nil {
		t.Fatalf("failed to set dir permissions: %v", err)
	}
	defer func() {
		_ = os.Chmod(tempDir, 0700)
	}()

	deleteErr := store.Delete(share.ID)
	if deleteErr == nil {
		t.Fatalf("expected delete to fail")
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("expected share to remain after rollback: %v", err)
	}
	if loaded.Path != share.Path {
		t.Fatalf("expected share to remain after rollback")
	}
}

func TestShareStore_GetDoesNotBlockWhileAuthorizedAccessPersists(t *testing.T) {
	const waitTimeout = 5 * time.Second

	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	share, err := store.Create(CreateShareOptions{
		Path:      "/test/file.txt",
		Type:      ShareTypeFile,
		CreatedBy: "user1",
	})
	if err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	originalWriter := shareStoreWriter
	writerStarted := make(chan struct{})
	writerRelease := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	shareStoreWriter = func(path string, data []byte) error {
		startOnce.Do(func() {
			close(writerStarted)
		})
		<-writerRelease
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		shareStoreWriter = originalWriter
		releaseOnce.Do(func() {
			close(writerRelease)
		})
	})

	accessDone := make(chan error, 1)
	go func() {
		_, accessErr := store.RecordAuthorizedAccess(share.ID)
		accessDone <- accessErr
	}()

	select {
	case <-writerStarted:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for share store write to start")
	}

	getDone := make(chan struct{})
	go func() {
		loaded, getErr := store.Get(share.ID)
		if getErr != nil {
			t.Errorf("Get() error during pending persist: %v", getErr)
		} else if loaded.AccessCount != 0 {
			t.Errorf("expected reads during pending persist to observe committed access_count 0, got %d", loaded.AccessCount)
		}
		close(getDone)
	}()

	select {
	case <-getDone:
	case <-time.After(waitTimeout):
		t.Fatal("Get() blocked on an in-flight authorized access save")
	}

	releaseOnce.Do(func() {
		close(writerRelease)
	})

	select {
	case accessErr := <-accessDone:
		if accessErr != nil {
			t.Fatalf("RecordAuthorizedAccess() error: %v", accessErr)
		}
	case <-time.After(waitTimeout):
		t.Fatal("RecordAuthorizedAccess() did not finish after releasing writer")
	}

	loaded, err := store.Get(share.ID)
	if err != nil {
		t.Fatalf("failed to reload share after access: %v", err)
	}
	if loaded.AccessCount != 1 {
		t.Fatalf("expected committed access_count 1 after save, got %d", loaded.AccessCount)
	}
}

func TestShareStore_ConcurrentWritesSerializePersistence(t *testing.T) {
	const waitTimeout = 5 * time.Second

	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")

	store, err := NewShareStore(storePath)
	if err != nil {
		t.Fatalf("failed to create share store: %v", err)
	}

	originalWriter := shareStoreWriter
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var startFirstOnce sync.Once
	var releaseFirstOnce sync.Once
	var startSecondOnce sync.Once
	var callCount int32
	shareStoreWriter = func(path string, data []byte) error {
		call := atomic.AddInt32(&callCount, 1)
		switch call {
		case 1:
			startFirstOnce.Do(func() {
				close(firstStarted)
			})
			<-firstRelease
		case 2:
			startSecondOnce.Do(func() {
				close(secondStarted)
			})
		}
		return originalWriter(path, data)
	}
	t.Cleanup(func() {
		shareStoreWriter = originalWriter
		startFirstOnce.Do(func() {
			close(firstStarted)
		})
		startSecondOnce.Do(func() {
			close(secondStarted)
		})
		releaseFirstOnce.Do(func() {
			close(firstRelease)
		})
	})

	firstDone := make(chan error, 1)
	go func() {
		_, createErr := store.Create(CreateShareOptions{Path: "/docs/first.txt", Type: ShareTypeFile, CreatedBy: "user1"})
		firstDone <- createErr
	}()

	select {
	case <-firstStarted:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for first share persist to start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, createErr := store.Create(CreateShareOptions{Path: "/docs/second.txt", Type: ShareTypeFile, CreatedBy: "user1"})
		secondDone <- createErr
	}()

	select {
	case <-secondStarted:
		t.Fatal("second share persist started before first persist completed")
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirstOnce.Do(func() {
		close(firstRelease)
	})

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Create() error: %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("first Create() did not finish after releasing writer")
	}

	select {
	case <-secondStarted:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for second share persist to start")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Create() error: %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("second Create() did not finish")
	}

	shares := store.ListByUser("user1")
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares after serialized persists, got %d", len(shares))
	}
}
