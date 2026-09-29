package hostagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// --- schemas ---------------------------------------------------------------------

const schemaBase = "https://auto-tournament.dev/fleet/host/v1/"

var (
	schemaOnce sync.Once
	schemaErr  error
	schemas    map[string]*jsonschema.Schema
)

// schemaDir is protocol/host-v1 at the repository root.
func schemaDir() string {
	return filepath.Join("..", "..", "..", "protocol", "host-v1")
}

func loadSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.Draft = jsonschema.Draft2020
		root := schemaDir()
		var names []string
		schemaErr = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".json") {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := c.AddResource(schemaBase+rel, f); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			names = append(names, rel)
			return nil
		})
		if schemaErr != nil {
			return
		}
		schemas = map[string]*jsonschema.Schema{}
		for _, n := range names {
			s, err := c.Compile(schemaBase + n)
			if err != nil {
				schemaErr = fmt.Errorf("%s: %w", n, err)
				return
			}
			schemas[n] = s
		}
	})
	if schemaErr != nil {
		t.Fatalf("schemas: %v", schemaErr)
	}
	return schemas
}

// validateFrame checks an envelope and its payload against the schemas.
func validateFrame(t *testing.T, data []byte) {
	t.Helper()
	s := loadSchemas(t)
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("frame is not JSON: %v", err)
	}
	if err := s["envelope.json"].Validate(doc); err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, data)
	}
	m := doc.(map[string]any)
	typ := m["type"].(string)
	ps, ok := s["messages/"+typ+".json"]
	if !ok {
		t.Fatalf("no schema for message type %q", typ)
	}
	if err := ps.Validate(m["payload"]); err != nil {
		t.Fatalf("%s payload invalid: %v\n%s", typ, err, data)
	}
}

func validateDoc(t *testing.T, name string, v any) {
	t.Helper()
	s := loadSchemas(t)
	b, _ := json.Marshal(v)
	var doc any
	_ = json.Unmarshal(b, &doc)
	if err := s[name].Validate(doc); err != nil {
		t.Fatalf("%s invalid: %v\n%s", name, err, b)
	}
}

// --- fake backend -------------------------------------------------------------------

type fakeBackend struct {
	mu         sync.Mutex
	root       string
	servers    []ServerState
	calls      []string
	holdMode   string
	logs       map[string]string
	installs   []ReadyUpPlan
	cfgAtStart map[int]string // fleet.cfg content when the server was started by create
}

func newFakeBackend(t *testing.T, n int) *fakeBackend {
	b := &fakeBackend{root: t.TempDir(), logs: map[string]string{}, cfgAtStart: map[int]string{}}
	for i := 1; i <= n; i++ {
		b.addServer(i, true)
	}
	return b
}

func (b *fakeBackend) addServer(n int, running bool) {
	dir := filepath.Join(b.root, ServerName(n))
	_ = os.MkdirAll(filepath.Join(dir, "game", "csgo", "cfg"), 0o755)
	safe := true
	b.servers = append(b.servers, ServerState{
		Number: n, Dir: dir, GamePort: 27015 + (n-1)*10, TVPort: 27020 + (n-1)*10, StatusPort: 27022 + (n-1)*10,
		Running: running, ReadyUpInstalled: "0.4.0", ReadyUpPresent: true, InstallID: fmt.Sprintf("inst-%08d", n),
		Health: "ok", Phase: "idle", UpdateSafe: &safe, CS2Build: 14032,
	})
}

func (b *fakeBackend) setBusy(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := false
	b.servers[n-1].UpdateSafe = &f
	b.servers[n-1].Phase = "live"
	b.servers[n-1].Busy = ServerName(n) + " (live R14, de_mirage, 8-5)"
}

func (b *fakeBackend) call(s string) {
	b.mu.Lock()
	b.calls = append(b.calls, s)
	b.mu.Unlock()
}

func (b *fakeBackend) Calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func (b *fakeBackend) Servers(context.Context) ([]ServerState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ServerState(nil), b.servers...), nil
}

func (b *fakeBackend) Host(context.Context) (HostFacts, error) {
	return HostFacts{Hostname: "cs2-box", OS: "linux test", Resources: Resources{CPUs: 8, Load1: 0.5, RAMMB: 32000, RAMFreeMB: 16000,
		Disk: []DiskInfo{{Mount: "/", TotalGB: 500, FreeGB: 200}}}, CS2: CS2Facts{MasterBuild: 14032, UpdatesHold: "auto"}}, nil
}

func (b *fakeBackend) setRunning(n int, r bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.servers[n-1].Running = r
}

func (b *fakeBackend) Start(_ context.Context, n int, mode string) error {
	b.call(fmt.Sprintf("start %d %s", n, mode))
	b.setRunning(n, true)
	return nil
}

