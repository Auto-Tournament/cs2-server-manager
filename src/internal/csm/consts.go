package csm

import "time"

// Shared defaults and constants used across the CSM core and TUI. Keeping
// these in one place helps avoid subtle drift between CLI, TUI and docs.

const (
	// DefaultCS2User is the dedicated system user CSM manages by default.
	DefaultCS2User = "cs2servermanager"

	// DefaultNumServers is the initial number of servers provisioned by the
	// install wizard and CLI bootstrap when no explicit value is provided.
	DefaultNumServers = 3

	// DefaultBaseGamePort and DefaultBaseTVPort define the starting ports for
	// server-1; additional servers use offsets of +10 (27025/27030, etc.).
	DefaultBaseGamePort = 27015
	DefaultBaseTVPort   = 27020

	// DefaultRCONPassword is the fallback when no RCON password is supplied.
	// The install wizard encourages users to override this.
	DefaultRCONPassword = "ntlan2025"

	// DefaultMasterDiskGB and DefaultPerServerDiskGB drive the install
	// wizard's disk space estimate and low-space confirmation. The server
	// value is based on observed server-1 size (~59,217,686,528 bytes).
	DefaultMasterDiskGB    = 56.0
	DefaultPerServerDiskGB = 56.0

	// DefaultMatchzyContainerName and DefaultMatchzyVolumeName define the
	// Docker resources used for the MatchZy MySQL database when running in
	// Docker-managed mode.
	DefaultMatchzyContainerName = "matchzy-mysql"
	DefaultMatchzyVolumeName    = "matchzy-mysql-data"

	// DefaultMatchzyDBName / User / Password are the defaults used when
	// provisioning a fresh MatchZy database, both for Docker-managed and
	// external DB setups when the user has not supplied explicit values.
	DefaultMatchzyDBName     = "matchzy"
	DefaultMatchzyDBUser     = "matchzy"
	DefaultMatchzyDBPassword = "matchzy"

	// DefaultMatchzyRootPassword is the default MySQL root password used for the
	// Docker-managed MatchZy database unless overridden via environment.
	DefaultMatchzyRootPassword = "MatchZyRoot!2025"

	// DefaultRootDir is the default on-disk root where CSM stores its state
	// (overrides, game_files, logs, etc.) when CSM_ROOT is not explicitly set.
	// This is created on demand during installs and updates.
	DefaultRootDir = "/opt/cs2-server-manager"
)

// Timeouts for long-running operations to prevent hanging
const (
	// TimeoutSteamCMD is the maximum time allowed for SteamCMD operations
	// (game installation/updates can take 10-30+ minutes depending on network)
	TimeoutSteamCMD = 60 * time.Minute

	// TimeoutRsync is the maximum time allowed for rsync operations
	// (copying game files can take several minutes for large directories)
	TimeoutRsync = 30 * time.Minute

	// TimeoutDocker is the maximum time allowed for Docker operations
	// (pulling images, starting containers, etc.)
	TimeoutDocker = 10 * time.Minute

	// TimeoutPluginDownload is the maximum time allowed for plugin downloads
	// (HTTP downloads with progress tracking)
	TimeoutPluginDownload = 15 * time.Minute
)
