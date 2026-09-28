package csm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
)

func TestSteamInfServerVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "steam.inf")
	_ = os.WriteFile(p, []byte("ClientVersion=2000701\r\nServerVersion=2000702\r\nPatchVersion=1.41.1.4\r\n"), 0o644)
	if v := steamInfServerVersion(p); v != 2000702 {
		t.Fatalf("ServerVersion = %d", v)
	}
	if v := steamInfServerVersion(filepath.Join(t.TempDir(), "missing")); v != 0 {
		t.Fatalf("missing = %d", v)
	}
}

func TestProcRSSOfThisProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	if mb, ok := procRSSMB(os.Getpid()); !ok || mb < 0 {
		t.Fatalf("rss = %d %v", mb, ok)
	}
}

func TestRenderHostAgentUnit(t *testing.T) {
	sys := RenderHostAgentUnit("/usr/local/bin/csm", "/opt/cs2-server-manager", true)
	for _, want := range []string{"ExecStart=/usr/local/bin/csm agent", "Environment=CSM_ROOT=/opt/cs2-server-manager", "Restart=always", "WantedBy=multi-user.target"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("system unit misses %q:\n%s", want, sys)
		}
	}
	if user := RenderHostAgentUnit("/usr/local/bin/csm", "/opt/x", false); !strings.Contains(user, "WantedBy=default.target") {
		t.Fatalf("user unit:\n%s", user)
	}
}

func TestHostAgentPathsUnderRoot(t *testing.T) {
	t.Setenv("CSM_ROOT", "/opt/csm-test-root")
	if got := HostAgentPaths().Credentials(); got != "/opt/csm-test-root/fleet/credentials.json" {
		t.Fatalf("credentials path = %s", got)
	}
}

// The backend's view of a server that runs without Ready Up and is stopped:
// nothing is invented.
func TestServerStateWithoutReadyUp(t *testing.T) {
	dir := t.TempDir()
	b := NewHostBackend()
	mgr := &TmuxManager{CS2User: "nobody-csm-test", NumServers: 1}
	row := FleetRow{Target: FleetTarget{Server: 1, Dir: dir, GamePort: 27015}, State: ReadyUpStopped, StatusPort: 27022}
	s := b.serverState(context.Background(), mgr, row)
	if s.Running || s.Health != "not_running" || s.ReadyUpInstalled != "" || s.UpdateSafe != nil || s.InstallID != "" {
		t.Fatalf("state = %+v", s)
	}
	inv := hostagent.BuildInventory("h", "1", hostagent.HostFacts{}, []hostagent.ServerState{s}, nil)
	if inv.Servers[0].ReadyUp.Installed != nil || inv.Servers[0].StatusPort != 27022 {
		t.Fatalf("inventory = %+v", inv.Servers[0])
	}

	// With Ready Up files on disk: installed "unknown", install_id read.
	fleetDir := filepath.Join(dir, "game", "csgo", "readyup", "plugins", "fleet")
	_ = os.MkdirAll(fleetDir, 0o755)
	_ = os.WriteFile(filepath.Join(fleetDir, "install_id"), []byte("01J8ZQ4T8W6N3X0F2R5K7M9P1C\n"), 0o644)
	s = b.serverState(context.Background(), mgr, row)
	if s.ReadyUpInstalled != "unknown" || s.InstallID != "01J8ZQ4T8W6N3X0F2R5K7M9P1C" {
		t.Fatalf("state = %+v", s)
	}
}
