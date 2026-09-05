package project

import (
	"net"
	"testing"
)

func TestRankIPsPrefersTailscale(t *testing.T) {
	ips := func(values ...string) []net.IP {
		out := make([]net.IP, 0, len(values))
		for _, v := range values {
			out = append(out, net.ParseIP(v))
		}
		return out
	}
	cases := []struct {
		name string
		in   []net.IP
		want string
	}{
		{"tailscale v4 over lan", ips("192.168.1.10", "100.101.102.103", "fe80::1"), "100.101.102.103"},
		{"tailscale v6 over lan v4", ips("10.0.0.5", "fd7a:115c:a1e0::1234"), "fd7a:115c:a1e0::1234"},
		{"lan v4 over global v6", ips("2001:db8::1", "172.16.0.2"), "172.16.0.2"},
		{"v6 when only v6", ips("fe80::1", "2001:db8::1"), "2001:db8::1"},
		{"skip loopback and link-local", ips("127.0.0.1", "169.254.1.1", "fe80::1"), ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		if got := rankIPs(tc.in); got != tc.want {
			t.Errorf("%s: rankIPs = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestObserveSourceIsBestEffort(t *testing.T) {
	s := ObserveSource()
	if s.Host == "" {
		t.Log("hostname unavailable on this platform")
	}
	if s.IP != "" && net.ParseIP(s.IP) == nil {
		t.Fatalf("ip %q is not a valid address", s.IP)
	}
}
