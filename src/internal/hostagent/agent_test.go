package hostagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHelloAndInventoryOnConnect(t *testing.T) {
	ta := startAgent(t, 2, "", nil)
	h := ta.p.wait("hello", isType(TypeHello))
	var hello HelloPayload
	_ = json.Unmarshal(h.env.Payload, &hello)
	if hello.HostID != "host_1" || hello.TenantID != "default" || hello.Versions.CSM != "1.11.0" {
		t.Fatalf("hello = %+v", hello)
	}
	if hello.Stream.LastRxSeq != 0 || hello.Stream.ID == "" {
		t.Fatalf("stream = %+v", hello.Stream)
	}
	if h.env.Seq != nil {
		t.Fatalf("hello must be ephemeral")
	}
	inv := ta.p.wait("inventory", isType(TypeInventory))
	var got Inventory
	_ = json.Unmarshal(inv.env.Payload, &got)
	if len(got.Servers) != 2 || got.Servers[1].Name != "server-2" || got.Servers[0].ReadyUp.InstallID != "inst-00000001" {
		t.Fatalf("inventory servers = %+v", got.Servers)
	}
}

func TestServersListRepliesInventoryAndResult(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeServersList, map[string]any{})
	ta.p.wait("inventory with ref", func(f frame) bool {
		return f.env.Type == TypeInventory && f.env.Ref != nil && *f.env.Ref == id
	})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("result = %+v", r)
	}
}

func TestRestartRefusedDuringMatchUnlessForced(t *testing.T) {
	ta := startAgent(t, 2, "", nil)
	ta.b.setBusy(2)

	id := ta.p.send(TypeServerRestart, map[string]any{"server": "server-2", "reason": "stuck"})
	r := ta.p.result(id)
	if r.Status != StatusRejected || r.Error == nil || r.Error.Code != CodeMatchInProgress {
		t.Fatalf("result = %+v", r)
	}
	if !strings.Contains(r.Error.Message, "live R14") {
		t.Fatalf("message should describe the match: %q", r.Error.Message)
	}
	for _, c := range ta.b.Calls() {
		if strings.HasPrefix(c, "restart") {
			t.Fatalf("restart ran without force: %v", ta.b.Calls())
		}
	}

	id = ta.p.send(TypeServerRestart, map[string]any{"server": "server-2", "reason": "hung", "force": map[string]any{"by": "u_1", "reason": "failover"}})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("forced result = %+v", r)
	}
	ta.p.wait("health restarted", func(f frame) bool {
		return f.env.Type == TypeHealth && strings.Contains(string(f.env.Payload), `"restarted"`)
	})
	if !strings.Contains(ta.logs.all(), "FORCED server.restart") {
		t.Fatalf("forced action not logged:\n%s", ta.logs.all())
	}
	// A safe server restarts without force.
	id = ta.p.send(TypeServerRestart, map[string]any{"server": "server-1", "reason": "x"})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("result = %+v", r)
	}
}

func TestStopStartAndUnknownServer(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeServerStop, map[string]any{"server": "server-1", "grace_s": 3})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("stop = %+v", r)
	}
	id = ta.p.send(TypeServerStart, map[string]any{"server": "server-1", "launch_mode": "alternate"})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("start = %+v", r)
	}
	calls := strings.Join(ta.b.Calls(), ",")
	if !strings.Contains(calls, "stop 1 3") || !strings.Contains(calls, "start 1 alternate") {
		t.Fatalf("calls = %s", calls)
	}
	id = ta.p.send(TypeServerStart, map[string]any{"server": "server-9"})
	if r := ta.p.result(id); r.Status != StatusRejected || r.Error.Code != CodeUnknownServer {
		t.Fatalf("unknown server = %+v", r)
	}
}

