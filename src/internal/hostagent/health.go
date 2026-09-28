package hostagent

import (
	"context"
	"sync"
	"time"
)

// Process health (FLEET.md §18.2 host.health)
//
// Every HealthEvery the agent looks at every server and reports changes the
// agent did not cause itself:
//
//   - exited: the server's tmux session went away without the agent (or a
//     job it runs) stopping it. csm starts CS2 in tmux and cannot see the
//     exit code, so "crashed" is never claimed.
//   - hung: the process runs, Ready Up is installed (status.json exists) and
//     its /health has not answered OK for HungAfter (30 s).
//   - recovered: /health answers again after hung, or the process is back
//     after exited (someone or csm.sh started it).
//   - restarted: sent by server.restart itself.
//
// csm does not restart anything on its own from here: during a match the
// platform decides (failover proposal, FLEET.md §11); when idle, csm's
// existing auto-restart rules apply.
//
// The same tick feeds host.inventory: sent when something changed (at most
// every 5 s) and every InventoryEvery regardless.

type serverTrack struct {
	seen       bool
	running    bool
	exited     bool
	hung       bool
	badSince   time.Time
	expecting  int       // jobs in flight on this server
	quietUntil time.Time // ignore transitions until then
	starts     []time.Time
}

type healthTracker struct {
	a        *Agent
	mu       sync.Mutex
	servers  map[int]*serverTrack
	all      int // heavy jobs in flight (expect changes everywhere)
	allQuiet time.Time
}

func newHealthTracker(a *Agent) *healthTracker {
	return &healthTracker{a: a, servers: map[int]*serverTrack{}}
}

func (h *healthTracker) track(n int) *serverTrack {
	t := h.servers[n]
	if t == nil {
		t = &serverTrack{}
		h.servers[n] = t
	}
	return t
}

// expect marks an agent job on server n (on) or its end (off). Transitions
// during the job and for 30 s after it are the agent's own.
func (h *healthTracker) expect(n int, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.track(n)
	if on {
		t.expecting++
		return
	}
	if t.expecting > 0 {
		t.expecting--
	}
	t.quietUntil = time.Now().Add(30 * time.Second)
}

func (h *healthTracker) expectAll(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if on {
		h.all++
		return
	}
	if h.all > 0 {
		h.all--
	}
	h.allQuiet = time.Now().Add(30 * time.Second)
}

// started counts a start the agent did (restarts_24h).
func (h *healthTracker) started(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.track(n)
	t.starts = append(t.starts, time.Now())
	t.running = true
	t.seen = true
	t.exited = false
}

func (h *healthTracker) forget(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.servers, n)
}

func (h *healthTracker) restarts24h(n int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.servers[n]
	if t == nil {
		return 0
	}
	cut := time.Now().Add(-24 * time.Hour)
	kept := t.starts[:0]
	for _, s := range t.starts {
		if s.After(cut) {
			kept = append(kept, s)
		}
	}
	t.starts = kept
	return len(kept)
}

func (h *healthTracker) run(ctx context.Context) {
	t := time.NewTicker(h.a.opts.HealthEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if h.a.currentCreds() == nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		inv, servers, err := h.a.buildInventory(cctx)
		cancel()
		if err != nil {
			continue
		}
		for _, ev := range h.observe(servers, time.Now()) {
			h.a.sendHealth(ev)
		}
		h.a.publishInventory(inv, "", false)
	}
}

// observe compares a fresh look with the last one and returns the events.
func (h *healthTracker) observe(servers []ServerState, now time.Time) []HealthPayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []HealthPayload
	quietAll := h.all > 0 || now.Before(h.allQuiet)
	present := map[int]bool{}
	for _, s := range servers {
		present[s.Number] = true
		t := h.track(s.Number)
		name := ServerName(s.Number)
		quiet := quietAll || t.expecting > 0 || now.Before(t.quietUntil) || s.Updating

		if !t.seen {
			t.seen = true
			// The state found at the first look is not a restart.
			t.running = s.Running
			continue
		}

		switch {
		case t.running && !s.Running:
			t.hung = false
			t.badSince = time.Time{}
			if !quiet {
				t.exited = true
				out = append(out, HealthPayload{Server: name, Event: "exited", Detail: "the server process stopped and the host agent did not stop it"})
			}
		case !t.running && s.Running:
			t.starts = append(t.starts, now)
			if t.exited && !quiet {
				out = append(out, HealthPayload{Server: name, Event: "recovered", Detail: "the server process is running again"})
			}
			t.exited = false
		}
		t.running = s.Running

		// Hang detection: only where Ready Up runs (it has a /health).
		if s.Running && s.ReadyUpPresent && s.Health != "ok" {
			if t.badSince.IsZero() {
				t.badSince = now
			}
			if !t.hung && now.Sub(t.badSince) >= h.a.opts.HungAfter && !quiet {
				t.hung = true
				out = append(out, HealthPayload{Server: name, Event: "hung", Detail: "Ready Up /health has not answered OK for " + now.Sub(t.badSince).Round(time.Second).String() + " (" + s.Health + ")"})
			}
		} else {
			if t.hung && s.Running {
				out = append(out, HealthPayload{Server: name, Event: "recovered", Detail: "Ready Up /health answers again"})
			}
			t.hung = false
			t.badSince = time.Time{}
		}
	}
	for n := range h.servers {
		if !present[n] {
			delete(h.servers, n)
		}
	}
	return out
}
