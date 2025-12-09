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

```bash
wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
bash install.sh
```

**Automated install:**

```bash
bash install.sh --auto --servers 5
```

**That's it!** Servers will be installed, configured, and started automatically.

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

## 🎮 Usage

### Interactive Menu

```bash
./manage.sh
```

### Quick Commands

```bash
./manage.sh install          # Install servers
./manage.sh start            # Start all
./manage.sh stop             # Stop all
./manage.sh status           # Check status
./manage.sh update-game      # Update CS2
./manage.sh update-plugins   # Update plugins
./manage.sh repair           # Fix issues
```

### Debug & Logs

```bash
sudo ./scripts/cs2_tmux.sh attach 1    # Attach to console
sudo ./scripts/cs2_tmux.sh debug 1     # Debug mode
sudo ./scripts/cs2_tmux.sh logs 1 100  # View logs
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

**Option 1: Quick Install (Recommended for most users)**

```bash
wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
bash install.sh
```

Uses the default `overrides/` folder from the repository.

**Option 2: Quick Install with Custom Overrides**

```bash
bash install.sh --auto --overrides /path/to/your-overrides
```

Use your own overrides directory (must have same structure as `overrides/game/`).

**Option 3: Git Clone & Customize (Recommended for advanced users)**

```bash
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager
# Edit overrides/ folder as needed
./manage.sh install
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
sudo ./scripts/cs2_tmux.sh debug 1
```

**Plugin errors:**

```bash
./manage.sh repair
```

**Check logs:**

```bash
sudo ./scripts/cs2_tmux.sh logs 1 100
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

# Run installer
./manage.sh
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
