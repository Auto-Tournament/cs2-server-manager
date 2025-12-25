package csm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	overridesDir := filepath.Join(root, "overrides")

	// Detect whether the MatchZy DB has been configured in "external" mode by
	// the install wizard. Newer wizard runs persist a __CSM_DB_MODE marker
	// alongside the standard MatchZy database.json fields so tools like this
	// can avoid trying to provision a Docker MySQL container when the user has
	// chosen an external database (for example, a local MySQL already running
	// on port 3306).
	skipDocker := getenvDefault("MATCHZY_SKIP_DOCKER", "0") == "1"

	if !skipDocker {
		cfgPath := filepath.Join(overridesDir, "game", "csgo", "cfg", "MatchZy", "database.json")
		if data, err := os.ReadFile(cfgPath); err == nil {
			var meta struct {
				DBMode string `json:"__CSM_DB_MODE,omitempty"`
			}
			// Ignore JSON errors here; older configs simply won't set DBMode.
			if err := json.Unmarshal(data, &meta); err == nil {
				if strings.EqualFold(strings.TrimSpace(meta.DBMode), "external") {
					skipDocker = true
				}
			}
		}
	}

	if skipDocker {
		fmt.Fprintln(&buf, "  [i] MATCHZY_SKIP_DOCKER=1 or external DB mode detected: skipping Docker provisioning and reusing the existing MatchZy database.")
	}

	cfg := BootstrapConfig{
		CS2User:           getenvDefault("CS2_USER", DefaultCS2User),
		OverridesDir:      overridesDir,
		MatchzySkipDocker: skipDocker,
	}

	if err := setupMatchZyDatabaseGo(&buf, cfg); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}
