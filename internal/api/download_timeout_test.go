package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsStreamingDownloadRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		want   bool
	}{
		{name: "authenticated file", method: http.MethodGet, target: "/api/v1/download/file.bin", want: true},
		{name: "authenticated archive", method: http.MethodGet, target: "/api/v1/download/docs?archive=zip", want: true},
		{name: "short public root", method: http.MethodGet, target: "/s/share-id/download", want: true},
		{name: "short public child", method: http.MethodGet, target: "/s/share-id/download/folder/file.bin", want: true},
		{name: "api public root", method: http.MethodGet, target: "/api/v1/public/shares/share-id/download", want: true},
		{name: "api public child", method: http.MethodGet, target: "/api/v1/public/shares/share-id/download/folder/file.bin", want: true},
		{name: "ticket post", method: http.MethodPost, target: "/api/v1/public/shares/share-id/download-ticket", want: false},
		{name: "download head", method: http.MethodHead, target: "/api/v1/download/file.bin", want: false},
		{name: "auth session", method: http.MethodGet, target: "/api/v1/auth/download-session", want: false},
		{name: "authenticated lookalike", method: http.MethodGet, target: "/api/v1/download-ticket", want: false},
		{name: "short public ticket", method: http.MethodGet, target: "/s/share-id/download-ticket", want: false},
		{name: "short public missing id", method: http.MethodGet, target: "/s//download", want: false},
		{name: "api public missing id", method: http.MethodGet, target: "/api/v1/public/shares//download", want: false},
		{name: "api public items", method: http.MethodGet, target: "/api/v1/public/shares/share-id/items", want: false},
		{name: "unrelated prefix", method: http.MethodGet, target: "/other/api/v1/download/file.bin", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			if got := isStreamingDownloadRequest(request); got != test.want {
				t.Fatalf("isStreamingDownloadRequest() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRequestTimeoutExceptStreamingDownloads(t *testing.T) {
	handler := requestTimeoutExceptStreamingDownloads(time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline := r.Context().Deadline()
		if hasDeadline {
			w.Header().Set("X-Test-Deadline", "present")
		} else {
			w.Header().Set("X-Test-Deadline", "absent")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name         string
		method       string
		target       string
		wantDeadline string
	}{
		{name: "ordinary API", method: http.MethodGet, target: "/api/v1/files", wantDeadline: "present"},
		{name: "ticket issuance", method: http.MethodPost, target: "/s/share-id/download-ticket", wantDeadline: "present"},
		{name: "authenticated stream", method: http.MethodGet, target: "/api/v1/download/file.bin", wantDeadline: "absent"},
		{name: "public stream", method: http.MethodGet, target: "/s/share-id/download/file.bin", wantDeadline: "absent"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
			}
			if got := response.Header().Get("X-Test-Deadline"); got != test.wantDeadline {
				t.Fatalf("deadline header = %q, want %q", got, test.wantDeadline)
			}
		})
	}
}
