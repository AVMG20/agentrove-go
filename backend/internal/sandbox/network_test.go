package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"agentrove/internal/config"
	"agentrove/internal/httpx"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

const netUser = "0a1b2c3d-0000-4000-8000-000000000001"

// fakeNet is an in-memory daemon: networks with their attached containers.
type fakeNet struct {
	containers map[string]string            // ref (name or id prefix) -> full id
	networks   map[string]map[string]bool   // name -> container ids
	labels     map[string]map[string]string // name -> labels
	aliases    map[string][]string          // network -> aliases of the last connect
	subnets    map[string]string            // network -> IPAM subnet
	ips        map[string]map[string]string // network -> container id -> IPv4
	creates    map[string]network.CreateOptions
	endpoints  map[string]*network.EndpointSettings // network -> last connect settings
	drivers    map[string]string                    // network -> driver ("" = bridge)
	options    map[string]map[string]string         // network -> driver options
	connects   int
}

func newFakeNet() *fakeNet {
	return &fakeNet{containers: map[string]string{}, networks: map[string]map[string]bool{},
		labels: map[string]map[string]string{}, aliases: map[string][]string{}, subnets: map[string]string{},
		ips: map[string]map[string]string{}, creates: map[string]network.CreateOptions{},
		endpoints: map[string]*network.EndpointSettings{}, drivers: map[string]string{}, options: map[string]map[string]string{}}
}

// fakeNetID is a stable 64-hex network id, so bridge names are br-<id[:12]>.
func fakeNetID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func (f *fakeNet) NetworkInspect(_ context.Context, name string, _ network.InspectOptions) (network.Inspect, error) {
	members, ok := f.networks[name]
	if !ok {
		return network.Inspect{}, errdefs.NotFound(errors.New("network " + name + " not found"))
	}
	driver := f.drivers[name]
	if driver == "" {
		driver = "bridge"
	}
	out := network.Inspect{Name: name, ID: fakeNetID(name), Driver: driver, Labels: f.labels[name], Options: f.options[name], Containers: map[string]network.EndpointResource{}}
	if sub := f.subnets[name]; sub != "" {
		out.IPAM.Config = []network.IPAMConfig{{Subnet: sub}}
	}
	for id := range members {
		ep := network.EndpointResource{}
		if ip := f.ips[name][id]; ip != "" {
			ep.IPv4Address = ip + "/16"
		}
		out.Containers[id] = ep
	}
	return out, nil
}

func (f *fakeNet) NetworkCreate(_ context.Context, name string, o network.CreateOptions) (network.CreateResponse, error) {
	if _, ok := f.networks[name]; ok {
		return network.CreateResponse{}, errors.New("network with name " + name + " already exists")
	}
	f.networks[name] = map[string]bool{}
	f.labels[name] = o.Labels
	f.creates[name] = o
	f.subnets[name] = fmt.Sprintf("172.%d.0.0/16", 20+len(f.subnets))
	return network.CreateResponse{ID: fakeNetID(name)}, nil
}

func (f *fakeNet) NetworkList(_ context.Context, o network.ListOptions) ([]network.Summary, error) {
	var out []network.Summary
	for name, labels := range f.labels {
		match := true
		for _, want := range o.Filters.Get("label") {
			k, v, hasV := strings.Cut(want, "=")
			got, ok := labels[k]
			if !ok || (hasV && got != v) {
				match = false
			}
		}
		if match {
			info, _ := f.NetworkInspect(context.Background(), name, network.InspectOptions{})
			out = append(out, info)
		}
	}
	return out, nil
}

func (f *fakeNet) NetworkConnect(_ context.Context, name, id string, cfg *network.EndpointSettings) error {
	members, ok := f.networks[name]
	if !ok {
		return errdefs.NotFound(errors.New("no network"))
	}
	if members[id] {
		return errors.New("endpoint with name x already exists in network " + name)
	}
	members[id] = true
	f.connects++
	if cfg != nil {
		f.aliases[name] = cfg.Aliases
		f.endpoints[name] = cfg
	}
	if sub := f.subnets[name]; sub != "" {
		if f.ips[name] == nil {
			f.ips[name] = map[string]string{}
		}
		base := netip.MustParsePrefix(sub).Addr().Next()
		for range len(f.ips[name]) + 1 {
			base = base.Next()
		}
		f.ips[name][id] = base.String()
	}
	return nil
}

