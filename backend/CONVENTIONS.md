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
`SANDBOX_PER_USER_NETWORKS` (default on) gives each user's box its own
bridge, `agentrove-user-net-<user id without dashes>` (labels
`agentrove.managed`, `agentrove.instance`, `agentrove.user`), so boxes can't
reach each other. The API only drives boxes over the Docker socket (exec);
boxes call the API (MCP) at `AGENTROVE_INTERNAL_URL`, so the API container
joins every user network with the alias `api` (plus that URL's host) and
rejoins them at startup. It finds its own container by hostname (Docker's
default, the short id) or `AGENTROVE_API_CONTAINER`; if neither works it logs
a warning once and boxes stay on the shared `DOCKER_NETWORK`, as with the
flag off. Existing boxes move networks by connect/disconnect the next time
they start (not recreated, so installed packages survive). Boxes still get
internet access through the bridge's NAT.

Verify on the host (`<id>` = user id without dashes):
```sh
docker network ls --filter label=agentrove.user        # one per user
docker network inspect agentrove-user-net-<id>         # box + API container
docker inspect -f '{{json .NetworkSettings.Networks}}' agentrove-sandbox-box-<id>
docker exec agentrove-sandbox-box-<id> curl -fsS http://api:8080/health   # works
docker exec agentrove-sandbox-box-<id> curl -m 3 http://<other box IP>:1  # times out; "refused" = reachable
docker exec agentrove-sandbox-box-<id> curl -fsSI https://registry.npmjs.org # egress works
```
Turn it off with `SANDBOX_PER_USER_NETWORKS=false`; boxes move back to the
shared network on their next start.

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
  half-life, which restarts the chat's CLI with `--resume`. Use
  `httpx.ClientIP(r, cfg.TrustedProxies, authService.BoxSubnets)` for the
  client address, never `X-Forwarded-For` directly: box network subnets
  (recorded by the Docker provider) are never trusted as proxies.
- `internal/usage` — usage meter. Endpoint follows `ANTHROPIC_BASE_URL`
  (official `https://api.anthropic.com/api/oauth/usage`, else
  `<ANTHROPIC_BASE_URL>/api/usage`).
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
