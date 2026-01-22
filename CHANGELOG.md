# CS2 Server Manager Changelog

All notable changes to CS2 Server Manager will be documented in this file.

---

# Unreleased

### ✨ New Features

#### IP Ban Management
- **IP unban utilities:**
  - `csm unban <server> <ip>` - Remove an IP from banned RCON requests
  - `csm unban-all <server>` - Clear all IPs banned for RCON attempts
  - `csm list-bans <server>` - List all banned IP addresses
  - TUI options in Tools tab for unbanning IPs
- **Automatic server reload**: Unban operations automatically send `removeip` or `exec banned_ip.cfg` commands to running servers via tmux (no restart needed)
- **RCON ban settings disabled by default**: New servers have RCON IP bans disabled (sv_rcon_maxfailures 0) to prevent Docker network IPs from being incorrectly banned

#### Enhanced Server Configuration
- **New Config tab in TUI** - Dedicated tab for server configuration management
- **Expanded config editor:**
  - RCON password
  - Max players
  - GSLT token
  - Hostname prefix
  - RCON ban settings (max failures, min failures, min failure time)
- **RCON ban configuration**: Configure RCON ban settings directly from TUI (set to 0 to disable)

### 🔧 Fixed

- **RCON ban false positives**: Disabled RCON IP bans by default to prevent Docker network IPs (e.g., 172.19.0.3) from being incorrectly banned
- **Unban operations**: Improved error handling - missing ban files or unbanned IPs are treated as success rather than errors

---

# 1.5.9

#### January 21, 2026

### ✨ New Features

#### Config File Editing in TUI
- **Config file editing in TUI Tools tab:**
  - Edit MatchZy `config.cfg`
  - Edit MatchZy `database.json`
  - Edit CounterStrikeSharp `admins.json`
- **Automatic syncing**: Edited configs automatically sync to all servers
- **Automatic ownership fixes**: Files are automatically chowned to `cs2servermanager` user after editing
- Opens `nano` editor with full terminal control (fixes Ctrl+X issues)
- Creates directories and files as needed
- Uses `syscall.Exec` to give `nano` full terminal control

### 📚 Documentation

- Updated documentation for new config editing features
- Created comprehensive CHANGELOG.md matching MatchZy Enhanced format and styling
- Documented all versions from v1.0.0 to current release

---

# 1.5.8

#### January 21, 2026

### 🔧 Fixed

- **Database connection for Docker mode** - Changed MySQL host from detected primary IP to `127.0.0.1` to fix Wireguard VPN connectivity issues where servers couldn't reach themselves via VPN interface
- Updated copyright year in documentation to 2026

---

# 1.5.7

#### January 19, 2026

### 🔧 Fixed

- Enhanced debugging output for server detection
- Fixed gameinfo path detection in Metamod detection logic

---

# 1.5.6

#### January 19, 2026

### ✨ New Features

#### Fast Config Update Command
- **`csm update-config <server>` command** - Fast config-only update without reinstalling game files (takes < 1 second)
- Stops server, deletes existing `server.cfg`, regenerates fresh config, and restarts server
- Perfect for fixing configuration issues without waiting for full reinstall

### 🔧 Changed

- Refactored buffer handling in server configuration functions for improved flexibility
- Improved logging in ReinstallServerInstanceWithContext for better clarity
- Updated .gitignore to include 'csm' and 'overrides' directories

---

# 1.5.5

#### January 19, 2026

### ✨ New Features

- Comprehensive file ownership management - All operations now ensure proper file ownership (`cs2servermanager` user)
- Enhanced TUI functionality and error handling

---

# 1.5.4

#### January 19, 2026

### ✨ New Features

#### Server Reinstallation
- **`csm reinstall <server>` command** - Completely rebuild a server from master-install (fixes corrupted files, `gameinfo.gi` errors, segmentation faults)
- **Reinstall feature in TUI** - Available in Servers tab → "Reinstall a server"
- **Real-time progress tracking** - Shows live output during long operations like `rsync`
- Stops server, removes server directory, copies fresh files from master-install, regenerates configs, and restarts server
- Perfect for fixing corrupted game files without manual intervention

### 📚 Documentation

- Updated documentation for server management and configuration

---

# 1.5.3

#### January 19, 2026

### 🔧 Fixed

- **Port binding conflicts** - Enhanced TmuxManager to detect and utilize server ports correctly
- Refined bootstrap process and error handling for steamcmd installation
- Enhanced default overrides handling and cleanup on cancellation

