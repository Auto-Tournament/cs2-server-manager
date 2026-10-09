package hostagent

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
)

// Backend is the machine: what the csm package does for the agent. Every
// method must be safe to call from several goroutines; the agent serializes
// the heavy jobs (create, updates, remove) itself.
type Backend interface {
	// Servers lists every server-N with its process and Ready Up state.
	// Server numbers are 1-based and contiguous as csm lays them out.
	Servers(ctx context.Context) ([]ServerState, error)
	// Host returns the machine facts of host.inventory.
	Host(ctx context.Context) (HostFacts, error)

	Start(ctx context.Context, server int, launchMode string) error
	// Stop asks the server to quit and kills it after grace.
	Stop(ctx context.Context, server int, graceS int) error
	Restart(ctx context.Context, server int) error

	// CreateServer provisions the next server-N. beforeStart runs after the
	// files are in place and before the server is started (fleet.cfg is
	// written there). It returns the new server's number and csm's log.
	CreateServer(ctx context.Context, beforeStart func(serverDir string) error) (int, string, error)
	// RemoveLastServer deletes the highest-numbered server.
	RemoveLastServer(ctx context.Context) (string, error)

	// UpdateGame runs SteamCMD and syncs the listed servers (nil = all).
	UpdateGame(ctx context.Context, servers []int, progress func(step string)) (string, error)
	// InstallReadyUp installs a Ready Up bundle into one server's folder.
	InstallReadyUp(ctx context.Context, server int, plan ReadyUpPlan) (string, error)

	// SetUpdatesHold sets csm's update hold (on | off | auto).
	SetUpdatesHold(mode string) error

	// LogFile is the file behind a log source (console/readyup need a server).
	LogFile(server int, source string) (string, error)

	// ChownToCS2User hands a file csm wrote to the CS2 user (no-op when csm
	// already runs as that user).
	ChownToCS2User(path string) error
}

// FirstServerCreator is an optional Backend extension: a host that can create
// its first server from the platform (csm instance mode) reports the game
// port that server gets. ok=false keeps the "run the install wizard" answer.
type FirstServerCreator interface {
	FirstServerGamePort() (port int, ok bool)
}

// FirstServersBootstrapper is an optional Backend extension: a server-N host
// with no servers yet installs its first `count` servers the way the csm
// install wizard does (the game, the servers, the plugin stack), so
// server.create works on a machine that was only linked (issue #108). It
// returns the server numbers it created and started.
type FirstServersBootstrapper interface {
	BootstrapFirstServers(ctx context.Context, count int, beforeStart func(serverDir string) error, progress func(step string, pct int)) ([]int, string, error)
	// FirstServersGamePort is the game port server-1 gets.
	FirstServersGamePort() int
}

// ReadyUpCurrentChecker is an optional Backend extension: a server that
// already runs the planned Ready Up (csm instance mode: the shared layer has
// it and the instance is mounted on that layer) needs no install, so the
// agent leaves it alone instead of stopping and restarting it.
type ReadyUpCurrentChecker interface {
	ReadyUpCurrent(server int, plan ReadyUpPlan) (current bool, why string)
}

// ReadyUpBundleChooser is an optional Backend extension: the bundle to
// install when host.update_plugins asks for `requested`. csm instance mode
// shares one Ready Up layer between all servers and builds it with the full
// bundle, so the platform's default (essentials) does not shrink it.
type ReadyUpBundleChooser interface {
	ReadyUpBundleFor(requested string) string
}

// LicenseAnswerAdopter takes the Ready Up license use the platform's admin
// accepted (accept_license on server.create and host.update_plugins) when
// the host has no answer of its own. It reports whether it saved one.
type LicenseAnswerAdopter interface {
	AdoptLicenseAnswer(use string) (bool, error)
}

// HostFacts are the machine parts of host.inventory.
type HostFacts struct {
	Hostname  string
	OS        string
	Resources Resources
	CS2       CS2Facts
	// License is which license this host's servers run under; nil without a key.
	License *InvLicense
}

