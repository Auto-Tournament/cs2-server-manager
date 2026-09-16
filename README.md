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

### Metamod version (pinned)

CounterStrikeSharp and MatchZy install from their latest releases, but Metamod:Source is **pinned** to a build that CounterStrikeSharp can load (currently `2.0.0.1411`). Metamod builds from 2026-09-08 onward raised the plugin API version, and the current CounterStrikeSharp release fails to load on them with `Plugin uses old SourceHook Metamod build ... (17 < 18)`.

- **Repair**: `sudo csm update-plugins` always reinstalls the full plugin bundle, so it replaces a newer, incompatible Metamod with the pinned build.
- **Override**: set `CSM_METAMOD_VERSION` to a [metamod-source release tag](https://github.com/alliedmodders/metamod-source/releases) (for example `2.0.0.1468`), or to `latest` to install the newest prerelease.

### Disk usage: hardlinked VPKs

Every server used to be a full copy of `master-install` (about 67 GB each). Almost all of that is `*.vpk` game archives (about 70 GB of VPKs per tree, versus about 1.2 GB for everything else), and CS2 only reads them. CSM now **hardlinks the `*.vpk` files** from `/home/<cs2user>/master-install/game` into each `server-N/game`. Each extra server then costs about 1.2 GB instead of 67 GB.

- **Only VPKs are shared.** Configs (`cfg/`), `addons/`, `gameinfo.gi`, MatchZy data, demos and logs stay real per-server files. Editing one server's config never touches another server.
- **Why hardlinks and not symlinks:** symlinked game directories broke demo recording and per-server configs. A hardlink looks like a normal file to the game.
- **Requirements:** `master-install` and the server directories must be on the same filesystem (the default layout under `/home/<cs2user>` is). If hardlinking fails (different filesystem, permissions), CSM logs it once and falls back to a real copy.
- **Updates:** SteamCMD only ever updates `master-install`. It writes changed files as new files, so an updated VPK gets a new inode and the servers' old links are not modified. `update-game` then syncs each server: rsync copies everything except `*.vpk` (and `csgo/addons/`), VPKs removed from master are deleted, and every VPK is re-linked atomically (link to a temp name, rename over the old file). A server process that still has the old file open keeps reading the old data until it restarts.
- **Opt out:** set `CSM_VPK_HARDLINK=0` and new syncs go back to full copies.

Migrating existing servers (one-off; `update-game` also re-links servers as it syncs them):

```bash
sudo csm dedupe-vpk --dry-run   # show what would be linked and the estimated savings
sudo csm stop
sudo csm dedupe-vpk             # hardlink identical VPKs (size + mtime match); prints disk usage before/after
sudo csm start
```

- `sudo csm dedupe-vpk 2` handles only server-2. `--verify` also byte-compares each file before linking (slow, reads everything).
- Running it twice is a no-op. VPKs that differ from master are left alone and reported.
- It refuses to run while target servers are running. Replacing a file by rename is safe for open files on Linux, but stopping first is recommended; `--allow-running` skips the check.
- **Rollback:** `sudo csm stop && sudo csm dedupe-vpk --undo && sudo csm start` turns the links back into independent copies (it checks free space first; you need about 70 GB per server). Also set `CSM_VPK_HARDLINK=0` wherever csm runs (for example `sudo CSM_VPK_HARDLINK=0 csm update-game`, and the monitor cron), or the next sync links the VPKs again.

### Several servers and the MatchZy database

By default every server on the machine uses **one shared MySQL database** (`matchzy` in the `matchzy-mysql` container), so match stats end up in one place.

**What broke.** MatchZy also keeps per-server settings in that database: `matchzy_server_id`, the bootstrap URL and token, the remote log URL, the demo upload URL and so on. Older MatchZy builds store those rows by setting name only. With one shared database, the last server to save wins, and every server loads that server's values when it starts:

```
server-1: [LoadPersistentConfig] Loaded matchzy_bootstrap_url: http://…/api/servers/s_3/bootstrap
server-1/2/3: "server_id":"s_3"
```

A tournament manager then sees one server three times: matches get loaded twice and results overwrite each other. This affects every install with 2 or more servers on shared MySQL.

**The fix has two parts.**

- **MatchZy** stores those settings per server ([MatchZy-Enhanced #17](https://github.com/sivert-io/MatchZy-Enhanced/pull/17)). By default it tells servers apart by bind address and game port. CSM starts servers with `-ip 0.0.0.0`, which doesn't identify anything, so MatchZy falls back to the machine name. That name is the same for every server on one machine.
- **CSM** therefore passes a name for each server on the start command line: `+matchzy_config_scope <hostname>-server-<N>`, for example `cs2-server-1`. It uses the server's directory name, so it stays the same across restarts, game and plugin updates, reinstalls and port changes. The hostname keeps two machines that share one database apart. If you rename the machine, or several machines have the same hostname, set `CSM_MATCHZY_SCOPE_PREFIX` (for example `CSM_MATCHZY_SCOPE_PREFIX=eu-1`) wherever csm starts servers.

**Choosing storage.** The install wizard has a **MatchZy storage** option:

- **Shared MySQL** (default). Stats are shared. With 2 or more servers this needs a MatchZy-Enhanced build that includes #17. <!-- TODO(matchzy-scope): name the release version once #17 ships (see MatchzyScopingMinVersion). -->
- **SQLite per server.** Each server keeps its own `matchzy.db` next to the plugin, so servers can't load each other's settings on any MatchZy build. Stats are not shared. Use this if you can't update MatchZy yet. For a non-interactive install: `sudo MATCHZY_DB_ENGINE=sqlite csm bootstrap`.

CSM only rewrites `database.json` when it still has CSM's `__CSM_NOTE` ("managed by CSM's install wizard"). If you removed or replaced that note, CSM leaves the file alone.

**Check an install:** `sudo csm doctor` reports **MatchZy per-server config (shared database)**. It fails when 2 or more servers report the same `matchzy_server_id`, or when servers share one MySQL database and either run a MatchZy build without per-server scoping or are still running without `+matchzy_config_scope`. It prints the steps to fix it.

**Migrating an existing install:**

1. Update csm.
2. Get a MatchZy build with per-server scoping and restart all servers: `sudo csm update-plugins` (it redeploys the plugins and restarts every server). The restart picks up the new start argument. If you only need the start argument, `sudo csm restart` is enough.
   - If that MatchZy build isn't available yet, switch to SQLite per server instead: set `"DatabaseType": "SQLite"` in `/home/<cs2user>/overrides/game/csgo/cfg/MatchZy/database.json` and `/home/<cs2user>/cs2-config/game/csgo/cfg/MatchZy/database.json`, then run `sudo csm update-plugins`.
3. Reconfigure each server once from your tournament manager (in MAT, re-save or re-bootstrap each server). Until a server saves its own values, it still reads the old shared ones. After that, each server keeps its own.
4. Run `sudo csm doctor` to confirm.

Match stats already in the shared database stay where they are.

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
sudo csm dedupe-vpk [server]    # Hardlink server VPKs to master-install (saves ~66 GB per server)
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

## 📦 Release from GitHub Actions

Manual releases can be run from **Actions → Release → Run workflow**.

Inputs:

- `mode` (required): `patch`, `minor`, `major`, or `explicit`
- `version` (optional): required only when `mode=explicit` (use `X.Y.Z` or `vX.Y.Z`)

What the workflow does:

- runs `scripts/release.sh` directly so version bump/tag/build/release/webhook behavior is identical to local releases
- builds and uploads the same Linux release assets:
  - `csm-linux-amd64`
  - `csm-linux-arm64`

Authentication:

- uses the repository `GITHUB_TOKEN` by default (no PAT needed for same-repository releases)
- for Discord notifications, set repository secret `DISCORD_WEBHOOK_URL`

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
