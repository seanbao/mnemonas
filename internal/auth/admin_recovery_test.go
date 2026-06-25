package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminRecoveryTestOldPassword = "OldPassword123!"
	adminRecoveryTestNewPassword = "ChangedPassword456!"
)

func writeAdminRecoveryUserFixture(t *testing.T, dir, username, password string, role Role, disabled, mustChange bool, credentialVersion uint64) (string, *User) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error: %v", err)
	}
	now := time.Date(2026, time.July, 14, 1, 2, 3, 0, time.UTC)
	user := &User{
		ID:                 "0123456789abcdef0123456789abcdef",
		Username:           username,
		PasswordHash:       string(hash),
		Role:               role,
		Disabled:           disabled,
		CreatedAt:          now,
		UpdatedAt:          now,
		MustChangePassword: mustChange,
		CredentialVersion:  credentialVersion,
		HomeDir:            "/",
	}
	if role != RoleAdmin {
		user.HomeDir = "/" + username
	}
	data, err := json.Marshal(persistedUserStore{
		SchemaVersion: userStoreSchemaVersion,
		Users:         []*User{user},
	})
	if err != nil {
		t.Fatalf("Marshal(users) error: %v", err)
	}
	usersPath := filepath.Join(dir, "users.json")
	if err := os.WriteFile(usersPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(users) error: %v", err)
	}
	return usersPath, user
}

func readAdminRecoveryUserFixture(t *testing.T, usersPath, username string) *User {
	t.Helper()
	store, generatedPassword, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatal("NewUserStore() generated an unexpected administrator")
	}
	user, err := store.GetByUsername(username)
	if err != nil {
		t.Fatalf("GetByUsername() error: %v", err)
	}
	return user
}

func writeAdminRecoverySessionsFixture(t *testing.T, path, targetUserID string) string {
	t.Helper()
	floor := time.Date(2026, time.July, 14, 2, 0, 0, 0, time.UTC)
	otherUserID := "fedcba9876543210fedcba9876543210"
	state := tokenSessionState{
		SchemaVersion:    tokenSessionSchemaVersion,
		RestartTimeFloor: floor,
		Sessions: map[string]sessionRegistryRecord{
			"00000000000000000000000000000001": {
				UserID:                targetUserID,
				ExpiresAt:             floor.Add(time.Hour),
				NextRefreshGeneration: 2,
				LastRotatedAt:         floor,
			},
			"00000000000000000000000000000002": {
				UserID:                otherUserID,
				ExpiresAt:             floor.Add(time.Hour),
				NextRefreshGeneration: 1,
				LastRotatedAt:         floor,
			},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal(sessions) error: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(sessions) error: %v", err)
	}
	return otherUserID
}

func readAdminRecoveryCredentialForTest(t *testing.T, path string) *adminRecoveryCredential {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(credential) error: %v", err)
	}
	credential, err := parseAdminRecoveryCredential(data)
	if err != nil {
		t.Fatalf("parseAdminRecoveryCredential() error: %v", err)
	}
	return credential
}

func TestRecoverAdminPasswordCompletesCredentialLifecycleAndRevokesSessions(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 7)
	sessionPath := filepath.Join(dir, "auth-sessions.json")
	otherUserID := writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)

	result, err := recoverAdminPasswordLocked(usersPath, "admin")
	if err != nil {
		t.Fatalf("recoverAdminPasswordLocked() error: %v", err)
	}
	if result == nil || result.Username != "admin" || result.CredentialPath != filepath.Join(dir, "initial-password.txt") || result.Resumed || result.AlreadyAvailable {
		t.Fatalf("recoverAdminPasswordLocked() result = %#v", result)
	}
	credential := readAdminRecoveryCredentialForTest(t, result.CredentialPath)
	if credential.Password == "" || credential.Password == adminRecoveryTestOldPassword {
		t.Fatal("recovery credential did not contain a distinct generated password")
	}
	if credential.PreviousCredentialVersion != 7 || credential.UserID != original.ID {
		t.Fatalf("recovery credential binding = %#v", credential)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), credential.Password) {
		t.Fatal("recovery result exposed the generated password")
	}
	info, err := os.Stat(result.CredentialPath)
	if err != nil {
		t.Fatalf("Stat(credential) error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential permissions = %04o, want 0600", got)
	}

	state, exists, err := loadTokenSessionState(sessionPath)
	if err != nil || !exists {
		t.Fatalf("loadTokenSessionState() exists=%v error=%v", exists, err)
	}
	if len(state.Sessions) != 1 {
		t.Fatalf("remaining sessions = %d, want 1", len(state.Sessions))
	}
	for _, record := range state.Sessions {
		if record.UserID != otherUserID {
			t.Fatalf("remaining session user = %q, want %q", record.UserID, otherUserID)
		}
	}

	store, generatedPassword, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if generatedPassword != "" {
		t.Fatal("reload generated an unexpected bootstrap administrator")
	}
	if _, err := store.Authenticate("admin", adminRecoveryTestOldPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate(old password) error = %v, want ErrInvalidCredentials", err)
	}
	recovered, err := store.Authenticate("admin", credential.Password)
	if err != nil {
		t.Fatalf("Authenticate(recovery password) error: %v", err)
	}
	if !recovered.MustChangePassword || recovered.CredentialVersion != 8 || recovered.PasswordChangedAt == nil || !recovered.UpdatedAt.Equal(*recovered.PasswordChangedAt) {
		t.Fatalf("recovered user = %#v", recovered)
	}
	if err := store.ChangePassword(recovered.ID, credential.Password, adminRecoveryTestNewPassword); err != nil {
		t.Fatalf("ChangePassword() error: %v", err)
	}
	if _, err := os.Lstat(result.CredentialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initial password file still exists after password change: %v", err)
	}
	if _, err := store.Authenticate("admin", adminRecoveryTestNewPassword); err != nil {
		t.Fatalf("Authenticate(changed password) error: %v", err)
	}
}

