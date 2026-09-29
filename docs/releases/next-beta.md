# Draft: CS2 Server Manager 1.12.0

> **Draft. Do not merge until the release.** Nothing has been tagged or published.
> Prepared 2026-09-29 against `master` `22a687d` (4 PRs since `v1.11.0`: #66–#69).
>
> csm has no beta channel: every release is a normal GitHub release, becomes "Latest" and is
> offered to every install by the built-in update check. This one goes out with the platform's
> 3.0.0-beta.14 and Ready Up's first beta (0.1.0-beta.1). Nothing changes for existing hosts
> unless you use the new commands.

---

## Release notes (paste into the GitHub release)

**CSM v1.12.0**

### New

- **Host agent: control this machine from Auto Tournament.** `csm link <platform-url> <code>`
  links the machine (get the command from Servers → Machines → Add machine), and
  `csm agent install` runs the agent as a systemd service. The platform can then list, start,
  stop, restart, create and update the machine's servers and see their health, with no SSH
  and no inbound port.
  - One outgoing WebSocket, `https`/`wss` only, certificates always verified. The token is
    stored with mode 600, rotated by the platform every 90 days, and never printed or logged.
  - A stop, restart or update of a server with a Ready Up match in progress is refused unless
    an admin gives a reason. The agent never restarts anything on its own.
  - `csm link status`, `csm unlink`, `csm agent status`.
- **Ready Up as the plugin stack.** `csm plugins stack readyup` installs
  [Ready Up](https://github.com/Auto-Tournament/ready-up) on every server instead of
  Metamod + CounterStrikeSharp + the old plugin.
  - Channel `stable` or `beta`, a pinned version, bundle `essentials` or `full`
    (`csm plugins channel|version|bundle`).
  - Every download is checked against the release's `SHA256SUMS`.
  - **Keeps itself up to date:** `csm monitor` updates Ready Up on its channel, only on
    servers that are stopped or idle with nobody connected, and never while updates are held.
    `csm plugins auto off` turns it off.
  - New servers get Ready Up before their first start. Servers the platform creates also get
    their link settings and join the platform by themselves.
  - After a CS2 update, csm puts the Ready Up line back into `gameinfo.gi`.
  - A host that runs the old stack stays on it. The README has a "Moving to Ready Up" section.
- **License answer, asked once.** Ready Up's installer needs to know whether you use it
  non-commercially or commercially. `csm update-plugins` asks once in a terminal (you type
  `I AGREE`), or set it with `csm plugins license noncommercial|commercial`.
  `AT_ACCEPT_LICENSE` overrides it. If you never answered, csm takes the answer from the
  platform. An answer you gave is never replaced.
- **Instance mode: one CS2 install, many servers.** `csm instance create` runs a server from
  the one shared, read-only CS2 install through overlayfs, instead of a full copy per server.
  Each instance only stores what it writes itself (logs, demos, backups). No root and no
  sudo: it uses an unprivileged user namespace (Linux 5.11 or newer). Each instance has its
  own ports, `HOME`, `/dev/shm`, console and crash restart. Ready Up only.
  - `csm instance create|start|stop|restart|status|remove|attach|logs|shell`,
    `csm instance layer ...`, `csm instance game`, `csm instance gc`.
  - `csm status`, `start|stop|restart`, `logs`, `attach` and the TUI work on instances.
  - With `csm instance config backend instances`, the host agent reports instances as the
    machine's servers, so the platform creates and scales instances.
- **Safe, versioned CS2 updates for instances.** A CS2 update never writes to an install a
  running server uses. csm builds a new game version next to the old one (hardlinked, so it
  costs only the changed files), and restarts idle instances onto it. Busy or held instances
  keep the old version until they are idle. Old versions are removed when nothing uses them.
  Ready Up updates work the same way: a new layer, never a change to the one in use.

### Changed

- **The legacy stack installs Auto Tournament CS2 1.4.35**, pinned, instead of whatever is
  latest (`CSM_LEGACY_PLUGIN_VERSION` picks another). On an Auto Tournament 3.x platform, csm
  refuses the legacy stack with a clear message before touching anything; use Ready Up there.

### Upgrading

- Update as usual (the TUI offers it, or download the new binary). No config changes.
  Existing `server-N` folders, `server.cfg` files and Ready Up data are kept, and nothing
  changes until you use the new commands.
- **To move a host to Ready Up** (Auto Tournament 3.0 betas):
  ```bash
  csm plugins stack readyup
  csm plugins channel beta              # while Ready Up has only pre-releases
  csm plugins license noncommercial     # or commercial
  csm update-plugins
  ```
- **To link the machine to the platform** (Auto Tournament 3.0.0-beta.14 or newer):
  ```bash
  csm link https://your-platform <code>
  csm agent install
  ```
- **To try instance mode:** `csm instance layer build`, then `csm instance create` and
  `csm instance start all`. See "Instance mode" in the README.

### Known limitations

- Instance mode needs Ready Up (not the legacy stack) and Linux 5.11 or newer. Instances save
  disk and setup time, not memory: each running CS2 server still needs its own RAM.
- The host agent only removes the last server, and doesn't change launch arguments yet.
- The TUI install wizard has no license field; it shows how to give the answer.

---

## How this release is cut (for the owner)

- **Where the version lives:** `src/internal/tui/version.go`, `const currentVersion = "1.11.0"`
  (no leading `v`). The license line date is baked in with `-ldflags`
  (`license.lineDate`, from `scripts/line-date.sh`).
- **Scheme:** plain `vX.Y.Z`, no pre-releases. New features → minor: **v1.12.0**.
- **How:** GitHub Actions → **Release** (`.github/workflows/release.yml`) → *Run workflow*,
  `mode: minor` (or `explicit` + `version: 1.12.0`). Or:
  `gh workflow run release.yml --repo Auto-Tournament/cs2-server-manager -f mode=minor`
  The workflow runs `scripts/release.sh`, which bumps `version.go`, commits
  `chore: release v1.12.0` **directly on master**, tags, pushes, builds
  `csm-linux-amd64` / `csm-linux-arm64` and runs `gh release create` with a one-line note.
- **Secrets/permissions:** `github.token` with `contents: write` (it must be allowed to push
  to master); optional `DISCORD_WEBHOOK_URL`.
- **Notes:** the script's notes are one line. Afterwards:
  `gh release edit v1.12.0 --notes-file <the part above the line>`.
- **Order:** after Ready Up 0.1.0-beta.1 is published (so `csm plugins channel beta` finds a
  release), before or together with platform 3.0.0-beta.14.
- **If you want this marked as a beta:** `release.sh` and `line-date.sh` only accept
  `X.Y.Z`, `gh release create` has no `--prerelease`, and the update check reads
  `releases/latest`. Since every new feature is opt-in and existing hosts behave as before,
  a normal 1.12.0 is the simpler choice.

---

## Readiness

| Check | Status |
|---|---|
| CI on `master` | Green on `22a687d`, `40334fc`, `c824c8e`. |
| Tests on real hardware | `scripts/instance-integration-test.sh` passed on the cs2 box (20 checks, isolated root, ports 27200+): isolation, crash restart, Ready Up update with idle restarts, update hold, two fake CS2 updates, master untouched. No real SteamCMD update was pulled through instance mode yet. |
| Open PRs | None besides this one. |
| Docs | README covers `csm link` / `csm agent`, `csm plugins`, "Moving to Ready Up" and "Instance mode". The docs site's csm pages (`cs2/server-manager/*`) don't cover these yet. README line 127 still says "Settings → Hosts → Add host"; the platform's UI is **Servers → Machines → Add machine**. README says commercial use "needs a paid license"; the rule is "you need a license". |
| Migrations | None. New files only: `fleet/credentials.json`, `plugins.json`, `instances/`. |
| Upgrade path (existing installs) | Binary swap. Hosts keep their stack and `server-N` folders. |
| Blockers | Ready Up 0.1.0-beta.1 must be published first, or the Ready Up stack has nothing to install. |

## Compatibility

| csm | Plugin stack | Ready Up | Platform |
|---|---|---|---|
| 1.10.x | legacy (latest AT CS2 plugin, 1.4.35) | status, live table, match protection; does not install it | update-hold poll gets 401 on 3.0 betas |
| 1.11.0 | legacy | + license key hand-off | poll works with 3.0 betas; license from platform ≥ 3.0.0-beta.14 |
| **1.12.0 (next)** | legacy (pinned 1.4.35, refused on 3.x) or **Ready Up** | installs and updates **0.1.0-beta.1** on the `beta` channel; instance mode | **3.0.0-beta.14**: Machines, `csm link`, auto-scaling, failover restart/create, license use. 2.x platforms: legacy stack as before. |
