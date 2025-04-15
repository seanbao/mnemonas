package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func TestWriteAuthFileAtomically_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "users.json")

	originalSyncAuthFileDir := syncAuthFileDir
	syncAuthFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncAuthFileDir = originalSyncAuthFileDir
	}()

	err := writeAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if err == nil {
		t.Fatal("expected writeAuthFileAtomically() to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync users directory") {
		t.Fatalf("expected directory sync error, got %v", err)
	}

	data, readErr := os.ReadFile(usersPath)
	if readErr != nil {
		t.Fatalf("expected users file to remain readable after sync failure, got %v", readErr)
	}
	if string(data) != "[]" {
		t.Fatalf("expected users content to be preserved, got %q", string(data))
	}
	info, statErr := os.Stat(usersPath)
	if statErr != nil {
		t.Fatalf("expected users file to exist after sync failure, got %v", statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected users file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestWriteAuthFileAtomically_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "nested", "state", "users.json")

	originalSyncAuthFileDir := syncAuthFileDir
	syncAuthFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncAuthFileDir = originalSyncAuthFileDir
	}()

	err := writeAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if err == nil {
		t.Fatal("expected writeAuthFileAtomically() to fail when directory tree sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync users directory tree") {
		t.Fatalf("expected directory tree sync error, got %v", err)
	}
	if _, statErr := os.Stat(usersPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no users file to be created, got %v", statErr)
	}
}

func TestWriteRegisteredAuthFileAtomically_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "nested", "state", "users.json")
	usersDir := filepath.Dir(usersPath)

	originalHook := afterValidateAuthFilePath
	var hookErr error
	hookApplied := false
	afterValidateAuthFilePath = func() {
		if hookApplied || hookErr != nil {
			return
		}
		hookApplied = true
		hookErr = os.Chmod(usersDir, 0500)
	}
	defer func() {
		afterValidateAuthFilePath = originalHook
		_ = os.Chmod(usersDir, 0755)
	}()

	err := writeRegisteredAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if hookErr != nil {
		t.Fatalf("afterValidateAuthFilePath hook error: %v", hookErr)
	}
	if err == nil {
		t.Fatal("expected writeRegisteredAuthFileAtomically() to fail when temp file creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create temp users file") {
		t.Fatalf("expected temp create error, got %v", err)
	}
	if _, statErr := os.Stat(usersPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no users file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(usersDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created users directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "nested")); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent directory to be removed, got %v", statErr)
	}

	afterValidateAuthFilePath = originalHook
	if err := writeRegisteredAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users"); err != nil {
		t.Fatalf("expected retry after failed write cleanup to succeed, got %v", err)
	}
}

func TestUserStore_SaveUserState_PersistsCanonicalOrder(t *testing.T) {
	usersPath := filepath.Join(t.TempDir(), "users.json")
	users := map[string]*User{
		"user-zeta":  {ID: "user-zeta", Username: "zeta"},
		"user-alpha": {ID: "user-alpha", Username: "Alpha"},
		"user-beta":  {ID: "user-beta", Username: "beta"},
	}

	expected := []struct {
		id       string
		username string
	}{
		{id: "user-alpha", username: "Alpha"},
		{id: "user-beta", username: "beta"},
		{id: "user-zeta", username: "zeta"},
	}

	for i := 0; i < 64; i++ {
		if err := saveUserState(usersPath, users); err != nil {
			t.Fatalf("saveUserState() error: %v", err)
		}

		data, err := os.ReadFile(usersPath)
		if err != nil {
			t.Fatalf("ReadFile(users.json) error: %v", err)
		}

		var persisted []User
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatalf("Unmarshal(persisted users) error: %v", err)
		}
		if len(persisted) != len(expected) {
			t.Fatalf("persisted user count = %d, want %d", len(persisted), len(expected))
		}
		for index, want := range expected {
			if persisted[index].ID != want.id || persisted[index].Username != want.username {
				t.Fatalf("persisted order at iteration %d = [%s:%s %s:%s %s:%s], want [%s:%s %s:%s %s:%s]",
					i,
					persisted[0].ID,
					persisted[0].Username,
					persisted[1].ID,
					persisted[1].Username,
					persisted[2].ID,
					persisted[2].Username,
					expected[0].id,
					expected[0].username,
					expected[1].id,
					expected[1].username,
					expected[2].id,
					expected[2].username,
				)
			}
		}
	}
}

func TestUserStore_Create_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	managedDir := filepath.Join(baseDir, "managed")
	backupDir := filepath.Join(baseDir, "managed-real")
	usersPath := filepath.Join(managedDir, "users.json")

	store, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	outsideDir := t.TempDir()
	outsideUsersPath := filepath.Join(outsideDir, "users.json")
	outsideContent := []byte("[]")
	if err := os.WriteFile(outsideUsersPath, outsideContent, 0600); err != nil {
		t.Fatalf("failed to seed outside users file: %v", err)
	}

	originalHook := afterValidateAuthFilePath
	var hookOnce sync.Once
	afterValidateAuthFilePath = func() {
		hookOnce.Do(func() {
			if err := os.Rename(managedDir, backupDir); err != nil {
				t.Fatalf("failed to move managed auth dir: %v", err)
			}
			if err := os.Symlink(outsideDir, managedDir); err != nil {
				t.Fatalf("failed to install auth dir symlink: %v", err)
			}
		})
	}
	t.Cleanup(func() {
		afterValidateAuthFilePath = originalHook
	})

	created, err := store.Create("rooted-write", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created == nil {
		t.Fatal("expected created user to be returned")
	}

	outsideData, err := os.ReadFile(outsideUsersPath)
	if err != nil {
		t.Fatalf("failed to read outside users file: %v", err)
	}
	if !bytes.Equal(outsideData, outsideContent) {
		t.Fatalf("expected outside users file to remain unchanged, got %s", string(outsideData))
	}

	managedData, err := os.ReadFile(filepath.Join(backupDir, "users.json"))
	if err != nil {
		t.Fatalf("failed to read rooted users file: %v", err)
	}
	if !strings.Contains(string(managedData), "\"username\": \"rooted-write\"") {
		t.Fatalf("expected rooted users file to contain created user, got %s", string(managedData))
	}
}

func TestTokenManager_RevokeToken_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	managedDir := filepath.Join(baseDir, "managed")
	backupDir := filepath.Join(baseDir, "managed-real")
	revocationFile := filepath.Join(managedDir, "token-revocations.json")

	tm := NewTokenManager(strings.Repeat("s", 32), 15*time.Minute, 24*time.Hour)
	if err := tm.EnablePersistence(revocationFile); err != nil {
		t.Fatalf("EnablePersistence() error: %v", err)
	}

	outsideDir := t.TempDir()
	outsideRevocationFile := filepath.Join(outsideDir, "token-revocations.json")
	outsideContent := []byte(`{"revoked_tokens":{"outside":"2024-01-01T00:00:00Z"},"user_revoked_at":{}}`)
	if err := os.WriteFile(outsideRevocationFile, outsideContent, 0600); err != nil {
		t.Fatalf("failed to seed outside revocation file: %v", err)
	}

	originalHook := afterValidateAuthFilePath
	var hookOnce sync.Once
	afterValidateAuthFilePath = func() {
		hookOnce.Do(func() {
			if err := os.Rename(managedDir, backupDir); err != nil {
				t.Fatalf("failed to move managed revocation dir: %v", err)
			}
			if err := os.Symlink(outsideDir, managedDir); err != nil {
				t.Fatalf("failed to install revocation dir symlink: %v", err)
			}
		})
	}
	t.Cleanup(func() {
		afterValidateAuthFilePath = originalHook
	})

	if err := tm.RevokeToken("revoked-after-validate"); err != nil {
		t.Fatalf("RevokeToken() error: %v", err)
	}

	outsideData, err := os.ReadFile(outsideRevocationFile)
	if err != nil {
		t.Fatalf("failed to read outside revocation file: %v", err)
	}
	if !bytes.Equal(outsideData, outsideContent) {
		t.Fatalf("expected outside revocation file to remain unchanged, got %s", string(outsideData))
	}

	managedData, err := os.ReadFile(filepath.Join(backupDir, "token-revocations.json"))
	if err != nil {
		t.Fatalf("failed to read rooted revocation file: %v", err)
	}
	if !strings.Contains(string(managedData), "revoked-after-validate") {
		t.Fatalf("expected rooted revocation file to contain revoked token, got %s", string(managedData))
	}
}

