package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
