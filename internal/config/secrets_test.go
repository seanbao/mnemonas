package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	t.Run("invalid json file", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		// Write invalid JSON
		if err := os.WriteFile(secretsPath, []byte("not valid json"), 0600); err != nil {
			t.Fatalf("failed to write invalid secrets: %v", err)
		}

		_, _, err := LoadOrCreateSecrets(tmpDir)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}

		info, statErr := os.Stat(secretsPath)
		if statErr != nil {
			t.Fatalf("failed to stat invalid secrets file: %v", statErr)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("expected invalid secrets file permissions to be tightened to 0600, got %o", info.Mode().Perm())
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

	originalSyncManagedDir := syncManagedDir
	syncManagedDir = func(dir string) error {
		return errors.New("directory fsync failed")
	}
	defer func() {
		syncManagedDir = originalSyncManagedDir
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
		if passwords[pwd] {
			t.Error("duplicate password generated")
		}
		passwords[pwd] = true

		// Verify no ambiguous characters
		for _, c := range pwd {
			if c == '0' || c == 'O' || c == 'l' || c == '1' || c == 'I' {
				t.Errorf("password contains ambiguous character: %c", c)
			}
		}
	}
}
