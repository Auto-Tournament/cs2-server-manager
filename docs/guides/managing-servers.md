# Managing Servers

This guide covers everyday operations: starting, stopping, and inspecting your CS2 servers using the `csm` binary.

## Using `csm`

The main entrypoint for managing servers is the `csm` CLI/TUI:

```bash
sudo csm       # Launch interactive TUI (installs, updates, status, logs, cleanup)
```

From the TUI you can:

- **Install or repair servers** (wizard).
- **Start / stop / restart all servers**.
- **Check status and logs**.
- **Run game or plugin updates**.
- **Install / manage the auto-update monitor**.

You can also call common actions directly from the CLI:

```bash
sudo csm install-deps           # Install core system dependencies
sudo csm bootstrap              # Install/redeploy servers
sudo csm start                  # Start all servers
sudo csm stop                   # Stop all servers
sudo csm restart                # Restart all servers
sudo csm status                 # Show tmux status in the CLI
sudo csm update-game            # Update CS2 game files
sudo csm update-plugins         # Update plugins (download + deploy)
sudo csm monitor                # Run one iteration of the auto-update monitor
sudo csm install-monitor-cron   # Install cron-based auto-update monitor
sudo csm cleanup-all            # Danger: remove all CS2 data and user
```

## Consoles and logs via `csm`

Servers run inside tmux sessions for easy console access. The `csm` binary provides helpers:

```bash
sudo csm status          # See all server sessions
sudo csm attach 1        # Attach to server 1 console
sudo csm logs 1 100      # Show last 100 lines of console output
sudo csm list-sessions   # List all tmux sessions
sudo csm debug 1         # Run server 1 in foreground for debugging
```

When attached to a tmux session:

- Press **Ctrl+B, then D** to detach without stopping the server.
- Type commands directly into the CS2 console.

## Where servers live

By default, server directories are under the CS2 user’s home, for example:

```text
/home/cs2servermanager/server-1
/home/cs2servermanager/server-2
/home/cs2servermanager/server-3
```

Each server has its own `game` folder with CS2 binaries and configs. Shared configuration is managed via the repo’s `overrides/` directory (see **Guides → Configuration & Overrides**).
