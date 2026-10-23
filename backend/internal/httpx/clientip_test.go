package httpx

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("127.0.0.1/32")}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"untrusted peer ignores header", "203.0.113.9:5555", []string{"1.2.3.4"}, "203.0.113.9"},
		{"no header", "10.0.0.2:80", nil, "10.0.0.2"},
		{"right-most untrusted wins", "10.0.0.2:80", []string{"6.6.6.6, 198.51.100.7, 10.0.0.3"}, "198.51.100.7"},
		{"spoofed left entries don't matter", "10.0.0.2:80", []string{"1.1.1.1, 2.2.2.2, 198.51.100.7"}, "198.51.100.7"},
		{"multiple headers concatenate", "10.0.0.2:80", []string{"6.6.6.6", "198.51.100.7, 10.0.0.3"}, "198.51.100.7"},
		{"hop with port", "10.0.0.2:80", []string{"198.51.100.7:4711"}, "198.51.100.7"},
		{"v4-mapped peer", "[::ffff:10.0.0.2]:80", []string{"198.51.100.7"}, "198.51.100.7"},
		{"all hops trusted", "127.0.0.1:80", []string{"10.1.1.1, 10.0.0.3"}, "10.1.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, trusted); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
	// Nothing trusted (the zero config): the header never counts.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:80"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := ClientIP(r, nil); got != "10.0.0.2" {
		t.Fatalf("untrusted config: %q", got)
	}
}
