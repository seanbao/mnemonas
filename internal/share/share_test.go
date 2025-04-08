package share

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestShareStore_RollbackAuthorizedAccess_PreservesRollbackStateWhenSaveFails(t *testing.T) {
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
	if loaded.AccessCount != 0 {
		t.Fatalf("expected rollback state to remain in memory despite save failure, got access_count %d", loaded.AccessCount)
	}
	if loaded.LastAccess != nil {
		t.Fatalf("expected last_access to roll back to nil despite save failure, got %v", loaded.LastAccess)
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
