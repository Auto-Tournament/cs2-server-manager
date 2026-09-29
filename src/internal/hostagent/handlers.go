package hostagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// handle runs one validated command and sends its one host.result.
func (a *Agent) handle(ctx context.Context, env *Envelope, cmd any) {
	ref := env.ID
	defer func() {
		if r := recover(); r != nil {
			a.logf("host agent: %s panicked: %v", env.Type, r)
			a.sendResult(ref, failed(CodeFailed, fmt.Sprintf("internal error in %s", env.Type), ""))
		}
	}()
	var res ResultPayload
	switch c := cmd.(type) {
	case *ServersListCmd:
		res = a.handleList(ctx, ref)
	case *ServerStartCmd:
		res = a.handleStart(ctx, c)
	case *ServerStopCmd:
		res = a.handleStop(ctx, c)
	case *ServerRestartCmd:
		res = a.handleRestart(ctx, c)
	case *ServerCreateCmd:
		res = a.handleCreate(ctx, ref, c)
	case *ServerRemoveCmd:
		res = a.handleRemove(ctx, ref, c)
	case *ServerSetLaunchArgsCmd:
		res = rejected(CodeUnsupported, "csm builds each server's launch line itself (csm start --alternate/--binary); per-server launch args are not supported yet")
	case *UpdateGameCmd:
		res = a.handleUpdateGame(ctx, ref, c)
	case *UpdatePluginsCmd:
		res = a.handleUpdatePlugins(ctx, ref, c)
	case *LogsTailCmd:
		// logs.tail sends its own result before the first chunk.
		a.handleLogsTail(ref, c)
		return
	case *LogsStopCmd:
		res = a.handleLogsStop(c)
	case *UpdatesHoldCmd:
		if err := a.opts.Backend.SetUpdatesHold(c.Mode); err != nil {
			res = failed(CodeFailed, err.Error(), "")
		} else {
			res = okResult("updates hold: " + c.Mode)
			a.markInventoryDirty()
		}
	default:
		res = rejected(CodeUnsupported, "not handled")
	}
	a.sendResult(ref, res)
}

// --- locking ---------------------------------------------------------------------

// quick takes a shared hold on the machine and a per-server slot. It fails
// (busy) instead of waiting when a heavy job or another command on the same
// server is running.
func (a *Agent) quick(server int, what string) (func(), *ResultPayload) {
	if !a.heavy.TryRLock() {
		a.heavyMu.Lock()
		name := a.heavyName
		a.heavyMu.Unlock()
		r := rejected(CodeBusy, fmt.Sprintf("%s is running on this host; try again when it is done", name))
		return nil, &r
	}
	a.serverMu.Lock()
	if op, ok := a.serverOp[server]; ok {
		a.serverMu.Unlock()
		a.heavy.RUnlock()
		r := rejected(CodeBusy, fmt.Sprintf("%s is already running on %s", op, ServerName(server)))
		return nil, &r
	}
	a.serverOp[server] = what
	a.serverMu.Unlock()
	a.health.expect(server, true)
	return func() {
		a.health.expect(server, false)
		a.serverMu.Lock()
		delete(a.serverOp, server)
		a.serverMu.Unlock()
		a.heavy.RUnlock()
	}, nil
}

// exclusive takes the machine for a heavy job. It waits for running quick
// commands but not for another heavy job (busy).
func (a *Agent) exclusive(what string) (func(), *ResultPayload) {
	a.heavyMu.Lock()
	if a.heavyName != "" {
		name := a.heavyName
		a.heavyMu.Unlock()
		r := rejected(CodeBusy, fmt.Sprintf("%s is already running on this host", name))
		return nil, &r
	}
	a.heavyName = what
	a.heavyMu.Unlock()
	a.heavy.Lock()
	a.health.expectAll(true)
	return func() {
		a.health.expectAll(false)
		a.heavy.Unlock()
		a.heavyMu.Lock()
		a.heavyName = ""
		a.heavyMu.Unlock()
		a.markInventoryDirty()
	}, nil
}

// --- gating ------------------------------------------------------------------------

