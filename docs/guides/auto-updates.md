# Auto Updates

CS2 Server Manager includes an automated update flow that works with **MatchZy Enhanced’s built-in auto-updater** and SteamCMD.

MatchZy emits clear log markers when a CS2 update is available and when it is about to shut the server down to apply that update. The Go-native monitor built into `csm` watches for these markers and runs the game update for the affected server.

## Log markers

MatchZy writes the following lines to the server console/logs:

```text
[MATCHZY_UPDATE_AVAILABLE] required_version=<number>
[MATCHZY_UPDATE_SHUTDOWN] required_version=<number>
```

- `[MATCHZY_UPDATE_AVAILABLE]` – A new CS2 update is available according to Steam’s `UpToDateCheck`. MatchZy has detected the update but will **not restart yet** while a MatchZy match is active.
- `[MATCHZY_UPDATE_SHUTDOWN]` – MatchZy has decided it is **safe to restart** (no active match) and is about to execute `quit` so the server process can shut down for the update.

The `required_version=<number>` value is the app build that the server should update to.

## What gets automated

The auto-update system:

- Detects when MatchZy has safely shut a server down for a CS2 update.
- Runs the game update via SteamCMD against the shared master install.
- rsyncs the updated master into the specific `server-N`.
- Restarts only that server.
- Enforces a per-server cooldown so you don’t get stuck in an update loop.

All of this is orchestrated by the Go-native monitor built into `csm` (no separate shell script required).

## How the monitor works

The auto-update monitor is designed to be run periodically (e.g., via cron) using the `csm` binary:

1. For each server:
   - If its `cs2-N` tmux session is **running**, the monitor only notes `[MATCHZY_UPDATE_AVAILABLE]` markers (if present) and waits; MatchZy will decide when it is safe to restart.
   - If the session is **stopped**, the monitor scans the per-server tmux log (`/home/<CS2_USER>/logs/server-N.log`) for a `[MATCHZY_UPDATE_SHUTDOWN]` line.
2. When a shutdown marker is detected for a stopped server, the monitor:
   - Parses the `required_version=<number>` value.
   - Ensures an update hasn’t been processed too recently for that specific server (1‑hour per-server cooldown).
   - Runs a **per-server game update** via `UpdateServerWithContext`, which:
     - Updates the shared master CS2 install via SteamCMD.
     - rsyncs the updated master into `server-N`.
     - Restarts `server-N`.
3. While a per-server update is in progress, a small status marker file is written next to the tmux log so the TUI and CLI `status` view can show `UPDATING` instead of just `STOPPED` or `RUNNING`.

Monitor logs are written into the consolidated `csm.log` file (see the logging guide). Entries from the auto-update monitor are tagged with `[log=auto_update_monitor.log]` so you can filter them easily.

## Setting up a cron job

Example cron entry to check every 5 minutes (manual setup):

```bash
sudo crontab -e
```

Add a line like:

```cron
*/5 * * * * /path/to/csm monitor >/dev/null 2>&1
```

Make sure the path matches where you installed the `csm` binary.

You can also let CSM install the cronjob for you (recommended):

```bash
sudo csm install-monitor-cron          # default */5 interval
sudo csm install-monitor-cron "*/10"   # custom interval
```

Or from the TUI:

- Run `sudo csm`
- In the **Setup** tab, choose **“Install/redeploy auto-update monitor (sudo)”**

## Manual updates

You can always run updates yourself instead of (or in addition to) the monitor:

```bash
csm                        # Launch TUI, then choose “Update CS2 game files”
csm                        # Launch TUI, then choose “Deploy plugins to all servers”
```

Use manual updates if you prefer full control or while initially testing your setup.
