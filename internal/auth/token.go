package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func init() {
	// NumericDate is encoded as a JSON number. Whole seconds are the finest
	// stable canonical representation across signing, parsing, and persistence.
	jwt.TimePrecision = time.Second
}

var tokenRandomRead = rand.Read
var persistTokenSessionState = func(tm *TokenManager) error { return tm.persistSessionStateLocked() }
var errTokenSessionFileSymlink = errors.New("token session file path must not be a symlink")

const (
	tokenSessionSchemaVersion        = 3
	maxTrackedRefreshSessions        = 4096
	maxTrackedRefreshSessionsPerUser = 64
	maxTokenSessionStateFileBytes    = 2 << 20
	maxPersistedSessionUserIDBytes   = 256
	minimumRefreshRotationInterval   = 30 * time.Second
	tokenValidationLeeway            = time.Second
	defaultRestartTimeLease          = time.Minute
	defaultTimeLeaseRenewalLead      = 15 * time.Second
	maxSessionIDGenerationAttempts   = 32
)

type sessionRegistryRecord struct {
	UserID                string    `json:"user_id"`
	ExpiresAt             time.Time `json:"expires_at"`
	NextRefreshGeneration uint64    `json:"next_refresh_generation"`
	LastRotatedAt         time.Time `json:"last_rotated_at"`
}

type tokenSessionState struct {
	SchemaVersion    int                              `json:"schema_version"`
	RestartTimeFloor time.Time                        `json:"restart_time_floor"`
	Sessions         map[string]sessionRegistryRecord `json:"sessions"`
}

type encodedSessionRegistryRecord struct {
	UserID                *string    `json:"user_id"`
	ExpiresAt             *time.Time `json:"expires_at"`
	NextRefreshGeneration *uint64    `json:"next_refresh_generation"`
	LastRotatedAt         *time.Time `json:"last_rotated_at"`
}

type encodedTokenSessionState struct {
	SchemaVersion    *int                                     `json:"schema_version"`
	RestartTimeFloor *time.Time                               `json:"restart_time_floor"`
	Sessions         *map[string]encodedSessionRegistryRecord `json:"sessions"`
}

// TokenManager handles JWT token generation and validation.
//
// sessionRegistry is authoritative: a signed token is accepted only while its
// random session ID has a matching persisted record. Removing that record is
// therefore a durable, bounded revocation operation and needs no tombstone.
type TokenManager struct {
	secretKey        []byte
	accessExpiry     time.Duration
	refreshExpiry    time.Duration
	issuer           string
	sessionStorePath string

	mu                      sync.RWMutex
	sessionRegistry         map[string]sessionRegistryRecord
	now                     func() time.Time
	monotonicNow            func() time.Time
	timeHighWater           time.Time
	timeHighWaterObservedAt time.Time
	restartTimeFloor        time.Time
	timeLeaseDuration       time.Duration
	timeLeaseRenewalLead    time.Duration
	refreshRotationInterval time.Duration
	refreshSessionLimit     int
	refreshUserSessionLimit int
}

// TokenClaims extends standard JWT claims.
type TokenClaims struct {
	jwt.RegisteredClaims
	UserID            string           `json:"uid"`
	Username          string           `json:"username"`
	Role              Role             `json:"role"`
	SessionID         string           `json:"sid"`
	SessionExpiresAt  *jwt.NumericDate `json:"sexp"`
	CredentialVersion uint64           `json:"cv"`
}

type refreshTokenClaims struct {
	jwt.RegisteredClaims
	CredentialVersion uint64 `json:"cv"`
	SessionID         string `json:"sid"`
	Generation        uint64 `json:"gen"`
}

// TokenPair contains access and refresh tokens.
type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
	TokenType        string    `json:"token_type"`
}