// gate loads the servers and refuses a disruptive action on any target whose
// Ready Up says update_safe=false, unless force is present (FLEET.md §18.2).
// targets nil = every server. It returns the targets' states.
func (a *Agent) gate(ctx context.Context, action string, targets []int, force *Force) ([]ServerState, *ResultPayload) {
	all, err := a.opts.Backend.Servers(ctx)
	if err != nil {
		r := failed(CodeFailed, "could not list servers: "+err.Error(), "")
		return nil, &r
	}
	byNum := map[int]ServerState{}
	for _, s := range all {
		byNum[s.Number] = s
	}
	var sel []ServerState
	if targets == nil {
		sel = all
	} else {
		for _, n := range targets {
			s, ok := byNum[n]
			if !ok {
				r := rejected(CodeUnknownServer, fmt.Sprintf("%s does not exist on this host", ServerName(n)))
				return nil, &r
			}
			sel = append(sel, s)
		}
	}
	var busy []string
	for _, s := range sel {
		if s.UpdateSafe != nil && !*s.UpdateSafe {
			d := s.Busy
			if d == "" {
				d = ServerName(s.Number)
			}
			busy = append(busy, d)
		}
	}
	if len(busy) == 0 {
		return sel, nil
	}
	if force == nil {
		r := rejected(CodeMatchInProgress, fmt.Sprintf("%s refused: Ready Up reports a match in progress (update_safe=false) on %s. Send force to go ahead anyway.", action, strings.Join(busy, "; ")))
		return nil, &r
	}
	a.logf("host agent: FORCED %s during a match on %s (by %s: %s)", action, strings.Join(busy, "; "), force.By, force.Reason)
	return sel, nil
}

// --- handlers -----------------------------------------------------------------------

func (a *Agent) handleList(ctx context.Context, ref string) ResultPayload {
	if err := a.sendInventory(ctx, ref, true); err != nil {
		return failed(CodeFailed, err.Error(), "")
	}
	return okResult("")
}

func (a *Agent) serverExists(ctx context.Context, n int) (*ServerState, *ResultPayload) {
	all, err := a.opts.Backend.Servers(ctx)
	if err != nil {
		r := failed(CodeFailed, "could not list servers: "+err.Error(), "")
		return nil, &r
	}
	for i := range all {
		if all[i].Number == n {
			return &all[i], nil
		}
	}
	r := rejected(CodeUnknownServer, fmt.Sprintf("%s does not exist on this host", ServerName(n)))
	return nil, &r
}

func (a *Agent) handleStart(ctx context.Context, c *ServerStartCmd) ResultPayload {
	n, _ := ParseServerName(c.Server)
	release, busy := a.quick(n, TypeServerStart)
	if busy != nil {
		return *busy
	}
	defer release()
	st, bad := a.serverExists(ctx, n)
	if bad != nil {
		return *bad
	}
	if st.Running {
		return okResult(c.Server + " is already running")
	}
	mode := c.LaunchMode
	if mode == "default" {
		mode = ""
	}
	if err := a.opts.Backend.Start(ctx, n, mode); err != nil {
		return failed(CodeFailed, "start "+c.Server+": "+err.Error(), "")
	}
	a.health.started(n)
	a.markInventoryDirty()
	return okResult(c.Server + " started")
}

func (a *Agent) handleStop(ctx context.Context, c *ServerStopCmd) ResultPayload {
	n, _ := ParseServerName(c.Server)
	release, busy := a.quick(n, TypeServerStop)
	if busy != nil {
		return *busy
	}
	defer release()
	sel, bad := a.gate(ctx, "server.stop", []int{n}, c.Force)
	if bad != nil {
		return *bad
	}
	if !sel[0].Running {
		return okResult(c.Server + " is not running")
	}
	grace := 10
	if c.GraceS != nil {
		grace = *c.GraceS
	}
	if err := a.opts.Backend.Stop(ctx, n, grace); err != nil {
		return failed(CodeFailed, "stop "+c.Server+": "+err.Error(), "")
	}
	a.markInventoryDirty()
	return okResult(c.Server + " stopped")
}

