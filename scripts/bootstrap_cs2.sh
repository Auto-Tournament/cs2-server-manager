#!/usr/bin/env bash
set -euo pipefail
trap 'echo "[!] Error on line $LINENO. See log: $LOG_FILE"; exit 1' ERR

###############################################################################
# CS2 Server Bootstrap (SteamCMD direct + shared config)
# - Creates one 'cs2' user
# - Installs CS2 via SteamCMD once to master-install
# - Copies master to individual server folders (server-1, server-2, etc.)
# - Creates shared config directory with overlay from source repo
# - Overlays config to each server instance
# - Configures Metamod in gameinfo.gi per instance
# - Use scripts/cs2_tmux.sh to start/stop/manage servers
###############################################################################

# ----------------------------- Configuration ---------------------------------
CS2_USER="${CS2_USER:-cs2}"                # single user for all servers
CS2_APP_ID="730"                           # CS2 Steam App ID

# Auto-detect number of servers or default to 3
if [[ -z "${NUM_SERVERS:-}" ]]; then
  # Count existing server directories
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

BASE_GAME_PORT="${BASE_GAME_PORT:-27015}"  # UDP (RCON uses same port on TCP)
BASE_TV_PORT="${BASE_TV_PORT:-27020}"      # UDP GOTV

DEFAULT_START_PARAMS="${DEFAULT_START_PARAMS:--insecure -tickrate 128 +sv_lan 0 +game_type 0 +game_mode 1 +game_alias \"MatchZy\"}"
RCON_PASSWORD="${RCON_PASSWORD:-ntlan2025}"

# Source directories for config overlay (defaults set after PROJECT_ROOT is determined)
GAME_FILES_DIR="${GAME_FILES_DIR:-}"
OVERRIDES_DIR="${OVERRIDES_DIR:-}"

# Enable/Disable Metamod in gameinfo.gi (1=add line, 0=remove line if present)
ENABLE_METAMOD="${ENABLE_METAMOD:-1}"

# Delete master and all servers before install (1) for fresh install, (0) to keep existing
FRESH_INSTALL="${FRESH_INSTALL:-0}"

# Update master install before copying to servers (1) or skip if exists (0)
UPDATE_MASTER="${UPDATE_MASTER:-1}"

# ------------------------------- Logging --------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Update defaults now that PROJECT_ROOT is known
GAME_FILES_DIR="${GAME_FILES_DIR:-${PROJECT_ROOT}/game_files}"
OVERRIDES_DIR="${OVERRIDES_DIR:-${PROJECT_ROOT}/overrides}"
MATCHZY_DB_CONFIG="${OVERRIDES_DIR}/game/csgo/cfg/MatchZy/database.json"
MATCHZY_DB_CONTAINER="${MATCHZY_DB_CONTAINER:-matchzy-mysql}"
MATCHZY_DB_VOLUME="${MATCHZY_DB_VOLUME:-matchzy-mysql-data}"
MATCHZY_DB_IMAGE="${MATCHZY_DB_IMAGE:-mysql:8.0}"
MATCHZY_DB_ROOT_PASSWORD="${MATCHZY_DB_ROOT_PASSWORD:-MatchZyRoot!2025}"
MATCHZY_SKIP_DOCKER="${MATCHZY_SKIP_DOCKER:-0}"

LOG_FILE="${SCRIPT_DIR}/bootstrap_cs2_$(date +%Y%m%d_%H%M%S).log"
log(){ echo "$@" | tee -a "$LOG_FILE"; }
exec > >(tee -a "$LOG_FILE") 2>&1

# ------------------------------- Root check -----------------------------------
if [[ $EUID -ne 0 ]]; then echo "Please run as root (sudo)."; exit 1; fi

log "=== Bootstrap started at $(date) ==="
log "Log file: ${LOG_FILE}"
echo "=== CS2 Server Bootstrap (SteamCMD Direct) ==="
echo "Servers to create : ${NUM_SERVERS}"
echo "CS2 user          : ${CS2_USER}"
echo "Plugin source     : ${GAME_FILES_DIR}"
echo "Custom overrides  : ${OVERRIDES_DIR}"
echo "Enable Metamod    : ${ENABLE_METAMOD}"
echo "Fresh install     : ${FRESH_INSTALL}"
echo "Update master     : ${UPDATE_MASTER}"
echo

