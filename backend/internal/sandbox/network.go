package sandbox

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// Per-user box networks: each user's box sits on its own bridge, and the API
// container joins every one of them (aliased so AGENTROVE_INTERNAL_URL still
// resolves). Boxes reach the API and the internet, but not each other. The
// backend itself only drives boxes through the Docker socket (exec), never
// over the network. The host firewall (egress*.go) narrows that further to
// the API's port and the public internet.

const (
	userNetworkPrefix = "agentrove-user-net-"
	// UserLabel carries the owning user on boxes and their networks.
	UserLabel = "agentrove.user"
)

// dockerNetAPI is the slice of the Docker client the network code uses, so
// it can be tested without a daemon.
type dockerNetAPI interface {
	NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error)
	NetworkConnect(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error
	NetworkDisconnect(ctx context.Context, networkID, containerID string, force bool) error
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
}

// UserNetworkName is the deterministic bridge name of a user's box network.
func UserNetworkName(userID string) string {
	return userNetworkPrefix + strings.ReplaceAll(userID, "-", "")
}

func isUserNetwork(name string) bool { return strings.HasPrefix(name, userNetworkPrefix) }

func (d *DockerProvider) userNetworkLabels(userID string) map[string]string {
	return map[string]string{ManagedLabel: "true", InstanceLabel: d.instance(), UserLabel: userID}
}

// userNetworkFilters selects this instance's per-user networks.
func (d *DockerProvider) userNetworkFilters() filters.Args {
	return filters.NewArgs(
		filters.Arg("label", ManagedLabel+"=true"),
		filters.Arg("label", InstanceLabel+"="+d.instance()),
		filters.Arg("label", UserLabel),
	)
}

// apiAliases are the names boxes use for the API on their network: the host
// of AGENTROVE_INTERNAL_URL (unless it's an IP) and "api".
func apiAliases(internalURL string) []string {
	out := []string{"api"}
	if u, err := url.Parse(strings.TrimSpace(internalURL)); err == nil {
		if h := u.Hostname(); h != "" && h != "api" && net.ParseIP(h) == nil {
			out = append(out, h)
		}
	}
	return out
}

// selfContainer finds the API's own container: AGENTROVE_API_CONTAINER, or
// the hostname, which Docker sets to the short container id by default.
// Empty means not running in a container on this daemon.
func (d *DockerProvider) selfContainer() string {
	d.selfOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ref, explicit := d.cfg.APIContainer, d.cfg.APIContainer != ""
		if !explicit {
			ref, _ = d.hostname()
		}
		if ref != "" {
			if info, err := d.net.ContainerInspect(ctx, ref); err == nil && (explicit || strings.HasPrefix(info.ID, ref)) {
				d.self = info.ID
				return
			}
		}
		slog.Warn("per-user box networks off: the API's own container was not found (set AGENTROVE_API_CONTAINER); boxes share DOCKER_NETWORK",
			"network", d.cfg.DockerNetwork, "ref", ref)
	})
	return d.self
}

// boxNetwork is the network a user's box should be on, set up and joined
// by the API. It falls back to the shared DOCKER_NETWORK when per-user
// networks are off, the API's container is unknown, or setup fails. The
// egress firewall covers the network before this returns, so the box never
// starts on an unfiltered bridge.
func (d *DockerProvider) boxNetwork(ctx context.Context, userID string) string {
	name := d.pickBoxNetwork(ctx, userID)
	d.egressCover(ctx, name)
	return name
}

func (d *DockerProvider) pickBoxNetwork(ctx context.Context, userID string) string {
	if !d.cfg.SandboxPerUserNetworks || userID == "" || d.selfContainer() == "" {
		d.ensureNetwork(ctx)
		return d.cfg.DockerNetwork
	}
	name := UserNetworkName(userID)
	d.netMu.Lock()
	defer d.netMu.Unlock()
	if d.joined[name] {
		return name
	}
	if _, err := d.net.NetworkInspect(ctx, name, network.InspectOptions{}); err != nil {
		_, err := d.net.NetworkCreate(ctx, name, userNetworkOptions(d.userNetworkLabels(userID)))
		if err != nil && !alreadyExists(err) {
			slog.Warn("create user box network; using the shared one", "network", name, "err", err)
			d.ensureNetwork(ctx)
			return d.cfg.DockerNetwork
		}
	}
	if err := d.joinLocked(ctx, name); err != nil {
		slog.Warn("join user box network; using the shared one", "network", name, "err", err)
		d.ensureNetwork(ctx)
		return d.cfg.DockerNetwork
	}
	return name
}

// userNetworkOptions creates a plain IPv4 bridge: IPv6 off even when the
// daemon defaults it on, so the firewall's IPv4 policy is the whole story.
func userNetworkOptions(labels map[string]string) network.CreateOptions {
	ipv6 := false
	return network.CreateOptions{Driver: "bridge", Labels: labels, EnableIPv6: &ipv6}
}