func (a *Agent) handleRestart(ctx context.Context, c *ServerRestartCmd) ResultPayload {
	n, _ := ParseServerName(c.Server)
	release, busy := a.quick(n, TypeServerRestart)
	if busy != nil {
		return *busy
	}
	defer release()
	if _, bad := a.gate(ctx, "server.restart", []int{n}, c.Force); bad != nil {
		return *bad
	}
	if c.Reason != "" {
		a.logf("host agent: restarting %s: %s", c.Server, c.Reason)
	}
	if err := a.opts.Backend.Restart(ctx, n); err != nil {
		return failed(CodeFailed, "restart "+c.Server+": "+err.Error(), "")
	}
	a.health.started(n)
	a.sendHealth(HealthPayload{Server: c.Server, Event: "restarted", Detail: truncate(c.Reason, 256)})
	a.markInventoryDirty()
	return okResult(c.Server + " restarted")
}

func (a *Agent) handleCreate(ctx context.Context, ref string, c *ServerCreateCmd) ResultPayload {
	count := 1
	if c.Count != nil {
		count = *c.Count
	}
	creds := a.currentCreds()
	key := c.EnrollKey
	if key == "" && creds != nil {
		key = creds.FleetKey
	}
	if *c.Enroll && key == "" {
		return rejected(CodeNoEnrollKey, "enroll needs a fleet enrollment key: this host was linked with a one-time code, so send enroll_key (rfk_…) with server.create")
	}
	if c.NamePrefix != "" && c.NamePrefix != "server-" {
		return rejected(CodeUnsupported, "csm names servers server-N; name_prefix is not supported")
	}
	release, busy := a.exclusive(TypeServerCreate)
	if busy != nil {
		return *busy
	}
	defer release()

	all, err := a.opts.Backend.Servers(ctx)
	if err != nil {
		return failed(CodeFailed, "could not list servers: "+err.Error(), "")
	}
	next := 0
	if len(all) == 0 {
		// An instance-mode host can start from zero servers (instances run
		// from the master install); a server-N host needs the wizard first.
		first, ok := 0, false
		if fc, isFC := a.opts.Backend.(FirstServerCreator); isFC {
			first, ok = fc.FirstServerGamePort()
		}
		if !ok {
			return rejected(CodeUnsupported, "no servers on this host yet: run the csm install wizard once, then servers can be created from the platform")
		}
		next = first
	} else {
		next = all[len(all)-1].GamePort + 10
	}
	if c.GamePort != nil {
		if *c.GamePort != next || count != 1 {
			return rejected(CodeUnsupported, fmt.Sprintf("csm spaces servers 10 ports apart: the next server gets game port %d", next))
		}
	}

	var out strings.Builder
	var names []string
	for i := 0; i < count; i++ {
		a.progress(ref, fmt.Sprintf("creating server %d of %d", i+1, count), i*100/count)
		wroteCfg := false
		n, log, err := a.opts.Backend.CreateServer(ctx, func(dir string) error {
			if !*c.Enroll {
				return nil
			}
			if err := WriteFleetCfg(dir, creds.PlatformURL, key, creds.InsecureDev, creds.CAFile, a.opts.Backend.ChownToCS2User); err != nil {
				return fmt.Errorf("writing fleet.cfg: %w", err)
			}
			wroteCfg = true
			return nil
		})
		out.WriteString(log)
		if err != nil {
			msg := fmt.Sprintf("creating server %d of %d failed: %v", i+1, count, err)
			if len(names) > 0 {
				msg += fmt.Sprintf(" (created before the failure: %s)", strings.Join(names, ", "))
			}
			return failed(CodeFailed, msg, out.String())
		}
		names = append(names, ServerName(n))
		if wroteCfg {
			fmt.Fprintf(&out, "\n%s: wrote cfg/ReadyUp/fleet.cfg (url + fleet key); Ready Up enrolls itself on start.\n", ServerName(n))
		}
	}
	a.progress(ref, "done", 100)
	return okResult("created " + strings.Join(names, ", ") + "\n" + out.String())
}

