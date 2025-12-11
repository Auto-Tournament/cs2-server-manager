# Configuration & Overrides

CS2 Server Manager is designed so your custom configs survive updates. This page explains how configuration is structured and how to safely customize servers.

## Installation methods

You can install with one of two common flows:

### 1. Download prebuilt binary (recommended)

```bash
chmod +x csm
sudo ./csm            # launches the interactive TUI installer
```

This uses the default `overrides/` folder that ships with the release, if present.

### 2. Git clone & customize

```bash
git clone https://github.com/sivert-io/cs2-server-manager.git
cd cs2-server-manager
# Edit overrides/ folder as needed
./scripts/start.sh          # or: go build -o csm ./src/cmd/cs2-tui && ./csm
```

Best if you want full control and to keep your own fork.

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

Anything you put here will be copied into each server’s game directory.

## Ports and RCON

Default ports (incrementing by 10):

| Server | Game  | GOTV  |
| ------ | ----- | ----- |
| 1      | 27015 | 27020 |
| 2      | 27025 | 27030 |
| 3      | 27035 | 27040 |

- **Default RCON password**: `ntlan2025`
- You can adjust ports and RCON in your overrides configs.

## Best practices

- Keep all long-term customizations inside `overrides/`.
- Use a git repo for your overrides directory so you can version changes.
- Avoid editing files directly under `/home/cs2/server-*` unless testing something temporarily.
- After changing configs, restart the relevant server(s) via `csm restart`.
