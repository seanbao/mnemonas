// Package auth provides user authentication and authorization for MnemoNAS
package auth

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

	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/bcrypt"
)

// Role defines user permission level
type Role string

const (
	RoleAdmin Role = "admin" // Full access, can manage users
	RoleUser  Role = "user"  // Normal user, can read/write own files
	RoleGuest Role = "guest" // Read-only access
)

// User represents a system user
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Email        string     `json:"email,omitempty"`
	PasswordHash string     `json:"password_hash"`
	Role         Role       `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	HomeDir      string     `json:"home_dir"`
	QuotaBytes   int64      `json:"quota_bytes"`
	UsedBytes    int64      `json:"used_bytes"`
}

// UserStore manages user persistence
type UserStore struct {
	mu       sync.RWMutex
	users    map[string]*User
	byName   map[string]*User
	filePath string
}

func cloneUser(user *User) *User {
	if user == nil {
		return nil
	}
	clone := *user
	if user.LastLoginAt != nil {
		lastLoginAt := *user.LastLoginAt
		clone.LastLoginAt = &lastLoginAt
	}
	return &clone
}

// Errors
var (
	ErrUserNotFound        = errors.New("user not found")
	ErrUserExists          = errors.New("user already exists")
	ErrUserDisabled        = errors.New("user is disabled")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrPasswordTooShort    = errors.New("password must be at least 8 characters")
	ErrLastAdmin           = errors.New("cannot delete last admin user")
	errUserStoreSymlink    = errors.New("users file path must not be a symlink")
	errPasswordFileSymlink = errors.New("initial password file path must not be a symlink")
)

// NewUserStore creates a new user store
// Returns the store, the initial admin password (if newly created), and any error
func NewUserStore(filePath string) (*UserStore, string, error) {
	store := &UserStore{
		users:    make(map[string]*User),
		byName:   make(map[string]*User),
		filePath: filePath,
	}

	if err := store.load(); err != nil {
		return nil, "", err
	}

	if len(store.users) == 0 {
		password, err := store.createDefaultAdmin()
		if err != nil {
			return nil, "", fmt.Errorf("failed to create default admin: %w", err)
		}
		return store, password, nil
	}

	return store, "", nil
}

func (s *UserStore) load() error {
	if err := validateAuthFilePath(s.filePath, errUserStoreSymlink); err != nil {
		return err
	}
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read users file: %w", err)
	}

	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("failed to parse users file: %w", err)
	}

	for _, u := range users {
		s.users[u.ID] = u
		s.byName[normalizeUsername(u.Username)] = u
	}

	return nil
}

func (s *UserStore) save() error {
	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize users: %w", err)
	}

	if err := writeAuthFileAtomically(s.filePath, data, errUserStoreSymlink, ".users-*.tmp", "users"); err != nil {
		return err
	}

	return nil
}

func validateAuthFilePath(path string, symlinkErr error) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat secure file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return symlinkErr
	}
	return nil
}

func writeAuthFileAtomically(path string, data []byte, symlinkErr error, pattern, label string) error {
	if err := validateAuthFilePath(path, symlinkErr); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("failed to create temp %s file: %w", label, err)
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
		return fmt.Errorf("failed to set temp %s permissions: %w", label, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write %s file: %w", label, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync %s file: %w", label, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp %s file: %w", label, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace %s file: %w", label, err)
	}
	cleanup = false

	return nil
}

func (s *UserStore) createDefaultAdmin() (string, error) {
	password := generateRandomPassword(16)

	admin := &User{
		ID:        generateID(),
		Username:  "admin",
		Role:      RoleAdmin,
		HomeDir:   "/",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	admin.PasswordHash = string(hash)

	// Write password to a secure file for user to retrieve
	passwordFile := filepath.Join(filepath.Dir(s.filePath), "initial-password.txt")
	passwordContent := fmt.Sprintf(`MnemoNAS Initial Admin Password
================================
Username: admin
Password: %s

