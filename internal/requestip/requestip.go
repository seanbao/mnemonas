package requestip

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

var trustedProxyHops atomic.Int32
var trustedProxyCIDRs atomic.Value

func init() {
	trustedProxyHops.Store(0)
	trustedProxyCIDRs.Store([]*net.IPNet(nil))
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

func SetTrustedProxyCIDRs(cidrs []string) error {
	networks, err := parseTrustedProxyCIDRs(cidrs)
	if err != nil {
		return err
	}
	trustedProxyCIDRs.Store(networks)
	return nil
}

func TrustedProxyCIDRs() []string {
	networks := trustedProxyNetworks()
	if len(networks) == 0 {
		return nil
	}
	values := make([]string, 0, len(networks))
	for _, network := range networks {
		if network != nil {
			values = append(values, network.String())
		}
	}
	return values
}

// ClientIP returns the client IP for a request.
// Forwarded headers are only trusted when the direct peer is loopback or an
// explicitly configured trusted proxy.
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
	if parsed.IsLoopback() {
		return true
	}
	for _, network := range trustedProxyNetworks() {
		if network != nil && network.Contains(parsed) {
			return true
		}
	}
	return false
}

func parseTrustedProxyCIDRs(cidrs []string) ([]*net.IPNet, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}

	networks := make([]*net.IPNet, 0, len(cidrs))
	for index, raw := range cidrs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		network, err := parseTrustedProxyCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy CIDR %d: %w", index, err)
		}
		networks = append(networks, network)
	}
	return networks, nil
}

func parseTrustedProxyCIDR(value string) (*net.IPNet, error) {
	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		network.IP = normalizeNetworkIP(network.IP)
		return network, nil
	}

	ip := ParseIP(value)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", value)
	}
	ip = normalizeNetworkIP(ip)
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}

func normalizeNetworkIP(ip net.IP) net.IP {
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4
	}
	return ip
}

func trustedProxyNetworks() []*net.IPNet {
	value := trustedProxyCIDRs.Load()
	if value == nil {
		return nil
	}
	networks, ok := value.([]*net.IPNet)
	if !ok || len(networks) == 0 {
		return nil
	}
	return networks
}
