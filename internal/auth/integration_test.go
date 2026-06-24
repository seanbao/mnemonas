package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const testValidPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func parseBootstrapCredentials(t *testing.T, passwordFile string) (string, string) {
	t.Helper()

	content, err := os.ReadFile(passwordFile)
	if err != nil {
		t.Fatalf("failed to read password file: %v", err)
	}

	var username string
	var password string
	for _, line := range strings.Split(string(content), "\n") {
		switch {
		case strings.HasPrefix(line, "Username: "):
			username = strings.TrimPrefix(line, "Username: ")
		case strings.HasPrefix(line, "Password: "):
			password = strings.TrimPrefix(line, "Password: ")
		}
	}

	if username == "" {
		t.Fatal("could not extract username from password file")
	}
	if password == "" {
		t.Fatal("could not extract password from password file")
	}

	return username, password
}

func marshalPersistedUsersFixture(t *testing.T, users []*User) []byte {
	t.Helper()
	data, err := json.Marshal(persistedUserStore{
		SchemaVersion: userStoreSchemaVersion,
		Users:         users,
	})
	if err != nil {
		t.Fatalf("Marshal(persisted users fixture) error: %v", err)
	}
	return data
}

// TestConfigMatrix_AuthInitialization tests authentication behavior under different configurations
func TestConfigMatrix_AuthInitialization(t *testing.T) {
	cases := []struct {
		name            string
		setupUsers      bool // If true, pre-create users file to simulate existing installation
		expectPassFile  bool
		expectUserFile  bool
		expectAdminUser bool
	}{
		{
			name:            "fresh install - creates password file and admin",
			setupUsers:      false,
			expectPassFile:  true,
			expectUserFile:  true,
			expectAdminUser: true,
		},
		{
			name:            "existing users - no new password file",
			setupUsers:      true,
			expectPassFile:  false,
			expectUserFile:  true,
			expectAdminUser: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			usersFile := filepath.Join(dir, "users.json")
			passwordFile := filepath.Join(dir, "initial-password.txt")

			// Setup: create existing users file if needed
			if tc.setupUsers {
				existingData := `{"schema_version":1,"users":[{"id":"existing-admin","username":"admin","password_hash":"` + testValidPasswordHash + `","role":"admin","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","must_change_password":false,"credential_version":1,"home_dir":"/"}]}`
				if err := os.WriteFile(usersFile, []byte(existingData), 0600); err != nil {
					t.Fatalf("failed to setup existing users: %v", err)
				}
			}

			// Act: create user store (triggers initialization)
			store, _, err := NewUserStore(usersFile)
			if err != nil {
				t.Fatalf("failed to create user store: %v", err)
			}

			// Assert: password file existence
			_, passFileErr := os.Stat(passwordFile)
			passFileExists := passFileErr == nil
			if passFileExists != tc.expectPassFile {
				t.Errorf("password file exists=%v, expected=%v", passFileExists, tc.expectPassFile)
			}

			// Assert: users file existence
			_, usersFileErr := os.Stat(usersFile)
			usersFileExists := usersFileErr == nil
			if usersFileExists != tc.expectUserFile {
				t.Errorf("users file exists=%v, expected=%v", usersFileExists, tc.expectUserFile)
			}

			// Assert: admin user existence
			admin, adminErr := store.GetByUsername("admin")
			adminExists := adminErr == nil && admin != nil
			if adminExists != tc.expectAdminUser {
				t.Errorf("admin user exists=%v, expected=%v", adminExists, tc.expectAdminUser)
			}
		})
	}
}

