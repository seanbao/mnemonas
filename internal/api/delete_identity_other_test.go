//go:build !linux && !darwin

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerObservedDeleteIdentityUnavailableOnUnsupportedPlatform(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	if err := fs.WriteFile(context.Background(), "/item.bin", strings.NewReader("item")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/", nil)
	listRec := httptest.NewRecorder()
	server.Router().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"deleteIdentityToken":null`) {
		t.Fatalf("unsupported list response status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	intentReq := httptest.NewRequest(http.MethodPost, "/api/v1/files-delete-intents", strings.NewReader(`{"targets":[{"path":"/item.bin","observedIdentityToken":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	intentReq.Header.Set("Content-Type", "application/json")
	intentRec := httptest.NewRecorder()
	server.Router().ServeHTTP(intentRec, intentReq)
	if intentRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported intent status=%d, want %d; body=%s", intentRec.Code, http.StatusServiceUnavailable, intentRec.Body.String())
	}
}
