package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// The host agent on an instance-mode host (instances.json backend=instances)
//
// Instances are the agent's servers: instance N is server-N to the platform.
// Its Dir is the instance's upper directory, laid out like a server folder,
// so the agent writes fleet.cfg there (the instance's own copy, over the
// layer's template) and reads Ready Up's status.json and install_id from it.
// Ready Up updates go to the shared layer; server restarts pick it up.

func (b *HostBackend) instances() (*InstanceManager, error) { return NewInstanceManager() }

func (b *HostBackend) checkInstance(m *InstanceManager, n int) error {
	if !m.Exists(n) {
		return fmt.Errorf("server-%d does not exist (no instance %d)", n, n)
	}
	if m.Pinned(n) {
		return fmt.Errorf("instance %d runs a private Ready Up layer (CI); it is not one of this host's servers", n)
	}
	return nil
}

func (b *HostBackend) instanceServers(ctx context.Context) ([]hostagent.ServerState, error) {
	m, err := b.instances()
	if err != nil {
		return nil, err
	}
	rows := ProbeFleet(ctx, m.FleetTargets())
	out := make([]hostagent.ServerState, len(rows))
	cur, _ := m.CurrentLayer()
	core := layerCore(cur)
	master := m.MasterBuild()
	var wg sync.WaitGroup
	for i, r := range rows {
		wg.Add(1)
		go func(i int, r FleetRow) {
			defer wg.Done()
			p, _ := m.Ports(r.Target.Server)
			s := b.serverStateTV(ctx, r, p.TV)
			if s.ReadyUpInstalled == "" || s.ReadyUpInstalled == "unknown" {
				// Ready Up lives in the shared layer, not in the upper dir.
				s.ReadyUpInstalled = core
			}
			if s.CS2Build == 0 {
				s.CS2Build = master
			}
			s.LaunchArgs = instanceCS2Args(instanceLaunch{Ports: p, Map: m.S.Map, MaxPlayers: m.maxPlayers(), Insecure: m.S.Insecure})
			out[i] = s
		}(i, r)
	}
	wg.Wait()
	return out, nil
}

// FirstServerGamePort lets the agent create the first server on an empty
// instance host (a server-N host needs the install wizard first).
func (b *HostBackend) FirstServerGamePort() (int, bool) {
	if !InstanceBackendOn() {
		return 0, false
	}
	m, err := b.instances()
	if err != nil {
		return 0, false
	}
	if _, err := os.Stat(filepath.Join(m.L.Master, "game", "csgo")); err != nil {
		return 0, false
	}
	p, err := m.Ports(1)
	if err != nil {
		return 0, false
	}
	return p.Game, true
}

func (b *HostBackend) instanceHostFacts(f *hostagent.HostFacts) {
	m, err := b.instances()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range []string{"/", m.L.Root, m.L.Master, ResolveRoot()} {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			continue
		}
		key := fmt.Sprintf("%d:%d", st.Blocks, st.Fsid)
		if seen[key] {
			continue
		}
		seen[key] = true
		gb := func(blocks uint64) float64 { return float64(int64(blocks*uint64(st.Bsize)/(1<<20))) / 1024 }
		f.Resources.Disk = append(f.Resources.Disk, hostagent.DiskInfo{Mount: p, TotalGB: round1(gb(st.Blocks)), FreeGB: round1(gb(st.Bavail))})
	}
	f.CS2.MasterBuild = m.MasterBuild()
	if s, err := LoadAutoUpdateSettings(); err == nil {
		f.CS2.UpdatesHold = s.Mode()
	} else {
		f.CS2.UpdatesHold = HoldModeOn
	}
	state := loadAutoUpdateState()
	for _, n := range m.List() {
		var off int64
		if st := state.Instances[fmt.Sprint(n)]; st != nil {
			off = st.LogOffset
		}
		if marker, _, err := logHasMarkerAfter(m.L.ConsoleLog(n), off, instanceUpdateMarkers...); err == nil && marker != "" {
			f.CS2.UpdateAvailable = true
			break
		}
	}
}

