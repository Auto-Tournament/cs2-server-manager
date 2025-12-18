# Troubleshooting

This page collects common issues and how to diagnose them.

## Server won’t start

Run the server in debug mode to see full console output:

```bash
sudo csm debug 1
```

Check for:

- Missing or invalid configs in your overrides.
- Plugin load errors (CounterStrikeSharp, MatchZy, etc.).
- Port conflicts if you changed defaults.

## Plugin errors or crashes

First, try the built-in repair:

```bash
csm update-plugins
```

Then check logs:

```bash
sudo csm logs 1 200
```

Look in the CounterStrikeSharp logs under each server’s `game/csgo/addons/counterstrikesharp/logs` directory for stack traces or error messages.

## Auto updates not working

- Verify the cron job is installed and points to the correct path.
- Inspect the monitor log entries in the consolidated `csm.log` file:

```bash
sudo tail -n 200 csm.log | sed -n '/\\[log=auto_update_monitor\\.log\\]/,$p'
```

- Confirm the AutoUpdater plugin is actually shutting servers down on updates.
- Run `csm update-game` manually to confirm updates work outside the monitor.

## Can’t connect to server

Check:

- Firewall rules: ensure game and GOTV ports are open (e.g., 27015/27020, 27025/27030, …).
- That the server is running:

```bash
csm status
```

- Any errors in the server console or logs.

## When in doubt

- Re-run the installer (Quick Start) or use the TUI install wizard to repair a broken installation.
- Temporarily remove custom overrides to see if a config change is causing the problem.
- Use `debug` mode on a single server until it runs cleanly, then roll changes out to others.
