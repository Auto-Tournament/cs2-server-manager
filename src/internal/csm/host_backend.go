package csm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// The host agent's view of this machine (hostagent.Backend)
//
// Everything here reuses what csm's CLI already does: TmuxManager for the
// processes, the add-server flow for server.create, UpdateGame /
// UpdateServer for host.update_game, the Ready Up /status probe for the
// fleet table and update_safe, and Ready Up's own install.sh for
// host.update_plugins.

// HostBackend implements hostagent.Backend.
type HostBackend struct {
	client *http.Client
	// launchMu guards CSM_LAUNCH_MODE, which the start path reads from the
	// environment (ApplyLaunchModeFlags).
	launchMu sync.Mutex

	// update_available reads the server logs; it is refreshed at most once
	// a minute, not on every health tick.
	updMu  sync.Mutex
	updAt  time.Time
	updVal bool
}

// NewHostBackend returns the backend for this machine.
func NewHostBackend() *HostBackend {
	return &HostBackend{client: &http.Client{Timeout: 3 * time.Second}}
}

var _ hostagent.Backend = (*HostBackend)(nil)
var _ hostagent.FirstServerCreator = (*HostBackend)(nil)
var _ hostagent.ReadyUpCurrentChecker = (*HostBackend)(nil)

// HostAgentPaths is where the agent keeps its files: <csm root>/fleet/.
func HostAgentPaths() hostagent.Paths { return hostagent.NewPaths(ResolveRoot()) }

func (b *HostBackend) manager() (*TmuxManager, error) {
	return NewTmuxManager()
}

var installIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// Servers lists server-1..N with process and Ready Up state.
func (b *HostBackend) Servers(ctx context.Context) ([]hostagent.ServerState, error) {
	if InstanceBackendOn() {
		return b.instanceServers(ctx)
	}
	mgr, err := b.manager()
	if err != nil {
		return nil, err
	}
	targets := mgr.FleetTargets()
	rows := ProbeFleet(ctx, targets)
	out := make([]hostagent.ServerState, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		wg.Add(1)
		go func(i int, r FleetRow) {
			defer wg.Done()
			out[i] = b.serverState(ctx, mgr, r)
		}(i, r)
	}
	wg.Wait()
	return out, nil
}

func (b *HostBackend) serverState(ctx context.Context, mgr *TmuxManager, r FleetRow) hostagent.ServerState {
	_, tvPort := detectServerPorts(mgr.CS2User, r.Target.Server)
	return b.serverStateTV(ctx, r, tvPort)
}

func (b *HostBackend) serverStateTV(ctx context.Context, r FleetRow, tvPort int) hostagent.ServerState {
	t := r.Target
	s := hostagent.ServerState{
		Number: t.Server, Dir: t.Dir, GamePort: t.GamePort, TVPort: tvPort, StatusPort: r.StatusPort,
		Running: t.Running, Updating: t.Updating, Health: "not_running", LaunchArgs: []string{},
	}
	if s.StatusPort <= 0 || s.StatusPort > 65535 {
		s.StatusPort = t.GamePort + ReadyUpPortOffset
	}
	d, derr := DiscoverReadyUp(t.Dir, t.GamePort)
	s.ReadyUpPresent = derr == nil && d.FromFile
	if data, err := os.ReadFile(filepath.Join(t.Dir, "game", "csgo", "readyup", "plugins", "fleet", "install_id")); err == nil {
		if id := strings.TrimSpace(string(data)); installIDRe.MatchString(id) {
			s.InstallID = id
		}
	}
	if fi, err := os.Stat(filepath.Join(t.Dir, "game", "csgo", "readyup")); err == nil && fi.IsDir() {
		s.ReadyUpInstalled = "unknown"
	}
	if t.Running {
		if s.ReadyUpPresent && d.PID > 0 {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", d.PID)); err == nil {
				s.PID = d.PID
				s.StartedAt = d.StartedAt
				if rss, ok := procRSSMB(d.PID); ok {
					s.RSSMB = &rss
				}
			}
		}
		s.Health = b.readyUpHealth(ctx, d, derr, r)
	}
	if r.State == ReadyUpOK && r.Status != nil {
		st := r.Status
		if v := strings.TrimSpace(string(st.Versions.Core)); v != "" {
			s.ReadyUpInstalled = v
		} else {
			s.ReadyUpInstalled = "unknown"
		}
		s.ServerID = st.ServerID
		// The raw phase ("live"), not PhaseLabel's "live R14": the round
		// would make every round an inventory change.
		s.Phase = st.Summary.Phase
		if s.Phase == "" {
			s.Phase = st.Summary.Mode
		}
		if st.UpdateSafe != nil {
			v := *st.UpdateSafe
			s.UpdateSafe = &v
			if !v {
				s.Busy = describeBusyServer(r)
			}
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(string(st.Versions.CS2Build)), 10, 64); err == nil && n > 0 {
			s.CS2Build = n
		}
	}
	if s.CS2Build == 0 {
		s.CS2Build = steamInfServerVersion(filepath.Join(t.Dir, "game", "csgo", "steam.inf"))
	}
	return s
}