---

# 1.5.2

#### January 18, 2026

### 🔧 Changed

- Enhanced steamcmd process management and install wizard cancellation handling
- Improved process management during steamcmd execution in bootstrap
- Enhanced install wizard logging during bootstrap process

---

# 1.5.1

#### January 18, 2026

### 🔧 Changed

- Enhanced install wizard cancellation handling and reset state on quit

---

# 1.5.0

#### January 18, 2026

**🎯 Shared Configuration & Enhanced Wizard Release**

This release introduces shared server configuration management and an enhanced installation wizard with new settings for better server customization.

### ✨ New Features

#### Shared Server Configuration
- Shared server configuration management system
- Server configuration editor in TUI
- Enhanced configuration handling across all servers

#### Enhanced Installation Wizard
- New settings in install wizard:
  - **GSLT token** configuration
  - **Max players** setting
  - **Hostname prefix** detection and configuration
- Better defaults and validation

### 🔧 Changed

- Removed deprecated MatchZy configuration files
- Enhanced project documentation and server configuration management

---

# 1.4.5

#### December 18, 2025

### 🔧 Changed

- Refactored initWizardDefaults to improve server count detection

**Note:** This version had the working launch command format that was later broken and then restored in recent fixes.

---

# 1.4.4

#### December 18, 2025

### 🔧 Changed

- Refactored MySQL host configuration in bootstrap process

---

# 1.4.3

#### December 18, 2025

### 🔧 Changed

- Refactored server management and enhanced bootstrapping process

---

# 1.4.2

#### December 18, 2025

### ✨ New Features

- Enhanced server management prompts with disk space estimates

### 🔧 Changed

- Updated syncMasterToServerWithContext to use masterDir for authoritative game files
- Removed unused tailContains function from monitor.go

---

# 1.4.1

#### December 18, 2025

### 🔧 Changed

- Updated plugin deployment process to sync configurations from shared directory

---

# 1.4.0

#### December 18, 2025

### ✨ New Features

- Enhanced auto-update functionality with MatchZy integration

---

# 1.3.10

#### December 18, 2025

### 🔧 Changed

- Refactored plugin management and update process

---

# 1.3.9

#### December 18, 2025

### 🔧 Fixed

- Clear existing persistent log file before starting tmux session to prevent log growth

---

# 1.3.8

#### December 18, 2025

### 🔧 Fixed

- Ensure CS2 user ownership of home directory after updates and plugin deployments

---

# 1.3.7

#### December 18, 2025

### ✨ New Features

- Added confirmation prompt before release in release.sh

### 🔧 Changed

- Refactored bootstrap and install wizard to remove RequireExistingMaster option

---

# 1.3.6

#### December 18, 2025

*No significant changes*

---

# 1.3.5

#### December 18, 2025

### 🔧 Changed

- Updated .gitignore to include .env and cs2-tui files
- Ensure ownership of home directory for CS2 user during bootstrap process to prevent permission issues

---

# 1.3.4

#### December 18, 2025

### ✨ New Features

- Implement RequireExistingMaster option in bootstrap and install wizard

### 🔧 Changed

- Enhanced TmuxManager to derive game and TV ports from autoexec.cfg
- Removed cs2-tui binary from repository

---

# 1.3.3

#### December 18, 2025

### 🔧 Changed

- Enhanced error handling and output visibility in TmuxManager
- Enhanced TUI logging and viewport management
- Refined install wizard height management for improved stability
- Enhanced install wizard layout and external DB configuration rendering

---

# 1.3.2

#### December 18, 2025

### ✨ New Features

- Install wizard field activation logic and improved cursor navigation

### 🔧 Changed

- Refactored CSM configuration handling and update documentation

---

# 1.3.1

#### December 18, 2025

### 🔧 Changed

- Enhanced documentation for CSM configuration and logging
- Updated .gitignore and enhanced troubleshooting documentation

---

# 1.3.0

#### December 18, 2025

**🎉 Major Release: TUI + Go-native Flows Refactor**

This release marks a complete rewrite of the CS2 Server Manager with a modern Terminal User Interface (TUI) and Go-native server management flows, replacing the previous shell script-based implementation.

### ✨ New Features

#### Complete TUI Rewrite
- **Terminal User Interface (TUI)** using Bubble Tea framework
- Interactive menu-driven interface with keyboard navigation
- Real-time progress tracking and live log output
- Enhanced error handling and cancellation support

