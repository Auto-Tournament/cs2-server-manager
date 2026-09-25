package csm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeReadyUp is a stand-in for Ready Up's status endpoint (FLEET.md §17).
type fakeReadyUp struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	status     map[string]any
	statusCode int
	noStream   bool
	auth       []string // Authorization headers seen
	lastIDs    []string // Last-Event-ID headers seen on /stream
	streams    int
	events     chan string // raw SSE text written to the open /stream
}

func newFakeReadyUp(t *testing.T, status map[string]any) *fakeReadyUp {
	t.Helper()
	f := &fakeReadyUp{t: t, status: status, statusCode: http.StatusOK, events: make(chan string, 64)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeReadyUp) port() int {
	_, p, _ := net.SplitHostPort(f.srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func (f *fakeReadyUp) authSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fakeReadyUp) lastEventIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lastIDs...)
}

func (f *fakeReadyUp) setStatus(fn func(doc map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.status)
}

func (f *fakeReadyUp) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	switch r.URL.Path {
	case "/status":
		f.mu.Lock()
		code := f.statusCode
		body, _ := json.Marshal(f.status)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	case "/stream":
		f.mu.Lock()
		if f.noStream {
			f.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		f.streams++
		f.lastIDs = append(f.lastIDs, r.Header.Get("Last-Event-ID"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-f.events:
				if ev == "CLOSE" {
					return
				}
				_, _ = w.Write([]byte(ev))
				w.(http.Flusher).Flush()
			}
		}
	default:
		http.NotFound(w, r)
	}
}

// liveStatus is a /status document for a live BO3 on map 2, round 14.
func liveStatus() map[string]any {
	var doc map[string]any
	_ = json.Unmarshal([]byte(`{
	  "server_id": "srv_1", "hostname": "cs2-server-1", "game_port": 27015, "uptime_s": 5400, "generated_at": 1790340000000,
	  "versions": {"core": "0.9.0", "plugin_api": 3, "plugins": {"match": "0.9.0", "fleet": "0.9.0"}, "cs2_build": 14090, "cs2_patch": "1.40.9.2"},
	  "selftest": {"pass": true, "passed": 12, "total": 12, "failures": [], "ran_at": 1790330000},
	  "platform": {"mode": "fleet", "state": "online", "since": 1790330000000, "reconnects": 0, "spool_msgs": 0},
	  "update_safe": false,
	  "summary": {"mode": "match", "phase": "live", "map": "de_mirage", "map_number": 2, "num_maps": 3, "round": 14,
	              "score": {"team1": 8, "team2": 5}, "series_score": {"team1": 1, "team2": 0},
	              "players": {"connected": 10, "expected": 10}, "match_id": "412",
	              "teams": {"team1": "NAVI", "team2": "G2"}, "paused": false},
	  "state": {"match_id": "412", "phase": "live", "round": {"number": 14},
	            "pause": {"active": false},
	            "series": {"num_maps": 3, "current_map": 2, "score": {"team1": 1, "team2": 0},
	                       "maps": {"1": {"name": "de_inferno"}, "2": {"name": "de_mirage"}, "3": {"name": "de_nuke"}}},
	            "teams": {"team1": {"name": "NAVI", "score": 8, "players": {"a": {"role": "player", "connected": true}, "b": {"role": "player", "connected": true}, "c": {"role": "coach", "connected": true}}},
	                      "team2": {"name": "G2", "score": 5, "players": {"d": {"role": "player", "connected": true}, "e": {"role": "player", "connected": false}}}}}
	}`), &doc)
	return doc
}

// idleStatus is a standalone Ready Up with no match.
func idleStatus() map[string]any {
	var doc map[string]any
	_ = json.Unmarshal([]byte(`{
	  "hostname": "cs2-server-2", "game_port": 27025, "uptime_s": 60, "generated_at": 1790340000000,
	  "versions": {"core": "0.9.0", "plugin_api": 3, "plugins": {"match": "0.9.0", "fleet": "0.9.0"}, "cs2_build": "14090", "cs2_patch": "1.40.9.2"},
	  "platform": {"mode": "standalone", "state": "offline", "since": 0},
	  "update_safe": true,
	  "summary": {"mode": "idle", "phase": "", "map": "de_dust2", "map_number": 0, "num_maps": 0, "round": 0,
	              "score": {"team1": 0, "team2": 0}, "series_score": {"team1": 0, "team2": 0},
	              "players": {"connected": 2, "expected": 0}},
	  "state": null
	}`), &doc)
	return doc
}

