#!/usr/bin/env bash
# agentrove-egress.sh: host-side box egress firewall (the transparent
# alternative to the API applying it, see deploy/README.md).
#
# Boxes (Agentrove's per-user agent containers) may reach the internet and
# the API on its port, nothing else: not the host (any address, any port),
# other containers or boxes, the LAN/VPC, link-local or cloud metadata.
# Rules live in their own nftables tables ("inet" and "bridge" families,
# named $TABLE), are drop-only and never touch Docker's or the host's rules.
#
# Usage (root, needs docker and nft on the host):
#   agentrove-egress.sh            apply now
#   agentrove-egress.sh --dry-run  print the rules and validate them (nft -c), apply nothing
#   agentrove-egress.sh print      print the rules only
#   agentrove-egress.sh watch      apply now and again whenever a Docker network
#                                  is created/removed or a container connects (systemd unit)
#   agentrove-egress.sh remove     delete the tables
#
# Settings (environment):
#   AGENTROVE_DOCKER_NETWORK  shared box network (default agentrove-sandbox-net)
#   AGENTROVE_API_PORTS       API ports boxes may use (default 8080)
#   AGENTROVE_EGRESS_ALLOW    extra IPv4 destinations: "10.0.0.5:5432,192.168.1.0/24,10.0.0.6:53/udp"
#   AGENTROVE_EGRESS_DNS      resolver IPv4s, or "any"; default: discovered
#   AGENTROVE_EGRESS_HAIRPIN_PORTS  public ports published on this host boxes may reach (default none)
#   AGENTROVE_EGRESS_TABLE    table name (default agentrove_box_host)
#
# Keep it in sync with backend/internal/sandbox/egress.go (same policy).
set -euo pipefail

SHARED="${AGENTROVE_DOCKER_NETWORK:-agentrove-sandbox-net}"
API_PORTS="${AGENTROVE_API_PORTS:-8080}"
ALLOW="${AGENTROVE_EGRESS_ALLOW:-}"
DNS="${AGENTROVE_EGRESS_DNS:-}"
HAIRPIN="${AGENTROVE_EGRESS_HAIRPIN_PORTS:-}"
TABLE="${AGENTROVE_EGRESS_TABLE:-agentrove_box_host}"

BLOCKED4="0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 168.63.129.16, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.88.99.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3"
BLOCKED6="::/127, ::ffff:0:0/96, 64:ff9b::/96, 64:ff9b:1::/48, 100::/64, 2001::/32, 2001:db8::/32, 2002::/16, fc00::/7, fe80::/10, fec0::/10, ff00::/8"

log() { echo "agentrove-egress: $*" >&2; }
die() { log "$*"; exit 1; }

[[ "$TABLE" =~ ^[a-z_][a-z0-9_]{0,63}$ ]] || die "bad table name $TABLE"
csv() { tr ', ' '\n\n' <<<"$1" | sed '/^$/d'; }
join() { local IFS=,; echo "$*" | sed 's/,/, /g'; }
is_ip4() { [[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$ ]]; }
is_port() { [[ "$1" =~ ^[0-9]{1,5}$ ]] && (( $1 > 0 && $1 < 65536 )); }

# Box networks: the shared one plus every per-user one (label agentrove.user).
networks() {
  { docker network inspect "$SHARED" -f '{{.Name}}' 2>/dev/null || true
    docker network ls -q --filter label=agentrove.managed=true --filter label=agentrove.user; } | sort -u
}

# One line per network: name bridge subnets(comma) api_ips(comma)
describe() {
  local n="$1" info name id driver bridge subnets api
  info=$(docker network inspect "$n" -f '{{.Name}}|{{.Id}}|{{.Driver}}|{{range $k, $v := .Options}}{{if eq $k "com.docker.network.bridge.name"}}{{$v}}{{end}}{{end}}|{{range .IPAM.Config}}{{.Subnet}},{{end}}|{{range .Containers}}{{.Name}}={{.IPv4Address}},{{end}}') || return 0
  IFS='|' read -r name id driver bridge subnets api <<<"$info"
  if [[ "$driver" != bridge ]]; then log "skipping $name: driver $driver"; return 0; fi
  bridge="${bridge:-br-${id:0:12}}"
  [[ "$bridge" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$ ]] || { log "skipping $name: bad bridge $bridge"; return 0; }
  # The API is the attached container that isn't a box.
  local ips=() e
  for e in $(csv "$api"); do
    [[ "${e%%=*}" == agentrove-sandbox-* ]] && continue
    e="${e#*=}"; e="${e%%/*}"; is_ip4 "$e" && ips+=("$e")
  done
  local v4=() s
  for s in $(csv "$subnets"); do is_ip4 "$s" && v4+=("$s"); done
  local sub_csv api_csv
  sub_csv=$(IFS=,; echo "${v4[*]:-}"); api_csv=$(IFS=,; echo "${ips[*]:-}")
  echo "$name $bridge ${sub_csv:--} ${api_csv:--}"
}

