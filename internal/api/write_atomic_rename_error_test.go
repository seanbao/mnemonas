package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestRespondStreamedWriteStateErrorReportsUnsupportedAtomicLayout(t *testing.T) {
	recorder := httptest.NewRecorder()
	handled := respondStreamedWriteStateError(
		recorder,
		errors.Join(errors.New("capture failed"), storage.ErrWriteAtomicRenameUnsupported),
	)

	if !handled {
		t.Fatal("respondStreamedWriteStateError() handled = false, want true")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "storage layout does not support atomic writes for this target") {
		t.Fatalf("response body = %q, want atomic-layout explanation", body)
	}
}
