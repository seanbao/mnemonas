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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/bcrypt"

	"github.com/seanbao/mnemonas/internal/rootio"
)

// Role defines user permission level
type Role string

const (
	RoleAdmin Role = "admin" // Full access, can manage users
	RoleUser  Role = "user"  // Normal user, can read/write own files
	RoleGuest Role = "guest" // Read-only access
)

const printInitialPasswordEnv = "MNEMONAS_PRINT_INITIAL_PASSWORD"

const (
	minPasswordLength      = 8
	maxPasswordBytes       = 72
	maxUsernameRuneCount   = 255
	userStoreSchemaVersion = 1
)

// User represents a system user
type User struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	Email              string     `json:"email,omitempty"`
	PasswordHash       string     `json:"password_hash"`
	Role               Role       `json:"role"`
	Groups             []string   `json:"groups,omitempty"`
	Disabled           bool       `json:"disabled"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	MustChangePassword bool       `json:"must_change_password"`
	PasswordChangedAt  *time.Time `json:"password_changed_at,omitempty"`
	CredentialVersion  uint64     `json:"credential_version"`
	HomeDir            string     `json:"home_dir"`
	QuotaBytes         int64      `json:"quota_bytes"`
	UsedBytes          int64      `json:"used_bytes"`
}

type persistedUserStore struct {
	SchemaVersion int     `json:"schema_version"`
	Users         []*User `json:"users"`
}

// UserPatch contains administrator-managed user fields that can be updated atomically.
type UserPatch struct {
	Email      *string
	Role       *Role
	Groups     *[]string
	HomeDir    *string
	QuotaBytes *int64
	Disabled   *bool
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
	return writeRegisteredAuthFileAtomically(path, data, errUserStoreSymlink, ".users-*.tmp", "users")
}
var syncAuthFileDir = syncAuthDir
var syncAuthRootDir = syncManagedAuthDir
var userRandomRead = rand.Read
var authFileRandomRead = rand.Read
var afterValidateAuthFilePath = func() {}

var authDirRootsMu sync.RWMutex
var authDirRoots = make(map[string]*os.Root)

// PersistenceWarningError reports that the auth mutation is already visible on
// disk, but the final directory fsync did not complete.
type PersistenceWarningError struct {
	err error
}

func (e *PersistenceWarningError) Error() string {
	return e.err.Error()
}

func (e *PersistenceWarningError) Unwrap() error {
	return e.err
}

func WrapPersistenceWarning(err error) error {
	if err == nil {
		return nil
	}
	var warning *PersistenceWarningError
	if errors.As(err, &warning) {
		return err
	}
	return &PersistenceWarningError{err: err}
}

func wrapAuthPersistenceWarning(err error) error {
	return WrapPersistenceWarning(err)
}

func isAuthPersistenceWarning(err error) bool {
	var warning *PersistenceWarningError
	return errors.As(err, &warning)
}

// IsPersistenceWarning reports whether err means the auth state was written but
// a durability sync step could not be confirmed.
func IsPersistenceWarning(err error) bool {
	return isAuthPersistenceWarning(err)
}

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
	clone.Groups = append([]string(nil), user.Groups...)
	if user.LastLoginAt != nil {
		lastLoginAt := *user.LastLoginAt
		clone.LastLoginAt = &lastLoginAt
	}
	if user.PasswordChangedAt != nil {
		passwordChangedAt := *user.PasswordChangedAt
		clone.PasswordChangedAt = &passwordChangedAt
	}
	return &clone
}

// Errors
var (
	ErrUserNotFound           = errors.New("user not found")
	ErrUserExists             = errors.New("user already exists")
	ErrUserDisabled           = errors.New("user is disabled")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrInvalidUsername        = errors.New("invalid username")
	ErrInvalidRole            = errors.New("invalid role")
	ErrPasswordTooShort       = errors.New("password must contain at least 8 UTF-8 bytes and not be whitespace-only")
	ErrPasswordTooLong        = errors.New("password must be at most 72 bytes")
	ErrPasswordChangeRequired = errors.New("password change required")
	ErrPasswordUnchanged      = errors.New("new password must differ from current password")
	ErrLastAdmin              = errors.New("cannot delete last admin user")
	errInvalidUserHomeDir     = errors.New("invalid home_dir")
	errInvalidUserGroups      = errors.New("invalid groups")
	errInvalidQuotaBytes      = errors.New("invalid quota_bytes")
	errUserStoreSymlink       = errors.New("users file path must not be a symlink")
	errPasswordFileSymlink    = errors.New("initial password file path must not be a symlink")
	errUserStoreSchema        = errors.New("invalid users file schema")
)

const authRootEscapeError = "path escapes from parent"

// NewUserStore creates a new user store.
// It returns the initial admin password when a bootstrap admin is created. If
// the returned error is a persistence warning, the returned store and password
// remain usable but the caller should surface the durability warning.
func NewUserStore(filePath string) (*UserStore, string, error) {
	normalizedPath, err := ensureAuthDirRoot(filePath, errUserStoreSymlink, "users")
	if err != nil {
		return nil, "", err
	}

	store := &UserStore{
		users:    make(map[string]*User),
		byName:   make(map[string]*User),
		filePath: normalizedPath,
	}

	if err := store.load(); err != nil {
		return nil, "", fmt.Errorf("load users: %w", err)
	}

	if !store.hasEnabledAdmin() {
		password, err := store.createDefaultAdmin()
		if err != nil {
			if isAuthPersistenceWarning(err) {
				return store, password, err
			}
			return nil, "", fmt.Errorf("failed to create default admin: %w", err)
		}
		return store, password, nil
	}

	return store, "", nil
}

func (s *UserStore) load() error {
	data, err := readRegisteredAuthFile(s.filePath, errUserStoreSymlink)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read users file: %w", err)
	}

	users, err := decodePersistedUserStore(data)
	if err != nil {
		return err
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
		cleanUsername, err := normalizeNewUsername(u.Username)
		if err != nil {
			return fmt.Errorf("users file contains invalid username at index %d: %w", i, err)
		}
		if err := validateRole(u.Role); err != nil {
			return fmt.Errorf("users file contains invalid role for user %q: %w", cleanUsername, err)
		}
		normalizedUsername := normalizeUsername(cleanUsername)
		if _, exists := loadedUsers[u.ID]; exists {
			return fmt.Errorf("users file contains duplicate user id %q", u.ID)
		}
		if existing, exists := loadedByName[normalizedUsername]; exists {
			return fmt.Errorf("users file contains duplicate username %q conflicting with %q", u.Username, existing.Username)
		}
		homeDir, err := normalizeHomeDir(u.HomeDir)
		if err != nil {
			return fmt.Errorf("users file contains invalid home_dir for user %q: %w", u.Username, err)
		}
		if err := validateRoleHomeDir(u.Role, homeDir); err != nil {
			return fmt.Errorf("users file contains invalid role/home_dir combination for user %q: %w", u.Username, err)
		}
		if u.QuotaBytes < 0 {
			return fmt.Errorf("users file contains invalid quota_bytes for user %q: %w", u.Username, errInvalidQuotaBytes)
		}
		if _, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil {
			return fmt.Errorf("users file contains invalid password_hash for user %q: %w", u.Username, err)
		}
		groups, err := normalizeGroupNames(u.Groups)
		if err != nil {
			return fmt.Errorf("users file contains invalid groups for user %q: %w", u.Username, err)
		}

		loaded := cloneUser(u)
		loaded.Username = cleanUsername
		loaded.HomeDir = homeDir
		loaded.Groups = groups
		loadedUsers[loaded.ID] = loaded
		loadedByName[normalizedUsername] = loaded
	}

	s.users = loadedUsers
	s.byName = loadedByName

	return nil
}

func decodePersistedUserStore(data []byte) ([]*User, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return nil, fmt.Errorf("failed to parse users file: %w", err)
		}
		return nil, fmt.Errorf("%w: users file must be a versioned JSON object with schema_version %d", errUserStoreSchema, userStoreSchemaVersion)
	}

	versionData, ok := document["schema_version"]
	if !ok || len(versionData) == 0 || strings.TrimSpace(string(versionData)) == "null" {
		return nil, fmt.Errorf("%w: users file is missing schema_version; expected %d", errUserStoreSchema, userStoreSchemaVersion)
	}
	var version int
	if err := json.Unmarshal(versionData, &version); err != nil {
		return nil, fmt.Errorf("%w: users file schema_version must be an integer; expected %d", errUserStoreSchema, userStoreSchemaVersion)
	}
	switch {
	case version < userStoreSchemaVersion:
		return nil, fmt.Errorf("%w: users file schema_version %d is obsolete; expected %d", errUserStoreSchema, version, userStoreSchemaVersion)
	case version > userStoreSchemaVersion:
		return nil, fmt.Errorf("%w: users file schema_version %d is unsupported; expected %d", errUserStoreSchema, version, userStoreSchemaVersion)
	}

	usersData, ok := document["users"]
	if !ok || len(usersData) == 0 || strings.TrimSpace(string(usersData)) == "null" {
		return nil, fmt.Errorf("%w: users file is missing users array", errUserStoreSchema)
	}
	var rawUsers []json.RawMessage
	if err := json.Unmarshal(usersData, &rawUsers); err != nil {
		return nil, fmt.Errorf("%w: users must be an array: %v", errUserStoreSchema, err)
	}

	users := make([]*User, 0, len(rawUsers))
	for index, rawUser := range rawUsers {
		if strings.TrimSpace(string(rawUser)) == "null" {
			users = append(users, nil)
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawUser, &fields); err != nil {
			return nil, fmt.Errorf("%w: user at index %d must be an object: %v", errUserStoreSchema, index, err)
		}
		for _, field := range []string{"must_change_password", "credential_version"} {
			value, ok := fields[field]
			if !ok || len(value) == 0 || strings.TrimSpace(string(value)) == "null" {
				return nil, fmt.Errorf("%w: user at index %d is missing required field %q", errUserStoreSchema, index, field)
			}
		}

		var user User
		if err := json.Unmarshal(rawUser, &user); err != nil {
			return nil, fmt.Errorf("%w: invalid user at index %d: %v", errUserStoreSchema, index, err)
		}
		if user.CredentialVersion == 0 {
			return nil, fmt.Errorf("%w: user at index %d has invalid credential_version 0", errUserStoreSchema, index)
		}
		users = append(users, &user)
	}
	return users, nil
}

func (s *UserStore) save() error {
	return saveUserState(s.filePath, s.users)
}

func saveUserState(filePath string, users map[string]*User) error {
	serializedUsers := make([]*User, 0, len(users))
	for _, user := range users {
		serializedUsers = append(serializedUsers, cloneUser(user))
	}
	sortUsersForList(serializedUsers)

	data, err := json.MarshalIndent(persistedUserStore{
		SchemaVersion: userStoreSchemaVersion,
		Users:         serializedUsers,
	}, "", "  ")
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

func enabledAdminCount(users map[string]*User) int {
	count := 0
	for _, user := range users {
		if user.Role == RoleAdmin && !user.Disabled {
			count++
		}
	}
	return count
}

func isEnabledAdmin(user *User) bool {
	return user != nil && user.Role == RoleAdmin && !user.Disabled
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

func (s *UserStore) commitSnapshotOnPersistenceWarning(snapshot userStoreSnapshot, err error) bool {
	if !isAuthPersistenceWarning(err) {
		return false
	}
	return s.commitSnapshot(snapshot)
}

func validateAuthFilePath(path string, symlinkErr error) error {
	cleaned, err := normalizeAuthFilePath(path)
	if err != nil {
		return err
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

func normalizeAuthFilePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve secure file path: %w", err)
	}
	return absPath, nil
}

func ensureAuthDirRoot(path string, symlinkErr error, label string) (string, error) {
	normalizedPath, _, _, err := ensureAuthDirRootWithState(path, symlinkErr, label, true)
	return normalizedPath, err
}

func ensureAuthDirRootWithState(path string, symlinkErr error, label string, create bool) (string, *os.Root, []string, error) {
	normalizedPath, err := normalizeAuthFilePath(path)
	if err != nil {
		return "", nil, nil, err
	}
	if err := validateAuthFilePath(normalizedPath, symlinkErr); err != nil {
		return "", nil, nil, err
	}

	dir := filepath.Dir(normalizedPath)
	authDirRootsMu.RLock()
	root := authDirRoots[dir]
	authDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil, nil
	}

	createdDirs := []string(nil)
	if create {
		var err error
		createdDirs, err = ensureAuthDir(dir, 0700, symlinkErr, label)
		if err != nil {
			return "", nil, createdDirs, fmt.Errorf("failed to create directory: %w", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil, nil
		}
		return "", nil, nil, fmt.Errorf("failed to stat %s directory: %w", label, err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, createdDirs, fmt.Errorf("failed to open %s directory root: %w", label, err)
	}

	authDirRootsMu.Lock()
	if existing := authDirRoots[dir]; existing != nil {
		authDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, createdDirs, nil
	}
	authDirRoots[dir] = root
	authDirRootsMu.Unlock()

	return normalizedPath, root, createdDirs, nil
}

func releaseRegisteredAuthDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	authDirRootsMu.Lock()
	if authDirRoots[dir] == root {
		delete(authDirRoots, dir)
	}
	authDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredAuthDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeAuthFilePath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	authDirRootsMu.RLock()
	root := authDirRoots[dir]
	authDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredAuthFile(path string, symlinkErr error) ([]byte, error) {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		normalizedPath, _, _, err = ensureAuthDirRootWithState(normalizedPath, symlinkErr, "auth", false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredAuthDirRoot(normalizedPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
	}
	return readAuthFileWithRoot(root, normalizedPath, symlinkErr)
}

func writeRegisteredAuthFileAtomically(path string, data []byte, symlinkErr error, pattern, label string) error {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return writeAuthFileAtomicallyWithRoot(root, normalizedPath, data, symlinkErr, pattern, label)
	}
	if err := validateAuthFilePath(normalizedPath, symlinkErr); err != nil {
		return err
	}
	registeredRoot := (*os.Root)(nil)
	releaseRootOnError := false
	createdDirs := []string(nil)
	normalizedPath, registeredRoot, createdDirs, err = ensureAuthDirRootWithState(normalizedPath, symlinkErr, label, true)
	if err != nil {
		releaseRegisteredAuthDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedAuthDirs(createdDirs, label, err)
	}
	if registeredRoot != nil {
		releaseRootOnError = true
	} else {
		root, normalizedPath, ok, err = registeredAuthDirRoot(normalizedPath)
		if err != nil {
			return err
		}
		if !ok {
			return &os.PathError{Op: "open", Path: filepath.Dir(normalizedPath), Err: os.ErrNotExist}
		}
		registeredRoot = root
	}
	if err := writeAuthFileAtomicallyWithRoot(registeredRoot, normalizedPath, data, symlinkErr, pattern, label); err != nil {
		if releaseRootOnError {
			releaseRegisteredAuthDirRoot(filepath.Dir(normalizedPath), registeredRoot)
			return cleanupCreatedAuthDirs(createdDirs, label, err)
		}
		return err
	}
	return nil
}

func renameRegisteredAuthFile(oldPath, newPath string, symlinkErr error) error {
	root, normalizedOldPath, ok, err := registeredAuthDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeAuthFilePath(newPath)
	if err != nil {
		return err
	}
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("auth file rename requires same parent directory")
	}
	if ok {
		afterValidateAuthFilePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapAuthRootPathError(err, symlinkErr)
		}
		return nil
	}
	if err := validateAuthFilePath(normalizedOldPath, symlinkErr); err != nil {
		return err
	}
	if err := validateAuthFilePath(normalizedNewPath, symlinkErr); err != nil {
		return err
	}
	normalizedOldPath, _, _, err = ensureAuthDirRootWithState(normalizedOldPath, symlinkErr, "auth", false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredAuthDirRoot(normalizedOldPath)
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateAuthFilePath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapAuthRootPathError(err, symlinkErr)
	}
	return nil
}

func removeRegisteredAuthFile(path string, symlinkErr error) error {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if err := validateAuthFilePath(normalizedPath, symlinkErr); err != nil {
		return err
	}
	afterValidateAuthFilePath()
	if !ok {
		normalizedPath, _, _, err = ensureAuthDirRootWithState(normalizedPath, symlinkErr, "auth", false)
		if err != nil {
			return err
		}
		root, normalizedPath, ok, err = registeredAuthDirRoot(normalizedPath)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	if err := root.Remove(filepath.Base(normalizedPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return mapAuthRootPathError(err, symlinkErr)
	}
	return nil
}

func syncRegisteredAuthDir(path string) error {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncAuthRootDir(root)
	}
	return syncAuthFileDir(filepath.Dir(normalizedPath))
}

func readAuthFileWithRoot(root *os.Root, path string, symlinkErr error) ([]byte, error) {
	if err := validateAuthFilePath(path, symlinkErr); err != nil {
		return nil, err
	}
	afterValidateAuthFilePath()

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapAuthRootPathError(err, symlinkErr)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeAuthFileAtomicallyWithRoot(root *os.Root, path string, data []byte, symlinkErr error, pattern, label string) error {
	if err := validateAuthFilePath(path, symlinkErr); err != nil {
		return err
	}
	afterValidateAuthFilePath()

	tmpFile, tmpName, err := createAuthTempFile(root, pattern, symlinkErr)
	if err != nil {
		return fmt.Errorf("failed to create temp %s file: %w", label, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("failed to set temp %s permissions: %w", label, err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("failed to write %s file: %w", label, err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("failed to sync %s file: %w", label, err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("failed to close temp %s file: %w", label, err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("failed to replace %s file: %w", label, mapAuthRootPathError(err, symlinkErr)))
	}
	cleanup = false
	if err := syncAuthRootDir(root); err != nil {
		return wrapAuthPersistenceWarning(fmt.Errorf("failed to sync %s directory: %w", label, err))
	}

	return nil
}

func newAuthTempName(pattern string) (string, error) {
	var suffix [8]byte
	if _, err := authFileRandomRead(suffix[:]); err != nil {
		return "", err
	}
	randomPart := hex.EncodeToString(suffix[:])
	if strings.Contains(pattern, "*") {
		return strings.Replace(pattern, "*", randomPart, 1), nil
	}
	return pattern + randomPart, nil
}

func createAuthTempFile(root *os.Root, pattern string, symlinkErr error) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newAuthTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapAuthRootPathError(err, symlinkErr)
	}

	return nil, "", errors.New("failed to allocate unique temp file")
}

func cleanupAuthTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func cleanupCreatedAuthDirs(createdDirs []string, label string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created %s directory %s: %w", label, dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncManagedAuthDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isAuthRootEscapeError(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	return pathErr.Err != nil && pathErr.Err.Error() == authRootEscapeError
}

func mapAuthRootPathError(err error, symlinkErr error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isAuthRootEscapeError(err) {
		return symlinkErr
	}
	return err
}

func syncCreatedAuthDirs(createdDirs []string, label string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncAuthFileDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync %s directory tree: %w", label, err)
		}
	}
	return nil
}

func ensureAuthDir(dir string, perm os.FileMode, symlinkErr error, label string) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, symlinkErr
		}
		return createdDirs, err
	}
	return createdDirs, syncCreatedAuthDirs(createdDirs, label)
}

func syncAuthDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
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
		ID:                 adminID,
		Username:           username,
		Role:               RoleAdmin,
		HomeDir:            "/",
		MustChangePassword: true,
		CredentialVersion:  1,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
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
This file will be automatically deleted after you change this password.
`, username, password)

	var persistenceWarning error
	if err := writeRegisteredAuthFileAtomically(passwordFile, []byte(passwordContent), errPasswordFileSymlink, ".initial-password-*.tmp", "initial password"); err != nil {
		if isAuthPersistenceWarning(err) {
			persistenceWarning = fmt.Errorf("initial password persistence warning: %w", err)
		} else {
			return "", fmt.Errorf("failed to write initial password file: %w", err)
		}
	}

	s.users[admin.ID] = admin
	s.byName[normalizeUsername(admin.Username)] = admin

	if err := s.save(); err != nil {
		if isAuthPersistenceWarning(err) {
			return password, errors.Join(persistenceWarning, fmt.Errorf("default admin persistence warning: %w", err))
		}
		delete(s.users, admin.ID)
		delete(s.byName, normalizeUsername(admin.Username))
		if removeErr := removeRegisteredAuthFile(passwordFile, errPasswordFileSymlink); removeErr != nil {
			return "", fmt.Errorf("failed to save default admin: %w (also failed to remove initial password file: %v)", err, removeErr)
		}
		return "", err
	}

	isInteractive := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	if isInteractive && shouldPrintInitialPasswordToTerminal() {
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
		fmt.Printf("Initial admin password saved to: %s\n", passwordFile)
		if isInteractive {
			fmt.Printf("Set %s=1 before first run to also print it in the terminal.\n", printInitialPasswordEnv)
		}
	}

	return password, persistenceWarning
}