// NewTokenManager creates a new token manager.
func NewTokenManager(secretKey string, accessExpiry, refreshExpiry time.Duration) *TokenManager {
	key := []byte(secretKey)
	if len(key) < 32 {
		key = make([]byte, 32)
		if n, err := tokenRandomRead(key); err != nil || n != len(key) {
			fallback := sha256.Sum256([]byte(secretKey))
			key = append([]byte(nil), fallback[:]...)
		}
	}

	return &TokenManager{
		secretKey:               key,
		accessExpiry:            accessExpiry,
		refreshExpiry:           refreshExpiry,
		issuer:                  "mnemonas",
		sessionRegistry:         make(map[string]sessionRegistryRecord),
		now:                     time.Now,
		monotonicNow:            time.Now,
		timeLeaseDuration:       defaultRestartTimeLease,
		timeLeaseRenewalLead:    defaultTimeLeaseRenewalLead,
		refreshRotationInterval: minimumRefreshRotationInterval,
		refreshSessionLimit:     maxTrackedRefreshSessions,
		refreshUserSessionLimit: maxTrackedRefreshSessionsPerUser,
	}
}

// UpdateExpiries updates token lifetimes used for newly issued tokens.
func (tm *TokenManager) UpdateExpiries(accessExpiry, refreshExpiry time.Duration) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.accessExpiry = accessExpiry
	tm.refreshExpiry = refreshExpiry
}

func (tm *TokenManager) effectiveNowLocked() time.Time {
	wallNow := tm.now()
	observedAt := tm.monotonicNow()
	if tm.timeHighWater.IsZero() {
		tm.timeHighWater = wallNow
	} else if !tm.timeHighWaterObservedAt.IsZero() && observedAt.After(tm.timeHighWaterObservedAt) {
		tm.timeHighWater = tm.timeHighWater.Add(observedAt.Sub(tm.timeHighWaterObservedAt))
	}
	if wallNow.After(tm.timeHighWater) {
		tm.timeHighWater = wallNow
	}
	tm.timeHighWaterObservedAt = observedAt
	return tm.timeHighWater
}

func (tm *TokenManager) currentTime() time.Time {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.effectiveNowLocked()
}

// EnableSessionPersistence configures the authoritative file-backed session
// registry. Existing files use a strict, bounded schema and fail closed.
func (tm *TokenManager) EnableSessionPersistence(filePath string) error {
	if filePath == "" {
		return nil
	}

	normalizedPath, err := ensureAuthDirRoot(filePath, errTokenSessionFileSymlink, "token sessions")
	if err != nil {
		return err
	}

	state, exists, err := loadTokenSessionState(normalizedPath)
	if err != nil {
		return fmt.Errorf("load token session file: %w", err)
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	previousPath := tm.sessionStorePath
	previousRegistry := cloneSessionRegistry(tm.sessionRegistry)
	previousRestartFloor := tm.restartTimeFloor
	previousHighWater := tm.timeHighWater
	previousObservedAt := tm.timeHighWaterObservedAt

	tm.sessionStorePath = normalizedPath
	for sessionID, record := range state.Sessions {
		if existing, ok := tm.sessionRegistry[sessionID]; ok && existing != record {
			tm.restorePersistenceEnableLocked(previousPath, previousRegistry, previousRestartFloor, previousHighWater, previousObservedAt)
			return fmt.Errorf("token session %q conflicts with in-memory state", sessionID)
		}
		tm.sessionRegistry[sessionID] = record
	}
	if state.RestartTimeFloor.After(tm.timeHighWater) {
		tm.timeHighWater = state.RestartTimeFloor
	}
	if state.RestartTimeFloor.After(tm.restartTimeFloor) {
		tm.restartTimeFloor = state.RestartTimeFloor
	}
	if wallNow := tm.now(); wallNow.After(tm.timeHighWater) {
		tm.timeHighWater = wallNow
	}
	tm.timeHighWaterObservedAt = tm.monotonicNow()
	now := tm.effectiveNowLocked()
	tm.cleanupExpiredSessionsLocked(now)
	if err := tm.validateActiveSessionLimitsLocked(now); err != nil {
		tm.restorePersistenceEnableLocked(previousPath, previousRegistry, previousRestartFloor, previousHighWater, previousObservedAt)
		return err
	}

	oldFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	err = persistTokenSessionState(tm)
	if err != nil && !isAuthPersistenceWarning(err) {
		tm.restartTimeFloor = oldFloor
		tm.restorePersistenceEnableLocked(previousPath, previousRegistry, previousRestartFloor, previousHighWater, previousObservedAt)
		return fmt.Errorf("initialize token session persistence: %w", err)
	}
	if !exists && tm.restartTimeFloor.IsZero() {
		tm.restorePersistenceEnableLocked(previousPath, previousRegistry, previousRestartFloor, previousHighWater, previousObservedAt)
		return errors.New("initialize token session persistence: restart time lease was not established")
	}
	return err
}

func (tm *TokenManager) restorePersistenceEnableLocked(path string, registry map[string]sessionRegistryRecord, floor, highWater, observedAt time.Time) {
	tm.sessionStorePath = path
	tm.sessionRegistry = registry
	tm.restartTimeFloor = floor
	tm.timeHighWater = highWater
	tm.timeHighWaterObservedAt = observedAt
}

func loadTokenSessionState(filePath string) (*tokenSessionState, bool, error) {
	data, err := readRegisteredAuthFileLimited(filePath, errTokenSessionFileSymlink, maxTokenSessionStateFileBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newTokenSessionState(), false, nil
		}
		return nil, false, fmt.Errorf("failed to read token session file: %w", err)
	}
	if len(data) == 0 {
		return nil, true, errors.New("token session file is empty")
	}

	state, err := decodeTokenSessionState(data)
	if err != nil {
		return nil, true, err
	}
	return state, true, nil
}

