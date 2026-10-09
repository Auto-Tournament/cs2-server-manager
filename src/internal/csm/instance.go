package csm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Instance mode: many CS2 servers from one read-only install
//
// A server-N folder is a full copy of the master install (~70 GB with
// hardlinked VPKs saving most of it). An instance is not a copy: it runs the
// master install through kernel overlayfs inside an unprivileged user + mount
// namespace (`unshare --user --map-root-user --mount`, no root, no sudo, no
// fuse-overlayfs). Every instance sees
//
//	upper  (instance-N/upper: everything this instance writes)
//	layer  (instances/layers/<id>: Ready Up, installed once by its install.sh)
//	game   (the CS2 install the layer was built on: master-install, or a
//	        game version in instances/games/<id> after a CS2 update; never
//	        written, see instance_game.go)
//
// merged at instance-N/merged, visible only inside that instance's namespace.
// What would collide between servers sharing one folder (Ready Up's
// state.json + lock and status.json, steam_appid.txt, rpt/, save/, demos,
// backups, $HOME/Steam) lands in each instance's own upper directory; each
// instance also gets its own HOME and, by default, its own /dev/shm.
//
// Ports: game = base + 10*N, GOTV +1, client +2, Ready Up status HTTP +7.
//
// The Ready Up layer and the game are versioned: a new release builds a new
// layer next to the old one, a CS2 update makes a new game version and a new
// layer on it, and layers/current moves. Running instances keep the layer
// and game version they were mounted with; csm restarts them onto the new
// ones only when they are idle (instance_update.go), never mid-match, and
// never while updates are on hold.

const (
	// ServerBackendServers is the default: full-copy server-N folders.
	ServerBackendServers = "servers"
	// ServerBackendInstances runs overlay instances instead.
	ServerBackendInstances = "instances"

	// DefaultInstanceBasePort makes instance 1 use 27015, like server-1.
	DefaultInstanceBasePort = 27005

	// Instance port offsets from the game port.
	instanceTVOffset     = 1
	instanceClientOffset = 2

	instanceSessionPrefix = "cs2-inst-"
	instanceDirPrefix     = "instance-"
	maxInstanceNumber     = 999

	// Files csm writes into an instance's upper layer.
	instanceCfgName       = "instance.cfg"
	instanceCustomCfgName = "instance_custom.cfg"

	EnvServerBackend       = "CSM_SERVER_BACKEND"
	EnvInstanceRoot        = "CSM_INSTANCE_ROOT"
	EnvInstanceMaster      = "CSM_MASTER_DIR"
	EnvInstanceBasePort    = "CSM_INSTANCE_BASE_PORT"
	EnvInstanceNice        = "CSM_INSTANCE_NICE"
	EnvInstanceSteamClient = "CSM_STEAMCLIENT"
	// EnvInstanceMasterReadOnly=1: csm never runs SteamCMD on the master install
	// (it belongs to another user or is updated elsewhere).
	EnvInstanceMasterReadOnly = "CSM_INSTANCE_MASTER_READONLY"
)

// InstanceSettings is <csm root>/instances.json.
type InstanceSettings struct {
	// Backend is "servers" (default) or "instances": what the host agent
	// reports and acts on.
	Backend string `json:"backend,omitempty"`
	// BasePort: instance N plays on BasePort + 10*N.
	BasePort int `json:"base_port,omitempty"`
	// Map is the start map (default de_dust2).
	Map string `json:"map,omitempty"`
	// MaxPlayers (default: the shared config's, else 10).
	MaxPlayers int `json:"max_players,omitempty"`
	// PrivateShm mounts a private /dev/shm per instance (nil = on). Every
	// instance is uid 0 inside its namespace, so Steam's /dev/shm/u0-* files
	// would otherwise be shared between instances.
	PrivateShm *bool `json:"private_shm,omitempty"`
	// Nice is the CPU niceness of the game process (0..19).
	Nice int `json:"nice,omitempty"`
	// Insecure starts cs2 with -insecure (no VAC): for test servers, e.g. a
	// LAN test with a VAC-banned account. Off by default.
	Insecure bool `json:"insecure,omitempty"`
}

// InstanceSettingKeys are the keys `csm instance config` accepts.
var InstanceSettingKeys = []string{"backend", "base_port", "map", "max_players", "private_shm", "nice", "insecure"}

func instanceSettingsPath() string { return filepath.Join(ResolveRoot(), "instances.json") }

// LoadInstanceSettings reads instances.json (a missing file is the defaults).
func LoadInstanceSettings() (InstanceSettings, error) {
	var s InstanceSettings
	data, err := os.ReadFile(instanceSettingsPath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", instanceSettingsPath(), err)
	}
	return s, nil
}

// Resolved applies environment overrides and defaults.
func (s InstanceSettings) Resolved() InstanceSettings {
	r := s
	r.Backend = strings.ToLower(strings.TrimSpace(getenvDefault(EnvServerBackend, s.Backend)))
	if r.Backend != ServerBackendInstances {
		r.Backend = ServerBackendServers
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(EnvInstanceBasePort))); err == nil && v > 0 {
		r.BasePort = v
	}
	if r.BasePort <= 0 {
		r.BasePort = DefaultInstanceBasePort
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(EnvInstanceNice))); err == nil {
		r.Nice = v
	}
	if r.Nice < 0 {
		r.Nice = 0
	}
	if r.Nice > 19 {
		r.Nice = 19
	}
	if strings.TrimSpace(r.Map) == "" {
		r.Map = "de_dust2"
	}
	return r
}