func TestRecoverAdminPasswordRequiresExistingEnabledAdministrator(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string) string
		username   string
		wantErr    error
		usersExist bool
	}{
		{
			name: "missing users file",
			setup: func(t *testing.T, dir string) string {
				return filepath.Join(dir, "users.json")
			},
			username: "admin",
			wantErr:  ErrUserNotFound,
		},
		{
			name: "missing target",
			setup: func(t *testing.T, dir string) string {
				path, _ := writeAdminRecoveryUserFixture(t, dir, "other-admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
				return path
			},
			username:   "admin",
			wantErr:    ErrUserNotFound,
			usersExist: true,
		},
		{
			name: "non administrator",
			setup: func(t *testing.T, dir string) string {
				path, _ := writeAdminRecoveryUserFixture(t, dir, "operator", adminRecoveryTestOldPassword, RoleUser, false, false, 1)
				return path
			},
			username:   "operator",
			wantErr:    ErrAdminRecoveryNotAdmin,
			usersExist: true,
		},
		{
			name: "disabled administrator",
			setup: func(t *testing.T, dir string) string {
				path, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, true, false, 1)
				return path
			},
			username:   "admin",
			wantErr:    ErrUserDisabled,
			usersExist: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			usersPath := tt.setup(t, dir)
			before, beforeErr := os.ReadFile(usersPath)
			result, err := recoverAdminPasswordLocked(usersPath, tt.username)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("recoverAdminPasswordLocked() error = %v, want %v", err, tt.wantErr)
			}
			if result != nil {
				t.Fatalf("recoverAdminPasswordLocked() result = %#v, want nil", result)
			}
			if _, err := os.Lstat(filepath.Join(dir, "initial-password.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected recovery created initial password file: %v", err)
			}
			after, afterErr := os.ReadFile(usersPath)
			if tt.usersExist {
				if beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) {
					t.Fatal("rejected recovery changed users file")
				}
			} else if !errors.Is(afterErr, os.ErrNotExist) {
				t.Fatalf("missing users recovery created users file: %v", afterErr)
			}
		})
	}
}

func TestRecoverAdminPasswordReusesMatchingBootstrapCredential(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, true, 3)
	credentialPath := filepath.Join(dir, "initial-password.txt")
	content := []byte(fmt.Sprintf("MnemoNAS Initial Admin Password\n================================\nUsername: admin\nPassword: %s\n", adminRecoveryTestOldPassword))
	if err := os.WriteFile(credentialPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile(bootstrap credential) error: %v", err)
	}
	writeAdminRecoverySessionsFixture(t, filepath.Join(dir, "auth-sessions.json"), original.ID)

	result, err := recoverAdminPasswordLocked(usersPath, "admin")
	if err != nil {
		t.Fatalf("recoverAdminPasswordLocked() error: %v", err)
	}
	if result == nil || !result.AlreadyAvailable || result.Resumed {
		t.Fatalf("recoverAdminPasswordLocked() result = %#v", result)
	}
	after, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatalf("ReadFile(bootstrap credential) error: %v", err)
	}
	if !bytes.Equal(after, content) {
		t.Fatal("matching bootstrap credential was overwritten")
	}
	user := readAdminRecoveryUserFixture(t, usersPath, "admin")
	if user.CredentialVersion != 3 || user.PasswordHash != original.PasswordHash {
		t.Fatal("matching bootstrap credential changed the user")
	}
	state, _, err := loadTokenSessionState(filepath.Join(dir, "auth-sessions.json"))
	if err != nil {
		t.Fatalf("loadTokenSessionState() error: %v", err)
	}
	for _, record := range state.Sessions {
		if record.UserID == original.ID {
			t.Fatal("matching bootstrap recovery retained an administrator session")
		}
	}
}