# ------------------------------ Dependencies ----------------------------------
echo "[*] Installing dependencies..."
if command -v apt-get >/dev/null 2>&1; then
  dpkg --add-architecture i386 || true
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl wget file tar bzip2 xz-utils unzip ca-certificates \
    lib32gcc-s1 lib32stdc++6 libc6-i386 net-tools tmux steamcmd rsync \
    jq >/dev/null
else
  echo "Only Debian/Ubuntu supported for now"; exit 1
fi

# Ensure /usr/bin/steamcmd is a working wrapper to /usr/games/steamcmd
if [[ -L /usr/bin/steamcmd || ! -x /usr/bin/steamcmd ]]; then
  rm -f /usr/bin/steamcmd
  cat >/usr/bin/steamcmd <<'EOS'
#!/usr/bin/env bash
exec /usr/games/steamcmd "$@"
EOS
  chmod 0755 /usr/bin/steamcmd
fi

# ------------------------------- Functions ------------------------------------

create_cs2_user() {
  local user="$1"
  if id -u "$user" >/dev/null 2>&1; then
    echo "  [i] User ${user} already exists"
  else
    if getent group "$user" >/dev/null 2>&1; then
      echo "  [*] Creating user ${user} with existing group ${user}"
      useradd -m -s /bin/bash -g "$user" "$user"
    else
      echo "  [*] Creating user ${user} and matching group"
      useradd -m -s /bin/bash -U "$user"
    fi
    loginctl enable-linger "$user" 2>/dev/null || true
    echo "  [✓] User ${user} created"
  fi
  mkdir -p "/home/${user}"
  chown -R "${user}:${user}" "/home/${user}"
  chmod 755 "/home/${user}"
  
  # Ensure /usr/games is in PATH for steamcmd
  if ! grep -q '/usr/games' "/home/${user}/.bashrc" 2>/dev/null; then
    echo 'export PATH="/usr/games:$PATH"' >> "/home/${user}/.bashrc"
    chown "${user}:${user}" "/home/${user}/.bashrc"
  fi
}

setup_steam_sdk_links() {
  local user="$1"
  local steam_dir="/home/${user}/.steam"
  local sdk64_dir="${steam_dir}/sdk64"
  local steamclient_src="/home/${user}/.local/share/Steam/steamcmd/linux64/steamclient.so"
  
  echo "  [*] Setting up Steam SDK symlinks for ${user}"
  
  # Create .steam/sdk64 directory
  mkdir -p "$sdk64_dir"
  chown -R "${user}:${user}" "$steam_dir"
  
  # Create symlink to steamclient.so
  if [[ -f "$steamclient_src" ]]; then
    ln -sf "$steamclient_src" "${sdk64_dir}/steamclient.so"
    echo "  [✓] Steam SDK symlink created: ${sdk64_dir}/steamclient.so -> ${steamclient_src}"
  else
    echo "  [!] Warning: steamclient.so not found at ${steamclient_src}"
    echo "      This will be created after first SteamCMD run"
  fi
  
  chown -R "${user}:${user}" "$steam_dir"
}

install_master_via_steamcmd() {
  local user="$1"
  local master_dir="/home/${user}/master-install"
  local gameinfo="${master_dir}/game/csgo/gameinfo.gi"
  
  if (( FRESH_INSTALL == 1 )) && [[ -d "$master_dir" ]]; then
    echo "  [*] FRESH_INSTALL=1: Deleting existing master install"
    rm -rf "$master_dir"
  fi
  
  if [[ -f "$gameinfo" ]] && (( UPDATE_MASTER == 0 )); then
    echo "  [i] Master install exists and UPDATE_MASTER=0, skipping"
    return 0
  fi
  
  if [[ -f "$gameinfo" ]] && (( UPDATE_MASTER == 1 )); then
    echo "  [*] Updating existing master install"
  else
    echo "  [*] Installing fresh CS2 master to ${master_dir}"
  fi
  
  mkdir -p "$master_dir"
  chown "${user}:${user}" "$master_dir"
  
  # Create .steam directory structure before SteamCMD runs (prevents symlink errors)
  local steam_dir="/home/${user}/.steam"
  mkdir -p "$steam_dir"
  chown -R "${user}:${user}" "$steam_dir"
  
  # Run SteamCMD as the cs2 user
  su - "$user" -c "
    set -e
    steamcmd +force_install_dir \"${master_dir}\" \
             +login anonymous \
             +app_update ${CS2_APP_ID} validate \
             +quit
  "
  
  if [[ -f "$gameinfo" ]]; then
    echo "  [✓] Master install complete/updated"
  else
    echo "  [!] Master install failed - gameinfo.gi not found"
    return 1
  fi
}

