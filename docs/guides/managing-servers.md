# Managing Servers

This guide covers everyday operations: starting, stopping, and inspecting your CS2 servers.

## Using `manage.sh`

The main entrypoint for managing servers is `manage.sh`:

```bash
./manage.sh
```

From the interactive menu you can:

- **Install servers** (initial setup or repair).
- **Start / stop all servers**.
- **Check status**.
- **Run game or plugin updates**.

You can also call common actions directly:

```bash
./manage.sh install          # Install servers
./manage.sh start            # Start all servers
./manage.sh stop             # Stop all servers
./manage.sh status           # Check status
./manage.sh update-game      # Update CS2
./manage.sh update-plugins   # Update plugins
./manage.sh repair           # Fix issues
```

## Using `cs2_tmux.sh` for consoles and logs

Servers run inside tmux sessions for easy console access. The helper script lives in `scripts/cs2_tmux.sh`:

```bash
sudo ./scripts/cs2_tmux.sh status      # See all server sessions
sudo ./scripts/cs2_tmux.sh attach 1    # Attach to server 1 console
sudo ./scripts/cs2_tmux.sh logs 1 100  # Show last 100 lines of console output
```

Other useful commands:

```bash
sudo ./scripts/cs2_tmux.sh start          # Start all servers
sudo ./scripts/cs2_tmux.sh start 1        # Start server 1 only
sudo ./scripts/cs2_tmux.sh stop 2         # Stop server 2
sudo ./scripts/cs2_tmux.sh restart 3      # Restart server 3
sudo ./scripts/cs2_tmux.sh list           # List all tmux sessions
sudo ./scripts/cs2_tmux.sh debug 1        # Run server 1 in foreground for debugging
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