func TestRecoverAdminPasswordRejectsUnsafePathsAndCredentialConflicts(t *testing.T) {
	t.Run("users symlink", func(t *testing.T) {
		dir := t.TempDir()
		realPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		linkPath := filepath.Join(dir, "linked-users.json")
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Fatalf("Symlink() error: %v", err)
		}
		if _, err := recoverAdminPasswordLocked(linkPath, "admin"); !errors.Is(err, errUserStoreSymlink) {
			t.Fatalf("recoverAdminPasswordLocked() error = %v, want errUserStoreSymlink", err)
		}
	})

	t.Run("initial password symlink", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		outside := filepath.Join(t.TempDir(), "credential.txt")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatalf("WriteFile(outside) error: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "initial-password.txt")); err != nil {
			t.Fatalf("Symlink() error: %v", err)
		}
		if _, err := recoverAdminPasswordLocked(usersPath, "admin"); !errors.Is(err, errPasswordFileSymlink) {
			t.Fatalf("recoverAdminPasswordLocked() error = %v, want errPasswordFileSymlink", err)
		}
		outsideData, err := os.ReadFile(outside)
		if err != nil || string(outsideData) != "outside" {
			t.Fatal("initial password symlink target changed")
		}
	})

	t.Run("session symlink", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		outside := filepath.Join(t.TempDir(), "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, outside, original.ID)
		outsideBefore, err := os.ReadFile(outside)
		if err != nil {
			t.Fatalf("ReadFile(outside sessions) error: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "auth-sessions.json")); err != nil {
			t.Fatalf("Symlink() error: %v", err)
		}
		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); result != nil || !errors.Is(err, errTokenSessionFileSymlink) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v, want session symlink rejection", result, err)
		}
		outsideAfter, err := os.ReadFile(outside)
		if err != nil || !bytes.Equal(outsideBefore, outsideAfter) {
			t.Fatal("session symlink target changed")
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("session symlink rejection changed user")
		}
		if _, err := os.Stat(filepath.Join(dir, "initial-password.txt")); err != nil {
			t.Fatalf("session symlink rejection did not retain pending marker: %v", err)
		}
	})

	t.Run("conflicting ordinary credential", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
		credentialPath := filepath.Join(dir, "initial-password.txt")
		content := []byte("Username: another-admin\nPassword: DifferentPassword123!\n")
		if err := os.WriteFile(credentialPath, content, 0o600); err != nil {
			t.Fatalf("WriteFile(credential) error: %v", err)
		}
		if _, err := recoverAdminPasswordLocked(usersPath, "admin"); !errors.Is(err, ErrAdminRecoveryCredentialConflict) {
			t.Fatalf("recoverAdminPasswordLocked() error = %v, want conflict", err)
		}
		after, err := os.ReadFile(credentialPath)
		if err != nil || !bytes.Equal(after, content) {
			t.Fatal("conflicting credential was overwritten")
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("credential conflict changed user")
		}
	})

	t.Run("wide credential permissions", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, true, 4)
		credentialPath := filepath.Join(dir, "initial-password.txt")
		content := []byte(fmt.Sprintf("Username: admin\nPassword: %s\n", adminRecoveryTestOldPassword))
		if err := os.WriteFile(credentialPath, content, 0o644); err != nil {
			t.Fatalf("WriteFile(credential) error: %v", err)
		}
		if _, err := recoverAdminPasswordLocked(usersPath, "admin"); !errors.Is(err, ErrAdminRecoveryCredentialPermissions) {
			t.Fatalf("recoverAdminPasswordLocked() error = %v, want permission rejection", err)
		}
		after, err := os.ReadFile(credentialPath)
		if err != nil || !bytes.Equal(after, content) {
			t.Fatal("permission rejection changed credential content")
		}
		info, err := os.Stat(credentialPath)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("permission rejection unexpectedly changed mode: info=%v error=%v", info, err)
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("permission rejection changed user")
		}
	})

	t.Run("user ID line injection", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
		data, err := os.ReadFile(usersPath)
		if err != nil {
			t.Fatalf("ReadFile(users) error: %v", err)
		}
		var persisted persistedUserStore
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatalf("Unmarshal(users) error: %v", err)
		}
		persisted.Users[0].ID = "malicious\nPassword: injected"
		data, err = json.Marshal(persisted)
		if err != nil {
			t.Fatalf("Marshal(users) error: %v", err)
		}
		if err := os.WriteFile(usersPath, data, 0o600); err != nil {
			t.Fatalf("WriteFile(users) error: %v", err)
		}

		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); err == nil || result != nil || !strings.Contains(err.Error(), "invalid user ID") {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "initial-password.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid user ID created credential file: %v", err)
		}
		if original.ID == persisted.Users[0].ID {
			t.Fatal("test fixture did not replace user ID")
		}
	})

	t.Run("credential appears before publish", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
		conflict := []byte("Username: another-admin\nPassword: ConcurrentPassword123!\n")
		originalWriter := adminRecoveryCredentialWriter
		adminRecoveryCredentialWriter = func(path string, data []byte) error {
			if err := os.WriteFile(path, conflict, 0o600); err != nil {
				return err
			}
			return originalWriter(path, data)
		}
		t.Cleanup(func() { adminRecoveryCredentialWriter = originalWriter })

		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); result != nil || !errors.Is(err, ErrAdminRecoveryCredentialConflict) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v, want publish conflict", result, err)
		}
		after, err := os.ReadFile(filepath.Join(dir, "initial-password.txt"))
		if err != nil || !bytes.Equal(after, conflict) {
			t.Fatal("atomic credential publication overwrote concurrent file")
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("credential publish conflict changed user")
		}
	})
}

