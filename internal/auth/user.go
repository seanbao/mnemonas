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

// Errors
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUserExists         = errors.New("user already exists")
	ErrUserDisabled       = errors.New("user is disabled")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")
	ErrLastAdmin          = errors.New("cannot delete last admin user")
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

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

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

	s.users[admin.ID] = admin
	s.byName[normalizeUsername(admin.Username)] = admin

	if err := s.save(); err != nil {
		return "", err
	}

	// Write password to a secure file for user to retrieve
	passwordFile := filepath.Join(filepath.Dir(s.filePath), "initial-password.txt")
	passwordContent := fmt.Sprintf(`MnemoNAS Initial Admin Password
================================
Username: admin
Password: %s

Please change this password after first login!
This file will be automatically deleted after you login.
`, password)

	if err := os.WriteFile(passwordFile, []byte(passwordContent), 0600); err != nil {
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
	return user, nil
}

// GetByUsername retrieves a user by username (case-insensitive)
func (s *UserStore) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.byName[normalizeUsername(username)]
	if !ok {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// Authenticate verifies username and password
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	user, err := s.GetByUsername(username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if user.Disabled {
		return nil, ErrUserDisabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	s.mu.Lock()
	now := time.Now()
	user.LastLoginAt = &now
	s.save()
	s.mu.Unlock()

	// Remove initial password file after successful login (if exists)
	passwordFile := filepath.Join(filepath.Dir(s.filePath), "initial-password.txt")
	os.Remove(passwordFile) // Ignore error - file may not exist

	return user, nil
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

	return user, nil
}

// Update updates user information
func (s *UserStore) Update(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[user.ID]; !ok {
		return ErrUserNotFound
	}

	oldName := ""
	for name, u := range s.byName {
		if u.ID == user.ID {
			oldName = name
			break
		}
	}

	user.UpdatedAt = time.Now()
	s.users[user.ID] = user

	newName := normalizeUsername(user.Username)
	if oldName != newName {
		delete(s.byName, oldName)
		s.byName[newName] = user
	}

	return s.save()
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

	user.PasswordHash = string(hash)
	user.UpdatedAt = time.Now()

	return s.save()
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

	user.PasswordHash = string(hash)
	user.UpdatedAt = time.Now()

	return s.save()
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

	delete(s.users, id)
	delete(s.byName, normalizeUsername(user.Username))

	return s.save()
}

// List returns all users
func (s *UserStore) List() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
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
