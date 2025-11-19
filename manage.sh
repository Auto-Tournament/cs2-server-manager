#!/usr/bin/env bash

###############################################################################
# CS2 Server Manager - Interactive Menu
###############################################################################

cd "$(dirname "${BASH_SOURCE[0]}")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

MATCHZY_DB_CONFIG="overrides/game/csgo/cfg/MatchZy/database.json"
MATCHZY_SKIP_DOCKER=0

show_header() {
  clear
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}          CS2 Server Manager${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
}

show_menu() {
  echo -e "${CYAN}═══ Setup & Installation ═══${NC}"
  echo "  1) Install / redeploy servers"
  echo
  echo -e "${GREEN}═══ Server Control ═══${NC}"
  echo "  2) Show server status"
  echo "  3) Start all servers"
  echo "  4) Stop all servers"
  echo "  5) Restart all servers"
  echo "  6) Start single server"
  echo "  7) Stop single server"
  echo "  8) Restart single server"
  echo
  echo -e "${BLUE}═══ Server Management ═══${NC}"
  echo "  9) Remove specific server"
  echo " 10) Reduce number of servers (5→3, etc)"
  echo " 11) List all server directories"
  echo
  echo -e "${YELLOW}═══ Updates & Maintenance ═══${NC}"
  echo " 12) Update CS2 game (after Valve update)"
  echo " 13) Update plugins (Metamod, CSS, MatchZy, AutoUpdater)"
  echo " 14) Apply config changes"
  echo " 15) Repair servers (verify files + reinstall plugins)"
  echo " 16) Install/reinstall auto-update monitor"
  echo
  echo -e "${CYAN}═══ Debugging & Logs ═══${NC}"
  echo " 17) Debug mode (run server in foreground)"
  echo " 18) View server logs"
  echo " 19) Attach to server console"
  echo " 20) List tmux sessions"
  echo " 21) Execute RCON command"
  echo
  echo -e "${RED}═══ Danger Zone ═══${NC}"
  echo " 22) Cleanup everything (remove servers/user)"
  echo
  echo "  0) Exit"
  echo
  echo -n "Choose an option: "
}

press_enter() {
  echo
  echo -e "${CYAN}Press Enter to continue...${NC}"
  read -r
}

ensure_matchzy_db_config_file() {
  local config_dir
  config_dir="$(dirname "$MATCHZY_DB_CONFIG")"
  mkdir -p "$config_dir"
  if [[ ! -f "$MATCHZY_DB_CONFIG" ]]; then
    cat > "$MATCHZY_DB_CONFIG" <<'EOF'
{
  "DatabaseType": "MySQL",
  "MySqlHost": "127.0.0.1",
  "MySqlDatabase": "matchzy",
  "MySqlUsername": "matchzy",
  "MySqlPassword": "matchzy",
  "MySqlPort": 3306
}
EOF
  fi
}

update_matchzy_db_config() {
  local host="$1"
  local port="$2"
  local db="$3"
  local user="$4"
  local pass="$5"
  local tmp
  tmp=$(mktemp)
  jq --arg host "$host" \
     --argjson port "$port" \
     --arg db "$db" \
     --arg user "$user" \
     --arg pass "$pass" '
      .DatabaseType = "MySQL" |
      .MySqlHost = $host |
      .MySqlPort = $port |
      .MySqlDatabase = $db |
      .MySqlUsername = $user |
      .MySqlPassword = $pass
     ' "$MATCHZY_DB_CONFIG" > "$tmp"
  mv "$tmp" "$MATCHZY_DB_CONFIG"
}

