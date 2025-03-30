package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingResponseWriter struct {
	header http.Header
	code   int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

func (w *failingResponseWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestNewAPIError(t *testing.T) {
	err := NewAPIError("TEST_CODE", "test message")

	if err.Code != "TEST_CODE" {
		t.Errorf("Code = %q, want %q", err.Code, "TEST_CODE")
	}
	if err.Message != "test message" {
		t.Errorf("Message = %q, want %q", err.Message, "test message")
	}
	if err.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
}

func TestAPIError_WithDetails(t *testing.T) {
	details := map[string]string{"field": "value"}
	err := NewAPIError("CODE", "message").WithDetails(details)

	if err.Details == nil {
		t.Error("Details should not be nil")
	}

	d, ok := err.Details.(map[string]string)
	if !ok {
		t.Error("Details type mismatch")
	}
	if d["field"] != "value" {
		t.Errorf("Details field = %q, want %q", d["field"], "value")
	}
}

func TestAPIError_WithRequestID(t *testing.T) {
	err := NewAPIError("CODE", "message").WithRequestID("req-123")

	if err.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want %q", err.RequestID, "req-123")
	}
}

func TestAPIError_Write(t *testing.T) {
	w := httptest.NewRecorder()
	err := NewAPIError("TEST_CODE", "test message")
	if writeErr := err.Write(w, http.StatusBadRequest); writeErr != nil {
		t.Fatalf("Write() error: %v", writeErr)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var response APIError
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Code != "TEST_CODE" {
		t.Errorf("Response code = %q, want %q", response.Code, "TEST_CODE")
	}
}

func TestAPIError_Write_ReturnsEncodingError(t *testing.T) {
	w := &failingResponseWriter{}
	err := NewAPIError("TEST_CODE", "test message")

	writeErr := err.Write(w, http.StatusBadRequest)
	if writeErr == nil {
		t.Fatal("expected write error")
	}
}

func TestAPIError_Write_InvalidDetailsFailsClosed(t *testing.T) {
	w := httptest.NewRecorder()
	err := NewAPIError("TEST_CODE", "test message").WithDetails(map[string]any{"bad": make(chan int)})

	writeErr := err.Write(w, http.StatusBadRequest)
	if writeErr == nil {
		t.Fatal("expected marshal error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if w.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", w.Body.String())
	}
}

func TestBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	BadRequest(w, "invalid input")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeBadRequest) {
		t.Errorf("Response should contain %q", ErrCodeBadRequest)
	}
	if !strings.Contains(body, "invalid input") {
		t.Error("Response should contain message")
	}
}

func TestNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	NotFound(w, "resource not found")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeNotFound) {
		t.Errorf("Response should contain %q", ErrCodeNotFound)
	}
}

func TestConflict(t *testing.T) {
	w := httptest.NewRecorder()
	Conflict(w, "resource already exists")

	if w.Code != http.StatusConflict {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusConflict)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeConflict) {
		t.Errorf("Response should contain %q", ErrCodeConflict)
	}
}

func TestInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	InternalError(w, "something went wrong")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeInternal) {
		t.Errorf("Response should contain %q", ErrCodeInternal)
	}
}

func TestServiceUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	ServiceUnavailable(w, "service down")

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeServiceUnavail) {
		t.Errorf("Response should contain %q", ErrCodeServiceUnavail)
	}
}

func TestUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	Unauthorized(w, "authentication required")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeUnauthorized) {
		t.Errorf("Response should contain %q", ErrCodeUnauthorized)
	}
}

func TestForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	Forbidden(w, "access denied")

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}

	body := w.Body.String()
	if !strings.Contains(body, ErrCodeForbidden) {
		t.Errorf("Response should contain %q", ErrCodeForbidden)
	}
}

func TestNewAPIResponse(t *testing.T) {
	data := map[string]int{"count": 42}
	resp := NewAPIResponse(data)

	if !resp.Success {
		t.Error("Success should be true")
	}
	if resp.Data == nil {
		t.Error("Data should not be nil")
	}
	if resp.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
}

func TestAPIResponse_WithMessage(t *testing.T) {
	resp := NewAPIResponse(nil).WithMessage("operation completed")

	if resp.Message != "operation completed" {
		t.Errorf("Message = %q, want %q", resp.Message, "operation completed")
	}
}

func TestAPIResponse_WithRequestID(t *testing.T) {
	resp := NewAPIResponse(nil).WithRequestID("req-456")

	if resp.RequestID != "req-456" {
		t.Errorf("RequestID = %q, want %q", resp.RequestID, "req-456")
	}
}

func TestAPIResponse_Write(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"status": "ok"}
	resp := NewAPIResponse(data)
	if writeErr := resp.Write(w, http.StatusCreated); writeErr != nil {
		t.Fatalf("Write() error: %v", writeErr)
	}

	if w.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusCreated)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var response APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if !response.Success {
		t.Error("Response success should be true")
	}
}

func TestAPIResponse_Write_IncludesNullDataForNilPayload(t *testing.T) {
	w := httptest.NewRecorder()
	resp := NewAPIResponse(nil).WithMessage("operation completed")
	if writeErr := resp.Write(w, http.StatusOK); writeErr != nil {
		t.Fatalf("Write() error: %v", writeErr)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	data, ok := payload["data"]
	if !ok {
		t.Fatalf("expected response to include data field, got %s", w.Body.String())
	}
	if string(data) != "null" {
		t.Fatalf("expected data field to be null, got %s", string(data))
	}
}

func TestAPIResponse_Write_ReturnsEncodingError(t *testing.T) {
	w := &failingResponseWriter{}
	resp := NewAPIResponse(map[string]string{"status": "ok"})

	writeErr := resp.Write(w, http.StatusCreated)
	if writeErr == nil {
		t.Fatal("expected write error")
	}
}

func TestAPIResponse_Write_InvalidDataFailsClosed(t *testing.T) {
	w := httptest.NewRecorder()
	resp := NewAPIResponse(map[string]any{"bad": make(chan int)})

	writeErr := resp.Write(w, http.StatusCreated)
	if writeErr == nil {
		t.Fatal("expected marshal error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if w.Body.String() != "Internal Server Error\n" {
		t.Fatalf("expected internal server error body, got %q", w.Body.String())
	}
}

func TestAPIError_Chaining(t *testing.T) {
	err := NewAPIError("CODE", "message").
		WithDetails(map[string]string{"key": "value"}).
		WithRequestID("req-789")

	if err.Code != "CODE" {
		t.Error("Code lost during chaining")
	}
	if err.Details == nil {
		t.Error("Details lost during chaining")
	}
	if err.RequestID != "req-789" {
		t.Error("RequestID lost during chaining")
	}
}

func TestAPIResponse_Chaining(t *testing.T) {
	resp := NewAPIResponse(nil).
		WithMessage("success").
		WithRequestID("req-abc")

	if resp.Message != "success" {
		t.Error("Message lost during chaining")
	}
	if resp.RequestID != "req-abc" {
		t.Error("RequestID lost during chaining")
	}
}

func TestErrorCodes(t *testing.T) {
	// Verify error code constants are defined correctly
	codes := []string{
		ErrCodeBadRequest,
		ErrCodeNotFound,
		ErrCodeConflict,
		ErrCodeInternal,
		ErrCodeServiceUnavail,
		ErrCodeUnauthorized,
		ErrCodeForbidden,
	}

	for _, code := range codes {
		if code == "" {
			t.Error("Error code should not be empty")
		}
	}
}