// PrivateShmOn reports whether instances get their own /dev/shm.
func (s InstanceSettings) PrivateShmOn() bool { return s.PrivateShm == nil || *s.PrivateShm }

// SetInstanceSetting changes one setting and saves it.
func SetInstanceSetting(key, value string) (InstanceSettings, error) {
	s, err := LoadInstanceSettings()
	if err != nil {
		return s, err
	}
	value = strings.TrimSpace(value)
	atoi := func(min, max int) (int, error) {
		n, err := strconv.Atoi(value)
		if err != nil || n < min || n > max {
			return 0, fmt.Errorf("%s must be a number from %d to %d", key, min, max)
		}
		return n, nil
	}
	switch key {
	case "backend":
		switch strings.ToLower(value) {
		case ServerBackendServers, "server", "folders":
			s.Backend = ServerBackendServers
		case ServerBackendInstances, "instance", "overlay":
			s.Backend = ServerBackendInstances
		default:
			return s, fmt.Errorf("backend must be servers or instances")
		}
	case "base_port":
		n, err := atoi(1024, 65535-10*maxInstanceNumber/10)
		if err != nil {
			return s, err
		}
		s.BasePort = n
	case "map":
		if !regexp.MustCompile(`^[A-Za-z0-9_./-]{1,64}$`).MatchString(value) {
			return s, fmt.Errorf("not a map name: %q", value)
		}
		s.Map = value
	case "max_players":
		n, err := atoi(1, 64)
		if err != nil {
			return s, err
		}
		s.MaxPlayers = n
	case "private_shm":
		on, err := parseOnOff(value)
		if err != nil {
			return s, err
		}
		s.PrivateShm = &on
	case "insecure":
		on, err := parseOnOff(value)
		if err != nil {
			return s, err
		}
		s.Insecure = on
	case "nice":
		n, err := atoi(0, 19)
		if err != nil {
			return s, err
		}
		s.Nice = n
	default:
		return s, fmt.Errorf("unknown setting %q (one of: %s)", key, strings.Join(InstanceSettingKeys, ", "))
	}
	return s, writeJSONAtomic(instanceSettingsPath(), s)
}

func parseOnOff(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes":
		return true, nil
	case "off", "0", "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("expected on or off, got %q", v)
}

// InstanceBackendOn reports whether this host runs instances rather than
// server-N folders (instances.json backend, or CSM_SERVER_BACKEND).
func InstanceBackendOn() bool {
	s, err := LoadInstanceSettings()
	if err != nil {
		return false
	}
	return s.Resolved().Backend == ServerBackendInstances
}

// --- layout ------------------------------------------------------------------

// InstanceLayout is where instance mode keeps its files.
type InstanceLayout struct {
	User   string
	Root   string // /home/<user>/instances
	Master string // /home/<user>/master-install
}

func newInstanceLayout(user string) InstanceLayout {
	l := InstanceLayout{User: user}
	l.Root = strings.TrimSpace(os.Getenv(EnvInstanceRoot))
	if l.Root == "" {
		l.Root = filepath.Join("/home", user, "instances")
	}
	l.Master = strings.TrimSpace(os.Getenv(EnvInstanceMaster))
	if l.Master == "" {
		l.Master = filepath.Join("/home", user, "master-install")
	}
	l.Root, l.Master = filepath.Clean(l.Root), filepath.Clean(l.Master)
	return l
}

func (l InstanceLayout) LayersDir() string        { return filepath.Join(l.Root, "layers") }
func (l InstanceLayout) CurrentLayerLink() string { return filepath.Join(l.LayersDir(), "current") }
func (l InstanceLayout) Dir(n int) string {
	return filepath.Join(l.Root, fmt.Sprintf("%s%d", instanceDirPrefix, n))
}
func (l InstanceLayout) Upper(n int) string      { return filepath.Join(l.Dir(n), "upper") }
func (l InstanceLayout) Work(n int) string       { return filepath.Join(l.Dir(n), "work") }
func (l InstanceLayout) Merged(n int) string     { return filepath.Join(l.Dir(n), "merged") }
func (l InstanceLayout) Home(n int) string       { return filepath.Join(l.Dir(n), "home") }
func (l InstanceLayout) ConsoleLog(n int) string { return filepath.Join(l.Dir(n), "console.log") }
func (l InstanceLayout) StopFlag(n int) string   { return filepath.Join(l.Dir(n), "stopped") }
func (l InstanceLayout) InUseFile(n int) string  { return filepath.Join(l.Dir(n), "layer.inuse") }
func (l InstanceLayout) StateFile(n int) string  { return filepath.Join(l.Dir(n), "instance.json") }
func (l InstanceLayout) RunScript(n int) string  { return filepath.Join(l.Dir(n), "run.sh") }
func (l InstanceLayout) ExecScript(n int) string { return filepath.Join(l.Dir(n), "exec.sh") }

