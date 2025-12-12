#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# CS2 Tmux Manager
# 
# Manages CS2 servers in tmux sessions for easy console access and debugging
#
# Usage:
#   ./cs2_tmux.sh start [server_num]    # Start server(s)
#   ./cs2_tmux.sh stop [server_num]     # Stop server(s)
#   ./cs2_tmux.sh restart [server_num]  # Restart server(s)
#   ./cs2_tmux.sh attach <server_num>   # Attach to server console
#   ./cs2_tmux.sh list                  # List all sessions
#   ./cs2_tmux.sh status                # Show status of all servers
#   ./cs2_tmux.sh logs <server_num> [lines] # Show latest log file
###############################################################################

CS2_USER="${CS2_USER:-cs2}"

# Auto-detect number of servers or default to 3
if [[ -z "${NUM_SERVERS:-}" ]]; then
  # Count existing server directories
  if [[ -d "/home/${CS2_USER}" ]]; then
    SERVER_COUNT=$(find /home/${CS2_USER} -maxdepth 1 -type d -name "server-*" 2>/dev/null | wc -l || echo 0)
    if [[ $SERVER_COUNT -gt 0 ]]; then
      NUM_SERVERS=$SERVER_COUNT
    else
      NUM_SERVERS=3
    fi
  else
    NUM_SERVERS=3  # Default to 3 servers if cs2 user doesn't exist yet
  fi
fi

BASE_PORT="${BASE_GAME_PORT:-27015}"

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

# Check if tmux is installed
check_tmux() {
  if ! command -v tmux >/dev/null 2>&1; then
    log_error "tmux is not installed"
    echo "Install with: sudo apt-get install tmux"
    exit 1
  fi
}

# Check if running as CS2 user or root
check_user() {
  if [[ $EUID -eq 0 ]]; then
    # Running as root, switch to CS2 user
    return 0
  elif [[ $(whoami) != "$CS2_USER" ]]; then
    log_error "This script must be run as root or as the '$CS2_USER' user"
    echo "Run with: sudo $0 $*"
    exit 1
  fi
}

# Get server directory
get_server_dir() {
  local server_num=$1
  echo "/home/${CS2_USER}/server-${server_num}"
}

# Get server port
get_server_port() {
  local server_num=$1
  echo $((BASE_PORT + (server_num - 1) * 10))
}

# Check if tmux session exists
session_exists() {
  local session_name=$1
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" tmux has-session -t "$session_name" 2>/dev/null
  else
    tmux has-session -t "$session_name" 2>/dev/null
  fi
}

# Start a single server
start_server() {
  local server_num=$1
  local session_name="cs2-${server_num}"
  local server_dir=$(get_server_dir "$server_num")
  local port=$(get_server_port "$server_num")
  
  if ! [[ -d "$server_dir" ]]; then
    log_error "Server $server_num not found at $server_dir"
    log_info "Run ./manage.sh and choose Option 1 (Install servers) first"
    return 1
  fi
  
  if session_exists "$session_name"; then
    log_warn "Server $server_num is already running (tmux session: $session_name)"
    return 0
  fi
  
  log_info "Starting server $server_num (port $port)..."
  
  # Create tmux session and run CS2
  # Use cs2.sh script from game folder (it does important setup)
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" tmux new-session -d -s "$session_name" -c "$server_dir/game" \
      "./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port $port +tv_port $((port + 5)) +maxplayers 10 -usercon"
  else
    tmux new-session -d -s "$session_name" -c "$server_dir/game" \
      "./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port $port +tv_port $((port + 5)) +maxplayers 10 -usercon"
  fi
  
  sleep 1
  
  if session_exists "$session_name"; then
    log_success "Server $server_num started (attach with: $0 attach $server_num)"
  else
    log_error "Failed to start server $server_num"
    return 1
  fi
}

# Stop a single server
stop_server() {
  local server_num=$1
  local session_name="cs2-${server_num}"
  
  if ! session_exists "$session_name"; then
    log_warn "Server $server_num is not running"
    return 0
  fi
  
  log_info "Stopping server $server_num..."
  
  # Send quit command to console
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" tmux send-keys -t "$session_name" "quit" C-m
    sleep 2
    # Force kill session if still exists
    sudo -u "$CS2_USER" tmux kill-session -t "$session_name" 2>/dev/null || true
  else
    tmux send-keys -t "$session_name" "quit" C-m
    sleep 2
    tmux kill-session -t "$session_name" 2>/dev/null || true
  fi
  
  log_success "Server $server_num stopped"
}

