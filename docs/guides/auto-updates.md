# Auto Updates

CS2 Server Manager includes an automated update flow that works with the CS2 AutoUpdater plugin and SteamCMD.

## What gets automated

The auto-update system:

- Detects when the AutoUpdater plugin shuts servers down for a game update.
- Runs the game update via SteamCMD.
- Restarts servers after the update.
- Enforces a cooldown so you don’t get stuck in an update loop.

All of this is now orchestrated by the Go-native monitor built into `csm` (no separate shell script required).

## How the monitor works

The auto-update monitor is designed to be run periodically (e.g., via cron) using the `csm` binary:

1. For each server:
   - If its `cs2-N` tmux session is **running**, it is left alone.
   - If the session is **stopped**, the monitor scans the per-server tmux log (`/home/<CS2_USER>/logs/server-N.log`) for the AutoUpdater shutdown message.
2. When that shutdown message is detected for a stopped server, the monitor:
   - Ensures an update hasn’t been processed too recently for that specific server (1-hour per-server cooldown).
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