func shouldPrintInitialPasswordToTerminal() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(printInitialPasswordEnv))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
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

// VerifyCredentials verifies username and password without mutating login state.
func (s *UserStore) VerifyCredentials(username, password string) (*User, error) {
	s.mu.RLock()
	user, ok := s.byName[normalizeUsername(username)]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrInvalidCredentials
	}
	user = cloneUser(user)
	s.mu.RUnlock()

	if user.Disabled {
		return nil, ErrUserDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	if user.MustChangePassword {
		return nil, ErrPasswordChangeRequired
	}
	return user, nil
}

// Authenticate verifies username and password
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	normalizedUsername := normalizeUsername(username)
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

		updated := cloneUser(user)
		now := time.Now()
		updated.LastLoginAt = &now
		snapshot.users[user.ID] = updated
		snapshot.byName[normalizedUsername] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return cloneUser(updated), err
			}
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return cloneUser(updated), nil
		}
	}
}

type initialPasswordFileRemoval struct {
	path    string
	content []byte
	removed bool
}

func (r *initialPasswordFileRemoval) restore() error {
	if r == nil || !r.removed {
		return nil
	}
	return writeRegisteredAuthFileAtomically(r.path, r.content, errPasswordFileSymlink, ".initial-password-*.tmp", "initial password")
}

