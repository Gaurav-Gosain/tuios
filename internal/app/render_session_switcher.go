package app

import (
	"image/color"
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

const sessionSwitcherWidth = 58

// renderSessionSwitcher renders the session switcher on the shared overlay
// grammar, returning the panel, geometry and hit rows.
func (m *OS) renderSessionSwitcher() (string, overlay.Geometry, []overlayRowHit) {
	// Daemon-only feature.
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return m.simpleOverlayPanel("", "Sessions",
			[]string{"Session management requires daemon mode.", "", "Start a daemon session with: tuios new"},
			[]overlay.Hint{{Key: "esc", Label: "close"}})
	}

	// Delete confirmation takes over the panel body.
	if m.SessionSwitcherConfirmDelete != "" {
		return m.simpleOverlayPanel("", "Delete session?",
			[]string{"'" + m.SessionLabel(m.SessionSwitcherConfirmDelete) + "'", "", "This cannot be undone."},
			[]overlay.Hint{{Key: "y", Label: "delete"}, {Key: "n", Label: "cancel"}, {Key: "esc", Label: "cancel"}})
	}

	filtered := FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery)
	if len(filtered) > 0 {
		m.SessionSwitcherSelected = clampInt(m.SessionSwitcherSelected, 0, len(filtered)-1)
	}

	empty := "No sessions found"
	if m.SessionSwitcherQuery != "" {
		empty = "No match, Enter to create '" + m.SessionSwitcherQuery + "'"
	}

	return m.renderListOverlay(listOverlay{
		Glyph:      "",
		Title:      "Sessions",
		Width:      sessionSwitcherWidth,
		MaxVisible: 10,
		Search:     true,
		Query:      m.SessionSwitcherQuery,
		Count:      len(filtered),
		Selected:   m.SessionSwitcherSelected,
		Scroll:     &m.SessionSwitcherScroll,
		EmptyMsg:   empty,
		Hints: []overlay.Hint{
			{Key: "⏎", Label: "switch"},
			{Key: "ctrl+r", Label: "rename"},
			{Key: "ctrl+d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.sessionSwitcherRow(filtered[i], selected, rowBg, pal, width)
		},
	})
}

// sessionSwitcherRow draws one session: its label, the identity behind it when a
// rename has made them differ, its pane count, and the worst agent state any of
// its panes is in. The state is the reason the row is this wide: a session with
// something blocked has to be visible before the switch, not after it.
func (m *OS) sessionSwitcherRow(item sessiontree.Node, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	// Right half first: it is fixed-width, so the label gets whatever is left.
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(panePlural(item.WindowCount))
	if glyph := agentStateIndicator(item.AgentState); glyph != "" {
		right += overlay.Style(rowBg).Foreground(agentGlyphColor(item.AgentState, pal)).
			Bold(sidebarAttention(item.AgentState)).Render(" " + glyph)
	}
	if item.IsCurrent {
		right = overlay.Style(rowBg).Foreground(pal.Success).Render("current  ") + right
	}

	// The identity is shown only when a display name is hiding it, so an
	// unrenamed session reads exactly as it always has.
	identity := ""
	if item.Title != item.ID {
		identity = " (" + printableTitle(item.ID) + ")"
	}

	avail := max(width-lipgloss.Width(right)-len(identity)-4, 1)
	labelColor := pal.FgDim
	if selected {
		labelColor = pal.Fg
	}
	left := overlay.Style(rowBg).Foreground(labelColor).Bold(selected).
		Render(overlay.Truncate(printableTitle(item.Title), avail))
	if identity != "" {
		left += overlay.Style(rowBg).Foreground(pal.FgMute).Render(identity)
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

// panePlural renders a pane count for a switcher row.
func panePlural(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return strconv.Itoa(n) + " panes"
}