func (f *fakeNet) NetworkDisconnect(_ context.Context, name, id string, _ bool) error {
	delete(f.networks[name], id)
	return nil
}

func (f *fakeNet) ContainerInspect(_ context.Context, ref string) (container.InspectResponse, error) {
	id, ok := f.containers[ref]
	if !ok {
		return container.InspectResponse{}, errdefs.NotFound(errors.New("no such container"))
	}
	info := container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: id, Name: "/" + ref,
		Image: "sha256:img-" + id[:4], State: &container.State{Running: true}},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{}}}
	for name, members := range f.networks {
		if members[id] {
			info.NetworkSettings.Networks[name] = &network.EndpointSettings{IPAddress: f.ips[name][id]}
		}
	}
	return info, nil
}

const apiID = "abcdef123456aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newNetProvider(perUser bool, apiContainer, hostname string) (*DockerProvider, *fakeNet) {
	f := newFakeNet()
	f.containers["abcdef123456"] = apiID
	f.networks["agentrove-sandbox-net"] = map[string]bool{apiID: true}
	cfg := &config.Config{DockerNetwork: "agentrove-sandbox-net", SandboxPerUserNetworks: perUser, APIContainer: apiContainer, HostStoragePath: "/s"}
	d := &DockerProvider{cfg: cfg, net: f, joined: map[string]bool{}, subnets: httpx.NewPrefixSet(),
		hostname: func() (string, error) { return hostname, nil }}
	d.netOnce.Do(func() {}) // shared network exists; skip the real client
	return d, f
}

func TestUserNetworkName(t *testing.T) {
	if got := UserNetworkName(netUser); got != "agentrove-user-net-0a1b2c3d000040008000000000000001" || !isUserNetwork(got) {
		t.Fatalf("name = %q", got)
	}
	if isUserNetwork("agentrove-sandbox-net") {
		t.Fatal("shared network counted as per-user")
	}
}

