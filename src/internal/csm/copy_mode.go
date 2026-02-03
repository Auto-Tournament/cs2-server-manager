package csm

import (
	"os"
	"strings"
)

// CopyMode controls how master-install/game is replicated into server-N/game.
// It is configured via the CSM_COPY_MODE environment variable.
type CopyMode string

const (
	CopyModeAuto    CopyMode = "auto"    // try reflink for install-like ops, else tuned rsync
	CopyModeReflink CopyMode = "reflink" // request reflink (falls back to tuned rsync if unsupported)
	CopyModeRsync   CopyMode = "rsync"   // tuned rsync
	CopyModeLegacy  CopyMode = "legacy"  // previous behavior (plain rsync -a --delete ...)
)

func CopyModeFromEnv() CopyMode {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CSM_COPY_MODE")))
	switch v {
	case string(CopyModeAuto):
		return CopyModeAuto
	case string(CopyModeReflink):
		return CopyModeReflink
	case string(CopyModeRsync):
		return CopyModeRsync
	case string(CopyModeLegacy):
		return CopyModeLegacy
	case "":
		return CopyModeLegacy
	default:
		// Unknown values fall back to legacy to preserve expected behaviour.
		return CopyModeLegacy
	}
}
