// Package config provides configuration management for MnemoNAS
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Secrets holds auto-generated secrets that persist across restarts
type Secrets struct {
	JWTSecret      string `json:"jwt_secret"`
	WebDAVPassword string `json:"webdav_password,omitempty"` // Auto-generated if not configured
	WebPassword    string `json:"web_password,omitempty"`    // Auto-generated initial admin password
	SetupShown     bool   `json:"setup_shown,omitempty"`     // True if setup info has been shown to user
}

// SecretsFile is the default filename for secrets
const SecretsFile = "secrets.json"

const secretsFileMode = 0600

var errSecretsFileSymlink = errors.New("secrets file path must not be a symlink")

// LoadSecrets loads secrets from file without creating new ones.
// Returns nil if file does not exist.
func LoadSecrets(dataDir string) (*Secrets, error) {
	secretsPath := filepath.Join(dataDir, SecretsFile)
	if err := validateSecretsFilePath(secretsPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(secretsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read secrets file: %w", err)
	}
	if err := ensureSecretsFilePermissions(secretsPath); err != nil {
		return nil, err
	}

	var secrets Secrets
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("failed to parse secrets file: %w", err)
	}
	return &secrets, nil
}

// SaveSecrets saves secrets to file
func SaveSecrets(dataDir string, secrets *Secrets) error {
	secretsPath := filepath.Join(dataDir, SecretsFile)
	if err := validateSecretsFilePath(secretsPath); err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize secrets: %w", err)
	}

	if err := writeSecretsFile(secretsPath, data); err != nil {
		return err
	}
	return nil
}

// MarkSetupShown marks the setup as shown in secrets file
func MarkSetupShown(dataDir string) error {
	secrets, err := LoadSecrets(dataDir)
	if err != nil {
		return err
	}
	if secrets == nil {
		return nil // No secrets file, nothing to mark
	}

	secrets.SetupShown = true
	return SaveSecrets(dataDir, secrets)
}

// LoadOrCreateSecrets loads secrets from file, creating them if they don't exist.
// Returns the secrets and a boolean indicating if they were newly created.
func LoadOrCreateSecrets(dataDir string) (*Secrets, bool, error) {
	secretsPath := filepath.Join(dataDir, SecretsFile)
	if err := validateSecretsFilePath(secretsPath); err != nil {
		return nil, false, err
	}

	// Try to load existing secrets
	data, err := os.ReadFile(secretsPath)
	if err == nil {
		if err := ensureSecretsFilePermissions(secretsPath); err != nil {
			return nil, false, err
		}
		var secrets Secrets
		if err := json.Unmarshal(data, &secrets); err != nil {
			return nil, false, fmt.Errorf("failed to parse secrets file: %w", err)
		}
		return &secrets, false, nil
	}

	if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("failed to read secrets file: %w", err)
	}

	// Create new secrets
	jwtSecret, err := generateSecureKey(32)
	if err != nil {
		return nil, false, err
	}
	webdavPassword, err := generateReadablePassword(16)
	if err != nil {
		return nil, false, err
	}
	secrets := &Secrets{
		JWTSecret:      jwtSecret,
		WebDAVPassword: webdavPassword,
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, false, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Save secrets with restricted permissions
	secretsData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("failed to serialize secrets: %w", err)
	}

	if err := writeSecretsFile(secretsPath, secretsData); err != nil {
		return nil, false, err
	}

	return secrets, true, nil
}

func validateSecretsFilePath(secretsPath string) error {
	return validateManagedFilePath(secretsPath, errSecretsFileSymlink, "secrets file")
}

func writeSecretsFile(secretsPath string, data []byte) error {
	if err := validateSecretsFilePath(secretsPath); err != nil {
		return err
	}

	dir := filepath.Dir(secretsPath)
	tmpFile, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp secrets file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(secretsFileMode); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set temp secrets permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write secrets file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync secrets file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp secrets file: %w", err)
	}
	if err := os.Rename(tmpPath, secretsPath); err != nil {
		return fmt.Errorf("failed to replace secrets file: %w", err)
	}
	cleanup = false
	if err := ensureSecretsFilePermissions(secretsPath); err != nil {
		return err
	}
	return nil
}

func ensureSecretsFilePermissions(secretsPath string) error {
	if err := os.Chmod(secretsPath, secretsFileMode); err != nil {
		return fmt.Errorf("failed to set secrets file permissions: %w", err)
	}
	return nil
}

// generateSecureKey generates a cryptographically secure random key
func generateSecureKey(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// generateReadablePassword generates a human-readable random password
// Uses a mix of lowercase, uppercase, and digits (no ambiguous characters)
func generateReadablePassword(length int) (string, error) {
	// Exclude ambiguous characters: 0, O, l, 1, I
	const charset = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random password: %w", err)
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}