// TestPasswordFileLifecycle tests the complete lifecycle of initial-password.txt
func TestPasswordFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	store, password, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	if password == "" {
		t.Fatal("expected generated bootstrap password")
	}
	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("password file should exist after fresh install: %v", err)
	}

	user, err := store.Authenticate("admin", password)
	if err != nil {
		t.Fatalf("failed to authenticate with bootstrap password: %v", err)
	}
	if user.Username != "admin" || !user.MustChangePassword {
		t.Fatalf("unexpected bootstrap user after login: %+v", user)
	}
	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("password file should remain after successful login: %v", err)
	}

	reloaded, generatedPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to reload user store: %v", err)
	}
	if generatedPassword != "" {
		t.Fatalf("reload generated unexpected bootstrap password %q", generatedPassword)
	}
	user, err = reloaded.Authenticate("admin", password)
	if err != nil {
		t.Fatalf("bootstrap login after restart failed: %v", err)
	}
	if !user.MustChangePassword {
		t.Fatalf("restart cleared must_change_password: %+v", user)
	}
	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("password file should remain after restart login: %v", err)
	}

	if err := reloaded.ChangePassword(user.ID, password, "        "); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("whitespace-only ChangePassword() error = %v, want ErrPasswordTooShort", err)
	}
	unchanged, err := reloaded.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID() after rejected password change: %v", err)
	}
	if !unchanged.MustChangePassword {
		t.Fatal("rejected whitespace-only password cleared must_change_password")
	}
	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("rejected whitespace-only password removed the initial password file: %v", err)
	}

	if err := reloaded.ChangePassword(user.ID, password, "changed-password-123"); err != nil {
		t.Fatalf("ChangePassword() error: %v", err)
	}
	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Fatalf("password file should be removed after successful password change: %v", err)
	}
}

// TestPasswordFilePermissions tests that password file has secure permissions
func TestPasswordFilePermissions(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	_, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	info, err := os.Stat(passwordFile)
	if err != nil {
		t.Fatalf("failed to stat password file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("password file permissions=%o, expected=0600", perm)
	}
}

// TestUsersFilePermissions tests that users file has secure permissions
func TestUsersFilePermissions(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")

	_, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	info, err := os.Stat(usersFile)
	if err != nil {
		t.Fatalf("failed to stat users file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("users file permissions=%o, expected=0600", perm)
	}
}

func TestUsersFileDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth")
	usersFile := filepath.Join(dir, "users.json")

	_, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("failed to stat users file directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("users file directory permissions=%o, expected=0700", perm)
	}
}

// TestPasswordFileContent tests the format of password file
func TestPasswordFileContent(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	_, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	content, err := os.ReadFile(passwordFile)
	if err != nil {
		t.Fatalf("failed to read password file: %v", err)
	}

	contentStr := string(content)

	requiredStrings := []string{
		"MnemoNAS",
		"Username: admin",
		"Password:",
		"change this password",
		"deleted after you change this password",
	}

	for _, s := range requiredStrings {
		if !strings.Contains(contentStr, s) {
			t.Errorf("password file missing required content: %q", s)
		}
	}
}

// TestReloadPreservesInitialPasswordFileUntilPasswordChange verifies that a
// restart cannot remove the only durable copy of an unchanged bootstrap secret.
func TestReloadPreservesInitialPasswordFileUntilPasswordChange(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	// First load: creates password file
	store1, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	// Read the password and login without changing it.
	content, _ := os.ReadFile(passwordFile)
	lines := strings.Split(string(content), "\n")
	var password string
	for _, line := range lines {
		if strings.HasPrefix(line, "Password: ") {
			password = strings.TrimPrefix(line, "Password: ")
			break
		}
	}
	store1.Authenticate("admin", password)

	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("password file should remain after login: %v", err)
	}

	// Reloading should retain the existing file and not generate a new secret.
	_, generatedPassword, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to reload user store: %v", err)
	}
	if generatedPassword != "" {
		t.Fatalf("reload generated unexpected bootstrap password %q", generatedPassword)
	}
	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("password file should remain after reload: %v", err)
	}
}

