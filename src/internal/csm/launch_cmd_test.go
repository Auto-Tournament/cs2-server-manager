package csm

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var scopeTokenRE = regexp.MustCompile(`^[a-z0-9._-]+$`)

func baseSpec(mode string, server int) launchSpec {
	return launchSpec{
		Mode:        mode,
		GamePort:    DefaultBaseGamePort + (server-1)*10,
		TVPort:      DefaultBaseTVPort + (server-1)*10,
		MaxPlayers:  10,
		GSLT:        "ABCDEF0123456789",
		ConfigScope: atcs2ConfigScopeFor("cs2", server),
	}
}

func TestATCS2ConfigScopeFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		server int
		want   string
	}{
		{"hostname prefix", "cs2", 1, "cs2-server-1"},
		{"upper case and spaces are normalised", "EU Box #1", 3, "eu-box-1-server-3"},
		{"fqdn dots are kept", "cs2.eu.example.org", 2, "cs2.eu.example.org-server-2"},
		{"empty prefix falls back to directory name", "", 2, "server-2"},
		{"prefix of only symbols falls back to directory name", "'$();`", 4, "server-4"},
		{"leading dash cannot look like a flag", "-port", 1, "port-server-1"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := atcs2ConfigScopeFor(tt.prefix, tt.server); got != tt.want {
				t.Fatalf("atcs2ConfigScopeFor(%q, %d) = %q, want %q", tt.prefix, tt.server, got, tt.want)
			}
		})
	}
}

func TestATCS2ConfigScopeLongPrefixKeepsServerSuffix(t *testing.T) {
	t.Parallel()

	prefix := strings.Repeat("very-long-hostname-", 10)
	seen := map[string]int{}
	for n := 1; n <= 12; n++ {
		s := atcs2ConfigScopeFor(prefix, n)
		if len(s) > atcs2ScopeMaxLen {
			t.Fatalf("scope %q is %d chars, max %d", s, len(s), atcs2ScopeMaxLen)
		}
		if !scopeTokenRE.MatchString(s) {
			t.Fatalf("scope %q has characters outside [a-z0-9._-]", s)
		}
		if prev, dup := seen[s]; dup {
			t.Fatalf("server-%d and server-%d both got scope %q", prev, n, s)
		}
		seen[s] = n
	}
}

func TestATCS2ConfigScopeUsesPrefixEnv(t *testing.T) {
	t.Setenv("CSM_AT_SCOPE_PREFIX", "Tournament EU")
	if got, want := ATCS2ConfigScope(2), "tournament-eu-server-2"; got != want {
		t.Fatalf("ATCS2ConfigScope(2) = %q, want %q", got, want)
	}
	// Stable across calls (restarts).
	if ATCS2ConfigScope(2) != ATCS2ConfigScope(2) {
		t.Fatal("scope is not stable across calls")
	}
}

func TestBuildLaunchCommandIncludesScopeInEveryMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		spec   launchSpec
		prefix string
	}{
		{"valve cs2.sh", baseSpec("valve", 1), "./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon"},
		{"steam runtime mode string falls back to cs2.sh", baseSpec("auto_off", 1), "./cs2.sh -dedicated -ip 0.0.0.0 "},
		{"legacy csm cs2.sh wrapper", func() launchSpec { s := baseSpec("valve", 1); s.LegacyCS2Sh = true; return s }(), "./cs2.sh +map de_dust2 -port 27015"},
		{"alternate csm.sh", baseSpec("alternate", 1), "./csm.sh +map de_dust2 -port 27015"},
		{"binary", baseSpec("binary", 1), "LD_LIBRARY_PATH="},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := buildLaunchCommand(tt.spec)
			if !strings.HasPrefix(cmd, tt.prefix) {
				t.Fatalf("command %q does not start with %q", cmd, tt.prefix)
			}
			want := " +at_config_scope cs2-server-1"
			if c := strings.Count(cmd, ATCS2ConfigScopeArg); c != 1 {
				t.Fatalf("command has %d %s args, want 1: %q", c, ATCS2ConfigScopeArg, cmd)
			}
			if !strings.Contains(cmd, want) {
				t.Fatalf("command %q does not contain %q", cmd, want)
			}
			if !strings.HasSuffix(cmd, " -gslt ABCDEF0123456789") {
				t.Fatalf("command %q lost the GSLT", cmd)
			}
		})
	}
}

