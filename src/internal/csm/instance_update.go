package csm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Keeping instances up to date
//
// Instances share a CS2 game version and a Ready Up layer, so an update is
// done once and every instance picks it up at its next start:
//
//   - Ready Up: a new release builds a new layer (layers/current moves).
//   - CS2: SteamCMD makes a new game version once (instance_game.go; the
//     version running instances use is never written), then the layer is
//     rebuilt from its own sources on it, so gameinfo.gi comes from the new
//     build.
//
// Old layers and game versions stay until no running instance uses them.
//
// A running instance whose layer or CS2 build is older than current is
// "restart pending". csm restarts it only when that is safe: updates are not
// on hold, Ready Up says update_safe=true, nobody is connected, and it has
// been idle for the grace period. Never mid-match.

// instanceUpdateMarkers: a CS2 update is out (legacy plugins, or Valve's own
// restart request).
var instanceUpdateMarkers = []string{autoUpdaterShutdownMarker, matchzyUpdateAvailableMarker, "MasterRequestRestart"}

// instanceAutoState is the monitor's memory for one instance.
type instanceAutoState struct {
	IdleSince int64 `json:"idle_since,omitempty"`
	LogOffset int64 `json:"log_offset,omitempty"`
}

// instanceLayerAutoState is the monitor's memory for the shared layer.
type instanceLayerAutoState struct {
	LastTry        int64  `json:"last_try,omitempty"`
	LastTarget     string `json:"last_target,omitempty"`
	LastGameUpdate int64  `json:"last_game_update,omitempty"`
}

func (st *autoUpdateState) instance(n int) *instanceAutoState {
	if st.Instances == nil {
		st.Instances = map[string]*instanceAutoState{}
	}
	key := strconv.Itoa(n)
	s, ok := st.Instances[key]
	if !ok || s == nil {
		s = &instanceAutoState{}
		st.Instances[key] = s
	}
	return s
}

// instanceRestartCheck is what decideInstanceRestart looks at.
type instanceRestartCheck struct {
	Pending   bool
	Why       string
	Hold      UpdateHold
	SafeKnown bool
	Safe      bool
	Players   int
	IdleSince time.Time
	Now       time.Time
	Grace     time.Duration
}

type instanceRestartDecision struct {
	Restart bool
	Idle    bool
	Reason  string
}

// decideInstanceRestart: restart a running instance onto the current layer /
// CS2 build only when it is idle and updates are not on hold.
func decideInstanceRestart(c instanceRestartCheck) instanceRestartDecision {
	if !c.Pending {
		return instanceRestartDecision{Reason: "up to date"}
	}
	upd := "restart pending (" + c.Why + ")"
	if c.Hold.On {
		return instanceRestartDecision{Reason: upd + " waits: updates are on hold (" + c.Hold.Reason + ")"}
	}
	switch {
	case !c.SafeKnown:
		return instanceRestartDecision{Reason: upd + " waits: Ready Up gave no update_safe answer"}
	case !c.Safe:
		return instanceRestartDecision{Reason: upd + " waits: a match is in progress (update_safe=false)"}
	case c.Players > 0:
		return instanceRestartDecision{Reason: upd + fmt.Sprintf(" waits: %d player(s) connected", c.Players)}
	}
	idleSince := c.IdleSince
	if idleSince.IsZero() || idleSince.After(c.Now) {
		idleSince = c.Now
	}
	if idle := c.Now.Sub(idleSince); idle < c.Grace {
		return instanceRestartDecision{Idle: true, Reason: upd + fmt.Sprintf(" waits: idle for %s of the %s grace period", idle.Round(time.Second), c.Grace)}
	}
	return instanceRestartDecision{Restart: true, Idle: true, Reason: upd + " (idle, update_safe=true)"}
}

// probeInstance asks Ready Up whether instance n may restart now.
func (m *InstanceManager) probeInstance(ctx context.Context, n int) (safe, known bool, players int) {
	row := ProbeReadyUp(ctx, http.DefaultClient, m.FleetTarget(n))
	safe, known = row.UpdateSafe()
	if row.Status != nil {
		players = row.Status.Summary.Players.Connected
	}
	return safe, known, players
}

