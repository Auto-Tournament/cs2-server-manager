#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# CS2 Server Manager - Quick Installer
#
# Usage:
#   wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
#   bash install.sh
#
# With options:
#   bash install.sh --auto --servers 5
###############################################################################

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[✓]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $*"; }
log_error() { echo -e "${RED}[✗]${NC} $*"; }

###############################################################################
# Configuration
###############################################################################

# GitHub repo settings
REPO_URL="https://github.com/sivert-io/cs2-server-manager.git"
REPO_BRANCH="master"
INSTALL_DIR="$HOME/cs2-server-manager"

# Default settings
AUTO_INSTALL=0
NUM_SERVERS=3
SKIP_DEPS=0
CUSTOM_OVERRIDES=""

###############################################################################
# Parse arguments
###############################################################################
parse_args() {
  while [[ $# -gt 0 ]]; do
    case $1 in
      --auto)
        AUTO_INSTALL=1
        shift
        ;;
      --servers)
        NUM_SERVERS="$2"
        shift 2
        ;;
      --skip-deps)
        SKIP_DEPS=1
        shift
        ;;
      --dir)
        INSTALL_DIR="$2"
        shift 2
        ;;
      --overrides)
        CUSTOM_OVERRIDES="$2"
        shift 2
        ;;
      --help|-h)
        show_help
        exit 0
        ;;
      *)
        log_error "Unknown option: $1"
        echo "Run with --help for usage"
        exit 1
        ;;
    esac
  done
}

