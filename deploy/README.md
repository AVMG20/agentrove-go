# Box network containment

Each user's agents run in a Docker "box" that runs code an attacker can control
(prompt injection), as root through `sudo`. A box must reach the internet (npm,
pip, git over https and ssh, the web) and the API (`http://api:8080`, for MCP).
It must not reach anything else.

## What a box can reach without a host firewall

| Path | Without rules | Why |
|---|---|---|
| Other users' boxes | blocked | Each user gets their own bridge (`agentrove-user-net-<id>`), and Docker drops traffic between bridges. Boxes that fall back to the shared `DOCKER_NETWORK` *can* reach each other. |
| `api:8080` | open (needed) | The API joins every box bridge with the alias `api`. |
| Other API ports, the API's other networks | open on its own bridge | Traffic between containers on one bridge never passes iptables. |
| **The host itself**: the gateway IP, `docker0`, LAN or public IPs, any port | **open** | That traffic is local to the host (INPUT), and Docker never filters INPUT. Any host service bound to `0.0.0.0` or `[::]` is reachable: SSH, Coolify's SSH-to-host, databases, Docker TCP API. |
| **Published container ports** (Coolify dashboard `:8000`, realtime `:6001/6002`, Traefik) through any host IP | **open** | Docker DNATs them from every interface, box bridges included. A cloud firewall in front of the host does not apply here. |
| **LAN/VPC, CGNAT/Tailscale, other hosts' private IPs** | **open** | The box's traffic is forwarded and masqueraded like any container's. |
| **Cloud metadata** (`169.254.169.254`, `fd00:ec2::254`, Azure `168.63.129.16`) | **open** | Same: masqueraded to the host's address, so it can fetch instance credentials. |
| Compose / Coolify services by name (`web`, `coolify`, `coolify-db`) | not resolvable | Docker's embedded DNS (`127.0.0.11` in the box) only answers for containers on the box's own network, which are the box and the API's aliases. |
| IPv6 | host over link-local | Box networks have no IPv6, but a link-local `fe80::` address can still reach host services on `[::]` (INPUT). |
| Docker socket | not mounted | Only the box's home and projects are bind-mounted. A Docker TCP socket on the host would be reachable through the host row above. |

## The fix: an nftables policy for the box bridges

The rules live in their own nftables tables, one `inet` and one `bridge`. They only
drop traffic. In nftables a drop in any base chain is final, and an accept only ends
that chain. So these tables can't open anything that Docker, Coolify or the host
firewall blocks, and they work whether Docker uses iptables-nft, iptables-legacy or
its own nftables backend. Docker and Coolify never touch these tables. Rules match
the box bridges by interface name, so a forged source address doesn't get around them.

For traffic arriving on a box bridge:

- **Forwarded traffic** (`inet` table, `forward` hook): established flows and
  same-bridge traffic pass. Traffic to the DNS servers that Docker's embedded resolver
  forwards to is allowed on port 53 (the resolver sends those queries from the box's
  namespace, so a private VPC resolver or home router needs this). Operator exceptions
  pass. Everything else to private, CGNAT, loopback, link-local, metadata, multicast or
  reserved addresses, or their IPv6 equivalents, is dropped. That includes published
  ports, which are DNAT'd to private container IPs before this hook sees them. Sources
  outside the bridge's subnet are dropped. The public internet passes.
- **Traffic to the host** (`inet` table, `input` hook): dropped, except IPv6 neighbour
  discovery. Boxes never need a host service.
- **Same-bridge frames** (`bridge` table, its own transaction, so the IP policy holds
  even if this part fails): inside a box subnet, only TCP to the API's port (`PORT`, the
  port of `AGENTROVE_INTERNAL_URL`) and replies from that port pass. A reply never has
  SYN without ACK, so the API port can't be used as a source to open connections. This
  blocks box → other API ports. On the shared fallback network it also blocks box → box,
  except on that port. The rules are stateless and don't name the API's address, so a
  redeployed API with a new IP stays reachable.
- **The API's own traffic**: the API is exempt on the shared network, because Docker
  often picks that network (the lexically first) as the API's default route. The API
  joins per-user networks with `GwPriority=-1` so that it never routes through them.

### Option A (default): the API applies the rules

The API already holds the Docker socket. At startup, before a box starts on a
network the rules don't cover yet, and every 10 minutes after that (to repair a
firewall reload), the API runs a short-lived helper container. The helper runs the
API's own image with `--network host --userns host --cap-drop ALL --cap-add NET_ADMIN`,
a read-only root filesystem and no new privileges, and executes
`nft -f`. Each run is logged (`box egress firewall applied`, with bridges, DNS and a
ruleset hash; the full ruleset at `LOG_LEVEL=DEBUG`). A failure is logged as
`box egress firewall NOT applied` and doesn't block boxes. The table is
`agentrove_box_<instance>`.

