package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/config"
)

func TestRecoverAdminOnlyCreatesPrivateCredentialWithoutPrintingPassword(t *testing.T) {
	dir := privateRecoveryAuthStateTestDir(t)
	usersFile := filepath.Join(dir, "auth", "users.json")
	oldPassword := prepareChangedRecoveryAdmin(t, usersFile)
	configPath := writeAdminRecoveryConfig(t, dir, usersFile, true)

	var output bytes.Buffer
	if err := recoverAdminOnly(configPath, "admin", &output); err != nil {
		t.Fatalf("recoverAdminOnly() error: %v", err)
	}

	credentialPath := filepath.Join(filepath.Dir(usersFile), "initial-password.txt")
	credential, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatalf("ReadFile(credential) error: %v", err)
	}
	recoveredPassword := recoveryPasswordFromCredential(t, credential)
	if strings.Contains(output.String(), oldPassword) || strings.Contains(output.String(), recoveredPassword) {
		t.Fatalf("recovery output disclosed a password: %q", output.String())
	}
	for _, want := range []string{
		"administrator recovery completed",
		`username: "admin"`,
		fmt.Sprintf("credential_file: %q", credentialPath),
		"status: created",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("recovery output %q does not contain %q", output.String(), want)
		}
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("Stat(credential) error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential mode = %o, want 600", got)
	}

	reloaded, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore(reload) error: %v", err)
	}
	recovered, err := reloaded.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	if !recovered.MustChangePassword {
		t.Fatal("recovered administrator does not require a password change")
	}
	if _, err := reloaded.VerifyCredentials("admin", recoveredPassword); !errors.Is(err, auth.ErrPasswordChangeRequired) {
		t.Fatalf("VerifyCredentials(recovered password) error = %v, want ErrPasswordChangeRequired", err)
	}
	if _, err := reloaded.VerifyCredentials("admin", oldPassword); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("VerifyCredentials(old password) error = %v, want ErrInvalidCredentials", err)
	}

	reacquired, err := auth.AcquireStateLock(usersFile)
	if err != nil {
		t.Fatalf("AcquireStateLock() after recovery error: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("Close(reacquired lock) error: %v", err)
	}
}

func TestRecoverAdminOnlyReportsExistingBootstrapCredentialWithoutPrintingIt(t *testing.T) {
	dir := privateRecoveryAuthStateTestDir(t)
	usersFile := filepath.Join(dir, "auth", "users.json")
	_, bootstrapPassword, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	configPath := writeAdminRecoveryConfig(t, dir, usersFile, true)

	var output bytes.Buffer
	if err := recoverAdminOnly(configPath, "admin", &output); err != nil {
		t.Fatalf("recoverAdminOnly() error: %v", err)
	}
	if !strings.Contains(output.String(), "status: already_available") {
		t.Fatalf("recovery output = %q, want already_available status", output.String())
	}
	if strings.Contains(output.String(), bootstrapPassword) {
		t.Fatalf("recovery output disclosed bootstrap password: %q", output.String())
	}
}

func TestRecoverAdminOnlyRejectsRunningAuthenticationWriter(t *testing.T) {
	dir := privateRecoveryAuthStateTestDir(t)
	usersFile := filepath.Join(dir, "auth", "users.json")
	_, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	configPath := writeAdminRecoveryConfig(t, dir, usersFile, true)

	stateLock, err := auth.AcquireStateLock(usersFile)
	if err != nil {
		t.Fatalf("AcquireStateLock() error: %v", err)
	}
	defer stateLock.Close()

	var output bytes.Buffer
	err = recoverAdminOnly(configPath, "admin", &output)
	if !errors.Is(err, auth.ErrAuthStateLockHeld) {
		t.Fatalf("recoverAdminOnly() error = %v, want ErrAuthStateLockHeld", err)
	}
	if output.Len() != 0 {
		t.Fatalf("failed recovery wrote output: %q", output.String())
	}
}

