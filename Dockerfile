# One binary for the whole app: the frontend is embedded into the Go API
# (internal/web), so no separate web server is needed.
FROM oven/bun:1-slim AS web
WORKDIR /app
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun --bun run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /app/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agentrove ./cmd/agentrove

FROM debian:bookworm-slim

# git/rg serve host-side reads of workspace files; the claude CLI handles title,
# commit-message and other one-shot generations (run as uid 1000); nft is run
# by the short-lived box egress firewall helper (same image, host network).
# The latest Claude Code release; a new one invalidates the cached install.
ADD https://downloads.claude.ai/claude-code-releases/latest /tmp/claude-version
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git ripgrep tmux bash procps nftables \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 1000 -s /bin/bash agent \
    && curl -fsSL https://claude.ai/install.sh | bash -s "$(cat /tmp/claude-version)" \
    && cp -L /root/.local/bin/claude /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.local/bin/claude /tmp/claude-version \
    && claude --version

COPY --from=build /out/agentrove /usr/local/bin/agentrove

ENV STORAGE_PATH=/data/agentrove/storage \
    PORT=8080
VOLUME /data/agentrove/storage
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/agentrove"]