func initialPasswordRollbackError(commitErr, restoreErr error) error {
	if restoreErr == nil {
		return commitErr
	}
	// The commit failure remains authoritative. A durability warning while
	// restoring the bootstrap file must not reclassify an uncommitted password
	// mutation as successful.
	return fmt.Errorf("%w; restore initial password file: %v", commitErr, restoreErr)
}

func removeInitialPasswordFileForUser(usersFilePath, username string) (*initialPasswordFileRemoval, error) {
	passwordFile := filepath.Join(filepath.Dir(usersFilePath), "initial-password.txt")
	content, err := readRegisteredAuthFile(passwordFile, errPasswordFileSymlink)
	if os.IsNotExist(err) {
		return &initialPasswordFileRemoval{path: passwordFile}, nil
	}
	if err != nil {
		return nil, err
	}

	matchedUsername := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "Username: "+username {
			matchedUsername = true
			break
		}
	}
	if !matchedUsername {
		return &initialPasswordFileRemoval{path: passwordFile}, nil
	}

	if err := removeRegisteredAuthFile(passwordFile, errPasswordFileSymlink); err != nil {
		return nil, err
	}
	removal := &initialPasswordFileRemoval{
		path:    passwordFile,
		content: append([]byte(nil), content...),
		removed: true,
	}
	if err := syncRegisteredAuthDir(passwordFile); err != nil {
		return removal, wrapAuthPersistenceWarning(fmt.Errorf("failed to sync initial password directory: %w", err))
	}
	return removal, nil
}

