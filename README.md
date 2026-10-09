<div align="center">
  <img src="assets/logo/csm-banner.png" alt="CS2 Server Manager (csm)" width="100%" />
  <p><strong>Command-line tool that installs and runs several CS2 servers on one Linux machine</strong></p>
  <p>
    <a href="https://github.com/Auto-Tournament/cs2-server-manager/releases/latest"><img src="https://img.shields.io/github/v/release/Auto-Tournament/cs2-server-manager?cacheSeconds=3600" alt="GitHub Release" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-PolyForm%20Noncommercial-blue.svg" alt="License: PolyForm Noncommercial" /></a>
    <a href="https://docs.autotournament.gg/cs2/server-manager"><img src="https://img.shields.io/badge/docs-docs.autotournament.gg-blue" alt="Docs" /></a>
    <a href="https://discord.gg/n7gHYau7aW"><img src="https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white" alt="Discord" /></a>
  </p>
</div>

<br />

csm installs CS2 with SteamCMD, puts a plugin stack on every server ([Ready Up](https://github.com/Auto-Tournament/ready-up), or the legacy Metamod + CounterStrikeSharp + [MatchZy Enhanced](https://github.com/Auto-Tournament/matchzy-enhanced)), runs each server in its own tmux session and keeps the game and plugins updated. It's for LAN organisers, small leagues and anyone running [Auto Tournament](https://github.com/Auto-Tournament/auto-tournament), which can then start, stop, create and update the servers itself.

## Features

- Interactive terminal UI and a plain CLI (`csm help`)
- Several CS2 servers on one machine, each in its own tmux session
- Instance mode: every server runs one shared install, so ten servers take about the disk space of one
- Ready Up installed and kept on its release channel on every server, or the legacy MatchZy Enhanced stack
- Automatic game and plugin updates from cron, never in the middle of a match
- Update hold while a tournament runs, set by you or by Auto Tournament
- `csm status`: process, map, phase, score and players of every server
- Host agent (`csm link`): Auto Tournament starts, stops, creates and updates servers on this machine
- Runs as its own user without sudo after a one-time `sudo csm setup-host`

## Install

On a Linux server, log in as the account the servers will run as, download the latest release to `/usr/local/bin/csm`, set up the host once, and start the installer:

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
sudo csm setup-host   # once: packages and lingering for your account
```

Then run csm without sudo. The installer sets up Ready Up and asks for its license:

```bash
csm                   # the interactive TUI installer
```

csm keeps its data in your home folder and logs to `~/logs/csm.log`. Configs you put in `overrides/` survive game and plugin updates. Running without sudo, and what to do when `steamcmd` can't be installed: [docs/INSTALL.md](docs/INSTALL.md).

## Usage

```bash
csm                  # interactive TUI
csm status           # every server: process, map, phase, score, players
csm start [server]   # start all servers, or one
csm stop [server]    # refuses while a Ready Up match is live
csm update-game      # update CS2
csm update-plugins   # install or update the plugin stack on every server
csm link <url> <code> # let Auto Tournament control this machine
csm link --name <club> <url> <code>  # and another platform (each sees only its own servers)
csm license set <key> # a commercial license key (free for non-commercial use)
csm license cap --link <club> 5      # this platform may create at most 5 servers here
```

### Several platforms on one host

A hosting provider can link one machine to several Auto Tournament platforms
(`csm link --name <name> ...`). Every server belongs to the platform that
created it; each platform sees, starts, stops, updates and removes only its own
servers, within the cap the host sets for it (`csm license cap --link`). Servers
made before the second link belong to the first. Updates are held while any
platform asks for a hold. `csm link status` lists the links and their servers;
`csm unlink <name>` removes one and gives its servers back to the first link.

Licenses: each platform's license goes to the Ready Up config of the servers it
owns. When the host sets a key of its own (`csm license set`), that key covers
every server here and is never overwritten by a platform; those servers count
toward the host's license, not the platform's.

Every command, update holds and the host agent: [docs/USAGE.md](docs/USAGE.md).

## Documentation

Full docs at **[docs.autotournament.gg](https://docs.autotournament.gg/cs2/server-manager)**. In this repo:

- [Install: user mode and SteamCMD](docs/INSTALL.md)
- [Usage: every command, update holds, host agent, CI host](docs/USAGE.md)
- [Reference: plugin stacks, instance mode, maps, disk, database, license keys](docs/REFERENCE.md)
- [Releasing](docs/RELEASING.md)

## Contributing

See the [contributing guide](.github/CONTRIBUTING.md). Bug reports go in [Issues](https://github.com/Auto-Tournament/cs2-server-manager/issues), questions on [Discord](https://discord.gg/n7gHYau7aW).

## Sponsors

csm is part of Auto Tournament, built by one person. If your organisation runs servers with it, a sponsorship pays for development and test servers: [GitHub Sponsors](https://github.com/sponsors/sivert-io) or [Ko-fi](https://ko-fi.com/sivert).

<!-- sponsors:start -->
<!-- sponsors:end -->

## License

csm is licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE). Copyright (c) 2025-2026 Sivert Gullberg Hansen. Free for non-commercial use; commercial use needs a license, see [pricing](https://autotournament.gg/pricing) and [LICENSING.md](LICENSING.md).
