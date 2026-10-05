# Releasing

Releases run from **Actions → Release → Run workflow**, with `mode` set to `patch`, `minor`, `major` or `explicit` (and `version` as `X.Y.Z` or `vX.Y.Z` when `mode=explicit`). The workflow runs `scripts/release.sh`, the same script used for local releases, and uploads `csm-linux-amd64` and `csm-linux-arm64`. It bakes the version line date (the release date of the tag's `x.y.0`, from `scripts/line-date.sh`) into the binary with `-ldflags` for license coverage; dev builds use their build date. It uses the repository's `GITHUB_TOKEN`. Set the `DISCORD_WEBHOOK_URL` secret for Discord notifications.

Release notes go in the GitHub release; the [docs changelog](https://docs.autotournament.gg/reference/changelog/csm) is generated from them, and [CHANGELOG.md](../CHANGELOG.md) only links there.

