package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type updateInfoMsg struct {
	latest string
	err    error
}

// updateCacheTTL defines how long we trust a cached update check result before
// hitting the GitHub API again. This helps avoid rate limits when the TUI is
// opened/closed frequently.
const updateCacheTTL = 1 * time.Minute

type updateCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// forceUpdateInfoMsg is used when the user explicitly triggers a "force
// update" from the Utilities tab, bypassing the local cache TTL.
type forceUpdateInfoMsg struct {
	latest string
	err    error
}

func checkForUpdates() tea.Cmd {
	return func() tea.Msg {
		latest, err := getCachedOrFetchLatest()
		if err != nil {
			return updateInfoMsg{err: err}
		}
		return updateInfoMsg{latest: latest}
	}
}

// checkForUpdatesForce bypasses the local cache TTL and always performs a
// direct GitHub API check, while still updating the cache on success.
func checkForUpdatesForce() tea.Cmd {
	return func() tea.Msg {
		latest, err := getCachedOrFetchLatestWithTTL(false)
		if err != nil {
			return forceUpdateInfoMsg{err: err}
		}
		return forceUpdateInfoMsg{latest: latest}
	}
}

// getCachedOrFetchLatest returns the latest version tag, using a small cache on
// disk to avoid hammering the GitHub API when the TUI is opened repeatedly.
func getCachedOrFetchLatest() (string, error) {
	return getCachedOrFetchLatestWithTTL(true)
}

// getCachedOrFetchLatestWithTTL is the internal implementation that optionally
// honours the cache TTL. When useTTL is false we always perform a fresh GitHub
// check (while still updating the cache on success).
func getCachedOrFetchLatestWithTTL(useTTL bool) (string, error) {
	cachePath := updateCachePath()

	// Try cached value first.
	var cached updateCache
	if cachePath != "" {
		if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &cached); err == nil && cached.Latest != "" && !cached.CheckedAt.IsZero() {
				if useTTL && time.Since(cached.CheckedAt) < updateCacheTTL {
					return cached.Latest, nil
				}
			}
		}
	}

	// Cache is missing or stale: hit GitHub.
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sivert-io/cs2-server-manager/releases/latest")
	if err != nil {
		// On network errors, fall back to any cached latest if present.
		if cached.Latest != "" {
			return cached.Latest, nil
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Again, fall back to cached result if we have one (e.g. rate limited).
		if cached.Latest != "" {
			return cached.Latest, nil
		}
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		if cached.Latest != "" {
			return cached.Latest, nil
		}
		return "", err
	}

	latest := strings.TrimSpace(payload.TagName)
	if latest == "" {
		if cached.Latest != "" {
			return cached.Latest, nil
		}
		return "", fmt.Errorf("empty latest tag from GitHub")
	}

	// Save back to cache best-effort.
	if cachePath != "" {
		_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)
		data, err := json.Marshal(updateCache{
			Latest:    latest,
			CheckedAt: time.Now(),
		})
		if err == nil {
			_ = os.WriteFile(cachePath, data, 0o644)
		}
	}

	return latest, nil
}

// updateCachePath chooses a location for the update cache. We prefer the
// directory of the csm binary; if that's not writable, we fall back to a user
// config directory.
func updateCachePath() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		path := filepath.Join(dir, ".csm-update.json")
		if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644); err == nil {
			f.Close()
			return path
		}
	}

	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		dir := filepath.Join(cfgDir, "cs2-server-manager")
		return filepath.Join(dir, "update.json")
	}

	return ""
}

func isNewerVersion(current, latest string) bool {
	cur := parseSemver(current)
	lat := parseSemver(latest)

	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	return lat[2] > cur[2]
}

func parseSemver(v string) [3]int {
	var res [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			n = 0
		}
		res[i] = n
	}
	return res
}


