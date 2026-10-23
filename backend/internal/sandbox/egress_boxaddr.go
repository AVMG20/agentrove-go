package sandbox

import (
	"context"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"
)

// Box address lookup: tells whether a request's peer address is a given
// user's box, so box-only credentials (the agent's MCP token) can be pinned
// to the box they were minted for. It matches the box container's exact
// addresses on its box network (its per-user network, or the shared one
// as a fallback) rather than a subnet: on the shared network every box
// shares a subnet.

const (
	boxAddrTTL        = 15 * time.Second
	boxAddrMinRefresh = time.Second
)

type boxAddrKey struct {
	d    *DockerProvider
	user string
}

type boxAddrEntry struct {
	mu      sync.Mutex
	addrs   []netip.Addr
	fetched time.Time
}

var boxAddrCache sync.Map // boxAddrKey -> *boxAddrEntry

// IsBoxAddr reports whether ip is an address of userID's box on one of its
// box networks. A cached answer is refreshed on a miss (the box may have
// restarted with a new address), at most once per second per user, so a
// flood of foreign requests can't turn into a flood of Docker calls.
func (d *DockerProvider) IsBoxAddr(ctx context.Context, userID string, ip netip.Addr) bool {
	ip = ip.Unmap()
	if userID == "" || !ip.IsValid() {
		return false
	}
	v, _ := boxAddrCache.LoadOrStore(boxAddrKey{d, userID}, &boxAddrEntry{})
	e := v.(*boxAddrEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	age := time.Since(e.fetched)
	if age < boxAddrTTL && containsAddr(e.addrs, ip) {
		return true
	}
	if age < boxAddrMinRefresh {
		return false
	}
	e.addrs, e.fetched = d.boxAddrs(ctx, userID), time.Now()
	return containsAddr(e.addrs, ip)
}

// boxAddrs are the user's box's addresses on the shared network and the
// user's own network; other (operator-attached) networks don't count.
func (d *DockerProvider) boxAddrs(ctx context.Context, userID string) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := d.net.ContainerInspect(ctx, containerName(BoxID(userID)))
	if err != nil || info.NetworkSettings == nil || info.State == nil || !info.State.Running {
		return nil
	}
	var out []netip.Addr
	own := UserNetworkName(userID)
	for name, ep := range info.NetworkSettings.Networks {
		if ep == nil || (name != own && name != d.cfg.DockerNetwork) {
			continue
		}
		for _, s := range []string{ep.IPAddress, ep.GlobalIPv6Address} {
			if a, err := netip.ParseAddr(s); err == nil {
				out = append(out, a.Unmap())
			}
		}
	}
	return out
}

func containsAddr(as []netip.Addr, ip netip.Addr) bool {
	for _, a := range as {
		if a == ip {
			return true
		}
	}
	return false
}

// IsBoxAddr reports whether ip is userID's Docker box (see
// DockerProvider.IsBoxAddr); false without a Docker provider.
func (m *Manager) IsBoxAddr(ctx context.Context, userID string, ip netip.Addr) bool {
	if d, ok := m.providers[KindDocker].(*DockerProvider); ok {
		return d.IsBoxAddr(ctx, userID, ip)
	}
	return false
}

// BoxAddrCheckable reports whether box addresses can be checked: a Docker
// provider exists. Pinning box credentials to their box makes sense then;
// per-user networks make the box's address unforgeable by other boxes.
func (m *Manager) BoxAddrCheckable() bool {
	_, ok := m.providers[KindDocker].(*DockerProvider)
	return ok
}

// FromUserBox reports whether r comes straight from userID's box. Boxes
// reach the API directly (no proxy), so the TCP peer is the box itself;
// X-Forwarded-For is never consulted. It is always true when pinning is
// off or can't work: AgentTokensBoxOnly unset, no Docker provider, no
// AGENTROVE_INTERNAL_URL (boxes then call the public BASE_URL, through the
// proxy), or the API not running as a container on that daemon.
func (m *Manager) FromUserBox(r *http.Request, userID string) bool {
	d, ok := m.providers[KindDocker].(*DockerProvider)
	if !ok || !m.cfg.AgentTokensBoxOnly || os.Getenv("AGENTROVE_INTERNAL_URL") == "" || d.selfContainer() == "" {
		return true
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return d.IsBoxAddr(r.Context(), userID, ap.Addr())
}
