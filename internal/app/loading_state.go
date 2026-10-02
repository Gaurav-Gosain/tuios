package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
	// The clock is read on the first load found, not on every message.
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
