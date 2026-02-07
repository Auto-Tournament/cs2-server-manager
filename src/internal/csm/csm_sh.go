package csm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// csmManagedLauncherSh is an alternate launcher that sets LD_LIBRARY_PATH to
// prefer the libs bundled with CS2. This file is written as game/csm.sh so we
// can keep Valve's game/cs2.sh intact and offer this as an opt-in workaround.
const csmManagedLauncherSh = `#!/usr/bin/env bash
set -euo pipefail

GAMEROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

# Default dedicated-server flags.
DEFAULT_ARGS=(
  -dedicated
  -ip 0.0.0.0
  -usercon
)

# Prefer bundled libraries shipped with CS2 (helps avoid loading incompatible
# distro-provided libs, e.g. libv8).
export LD_LIBRARY_PATH="${GAMEROOT}/bin/linuxsteamrt64:${GAMEROOT}/csgo/bin/linuxsteamrt64:${LD_LIBRARY_PATH:-}"

export ENABLE_PATHMATCH=1

exec "${GAMEROOT}/bin/linuxsteamrt64/cs2" "${DEFAULT_ARGS[@]}" "$@"
`

func ensureCSMLauncherSh(ctx context.Context, w io.Writer, cs2User, gameDir string) error {
	if w == nil {
		w = io.Discard
	}
	gameDir = strings.TrimSpace(gameDir)
	if gameDir == "" {
		return fmt.Errorf("gameDir is empty")
	}
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		return fmt.Errorf("failed to create gameDir %s: %w", gameDir, err)
	}

	dst := filepath.Join(gameDir, "csm.sh")

	// Avoid rewriting if already up-to-date.
	if data, err := os.ReadFile(dst); err == nil && string(data) == csmManagedLauncherSh {
		_ = os.Chmod(dst, 0o755)
		_ = ensureOwnedByUser(cs2User, dst)
		return nil
	}

	fmt.Fprintf(w, "  [*] Installing alternate launcher -> %s\n", dst)
	if err := os.WriteFile(dst, []byte(csmManagedLauncherSh), 0o755); err != nil {
		return fmt.Errorf("failed to write %s: %w", dst, err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", dst, err)
	}

	// Best-effort ownership fix (non-fatal): prefer user:user.
	if err := ensureOwnedByUser(cs2User, dst); err != nil {
		fmt.Fprintf(w, "  [i] Warning: failed to chown %s to %s:%s (%v)\n", dst, cs2User, cs2User, err)
	}
	_ = ensureOwnedByUser(cs2User, gameDir)

	_ = ctx
	return nil
}
