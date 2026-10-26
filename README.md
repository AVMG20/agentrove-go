# Bitbox

Bitbox (formerly Agentrove) is a Go rewrite of the Agentrove backend, as a web app only. Internal names (Docker images and containers, env vars, storage paths, the Go module and the `agentrove` MCP server id) keep the old name. It runs Claude Code, Codex and Antigravity through provider accounts, needs no Redis and stores everything in SQLite. The React frontend in `frontend/` is the upstream app with the desktop/mobile shells removed; the API keeps its paths and JSON shapes.

## What changed vs. the Python backend

- **One Go binary.** It uses net/http, SQLite (modernc, pure Go, WAL), JWT/bcrypt auth and the Docker SDK. Streaming, queues and pub/sub run in process, so Redis and the SQLAdmin dashboard are gone.
- **Claude first.** The `claude` CLI is driven directly over its stream-json control protocol, with no ACP adapter. There is one long-lived CLI process per chat. Model and permission-mode switches apply live. Idle processes close after `CHAT_PROCESS_IDLE_TTL_SECONDS` and `--resume` their session on the next message, including after an API restart.
- **Provider accounts.** Settings → Providers holds named agent accounts, and every one is optional: Claude (Claude sign-in, a Claude token, or a custom endpoint with base URL, token and usage URL), Codex (ChatGPT sign-in or an OpenAI API key) and Antigravity (Google sign-in or a Gemini API key). Sign-ins run the CLI's own login in the box and show its link and code in Settings. Each account keeps its own config dir in the box, and tokens are stored encrypted and never sent back. The composer's account control (bottom left) shows the thread's account with its plan usage and switches accounts; once a thread has messages its account is fixed, and picking another one opens a new thread in the same project. A user without accounts is asked to add one before starting a thread. Codex and Antigravity run one `codex exec --json` / `agy -p` process per turn, resumed by thread id; Claude-only features (permission modes, personas, slash commands) stay on Claude threads. Automations can run on any provider's model: the run uses the default account when it is that provider's, else the provider's oldest account. Titles and prompt enhancing use the default Claude account (and are skipped without one).
- **One box per user, stopped when idle.** All of a user's projects run in one Docker container (`/home/user/projects/<project>`) with a persistent home, so toolchains installed once serve every project. A box with no running turn or open terminal for `SANDBOX_IDLE_TTL_SECONDS` (default 15m) is stopped. The next request that needs it (a message, terminal or git mutation) starts it again, rebuilt from the current image if that changed.
- **Reads don't wake containers.** Workspaces live on a host dir that is bind-mounted into the sandbox. File browsing, diffs, git status and search read the host copy directly, so they work while the container is stopped.
- **Claude login per user.** The login is stored under `STORAGE_PATH/agent-homes/<user>/claude` and mounted into every sandbox of that user. Title, commit-message and PR generation use the same login.
- **A browser in every box.** Agents get Playwright MCP tools (navigate, click, type, hover, dropdowns, dialogs, scrolling, snapshots, screenshots, console and network) for one shared headless Chromium in the box, which reaches the dev servers they start on `localhost`. The Browser view (globe button, top right) shows the same browser live and lets you use it (tabs, URL bar, window size, reset). The browser profile persists in the box and is shared with the agent, so anything you log into in the Browser view is usable by the agent; Reset wipes it. Chromium starts on first use and stops after `SANDBOX_BROWSER_IDLE_SECONDS` (default 10m) with no agent or viewer attached; turn the agent tools off per user in Settings, or for everyone with `SANDBOX_BROWSER_ENABLED=false`.
- **MCP over HTTP.** The agentrove MCP server (chats, automations, artifacts) is served by the API at `/api/v1/mcp`. Each session gets it automatically, so there is no stdio sidecar.

## Run with Docker

```sh
cp .env.example .env               # set SECRET_KEY (openssl rand -hex 32)
./sandbox/docker/build.sh          # builds agentrove-sandbox:latest
docker compose up --build
```

Open http://localhost:8080 (`APP_PORT`). The frontend is embedded into the Go binary, so one container (and one executable) serves the whole app. The first account you register becomes the superuser. Add your agent accounts under Settings → Providers. The sandbox image ships the latest `claude`, `codex` and Antigravity's `agy`: a build after a new release of any of them installs it (the Dockerfiles `ADD` each CLI's release pointer, so a new release invalidates the cached install), and a box picks up a rebuilt image on its next start.

For production (e.g. Coolify), use `docker-compose-production.yml`. It builds the sandbox image and the app image (point the domain at `api`, port 8080) and keeps data in `/data/agentrove-go/storage`. Set `SECRET_KEY` and `APP_URL`.

