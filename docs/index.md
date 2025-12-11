---
title: CS2 Server Manager
hide:
  - navigation
  - toc
---

# CS2 Server Manager

Automated multi-server management for Counter-Strike 2. Deploy multiple dedicated CS2 servers in minutes with competitive plugins, auto-updates, and tournament integration.

Designed to work hand-in-hand with:

- **[MatchZy Auto Tournament](https://mat.sivert.io)** – web UI and API for automated CS2 tournaments.
- **[MatchZy Enhanced](https://me.sivert.io)** – enhanced MatchZy plugin for in-server automation.

## What it does

- **Multi-server deployment**: Spin up 3–5 CS2 servers with a single command.
- **Tournament-ready stack**: Installs Metamod, CounterStrikeSharp, MatchZy (enhanced), and AutoUpdater.
- **Safe updates**: Handles game and plugin updates automatically while preserving your configs.
- **Persistent overrides**: Everything in `overrides/` survives updates.
- **Observability & control**: Handy management script and tmux integration for logs and debugging.

## Quick Start

For most users, this is all you need:

```bash
chmod +x csm
sudo ./csm            # launches the interactive TUI installer
```

Read the **Getting Started** section for a full walkthrough.

## Project layout

- `scripts/` – dev/ops helper scripts (TUI launcher, release tooling, optional webhooks).
- `overrides/` – your persistent game and plugin configuration.

See:

- **Getting Started → Quick Start** – first-time setup.
- **Guides → Managing Servers** – everyday operations.
- **Guides → Configuration & Overrides** – customizing your servers.
- **Guides → Auto Updates** – how updates are handled behind the scenes.
- **Guides → Troubleshooting** – common problems and fixes.

---

## Support

- [GitHub Issues](https://github.com/sivert-io/cs2-server-manager/issues) – report bugs or request features.
- [Discussions](https://github.com/sivert-io/cs2-server-manager/discussions) – ask questions and share ideas.
- [Discord Community](https://discord.gg/n7gHYau7aW) – real-time support and chat with other hosts.

## Related projects

- [CS2 Server Manager](https://sivert-io.github.io/cs2-server-manager/) – multi-server CS2 deployment and management.
- [MatchZy Auto Tournament](https://mat.sivert.io) – web UI and API for automated CS2 tournaments.
- [MatchZy Enhanced](https://me.sivert.io) – enhanced MatchZy plugin for in-server automation.

---

## License & credits

<div align="center" markdown>

MIT License • Built on MatchZy • Made with :material-heart: for the CS2 community
MIT License • Built on MatchZy • Made with :material-heart: for the CS2 community