// Create creates a new user
func (s *UserStore) Create(username, password, email string, role Role) (*User, error) {
	return s.CreateWithGroups(username, password, email, role, nil)
}

// CreateUserOptions contains optional metadata for creating a user.
type CreateUserOptions struct {
	Groups     []string
	HomeDir    *string
	QuotaBytes int64
}

// CreateWithGroups creates a new user with group memberships.
func (s *UserStore) CreateWithGroups(username, password, email string, role Role, groups []string) (*User, error) {
	return s.CreateWithOptions(username, password, email, role, CreateUserOptions{Groups: groups})
}

// CreateWithOptions creates a new user with optional metadata.
func (s *UserStore) CreateWithOptions(username, password, email string, role Role, options CreateUserOptions) (*User, error) {
	cleanUsername, err := normalizeNewUsername(username)
	if err != nil {
		return nil, err
	}
	if err := validateRole(role); err != nil {
		return nil, err
	}
	normalizedGroups, err := normalizeGroupNames(options.Groups)
	if err != nil {
		return nil, err
	}
	homeDir := "/" + cleanUsername
	if options.HomeDir != nil {
		homeDir, err = normalizeHomeDir(*options.HomeDir)
		if err != nil {
			return nil, err
		}
	}
	if err := validateRoleHomeDir(role, homeDir); err != nil {
		return nil, err
	}
	if options.QuotaBytes < 0 {
		return nil, errInvalidQuotaBytes
	}

	if err := validateNewPassword(password); err != nil {
		return nil, err
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
		ID:                userID,
		Username:          cleanUsername,
		Email:             email,
		PasswordHash:      string(hash),
		Role:              role,
		Groups:            normalizedGroups,
		HomeDir:           homeDir,
		QuotaBytes:        options.QuotaBytes,
		CredentialVersion: 1,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
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
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return cloneUser(user), err
			}
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
		updated.PasswordHash = existing.PasswordHash
		updated.MustChangePassword = existing.MustChangePassword
		updated.CredentialVersion = existing.CredentialVersion
		updated.PasswordChangedAt = nil
		if existing.PasswordChangedAt != nil {
			passwordChangedAt := *existing.PasswordChangedAt
			updated.PasswordChangedAt = &passwordChangedAt
		}
		updated.LastLoginAt = nil
		if existing.LastLoginAt != nil {
			lastLoginAt := *existing.LastLoginAt
			updated.LastLoginAt = &lastLoginAt
		}
		updated.CreatedAt = existing.CreatedAt
		cleanUsername, err := normalizeNewUsername(updated.Username)
		if err != nil {
			return err
		}
		updated.Username = cleanUsername
		if err := validateRole(updated.Role); err != nil {
			return err
		}
		groups, err := normalizeGroupNames(updated.Groups)
		if err != nil {
			return err
		}
		updated.Groups = groups
		homeDir, err := normalizeHomeDir(updated.HomeDir)
		if err != nil {
			return err
		}
		if isEnabledAdmin(existing) && !isEnabledAdmin(updated) && enabledAdminCount(snapshot.users) <= 1 {
			return ErrLastAdmin
		}
		if err := validateRoleHomeDir(updated.Role, homeDir); err != nil {
			return err
		}
		updated.HomeDir = homeDir
		if updated.QuotaBytes < 0 {
			return errInvalidQuotaBytes
		}
		updated.UpdatedAt = time.Now()
		oldName := normalizeUsername(existing.Username)
		newName := normalizeUsername(updated.Username)
		if other, ok := snapshot.byName[newName]; ok && other.ID != user.ID {
			return ErrUserExists
		}
		snapshot.users[user.ID] = updated
		delete(snapshot.byName, oldName)
		snapshot.byName[newName] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return err
			}
			return err
		}
		if s.commitSnapshot(snapshot) {
			return nil
		}
	}
}