func (b *fakeBackend) Stop(_ context.Context, n, grace int) error {
	b.call(fmt.Sprintf("stop %d %d", n, grace))
	b.setRunning(n, false)
	return nil
}

func (b *fakeBackend) Restart(_ context.Context, n int) error {
	b.call(fmt.Sprintf("restart %d", n))
	return nil
}

func (b *fakeBackend) CreateServer(_ context.Context, beforeStart func(string) error) (int, string, error) {
	b.mu.Lock()
	n := len(b.servers) + 1
	b.addServer(n, false)
	dir := b.servers[n-1].Dir
	b.mu.Unlock()
	if err := beforeStart(dir); err != nil {
		return 0, "copy done\n", err
	}
	data, _ := os.ReadFile(FleetCfgPath(dir))
	b.mu.Lock()
	b.cfgAtStart[n] = string(data)
	b.servers[n-1].Running = true
	b.mu.Unlock()
	b.call(fmt.Sprintf("create %d", n))
	return n, "[✓] Server-" + fmt.Sprint(n) + " ready\n", nil
}

func (b *fakeBackend) RemoveLastServer(context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.servers)
	b.servers = b.servers[:n-1]
	b.calls = append(b.calls, fmt.Sprintf("remove %d", n))
	return "removed\n", nil
}

func (b *fakeBackend) UpdateGame(_ context.Context, servers []int, progress func(string)) (string, error) {
	progress("steamcmd")
	b.call(fmt.Sprintf("update_game %v", servers))
	return "updated\n", nil
}

func (b *fakeBackend) InstallReadyUp(_ context.Context, n int, plan ReadyUpPlan) (string, error) {
	b.mu.Lock()
	b.installs = append(b.installs, plan)
	b.mu.Unlock()
	b.call(fmt.Sprintf("install %d %s", n, plan.Component))
	return "installed\n", nil
}

func (b *fakeBackend) SetUpdatesHold(mode string) error {
	b.mu.Lock()
	b.holdMode = mode
	b.mu.Unlock()
	b.call("hold " + mode)
	return nil
}

func (b *fakeBackend) LogFile(n int, source string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.logs[fmt.Sprintf("%d/%s", n, source)]
	if !ok {
		return "", fmt.Errorf("no %s log", source)
	}
	return p, nil
}

func (b *fakeBackend) ChownToCS2User(string) error { return nil }

// --- fake platform ------------------------------------------------------------------

type frame struct {
	env Envelope
	raw []byte
}

// fakePlatform speaks the host side of the gateway, enough for the agent:
// it checks the bearer token, validates every frame against the schemas,
// answers hello with welcome and records everything.
type fakePlatform struct {
	t     *testing.T
	srv   *httptest.Server
	token string

	mu         sync.Mutex
	frames     []frame
	conns      []*websocket.Conn
	hellos     []HelloPayload
	txSeq      int64
	resume     string // "resumed" (default) or "reset"
	platRx     int64  // what welcome says we received
	cond       *sync.Cond
	rejectWith int // close code instead of welcome
	noAutoAck  bool
}

func newFakePlatform(t *testing.T, token string) *fakePlatform {
	p := &fakePlatform{t: t, token: token, resume: "resumed"}
	p.cond = sync.NewCond(&p.mu)
	up := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc(HostWSPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+p.currentToken() {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.RawQuery, "rhs_") {
			t.Errorf("token in the URL")
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		p.serve(c)
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakePlatform) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

func (p *fakePlatform) wsURL() string {
	return "ws" + strings.TrimPrefix(p.srv.URL, "http") + HostWSPath
}

func (p *fakePlatform) serve(c *websocket.Conn) {
	p.mu.Lock()
	p.conns = append(p.conns, c)
	p.mu.Unlock()
	defer c.Close()
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
			return
		}
		if mt != websocket.TextMessage {
			p.t.Errorf("binary frame")
			return
		}
		validateFrame(p.t, data)
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			p.t.Errorf("bad frame: %v", err)
			return
		}
		p.mu.Lock()
		p.frames = append(p.frames, frame{env: env, raw: data})
		reject := p.rejectWith
		resume, platRx := p.resume, p.platRx
		p.cond.Broadcast()
		p.mu.Unlock()

		if env.Type == TypeHello {
			var h HelloPayload
			_ = json.Unmarshal(env.Payload, &h)
			p.mu.Lock()
			p.hellos = append(p.hellos, h)
			p.mu.Unlock()
			if reject != 0 {
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(reject, "rejected"), time.Now().Add(time.Second))
				return
			}
			w := map[string]any{
				"session_id": NewULID(), "protocol": 1,
				"heartbeat": map[string]any{"interval_ms": 10000, "timeout_ms": 30000},
				"resume":    map[string]any{"result": resume, "platform_last_rx_seq": platRx},
				// server-channel fields a shared gateway might add: ignored.
				"server_config_rev": 0, "admins_rev": 0, "assignment": nil,
			}
			p.write(c, TypeWelcome, w, env.ID, 0)
		}
	}
}