#### Go-native Server Management
- **Go-native server management flows** replacing shell scripts
- Better error handling and state management
- Improved logging and debugging capabilities
- More reliable process management

#### Interactive Install Wizard
- **Interactive install wizard** with live progress tracking
- Step-by-step server configuration
- Real-time validation and feedback
- Better user experience for initial setup

### 🔧 Changed

- Removed deprecated installation and auto-update scripts
- Migrated from shell-based to Go-based implementation
- Improved code maintainability and extensibility

---

# 1.2.8

#### December 18, 2025

### ✨ New Features

- Enhanced server instance creation with hostname prefix detection

---

# 1.2.7

#### December 18, 2025

*No significant changes*

---

# 1.2.6

#### December 18, 2025

*No significant changes*

---

# 1.2.5

#### December 18, 2025

### 🔧 Changed

- Enhanced server configuration and installation wizard

---

# 1.2.4

#### December 18, 2025

### ✨ New Features

- Enhanced MatchZy database configuration handling
- Implemented per-server auto-update functionality and enhanced monitoring
- Enhanced RunAutoUpdateMonitor with root privilege check

### 🔧 Changed

- Refactored TUI styles and error handling
- Major TUI + Go-native flows refactor

---

# 1.2.3

#### December 18, 2025

*No significant changes*

---

# 1.2.2

#### December 18, 2025

### 🔧 Changed

- Refactored dependency installation logging and ensureBootstrapDependencies function

---

# 1.2.1

#### December 18, 2025

### 🔧 Changed

- Enhanced install wizard and quit confirmation handling

---

# 1.2.0

#### December 18, 2025

### ✨ New Features

- Enhanced install wizard and menu item descriptions

---

# 1.1.13

#### December 18, 2025

*No significant changes*

---

# 1.1.12

#### December 18, 2025

*No significant changes*

---

# 1.1.11

#### December 18, 2025

*No significant changes*

---

# 1.1.10

#### December 18, 2025

*No significant changes*

---

# 1.1.9

#### December 18, 2025

*No significant changes*

---

# 1.1.8

#### December 18, 2025

*No significant changes*

---

# 1.1.7

#### December 18, 2025

*No significant changes*

---

# 1.1.6

#### December 18, 2025

*No significant changes*

---

# 1.1.5

#### December 18, 2025

*No significant changes*

---

# 1.1.4

#### December 18, 2025

*No significant changes*

---

# 1.1.3

#### December 18, 2025

*No significant changes*

---

# 1.1.2

#### December 18, 2025

*No significant changes*

---

# 1.1.1

#### December 18, 2025

*No significant changes*

---

# 1.1.0

#### December 18, 2025

### ✨ New Features

- Implemented core CSM functionality with new features and configurations
- Added link to full documentation site in README
- Updated README with enhanced installation instructions and usage guidelines

---

# 1.0.4

#### December 18, 2025

*No significant changes*

---

# 1.0.3

#### December 18, 2025

*No significant changes*

---

# 1.0.2

#### December 18, 2025

*No significant changes*

---

# 1.0.1

#### December 18, 2025

### 🔧 Changed

- Enhanced release script with git commit/tag functionality

---

# 1.0.0

#### December 18, 2025

**🎉 Initial Release**

This marks the first official release of CS2 Server Manager, a comprehensive tool for managing multiple Counter-Strike 2 dedicated servers.

### ✨ New Features

#### Multi-Server Deployment
- Deploy and manage multiple CS2 server instances from a single installation
- Automatic port allocation and conflict detection
- Isolated server directories with shared master installation

#### Automated Plugin Installation
- **Metamod:Source** - Core plugin framework
- **CounterStrikeSharp** - C# scripting plugin for CS2
- **MatchZy** - Tournament management plugin
- Automatic plugin deployment and configuration

#### Docker-Based Database
- **Docker-based MySQL database provisioning** for MatchZy
- Automatic database setup and configuration
- Secure connection handling

#### Server Management
- **Tmux-based server management** - Detachable server sessions
- Start, stop, restart, and monitor servers
- Interactive console access via `csm attach <server>`
- Real-time log viewing

#### Auto-Update Monitor
- **Auto-update monitor functionality** - Keeps servers up to date
- Automatic SteamCMD updates
- Configurable update intervals

#### Additional Features
- **Map thumbnail extraction** - Generate map previews
- **Configuration override system** - Customize server configs per-instance
- **Shared configuration management** - Centralized config management

---

## Previous Development

For the complete development history, see: `git log --oneline --all`
