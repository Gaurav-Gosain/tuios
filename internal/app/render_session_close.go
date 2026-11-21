package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const sessionCloseInnerWidth = 54

func (m *OS) renderSessionClose() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(sessionCloseInnerWidth, m.modalRoomWidth())
	rows := m.sessionCloseRowCount()
	m.closeList.Fit(rows, rows)

	toll := m.SessionTollFor(m.SessionCloseTarget)
	if m.SessionCloseWindow != "" {
		toll = m.windowToll(m.windowIndexByID(m.SessionCloseWindow))
	} else if m.SessionCloseWorkspace != 0 {
		toll = m.workspaceToll(m.SessionCloseWorkspace)
	}
	tollColor := pal.FgDim
	if toll.Working > 0 || toll.Blocked > 0 {
		tollColor = pal.Warn
	}
	tollLine := toll.Line()
	if m.SessionCloseWindow != "" {
		tollLine = m.windowTollLine(m.windowIndexByID(m.SessionCloseWindow), toll)
	}

	body := []string{
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(m.sessionCloseQuestion(), max(width-1, 1))), width, bg),
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(tollColor).Render(overlay.Truncate(tollLine, max(width-1, 1))), width, bg),
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
		m.sessionCloseRow(SessionCloseRowCancel, width, pal),
		m.sessionCloseRow(SessionCloseRowClose, width, pal),
	}
	tollLineDrawn := false
	if rows > SessionCloseRowDeleteWorktree {
		if line := m.worktreeTollLine(); line != "" {
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

func (m *OS) sessionCloseRow(idx, width int, pal overlay.Palette) string {
	label := "Cancel"
	destructive := false
	switch idx {
	case SessionCloseRowClose:
		label, destructive = m.sessionCloseLabel(), true
	case SessionCloseRowDeleteWorktree:
		label, destructive = "Delete worktree too", true
	}
	return dialogAnswerRow(label, m.closeList.Cursor == idx, destructive, width, pal)
}

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
