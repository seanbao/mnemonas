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
		secrets, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		if secrets.JWTSecret == "" {
			t.Error("JWTSecret should not be empty")
		}

		if len(secrets.JWTSecret) < 64 { // 32 bytes = 64 hex chars
			t.Errorf("JWTSecret too short: got %d chars, want at least 64", len(secrets.JWTSecret))
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
		secrets1, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		// Load again - should return same secrets
		secrets2, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		if secrets1.JWTSecret != secrets2.JWTSecret {
			t.Errorf("JWTSecret changed: got %s, want %s", secrets2.JWTSecret, secrets1.JWTSecret)
		}
	})

	t.Run("invalid json file", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		// Write invalid JSON
		if err := os.WriteFile(secretsPath, []byte("not valid json"), 0600); err != nil {
			t.Fatalf("failed to write invalid secrets: %v", err)
		}

		_, err := LoadOrCreateSecrets(tmpDir)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("preserves existing secrets content", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretsPath := filepath.Join(tmpDir, SecretsFile)

		// Write custom secrets
		customSecret := "my-custom-jwt-secret-key"
		data, _ := json.Marshal(&Secrets{JWTSecret: customSecret})
		if err := os.WriteFile(secretsPath, data, 0600); err != nil {
			t.Fatalf("failed to write custom secrets: %v", err)
		}

		secrets, err := LoadOrCreateSecrets(tmpDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecrets failed: %v", err)
		}

		if secrets.JWTSecret != customSecret {
			t.Errorf("JWTSecret changed: got %s, want %s", secrets.JWTSecret, customSecret)
		}
	})
}

func TestGenerateSecureKey(t *testing.T) {
	// Generate multiple keys and verify uniqueness
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key := generateSecureKey(32)
		if len(key) != 64 { // 32 bytes = 64 hex chars
			t.Errorf("key length incorrect: got %d, want 64", len(key))
		}
		if keys[key] {
			t.Error("duplicate key generated")
		}
		keys[key] = true
	}
}
