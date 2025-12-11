<div align="center">
  <img src="docs/icon.svg" alt="CS2 Server Manager" width="140" height="140">
  
  # CS2 Server Manager
  
  💣 **Automated multi-server management for Counter-Strike 2**
  
  <p>Deploy multiple dedicated CS2 servers in minutes with competitive plugins, auto-updates, and tournament integration.</p>

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Required-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/engine/install/)

**🔗 [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament)** • **[Enhanced MatchZy](https://github.com/sivert-io/MatchZy)**

</div>

---

## 🚀 Quick Start

Download the latest **prebuilt `csm` binary** from the GitHub Releases page and copy it to your server, then:

```bash
chmod +x csm
./csm              # launches the interactive TUI
```

---

## ✨ Features

💣 **Multi-Server Deployment** — 3–5 servers with one command  
⚙️ **Auto-Plugin Install** — Metamod, CounterStrikeSharp, MatchZy, AutoUpdater  
🔁 **Auto-Updates** — Game & plugin updates happen automatically  
📦 **Config Persistence** — Your configs in `overrides/` survive all updates  
🏆 **Tournament Ready** — Integrates with MatchZy Auto Tournament  
🔐 **MySQL Setup** — Docker-based database auto-provisioned  
🖥 **Interactive Menu** — Easy server management

---

## 🎮 Usage (CSM CLI + TUI)

### Interactive TUI (recommended)

Use the Bubble Tea–based TUI to manage everything:

```bash
./scripts/start.sh          # builds and runs the CSM (CS2 Server Manager) TUI

# or manually:
go build -o csm ./src/cmd/cs2-tui
./csm
```

The TUI handles installs, status, start/stop/restart, updates, and more using the [Bubble Tea](https://github.com/charmbracelet/bubbletea) Go framework.

### CLI commands (non-interactive)

Once `csm` is built or downloaded, you can also use it directly:

```bash
./csm install-deps           # Install core system dependencies (run with sudo)
./csm bootstrap              # Install/redeploy servers (reads env for options, run with sudo)
./csm status                 # Tmux status overview
./csm start                  # Start all servers
./csm stop                   # Stop all servers
./csm restart                # Restart all servers
./csm update-game            # Update CS2 game files
./csm update-plugins         # Update plugins (download + deploy)
./csm monitor                # Run one iteration of the auto-update monitor
./csm install-monitor-cron   # Install cron-based auto-update monitor
./csm cleanup-all            # Danger: remove all CS2 data and user
./csm public-ip              # Print public IP
```

### Debug & Logs

```bash
sudo ./csm attach 1       # Attach to server 1 console (tmux)
sudo ./csm debug 1        # Run server 1 in foreground (debug mode)
sudo ./csm logs 1 100     # View last 100 lines of server 1 logs
```

---

## 🔌 Plugins

All installed automatically:

- **Metamod:Source** — Plugin framework
- **CounterStrikeSharp** — C# plugin loader
- **MatchZy Enhanced** — Tournament automation with extra events
- **CS2-AutoUpdater** — Auto-shutdown on game updates

---

## 🤖 Auto-Update Monitoring

Automatically installed! Monitors every 5 minutes:

✅ Detects AutoUpdater shutdowns  
✅ Runs SteamCMD updates  
✅ Restarts servers  
✅ 1-hour cooldown protection

**View logs:**

```bash
sudo tail -f /var/log/cs2_auto_update_monitor.log
```

---

## ⚙️ Configuration

### Installation Methods

**Option 1: Download prebuilt binary (recommended)**

```bash
chmod +x csm
sudo ./csm            # launches the interactive TUI installer
```

**Option 2: Git Clone & Customize (Recommended for advanced users)**

```bash
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager
# Edit overrides/ folder as needed
./scripts/start.sh          # or: go build -o csm ./src/cmd/cs2-tui && ./csm
```

Best for users who want to customize configs before installation or maintain their own fork.

### Overrides Directory

The `overrides/` folder contains custom configurations that are applied to all servers:

- Files in `overrides/` are **never deleted** during updates
- They overlay on top of default plugin configs
- Structure: `overrides/game/csgo/cfg/...` and `overrides/game/csgo/addons/...`

**Server Ports** (increment by 10):

| Server | Game  | GOTV  |
| ------ | ----- | ----- |
| 1      | 27015 | 27020 |
| 2      | 27025 | 27030 |
| 3      | 27035 | 27040 |

**Default RCON:** `ntlan2025`

**Custom Configs** — Place in `overrides/game/csgo/`:

```
overrides/game/csgo/
├── cfg/MatchZy/
└── addons/
```

These persist through all updates.

---

## 🐛 Troubleshooting

**Server won't start:**

```bash
sudo ./csm debug 1
```

**Plugin errors:**

```bash
./csm update-plugins
```

**Check logs:**

```bash
sudo ./csm logs 1 100
```

---

## 📋 Manual Install

If you prefer manual setup:

```bash
# Install dependencies
sudo apt-get update
sudo apt-get install -y lib32gcc-s1 lib32stdc++6 steamcmd tmux curl jq unzip tar rsync git

# Install Docker: https://docs.docker.com/engine/install/

# Clone repo
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager

# Run TUI/CLI after cloning
./scripts/start.sh         # or: go build -o csm ./src/cmd/cs2-tui && ./csm
```

---

## 🔗 Links

- [MatchZy Enhanced Fork](https://github.com/sivert-io/MatchZy)
- [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament)
- [CounterStrikeSharp Docs](https://docs.cssharp.dev/)

---

<div align="center">
  <strong>Made with ❤️ for the CS2 community</strong>
</div>
