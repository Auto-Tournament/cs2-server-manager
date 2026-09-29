// Package readyup finds, downloads and verifies Ready Up releases.
//
// Ready Up (github.com/Auto-Tournament/ready-up) publishes every release on
// GitHub with component zips, two bundles and a SHA256SUMS file:
//
//	ready-up-essentials-<v>-linuxsteamrt64.zip   core + essentials + match + fleet + practice
//	ready-up-full-<v>-linuxsteamrt64.zip         everything, skins included
//	SHA256SUMS
//
// Versions are SemVer: vX.Y.Z is a stable release (marked "latest" on GitHub),
// vX.Y.Z-beta.N and vX.Y.Z-rc.N are pre-releases and never "latest". So there
// are two channels, as in Ready Up's install.sh:
//
//   - stable: GitHub's releases/latest. While Ready Up has only pre-releases
//     this is a 404, and Resolve says so and names the newest pre-release.
//   - beta: the newest published release, pre-releases included.
//
// A pinned version (vX.Y.Z or vX.Y.Z-beta.N) overrides the channel.
//
// csm downloads the bundle once, checks it against SHA256SUMS, and hands it to
// the install.sh that ships inside the bundle (readyup/tools/install.sh) with
// --zip, once per server. install.sh checks the zip again, lays out the files,
// keeps cfg/ReadyUp/* and patches gameinfo.gi. Nothing here touches a server.
package readyup

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Defaults.
const (
	DefaultRepo = "Auto-Tournament/ready-up"
	DefaultAPI  = "https://api.github.com"

	ChannelStable = "stable"
	ChannelBeta   = "beta"

	BundleEssentials = "essentials"
	BundleFull       = "full"

	// InstallerInZip is install.sh's path inside every bundle (core ships it).
	InstallerInZip = "readyup/tools/install.sh"
	// SumsAsset is the checksum file of a release.
	SumsAsset = "SHA256SUMS"

	maxDownloadBytes = 512 << 20
)

// Error codes.
const (
	CodeNoRelease = "no_release"
	CodeDownload  = "download_failed"
	CodeChecksum  = "checksum_failed"
)

// Error is a failure with a machine-readable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// IsNoRelease reports whether err means "no such Ready Up release".
func IsNoRelease(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeNoRelease
}

// Asset is one file of a GitHub release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is the part of a GitHub release csm uses.
type Release struct {
	TagName     string  `json:"tag_name"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	PublishedAt string  `json:"published_at"`
	CreatedAt   string  `json:"created_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets"`
}

func (r Release) time() time.Time {
	for _, s := range []string{r.PublishedAt, r.CreatedAt} {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

var versionRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]{0,31})?$`)

// NormalizeChannel returns stable or beta ("" is stable).
func NormalizeChannel(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ChannelStable:
		return ChannelStable, nil
	case ChannelBeta, "pre", "prerelease":
		return ChannelBeta, nil
	}
	return "", fmt.Errorf("channel is stable or beta (got %q)", s)
}

// NormalizeVersion returns "" for "" / "latest" (follow the channel) and
// vX.Y.Z[-suffix] for a version, with the leading v added.
func NormalizeVersion(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "latest") {
		return "", nil
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	if !versionRe.MatchString(s) {
		return "", fmt.Errorf("version is vX.Y.Z or vX.Y.Z-beta.N (got %q)", s)
	}
	return s, nil
}

// NormalizeBundle returns essentials or full. The host protocol's names are
// accepted too: default = essentials, skins = full.
func NormalizeBundle(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", BundleEssentials, "default":
		return BundleEssentials, nil
	case BundleFull, "skins":
		return BundleFull, nil
	}
	return "", fmt.Errorf("bundle is essentials or full (got %q)", s)
}

// Client talks to the GitHub releases API.
type Client struct {
	HTTP *http.Client
	// API is the GitHub API base URL (default DefaultAPI).
	API string
	// Repo is owner/name (default DefaultRepo).
	Repo string
	// UserAgent is sent with every request (default "csm").
	UserAgent string
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (c *Client) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return DefaultAPI
}

