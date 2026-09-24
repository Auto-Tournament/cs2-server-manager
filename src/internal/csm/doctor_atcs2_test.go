package csm

import (
	"strings"
	"testing"
)

const sharedTarget = "localhost:3306/auto_tournament_cs2"

func sharedMySQL(server int, scoping scopingSupport, serverID string, running, scopeArg bool) atcs2ServerFacts {
	return atcs2ServerFacts{
		Server:      server,
		DBEngine:    ATCS2DBEngineMySQL,
		MySQLTarget: sharedTarget,
		Scoping:     scoping,
		ServerID:    serverID,
		Running:     running,
		HasScopeArg: scopeArg,
	}
}

func TestEvaluateATCS2Scope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		facts      []atcs2ServerFacts
		want       DoctorCheckStatus
		detailHas  []string
		fixHas     []string
		fixMissing []string
	}{
		{
			name: "production bug: shared MySQL, old plugin, all report s_3",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingNo, "s_3", true, false),
				sharedMySQL(2, scopingNo, "s_3", true, false),
				sharedMySQL(3, scopingNo, "s_3", true, false),
			},
			want:      DoctorFail,
			detailHas: []string{`server-1, server-2, server-3 all report at_server_id "s_3"`, "still run the old plugin", "must be replaced"},
			fixHas:    []string{"Replace the old plugin with Auto Tournament CS2 2.0.0 or newer", "sudo csm update-plugins", "reconfigure every server", "sudo csm doctor"},
		},
		{
			name: "fixed: shared MySQL, scoping plugin, scope args, distinct ids",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingYes, "s_2", true, true),
				sharedMySQL(3, scopingYes, "s_3", true, true),
			},
			want:      DoctorOK,
			detailHas: []string{"Auto Tournament CS2 2.0.0 or newer", "+at_config_scope"},
		},
		{
			name: "scoping plugin but servers still running with old start args",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, false),
				sharedMySQL(2, scopingYes, "s_2", true, false),
			},
			want:       DoctorFail,
			detailHas:  []string{"running without +at_config_scope"},
			fixHas:     []string{"sudo csm restart"},
			fixMissing: []string{"update-plugins", "reconfigure"},
		},
		{
			name: "stopped servers are not blamed for missing start args",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingYes, "", false, false),
				sharedMySQL(2, scopingYes, "", false, false),
			},
			want: DoctorOK,
		},
		{
			name: "every server on its own SQLite",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineSQLite, Scoping: scopingYes, ServerID: "s_1"},
				{Server: 2, DBEngine: ATCS2DBEngineSQLite, Scoping: scopingYes, ServerID: "s_2"},
			},
			want:      DoctorOK,
			detailHas: []string{"own SQLite"},
		},
		{
			name: "old plugin fails even on SQLite: it must be replaced",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineSQLite, Scoping: scopingNo, PluginVersion: "1.4.34", ServerID: "s_1"},
				{Server: 2, DBEngine: ATCS2DBEngineSQLite, Scoping: scopingYes, PluginVersion: "2.0.0", ServerID: "s_2"},
			},
			want:      DoctorFail,
			detailHas: []string{"server-1 (plugin 1.4.34) still run the old plugin", "Auto Tournament CS2 2.0.0 or newer", "must be replaced"},
			fixHas:    []string{"Replace the old plugin", "sudo csm update-plugins"},
		},
		{
			name: "old plugin folder still installed next to the new one",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineSQLite, Scoping: scopingYes, PluginVersion: "2.0.0", LegacyPlugin: true},
			},
			want:      DoctorFail,
			detailHas: []string{"server-1 (old plugins/MatchZy/ folder)"},
			fixHas:    []string{"sudo csm update-plugins"},
		},
		{
			name: "only the old plugin installed, no AutoTournamentCS2.dll",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineMySQL, MySQLTarget: sharedTarget, Scoping: scopingUnknown, LegacyPlugin: true, Running: true, HasScopeArg: true},
				sharedMySQL(2, scopingYes, "", true, true),
			},
			want:       DoctorFail,
			detailHas:  []string{"server-1 (old plugins/MatchZy/ folder) still run the old plugin"},
			fixHas:     []string{"Replace the old plugin"},
			fixMissing: []string{"was not found"},
		},
		{
			name: "SQLite but ids still duplicated until the manager reconfigures",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineSQLite, ServerID: "s_3"},
				{Server: 2, DBEngine: ATCS2DBEngineSQLite, ServerID: "s_3"},
			},
			want:       DoctorFail,
			fixHas:     []string{"reconfigure every server"},
			fixMissing: []string{"update-plugins"},
		},
		{
			name: "plugin missing on a shared-MySQL server",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingUnknown, "", false, false),
			},
			want:      DoctorWarn,
			detailHas: []string{"AutoTournamentCS2.dll was not found for server-2"},
			fixHas:    []string{"sudo csm update-plugins"},
		},
		{
			name: "separate MySQL databases do not share config",
			facts: []atcs2ServerFacts{
				{Server: 1, DBEngine: ATCS2DBEngineMySQL, MySQLTarget: "localhost:3306/stats1", Scoping: scopingYes, ServerID: "s_1", Running: true},
				{Server: 2, DBEngine: ATCS2DBEngineMySQL, MySQLTarget: "localhost:3306/stats2", Scoping: scopingYes, ServerID: "s_2", Running: true},
			},
			want: DoctorOK,
		},
		{
			name:  "single server",
			facts: []atcs2ServerFacts{sharedMySQL(1, scopingYes, "s_1", true, false)},
			want:  DoctorOK,
		},
		{
			name: "only the old-plugin servers are named",
			facts: []atcs2ServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingNo, "s_2", true, true),
				{Server: 3, DBEngine: ATCS2DBEngineMySQL, MySQLTarget: "db:3306/other", Scoping: scopingNo, ServerID: "s_9", Running: true},
			},
			want:      DoctorFail,
			detailHas: []string{"server-2 (a build without at_config_scope), server-3 (a build without at_config_scope) still run the old plugin"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := evaluateATCS2Scope("cs2servermanager", tt.facts)
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

func TestEvaluateATCS2ScopeNamesPluginVersion(t *testing.T) {
	t.Parallel()

	a := sharedMySQL(1, scopingNo, "s_1", true, true)
	a.PluginVersion = "1.4.34"
	b := sharedMySQL(2, scopingYes, "s_2", true, true)
	b.PluginVersion = "2.0.0"
	got := evaluateATCS2Scope("cs2servermanager", []atcs2ServerFacts{a, b})
	if got.Status != DoctorFail {
		t.Fatalf("status = %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Detail, "server-1 (plugin 1.4.34) still run the old plugin. csm needs Auto Tournament CS2 2.0.0 or newer") {
		t.Fatalf("detail does not name the old version:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "server-2 (plugin") {
		t.Fatalf("server-2 is new enough and must not be named:\n%s", got.Detail)
	}
}

func TestATCS2Requirement(t *testing.T) {
	t.Parallel()

	if got, want := ATCS2Requirement(), "Auto Tournament CS2 2.0.0 or newer"; got != want {
		t.Fatalf("ATCS2Requirement() = %q, want %q", got, want)
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

func TestLastATCS2PluginVersion(t *testing.T) {
	t.Parallel()

	log := strings.Join([]string{
		`[Auto Tournament] [SendEventAsync] SENDING DATA: {"server_id":"s_1","plugin_version":"2.0.0","event":"server_health"}`,
		`[Auto Tournament CS2 v2.0.1 LOADED] Auto Tournament CS2`,
	}, "\n")
	if got := lastATCS2PluginVersion(log); got != "2.0.1" {
		t.Fatalf("got %q, want 2.0.1 (the later LOADED line)", got)
	}
	// A pre-2.0.0 build still sends plugin_version in its events, so an old
	// plugin that is still loaded is recognised.
	if got := lastATCS2PluginVersion(`{"server_id":"s_1","plugin_version":"1.4.34","db_ok":true}`); got != "1.4.34" {
		t.Fatalf("event form: got %q", got)
	}
	if got := lastATCS2PluginVersion("no version here"); got != "" {
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
		{"logged version new enough beats an old-looking dll", "2.0.0", "", scopingNo, scopingYes, "2.0.0", false},
		{"logged old version beats a newer deployed release (not restarted yet)", "1.4.34", "v2.0.0", scopingYes, scopingNo, "1.4.34", false},
		{"deployed release used when nothing is logged", "", "v2.0.1", scopingNo, scopingYes, "2.0.1", false},
		{"deployed old release", "", "v1.4.34", scopingYes, scopingNo, "1.4.34", false},
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

func TestLastATCS2ServerID(t *testing.T) {
	t.Parallel()

	log := strings.Join([]string{
		`[LoadPersistentConfig] Loaded at_bootstrap_url: http://192.168.50.196:3069/api/servers/s_3/bootstrap`,
		`[LoadPersistentConfig] Loaded at_server_id: s_3`,
		`[SaveConfigValue] Saved config: at_server_id = s_1`,
		`[SendEventAsync] {"event":"going_live","server_id":"s_2","matchid":"42"}`,
	}, "\n")
	if got := lastATCS2ServerID(log); got != "s_2" {
		t.Fatalf("lastATCS2ServerID = %q, want s_2", got)
	}
	if got := lastATCS2ServerID(`[SaveConfigValue] Saved config: at_server_id = s_3`); got != "s_3" {
		t.Fatalf("SaveConfigValue form: got %q", got)
	}
	if got := lastATCS2ServerID("no ids here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestScopingSupportInBytes(t *testing.T) {
	t.Parallel()

	junk := []byte("\x00MZ\x90\x00 some assembly bytes ")
	if got := scopingSupportInBytes(append(junk, utf16le("at_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-16LE literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, []byte("+at_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-8 literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, utf16le("at_server_id")...)); got != scopingNo {
		t.Fatalf("old build: got %v", got)
	}
	if got := dllScopingSupport("/nonexistent/AutoTournamentCS2.dll"); got != scopingUnknown {
		t.Fatalf("missing dll: got %v", got)
	}
}

func TestScopeArgOnRunningServer(t *testing.T) {
	t.Parallel()

	ps := strings.Join([]string{
		"tmux new-session -d -s cs2-1 ./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +at_config_scope cs2-server-1",
		"/home/cs2servermanager/server-1/game/bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +at_config_scope cs2-server-1",
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

func TestParseATCS2DBEngine(t *testing.T) {
	t.Parallel()

	engine, target := parseATCS2DBEngine([]byte(`{"DatabaseType":"MySQL","MySqlHost":"localhost","MySqlPort":3306,"MySqlDatabase":"auto_tournament_cs2"}`))
	_, target2 := parseATCS2DBEngine([]byte(`{"DatabaseType":"mysql","MySqlHost":"127.0.0.1","MySqlPort":0,"MySqlDatabase":"auto_tournament_cs2"}`))
	if engine != ATCS2DBEngineMySQL || target != sharedTarget || target2 != sharedTarget {
		t.Fatalf("got %q %q %q", engine, target, target2)
	}
	if engine, target := parseATCS2DBEngine([]byte(liveWorkaroundDBJSON)); engine != ATCS2DBEngineSQLite || target != "" {
		t.Fatalf("sqlite: got %q %q", engine, target)
	}
	if engine, _ := parseATCS2DBEngine([]byte("nope")); engine != "" {
		t.Fatalf("invalid json: got %q", engine)
	}
}

func TestATCS2MinVersion(t *testing.T) {
	t.Parallel()

	// 2.0.0 is the renamed plugin: AutoTournamentCS2.dll, cfg/
	// AutoTournamentCS2/ and +at_config_scope. Every 1.x build is the old
	// plugin and must be replaced.
	if ATCS2MinVersion != "2.0.0" {
		t.Fatalf("ATCS2MinVersion = %q, want 2.0.0", ATCS2MinVersion)
	}
	noDLL := func() scopingSupport { t.Fatal("DLL probe must not run when a version parses"); return scopingUnknown }
	for v, want := range map[string]scopingSupport{
		"1.4.28": scopingNo, "1.4.34": scopingNo, "v1.9.9": scopingNo,
		"2.0.0": scopingYes, "v2.0.0": scopingYes, "2.1.3": scopingYes,
	} {
		if got, _ := resolveScopingSupport(v, "", noDLL); got != want {
			t.Errorf("resolveScopingSupport(%q) = %v, want %v", v, got, want)
		}
	}
}
