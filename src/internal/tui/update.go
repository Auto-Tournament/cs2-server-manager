package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// isNewerVersion performs a simple semver-ish comparison of two version
// strings. It returns true if latest represents a newer version than current.
func isNewerVersion(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}
	if current == latest {
		return false
	}
	// For now, treat any differing string as "newer" to keep behaviour simple.
	return current != latest
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
