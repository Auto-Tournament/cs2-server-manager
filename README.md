<div align="center">
  <img src="docs/icon.svg" alt="CS2 Server Manager" width="140" height="140">
  
  # CS2 Server Manager
  
  💣 **Automated multi-server management for Counter-Strike 2**
  
  <p>Deploy multiple dedicated CS2 servers in minutes with competitive plugins, auto-updates, and tournament integration.</p>

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Required-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/engine/install/)

**🔗 [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament)** • **[MatchZy Enhanced](https://github.com/sivert-io/MatchZy-Enhanced)**

</div>

---

## 🚀 Quick Start (global install, recommended)

On a Linux server, you can **install `csm` globally** and launch the latest release with a single command:

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
sudo csm          # launches the interactive TUI installer
```

By default, CSM stores its data under `/opt/cs2-server-manager` (creating it on demand) so overrides, game files, and logs are kept in one place.

---

## ✨ Features

💣 **Multi-Server Deployment** — 3–5 servers with one command  
⚙️ **Auto-Plugin Install** — Metamod, CounterStrikeSharp, MatchZy  
🔁 **Auto-Updates** — Game & plugin updates happen automatically  
📦 **Config Persistence** — Your configs in `overrides/` survive all updates  
🏆 **Tournament Ready** — Integrates with MatchZy Auto Tournament  
🔐 **MySQL Setup** — Docker-based database auto-provisioned  
🖥 **Interactive Menu** — Easy server management

---

## 🎮 Usage

Once installed globally you can:

```bash
sudo csm    # launch the TUI for installs, updates, status, etc. (requires sudo)
csm help    # show CLI help without sudo
```

Common CLI commands:

```bash
# Server management
sudo csm status                 # Tmux status overview
sudo csm start [server]         # Start all servers (or specific server)
sudo csm stop [server]          # Stop all servers (or specific server)
sudo csm restart [server]       # Restart all servers (or specific server)

# Updates
sudo csm update-game            # Update CS2 game files
sudo csm update-plugins         # Update plugins (download + deploy)
sudo csm monitor                # Run one iteration of the auto-update monitor

# Setup & maintenance
sudo csm install-deps           # Install core system dependencies
sudo csm bootstrap              # Install/redeploy servers
sudo csm install-monitor-cron   # Install cron-based auto-update monitor
sudo csm reinstall <server>     # Rebuild a server (fixes corrupted files)

# Cleanup
sudo csm cleanup-all            # Danger: remove all CS2 data and user
```

For logs and debugging:

```bash
sudo csm attach 1        # Attach to server 1 console (tmux)
sudo csm debug 1         # Run server 1 in foreground (debug mode)
sudo csm logs 1 100      # View last 100 lines of server 1 logs
sudo csm logs-file 1     # Print the log file path for server 1
```

---

## 📚 Documentation & Links

- [Full documentation site](https://sivert-io.github.io/cs2-server-manager/) – hosted docs and guides.
- [Getting Started](docs/getting-started/quick-start.md) – installation and first run.
- [Managing Servers](docs/guides/managing-servers.md) – day-to-day operations.
- [Configuration & Overrides](docs/guides/configuration.md) – customizing configs.
- [Auto Updates](docs/guides/auto-updates.md) – how the monitor and updates work.
- [Troubleshooting](docs/guides/troubleshooting.md) – common issues and fixes.
- [MatchZy Enhanced Fork](https://github.com/sivert-io/MatchZy-Enhanced)
- [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament)
- [Project Roadmap & Status](https://kan.sivert.io/MAT) – kanban board (auto-updates from GitHub issues)

---

<div align="center">
  <strong>Made with ❤️ for the CS2 community</strong>
</div>
