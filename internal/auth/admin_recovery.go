package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"github.com/seanbao/mnemonas/internal/rootio"
)

const (
	adminRecoveryCredentialSchemaVersion = 1
	adminRecoveryCredentialMarker        = "mnemonas-admin-password-recovery"
	adminRecoveryPasswordLength          = 20
	maxAdminRecoveryCredentialFileBytes  = maxInitialPasswordFileBytes
	maxAdminRecoveryPasswordAttempts     = 32
)

var (
	// ErrAdminRecoveryNotAdmin indicates that the selected account is not an
	// administrator and therefore cannot be recovered by the offline flow.
	ErrAdminRecoveryNotAdmin = errors.New("administrator password recovery target is not an administrator")
	// ErrAdminRecoveryCredentialConflict indicates that initial-password.txt
	// cannot be safely associated with the selected account and state.
	ErrAdminRecoveryCredentialConflict = errors.New("administrator recovery credential file conflicts with current user state")
	// ErrAdminRecoveryCredentialPermissions indicates that an existing
	// plaintext credential file is readable beyond its owner.
	ErrAdminRecoveryCredentialPermissions = errors.New("administrator recovery credential file permissions must be 0600")
	// ErrAdminRecoveryUnsupported indicates that the platform cannot enforce
	// the Unix file-mode and no-follow guarantees required by offline recovery.
	ErrAdminRecoveryUnsupported = errors.New("administrator password recovery is unsupported on this platform")
	// ErrAdminRecoveryPending indicates that an offline recovery marker exists
	// but its password has not been committed to the user record yet.
	ErrAdminRecoveryPending = errors.New("administrator password recovery is incomplete; keep nasd stopped and rerun --recover-admin")

	adminRecoveryCredentialWriter = func(path string, data []byte) error {
		return writeAdminRecoveryCredentialAtomically(path, data)
	}
	adminRecoverySessionStateWriter = writeAdminRecoverySessionState
	adminRecoveryUserStateWriter    = saveUserState
	adminRecoveryNow                = time.Now
	adminRecoveryRemoveTempFile     = func(root *os.Root, name string) error { return root.Remove(name) }
)

// AdminPasswordRecoveryResult contains only non-sensitive recovery metadata.
// The generated password is written exclusively to CredentialPath.
type AdminPasswordRecoveryResult struct {
	Username         string
	CredentialPath   string
	Resumed          bool
	AlreadyAvailable bool
}

type adminRecoveryCredential struct {
	Username                  string
	UserID                    string
	PreviousCredentialVersion uint64
	Password                  string
}

type adminRecoveryCredentialState int

const (
	adminRecoveryCredentialPending adminRecoveryCredentialState = iota
	adminRecoveryCredentialCommitted
)

// CheckAdminRecoverySupported reports whether the current platform can enforce
// the filesystem guarantees required by offline administrator recovery.
func CheckAdminRecoverySupported() error {
	return ensureAdminRecoverySupported()
}

// RecoverAdminPassword resets an existing enabled administrator to a random
// temporary password while this lock remains held. The lock must belong to the
// authentication-state directory containing usersFilePath.
//
// A non-nil result with a persistence warning means the recovery state was
// committed, but the final directory durability sync could not be confirmed.
// The generated password is never returned to the caller.
func (l *StateLock) RecoverAdminPassword(usersFilePath, username string) (*AdminPasswordRecoveryResult, error) {
	if err := CheckAdminRecoverySupported(); err != nil {
		return nil, err
	}
	normalizedUsersPath, err := normalizeAuthFilePath(usersFilePath)
	if err != nil {
		return nil, fmt.Errorf("resolve administrator recovery users file: %w", err)
	}
	expectedLockPath := filepath.Join(filepath.Dir(normalizedUsersPath), authStateLockFileName)
	if l == nil || l.state == nil {
		return nil, ErrAuthStateLockRequired
	}

	state := l.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.file == nil || state.path != expectedLockPath {
		return nil, ErrAuthStateLockRequired
	}
	return recoverAdminPasswordLocked(normalizedUsersPath, username)
}

