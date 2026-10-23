package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Box egress firewall: applying the rules (generation is in egress.go).
//
// The API already holds the Docker socket, so it applies the rules itself
// by running a short-lived helper container in the host's network
// namespace with NET_ADMIN (the API's own image, which ships nft, unless
// SANDBOX_EGRESS_IMAGE is set). It does so at startup, synchronously
// before a box starts on a network the rules don't cover yet (so a new
// user's box is never up unprotected), and every egressRepairInterval to
// heal a host firewall reload that dropped the table. Failures are logged
// as errors and never block boxes or the box -> API path.
//
// Env (read here so the firewall stays self-contained):
//   SANDBOX_EGRESS_FIREWALL       on (default) | off | dry-run
//   SANDBOX_EGRESS_ALLOW          extra destinations, e.g. "10.0.0.5:5432,192.168.1.0/24"
//   SANDBOX_EGRESS_DNS            resolver addresses, or "any"; default: discovered
//   SANDBOX_EGRESS_HAIRPIN_PORTS  public ports published on this host boxes may reach
//   SANDBOX_EGRESS_IMAGE          helper image with /bin/sh and nft

const (
	egressRepairInterval = 10 * time.Minute
	egressRetryAfter     = 30 * time.Second
	egressHelperTimeout  = 60 * time.Second
	egressHelperLabel    = "agentrove.egress-helper"
)

// egressRunner runs a shell script in the host network namespace with
// NET_ADMIN and returns its combined output.
type egressRunner func(ctx context.Context, script string, env []string) (string, error)

