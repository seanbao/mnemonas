package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunRejectsRequestError(t *testing.T) {
	getenv := func(key string) string {
		if key == "MNEMONAS_HEALTHCHECK_URL" {
			return "http://127.0.0.1:1/health"
		}
		return ""
	}

	var stderr bytes.Buffer

	if code := run(getenv, &stderr); code != 1 {
		t.Fatalf("run() = %d, want 1 for request error", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected an error message")
	}
}

func TestRunRejectsInvalidURL(t *testing.T) {
	tests := []string{
		"://invalid-url",
		"file:///etc/passwd",
		"http://127.0.0.1:8080/health\nX=1",
		"/health",
	}

	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "MNEMONAS_HEALTHCHECK_URL" {
					return rawURL
				}
				return ""
			}

			var stderr bytes.Buffer
			if code := run(getenv, &stderr); code != 2 {
				t.Fatalf("run() = %d, want 2; stderr=%s", code, stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("expected an error message")
			}
		})
	}
}

func TestRunAcceptsHealthyStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "MNEMONAS_HEALTHCHECK_URL" {
			return server.URL + "/health"
		}
		return ""
	}

	var stderr bytes.Buffer
	if code := run(getenv, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%s", code, stderr.String())
	}
}

func TestRunRejectsUnhealthyStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "MNEMONAS_HEALTHCHECK_URL" {
			return server.URL
		}
		return ""
	}

	var stderr bytes.Buffer
	if code := run(getenv, &stderr); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected an error message")
	}
}

func TestRunRejectsInvalidTimeout(t *testing.T) {
	getenv := func(key string) string {
		if key == "MNEMONAS_HEALTHCHECK_TIMEOUT" {
			return "not-a-duration"
		}
		return ""
	}

	var stderr bytes.Buffer
	if code := run(getenv, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}

func TestRunRejectsNonPositiveTimeout(t *testing.T) {
	getenv := func(key string) string {
		if key == "MNEMONAS_HEALTHCHECK_TIMEOUT" {
			return "0s"
		}
		return ""
	}

	var stderr bytes.Buffer
	if code := run(getenv, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
}
