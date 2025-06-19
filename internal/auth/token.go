package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func init() {
	if jwt.TimePrecision > time.Nanosecond {
		jwt.TimePrecision = time.Nanosecond
	}
}

var tokenRandomRead = rand.Read
var afterRevokeTokenCleanup = func() {}

// TokenManager handles JWT token generation and validation
type TokenManager struct {
	secretKey     []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
	issuer        string

	// Revoked tokens (for logout)
	mu            sync.RWMutex
	revokedTokens map[string]time.Time // token ID -> expiry time
	userRevokedAt map[string]time.Time // user ID -> revocation timestamp
}

// TokenClaims extends standard JWT claims
type TokenClaims struct {
	jwt.RegisteredClaims
	UserID   string `json:"uid"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
	TokenID  string `json:"jti"` // For revocation
}

// TokenPair contains access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
}

// NewTokenManager creates a new token manager
func NewTokenManager(secretKey string, accessExpiry, refreshExpiry time.Duration) *TokenManager {
	key := []byte(secretKey)
	if len(key) < 32 {
		// Generate a secure key if not provided
		key = make([]byte, 32)
		if _, err := tokenRandomRead(key); err != nil {
			fallback := sha256.Sum256([]byte(secretKey))
			key = append([]byte(nil), fallback[:]...)
		}
	}

	return &TokenManager{
		secretKey:     key,
		accessExpiry:  accessExpiry,
		refreshExpiry: refreshExpiry,
		issuer:        "mnemonas",
		revokedTokens: make(map[string]time.Time),
		userRevokedAt: make(map[string]time.Time),
	}
}

// GenerateTokenPair creates access and refresh tokens for a user
func (tm *TokenManager) GenerateTokenPair(user *User) (*TokenPair, error) {
	tm.CleanupRevokedTokens()

	now := jwtTimestamp(time.Now())
	accessExpiry := now.Add(tm.accessExpiry)
	refreshExpiry := now.Add(tm.refreshExpiry)

	// Generate unique token ID
	tokenID, err := generateTokenID()
	if err != nil {
		return nil, fmt.Errorf("generate token id: %w", err)
	}

	// Access token claims
	accessClaims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tm.issuer,
			Subject:   user.ID,
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        tokenID,
		},
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		TokenID:  tokenID,
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(tm.secretKey)
	if err != nil {
		return nil, err
	}

	// Refresh token claims (longer expiry, less info)
	refreshClaims := jwt.RegisteredClaims{
		Issuer:    tm.issuer,
		Subject:   user.ID,
		ExpiresAt: jwt.NewNumericDate(refreshExpiry),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ID:        tokenID + "-refresh",
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(tm.secretKey)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    accessExpiry,
		TokenType:    "Bearer",
	}, nil
}

// ValidateAccessToken validates an access token and returns claims
func (tm *TokenManager) ValidateAccessToken(tokenString string) (*TokenClaims, error) {
	tm.CleanupRevokedTokens()

	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return tm.secretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.UserID == "" || claims.Username == "" || claims.TokenID == "" {
		return nil, ErrInvalidToken
	}

	// Check if token is revoked
	if tm.isRevoked(claims.TokenID) {
		return nil, ErrTokenRevoked
	}

	if tm.isUserRevoked(claims.UserID, claims.IssuedAt) {
		return nil, ErrTokenRevoked
	}

	return claims, nil
}

func (tm *TokenManager) validateRefreshTokenClaims(tokenString string) (*jwt.RegisteredClaims, error) {
	tm.CleanupRevokedTokens()

	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return tm.secretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	if !strings.HasSuffix(claims.ID, "-refresh") {
		return nil, ErrInvalidToken
	}

	if tm.isRevoked(claims.ID) {
		return nil, ErrTokenRevoked
	}

	// Check if associated access token is revoked
	accessTokenID := strings.TrimSuffix(claims.ID, "-refresh")
	if tm.isRevoked(accessTokenID) {
		return nil, ErrTokenRevoked
	}

	if tm.isUserRevoked(claims.Subject, claims.IssuedAt) {
		return nil, ErrTokenRevoked
	}

	return claims, nil
}

// ValidateRefreshToken validates a refresh token
func (tm *TokenManager) ValidateRefreshToken(tokenString string) (string, error) {
	claims, err := tm.validateRefreshTokenClaims(tokenString)
	if err != nil {
		return "", err
	}

	return claims.Subject, nil
}

// RevokeToken revokes a token by ID
func (tm *TokenManager) RevokeToken(tokenID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	tm.cleanupRevokedTokensLocked(now)
	afterRevokeTokenCleanup()

	// Store with expiry time for cleanup
	tm.revokedTokens[tokenID] = now.Add(tm.refreshExpiry)
}

// RevokeByUser revokes all tokens for a user (used when password changed)
func (tm *TokenManager) RevokeByUser(userID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	tm.cleanupRevokedTokensLocked(now)
	tm.userRevokedAt[userID] = jwtTimestamp(now)
}

// isRevoked checks if a token is revoked
func (tm *TokenManager) isRevoked(tokenID string) bool {
	tm.mu.RLock()
	expiry, revoked := tm.revokedTokens[tokenID]
	tm.mu.RUnlock()

	if !revoked {
		return false
	}

	if time.Now().After(expiry) {
		tm.mu.Lock()
		if currentExpiry, ok := tm.revokedTokens[tokenID]; ok && time.Now().After(currentExpiry) {
			delete(tm.revokedTokens, tokenID)
		}
		tm.mu.Unlock()
		return false
	}

	return true
}

// CleanupRevokedTokens removes expired revoked tokens
func (tm *TokenManager) CleanupRevokedTokens() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.cleanupRevokedTokensLocked(time.Now())
}

func (tm *TokenManager) cleanupRevokedTokensLocked(now time.Time) {
	for id, expiry := range tm.revokedTokens {
		if now.After(expiry) {
			delete(tm.revokedTokens, id)
		}
	}

	for userID, revokedAt := range tm.userRevokedAt {
		if now.Sub(revokedAt) > tm.refreshExpiry {
			delete(tm.userRevokedAt, userID)
		}
	}
}

func (tm *TokenManager) isUserRevoked(userID string, issuedAt *jwt.NumericDate) bool {
	if issuedAt == nil {
		return false
	}

	tm.mu.RLock()
	revokedAt, revoked := tm.userRevokedAt[userID]
	tm.mu.RUnlock()

	if !revoked {
		return false
	}

	return !issuedAt.Time.After(revokedAt)
}

// Token errors
var (
	ErrInvalidToken = errors.New("invalid token")
	ErrTokenExpired = errors.New("token expired")
	ErrTokenRevoked = errors.New("token revoked")
)

func generateTokenID() (string, error) {
	b := make([]byte, 16)
	if _, err := tokenRandomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func jwtTimestamp(now time.Time) time.Time {
	return now.UTC().Truncate(jwt.TimePrecision)
}
