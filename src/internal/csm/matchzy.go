package csm

import (
	"bytes"
	"os"
	"path/filepath"
)

// VerifyMatchzyDB verifies (and if needed, repairs) the MatchZy MySQL database
// using the existing Docker-based provisioning logic. It reads the
// overrides MatchZy database.json config, ensures the Docker container,
// database and user exist, and returns a human-readable log.
func VerifyMatchzyDB() (string, error) {
	var buf bytes.Buffer

	root, err := os.Getwd()
	if err != nil {
		root = "."
	}

	cfg := BootstrapConfig{
		CS2User:           getenvDefault("CS2_USER", DefaultCS2User),
		OverridesDir:      filepath.Join(root, "overrides"),
		MatchzySkipDocker: getenvDefault("MATCHZY_SKIP_DOCKER", "0") == "1",
	}

	if err := setupMatchZyDatabaseGo(&buf, cfg); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}
