package csm

import (
	"strings"
	"testing"
)

const sharedTarget = "localhost:3306/matchzy"

func sharedMySQL(server int, scoping scopingSupport, serverID string, running, scopeArg bool) matchzyServerFacts {
	return matchzyServerFacts{
		Server:      server,
		DBEngine:    MatchzyDBEngineMySQL,
		MySQLTarget: sharedTarget,
		Scoping:     scoping,
		ServerID:    serverID,
		Running:     running,
		HasScopeArg: scopeArg,
	}
}

func TestEvaluateMatchzyScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		facts      []matchzyServerFacts
		want       DoctorCheckStatus
		detailHas  []string
		fixHas     []string
		fixMissing []string
	}{
		{
			name: "production bug: shared MySQL, old plugin, all report s_3",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingNo, "s_3", true, false),
				sharedMySQL(2, scopingNo, "s_3", true, false),
				sharedMySQL(3, scopingNo, "s_3", true, false),
			},
			want:      DoctorFail,
			detailHas: []string{`server-1, server-2, server-3 all report matchzy_server_id "s_3"`, "older than 1.4.26 (no per-server config scoping)"},
			fixHas:    []string{"Auto Tournament CS2 (formerly MatchZy Enhanced) 1.4.26 or newer", "sudo csm update-plugins", `"DatabaseType": "SQLite"`, "reconfigure every server", "sudo csm doctor"},
		},
		{
			name: "fixed: shared MySQL, scoping plugin, scope args, distinct ids",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingYes, "s_2", true, true),
				sharedMySQL(3, scopingYes, "s_3", true, true),
			},
			want:      DoctorOK,
			detailHas: []string{"per-server config scoping"},
		},
		{
			name: "scoping plugin but servers still running with old start args",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, false),
				sharedMySQL(2, scopingYes, "s_2", true, false),
			},
			want:       DoctorFail,
			detailHas:  []string{"running without +matchzy_config_scope"},
			fixHas:     []string{"sudo csm restart"},
			fixMissing: []string{"update-plugins", "reconfigure"},
		},
		{
			name: "stopped servers are not blamed for missing start args",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "", false, false),
				sharedMySQL(2, scopingYes, "", false, false),
			},
			want: DoctorOK,
		},
		{
			name: "every server on its own SQLite",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineSQLite, Scoping: scopingNo, ServerID: "s_1"},
				{Server: 2, DBEngine: MatchzyDBEngineSQLite, Scoping: scopingNo, ServerID: "s_2"},
			},
			want:      DoctorOK,
			detailHas: []string{"own SQLite"},
		},
		{
			name: "SQLite but ids still duplicated until the manager reconfigures",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineSQLite, ServerID: "s_3"},
				{Server: 2, DBEngine: MatchzyDBEngineSQLite, ServerID: "s_3"},
			},
			want:       DoctorFail,
			fixHas:     []string{"reconfigure every server"},
			fixMissing: []string{"update-plugins"},
		},
		{
			name: "plugin missing on a shared-MySQL server",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingUnknown, "", false, false),
			},
			want:      DoctorWarn,
			detailHas: []string{"MatchZy.dll was not found for server-2"},
			fixHas:    []string{"sudo csm update-plugins"},
		},
		{
			name: "separate MySQL databases do not share config",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "localhost:3306/matchzy1", Scoping: scopingNo, ServerID: "s_1", Running: true},
				{Server: 2, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "localhost:3306/matchzy2", Scoping: scopingNo, ServerID: "s_2", Running: true},
			},
			want: DoctorOK,
		},
		{
			name:  "single server",
			facts: []matchzyServerFacts{sharedMySQL(1, scopingNo, "s_1", true, false)},
			want:  DoctorOK,
		},
		{
			name: "only the old-plugin servers in a shared group are named",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingNo, "s_2", true, true),
				{Server: 3, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "db:3306/other", Scoping: scopingNo, ServerID: "s_9", Running: true},
			},
			want:      DoctorFail,
			detailHas: []string{"and server-2 run a MatchZy build older than"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := evaluateMatchzyScope("cs2servermanager", tt.facts)
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s\ndetail: %s\nfix: %s", got.Status, tt.want, got.Detail, got.FixHint)
			}
			if got.Fix != nil {
				t.Fatal("this check must never restart or rewrite anything automatically")
			}
			for _, s := range tt.detailHas {
				if !strings.Contains(got.Detail, s) {
					t.Errorf("detail missing %q:\n%s", s, got.Detail)
				}
			}
			for _, s := range tt.fixHas {
				if !strings.Contains(got.FixHint, s) {
					t.Errorf("fix hint missing %q:\n%s", s, got.FixHint)
				}
			}
			for _, s := range tt.fixMissing {
				if strings.Contains(got.FixHint, s) {
					t.Errorf("fix hint should not mention %q:\n%s", s, got.FixHint)
				}
			}
		})
	}
}