func TestRecoverAdminPasswordResumesPendingAndRecognizesCommittedMarker(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 11)
	credentialPath := filepath.Join(dir, "initial-password.txt")
	pending := &adminRecoveryCredential{
		Username:                  original.Username,
		UserID:                    original.ID,
		PreviousCredentialVersion: original.CredentialVersion,
		Password:                  "PendingRecovery123!",
	}
	if err := os.WriteFile(credentialPath, marshalAdminRecoveryCredential(pending), 0o600); err != nil {
		t.Fatalf("WriteFile(pending marker) error: %v", err)
	}

	result, err := recoverAdminPasswordLocked(usersPath, "admin")
	if err != nil {
		t.Fatalf("recoverAdminPasswordLocked(pending) error: %v", err)
	}
	if result == nil || !result.Resumed || result.AlreadyAvailable {
		t.Fatalf("recoverAdminPasswordLocked(pending) result = %#v", result)
	}
	committed := readAdminRecoveryUserFixture(t, usersPath, "admin")
	if committed.CredentialVersion != 12 || bcrypt.CompareHashAndPassword([]byte(committed.PasswordHash), []byte(pending.Password)) != nil {
		t.Fatal("pending recovery marker was not committed")
	}

	result, err = recoverAdminPasswordLocked(usersPath, "admin")
	if err != nil {
		t.Fatalf("recoverAdminPasswordLocked(committed) error: %v", err)
	}
	if result == nil || !result.Resumed || !result.AlreadyAvailable {
		t.Fatalf("recoverAdminPasswordLocked(committed) result = %#v", result)
	}
	after := readAdminRecoveryUserFixture(t, usersPath, "admin")
	if after.CredentialVersion != committed.CredentialVersion || after.PasswordHash != committed.PasswordHash {
		t.Fatal("committed recovery marker was applied twice")
	}
}

func TestRecoverAdminPasswordRejectsConflictingRecoveryMarker(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 5)
	credentialPath := filepath.Join(dir, "initial-password.txt")
	conflict := &adminRecoveryCredential{
		Username:                  original.Username,
		UserID:                    original.ID,
		PreviousCredentialVersion: 3,
		Password:                  "ConflictingRecovery123!",
	}
	content := marshalAdminRecoveryCredential(conflict)
	if err := os.WriteFile(credentialPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile(conflict marker) error: %v", err)
	}
	if _, err := recoverAdminPasswordLocked(usersPath, "admin"); !errors.Is(err, ErrAdminRecoveryCredentialConflict) {
		t.Fatalf("recoverAdminPasswordLocked() error = %v, want conflict", err)
	}
	after, err := os.ReadFile(credentialPath)
	if err != nil || !bytes.Equal(after, content) {
		t.Fatal("conflicting recovery marker changed")
	}
	user := readAdminRecoveryUserFixture(t, usersPath, "admin")
	if user.CredentialVersion != original.CredentialVersion || user.PasswordHash != original.PasswordHash {
		t.Fatal("conflicting recovery marker changed user")
	}
}

