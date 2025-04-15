// Package config provides configuration management for MnemoNAS
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
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

// ErrSecretsNotFound indicates that the runtime secrets file is unexpectedly missing.
var ErrSecretsNotFound = errors.New("secrets file not found")

// LoadSecrets loads secrets from file without creating new ones.
// Returns nil if file does not exist.
func LoadSecrets(dataDir string) (*Secrets, error) {
	secretsPath := filepath.Join(dataDir, SecretsFile)
	normalizedPath, err := ensureManagedDirRoot(secretsPath, errSecretsFileSymlink, "secrets file", false)
	if err != nil {
		return nil, err
	}
	data, err := readRegisteredManagedFile(normalizedPath, errSecretsFileSymlink, "secrets file")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read secrets file: %w", err)
	}
	if err := ensureSecretsFilePermissions(normalizedPath); err != nil {
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
		return ErrSecretsNotFound
	}

	secrets.SetupShown = true
	return SaveSecrets(dataDir, secrets)
}

// LoadOrCreateSecrets loads secrets from file, creating them if they don't exist.
// Returns the secrets and a boolean indicating if they were newly created.
func LoadOrCreateSecrets(dataDir string) (*Secrets, bool, error) {
	secretsPath := filepath.Join(dataDir, SecretsFile)
	normalizedPath, err := ensureManagedDirRoot(secretsPath, errSecretsFileSymlink, "secrets file", false)
	if err != nil {
		return nil, false, err
	}

	// Try to load existing secrets
	data, err := readRegisteredManagedFile(normalizedPath, errSecretsFileSymlink, "secrets file")
	if err == nil {
		if err := ensureSecretsFilePermissions(normalizedPath); err != nil {
			return nil, false, err
		}
		var secrets Secrets
		if parseErr := json.Unmarshal(data, &secrets); parseErr != nil {
			if recoverErr := recoverCorruptSecretsFile(normalizedPath, parseErr); recoverErr != nil {
				return nil, false, errors.Join(
					fmt.Errorf("failed to parse secrets file: %w", parseErr),
					fmt.Errorf("recover corrupt secrets file: %w", recoverErr),
				)
			}
			err = os.ErrNotExist
		} else {
			return &secrets, false, nil
		}
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

	// Save secrets with restricted permissions
	secretsData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("failed to serialize secrets: %w", err)
	}

	if err := writeSecretsFile(normalizedPath, secretsData); err != nil {
		return nil, false, err
	}

	return secrets, true, nil
}

func recoverCorruptSecretsFile(secretsPath string, loadErr error) error {
	if !isRecoverableSecretsLoadError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", secretsPath, time.Now().UnixNano())
	if err := renameRegisteredSecretsFile(secretsPath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt secrets file: %w", err)
	}
	if err := syncRegisteredManagedDir(secretsPath, "secrets file"); err != nil {
		if rollbackErr := renameRegisteredSecretsFile(corruptPath, secretsPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt secrets directory: %w", err),
				fmt.Errorf("rollback corrupt secrets backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredManagedDir(secretsPath, "secrets file"); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt secrets directory: %w", err),
				fmt.Errorf("sync corrupt secrets rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt secrets directory: %w", err)
	}

	return nil
}

func isRecoverableSecretsLoadError(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}

	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

func renameRegisteredSecretsFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredManagedDirRoot(oldPath, "secrets file")
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeManagedFilePath(newPath, "secrets file")
	if err != nil {
		return err
	}
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("secrets rename requires same parent directory")
	}
	if err := validateManagedFilePath(normalizedOldPath, errSecretsFileSymlink, "secrets file"); err != nil {
		return err
	}
	if err := validateManagedFilePath(normalizedNewPath, errSecretsFileSymlink, "secrets file"); err != nil {
		return err
	}
	if ok {
		afterValidateManagedFilePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapManagedRootPathError(err, errSecretsFileSymlink)
		}
		return nil
	}
	return os.Rename(normalizedOldPath, normalizedNewPath)
}

func validateSecretsFilePath(secretsPath string) error {
	return validateManagedFilePath(secretsPath, errSecretsFileSymlink, "secrets file")
}

func writeSecretsFile(secretsPath string, data []byte) error {
	if err := writeRegisteredManagedFileAtomically(secretsPath, data, errSecretsFileSymlink, ".secrets-*.tmp", "secrets", secretsFileMode); err != nil {
		return err
	}
	return ensureSecretsFilePermissions(secretsPath)
}

func ensureSecretsFilePermissions(secretsPath string) error {
	if err := chmodRegisteredManagedFile(secretsPath, secretsFileMode, errSecretsFileSymlink, "secrets file"); err != nil {
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
