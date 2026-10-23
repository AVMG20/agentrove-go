package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Box egress firewall: rule generation (pure; applied by egress_apply.go).
//
// Boxes run attacker-controllable code as root, so their network reach is
// cut down on the host to: the internet, their own bridge (the API), and
// the DNS servers Docker's embedded resolver forwards to. Everything else a
// box could route to is dropped: the host itself (any address: gateway IPs,
// docker0, public IPs; SSH, Coolify's :8000, databases on 0.0.0.0), other
// containers and networks, the LAN/VPC (RFC 1918, CGNAT), link-local and
// cloud metadata (169.254.169.254, Azure's 168.63.129.16), and IPv6
// equivalents.
//
// The rules live in their own nftables table, one per API instance. In
// nftables a drop in any base chain is final while an accept only ends that
// chain, so this table can only take traffic away: it never overrides
// Docker's own chains (iptables-nft, iptables-legacy or Docker's nftables
// backend all keep working) and Docker never touches it. Interfaces are
// matched by bridge name, not source address, so a box can't escape the
// policy by spoofing a source outside its subnet.

const egressTablePrefix = "agentrove_box_"

// egressBlocked4 are the IPv4 destinations a box may not reach: "this"
// network, RFC 1918, CGNAT (Tailscale, Alibaba metadata), loopback, Azure
// WireServer, link-local (cloud metadata), IETF/TEST-NETs, 6to4 relay,
// benchmarking, multicast and reserved. Non-overlapping, sorted.
var egressBlocked4 = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"168.63.129.16", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24",
	"192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3",
}

// egressBlocked6 are the IPv6 equivalents, including the ranges that embed
// IPv4 addresses (mapped, NAT64, 6to4, Teredo) so they can't smuggle a
// private IPv4 destination. ULA covers AWS's fd00:ec2::254 metadata.
var egressBlocked6 = []string{
	"::/127", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
	"2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10",
	"fec0::/10", "ff00::/8",
}

// egressMode is SANDBOX_EGRESS_FIREWALL.
type egressMode int

const (
	egressOn     egressMode = iota // apply the rules (default)
	egressOff                      // remove the API's table and leave the host alone
	egressDryRun                   // log the rules and validate them with `nft -c`, never apply
)

func (m egressMode) String() string {
	switch m {
	case egressOff:
		return "off"
	case egressDryRun:
		return "dry-run"
	}
	return "on"
}

// parseEgressMode reads SANDBOX_EGRESS_FIREWALL. Unknown values fail safe
// (on) and are reported.
func parseEgressMode(s string) (egressMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "on", "true", "1", "yes", "enforce":
		return egressOn, true
	case "off", "false", "0", "no", "disabled":
		return egressOff, true
	case "dry-run", "dryrun", "dry_run", "check":
		return egressDryRun, true
	}
	return egressOn, false
}

// egressNet is one box network as the firewall sees it.
type egressNet struct {
	Name    string
	Bridge  string         // host interface, e.g. br-0123456789ab
	Subnets []netip.Prefix // IPAM subnets; IPv4 ones drive anti-spoofing
	// Trusted are source addresses exempt from the policy: the API's own
	// addresses on the shared network, which Docker may pick as the API's
	// default route (lexically first network), so its own egress would
	// otherwise be filtered as a box's.
	Trusted []netip.Addr
	// API are the API container's addresses on this bridge (informational;
	// the shared network's become Trusted).
	API []netip.Addr
}

// egressRule is an operator exception (SANDBOX_EGRESS_ALLOW).
type egressRule struct {
	Prefix netip.Prefix
	Proto  string // "tcp", "udp", or "" for any
	Port   uint16 // 0 = any
}

// egressPolicy is everything the ruleset is generated from.
type egressPolicy struct {
	Table string
	Nets  []egressNet
	// DNS are the upstream servers Docker's embedded resolver (127.0.0.11,
	// inside the box) forwards to. It sends those queries from the box's
	// network namespace, so a private upstream (VPC resolver, home router)
	// must be allowed on port 53. DNSAny allows port 53 to any address when
	// the upstreams are unknown.
	DNS     []netip.Addr
	DNSAny  bool
	Allow   []egressRule
	Hairpin []uint16 // public ports published on this host boxes may reach
	// APIPorts are the API's TCP ports boxes may use on their bridge.
	APIPorts []uint16
	// HostAPI: the API runs on the host, not in a container, so boxes
	// reach it through the bridge gateway on APIPorts (input hook).
	HostAPI bool
}

var (
	ifaceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
	tableRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)
)

