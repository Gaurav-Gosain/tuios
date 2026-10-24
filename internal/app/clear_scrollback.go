package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// clearScrollbackTimeout bounds the daemon's clear. The daemon waits for its
// emulator to take the output queued before the clear, which is none at a
// prompt.
const clearScrollbackTimeout = 10 * time.Second

// clearScrollbackDoneMsg is the daemon's answer to a clear.
type clearScrollbackDoneMsg struct{ err error }

// clearScrollbackRequest is what the daemon clear needs, copied off the model
// so it can run off the update goroutine.
type clearScrollbackRequest struct {
	Session, Window, Host, Build string
}

// ClearScrollback clears the focused pane's scrollback and the rows above its
// prompt, which is what cmd+k does in Ghostty, iTerm2 and Terminal.app. The
// prompt row moves to the top of the pane, and nothing is typed to the
// program. See session.ClearHistorySequence.
//
// In a daemon session the daemon does the clear, through the clear-history
// verb. It writes the clear into the pane's stream, so every client of the
// session and the daemon's own emulator clear at the same byte, and an attach
// later does not bring the history back. Clearing this client's emulator
// alone would last only until the next attach.
func (m *OS) ClearScrollback() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil || w.Terminal == nil {
		m.ShowNotification("No pane is open. Open a pane to clear its scrollback.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	// The history the copy view shows is about to go.
	if w.InCopyMode() {
		w.ExitCopyMode()
	}

	if m.IsDaemonSession && m.DaemonClient != nil {
		req := clearScrollbackRequest{
			Session: m.SessionName,
			Window:  w.ID,
			Host:    m.AttachedHost,
			Build:   m.DaemonClient.ClientVersion(),
		}
		return func() tea.Msg {
			return clearScrollbackDoneMsg{err: clearScrollbackInDaemon(req)}
		}
	}

	w.LockIO()
	data, err := session.ClearHistorySequence(w.Terminal, true)
	if err == nil {
		_, _ = w.Terminal.Write(data)
	}
	w.UnlockIO()
	if err != nil {
		m.ShowNotification(clearScrollbackNote(err), "warning", m.Settings.NotificationDuration)
		return nil
	}
	w.ScrollbackOffset = 0
	w.MarkContentDirty()
	w.InvalidateCache()
	return nil
}

// clearScrollbackInDaemon asks the session's daemon to clear the pane.
func clearScrollbackInDaemon(req clearScrollbackRequest) error {
	c, err := dialSessionDaemon(req.Host, req.Build)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_, err = c.CallWithTimeout("clear-history", map[string]any{
		"session": req.Session,
		"window":  req.Window,
		"screen":  true,
	}, clearScrollbackTimeout)
	return err
}

// applyClearScrollbackDone reports a clear that failed. A clear that worked
// shows itself: the pane's stream brings it.
func (m *OS) applyClearScrollbackDone(msg clearScrollbackDoneMsg) {
	if msg.err == nil {
		return
	}
	m.LogError("Could not clear the scrollback: %v", msg.err)
	m.ShowNotification(clearScrollbackNote(msg.err), "warning", m.Settings.NotificationDuration)
}

// clearScrollbackNote is the message for a clear that failed.
func clearScrollbackNote(err error) string {
	if session.IsFullScreenError(err) {
		return "A full-screen program uses the pane. Quit the program, then clear the scrollback."
	}
	if refusal := linkRefusal("", err); refusal != "" {
		return refusal
	}
	return "tuios cannot clear the scrollback: " + err.Error()
}
