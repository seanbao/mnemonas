package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadOrCreateSecrets(t *testing.T) {
	t.Run("create new secrets", func(t *testing.T) {
		tmpDir := t.TempDir()
		secrets, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		if !isNew {
			t.Error("expected isNew to be true for new secrets")
		}

		if secrets.JWTSecret == "" {
			t.Error("JWTSecret should not be empty")
		}

		if len(secrets.JWTSecret) < 64 { // 32 bytes = 64 hex chars
			t.Errorf("JWTSecret too short: got %d chars, want at least 64", len(secrets.JWTSecret))
		}

		if secrets.WebDAVPassword == "" {
			t.Error("WebDAVPassword should not be empty")
		}

		if len(secrets.WebDAVPassword) != 16 {
			t.Errorf("WebDAVPassword length incorrect: got %d, want 16", len(secrets.WebDAVPassword))
		}
		assertReadablePasswordFormat(t, secrets.WebDAVPassword)

		// Verify file was created with correct permissions
		secretsPath := filepath.Join(tmpDir, SecretsFile)
		info, err := os.Stat(secretsPath)
		if err != nil {
			t.Fatalf("secrets file not created: %v", err)
		}

		if info.Mode().Perm() != 0600 {
			t.Errorf("secrets file permissions incorrect: got %o, want 0600", info.Mode().Perm())
		}
	})

	t.Run("tightens permissions for existing secrets file", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		data, err := json.Marshal(&Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
		if err != nil {
			t.Fatalf("failed to marshal secrets: %v", err)
		}
		if err := os.WriteFile(secretsPath, data, 0644); err != nil {
			t.Fatalf("failed to write existing secrets: %v", err)
		}

		_, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}
		if isNew {
			t.Fatal("expected existing secrets to load without recreation")
		}

		info, err := os.Stat(secretsPath)
		if err != nil {
			t.Fatalf("failed to stat secrets file: %v", err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("expected existing secrets permissions to be tightened to 0600, got %o", info.Mode().Perm())
		}
	})

	t.Run("removes legacy web password from existing secrets file", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		if err := os.WriteFile(secretsPath, []byte(`{"jwt_secret":"jwt","webdav_password":"webdav","web_password":"web-admin"}`), 0o600); err != nil {
			t.Fatalf("failed to write legacy secrets: %v", err)
		}

		secrets, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}
		if isNew {
			t.Fatal("expected existing secrets to load without recreation")
		}
		if secrets.JWTSecret != "jwt" || secrets.WebDAVPassword != "webdav" {
			t.Fatalf("expected non-legacy secrets to be preserved, got %+v", secrets)
		}

		data, err := os.ReadFile(secretsPath)
		if err != nil {
			t.Fatalf("failed to read scrubbed secrets: %v", err)
		}
		if strings.Contains(string(data), "web_password") || strings.Contains(string(data), "web-admin") {
			t.Fatalf("expected legacy web password to be removed, got %s", string(data))
		}
	})

	t.Run("repairs missing generated secrets in existing secrets file", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)
		completedAt := time.Date(2026, 7, 10, 9, 30, 0, 0, time.UTC)
		deferredAt := time.Date(2026, 7, 9, 9, 30, 0, 0, time.UTC)
		deferredUntil := time.Date(2026, 7, 12, 9, 30, 0, 0, time.UTC)

		incomplete := Secrets{SetupLifecycle: SetupLifecycleState{
			CompletedAt:   &completedAt,
			DeferredAt:    &deferredAt,
			DeferredUntil: &deferredUntil,
		}}
		data, err := json.Marshal(incomplete)
		if err != nil {
			t.Fatalf("failed to marshal incomplete secrets: %v", err)
		}
		if err := os.WriteFile(secretsPath, data, 0o600); err != nil {
			t.Fatalf("failed to write incomplete secrets: %v", err)
		}

		secrets, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}
		if isNew {
			t.Fatal("expected existing incomplete secrets to be repaired without marking the file new")
		}
		if strings.TrimSpace(secrets.JWTSecret) == "" {
			t.Fatal("expected missing JWT secret to be repaired")
		}
		if strings.TrimSpace(secrets.WebDAVPassword) == "" {
			t.Fatal("expected missing WebDAV password to be repaired")
		}
		assertReadablePasswordFormat(t, secrets.WebDAVPassword)
		if secrets.SetupLifecycle.CompletedAt == nil || !secrets.SetupLifecycle.CompletedAt.Equal(completedAt) {
			t.Fatalf("expected completed_at to be preserved, got %+v", secrets.SetupLifecycle)
		}
		if secrets.SetupLifecycle.DeferredAt == nil || !secrets.SetupLifecycle.DeferredAt.Equal(deferredAt) {
			t.Fatalf("expected deferred_at to be preserved, got %+v", secrets.SetupLifecycle)
		}
		if secrets.SetupLifecycle.DeferredUntil == nil || !secrets.SetupLifecycle.DeferredUntil.Equal(deferredUntil) {
			t.Fatalf("expected deferred_until to be preserved, got %+v", secrets.SetupLifecycle)
		}

		reloaded, isNewReloaded, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets reload failed: %v", err)
		}
		if isNewReloaded {
			t.Fatal("expected repaired secrets to persist")
		}
		if reloaded.JWTSecret != secrets.JWTSecret || reloaded.WebDAVPassword != secrets.WebDAVPassword {
			t.Fatalf("expected repaired secrets to persist, got %+v want %+v", reloaded, secrets)
		}
		assertSetupLifecycleEqual(t, reloaded.SetupLifecycle, secrets.SetupLifecycle)
	})

	t.Run("load existing secrets", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create initial secrets
		secrets1, isNew1, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}
		if !isNew1 {
			t.Error("expected isNew to be true for new secrets")
		}

		// Load again - should return same secrets
		secrets2, isNew2, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}
		if isNew2 {
			t.Error("expected isNew to be false for existing secrets")
		}

		if secrets1.JWTSecret != secrets2.JWTSecret {
			t.Errorf("JWTSecret changed: got %s, want %s", secrets2.JWTSecret, secrets1.JWTSecret)
		}

		if secrets1.WebDAVPassword != secrets2.WebDAVPassword {
			t.Errorf("WebDAVPassword changed: got %s, want %s", secrets2.WebDAVPassword, secrets1.WebDAVPassword)
		}
	})

	t.Run("invalid json file recovers by backing up and regenerating secrets", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		// Write invalid JSON
		if err := os.WriteFile(secretsPath, []byte("not valid json"), 0600); err != nil {
			t.Fatalf("failed to write invalid secrets: %v", err)
		}

		secrets, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets() should recover invalid JSON: %v", err)
		}
		if !isNew {
			t.Fatal("expected invalid secrets file recovery to regenerate secrets")
		}
		if secrets.JWTSecret == "" {
			t.Fatal("expected regenerated JWT secret to be non-empty")
		}
		if secrets.WebDAVPassword == "" {
			t.Fatal("expected regenerated WebDAV password to be non-empty")
		}
		assertReadablePasswordFormat(t, secrets.WebDAVPassword)

		entries, readDirErr := os.ReadDir(tmpDir)
		if readDirErr != nil {
			t.Fatalf("ReadDir(tmpDir) error: %v", readDirErr)
		}
		foundBackup := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "secrets.json.corrupt.") {
				foundBackup = true
				break
			}
		}
		if !foundBackup {
			t.Fatal("expected corrupt secrets backup to be created")
		}

		reloaded, isNewReloaded, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets() after recovery failed: %v", err)
		}
		if isNewReloaded {
			t.Fatal("expected regenerated secrets to persist across reload")
		}
		if reloaded.JWTSecret != secrets.JWTSecret {
			t.Fatalf("expected regenerated JWT secret to persist, got %q want %q", reloaded.JWTSecret, secrets.JWTSecret)
		}
		if reloaded.WebDAVPassword != secrets.WebDAVPassword {
			t.Fatalf("expected regenerated WebDAV password to persist, got %q want %q", reloaded.WebDAVPassword, secrets.WebDAVPassword)
		}

		info, statErr := os.Stat(secretsPath)
		if statErr != nil {
			t.Fatalf("failed to stat recovered secrets file: %v", statErr)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("expected recovered secrets file permissions to be 0600, got %o", info.Mode().Perm())
		}
	})

	t.Run("preserves existing secrets content", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		// Write custom secrets
		customJWT := "my-custom-jwt-secret-key"
		customWebDAV := "my-custom-webdav"
		data, _ := json.Marshal(&Secrets{JWTSecret: customJWT, WebDAVPassword: customWebDAV})
		if err := os.WriteFile(secretsPath, data, 0600); err != nil {
			t.Fatalf("failed to write custom secrets: %v", err)
		}

		secrets, isNew, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		if isNew {
			t.Error("expected isNew to be false for existing secrets")
		}

		if secrets.JWTSecret != customJWT {
			t.Errorf("JWTSecret changed: got %s, want %s", secrets.JWTSecret, customJWT)
		}

		if secrets.WebDAVPassword != customWebDAV {
			t.Errorf("WebDAVPassword changed: got %s, want %s", secrets.WebDAVPassword, customWebDAV)
		}
	})
}

