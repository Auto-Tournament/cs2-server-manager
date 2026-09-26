package csm

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecideRunAs(t *testing.T) {
	cases := []struct {
		name    string
		euid    int
		current string
		target  string
		want    runAsMode
	}{
		{"root switches user", 0, "root", "cs2servermanager", runAsSwitchUser},
		{"root named like target still switches", 0, "cs2servermanager", "cs2servermanager", runAsSwitchUser},
		{"cs2 user runs directly", 998, "cs2servermanager", "cs2servermanager", runAsDirect},
		{"cs2 user with padded target", 998, "cs2servermanager", " cs2servermanager ", runAsDirect},
		{"other user switches (su prompts, as before)", 1000, "alice", "cs2servermanager", runAsSwitchUser},
		{"empty target never direct", 1000, "", "", runAsSwitchUser},
		{"unknown current user", 998, "", "cs2servermanager", runAsSwitchUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideRunAs(c.euid, c.current, c.target); got != c.want {
				t.Fatalf("decideRunAs(%d, %q, %q) = %v, want %v", c.euid, c.current, c.target, got, c.want)
			}
		})
	}
}

func TestPrivilegeError(t *testing.T) {
	if err := privilegeError(0, "root", "cs2servermanager", "monitor"); err != nil {
		t.Fatalf("root: unexpected error %v", err)
	}
	if err := privilegeError(998, "cs2servermanager", "cs2servermanager", "monitor"); err != nil {
		t.Fatalf("cs2 user: unexpected error %v", err)
	}
	err := privilegeError(1000, "alice", "cs2servermanager", "monitor")
	if err == nil {
		t.Fatal("other user: expected an error")
	}
	for _, want := range []string{"monitor", "cs2servermanager", "sudo csm setup-host", `"alice"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// An empty CS2 user falls back to the default in the message.
	if err := privilegeError(1000, "alice", "", "bootstrap"); err == nil || !strings.Contains(err.Error(), DefaultCS2User) {
		t.Fatalf("empty cs2 user: got %v", err)
	}
}

func TestSteamcmdArgv(t *testing.T) {
	args := []string{"+force_install_dir", "/home/cs2/master-install", "+quit"}
	cases := []struct {
		name    string
		euid    int
		current string
		setHome bool
		want    []string
	}{
		{"root with -H", 0, "root", true, []string{"sudo", "-u", "cs2", "-H", "steamcmd", "+force_install_dir", "/home/cs2/master-install", "+quit"}},
		{"root without -H", 0, "root", false, []string{"sudo", "-u", "cs2", "steamcmd", "+force_install_dir", "/home/cs2/master-install", "+quit"}},
		{"cs2 user runs steamcmd directly", 998, "cs2", true, []string{"steamcmd", "+force_install_dir", "/home/cs2/master-install", "+quit"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := steamcmdArgv(c.euid, c.current, "cs2", c.setHome, args...)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestUserModeEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/root",
		"TMUX=/tmp/tmux-1000/default,123,0",
		"TMUX_PANE=%1",
		"TMUX_TMPDIR=/run/user/998",
		"TMUXX=keep",
		"LANG=C.UTF-8",
	}
	got := userModeEnv(in, "/home/cs2")
	want := []string{"PATH=/usr/bin:/bin", "TMUXX=keep", "LANG=C.UTF-8", "HOME=/home/cs2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetupHostNextSteps(t *testing.T) {
	out := setupHostNextSteps("cs2servermanager")
	for _, want := range []string{"run csm as cs2servermanager, no sudo", "sudo -iu cs2servermanager", "csm status"} {
		if !strings.Contains(out, want) {
			t.Errorf("next steps missing %q:\n%s", want, out)
		}
	}
}

func TestSteamcmdPreflightFor(t *testing.T) {
	cases := []struct {
		euid       int
		user, want string
		expectSudo bool
	}{
		{1000, "cs2servermanager", "cs2servermanager", false}, // user mode
		{0, "root", "cs2servermanager", false},                // root
		{1001, "alice", "cs2servermanager", true},             // other user
	}
	for _, c := range cases {
		got := steamcmdPreflightFor(c.euid, c.user, c.want) == steamcmdNeedsSudo
		if got != c.expectSudo {
			t.Errorf("steamcmdPreflightFor(%d, %q, %q) needs sudo = %v, want %v", c.euid, c.user, c.want, got, c.expectSudo)
		}
	}
}