func TestRecoverAdminPasswordHardFailuresLeaveUserUnchangedAndMarkerResumable(t *testing.T) {
	t.Run("credential write", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		originalWriter := adminRecoveryCredentialWriter
		adminRecoveryCredentialWriter = func(string, []byte) error { return errors.New("credential write failed") }
		t.Cleanup(func() { adminRecoveryCredentialWriter = originalWriter })

		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); err == nil || result != nil {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "initial-password.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed credential write left file: %v", err)
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("credential write failure changed user")
		}
	})

	t.Run("session save", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 2)
		sessionPath := filepath.Join(dir, "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)
		originalWriter := adminRecoverySessionStateWriter
		adminRecoverySessionStateWriter = func(string, *tokenSessionState) error { return errors.New("session save failed") }
		t.Cleanup(func() { adminRecoverySessionStateWriter = originalWriter })

		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); err == nil || result != nil || isAuthPersistenceWarning(err) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "initial-password.txt")); err != nil {
			t.Fatalf("session failure did not retain pending marker: %v", err)
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("session save failure changed user")
		}
		state, _, err := loadTokenSessionState(sessionPath)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		found := false
		for _, record := range state.Sessions {
			found = found || record.UserID == original.ID
		}
		if !found {
			t.Fatal("hard session failure changed persisted sessions")
		}
	})

	t.Run("user save then resume", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 9)
		sessionPath := filepath.Join(dir, "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)
		originalWriter := adminRecoveryUserStateWriter
		adminRecoveryUserStateWriter = func(string, map[string]*User) error { return errors.New("user save failed") }
		t.Cleanup(func() { adminRecoveryUserStateWriter = originalWriter })

		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); err == nil || result != nil || isAuthPersistenceWarning(err) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
		}
		credential := readAdminRecoveryCredentialForTest(t, filepath.Join(dir, "initial-password.txt"))
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("user save failure changed user")
		}
		state, _, err := loadTokenSessionState(sessionPath)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		for _, record := range state.Sessions {
			if record.UserID == original.ID {
				t.Fatal("user save failure occurred before session deletion was committed")
			}
		}

		adminRecoveryUserStateWriter = originalWriter
		result, err := recoverAdminPasswordLocked(usersPath, "admin")
		if err != nil {
			t.Fatalf("recoverAdminPasswordLocked(resume) error: %v", err)
		}
		if result == nil || !result.Resumed || result.AlreadyAvailable {
			t.Fatalf("recoverAdminPasswordLocked(resume) result = %#v", result)
		}
		user = readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.CredentialVersion != original.CredentialVersion+1 || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credential.Password)) != nil {
			t.Fatal("resumed user save did not commit pending credential")
		}
	})

	t.Run("credential warning stops before sessions and resumes", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 10)
		sessionPath := filepath.Join(dir, "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)
		originalCredentialWriter := adminRecoveryCredentialWriter
		originalSessionWriter := adminRecoverySessionStateWriter
		sessionWriteCalls := 0
		adminRecoveryCredentialWriter = func(path string, data []byte) error {
			if err := originalCredentialWriter(path, data); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("credential directory sync failed"))
		}
		adminRecoverySessionStateWriter = func(path string, state *tokenSessionState) error {
			sessionWriteCalls++
			return originalSessionWriter(path, state)
		}
		t.Cleanup(func() {
			adminRecoveryCredentialWriter = originalCredentialWriter
			adminRecoverySessionStateWriter = originalSessionWriter
		})

		result, err := recoverAdminPasswordLocked(usersPath, "admin")
		if result != nil || err == nil || !IsPersistenceWarning(err) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v, want credential persistence warning", result, err)
		}
		if sessionWriteCalls != 0 {
			t.Fatalf("credential warning triggered %d session writes", sessionWriteCalls)
		}
		if _, err := os.Stat(filepath.Join(dir, "initial-password.txt")); err != nil {
			t.Fatalf("credential warning did not leave visible marker: %v", err)
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("credential warning changed user")
		}
		state, _, err := loadTokenSessionState(sessionPath)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		foundTarget := false
		for _, record := range state.Sessions {
			foundTarget = foundTarget || record.UserID == original.ID
		}
		if !foundTarget {
			t.Fatal("credential warning revoked sessions before marker durability was confirmed")
		}

		adminRecoveryCredentialWriter = originalCredentialWriter
		result, err = recoverAdminPasswordLocked(usersPath, "admin")
		if err != nil {
			t.Fatalf("recoverAdminPasswordLocked(resume) error: %v", err)
		}
		if result == nil || !result.Resumed || result.AlreadyAvailable {
			t.Fatalf("recoverAdminPasswordLocked(resume) result = %#v", result)
		}
		user = readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.CredentialVersion != original.CredentialVersion+1 {
			t.Fatal("recovery did not resume after visible credential warning")
		}
	})

	t.Run("linked temp cleanup failure stops and retry removes secret", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 12)
		sessionPath := filepath.Join(dir, "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)
		originalRemove := adminRecoveryRemoveTempFile
		removeCalls := 0
		adminRecoveryRemoveTempFile = func(root *os.Root, name string) error {
			removeCalls++
			if removeCalls == 1 {
				return errors.New("temp removal failed")
			}
			return originalRemove(root, name)
		}
		t.Cleanup(func() { adminRecoveryRemoveTempFile = originalRemove })

		result, err := recoverAdminPasswordLocked(usersPath, "admin")
		if result != nil || err == nil || !IsPersistenceWarning(err) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v, want linked temp warning", result, err)
		}
		matches, err := filepath.Glob(filepath.Join(dir, ".initial-password-recovery-*.tmp"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("linked temp files = %v, error=%v, want one", matches, err)
		}
		credential := readAdminRecoveryCredentialForTest(t, filepath.Join(dir, "initial-password.txt"))
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("linked temp cleanup warning changed user")
		}
		state, _, err := loadTokenSessionState(sessionPath)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		for _, record := range state.Sessions {
			if record.UserID == original.ID {
				goto sessionsPreserved
			}
		}
		t.Fatal("linked temp cleanup warning revoked sessions")

	sessionsPreserved:
		result, err = recoverAdminPasswordLocked(usersPath, "admin")
		if err != nil {
			t.Fatalf("recoverAdminPasswordLocked(resume) error: %v", err)
		}
		if result == nil || !result.Resumed || result.AlreadyAvailable {
			t.Fatalf("recoverAdminPasswordLocked(resume) result = %#v", result)
		}
		if bcrypt.CompareHashAndPassword([]byte(readAdminRecoveryUserFixture(t, usersPath, "admin").PasswordHash), []byte(credential.Password)) != nil {
			t.Fatal("retry did not commit linked recovery credential")
		}
		matches, err = filepath.Glob(filepath.Join(dir, ".initial-password-recovery-*.tmp"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("linked temp files after recovery = %v, error=%v", matches, err)
		}
		store, _, err := NewUserStore(usersPath)
		if err != nil {
			t.Fatalf("NewUserStore() error: %v", err)
		}
		user, err = store.Authenticate("admin", credential.Password)
		if err != nil {
			t.Fatalf("Authenticate(recovery password) error: %v", err)
		}
		if err := store.ChangePassword(user.ID, credential.Password, adminRecoveryTestNewPassword); err != nil {
			t.Fatalf("ChangePassword() error: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "initial-password.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("credential target remains after password change: %v", err)
		}
		matches, err = filepath.Glob(filepath.Join(dir, ".initial-password-recovery-*.tmp"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("temp credentials remain after password change = %v, error=%v", matches, err)
		}
	})

	t.Run("session warning commits before user hard failure", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 15)
		sessionPath := filepath.Join(dir, "auth-sessions.json")
		writeAdminRecoverySessionsFixture(t, sessionPath, original.ID)
		pending := &adminRecoveryCredential{
			Username:                  original.Username,
			UserID:                    original.ID,
			PreviousCredentialVersion: original.CredentialVersion,
			Password:                  "SessionWarning123!",
		}
		if err := os.WriteFile(filepath.Join(dir, "initial-password.txt"), marshalAdminRecoveryCredential(pending), 0o600); err != nil {
			t.Fatalf("WriteFile(pending credential) error: %v", err)
		}
		originalSessionWriter := adminRecoverySessionStateWriter
		originalUserWriter := adminRecoveryUserStateWriter
		adminRecoverySessionStateWriter = func(path string, state *tokenSessionState) error {
			if err := originalSessionWriter(path, state); err != nil {
				return err
			}
			return wrapAuthPersistenceWarning(errors.New("session directory sync failed"))
		}
		adminRecoveryUserStateWriter = func(string, map[string]*User) error {
			return errors.New("user save failed")
		}
		t.Cleanup(func() {
			adminRecoverySessionStateWriter = originalSessionWriter
			adminRecoveryUserStateWriter = originalUserWriter
		})

		result, err := recoverAdminPasswordLocked(usersPath, "admin")
		if result != nil || err == nil || IsPersistenceWarning(err) {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v, want authoritative user hard failure", result, err)
		}
		user := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
			t.Fatal("user hard failure changed user")
		}
		state, _, err := loadTokenSessionState(sessionPath)
		if err != nil {
			t.Fatalf("loadTokenSessionState() error: %v", err)
		}
		for _, record := range state.Sessions {
			if record.UserID == original.ID {
				t.Fatal("committed session warning did not retain session deletion")
			}
		}

		adminRecoverySessionStateWriter = originalSessionWriter
		adminRecoveryUserStateWriter = originalUserWriter
		result, err = recoverAdminPasswordLocked(usersPath, "admin")
		if err != nil {
			t.Fatalf("recoverAdminPasswordLocked(resume) error: %v", err)
		}
		if result == nil || !result.Resumed || result.AlreadyAvailable {
			t.Fatalf("recoverAdminPasswordLocked(resume) result = %#v", result)
		}
		user = readAdminRecoveryUserFixture(t, usersPath, "admin")
		if user.CredentialVersion != original.CredentialVersion+1 {
			t.Fatal("recovery did not resume after user save failure")
		}
	})
}

