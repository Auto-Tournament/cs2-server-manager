package csm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// fakeReadyUpGitHub serves Ready Up releases the way GitHub does. latest is
// the stable release ("" = releases/latest is 404, as while only
// pre-releases exist).
func fakeReadyUpGitHub(t *testing.T, latest string, rels ...readyup.Release) *httptest.Server {
	t.Helper()
	files := map[string][]byte{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const base = "/repos/Auto-Tournament/ready-up/releases"
		switch {
		case r.URL.Path == base:
			_ = json.NewEncoder(w).Encode(rels)
		case r.URL.Path == base+"/latest" || strings.HasPrefix(r.URL.Path, base+"/tags/"):
			want := latest
			if strings.HasPrefix(r.URL.Path, base+"/tags/") {
				want = strings.TrimPrefix(r.URL.Path, base+"/tags/")
			}
			for _, rel := range rels {
				if want != "" && rel.TagName == want {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			if data, ok := files[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok {
				_, _ = w.Write(data)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	for i := range rels {
		v := strings.TrimPrefix(rels[i].TagName, "v")
		name := "ready-up-essentials-" + v + "-linuxsteamrt64.zip"
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create(readyup.InstallerInZip)
		_, _ = f.Write([]byte("#!/bin/bash\n"))
		_ = zw.Close()
		h := sha256.Sum256(buf.Bytes())
		files[rels[i].TagName+"/"+name] = buf.Bytes()
		files[rels[i].TagName+"/SHA256SUMS"] = []byte(hex.EncodeToString(h[:]) + "  " + name + "\n")
		rels[i].Assets = []readyup.Asset{
			{Name: name, URL: srv.URL + "/dl/" + rels[i].TagName + "/" + name},
			{Name: "SHA256SUMS", URL: srv.URL + "/dl/" + rels[i].TagName + "/SHA256SUMS"},
		}
	}
	t.Setenv(EnvReadyUpAPI, srv.URL)
	return srv
}

func pluginTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CSM_ROOT", t.TempDir())
	t.Setenv("CS2_USER", "csm-test-no-such-user")
	for _, k := range []string{EnvPluginStack, EnvReadyUpChannel, EnvReadyUpVersion, EnvReadyUpBundle, EnvAcceptLicense, EnvPlatformURL, EnvPlatformToken} {
		t.Setenv(k, "")
	}
}

func TestPluginSettingsRoundTrip(t *testing.T) {
	pluginTestEnv(t)
	r := PluginSettings{}.Resolved()
	if r.Stack != "" || r.ReadyUpChannel != "stable" || r.ReadyUpBundle != "essentials" || r.AcceptLicense != "" || !r.AutoUpdateOn() {
		t.Fatalf("defaults = %+v", r)
	}
	for _, kv := range [][2]string{{"stack", "readyup"}, {"channel", "beta"}, {"version", "0.1.0-beta.2"}, {"bundle", "full"}, {"license", "noncommercial"}, {"auto", "off"}} {
		if _, err := SetPluginSetting(kv[0], kv[1]); err != nil {
			t.Fatalf("%s %s: %v", kv[0], kv[1], err)
		}
	}
	s, err := LoadPluginSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Stack != "readyup" || s.StackSetBy != "operator" || s.ReadyUpChannel != "beta" || s.ReadyUpVersion != "v0.1.0-beta.2" ||
		s.ReadyUpBundle != "full" || s.AcceptLicense != "noncommercial" || s.AcceptLicenseBy != "operator" || s.AutoUpdateOn() {
		t.Fatalf("saved = %+v", s)
	}
	for _, bad := range [][2]string{{"stack", "metamod"}, {"channel", "nightly"}, {"version", "1.0"}, {"license", "yes"}, {"auto", "maybe"}, {"colour", "red"}} {
		if _, err := SetPluginSetting(bad[0], bad[1]); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	// The environment wins, like on the platform.
	t.Setenv(EnvAcceptLicense, "commercial")
	t.Setenv(EnvPluginStack, "legacy")
	if r := s.Resolved(); r.AcceptLicense != "commercial" || r.Stack != "legacy" {
		t.Fatalf("env = %+v", r)
	}
	if _, err := SetPluginSetting("version", "latest"); err != nil {
		t.Fatal(err)
	}
	if s, _ := LoadPluginSettings(); s.ReadyUpVersion != "" {
		t.Fatalf("latest did not clear the pin: %q", s.ReadyUpVersion)
	}
}

func TestPluginStackFreshInstall(t *testing.T) {
	pluginTestEnv(t)
	fakeReadyUpGitHub(t, "", readyup.Release{TagName: "v0.1.0-beta.1", Prerelease: true, PublishedAt: "2026-09-28T00:00:00Z"})
	var log bytes.Buffer
	stack, why := PluginStack(context.Background(), &log, "csm-test-no-such-user")
	if stack != PluginStackLegacy || !strings.Contains(why, "no stable") || !strings.Contains(log.String(), "csm plugins channel beta") {
		t.Fatalf("pre-releases only = %s (%s)\n%s", stack, why, log.String())
	}
	if s, _ := LoadPluginSettings(); s.Stack != "" {
		t.Fatalf("a legacy fallback was saved: %+v", s)
	}

	fakeReadyUpGitHub(t, "v0.1.0", readyup.Release{TagName: "v0.1.0", PublishedAt: "2026-10-01T00:00:00Z"})
	stack, _ = PluginStack(context.Background(), io.Discard, "csm-test-no-such-user")
	if s, _ := LoadPluginSettings(); stack != PluginStackReadyUp || s.Stack != PluginStackReadyUp || s.StackSetBy != "fresh-install" {
		t.Fatalf("stable release = %s %+v", stack, s)
	}
	// Once chosen it is not asked again.
	t.Setenv(EnvReadyUpAPI, "http://127.0.0.1:1")
	if stack, _ := PluginStack(context.Background(), io.Discard, "csm-test-no-such-user"); stack != PluginStackReadyUp {
		t.Fatalf("saved choice ignored: %s", stack)
	}
}

func TestReadyUpInstallNeedsALicenseAnswer(t *testing.T) {
	pluginTestEnv(t)
	fakeReadyUpGitHub(t, "", readyup.Release{TagName: "v0.1.0-beta.1", Prerelease: true, PublishedAt: "2026-09-28T00:00:00Z"})
	s := PluginSettings{Stack: PluginStackReadyUp, ReadyUpChannel: "beta"}
	if _, err := ReadyUpPlanFor(context.Background(), io.Discard, s); !errors.As(err, &ReadyUpLicenseError{}) {
		t.Fatalf("no license = %v", err)
	}
	s.AcceptLicense = "noncommercial"
	b, err := ReadyUpPlanFor(context.Background(), io.Discard, s)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(b.Dir)

	var cmds []string
	old := runReadyUpInstaller
	runReadyUpInstaller = func(_ context.Context, user, cmdline string, w io.Writer) error {
		cmds = append(cmds, user+": "+cmdline)
		if strings.Contains(cmdline, "server-2") {
			return errors.New("exit status 1")
		}
		return nil
	}
	defer func() { runReadyUpInstaller = old }()
	err = installReadyUpOn(context.Background(), io.Discard, "cs2", b, "noncommercial",
		[]readyUpTarget{{1, "/home/cs2/server-1"}, {2, "/home/cs2/server-2"}, {3, "/home/cs2/server-3"}})
	if err == nil || !strings.Contains(err.Error(), "server-2") || !strings.Contains(err.Error(), "already done: server-1") {
		t.Fatalf("err = %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("kept going after a failure: %v", cmds)
	}
	for _, want := range []string{"cs2: NO_COLOR=1 bash '", "'essentials' --dir '/home/cs2/server-1' --yes --zip '", "--accept-license='noncommercial'"} {
		if !strings.Contains(cmds[0], want) {
			t.Fatalf("cmd %q lacks %q", cmds[0], want)
		}
	}
	if strings.Contains(cmds[0], "sv_setsteamaccount") || strings.Contains(cmds[0], "--license-key") {
		t.Fatalf("cmd carries a secret or GSLT: %s", cmds[0])
	}
}

func TestDecideReadyUpUpdate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := readyUpServerCheck{Installed: "0.1.0-beta.1", Target: "v0.1.0-beta.2", Running: true,
		SafeKnown: true, Safe: true, Now: now, Grace: 10 * time.Minute, IdleSince: now.Add(-11 * time.Minute)}
	cases := []struct {
		name   string
		mut    func(c *readyUpServerCheck)
		update bool
		reason string
	}{
		{"idle past grace", func(c *readyUpServerCheck) {}, true, "idle"},
		{"current", func(c *readyUpServerCheck) { c.Installed = "0.1.0-beta.2" }, false, "current"},
		{"not installed", func(c *readyUpServerCheck) { c.Installed = "" }, false, "not installed"},
		{"hold", func(c *readyUpServerCheck) { c.Hold = UpdateHold{On: true, Reason: "tournament running"} }, false, "on hold"},
		{"match live", func(c *readyUpServerCheck) { c.Safe = false }, false, "update_safe=false"},
		{"no answer", func(c *readyUpServerCheck) { c.SafeKnown = false }, false, "no update_safe"},
		{"players", func(c *readyUpServerCheck) { c.Players = 3 }, false, "3 player"},
		{"grace", func(c *readyUpServerCheck) { c.IdleSince = now.Add(-time.Minute) }, false, "grace"},
		{"stopped", func(c *readyUpServerCheck) { c.Running = false; c.SafeKnown = false }, true, "stopped"},
		{"stopped but held", func(c *readyUpServerCheck) { c.Running = false; c.Hold = UpdateHold{On: true} }, false, "on hold"},
		{"failed recently", func(c *readyUpServerCheck) { c.LastTry = now.Add(-5 * time.Minute); c.LastFor = c.Target }, false, "last try failed"},
		{"failed for an older target", func(c *readyUpServerCheck) { c.LastTry = now.Add(-5 * time.Minute); c.LastFor = "v0.1.0-beta.1" }, true, "idle"},
	}
	for _, tc := range cases {
		c := base
		tc.mut(&c)
		d := decideReadyUpUpdate(c)
		if d.Update != tc.update || !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("%s: %+v", tc.name, d)
		}
	}
}

func TestReadyUpAutoTargetIsCached(t *testing.T) {
	pluginTestEnv(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`[{"tag_name":"v0.1.0-beta.3","prerelease":true,"published_at":"2026-09-28T00:00:00Z"}]`))
	}))
	defer srv.Close()
	t.Setenv(EnvReadyUpAPI, srv.URL)
	st := &readyUpAutoState{}
	r := PluginSettings{ReadyUpChannel: "beta"}.Resolved()
	now := time.Now()
	for i := 0; i < 3; i++ {
		if tag, err := readyUpAutoTarget(context.Background(), st, r, now.Add(time.Duration(i)*time.Minute)); err != nil || tag != "v0.1.0-beta.3" {
			t.Fatalf("target = %q %v", tag, err)
		}
	}
	if hits != 1 {
		t.Fatalf("GitHub asked %d times within the check interval", hits)
	}
	if _, err := readyUpAutoTarget(context.Background(), st, r, now.Add(readyUpCheckEvery+time.Minute)); err != nil || hits != 2 {
		t.Fatalf("not asked again after the interval: hits=%d %v", hits, err)
	}
	r.ReadyUpVersion = "v0.1.0-beta.1"
	_, _ = readyUpAutoTarget(context.Background(), st, r, now.Add(readyUpCheckEvery+2*time.Minute))
	if hits != 3 {
		t.Fatalf("a new pin did not trigger a lookup: hits=%d", hits)
	}
}

func TestAdoptPlatformLicenseUse(t *testing.T) {
	pluginTestEnv(t)
	var w bytes.Buffer
	adoptPlatformLicenseUse(&w, &PlatformLicense{Revision: "none"})
	if s, _ := LoadPluginSettings(); s.AcceptLicense != "" {
		t.Fatalf("adopted without a key or use: %+v", s)
	}
	adoptPlatformLicenseUse(&w, &PlatformLicense{Use: "noncommercial"})
	if s, _ := LoadPluginSettings(); s.AcceptLicense != "noncommercial" || s.AcceptLicenseBy != "platform" {
		t.Fatalf("use not adopted: %+v", s)
	}
	// An answer on record is never replaced.
	adoptPlatformLicenseUse(&w, &PlatformLicense{Use: "commercial"})
	if s, _ := LoadPluginSettings(); s.AcceptLicense != "noncommercial" {
		t.Fatalf("answer replaced: %+v", s)
	}
}

func TestPromptReadyUpLicense(t *testing.T) {
	pluginTestEnv(t)
	var out bytes.Buffer
	if use, err := PromptReadyUpLicense(strings.NewReader("3\n1\nno\n"), &out); use != "" || err != nil {
		t.Fatalf("declined = %q %v", use, err)
	}
	if s, _ := LoadPluginSettings(); s.AcceptLicense != "" {
		t.Fatalf("saved without agreeing: %+v", s)
	}
	if use, err := PromptReadyUpLicense(strings.NewReader("2\n i agree \n"), &out); use != "commercial" || err != nil {
		t.Fatalf("agreed = %q %v", use, err)
	}
	if s, _ := LoadPluginSettings(); s.AcceptLicense != "commercial" {
		t.Fatalf("not saved: %+v", s)
	}
	if !strings.Contains(out.String(), "PolyForm Noncommercial") || !strings.Contains(out.String(), "please enter 1 or 2") {
		t.Fatalf("prompt text:\n%s", out.String())
	}
}

func TestLegacyPluginIsPinned(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/repos/Auto-Tournament/cs2-plugin/releases/tags/v1.4.35":
			_, _ = w.Write([]byte(`{"tag_name":"v1.4.35","assets":[{"name":"MatchZy-1.4.35.zip","browser_download_url":"https://example.invalid/MatchZy-1.4.35.zip"}]}`))
		case "/repos/Auto-Tournament/cs2-plugin/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","assets":[{"name":"AutoTournamentCS2-2.0.0.zip","browser_download_url":"https://example.invalid/AutoTournamentCS2-2.0.0.zip"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = old }()
	t.Setenv(legacyPluginVersionEnv, "")

	up := &PluginUpdater{}
	rel, u, err := up.resolveLegacyPlugin(io.Discard)
	if err != nil || rel.TagName != LegacyPluginPinnedVersion || !strings.HasSuffix(u, "MatchZy-1.4.35.zip") {
		t.Fatalf("pinned = %s %s %v", rel.TagName, u, err)
	}
	for _, p := range asked {
		if strings.HasSuffix(p, "/latest") {
			t.Fatalf("asked for releases/latest: %v", asked)
		}
	}
	t.Setenv(legacyPluginVersionEnv, "latest")
	if rel, _, err := up.resolveLegacyPlugin(io.Discard); err != nil || rel.TagName != "v2.0.0" {
		t.Fatalf("latest override = %s %v", rel.TagName, err)
	}
}

func TestCheckLegacyPlatform(t *testing.T) {
	version := "3.0.0-beta.13"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != platformVersionPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"version":"` + version + `"}`))
	}))
	defer srv.Close()
	err := checkLegacyPlatform(context.Background(), io.Discard, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "only talks to Ready Up") || !strings.Contains(err.Error(), "csm plugins stack readyup") {
		t.Fatalf("3.x = %v", err)
	}
	version = "2.4.15"
	if err := checkLegacyPlatform(context.Background(), io.Discard, srv.URL); err != nil {
		t.Fatalf("2.x = %v", err)
	}
	var w bytes.Buffer
	if err := checkLegacyPlatform(context.Background(), &w, "http://127.0.0.1:1"); err != nil || !strings.Contains(w.String(), "anyway") {
		t.Fatalf("unreachable = %v %q", err, w.String())
	}
	if err := checkLegacyPlatform(context.Background(), io.Discard, ""); err != nil {
		t.Fatalf("no platform = %v", err)
	}
}

// Instance mode builds its shared layer with the full bundle unless the
// operator chose one; classic servers keep essentials as the default.
func TestLayerBundle(t *testing.T) {
	pluginTestEnv(t)
	s := PluginSettings{}
	if got := s.LayerBundle(); got != "full" {
		t.Fatalf("default layer bundle = %q, want full", got)
	}
	if got := s.Resolved().ReadyUpBundle; got != "essentials" {
		t.Fatalf("classic default = %q, want essentials", got)
	}
	for _, c := range []struct{ requested, want string }{{"default", "full"}, {"", "full"}, {"essentials", "full"}, {"skins", "full"}, {"full", "full"}} {
		if got := s.LayerBundleFor(c.requested); got != c.want {
			t.Fatalf("LayerBundleFor(%q) = %q, want %q", c.requested, got, c.want)
		}
	}
	// An operator's choice wins, but the platform can still ask for full.
	s.ReadyUpBundle = "essentials"
	if got := s.LayerBundle(); got != "essentials" {
		t.Fatalf("chosen essentials: layer bundle = %q", got)
	}
	if got := s.LayerBundleFor("default"); got != "essentials" {
		t.Fatalf("chosen essentials, platform default: %q", got)
	}
	if got := s.LayerBundleFor("skins"); got != "full" {
		t.Fatalf("chosen essentials, platform skins: %q", got)
	}
	t.Setenv(EnvReadyUpBundle, "full")
	if got := s.LayerBundle(); got != "full" {
		t.Fatalf("%s=full: %q", EnvReadyUpBundle, got)
	}
}

// The host agent asks the backend which bundle to install: instance hosts
// get the layer bundle, server-N hosts what the platform asked for.
func TestHostBackendBundleFor(t *testing.T) {
	pluginTestEnv(t)
	b := &HostBackend{}
	t.Setenv(EnvServerBackend, "servers")
	if got := b.ReadyUpBundleFor("default"); got != "default" {
		t.Fatalf("classic: %q, want default", got)
	}
	t.Setenv(EnvServerBackend, ServerBackendInstances)
	if got := b.ReadyUpBundleFor("default"); got != "full" {
		t.Fatalf("instances: %q, want full", got)
	}
}
