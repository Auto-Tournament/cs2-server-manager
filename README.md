<div align="center">
  <img src="assets/icon.svg" alt="CS2 Server Manager" width="140" height="140">
  
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

### If `steamcmd` can’t be installed (Debian/Ubuntu)

If you see `E: Unable to locate package steamcmd`, your apt sources likely don’t include the component that provides SteamCMD.

- **Note**: When run via `sudo csm install-deps` (or the TUI equivalent), CSM will attempt to **automatically enable the required component(s)** in `/etc/apt/sources.list`, write a timestamped backup (e.g. `/etc/apt/sources.list.csm.bak-YYYYMMDD-HHMMSS`), then rerun `apt-get update` and retry the install.

- **Debian (Bookworm)**: ensure your apt sources include `contrib` + `non-free` (and often `non-free-firmware`), then:

```bash
sudo apt-get update
sudo apt-get install steamcmd
```

- **Ubuntu**: enable `multiverse`, then:

```bash
sudo add-apt-repository multiverse
sudo apt-get update
sudo apt-get install steamcmd
```

If auto-fix can’t update your sources (or you don’t want it to), CSM will show a targeted hint. Full logs are written to `/opt/cs2-server-manager/logs/csm.log` by default.

---

### Newer distros: CounterStrikeSharp compatibility (Steam Runtime)

On newer Linux distributions (for example Debian 13 / Ubuntu 25.04+), CounterStrikeSharp may fail to load under the system runtime. CSM can automatically launch the server using **Steam Runtime (SteamRT3)** as a workaround, based on upstream findings in [CounterStrikeSharp issue #1024](https://github.com/roflmuffin/CounterStrikeSharp/issues/1024).

- **Auto behavior**: on affected OS versions, CSM installs Steam Runtime (app `1628350`) into `/home/<cs2user>/steamrt` (if missing) and starts servers via the runtime wrapper.
- **Override**: set `CSM_STEAMRT=1` to force-enable, or `CSM_STEAMRT=0` to force-disable.

### CS2 launch script (`cs2.sh`) (default) and alternate launcher (`csm.sh`)

- **Default**: CSM launches using Valve’s `game/cs2.sh` (kept intact).
- **Alternate (opt-in)**: CSM also installs `game/csm.sh`, which sets `LD_LIBRARY_PATH` to prefer CS2-bundled libs (helpful for common `libserver.so` / `libv8` mismatch issues). Use it when troubleshooting by starting with:

```bash
sudo csm start --alternate
```

You can also target a single server:

```bash
sudo csm start --alternate 1
```

If you want to run the `cs2` **binary directly** (not recommended; provided for troubleshooting), you can use:

```bash
sudo csm start --binary
```

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
sudo csm update-config <server> # Regenerate server configs without reinstalling
sudo csm unban <server> <ip>    # Remove IP from banned RCON requests (use 0 for all servers)
sudo csm unban-all <server>     # Clear all IPs banned for RCON attempts (use 0 for all servers)
csm list-bans <server>          # List banned IPs for a server

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

## 📚 Documentation & Links (docs.sivert.io)

- [Docs home](https://docs.sivert.io/docs/csm) – hosted docs and guides.
- [Quick Start](https://docs.sivert.io/docs/csm/quick-start) – installation and first run.
- [Managing Servers](https://docs.sivert.io/docs/csm/user/managing-servers) – day-to-day operations.
- [Configuration & Overrides](https://docs.sivert.io/docs/csm/user/configuration) – customizing configs.
- [Auto Updates](https://docs.sivert.io/docs/csm/user/auto-updates) – how the monitor and updates work.
- [Troubleshooting](https://docs.sivert.io/docs/csm/user/troubleshooting) – common issues and fixes.
- [MatchZy Enhanced Fork](https://github.com/sivert-io/MatchZy-Enhanced)
- [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament)
- [Project Roadmap & Status](https://kan.sivert.io/MAT) – kanban board (auto-updates from GitHub issues)

---

<div align="center">
  <strong>Made with ❤️ for the CS2 community</strong>
</div>
