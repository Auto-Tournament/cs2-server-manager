<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/csm-wordmark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/logo/csm-wordmark-light.svg">
    <img src="assets/logo/csm-wordmark-light.svg" alt="CS2 Server Manager" height="56">
  </picture>

  # CS2 Server Manager (csm)

</div>

csm is a command-line tool with an interactive terminal UI that installs and runs several Counter-Strike 2 dedicated servers on one Linux machine. It installs the game with SteamCMD, sets up Metamod:Source, CounterStrikeSharp and [MatchZy-Enhanced](https://github.com/sivert-io/MatchZy-Enhanced) on every server, runs each server in its own tmux session, and keeps game and plugin updates going through a cron-driven monitor.

It's for people running their own match servers: LAN organisers, small leagues, and anyone using [MatchZy Auto Tournament (MAT)](https://github.com/sivert-io/matchzy-auto-tournament) who needs servers for it to control. The default MatchZy database is MySQL in a Docker container, so Docker is needed for that setup.

Full documentation lives at [docs.sivert.io/docs/csm](https://docs.sivert.io/docs/csm).

## Install

On a Linux server, download the latest release to `/usr/local/bin/csm` and start the installer:

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

csm keeps its data (overrides, game files, logs) under `/opt/cs2-server-manager` and creates it when needed. The log is `/opt/cs2-server-manager/logs/csm.log`. The install wizard sets up 3 servers by default. Configs you put in `overrides/` survive game and plugin updates.

See the [Quick Start](https://docs.sivert.io/docs/csm/quick-start) for the full first run.

### If `steamcmd` can't be installed (Debian/Ubuntu)

`E: Unable to locate package steamcmd` means your apt sources don't include the component that ships SteamCMD. `sudo csm install-deps` (or the same step in the TUI) tries to fix this itself: it enables the component in `/etc/apt/sources.list`, writes a timestamped backup (for example `/etc/apt/sources.list.csm.bak-YYYYMMDD-HHMMSS`), runs `apt-get update` and retries. If that doesn't work, or you'd rather do it by hand:

Debian (Bookworm): add `contrib` and `non-free` (often also `non-free-firmware`) to your apt sources, then:

```bash
sudo apt-get update
sudo apt-get install steamcmd
```

Ubuntu: enable `multiverse`, then:

```bash
sudo add-apt-repository multiverse
sudo apt-get update
sudo apt-get install steamcmd
```

## Usage

```bash
sudo csm    # interactive TUI for installs, updates, status and so on
csm help    # CLI help, no sudo needed
```

```bash
# Servers
sudo csm status                 # tmux status overview
sudo csm start [server]         # start all servers, or one
sudo csm stop [server]
sudo csm restart [server]

# Updates
sudo csm update-game            # update CS2 game files
sudo csm update-plugins         # download and deploy plugins, restart servers
sudo csm monitor                # run the auto-update monitor once
sudo csm install-monitor-cron   # run the monitor from cron
sudo csm remove-monitor-cron

# Setup and maintenance
sudo csm install-deps           # install system dependencies
sudo csm bootstrap              # install or redeploy servers without the TUI
sudo csm doctor                 # diagnose common problems and offer fixes
sudo csm reinstall <server>     # rebuild one server from master-install
sudo csm update-config <server> # regenerate server configs without reinstalling
sudo csm dedupe-vpk [server]    # hardlink server VPKs to master-install
sudo csm unban <server> <ip>    # remove an IP banned for RCON attempts (0 = all servers)
sudo csm unban-all <server>     # clear all RCON bans (0 = all servers)
csm list-bans <server>

# Logs and debugging
sudo csm attach 1               # attach to server 1's console (tmux)
sudo csm debug 1                # run server 1 in the foreground
sudo csm logs 1 100             # last 100 log lines for server 1
sudo csm logs-file 1            # path to server 1's log file

# Removes all CS2 data and the CS2 user
sudo csm cleanup-all
```

Day-to-day operation, configuration and the update monitor are covered in [Managing Servers](https://docs.sivert.io/docs/csm/user/managing-servers), [Configuration & Overrides](https://docs.sivert.io/docs/csm/user/configuration) and [Auto Updates](https://docs.sivert.io/docs/csm/user/auto-updates).

## Launch modes

By default csm starts servers with Valve's `game/cs2.sh`, unchanged. It also installs `game/csm.sh`, which sets `LD_LIBRARY_PATH` to prefer the libraries bundled with CS2. That helps with `libserver.so` and `libv8` mismatches. You can also run the `cs2` binary directly, which is only meant for troubleshooting.

```bash
sudo csm start --alternate      # use csm.sh
sudo csm start --alternate 1    # just server 1
sudo csm start --binary         # run the cs2 binary directly
```

`--alternate` and `--binary` work on `start`, `restart` and `debug`, and only apply to that one command. To use a launcher everywhere csm starts servers (including `update-plugins`, `update-game` and the monitor), set `CSM_LAUNCH_MODE=alternate` or `CSM_LAUNCH_MODE=binary`, for example `sudo CSM_LAUNCH_MODE=alternate csm restart`. A flag overrides the variable. Every launcher gets the same `+matchzy_config_scope` argument (see below).

### Newer distros and Steam Runtime

On newer distributions such as Debian 13 and Ubuntu 25.04+, CounterStrikeSharp can fail to load under the system runtime ([CounterStrikeSharp #1024](https://github.com/roflmuffin/CounterStrikeSharp/issues/1024)). On those versions csm installs Steam Runtime (SteamRT3, app `1628350`) into `/home/<cs2user>/steamrt` and starts servers through its wrapper. Set `CSM_STEAMRT=1` to force this on, or `CSM_STEAMRT=0` to force it off.

## Metamod version

CounterStrikeSharp and MatchZy install from their latest releases. Metamod:Source is pinned to `2.0.0.1411`, because Metamod builds from 2026-09-08 onward raised the plugin API version, and the current CounterStrikeSharp release fails on them with `Plugin uses old SourceHook Metamod build ... (17 < 18)`.

`sudo csm update-plugins` reinstalls the whole plugin bundle, so it replaces a newer Metamod with the pinned build. To choose a different build, set `CSM_METAMOD_VERSION` to a [metamod-source release tag](https://github.com/alliedmodders/metamod-source/releases) (for example `2.0.0.1468`), or to `latest` for the newest prerelease.

## Disk usage: hardlinked VPKs

A full copy of `master-install` is about 67 GB, and nearly all of it is `*.vpk` archives that CS2 only reads. csm hardlinks the VPKs from `/home/<cs2user>/master-install/game` into each `server-N/game`, so each extra server costs about 1.2 GB on disk instead of 67 GB. The install wizard estimates about 71 GB for `master-install` plus about 2 GB per server.

- Only VPKs are shared. `cfg/`, `addons/`, `gameinfo.gi`, MatchZy data, demos and logs stay separate per server. Hardlinks are used instead of symlinks because symlinked game directories broke demo recording and per-server configs.
- `master-install` and the servers must be on the same filesystem (the default layout under `/home/<cs2user>` is). If linking fails, csm logs it once and copies instead.
- SteamCMD only updates `master-install` and writes changed files as new files, so the servers' links aren't modified. `update-game` then syncs each server: it rsyncs everything except `*.vpk` and `csgo/addons/`, deletes VPKs removed from master, and re-links every VPK atomically. A running server keeps reading the old file until it restarts.
- `CSM_VPK_HARDLINK=0` turns this off, and new syncs make full copies again.

To convert existing servers (`update-game` also re-links servers as it syncs them):

```bash
sudo csm dedupe-vpk --dry-run   # what would be linked, and the estimated savings
sudo csm stop
sudo csm dedupe-vpk             # link VPKs whose size and mtime match master; prints disk usage before and after
sudo csm start
```

`sudo csm dedupe-vpk 2` handles only server-2. `--verify` byte-compares each file before linking, which is slow. Running it twice does nothing the second time, and VPKs that differ from master are reported and left alone. It refuses to run while target servers are running unless you pass `--allow-running`.

To undo it: `sudo csm stop && sudo csm dedupe-vpk --undo && sudo csm start`. This needs about 70 GB free per server and checks first. Also set `CSM_VPK_HARDLINK=0` wherever csm runs (for example `sudo CSM_VPK_HARDLINK=0 csm update-game`, and the monitor cron), or the next sync links them again.

## Several servers and the MatchZy database

By default every server on the machine uses one MySQL database (`matchzy` in the `matchzy-mysql` container), so match stats end up in one place.

MatchZy also stores per-server settings in that database: `matchzy_server_id`, the bootstrap URL and token, the remote log URL, the demo upload URL and so on. Older MatchZy builds key those rows by setting name only, so the last server to save wins and every server loads its values on start. A tournament manager like MAT then sees one server several times, matches get loaded twice, and results overwrite each other. This affects any install with 2 or more servers on shared MySQL.

The fix has two parts:

- [MatchZy-Enhanced 1.4.26](https://github.com/sivert-io/MatchZy-Enhanced/releases/tag/v1.4.26) and newer store those settings per server ([#17](https://github.com/sivert-io/MatchZy-Enhanced/pull/17)). It tells servers apart by bind address and port, but csm starts servers with `-ip 0.0.0.0`, so MatchZy would fall back to the machine name, which every server on the machine shares.
- csm therefore passes `+matchzy_config_scope <hostname>-server-<N>` (for example `cs2-server-1`) when it starts each server. The name comes from the server's directory, so it survives restarts, updates, reinstalls and port changes, and the hostname keeps two machines sharing one database apart. If you rename the machine, or several machines share a hostname, set `CSM_MATCHZY_SCOPE_PREFIX` (for example `eu-1`) wherever csm starts servers.

The install wizard's **MatchZy storage** option picks between shared MySQL (the default; needs MatchZy-Enhanced 1.4.26+ with 2 or more servers) and SQLite per server, where each server keeps its own `matchzy.db`. SQLite works on any MatchZy build but stats aren't shared. For a non-interactive install use `sudo MATCHZY_DB_ENGINE=sqlite csm bootstrap`. csm only rewrites `database.json` while it still contains the `__CSM_NOTE` marker. Remove the note and csm leaves the file alone.

`sudo csm doctor` checks this under "MatchZy per-server config (shared database)". It fails when 2 or more servers report the same `matchzy_server_id`, or when servers share MySQL and run MatchZy older than 1.4.26 or without `+matchzy_config_scope`, and it prints how to fix it.

To migrate an existing install:

1. Update csm.
2. Run `sudo csm update-plugins`. It installs the latest MatchZy-Enhanced, redeploys and restarts every server, which also picks up the new start argument. If you're already on 1.4.26 or newer, `sudo csm restart` is enough. If you can't update MatchZy, set `"DatabaseType": "SQLite"` in `/home/<cs2user>/overrides/game/csgo/cfg/MatchZy/database.json` and `/home/<cs2user>/cs2-config/game/csgo/cfg/MatchZy/database.json`, then run `sudo csm update-plugins`.
3. Reconfigure each server once from your tournament manager (in MAT, re-save or re-bootstrap each server). Until a server saves its own values it still reads the old shared ones.
4. Run `sudo csm doctor` to confirm.

Match stats already in the shared database stay where they are.

## Releasing

Releases run from **Actions → Release → Run workflow**, with `mode` set to `patch`, `minor`, `major` or `explicit` (and `version` as `X.Y.Z` or `vX.Y.Z` when `mode=explicit`). The workflow runs `scripts/release.sh`, the same script used for local releases, and uploads `csm-linux-amd64` and `csm-linux-arm64`. It uses the repository's `GITHUB_TOKEN`. Set the `DISCORD_WEBHOOK_URL` secret for Discord notifications.

## Links

- [Documentation](https://docs.sivert.io/docs/csm)
- [Troubleshooting](https://docs.sivert.io/docs/csm/user/troubleshooting)
- [MatchZy Auto Tournament](https://github.com/sivert-io/matchzy-auto-tournament), a web app for running tournaments on these servers
- [MatchZy-Enhanced](https://github.com/sivert-io/MatchZy-Enhanced), the MatchZy fork csm installs
- [Issues](https://github.com/sivert-io/cs2-server-manager/issues)
