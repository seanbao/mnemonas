// Package auth provides user authentication and authorization for MnemoNAS
package auth

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
	"unicode"

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
	writeMu  sync.Mutex
	users    map[string]*User
	byName   map[string]*User
	filePath string
	version  uint64
}

var userStoreWriter = func(path string, data []byte) error {
	return writeAuthFileAtomically(path, data, errUserStoreSymlink, ".users-*.tmp", "users")
}
var syncAuthFileDir = syncAuthDir
var userRandomRead = rand.Read

type userStoreSnapshot struct {
	users    map[string]*User
	byName   map[string]*User
	filePath string
	version  uint64
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
	ErrInvalidUsername     = errors.New("invalid username")
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
		if recoverErr := store.recoverCorruptUsers(err); recoverErr != nil {
			return nil, "", errors.Join(
				fmt.Errorf("load users: %w", err),
				fmt.Errorf("recover corrupt users: %w", recoverErr),
			)
		}
	}

	if !store.hasEnabledAdmin() {
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

	loadedUsers := make(map[string]*User, len(users))
	loadedByName := make(map[string]*User, len(users))
	for i, u := range users {
		if u == nil {
			return fmt.Errorf("users file contains null entry at index %d", i)
		}
		if u.ID == "" {
			return fmt.Errorf("users file contains user with empty id at index %d", i)
		}
		normalizedUsername := normalizeUsername(u.Username)
		if normalizedUsername == "" {
			return fmt.Errorf("users file contains user with empty username at index %d", i)
		}
		if _, exists := loadedUsers[u.ID]; exists {
			return fmt.Errorf("users file contains duplicate user id %q", u.ID)
		}
		if existing, exists := loadedByName[normalizedUsername]; exists {
			return fmt.Errorf("users file contains duplicate username %q conflicting with %q", u.Username, existing.Username)
		}

		loaded := cloneUser(u)
		loadedUsers[loaded.ID] = loaded
		loadedByName[normalizedUsername] = loaded
	}

	s.users = loadedUsers
	s.byName = loadedByName

	return nil
}

func (s *UserStore) recoverCorruptUsers(loadErr error) error {
	if !isRecoverableUserLoadError(loadErr) {
		return loadErr
	}

	dir := filepath.Dir(s.filePath)
	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.filePath, time.Now().UnixNano())
	if err := os.Rename(s.filePath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt users file: %w", err)
	}
	if err := syncAuthFileDir(dir); err != nil {
		if rollbackErr := os.Rename(corruptPath, s.filePath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt users directory: %w", err),
				fmt.Errorf("rollback corrupt users backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncAuthFileDir(dir); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt users directory: %w", err),
				fmt.Errorf("sync corrupt users rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt users directory: %w", err)
	}

	s.users = make(map[string]*User)
	s.byName = make(map[string]*User)
	return nil
}

func isRecoverableUserLoadError(err error) bool {
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

func (s *UserStore) save() error {
	return saveUserState(s.filePath, s.users)
}

func saveUserState(filePath string, users map[string]*User) error {
	serializedUsers := make([]*User, 0, len(users))
	for _, user := range users {
		serializedUsers = append(serializedUsers, cloneUser(user))
	}

	data, err := json.MarshalIndent(serializedUsers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize users: %w", err)
	}

	if err := userStoreWriter(filePath, data); err != nil {
		return err
	}

	return nil
}

func cloneUserMap(users map[string]*User) map[string]*User {
	cloned := make(map[string]*User, len(users))
	for id, user := range users {
		cloned[id] = cloneUser(user)
	}
	return cloned
}

func buildUserNameIndex(users map[string]*User) map[string]*User {
	byName := make(map[string]*User, len(users))
	for _, user := range users {
		byName[normalizeUsername(user.Username)] = user
	}
	return byName
}

func (s *UserStore) snapshotState() userStoreSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := cloneUserMap(s.users)
	return userStoreSnapshot{
		users:    users,
		byName:   buildUserNameIndex(users),
		filePath: s.filePath,
		version:  s.version,
	}
}

