#!/usr/bin/env bash
set -euo pipefail

# CSM (CS2 Server Manager) - Discord Webhook Script
# Sends a Discord webhook notification for a CSM release.

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Locate project root and source .env if present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_ROOT}"

if [ -f "${PROJECT_ROOT}/.env" ]; then
  echo -e "${BLUE}Sourcing .env file...${NC}"
  set -a
  # shellcheck disable=SC1090
  source "${PROJECT_ROOT}/.env"
  set +a
fi

# Configuration
REPO_OWNER="${REPO_OWNER:-sivert-io}"
REPO_NAME="${REPO_NAME:-cs2-server-manager}"
DISCORD_WEBHOOK_URL="${DISCORD_WEBHOOK_URL:-}"

echo -e "${GREEN}CSM - Discord Webhook${NC}"
echo "======================="
echo ""

# Determine version (NEW_VERSION without "v", TAG with "v")
TAG_RAW="${1:-}"
NEW_VERSION=""
TAG=""

if [ -n "$TAG_RAW" ]; then
  # Accept either vX.Y.Z or X.Y.Z
  TAG="$TAG_RAW"
  NEW_VERSION="${TAG_RAW#v}"
else
  VERSION_FILE="src/internal/tui/version.go"
  if [ -f "$VERSION_FILE" ]; then
    TAG=$(grep 'const[[:space:]]\+currentVersion' "$VERSION_FILE" | sed -E 's/.*"([^"]+)".*/\1/' | head -n 1)
    if [ -z "$TAG" ]; then
      echo -e "${RED}Error: could not determine currentVersion from ${VERSION_FILE}${NC}"
      exit 1
    fi
    NEW_VERSION="${TAG#v}"
  else
    echo -e "${RED}Error: ${VERSION_FILE} not found and no version provided${NC}"
    echo ""
    echo "Usage:"
    echo "  ./scripts/discord-webhook.sh vX.Y.Z"
    echo "  ./scripts/discord-webhook.sh X.Y.Z"
    echo ""
    echo "Make sure DISCORD_WEBHOOK_URL is set in .env or your environment."
    exit 1
  fi
fi

if ! [[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo -e "${RED}Invalid version format. Use semantic versioning (e.g., 0.2.0 or v0.2.0).${NC}"
  exit 1
fi

if [ -z "$DISCORD_WEBHOOK_URL" ]; then
  echo -e "${RED}Error: DISCORD_WEBHOOK_URL is not set.${NC}"
  echo "Set it in your .env file at the project root or export it in your shell:"
  echo
  echo "  export DISCORD_WEBHOOK_URL=\"https://discord.com/api/webhooks/...\""
  echo "  ./scripts/discord-webhook.sh v${NEW_VERSION}"
  exit 1
fi

echo -e "${BLUE}Version: ${GREEN}v${NEW_VERSION}${NC}"
echo -e "${BLUE}Repository: ${GREEN}${REPO_OWNER}/${REPO_NAME}${NC}"
echo ""

# Compute changelog: recent commits since previous tag
CURRENT_TAG="v${NEW_VERSION}"
PREV_TAG=$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+' | grep -vx "$CURRENT_TAG" | head -n 1 || true)

if [ -n "$PREV_TAG" ]; then
  LOG_RANGE="${PREV_TAG}..${CURRENT_TAG}"
else
  # Fallback: just show recent commits leading to the current tag/HEAD
  LOG_RANGE="${CURRENT_TAG}"
fi

CHANGELOG=$(git log --oneline --no-decorate ${LOG_RANGE} 2>/dev/null | head -20 || true)
if [ -z "$CHANGELOG" ]; then
  CHANGELOG="- Release ${CURRENT_TAG}"
else
  # Turn into bullet list
  CHANGELOG="- ${CHANGELOG//$'\n'/$'\n- '}"
fi

RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/tag/${CURRENT_TAG}"

# Build Discord payload
echo -e "${BLUE}Preparing Discord payload...${NC}"

if command -v jq >/dev/null 2>&1; then
  PAYLOAD=$(jq -n \
    --arg tag "$CURRENT_TAG" \
    --arg ver "$NEW_VERSION" \
    --arg changelog "$CHANGELOG" \
    --arg url "$RELEASE_URL" \
    '{
      content: ("🚀 **New CSM release: " + $tag + "**"),
      embeds: [{
        title: ("CS2 Server Manager " + $tag),
        description: "A new version of CSM has been released.",
        color: 3066993,
        fields: [
          {
            name: "📦 Changelog (recent commits)",
            value: $changelog,
            inline: false
          },
          {
            name: "🔗 GitHub Release",
            value: ("[View Release](" + $url + ")"),
            inline: true
          }
        ]
      }]
    }')
else
  # Fallback: minimal manual JSON construction with basic escaping.
  ESC_CHANGELOG=$(printf '%s' "$CHANGELOG" | sed 's/\\/\\\\/g; s/"/\\"/g')
  ESC_CHANGELOG=${ESC_CHANGELOG//$'\n'/'\\n'}

  PAYLOAD=$(cat <<EOF
{
  "content": "🚀 **New CSM release: v${NEW_VERSION}**",
  "embeds": [{
    "title": "CS2 Server Manager v${NEW_VERSION}",
    "description": "A new version of CSM has been released.",
    "color": 3066993,
    "fields": [
      {
        "name": "📦 Changelog (recent commits)",
        "value": "${ESC_CHANGELOG}",
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
fi

echo -e "${BLUE}Sending webhook to Discord...${NC}"

HTTP_CODE=$(curl -sS -w "%{http_code}" -o /tmp/csm_webhook_resp.txt \
  -H "Content-Type: application/json" \
  -X POST \
  -d "$PAYLOAD" \
  "$DISCORD_WEBHOOK_URL" || echo "000")

if [ "$HTTP_CODE" = "204" ] || [ "$HTTP_CODE" = "200" ]; then
  echo -e "${GREEN}Discord notification sent successfully.${NC}"
else
  echo -e "${RED}Failed to send Discord notification. HTTP status: ${HTTP_CODE}${NC}"
  echo "Response body:"
  cat /tmp/csm_webhook_resp.txt || true
  exit 1
fi

