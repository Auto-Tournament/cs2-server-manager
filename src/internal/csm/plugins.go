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
	// CS2User is the detected CS2 user, or "" when it could not be found.
	CS2User string
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
	cs2User := ""
	if mgr, err := NewTmuxManager(); err == nil && mgr.CS2User != "" {
		// Overrides are in the CS2 user's home directory. Copy over any files
		// older TUI builds left in the legacy <root>/overrides folder first.
		EnsureOverridesMigrated(mgr.CS2User)
		cs2User = mgr.CS2User
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
		CS2User:      cs2User,
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
// MetamodPinnedVersion), the latest CounterStrikeSharp and Auto Tournament CS2
// plugins into game_files/, then applies overrides.
//
// It also carries an install made before plugin 2.0.0 over to the new names:
// cfg/MatchZy/ becomes cfg/AutoTournamentCS2/ (EnsureATCS2CfgCarriedOver),
// the old plugins/MatchZy/ folder is removed from the staging tree, and the
// plugin database container is renamed (migrateLegacyATCS2Container).
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

		// Carry an install made before the plugin rename over to the new
		// names before anything reads or writes the new cfg folder.
		if err := EnsureATCS2CfgCarriedOver(w, up.CS2User); err != nil {
			log("[WARN] Carrying cfg/%s over to cfg/%s did not finish: %v", legacyATCS2CfgDirName, ATCS2CfgDirName, err)
		}
		for _, p := range legacyATCS2EnvProblems() {
			log("[WARN] %s", p)
		}
		if err := migrateLegacyATCS2Container(w, atcs2DBContainerName()); err != nil {
			log("[WARN] Plugin database container: %v", err)
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
		if err := up.downloadATCS2(w); err != nil {
			log("[ERROR] Auto Tournament CS2 update failed: %v", err)
			failed = append(failed, "Auto Tournament CS2")
		}

		if len(failed) == 0 {
			// Apply overrides to game_files/ for consistency (staging area)
			up.applyOverrides(w)
			// The staging addons were emptied above, so the old plugin folder
			// can only be back if an override put it there. Never ship it.
			_ = removeLegacyATCS2Plugin(w, "game_files", filepath.Join(up.GameDir, "csgo"))
		}

		log("")
		if len(failed) == 0 {
			log("[✓] All plugins updated successfully!")
			log("")
			log("Installation summary:")
			log("  • Metamod:Source      → game_files/game/csgo/addons/metamod/")
			log("  • CounterStrikeSharp  → game_files/game/csgo/addons/counterstrikesharp/")
			log("  • Auto Tournament CS2 → game_files/game/csgo/addons/counterstrikesharp/plugins/%s/", ATCS2PluginDirName)
			log("  • User overrides      → Applied to game_files/ and cs2-config/")

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
// CounterStrikeSharp installs from its latest release, so this pin has to
// move in step with what that release needs:
//
//   - Metamod commit 0cc4e20 ("Bump MMS Api version, and min load version",
//     2026-09-08) raised the minimum plugin API version. CounterStrikeSharp up
//     to v1.0.374 was built against the old API and fails on newer Metamod
//     ("Plugin uses old SourceHook Metamod build ... (17 < 18)"), so this was
//     pinned to 2.0.0.1411, the last build before that change.
//   - CounterStrikeSharp v1.0.375 (2026-09-24) moved from SourceHook to KHook
//     and requires Metamod build 1467 or newer with KHook support. It is also
//     the release that fixes the CS2 1.41.8.x update, so it cannot be skipped:
//     on 1.0.374 the CBaseEntity_Teleport offset is stale (162 vs 164 on
//     Linux), and any plugin that teleports an entity silently misbehaves.
//
// So 2.0.0.1469, the newest build and >= 1467. The two only work as a pair:
// CounterStrikeSharp v1.0.375+ on this, and never v1.0.374 or older. Check
// what the latest CounterStrikeSharp release requires before changing this.
const MetamodPinnedVersion = "2.0.0.1469"

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
	if err := up.unzipTo(tmpZip, filepath.Join(up.GameDir, "csgo")); err != nil {
		return err
	}

	// glibc 2.41+ (Debian 13, Ubuntu 25.04) will not load libraries that ask
	// for an executable stack, and counterstrikesharp.so does. Clear the flag
	// in the staged copy; every server gets its addons from here. Best-effort:
	// older distros load the library either way.
	cssDir := filepath.Join(up.GameDir, "csgo", "addons", "counterstrikesharp")
	if err := clearExecStackInTree(w, cssDir); err != nil {
		fmt.Fprintf(w, "[CSS] [WARN] Some libraries still request an executable stack; CounterStrikeSharp may not load on Debian 13 / Ubuntu 25.04: %v\n", err)
	}
	return nil
}

// selectATCS2Asset returns the name and download URL of the plugin release
// asset, AutoTournamentCS2-<version>.zip, or "" when the release has none.
// Releases before 2.0.0 ship MatchZy-<version>.zip, which is never picked.
func selectATCS2Asset(assets []metamodReleaseAsset) (string, string) {
	for _, a := range assets {
		if strings.HasPrefix(a.Name, ATCS2AssetPrefix) && strings.HasSuffix(a.Name, ATCS2AssetSuffix) {
			return a.Name, a.URL
		}
	}
	return "", ""
}

// atcs2VersionEnv pins the Auto Tournament CS2 plugin release to install,
// mirroring metamodVersionEnv. Set it to a release tag from
// Auto-Tournament/cs2-plugin (e.g. "2.0.0" or "v2.0.0"); the "v" prefix is
// added automatically when missing. This works for pre-releases, which
// /releases/latest never returns. Unset or empty keeps the old "latest"
// behaviour.
const atcs2VersionEnv = "CSM_ATCS2_VERSION"

// atcs2TargetVersion returns the release tag to install (with a leading "v"
// added if missing), or "" when the newest release should be selected.
func atcs2TargetVersion() string {
	v := strings.TrimSpace(os.Getenv(atcs2VersionEnv))
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

type atcs2Release struct {
	TagName string                `json:"tag_name"`
	HTMLURL string                `json:"html_url"`
	Assets  []metamodReleaseAsset `json:"assets"`
}

// atcs2ReleasesURL is the Auto-Tournament/cs2-plugin releases API base. A
// package-level var, rather than a local const, so tests can point it at a
// stub server.
var atcs2ReleasesURL = "https://api.github.com/repos/Auto-Tournament/cs2-plugin/releases"

// atcs2ReleaseURL returns the GitHub releases API URL to fetch for the given
// pinned target ("" meaning "no pin"): base+"/latest" when unpinned, or
// base+"/tags/<target>" (URL-escaped) when pinned.
func atcs2ReleaseURL(base, target string) string {
	if target == "" {
		return base + "/latest"
	}
	return base + "/tags/" + url.PathEscape(target)
}

func (up *PluginUpdater) downloadATCS2(w io.Writer) error {
	var rel atcs2Release
	target := atcs2TargetVersion()
	if target != "" {
		fmt.Fprintf(w, "[Auto Tournament CS2] %s=%s; fetching pinned release %s...\n", atcs2VersionEnv, os.Getenv(atcs2VersionEnv), target)
		if err := up.fetchJSON(atcs2ReleaseURL(atcs2ReleasesURL, target), &rel); err != nil {
			return fmt.Errorf("failed to fetch Auto Tournament CS2 release %s from Auto-Tournament/cs2-plugin: %w", target, err)
		}
		fmt.Fprintf(w, "[Auto Tournament CS2] Using pinned Auto Tournament CS2 %s (override with %s)\n", rel.TagName, atcs2VersionEnv)
	} else {
		fmt.Fprintln(w, "[Auto Tournament CS2] Fetching the latest Auto Tournament CS2 plugin release...")
		if err := up.fetchJSON(atcs2ReleaseURL(atcs2ReleasesURL, target), &rel); err != nil {
			return fmt.Errorf("failed to fetch Auto Tournament CS2 releases from Auto-Tournament/cs2-plugin: %w", err)
		}
	}

	assetName, downloadURL := selectATCS2Asset(rel.Assets)
	if downloadURL == "" {
		return fmt.Errorf("release %s has no %s-<version>%s asset; csm needs %s", rel.TagName, ATCS2AssetPrefix, ATCS2AssetSuffix, ATCS2Requirement())
	}

	fmt.Fprintf(w, "[Auto Tournament CS2] Target: Auto Tournament CS2 %s (%s)\n", rel.TagName, assetName)
	fmt.Fprintln(w, "[Auto Tournament CS2] Downloading...")

	resp, err := RetryHTTPGet(up.httpClient(), downloadURL, DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("failed to download Auto Tournament CS2 archive from %s after retries: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	tmpZip := filepath.Join(up.TempDir, "auto_tournament_cs2.zip")
	f, err := os.Create(tmpZip)
	if err != nil {
		return err
	}

	pw := &downloadProgressWriter{
		dest:     f,
		progress: w,
		label:    "[Auto Tournament CS2]",
		total:    resp.ContentLength,
	}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	extractDir := filepath.Join(up.TempDir, "auto_tournament_cs2_extract")
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
	pluginRoot := ""
	_ = filepath.WalkDir(extractDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, "addons/counterstrikesharp") {
			pluginRoot = filepath.Dir(filepath.Dir(path)) // up to csgo/
			return io.EOF                                 // early stop
		}
		return nil
	})
	if pluginRoot == "" {
		// Fallback: the zip might extract to a versioned subdirectory like
		// AutoTournamentCS2-2.0.0/
		entries, err := os.ReadDir(extractDir)
		if err == nil && len(entries) == 1 && entries[0].IsDir() {
			// Single subdirectory - likely the versioned folder
			pluginRoot = filepath.Join(extractDir, entries[0].Name())
			// Check if this subdirectory has the structure we need
			if _, err := os.Stat(filepath.Join(pluginRoot, "csgo", "addons")); err == nil {
				// Found csgo/addons structure, use this
			} else if _, err := os.Stat(filepath.Join(pluginRoot, "addons")); err == nil {
				// Found addons at root, need to go up one level conceptually
				// But actually the structure might be pluginRoot/csgo/addons or pluginRoot/addons
				// Let's check for csgo first
				pluginRoot = extractDir // Use extract dir and let rsync handle it
			} else {
				pluginRoot = extractDir
			}
		} else {
			pluginRoot = extractDir
		}
	}

	// Verify the source directory exists before rsync
	if fi, err := os.Stat(pluginRoot); err != nil || !fi.IsDir() {
		return fmt.Errorf("Auto Tournament CS2 extract directory not found or invalid: %s (error: %v)", pluginRoot, err)
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
	pluginRootAbs, err := filepath.Abs(pluginRoot)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for source: %w", err)
	}

	fmt.Fprintf(w, "[Auto Tournament CS2] Syncing from %s to %s...\n", pluginRootAbs, dstDirAbs)
	fmt.Fprintf(w, "[Auto Tournament CS2] Root dir: %s, Game dir: %s\n", up.RootDir, up.GameDir)

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

	if err := runCmdLogged(w, "rsync", "-a", pluginRootAbs+string(os.PathSeparator), dstDirAbs+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("rsync failed: %w (source: %s, dest: %s, root: %s)", err, pluginRootAbs, dstDirAbs, up.RootDir)
	}

	// The release must have put the plugin where every server loads it from.
	pluginDir := atcs2PluginDir(dstDirAbs)
	if _, err := os.Stat(filepath.Join(pluginDir, ATCS2DLLName)); err != nil {
		return fmt.Errorf("%s from release %s does not contain addons/counterstrikesharp/plugins/%s/%s", assetName, rel.TagName, ATCS2PluginDirName, ATCS2DLLName)
	}

	// Record the release next to AutoTournamentCS2.dll. It travels with the
	// addons to every server, so `csm doctor` can tell which build is deployed.
	marker := filepath.Join(pluginDir, ATCS2ReleaseMarkerFile)
	if err := os.WriteFile(marker, []byte(strings.TrimSpace(rel.TagName)+"\n"), 0o644); err != nil {
		fmt.Fprintf(w, "[Auto Tournament CS2] [WARN] Could not record release version in %s: %v\n", marker, err)
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
