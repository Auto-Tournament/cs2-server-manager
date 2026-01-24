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

# Try to find go in common locations if not in PATH
GO_CMD=""
if command -v go >/dev/null 2>&1; then
  GO_CMD="go"
elif [ -x "/usr/local/go/bin/go" ]; then
  GO_CMD="/usr/local/go/bin/go"
elif [ -x "/usr/local/bin/go" ]; then
  GO_CMD="/usr/local/bin/go"
elif [ -x "/opt/go/bin/go" ]; then
  GO_CMD="/opt/go/bin/go"
fi

if [ -z "$GO_CMD" ]; then
  echo "Go is required but was not found in PATH or common locations."
  echo "Install Go from https://go.dev/dl/ and try again."
  echo "Current PATH: $PATH"
  exit 1
fi

echo "[cs2-server-manager] Building CSM (CS2 Server Manager CLI)..."
# Avoid leaving root-owned build artifacts when invoked via sudo.
BUILD_USER="${SUDO_USER:-$(id -un)}"
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
  chown -R "${BUILD_USER}:${BUILD_USER}" "$BUILD_DIR" 2>/dev/null || true
  sudo -u "$BUILD_USER" -E "$GO_CMD" build -o "${BUILD_DIR}/csm" ./src/cmd/cs2-tui
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