// readyUpHealth asks Ready Up's /health (FLEET.md §17.2): 200 ok, 503
// failing, anything else (timeout, refused) no_response.
func (b *HostBackend) readyUpHealth(ctx context.Context, d ReadyUpDiscovery, derr error, r FleetRow) string {
	if derr != nil || (!d.FromFile && r.State != ReadyUpOK) {
		return "no_response"
	}
	hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := d.newRequest(hctx, "/health")
	if err != nil {
		return "no_response"
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return "no_response"
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "ok"
	case http.StatusServiceUnavailable:
		return "failing"
	}
	return "no_response"
}

func procRSSMB(pid int) (int64, bool) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
			fields := strings.Fields(v)
			if len(fields) > 0 {
				if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					return kb / 1024, true
				}
			}
		}
	}
	return 0, false
}

// steamInfServerVersion reads ServerVersion from a steam.inf (0 = unknown).
func steamInfServerVersion(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "ServerVersion") {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// Host returns the machine facts.
func (b *HostBackend) Host(ctx context.Context) (hostagent.HostFacts, error) {
	f := hostagent.HostFacts{OS: HostOSDescription()}
	f.Hostname, _ = os.Hostname()
	f.Resources.CPUs = runtime.NumCPU()
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fs := strings.Fields(string(data)); len(fs) > 0 {
			f.Resources.Load1, _ = strconv.ParseFloat(fs[0], 64)
		}
	}
	f.Resources.RAMMB, f.Resources.RAMFreeMB = memInfoMB()
	f.Resources.Disk = []hostagent.DiskInfo{}

	user := configuredCS2User()
	mgr, merr := b.manager()
	if InstanceBackendOn() {
		b.instanceHostFacts(&f)
		return f, nil
	}
	if merr == nil {
		user = mgr.CS2User
	}
	seen := map[string]bool{}
	for _, p := range []string{"/", filepath.Join("/home", user), ResolveRoot()} {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			continue
		}
		key := fmt.Sprintf("%d:%d", st.Blocks, st.Fsid)
		if seen[key] {
			continue
		}
		seen[key] = true
		gb := func(blocks uint64) float64 {
			return float64(int64(blocks*uint64(st.Bsize)/(1<<20))) / 1024
		}
		f.Resources.Disk = append(f.Resources.Disk, hostagent.DiskInfo{Mount: p, TotalGB: round1(gb(st.Blocks)), FreeGB: round1(gb(st.Bavail))})
	}

	// The install new servers run: the current game version on an instance
	// host, the master install otherwise.
	inf := filepath.Join("/home", user, "master-install", "game", "csgo", "steam.inf")
	if InstancesExist() {
		if m, err := NewInstanceManager(); err == nil {
			inf = filepath.Join(m.CurrentGame(), "game", "csgo", "steam.inf")
		}
	}
	f.CS2.MasterBuild = steamInfServerVersion(inf)
	if data, err := os.ReadFile(inf); err == nil {
		f.CS2.MasterPatch = parsePatchVersion(string(data))
	}
	if s, err := LoadAutoUpdateSettings(); err == nil {
		f.CS2.UpdatesHold = s.Mode()
	} else {
		f.CS2.UpdatesHold = HoldModeOn
	}
	if merr == nil {
		f.CS2.UpdateAvailable = b.updateAvailable(mgr)
	}
	return f, nil
}

// updateAvailable: a server log shows the CS2 update marker the auto-update
// monitor acts on, after the offset the monitor already handled.
func (b *HostBackend) updateAvailable(mgr *TmuxManager) bool {
	b.updMu.Lock()
	defer b.updMu.Unlock()
	if time.Since(b.updAt) < time.Minute {
		return b.updVal
	}
	b.updAt = time.Now()
	b.updVal = false
	state := loadAutoUpdateState()
	for i := 1; i <= mgr.NumServers; i++ {
		marker, _, err := logHasMarkerAfter(mgr.ServerLogPath(i), state.server(i).LogOffset, autoUpdaterShutdownMarker, matchzyUpdateAvailableMarker)
		if err == nil && marker != "" {
			b.updVal = true
			break
		}
	}
	return b.updVal
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func memInfoMB() (total, free int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fs[1], 10, 64)
		switch fs[0] {
		case "MemTotal:":
			total = kb / 1024
		case "MemAvailable:":
			free = kb / 1024
		}
	}
	return total, free
}

