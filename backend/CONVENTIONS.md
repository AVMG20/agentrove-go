# Agentrove Go backend — conventions

Rewrite of the Python/FastAPI Agentrove backend in Go. The
frontend in `../frontend` is unchanged, so **every endpoint must keep the
Python JSON contract** (paths, methods, field names, status codes, error
shape `{"detail": "..."}`). When unsure, read the Python endpoint + schema
and the frontend service/types that call it (`frontend/src/services/*.ts`,
`frontend/src/types/*.ts`).

Scope changes vs Python:
- **Claude only.** No Codex/Copilot/Cursor/Grok/OpenCode/Antigravity. Agent
  kind is always `"claude"`. Model ids are Claude ids/aliases.
- **No Redis.** In-process pub/sub (`internal/events.Hub`), queues in SQLite.
- **No ACP.** The Claude CLI is driven directly (`claude -p --input-format
  stream-json --output-format stream-json`).
- **Workspace files live on a host dir the API can read.** The user's Docker
  box bind-mounts their projects dir at `/home/user/projects` (and their
  persistent home at `/home/user`). Read-only operations (file tree,
  file content, diff, search, zip) run on the host through
  `Sandbox.HostPath(...)` so they work while the container is stopped.
  Mutations (writes via git commands, commit, push, checkout, …) and agent
  processes go through `Sandbox.Exec`/`Start`, which wake the container.
- **Idle containers stop** after `SANDBOX_IDLE_TTL_SECONDS` (default 900).
  Anything holding a container busy for a long time (terminal, agent turn)
  must hold `sandbox.Acquire()` / the release func from `Start`/`StartPTY`.

## Box networks
Treat a box as hostile: the agent in it can be prompt-injected and has root
through sudo. It may reach the internet and the API's HTTP port, nothing else.

