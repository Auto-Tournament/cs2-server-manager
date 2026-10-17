package csm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProc adds a process to a fake /proc: its cwd link and NUL-separated cmdline.
func fakeProc(t *testing.T, root, pid, cwd string, argv ...string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestServerCS2Cmdlines(t *testing.T) {
	root := t.TempDir()
	s1 := "/home/cs2/server-1"
	fakeProc(t, root, "100", s1+"/game", s1+"/game/bin/linuxsteamrt64/cs2", "-dedicated", "-allow_round_backup_files")
	fakeProc(t, root, "101", s1+"/game", "bash", "./cs2.sh") // launcher, not cs2
	fakeProc(t, root, "200", "/home/cs2/server-10/game", "/x/cs2", "-dedicated")
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := serverCS2Cmdlines(root, s1)
	if len(got) != 1 {
		t.Fatalf("server-1: got %d cmdlines, want 1 (server-10 must not match server-1)", len(got))
	}
	if !anyCmdlineHasFlag(got, roundBackupFlag) {
		t.Fatal("flag not found in server-1 cmdline")
	}
	if n := len(serverCS2Cmdlines(root, "/home/cs2/server-10")); n != 1 {
		t.Fatalf("server-10: got %d, want 1", n)
	}
	if anyCmdlineHasFlag(serverCS2Cmdlines(root, "/home/cs2/server-10"), roundBackupFlag) {
		t.Fatal("server-10 has no flag")
	}
}

func TestAnyCmdlineHasFlagExactArg(t *testing.T) {
	if anyCmdlineHasFlag([][]byte{[]byte("cs2\x00-allow_round_backup_files_x\x00")}, roundBackupFlag) {
		t.Fatal("a longer arg must not count as the flag")
	}
}

func TestCheckRoundBackupFlagNoneRunning(t *testing.T) {
	c := checkRoundBackupFlag(DoctorMeta{CS2User: "cs2", Servers: []int{1, 2}}, func(int) bool { return false })
	if c.Status != DoctorOK {
		t.Fatalf("status = %s, want OK", c.Status)
	}
}

func TestCheckRoundBackupFlagRunningWithoutProcess(t *testing.T) {
	// tmux session up, no cs2 process under the server dir (still starting).
	c := checkRoundBackupFlag(DoctorMeta{CS2User: "csm-test-nouser", Servers: []int{1}}, func(int) bool { return true })
	if c.Status != DoctorWarn {
		t.Fatalf("status = %s, want WARN", c.Status)
	}
}