| Env | Default | |
|---|---|---|
| `SANDBOX_EGRESS_FIREWALL` | `on` | `off` removes this instance's tables and stops managing them (kill switch). `dry-run` logs the ruleset and validates it with `nft -c` without applying it. |
| `SANDBOX_EGRESS_ALLOW` | none | Private destinations boxes may reach, e.g. `10.0.0.5:5432,192.168.1.0/24,10.0.0.6:53/udp,[fd00::5]:443`. |
| `SANDBOX_EGRESS_DNS` | discovered | Resolver addresses allowed on port 53, or `any`. They are discovered from `# ExtServers:` in the API's `/etc/resolv.conf` (Docker 25+). If that line is missing, port 53 is allowed to any address and a warning is logged. |
| `SANDBOX_EGRESS_HAIRPIN_PORTS` | none | Public ports published on this host that boxes may reach through a public address, e.g. `80,443` so agents can open the app's own URL. Caveat: Traefik then sees a private source address, so IP-allowlist middlewares that trust private ranges would let boxes in. |
| `SANDBOX_EGRESS_IMAGE` | the API's image | The helper image. It needs `/bin/sh` and `nft`. |

Requirements: a kernel with nf_tables (any current distro), and `nft` in the API image.

### Option B: the host script (transparent, no API involvement)

`deploy/agentrove-egress.sh` generates the same policy from `docker network inspect` and
applies it to the tables `inet/bridge agentrove_box_host`. Run it as root on the host:

```sh
deploy/agentrove-egress.sh --dry-run    # print and validate, apply nothing
deploy/agentrove-egress.sh              # apply once
deploy/agentrove-egress.sh remove
# Keep it applied: re-applies on Docker network events and every 10 minutes.
install -m 755 deploy/agentrove-egress.sh /usr/local/sbin/
install -m 644 deploy/agentrove-egress.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now agentrove-egress
```

If you use the script, set `SANDBOX_EGRESS_FIREWALL=off` on the API. Both policies
only drop, so an exception allowed in one is still dropped by the other. The script
reacts to network events, so a new user's first box can run unfiltered for about a
second. Option A has no such window.

### Not chosen: internal networks plus an egress proxy

Box networks with `internal: true` and an HTTP(S) proxy as the only way out would allow
per-domain allowlists. But they break everything that isn't HTTP: git over ssh, raw TCP,
package managers that ignore `HTTPS_PROXY`, and UDP. Agents would hit those failures
constantly. Use this only if a domain allowlist is required. You can still layer it on
later, because the nftables policy stays valid.

## Host verification checklist

Run on the host. `B` is a running box; start one by opening a chat first.

```sh
B=$(docker ps --filter label=agentrove.box=true --format '{{.Names}}' | head -1)
GW=$(docker exec $B ip route | awk '/default/ {print $3}')
API=$(docker exec $B getent hosts api | awk '{print $1}')
T=$(nft list tables | awk '/agentrove_box_/ {print $3; exit}')
nft list table inet $T; nft list table bridge $T     # rules present
docker logs <api container> 2>&1 | grep 'box egress firewall'
```

Must **succeed**:

```sh
docker exec $B curl -fsS -m 5 http://api:8080/health
docker exec $B curl -fsSI -m 10 https://registry.npmjs.org
docker exec $B pip download --no-deps -q -d /tmp/x six
docker exec $B git ls-remote https://github.com/git/git HEAD
docker exec $B ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 -T git@github.com  # "Permission denied (publickey)" = reachable
docker exec $B dig +short pypi.org
```

Must **fail with a timeout**. `curl` exits 28 or `nc` reports "timed out". If you get
"Connection refused", the port is reachable but closed, which means the rule isn't
working:

```sh
docker exec $B curl -sS -m 3 http://$GW:8000          # Coolify dashboard via gateway
docker exec $B nc -zvw3 $GW 22                         # host SSH
docker exec $B nc -zvw3 172.17.0.1 22                  # docker0
docker exec $B curl -sS -m 3 http://<host public IP>:8000
docker exec $B nc -zvw3 <host LAN IP> 22
docker exec $B curl -sS -m 3 http://169.254.169.254/latest/meta-data/
docker exec $B curl -sS -m 3 http://168.63.129.16/      # Azure WireServer
docker exec $B nc -zvw3 <a LAN host> 80
docker exec $B nc -zvw3 $API 22                         # another port on the API
docker exec $B curl -sS -m 3 http://<API IP on its compose/coolify network>:8080/health
docker exec $B nc -zvw3 <another box's IP> 22           # docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' <other box>
docker exec $B getent hosts web coolify coolify-db coolify-proxy   # no output
```

IPv6:

```sh
docker exec $B ip -6 addr                       # no global address (link-local at most)
docker exec $B curl -6 -sS -m 3 https://ipv6.google.com            # fails: no IPv6 route
H6=$(ip -6 addr show dev <box bridge> | awk '/fe80/ {print $2}' | cut -d/ -f1)
docker exec $B nc -6 -zvw3 "$H6%eth0" 22         # host over link-local: times out
```

Counters show what was dropped: `nft list chain inet $T box_egress`, `... box_input`.

To roll back: `SANDBOX_EGRESS_FIREWALL=off` and restart the API (it removes its tables).
Alternatively, run `nft delete table inet $T; nft delete table bridge $T`.
