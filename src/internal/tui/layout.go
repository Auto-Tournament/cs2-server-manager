package tui

import (
	"strings"

	"github.com/muesli/reflow/wordwrap"
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
	if m.vpRawContent == "" || m.vp.Width == 0 {
		return
	}

	content := m.vpRawContent

	// Respect terminal width when wrapping viewport content. Leave a small
	// margin so the border/header don't butt against the terminal edge.
	if m.width > 0 {
		wrapWidth := m.width - 4
		if wrapWidth < 20 {
			wrapWidth = m.width
		}
		if wrapWidth > 0 {
			content = wordwrap.String(content, wrapWidth)
		}
	}

	m.vp.SetContent(content)

	// Track how many logical lines of content we have so we can size the
	// viewport to the smaller of "content lines" and "available height".
	lines := strings.Count(content, "\n") + 1
	if lines < 1 {
		lines = 1
	}
	m.vpContentLines = lines

	// Compute the maximum viewport height based on terminal rows, then clamp
	// to the number of content lines with a small floor so the viewport never
	// collapses to a tiny box.
	maxH := 20
	if m.height > 0 {
		maxH = m.height - 8
		if maxH < 4 {
			maxH = 4
		}
	}

	h := maxH
	if lines < maxH {
		if lines < 4 {
			h = 4
		} else {
			h = lines
		}
	}

	m.vp.Height = h
}



