package csm

import "time"

// Shared defaults and constants used across the CSM core and TUI. Keeping
// these in one place helps avoid subtle drift between CLI, TUI and docs.

const (
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

	// DefaultMasterDiskGB and DefaultPerServerLinkedDiskGB drive the install
	// wizard's disk space estimate (see EstimateInstallDisk) when nothing is
	// installed yet to measure. A full install is ~71 GB, ~70 GB of it VPKs.
	// With VPK hardlinks (default) a server only adds its non-VPK files
	// (~1.2 GB observed); with CSM_VPK_HARDLINK=0 it is a full copy.
	DefaultMasterDiskGB          = 71.0
	DefaultPerServerLinkedDiskGB = 2.0

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
