package csm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live fleet data from Ready Up's /stream
//
// /stream is Server-Sent Events (FLEET.md §17.2):
//
//	event: snapshot   data: {"summary":…,"platform":…,"update_safe":…,"state":…}
//	event: patch      data: {"rev":513,"patch":{…JSON merge patch on state…}}
//	event: status     data: {"platform":{…},"update_safe":false}   (merge patch on the document)
//	: keepalive       every 15 s
//
// The watcher keeps one goroutine per server. Each one reads status.json,
// fetches /status once (for the fields a snapshot does not carry, like
// versions), then follows /stream and applies every event to its copy of the
// document. Patches change `state`, not `summary`, so the watcher derives the
// summary's match fields from `state` again after each patch.
//
// When the stream drops, the goroutine reconnects with exponential backoff
// and sends Last-Event-ID so Ready Up can replay what was missed. A Ready Up
// without /stream (404) is polled on /status instead.

// WatchOptions tunes the watcher. Zero values take the defaults.
type WatchOptions struct {
	// PollInterval is how often /status is polled when /stream is not
	// available, and how often a stopped server is looked at again.
	PollInterval time.Duration
	// IdleTimeout drops a stream that sent nothing (not even a keepalive).
	IdleTimeout time.Duration
	// MinBackoff and MaxBackoff bound the reconnect delay.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// StreamRetry is how long to poll before trying /stream again after it
	// was not available.
	StreamRetry time.Duration
}

func (o WatchOptions) withDefaults() WatchOptions {
	if o.PollInterval <= 0 {
		o.PollInterval = 5 * time.Second
	}
	if o.IdleTimeout <= 0 {
		// Three missed keepalives.
		o.IdleTimeout = 45 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.StreamRetry <= 0 {
		o.StreamRetry = time.Minute
	}
	return o
}

// FleetWatcher keeps a live FleetRow per server.
type FleetWatcher struct {
	opts    WatchOptions
	client  *http.Client
	mu      sync.Mutex
	entries map[int]*watchEntry
	changed chan struct{}
}

type watchEntry struct {
	target FleetTarget
	row    FleetRow
	doc    map[string]any // the /status document, kept current by the stream
	lastID string
}

// NewFleetWatcher creates a watcher for targets. Call Run to start it.
func NewFleetWatcher(targets []FleetTarget, opts WatchOptions) *FleetWatcher {
	w := &FleetWatcher{
		opts:    opts.withDefaults(),
		client:  &http.Client{},
		entries: make(map[int]*watchEntry, len(targets)),
		changed: make(chan struct{}, 1),
	}
	for _, t := range targets {
		state := ReadyUpNoResponse
		if !t.Running {
			state = ReadyUpStopped
		}
		w.entries[t.Server] = &watchEntry{
			target: t,
			row:    FleetRow{Target: t, StatusPort: t.GamePort + ReadyUpPortOffset, State: state, Err: "connecting"},
		}
	}
	return w
}

// Run starts one goroutine per server. They stop when ctx is done.
func (w *FleetWatcher) Run(ctx context.Context) {
	w.mu.Lock()
	servers := make([]int, 0, len(w.entries))
	for n := range w.entries {
		servers = append(servers, n)
	}
	w.mu.Unlock()
	for _, n := range servers {
		go w.watchServer(ctx, n)
	}
}

// Changed signals (coalesced) that at least one row changed.
func (w *FleetWatcher) Changed() <-chan struct{} { return w.changed }

// Rows returns a copy of every row, in server order.
func (w *FleetWatcher) Rows() []FleetRow {
	w.mu.Lock()
	defer w.mu.Unlock()
	rows := make([]FleetRow, 0, len(w.entries))
	for _, e := range w.entries {
		rows = append(rows, e.row)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Target.Server < rows[b].Target.Server })
	return rows
}

// SetProcess updates what tmux says about a server (the TUI re-checks it
// periodically).
func (w *FleetWatcher) SetProcess(t FleetTarget) {
	w.mu.Lock()
	e, ok := w.entries[t.Server]
	if !ok {
		w.mu.Unlock()
		return
	}
	was := e.target.Running
	e.target = t
	e.row.Target = t
	if !t.Running {
		e.row.State = ReadyUpStopped
		e.row.Status = nil
		e.row.Err = ""
	} else if !was {
		e.row.State = ReadyUpNoResponse
		e.row.Err = "connecting"
	}
	w.mu.Unlock()
	w.notify()
}

func (w *FleetWatcher) notify() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *FleetWatcher) target(n int) FleetTarget {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.entries[n].target
}

// update runs fn on a server's entry under the lock and signals a change.
func (w *FleetWatcher) update(n int, fn func(e *watchEntry)) {
	w.mu.Lock()
	fn(w.entries[n])
	w.mu.Unlock()
	w.notify()
}

