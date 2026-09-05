package project

import (
	"context"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

// Source is best-effort writer-observed machine context. It is informational:
// it is not part of the project identity and not compared on idempotent
// retries, because hostnames and addresses can change within one session.
type Source struct {
	Host string `json:"host,omitempty"`
	User string `json:"user,omitempty"`
	IP   string `json:"ip,omitempty"`
}

var (
	tailscaleV4 = mustCIDR("100.64.0.0/10")
	tailscaleV6 = mustCIDR("fd7a:115c:a1e0::/48")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ObserveSource collects hostname, OS user, and one representative IP.
// Every field is optional; failures leave it empty.
func ObserveSource() Source {
	var s Source
	if host, err := os.Hostname(); err == nil {
		s.Host = strings.TrimSpace(host)
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		s.User = u.Username
	} else if name := os.Getenv("USER"); name != "" {
		s.User = name
	}
	if ip := tailscaleIP(); ip != "" {
		s.IP = ip
	} else if ifaces, err := net.Interfaces(); err == nil {
		s.IP = pickIP(ifaces)
	}
	return s
}

// tailscaleIP asks the local tailscale CLI when it is installed. The CGNAT
// range 100.64.0.0/10 is shared with other tunnels, so this is the only
// authoritative source; pickIP's range check is the fallback.
func tailscaleIP() string {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "ip", "-4").Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(out))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// pickIP prefers a Tailscale address (stable and routable across the mesh),
// then any other non-loopback, non-link-local IPv4, then IPv6.
func pickIP(ifaces []net.Interface) string {
	var candidates []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			candidates = append(candidates, ipNet.IP)
		}
	}
	return rankIPs(candidates)
}

func rankIPs(candidates []net.IP) string {
	best, bestRank := "", 0
	for _, ip := range candidates {
		rank := ipRank(ip)
		if rank > bestRank {
			best, bestRank = ip.String(), rank
		}
	}
	return best
}

func ipRank(ip net.IP) int {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return 0
	}
	v4 := ip.To4()
	switch {
	case v4 != nil && tailscaleV4.Contains(v4):
		return 4
	case v4 == nil && tailscaleV6.Contains(ip):
		return 3
	case v4 != nil:
		return 2
	default:
		return 1
	}
}