func TestUserStore(t *testing.T) {
	dir := t.TempDir()

	t.Run("create and authenticate", func(t *testing.T) {
		usersFile := filepath.Join(dir, "users1.json")
		store, _, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("testuser", "password123", "test@example.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		if user.Username != "testuser" {
			t.Errorf("expected username testuser, got %s", user.Username)
		}
		if user.Role != RoleUser {
			t.Errorf("expected role user, got %s", user.Role)
		}

		authUser, err := store.Authenticate("testuser", "password123")
		if err != nil {
			t.Fatalf("failed to authenticate: %v", err)
		}
		if authUser.ID != user.ID {
			t.Errorf("expected user ID %s, got %s", user.ID, authUser.ID)
		}

		_, err = store.Authenticate("testuser", "wrongpassword")
		if err != ErrInvalidCredentials {
			t.Errorf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("duplicate username", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users2.json"))
		store.Create("duplicate", "password123", "", RoleUser)

		_, err := store.Create("duplicate", "password456", "", RoleUser)
		if err != ErrUserExists {
			t.Errorf("expected ErrUserExists, got %v", err)
		}
	})

	t.Run("short password", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3.json"))
		_, err := store.Create("shortpass", "short", "", RoleUser)
		if err != ErrPasswordTooShort {
			t.Errorf("expected ErrPasswordTooShort, got %v", err)
		}
	})

	t.Run("invalid username", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-invalid-username.json"))
		_, err := store.Create("../escape", "password123", "", RoleUser)
		if err != ErrInvalidUsername {
			t.Fatalf("expected ErrInvalidUsername, got %v", err)
		}
		if _, lookupErr := store.GetByUsername("../escape"); lookupErr != ErrUserNotFound {
			t.Fatalf("expected invalid username create to leave no persisted user, got %v", lookupErr)
		}
	})

	t.Run("invalid stored home dir", func(t *testing.T) {
		usersPath := filepath.Join(dir, "users-invalid-home-dir.json")
		content := `[
		  {
		    "id": "u-invalid-home",
		    "username": "broken-user",
		    "password_hash": "$2a$10$dummy",
		    "role": "user",
		    "created_at": "2024-01-01T00:00:00Z",
		    "updated_at": "2024-01-01T00:00:00Z",
		    "home_dir": ""
		  }
		]`
		if err := os.WriteFile(usersPath, []byte(content), 0600); err != nil {
			t.Fatalf("failed to seed users file: %v", err)
		}

		if _, _, err := NewUserStore(usersPath); err == nil {
			t.Fatal("expected invalid home_dir in users file to fail")
		} else if !strings.Contains(err.Error(), "invalid home_dir") {
			t.Fatalf("expected invalid home_dir error, got %v", err)
		}
	})

	t.Run("create returns entropy failure", func(t *testing.T) {
		store, _, err := NewUserStore(filepath.Join(dir, "users3-rand-fail.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		originalRandomRead := userRandomRead
		userRandomRead = func([]byte) (int, error) {
			return 0, errors.New("entropy unavailable")
		}
		defer func() {
			userRandomRead = originalRandomRead
		}()

		user, err := store.Create("randfail", "password123", "", RoleUser)
		if err == nil {
			t.Fatal("expected create to fail when entropy source is unavailable")
		}
		if user != nil {
			t.Fatalf("expected no user to be returned on entropy failure, got %+v", user)
		}
		if !strings.Contains(err.Error(), "generate user ID") {
			t.Fatalf("expected generate user ID error, got %v", err)
		}
		if _, lookupErr := store.GetByUsername("randfail"); lookupErr != ErrUserNotFound {
			t.Fatalf("expected failed create to leave no persisted user, got %v", lookupErr)
		}
	})

	t.Run("new user store returns default admin entropy failure", func(t *testing.T) {
		originalRandomRead := userRandomRead
		userRandomRead = func([]byte) (int, error) {
			return 0, errors.New("entropy unavailable")
		}
		defer func() {
			userRandomRead = originalRandomRead
		}()

		usersFile := filepath.Join(t.TempDir(), "users.json")
		store, password, err := NewUserStore(usersFile)
		if err == nil {
			t.Fatal("expected NewUserStore to fail when default admin randomness is unavailable")
		}
		if store != nil {
			t.Fatalf("expected no store to be returned on default admin entropy failure, got %+v", store)
		}
		if password != "" {
			t.Fatalf("expected no initial password on failure, got %q", password)
		}
		if !strings.Contains(err.Error(), "generate default admin password") {
			t.Fatalf("expected default admin password generation error, got %v", err)
		}
		if _, statErr := os.Stat(usersFile); !os.IsNotExist(statErr) {
			t.Fatalf("expected no users file to be written, got %v", statErr)
		}
		passwordFile := filepath.Join(filepath.Dir(usersFile), "initial-password.txt")
		if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
			t.Fatalf("expected no initial password file to be written, got %v", statErr)
		}
	})

	t.Run("change password", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users4.json"))
		user, _ := store.Create("changepw", "oldpassword123", "", RoleUser)

		err := store.ChangePassword(user.ID, "oldpassword123", "newpassword456")
		if err != nil {
			t.Fatalf("failed to change password: %v", err)
		}

		_, err = store.Authenticate("changepw", "oldpassword123")
		if err != ErrInvalidCredentials {
			t.Errorf("expected old password to fail")
		}

		_, err = store.Authenticate("changepw", "newpassword456")
		if err != nil {
			t.Errorf("expected new password to work: %v", err)
		}
	})

	t.Run("disable user", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5.json"))
		user, _ := store.Create("disabled", "password123", "", RoleUser)

		user.Disabled = true
		store.Update(user)

		_, err := store.Authenticate("disabled", "password123")
		if err != ErrUserDisabled {
			t.Errorf("expected ErrUserDisabled, got %v", err)
		}
	})

	t.Run("update validates and normalizes home dir", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-home-dir.json"))
		user, _ := store.Create("homefix", "password123", "", RoleUser)

		user.HomeDir = " \\homefix\\docs// "
		if err := store.Update(user); err != nil {
			t.Fatalf("expected normalized home_dir update to succeed, got %v", err)
		}

		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload updated user: %v", err)
		}
		if fresh.HomeDir != "/homefix/docs" {
			t.Fatalf("expected normalized home_dir /homefix/docs, got %s", fresh.HomeDir)
		}

		user.HomeDir = "../secret"
		if err := store.Update(user); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("expected invalid home_dir error, got %v", err)
		}

		fresh, err = store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload user after rejected update: %v", err)
		}
		if fresh.HomeDir != "/homefix/docs" {
			t.Fatalf("expected stored home_dir to remain /homefix/docs, got %s", fresh.HomeDir)
		}
	})

	t.Run("returned users are detached copies", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-copy.json"))
		user, _ := store.Create("copyuser", "password123", "copy@test.com", RoleUser)

		fetched, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to get user: %v", err)
		}
		fetched.Disabled = true
		fetched.Email = "mutated@test.com"

		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to get fresh user: %v", err)
		}
		if fresh.Disabled {
			t.Fatal("expected detached copy mutation to leave store state unchanged")
		}
		if fresh.Email != "copy@test.com" {
			t.Fatalf("expected stored email copy@test.com, got %s", fresh.Email)
		}
	})

	t.Run("failed mutations roll back in-memory state", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		changeUser, err := store.Create("rollback-change", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}
		deleteUser, err := store.Create("rollback-delete", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}
		resetUser, err := store.Create("rollback-reset", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}
		updateUser, err := store.Create("rollback-update", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create update user: %v", err)
		}

		store.filePath = storeDir

		updateUser.Disabled = true
		if err := store.Update(updateUser); err == nil {
			t.Fatal("expected update save failure")
		}
		freshUpdated, err := store.GetByID(updateUser.ID)
		if err != nil {
			t.Fatalf("failed to reload updated user: %v", err)
		}
		if freshUpdated.Disabled {
			t.Fatal("expected failed update to roll back disabled flag")
		}

		if err := store.ChangePassword(changeUser.ID, "password123", "newpassword456"); err == nil {
			t.Fatal("expected change password save failure")
		}
		freshChange, err := store.GetByID(changeUser.ID)
		if err != nil {
			t.Fatalf("failed to reload rollback change user: %v", err)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(freshChange.PasswordHash), []byte("newpassword456")); err == nil {
			t.Fatal("expected failed password change to leave new password unusable")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(freshChange.PasswordHash), []byte("password123")); err != nil {
			t.Fatalf("expected old password hash to remain valid after rollback, got %v", err)
		}

		if err := store.ResetPassword(resetUser.ID, "resetpass456"); err == nil {
			t.Fatal("expected reset password save failure")
		}
		freshReset, err := store.GetByID(resetUser.ID)
		if err != nil {
			t.Fatalf("failed to reload rollback reset user: %v", err)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(freshReset.PasswordHash), []byte("resetpass456")); err == nil {
			t.Fatal("expected failed reset password to leave new password unusable")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(freshReset.PasswordHash), []byte("password123")); err != nil {
			t.Fatalf("expected original password hash to remain valid after failed reset, got %v", err)
		}

		if err := store.Delete(deleteUser.ID); err == nil {
			t.Fatal("expected delete save failure")
		}
		if _, err := store.GetByID(deleteUser.ID); err != nil {
			t.Fatalf("expected failed delete to keep user in store, got %v", err)
		}
	})

	t.Run("delete user", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users6.json"))
		user, _ := store.Create("todelete", "password123", "", RoleUser)

		err := store.Delete(user.ID)
		if err != nil {
			t.Fatalf("failed to delete user: %v", err)
		}

		_, err = store.GetByID(user.ID)
		if err != ErrUserNotFound {
			t.Errorf("expected ErrUserNotFound, got %v", err)
		}
	})

	t.Run("cannot delete last admin", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users7.json"))
		// NewUserStore creates default admin, so we get existing admin
		admin, _ := store.GetByUsername("admin")

		err := store.Delete(admin.ID)
		if err != ErrLastAdmin {
			t.Errorf("expected ErrLastAdmin, got %v", err)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		usersFile := filepath.Join(dir, "users8.json")
		store1, _, _ := NewUserStore(usersFile)
		store1.Create("persistent", "password123", "p@example.com", RoleUser)

		store2, _, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to load existing store: %v", err)
		}

		user, err := store2.GetByUsername("persistent")
		if err != nil {
			t.Fatalf("failed to find persistent user: %v", err)
		}
		if user.Email != "p@example.com" {
			t.Errorf("expected email p@example.com, got %s", user.Email)
		}
	})

	t.Run("list users sorts by username case-insensitively", func(t *testing.T) {
		store, _, err := NewUserStore(filepath.Join(dir, "users8-order.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		if _, err := store.Create("zeta", "password123", "", RoleUser); err != nil {
			t.Fatalf("failed to create zeta user: %v", err)
		}
		if _, err := store.Create("Alpha", "password123", "", RoleUser); err != nil {
			t.Fatalf("failed to create Alpha user: %v", err)
		}
		if _, err := store.Create("beta", "password123", "", RoleUser); err != nil {
			t.Fatalf("failed to create beta user: %v", err)
		}

		users := store.List()
		if len(users) < 4 {
			t.Fatalf("expected at least 4 users including default admin, got %d", len(users))
		}

		orderedNames := make([]string, len(users))
		for i, user := range users {
			orderedNames[i] = user.Username
		}

		alphaIndex := -1
		betaIndex := -1
		zetaIndex := -1
		for i, name := range orderedNames {
			switch name {
			case "Alpha":
				alphaIndex = i
			case "beta":
				betaIndex = i
			case "zeta":
				zetaIndex = i
			}
		}

		if alphaIndex == -1 || betaIndex == -1 || zetaIndex == -1 {
			t.Fatalf("expected Alpha, beta, and zeta in listed users, got %v", orderedNames)
		}
		if !(alphaIndex < betaIndex && betaIndex < zetaIndex) {
			t.Fatalf("expected alphabetical order Alpha < beta < zeta, got %v", orderedNames)
		}
	})

	t.Run("get by username stays responsive during login persistence", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("slowlogin", "password123", "slow@login.test", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		originalWriter := userStoreWriter
		writerStarted := make(chan struct{})
		writerRelease := make(chan struct{})
		var startOnce sync.Once
		var releaseOnce sync.Once
		userStoreWriter = func(path string, data []byte) error {
			startOnce.Do(func() {
				close(writerStarted)
			})
			<-writerRelease
			return originalWriter(path, data)
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
			releaseOnce.Do(func() {
				close(writerRelease)
			})
		})

		authDone := make(chan struct {
			user *User
			err  error
		}, 1)
		go func() {
			authUser, authErr := store.Authenticate("slowlogin", "password123")
			authDone <- struct {
				user *User
				err  error
			}{user: authUser, err: authErr}
		}()

		select {
		case <-writerStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for user store write to start")
		}

		lookupDone := make(chan struct{})
		go func() {
			loaded, lookupErr := store.GetByUsername("slowlogin")
			if lookupErr != nil {
				t.Errorf("GetByUsername() error during pending login persist: %v", lookupErr)
			} else if loaded.LastLoginAt != nil {
				t.Errorf("expected reads during pending login persist to observe committed LastLoginAt=nil, got %v", loaded.LastLoginAt)
			}
			close(lookupDone)
		}()

		select {
		case <-lookupDone:
		case <-time.After(time.Second):
			t.Fatal("GetByUsername() blocked on an in-flight Authenticate() save")
		}

		releaseOnce.Do(func() {
			close(writerRelease)
		})

		select {
		case result := <-authDone:
			if result.err != nil {
				t.Fatalf("Authenticate() error: %v", result.err)
			}
			if result.user == nil || result.user.ID != user.ID {
				t.Fatalf("expected Authenticate() to return user %s, got %+v", user.ID, result.user)
			}
			if result.user.LastLoginAt == nil {
				t.Fatal("expected Authenticate() result to include last_login_at after successful save")
			}
		case <-time.After(time.Second):
			t.Fatal("Authenticate() did not finish after releasing writer")
		}

		loaded, err := store.GetByUsername("slowlogin")
		if err != nil {
			t.Fatalf("failed to reload user after authenticate: %v", err)
		}
		if loaded.LastLoginAt == nil {
			t.Fatal("expected persisted last_login_at after authenticate")
		}
	})

	t.Run("returns error when last login persist fails hard", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		if _, err := store.Create("persistfail", "password123", "persistfail@example.com", RoleUser); err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		originalWriter := userStoreWriter
		userStoreWriter = func(path string, data []byte) error {
			return errors.New("persist failed")
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
		})

		authUser, err := store.Authenticate("persistfail", "password123")
		if err == nil || isAuthPersistenceWarning(err) {
			t.Fatalf("expected hard persistence error, got %v", err)
		}
		if authUser != nil {
			t.Fatalf("expected Authenticate() to fail closed on hard persistence error, got %+v", authUser)
		}

		loaded, err := store.GetByUsername("persistfail")
		if err != nil {
			t.Fatalf("GetByUsername() error: %v", err)
		}
		if loaded.LastLoginAt != nil {
			t.Fatalf("expected failed persist to leave stored last_login_at unset, got %v", loaded.LastLoginAt)
		}
	})

	t.Run("returns warning and updates last login when auth persistence fsync is uncertain", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("warnlogin", "password123", "warnlogin@example.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		originalSyncAuthFileDir := syncAuthFileDir
		originalSyncAuthRootDir := syncAuthRootDir
		syncAuthFileDir = func(dir string) error {
			return errors.New("directory fsync failed")
		}
		syncAuthRootDir = func(root *os.Root) error {
			return errors.New("directory fsync failed")
		}
		defer func() {
			syncAuthFileDir = originalSyncAuthFileDir
			syncAuthRootDir = originalSyncAuthRootDir
		}()

		authUser, err := store.Authenticate("warnlogin", "password123")
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected Authenticate() warning error, got %v", err)
		}
		if authUser == nil || authUser.ID != user.ID {
			t.Fatalf("expected Authenticate() to return user %s on warning, got %+v", user.ID, authUser)
		}
		if authUser.LastLoginAt == nil {
			t.Fatal("expected Authenticate() result to include last_login_at on warning")
		}

		loaded, err := store.GetByUsername("warnlogin")
		if err != nil {
			t.Fatalf("GetByUsername() error: %v", err)
		}
		if loaded.LastLoginAt == nil {
			t.Fatal("expected warning persist to keep last_login_at committed in memory")
		}
	})

	t.Run("directory sync warnings keep mutations committed", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		originalSyncAuthFileDir := syncAuthFileDir
		originalSyncAuthRootDir := syncAuthRootDir
		syncAuthFileDir = func(dir string) error {
			return errors.New("directory fsync failed")
		}
		syncAuthRootDir = func(root *os.Root) error {
			return errors.New("directory fsync failed")
		}
		defer func() {
			syncAuthFileDir = originalSyncAuthFileDir
			syncAuthRootDir = originalSyncAuthRootDir
		}()

		created, err := store.Create("warncreate", "password123", "warncreate@example.com", RoleUser)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected create warning error, got %v", err)
		}
		if created == nil {
			t.Fatal("expected create to return user on persistence warning")
		}
		if _, err := store.GetByUsername("warncreate"); err != nil {
			t.Fatalf("expected create warning to commit in-memory user, got %v", err)
		}

		created.Disabled = true
		if err := store.Update(created); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected update warning error, got %v", err)
		}
		freshCreated, err := store.GetByID(created.ID)
		if err != nil {
			t.Fatalf("failed to reload updated user: %v", err)
		}
		if !freshCreated.Disabled {
			t.Fatal("expected update warning to commit disabled flag in memory")
		}

		changeUser, err := store.Create("warnchange", "password123", "warnchange@example.com", RoleUser)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected change fixture create warning error, got %v", err)
		}
		if err := store.ChangePassword(changeUser.ID, "password123", "newpassword456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected change password warning error, got %v", err)
		}
		if _, err := store.Authenticate("warnchange", "newpassword456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warned password change, got %v", err)
		}

		resetUser, err := store.Create("warnreset", "password123", "warnreset@example.com", RoleUser)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected reset fixture create warning error, got %v", err)
		}
		if err := store.ResetPassword(resetUser.ID, "resetpass456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected reset password warning error, got %v", err)
		}
		if _, err := store.Authenticate("warnreset", "resetpass456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warned reset password, got %v", err)
		}

		deleteUser, err := store.Create("warndelete", "password123", "warndelete@example.com", RoleUser)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected delete fixture create warning error, got %v", err)
		}
		if err := store.Delete(deleteUser.ID); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected delete warning error, got %v", err)
		}
		if _, err := store.GetByID(deleteUser.ID); err != ErrUserNotFound {
			t.Fatalf("expected delete warning to remove user from memory, got %v", err)
		}

		reloaded, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to reload user store after warnings: %v", err)
		}
		if _, err := reloaded.GetByUsername("warncreate"); err != nil {
			t.Fatalf("expected warning-created user on disk, got %v", err)
		}
		reloadedCreated, err := reloaded.GetByUsername("warncreate")
		if err != nil {
			t.Fatalf("failed to reload updated warning user: %v", err)
		}
		if !reloadedCreated.Disabled {
			t.Fatal("expected warning-updated disabled flag on disk")
		}
		if _, err := reloaded.Authenticate("warnchange", "newpassword456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warning-changed password on disk, got %v", err)
		}
		if _, err := reloaded.Authenticate("warnreset", "resetpass456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warning-reset password on disk, got %v", err)
		}
		if _, err := reloaded.GetByID(deleteUser.ID); err != ErrUserNotFound {
			t.Fatalf("expected warning-deleted user to stay deleted on disk, got %v", err)
		}
	})

	t.Run("concurrent writes serialize persistence", func(t *testing.T) {
		storeDir := t.TempDir()
		store, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		originalWriter := userStoreWriter
		firstStarted := make(chan struct{})
		firstRelease := make(chan struct{})
		secondStarted := make(chan struct{})
		var startFirstOnce sync.Once
		var releaseFirstOnce sync.Once
		var startSecondOnce sync.Once
		var callCount int32
		userStoreWriter = func(path string, data []byte) error {
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
			userStoreWriter = originalWriter
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
			_, createErr := store.Create("firstuser", "password123", "first@example.com", RoleUser)
			firstDone <- createErr
		}()

		select {
		case <-firstStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for first user-store persist to start")
		}

		secondDone := make(chan error, 1)
		go func() {
			_, createErr := store.Create("seconduser", "password123", "second@example.com", RoleUser)
			secondDone <- createErr
		}()

		select {
		case <-secondStarted:
			t.Fatal("second user-store persist started before first persist completed")
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
		case <-time.After(2 * time.Second):
			t.Fatal("first Create() did not finish after releasing writer")
		}

		select {
		case <-secondStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for second user-store persist to start")
		}

		select {
		case err := <-secondDone:
			if err != nil {
				t.Fatalf("second Create() error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("second Create() did not finish")
		}

		if _, err := store.GetByUsername("firstuser"); err != nil {
			t.Fatalf("expected first created user to exist, got %v", err)
		}
		if _, err := store.GetByUsername("seconduser"); err != nil {
			t.Fatalf("expected second created user to exist, got %v", err)
		}
	})
}

func TestTokenManager(t *testing.T) {
	secret := "test-jwt-secret-key-for-testing-32b"

	t.Run("generate and validate", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-123",
			Username: "testuser",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}

		if tokenPair.AccessToken == "" {
			t.Error("access token is empty")
		}
		if tokenPair.RefreshToken == "" {
			t.Error("refresh token is empty")
		}
		if tokenPair.TokenType != "Bearer" {
			t.Errorf("expected token type Bearer, got %s", tokenPair.TokenType)
		}

		claims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("failed to validate access token: %v", err)
		}
		if claims.UserID != "user-123" {
			t.Errorf("expected user ID user-123, got %s", claims.UserID)
		}
		if claims.Role != RoleUser {
			t.Errorf("expected role user, got %s", claims.Role)
		}

		userID, err := tm.ValidateRefreshToken(tokenPair.RefreshToken)
		if err != nil {
			t.Fatalf("failed to validate refresh token: %v", err)
		}
		if userID != "user-123" {
			t.Errorf("expected user ID user-123, got %s", userID)
		}
	})

	t.Run("access token rejected as refresh token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-refresh-confusion",
			Username: "refresh-confusion",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}

		_, err = tm.ValidateRefreshToken(tokenPair.AccessToken)
		if err != ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken for access token used as refresh token, got %v", err)
		}
	})

	t.Run("refresh token rejected as access token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-access-confusion",
			Username: "access-confusion",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}

		_, err = tm.ValidateAccessToken(tokenPair.RefreshToken)
		if err != ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken for refresh token used as access token, got %v", err)
		}
	})

	t.Run("revoke token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-456",
			Username: "revokeuser",
			Role:     RoleUser,
		}

		tokenPair, _ := tm.GenerateTokenPair(user)
		claims, _ := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err := tm.RevokeToken(claims.TokenID); err != nil {
			t.Fatalf("RevokeToken() error: %v", err)
		}

		_, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("expected ErrTokenRevoked, got %v", err)
		}
	})

	t.Run("revoke token returns warning and keeps revocation in memory when persistence fsync is uncertain", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-warning-revoke",
			Username: "warning-revoke",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		claims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("ValidateAccessToken() before revoke error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		err = tm.RevokeToken(claims.TokenID)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected persistence warning, got %v", err)
		}

		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected token to remain revoked in memory, got %v", err)
		}
	})

	t.Run("revoke token rolls back cleanup state on hard persistence failure", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-hard-revoke-token",
			Username: "hard-revoke-token",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		claims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("ValidateAccessToken() before revoke error: %v", err)
		}

		expiredCleanupExpiry := time.Now().Add(-2 * time.Minute)
		tm.mu.Lock()
		tm.revokedTokens["expired-cleanup-token"] = expiredCleanupExpiry
		tm.mu.Unlock()

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		err = tm.RevokeToken(claims.TokenID)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected persistence warning, got %v", err)
		}

		tm.mu.RLock()
		_, hasExpiredCleanup := tm.revokedTokens["expired-cleanup-token"]
		_, hasNewRevocation := tm.revokedTokens[claims.TokenID]
		tm.mu.RUnlock()
		if hasExpiredCleanup {
			t.Fatal("expected warning to keep expired cleanup entry removed")
		}
		if !hasNewRevocation {
			t.Fatal("expected warning to keep new token revocation in memory")
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after warning, got %v", err)
		}
	})

	t.Run("revoke token blocks concurrent validation until revocation is recorded", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-456-race",
			Username: "revoke-race",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}
		claims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("failed to validate access token before revoke: %v", err)
		}

		enteredCleanup := make(chan struct{})
		releaseCleanup := make(chan struct{})
		originalAfterRevokeTokenCleanup := afterRevokeTokenCleanup
		afterRevokeTokenCleanup = func() {
			close(enteredCleanup)
			<-releaseCleanup
		}
		defer func() {
			afterRevokeTokenCleanup = originalAfterRevokeTokenCleanup
		}()

		revokeDone := make(chan struct{})
		go func() {
			if err := tm.RevokeToken(claims.TokenID); err != nil {
				t.Errorf("RevokeToken() error: %v", err)
			}
			close(revokeDone)
		}()

		select {
		case <-enteredCleanup:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for revoke cleanup hook")
		}

		validateResult := make(chan error, 1)
		go func() {
			_, err := tm.ValidateAccessToken(tokenPair.AccessToken)
			validateResult <- err
		}()

		select {
		case err := <-validateResult:
			t.Fatalf("expected validation to block until revoke completes, got %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		close(releaseCleanup)

		select {
		case <-revokeDone:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for revoke completion")
		}

		select {
		case err := <-validateResult:
			if err != ErrTokenRevoked {
				t.Fatalf("expected ErrTokenRevoked after revoke completes, got %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for blocked validation result")
		}
	})

	t.Run("revoke by user", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-789",
			Username: "multitoken",
			Role:     RoleUser,
		}

		tokenPair1, _ := tm.GenerateTokenPair(user)
		tokenPair2, _ := tm.GenerateTokenPair(user)

		if err := tm.RevokeByUser(user.ID); err != nil {
			t.Fatalf("RevokeByUser() error: %v", err)
		}

		_, err := tm.ValidateAccessToken(tokenPair1.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("token1: expected ErrTokenRevoked, got %v", err)
		}
		_, err = tm.ValidateAccessToken(tokenPair2.AccessToken)
		if err != ErrTokenRevoked {
			t.Errorf("token2: expected ErrTokenRevoked, got %v", err)
		}

		_, err = tm.ValidateRefreshToken(tokenPair1.RefreshToken)
		if err != ErrTokenRevoked {
			t.Errorf("refresh token: expected ErrTokenRevoked, got %v", err)
		}
	})

	t.Run("revoke by user returns warning and keeps in-memory revocation on hard persistence failure", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-hard-revoke",
			Username: "hard-revoke",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		expiredCleanupTokenExpiry := time.Now().Add(-2 * time.Minute)
		expiredCleanupUserRevokedAt := time.Now().Add(-3 * time.Minute)
		tm.mu.Lock()
		tm.revokedTokens["expired-cleanup-token"] = expiredCleanupTokenExpiry
		tm.userRevokedAt["expired-cleanup-user"] = expiredCleanupUserRevokedAt
		tm.mu.Unlock()

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		err = tm.RevokeByUser(user.ID)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected persistence warning, got %v", err)
		}

		persistTokenRevocations = originalPersistTokenRevocations

		tm.mu.RLock()
		_, hasExpiredCleanupToken := tm.revokedTokens["expired-cleanup-token"]
		_, hasExpiredCleanupUser := tm.userRevokedAt["expired-cleanup-user"]
		_, revokedUser := tm.userRevokedAt[user.ID]
		tm.mu.RUnlock()
		if hasExpiredCleanupToken {
			t.Fatal("expected warning to keep expired token cleanup entry removed")
		}
		if !hasExpiredCleanupUser {
			t.Fatal("expected unaffected user revocation entry to remain present")
		}
		if !revokedUser {
			t.Fatal("expected warning to keep user revocation state in memory")
		}

		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after warning, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after warning, got %v", err)
		}
	})

	t.Run("revoke by user does not revoke newly issued tokens in same second", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		user := &User{
			ID:       "user-same-second",
			Username: "same-second",
			Role:     RoleUser,
		}

		oldPair, _ := tm.GenerateTokenPair(user)
		if err := tm.RevokeByUser(user.ID); err != nil {
			t.Fatalf("RevokeByUser() error: %v", err)
		}
		newPair, _ := tm.GenerateTokenPair(user)

		_, err := tm.ValidateAccessToken(oldPair.AccessToken)
		if err != ErrTokenRevoked {
			t.Fatalf("old access token: expected ErrTokenRevoked, got %v", err)
		}

		if _, err := tm.ValidateAccessToken(newPair.AccessToken); err != nil {
			t.Fatalf("new access token should remain valid, got %v", err)
		}

		if _, err := tm.ValidateRefreshToken(newPair.RefreshToken); err != nil {
			t.Fatalf("new refresh token should remain valid, got %v", err)
		}
	})

	t.Run("persisted token revocations survive restart", func(t *testing.T) {
		revocationFile := filepath.Join(t.TempDir(), "token-revocations.json")
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() error: %v", err)
		}

		user := &User{
			ID:       "user-persisted-revocation",
			Username: "persisted-revocation",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		accessClaims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("ValidateAccessToken() before revoke error: %v", err)
		}
		refreshClaims, err := tm.validateRefreshTokenClaims(tokenPair.RefreshToken)
		if err != nil {
			t.Fatalf("validateRefreshTokenClaims() before revoke error: %v", err)
		}

		if err := tm.RevokeToken(accessClaims.TokenID); err != nil {
			t.Fatalf("RevokeToken(access) error: %v", err)
		}
		if err := tm.RevokeToken(refreshClaims.ID); err != nil {
			t.Fatalf("RevokeToken(refresh) error: %v", err)
		}

		restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() after restart error: %v", err)
		}

		if _, err := restarted.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject revoked access token, got %v", err)
		}
		if _, err := restarted.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject revoked refresh token, got %v", err)
		}
	})

	t.Run("corrupt token revocation file recovers on startup", func(t *testing.T) {
		storeDir := t.TempDir()
		revocationFile := filepath.Join(storeDir, "token-revocations.json")
		if err := os.WriteFile(revocationFile, []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile(corrupt revocation file) error: %v", err)
		}

		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() with corrupt revocation file error: %v", err)
		}

		entries, err := os.ReadDir(storeDir)
		if err != nil {
			t.Fatalf("ReadDir(storeDir) error: %v", err)
		}
		foundBackup := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "token-revocations.json.corrupt.") {
				foundBackup = true
				break
			}
		}
		if !foundBackup {
			t.Fatal("expected corrupt token revocation backup to be created")
		}

		user := &User{
			ID:       "user-corrupt-revocation-recovery",
			Username: "corrupt-revocation-recovery",
			Role:     RoleUser,
		}
		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}
		accessClaims, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != nil {
			t.Fatalf("ValidateAccessToken() before revoke error: %v", err)
		}
		if err := tm.RevokeToken(accessClaims.TokenID); err != nil {
			t.Fatalf("RevokeToken(access) error: %v", err)
		}

		restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() after recovery restart error: %v", err)
		}
		if _, err := restarted.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject revoked access token after recovery, got %v", err)
		}
	})

	t.Run("persisted user revocations survive restart", func(t *testing.T) {
		revocationFile := filepath.Join(t.TempDir(), "token-revocations.json")
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() error: %v", err)
		}

		user := &User{
			ID:       "user-persisted-user-revoke",
			Username: "persisted-user-revoke",
			Role:     RoleUser,
		}

		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		if err := tm.RevokeByUser(user.ID); err != nil {
			t.Fatalf("RevokeByUser() error: %v", err)
		}

		restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnablePersistence(revocationFile); err != nil {
			t.Fatalf("EnablePersistence() after restart error: %v", err)
		}

		if _, err := restarted.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject user-revoked access token, got %v", err)
		}
		if _, err := restarted.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject user-revoked refresh token, got %v", err)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)

		_, err := tm.ValidateAccessToken("invalid-token")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		tm := NewTokenManager(secret, -1*time.Hour, -1*time.Hour)

		user := &User{
			ID:       "user-exp",
			Username: "expired",
			Role:     RoleUser,
		}

		tokenPair, _ := tm.GenerateTokenPair(user)

		_, err := tm.ValidateAccessToken(tokenPair.AccessToken)
		if err != ErrTokenExpired {
			t.Errorf("expected ErrTokenExpired, got %v", err)
		}
	})

	t.Run("generate token pair returns entropy failure", func(t *testing.T) {
		originalRandomRead := tokenRandomRead
		tokenRandomRead = func(b []byte) (int, error) {
			return 0, errors.New("entropy unavailable")
		}
		defer func() {
			tokenRandomRead = originalRandomRead
		}()

		tm := NewTokenManager(strings.Repeat("a", 32), 15*time.Minute, 24*time.Hour)
		user := &User{
			ID:       "user-entropy-failure",
			Username: "entropy-failure",
			Role:     RoleUser,
		}

		_, err := tm.GenerateTokenPair(user)
		if err == nil {
			t.Fatal("expected token generation to fail when token ID entropy is unavailable")
		}
		if !strings.Contains(err.Error(), "generate token id") {
			t.Fatalf("expected wrapped token id generation error, got %v", err)
		}
	})

	t.Run("short secret falls back to deterministic key when entropy fails", func(t *testing.T) {
		originalRandomRead := tokenRandomRead
		tokenRandomRead = func(b []byte) (int, error) {
			return 0, errors.New("entropy unavailable")
		}
		defer func() {
			tokenRandomRead = originalRandomRead
		}()

		secret := "short-secret"
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		fallback := sha256.Sum256([]byte(secret))
		if !bytes.Equal(tm.secretKey, fallback[:]) {
			t.Fatalf("expected fallback secret key %x, got %x", fallback, tm.secretKey)
		}
	})
}

