package webdav

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestHandleErrorReportsUnsupportedAtomicWriteLayout(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := &Handler{}

	handler.handleError(
		recorder,
		errors.Join(errors.New("capture failed"), storage.ErrWriteAtomicRenameUnsupported),
	)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "storage layout does not support atomic write for this target") {
		t.Fatalf("response body = %q, want atomic-layout explanation", body)
	}
}
