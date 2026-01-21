# Configuration & Overrides

CS2 Server Manager is designed so your custom configs survive updates. This page explains how configuration is structured and how to safely customize servers.

## Installation methods

You can install with one of two common flows:

### 1. Global install from prebuilt binary (recommended)

Install `csm` into `/usr/local/bin` and run the TUI:

```bash
arch=$(uname -m); \
case "$arch" in \
  x86_64)  asset="csm-linux-amd64" ;; \
  aarch64|arm64) asset="csm-linux-arm64" ;; \
  *) echo "Unsupported architecture: $arch" && exit 1 ;; \
esac; \
tmp=$(mktemp); \
curl -L "https://github.com/sivert-io/cs2-server-manager/releases/latest/download/$asset" -o "$tmp" && \
sudo install -m 0755 "$tmp" /usr/local/bin/csm && \
rm "$tmp" && \
sudo csm            # launches the interactive TUI installer
```

By default, CSM uses a **configuration root** where it expects `overrides/` and `game_files/`:

- If you set `CSM_ROOT`, it uses that directory.
- Otherwise, it defaults to `/opt/cs2-server-manager` (creating it on demand).

The first time you run the installer, CSM will seed the root’s `overrides/` folder with safe defaults if it doesn’t exist yet.

### 2. Git clone & customize

```bash
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager
# Edit overrides/ folder as needed
go build -o csm ./src/cmd/cs2-tui
sudo ./csm                  # local/dev build, not needed for normal installs
```

Best if you want full control and to keep your own fork. For most hosts, the global install in option 1 is simpler.

## Overrides directory

The `overrides/` folder contains configuration that is applied to all servers:

- Files in `overrides/` are **never deleted** during updates.
- They layer on top of the default plugin configs.
- Structure mirrors the game folder:

```text
overrides/game/csgo/
├── cfg/MatchZy/
└── addons/
```

Anything you put here will be copied into each server’s game directory (via `/home/<cs2_user>/cs2-config/game`).

## Ports and RCON

Default ports (incrementing by 10):

| Server | Game  | GOTV  |
| ------ | ----- | ----- |
| 1      | 27015 | 27020 |
| 2      | 27025 | 27030 |
| 3      | 27035 | 27040 |

The install wizard will prompt you to set an RCON password during setup.

### Updating server configuration

You can update RCON password, max players, and GSLT token across all servers without reinstalling:

**Via TUI**: Navigate to **Updates tab → "Update server configs"**

This will:
- Prompt for new RCON password, max players, and GSLT token
- Update the shared configuration
- Sync changes to all servers
- Restart servers to apply changes

**Via CLI**: Edit the shared config files manually:
- RCON & maxplayers: `/home/<cs2_user>/cs2-config/game/csgo/cfg/server.cfg`
- GSLT token: `/home/<cs2_user>/cs2-config/server.gslt`

Then restart servers: `sudo csm restart`

### Editing plugin configuration files

The TUI provides quick access to edit key configuration files:

**Via TUI**: Navigate to **Tools tab**:
- **Edit MatchZy config.cfg** - Edit shared MatchZy configuration
- **Edit MatchZy database.json** - Edit database connection settings
- **Edit CounterStrikeSharp admins.json** - Edit CSS admin permissions

These options will:
1. Quit the TUI and open the file in nano
2. Automatically fix file ownership after editing
3. Sync the edited config to all servers
4. Show instructions to restart the TUI

All edits are automatically synced to all servers and ownership is fixed.

## Best practices

- Keep all long-term customizations inside `overrides/`.
- Use a git repo for your overrides directory so you can version changes.
- Avoid editing files directly under `/home/<cs2_user>/server-*` unless testing something temporarily.
- After changing configs, restart the relevant server(s) via `csm restart`.
