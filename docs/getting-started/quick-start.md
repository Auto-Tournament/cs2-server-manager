# Quick Start

This guide gets you from zero to running CS2 servers in a few minutes.

## Prerequisites

- **OS**: Ubuntu 22.04+ (or similar modern Linux distro).
- **Root / sudo access** on the machine that will host the servers.
- **Docker** installed and running (for MySQL and supporting services).
- **Enough resources** for multiple CS2 servers (CPU, RAM, and disk).

## 1. Install CSM globally and run the installer wizard

From your target server, you can install and launch the latest **prebuilt `csm` binary globally** from the [GitHub Releases](https://github.com/sivert-io/cs2-server-manager/releases/latest) page with a single command. The installer is a guided Bubble Tea TUI, so you can navigate with arrows and confirm with Enter:

```bash
arch=$(uname -m); \
case "$arch" in \
  x86_64)  asset="csm-linux-amd64" ;; \
  aarch64|arm64) asset="csm-linux-arm64" ;; \
  *) echo "Unsupported architecture: $arch" && exit 1 ;; \
esac; \
tmp=$(mktemp); \
curl -L "https://github.com/sivert-io/cs2-server-manager/releases/latest/download/$asset" -o "$tmp" && \
sudo install -m 0755 "$tmp" /usr/local/bin/csm && \
rm "$tmp" && \
sudo csm            # launches the interactive TUI installer
```

The installer wizard will:

- Install required system dependencies.
- Download CS2 server files.
- Set up Dockerized MySQL.
- Install Metamod, CounterStrikeSharp, and MatchZy (enhanced).
- Configure multiple CS2 instances with sane defaults.

## 2. Use the CSM TUI

Once installation completes, you can re-open the TUI at any time with:

```bash
sudo csm            # run all TUI actions (install, updates, status, cleanup)
```

CLI helpers such as `csm help` can be run without sudo, but for simplicity you can prefix most operational commands with `sudo` as well.

The TUI has four main tabs (use arrow keys to navigate):

- **Install** - Run install wizard, manage dependencies, set up auto-updates
- **Updates** - Update game, plugins, or server configs (RCON, maxplayers, GSLT)
- **Servers** - View status, logs, start/stop/restart, add/remove/reinstall servers
- **Tools** - Database management, map thumbnails, utilities

## 3. Common CLI one-liners

These commands are shortcuts around the TUI:

```bash
# Setup & maintenance
sudo csm install-deps           # Install core system dependencies
sudo csm install-monitor-cron   # Install cron-based auto-update monitor
sudo csm reinstall <server>     # Rebuild a corrupted server

# Server control
sudo csm status                 # Check tmux status
sudo csm start [server]         # Start all servers (or specific server)
sudo csm stop [server]          # Stop all servers (or specific server)
sudo csm restart [server]       # Restart all servers (or specific server)

# Updates
sudo csm update-game            # Update CS2 after Valve update
sudo csm update-plugins         # Update plugins
sudo csm monitor                # Run one monitor iteration

# Debugging
sudo csm attach 1               # Attach to server 1 console
sudo csm logs 1 200             # View last 200 lines of server 1 logs
sudo csm debug 1                # Run server 1 in foreground (debug mode)
```

If you keep your `overrides/` and `game_files/` in a specific directory (for example `/opt/cs2-server-manager`), you can point CSM at it explicitly:

```bash
export CSM_ROOT=/opt/cs2-server-manager
cd /opt/cs2-server-manager && csm
```

## 4. Next steps

- See **Guides → Managing Servers** for day-to-day operations.
- See **Guides → Configuration & Overrides** to customize configs before or after installation.
- See **Guides → Auto Updates** to understand how updates are handled.
