package app

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
			[]string{"'" + m.SessionSwitcherConfirmDelete + "'", "", "This cannot be undone."},
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
			{Key: "ctrl+d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			item := filtered[i]
			trailing, trailColor := "", pal.FgMute
			if item.IsCurrent {
				trailing, trailColor = "current", pal.Success
			}
			labelColor := pal.FgDim
			if selected {
				labelColor = pal.Fg
			}
			// Laundered on the way to the screen only: item.Title is also the name
			// the switch is made by, so the model keeps it verbatim.
			name := overlay.Truncate(printableTitle(item.Title), max(width-lipgloss.Width(trailing)-4, 1))
			return listRowLine(width, listRowMarker(selected), name, trailing, labelColor, trailColor, selected, rowBg, pal)
		},
	})
}