// Patch applies only explicitly provided administrator-managed fields to the
// latest stored user record.
func (s *UserStore) Patch(id string, patch UserPatch) (*User, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		existing, ok := snapshot.users[id]
		if !ok {
			return nil, ErrUserNotFound
		}

		updated := cloneUser(existing)
		if patch.Email != nil {
			updated.Email = strings.TrimSpace(*patch.Email)
		}
		if patch.Role != nil {
			updated.Role = *patch.Role
		}
		if patch.Groups != nil {
			updated.Groups = append([]string(nil), (*patch.Groups)...)
		}
		if patch.HomeDir != nil {
			updated.HomeDir = *patch.HomeDir
		}
		if patch.QuotaBytes != nil {
			updated.QuotaBytes = *patch.QuotaBytes
		}
		if patch.Disabled != nil {
			updated.Disabled = *patch.Disabled
		}

		if err := validateRole(updated.Role); err != nil {
			return nil, err
		}
		groups, err := normalizeGroupNames(updated.Groups)
		if err != nil {
			return nil, err
		}
		updated.Groups = groups
		homeDir, err := normalizeHomeDir(updated.HomeDir)
		if err != nil {
			return nil, err
		}
		if isEnabledAdmin(existing) && !isEnabledAdmin(updated) && enabledAdminCount(snapshot.users) <= 1 {
			return nil, ErrLastAdmin
		}
		if err := validateRoleHomeDir(updated.Role, homeDir); err != nil {
			return nil, err
		}
		updated.HomeDir = homeDir
		if updated.QuotaBytes < 0 {
			return nil, errInvalidQuotaBytes
		}
		updated.UpdatedAt = time.Now()
		snapshot.users[id] = updated
		snapshot.byName[normalizeUsername(updated.Username)] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return cloneUser(updated), err
			}
			return nil, err
		}
		if s.commitSnapshot(snapshot) {
			return cloneUser(updated), nil
		}
	}
}

