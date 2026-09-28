package csm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

const testLicenseKey = "ATL1.eyJ2IjoxfQ.c2lnbmF0dXJl"

func TestWriteServerLicenseSetAndClear(t *testing.T) {
	dir := t.TempDir()
	serverCfg := filepath.Join(dir, "server.cfg")
	orig := "hostname \"CS2 Server #1\"\nrcon_password \"x\"\n"
	if err := os.WriteFile(serverCfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeServerLicense(dir, testLicenseKey, ""); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, readyUpLicenseCfg)
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `readyup_license_key "`+testLicenseKey+`"`) {
		t.Fatalf("cfg content:\n%s", data)
	}
	if fi, _ := os.Stat(cfgFile); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 600", fi.Mode().Perm())
	}
	sc, _ := os.ReadFile(serverCfg)
	if strings.Count(string(sc), "exec readyup_license.cfg") != 1 || !strings.HasPrefix(string(sc), orig) {
		t.Fatalf("server.cfg:\n%s", sc)
	}

	// Setting it again changes nothing in server.cfg (one exec line).
	if err := writeServerLicense(dir, testLicenseKey, ""); err != nil {
		t.Fatal(err)
	}
	sc2, _ := os.ReadFile(serverCfg)
	if string(sc2) != string(sc) {
		t.Fatalf("second set changed server.cfg:\n%s", sc2)
	}

	// Clearing removes the file and the exec line, and leaves the rest.
	if err := writeServerLicense(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgFile); !os.IsNotExist(err) {
		t.Fatalf("license cfg still there: %v", err)
	}
	sc3, _ := os.ReadFile(serverCfg)
	if strings.Contains(string(sc3), "readyup_license") || !strings.HasPrefix(string(sc3), orig) {
		t.Fatalf("server.cfg after clear:\n%s", sc3)
	}
	// Clearing twice is fine and leaves server.cfg alone.
	if err := writeServerLicense(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	sc4, _ := os.ReadFile(serverCfg)
	if string(sc4) != string(sc3) {
		t.Fatal("second clear changed server.cfg")
	}
}

func TestWriteServerLicenseRefusesNonKeys(t *testing.T) {
	dir := t.TempDir()
	if err := writeServerLicense(dir, `x"; rcon_password "pwned`, ""); err == nil {
		t.Fatal("a non-key was written into a cfg")
	}
	if _, err := os.Stat(filepath.Join(dir, readyUpLicenseCfg)); !os.IsNotExist(err) {
		t.Fatal("file written for a non-key")
	}
}

func TestWriteServerLicenseWithoutServerCfg(t *testing.T) {
	dir := t.TempDir()
	if err := writeServerLicense(dir, testLicenseKey, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.cfg")); !os.IsNotExist(err) {
		t.Fatal("server.cfg should not be created")
	}
}

func TestWithLicenseExecDedupes(t *testing.T) {
	in := "a\nexec readyup_license.cfg\nb\nexec readyup_license // old\n"
	got := withLicenseExec(in, true)
	if strings.Count(got, "exec readyup_license") != 1 || !strings.HasPrefix(got, "a\nb\n") {
		t.Fatalf("got %q", got)
	}
	if got := withLicenseExec("exec readyup_license_other.cfg\n", false); got != "exec readyup_license_other.cfg\n" {
		t.Fatalf("touched another exec: %q", got)
	}
}

func TestLicenseSettingsRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CSM_ROOT", root)
	s, err := LoadLicenseSettings()
	if err != nil || s.Key != "" {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	if err := saveLicenseSettings(LicenseSettings{Key: testLicenseKey}, "nobody"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "license.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", fi, err)
	}
	s, err = LoadLicenseSettings()
	if err != nil || s.Key != testLicenseKey {
		t.Fatalf("got %+v %v", s, err)
	}
}

func TestSetAndClearLicenseKey(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	t.Setenv("CS2_USER", "csm-test-user-that-does-not-exist")
	var out bytes.Buffer
	if _, err := SetLicenseKey(&out, "not a key"); err == nil {
		t.Fatal("stored a non-key")
	}
	s, err := SetLicenseKey(&out, "  "+testLicenseKey+"\n")
	if err != nil {
		t.Fatal(err)
	}
	// Stored even though it does not verify (unknown signing key): a warning, never a refusal.
	if !s.Set || s.Result.Valid {
		t.Fatalf("summary %+v", s)
	}
	stored, _ := LoadLicenseSettings()
	if stored.Key != testLicenseKey {
		t.Fatalf("stored %q", stored.Key)
	}
	if err := ClearLicenseKey(&out); err != nil {
		t.Fatal(err)
	}
	if stored, _ := LoadLicenseSettings(); stored.Key != "" {
		t.Fatal("key still stored")
	}
}

func TestLicenseSummaryLine(t *testing.T) {
	if got := (LicenseSummary{}).Line(); got != "No commercial license key set (free for non-commercial use: https://autotournament.gg/pricing)" {
		t.Fatalf("no key: %q", got)
	}
	p := &license.Payload{ID: "L-abc", Licensee: "NTLAN", Product: "servers", Pack: "S", MaxServers: 6, Kind: "event", ValidFrom: "2026-10-16", ValidTo: "2026-10-16", UpdatesUntil: "2026-10-16"}
	s := LicenseSummary{Set: true, ID: "L-abc", Result: license.Result{Valid: true, Status: license.StatusOK, License: p}}
	if got := s.Line(); got != "Licensed to NTLAN · Servers S (6 servers) · event 2026-10-16 · valid" {
		t.Fatalf("line: %q", got)
	}
	if got := s.VerifyLink(); got != "https://autotournament.gg/verify/L-abc" {
		t.Fatalf("link: %q", got)
	}
	rep := s.Report()
	if !strings.Contains(rep, "https://autotournament.gg/verify/L-abc") || strings.Contains(rep, "ATL1") {
		t.Fatalf("report:\n%s", rep)
	}

	s.Result.Status = license.StatusWarning
	s.Result.Warnings = []license.Message{{Code: license.CodeTooManyServers, Message: "8 servers set up; license covers 6."}}
	if got := s.Line(); !strings.HasSuffix(got, "· valid, 1 warning") {
		t.Fatalf("warning line: %q", got)
	}
	if rep := s.Report(); !strings.Contains(rep, "warning: 8 servers set up; license covers 6.") {
		t.Fatalf("report:\n%s", rep)
	}

	year := &license.Payload{ID: "L-y", Product: "platform", Pack: "M", MaxServers: 15, Kind: "year", UpdatesUntil: "2027-09-28"}
	s = LicenseSummary{Set: true, ID: "L-y", Result: license.Result{Valid: true, Status: license.StatusOK, License: year}}
	if got := s.Line(); got != "Licensed to license L-y · Platform M (15 servers) · updates until 2027-09-28 · valid" {
		t.Fatalf("year line: %q", got)
	}

	bad := LicenseSummary{Set: true, Result: license.Result{Status: license.StatusInvalid, Warnings: []license.Message{{Code: license.CodeBadSignature, Message: "The license key signature does not match."}}}}
	if got := bad.Line(); got != "License key not valid: The license key signature does not match." {
		t.Fatalf("invalid line: %q", got)
	}
}
