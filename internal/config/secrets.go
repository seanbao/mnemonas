// Package config provides configuration management for MnemoNAS
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Secrets holds auto-generated secrets that persist across restarts
type Secrets struct {
	JWTSecret      string              `json:"jwt_secret"`
	WebDAVPassword string              `json:"webdav_password,omitempty"` // Auto-generated if not configured
	SetupLifecycle SetupLifecycleState `json:"setup_lifecycle,omitempty"`
}

// SetupLifecycleState records durable setup completion and deferral state.
type SetupLifecycleState struct {
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DeferredAt    *time.Time `json:"deferred_at,omitempty"`
	DeferredUntil *time.Time `json:"deferred_until,omitempty"`
}

// SecretsFile is the default filename for secrets
const SecretsFile = "secrets.json"

const secretsFileMode = 0600

const (
	readablePasswordLowerChars = "abcdefghjkmnpqrstuvwxyz"
	readablePasswordUpperChars = "ABCDEFGHJKMNPQRSTUVWXYZ"
	readablePasswordDigitChars = "23456789"
	readablePasswordChars      = readablePasswordLowerChars + readablePasswordUpperChars + readablePasswordDigitChars
)

var errSecretsFileSymlink = errors.New("secrets file path must not be a symlink")
var secretsMu sync.Mutex

// ErrSecretsNotFound indicates that the runtime secrets file is unexpectedly missing.
var ErrSecretsNotFound = errors.New("secrets file not found")

// LoadSecrets loads secrets from file without creating new ones.
// Returns nil if file does not exist.
func LoadSecrets(dataDir string) (*Secrets, error) {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	return loadSecrets(dataDir)
}

func loadSecrets(dataDir string) (*Secrets, error) {
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
	if err := scrubLegacyWebPassword(normalizedPath, data, &secrets); err != nil {
		return nil, err
	}
	return &secrets, nil
}

// SaveSecrets saves secrets to file
func SaveSecrets(dataDir string, secrets *Secrets) error {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	return saveSecrets(dataDir, secrets)
}

