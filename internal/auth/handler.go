package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Handler provides HTTP handlers for authentication
type Handler struct {
	userStore    *UserStore
	tokenManager *TokenManager
}

// NewHandler creates a new auth handler
func NewHandler(us *UserStore, tm *TokenManager) *Handler {
	return &Handler{
		userStore:    us,
		tokenManager: tm,
	}
}

// LoginRequest is the login request body
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the login response body
type LoginResponse struct {
	Success      bool      `json:"success"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	User         UserInfo  `json:"user"`
}

// UserInfo is public user information
type UserInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Role     Role   `json:"role"`
	HomeDir  string `json:"home_dir"`
}

// RefreshRequest is the refresh token request body
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// ErrorResponse is an error response body
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

// HandleLogin handles POST /api/v1/auth/login
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "MISSING_CREDENTIALS")
		return
	}

	user, err := h.userStore.Authenticate(req.Username, req.Password)
	if err != nil {
		switch err {
		case ErrInvalidCredentials:
			writeError(w, http.StatusUnauthorized, "invalid username or password", "INVALID_CREDENTIALS")
		case ErrUserDisabled:
			writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
		default:
			writeError(w, http.StatusInternalServerError, "authentication failed", "AUTH_ERROR")
		}
		return
	}

	tokenPair, err := h.tokenManager.GenerateTokenPair(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", "TOKEN_ERROR")
		return
	}

	resp := LoginResponse{
		Success:      true,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
		User: UserInfo{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			HomeDir:  user.HomeDir,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleRefresh handles POST /api/v1/auth/refresh
func (h *Handler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh token required", "MISSING_TOKEN")
		return
	}

	userID, err := h.tokenManager.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		switch err {
		case ErrTokenExpired:
			writeError(w, http.StatusUnauthorized, "refresh token expired", "TOKEN_EXPIRED")
		case ErrTokenRevoked:
			writeError(w, http.StatusUnauthorized, "token has been revoked", "TOKEN_REVOKED")
		default:
			writeError(w, http.StatusUnauthorized, "invalid refresh token", "INVALID_TOKEN")
		}
		return
	}

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found", "USER_NOT_FOUND")
		return
	}

	if user.Disabled {
		writeError(w, http.StatusForbidden, "user account is disabled", "USER_DISABLED")
		return
	}

	tokenPair, err := h.tokenManager.GenerateTokenPair(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", "TOKEN_ERROR")
		return
	}

	resp := LoginResponse{
		Success:      true,
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		TokenType:    tokenPair.TokenType,
		User: UserInfo{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			HomeDir:  user.HomeDir,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleLogout handles POST /api/v1/auth/logout
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	claims := GetClaimsFromContext(r.Context())
	if claims != nil {
		h.tokenManager.RevokeToken(claims.TokenID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "logged out successfully",
	})
}

// HandleMe handles GET /api/v1/auth/me
func (h *Handler) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}

	resp := map[string]interface{}{
		"success": true,
		"user": UserInfo{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			HomeDir:  user.HomeDir,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ChangePasswordRequest is the change password request body
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// HandleChangePassword handles POST /api/v1/auth/password
func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", "NOT_AUTHENTICATED")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old and new password required", "MISSING_PASSWORD")
		return
	}

	if err := h.userStore.ChangePassword(user.ID, req.OldPassword, req.NewPassword); err != nil {
		switch err {
		case ErrInvalidCredentials:
			writeError(w, http.StatusUnauthorized, "current password is incorrect", "INVALID_PASSWORD")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
		default:
			writeError(w, http.StatusInternalServerError, "failed to change password", "PASSWORD_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	h.tokenManager.RevokeByUser(user.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "password changed successfully",
	})
}

// Admin endpoints

// CreateUserRequest is the create user request body
type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

// HandleListUsers handles GET /api/v1/admin/users
func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	users := h.userStore.List()
	userInfos := make([]map[string]interface{}, 0, len(users))

	for _, u := range users {
		info := map[string]interface{}{
			"id":          u.ID,
			"username":    u.Username,
			"email":       u.Email,
			"role":        u.Role,
			"disabled":    u.Disabled,
			"home_dir":    u.HomeDir,
			"created_at":  u.CreatedAt,
			"updated_at":  u.UpdatedAt,
			"quota_bytes": u.QuotaBytes,
			"used_bytes":  u.UsedBytes,
		}
		if u.LastLoginAt != nil {
			info["last_login_at"] = u.LastLoginAt
		}
		userInfos = append(userInfos, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"users":   userInfos,
		"total":   len(userInfos),
	})
}

// HandleCreateUser handles POST /api/v1/admin/users
func (h *Handler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "MISSING_FIELDS")
		return
	}

	role := Role(strings.ToLower(req.Role))
	if role == "" {
		role = RoleUser
	}
	if role != RoleAdmin && role != RoleUser && role != RoleGuest {
		writeError(w, http.StatusBadRequest, "invalid role, must be admin, user, or guest", "INVALID_ROLE")
		return
	}

	user, err := h.userStore.Create(req.Username, req.Password, req.Email, role)
	if err != nil {
		switch err {
		case ErrUserExists:
			writeError(w, http.StatusConflict, "user already exists", "USER_EXISTS")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create user", "CREATE_ERROR")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"user": UserInfo{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
			HomeDir:  user.HomeDir,
		},
	})
}

// HandleDeleteUser handles DELETE /api/v1/admin/users/{id}
func (h *Handler) HandleDeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	// Prevent self-deletion
	currentUser := GetUserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID {
		writeError(w, http.StatusBadRequest, "cannot delete your own account", "SELF_DELETE")
		return
	}

	if err := h.userStore.Delete(userID); err != nil {
		switch err {
		case ErrUserNotFound:
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case ErrLastAdmin:
			writeError(w, http.StatusBadRequest, "cannot delete last admin user", "LAST_ADMIN")
		default:
			writeError(w, http.StatusInternalServerError, "failed to delete user", "DELETE_ERROR")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "user deleted successfully",
	})
}

// HandleResetUserPassword handles POST /api/v1/admin/users/{id}/reset-password
func (h *Handler) HandleResetUserPassword(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new password required", "MISSING_PASSWORD")
		return
	}

	if err := h.userStore.ResetPassword(userID, req.NewPassword); err != nil {
		switch err {
		case ErrUserNotFound:
			writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		case ErrPasswordTooShort:
			writeError(w, http.StatusBadRequest, "password must be at least 8 characters", "PASSWORD_TOO_SHORT")
		default:
			writeError(w, http.StatusInternalServerError, "failed to reset password", "RESET_ERROR")
		}
		return
	}

	// Revoke all tokens for this user
	h.tokenManager.RevokeByUser(userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "password reset successfully",
	})
}

// HandleToggleUserStatus handles PUT /api/v1/admin/users/{id}/status
func (h *Handler) HandleToggleUserStatus(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	if !IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin access required", "FORBIDDEN")
		return
	}

	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}

	// Prevent disabling self
	currentUser := GetUserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == userID && req.Disabled {
		writeError(w, http.StatusBadRequest, "cannot disable your own account", "SELF_DISABLE")
		return
	}

	// Prevent disabling last admin
	if user.Role == RoleAdmin && req.Disabled {
		// Count active admins
		activeAdmins := 0
		for _, u := range h.userStore.List() {
			if u.Role == RoleAdmin && !u.Disabled {
				activeAdmins++
			}
		}
		if activeAdmins <= 1 {
			writeError(w, http.StatusBadRequest, "cannot disable last admin user", "LAST_ADMIN")
			return
		}
	}

	user.Disabled = req.Disabled
	if err := h.userStore.Update(user); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user status", "UPDATE_ERROR")
		return
	}

	// If disabling, revoke all tokens
	if req.Disabled {
		h.tokenManager.RevokeByUser(userID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"disabled": req.Disabled,
		"message":  "user status updated successfully",
	})
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Success: false,
		Error:   message,
		Code:    code,
	})
}
