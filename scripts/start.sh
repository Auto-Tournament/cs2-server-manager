#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

# Ensure TERM is present for full-screen TUI (some sudo configs drop env vars).
export TERM="${TERM:-xterm-256color}"

# Preserve user's PATH when running with sudo (common locations for Go)
export PATH="${PATH}:/usr/local/go/bin:/usr/local/bin:/opt/go/bin"

BUILD_DIR="${BUILD_DIR:-build}"
mkdir -p "$BUILD_DIR"

# Try to find go in common locations - always use full path for sudo compatibility
GO_CMD=""
if [ -x "/usr/local/go/bin/go" ]; then
  GO_CMD="/usr/local/go/bin/go"
elif [ -x "/usr/local/bin/go" ]; then
  GO_CMD="/usr/local/bin/go"
elif [ -x "/opt/go/bin/go" ]; then
  GO_CMD="/opt/go/bin/go"
elif command -v go >/dev/null 2>&1; then
  # If found in PATH, get the full path
  GO_CMD="$(command -v go)"
fi

if [ -z "$GO_CMD" ]; then
  echo "Go is required but was not found in PATH or common locations."
  echo "Install Go from https://go.dev/dl/ and try again."
  echo ""
  echo "Checked locations:"
  echo "  - /usr/local/go/bin/go"
  echo "  - /usr/local/bin/go"
  echo "  - /opt/go/bin/go"
  echo "  - PATH: $PATH"
  exit 1
fi

echo "[cs2-server-manager] Using Go at: $GO_CMD"

echo "[cs2-server-manager] Building CSM (CS2 Server Manager CLI)..."
# Avoid leaving root-owned build artifacts when invoked via sudo.
BUILD_USER="${SUDO_USER:-$(id -un)}"
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
  chown -R "${BUILD_USER}:${BUILD_USER}" "$BUILD_DIR" 2>/dev/null || true
  
  # Set Go cache directories to build user's home to avoid permission issues
  BUILD_USER_HOME=$(getent passwd "$BUILD_USER" | cut -d: -f6)
  export GOCACHE="${BUILD_USER_HOME}/.cache/go-build"
  export GOMODCACHE="${BUILD_USER_HOME}/go/pkg/mod"
  
  sudo -u "$BUILD_USER" \
    env GOCACHE="$GOCACHE" GOMODCACHE="$GOMODCACHE" PATH="$PATH" TERM="$TERM" \
    "$GO_CMD" build -o "${BUILD_DIR}/csm" ./src/cmd/cs2-tui
else
  "$GO_CMD" build -o "${BUILD_DIR}/csm" ./src/cmd/cs2-tui
fi

echo "[cs2-server-manager] Launching CSM with DEBUG logging enabled..."
# Force stdio to the controlling TTY so the Bubble Tea renderer can initialize,
# even when launched from tmux or other wrappers.
if [ "$(id -u)" -eq 0 ]; then
  CSM_ROOT="$PWD" CSM_LOG_DIR="$PWD/logs" DEBUG=1 exec "${BUILD_DIR}/csm" </dev/tty >/dev/tty 2>/dev/tty
else
  exec sudo -E env CSM_ROOT="$PWD" CSM_LOG_DIR="$PWD/logs" DEBUG=1 "${BUILD_DIR}/csm" </dev/tty >/dev/tty 2>/dev/tty
fi
