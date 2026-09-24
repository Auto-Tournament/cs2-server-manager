package csm

import (
	"bytes"
	"path/filepath"
)

// VerifyATCS2DB verifies (and if needed, repairs) the Auto Tournament CS2 MySQL
// database
// using the existing Docker-based provisioning logic. It reads the
// overrides cfg/AutoTournamentCS2/database.json config (carrying it over
// from cfg/MatchZy/ first), ensures the Docker container,
// database and user exist, and returns a human-readable log.
func VerifyATCS2DB() (string, error) {
	var buf bytes.Buffer

	cs2User := getenvDefault("CS2_USER", DefaultCS2User)
	cfg := BootstrapConfig{
		CS2User:         cs2User,
		OverridesDir:    filepath.Join("/home", cs2User, "overrides"),
		ATCS2SkipDocker: getenvDefault("AT_SKIP_DOCKER", "0") == "1",
	}

	if err := EnsureATCS2CfgCarriedOver(&buf, cs2User); err != nil {
		buf.WriteString("  [!] " + err.Error() + "\n")
	}
	if err := setupATCS2DatabaseGo(&buf, cfg); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}