func (a *Agent) handleRemove(ctx context.Context, ref string, c *ServerRemoveCmd) ResultPayload {
	n, _ := ParseServerName(c.Server)
	if c.KeepFiles {
		return rejected(CodeUnsupported, "csm removes a server with its files; keep_files is not supported")
	}
	release, busy := a.exclusive(TypeServerRemove)
	if busy != nil {
		return *busy
	}
	defer release()
	all, err := a.opts.Backend.Servers(ctx)
	if err != nil {
		return failed(CodeFailed, "could not list servers: "+err.Error(), "")
	}
	if len(all) == 0 || all[len(all)-1].Number != n {
		return rejected(CodeUnsupported, "csm can only remove the highest-numbered server (server-N are kept contiguous)")
	}
	if _, bad := a.gate(ctx, "server.remove", []int{n}, c.Force); bad != nil {
		return *bad
	}
	a.progress(ref, "removing "+c.Server, -1)
	log, err := a.opts.Backend.RemoveLastServer(ctx)
	if err != nil {
		return failed(CodeFailed, "remove "+c.Server+": "+err.Error(), log)
	}
	a.health.forget(n)
	return okResult(c.Server + " removed\n" + log)
}

func (a *Agent) handleUpdateGame(ctx context.Context, ref string, c *UpdateGameCmd) ResultPayload {
	targets, _ := parseServerList(c.Servers)
	if len(c.Servers) == 0 {
		targets = nil
	}
	release, busy := a.exclusive(TypeUpdateGame)
	if busy != nil {
		return *busy
	}
	defer release()
	sel, bad := a.gate(ctx, "host.update_game", targets, c.Force)
	if bad != nil {
		return *bad
	}
	nums := make([]int, 0, len(sel))
	for _, s := range sel {
		nums = append(nums, s.Number)
	}
	if targets == nil {
		nums = nil
	}
	a.progress(ref, "SteamCMD update", 0)
	steps := 0
	log, err := a.opts.Backend.UpdateGame(ctx, nums, func(step string) {
		steps++
		a.progress(ref, step, -1)
	})
	if err != nil {
		return failed(CodeFailed, "update-game: "+err.Error(), log)
	}
	a.progress(ref, "done", 100)
	return okResult(log)
}

func (a *Agent) handleUpdatePlugins(ctx context.Context, ref string, c *UpdatePluginsCmd) ResultPayload {
	targets, _ := parseServerList(c.Servers)
	if len(c.Servers) == 0 {
		targets = nil
	}
	release, busy := a.exclusive(TypeUpdatePlugins)
	if busy != nil {
		return *busy
	}
	defer release()
	sel, bad := a.gate(ctx, "host.update_plugins", targets, c.Force)
	if bad != nil {
		return *bad
	}
	if len(sel) == 0 {
		return rejected(CodeUnknownServer, "no servers on this host")
	}
	cfg, err := LoadConfig(a.opts.Paths)
	if err != nil {
		return failed(CodeFailed, err.Error(), "")
	}
	a.progress(ref, "resolving Ready Up "+c.ReadyUp.Version, 0)
	plan, cleanup, err := a.opts.Fetcher.Prepare(ctx, cfg, c.ReadyUp.Version, c.ReadyUp.Bundle)
	defer cleanup()
	if err != nil {
		var pe *PlanError
		if errors.As(err, &pe) {
			return failed(pe.Code, pe.Msg, "")
		}
		return failed(CodeFailed, err.Error(), "")
	}

	var out strings.Builder
	var done []string
	for i, s := range sel {
		name := ServerName(s.Number)
		a.progress(ref, "installing Ready Up on "+name, i*100/len(sel))
		wasRunning := s.Running
		if wasRunning {
			if err := a.opts.Backend.Stop(ctx, s.Number, 10); err != nil {
				return failed(CodeFailed, fmt.Sprintf("stopping %s before the install: %v%s", name, err, doneSuffix(done)), out.String())
			}
		}
		log, ierr := a.opts.Backend.InstallReadyUp(ctx, s.Number, plan)
		fmt.Fprintf(&out, "== %s ==\n%s\n", name, strings.TrimSpace(log))
		var serr error
		if wasRunning {
			serr = a.opts.Backend.Start(ctx, s.Number, "")
			if serr == nil {
				a.health.started(s.Number)
			}
		}
		if ierr != nil {
			code := CodeInstallFailed
			if strings.Contains(strings.ToLower(log), "accept-license") || strings.Contains(strings.ToLower(log), "license") && strings.Contains(strings.ToLower(log), "accept") {
				code = CodeLicense
				ierr = fmt.Errorf("%v (unattended installs need a license choice: csm agent config readyup_accept_license noncommercial|commercial)", ierr)
			}
			return failed(code, fmt.Sprintf("Ready Up install on %s failed: %v%s", name, ierr, doneSuffix(done)), out.String())
		}
		if serr != nil {
			return failed(CodeFailed, fmt.Sprintf("Ready Up installed on %s but the server did not start again: %v%s", name, serr, doneSuffix(done)), out.String())
		}
		done = append(done, name)
	}
	a.progress(ref, "done", 100)
	src := plan.Version
	if src == "" {
		src = "the configured bundle"
	}
	return okResult(fmt.Sprintf("Ready Up %s (%s) installed on %s\n%s", src, plan.Component, strings.Join(done, ", "), out.String()))
}

