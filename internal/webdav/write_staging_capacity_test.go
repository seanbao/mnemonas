package webdav

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestHandleErrorMapsWriteStagingInsufficientStorageWithoutRetryAfter(t *testing.T) {
	response := httptest.NewRecorder()
	(&Handler{}).handleError(
		response,
		errors.Join(errors.New("reserve staging"), storage.ErrInsufficientStorage),
	)
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInsufficientStorage)
	}
	if got := response.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty for capacity exhaustion", got)
	}
}