func recoverAdminPasswordLocked(usersFilePath, username string) (*AdminPasswordRecoveryResult, error) {
	store, err := openExistingUserStoreForAdminRecovery(usersFilePath)
	if err != nil {
		return nil, err
	}

	cleanUsername, err := normalizeNewUsername(username)
	if err != nil {
		return nil, err
	}
	user, err := store.GetByUsername(cleanUsername)
	if err != nil {
		return nil, err
	}
	if user.Role != RoleAdmin {
		return nil, ErrAdminRecoveryNotAdmin
	}
	if user.Disabled {
		return nil, ErrUserDisabled
	}
	if err := validateAdminRecoveryUserID(user.ID); err != nil {
		return nil, err
	}

	credentialPath := filepath.Join(filepath.Dir(store.filePath), "initial-password.txt")
	result := &AdminPasswordRecoveryResult{
		Username:       user.Username,
		CredentialPath: credentialPath,
	}
	if err := cleanupAdminRecoveryCredentialTempFiles(credentialPath); err != nil {
		return nil, fmt.Errorf("cleanup administrator recovery credential temp files: %w", err)
	}

	credential, credentialExists, recoveryCredential, err := loadAdminRecoveryCredential(credentialPath)
	if err != nil {
		return nil, err
	}
	if credentialExists {
		if err := confirmAdminRecoveryCredentialDurability(credentialPath); err != nil {
			return nil, err
		}
	}

	var warnings []error
	var state adminRecoveryCredentialState
	switch {
	case credentialExists && recoveryCredential:
		state, err = classifyAdminRecoveryCredential(user, credential)
		if err != nil {
			return nil, err
		}
		result.Resumed = true
		result.AlreadyAvailable = state == adminRecoveryCredentialCommitted
	case credentialExists:
		if !bootstrapCredentialMatchesUser(user, credential) {
			return nil, ErrAdminRecoveryCredentialConflict
		}
		result.AlreadyAvailable = true
		state = adminRecoveryCredentialCommitted
	default:
		if user.CredentialVersion == math.MaxUint64 {
			return nil, errors.New("administrator credential version cannot be incremented")
		}
		credential, err = newAdminRecoveryCredential(user)
		if err != nil {
			return nil, err
		}
		if err := adminRecoveryCredentialWriter(credentialPath, marshalAdminRecoveryCredential(credential)); err != nil {
			// The user record must never be committed until the recovery marker's
			// directory durability is confirmed. A visible-but-unconfirmed marker
			// can be safely resumed, but continuing here could leave a new password
			// without its only retrieval path after a power loss.
			return nil, fmt.Errorf("write administrator recovery credential file: %w", err)
		}
		persistedCredential, exists, isRecovery, err := loadAdminRecoveryCredential(credentialPath)
		if err != nil {
			return nil, err
		}
		if !exists || !isRecovery || !equalAdminRecoveryCredential(persistedCredential, credential) {
			return nil, ErrAdminRecoveryCredentialConflict
		}
		if err := confirmAdminRecoveryCredentialDurability(credentialPath); err != nil {
			return nil, err
		}
		credential = persistedCredential
		state = adminRecoveryCredentialPending
	}
	if state == adminRecoveryCredentialPending && user.CredentialVersion == math.MaxUint64 {
		return nil, adminRecoveryHardFailure(errors.New("administrator credential version cannot be incremented"), warnings)
	}

	sessionPath := filepath.Join(filepath.Dir(store.filePath), "auth-sessions.json")
	if err := revokeAdminRecoverySessions(sessionPath, user.ID); err != nil {
		if !isAuthPersistenceWarning(err) {
			return nil, adminRecoveryHardFailure(fmt.Errorf("revoke administrator sessions: %w", err), warnings)
		}
		warnings = append(warnings, fmt.Errorf("administrator session persistence warning: %w", err))
	}

	if state == adminRecoveryCredentialCommitted {
		return result, errors.Join(warnings...)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(credential.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, adminRecoveryHardFailure(fmt.Errorf("hash administrator recovery password: %w", err), warnings)
	}

	snapshot := store.snapshotState()
	current, ok := snapshot.users[user.ID]
	if !ok || current.CredentialVersion != credential.PreviousCredentialVersion || current.Username != credential.Username || current.Role != RoleAdmin || current.Disabled {
		return nil, adminRecoveryHardFailure(ErrAdminRecoveryCredentialConflict, warnings)
	}
	updated := cloneUser(current)
	now := adminRecoveryNow()
	updated.PasswordHash = string(hash)
	updated.MustChangePassword = true
	updated.CredentialVersion++
	updated.PasswordChangedAt = &now
	updated.UpdatedAt = now
	snapshot.users[updated.ID] = updated
	snapshot.byName[normalizeUsername(updated.Username)] = updated

	if err := adminRecoveryUserStateWriter(snapshot.filePath, snapshot.users); err != nil {
		if !isAuthPersistenceWarning(err) {
			return nil, adminRecoveryHardFailure(fmt.Errorf("save recovered administrator: %w", err), warnings)
		}
		warnings = append(warnings, fmt.Errorf("recovered administrator persistence warning: %w", err))
	}

	return result, errors.Join(warnings...)
}

// ValidateAdminRecoveryStartupState rejects a pending or inconsistent offline
// recovery before the normal service starts using the authentication files.
func ValidateAdminRecoveryStartupState(usersFilePath string) error {
	if err := CheckAdminRecoverySupported(); err != nil {
		return nil
	}

	store, err := openExistingUserStoreForAdminRecovery(usersFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	credentialPath := filepath.Join(filepath.Dir(store.filePath), "initial-password.txt")
	tempExists, err := hasAdminRecoveryCredentialTempFiles(credentialPath)
	if err != nil {
		return fmt.Errorf("inspect administrator recovery credential temp files: %w", err)
	}
	if tempExists {
		return ErrAdminRecoveryPending
	}

	data, err := readRegisteredAuthFileLimited(credentialPath, errPasswordFileSymlink, maxAdminRecoveryCredentialFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect administrator recovery credential: %w", err)
	}
	if !isAdminRecoveryCredentialData(data) {
		return nil
	}

	credential, _, recoveryCredential, err := loadAdminRecoveryCredential(credentialPath)
	if err != nil {
		return err
	}
	if !recoveryCredential || credential == nil {
		return ErrAdminRecoveryCredentialConflict
	}
	user, err := store.GetByUsername(credential.Username)
	if err != nil || user.ID != credential.UserID {
		return ErrAdminRecoveryCredentialConflict
	}
	state, err := classifyAdminRecoveryCredential(user, credential)
	if err != nil {
		return err
	}
	if state == adminRecoveryCredentialPending {
		return fmt.Errorf("%w for administrator %q", ErrAdminRecoveryPending, user.Username)
	}
	return nil
}

func writeAdminRecoveryCredentialAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("administrator recovery credential directory is not registered")
	}
	if err := validateAuthFilePath(normalizedPath, errPasswordFileSymlink); err != nil {
		return err
	}
	afterValidateAuthFilePath()
	tmpFile, tmpName, err := createAuthTempFile(root, ".initial-password-recovery-*.tmp", errPasswordFileSymlink)
	if err != nil {
		return fmt.Errorf("create temporary administrator recovery credential: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("set administrator recovery credential permissions: %w", err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("write administrator recovery credential: %w", err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("sync administrator recovery credential: %w", err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("close administrator recovery credential: %w", err))
	}
	if err := root.Link(tmpName, filepath.Base(normalizedPath)); err != nil {
		mapped := mapAuthRootPathError(err, errPasswordFileSymlink)
		if errors.Is(mapped, os.ErrExist) {
			mapped = ErrAdminRecoveryCredentialConflict
		}
		return cleanupAuthTempPath(root, tmpName, fmt.Errorf("publish administrator recovery credential: %w", mapped))
	}
	cleanup = false
	if err := adminRecoveryRemoveTempFile(root, tmpName); err != nil {
		return wrapAuthPersistenceWarning(fmt.Errorf("remove linked administrator recovery credential temp file: %w", err))
	}
	if err := syncAuthRootDir(root); err != nil {
		return wrapAuthPersistenceWarning(fmt.Errorf("sync administrator recovery credential directory: %w", err))
	}
	return nil
}

func cleanupAdminRecoveryCredentialTempFiles(path string) error {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if !isAdminRecoveryCredentialTempName(name) {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("stat administrator recovery credential temp file %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("administrator recovery credential temp path %q is not a regular file", name)
		}
		if err := adminRecoveryRemoveTempFile(root, name); err != nil {
			return fmt.Errorf("remove administrator recovery credential temp file %q: %w", name, err)
		}
		removed = true
	}
	if removed {
		if err := syncAuthRootDir(root); err != nil {
			return wrapAuthPersistenceWarning(fmt.Errorf("sync administrator recovery credential temp cleanup for %s: %w", normalizedPath, err))
		}
	}
	return nil
}

func hasAdminRecoveryCredentialTempFiles(path string) (bool, error) {
	root, _, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return false, err
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isAdminRecoveryCredentialTempName(name) {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return false, fmt.Errorf("stat administrator recovery credential temp file %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("administrator recovery credential temp path %q is not a regular file", name)
		}
		return true, nil
	}
	return false, nil
}

func isAdminRecoveryCredentialTempName(name string) bool {
	const prefix = ".initial-password-recovery-"
	const suffix = ".tmp"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	randomPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(randomPart) != 16 {
		return false
	}
	for _, char := range randomPart {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func confirmAdminRecoveryCredentialDurability(path string) error {
	root, _, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("administrator recovery credential directory is not registered")
	}
	if err := syncAuthRootDir(root); err != nil {
		return wrapAuthPersistenceWarning(fmt.Errorf("confirm administrator recovery credential directory durability: %w", err))
	}
	return nil
}

func equalAdminRecoveryCredential(left, right *adminRecoveryCredential) bool {
	return left != nil && right != nil &&
		left.Username == right.Username &&
		left.UserID == right.UserID &&
		left.PreviousCredentialVersion == right.PreviousCredentialVersion &&
		left.Password == right.Password
}

func openExistingUserStoreForAdminRecovery(filePath string) (*UserStore, error) {
	normalizedPath, _, _, err := ensureAuthDirRootWithState(filePath, errUserStoreSymlink, "users", false)
	if err != nil {
		return nil, err
	}
	store := &UserStore{
		users:    make(map[string]*User),
		byName:   make(map[string]*User),
		filePath: normalizedPath,
	}
	if err := store.load(); err != nil {
		return nil, fmt.Errorf("load existing users for administrator recovery: %w", err)
	}
	return store, nil
}

func newAdminRecoveryCredential(user *User) (*adminRecoveryCredential, error) {
	if err := validateAdminRecoveryUserID(user.ID); err != nil {
		return nil, err
	}
	for range maxAdminRecoveryPasswordAttempts {
		password, err := generateRandomPassword(adminRecoveryPasswordLength)
		if err != nil {
			return nil, fmt.Errorf("generate administrator recovery password: %w", err)
		}
		if err := validateNewPassword(password); err != nil {
			return nil, fmt.Errorf("generate administrator recovery password: %w", err)
		}
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil {
			continue
		}
		return &adminRecoveryCredential{
			Username:                  user.Username,
			UserID:                    user.ID,
			PreviousCredentialVersion: user.CredentialVersion,
			Password:                  password,
		}, nil
	}
	return nil, errors.New("generate administrator recovery password distinct from current password")
}

func marshalAdminRecoveryCredential(credential *adminRecoveryCredential) []byte {
	return []byte(fmt.Sprintf(`MnemoNAS Administrator Password Recovery
Recovery Marker: %s
Recovery Schema: %d
Username: %s
User ID: %s
Previous Credential Version: %d
Password: %s
`,
		adminRecoveryCredentialMarker,
		adminRecoveryCredentialSchemaVersion,
		credential.Username,
		credential.UserID,
		credential.PreviousCredentialVersion,
		credential.Password,
	))
}

func loadAdminRecoveryCredential(path string) (*adminRecoveryCredential, bool, bool, error) {
	data, err := readAdminRecoveryCredentialFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("read initial password file: %w", err)
	}
	if isAdminRecoveryCredentialData(data) {
		credential, err := parseAdminRecoveryCredential(data)
		if err != nil {
			return nil, true, true, fmt.Errorf("%w: %v", ErrAdminRecoveryCredentialConflict, err)
		}
		return credential, true, true, nil
	}
	credential, err := parseBootstrapCredential(data)
	if err != nil {
		return nil, true, false, fmt.Errorf("%w: %v", ErrAdminRecoveryCredentialConflict, err)
	}
	return credential, true, false, nil
}

func isAdminRecoveryCredentialData(data []byte) bool {
	return strings.Contains(string(data), "Recovery Marker:") || strings.Contains(string(data), "Recovery Schema:")
}

func readAdminRecoveryCredentialFile(path string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredAuthDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		normalizedPath, _, _, err = ensureAuthDirRootWithState(normalizedPath, errPasswordFileSymlink, "initial password", false)
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
	if err := validateAuthFilePath(normalizedPath, errPasswordFileSymlink); err != nil {
		return nil, err
	}
	afterValidateAuthFilePath()
	file, err := rootio.OpenRegularFileNoFollow(root, filepath.Base(normalizedPath))
	if err != nil {
		return nil, mapAuthRootPathError(err, errPasswordFileSymlink)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat initial password file: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, ErrAdminRecoveryCredentialPermissions
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAdminRecoveryCredentialFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAdminRecoveryCredentialFileBytes {
		return nil, fmt.Errorf("initial password file exceeds %d-byte limit", maxAdminRecoveryCredentialFileBytes)
	}
	return data, nil
}

func parseAdminRecoveryCredential(data []byte) (*adminRecoveryCredential, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 7 || lines[0] != "MnemoNAS Administrator Password Recovery" {
		return nil, errors.New("invalid recovery credential structure")
	}
	marker, err := parseAdminRecoveryLine(lines[1], "Recovery Marker")
	if err != nil || marker != adminRecoveryCredentialMarker {
		return nil, errors.New("invalid recovery marker")
	}
	schemaText, err := parseAdminRecoveryLine(lines[2], "Recovery Schema")
	if err != nil {
		return nil, err
	}
	schema, err := strconv.Atoi(schemaText)
	if err != nil || schema != adminRecoveryCredentialSchemaVersion {
		return nil, fmt.Errorf("unsupported recovery schema %q", schemaText)
	}
	username, err := parseAdminRecoveryLine(lines[3], "Username")
	if err != nil {
		return nil, err
	}
	if _, err := normalizeNewUsername(username); err != nil {
		return nil, fmt.Errorf("invalid recovery username: %w", err)
	}
	userID, err := parseAdminRecoveryLine(lines[4], "User ID")
	if err != nil || validateAdminRecoveryUserID(userID) != nil {
		return nil, errors.New("invalid recovery user ID")
	}
	versionText, err := parseAdminRecoveryLine(lines[5], "Previous Credential Version")
	if err != nil {
		return nil, err
	}
	version, err := strconv.ParseUint(versionText, 10, 64)
	if err != nil || version == 0 {
		return nil, errors.New("invalid previous credential version")
	}
	password, err := parseAdminRecoveryLine(lines[6], "Password")
	if err != nil {
		return nil, err
	}
	if err := validateNewPassword(password); err != nil {
		return nil, fmt.Errorf("invalid recovery password: %w", err)
	}
	return &adminRecoveryCredential{
		Username:                  username,
		UserID:                    userID,
		PreviousCredentialVersion: version,
		Password:                  password,
	}, nil
}

func validateAdminRecoveryUserID(userID string) error {
	if userID == "" || len(userID) > maxPersistedSessionUserIDBytes || strings.IndexFunc(userID, unicode.IsControl) >= 0 {
		return errors.New("administrator recovery target has an invalid user ID")
	}
	return nil
}

func parseAdminRecoveryLine(line, field string) (string, error) {
	prefix := field + ": "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("recovery credential is missing %s", field)
	}
	value := strings.TrimPrefix(line, prefix)
	if value == "" {
		return "", fmt.Errorf("recovery credential has empty %s", field)
	}
	return value, nil
}

func parseBootstrapCredential(data []byte) (*adminRecoveryCredential, error) {
	var username string
	var password string
	usernameCount := 0
	passwordCount := 0
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Username: "):
			username = strings.TrimPrefix(line, "Username: ")
			usernameCount++
		case strings.HasPrefix(line, "Password: "):
			password = strings.TrimPrefix(line, "Password: ")
			passwordCount++
		}
	}
	if usernameCount != 1 || passwordCount != 1 {
		return nil, errors.New("initial password file must contain one username and one password")
	}
	if _, err := normalizeNewUsername(username); err != nil {
		return nil, fmt.Errorf("invalid bootstrap username: %w", err)
	}
	if err := validateNewPassword(password); err != nil {
		return nil, fmt.Errorf("invalid bootstrap password: %w", err)
	}
	return &adminRecoveryCredential{Username: username, Password: password}, nil
}

