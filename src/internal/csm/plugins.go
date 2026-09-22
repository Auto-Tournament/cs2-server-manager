package csm

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PluginUpdater describes where plugin assets live on disk.
type PluginUpdater struct {
	RootDir      string
	GameDir      string
	OverridesDir string
	TempDir      string
}

type metamodReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type metamodRelease struct {
	TagName     string                `json:"tag_name"`
	Prerelease  bool                  `json:"prerelease"`
	PublishedAt string                `json:"published_at"`
	CreatedAt   string                `json:"created_at"`
	Assets      []metamodReleaseAsset `json:"assets"`
}

// NewPluginUpdater discovers the game_files and overrides directories.
// Overrides are stored in the CS2 user's home directory for easier access.
// Temporary files are stored in /tmp to avoid creating root-owned files in user directories.
func NewPluginUpdater() *PluginUpdater {
	root := ResolveRoot()

	// Try to get the CS2 user for overrides location
	overridesDir := ""
	tempDir := ""
	if mgr, err := NewTmuxManager(); err == nil && mgr.CS2User != "" {
		// Overrides are in the CS2 user's home directory. Copy over any files
		// older TUI builds left in the legacy <root>/overrides folder first.
		EnsureOverridesMigrated(mgr.CS2User)
		overridesDir = OverridesGameDir(mgr.CS2User)
		// Temp files go to /tmp with user-specific directory to avoid conflicts
		tempDir = filepath.Join(os.TempDir(), fmt.Sprintf("csm-plugin-downloads-%s", mgr.CS2User))
	} else {
		// Fallback to <root>/overrides if we can't detect the user
		overridesDir = OverridesGameDir("")
		// Use system temp directory with a generic name
		tempDir = filepath.Join(os.TempDir(), "csm-plugin-downloads")
	}

	return &PluginUpdater{
		RootDir:      root,
		GameDir:      filepath.Join(root, "game_files", "game"),
		OverridesDir: overridesDir,
		TempDir:      tempDir,
	}
}

// CheckDiskSpaceForPluginUpdate checks that the filesystem that holds (or
// will hold) gameDir has at least 1GB free for plugin updates. gameDir does
// not need to exist yet: on a fresh install game_files/game is only created by
// the update itself, so the check inspects the closest existing parent.
func CheckDiskSpaceForPluginUpdate(gameDir string) error {
	const minRequiredGB = 1.0
	return requireFreeDiskGB(gameDir, minRequiredGB)
}

