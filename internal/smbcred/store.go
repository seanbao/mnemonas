// Package smbcred stores SMB-specific password material.
package smbcred

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/seanbao/mnemonas/internal/rootio"
	"golang.org/x/crypto/md4"
)

const credentialFileMode os.FileMode = 0600

// ErrUnsafePath means the credential path resolves through an unsafe path component.
var ErrUnsafePath = errors.New("unsafe SMB credential path")

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
	data, err := readCredentialFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read SMB credential file: %w", mapCredentialPathError(err))
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
	if err := ensureCredentialDir(filepath.Dir(s.filePath)); err != nil {
		return fmt.Errorf("failed to create SMB credential directory: %w", err)
	}

	data, err := json.MarshalIndent(credentialFile{Credentials: s.snapshotLocked()}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode SMB credentials: %w", err)
	}
	data = append(data, '\n')

	dirRoot, err := os.OpenRoot(filepath.Dir(s.filePath))
	if err != nil {
		return fmt.Errorf("failed to open SMB credential directory: %w", mapCredentialPathError(err))
	}
	defer dirRoot.Close()

	file, tmpName, err := createCredentialTempFile(dirRoot)
	if err != nil {
		return fmt.Errorf("failed to create temporary SMB credential file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = dirRoot.Remove(tmpName)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write temporary SMB credential file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to sync temporary SMB credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close temporary SMB credential file: %w", err)
	}
	if err := dirRoot.Rename(tmpName, filepath.Base(s.filePath)); err != nil {
		return fmt.Errorf("failed to replace SMB credential file: %w", mapCredentialPathError(err))
	}
	cleanup = false
	if err := syncCredentialDir(dirRoot); err != nil {
		return fmt.Errorf("failed to sync SMB credential directory: %w", err)
	}
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

func readCredentialFile(filePath string) ([]byte, error) {
	file, err := rootio.OpenFilePathNoFollow(filePath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

func ensureCredentialDir(dir string) error {
	if _, err := rootio.MkdirAllPathNoFollowTracked(dir, 0700); err != nil {
		return mapCredentialPathError(err)
	}
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return mapCredentialPathError(err)
	}
	defer dirHandle.Close()
	if err := dirHandle.Chmod(0700); err != nil {
		return err
	}
	return nil
}

func createCredentialTempFile(root *os.Root) (*os.File, string, error) {
	for range 32 {
		tmpName, err := tempCredentialName()
		if err != nil {
			return nil, "", err
		}
		file, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, credentialFileMode)
		if err == nil {
			return file, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapCredentialPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temporary SMB credential file")
}

func tempCredentialName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("failed to generate temporary SMB credential file name: %w", err)
	}
	return ".smb-credentials-" + hex.EncodeToString(random[:]) + ".tmp", nil
}

func syncCredentialDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()
	return dirHandle.Sync()
}

func mapCredentialPathError(err error) error {
	if err == nil {
		return nil
	}
	if rootio.IsSymlinkError(err) {
		return fmt.Errorf("%w: path must not contain symlink components", ErrUnsafePath)
	}
	return err
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
