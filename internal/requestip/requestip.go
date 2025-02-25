package requestip

import (
	"net"
	"net/http"
	"strings"
)

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
		parts := strings.Split(forwarded, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if parsed := ParseIP(strings.TrimSpace(parts[i])); parsed != nil {
				return parsed.String()
			}
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		if parsed := ParseIP(realIP); parsed != nil {
			return parsed.String()
		}
	}

	return ""
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
