package csm

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// Keeping Ready Up up to date by itself
//
// On the readyup stack, every `csm monitor` cycle (cron, every 5 minutes)
// also keeps Ready Up on its channel:
//
//   - It asks GitHub for the target release at most every
//     readyUpCheckEvery (the channel's newest, or the pinned version).
//   - A server whose installed Ready Up (readyup/installed.json) differs from
//     the target is updated only when it is safe: updates are not on hold
//     (the platform's update-hold, or `csm updates hold on`), and the server
//     is stopped, or Ready Up reports update_safe=true with nobody connected
//     for the idle grace period. Never mid-match.
//   - A failed install is retried after readyUpRetryAfter, not every cycle.
//
// `csm plugins auto off` turns this off. It needs the license answer like
// every install; without one the cycle says how to give it and moves on.

const (
	readyUpCheckEvery = 30 * time.Minute
	readyUpRetryAfter = time.Hour
)

// readyUpAutoState is what the monitor remembers about Ready Up.
type readyUpAutoState struct {
	CheckedAt int64  `json:"checked_at,omitempty"`
	Target    string `json:"target,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Pin       string `json:"pin,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// readyUpServerCheck is what decideReadyUpUpdate looks at for one server.
type readyUpServerCheck struct {
	Installed string // core version from installed.json, "" = no Ready Up
	Target    string // vX.Y.Z[-beta.N]
	Running   bool
	// SafeKnown / Safe / Players come from Ready Up's /status.
	SafeKnown bool
	Safe      bool
	Players   int
	Hold      UpdateHold
	IdleSince time.Time
	Now       time.Time
	Grace     time.Duration
	LastTry   time.Time
	LastFor   string // the target of LastTry
}

// readyUpUpdateDecision is the verdict for one server.
type readyUpUpdateDecision struct {
	Update bool
	Idle   bool // idle now (starts or keeps the grace clock)
	Reason string
}

func decideReadyUpUpdate(c readyUpServerCheck) readyUpUpdateDecision {
	want := strings.TrimPrefix(c.Target, "v")
	switch {
	case c.Installed == "":
		return readyUpUpdateDecision{Reason: "Ready Up is not installed (csm update-plugins installs it)"}
	case c.Installed == want:
		return readyUpUpdateDecision{Reason: "Ready Up " + want + " is current"}
	}
	upd := fmt.Sprintf("Ready Up %s -> %s", c.Installed, want)
	if c.Hold.On {
		return readyUpUpdateDecision{Reason: upd + " waits: updates are on hold (" + c.Hold.Reason + ")"}
	}
	if !c.LastTry.IsZero() && c.LastFor == c.Target && c.Now.Sub(c.LastTry) < readyUpRetryAfter {
		return readyUpUpdateDecision{Reason: upd + fmt.Sprintf(" waits: the last try failed %s ago", c.Now.Sub(c.LastTry).Round(time.Minute))}
	}
	if !c.Running {
		return readyUpUpdateDecision{Update: true, Reason: upd + " (server stopped)"}
	}
	switch {
	case !c.SafeKnown:
		return readyUpUpdateDecision{Reason: upd + " waits: Ready Up gave no update_safe answer"}
	case !c.Safe:
		return readyUpUpdateDecision{Reason: upd + " waits: a match is in progress (update_safe=false)"}
	case c.Players > 0:
		return readyUpUpdateDecision{Reason: upd + fmt.Sprintf(" waits: %d player(s) connected", c.Players)}
	}
	idleSince := c.IdleSince
	if idleSince.IsZero() || idleSince.After(c.Now) {
		idleSince = c.Now
	}
	if idle := c.Now.Sub(idleSince); idle < c.Grace {
		return readyUpUpdateDecision{Idle: true, Reason: upd + fmt.Sprintf(" waits: idle for %s of the %s grace period", idle.Round(time.Second), c.Grace)}
	}
	return readyUpUpdateDecision{Update: true, Idle: true, Reason: upd + " (idle, update_safe=true)"}
}

// readyUpAutoTarget returns the release the monitor keeps servers on, asking
// GitHub at most every readyUpCheckEvery (or when the channel / pin changed).
func readyUpAutoTarget(ctx context.Context, st *readyUpAutoState, r PluginSettings, now time.Time) (string, error) {
	fresh := st.Target != "" && st.Channel == r.ReadyUpChannel && st.Pin == r.ReadyUpVersion &&
		now.Sub(time.Unix(st.CheckedAt, 0)) < readyUpCheckEvery
	if fresh {
		return st.Target, nil
	}
	rel, err := readyUpClient().Resolve(ctx, r.ReadyUpChannel, r.ReadyUpVersion)
	st.CheckedAt, st.Channel, st.Pin = now.Unix(), r.ReadyUpChannel, r.ReadyUpVersion
	if err != nil {
		st.Target, st.LastError = "", err.Error()
		return "", err
	}
	st.Target, st.LastError = rel.TagName, ""
	return rel.TagName, nil
}

// runReadyUpAutoUpdate is the Ready Up part of one monitor cycle.
func runReadyUpAutoUpdate(ctx context.Context, logf func(string, ...any), mgr *TmuxManager, hold UpdateHold, grace time.Duration, state *autoUpdateState, saveState func()) {
	s, err := LoadPluginSettings()
	if err != nil {
		logf("Ready Up: could not read plugin settings: %v", err)
		return
	}
	r := s.Resolved()
	if r.Stack != PluginStackReadyUp {
		return
	}
	if !r.AutoUpdateOn() {
		logf("Ready Up: automatic updates are off (csm plugins auto on).")
		return
	}
	if r.AcceptLicense == "" {
		logf("Ready Up: not updating: no license answer yet (csm plugins license noncommercial|commercial, or %s).", EnvAcceptLicense)
		return
	}
	if state.ReadyUp == nil {
		state.ReadyUp = &readyUpAutoState{}
	}
	target, err := readyUpAutoTarget(ctx, state.ReadyUp, r, time.Now())
	saveState()
	if err != nil {
		logf("Ready Up: no target release: %v", err)
		return
	}

	var bundle *readyup.Bundle
	defer func() {
		if bundle != nil {
			_ = os.RemoveAll(bundle.Dir)
		}
	}()
	for i := 1; i <= mgr.NumServers; i++ {
		dir := mgr.serverDir(i)
		in, _ := readyup.ReadInstalled(dir)
		installed := ""
		if in != nil {
			installed = in.Components["core"]
		}
		st := state.server(i)
		gamePort, _ := detectServerPorts(mgr.CS2User, i)
		running := mgr.IsRunning(i)
		c := readyUpServerCheck{
			Installed: installed, Target: target, Running: running, Hold: hold,
			Now: time.Now(), Grace: grace, LastFor: st.ReadyUpLastTarget,
		}
		if st.ReadyUpIdleSince > 0 {
			c.IdleSince = time.Unix(st.ReadyUpIdleSince, 0)
		}
		if st.ReadyUpLastTry > 0 {
			c.LastTry = time.Unix(st.ReadyUpLastTry, 0)
		}
		if running && installed != "" && installed != strings.TrimPrefix(target, "v") && !hold.On {
			row := ProbeReadyUp(ctx, http.DefaultClient, FleetTarget{Server: i, Dir: dir, GamePort: gamePort, Running: true})
			c.Safe, c.SafeKnown = row.UpdateSafe()
			if row.Status != nil {
				c.Players = row.Status.Summary.Players.Connected
			}
		}
		d := decideReadyUpUpdate(c)
		if d.Idle {
			if st.ReadyUpIdleSince == 0 {
				st.ReadyUpIdleSince = c.Now.Unix()
			}
		} else {
			st.ReadyUpIdleSince = 0
		}
		saveState()
		if installed == "" || installed == strings.TrimPrefix(target, "v") {
			continue
		}
		logf("Server-%d: %s.", i, d.Reason)
		if !d.Update {
			continue
		}
		if bundle == nil {
			var out bytes.Buffer
			bundle, err = ReadyUpPlanFor(ctx, &out, s)
			if out.Len() > 0 {
				logf("%s", strings.TrimRight(out.String(), "\n"))
			}
			if err != nil {
				logf("Ready Up: download failed: %v", err)
				st.ReadyUpLastTry, st.ReadyUpLastTarget = time.Now().Unix(), target
				saveState()
				return
			}
		}
		st.ReadyUpLastTry, st.ReadyUpLastTarget, st.ReadyUpIdleSince = time.Now().Unix(), target, 0
		saveState()
		password := serverRCONPassword(mgr.CS2User, i)
		addr := fmt.Sprintf("127.0.0.1:%d", gamePort)
		if running {
			_ = rconSay(addr, password, "[csm] Server restarting for a Ready Up update. Back in a minute.")
			if err := mgr.Stop(i); err != nil {
				logf("Server-%d: could not stop it for the Ready Up update: %v", i, err)
				continue
			}
		}
		var out bytes.Buffer
		ierr := installReadyUpOn(ctx, &out, mgr.CS2User, bundle, r.AcceptLicense, []readyUpTarget{{Num: i, Dir: dir}})
		logf("%s", strings.TrimRight(out.String(), "\n"))
		if running {
			if err := mgr.Start(i); err != nil {
				logf("Server-%d: did not start again after the Ready Up update: %v", i, err)
				continue
			}
		}
		if ierr != nil {
			logf("Server-%d: Ready Up update failed (retrying in %s): %v", i, readyUpRetryAfter, ierr)
			continue
		}
		st.ReadyUpLastTry, st.ReadyUpLastTarget = 0, ""
		saveState()
		logf("Server-%d: Ready Up is now %s.", i, strings.TrimPrefix(target, "v"))
	}
}

// adoptPlatformLicenseUse takes the license answer from the platform when the
// operator has not given one: its `use` when the platform sends it, else
// "commercial" when it hands over a valid license key (a key is a paid
// commercial license). It never replaces an answer the operator gave.
func adoptPlatformLicenseUse(w interface{ Write([]byte) (int, error) }, lic *PlatformLicense) {
	if lic == nil {
		return
	}
	s, err := LoadPluginSettings()
	if err != nil || s.AcceptLicense != "" {
		return
	}
	use := strings.ToLower(strings.TrimSpace(lic.Use))
	if use != "noncommercial" && use != "commercial" {
		use = ""
		if lic.Key != nil && license.LooksLikeKey(strings.TrimSpace(*lic.Key)) {
			use = "commercial"
		}
	}
	if use == "" {
		return
	}
	s.AcceptLicense = use
	s.AcceptLicenseAt = time.Now().UTC().Format(time.RFC3339)
	s.AcceptLicenseBy = LicenseSourcePlatform
	if err := savePluginSettings(s); err != nil {
		fmt.Fprintf(w, "Ready Up: could not save the platform's license answer: %v\n", err)
		return
	}
	fmt.Fprintf(w, "Ready Up: license answer %q taken from the platform.\n", use)
}