// writeDiscovery writes game/csgo/readyup/status.json under dir.
func writeDiscovery(t *testing.T, dir string, d map[string]any) {
	t.Helper()
	p := readyUpDiscoveryPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if err := os.WriteFile(p, b, 0o640); err != nil {
		t.Fatal(err)
	}
}

// freePort returns a loopback port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func TestDiscoverReadyUp(t *testing.T) {
	dir := t.TempDir()

	d, err := DiscoverReadyUp(dir, 27015)
	if err != nil || d.FromFile || d.Port != 27022 || d.BaseURL() != "http://127.0.0.1:27022" {
		t.Fatalf("no file: got %+v, %v; want default port 27022", d, err)
	}

	writeDiscovery(t, dir, map[string]any{"port": 28000, "bind": "127.0.0.1", "token": "rst_abc", "pid": 42, "game_port": 27015, "started_at": 1790340000})
	d, err = DiscoverReadyUp(dir, 27015)
	if err != nil || !d.FromFile || d.Port != 28000 || d.Token != "rst_abc" || d.PID != 42 {
		t.Fatalf("with file: got %+v, %v", d, err)
	}

	if err := os.WriteFile(readyUpDiscoveryPath(dir), []byte("{not json"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverReadyUp(dir, 27015); err == nil {
		t.Fatal("broken status.json: want an error, not a silent default")
	}

	writeDiscovery(t, dir, map[string]any{"port": 0})
	if _, err := DiscoverReadyUp(dir, 27015); err == nil {
		t.Fatal("port 0: want an error")
	}
}

func TestReadyUpBaseURL(t *testing.T) {
	for _, tt := range []struct{ bind, want string }{
		{"", "http://127.0.0.1:27022"},
		{"0.0.0.0", "http://127.0.0.1:27022"},
		{"::", "http://127.0.0.1:27022"},
		{"127.0.0.1", "http://127.0.0.1:27022"},
		{"10.0.0.5", "http://10.0.0.5:27022"},
		{"::1", "http://[::1]:27022"},
	} {
		if got := (ReadyUpDiscovery{Port: 27022, Bind: tt.bind}).BaseURL(); got != tt.want {
			t.Errorf("bind %q: %s, want %s", tt.bind, got, tt.want)
		}
	}
}

func TestProbeReadyUpViaDiscoveryFile(t *testing.T) {
	f := newFakeReadyUp(t, liveStatus())
	dir := t.TempDir()
	writeDiscovery(t, dir, map[string]any{"port": f.port(), "bind": "127.0.0.1", "token": "rst_secret"})

	row := ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 1, Dir: dir, GamePort: 27015, Running: true})
	if row.State != ReadyUpOK || row.Status == nil {
		t.Fatalf("state %s err %q, want ok", row.State, row.Err)
	}
	if row.StatusPort != f.port() {
		t.Fatalf("status port %d, want %d from status.json", row.StatusPort, f.port())
	}
	s := row.Status
	if s.Summary.Map != "de_mirage" || s.Summary.Round != 14 || s.Summary.Score.Team1 != 8 || string(s.Summary.MatchID) != "412" {
		t.Fatalf("summary not decoded: %+v", s.Summary)
	}
	if string(s.Versions.CS2Build) != "14090" || string(s.Versions.Core) != "0.9.0" || string(s.Versions.PluginAPI) != "3" {
		t.Fatalf("versions not decoded: %+v", s.Versions)
	}
	if safe, known := row.UpdateSafe(); !known || safe {
		t.Fatalf("UpdateSafe = %v,%v; want false,true", safe, known)
	}
	if auth := f.authSeen(); len(auth) == 0 || auth[0] != "Bearer rst_secret" {
		t.Fatalf("token not sent as Bearer: %v", auth)
	}
}

