package csm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformDrivesUpdates(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CSM_ROOT", root)
	t.Setenv("CSM_LOCAL_UPDATES", "")

	if PlatformDrivesUpdates() {
		t.Fatal("a host without fleet credentials updates itself")
	}

	creds := HostAgentPaths().Credentials()
	if err := os.MkdirAll(filepath.Dir(creds), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creds, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !PlatformDrivesUpdates() {
		t.Fatal("an enrolled host should leave starting updates to the platform")
	}

	t.Setenv("CSM_LOCAL_UPDATES", "1")
	if PlatformDrivesUpdates() {
		t.Fatal("CSM_LOCAL_UPDATES=1 should keep updates local")
	}
}

func TestMonitorCronEntryKeepsTheAgentRoot(t *testing.T) {
	unit := "[Service]\nType=simple\nEnvironment=CSM_ROOT=/usr/local/bin\nExecStart=/usr/local/bin/csm agent\n"
	if got := unitCSMRoot(unit); got != "/usr/local/bin" {
		t.Fatalf("unitCSMRoot = %q", got)
	}
	if got := monitorCronEntry("*/5", "/usr/local/bin/csm", "/usr/local/bin"); got != "*/5 * * * * CSM_ROOT=/usr/local/bin /usr/local/bin/csm monitor >/dev/null 2>&1" {
		t.Fatalf("entry = %q", got)
	}
	// A root cron could not pass through safely is left out, not quoted.
	if got := monitorCronEntry("*/5", "/usr/local/bin/csm", "/home/a b"); got != "*/5 * * * * /usr/local/bin/csm monitor >/dev/null 2>&1" {
		t.Fatalf("entry = %q", got)
	}
}

func TestGamePatchReadsPatchVersion(t *testing.T) {
	dir := t.TempDir()
	inf := filepath.Join(dir, "game", "csgo", "steam.inf")
	if err := os.MkdirAll(filepath.Dir(inf), 0o755); err != nil {
		t.Fatal(err)
	}
	// Valve writes CRLF.
	if err := os.WriteFile(inf, []byte("ClientVersion=2000924\r\nServerVersion=2000924\r\nPatchVersion=1.41.8.8\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gamePatch(dir); got != "1.41.8.8" {
		t.Fatalf("gamePatch = %q", got)
	}
	if got := gamePatch(t.TempDir()); got != "" {
		t.Fatalf("no steam.inf: %q", got)
	}
}