func readRegisteredAuthFileLimited(path string, symlinkErr error, limit int64) ([]byte, error) {
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
	if err := validateAuthFilePath(normalizedPath, symlinkErr); err != nil {
		return nil, err
	}
	afterValidateAuthFilePath()

	file, err := rootio.OpenRegularFileNoFollow(root, filepath.Base(normalizedPath))
	if err != nil {
		return nil, mapAuthRootPathError(err, symlinkErr)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("auth file exceeds %d-byte limit", limit)
	}
	return data, nil
}

func decodeTokenSessionState(data []byte) (*tokenSessionState, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, fmt.Errorf("failed to parse token session file: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var encoded encodedTokenSessionState
	if err := decoder.Decode(&encoded); err != nil {
		return nil, fmt.Errorf("failed to parse token session file: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("failed to parse token session file: %w", err)
	}
	if encoded.SchemaVersion == nil {
		return nil, errors.New("token session file is missing schema version")
	}
	if *encoded.SchemaVersion != tokenSessionSchemaVersion {
		return nil, fmt.Errorf("unsupported token session schema version %d", *encoded.SchemaVersion)
	}
	if encoded.RestartTimeFloor == nil || encoded.RestartTimeFloor.IsZero() {
		return nil, errors.New("token session file is missing restart time floor")
	}
	if encoded.Sessions == nil {
		return nil, errors.New("token session file is missing sessions")
	}
	if len(*encoded.Sessions) > maxTrackedRefreshSessions {
		return nil, fmt.Errorf("token session file contains %d sessions; maximum is %d", len(*encoded.Sessions), maxTrackedRefreshSessions)
	}
	state := tokenSessionState{
		SchemaVersion:    *encoded.SchemaVersion,
		RestartTimeFloor: *encoded.RestartTimeFloor,
		Sessions:         make(map[string]sessionRegistryRecord, len(*encoded.Sessions)),
	}
	for sessionID, encodedRecord := range *encoded.Sessions {
		if encodedRecord.UserID == nil || encodedRecord.ExpiresAt == nil || encodedRecord.NextRefreshGeneration == nil || encodedRecord.LastRotatedAt == nil {
			return nil, fmt.Errorf("token session file contains session %q with missing fields", sessionID)
		}
		record := sessionRegistryRecord{
			UserID:                *encodedRecord.UserID,
			ExpiresAt:             *encodedRecord.ExpiresAt,
			NextRefreshGeneration: *encodedRecord.NextRefreshGeneration,
			LastRotatedAt:         *encodedRecord.LastRotatedAt,
		}
		if err := validatePersistedSessionRecord(sessionID, record); err != nil {
			return nil, err
		}
		state.Sessions[sessionID] = record
	}
	return &state, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("token session file contains trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := walkJSONToken(decoder, first); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func walkJSONToken(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONToken(decoder, value); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONToken(decoder, value); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func validatePersistedSessionRecord(sessionID string, record sessionRegistryRecord) error {
	if !validSessionID(sessionID) {
		return fmt.Errorf("token session file contains invalid session ID %q", sessionID)
	}
	if record.UserID == "" || len(record.UserID) > maxPersistedSessionUserIDBytes {
		return fmt.Errorf("token session file contains invalid user ID for session %q", sessionID)
	}
	if record.ExpiresAt.IsZero() {
		return fmt.Errorf("token session file contains zero expiry for session %q", sessionID)
	}
	if record.LastRotatedAt.IsZero() || record.LastRotatedAt.After(record.ExpiresAt) {
		return fmt.Errorf("token session file contains invalid rotation time for session %q", sessionID)
	}
	return nil
}

func validSessionID(sessionID string) bool {
	if len(sessionID) != 32 || strings.ToLower(sessionID) != sessionID {
		return false
	}
	decoded, err := hex.DecodeString(sessionID)
	return err == nil && len(decoded) == 16
}

func newTokenSessionState() *tokenSessionState {
	return &tokenSessionState{
		SchemaVersion: tokenSessionSchemaVersion,
		Sessions:      make(map[string]sessionRegistryRecord),
	}
}

func (tm *TokenManager) persistSessionStateLocked() error {
	if tm.sessionStorePath == "" {
		return nil
	}
	if tm.restartTimeFloor.IsZero() {
		return errors.New("token session restart time floor is not initialized")
	}
	if len(tm.sessionRegistry) > maxTrackedRefreshSessions {
		return fmt.Errorf("token session registry contains %d sessions; maximum is %d", len(tm.sessionRegistry), maxTrackedRefreshSessions)
	}

	state := tokenSessionState{
		SchemaVersion:    tokenSessionSchemaVersion,
		RestartTimeFloor: tm.restartTimeFloor,
		Sessions:         cloneSessionRegistry(tm.sessionRegistry),
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal token sessions: %w", err)
	}
	if len(data) > maxTokenSessionStateFileBytes {
		return fmt.Errorf("token session state exceeds %d-byte limit", maxTokenSessionStateFileBytes)
	}

	return writeRegisteredAuthFileAtomically(tm.sessionStorePath, data, errTokenSessionFileSymlink, ".auth-sessions-*.tmp", "token sessions")
}

func cloneSessionRegistry(source map[string]sessionRegistryRecord) map[string]sessionRegistryRecord {
	clone := make(map[string]sessionRegistryRecord, len(source))
	for sessionID, record := range source {
		clone[sessionID] = record
	}
	return clone
}

func (tm *TokenManager) extendRestartTimeLeaseLocked(now time.Time) {
	if tm.sessionStorePath == "" || tm.timeLeaseDuration <= 0 {
		return
	}
	candidate := now.Add(tm.timeLeaseDuration)
	if candidate.After(tm.restartTimeFloor) {
		tm.restartTimeFloor = candidate
	}
}

func (tm *TokenManager) prepareValidationTime() (time.Time, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := tm.effectiveNowLocked()
	if tm.sessionStorePath == "" {
		return now, nil
	}
	if tm.timeLeaseDuration <= 0 || tm.timeLeaseRenewalLead < 0 || tm.timeLeaseRenewalLead >= tm.timeLeaseDuration {
		return time.Time{}, fmt.Errorf("%w: invalid restart time lease configuration", ErrTokenStateUnavailable)
	}
	if !tm.restartTimeFloor.IsZero() && now.Add(tm.timeLeaseRenewalLead).Before(tm.restartTimeFloor) {
		return now, nil
	}

	previousFloor := tm.restartTimeFloor
	removed := tm.cleanupExpiredSessionsLocked(now)
	tm.extendRestartTimeLeaseLocked(now)
	err := persistTokenSessionState(tm)
	if err == nil || isAuthPersistenceWarning(err) {
		return now, nil
	}
	tm.restartTimeFloor = previousFloor
	tm.restoreRemovedSessionsLocked(removed)
	if previousFloor.IsZero() || !now.Before(previousFloor) {
		return time.Time{}, fmt.Errorf("%w: renew restart time lease: %v", ErrTokenStateUnavailable, err)
	}
	return now, nil
}

func (tm *TokenManager) restoreRemovedSessionsLocked(removed map[string]sessionRegistryRecord) {
	for sessionID, record := range removed {
		tm.sessionRegistry[sessionID] = record
	}
}

// GenerateTokenPair creates and durably registers a new login session before
// returning either token. A persistence warning is returned together with the
// usable pair because the atomic rename already committed the registry row.
func (tm *TokenManager) GenerateTokenPair(user *User) (*TokenPair, error) {
	if err := validateTokenUser(user); err != nil {
		return nil, err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := jwtTimestamp(tm.effectiveNowLocked())
	removed := tm.cleanupExpiredSessionsLocked(now)
	if tm.activeSessionCountLocked(now) >= tm.refreshSessionLimit || tm.activeSessionCountForUserLocked(user.ID, now) >= tm.refreshUserSessionLimit {
		tm.restoreRemovedSessionsLocked(removed)
		return nil, ErrRefreshSessionLimit
	}

	sessionID, err := tm.generateUniqueSessionIDLocked()
	if err != nil {
		tm.restoreRemovedSessionsLocked(removed)
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	sessionExpiresAt := jwtTimestamp(now.Add(tm.refreshExpiry))
	if !sessionExpiresAt.After(now) {
		tm.restoreRemovedSessionsLocked(removed)
		return nil, ErrTokenExpired
	}

	pair, err := signTokenPair(
		user,
		sessionID,
		sessionExpiresAt,
		0,
		sessionID,
		now,
		tm.accessExpiry,
		tm.issuer,
		append([]byte(nil), tm.secretKey...),
	)
	if err != nil {
		tm.restoreRemovedSessionsLocked(removed)
		return nil, err
	}
	lastRotatedAt := now
	if tm.refreshRotationInterval > 0 {
		lastRotatedAt = now.Add(-tm.refreshRotationInterval)
	}
	tm.sessionRegistry[sessionID] = sessionRegistryRecord{
		UserID:                user.ID,
		ExpiresAt:             sessionExpiresAt,
		NextRefreshGeneration: 0,
		LastRotatedAt:         lastRotatedAt,
	}
	previousFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	if err := persistTokenSessionState(tm); err != nil {
		if !isAuthPersistenceWarning(err) {
			delete(tm.sessionRegistry, sessionID)
			tm.restoreRemovedSessionsLocked(removed)
			tm.restartTimeFloor = previousFloor
			return nil, err
		}
		return pair, err
	}
	return pair, nil
}

func validateTokenUser(user *User) error {
	if user == nil || user.ID == "" || len(user.ID) > maxPersistedSessionUserIDBytes || user.Username == "" {
		return ErrInvalidToken
	}
	return nil
}

func (tm *TokenManager) generateUniqueSessionIDLocked() (string, error) {
	for range maxSessionIDGenerationAttempts {
		sessionID, err := generateTokenID()
		if err != nil {
			return "", err
		}
		if _, exists := tm.sessionRegistry[sessionID]; !exists {
			return sessionID, nil
		}
	}
	return "", errors.New("could not allocate a unique session ID")
}

func (tm *TokenManager) generateTokenPairForSession(user *User, sessionID string, sessionExpiresAt time.Time, generation ...uint64) (*TokenPair, error) {
	if err := validateTokenUser(user); err != nil || !validSessionID(sessionID) || sessionExpiresAt.IsZero() || len(generation) > 1 {
		return nil, ErrInvalidToken
	}

	now, err := tm.prepareValidationTime()
	if err != nil {
		return nil, err
	}

	tm.mu.Lock()
	record, tracked := tm.sessionRegistry[sessionID]
	if !tracked {
		tm.mu.Unlock()
		return nil, ErrTokenRevoked
	}
	sessionExpiresAt = jwtTimestamp(sessionExpiresAt)
	if record.UserID != user.ID || !record.ExpiresAt.Equal(sessionExpiresAt) {
		tm.mu.Unlock()
		return nil, ErrInvalidToken
	}
	if !record.ExpiresAt.After(now) {
		tm.mu.Unlock()
		return nil, ErrTokenExpired
	}
	if record.NextRefreshGeneration == math.MaxUint64 {
		tm.mu.Unlock()
		return nil, ErrInvalidToken
	}
	refreshGeneration := record.NextRefreshGeneration + 1
	if len(generation) == 1 && generation[0] != refreshGeneration {
		tm.mu.Unlock()
		return nil, ErrInvalidToken
	}
	tm.mu.Unlock()

	tokenID, err := generateTokenID()
	if err != nil {
		return nil, fmt.Errorf("generate token id: %w", err)
	}

	return tm.signTokenPair(user, sessionID, sessionExpiresAt, refreshGeneration, tokenID, jwtTimestamp(now))
}

func (tm *TokenManager) signTokenPair(user *User, sessionID string, sessionExpiresAt time.Time, refreshGeneration uint64, tokenID string, now time.Time) (*TokenPair, error) {
	tm.mu.Lock()
	accessDuration := tm.accessExpiry
	issuer := tm.issuer
	secretKey := append([]byte(nil), tm.secretKey...)
	tm.mu.Unlock()
	return signTokenPair(user, sessionID, sessionExpiresAt, refreshGeneration, tokenID, now, accessDuration, issuer, secretKey)
}

func signTokenPair(user *User, sessionID string, sessionExpiresAt time.Time, refreshGeneration uint64, tokenID string, now time.Time, accessDuration time.Duration, issuer string, secretKey []byte) (*TokenPair, error) {
	accessExpiry := now.Add(accessDuration)
	if accessExpiry.After(sessionExpiresAt) {
		accessExpiry = sessionExpiresAt
	}

	accessClaims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   user.ID,
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        tokenID,
		},
		UserID:            user.ID,
		Username:          user.Username,
		Role:              user.Role,
		SessionID:         sessionID,
		SessionExpiresAt:  jwt.NewNumericDate(sessionExpiresAt),
		CredentialVersion: user.CredentialVersion,
	}
	accessTokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(secretKey)
	if err != nil {
		return nil, err
	}

	refreshClaims := refreshTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   user.ID,
			ExpiresAt: jwt.NewNumericDate(sessionExpiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        tokenID + "-refresh",
		},
		CredentialVersion: user.CredentialVersion,
		SessionID:         sessionID,
		Generation:        refreshGeneration,
	}
	refreshTokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(secretKey)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:      accessTokenString,
		RefreshToken:     refreshTokenString,
		ExpiresAt:        accessExpiry,
		RefreshExpiresAt: sessionExpiresAt,
		TokenType:        "Bearer",
	}, nil
}

// ValidateAccessToken validates an access token and its authoritative session.
func (tm *TokenManager) ValidateAccessToken(tokenString string) (*TokenClaims, error) {
	validationNow, err := tm.prepareValidationTime()
	if err != nil {
		return nil, err
	}
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return tm.secretKey, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tm.issuer),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return validationNow }),
		jwt.WithLeeway(tokenValidationLeeway),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid || claims.UserID == "" || claims.Subject != claims.UserID || claims.Username == "" || claims.ID == "" || !validSessionID(claims.SessionID) || claims.SessionExpiresAt == nil || claims.ExpiresAt == nil || claims.SessionExpiresAt.Time.Before(claims.ExpiresAt.Time) {
		return nil, ErrInvalidToken
	}

	tm.mu.Lock()
	record, tracked := tm.sessionRegistry[claims.SessionID]
	tm.mu.Unlock()
	if !tracked {
		return nil, ErrTokenRevoked
	}
	if record.UserID != claims.UserID || !record.ExpiresAt.Equal(claims.SessionExpiresAt.Time) {
		return nil, ErrInvalidToken
	}
	if !record.ExpiresAt.After(validationNow) {
		return nil, ErrTokenExpired
	}
	return claims, nil
}

