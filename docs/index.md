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
- **[MatchZy Enhanced](https://github.com/sivert-io/MatchZy-Enhanced)** – enhanced MatchZy plugin for in-server automation.

## What it does

- **Multi-server deployment**: Spin up 3–5 CS2 servers with a single command.
- **Tournament-ready stack**: Installs Metamod, CounterStrikeSharp, MatchZy (enhanced).
- **Safe updates**: Handles game and plugin updates automatically while preserving your configs.
- **Persistent overrides**: Everything in `overrides/` survives updates.
- **Observability & control**: Go-based CLI/TUI and tmux integration for logs and debugging.

## Quick Start

For most users, installing `csm` globally and running the TUI is all you need:

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

Read the **Getting Started** section for a full walkthrough. For advanced setups you can point CSM at a dedicated config directory via `CSM_ROOT` and `CSM_LOG_DIR` (see **Guides → Configuration & Overrides**).

## Project layout

- `overrides/` – your persistent game and plugin configuration (under the CSM root, typically next to the `csm` binary unless `CSM_ROOT` is set).

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

## Project Status

- [Roadmap & Kanban Board](https://kan.sivert.io/MAT) – view project status, roadmap, and current work (auto-updates from GitHub issues).

## Related projects

- [CS2 Server Manager](https://sivert-io.github.io/cs2-server-manager/) – multi-server CS2 deployment and management.
- [MatchZy Auto Tournament](https://mat.sivert.io) – web UI and API for automated CS2 tournaments.
- [MatchZy Enhanced](https://github.com/sivert-io/MatchZy-Enhanced) – enhanced MatchZy plugin for in-server automation.

---

## License & credits

<div align="center" markdown>

MIT License • Built on MatchZy • Made with :material-heart: for the CS2 community
MIT License • Built on MatchZy • Made with :material-heart: for the CS2 community
