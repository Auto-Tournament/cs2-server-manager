#!/usr/bin/env bash

###############################################################################
# CS2 Server Cleanup Script
# Removes all CS2 servers (tmux sessions) and their files
###############################################################################

CS2_USER="${CS2_USER:-cs2}"

# Root check
if [[ $EUID -ne 0 ]]; then
  echo "Please run as root (sudo)."; exit 1
fi

echo "=== CS2 Server Cleanup ==="
echo "This will DELETE all CS2 servers and their data!"
echo

# Check if cs2 user exists
if ! id -u "$CS2_USER" >/dev/null 2>&1; then
  echo "User '${CS2_USER}' not found. Nothing to clean up."
  exit 0
fi

# Find server directories
servers=()
for server_dir in /home/${CS2_USER}/server-*; do
  [[ -d "$server_dir" ]] && servers+=("$(basename "$server_dir")")
done

echo "CS2 User: ${CS2_USER}"
echo "Home Dir: /home/${CS2_USER}"
echo

if [[ ${#servers[@]} -gt 0 ]]; then
  echo "Found servers:"
  for server in "${servers[@]}"; do
    echo "  - $server"
  done
  echo
fi

read -p "Delete user '${CS2_USER}' and ALL server data? Type 'yes' to confirm: " confirm

if [[ "$confirm" != "yes" ]]; then
  echo "Cleanup cancelled"
  exit 0
fi

echo
echo "[*] Stopping all tmux sessions..."
# Get all tmux sessions for cs2 user
tmux_sessions=$(su - "$CS2_USER" -c "tmux list-sessions 2>/dev/null | grep cs2- | cut -d: -f1" || true)

if [[ -n "$tmux_sessions" ]]; then
  while IFS= read -r session; do
    [[ -z "$session" ]] && continue
    echo "  [*] Stopping tmux session: ${session}"
    su - "$CS2_USER" -c "tmux send-keys -t ${session} 'quit' C-m 2>/dev/null" || true
    sleep 1
    su - "$CS2_USER" -c "tmux kill-session -t ${session} 2>/dev/null" || true
  done <<< "$tmux_sessions"
fi

echo "[*] Cleaning up MatchZy MySQL Docker container..."
MATCHZY_DB_CONTAINER="${MATCHZY_DB_CONTAINER:-matchzy-mysql}"
MATCHZY_DB_VOLUME="${MATCHZY_DB_VOLUME:-matchzy-mysql-data}"

if command -v docker >/dev/null 2>&1; then
  # Check if container exists
  if docker ps -a --format '{{.Names}}' | grep -Fxq "$MATCHZY_DB_CONTAINER" 2>/dev/null; then
    echo "  [*] Stopping and removing Docker container: ${MATCHZY_DB_CONTAINER}"
    docker stop "$MATCHZY_DB_CONTAINER" >/dev/null 2>&1 || true
    docker rm "$MATCHZY_DB_CONTAINER" >/dev/null 2>&1 || true
    echo "  [✓] Container removed"
    
    # Ask about volume removal
    if docker volume ls --format '{{.Name}}' | grep -Fxq "$MATCHZY_DB_VOLUME" 2>/dev/null; then
      echo
      read -p "Delete MatchZy database volume '${MATCHZY_DB_VOLUME}'? This will delete all database data. (y/N): " delete_volume
      if [[ "$delete_volume" =~ ^[Yy]$ ]]; then
        echo "  [*] Removing Docker volume: ${MATCHZY_DB_VOLUME}"
        docker volume rm "$MATCHZY_DB_VOLUME" >/dev/null 2>&1 || true
        echo "  [✓] Database volume removed"
      else
        echo "  [i] Database volume kept (can be reused on next install)"
      fi
    fi
  else
    echo "  [i] MatchZy MySQL container not found"
  fi
else
  echo "  [i] Docker not installed, skipping container cleanup"
fi

echo "[*] Removing user and home directory..."
echo "  [*] Deleting /home/${CS2_USER}"
userdel -r "$CS2_USER" 2>/dev/null || {
  rm -rf "/home/${CS2_USER}" 2>/dev/null || true
  userdel "$CS2_USER" 2>/dev/null || true
}

echo
echo "[✓] Cleanup complete!"
echo "You can now run ./manage.sh and choose Option 1 to create fresh servers"
