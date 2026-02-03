package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type DoctorCheckStatus string

const (
	DoctorOK   DoctorCheckStatus = "OK"
	DoctorWarn DoctorCheckStatus = "WARN"
	DoctorFail DoctorCheckStatus = "FAIL"
)

type DoctorOptions struct {
	// Server selects which server(s) to check.
	// - 0 => all discovered servers
	// - N => server-N only
	Server int
}

type DoctorMeta struct {
	CS2User        string
	Servers        []int
	SteamRTMode    string
	SteamRTPlanned bool
}

type DoctorCheck struct {
	ID     string
	Title  string
	Status DoctorCheckStatus
	Detail string

	// Fix, when non-nil, applies an automated repair. It should be idempotent.
	Fix func(ctx context.Context) (string, error)

	// FixHint is shown even if Fix is nil (e.g. manual instructions).
	FixHint string
}

// DoctorScan runs a set of health checks and returns check results plus some
// basic metadata for reporting.
func DoctorScan(ctx context.Context, opts DoctorOptions) (DoctorMeta, []DoctorCheck, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return DoctorMeta{}, nil, err
	}

	userName := strings.TrimSpace(mgr.CS2User)
	if userName == "" {
		userName = DefaultCS2User
	}

	servers := []int{}
	if opts.Server > 0 {
		servers = []int{opts.Server}
	} else {
		for i := 1; i <= mgr.NumServers; i++ {
			servers = append(servers, i)
		}
	}

	steamRTPlanned, steamRTMode := shouldUseSteamRuntimeLauncher()

	meta := DoctorMeta{
		CS2User:        userName,
		Servers:        servers,
		SteamRTMode:    steamRTMode,
		SteamRTPlanned: steamRTPlanned,
	}

	var checks []DoctorCheck

	// Checks are appended below (implemented in this file for now).
	checks = append(checks, checkSteamcmd(meta))
	checks = append(checks, checkOwnership(meta))
	checks = append(checks, checkSteamRT(meta))
	checks = append(checks, checkLibV8(meta, opts))

	return meta, checks, nil
}

