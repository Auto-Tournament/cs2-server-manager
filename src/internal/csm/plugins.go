package csm

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

// NewPluginUpdater discovers the game_files and overrides directories based on
// the resolved CSM root (see ResolveRoot), which honours CSM_ROOT when set and
// otherwise falls back to a sensible default such as /opt/cs2-server-manager.
func NewPluginUpdater() *PluginUpdater {
	root := ResolveRoot()
	return &PluginUpdater{
		RootDir:      root,
		GameDir:      filepath.Join(root, "game_files", "game"),
		OverridesDir: filepath.Join(root, "overrides", "game"),
		TempDir:      filepath.Join(root, ".plugin_downloads"),
	}
}

// UpdatePlugins downloads and stages the latest Metamod:Source,
// CounterStrikeSharp and MatchZy (enhanced if available) plugins into
// game_files/, then applies overrides.
func UpdatePlugins() (string, error) {
	up := NewPluginUpdater()
	var buf bytes.Buffer
	var w io.Writer = &buf

	// When CSM_PLUGINS_LOG is set (used by the TUI install wizard), mirror
	// plugin update output into that file so the UI can show a live tail
	// including HTTP download progress.
	if logPath := strings.TrimSpace(os.Getenv("CSM_PLUGINS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer func() {
				_ = f.Close()
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

	// Ensure a clean plugin baseline before downloading new bundles so that
	// stale files from previous versions are not carried forward. The deploy
	// step will mirror this clean tree into each server's addons directory.
	addonsDir := filepath.Join(up.GameDir, "csgo", "addons")
	if err := os.RemoveAll(addonsDir); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to clean existing addons directory %s: %w", addonsDir, err)
	}
	if err := os.MkdirAll(addonsDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(up.TempDir, 0o755); err != nil {
		return "", err
	}

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
		up.applyOverrides(w)
	}

	up.cleanupTemp()

	log("")
	if len(failed) == 0 {
		log("[✓] All plugins updated successfully!")
		log("")
		log("Installation summary:")
		log("  • Metamod:Source     → game_files/game/csgo/addons/metamod/")
		log("  • CounterStrikeSharp → game_files/game/csgo/addons/counterstrikesharp/")
		log("  • MatchZy            → game_files/game/csgo/addons/counterstrikesharp/plugins/MatchZy/")
		log("  • Custom overrides   → Applied from overrides/game/")
		
		// Fix ownership of all server files
		log("")
		log("[*] Fixing file ownership...")
		if mgr, err := NewTmuxManager(); err == nil {
			if err := fixServerOwnership(mgr.CS2User); err != nil {
				log("[!] Warning: Failed to fix ownership: %v", err)
			} else {
				log("[✓] File ownership fixed")
			}
		} else {
			log("[!] Warning: Could not detect CS2 user, skipping ownership fix")
		}
		
		return buf.String(), nil
	}

	log("[✗] Some plugins failed: %s", strings.Join(failed, ", "))
	return buf.String(), fmt.Errorf("some plugins failed to update: %s", strings.Join(failed, ", "))
}

// --- helpers ---

func (up *PluginUpdater) httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

func (up *PluginUpdater) downloadMetamod(w io.Writer) error {
	// Scrape the Metamod dev downloads page for the latest build number.
	const mmBranch = "2.0"
	const mmPage = "https://www.metamodsource.net/downloads.php?branch=dev"

	resp, err := up.httpClient().Get(mmPage)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	re := regexp.MustCompile(`Latest downloads for version.*?build\s+([0-9]+)`)
	m := re.FindStringSubmatch(string(data))
	build := "1374"
	if len(m) >= 2 {
		build = m[1]
	}

	url := fmt.Sprintf("https://mms.alliedmods.net/mmsdrop/%s/mmsource-%s.0-git%s-linux.tar.gz", mmBranch, mmBranch, build)

	fmt.Fprintf(w, "[Metamod] Downloading Metamod:Source %s build %s...\n", mmBranch, build)
	resp2, err := up.httpClient().Get(url)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp2.StatusCode)
	}

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
	if _, err := io.Copy(pw, resp2.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Fprintf(w, "[Metamod] Extracting to %s/csgo/...\n", up.GameDir)
	// Use system tar for simplicity; dependencies are installed by InstallDependencies.
	return runCmdLogged(w, "tar", "-xzf", tmpPath, "-C", filepath.Join(up.GameDir, "csgo"))
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

	resp, err := up.httpClient().Get(downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

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
	fmt.Fprintln(w, "[MatchZy] Fetching latest MatchZy Enhanced release...")

	type release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	var rel release
	if err := up.fetchJSON("https://api.github.com/repos/sivert-io/MatchZy-Enhanced/releases/latest", &rel); err != nil {
		return fmt.Errorf("failed to fetch MatchZy Enhanced releases from sivert-io/MatchZy-Enhanced: %w", err)
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

	fmt.Fprintf(w, "[MatchZy] Target: MatchZy %s (Enhanced Fork)\n", rel.TagName)
	fmt.Fprintln(w, "[MatchZy] Downloading...")

	resp, err := up.httpClient().Get(downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

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
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
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
		matchzyRoot = extractDir
	}

	// Sync into game_files/game/csgo/.
	if err := runCmdLogged(w, "rsync", "-a", matchzyRoot+string(os.PathSeparator), filepath.Join(up.GameDir, "csgo")+string(os.PathSeparator)); err != nil {
		return err
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

func (up *PluginUpdater) cleanupTemp() {
	_ = os.RemoveAll(up.TempDir)
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
	resp, err := up.httpClient().Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s failed with status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
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
