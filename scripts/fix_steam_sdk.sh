#!/usr/bin/env bash
set -euo pipefail

###############################################################################
# Fix Steam SDK Symlinks for CS2 Server
# 
# This script creates the necessary .steam/sdk64 directory and symlinks
# to fix the "steamclient.so: cannot open shared object file" error
###############################################################################

CS2_USER="${CS2_USER:-cs2}"

# Check if running as root
if [[ $EUID -ne 0 ]]; then
  echo "Error: This script must be run as root (use sudo)"
  exit 1
fi

echo "=========================================="
echo "  CS2 Steam SDK Symlink Fix"
echo "=========================================="
echo

STEAM_DIR="/home/${CS2_USER}/.steam"
SDK64_DIR="${STEAM_DIR}/sdk64"
STEAMCLIENT_SRC="/home/${CS2_USER}/.local/share/Steam/steamcmd/linux64/steamclient.so"

echo "[*] Setting up Steam SDK symlinks for ${CS2_USER}..."

# Create .steam/sdk64 directory
mkdir -p "$SDK64_DIR"

# Create symlink to steamclient.so
if [[ -f "$STEAMCLIENT_SRC" ]]; then
  ln -sf "$STEAMCLIENT_SRC" "${SDK64_DIR}/steamclient.so"
  echo "[✓] Steam SDK symlink created:"
  echo "    ${SDK64_DIR}/steamclient.so -> ${STEAMCLIENT_SRC}"
else
  echo "[!] Error: steamclient.so not found at ${STEAMCLIENT_SRC}"
  echo "    Make sure SteamCMD has been run at least once"
  exit 1
fi

# Fix ownership
chown -R "${CS2_USER}:${CS2_USER}" "$STEAM_DIR"
echo "[✓] Ownership fixed for ${STEAM_DIR}"

echo
echo "=========================================="
echo "  Fix Applied Successfully!"
echo "=========================================="
echo
echo "Verification:"
ls -lh "${SDK64_DIR}/steamclient.so"
echo
echo "Now try starting the server:"
echo "  ./manage.sh → Option 16 (Debug mode)"
echo

