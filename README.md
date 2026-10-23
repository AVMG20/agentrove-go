# Agentrove (Go)

A Go rewrite of the Agentrove backend, as a web app only. It is Claude-only, needs no Redis and stores everything in SQLite. The React frontend in `frontend/` is the upstream app with the desktop/mobile shells removed; the API keeps its paths and JSON shapes.

## What changed vs. the Python backend

- **One Go binary.** It uses net/http, SQLite (modernc, pure Go, WAL), JWT/bcrypt auth and the Docker SDK. Streaming, queues and pub/sub run in process, so Redis and the SQLAdmin dashboard are gone.
- **Claude only.** The `claude` CLI is driven directly over its stream-json control protocol, with no ACP adapter. There is one long-lived CLI process per chat. Model and permission-mode switches apply live. Idle processes close after `CHAT_PROCESS_IDLE_TTL_SECONDS` and `--resume` their session on the next message, including after an API restart.
- **One box per user, stopped when idle.** All of a user's projects run in one Docker container (`/home/user/projects/<project>`) with a persistent home, so toolchains installed once serve every project. A box with no running turn or open terminal for `SANDBOX_IDLE_TTL_SECONDS` (default 15m) is stopped. The next request that needs it (a message, terminal or git mutation) starts it again, rebuilt from the current image if that changed.
- **Reads don't wake containers.** Workspaces live on a host dir that is bind-mounted into the sandbox. File browsing, diffs, git status and search read the host copy directly, so they work while the container is stopped.
- **Claude login per user.** The login is stored under `STORAGE_PATH/agent-homes/<user>/claude` and mounted into every sandbox of that user. Title, commit-message and PR generation use the same login.
- **MCP over HTTP.** The agentrove MCP server (chats, channels, automations, artifacts) is served by the API at `/api/v1/mcp`. Each session gets it automatically, so there is no stdio sidecar.

## Run with Docker

```sh
cp .env.example .env               # set SECRET_KEY (openssl rand -hex 32)
./sandbox/docker/build.sh          # builds agentrove-sandbox:latest
docker compose up --build
```

Open http://localhost:3000. The first account you register becomes the superuser. Open a terminal in a project and run `claude` once to log in, or set `CLAUDE_CODE_OAUTH_TOKEN` in `.env`.

For production (e.g. Coolify), use `docker-compose-production.yml`. It builds the sandbox image, serves the built frontend through nginx (`web`, port 80) and keeps data in `/data/agentrove-go/storage`. Set `SECRET_KEY` and `APP_URL`.

## Run locally without Compose

Projects always run in the user's box, so Docker is required. Build the box image once, then run the API and the frontend:

```sh
sandbox/docker/build.sh
cd backend
SECRET_KEY=$(openssl rand -hex 32) STORAGE_PATH=$PWD/../storage go run ./cmd/agentrove
cd ../frontend && bun install && API_PROXY_TARGET=http://localhost:8080 bun run dev
```

With Docker Desktop on macOS, also set `DOCKER_HOST=unix://$HOME/.docker/run/docker.sock`. Each user's HOME lives in `STORAGE_PATH/agent-homes/<user>` and their projects in `STORAGE_PATH/workspaces/<user>`.

## Layout

```
backend/
  cmd/agentrove        entrypoint
  internal/
    claude             CLI session driver, stream translation, permissions, one-shot generation
    chat               chat API, turn runtime, SSE feeds, queue, attachments
    sandbox            docker/host providers, idle reaper, leases
    sandboxapi         files, git, search, terminal websocket
    workspace          workspace lifecycle
    gitops             worktrees, checkpoints, host-side git
    channel            multi-agent channels
    automation         cron + GitHub-triggered automations
    artifact           published HTML artifacts
    mcpserver          agentrove MCP tools over HTTP
    auth settings github skills usage
    db                 SQLite + embedded migrations
sandbox/docker         Claude-only sandbox image
frontend/              React app
```

## Tests

```sh
cd backend && go vet ./... && go test -race ./...
cd frontend && bun run typecheck && bun run lint && bunx vitest run
```

## Not ported

- The Tauri desktop and iOS apps, and the desktop app's "cloud" mode.
- The browser-use MCP.
- The non-Claude agents (Codex, Copilot, Cursor, OpenCode, Grok, Antigravity).

Existing Python databases are not migrated; start with a fresh storage dir.