func (c *Client) repo() string {
	if c.Repo != "" {
		return c.Repo
	}
	return DefaultRepo
}

func (c *Client) ua() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return "csm"
}

// get fetches a URL. A 404 is (nil, 404, nil).
func (c *Client) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.ua())
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" && strings.HasPrefix(u, DefaultAPI) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	return body, resp.StatusCode, nil
}

func (c *Client) getRelease(ctx context.Context, u string) (Release, int, error) {
	var rel Release
	body, code, err := c.get(ctx, u)
	if err != nil {
		return rel, code, &Error{Code: CodeNoRelease, Msg: "could not ask GitHub for Ready Up releases: " + err.Error()}
	}
	if code != http.StatusOK {
		return rel, code, nil
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return rel, code, &Error{Code: CodeNoRelease, Msg: "GitHub's answer for " + u + " is not a release: " + err.Error()}
	}
	return rel, code, nil
}

// List returns the newest releases (drafts left out).
func (c *Client) List(ctx context.Context) ([]Release, error) {
	u := c.api() + "/repos/" + c.repo() + "/releases?per_page=30"
	body, code, err := c.get(ctx, u)
	if err != nil {
		return nil, &Error{Code: CodeNoRelease, Msg: "could not list Ready Up releases: " + err.Error()}
	}
	if code != http.StatusOK {
		return nil, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf("GitHub answered %d when listing Ready Up releases (%s)", code, c.repo())}
	}
	var rels []Release
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, &Error{Code: CodeNoRelease, Msg: "GitHub's release list is not JSON: " + err.Error()}
	}
	out := rels[:0]
	for _, r := range rels {
		if !r.Draft && r.TagName != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// Newest returns the most recently published release, pre-releases included
// when pre is true (install.sh --channel beta picks the same one).
func Newest(rels []Release, pre bool) (Release, bool) {
	var best Release
	found := false
	for _, r := range rels {
		if r.Draft || r.TagName == "" || (r.Prerelease && !pre) {
			continue
		}
		if !found || r.time().After(best.time()) {
			best, found = r, true
		}
	}
	return best, found
}

// Resolve picks the release to install: version when set (vX.Y.Z or
// vX.Y.Z-beta.N), else the newest on channel. Every "there is none" answer is
// an *Error with CodeNoRelease whose message says what to do.
func (c *Client) Resolve(ctx context.Context, channel, version string) (Release, error) {
	v, err := NormalizeVersion(version)
	if err != nil {
		return Release{}, &Error{Code: CodeNoRelease, Msg: err.Error()}
	}
	ch, err := NormalizeChannel(channel)
	if err != nil {
		return Release{}, &Error{Code: CodeNoRelease, Msg: err.Error()}
	}
	base := c.api() + "/repos/" + c.repo() + "/releases"

	if v != "" {
		rel, code, err := c.getRelease(ctx, base+"/tags/"+url.PathEscape(v))
		if err != nil {
			return rel, err
		}
		if code == http.StatusNotFound || (code == http.StatusOK && rel.Draft) {
			return rel, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(
				"Ready Up has no published release %s (https://github.com/%s/releases)", v, c.repo())}
		}
		if code != http.StatusOK {
			return rel, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf("GitHub answered %d for Ready Up %s", code, v)}
		}
		return rel, nil
	}

	if ch == ChannelBeta {
		rels, err := c.List(ctx)
		if err != nil {
			return Release{}, err
		}
		rel, ok := Newest(rels, true)
		if !ok {
			return rel, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(
				"Ready Up has no published release yet, not even a pre-release (https://github.com/%s/releases)", c.repo())}
		}
		return rel, nil
	}

	rel, code, err := c.getRelease(ctx, base+"/latest")
	if err != nil {
		return rel, err
	}
	if code == http.StatusOK && rel.TagName != "" && !rel.Prerelease {
		return rel, nil
	}
	if code != http.StatusOK && code != http.StatusNotFound {
		return rel, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf("GitHub answered %d for the latest Ready Up release", code)}
	}
	// releases/latest is 404 while there are only pre-releases.
	if rels, lerr := c.List(ctx); lerr == nil {
		if pre, ok := Newest(rels, true); ok {
			return Release{}, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(
				"Ready Up has no stable release yet, only pre-releases (newest: %s). "+
					"To install pre-releases use the beta channel (csm plugins channel beta), "+
					"or pin one (csm plugins version %s). Pre-releases are for testing.", pre.TagName, pre.TagName)}
		}
	}
	return Release{}, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(
		"Ready Up has no published release yet (https://github.com/%s/releases)", c.repo())}
}