func TestRecoverAdminPasswordRejectsMalformedOrOversizedSessionState(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "malformed", data: []byte(`{"schema_version":3,"sessions":{}}`)},
		{name: "oversized", data: bytes.Repeat([]byte("x"), maxTokenSessionStateFileBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 6)
			if err := os.WriteFile(filepath.Join(dir, "auth-sessions.json"), tt.data, 0o600); err != nil {
				t.Fatalf("WriteFile(sessions) error: %v", err)
			}
			result, err := recoverAdminPasswordLocked(usersPath, "admin")
			if err == nil || result != nil {
				t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "initial-password.txt")); err != nil {
				t.Fatalf("session load failure did not retain pending marker: %v", err)
			}
			user := readAdminRecoveryUserFixture(t, usersPath, "admin")
			if user.PasswordHash != original.PasswordHash || user.CredentialVersion != original.CredentialVersion {
				t.Fatal("invalid session state changed user")
			}
		})
	}
}

func TestRecoverAdminPasswordReturnsCommittedPersistenceWarningsWithResult(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 13)
	writeAdminRecoverySessionsFixture(t, filepath.Join(dir, "auth-sessions.json"), original.ID)
	pending := &adminRecoveryCredential{
		Username:                  original.Username,
		UserID:                    original.ID,
		PreviousCredentialVersion: original.CredentialVersion,
		Password:                  "WarningRecovery123!",
	}
	if err := os.WriteFile(filepath.Join(dir, "initial-password.txt"), marshalAdminRecoveryCredential(pending), 0o600); err != nil {
		t.Fatalf("WriteFile(pending credential) error: %v", err)
	}

	originalSessionWriter := adminRecoverySessionStateWriter
	originalUserWriter := adminRecoveryUserStateWriter
	adminRecoverySessionStateWriter = func(path string, state *tokenSessionState) error {
		if err := originalSessionWriter(path, state); err != nil {
			return err
		}
		return wrapAuthPersistenceWarning(errors.New("session directory sync failed"))
	}
	adminRecoveryUserStateWriter = func(path string, users map[string]*User) error {
		if err := originalUserWriter(path, users); err != nil {
			return err
		}
		return wrapAuthPersistenceWarning(errors.New("users directory sync failed"))
	}
	t.Cleanup(func() {
		adminRecoverySessionStateWriter = originalSessionWriter
		adminRecoveryUserStateWriter = originalUserWriter
	})

	result, err := recoverAdminPasswordLocked(usersPath, "admin")
	if result == nil {
		t.Fatal("recoverAdminPasswordLocked() result = nil with committed warnings")
	}
	if err == nil || !IsPersistenceWarning(err) {
		t.Fatalf("recoverAdminPasswordLocked() error = %v, want persistence warning", err)
	}
	if !result.Resumed || result.AlreadyAvailable {
		t.Fatalf("recoverAdminPasswordLocked() result = %#v, want resumed pending marker", result)
	}
	credential := readAdminRecoveryCredentialForTest(t, result.CredentialPath)
	user := readAdminRecoveryUserFixture(t, usersPath, "admin")
	if user.CredentialVersion != original.CredentialVersion+1 || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credential.Password)) != nil {
		t.Fatal("persistence warnings did not retain committed recovery state")
	}
}

