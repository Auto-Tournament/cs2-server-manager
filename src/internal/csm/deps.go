package csm

import (
	"bytes"
	"fmt"
	"os"
)

// InstallDependencies installs the core system dependencies required by CSM
// (tmux, steamcmd, rsync, jq, etc.) using the same logic as the bootstrap
// flow. It is intended to be run once on a fresh host.
func InstallDependencies() (string, error) {
	var buf bytes.Buffer

	if os.Geteuid() != 0 {
		return "", fmt.Errorf("dependency installation must be run as root (use sudo)")
	}

	if err := ensureBootstrapDependencies(&buf); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}


