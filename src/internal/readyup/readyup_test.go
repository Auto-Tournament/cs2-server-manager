package readyup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves a Ready Up repo with the given releases (newest last)
// and their assets. latest is what releases/latest returns ("" = 404).
type fakeGitHub struct {
	srv      *httptest.Server
	releases []Release
	latest   string
	files    map[string][]byte
	hits     map[string]int
}

func bundleZip(t *testing.T, installer string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"readyup/manifests/core.json": `{"component":"core","version":"0.1.0-beta.1","files":[]}`,
		InstallerInZip:                installer,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumLine(data []byte, name string) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]) + "  " + name + "\n"
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{files: map[string][]byte{}, hits: map[string]int{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.hits[r.URL.Path]++
		const base = "/repos/Auto-Tournament/ready-up/releases"
		switch {
		case r.URL.Path == base:
			// GitHub lists newest first.
			out := make([]Release, 0, len(g.releases))
			for i := len(g.releases) - 1; i >= 0; i-- {
				out = append(out, g.releases[i])
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.URL.Path == base+"/latest":
			for _, rel := range g.releases {
				if rel.TagName == g.latest && g.latest != "" {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, base+"/tags/"):
			tag := strings.TrimPrefix(r.URL.Path, base+"/tags/")
			for _, rel := range g.releases {
				if rel.TagName == tag {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			data, ok := g.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// add publishes a release with essentials + full bundles and SHA256SUMS.
func (g *fakeGitHub) add(t *testing.T, tag string, pre bool, published string) {
	v := strings.TrimPrefix(tag, "v")
	rel := Release{TagName: tag, Prerelease: pre, PublishedAt: published}
	sums := ""
	for _, b := range []string{"essentials-plugin", "essentials", "full"} {
		name := "ready-up-" + b + "-" + v + "-linuxsteamrt64.zip"
		data := bundleZip(t, "#!/bin/bash\necho "+b+" "+tag+"\n")
		g.files[name] = data
		sums += sumLine(data, name)
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: g.srv.URL + "/dl/" + name})
	}
	g.files[tag+"/SHA256SUMS"] = []byte(sums)
	rel.Assets = append(rel.Assets, Asset{Name: SumsAsset, URL: g.srv.URL + "/dl/" + tag + "/SHA256SUMS"})
	g.releases = append(g.releases, rel)
}

func (g *fakeGitHub) client() *Client { return &Client{API: g.srv.URL} }

func TestResolveOnlyPreReleases(t *testing.T) {
	g := newFakeGitHub(t)
	g.add(t, "v0.1.0-beta.1", true, "2026-09-20T10:00:00Z")
	g.add(t, "v0.1.0-beta.2", true, "2026-09-27T10:00:00Z")
	ctx := context.Background()

	_, err := g.client().Resolve(ctx, "stable", "")
	if !IsNoRelease(err) || !strings.Contains(err.Error(), "no stable release yet") ||
		!strings.Contains(err.Error(), "v0.1.0-beta.2") || !strings.Contains(err.Error(), "csm plugins channel beta") {
		t.Fatalf("stable with only pre-releases = %v", err)
	}
	rel, err := g.client().Resolve(ctx, "beta", "latest")
	if err != nil || rel.TagName != "v0.1.0-beta.2" || !rel.Prerelease {
		t.Fatalf("beta = %+v %v", rel, err)
	}
	rel, err = g.client().Resolve(ctx, "stable", "0.1.0-beta.1")
	if err != nil || rel.TagName != "v0.1.0-beta.1" {
		t.Fatalf("pinned = %+v %v", rel, err)
	}
	if _, err := g.client().Resolve(ctx, "", "v9.9.9"); !IsNoRelease(err) || !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("missing pin = %v", err)
	}
}

func TestResolveStableAndBeta(t *testing.T) {
	g := newFakeGitHub(t)
	g.add(t, "v0.1.0", false, "2026-10-01T10:00:00Z")
	g.add(t, "v0.2.0-beta.1", true, "2026-10-05T10:00:00Z")
	g.latest = "v0.1.0"
	ctx := context.Background()
	if rel, err := g.client().Resolve(ctx, "", ""); err != nil || rel.TagName != "v0.1.0" {
		t.Fatalf("stable = %+v %v", rel, err)
	}
	if rel, err := g.client().Resolve(ctx, "beta", ""); err != nil || rel.TagName != "v0.2.0-beta.1" {
		t.Fatalf("beta = %+v %v", rel, err)
	}
}

func TestResolveNothing(t *testing.T) {
	g := newFakeGitHub(t)
	_, err := g.client().Resolve(context.Background(), "stable", "")
	if !IsNoRelease(err) || !strings.Contains(err.Error(), "no published release yet") {
		t.Fatalf("empty repo stable = %v", err)
	}
	_, err = g.client().Resolve(context.Background(), "beta", "")
	if !IsNoRelease(err) {
		t.Fatalf("empty repo beta = %v", err)
	}
}

func TestNormalize(t *testing.T) {
	if v, err := NormalizeVersion("0.1.0-beta.3"); err != nil || v != "v0.1.0-beta.3" {
		t.Fatalf("%q %v", v, err)
	}
	for _, bad := range []string{"1.0", "v1.0.0; rm -rf /", "vX"} {
		if _, err := NormalizeVersion(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if _, err := NormalizeChannel("nightly"); err == nil {
		t.Fatal("nightly accepted")
	}
	if b, _ := NormalizeBundle("skins"); b != BundleFull {
		t.Fatalf("skins -> %q", b)
	}
	if b, _ := NormalizeBundle("default"); b != BundleEssentials {
		t.Fatalf("default -> %q", b)
	}
}

func TestDownloadVerifiesAndExtractsInstaller(t *testing.T) {
	g := newFakeGitHub(t)
	g.add(t, "v0.1.0-beta.1", true, "2026-09-20T10:00:00Z")
	ctx := context.Background()
	rel, err := g.client().Resolve(ctx, "beta", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.client().Download(ctx, rel, "essentials", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(b.Zip) != "ready-up-essentials-0.1.0-beta.1-linuxsteamrt64.zip" {
		t.Fatalf("picked %s (the essentials plugin zip is not the bundle)", b.Zip)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(b.Zip), SumsAsset)); err != nil {
		t.Fatalf("SHA256SUMS not next to the zip: %v", err)
	}
	inst, err := os.ReadFile(b.Installer)
	if err != nil || !strings.Contains(string(inst), "essentials v0.1.0-beta.1") {
		t.Fatalf("installer = %q %v", inst, err)
	}

	// A tampered zip is refused and nothing is left behind.
	name := "ready-up-full-0.1.0-beta.1-linuxsteamrt64.zip"
	g.files[name] = append([]byte{}, g.files[name]...)
	g.files[name][len(g.files[name])-1] ^= 0xff
	tmp := t.TempDir()
	if _, err := g.client().Download(ctx, rel, "full", tmp); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered = %v", err)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("left %d entries behind", len(ents))
	}

	// No SHA256SUMS: refused.
	rel.Assets = rel.Assets[:len(rel.Assets)-1]
	if _, err := g.client().Download(ctx, rel, "essentials", t.TempDir()); err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("no sums = %v", err)
	}
}

func TestInstallCmdline(t *testing.T) {
	got := InstallArgs{Installer: "/tmp/x/install.sh", Bundle: "essentials", Dir: "/home/cs2/server-3",
		Zip: "/tmp/x/ready-up-essentials-0.1.0-linuxsteamrt64.zip", Version: "v0.1.0", AcceptLicense: "noncommercial"}.Cmdline()
	want := "NO_COLOR=1 bash '/tmp/x/install.sh' 'essentials' --dir '/home/cs2/server-3' --yes --zip '/tmp/x/ready-up-essentials-0.1.0-linuxsteamrt64.zip' --accept-license='noncommercial' </dev/null"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	got = InstallArgs{Installer: "i'.sh", Bundle: "full", Dir: "/d", Version: "v0.2.0"}.Cmdline()
	if !strings.Contains(got, `'i'\''.sh'`) || !strings.Contains(got, "--version 'v0.2.0'") || strings.Contains(got, "accept") {
		t.Fatalf("got %s", got)
	}
}

func TestReadInstalled(t *testing.T) {
	dir := t.TempDir()
	if in, err := ReadInstalled(dir); in != nil || err != nil {
		t.Fatalf("empty = %v %v", in, err)
	}
	p := filepath.Join(dir, "game", "csgo", "readyup")
	_ = os.MkdirAll(p, 0o755)
	_ = os.WriteFile(filepath.Join(p, "installed.json"), []byte(`{"components":{"core":"0.1.0-beta.2","match":"0.1.0-beta.2"}}`), 0o644)
	in, err := ReadInstalled(dir)
	if err != nil || in.Summary() != "0.1.0-beta.2 (core, match)" {
		t.Fatalf("summary = %q %v", in.Summary(), err)
	}
}

func TestCompareVersions(t *testing.T) {
	// Each row is strictly lower than the next.
	order := []string{
		"v0.1.0-alpha", "v0.1.0-alpha.1", "v0.1.0-alpha.beta", "v0.1.0-beta",
		"v0.1.0-beta.2", "v0.1.0-beta.10", "v0.1.0-rc.1", "v0.1.0",
		"v0.1.1", "v0.2.0-beta.1", "v0.2.0", "v0.10.0", "v1.0.0",
	}
	for i := range order {
		for j := range order {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			got, ok := CompareVersions(order[i], order[j])
			if !ok || got != want {
				t.Errorf("CompareVersions(%s, %s) = %d, %v; want %d", order[i], order[j], got, ok, want)
			}
		}
	}
	if c, ok := CompareVersions("0.1.0", "v0.1.0"); !ok || c != 0 {
		t.Errorf("v prefix should not matter: %d %v", c, ok)
	}
	if c, ok := CompareVersions("v1.0.0+abc", "v1.0.0+def"); !ok || c != 0 {
		t.Errorf("build metadata is ignored: %d %v", c, ok)
	}
	for _, bad := range []string{"", "latest", "v1", "v1.2", "v1.2.x", "v1.2.3-", "v1.2.3-a..b"} {
		if _, ok := CompareVersions(bad, "v1.0.0"); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestNewestIgnoresOrderAndTime(t *testing.T) {
	mk := func(tag string, pre bool, at string) Release {
		return Release{TagName: tag, Prerelease: pre, PublishedAt: at}
	}
	base := []Release{
		mk("v0.1.0-beta.2", true, "2026-09-01T00:00:00Z"), // highest version, published first
		mk("v0.1.0-beta.1", true, "2026-09-10T00:00:00Z"),
		mk("v0.1.0-alpha", true, "2026-09-20T00:00:00Z"),
	}
	// Every permutation gives the same answer.
	perms := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, p := range perms {
		rels := []Release{base[p[0]], base[p[1]], base[p[2]]}
		if got, ok := Newest(rels, true); !ok || got.TagName != "v0.1.0-beta.2" {
			t.Errorf("order %v: Newest = %q %v", p, got.TagName, ok)
		}
	}

	// A backported patch created after a newer minor does not win.
	rels := []Release{
		mk("v0.1.5", false, "2026-10-09T00:00:00Z"),
		mk("v0.2.0", false, "2026-10-01T00:00:00Z"),
	}
	if got, _ := Newest(rels, false); got.TagName != "v0.2.0" {
		t.Errorf("backport: Newest = %q", got.TagName)
	}
	// beta.10 beats beta.2; a final beats its rc; pre-releases excluded when asked.
	rels = []Release{mk("v1.0.0-beta.2", true, ""), mk("v1.0.0-beta.10", true, ""), mk("v0.9.0", false, "")}
	if got, _ := Newest(rels, true); got.TagName != "v1.0.0-beta.10" {
		t.Errorf("beta.10: Newest = %q", got.TagName)
	}
	if got, _ := Newest(rels, false); got.TagName != "v0.9.0" {
		t.Errorf("stable only: Newest = %q", got.TagName)
	}
	rels = []Release{mk("v1.0.0", false, ""), mk("v1.0.0-rc.1", true, "")}
	if got, _ := Newest(rels, true); got.TagName != "v1.0.0" {
		t.Errorf("final vs rc: Newest = %q", got.TagName)
	}
	// Drafts and empty tags are skipped; nothing left means not found.
	rels = []Release{{TagName: "v9.0.0", Draft: true}, {TagName: ""}}
	if _, ok := Newest(rels, true); ok {
		t.Error("drafts must be skipped")
	}
}

func TestResolveIgnoresListOrder(t *testing.T) {
	// Newest version was published first and listed last.
	g := newFakeGitHub(t)
	g.add(t, "v0.1.0-alpha", true, "2026-09-25T10:00:00Z")
	g.add(t, "v0.1.0-beta.1", true, "2026-09-20T10:00:00Z")
	g.add(t, "v0.1.0-beta.2", true, "2026-09-10T10:00:00Z")
	ctx := context.Background()
	_, err := g.client().Resolve(ctx, "stable", "")
	if !IsNoRelease(err) || !strings.Contains(err.Error(), "newest: v0.1.0-beta.2") {
		t.Fatalf("stable message = %v", err)
	}
	if rel, err := g.client().Resolve(ctx, "beta", ""); err != nil || rel.TagName != "v0.1.0-beta.2" {
		t.Fatalf("beta = %+v %v", rel, err)
	}
}