func TestNewUserStore_BootstrapsRecoveryAdminWhenNoEnabledAdminExists(t *testing.T) {
	cases := []struct {
		name         string
		existingUser *User
	}{
		{
			name: "only regular user exists",
			existingUser: &User{
				ID:                "user-1",
				Username:          "member",
				PasswordHash:      testValidPasswordHash,
				Role:              RoleUser,
				CredentialVersion: 1,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
				HomeDir:           "/member",
			},
		},
		{
			name: "only disabled admin exists",
			existingUser: &User{
				ID:                "user-2",
				Username:          "disabled-admin",
				PasswordHash:      testValidPasswordHash,
				Role:              RoleAdmin,
				Disabled:          true,
				CredentialVersion: 1,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
				HomeDir:           "/",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			usersFile := filepath.Join(dir, "users.json")
			passwordFile := filepath.Join(dir, "initial-password.txt")

			data := marshalPersistedUsersFixture(t, []*User{tc.existingUser})
			if err := os.WriteFile(usersFile, data, 0600); err != nil {
				t.Fatalf("WriteFile(users.json) error: %v", err)
			}

			store, password, err := NewUserStore(usersFile)
			if err != nil {
				t.Fatalf("NewUserStore() error: %v", err)
			}
			if password == "" {
				t.Fatal("expected recovery admin password")
			}

			bootstrapUsername, bootstrapPassword := parseBootstrapCredentials(t, passwordFile)
			if bootstrapPassword != password {
				t.Fatalf("returned bootstrap password mismatch: got %q from file, %q from NewUserStore", bootstrapPassword, password)
			}

			users := store.List()
			if len(users) != 2 {
				t.Fatalf("expected 2 users after recovery bootstrap, got %d", len(users))
			}

			existing, err := store.GetByUsername(tc.existingUser.Username)
			if err != nil {
				t.Fatalf("GetByUsername(%s) error: %v", tc.existingUser.Username, err)
			}
			if existing.Role != tc.existingUser.Role || existing.Disabled != tc.existingUser.Disabled {
				t.Fatalf("expected existing user to remain unchanged, got %+v", existing)
			}

			bootstrapAdmin, err := store.GetByUsername(bootstrapUsername)
			if err != nil {
				t.Fatalf("GetByUsername(%s) error: %v", bootstrapUsername, err)
			}
			if bootstrapAdmin.Role != RoleAdmin {
				t.Fatalf("expected recovery user role admin, got %s", bootstrapAdmin.Role)
			}
			if bootstrapAdmin.Disabled {
				t.Fatal("expected recovery admin to be enabled")
			}

			authenticated, err := store.Authenticate(bootstrapUsername, bootstrapPassword)
			if err != nil {
				t.Fatalf("Authenticate(%s) error: %v", bootstrapUsername, err)
			}
			if authenticated == nil || authenticated.Role != RoleAdmin {
				t.Fatalf("expected authenticated recovery admin, got %+v", authenticated)
			}

			if _, err := os.Stat(passwordFile); err != nil {
				t.Fatalf("password file should remain until the recovery admin changes it: %v", err)
			}
		})
	}
}

func TestNewUserStore_BootstrapsUniqueRecoveryAdminWhenAdminUsernameOccupied(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	users := []*User{
		{
			ID:                "user-1",
			Username:          "admin",
			PasswordHash:      testValidPasswordHash,
			Role:              RoleUser,
			CredentialVersion: 1,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
			HomeDir:           "/admin",
		},
	}
	data := marshalPersistedUsersFixture(t, users)
	if err := os.WriteFile(usersFile, data, 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	store, password, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if password == "" {
		t.Fatal("expected recovery admin password")
	}

	bootstrapUsername, bootstrapPassword := parseBootstrapCredentials(t, passwordFile)
	if bootstrapUsername != "admin-recovery" {
		t.Fatalf("expected unique recovery username admin-recovery, got %q", bootstrapUsername)
	}
	if bootstrapPassword != password {
		t.Fatalf("returned bootstrap password mismatch: got %q from file, %q from NewUserStore", bootstrapPassword, password)
	}

	existing, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	if existing.Role != RoleUser {
		t.Fatalf("expected existing admin-named user to remain non-admin, got %s", existing.Role)
	}

	recoveryAdmin, err := store.GetByUsername("admin-recovery")
	if err != nil {
		t.Fatalf("GetByUsername(admin-recovery) error: %v", err)
	}
	if recoveryAdmin.Role != RoleAdmin {
		t.Fatalf("expected recovery admin role admin, got %s", recoveryAdmin.Role)
	}

	authenticated, err := store.Authenticate(bootstrapUsername, bootstrapPassword)
	if err != nil {
		t.Fatalf("Authenticate(%s) error: %v", bootstrapUsername, err)
	}
	if authenticated == nil || authenticated.Username != "admin-recovery" {
		t.Fatalf("expected authenticated recovery admin, got %+v", authenticated)
	}
}

func TestAuthenticate_NonBootstrapUserDoesNotDeleteRecoveryPasswordFile(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	memberHash, err := bcrypt.GenerateFromPassword([]byte("memberpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error: %v", err)
	}
	users := []*User{
		{
			ID:                "user-1",
			Username:          "member",
			PasswordHash:      string(memberHash),
			Role:              RoleUser,
			CredentialVersion: 1,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
			HomeDir:           "/member",
		},
	}
	data := marshalPersistedUsersFixture(t, users)
	if err := os.WriteFile(usersFile, data, 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	store, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}

	bootstrapUsername, bootstrapPassword := parseBootstrapCredentials(t, passwordFile)
	if bootstrapUsername != "admin" {
		t.Fatalf("expected bootstrap username admin, got %q", bootstrapUsername)
	}

	member, err := store.Authenticate("member", "memberpass123")
	if err != nil {
		t.Fatalf("Authenticate(member) error: %v", err)
	}
	if member == nil || member.Username != "member" {
		t.Fatalf("expected authenticated member user, got %+v", member)
	}

	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("expected recovery password file to survive non-bootstrap login, got %v", err)
	}

	recoveryAdmin, err := store.Authenticate(bootstrapUsername, bootstrapPassword)
	if err != nil {
		t.Fatalf("Authenticate(%s) error: %v", bootstrapUsername, err)
	}
	if recoveryAdmin == nil || recoveryAdmin.Username != bootstrapUsername {
		t.Fatalf("expected authenticated bootstrap admin, got %+v", recoveryAdmin)
	}

	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("expected recovery password file to remain until password change, got %v", err)
	}
}