// egressTableName is the nftables table of one API instance, so two
// instances on one daemon never replace each other's rules.
func egressTableName(instance string) string { return egressTablePrefix + instance }

// nftRuleset renders the policy as an atomic `nft -f` script: the table is
// created if missing, deleted and recreated in one transaction, so there
// is never a moment without rules and re-running it is idempotent.
func nftRuleset(p egressPolicy) (string, error) {
	if !tableRe.MatchString(p.Table) {
		return "", fmt.Errorf("invalid nftables table name %q", p.Table)
	}
	nets := append([]egressNet(nil), p.Nets...)
	sort.Slice(nets, func(i, j int) bool { return nets[i].Bridge < nets[j].Bridge })
	seen := map[string]bool{}
	for _, n := range nets {
		if !ifaceRe.MatchString(n.Bridge) {
			return "", fmt.Errorf("network %s: invalid bridge interface name %q", n.Name, n.Bridge)
		}
		if seen[n.Bridge] {
			return "", fmt.Errorf("bridge %s listed twice", n.Bridge)
		}
		seen[n.Bridge] = true
	}
	for _, r := range p.Allow {
		if r.Proto != "" && r.Proto != "tcp" && r.Proto != "udp" {
			return "", fmt.Errorf("invalid protocol %q", r.Proto)
		}
	}

	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Generated by agentrove: box egress firewall (backend/CONVENTIONS.md, \"Box networks\").")
	w("# Boxes reach the internet, their own bridge (the API) and their DNS upstreams;")
	w("# the host, private, link-local and metadata addresses are dropped. Drop-only:")
	w("# an accept here never overrides Docker's or the host's rules.")
	w("table inet %s", p.Table)
	w("delete table inet %s", p.Table)
	w("table inet %s {", p.Table)
	w("\tset blocked4 {")
	w("\t\ttype ipv4_addr")
	w("\t\tflags interval")
	w("\t\telements = { %s }", strings.Join(egressBlocked4, ", "))
	w("\t}")
	w("\tset blocked6 {")
	w("\t\ttype ipv6_addr")
	w("\t\tflags interval")
	w("\t\telements = { %s }", strings.Join(egressBlocked6, ", "))
	w("\t}")

	w("\tchain forward {")
	w("\t\ttype filter hook forward priority -10; policy accept;")
	w("\t\tct state established,related accept")
	for _, n := range nets {
		// Same bridge: box <-> API. Only seen here when br_netfilter
		// sends bridged traffic through the IP hooks; never break it.
		w("\t\tiifname %q oifname %q accept", n.Bridge, n.Bridge)
		for _, a := range sortedAddrs(n.Trusted) {
			w("\t\tiifname %q %s saddr %s accept", n.Bridge, family(a), a)
		}
		if v4 := prefixesOf(n.Subnets, true); len(v4) > 0 {
			w("\t\tiifname %q ip saddr != { %s } drop", n.Bridge, strings.Join(v4, ", "))
		}
		w("\t\tiifname %q jump box_egress", n.Bridge)
	}
	w("\t}")

	w("\tchain box_egress {")
	if p.DNSAny {
		w("\t\tudp dport 53 accept")
		w("\t\ttcp dport 53 accept")
	} else {
		v4, v6 := splitAddrs(p.DNS)
		for _, fam := range []struct {
			name  string
			addrs []string
		}{{"ip", v4}, {"ip6", v6}} {
			if len(fam.addrs) == 0 {
				continue
			}
			w("\t\t%s daddr { %s } udp dport 53 accept", fam.name, strings.Join(fam.addrs, ", "))
			w("\t\t%s daddr { %s } tcp dport 53 accept", fam.name, strings.Join(fam.addrs, ", "))
		}
	}
	for _, r := range sortedRules(p.Allow) {
		fam := "ip"
		if r.Prefix.Addr().Is6() {
			fam = "ip6"
		}
		match := fmt.Sprintf("%s daddr %s", fam, prefixString(r.Prefix))
		switch {
		case r.Port != 0:
			proto := r.Proto
			if proto == "" {
				proto = "tcp"
			}
			match += fmt.Sprintf(" %s dport %d", proto, r.Port)
		case r.Proto != "":
			match += " meta l4proto " + r.Proto
		}
		w("\t\t%s accept", match)
	}
	if ports := sortedPorts(p.Hairpin); len(ports) > 0 {
		// A public address of this host DNAT'd to a published container
		// port (e.g. Traefik on 80/443): no more than internet access.
		w("\t\tmeta l4proto tcp ct status dnat ct original ip daddr != @blocked4 ct original proto-dst { %s } accept", strings.Join(ports, ", "))
	}
	w("\t\tip daddr @blocked4 counter drop")
	w("\t\tip6 daddr @blocked6 counter drop")
	w("\t}")

	w("\tchain input {")
	w("\t\ttype filter hook input priority -10; policy accept;")
	w("\t\tct state established,related accept")
	var bridges []string
	for _, n := range nets {
		for _, a := range sortedAddrs(n.Trusted) {
			w("\t\tiifname %q %s saddr %s accept", n.Bridge, family(a), a)
		}
		bridges = append(bridges, strconv.Quote(n.Bridge))
	}
	if len(bridges) > 0 {
		if ports := sortedPorts(p.APIPorts); p.HostAPI && len(ports) > 0 {
			w("\t\tiifname { %s } tcp dport { %s } accept", strings.Join(bridges, ", "), strings.Join(ports, ", "))
		}
		w("\t\tiifname { %s } jump box_input", strings.Join(bridges, ", "))
	}
	w("\t}")
	w("\tchain box_input {")
	// Boxes never need a host service: DNS is answered inside the box.
	w("\t\ticmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit } accept")
	w("\t\tcounter drop")
	w("\t}")
	w("}")

	return b.String(), nil
}

