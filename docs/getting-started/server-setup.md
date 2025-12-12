# Server Setup

This page explains how to prepare a machine for CS2 Server Manager and what the installation script expects.

## System requirements

- **Linux server** (Ubuntu 22.04+ recommended).
- **Root / sudo access**.
- **64-bit CPU** with sufficient cores for multiple game servers.
- **At least 8 GB RAM** (more if running many servers).
- **Stable network** with open ports for game traffic and GOTV.

## Required packages

The installer will attempt to install most dependencies for you, but in locked-down environments you may need to do it manually:

```bash
sudo apt-get update
sudo apt-get install -y \
  lib32gcc-s1 lib32stdc++6 \
  steamcmd tmux curl jq unzip tar rsync git
```

You also need **Docker** for the MySQL container and related services. Follow the official docs:

- https://docs.docker.com/engine/install/

## Network and ports

By default, servers use incrementing ports. A typical layout is:

| Server | Game  | GOTV  |
|--------|-------|-------|
| 1      | 27015 | 27020 |
| 2      | 27025 | 27030 |
| 3      | 27035 | 27040 |

Make sure your firewall allows traffic on these ports (or whatever you configure) for both UDP and TCP where needed.

## Filesystem layout

After installation, your key locations are:

- The cloned repo (or download directory) for `cs2-server-manager`.
- The CS2 server installation directory created by the installer.
- The `overrides/` directory inside the repo, used for persistent configs.

See **Guides → Configuration & Overrides** for details on how overrides work.

## Running the installer

Once prerequisites are in place, follow **Getting Started → Quick Start** to run the installer script and bring up your first servers.

{
"cells": [],
"metadata": {
"language_info": {
"name": "python"
}
},
"nbformat": 4,
"nbformat_minor": 2
}
