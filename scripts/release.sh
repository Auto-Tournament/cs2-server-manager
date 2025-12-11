#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

TAG="${1:-}"
CSM_DRY_RUN="${CSM_DRY_RUN:-false}"
CSM_WEBHOOK_DRY_RUN="${CSM_WEBHOOK_DRY_RUN:-false}"

if [[ -z "$TAG" ]]; then
  echo "Usage: ./scripts/release.sh vX.Y.Z"
  echo "Example: ./scripts/release.sh v0.2.0"
  echo
  echo "Environment flags:"
  echo "  CSM_DRY_RUN=true         Build binaries but DO NOT create a GitHub release or send webhooks."
  echo "  CSM_WEBHOOK_DRY_RUN=true Print the Discord payload instead of sending it."
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required but was not found in PATH."
  echo "Install Go from https://go.dev/dl/ and try again."
  exit 1
fi

if [[ "$CSM_DRY_RUN" != "true" ]]; then
  if ! command -v gh >/dev/null 2>&1; then
    echo "GitHub CLI (gh) is required to create releases."
    echo "Install it from https://cli.github.com/ and run 'gh auth login' first."
    exit 1
  fi
fi

# Ensure the internal CSM version matches the tag we are releasing.
VERSION_FILE="src/internal/tui/version.go"
if [[ -f "$VERSION_FILE" ]]; then
  FILE_VERSION=$(grep 'currentVersion' "$VERSION_FILE" | sed -E 's/.*\"([^\"]+)\".*/\1/')
  if [[ -z "$FILE_VERSION" ]]; then
    echo "Could not determine currentVersion from $VERSION_FILE"
    exit 1
  fi
  if [[ "$FILE_VERSION" != "$TAG" ]]; then
    echo "Version mismatch:"
    echo "  Tag:          $TAG"
    echo "  version.go:   $FILE_VERSION"
    echo ""
    echo "Please bump currentVersion in $VERSION_FILE to match the tag before releasing."
    exit 1
  fi
else
  echo "Warning: $VERSION_FILE not found; skipping version consistency check."
fi

DIST_DIR="dist/releases/${TAG}"
mkdir -p "${DIST_DIR}"

echo "[csm] Building release binaries for ${TAG}..."

# Linux amd64
GOOS=linux GOARCH=amd64 go build -o "${DIST_DIR}/csm-linux-amd64" ./src/cmd/cs2-tui

# Linux arm64 (common on ARM servers)
GOOS=linux GOARCH=arm64 go build -o "${DIST_DIR}/csm-linux-arm64" ./src/cmd/cs2-tui

if [[ "$CSM_DRY_RUN" == "true" ]]; then
  echo "[csm] DRY RUN: would create GitHub release ${TAG} with assets:"
  echo "  - ${DIST_DIR}/csm-linux-amd64"
  echo "  - ${DIST_DIR}/csm-linux-arm64"
else
  echo "[csm] Creating GitHub release ${TAG}..."

  gh release create "${TAG}" \
    "${DIST_DIR}/csm-linux-amd64" \
    "${DIST_DIR}/csm-linux-arm64" \
    --title "CSM ${TAG}" \
    --notes "CS2 Server Manager (CSM) release ${TAG}"

  echo "[csm] Release ${TAG} created with assets in ${DIST_DIR}"
fi

# Optional Discord webhook notification.
# Set CSM_DISCORD_WEBHOOK_URL to enable, e.g.:
#   export CSM_DISCORD_WEBHOOK_URL="https://discord.com/api/webhooks/..."
if [[ -n "${CSM_DISCORD_WEBHOOK_URL:-}" || "$CSM_WEBHOOK_DRY_RUN" == "true" ]]; then
  if [[ -z "${CSM_DISCORD_WEBHOOK_URL:-}" && "$CSM_WEBHOOK_DRY_RUN" != "true" ]]; then
    echo "[csm] Skipping Discord webhook: CSM_DISCORD_WEBHOOK_URL not set."
  fi

  echo "[csm] Preparing Discord release notification..."

  REPO_OWNER="${REPO_OWNER:-sivert-io}"
  REPO_NAME="${REPO_NAME:-cs2-server-manager}"
  RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/tag/${TAG}"

  CHANGELOG=$(git log --oneline --no-decorate "$(git describe --tags --abbrev=0 --match 'v*' --exclude="$TAG" 2>/dev/null)..${TAG}" 2>/dev/null | head -20)
  if [[ -z "$CHANGELOG" ]]; then
    CHANGELOG="- Release ${TAG}"
  else
    CHANGELOG="- " && CHANGELOG="- ${CHANGELOG//$'\n'/$'\n- '}"
  fi

  PAYLOAD=$(cat <<EOF
{
  "content": "🚀 **New CSM release: ${TAG}**",
  "embeds": [{
    "title": "CS2 Server Manager ${TAG}",
    "description": "A new version of CSM has been released.",
    "color": 3066993,
    "fields": [
      {
        "name": "📦 Changelog (recent commits)",
        "value": "$(printf '%s' "$CHANGELOG" | sed 's/\"/\\\"/g')",
        "inline": false
      },
      {
        "name": "🔗 GitHub Release",
        "value": "[View Release](${RELEASE_URL})",
        "inline": true
      }
    ]
  }]
}
EOF
)

  if [[ "$CSM_WEBHOOK_DRY_RUN" == "true" ]]; then
    echo "[csm] DRY RUN (webhook): would send the following payload to Discord:"
    echo "$PAYLOAD"
  elif [[ -n "${CSM_DISCORD_WEBHOOK_URL:-}" && "$CSM_DRY_RUN" != "true" ]]; then
    echo "[csm] Sending Discord release notification..."
    curl -sS -X POST \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD" \
      "$CSM_DISCORD_WEBHOOK_URL" >/dev/null || echo "[csm] Warning: failed to send Discord webhook"
  else
    echo "[csm] Skipping actual webhook send (CSM_DRY_RUN=${CSM_DRY_RUN}, CSM_WEBHOOK_DRY_RUN=${CSM_WEBHOOK_DRY_RUN})."
  fi
fi