// nftBridgeRuleset is the layer-2 part, applied as its own transaction
// after nftRuleset so the IP policy never depends on bridge-family support.
// Frames between ports of one box bridge (box <-> API, box <-> box on the
// shared network) never reach the IP hooks unless br_netfilter is loaded,
// so they are filtered here: inside a box subnet only TCP to the API's
// ports and replies from them pass, never a new connection from those
// ports. The rules are stateless and don't name the API's address, so a
// redeployed API with a new address keeps working even before the next
// apply. They are scoped by subnet, not bridge name (meta ibrname needs
// nft_meta_bridge, missing on some kernels): Docker subnets don't overlap,
// so a subnet names one bridge.
func nftBridgeRuleset(p egressPolicy) (string, error) {
	if !tableRe.MatchString(p.Table) {
		return "", fmt.Errorf("invalid nftables table name %q", p.Table)
	}
	nets := append([]egressNet(nil), p.Nets...)
	sort.Slice(nets, func(i, j int) bool { return nets[i].Bridge < nets[j].Bridge })
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("table bridge %s", p.Table)
	w("delete table bridge %s", p.Table)
	w("table bridge %s {", p.Table)
	w("\tchain forward {")
	w("\t\ttype filter hook forward priority -10; policy accept;")
	ports := strings.Join(sortedPorts(p.APIPorts), ", ")
	for _, n := range nets {
		subnets := prefixesOf(n.Subnets, true)
		if len(subnets) == 0 || ports == "" {
			continue
		}
		subs := strings.Join(subnets, ", ")
		w("\t\t# %s (%s)", n.Name, n.Bridge)
		w("\t\tip saddr { %s } ip daddr { %s } tcp dport { %s } accept", subs, subs, ports)
		w("\t\tip saddr { %s } ip daddr { %s } tcp sport { %s } tcp flags & (syn | ack) != syn accept", subs, subs, ports)
		w("\t\tip saddr { %s } ip daddr { %s } counter drop", subs, subs)
	}
	w("\t}")
	w("}")
	return b.String(), nil
}

func family(a netip.Addr) string {
	if a.Is4() || a.Is4In6() {
		return "ip"
	}
	return "ip6"
}

func prefixString(p netip.Prefix) string {
	if p.IsSingleIP() {
		return p.Addr().String()
	}
	return p.Masked().String()
}

func prefixesOf(ps []netip.Prefix, v4 bool) []string {
	set := map[string]bool{}
	for _, p := range ps {
		if p.IsValid() && p.Addr().Is4() == v4 {
			set[prefixString(p)] = true
		}
	}
	return sortedKeys(set)
}