func saveSecrets(dataDir string, secrets *Secrets) error {
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

// CompleteSetup records the first completion time and clears any deferral.
// Lifecycle updates are serialized with all secrets file operations.
func CompleteSetup(dataDir string, now time.Time) (SetupLifecycleState, error) {
	secretsMu.Lock()
	defer secretsMu.Unlock()

	secrets, err := loadSecrets(dataDir)
	if err != nil {
		return SetupLifecycleState{}, err
	}
	if secrets == nil {
		return SetupLifecycleState{}, ErrSecretsNotFound
	}

	if secrets.SetupLifecycle.CompletedAt == nil {
		completedAt := now
		secrets.SetupLifecycle.CompletedAt = &completedAt
	}
	secrets.SetupLifecycle.DeferredAt = nil
	secrets.SetupLifecycle.DeferredUntil = nil
	if err := saveSecrets(dataDir, secrets); err != nil {
		return SetupLifecycleState{}, err
	}
	return cloneSetupLifecycleState(secrets.SetupLifecycle), nil
}

// DeferSetup records a new setup deferral unless setup is already complete.
// Lifecycle updates are serialized with all secrets file operations.
func DeferSetup(dataDir string, now, until time.Time) (SetupLifecycleState, error) {
	secretsMu.Lock()
	defer secretsMu.Unlock()

	secrets, err := loadSecrets(dataDir)
	if err != nil {
		return SetupLifecycleState{}, err
	}
	if secrets == nil {
		return SetupLifecycleState{}, ErrSecretsNotFound
	}
	if secrets.SetupLifecycle.CompletedAt != nil {
		return cloneSetupLifecycleState(secrets.SetupLifecycle), nil
	}

	deferredAt := now
	deferredUntil := until
	secrets.SetupLifecycle.DeferredAt = &deferredAt
	secrets.SetupLifecycle.DeferredUntil = &deferredUntil
	if err := saveSecrets(dataDir, secrets); err != nil {
		return SetupLifecycleState{}, err
	}
	return cloneSetupLifecycleState(secrets.SetupLifecycle), nil
}

func cloneSetupLifecycleState(state SetupLifecycleState) SetupLifecycleState {
	clone := state
	if state.CompletedAt != nil {
		completedAt := *state.CompletedAt
		clone.CompletedAt = &completedAt
	}
	if state.DeferredAt != nil {
		deferredAt := *state.DeferredAt
		clone.DeferredAt = &deferredAt
	}
	if state.DeferredUntil != nil {
		deferredUntil := *state.DeferredUntil
		clone.DeferredUntil = &deferredUntil
	}
	return clone
}

// LoadOrCreateSecrets loads secrets from file, creating them if they don't exist.
// Returns the secrets and a boolean indicating if they were newly created.
func LoadOrCreateSecrets(dataDir string) (*Secrets, bool, error) {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	return loadOrCreateSecrets(dataDir)
}

func loadOrCreateSecrets(dataDir string) (*Secrets, bool, error) {
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
			if err := repairLoadedGeneratedSecrets(normalizedPath, data, &secrets); err != nil {
				return nil, false, err
			}
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

func scrubLegacyWebPassword(secretsPath string, data []byte, secrets *Secrets) error {
	if !hasLegacyWebPassword(data) {
		return nil
	}
	return writeScrubbedSecretsFile(secretsPath, secrets)
}

func repairLoadedGeneratedSecrets(secretsPath string, data []byte, secrets *Secrets) error {
	changed := hasLegacyWebPassword(data)
	repaired, err := repairRequiredGeneratedSecrets(secrets)
	if err != nil {
		return err
	}
	if !changed && !repaired {
		return nil
	}
	return writeScrubbedSecretsFile(secretsPath, secrets)
}

func repairRequiredGeneratedSecrets(secrets *Secrets) (bool, error) {
	changed := false
	if strings.TrimSpace(secrets.JWTSecret) == "" {
		jwtSecret, err := generateSecureKey(32)
		if err != nil {
			return false, err
		}
		secrets.JWTSecret = jwtSecret
		changed = true
	}
	if strings.TrimSpace(secrets.WebDAVPassword) == "" {
		webdavPassword, err := generateReadablePassword(16)
		if err != nil {
			return false, err
		}
		secrets.WebDAVPassword = webdavPassword
		changed = true
	}
	return changed, nil
}

func writeScrubbedSecretsFile(secretsPath string, secrets *Secrets) error {
	cleanData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize scrubbed secrets: %w", err)
	}
	if err := writeSecretsFile(secretsPath, cleanData); err != nil {
		return fmt.Errorf("failed to repair secrets file: %w", err)
	}
	return nil
}

func hasLegacyWebPassword(data []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["web_password"]
	return ok
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
	if ok {
		afterValidateManagedFilePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapManagedRootPathError(err, errSecretsFileSymlink)
		}
		return nil
	}
	if err := validateManagedFilePath(normalizedOldPath, errSecretsFileSymlink, "secrets file"); err != nil {
		return err
	}
	if err := validateManagedFilePath(normalizedNewPath, errSecretsFileSymlink, "secrets file"); err != nil {
		return err
	}
	normalizedOldPath, _, _, err = ensureManagedDirRootWithState(normalizedOldPath, errSecretsFileSymlink, "secrets file", false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredManagedDirRoot(normalizedOldPath, "secrets file")
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateManagedFilePath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapManagedRootPathError(err, errSecretsFileSymlink)
	}
	return nil
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
// Uses lowercase, uppercase, and digits while excluding ambiguous characters.
func generateReadablePassword(length int) (string, error) {
	b := make([]byte, 0, length)
	if length >= 3 {
		for _, charset := range []string{
			readablePasswordLowerChars,
			readablePasswordUpperChars,
			readablePasswordDigitChars,
		} {
			ch, err := randomReadablePasswordByte(charset)
			if err != nil {
				return "", err
			}
			b = append(b, ch)
		}
	}
	for len(b) < length {
		ch, err := randomReadablePasswordByte(readablePasswordChars)
		if err != nil {
			return "", err
		}
		b = append(b, ch)
	}

	if err := shuffleReadablePassword(b); err != nil {
		return "", err
	}

	return string(b), nil
}

func randomReadablePasswordByte(charset string) (byte, error) {
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
	if err != nil {
		return 0, fmt.Errorf("failed to generate random password: %w", err)
	}
	return charset[index.Int64()], nil
}

func shuffleReadablePassword(password []byte) error {
	for i := len(password) - 1; i > 0; i-- {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return fmt.Errorf("failed to shuffle random password: %w", err)
		}
		j := int(index.Int64())
		password[i], password[j] = password[j], password[i]
	}
	return nil
}