func (s *UserStore) commitSnapshot(snapshot userStoreSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != snapshot.version {
		return false
	}

	s.users = snapshot.users
	s.byName = snapshot.byName
	s.version++
	return true
}

func validateAuthFilePath(path string, symlinkErr error) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return fmt.Errorf("failed to resolve secure file path: %w", err)
		}
		cleaned = absPath
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
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

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat secure file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return symlinkErr
		}
	}
	return nil
}

func writeAuthFileAtomically(path string, data []byte, symlinkErr error, pattern, label string) error {
	if err := validateAuthFilePath(path, symlinkErr); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := ensureAuthDir(dir, 0755, label); err != nil {
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
	if err := syncAuthFileDir(dir); err != nil {
		return fmt.Errorf("failed to sync %s directory: %w", label, err)
	}

	return nil
}

func collectMissingAuthDirs(dir string) ([]string, error) {
	missing := make([]string, 0)
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return missing, nil
}

func syncCreatedAuthDirs(createdDirs []string, label string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncAuthFileDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync %s directory tree: %w", label, err)
		}
	}
	return nil
}

func ensureAuthDir(dir string, perm os.FileMode, label string) error {
	createdDirs, err := collectMissingAuthDirs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return syncCreatedAuthDirs(createdDirs, label)
}

