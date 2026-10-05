# Reference

Plugin stacks, instance mode, maps, launch modes, disk use, the MatchZy database and license keys.

## Plugin stack: Ready Up or legacy

csm installs one of two plugin stacks on every server:

- **Ready Up** (`readyup`): [Ready Up](https://github.com/Auto-Tournament/ready-up), a native CS2 plugin suite. No Metamod, no CounterStrikeSharp. This is what Auto Tournament 3.x talks to.
- **Legacy** (`legacy`): Metamod:Source + CounterStrikeSharp + [MatchZy Enhanced](https://github.com/Auto-Tournament/matchzy-enhanced) 1.4.35, the MatchZy-era plugin (`matchzy_*` cvars) that Auto Tournament 2.x talks to.

```bash
csm plugins                          # what is chosen, what would be installed, what each server has
csm plugins stack readyup            # or legacy
csm plugins channel beta             # stable (default) or beta (pre-releases too)
csm plugins version v0.1.0-beta.2    # pin a release; "latest" follows the channel again
csm plugins bundle full              # essentials (default; instance mode: full) or full (adds skins and the extras)
csm plugins license noncommercial    # or commercial; asked once, then remembered
csm plugins auto off                 # stop csm monitor from updating Ready Up (default on)
csm update-plugins                   # install / update now (stops and restarts the servers)
```

**Which stack.** Nothing changes by itself on a host that already runs the legacy stack: it stays legacy until you switch. A fresh install gets Ready Up once Ready Up has a stable release, and the legacy stack until then (the install log says how to opt into a pre-release). The platform's **Update Ready Up** button (`host.update_plugins`) on a host that never chose a stack sets it to Ready Up. `CSM_PLUGIN_STACK=readyup|legacy` overrides the setting.

**Channels.** `stable` installs the latest stable Ready Up release (GitHub's "latest"). `beta` installs the newest release including pre-releases (`vX.Y.Z-beta.N`, `-rc.N`). A pinned version wins over the channel. While Ready Up has only pre-releases, `stable` finds nothing and says so, naming the newest pre-release; use `csm plugins channel beta` or pin it. `CSM_READYUP_CHANNEL`, `CSM_READYUP_VERSION` and `CSM_READYUP_BUNDLE` override the settings.

**How it installs.** csm downloads the bundle zip (`ready-up-essentials-*` or `ready-up-full-*`) and `SHA256SUMS` once, refuses a zip that is not listed or does not match, and runs the `install.sh` that ships inside the bundle on each server as the CS2 user: `install.sh <bundle> --dir server-N --yes --zip <zip> --accept-license=<answer>`. install.sh verifies the checksum again, lays out `game/csgo/readyup/`, keeps everything in `cfg/ReadyUp/` (including `fleet.cfg`) and `readyup.cfg`, and adds `Game csgo/readyup` to `gameinfo.gi`. After a CS2 update replaces `gameinfo.gi`, csm puts that line back on every server that has Ready Up. csm never passes the license key on the command line (it is in `cfg/readyup_license.cfg`, written by `csm license set` or taken from the platform) and never touches GSLT or `sv_setsteamaccount`. A server csm creates (`csm` add-server, or the platform's `server.create`) gets Ready Up before its first start.

**License answer.** Ready Up is free for noncommercial use (PolyForm Noncommercial 1.0.0); commercial use needs a license. install.sh needs your answer before an unattended install, and csm never picks one for you. It is asked once: `csm update-plugins` asks in a terminal (answer `I AGREE`), or set it with `csm plugins license noncommercial|commercial`. `AT_ACCEPT_LICENSE=noncommercial|commercial` (the same variable as the platform) overrides it. When no answer is saved and the platform hands over a license key (see [License key](REFERENCE.md#license-key)), csm records `commercial`, since a key is a commercial license; a platform that sends `license.use` in the update-hold answer sets it directly. An answer you gave is never replaced.

**Automatic updates.** On the Ready Up stack every `csm monitor` cycle (cron, every 5 minutes) keeps Ready Up on its channel. It asks GitHub at most every 30 minutes. A server is updated only when updates are not on hold (`csm updates hold`, or the platform's update-hold while a tournament runs) and it is stopped, or Ready Up reports `update_safe: true` with nobody connected for the idle grace period (`csm updates grace`). Never mid-match. A failed update is retried after an hour. Each cycle logs what it did to `csm.log`.

**Legacy stack version.** The legacy stack installs MatchZy Enhanced **v1.4.35**, no longer "whatever is latest" (the plugin repo's next major is not what Auto Tournament 2.x expects). `CSM_LEGACY_PLUGIN_VERSION` picks another tag, or `latest`. When csm knows the platform (`csm updates platform`, or `csm link`) it asks it for its version first, and refuses the legacy stack for Auto Tournament 3.x with a message that points here; nothing on the servers changes. An unreachable platform does not block the install.

### Moving to Ready Up

csm does not migrate a legacy host by itself. To move one:

1. `csm plugins stack readyup`, then `csm plugins channel beta` while Ready Up has only pre-releases, and `csm plugins license noncommercial` (or `commercial`).
2. `csm update-plugins`. It installs Ready Up on every server and restarts them. The legacy `addons/` (Metamod, CounterStrikeSharp, MatchZy) stay where they are; Ready Up runs alongside Metamod.
3. Check each server: `ru selftest` in its console should say PASS, and `csm status` shows the Ready Up version.
4. Link the servers to the platform: `csm link` for the host agent, and `cfg/ReadyUp/fleet.cfg` (`url`, `enroll_key`) per server, which csm writes for servers the platform creates.
5. When nothing uses the legacy plugin any more, remove the `Game csgo/addons/metamod` line from each `game/csgo/gameinfo.gi` (or run the install wizard with Metamod off) and delete `game/csgo/addons/`. MatchZy's database and configs are not used by Ready Up.

## Instance mode (one install, many servers)

A `server-N` folder is a full copy of the CS2 install. Instance mode doesn't copy anything: every instance runs the one `master-install` read-only, and only stores the files it writes itself. Ten servers cost about as much disk as one.

### How much disk?

| Servers | Instance mode | Classic mode (full copy per server) |
|---|---|---|
| 1 | ≈ 69 GB | ≈ 138 GB (master + copy) |
| 10 | ≈ 69.1 GB | ≈ 760 GB |

- The CS2 install (`master-install`) is about 69 GB. In instance mode all servers run that one install read-only.
- Each instance's own writable files are about 10 MB (6.7 to 14 MB, mostly its demo) plus about 1 MB of Steam home. The Ready Up layer, shared by all instances, is about 9 MB.
- Classic mode is less than the table shows if `csm dedupe-vpk` hardlinks the VPKs (see *Disk usage: hardlinked VPKs*).
- Start time is about 5 to 7 seconds per instance until "server started" (4.6 to 7.1 s measured).
- RAM is not shared in any meaningful way: each server still uses about 1 to 2 GB.
- Demos (kept 24 h) and round backups (72 h) add to this over time.

CS2's size changes with updates, so treat these as a snapshot. Measured on one CS2 host in September 2026 with CS2 1.41.8.5, using `du` with hardlinks counted once.

It works with kernel overlayfs inside an unprivileged user namespace (`unshare --user --map-root-user --mount`). No root, no sudo, no fuse-overlayfs; it needs Linux 5.11 or newer. Each instance sees three layers merged together, and only it can see the result:

| Layer | Where | What |
|---|---|---|
| upper | `~/instances/instance-N/upper` | everything instance N writes: logs, demos, backups, Ready Up's `state.json` and `status.json`, `steam_appid.txt`, its `fleet.cfg` |
| Ready Up layer | `~/instances/layers/<id>` | Ready Up, installed once by its own `install.sh`, plus the patched `gameinfo.gi` |
| game | `~/master-install`, then `~/instances/games/<id>` | the CS2 install the layer was built on. Never written to (see *CS2 updates* below) |

Each instance also gets its own `HOME` (Steam writes to `$HOME/Steam`) and, by default, its own `/dev/shm`. Instance N uses game port `base + 10×N`, GOTV `+1`, client port `+2` and the Ready Up status port `+7`. The default base is 27005, so instance 1 plays on 27015 like `server-1` does.

```bash
csm instance layer build             # install the configured Ready Up release into a shared layer
csm instance create                  # instance 1 (then 2, 3, ...)
csm instance start all
csm instance status                  # state, ports, Ready Up phase, layer, pending restarts
csm instance attach 1                # the server console (Ctrl-b d to leave)
csm instance logs 1                  # console log tail
csm instance shell 1                 # a shell in a stopped instance's merged view
csm instance exec 1 -- ls game/csgo  # a command in a stopped instance's merged view
csm instance reset 1                 # empty a stopped instance (delete what it wrote)
csm instance stop 1
csm instance remove 1                # deletes the instance and everything it wrote
```

With `csm instance config backend instances`, the everyday commands act on instances too: `csm status` (and `--watch`, `--json`) lists them with the current layer, CS2 version and pending restarts; `csm start|stop|restart [N]`, `csm logs N` and `csm attach N` take an instance number; the TUI's dashboard and start/stop/restart-all use the instances. `csm stop` and `csm restart` refuse a busy instance unless you add `--force`, as they do for `server-N`.

**Settings.** Each instance has two cfg files in its `upper/game/csgo/cfg/`:

| File | Who writes it | What |
|---|---|---|
| `instance.cfg` | csm, on every start (your edits are lost) | hostname, RCON password, `sv_hibernate_when_empty 0`, `tv_enable 1`, `log on` (Ready Up reads the match log), the license key |
| `instance_custom.cfg` | you; csm never touches it | your own settings. `instance.cfg` runs it last, so it wins |

Don't edit Ready Up's own files in an instance: a changed copy of a layer file hides every later update of that file from that instance. `csm instance status` and every update warn about such copies.

**Running.** Each instance runs in tmux session `cs2-inst-N` under a small supervisor script. If CS2 exits without `csm instance stop` (a crash, or `quit` in the console), the supervisor starts it again after 10 seconds. It gives up after 5 exits in 10 minutes. The console log is `~/instances/instance-N/console.log`.

**Plugins.** All instances share one Ready Up layer, so csm builds it with the `full` bundle: every plugin (match, practice, essentials, skins, midas, whitelist, deathmatch, addons) is in it, and each server only turns plugins on or off. The platform does that per server with `cmd plugins.set`, which Ready Up keeps in the instance's own `plugins.json` (in its upper directory), so one instance can run a tournament and the next one practice or deathmatch. A platform request for the default bundle still builds `full`; `csm plugins bundle essentials` (or `CSM_READYUP_BUNDLE`) makes the layer essentials only. A layer built before this keeps its bundle until the next `csm instance update`. Classic `server-N` folders are unchanged: they get essentials unless you choose full.

**Updates.** An update happens once for all instances:

- **Ready Up**: `csm instance update` (or `csm monitor` following `csm plugins channel/version`) builds a new layer next to the old one and makes it current. Running instances keep the layer they started with.
- **CS2**: `csm instance update-game` (or `csm update-game`, or `csm monitor` when an instance logs that an update is out) makes a new game version once, then rebuilds the Ready Up layer on it, because `gameinfo.gi` comes from the game.

After either one, csm restarts only idle instances onto the new version. An instance is idle when Ready Up reports `update_safe: true` and nobody is connected. A busy instance keeps running and shows *restart pending*. `csm monitor` restarts it once it has been idle for the grace period (`csm updates grace`). Nothing restarts while updates are on hold (`csm updates hold`, or the platform's hold during a tournament), and never mid-match. `csm instance layer use <id>` switches back to an older layer; csm keeps the previous one on the same CS2 version.

**Private layers.** `csm instance layer build --for N --zip Z --installer I` builds a layer for instance N alone: `layers/current` does not move, N mounts it through its pin (`instance-N/layer.pin`) from its next start or `csm instance exec`, and no other instance ever does. N's previous private layer is removed. A pinned instance is not one of the host's servers (host agent, fleet and `start all` skip it). `csm instance layer unpin N` puts it back on the shared layer. Ready Up's CI uses this (`csm ci setup --instance N`).

**CS2 updates.** A running instance has its CS2 install mounted, and changing files under a mounted overlay is undefined. So csm never runs SteamCMD on an install an instance may be using:

1. SteamCMD runs against a temporary overlay: the current version below, an empty directory on top. Everything it writes, replaces, trims or deletes lands on top; the current version is not touched.
2. The new version, `~/instances/games/<id>`, is a hardlinked copy of the current one (`cp -al`) with those changes applied: changed files replace their link, deleted ones go. Unchanged files are shared, so a version costs only the files the update changed (a few GB rather than ~70). Where hardlinks are not possible (another filesystem, or a master owned by another user with `fs.protected_hardlinks`), it is a full copy, reflinked where the filesystem can; csm checks the free space first.
3. The Ready Up layer is rebuilt on the new version and records which version it sits on. `games/current` points at the newest.

A plain hardlinked copy is not enough on its own: SteamCMD replaces most changed files with new ones, but it trims a file that only lost trailing bytes in place, which would change it for every version sharing that file. The overlay in step 1 catches that.

Instances restart onto the new layer and version as above; until then they keep their old ones. An old version stays as long as a layer or a running instance uses it; csm removes it after that (`csm monitor`, the next update, or `csm instance gc`). `csm instance game` lists the versions. `~/master-install` is only the first version: instance mode never writes to it and never removes it. With `CSM_INSTANCE_MASTER_READONLY=1` csm makes no game versions, and whatever updates the master install does so under running instances.

**Host agent.** With `csm instance config backend instances`, the host agent reports instances as the host's servers (instance N is `server-N` to the platform), and `server.create`, `start`, `stop`, `restart` and `remove` act on instances. A server the platform creates gets its own `fleet.cfg` (platform URL and enroll key) in its upper layer and enrolls itself; the platform links it. `host.update_plugins` updates the shared layer (with the layer bundle, see *Plugins* above), and `host.update_game` makes a new game version once. An instance host can create its first server from the platform. The default backend stays `servers`, and nothing changes for existing `server-N` hosts.

**Other settings.** `csm instance config` shows and sets `base_port`, `map`, `max_players`, `private_shm on|off`, `nice` (the game's CPU priority) and `insecure on|off` (start cs2 with `-insecure`, no VAC: for test servers, e.g. testing with a VAC-banned account; restart the instances after changing it). Environment overrides: `CSM_SERVER_BACKEND`, `CSM_INSTANCE_ROOT`, `CSM_MASTER_DIR`, `CSM_INSTANCE_BASE_PORT`, `CSM_INSTANCE_NICE`, `CSM_STEAMCLIENT`, and `CSM_INSTANCE_MASTER_READONLY=1` if something else keeps the master install updated. Instances never get a GSLT.

**Testing.** `scripts/instance-integration-test.sh` runs two real instances against a test copy of a CS2 install and checks the isolation, the crash restart, the shadow warning, a Ready Up update with idle restarts, the update hold, two CS2 updates with a fake SteamCMD (hardlinked versions, nothing written through to the running version, held instances keeping theirs, old versions collected), the plain `csm status/stop/start/logs` commands, and that the master install is left untouched. It needs two Ready Up bundle zips; the header has the details.

## Map thumbnails and maps.json

`csm extract-map-data` reads the map screenshots out of the master install's `pak01_dir.vpk` and writes them to `map_thumbnails/` in the current directory: a PNG, a full-size WEBP and a 1280px `_thumb.webp` per map, plus the game's small map badge as `<map>_icon.svg`. Next to them it writes `maps.json`, which the Auto Tournament platform can read to learn which maps exist and which are in the current Active Duty pool:

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
      "images": { "full": "de_dust2.webp", "thumb": "de_dust2_thumb.webp", "icon": "de_dust2_icon.svg" },
      "variants": ["de_dust2_1_thumb.webp"]
    }
  ],
  "activeDuty": ["de_ancient", "de_dust2", "de_inferno"]
}
```

- `maps` holds every map VPK in `game/csgo/maps` (vanity scenes, `graphics_settings` and the like are skipped) plus every map with a screenshot. `mode` is `defusal`, `hostage`, `armsrace` or `other`; `images` is left out when the game ships neither a screenshot nor an icon for that map.
- `activeDuty` is the `mg_active` map group from `gamemodes.txt` inside `pak01_dir.vpk`.
- `patchVersion` comes from `game/csgo/steam.inf` and `buildId` from `steamapps/appmanifest_730.acf`.
- If nothing but `generatedAt` would change, `maps.json` is left as it is.

The command prints each step as it goes, then one line per map (`[12/40] de_dust2 … updated`), and a summary at the end. It needs Python with the `vpk` and `Pillow` modules; run it once with sudo and csm sets them up in its own virtualenv.

To send the result to this repository, point `--publish` at a git checkout of it:

```bash
csm extract-map-data --publish --repo ~/cs2-server-manager
```

csm copies `map_thumbnails/` into the checkout, commits it on a new `maps/update-<timestamp>` branch and checks with `git push --dry-run` that you can push. It does not push or open the PR itself; it prints the `git push` and `gh pr create` commands to run. The checkout must have no uncommitted changes. csm uses your existing git setup for the commit author and push access, and never stores tokens.

## Skin images and skins.json

`csm extract-skin-data` reads the weapon skin previews out of `pak01_dir.vpk` and writes them to `skin_images/` in the current directory: one WEBP per weapon and paint kit, at most 512 px wide, from the game's "light" wear preview. Next to them it writes `skins.json`, with the names from `scripts/items/items_game.txt` and `resource/csgo_english.txt`:

```json
{
  "skins": [
    {
      "weapon": "weapon_ak47",
      "weaponName": "AK-47",
      "paintKit": 180,
      "paintKitName": "cu_fireserpent_ak47_bravo",
      "name": "Fire Serpent",
      "rarity": "ancient",
      "image": "weapon_ak47_cu_fireserpent_ak47_bravo.webp"
    }
  ]
}
```

- `paintKit` is the id a skin plugin sets; `rarity` is the paint kit's rarity (`common` … `ancient`).
- Previews with no matching paint kit (pets, a few odd names) are skipped and counted at the end.
- An image whose bytes did not change is left as it is, so a rerun after a CS2 update only rewrites what changed.

The previews are LZ4-compressed textures, not PNGs: csm decodes them itself, with the `lz4` Python module when it is installed and its own decoder when not. A full run takes a few minutes.

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

This is the legacy stack. CounterStrikeSharp installs from its latest release and MatchZy Enhanced is pinned (see [Plugin stack](REFERENCE.md#plugin-stack-ready-up-or-legacy)). Metamod:Source is pinned to `2.0.0.1469`, because the two only work as a pair: CounterStrikeSharp v1.0.375 and newer need Metamod build 1467 or newer (with KHook support), and v1.0.374 and older fail on those builds with `Plugin uses old SourceHook Metamod build ... (17 < 18)`. v1.0.375 is also the release that supports the CS2 1.41.8.x update, so after that update run `csm update-plugins` to get both at once.

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

- [MatchZy Enhanced 1.4.28](https://github.com/Auto-Tournament/matchzy-enhanced/releases/tag/v1.4.28) and newer store those settings per server ([#17](https://github.com/Auto-Tournament/matchzy-enhanced/pull/17)). 1.4.26 added this but could not read `+matchzy_config_scope` ([#18](https://github.com/Auto-Tournament/matchzy-enhanced/pull/18), fixed in 1.4.27), and 1.4.27 could still load another server's `matchzy_server_id` ([#19](https://github.com/Auto-Tournament/matchzy-enhanced/pull/19), fixed in 1.4.28). It tells servers apart by bind address and port, but csm starts servers with `-ip 0.0.0.0`, so MatchZy would fall back to the machine name, which every server on the machine shares.
- csm therefore passes `+matchzy_config_scope <hostname>-server-<N>` (for example `cs2-server-1`) when it starts each server. The name comes from the server's directory, so it survives restarts, updates, reinstalls and port changes, and the hostname keeps two machines sharing one database apart. If you rename the machine, or several machines share a hostname, set `CSM_MATCHZY_SCOPE_PREFIX` (for example `eu-1`) wherever csm starts servers.

The install wizard's **MatchZy storage** option picks between shared MySQL (the default; needs the CS2 plugin 1.4.28+ with 2 or more servers) and SQLite per server, where each server keeps its own `matchzy.db`. SQLite works on any MatchZy build but stats aren't shared. For a non-interactive install use `MATCHZY_DB_ENGINE=sqlite csm bootstrap`. csm only rewrites `database.json` while it still contains the `__CSM_NOTE` marker. Remove the note and csm leaves the file alone.

`csm doctor` checks this under "MatchZy per-server config (shared database)". It fails when 2 or more servers report the same `matchzy_server_id`, or when servers share MySQL and run MatchZy older than 1.4.28 or without `+matchzy_config_scope`, and it prints how to fix it.

To migrate an existing install:

1. Update csm.
2. Run `csm update-plugins`. It installs the latest CS2 plugin, redeploys and restarts every server, which also picks up the new start argument. If you're already on 1.4.28 or newer, `csm restart` is enough. If you can't update MatchZy, set `"DatabaseType": "SQLite"` in `/home/<cs2user>/overrides/game/csgo/cfg/MatchZy/database.json` and `/home/<cs2user>/cs2-config/game/csgo/cfg/MatchZy/database.json`, then run `csm update-plugins`.
3. Reconfigure each server once from your tournament manager (in MAT, re-save or re-bootstrap each server). Until a server saves its own values it still reads the old shared ones.
4. Run `csm doctor` to confirm.

Match stats already in the shared database stay where they are.

## License key

csm is free for non-commercial use, with no key. For commercial use, paste your [Auto Tournament license](https://autotournament.gg/pricing) key once:

```bash
csm license set ATL1.xxxxx.yyyyy     # or pipe it in: csm license set < key.txt
csm license status
```

```
Licensed to NTLAN · Servers S (6 servers) · event 2026-10-16 · valid
License id: L-3kq8Zx0bQ1aR
Check it:   https://autotournament.gg/verify/L-3kq8Zx0bQ1aR
```

- csm checks the key offline (Ed25519 signature, no network). `csm status` shows the same line under the fleet table; without a key it says so in one line.
- The key is stored in `/opt/cs2-server-manager/license.json` (mode 600). csm never prints the whole key, only the license id.
- csm hands the key to Ready Up on every server: it writes `game/csgo/cfg/readyup_license.cfg` (mode 600, setting `readyup_license_key`) and adds `exec readyup_license.cfg` to each `server.cfg`. `update-config`, `bootstrap` and `reinstall` keep it there. Servers pick it up at the next map load or restart. `csm license clear` removes both again.
- **From the platform.** When csm is pointed at Auto Tournament (`csm updates platform <url> <token>`), the update monitor applies the key saved in the platform, the same way `csm license set` / `csm license clear` would, whenever it changes (the answer's `revision`). Precedence: a key from the platform wins, and replaces one set with `csm license set`; when the platform has no key, csm clears only a key it got from the platform and leaves one set by hand alone; when the platform can't be asked, nothing changes. A failed hand-off is a warning in `auto_update_monitor.log` and is tried again at the next poll. `csm license status` shows when the key came from the platform. The log names the license id, never the key.
- **Nothing is ever blocked.** A key that doesn't match, an event window that has ended, a release line newer than the license's updates, or more servers than the license covers are warnings only. csm counts every `server-N` it set up, spares and test servers included, and the server warning says so.
- Both a Servers and a Platform license cover csm. A release is covered when its version line (the release date of its `x.y.0`) is on or before the license's `updates_until`; founder licenses cover every line.

