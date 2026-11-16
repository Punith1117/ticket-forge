package auth_test

import (
	"net/http"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/auth"
)

func TestIPExtractor_NoTrustedProxies(t *testing.T) {
	extractor, err := auth.NewIPExtractor(nil)
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	req, _ := http.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "203.0.113.10:45678"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.2")

	// Absent trusted proxy configuration: MUST use RemoteAddr and ignore headers
	ip := extractor.ClientIP(req)
	if ip != "203.0.113.10" {
		t.Errorf("expected 203.0.113.10, got %s", ip)
	}
}

func TestIPExtractor_UntrustedConnectingPeer(t *testing.T) {
	// Configure 10.0.0.1 as trusted proxy, but incoming request is from 203.0.113.10
	extractor, err := auth.NewIPExtractor([]string{"10.0.0.1"})
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	req, _ := http.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "203.0.113.10:45678"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.2")

	// Untrusted connecting peer: MUST ignore spoofed headers
	ip := extractor.ClientIP(req)
	if ip != "203.0.113.10" {
		t.Errorf("expected 203.0.113.10, got %s", ip)
	}
}

func TestIPExtractor_TrustedProxy_HonorsXFF(t *testing.T) {
	// 10.0.0.1 is trusted proxy
	extractor, err := auth.NewIPExtractor([]string{"10.0.0.1"})
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	req, _ := http.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "10.0.0.1:45678"
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 10.0.0.1")

	ip := extractor.ClientIP(req)
	if ip != "198.51.100.5" {
		t.Errorf("expected 198.51.100.5, got %s", ip)
	}
}

func TestIPExtractor_TrustedProxy_CIDR(t *testing.T) {
	// 10.0.0.0/8 and 127.0.0.1 are trusted
	extractor, err := auth.NewIPExtractor([]string{"10.0.0.0/8", "127.0.0.1"})
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	req, _ := http.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	ip := extractor.ClientIP(req)
	if ip != "203.0.113.99" {
		t.Errorf("expected 203.0.113.99, got %s", ip)
	}
}

func TestIPExtractor_TrustedProxy_FallbackXRealIP(t *testing.T) {
	extractor, err := auth.NewIPExtractor([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	req, _ := http.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "198.51.100.77")

	ip := extractor.ClientIP(req)
	if ip != "198.51.100.77" {
		t.Errorf("expected 198.51.100.77, got %s", ip)
	}
}

func TestIPExtractor_IPv6(t *testing.T) {
	extractor, err := auth.NewIPExtractor([]string{"::1", "2001:db8::/32"})
	if err != nil {
		t.Fatalf("failed to create extractor: %v", err)
	}

	// 1. Untrusted IPv6 connecting peer
	req1, _ := http.NewRequest("POST", "/test", nil)
	req1.RemoteAddr = "[2001:db9::1]:45678"
	req1.Header.Set("X-Forwarded-For", "1.2.3.4")

	if ip := extractor.ClientIP(req1); ip != "2001:db9::1" {
		t.Errorf("expected 2001:db9::1, got %s", ip)
	}

	// 2. Trusted IPv6 connecting peer
	req2, _ := http.NewRequest("POST", "/test", nil)
	req2.RemoteAddr = "[2001:db8::10]:45678"
	req2.Header.Set("X-Forwarded-For", "2001:db9::99")

	if ip := extractor.ClientIP(req2); ip != "2001:db9::99" {
		t.Errorf("expected 2001:db9::99, got %s", ip)
	}
}

func TestIPExtractor_InvalidConfig(t *testing.T) {
	_, err := auth.NewIPExtractor([]string{"invalid-ip-format"})
	if err == nil {
		t.Fatal("expected error for invalid IP/CIDR string, got nil")
	}
}
