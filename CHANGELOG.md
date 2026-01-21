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

### Changed
- Refactored initWizardDefaults to improve server count detection

**Note:** This version had the working launch command format that was later broken and then restored.

---

## [1.4.4] - 2025-12-18

### Changed
- Refactored MySQL host configuration in bootstrap process

---

## [1.4.3] - 2025-12-18

### Changed
- Refactored server management and enhance bootstrapping process

---

## [1.4.2] - 2025-12-18

### Added
- Enhanced server management prompts with disk space estimates

### Changed
- Updated syncMasterToServerWithContext to use masterDir for authoritative game files
- Removed unused tailContains function from monitor.go

---

## [1.4.1] - 2025-12-18

### Changed
- Updated plugin deployment process to sync configurations from shared directory

---

## [1.4.0] - 2025-12-18

### Added
- Enhanced auto-update functionality with MatchZy integration

---

## [1.3.10] - 2025-12-18

### Changed
- Refactored plugin management and update process

---

## [1.3.9] - 2025-12-18

### Fixed
- Clear existing persistent log file before starting tmux session to prevent log growth

---

## [1.3.8] - 2025-12-18

### Fixed
- Ensure CS2 user ownership of home directory after updates and plugin deployments

---

## [1.3.7] - 2025-12-18

### Added
- Added confirmation prompt before release in release.sh

### Changed
- Refactored bootstrap and install wizard to remove RequireExistingMaster option

---

## [1.3.6] - 2025-12-18

*No significant changes*

---

## [1.3.5] - 2025-12-18

### Changed
- Updated .gitignore to include .env and cs2-tui files
- Ensure ownership of home directory for CS2 user during bootstrap process to prevent permission issues

---

## [1.3.4] - 2025-12-18

### Added
- Implement RequireExistingMaster option in bootstrap and install wizard

### Changed
- Enhanced TmuxManager to derive game and TV ports from autoexec.cfg
- Removed cs2-tui binary from repository

---

## [1.3.3] - 2025-12-18

### Changed
- Enhanced error handling and output visibility in TmuxManager
- Enhanced TUI logging and viewport management
- Refined install wizard height management for improved stability
- Enhanced install wizard layout and external DB configuration rendering

---

## [1.3.2] - 2025-12-18

### Added
- Install wizard field activation logic and improved cursor navigation

### Changed
- Refactored CSM configuration handling and update documentation

---

## [1.3.1] - 2025-12-18

### Changed
- Enhanced documentation for CSM configuration and logging
- Updated .gitignore and enhanced troubleshooting documentation

---

## [1.3.0] - 2025-12-18

### Major Release: TUI + Go-native flows refactor

### Added
- Complete TUI (Terminal User Interface) rewrite using Bubble Tea
- Go-native server management flows (replacing shell scripts)
- Interactive install wizard with live progress tracking
- Enhanced error handling and cancellation support

### Changed
- Removed deprecated installation and auto-update scripts
- Migrated from shell-based to Go-based implementation

---

## [1.2.8] - 2025-12-18

### Added
- Enhanced server instance creation with hostname prefix detection

---

## [1.2.7] - 2025-12-18

*No significant changes*

---

## [1.2.6] - 2025-12-18

*No significant changes*

---

## [1.2.5] - 2025-12-18

### Changed
- Enhanced server configuration and installation wizard

---

## [1.2.4] - 2025-12-18

### Added
- Enhanced MatchZy database configuration handling
- Implemented per-server auto-update functionality and enhanced monitoring
- Enhanced RunAutoUpdateMonitor with root privilege check

### Changed
- Refactored TUI styles and error handling
- Major TUI + Go-native flows refactor

---

## [1.2.3] - 2025-12-18

*No significant changes*

---

## [1.2.2] - 2025-12-18

### Changed
- Refactored dependency installation logging and ensureBootstrapDependencies function

---

## [1.2.1] - 2025-12-18

### Changed
- Enhanced install wizard and quit confirmation handling

---

## [1.2.0] - 2025-12-18

### Added
- Enhanced install wizard and menu item descriptions

---

## [1.1.13] - 2025-12-18

*No significant changes*

---

## [1.1.12] - 2025-12-18

*No significant changes*

---

## [1.1.11] - 2025-12-18

*No significant changes*

---

## [1.1.10] - 2025-12-18

*No significant changes*

---

## [1.1.9] - 2025-12-18

*No significant changes*

---

## [1.1.8] - 2025-12-18

*No significant changes*

---

## [1.1.7] - 2025-12-18

*No significant changes*

---

## [1.1.6] - 2025-12-18

*No significant changes*

---

## [1.1.5] - 2025-12-18

*No significant changes*

---

## [1.1.4] - 2025-12-18

*No significant changes*

---

## [1.1.3] - 2025-12-18

*No significant changes*

---

## [1.1.2] - 2025-12-18

*No significant changes*

---

## [1.1.1] - 2025-12-18

*No significant changes*

---

## [1.1.0] - 2025-12-18

### Added
- Implemented core CSM functionality with new features and configurations
- Added link to full documentation site in README
- Updated README with enhanced installation instructions and usage guidelines

---

## [1.0.4] - 2025-12-18

*No significant changes*

---

## [1.0.3] - 2025-12-18

*No significant changes*

---

## [1.0.2] - 2025-12-18

*No significant changes*

---

## [1.0.1] - 2025-12-18

### Changed
- Enhanced release script with git commit/tag functionality

---

## [1.0.0] - 2025-12-18

### Initial Release

### Added
- Multi-server CS2 deployment
- Automated plugin installation (Metamod, CounterStrikeSharp, MatchZy)
- Docker-based MySQL database provisioning
- Auto-update monitor functionality
- Map thumbnail extraction
- Tmux-based server management
- Configuration override system

---

## Previous Development

For the complete development history, see: `git log --oneline --all`
