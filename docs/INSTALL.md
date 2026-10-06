# Install: user mode and SteamCMD

The basic install is in the [README](../README.md#install).

## Run csm without sudo (user mode)

csm runs as the account that owns the servers and refuses to run as root. Log in as that account and set the host up once:

```bash
sudo csm setup-host
```

It installs the system dependencies (and accepts the Steam license the `steamcmd` package asks for), runs `loginctl enable-linger` for your account, adds it to the `docker` group for the MatchZy MySQL container, gives it csm's files in your home (or `CSM_ROOT`) and the csm files root runs left in `/tmp`, and moves the auto-update monitor from root's crontab into yours. It is safe to run again, and it never starts, stops, restarts or updates a server. Log out and back in so the docker group applies, then run csm without sudo:

```bash
csm status
csm                         # TUI
```

Servers that are already running keep running: csm finds them in the same tmux server as before. Only `sudo csm install-deps`, `sudo csm self-update` and `sudo csm cleanup-all` still need root. On a host that already runs servers, use `sudo csm setup-host --skip-deps`: `apt-get install` can upgrade tmux, and a newer tmux client can't talk to the tmux server the running servers live in. `--skip-linger` skips `loginctl`, and `--skip-docker` leaves Docker alone (members of the `docker` group can control every container on the host; use SQLite or your own MySQL server then).

`csm self-update` run as the user can't replace `/usr/local/bin/csm`, so it installs the new binary into `~/.local/bin/csm`, which login shells put first in `PATH`. Run `csm install-monitor-cron` afterwards so cron uses it too.

## If `steamcmd` can't be installed (Debian/Ubuntu)

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