// InvLicense is host.inventory.license: whether this host runs under a
// license of its own (a hosting provider's `csm license set`), so the
// platform leaves its servers out of the platform's own count, and the cap
// the host set for the platform (`csm license cap`).
type InvLicense struct {
	Own       bool   `json:"own"`
	LicenseID string `json:"license_id,omitempty"`
	Cap       *int   `json:"cap,omitempty"`
}

// Resources is host.inventory.resources.
type Resources struct {
	CPUs      int        `json:"cpus"`
	Load1     float64    `json:"load1"`
	RAMMB     int64      `json:"ram_mb"`
	RAMFreeMB int64      `json:"ram_free_mb"`
	Disk      []DiskInfo `json:"disk"`
}

// DiskInfo is one mount.
type DiskInfo struct {
	Mount   string  `json:"mount"`
	TotalGB float64 `json:"total_gb"`
	FreeGB  float64 `json:"free_gb"`
}

// CS2Facts is host.inventory.cs2.
type CS2Facts struct {
	MasterBuild int64 `json:"master_build"`
	// MasterPatch is PatchVersion ("1.41.8.9") of the CS2 install new
	// servers run, the unit Steam's UpToDateCheck takes. The platform asks
	// Steam with it and sends host.update_game when it is behind.
	MasterPatch     string `json:"master_patch,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdatesHold     string `json:"updates_hold"`
}

// ServerState is one server as the backend sees it.
type ServerState struct {
	Number     int
	Dir        string
	GamePort   int
	TVPort     int
	StatusPort int
	Running    bool
	// Updating is csm's own UPDATING marker.
	Updating   bool
	PID        int
	StartedAt  int64
	CPUPct     *float64
	RSSMB      *int64
	CS2Build   int64
	LaunchArgs []string

	// ReadyUp
	ReadyUpInstalled string // version, "unknown" when installed without an answer, "" = not installed
	ReadyUpPresent   bool   // status.json exists: Ready Up runs (or ran) here
	InstallID        string
	ServerID         string
	Health           string // ok | failing | no_response | not_running
	Phase            string
	// UpdateSafe is nil when Ready Up gave no verdict.
	UpdateSafe *bool
	// Busy describes a server with update_safe=false for messages.
	Busy string
}

// ReadyUpPlan says where a Ready Up bundle comes from (see readyup_bundle.go).
type ReadyUpPlan struct {
	// Component is the install.sh bundle name: essentials or full.
	Component string
	// Version is the release tag for install.sh --version (empty with Zip).
	Version string
	// Zip is a local bundle zip for install.sh --zip.
	Zip string
	// Installer is a local copy of Ready Up's install.sh.
	Installer string
	// AcceptLicense is passed as --accept-license=… when set.
	AcceptLicense string
}

// --- fleet.cfg ----------------------------------------------------------------

// FleetCfgPath is where Ready Up reads its fleet link settings, relative to a
// server-N directory (FLEET.md §4.1 B, Ready Up cfg/ReadyUp/fleet.cfg).
func FleetCfgPath(serverDir string) string {
	return serverDir + "/game/csgo/cfg/ReadyUp/fleet.cfg"
}

// fleetCfgKeys are the keys csm owns in fleet.cfg; any other line is kept.
var fleetCfgKeys = map[string]bool{
	"url": true, "fleet_url": true,
	"enroll_key": true, "fleet_enroll_key": true,
	"enroll_code": true, "fleet_enroll_code": true,
	"insecure_dev": true, "fleet_insecure_dev": true, "insecure": true, "fleet_insecure": true,
	"ca_file": true, "fleet_ca_file": true,
}

// RenderFleetCfg returns fleet.cfg with url, enroll_key and (when set)
// insecure_dev / ca_file set by csm. Ready Up's format is `key = value` with
// // or # comments; lines csm does not own (offline_pause_minutes, spool
// limits, a commented template) are kept.
//
// publicAddr, when set and the file has no public_addr yet, becomes
// `public_addr`: the address players connect to. Without it the platform
// falls back to the address the server's link comes from, which behind a
// proxy or tunnel is another machine (NTLAN 2026-10-05). A public_addr
// already in the file is the operator's and stays.
func RenderFleetCfg(existing, platformURL, enrollKey string, insecure bool, caFile, publicAddr string) string {
	var keep []string
	afterAddrMarker := false
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		csmAddr := afterAddrMarker
		afterAddrMarker = false
		if k, _, ok := strings.Cut(t, "="); ok && !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "#") {
			key := strings.ToLower(strings.TrimSpace(k))
			if fleetCfgKeys[key] || (csmAddr && key == "public_addr") {
				continue
			}
		}
		if t == fleetCfgAddrMarker {
			afterAddrMarker = true
			continue
		}
		if strings.HasPrefix(t, "// csm host agent") {
			continue
		}
		keep = append(keep, line)
	}
	for len(keep) > 0 && strings.TrimSpace(keep[len(keep)-1]) == "" {
		keep = keep[:len(keep)-1]
	}
	var b strings.Builder
	for _, l := range keep {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(keep) > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("// csm host agent: this server enrolls itself with the fleet key (FLEET.md §4.1 B).\n")
	b.WriteString("// csm host agent: url and enroll_key are rewritten by csm; edit the other settings freely.\n")
	b.WriteString("url = " + platformURL + "\n")
	b.WriteString("enroll_key = " + enrollKey + "\n")
	if insecure {
		b.WriteString("insecure_dev = 1\n")
	}
	if strings.TrimSpace(caFile) != "" {
		b.WriteString("ca_file = " + caFile + "\n")
	}
	if publicAddr = strings.TrimSpace(publicAddr); publicAddr != "" && !hasCfgKey(keep, "public_addr", "fleet_public_addr") {
		b.WriteString(fleetCfgAddrMarker + "\n")
		b.WriteString("public_addr = " + publicAddr + "\n")
	}
	return b.String()
}

// fleetCfgAddrMarker comes right before a public_addr csm wrote, so the next
// render replaces that one (the machine's address may change) and keeps an
// operator's own.
const fleetCfgAddrMarker = "// csm host agent: public_addr below is this machine's address; remove this line to keep your own."

// hasCfgKey reports whether one of the lines sets one of the keys.
func hasCfgKey(lines []string, keys ...string) bool {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue
		}
		k, _, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		for _, want := range keys {
			if strings.EqualFold(strings.TrimSpace(k), want) {
				return true
			}
		}
	}
	return false
}

// MachineAddress is this machine's primary IPv4 address (the source of its
// default route), or "" when it cannot tell. A var so tests can pin it.
var MachineAddress = func() string {
	if out, err := exec.Command("ip", "-4", "route", "get", "1.1.1.1").CombinedOutput(); err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "src" && i+1 < len(fields) {
				if ip := net.ParseIP(fields[i+1]); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
					return ip.String()
				}
			}
		}
	}
	return ""
}

// WriteFleetCfg writes (or updates) a server's fleet.cfg, mode 0600, so Ready
// Up self-enrolls on its next start. chown hands the file to the CS2 user.
func WriteFleetCfg(serverDir, platformURL, enrollKey string, insecure bool, caFile string, chown func(string) error) error {
	if !IsFleetKey(enrollKey) {
		return errNoKey
	}
	path := FleetCfgPath(serverDir)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := RenderFleetCfg(string(existing), platformURL, enrollKey, insecure, caFile, MachineAddress())
	dir := serverDir + "/game/csgo/cfg/ReadyUp"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if chown != nil {
		_ = chown(dir)
	}
	if err := writeFileAtomic(path, []byte(content), 0o600); err != nil {
		return err
	}
	if chown != nil {
		if err := chown(path); err != nil {
			return err
		}
	}
	return nil
}

type fleetCfgError string

func (e fleetCfgError) Error() string { return string(e) }

const errNoKey = fleetCfgError("no fleet enrollment key: link the host with a key (csm link <url> rfk_…) or send enroll_key")
