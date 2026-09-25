# Contributing to CS2 Server Manager

csm is written in Go (`go.mod` says 1.21). The CLI entry point is `src/cmd/cs2-tui`, the server logic is in `src/internal/csm` and the terminal UI is in `src/internal/tui`.

## Building and testing

```bash
git clone https://github.com/YOUR_USERNAME/cs2-server-manager.git
cd cs2-server-manager
go test ./...
GOOS=linux GOARCH=amd64 go build -o csm ./src/cmd/cs2-tui
```

csm only runs on Linux, and most commands need a real CS2 install. They run either as root (`sudo csm ...`) or, after a one-time `sudo csm setup-host`, as the CS2 user without sudo ("user mode"). Test changes both ways when they touch tmux, SteamCMD, file ownership or cron, on a Linux machine or VM you don't mind breaking.

Only `setup-host`, `install-deps`, `cleanup-all`, creating the CS2 user and the Docker MySQL container need root. For anything else, don't add a root check: use `requireRootOrCS2User` / `CanManageServers`, run commands as the CS2 user through `userShellCommand` and `steamcmdAsUser`, and guard `chown` with `canChown()` (see `src/internal/csm/usermode.go`).

## Contributor License Agreement

Before your first pull request can be merged, you sign the [CLA](../CLA.md) by
commenting on the pull request as the CLA bot asks. You keep your copyright;
the CLA lets the maintainer offer the project under both the non-commercial
licence and commercial licences.

## Pull requests

- Keep each PR to one fix or feature.
- Write commit messages that say what changed and why.
- Add or update tests in `src/internal/csm` when you change behaviour there.
- Update `README.md` or the [docs](https://docs.sivert.io/docs/csm) if a command, flag or environment variable changes.

## Reporting bugs

[Open an issue](https://github.com/sivert-io/cs2-server-manager/issues/new/choose) with what you did, what you expected, what happened, your distro and version, and the relevant part of `/opt/cs2-server-manager/logs/csm.log`. Output from `csm doctor` (as the CS2 user, or with sudo) helps too.

If you need other people to help test something (several players, different distros), use the **Community Request** issue template.

Be respectful and constructive.
