#!/bin/bash
# HOME is the user's persistent host dir: give a fresh one shell defaults.
if [ ! -f "$HOME/.bashrc" ]; then
    cp -n /etc/skel/.bashrc /etc/skel/.profile "$HOME"/ 2>/dev/null
fi
mkdir -p "$HOME/projects" "$HOME/claude" "$HOME/.local/bin"
exec "$@"