// HostOSDescription is "linux <PRETTY_NAME>".
func HostOSDescription() string {
	name := ""
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				name = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	if name == "" {
		return runtime.GOOS
	}
	return runtime.GOOS + " " + name
}

func (b *HostBackend) checkServer(mgr *TmuxManager, server int) error {
	if server <= 0 || server > mgr.NumServers {
		return fmt.Errorf("server-%d does not exist (%d installed)", server, mgr.NumServers)
	}
	return nil
}

// Start starts one server. launchMode "" keeps csm's default launcher.
func (b *HostBackend) Start(ctx context.Context, server int, launchMode string) error {
	if InstanceBackendOn() {
		return b.instanceStart(ctx, server)
	}
	mgr, err := b.manager()
	if err != nil {
		return err
	}
	if err := b.checkServer(mgr, server); err != nil {
		return err
	}
	b.launchMu.Lock()
	defer b.launchMu.Unlock()
	if launchMode != "" {
		prev, had := os.LookupEnv("CSM_LAUNCH_MODE")
		if err := ApplyLaunchModeFlags(launchMode == "alternate", launchMode == "binary"); err != nil {
			return err
		}
		defer func() {
			if had {
				_ = os.Setenv("CSM_LAUNCH_MODE", prev)
			} else {
				_ = os.Unsetenv("CSM_LAUNCH_MODE")
			}
		}()
	}
	err = mgr.Start(server)
	LogAction("agent", fmt.Sprintf("start server-%d", server), "", err)
	return err
}