func (b *HostBackend) instanceStart(ctx context.Context, n int) error {
	m, err := b.instances()
	if err != nil {
		return err
	}
	if err := b.checkInstance(m, n); err != nil {
		return err
	}
	err = m.Start(ctx, n)
	LogAction("agent", fmt.Sprintf("start instance-%d", n), "", err)
	return err
}

func (b *HostBackend) instanceStop(n, graceS int) error {
	m, err := b.instances()
	if err != nil {
		return err
	}
	if err := b.checkInstance(m, n); err != nil {
		return err
	}
	if graceS <= 0 {
		graceS = 1
	}
	err = m.Stop(n, time.Duration(graceS)*time.Second)
	LogAction("agent", fmt.Sprintf("stop instance-%d", n), "", err)
	return err
}

func (b *HostBackend) instanceRestart(ctx context.Context, n int) error {
	m, err := b.instances()
	if err != nil {
		return err
	}
	if err := b.checkInstance(m, n); err != nil {
		return err
	}
	err = m.Restart(ctx, n)
	LogAction("agent", fmt.Sprintf("restart instance-%d", n), "", err)
	return err
}

// instanceCreate creates the next instance (building the Ready Up layer
// first on a host without one), runs beforeStart on its upper directory
// (fleet.cfg) and starts it.
func (b *HostBackend) instanceCreate(ctx context.Context, beforeStart func(serverDir string) error) (int, string, error) {
	m, err := b.instances()
	if err != nil {
		return 0, "", err
	}
	var out bytes.Buffer
	if _, err := m.CurrentLayer(); err != nil {
		if _, err := m.BuildLayerFromRelease(ctx, &out, "first instance"); err != nil {
			return 0, out.String(), fmt.Errorf("no Ready Up layer and building one failed: %w", err)
		}
	}
	n, err := m.Create(&out, 0)
	if err != nil {
		return n, out.String(), err
	}
	if beforeStart != nil {
		if err := beforeStart(m.L.Upper(n)); err != nil {
			return n, out.String(), err
		}
	}
	if err := m.Start(ctx, n); err != nil {
		return n, out.String(), err
	}
	fmt.Fprintf(&out, "[✓] instance %d started\n", n)
	LogAction("agent", fmt.Sprintf("create instance-%d", n), out.String(), nil)
	return n, out.String(), nil
}

func (b *HostBackend) instanceRemoveLast() (string, error) {
	m, err := b.instances()
	if err != nil {
		return "", err
	}
	list := m.Serving()
	if len(list) == 0 {
		return "", fmt.Errorf("no instances to remove")
	}
	var out bytes.Buffer
	err = m.Remove(&out, list[len(list)-1], true)
	LogAction("agent", "remove last instance", out.String(), err)
	return out.String(), err
}

func (b *HostBackend) instanceUpdateGame(ctx context.Context, progress func(string)) (string, error) {
	m, err := b.instances()
	if err != nil {
		return "", err
	}
	progress("SteamCMD update of the shared master install, Ready Up layer rebuild, restart of idle instances")
	var out bytes.Buffer
	out.WriteString("Instances share one master install: it is updated once for all of them.\n")
	err = m.UpdateGame(ctx, &out, instanceHoldNow(ctx))
	LogAction("agent", "instance update-game", out.String(), err)
	return out.String(), err
}