func (tm *TokenManager) validateRefreshTokenClaims(tokenString string) (*refreshTokenClaims, error) {
	claims, validationNow, err := tm.parseRefreshTokenClaimsAtCurrentTime(tokenString)
	if err != nil {
		return nil, err
	}
	tm.mu.Lock()
	err = tm.validateRefreshTokenClaimsLocked(claims, validationNow)
	tm.mu.Unlock()
	if err != nil {
		if errors.Is(err, errRefreshTokenReused) {
			return claims, err
		}
		return nil, err
	}
	return claims, nil
}

func (tm *TokenManager) parseRefreshTokenClaims(tokenString string) (*refreshTokenClaims, error) {
	claims, _, err := tm.parseRefreshTokenClaimsAtCurrentTime(tokenString)
	return claims, err
}

func (tm *TokenManager) parseRefreshTokenClaimsAtCurrentTime(tokenString string) (*refreshTokenClaims, time.Time, error) {
	validationNow, err := tm.prepareValidationTime()
	if err != nil {
		return nil, time.Time{}, err
	}
	token, err := jwt.ParseWithClaims(tokenString, &refreshTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return tm.secretKey, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tm.issuer),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return validationNow }),
		jwt.WithLeeway(tokenValidationLeeway),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, validationNow, ErrTokenExpired
		}
		return nil, validationNow, ErrInvalidToken
	}

	claims, ok := token.Claims.(*refreshTokenClaims)
	if !ok || !token.Valid {
		return nil, validationNow, ErrInvalidToken
	}
	accessTokenID := strings.TrimSuffix(claims.ID, "-refresh")
	if claims.Subject == "" || !validSessionID(claims.SessionID) || claims.ExpiresAt == nil || accessTokenID == "" || accessTokenID == claims.ID {
		return nil, validationNow, ErrInvalidToken
	}
	return claims, validationNow, nil
}