// CfgDir is the instance's game/csgo/cfg in its upper layer.
func (l InstanceLayout) CfgDir(n int) string {
	return filepath.Join(l.Upper(n), "game", "csgo", "cfg")
}

func instanceSession(n int) string { return fmt.Sprintf("%s%d", instanceSessionPrefix, n) }

// InstancePorts are the ports instance N uses.
type InstancePorts struct {
	Game, TV, Client, Status int
}

// instancePorts returns the ports of instance n for a base port.
func instancePorts(base, n int) (InstancePorts, error) {
	if n < 1 || n > maxInstanceNumber {
		return InstancePorts{}, fmt.Errorf("instance number must be 1..%d", maxInstanceNumber)
	}
	if base < 1024 {
		return InstancePorts{}, fmt.Errorf("base port %d is below 1024", base)
	}
	g := base + 10*n
	p := InstancePorts{Game: g, TV: g + instanceTVOffset, Client: g + instanceClientOffset, Status: g + ReadyUpPortOffset}
	if p.Status > 65535 {
		return InstancePorts{}, fmt.Errorf("instance %d would use port %d (base %d): too high", n, p.Status, base)
	}
	return p, nil
}

// overlayPathOK rejects paths overlayfs's option parser cannot take.
func overlayPathOK(p string) error {
	switch {
	case p == "" || !filepath.IsAbs(p):
		return fmt.Errorf("overlay path %q must be absolute", p)
	case strings.ContainsAny(p, ",:\n\\\"'$`"):
		return fmt.Errorf("overlay path %q contains a character overlayfs or the launch script cannot take (, : \\ quotes $ `)", p)
	}
	return nil
}

// overlayMountOptions is the -o value of the overlay mount; lowers are listed
// top first.
func overlayMountOptions(lowers []string, upper, work string) (string, error) {
	if len(lowers) == 0 {
		return "", errors.New("overlay needs at least one lower directory")
	}
	for _, p := range append(append([]string{}, lowers...), upper, work) {
		if err := overlayPathOK(p); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(lowers, ":"), upper, work), nil
}

// unshareArgv is the namespace every instance (and every layer build) runs
// in: a user namespace mapping the caller to root, with a private mount
// namespace so the overlay mount is invisible to everything else and dies
// with the last process in it.
func unshareArgv() []string {
	return []string{"unshare", "--user", "--map-root-user", "--mount", "--propagation", "private"}
}

// --- launch scripts ---------------------------------------------------------

// instanceLaunch is everything the launch scripts need.
type instanceLaunch struct {
	N           int
	L           InstanceLayout
	Ports       InstancePorts
	Map         string
	MaxPlayers  int
	PrivateShm  bool
	Nice        int
	SteamClient string
	Insecure    bool
}

// instanceCS2Args is the cs2 command line (relative to merged/game).
func instanceCS2Args(s instanceLaunch) []string {
	args := []string{"./bin/linuxsteamrt64/cs2", "-dedicated", "-ip", "0.0.0.0", "-usercon"}
	if s.Insecure {
		args = append(args, "-insecure") // instances.json insecure: no VAC (test servers)
	}
	return append(args,
		"-port", strconv.Itoa(s.Ports.Game),
		"+tv_port", strconv.Itoa(s.Ports.TV),
		"+clientport", strconv.Itoa(s.Ports.Client),
		"+maxplayers", strconv.Itoa(s.MaxPlayers),
		"+game_type", "0", "+game_mode", "1",
		"+exec", instanceCfgName,
		"+map", s.Map,
	)
}

// renderInstanceExecScript is exec.sh: run inside the namespace with the
// layer directory as $1 and the game version it sits on as $2, it mounts the
// overlay (and a private /dev/shm) and execs cs2 in the merged view.
func renderInstanceExecScript(s instanceLaunch) (string, error) {
	n := s.N
	if err := overlayPathOK(s.L.Master); err != nil {
		return "", err
	}
	opts, err := overlayMountOptions([]string{"/@LAYER@", "/@GAME@"}, s.L.Upper(n), s.L.Work(n))
	if err != nil {
		return "", err
	}
	if err := overlayPathOK(s.L.Merged(n)); err != nil {
		return "", err
	}
	opts = strings.Replace(opts, "/@LAYER@", "$LAYER", 1)
	opts = strings.Replace(opts, "/@GAME@", "$GAME", 1)
	q := shellQuote
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	fmt.Fprintf(&b, "# csm instance %d: mount + exec cs2 (runs inside its user+mount namespace; written by csm on every start)\n", n)
	b.WriteString("set -euo pipefail\n")
	b.WriteString("LAYER=\"${1:?layer directory}\"\n")
	fmt.Fprintf(&b, "GAME=\"${2:-%s}\"\n", s.L.Master)
	b.WriteString("case \"$LAYER$GAME\" in *,*|*:*) echo \"[csm] bad layer or game path $LAYER $GAME\" >&2; exit 1 ;; esac\n")
	fmt.Fprintf(&b, "mount -t overlay overlay -o \"%s\" %s\n", opts, q(s.L.Merged(n)))
	if s.PrivateShm {
		b.WriteString("mount -t tmpfs -o mode=1777,size=1g tmpfs /dev/shm || echo \"[csm] warning: no private /dev/shm (sharing the host's)\"\n")
	}
	fmt.Fprintf(&b, "cd %s\n", q(filepath.Join(s.L.Merged(n), "game")))
	fmt.Fprintf(&b, "export HOME=%s\n", q(s.L.Home(n)))
	fmt.Fprintf(&b, "export LD_LIBRARY_PATH=%s\"${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\"\n", q(filepath.Join(s.L.Merged(n), "game", "bin", "linuxsteamrt64")))
	b.WriteString("echo \"[csm] instance mounted on layer $(basename \"$LAYER\"), game $GAME: $(date -u +%FT%TZ)\"\n")
	args := instanceCS2Args(s)
	for i, a := range args {
		args[i] = q(a)
	}
	fmt.Fprintf(&b, "exec nice -n %d %s\n", s.Nice, strings.Join(args, " "))
	return b.String(), nil
}