`SANDBOX_PER_USER_NETWORKS` (default on) gives each user's box its own
bridge, `agentrove-user-net-<user id without dashes>` (labels
`agentrove.managed`, `agentrove.instance`, `agentrove.user`; IPv6 off), so
boxes can't reach each other. The API only drives boxes over the Docker
socket (exec). Boxes call the API (MCP) at `AGENTROVE_INTERNAL_URL`, so the
API container joins every user network with the alias `api` (plus that URL's
host) and `GwPriority=-1`, so the API's own traffic never routes through a
box bridge, and rejoins them at startup. It finds its own container by
hostname (Docker's default, the short id) or `AGENTROVE_API_CONTAINER`. If
neither works, it logs a warning once and boxes stay on the shared
`DOCKER_NETWORK`, as with the flag off. Existing boxes move networks by
connect/disconnect the next time they start. They aren't recreated, so
installed packages survive.

**Egress firewall** (`internal/sandbox/egress*.go`, `SANDBOX_EGRESS_FIREWALL`,
default `on`). Docker alone leaves the host itself (gateway and any host IP,
any port), published ports such as Coolify's `:8000`, the LAN/VPC and cloud
metadata reachable from a box. The API closes that with nftables tables
`inet` and `bridge` `agentrove_box_<instance>`, applied by a short-lived
helper container: the API image with host network, `NET_ADMIN` only and a
read-only root filesystem, running `nft`.
- Generation is pure and golden-tested: `nftRuleset` / `nftBridgeRuleset`
  turn an `egressPolicy` (bridges, subnets, API addresses, DNS upstreams,
  exceptions) into atomic `nft -f` scripts. The apply step is
  `egressApplyLocked` / `runEgressHelper`.
- The policy is drop-only:
  - Forwarded traffic from box bridges to private, CGNAT, link-local,
    metadata or host addresses (IPv4 and IPv6) is dropped.
  - Box → host (input) is dropped.
  - On the bridge itself, only TCP to the API's port and replies from it
    pass. These rules are stateless and don't name the API's address, so they
    survive an API redeploy.
  - DNS to the embedded resolver's upstreams is allowed. They're
    discovered from `# ExtServers:` in the API's resolv.conf; when unknown,
    port 53 is allowed to any address.
  - The API's own address is exempt on the shared network.
- The rules are applied at startup (`ReconnectNetworks`), synchronously in
  `boxNetwork` before a box starts on a network they don't cover, and every
  10 minutes as a repair. A failure is logged as an error and never blocks
  boxes.
- `off` removes this instance's tables. `dry-run` logs the ruleset and runs
  `nft -c` only.
- Other env: `SANDBOX_EGRESS_ALLOW`, `SANDBOX_EGRESS_DNS`,
  `SANDBOX_EGRESS_HAIRPIN_PORTS`, `SANDBOX_EGRESS_IMAGE`.
- `deploy/agentrove-egress.sh` (with a systemd unit) applies the same
  policy from the host instead. Use one or the other. `deploy/README.md`
  has the full reachability map.
- `Manager.IsBoxAddr(ctx, userID, ip)` tells whether a peer address is
  that user's running box, by exact IP on its per-user or the shared
  network. Use it to pin box-only credentials (MCP tokens) to the box.

Verify on the host (`<id>` = user id without dashes, `B=agentrove-sandbox-box-<id>`):
```sh
docker network ls --filter label=agentrove.user        # one per user
docker network inspect agentrove-user-net-<id>         # box + API container
nft list tables | grep agentrove_box_                  # inet + bridge tables
GW=$(docker exec $B ip route | awk '/default/ {print $3}')
docker exec $B curl -fsS http://api:8080/health        # works
docker exec $B curl -fsSI https://registry.npmjs.org   # works (also: git ls-remote https://github.com/git/git, ssh -T git@github.com)
docker exec $B curl -m 3 http://$GW:8000               # times out ("refused" = reachable!)
docker exec $B nc -zvw3 $GW 22                         # times out
docker exec $B curl -m 3 http://169.254.169.254/       # times out
docker exec $B nc -zvw3 <other box IP> 22              # times out
docker exec $B nc -zvw3 <LAN host / host LAN IP> 22    # times out
docker exec $B nc -zvw3 <api IP> 22                    # times out (only :8080 on the API)
docker exec $B ip -6 addr                              # no global IPv6
```
`deploy/README.md` has the full checklist, including IPv6 link-local to the
host. Turn per-user networks off with `SANDBOX_PER_USER_NETWORKS=false`;
boxes move back to the shared network on their next start, where the
bridge-table rules still keep them from each other.

## Layout
- `cmd/agentrove` — entrypoint.
- `internal/config` — env config. `internal/db` — SQLite + embedded
  migrations (`internal/db/migrations/NNN_name.sql`, applied in order).
- `internal/httpx` — `Wrap`, `JSON/OK/Created/NoContent`, `Decode`,
  `Error` (`httpx.NotFound("…")` etc.), `SSE`.
- `internal/auth` — `Service.Require(handler)` for authed routes,
  `auth.UserFrom(r.Context())` for the current user. `Require` accepts
  session tokens only; MCP / artifact-agent tokens are scoped to their own
  endpoints (`/api/v1/mcp`, artifacts). The MCP token lives
  `AGENTROVE_MCP_TOKEN_LIFETIME_SECONDS` (default 72h) and is re-minted at
  half-life, which restarts the chat's CLI with `--resume`. Agent tokens
  carry their chat (the box's `X-Agentrove-Chat-Id` header is ignored) and,
  with `AGENTROVE_AGENT_TOKENS_BOX_ONLY` (default on), are only accepted
  from the owning user's box (`sandbox.Manager.FromUserBox`, by TCP peer).
  Agents may only edit or delete agent-created personas and automations. Use
  `httpx.ClientIP(r, cfg.TrustedProxies, authService.BoxSubnets)` for the
  client address, never `X-Forwarded-For` directly: box network subnets
  (recorded by the Docker provider) are never trusted as proxies.
- `internal/usage` — usage meter. Endpoint follows `ANTHROPIC_BASE_URL`
  (official `https://api.anthropic.com/api/oauth/usage`, else
  `<ANTHROPIC_BASE_URL>/api/usage`); a `CLAUDE_USAGE_URL` env var (user or
  `AGENT_ENV`) replaces the endpoint.
- `internal/store` — ownership-checked `GetWorkspace`, `GetWorkspaceBySandbox`,
  `GetChat`, `GetMemberChat`, `GetUserSettings`, `UserEnv`.
- `internal/sandbox` — `Manager.For(workspace) -> *Sandbox`. `Sandbox.Exec`,
  `Shell`, `Start`, `StartPTY`, `HostPath`, `Resolve`, `Acquire`, `Touch`.
- `internal/app.App` — shared deps passed to every module, plus `Hooks` for
  cross-module calls (e.g. `Hooks.StartChatTurn`).
- `internal/server` — module wiring. Each group has its own file
  (`modules_chat.go`, `modules_workspace.go`, `modules_collab.go`); only edit
  your own group's file.

## Module pattern
```go
package foo

type Module struct{ a *app.App }

func New(a *app.App) *Module { return &Module{a: a} }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix
	mux.HandleFunc("GET "+p+"/foo/{id}", m.a.Auth.Require(m.get))
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	id := r.PathValue("id")
	...
	return httpx.OK(w, out)
}
```
Optional `Start(ctx)` (background loops) and `Stop(ctx)` (shutdown).

Note Go's ServeMux: a pattern ending in `/` matches subtrees; use `{$}` for an
exact trailing slash (e.g. `"GET "+p+"/settings/{$}"`). Register both
`/x` and `/x/` when the frontend may call either.

## Style
- Plain `database/sql` with `?` placeholders; ids are `uuid.NewString()`;
  timestamps `db.Now()` / `db.Time` / `db.NullTime`; JSON columns `db.JSON[T]`.
- Return errors; let `httpx.Wrap` render them. Wrap only where context helps.
- No global state except what a module owns. Comments explain *why*, briefly.
- `gofmt`, `go vet ./...` clean. Add focused tests where logic is non-trivial
  (parsers, path handling); use a temp SQLite DB via `db.Open(t.TempDir()+"/t.db")`.
- Don't run destructive commands against any existing database.
