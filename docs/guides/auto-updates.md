# Auto Updates

CS2 Server Manager includes an automated update flow that works with the CS2 AutoUpdater plugin and SteamCMD.

## What gets automated

The auto-update system:

- Detects when the AutoUpdater plugin shuts servers down for a game update.
- Runs the game update via SteamCMD.
- Restarts servers after the update.
- Enforces a cooldown so you don’t get stuck in an update loop.

All of this is orchestrated by `scripts/auto_update_monitor.sh`.

## How the monitor script works

`auto_update_monitor.sh` is designed to be run periodically (e.g., via cron):

1. Checks if all servers are stopped.
2. Scans the latest CounterStrikeSharp logs for the AutoUpdater shutdown message.
3. Ensures an update hasn’t been processed too recently (1-hour cooldown).
4. Runs the update script and restarts servers.

Logs are written to:

```bash
sudo tail -f /var/log/cs2_auto_update_monitor.log
```

## Setting up a cron job

Example cron entry to check every 5 minutes:

```bash
sudo crontab -e
```

Add a line like:

```cron
*/5 * * * * /path/to/cs2-server-manager/scripts/auto_update_monitor.sh >/dev/null 2>&1
```

Make sure the path matches where you cloned or installed `cs2-server-manager`.

## Manual updates

You can always run updates yourself instead of (or in addition to) the monitor:

```bash
./manage.sh update-game      # Update CS2
./manage.sh update-plugins   # Update plugins
```

Use manual updates if you prefer full control or while initially testing your setup.

