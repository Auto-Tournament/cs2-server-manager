#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

MODE="${1:-}"
TAG=""
CSM_DRY_RUN="${CSM_DRY_RUN:-false}"
CSM_WEBHOOK_DRY_RUN="${CSM_WEBHOOK_DRY_RUN:-false}"

VERSION_FILE="src/internal/tui/version.go"

if [[ "$MODE" =~ ^(patch|minor|major)$ ]]; then
  # Auto-bump mode: read currentVersion from version.go and compute next tag.
  if [[ ! -f "$VERSION_FILE" ]]; then
    echo "Error: $VERSION_FILE not found; cannot auto-bump version."
    exit 1
  fi

  CURRENT_VERSION=$(grep 'const[[:space:]]\+currentVersion' "$VERSION_FILE" | sed -E 's/.*"([^"]+)".*/\1/' | head -n 1)
  if [[ -z "$CURRENT_VERSION" ]]; then
    echo "Could not determine currentVersion from $VERSION_FILE"
    exit 1
  fi

  # Strip leading "v" if present, then split into semver components.
  BASE="${CURRENT_VERSION#v}"
  IFS='.' read -r MAJOR MINOR PATCH <<< "$BASE"
  MAJOR=${MAJOR:-0}
  MINOR=${MINOR:-0}
  PATCH=${PATCH:-0}

  case "$MODE" in
    patch)
      PATCH=$((PATCH + 1))
      ;;
    minor)
      MINOR=$((MINOR + 1))
      PATCH=0
      ;;
    major)
      MAJOR=$((MAJOR + 1))
      MINOR=0
      PATCH=0
      ;;
  esac

  TAG="v${MAJOR}.${MINOR}.${PATCH}"

  echo "[csm] Bumping version: ${CURRENT_VERSION} -> ${TAG}"

  # Update currentVersion in version.go to match the new tag.
  # Use a portable in-place edit (creates a .bak on macOS/BSD).
  # We anchor on the const line to avoid touching comments.
  sed -i.bak -E 's/^(const[[:space:]]+currentVersion[[:space:]]*=[[:space:]]*")([^"]+)(")/\1'"${TAG}"'\3/' "$VERSION_FILE"
  rm -f "${VERSION_FILE}.bak"

elif [[ "$MODE" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  # Explicit tag provided, e.g. v0.2.0
  TAG="$MODE"
else
  echo "Usage:"
  echo "  ./scripts/release.sh vX.Y.Z        # create release for explicit tag"
  echo "  ./scripts/release.sh patch         # bump patch version and release"
  echo "  ./scripts/release.sh minor         # bump minor version and release"
  echo "  ./scripts/release.sh major         # bump major version and release"
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
if [[ -f "$VERSION_FILE" ]]; then
  FILE_VERSION=$(grep 'const[[:space:]]\+currentVersion' "$VERSION_FILE" | sed -E 's/.*\"([^\"]+)\".*/\1/' | head -n 1)
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


