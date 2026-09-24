package csm

import "path/filepath"

// On-disk names of the Auto Tournament CS2 plugin, relative to a server's
// game/csgo directory. Plugin 2.0.0 renamed every one of them; the legacy
// names are only used to find and carry over an install made before that.
const (
	// ATCS2PluginDirName is the plugin's folder under
	// addons/counterstrikesharp/plugins/.
	ATCS2PluginDirName = "AutoTournamentCS2"
	// ATCS2DLLName is the plugin assembly inside ATCS2PluginDirName.
	ATCS2DLLName = "AutoTournamentCS2.dll"
	// ATCS2CfgDirName is the plugin's config folder under cfg/.
	ATCS2CfgDirName = "AutoTournamentCS2"
	// ATCS2SQLiteFile is the database file the plugin creates in SQLite mode.
	ATCS2SQLiteFile = "auto_tournament_cs2.db"
	// ATCS2AssetPrefix and ATCS2AssetSuffix select the plugin release asset,
	// AutoTournamentCS2-<version>.zip.
	ATCS2AssetPrefix = "AutoTournamentCS2"
	ATCS2AssetSuffix = ".zip"

	// legacyATCS2PluginDirName and legacyATCS2CfgDirName are the plugin
	// folder and config folder of plugin builds before 2.0.0.
	legacyATCS2PluginDirName = "MatchZy"
	legacyATCS2CfgDirName    = "MatchZy"
)

// atcs2PluginDir returns addons/counterstrikesharp/plugins/AutoTournamentCS2
// under csgoDir.
func atcs2PluginDir(csgoDir string) string {
	return filepath.Join(csgoDir, "addons", "counterstrikesharp", "plugins", ATCS2PluginDirName)
}

// legacyATCS2PluginDir returns the pre-2.0.0 plugin folder under csgoDir.
func legacyATCS2PluginDir(csgoDir string) string {
	return filepath.Join(csgoDir, "addons", "counterstrikesharp", "plugins", legacyATCS2PluginDirName)
}