func TestTokenManager_CleanupRevokedTokens_RemovesExpiredEntries(t *testing.T) {
	tm := NewTokenManager("cleanup-secret", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.userRevokedAt["expired-user"] = time.Now().Add(-2 * time.Minute)
	tm.mu.Unlock()

	tm.CleanupRevokedTokens()

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if _, ok := tm.revokedTokens["expired-token"]; ok {
		t.Fatal("expected expired token entry to be removed")
	}
	if _, ok := tm.userRevokedAt["expired-user"]; ok {
		t.Fatal("expected expired user revocation entry to be removed")
	}
}

func TestTokenManager_CleanupRevokedTokens_RollsBackExpiredEntriesOnHardPersistenceFailure(t *testing.T) {
	tm := NewTokenManager("cleanup-hard-failure", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.userRevokedAt["expired-user"] = time.Now().Add(-2 * time.Minute)
	tm.mu.Unlock()

	originalPersistTokenRevocations := persistTokenRevocations
	persistTokenRevocations = func(tm *TokenManager) error {
		return errors.New("write failed")
	}
	defer func() {
		persistTokenRevocations = originalPersistTokenRevocations
	}()

	tm.CleanupRevokedTokens()

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if _, ok := tm.revokedTokens["expired-token"]; !ok {
		t.Fatal("expected hard failure to restore expired token entry")
	}
	if _, ok := tm.userRevokedAt["expired-user"]; !ok {
		t.Fatal("expected hard failure to restore expired user revocation entry")
	}
}

func TestTokenManager_CleanupRevokedTokens_KeepsExpiredEntriesRemovedOnPersistenceWarning(t *testing.T) {
	tm := NewTokenManager("cleanup-warning", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.userRevokedAt["expired-user"] = time.Now().Add(-2 * time.Minute)
	tm.mu.Unlock()

	originalPersistTokenRevocations := persistTokenRevocations
	persistTokenRevocations = func(tm *TokenManager) error {
		return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
	}
	defer func() {
		persistTokenRevocations = originalPersistTokenRevocations
	}()

	tm.CleanupRevokedTokens()

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if _, ok := tm.revokedTokens["expired-token"]; ok {
		t.Fatal("expected warning cleanup to keep expired token removed")
	}
	if _, ok := tm.userRevokedAt["expired-user"]; ok {
		t.Fatal("expected warning cleanup to keep expired user revocation removed")
	}
}

func TestTokenManager_IsRevoked_RollsBackExpiredCleanupOnHardPersistenceFailure(t *testing.T) {
	tm := NewTokenManager("is-revoked-hard-failure", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.mu.Unlock()

	originalPersistTokenRevocations := persistTokenRevocations
	persistTokenRevocations = func(tm *TokenManager) error {
		return errors.New("write failed")
	}
	defer func() {
		persistTokenRevocations = originalPersistTokenRevocations
	}()

	if tm.isRevoked("expired-token") {
		t.Fatal("expected expired token cleanup to report not revoked")
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if _, ok := tm.revokedTokens["expired-token"]; !ok {
		t.Fatal("expected hard failure to restore expired token cleanup entry")
	}
}

func TestTokenManager_IsRevoked_KeepsExpiredCleanupRemovedOnPersistenceWarning(t *testing.T) {
	tm := NewTokenManager("is-revoked-warning", time.Minute, time.Minute)

	tm.mu.Lock()
	tm.revokedTokens["expired-token"] = time.Now().Add(-time.Minute)
	tm.mu.Unlock()

	originalPersistTokenRevocations := persistTokenRevocations
	persistTokenRevocations = func(tm *TokenManager) error {
		return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
	}
	defer func() {
		persistTokenRevocations = originalPersistTokenRevocations
	}()

	if tm.isRevoked("expired-token") {
		t.Fatal("expected expired token cleanup to report not revoked")
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if _, ok := tm.revokedTokens["expired-token"]; ok {
		t.Fatal("expected warning cleanup to keep expired token removed")
	}
}

func TestMiddleware(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "middleware_users.json")
	store, _, _ := NewUserStore(usersFile)
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)

	user, _ := store.Create("middlewareuser", "password123", "", RoleUser)
	admin, _ := store.Create("middlewareadmin", "password123", "", RoleAdmin)

	userToken, _ := tm.GenerateTokenPair(user)
	adminToken, _ := tm.GenerateTokenPair(admin)

	mw := NewMiddleware(store, tm)

	t.Run("require auth - valid token", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser == nil {
				t.Error("user not found in context")
			} else if ctxUser.Username != "middlewareuser" {
				t.Errorf("wrong user in context: %s", ctxUser.Username)
			}
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - no token", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"MISSING_AUTH_HEADER"`)) {
			t.Fatalf("expected MISSING_AUTH_HEADER payload, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - disabled user", func(t *testing.T) {
		disabledUser, err := store.Create("middleware-disabled", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create disabled user: %v", err)
		}
		disabledUser.Disabled = true
		if err := store.Update(disabledUser); err != nil {
			t.Fatalf("failed to disable user: %v", err)
		}

		disabledToken, err := tm.GenerateTokenPair(disabledUser)
		if err != nil {
			t.Fatalf("failed to generate token: %v", err)
		}

		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+disabledToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"USER_DISABLED"`)) {
			t.Fatalf("expected USER_DISABLED payload, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - invalid token", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"INVALID_TOKEN"`)) {
			t.Fatalf("expected INVALID_TOKEN payload, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - refresh token rejected", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.RefreshToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"INVALID_TOKEN"`)) {
			t.Fatalf("expected INVALID_TOKEN payload for refresh token bearer auth, got %s", rec.Body.String())
		}
	})

	t.Run("require auth - query token rejected for download", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/api/v1/download/test.txt?auth="+userToken.AccessToken, nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("require auth - download session cookie allowed for download", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/api/v1/download/test.txt", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - download session cookie allowed for thumbnails", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/api/v1/thumbnails/test.jpg?size=medium", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - query token rejected for non-download", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/api/v1/files/test.txt?auth="+userToken.AccessToken, nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("require role - admin only", func(t *testing.T) {
		called := false
		// Chain RequireAuth -> RequireRole
		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		handler := mw.RequireAuth(mw.RequireRole(RoleAdmin)(innerHandler))

		req := httptest.NewRequest("GET", "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("admin handler should be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200 for admin, got %d", rec.Code)
		}

		called = false
		handler2 := mw.RequireAuth(mw.RequireRole(RoleAdmin)(innerHandler))
		req = httptest.NewRequest("GET", "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec = httptest.NewRecorder()
		handler2.ServeHTTP(rec, req)

		if called {
			t.Error("user handler should not be called for admin endpoint")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected status 403 for user, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"INSUFFICIENT_PERMISSIONS"`)) {
			t.Fatalf("expected INSUFFICIENT_PERMISSIONS payload, got %s", rec.Body.String())
		}
	})

	t.Run("require role - missing auth context uses structured unauthorized error", func(t *testing.T) {
		handler := mw.RequireRole(RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/admin", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"NOT_AUTHENTICATED"`)) {
			t.Fatalf("expected NOT_AUTHENTICATED payload, got %s", rec.Body.String())
		}
	})

	t.Run("optional auth - with token", func(t *testing.T) {
		called := false
		handler := mw.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser == nil {
				t.Error("expected user in context")
			}
		}))

		req := httptest.NewRequest("GET", "/optional", nil)
		req.Header.Set("Authorization", "Bearer "+userToken.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called")
		}
	})

	t.Run("optional auth - without token", func(t *testing.T) {
		called := false
		handler := mw.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser != nil {
				t.Error("expected no user in context")
			}
		}))

		req := httptest.NewRequest("GET", "/optional", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called even without token")
		}
	})

	t.Run("excluded paths", func(t *testing.T) {
		mwWithExclude := NewMiddlewareWithExclude(store, tm, []string{"/health", "/public/"})

		called := false
		handler := mwWithExclude.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called for excluded path")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}

		called = false
		req = httptest.NewRequest("GET", "/public/files/test.txt", nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler should be called for excluded prefix")
		}
	})

	t.Run("path boundary matching", func(t *testing.T) {
		mwWithExclude := NewMiddlewareWithExclude(store, tm, []string{"/health", "/api/v1/version", "/public/"})

		handler := mwWithExclude.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for _, allowedPath := range []string{"/health", "/health/ready", "/api/v1/version", "/public/files/test.txt"} {
			req := httptest.NewRequest(http.MethodGet, allowedPath, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected excluded path %s to bypass auth, got status %d", allowedPath, rec.Code)
			}
		}

		for _, blockedPath := range []string{"/healthz", "/api/v1/version-extra", "/publicity"} {
			req := httptest.NewRequest(http.MethodGet, blockedPath, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected same-prefix path %s to require auth, got status %d", blockedPath, rec.Code)
			}
		}
	})

	t.Run("download session cookie path boundaries", func(t *testing.T) {
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for _, allowedPath := range []string{"/api/v1/download/test.txt", "/api/v1/thumbnails/test.jpg", "/api/v1/download"} {
			req := httptest.NewRequest(http.MethodGet, allowedPath, nil)
			req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected download cookie path %s to be allowed, got status %d", allowedPath, rec.Code)
			}
		}

		for _, blockedPath := range []string{"/api/v1/download-anything", "/api/v1/thumbnails-extra"} {
			req := httptest.NewRequest(http.MethodGet, blockedPath, nil)
			req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: userToken.AccessToken})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected same-prefix cookie path %s to require bearer auth, got status %d", blockedPath, rec.Code)
			}
		}
	})
}