func TestProbeReadyUpDefaultPort(t *testing.T) {
	// No status.json: csm tries game port + 7.
	f := newFakeReadyUp(t, idleStatus())
	row := ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 2, Dir: t.TempDir(), GamePort: f.port() - ReadyUpPortOffset, Running: true})
	if row.State != ReadyUpOK {
		t.Fatalf("state %s err %q, want ok on the default port", row.State, row.Err)
	}
	if string(row.Status.Versions.CS2Build) != "14090" {
		t.Fatalf("cs2_build as a string: %q", row.Status.Versions.CS2Build)
	}
	if safe, known := row.UpdateSafe(); !known || !safe {
		t.Fatalf("UpdateSafe = %v,%v; want true,true", safe, known)
	}
	if auth := f.authSeen(); auth[0] != "" {
		t.Fatalf("no token known, but sent %q", auth[0])
	}
}

func TestProbeReadyUpWithoutReadyUp(t *testing.T) {
	// A server running the AT CS2 plugin: no status.json, nothing on +7.
	gamePort := freePort(t) - ReadyUpPortOffset
	row := ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 3, Dir: t.TempDir(), GamePort: gamePort, Running: true})
	if row.State != ReadyUpNone {
		t.Fatalf("state %s (%s), want none", row.State, row.Err)
	}
	if _, known := row.UpdateSafe(); known {
		t.Fatal("no Ready Up must not give an update_safe verdict")
	}

	// Something else on +7 that is not Ready Up (404): still "no Ready Up".
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	_, p, _ := net.SplitHostPort(other.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	row = ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 3, Dir: t.TempDir(), GamePort: n - ReadyUpPortOffset, Running: true})
	if row.State != ReadyUpNone {
		t.Fatalf("404 without status.json: state %s, want none", row.State)
	}
}

func TestProbeReadyUpNotAnswering(t *testing.T) {
	// status.json exists, so Ready Up is installed; a failure is "no answer".
	dir := t.TempDir()
	writeDiscovery(t, dir, map[string]any{"port": freePort(t)})
	row := ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 1, Dir: dir, GamePort: 27015, Running: true})
	if row.State != ReadyUpNoResponse || row.Err == "" {
		t.Fatalf("state %s err %q, want no_response with a reason", row.State, row.Err)
	}

	f := newFakeReadyUp(t, liveStatus())
	f.mu.Lock()
	f.statusCode = http.StatusUnauthorized
	f.mu.Unlock()
	writeDiscovery(t, dir, map[string]any{"port": f.port(), "token": "wrong"})
	row = ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 1, Dir: dir, GamePort: 27015, Running: true})
	if row.State != ReadyUpNoResponse || !strings.Contains(row.Err, "token") {
		t.Fatalf("401: state %s err %q", row.State, row.Err)
	}
}

func TestProbeReadyUpStoppedServer(t *testing.T) {
	f := newFakeReadyUp(t, liveStatus())
	dir := t.TempDir()
	writeDiscovery(t, dir, map[string]any{"port": f.port()})
	row := ProbeReadyUp(context.Background(), http.DefaultClient, FleetTarget{Server: 1, Dir: dir, GamePort: 27015, Running: false})
	if row.State != ReadyUpStopped || len(f.authSeen()) != 0 {
		t.Fatalf("stopped server: state %s, %d requests; want stopped and none", row.State, len(f.authSeen()))
	}
}

func TestProbeFleetOrder(t *testing.T) {
	f := newFakeReadyUp(t, idleStatus())
	var targets []FleetTarget
	for i := 5; i >= 1; i-- {
		targets = append(targets, FleetTarget{Server: i, Dir: t.TempDir(), GamePort: f.port() - ReadyUpPortOffset, Running: i%2 == 1})
	}
	rows := ProbeFleet(context.Background(), targets)
	for i, r := range rows {
		if r.Target.Server != i+1 {
			t.Fatalf("row %d is server %d", i, r.Target.Server)
		}
		want := ReadyUpStopped
		if r.Target.Running {
			want = ReadyUpOK
		}
		if r.State != want {
			t.Fatalf("server %d: %s, want %s", r.Target.Server, r.State, want)
		}
	}
}

func TestFlexString(t *testing.T) {
	var v struct {
		A flexString `json:"a"`
		B flexString `json:"b"`
		C flexString `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a": 14090, "b": "1.40", "c": null}`), &v); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(v.A, "|", v.B, "|", v.C); got != "14090|1.40|" {
		t.Fatalf("got %q", got)
	}
}
