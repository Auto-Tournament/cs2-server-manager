#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# Setup CS2 Auto-Update Monitor Cronjob
# 
# This script installs a cronjob that monitors for AutoUpdater shutdowns
# and automatically triggers updates when detected.
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR_SCRIPT="${SCRIPT_DIR}/auto_update_monitor.sh"
CRON_INTERVAL="${1:-*/5}"  # Default: every 5 minutes

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
echo "  CS2 Auto-Update Monitor Setup"
echo "============================================"
echo
log_info "This will set up automatic monitoring for CS2 game updates"
echo
echo "How it works:"
echo "  1. Cronjob runs every 5 minutes (configurable)"
echo "  2. Checks if all servers are shut down"
echo "  3. Looks for AutoUpdater shutdown message in logs"
echo "  4. Automatically runs game update"
echo "  5. Restarts all servers"
echo
log_warn "The monitor will only trigger when:"
log_warn "  • ALL servers are shut down"
log_warn "  • AutoUpdater shutdown message is found in logs"
log_warn "  • At least 1 hour has passed since last auto-update"
echo
read -p "Continue with installation? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
fi

###############################################################################
# Make monitor script executable
###############################################################################
log_info "Making monitor script executable..."
chmod +x "$MONITOR_SCRIPT"
log_success "Monitor script is executable"

###############################################################################
# Create log file with proper permissions
###############################################################################
LOG_FILE="/var/log/cs2_auto_update_monitor.log"
log_info "Creating log file: $LOG_FILE"
touch "$LOG_FILE"
chmod 644 "$LOG_FILE"
log_success "Log file created"

###############################################################################
# Set up cronjob
###############################################################################
CRON_COMMAND="${MONITOR_SCRIPT} >> /var/log/cs2_auto_update_monitor.log 2>&1"
CRON_LINE="${CRON_INTERVAL} * * * * $CRON_COMMAND"

log_info "Setting up cronjob..."

# Check if cronjob already exists
if crontab -l 2>/dev/null | grep -F "$MONITOR_SCRIPT" >/dev/null; then
    log_warn "Cronjob already exists, removing old entry..."
    crontab -l 2>/dev/null | grep -v "$MONITOR_SCRIPT" | crontab -
fi

# Add new cronjob
(crontab -l 2>/dev/null; echo "$CRON_LINE") | crontab -

log_success "Cronjob installed successfully!"
echo

###############################################################################
# Summary
###############################################################################
echo "============================================"
echo "  Installation Complete!"
echo "============================================"
echo
log_success "Auto-update monitor is now active"
echo
echo "Configuration:"
echo "  Monitor Script: $MONITOR_SCRIPT"
echo "  Check Interval: Every 5 minutes"
echo "  Log File:       $LOG_FILE"
echo
echo "The monitor will check:"
echo "  1. Are all servers shut down?"
echo "  2. Is there an AutoUpdater shutdown message in logs?"
echo "  3. If yes to both → automatic update triggered"
echo
echo "View current cronjobs:"
echo "  sudo crontab -l"
echo
echo "View monitor logs:"
echo "  sudo tail -f $LOG_FILE"
echo
echo "Manually test the monitor:"
echo "  sudo $MONITOR_SCRIPT"
echo
echo "Remove the cronjob:"
echo "  sudo crontab -e"
echo "  (then delete the line containing: auto_update_monitor.sh)"
echo
log_warn "Note: Updates will only trigger once per hour to prevent loops"
echo

exit 0