// instanceInstallReadyUp makes the shared layer carry the planned Ready Up
// (building it once; later calls for other servers find it current). The
// agent stops and starts the server around this call, so the restart picks
// the new layer up.
func (b *HostBackend) instanceInstallReadyUp(ctx context.Context, n int, plan hostagent.ReadyUpPlan) (string, error) {
	m, err := b.instances()
	if err != nil {
		return "", err
	}
	if err := b.checkInstance(m, n); err != nil {
		return "", err
	}
	var out bytes.Buffer
	if cur, err := m.CurrentLayer(); err == nil && layerHasPlan(m, cur, plan) {
		info := m.ReadLayerInfo(cur)
		fmt.Fprintf(&out, "The shared Ready Up layer %s already has Ready Up %s (%s).\n", info.ID, info.Core, info.Bundle)
		return out.String(), nil
	}
	src := LayerSource{Installer: plan.Installer, Zip: plan.Zip, Bundle: plan.Component, Version: plan.Version, AcceptLicense: plan.AcceptLicense}
	if src.Bundle == "" {
		src.Bundle = readyup.BundleFull
	}
	_, err = m.BuildLayer(ctx, &out, src, "platform")
	if err == nil {
		adoptReadyUpStack(&out, "platform")
	}
	LogAction("agent", fmt.Sprintf("Ready Up layer for instance-%d", n), out.String(), err)
	return out.String(), err
}

// layerHasPlan: layer dir carries the planned Ready Up. A release plan
// matches on its version; a plan from a configured bundle zip (agent config
// readyup_bundle: no version) matches when the layer was built from a zip
// with the same bytes. Either way the bundle must match.
func layerHasPlan(m *InstanceManager, dir string, plan hostagent.ReadyUpPlan) bool {
	info := m.ReadLayerInfo(dir)
	if plan.Component != "" && info.Bundle != plan.Component {
		return false
	}
	if want := strings.TrimPrefix(plan.Version, "v"); want != "" {
		return info.Core == want
	}
	return plan.Zip != "" && info.Zip != "" && sameFileContent(plan.Zip, filepath.Join(dir+".src", info.Zip))
}

// ReadyUpBundleFor (hostagent.ReadyUpBundleChooser): on an instance host the
// shared layer is built with PluginSettings.LayerBundle (full unless the
// operator chose one), whatever default the platform asks for; classic
// servers get what was asked.
func (b *HostBackend) ReadyUpBundleFor(requested string) string {
	if !InstanceBackendOn() {
		return requested
	}
	s, err := LoadPluginSettings()
	if err != nil {
		return requested
	}
	return s.LayerBundleFor(requested)
}

// ReadyUpCurrent (hostagent.ReadyUpCurrentChecker): on an instance host a
// server needs no Ready Up install when the current layer already carries
// the plan and the instance is not running an older layer. A new instance
// (created on the current layer) is then left running instead of being
// stopped and restarted, and the layer is not rebuilt for every new server.
func (b *HostBackend) ReadyUpCurrent(n int, plan hostagent.ReadyUpPlan) (bool, string) {
	if !InstanceBackendOn() {
		return false, ""
	}
	m, err := b.instances()
	if err != nil || !m.Exists(n) {
		return false, ""
	}
	cur, err := m.CurrentLayer()
	if err != nil || !layerHasPlan(m, cur, plan) {
		return false, ""
	}
	if inUse := m.LayerInUse(n); inUse != "" && m.IsRunning(n) && filepath.Clean(inUse) != filepath.Clean(cur) {
		return false, ""
	}
	info := m.ReadLayerInfo(cur)
	return true, fmt.Sprintf("instance %d runs on the shared Ready Up layer %s (Ready Up %s, %s)", n, info.ID, info.Core, info.Bundle)
}

func (b *HostBackend) instanceLogFile(n int, source string) (string, error) {
	m, err := b.instances()
	if err != nil {
		return "", err
	}
	if err := b.checkInstance(m, n); err != nil {
		return "", err
	}
	switch source {
	case "console":
		return m.L.ConsoleLog(n), nil
	case "readyup":
		dir := filepath.Join(m.L.Upper(n), "game", "csgo", "logs")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("instance %d has no game/csgo/logs yet", n)
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
			return "", fmt.Errorf("instance %d has no CS2 log files yet", n)
		}
		return filepath.Join(dir, newest), nil
	}
	return "", fmt.Errorf("unknown log source %q", source)
}
