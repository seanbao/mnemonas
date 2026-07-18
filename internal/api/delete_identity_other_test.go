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

func TestServerDownloadIdentityConditionFailsClosedOnUnsupportedPlatform(t *testing.T) {
	server, fs, _ := setupTestServer(t)
	if err := fs.WriteFile(context.Background(), "/download.bin", strings.NewReader("content")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	headReq := httptest.NewRequest(http.MethodHead, "/api/v1/download/download.bin", nil)
	headRec := httptest.NewRecorder()
	server.Router().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d, want %d", headRec.Code, http.StatusOK)
	}
	if identity := headRec.Header().Get(downloadIdentityHeader); identity != "" {
		t.Fatalf("unsupported download identity = %q, want empty", identity)
	}

	resumeReq := httptest.NewRequest(http.MethodGet, "/api/v1/download/download.bin", nil)
	resumeReq.Header.Set(downloadIdentityPreconditionHeader, strings.Repeat("a", 64))
	resumeRec := httptest.NewRecorder()
	server.Router().ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusPreconditionFailed {
		t.Fatalf("conditional GET status=%d, want %d; body=%s", resumeRec.Code, http.StatusPreconditionFailed, resumeRec.Body.String())
	}
	if contentLength := resumeRec.Header().Get("Content-Length"); contentLength != "0" {
		t.Fatalf("conditional GET Content-Length=%q, want 0", contentLength)
	}
	if resumeRec.Body.Len() != 0 {
		t.Fatalf("conditional GET body length=%d, want 0", resumeRec.Body.Len())
	}
}