// Crash restarts: the supervisor restarts cs2 whenever it exits without
// `csm instance stop`, and gives up after instanceCrashLimit exits within
// instanceCrashWindow seconds (a crash loop).
const (
	instanceCrashLimit   = 5
	instanceCrashWindow  = 600
	instanceRestartDelay = 10
)

// renderInstanceRunScript is run.sh, the supervisor tmux runs: it resolves
// the current layer and the game version it was built on, records both,
// runs exec.sh in a fresh namespace and restarts it after a crash.
func renderInstanceRunScript(s instanceLaunch) string {
	n := s.N
	q := shellQuote
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	fmt.Fprintf(&b, "# csm instance %d supervisor (written by csm on every start; `csm instance stop %d` stops it)\n", n, n)
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "STOP=%s\nINUSE=%s\nCURRENT=%s\nPIN=%s\nEXEC=%s\nMASTER=%s\n", q(s.L.StopFlag(n)), q(s.L.InUseFile(n)), q(s.L.CurrentLayerLink()), q(s.L.PinFile(n)), q(s.L.ExecScript(n)), q(s.L.Master))
	b.WriteString("exits=()\n")
	b.WriteString("while :; do\n")
	b.WriteString("  if [ -s \"$PIN\" ]; then\n")
	b.WriteString("    # a private layer (csm instance layer build --for N), never layers/current\n")
	b.WriteString("    LAYER=\"$(dirname \"$CURRENT\")/$(head -n1 \"$PIN\")\"\n")
	b.WriteString("    [ -d \"$LAYER\" ] || { echo \"[csm] pinned layer $LAYER is gone (csm instance layer build --for N)\"; exit 1; }\n")
	b.WriteString("  else\n")
	b.WriteString("    LAYER=$(readlink -f \"$CURRENT\") || { echo \"[csm] no Ready Up layer at $CURRENT (csm instance layer build)\"; exit 1; }\n")
	b.WriteString("  fi\n")
	b.WriteString("  GAME=$(cat \"$LAYER.game\" 2>/dev/null) || GAME=\"\"\n")
	b.WriteString("  [ -n \"$GAME\" ] || GAME=\"$MASTER\"\n")
	b.WriteString("  printf '%s\\n%s\\n' \"$LAYER\" \"$GAME\" > \"$INUSE\"\n")
	fmt.Fprintf(&b, "  %s /bin/bash \"$EXEC\" \"$LAYER\" \"$GAME\"\n", strings.Join(unshareArgv(), " "))
	b.WriteString("  code=$?\n")
	fmt.Fprintf(&b, "  echo \"[csm] instance %d exited with code $code at $(date -u +%%FT%%TZ)\"\n", n)
	b.WriteString("  [ -e \"$STOP\" ] && { rm -f \"$INUSE\"; exit 0; }\n")
	b.WriteString("  now=$(date +%s); kept=()\n")
	fmt.Fprintf(&b, "  for t in \"${exits[@]}\"; do (( now - t < %d )) && kept+=(\"$t\"); done\n", instanceCrashWindow)
	b.WriteString("  exits=(\"${kept[@]}\" \"$now\")\n")
	fmt.Fprintf(&b, "  if (( ${#exits[@]} >= %d )); then\n", instanceCrashLimit)
	fmt.Fprintf(&b, "    echo \"[csm] instance %d exited %d times in %d minutes; not restarting it again (csm instance start %d)\"\n", n, instanceCrashLimit, instanceCrashWindow/60, n)
	b.WriteString("    rm -f \"$INUSE\"; exit 1\n")
	b.WriteString("  fi\n")
	fmt.Fprintf(&b, "  echo \"[csm] restarting in %ds (crash restart)\"\n", instanceRestartDelay)
	fmt.Fprintf(&b, "  sleep %d\n", instanceRestartDelay)
	b.WriteString("  [ -e \"$STOP\" ] && { rm -f \"$INUSE\"; exit 0; }\n")
	b.WriteString("done\n")
	return b.String()
}