configure_matchzy_database() {
  ensure_matchzy_db_config_file

  local current_host current_port current_db current_user current_pass
  current_host=$(jq -r '.MySqlHost // "127.0.0.1"' "$MATCHZY_DB_CONFIG")
  current_port=$(jq -r '.MySqlPort // 3306' "$MATCHZY_DB_CONFIG")
  current_db=$(jq -r '.MySqlDatabase // "matchzy"' "$MATCHZY_DB_CONFIG")
  current_user=$(jq -r '.MySqlUsername // "matchzy"' "$MATCHZY_DB_CONFIG")
  current_pass=$(jq -r '.MySqlPassword // "matchzy"' "$MATCHZY_DB_CONFIG")

  echo
  echo -e "${GREEN}MatchZy Database Configuration${NC}"
  echo "  1) Auto-manage local MySQL via Docker (default)"
  echo "  2) Use existing MySQL server (skip Docker provisioning)"
  echo
  read -rp "Choose an option [1]: " db_choice

  if [[ "$db_choice" == "2" ]]; then
    read -rp "MySQL host [$current_host]: " new_host
    read -rp "MySQL port [$current_port]: " new_port
    read -rp "Database name [$current_db]: " new_db
    read -rp "Database user [$current_user]: " new_user
    read -rp "Database password [$current_pass]: " new_pass

    new_host=${new_host:-$current_host}
    new_port=${new_port:-$current_port}
    new_db=${new_db:-$current_db}
    new_user=${new_user:-$current_user}
    new_pass=${new_pass:-$current_pass}

    if ! [[ "$new_port" =~ ^[0-9]+$ ]]; then
      echo -e "${RED}Invalid port number.${NC}"
      press_enter
      return 1
    fi

    update_matchzy_db_config "$new_host" "$new_port" "$new_db" "$new_user" "$new_pass"
    MATCHZY_SKIP_DOCKER=1
    echo
    echo -e "${YELLOW}Docker provisioning will be skipped. Ensure the provided database is reachable.${NC}"
  else
    MATCHZY_SKIP_DOCKER=0
    echo
    echo -e "${GREEN}Will provision/manage local MatchZy MySQL via Docker.${NC}"
  fi
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo -e "${RED}Docker is required for MatchZy database provisioning but is not installed.${NC}"
    echo "Install Docker Engine using the official instructions:"
    echo "  https://docs.docker.com/engine/install/"
    echo
    press_enter
    return 1
  fi

  if ! systemctl is-active --quiet docker; then
    echo -e "${YELLOW}Docker is installed but not running. Attempting to start it...${NC}"
    if ! sudo systemctl start docker; then
      echo -e "${RED}Failed to start Docker service. Start Docker manually then retry.${NC}"
      press_enter
      return 1
    fi
  fi

  return 0
}

