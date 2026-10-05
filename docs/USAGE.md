# Usage


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
csm update-plugins         # install/update the plugin stack on every server (Ready Up or legacy)
csm plugins                # plugin stack, Ready Up channel/version/bundle/license, what each server has
csm monitor                # run the auto-update monitor once
csm updates hold on        # no automatic restarts; "off" to resume, "auto" to let the platform decide
csm updates platform <url> <token> # let Auto Tournament hold updates while a tournament runs
csm updates check          # ask the platform now whether updates are held
csm install-monitor-cron   # run the monitor from cron (the crontab of the user running it)
csm remove-monitor-cron

# Auto Tournament host agent (the platform starts, stops, creates and updates servers)
csm link <url> <code|key>  # link this machine to the platform (code from Servers → Machines → Add machine)
csm link status            # show the link (never the token)
csm agent install          # run the host agent as a systemd service (csm agent = foreground)
csm unlink                 # forget the link

# License (commercial use only; never blocks anything)
csm license set <key>      # store an Auto Tournament license key; Ready Up on every server gets it
csm license status         # check it offline
csm license clear

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
csm extract-map-data       # map thumbnails, map icons + maps.json into ./map_thumbnails
csm extract-skin-data      # weapon skin images + skins.json into ./skin_images

# Logs and debugging
csm attach 1               # attach to server 1's console (tmux)
csm debug 1                # run server 1 in the foreground
csm logs 1 100             # last 100 log lines for server 1
csm logs-file 1            # path to server 1's log file

# Removes all CS2 data and the CS2 user
sudo csm cleanup-all
```

## Holding updates during a tournament

The monitor restarts a server for a CS2 update once it has been idle for the grace period: nobody connected, no match loaded. That is a local judgement, and it stays right only until [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament) gives that server the next match of a running tournament.

Point csm at the platform and it asks before every restart:

```bash
csm updates platform https://cs.example.io "$SERVER_TOKEN"
csm updates check
```

The token is the platform's `SERVER_TOKEN` — the same fleet-wide token the plugin already uses for event webhooks and demo uploads, not a new secret. csm polls the platform, so the game server needs no inbound port. `CSM_PLATFORM_URL` and `CSM_PLATFORM_TOKEN` override the stored values for hosts that keep secrets out of files; the settings file is written owner-only either way.

Updates are then held while a tournament is in progress or any match is loaded or live. **If the platform cannot be reached, updates stay held** — csm will not restart a server while it cannot tell whether a tournament is running. Every skipped update says which of these it was in `auto_update_monitor.log`.

The same answer carries the platform's license key, so a key an admin saves or clears in the platform (Settings → License) reaches Ready Up on every server without anyone running `csm license set` — see [License key](REFERENCE.md#license-key). The poll contract: `GET /api/servers/update-hold` with `X-Auto-Tournament-Token: <SERVER_TOKEN>` answers `{"success":true,"hold":…,"reason":"…","license":{"key":"ATL1…"|null,"revision":"sha256:…"|"none"}}`; `license` is missing on older platforms and null when the platform could not read its key, and both mean "change nothing".

`csm updates hold on` and `off` are overrides that win over the platform; `csm updates hold auto` goes back to asking it. `csm update-game` and `csm update-server` run whatever the hold says; the only thing that stops them is a live match on a Ready Up server (next section).

Day-to-day operation, configuration and the update monitor are covered in [Managing Servers](https://docs.sivert.io/docs/csm/user/managing-servers), [Configuration & Overrides](https://docs.sivert.io/docs/csm/user/configuration) and [Auto Updates](https://docs.sivert.io/docs/csm/user/auto-updates).

## Ready Up servers: live status and match protection

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

Servers without Ready Up (for example with MatchZy Enhanced) show `no Ready Up` and behave exactly as before.

## Host agent: control this machine from Auto Tournament (`csm link`)

With the host agent, admins add a machine once and then start, stop, restart, create and update its servers from the [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament) web UI. No SSH, and no inbound port: csm keeps one outbound WebSocket to the platform (`wss://<platform>/api/fleet/host`). This is the hosts channel of Ready Up's [fleet protocol](https://github.com/Auto-Tournament/ready-up/blob/master/docs/FLEET.md) (§18); Ready Up's own connection per server stays for the match itself.

**1. Link the machine.** On the platform, open **Servers → Machines → Add machine** and copy the one-time code (valid 15 minutes). On the machine, as the CS2 user (or root):

```bash
csm link https://cs.example.io RUE-7F3K-9QX2-LM4D-P8TW
# or with a reusable fleet enrollment key, for scripted installs:
csm link https://cs.example.io rfk_…
# "-" reads the code or key from stdin, so it stays out of the shell history:
csm link https://cs.example.io - < key.txt
```