api_container() {
  local n="$1"
  docker network inspect "$n" -f '{{range .Containers}}{{.Name}} {{end}}' 2>/dev/null | tr ' ' '\n' | grep -v '^agentrove-sandbox-' | sed '/^$/d' | head -1
}

# Upstreams of Docker's embedded resolver, read from the API's resolv.conf
# ("# ExtServers: [...]", Docker >= 25). Empty output + rc 1 = unknown.
discover_dns() {
  local api conf
  api=$(api_container "$SHARED") || true
  [[ -n "$api" ]] || return 1
  conf=$(docker exec "$api" cat /etc/resolv.conf 2>/dev/null) || return 1
  grep -q '^# ExtServers:' <<<"$conf" || return 1
  sed -n 's/^# ExtServers: *\[\(.*\)\]/\1/p' <<<"$conf" | tr ' ' '\n' | grep -v '^host(' | grep -E '^[0-9.]+$' || true
}

# fields <line>: sets name bridge subnets api ("-" = none).
fields() {
  read -r name bridge subnets api <<<"$1"
  [[ "$subnets" == - ]] && subnets=""
  [[ "$api" == - ]] && api=""
  return 0
}

ruleset() {
  local lines=() l name bridge subnets api
  while read -r n; do l=$(describe "$n"); [[ -n "$l" ]] && lines+=("$l"); done < <(networks)
  IFS=$'\n' lines=($(printf '%s\n' "${lines[@]}" | sort -k2 -u)); unset IFS

  echo "# Generated by deploy/agentrove-egress.sh: Agentrove box egress firewall."
  echo "table inet $TABLE"
  echo "delete table inet $TABLE"
  echo "table inet $TABLE {"
  printf '\tset blocked4 {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t\telements = { %s }\n\t}\n' "$BLOCKED4"
  printf '\tset blocked6 {\n\t\ttype ipv6_addr\n\t\tflags interval\n\t\telements = { %s }\n\t}\n' "$BLOCKED6"
  printf '\tchain forward {\n\t\ttype filter hook forward priority -10; policy accept;\n\t\tct state established,related accept\n'
  local bridges=()
  for l in "${lines[@]}"; do
    fields "$l"
    bridges+=("\"$bridge\"")
    printf '\t\tiifname "%s" oifname "%s" accept\n' "$bridge" "$bridge"
    if [[ "$name" == "$SHARED" && -n "$api" ]]; then
      for a in $(csv "$api"); do printf '\t\tiifname "%s" ip saddr %s accept\n' "$bridge" "$a"; done
    fi
    [[ -n "$subnets" ]] && printf '\t\tiifname "%s" ip saddr != { %s } drop\n' "$bridge" "$(join $(csv "$subnets"))"
    printf '\t\tiifname "%s" jump box_egress\n' "$bridge"
  done
  printf '\t}\n\tchain box_egress {\n'
  local dns_list
  if [[ "$DNS" == any ]]; then
    printf '\t\tudp dport 53 accept\n\t\ttcp dport 53 accept\n'
  else
    if [[ -n "$DNS" ]]; then dns_list=$(csv "$DNS"); elif ! dns_list=$(discover_dns); then
      log "DNS upstreams unknown; allowing port 53 to any address (set AGENTROVE_EGRESS_DNS to narrow it)"; dns_list=any; fi
    if [[ "$dns_list" == any ]]; then
      printf '\t\tudp dport 53 accept\n\t\ttcp dport 53 accept\n'
    else
      local d4=() d
      for d in $dns_list; do is_ip4 "$d" && d4+=("$d"); done
      if (( ${#d4[@]} )); then
        printf '\t\tip daddr { %s } udp dport 53 accept\n\t\tip daddr { %s } tcp dport 53 accept\n' "$(join "${d4[@]}")" "$(join "${d4[@]}")"
      fi
    fi
  fi
  local e host port proto
  for e in $(csv "$ALLOW"); do
    proto=tcp; [[ "$e" == */udp ]] && { proto=udp; e="${e%/udp}"; }; e="${e%/tcp}"
    host="$e"; port=""
    [[ "$e" == *:* ]] && { host="${e%:*}"; port="${e##*:}"; }
    is_ip4 "$host" || { log "ignoring AGENTROVE_EGRESS_ALLOW entry $e"; continue; }
    if [[ -n "$port" ]]; then is_port "$port" || { log "ignoring $e: bad port"; continue; }
      printf '\t\tip daddr %s %s dport %s accept\n' "$host" "$proto" "$port"
    else printf '\t\tip daddr %s accept\n' "$host"; fi
  done
  local hp=() p
  for p in $(csv "$HAIRPIN"); do is_port "$p" && hp+=("$p"); done
  if (( ${#hp[@]} )); then
    printf '\t\tmeta l4proto tcp ct status dnat ct original ip daddr != @blocked4 ct original proto-dst { %s } accept\n' "$(join "${hp[@]}")"
  fi
  printf '\t\tip daddr @blocked4 counter drop\n\t\tip6 daddr @blocked6 counter drop\n\t}\n'
  printf '\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n\t\tct state established,related accept\n'
  for l in "${lines[@]}"; do
    fields "$l"
    if [[ "$name" == "$SHARED" && -n "$api" ]]; then
      for a in $(csv "$api"); do printf '\t\tiifname "%s" ip saddr %s accept\n' "$bridge" "$a"; done
    fi
  done
  (( ${#bridges[@]} )) && printf '\t\tiifname { %s } jump box_input\n' "$(join "${bridges[@]}")"
  printf '\t}\n\tchain box_input {\n\t\ticmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit } accept\n\t\tcounter drop\n\t}\n}\n'

  # Layer 2, separate transaction (nftBridgeRuleset): box -> API port only.
  BRIDGE_RULES="table bridge $TABLE
delete table bridge $TABLE
table bridge $TABLE {
	chain forward {
		type filter hook forward priority -10; policy accept;"
  local ports=() q
  for q in $(csv "$API_PORTS"); do is_port "$q" && ports+=("$q"); done
  for l in "${lines[@]}"; do
    fields "$l"
    [[ -n "$subnets" && ${#ports[@]} -gt 0 ]] || continue
    local subs pl
    subs=$(join $(csv "$subnets")); pl=$(join "${ports[@]}")
    # Stateless and API-address-free: TCP to the API port, replies from it
    # (never a new connection), nothing else inside the subnet.
    BRIDGE_RULES+="
		# $name ($bridge)
		ip saddr { $subs } ip daddr { $subs } tcp dport { $pl } accept
		ip saddr { $subs } ip daddr { $subs } tcp sport { $pl } tcp flags & (syn | ack) != syn accept
		ip saddr { $subs } ip daddr { $subs } counter drop"
  done
  BRIDGE_RULES+="
	}
}"
  echo "$BRIDGE_RULES" >"$BRIDGE_FILE"
}

apply() {
  local flags="${1:-}" inet
  BRIDGE_FILE=$(mktemp); inet=$(mktemp)
  trap 'rm -f "$BRIDGE_FILE" "$inet"' RETURN
  ruleset >"$inet"
  if [[ "$flags" == print ]]; then cat "$inet" "$BRIDGE_FILE"; return; fi
  [[ "$flags" == -c ]] && cat "$inet" "$BRIDGE_FILE"
  nft $flags -f "$inet" || die "inet table $TABLE NOT applied; boxes are unfiltered"
  if nft $flags -f "$BRIDGE_FILE"; then :; else log "WARNING: bridge table not applied; same-bridge traffic is unfiltered"; fi
  if [[ "$flags" == -c ]]; then log "rules valid (dry-run, nothing applied)"
  else log "applied tables inet/bridge $TABLE ($(grep -c 'jump box_egress' "$inet") box networks)"; fi
}

remove() {
  local fam
  for fam in inet bridge; do
    nft list table "$fam" "$TABLE" >/dev/null 2>&1 && nft delete table "$fam" "$TABLE" && log "removed $fam table $TABLE"
  done
  return 0
}

case "${1:-apply}" in
  apply) apply ;;
  --dry-run|dry-run|check) apply -c ;;
  print) apply print ;;
  remove) remove ;;
  watch)
    apply || true
    # Re-apply on network changes; also every 10 minutes to heal a firewall reload.
    while true; do
      timeout 600 docker events --filter type=network \
        --filter event=create --filter event=destroy --filter event=connect --format '{{.Action}} {{.Actor.Attributes.name}}' \
        | while read -r action net; do log "$action $net"; apply || true; done || true
      apply || true
    done ;;
  *) sed -n '2,30p' "$0"; exit 2 ;;
esac
