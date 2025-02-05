// Package config provides configuration management for MnemoNAS
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Secrets holds auto-generated secrets that persist across restarts
type Secrets struct {
	JWTSecret string `json:"jwt_secret"`
}

// SecretsFile is the default filename for secrets
const SecretsFile = "secrets.json"

// LoadOrCreateSecrets loads secrets from file, creating them if they don't exist
func LoadOrCreateSecrets(dataDir string) (*Secrets, error) {
	secretsPath := filepath.Join(dataDir, SecretsFile)

	// Try to load existing secrets
	data, err := os.ReadFile(secretsPath)
	if err == nil {
		var secrets Secrets
		if err := json.Unmarshal(data, &secrets); err != nil {
			return nil, fmt.Errorf("failed to parse secrets file: %w", err)
		}
		return &secrets, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read secrets file: %w", err)
	}

	// Create new secrets
	secrets := &Secrets{
		JWTSecret: generateSecureKey(32),
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Save secrets with restricted permissions
	secretsData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize secrets: %w", err)
	}

	if err := os.WriteFile(secretsPath, secretsData, 0600); err != nil {
		return nil, fmt.Errorf("failed to write secrets file: %w", err)
	}

	return secrets, nil
}

// generateSecureKey generates a cryptographically secure random key
func generateSecureKey(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random key: " + err.Error())
	}
	return hex.EncodeToString(b)
}