func TestSaveSecrets_TightensExistingFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	if err := os.WriteFile(secretsPath, []byte(`{"jwt_secret":"old"}`), 0644); err != nil {
		t.Fatalf("failed to seed secrets file: %v", err)
	}

	if err := SaveSecrets(tmpDir, &Secrets{JWTSecret: "new", WebDAVPassword: "password"}); err != nil {
		t.Fatalf("SaveSecrets failed: %v", err)
	}

	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("failed to stat secrets file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected SaveSecrets to tighten permissions to 0600, got %o", info.Mode().Perm())
	}
}

func TestSaveSecrets_ReturnsDirectorySyncError(t *testing.T) {
	tmpDir := t.TempDir()

	originalSyncManagedRootDir := syncManagedRootDir
	syncManagedRootDir = func(root *os.Root) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncManagedRootDir = originalSyncManagedRootDir
	}()

	err := SaveSecrets(tmpDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if err == nil {
		t.Fatal("expected SaveSecrets to fail when directory sync fails")
	}
	if !strings.Contains(err.Error(), "failed to sync secrets directory") {
		t.Fatalf("expected secrets directory sync error, got %v", err)
	}

	secrets, loadErr := LoadSecrets(tmpDir)
	if loadErr != nil {
		t.Fatalf("expected secrets file to remain readable after sync failure, got %v", loadErr)
	}
	if secrets == nil {
		t.Fatal("expected secrets to remain present after sync failure")
	}
	if secrets.JWTSecret != "jwt" || secrets.WebDAVPassword != "password" {
		t.Fatalf("expected saved secrets to persist despite sync failure, got %+v", secrets)
	}
}

