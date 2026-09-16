package csm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchModeFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"nothing set uses Valve cs2.sh", nil, "valve"},
		{"alternate", map[string]string{"CSM_LAUNCH_MODE": "alternate"}, "alternate"},
		{"binary, case and spaces ignored", map[string]string{"CSM_LAUNCH_MODE": " Binary "}, "binary"},
		{"legacy CSM_ALTERNATE_LAUNCHER", map[string]string{"CSM_ALTERNATE_LAUNCHER": "yes"}, "alternate"},
		{"legacy variable off", map[string]string{"CSM_ALTERNATE_LAUNCHER": "0"}, "valve"},
		{"CSM_LAUNCH_MODE wins over legacy", map[string]string{"CSM_LAUNCH_MODE": "binary", "CSM_ALTERNATE_LAUNCHER": "1"}, "binary"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := launchModeFromEnv(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Fatalf("launchModeFromEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyLaunchModeFlags(t *testing.T) {
	if err := ApplyLaunchModeFlags(true, true); err == nil {
		t.Fatal("--alternate with --binary should fail")
	}

	t.Setenv("CSM_LAUNCH_MODE", "binary")
	if err := ApplyLaunchModeFlags(false, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("CSM_LAUNCH_MODE"); got != "binary" {
		t.Fatalf("no flags should keep exported CSM_LAUNCH_MODE, got %q", got)
	}

	if err := ApplyLaunchModeFlags(true, false); err != nil {
		t.Fatal(err)
	}
	if got := launchModeFromEnv(os.Getenv); got != "alternate" {
		t.Fatalf("--alternate resolved to %q", got)
	}
	if err := ApplyLaunchModeFlags(false, true); err != nil {
		t.Fatal(err)
	}
	if got := launchModeFromEnv(os.Getenv); got != "binary" {
		t.Fatalf("--binary resolved to %q", got)
	}
}

// TestServerLaunchHonoursModeForStartAndDebug is the regression test for
// `csm start|restart --alternate|--binary` launching with cs2.sh anyway: the
// Steam Runtime mode string used to overwrite the launch mode in Start.
// Restart calls Start, so Start and Debug cover every entry point.
func TestServerLaunchHonoursModeForStartAndDebug(t *testing.T) {
	t.Setenv("CSM_STEAMRT", "0")
	t.Setenv("CSM_MATCHZY_SCOPE_PREFIX", "box")
	t.Setenv("CSM_ALTERNATE_LAUNCHER", "")

	tests := []struct {
		name       string
		alternate  bool
		binary     bool
		envMode    string
		wantPrefix string
		wantCSMSh  bool
	}{
		{"default", false, false, "", "./cs2.sh -dedicated ", false},
		{"--alternate", true, false, "", "./csm.sh +map de_dust2 ", true},
		{"--binary", false, true, "", "LD_LIBRARY_PATH=./bin/linuxsteamrt64", false},
		{"CSM_LAUNCH_MODE=alternate", false, false, "alternate", "./csm.sh +map de_dust2 ", true},
		{"CSM_LAUNCH_MODE=binary", false, false, "binary", "LD_LIBRARY_PATH=./bin/linuxsteamrt64", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CSM_LAUNCH_MODE", tt.envMode)
			if err := ApplyLaunchModeFlags(tt.alternate, tt.binary); err != nil {
				t.Fatal(err)
			}

			gameDir := filepath.Join(t.TempDir(), "server-2", "game")
			m := &TmuxManager{CS2User: "csm-test-no-such-user"}

			start, startRT, _ := m.serverLaunch(2, gameDir, 27025, 27030, 10, "Start")
			debug, debugRT, _ := m.serverLaunch(2, gameDir, 27025, 27030, 10, "Debug")
			if start != debug || startRT != debugRT {
				t.Fatalf("start and debug differ:\n start: %q\n debug: %q", start, debug)
			}
			if !strings.HasPrefix(start, tt.wantPrefix) {
				t.Fatalf("command %q does not start with %q", start, tt.wantPrefix)
			}
			if c := strings.Count(start, MatchzyConfigScopeArg+" box-server-2"); c != 1 {
				t.Fatalf("command has %d scope args, want 1: %q", c, start)
			}
			_, err := os.Stat(filepath.Join(gameDir, "csm.sh"))
			if gotCSMSh := err == nil; gotCSMSh != tt.wantCSMSh {
				t.Fatalf("csm.sh installed = %v, want %v", gotCSMSh, tt.wantCSMSh)
			}
		})
	}
}

func TestServerLaunchCommandSteamRuntimeKeepsModeAndScope(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"valve", "alternate", "binary"} {
		s := baseSpec(mode, 1)
		plain := serverLaunchCommand(s, "")
		if plain != buildLaunchCommand(s) {
			t.Fatalf("%s: without Steam Runtime the command must be unwrapped: %q", mode, plain)
		}
		wrapped := serverLaunchCommand(s, "/home/cs2/steamrt/run")
		if !strings.HasPrefix(wrapped, "bash -lc ") || !strings.Contains(wrapped, plain) {
			t.Fatalf("%s: wrapped command %q does not run %q", mode, wrapped, plain)
		}
		// The wrapper repeats the command once for its fallback.
		if c := strings.Count(wrapped, MatchzyConfigScopeArg+" cs2-server-1"); c != 2 {
			t.Fatalf("%s: wrapped command has %d scope args, want 2: %q", mode, c, wrapped)
		}
	}
}
