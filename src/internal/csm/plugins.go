package csm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PluginUpdater encapsulates logic for downloading and staging CS2 plugins
// (Metamod, CounterStrikeSharp, MatchZy, CS2-AutoUpdater) into the
// game_files tree, and then applying overrides.
type PluginUpdater struct {
	GameDir      string // typically <root>/game_files/game
	OverridesDir string // typically <root>/overrides/game
	TempDir      string // e.g. <root>/.plugin_downloads
	Client       *http.Client
}

// NewPluginUpdater creates a PluginUpdater with sensible defaults based on the
// current working directory, matching the original update.sh behavior.
func NewPluginUpdater() *PluginUpdater {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	gameDir := filepath.Join(root, "game_files", "game")
	overridesDir := filepath.Join(root, "overrides", "game")
	tempDir := filepath.Join(root, ".plugin_downloads")
	return &PluginUpdater{
		GameDir:      gameDir,
		OverridesDir: overridesDir,
		TempDir:      tempDir,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// UpdatePlugins downloads and stages all plugins (non-dry run). It returns a
// human-readable log of what happened.
func UpdatePlugins() (string, error) {
	up := NewPluginUpdater()
	var buf bytes.Buffer
	err := up.Update(&buf, false)
	return buf.String(), err
}

// Update performs the plugin update. If dryRun is true, it will only report
// what it would do without writing to disk.
func (u *PluginUpdater) Update(w io.Writer, dryRun bool) error {
	logf(w, "============================================")
	logf(w, "  CS2 Plugin Updater")
	if dryRun {
		logf(w, "  [DRY-RUN MODE] No files will be modified")
	}
	logf(w, "============================================\n")

	if err := u.checkDependencies(w); err != nil {
		return err
	}

	if !dryRun {
		if err := u.setupDirs(w); err != nil {
			return err
		}
	}

	logf(w, "Downloaded plugins will be placed under: %s\n", u.GameDir)
	logf(w, "Custom overrides will be applied from: %s\n\n", u.OverridesDir)

	var failed []string

	if err := u.downloadMetamod(w, dryRun); err != nil {
		failed = append(failed, "Metamod:Source ("+err.Error()+")")
	}
	logf(w, "\n")

	if err := u.downloadCounterStrikeSharp(w, dryRun); err != nil {
		failed = append(failed, "CounterStrikeSharp ("+err.Error()+")")
	}
	logf(w, "\n")

	if err := u.downloadMatchZy(w, dryRun); err != nil {
		failed = append(failed, "MatchZy ("+err.Error()+")")
	}
	logf(w, "\n")

	if err := u.downloadCS2AutoUpdater(w, dryRun); err != nil {
		failed = append(failed, "CS2-AutoUpdater ("+err.Error()+")")
	}
	logf(w, "\n")

	if !dryRun && len(failed) == 0 {
		if err := u.applyOverrides(w); err != nil {
			failed = append(failed, "overrides: "+err.Error())
		}
		logf(w, "\n")
	}

	if !dryRun {
		_ = u.cleanupTemp(w)
	}

	logf(w, "============================================\n")
	if len(failed) == 0 {
		if dryRun {
			logf(w, "[OK] Dry-run complete – all plugin downloads and installs appear healthy.\n")
			logf(w, "Run without dry-run to actually download and install.\n")
		} else {
			logf(w, "[OK] All plugins downloaded and staged successfully.\n\n")
			logf(w, "Installation summary:\n")
			logf(w, "  • Metamod:Source     → %s\n", filepath.Join(u.GameDir, "csgo", "addons", "metamod"))
			logf(w, "  • CounterStrikeSharp → %s\n", filepath.Join(u.GameDir, "csgo", "addons", "counterstrikesharp"))
			logf(w, "  • MatchZy            → %s\n", filepath.Join(u.GameDir, "csgo", "addons", "counterstrikesharp", "plugins", "MatchZy"))
			logf(w, "  • CS2-AutoUpdater    → %s\n", filepath.Join(u.GameDir, "csgo", "addons", "counterstrikesharp", "plugins", "CS2AutoUpdater"))
			logf(w, "  • Custom configs     → %s (overlaid)\n", filepath.Join(u.OverridesDir, "csgo"))
		}
		logf(w, "============================================\n")
		return nil
	}

	logf(w, "[WARN] Some plugin steps failed:\n")
	for _, f := range failed {
		logf(w, "  - %s\n", f)
	}
	logf(w, "============================================\n")
	return fmt.Errorf("one or more plugin updates failed")
}

func (u *PluginUpdater) checkDependencies(w io.Writer) error {
	missing := []string{}
	for _, name := range []string{"curl", "tar", "unzip", "rsync"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		logf(w, "[ERROR] Missing system tools: %s\n", strings.Join(missing, ", "))
		logf(w, "Please install them, for example on Debian/Ubuntu:\n")
		logf(w, "  sudo apt-get install curl tar unzip rsync\n")
		return fmt.Errorf("missing dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (u *PluginUpdater) setupDirs(w io.Writer) error {
	if err := os.MkdirAll(u.TempDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(u.GameDir, "csgo", "addons"), 0o755); err != nil {
		return err;
	}
	if err := os.MkdirAll(filepath.Join(u.OverridesDir, "csgo"), 0o755); err != nil {
		return err
	}
	return nil
}

func (u *PluginUpdater) cleanupTemp(w io.Writer) error {
	if _, err := os.Stat(u.TempDir); err == nil {
		_ = exec.Command("chmod", "-R", "u+rwX", u.TempDir).Run()
		return os.RemoveAll(u.TempDir)
	}
	return nil
}

func (u *PluginUpdater) applyOverrides(w io.Writer) error {
	ovDir := filepath.Join(u.OverridesDir, "csgo")
	if fi, err := os.Stat(ovDir); err != nil || !fi.IsDir() {
		return nil
	}
	logf(w, "Applying custom overrides from %s\n", ovDir)
	cmd := exec.Command("rsync", "-a", ovDir+string(os.PathSeparator), filepath.Join(u.GameDir, "csgo")+"/")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logf(w, "[ERROR] rsync overrides: %v\n%s\n", err, string(out))
		return err
	}
	logf(w, "[OK] Overrides applied.\n")
	return nil
}

func (u *PluginUpdater) downloadMetamod(w io.Writer, dryRun bool) error {
	logf(w, "Fetching latest Metamod:Source dev build...\n")
	version := "2.0"
	build := ""

	// Try to detect latest build, but fall back to a known-good build.
	if !dryRun {
		resp, err := u.Client.Get("https://www.metamodsource.net/downloads.php?branch=dev")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				s := string(body)
				// Very simple heuristic: look for "build " followed by digits.
				if idx := strings.Index(s, "build "); idx != -1 {
					rest := s[idx+len("build "):]
					for i := 0; i < len(rest); i++ {
						if rest[i] < '0' || rest[i] > '9' {
							build = rest[:i]
							break
						}
					}
				}
			}
		} else {
			logf(w, "[WARN] Could not query metamod site: %v\n", err)
		}
	}

	if build == "" {
		build = "1374"
		logf(w, "[WARN] Using fallback Metamod build %s\n", build)
	}

	url := fmt.Sprintf("https://mms.alliedmods.net/mmsdrop/%s/mmsource-%s.0-git%s-linux.tar.gz", version, version, build)
	logf(w, "Target: Metamod:Source %s build %s\n", version, build)
	if dryRun {
		logf(w, "[DRY-RUN] Would download from: %s\n", url)
		return nil
	}

	if err := u.fetchToFile(w, url, filepath.Join(u.TempDir, "metamod.tar.gz")); err != nil {
		return fmt.Errorf("download metamod: %w", err)
	}
	logf(w, "[OK] Downloaded Metamod:Source\n")

	dest := filepath.Join(u.GameDir, "csgo")
	if err := runCmdLogged(w, "tar", "-xzf", filepath.Join(u.TempDir, "metamod.tar.gz"), "-C", dest); err != nil {
		return fmt.Errorf("extract metamod: %w", err)
	}
	logf(w, "[OK] Metamod:Source installed into %s\n", dest)
	return nil
}

func (u *PluginUpdater) downloadCounterStrikeSharp(w io.Writer, dryRun bool) error {
	api := "https://api.github.com/repos/roflmuffin/CounterStrikeSharp/releases/latest"

	logf(w, "Fetching latest CounterStrikeSharp release...\n")
	if dryRun {
		logf(w, "[DRY-RUN] Would query %s\n", api)
		return nil
	}

	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := u.fetchJSON(api, &rel); err != nil {
		return fmt.Errorf("fetch CSS release: %w", err)
	}

	var url string
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, "with-runtime-linux") && strings.HasSuffix(a.Name, ".zip") {
			url = a.URL
			break
		}
	}
	if url == "" {
		for _, a := range rel.Assets {
			if strings.Contains(strings.ToLower(a.Name), "linux") && strings.HasSuffix(a.Name, ".zip") {
				url = a.URL
				break
			}
		}
	}
	if url == "" {
		return fmt.Errorf("no suitable CounterStrikeSharp Linux asset found")
	}

	logf(w, "Target: CounterStrikeSharp %s (%s)\n", rel.TagName, url)
	if dryRun {
		logf(w, "[DRY-RUN] Would download from: %s\n", url)
		return nil
	}

	dst := filepath.Join(u.TempDir, "counterstrikesharp.zip")
	if err := u.fetchToFile(w, url, dst); err != nil {
		return fmt.Errorf("download CSS: %w", err)
	}
	logf(w, "[OK] Downloaded CounterStrikeSharp\n")

	dest := filepath.Join(u.GameDir, "csgo")
	if err := runCmdLogged(w, "unzip", "-o", dst, "-d", dest); err != nil {
		return fmt.Errorf("extract CSS: %w", err)
	}
	logf(w, "[OK] CounterStrikeSharp extracted into %s\n", dest)
	return nil
}

