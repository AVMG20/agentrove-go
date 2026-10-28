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
			if got := ClientIP(r, trusted, nil); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
	// Nothing trusted (the zero config): the header never counts.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:80"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := ClientIP(r, nil, nil); got != "10.0.0.2" {
		t.Fatalf("untrusted config: %q", got)
	}
}

// Box networks are private but never proxies, even when TRUSTED_PROXIES
// covers them, so code in a box can't forge X-Forwarded-For.
func TestClientIPBoxSubnets(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	boxes := NewPrefixSet()
	var nilSet *PrefixSet
	if nilSet.Contains(netip.MustParseAddr("172.20.0.5")) {
		t.Fatal("nil set not empty")
	}
	boxes.Add(netip.MustParsePrefix("172.20.0.9/16"), netip.MustParsePrefix("172.20.0.0/16"))
	ip := func(remote string, xff string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", xff)
		return ClientIP(r, trusted, boxes)
	}
	if got := ip("172.20.0.5:4000", "6.6.6.6"); got != "172.20.0.5" {
		t.Fatalf("box peer honoured XFF: %q", got)
	}
	// nginx on another private network still forwards the real client.
	if got := ip("172.19.0.3:4000", "198.51.100.7, 172.18.0.2"); got != "198.51.100.7" {
		t.Fatalf("proxy chain: %q", got)
	}
	// A box address in the chain is a client, not a hop to skip.
	if got := ip("172.19.0.3:4000", "6.6.6.6, 172.20.0.5"); got != "172.20.0.5" {
		t.Fatalf("box hop: %q", got)
	}
}

// A Cloudflare edge is replaced by CF-Connecting-IP; anyone else's is ignored.
func TestClientIPCloudflare(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	ip := func(remote, xff, cf string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		return ClientIP(r, trusted, nil)
	}
	cases := []struct{ name, remote, xff, cf, want string }{
		{"edge behind proxy", "10.0.0.2:80", "172.71.98.178", "198.51.100.7", "198.51.100.7"},
		{"proxy trusting cloudflare", "10.0.0.2:80", "198.51.100.7, 162.159.113.74", "198.51.100.7", "198.51.100.7"},
		{"edge direct", "141.101.76.111:443", "", "2001:db8::1", "2001:db8::1"},
		{"v4-mapped edge", "[::ffff:172.71.98.178]:443", "", "198.51.100.7", "198.51.100.7"},
		{"forged from non-edge", "10.0.0.2:80", "203.0.113.9", "198.51.100.7", "203.0.113.9"},
		{"forged direct", "203.0.113.9:443", "", "198.51.100.7", "203.0.113.9"},
		{"edge without header", "10.0.0.2:80", "172.71.98.178", "", "172.71.98.178"},
		{"edge with garbage header", "10.0.0.2:80", "172.71.98.178", "nope", "172.71.98.178"},
	}
	for _, tc := range cases {
		if got := ip(tc.remote, tc.xff, tc.cf); got != tc.want {
			t.Errorf("%s: ClientIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}
