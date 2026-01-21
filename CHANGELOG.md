# Changelog

All notable changes to CS2 Server Manager will be documented in this file.

---

## [Unreleased]

### Added
- Config file editing in TUI Tools tab:
  - Edit MatchZy config.cfg
  - Edit MatchZy database.json  
  - Edit CounterStrikeSharp admins.json
- Automatic config syncing to all servers after editing
- Automatic ownership fixes after config editing

### Fixed
- RCON password not being set - Fixed launch command format (reverted to v1.4.5 working format)
- Server.cfg generation - Now creates properly formatted configs with all required settings
- Metamod detection bug - Fixed chicken-and-egg problem where broken servers prevented Metamod from being enabled
- Database connection - Docker mode now uses `127.0.0.1` instead of detected primary IP (fixes Wireguard VPN issues)
- Tmux console interactivity - Switched from `tee` to `pipe-pane` for proper interactive console
- Port binding conflicts - Reverted to v1.4.5 launch command format (`+tv_port` not `-tv_port`)

### Changed
- Config file editing automatically syncs to all servers and fixes ownership
- Improved error handling and progress tracking for reinstall operations

---

## [1.5.8] - 2026-01-19

### Fixed
- **Database connection for Docker mode** - Changed MySQL host from detected primary IP to `127.0.0.1` to fix Wireguard VPN connectivity issues
- Updated copyright year in documentation to 2026

---

## [1.5.7] - 2026-01-19

### Fixed
- Enhanced debugging output for server detection
- Fixed gameinfo path detection in Metamod detection logic

---

## [1.5.6] - 2026-01-19

### Added
- **`csm update-config <server>` command** - Fast config-only update without reinstalling game files (takes < 1 second)

### Changed
- Refactored buffer handling in server configuration functions for improved flexibility
- Improved logging in ReinstallServerInstanceWithContext for better clarity
- Updated .gitignore to include 'csm' and 'overrides' directories

---

## [1.5.5] - 2026-01-19

### Added
- Comprehensive file ownership management - All operations now ensure proper file ownership
- Enhanced TUI functionality and error handling

---

## [1.5.4] - 2026-01-19

### Added
- **`csm reinstall <server>` command** - Completely rebuild a server from master-install (fixes corrupted files)
- Reinstall feature in TUI (Servers tab → "Reinstall a server")
- Real-time progress tracking for reinstall operations

### Changed
- Updated documentation for server management and configuration

---

## [1.5.3] - 2026-01-19

### Fixed
- **Port binding conflicts** - Enhanced TmuxManager to detect and utilize server ports correctly
- Refined bootstrap process and error handling for steamcmd installation
- Enhanced default overrides handling and cleanup on cancellation

---

## [1.5.2] - 2026-01-19

### Changed
- Enhanced steamcmd process management and install wizard cancellation handling
- Improved process management during steamcmd execution in bootstrap
- Enhanced install wizard logging during bootstrap process

---

## [1.5.1] - 2026-01-19

### Changed
- Enhanced install wizard cancellation handling and reset state on quit

---

## [1.5.0] - 2026-01-19

### Added
- Shared server configuration management
- Enhanced installation wizard for new settings (GSLT, max players, hostname prefix)
- Server configuration editor in TUI

### Changed
- Removed deprecated MatchZy configuration files
- Enhanced project documentation and server configuration management

---

## [1.4.5] - 2025-12-18

### Fixed
- Stable release with working launch command format
- Proper port binding for multiple servers
- Metamod and CounterStrikeSharp integration

---

## Previous Versions

For earlier versions, see the git history: `git log --oneline`