func TestValidateAdminRecoveryStartupStateRejectsIncompleteRecovery(t *testing.T) {
	t.Run("pending marker", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 21)
		credential := &adminRecoveryCredential{
			Username:                  original.Username,
			UserID:                    original.ID,
			PreviousCredentialVersion: original.CredentialVersion,
			Password:                  "PendingStartupRecovery123!",
		}
		if err := os.WriteFile(filepath.Join(dir, "initial-password.txt"), marshalAdminRecoveryCredential(credential), 0o600); err != nil {
			t.Fatalf("WriteFile(recovery marker) error: %v", err)
		}

		err := ValidateAdminRecoveryStartupState(usersPath)
		if !errors.Is(err, ErrAdminRecoveryPending) {
			t.Fatalf("ValidateAdminRecoveryStartupState() error = %v, want ErrAdminRecoveryPending", err)
		}
		unchanged := readAdminRecoveryUserFixture(t, usersPath, "admin")
		if unchanged.CredentialVersion != original.CredentialVersion || unchanged.PasswordHash != original.PasswordHash {
			t.Fatal("startup validation changed a pending administrator")
		}
	})

	t.Run("orphan credential temp", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		tempPath := filepath.Join(dir, ".initial-password-recovery-0123456789abcdef.tmp")
		if err := os.WriteFile(tempPath, []byte("incomplete"), 0o600); err != nil {
			t.Fatalf("WriteFile(temp) error: %v", err)
		}

		if err := ValidateAdminRecoveryStartupState(usersPath); !errors.Is(err, ErrAdminRecoveryPending) {
			t.Fatalf("ValidateAdminRecoveryStartupState() error = %v, want ErrAdminRecoveryPending", err)
		}
		if _, err := os.Stat(tempPath); err != nil {
			t.Fatalf("startup validation removed recovery temp file: %v", err)
		}
	})

	t.Run("malformed marker", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		if err := os.WriteFile(filepath.Join(dir, "initial-password.txt"), []byte("Recovery Marker: malformed\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(marker) error: %v", err)
		}

		if err := ValidateAdminRecoveryStartupState(usersPath); !errors.Is(err, ErrAdminRecoveryCredentialConflict) {
			t.Fatalf("ValidateAdminRecoveryStartupState() error = %v, want ErrAdminRecoveryCredentialConflict", err)
		}
	})
}

func TestValidateAdminRecoveryStartupStateAllowsSafeStates(t *testing.T) {
	t.Run("no authentication state", func(t *testing.T) {
		if err := ValidateAdminRecoveryStartupState(filepath.Join(t.TempDir(), "missing", "users.json")); err != nil {
			t.Fatalf("ValidateAdminRecoveryStartupState() error: %v", err)
		}
	})

	t.Run("ordinary bootstrap credential", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, true, 1)
		content := []byte(fmt.Sprintf("Username: admin\nPassword: %s\n", adminRecoveryTestOldPassword))
		if err := os.WriteFile(filepath.Join(dir, "initial-password.txt"), content, 0o600); err != nil {
			t.Fatalf("WriteFile(bootstrap credential) error: %v", err)
		}

		if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
			t.Fatalf("ValidateAdminRecoveryStartupState() error: %v", err)
		}
	})

	t.Run("unrelated temp-like file", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 1)
		unrelatedPath := filepath.Join(dir, ".initial-password-recovery-not-ours.tmp")
		if err := os.WriteFile(unrelatedPath, []byte("unrelated"), 0o600); err != nil {
			t.Fatalf("WriteFile(unrelated) error: %v", err)
		}

		if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
			t.Fatalf("ValidateAdminRecoveryStartupState() error: %v", err)
		}
		if _, err := os.Stat(unrelatedPath); err != nil {
			t.Fatalf("startup validation removed unrelated file: %v", err)
		}
	})

	t.Run("committed recovery marker", func(t *testing.T) {
		dir := t.TempDir()
		usersPath, _ := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 8)
		if result, err := recoverAdminPasswordLocked(usersPath, "admin"); err != nil || result == nil {
			t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
		}

		if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
			t.Fatalf("ValidateAdminRecoveryStartupState() error: %v", err)
		}
	})
}