// renderInstanceCfg is cfg/instance.cfg, executed at launch. csm rewrites it
// on every start; instance_custom.cfg (exec'd last when it exists) is the
// operator's.
func renderInstanceCfg(n int, hostname, rcon string, licenseCfg, customCfg bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// csm instance %d: written by csm on every start; edits are overwritten.\n", n)
	fmt.Fprintf(&b, "// Put your own settings in cfg/%s (exec'd last).\n", instanceCustomCfgName)
	fmt.Fprintf(&b, "hostname %q\n", hostname)
	if rcon != "" {
		fmt.Fprintf(&b, "rcon_password %q\n", rcon)
	}
	b.WriteString("sv_hibernate_when_empty 0\n")
	b.WriteString("tv_enable 1\n")
	b.WriteString("sv_lan 0\n")
	b.WriteString("// Ready Up reads the match log: logging must be on.\n")
	b.WriteString("log on\n")
	if licenseCfg {
		fmt.Fprintf(&b, "exec %s\n", readyUpLicenseCfg)
	}
	if customCfg {
		fmt.Fprintf(&b, "exec %s\n", instanceCustomCfgName)
	}
	return b.String()
}

// --- state -------------------------------------------------------------------

// instanceState is instance-N/instance.json.
type instanceState struct {
	Number    int   `json:"number"`
	CreatedAt int64 `json:"created_at"`
	// StartedAt / MasterBuild are recorded by the last start.
	StartedAt   int64 `json:"started_at,omitempty"`
	MasterBuild int64 `json:"master_build,omitempty"`
}