// setDoc stores a fresh document for a server and rebuilds its row.
func (w *FleetWatcher) setDoc(n int, d ReadyUpDiscovery, doc map[string]any, source string) {
	st, err := statusFromDoc(doc)
	w.update(n, func(e *watchEntry) {
		if !e.target.Running {
			return
		}
		e.doc = doc
		applyProbeResult(&e.row, d, st, err)
		e.row.Source = source
		e.row.UpdatedAt = time.Now()
	})
}

func (w *FleetWatcher) setErr(n int, d ReadyUpDiscovery, err error) {
	w.update(n, func(e *watchEntry) {
		if !e.target.Running {
			return
		}
		applyProbeResult(&e.row, d, nil, err)
		e.row.Source = ""
		e.doc = nil
		e.lastID = ""
	})
}

// sleep waits for d or until ctx is done; it reports false when ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// errStreamUnsupported: this Ready Up has no /stream; poll /status instead.
var errStreamUnsupported = errors.New("/stream not available")

func (w *FleetWatcher) watchServer(ctx context.Context, n int) {
	backoff := w.opts.MinBackoff
	grow := func() {
		backoff *= 2
		if backoff > w.opts.MaxBackoff {
			backoff = w.opts.MaxBackoff
		}
	}
	for ctx.Err() == nil {
		t := w.target(n)
		if !t.Running {
			if !sleepCtx(ctx, w.opts.PollInterval) {
				return
			}
			continue
		}
		// Re-read status.json every time: a restarted server may have a new
		// port or token.
		d, derr := DiscoverReadyUp(t.Dir, t.GamePort)
		if derr != nil {
			w.setErr(n, d, derr)
			if !sleepCtx(ctx, backoff) {
				return
			}
			grow()
			continue
		}
		_, doc, err := FetchReadyUpStatus(ctx, w.client, d)
		if err != nil {
			w.setErr(n, d, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			grow()
			continue
		}
		w.setDoc(n, d, doc, "poll")

		gotEvents, err := w.followStream(ctx, n, d)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errStreamUnsupported):
			w.pollUntil(ctx, n, d, time.Now().Add(w.opts.StreamRetry))
			backoff = w.opts.MinBackoff
			continue
		case gotEvents:
			// The stream worked for a while; reconnect quickly.
			backoff = w.opts.MinBackoff
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		grow()
	}
}

// pollUntil polls /status until deadline, an error, or ctx is done.
func (w *FleetWatcher) pollUntil(ctx context.Context, n int, d ReadyUpDiscovery, deadline time.Time) {
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, w.opts.PollInterval) {
			return
		}
		if !w.target(n).Running {
			return
		}
		_, doc, err := FetchReadyUpStatus(ctx, w.client, d)
		if err != nil {
			w.setErr(n, d, err)
			return
		}
		w.setDoc(n, d, doc, "poll")
	}
}