// apiEndpoint is how the API joins a box network: aliased for
// AGENTROVE_INTERNAL_URL, and never its default gateway (Docker >= 28
// honours GwPriority; older daemons pick the lexically first network, the
// shared one), so the API's own egress doesn't cross a box bridge.
func apiEndpoint() *network.EndpointSettings {
	return &network.EndpointSettings{Aliases: apiAliases(os.Getenv("AGENTROVE_INTERNAL_URL")), GwPriority: -1}
}

// joinLocked connects the API container to a network; already connected is
// fine. Callers hold netMu.
func (d *DockerProvider) joinLocked(ctx context.Context, name string) error {
	if info, err := d.net.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		d.recordSubnets(info)
		if _, ok := info.Containers[d.self]; ok {
			d.joined[name] = true
			return nil
		}
	}
	err := d.net.NetworkConnect(ctx, name, d.self, apiEndpoint())
	if err != nil && !alreadyExists(err) {
		return err
	}
	d.joined[name] = true
	return nil
}

// ReconnectNetworks rejoins every per-user network at startup: a redeploy
// recreates the API container without the connections made at runtime, and
// running boxes would otherwise lose the API. It then starts the box
// egress firewall (applied now and kept applied while ctx lives).
func (d *DockerProvider) ReconnectNetworks(ctx context.Context) {
	defer d.startEgressFirewall(ctx)
	if !d.cfg.SandboxPerUserNetworks || d.selfContainer() == "" {
		return
	}
	list, err := d.net.NetworkList(ctx, network.ListOptions{Filters: d.userNetworkFilters()})
	if err != nil {
		slog.Warn("list user box networks", "err", err)
		return
	}
	d.netMu.Lock()
	defer d.netMu.Unlock()
	for _, n := range list {
		if err := d.joinLocked(ctx, n.Name); err != nil {
			slog.Warn("rejoin user box network", "network", n.Name, "err", err)
		}
	}
}

// LoadBoxSubnets records the subnets of the shared and every per-user box
// network, so requests from boxes are never taken for a trusted proxy
// (httpx.ClientIP). Networks created later are recorded as they are joined.
func (d *DockerProvider) LoadBoxSubnets(ctx context.Context) {
	if d.cfg.DockerNetwork != "" {
		if info, err := d.net.NetworkInspect(ctx, d.cfg.DockerNetwork, network.InspectOptions{}); err == nil {
			d.recordSubnets(info)
		}
	}
	list, err := d.net.NetworkList(ctx, network.ListOptions{Filters: d.userNetworkFilters()})
	if err != nil {
		slog.Warn("list user box networks", "err", err)
		return
	}
	for _, n := range list {
		d.recordSubnets(n)
	}
}

func (d *DockerProvider) recordSubnets(info network.Inspect) {
	if d.subnets == nil {
		return
	}
	for _, c := range info.IPAM.Config {
		if p, err := netip.ParsePrefix(c.Subnet); err == nil {
			d.subnets.Add(p)
		}
	}
}

// planBoxNetworks says how to move a stopped box onto target: connect it
// if missing, then drop the shared and other per-user networks. Networks
// we don't manage are left alone.
func planBoxNetworks(current []string, target, shared string) (connect bool, disconnect []string) {
	if target == "" {
		return false, nil
	}
	connect = true
	for _, n := range current {
		switch {
		case n == target:
			connect = false
		case n == shared || isUserNetwork(n):
			disconnect = append(disconnect, n)
		}
	}
	sort.Strings(disconnect)
	return connect, disconnect
}

// moveBox puts a stopped box on target with connect/disconnect, keeping the
// container (and the packages installed in it).
func (d *DockerProvider) moveBox(ctx context.Context, info container.InspectResponse, target string) error {
	var current []string
	if info.NetworkSettings != nil {
		for n := range info.NetworkSettings.Networks {
			current = append(current, n)
		}
	}
	connect, disconnect := planBoxNetworks(current, target, d.cfg.DockerNetwork)
	if !connect && len(disconnect) == 0 {
		return nil
	}
	slog.Info("moving sandbox to its network", "container", strings.TrimPrefix(info.Name, "/"), "network", target, "leaving", disconnect)
	if connect {
		if err := d.net.NetworkConnect(ctx, target, info.ID, nil); err != nil && !alreadyExists(err) {
			return err
		}
	}
	for _, n := range disconnect {
		if err := d.net.NetworkDisconnect(ctx, n, info.ID, true); err != nil {
			return err
		}
	}
	return nil
}

// alreadyExists matches the daemon's "network ... already exists",
// "endpoint ... already exists" and "already attached" errors.
func alreadyExists(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "already attached")
}
