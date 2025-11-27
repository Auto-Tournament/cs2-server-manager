#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# CS2 Update Manager
# 
# Unified script for all CS2 update operations:
# - Update plugins (Metamod, CounterStrikeSharp, MatchZy, AutoUpdater)
# - Deploy plugins to running servers
# - Update CS2 game files (after Valve updates)
#
# Usage:
#   ./update.sh plugins              # Download latest plugins only
#   ./update.sh plugins-deploy       # Download + deploy to all servers
#   ./update.sh game                 # Update CS2 game files
#   ./update.sh all                  # Update everything (game + plugins)
#   ./update.sh plugins --dry-run    # Check versions without installing
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
GAME_FILES_DIR="${PROJECT_ROOT}/game_files/game"
OVERRIDES_DIR="${PROJECT_ROOT}/overrides/game"
TEMP_DIR="${PROJECT_ROOT}/.plugin_downloads"

CS2_USER="${CS2_USER:-cs2}"
CS2_APP_ID="730"

# Auto-detect number of servers
if [[ -z "${NUM_SERVERS:-}" ]]; then
  if [[ -d "/home/${CS2_USER}" ]]; then
    SERVER_COUNT=$(find /home/${CS2_USER} -maxdepth 1 -type d -name "server-*" 2>/dev/null | wc -l || echo 0)
    if [[ $SERVER_COUNT -gt 0 ]]; then
      NUM_SERVERS=$SERVER_COUNT
    else
      NUM_SERVERS=3  # Default to 3 servers
    fi
  else
    NUM_SERVERS=3  # Default to 3 servers if cs2 user doesn't exist yet
  fi
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[✓]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $*"; }
log_error() { echo -e "${RED}[✗]${NC} $*"; }

###############################################################################
# Plugin Update Functions
###############################################################################