func TestAuthHandler(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "handler_users.json")
	store, _, _ := NewUserStore(usersFile)
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)

	store.Create("handleruser", "password123", "handler@test.com", RoleUser)
	store.Create("handleradmin", "adminpass123", "admin@test.com", RoleAdmin)

	h := NewHandler(store, tm)

	type authEnvelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
		Error   *ErrorDetail    `json:"error"`
	}

	t.Run("login success", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		var resp LoginResponse
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		if !envelope.Success {
			t.Error("expected success true")
		}
		if resp.AccessToken == "" {
			t.Error("expected access token")
		}
		if resp.User.Username != "handleruser" {
			t.Errorf("expected username handleruser, got %s", resp.User.Username)
		}
	})

	t.Run("login returns internal error when last login persist fails hard", func(t *testing.T) {
		storeDir := t.TempDir()
		warningStore, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}
		if _, err := warningStore.Create("hardlogin", "password123", "hardlogin@test.com", RoleUser); err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		warningHandler := NewHandler(warningStore, NewTokenManager("hard-login-secret", 15*time.Minute, 24*time.Hour))

		originalWriter := userStoreWriter
		userStoreWriter = func(path string, data []byte) error {
			return errors.New("persist failed")
		}
		defer func() {
			userStoreWriter = originalWriter
		}()

		body := `{"username":"hardlogin","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		warningHandler.HandleLogin(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "AUTH_ERROR" {
			t.Fatalf("expected AUTH_ERROR, got %+v", envelope.Error)
		}
	})

	t.Run("login returns warning when last login persistence fsync fails", func(t *testing.T) {
		storeDir := t.TempDir()
		warningStore, _, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}
		if _, err := warningStore.Create("warnlogin", "password123", "warnlogin@test.com", RoleUser); err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		warningHandler := NewHandler(warningStore, NewTokenManager("warn-login-secret", 15*time.Minute, 24*time.Hour))

		originalSyncAuthFileDir := syncAuthFileDir
		originalSyncAuthRootDir := syncAuthRootDir
		syncAuthFileDir = func(dir string) error {
			return errors.New("directory fsync failed")
		}
		syncAuthRootDir = func(root *os.Root) error {
			return errors.New("directory fsync failed")
		}
		defer func() {
			syncAuthFileDir = originalSyncAuthFileDir
			syncAuthRootDir = originalSyncAuthRootDir
		}()

		body := `{"username":"warnlogin","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		warningHandler.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Message != "login succeeded with persistence warning" {
			t.Fatalf("message = %q, want %q", envelope.Message, "login succeeded with persistence warning")
		}
		var resp LoginResponse
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}
		if resp.AccessToken == "" || resp.RefreshToken == "" {
			t.Fatalf("expected login warning response to still issue tokens, got %+v", resp)
		}
	})

	t.Run("login trims surrounding username whitespace", func(t *testing.T) {
		body := `{"username":"  handleruser  ","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("login rejects unknown fields", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123","unexpected":true}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("expected INVALID_REQUEST error, got %+v", envelope.Error)
		}
	})

	t.Run("login rejects oversized bodies", func(t *testing.T) {
		body := bytes.Repeat([]byte{'x'}, defaultJSONRequestBodyLimit+1)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected PAYLOAD_TOO_LARGE error, got %+v", envelope.Error)
		}
	})

	t.Run("login failure", func(t *testing.T) {
		body := `{"username":"handleruser","password":"wrongpassword"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_CREDENTIALS" {
			t.Fatalf("expected INVALID_CREDENTIALS error, got %+v", envelope.Error)
		}
	})

	t.Run("disabled user login does not reveal account state", func(t *testing.T) {
		disabledUser, err := store.Create("disabled-login", "password123", "disabled-login@test.com", RoleUser)
		if err != nil {
			t.Fatalf("create disabled user error: %v", err)
		}
		disabledUser.Disabled = true
		if err := store.Update(disabledUser); err != nil {
			t.Fatalf("disable user error: %v", err)
		}

		body := `{"username":"disabled-login","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_CREDENTIALS" {
			t.Fatalf("expected INVALID_CREDENTIALS error, got %+v", envelope.Error)
		}
	})

	t.Run("login rate limited after repeated failures", func(t *testing.T) {
		h.loginFailureLimit = 3
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 11, 0, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		body := `{"username":"handleruser","password":"wrongpassword"}`
		for attempt := 1; attempt < h.loginFailureLimit; attempt++ {
			req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
			req.RemoteAddr = "203.0.113.5:1234"
			rec := httptest.NewRecorder()

			h.HandleLogin(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: expected status 401, got %d", attempt, rec.Code)
			}
		}

		lockedReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		lockedReq.RemoteAddr = "203.0.113.5:1234"
		lockedRec := httptest.NewRecorder()
		h.HandleLogin(lockedRec, lockedReq)

		if lockedRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected status 429, got %d", lockedRec.Code)
		}
		var envelope authEnvelope
		if err := json.Unmarshal(lockedRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
		}

		now = now.Add(h.loginLockDuration + time.Second)
		successReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"password123"}`))
		successReq.RemoteAddr = "203.0.113.5:1234"
		successRec := httptest.NewRecorder()
		h.HandleLogin(successRec, successReq)

		if successRec.Code != http.StatusOK {
			t.Fatalf("expected status 200 after lock expiry, got %d", successRec.Code)
		}

		postSuccessReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		postSuccessReq.RemoteAddr = "203.0.113.5:1234"
		postSuccessRec := httptest.NewRecorder()
		h.HandleLogin(postSuccessRec, postSuccessReq)

		if postSuccessRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected failures to reset after successful login, got %d", postSuccessRec.Code)
		}
	})

	t.Run("login rate limiting normalizes username casing", func(t *testing.T) {
		h.loginFailureLimit = 2
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		firstReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"HANDLERUSER","password":"wrongpassword"}`))
		firstReq.RemoteAddr = "203.0.113.6:1234"
		firstRec := httptest.NewRecorder()
		h.HandleLogin(firstRec, firstReq)

		if firstRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected first mixed-case attempt to return 401, got %d", firstRec.Code)
		}

		secondReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		secondReq.RemoteAddr = "203.0.113.6:1234"
		secondRec := httptest.NewRecorder()
		h.HandleLogin(secondRec, secondReq)

		if secondRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected second differently-cased attempt to return 429, got %d", secondRec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(secondRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
		}
	})

	t.Run("login rate limiting ignores spoofed leading forwarded for entries", func(t *testing.T) {
		h.loginFailureLimit = 2
		h.loginFailureWindow = time.Minute
		h.loginLockDuration = time.Minute
		h.loginAttempts = newLoginAttemptTracker()
		now := time.Date(2026, 3, 19, 12, 30, 0, 0, time.UTC)
		h.loginAttempts.now = func() time.Time { return now }

		firstReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		firstReq.RemoteAddr = "127.0.0.1:8080"
		firstReq.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.20")
		firstRec := httptest.NewRecorder()
		h.HandleLogin(firstRec, firstReq)

		if firstRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected first spoofed forwarded attempt to return 401, got %d", firstRec.Code)
		}

		secondReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(`{"username":"handleruser","password":"wrongpassword"}`))
		secondReq.RemoteAddr = "127.0.0.1:8080"
		secondReq.Header.Set("X-Forwarded-For", "203.0.113.11, 198.51.100.20")
		secondRec := httptest.NewRecorder()
		h.HandleLogin(secondRec, secondReq)

		if secondRec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected repeated spoofed forwarded attempts to return 429, got %d", secondRec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(secondRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "LOGIN_RATE_LIMITED" {
			t.Fatalf("expected LOGIN_RATE_LIMITED error, got %+v", envelope.Error)
		}
	})

	t.Run("login missing credentials", func(t *testing.T) {
		body := `{"username":"handleruser"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()

		h.HandleLogin(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("refresh token", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)

		var loginEnvelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &loginEnvelope); err != nil {
			t.Fatalf("unmarshal login envelope error: %v", err)
		}
		var loginResp LoginResponse
		if err := json.Unmarshal(loginEnvelope.Data, &loginResp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		refreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.RefreshToken})
		req = httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		rec = httptest.NewRecorder()
		h.HandleRefresh(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var refreshEnvelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &refreshEnvelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		var refreshResp LoginResponse
		if err := json.Unmarshal(refreshEnvelope.Data, &refreshResp); err != nil {
			t.Fatalf("unmarshal refresh payload error: %v", err)
		}

		if refreshResp.AccessToken == "" {
			t.Error("expected new access token")
		}
		if refreshResp.RefreshToken == "" {
			t.Error("expected rotated refresh token")
		}

		replayReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		replayRec := httptest.NewRecorder()
		h.HandleRefresh(replayRec, replayReq)

		if replayRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected replayed refresh token status 401, got %d: %s", replayRec.Code, replayRec.Body.String())
		}

		var replayEnvelope authEnvelope
		if err := json.Unmarshal(replayRec.Body.Bytes(), &replayEnvelope); err != nil {
			t.Fatalf("unmarshal replay envelope error: %v", err)
		}
		if replayEnvelope.Error == nil {
			t.Fatalf("expected replay refresh token error payload, got %s", replayRec.Body.String())
		}
		if replayEnvelope.Error.Code != "TOKEN_REVOKED" {
			t.Fatalf("expected replay refresh token code TOKEN_REVOKED, got %s", replayEnvelope.Error.Code)
		}

		newRefreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: refreshResp.RefreshToken})
		newRefreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(newRefreshBody))
		newRefreshRec := httptest.NewRecorder()
		h.HandleRefresh(newRefreshRec, newRefreshReq)

		if newRefreshRec.Code != http.StatusOK {
			t.Fatalf("expected rotated refresh token to remain usable, got %d: %s", newRefreshRec.Code, newRefreshRec.Body.String())
		}

		accessTokenBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.AccessToken})
		accessTokenReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(accessTokenBody))
		accessTokenRec := httptest.NewRecorder()
		h.HandleRefresh(accessTokenRec, accessTokenReq)

		if accessTokenRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected access token masquerading as refresh token status 401, got %d: %s", accessTokenRec.Code, accessTokenRec.Body.String())
		}

		var accessTokenEnvelope authEnvelope
		if err := json.Unmarshal(accessTokenRec.Body.Bytes(), &accessTokenEnvelope); err != nil {
			t.Fatalf("unmarshal access-token refresh envelope error: %v", err)
		}
		if accessTokenEnvelope.Error == nil {
			t.Fatalf("expected invalid refresh token error payload, got %s", accessTokenRec.Body.String())
		}
		if accessTokenEnvelope.Error.Code != "INVALID_TOKEN" {
			t.Fatalf("expected access-token refresh code INVALID_TOKEN, got %s", accessTokenEnvelope.Error.Code)
		}
	})

	t.Run("refresh token returns warning when revocation persistence fsync fails", func(t *testing.T) {
		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)

		var loginEnvelope authEnvelope
		if err := json.Unmarshal(loginRec.Body.Bytes(), &loginEnvelope); err != nil {
			t.Fatalf("unmarshal login envelope error: %v", err)
		}
		var loginResp LoginResponse
		if err := json.Unmarshal(loginEnvelope.Data, &loginResp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		refreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.RefreshToken})
		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", refreshRec.Code, refreshRec.Body.String())
		}
		if got := refreshRec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		var refreshEnvelope authEnvelope
		if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshEnvelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		if !refreshEnvelope.Success {
			t.Fatalf("expected success response, got %s", refreshRec.Body.String())
		}
		if refreshEnvelope.Message != "refresh token rotated with persistence warning" {
			t.Fatalf("message = %q, want %q", refreshEnvelope.Message, "refresh token rotated with persistence warning")
		}

		replayReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
		replayRec := httptest.NewRecorder()
		h.HandleRefresh(replayRec, replayReq)
		if replayRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected replayed refresh token status 401, got %d: %s", replayRec.Code, replayRec.Body.String())
		}
	})

	t.Run("me endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
		user, _ := store.GetByUsername("handleruser")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleMe(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal me envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatal("expected me success")
		}
	})

	t.Run("download session endpoint", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)

		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           user.ID,
			Username:         user.Username,
			Role:             user.Role,
		})
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		if cookies[0].Name != DownloadSessionCookieName {
			t.Fatalf("expected cookie %q, got %q", DownloadSessionCookieName, cookies[0].Name)
		}
		if cookies[0].Path != "/api/v1" {
			t.Fatalf("expected download cookie path, got %q", cookies[0].Path)
		}
	})

	t.Run("download session cookie ignores spoofed forwarded proto from untrusted source", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)

		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		req.Header.Set("X-Forwarded-Proto", "https")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           user.ID,
			Username:         user.Username,
			Role:             user.Role,
		})
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		if cookies[0].Secure {
			t.Fatal("expected spoofed forwarded proto to leave cookie insecure")
		}
	})

	t.Run("admin list users", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleListUsers(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal users envelope error: %v", err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal users payload error: %v", err)
		}

		users := resp["users"].([]interface{})
		if len(users) < 2 {
			t.Errorf("expected at least 2 users, got %d", len(users))
		}
	})

	t.Run("admin create user", func(t *testing.T) {
		body := `{"username":"newuser","password":"newpass123","email":"new@test.com","role":"user"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatal("expected create user success")
		}
	})

	t.Run("admin create user rejects unknown fields", func(t *testing.T) {
		body := `{"username":"unknownfielduser","password":"newpass123","email":"new@test.com","role":"user","unexpected":true}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("expected INVALID_REQUEST error, got %+v", envelope.Error)
		}
		if _, err := store.GetByUsername("unknownfielduser"); err == nil {
			t.Fatal("expected user creation to be rejected before persistence")
		}
	})

	t.Run("admin create user rejects whitespace-only username", func(t *testing.T) {
		body := `{"username":"   ","password":"newpass123","email":"new@test.com","role":"user"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "MISSING_FIELDS" {
			t.Fatalf("expected MISSING_FIELDS error, got %+v", envelope.Error)
		}
	})

	t.Run("admin create user rejects invalid username", func(t *testing.T) {
		body := `{"username":"../escape","password":"newpass123","email":"new@test.com","role":"user"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		ctx := context.WithValue(req.Context(), ContextKeyUser, admin)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create user envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_USERNAME" {
			t.Fatalf("expected INVALID_USERNAME error, got %+v", envelope.Error)
		}
		if _, err := store.GetByUsername("../escape"); err == nil {
			t.Fatal("expected invalid username create to be rejected before persistence")
		}
	})

	t.Run("non-admin cannot list users", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
		user, _ := store.GetByUsername("handleruser")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.HandleListUsers(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected status 403, got %d", rec.Code)
		}
	})

	t.Run("admin mutation internal errors are hidden", func(t *testing.T) {
		assertInternalError := func(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
			t.Helper()

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
			}

			var envelope authEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("unmarshal envelope error: %v", err)
			}
			if envelope.Error == nil {
				t.Fatalf("expected error payload, got %s", rec.Body.String())
			}
			if envelope.Error.Code != wantCode {
				t.Fatalf("expected error code %s, got %s", wantCode, envelope.Error.Code)
			}
			if envelope.Error.Message != "internal server error" {
				t.Fatalf("expected generic internal error message, got %q", envelope.Error.Message)
			}
		}

		brokenStoreDir := t.TempDir()
		brokenStore, _, err := NewUserStore(filepath.Join(brokenStoreDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create broken store fixture: %v", err)
		}
		admin, err := brokenStore.GetByUsername("admin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		changeUser, err := brokenStore.Create("managed-change", "password123", "managed-change@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}
		deleteUser, err := brokenStore.Create("managed-delete", "password123", "managed-delete@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}
		resetUser, err := brokenStore.Create("managed-reset", "password123", "managed-reset@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}
		toggleUser, err := brokenStore.Create("managed-toggle", "password123", "managed-toggle@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}
		brokenStore.filePath = brokenStoreDir
		brokenHandler := NewHandler(brokenStore, tm)

		createReq := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(`{"username":"brokencreate","password":"password123","email":"broken@test.com","role":"user"}`))
		createReq = createReq.WithContext(context.WithValue(createReq.Context(), ContextKeyUser, admin))
		createRec := httptest.NewRecorder()
		brokenHandler.HandleCreateUser(createRec, createReq)
		assertInternalError(t, createRec, "CREATE_ERROR")

		changeReq := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"password123","new_password":"newpassword456"}`))
		changeReq = changeReq.WithContext(context.WithValue(changeReq.Context(), ContextKeyUser, changeUser))
		changeRec := httptest.NewRecorder()
		brokenHandler.HandleChangePassword(changeRec, changeReq)
		assertInternalError(t, changeRec, "PASSWORD_ERROR")

		deleteReq := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+deleteUser.ID, nil)
		deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), ContextKeyUser, admin))
		deleteRec := httptest.NewRecorder()
		brokenHandler.HandleDeleteUser(deleteRec, deleteReq, deleteUser.ID)
		assertInternalError(t, deleteRec, "DELETE_ERROR")

		resetReq := httptest.NewRequest("POST", "/api/v1/admin/users/"+resetUser.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"resetpass123"}`))
		resetReq = resetReq.WithContext(context.WithValue(resetReq.Context(), ContextKeyUser, admin))
		resetRec := httptest.NewRecorder()
		brokenHandler.HandleResetUserPassword(resetRec, resetReq, resetUser.ID)
		assertInternalError(t, resetRec, "RESET_ERROR")

		toggleReq := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{"disabled":true}`))
		toggleReq = toggleReq.WithContext(context.WithValue(toggleReq.Context(), ContextKeyUser, admin))
		toggleRec := httptest.NewRecorder()
		brokenHandler.HandleToggleUserStatus(toggleRec, toggleReq, toggleUser.ID)
		assertInternalError(t, toggleRec, "UPDATE_ERROR")
	})

	t.Run("admin mutations return warning when only auth persistence fsync fails", func(t *testing.T) {
		warningStoreDir := t.TempDir()
		warningStore, _, err := NewUserStore(filepath.Join(warningStoreDir, "users.json"))
		if err != nil {
			t.Fatalf("failed to create warning store fixture: %v", err)
		}
		warningTM := NewTokenManager("warning-secret", 15*time.Minute, 24*time.Hour)
		warningHandler := NewHandler(warningStore, warningTM)

		admin, err := warningStore.GetByUsername("admin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		changeUser, err := warningStore.Create("warning-change", "password123", "warning-change@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}
		deleteUser, err := warningStore.Create("warning-delete", "password123", "warning-delete@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}
		resetUser, err := warningStore.Create("warning-reset", "password123", "warning-reset@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}
		toggleUser, err := warningStore.Create("warning-toggle", "password123", "warning-toggle@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}

		originalSyncAuthFileDir := syncAuthFileDir
		originalSyncAuthRootDir := syncAuthRootDir
		syncAuthFileDir = func(dir string) error {
			return errors.New("directory fsync failed")
		}
		syncAuthRootDir = func(root *os.Root) error {
			return errors.New("directory fsync failed")
		}
		defer func() {
			syncAuthFileDir = originalSyncAuthFileDir
			syncAuthRootDir = originalSyncAuthRootDir
		}()

		assertWarningSuccess := func(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantMessage string) map[string]interface{} {
			t.Helper()
			if rec.Code != wantStatus {
				t.Fatalf("expected status %d, got %d: %s", wantStatus, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
				t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
			}
			var envelope authEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("unmarshal warning envelope error: %v", err)
			}
			if !envelope.Success {
				t.Fatalf("expected success payload, got %s", rec.Body.String())
			}
			if envelope.Message != wantMessage {
				t.Fatalf("message = %q, want %q", envelope.Message, wantMessage)
			}
			var data map[string]interface{}
			if err := json.Unmarshal(envelope.Data, &data); err != nil {
				t.Fatalf("unmarshal warning data error: %v", err)
			}
			warning, ok := data["warning"].(bool)
			if !ok || !warning {
				t.Fatalf("expected warning flag in payload, got %+v", data)
			}
			return data
		}

		createReq := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(`{"username":"warningcreate","password":"password123","email":"warningcreate@test.com","role":"user"}`))
		createReq = createReq.WithContext(context.WithValue(createReq.Context(), ContextKeyUser, admin))
		createRec := httptest.NewRecorder()
		warningHandler.HandleCreateUser(createRec, createReq)
		createData := assertWarningSuccess(t, createRec, http.StatusCreated, "user created with persistence warning")
		if _, ok := createData["user"].(map[string]interface{}); !ok {
			t.Fatalf("expected user payload for create warning, got %+v", createData)
		}
		if _, err := warningStore.GetByUsername("warningcreate"); err != nil {
			t.Fatalf("expected warned create to commit user, got %v", err)
		}

		changeReq := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"password123","new_password":"newpassword456"}`))
		changeReq = changeReq.WithContext(context.WithValue(changeReq.Context(), ContextKeyUser, changeUser))
		changeRec := httptest.NewRecorder()
		warningHandler.HandleChangePassword(changeRec, changeReq)
		assertWarningSuccess(t, changeRec, http.StatusOK, "password changed with persistence warning")
		if _, err := warningStore.Authenticate("warning-change", "newpassword456"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warned password change, got %v", err)
		}

		deleteReq := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+deleteUser.ID, nil)
		deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), ContextKeyUser, admin))
		deleteRec := httptest.NewRecorder()
		warningHandler.HandleDeleteUser(deleteRec, deleteReq, deleteUser.ID)
		assertWarningSuccess(t, deleteRec, http.StatusOK, "user deleted with persistence warning")
		if _, err := warningStore.GetByID(deleteUser.ID); err != ErrUserNotFound {
			t.Fatalf("expected warned delete to remove user, got %v", err)
		}

		resetReq := httptest.NewRequest("POST", "/api/v1/admin/users/"+resetUser.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"resetpass123"}`))
		resetReq = resetReq.WithContext(context.WithValue(resetReq.Context(), ContextKeyUser, admin))
		resetRec := httptest.NewRecorder()
		warningHandler.HandleResetUserPassword(resetRec, resetReq, resetUser.ID)
		assertWarningSuccess(t, resetRec, http.StatusOK, "password reset with persistence warning")
		if _, err := warningStore.Authenticate("warning-reset", "resetpass123"); !isAuthPersistenceWarning(err) {
			t.Fatalf("expected warning when authenticating warned reset password, got %v", err)
		}

		toggleReq := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{"disabled":true}`))
		toggleReq = toggleReq.WithContext(context.WithValue(toggleReq.Context(), ContextKeyUser, admin))
		toggleRec := httptest.NewRecorder()
		warningHandler.HandleToggleUserStatus(toggleRec, toggleReq, toggleUser.ID)
		toggleData := assertWarningSuccess(t, toggleRec, http.StatusOK, "user status updated with persistence warning")
		disabled, ok := toggleData["disabled"].(bool)
		if !ok || !disabled {
			t.Fatalf("expected disabled=true in toggle warning payload, got %+v", toggleData)
		}
		refreshedToggle, err := warningStore.GetByID(toggleUser.ID)
		if err != nil {
			t.Fatalf("failed to reload warned toggle user: %v", err)
		}
		if !refreshedToggle.Disabled {
			t.Fatal("expected warned toggle to commit disabled state")
		}
	})

	t.Run("change password returns warning and revokes existing tokens when revocation persistence fails hard", func(t *testing.T) {
		changeUser, err := store.Create("hard-revoke-change", "password123", "hard-revoke-change@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(changeUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		req := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"password123","new_password":"newpassword456"}`))
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, changeUser))
		rec := httptest.NewRecorder()

		h.HandleChangePassword(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal change password envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatalf("expected success response, got %s", rec.Body.String())
		}
		if envelope.Message != "password changed with persistence warning" {
			t.Fatalf("message = %q, want %q", envelope.Message, "password changed with persistence warning")
		}

		if _, err := store.Authenticate("hard-revoke-change", "newpassword456"); err != nil {
			t.Fatalf("expected changed password to authenticate successfully, got %v", err)
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after password change, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after password change, got %v", err)
		}
	})

	t.Run("reset password returns warning and revokes existing tokens when revocation persistence fails hard", func(t *testing.T) {
		admin, err := store.GetByUsername("handleradmin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		resetUser, err := store.Create("hard-revoke-reset", "password123", "hard-revoke-reset@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create reset user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(resetUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		req := httptest.NewRequest("POST", "/api/v1/admin/users/"+resetUser.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"resetpass456"}`))
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()

		h.HandleResetUserPassword(rec, req, resetUser.ID)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal reset password envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatalf("expected success response, got %s", rec.Body.String())
		}
		if envelope.Message != "password reset with persistence warning" {
			t.Fatalf("message = %q, want %q", envelope.Message, "password reset with persistence warning")
		}

		if _, err := store.Authenticate("hard-revoke-reset", "resetpass456"); err != nil {
			t.Fatalf("expected reset password to authenticate successfully, got %v", err)
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after password reset, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after password reset, got %v", err)
		}
	})

	t.Run("delete user returns warning and revokes existing tokens when revocation persistence fails hard", func(t *testing.T) {
		admin, err := store.GetByUsername("handleradmin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		deleteUser, err := store.Create("hard-revoke-delete", "password123", "hard-revoke-delete@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(deleteUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		req := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+deleteUser.ID, nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()

		h.HandleDeleteUser(rec, req, deleteUser.ID)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal delete user envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatalf("expected success response, got %s", rec.Body.String())
		}
		if envelope.Message != "user deleted with persistence warning" {
			t.Fatalf("message = %q, want %q", envelope.Message, "user deleted with persistence warning")
		}

		if _, err := store.GetByID(deleteUser.ID); err != ErrUserNotFound {
			t.Fatalf("expected deleted user to be removed from store, got %v", err)
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after delete warning, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after delete warning, got %v", err)
		}
	})

	t.Run("disable user returns warning and revokes existing tokens when revocation persistence fails hard", func(t *testing.T) {
		admin, err := store.GetByUsername("handleradmin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		toggleUser, err := store.Create("hard-revoke-toggle", "password123", "hard-revoke-toggle@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(toggleUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenRevocations
		persistTokenRevocations = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenRevocations = originalPersistTokenRevocations
		}()

		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{"disabled":true}`))
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()

		h.HandleToggleUserStatus(rec, req, toggleUser.ID)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != authPersistenceWarningHeader {
			t.Fatalf("warning header = %q, want %q", got, authPersistenceWarningHeader)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal toggle user envelope error: %v", err)
		}
		if !envelope.Success {
			t.Fatalf("expected success response, got %s", rec.Body.String())
		}
		if envelope.Message != "user status updated with persistence warning" {
			t.Fatalf("message = %q, want %q", envelope.Message, "user status updated with persistence warning")
		}

		updatedUser, err := store.GetByID(toggleUser.ID)
		if err != nil {
			t.Fatalf("GetByID(toggleUser) error: %v", err)
		}
		if !updatedUser.Disabled {
			t.Fatal("expected disabled user state to remain committed after warning")
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after disable warning, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after disable warning, got %v", err)
		}
	})

	t.Run("toggle user status requires disabled field", func(t *testing.T) {
		toggleUser, err := store.Create("toggle-missing-field", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create toggle user: %v", err)
		}
		toggleUser.Disabled = true
		if err := store.Update(toggleUser); err != nil {
			t.Fatalf("failed to disable toggle user: %v", err)
		}

		admin, _ := store.GetByUsername("handleradmin")
		toggleReq := httptest.NewRequest("PUT", "/api/v1/admin/users/"+toggleUser.ID+"/status", bytes.NewBufferString(`{}`))
		toggleReq = toggleReq.WithContext(context.WithValue(toggleReq.Context(), ContextKeyUser, admin))
		toggleRec := httptest.NewRecorder()
		h.HandleToggleUserStatus(toggleRec, toggleReq, toggleUser.ID)

		if toggleRec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", toggleRec.Code, toggleRec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(toggleRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal toggle envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "MISSING_DISABLED" {
			t.Fatalf("expected MISSING_DISABLED error, got %+v", envelope.Error)
		}

		refreshed, err := store.GetByID(toggleUser.ID)
		if err != nil {
			t.Fatalf("failed to reload toggle user: %v", err)
		}
		if !refreshed.Disabled {
			t.Fatal("expected missing disabled field to leave user disabled")
		}
	})

	t.Run("delete user revokes outstanding refresh tokens", func(t *testing.T) {
		admin, _ := store.GetByUsername("handleradmin")
		deleteUser, err := store.Create("delete-revoke", "password123", "delete-revoke@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create delete user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(deleteUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		deleteReq := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+deleteUser.ID, nil)
		deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), ContextKeyUser, admin))
		deleteRec := httptest.NewRecorder()
		h.HandleDeleteUser(deleteRec, deleteReq, deleteUser.ID)

		if deleteRec.Code != http.StatusOK {
			t.Fatalf("expected delete status 200, got %d: %s", deleteRec.Code, deleteRec.Body.String())
		}

		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBufferString(`{"refresh_token":"`+tokenPair.RefreshToken+`"}`))
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected refresh status 401, got %d: %s", refreshRec.Code, refreshRec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(refreshRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "TOKEN_REVOKED" {
			t.Fatalf("expected TOKEN_REVOKED after deleting user, got %+v", envelope.Error)
		}
	})
}

