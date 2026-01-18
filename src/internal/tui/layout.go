package tui

import (
	"strings"
)

// padViewToHeight is a small helper that ensures a view string occupies a
// stable number of lines relative to the current terminal height. This helps
// prevent "ghost" lines from previous frames when views toggle content on/off
// (e.g. showing/hiding extra form rows) without having to manage a full
// viewport for every page.
//
// targetOffset allows callers to reserve a few lines for outer chrome
// (tab bar, global status line, etc.) so the view itself doesn't push the top
// border off-screen.
func (m model) padViewToHeight(view string, targetOffset int) string {
	if m.height <= 0 {
		return view
	}

	target := m.height - targetOffset
	if target < 1 {
		target = m.height
	}

	lines := strings.Count(view, "\n")
	if lines < target {
		view += strings.Repeat("\n", target-lines)
	}
	return view
}

// layoutViewport reflows the current viewport content to the terminal width
// (when known) and clamps the height so we don't render excessive empty space
// below the content or push the header off-screen. This follows Bubble Tea's
// recommended pattern of capturing WindowSizeMsg and wrapping content
// accordingly.
func (m *model) layoutViewport() {
	// This function is kept for compatibility but the viewport content
	// is managed directly by viewport methods in the PR version.
	// The viewport is sized and updated directly where it's used.
}



