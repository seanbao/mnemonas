// Package api provides REST API and error handling utilities
package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// APIError represents a structured API error response
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Error codes
const (
	ErrCodeBadRequest      = "BAD_REQUEST"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeConflict        = "CONFLICT"
	ErrCodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
	ErrCodeQuotaExceeded   = "QUOTA_EXCEEDED"
	ErrCodeInternal        = "INTERNAL_ERROR"
	ErrCodeServiceUnavail  = "SERVICE_UNAVAILABLE"
	ErrCodeUnauthorized    = "UNAUTHORIZED"
	ErrCodeForbidden       = "FORBIDDEN"
)

// NewAPIError creates a new API error
func NewAPIError(code, message string) *APIError {
	return &APIError{
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// WithDetails adds details to the error
func (e *APIError) WithDetails(details any) *APIError {
	e.Details = details
	return e
}

// WithRequestID adds request ID to the error
func (e *APIError) WithRequestID(id string) *APIError {
	e.RequestID = id
	return e
}

// Write writes the error as JSON response
func (e *APIError) Write(w http.ResponseWriter, status int) error {
	payload, err := json.Marshal(e)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

// Common error responses
func BadRequest(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeBadRequest, message).Write(w, http.StatusBadRequest)
}

func NotFound(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeNotFound, message).Write(w, http.StatusNotFound)
}

func Conflict(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeConflict, message).Write(w, http.StatusConflict)
}

func InternalError(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeInternal, message).Write(w, http.StatusInternalServerError)
}

func ServiceUnavailable(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeServiceUnavail, message).Write(w, http.StatusServiceUnavailable)
}

func Unauthorized(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeUnauthorized, message).Write(w, http.StatusUnauthorized)
}

func Forbidden(w http.ResponseWriter, message string) {
	NewAPIError(ErrCodeForbidden, message).Write(w, http.StatusForbidden)
}

// APIResponse represents a successful API response
type APIResponse struct {
	Success   bool   `json:"success"`
	Data      any    `json:"data,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Timestamp string `json:"timestamp"`
}

// NewAPIResponse creates a new API response
func NewAPIResponse(data any) *APIResponse {
	if data == nil {
		data = json.RawMessage("null")
	}
	return &APIResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// WithMessage adds a message to the response
func (r *APIResponse) WithMessage(message string) *APIResponse {
	r.Message = message
	return r
}

// WithRequestID adds request ID to the response
func (r *APIResponse) WithRequestID(id string) *APIResponse {
	r.RequestID = id
	return r
}

// Write writes the response as JSON
func (r *APIResponse) Write(w http.ResponseWriter, status int) error {
	payload, err := json.Marshal(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}
