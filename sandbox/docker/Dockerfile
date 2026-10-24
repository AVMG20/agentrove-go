# Sandbox image for a user's "box": one container per user holding all their
# projects (/home/user/projects) and a persistent home (/home/user, a host
# bind mount). Everything here lives outside HOME so the mount doesn't hide
# it. The API starts the box on demand and stops it after
# SANDBOX_IDLE_TTL_SECONDS of inactivity.

# The agents' browser MCP server: a lazy proxy in front of playwright-mcp
# (backend/cmd/agentrove-browser-mcp, standard library only).
FROM golang:1.26-bookworm AS browser-mcp
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
COPY backend/cmd/agentrove-browser-mcp ./cmd/agentrove-browser-mcp
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agentrove-browser-mcp ./cmd/agentrove-browser-mcp

FROM ubuntu:24.04

ARG CLAUDE_VERSION=2.1.308
ARG NODE_MAJOR=22
ARG PLAYWRIGHT_MCP_VERSION=0.0.83

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/user \
    TERM=xterm-256color \
    PATH="/home/user/.local/bin:/home/user/.cargo/bin:/home/user/go/bin:/usr/local/go/bin:$PATH" \
    CLAUDE_CONFIG_DIR=/home/user/claude \
    DISABLE_AUTOUPDATER=1

# git from the git-core PPA: worktrees use `git worktree add --relative-paths` (git >= 2.48).
# Language toolchains (Java, Rust, Go, ...) are installed by the agent into
# the persistent home on demand.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg software-properties-common \
    && add-apt-repository -y ppa:git-core/ppa \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
    && curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get install -y --no-install-recommends \
        git gh nodejs tmux ripgrep fd-find jq less tree file vim nano wget unzip zip \
        build-essential pkg-config libssl-dev python3 python3-venv python3-pip pipx sudo openssh-client \
        procps psmisc lsof rsync patch iproute2 netcat-openbsd dnsutils \
    && ln -sf "$(command -v fdfind)" /usr/local/bin/fd \
    && rm -rf /var/lib/apt/lists/*

# The box's shared headless browser (agentrove-browser) and Playwright MCP.
# Ubuntu's chromium package is a snap stub that doesn't run in containers, so
# this installs the Chromium headless shell that the pinned @playwright/mcp's
# own Playwright expects, with its libraries and fonts (Liberation, Noto
# Color Emoji, WenQuanYi/IPA for CJK). socat bridges the API's viewer to CDP.
# PLAYWRIGHT_BROWSERS_PATH is only set for the install: as image env it would
# send the projects' own Playwright to this read-only dir.
RUN apt-get update && apt-get install -y --no-install-recommends socat \
    && npm install -g --no-fund --no-audit @playwright/mcp@${PLAYWRIGHT_MCP_VERSION} \
    && PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright node /usr/lib/node_modules/@playwright/mcp/node_modules/playwright/cli.js \
        install --with-deps --only-shell chromium \
    && mkdir -p /usr/local/lib/agentrove-browser \
    && ln -s "$(find /opt/ms-playwright -type f -name chrome-headless-shell -perm -u+x | head -n 1)" /usr/local/lib/agentrove-browser/chrome \
    && /usr/local/lib/agentrove-browser/chrome --version \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/* /root/.npm

# Claude Code and uv as system binaries (HOME is a mount at runtime).
RUN curl -fsSL https://claude.ai/install.sh | bash -s ${CLAUDE_VERSION} \
    && install -m 755 "$(readlink -f /home/user/.local/bin/claude)" /usr/local/bin/claude \
    && rm -rf /home/user/.local /home/user/.claude* \
    && claude --version \
    && curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin UV_NO_MODIFY_PATH=1 sh \
    && printf '#!/bin/sh\nexec echo "$GITHUB_TOKEN"\n' > /usr/local/bin/git-askpass \
    && chmod 755 /usr/local/bin/git-askpass

# ubuntu:24.04 ships an "ubuntu" user at uid 1000; the sandbox user must own
# that uid.
RUN (userdel -r ubuntu 2>/dev/null || true) \
    && rm -rf /home/user \
    && useradd -m -s /bin/bash -u 1000 user \
    && echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user \
    && chmod 0440 /etc/sudoers.d/user

COPY --chmod=755 sandbox/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY --chmod=755 sandbox/docker/agentrove-browser /usr/local/bin/agentrove-browser
COPY --from=browser-mcp /out/agentrove-browser-mcp /usr/local/bin/agentrove-browser-mcp
# initialize + tools/list answers, so sessions don't start playwright-mcp
# until a tool is used.
RUN agentrove-browser-mcp -write-cache /usr/local/share/agentrove-browser/mcp-cache.json

USER user
WORKDIR /home/user/projects

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["sleep", "infinity"]
