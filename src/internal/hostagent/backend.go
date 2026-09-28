package hostagent

import (
	"context"
	"os"
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

// HostFacts are the machine parts of host.inventory.
type HostFacts struct {
	Hostname  string
	OS        string
	Resources Resources
	CS2       CS2Facts
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
	MasterBuild     int64  `json:"master_build"`
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
	"insecure_dev": true, "fleet_insecure_dev": true,
	"ca_file": true, "fleet_ca_file": true,
}

// RenderFleetCfg returns fleet.cfg with url, enroll_key and (when set)
// insecure_dev / ca_file set by csm. Ready Up's format is `key = value` with
// // or # comments; lines csm does not own (offline_pause_minutes, spool
// limits, a commented template) are kept.
func RenderFleetCfg(existing, platformURL, enrollKey string, insecure bool, caFile string) string {
	var keep []string
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if k, _, ok := strings.Cut(t, "="); ok && !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "#") {
			if fleetCfgKeys[strings.ToLower(strings.TrimSpace(k))] {
				continue
			}
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
	return b.String()
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
	content := RenderFleetCfg(string(existing), platformURL, enrollKey, insecure, caFile)
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
