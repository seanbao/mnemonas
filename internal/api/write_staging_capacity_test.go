package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestRespondStreamedWriteStateErrorMapsInsufficientStorageWithoutRetryAfter(t *testing.T) {
	response := httptest.NewRecorder()
	if handled := respondStreamedWriteStateError(
		response,
		errors.Join(errors.New("reserve staging"), storage.ErrInsufficientStorage),
	); !handled {
		t.Fatal("respondStreamedWriteStateError() handled = false, want true")
	}
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInsufficientStorage)
	}
	if got := response.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty for capacity exhaustion", got)
	}
	var apiErr APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode response error = %v; body=%s", err, response.Body.String())
	}
	if apiErr.Code != ErrCodeInsufficientStorage {
		t.Fatalf("error code = %q, want %q", apiErr.Code, ErrCodeInsufficientStorage)
	}
}