// restartForUpdate announces, restarts and logs one instance.
func (m *InstanceManager) restartForUpdate(ctx context.Context, logf func(string, ...any), n int, why string) {
	if p, err := m.Ports(n); err == nil {
		_ = rconSay(fmt.Sprintf("127.0.0.1:%d", p.Game), detectRCONPassword(m.L.User), "[csm] Server restarting for an update. Back in a minute.")
	}
	if sh, err := m.Shadows(n); err == nil && len(sh) > 0 {
		logf("%s", strings.TrimRight(describeShadows(n, sh), "\n"))
	}
	if err := m.Restart(ctx, n); err != nil {
		logf("Instance-%d: restart for the update failed: %v", n, err)
		return
	}
	logf("Instance-%d: restarted (%s).", n, why)
}

// RestartIdleInstances restarts every running, restart-pending instance that
// Ready Up reports idle right now (no grace period: an operator or the
// platform asked). Busy ones are left for csm monitor.
func (m *InstanceManager) RestartIdleInstances(ctx context.Context, w io.Writer, hold UpdateHold) {
	logf := func(f string, a ...any) { fmt.Fprintf(w, f+"\n", a...) }
	for _, n := range m.List() {
		if !m.IsRunning(n) {
			continue
		}
		pending, why := m.RestartPending(n)
		if !pending {
			continue
		}
		c := instanceRestartCheck{Pending: true, Why: why, Hold: hold, Now: time.Now()}
		c.Safe, c.SafeKnown, c.Players = m.probeInstance(ctx, n)
		d := decideInstanceRestart(c)
		if !d.Restart {
			logf("Instance-%d: %s; csm monitor restarts it once it is idle.", n, d.Reason)
			continue
		}
		m.restartForUpdate(ctx, logf, n, why)
	}
}

// UpdateGame makes a new CS2 game version with SteamCMD, rebuilds the Ready
// Up layer on it and restarts idle instances. Busy instances keep running on
// their old layer and game version until csm monitor finds them idle; old
// versions are removed once nothing uses them.
func (m *InstanceManager) UpdateGame(ctx context.Context, w io.Writer, hold UpdateHold) error {
	if err := m.requireInstancePrivileges("instance update-game"); err != nil {
		return err
	}
	_, err := withGameUpdateLock(func() (string, error) { return "", m.updateGameLocked(ctx, w) })
	if err != nil {
		return err
	}
	m.RestartIdleInstances(ctx, w, hold)
	m.GC(w)
	return nil
}

// updateGameLocked is UpdateGame's SteamCMD + layer part; the caller holds
// the game update lock.
func (m *InstanceManager) updateGameLocked(ctx context.Context, w io.Writer) error {
	if masterReadOnly() {
		return fmt.Errorf("%s is set: csm does not update the master install %s", EnvInstanceMasterReadOnly, m.L.Master)
	}
	if err := steamcmdRunAsPreflight(m.L.User); err != nil {
		return err
	}
	if _, err := m.updateGameVersion(ctx, w); err != nil {
		return err
	}
	if cur, err := m.CurrentLayer(); err == nil {
		if stale, why := m.layerStale(cur); stale {
			fmt.Fprintf(w, "[Ready Up layer] %s; rebuilding it.\n", why)
			if _, err := m.RebuildLayer(ctx, w, "CS2 update"); err != nil {
				return fmt.Errorf("rebuilding the Ready Up layer on the new CS2 version: %w", err)
			}
		}
	}
	return nil
}

// layerStale: the layer does not sit on the current game version, or the
// install it sits on changed build under it (a master install updated
// outside csm).
func (m *InstanceManager) layerStale(layer string) (bool, string) {
	info := m.ReadLayerInfo(layer)
	game, cur := m.layerGame(layer), m.CurrentGame()
	if game != cur {
		return true, fmt.Sprintf("layer %s sits on game version %s, the current one is %s", info.ID, filepath.Base(game), filepath.Base(cur))
	}
	if mb := gameBuild(cur); mb > 0 && info.MasterBuild != 0 && info.MasterBuild != mb {
		return true, fmt.Sprintf("layer %s was built on CS2 build %d, the install is now %d", info.ID, info.MasterBuild, mb)
	}
	return false, ""
}