setup_shared_config() {
  local user="$1"
  local config_dir="/home/${user}/cs2-config"
  
  echo "  [*] Setting up shared config directory at ${config_dir}"
  mkdir -p "${config_dir}/game"
  
  # First, copy clean plugin files from game_files
  if [[ -d "${GAME_FILES_DIR}/game" ]]; then
    echo "  [*] Copying plugin files from game_files/"
    rsync -a --delete \
          --exclude '.git/' \
          "${GAME_FILES_DIR}/game/" "${config_dir}/game/" || {
      echo "  [!] rsync game_files failed"
      return 1
    }
  else
    echo "  [!] game_files/game not found - run ./manage.sh and choose 'Install servers' to download plugins"
  fi
  
  # Then, overlay custom configs from overrides (never delete, only add/update)
  if [[ -d "${OVERRIDES_DIR}/game" ]]; then
    echo "  [*] Applying custom overrides from overrides/"
    rsync -a \
          --exclude '.git/' \
          "${OVERRIDES_DIR}/game/" "${config_dir}/game/" || {
      echo "  [!] rsync overrides failed"
      return 1
    }
  else
    echo "  [i] No overrides found at overrides/game/"
  fi
  
  chown -R "${user}:${user}" "$config_dir"
  echo "  [✓] Shared config ready at ${config_dir}/game"
  echo "  [✓] Plugins from: game_files/, Custom configs from: overrides/"
}

copy_master_to_server() {
  local user="$1"
  local server_num="$2"
  local master_dir="/home/${user}/master-install"
  local server_dir="/home/${user}/server-${server_num}"
  
  if (( FRESH_INSTALL == 1 )) && [[ -d "$server_dir" ]]; then
    echo "  [*] FRESH_INSTALL=1: Deleting existing server-${server_num}"
    rm -rf "$server_dir"
  fi
  
  if [[ -d "$server_dir" ]]; then
    echo "  [i] Server ${server_num} already exists, skipping copy"
    return 0
  fi
  
  echo "  [*] Copying master to server-${server_num}"
  
  # Use rsync for efficient copy with progress
  rsync -a --info=PROGRESS2 "$master_dir/" "$server_dir/" || {
    echo "  [!] Copy failed for server-${server_num}"
    return 1
  }
  
  chown -R "${user}:${user}" "$server_dir"
  echo "  [✓] Server-${server_num} copied from master"
}

overlay_config_to_server() {
  local user="$1"
  local server_num="$2"
  local config_dir="/home/${user}/cs2-config/game"
  local server_dir="/home/${user}/server-${server_num}/game"
  
  if [[ ! -d "$config_dir" ]]; then
    echo "  [i] No shared config to overlay for server-${server_num}"
    return 0
  fi
  
  echo "  [*] Overlaying shared config to server-${server_num}"
  
  rsync -a \
        --exclude '.git/' \
        "$config_dir/" "$server_dir/" || {
    echo "  [!] Config overlay failed for server-${server_num}"
    return 1
  }
  
  chown -R "${user}:${user}" "$server_dir"
  echo "  [✓] Config overlaid to server-${server_num}"
}