install_servers() {
  local auto_yes=${1:-0}  # Non-interactive mode flag
  
  show_header
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Install / Redeploy CS2 Servers${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  if (( auto_yes == 1 )); then
    ensure_matchzy_db_config_file
    MATCHZY_SKIP_DOCKER=0
  else
    if ! configure_matchzy_database; then
      return 1
    fi
  fi
  
  if (( MATCHZY_SKIP_DOCKER == 0 )); then
    if ! require_docker; then
      return 1
    fi
  fi
  
  # Auto-detect existing servers or default to 3
  local detected_servers=3
  if [[ -d "/home/cs2" ]]; then
    local server_count=$(find /home/cs2 -maxdepth 1 -type d -name "server-*" 2>/dev/null | wc -l)
    if [[ $server_count -gt 0 ]]; then
      detected_servers=$server_count
    fi
  fi

  if (( auto_yes == 1 )); then
    # Non-interactive mode: use all defaults
    num_servers=$detected_servers
    base_port=27015
    tv_port=27020
    cs2_user="cs2"
    metamod_flag=1
    fresh_flag=0
    update_master_flag=1
    rcon_password="ntlan2025"
    run_plugin_update=1
    echo "Using default values (non-interactive mode)..."
  else
    # Interactive mode
    echo "Leave blank to use defaults (shown in brackets)."
    echo
    
    read -rp "Number of servers [$detected_servers]: " num_servers
    if [[ ! "$num_servers" =~ ^[0-9]+$ ]]; then num_servers=$detected_servers; fi

    read -rp "Base game port [27015]: " base_port
    if [[ ! "$base_port" =~ ^[0-9]+$ ]]; then base_port=27015; fi

    read -rp "Base GOTV port [27020]: " tv_port
    if [[ ! "$tv_port" =~ ^[0-9]+$ ]]; then tv_port=27020; fi

    read -rp "CS2 system user [cs2]: " cs2_user
    cs2_user=${cs2_user:-cs2}

    read -rp "Enable Metamod? [Y/n]: " enable_metamod
    if [[ "$enable_metamod" =~ ^[Nn]$ ]]; then metamod_flag=0; else metamod_flag=1; fi

    read -rp "Fresh install (delete existing servers)? [y/N]: " fresh
    if [[ "$fresh" =~ ^[Yy]$ ]]; then fresh_flag=1; else fresh_flag=0; fi

    read -rp "Update master install via SteamCMD? [Y/n]: " update_master
    if [[ "$update_master" =~ ^[Nn]$ ]]; then update_master_flag=0; else update_master_flag=1; fi

    read -rp "RCON password [ntlan2025]: " rcon_password
    rcon_password=${rcon_password:-ntlan2025}

    read -rp "Download latest plugins before install? [Y/n]: " download_plugins
    if [[ "$download_plugins" =~ ^[Nn]$ ]]; then
      run_plugin_update=0
    else
      run_plugin_update=1
    fi
  fi

  echo
  echo -e "${YELLOW}Summary:${NC}"
  local matchzy_mode
  if [[ ${MATCHZY_SKIP_DOCKER:-0} -eq 0 ]]; then
    matchzy_mode="Docker (managed)"
  else
    matchzy_mode="External (manual)"
  fi
  echo "  Servers        : $num_servers"
  echo "  Base port      : $base_port"
  echo "  GOTV base port : $tv_port"
  echo "  CS2 user       : $cs2_user"
  echo "  Metamod        : $([[ $metamod_flag -eq 1 ]] && echo Enabled || echo Disabled)"
  echo "  Fresh install  : $([[ $fresh_flag -eq 1 ]] && echo Yes || echo No)"
  echo "  Update master  : $([[ $update_master_flag -eq 1 ]] && echo Yes || echo No)"
  echo "  RCON password  : $rcon_password"
  echo "  Update plugins : $([[ $run_plugin_update -eq 1 ]] && echo Yes || echo No)"
  echo "  MatchZy DB     : $matchzy_mode"
  echo
  
  if (( auto_yes == 1 )); then
    confirm="y"
    echo "Auto-confirming installation..."
  else
    read -rp "Proceed with installation? (y/N): " confirm
  fi
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    if [[ $run_plugin_update -eq 1 ]]; then
      echo -e "${GREEN}Downloading latest plugins...${NC}"
      echo
      
      # Capture both stdout and stderr
      local plugin_output
      local plugin_exit_code
      
      plugin_output=$(./scripts/update.sh plugins 2>&1)
      plugin_exit_code=$?
      
      # Show the output
      echo "$plugin_output"
      
      if [[ $plugin_exit_code -ne 0 ]]; then
        echo
        echo -e "${RED}════════════════════════════════════════════════════════${NC}"
        echo -e "${RED}Plugin download failed with exit code: ${plugin_exit_code}${NC}"
        echo -e "${RED}════════════════════════════════════════════════════════${NC}"
        echo
        echo "Diagnostic information:"
        echo "  Working directory: $(pwd)"
        echo "  Script exists: $(test -f ./scripts/update.sh && echo "Yes" || echo "No")"
        echo "  Script executable: $(test -x ./scripts/update.sh && echo "Yes" || echo "No")"
        echo
        echo "Checking dependencies:"
        for cmd in curl jq unzip tar rsync; do
          if command -v "$cmd" >/dev/null 2>&1; then
            echo "  ✓ $cmd: $(command -v "$cmd")"
          else
            echo "  ✗ $cmd: NOT FOUND"
          fi
        done
        echo
        echo -e "${YELLOW}To debug manually, run:${NC}"
        echo "  cd $(pwd) && ./scripts/update.sh plugins"
        echo
        if (( auto_yes == 0 )); then press_enter; fi
        return 1
      fi
      echo
    fi

    sudo env \
      MATCHZY_SKIP_DOCKER="${MATCHZY_SKIP_DOCKER:-0}" \
      NUM_SERVERS="$num_servers" \
      BASE_GAME_PORT="$base_port" \
      BASE_TV_PORT="$tv_port" \
      CS2_USER="$cs2_user" \
      ENABLE_METAMOD="$metamod_flag" \
      FRESH_INSTALL="$fresh_flag" \
      UPDATE_MASTER="$update_master_flag" \
      RCON_PASSWORD="$rcon_password" \
      ./scripts/bootstrap_cs2.sh
    echo
    echo "Installation complete."
    
    # Install auto-update monitor cronjob
    echo
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Setting up Auto-Update Monitor${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo
    echo -e "${BLUE}[INFO]${NC} Installing auto-update monitor cronjob..."
    
    # Make monitor script executable
    chmod +x ./scripts/auto_update_monitor.sh
    
    # Create log file
    sudo touch /var/log/cs2_auto_update_monitor.log 2>/dev/null || true
    sudo chmod 644 /var/log/cs2_auto_update_monitor.log 2>/dev/null || true
    
    # Set up cronjob
    MONITOR_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/auto_update_monitor.sh"
    CRON_COMMAND="${MONITOR_SCRIPT} >> /var/log/cs2_auto_update_monitor.log 2>&1"
    CRON_LINE="*/5 * * * * $CRON_COMMAND"
    
    # Remove old cronjob if exists and add new one (in root's crontab)
    (sudo crontab -l 2>/dev/null | grep -v "auto_update_monitor.sh" || true; echo "$CRON_LINE") | sudo crontab -
    
    echo -e "${GREEN}✓${NC} Auto-update monitor installed (checks every 5 minutes)"
    echo -e "${GREEN}✓${NC} Log file: /var/log/cs2_auto_update_monitor.log"
    echo
    echo "The monitor will automatically:"
    echo "  • Detect when AutoUpdater shuts down servers for game updates"
    echo "  • Run game updates via SteamCMD"
    echo "  • Restart all servers"
    echo
    echo "View monitor logs: sudo tail -f /var/log/cs2_auto_update_monitor.log"
    echo
    
    # Start all servers
    echo
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Starting All Servers${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo
    sudo ./scripts/cs2_tmux.sh start
    echo
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  All Done! Servers are starting up...${NC}"
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo
    echo "Check server status:"
    echo "  ./manage.sh status"
    echo
    echo "View server console:"
    echo "  sudo ./scripts/cs2_tmux.sh attach 1"
    echo
  else
    echo "Cancelled."
  fi
  
  if (( auto_yes == 0 )); then press_enter; fi
}

# 1. Show status
show_status() {
  show_header
  echo -e "${GREEN}Checking server status...${NC}"
  echo
  sudo ./scripts/cs2_tmux.sh status
  press_enter
}

# 2. Start all
start_all() {
  show_header
  echo -e "${GREEN}Starting all servers...${NC}"
  echo
  sudo ./scripts/cs2_tmux.sh start
  press_enter
}

# 3. Stop all
stop_all() {
  show_header
  echo -e "${YELLOW}Stopping all servers...${NC}"
  echo
  sudo ./scripts/cs2_tmux.sh stop
  press_enter
}

# 4. Restart all
restart_all() {
  show_header
  echo -e "${YELLOW}Restarting all servers...${NC}"
  echo
  sudo ./scripts/cs2_tmux.sh restart
  press_enter
}

# 6. Start single server
start_single() {
  show_header
  echo -e "${GREEN}Start which server?${NC}"
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ "$server_num" =~ ^[0-9]+$ ]]; then
    echo
    echo "Starting server $server_num..."
    sudo ./scripts/cs2_tmux.sh start "$server_num"
  else
    echo -e "${RED}Invalid server number${NC}"
  fi
  press_enter
}

# 7. Stop single server
stop_single() {
  show_header
  echo -e "${YELLOW}Stop which server?${NC}"
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ "$server_num" =~ ^[0-9]+$ ]]; then
    echo
    echo "Stopping server $server_num..."
    sudo ./scripts/cs2_tmux.sh stop "$server_num"
  else
    echo -e "${RED}Invalid server number${NC}"
  fi
  press_enter
}