func TestLoadOrCreateSecrets_ReturnsDirectoryTreeSyncError(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "nested", "data")

	originalSyncManagedDir := syncManagedDir
	syncManagedDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncManagedDir = originalSyncManagedDir
	}()

	if _, _, err := LoadOrCreateSecrets(dataDir); err == nil {
		t.Fatal("expected LoadOrCreateSecrets() to fail when directory tree sync fails")
	} else if !strings.Contains(err.Error(), "failed to sync managed directory tree") {
		t.Fatalf("expected managed directory tree sync error, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dataDir, SecretsFile)); !os.IsNotExist(statErr) {
		t.Fatalf("expected no secrets file to be created, got %v", statErr)
	}
}

func TestLoadSecrets_TightensExistingFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	data, err := json.Marshal(&Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if err != nil {
		t.Fatalf("failed to marshal secrets: %v", err)
	}
	if err := os.WriteFile(secretsPath, data, 0644); err != nil {
		t.Fatalf("failed to seed secrets file: %v", err)
	}

	secrets, err := LoadSecrets(tmpDir)
	if err != nil {
		t.Fatalf("LoadSecrets failed: %v", err)
	}
	if secrets == nil {
		t.Fatal("expected secrets to load")
	}

	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("failed to stat secrets file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected LoadSecrets to tighten permissions to 0600, got %o", info.Mode().Perm())
	}
}

