# Install: user mode and SteamCMD

The basic install is in the [README](../README.md#install).

## Run csm without sudo (user mode)

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