// Stop sends `quit` to the server console, waits up to graceS for it to
// exit, then kills the tmux session (as `csm stop` does).
func (b *HostBackend) Stop(ctx context.Context, server int, graceS int) error {
	if InstanceBackendOn() {
		return b.instanceStop(server, graceS)
	}
	mgr, err := b.manager()
	if err != nil {
		return err
	}
	if err := b.checkServer(mgr, server); err != nil {
		return err
	}
	if graceS > 0 && mgr.IsRunning(server) {
		_ = mgr.tmux("send-keys", "-t", mgr.sessionName(server), "quit", "Enter")
		deadline := time.Now().Add(time.Duration(graceS) * time.Second)
		for time.Now().Before(deadline) && mgr.IsRunning(server) {
			select {
			case <-ctx.Done():
				deadline = time.Now()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	err = mgr.Stop(server)
	LogAction("agent", fmt.Sprintf("stop server-%d", server), "", err)
	return err
}

// tmux runs a tmux command against the CS2 user's tmux server.
func (m *TmuxManager) tmux(args ...string) error {
	if os.Geteuid() == 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = shellQuote(a)
		}
		return m.runAsCS2User("tmux " + strings.Join(quoted, " ")).Run()
	}
	return exec.Command("tmux", args...).Run()
}

// Restart restarts one server.
func (b *HostBackend) Restart(ctx context.Context, server int) error {
	if InstanceBackendOn() {
		return b.instanceRestart(ctx, server)
	}
	mgr, err := b.manager()
	if err != nil {
		return err
	}
	if err := b.checkServer(mgr, server); err != nil {
		return err
	}
	err = mgr.Restart(server)
	LogAction("agent", fmt.Sprintf("restart server-%d", server), "", err)
	return err
}

// CreateServer runs the add-server flow with a pre-start hook.
func (b *HostBackend) CreateServer(ctx context.Context, beforeStart func(serverDir string) error) (int, string, error) {
	if InstanceBackendOn() {
		return b.instanceCreate(ctx, beforeStart)
	}
	n, out, err := AddServerInstanceForAgent(ctx, beforeStart)
	LogAction("agent", "add server", out, err)
	if err == nil {
		if fi, serr := os.Stat(filepath.Join("/home", configuredOrDetectedUser(), fmt.Sprintf("server-%d", n), "game", "csgo", "readyup")); serr != nil || !fi.IsDir() {
			out += fmt.Sprintf("\nReady Up is not installed on server-%d yet (new servers are copies of the master install): send host.update_plugins for it.\n", n)
		}
	}
	return n, out, err
}

func configuredOrDetectedUser() string {
	if mgr, err := NewTmuxManager(); err == nil {
		return mgr.CS2User
	}
	return configuredCS2User()
}

// RemoveLastServer removes the highest-numbered server.
func (b *HostBackend) RemoveLastServer(ctx context.Context) (string, error) {
	if InstanceBackendOn() {
		return b.instanceRemoveLast()
	}
	out, err := RemoveLastServerInstance()
	LogAction("agent", "remove last server", out, err)
	return out, err
}

// UpdateGame updates every server (servers nil) or the listed ones.
func (b *HostBackend) UpdateGame(ctx context.Context, servers []int, progress func(step string)) (string, error) {
	if InstanceBackendOn() {
		return b.instanceUpdateGame(ctx, progress)
	}
	if len(servers) == 0 {
		progress("SteamCMD update of the master install, then sync and restart of every server")
		out, err := UpdateGameWithContext(ctx)
		LogAction("agent", "update-game", out, err)
		return out, err
	}
	var all strings.Builder
	sorted := append([]int(nil), servers...)
	sort.Ints(sorted)
	for _, n := range sorted {
		progress(fmt.Sprintf("updating server-%d", n))
		out, err := UpdateServerWithContext(ctx, n)
		LogAction("agent", fmt.Sprintf("update-server-%d", n), out, err)
		all.WriteString(out)
		if err != nil {
			return all.String(), fmt.Errorf("server-%d: %w", n, err)
		}
	}
	return all.String(), nil
}

// InstallReadyUp runs Ready Up's install.sh for one server as the CS2 user.
func (b *HostBackend) InstallReadyUp(ctx context.Context, server int, plan hostagent.ReadyUpPlan) (string, error) {
	if InstanceBackendOn() {
		return b.instanceInstallReadyUp(ctx, server, plan)
	}
	mgr, err := b.manager()
	if err != nil {
		return "", err
	}
	if err := b.checkServer(mgr, server); err != nil {
		return "", err
	}
	dir := mgr.serverDir(server)
	cmdline := readyup.InstallArgs{
		Installer: plan.Installer, Bundle: plan.Component, Dir: dir,
		Zip: plan.Zip, Version: plan.Version, AcceptLicense: plan.AcceptLicense,
	}.Cmdline()
	var buf bytes.Buffer
	err = runReadyUpInstaller(ctx, mgr.CS2User, cmdline, &buf)
	if err == nil {
		applyStoredLicenseToServer(&buf, mgr.CS2User, server)
		adoptReadyUpStack(&buf, "platform")
	}
	out := buf.String()
	LogAction("agent", fmt.Sprintf("install Ready Up on server-%d", server), out, err)
	return out, err
}

// SetUpdatesHold sets csm's update hold.
func (b *HostBackend) SetUpdatesHold(mode string) error {
	m, err := ParseHoldMode(mode)
	if err != nil {
		return err
	}
	_, err = SetUpdateHoldMode(m)
	LogAction("agent", "updates hold "+m, "", err)
	return err
}

// LogFile maps a logs.tail source to a file.
func (b *HostBackend) LogFile(server int, source string) (string, error) {
	switch source {
	case "csm", "monitor":
		// The monitor writes into csm's consolidated log (writeMonitorLog).
		return filepath.Join(logDir(), "csm.log"), nil
	}
	if InstanceBackendOn() {
		return b.instanceLogFile(server, source)
	}
	mgr, err := b.manager()
	if err != nil {
		return "", err
	}
	if err := b.checkServer(mgr, server); err != nil {
		return "", err
	}
	switch source {
	case "console":
		return mgr.ServerLogPath(server), nil
	case "readyup":
		// Ready Up logs through the engine: its lines are in CS2's own log
		// files (game/csgo/logs, with `log on`). The newest one is used.
		dir := filepath.Join(mgr.serverDir(server), "game", "csgo", "logs")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("server-%d has no game/csgo/logs (CS2 logging is off)", server)
		}
		var newest string
		var newestT time.Time
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().After(newestT) {
				newest, newestT = e.Name(), fi.ModTime()
			}
		}
		if newest == "" {
			return "", fmt.Errorf("server-%d has no CS2 log files yet", server)
		}
		return filepath.Join(dir, newest), nil
	}
	return "", fmt.Errorf("unknown log source %q", source)
}

// ChownToCS2User hands a file to the CS2 user when csm runs as root.
func (b *HostBackend) ChownToCS2User(path string) error {
	return ensureOwnedByUser(configuredOrDetectedUser(), path)
}