func syncAuthDir(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func (s *UserStore) hasEnabledAdmin() bool {
	for _, user := range s.users {
		if user.Role == RoleAdmin && !user.Disabled {
			return true
		}
	}
	return false
}

func (s *UserStore) bootstrapAdminUsername() string {
	const recoveryPrefix = "admin-recovery"

	if _, exists := s.byName[normalizeUsername("admin")]; !exists {
		return "admin"
	}

	candidate := recoveryPrefix
	for suffix := 2; ; suffix++ {
		if _, exists := s.byName[normalizeUsername(candidate)]; !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", recoveryPrefix, suffix)
	}
}

func (s *UserStore) createDefaultAdmin() (string, error) {
	password, err := generateRandomPassword(16)
	if err != nil {
		return "", fmt.Errorf("generate default admin password: %w", err)
	}
	adminID, err := generateID()
	if err != nil {
		return "", fmt.Errorf("generate default admin ID: %w", err)
	}
	username := s.bootstrapAdminUsername()

	admin := &User{
		ID:        adminID,
		Username:  username,
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
Username: %s
Password: %s

Please change this password after first login!
This file will be automatically deleted after you login.
`, username, password)

	if err := writeAuthFileAtomically(passwordFile, []byte(passwordContent), errPasswordFileSymlink, ".initial-password-*.tmp", "initial password"); err != nil {
		return "", fmt.Errorf("failed to write initial password file: %w", err)
	}

	s.users[admin.ID] = admin
	s.byName[normalizeUsername(admin.Username)] = admin

	if err := s.save(); err != nil {
		delete(s.users, admin.ID)
		delete(s.byName, normalizeUsername(admin.Username))
		if removeErr := os.Remove(passwordFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return "", fmt.Errorf("failed to save default admin: %w (also failed to remove initial password file: %v)", err, removeErr)
		}
		return "", err
	}

	// Also print to terminal if running interactively (for convenience)
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		fmt.Printf("\n")
		fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
		fmt.Printf("║  Default admin account created                           ║\n")
		fmt.Printf("║  Username: %-45s ║\n", username)
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
	normalizedUsername := normalizeUsername(username)
	var authenticatedUser *User
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.byName[normalizedUsername]
		if !ok {
			return nil, ErrInvalidCredentials
		}

		if user.Disabled {
			return nil, ErrUserDisabled
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
			return nil, ErrInvalidCredentials
		}

		if err := removeInitialPasswordFileForUser(snapshot.filePath, user.Username); err != nil {
			return nil, fmt.Errorf("failed to remove initial password file: %w", err)
		}

		updated := cloneUser(user)
		now := time.Now()
		updated.LastLoginAt = &now
		snapshot.users[user.ID] = updated
		snapshot.byName[normalizedUsername] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			authenticatedUser = cloneUser(updated)
			break
		}
		if s.commitSnapshot(snapshot) {
			authenticatedUser = cloneUser(updated)
			break
		}
	}

	return authenticatedUser, nil
}

func removeInitialPasswordFileForUser(usersFilePath, username string) error {
	passwordFile := filepath.Join(filepath.Dir(usersFilePath), "initial-password.txt")
	content, err := os.ReadFile(passwordFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	matchedUsername := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "Username: "+username {
			matchedUsername = true
			break
		}
	}
	if !matchedUsername {
		return nil
	}

	if err := os.Remove(passwordFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Create creates a new user
func (s *UserStore) Create(username, password, email string, role Role) (*User, error) {
	cleanUsername, err := normalizeNewUsername(username)
	if err != nil {
		return nil, err
	}

	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	userID, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generate user ID: %w", err)
	}

	user := &User{
		ID:           userID,
		Username:     cleanUsername,
		Email:        email,
		PasswordHash: string(hash),
		Role:         role,
		HomeDir:      "/" + cleanUsername,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	normalizedUsername := normalizeUsername(cleanUsername)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if _, ok := snapshot.byName[normalizedUsername]; ok {
			return nil, ErrUserExists
		}

		snapshot.users[user.ID] = cloneUser(user)
		snapshot.byName[normalizedUsername] = snapshot.users[user.ID]

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return cloneUser(user), nil
		}
	}
}

// Update updates user information
func (s *UserStore) Update(user *User) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		existing, ok := snapshot.users[user.ID]
		if !ok {
			return ErrUserNotFound
		}

		updated := cloneUser(user)
		updated.UpdatedAt = time.Now()
		oldName := normalizeUsername(existing.Username)
		newName := normalizeUsername(updated.Username)
		snapshot.users[user.ID] = updated
		delete(snapshot.byName, oldName)
		snapshot.byName[newName] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// ChangePassword changes a user's password
func (s *UserStore) ChangePassword(id, oldPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.users[id]
		if !ok {
			return ErrUserNotFound
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
			return ErrInvalidCredentials
		}

		updated := cloneUser(user)
		updated.PasswordHash = string(hash)
		updated.UpdatedAt = time.Now()
		snapshot.users[id] = updated
		snapshot.byName[normalizeUsername(updated.Username)] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// ResetPassword resets a user's password (admin only)
func (s *UserStore) ResetPassword(id, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.users[id]
		if !ok {
			return ErrUserNotFound
		}

		updated := cloneUser(user)
		updated.PasswordHash = string(hash)
		updated.UpdatedAt = time.Now()
		snapshot.users[id] = updated
		snapshot.byName[normalizeUsername(updated.Username)] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// Delete removes a user
func (s *UserStore) Delete(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.users[id]
		if !ok {
			return ErrUserNotFound
		}

		if user.Role == RoleAdmin {
			adminCount := 0
			for _, candidate := range snapshot.users {
				if candidate.Role == RoleAdmin && !candidate.Disabled {
					adminCount++
				}
			}
			if adminCount <= 1 {
				return ErrLastAdmin
			}
		}

		delete(snapshot.users, id)
		delete(snapshot.byName, normalizeUsername(user.Username))

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// List returns all users
func (s *UserStore) List() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, cloneUser(u))
	}
	sortUsersForList(users)
	return users
}

func sortUsersForList(users []*User) {
	sort.Slice(users, func(i, j int) bool {
		left := normalizeUsername(users[i].Username)
		right := normalizeUsername(users[j].Username)
		if left != right {
			return left < right
		}
		if users[i].Username != users[j].Username {
			return users[i].Username < users[j].Username
		}
		return users[i].ID < users[j].ID
	})
}

// Count returns the number of users
func (s *UserStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// Helper functions
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := userRandomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	b := make([]byte, length)
	if _, err := userRandomRead(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}

func normalizeNewUsername(username string) (string, error) {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", ErrInvalidUsername
	}
	if strings.ContainsAny(trimmed, "/\\") {
		return "", ErrInvalidUsername
	}
	if strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return "", ErrInvalidUsername
	}
	return trimmed, nil
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