// UpdatePlugins downloads and stages Metamod:Source (pinned, see
// MetamodPinnedVersion), the latest CounterStrikeSharp and MatchZy (enhanced if available) plugins into
// game_files/, then applies overrides.
// This function is protected by a mutex to prevent concurrent updates.
func UpdatePlugins() (string, error) {
	var result string
	var resultErr error

	err := withPluginUpdateLock(func() error {
		up := NewPluginUpdater()
		var buf bytes.Buffer
		var w io.Writer = &buf

		// When CSM_PLUGINS_LOG is set (used by the TUI install wizard), mirror
		// plugin update output into that file so the UI can show a live tail
		// including HTTP download progress.
		if logPath := strings.TrimSpace(os.Getenv("CSM_PLUGINS_LOG")); logPath != "" {
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				defer func() {
					if err := f.Close(); err != nil {
						fmt.Fprintf(os.Stderr, "CSM_PLUGINS_LOG close failed: %v\n", err)
					}
				}()
				if bufPtr, ok := w.(*bytes.Buffer); ok {
					w = &teeWriter{buf: bufPtr, file: f}
				} else {
					w = io.MultiWriter(w, f)
				}
			}
		}

		log := func(format string, args ...any) {
			fmt.Fprintf(w, format, args...)
			if !strings.HasSuffix(format, "\n") {
				fmt.Fprintln(w)
			}
		}

		log("=== Update Plugins ===")
		log("")

		// Check disk space before starting downloads
		// A real shortage already says "insufficient disk space"; any other
		// error means the check itself could not run, and must not be
		// reported as a full disk.
		if err := CheckDiskSpaceForPluginUpdate(up.GameDir); err != nil {
			resultErr = fmt.Errorf("plugin update pre-check failed: %w", err)
			return resultErr
		}

		// Ensure a clean plugin baseline before downloading new bundles so that
		// stale files from previous versions are not carried forward. The deploy
		// step will mirror this clean tree into each server's addons directory.
		addonsDir := filepath.Join(up.GameDir, "csgo", "addons")
		if err := os.RemoveAll(addonsDir); err != nil && !os.IsNotExist(err) {
			resultErr = fmt.Errorf("failed to clean existing addons directory %s: %w", addonsDir, err)
			return resultErr
		}
		if err := os.MkdirAll(addonsDir, 0o755); err != nil {
			resultErr = err
			return resultErr
		}
		if err := os.MkdirAll(up.TempDir, 0o755); err != nil {
			resultErr = err
			return resultErr
		}

		// Ensure temp cleanup happens even on error
		defer func() {
			if err := os.RemoveAll(up.TempDir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to cleanup temp directory %s: %v\n", up.TempDir, err)
			}
		}()

		var failed []string

		if err := up.downloadMetamod(w); err != nil {
			log("[ERROR] Metamod:Source update failed: %v", err)
			failed = append(failed, "Metamod:Source")
		}
		if err := up.downloadCounterStrikeSharp(w); err != nil {
			log("[ERROR] CounterStrikeSharp update failed: %v", err)
			failed = append(failed, "CounterStrikeSharp")
		}
		if err := up.downloadMatchZy(w); err != nil {
			log("[ERROR] MatchZy update failed: %v", err)
			failed = append(failed, "MatchZy")
		}

		if len(failed) == 0 {
			// Apply overrides to game_files/ for consistency (staging area)
			up.applyOverrides(w)
		}

		log("")
		if len(failed) == 0 {
			log("[✓] All plugins updated successfully!")
			log("")
			log("Installation summary:")
			log("  • Metamod:Source     → game_files/game/csgo/addons/metamod/")
			log("  • CounterStrikeSharp → game_files/game/csgo/addons/counterstrikesharp/")
			log("  • MatchZy            → game_files/game/csgo/addons/counterstrikesharp/plugins/MatchZy/")
			log("  • User overrides     → Applied to game_files/ and cs2-config/")

			result = buf.String()
			return nil
		}

		log("[✗] Some plugins failed: %s", strings.Join(failed, ", "))
		result = buf.String()
		resultErr = fmt.Errorf("some plugins failed to update: %s", strings.Join(failed, ", "))
		return resultErr
	})

	if err != nil {
		return result, resultErr
	}
	return result, nil
}

// --- helpers ---

func (up *PluginUpdater) httpClient() *http.Client {
	return &http.Client{Timeout: TimeoutPluginDownload}
}

// MetamodPinnedVersion is the Metamod:Source GitHub release tag CSM installs
// by default.
//
// Metamod commit 0cc4e20 ("Bump MMS Api version, and min load version",
// 2026-09-08) raised the minimum plugin API version. CounterStrikeSharp
// v1.0.374 is built against the old API, so newer Metamod builds refuse to
// load it ("Plugin uses old SourceHook Metamod build ... (17 < 18)") and
// neither CounterStrikeSharp nor MatchZy loads. 2.0.0.1411 (2026-08-31) is the
// last build published before that change and before the SourceHook rewrite.
//
// Raise this once a CounterStrikeSharp release loads on the newer Metamod API.
const MetamodPinnedVersion = "2.0.0.1411"