func classifyAdminRecoveryCredential(user *User, credential *adminRecoveryCredential) (adminRecoveryCredentialState, error) {
	if credential.Username != user.Username || credential.UserID != user.ID {
		return 0, ErrAdminRecoveryCredentialConflict
	}
	if user.CredentialVersion == credential.PreviousCredentialVersion {
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credential.Password)) == nil {
			return 0, ErrAdminRecoveryCredentialConflict
		}
		return adminRecoveryCredentialPending, nil
	}
	if credential.PreviousCredentialVersion != math.MaxUint64 && user.CredentialVersion == credential.PreviousCredentialVersion+1 && user.MustChangePassword && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credential.Password)) == nil {
		return adminRecoveryCredentialCommitted, nil
	}
	return 0, ErrAdminRecoveryCredentialConflict
}

func bootstrapCredentialMatchesUser(user *User, credential *adminRecoveryCredential) bool {
	return credential.Username == user.Username && user.MustChangePassword && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credential.Password)) == nil
}

func revokeAdminRecoverySessions(path, userID string) error {
	state, exists, err := loadTokenSessionState(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	removed := false
	for sessionID, record := range state.Sessions {
		if record.UserID == userID {
			delete(state.Sessions, sessionID)
			removed = true
		}
	}
	if !removed {
		return nil
	}
	return adminRecoverySessionStateWriter(path, state)
}

func writeAdminRecoverySessionState(path string, state *tokenSessionState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal token sessions: %w", err)
	}
	if len(data) > maxTokenSessionStateFileBytes {
		return fmt.Errorf("token session state exceeds %d-byte limit", maxTokenSessionStateFileBytes)
	}
	return writeRegisteredAuthFileAtomically(path, data, errTokenSessionFileSymlink, ".auth-sessions-*.tmp", "token sessions")
}

func adminRecoveryHardFailure(hardErr error, warnings []error) error {
	if len(warnings) == 0 {
		return hardErr
	}
	return fmt.Errorf("%w (earlier committed persistence warning: %v)", hardErr, errors.Join(warnings...))
}