func TestInvalidPayloadUnknownTypeAndAcks(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeServerStart, map[string]any{"server": "../etc"})
	if r := ta.p.result(id); r.Status != StatusRejected || r.Error.Code != CodeBadArgs {
		t.Fatalf("bad server name = %+v", r)
	}
	id = ta.p.send(TypeServerStop, map[string]any{"server": 5})
	if r := ta.p.result(id); r.Status != StatusRejected || r.Error.Code != CodeBadArgs {
		t.Fatalf("wrong type = %+v", r)
	}
	id = ta.p.send("host.frobnicate", map[string]any{})
	ta.p.wait("unknown_type error", func(f frame) bool {
		return f.env.Type == TypeError && f.env.Ref != nil && *f.env.Ref == id && strings.Contains(string(f.env.Payload), "unknown_type")
	})
	// Three reliable messages: the agent acks seq 3 (piggybacked or alone).
	ta.p.wait("ack 3", func(f frame) bool { return f.env.Ack != nil && *f.env.Ack == 3 })
	// Nothing is executed for them.
	if calls := ta.b.Calls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestExpiredAndStaleCommandsAreNotRun(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeServerStop, map[string]any{"server": "server-1", "expires_at": time.Now().Add(-time.Minute).UnixMilli()})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeExpired {
		t.Fatalf("expired = %+v", r)
	}
	ta.p.mu.Lock()
	ta.p.txSeq++
	seq := ta.p.txSeq
	ta.p.mu.Unlock()
	old := NewULID()
	ta.p.sendRaw(map[string]any{"v": 1, "type": TypeServerStop, "id": old, "seq": seq, "ts": time.Now().Add(-time.Hour).UnixMilli(),
		"payload": map[string]any{"server": "server-1"}})
	if r := ta.p.result(old); r.Error == nil || r.Error.Code != CodeStale {
		t.Fatalf("stale = %+v", r)
	}
	if calls := ta.b.Calls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestDuplicateAndOutOfOrderReliable(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeUpdatesHold, map[string]any{"mode": "on"})
	ta.p.result(id)
	// Replay seq 1 (a lost ack): acked again, not run again.
	ta.p.sendRaw(map[string]any{"v": 1, "type": TypeUpdatesHold, "id": NewULID(), "seq": 1, "ts": time.Now().UnixMilli(), "payload": map[string]any{"mode": "off"}})
	// Seq 5 after 1: dropped unacked.
	gap := NewULID()
	ta.p.sendRaw(map[string]any{"v": 1, "type": TypeUpdatesHold, "id": gap, "seq": 5, "ts": time.Now().UnixMilli(), "payload": map[string]any{"mode": "off"}})
	time.Sleep(300 * time.Millisecond)
	calls := ta.b.Calls()
	if len(calls) != 1 || calls[0] != "hold on" {
		t.Fatalf("calls = %v", calls)
	}
	if n := ta.p.count(func(f frame) bool { return f.env.Ref != nil && *f.env.Ref == gap }); n != 0 {
		t.Fatalf("out-of-order message was answered")
	}
	st := loadState(ta.paths, "host_1")
	if st.LastRxSeq != 1 {
		t.Fatalf("persisted rx seq = %d", st.LastRxSeq)
	}
}

func TestCreateWritesFleetCfgBeforeStart(t *testing.T) {
	ta := startAgent(t, 2, testKey, nil)
	id := ta.p.send(TypeServerCreate, map[string]any{"enroll": true})
	r := ta.p.result(id)
	if r.Status != StatusOK || !strings.Contains(r.Output, "server-3") {
		t.Fatalf("create = %+v", r)
	}
	ta.p.wait("progress", func(f frame) bool { return f.env.Type == TypeProgress })
	cfg := ta.b.cfgAtStart[3]
	if !strings.Contains(cfg, "enroll_key = "+testKey) || !strings.Contains(cfg, "url = http://localhost:") || !strings.Contains(cfg, "insecure_dev = 1") {
		t.Fatalf("fleet.cfg at start:\n%s", cfg)
	}
	if strings.Contains(r.Output, testKey) {
		t.Fatalf("the key leaked into the result")
	}
	if fi, err := os.Stat(FleetCfgPath(filepath.Join(ta.b.root, "server-3"))); err != nil || (fi.Mode().Perm()&0o077 != 0 && !isWindows()) {
		t.Fatalf("fleet.cfg mode: %v %v", fi, err)
	}
}