configure_metamod() {
  local user="$1"
  local server_num="$2"
  local gameinfo="/home/${user}/server-${server_num}/game/csgo/gameinfo.gi"
  
  if [[ ! -f "$gameinfo" ]]; then
    echo "  [!] gameinfo.gi not found for server-${server_num} — cannot configure Metamod"
    return
  fi
  
  cp "$gameinfo" "${gameinfo}.backup" || true
  
  if (( ENABLE_METAMOD == 1 )); then
    echo "  [*] Enabling Metamod in gameinfo.gi for server-${server_num}"
    if ! grep -q "csgo/addons/metamod" "$gameinfo"; then
      sed -i '/Game_LowViolence.*csgo_lv/a\                        Game    csgo/addons/metamod' "$gameinfo"
      echo "  [✓] Metamod path added for server-${server_num}"
    else
      echo "  [i] Metamod already enabled for server-${server_num}"
    fi
  else
    echo "  [*] Disabling Metamod in gameinfo.gi for server-${server_num}"
    if grep -q "csgo/addons/metamod" "$gameinfo"; then
      sed -i '/csgo\/addons\/metamod/d' "$gameinfo"
      echo "  [✓] Metamod path removed for server-${server_num}"
    else
      echo "  [i] Metamod already disabled for server-${server_num}"
    fi
  fi
  
  chown "${user}:${user}" "$gameinfo" "${gameinfo}.backup" || true
}

customize_server_cfg() {
  local user="$1"
  local server_num="$2"
  local cfg_dir="/home/${user}/server-${server_num}/game/csgo/cfg"
  local server_cfg="${cfg_dir}/server.cfg"
  local autoexec_cfg="${cfg_dir}/autoexec.cfg"
  
  # Calculate ports for this server
  local game_port=$(( BASE_GAME_PORT + (server_num-1)*10 ))
  local tv_port=$(( BASE_TV_PORT + (server_num-1)*10 ))
  
  mkdir -p "$cfg_dir"
  
  echo "  [*] Customizing configs for server-${server_num}"
  
  # Update or create server.cfg
  if [[ -f "$server_cfg" ]]; then
    # Update hostname with server index
    sed -i "s/hostname \".*\"/hostname \"NTLAN CS2 Server #${server_num}\"/" "$server_cfg"
    
    # Remove any existing rcon_password line
    sed -i '/^rcon_password/d' "$server_cfg"
    
    # Add rcon_password at the top
    sed -i "1i rcon_password \"${RCON_PASSWORD}\"" "$server_cfg"
    echo "  [✓] Updated server.cfg"
  else
    echo "  [i] Creating server.cfg"
    cat > "$server_cfg" <<EOF
// ========================================
// RCON Configuration
// ========================================
rcon_password "${RCON_PASSWORD}"
ip "0.0.0.0"                    // Bind to all network interfaces

// ========================================
// Server Identity
// ========================================
hostname "NTLAN CS2 Server #${server_num}"

// ========================================
// Logging
// ========================================
log on
sv_logbans 1
sv_logecho 1
sv_logfile 1
sv_log_onefile 0

// ========================================
// Network Settings
// ========================================
sv_lan 0                        // Set to 0 for internet, 1 for LAN-only
sv_password ""                  // Server password (empty = public)

// ========================================
// Server Performance
// ========================================
sv_maxrate 0                    // Max bandwidth (0 = unlimited)
sv_minrate 196608              // Min bandwidth
sv_maxcmdrate 128              // Max command rate (match tickrate)
sv_mincmdrate 64               // Min command rate
sv_hibernate_when_empty 0      // Never hibernate (required for CS2-AutoUpdater)
EOF
    echo "  [✓] Created server.cfg"
  fi
  
  # Always create/overwrite autoexec.cfg with RCON password
  cat > "$autoexec_cfg" <<EOF
// ===================================================
// Auto-executed on server startup
// ===================================================

// RCON Configuration (ensures it's always set)
rcon_password "${RCON_PASSWORD}"
ip "0.0.0.0"

// Server Identity
hostname "NTLAN CS2 Server #${server_num}"

// Start warmup mode
startwarmup

// Startup message
echo "==========================================="
echo " NTLAN CS2 Server #${server_num}"
echo " Port: Game ${game_port}, TV ${tv_port}"
echo " RCON: Enabled on port ${game_port} (TCP)"
echo " RCON Password: ${RCON_PASSWORD}"
echo "==========================================="
EOF
  
  chown -R "${user}:${user}" "$cfg_dir"
  chmod 644 "$server_cfg" "$autoexec_cfg" 2>/dev/null || true
  
  echo "  [✓] Server #${server_num} configured (server.cfg + autoexec.cfg)"
}

detect_primary_ip() {
  local ip
  ip=$(ip route get 1.1.1.1 2>/dev/null | awk '/src/ {print $7; exit}')
  ip=${ip:-$(hostname -I 2>/dev/null | awk '{print $1}')}
  ip=${ip:-127.0.0.1}
  echo "$ip"
}