// metamodVersionEnv overrides MetamodPinnedVersion. Set it to a release tag
// from alliedmodders/metamod-source (e.g. "2.0.0.1468") or to "latest" to
// install the newest prerelease (falling back to the newest stable release).
const metamodVersionEnv = "CSM_METAMOD_VERSION"

// metamodLatest is the metamodVersionEnv value that restores the old
// "newest prerelease" behaviour.
const metamodLatest = "latest"

// metamodTargetVersion returns the Metamod release tag to install, or
// metamodLatest when the newest release should be selected.
func metamodTargetVersion() string {
	v := strings.TrimSpace(os.Getenv(metamodVersionEnv))
	if v == "" {
		return MetamodPinnedVersion
	}
	if strings.EqualFold(v, metamodLatest) {
		return metamodLatest
	}
	return v
}

func (up *PluginUpdater) downloadMetamod(w io.Writer) error {
	const releasesURL = "https://api.github.com/repos/alliedmodders/metamod-source/releases"

	var release metamodRelease
	if target := metamodTargetVersion(); target == metamodLatest {
		fmt.Fprintf(w, "[Metamod] %s=latest; fetching latest Metamod:Source prerelease...\n", metamodVersionEnv)
		var payload []metamodRelease
		if err := up.fetchJSON(releasesURL, &payload); err != nil {
			return fmt.Errorf("failed to fetch Metamod releases from alliedmodders/metamod-source: %w", err)
		}
		var ok bool
		release, ok = selectMetamodRelease(payload)
		if !ok {
			return fmt.Errorf("no Metamod releases found")
		}
		if !release.Prerelease {
			fmt.Fprintln(w, "[Metamod] No prerelease found; falling back to latest stable release.")
		}
	} else {
		if target == MetamodPinnedVersion {
			fmt.Fprintf(w, "[Metamod] Using pinned Metamod:Source %s (override with %s)\n", target, metamodVersionEnv)
		} else {
			fmt.Fprintf(w, "[Metamod] Using Metamod:Source %s from %s\n", target, metamodVersionEnv)
		}
		if err := up.fetchJSON(releasesURL+"/tags/"+url.PathEscape(target), &release); err != nil {
			return fmt.Errorf("failed to fetch Metamod release %s from alliedmodders/metamod-source: %w", target, err)
		}
	}

	assetName, downloadURL := selectMetamodLinuxAsset(release.Assets)
	if downloadURL == "" {
		return fmt.Errorf("no suitable Metamod linux x86_64 asset found in release %s", release.TagName)
	}

	fmt.Fprintf(w, "[Metamod] Target: Metamod:Source %s (%s)\n", release.TagName, assetName)
	fmt.Fprintln(w, "[Metamod] Downloading Metamod:Source...")

	resp2, err := RetryHTTPGet(up.httpClient(), downloadURL, DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("failed to download Metamod archive from %s after retries: %w", downloadURL, err)
	}
	defer resp2.Body.Close()

	tmpPath := filepath.Join(up.TempDir, "metamod.tar.gz")
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	pw := &downloadProgressWriter{
		dest:     f,
		progress: w,
		label:    "[Metamod]",
		total:    resp2.ContentLength,
	}
	written, err := io.Copy(pw, resp2.Body)
	if err != nil {
		_ = f.Close()
		return err
	}
	if written == 0 {
		_ = f.Close()
		return fmt.Errorf("downloaded Metamod archive from %s is empty (asset: %s)", downloadURL, assetName)
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Fprintf(w, "[Metamod] Extracting to %s/csgo/...\n", up.GameDir)
	// Use system tar for simplicity; dependencies are installed by InstallDependencies.
	return runCmdLogged(w, "tar", "-xzf", tmpPath, "-C", filepath.Join(up.GameDir, "csgo"))
}

func selectMetamodRelease(releases []metamodRelease) (metamodRelease, bool) {
	var latestPrerelease *metamodRelease
	var latestStable *metamodRelease
	for i := range releases {
		release := &releases[i]
		if release.Prerelease {
			if latestPrerelease == nil || metamodReleaseTime(*release).After(metamodReleaseTime(*latestPrerelease)) {
				latestPrerelease = release
			}
			continue
		}
		if latestStable == nil || metamodReleaseTime(*release).After(metamodReleaseTime(*latestStable)) {
			latestStable = release
		}
	}
	if latestPrerelease != nil {
		return *latestPrerelease, true
	}
	if latestStable != nil {
		return *latestStable, true
	}
	return metamodRelease{}, false
}

func metamodReleaseTime(release metamodRelease) time.Time {
	if release.PublishedAt != "" {
		if published, err := time.Parse(time.RFC3339, release.PublishedAt); err == nil {
			return published
		}
	}
	if release.CreatedAt != "" {
		if created, err := time.Parse(time.RFC3339, release.CreatedAt); err == nil {
			return created
		}
	}
	return time.Time{}
}

func selectMetamodLinuxAsset(assets []metamodReleaseAsset) (string, string) {
	type candidate struct {
		name string
		url  string
	}
	var fallback candidate
	for _, a := range assets {
		nameLower := strings.ToLower(a.Name)
		if !strings.Contains(nameLower, "linux") || !strings.HasSuffix(nameLower, ".tar.gz") {
			continue
		}
		if strings.Contains(nameLower, "x86_64") || strings.Contains(nameLower, "amd64") {
			return a.Name, a.URL
		}
		if fallback.url == "" {
			fallback = candidate{name: a.Name, url: a.URL}
		}
	}
	return fallback.name, fallback.url
}

func (up *PluginUpdater) downloadCounterStrikeSharp(w io.Writer) error {
	const apiURL = "https://api.github.com/repos/roflmuffin/CounterStrikeSharp/releases/latest"

	fmt.Fprintln(w, "[CSS] Fetching latest CounterStrikeSharp release...")
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := up.fetchJSON(apiURL, &payload); err != nil {
		return err
	}

	var downloadURL string
	for _, a := range payload.Assets {
		if strings.Contains(a.Name, "with-runtime-linux") {
			downloadURL = a.URL
			break
		}
	}
	if downloadURL == "" {
		for _, a := range payload.Assets {
			if strings.Contains(a.Name, "linux") {
				downloadURL = a.URL
				break
			}
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no suitable CounterStrikeSharp linux asset found")
	}

	fmt.Fprintf(w, "[CSS] Target: CounterStrikeSharp %s\n", payload.TagName)
	fmt.Fprintln(w, "[CSS] Downloading CounterStrikeSharp...")

	resp, err := RetryHTTPGet(up.httpClient(), downloadURL, DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("failed to download CounterStrikeSharp archive from %s after retries: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	tmpZip := filepath.Join(up.TempDir, "counterstrikesharp.zip")
	f, err := os.Create(tmpZip)
	if err != nil {
		return err
	}

	pw := &downloadProgressWriter{
		dest:     f,
		progress: w,
		label:    "[CSS]",
		total:    resp.ContentLength,
	}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Fprintf(w, "[CSS] Extracting to %s/csgo/...\n", up.GameDir)
	return up.unzipTo(tmpZip, filepath.Join(up.GameDir, "csgo"))
}

func (up *PluginUpdater) downloadMatchZy(w io.Writer) error {
	fmt.Fprintln(w, "[MatchZy] Fetching the latest Auto Tournament CS2 plugin release...")

	type release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	var rel release
	if err := up.fetchJSON("https://api.github.com/repos/Auto-Tournament/cs2-plugin/releases/latest", &rel); err != nil {
		return fmt.Errorf("failed to fetch Auto Tournament CS2 releases from Auto-Tournament/cs2-plugin: %w", err)
	}

	var downloadURL string
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, "MatchZy") && !strings.Contains(a.Name, "with") {
			downloadURL = a.URL
			break
		}
	}
	if downloadURL == "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(a.Name, ".zip") {
				downloadURL = a.URL
				break
			}
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no suitable MatchZy asset found")
	}

	fmt.Fprintf(w, "[MatchZy] Target: Auto Tournament CS2 %s\n", rel.TagName)
	fmt.Fprintln(w, "[MatchZy] Downloading...")

	resp, err := RetryHTTPGet(up.httpClient(), downloadURL, DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("failed to download MatchZy archive from %s after retries: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	tmpZip := filepath.Join(up.TempDir, "matchzy.zip")
	f, err := os.Create(tmpZip)
	if err != nil {
		return err
	}

	pw := &downloadProgressWriter{
		dest:     f,
		progress: w,
		label:    "[MatchZy]",
		total:    resp.ContentLength,
	}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	extractDir := filepath.Join(up.TempDir, "matchzy_extract")
	// Clean up extract directory if it exists from a previous failed attempt
	if err := os.RemoveAll(extractDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clean extract directory %s: %w", extractDir, err)
	}
	// Ensure extract directory exists and is writable
	if err := EnsureDirectoryExists(extractDir); err != nil {
		return fmt.Errorf("failed to prepare extract directory: %w", err)
	}
	if err := up.unzipTo(tmpZip, extractDir); err != nil {
		return err
	}

	// Try to find a root containing addons/counterstrikesharp.
	matchzyRoot := ""
	_ = filepath.WalkDir(extractDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, "addons/counterstrikesharp") {
			matchzyRoot = filepath.Dir(filepath.Dir(path)) // up to csgo/
			return io.EOF                                  // early stop
		}
		return nil
	})
	if matchzyRoot == "" {
		// Fallback: look for any directory that contains MatchZy files
		// The zip might extract to a versioned subdirectory like MatchZy-1.4.10/
		entries, err := os.ReadDir(extractDir)
		if err == nil && len(entries) == 1 && entries[0].IsDir() {
			// Single subdirectory - likely the versioned folder
			matchzyRoot = filepath.Join(extractDir, entries[0].Name())
			// Check if this subdirectory has the structure we need
			if _, err := os.Stat(filepath.Join(matchzyRoot, "csgo", "addons")); err == nil {
				// Found csgo/addons structure, use this
			} else if _, err := os.Stat(filepath.Join(matchzyRoot, "addons")); err == nil {
				// Found addons at root, need to go up one level conceptually
				// But actually the structure might be matchzyRoot/csgo/addons or matchzyRoot/addons
				// Let's check for csgo first
				matchzyRoot = extractDir // Use extract dir and let rsync handle it
			} else {
				matchzyRoot = extractDir
			}
		} else {
			matchzyRoot = extractDir
		}
	}

	// Verify the source directory exists before rsync
	if fi, err := os.Stat(matchzyRoot); err != nil || !fi.IsDir() {
		return fmt.Errorf("MatchZy extract directory not found or invalid: %s (error: %v)", matchzyRoot, err)
	}

	// Ensure destination exists - use absolute paths to avoid rsync getcwd() errors
	dstDir := filepath.Join(up.GameDir, "csgo")
	dstDirAbs, err := filepath.Abs(dstDir)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for destination: %w", err)
	}

	if err := EnsureDirectoryExists(dstDirAbs); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDirAbs, err)
	}

	// Verify destination exists and is a directory
	if fi, err := os.Stat(dstDirAbs); err != nil || !fi.IsDir() {
		return fmt.Errorf("destination directory does not exist or is not a directory: %s (error: %v)", dstDirAbs, err)
	}

	// Use absolute paths for rsync to avoid getcwd() errors
	matchzyRootAbs, err := filepath.Abs(matchzyRoot)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for source: %w", err)
	}

	fmt.Fprintf(w, "[MatchZy] Syncing from %s to %s...\n", matchzyRootAbs, dstDirAbs)
	fmt.Fprintf(w, "[MatchZy] Root dir: %s, Game dir: %s\n", up.RootDir, up.GameDir)

	// Sync into game_files/game/csgo/ using absolute paths
	// Change to a safe directory before rsync to avoid getcwd() errors
	originalWd, _ := os.Getwd()
	defer func() {
		if originalWd != "" {
			_ = os.Chdir(originalWd)
		}
	}()

	// Change to /tmp (a safe, always-existing directory) before rsync
	if err := os.Chdir(os.TempDir()); err != nil {
		return fmt.Errorf("failed to change to temp directory: %w", err)
	}

	if err := runCmdLogged(w, "rsync", "-a", matchzyRootAbs+string(os.PathSeparator), dstDirAbs+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("rsync failed: %w (source: %s, dest: %s, root: %s)", err, matchzyRootAbs, dstDirAbs, up.RootDir)
	}

	// Record the release next to MatchZy.dll. It travels with the addons to
	// every server, so `csm doctor` can tell which build is deployed.
	marker := filepath.Join(dstDirAbs, "addons", "counterstrikesharp", "plugins", "MatchZy", MatchzyReleaseMarkerFile)
	if err := os.WriteFile(marker, []byte(strings.TrimSpace(rel.TagName)+"\n"), 0o644); err != nil {
		fmt.Fprintf(w, "[MatchZy] [WARN] Could not record release version in %s: %v\n", marker, err)
	}
	return nil
}

