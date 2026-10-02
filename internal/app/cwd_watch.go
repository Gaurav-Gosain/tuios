package app

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// CwdChangedMsg carries an OSC 7 working-directory change from a window's PTY
// writer goroutine to the Update loop, which decides (on the focused window
// only) whether to look for a project tape. It mirrors NotificationMsg: the VT
// callback runs off the render goroutine and cannot touch OS state directly.
type CwdChangedMsg struct {
	WindowID string
	Cwd      string
}

// ListenForCwdChange waits for the next working-directory change and delivers it
// to the Update loop as a CwdChangedMsg.
func ListenForCwdChange(ch chan CwdChangedMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// ensureCwdChangeChan lazily creates the cwd-change channel. Called only on the
// Update goroutine (from Init and window setup), so the nil check needs no lock.
func (m *OS) ensureCwdChangeChan() chan CwdChangedMsg {
	if m.PendingCwdChange == nil {
		m.PendingCwdChange = make(chan CwdChangedMsg, 16)
	}
	return m.PendingCwdChange
}

// setupCwdWatch wires a window's OSC 7 working-directory callback to the
// cwd-change channel. The callback fires on the window's PTY writer goroutine,
// so it only does a non-blocking send; the actual work happens on the Update
// goroutine where OS state is owned.
func (m *OS) setupCwdWatch(window *terminal.Window) {
	if window == nil {
		return
	}
	ch := m.ensureCwdChangeChan()
	id := window.ID
	window.CwdFunc = func(cwd string) {
		select {
		case ch <- CwdChangedMsg{WindowID: id, Cwd: cwd}:
		default:
			// Channel full: drop. A missed cwd change only defers detection to
			// the next one; it never blocks the PTY reader.
		}
	}
}

// localCwdPath extracts a local filesystem path from an OSC 7 payload. OSC 7
// carries a file://host/path URI; a bare path is also accepted for shells that
// emit one. A non-empty, non-local host means a remote shell, which is ignored.
func localCwdPath(raw string) (string, bool) {
	path, host, ok := session.ParseCwdAnnouncement(raw)
	if !ok || host != "" {
		return "", false
	}
	return path, true
}

// isLocalHost reports whether an OSC 7 host refers to this machine.
func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	if h, err := os.Hostname(); err == nil && strings.EqualFold(h, host) {
		return true
	}
	return false
}

// onCwdChange handles a raw working-directory change. It filters to the focused
// window and, when the feature is enabled, schedules a debounced evaluation.
// It returns the debounce command (or nil), and is a no-op that returns nil for
// background windows, the off mode, and remote (SSH) directories.
func (m *OS) onCwdChange(msg CwdChangedMsg) tea.Cmd {
	// Record it on the window first, ahead of every gate below.
	//
	// Those gates are the tape detector's, and they are narrow on purpose: it
	// only cares about the focused pane, and only while its own feature is on.
	// The directory itself is a fact about the pane that other surfaces want
	// whether or not the tape detector is running, and folding the recording
	// into the detector's conditions is how the rail's file view would have come
	// out empty for anyone with tape autorun off.
	m.recordWindowCwd(msg.WindowID, msg.Cwd)
	return m.projectTapeOnCwdChange(msg)
}