func doneSuffix(done []string) string {
	if len(done) == 0 {
		return ""
	}
	return " (already done: " + strings.Join(done, ", ") + ")"
}

// --- inventory ----------------------------------------------------------------------

// Inventory is host.inventory.
type Inventory struct {
	HostID     string      `json:"host_id"`
	Hostname   string      `json:"hostname"`
	CSMVersion string      `json:"csm_version"`
	OS         string      `json:"os"`
	Resources  Resources   `json:"resources"`
	CS2        CS2Facts    `json:"cs2"`
	Servers    []InvServer `json:"servers"`
}

// InvServer is one host.inventory.servers[] entry.
type InvServer struct {
	Name       string     `json:"name"`
	Dir        string     `json:"dir"`
	GamePort   int        `json:"game_port"`
	TVPort     int        `json:"tv_port,omitempty"`
	StatusPort int        `json:"status_port"`
	Process    InvProcess `json:"process"`
	ReadyUp    InvReadyUp `json:"readyup"`
	CS2Build   int64      `json:"cs2_build"`
	LaunchArgs []string   `json:"launch_args"`
}

// InvProcess is servers[].process.
type InvProcess struct {
	Running    bool     `json:"running"`
	PID        int      `json:"pid,omitempty"`
	StartedAt  int64    `json:"started_at,omitempty"`
	Restarts24 int      `json:"restarts_24h"`
	CPUPct     *float64 `json:"cpu_pct,omitempty"`
	RSSMB      *int64   `json:"rss_mb,omitempty"`
}

// InvReadyUp is servers[].readyup.
type InvReadyUp struct {
	Installed  *string `json:"installed"`
	InstallID  string  `json:"install_id,omitempty"`
	ServerID   string  `json:"server_id,omitempty"`
	Health     string  `json:"health"`
	Phase      string  `json:"phase,omitempty"`
	UpdateSafe *bool   `json:"update_safe,omitempty"`
}