// UpdateReadyUp builds a layer from the configured Ready Up release (unless
// the current one already has it) and restarts idle instances onto it.
func (m *InstanceManager) UpdateReadyUp(ctx context.Context, w io.Writer, hold UpdateHold, src *LayerSource) error {
	if src != nil {
		if _, err := m.BuildLayer(ctx, w, *src, "operator"); err != nil {
			return err
		}
	} else {
		s, err := LoadPluginSettings()
		if err != nil {
			return err
		}
		r := s.Resolved()
		if cur, err := m.CurrentLayer(); err == nil && r.AcceptLicense != "" {
			if rel, err := readyUpClient().Resolve(ctx, r.ReadyUpChannel, r.ReadyUpVersion); err == nil &&
				layerCore(cur) == strings.TrimPrefix(rel.TagName, "v") && m.ReadLayerInfo(cur).Bundle == s.LayerBundle() {
				fmt.Fprintf(w, "[Ready Up layer] %s already has Ready Up %s (%s).\n", m.ReadLayerInfo(cur).ID, layerCore(cur), s.LayerBundle())
				m.RestartIdleInstances(ctx, w, hold)
				return nil
			}
		}
		if _, err := m.BuildLayerFromRelease(ctx, w, "operator"); err != nil {
			return err
		}
	}
	m.RestartIdleInstances(ctx, w, hold)
	m.GC(w)
	return nil
}

