# Draft: CS2 Server Manager 1.11.0

> **Draft. Do not merge until the release.** Nothing has been tagged or published.
> Prepared 2026-09-29 against `master` `1a5a404` (3 PRs since `v1.10.3`).
>
> csm has no beta channel: every release is a normal GitHub release, becomes "Latest" and is
> offered to every install by the built-in update check. This one goes out with the platform's
> 3.0.0-beta.14 and Ready Up's first beta; see "How this release is cut" for the choice.

---

## Release notes (paste into the GitHub release)

**CSM v1.11.0**

### New

- **Auto Tournament license key.** `csm license set <key>` stores your license key
  (or `csm license set < key.txt`), `csm license status` shows who it is licensed to and
  what it covers, and `csm license clear` removes it. csm checks the key offline. Nothing is
  ever blocked: a problem is a warning, and without a key csm prints one quiet line.
  The key is stored in `license.json` (mode 600) and is never printed in full or logged.
- **csm hands the key to Ready Up** on every server it manages
  (`game/csgo/cfg/readyup_license.cfg`, exec'd from `server.cfg`). Ready Up reads it at the
  next map load or restart.
- **The key can come from the platform.** When an admin saves or clears the key in Auto
  Tournament (Settings → License), the update monitor applies it on its next poll, with no
  command on the host. A key from the platform replaces one you set by hand; clearing it on
  the platform only removes a key that came from the platform.
- `csm status` shows the license line under the fleet table.

### Fixed

- **The update-hold poll works with Auto Tournament 3.0 again.** csm sent its server token
  only as `X-MatchZy-Token`, which the 3.0 platform no longer reads, so every poll got a 401
  and game updates stayed on hold. csm now sends `X-Auto-Tournament-Token` too.

### Changed

- Every server counts toward a license's server limit, including spare, practice and test
  servers. The wording is "you need a license"; it is still only a warning.

### Upgrading

- Update as usual (the TUI offers it, or download the new binary). No config changes.
  Existing servers, `server.cfg` files and Ready Up data are kept.
- If you run Auto Tournament 3.0 betas: update now, otherwise game updates stay held (see Fixed).
- If you set a key with `csm license set`, it stays until the platform sends a different one.

### Known limitations

- **csm does not install Ready Up.** Install it with Ready Up's `install.sh`. csm shows Ready
  Up's live state and refuses to stop or update a server during a Ready Up match (since
  1.10.0), and keeps Ready Up's files and data across game updates (since 1.10.2). It does
  not put the Ready Up line back into `gameinfo.gi` after a CS2 update: run Ready Up's
  installer again after each one.
- **The plugin csm installs is the latest stable Auto Tournament CS2 plugin, 1.4.35.**
  Auto Tournament 3.0 needs 2.0.0 or newer, which is still a pre-release. On a 3.0 platform,
  install 2.0.0 by hand until this is sorted.
- **Host agent (`csm link`) is not in this release.** Letting the platform start, stop and
  update servers through csm is in progress.

---

## How this release is cut (for the owner)

- **Where the version lives:** `src/internal/tui/version.go`, `const currentVersion = "1.10.3"`
  (no leading `v`). The license line date is baked in with `-ldflags`
  (`license.lineDate`, from `scripts/line-date.sh`).
- **Scheme:** plain `vX.Y.Z`, no pre-releases. New features → minor: **v1.11.0**.
- **How:** GitHub Actions → **Release** (`.github/workflows/release.yml`) → *Run workflow*,
  `mode: minor` (or `explicit` + `version: 1.11.0`). Or:
  `gh workflow run release.yml --repo Auto-Tournament/cs2-server-manager -f mode=minor`
  The workflow runs `scripts/release.sh`, which bumps `version.go`, commits
  `chore: release v1.11.0` **directly on master**, tags, pushes, builds
  `csm-linux-amd64` / `csm-linux-arm64` and runs `gh release create` with a one-line note.
- **Secrets/permissions:** `github.token` with `contents: write` (it must be allowed to push
  to master); optional `DISCORD_WEBHOOK_URL`.
- **Notes:** the script's notes are one line. Afterwards:
  `gh release edit v1.11.0 --notes-file <the part above the line>`.
- **If you want this marked as a beta:** `release.sh` and `line-date.sh` only accept
  `X.Y.Z`, `gh release create` has no `--prerelease`, and the update check reads
  `releases/latest` (which skips pre-releases). A `1.11.0-beta.1` would need those three
  changed. Since the changes are small and fix a bug that holds updates on 3.0 platforms,
  a normal 1.11.0 is the simpler choice.

---

## Readiness

| Check | Status |
|---|---|
| CI on `master` | Green on `1a5a404`, `c429d5b`, `a74be36`. |
| Open PRs | None. Host agent / `csm link` work is not pushed yet (platform side is `Auto-Tournament/auto-tournament#421`, draft). |
| Docs | README covers `csm license` and the update-hold poll contract. The docs site's license pages were merged (`Auto-Tournament/docs#20`, `#21`). |
| Migrations | None. `license.json` is new and gets `source` / `platform_revision` fields; nothing existing is rewritten. |
| Upgrade path (existing installs) | Binary swap. Keeps servers, configs and Ready Up data. |
| Blockers | Not for this release itself. For the coordinated set: csm installs cs2-plugin 1.4.35 while platform 3.0 needs ≥ 2.0.0 (`downloadMatchZy` in `src/internal/csm/plugins.go` reads `releases/latest`). Either publish cs2-plugin 2.0.0 as the latest release (which moves 2.x-platform users of csm onto 2.0.0 too) or let csm pick the plugin major to match the platform. |

## Compatibility

| csm | Installs AT CS2 plugin | Ready Up | Platform |
|---|---|---|---|
| 1.10.0–1.10.1 | latest stable (1.4.35) | status, live table, match protection; does not install it | update-hold poll gets 401 on 3.0 betas (≥ beta 9) |
| 1.10.2–1.10.3 | latest stable (1.4.35) | + keeps `csgo/readyup` across game updates | same 401 |
| **1.11.0 (next)** | latest stable (1.4.35) | + license key hand-off (`readyup_license_key`), read by Ready Up 0.1.0-beta.1 | poll works with 3.0 betas; license from platform ≥ 3.0.0-beta.14 (older platforms: nothing changes) |
| later | — | may install Ready Up once it has a release | host agent (`csm link`) with platform #421 |
