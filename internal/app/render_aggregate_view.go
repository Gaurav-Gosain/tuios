package app

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The all-windows overlay is a jump-to-window picker, so it is drawn as one:
// the same search-and-list panel the command palette, the session switcher and
// the workspace switcher use, on the same grammar, with the same keys.
//
// It used to be a bespoke two-pane layout sized to fixed fractions of the
// screen, four fifths wide by three quarters tall whatever it held. A session
// with one window got a modal covering most of the terminal and almost entirely
// empty, and the right-hand pane showed the selected pane's raw output with no
// framing, which on a pane full of compiler diagnostics read as a wall of noise
// beside the list rather than as information about it. It also returned no hit
// rows, so the one overlay whose whole purpose is picking something could not be
// clicked.
//
// The preview is gone rather than fixed. The question this overlay answers is
// "which of my windows do I want", and the last few lines of a pane's scrollback
// answer it far worse than the pane's name, where it is, what it is doing and
// what directory it is in, all of which fit on the row itself. Dropping it also
// stops the list rebuilding a preview string for every window, under the pane's
// IO lock, on every keystroke.

const (
	// Wide enough for a name, a directory and a workspace tag on one row, and
	// the same order of width as the session switcher beside it.
	aggregateViewWidth = 62
	// Where a list stops being scannable and starts being scrolled. The panel
	// takes the smaller of this and what it actually holds, so one window gets a
	// one-row panel, and panelBody trims it again on a short screen.
	aggregateViewMaxRows = 12
	// A directory is context for the name, never the thing being read, so it is
	// held to a tag's worth of width.
	aggregateViewCWDMax = 18
)

// renderAggregateView renders the all-windows picker and returns the panel, its
// geometry and its hit rows.
func (m *OS) renderAggregateView() (string, overlay.Geometry, []overlayRowHit) {
	items := m.GetAggregateViewItems()
	filtered := FilterAggregateViewItems(items, m.AggregateViewQuery)
	if len(filtered) > 0 {
		m.AggregateViewSelected = clampInt(m.AggregateViewSelected, 0, len(filtered)-1)
	}

	empty := "No windows"
	if m.AggregateViewQuery != "" {
		empty = "No window matches that"
	}

	return m.renderListOverlay(listOverlay{
		Glyph:      "\uf009", // grid of panes
		Title:      "Windows",
		Width:      aggregateViewWidth,
		MaxVisible: min(max(len(filtered), 1), aggregateViewMaxRows),
		Search:     true,
		Query:      m.AggregateViewQuery,
		Count:      len(filtered),
		Selected:   m.AggregateViewSelected,
		Scroll:     &m.AggregateViewScroll,
		EmptyMsg:   empty,
		Hints: []overlay.Hint{
			{Key: "⏎", Label: "jump"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.aggregateViewRow(filtered[i], selected, rowBg, pal, width)
		},
	})
}

// aggregateViewRow draws one window: what it is called, what its agent is doing,
// where it is, and which directory it is in.
//
// The order is the order the question gets asked in. The name is what the user
// is looking for, so it leads and gets whatever width is left over; the agent
// glyph rides in front of it in the rail's own colours, because a pane waiting
// on an answer is the usual reason for opening this list at all; and the
// workspace tag anchors the right edge, because "where is it" is the thing the
// jump is about to act on.
//
// The pane's pixel dimensions used to sit on every row. They are the same number
// for every tiled pane in a session, so the column said nothing and cost the
// name eleven cells.
func (m *OS) aggregateViewRow(item AggregateViewItem, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	// Right cluster first: it is fixed-width, so the name gets the remainder.
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(m.workspaceTag(item.Workspace))
	if flag := aggregateRowFlag(item); flag != "" {
		right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(flag+"  ") + right
	}
	if dir := aggregateShortCWD(item.CWD); dir != "" {
		right = overlay.Style(rowBg).Foreground(pal.FgDim).Render(dir+"  ") + right
	}

	// The focused pane is marked in the accent rather than with the asterisk it
	// used to wear jammed against its name, which read as part of the name.
	mark, markW := "  ", 2
	if state := item.Window.AgentState; agentStateIndicator(state) != "" {
		mark = overlay.Style(rowBg).Foreground(agentGlyphColor(state, pal)).
			Bold(sidebarAttention(state)).Render(agentStateIndicator(state)) + " "
	} else if item.IsFocused {
		mark = overlay.Style(rowBg).Foreground(pal.Accent).Render(accentMark()) + " "
	}

	avail := max(width-lipgloss.Width(right)-markW-3, 1)
	nameColor := pal.FgDim
	if selected {
		nameColor = pal.Fg
	}
	left := mark + overlay.Style(rowBg).Foreground(nameColor).Bold(selected).
		Render(overlay.Truncate(item.Title, avail))

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-1, 1)
	return " " + left + overlay.Style(rowBg).Render(strings.Repeat(" ", gap)) + right
}

// aggregateRowFlag names a window that is not simply sitting in its layout.
// Minimized wins over floating: a minimized pane is not on screen at all, which
// is the more surprising of the two to find in a list of windows.
func aggregateRowFlag(item AggregateViewItem) string {
	switch {
	case item.IsMinimized:
		return "min"
	case item.IsFloating:
		return "float"
	default:
		return ""
	}
}

// aggregateShortCWD is the tail of a working directory, which is the part that
// tells two shells apart. The home directory reads as "~" the way every shell
// prompt writes it, and an unknown cwd (every platform but Linux) is no column
// at all rather than an empty one.
func aggregateShortCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if cwd == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(cwd, home+string(filepath.Separator)); ok {
			cwd = "~" + string(filepath.Separator) + rest
		}
	}
	if lipgloss.Width(cwd) <= aggregateViewCWDMax {
		return cwd
	}
	// Cut from the left, and cut at a separator. The leaf is what identifies the
	// directory and the ellipsis says the path goes on above it, but a cut taken
	// mid-segment lands in the middle of whatever the segment is called, and on a
	// path with a hash in it that is a row of noise where the context should be.
	segs := strings.Split(cwd, string(filepath.Separator))
	tail := ""
	for i := len(segs) - 1; i >= 0; i-- {
		next := string(filepath.Separator) + segs[i] + tail
		if lipgloss.Width(next)+1 > aggregateViewCWDMax {
			break
		}
		tail = next
	}
	if tail == "" {
		// One segment on its own is longer than the column, so there is no
		// separator to cut at and the leaf itself has to give.
		return "…" + overlay.Truncate(segs[len(segs)-1], aggregateViewCWDMax-1)
	}
	return "…" + tail
}
