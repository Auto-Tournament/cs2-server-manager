# Quick Start

This guide gets you from zero to running CS2 servers in a few minutes.

## Prerequisites

- **OS**: Ubuntu 22.04+ (or similar modern Linux distro).
- **Root / sudo access** on the machine that will host the servers.
- **Docker** installed and running (for MySQL and supporting services).
- **Enough resources** for multiple CS2 servers (CPU, RAM, and disk).

## 1. Download and run the installer

From your target server:

```bash
wget https://raw.githubusercontent.com/sivert-io/cs2-server-manager/master/install.sh
bash install.sh
```

To run fully unattended with 5 servers:

```bash
bash install.sh --auto --servers 5
```

The installer will:

- Install required system dependencies.
- Download CS2 server files.
- Set up Dockerized MySQL.
- Install Metamod, CounterStrikeSharp, MatchZy, and AutoUpdater.
- Configure multiple CS2 instances with sane defaults.

## 2. Use the management menu

Once installation completes, manage everything with:

```bash
./manage.sh
```

From the menu you can:

- Install or repair servers.
- Start/stop all servers.
- Check status.
- Run updates.

## 3. Common one-liners

These commands are shortcuts around the menu:

```bash
./manage.sh install          # Install servers
./manage.sh start            # Start all servers
./manage.sh stop             # Stop all servers
./manage.sh status           # Check status
./manage.sh update-game      # Update CS2
./manage.sh update-plugins   # Update plugins
./manage.sh repair           # Fix issues
```

## 4. Next steps

- See **Guides → Managing Servers** for day-to-day operations.
- See **Guides → Configuration & Overrides** to customize configs before or after installation.
- See **Guides → Auto Updates** to understand how updates are handled.

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
