package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresSpoofedForwarding(t *testing.T) {
	cases := []struct {
		remote, xff, want string
	}{
		// Straight from the internet: the header is the client's own word.
		{"203.0.113.7:5000", "1.2.3.4", "203.0.113.7"},
		// Behind our proxy: the proxy appends the real peer last.
		{"127.0.0.1:5000", "1.2.3.4, 198.51.100.9", "198.51.100.9"},
		{"10.0.0.2:5000", "198.51.100.9", "198.51.100.9"},
		{"127.0.0.1:5000", "", "127.0.0.1"},
		{"127.0.0.1:5000", "garbage", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("remote %s, xff %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
