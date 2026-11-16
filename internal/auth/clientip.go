package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// IPExtractor extracts client IP addresses from HTTP requests with strict proxy trust verification.
//
// Invariant:
// 1. By default, RemoteAddr is always used.
// 2. X-Forwarded-For and X-Real-IP are NEVER trusted unless the immediate connecting peer (RemoteAddr)
//    is an explicitly configured trusted proxy.
// 3. When trusted proxy configuration is absent, forwarded headers are completely ignored.
type IPExtractor struct {
	trustedNets []*net.IPNet
}

// NewIPExtractor constructs an IPExtractor with the specified trusted proxy IP addresses or CIDR ranges.
// e.g. "127.0.0.1, 10.0.0.0/8, ::1"
func NewIPExtractor(trustedProxies []string) (*IPExtractor, error) {
	var nets []*net.IPNet
	for _, entry := range trustedProxies {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}

		// Try parsing as CIDR first
		_, ipNet, err := net.ParseCIDR(trimmed)
		if err == nil {
			nets = append(nets, ipNet)
			continue
		}

		// Try parsing as single IP (convert to /32 for IPv4 or /128 for IPv6)
		ip := net.ParseIP(trimmed)
		if ip == nil {
			return nil, fmt.Errorf("auth: invalid trusted proxy IP or CIDR: %q", trimmed)
		}

		var mask net.IPMask
		if ip.To4() != nil {
			mask = net.CIDRMask(32, 32)
		} else {
			mask = net.CIDRMask(128, 128)
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: mask})
	}

	return &IPExtractor{trustedNets: nets}, nil
}

// ClientIP extracts the verified client IP from the HTTP request.
func (e *IPExtractor) ClientIP(r *http.Request) string {
	peerHost := extractHost(r.RemoteAddr)
	peerIP := net.ParseIP(peerHost)
	if peerIP == nil {
		// Fallback to raw string if unable to parse
		return peerHost
	}

	// If no trusted proxies are configured, always use the connecting peer's RemoteAddr.
	if len(e.trustedNets) == 0 {
		return peerIP.String()
	}

	// If connecting peer is NOT trusted, ignore forwarded headers.
	if !e.isTrusted(peerIP) {
		return peerIP.String()
	}

	// Connecting peer is trusted; inspect X-Forwarded-For from right to left.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Walk right-to-left to find the first untrusted upstream IP
		for i := len(parts) - 1; i >= 0; i-- {
			raw := strings.TrimSpace(parts[i])
			ip := net.ParseIP(raw)
			if ip == nil {
				continue
			}
			if !e.isTrusted(ip) {
				return ip.String()
			}
		}
		// If all IPs in X-Forwarded-For are trusted, use the leftmost valid IP
		for _, part := range parts {
			raw := strings.TrimSpace(part)
			ip := net.ParseIP(raw)
			if ip != nil {
				return ip.String()
			}
		}
	}

	// Fallback to X-Real-IP if connecting peer is trusted and X-Forwarded-For is absent/unusable
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}

	return peerIP.String()
}

func (e *IPExtractor) isTrusted(ip net.IP) bool {
	for _, n := range e.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func extractHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		return host
	}
	// remoteAddr had no port or was already host only
	return strings.Trim(strings.TrimSpace(remoteAddr), "[]")
}
