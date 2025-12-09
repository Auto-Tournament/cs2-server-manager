---
title: CS2 Server Manager
---

# CS2 Server Manager

Automated multi-server management for Counter-Strike 2. Deploy multiple dedicated CS2 servers in minutes with competitive plugins, auto-updates, and tournament integration.

## What it does

- **Multi-server deployment**: Spin up 3–5 CS2 servers with a single command.
- **Tournament-ready stack**: Installs Metamod, CounterStrikeSharp, MatchZy (enhanced), and AutoUpdater.
- **Safe updates**: Handles game and plugin updates automatically while preserving your configs.
- **Persistent overrides**: Everything in `overrides/` survives updates.
- **Observability & control**: Handy management script and tmux integration for logs and debugging.

## Quick Start

For most users, this is all you need:

```bash
wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
bash install.sh
```

For automated installs (no prompts):

```bash
bash install.sh --auto --servers 5
```

Read the **Getting Started** section for a full walkthrough.

## Project layout

- `install.sh` – one-shot installer for CS2 servers and required dependencies.
- `manage.sh` – main CLI for installing, starting, stopping, and repairing servers.
- `scripts/` – supporting utilities (`cs2_tmux.sh`, update helpers, etc.).
- `overrides/` – your persistent game and plugin configuration.

See:

- **Getting Started → Quick Start** – first-time setup.
- **Guides → Managing Servers** – everyday operations.
- **Guides → Configuration & Overrides** – customizing your servers.
- **Guides → Auto Updates** – how updates are handled behind the scenes.
- **Guides → Troubleshooting** – common problems and fixes.