# Restart a single server
restart_server() {
  local server_num=$1
  stop_server "$server_num"
  sleep 1
  start_server "$server_num"
}

# Attach to a server console
attach_server() {
  local server_num=$1
  local session_name="cs2-${server_num}"
  
  if ! session_exists "$session_name"; then
    log_error "Server $server_num is not running"
    return 1
  fi
  
  log_info "Attaching to server $server_num console..."
  log_info "Press Ctrl+B then D to detach without stopping the server"
  echo
  sleep 1
  
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" tmux attach-session -t "$session_name"
  else
    tmux attach-session -t "$session_name"
  fi
}

# List all sessions
list_sessions() {
  log_info "CS2 Tmux Sessions:"
  echo
  
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" tmux list-sessions 2>/dev/null | grep "cs2-" || log_warn "No CS2 sessions running"
  else
    tmux list-sessions 2>/dev/null | grep "cs2-" || log_warn "No CS2 sessions running"
  fi
}

# Show status of all servers
show_status() {
  echo "=========================================="
  echo "  CS2 Server Status (Tmux)"
  echo "=========================================="
  echo
  
  for ((i=1; i<=NUM_SERVERS; i++)); do
    local session_name="cs2-${i}"
    local port=$(get_server_port "$i")
    local server_dir=$(get_server_dir "$i")
    
    if ! [[ -d "$server_dir" ]]; then
      continue
    fi
    
    printf "Server %d (Port %d): " "$i" "$port"
    
    if session_exists "$session_name"; then
      echo -e "${GREEN}RUNNING${NC}"
      echo "  Attach: $0 attach $i"
    else
      echo -e "${RED}STOPPED${NC}"
      echo "  Start:  $0 start $i"
    fi
    echo
  done
  
  echo "=========================================="
}

# Run server in debug mode (foreground, no tmux)
debug_server() {
  local server_num=$1
  local server_dir=$(get_server_dir "$server_num")
  local port=$(get_server_port "$server_num")
  
  if ! [[ -d "$server_dir" ]]; then
    log_error "Server $server_num not found at $server_dir"
    log_info "Run ./manage.sh and choose Option 1 (Install servers) first"
    return 1
  fi
  
  log_info "Starting server $server_num in DEBUG mode (foreground)"
  log_info "Port: $port, Directory: $server_dir"
  log_warn "Press Ctrl+C to stop the server"
  echo
  echo "=========================================="
  echo
  
  cd "$server_dir/game" || return 1
  
  if [[ $EUID -eq 0 ]]; then
    sudo -u "$CS2_USER" ./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port "$port" +tv_port $((port + 5)) +maxplayers 10 -usercon
  else
    ./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port "$port" +tv_port $((port + 5)) +maxplayers 10 -usercon
  fi
}

# Show logs for a server
show_logs() {
  local server_num=$1
  local lines="${2:-0}"  # 0 = show all, otherwise show last N lines
  local session_name="cs2-${server_num}"
  local server_dir=$(get_server_dir "$server_num")
  
  if ! [[ -d "$server_dir" ]]; then
    log_error "Server $server_num not found at $server_dir"
    return 1
  fi
  
  # Try to get tmux session output first (full server console)
  local tmux_output=""
  if [[ $EUID -eq 0 ]]; then
    if sudo -u "$CS2_USER" tmux has-session -t "$session_name" 2>/dev/null; then
      tmux_output=$(sudo -u "$CS2_USER" tmux capture-pane -t "$session_name" -p 2>/dev/null)
    fi
  else
    if tmux has-session -t "$session_name" 2>/dev/null; then
      tmux_output=$(tmux capture-pane -t "$session_name" -p 2>/dev/null)
    fi
  fi
  
  if [[ -n "$tmux_output" ]]; then
    log_info "Tmux session output for server $server_num:"
    log_info "Session: $session_name"
    echo "=========================================="
    if [[ $lines -eq 0 ]]; then
      # Show all
      echo "$tmux_output"
    else
      # Show last N lines
      echo "$tmux_output" | tail -n "$lines"
    fi
    echo "=========================================="
    echo
    log_info "To see more lines: $0 logs $server_num <num_lines>"
    log_info "To follow live: $0 attach $server_num"
    return 0
  fi
  
  # Fallback to CounterStrikeSharp logs if tmux session not available
  log_warn "Tmux session not found, falling back to CounterStrikeSharp logs"
  local log_dir="${server_dir}/game/csgo/addons/counterstrikesharp/logs"
  
  if ! [[ -d "$log_dir" ]]; then
    log_error "Log directory not found: $log_dir"
    log_info "Server may not have been started yet or CounterStrikeSharp is not installed"
    return 1
  fi
  
  # Find the most recent log-all*.txt file
  local latest_log=$(find "$log_dir" -maxdepth 1 -type f -name "log-all*.txt" -printf '%T@ %p\n' 2>/dev/null | sort -n | tail -1 | cut -d' ' -f2-)
  
  if [[ -z "$latest_log" ]]; then
    log_error "No log files found in $log_dir"
    return 1
  fi
  
  log_info "Latest CounterStrikeSharp log for server $server_num:"
  log_info "File: $latest_log"
  if [[ $lines -eq 0 ]]; then
    log_info "Showing all lines"
    echo "=========================================="
    cat "$latest_log"
  else
    log_info "Showing last $lines lines"
    echo "=========================================="
    tail -n "$lines" "$latest_log"
  fi
  echo "=========================================="
  echo
  log_info "To see more lines: $0 logs $server_num <num_lines>"
  log_info "To follow live: tail -f $latest_log"
}