// ChangePassword changes a user's password
func (s *UserStore) ChangePassword(id, oldPassword, newPassword string) error {
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var initialPasswordWarning error

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.users[id]
		if !ok {
			return ErrUserNotFound
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
			return ErrInvalidCredentials
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(newPassword)); err == nil {
			return ErrPasswordUnchanged
		}
		initialPasswordRemoval, err := removeInitialPasswordFileForUser(snapshot.filePath, user.Username)
		if err != nil && !isAuthPersistenceWarning(err) {
			return fmt.Errorf("failed to remove initial password file: %w", err)
		}
		if isAuthPersistenceWarning(err) {
			initialPasswordWarning = errors.Join(initialPasswordWarning, err)
		}

		updated := cloneUser(user)
		now := time.Now()
		updated.PasswordHash = string(hash)
		updated.MustChangePassword = false
		updated.PasswordChangedAt = &now
		updated.CredentialVersion++
		updated.UpdatedAt = now
		snapshot.users[id] = updated
		snapshot.byName[normalizeUsername(updated.Username)] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return errors.Join(initialPasswordWarning, err)
			}
			return initialPasswordRollbackError(err, initialPasswordRemoval.restore())
		}
		if s.commitSnapshot(snapshot) {
			return initialPasswordWarning
		}
	}
}