func (tm *TokenManager) validateRefreshTokenClaimsLocked(claims *refreshTokenClaims, now time.Time) error {
	record, tracked := tm.sessionRegistry[claims.SessionID]
	if !tracked {
		return ErrTokenRevoked
	}
	if record.UserID != claims.Subject || !record.ExpiresAt.Equal(claims.ExpiresAt.Time) {
		return ErrInvalidToken
	}
	if !record.ExpiresAt.After(now) {
		return ErrTokenExpired
	}
	if claims.Generation < record.NextRefreshGeneration {
		return errRefreshTokenReused
	}
	if claims.Generation > record.NextRefreshGeneration {
		return ErrInvalidToken
	}
	return nil
}

// ValidateRefreshToken validates a refresh token.
func (tm *TokenManager) ValidateRefreshToken(tokenString string) (string, error) {
	claims, err := tm.validateRefreshTokenClaims(tokenString)
	if err != nil {
		if errors.Is(err, errRefreshTokenReused) {
			return "", ErrTokenRevoked
		}
		return "", err
	}
	return claims.Subject, nil
}

func (tm *TokenManager) consumeRefreshTokenClaims(claims *refreshTokenClaims) error {
	if claims == nil || claims.Subject == "" || !validSessionID(claims.SessionID) || claims.ExpiresAt == nil || strings.TrimSuffix(claims.ID, "-refresh") == "" || !strings.HasSuffix(claims.ID, "-refresh") {
		return ErrInvalidToken
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := tm.effectiveNowLocked()
	if !claims.ExpiresAt.Time.After(now) {
		return ErrTokenExpired
	}
	if err := tm.validateRefreshTokenClaimsLocked(claims, now); err != nil {
		return err
	}
	if claims.Generation == math.MaxUint64 {
		return ErrInvalidToken
	}
	previous := tm.sessionRegistry[claims.SessionID]
	if tm.refreshRotationInterval > 0 && now.Before(previous.LastRotatedAt.Add(tm.refreshRotationInterval)) {
		return ErrRefreshRateLimited
	}

	updated := previous
	updated.NextRefreshGeneration = claims.Generation + 1
	updated.LastRotatedAt = now
	tm.sessionRegistry[claims.SessionID] = updated
	previousFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	if err := persistTokenSessionState(tm); err != nil {
		if !isAuthPersistenceWarning(err) {
			tm.sessionRegistry[claims.SessionID] = previous
			tm.restartTimeFloor = previousFloor
		}
		return err
	}
	return nil
}

// RevokeSession removes one authoritative session record. Missing records are
// already permanently rejected and therefore make this operation idempotent.
func (tm *TokenManager) RevokeSession(sessionID string, expiresAt time.Time) error {
	return tm.RevokeSessions(map[string]time.Time{sessionID: expiresAt})
}

// RevokeSessions atomically removes one or more login sessions.
func (tm *TokenManager) RevokeSessions(sessions map[string]time.Time) error {
	if len(sessions) == 0 {
		return nil
	}
	for sessionID, expiresAt := range sessions {
		if !validSessionID(sessionID) || expiresAt.IsZero() {
			return ErrInvalidToken
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	now := tm.effectiveNowLocked()
	removedExpired := tm.cleanupExpiredSessionsLocked(now)
	removedRequested := make(map[string]sessionRegistryRecord, len(sessions))
	for sessionID := range sessions {
		if record, exists := tm.sessionRegistry[sessionID]; exists {
			removedRequested[sessionID] = record
			delete(tm.sessionRegistry, sessionID)
		}
	}
	if len(removedExpired) == 0 && len(removedRequested) == 0 {
		return nil
	}

	previousFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	if err := persistTokenSessionState(tm); err != nil {
		if !isAuthPersistenceWarning(err) {
			tm.restoreRemovedSessionsLocked(removedExpired)
			tm.restoreRemovedSessionsLocked(removedRequested)
			tm.restartTimeFloor = previousFloor
		}
		return err
	}
	return nil
}

// RevokeByUser atomically removes all active sessions for a user.
func (tm *TokenManager) RevokeByUser(userID string) error {
	if userID == "" {
		return ErrInvalidToken
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := tm.effectiveNowLocked()
	removedExpired := tm.cleanupExpiredSessionsLocked(now)
	removedUser := make(map[string]sessionRegistryRecord)
	for sessionID, record := range tm.sessionRegistry {
		if record.UserID == userID {
			removedUser[sessionID] = record
			delete(tm.sessionRegistry, sessionID)
		}
	}
	if len(removedExpired) == 0 && len(removedUser) == 0 {
		return nil
	}

	previousFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	if err := persistTokenSessionState(tm); err != nil {
		if !isAuthPersistenceWarning(err) {
			tm.restoreRemovedSessionsLocked(removedExpired)
			tm.restoreRemovedSessionsLocked(removedUser)
			tm.restartTimeFloor = previousFloor
		}
		return err
	}
	return nil
}

// CleanupExpiredSessions removes expired registry records transactionally.
func (tm *TokenManager) CleanupExpiredSessions() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := tm.effectiveNowLocked()
	removed := tm.cleanupExpiredSessionsLocked(now)
	if len(removed) == 0 {
		return nil
	}
	previousFloor := tm.restartTimeFloor
	tm.extendRestartTimeLeaseLocked(now)
	if err := persistTokenSessionState(tm); err != nil {
		if !isAuthPersistenceWarning(err) {
			tm.restoreRemovedSessionsLocked(removed)
			tm.restartTimeFloor = previousFloor
		}
		return err
	}
	return nil
}

func (tm *TokenManager) cleanupExpiredSessionsLocked(now time.Time) map[string]sessionRegistryRecord {
	removed := make(map[string]sessionRegistryRecord)
	for sessionID, record := range tm.sessionRegistry {
		if !record.ExpiresAt.After(now) {
			removed[sessionID] = record
			delete(tm.sessionRegistry, sessionID)
		}
	}
	return removed
}

func (tm *TokenManager) activeSessionCountLocked(now time.Time) int {
	count := 0
	for _, record := range tm.sessionRegistry {
		if record.ExpiresAt.After(now) {
			count++
		}
	}
	return count
}

func (tm *TokenManager) activeSessionCountForUserLocked(userID string, now time.Time) int {
	count := 0
	for _, record := range tm.sessionRegistry {
		if record.UserID == userID && record.ExpiresAt.After(now) {
			count++
		}
	}
	return count
}

func (tm *TokenManager) validateActiveSessionLimitsLocked(now time.Time) error {
	if tm.activeSessionCountLocked(now) > tm.refreshSessionLimit {
		return ErrRefreshSessionLimit
	}
	perUser := make(map[string]int)
	for _, record := range tm.sessionRegistry {
		if !record.ExpiresAt.After(now) {
			continue
		}
		perUser[record.UserID]++
		if perUser[record.UserID] > tm.refreshUserSessionLimit {
			return ErrRefreshSessionLimit
		}
	}
	return nil
}

// Token errors.
var (
	ErrInvalidToken          = errors.New("invalid token")
	ErrTokenExpired          = errors.New("token expired")
	ErrTokenRevoked          = errors.New("token revoked")
	ErrRefreshRateLimited    = errors.New("refresh rotation rate limited")
	ErrRefreshSessionLimit   = errors.New("active login session limit reached")
	ErrTokenStateUnavailable = errors.New("token session state unavailable")

	errRefreshTokenReused = fmt.Errorf("refresh token reused: %w", ErrTokenRevoked)
)

func generateTokenID() (string, error) {
	b := make([]byte, 16)
	n, err := tokenRandomRead(b)
	if err != nil {
		return "", err
	}
	if n != len(b) {
		return "", io.ErrUnexpectedEOF
	}
	return hex.EncodeToString(b), nil
}

func jwtTimestamp(now time.Time) time.Time {
	return now.UTC().Truncate(jwt.TimePrecision)
}
