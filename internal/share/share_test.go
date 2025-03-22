package share

import (
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

func TestWriteShareStoreFile_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")

	originalSyncShareStoreDir := syncShareStoreDir
	syncShareStoreDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncShareStoreDir = originalSyncShareStoreDir
	}()

	err := writeShareStoreFile(storePath, []byte("[]"))
	if err == nil {
		t.Fatal("expected writeShareStoreFile() to fail when directory sync fails")
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
}

func TestNewShareStore_ReturnsErrorWhenCorruptSharesBackupSyncFails(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "shares.json")
	if err := os.WriteFile(storePath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(shares.json) error: %v", err)
	}

	originalSyncShareStoreDir := syncShareStoreDir
	syncFailed := false
	syncShareStoreDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncShareStoreDir = originalSyncShareStoreDir
	}()

	if _, err := NewShareStore(storePath); err == nil {
		t.Fatal("expected NewShareStore() to fail when corrupt shares backup sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt shares directory") {
		t.Fatalf("expected corrupt shares sync failure in error, got %v", err)
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
			name:    "negative max access",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareTypeFile, CreatedBy: "user1", MaxAccess: -1},
			wantErr: errInvalidMaxAccess,
		},
		{
			name:    "unsupported permission",
			opts:    CreateShareOptions{Path: "/test/file.txt", Type: ShareTypeFile, CreatedBy: "user1", Permission: PermissionReadWrite},
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
			ID:         "invalid-path",
			Path:       "../escape.txt",
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
			ID:         "blank-path",
			Path:       "   ",
			Type:       ShareTypeFile,
			CreatedBy:  "user1",
			CreatedAt:  time.Now(),
			Permission: PermissionRead,
			Enabled:    true,
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal(legacy shares) error: %v", err)
	}
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

	if _, err := store.Get("invalid-path"); err != ErrShareNotFound {
		t.Fatalf("expected invalid path share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("invalid-max-access"); err != ErrShareNotFound {
		t.Fatalf("expected invalid max_access share to be dropped on load, got %v", err)
	}
	if _, err := store.Get("blank-path"); err != ErrShareNotFound {
		t.Fatalf("expected blank path share to be dropped on load, got %v", err)
	}
	if shares := store.GetByPath("/docs/report.pdf"); len(shares) != 1 || shares[0].ID != "valid" {
		t.Fatalf("expected only normalized valid share in path index, got %+v", shares)
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
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal(legacy shares) error: %v", err)
	}
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

func TestShareStore_CreateRejectsSymlinkPath(t *testing.T) {
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
	if !errors.Is(err, errShareStoreSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if created != nil {
		t.Fatal("expected failed create to return nil share")
	}
	if len(store.shares) != 0 {
		t.Fatalf("expected shares map to remain empty, got %d entries", len(store.shares))
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("first Create() did not finish after releasing writer")
	}

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second share persist to start")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Create() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Create() did not finish")
	}

	shares := store.ListByUser("user1")
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares after serialized persists, got %d", len(shares))
	}
}
