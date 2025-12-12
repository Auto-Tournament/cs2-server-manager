#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# CS2 Auto-Update Monitor
# 
# Detects when the CS2 AutoUpdater plugin shuts down servers for a game update
# and automatically triggers the update process.
#
# This script:
#   1. Checks if all servers are shut down
#   2. Looks for the AutoUpdater shutdown message in logs
#   3. Runs game and plugin updates automatically
#   4. Restarts all servers
#
# Designed to be run via cron (e.g., every 5 minutes)
###############################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
STATE_FILE="/tmp/cs2_auto_update_monitor.state"
LOG_FILE="/var/log/cs2_auto_update_monitor.log"

CS2_USER="${CS2_USER:-cs2}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Logging functions
log_info() { 
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] $*"
    echo -e "${BLUE}${msg}${NC}" | tee -a "$LOG_FILE"
}

log_success() { 
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [SUCCESS] $*"
    echo -e "${GREEN}${msg}${NC}" | tee -a "$LOG_FILE"
}

log_warn() { 
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [WARN] $*"
    echo -e "${YELLOW}${msg}${NC}" | tee -a "$LOG_FILE"
}

log_error() { 
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [ERROR] $*"
    echo -e "${RED}${msg}${NC}" | tee -a "$LOG_FILE"
}

###############################################################################
# Check if running as root
###############################################################################
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root (use sudo)"
    exit 1
fi

###############################################################################
# Auto-detect number of servers
###############################################################################
if [[ ! -d "/home/${CS2_USER}" ]]; then
    log_error "CS2 user home directory not found: /home/${CS2_USER}"
    exit 1
fi

NUM_SERVERS=$(find /home/${CS2_USER} -maxdepth 1 -type d -name "server-*" 2>/dev/null | wc -l || echo 0)

if [[ $NUM_SERVERS -eq 0 ]]; then
    log_error "No CS2 servers found in /home/${CS2_USER}"
    exit 1
fi

log_info "Detected $NUM_SERVERS CS2 servers"

###############################################################################
# Function: Check if all servers are shut down
###############################################################################
check_servers_down() {
    local all_down=true
    
    for ((i=1; i<=NUM_SERVERS; i++)); do
        local session_name="cs2-${i}"
        if sudo -u "$CS2_USER" tmux has-session -t "$session_name" 2>/dev/null; then
            all_down=false
            break
        fi
    done
    
    if $all_down; then
        return 0  # All servers are down
    else
        return 1  # At least one server is running
    fi
}

###############################################################################
# Function: Check logs for AutoUpdater shutdown message
###############################################################################
check_autoupdater_shutdown() {
    local found=false
    local shutdown_msg="plugin:AutoUpdater Shutting the server down due to the new game update"
    
    log_info "Checking logs for AutoUpdater shutdown message..."
    
    for ((i=1; i<=NUM_SERVERS; i++)); do
        local log_dir="/home/${CS2_USER}/server-${i}/game/csgo/addons/counterstrikesharp/logs"
        
        if [[ ! -d "$log_dir" ]]; then
            log_warn "Log directory not found for server $i: $log_dir"
            continue
        fi
        
        # Find the most recent log file
        local latest_log=$(find "$log_dir" -maxdepth 1 -type f -name "log-all*.txt" -printf '%T@ %p\n' 2>/dev/null | sort -n | tail -1 | cut -d' ' -f2-)
        
        if [[ -z "$latest_log" ]]; then
            log_warn "No log files found for server $i"
            continue
        fi
        
        # Check if the shutdown message exists in the log
        # We'll look at the last 200 lines to cover recent activity
        if tail -n 200 "$latest_log" | grep -q "$shutdown_msg"; then
            log_info "Found AutoUpdater shutdown message in server $i log: $latest_log"
            found=true
            break
        fi
    done
    
    if $found; then
        return 0
    else
        return 1
    fi
}

###############################################################################
# Function: Check if we've already processed this update
###############################################################################
should_process_update() {
    # If state file doesn't exist, we should process
    if [[ ! -f "$STATE_FILE" ]]; then
        return 0
    fi
    
    # Read the last update timestamp
    local last_update
    last_update=$(cat "$STATE_FILE")
    
    # Get current timestamp
    local current_time
    current_time=$(date +%s)
    
    # If last update was more than 1 hour ago, allow processing again
    # This prevents multiple rapid updates but allows retry if something went wrong
    local time_diff=$((current_time - last_update))
    if [[ $time_diff -gt 3600 ]]; then
        return 0
    else
        log_info "Update already processed recently (${time_diff}s ago), skipping"
        return 1
    fi
}

###############################################################################
# Function: Mark update as processed
###############################################################################
mark_update_processed() {
    date +%s > "$STATE_FILE"
    log_info "Marked update as processed at $(date)"
}

###############################################################################
# Function: Perform the update
###############################################################################
perform_update() {
    log_info "=========================================="
    log_info "Starting automated CS2 update process"
    log_info "=========================================="
    
    # Run game update
    log_info "Step 1: Updating CS2 game files..."
    if cd "$PROJECT_ROOT" && "${SCRIPT_DIR}/update.sh" game <<< "y"; then
        log_success "Game update completed successfully"
    else
        log_error "Game update failed!"
        return 1
    fi
    
    # Wait a bit for servers to stabilize
    sleep 5
    
    # Check server status
    log_info "Checking server status after update..."
    "${SCRIPT_DIR}/cs2_tmux.sh" status | tee -a "$LOG_FILE"
    
    log_info "=========================================="
    log_success "Automated update process completed!"
    log_info "=========================================="
    
    return 0
}

###############################################################################
# Main Logic
###############################################################################
main() {
    log_info "Starting CS2 Auto-Update Monitor check"
    
    # Step 1: Check if all servers are down
    if ! check_servers_down; then
        log_info "Servers are running normally, no action needed"
        exit 0
    fi
    
    log_info "All servers are shut down, checking logs for AutoUpdater message..."
    
    # Step 2: Check logs for AutoUpdater shutdown message
    if ! check_autoupdater_shutdown; then
        log_info "No AutoUpdater shutdown message found in logs"
        log_info "Servers may have been stopped manually or for other reasons"
        exit 0
    fi
    
    log_warn "AutoUpdater shutdown detected!"
    
    # Step 3: Check if we should process this update
    if ! should_process_update; then
        exit 0
    fi
    
    log_info "Proceeding with automated update..."
    
    # Step 4: Perform the update
    if perform_update; then
        mark_update_processed
        log_success "Automated update completed successfully!"
        
        # Optionally send notification (uncomment if you have mail configured)
        # echo "CS2 servers have been automatically updated" | mail -s "CS2 Auto-Update Complete" root
    else
        log_error "Automated update failed! Manual intervention may be required."
        
        # Optionally send error notification
        # echo "CS2 auto-update failed! Check logs at $LOG_FILE" | mail -s "CS2 Auto-Update FAILED" root
        
        exit 1
    fi
}

# Run main function
main "$@"

exit 0