func TestBuildLaunchCommandUniqueScopePerServer(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}
	for n := 1; n <= 5; n++ {
		cmd := buildLaunchCommand(baseSpec("valve", n))
		fields := strings.Fields(cmd)
		scope := ""
		for i, f := range fields {
			if f == ATCS2ConfigScopeArg && i+1 < len(fields) {
				scope = fields[i+1]
			}
		}
		if scope == "" {
			t.Fatalf("server-%d: no scope in %q", n, cmd)
		}
		if prev, dup := seen[scope]; dup {
			t.Fatalf("server-%d and server-%d share scope %q", prev, n, scope)
		}
		seen[scope] = n
	}
}

func TestBuildLaunchCommandOmitsEmptyScope(t *testing.T) {
	t.Parallel()

	s := baseSpec("valve", 1)
	s.ConfigScope = "  '\"$;  "
	if cmd := buildLaunchCommand(s); strings.Contains(cmd, ATCS2ConfigScopeArg) {
		t.Fatalf("empty scope should be omitted, got %q", cmd)
	}
}

// hostileScope contains every kind of shell metacharacter that could break out
// of the shell layers the launch command passes through if it were not
// sanitised. Its payload is deliberately harmless (true / exit), so even a
// broken sanitiser can't do damage when the shell test runs it.
const hostileScope = `evil'; true #"$(true)` + "`true`" + ` && exit 7 \`

func TestBuildLaunchCommandEscapesHostileScope(t *testing.T) {
	t.Parallel()

	withScope := baseSpec("valve", 1)
	withScope.ConfigScope = hostileScope
	withoutScope := baseSpec("valve", 1)
	withoutScope.ConfigScope = ""

	cmd := buildLaunchCommand(withScope)
	base := buildLaunchCommand(withoutScope)

	arg := atcs2ScopeLaunchArg(hostileScope)
	token := strings.TrimPrefix(arg, " "+ATCS2ConfigScopeArg+" ")
	if !scopeTokenRE.MatchString(token) {
		t.Fatalf("sanitised scope %q has shell metacharacters", token)
	}
	// The scope must add nothing but the argument itself.
	if strings.Replace(cmd, arg, "", 1) != base {
		t.Fatalf("scope changed more than its own argument:\n with: %q\n base: %q", cmd, base)
	}

	// Through tmux the launch command sits in single quotes, and for Steam
	// Runtime inside a double-quoted bash -lc string; neither kind of quote
	// may come from the scope.
	tmux := buildTmuxStartCmdline("/home/cs2servermanager/server-1/game", "cs2-1", cmd)
	if got := strings.Count(tmux, "'"); got != 2 {
		t.Fatalf("tmux cmdline has %d single quotes, want 2: %q", got, tmux)
	}
	rt := wrapSteamRuntimeLaunch("/home/cs2servermanager/steamrt/run", cmd)
	if strings.Count(rt, "'") != 0 {
		t.Fatalf("steam runtime wrapper gained single quotes: %q", rt)
	}
	if strings.Count(rt, `"`) != strings.Count(wrapSteamRuntimeLaunch("/home/cs2servermanager/steamrt/run", base), `"`) {
		t.Fatalf("scope added double quotes to the steam runtime wrapper: %q", rt)
	}
}

// TestLaunchCommandShellSplitsScope runs the launch command's arguments
// through a real shell and checks the server would receive the scope as one
// argv entry right after +at_config_scope.
func TestLaunchCommandShellSplitsScope(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}

	for _, scope := range []string{"cs2-server-1", hostileScope} {
		s := baseSpec("valve", 1)
		s.ConfigScope = scope
		cmd := buildLaunchCommand(s)
		args := strings.TrimPrefix(cmd, "./cs2.sh ")

		out, err := exec.Command(sh, "-c", "printf '%s\\n' "+args).Output()
		if err != nil {
			t.Fatalf("sh failed for %q: %v", cmd, err)
		}
		argv := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		want := sanitizeScopeToken(scope)
		found := false
		for i, a := range argv {
			if a == ATCS2ConfigScopeArg {
				if i+1 >= len(argv) || argv[i+1] != want {
					t.Fatalf("argv after %s = %v, want %q", ATCS2ConfigScopeArg, argv[i+1:], want)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("%s not in argv %v", ATCS2ConfigScopeArg, argv)
		}
	}
}
