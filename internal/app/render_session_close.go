package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// sessionCloseInnerWidth is the dialog's preferred inner width, sized so the
// toll line still fits with both agent clauses on it. The blocked call-out is
// last and would be the part cut, and it is the one the dialog exists to show.
const sessionCloseInnerWidth = 54

// renderSessionClose draws the close confirmation: the question, what closing
// would take down counted as the frame is built, and the answer rows — two, or
// three when the target session sits in a worktree.
//
// A dialog rather than a panel because it is one focused question with a few
// answers, which is exactly what the micro-dialog is for.
func (m *OS) renderSessionClose() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(sessionCloseInnerWidth, m.GetRenderWidth())
	rows := m.sessionCloseRowCount()
	m.SessionCloseSelected = clampInt(m.SessionCloseSelected, 0, rows-1)

	toll := m.SessionTollFor(m.SessionCloseTarget)
	if m.SessionCloseWindow != "" {
		toll = m.windowToll(m.windowIndexByID(m.SessionCloseWindow))
	} else if m.SessionCloseWorkspace != 0 {
		toll = m.workspaceToll(m.SessionCloseWorkspace)
	}
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
	// The worktree toll line sits between the Close row and the delete row,
	// so the delete row's rect moves down one when it is drawn.
	tollLineDrawn := false
	if rows > SessionCloseRowDeleteWorktree {
		if line := m.worktreeTollLine(); line != "" {
			// The delete row's own toll, warn-coloured when it names the
			// refusal: commits a detached HEAD holds are the one thing on the
			// line that the row will not do.
			t := m.SessionCloseWorktreeToll
			lineColor := pal.FgDim
			if t != nil && t.Own > 0 {
				lineColor = pal.Warn
			}
			body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
				overlay.Style(bg).Foreground(lineColor).Render(overlay.Truncate(line, max(width-1, 1))), width, bg))
			tollLineDrawn = true
		}
		body = append(body, m.sessionCloseRow(SessionCloseRowDeleteWorktree, width, pal))
	}

	content, geo := overlay.Dialog{
		Title: m.sessionCloseTitle(),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "run"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)

	// One rect per drawn row, in drawn order, so a click answers the dialog the
	// user is looking at. A row that is not drawn has no rect.
	hits := make([]overlayRowHit, 0, rows)
	for i := range rows {
		y := geo.BodyY + 3 + i
		if tollLineDrawn && i == SessionCloseRowDeleteWorktree {
			y++
		}
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// sessionCloseRow draws one answer. The delete row is destructive like the
// Close row: it is the more destructive of the two, and it is the answer
// nobody should reach by walking past it without looking.
func (m *OS) sessionCloseRow(idx, width int, pal overlay.Palette) string {
	label := "Cancel"
	destructive := false
	switch idx {
	case SessionCloseRowClose:
		label, destructive = m.sessionCloseLabel(), true
	case SessionCloseRowDeleteWorktree:
		label, destructive = "Delete worktree too", true
	}
	return dialogAnswerRow(label, m.SessionCloseSelected == idx, destructive, width, pal)
}

// dialogAnswerRow draws one answer of a confirmation. The destructive row
// takes the warn colour on the cursor and stays muted off it, which is the
// same weight split the dock controls use, so the button and the row that
// confirms it read as one thing. The restart dialog draws its answers with it
// too.
func dialogAnswerRow(label string, cursor, destructive bool, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}
	labelColor := pal.FgDim
	switch {
	case destructive && cursor:
		labelColor = pal.Warn
	case destructive:
		labelColor = pal.FgMute
	case cursor:
		labelColor = pal.Fg
	}
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(labelColor).Bold(cursor).Render(label)
	return overlay.Fill(row, width, bg)
}
