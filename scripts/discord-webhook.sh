#!/bin/bash
set -e

# MatchZy Auto Tournament - Discord Webhook Script
# Sends a Discord webhook notification for a release

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Source .env file if it exists (from project root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
if [ -f "${PROJECT_ROOT}/.env" ]; then
    echo -e "${BLUE}Sourcing .env file...${NC}"
    # Export variables from .env, handling comments and empty lines
    set -a
    source "${PROJECT_ROOT}/.env"
    set +a
fi

# Configuration
DOCKER_USERNAME="${DOCKER_USERNAME:-sivertio}"
IMAGE_NAME="matchzy-auto-tournament"
DOCKER_IMAGE="${DOCKER_USERNAME}/${IMAGE_NAME}"
REPO_OWNER="sivert-io"
REPO_NAME="matchzy-auto-tournament"

echo -e "${GREEN}MatchZy Auto Tournament - Discord Webhook${NC}"
echo "========================================="
echo ""

# Get version from argument or package.json
if [ -n "$1" ]; then
    NEW_VERSION="$1"
    # Remove 'v' prefix if present
    NEW_VERSION="${NEW_VERSION#v}"
else
    # Get current version from package.json
    if [ -f "package.json" ]; then
        NEW_VERSION=$(grep '"version"' package.json | head -1 | awk -F '"' '{print $4}')
        echo -e "${BLUE}Using version from package.json: ${GREEN}${NEW_VERSION}${NC}"
    else
        echo -e "${RED}Error: package.json not found and no version provided${NC}"
        echo ""
        echo "Usage:"
        echo "  ./scripts/discord-webhook.sh [VERSION]"
        echo ""
        echo "Or set DISCORD_WEBHOOK_URL environment variable:"
        echo "  export DISCORD_WEBHOOK_URL=\"https://discord.com/api/webhooks/...\""
        echo "  ./scripts/discord-webhook.sh [VERSION]"
        exit 1
    fi
fi

# (rest of script unchanged) ...