func (up *PluginUpdater) applyOverrides(w io.Writer) {
	src := filepath.Join(up.OverridesDir, "csgo")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return
	}
	fmt.Fprintln(w, "[Overrides] Applying custom config overrides from overrides/game/ ...")
	_ = runCmdLogged(w, "rsync", "-a", src+string(os.PathSeparator), filepath.Join(up.GameDir, "csgo")+string(os.PathSeparator))
}

// downloadProgressWriter wraps a destination writer so that as bytes are
// written we can emit coarse-grained progress updates to a log writer. This is
// used for plugin downloads so both CLI and TUI flows can show live progress
// similar to wget/curl without overwhelming the logs.
type downloadProgressWriter struct {
	dest     io.Writer
	progress io.Writer
	label    string
	total    int64
	written  int64
	lastPct  int
}

func (pw *downloadProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.dest.Write(p)
	if n <= 0 || pw.total <= 0 || pw.progress == nil {
		return n, err
	}

	pw.written += int64(n)
	pct := int(pw.written * 100 / pw.total)
	if pct > 100 {
		pct = 100
	}

	// Log on first write, every +5%, and at 100%.
	if pw.lastPct == 0 || pct >= pw.lastPct+5 || pct == 100 {
		pw.lastPct = pct
		mbTotal := float64(pw.total) / (1024.0 * 1024.0)
		mbWritten := float64(pw.written) / (1024.0 * 1024.0)
		if mbTotal > 0 {
			fmt.Fprintf(pw.progress, "%s Downloaded %d%% (%.1f / %.1f MB)\n", pw.label, pct, mbWritten, mbTotal)
		} else {
			fmt.Fprintf(pw.progress, "%s Downloaded %d%%\n", pw.label, pct)
		}
	}

	return n, err
}

func (up *PluginUpdater) fetchJSON(url string, v any) error {
	cfg := DefaultRetryConfig()
	data, err := RetryHTTPRead(up.httpClient(), url, cfg)
	if err != nil {
		return fmt.Errorf("failed to fetch JSON from %s after retries: %w", url, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse JSON response from %s: %w", url, err)
	}
	return nil
}

func (up *PluginUpdater) unzipTo(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fp := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fp, dest) {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fp, f.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()
			return err
		}

		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			_ = rc.Close()
			return err
		}

		if err := out.Close(); err != nil {
			_ = rc.Close()
			return err
		}
		if err := rc.Close(); err != nil {
			return err
		}
	}
	return nil
}
