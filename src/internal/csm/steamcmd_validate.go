package csm

import (
	"os"
	"strings"
)

// SteamcmdShouldValidate reports whether SteamCMD app_update should include
// the "validate" token. Default: true (safe).
//
// Controlled by CSM_STEAMCMD_VALIDATE:
// - "0"/"false"/"off"/"no" => false
// - "1"/"true"/"on"/"yes"  => true
// - empty/other            => true
func SteamcmdShouldValidate() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CSM_STEAMCMD_VALIDATE"))) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	default:
		return true
	}
}
