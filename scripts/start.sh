#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

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
"$GO_CMD" build -o "${BUILD_DIR}/csm" ./src/cmd/cs2-tui

echo "[cs2-server-manager] Launching CSM with DEBUG logging enabled..."
# Ensure stdin is available for the TUI by explicitly redirecting from the terminal
CSM_ROOT="$PWD" DEBUG=1 exec "${BUILD_DIR}/csm" < /dev/tty
