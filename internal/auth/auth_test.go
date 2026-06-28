package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/seanbao/mnemonas/internal/requestip"
)

func TestAuthPersistenceWarningWrapperPreservesErrorSemantics(t *testing.T) {
	baseErr := errors.New("directory sync failed")

	if got := wrapAuthPersistenceWarning(nil); got != nil {
		t.Fatalf("wrapAuthPersistenceWarning(nil) = %v, want nil", got)
	}
	warningErr := wrapAuthPersistenceWarning(baseErr)
	if !isAuthPersistenceWarning(warningErr) {
		t.Fatalf("expected auth persistence warning, got %T", warningErr)
	}
	if !errors.Is(warningErr, baseErr) {
		t.Fatalf("expected auth persistence warning to unwrap %v", baseErr)
	}
	if warningErr.Error() != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", warningErr.Error(), baseErr.Error())
	}
	if got := WrapPersistenceWarning(nil); got != nil {
		t.Fatalf("WrapPersistenceWarning(nil) = %v, want nil", got)
	}
	if got := WrapPersistenceWarning(warningErr); got != warningErr {
		t.Fatalf("WrapPersistenceWarning(existing warning) = %T, want original warning", got)
	}
}

func TestShouldPrintInitialPasswordToTerminal(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "yes uppercase", value: "YES", want: true},
		{name: "off", value: "off", want: false},
		{name: "random", value: "please", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(printInitialPasswordEnv, tc.value)
			if got := shouldPrintInitialPasswordToTerminal(); got != tc.want {
				t.Fatalf("shouldPrintInitialPasswordToTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWriteRegisteredAuthFileAtomically_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "users.json")

	originalSyncAuthRootDir := syncAuthRootDir
	syncAuthRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncAuthRootDir = originalSyncAuthRootDir
	}()

	err := writeRegisteredAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if err == nil {
		t.Fatal("expected writeRegisteredAuthFileAtomically() to fail when directory sync fails")
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

func TestWriteRegisteredAuthFileAtomically_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	usersPath := filepath.Join(tmpDir, "nested", "state", "users.json")

	originalSyncAuthFileDir := syncAuthFileDir
	syncAuthFileDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncAuthFileDir = originalSyncAuthFileDir
	}()

	err := writeRegisteredAuthFileAtomically(usersPath, []byte("[]"), errUserStoreSymlink, ".users-*.tmp", "users")
	if err == nil {
		t.Fatal("expected writeRegisteredAuthFileAtomically() to fail when directory tree sync fails")
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
		"user-zeta":  {ID: "user-zeta", Username: "zeta", CredentialVersion: 1},
		"user-alpha": {ID: "user-alpha", Username: "Alpha", CredentialVersion: 1},
		"user-beta":  {ID: "user-beta", Username: "beta", CredentialVersion: 1},
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

		var state persistedUserStore
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatalf("Unmarshal(persisted users) error: %v", err)
		}
		if state.SchemaVersion != userStoreSchemaVersion {
			t.Fatalf("persisted schema version = %d, want %d", state.SchemaVersion, userStoreSchemaVersion)
		}
		persisted := state.Users
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

func TestNewUserStore_LoadRejectsUsersSymlinkInsertedAfterValidation(t *testing.T) {
	managedDir := t.TempDir()
	usersPath := filepath.Join(managedDir, "users.json")
	existingData := `{"schema_version":1,"users":[{"id":"existing-admin","username":"admin","password_hash":"` + testValidPasswordHash + `","role":"admin","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","must_change_password":false,"credential_version":1,"home_dir":"/"}]}`
	if err := os.WriteFile(usersPath, []byte(existingData), 0600); err != nil {
		t.Fatalf("failed to setup users file: %v", err)
	}
	linkedTarget := filepath.Join(managedDir, "linked-users.json")
	if err := os.WriteFile(linkedTarget, []byte(`{"schema_version":1,"users":[]}`), 0600); err != nil {
		t.Fatalf("failed to setup linked users file: %v", err)
	}

	originalHook := afterValidateAuthFilePath
	var hookErr error
	swapped := false
	afterValidateAuthFilePath = func() {
		if hookErr != nil || swapped {
			return
		}
		swapped = true
		if err := os.Remove(usersPath); err != nil {
			hookErr = err
			return
		}
		hookErr = os.Symlink(filepath.Base(linkedTarget), usersPath)
	}
	t.Cleanup(func() {
		afterValidateAuthFilePath = originalHook
	})

	_, _, err := NewUserStore(usersPath)
	if hookErr != nil {
		t.Fatalf("afterValidateAuthFilePath hook error: %v", hookErr)
	}
	if !errors.Is(err, errUserStoreSymlink) {
		t.Fatalf("expected users file symlink rejection, got %v", err)
	}
}

func TestTokenManager_SessionWriteDoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	managedDir := filepath.Join(baseDir, "managed")
	backupDir := filepath.Join(baseDir, "managed-real")
	revocationFile := filepath.Join(managedDir, "auth-sessions.json")

	tm := NewTokenManager(strings.Repeat("s", 32), 15*time.Minute, 24*time.Hour)
	if err := tm.EnableSessionPersistence(revocationFile); err != nil {
		t.Fatalf("EnableSessionPersistence() error: %v", err)
	}

	outsideDir := t.TempDir()
	outsideRevocationFile := filepath.Join(outsideDir, "auth-sessions.json")
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

	if _, err := tm.GenerateTokenPair(&User{ID: "symlink-write-user", Username: "symlink-write-user", Role: RoleUser}); err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	outsideData, err := os.ReadFile(outsideRevocationFile)
	if err != nil {
		t.Fatalf("failed to read outside revocation file: %v", err)
	}
	if !bytes.Equal(outsideData, outsideContent) {
		t.Fatalf("expected outside revocation file to remain unchanged, got %s", string(outsideData))
	}

	managedData, err := os.ReadFile(filepath.Join(backupDir, "auth-sessions.json"))
	if err != nil {
		t.Fatalf("failed to read rooted revocation file: %v", err)
	}
	if !strings.Contains(string(managedData), "symlink-write-user") {
		t.Fatalf("expected rooted session file to contain issued session, got %s", string(managedData))
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
		if store.Count() != 2 {
			t.Fatalf("expected default admin plus created user, got %d users", store.Count())
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

	t.Run("verify credentials without login mutation", func(t *testing.T) {
		usersFile := filepath.Join(dir, "users-verify.json")
		store, _, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		user, err := store.Create("verifyuser", "password123", "verify@example.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		verified, err := store.VerifyCredentials("verifyuser", "password123")
		if err != nil {
			t.Fatalf("failed to verify credentials: %v", err)
		}
		if verified.ID != user.ID {
			t.Fatalf("verified user ID = %s, want %s", verified.ID, user.ID)
		}
		if verified.LastLoginAt != nil {
			t.Fatal("VerifyCredentials should not update last_login_at")
		}

		reloaded, err := store.GetByUsername("verifyuser")
		if err != nil {
			t.Fatalf("failed to reload user: %v", err)
		}
		if reloaded.LastLoginAt != nil {
			t.Fatal("VerifyCredentials should not persist last_login_at")
		}

		if _, err := store.VerifyCredentials("verifyuser", "wrongpassword"); err != ErrInvalidCredentials {
			t.Fatalf("wrong password error = %v, want %v", err, ErrInvalidCredentials)
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

	t.Run("long password", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-long-password.json"))
		_, err := store.Create("longpass", strings.Repeat("a", 73), "", RoleUser)
		if err != ErrPasswordTooLong {
			t.Errorf("expected ErrPasswordTooLong, got %v", err)
		}
	})

	t.Run("invalid username", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-invalid-username.json"))
		_, err := store.Create("../escape", "password123", "", RoleUser)
		if err != ErrInvalidUsername {
			t.Fatalf("expected ErrInvalidUsername, got %v", err)
		}
		_, err = store.Create(strings.Repeat("a", 256), "password123", "", RoleUser)
		if err != ErrInvalidUsername {
			t.Fatalf("expected ErrInvalidUsername for overlong username, got %v", err)
		}
		if _, lookupErr := store.GetByUsername("../escape"); lookupErr != ErrUserNotFound {
			t.Fatalf("expected invalid username create to leave no persisted user, got %v", lookupErr)
		}
	})

	t.Run("invalid role", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-invalid-role.json"))
		if _, err := store.Create("badrole", "password123", "", Role("owner")); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("expected ErrInvalidRole from create, got %v", err)
		}
		if _, lookupErr := store.GetByUsername("badrole"); !errors.Is(lookupErr, ErrUserNotFound) {
			t.Fatalf("expected invalid role create to leave no persisted user, got %v", lookupErr)
		}
	})

	t.Run("create with groups normalizes memberships", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-groups.json"))
		user, err := store.CreateWithGroups("groupuser", "password123", "", RoleUser, []string{"Family", "editors", "family", " qa-team "})
		if err != nil {
			t.Fatalf("CreateWithGroups() error: %v", err)
		}
		if got, want := strings.Join(user.Groups, ","), "editors,family,qa-team"; got != want {
			t.Fatalf("groups = %q, want %q", got, want)
		}
		fresh, err := store.GetByUsername("groupuser")
		if err != nil {
			t.Fatalf("GetByUsername(groupuser) error: %v", err)
		}
		if got, want := strings.Join(fresh.Groups, ","), "editors,family,qa-team"; got != want {
			t.Fatalf("stored groups = %q, want %q", got, want)
		}
	})

	t.Run("create rejects invalid groups", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users3-invalid-groups.json"))
		if _, err := store.CreateWithGroups("badgroups", "password123", "", RoleUser, []string{"family/team"}); !errors.Is(err, errInvalidUserGroups) {
			t.Fatalf("expected invalid groups error, got %v", err)
		}
		if _, lookupErr := store.GetByUsername("badgroups"); !errors.Is(lookupErr, ErrUserNotFound) {
			t.Fatalf("expected invalid groups create to leave no persisted user, got %v", lookupErr)
		}
	})

	t.Run("invalid stored home dir", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			homeDir string
		}{
			{name: "empty", homeDir: ""},
			{name: "dot-segment", homeDir: "/broken/./home"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				usersPath := filepath.Join(dir, "users-invalid-home-dir-"+tc.name+".json")
				content := fmt.Sprintf(`{
		  "schema_version": 1,
		  "users": [
		  {
		    "id": "u-invalid-home",
		    "username": "broken-user",
		    "password_hash": "$2a$10$dummy",
		    "role": "user",
		    "created_at": "2024-01-01T00:00:00Z",
		    "updated_at": "2024-01-01T00:00:00Z",
		    "must_change_password": false,
		    "credential_version": 1,
		    "home_dir": %q
		  }
		]}`, tc.homeDir)
				if err := os.WriteFile(usersPath, []byte(content), 0600); err != nil {
					t.Fatalf("failed to seed users file: %v", err)
				}

				if _, _, err := NewUserStore(usersPath); err == nil {
					t.Fatal("expected invalid home_dir in users file to fail")
				} else if !strings.Contains(err.Error(), "invalid home_dir") {
					t.Fatalf("expected invalid home_dir error, got %v", err)
				}
			})
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

	t.Run("authenticate retains initial password file until the default admin changes it", func(t *testing.T) {
		storeDir := t.TempDir()
		usersFile := filepath.Join(storeDir, "users.json")
		store, password, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}
		passwordFile := filepath.Join(storeDir, "initial-password.txt")
		if _, statErr := os.Stat(passwordFile); statErr != nil {
			t.Fatalf("expected initial password file before login, got %v", statErr)
		}

		if _, err := store.Authenticate("admin", password); err != nil {
			t.Fatalf("Authenticate(default admin) error: %v", err)
		}
		if _, statErr := os.Stat(passwordFile); statErr != nil {
			t.Fatalf("expected initial password file to remain after login, got %v", statErr)
		}
	})

	t.Run("authenticate keeps initial password file when default admin login persist fails hard", func(t *testing.T) {
		storeDir := t.TempDir()
		usersFile := filepath.Join(storeDir, "users.json")
		store, password, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}
		passwordFile := filepath.Join(storeDir, "initial-password.txt")
		if _, statErr := os.Stat(passwordFile); statErr != nil {
			t.Fatalf("expected initial password file before login, got %v", statErr)
		}

		originalWriter := userStoreWriter
		userStoreWriter = func(path string, data []byte) error {
			return errors.New("persist failed")
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
		})

		authUser, err := store.Authenticate("admin", password)
		if err == nil || isAuthPersistenceWarning(err) {
			t.Fatalf("expected hard persistence error, got %v", err)
		}
		if authUser != nil {
			t.Fatalf("expected Authenticate() to fail closed on hard persistence error, got %+v", authUser)
		}
		admin, lookupErr := store.GetByUsername("admin")
		if lookupErr != nil {
			t.Fatalf("GetByUsername(admin) error: %v", lookupErr)
		}
		if admin.LastLoginAt != nil {
			t.Fatalf("expected failed login persist to leave last_login_at unset, got %v", admin.LastLoginAt)
		}
		if _, statErr := os.Stat(passwordFile); statErr != nil {
			t.Fatalf("expected initial password file to remain after failed login persist, got %v", statErr)
		}
	})

	t.Run("new user store keeps default admin password when users save returns persistence warning", func(t *testing.T) {
		storeDir := t.TempDir()
		usersFile := filepath.Join(storeDir, "users.json")
		passwordFile := filepath.Join(storeDir, "initial-password.txt")

		originalWriter := userStoreWriter
		userStoreWriter = func(path string, data []byte) error {
			if err := originalWriter(path, data); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
		})

		store, password, err := NewUserStore(usersFile)
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected persistence warning from NewUserStore, got %v", err)
		}
		if store == nil {
			t.Fatal("expected user store to be returned with persistence warning")
		}
		if password == "" {
			t.Fatal("expected initial password to be returned with persistence warning")
		}
		if _, statErr := os.Stat(passwordFile); statErr != nil {
			t.Fatalf("expected initial password file to remain after persistence warning, got %v", statErr)
		}
		if _, statErr := os.Stat(usersFile); statErr != nil {
			t.Fatalf("expected users file to remain after persistence warning, got %v", statErr)
		}
		if _, authErr := store.Authenticate("admin", password); !isAuthPersistenceWarning(authErr) {
			t.Fatalf("expected returned store to authenticate default admin with persistence warning, got %v", authErr)
		}

		userStoreWriter = originalWriter
		reloaded, reloadedPassword, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("expected reload after warning to succeed, got %v", err)
		}
		if reloadedPassword != "" {
			t.Fatalf("expected reload to avoid creating another password, got %q", reloadedPassword)
		}
		if _, authErr := reloaded.Authenticate("admin", password); authErr != nil {
			t.Fatalf("expected reloaded store to authenticate default admin, got %v", authErr)
		}
	})

	t.Run("password changes remove the matching initial password file", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			change     func(*UserStore, *User, string) error
			mustChange bool
		}{
			{
				name: "change password",
				change: func(store *UserStore, user *User, initialPassword string) error {
					return store.ChangePassword(user.ID, initialPassword, "changed-password-123")
				},
				mustChange: false,
			},
			{
				name: "administrator reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetPassword(user.ID, "reset-password-123")
				},
				mustChange: true,
			},
			{
				name: "self reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetOwnPassword(user.ID, "self-reset-password-123")
				},
				mustChange: false,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				storeDir := t.TempDir()
				usersFile := filepath.Join(storeDir, "users.json")
				store, initialPassword, err := NewUserStore(usersFile)
				if err != nil {
					t.Fatalf("NewUserStore() error: %v", err)
				}
				admin, err := store.GetByUsername("admin")
				if err != nil {
					t.Fatalf("GetByUsername(admin) error: %v", err)
				}
				passwordFile := filepath.Join(storeDir, "initial-password.txt")

				if err := tc.change(store, admin, initialPassword); err != nil {
					t.Fatalf("password mutation error: %v", err)
				}
				if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
					t.Fatalf("expected matching initial password file to be removed, got %v", statErr)
				}
				updated, err := store.GetByID(admin.ID)
				if err != nil {
					t.Fatalf("GetByID(admin) error: %v", err)
				}
				if updated.MustChangePassword != tc.mustChange {
					t.Fatalf("MustChangePassword = %v, want %v", updated.MustChangePassword, tc.mustChange)
				}
			})
		}
	})

	t.Run("password mutations restore the initial password file after a hard state save failure", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(*UserStore, *User, string) error
		}{
			{
				name: "change password",
				change: func(store *UserStore, user *User, initialPassword string) error {
					return store.ChangePassword(user.ID, initialPassword, "changed-password-123")
				},
			},
			{
				name: "administrator reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetPassword(user.ID, "reset-password-123")
				},
			},
			{
				name: "self reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetOwnPassword(user.ID, "self-reset-password-123")
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				storeDir := t.TempDir()
				usersFile := filepath.Join(storeDir, "users.json")
				store, initialPassword, err := NewUserStore(usersFile)
				if err != nil {
					t.Fatalf("NewUserStore() error: %v", err)
				}
				admin, err := store.GetByUsername("admin")
				if err != nil {
					t.Fatalf("GetByUsername(admin) error: %v", err)
				}
				passwordFile := filepath.Join(storeDir, "initial-password.txt")
				originalContent, err := os.ReadFile(passwordFile)
				if err != nil {
					t.Fatalf("ReadFile(initial password) error: %v", err)
				}

				originalWriter := userStoreWriter
				userStoreWriter = func(string, []byte) error {
					return errors.New("persist failed")
				}
				t.Cleanup(func() {
					userStoreWriter = originalWriter
				})

				err = tc.change(store, admin, initialPassword)
				if err == nil || isAuthPersistenceWarning(err) {
					t.Fatalf("expected hard persistence error, got %v", err)
				}
				restoredContent, readErr := os.ReadFile(passwordFile)
				if readErr != nil {
					t.Fatalf("expected initial password file to be restored, got %v", readErr)
				}
				if !bytes.Equal(restoredContent, originalContent) {
					t.Fatalf("restored initial password content = %q, want %q", restoredContent, originalContent)
				}
			})
		}
	})

	t.Run("restore durability warning does not hide a hard password state failure", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(*UserStore, *User, string) error
		}{
			{
				name: "change password",
				change: func(store *UserStore, user *User, initialPassword string) error {
					return store.ChangePassword(user.ID, initialPassword, "changed-password-123")
				},
			},
			{
				name: "administrator reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetPassword(user.ID, "reset-password-123")
				},
			},
			{
				name: "self reset",
				change: func(store *UserStore, user *User, _ string) error {
					return store.ResetOwnPassword(user.ID, "self-reset-password-123")
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				storeDir := t.TempDir()
				usersFile := filepath.Join(storeDir, "users.json")
				store, initialPassword, err := NewUserStore(usersFile)
				if err != nil {
					t.Fatalf("NewUserStore() error: %v", err)
				}
				admin, err := store.GetByUsername("admin")
				if err != nil {
					t.Fatalf("GetByUsername(admin) error: %v", err)
				}
				passwordFile := filepath.Join(storeDir, "initial-password.txt")
				persistErr := errors.New("persist failed")

				originalWriter := userStoreWriter
				userStoreWriter = func(string, []byte) error { return persistErr }
				originalSyncAuthRootDir := syncAuthRootDir
				var syncCalls int
				syncAuthRootDir = func(root *os.Root) error {
					syncCalls++
					if syncCalls == 2 {
						return errors.New("restore directory fsync failed")
					}
					return originalSyncAuthRootDir(root)
				}
				t.Cleanup(func() {
					userStoreWriter = originalWriter
					syncAuthRootDir = originalSyncAuthRootDir
				})

				err = tc.change(store, admin, initialPassword)
				if err == nil || !errors.Is(err, persistErr) || isAuthPersistenceWarning(err) {
					t.Fatalf("password mutation error = %v, want authoritative hard persistence failure", err)
				}
				if !strings.Contains(err.Error(), "restore initial password file") || !strings.Contains(err.Error(), "restore directory fsync failed") {
					t.Fatalf("password mutation error = %v, want restore durability warning", err)
				}
				if syncCalls != 2 {
					t.Fatalf("directory sync calls = %d, want removal plus restore", syncCalls)
				}
				if _, statErr := os.Stat(passwordFile); statErr != nil {
					t.Fatalf("expected restored initial password file, got %v", statErr)
				}
				updated, err := store.GetByID(admin.ID)
				if err != nil {
					t.Fatalf("GetByID(admin) error: %v", err)
				}
				if !reflect.DeepEqual(updated, admin) {
					t.Fatalf("hard failure changed in-memory user: got %+v, want %+v", updated, admin)
				}
			})
		}
	})

	t.Run("password handler reports hard failure when bootstrap rollback sync is uncertain", func(t *testing.T) {
		storeDir := t.TempDir()
		store, initialPassword, err := NewUserStore(filepath.Join(storeDir, "users.json"))
		if err != nil {
			t.Fatalf("NewUserStore() error: %v", err)
		}
		admin, err := store.GetByUsername("admin")
		if err != nil {
			t.Fatalf("GetByUsername(admin) error: %v", err)
		}

		originalWriter := userStoreWriter
		userStoreWriter = func(string, []byte) error { return errors.New("persist failed") }
		originalSyncAuthRootDir := syncAuthRootDir
		var syncCalls int
		syncAuthRootDir = func(root *os.Root) error {
			syncCalls++
			if syncCalls == 2 {
				return errors.New("restore directory fsync failed")
			}
			return originalSyncAuthRootDir(root)
		}
		t.Cleanup(func() {
			userStoreWriter = originalWriter
			syncAuthRootDir = originalSyncAuthRootDir
		})

		handler := NewHandler(store, NewTokenManager("rollback-test-secret", time.Minute, time.Hour))
		body := fmt.Sprintf(`{"expected_user_id":%q,"old_password":%q,"new_password":"changed-password-123"}`, admin.ID, initialPassword)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		handler.HandleChangePassword(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("password handler status = %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
		}
		if _, statErr := os.Stat(filepath.Join(storeDir, "initial-password.txt")); statErr != nil {
			t.Fatalf("expected restored initial password file, got %v", statErr)
		}
	})

	t.Run("initial password removal sync warning keeps password change committed", func(t *testing.T) {
		storeDir := t.TempDir()
		usersFile := filepath.Join(storeDir, "users.json")
		store, initialPassword, err := NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("NewUserStore() error: %v", err)
		}
		admin, err := store.GetByUsername("admin")
		if err != nil {
			t.Fatalf("GetByUsername(admin) error: %v", err)
		}
		passwordFile := filepath.Join(storeDir, "initial-password.txt")

		originalSyncAuthRootDir := syncAuthRootDir
		var syncCalls int
		syncAuthRootDir = func(root *os.Root) error {
			syncCalls++
			if syncCalls == 1 {
				return errors.New("directory fsync failed")
			}
			return originalSyncAuthRootDir(root)
		}
		t.Cleanup(func() {
			syncAuthRootDir = originalSyncAuthRootDir
		})

		err = store.ChangePassword(admin.ID, initialPassword, "changed-password-123")
		if !isAuthPersistenceWarning(err) {
			t.Fatalf("expected initial password removal persistence warning, got %v", err)
		}
		if syncCalls < 2 {
			t.Fatalf("expected removal and user-state directory syncs, got %d", syncCalls)
		}
		if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
			t.Fatalf("expected initial password file to remain removed after warning, got %v", statErr)
		}
		updated, err := store.GetByID(admin.ID)
		if err != nil {
			t.Fatalf("GetByID(admin) error: %v", err)
		}
		if updated.MustChangePassword {
			t.Fatal("expected password change to clear forced-change state after warning")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("changed-password-123")); err != nil {
			t.Fatalf("expected new password hash to be committed after warning: %v", err)
		}
	})

	t.Run("invalid stored role", func(t *testing.T) {
		usersPath := filepath.Join(dir, "users-invalid-role.json")
		content := `{
		  "schema_version": 1,
		  "users": [
		  {
		    "id": "u-invalid-role",
		    "username": "broken-role",
		    "password_hash": "$2a$10$dummy",
		    "role": "owner",
		    "created_at": "2024-01-01T00:00:00Z",
		    "updated_at": "2024-01-01T00:00:00Z",
		    "must_change_password": false,
		    "credential_version": 1,
		    "home_dir": "/broken-role"
		  }
		]}`
		if err := os.WriteFile(usersPath, []byte(content), 0600); err != nil {
			t.Fatalf("WriteFile(users-invalid-role) error: %v", err)
		}

		if _, _, err := NewUserStore(usersPath); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("expected ErrInvalidRole from load, got %v", err)
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

		user.HomeDir = "/homefix/./docs"
		if err := store.Update(user); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("expected invalid dot-segment home_dir error, got %v", err)
		}

		user.HomeDir = "/homefix/docs\x00secret"
		if err := store.Update(user); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("expected invalid NUL home_dir error, got %v", err)
		}

		user.HomeDir = "/homefix/docs\x07secret"
		if err := store.Update(user); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("expected invalid control-character home_dir error, got %v", err)
		}

		fresh, err = store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload user after rejected update: %v", err)
		}
		if fresh.HomeDir != "/homefix/docs" {
			t.Fatalf("expected stored home_dir to remain /homefix/docs, got %s", fresh.HomeDir)
		}
	})

	t.Run("create rejects root home dir for non-admin roles", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-root-create-home-dir.json"))
		rootHome := "/"

		for _, tc := range []struct {
			role     Role
			username string
		}{
			{role: RoleUser, username: "rootuser"},
			{role: RoleGuest, username: "rootguest"},
		} {
			_, err := store.CreateWithOptions(tc.username, "password123", "", tc.role, CreateUserOptions{HomeDir: &rootHome})
			if !errors.Is(err, errInvalidUserHomeDir) {
				t.Fatalf("CreateWithOptions(%s, root home_dir) error = %v, want errInvalidUserHomeDir", tc.role, err)
			}
			if _, err := store.GetByUsername(tc.username); !errors.Is(err, ErrUserNotFound) {
				t.Fatalf("expected %s to remain unpersisted, got %v", tc.username, err)
			}
		}

		admin, err := store.CreateWithOptions("rootadmin", "password123", "", RoleAdmin, CreateUserOptions{HomeDir: &rootHome})
		if err != nil {
			t.Fatalf("expected admin root home_dir to remain valid, got %v", err)
		}
		if admin.HomeDir != "/" {
			t.Fatalf("admin home_dir = %q, want /", admin.HomeDir)
		}
	})

	t.Run("update rejects root home dir for non-admin roles", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-root-update-home-dir.json"))
		rootHome := "/"

		user, err := store.Create("rootupdateuser", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("Create(rootupdateuser) error: %v", err)
		}
		user.HomeDir = rootHome
		if err := store.Update(user); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("Update(user root home_dir) error = %v, want errInvalidUserHomeDir", err)
		}
		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("GetByID(rootupdateuser) error: %v", err)
		}
		if fresh.HomeDir != "/rootupdateuser" {
			t.Fatalf("expected rejected update to preserve /rootupdateuser, got %q", fresh.HomeDir)
		}

		admin, err := store.CreateWithOptions("rootupdateadmin", "password123", "", RoleAdmin, CreateUserOptions{HomeDir: &rootHome})
		if err != nil {
			t.Fatalf("CreateWithOptions(rootupdateadmin) error: %v", err)
		}
		admin.Role = RoleGuest
		if err := store.Update(admin); !errors.Is(err, errInvalidUserHomeDir) {
			t.Fatalf("Update(admin demotion with root home_dir) error = %v, want errInvalidUserHomeDir", err)
		}
		fresh, err = store.GetByID(admin.ID)
		if err != nil {
			t.Fatalf("GetByID(rootupdateadmin) error: %v", err)
		}
		if fresh.Role != RoleAdmin || fresh.HomeDir != "/" {
			t.Fatalf("expected rejected demotion to preserve admin root home_dir, got role=%s home_dir=%q", fresh.Role, fresh.HomeDir)
		}
	})

	t.Run("update validates and normalizes groups", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-groups.json"))
		user, _ := store.Create("groupfix", "password123", "", RoleUser)

		user.Groups = []string{"Family", "editors", "family"}
		if err := store.Update(user); err != nil {
			t.Fatalf("Update(groups) error: %v", err)
		}
		fresh, err := store.GetByUsername("groupfix")
		if err != nil {
			t.Fatalf("GetByUsername(groupfix) error: %v", err)
		}
		if got, want := strings.Join(fresh.Groups, ","), "editors,family"; got != want {
			t.Fatalf("stored groups = %q, want %q", got, want)
		}

		fresh.Groups = []string{""}
		if err := store.Update(fresh); !errors.Is(err, errInvalidUserGroups) {
			t.Fatalf("expected invalid groups error, got %v", err)
		}
	})

	t.Run("update validates username and preserves unique index", func(t *testing.T) {
		store, _, _ := NewUserStore(filepath.Join(dir, "users5-username.json"))
		user, _ := store.Create("namefix", "password123", "", RoleUser)
		other, _ := store.Create("othername", "password123", "", RoleUser)

		user.Username = "renamed"
		if err := store.Update(user); err != nil {
			t.Fatalf("expected username update to succeed, got %v", err)
		}
		if _, err := store.GetByUsername("renamed"); err != nil {
			t.Fatalf("expected renamed username lookup to succeed: %v", err)
		}
		if _, err := store.GetByUsername("namefix"); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected old username lookup to fail, got %v", err)
		}

		user.Username = "bad/name"
		if err := store.Update(user); !errors.Is(err, ErrInvalidUsername) {
			t.Fatalf("expected invalid username error, got %v", err)
		}

		user.Username = other.Username
		if err := store.Update(user); !errors.Is(err, ErrUserExists) {
			t.Fatalf("expected duplicate username error, got %v", err)
		}
		user.Username = "renamed"
		user.Role = Role("owner")
		if err := store.Update(user); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("expected invalid role error, got %v", err)
		}

		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload user after rejected username updates: %v", err)
		}
		if fresh.Username != "renamed" {
			t.Fatalf("expected stored username to remain renamed, got %s", fresh.Username)
		}
		if fresh.Role != RoleUser {
			t.Fatalf("expected stored role to remain user, got %s", fresh.Role)
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

	t.Run("update cannot remove last enabled admin", func(t *testing.T) {
		store, _, err := NewUserStore(filepath.Join(dir, "users7-update-last-admin.json"))
		if err != nil {
			t.Fatalf("failed to create user store: %v", err)
		}

		admin, err := store.GetByUsername("admin")
		if err != nil {
			t.Fatalf("failed to get default admin: %v", err)
		}

		demoted := cloneUser(admin)
		demoted.Role = RoleUser
		if err := store.Update(demoted); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin when demoting only enabled admin, got %v", err)
		}

		disabled := cloneUser(admin)
		disabled.Disabled = true
		if err := store.Update(disabled); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin when disabling only enabled admin, got %v", err)
		}

		fresh, err := store.GetByID(admin.ID)
		if err != nil {
			t.Fatalf("failed to reload admin: %v", err)
		}
		if fresh.Role != RoleAdmin || fresh.Disabled {
			t.Fatalf("expected rejected updates to preserve enabled admin, got role=%s disabled=%v", fresh.Role, fresh.Disabled)
		}

		secondAdmin, err := store.Create("second-admin", "password123", "", RoleAdmin)
		if err != nil {
			t.Fatalf("failed to create second admin: %v", err)
		}
		secondAdmin.Role = RoleUser
		if err := store.Update(secondAdmin); err != nil {
			t.Fatalf("expected demoting one of two enabled admins to succeed, got %v", err)
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
		waitForPersist := 15 * time.Second
		go func() {
			_, createErr := store.Create("firstuser", "password123", "first@example.com", RoleUser)
			firstDone <- createErr
		}()

		select {
		case <-firstStarted:
		case <-time.After(waitForPersist):
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
		case <-time.After(waitForPersist):
			t.Fatal("first Create() did not finish after releasing writer")
		}

		select {
		case <-secondStarted:
		case <-time.After(waitForPersist):
			t.Fatal("timed out waiting for second user-store persist to start")
		}

		select {
		case err := <-secondDone:
			if err != nil {
				t.Fatalf("second Create() error: %v", err)
			}
		case <-time.After(waitForPersist):
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

	t.Run("updated expiries affect new token pairs", func(t *testing.T) {
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		tm.UpdateExpiries(45*time.Minute, 36*time.Hour)

		user := &User{
			ID:       "user-updated-expiry",
			Username: "updated-expiry",
			Role:     RoleUser,
		}

		issuedAt := time.Now()
		tokenPair, err := tm.GenerateTokenPair(user)
		if err != nil {
			t.Fatalf("failed to generate token pair: %v", err)
		}

		if tokenPair.ExpiresAt.Before(issuedAt.Add(44*time.Minute)) || tokenPair.ExpiresAt.After(issuedAt.Add(46*time.Minute)) {
			t.Fatalf("access expiry = %s, want about 45m after %s", tokenPair.ExpiresAt, issuedAt)
		}
		if tokenPair.RefreshExpiresAt.Before(issuedAt.Add(35*time.Hour)) || tokenPair.RefreshExpiresAt.After(issuedAt.Add(37*time.Hour)) {
			t.Fatalf("refresh expiry = %s, want about 36h after %s", tokenPair.RefreshExpiresAt, issuedAt)
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

	t.Run("revoke by user rolls back session removal on hard persistence failure", func(t *testing.T) {
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

		originalPersistTokenSessionState := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenSessionState
		}()

		err = tm.RevokeByUser(user.ID)
		if err == nil || isAuthPersistenceWarning(err) {
			t.Fatalf("expected hard persistence error, got %v", err)
		}

		persistTokenSessionState = originalPersistTokenSessionState

		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected access token to remain valid after failed revoke, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected refresh token to remain valid after failed revoke, got %v", err)
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

	t.Run("persisted session removal survives restart", func(t *testing.T) {
		revocationFile := filepath.Join(t.TempDir(), "auth-sessions.json")
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnableSessionPersistence(revocationFile); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
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
		if err := tm.RevokeSession(accessClaims.SessionID, accessClaims.SessionExpiresAt.Time); err != nil {
			t.Fatalf("RevokeSession() error: %v", err)
		}

		restarted := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := restarted.EnableSessionPersistence(revocationFile); err != nil {
			t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
		}

		if _, err := restarted.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject revoked access token, got %v", err)
		}
		if _, err := restarted.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected restarted manager to reject revoked refresh token, got %v", err)
		}
	})

	t.Run("corrupt token revocation file fails closed on startup", func(t *testing.T) {
		storeDir := t.TempDir()
		revocationFile := filepath.Join(storeDir, "auth-sessions.json")
		if err := os.WriteFile(revocationFile, []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile(corrupt revocation file) error: %v", err)
		}

		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnableSessionPersistence(revocationFile); err == nil {
			t.Fatal("expected EnableSessionPersistence() to reject corrupt revocation file")
		}

		entries, err := os.ReadDir(storeDir)
		if err != nil {
			t.Fatalf("ReadDir(storeDir) error: %v", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "auth-sessions.json.corrupt.") {
				t.Fatalf("unexpected corrupt-file backup %q", entry.Name())
			}
		}
		data, err := os.ReadFile(revocationFile)
		if err != nil {
			t.Fatalf("ReadFile(corrupt revocation file) error: %v", err)
		}
		if string(data) != "{" {
			t.Fatalf("corrupt revocation file changed to %q", string(data))
		}
	})

	t.Run("persisted user revocations survive restart", func(t *testing.T) {
		revocationFile := filepath.Join(t.TempDir(), "auth-sessions.json")
		tm := NewTokenManager(secret, 15*time.Minute, 24*time.Hour)
		if err := tm.EnableSessionPersistence(revocationFile); err != nil {
			t.Fatalf("EnableSessionPersistence() error: %v", err)
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
		if err := restarted.EnableSessionPersistence(revocationFile); err != nil {
			t.Fatalf("EnableSessionPersistence() after restart error: %v", err)
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
		tm := NewTokenManager(secret, -1*time.Hour, time.Hour)

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
		if !strings.Contains(err.Error(), "generate session id") {
			t.Fatalf("expected wrapped session id generation error, got %v", err)
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

	t.Run("require auth - access session cookie", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if got := GetAccessTokenFromContext(r.Context()); got != userToken.AccessToken {
				t.Fatalf("access token from context = %q, want access cookie token", got)
			}
			ctxUser := GetUserFromContext(r.Context())
			if ctxUser == nil || ctxUser.Username != "middlewareuser" {
				t.Fatalf("expected middlewareuser in context, got %#v", ctxUser)
			}
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: userToken.AccessToken})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !called {
			t.Error("handler was not called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("require auth - access session cookie rejects conflicting duplicate", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Cookie", AccessSessionCookieName+"=stale; "+AccessSessionCookieName+"="+userToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if called {
			t.Error("handler was called for ambiguous cookies")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d: %s", rec.Code, rec.Body.String())
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

	t.Run("require auth - download session cookie rejects conflicting duplicate", func(t *testing.T) {
		called := false
		handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

		req := httptest.NewRequest("GET", "/api/v1/download/test.txt", nil)
		req.Header.Set("Cookie", DownloadSessionCookieName+"=stale; "+DownloadSessionCookieName+"="+userToken.AccessToken)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if called {
			t.Error("handler was called for ambiguous cookies")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
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

	t.Run("require role - disabled context user is rejected", func(t *testing.T) {
		handler := mw.RequireRole(RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))

		req := httptest.NewRequest("GET", "/admin", nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, &User{
			Username: "disabled-admin",
			Role:     RoleAdmin,
			Disabled: true,
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"USER_DISABLED"`)) {
			t.Fatalf("expected USER_DISABLED payload, got %s", rec.Body.String())
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

	t.Run("optional auth - with access session cookie", func(t *testing.T) {
		called := false
		handler := mw.OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if GetUserFromContext(r.Context()) == nil {
				t.Fatal("expected user in context")
			}
			if got := GetAccessTokenFromContext(r.Context()); got != userToken.AccessToken {
				t.Fatalf("access token from context = %q, want cookie token", got)
			}
		}))

		req := httptest.NewRequest("GET", "/optional", nil)
		req.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: userToken.AccessToken})
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

	t.Run("set exclude paths replaces default bypass list", func(t *testing.T) {
		mwWithExclude := NewMiddleware(store, tm)
		mwWithExclude.SetExcludePaths([]string{"/custom-public"})

		handler := mwWithExclude.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/custom-public", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected custom excluded path to bypass auth, got status %d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/health", nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected replaced default /health path to require auth, got status %d", rec.Code)
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

	t.Run("with claims context preserves nil and stores non-nil claims", func(t *testing.T) {
		ctx := context.Background()
		if WithClaimsContext(ctx, nil) != ctx {
			t.Fatal("expected nil claims to preserve original context")
		}

		claims := &TokenClaims{UserID: user.ID, Username: user.Username, Role: user.Role}
		withClaims := WithClaimsContext(ctx, claims)
		if got := GetClaimsFromContext(withClaims); got != claims {
			t.Fatalf("claims from context = %#v, want original claims", got)
		}
	})

	t.Run("is admin requires enabled admin user", func(t *testing.T) {
		tests := []struct {
			name string
			user *User
			want bool
		}{
			{name: "missing user"},
			{name: "regular user", user: &User{Role: RoleUser}},
			{name: "disabled admin", user: &User{Role: RoleAdmin, Disabled: true}},
			{name: "enabled admin", user: &User{Role: RoleAdmin}, want: true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()
				if tt.user != nil {
					ctx = context.WithValue(ctx, ContextKeyUser, tt.user)
				}
				if got := IsAdmin(ctx); got != tt.want {
					t.Fatalf("IsAdmin() = %v, want %v", got, tt.want)
				}
			})
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
		originalHops := requestip.TrustedProxyHops()
		requestip.SetTrustedProxyHops(1)
		defer requestip.SetTrustedProxyHops(originalHops)

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

		if newRefreshRec.Code != http.StatusUnauthorized {
			t.Fatalf("rotated refresh token status = %d, want 401 after family replay revocation: %s", newRefreshRec.Code, newRefreshRec.Body.String())
		}
		if _, err := tm.ValidateAccessToken(refreshResp.AccessToken); !errors.Is(err, ErrTokenRevoked) {
			t.Fatalf("rotated access token error = %v, want %v", err, ErrTokenRevoked)
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

	t.Run("concurrent refresh consumes a token only once", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
		}

		var loginEnvelope authEnvelope
		if err := json.Unmarshal(loginRec.Body.Bytes(), &loginEnvelope); err != nil {
			t.Fatalf("unmarshal login envelope error: %v", err)
		}
		var loginResp LoginResponse
		if err := json.Unmarshal(loginEnvelope.Data, &loginResp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}

		originalRandomRead := tokenRandomRead
		enteredTokenGeneration := make(chan int, 2)
		releaseTokenGeneration := make(chan struct{})
		var tokenGenerationCalls int32
		var releaseOnce sync.Once
		releaseGeneration := func() {
			releaseOnce.Do(func() {
				close(releaseTokenGeneration)
			})
		}
		tokenRandomRead = func(b []byte) (int, error) {
			call := int(atomic.AddInt32(&tokenGenerationCalls, 1))
			enteredTokenGeneration <- call
			<-releaseTokenGeneration
			for i := range b {
				b[i] = byte(call)
			}
			return len(b), nil
		}
		t.Cleanup(func() {
			tokenRandomRead = originalRandomRead
			releaseGeneration()
		})

		refreshBody, _ := json.Marshal(RefreshRequest{RefreshToken: loginResp.RefreshToken})
		doRefresh := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewBuffer(refreshBody))
			rec := httptest.NewRecorder()
			h.HandleRefresh(rec, req)
			return rec
		}

		firstDone := make(chan *httptest.ResponseRecorder, 1)
		secondDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			firstDone <- doRefresh()
		}()

		select {
		case <-enteredTokenGeneration:
		case <-time.After(2 * time.Second):
			t.Fatal("first refresh did not reach token generation")
		}

		go func() {
			secondDone <- doRefresh()
		}()

		var earlySecond *httptest.ResponseRecorder
		select {
		case <-enteredTokenGeneration:
		case earlySecond = <-secondDone:
		case <-time.After(2 * time.Second):
			t.Fatal("second refresh neither failed nor reached token generation")
		}
		releaseGeneration()

		firstRec := <-firstDone
		secondRec := earlySecond
		if secondRec == nil {
			secondRec = <-secondDone
		}

		successes := 0
		unauthorized := 0
		var published LoginResponse
		for _, rec := range []*httptest.ResponseRecorder{firstRec, secondRec} {
			switch rec.Code {
			case http.StatusOK:
				successes++
				var envelope authEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("unmarshal concurrent refresh envelope: %v", err)
				}
				if err := json.Unmarshal(envelope.Data, &published); err != nil {
					t.Fatalf("unmarshal concurrent refresh payload: %v", err)
				}
			case http.StatusUnauthorized:
				unauthorized++
			default:
				t.Fatalf("unexpected refresh status %d: %s", rec.Code, rec.Body.String())
			}
		}
		if successes != 1 || unauthorized != 1 {
			t.Fatalf("concurrent refresh statuses = [%d, %d], want one 200 and one 401", firstRec.Code, secondRec.Code)
		}
		if _, err := tm.ValidateAccessToken(published.AccessToken); !errors.Is(err, ErrTokenRevoked) {
			t.Fatalf("concurrently published access token error = %v, want %v after replay", err, ErrTokenRevoked)
		}
	})

	t.Run("cookie session login omits bearer tokens and sets HttpOnly cookies", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		req.Header.Set(sessionModeHeader, sessionModeCookie)
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal login envelope error: %v", err)
		}
		var resp LoginResponse
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal login payload error: %v", err)
		}
		if resp.AccessToken != "" || resp.RefreshToken != "" {
			t.Fatalf("cookie-session login leaked bearer tokens: %+v", resp)
		}

		cookies := rec.Result().Cookies()
		if len(cookies) != 2 {
			t.Fatalf("expected access and refresh cookies, got %+v", cookies)
		}
		wantPaths := map[string]string{
			AccessSessionCookieName:  sessionCookiePath,
			RefreshSessionCookieName: refreshSessionCookiePath,
		}
		for _, cookie := range cookies {
			wantPath, ok := wantPaths[cookie.Name]
			if !ok {
				t.Fatalf("unexpected cookie %q", cookie.Name)
			}
			if !cookie.HttpOnly {
				t.Fatalf("cookie %s is not HttpOnly", cookie.Name)
			}
			if cookie.Path != wantPath {
				t.Fatalf("cookie %s Path = %q, want %q", cookie.Name, cookie.Path, wantPath)
			}
			if cookie.Value == "" {
				t.Fatalf("cookie %s has empty value", cookie.Name)
			}
			if cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie %s SameSite = %v, want Lax", cookie.Name, cookie.SameSite)
			}
			delete(wantPaths, cookie.Name)
		}
		if len(wantPaths) != 0 {
			t.Fatalf("missing cookies: %+v", wantPaths)
		}
	})

	t.Run("cookie session login ignores spoofed forwarded proto from untrusted source", func(t *testing.T) {
		originalHops := requestip.TrustedProxyHops()
		requestip.SetTrustedProxyHops(1)
		defer requestip.SetTrustedProxyHops(originalHops)

		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		req.RemoteAddr = "203.0.113.5:1234"
		req.Header.Set(sessionModeHeader, sessionModeCookie)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Secure {
				t.Fatalf("cookie %s unexpectedly used Secure for untrusted forwarded proto", cookie.Name)
			}
		}
	})

	t.Run("cookie session login trusts forwarded proto from loopback proxy", func(t *testing.T) {
		originalHops := requestip.TrustedProxyHops()
		requestip.SetTrustedProxyHops(1)
		defer requestip.SetTrustedProxyHops(originalHops)

		body := `{"username":"handleruser","password":"password123"}`
		req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set(sessionModeHeader, sessionModeCookie)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		h.HandleLogin(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if !cookie.Secure {
				t.Fatalf("cookie %s did not use Secure for trusted forwarded proto", cookie.Name)
			}
		}
	})

	t.Run("cookie session refresh uses refresh cookie and omits bearer tokens", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginReq.Header.Set(sessionModeHeader, sessionModeCookie)
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
		}

		var refreshCookie *http.Cookie
		for _, cookie := range loginRec.Result().Cookies() {
			if cookie.Name == RefreshSessionCookieName {
				refreshCookie = cookie
				break
			}
		}
		if refreshCookie == nil {
			t.Fatal("login did not set refresh cookie")
		}

		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		refreshReq.AddCookie(refreshCookie)
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusOK {
			t.Fatalf("refresh status = %d: %s", refreshRec.Code, refreshRec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(refreshRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		var resp LoginResponse
		if err := json.Unmarshal(envelope.Data, &resp); err != nil {
			t.Fatalf("unmarshal refresh payload error: %v", err)
		}
		if resp.AccessToken != "" || resp.RefreshToken != "" {
			t.Fatalf("cookie-session refresh leaked bearer tokens: %+v", resp)
		}

		setCookies := map[string]*http.Cookie{}
		for _, cookie := range refreshRec.Result().Cookies() {
			setCookies[cookie.Name] = cookie
		}
		for _, name := range []string{AccessSessionCookieName, RefreshSessionCookieName} {
			cookie := setCookies[name]
			if cookie == nil {
				t.Fatalf("refresh did not set cookie %s", name)
			}
			if !cookie.HttpOnly {
				t.Fatalf("cookie %s is not HttpOnly", name)
			}
			if cookie.Value == "" {
				t.Fatalf("cookie %s has empty value", name)
			}
		}
		if setCookies[RefreshSessionCookieName].Value == refreshCookie.Value {
			t.Fatal("expected refresh cookie to rotate")
		}
	})

	t.Run("cookie session refresh replay clears the revoked session cookies", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginReq.Header.Set(sessionModeHeader, sessionModeCookie)
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
		}

		var refreshCookie *http.Cookie
		for _, cookie := range loginRec.Result().Cookies() {
			if cookie.Name == RefreshSessionCookieName {
				refreshCookie = cookie
				break
			}
		}
		if refreshCookie == nil {
			t.Fatal("login did not set refresh cookie")
		}

		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		refreshReq.AddCookie(refreshCookie)
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)
		if refreshRec.Code != http.StatusOK {
			t.Fatalf("refresh status = %d: %s", refreshRec.Code, refreshRec.Body.String())
		}

		replayReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		replayReq.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: "stale-access"})
		replayReq.AddCookie(refreshCookie)
		replayReq.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: "stale-download"})
		replayRec := httptest.NewRecorder()
		h.HandleRefresh(replayRec, replayReq)

		if replayRec.Code != http.StatusUnauthorized {
			t.Fatalf("replayed refresh cookie status = %d, want 401: %s", replayRec.Code, replayRec.Body.String())
		}
		assertSessionCookiesCleared(t, replayRec.Result().Cookies())
	})

	t.Run("cookie session refresh after user revocation clears stale cookies", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginReq.Header.Set(sessionModeHeader, sessionModeCookie)
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
		}

		var refreshCookie *http.Cookie
		for _, cookie := range loginRec.Result().Cookies() {
			if cookie.Name == RefreshSessionCookieName {
				refreshCookie = cookie
				break
			}
		}
		if refreshCookie == nil {
			t.Fatal("login did not set refresh cookie")
		}

		user, err := store.GetByUsername("handleruser")
		if err != nil {
			t.Fatalf("GetByUsername(handleruser) error: %v", err)
		}
		if err := tm.RevokeByUser(user.ID); err != nil {
			t.Fatalf("RevokeByUser(handleruser) error: %v", err)
		}

		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		refreshReq.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: "stale-access"})
		refreshReq.AddCookie(refreshCookie)
		refreshReq.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: "stale-download"})
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusUnauthorized {
			t.Fatalf("revoked user refresh status = %d, want 401: %s", refreshRec.Code, refreshRec.Body.String())
		}
		wantPaths := map[string]string{
			AccessSessionCookieName:   sessionCookiePath,
			RefreshSessionCookieName:  refreshSessionCookiePath,
			DownloadSessionCookieName: downloadSessionCookiePath,
		}
		for _, cookie := range refreshRec.Result().Cookies() {
			wantPath, ok := wantPaths[cookie.Name]
			if !ok {
				t.Fatalf("unexpected clearing cookie %q", cookie.Name)
			}
			if cookie.Path != wantPath {
				t.Fatalf("cookie %s Path = %q, want %q", cookie.Name, cookie.Path, wantPath)
			}
			if cookie.MaxAge != -1 {
				t.Fatalf("cookie %s MaxAge = %d, want -1", cookie.Name, cookie.MaxAge)
			}
			delete(wantPaths, cookie.Name)
		}
		if len(wantPaths) != 0 {
			t.Fatalf("missing clearing cookies: %+v", wantPaths)
		}
	})

	t.Run("cookie session refresh rejects conflicting duplicate refresh cookie", func(t *testing.T) {
		body := `{"username":"handleruser","password":"password123"}`
		loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBufferString(body))
		loginReq.Header.Set(sessionModeHeader, sessionModeCookie)
		loginRec := httptest.NewRecorder()
		h.HandleLogin(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
		}

		var refreshCookie *http.Cookie
		for _, cookie := range loginRec.Result().Cookies() {
			if cookie.Name == RefreshSessionCookieName {
				refreshCookie = cookie
				break
			}
		}
		if refreshCookie == nil {
			t.Fatal("login did not set refresh cookie")
		}

		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		refreshReq.Header.Set("Cookie", RefreshSessionCookieName+"=stale; "+RefreshSessionCookieName+"="+refreshCookie.Value)
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusUnauthorized {
			t.Fatalf("refresh status = %d, want 401: %s", refreshRec.Code, refreshRec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(refreshRec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal refresh envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_TOKEN" {
			t.Fatalf("expected INVALID_TOKEN response, got %s", refreshRec.Body.String())
		}
	})

	t.Run("refresh without token clears stale session cookies", func(t *testing.T) {
		refreshReq := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
		refreshReq.AddCookie(&http.Cookie{Name: AccessSessionCookieName, Value: "stale-access"})
		refreshReq.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: "stale-download"})
		refreshRec := httptest.NewRecorder()
		h.HandleRefresh(refreshRec, refreshReq)

		if refreshRec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", refreshRec.Code, refreshRec.Body.String())
		}

		wantPaths := map[string]string{
			AccessSessionCookieName:   sessionCookiePath,
			RefreshSessionCookieName:  refreshSessionCookiePath,
			DownloadSessionCookieName: "/api/v1",
		}
		for _, cookie := range refreshRec.Result().Cookies() {
			wantPath, ok := wantPaths[cookie.Name]
			if !ok {
				t.Fatalf("unexpected clearing cookie %q", cookie.Name)
			}
			if cookie.MaxAge != -1 {
				t.Fatalf("cookie %s MaxAge = %d, want -1", cookie.Name, cookie.MaxAge)
			}
			if cookie.Path != wantPath {
				t.Fatalf("cookie %s Path = %q, want %q", cookie.Name, cookie.Path, wantPath)
			}
			delete(wantPaths, cookie.Name)
		}
		if len(wantPaths) != 0 {
			t.Fatalf("missing clearing cookies: %+v", wantPaths)
		}
	})

	t.Run("refresh token returns warning when revocation persistence fsync fails", func(t *testing.T) {
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

		originalPersistTokenSessionState := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return wrapAuthPersistenceWarning(errors.New("directory fsync failed"))
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenSessionState
		}()

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

	t.Run("me endpoint rejects disabled user context", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, &User{
			ID:       "disabled-user",
			Username: "disabled-user",
			Role:     RoleUser,
			Disabled: true,
		}))
		rec := httptest.NewRecorder()
		h.HandleMe(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal me disabled envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "USER_DISABLED" {
			t.Fatalf("expected USER_DISABLED error, got %+v", envelope.Error)
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
		ctx = context.WithValue(ctx, ContextKeyAccessToken, pair.AccessToken)
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
		if cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatalf("expected download cookie SameSite=Strict, got %v", cookies[0].SameSite)
		}
	})

	t.Run("download session rejects disabled user context", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		ctx := context.WithValue(req.Context(), ContextKeyUser, &User{
			ID:       "disabled-user",
			Username: "disabled-user",
			Role:     RoleUser,
			Disabled: true,
		})
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           "disabled-user",
			Username:         "disabled-user",
			Role:             RoleUser,
		})
		ctx = context.WithValue(ctx, ContextKeyAccessToken, "disabled-access-token")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
		}
		if cookies := rec.Result().Cookies(); len(cookies) != 0 {
			t.Fatalf("expected no download session cookie, got %d", len(cookies))
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal disabled download session envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "USER_DISABLED" {
			t.Fatalf("expected USER_DISABLED error, got %+v", envelope.Error)
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
		ctx = context.WithValue(ctx, ContextKeyAccessToken, pair.AccessToken)
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

	t.Run("download session cookie ignores forwarded proto when trusted proxy hops disabled", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)

		originalHops := requestip.TrustedProxyHops()
		requestip.SetTrustedProxyHops(0)
		defer requestip.SetTrustedProxyHops(originalHops)

		req := httptest.NewRequest("POST", "/api/v1/auth/download-session", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		req.Header.Set("X-Forwarded-Proto", "https")
		ctx := context.WithValue(req.Context(), ContextKeyUser, user)
		ctx = context.WithValue(ctx, ContextKeyClaims, &TokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
			UserID:           user.ID,
			Username:         user.Username,
			Role:             user.Role,
		})
		ctx = context.WithValue(ctx, ContextKeyAccessToken, pair.AccessToken)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		h.HandleCreateDownloadSession(rec, req)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		if cookies[0].Secure {
			t.Fatal("expected trusted proxy hops disabled to leave cookie insecure")
		}
	})

	t.Run("logout revokes current token and clears download session cookie", func(t *testing.T) {
		user, _ := store.GetByUsername("handleruser")
		pair, _ := tm.GenerateTokenPair(user)
		claims, err := tm.ValidateAccessToken(pair.AccessToken)
		if err != nil {
			t.Fatalf("ValidateAccessToken(access) error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: pair.AccessToken})
		req = req.WithContext(WithClaimsContext(req.Context(), claims))
		rec := httptest.NewRecorder()

		h.HandleLogout(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if _, err := tm.ValidateAccessToken(pair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after logout, got %v", err)
		}

		cookies := rec.Result().Cookies()
		if len(cookies) != 3 {
			t.Fatalf("expected three cookie clearing headers, got %d", len(cookies))
		}
		wantPaths := map[string]string{
			AccessSessionCookieName:   sessionCookiePath,
			RefreshSessionCookieName:  refreshSessionCookiePath,
			DownloadSessionCookieName: "/api/v1",
		}
		for _, cookie := range cookies {
			wantPath, ok := wantPaths[cookie.Name]
			if !ok {
				t.Fatalf("unexpected clearing cookie %q", cookie.Name)
			}
			if cookie.MaxAge != -1 {
				t.Fatalf("cookie %s MaxAge = %d, want -1", cookie.Name, cookie.MaxAge)
			}
			if cookie.Path != wantPath {
				t.Fatalf("cookie %s Path = %q, want %q", cookie.Name, cookie.Path, wantPath)
			}
			delete(wantPaths, cookie.Name)
		}
		if len(wantPaths) != 0 {
			t.Fatalf("missing clearing cookies: %+v", wantPaths)
		}
	})

	t.Run("logout without claims still clears download session cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: DownloadSessionCookieName, Value: "stale"})
		rec := httptest.NewRecorder()

		h.HandleLogout(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 3 {
			t.Fatalf("expected all session cookies to be cleared, got %+v", cookies)
		}
		cleared := map[string]bool{}
		for _, cookie := range cookies {
			if cookie.MaxAge != -1 {
				t.Fatalf("cookie %s MaxAge = %d, want -1", cookie.Name, cookie.MaxAge)
			}
			cleared[cookie.Name] = true
		}
		for _, name := range []string{AccessSessionCookieName, RefreshSessionCookieName, DownloadSessionCookieName} {
			if !cleared[name] {
				t.Fatalf("expected cookie %s to be cleared, got %+v", name, cookies)
			}
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

	t.Run("admin list users persists quota trend history when values change", func(t *testing.T) {
		quotaDir := t.TempDir()
		quotaStore, _, err := NewUserStore(filepath.Join(quotaDir, "users.json"))
		if err != nil {
			t.Fatalf("NewUserStore(quota) error: %v", err)
		}
		quotaBytes := int64(100)
		quotaUser, err := quotaStore.CreateWithOptions("quotauser", "password123", "quota@test.com", RoleUser, CreateUserOptions{
			QuotaBytes: quotaBytes,
		})
		if err != nil {
			t.Fatalf("CreateWithOptions(quotauser) error: %v", err)
		}
		quotaHandler := NewHandler(quotaStore, NewTokenManager("quota-history-secret", 15*time.Minute, 24*time.Hour))
		usedBytes := int64(50)
		quotaHandler.SetUserUsageResolver(func(_ context.Context, user *User) (int64, error) {
			if user.ID == quotaUser.ID {
				return usedBytes, nil
			}
			return user.UsedBytes, nil
		})
		admin, err := quotaStore.GetByUsername("admin")
		if err != nil {
			t.Fatalf("GetByUsername(admin) error: %v", err)
		}

		listUsers := func(t *testing.T) struct {
			Users                 []map[string]interface{} `json:"users"`
			Total                 int                      `json:"total"`
			QuotaHistory          []UserQuotaTrendPoint    `json:"quota_history"`
			QuotaHistoryAvailable bool                     `json:"quota_history_available"`
		} {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
			rec := httptest.NewRecorder()
			quotaHandler.HandleListUsers(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("HandleListUsers status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var envelope authEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("unmarshal quota history envelope error: %v", err)
			}
			var payload struct {
				Users                 []map[string]interface{} `json:"users"`
				Total                 int                      `json:"total"`
				QuotaHistory          []UserQuotaTrendPoint    `json:"quota_history"`
				QuotaHistoryAvailable bool                     `json:"quota_history_available"`
			}
			if err := json.Unmarshal(envelope.Data, &payload); err != nil {
				t.Fatalf("unmarshal quota history payload error: %v; data=%s", err, string(envelope.Data))
			}
			return payload
		}

		first := listUsers(t)
		if !first.QuotaHistoryAvailable {
			t.Fatal("expected quota history to be available")
		}
		if len(first.Users) != 2 || first.Total != 2 {
			t.Fatalf("expected two users in quota history fixture, got total=%d users=%d", first.Total, len(first.Users))
		}
		if len(first.QuotaHistory) != 1 {
			t.Fatalf("first quota history length = %d, want 1", len(first.QuotaHistory))
		}
		if point := first.QuotaHistory[0]; point.TotalCount != 2 || point.ActiveCount != 2 || point.LimitedCount != 1 || point.LimitedUsedBytes != usedBytes || point.QuotaBytes != quotaBytes {
			t.Fatalf("unexpected first quota history point: %+v", point)
		}

		second := listUsers(t)
		if len(second.QuotaHistory) != 1 {
			t.Fatalf("unchanged quota history length = %d, want 1", len(second.QuotaHistory))
		}

		usedBytes = 95
		third := listUsers(t)
		if len(third.QuotaHistory) != 2 {
			t.Fatalf("changed quota history length = %d, want 2", len(third.QuotaHistory))
		}
		latest := third.QuotaHistory[0]
		if latest.LimitedUsedBytes != usedBytes || latest.WarningCount != 1 || latest.ExceededCount != 0 || latest.AttentionCount != 1 {
			t.Fatalf("unexpected changed quota history point: %+v", latest)
		}
		if third.QuotaHistory[1].LimitedUsedBytes != 50 {
			t.Fatalf("expected previous quota history point to retain 50 used bytes, got %+v", third.QuotaHistory[1])
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
		var data map[string]map[string]interface{}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatalf("unmarshal create user data error: %v", err)
		}
		userData := data["user"]
		for _, field := range []string{"id", "username", "email", "role", "disabled", "home_dir", "created_at", "updated_at", "quota_bytes", "used_bytes"} {
			if _, ok := userData[field]; !ok {
				t.Fatalf("expected create user response to include %q, got %+v", field, userData)
			}
		}
	})

	t.Run("admin create user with groups", func(t *testing.T) {
		body := `{"username":"newgroupuser","password":"newpass123","email":"groups@test.com","role":"user","groups":["Family","editors","family"]}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
		}
		created, err := store.GetByUsername("newgroupuser")
		if err != nil {
			t.Fatalf("GetByUsername(newgroupuser) error: %v", err)
		}
		if got, want := strings.Join(created.Groups, ","), "editors,family"; got != want {
			t.Fatalf("created groups = %q, want %q", got, want)
		}
	})

	t.Run("admin create user with home directory and quota", func(t *testing.T) {
		body := `{"username":"homequotauser","password":"newpass123","email":"quota@test.com","role":"user","home_dir":" /team/homequota ","quota_bytes":1048576}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
		}
		created, err := store.GetByUsername("homequotauser")
		if err != nil {
			t.Fatalf("GetByUsername(homequotauser) error: %v", err)
		}
		if created.HomeDir != "/team/homequota" {
			t.Fatalf("created home_dir = %q, want /team/homequota", created.HomeDir)
		}
		if created.QuotaBytes != 1048576 {
			t.Fatalf("created quota_bytes = %d, want 1048576", created.QuotaBytes)
		}
	})

	t.Run("admin create user rejects invalid groups", func(t *testing.T) {
		body := `{"username":"invalidgroups","password":"newpass123","role":"user","groups":["family/team"]}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_GROUPS" {
			t.Fatalf("expected INVALID_GROUPS error, got %+v", envelope.Error)
		}
	})

	t.Run("admin create user rejects invalid home directory", func(t *testing.T) {
		for idx, homeDir := range []string{"../team/home", "/team/./home"} {
			body := fmt.Sprintf(`{"username":"invalidhomedir%d","password":"newpass123","role":"user","home_dir":%q}`, idx, homeDir)
			req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
			admin, _ := store.GetByUsername("handleradmin")
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
			rec := httptest.NewRecorder()
			h.HandleCreateUser(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("home_dir %q: expected status 400, got %d: %s", homeDir, rec.Code, rec.Body.String())
			}
			var envelope authEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("home_dir %q: unmarshal create error envelope: %v", homeDir, err)
			}
			if envelope.Error == nil || envelope.Error.Code != "INVALID_HOME_DIR" {
				t.Fatalf("home_dir %q: expected INVALID_HOME_DIR error, got %+v", homeDir, envelope.Error)
			}
		}
	})

	t.Run("admin create user rejects root home directory for non-admin", func(t *testing.T) {
		body := `{"username":"rootnonadmin","password":"newpass123","role":"user","home_dir":"/"}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_HOME_DIR" {
			t.Fatalf("expected INVALID_HOME_DIR error, got %+v", envelope.Error)
		}
		if _, err := store.GetByUsername("rootnonadmin"); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected rootnonadmin to remain unpersisted, got %v", err)
		}
	})

	t.Run("admin create user rejects negative quota", func(t *testing.T) {
		body := `{"username":"invalidquota","password":"newpass123","role":"user","quota_bytes":-1}`
		req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleCreateUser(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal create error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_QUOTA" {
			t.Fatalf("expected INVALID_QUOTA error, got %+v", envelope.Error)
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

	t.Run("admin update user metadata and quota", func(t *testing.T) {
		user, err := store.Create("editableuser", "password123", "old@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create editable user: %v", err)
		}

		body := `{"email":"new@test.com","role":"guest","groups":["Family","editors"],"home_dir":"/guests/editableuser","quota_bytes":1048576}`
		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+user.ID, bytes.NewBufferString(body))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleUpdateUser(rec, req, user.ID)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		updated, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("GetByID(updated) error: %v", err)
		}
		if updated.Email != "new@test.com" || updated.Role != RoleGuest || updated.HomeDir != "/guests/editableuser" || updated.QuotaBytes != 1048576 {
			t.Fatalf("unexpected updated user: %+v", updated)
		}
		if got, want := strings.Join(updated.Groups, ","), "editors,family"; got != want {
			t.Fatalf("updated groups = %q, want %q", got, want)
		}
	})

	t.Run("admin update user rejects control character home dir", func(t *testing.T) {
		user, err := store.Create("controlhome", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create control home user: %v", err)
		}

		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+user.ID, bytes.NewBufferString(`{"home_dir":"/users/control\u0007home"}`))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleUpdateUser(rec, req, user.ID)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal update error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_HOME_DIR" {
			t.Fatalf("expected INVALID_HOME_DIR error, got %+v", envelope.Error)
		}
		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("GetByID(controlhome) error: %v", err)
		}
		if fresh.HomeDir != "/controlhome" {
			t.Fatalf("expected rejected update to preserve home_dir /controlhome, got %q", fresh.HomeDir)
		}
	})

	t.Run("admin update user rejects root home directory for non-admin", func(t *testing.T) {
		user, err := store.Create("rootedithome", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create root edit user: %v", err)
		}

		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+user.ID, bytes.NewBufferString(`{"home_dir":"/"}`))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleUpdateUser(rec, req, user.ID)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal update error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_HOME_DIR" {
			t.Fatalf("expected INVALID_HOME_DIR error, got %+v", envelope.Error)
		}
		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("GetByID(rootedithome) error: %v", err)
		}
		if fresh.HomeDir != "/rootedithome" {
			t.Fatalf("expected rejected update to preserve home_dir /rootedithome, got %q", fresh.HomeDir)
		}
	})

	t.Run("admin update user rejects invalid groups", func(t *testing.T) {
		user, err := store.Create("badupdategroups", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create bad groups user: %v", err)
		}

		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+user.ID, bytes.NewBufferString(`{"groups":["bad/group"]}`))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleUpdateUser(rec, req, user.ID)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal update error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_GROUPS" {
			t.Fatalf("expected INVALID_GROUPS error, got %+v", envelope.Error)
		}
	})

	t.Run("admin update user rejects negative quota", func(t *testing.T) {
		user, err := store.Create("negativequota", "password123", "", RoleUser)
		if err != nil {
			t.Fatalf("failed to create quota user: %v", err)
		}

		req := httptest.NewRequest("PUT", "/api/v1/admin/users/"+user.ID, bytes.NewBufferString(`{"quota_bytes":-1}`))
		admin, _ := store.GetByUsername("handleradmin")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleUpdateUser(rec, req, user.ID)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal update error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_QUOTA" {
			t.Fatalf("expected INVALID_QUOTA error, got %+v", envelope.Error)
		}
	})

	t.Run("admin create user rejects password above bcrypt limit", func(t *testing.T) {
		body := fmt.Sprintf(`{"username":"toolongpass","password":%q,"email":"new@test.com","role":"user"}`, strings.Repeat("a", 73))
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
		if envelope.Error == nil || envelope.Error.Code != "PASSWORD_TOO_LONG" {
			t.Fatalf("expected PASSWORD_TOO_LONG error, got %+v", envelope.Error)
		}
		if _, err := store.GetByUsername("toolongpass"); err == nil {
			t.Fatal("expected long password create to be rejected before persistence")
		}
	})

	t.Run("change password rejects disabled user context without mutating password", func(t *testing.T) {
		disabledUser, err := store.Create("disabled-change-handler", "password123", "disabled-change-handler@test.com", RoleUser)
		if err != nil {
			t.Fatalf("create disabled change user error: %v", err)
		}
		disabledUser.Disabled = true
		if err := store.Update(disabledUser); err != nil {
			t.Fatalf("disable change user error: %v", err)
		}

		body := fmt.Sprintf(`{"expected_user_id":%q,"old_password":"password123","new_password":"newpassword456"}`, disabledUser.ID)
		req := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(body))
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, disabledUser))
		rec := httptest.NewRecorder()

		h.HandleChangePassword(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
		}
		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal disabled change password envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "USER_DISABLED" {
			t.Fatalf("expected USER_DISABLED error, got %+v", envelope.Error)
		}

		refreshed, err := store.GetByID(disabledUser.ID)
		if err != nil {
			t.Fatalf("reload disabled change user error: %v", err)
		}
		refreshed.Disabled = false
		if err := store.Update(refreshed); err != nil {
			t.Fatalf("reenable change user error: %v", err)
		}
		if _, err := store.Authenticate("disabled-change-handler", "password123"); err != nil {
			t.Fatalf("expected old password to remain valid, got %v", err)
		}
		if _, err := store.Authenticate("disabled-change-handler", "newpassword456"); err != ErrInvalidCredentials {
			t.Fatalf("expected new password to be rejected, got %v", err)
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

		changeBody := fmt.Sprintf(`{"expected_user_id":%q,"old_password":"password123","new_password":"newpassword456"}`, changeUser.ID)
		changeReq := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(changeBody))
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
		userData, ok := createData["user"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected user payload for create warning, got %+v", createData)
		}
		for _, field := range []string{"id", "username", "email", "role", "disabled", "home_dir", "created_at", "updated_at", "quota_bytes", "used_bytes"} {
			if _, ok := userData[field]; !ok {
				t.Fatalf("expected create warning user payload to include %q, got %+v", field, userData)
			}
		}
		if _, err := warningStore.GetByUsername("warningcreate"); err != nil {
			t.Fatalf("expected warned create to commit user, got %v", err)
		}

		changeBody := fmt.Sprintf(`{"expected_user_id":%q,"old_password":"password123","new_password":"newpassword456"}`, changeUser.ID)
		changeReq := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(changeBody))
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

	t.Run("change password relies on durable credential version when revocation persistence fails hard", func(t *testing.T) {
		changeUser, err := store.Create("hard-revoke-change", "password123", "hard-revoke-change@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create change user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(changeUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenRevocations
		}()

		body := fmt.Sprintf(`{"expected_user_id":%q,"old_password":"password123","new_password":"newpassword456"}`, changeUser.ID)
		req := httptest.NewRequest("POST", "/api/v1/auth/password", bytes.NewBufferString(body))
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
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the token structurally valid, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the refresh token structurally valid, got %v", err)
		}
		assertAccessRejectedByMiddleware(t, store, tm, tokenPair.AccessToken, http.StatusUnauthorized)
		assertRefreshRejectedByHandler(t, h, tokenPair.RefreshToken, http.StatusUnauthorized)
	})

	t.Run("reset password relies on durable credential version when revocation persistence fails hard", func(t *testing.T) {
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

		originalPersistTokenRevocations := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenRevocations
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
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the token structurally valid, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the refresh token structurally valid, got %v", err)
		}
		assertAccessRejectedByMiddleware(t, store, tm, tokenPair.AccessToken, http.StatusUnauthorized)
		assertRefreshRejectedByHandler(t, h, tokenPair.RefreshToken, http.StatusUnauthorized)
	})

	t.Run("delete user relies on durable account removal when revocation persistence fails hard", func(t *testing.T) {
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

		originalPersistTokenRevocations := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenRevocations
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
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the token structurally valid, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the refresh token structurally valid, got %v", err)
		}
		assertAccessRejectedByMiddleware(t, store, tm, tokenPair.AccessToken, http.StatusUnauthorized)
		assertRefreshRejectedByHandler(t, h, tokenPair.RefreshToken, http.StatusUnauthorized)
	})

	t.Run("disable user relies on durable account status when revocation persistence fails hard", func(t *testing.T) {
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

		originalPersistTokenRevocations := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenRevocations
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
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the token structurally valid, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected revocation-store rollback to leave the refresh token structurally valid, got %v", err)
		}
		assertAccessRejectedByMiddleware(t, store, tm, tokenPair.AccessToken, http.StatusForbidden)
		assertRefreshRejectedByHandler(t, h, tokenPair.RefreshToken, http.StatusForbidden)
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

	t.Run("admin revoke user sessions revokes outstanding tokens", func(t *testing.T) {
		admin, _ := store.GetByUsername("handleradmin")
		revokeUser, err := store.Create("session-revoke", "password123", "session-revoke@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create revoke user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(revokeUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/admin/users/"+revokeUser.ID+"/revoke-sessions", nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleRevokeUserSessions(rec, req, revokeUser.ID)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected revoke sessions status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal revoke sessions envelope error: %v", err)
		}
		if !envelope.Success || envelope.Message != "user sessions revoked successfully" {
			t.Fatalf("unexpected revoke sessions envelope: %+v", envelope)
		}
		var data map[string]interface{}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatalf("unmarshal revoke sessions payload error: %v", err)
		}
		if revoked, ok := data["revoked"].(bool); !ok || !revoked {
			t.Fatalf("expected revoked=true payload, got %+v", data)
		}

		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after session revoke, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after session revoke, got %v", err)
		}
	})

	t.Run("admin revoke user sessions rejects missing user", func(t *testing.T) {
		admin, _ := store.GetByUsername("handleradmin")
		req := httptest.NewRequest("POST", "/api/v1/admin/users/missing-user/revoke-sessions", nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleRevokeUserSessions(rec, req, "missing-user")

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected revoke sessions status 404, got %d: %s", rec.Code, rec.Body.String())
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal revoke missing envelope error: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "USER_NOT_FOUND" {
			t.Fatalf("expected USER_NOT_FOUND error, got %+v", envelope.Error)
		}
	})

	t.Run("admin revoke user sessions fails transactionally when persistence fails hard", func(t *testing.T) {
		admin, err := store.GetByUsername("handleradmin")
		if err != nil {
			t.Fatalf("failed to get admin user: %v", err)
		}
		revokeUser, err := store.Create("hard-revoke-sessions", "password123", "hard-revoke-sessions@test.com", RoleUser)
		if err != nil {
			t.Fatalf("failed to create revoke user: %v", err)
		}

		tokenPair, err := tm.GenerateTokenPair(revokeUser)
		if err != nil {
			t.Fatalf("GenerateTokenPair() error: %v", err)
		}

		originalPersistTokenRevocations := persistTokenSessionState
		persistTokenSessionState = func(tm *TokenManager) error {
			return errors.New("write failed")
		}
		defer func() {
			persistTokenSessionState = originalPersistTokenRevocations
		}()

		req := httptest.NewRequest("POST", "/api/v1/admin/users/"+revokeUser.ID+"/revoke-sessions", nil)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyUser, admin))
		rec := httptest.NewRecorder()
		h.HandleRevokeUserSessions(rec, req, revokeUser.ID)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected revoke sessions status 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Warning"); got != "" {
			t.Fatalf("unexpected warning header %q on hard failure", got)
		}

		var envelope authEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal revoke sessions error envelope: %v", err)
		}
		if envelope.Success || envelope.Error == nil || envelope.Error.Code != "REVOKE_SESSIONS_ERROR" {
			t.Fatalf("unexpected revoke sessions error envelope: %+v", envelope)
		}
		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != nil {
			t.Fatalf("expected failed revoke to leave access token valid, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != nil {
			t.Fatalf("expected failed revoke to leave refresh token valid, got %v", err)
		}

		persistTokenSessionState = originalPersistTokenRevocations
		retryReq := httptest.NewRequest("POST", "/api/v1/admin/users/"+revokeUser.ID+"/revoke-sessions", nil)
		retryReq = retryReq.WithContext(context.WithValue(retryReq.Context(), ContextKeyUser, admin))
		retryRec := httptest.NewRecorder()
		h.HandleRevokeUserSessions(retryRec, retryReq, revokeUser.ID)
		if retryRec.Code != http.StatusOK {
			t.Fatalf("retry revoke sessions status = %d, want 200: %s", retryRec.Code, retryRec.Body.String())
		}

		if _, err := tm.ValidateAccessToken(tokenPair.AccessToken); err != ErrTokenRevoked {
			t.Fatalf("expected access token to be revoked after retry, got %v", err)
		}
		if _, err := tm.ValidateRefreshToken(tokenPair.RefreshToken); err != ErrTokenRevoked {
			t.Fatalf("expected refresh token to be revoked after retry, got %v", err)
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

func TestHandleChangePasswordRequiresExpectedUserID(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("scope-required", "shared-password-123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(scope-required) error: %v", err)
	}
	before, err := store.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-required) before request error: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("scope-required-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password",
		strings.NewReader(`{"old_password":"shared-password-123","new_password":"replacement-password-456"}`),
	)
	request = request.WithContext(context.WithValue(request.Context(), ContextKeyUser, user))
	response := httptest.NewRecorder()

	handler.HandleChangePassword(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing expected user id status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var envelope ResponseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal missing expected user id response error: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != "MISSING_EXPECTED_USER_ID" {
		t.Fatalf("missing expected user id error = %+v, want MISSING_EXPECTED_USER_ID", envelope.Error)
	}
	after, err := store.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-required) after request error: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("missing expected user id mutated account: got %+v, want %+v", after, before)
	}
}

func TestHandleChangePasswordRejectsStaleExpectedUserAfterSessionSwitch(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	staleUser, err := store.Create("scope-stale", "shared-password-123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(scope-stale) error: %v", err)
	}
	currentUser, err := store.Create("scope-current", "shared-password-123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(scope-current) error: %v", err)
	}
	staleBefore, err := store.GetByID(staleUser.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-stale) before request error: %v", err)
	}
	currentBefore, err := store.GetByID(currentUser.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-current) before request error: %v", err)
	}
	tokenManager := NewTokenManager("scope-switch-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour)
	tokenPair, err := tokenManager.GenerateTokenPair(currentUser)
	if err != nil {
		t.Fatalf("GenerateTokenPair(scope-current) error: %v", err)
	}
	handler := NewHandler(store, tokenManager)
	body := fmt.Sprintf(
		`{"expected_user_id":%q,"old_password":"shared-password-123","new_password":"replacement-password-456"}`,
		staleUser.ID,
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), ContextKeyUser, currentUser))
	response := httptest.NewRecorder()

	handler.HandleChangePassword(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("stale expected user status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var envelope ResponseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal stale expected user response error: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != "AUTH_SCOPE_CHANGED" {
		t.Fatalf("stale expected user error = %+v, want AUTH_SCOPE_CHANGED", envelope.Error)
	}
	if got := response.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("stale expected user response changed cookies: %v", got)
	}
	staleAfter, err := store.GetByID(staleUser.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-stale) after request error: %v", err)
	}
	currentAfter, err := store.GetByID(currentUser.ID)
	if err != nil {
		t.Fatalf("GetByID(scope-current) after request error: %v", err)
	}
	if !reflect.DeepEqual(staleAfter, staleBefore) {
		t.Fatalf("stale expected user request mutated stale account: got %+v, want %+v", staleAfter, staleBefore)
	}
	if !reflect.DeepEqual(currentAfter, currentBefore) {
		t.Fatalf("stale expected user request mutated current account: got %+v, want %+v", currentAfter, currentBefore)
	}
	if _, err := tokenManager.ValidateAccessToken(tokenPair.AccessToken); err != nil {
		t.Fatalf("stale expected user request revoked current session: %v", err)
	}
}

func TestHandleChangePasswordAcceptsMatchingExpectedUserID(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("scope-match", "shared-password-123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(scope-match) error: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("scope-match-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour))
	body := fmt.Sprintf(
		`{"expected_user_id":%q,"old_password":"shared-password-123","new_password":"replacement-password-456"}`,
		user.ID,
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), ContextKeyUser, user))
	response := httptest.NewRecorder()

	handler.HandleChangePassword(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("matching expected user status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if _, err := store.VerifyCredentials(user.Username, "replacement-password-456"); err != nil {
		t.Fatalf("matching expected user did not commit new password: %v", err)
	}
	if _, err := store.VerifyCredentials(user.Username, "shared-password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("matching expected user kept old password valid: %v", err)
	}
}

func TestWriteSuccess_InvalidPayloadReturnsInternalServerError(t *testing.T) {
	rec := httptest.NewRecorder()

	writeSuccess(rec, http.StatusOK, map[string]interface{}{
		"bad": make(chan int),
	}, "ok")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var envelope struct {
		Success bool         `json:"success"`
		Error   *ErrorDetail `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal fallback envelope error: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Success || envelope.Error == nil {
		t.Fatalf("expected error envelope, got %s", rec.Body.String())
	}
	if envelope.Error.Code != "INTERNAL_ERROR" || envelope.Error.Message != "internal server error" {
		t.Fatalf("unexpected fallback error: %+v", envelope.Error)
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
	staleKey := loginAttemptKey{clientIP: "203.0.113.1"}
	freshKey := loginAttemptKey{clientIP: "203.0.113.2"}
	tracker.attempts[staleKey] = loginAttemptState{failures: 1, lastFailure: now.Add(-2 * time.Minute)}
	tracker.entriesByIP[staleKey.clientIP] = 1

	tracker.recordFailure(freshKey, 5, time.Minute, time.Minute)

	if _, ok := tracker.attempts[staleKey]; ok {
		t.Fatal("expected stale tracker entry to be pruned")
	}
	if _, ok := tracker.attempts[freshKey]; !ok {
		t.Fatal("expected fresh tracker entry to remain")
	}
	if _, ok := tracker.entriesByIP[staleKey.clientIP]; ok {
		t.Fatal("expected stale tracker per-IP count to be pruned")
	}
}

func TestAuthHandler_LoginRejectsHugeUsernamesWithoutRetainingThem(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("create user store: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("huge-username-secret", 15*time.Minute, 24*time.Hour))
	username := strings.Repeat("a", defaultJSONRequestBodyLimit-128)
	for attempt := 0; attempt < 3; attempt++ {
		attemptUsername := username[:len(username)-1] + string(rune('a'+attempt))
		requestBody, err := json.Marshal(LoginRequest{Username: attemptUsername, Password: "password123"})
		if err != nil {
			t.Fatalf("attempt %d marshal login request: %v", attempt, err)
		}
		if len(requestBody) >= defaultJSONRequestBodyLimit {
			t.Fatalf("attempt %d request size = %d, want below %d", attempt, len(requestBody), defaultJSONRequestBodyLimit)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(requestBody))
		req.RemoteAddr = "203.0.113.10:1234"
		rec := httptest.NewRecorder()
		handler.HandleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d: %s", attempt, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
		var envelope struct {
			Error *ErrorDetail `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("attempt %d unmarshal response: %v", attempt, err)
		}
		if envelope.Error == nil || envelope.Error.Code != "INVALID_CREDENTIALS" {
			t.Fatalf("attempt %d error = %+v, want INVALID_CREDENTIALS", attempt, envelope.Error)
		}
	}
	if got := len(handler.loginAttempts.attempts); got != 1 {
		t.Fatalf("tracked attempts = %d, want one fixed invalid-username bucket", got)
	}
	wantDigest := sha256.Sum256([]byte("invalid-login-username"))
	for key := range handler.loginAttempts.attempts {
		if key.usernameDigest != wantDigest {
			t.Fatal("invalid username attempt did not use the fixed digest bucket")
		}
		if key.clientIP != "203.0.113.10" {
			t.Fatalf("invalid username attempt client IP = %q, want 203.0.113.10", key.clientIP)
		}
	}
}

func TestNormalizeLoginUsernamePreservesMaximumValidUnicodeLength(t *testing.T) {
	username := strings.Repeat("😀", maxUsernameRuneCount)
	got, err := normalizeLoginUsername(username)
	if err != nil {
		t.Fatalf("normalize maximum-length username: %v", err)
	}
	if got != username {
		t.Fatal("maximum-length username changed during normalization")
	}

	if _, err := normalizeLoginUsername(username + "😀"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("normalize oversized username error = %v, want %v", err, ErrInvalidUsername)
	}
}

func TestAuthHandler_LoginCredentialChecksAreBoundedPerIP(t *testing.T) {
	dir := t.TempDir()
	store, _, err := NewUserStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("create user store: %v", err)
	}
	if _, err := store.Create("credential-check-user", "password123", "", RoleUser); err != nil {
		t.Fatalf("create credential check user: %v", err)
	}
	handler := NewHandler(store, NewTokenManager("bounded-attempt-secret", 15*time.Minute, 24*time.Hour))
	handler.loginFailureLimit = defaultLoginAttemptMaxEntriesPerIP + 100
	fixedNow := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	handler.loginAttempts.now = func() time.Time { return fixedNow }
	clientIP := "203.0.113.20"

	for i := 0; i < defaultLoginCredentialCheckLimit+20; i++ {
		body, err := json.Marshal(LoginRequest{
			Username: "credential-check-user",
			Password: "wrong-password",
		})
		if err != nil {
			t.Fatalf("marshal login request %d: %v", i, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.RemoteAddr = clientIP + ":1234"
		rec := httptest.NewRecorder()
		handler.HandleLogin(rec, req)

		if i < defaultLoginCredentialCheckLimit && rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i, rec.Code, http.StatusUnauthorized)
		}
		if i >= defaultLoginCredentialCheckLimit && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want %d", i, rec.Code, http.StatusTooManyRequests)
		}
	}

	tracker := handler.loginAttempts
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if got := len(tracker.attempts); got != 1 {
		t.Fatalf("tracked attempts = %d, want 1", got)
	}
	if got := tracker.entriesByIP[clientIP]; got != 1 {
		t.Fatalf("per-IP tracked attempts = %d, want 1", got)
	}
	if got := tracker.credentialChecksByIP[clientIP].checks; got != defaultLoginCredentialCheckLimit {
		t.Fatalf("per-IP credential checks = %d, want %d", got, defaultLoginCredentialCheckLimit)
	}
}

func TestLoginAttemptTracker_EnforcesGlobalEntryLimit(t *testing.T) {
	tracker := newLoginAttemptTracker()
	tracker.maxEntries = 3
	tracker.maxEntriesPerIP = 2
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	keys := []loginAttemptKey{
		{usernameDigest: sha256.Sum256([]byte("user-1")), clientIP: "203.0.113.1"},
		{usernameDigest: sha256.Sum256([]byte("user-2")), clientIP: "203.0.113.1"},
		{usernameDigest: sha256.Sum256([]byte("user-3")), clientIP: "203.0.113.2"},
		{usernameDigest: sha256.Sum256([]byte("user-4")), clientIP: "203.0.113.3"},
	}
	for i, key := range keys[:3] {
		if locked := tracker.recordFailure(key, 10, time.Minute, time.Minute); locked {
			t.Fatalf("attempt %d was unexpectedly limited", i)
		}
		now = now.Add(time.Second)
	}
	if locked := tracker.recordFailure(keys[3], 10, time.Minute, time.Minute); locked {
		t.Fatal("new entry was unexpectedly locked at the global limit")
	}
	if got := len(tracker.attempts); got != tracker.maxEntries {
		t.Fatalf("tracked attempts = %d, want %d", got, tracker.maxEntries)
	}
	if _, ok := tracker.attempts[keys[0]]; ok {
		t.Fatal("expected the oldest unlocked entry to be evicted")
	}
	if _, ok := tracker.attempts[keys[3]]; !ok {
		t.Fatal("expected new entry to replace the oldest unlocked entry")
	}
}

func TestUserQuotaTrendHistoryRetentionPolicy(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	point := func(capturedAt time.Time, usedBytes int64) UserQuotaTrendPoint {
		return UserQuotaTrendPoint{
			CapturedAt:       capturedAt,
			TotalCount:       1,
			ActiveCount:      1,
			LimitedCount:     1,
			WarningCount:     0,
			ExceededCount:    0,
			AttentionCount:   0,
			UsedBytes:        usedBytes,
			LimitedUsedBytes: usedBytes,
			QuotaBytes:       10_000,
		}
	}

	history := []UserQuotaTrendPoint{
		point(time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC), 601),
		point(time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC), 402),
		point(time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC), 202),
		point(now.Add(-2*time.Hour), 102),
		point(time.Date(2024, 12, 10, 12, 0, 0, 0, time.UTC), 501),
		point(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC), 301),
		point(now.Add(-time.Hour), 101),
		point(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC), 201),
		point(time.Date(2025, 1, 20, 12, 0, 0, 0, time.UTC), 401),
	}

	retained := applyUserQuotaTrendRetention(history, now, maxUserQuotaTrendHistory)
	gotUsedBytes := make([]int64, 0, len(retained))
	for _, retainedPoint := range retained {
		gotUsedBytes = append(gotUsedBytes, retainedPoint.UsedBytes)
	}
	wantUsedBytes := []int64{101, 102, 201, 301, 401, 501}
	if fmt.Sprint(gotUsedBytes) != fmt.Sprint(wantUsedBytes) {
		t.Fatalf("retained quota trend used bytes = %v, want %v", gotUsedBytes, wantUsedBytes)
	}

	capFixture := make([]UserQuotaTrendPoint, 0, 6)
	for i := 0; i < 6; i++ {
		capFixture = append(capFixture, point(now.Add(-time.Duration(i)*time.Minute), int64(i+1)))
	}
	capped := applyUserQuotaTrendRetention(capFixture, now, 3)
	if len(capped) != 3 {
		t.Fatalf("capped retention length = %d, want 3", len(capped))
	}
	gotCappedUsedBytes := []int64{capped[0].UsedBytes, capped[1].UsedBytes, capped[2].UsedBytes}
	wantCappedUsedBytes := []int64{1, 2, 3}
	if fmt.Sprint(gotCappedUsedBytes) != fmt.Sprint(wantCappedUsedBytes) {
		t.Fatalf("capped quota trend used bytes = %v, want %v", gotCappedUsedBytes, wantCappedUsedBytes)
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
	if !admin.MustChangePassword {
		t.Fatal("expected bootstrap admin to require a password change")
	}
	if admin.PasswordChangedAt != nil {
		t.Fatalf("expected bootstrap admin password_changed_at to be unset, got %v", admin.PasswordChangedAt)
	}
}

func TestUserStorePasswordLifecyclePersists(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	store, initialPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}

	authenticated, err := store.Authenticate(admin.Username, initialPassword)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if !authenticated.MustChangePassword {
		t.Fatal("expected login to preserve must_change_password")
	}
	if authenticated.PasswordChangedAt != nil {
		t.Fatalf("expected login to preserve empty password_changed_at, got %v", authenticated.PasswordChangedAt)
	}
	if authenticated.LastLoginAt == nil {
		t.Fatal("expected login to set last_login_at")
	}
	if _, statErr := os.Stat(passwordFile); statErr != nil {
		t.Fatalf("bootstrap login removed initial password file: %v", statErr)
	}

	reloadedStore, generatedPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("reload NewUserStore() error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatal("expected reload with enabled admin not to create another bootstrap password")
	}
	reloadedAdmin, err := reloadedStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("reload GetByID(admin) error: %v", err)
	}
	if !reloadedAdmin.MustChangePassword || reloadedAdmin.PasswordChangedAt != nil || reloadedAdmin.LastLoginAt == nil {
		t.Fatalf("unexpected reloaded bootstrap lifecycle state: %+v", reloadedAdmin)
	}
	if _, statErr := os.Stat(passwordFile); statErr != nil {
		t.Fatalf("user-store restart removed initial password file: %v", statErr)
	}

	if err := reloadedStore.ChangePassword(admin.ID, initialPassword, "changedpass456"); err != nil {
		t.Fatalf("ChangePassword() error: %v", err)
	}
	changedAdmin, err := reloadedStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(changed admin) error: %v", err)
	}
	if changedAdmin.MustChangePassword {
		t.Fatal("expected self password change to clear must_change_password")
	}
	if changedAdmin.PasswordChangedAt == nil {
		t.Fatal("expected self password change to set password_changed_at")
	}
	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatalf("successful password change did not remove initial password file: %v", statErr)
	}
	changedAt := *changedAdmin.PasswordChangedAt

	reloadedStore, _, err = NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("reload changed password state error: %v", err)
	}
	changedAdmin, err = reloadedStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("reload changed admin error: %v", err)
	}
	if changedAdmin.MustChangePassword || changedAdmin.PasswordChangedAt == nil || !changedAdmin.PasswordChangedAt.Equal(changedAt) {
		t.Fatalf("unexpected persisted changed password state: %+v", changedAdmin)
	}

	if err := reloadedStore.ResetPassword(admin.ID, "resetpass456"); err != nil {
		t.Fatalf("ResetPassword() error: %v", err)
	}
	resetAdmin, err := reloadedStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(reset admin) error: %v", err)
	}
	if !resetAdmin.MustChangePassword {
		t.Fatal("expected administrative password reset to require a password change")
	}
	if resetAdmin.PasswordChangedAt == nil {
		t.Fatal("expected administrative password reset to record password_changed_at")
	}

	if err := reloadedStore.ResetOwnPassword(admin.ID, "selfreset456"); err != nil {
		t.Fatalf("ResetOwnPassword() error: %v", err)
	}
	selfResetAdmin, err := reloadedStore.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(self-reset admin) error: %v", err)
	}
	if selfResetAdmin.MustChangePassword || selfResetAdmin.PasswordChangedAt == nil {
		t.Fatalf("unexpected self-reset password state: %+v", selfResetAdmin)
	}
}

func TestAuthCredentialLifecycleResponses(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	store, initialPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	tokenManager := NewTokenManager("credential-lifecycle-secret", 15*time.Minute, 24*time.Hour)
	handler := NewHandler(store, tokenManager)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(fmt.Sprintf(`{"username":"admin","password":%q}`, initialPassword)))
	loginRec := httptest.NewRecorder()
	handler.HandleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var loginEnvelope struct {
		Data LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginEnvelope); err != nil {
		t.Fatalf("unmarshal login response error: %v", err)
	}
	if !loginEnvelope.Data.User.MustChangePassword {
		t.Fatal("expected login response to expose must_change_password=true")
	}
	if strings.Contains(loginRec.Body.String(), "password_changed_at") {
		t.Fatalf("login response exposed password_changed_at: %s", loginRec.Body.String())
	}
	if _, statErr := os.Stat(passwordFile); statErr != nil {
		t.Fatalf("login removed initial password file: %v", statErr)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewBufferString(fmt.Sprintf(`{"refresh_token":%q}`, loginEnvelope.Data.RefreshToken)))
	refreshRec := httptest.NewRecorder()
	handler.HandleRefresh(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", refreshRec.Code, refreshRec.Body.String())
	}
	if _, statErr := os.Stat(passwordFile); statErr != nil {
		t.Fatalf("refresh removed initial password file: %v", statErr)
	}

	reloadedStore, generatedPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("reload after refresh error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatalf("reload after refresh generated bootstrap password %q", generatedPassword)
	}
	if reloadedAdmin, reloadErr := reloadedStore.GetByUsername("admin"); reloadErr != nil || !reloadedAdmin.MustChangePassword {
		t.Fatalf("reload after refresh lost forced-change state: user=%+v error=%v", reloadedAdmin, reloadErr)
	}
	if _, statErr := os.Stat(passwordFile); statErr != nil {
		t.Fatalf("restart after refresh removed initial password file: %v", statErr)
	}

	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq = meReq.WithContext(context.WithValue(meReq.Context(), ContextKeyUser, admin))
	meRec := httptest.NewRecorder()
	handler.HandleMe(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", meRec.Code, meRec.Body.String())
	}
	var meEnvelope struct {
		Data struct {
			User UserInfo `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &meEnvelope); err != nil {
		t.Fatalf("unmarshal me response error: %v", err)
	}
	if !meEnvelope.Data.User.MustChangePassword {
		t.Fatal("expected current-user response to expose must_change_password=true")
	}
	if strings.Contains(meRec.Body.String(), "password_changed_at") {
		t.Fatalf("current-user response exposed password_changed_at: %s", meRec.Body.String())
	}

	selfResetReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+admin.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"selfreset456"}`))
	selfResetReq = selfResetReq.WithContext(context.WithValue(selfResetReq.Context(), ContextKeyUser, admin))
	selfResetRec := httptest.NewRecorder()
	handler.HandleResetUserPassword(selfResetRec, selfResetReq, admin.ID)
	if selfResetRec.Code != http.StatusOK {
		t.Fatalf("self reset status = %d, body = %s", selfResetRec.Code, selfResetRec.Body.String())
	}
	selfResetAdmin, err := store.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(self-reset admin) error: %v", err)
	}
	if selfResetAdmin.MustChangePassword || selfResetAdmin.PasswordChangedAt == nil {
		t.Fatalf("unexpected self-reset lifecycle state: %+v", selfResetAdmin)
	}
	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatalf("successful self password reset did not remove initial password file: %v", statErr)
	}
	meAfterChangeReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meAfterChangeReq = meAfterChangeReq.WithContext(context.WithValue(meAfterChangeReq.Context(), ContextKeyUser, selfResetAdmin))
	meAfterChangeRec := httptest.NewRecorder()
	handler.HandleMe(meAfterChangeRec, meAfterChangeReq)
	if meAfterChangeRec.Code != http.StatusOK {
		t.Fatalf("me after password change status = %d, body = %s", meAfterChangeRec.Code, meAfterChangeRec.Body.String())
	}
	if strings.Contains(meAfterChangeRec.Body.String(), "password_changed_at") {
		t.Fatalf("current-user response exposed stored password_changed_at: %s", meAfterChangeRec.Body.String())
	}

	target, err := store.Create("reset-target", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(reset target) error: %v", err)
	}
	if err := store.ChangePassword(target.ID, "password123", "changedpass456"); err != nil {
		t.Fatalf("ChangePassword(reset target) error: %v", err)
	}
	resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID+"/reset-password", bytes.NewBufferString(`{"new_password":"targetreset456"}`))
	resetReq = resetReq.WithContext(context.WithValue(resetReq.Context(), ContextKeyUser, selfResetAdmin))
	resetRec := httptest.NewRecorder()
	handler.HandleResetUserPassword(resetRec, resetReq, target.ID)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("target reset status = %d, body = %s", resetRec.Code, resetRec.Body.String())
	}
	resetTarget, err := store.GetByID(target.ID)
	if err != nil {
		t.Fatalf("GetByID(reset target) error: %v", err)
	}
	if !resetTarget.MustChangePassword || resetTarget.PasswordChangedAt == nil {
		t.Fatalf("unexpected administrative reset lifecycle state: %+v", resetTarget)
	}
	reloadedStore, generatedPassword, err = NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("reload after administrative reset error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatal("expected reload after administrative reset not to create a bootstrap password")
	}
	persistedResetTarget, err := reloadedStore.GetByID(target.ID)
	if err != nil {
		t.Fatalf("reload reset target error: %v", err)
	}
	if !persistedResetTarget.MustChangePassword || persistedResetTarget.PasswordChangedAt == nil {
		t.Fatalf("unexpected persisted administrative reset lifecycle state: %+v", persistedResetTarget)
	}

	adminUsersReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	adminUsersReq = adminUsersReq.WithContext(context.WithValue(adminUsersReq.Context(), ContextKeyUser, selfResetAdmin))
	adminUsersRec := httptest.NewRecorder()
	handler.HandleListUsers(adminUsersRec, adminUsersReq)
	if adminUsersRec.Code != http.StatusOK {
		t.Fatalf("admin users status = %d, body = %s", adminUsersRec.Code, adminUsersRec.Body.String())
	}
	if !strings.Contains(adminUsersRec.Body.String(), "password_changed_at") {
		t.Fatalf("admin users response omitted password_changed_at: %s", adminUsersRec.Body.String())
	}
}

func TestUserStoreUpdatePreservesConcurrentPasswordReset(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("concurrent-reset", "original-password", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	staleUpdate, err := store.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	staleUpdate.Email = "updated@example.com"

	resetDone := make(chan error, 1)
	go func() {
		resetDone <- store.ResetPassword(user.ID, "reset-password")
	}()
	if err := <-resetDone; err != nil {
		t.Fatalf("ResetPassword() error: %v", err)
	}

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- store.Update(staleUpdate)
	}()
	if err := <-updateDone; err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	updated, err := store.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID(updated) error: %v", err)
	}
	if updated.Email != "updated@example.com" || !updated.MustChangePassword || updated.PasswordChangedAt == nil {
		t.Fatalf("unexpected updated credential lifecycle: %+v", updated)
	}
	if authenticated, err := store.Authenticate(user.Username, "reset-password"); err != nil || !authenticated.MustChangePassword {
		t.Fatalf("Authenticate(reset password) = %+v, %v; want required-change user", authenticated, err)
	}
	if _, err := store.Authenticate(user.Username, "original-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate(original password) error = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestUserStorePatchMergesIndependentConcurrentFields(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("concurrent-patch", "original-password", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	disabled := true
	disableDone := make(chan error, 1)
	go func() {
		_, patchErr := store.Patch(user.ID, UserPatch{Disabled: &disabled})
		disableDone <- patchErr
	}()
	if err := <-disableDone; err != nil {
		t.Fatalf("Patch(disabled) error: %v", err)
	}

	email := "updated@example.com"
	groups := []string{"editors", "family"}
	profileDone := make(chan error, 1)
	go func() {
		_, patchErr := store.Patch(user.ID, UserPatch{Email: &email, Groups: &groups})
		profileDone <- patchErr
	}()
	if err := <-profileDone; err != nil {
		t.Fatalf("Patch(profile) error: %v", err)
	}

	updated, err := store.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID(updated) error: %v", err)
	}
	if !updated.Disabled || updated.Email != email || !reflect.DeepEqual(updated.Groups, groups) {
		t.Fatalf("independent patches did not merge: %+v", updated)
	}
}

func TestCredentialVersionRejectsTokensIssuedFromStaleAuthentication(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	user, err := store.Create("credential-version", "original-password", "", RoleUser)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	authenticated, err := store.Authenticate(user.Username, "original-password")
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if err := store.ResetPassword(user.ID, "reset-password"); err != nil {
		t.Fatalf("ResetPassword() error: %v", err)
	}

	tokenManager := NewTokenManager("credential-version-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour)
	stalePair, err := tokenManager.GenerateTokenPair(authenticated)
	if err != nil {
		t.Fatalf("GenerateTokenPair(stale user) error: %v", err)
	}
	middleware := NewMiddleware(store, tokenManager)
	protected := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	request.Header.Set("Authorization", "Bearer "+stalePair.AccessToken)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"TOKEN_REVOKED"`) {
		t.Fatalf("stale access token response = %d %s, want 401 TOKEN_REVOKED", response.Code, response.Body.String())
	}

	handler := NewHandler(store, tokenManager)
	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(fmt.Sprintf(`{"refresh_token":%q}`, stalePair.RefreshToken)))
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshResponse := httptest.NewRecorder()
	handler.HandleRefresh(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusUnauthorized || !strings.Contains(refreshResponse.Body.String(), `"code":"TOKEN_REVOKED"`) {
		t.Fatalf("stale refresh token response = %d %s, want 401 TOKEN_REVOKED", refreshResponse.Code, refreshResponse.Body.String())
	}
}

func TestRequireAuthRestrictsPasswordChangeRequiredUser(t *testing.T) {
	store, _, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	tokenManager := NewTokenManager("password-change-gate-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour)
	pair, err := tokenManager.GenerateTokenPair(admin)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	middleware := NewMiddleware(store, tokenManager)
	protected := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tt := range []struct {
		method     string
		path       string
		wantStatus int
	}{
		{method: http.MethodGet, path: "/api/v1/files", wantStatus: http.StatusForbidden},
		{method: http.MethodGet, path: "/api/v1/auth/me", wantStatus: http.StatusNoContent},
		{method: http.MethodPost, path: "/api/v1/auth/password", wantStatus: http.StatusNoContent},
		{method: http.MethodGet, path: "/api/v1/auth/password", wantStatus: http.StatusForbidden},
	} {
		request := httptest.NewRequest(tt.method, tt.path, nil)
		request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, request)
		if response.Code != tt.wantStatus {
			t.Fatalf("%s %s status = %d, want %d; body=%s", tt.method, tt.path, response.Code, tt.wantStatus, response.Body.String())
		}
		if tt.wantStatus == http.StatusForbidden && !strings.Contains(response.Body.String(), `"code":"PASSWORD_CHANGE_REQUIRED"`) {
			t.Fatalf("%s %s response missing PASSWORD_CHANGE_REQUIRED: %s", tt.method, tt.path, response.Body.String())
		}
	}
}

func TestVerifyCredentialsRejectsPasswordChangeRequiredUser(t *testing.T) {
	store, bootstrapPassword, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	if _, err := store.VerifyCredentials(admin.Username, bootstrapPassword); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("VerifyCredentials(bootstrap admin) error = %v, want %v", err, ErrPasswordChangeRequired)
	}
	if err := store.ResetOwnPassword(admin.ID, bootstrapPassword); err != nil {
		t.Fatalf("ResetOwnPassword(admin) error: %v", err)
	}
	if _, err := store.VerifyCredentials(admin.Username, bootstrapPassword); err != nil {
		t.Fatalf("VerifyCredentials(changed admin) error: %v", err)
	}

	user, err := store.Create("reset-webdav-user", "original-password", "", RoleUser)
	if err != nil {
		t.Fatalf("Create(user) error: %v", err)
	}
	if err := store.ResetPassword(user.ID, "temporary-password"); err != nil {
		t.Fatalf("ResetPassword(user) error: %v", err)
	}
	if _, err := store.VerifyCredentials(user.Username, "temporary-password"); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("VerifyCredentials(reset user) error = %v, want %v", err, ErrPasswordChangeRequired)
	}
}

func TestChangePasswordRejectsUnchangedPasswordWithoutClearingRequirement(t *testing.T) {
	store, bootstrapPassword, err := NewUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	originalVersion := admin.CredentialVersion

	if err := store.ChangePassword(admin.ID, bootstrapPassword, bootstrapPassword); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("ChangePassword(unchanged) error = %v, want %v", err, ErrPasswordUnchanged)
	}
	unchanged, err := store.GetByID(admin.ID)
	if err != nil {
		t.Fatalf("GetByID(admin) error: %v", err)
	}
	if !unchanged.MustChangePassword || unchanged.PasswordChangedAt != nil || unchanged.CredentialVersion != originalVersion {
		t.Fatalf("unchanged password mutated credential lifecycle: %+v", unchanged)
	}

	handler := NewHandler(store, NewTokenManager("unchanged-password-secret-at-least-32-bytes", 15*time.Minute, 24*time.Hour))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(fmt.Sprintf(`{"expected_user_id":%q,"old_password":%q,"new_password":%q}`, admin.ID, bootstrapPassword, bootstrapPassword)))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), ContextKeyUser, unchanged))
	response := httptest.NewRecorder()
	handler.HandleChangePassword(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"PASSWORD_UNCHANGED"`) {
		t.Fatalf("unchanged password response = %d %s, want 400 PASSWORD_UNCHANGED", response.Code, response.Body.String())
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

	t.Run("reject negative quota update", func(t *testing.T) {
		fresh, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload quota user: %v", err)
		}
		fresh.QuotaBytes = -1
		if err := store.Update(fresh); !errors.Is(err, errInvalidQuotaBytes) {
			t.Fatalf("expected invalid quota error, got %v", err)
		}

		updated, err := store.GetByID(user.ID)
		if err != nil {
			t.Fatalf("failed to reload quota user after rejected update: %v", err)
		}
		if updated.QuotaBytes < 0 {
			t.Fatalf("expected rejected update to preserve non-negative quota, got %d", updated.QuotaBytes)
		}
	})
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