func sortedAddrs(as []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(as))
	seen := map[netip.Addr]bool{}
	for _, a := range as {
		a = a.Unmap()
		if a.IsValid() && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

func splitAddrs(as []netip.Addr) (v4, v6 []string) {
	for _, a := range sortedAddrs(as) {
		if a.Is4() {
			v4 = append(v4, a.String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	return v4, v6
}

func sortedRules(rs []egressRule) []egressRule {
	out := append([]egressRule(nil), rs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if c := a.Prefix.Addr().Compare(b.Prefix.Addr()); c != 0 {
			return c < 0
		}
		if a.Prefix.Bits() != b.Prefix.Bits() {
			return a.Prefix.Bits() < b.Prefix.Bits()
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return a.Port < b.Port
	})
	return out
}

func sortedPorts(ps []uint16) []string {
	set := map[uint16]bool{}
	for _, p := range ps {
		if p != 0 {
			set[p] = true
		}
	}
	nums := make([]int, 0, len(set))
	for p := range set {
		nums = append(nums, int(p))
	}
	sort.Ints(nums)
	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = strconv.Itoa(n)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseEgressAllow reads SANDBOX_EGRESS_ALLOW: comma/space separated
// entries "CIDR", "ADDR[/BITS]:PORT", "[V6[/BITS]]:PORT", each optionally
// suffixed "/tcp" or "/udp" (a port alone means tcp). Bad entries are
// returned as errors and skipped.
func parseEgressAllow(s string) ([]egressRule, []error) {
	var out []egressRule
	var errs []error
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		r, err := parseEgressEntry(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("SANDBOX_EGRESS_ALLOW %q: %w", f, err))
			continue
		}
		out = append(out, r)
	}
	return out, errs
}

func parseEgressEntry(s string) (egressRule, error) {
	var r egressRule
	for _, proto := range []string{"tcp", "udp"} {
		if rest, ok := strings.CutSuffix(s, "/"+proto); ok {
			s, r.Proto = rest, proto
			break
		}
	}
	host, port := s, ""
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return r, fmt.Errorf("missing ]")
		}
		host = s[1:end]
		if rest := s[end+1:]; rest != "" {
			p, ok := strings.CutPrefix(rest, ":")
			if !ok {
				return r, fmt.Errorf("expected :PORT after ]")
			}
			port = p
		}
	} else if strings.Count(s, ":") == 1 {
		host, port, _ = strings.Cut(s, ":")
	}
	prefix, err := parsePrefixOrAddr(host)
	if err != nil {
		return r, err
	}
	r.Prefix = prefix
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return r, fmt.Errorf("bad port %q", port)
		}
		r.Port = uint16(n)
		if r.Proto == "" {
			r.Proto = "tcp"
		}
	}
	return r, nil
}

func parsePrefixOrAddr(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// parseEgressPorts reads SANDBOX_EGRESS_HAIRPIN_PORTS ("80,443").
func parseEgressPorts(s string) ([]uint16, []error) {
	var out []uint16
	var errs []error
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.ParseUint(f, 10, 16)
		if err != nil || n == 0 {
			errs = append(errs, fmt.Errorf("SANDBOX_EGRESS_HAIRPIN_PORTS: bad port %q", f))
			continue
		}
		out = append(out, uint16(n))
	}
	return out, errs
}

// parseEgressDNS reads SANDBOX_EGRESS_DNS: "" = discover, "any" = port 53
// to any address, else a list of resolver addresses.
func parseEgressDNS(s string) (addrs []netip.Addr, any, set bool, errs []error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false, false, nil
	}
	if strings.EqualFold(s, "any") {
		return nil, true, true, nil
	}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		a, err := netip.ParseAddr(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("SANDBOX_EGRESS_DNS %q: %w", f, err))
			continue
		}
		addrs = append(addrs, a.Unmap())
	}
	return addrs, false, true, errs
}

// dockerResolver is Docker's embedded DNS server inside a container.
var dockerResolver = netip.MustParseAddr("127.0.0.11")

// parseResolvConf finds the upstream servers Docker's embedded resolver
// forwards to, from a resolv.conf in a container on a user-defined network
// (the API's own /etc/resolv.conf). Docker >= 25 records them as
// "# ExtServers: [10.0.0.2 host(127.0.0.53)]"; host(...) entries are
// queried from the host's namespace and need no rule. Without that line it
// falls back to plain nameserver lines (an API running on the host); known
// is false when the upstreams can't be determined (older Docker, or a
// loopback stub like systemd-resolved, behind which Docker uses other
// servers).
func parseResolvConf(data []byte) (addrs []netip.Addr, known bool) {
	var nameservers []netip.Addr
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "# ExtServers:"); ok {
			rest = strings.Trim(strings.TrimSpace(rest), "[]")
			out := []netip.Addr{}
			for _, f := range strings.Fields(rest) {
				if strings.HasPrefix(f, "host(") {
					continue
				}
				if a, err := netip.ParseAddr(f); err == nil {
					out = append(out, a.Unmap())
				}
			}
			return out, true
		}
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				nameservers = append(nameservers, a.Unmap())
			}
		}
	}
	if len(nameservers) == 0 {
		return nil, false
	}
	for _, a := range nameservers {
		if a.IsLoopback() || a == dockerResolver {
			return nil, false
		}
	}
	return nameservers, true
}
