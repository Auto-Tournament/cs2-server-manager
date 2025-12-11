# Managing Servers

This guide covers everyday operations: starting, stopping, and inspecting your CS2 servers.

## Using `csm`

The main entrypoint for managing servers is the `csm` binary:

```bash
./csm            # Launch interactive TUI (non-privileged actions)
sudo ./csm       # Launch TUI with sudo for privileged actions (cron, cleanup, etc.)
./csm status     # Show tmux status in the CLI
```

Common CLI commands:

```bash
./csm install-deps           # Install core system dependencies (run with sudo)
./csm bootstrap              # Install/redeploy servers (reads env for options)
./csm start                  # Start all servers
./csm stop                   # Stop all servers
./csm restart                # Restart all servers
./csm update-game            # Update CS2 game files
./csm update-plugins         # Update plugins (download + deploy)
./csm monitor                # Run one iteration of the auto-update monitor
./csm install-monitor-cron   # Install cron-based auto-update monitor (run with sudo)
./csm cleanup-all            # Danger: remove all CS2 data and user (run with sudo)
```

## Using `csm` for consoles and logs

Servers run inside tmux sessions for easy console access. The `csm` binary provides convenient helpers:

```bash
sudo ./csm status          # See all server sessions
sudo ./csm attach 1        # Attach to server 1 console
sudo ./csm logs 1 100      # Show last 100 lines of console output
```

Other useful commands:

```bash
sudo ./csm start           # Start all servers
sudo ./csm start 1         # Start server 1 only
sudo ./csm stop 2          # Stop server 2
sudo ./csm restart 3       # Restart server 3
sudo ./csm list-sessions   # List all tmux sessions
sudo ./csm debug 1         # Run server 1 in foreground for debugging
```

When attached to a tmux session:

- Press **Ctrl+B, then D** to detach without stopping the server.
- Type commands directly into the CS2 console.

## Where servers live

By default, server directories are under the CS2 user’s home, for example:

```text
/home/cs2/server-1
/home/cs2/server-2
/home/cs2/server-3
```

Each server has its own `game` folder with CS2 binaries and configs. Shared configuration is managed via the repo’s `overrides/` directory (see **Guides → Configuration & Overrides**).
