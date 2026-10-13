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

func TestCrontabArgs(t *testing.T) {
	if got := crontabArgs("", "-l"); !reflect.DeepEqual(got, []string{"-l"}) {
		t.Fatalf("current user: %q", got)
	}
	if got := crontabArgs("cs2", "-"); !reflect.DeepEqual(got, []string{"-u", "cs2", "-"}) {
		t.Fatalf("other user: %q", got)
	}
}
