#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

BUILD_DIR="${BUILD_DIR:-build}"
mkdir -p "$BUILD_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required but was not found in PATH."
  echo "Install Go from https://go.dev/dl/ and try again."
  exit 1
fi

echo "[cs2-server-manager] Building CSM (CS2 Server Manager CLI)..."
go build -o "${BUILD_DIR}/csm" ./src/cmd/cs2-tui

echo "[cs2-server-manager] Launching CSM with DEBUG logging enabled..."
CSM_ROOT="$PWD" DEBUG=1 exec "${BUILD_DIR}/csm"