Please change this password after first login!
This file will be automatically deleted after you login.
`, password)

	s.users[admin.ID] = admin
	s.byName[normalizeUsername(admin.Username)] = admin

	if err := s.save(); err != nil {
		delete(s.users, admin.ID)
		delete(s.byName, normalizeUsername(admin.Username))
		return "", err
	}

	if err := writeAuthFileAtomically(passwordFile, []byte(passwordContent), errPasswordFileSymlink, ".initial-password-*.tmp", "initial password"); err != nil {
		delete(s.users, admin.ID)
		delete(s.byName, normalizeUsername(admin.Username))
		if rollbackErr := s.save(); rollbackErr != nil {
			return "", fmt.Errorf("failed to write initial password file: %w (also failed to roll back default admin: %v)", err, rollbackErr)
		}
		return "", fmt.Errorf("failed to write initial password file: %w", err)
	}

	// Also print to terminal if running interactively (for convenience)
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		fmt.Printf("\n")
		fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
		fmt.Printf("║  Default admin account created                           ║\n")
		fmt.Printf("║  Username: admin                                         ║\n")
		fmt.Printf("║  Password: %-45s ║\n", password)
		fmt.Printf("║                                                          ║\n")
		fmt.Printf("║  Please change this password after first login!          ║\n")
		fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
		fmt.Printf("\n")
	} else {
		// When not interactive, just log the file location
		fmt.Printf("Initial admin password saved to: %s\n", passwordFile)
	}

	return password, nil
}

// GetByID retrieves a user by ID
func (s *UserStore) GetByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return cloneUser(user), nil
}

// GetByUsername retrieves a user by username (case-insensitive)
func (s *UserStore) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.byName[normalizeUsername(username)]
	if !ok {
		return nil, ErrUserNotFound
	}
	return cloneUser(user), nil
}

// Authenticate verifies username and password
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	s.mu.Lock()
	user, ok := s.byName[normalizeUsername(username)]
	if !ok {
		s.mu.Unlock()
		return nil, ErrInvalidCredentials
	}

	if user.Disabled {
		s.mu.Unlock()
		return nil, ErrUserDisabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.mu.Unlock()
		return nil, ErrInvalidCredentials
	}

	original := cloneUser(user)
	updated := cloneUser(user)
	now := time.Now()
	updated.LastLoginAt = &now
	s.users[user.ID] = updated
	s.byName[normalizeUsername(updated.Username)] = updated
	if err := s.save(); err != nil {
		s.users[user.ID] = original
		s.byName[normalizeUsername(original.Username)] = original
		updated = original
	}
	s.mu.Unlock()

	// Remove initial password file after successful login (if exists)
	passwordFile := filepath.Join(filepath.Dir(s.filePath), "initial-password.txt")
	os.Remove(passwordFile) // Ignore error - file may not exist

	return cloneUser(updated), nil
}

// Create creates a new user
func (s *UserStore) Create(username, password, email string, role Role) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byName[normalizeUsername(username)]; ok {
		return nil, ErrUserExists
	}

	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &User{
		ID:           generateID(),
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
		Role:         role,
		HomeDir:      "/" + username,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	s.users[user.ID] = user
	s.byName[normalizeUsername(username)] = user

	if err := s.save(); err != nil {
		delete(s.users, user.ID)
		delete(s.byName, normalizeUsername(username))
		return nil, err
	}

	return cloneUser(user), nil
}

// Update updates user information
func (s *UserStore) Update(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.users[user.ID]
	if !ok {
		return ErrUserNotFound
	}

	updated := cloneUser(user)
	updated.UpdatedAt = time.Now()
	oldName := normalizeUsername(existing.Username)
	newName := normalizeUsername(updated.Username)
	s.users[user.ID] = updated
	delete(s.byName, oldName)
	s.byName[newName] = updated
	if err := s.save(); err != nil {
		s.users[user.ID] = existing
		delete(s.byName, newName)
		s.byName[oldName] = existing
		return err
	}

	return nil
}

// ChangePassword changes a user's password
func (s *UserStore) ChangePassword(id, oldPassword, newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]
	if !ok {
		return ErrUserNotFound
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrInvalidCredentials
	}

	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	original := cloneUser(user)
	updated := cloneUser(user)
	updated.PasswordHash = string(hash)
	updated.UpdatedAt = time.Now()
	s.users[id] = updated
	s.byName[normalizeUsername(updated.Username)] = updated
	if err := s.save(); err != nil {
		s.users[id] = original
		s.byName[normalizeUsername(original.Username)] = original
		return err
	}

	return nil
}

// ResetPassword resets a user's password (admin only)
func (s *UserStore) ResetPassword(id, newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]
	if !ok {
		return ErrUserNotFound
	}

	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	original := cloneUser(user)
	updated := cloneUser(user)
	updated.PasswordHash = string(hash)
	updated.UpdatedAt = time.Now()
	s.users[id] = updated
	s.byName[normalizeUsername(updated.Username)] = updated
	if err := s.save(); err != nil {
		s.users[id] = original
		s.byName[normalizeUsername(original.Username)] = original
		return err
	}

	return nil
}

// Delete removes a user
func (s *UserStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]
	if !ok {
		return ErrUserNotFound
	}

	if user.Role == RoleAdmin {
		adminCount := 0
		for _, u := range s.users {
			if u.Role == RoleAdmin && !u.Disabled {
				adminCount++
			}
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}

	original := user
	delete(s.users, id)
	delete(s.byName, normalizeUsername(user.Username))
	if err := s.save(); err != nil {
		s.users[id] = original
		s.byName[normalizeUsername(original.Username)] = original
		return err
	}

	return nil
}

// List returns all users
func (s *UserStore) List() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, cloneUser(u))
	}
	return users
}

// Count returns the number of users
func (s *UserStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// Helper functions
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	b := make([]byte, length)
	rand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

func normalizeUsername(username string) string {
	result := make([]byte, len(username))
	for i := 0; i < len(username); i++ {
		c := username[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}
