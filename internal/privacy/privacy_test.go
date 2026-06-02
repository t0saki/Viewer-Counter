package privacy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqWith(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	hdrs := []string{"X-Real-IP"}

	// trust_proxy off -> RemoteAddr, headers ignored even if spoofed.
	if got := ClientIP(reqWith("10.0.0.1:5000", map[string]string{"X-Real-IP": "9.9.9.9"}), false, hdrs); got != "10.0.0.1" {
		t.Errorf("trust off = %q, want 10.0.0.1", got)
	}

	// trust_proxy on -> trusted header wins.
	if got := ClientIP(reqWith("10.0.0.1:5000", map[string]string{"X-Real-IP": "203.0.113.7"}), true, hdrs); got != "203.0.113.7" {
		t.Errorf("X-Real-IP = %q, want 203.0.113.7", got)
	}

	// Spoofed left-most XFF must NOT win; right-most (proxy-observed) is used.
	got := ClientIP(reqWith("10.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.7"}), true, hdrs)
	if got != "203.0.113.7" {
		t.Errorf("XFF fallback = %q, want right-most 203.0.113.7 (not spoofed 1.2.3.4)", got)
	}

	// Header preference order.
	order := []string{"CF-Connecting-IP", "X-Real-IP"}
	got = ClientIP(reqWith("10.0.0.1:5000", map[string]string{"CF-Connecting-IP": "198.51.100.5", "X-Real-IP": "203.0.113.7"}), true, order)
	if got != "198.51.100.5" {
		t.Errorf("preferred header = %q, want 198.51.100.5", got)
	}
}

func TestStoreIPModes(t *testing.T) {
	ip := "203.0.113.45"
	if got := New("none", "s", true, false, nil).StoreIP(ip); got != "" {
		t.Errorf("none = %q, want empty", got)
	}
	if got := New("full", "s", true, false, nil).StoreIP(ip); got != ip {
		t.Errorf("full = %q, want %q", got, ip)
	}
	if got := New("truncate", "s", true, false, nil).StoreIP(ip); got != "203.0.113.0" {
		t.Errorf("truncate = %q, want 203.0.113.0", got)
	}
	h := New("hash", "salt", true, false, nil)
	a, b := h.StoreIP(ip), h.StoreIP(ip)
	if a == "" || a != b {
		t.Errorf("hash not deterministic: %q vs %q", a, b)
	}
	if a == ip {
		t.Errorf("hash returned raw ip")
	}
}

func TestTruncateIPv6(t *testing.T) {
	got := New("truncate", "", true, false, nil).StoreIP("2001:db8:1234:5678::1")
	if got != "2001:db8:1234::" {
		t.Errorf("ipv6 truncate = %q, want 2001:db8:1234::", got)
	}
}

func TestIsBot(t *testing.T) {
	p := New("hash", "s", true, true, []string{"bot", "curl/"})
	if !p.IsBot("Mozilla/5.0 (compatible; Googlebot/2.1)") {
		t.Error("expected Googlebot to be detected")
	}
	if !p.IsBot("curl/8.1.0") {
		t.Error("expected curl to be detected")
	}
	if p.IsBot("Mozilla/5.0 (Macintosh)") {
		t.Error("regular UA should not be a bot")
	}
	if New("hash", "s", true, false, []string{"bot"}).IsBot("Googlebot") {
		t.Error("bot detection should be off when disabled")
	}
}

func TestDedupKeyStable(t *testing.T) {
	a := DedupKey("s", "/p", "1.2.3.4", "ua")
	b := DedupKey("s", "/p", "1.2.3.4", "ua")
	c := DedupKey("s", "/p", "1.2.3.5", "ua")
	if a != b {
		t.Error("same inputs should give same key")
	}
	if a == c {
		t.Error("different ip should give different key")
	}
}
