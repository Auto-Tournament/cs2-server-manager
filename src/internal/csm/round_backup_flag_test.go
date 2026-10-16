package csm

import (
	"slices"
	"strings"
	"testing"
)

// CS2 writes no round backups without -allow_round_backup_files (October 2026),
// so every way csm starts a server must pass it.
func TestEveryLaunchAllowsRoundBackupFiles(t *testing.T) {
	for _, spec := range []launchSpec{
		{Mode: "valve", GamePort: 27015, TVPort: 27020, MaxPlayers: 12},
		{Mode: "valve", LegacyCS2Sh: true, GamePort: 27015, TVPort: 27020, MaxPlayers: 12},
		{Mode: "alternate", GamePort: 27015, TVPort: 27020, MaxPlayers: 12},
		{Mode: "binary", GamePort: 27015, TVPort: 27020, MaxPlayers: 12, GSLT: "token"},
	} {
		if got := buildLaunchCommand(spec); !strings.Contains(got, " "+roundBackupFlag) {
			t.Errorf("mode %q (legacy %v): no %s in %q", spec.Mode, spec.LegacyCS2Sh, roundBackupFlag, got)
		}
	}
	args := instanceCS2Args(instanceLaunch{N: 1, Map: "de_dust2", MaxPlayers: 12})
	if !slices.Contains(args, roundBackupFlag) {
		t.Errorf("instance args lack %s: %v", roundBackupFlag, args)
	}
}