func (u *PluginUpdater) downloadMatchZy(w io.Writer, dryRun bool) error {
	logf(w, "Fetching latest MatchZy release (Enhanced / fallback)...\n")

	type rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	apiPrimary := "https://api.github.com/repos/sivert-io/MatchZy/releases/latest"

	var r rel
	err := u.fetchJSON(apiPrimary, &r)
	repoName := "(Enhanced Fork)"
	if err != nil {
		logf(w, "[WARN] Primary MatchZy repo not reachable (%v), trying fallback...\n", err)
		if err2 := u.fetchJSON("https://api.github.com/repos/shobhit-io/MatchZy/releases/latest", &r); err2 != nil {
			return fmt.Errorf("fetch MatchZy release (both primary and fallback) failed: %v / %v", err, err2)
		}
		repoName = "(Official)"
	}

	var url string
	for _, a := range r.Assets {
		if strings.Contains(a.Name, "MatchZy") && !strings.Contains(a.Name, "with") && strings.HasSuffix(a.Name, ".zip") {
			url = a.URL
			break
		}
	}
	if url == "" {
		for _, a := range r.Assets {
			if strings.HasSuffix(a.Name, ".zip") {
				url = a.URL
				break
			}
		}
	}
	if url == "" {
		return fmt.Errorf("no suitable MatchZy asset found")
	}

	logf(w, "Target: MatchZy %s %s\n", r.TagName, repoName)
	if dryRun {
		logf(w, "[DRY-RUN] Would download from: %s\n", url)
		return nil
	}

	zipPath := filepath.Join(u.TempDir, "matchzy.zip")
	if err := u.fetchToFile(w, url, zipPath); err != nil {
		return fmt.Errorf("download MatchZy: %w", err)
	}
	logf(w, "[OK] Downloaded MatchZy\n")

	extractDir := filepath.Join(u.TempDir, "matchzy_extract")
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}

	if err := runCmdLogged(w, "unzip", "-o", zipPath, "-d", extractDir); err != nil {
		return fmt.Errorf("extract MatchZy: %w", err)
	}
	_ = exec.Command("chmod", "-R", "u+rwX,go+rX", extractDir).Run()

	root := ""
	// Pattern 1: MatchZy* dir
	if entries, _ := os.ReadDir(extractDir); len(entries) > 0 {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), "matchzy") {
				root = filepath.Join(extractDir, e.Name())
				break
			}
		}
	}
	// Pattern 2/3: direct addons tree
	if root == "" {
		if fi, err := os.Stat(filepath.Join(extractDir, "addons")); err == nil && fi.IsDir() {
			root = extractDir
		}
	}
	if root == "" {
		return fmt.Errorf("could not find MatchZy root in extracted archive")
	}

	dst := filepath.Join(u.GameDir, "csgo")
	if err := runCmdLogged(w, "rsync", "-a", root+string(os.PathSeparator), dst+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("rsync MatchZy: %w", err)
	}
	logf(w, "[OK] MatchZy installed into %s\n", dst)
	return nil
}