func TestDeleteRecoveredAdministratorRemovesCredentialAndAllowsRestart(t *testing.T) {
	dir := t.TempDir()
	usersPath, recoveredAdmin := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
	store, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if _, err := store.Create("backup-admin", "BackupAdminPassword123!", "", RoleAdmin); err != nil {
		t.Fatalf("Create(backup admin) error: %v", err)
	}
	if result, err := recoverAdminPasswordLocked(usersPath, recoveredAdmin.Username); err != nil || result == nil {
		t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
	}

	reloaded, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore(reload) error: %v", err)
	}
	if err := reloaded.Delete(recoveredAdmin.ID); err != nil {
		t.Fatalf("Delete(recovered admin) error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "initial-password.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Delete(recovered admin) left credential file: %v", err)
	}
	if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
		t.Fatalf("ValidateAdminRecoveryStartupState() after delete error: %v", err)
	}
	if _, _, err := NewUserStore(usersPath); err != nil {
		t.Fatalf("NewUserStore(restart) error: %v", err)
	}
}

func TestDeleteRecoveredAdministratorRestoresCredentialWhenUserSaveFails(t *testing.T) {
	dir := t.TempDir()
	usersPath, recoveredAdmin := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
	store, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if _, err := store.Create("backup-admin", "BackupAdminPassword123!", "", RoleAdmin); err != nil {
		t.Fatalf("Create(backup admin) error: %v", err)
	}
	result, err := recoverAdminPasswordLocked(usersPath, recoveredAdmin.Username)
	if err != nil || result == nil {
		t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
	}
	credentialBefore, err := os.ReadFile(result.CredentialPath)
	if err != nil {
		t.Fatalf("ReadFile(credential) error: %v", err)
	}

	reloaded, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore(reload) error: %v", err)
	}
	originalWriter := userStoreWriter
	t.Cleanup(func() { userStoreWriter = originalWriter })
	userStoreWriter = func(string, []byte) error { return errors.New("injected user save failure") }
	err = reloaded.Delete(recoveredAdmin.ID)
	userStoreWriter = originalWriter
	if err == nil || isAuthPersistenceWarning(err) {
		t.Fatalf("Delete(recovered admin) error = %v, want hard failure", err)
	}
	credentialAfter, err := os.ReadFile(result.CredentialPath)
	if err != nil {
		t.Fatalf("ReadFile(restored credential) error: %v", err)
	}
	if !bytes.Equal(credentialAfter, credentialBefore) {
		t.Fatal("failed user deletion did not restore the recovery credential")
	}
	if _, err := reloaded.GetByID(recoveredAdmin.ID); err != nil {
		t.Fatalf("failed user deletion removed administrator from memory: %v", err)
	}
	if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
		t.Fatalf("ValidateAdminRecoveryStartupState() after rollback error: %v", err)
	}
}

func TestUpdateRecoveredAdministratorRejectsRenameUntilPasswordChanged(t *testing.T) {
	dir := t.TempDir()
	usersPath, original := writeAdminRecoveryUserFixture(t, dir, "admin", adminRecoveryTestOldPassword, RoleAdmin, false, false, 4)
	result, err := recoverAdminPasswordLocked(usersPath, original.Username)
	if err != nil || result == nil {
		t.Fatalf("recoverAdminPasswordLocked() result=%#v error=%v", result, err)
	}
	credential := readAdminRecoveryCredentialForTest(t, result.CredentialPath)

	store, _, err := NewUserStore(usersPath)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername(original.Username)
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	admin.Username = "Admin"
	if err := store.Update(admin); !errors.Is(err, ErrInitialPasswordActive) {
		t.Fatalf("Update(case-only rename with recovery credential) error = %v, want ErrInitialPasswordActive", err)
	}
	admin.Username = "renamed-admin"
	if err := store.Update(admin); !errors.Is(err, ErrInitialPasswordActive) {
		t.Fatalf("Update(rename with recovery credential) error = %v, want ErrInitialPasswordActive", err)
	}
	if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
		t.Fatalf("ValidateAdminRecoveryStartupState() after rejected rename error: %v", err)
	}

	if err := store.ChangePassword(admin.ID, credential.Password, adminRecoveryTestNewPassword); err != nil {
		t.Fatalf("ChangePassword() error: %v", err)
	}
	admin, err = store.GetByUsername(original.Username)
	if err != nil {
		t.Fatalf("GetByUsername(admin after password change) error: %v", err)
	}
	admin.Username = "renamed-admin"
	if err := store.Update(admin); err != nil {
		t.Fatalf("Update(rename after password change) error: %v", err)
	}
	if _, err := store.GetByUsername("renamed-admin"); err != nil {
		t.Fatalf("GetByUsername(renamed admin) error: %v", err)
	}
	if err := ValidateAdminRecoveryStartupState(usersPath); err != nil {
		t.Fatalf("ValidateAdminRecoveryStartupState() after completed rename error: %v", err)
	}
}