func TestCreateEnrollNeedsAKey(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeServerCreate, map[string]any{"enroll": true})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeNoEnrollKey {
		t.Fatalf("create = %+v", r)
	}
	// With the key in the message it works.
	id = ta.p.send(TypeServerCreate, map[string]any{"enroll": true, "enroll_key": testKey})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("create = %+v", r)
	}
	if !strings.Contains(ta.b.cfgAtStart[2], testKey) {
		t.Fatalf("fleet.cfg = %q", ta.b.cfgAtStart[2])
	}
	// Without enroll nothing is written.
	id = ta.p.send(TypeServerCreate, map[string]any{"enroll": false, "game_port": 27035})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("create = %+v", r)
	}
	if ta.b.cfgAtStart[3] != "" {
		t.Fatalf("fleet.cfg written without enroll")
	}
	id = ta.p.send(TypeServerCreate, map[string]any{"enroll": false, "game_port": 30000})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeUnsupported {
		t.Fatalf("odd port = %+v", r)
	}
}

func TestUpdatePluginsWithoutReleaseFails(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer gh.Close()
	ta := startAgent(t, 1, "", func(o *Options) { o.Fetcher = &Fetcher{GitHubAPI: gh.URL} })
	id := ta.p.send(TypeUpdatePlugins, map[string]any{"readyup": map[string]any{"version": "0.4.0", "bundle": "default"}})
	r := ta.p.result(id)
	if r.Status != StatusFailed || r.Error.Code != CodeNoRelease || !strings.Contains(r.Error.Message, "readyup_bundle") {
		t.Fatalf("result = %+v", r)
	}
	for _, c := range ta.b.Calls() {
		if strings.HasPrefix(c, "install") || strings.HasPrefix(c, "stop") {
			t.Fatalf("something ran: %v", ta.b.Calls())
		}
	}
}

func TestUpdatePluginsFromConfiguredBundle(t *testing.T) {
	ta := startAgent(t, 2, "", nil)
	dir := t.TempDir()
	zip := filepath.Join(dir, "ready-up-full.zip")
	inst := filepath.Join(dir, "install.sh")
	_ = os.WriteFile(zip, []byte("PK"), 0o644)
	_ = os.WriteFile(inst, []byte("#!/bin/sh\n"), 0o755)
	if _, err := SetConfig(ta.paths, "readyup_bundle", abs(zip)); err != nil {
		t.Fatal(err)
	}
	if _, err := SetConfig(ta.paths, "readyup_installer", abs(inst)); err != nil {
		t.Fatal(err)
	}
	ta.b.setBusy(2)
	id := ta.p.send(TypeUpdatePlugins, map[string]any{"readyup": map[string]any{"version": "latest", "bundle": "skins"}})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeMatchInProgress {
		t.Fatalf("busy = %+v", r)
	}
	id = ta.p.send(TypeUpdatePlugins, map[string]any{"servers": []string{"server-1"}, "readyup": map[string]any{"version": "latest", "bundle": "skins"}})
	r := ta.p.result(id)
	if r.Status != StatusOK {
		t.Fatalf("result = %+v", r)
	}
	calls := strings.Join(ta.b.Calls(), ",")
	if calls != "stop 1 10,install 1 full,start 1 " {
		t.Fatalf("calls = %s", calls)
	}
	if ta.b.installs[0].Zip != abs(zip) || ta.b.installs[0].Installer != abs(inst) {
		t.Fatalf("plan = %+v", ta.b.installs[0])
	}
}

func TestAuthRotateWritesTokenAndReplies(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	newTok := "rhs_abcdefghijkl_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	id := ta.p.send(TypeAuthRotate, map[string]any{"token": newTok, "old_valid_until": time.Now().Add(24 * time.Hour).UnixMilli()})
	f := ta.p.wait("auth.rotated", isType(TypeAuthRotated))
	if f.env.Seq == nil || f.env.Ref == nil || *f.env.Ref != id {
		t.Fatalf("auth.rotated must be reliable with ref: %s", f.raw)
	}
	c, err := LoadCredentials(ta.paths)
	if err != nil || c.Token != newTok {
		t.Fatalf("stored token not rotated: %v", err)
	}
	// The next connect uses the new token.
	ta.p.mu.Lock()
	ta.p.token = newTok
	ta.p.mu.Unlock()
	_ = ta.p.conn().Close()
	ta.p.wait("second hello", func(f frame) bool { return ta.p.countLocked(isType(TypeHello)) >= 2 })
	if strings.Contains(ta.logs.all(), "CCCCCCCC") {
		t.Fatalf("new token logged")
	}
}

