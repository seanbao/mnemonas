// Package smbcred stores SMB-specific password material.
package smbcred

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

const credentialFileMode os.FileMode = 0600

// Credential is the stored SMB authentication material for one MnemoNAS user.
type Credential struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username,omitempty"`
	Enabled   bool      `json:"enabled"`
	NTHashHex string    `json:"nt_hash_hex"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type credentialFile struct {
	Credentials []Credential `json:"credentials"`
}

// Store persists SMB credentials in a JSON file under .mnemonas.
type Store struct {
	mu         sync.RWMutex
	filePath   string
	byUserID   map[string]Credential
	byUsername map[string]string
}

// NewStore opens or creates an in-memory store backed by filePath.
func NewStore(filePath string) (*Store, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("SMB credential file path cannot be empty")
	}
	store := &Store{
		filePath:   filePath,
		byUserID:   map[string]Credential{},
		byUsername: map[string]string{},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Path returns the backing file path.
func (s *Store) Path() string {
	return s.filePath
}

// SetPassword hashes password as an NT hash and stores it for userID.
func (s *Store) SetPassword(userID, username, password string, enabled bool) (Credential, error) {
	if password == "" {
		return Credential{}, errors.New("SMB password cannot be empty")
	}
	return s.SetCredential(userID, username, NTHashHex(password), enabled)
}

// SetCredential stores an already-derived NT hash for userID.
func (s *Store) SetCredential(userID, username, ntHashHex string, enabled bool) (Credential, error) {
	userID = strings.TrimSpace(userID)
	username = strings.TrimSpace(username)
	normalizedHash, err := NormalizeNTHashHex(ntHashHex)
	if err != nil {
		return Credential{}, err
	}
	if userID == "" {
		return Credential{}, errors.New("SMB credential user ID cannot be empty")
	}
	if strings.IndexFunc(userID, isInvalidNameRune) >= 0 {
		return Credential{}, fmt.Errorf("invalid SMB credential user ID: %q", userID)
	}
	if username != "" && strings.IndexFunc(username, isInvalidNameRune) >= 0 {
		return Credential{}, fmt.Errorf("invalid SMB credential username: %q", username)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	credential, ok := s.byUserID[userID]
	if !ok {
		credential = Credential{
			UserID:    userID,
			CreatedAt: now,
		}
	}
	credential.Username = username
	credential.Enabled = enabled
	credential.NTHashHex = normalizedHash
	credential.UpdatedAt = now

	s.byUserID[userID] = credential
	s.rebuildUsernameIndexLocked()
	if err := s.saveLocked(); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

// Disable marks a user's SMB credential inactive without deleting the hash.
func (s *Store) Disable(userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("SMB credential user ID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	credential, ok := s.byUserID[userID]
	if !ok {
		return nil
	}
	credential.Enabled = false
	credential.UpdatedAt = time.Now().UTC()
	s.byUserID[userID] = credential
	if err := s.saveLocked(); err != nil {
		return err
	}
	return nil
}

// GetByUserID returns one credential by MnemoNAS user ID.
func (s *Store) GetByUserID(userID string) (Credential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	credential, ok := s.byUserID[strings.TrimSpace(userID)]
	return credential, ok
}

// GetByUsername returns one credential by case-insensitive username.
func (s *Store) GetByUsername(username string) (Credential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID, ok := s.byUsername[usernameKey(username)]
	if !ok {
		return Credential{}, false
	}
	credential, ok := s.byUserID[userID]
	return credential, ok
}

// List returns all credentials sorted by user ID.
func (s *Store) List() []Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.snapshotLocked()
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read SMB credential file: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	var payload credentialFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("failed to parse SMB credential file: %w", err)
	}
	for _, credential := range payload.Credentials {
		if credential.UserID == "" {
			return errors.New("SMB credential file contains an empty user ID")
		}
		normalizedHash, err := NormalizeNTHashHex(credential.NTHashHex)
		if err != nil {
			return fmt.Errorf("invalid SMB credential hash for user %q: %w", credential.UserID, err)
		}
		credential.NTHashHex = normalizedHash
		s.byUserID[credential.UserID] = credential
	}
	s.rebuildUsernameIndexLocked()
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0700); err != nil {
		return fmt.Errorf("failed to create SMB credential directory: %w", err)
	}

	data, err := json.MarshalIndent(credentialFile{Credentials: s.snapshotLocked()}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode SMB credentials: %w", err)
	}
	data = append(data, '\n')

	tmpPath, err := tempCredentialPath(s.filePath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, credentialFileMode)
	if err != nil {
		return fmt.Errorf("failed to create temporary SMB credential file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write temporary SMB credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close temporary SMB credential file: %w", err)
	}
	if err := os.Rename(tmpPath, s.filePath); err != nil {
		return fmt.Errorf("failed to replace SMB credential file: %w", err)
	}
	cleanup = false
	return nil
}

func (s *Store) snapshotLocked() []Credential {
	credentials := make([]Credential, 0, len(s.byUserID))
	for _, credential := range s.byUserID {
		credentials = append(credentials, credential)
	}
	sort.Slice(credentials, func(i, j int) bool {
		return credentials[i].UserID < credentials[j].UserID
	})
	return credentials
}

func (s *Store) rebuildUsernameIndexLocked() {
	s.byUsername = map[string]string{}
	for userID, credential := range s.byUserID {
		if credential.Username == "" {
			continue
		}
		s.byUsername[usernameKey(credential.Username)] = userID
	}
}

func tempCredentialPath(filePath string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("failed to generate temporary SMB credential file name: %w", err)
	}
	return filepath.Join(filepath.Dir(filePath), ".smb-credentials-"+hex.EncodeToString(random[:])+".tmp"), nil
}

func usernameKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func isInvalidNameRune(r rune) bool {
	return r <= 0x20 || r == 0x7f
}

// NTHashHex derives the NT hash used by NTLM from an SMB password.
func NTHashHex(password string) string {
	codeUnits := utf16.Encode([]rune(password))
	data := make([]byte, 0, len(codeUnits)*2)
	for _, codeUnit := range codeUnits {
		data = append(data, byte(codeUnit), byte(codeUnit>>8))
	}
	digest := md4.New()
	_, _ = digest.Write(data)
	return hex.EncodeToString(digest.Sum(nil))
}

// NormalizeNTHashHex validates and lowercases a 16-byte NT hash.
func NormalizeNTHashHex(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("SMB NT hash cannot be empty")
	}
	if trimmed != value {
		return "", errors.New("SMB NT hash must not contain leading or trailing whitespace")
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil || len(decoded) != 16 {
		return "", errors.New("SMB NT hash must be 32 hexadecimal characters")
	}
	return strings.ToLower(trimmed), nil
}