The API trusts `X-Forwarded-For` only from `TRUSTED_PROXIES` (default: loopback and private ranges, which covers Traefik on the Docker network). Requests from the box networks never count as a proxy, so code in a box can't forge its address. Don't publish the API port directly on a network where untrusted clients have private addresses.

Box networking: each user's box gets its own bridge network. The API firewalls boxes on the host with nftables, so they reach the internet and `api:8080` but not the host, other containers, the LAN or cloud metadata. To do this it runs a short-lived `NET_ADMIN` helper container with host networking. `SANDBOX_EGRESS_FIREWALL=off` turns this off, and `dry-run` only validates the rules. To manage the rules on the host instead, use `deploy/agentrove-egress.sh`. `deploy/README.md` has the reachability map, settings (`SANDBOX_EGRESS_ALLOW` for LAN services boxes may use) and a verification checklist.

### Usage meter

The composer shows the plan usage of the thread's account (`GET /api/v1/usage?profile_id=`), and its account list shows each account's. A Claude sign-in reads Anthropic's OAuth usage endpoint with the sign-in's access token, as does a Claude token that is an OAuth `sk-ant-oat…` token; plain API keys have no usage. A custom endpoint (e.g. `https://claude.bitsec.dev`) reads `<base URL>/api/usage` with the endpoint's token, or the account's usage URL if set (e.g. `https://claude-admin.bitsec.dev/api/usage` for bitsec); those URLs must resolve to a public address. A Codex ChatGPT sign-in reads ChatGPT's rate limits (5-hour and weekly, or monthly on the free plan). Antigravity reports no usage.

Claude credentials, base URL and usage URL are no longer environment variables in Settings: on upgrade, existing ones (`CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_USAGE_URL`) become a "Claude (from env vars)" account (made the default if you had none, with your threads moved onto it) when the credential Claude used was your own; the account sends the token in the same variable. Threads from before accounts move onto a "Claude" sign-in account on the box's `~/claude` when that login exists; otherwise they ask for a new thread.

## Run locally without Compose

Projects always run in the user's box, so Docker is required. Build the box image once, then run the API and the Vite dev server (hot reload, proxies `/api` to the API):

```sh
sandbox/docker/build.sh
cd backend
SECRET_KEY=$(openssl rand -hex 32) STORAGE_PATH=$PWD/../storage go run ./cmd/agentrove
cd ../frontend && bun install && API_PROXY_TARGET=http://localhost:8080 bun run dev
```

Open http://localhost:3000. To have the API serve a built frontend instead, run `bun run build` and start the API with `FRONTEND_DIR=../frontend/dist`.

To build the single executable (API with the frontend embedded):

```sh
cd frontend && bun install && bun run build && cp -r dist/. ../backend/internal/web/dist/
cd ../backend && CGO_ENABLED=0 go build -o agentrove ./cmd/agentrove
```

It still needs Docker on the host (for the boxes), plus `git`, `rg` and the `claude` CLI on its `PATH`. The Vite dev server also works against the compose stack: `API_PROXY_TARGET=http://localhost:8080 bun run dev`.

With Docker Desktop on macOS, also set `DOCKER_HOST=unix://$HOME/.docker/run/docker.sock`. Each user's HOME lives in `STORAGE_PATH/agent-homes/<user>` and their projects in `STORAGE_PATH/workspaces/<user>`.

## Layout

```
backend/
  cmd/agentrove        entrypoint
  internal/
    claude             CLI session driver, stream translation, permissions, one-shot generation
    codex antigravity  per-turn CLI adapters (JSON lines → the same stream events)
    profiles           provider profiles: storage, launch env, status, in-box sign-in
    chat               chat API, turn runtime, SSE feeds, queue, attachments
    sandbox            docker/host providers, idle reaper, leases
    sandboxapi         files, git, search, terminal and browser websockets
    browserview        live viewer of the box browser (CDP over docker exec)
    workspace          workspace lifecycle
    gitops             worktrees, checkpoints, host-side git
    automation         cron + GitHub-triggered automations
    artifact           published HTML artifacts
    mcpserver          agentrove MCP tools over HTTP
    auth settings github skills usage
    db                 SQLite + embedded migrations
  cmd/agentrove-browser-mcp  lazy Playwright MCP proxy, built into the box image
sandbox/docker         sandbox image (claude, codex, agy, agentrove-browser)
frontend/              React app
```

## Tests

```sh
cd backend && go vet ./... && go test -race ./...
cd frontend && bun run typecheck && bun run lint && bunx vitest run
```

## Not ported

- The Tauri desktop and iOS apps, and the desktop app's "cloud" mode.
- The browser-use MCP (replaced by Playwright MCP on a browser in the box).
- The other non-Claude agents (Copilot, Cursor, OpenCode, Grok).

Existing Python databases are not migrated; start with a fresh storage dir.