func TestAuthenticate_PrefixUsernameDoesNotDeleteRecoveryPasswordFile(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	adminHash, err := bcrypt.GenerateFromPassword([]byte("adminpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error: %v", err)
	}
	users := []*User{
		{
			ID:                "user-1",
			Username:          "admin",
			PasswordHash:      string(adminHash),
			Role:              RoleUser,
			CredentialVersion: 1,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
			HomeDir:           "/admin",
		},
	}
	data := marshalPersistedUsersFixture(t, users)
	if err := os.WriteFile(usersFile, data, 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	store, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}

	bootstrapUsername, bootstrapPassword := parseBootstrapCredentials(t, passwordFile)
	if bootstrapUsername != "admin-recovery" {
		t.Fatalf("expected bootstrap username admin-recovery, got %q", bootstrapUsername)
	}

	user, err := store.Authenticate("admin", "adminpass123")
	if err != nil {
		t.Fatalf("Authenticate(admin) error: %v", err)
	}
	if user == nil || user.Username != "admin" {
		t.Fatalf("expected authenticated existing admin-named user, got %+v", user)
	}

	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("expected recovery password file to survive prefix username login, got %v", err)
	}

	recoveryAdmin, err := store.Authenticate(bootstrapUsername, bootstrapPassword)
	if err != nil {
		t.Fatalf("Authenticate(%s) error: %v", bootstrapUsername, err)
	}
	if recoveryAdmin == nil || recoveryAdmin.Username != bootstrapUsername {
		t.Fatalf("expected authenticated recovery admin, got %+v", recoveryAdmin)
	}

	if _, err := os.Stat(passwordFile); err != nil {
		t.Fatalf("expected recovery password file to remain until password change, got %v", err)
	}
}

func TestNewUserStore_RejectsCorruptUsersFileWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	corruptData := []byte("{invalid json")
	if err := os.WriteFile(usersFile, corruptData, 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	if _, _, err := NewUserStore(usersFile); err == nil {
		t.Fatal("expected NewUserStore() to reject corrupt users file")
	}
	data, err := os.ReadFile(usersFile)
	if err != nil {
		t.Fatalf("ReadFile(users.json) error: %v", err)
	}
	if !bytes.Equal(data, corruptData) {
		t.Fatalf("corrupt users file changed to %q", string(data))
	}
	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatal("expected no initial password file after fail-closed load")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "users.json.corrupt.") {
			t.Fatalf("unexpected corrupt users backup %q", entry.Name())
		}
	}
}

