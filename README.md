<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/csm-wordmark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/logo/csm-wordmark-light.svg">
    <img src="assets/logo/csm-wordmark-light.svg" alt="CS2 Server Manager" height="56">
  </picture>

  # CS2 Server Manager (csm)

</div>

<div align="center">

### Sponsor Auto Tournament

Running tournaments or LANs with Auto Tournament? Your organisation can keep it growing.
Auto Tournament is built and maintained by one person — sponsorships pay for development, test servers and infrastructure.

[![Sponsor on GitHub](https://img.shields.io/badge/Sponsor-GitHub-ea4aaa?logo=githubsponsors&logoColor=white)](https://github.com/sponsors/sivert-io)
[![Support on Ko-fi](https://img.shields.io/badge/Support-Ko--fi-ff5e5b?logo=kofi&logoColor=white)](https://ko-fi.com/sivert)
[![Become a sponsor](https://img.shields.io/badge/Become%20a%20sponsor-Discord-5865F2?logo=discord&logoColor=white)](https://discord.gg/n7gHYau7aW)

Using it for a business, paid events or hosting? That needs a commercial licence → [Licensing](https://docs.autotournament.gg/reference/licensing)

</div>

> **Moved:** this repository is now part of the [Auto-Tournament](https://github.com/Auto-Tournament)
> organisation, together with Auto Tournament (formerly MatchZy Auto Tournament). Old links
> redirect, and nothing changes for existing installs.

csm is a command-line tool with an interactive terminal UI that installs and runs several Counter-Strike 2 dedicated servers on one Linux machine. It installs the game with SteamCMD, sets up Metamod:Source, CounterStrikeSharp and [Auto Tournament CS2](https://github.com/Auto-Tournament/cs2-plugin) (formerly MatchZy Enhanced) on every server, runs each server in its own tmux session, and keeps game and plugin updates going through a cron-driven monitor.

It's for people running their own match servers: LAN organisers, small leagues, and anyone using [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament) who needs servers for it to control. The default MatchZy database is MySQL in a Docker container, so Docker is needed for that setup.

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
curl -L "https://github.com/Auto-Tournament/cs2-server-manager/releases/latest/download/$asset" -o "$tmp" && \
sudo install -m 0755 "$tmp" /usr/local/bin/csm && \
rm "$tmp" && \
sudo csm          # launches the interactive TUI installer
```

csm keeps its data (overrides, game files, logs) under `/opt/cs2-server-manager` and creates it when needed. The log is `/opt/cs2-server-manager/logs/csm.log`. The install wizard sets up 3 servers by default. Configs you put in `overrides/` survive game and plugin updates.

See the [Quick Start](https://docs.sivert.io/docs/csm/quick-start) for the full first run.

### Run csm without sudo (user mode)

csm can run as its service user (`cs2servermanager` by default, or `CS2_USER`) instead of root. Set the host up once as root:

```bash
sudo csm setup-host
```

It installs the system dependencies, creates the user if needed, runs `loginctl enable-linger` for it, gives it the state directory (`/opt/cs2-server-manager`) and the csm files root runs left in `/tmp`, and moves the auto-update monitor from root's crontab into the user's crontab. It is safe to run again, and it never starts, stops, restarts or updates a server. From then on, run csm as that user, without sudo:

```bash
sudo -iu cs2servermanager   # or log in as that user
csm status
csm                         # TUI
```

Servers that are already running keep running: csm finds them in the same tmux server as before. A few things still need root and say so when you try them as the user: `sudo csm install-deps`, `sudo csm cleanup-all`, and creating the MatchZy MySQL Docker container (`sudo csm bootstrap`; as the user, bootstrap leaves an existing container alone). On a host that already runs servers, use `sudo csm setup-host --skip-deps`: `apt-get install` can upgrade tmux, and a newer tmux client can't talk to the tmux server the running servers live in. `--skip-linger` skips `loginctl`.

`csm self-update` run as the user can't replace `/usr/local/bin/csm`, so it installs the new binary into `~/.local/bin/csm`, which login shells put first in `PATH`. Run `csm install-monitor-cron` afterwards so cron uses it too.

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

Run these as the CS2 user after `sudo csm setup-host` (see above), or with `sudo` as before.

```bash
csm                        # interactive TUI for installs, updates, status and so on
csm help                   # CLI help
```

```bash
# Servers
csm status                 # fleet table: process, map, phase, score, players, Ready Up
csm status --watch         # the same table, updated live
csm start [server]         # start all servers, or one
csm stop [server]          # refuses while a Ready Up match is live; --force overrides
csm restart [server]

# Updates
csm update-game            # update CS2 game files
csm update-plugins         # download and deploy plugins, restart servers
csm monitor                # run the auto-update monitor once
csm updates hold on        # no automatic restarts; "off" to resume, "auto" to let the platform decide
csm updates platform <url> <token> # let Auto Tournament hold updates while a tournament runs
csm updates check          # ask the platform now whether updates are held
csm install-monitor-cron   # run the monitor from cron (the crontab of the user running it)
csm remove-monitor-cron

# Setup and maintenance
sudo csm setup-host        # one-time root setup for user mode (--skip-deps, --skip-linger)
sudo csm install-deps      # install system dependencies
csm bootstrap              # install or redeploy servers without the TUI
csm doctor                 # diagnose common problems and offer fixes
csm reinstall <server>     # rebuild one server from master-install
csm update-config <server> # regenerate server configs without reinstalling
csm dedupe-vpk [server]    # hardlink server VPKs to master-install
csm unban <server> <ip>    # remove an IP banned for RCON attempts (0 = all servers)
csm unban-all <server>     # clear all RCON bans (0 = all servers)
csm list-bans <server>
csm extract-map-data       # map thumbnails + maps.json into ./map_thumbnails

# Logs and debugging
csm attach 1               # attach to server 1's console (tmux)
csm debug 1                # run server 1 in the foreground
csm logs 1 100             # last 100 log lines for server 1
csm logs-file 1            # path to server 1's log file

# Removes all CS2 data and the CS2 user
sudo csm cleanup-all
```

### Holding updates during a tournament

The monitor restarts a server for a CS2 update once it has been idle for the grace period: nobody connected, no match loaded. That is a local judgement, and it stays right only until [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament) gives that server the next match of a running tournament.

Point csm at the platform and it asks before every restart:

```bash
csm updates platform https://cs.example.io "$SERVER_TOKEN"
csm updates check
```

The token is the platform's `SERVER_TOKEN` — the same fleet-wide token the plugin already uses for event webhooks and demo uploads, not a new secret. csm polls the platform, so the game server needs no inbound port. `CSM_PLATFORM_URL` and `CSM_PLATFORM_TOKEN` override the stored values for hosts that keep secrets out of files; the settings file is written owner-only either way.

Updates are then held while a tournament is in progress or any match is loaded or live. **If the platform cannot be reached, updates stay held** — csm will not restart a server while it cannot tell whether a tournament is running. Every skipped update says which of these it was in `auto_update_monitor.log`.

`csm updates hold on` and `off` are overrides that win over the platform; `csm updates hold auto` goes back to asking it. `csm update-game` and `csm update-server` run whatever the hold says; the only thing that stops them is a live match on a Ready Up server (next section).

Day-to-day operation, configuration and the update monitor are covered in [Managing Servers](https://docs.sivert.io/docs/csm/user/managing-servers), [Configuration & Overrides](https://docs.sivert.io/docs/csm/user/configuration) and [Auto Updates](https://docs.sivert.io/docs/csm/user/auto-updates).

### Ready Up servers: live status and match protection

Servers that run [Ready Up](https://github.com/Auto-Tournament/ready-up) publish their state on a small local HTTP endpoint (`/status` and a live `/stream`). csm reads the port and a read-only token from `server-N/game/csgo/readyup/status.json`, or tries the game port + 7 (Ready Up's default `status_http_port`) when that file is missing.

`csm status` (and **Servers → Servers dashboard** in the TUI) shows one row per server:

```
#  PORT   PROC     MAP            PHASE        SCORE      PLAYERS  MATCH           PLATFORM    READY UP  CS2    SAFE
1  27015  running  de_mirage 2/3  live R14     8-5 (1-0)  10/10    412 NAVI vs G2  online      0.9.0     14090  NO
2  27025  running  de_dust2       idle         -          2        -               standalone  0.9.0     14090  yes
3  27035  running  -              no Ready Up  -          -        -               -           -         -      -
```

The TUI dashboard and `csm status --watch` follow each server's `/stream` and update as rounds are played; a Ready Up without `/stream` is polled instead. `csm status --json` prints the same data for scripts.

`SAFE` is Ready Up's `update_safe`: `NO` from the moment a match loads until the series is over and its demo is uploaded. While it says `NO`, `stop`, `restart`, `update-game`, `update-server` and `update-plugins` refuse to run and name the match that is in the way. The auto-update monitor skips that server too. Add `--force` to go ahead anyway; forced runs are written to `csm.log`. The TUI never forces; it tells you the command to run.

Servers without Ready Up (for example with the Auto Tournament CS2 plugin) show `no Ready Up` and behave exactly as before.

### Ready Up CI test host (`csm ci`)

`csm ci` turns a csm host into the test host for [Ready Up](https://github.com/Auto-Tournament/ready-up)'s real-server compatibility check: a separate CS2 install plus a GitHub Actions self-hosted runner labelled `readyup-live`. Run it as the CS2 user after the one-time `sudo csm setup-host` (it refuses root, so the runner never runs as root):

```bash
sudo -iu cs2servermanager
csm ci setup --token <registration token>   # [--repo Auto-Tournament/ready-up] [--dir ~/ru-ci] [--port 27095]
csm ci status
csm ci update                               # SteamCMD update of the CI install only
csm ci remove --token <removal token>       # [--purge] also deletes the runner files and the CI install
```

The registration token comes from the repository's **Settings → Actions → Runners → New self-hosted runner** and lasts an hour; the removal token comes from the runner's page there. csm only hands a token to the runner's `config.sh`: it is never written to disk, and it is redacted from csm's output and log.

`setup`:

1. checks that lingering is on for the user (`setup-host` enables it; otherwise `sudo loginctl enable-linger cs2servermanager`), since `systemd --user` services stop at logout without it;
2. downloads the latest `linux-x64` runner from [actions/runner](https://github.com/actions/runner/releases), checks the SHA256 published with the release, unpacks it into `~/actions-runner-readyup` and registers it as `<hostname>-readyup-live` with the label `readyup-live`;
3. writes `CS2_CI_DIR=<dir>` and `CS2_CI_PORT=<port>` into the runner's `.env`, so every job sees them;
4. installs CS2 into `--dir` with SteamCMD (`app_update 730 validate`, anonymous);
5. writes the user unit `~/.config/systemd/user/actions.runner.readyup.service` (it runs `run.sh`) and enables and starts it with `systemctl --user`.

It is safe to run again: an unpacked or registered runner is kept, the install is updated, and the service is restarted to pick up a new `--dir` or `--port`. If `config.sh` reports missing .NET dependencies, run `sudo ~cs2servermanager/actions-runner-readyup/bin/installdependencies.sh` once.

The CI install is **not one of the numbered servers**. It does not live in a `server-N` directory, so it is not in the server list or `csm status`, and `csm monitor`, auto-update, `update-game` and start/stop/restart never touch it. `csm ci` never starts, stops, restarts or updates a numbered server. The Ready Up workflow starts and stops the CI server itself. Give it a port that the numbered servers don't use (27095 by default). `--dir` must not be a server directory, the master install or the home directory, and `--purge` only deletes a directory that `csm ci setup` created.

Security: this is a self-hosted runner for a public repository, so the Ready Up workflow that uses it only runs on `schedule`, `workflow_dispatch` and `workflow_run`, never on `pull_request`: code from forks never reaches this host. The runner runs as the unprivileged CS2 user, not root, and the CI server is started with `+sv_lan 1`, so it doesn't advertise itself or accept Steam clients from the internet.

## Map thumbnails and maps.json

`csm extract-map-data` reads the map screenshots out of the master install's `pak01_dir.vpk` and writes them to `map_thumbnails/` in the current directory: a PNG, a full-size WEBP and a 1280px `_thumb.webp` per map. Next to them it writes `maps.json`, which the Auto Tournament platform can read to learn which maps exist and which are in the current Active Duty pool:

```json
{
  "generatedAt": "2026-09-25T08:55:32Z",
  "patchVersion": "1.41.1.4",
  "buildId": "20123456",
  "maps": [
    {
      "id": "de_dust2",
      "name": "Dust II",
      "mode": "defusal",
      "images": { "full": "de_dust2.webp", "thumb": "de_dust2_thumb.webp" },
      "variants": ["de_dust2_1_thumb.webp"]
    }
  ],
  "activeDuty": ["de_ancient", "de_dust2", "de_inferno"]
}
```

- `maps` holds every map VPK in `game/csgo/maps` (vanity scenes, `graphics_settings` and the like are skipped) plus every map with a screenshot. `mode` is `defusal`, `hostage`, `armsrace` or `other`; `images` is left out when the game ships no screenshot for that map.
- `activeDuty` is the `mg_active` map group from `gamemodes.txt` inside `pak01_dir.vpk`.
- `patchVersion` comes from `game/csgo/steam.inf` and `buildId` from `steamapps/appmanifest_730.acf`.
- If nothing but `generatedAt` would change, `maps.json` is left as it is.

The command prints each step as it goes, then one line per map (`[12/40] de_dust2 … updated`), and a summary at the end. It needs Python with the `vpk` and `Pillow` modules; run it once with sudo and csm sets them up in its own virtualenv.

To send the result to this repository, point `--publish` at a git checkout of it:

```bash
csm extract-map-data --publish --repo ~/cs2-server-manager
```

csm copies `map_thumbnails/` into the checkout, commits it on a new `maps/update-<timestamp>` branch and checks with `git push --dry-run` that you can push. It does not push or open the PR itself; it prints the `git push` and `gh pr create` commands to run. The checkout must have no uncommitted changes. csm uses your existing git setup for the commit author and push access, and never stores tokens.

## Launch modes

By default csm starts servers with Valve's `game/cs2.sh`, unchanged. It also installs `game/csm.sh`, which sets `LD_LIBRARY_PATH` to prefer the libraries bundled with CS2. That helps with `libserver.so` and `libv8` mismatches. You can also run the `cs2` binary directly, which is only meant for troubleshooting.

```bash
csm start --alternate      # use csm.sh
csm start --alternate 1    # just server 1
csm start --binary         # run the cs2 binary directly
```

`--alternate` and `--binary` work on `start`, `restart` and `debug`, and only apply to that one command. To use a launcher everywhere csm starts servers (including `update-plugins`, `update-game` and the monitor), set `CSM_LAUNCH_MODE=alternate` or `CSM_LAUNCH_MODE=binary`, for example `CSM_LAUNCH_MODE=alternate csm restart`. A flag overrides the variable. Every launcher gets the same `+matchzy_config_scope` argument (see below).

### Newer distros and Steam Runtime

On newer distributions such as Debian 13 and Ubuntu 25.04+, CounterStrikeSharp can fail to load under the system runtime ([CounterStrikeSharp #1024](https://github.com/roflmuffin/CounterStrikeSharp/issues/1024)). On those versions csm installs Steam Runtime (SteamRT3, app `1628350`) into `/home/<cs2user>/steamrt` and starts servers through its wrapper. Set `CSM_STEAMRT=1` to force this on, or `CSM_STEAMRT=0` to force it off.

## Metamod version

CounterStrikeSharp and MatchZy install from their latest releases. Metamod:Source is pinned to `2.0.0.1469`, because the two only work as a pair: CounterStrikeSharp v1.0.375 and newer need Metamod build 1467 or newer (with KHook support), and v1.0.374 and older fail on those builds with `Plugin uses old SourceHook Metamod build ... (17 < 18)`. v1.0.375 is also the release that supports the CS2 1.41.8.x update, so after that update run `csm update-plugins` to get both at once.

`csm update-plugins` reinstalls the whole plugin bundle, so it replaces a newer Metamod with the pinned build. To choose a different build, set `CSM_METAMOD_VERSION` to a [metamod-source release tag](https://github.com/alliedmodders/metamod-source/releases) (for example `2.0.0.1468`), or to `latest` for the newest prerelease.

## Disk usage: hardlinked VPKs

A full copy of `master-install` is about 67 GB, and nearly all of it is `*.vpk` archives that CS2 only reads. csm hardlinks the VPKs from `/home/<cs2user>/master-install/game` into each `server-N/game`, so each extra server costs about 1.2 GB on disk instead of 67 GB. The install wizard estimates about 71 GB for `master-install` plus about 2 GB per server.

- Only VPKs are shared. `cfg/`, `addons/`, `gameinfo.gi`, MatchZy data, demos and logs stay separate per server. Hardlinks are used instead of symlinks because symlinked game directories broke demo recording and per-server configs.
- `master-install` and the servers must be on the same filesystem (the default layout under `/home/<cs2user>` is). If linking fails, csm logs it once and copies instead.
- SteamCMD only updates `master-install` and writes changed files as new files, so the servers' links aren't modified. `update-game` then syncs each server: it rsyncs everything except `*.vpk` and `csgo/addons/`, deletes VPKs removed from master, and re-links every VPK atomically. A running server keeps reading the old file until it restarts.
- `CSM_VPK_HARDLINK=0` turns this off, and new syncs make full copies again.

To convert existing servers (`update-game` also re-links servers as it syncs them):

```bash
csm dedupe-vpk --dry-run   # what would be linked, and the estimated savings
csm stop
csm dedupe-vpk             # link VPKs whose size and mtime match master; prints disk usage before and after
csm start
```

`csm dedupe-vpk 2` handles only server-2. `--verify` byte-compares each file before linking, which is slow. Running it twice does nothing the second time, and VPKs that differ from master are reported and left alone. It refuses to run while target servers are running unless you pass `--allow-running`.

To undo it: `csm stop && csm dedupe-vpk --undo && csm start`. This needs about 70 GB free per server and checks first. Also set `CSM_VPK_HARDLINK=0` wherever csm runs (for example `CSM_VPK_HARDLINK=0 csm update-game`, and the monitor cron), or the next sync links them again.

## Several servers and the MatchZy database

By default every server on the machine uses one MySQL database (`matchzy` in the `matchzy-mysql` container), so match stats end up in one place.

MatchZy also stores per-server settings in that database: `matchzy_server_id`, the bootstrap URL and token, the remote log URL, the demo upload URL and so on. Older MatchZy builds key those rows by setting name only, so the last server to save wins and every server loads its values on start. A tournament manager like MAT then sees one server several times, matches get loaded twice, and results overwrite each other. This affects any install with 2 or more servers on shared MySQL.

The fix has two parts:

- [Auto Tournament CS2 1.4.28](https://github.com/Auto-Tournament/cs2-plugin/releases/tag/v1.4.28) and newer store those settings per server ([#17](https://github.com/Auto-Tournament/cs2-plugin/pull/17)). 1.4.26 added this but could not read `+matchzy_config_scope` ([#18](https://github.com/Auto-Tournament/cs2-plugin/pull/18), fixed in 1.4.27), and 1.4.27 could still load another server's `matchzy_server_id` ([#19](https://github.com/Auto-Tournament/cs2-plugin/pull/19), fixed in 1.4.28). It tells servers apart by bind address and port, but csm starts servers with `-ip 0.0.0.0`, so MatchZy would fall back to the machine name, which every server on the machine shares.
- csm therefore passes `+matchzy_config_scope <hostname>-server-<N>` (for example `cs2-server-1`) when it starts each server. The name comes from the server's directory, so it survives restarts, updates, reinstalls and port changes, and the hostname keeps two machines sharing one database apart. If you rename the machine, or several machines share a hostname, set `CSM_MATCHZY_SCOPE_PREFIX` (for example `eu-1`) wherever csm starts servers.

The install wizard's **MatchZy storage** option picks between shared MySQL (the default; needs the CS2 plugin 1.4.28+ with 2 or more servers) and SQLite per server, where each server keeps its own `matchzy.db`. SQLite works on any MatchZy build but stats aren't shared. For a non-interactive install use `MATCHZY_DB_ENGINE=sqlite csm bootstrap`. csm only rewrites `database.json` while it still contains the `__CSM_NOTE` marker. Remove the note and csm leaves the file alone.

`csm doctor` checks this under "MatchZy per-server config (shared database)". It fails when 2 or more servers report the same `matchzy_server_id`, or when servers share MySQL and run MatchZy older than 1.4.28 or without `+matchzy_config_scope`, and it prints how to fix it.

To migrate an existing install:

1. Update csm.
2. Run `csm update-plugins`. It installs the latest CS2 plugin, redeploys and restarts every server, which also picks up the new start argument. If you're already on 1.4.28 or newer, `csm restart` is enough. If you can't update MatchZy, set `"DatabaseType": "SQLite"` in `/home/<cs2user>/overrides/game/csgo/cfg/MatchZy/database.json` and `/home/<cs2user>/cs2-config/game/csgo/cfg/MatchZy/database.json`, then run `csm update-plugins`.
3. Reconfigure each server once from your tournament manager (in MAT, re-save or re-bootstrap each server). Until a server saves its own values it still reads the old shared ones.
4. Run `csm doctor` to confirm.

Match stats already in the shared database stay where they are.

## Releasing

Releases run from **Actions → Release → Run workflow**, with `mode` set to `patch`, `minor`, `major` or `explicit` (and `version` as `X.Y.Z` or `vX.Y.Z` when `mode=explicit`). The workflow runs `scripts/release.sh`, the same script used for local releases, and uploads `csm-linux-amd64` and `csm-linux-arm64`. It uses the repository's `GITHUB_TOKEN`. Set the `DISCORD_WEBHOOK_URL` secret for Discord notifications.

## Sponsors

Your logo here — [sponsor Auto Tournament](https://discord.gg/n7gHYau7aW) to be listed.

## License

PolyForm Noncommercial 1.0.0, see [LICENSE](LICENSE). Free for non-commercial use; commercial use (paid hosting, selling it, paid-entry events, business use) needs a license — see [pricing](https://autotournament.gg/pricing) and [LICENSING.md](LICENSING.md). The [Auto Tournament CS2](https://github.com/Auto-Tournament/cs2-plugin) plugin stays MIT.

## Links

- [Documentation](https://docs.sivert.io/docs/csm)
- [Troubleshooting](https://docs.sivert.io/docs/csm/user/troubleshooting)
- [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament), a web app for running tournaments on these servers
- [Auto Tournament CS2](https://github.com/Auto-Tournament/cs2-plugin), the CS2 plugin csm installs (formerly MatchZy Enhanced)
- [Issues](https://github.com/Auto-Tournament/cs2-server-manager/issues)
