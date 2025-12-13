#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# Remove CS2 Auto-Update Monitor Cronjob
# 
# This script removes the auto-update monitor cronjob
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR_SCRIPT="${SCRIPT_DIR}/auto_update_monitor.sh"

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
# Check if running as root
###############################################################################
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root (use sudo)"
    exit 1
fi

echo "============================================"
echo "  Remove CS2 Auto-Update Monitor"
echo "============================================"
echo
log_warn "This will remove the automatic update monitoring cronjob"
echo
read -p "Continue? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
fi

###############################################################################
# Remove cronjob
###############################################################################
log_info "Removing cronjob..."

if crontab -l 2>/dev/null | grep -F "$MONITOR_SCRIPT" >/dev/null; then
    crontab -l 2>/dev/null | grep -v "$MONITOR_SCRIPT" | crontab -
    log_success "Cronjob removed successfully"
else
    log_warn "No cronjob found for auto-update monitor"
fi

###############################################################################
# Clean up state file
###############################################################################
STATE_FILE="/tmp/cs2_auto_update_monitor.state"
if [[ -f "$STATE_FILE" ]]; then
    log_info "Removing state file..."
    rm -f "$STATE_FILE"
    log_success "State file removed"
fi

echo
log_info "You can keep the log file at /var/log/cs2_auto_update_monitor.log for reference"
log_info "Or remove it with: sudo rm /var/log/cs2_auto_update_monitor.log"
echo
log_success "Auto-update monitor has been disabled"
echo

exit 0