func readInstanceState(path string) (instanceState, error) {
	var s instanceState
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// --- manager -----------------------------------------------------------------

// InstanceManager runs instances for the CS2 user.
type InstanceManager struct {
	L InstanceLayout
	S InstanceSettings
}

// NewInstanceManager loads the settings and layout.
func NewInstanceManager() (*InstanceManager, error) {
	s, err := LoadInstanceSettings()
	if err != nil {
		return nil, err
	}
	return &InstanceManager{L: newInstanceLayout(configuredCS2User()), S: s.Resolved()}, nil
}

// requireInstancePrivileges: instances run as the CS2 user (csm is root or
// that user).
func (m *InstanceManager) requireInstancePrivileges(what string) error {
	return requireRootOrCS2User(what, m.L.User)
}

// Ports of instance n.
func (m *InstanceManager) Ports(n int) (InstancePorts, error) { return instancePorts(m.S.BasePort, n) }

var instanceDirRe = regexp.MustCompile(`^` + instanceDirPrefix + `([0-9]+)$`)

// List returns the created instances, lowest first.
func (m *InstanceManager) List() []int {
	entries, err := os.ReadDir(m.L.Root)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		mm := instanceDirRe.FindStringSubmatch(e.Name())
		if !e.IsDir() || mm == nil {
			continue
		}
		n, err := strconv.Atoi(mm[1])
		if err != nil || n < 1 || n > maxInstanceNumber {
			continue
		}
		if _, err := os.Stat(m.L.StateFile(n)); err == nil {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// Exists reports whether instance n was created.
func (m *InstanceManager) Exists(n int) bool {
	_, err := os.Stat(m.L.StateFile(n))
	return err == nil
}

func (m *InstanceManager) nextFree() int {
	used := map[int]bool{}
	for _, n := range m.List() {
		used[n] = true
	}
	for n := 1; n <= maxInstanceNumber; n++ {
		if used[n] || m.numberTaken(n) {
			continue
		}
		return n
	}
	return 0
}

// instancePortInUse is portInUse, swappable in tests.
var instancePortInUse = portInUse

// numberTaken: instance n would clash with a classic server-N folder (same
// port scheme, even while that server is stopped) or its game port is in use.
func (m *InstanceManager) numberTaken(n int) bool {
	classic := filepath.Join(filepath.Dir(m.L.Root), fmt.Sprintf("server-%d", n))
	if fi, err := os.Stat(classic); err == nil && fi.IsDir() {
		return true
	}
	if ports, err := m.Ports(n); err == nil && instancePortInUse(ports.Game) {
		return true
	}
	return false
}

// userCmd runs a shell command line as the CS2 user.
func (m *InstanceManager) userCmd(cmdline string) *exec.Cmd {
	return userShellCommand(m.L.User, cmdline)
}

func (m *InstanceManager) tmux(args ...string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return m.userCmd("tmux " + strings.Join(quoted, " ")).Run()
}

// IsRunning: the instance's tmux session (its supervisor) exists.
func (m *InstanceManager) IsRunning(n int) bool {
	return m.tmux("has-session", "-t", "="+instanceSession(n)) == nil
}

// own hands paths csm created as root to the CS2 user.
func (m *InstanceManager) own(paths ...string) {
	if !canChown() {
		return
	}
	for _, p := range paths {
		_ = ensureOwnedByUser(m.L.User, p)
	}
}

// mkdirs creates dirs (and parents under the instance root) owned by the user.
func (m *InstanceManager) mkdirs(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		if canChown() {
			// Parents MkdirAll created as root, up to the instance root.
			for p := d; strings.HasPrefix(p, m.L.Root) && len(p) >= len(m.L.Root); p = filepath.Dir(p) {
				m.own(p)
				if p == m.L.Root {
					break
				}
			}
		}
	}
	return nil
}

// Create makes instance n (0 = the lowest free number): its directories and
// state. It needs a Ready Up layer to start, not to be created.
// Create makes instance n (0: the next free one), within a paid license's
// limit. The agent creates through LinkBackend, which checks per platform
// and calls createUngated.
func (m *InstanceManager) Create(w io.Writer, n int) (int, error) {
	if !m.Exists(n) || n == 0 {
		if err := GateCreate(context.Background(), 1); err != nil {
			return 0, err
		}
	}
	return m.createUngated(w, n)
}

func (m *InstanceManager) createUngated(w io.Writer, n int) (int, error) {
	if err := m.requireInstancePrivileges("instance create"); err != nil {
		return 0, err
	}
	if n == 0 {
		n = m.nextFree()
	}
	ports, err := m.Ports(n)
	if err != nil {
		return 0, err
	}
	if m.Exists(n) {
		return n, fmt.Errorf("instance %d already exists", n)
	}
	for _, p := range []string{m.L.Root, m.L.Master, m.L.Upper(n), m.L.Work(n), m.L.Merged(n)} {
		if err := overlayPathOK(p); err != nil {
			return 0, err
		}
	}
	if fi, err := os.Stat(filepath.Join(m.CurrentGame(), "game", "bin", "linuxsteamrt64", "cs2")); err != nil || fi.IsDir() {
		return 0, fmt.Errorf("no CS2 install at %s (install it with the csm install wizard, or set %s)", m.CurrentGame(), EnvInstanceMaster)
	}
	if err := m.mkdirs(m.L.Upper(n), m.L.Work(n), m.L.Merged(n), filepath.Join(m.L.Home(n), ".steam", "sdk64"), m.L.CfgDir(n)); err != nil {
		return 0, err
	}
	st := instanceState{Number: n, CreatedAt: time.Now().Unix()}
	if err := writeJSONAtomic(m.L.StateFile(n), st); err != nil {
		return 0, err
	}
	m.own(m.L.StateFile(n))
	fmt.Fprintf(w, "[✓] instance %d created at %s (game %d, GOTV %d, Ready Up status %d)\n", n, m.L.Dir(n), ports.Game, ports.TV, ports.Status)
	return n, nil
}

// steamClientPath finds the steamclient.so CS2 loads from ~/.steam/sdk64.
func (m *InstanceManager) steamClientPath() string {
	cands := []string{
		os.Getenv(EnvInstanceSteamClient),
		filepath.Join("/home", m.L.User, ".steam", "sdk64", "steamclient.so"),
		filepath.Join("/home", m.L.User, ".local", "share", "Steam", "steamcmd", "linux64", "steamclient.so"),
		filepath.Join("/home", m.L.User, ".steam", "steamcmd", "linux64", "steamclient.so"),
		filepath.Join("/home", m.L.User, "Steam", "linux64", "steamclient.so"),
	}
	for _, c := range cands {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if p, err := filepath.EvalSymlinks(c); err == nil {
			return p
		}
	}
	return ""
}

func (m *InstanceManager) maxPlayers() int {
	if m.S.MaxPlayers > 0 {
		return m.S.MaxPlayers
	}
	if n := detectMaxPlayers(m.L.User); n > 0 {
		return n
	}
	return 10
}

func (m *InstanceManager) launch(n int) (instanceLaunch, error) {
	ports, err := m.Ports(n)
	if err != nil {
		return instanceLaunch{}, err
	}
	return instanceLaunch{
		N: n, L: m.L, Ports: ports, Map: m.S.Map, MaxPlayers: m.maxPlayers(),
		PrivateShm: m.S.PrivateShmOn(), Nice: m.S.Nice, SteamClient: m.steamClientPath(), Insecure: m.S.Insecure,
	}, nil
}

// prepare writes everything a start needs: scripts, instance.cfg, the
// license cfg, the Steam client link, and the state.
func (m *InstanceManager) prepare(n int) (instanceLaunch, error) {
	s, err := m.launch(n)
	if err != nil {
		return s, err
	}
	if err := m.mkdirs(m.L.Upper(n), m.L.Work(n), m.L.Merged(n), filepath.Join(m.L.Home(n), ".steam", "sdk64"), m.L.CfgDir(n)); err != nil {
		return s, err
	}
	link := filepath.Join(m.L.Home(n), ".steam", "sdk64", "steamclient.so")
	if s.SteamClient != "" {
		_ = os.Remove(link)
		if err := os.Symlink(s.SteamClient, link); err != nil {
			return s, err
		}
		m.own(link)
	}
	execSh, err := renderInstanceExecScript(s)
	if err != nil {
		return s, err
	}
	if err := writeFileAtomicMode(m.L.ExecScript(n), []byte(execSh), 0o755); err != nil {
		return s, err
	}
	if err := writeFileAtomicMode(m.L.RunScript(n), []byte(renderInstanceRunScript(s)), 0o755); err != nil {
		return s, err
	}

	// cfg: instance.cfg always, readyup_license.cfg when csm has a key.
	cfgDir := m.L.CfgDir(n)
	haveLicense := false
	if ls, err := LoadLicenseSettings(); err == nil && ls.Key != "" {
		if err := writeFileAtomicMode(filepath.Join(cfgDir, readyUpLicenseCfg), []byte(readyUpLicenseCfgContent(ls.Key)), 0o600); err == nil {
			haveLicense = true
			m.own(filepath.Join(cfgDir, readyUpLicenseCfg))
		}
	} else {
		_ = os.Remove(filepath.Join(cfgDir, readyUpLicenseCfg))
	}
	_, customErr := os.Stat(filepath.Join(cfgDir, instanceCustomCfgName))
	hostname := fmt.Sprintf("%s #%d", detectHostnamePrefix(m.L.User), n)
	cfg := renderInstanceCfg(n, hostname, detectRCONPassword(m.L.User), haveLicense, customErr == nil)
	if err := writeFileAtomicMode(filepath.Join(cfgDir, instanceCfgName), []byte(cfg), 0o640); err != nil {
		return s, err
	}

	st, _ := readInstanceState(m.L.StateFile(n))
	st.Number = n
	if st.CreatedAt == 0 {
		st.CreatedAt = time.Now().Unix()
	}
	st.StartedAt = time.Now().Unix()
	st.MasterBuild = m.layerBuildFor(n)
	if err := writeJSONAtomic(m.L.StateFile(n), st); err != nil {
		return s, err
	}
	m.own(m.L.ExecScript(n), m.L.RunScript(n), filepath.Join(cfgDir, instanceCfgName), m.L.StateFile(n))
	return s, nil
}

// writeFileAtomicMode writes path through a temp file and rename.
func writeFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Start starts instance n in its tmux session.
func (m *InstanceManager) Start(ctx context.Context, n int) error {
	if err := m.requireInstancePrivileges("instance start"); err != nil {
		return err
	}
	if err := GateStart(); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist (csm instance create %d)", n, n)
	}
	if m.IsRunning(n) {
		return fmt.Errorf("instance %d is already running", n)
	}
	if m.execBusy(n) {
		return fmt.Errorf("instance %d is busy: a `csm instance exec` runs in it", n)
	}
	if _, err := m.EffectiveLayer(n); err != nil {
		return err
	}
	s, err := m.prepare(n)
	if err != nil {
		return err
	}
	if s.SteamClient == "" {
		LogWarn("instance: no steamclient.so found; CS2 may not start (set " + EnvInstanceSteamClient + ")")
	}
	if portInUse(s.Ports.Game) {
		return fmt.Errorf("instance %d: game port %d is already in use", n, s.Ports.Game)
	}
	_ = os.Remove(m.L.StopFlag(n))
	log := m.L.ConsoleLog(n)
	if f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		fmt.Fprintf(f, "\n[csm] ==== instance %d start %s ====\n", n, time.Now().UTC().Format(time.RFC3339))
		f.Close()
		m.own(log)
	}
	session := instanceSession(n)
	cmdline := fmt.Sprintf("tmux new-session -d -s %s -x 250 -y 50 %s && tmux pipe-pane -o -t %s %s",
		shellQuote(session), shellQuote(m.L.RunScript(n)), shellQuote("="+session+":"), shellQuote("cat >> "+shellQuote(log)))
	if out, err := m.userCmd(cmdline).CombinedOutput(); err != nil {
		return fmt.Errorf("instance %d: tmux start failed: %v %s", n, err, strings.TrimSpace(string(out)))
	}
	LogAction("instance", fmt.Sprintf("start instance-%d", n), "", nil)
	return nil
}