func TestWriteSuccess_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	rec := httptest.NewRecorder()

	writeSuccess(rec, http.StatusOK, map[string]interface{}{
		"bad": make(chan int),
	}, "ok")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if rec.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", rec.Body.String())
	}
}

func TestWriteSuccess_IncludesNullDataForNilPayload(t *testing.T) {
	rec := httptest.NewRecorder()

	writeSuccess(rec, http.StatusOK, nil, "ok")

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	data, ok := payload["data"]
	if !ok {
		t.Fatalf("expected response to include data field, got %s", rec.Body.String())
	}
	if string(data) != "null" {
		t.Fatalf("expected data field to be null, got %s", string(data))
	}
}

func TestLoginAttemptTracker_PrunesStaleEntriesOnNewFailure(t *testing.T) {
	tracker := newLoginAttemptTracker()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	tracker.attempts["stale"] = loginAttemptState{failures: 1, lastFailure: now.Add(-2 * time.Minute)}

	tracker.recordFailure("fresh", 5, time.Minute, time.Minute)

	if _, ok := tracker.attempts["stale"]; ok {
		t.Fatal("expected stale tracker entry to be pruned")
	}
	if _, ok := tracker.attempts["fresh"]; !ok {
		t.Fatal("expected fresh tracker entry to remain")
	}
}

func TestDefaultAdminCreation(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "new_users.json")

	store, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("default admin should exist: %v", err)
	}

	if admin.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", admin.Role)
	}
}

func TestUserStoreQuota(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "quota_users.json")
	store, _, _ := NewUserStore(usersFile)

	user, _ := store.Create("quotauser", "password123", "", RoleUser)

	t.Run("set and check quota", func(t *testing.T) {
		user.QuotaBytes = 1024 * 1024 * 100
		user.UsedBytes = 1024 * 1024 * 50
		store.Update(user)

		updated, _ := store.GetByID(user.ID)
		if updated.QuotaBytes != 1024*1024*100 {
			t.Errorf("expected quota 100MB, got %d", updated.QuotaBytes)
		}
		if updated.UsedBytes != 1024*1024*50 {
			t.Errorf("expected used 50MB, got %d", updated.UsedBytes)
		}
	})
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
