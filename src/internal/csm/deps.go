package csm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// InstallDependencies installs the core system packages needed for CS2 Server
// Manager (tmux, steamcmd, rsync, jq, etc.). It mirrors a subset of the
// bootstrap dependency logic but as a standalone helper callable from the TUI
// or CLI.
func InstallDependencies() (string, error) {
	var buf bytes.Buffer
	if err := installDeps(&buf); err != nil {
		out := buf.String()
		AppendLog("deps.log", out)
		return out, err
	}
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		out = "System dependencies installed successfully (or already up to date)."
	}
	AppendLog("deps.log", out)
	return out, nil
}

func installDeps(w io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("dependency installation must be run as root (use sudo)")
	}

	// If CSM_DEPS_LOG is set, mirror dependency installation output into that
	// file so the TUI can show a live tail while apt-get and friends run,
	// similar to how bootstrap/steamcmd logging works.
	if logPath, ok := os.LookupEnv("CSM_DEPS_LOG"); ok && logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer f.Close()
			// installDeps is only ever called with a *bytes.Buffer today, so
			// we can safely assert and reuse the existing teeWriter helper
			// used by the bootstrap/steamcmd path.
			if buf, ok := w.(*bytes.Buffer); ok {
				tw := &teeWriter{buf: buf, file: f}
				return ensureBootstrapDependencies(tw)
			}
			// Fallback: still stream directly to the file plus the generic writer.
			tw := &teeWriter{buf: &bytes.Buffer{}, file: f}
			return ensureBootstrapDependencies(tw)
		}
		// Fall back to plain writer if we can't open the log file.
	}

	return ensureBootstrapDependencies(w)
}