// Stop asks instance n to quit, waits up to grace, then kills its session
// (the overlay mount goes with its namespace).
func (m *InstanceManager) Stop(n int, grace time.Duration) error {
	if err := m.requireInstancePrivileges("instance stop"); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist", n)
	}
	if err := os.WriteFile(m.L.StopFlag(n), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return err
	}
	m.own(m.L.StopFlag(n))
	if !m.IsRunning(n) {
		return nil
	}
	session := "=" + instanceSession(n)
	_ = m.tmux("send-keys", "-t", session+":", "quit", "Enter")
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && m.IsRunning(n) {
		time.Sleep(500 * time.Millisecond)
	}
	if m.IsRunning(n) {
		_ = m.tmux("kill-session", "-t", session)
	}
	_ = os.Remove(m.L.InUseFile(n))
	LogAction("instance", fmt.Sprintf("stop instance-%d", n), "", nil)
	return nil
}

// Restart stops and starts instance n.
func (m *InstanceManager) Restart(ctx context.Context, n int) error {
	if err := m.Stop(n, 30*time.Second); err != nil {
		return err
	}
	return m.Start(ctx, n)
}

// Remove deletes instance n and everything it wrote. A running instance is
// refused unless force (then it is stopped first).
func (m *InstanceManager) Remove(w io.Writer, n int, force bool) error {
	if err := m.requireInstancePrivileges("instance remove"); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist", n)
	}
	if m.IsRunning(n) {
		if !force {
			return fmt.Errorf("instance %d is running: stop it first (or --force)", n)
		}
		if err := m.Stop(n, 30*time.Second); err != nil {
			return err
		}
	}
	if err := removeTreeForce(m.L.Dir(n)); err != nil {
		return err
	}
	fmt.Fprintf(w, "[✓] instance %d removed (%s)\n", n, m.L.Dir(n))
	LogAction("instance", fmt.Sprintf("remove instance-%d", n), "", nil)
	return nil
}

