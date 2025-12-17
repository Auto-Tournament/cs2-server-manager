# Auto Updates

CS2 Server Manager includes an automated update flow that works with the CS2 AutoUpdater plugin and SteamCMD.

## What gets automated

The auto-update system:

- Detects when the AutoUpdater plugin shuts servers down for a game update.
- Runs the game update via SteamCMD.
- Restarts servers after the update.
- Enforces a cooldown so you don’t get stuck in an update loop.

All of this is now orchestrated by the Go-native monitor built into `csm` (no separate shell script required).

## How the monitor script works

The auto-update monitor is designed to be run periodically (e.g., via cron) using the `csm` binary:

1. Checks if all servers are stopped.
2. Scans the latest CounterStrikeSharp logs for the AutoUpdater shutdown message.
3. Ensures an update hasn’t been processed too recently (1-hour cooldown).
4. Runs the Go-native game update and restarts servers.

Logs are written to:

```bash
sudo tail -f /var/log/cs2_auto_update_monitor.log
```

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
