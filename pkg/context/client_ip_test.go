package context

import (
	"net/http/httptest"
	"testing"
)

func clientIP(remote string, headers map[string]string) string {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return (&Context{Request: req}).ClientIP()
}

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeers(t *testing.T) {
	got := clientIP("203.0.113.9:5555", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "1.2.3.4"})
	if got != "203.0.113.9" {
		t.Fatalf("spoofed header believed: %s", got)
	}
}

func TestClientIPBehindTrustedProxy(t *testing.T) {
	// Client sent a forged X-Forwarded-For; nginx on localhost appended the real peer.
	if got := clientIP("127.0.0.1:40000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"}); got != "198.51.100.7" {
		t.Fatalf("rightmost untrusted hop: %s", got)
	}
	// Chain of private proxies.
	if got := clientIP("10.0.0.2:1", map[string]string{"X-Forwarded-For": "198.51.100.7, 10.0.0.5"}); got != "198.51.100.7" {
		t.Fatalf("skip trusted hops: %s", got)
	}
	if got := clientIP("[::1]:8080", map[string]string{"X-Real-IP": "198.51.100.8"}); got != "198.51.100.8" {
		t.Fatalf("X-Real-IP from trusted peer: %s", got)
	}
	if got := clientIP("[::1]:8080", nil); got != "::1" {
		t.Fatalf("IPv6 remote addr: %s", got)
	}
}

func TestSetTrustedProxies(t *testing.T) {
	t.Cleanup(func() { _ = SetTrustedProxies(defaultTrustedProxies...) })
	if err := SetTrustedProxies("203.0.113.0/24", "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	if got := clientIP("203.0.113.9:1", map[string]string{"X-Forwarded-For": "192.0.2.44"}); got != "192.0.2.44" {
		t.Fatalf("custom trusted range: %s", got)
	}
	if got := clientIP("127.0.0.1:1", map[string]string{"X-Forwarded-For": "192.0.2.44"}); got != "127.0.0.1" {
		t.Fatalf("loopback no longer trusted after replace: %s", got)
	}
	if err := SetTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("expected error for bad CIDR")
	}
}
