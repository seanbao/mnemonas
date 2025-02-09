package requestip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP_IgnoresSpoofedForwardedHeadersFromUntrustedSource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "203.0.113.5" {
		t.Fatalf("ClientIP() = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientIP_UsesForwardedHeadersFromTrustedProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 127.0.0.1")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_FallsBackToXRealIPWhenForwardedForMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "198.51.100.21" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.21")
	}
}