func TestReconnectResumesAndReplaysUnacked(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	id := ta.p.send(TypeUpdatesHold, map[string]any{"mode": "auto"})
	first := ta.p.wait("result", func(f frame) bool { return f.env.Type == TypeResult && *f.env.Ref == id })
	// The platform never acks and drops the link: the result is replayed
	// with the same seq and id after the reconnect.
	_ = ta.p.conn().Close()
	ta.p.wait("replayed result", func(f frame) bool {
		return f.env.Type == TypeResult && f.env.ID == first.env.ID && f.env.TS >= 0 && ta.p.countLocked(isType(TypeHello)) >= 2 && ta.p.countLocked(func(g frame) bool { return g.env.ID == first.env.ID }) >= 2
	})
	ta.p.mu.Lock()
	h := ta.p.hellos[len(ta.p.hellos)-1]
	ta.p.mu.Unlock()
	if h.Stream.LastRxSeq != 1 || h.Stream.LastTxSeq < 1 {
		t.Fatalf("second hello stream = %+v", h.Stream)
	}
	// Commands are not run twice.
	if n := len(ta.b.Calls()); n != 1 {
		t.Fatalf("calls = %v", ta.b.Calls())
	}
}

func TestRevokedTokenBacksOffAndReenrollsWithKey(t *testing.T) {
	enrolls := 0
	var ta *testAgent
	enrollSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		validateDoc(t, "http/enroll.request.json", req)
		enrolls++
		ta.p.mu.Lock()
		ta.p.token = "rhs_abcdefghijkl_DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
		ta.p.rejectWith = 0
		ta.p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "host_id": "host_1", "tenant_id": "default",
			"token": "rhs_abcdefghijkl_DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", "ws_url": ta.p.wsURL()})
	}))
	defer enrollSrv.Close()
	ta = startAgent(t, 1, testKey, nil)
	c, _ := LoadCredentials(ta.paths)
	c.PlatformURL = enrollSrv.URL
	_ = SaveCredentials(ta.paths, c)
	ta.p.mu.Lock()
	ta.p.rejectWith = CloseRevoked
	ta.p.mu.Unlock()
	_ = ta.p.conn().Close()
	ta.p.wait("hello after re-enroll", func(f frame) bool { return enrolls == 1 && ta.p.countLocked(isType(TypeHello)) >= 3 })
	c, _ = LoadCredentials(ta.paths)
	if !strings.HasPrefix(c.Token, "rhs_abcdefghijkl_DDDD") || c.FleetKey != testKey {
		t.Fatalf("credentials after re-enroll: host %s key kept %v", c.HostID, c.FleetKey == testKey)
	}
}

