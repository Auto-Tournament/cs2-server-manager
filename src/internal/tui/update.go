package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// updateInfoMsg reports the result of a background update check.
type updateInfoMsg struct {
	latest string
	err    error
}

// forceUpdateInfoMsg is like updateInfoMsg but for explicit "force" checks.
type forceUpdateInfoMsg struct {
	latest string
	err    error
}

// checkForUpdates checks GitHub Releases for the latest version tag and sends
// an updateInfoMsg back into the TUI.
func checkForUpdates() tea.Cmd {
	return func() tea.Msg {
		latest, err := fetchLatestVersion()
		return updateInfoMsg{latest: latest, err: err}
	}
}

// checkForUpdatesForce forces an immediate update check and returns a
// forceUpdateInfoMsg.
func checkForUpdatesForce() tea.Cmd {
	return func() tea.Msg {
		latest, err := fetchLatestVersion()
		return forceUpdateInfoMsg{latest: latest, err: err}
	}
}

// isNewerVersion performs a simple semver comparison of two version strings.
// It returns true if latest represents a newer version than current, and never
// reports a downgrade as an update (e.g. 1.2.3 -> 1.2.2).
func isNewerVersion(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}
	if current == latest {
		return false
	}

	parse := func(v string) (major, minor, patch int, ok bool) {
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(v, "v")
		parts := strings.SplitN(v, ".", 3)
		if len(parts) < 1 {
			return 0, 0, 0, false
		}
		toInt := func(s string) int {
			n, _ := strconv.Atoi(s)
			return n
		}
		major = toInt(parts[0])
		if len(parts) > 1 {
			minor = toInt(parts[1])
		}
		if len(parts) > 2 {
			patch = toInt(parts[2])
		}
		return major, minor, patch, true
	}

	cMaj, cMin, cPatch, _ := parse(current)
	lMaj, lMin, lPatch, _ := parse(latest)

	if lMaj > cMaj {
		return true
	}
	if lMaj < cMaj {
		return false
	}
	if lMin > cMin {
		return true
	}
	if lMin < cMin {
		return false
	}
	return lPatch > cPatch
}

// fetchLatestVersion calls the GitHub Releases API to discover the latest tag.
func fetchLatestVersion() (string, error) {
	const url = "https://api.github.com/repos/sivert-io/cs2-server-manager/releases/latest"

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update check failed with status %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.TagName, nil
}