# Usage information
usage() {
  cat << EOF
CS2 Tmux Manager - Easy console access for CS2 servers

Usage:
  $0 start [server_num]       Start server(s) in tmux
  $0 stop [server_num]        Stop server(s)
  $0 restart [server_num]     Restart server(s)
  $0 attach <server_num>      Attach to server console
  $0 list                     List all tmux sessions
  $0 status                   Show status of all servers
  $0 logs <server_num> [lines] Show tmux session output (default: all lines)
  $0 debug <server_num>       Start server in foreground (for debugging)

Examples:
  $0 start                    Start all servers
  $0 start 1                  Start server 1 only
  $0 stop 2                   Stop server 2
  $0 restart 3                Restart server 3
  $0 attach 1                 Attach to server 1 console
  $0 status                   Show all server status
  $0 logs 1                   Show all server 1 tmux session output
  $0 logs 1 100               Show last 100 lines of server 1 tmux session
  $0 debug 1                  Start server 1 in foreground (see all output)

Debug Mode:
  - Use 'debug' to run server in current terminal
  - See all output directly (great for troubleshooting)
  - Press Ctrl+C to stop
  - Example: $0 debug 1

Logs:
  - View tmux session output: $0 logs <num>
  - Shows full server console output from tmux session
  - Specify line count: $0 logs <num> <lines>
  - Falls back to CounterStrikeSharp logs if tmux session unavailable
  - Example: $0 logs 1 (shows all) or $0 logs 1 200 (shows last 200 lines)

Tmux Tips:
  - Attach to console: $0 attach <num>
  - Detach without stopping: Press Ctrl+B, then D
  - Scroll in tmux: Press Ctrl+B, then [
  - Exit scroll mode: Press Q
  - Send commands: Just type when attached

EOF
}

###############################################################################
# Main
###############################################################################

check_tmux
check_user

case "${1:-}" in
  start)
    if [[ -n "${2:-}" ]]; then
      start_server "$2"
    else
      log_info "Starting all servers..."
      for ((i=1; i<=NUM_SERVERS; i++)); do
        start_server "$i"
      done
    fi
    echo
    show_status
    ;;
    
  stop)
    if [[ -n "${2:-}" ]]; then
      stop_server "$2"
    else
      log_info "Stopping all servers..."
      for ((i=1; i<=NUM_SERVERS; i++)); do
        stop_server "$i"
      done
    fi
    ;;
    
  restart)
    if [[ -n "${2:-}" ]]; then
      restart_server "$2"
    else
      log_info "Restarting all servers..."
      for ((i=1; i<=NUM_SERVERS; i++)); do
        restart_server "$i"
      done
    fi
    echo
    show_status
    ;;
    
  attach)
    if [[ -z "${2:-}" ]]; then
      log_error "Please specify server number"
      echo "Usage: $0 attach <server_num>"
      exit 1
    fi
    attach_server "$2"
    ;;
    
  list)
    list_sessions
    ;;
    
  status)
    show_status
    ;;
    
  debug)
    if [[ -z "${2:-}" ]]; then
      log_error "Please specify server number"
      echo "Usage: $0 debug <server_num>"
      exit 1
    fi
    debug_server "$2"
    ;;
    
  logs)
    if [[ -z "${2:-}" ]]; then
      log_error "Please specify server number"
      echo "Usage: $0 logs <server_num> [num_lines]"
      exit 1
    fi
    show_logs "$2" "${3:-}"
    ;;
    
  *)
    usage
    exit 1
    ;;
esac

