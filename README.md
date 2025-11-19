<div align="center">
  <img src="csm-icon.svg" alt="CS2 Server Manager" width="140" height="140">
  
  # CS2 Server Manager
  
  💣 **Automated multi-server management for Counter-Strike 2 — one script from clean install to tournament-ready**
  
  <p>Deploy 3–5 dedicated CS2 servers in minutes. Fully preconfigured with essential competitive plugins, auto-updates, and seamless integration with MatchZy Auto Tournament.</p>

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Required-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/engine/install/)
[![Bash](https://img.shields.io/badge/Script-Bash-1f425f.svg)](manage.sh)

**🔗 <a href="https://github.com/sivert-io/cs2-server-manager" target="_blank">GitHub Repository</a>** • <a href="https://github.com/sivert-io/matchzy-auto-tournament" target="_blank">MatchZy Auto Tournament</a> • <a href="https://github.com/sivert-io/MatchZy" target="_blank">Enhanced MatchZy Plugin</a>

</div>

---

## ✨ Features

💣 **Multi-Server Deployment** — Spins up 3–5 dedicated CS2 servers automatically  
⚙️ **Automated Plugin Installer** — Installs Metamod, CounterStrikeSharp, MatchZy (enhanced fork), AutoUpdater  
🔁 **Auto-Update Support** — Game updates + plugin updates handled for you  
📦 **Full Override System** — Your configs inside `overrides/` survive ALL updates  
🏆 **Tournament Ready** — Fully integrates with  
➡️ <a href="https://github.com/sivert-io/matchzy-auto-tournament" target="_blank">MatchZy Auto Tournament</a>  
🔐 **Docker-Backed MySQL Setup** — Automatic MatchZy database provisioning  
🖥 **Interactive Menu** — Install, start, stop, update, debug  
🛠 **Advanced Debug Tools** — Tmux console attach, log tailing, repair tools  

---

## 🚀 Quick Start

### ⚡ One-Line Installation (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh | bash
```

This will:
- ✅ Check and install all dependencies
- ✅ Download CS2 Server Manager
- ✅ Run interactive installation
- ✅ Set up auto-update monitoring

**Fully automated (no prompts):**

```bash
curl -fsSL https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh | bash -s -- --auto
```

**Custom number of servers:**

```bash
curl -fsSL https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh | bash -s -- --auto --servers 5
```

---

### 🧰 Manual Installation

**Prerequisites:**

```bash
sudo apt-get update
sudo apt-get install -y lib32gcc-s1 lib32stdc++6 steamcmd tmux curl jq unzip tar rsync git
```

Install **Docker Engine** (required):

👉 [https://docs.docker.com/engine/install/](https://docs.docker.com/engine/install/)

**Install:**

```bash
# Clone and enter directory
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager

# Interactive mode
./manage.sh
# → Choose Option 1
# → Default: 3 servers
# → Wait 15–30 minutes for CS2 download (~60GB)
```

**Non-Interactive (automated defaults):**

```bash
./manage.sh install
```

### Start Servers

```bash
./manage.sh start          # Non-interactive
# OR
./manage.sh                # Interactive menu → Option 3
```

---

## 🔌 Included Plugins

Automatically installed & configured during setup:

* **Metamod:Source** — Core plugin framework
* **CounterStrikeSharp** — Modern C# plugin loader
* **MatchZy Enhanced Fork** — Tournament automation version

  * Extra events for MatchZy Auto Tournament
  * Real-time player tracking & match lifecycle
* **CS2-AutoUpdater** — Automatic game update daemon

---

## 🤖 Automated Update Monitoring

**Automatically installed during setup!** When you run `./manage.sh` and choose install, an auto-update monitor is configured to:

✅ **Detect AutoUpdater Shutdowns** — Monitors for game update shutdowns every 5 minutes  
✅ **Auto-Run Updates** — Triggers SteamCMD updates when detected  
✅ **Auto-Restart Servers** — Brings servers back online with new version  
✅ **Safe Operation** — Only triggers when ALL servers are down  
✅ **Cooldown Protection** — Won't update more than once per hour  

**View monitor logs:**
```bash
sudo tail -f /var/log/cs2_auto_update_monitor.log
```

**Remove if needed:**
```bash
sudo ./scripts/remove_auto_update_cron.sh
```

---

## 🎮 Usage

### Interactive Menu

```bash
./manage.sh
```

**Main operations:**

* **1** — Install/redeploy servers
* **2** — Server status
* **3/4/5** — Start / Stop / Restart all
* **12** — Update CS2 game files
* **13** — Update plugins
* **15** — Repair without re-download
* **16** — Debug mode (foreground)

---

### Non-Interactive Commands

```bash
./manage.sh install
./manage.sh start
./manage.sh stop
./manage.sh status
./manage.sh update-game
./manage.sh update-plugins
./manage.sh repair
./manage.sh help
```

---

### Direct Server Management

```bash
sudo ./scripts/cs2_tmux.sh start
sudo ./scripts/cs2_tmux.sh start 1
sudo ./scripts/cs2_tmux.sh stop
sudo ./scripts/cs2_tmux.sh status
sudo ./scripts/cs2_tmux.sh attach 1
sudo ./scripts/cs2_tmux.sh debug 1
sudo ./scripts/cs2_tmux.sh logs 1 100
```

---

## ⚙️ Configuration

### Server Ports

(Increment by 10 per server)

| Server | Game Port | GOTV Port |
| ------ | --------- | --------- |
| 1      | 27015     | 27020     |
| 2      | 27025     | 27030     |
| 3      | 27035     | 27040     |

**Default RCON:** `ntlan2025`
(Change via installer or overrides)

---

### Custom Config Overrides

Place configs in `overrides/game/csgo/`:

```
overrides/game/csgo/
├── cfg/MatchZy/
└── addons/
```

✔ These files **persist through all updates**
✔ MatchZy configs remain untouched
✔ Plugin configs remain consistent

---

## 🗄️ MatchZy Database (Docker)

* Reads `overrides/.../MatchZy/database.json` during install
* Creates a MySQL container with the specified credentials
* Exposes port defined in your JSON file
* Automatically rewrites `MySqlHost` to your machine IP
* Ensures all server instances connect correctly

Docker **must** be running before installation.

---

## 🐛 Troubleshooting

### Server won't start

```bash
sudo ./scripts/cs2_tmux.sh debug 1
```

### Plugins failing / steamclient.so errors

```bash
./manage.sh repair
```

### Logs

```bash
sudo ./scripts/cs2_tmux.sh logs 1 100
```

---

## 📂 File Structure

```
/home/cs2/
├── master-install/
├── server-1/
├── server-2/
└── server-3/

cs2-server-manager/
├── scripts/
├── game_files/
├── overrides/
└── manage.sh
```

---

## 🔗 Related Projects

* **MatchZy Enhanced Fork** — [https://github.com/sivert-io/MatchZy](https://github.com/sivert-io/MatchZy)
* **MatchZy Auto Tournament** — [https://github.com/sivert-io/matchzy-auto-tournament](https://github.com/sivert-io/matchzy-auto-tournament)
* **CS2 Dedicated Server Docs** — [https://developer.valvesoftware.com/wiki/Counter-Strike_2/Dedicated_Servers](https://developer.valvesoftware.com/wiki/Counter-Strike_2/Dedicated_Servers)
* **CounterStrikeSharp** — [https://docs.cssharp.dev/](https://docs.cssharp.dev/)

---

<div align="center">
  <strong>Made with ❤️ for the CS2 community</strong>
</div>
```