// FormatDoctorReport renders a stable, human-readable report.
func FormatDoctorReport(meta DoctorMeta, checks []DoctorCheck) string {
	var b strings.Builder
	fmt.Fprintln(&b, "CSM Doctor")
	fmt.Fprintln(&b, "==========")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "CS2 user: %s\n", meta.CS2User)
	if len(meta.Servers) == 0 {
		fmt.Fprintln(&b, "Servers: (none discovered)")
	} else {
		fmt.Fprintf(&b, "Servers: %s\n", joinInts(meta.Servers))
	}
	if meta.SteamRTPlanned {
		fmt.Fprintf(&b, "Steam Runtime: planned (%s)\n", meta.SteamRTMode)
	} else {
		fmt.Fprintf(&b, "Steam Runtime: not planned (%s)\n", meta.SteamRTMode)
	}
	fmt.Fprintln(&b)

	for _, c := range checks {
		fmt.Fprintf(&b, "[%s] %s\n", c.Status, c.Title)
		if strings.TrimSpace(c.Detail) != "" {
			for _, line := range strings.Split(strings.TrimRight(c.Detail, "\n"), "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
		if strings.TrimSpace(c.FixHint) != "" {
			for _, line := range strings.Split(strings.TrimRight(c.FixHint, "\n"), "\n") {
				fmt.Fprintf(&b, "  Fix: %s\n", line)
			}
		} else if c.Fix != nil {
			fmt.Fprintf(&b, "  Fix: available (run doctor to apply)\n")
		}
		fmt.Fprintln(&b)
	}

	return b.String()
}

func joinInts(ns []int) string {
	var parts []string
	for _, n := range ns {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}

func checkSteamcmd(meta DoctorMeta) DoctorCheck {
	// steamcmd must be runnable (CSM generally expects steamcmd in PATH).
	if p, err := exec.LookPath("steamcmd"); err == nil {
		// If steamcmd is resolved to /usr/games/steamcmd, prefer installing the
		// /usr/bin/steamcmd wrapper so other tools and scripts behave predictably.
		if filepath.Clean(p) == "/usr/games/steamcmd" && needsSteamcmdWrapper() {
			return DoctorCheck{
				ID:     "steamcmd-wrapper",
				Title:  "SteamCMD wrapper (/usr/bin/steamcmd)",
				Status: DoctorFail,
				Detail: "steamcmd resolves to /usr/games/steamcmd but /usr/bin/steamcmd wrapper is missing or not executable.",
				Fix: func(ctx context.Context) (string, error) {
					var buf bytes.Buffer
					err := EnsureSteamcmdWrapper(&buf)
					return buf.String(), err
				},
			}
		}
		return DoctorCheck{
			ID:     "steamcmd",
			Title:  "SteamCMD available",
			Status: DoctorOK,
			Detail: fmt.Sprintf("steamcmd found in PATH (%s).", p),
		}
	}

	// Not found in PATH. If /usr/games/steamcmd exists, we can fix by creating wrapper.
	if _, err := os.Stat("/usr/games/steamcmd"); err == nil {
		return DoctorCheck{
			ID:     "steamcmd-wrapper",
			Title:  "SteamCMD wrapper (/usr/bin/steamcmd)",
			Status: DoctorFail,
			Detail: "steamcmd is installed at /usr/games/steamcmd but not available as /usr/bin/steamcmd.",
			Fix: func(ctx context.Context) (string, error) {
				var buf bytes.Buffer
				err := EnsureSteamcmdWrapper(&buf)
				return buf.String(), err
			},
		}
	}

	return DoctorCheck{
		ID:      "steamcmd",
		Title:   "SteamCMD available",
		Status:  DoctorFail,
		Detail:  "steamcmd not found in PATH (and /usr/games/steamcmd not found).",
		FixHint: "Install it (Debian/Ubuntu): sudo apt-get update && sudo apt-get install steamcmd",
	}
}

func checkOwnership(meta DoctorMeta) DoctorCheck {
	u, err := user.Lookup(meta.CS2User)
	if err != nil {
		return DoctorCheck{
			ID:     "ownership",
			Title:  "Ownership under /home/<cs2user>",
			Status: DoctorWarn,
			Detail: fmt.Sprintf("Could not look up user %q: %v", meta.CS2User, err),
		}
	}
	uid, _ := strconv.Atoi(u.Uid)

	paths := []string{
		filepath.Join("/home", meta.CS2User),
		filepath.Join("/home", meta.CS2User, "master-install"),
		filepath.Join("/home", meta.CS2User, "cs2-config"),
		filepath.Join("/home", meta.CS2User, "logs"),
	}
	// Spot-check server-1 as a representative (if any servers exist).
	if len(meta.Servers) > 0 {
		paths = append(paths, filepath.Join("/home", meta.CS2User, fmt.Sprintf("server-%d", meta.Servers[0])))
	}

	var bad []string
	for _, p := range paths {
		fi, serr := os.Stat(p)
		if serr != nil || !fi.IsDir() {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if int(st.Uid) != uid {
			bad = append(bad, p)
		}
	}

	if len(bad) == 0 {
		return DoctorCheck{
			ID:     "ownership",
			Title:  "Ownership under /home/<cs2user>",
			Status: DoctorOK,
			Detail: "No obvious ownership mismatches detected in key directories.",
		}
	}

	return DoctorCheck{
		ID:     "ownership",
		Title:  "Ownership under /home/<cs2user>",
		Status: DoctorFail,
		Detail: "Some directories are not owned by the CS2 user:\n- " + strings.Join(bad, "\n- "),
		Fix: func(ctx context.Context) (string, error) {
			var buf bytes.Buffer
			fmt.Fprintf(&buf, "[*] Fixing ownership under /home/%s ...\n", meta.CS2User)
			err := fixServerOwnership(meta.CS2User)
			if err == nil {
				fmt.Fprintln(&buf, "[✓] Ownership fixed")
			}
			return buf.String(), err
		},
	}
}

func checkSteamRT(meta DoctorMeta) DoctorCheck {
	if !meta.SteamRTPlanned {
		return DoctorCheck{
			ID:     "steamrt",
			Title:  "Steam Runtime installed (SteamRT3)",
			Status: DoctorOK,
			Detail: "Not needed (Steam Runtime launcher is not planned for this OS/env).",
		}
	}
	if steamRuntimeInstalled(meta.CS2User) {
		return DoctorCheck{
			ID:     "steamrt",
			Title:  "Steam Runtime installed (SteamRT3)",
			Status: DoctorOK,
			Detail: "Steam Runtime is installed.",
		}
	}
	return DoctorCheck{
		ID:     "steamrt",
		Title:  "Steam Runtime installed (SteamRT3)",
		Status: DoctorFail,
		Detail: fmt.Sprintf("Steam Runtime is expected (%s) but /home/%s/steamrt/run was not found.", meta.SteamRTMode, meta.CS2User),
		Fix: func(ctx context.Context) (string, error) {
			var buf bytes.Buffer
			err := ensureSteamRuntimeInstalled(ctx, &buf, meta.CS2User)
			return buf.String(), err
		},
	}
}

func checkLibV8(meta DoctorMeta, opts DoctorOptions) DoctorCheck {
	// Only run this check if server selection resolves to something.
	if len(meta.Servers) == 0 {
		return DoctorCheck{
			ID:     "libv8",
			Title:  "CS2 shared libs (libv8.so) present",
			Status: DoctorWarn,
			Detail: "No servers discovered; skipping libv8 check.",
		}
	}

	var missing []int
	var hints []string
	for _, n := range meta.Servers {
		root := filepath.Join("/home", meta.CS2User, fmt.Sprintf("server-%d", n), "game")
		ok, hint := serverHasLibV8(root)
		if !ok {
			missing = append(missing, n)
			hints = append(hints, fmt.Sprintf("server-%d checked: %s", n, hint))
		}
	}
	if len(missing) == 0 {
		return DoctorCheck{
			ID:     "libv8",
			Title:  "CS2 shared libs (libv8.so) present",
			Status: DoctorOK,
			Detail: "libv8.so appears present for checked servers.",
		}
	}

	// Fix scope: if the user targeted a single server, repair that server. If
	// they asked for all, repair all (simplest/safest for fresh installs).
	fixServer := 0
	if opts.Server > 0 {
		fixServer = opts.Server
	}
	return DoctorCheck{
		ID:     "libv8",
		Title:  "CS2 shared libs (libv8.so) present",
		Status: DoctorFail,
		Detail: fmt.Sprintf("Missing or not found for: %s\n%s", joinInts(missing), strings.Join(hints, "\n")),
		Fix: func(ctx context.Context) (string, error) {
			return FixLibV8WithContext(ctx, fixServer)
		},
		FixHint: "This runs SteamCMD validate on master-install and rsyncs master -> server(s).",
	}
}