check_dependencies() {
  local missing=()
  for cmd in curl jq unzip tar rsync; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      missing+=("$cmd")
    fi
  done
  
  if [[ ${#missing[@]} -gt 0 ]]; then
    log_error "Missing required tools: ${missing[*]}"
    log_info "Install with: sudo apt-get install curl jq unzip tar rsync"
    exit 1
  fi
}

setup_directories() {
  mkdir -p "$TEMP_DIR"
  mkdir -p "$GAME_FILES_DIR/csgo/addons"
  mkdir -p "$OVERRIDES_DIR/csgo"
}

cleanup_temp() {
  # Fix permissions before cleanup to avoid permission denied errors
  if [[ -d "$TEMP_DIR" ]]; then
    chmod -R u+rwX "$TEMP_DIR" 2>/dev/null || true
    rm -rf "$TEMP_DIR" 2>/dev/null || true
  fi
}

apply_overrides() {
  if [[ ! -d "$OVERRIDES_DIR/csgo" ]]; then
    return 0
  fi
  
  log_info "Applying custom config overrides from overrides/game/"
  rsync -a "$OVERRIDES_DIR/csgo/" "$GAME_FILES_DIR/csgo/" 2>/dev/null || true
  log_success "Overrides applied"
}

download_metamod() {
  local DRY_RUN=${1:-0}
  log_info "Fetching latest Metamod:Source dev build..."
  
  local MM_VERSION="2.0"
  local MM_BUILD=""
  
  local MM_PAGE_CONTENT
  if MM_PAGE_CONTENT=$(curl -fsSL "https://www.metamodsource.net/downloads.php?branch=dev" 2>&1); then
    MM_BUILD=$(echo "$MM_PAGE_CONTENT" | grep -oP 'Latest downloads for version.*?build \K[0-9]+' | head -n1)
  else
    log_warn "Could not fetch Metamod:Source website (network issue?)"
  fi
  
  if [[ -z "$MM_BUILD" ]]; then
    log_warn "Could not auto-detect latest build, using fallback build 1374"
    MM_BUILD="1374"
  fi
  
  local MM_URL="https://mms.alliedmods.net/mmsdrop/${MM_VERSION}/mmsource-${MM_VERSION}.0-git${MM_BUILD}-linux.tar.gz"
  
  log_info "Target: Metamod:Source ${MM_VERSION} build ${MM_BUILD}"
  
  if (( DRY_RUN == 1 )); then
    log_info "[DRY-RUN] Would download from: $MM_URL"
    return 0
  fi
  
  log_info "Downloading Metamod:Source ${MM_VERSION} build ${MM_BUILD}..."
  
  local curl_output
  if curl_output=$(curl -fSL -o "${TEMP_DIR}/metamod.tar.gz" "$MM_URL" 2>&1); then
    log_success "Downloaded Metamod:Source"
    log_info "Extracting Metamod:Source to ${GAME_FILES_DIR}/csgo/"
    if tar -xzf "${TEMP_DIR}/metamod.tar.gz" -C "${GAME_FILES_DIR}/csgo/" 2>&1; then
      log_success "✓ Metamod:Source installed"
    else
      log_error "Failed to extract Metamod:Source archive"
      log_error "Reason: Archive may be corrupted or incomplete"
      return 1
    fi
  else
    log_error "Failed to download Metamod:Source from ${MM_URL}"
    if [[ "$curl_output" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$curl_output" =~ "404" ]]; then
      log_error "Reason: File not found (build ${MM_BUILD} may not exist)"
    else
      log_error "Reason: ${curl_output}"
    fi
    return 1
  fi
}

download_counterstrikesharp() {
  local DRY_RUN=${1:-0}
  log_info "Fetching latest CounterStrikeSharp release..."
  
  local API_URL="https://api.github.com/repos/roflmuffin/CounterStrikeSharp/releases/latest"
  local RELEASE_INFO
  local api_error
  
  if ! RELEASE_INFO=$(curl -fsSL "$API_URL" 2>&1); then
    log_error "Failed to fetch CounterStrikeSharp release info from GitHub"
    if [[ "$RELEASE_INFO" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$RELEASE_INFO" =~ "rate limit" ]]; then
      log_error "Reason: GitHub API rate limit exceeded - try again later"
    else
      log_error "Reason: ${RELEASE_INFO}"
    fi
    return 1
  fi
  
  local VERSION=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
  
  local DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | contains("with-runtime-linux")) | .browser_download_url' | head -n1)
  
  if [[ -z "$DOWNLOAD_URL" || "$DOWNLOAD_URL" == "null" ]]; then
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | contains("linux")) | .browser_download_url' | head -n1)
  fi
  
  if [[ -z "$DOWNLOAD_URL" || "$DOWNLOAD_URL" == "null" ]]; then
    log_error "Could not find Linux download for CounterStrikeSharp"
    log_error "Reason: No release assets found - GitHub API may have changed"
    return 1
  fi
  
  log_info "Target: CounterStrikeSharp $VERSION (with-runtime)"
  
  if (( DRY_RUN == 1 )); then
    log_info "[DRY-RUN] Would download from: $DOWNLOAD_URL"
    return 0
  fi
  
  log_info "Downloading CounterStrikeSharp $VERSION..."
  
  local curl_output
  if curl_output=$(curl -fSL -o "${TEMP_DIR}/counterstrikesharp.zip" "$DOWNLOAD_URL" 2>&1); then
    log_success "Downloaded CounterStrikeSharp $VERSION"
    log_info "Extracting CounterStrikeSharp to ${GAME_FILES_DIR}/csgo/"
    if unzip -o "${TEMP_DIR}/counterstrikesharp.zip" -d "${GAME_FILES_DIR}/csgo/" >/dev/null 2>&1; then
      log_success "✓ CounterStrikeSharp installed"
    else
      log_error "Failed to extract CounterStrikeSharp archive"
      log_error "Reason: Archive may be corrupted or zip not installed"
      return 1
    fi
  else
    log_error "Failed to download CounterStrikeSharp from GitHub"
    if [[ "$curl_output" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$curl_output" =~ "404" ]]; then
      log_error "Reason: File not found - release may have been removed"
    else
      log_error "Reason: ${curl_output}"
    fi
    return 1
  fi
}

download_matchzy() {
  local DRY_RUN=${1:-0}
  log_info "Fetching latest MatchZy release (Enhanced Fork)..."
  
  # Use enhanced fork with additional events for tournament automation
  # Falls back to official release if enhanced fork is unavailable
  local API_URL="https://api.github.com/repos/sivert-io/MatchZy/releases/latest"
  local FALLBACK_URL="https://api.github.com/repos/shobhit-pathak/MatchZy/releases/latest"
  local RELEASE_INFO
  
  # Try enhanced fork first
  if ! RELEASE_INFO=$(curl -fsSL "$API_URL" 2>&1); then
    log_warn "Enhanced fork not available, trying official release..."
    if ! RELEASE_INFO=$(curl -fsSL "$FALLBACK_URL" 2>&1); then
      log_error "Failed to fetch MatchZy release info from GitHub"
      if [[ "$RELEASE_INFO" =~ "Could not resolve host" ]]; then
        log_error "Reason: Network/DNS issue - check your internet connection"
      elif [[ "$RELEASE_INFO" =~ "rate limit" ]]; then
        log_error "Reason: GitHub API rate limit exceeded - try again later"
      else
        log_error "Reason: ${RELEASE_INFO}"
      fi
      return 1
    fi
  fi
  
  local VERSION=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
  local REPO_NAME=$(echo "$RELEASE_INFO" | jq -r '.html_url' | grep -q "sivert-io" && echo "(Enhanced Fork)" || echo "(Official)")
  
  local DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | contains("MatchZy") and (contains("with") | not)) | .browser_download_url' | head -n1)
  
  if [[ -z "$DOWNLOAD_URL" || "$DOWNLOAD_URL" == "null" ]]; then
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | endswith(".zip")) | .browser_download_url' | head -n1)
  fi
  
  if [[ -z "$DOWNLOAD_URL" || "$DOWNLOAD_URL" == "null" ]]; then
    log_error "Could not find download for MatchZy"
    log_error "Reason: No release assets found - GitHub API may have changed"
    return 1
  fi
  
  log_info "Target: MatchZy $VERSION $REPO_NAME"
  
  if (( DRY_RUN == 1 )); then
    log_info "[DRY-RUN] Would download from: $DOWNLOAD_URL"
    return 0
  fi
  
  log_info "Downloading MatchZy $VERSION..."
  
  local curl_output
  if curl_output=$(curl -fSL -o "${TEMP_DIR}/matchzy.zip" "$DOWNLOAD_URL" 2>&1); then
    log_success "Downloaded MatchZy $VERSION"
    log_info "Extracting MatchZy..."
    # Clean up any existing extract directory
    chmod -R u+rwX "${TEMP_DIR}/matchzy_extract" 2>/dev/null || true
    rm -rf "${TEMP_DIR}/matchzy_extract" 2>/dev/null || true
    mkdir -p "${TEMP_DIR}/matchzy_extract"
    
    local unzip_output=""
    local unzip_status=0
    if ! unzip_output=$(unzip -o "${TEMP_DIR}/matchzy.zip" -d "${TEMP_DIR}/matchzy_extract" 2>&1); then
      unzip_status=$?
    fi

    # Fix permissions on extracted files (Windows zips often have bad permissions)
    chmod -R u+rwX "${TEMP_DIR}/matchzy_extract" 2>/dev/null || true

    if [[ $unzip_status -gt 0 ]]; then
      if [[ $unzip_status -eq 1 && -d "${TEMP_DIR}/matchzy_extract" ]]; then
        local unzip_warning
        unzip_warning=$(echo "$unzip_output" | head -n 2 | tr -d '\r')
        log_warn "Unzip reported warnings (likely Windows path separators). Continuing..."
        log_warn "$unzip_warning"
      else
        log_error "Failed to extract MatchZy archive"
        if [[ -n "$unzip_output" ]]; then
          log_error "Reason: ${unzip_output}"
        else
          log_error "Reason: Archive may be corrupted or zip not installed"
        fi
        return 1
      fi
    fi

    # Try multiple archive structure patterns
    local matchzy_root=""
    
    # Pattern 1: Old structure with MatchZy* root folder
    matchzy_root=$(find "${TEMP_DIR}/matchzy_extract" -maxdepth 1 -type d -name "MatchZy*" 2>/dev/null | head -n1)
    
    # Pattern 2: New structure - extracts directly to addons/counterstrikesharp/
    if [[ -z "$matchzy_root" ]]; then
      if [[ -d "${TEMP_DIR}/matchzy_extract/addons/counterstrikesharp" ]]; then
        matchzy_root="${TEMP_DIR}/matchzy_extract"
      fi
    fi
    
    # Pattern 3: Check if extract directory itself contains addons structure
    if [[ -z "$matchzy_root" ]]; then
      if [[ -d "${TEMP_DIR}/matchzy_extract/addons" ]]; then
        matchzy_root="${TEMP_DIR}/matchzy_extract"
      fi
    fi
    
    if [[ -n "$matchzy_root" && -d "$matchzy_root" ]]; then
      # Fix permissions before syncing
      chmod -R u+rwX,go+rX "$matchzy_root" 2>/dev/null || true
      
      if rsync -a "$matchzy_root/" "${GAME_FILES_DIR}/csgo/" 2>&1; then
        log_success "✓ MatchZy installed (plugins, configs, and all assets merged)"
      else
        log_error "Failed to merge MatchZy files"
        log_error "Reason: Permission issue or disk full"
        return 1
      fi
    else
      log_error "Failed to find MatchZy root folder in package"
      log_error "Reason: Archive structure may have changed"
      log_info "Debug: Contents of extract directory:"
      ls -la "${TEMP_DIR}/matchzy_extract" 2>/dev/null | head -10 || true
      return 1
    fi
  else
    log_error "Failed to download MatchZy from GitHub"
    if [[ "$curl_output" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$curl_output" =~ "404" ]]; then
      log_error "Reason: File not found - release may have been removed"
    else
      log_error "Reason: ${curl_output}"
    fi
    return 1
  fi
}

download_cs2autoupdater() {
  local DRY_RUN=${1:-0}
  log_info "Fetching latest CS2-AutoUpdater release..."
  
  local API_URL="https://api.github.com/repos/dran1x/CS2-AutoUpdater/releases/latest"
  local RELEASE_INFO
  
  if ! RELEASE_INFO=$(curl -fsSL "$API_URL" 2>&1); then
    log_error "Failed to fetch CS2-AutoUpdater release info from GitHub"
    if [[ "$RELEASE_INFO" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$RELEASE_INFO" =~ "rate limit" ]]; then
      log_error "Reason: GitHub API rate limit exceeded - try again later"
    else
      log_error "Reason: ${RELEASE_INFO}"
    fi
    return 1
  fi
  
  local VERSION=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
  
  local DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | endswith(".zip")) | .browser_download_url' | head -n1)
  
  if [[ -z "$DOWNLOAD_URL" || "$DOWNLOAD_URL" == "null" ]]; then
    log_error "Could not find download for CS2-AutoUpdater"
    log_error "Reason: No release assets found - GitHub API may have changed"
    return 1
  fi
  
  log_info "Target: CS2-AutoUpdater $VERSION"
  
  if (( DRY_RUN == 1 )); then
    log_info "[DRY-RUN] Would download from: $DOWNLOAD_URL"
    return 0
  fi
  
  log_info "Downloading CS2-AutoUpdater $VERSION..."
  
  local curl_output
  if curl_output=$(curl -fSL -o "${TEMP_DIR}/cs2autoupdater.zip" "$DOWNLOAD_URL" 2>&1); then
    log_success "Downloaded CS2-AutoUpdater $VERSION"
    log_info "Extracting CS2-AutoUpdater..."
    if unzip -o "${TEMP_DIR}/cs2autoupdater.zip" -d "${TEMP_DIR}/autoupdater_extract" >/dev/null 2>&1; then
      if [[ -d "${TEMP_DIR}/autoupdater_extract/plugins" ]]; then
        mkdir -p "${GAME_FILES_DIR}/csgo/addons/counterstrikesharp/plugins"
        if rsync -a "${TEMP_DIR}/autoupdater_extract/plugins/" "${GAME_FILES_DIR}/csgo/addons/counterstrikesharp/plugins/" 2>&1; then
          log_success "✓ CS2-AutoUpdater installed"
        else
          log_error "Failed to merge CS2-AutoUpdater files"
          log_error "Reason: Permission issue or disk full"
          return 1
        fi
      else
        log_error "Failed to find plugins folder in CS2-AutoUpdater package"
        log_error "Reason: Archive structure may have changed"
        return 1
      fi
    else
      log_error "Failed to extract CS2-AutoUpdater archive"
      log_error "Reason: Archive may be corrupted or zip not installed"
      return 1
    fi
  else
    log_error "Failed to download CS2-AutoUpdater from GitHub"
    if [[ "$curl_output" =~ "Could not resolve host" ]]; then
      log_error "Reason: Network/DNS issue - check your internet connection"
    elif [[ "$curl_output" =~ "404" ]]; then
      log_error "Reason: File not found - release may have been removed"
    else
      log_error "Reason: ${curl_output}"
    fi
    return 1
  fi
}

###############################################################################
# Command: plugins
###############################################################################
cmd_plugins() {
  local DRY_RUN=0
  if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=1
  fi
  
  echo "============================================"
  echo "  CS2 Plugin Updater"
  if (( DRY_RUN == 1 )); then
    echo "  [DRY-RUN MODE - No files will be modified]"
  fi
  echo "============================================"
  echo
  
  check_dependencies
  
  if (( DRY_RUN == 0 )); then
    setup_directories
  fi
  
  echo
  echo "Downloaded plugins go to: $GAME_FILES_DIR"
  echo "Custom configs go to:     $OVERRIDES_DIR"
  echo
  
  local failed=()
  
  download_metamod $DRY_RUN || failed+=("Metamod:Source")
  echo
  
  download_counterstrikesharp $DRY_RUN || failed+=("CounterStrikeSharp")
  echo
  
  download_matchzy $DRY_RUN || failed+=("MatchZy")
  echo
  
  download_cs2autoupdater $DRY_RUN || failed+=("CS2-AutoUpdater")
  echo
  
  if (( DRY_RUN == 0 )) && [[ ${#failed[@]} -eq 0 ]]; then
    apply_overrides
    echo
  fi
  
  if (( DRY_RUN == 0 )); then
    cleanup_temp
  fi
  
  echo "============================================"
  if [[ ${#failed[@]} -eq 0 ]]; then
    if (( DRY_RUN == 1 )); then
      log_success "Dry-run complete - all plugins available!"
      echo
      echo "Run without --dry-run to actually download and install."
    else
      log_success "All plugins updated successfully!"
      echo
      echo "Installation summary:"
      echo "  • Metamod:Source     → game_files/game/csgo/addons/metamod/"
      echo "  • CounterStrikeSharp → game_files/game/csgo/addons/counterstrikesharp/"
      echo "  • MatchZy            → game_files/game/csgo/addons/counterstrikesharp/plugins/MatchZy/"
      echo "  • CS2-AutoUpdater    → game_files/game/csgo/addons/counterstrikesharp/plugins/AutoUpdater/"
      echo "  • Custom overrides   → Applied from overrides/game/"
    fi
    echo "============================================"
    return 0
  else
    log_error "Some plugins failed: ${failed[*]}"
    echo "============================================"
    return 1
  fi
}

###############################################################################
# Command: plugins-deploy
###############################################################################
cmd_plugins_deploy() {
  # Check if running as root
  if [[ $EUID -ne 0 ]]; then 
    log_error "This command must be run as root (use sudo)"
    exit 1
  fi
  
  echo "============================================"
  echo "  Update Plugins on All Servers"
  echo "============================================"
  echo "This will:"
  echo "  1. Download latest plugins"
  echo "  2. Stop all CS2 servers"
  echo "  3. Update plugins in each server"
  echo "  4. Restart servers"
  echo
  read -p "Continue? (y/N): " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
  fi
  
  # Step 1: Download latest plugins
  echo
  echo "[1/4] Downloading latest plugins..."
  echo "----------------------------------------"
  sudo -u "$SUDO_USER" bash "$0" plugins
  
  # Check if download was successful
  if [[ ! -d "${GAME_FILES_DIR}/csgo/addons/metamod" ]]; then
    log_error "Plugin download failed or incomplete"
    exit 1
  fi
  
  # Step 2: Stop all servers
  echo
  echo "[2/4] Stopping all CS2 servers..."
  echo "----------------------------------------"
  for ((i=1; i<=NUM_SERVERS; i++)); do
    session_name="cs2-${i}"
    if su - "$CS2_USER" -c "tmux has-session -t ${session_name} 2>/dev/null"; then
      echo "  Stopping cs2-${i}..."
      su - "$CS2_USER" -c "tmux send-keys -t ${session_name} 'quit' C-m" || true
      sleep 1
      su - "$CS2_USER" -c "tmux kill-session -t ${session_name} 2>/dev/null" || true
    else
      echo "  cs2-${i} not running, skipping"
    fi
  done
  echo "  All servers stopped"
  
  # Step 3: Overlay plugins to each server
  echo
  echo "[3/4] Updating plugins in server instances..."
  echo "----------------------------------------"
  for ((i=1; i<=NUM_SERVERS; i++)); do
    SERVER_DIR="/home/${CS2_USER}/server-${i}/game/csgo"
    
    if [[ ! -d "$SERVER_DIR" ]]; then
      echo "  Warning: server-${i} not found at ${SERVER_DIR}, skipping"
      continue
    fi
    
    echo "  Updating server-${i}..."
    
    # Backup existing addons
    if [[ -d "${SERVER_DIR}/addons" ]]; then
      echo "    Creating backup of existing addons..."
      cp -a "${SERVER_DIR}/addons" "${SERVER_DIR}/addons.backup.$(date +%Y%m%d_%H%M%S)" || true
    fi
    
    # Sync plugins from source to server
    echo "    Syncing Metamod..."
    rsync -a --delete \
          "${GAME_FILES_DIR}/csgo/addons/metamod/" \
          "${SERVER_DIR}/addons/metamod/" 2>/dev/null || echo "    Warning: Metamod sync had issues"
    
    echo "    Syncing CounterStrikeSharp..."
    rsync -a --delete \
          --exclude 'configs/' \
          --exclude 'data/' \
          --exclude 'logs/' \
          "${GAME_FILES_DIR}/csgo/addons/counterstrikesharp/" \
          "${SERVER_DIR}/addons/counterstrikesharp/" 2>/dev/null || echo "    Warning: CSS sync had issues"
    
    chown -R "${CS2_USER}:${CS2_USER}" "${SERVER_DIR}/addons"
    
    echo "    ✓ server-${i} updated"
  done
  
  # Step 4: Restart all servers
  echo
  echo "[4/4] Restarting all CS2 servers..."
  echo "----------------------------------------"
  echo "Using tmux to start servers..."
  echo
  
  "${SCRIPT_DIR}/cs2_tmux.sh" start
  
  sleep 3
  
  echo
  echo "============================================"
  echo "  Update Complete!"
  echo "============================================"
  echo
  echo "Plugins updated successfully:"
  echo "  • Metamod:Source"
  echo "  • CounterStrikeSharp"
  echo "  • MatchZy"
  echo "  • CS2-AutoUpdater"
  echo
  echo "Check server status:"
  echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh status"
  echo
  echo "Rollback if needed:"
  echo "  Backups saved to: /home/${CS2_USER}/server-X/game/csgo/addons.backup.*"
  echo
}

###############################################################################
# Command: game
###############################################################################
cmd_game() {
  # Check if running as root
  if [[ $EUID -ne 0 ]]; then 
    log_error "This command must be run as root (use sudo)"
    exit 1
  fi
  
  # Check if cs2 user exists
  if ! id -u "$CS2_USER" >/dev/null 2>&1; then
    log_error "CS2 user '$CS2_USER' does not exist"
    log_info "Run ./manage.sh and choose Option 1 (Install servers) first"
    exit 1
  fi
  
  # Check if master installation exists
  MASTER_DIR="/home/${CS2_USER}/master-install"
  if [[ ! -d "$MASTER_DIR/game/csgo" ]]; then
    log_error "Master installation not found at ${MASTER_DIR}"
    exit 1
  fi
  
  echo "============================================"
  echo "  CS2 Game Server Update"
  echo "============================================"
  echo "This will:"
  echo "  1. Update master CS2 installation (SteamCMD)"
  echo "  2. Stop all running CS2 servers"
  echo "  3. Update game files on each server"
  echo "  4. Restart all servers"
  echo
  log_warn "This updates CS2 game files only"
  log_info "Your configs and plugins will be preserved"
  echo
  read -p "Continue? (y/N): " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
  fi
  
  # Step 1: Update master installation via SteamCMD
  echo
  echo "============================================"
  echo "[1/4] Updating Master CS2 Installation"
  echo "============================================"
  log_info "This may take several minutes..."
  echo
  
  su - "$CS2_USER" -c "
    set -e
    steamcmd +force_install_dir \"${MASTER_DIR}\" \
             +login anonymous \
             +app_update ${CS2_APP_ID} validate \
             +quit
  "
  
  if [[ -f "${MASTER_DIR}/game/csgo/gameinfo.gi" ]]; then
    log_success "Master installation updated successfully"
  else
    log_error "Master installation update failed"
    exit 1
  fi
  
  # Step 2: Stop all servers
  echo
  echo "============================================"
  echo "[2/4] Stopping All CS2 Servers"
  echo "============================================"
  echo
  
  for ((i=1; i<=NUM_SERVERS; i++)); do
    session_name="cs2-${i}"
    if su - "$CS2_USER" -c "tmux has-session -t ${session_name} 2>/dev/null"; then
      log_info "Stopping server ${i}..."
      su - "$CS2_USER" -c "tmux send-keys -t ${session_name} 'quit' C-m" || true
      sleep 2
      su - "$CS2_USER" -c "tmux kill-session -t ${session_name} 2>/dev/null" || true
      log_success "Server ${i} stopped"
    else
      log_info "Server ${i} not running, skipping"
    fi
  done
  
  log_success "All servers stopped"
  
  # Step 3: Update each server instance
  echo
  echo "============================================"
  echo "[3/4] Updating Server Instances"
  echo "============================================"
  echo
  
  for ((i=1; i<=NUM_SERVERS; i++)); do
    SERVER_DIR="/home/${CS2_USER}/server-${i}"
    
    if [[ ! -d "$SERVER_DIR" ]]; then
      log_warn "Server ${i} not found at ${SERVER_DIR}, skipping"
      continue
    fi
    
    log_info "Updating server ${i}..."
    
    BACKUP_TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    
    log_info "  Creating backup of configs and plugins..."
    
    if [[ -f "${SERVER_DIR}/game/csgo/gameinfo.gi" ]]; then
      cp "${SERVER_DIR}/game/csgo/gameinfo.gi" \
         "${SERVER_DIR}/game/csgo/gameinfo.gi.backup.${BACKUP_TIMESTAMP}"
    fi
    
    if [[ -d "${SERVER_DIR}/game/csgo/cfg" ]]; then
      cp -a "${SERVER_DIR}/game/csgo/cfg" \
         "${SERVER_DIR}/game/csgo/cfg.backup.${BACKUP_TIMESTAMP}"
    fi
    
    if [[ -d "${SERVER_DIR}/game/csgo/addons" ]]; then
      cp -a "${SERVER_DIR}/game/csgo/addons" \
         "${SERVER_DIR}/game/csgo/addons.backup.${BACKUP_TIMESTAMP}"
    fi
    
    log_success "  Backups created"
    
    log_info "  Syncing game files from master..."
    
    rsync -a --info=PROGRESS2 \
          --exclude 'csgo/cfg/' \
          --exclude 'csgo/addons/' \
          --exclude 'csgo/gameinfo.gi' \
          --exclude 'csgo/*.log' \
          --exclude 'csgo/logs/' \
          "${MASTER_DIR}/" "${SERVER_DIR}/" || {
      log_error "  Failed to update server ${i}"
      log_warn "  Restoring from backup..."
      if [[ -f "${SERVER_DIR}/game/csgo/gameinfo.gi.backup.${BACKUP_TIMESTAMP}" ]]; then
        cp "${SERVER_DIR}/game/csgo/gameinfo.gi.backup.${BACKUP_TIMESTAMP}" \
           "${SERVER_DIR}/game/csgo/gameinfo.gi"
      fi
      continue
    }
    
    chown -R "${CS2_USER}:${CS2_USER}" "$SERVER_DIR"
    
    log_success "Server ${i} updated successfully"
    echo
  done
  
  log_success "All servers updated"
  
  # Step 4: Restart all servers
  echo
  echo "============================================"
  echo "[4/4] Restarting All CS2 Servers"
  echo "============================================"
  echo
  
  log_info "Starting servers using tmux..."
  "${SCRIPT_DIR}/cs2_tmux.sh" start
  
  sleep 3
  
  echo
  echo "============================================"
  echo "  Update Complete!"
  echo "============================================"
  echo
  log_success "CS2 servers have been updated to the latest version"
  echo
  echo "Check server status:"
  echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh status"
  echo
  echo "Backups saved to:"
  echo "  /home/${CS2_USER}/server-X/game/csgo/*.backup.*"
  echo
  log_info "If you encounter issues, you can restore from backups"
  echo
}

###############################################################################
# Command: all
###############################################################################
cmd_all() {
  if [[ $EUID -ne 0 ]]; then 
    log_error "This command must be run as root (use sudo)"
    exit 1
  fi
  
  echo "============================================"
  echo "  Update Everything"
  echo "============================================"
  echo "This will:"
  echo "  1. Update CS2 game files"
  echo "  2. Download latest plugins"
  echo "  3. Deploy plugins to all servers"
  echo
  read -p "Continue? (y/N): " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
  fi
  
  log_info "Step 1: Updating CS2 game files..."
  cmd_game
  
  echo
  log_info "Step 2: Updating plugins..."
  cmd_plugins_deploy
  
  echo
  log_success "Everything updated!"
}

###############################################################################
# Usage
###############################################################################
usage() {
  cat << EOF
CS2 Update Manager - Unified update script

Usage:
  $0 plugins              Download latest plugins only
  $0 plugins --dry-run    Check plugin versions without downloading
  $0 plugins-deploy       Download plugins + deploy to all servers (requires sudo)
  $0 game                 Update CS2 game files (requires sudo)
  $0 all                  Update everything - game + plugins (requires sudo)

Commands:
  plugins         - Download Metamod, CounterStrikeSharp, MatchZy, AutoUpdater
                    Extracts to game_files/game/csgo/addons/
                    Does NOT deploy to servers
  
  plugins-deploy  - Downloads plugins + stops servers + updates all servers + restarts
                    Use this to update plugins on running servers
                    Requires sudo
  
  game           - Updates CS2 game files via SteamCMD
                   Stops servers + updates game files + restarts
                   Preserves configs and plugins
                   Requires sudo
  
  all            - Updates CS2 game files + plugins on all servers
                   Complete update of everything
                   Requires sudo

Examples:
  ./update.sh plugins              # Just download latest plugins
  sudo ./update.sh plugins-deploy  # Update plugins on all servers
  sudo ./update.sh game            # Update CS2 after Valve update
  sudo ./update.sh all             # Update everything

Files:
  Downloaded plugins: ${GAME_FILES_DIR}
  Custom configs:     ${OVERRIDES_DIR}

EOF
}

###############################################################################
# Main
###############################################################################
trap 'cleanup_temp' EXIT INT TERM

case "${1:-}" in
  plugins)
    shift
    cmd_plugins "$@"
    exit $?
    ;;
  plugins-deploy)
    cmd_plugins_deploy
    exit $?
    ;;
  game)
    cmd_game
    exit $?
    ;;
  all)
    cmd_all
    exit $?
    ;;
  *)
    usage
    exit 1
    ;;
esac

exit 0