func TestLogsTail(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	logPath := filepath.Join(t.TempDir(), "server-1.log")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
	}
	b.WriteString("token rhs_abcdefghijkl_EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE here\n")
	_ = os.WriteFile(logPath, []byte(b.String()), 0o644)
	ta.b.mu.Lock()
	ta.b.logs["1/console"] = logPath
	ta.b.mu.Unlock()

	id := ta.p.send(TypeLogsTail, map[string]any{"server": "server-1", "source": "console", "lines": 5})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("tail = %+v", r)
	}
	f := ta.p.wait("chunk eof", func(f frame) bool {
		return f.env.Type == TypeLogsChunk && strings.Contains(string(f.env.Payload), `"eof":true`)
	})
	var ch LogsChunkPayload
	_ = json.Unmarshal(f.env.Payload, &ch)
	if ch.StreamID != id || len(ch.Lines) != 5 || !strings.Contains(ch.Lines[4], "rhs_…") || strings.Contains(string(f.raw), "EEEE") {
		t.Fatalf("chunk = %+v", ch)
	}

	// Follow: new lines arrive, logs.stop ends the stream with eof.
	id = ta.p.send(TypeLogsTail, map[string]any{"server": "server-1", "source": "console", "lines": 0, "follow": true, "max_s": 60})
	ta.p.result(id)
	fh, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = fh.WriteString("fresh line\n")
	fh.Close()
	ta.p.wait("followed line", func(f frame) bool {
		return f.env.Type == TypeLogsChunk && strings.Contains(string(f.env.Payload), "fresh line")
	})
	sid := id
	stop := ta.p.send(TypeLogsStop, map[string]any{"stream_id": sid})
	if r := ta.p.result(stop); r.Status != StatusOK {
		t.Fatalf("stop = %+v", r)
	}
	ta.p.wait("eof of the followed stream", func(f frame) bool {
		return f.env.Type == TypeLogsChunk && strings.Contains(string(f.env.Payload), sid) && strings.Contains(string(f.env.Payload), `"eof":true`)
	})

	id = ta.p.send(TypeLogsTail, map[string]any{"source": "monitor"})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeNotFound {
		t.Fatalf("missing log = %+v", r)
	}
}

func TestBusyWhileHeavyJobRuns(t *testing.T) {
	ta := startAgent(t, 1, "", nil)
	release, bad := ta.a.exclusive(TypeUpdateGame)
	if bad != nil {
		t.Fatal(bad)
	}
	id := ta.p.send(TypeServerRestart, map[string]any{"server": "server-1", "reason": "x"})
	r := ta.p.result(id)
	release()
	if r.Error == nil || r.Error.Code != CodeBusy || !strings.Contains(r.Error.Message, "host.update_game") {
		t.Fatalf("result = %+v", r)
	}
}

func TestUpdateGameGatedAndRuns(t *testing.T) {
	ta := startAgent(t, 3, "", nil)
	ta.b.setBusy(3)
	id := ta.p.send(TypeUpdateGame, map[string]any{})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeMatchInProgress {
		t.Fatalf("all = %+v", r)
	}
	id = ta.p.send(TypeUpdateGame, map[string]any{"servers": []string{"server-1", "server-2"}})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("subset = %+v", r)
	}
	if calls := strings.Join(ta.b.Calls(), ","); calls != "update_game [1 2]" {
		t.Fatalf("calls = %s", calls)
	}
}

func TestRemoveOnlyLastServer(t *testing.T) {
	ta := startAgent(t, 3, "", nil)
	id := ta.p.send(TypeServerRemove, map[string]any{"server": "server-2"})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeUnsupported {
		t.Fatalf("middle = %+v", r)
	}
	id = ta.p.send(TypeServerRemove, map[string]any{"server": "server-3"})
	if r := ta.p.result(id); r.Status != StatusOK {
		t.Fatalf("last = %+v", r)
	}
}

func TestNotLinkedWaitsForCredentials(t *testing.T) {
	p := newFakePlatform(t, testToken)
	paths := Paths{Dir: filepath.Join(t.TempDir(), "fleet")}
	sink := &logSink{}
	a := New(Options{Paths: paths, Backend: newFakeBackend(t, 1), CSMVersion: "1", Hostname: "h", Logf: sink.logf,
		HealthEvery: time.Hour, NotLinkedWait: 20 * time.Millisecond, BackoffBase: 10 * time.Millisecond, BackoffCap: 20 * time.Millisecond})
	ctx, cancel := contextWithCancel()
	defer cancel()
	go func() { _ = a.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(sink.all(), "not linked") {
		t.Fatalf("log = %s", sink.all())
	}
	_ = SaveCredentials(paths, &Credentials{PlatformURL: "http://localhost:1", WSURL: p.wsURL(), HostID: "host_9", Token: testToken,
		MachineID: "0123456789abcdef0123456789abcdef", InsecureDev: true})
	p.wait("hello once linked", isType(TypeHello))
}

func isWindows() bool { return runtime.GOOS == "windows" }

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