func TestSetupLifecycleUpdatesRequireSecretsFile(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		_, err := CompleteSetup(t.TempDir(), time.Now())
		if !errors.Is(err, ErrSecretsNotFound) {
			t.Fatalf("expected ErrSecretsNotFound, got %v", err)
		}
	})

	t.Run("defer", func(t *testing.T) {
		now := time.Now()
		_, err := DeferSetup(t.TempDir(), now, now.Add(24*time.Hour))
		if !errors.Is(err, ErrSecretsNotFound) {
			t.Fatalf("expected ErrSecretsNotFound, got %v", err)
		}
	})
}

func TestSetupLifecyclePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	if _, _, err := LoadOrCreateSecrets(tmpDir); err != nil {
		t.Fatalf("LoadOrCreateSecrets() error: %v", err)
	}

	firstDeferredAt := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	firstDeferredUntil := firstDeferredAt.Add(72 * time.Hour)
	deferred, err := DeferSetup(tmpDir, firstDeferredAt, firstDeferredUntil)
	if err != nil {
		t.Fatalf("DeferSetup() error: %v", err)
	}
	assertSetupLifecycleEqual(t, deferred, SetupLifecycleState{
		DeferredAt:    &firstDeferredAt,
		DeferredUntil: &firstDeferredUntil,
	})

	// Mutating the returned state must not alter the persisted lifecycle.
	*deferred.DeferredAt = deferred.DeferredAt.Add(time.Hour)
	loaded, err := LoadSecrets(tmpDir)
	if err != nil {
		t.Fatalf("LoadSecrets() after defer error: %v", err)
	}
	assertSetupLifecycleEqual(t, loaded.SetupLifecycle, SetupLifecycleState{
		DeferredAt:    &firstDeferredAt,
		DeferredUntil: &firstDeferredUntil,
	})

	secondDeferredAt := firstDeferredAt.Add(24 * time.Hour)
	secondDeferredUntil := secondDeferredAt.Add(7 * 24 * time.Hour)
	deferred, err = DeferSetup(tmpDir, secondDeferredAt, secondDeferredUntil)
	if err != nil {
		t.Fatalf("second DeferSetup() error: %v", err)
	}
	assertSetupLifecycleEqual(t, deferred, SetupLifecycleState{
		DeferredAt:    &secondDeferredAt,
		DeferredUntil: &secondDeferredUntil,
	})

	completedAt := secondDeferredAt.Add(time.Hour)
	completed, err := CompleteSetup(tmpDir, completedAt)
	if err != nil {
		t.Fatalf("CompleteSetup() error: %v", err)
	}
	assertSetupLifecycleEqual(t, completed, SetupLifecycleState{CompletedAt: &completedAt})

	laterCompletion := completedAt.Add(24 * time.Hour)
	completedAgain, err := CompleteSetup(tmpDir, laterCompletion)
	if err != nil {
		t.Fatalf("second CompleteSetup() error: %v", err)
	}
	assertSetupLifecycleEqual(t, completedAgain, SetupLifecycleState{CompletedAt: &completedAt})

	deferredAfterCompletion, err := DeferSetup(tmpDir, laterCompletion, laterCompletion.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("DeferSetup() after completion error: %v", err)
	}
	assertSetupLifecycleEqual(t, deferredAfterCompletion, SetupLifecycleState{CompletedAt: &completedAt})

	reloaded, err := LoadSecrets(tmpDir)
	if err != nil {
		t.Fatalf("LoadSecrets() after completion error: %v", err)
	}
	assertSetupLifecycleEqual(t, reloaded.SetupLifecycle, SetupLifecycleState{CompletedAt: &completedAt})
}

