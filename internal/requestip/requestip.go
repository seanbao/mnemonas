package requestip

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

var trustedProxyHops atomic.Int32

func init() {
	trustedProxyHops.Store(0)
}

func SetTrustedProxyHops(hops int) {
	if hops < 0 {
		hops = 0
	}
	trustedProxyHops.Store(int32(hops))
}

func TrustedProxyHops() int {
	return int(trustedProxyHops.Load())
}

// ClientIP returns the client IP for a request.
// Forwarded headers are only trusted when the direct peer is loopback or private.
func ClientIP(r *http.Request) string {
	remoteIP := RemoteIP(r.RemoteAddr)
	if IsTrustedForwardedSource(remoteIP) {
		if forwardedIP := ForwardedClientIP(r); forwardedIP != "" {
			return forwardedIP
		}
	}

	if remoteIP != "" {
		return remoteIP
	}

	if remoteAddr := strings.TrimSpace(r.RemoteAddr); remoteAddr != "" {
		return remoteAddr
	}

	return "unknown"
}

func ForwardedClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if selected := selectForwardedForClientIP(forwarded, TrustedProxyHops()); selected != "" {
			return selected
		}
	}

	if TrustedProxyHops() == 1 {
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			if parsed := ParseIP(realIP); parsed != nil {
				return parsed.String()
			}
		}
	}

	return ""
}

func selectForwardedForClientIP(forwarded string, trustedProxyHops int) string {
	if trustedProxyHops <= 0 {
		return ""
	}

	parts := strings.Split(forwarded, ",")
	validIPs := make([]string, 0, len(parts))
	for _, part := range parts {
		if parsed := ParseIP(strings.TrimSpace(part)); parsed != nil {
			validIPs = append(validIPs, parsed.String())
		}
	}
	if len(validIPs) < trustedProxyHops {
		return ""
	}
	return validIPs[len(validIPs)-trustedProxyHops]
}

func RemoteIP(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil && host != "" {
		return NormalizeIPString(host)
	}
	return NormalizeIPString(trimmed)
}

func NormalizeIPString(value string) string {
	if parsed := ParseIP(value); parsed != nil {
		return parsed.String()
	}
	return value
}

func ParseIP(value string) net.IP {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil && host != "" {
		trimmed = host
	} else if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if zoneIndex := strings.Index(trimmed, "%"); zoneIndex >= 0 {
		trimmed = trimmed[:zoneIndex]
	}
	return net.ParseIP(trimmed)
}

func IsTrustedForwardedSource(ip string) bool {
	parsed := ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsLoopback() || parsed.IsPrivate()
}
