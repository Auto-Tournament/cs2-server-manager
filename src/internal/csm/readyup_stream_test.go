package csm

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadSSE(t *testing.T) {
	body := ": hello\r\n" +
		"event: snapshot\r\n" +
		"id: 512\r\n" +
		"data: {\"a\":1}\r\n" +
		"\r\n" +
		": keepalive\n" +
		"\n" +
		"event: patch\n" +
		"id:513\n" +
		"data: line1\n" +
		"data: line2\n" +
		"\n" +
		"data: no event name\n" +
		"\n" +
		"event: status\n" +
		"data: {\"cut\":\"off\"}\n" // no blank line: never dispatched
	var got []SSEEvent
	lines := 0
	err := ReadSSE(strings.NewReader(body), func() { lines++ }, func(ev SSEEvent) error {
		got = append(got, ev)
		return nil
	})
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF at the end of the body", err)
	}
	want := []SSEEvent{
		{Event: "snapshot", ID: "512", Data: `{"a":1}`},
		{Event: "patch", ID: "513", Data: "line1\nline2"},
		{Event: "message", Data: "no event name"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got %#v\nwant %#v", got, want)
	}
	if lines < 14 {
		t.Fatalf("activity called %d times; keepalives must count too", lines)
	}
}

func TestMergePatchRFC7396(t *testing.T) {
	// Examples from RFC 7396 appendix A.
	for _, tt := range []struct{ target, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`{"e":null}`, `{"a":1}`, `{"a":1,"e":null}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	} {
		var target, patch, want any
		_ = json.Unmarshal([]byte(tt.target), &target)
		_ = json.Unmarshal([]byte(tt.patch), &patch)
		_ = json.Unmarshal([]byte(tt.want), &want)
		if got := mergePatch(target, patch); !reflect.DeepEqual(got, want) {
			t.Errorf("merge %s + %s = %v, want %v", tt.target, tt.patch, got, want)
		}
	}
}

func TestApplyStreamEvents(t *testing.T) {
	doc := liveStatus()

	// Round 14 ends 9-5; team2's missing player connects.
	must(t, applyStreamEvent(doc, SSEEvent{Event: "patch", ID: "513",
		Data: `{"rev":513,"patch":{"round":{"number":15},"teams":{"team1":{"score":9},"team2":{"players":{"e":{"connected":true}}}}}}`}))
	st, err := statusFromDoc(doc)
	must(t, err)
	s := st.Summary
	if s.Round != 15 || s.Score.Team1 != 9 || s.Score.Team2 != 5 || s.Players.Connected != 4 || s.Players.Expected != 4 {
		t.Fatalf("after patch: round %d score %d-%d players %d/%d; want 15, 9-5, 4/4 (coach not counted)",
			s.Round, s.Score.Team1, s.Score.Team2, s.Players.Connected, s.Players.Expected)
	}
	if s.Map != "de_mirage" || s.MapNumber != 2 || s.NumMaps != 3 || s.Teams == nil || s.Teams.Team1 != "NAVI" {
		t.Fatalf("derived fields lost: %+v", s)
	}
	if string(st.Versions.Core) != "0.9.0" {
		t.Fatal("a patch must not touch fields outside state/summary")
	}

	// Tactical pause.
	must(t, applyStreamEvent(doc, SSEEvent{Event: "patch", Data: `{"rev":514,"patch":{"phase":"paused","pause":{"active":true,"type":"tactical"}}}`}))
	st, _ = statusFromDoc(doc)
	if got := PhaseLabel(st.Summary); got != "paused" {
		t.Fatalf("phase label %q, want paused", got)
	}

	// The platform link drops: a status event, merge-patched onto the document.
	must(t, applyStreamEvent(doc, SSEEvent{Event: "status", Data: `{"platform":{"state":"offline","since":1790340000000,"auto_pause_in_s":142},"update_safe":false}`}))
	st, _ = statusFromDoc(doc)
	if st.Platform.State != "offline" || st.Platform.Mode != "fleet" || st.Platform.AutoPauseInS == nil || *st.Platform.AutoPauseInS != 142 {
		t.Fatalf("platform after status event: %+v", st.Platform)
	}

	// Map 2 ends and the series is over: postgame; then a snapshot for idle.
	must(t, applyStreamEvent(doc, SSEEvent{Event: "patch", Data: `{"rev":515,"patch":{"phase":"series_end","pause":{"active":false},"series":{"score":{"team1":2}}}}`}))
	st, _ = statusFromDoc(doc)
	if PhaseLabel(st.Summary) != "postgame" || st.Summary.SeriesScore.Team1 != 2 {
		t.Fatalf("series end: %q, series %+v", PhaseLabel(st.Summary), st.Summary.SeriesScore)
	}
	must(t, applyStreamEvent(doc, SSEEvent{Event: "snapshot", ID: "600",
		Data: `{"summary":{"mode":"idle","phase":"","map":"de_mirage","players":{"connected":0,"expected":0}},"platform":{"mode":"fleet","state":"online"},"update_safe":true,"state":null}`}))
	st, _ = statusFromDoc(doc)
	if PhaseLabel(st.Summary) != "idle" || st.UpdateSafe == nil || !*st.UpdateSafe || st.Platform.AutoPauseInS != nil {
		t.Fatalf("snapshot did not replace: %+v, safe=%v", st.Summary, st.UpdateSafe)
	}
	if string(st.Versions.CS2Build) != "14090" {
		t.Fatal("a snapshot must keep the fields it does not carry")
	}

	if err := applyStreamEvent(doc, SSEEvent{Event: "patch", Data: `not json`}); err == nil {
		t.Fatal("bad patch: want an error")
	}
	must(t, applyStreamEvent(doc, SSEEvent{Event: "something_new", Data: `{}`}))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// waitRow polls the watcher until cond holds for server n.
func waitRow(t *testing.T, w *FleetWatcher, n int, what string, cond func(FleetRow) bool) FleetRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, r := range w.Rows() {
			if r.Target.Server == n && cond(r) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for server-%d: %s; rows: %+v", n, what, w.Rows())
		}
		select {
		case <-w.Changed():
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func fastWatch() WatchOptions {
	return WatchOptions{
		PollInterval: 30 * time.Millisecond,
		IdleTimeout:  2 * time.Second,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		StreamRetry:  time.Hour,
	}
}

func TestFleetWatcherStream(t *testing.T) {
	f := newFakeReadyUp(t, liveStatus())
	dir := t.TempDir()
	writeDiscovery(t, dir, map[string]any{"port": f.port(), "token": "rst_x"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewFleetWatcher([]FleetTarget{{Server: 1, Dir: dir, GamePort: 27015, Running: true}}, fastWatch())
	w.Run(ctx)

	waitRow(t, w, 1, "first /status", func(r FleetRow) bool { return r.State == ReadyUpOK })

	f.events <- "event: snapshot\nid: 512\ndata: " + mustJSON(t, map[string]any{
		"summary": liveStatus()["summary"], "platform": liveStatus()["platform"], "update_safe": false, "state": liveStatus()["state"],
	}) + "\n\n"
	f.events <- ": keepalive\n\n"
	f.events <- "event: patch\nid: 513\ndata: {\"rev\":513,\"patch\":{\"round\":{\"number\":15},\"teams\":{\"team1\":{\"score\":9}}}}\n\n"

	r := waitRow(t, w, 1, "patch applied", func(r FleetRow) bool {
		return r.Source == "stream" && r.Status != nil && r.Status.Summary.Round == 15
	})
	if r.Status.Summary.Score.Team1 != 9 || PhaseLabel(r.Status.Summary) != "live R15" {
		t.Fatalf("after patch: %+v", r.Status.Summary)
	}

	// The stream drops; the watcher reconnects and resumes from the last id.
	f.events <- "CLOSE"
	deadline := time.Now().Add(5 * time.Second)
	for {
		ids := f.lastEventIDs()
		if len(ids) >= 2 {
			if ids[0] != "" || ids[1] != "513" {
				t.Fatalf("Last-Event-ID per connection = %q, want [\"\" \"513\"]", ids)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reconnect; Last-Event-IDs %q", ids)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, a := range f.authSeen() {
		if a != "Bearer rst_x" {
			t.Fatalf("request without the token: %q", a)
		}
	}

	f.events <- "event: status\ndata: {\"update_safe\":true}\n\n"
	waitRow(t, w, 1, "status event", func(r FleetRow) bool { s, k := r.UpdateSafe(); return k && s })
}

func TestFleetWatcherPollsWithoutStream(t *testing.T) {
	f := newFakeReadyUp(t, idleStatus())
	f.mu.Lock()
	f.noStream = true
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewFleetWatcher([]FleetTarget{{Server: 2, Dir: t.TempDir(), GamePort: f.port() - ReadyUpPortOffset, Running: true}}, fastWatch())
	w.Run(ctx)

	waitRow(t, w, 2, "polled idle", func(r FleetRow) bool { return r.State == ReadyUpOK && r.Source == "poll" })
	f.setStatus(func(doc map[string]any) {
		doc["summary"].(map[string]any)["phase"] = "warmup"
		doc["update_safe"] = false
	})
	r := waitRow(t, w, 2, "polled warmup", func(r FleetRow) bool {
		return r.Status != nil && r.Status.Summary.Phase == "warmup"
	})
	if s, k := r.UpdateSafe(); !k || s {
		t.Fatal("update_safe from the poll not applied")
	}
}

func TestFleetWatcherProcessChanges(t *testing.T) {
	gamePort := freePort(t) - ReadyUpPortOffset
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := FleetTarget{Server: 3, Dir: t.TempDir(), GamePort: gamePort, Running: false}
	w := NewFleetWatcher([]FleetTarget{target}, fastWatch())
	w.Run(ctx)
	if r := w.Rows()[0]; r.State != ReadyUpStopped {
		t.Fatalf("stopped target starts as %s", r.State)
	}
	target.Running = true
	w.SetProcess(target)
	waitRow(t, w, 3, "no Ready Up once running", func(r FleetRow) bool { return r.State == ReadyUpNone })
	target.Running = false
	w.SetProcess(target)
	waitRow(t, w, 3, "stopped again", func(r FleetRow) bool { return r.State == ReadyUpStopped })
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return string(b)
}
