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
		// What it would take down, counted off the same state the close dialog
		// counts: a destructive answer given without the toll is a guess, and this
		// one used to say only that it could not be undone.
		return m.simpleOverlayPanel("", "Delete session?",
			[]string{
				"'" + m.SessionLabel(m.SessionSwitcherConfirmDelete) + "'",
				m.SessionTollFor(m.SessionSwitcherConfirmDelete).Line(),
				"", "This cannot be undone.",
			},
			[]overlay.Hint{{Key: "y", Label: "delete"}, {Key: "n", Label: "cancel"}, {Key: "esc", Label: "cancel"}})
	}

	// Arbitrated over every session the switcher knows, not over the filtered
	// rows: a query is a view, and a session must not change colour because
	// something else was typed out of sight.
	m.refreshSessionColorsFor(m.SessionSwitcherItems)

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
	// A restored session is by definition not the current one, so the tag sits
	// in the slot "current" would otherwise hold and never competes with it.
	if item.Restored {
		right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(sidebarRestoredTag+"  ") + right
	}

	// The identity is shown only when a display name is hiding it, so an
	// unrenamed session reads exactly as it always has.
	identity := ""
	if item.Title != item.ID {
		identity = " (" + printableTitle(item.ID) + ")"
	}

	// The switcher wears the rail's identity mark in the rail's colour, so the
	// row picked here is recognisably the row that was being looked at there.
	// It leads the label rather than riding the marker column, which the
	// selection cursor owns.
	mark, markW := "", 0
	if tint := m.sessionTint(item.ID, rowBg); tint != nil {
		mark = overlay.Style(rowBg).Foreground(tint).Render(accentMark() + " ")
		markW = 2
	}

	avail := max(width-lipgloss.Width(right)-len(identity)-markW-4, 1)
	labelColor := pal.FgDim
	if selected {
		labelColor = pal.Fg
	}
	left := mark + overlay.Style(rowBg).Foreground(labelColor).Bold(selected).
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