func TestNewUserStore_RejectsInvalidPersistedUserInvariants(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*User)
		messagePart string
	}{
		{
			name: "non-admin root home",
			mutate: func(user *User) {
				user.Role = RoleUser
				user.HomeDir = "/"
			},
			messagePart: "role/home_dir combination",
		},
		{
			name: "negative quota",
			mutate: func(user *User) {
				user.QuotaBytes = -1
			},
			messagePart: "invalid quota_bytes",
		},
		{
			name: "malformed password hash",
			mutate: func(user *User) {
				user.PasswordHash = "not-a-bcrypt-hash"
			},
			messagePart: "invalid password_hash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			usersFile := filepath.Join(dir, "users.json")
			user := &User{
				ID:                "admin-1",
				Username:          "admin",
				PasswordHash:      testValidPasswordHash,
				Role:              RoleAdmin,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
				CredentialVersion: 1,
				HomeDir:           "/",
			}
			test.mutate(user)
			payload := marshalPersistedUsersFixture(t, []*User{user})
			if err := os.WriteFile(usersFile, payload, 0600); err != nil {
				t.Fatalf("WriteFile(users.json) error: %v", err)
			}

			store, password, err := NewUserStore(usersFile)
			if err == nil || !strings.Contains(err.Error(), test.messagePart) {
				t.Fatalf("NewUserStore() error = %v, want %q", err, test.messagePart)
			}
			if store != nil || password != "" {
				t.Fatalf("NewUserStore() = (%+v, %q), want fail-closed result", store, password)
			}
			persisted, readErr := os.ReadFile(usersFile)
			if readErr != nil {
				t.Fatalf("ReadFile(users.json) error: %v", readErr)
			}
			if !bytes.Equal(persisted, payload) {
				t.Fatal("rejected users file was modified")
			}
			if _, statErr := os.Stat(filepath.Join(dir, "initial-password.txt")); !os.IsNotExist(statErr) {
				t.Fatalf("rejected users file created an initial password file: %v", statErr)
			}
		})
	}
}

func TestNewUserStore_RejectsNullUserEntry(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	if err := os.WriteFile(usersFile, []byte(`{"schema_version":1,"users":[null]}`), 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	if _, _, err := NewUserStore(usersFile); err == nil {
		t.Fatal("expected NewUserStore() to reject null user entries")
	} else if !strings.Contains(err.Error(), "null entry") {
		t.Fatalf("expected null entry error, got %v", err)
	}

	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatal("expected no password file when users file contains null entries")
	}
}

func TestNewUserStore_RejectsDuplicateNormalizedUsername(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	now := time.Now()
	users := []*User{
		{
			ID:                "user-1",
			Username:          "Admin",
			PasswordHash:      testValidPasswordHash,
			Role:              RoleAdmin,
			CredentialVersion: 1,
			CreatedAt:         now,
			UpdatedAt:         now,
			HomeDir:           "/",
		},
		{
			ID:                "user-2",
			Username:          "admin",
			PasswordHash:      testValidPasswordHash,
			Role:              RoleUser,
			CredentialVersion: 1,
			CreatedAt:         now,
			UpdatedAt:         now,
			HomeDir:           "/admin",
		},
	}
	data := marshalPersistedUsersFixture(t, users)
	if err := os.WriteFile(usersFile, data, 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	if _, _, err := NewUserStore(usersFile); err == nil {
		t.Fatal("expected NewUserStore() to reject duplicate normalized usernames")
	} else if !strings.Contains(err.Error(), "duplicate username") {
		t.Fatalf("expected duplicate username error, got %v", err)
	}

	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatal("expected no password file when users file contains duplicate usernames")
	}
}

func TestNewUserStore_RejectsSymlinkUsersFile(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "real-users.json")
	symlinkFile := filepath.Join(dir, "users.json")

	if err := os.WriteFile(targetFile, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to write target users file: %v", err)
	}
	if err := os.Symlink(targetFile, symlinkFile); err != nil {
		t.Fatalf("failed to create symlink users file: %v", err)
	}

	_, _, err := NewUserStore(symlinkFile)
	if !errors.Is(err, errUserStoreSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestNewUserStore_RejectsSymlinkUsersParentDirectory(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real-users")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real users dir: %v", err)
	}
	usersFile := filepath.Join(realDir, "users.json")
	if err := os.WriteFile(usersFile, []byte("[]"), 0600); err != nil {
		t.Fatalf("failed to seed users file: %v", err)
	}
	linkedDir := filepath.Join(dir, "linked-users")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create users dir symlink: %v", err)
	}

	_, _, err := NewUserStore(filepath.Join(linkedDir, "users.json"))
	if !errors.Is(err, errUserStoreSymlink) {
		t.Fatalf("expected parent-directory symlink error, got %v", err)
	}
}