func TestEvaluateMatchzyScopeNamesPluginVersion(t *testing.T) {
	t.Parallel()

	a := sharedMySQL(1, scopingNo, "s_1", true, true)
	a.PluginVersion = "1.4.25"
	b := sharedMySQL(2, scopingYes, "s_2", true, true)
	b.PluginVersion = "1.4.26"
	got := evaluateMatchzyScope("cs2servermanager", []matchzyServerFacts{a, b})
	if got.Status != DoctorFail {
		t.Fatalf("status = %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Detail, "server-1 (MatchZy 1.4.25) run a MatchZy build older than 1.4.26") {
		t.Fatalf("detail does not name the old version:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "server-2 (MatchZy") {
		t.Fatalf("server-2 is new enough and must not be named:\n%s", got.Detail)
	}
}

func TestMatchzyScopingRequirement(t *testing.T) {
	t.Parallel()

	if got, want := MatchzyScopingRequirement(), "Auto Tournament CS2 (formerly MatchZy Enhanced) 1.4.26 or newer"; got != want {
		t.Fatalf("MatchzyScopingRequirement() = %q, want %q", got, want)
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.4.26", "1.4.26", 0, true},
		{"v1.4.26", "1.4.26", 0, true},
		{"1.4.25", "1.4.26", -1, true},
		{"1.4.27", "1.4.26", 1, true},
		{"1.5", "1.4.26", 1, true},
		{"1.4.100", "1.4.26", 1, true}, // numeric, not lexical
		{"2.0.0", "1.4.26", 1, true},
		{"1.4", "1.4.0", 0, true},
		{"1.4.26-rc1", "1.4.26", 0, true},
		{"", "1.4.26", 0, false},
		{"dev", "1.4.26", 0, false},
	}
	for _, tt := range tests {
		got, ok := compareVersions(tt.a, tt.b)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Fatalf("compareVersions(%q, %q) = %d, %v; want %d, %v", tt.a, tt.b, got, ok, tt.want, tt.ok)
		}
	}
}

func TestLastMatchzyPluginVersion(t *testing.T) {
	t.Parallel()

	log := strings.Join([]string{
		`[MatchZy] [SendEventAsync] SENDING DATA: {"server_id":"s_1","plugin_version":"1.4.25","event":"server_health"}`,
		`[MatchZy v1.4.26 LOADED] MatchZy Enhanced by WD- & sivert-io`,
	}, "\n")
	if got := lastMatchzyPluginVersion(log); got != "1.4.26" {
		t.Fatalf("got %q, want 1.4.26 (the later LOADED line)", got)
	}
	if got := lastMatchzyPluginVersion(`{"server_id":"s_1","plugin_version":"1.4.25","db_ok":true}`); got != "1.4.25" {
		t.Fatalf("event form: got %q", got)
	}
	if got := lastMatchzyPluginVersion("no version here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestResolveScopingSupportPrefersVersions(t *testing.T) {
	t.Parallel()

	probed := 0
	probe := func(result scopingSupport) func() scopingSupport {
		return func() scopingSupport { probed++; return result }
	}

	tests := []struct {
		name        string
		logV, depV  string
		dll         scopingSupport
		want        scopingSupport
		wantVersion string
		wantProbe   bool
	}{
		{"logged version new enough beats an old-looking dll", "1.4.26", "", scopingNo, scopingYes, "1.4.26", false},
		{"logged old version beats a newer deployed release (not restarted yet)", "1.4.25", "v1.4.26", scopingYes, scopingNo, "1.4.25", false},
		{"deployed release used when nothing is logged", "", "v1.4.27", scopingNo, scopingYes, "1.4.27", false},
		{"deployed old release", "", "v1.4.24", scopingYes, scopingNo, "1.4.24", false},
		{"unparseable versions fall back to the dll", "dev", "latest", scopingYes, scopingYes, "", true},
		{"no versions fall back to the dll", "", "", scopingUnknown, scopingUnknown, "", true},
	}
	for _, tt := range tests {
		before := probed
		got, v := resolveScopingSupport(tt.logV, tt.depV, probe(tt.dll))
		if got != tt.want || v != tt.wantVersion {
			t.Fatalf("%s: got %v %q, want %v %q", tt.name, got, v, tt.want, tt.wantVersion)
		}
		if (probed > before) != tt.wantProbe {
			t.Fatalf("%s: dll probed=%v, want %v", tt.name, probed > before, tt.wantProbe)
		}
	}
}

func TestLastMatchzyServerID(t *testing.T) {
	t.Parallel()

	log := strings.Join([]string{
		`[LoadPersistentConfig] Loaded matchzy_bootstrap_url: http://192.168.50.196:3069/api/servers/s_3/bootstrap`,
		`[LoadPersistentConfig] Loaded matchzy_server_id: s_3`,
		`[SaveConfigValue] Saved config: matchzy_server_id = s_1`,
		`[SendEventAsync] {"event":"going_live","server_id":"s_2","matchid":"42"}`,
	}, "\n")
	if got := lastMatchzyServerID(log); got != "s_2" {
		t.Fatalf("lastMatchzyServerID = %q, want s_2", got)
	}
	if got := lastMatchzyServerID(`[SaveConfigValue] Saved config: matchzy_server_id = s_3`); got != "s_3" {
		t.Fatalf("SaveConfigValue form: got %q", got)
	}
	if got := lastMatchzyServerID("no ids here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestScopingSupportInBytes(t *testing.T) {
	t.Parallel()

	junk := []byte("\x00MZ\x90\x00 some assembly bytes ")
	if got := scopingSupportInBytes(append(junk, utf16le("matchzy_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-16LE literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, []byte("+matchzy_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-8 literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, utf16le("matchzy_server_id")...)); got != scopingNo {
		t.Fatalf("old build: got %v", got)
	}
	if got := dllScopingSupport("/nonexistent/MatchZy.dll"); got != scopingUnknown {
		t.Fatalf("missing dll: got %v", got)
	}
}

func TestScopeArgOnRunningServer(t *testing.T) {
	t.Parallel()

	ps := strings.Join([]string{
		"tmux new-session -d -s cs2-1 ./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +matchzy_config_scope cs2-server-1",
		"/home/cs2servermanager/server-1/game/bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +matchzy_config_scope cs2-server-1",
		"/home/cs2servermanager/server-2/game/bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 +map de_dust2 -port 27025 +tv_port 27030 +maxplayers 10 -usercon",
		"/home/other/cs2 -port 270150",
		"nc -port 27035",
	}, "\n")

	tests := []struct {
		port         int
		running, arg bool
	}{
		{27015, true, true},
		{27025, true, false},
		{27035, false, false}, // only a non-cs2 process
		{27045, false, false},
	}
	for _, tt := range tests {
		running, arg := scopeArgOnRunningServer(ps, tt.port)
		if running != tt.running || arg != tt.arg {
			t.Fatalf("port %d: running=%v arg=%v, want %v %v", tt.port, running, arg, tt.running, tt.arg)
		}
	}
	// 270150 must not count as 27015 on its own.
	if running, _ := scopeArgOnRunningServer("/x/cs2 -port 270150", 27015); running {
		t.Fatal("-port 270150 matched port 27015")
	}
}

func TestParseMatchzyDBEngine(t *testing.T) {
	t.Parallel()

	engine, target := parseMatchzyDBEngine([]byte(`{"DatabaseType":"MySQL","MySqlHost":"localhost","MySqlPort":3306,"MySqlDatabase":"matchzy"}`))
	_, target2 := parseMatchzyDBEngine([]byte(`{"DatabaseType":"mysql","MySqlHost":"127.0.0.1","MySqlPort":0,"MySqlDatabase":"matchzy"}`))
	if engine != MatchzyDBEngineMySQL || target != sharedTarget || target2 != sharedTarget {
		t.Fatalf("got %q %q %q", engine, target, target2)
	}
	if engine, target := parseMatchzyDBEngine([]byte(liveWorkaroundDBJSON)); engine != MatchzyDBEngineSQLite || target != "" {
		t.Fatalf("sqlite: got %q %q", engine, target)
	}
	if engine, _ := parseMatchzyDBEngine([]byte("nope")); engine != "" {
		t.Fatalf("invalid json: got %q", engine)
	}
}
