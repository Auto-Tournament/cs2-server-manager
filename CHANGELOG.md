# Changelog

All notable changes to CS2 Server Manager will be documented in this file.

## [Unreleased]

### Added
- `csm reinstall <server>` command - Completely rebuild a server from master-install (fixes corrupted files)
- `csm update-config <server>` command - Fast config-only update without reinstalling game files
- Config file editing in TUI Tools tab:
  - Edit MatchZy config.cfg
  - Edit MatchZy database.json
  - Edit CounterStrikeSharp admins.json
- Comprehensive ownership fixes - All operations now ensure proper file ownership
- Real-time CLI output for long-running operations (reinstall, etc.)

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
