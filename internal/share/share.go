// Package share provides file sharing functionality for MnemoNAS
package share

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrShareNotFound     = errors.New("share not found")
	ErrShareExpired      = errors.New("share has expired")
	ErrShareAccessLimit  = errors.New("share access limit reached")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrShareDisabled     = errors.New("share is disabled")
	errShareStoreSymlink = errors.New("share store path must not be a symlink")
)

// ShareType represents the type of shared resource
type ShareType string

const (
	ShareTypeFile   ShareType = "file"
	ShareTypeFolder ShareType = "folder"
)

// Permission represents sharing permissions
type Permission string

const (
	PermissionRead      Permission = "read"
	PermissionReadWrite Permission = "read_write"
)

// Share represents a shared file or folder
type Share struct {
	ID           string     `json:"id"`
	Path         string     `json:"path"`
	Type         ShareType  `json:"type"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	PasswordHash string     `json:"password_hash"`
	Permission   Permission `json:"permission"`
	Enabled      bool       `json:"enabled"`
	AccessCount  int64      `json:"access_count"`
	MaxAccess    int64      `json:"max_access"`
	LastAccess   *time.Time `json:"last_access"`
	Description  string     `json:"description"`
}

// IsExpired checks if the share has expired
func (s *Share) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*s.ExpiresAt)
}

// HasPassword checks if the share requires a password
func (s *Share) HasPassword() bool {
	return s.PasswordHash != ""
}

// CheckPassword verifies the provided password
func (s *Share) CheckPassword(password string) bool {
	if !s.HasPassword() {
		return true
	}
	err := bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(password))
	return err == nil
}

// IsAccessLimitReached checks if max access count is reached
func (s *Share) IsAccessLimitReached() bool {
	if s.MaxAccess == 0 {
		return false
	}
	return s.AccessCount >= s.MaxAccess
}

// CanAccess checks if the share can be accessed
func (s *Share) CanAccess() error {
	if !s.Enabled {
		return ErrShareDisabled
	}
	if s.IsExpired() {
		return ErrShareExpired
	}
	if s.IsAccessLimitReached() {
		return ErrShareAccessLimit
	}
	return nil
}

// ShareStore manages share persistence
type ShareStore struct {
	mu       sync.RWMutex
	shares   map[string]*Share
	pathIdx  map[string][]string
	filePath string
}

type authorizedAccessReservation struct {
	id                 string
	currentLastAccess  time.Time
	previousLastAccess *time.Time
}

// NewShareStore creates a new share store
func NewShareStore(filePath string) (*ShareStore, error) {
	store := &ShareStore{
		shares:   make(map[string]*Share),
		pathIdx:  make(map[string][]string),
		filePath: filePath,
	}

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load shares: %w", err)
	}

	return store, nil
}

func (s *ShareStore) load() error {
	if err := validateShareStorePath(s.filePath); err != nil {
		return err
	}
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var shares []*Share
	if err := json.Unmarshal(data, &shares); err != nil {
		return fmt.Errorf("failed to parse shares file: %w", err)
	}

	s.shares = make(map[string]*Share)
	s.pathIdx = make(map[string][]string)

	for _, share := range shares {
		s.shares[share.ID] = share
		s.pathIdx[share.Path] = append(s.pathIdx[share.Path], share.ID)
	}

	return nil
}

func (s *ShareStore) save() error {
	shares := make([]*Share, 0, len(s.shares))
	for _, share := range s.shares {
		shares = append(shares, share)
	}

	data, err := json.MarshalIndent(shares, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize shares: %w", err)
	}

	if err := writeShareStoreFile(s.filePath, data); err != nil {
		return err
	}

	return nil
}

func validateShareStorePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat share store: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errShareStoreSymlink
	}
	return nil
}

func writeShareStoreFile(path string, data []byte) error {
	if err := validateShareStorePath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".shares-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp shares file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to set temp shares permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write shares file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync shares file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp shares file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace shares file: %w", err)
	}
	cleanup = false

	return nil
}

// CreateShareOptions contains options for creating a share
type CreateShareOptions struct {
	Path        string
	Type        ShareType
	CreatedBy   string
	ExpiresIn   *time.Duration
	Password    string
	Permission  Permission
	MaxAccess   int64
	Description string
}

// Create creates a new share
func (s *ShareStore) Create(opts CreateShareOptions) (*Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := generateShareID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate share ID: %w", err)
	}

	share := &Share{
		ID:          id,
		Path:        opts.Path,
		Type:        opts.Type,
		CreatedBy:   opts.CreatedBy,
		CreatedAt:   time.Now(),
		Permission:  opts.Permission,
		Enabled:     true,
		MaxAccess:   opts.MaxAccess,
		Description: opts.Description,
	}

	if opts.ExpiresIn != nil {
		exp := time.Now().Add(*opts.ExpiresIn)
		share.ExpiresAt = &exp
	}

	if opts.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(opts.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
		share.PasswordHash = string(hash)
	}

	if share.Permission == "" {
		share.Permission = PermissionRead
	}

	s.shares[id] = share
	s.pathIdx[opts.Path] = append(s.pathIdx[opts.Path], id)

	if err := s.save(); err != nil {
		delete(s.shares, id)
		ids := s.pathIdx[opts.Path]
		for i, sid := range ids {
			if sid == id {
				s.pathIdx[opts.Path] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(s.pathIdx[opts.Path]) == 0 {
			delete(s.pathIdx, opts.Path)
		}
		return nil, err
	}

	return copyShare(share), nil
}

// Get retrieves a share by ID
func (s *ShareStore) Get(id string) (*Share, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	share, ok := s.shares[id]
	if !ok {
		return nil, ErrShareNotFound
	}

	return copyShare(share), nil
}

// GetByPath retrieves all shares for a path
func (s *ShareStore) GetByPath(path string) []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.pathIdx[path]
	shares := make([]*Share, 0, len(ids))

	for _, id := range ids {
		if share, ok := s.shares[id]; ok {
			shares = append(shares, copyShare(share))
		}
	}

	return shares
}

// ListByUser lists all shares created by a user
func (s *ShareStore) ListByUser(userID string) []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var shares []*Share
	for _, share := range s.shares {
		if share.CreatedBy == userID {
			shares = append(shares, copyShare(share))
		}
	}

	return shares
}

// ListAll lists all shares
func (s *ShareStore) ListAll() []*Share {
	s.mu.RLock()
	defer s.mu.RUnlock()

	shares := make([]*Share, 0, len(s.shares))
	for _, share := range s.shares {
		shares = append(shares, copyShare(share))
	}

	return shares
}

// Update updates a share
func (s *ShareStore) Update(id string, fn func(*Share) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.shares[id]
	if !ok {
		return ErrShareNotFound
	}

	prev := copyShare(share)
	updated := copyShare(share)
	if err := fn(updated); err != nil {
		return err
	}

	oldPath := prev.Path
	newPath := updated.Path
	prevOldIDs := append([]string(nil), s.pathIdx[oldPath]...)
	prevNewIDs := append([]string(nil), s.pathIdx[newPath]...)
	s.shares[id] = updated
	if oldPath != newPath {
		ids := s.pathIdx[oldPath]
		for i, sid := range ids {
			if sid == id {
				s.pathIdx[oldPath] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(s.pathIdx[oldPath]) == 0 {
			delete(s.pathIdx, oldPath)
		}
		s.pathIdx[newPath] = append(s.pathIdx[newPath], id)
	}

	if err := s.save(); err != nil {
		s.shares[id] = prev
		if oldPath != newPath {
			if len(prevOldIDs) == 0 {
				delete(s.pathIdx, oldPath)
			} else {
				s.pathIdx[oldPath] = prevOldIDs
			}
			if len(prevNewIDs) == 0 {
				delete(s.pathIdx, newPath)
			} else {
				s.pathIdx[newPath] = prevNewIDs
			}
		}
		return err
	}

	return nil
}

// Delete deletes a share
func (s *ShareStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.shares[id]
	if !ok {
		return ErrShareNotFound
	}

	prevShare := copyShare(share)
	prevIDs := append([]string(nil), s.pathIdx[share.Path]...)

	ids := s.pathIdx[share.Path]
	for i, sid := range ids {
		if sid == id {
			s.pathIdx[share.Path] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(s.pathIdx[share.Path]) == 0 {
		delete(s.pathIdx, share.Path)
	}

	delete(s.shares, id)

	if err := s.save(); err != nil {
		s.shares[id] = prevShare
		if len(prevIDs) == 0 {
			delete(s.pathIdx, prevShare.Path)
		} else {
			s.pathIdx[prevShare.Path] = prevIDs
		}
		return err
	}

	return nil
}

// RecordAccess records an access to the share
func (s *ShareStore) RecordAccess(id string) error {
	_, err := s.RecordAuthorizedAccess(id)
	return err
}

// RecordAuthorizedAccess validates access constraints and records an access atomically.
func (s *ShareStore) RecordAuthorizedAccess(id string) (*Share, error) {
	share, _, err := s.reserveAuthorizedAccess(id)
	if err != nil {
		return nil, err
	}
	return share, nil
}

func (s *ShareStore) reserveAuthorizedAccess(id string) (*Share, *authorizedAccessReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.shares[id]
	if !ok {
		return nil, nil, ErrShareNotFound
	}

	if err := share.CanAccess(); err != nil {
		return nil, nil, err
	}

	prev := copyShare(share)
	share.AccessCount++
	now := time.Now()
	share.LastAccess = &now

	if err := s.save(); err != nil {
		*share = *prev
		return nil, nil, err
	}

	return copyShare(share), &authorizedAccessReservation{
		id:                 id,
		currentLastAccess:  now,
		previousLastAccess: cloneTimePtr(prev.LastAccess),
	}, nil
}

func (s *ShareStore) rollbackAuthorizedAccess(reservation *authorizedAccessReservation) error {
	if reservation == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.shares[reservation.id]
	if !ok {
		return ErrShareNotFound
	}
	if share.AccessCount == 0 {
		return nil
	}

	prev := copyShare(share)
	share.AccessCount--
	if share.LastAccess != nil && share.LastAccess.Equal(reservation.currentLastAccess) {
		share.LastAccess = cloneTimePtr(reservation.previousLastAccess)
	}

	if err := s.save(); err != nil {
		*share = *prev
		return err
	}

	return nil
}

// Access validates and records access to a share
func (s *ShareStore) Access(id string, password string) (*Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, ok := s.shares[id]
	if !ok {
		return nil, ErrShareNotFound
	}

	if err := share.CanAccess(); err != nil {
		return nil, err
	}

	if share.HasPassword() && !share.CheckPassword(password) {
		return nil, ErrInvalidPassword
	}

	prev := copyShare(share)
	share.AccessCount++
	now := time.Now()
	share.LastAccess = &now

	if err := s.save(); err != nil {
		*share = *prev
		return nil, err
	}

	return copyShare(share), nil
}

func copyShare(share *Share) *Share {
	if share == nil {
		return nil
	}

	copy := *share
	if share.ExpiresAt != nil {
		expiresAt := *share.ExpiresAt
		copy.ExpiresAt = &expiresAt
	}
	if share.LastAccess != nil {
		lastAccess := *share.LastAccess
		copy.LastAccess = &lastAccess
	}

	return &copy
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func generateShareID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ShareInfo is a safe representation of a share for API responses
type ShareInfo struct {
	ID          string     `json:"id"`
	Path        string     `json:"path"`
	Type        ShareType  `json:"type"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	HasPassword bool       `json:"has_password"`
	Permission  Permission `json:"permission"`
	Enabled     bool       `json:"enabled"`
	AccessCount int64      `json:"access_count"`
	MaxAccess   int64      `json:"max_access"`
	LastAccess  *time.Time `json:"last_access"`
	Description string     `json:"description"`
	URL         string     `json:"url,omitempty"`
}

// ToInfo converts a Share to ShareInfo
func (s *Share) ToInfo() *ShareInfo {
	return &ShareInfo{
		ID:          s.ID,
		Path:        s.Path,
		Type:        s.Type,
		CreatedBy:   s.CreatedBy,
		CreatedAt:   s.CreatedAt,
		ExpiresAt:   s.ExpiresAt,
		HasPassword: s.HasPassword(),
		Permission:  s.Permission,
		Enabled:     s.Enabled,
		AccessCount: s.AccessCount,
		MaxAccess:   s.MaxAccess,
		LastAccess:  s.LastAccess,
		Description: s.Description,
	}
}