ensure_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "  [!] Docker is required for the MatchZy database. Please install Docker Engine using the official instructions:"
    echo "      https://docs.docker.com/engine/install/"
    return 1
  fi

  systemctl enable docker >/dev/null 2>&1 || true
  systemctl start docker >/dev/null 2>&1 || true
}

update_matchzy_database_json() {
  local host="$1"
  local port="$2"
  local db_name="${3:-matchzy}"
  local db_user="${4:-matchzy}"
  local db_pass="${5:-matchzy}"
  local orig_owner=""
  local orig_mode=""

  # Create directory if it doesn't exist
  mkdir -p "$(dirname "$MATCHZY_DB_CONFIG")"

  if [[ -f "$MATCHZY_DB_CONFIG" ]]; then
    orig_owner=$(stat -c '%u:%g' "$MATCHZY_DB_CONFIG" 2>/dev/null || echo "")
    orig_mode=$(stat -c '%a' "$MATCHZY_DB_CONFIG" 2>/dev/null || echo "")
  fi

  local tmp
  tmp=$(mktemp)
  
  # Update or create the JSON file with all required fields
  if [[ -f "$MATCHZY_DB_CONFIG" ]]; then
    # Update existing file
    jq --arg host "$host" \
       --argjson port "$port" \
       --arg db "$db_name" \
       --arg user "$db_user" \
       --arg pass "$db_pass" '
      .DatabaseType = "MySQL" |
      .MySqlHost = $host |
      .MySqlPort = $port |
      .MySqlDatabase = $db |
      .MySqlUsername = $user |
      .MySqlPassword = $pass
    ' "$MATCHZY_DB_CONFIG" > "$tmp"
  else
    # Create new file with all fields
    cat > "$tmp" <<EOF
{
  "DatabaseType": "MySQL",
  "MySqlHost": "$host",
  "MySqlPort": $port,
  "MySqlDatabase": "$db_name",
  "MySqlUsername": "$db_user",
  "MySqlPassword": "$db_pass"
}
EOF
  fi
  
  mv "$tmp" "$MATCHZY_DB_CONFIG"

  if [[ -n "${SUDO_USER:-}" ]]; then
    chown "${SUDO_USER}:${SUDO_USER}" "$MATCHZY_DB_CONFIG" 2>/dev/null || true
  elif [[ -n "$orig_owner" ]]; then
    chown "$orig_owner" "$MATCHZY_DB_CONFIG" 2>/dev/null || true
  fi

  if [[ -n "$orig_mode" ]]; then
    chmod "$orig_mode" "$MATCHZY_DB_CONFIG" 2>/dev/null || true
  else
    chmod 664 "$MATCHZY_DB_CONFIG" 2>/dev/null || true
  fi
}

