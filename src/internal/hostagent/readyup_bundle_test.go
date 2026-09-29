package hostagent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeReadyUpReleases serves Auto-Tournament/ready-up with the given
// releases (tag -> prerelease) and real, checksummed bundle zips. latest is
// what releases/latest answers ("" = 404, as while only pre-releases exist).
//
// The tags are visited in sorted order (never map order) and the list is served
// oldest version first with the oldest version published last, the opposite of
// what GitHub gives and of version order, so nothing can pass by relying on
// list position or publish time.
func fakeReadyUpReleases(t *testing.T, latest string, tags map[string]bool) *httptest.Server {
	t.Helper()
	files := map[string][]byte{}
	var rels []map[string]any
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
				if rel["tag_name"] == want && want != "" {
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
	sorted := make([]string, 0, len(tags))
	for tag := range tags {
		sorted = append(sorted, tag)
	}
	sort.Strings(sorted)
	day := 28
	for _, tag := range sorted {
		pre := tags[tag]
		v := strings.TrimPrefix(tag, "v")
		var assets []map[string]any
		sums := ""
		for _, b := range []string{"essentials", "full"} {
			name := "ready-up-" + b + "-" + v + "-linuxsteamrt64.zip"
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			f, _ := zw.Create("readyup/tools/install.sh")
			_, _ = f.Write([]byte("#!/bin/bash\n# " + b + " " + tag + "\n"))
			_ = zw.Close()
			files[tag+"/"+name] = buf.Bytes()
			h := sha256.Sum256(buf.Bytes())
			sums += hex.EncodeToString(h[:]) + "  " + name + "\n"
			assets = append(assets, map[string]any{"name": name, "browser_download_url": srv.URL + "/dl/" + tag + "/" + name})
		}
		files[tag+"/SHA256SUMS"] = []byte(sums)
		assets = append(assets, map[string]any{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/dl/" + tag + "/SHA256SUMS"})
		rels = append(rels, map[string]any{"tag_name": tag, "prerelease": pre, "assets": assets,
			"published_at": "2026-09-" + string(rune('0'+day/10)) + string(rune('0'+day%10)) + "T00:00:00Z"})
		day--
	}
	return srv
}

func TestPrepareFromGitHubRelease(t *testing.T) {
	gh := fakeReadyUpReleases(t, "v0.5.0", map[string]bool{"v0.5.0": false})
	f := &Fetcher{GitHubAPI: gh.URL, TempDir: t.TempDir(),
		Defaults: func() ReadyUpDefaults { return ReadyUpDefaults{AcceptLicense: "noncommercial"} }}

	plan, cleanup, err := f.Prepare(context.Background(), AgentConfig{}, "0.5.0", "default")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v0.5.0" || plan.Component != "essentials" || plan.AcceptLicense != "noncommercial" ||
		filepath.Base(plan.Zip) != "ready-up-essentials-0.5.0-linuxsteamrt64.zip" {
		t.Fatalf("plan = %+v", plan)
	}
	inst, err := os.ReadFile(plan.Installer)
	if err != nil || !strings.Contains(string(inst), "essentials v0.5.0") {
		t.Fatalf("installer from the bundle = %q %v", inst, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(plan.Zip), "SHA256SUMS")); err != nil {
		t.Fatalf("SHA256SUMS is not next to the zip for install.sh: %v", err)
	}
	cleanup()
	if _, err := os.Stat(plan.Zip); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s", plan.Zip)
	}

	// The agent's own license setting wins over the host's.
	plan, cleanup, err = f.Prepare(context.Background(), AgentConfig{ReadyUpAcceptLicense: "commercial"}, "latest", "skins")
	cleanup()
	if err != nil || plan.AcceptLicense != "commercial" || plan.Component != "full" || plan.Version != "v0.5.0" {
		t.Fatalf("latest/skins = %+v %v", plan, err)
	}
}

func TestPrepareLatestFollowsTheHostChannel(t *testing.T) {
	gh := fakeReadyUpReleases(t, "", map[string]bool{"v0.1.0-beta.1": true, "v0.1.0-beta.2": true})
	def := ReadyUpDefaults{}
	f := &Fetcher{GitHubAPI: gh.URL, TempDir: t.TempDir(), Defaults: func() ReadyUpDefaults { return def }}

	// Stable channel, only pre-releases: no_release naming the newest one.
	_, cleanup, err := f.Prepare(context.Background(), AgentConfig{}, "latest", "default")
	cleanup()
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != CodeNoRelease || !strings.Contains(pe.Msg, "no stable release yet") ||
		!strings.Contains(pe.Msg, "v0.1.0-beta.2") {
		t.Fatalf("stable = %v", err)
	}

	def.Channel = "beta"
	plan, cleanup, err := f.Prepare(context.Background(), AgentConfig{}, "latest", "default")
	cleanup()
	if err != nil || plan.Version != "v0.1.0-beta.2" {
		t.Fatalf("beta = %+v %v", plan, err)
	}

	// A pin on the host holds "latest" to it.
	def.Version = "v0.1.0-beta.1"
	plan, cleanup, err = f.Prepare(context.Background(), AgentConfig{}, "latest", "default")
	cleanup()
	if err != nil || plan.Version != "v0.1.0-beta.1" {
		t.Fatalf("pinned = %+v %v", plan, err)
	}

	// An explicit version from the platform wins over the pin.
	plan, cleanup, err = f.Prepare(context.Background(), AgentConfig{}, "0.1.0-beta.2", "default")
	cleanup()
	if err != nil || plan.Version != "v0.1.0-beta.2" {
		t.Fatalf("explicit = %+v %v", plan, err)
	}
}

func TestPrepareConfiguredSources(t *testing.T) {
	inst := filepath.Join(t.TempDir(), "install.sh")
	_ = os.WriteFile(inst, []byte("#!/bin/sh\n"), 0o755)
	f := &Fetcher{GitHubAPI: "http://127.0.0.1:1"}
	cfg := AgentConfig{ReadyUpInstaller: inst, ReadyUpBundle: "/does/not/exist-{version}.zip"}
	_, cleanup, err := f.Prepare(context.Background(), cfg, "0.5.0", "skins")
	cleanup()
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != CodeNoRelease || !strings.Contains(err.Error(), "exist-0.5.0.zip") {
		t.Fatalf("missing bundle = %v", err)
	}
	if _, err := SetConfig(Paths{Dir: t.TempDir()}, "readyup_bundle", "http://insecure.example.com/x.zip"); err == nil {
		t.Fatalf("http bundle accepted")
	}
	if _, err := SetConfig(Paths{Dir: t.TempDir()}, "readyup_accept_license", "yes"); err == nil {
		t.Fatalf("bad license choice accepted")
	}
}