func TestAPIAliases(t *testing.T) {
	for in, want := range map[string][]string{
		"":                       {"api"},
		"http://api:8080":        {"api"},
		"http://api-pr-3:8080":   {"api", "api-pr-3"},
		"http://172.18.0.2:8080": {"api"},
	} {
		if got := apiAliases(in); !reflect.DeepEqual(got, want) {
			t.Errorf("apiAliases(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBoxNetworkPerUser(t *testing.T) {
	d, f := newNetProvider(true, "", "abcdef123456")
	t.Setenv("AGENTROVE_INTERNAL_URL", "http://api:8080")
	name := d.boxNetwork(context.Background(), netUser)
	if name != UserNetworkName(netUser) {
		t.Fatalf("network = %q", name)
	}
	if !f.networks[name][apiID] {
		t.Fatal("API did not join the user network")
	}
	if l := f.labels[name]; l[ManagedLabel] != "true" || l[UserLabel] != netUser || l[InstanceLabel] != d.instance() {
		t.Fatalf("labels = %v", l)
	}
	if !reflect.DeepEqual(f.aliases[name], []string{"api"}) {
		t.Fatalf("aliases = %v", f.aliases[name])
	}
	if ep := f.endpoints[name]; ep == nil || ep.GwPriority >= 0 {
		t.Fatalf("API may become its default gateway: %+v", ep)
	}
	if o := f.creates[name]; o.Driver != "bridge" || o.EnableIPv6 == nil || *o.EnableIPv6 {
		t.Fatalf("create options = %+v", o)
	}
	// Idempotent: cached, and a fresh provider sees the existing membership.
	d.boxNetwork(context.Background(), netUser)
	d2 := &DockerProvider{cfg: d.cfg, net: f, joined: map[string]bool{}, hostname: d.hostname}
	d2.netOnce.Do(func() {})
	if d2.boxNetwork(context.Background(), netUser) != name || f.connects != 1 {
		t.Fatalf("reconnected %d times", f.connects)
	}
}

func TestBoxNetworkFallsBack(t *testing.T) {
	for name, d := range map[string]*DockerProvider{
		"flag off":     func() *DockerProvider { d, _ := newNetProvider(false, "", "abcdef123456"); return d }(),
		"no container": func() *DockerProvider { d, _ := newNetProvider(true, "", "laptop"); return d }(),
		"bad explicit": func() *DockerProvider { d, _ := newNetProvider(true, "missing", "abcdef123456"); return d }(),
	} {
		if got := d.boxNetwork(context.Background(), netUser); got != "agentrove-sandbox-net" {
			t.Errorf("%s: network = %q", name, got)
		}
	}
	// An explicit container name works even when the hostname isn't the id.
	d, f := newNetProvider(true, "agentrove-api-1", "custom-host")
	f.containers["agentrove-api-1"] = apiID
	if got := d.boxNetwork(context.Background(), netUser); got != UserNetworkName(netUser) {
		t.Fatalf("explicit: network = %q", got)
	}
}

func TestReconnectNetworks(t *testing.T) {
	d, f := newNetProvider(true, "", "abcdef123456")
	ours, other := UserNetworkName(netUser), UserNetworkName("ffff")
	f.networks[ours], f.labels[ours] = map[string]bool{}, d.userNetworkLabels(netUser)
	// Another instance's network on the same daemon is left alone.
	f.networks[other], f.labels[other] = map[string]bool{}, map[string]string{ManagedLabel: "true", InstanceLabel: "other", UserLabel: "ffff"}
	d.ReconnectNetworks(context.Background())
	if !f.networks[ours][apiID] || f.networks[other][apiID] {
		t.Fatalf("membership = %v", f.networks)
	}
	d.ReconnectNetworks(context.Background())
	if f.connects != 1 {
		t.Fatalf("connects = %d", f.connects)
	}
}

func TestPlanBoxNetworks(t *testing.T) {
	user := UserNetworkName(netUser)
	cases := []struct {
		name       string
		current    []string
		target     string
		connect    bool
		disconnect []string
	}{
		{"migrate from shared", []string{"agentrove-sandbox-net"}, user, true, []string{"agentrove-sandbox-net"}},
		{"already there", []string{user}, user, false, nil},
		{"back to shared when off", []string{user}, "agentrove-sandbox-net", true, []string{user}},
		{"keeps foreign networks", []string{"agentrove-sandbox-net", "my-db-net", user}, user, false, []string{"agentrove-sandbox-net"}},
		{"no network configured", []string{"bridge"}, "", false, nil},
	}
	for _, tc := range cases {
		connect, disconnect := planBoxNetworks(tc.current, tc.target, "agentrove-sandbox-net")
		if connect != tc.connect || !reflect.DeepEqual(disconnect, tc.disconnect) {
			t.Errorf("%s: connect=%v disconnect=%v", tc.name, connect, disconnect)
		}
	}
}

func TestMoveBox(t *testing.T) {
	d, f := newNetProvider(true, "", "abcdef123456")
	f.containers["box"] = "boxid"
	f.networks["agentrove-sandbox-net"]["boxid"] = true
	target := d.boxNetwork(context.Background(), netUser)
	info, _ := f.ContainerInspect(context.Background(), "box")
	if err := d.moveBox(context.Background(), info, target); err != nil {
		t.Fatal(err)
	}
	if !f.networks[target]["boxid"] || f.networks["agentrove-sandbox-net"]["boxid"] {
		t.Fatalf("box networks = %v", f.networks)
	}
	if !f.networks["agentrove-sandbox-net"][apiID] {
		t.Fatal("API left the shared network")
	}
}

func TestBoxSubnetsRecorded(t *testing.T) {
	d, f := newNetProvider(true, "", "abcdef123456")
	f.subnets["agentrove-sandbox-net"] = "172.18.0.0/16"
	existing := UserNetworkName("ffff")
	f.networks[existing], f.labels[existing], f.subnets[existing] = map[string]bool{}, d.userNetworkLabels("ffff"), "10.99.0.0/24"
	d.LoadBoxSubnets(context.Background())
	for _, ip := range []string{"172.18.0.4", "10.99.0.7"} {
		if !d.subnets.Contains(netip.MustParseAddr(ip)) {
			t.Errorf("%s not recorded at startup", ip)
		}
	}
	// A network created later is recorded when the API joins it.
	name := d.boxNetwork(context.Background(), netUser)
	sub := netip.MustParsePrefix(f.subnets[name])
	if !d.subnets.Contains(sub.Addr().Next()) {
		t.Fatalf("new network %s (%s) not recorded", name, sub)
	}
	if d.subnets.Contains(netip.MustParseAddr("192.168.1.1")) {
		t.Fatal("unrelated address recorded")
	}
}