func (p *fakePlatform) write(c *websocket.Conn, typ string, payload any, ref string, seq int64) string {
	env := map[string]any{"v": 1, "type": typ, "id": NewULID(), "ts": time.Now().UnixMilli(), "payload": payload}
	if ref != "" {
		env["ref"] = ref
	}
	if seq > 0 {
		env["seq"] = seq
	}
	b, _ := json.Marshal(env)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
		p.t.Logf("write: %v", err)
	}
	return env["id"].(string)
}

func (p *fakePlatform) conn() *websocket.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.conns) == 0 {
		return nil
	}
	return p.conns[len(p.conns)-1]
}

// send sends a reliable command and returns its id.
func (p *fakePlatform) send(typ string, payload any) string {
	p.mu.Lock()
	p.txSeq++
	seq := p.txSeq
	p.mu.Unlock()
	return p.write(p.conn(), typ, payload, "", seq)
}

// sendRaw sends a prepared envelope map.
func (p *fakePlatform) sendRaw(env map[string]any) {
	b, _ := json.Marshal(env)
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conns[len(p.conns)-1].WriteMessage(websocket.TextMessage, b)
}

// wait blocks until a frame matches, or fails the test.
func (p *fakePlatform) wait(what string, match func(f frame) bool) frame {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	go func() {
		time.Sleep(5 * time.Second)
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		for _, f := range p.frames {
			if match(f) {
				return f
			}
		}
		if time.Now().After(deadline) {
			types := []string{}
			for _, f := range p.frames {
				types = append(types, f.env.Type)
			}
			p.t.Fatalf("timed out waiting for %s; got %v", what, types)
		}
		p.cond.Wait()
	}
}

func (p *fakePlatform) count(match func(f frame) bool) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.countLocked(match)
}

// countLocked is count for use inside a wait predicate (p.mu held).
func (p *fakePlatform) countLocked(match func(f frame) bool) int {
	n := 0
	for _, f := range p.frames {
		if match(f) {
			n++
		}
	}
	return n
}

func (p *fakePlatform) result(ref string) ResultPayload {
	p.t.Helper()
	f := p.wait("host.result for "+ref, func(f frame) bool {
		return f.env.Type == TypeResult && f.env.Ref != nil && *f.env.Ref == ref
	})
	var r ResultPayload
	_ = json.Unmarshal(f.env.Payload, &r)
	return r
}

func isType(typ string) func(frame) bool {
	return func(f frame) bool { return f.env.Type == typ }
}

// --- agent under test -----------------------------------------------------------------

const testToken = "rhs_abcdefghijkl_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const testKey = "rfk_abcdefghijkl_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

type testAgent struct {
	a      *Agent
	b      *fakeBackend
	p      *fakePlatform
	paths  Paths
	cancel context.CancelFunc
	done   chan struct{}
	logs   *logSink
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logSink) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func startAgent(t *testing.T, servers int, fleetKey string, mutate func(*Options)) *testAgent {
	t.Helper()
	p := newFakePlatform(t, testToken)
	b := newFakeBackend(t, servers)
	paths := Paths{Dir: filepath.Join(t.TempDir(), "fleet")}
	creds := &Credentials{
		PlatformURL: strings.Replace(p.srv.URL, "127.0.0.1", "localhost", 1), WSURL: p.wsURL(), HostID: "host_1", TenantID: "default",
		Token: testToken, FleetKey: fleetKey, MachineID: "0123456789abcdef0123456789abcdef", EnrolledAt: nowRFC3339(), InsecureDev: true,
	}
	if err := SaveCredentials(paths, creds); err != nil {
		t.Fatal(err)
	}
	sink := &logSink{}
	o := Options{
		Paths: paths, Backend: b, CSMVersion: "1.11.0", OS: "linux test", Hostname: "cs2-box", Logf: sink.logf,
		HealthEvery: time.Hour, BackoffBase: 10 * time.Millisecond, BackoffCap: 50 * time.Millisecond, NotLinkedWait: 20 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&o)
	}
	a := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	ta := &testAgent{a: a, b: b, p: p, paths: paths, cancel: cancel, done: make(chan struct{}), logs: sink}
	go func() {
		_ = a.Run(ctx)
		close(ta.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-ta.done:
		case <-time.After(5 * time.Second):
			t.Errorf("agent did not stop")
		}
		if strings.Contains(sink.all(), testToken) || strings.Contains(sink.all(), testKey) {
			t.Errorf("a secret reached the log:\n%s", sink.all())
		}
	})
	p.wait("welcome processed (inventory)", isType(TypeInventory))
	return ta
}
