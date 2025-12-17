package csm

import (
	"bytes"
	"context"
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
	return InstallDependenciesWithContext(context.Background())
}

// InstallDependenciesWithContext is like InstallDependencies but accepts a
// context so callers (TUI/CLI) can cancel long-running apt-get operations.
func InstallDependenciesWithContext(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	if err := installDeps(ctx, &buf); err != nil {
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

func installDeps(ctx context.Context, w io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("dependency installation must be run as root (use sudo)")
	}

	// If CSM_DEPS_LOG is set, mirror dependency installation output into that
	// file so the TUI can show a live tail while apt-get and friends run,
	// similar to how bootstrap/steamcmd logging works.
	if logPath, ok := os.LookupEnv("CSM_DEPS_LOG"); ok && logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer func() {
				if cerr := f.Close(); cerr != nil {
					fmt.Fprintf(os.Stderr, "CSM_DEPS_LOG close failed: %v\n", cerr)
				}
			}()
			// When the caller uses a *bytes.Buffer (as CLI/TUI do), reuse the
			// existing teeWriter helper so output is mirrored into both the
			// buffer and the on-disk log.
			if buf, ok := w.(*bytes.Buffer); ok {
				tw := &teeWriter{buf: buf, file: f}
				return ensureBootstrapDependenciesContext(ctx, tw)
			}
			// Fallback: mirror output to both the caller's writer and the
			// dependency log file.
			mw := io.MultiWriter(w, f)
			return ensureBootstrapDependenciesContext(ctx, mw)
		}
		// Fall back to plain writer if we can't open the log file.
	}

	return ensureBootstrapDependenciesContext(ctx, w)
}