func TestNewUserStore_RejectsSymlinkPasswordFileAndRollsBackAdmin(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	targetPasswordFile := filepath.Join(dir, "real-initial-password.txt")
	symlinkPasswordFile := filepath.Join(dir, "initial-password.txt")

	if err := os.WriteFile(targetPasswordFile, []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to write target password file: %v", err)
	}
	if err := os.Symlink(targetPasswordFile, symlinkPasswordFile); err != nil {
		t.Fatalf("failed to create password symlink: %v", err)
	}

	_, _, err := NewUserStore(usersFile)
	if !errors.Is(err, errPasswordFileSymlink) {
		t.Fatalf("expected password symlink error, got %v", err)
	}

	content, readErr := os.ReadFile(usersFile)
	if os.IsNotExist(readErr) {
		return
	}
	if readErr != nil {
		t.Fatalf("failed to read rolled back users file: %v", readErr)
	}
	if strings.Contains(string(content), "\"admin\"") {
		t.Fatalf("expected rolled back users file to omit default admin, got %s", string(content))
	}
}

// TestBoundaryConditions_Password tests password validation boundaries
func TestBoundaryConditions_Password(t *testing.T) {
	cases := []struct {
		name        string
		password    string
		expectError error
	}{
		{"empty password", "", ErrPasswordTooShort},
		{"1 char", "a", ErrPasswordTooShort},
		{"7 chars", "1234567", ErrPasswordTooShort},
		{"whitespace only", "        ", ErrPasswordTooShort},
		{"8 bytes (min)", "12345678", nil},
		{"9 UTF-8 bytes", "密码密", nil},
		{"16 chars", "1234567890123456", nil},
		{"72 chars (bcrypt max)", strings.Repeat("a", 72), nil},
		{"73 chars (above bcrypt max)", strings.Repeat("a", 73), ErrPasswordTooLong},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, _, _ := NewUserStore(filepath.Join(dir, "users.json"))

			_, err := store.Create("testuser", tc.password, "", RoleUser)

			if tc.expectError != nil {
				if err != tc.expectError {
					t.Errorf("expected error %v, got %v", tc.expectError, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestBoundaryConditions_Username tests username handling
func TestBoundaryConditions_Username(t *testing.T) {
	cases := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"normal", "testuser", false},
		{"with numbers", "user123", false},
		{"uppercase", "TESTUSER", false}, // Different from "admin", should work
		{"mixed case", "TestUser2", false},
		{"with underscore", "test_user", false},
		{"unicode", "用户", false},
		{"empty", "", true},
		{"dot path", "..", true},
		{"slash path", "team/user", true},
		{"single char", "x", false},
		{"long name", strings.Repeat("a", 255), false},
		{"too long name", strings.Repeat("a", 256), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, _, _ := NewUserStore(filepath.Join(dir, "users.json"))

			_, err := store.Create(tc.username, "validpassword123", "", RoleUser)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestCaseInsensitiveUsername tests that usernames are case-insensitive
func TestCaseInsensitiveUsername(t *testing.T) {
	dir := t.TempDir()
	store, _, _ := NewUserStore(filepath.Join(dir, "users.json"))

	// Create user with mixed case
	_, err := store.Create("TestUser", "password123", "", RoleUser)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Should find with different cases
	testCases := []string{"testuser", "TESTUSER", "TestUser", "tEsTuSeR"}
	for _, tc := range testCases {
		user, err := store.GetByUsername(tc)
		if err != nil {
			t.Errorf("failed to find user with username %q: %v", tc, err)
		} else if user.Username != "TestUser" {
			t.Errorf("expected original username TestUser, got %s", user.Username)
		}
	}

	// Should reject duplicate with different case
	_, err = store.Create("TESTUSER", "password456", "", RoleUser)
	if err != ErrUserExists {
		t.Errorf("expected ErrUserExists for case-variant duplicate, got %v", err)
	}
}
