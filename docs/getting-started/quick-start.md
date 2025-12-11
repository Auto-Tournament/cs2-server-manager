# Quick Start

This guide gets you from zero to running CS2 servers in a few minutes.

## Prerequisites

- **OS**: Ubuntu 22.04+ (or similar modern Linux distro).
- **Root / sudo access** on the machine that will host the servers.
- **Docker** installed and running (for MySQL and supporting services).
- **Enough resources** for multiple CS2 servers (CPU, RAM, and disk).

## 1. Download CSM and run the installer wizard

From your target server, download the latest **prebuilt `csm` binary** from GitHub Releases and run it. The installer is a guided form built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [huh](https://github.com/charmbracelet/huh), so you can navigate with arrows and confirm with Enter:

```bash
chmod +x csm
sudo ./csm            # launches the interactive TUI installer
```

The installer wizard will:

- Install required system dependencies.
- Download CS2 server files.
- Set up Dockerized MySQL.
- Install Metamod, CounterStrikeSharp, MatchZy, and AutoUpdater.
- Configure multiple CS2 instances with sane defaults.

## 2. Use the CSM TUI

Once installation completes, you can re-open the TUI at any time with `./csm` (or rebuild locally via `./scripts/start.sh`).

From the TUI you can:

- Install or repair servers (wizard).
- Start/stop/restart all servers.
- Check status and logs.
- Run game/plugin updates.

## 3. Common CLI one-liners

These commands are shortcuts around the TUI:

```bash
./csm install-deps           # Install core system dependencies (run with sudo)
./csm status                 # Check tmux status
./csm update-game            # Update CS2
./csm update-plugins         # Update plugins
./csm monitor                # Run one monitor iteration
./csm install-monitor-cron   # Install cron-based auto-update monitor
```

## 4. Next steps

- See **Guides → Managing Servers** for day-to-day operations.
- See **Guides → Configuration & Overrides** to customize configs before or after installation.
- See **Guides → Auto Updates** to understand how updates are handled.
