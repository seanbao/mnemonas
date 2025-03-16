package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
				existingData := `[{"id":"existing-admin","username":"admin","password_hash":"$2a$10$dummy","role":"admin","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","home_dir":"/"}]`
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

	// Step 1: Fresh install - password file should be created
	store, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	// Verify password file exists
	if _, err := os.Stat(passwordFile); os.IsNotExist(err) {
		t.Fatal("password file should exist after fresh install")
	}

	// Step 2: Read password from file
	content, err := os.ReadFile(passwordFile)
	if err != nil {
		t.Fatalf("failed to read password file: %v", err)
	}

	// Extract password from file content
	lines := strings.Split(string(content), "\n")
	var password string
	for _, line := range lines {
		if strings.HasPrefix(line, "Password: ") {
			password = strings.TrimPrefix(line, "Password: ")
			break
		}
	}

	if password == "" {
		t.Fatal("could not extract password from password file")
	}

	// Step 3: Login with extracted password
	user, err := store.Authenticate("admin", password)
	if err != nil {
		t.Fatalf("failed to authenticate with password from file: %v", err)
	}

	if user.Username != "admin" {
		t.Errorf("expected admin, got %s", user.Username)
	}

	// Step 4: Verify password file is deleted after login
	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Error("password file should be deleted after successful login")
	}

	// Step 5: Subsequent logins should still work
	user, err = store.Authenticate("admin", password)
	if err != nil {
		t.Fatalf("subsequent login failed: %v", err)
	}

	if user.Username != "admin" {
		t.Errorf("expected admin, got %s", user.Username)
	}
}

func TestAuthenticate_FailsWhenInitialPasswordFileCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	store, password, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}
	if password == "" {
		t.Fatal("expected initial admin password")
	}

	if err := os.Remove(passwordFile); err != nil {
		t.Fatalf("failed to remove initial password file: %v", err)
	}
	if err := os.Mkdir(passwordFile, 0700); err != nil {
		t.Fatalf("failed to replace password file with directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(passwordFile, "blocker"), []byte("x"), 0600); err != nil {
		t.Fatalf("failed to make password directory non-empty: %v", err)
	}

	user, err := store.Authenticate("admin", password)
	if err == nil {
		t.Fatal("expected Authenticate to fail when initial password file removal fails")
	}
	if !strings.Contains(err.Error(), "failed to remove initial password file") {
		t.Fatalf("expected initial password file removal error, got %v", err)
	}
	if user != nil {
		t.Fatalf("expected no authenticated user on cleanup failure, got %+v", user)
	}

	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("failed to reload admin after cleanup failure: %v", err)
	}
	if admin.LastLoginAt != nil {
		t.Fatalf("expected failed cleanup to leave LastLoginAt unset, got %v", admin.LastLoginAt)
	}

	if err := os.Remove(filepath.Join(passwordFile, "blocker")); err != nil {
		t.Fatalf("failed to remove blocker file: %v", err)
	}
	if err := os.Remove(passwordFile); err != nil {
		t.Fatalf("failed to remove blocker directory: %v", err)
	}

	user, err = store.Authenticate("admin", password)
	if err != nil {
		t.Fatalf("Authenticate after cleanup recovery failed: %v", err)
	}
	if user == nil || user.Username != "admin" {
		t.Fatalf("expected recovered admin login, got %+v", user)
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
		"automatically deleted",
	}

	for _, s := range requiredStrings {
		if !strings.Contains(contentStr, s) {
			t.Errorf("password file missing required content: %q", s)
		}
	}
}

// TestReloadPreservesNoPasswordFile tests that reloading existing UserStore doesn't create new password file
func TestReloadPreservesNoPasswordFile(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")

	// First load: creates password file
	store1, _, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to create user store: %v", err)
	}

	// Read password and login to delete the file
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

	// Verify file is deleted
	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Fatal("password file should be deleted after login")
	}

	// Second load: should NOT create new password file
	_, _, err = NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("failed to reload user store: %v", err)
	}

	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Error("password file should NOT be created on reload of existing users")
	}
}

func TestNewUserStore_RecoversFromCorruptUsersFile(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	if err := os.WriteFile(usersFile, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	store, password, err := NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	if password == "" {
		t.Fatal("expected recovered user store to bootstrap a new admin password")
	}
	if _, err := store.Authenticate("admin", password); err != nil {
		t.Fatalf("Authenticate(admin) after recovery error: %v", err)
	}
	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Fatal("password file should be deleted after successful login")
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	foundBackup := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "users.json.corrupt.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("expected corrupt users backup to be created")
	}
}

func TestNewUserStore_ReturnsErrorWhenCorruptUsersBackupSyncFails(t *testing.T) {
	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.json")
	passwordFile := filepath.Join(dir, "initial-password.txt")
	if err := os.WriteFile(usersFile, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("WriteFile(users.json) error: %v", err)
	}

	originalSyncAuthFileDir := syncAuthFileDir
	syncFailed := false
	syncAuthFileDir = func(dir string) error {
		if !syncFailed {
			syncFailed = true
			return errors.New("directory fsync failed")
		}
		return nil
	}
	defer func() {
		syncAuthFileDir = originalSyncAuthFileDir
	}()

	if _, _, err := NewUserStore(usersFile); err == nil {
		t.Fatal("expected NewUserStore() to fail when corrupt users backup sync fails")
	} else if !strings.Contains(err.Error(), "sync corrupt users directory") {
		t.Fatalf("expected corrupt users sync failure in error, got %v", err)
	}

	if _, statErr := os.Stat(usersFile); statErr != nil {
		t.Fatalf("expected original corrupt users file to remain after rollback, got %v", statErr)
	}
	if _, statErr := os.Stat(passwordFile); !os.IsNotExist(statErr) {
		t.Fatal("expected no password file when corrupt users recovery fails")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir() error: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "users.json.corrupt.") {
			t.Fatalf("expected no corrupt backup after rollback, found %s", entry.Name())
		}
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
		{"8 chars (min)", "12345678", nil},
		{"16 chars", "1234567890123456", nil},
		{"72 chars (bcrypt max)", strings.Repeat("a", 72), nil},
		// Note: bcrypt silently truncates passwords > 72 bytes, so 100 chars still works
		// but only the first 72 bytes are used for hashing
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