# 8. Restart single server
restart_single() {
  show_header
  echo -e "${YELLOW}Restart which server?${NC}"
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ "$server_num" =~ ^[0-9]+$ ]]; then
    echo
    echo "Restarting server $server_num..."
    sudo ./scripts/cs2_tmux.sh restart "$server_num"
  else
    echo -e "${RED}Invalid server number${NC}"
  fi
  press_enter
}

# 9. Remove specific server
remove_specific_server() {
  show_header
  echo -e "${RED}════════════════════════════════════════════════════════${NC}"
  echo -e "${RED}  Remove Specific Server${NC}"
  echo -e "${RED}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will permanently delete a server directory."
  echo
  echo -n "Enter server number to remove: "
  read -r server_num
  
  if [[ ! "$server_num" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}Invalid server number${NC}"
    press_enter
    return
  fi
  
  local server_dir="/home/cs2/server-${server_num}"
  
  if [[ ! -d "$server_dir" ]]; then
    echo -e "${RED}Server $server_num does not exist at $server_dir${NC}"
    press_enter
    return
  fi
  
  echo
  echo -e "${YELLOW}WARNING: This will delete:${NC}"
  echo "  $server_dir"
  echo
  echo -n "Type server number again to confirm: "
  read -r confirm
  
  if [[ "$confirm" == "$server_num" ]]; then
    echo
    echo "Stopping server $server_num..."
    sudo ./scripts/cs2_tmux.sh stop "$server_num" 2>/dev/null || true
    
    echo "Removing server directory..."
    sudo rm -rf "$server_dir"
    
    echo -e "${GREEN}Server $server_num removed successfully${NC}"
  else
    echo "Cancelled."
  fi
  press_enter
}

# 10. Reduce number of servers
reduce_servers() {
  show_header
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo -e "${YELLOW}  Reduce Number of Servers${NC}"
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo
  echo "Current servers:"
  for i in /home/cs2/server-*; do
    if [[ -d "$i" ]]; then
      echo "  $(basename "$i")"
    fi
  done
  echo
  echo -n "How many servers do you want to keep? "
  read -r keep_count
  
  if [[ ! "$keep_count" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}Invalid number${NC}"
    press_enter
    return
  fi
  
  # Find all server numbers
  local all_servers=()
  for dir in /home/cs2/server-*; do
    if [[ -d "$dir" ]]; then
      local num=$(basename "$dir" | grep -oP 'server-\K[0-9]+')
      all_servers+=("$num")
    fi
  done
  
  local total_servers=${#all_servers[@]}
  
  # Check if we need to remove any
  if [[ $keep_count -ge $total_servers ]]; then
    echo -e "${YELLOW}You already have $total_servers servers. Nothing to remove.${NC}"
    press_enter
    return
  fi
  
  # Sort in reverse order (highest to lowest)
  IFS=$'\n' sorted_servers=($(sort -rn <<<"${all_servers[*]}"))
  unset IFS
  
  # Calculate how many to remove
  local remove_count=$((total_servers - keep_count))
  
  # Show which servers will be removed (first N from reverse sorted = highest numbers)
  echo
  echo -e "${YELLOW}Will remove (from highest to lowest):${NC}"
  for ((i=0; i<remove_count; i++)); do
    echo "  server-${sorted_servers[$i]}"
  done
  echo
  echo -e "${GREEN}Will keep:${NC}"
  for ((i=remove_count; i<total_servers; i++)); do
    echo "  server-${sorted_servers[$i]}"
  done
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    # Remove from highest to lowest
    for ((i=0; i<remove_count; i++)); do
      local num="${sorted_servers[$i]}"
      echo "Stopping and removing server-$num..."
      sudo ./scripts/cs2_tmux.sh stop "$num" 2>/dev/null || true
      sudo rm -rf "/home/cs2/server-$num"
      echo "✓ Server-$num removed"
    done
    echo
    echo -e "${GREEN}Reduction complete. Remaining servers: $keep_count${NC}"
  else
    echo "Cancelled."
  fi
  press_enter
}

# 11. List all server directories
list_servers() {
  show_header
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo -e "${BLUE}  Server Directories${NC}"
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo
  
  if [[ ! -d "/home/cs2" ]]; then
    echo "No CS2 installation found."
    press_enter
    return
  fi
  
  echo "Master Installation:"
  if [[ -d "/home/cs2/master-install" ]]; then
    local size=$(du -sh /home/cs2/master-install 2>/dev/null | cut -f1)
    echo "  /home/cs2/master-install ($size)"
  else
    echo "  Not found"
  fi
  
  echo
  echo "Config Template:"
  if [[ -d "/home/cs2/cs2-config" ]]; then
    local size=$(du -sh /home/cs2/cs2-config 2>/dev/null | cut -f1)
    echo "  /home/cs2/cs2-config ($size)"
  else
    echo "  Not found"
  fi
  
  echo
  echo "Server Instances:"
  local count=0
  for dir in /home/cs2/server-*; do
    if [[ -d "$dir" ]]; then
      local num=$(basename "$dir" | grep -oP 'server-\K[0-9]+')
      local size=$(du -sh "$dir" 2>/dev/null | cut -f1)
      local port=$((27015 + (num - 1) * 10))
      echo "  Server $num: $dir ($size, port $port)"
      ((count++))
    fi
  done
  
  if [[ $count -eq 0 ]]; then
    echo "  No servers found"
  fi
  
  echo
  echo "Total servers: $count"
  press_enter
}

# 12. Update CS2 game
update_cs2() {
  show_header
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo -e "${YELLOW}  Update CS2 Game Files (After Valve Update)${NC}"
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will:"
  echo "  • Update master CS2 installation via SteamCMD"
  echo "  • Stop all servers"
  echo "  • Update game files on all servers"
  echo "  • Restart all servers"
  echo
  echo "Your configs and plugins will be preserved."
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    sudo ./scripts/update.sh game
  else
    echo "Cancelled."
  fi
  press_enter
}

# 7. Update plugins
update_plugins() {
  show_header
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo -e "${YELLOW}  Update Plugins${NC}"
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will:"
  echo "  • Download latest plugins"
  echo "  • Stop all servers"
  echo "  • Update plugins on all servers"
  echo "  • Restart all servers"
  echo
  echo "Plugins: Metamod, CounterStrikeSharp, MatchZy, CS2-AutoUpdater"
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    sudo ./scripts/update.sh plugins-deploy
  else
    echo "Cancelled."
  fi
  press_enter
}

# 8. Apply config changes
apply_configs() {
  show_header
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo -e "${YELLOW}  Apply Configuration Changes${NC}"
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo

  if ! configure_matchzy_database; then
    return 1
  fi

  if (( MATCHZY_SKIP_DOCKER == 0 )); then
    if ! require_docker; then
      return 1
    fi
  fi

  if ! require_docker; then
    return 1
  fi

  echo "This will:"
  echo "  • Apply configs from game_files/ and overrides/"
  echo "  • Update all server instances"
  echo "  • Restart servers"
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    sudo env MATCHZY_SKIP_DOCKER="${MATCHZY_SKIP_DOCKER:-0}" ./scripts/bootstrap_cs2.sh
    echo
    echo "Restarting servers..."
    sudo ./scripts/cs2_tmux.sh restart
  else
    echo "Cancelled."
  fi
  press_enter
}

# 16. Debug mode (run server in foreground)
debug_mode() {
  show_header
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Debug Mode - Run Server in Foreground${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will run a server in your current terminal window."
  echo "You'll see all output directly - perfect for troubleshooting!"
  echo
  echo -e "${YELLOW}Press Ctrl+C to stop the server when done.${NC}"
  echo
  echo -n "Enter server number to debug: "
  read -r server_num
  
  if [[ "$server_num" =~ ^[0-9]+$ ]]; then
    echo
    echo "Starting server $server_num in DEBUG mode..."
    echo
    sudo ./scripts/cs2_tmux.sh debug "$server_num"
  else
    echo -e "${RED}Invalid server number${NC}"
  fi
  press_enter
}

# 17. View server logs
view_server_logs() {
  show_header
  echo -e "${BLUE}View Server Logs${NC}"
  echo
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ ! "$server_num" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}Invalid server number${NC}"
    press_enter
    return
  fi
  
  echo -n "How many lines to show? [50]: "
  read -r lines
  lines=${lines:-50}
  
  echo
  sudo ./scripts/cs2_tmux.sh logs "$server_num" "$lines"
  press_enter
}

# 18. Attach to console
attach_console() {
  show_header
  echo -e "${BLUE}Attach to which server console?${NC}"
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ "$server_num" =~ ^[0-9]+$ ]]; then
    echo
    echo -e "${CYAN}Attaching to server $server_num console...${NC}"
    echo -e "${CYAN}Press Ctrl+B then D to detach (server keeps running)${NC}"
    sleep 2
    sudo ./scripts/cs2_tmux.sh attach "$server_num"
  else
    echo -e "${RED}Invalid server number${NC}"
    press_enter
  fi
}

# 19. List tmux sessions
list_tmux_sessions() {
  show_header
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo -e "${BLUE}  Active Tmux Sessions${NC}"
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo
  sudo ./scripts/cs2_tmux.sh list
  echo
  press_enter
}

# 20. Execute RCON command
execute_rcon() {
  show_header
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Execute RCON Command${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo -n "Enter server number: "
  read -r server_num
  
  if [[ ! "$server_num" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}Invalid server number${NC}"
    press_enter
    return
  fi
  
  local port=$((27015 + (server_num - 1) * 10))
  
  echo -n "Enter RCON password [ntlan2025]: "
  read -r rcon_pass
  rcon_pass=${rcon_pass:-ntlan2025}
  
  echo -n "Enter command to execute: "
  read -r command
  
  if [[ -z "$command" ]]; then
    echo -e "${RED}No command entered${NC}"
    press_enter
    return
  fi
  
  echo
  echo "Executing on localhost:$port..."
  echo "Command: $command"
  echo
  
  if command -v mcrcon >/dev/null 2>&1; then
    mcrcon -H localhost -P "$port" -p "$rcon_pass" "$command"
  else
    echo -e "${RED}mcrcon not installed${NC}"
    echo "Install with: sudo apt-get install mcrcon"
  fi
  
  echo
  press_enter
}

# 10. Test RCON
test_rcon() {
  show_header
  echo -e "${BLUE}Test RCON Connection${NC}"
  echo
  echo "Default RCON password: ntlan2025"
  echo
  echo -n "Enter server IP [localhost]: "
  read -r rcon_host
  rcon_host=${rcon_host:-localhost}
  
  echo -n "Enter server port [27015]: "
  read -r rcon_port
  rcon_port=${rcon_port:-27015}
  
  echo
  echo "Testing RCON connection to $rcon_host:$rcon_port..."
  echo
  
  if command -v mcrcon >/dev/null 2>&1; then
    mcrcon -H "$rcon_host" -P "$rcon_port" -p ntlan2025 "status"
  else
    echo -e "${RED}mcrcon not installed${NC}"
    echo "Install with: sudo apt-get install mcrcon"
  fi
  
  press_enter
}

# 11. View logs
view_logs() {
  show_header
  echo -e "${BLUE}View logs for which server?${NC}"
  echo -n "Enter server number (1-5): "
  read -r server_num
  
  if [[ "$server_num" =~ ^[1-5]$ ]]; then
    LOG_DIR="/home/cs2/server-$server_num/game/csgo/logs"
    if [[ -d "$LOG_DIR" ]]; then
      echo
      echo "Recent log files:"
      ls -lht "$LOG_DIR" | head -n 10
      echo
      echo -n "Enter log filename to view (or press Enter to skip): "
      read -r logfile
      if [[ -n "$logfile" && -f "$LOG_DIR/$logfile" ]]; then
        less "$LOG_DIR/$logfile"
      fi
    else
      echo -e "${RED}Log directory not found${NC}"
    fi
  else
    echo -e "${RED}Invalid server number${NC}"
  fi
  press_enter
}

# 13. Repair servers
repair_servers() {
  show_header
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo -e "${YELLOW}  Repair CS2 Servers${NC}"
  echo -e "${YELLOW}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will:"
  echo "  1. Stop all servers"
  echo "  2. Validate master installation (verify files, no re-download)"
  echo "  3. Download latest plugins"
  echo "  4. Re-apply all plugins and configs to servers"
  echo "  5. Fix Metamod configuration"
  echo "  6. Fix Steam SDK symlinks"
  echo "  7. Restart all servers"
  echo
  echo "This does NOT re-download the 60GB CS2 files unless corrupted."
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    echo "=== Step 1/7: Stopping servers ==="
    sudo ./scripts/cs2_tmux.sh stop
    sleep 2
    
    echo
    echo "=== Step 2/7: Validating master installation ==="
    echo "This may take a few minutes..."
    sudo -u cs2 bash -c "steamcmd +force_install_dir /home/cs2/master-install +login anonymous +app_update 730 validate +quit" || {
      echo -e "${RED}Master validation failed${NC}"
      press_enter
      return 1
    }
    
    echo
    echo "=== Step 3/7: Downloading latest plugins ==="
    local plugin_output
    local plugin_exit_code
    
    plugin_output=$(./scripts/update.sh plugins 2>&1)
    plugin_exit_code=$?
    
    echo "$plugin_output"
    
    if [[ $plugin_exit_code -ne 0 ]]; then
      echo
      echo -e "${RED}Plugin download failed with exit code: ${plugin_exit_code}${NC}"
      echo
      echo "To debug manually, run:"
      echo "  cd $(pwd) && ./scripts/update.sh plugins"
      press_enter
      return 1
    fi
    
    echo
    echo "=== Step 4/7: Re-applying plugins and configs ==="
    UPDATE_MASTER=0 ENABLE_METAMOD=1 sudo -E ./scripts/bootstrap_cs2.sh || {
      echo -e "${RED}Bootstrap failed${NC}"
      press_enter
      return 1
    }
    
    echo
    echo "=== Step 5/7: Fixing Metamod configuration ==="
    # Metamod is configured during bootstrap, just verify
    if grep -q "csgo/addons/metamod" /home/cs2/server-1/game/csgo/gameinfo.gi; then
      echo "✓ Metamod configuration verified"
    else
      echo -e "${YELLOW}! Metamod configuration might need manual check${NC}"
    fi
    
    echo
    echo "=== Step 6/7: Fixing Steam SDK ==="
    sudo ./scripts/fix_steam_sdk.sh || true
    
    echo
    echo "=== Step 7/7: Starting servers ==="
    sudo ./scripts/cs2_tmux.sh start
    sleep 3
    
    echo
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Repair Complete!${NC}"
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo
    echo "Check server status:"
    echo "  sudo ./scripts/cs2_tmux.sh status"
    echo
    echo "View server console:"
    echo "  sudo ./scripts/cs2_tmux.sh attach 1"
    echo
  else
    echo "Cancelled."
  fi
  press_enter
}

# 16. Install/reinstall auto-update monitor
install_auto_update_monitor() {
  show_header
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo -e "${BLUE}  Install Auto-Update Monitor${NC}"
  echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This will set up a cronjob that:"
  echo "  • Runs every 5 minutes"
  echo "  • Checks if all servers are shut down"
  echo "  • Looks for AutoUpdater shutdown message in logs"
  echo "  • Automatically triggers game updates when detected"
  echo "  • Restarts servers after update"
  echo
  echo "The cronjob will be added to root's crontab."
  echo
  echo -n "Continue? (y/N): "
  read -r confirm
  
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    echo
    echo -e "${BLUE}[INFO]${NC} Installing auto-update monitor..."
    
    # Make monitor script executable
    chmod +x ./scripts/auto_update_monitor.sh
    echo -e "${GREEN}✓${NC} Made monitor script executable"
    
    # Create log file
    sudo touch /var/log/cs2_auto_update_monitor.log 2>/dev/null || true
    sudo chmod 644 /var/log/cs2_auto_update_monitor.log 2>/dev/null || true
    echo -e "${GREEN}✓${NC} Created log file"
    
    # Set up cronjob
    MONITOR_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/auto_update_monitor.sh"
    CRON_COMMAND="${MONITOR_SCRIPT} >> /var/log/cs2_auto_update_monitor.log 2>&1"
    CRON_LINE="*/5 * * * * $CRON_COMMAND"
    
    # Remove old cronjob if exists and add new one
    (sudo crontab -l 2>/dev/null | grep -v "auto_update_monitor.sh" || true; echo "$CRON_LINE") | sudo crontab -
    
    echo -e "${GREEN}✓${NC} Cronjob installed (runs every 5 minutes)"
    echo
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Installation Complete!${NC}"
    echo -e "${GREEN}════════════════════════════════════════════════════════${NC}"
    echo
    echo "Monitor settings:"
    echo "  Schedule: Every 5 minutes (*/5 * * * *)"
    echo "  Log file: /var/log/cs2_auto_update_monitor.log"
    echo
    echo "Verify installation:"
    echo "  sudo crontab -l | grep auto_update_monitor"
    echo
    echo "View logs:"
    echo "  sudo tail -f /var/log/cs2_auto_update_monitor.log"
    echo
    echo "The monitor will automatically update servers when"
    echo "AutoUpdater shuts them down for game updates."
  else
    echo "Cancelled."
  fi
  press_enter
}

# 22. Cleanup everything
cleanup_all() {
  show_header
  echo -e "${RED}════════════════════════════════════════════════════════${NC}"
  echo -e "${RED}  WARNING: This will delete ALL CS2 data${NC}"
  echo -e "${RED}════════════════════════════════════════════════════════${NC}"
  echo
  echo "This removes:"
  echo "  • /home/cs2/master-install"
  echo "  • /home/cs2/server-* directories"
  echo "  • /home/cs2/cs2-config"
  echo "  • tmux sessions and systemd services"
  echo "  • Optionally the cs2 user"
  echo
  echo -n "Continue? (type DELETE to confirm): "
  read -r confirm
  if [[ "$confirm" == "DELETE" ]]; then
    echo
    sudo ./scripts/cleanup_cs2.sh
    echo
    echo "Cleanup complete."
  else
    echo "Cancelled."
  fi
  press_enter
}

# Main loop
main() {
  while true; do
    show_header
    show_menu
    read -r choice
    
    case $choice in
      1) install_servers ;;
      2) show_status ;;
      3) start_all ;;
      4) stop_all ;;
      5) restart_all ;;
      6) start_single ;;
      7) stop_single ;;
      8) restart_single ;;
      9) remove_specific_server ;;
      10) reduce_servers ;;
      11) list_servers ;;
      12) update_cs2 ;;
      13) update_plugins ;;
      14) apply_configs ;;
      15) repair_servers ;;
      16) install_auto_update_monitor ;;
      17) debug_mode ;;
      18) view_server_logs ;;
      19) attach_console ;;
      20) list_tmux_sessions ;;
      21) execute_rcon ;;
      22) cleanup_all ;;
      0) 
        show_header
        echo "Goodbye!"
        exit 0
        ;;
      *)
        echo -e "${RED}Invalid option${NC}"
        sleep 1
        ;;
    esac
  done
}