// followStream reads /stream until it ends. gotEvents is true when at least
// one event was applied.
func (w *FleetWatcher) followStream(ctx context.Context, n int, d ReadyUpDiscovery) (gotEvents bool, err error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := d.newRequest(sctx, "/stream")
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	w.mu.Lock()
	if id := w.entries[n].lastID; id != "" {
		req.Header.Set("Last-Event-ID", id)
	}
	w.mu.Unlock()

	resp, err := w.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed ||
		resp.StatusCode == http.StatusNotImplemented {
		return false, errStreamUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("/stream returned %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "text/event-stream") {
		return false, errStreamUnsupported
	}

	idle := time.AfterFunc(w.opts.IdleTimeout, cancel)
	defer idle.Stop()

	err = ReadSSE(resp.Body, func() { idle.Reset(w.opts.IdleTimeout) }, func(ev SSEEvent) error {
		var applyErr error
		w.update(n, func(e *watchEntry) {
			if !e.target.Running || e.doc == nil {
				return
			}
			applyErr = applyStreamEvent(e.doc, ev)
			if applyErr != nil {
				return
			}
			if ev.ID != "" {
				e.lastID = ev.ID
			}
			st, serr := statusFromDoc(e.doc)
			applyProbeResult(&e.row, d, st, serr)
			e.row.Source = "stream"
			e.row.UpdatedAt = time.Now()
		})
		if applyErr != nil {
			return applyErr
		}
		gotEvents = true
		return nil
	})
	if sctx.Err() != nil && ctx.Err() == nil {
		return gotEvents, fmt.Errorf("/stream idle for %s", w.opts.IdleTimeout)
	}
	return gotEvents, err
}

// SSEEvent is one Server-Sent Event.
type SSEEvent struct {
	Event string
	ID    string
	Data  string
}

// ReadSSE parses a text/event-stream body and calls onEvent for every event.
// activity is called for every line, comments included, so callers can run
// an idle timer off keepalives. It returns when the body ends or onEvent
// returns an error.
func ReadSSE(r io.Reader, activity func(), onEvent func(SSEEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	var ev SSEEvent
	var data []string
	hasData := false
	for sc.Scan() {
		if activity != nil {
			activity()
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if hasData {
				ev.Data = strings.Join(data, "\n")
				if ev.Event == "" {
					ev.Event = "message"
				}
				if err := onEvent(ev); err != nil {
					return err
				}
			}
			ev = SSEEvent{}
			data = data[:0]
			hasData = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment (keepalive)
		}
		field, value := line, ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			field, value = line[:i], strings.TrimPrefix(line[i+1:], " ")
		}
		switch field {
		case "event":
			ev.Event = value
		case "data":
			data = append(data, value)
			hasData = true
		case "id":
			ev.ID = value
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// applyStreamEvent applies one /stream event to a /status document.
func applyStreamEvent(doc map[string]any, ev SSEEvent) error {
	switch ev.Event {
	case "snapshot":
		var snap map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &snap); err != nil {
			return fmt.Errorf("bad snapshot: %w", err)
		}
		// A snapshot replaces what it carries; fields it does not carry
		// (versions, hostname, …) stay as /status gave them.
		for k, v := range snap {
			doc[k] = v
		}
	case "status":
		var p map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
			return fmt.Errorf("bad status event: %w", err)
		}
		mergePatch(doc, p)
	case "patch":
		var p struct {
			Rev   json.Number    `json:"rev"`
			Patch map[string]any `json:"patch"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
			return fmt.Errorf("bad patch: %w", err)
		}
		state, _ := doc["state"].(map[string]any)
		if state == nil {
			state = map[string]any{}
		}
		doc["state"] = mergePatch(state, p.Patch)
		doc["summary"] = summaryFromState(doc["state"].(map[string]any), asMap(doc["summary"]))
	default:
		// Unknown events are ignored so newer Ready Up builds can add some.
	}
	return nil
}

// mergePatch applies an RFC 7396 JSON merge patch to target in place and
// returns the result.
func mergePatch(target any, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	tm, ok := target.(map[string]any)
	if !ok || tm == nil {
		tm = map[string]any{}
	}
	for k, v := range pm {
		if v == nil {
			delete(tm, k)
			continue
		}
		tm[k] = mergePatch(tm[k], v)
	}
	return tm
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asNum(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	}
	return ""
}

// summaryFromState rebuilds status.summary's match fields from a MatchState
// (FLEET.md §9.1) after a patch. Fields the state does not determine (mode)
// are kept from prev.
func summaryFromState(state map[string]any, prev map[string]any) map[string]any {
	s := map[string]any{}
	for k, v := range prev {
		s[k] = v
	}
	if mode, _ := s["mode"].(string); mode == "" || mode == "idle" {
		s["mode"] = "match"
	}
	if v, ok := state["phase"].(string); ok {
		s["phase"] = v
	}
	if id := asString(state["match_id"]); id != "" {
		s["match_id"] = id
	}
	if round := asMap(state["round"]); round != nil {
		if n, ok := asNum(round["number"]); ok {
			s["round"] = n
		}
	}
	if pause := asMap(state["pause"]); pause != nil {
		if a, ok := pause["active"].(bool); ok {
			s["paused"] = a
		}
	}

	teams := asMap(state["teams"])
	t1, t2 := asMap(teams["team1"]), asMap(teams["team2"])
	if t1 != nil || t2 != nil {
		names := map[string]any{"team1": asString(t1["name"]), "team2": asString(t2["name"])}
		s["teams"] = names
		sc1, _ := asNum(t1["score"])
		sc2, _ := asNum(t2["score"])
		s["score"] = map[string]any{"team1": sc1, "team2": sc2}

		connected, expected := 0, 0
		for _, t := range []map[string]any{t1, t2} {
			for _, p := range asMap(t["players"]) {
				pm := asMap(p)
				if role, _ := pm["role"].(string); role != "" && role != "player" {
					continue
				}
				expected++
				if c, _ := pm["connected"].(bool); c {
					connected++
				}
			}
		}
		if expected == 0 {
			if n, ok := asNum(asMap(state["ready"])["required_per_team"]); ok {
				expected = int(n) * 2
			}
		}
		s["players"] = map[string]any{"connected": connected, "expected": expected}
	}

	if series := asMap(state["series"]); series != nil {
		if n, ok := asNum(series["num_maps"]); ok {
			s["num_maps"] = n
		}
		cur, hasCur := asNum(series["current_map"])
		if hasCur {
			s["map_number"] = cur
			key := strconv.Itoa(int(cur))
			if m := asMap(asMap(series["maps"])[key]); m != nil {
				if name := asString(m["name"]); name != "" {
					s["map"] = name
				}
			}
		}
		if ss := asMap(series["score"]); ss != nil {
			a, _ := asNum(ss["team1"])
			b, _ := asNum(ss["team2"])
			s["series_score"] = map[string]any{"team1": a, "team2": b}
		}
	}
	return s
}
