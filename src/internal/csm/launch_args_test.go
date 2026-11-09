package csm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLaunchArgs(t *testing.T) {
	ok := [][]string{
		nil,
		{"-allow_round_backup_files"},
		{"+sv_setsteamaccount", "ABCDEF0123456789"},
		{"-tickrate", "128", "+sv_lan", "1", "+exec", "extra.cfg"},
		{"+host_workshop_collection", "123456"},
	}
	for _, a := range ok {
		if err := ValidateLaunchArgs(a); err != nil {
			t.Errorf("%q: %v", a, err)
		}
	}
	bad := [][]string{
		{"value-first"},
		{"-port", "27999"},        // csm's
		{"+MAP", "de_inferno"},    // csm's, any case
		{"+sv_tags", "a b"},       // space
		{"+say", "hi;rm"},         // shell
		{"+sv_hostname", "$(id)"}, // shell
		{"+sv_hostname", "`id`"},  // shell
		{"+sv_password", "\"x\""}, // quotes
		{"-x", ""},                // empty
		{"-" + strings.Repeat("a", MaxLaunchArgLen)},
	}
	for _, a := range bad {
		if err := ValidateLaunchArgs(a); err == nil {
			t.Errorf("%q: want an error", a)
		}
	}
	many := make([]string, MaxLaunchArgs+1)
	for i := range many {
		many[i] = "-x"
	}
	if ValidateLaunchArgs(many) == nil {
		t.Error("too many arguments accepted")
	}
}

func TestLaunchArgsFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := readLaunchArgs(dir); got != nil {
		t.Fatalf("no file: %q", got)
	}
	want := []string{"+sv_setsteamaccount", "TOKEN123", "-tickrate", "128"}
	if err := writeLaunchArgs(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := readLaunchArgs(dir); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("read %q, want %q", got, want)
	}
	if err := writeLaunchArgs(dir, []string{"-port", "1"}); err == nil {
		t.Fatal("a managed argument was saved")
	}
	if err := writeLaunchArgs(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, launchArgsFile)); !os.IsNotExist(err) {
		t.Fatal("clearing left the file")
	}
	// A hand-edited bad file starts the server without extras instead of failing.
	_ = os.WriteFile(filepath.Join(dir, launchArgsFile), []byte(`{"args":["+x","a;b"]}`), 0o644)
	if got := readLaunchArgs(dir); got != nil {
		t.Fatalf("bad file read as %q", got)
	}
}

func TestExtraLaunchArgsInBothLaunchLines(t *testing.T) {
	extra := []string{"+sv_setsteamaccount", "TOKEN123"}
	line := buildLaunchCommand(launchSpec{Mode: "binary", GamePort: 27015, TVPort: 27020, MaxPlayers: 12, Extra: extra})
	if !strings.HasSuffix(line, " "+roundBackupFlag+" +sv_setsteamaccount TOKEN123") {
		t.Fatalf("server-N line %q", line)
	}
	// An invalid list never reaches the shell line.
	line = buildLaunchCommand(launchSpec{Mode: "binary", GamePort: 27015, TVPort: 27020, MaxPlayers: 12, Extra: []string{"+x", "$(id)"}})
	if strings.Contains(line, "$(id)") {
		t.Fatalf("unsafe extra in %q", line)
	}
	args := instanceCS2Args(instanceLaunch{N: 1, Map: "de_dust2", MaxPlayers: 12, Extra: extra})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "+game_mode 1 +sv_setsteamaccount TOKEN123 +exec") || args[len(args)-2] != "+map" {
		t.Fatalf("instance args %q", args)
	}
}