show_help() {
  cat << EOF
CS2 Server Manager - Quick Installer

Usage:
  wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
  bash install.sh [OPTIONS]

Options:
  --auto          Run installation without prompts (uses defaults)
  --servers N     Number of servers to install (default: 3)
  --skip-deps     Skip dependency installation check
  --dir PATH      Installation directory (default: ~/cs2-server-manager)
  --overrides PATH Custom overrides directory (default: uses repo's overrides/)
  --help, -h      Show this help message

Examples:
  # Download and run interactively
  wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
  bash install.sh

  # Auto-install with 5 servers
  bash install.sh --auto --servers 5

  # Install to custom directory
  bash install.sh --auto --dir /opt/cs2
  
  # Use custom overrides directory
  bash install.sh --auto --overrides /path/to/my-overrides
  
  # Or: git clone, customize overrides/, then run manage.sh
  git clone https://github.com/sivert-io/cs2-server-manager.git
  cd cs2-server-manager
  # Edit overrides/ folder as needed
  ./manage.sh install

EOF
}

###############################################################################
# Check if running as root
###############################################################################
check_root() {
  if [[ $EUID -eq 0 ]]; then
    log_error "This script should NOT be run as root"
    log_info "It will use sudo when needed"
    exit 1
  fi
}

###############################################################################
# Check prerequisites
###############################################################################
check_prerequisites() {
  log_info "Checking prerequisites..."
  
  local missing=()
  
  # Essential tools
  for cmd in git curl sudo; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      missing+=("$cmd")
    fi
  done
  
  if [[ ${#missing[@]} -gt 0 ]]; then
    log_error "Missing required tools: ${missing[*]}"
    log_info "Install with: sudo apt-get update && sudo apt-get install -y ${missing[*]}"
    exit 1
  fi
  
  log_success "Prerequisites check passed"
}

###############################################################################
# Check and install dependencies
###############################################################################
check_dependencies() {
  if [[ $SKIP_DEPS -eq 1 ]]; then
    log_info "Skipping dependency check (--skip-deps)"
    return 0
  fi
  
  log_info "Checking system dependencies..."
  
  local missing=()
  local deps=(lib32gcc-s1 lib32stdc++6 steamcmd tmux curl jq unzip tar rsync)
  
  for dep in "${deps[@]}"; do
    if ! dpkg -l | grep -q "^ii  $dep"; then
      missing+=("$dep")
    fi
  done
  
  if [[ ${#missing[@]} -gt 0 ]]; then
    log_warn "Missing dependencies: ${missing[*]}"
    echo
    if [[ $AUTO_INSTALL -eq 1 ]]; then
      log_info "Auto-installing dependencies..."
      # Allow apt-get update to fail (some repos might have transient issues)
      sudo apt-get update || log_warn "Some apt repositories had issues, but continuing..."
      sudo apt-get install -y "${missing[@]}" || {
        log_error "Failed to install dependencies"
        log_info "Try running manually: sudo apt-get install -y ${missing[*]}"
        exit 1
      }
    else
      echo -n "Install missing dependencies now? (Y/n): "
      read -r response
      if [[ ! "$response" =~ ^[Nn]$ ]]; then
        # Allow apt-get update to fail (some repos might have transient issues)
        sudo apt-get update || log_warn "Some apt repositories had issues, but continuing..."
        sudo apt-get install -y "${missing[@]}" || {
          log_error "Failed to install dependencies"
          log_info "Try running manually: sudo apt-get install -y ${missing[*]}"
          exit 1
        }
      else
        log_error "Cannot continue without dependencies"
        exit 1
      fi
    fi
  else
    log_success "All dependencies installed"
  fi
}

###############################################################################
# Check Docker
###############################################################################
check_docker() {
  log_info "Checking Docker installation..."
  
  if ! command -v docker >/dev/null 2>&1; then
    log_error "Docker is not installed"
    echo
    echo "Docker is required for MatchZy database provisioning."
    echo "Install Docker Engine:"
    echo "  https://docs.docker.com/engine/install/"
    echo
    if [[ $AUTO_INSTALL -eq 1 ]]; then
      log_error "Auto-install cannot continue without Docker"
      exit 1
    else
      echo -n "Continue anyway? (y/N): "
      read -r response
      if [[ ! "$response" =~ ^[Yy]$ ]]; then
        exit 1
      fi
    fi
  else
    log_success "Docker is installed"
    
    if ! systemctl is-active --quiet docker; then
      log_warn "Docker is not running, attempting to start..."
      sudo systemctl start docker || log_warn "Could not start Docker"
    fi
  fi
}

###############################################################################
# Backup user overrides
###############################################################################
backup_user_overrides() {
  local backup_dir="$1"
  local overrides_dir="$2"
  
  if [[ ! -d "$overrides_dir" ]]; then
    return 0  # Nothing to backup
  fi
  
  mkdir -p "$backup_dir"
  
  # Backup important user files that should be preserved
  local files_to_backup=(
    "game/csgo/cfg/MatchZy/database.json"
    "game/csgo/addons/counterstrikesharp/configs/admins.json"
  )
  
  for file in "${files_to_backup[@]}"; do
    local src="$overrides_dir/$file"
    local dst="$backup_dir/$file"
    
    if [[ -f "$src" ]]; then
      mkdir -p "$(dirname "$dst")"
      cp "$src" "$dst" 2>/dev/null && log_info "Backed up: $file" || true
    fi
  done
  
  # Also backup any other .json files in key directories (user might have added more)
  find "$overrides_dir" -type f -name "*.json" | while read -r json_file; do
    local rel_path="${json_file#$overrides_dir/}"
    local dst="$backup_dir/$rel_path"
    
    # Skip if already backed up
    if [[ ! -f "$dst" ]]; then
      mkdir -p "$(dirname "$dst")"
      cp "$json_file" "$dst" 2>/dev/null || true
    fi
  done
}

###############################################################################
# Restore user overrides
###############################################################################
restore_user_overrides() {
  local backup_dir="$1"
  local overrides_dir="$2"
  
  if [[ ! -d "$backup_dir" ]]; then
    return 0  # Nothing to restore
  fi
  
  # Restore backed up files (always overwrite repo defaults with user's custom files)
  if [[ -d "$backup_dir" ]]; then
    find "$backup_dir" -type f | while read -r backup_file; do
      local rel_path="${backup_file#$backup_dir/}"
      local dst="$overrides_dir/$rel_path"
      
      # Always restore user's custom files (they take precedence over repo defaults)
      mkdir -p "$(dirname "$dst")"
      if cp "$backup_file" "$dst" 2>/dev/null; then
        log_info "Restored: $rel_path"
      fi
    done
  fi
}

###############################################################################
# Sync overrides from repository
###############################################################################
sync_overrides_from_repo() {
  local install_dir="$1"
  
  cd "$install_dir" || return 1
  
  log_info "Syncing overrides folder from repository..."
  
  # Pull latest changes to get updated overrides
  if git pull origin "$REPO_BRANCH" >/dev/null 2>&1; then
    log_success "Repository updated"
  else
    # If pull fails, try fetching and checking out
    git fetch origin "$REPO_BRANCH" >/dev/null 2>&1 || true
    git checkout "origin/$REPO_BRANCH" -- overrides/ >/dev/null 2>&1 || {
      log_warn "Could not sync overrides from repository"
      return 1
    }
  fi
  
  # Ensure overrides directory exists
  if [[ ! -d "$install_dir/overrides" ]]; then
    log_warn "overrides/ directory not found in repository"
    return 1
  fi
  
  log_success "Overrides folder synced from repository"
}

###############################################################################
# Download/Clone repository
###############################################################################
download_repo() {
  log_info "Downloading CS2 Server Manager..."
  
  local backup_temp=""
  local had_existing=0
  
  # Backup user overrides if directory exists
  if [[ -d "$INSTALL_DIR" ]] && [[ -d "$INSTALL_DIR/overrides" ]]; then
    had_existing=1
    backup_temp=$(mktemp -d)
    log_info "Backing up existing overrides..."
    backup_user_overrides "$backup_temp" "$INSTALL_DIR/overrides"
  fi
  
  if [[ -d "$INSTALL_DIR" ]]; then
    log_warn "Directory already exists: $INSTALL_DIR"
    if [[ $AUTO_INSTALL -eq 1 ]]; then
      log_info "Updating existing installation..."
      # Try to update via git pull instead of removing
      if [[ -d "$INSTALL_DIR/.git" ]]; then
        cd "$INSTALL_DIR" || exit 1
        if git pull origin "$REPO_BRANCH" >/dev/null 2>&1; then
          log_success "Repository updated"
        else
          log_warn "Could not pull updates, removing and re-cloning..."
          rm -rf "$INSTALL_DIR"
          git clone --branch "$REPO_BRANCH" "$REPO_URL" "$INSTALL_DIR" || {
            log_error "Failed to clone repository"
            exit 1
          }
        fi
      else
        log_info "Not a git repository, removing and re-cloning..."
        rm -rf "$INSTALL_DIR"
        git clone --branch "$REPO_BRANCH" "$REPO_URL" "$INSTALL_DIR" || {
          log_error "Failed to clone repository"
          exit 1
        }
      fi
    else
      echo -n "Update existing installation? (Y/n): "
      read -r response
      if [[ "$response" =~ ^[Nn]$ ]]; then
        log_info "Keeping existing installation"
        return 0
      else
        # Update via git pull if it's a git repo
        if [[ -d "$INSTALL_DIR/.git" ]]; then
          cd "$INSTALL_DIR" || exit 1
          if git pull origin "$REPO_BRANCH" >/dev/null 2>&1; then
            log_success "Repository updated"
          else
            log_warn "Could not pull updates"
          fi
        else
          log_warn "Not a git repository, cannot update"
        fi
      fi
    fi
  else
    # Clone the repository
    if git clone --branch "$REPO_BRANCH" "$REPO_URL" "$INSTALL_DIR"; then
      log_success "Repository downloaded to $INSTALL_DIR"
    else
      log_error "Failed to clone repository"
      exit 1
    fi
  fi
  
  # Sync overrides from repo first (to get latest structure)
  sync_overrides_from_repo "$INSTALL_DIR"
  
  # Restore user overrides if we backed them up (preserves user customizations)
  if [[ $had_existing -eq 1 ]] && [[ -n "$backup_temp" ]] && [[ -d "$backup_temp" ]]; then
    log_info "Restoring your custom overrides (database.json, admins.json, etc.)..."
    restore_user_overrides "$backup_temp" "$INSTALL_DIR/overrides"
    rm -rf "$backup_temp"
    log_success "User customizations preserved"
  fi
}

###############################################################################
# Run installation
###############################################################################
run_installation() {
  log_info "Starting CS2 server installation..."
  echo
  
  cd "$INSTALL_DIR" || exit 1
  
  if [[ ! -f "./manage.sh" ]]; then
    log_error "manage.sh not found in $INSTALL_DIR"
    exit 1
  fi
  
  # Make manage.sh executable
  chmod +x ./manage.sh
  
  # Handle custom overrides
  if [[ -n "$CUSTOM_OVERRIDES" ]]; then
    if [[ ! -d "$CUSTOM_OVERRIDES" ]]; then
      log_error "Custom overrides directory not found: $CUSTOM_OVERRIDES"
      exit 1
    fi
    log_info "Using custom overrides from: $CUSTOM_OVERRIDES"
    export OVERRIDES_DIR="$CUSTOM_OVERRIDES"
  fi
  
  # Run installation
  if [[ $AUTO_INSTALL -eq 1 ]]; then
    log_info "Running automatic installation (non-interactive)..."
    ./manage.sh install
  else
    log_info "Starting interactive installation..."
    ./manage.sh
  fi
}

###############################################################################
# Show completion message
###############################################################################
show_completion() {
  echo
  echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}  CS2 Server Manager Installation Complete!${NC}"
  echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "Installation directory: $INSTALL_DIR"
  echo
  echo "Next steps:"
  echo "  cd $INSTALL_DIR"
  echo "  ./manage.sh              # Interactive menu"
  echo "  ./manage.sh status       # Check server status"
  echo
  echo "Auto-update monitor:"
  echo "  sudo tail -f /var/log/cs2_auto_update_monitor.log"
  echo
  echo "Documentation:"
  echo "  cat README.md"
  echo
  log_success "Happy gaming! 🎮"
  echo
}

###############################################################################
# Main
###############################################################################
main() {
  clear
  
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  CS2 Server Manager - Quick Installer${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  parse_args "$@"
  
  check_root
  check_prerequisites
  check_dependencies
  check_docker
  
  echo
  log_info "Installation settings:"
  echo "  Directory:  $INSTALL_DIR"
  echo "  Servers:    $NUM_SERVERS"
  echo "  Auto-mode:  $([[ $AUTO_INSTALL -eq 1 ]] && echo "Yes" || echo "No")"
  if [[ -n "$CUSTOM_OVERRIDES" ]]; then
    echo "  Overrides:  $CUSTOM_OVERRIDES"
  else
    echo "  Overrides:  (using repo defaults)"
  fi
  echo
  
  if [[ $AUTO_INSTALL -eq 0 ]]; then
    echo -n "Continue with installation? (Y/n): "
    read -r response
    if [[ "$response" =~ ^[Nn]$ ]]; then
      log_info "Installation cancelled"
      exit 0
    fi
  fi
  
  echo
  download_repo
  run_installation
  show_completion
}

main "$@"

