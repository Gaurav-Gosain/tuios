package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// sessionCloseInnerWidth is the dialog's preferred inner width, sized so the
// toll line still fits with both agent clauses on it. The blocked call-out is
// last and would be the part cut, and it is the one the dialog exists to show.
const sessionCloseInnerWidth = 54

// renderSessionClose draws the close confirmation: the question, what closing
// would take down counted as the frame is built, and the two rows.
//
// A dialog rather than a panel because it is one focused question with two
// answers, which is exactly what the micro-dialog is for.
func (m *OS) renderSessionClose() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(sessionCloseInnerWidth, m.GetRenderWidth())
	m.SessionCloseSelected = clampInt(m.SessionCloseSelected, 0, sessionCloseRowCount-1)

	toll := m.SessionTollFor(m.SessionCloseTarget)
	// The toll is warn-coloured only when an agent is in it. Colouring it always
	// would spend the one loud thing in the dialog on "three shells are open",
	// and then have nothing left for the case that matters.
	tollColor := pal.FgDim
	if toll.Working > 0 || toll.Blocked > 0 {
		tollColor = pal.Warn
	}

	body := []string{
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(m.sessionCloseQuestion(), max(width-1, 1))), width, bg),
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(tollColor).Render(overlay.Truncate(toll.Line(), max(width-1, 1))), width, bg),
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
		m.sessionCloseRow(SessionCloseRowCancel, width, pal),
		m.sessionCloseRow(SessionCloseRowClose, width, pal),
	}

	content, geo := overlay.Dialog{
		Title: "close session",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "run"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)

	// One rect per drawn row, in drawn order, so a click answers the dialog the
	// user is looking at.
	hits := make([]overlayRowHit, 0, sessionCloseRowCount)
	for i := range sessionCloseRowCount {
		y := geo.BodyY + 3 + i
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// sessionCloseRow draws one answer. The destructive row takes the warn colour
// on the cursor and stays muted off it, which is the same weight split the dock
// controls use, so the button and the row that confirms it read as one thing.
func (m *OS) sessionCloseRow(idx, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	cursor := m.SessionCloseSelected == idx
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}

	label, labelColor := "Cancel", pal.FgDim
	if idx == SessionCloseRowClose {
		label, labelColor = "Close session", pal.FgMute
		if cursor {
			labelColor = pal.Warn
		}
	} else if cursor {
		labelColor = pal.Fg
	}

	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(labelColor).Bold(cursor).Render(label)
	return overlay.Fill(row, width, bg)
}
