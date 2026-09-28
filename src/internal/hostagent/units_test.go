package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestULID(t *testing.T) {
	seen := map[string]bool{}
	var ids []string
	for i := 0; i < 2000; i++ {
		id := NewULID()
		if !ulidRe.MatchString(id) {
			t.Fatalf("not a ULID: %s", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("ULIDs from one process must sort in creation order")
	}
	// The time prefix decodes to now.
	var ms int64
	for _, c := range ids[0][:10] {
		ms = ms<<5 | int64(strings.IndexRune(crockford, c))
	}
	if d := time.Since(time.UnixMilli(ms)); d < 0 || d > time.Minute {
		t.Fatalf("time prefix off by %s", d)
	}
}

func TestParseEnvelope(t *testing.T) {
	good := `{"v":1,"type":"server.start","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","seq":3,"ts":1,"ref":null,"payload":{"server":"server-1"}}`
	env, err := ParseEnvelope([]byte(good))
	if err != nil || env.Type != TypeServerStart || *env.Seq != 3 || env.Ref != nil {
		t.Fatalf("good: %+v %v", env, err)
	}
	bad := map[string]string{
		"v2":          `{"v":2,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{}}`,
		"no ts":       `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","payload":{}}`,
		"extra field": `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{},"token":"x"}`,
		"bad type":    `{"v":1,"type":"Server.Start","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{}}`,
		"bad id":      `{"v":1,"type":"ping","id":"nope","ts":1,"payload":{}}`,
		"seq 0":       `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","seq":0,"ts":1,"payload":{}}`,
		"array":       `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":[]}`,
		"trailing":    `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{}} {}`,
		"not json":    `hello`,
	}
	for name, b := range bad {
		if _, err := ParseEnvelope([]byte(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	big := `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{"x":"` + strings.Repeat("a", MaxFrameBytes) + `"}}`
	if _, err := ParseEnvelope([]byte(big)); err == nil {
		t.Errorf("oversized frame accepted")
	}
}

func TestRedact(t *testing.T) {
	in := "tok rhs_abcdefghijkl_" + strings.Repeat("A", 43) + " key rfk_abcdefghijkl_" + strings.Repeat("B", 43) +
		" srv rus_abcdefghijkl_" + strings.Repeat("C", 43) + " code RUE-7F3K-9QX2-LM4D-P8TW status rst_0123456789abcdef"
	out := Redact(in)
	for _, s := range []string{"AAAA", "BBBB", "CCCC", "7F3K", "0123456789abcdef"} {
		if strings.Contains(out, s) {
			t.Fatalf("%q survived: %s", s, out)
		}
	}
	if !strings.Contains(out, "rhs_…") || !strings.Contains(out, "RUE-…") {
		t.Fatalf("markers missing: %s", out)
	}
}

func TestSchemasRejectBadFrames(t *testing.T) {
	s := loadSchemas(t)
	check := func(name, doc string, want bool) {
		t.Helper()
		var v any
		if err := json.Unmarshal([]byte(doc), &v); err != nil {
			t.Fatal(err)
		}
		err := s[name].Validate(v)
		if (err == nil) != want {
			t.Errorf("%s %s: valid=%v, want %v (%v)", name, doc, err == nil, want, err)
		}
	}
	check("messages/host.result.json", `{"status":"ok"}`, true)
	check("messages/host.result.json", `{"status":"ok","error":{"code":"x"}}`, false)
	check("messages/host.result.json", `{"status":"rejected"}`, false)
	check("messages/host.result.json", `{"status":"rejected","error":{"code":"match_in_progress"}}`, true)
	check("messages/server.start.json", `{"server":"server-0"}`, false)
	check("messages/server.start.json", `{"server":"server-12"}`, true)
	check("messages/server.restart.json", `{"server":"server-1"}`, false)
	check("messages/host.update_plugins.json", `{"readyup":{"version":"0.4.0","bundle":"default"}}`, true)
	check("messages/host.update_plugins.json", `{"readyup":{"version":"; rm -rf /","bundle":"default"}}`, false)
	check("http/enroll.request.json", `{"kind":"host","code":"RUE-1","key":"x","machine_id":"0123456789abcdef0123456789abcdef","hostname":"h","os":"l","csm_version":"1"}`, false)
	check("envelope.json", `{"v":1,"type":"ping","id":"01J8ZQ4T8W6N3X0F2R5K7M9P1C","ts":1,"payload":{},"x":1}`, false)
}

func TestCheckURL(t *testing.T) {
	cases := []struct {
		url      string
		insecure bool
		ok       bool
	}{
		{"https://at.example.com", false, true},
		{"http://at.example.com", false, false},
		{"http://at.example.com", true, false},
		{"http://127.0.0.1:3000", true, true},
		{"http://192.168.50.10", true, true},
		{"http://localhost:3000", false, false},
		{"https://user:pw@at.example.com", false, false},
		{"https://at.example.com/?token=x", false, false},
		{"ftp://at.example.com", true, false},
		{"not a url", false, false},
	}
	for _, c := range cases {
		_, err := CheckURL(c.url, c.insecure, "https", "http")
		if (err == nil) != c.ok {
			t.Errorf("%s insecure=%v: err=%v", c.url, c.insecure, err)
		}
	}
	u, _ := CheckURL("https://at.example.com/base/", false, "https", "http")
	if got := DefaultWSURL(u); got != "wss://at.example.com/base/api/fleet/host" {
		t.Fatalf("ws url = %s", got)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	p := Paths{Dir: filepath.Join(t.TempDir(), "fleet")}
	if _, err := LoadCredentials(p); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("missing = %v", err)
	}
	c := &Credentials{PlatformURL: "https://at.example.com", WSURL: "wss://at.example.com/api/fleet/host", HostID: "h1",
		Token: testToken, FleetKey: testKey, MachineID: "0123456789abcdef0123456789abcdef"}
	if err := SaveCredentials(p, c); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.Credentials())
	if err != nil {
		t.Fatal(err)
	}
	if !isWindows() && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	got, err := LoadCredentials(p)
	if err != nil || got.Token != testToken || got.FleetKey != testKey {
		t.Fatalf("load = %+v %v", got, err)
	}
	bad := *c
	bad.Token = "rus_abcdefghijkl_" + strings.Repeat("A", 43)
	if err := SaveCredentials(p, &bad); err == nil {
		t.Fatalf("a server token was accepted as a host token")
	}
	if err := RemoveCredentials(p); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(p); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("after remove = %v", err)
	}
}

func TestMachineIDIsHashed(t *testing.T) {
	raw := "4c4c4544003610428057b4c04f4b4d32"
	id, err := MachineID(func(p string) ([]byte, error) {
		if p == "/etc/machine-id" {
			return []byte(raw + "\n"), nil
		}
		return nil, os.ErrNotExist
	})
	if err != nil || len(id) != 32 || id == raw || strings.Contains(id, raw[:8]) {
		t.Fatalf("id = %q %v", id, err)
	}
	again, _ := MachineID(func(string) ([]byte, error) { return []byte(raw), nil })
	if again != id {
		t.Fatalf("not stable")
	}
	if _, err := MachineID(func(string) ([]byte, error) { return nil, os.ErrNotExist }); err == nil {
		t.Fatalf("no machine id accepted")
	}
}

func TestEnroll(t *testing.T) {
	var got map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EnrollPath || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		switch got["code"] {
		case "RUE-BAD0-BAD0-BAD0-BAD0":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"code":"invalid_code","error":"Invalid enrollment code"}`))
			return
		}
		resp := map[string]any{"success": true, "host_id": "host_7", "tenant_id": "default", "token": testToken, "reenrolled": false}
		validateDoc(t, "http/enroll.response.json", resp)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	opts := EnrollOptions{PlatformURL: srv.URL + "/", MachineID: "0123456789abcdef0123456789abcdef", Hostname: "box", OS: "linux", CSMVersion: "1.11.0", HTTPClient: srv.Client()}

	opts.CodeOrKey = "RUE-7F3K-9QX2-LM4D-P8TW"
	c, err := Enroll(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	validateDoc(t, "http/enroll.request.json", got)
	if got["kind"] != "host" || got["code"] != "RUE-7F3K-9QX2-LM4D-P8TW" || got["key"] != nil {
		t.Fatalf("request = %v", got)
	}
	if c.HostID != "host_7" || c.FleetKey != "" || c.WSURL != "wss"+strings.TrimPrefix(srv.URL, "https")+HostWSPath {
		t.Fatalf("creds = %+v", c)
	}

	opts.CodeOrKey = testKey
	c, err = Enroll(context.Background(), opts)
	if err != nil || got["key"] != testKey || c.FleetKey != testKey {
		t.Fatalf("key enroll: %v %v", got, err)
	}

	opts.CodeOrKey = "RUE-BAD0-BAD0-BAD0-BAD0"
	_, err = Enroll(context.Background(), opts)
	var ee *EnrollError
	if !errors.As(err, &ee) || ee.Status != 401 || ee.Code != "invalid_code" || strings.Contains(err.Error(), "BAD0") && strings.Contains(err.Error(), "RUE-BAD0-BAD0") {
		t.Fatalf("refused = %v", err)
	}

	opts.CodeOrKey = "rfk_short"
	if _, err := Enroll(context.Background(), opts); err == nil {
		t.Fatalf("malformed key accepted")
	}
	opts.CodeOrKey = "RUE-7F3K-9QX2-LM4D-P8TW"
	opts.PlatformURL = "http://at.example.com"
	if _, err := Enroll(context.Background(), opts); err == nil {
		t.Fatalf("plain http accepted")
	}
}

func TestFleetCfg(t *testing.T) {
	existing := "// template\n// url = https://tournament.example.com\noffline_pause_minutes = 5\nurl = https://old.example.com\nenroll_code = RUE-1111-2222-3333-4444\n"
	out := RenderFleetCfg(existing, "https://at.example.com", testKey, false, "")
	for _, want := range []string{"// url = https://tournament.example.com", "offline_pause_minutes = 5", "url = https://at.example.com", "enroll_key = " + testKey} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	for _, gone := range []string{"old.example.com", "enroll_code", "insecure_dev"} {
		if strings.Contains(out, gone) {
			t.Fatalf("%q kept in\n%s", gone, out)
		}
	}
	// Rewriting is stable.
	if again := RenderFleetCfg(out, "https://at.example.com", testKey, false, ""); again != out {
		t.Fatalf("not idempotent:\n%s\n---\n%s", out, again)
	}

	dir := t.TempDir()
	var chowned []string
	if err := WriteFleetCfg(dir, "http://10.0.0.5:3000", testKey, true, "", func(p string) error { chowned = append(chowned, p); return nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(FleetCfgPath(dir))
	if !strings.Contains(string(data), "insecure_dev = 1") || len(chowned) != 2 {
		t.Fatalf("cfg = %s chowned = %v", data, chowned)
	}
	if fi, _ := os.Stat(FleetCfgPath(dir)); !isWindows() && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	if err := WriteFleetCfg(dir, "https://x", "", false, "", nil); err == nil {
		t.Fatalf("no key accepted")
	}
}

func TestBuildInventoryMatchesSchema(t *testing.T) {
	safe := false
	cpu := 12.5
	servers := []ServerState{
		{Number: 2, Dir: "/home/cs2/server-2", GamePort: 27025, StatusPort: 27032, Running: false},
		{Number: 1, Dir: "/home/cs2/server-1", GamePort: 27015, TVPort: 27020, StatusPort: 27022, Running: true, PID: 4242, StartedAt: 1790340000,
			CPUPct: &cpu, ReadyUpInstalled: "0.4.0", ReadyUpPresent: true, InstallID: "abc12345", ServerID: "srv_1", Health: "ok", Phase: "live", UpdateSafe: &safe, CS2Build: 14032},
	}
	inv := BuildInventory("host_1", "1.11.0", HostFacts{Hostname: "box", OS: "linux", CS2: CS2Facts{MasterBuild: 14032}}, servers, func(n int) int { return n })
	validateDoc(t, "messages/host.inventory.json", inv)
	if inv.Servers[0].Name != "server-1" || inv.Servers[1].ReadyUp.Health != "not_running" || inv.Servers[1].ReadyUp.Installed != nil {
		t.Fatalf("inventory = %+v", inv.Servers)
	}
	if inv.CS2.UpdatesHold != "auto" || inv.Servers[0].Process.Restarts24 != 1 {
		t.Fatalf("defaults: %+v", inv)
	}
	// Load and CPU are not changes.
	a := inventoryKey(inv)
	inv.Resources.Load1 = 9
	cpu2 := 99.0
	inv.Servers[0].Process.CPUPct = &cpu2
	if inventoryKey(inv) != a {
		t.Fatalf("volatile fields counted as a change")
	}
	inv.Servers[0].ReadyUp.Phase = "halftime"
	if inventoryKey(inv) == a {
		t.Fatalf("phase change not seen")
	}
}

func TestHealthObserve(t *testing.T) {
	a := New(Options{HungAfter: 30 * time.Second})
	h := a.health
	now := time.Now()
	st := func(running bool, health string) []ServerState {
		return []ServerState{{Number: 1, Running: running, ReadyUpPresent: true, Health: health}}
	}
	events := func(evs []HealthPayload) string {
		var s []string
		for _, e := range evs {
			s = append(s, e.Server+":"+e.Event)
		}
		return strings.Join(s, ",")
	}
	if e := events(h.observe(st(true, "ok"), now)); e != "" {
		t.Fatalf("first look = %s", e)
	}
	if e := events(h.observe(st(false, ""), now.Add(5*time.Second))); e != "server-1:exited" {
		t.Fatalf("exit = %s", e)
	}
	if e := events(h.observe(st(true, "ok"), now.Add(10*time.Second))); e != "server-1:recovered" {
		t.Fatalf("back = %s", e)
	}
	if e := events(h.observe(st(true, "no_response"), now.Add(15*time.Second))); e != "" {
		t.Fatalf("not hung yet = %s", e)
	}
	if e := events(h.observe(st(true, "no_response"), now.Add(46*time.Second))); e != "server-1:hung" {
		t.Fatalf("hung = %s", e)
	}
	if e := events(h.observe(st(true, "no_response"), now.Add(50*time.Second))); e != "" {
		t.Fatalf("hung twice = %s", e)
	}
	if e := events(h.observe(st(true, "ok"), now.Add(55*time.Second))); e != "server-1:recovered" {
		t.Fatalf("recovered = %s", e)
	}
	// The agent's own stop is not an exit.
	h.expect(1, true)
	if e := events(h.observe(st(false, ""), now.Add(60*time.Second))); e != "" {
		t.Fatalf("own stop reported = %s", e)
	}
	h.expect(1, false)
	// A server without Ready Up never hangs.
	h2 := New(Options{}).health
	noRU := []ServerState{{Number: 1, Running: true, Health: "no_response"}}
	h2.observe(noRU, now)
	if e := events(h2.observe(noRU, now.Add(time.Hour))); e != "" {
		t.Fatalf("hung without Ready Up = %s", e)
	}
}

func TestBackoffAndRetryAfter(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		d := backoff(attempt, time.Second, 30*time.Second)
		if d <= 0 || d > 30*time.Second {
			t.Fatalf("attempt %d: %s", attempt, d)
		}
	}
	if d := retryAfter(`{"retry_after_ms":1500}`, time.Minute); d != 1500*time.Millisecond {
		t.Fatalf("retry after = %s", d)
	}
	if d := retryAfter("rate limited", time.Minute); d != time.Minute {
		t.Fatalf("default = %s", d)
	}
}

func TestPrepareReleaseLookup(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Auto-Tournament/ready-up/releases/tags/v0.5.0":
			_, _ = w.Write([]byte(`{"tag_name":"v0.5.0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	inst := filepath.Join(t.TempDir(), "install.sh")
	_ = os.WriteFile(inst, []byte("#!/bin/sh\n"), 0o755)
	f := &Fetcher{GitHubAPI: gh.URL}
	cfg := AgentConfig{ReadyUpInstaller: inst, ReadyUpAcceptLicense: "commercial"}

	plan, cleanup, err := f.Prepare(context.Background(), cfg, "0.5.0", "default")
	cleanup()
	if err != nil || plan.Version != "v0.5.0" || plan.Component != "essentials" || plan.Installer != inst || plan.AcceptLicense != "commercial" {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	_, cleanup, err = f.Prepare(context.Background(), cfg, "latest", "skins")
	cleanup()
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != CodeNoRelease {
		t.Fatalf("no latest release = %v", err)
	}
	cfg.ReadyUpBundle = "/does/not/exist-{version}.zip"
	_, cleanup, err = f.Prepare(context.Background(), cfg, "0.5.0", "skins")
	cleanup()
	if !errors.As(err, &pe) || pe.Code != CodeNoRelease || !strings.Contains(err.Error(), "exist-0.5.0.zip") {
		t.Fatalf("missing bundle = %v", err)
	}
	if _, err := SetConfig(Paths{Dir: t.TempDir()}, "readyup_bundle", "http://insecure.example.com/x.zip"); err == nil {
		t.Fatalf("http bundle accepted")
	}
	if _, err := SetConfig(Paths{Dir: t.TempDir()}, "readyup_accept_license", "yes"); err == nil {
		t.Fatalf("bad license choice accepted")
	}
}
