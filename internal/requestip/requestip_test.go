package requestip

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setTrustedProxyHopsForTest(t *testing.T, hops int) {
	t.Helper()
	previous := TrustedProxyHops()
	SetTrustedProxyHops(hops)
	t.Cleanup(func() {
		SetTrustedProxyHops(previous)
	})
}

func setTrustedProxyCIDRsForTest(t *testing.T, cidrs []string) {
	t.Helper()
	previous := TrustedProxyCIDRs()
	if err := SetTrustedProxyCIDRs(cidrs); err != nil {
		t.Fatalf("SetTrustedProxyCIDRs(%v) error: %v", cidrs, err)
	}
	t.Cleanup(func() {
		if err := SetTrustedProxyCIDRs(previous); err != nil {
			t.Fatalf("restore trusted proxy CIDRs error: %v", err)
		}
	})
}

func TestClientIP_IgnoresSpoofedForwardedHeadersFromUntrustedSource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "203.0.113.5" {
		t.Fatalf("ClientIP() = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientIP_DoesNotTrustPrivateProxyWithoutExplicitCIDR(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "10.0.0.2" {
		t.Fatalf("ClientIP() = %q, want %q", got, "10.0.0.2")
	}
}

func TestClientIP_UsesLastForwardedAddressFromTrustedProxy(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 198.51.100.20")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_FallsBackToXRealIPWhenForwardedForMissing(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)
	setTrustedProxyCIDRsForTest(t, []string{"10.0.0.0/8"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "198.51.100.21" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.21")
	}
}

func TestParseIP_AllowsCommonForwardedHeaderAddressForms(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "ipv4 host port", value: "198.51.100.20:443", want: "198.51.100.20"},
		{name: "bracketed ipv6 host port", value: "[2001:db8::10]:443", want: "2001:db8::10"},
		{name: "bracketed ipv6", value: "[2001:db8::10]", want: "2001:db8::10"},
		{name: "ipv6 zone", value: "fe80::10%eth0", want: "fe80::10"},
		{name: "bracketed ipv6 zone host port", value: "[fe80::10%eth0]:8443", want: "fe80::10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseIP(tt.value)
			if parsed == nil {
				t.Fatalf("ParseIP(%q) returned nil", tt.value)
			}
			if got := parsed.String(); got != tt.want {
				t.Fatalf("ParseIP(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestClientIP_UsesForwardedHeadersWithPortsFromTrustedProxy(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.99:8443, 198.51.100.20:443")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_IgnoresSpoofedLeadingForwardedEntriesFromTrustedProxy(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.250, 198.51.100.20")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_UsesBracketedIPv6FromTrustedProxy(t *testing.T) {
	setTrustedProxyHopsForTest(t, 1)
	setTrustedProxyCIDRsForTest(t, []string{"fd00::/8"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[fd00::1]:8080"
	req.Header.Set("X-Real-IP", "[2001:db8::20]:8443")

	if got := ClientIP(req); got != net.ParseIP("2001:db8::20").String() {
		t.Fatalf("ClientIP() = %q, want %q", got, net.ParseIP("2001:db8::20").String())
	}
}

func TestClientIP_UsesConfiguredTrustedProxyHops(t *testing.T) {
	setTrustedProxyHopsForTest(t, 2)
	setTrustedProxyCIDRsForTest(t, []string{"10.0.0.0/8"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 203.0.113.30")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_ConfiguredTrustedProxyHopsStillIgnoreSpoofedLeadingEntries(t *testing.T) {
	setTrustedProxyHopsForTest(t, 2)
	setTrustedProxyCIDRsForTest(t, []string{"10.0.0.0/8"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.250, 198.51.100.20, 203.0.113.30")

	if got := ClientIP(req); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.20")
	}
}

func TestClientIP_TrustedProxyHopsZeroDisablesForwardedHeaders(t *testing.T) {
	setTrustedProxyHopsForTest(t, 0)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:8080"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "198.51.100.21")

	if got := ClientIP(req); got != "10.0.0.2" {
		t.Fatalf("ClientIP() = %q, want %q", got, "10.0.0.2")
	}
}

func TestSetTrustedProxyCIDRsRejectsInvalidCIDR(t *testing.T) {
	if err := SetTrustedProxyCIDRs([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected invalid trusted proxy CIDR error")
	}
}
