package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type loadingShownMsg struct{}

func (m *OS) loadingFrameCmd() tea.Cmd {
	var now, due time.Time
	consider := func(loading bool, since time.Time) {
		if !loading || since.IsZero() {
			return
		}
		if now.IsZero() {
			now = time.Now()
		}
		at := since.Add(overlay.LoadingDelay)
		if at.After(now) && at.After(m.loadingFrameAt) && (due.IsZero() || at.Before(due)) {
			due = at
		}
	}
	consider(m.ShowAgentMail && m.AgentMail.Loading, m.AgentMail.LoadingSince)
	if p := m.Inbox.Peek; p != nil {
		consider(p.Loading, p.LoadingSince)
	}
	consider(m.review.loading, m.review.loadingSince)
	consider(m.ShowLauncher && len(m.LauncherItems) == 0, m.LauncherOpenedAt)
	if due.IsZero() {
		return nil
	}
	m.loadingFrameAt = due
	return tea.Tick(due.Sub(now), func(time.Time) tea.Msg { return loadingShownMsg{} })
}

func (m *OS) emptyPanel(title string, preferredWidth, rows int, message string, detail []string, hint overlay.Hint, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Surface
	width := m.panelWidth(preferredWidth)
	var wrapped []string
	for _, d := range detail {
		wrapped = append(wrapped, wrapPlain(d, max(width-4, 1))...)
	}
	e := overlay.Empty{Message: message, Detail: wrapped, Hint: hint}
	rows, hints = m.panelBody(max(rows, len(wrapped)+4), 0, width, nil, hints)
	panel := overlay.Panel{
		Title: title,
		Width: width,
		Body:  strings.Join(e.Lines(width, rows, bg, pal), "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}