// removeTreeForce removes dir even where overlayfs left directories without
// permissions (work/work is mode 000).
func removeTreeForce(dir string) error {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		} else if err != nil && d != nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		if out, err2 := exec.Command("rm", "-rf", dir).CombinedOutput(); err2 != nil {
			return fmt.Errorf("remove %s: %v (%s)", dir, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// ShellCommand is an interactive shell inside a fresh namespace with
// instance n's view mounted at its merged directory (for inspection; it is
// not the running server's namespace).
func (m *InstanceManager) ShellCommand(n int) (*exec.Cmd, error) {
	if err := m.requireInstancePrivileges("instance shell"); err != nil {
		return nil, err
	}
	if !m.Exists(n) {
		return nil, fmt.Errorf("instance %d does not exist", n)
	}
	layer, err := m.EffectiveLayer(n)
	if err != nil {
		return nil, err
	}
	opts, err := overlayMountOptions([]string{layer, m.layerGame(layer)}, m.L.Upper(n), m.L.Work(n))
	if err != nil {
		return nil, err
	}
	if m.IsRunning(n) {
		// Two overlay mounts sharing one upper/work is undefined behaviour.
		return nil, fmt.Errorf("instance %d is running; its files are in %s (stop it for a merged shell)", n, m.L.Upper(n))
	}
	inner := fmt.Sprintf("mount -t overlay overlay -o %s %s && cd %s && echo '[csm] instance %d view (layer %s); exit to leave' && exec bash -i",
		shellQuote(opts), shellQuote(m.L.Merged(n)), shellQuote(m.L.Merged(n)), n, filepath.Base(layer))
	cmdline := strings.Join(unshareArgv(), " ") + " bash -c " + shellQuote(inner)
	cmd := m.userCmd(cmdline)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, nil
}

// LayerInUse is the layer a running instance was mounted with ("" = none).
func (m *InstanceManager) LayerInUse(n int) string {
	data, err := os.ReadFile(m.L.InUseFile(n))
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(first)
}

// restartPending says why a running instance should be restarted to pick
// up an update: it runs an older Ready Up layer, or an older CS2 build than
// the current layer sits on.
func restartPending(inUse, current string, startedBuild, masterBuild int64) (bool, string) {
	var why []string
	if inUse != "" && current != "" && filepath.Clean(inUse) != filepath.Clean(current) {
		why = append(why, fmt.Sprintf("Ready Up layer %s -> %s", filepath.Base(inUse), filepath.Base(current)))
	}
	if startedBuild > 0 && masterBuild > 0 && startedBuild != masterBuild {
		why = append(why, fmt.Sprintf("CS2 build %d -> %d", startedBuild, masterBuild))
	}
	return len(why) > 0, strings.Join(why, ", ")
}

// RestartPending is restartPending for running instance n.
func (m *InstanceManager) RestartPending(n int) (bool, string) {
	current, _ := m.EffectiveLayer(n)
	st, _ := readInstanceState(m.L.StateFile(n))
	return restartPending(m.LayerInUse(n), current, st.MasterBuild, m.layerBuildFor(n))
}

// MasterBuild is the ServerVersion of the current CS2 game version (the
// master install until the first update; 0 = unknown).
func (m *InstanceManager) MasterBuild() int64 { return gameBuild(m.CurrentGame()) }

// currentLayerBuild is the CS2 build a start gets now: that of the game
// version the current layer sits on (the current game version without one).
func (m *InstanceManager) currentLayerBuild() int64 {
	if cur, err := m.CurrentLayer(); err == nil {
		return gameBuild(m.layerGame(cur))
	}
	return m.MasterBuild()
}

// FleetTarget is instance n as a fleet row: Dir is its upper directory,
// laid out like a server-N folder, where Ready Up writes status.json.
func (m *InstanceManager) FleetTarget(n int) FleetTarget {
	p, _ := m.Ports(n)
	return FleetTarget{Server: n, Dir: m.L.Upper(n), GamePort: p.Game, Running: m.IsRunning(n)}
}

// FleetTargets lists the serving instances (not pinned, CI ones).
func (m *InstanceManager) FleetTargets() []FleetTarget {
	var out []FleetTarget
	for _, n := range m.Serving() {
		out = append(out, m.FleetTarget(n))
	}
	return out
}

// InstancesExist reports whether this host has any serving instance.
func InstancesExist() bool {
	m, err := NewInstanceManager()
	return err == nil && len(m.Serving()) > 0
}

// InstanceSession is instance n's tmux session name.
func InstanceSession(n int) string { return instanceSession(n) }

// masterReadOnly: CSM_INSTANCE_MASTER_READONLY=1 keeps SteamCMD off the
// master install (csm only reads it).
func masterReadOnly() bool {
	on, err := parseOnOff(os.Getenv(EnvInstanceMasterReadOnly))
	return err == nil && on
}
