package tui

// currentVersion is the version string shown in the TUI title. In a full
// release build this is typically overridden at link-time via -ldflags.
// IMPORTANT: do not include a leading "v" here; the UI adds it when rendering.
const currentVersion = "1.7.1"

// Version returns the current CSM version string used by the TUI and logs.
func Version() string {
	return currentVersion
}