# Command-line mode (non-interactive)
if [[ $# -gt 0 ]]; then
  case "$1" in
    install|1)
      install_servers 1  # Pass 1 for auto-yes mode
      ;;
    status|2)
      show_status
      ;;
    start|3)
      start_all
      ;;
    stop|4)
      stop_all
      ;;
    restart|5)
      restart_all
      ;;
    update-game|12)
      update_cs2
      ;;
    update-plugins|13)
      update_plugins
      ;;
    repair|15)
      repair_servers
      ;;
    help|--help|-h)
      echo "CS2 Server Manager - Command-line usage"
      echo
      echo "Usage: $0 [command]"
      echo
      echo "Commands:"
      echo "  install           Install/redeploy servers (uses defaults, auto-confirms)"
      echo "  status            Show server status"
      echo "  start             Start all servers"
      echo "  stop              Stop all servers"
      echo "  restart           Restart all servers"
      echo "  update-game       Update CS2 game files"
      echo "  update-plugins    Update plugins"
      echo "  repair            Repair servers"
      echo "  help              Show this help message"
      echo
      echo "If no command is provided, launches interactive menu."
      echo
      exit 0
      ;;
    *)
      echo -e "${RED}Unknown command: $1${NC}"
      echo "Run '$0 help' for usage information"
      exit 1
      ;;
  esac
  exit $?
fi

# Interactive mode
main