func TestConcurrentLegacyScrubAndSetupCompletionPreservesLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, SecretsFile)
	if err := os.WriteFile(secretsPath, []byte(`{"jwt_secret":"jwt","webdav_password":"webdav","web_password":"legacy"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(secrets) error: %v", err)
	}

	completedAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	errorsCh := make(chan error, 17)
	var wg sync.WaitGroup
	wg.Add(17)
	go func() {
		defer wg.Done()
		<-start
		_, err := CompleteSetup(tmpDir, completedAt)
		errorsCh <- err
	}()
	for range 16 {
		go func() {
			defer wg.Done()
			<-start
			_, err := LoadSecrets(tmpDir)
			errorsCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent secrets operation error: %v", err)
		}
	}

	loaded, err := LoadSecrets(tmpDir)
	if err != nil {
		t.Fatalf("LoadSecrets() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadSecrets() returned nil")
	}
	assertSetupLifecycleEqual(t, loaded.SetupLifecycle, SetupLifecycleState{CompletedAt: &completedAt})
	raw, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("ReadFile(secrets) error: %v", err)
	}
	if strings.Contains(string(raw), `"web_password"`) {
		t.Fatalf("legacy web password was not scrubbed: %s", raw)
	}
}

func assertSetupLifecycleEqual(t *testing.T, got, want SetupLifecycleState) {
	t.Helper()
	assertOptionalTimeEqual(t, "completed_at", got.CompletedAt, want.CompletedAt)
	assertOptionalTimeEqual(t, "deferred_at", got.DeferredAt, want.DeferredAt)
	assertOptionalTimeEqual(t, "deferred_until", got.DeferredUntil, want.DeferredUntil)
}

func assertOptionalTimeEqual(t *testing.T, field string, got, want *time.Time) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
		return
	}
	if !got.Equal(*want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func TestLoadSecrets_TightensInvalidExistingFilePermissionsBeforeParseError(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	if err := os.WriteFile(secretsPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to seed invalid secrets file: %v", err)
	}

	_, err := LoadSecrets(tmpDir)
	if err == nil {
		t.Fatal("expected LoadSecrets to fail on invalid JSON")
	}

	info, statErr := os.Stat(secretsPath)
	if statErr != nil {
		t.Fatalf("failed to stat invalid secrets file: %v", statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected invalid secrets file permissions to be tightened to 0600, got %o", info.Mode().Perm())
	}
}

func TestSaveSecrets_RejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "sensitive.json")
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	if err := os.WriteFile(targetPath, []byte(`{"keep":"original"}`), 0600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	if err := os.Symlink(targetPath, secretsPath); err != nil {
		t.Fatalf("failed to create symlink secrets path: %v", err)
	}

	err := SaveSecrets(tmpDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(data) != `{"keep":"original"}` {
		t.Fatalf("expected target file to remain unchanged, got %q", string(data))
	}
}

func TestLoadSecrets_RejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "sensitive.json")
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	data, err := json.Marshal(&Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if err != nil {
		t.Fatalf("failed to marshal secrets: %v", err)
	}
	if err := os.WriteFile(targetPath, data, 0600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	if err := os.Symlink(targetPath, secretsPath); err != nil {
		t.Fatalf("failed to create symlink secrets path: %v", err)
	}

	_, err = LoadSecrets(tmpDir)
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestLoadOrCreateSecrets_RejectsSymlinkPath(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "sensitive.json")
	secretsPath := filepath.Join(tmpDir, SecretsFile)

	if err := os.WriteFile(targetPath, []byte(`{"keep":"original"}`), 0600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	if err := os.Symlink(targetPath, secretsPath); err != nil {
		t.Fatalf("failed to create symlink secrets path: %v", err)
	}

	_, _, err := LoadOrCreateSecrets(tmpDir)
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestSaveSecrets_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-secrets")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real secrets dir: %v", err)
	}
	targetPath := filepath.Join(realDir, SecretsFile)
	if err := os.WriteFile(targetPath, []byte(`{"keep":"original"}`), 0600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-secrets")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create secrets dir symlink: %v", err)
	}

	err := SaveSecrets(linkedDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(data) != `{"keep":"original"}` {
		t.Fatalf("expected target file to remain unchanged, got %q", string(data))
	}
}

func TestLoadSecrets_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-secrets")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real secrets dir: %v", err)
	}
	targetPath := filepath.Join(realDir, SecretsFile)
	data, err := json.Marshal(&Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if err != nil {
		t.Fatalf("failed to marshal secrets: %v", err)
	}
	if err := os.WriteFile(targetPath, data, 0600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-secrets")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create secrets dir symlink: %v", err)
	}

	_, err = LoadSecrets(linkedDir)
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
}

func TestLoadOrCreateSecrets_RejectsSymlinkParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real-secrets")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("failed to create real secrets dir: %v", err)
	}
	linkedDir := filepath.Join(tmpDir, "linked-secrets")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("failed to create secrets dir symlink: %v", err)
	}

	_, _, err := LoadOrCreateSecrets(linkedDir)
	if !errors.Is(err, errSecretsFileSymlink) {
		t.Fatalf("expected parent-directory symlink rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realDir, SecretsFile)); !os.IsNotExist(statErr) {
		t.Fatalf("expected no secrets file created in symlink target dir, got %v", statErr)
	}
}

func TestSaveSecrets_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	secretsDir := filepath.Join(baseDir, "secrets")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	outsidePath := filepath.Join(outsideDir, SecretsFile)
	if err := os.WriteFile(outsidePath, []byte(`{"keep":"outside"}`), 0600); err != nil {
		t.Fatalf("failed to seed outside secrets: %v", err)
	}

	originalHook := afterValidateManagedFilePath
	var hookErr error
	afterValidateManagedFilePath = func() {
		if hookErr != nil {
			return
		}
		backupDir := filepath.Join(baseDir, "secrets-backup")
		if err := os.Rename(secretsDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, secretsDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
	}()

	err := SaveSecrets(secretsDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected SaveSecrets to stay bound to the original directory, got %v", err)
	}

	data, readErr := os.ReadFile(outsidePath)
	if readErr != nil {
		t.Fatalf("failed to read outside secrets: %v", readErr)
	}
	if string(data) != `{"keep":"outside"}` {
		t.Fatalf("expected outside secrets to remain unchanged, got %q", string(data))
	}

	loaded, loadErr := LoadSecrets(filepath.Join(baseDir, "secrets-backup"))
	if loadErr != nil {
		t.Fatalf("failed to load secrets written through original directory root: %v", loadErr)
	}
	if loaded == nil || loaded.JWTSecret != "jwt" || loaded.WebDAVPassword != "password" {
		t.Fatalf("expected saved secrets to remain bound to original directory, got %+v", loaded)
	}
}

func TestSaveSecrets_CleansCreatedDirectoriesWhenTempCreateFails(t *testing.T) {
	tmpDir := t.TempDir()
	secretsDir := filepath.Join(tmpDir, "nested", "secrets")
	secretsPath := filepath.Join(secretsDir, SecretsFile)

	originalHook := afterValidateManagedFilePath
	var hookErr error
	hookApplied := false
	afterValidateManagedFilePath = func() {
		if hookApplied || hookErr != nil {
			return
		}
		hookApplied = true
		hookErr = os.Chmod(secretsDir, 0500)
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
		_ = os.Chmod(secretsDir, 0755)
	}()

	err := SaveSecrets(secretsDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"})
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err == nil {
		t.Fatal("expected SaveSecrets() to fail when temp file creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create temp secrets file") {
		t.Fatalf("expected temp create error, got %v", err)
	}
	if _, statErr := os.Stat(secretsPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no secrets file to be created, got %v", statErr)
	}
	if _, statErr := os.Stat(secretsDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected created secrets directory to be removed, got %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "nested")); !os.IsNotExist(statErr) {
		t.Fatalf("expected created parent directory to be removed, got %v", statErr)
	}

	afterValidateManagedFilePath = originalHook
	if err := SaveSecrets(secretsDir, &Secrets{JWTSecret: "jwt", WebDAVPassword: "password"}); err != nil {
		t.Fatalf("expected retry after failed save cleanup to succeed, got %v", err)
	}
}

func TestLoadSecrets_DoesNotFollowSymlinkInsertedAfterValidation(t *testing.T) {
	baseDir := t.TempDir()
	secretsDir := filepath.Join(baseDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0755); err != nil {
		t.Fatalf("failed to create secrets dir: %v", err)
	}
	secretsPath := filepath.Join(secretsDir, SecretsFile)
	if err := os.WriteFile(secretsPath, []byte(`{"jwt_secret":"jwt","webdav_password":"password"}`), 0600); err != nil {
		t.Fatalf("failed to seed secrets file: %v", err)
	}
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, SecretsFile), []byte(`{"jwt_secret":"outside"}`), 0600); err != nil {
		t.Fatalf("failed to seed outside secrets file: %v", err)
	}

	originalHook := afterValidateManagedFilePath
	var hookErr error
	afterValidateManagedFilePath = func() {
		if hookErr != nil {
			return
		}
		backupDir := filepath.Join(baseDir, "secrets-backup")
		if err := os.Rename(secretsDir, backupDir); err != nil {
			hookErr = err
			return
		}
		if err := os.Symlink(outsideDir, secretsDir); err != nil {
			hookErr = err
		}
	}
	defer func() {
		afterValidateManagedFilePath = originalHook
	}()

	loaded, err := LoadSecrets(secretsDir)
	if hookErr != nil {
		t.Fatalf("afterValidateManagedFilePath hook error: %v", hookErr)
	}
	if err != nil {
		t.Fatalf("expected LoadSecrets to stay bound to the original directory, got %v", err)
	}
	if loaded == nil || loaded.JWTSecret != "jwt" || loaded.WebDAVPassword != "password" {
		t.Fatalf("expected secrets load to ignore the swapped symlink target, got %+v", loaded)
	}
}

func TestGenerateSecureKey(t *testing.T) {
	// Generate multiple keys and verify uniqueness
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key, err := generateSecureKey(32)
		if err != nil {
			t.Fatalf("generateSecureKey failed: %v", err)
		}
		if len(key) != 64 { // 32 bytes = 64 hex chars
			t.Errorf("key length incorrect: got %d, want 64", len(key))
		}
		if keys[key] {
			t.Error("duplicate key generated")
		}
		keys[key] = true
	}
}

func TestGenerateReadablePassword(t *testing.T) {
	// Generate multiple passwords and verify uniqueness and format
	passwords := make(map[string]bool)
	for i := 0; i < 100; i++ {
		pwd, err := generateReadablePassword(16)
		if err != nil {
			t.Fatalf("generateReadablePassword failed: %v", err)
		}
		if len(pwd) != 16 {
			t.Errorf("password length incorrect: got %d, want 16", len(pwd))
		}
		assertReadablePasswordFormat(t, pwd)
		if passwords[pwd] {
			t.Error("duplicate password generated")
		}
		passwords[pwd] = true
	}
}

func assertReadablePasswordFormat(t *testing.T, password string) {
	t.Helper()
	var hasLower, hasUpper, hasDigit bool
	for _, c := range password {
		switch {
		case strings.ContainsRune(readablePasswordLowerChars, c):
			hasLower = true
		case strings.ContainsRune(readablePasswordUpperChars, c):
			hasUpper = true
		case strings.ContainsRune(readablePasswordDigitChars, c):
			hasDigit = true
		default:
			t.Fatalf("password contains unsupported or ambiguous character: %c", c)
		}
	}
	if !hasLower || !hasUpper || !hasDigit {
		t.Fatalf("password should contain lowercase, uppercase, and digits; got %q", password)
	}
}