setup_matchzy_database() {
  # Create database.json with defaults if it doesn't exist
  if [[ ! -f "$MATCHZY_DB_CONFIG" ]]; then
    echo "  [i] MatchZy database config not found, creating with defaults..."
    mkdir -p "$(dirname "$MATCHZY_DB_CONFIG")"
    cat > "$MATCHZY_DB_CONFIG" <<EOF
{
  "DatabaseType": "MySQL",
  "MySqlHost": "127.0.0.1",
  "MySqlPort": 3306,
  "MySqlDatabase": "matchzy",
  "MySqlUsername": "matchzy",
  "MySqlPassword": "matchzy"
}
EOF
    echo "  [✓] Created ${MATCHZY_DB_CONFIG} with default values"
  fi

  if ! jq empty "$MATCHZY_DB_CONFIG" >/dev/null 2>&1; then
    echo "  [!] MatchZy database config is not valid JSON"
    return 1
  fi

  local db_type
  db_type=$(jq -r '.DatabaseType // "MySQL"' "$MATCHZY_DB_CONFIG" | tr '[:upper:]' '[:lower:]')
  if [[ "$db_type" != "mysql" ]]; then
    echo "  [i] DatabaseType=${db_type}; skipping Docker provisioning"
    return 0
  fi

  if [[ "${MATCHZY_SKIP_DOCKER}" == "1" ]]; then
    echo "  [i] MATCHZY_SKIP_DOCKER=1: Skipping Docker provisioning (using external database)."
    return 0
  fi

  ensure_docker || return 1

  local mysql_db mysql_user mysql_pass mysql_port
  mysql_db=$(jq -r '.MySqlDatabase // "matchzy"' "$MATCHZY_DB_CONFIG")
  mysql_user=$(jq -r '.MySqlUsername // "matchzy"' "$MATCHZY_DB_CONFIG")
  mysql_pass=$(jq -r '.MySqlPassword // "matchzy"' "$MATCHZY_DB_CONFIG")
  mysql_port=$(jq -r '.MySqlPort // 3306' "$MATCHZY_DB_CONFIG")
  if ! [[ "$mysql_port" =~ ^[0-9]+$ ]]; then
    mysql_port=3306
  fi

  local container_name="$MATCHZY_DB_CONTAINER"
  local container_exists=0
  local current_port=""

  if docker ps -a --format '{{.Names}}' | grep -Fxq "$container_name"; then
    container_exists=1
    current_port=$(docker inspect -f '{{range $p, $cfg := .NetworkSettings.Ports}}{{if eq $p "3306/tcp"}}{{(index $cfg 0).HostPort}}{{end}}{{end}}' "$container_name" 2>/dev/null || echo "")
  fi

  if ss -ltn 2>/dev/null | grep -q ":${mysql_port} "; then
    if (( container_exists == 0 )) || [[ "$current_port" != "$mysql_port" ]]; then
      echo "  [!] Port ${mysql_port} is already in use on this host."
      echo "      Update ${MATCHZY_DB_CONFIG} (MySqlPort) to a free port or point it at your existing database."
      echo "      After editing, rerun ./scripts/bootstrap_cs2.sh."
      return 1
    fi
  fi

  local host_ip
  host_ip=$(detect_primary_ip)
  # Update database.json with correct values (host, port, and ensure username/password/database match Docker container)
  update_matchzy_database_json "$host_ip" "$mysql_port" "$mysql_db" "$mysql_user" "$mysql_pass"

  local recreate=0

  if (( container_exists == 1 )); then
    if [[ "$current_port" != "$mysql_port" ]]; then
      echo "  [*] Recreating ${container_name} to use host port ${mysql_port}"
      docker rm -f "$container_name" >/dev/null
      recreate=1
    fi
  else
    recreate=1
  fi

  if (( recreate == 1 )); then
    docker pull "$MATCHZY_DB_IMAGE" >/dev/null
    docker run -d \
      --name "$container_name" \
      -e MYSQL_ROOT_PASSWORD="$MATCHZY_DB_ROOT_PASSWORD" \
      -e MYSQL_DATABASE="$mysql_db" \
      -e MYSQL_USER="$mysql_user" \
      -e MYSQL_PASSWORD="$mysql_pass" \
      -p "${mysql_port}:3306" \
      -v "${MATCHZY_DB_VOLUME}:/var/lib/mysql" \
      --restart unless-stopped \
      "$MATCHZY_DB_IMAGE" >/dev/null
    echo "  [✓] Started MatchZy MySQL container (${container_name}) on port ${mysql_port}"
  else
    docker start "$container_name" >/dev/null
    echo "  [✓] MatchZy MySQL container (${container_name}) already running"
  fi

  local ready=0
  for _ in {1..30}; do
    if docker exec "$container_name" mysqladmin ping -h "127.0.0.1" -uroot -p"$MATCHZY_DB_ROOT_PASSWORD" --silent >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done

  if (( ready == 1 )); then
    echo "  [✓] MatchZy database is ready at ${host_ip}:${mysql_port}"
  else
    echo "  [i] MatchZy database is starting up (Docker container is running)"
  fi

  return 0
}

stop_tmux_server() {
  local server_num="$1"
  local session_name="cs2-${server_num}"
  
  # Check if tmux session exists
  if su - "$CS2_USER" -c "tmux has-session -t ${session_name} 2>/dev/null"; then
    echo "  [*] Stopping tmux session for server-${server_num}"
    su - "$CS2_USER" -c "tmux send-keys -t ${session_name} 'quit' C-m"
    sleep 2
    su - "$CS2_USER" -c "tmux kill-session -t ${session_name} 2>/dev/null" || true
  else
    echo "  [i] Server ${server_num} not running in tmux, skipping stop"
  fi
}

