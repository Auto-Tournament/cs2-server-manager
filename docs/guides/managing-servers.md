# Managing Servers

This guide covers everyday operations: starting, stopping, and inspecting your CS2 servers using the `csm` binary.

## Using `csm`

The main entrypoint for managing servers is the `csm` CLI/TUI:

```bash
sudo csm       # Launch interactive TUI (installs, updates, status, logs, cleanup)
```

The TUI has four main tabs:

- **Install** - Install dependencies, run the install wizard, set up auto-update cron
- **Updates** - Update CS2 game files, update plugins, update server configs (RCON, maxplayers, GSLT)
- **Servers** - View dashboard, logs, start/stop/restart servers, add/remove/reinstall servers
- **Tools** - MatchZy database management, edit configs, unban IPs, map thumbnails, public IP, force update CSM, CLI help

### Common CLI commands

```bash
# Setup & Installation
sudo csm install-deps           # Install core system dependencies
sudo csm bootstrap              # Install/redeploy servers (non-interactive)
sudo csm install-monitor-cron   # Install cron-based auto-update monitor

# Server Control
sudo csm start [server]         # Start all servers (or specific server number)
sudo csm stop [server]          # Stop all servers (or specific server number)
sudo csm restart [server]       # Restart all servers (or specific server number)
sudo csm status                 # Show tmux status overview

# Updates
sudo csm update-game            # Update CS2 game files after Valve update
sudo csm update-plugins         # Update plugins (download + deploy)
sudo csm update-server <n>      # Update a specific server instance
sudo csm monitor                # Run one iteration of the auto-update monitor

# Server Management
sudo csm reinstall <server>     # Completely rebuild a server from master (fixes corrupted files)
sudo csm update-config <server> # Regenerate server.cfg and autoexec.cfg (fast, no file copying)
sudo csm unban <server> <ip>    # Unban an IP address (use 0 for all servers)
csm list-bans <server>          # List all banned IP addresses for a server

# Cleanup
sudo csm cleanup-all            # Danger: remove all CS2 data and user
```

## Consoles and logs via `csm`

Servers run inside tmux sessions for easy console access. The `csm` binary provides helpers:

```bash
sudo csm status          # See all server sessions
sudo csm attach 1        # Attach to server 1 console
sudo csm logs 1 100      # Show last 100 lines of console output
sudo csm logs-file 1     # Print the raw log file path for server 1
sudo csm list-sessions   # List all tmux sessions
sudo csm debug 1         # Run server 1 in foreground for debugging
```

When attached to a tmux session:

- Press **Ctrl+B, then D** to detach without stopping the server.
- Type commands directly into the CS2 console.

## Scaling servers

You can add or remove servers without a full reinstall:

```bash
# Via TUI: Navigate to Servers tab → "Add servers" or "Remove servers"

# Via CLI (prompts are easier in TUI):
# Adding servers copies from master-install and applies your existing config
# Removing servers deletes the highest-numbered servers
```

## Fixing corrupted servers

If a server has corrupted game files (e.g., `gameinfo.gi` errors, crashes), use the reinstall command:

```bash
sudo csm reinstall 2     # Completely rebuilds server 2 from master-install
```

This will:
1. Stop the server
2. Delete the corrupted server directory
3. Copy fresh files from `master-install`
4. Apply all your existing settings (ports, RCON, hostname, GSLT)
5. Start the server automatically

Your configuration is preserved - only the game files are replaced.

## Updating configuration without reinstalling

If you just need to fix or regenerate `server.cfg` and `autoexec.cfg` (for example, after fixing config syntax errors), use the faster `update-config` command:

```bash
sudo csm update-config 1     # Regenerates configs for server 1 (takes < 1 second)
```

This is much faster than `reinstall` since it doesn't copy game files. It will:
1. Regenerate `server.cfg` with proper formatting
2. Regenerate `autoexec.cfg`
3. Fix file ownership
4. Restart the server to apply changes

## Managing IP bans

If an IP address gets banned for RCON hacking attempts (e.g., Docker network IPs being incorrectly banned), you can unban it:

```bash
# Remove IP from banned RCON requests (specific server)
sudo csm unban 1 172.19.0.3

# Remove IP from banned RCON requests (all servers)
sudo csm unban 0 172.19.0.3

# Clear all IPs banned for RCON attempts (specific server)
sudo csm unban-all 1

# Clear all IPs banned for RCON attempts (all servers)
sudo csm unban-all 0

# List banned IPs for a server
csm list-bans 1
```

**Note:** The server must be restarted for the unban to take effect. You can restart with `sudo csm restart <server>`.

You can also use the TUI: Navigate to **Tools** tab → **Unban IP address** or **Unban all IPs**.

## Where servers live

By default, server directories are under the CS2 user’s home, for example:

```text
/home/cs2servermanager/server-1
/home/cs2servermanager/server-2
/home/cs2servermanager/server-3
```

Each server has its own `game` folder with CS2 binaries and configs. Shared configuration is managed via the repo’s `overrides/` directory (see **Guides → Configuration & Overrides**).
