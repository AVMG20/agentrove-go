# One image for the whole app: the Go API also serves the built frontend
# (FRONTEND_DIR), so no separate web server is needed.
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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agentrove ./cmd/agentrove

FROM debian:bookworm-slim
ARG CLAUDE_VERSION=2.1.308

# git/rg serve host-side reads of workspace files; the claude CLI handles title,
# commit-message and other one-shot generations (run as uid 1000); nft is run
# by the short-lived box egress firewall helper (same image, host network).
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git ripgrep tmux bash procps nftables \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 1000 -s /bin/bash agent \
    && curl -fsSL https://claude.ai/install.sh | bash -s ${CLAUDE_VERSION} \
    && cp -L /root/.local/bin/claude /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.local/bin/claude \
    && claude --version

COPY --from=build /out/agentrove /usr/local/bin/agentrove
COPY --from=web /app/dist /usr/share/agentrove/web

ENV STORAGE_PATH=/data/agentrove/storage \
    FRONTEND_DIR=/usr/share/agentrove/web \
    PORT=8080
VOLUME /data/agentrove/storage
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/agentrove"]