// runInstanceMonitor is the instance part of one `csm monitor` cycle.
func runInstanceMonitor(ctx context.Context, logf func(string, ...any), hold UpdateHold, grace time.Duration, state *autoUpdateState, saveState func()) {
	m, err := NewInstanceManager()
	if err != nil {
		logf("Instances: %v", err)
		return
	}
	list := m.List()
	if len(list) == 0 {
		return
	}
	if state.InstanceLayer == nil {
		state.InstanceLayer = &instanceLayerAutoState{}
	}
	ls := state.InstanceLayer
	logw := monitorWriter{logf}

	// 1. CS2 update: a marker in any instance's console log.
	var marked []int
	sizes := map[int]int64{}
	for _, n := range list {
		marker, size, err := logHasMarkerAfter(m.L.ConsoleLog(n), state.instance(n).LogOffset, instanceUpdateMarkers...)
		if err != nil {
			continue
		}
		sizes[n] = size
		if marker != "" {
			marked = append(marked, n)
		}
	}
	if len(marked) > 0 {
		switch {
		case hold.On:
			logf("Instances: CS2 update available (instance %v); waits: updates are on hold (%s).", marked, hold.Reason)
		case masterReadOnly():
			logf("Instances: CS2 update available (instance %v); %s is set, so the master install is not updated here.", marked, EnvInstanceMasterReadOnly)
		case ls.LastGameUpdate > 0 && time.Since(time.Unix(ls.LastGameUpdate, 0)) < autoUpdateCooldown:
			logf("Instances: CS2 update available; the master install was updated %s ago, waiting for the %s cooldown.",
				time.Since(time.Unix(ls.LastGameUpdate, 0)).Round(time.Second), autoUpdateCooldown)
		default:
			logf("Instances: CS2 update available (instance %v); making a new game version once.", marked)
			for n, sz := range sizes {
				state.instance(n).LogOffset = sz
			}
			ls.LastGameUpdate = time.Now().Unix()
			saveState()
			_, err := withGameUpdateLock(func() (string, error) {
				if err := steamcmdRunAsPreflight(m.L.User); err != nil {
					return "", err
				}
				_, err := m.updateGameVersion(ctx, logw)
				return "", err
			})
			if err != nil {
				logf("Instances: CS2 update failed: %v", err)
			}
		}
	}

	// 2. The layer follows the game version (gameinfo.gi comes from it).
	cur, cerr := m.CurrentLayer()
	if cerr != nil {
		logf("Instances: %v", cerr)
		return
	}
	if stale, why := m.layerStale(cur); stale {
		logf("Instances: %s; rebuilding it.", why)
		if p, err := m.RebuildLayer(ctx, logw, "CS2 build "+strconv.FormatInt(m.MasterBuild(), 10)); err != nil {
			logf("Instances: layer rebuild failed: %v", err)
		} else {
			cur = p
		}
	}

	// 3. Ready Up on its channel.
	if s, err := LoadPluginSettings(); err == nil {
		r := s.Resolved()
		switch {
		case !r.AutoUpdateOn():
		case r.AcceptLicense == "":
			logf("Instances: Ready Up not updated: no license answer yet (csm plugins license noncommercial|commercial).")
		default:
			if state.ReadyUp == nil {
				state.ReadyUp = &readyUpAutoState{}
			}
			target, err := readyUpAutoTarget(ctx, state.ReadyUp, r, time.Now())
			saveState()
			have := layerCore(cur)
			want := strings.TrimPrefix(target, "v")
			switch {
			case err != nil:
				logf("Instances: no Ready Up target release: %v", err)
			case have == want:
			case hold.On:
				logf("Instances: Ready Up %s -> %s waits: updates are on hold (%s).", have, want, hold.Reason)
			case ls.LastTarget == target && time.Since(time.Unix(ls.LastTry, 0)) < readyUpRetryAfter:
				logf("Instances: Ready Up %s -> %s waits: the last try failed %s ago.", have, want, time.Since(time.Unix(ls.LastTry, 0)).Round(time.Minute))
			default:
				logf("Instances: Ready Up %s -> %s: building a new layer.", have, want)
				if _, err := m.BuildLayerFromRelease(ctx, logw, "auto-update"); err != nil {
					ls.LastTry, ls.LastTarget = time.Now().Unix(), target
					logf("Instances: Ready Up layer build failed (retrying in %s): %v", readyUpRetryAfter, err)
				} else {
					ls.LastTry, ls.LastTarget = 0, ""
				}
				saveState()
			}
		}
	}

	// 4. Rolling restarts of idle instances.
	for _, n := range list {
		st := state.instance(n)
		if !m.IsRunning(n) {
			st.IdleSince = 0
			continue
		}
		pending, why := m.RestartPending(n)
		c := instanceRestartCheck{Pending: pending, Why: why, Hold: hold, Now: time.Now(), Grace: grace}
		if pending && !hold.On {
			c.Safe, c.SafeKnown, c.Players = m.probeInstance(ctx, n)
		}
		if st.IdleSince > 0 {
			c.IdleSince = time.Unix(st.IdleSince, 0)
		}
		d := decideInstanceRestart(c)
		if d.Idle {
			if st.IdleSince == 0 {
				st.IdleSince = c.Now.Unix()
			}
		} else {
			st.IdleSince = 0
		}
		saveState()
		if !pending {
			continue
		}
		logf("Instance-%d: %s.", n, d.Reason)
		if !d.Restart {
			continue
		}
		st.IdleSince = 0
		saveState()
		m.restartForUpdate(ctx, logf, n, why)
	}

	// 5. Old layers and game versions nothing uses any more.
	m.GC(logw)
}

// monitorWriter turns monitor log lines into an io.Writer.
type monitorWriter struct{ logf func(string, ...any) }

func (w monitorWriter) Write(p []byte) (int, error) {
	if s := strings.TrimRight(string(p), "\n"); s != "" {
		w.logf("%s", s)
	}
	return len(p), nil
}

// instanceHoldNow resolves the update hold for CLI/agent actions.
func instanceHoldNow(ctx context.Context) UpdateHold {
	s, err := LoadAutoUpdateSettings()
	if err != nil {
		return UpdateHold{On: true, Source: HoldSourceManual, Reason: "the settings file could not be read: " + err.Error()}
	}
	return ResolveUpdateHold(ctx, s)
}

// InstanceHoldNow is instanceHoldNow for the CLI.
func InstanceHoldNow(ctx context.Context) UpdateHold { return instanceHoldNow(ctx) }
