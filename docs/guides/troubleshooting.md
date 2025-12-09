# Troubleshooting

This page collects common issues and how to diagnose them.

## Server won’t start

Run the server in debug mode to see full console output:

```bash
sudo ./scripts/cs2_tmux.sh debug 1
```

Check for:

- Missing or invalid configs in your overrides.
- Plugin load errors (CounterStrikeSharp, MatchZy, etc.).
- Port conflicts if you changed defaults.

## Plugin errors or crashes

First, try the built-in repair:

```bash
./manage.sh repair
```

Then check logs:

```bash
sudo ./scripts/cs2_tmux.sh logs 1 200
```

Look in the CounterStrikeSharp logs under each server’s `game/csgo/addons/counterstrikesharp/logs` directory for stack traces or error messages.

## Auto updates not working

- Verify the cron job is installed and points to the correct path.
- Inspect the monitor log:

```bash
sudo tail -n 200 /var/log/cs2_auto_update_monitor.log
```

- Confirm the AutoUpdater plugin is actually shutting servers down on updates.
- Run `./manage.sh update-game` manually to confirm updates work outside the monitor.

## Can’t connect to server

Check:

- Firewall rules: ensure game and GOTV ports are open (e.g., 27015/27020, 27025/27030, …).
- That the server is running:

```bash
./manage.sh status
sudo ./scripts/cs2_tmux.sh status
```

- Any errors in the server console or logs.

## When in doubt

- Re-run the installer or `./manage.sh install` to repair a broken installation.
- Temporarily remove custom overrides to see if a config change is causing the problem.
- Use `debug` mode on a single server until it runs cleanly, then roll changes out to others.