# --------------------------------- Main ---------------------------------------

echo "[*] Setting up ${NUM_SERVERS} CS2 servers..."
echo

# Create the single cs2 user
echo "[1/5] Creating CS2 user..."
create_cs2_user "$CS2_USER"
echo

# Install/update master via SteamCMD
echo "[2/5] Installing/updating master CS2 installation..."
install_master_via_steamcmd "$CS2_USER"
echo

# Setup Steam SDK symlinks (required for server to start)
echo "[3/5] Setting up Steam SDK symlinks..."
setup_steam_sdk_links "$CS2_USER"
echo

# Provision MatchZy database via Docker (if configured)
echo "[4/5] Provisioning MatchZy database (Docker)..."
if ! setup_matchzy_database; then
  echo "  [!] MatchZy database provisioning skipped (config missing or Docker unavailable)"
  echo "      Install Docker and rerun bootstrap if you need the built-in database."
fi
echo

# Setup shared config directory
echo "[5/5] Setting up shared configuration..."
setup_shared_config "$CS2_USER"
echo

# Now create each server instance
echo "[*] Creating ${NUM_SERVERS} server instances..."
echo

for ((i=1; i<=NUM_SERVERS; i++)); do
  game_port=$(( BASE_GAME_PORT + (i-1)*10 ))
  tv_port=$(( BASE_TV_PORT + (i-1)*10 ))

  echo "[${i}/${NUM_SERVERS}] Setting up server-${i}..."
  
  # Stop server if running in tmux (to safely modify files)
  stop_tmux_server "$i"
  
  # Copy master to server instance (skips if exists unless FRESH_INSTALL=1)
  copy_master_to_server "$CS2_USER" "$i" || true
  
  # Overlay shared config to this server instance
  overlay_config_to_server "$CS2_USER" "$i" || true
  
  # Configure Metamod (enable or disable based on ENABLE_METAMOD)
  configure_metamod "$CS2_USER" "$i"
  
  # Customize server.cfg with unique hostname and RCON
  customize_server_cfg "$CS2_USER" "$i"
  
  echo "  [✓] Server-${i} ready (port ${game_port}, TV ${tv_port})"
  echo
done

echo "=== Setup Complete ==="
echo "User              : ${CS2_USER}"
echo "Master install    : /home/${CS2_USER}/master-install"
echo "Shared config     : /home/${CS2_USER}/cs2-config/game"
echo "Server instances  : /home/${CS2_USER}/server-1 through server-${NUM_SERVERS}"
echo
echo "Server details:"
for ((i=1; i<=NUM_SERVERS; i++)); do
  game_port=$(( BASE_GAME_PORT + (i-1)*10 ))
  tv_port=$(( BASE_TV_PORT + (i-1)*10 ))
  echo "  Server ${i}: Game ${game_port}, TV ${tv_port}"
done
echo
echo "RCON password : ${RCON_PASSWORD} (override with RCON_PASSWORD=xxxxx)"
echo "Plugin source : ${GAME_FILES_DIR}/game -> /home/${CS2_USER}/cs2-config/game"
echo "Custom configs: ${OVERRIDES_DIR}/game -> /home/${CS2_USER}/cs2-config/game"
echo "Metamod       : set ENABLE_METAMOD=1 to enable, 0 to disable (currently: ${ENABLE_METAMOD})"
echo
echo "======================================"
echo "Server Management (Tmux):"
echo "======================================"
echo "Start all servers:"
echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh start"
echo
echo "Stop all servers:"
echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh stop"
echo
echo "Check status:"
echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh status"
echo
echo "Attach to console (Server 1):"
echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh attach 1"
echo "  (Press Ctrl+B then D to detach safely)"
echo
echo "Debug mode (see all output):"
echo "  sudo ${SCRIPT_DIR}/cs2_tmux.sh debug 1"
echo "======================================"
echo
echo "Next steps:"
echo "  • Run ./manage.sh for interactive menu"
echo "  • Update plugins: ./manage.sh → Option 13"
echo "  • Update game:    ./manage.sh → Option 12"
echo "  • Start servers:  ./manage.sh → Option 3"