func TestRecoverAdminOnlyRejectsDisabledAuthAndMissingUsersWithoutMutation(t *testing.T) {
	t.Run("authentication disabled", func(t *testing.T) {
		dir := privateRecoveryAuthStateTestDir(t)
		usersFile := filepath.Join(dir, "auth", "users.json")
		_, _, err := auth.NewUserStore(usersFile)
		if err != nil {
			t.Fatalf("NewUserStore() error: %v", err)
		}
		configPath := writeAdminRecoveryConfig(t, dir, usersFile, false)

		err = recoverAdminOnly(configPath, "admin", &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "auth.enabled=true") {
			t.Fatalf("recoverAdminOnly() error = %v, want disabled-auth rejection", err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(usersFile), "auth-state.lock")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disabled-auth recovery created a lock file, stat error = %v", err)
		}
	})

	t.Run("users file missing", func(t *testing.T) {
		dir := privateRecoveryAuthStateTestDir(t)
		usersFile := filepath.Join(dir, "missing-auth", "users.json")
		configPath := writeAdminRecoveryConfig(t, dir, usersFile, true)

		err := recoverAdminOnly(configPath, "admin", &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("recoverAdminOnly() error = %v, want missing-users rejection", err)
		}
		if _, err := os.Stat(filepath.Dir(usersFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing-users recovery created an auth directory, stat error = %v", err)
		}
	})
}

func TestRecoverAdminOnlyRejectsEmptyUsernameBeforeLoadingConfig(t *testing.T) {
	err := recoverAdminOnly(filepath.Join(t.TempDir(), "missing.toml"), "  ", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "username cannot be empty") {
		t.Fatalf("recoverAdminOnly() error = %v, want empty-username rejection", err)
	}
}

func TestValidateRuntimeAdminRecoveryStateRejectsPendingMarker(t *testing.T) {
	dir := privateRecoveryAuthStateTestDir(t)
	usersFile := filepath.Join(dir, "auth", "users.json")
	prepareChangedRecoveryAdmin(t, usersFile)
	store, _, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	marker := fmt.Sprintf(`MnemoNAS Administrator Password Recovery
Recovery Marker: mnemonas-admin-password-recovery
Recovery Schema: 1
Username: %s
User ID: %s
Previous Credential Version: %d
Password: PendingRuntimeRecovery123!
`, admin.Username, admin.ID, admin.CredentialVersion)
	if err := os.WriteFile(filepath.Join(filepath.Dir(usersFile), "initial-password.txt"), []byte(marker), 0o600); err != nil {
		t.Fatalf("WriteFile(marker) error: %v", err)
	}

	cfg := config.Default()
	cfg.Auth.Enabled = true
	cfg.Auth.UsersFile = usersFile
	if err := validateRuntimeAdminRecoveryState(cfg); !errors.Is(err, auth.ErrAdminRecoveryPending) {
		t.Fatalf("validateRuntimeAdminRecoveryState() error = %v, want ErrAdminRecoveryPending", err)
	}

	cfg.Auth.Enabled = false
	if err := validateRuntimeAdminRecoveryState(cfg); err != nil {
		t.Fatalf("disabled authentication recovery validation error: %v", err)
	}
}

func privateRecoveryAuthStateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatalf("Abs(temp directory) error: %v", err)
	}
	for current := dir; current != tempRoot; current = filepath.Dir(current) {
		if err := os.Chmod(current, 0o700); err != nil {
			t.Fatalf("Chmod(%s) error: %v", current, err)
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	return dir
}

func prepareChangedRecoveryAdmin(t *testing.T, usersFile string) string {
	t.Helper()
	store, initialPassword, err := auth.NewUserStore(usersFile)
	if err != nil {
		t.Fatalf("NewUserStore() error: %v", err)
	}
	admin, err := store.GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername(admin) error: %v", err)
	}
	oldPassword := "existing-admin-password-4829"
	if err := store.ChangePassword(admin.ID, initialPassword, oldPassword); err != nil {
		t.Fatalf("ChangePassword() error: %v", err)
	}
	return oldPassword
}

func writeAdminRecoveryConfig(t *testing.T, dir, usersFile string, authEnabled bool) string {
	t.Helper()
	configPath := filepath.Join(dir, "config.toml")
	storageRoot := filepath.Join(dir, "storage")
	contents := fmt.Sprintf("[server]\nhost = \"127.0.0.1\"\n\n[storage]\nroot = %q\n\n[auth]\nenabled = %t\nusers_file = %q\n", storageRoot, authEnabled, usersFile)
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error: %v", err)
	}
	return configPath
}

func recoveryPasswordFromCredential(t *testing.T, credential []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(credential), "\n") {
		if strings.HasPrefix(line, "Password: ") {
			password := strings.TrimPrefix(line, "Password: ")
			if password == "" {
				t.Fatal("recovery credential contains an empty password")
			}
			return password
		}
	}
	t.Fatal("recovery credential does not contain a password")
	return ""
}