// ResetPassword resets a user's password (admin only)
func (s *UserStore) ResetPassword(id, newPassword string) error {
	return s.resetPassword(id, newPassword, true)
}

// ResetOwnPassword resets an administrator's own password without requiring a
// subsequent forced password change.
func (s *UserStore) ResetOwnPassword(id, newPassword string) error {
	return s.resetPassword(id, newPassword, false)
}

func (s *UserStore) resetPassword(id, newPassword string, mustChangePassword bool) error {
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var initialPasswordWarning error

	for {
		snapshot := s.snapshotState()
		user, ok := snapshot.users[id]
		if !ok {
			return ErrUserNotFound
		}
		initialPasswordRemoval, err := removeInitialPasswordFileForUser(snapshot.filePath, user.Username)
		if err != nil && !isAuthPersistenceWarning(err) {
			return fmt.Errorf("failed to remove initial password file: %w", err)
		}
		if isAuthPersistenceWarning(err) {
			initialPasswordWarning = errors.Join(initialPasswordWarning, err)
		}

		updated := cloneUser(user)
		now := time.Now()
		updated.PasswordHash = string(hash)
		updated.MustChangePassword = mustChangePassword
		updated.PasswordChangedAt = &now
		updated.CredentialVersion++
		updated.UpdatedAt = now
		snapshot.users[id] = updated
		snapshot.byName[normalizeUsername(updated.Username)] = updated

		if err := saveUserState(snapshot.filePath, snapshot.users); err != nil {
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return errors.Join(initialPasswordWarning, err)
			}
			return initialPasswordRollbackError(err, initialPasswordRemoval.restore())
		}
		if s.commitSnapshot(snapshot) {
			return initialPasswordWarning
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
			if s.commitSnapshotOnPersistenceWarning(snapshot, err) {
				return err
			}
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
	maxRandomByte := byte(256 - (256 % len(charset)))
	for i := range b {
		for {
			var randomByte [1]byte
			n, err := userRandomRead(randomByte[:])
			if err != nil {
				return "", err
			}
			if n != len(randomByte) {
				return "", io.ErrUnexpectedEOF
			}
			if randomByte[0] >= maxRandomByte {
				continue
			}
			b[i] = charset[int(randomByte[0])%len(charset)]
			break
		}
	}
	return string(b), nil
}

func normalizeNewUsername(username string) (string, error) {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", ErrInvalidUsername
	}
	if utf8.RuneCountInString(trimmed) > maxUsernameRuneCount {
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

func validateNewPassword(password string) error {
	if strings.TrimSpace(password) == "" || len(password) < minPasswordLength {
		return ErrPasswordTooShort
	}
	if len([]byte(password)) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

func validateRole(role Role) error {
	switch role {
	case RoleAdmin, RoleUser, RoleGuest:
		return nil
	default:
		return ErrInvalidRole
	}
}

func validateRoleHomeDir(role Role, homeDir string) error {
	if role != RoleAdmin && path.Clean(homeDir) == "/" {
		return errInvalidUserHomeDir
	}
	return nil
}

func normalizeHomeDir(homeDir string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(homeDir), "\\", "/")
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", errInvalidUserHomeDir
	}
	if normalized == "" {
		return "", errInvalidUserHomeDir
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errInvalidUserHomeDir
		}
	}
	cleaned := path.Clean("/" + normalized)
	if cleaned != "/" && !strings.HasPrefix(cleaned, "/") {
		return "", errInvalidUserHomeDir
	}
	return cleaned, nil
}

func normalizeGroupNames(groups []string) ([]string, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(groups))
	normalized := make([]string, 0, len(groups))
	for _, group := range groups {
		cleaned, err := normalizeGroupName(group)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeGroupName(group string) (string, error) {
	trimmed := strings.TrimSpace(group)
	if trimmed == "" || len(trimmed) > maxUsernameRuneCount || utf8.RuneCountInString(trimmed) > maxUsernameRuneCount {
		return "", errInvalidUserGroups
	}
	for _, r := range trimmed {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return "", errInvalidUserGroups
		}
	}
	return normalizeUsername(trimmed), nil
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