// BundleAsset finds the bundle zip in a release. ready-up-essentials-plugin-*
// is the essentials component, not the bundle.
func BundleAsset(rel Release, bundle string) (Asset, error) {
	b, err := NormalizeBundle(bundle)
	if err != nil {
		return Asset{}, err
	}
	prefix := "ready-up-" + b + "-"
	for _, a := range rel.Assets {
		if !strings.HasPrefix(a.Name, prefix) || !strings.HasSuffix(a.Name, ".zip") {
			continue
		}
		if strings.HasPrefix(a.Name, "ready-up-essentials-plugin-") {
			continue
		}
		return a, nil
	}
	return Asset{}, &Error{Code: CodeNoRelease, Msg: fmt.Sprintf("Ready Up %s has no %s bundle (%s*.zip)", rel.TagName, b, prefix)}
}

func findAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Bundle is a downloaded, verified Ready Up bundle.
type Bundle struct {
	Tag        string
	Prerelease bool
	// Name is essentials or full.
	Name string
	// Dir holds Zip, SHA256SUMS and Installer. Remove it when done.
	Dir string
	// Zip is the bundle zip; SHA256SUMS sits next to it, so install.sh --zip
	// verifies it again.
	Zip string
	// Installer is install.sh from the bundle.
	Installer string
}

// Download fetches the bundle and SHA256SUMS of rel into a new directory
// under tempDir ("" = the system temp dir), verifies the zip and extracts
// install.sh. A release without SHA256SUMS, or a zip that does not match it,
// is refused. The directory and files are world-readable, because install.sh
// runs as the CS2 user while csm may run as root.
func (c *Client) Download(ctx context.Context, rel Release, bundle, tempDir string) (*Bundle, error) {
	b, err := NormalizeBundle(bundle)
	if err != nil {
		return nil, err
	}
	zipAsset, err := BundleAsset(rel, b)
	if err != nil {
		return nil, err
	}
	sumsAsset, ok := findAsset(rel, SumsAsset)
	if !ok {
		return nil, &Error{Code: CodeChecksum, Msg: fmt.Sprintf("Ready Up %s has no SHA256SUMS; csm does not install unverified zips", rel.TagName)}
	}
	dir, err := os.MkdirTemp(tempDir, "readyup-"+b+"-*")
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o755)
	fail := func(e error) (*Bundle, error) {
		_ = os.RemoveAll(dir)
		return nil, e
	}
	zipPath := filepath.Join(dir, filepath.Base(zipAsset.Name))
	sumsPath := filepath.Join(dir, SumsAsset)
	if err := c.fetchFile(ctx, sumsAsset.URL, sumsPath); err != nil {
		return fail(&Error{Code: CodeDownload, Msg: "downloading SHA256SUMS: " + err.Error()})
	}
	if err := c.fetchFile(ctx, zipAsset.URL, zipPath); err != nil {
		return fail(&Error{Code: CodeDownload, Msg: "downloading " + zipAsset.Name + ": " + err.Error()})
	}
	if err := VerifySHA256(zipPath, sumsPath); err != nil {
		return fail(err)
	}
	inst := filepath.Join(dir, "install.sh")
	if err := ExtractFile(zipPath, InstallerInZip, inst, 0o755); err != nil {
		return fail(&Error{Code: CodeDownload, Msg: fmt.Sprintf("%s has no %s: %v", zipAsset.Name, InstallerInZip, err)})
	}
	return &Bundle{Tag: rel.TagName, Prerelease: rel.Prerelease, Name: b, Dir: dir, Zip: zipPath, Installer: inst}, nil
}