// egressDockerAPI is the slice of the Docker client the helper uses.
type egressDockerAPI interface {
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

type egressFirewall struct {
	mode    egressMode
	table   string
	allow   []egressRule
	hairpin []uint16
	dns     []netip.Addr
	dnsAny  bool
	dnsSet  bool
	image   string
	run     egressRunner // nil: no Docker client (tests, host provider)
	// resolvConf reads the API's own resolv.conf (DNS upstream discovery).
	resolvConf func() ([]byte, error)

	mu       sync.Mutex
	last     string          // last ruleset applied (or validated in dry-run)
	covered  map[string]bool // networks the last ruleset covers
	lastFail time.Time
	started  bool
}

// egressStates holds each provider's firewall; DockerProvider's fields
// belong to docker.go, so the state hangs off the provider here.
var egressStates sync.Map // *DockerProvider -> *egressFirewall

func (d *DockerProvider) egress() *egressFirewall {
	if fw, ok := egressStates.Load(d); ok {
		return fw.(*egressFirewall)
	}
	fw, _ := egressStates.LoadOrStore(d, d.newEgressFirewall())
	return fw.(*egressFirewall)
}

func (d *DockerProvider) newEgressFirewall() *egressFirewall {
	fw := &egressFirewall{
		table:      egressTableName(d.instance()),
		image:      strings.TrimSpace(os.Getenv("SANDBOX_EGRESS_IMAGE")),
		covered:    map[string]bool{},
		resolvConf: func() ([]byte, error) { return os.ReadFile("/etc/resolv.conf") },
	}
	var ok bool
	if fw.mode, ok = parseEgressMode(os.Getenv("SANDBOX_EGRESS_FIREWALL")); !ok {
		slog.Warn("unknown SANDBOX_EGRESS_FIREWALL; using on", "value", os.Getenv("SANDBOX_EGRESS_FIREWALL"))
	}
	var errs, e []error
	fw.allow, errs = parseEgressAllow(os.Getenv("SANDBOX_EGRESS_ALLOW"))
	fw.hairpin, e = parseEgressPorts(os.Getenv("SANDBOX_EGRESS_HAIRPIN_PORTS"))
	errs = append(errs, e...)
	fw.dns, fw.dnsAny, fw.dnsSet, e = parseEgressDNS(os.Getenv("SANDBOX_EGRESS_DNS"))
	errs = append(errs, e...)
	for _, err := range errs {
		slog.Warn("box egress firewall: ignoring bad setting", "err", err)
	}
	if d.cli != nil {
		fw.run = func(ctx context.Context, script string, env []string) (string, error) {
			image, err := d.egressImage(ctx, fw.image)
			if err != nil {
				return "", err
			}
			return runEgressHelper(ctx, d.cli, image, "agentrove-egress-"+d.instance(), d.instance(), script, env)
		}
	}
	return fw
}

// egressImage is the helper image: SANDBOX_EGRESS_IMAGE, else the image
// of the API's own container (pinned to this build, already on the host).
func (d *DockerProvider) egressImage(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if self := d.selfContainer(); self != "" {
		if info, err := d.net.ContainerInspect(ctx, self); err == nil && info.Image != "" {
			return info.Image, nil
		}
	}
	return "", errors.New("no helper image: the API's own container is unknown; set SANDBOX_EGRESS_IMAGE to an image with nft")
}

// egressCollect gathers the policy from the daemon: the shared box network
// and this instance's per-user networks, each with the API's addresses on it.
func (d *DockerProvider) egressCollect(ctx context.Context, fw *egressFirewall) egressPolicy {
	p := egressPolicy{Table: fw.table, Allow: fw.allow, Hairpin: fw.hairpin, APIPorts: egressAPIPorts(d.cfg.Port, os.Getenv("AGENTROVE_INTERNAL_URL"))}
	self := d.selfContainer()
	p.HostAPI = self == ""
	seen := map[string]bool{}
	add := func(info network.Inspect, shared bool) {
		n, ok := egressNetFrom(info)
		if !ok || seen[n.Bridge] {
			return
		}
		if ep, ok := info.Containers[self]; ok && self != "" {
			for _, s := range []string{ep.IPv4Address, ep.IPv6Address} {
				if pfx, err := netip.ParsePrefix(s); err == nil {
					n.API = append(n.API, pfx.Addr())
				}
			}
		}
		if shared {
			n.Trusted = n.API
		}
		seen[n.Bridge] = true
		p.Nets = append(p.Nets, n)
	}
	if d.cfg.DockerNetwork != "" {
		if info, err := d.net.NetworkInspect(ctx, d.cfg.DockerNetwork, network.InspectOptions{}); err == nil {
			add(info, true)
		}
	}
	if list, err := d.net.NetworkList(ctx, network.ListOptions{Filters: d.userNetworkFilters()}); err != nil {
		slog.Warn("box egress firewall: list user box networks", "err", err)
	} else {
		for _, n := range list {
			// The list omits attached containers; inspect for the API's address.
			if info, err := d.net.NetworkInspect(ctx, n.Name, network.InspectOptions{}); err == nil {
				n = info
			}
			add(n, false)
		}
	}
	switch {
	case fw.dnsSet:
		p.DNS, p.DNSAny = fw.dns, fw.dnsAny
	default:
		data, err := fw.resolvConf()
		addrs, known := parseResolvConf(data)
		if err != nil || !known {
			slog.Warn("box egress firewall: DNS upstreams unknown, allowing port 53 to any address; set SANDBOX_EGRESS_DNS to narrow it", "err", err)
			p.DNSAny = true
		} else {
			p.DNS = addrs
		}
	}
	return p
}

// egressAPIPorts are the API ports boxes use: PORT and the port of
// AGENTROVE_INTERNAL_URL.
func egressAPIPorts(port int, internalURL string) []uint16 {
	var out []uint16
	if port > 0 && port < 65536 {
		out = append(out, uint16(port))
	}
	if u, err := url.Parse(strings.TrimSpace(internalURL)); err == nil && u.Host != "" {
		ps := u.Port()
		if ps == "" {
			ps = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		if n, err := strconv.ParseUint(ps, 10, 16); err == nil && n > 0 {
			out = append(out, uint16(n))
		}
	}
	if len(out) == 0 {
		out = []uint16{8080}
	}
	return out
}

// egressNetFrom maps a Docker network to its host bridge interface.
func egressNetFrom(info network.Inspect) (egressNet, bool) {
	if info.Driver != "bridge" {
		slog.Warn("box egress firewall: not a bridge network, can't filter it", "network", info.Name, "driver", info.Driver)
		return egressNet{}, false
	}
	bridge := info.Options["com.docker.network.bridge.name"]
	if bridge == "" && len(info.ID) >= 12 {
		bridge = "br-" + info.ID[:12]
	}
	if !ifaceRe.MatchString(bridge) {
		slog.Warn("box egress firewall: unusable bridge name", "network", info.Name, "bridge", bridge)
		return egressNet{}, false
	}
	n := egressNet{Name: info.Name, Bridge: bridge}
	for _, c := range info.IPAM.Config {
		if pfx, err := netip.ParsePrefix(c.Subnet); err == nil {
			n.Subnets = append(n.Subnets, pfx)
		}
	}
	return n, true
}

// egressBridgeWarning marks a helper run whose layer-2 table failed.
const egressBridgeWarning = "agentrove-egress: WARNING"

// egressScript is the helper's shell script for a mode. Rulesets come in
// $AGENTROVE_NFT (IP, must succeed) and $AGENTROVE_NFT_BRIDGE (layer 2,
// best effort), so no rule text is ever interpolated into shell.
func egressScript(mode egressMode) string {
	check := `command -v nft >/dev/null 2>&1 || { echo "agentrove-egress: nft not found in the helper image (install nftables or set SANDBOX_EGRESS_IMAGE)" >&2; exit 127; }
T="$AGENTROVE_TABLE"
`
	switch mode {
	case egressOff:
		return check + `for fam in inet bridge; do
  if nft list table "$fam" "$T" >/dev/null 2>&1; then
    nft delete table "$fam" "$T" && echo "agentrove-egress: removed $fam table $T"
  fi
done
echo "agentrove-egress: firewall off"`
	case egressDryRun:
		return check + `printf '%s' "$AGENTROVE_NFT" | nft -c -f - || exit 1
echo "agentrove-egress: inet table $T valid (dry-run, not applied)"
if [ -n "$AGENTROVE_NFT_BRIDGE" ]; then
  printf '%s' "$AGENTROVE_NFT_BRIDGE" | nft -c -f - && echo "agentrove-egress: bridge table $T valid (dry-run)" || echo "` + egressBridgeWarning + `: bridge table $T invalid on this host"
fi`
	}
	return check + `printf '%s' "$AGENTROVE_NFT" | nft -f - || exit 1
echo "agentrove-egress: applied inet table $T"
if [ -n "$AGENTROVE_NFT_BRIDGE" ]; then
  if printf '%s' "$AGENTROVE_NFT_BRIDGE" | nft -f -; then
    echo "agentrove-egress: applied bridge table $T"
  else
    echo "` + egressBridgeWarning + `: bridge table $T not applied; same-bridge traffic (box -> other API ports) is unfiltered"
  fi
fi`
}

// egressApplyLocked regenerates the rulesets from the daemon's networks
// and runs them.
// Callers hold fw.mu.
func (d *DockerProvider) egressApplyLocked(ctx context.Context, fw *egressFirewall, reason string) error {
	p := d.egressCollect(ctx, fw)
	ruleset, err := nftRuleset(p)
	var bridgeRules string
	if err == nil {
		bridgeRules, err = nftBridgeRuleset(p)
	}
	if err != nil {
		fw.lastFail = time.Now()
		slog.Error("box egress firewall: rule generation failed", "err", err)
		return err
	}
	names, bridges := make([]string, 0, len(p.Nets)), make([]string, 0, len(p.Nets))
	for _, n := range p.Nets {
		names = append(names, n.Name)
		bridges = append(bridges, n.Bridge)
	}
	sum := sha256.Sum256([]byte(ruleset + bridgeRules))
	attrs := []any{"mode", fw.mode.String(), "reason", reason, "table", p.Table, "networks", len(names),
		"bridges", strings.Join(bridges, ","), "dns", dnsSummary(p), "allow", len(p.Allow),
		"hairpin_ports", len(p.Hairpin), "ruleset_sha256", hex.EncodeToString(sum[:6])}
	changed := ruleset+bridgeRules != fw.last
	if changed {
		slog.Debug("box egress firewall ruleset", append(attrs, "ruleset", ruleset, "bridge_ruleset", bridgeRules)...)
	}
	if fw.mode == egressDryRun && changed {
		// The whole point of a dry run: show exactly what would be applied.
		slog.Info("box egress firewall dry-run ruleset (not applied)", "table", p.Table, "ruleset", ruleset, "bridge_ruleset", bridgeRules)
	}
	out, err := fw.run(ctx, egressScript(fw.mode), []string{"AGENTROVE_NFT=" + ruleset, "AGENTROVE_NFT_BRIDGE=" + bridgeRules, "AGENTROVE_TABLE=" + p.Table})
	if err != nil {
		fw.lastFail = time.Now()
		slog.Error("box egress firewall NOT applied: boxes can reach the host and private networks",
			append(attrs, "err", err, "output", strings.TrimSpace(out))...)
		return err
	}
	fw.lastFail = time.Time{}
	fw.covered = map[string]bool{}
	for _, n := range names {
		fw.covered[n] = true
	}
	msg := "box egress firewall applied"
	if fw.mode == egressDryRun {
		msg = "box egress firewall validated (dry-run, not applied)"
	}
	switch {
	case strings.Contains(out, egressBridgeWarning):
		slog.Warn(msg+" without its layer-2 table", append(attrs, "output", strings.TrimSpace(out))...)
	case changed:
		slog.Info(msg, append(attrs, "output", strings.TrimSpace(out))...)
	default:
		slog.Debug(msg+" (unchanged)", attrs...)
	}
	fw.last = ruleset + bridgeRules
	return nil
}

func dnsSummary(p egressPolicy) string {
	if p.DNSAny {
		return "any"
	}
	v4, v6 := splitAddrs(p.DNS)
	if all := append(v4, v6...); len(all) > 0 {
		return strings.Join(all, ",")
	}
	return "embedded-only"
}

// egressCover makes sure the firewall covers a box network before a box
// starts on it; a network created since the last apply triggers one now.
func (d *DockerProvider) egressCover(ctx context.Context, networkName string) {
	fw := d.egress()
	if fw.mode == egressOff || fw.run == nil || networkName == "" {
		return
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.covered[networkName] {
		return
	}
	if !fw.lastFail.IsZero() && time.Since(fw.lastFail) < egressRetryAfter {
		slog.Warn("box egress firewall: starting a box on an unfiltered network (last apply failed)", "network", networkName)
		return
	}
	if d.egressApplyLocked(ctx, fw, "network "+networkName) == nil && !fw.covered[networkName] {
		slog.Warn("box egress firewall: network not covered", "network", networkName)
	}
}

// startEgressFirewall applies the rules at startup and keeps them applied;
// with the firewall off it removes this instance's table instead.
func (d *DockerProvider) startEgressFirewall(ctx context.Context) {
	fw := d.egress()
	fw.mu.Lock()
	if fw.started || fw.run == nil {
		fw.mu.Unlock()
		return
	}
	fw.started = true
	if fw.mode == egressOff {
		fw.mu.Unlock()
		slog.Warn("box egress firewall off (SANDBOX_EGRESS_FIREWALL=off): boxes can reach the host, its LAN and cloud metadata")
		if !tableRe.MatchString(fw.table) {
			return
		}
		out, err := fw.run(ctx, egressScript(egressOff), []string{"AGENTROVE_TABLE=" + fw.table})
		slog.Info("box egress firewall: removing this instance's rules", "table", fw.table, "output", strings.TrimSpace(out), "err", err)
		return
	}
	_ = d.egressApplyLocked(ctx, fw, "startup")
	fw.mu.Unlock()
	go func() {
		t := time.NewTicker(egressRepairInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fw.mu.Lock()
				_ = d.egressApplyLocked(ctx, fw, "repair")
				fw.mu.Unlock()
			}
		}
	}()
}

// runEgressHelper runs script in a throwaway container: host network
// namespace, NET_ADMIN only, read-only root, no new privileges.
func runEgressHelper(ctx context.Context, cli egressDockerAPI, image, name, instance, script string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, egressHelperTimeout)
	defer cancel()
	// RemoveVolumes: the API image declares VOLUME /data/agentrove/storage,
	// so every helper run gets an anonymous volume that would otherwise leak
	// (one per apply, every egressRepairInterval).
	_ = cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true, RemoveVolumes: true})
	cc := &container.Config{
		Image:      image,
		User:       "0:0",
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{script},
		Env:        env,
		Labels:     map[string]string{egressHelperLabel: instance},
	}
	hc := &container.HostConfig{
		NetworkMode:    "host",
		UsernsMode:     "host",
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{"NET_ADMIN"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
	}
	created, err := cli.ContainerCreate(ctx, cc, hc, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("create egress helper (image %s): %w", image, err)
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rcancel()
		_ = cli.ContainerRemove(rctx, created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}()
	waitC, errC := cli.ContainerWait(ctx, created.ID, container.WaitConditionNextExit)
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start egress helper: %w", err)
	}
	var code int64
	select {
	case res := <-waitC:
		code = res.StatusCode
		if res.Error != nil && res.Error.Message != "" {
			return "", fmt.Errorf("egress helper: %s", res.Error.Message)
		}
	case err := <-errC:
		return "", fmt.Errorf("wait for egress helper: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	}
	var stdout, stderr bytes.Buffer
	if logs, err := cli.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true}); err == nil {
		_, _ = stdcopy.StdCopy(&stdout, &stderr, logs)
		logs.Close()
	}
	out := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	if code != 0 {
		return out, fmt.Errorf("egress helper exited %d", code)
	}
	return out, nil
}
