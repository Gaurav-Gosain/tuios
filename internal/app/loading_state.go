package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Loading states are drawn only once a load has run past overlay.LoadingDelay.
// Most reads answer well inside it, and a "reading" line that is drawn for one
// frame and gone the next is a flicker rather than news. A load records when
// it started next to its flag, the renderer asks overlay.ShowLoading, and
// Update arms one frame at the delay (loadingFrameCmd) so the state is drawn
// even when nothing else would draw a frame then.

// loadingShownMsg is the frame asked for at the loading delay. It carries
// nothing: the renderer reads each load's start time.
type loadingShownMsg struct{}

// loadingFrameCmd arms the frame for the earliest load that has not had one
// armed yet. Update asks it after every message, like the files sync, so the
// places that start a load do not each have to carry a timer. It reads a few
// fields and allocates nothing when no load is out.
func (m *OS) loadingFrameCmd() tea.Cmd {
	now := time.Now()
	var due time.Time
	consider := func(loading bool, since time.Time) {
		if !loading || since.IsZero() {
			return
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

// emptyPanel draws a panel whose body is an empty state: the message centred
// in rows rows, with detail wrapped to the panel's width and one key hint
// under it. It is for overlays that have nothing to list, which used to say
// so in a line of text at the top left of an otherwise blank panel.
func (m *OS) emptyPanel(title string, preferredWidth, rows int, message string, detail []string, hint overlay.Hint, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
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