func (u *PluginUpdater) downloadCS2AutoUpdater(w io.Writer, dryRun bool) error {
	logf(w, "Fetching latest CS2-AutoUpdater release...\n")

	type rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}

	api := "https://api.github.com/repos/dran1x/CS2-AutoUpdater/releases/latest"

	if dryRun {
		logf(w, "[DRY-RUN] Would query %s\n", api)
		return nil
	}

	var r rel
	if err := u.fetchJSON(api, &r); err != nil {
		return fmt.Errorf("fetch CS2-AutoUpdater release: %w", err)
	}

	var url string
	for _, a := range r.Assets {
		if strings.HasSuffix(a.Name, ".zip") {
			url = a.URL
			break
		}
	}
	if url == "" {
		return fmt.Errorf("no CS2-AutoUpdater zip asset found")
	}

	logf(w, "Target: CS2-AutoUpdater %s\n", r.TagName)

	zipPath := filepath.Join(u.TempDir, "cs2autoupdater.zip")
	if err := u.fetchToFile(w, url, zipPath); err != nil {
		return fmt.Errorf("download CS2-AutoUpdater: %w", err)
	}
	logf(w, "[OK] Downloaded CS2-AutoUpdater\n")

	extractDir := filepath.Join(u.TempDir, "cs2autoupdater_extract")
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}

	if err := runCmdLogged(w, "unzip", "-o", zipPath, "-d", extractDir); err != nil {
		return fmt.Errorf("extract CS2-AutoUpdater: %w", err)
	}

	src := filepath.Join(extractDir, "plugins")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("no plugins/ directory in CS2-AutoUpdater archive")
	}

	dst := filepath.Join(u.GameDir, "csgo", "addons", "counterstrikesharp", "plugins")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	if err := runCmdLogged(w, "rsync", "-a", src+string(os.PathSeparator), dst+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("rsync CS2-AutoUpdater: %w", err)
	}
	logf(w, "[OK] CS2-AutoUpdater installed into %s\n", dst)
	return nil
}

func (u *PluginUpdater) fetchToFile(w io.Writer, url, path string) error {
	resp, err := u.Client.Get(url)
	if err != nil {
		logf(w, "[ERROR] HTTP GET %s: %v\n", url, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logf(w, "[ERROR] HTTP %s: %d\n%s\n", url, resp.StatusCode, string(body))
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err;
	}
	return nil
}

func (u *PluginUpdater) fetchJSON(url string, out any) error {
	resp, err := u.Client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func runCmdLogged(w io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logf(w, "$ %s %s\n%s\n", name, strings.Join(args, " "), string(out))
	}
	return err
}

func logf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}


