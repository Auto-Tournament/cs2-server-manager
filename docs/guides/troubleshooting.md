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
- Corrupted game files (see "Corrupted game files" section below).

## Corrupted game files

If you see errors like:

```
FATAL ERROR: Application unable to load gameinfo.gi file from directory "csgo"
Failed to load layered mod 'csgo_imported'.  Can't find 'csgo_imported/gameinfo.gi'
Segmentation fault
```

The server's game files are corrupted. **Solution: Reinstall the server**

```bash
sudo csm reinstall 2     # Replace 2 with your server number
```

This completely rebuilds the server from the master installation while preserving all your settings (ports, RCON password, hostname, GSLT token). The reinstall command will:

1. Stop the server
2. Delete the corrupted directory
3. Copy fresh game files from master-install
4. Reconfigure everything with your existing settings
5. Start the server automatically

Available via TUI: **Servers tab → "Reinstall a server"**

## Port binding conflicts

**Fixed in v1.5.3+**

Older versions had a bug where all servers tried to bind to port 27015, causing conflicts. If you're on an older version:

1. Update to the latest CSM version
2. Restart your servers: `sudo csm restart`

Each server now properly binds to its configured ports (27015, 27025, 27035, etc.).

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

- Confirm that MatchZy logs contain the expected update markers when Valve ships a new build, for example:
  - `[MATCHZY_UPDATE_AVAILABLE] required_version=14129`
  - `[MATCHZY_UPDATE_SHUTDOWN] required_version=14129`
- Run `csm update-game` manually to confirm updates work outside the monitor.

## Can’t connect to server

Check:

- Firewall rules: ensure game and GOTV ports are open (e.g., 27015/27020, 27025/27030, …).
- That the server is running:

```bash
csm status
```

- Any errors in the server console or logs.

## Updating configuration without reinstalling

If you need to change RCON password, max players, or GSLT token across all servers:

```bash
# Via TUI: Updates tab → "Update server configs"
# Or via CLI:
# (Edit the shared config files, then sync to all servers)
```

This updates configs without reinstalling the entire server.

## Getting help

If you're still stuck:

1. Check server logs: `sudo csm logs <server> 200`
2. Check log files directly: `sudo csm logs-file <server>`
3. Run in debug mode: `sudo csm debug <server>`
4. Join our [Discord community](https://discord.gg/n7gHYau7aW)
5. Open an issue on [GitHub](https://github.com/sivert-io/cs2-server-manager/issues)

## When in doubt

- For corrupted files: Use `csm reinstall <server>` to rebuild from master.
- For config issues: Use the TUI install wizard or `csm bootstrap` to repair.
- For plugin issues: Run `csm update-plugins` to refresh plugins.
- Temporarily remove custom overrides to see if a config change is causing the problem.
- Use `debug` mode on a single server until it runs cleanly, then roll changes out to others.