csm enrolls the machine (`POST /api/fleet/enroll` with `kind: "host"`) and stores the host id and host token in `<csm root>/fleet/credentials.json` (mode 0600; `/opt/cs2-server-manager/fleet/` by default). The token is never printed or logged. The machine is identified by a hash of `/etc/machine-id`, so linking the same machine again gives back the same host.

**2. Run the agent.**

```bash
csm agent install   # systemd unit csm-agent.service: a system unit as root,
                    # a systemd --user unit as the CS2 user (needs lingering: sudo csm setup-host)
csm agent status    # link + service state
journalctl -u csm-agent -f          # logs (journalctl --user -u csm-agent -f in user mode)
```

`csm agent` runs it in the foreground instead. Its lines also go to `csm.log`, prefixed `[agent]`.

**What the platform can do** (every command gets exactly one answer; long jobs report progress):

| Platform message | csm does |
|---|---|
| `host.servers.list` | sends the inventory: every `server-N` with ports, process state, and Ready Up's version, `install_id`, phase and `update_safe` |
| `server.start` / `server.stop` / `server.restart` | `csm start` / `stop` (console `quit`, then kill after the grace time) / `restart` |
| `server.create` | adds the next `server-N` like the TUI's add-server. With `enroll: true` csm writes `game/csgo/cfg/ReadyUp/fleet.cfg` (`url` + `enroll_key`, mode 0600) before the first start, so Ready Up enrolls itself (FLEET §4.1 B). On the Ready Up stack csm installs Ready Up on the new server before that first start, so it comes up enrolled with no further step |
| `server.remove` | removes the highest-numbered server (csm keeps `server-N` contiguous) |
| `host.update_game` | `csm update-game`, or `csm update-server N` for a list |
| `host.update_plugins` | installs Ready Up like `csm update-plugins` does (bundle `default` → essentials, `skins` → full; an instance host builds its shared layer with the full bundle unless `csm plugins bundle` says otherwise; version `latest` = the host's channel or pin), stopping and restarting running servers. The first one on a host without a stack choice sets it to Ready Up |
| `host.updates_hold` | `csm updates hold on\|off\|auto` |
| `logs.tail` / `logs.stop` | tails a server console log, CS2's log (`readyup`), or `csm.log` (`csm`, `monitor`), optionally following it |

The agent also reports **health**: `exited` when a server process stops without csm stopping it, `hung` when Ready Up's `/health` has not answered for 30 s, `recovered` and `restarted`. It never restarts anything on its own during a match; the platform (an admin) decides.

**Live matches.** `server.stop`, `server.restart`, `server.remove`, `host.update_game` and `host.update_plugins` are refused with `match_in_progress` for any server whose Ready Up says `update_safe: false`, exactly like the local `--force` gate. The platform can send `force` (root admins only, audited on the platform); csm then logs `FORCED …` with who and why.

**Where Ready Up comes from.** The same place as `csm update-plugins`: the GitHub release on the host's channel (`csm plugins channel`) or its pinned version (`csm plugins version`), checked against the release's `SHA256SUMS`, installed with the `install.sh` inside the bundle. A `version` other than `latest` from the platform wins over the pin. If there is no such release the platform gets `failed / no_release` with the reason (for example "no stable release yet, only pre-releases") and nothing is touched; csm never reports an install that did not happen. The license answer is `csm plugins license` (or `AT_ACCEPT_LICENSE`); without one the platform gets `license_not_accepted`. To install a zip that is not on GitHub, point the agent at it (a path, or an https URL with `{version}` / `{bundle}` placeholders):

```bash
csm agent config readyup_bundle /opt/readyup/ready-up-{bundle}.zip
csm agent config readyup_accept_license commercial   # overrides csm plugins license for the agent
csm agent config                                     # show all settings
```

**Security.** `https://` and `wss://` by default; `csm link --insecure` allows `http://` and `ws://` for a platform served over plain `http://ip:port` (the token then travels unencrypted, so prefer https). When you link over `https://` and the platform answers with a `ws://` URL for the same host (a TLS proxy that does not pass `X-Forwarded-Proto`), csm uses `wss://` instead. Certificates are always verified (`--ca-file` adds a private CA). The host token goes only in the `Authorization` header, is rotated by the platform every 90 days (`auth.rotate`, written atomically), and a revoked token makes the agent back off (and re-enroll by itself when it was linked with a fleet key). Tokens, keys and codes are redacted from logs and from everything sent back. Every inbound message is validated and frames are capped at 1 MiB. Commands older than 5 minutes (a replay after an outage) are refused, not run.

`csm unlink` forgets the link (revoke the host on the platform too). The message formats are JSON Schemas in [`protocol/host-v1/`](protocol/host-v1); the tests check every frame the agent sends against them.

## Ready Up CI test host (`csm ci`)

`csm ci` turns a csm host into the test host for [Ready Up](https://github.com/Auto-Tournament/ready-up)'s real-server compatibility check: a CI server plus a GitHub Actions self-hosted runner labelled `readyup-live`. Run it as the CS2 user after the one-time `sudo csm setup-host` (it refuses root, so the runner never runs as root):

```bash
sudo -iu cs2servermanager
csm ci setup --instance 9 --token <registration token>   # the CI server is instance 9 (port 27095)
csm ci status
csm ci remove --token <removal token>       # [--purge] also deletes the runner files and the CI server
```

The registration token comes from the repository's **Settings → Actions → Runners → New self-hosted runner** and lasts an hour; it is only needed while the runner is not registered yet. The removal token comes from the runner's page there. csm only hands a token to the runner's `config.sh`: it is never written to disk, and it is redacted from csm's output and log.

**On a csm instance (`--instance N`, recommended).** The CI server is instance N (game port `base + 10×N`: 27095 for 9), so it costs no CS2 copy, only the few MB each run writes. `setup` creates the instance if needed and makes it private: it is not one of the host's servers (the host agent, the fleet, `start all` and `csm monitor`'s restarts leave it alone). It writes `CS2_CI_INSTANCE=N`, `CS2_CI_DIR=<the instance's merged view>`, `CS2_CI_PORT` and `CS2_CI_CSM=<this csm>` into the runner's `.env`. Every CI run then:

1. empties the instance (`csm instance reset N`) and builds the freshly built bundle into a layer of its own (`csm instance layer build --for N --zip <bundle> --installer install.sh --bundle full`). That layer never becomes current, no other instance ever mounts it, and the previous run's layer is removed;
2. runs its own `cs2.sh` launch and the live tests inside `csm instance exec N -- <command>`: a namespace with the instance's view mounted at `CS2_CI_DIR`. Only one exec (or start) can use an instance at a time.

The CS2 build is the shared game version: csm keeps it updated for every instance (`csm monitor`, `csm instance update-game`); `csm ci update` only applies to a `--dir` install.

**On a CS2 install of its own (`--dir`, no `--instance`).** `setup` checks linger, downloads and registers the runner, writes `CS2_CI_DIR` and `CS2_CI_PORT` (`--port`, 27095 by default) into `.env`, copies the master install into `--dir` (default `~/ru-ci`, about 70 GB) and updates it with SteamCMD; `csm ci update` updates it later. In detail, `setup`:

1. checks that lingering is on for the user (`setup-host` enables it; otherwise `sudo loginctl enable-linger cs2servermanager`), since `systemd --user` services stop at logout without it;
2. downloads the latest `linux-x64` runner from [actions/runner](https://github.com/actions/runner/releases), checks the SHA256 published with the release, unpacks it into `~/actions-runner-readyup` and registers it as `<hostname>-readyup-live` with the label `readyup-live`;
3. writes `CS2_CI_DIR=<dir>` and `CS2_CI_PORT=<port>` into the runner's `.env`, so every job sees them;
4. installs CS2 into `--dir` with SteamCMD (`app_update 730 validate`, anonymous);
5. writes the user unit `~/.config/systemd/user/actions.runner.readyup.service` (it runs `run.sh`) and enables and starts it with `systemctl --user`.

It is safe to run again: an unpacked or registered runner is kept, the install is updated, and the service is restarted to pick up a new `--dir` or `--port`. If `config.sh` reports missing .NET dependencies, run `sudo ~cs2servermanager/actions-runner-readyup/bin/installdependencies.sh` once.

The CI server is **not one of the numbered servers**. A `--dir` install does not live in a `server-N` directory, so it is not in the server list or `csm status`, and `csm monitor`, auto-update, `update-game` and start/stop/restart never touch it; a CI instance is private (above). `csm ci` never starts, stops, restarts or updates a numbered server or another instance. The Ready Up workflow starts and stops the CI server itself. Give it a port that the numbered servers don't use (27095 by default). `--dir` must not be a server directory, the master install or the home directory, and `--purge` only deletes a directory that `csm ci setup` created.

Security: this is a self-hosted runner for a public repository, so the Ready Up workflow that uses it only runs on `schedule`, `workflow_dispatch` and `workflow_run`, never on `pull_request`: code from forks never reaches this host. The runner runs as the unprivileged CS2 user, not root, and the CI server is started with `+sv_lan 1`, so it doesn't advertise itself or accept Steam clients from the internet.

