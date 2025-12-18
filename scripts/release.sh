#!/usr/bin/env bash

set -euo pipefail

# Always run from the repository root
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.."

MODE="${1:-}"
TAG=""
CSM_DRY_RUN="${CSM_DRY_RUN:-false}"
CSM_WEBHOOK_DRY_RUN="${CSM_WEBHOOK_DRY_RUN:-false}"

VERSION_FILE="src/internal/tui/version.go"

confirm_release() {
  local tag="$1"

  # In non-interactive environments (e.g. CI), skip the prompt to preserve
  # existing automation behaviour.
  if [[ ! -t 0 ]]; then
    echo "[csm] Non-interactive shell; skipping confirmation for ${tag}."
    return 0
  fi

  echo
  read -r -p "[csm] About to build and publish release ${tag}. Continue? [y/N]: " answer
  case "$answer" in
    y|Y|yes|YES)
      ;;
    *)
      echo "[csm] Release cancelled."
      exit 0
      ;;
  esac
}

git_commit_and_tag() {
  local tag="$1"
  local version_file="$2"

  if [[ "${CSM_SKIP_GIT:-false}" == "true" ]]; then
    echo "[csm] Skipping git commit/tag (CSM_SKIP_GIT=true)."
    return 0
  fi

  if ! command -v git >/dev/null 2>&1; then
    echo "[csm] git not found; skipping commit/tag."
    return 0
  fi

  # Stage the version file.
  if ! git add "$version_file"; then
    echo "[csm] git add failed for ${version_file}; aborting release."
    return 1
  fi

  # Only commit if there is something staged.
  if git diff --cached --quiet; then
    echo "[csm] No changes to commit in ${version_file}; skipping commit."
  else
    if ! git commit -m "chore: release ${tag}"; then
      echo "[csm] git commit failed; aborting release."
      return 1
    fi
    echo "[csm] Committed version bump for ${tag}."
  fi

  # Try to create an annotated tag; if it already exists, continue.
  if git tag -a "${tag}" -m "CSM ${tag}" 2>/dev/null; then
    echo "[csm] Created git tag ${tag}."
  else
    echo "[csm] git tag ${tag} failed (maybe it already exists); continuing."
  fi

  # Push the commit and tag to the default remote (origin) unless disabled.
  if [[ "${CSM_SKIP_GIT_PUSH:-false}" == "true" ]]; then
    echo "[csm] Skipping git push (CSM_SKIP_GIT_PUSH=true)."
    return 0
  fi

  if git rev-parse --git-dir >/dev/null 2>&1; then
    echo "[csm] Pushing commit and tag ${tag} to origin..."
    # Push the current branch and the tag; ignore errors but warn.
    if ! git push origin HEAD --follow-tags; then
      echo "[csm] Warning: git push origin HEAD --follow-tags failed."
    fi
  fi
}

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

  # Ask for confirmation before modifying version.go, tagging and building.
  confirm_release "${TAG}"

  # Update currentVersion in version.go to match the new tag.
  # Use a portable in-place edit (creates a .bak on macOS/BSD).
  # We anchor on the const line to avoid touching comments.
  sed -i.bak -E 's/^(const[[:space:]]+currentVersion[[:space:]]*=[[:space:]]*")([^"]+)(")/\1'"${TAG}"'\3/' "$VERSION_FILE"
  rm -f "${VERSION_FILE}.bak"

  # Commit and tag the version bump.
  if ! git_commit_and_tag "${TAG}" "${VERSION_FILE}"; then
    exit 1
  fi

elif [[ "$MODE" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  # Explicit version provided, e.g. v0.2.0 or 0.2.0
  BASE="${MODE#v}"
  TAG="v${BASE}"

  echo "[csm] Using explicit version: ${TAG}"

  # Ask for confirmation before modifying version.go, tagging and building.
  confirm_release "${TAG}"

  # Update currentVersion in version.go to match the requested tag so the
  # consistency check passes and the binary reports the correct version.
  if [[ -f "$VERSION_FILE" ]]; then
    sed -i.bak -E 's/^(const[[:space:]]+currentVersion[[:space:]]*=[[:space:]]*")([^"]+)(")/\1'"${TAG}"'\3/' "$VERSION_FILE"
    rm -f "${VERSION_FILE}.bak"
  else
    echo "Warning: $VERSION_FILE not found; skipping version.go update."
  fi

  # Commit and tag the version bump.
  if ! git_commit_and_tag "${TAG}" "${VERSION_FILE}"; then
    exit 1
  fi
else
  echo "Usage:"
  echo "  ./scripts/release.sh vX.Y.Z        # create release for explicit tag"
  echo "  ./scripts/release.sh X.Y.Z         # create release for explicit tag"
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

# Optional Discord webhook notification via scripts/discord-webhook.sh.
# Configure DISCORD_WEBHOOK_URL in .env at the project root.
if [[ "$CSM_DRY_RUN" != "true" && "${CSM_SKIP_DISCORD:-false}" != "true" ]]; then
  if [[ -f "./scripts/discord-webhook.sh" ]]; then
    echo "[csm] Sending Discord notification for ${TAG}..."
    if ! bash ./scripts/discord-webhook.sh "${TAG}"; then
      echo "[csm] Warning: Discord webhook script failed."
    fi
  else
    echo "[csm] Skipping Discord webhook: scripts/discord-webhook.sh not found."
  fi
fi