func (c *Client) fetchFile(ctx context.Context, src, dst string) error {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("not a download URL: %q", src)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua())
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download answered %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownloadBytes {
		err = errors.New("download is over 512 MiB")
	}
	if err == nil && n == 0 {
		err = errors.New("download is empty")
	}
	return err
}

// VerifySHA256 checks file against its line in a SHA256SUMS file
// ("<hex>  <name>" or "<hex> *<name>").
func VerifySHA256(file, sumsFile string) error {
	name := filepath.Base(file)
	f, err := os.Open(sumsFile)
	if err != nil {
		return &Error{Code: CodeChecksum, Msg: "reading SHA256SUMS: " + err.Error()}
	}
	defer f.Close()
	want := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return &Error{Code: CodeChecksum, Msg: name + " is not listed in SHA256SUMS; csm does not install unverified zips"}
	}
	got, err := fileSHA256(file)
	if err != nil {
		return &Error{Code: CodeChecksum, Msg: err.Error()}
	}
	if got != want {
		return &Error{Code: CodeChecksum, Msg: fmt.Sprintf("checksum mismatch for %s (SHA256SUMS says %s, the download is %s)", name, want, got)}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ExtractFile copies one file out of a zip.
func ExtractFile(zipPath, name, dst string, mode os.FileMode) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(rc, 16<<20)); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dst, mode)
	}
	return os.ErrNotExist
}

// InstallArgs are the options of one install.sh run.
type InstallArgs struct {
	// Installer is the path of install.sh.
	Installer string
	// Bundle is essentials or full.
	Bundle string
	// Dir is the CS2 server root (the folder with game/).
	Dir string
	// Zip installs from a local zip (install.sh --zip).
	Zip string
	// Version asks install.sh to download that release (only without Zip).
	Version string
	// AcceptLicense is noncommercial or commercial ("" = the saved answer).
	AcceptLicense string
}

// Cmdline is the shell command that runs install.sh unattended: --yes, no
// terminal, no colour. It never passes a license key (that would show up in
// ps); csm writes cfg/readyup_license.cfg itself (csm license set).
func (a InstallArgs) Cmdline() string {
	q := shellQuote
	args := []string{"bash", q(a.Installer), q(a.Bundle), "--dir", q(a.Dir), "--yes"}
	switch {
	case a.Zip != "":
		args = append(args, "--zip", q(a.Zip))
	case a.Version != "":
		args = append(args, "--version", q(a.Version))
	}
	if a.AcceptLicense != "" {
		args = append(args, "--accept-license="+q(a.AcceptLicense))
	}
	return "NO_COLOR=1 " + strings.Join(args, " ") + " </dev/null"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Installed is game/csgo/readyup/installed.json (written by install.sh).
type Installed struct {
	Components map[string]string `json:"components"`
	Updated    string            `json:"updated"`
}

// ReadInstalled reads what install.sh recorded on a server (serverDir is the
// folder with game/). A server without Ready Up is (nil, nil).
func ReadInstalled(serverDir string) (*Installed, error) {
	data, err := os.ReadFile(filepath.Join(serverDir, "game", "csgo", "readyup", "installed.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var in Installed
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// Summary is "core 0.1.0-beta.1, match 0.1.0-beta.1, ..." sorted by name.
func (in *Installed) Summary() string {
	if in == nil || len(in.Components) == 0 {
		return "not installed"
	}
	names := make([]string, 0, len(in.Components))
	for n := range in.Components {
		names = append(names, n)
	}
	sort.Strings(names)
	// One version for all: say it once.
	v := in.Components[names[0]]
	same := true
	for _, n := range names {
		if in.Components[n] != v {
			same = false
			break
		}
	}
	if same {
		return v + " (" + strings.Join(names, ", ") + ")"
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + " " + in.Components[n]
	}
	return strings.Join(parts, ", ")
}