// BuildInventory assembles host.inventory from the backend's view.
func BuildInventory(hostID, csmVersion string, facts HostFacts, servers []ServerState, restarts func(int) int) Inventory {
	inv := Inventory{
		HostID: hostID, Hostname: truncate(facts.Hostname, 255), CSMVersion: csmVersion, OS: truncate(facts.OS, 128),
		Resources: facts.Resources, CS2: facts.CS2, Servers: make([]InvServer, 0, len(servers)),
	}
	if inv.Resources.Disk == nil {
		inv.Resources.Disk = []DiskInfo{}
	}
	if inv.CS2.UpdatesHold == "" {
		inv.CS2.UpdatesHold = "auto"
	}
	sorted := append([]ServerState(nil), servers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Number < sorted[j].Number })
	for _, s := range sorted {
		health := s.Health
		if !s.Running {
			health = "not_running"
		} else if health == "" || health == "not_running" {
			health = "no_response"
		}
		e := InvServer{
			Name: ServerName(s.Number), Dir: s.Dir, GamePort: s.GamePort, TVPort: s.TVPort, StatusPort: s.StatusPort,
			Process:  InvProcess{Running: s.Running, CPUPct: s.CPUPct, RSSMB: s.RSSMB},
			ReadyUp:  InvReadyUp{InstallID: s.InstallID, ServerID: s.ServerID, Health: health, Phase: s.Phase, UpdateSafe: s.UpdateSafe},
			CS2Build: s.CS2Build, LaunchArgs: s.LaunchArgs,
		}
		if s.Running {
			e.Process.PID = s.PID
			e.Process.StartedAt = s.StartedAt
		}
		if restarts != nil {
			e.Process.Restarts24 = restarts(s.Number)
		}
		if s.ReadyUpInstalled != "" {
			v := truncate(s.ReadyUpInstalled, 64)
			e.ReadyUp.Installed = &v
		}
		if e.LaunchArgs == nil {
			e.LaunchArgs = []string{}
		}
		inv.Servers = append(inv.Servers, e)
	}
	return inv
}

// inventoryKey is the part of the inventory that counts as a change (not
// load, free RAM, CPU or RSS, which move all the time).
func inventoryKey(inv Inventory) string {
	c := inv
	c.Resources = Resources{}
	c.Servers = make([]InvServer, len(inv.Servers))
	for i, s := range inv.Servers {
		s.Process.CPUPct = nil
		s.Process.RSSMB = nil
		c.Servers[i] = s
	}
	return string(mustJSON(c))
}

func (a *Agent) markInventoryDirty() {
	a.invMu.Lock()
	a.invDirty = true
	a.invMu.Unlock()
}

// buildInventory asks the backend and builds the message.
func (a *Agent) buildInventory(ctx context.Context) (Inventory, []ServerState, error) {
	creds := a.currentCreds()
	hostID := ""
	if creds != nil {
		hostID = creds.HostID
	}
	servers, err := a.opts.Backend.Servers(ctx)
	if err != nil {
		return Inventory{}, nil, err
	}
	facts, err := a.opts.Backend.Host(ctx)
	if err != nil {
		return Inventory{}, nil, err
	}
	if facts.Hostname == "" {
		facts.Hostname = a.opts.Hostname
	}
	if facts.OS == "" {
		facts.OS = a.opts.OS
	}
	return BuildInventory(hostID, a.opts.CSMVersion, facts, servers, a.health.restarts24h), servers, nil
}

// sendInventory sends host.inventory now (ref = the host.servers.list it
// answers, if any).
func (a *Agent) sendInventory(ctx context.Context, ref string, force bool) error {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	inv, _, err := a.buildInventory(cctx)
	if err != nil {
		a.logf("host agent: inventory: %v", err)
		return err
	}
	a.publishInventory(inv, ref, force)
	return nil
}

func (a *Agent) publishInventory(inv Inventory, ref string, force bool) {
	key := inventoryKey(inv)
	a.invMu.Lock()
	changed := key != a.invLast || a.invDirty
	due := time.Since(a.invAt) >= a.opts.InventoryEvery
	if !force && !due && (!changed || time.Since(a.invAt) < 5*time.Second) {
		a.invMu.Unlock()
		return
	}
	a.invMu.Unlock()
	if a.sendEphemeral(TypeInventory, inv, ref) {
		a.invMu.Lock()
		a.invLast = key
		a.invAt = time.Now()
		a.invDirty = false
		a.invMu.Unlock()
	}
}

func (a *Agent) sendHealth(h HealthPayload) {
	h.Detail = truncate(Redact(h.Detail), 512)
	a.logf("host agent: %s %s %s", h.Server, h.Event, h.Detail)
	a.sendReliable(TypeHealth, h, "")
}
