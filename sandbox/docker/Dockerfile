# Claude-only sandbox image: one container per workspace, started on demand
# and stopped by the API after SANDBOX_IDLE_TTL_SECONDS of inactivity.
FROM ubuntu:24.04

ARG CLAUDE_VERSION=2.1.308
ARG NODE_MAJOR=22

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/user \
    TERM=xterm-256color \
    PATH="/home/user/.local/bin:$PATH" \
    CLAUDE_CONFIG_DIR=/home/user/.claude

# git from the git-core PPA: worktrees use `git worktree add --relative-paths` (git >= 2.48).
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg software-properties-common \
    && add-apt-repository -y ppa:git-core/ppa \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
    && curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get install -y --no-install-recommends \
        git gh nodejs tmux ripgrep fd-find jq less tree file vim nano wget unzip zip \
        build-essential python3 python3-venv python3-pip pipx sudo openssh-client \
        procps psmisc lsof rsync patch iproute2 netcat-openbsd dnsutils \
    && ln -sf "$(command -v fdfind)" /usr/local/bin/fd \
    && rm -rf /var/lib/apt/lists/*

# ubuntu:24.04 ships an "ubuntu" user at uid 1000; the sandbox user must own
# that uid. HOME already exists (apt tooling ran with HOME=/home/user), so chown it.
RUN (userdel -r ubuntu 2>/dev/null || true) \
    && useradd -m -s /bin/bash -u 1000 user \
    && echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user \
    && chmod 0440 /etc/sudoers.d/user \
    && chown -R user:user /home/user

COPY --chmod=755 sandbox/docker/entrypoint.sh /usr/local/bin/entrypoint.sh

USER user
WORKDIR /home/user

RUN curl -fsSL https://claude.ai/install.sh | bash -s ${CLAUDE_VERSION} \
    && claude --version \
    && pipx install uv \
    && printf '#!/bin/sh\nexec echo "$GITHUB_TOKEN"\n' > /home/user/.git-askpass.sh \
    && chmod +x /home/user/.git-askpass.sh \
    && mkdir -p /home/user/.claude /home/user/workspace \
    && echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["sleep", "infinity"]
