package csm

import (
	"reflect"
	"testing"
)

const testMonitorEntry = "*/5 * * * * /usr/local/bin/csm monitor >/dev/null 2>&1"

func TestStripMonitorCronLines(t *testing.T) {
	tab := "# m h dom mon dow command\n" +
		"0 3 * * * /usr/local/bin/backup.sh\n" +
		testMonitorEntry + "\n" +
		"@reboot /usr/bin/true\n"
	kept, removed := stripMonitorCronLines(tab)
	wantKept := "# m h dom mon dow command\n0 3 * * * /usr/local/bin/backup.sh\n@reboot /usr/bin/true\n"
	if kept != wantKept {
		t.Fatalf("kept = %q, want %q", kept, wantKept)
	}
	if !reflect.DeepEqual(removed, []string{testMonitorEntry}) {
		t.Fatalf("removed = %q", removed)
	}

	// Empty crontab and a crontab with only the monitor entry.
	if kept, removed := stripMonitorCronLines(""); kept != "" || removed != nil {
		t.Fatalf("empty: kept=%q removed=%q", kept, removed)
	}
	if kept, removed := stripMonitorCronLines(testMonitorEntry); kept != "" || len(removed) != 1 {
		t.Fatalf("only entry: kept=%q removed=%q", kept, removed)
	}
}

func TestWithMonitorCronLine(t *testing.T) {
	// Replaces an old entry (other interval / path), keeps other lines, and
	// always ends with a newline.
	tab := "0 3 * * * /usr/local/bin/backup.sh\n*/10 * * * * /opt/csm monitor\n"
	got := withMonitorCronLine(tab, testMonitorEntry)
	want := "0 3 * * * /usr/local/bin/backup.sh\n" + testMonitorEntry + "\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := withMonitorCronLine("", testMonitorEntry); got != testMonitorEntry+"\n" {
		t.Fatalf("empty tab: got %q", got)
	}
	// Idempotent.
	if again := withMonitorCronLine(want, testMonitorEntry); again != want {
		t.Fatalf("second install changed the crontab: %q", again)
	}
}

func TestMigrateMonitorCron(t *testing.T) {
	rootTab := "0 4 * * * /usr/sbin/logrotate /etc/logrotate.conf\n" + testMonitorEntry + "\n"
	userTab := "MAILTO=\"\"\n"

	newRoot, newUser, moved := migrateMonitorCron(rootTab, userTab)
	if !moved {
		t.Fatal("expected the entry to move")
	}
	if newRoot != "0 4 * * * /usr/sbin/logrotate /etc/logrotate.conf\n" {
		t.Fatalf("root crontab = %q", newRoot)
	}
	if newUser != "MAILTO=\"\"\n"+testMonitorEntry+"\n" {
		t.Fatalf("user crontab = %q", newUser)
	}

	// Second run: nothing left in root, user crontab untouched.
	r2, u2, moved2 := migrateMonitorCron(newRoot, newUser)
	if moved2 || r2 != newRoot || u2 != newUser {
		t.Fatalf("second migration changed something: moved=%v root=%q user=%q", moved2, r2, u2)
	}

	// The user already has an (older) entry: it is replaced, not duplicated.
	_, u3, _ := migrateMonitorCron(rootTab, "*/10 * * * * /usr/local/bin/csm monitor\n")
	if u3 != testMonitorEntry+"\n" {
		t.Fatalf("user crontab with old entry = %q", u3)
	}

	// Several root entries: the last one wins.
	_, u4, _ := migrateMonitorCron("*/10 * * * * /a/csm monitor\n"+testMonitorEntry+"\n", "")
	if u4 != testMonitorEntry+"\n" {
		t.Fatalf("several root entries: user crontab = %q", u4)
	}
}

func TestCrontabArgs(t *testing.T) {
	if got := crontabArgs("", "-l"); !reflect.DeepEqual(got, []string{"-l"}) {
		t.Fatalf("current user: %q", got)
	}
	if got := crontabArgs("cs2", "-"); !reflect.DeepEqual(got, []string{"-u", "cs2", "-"}) {
		t.Fatalf("other user: %q", got)
	}
}
