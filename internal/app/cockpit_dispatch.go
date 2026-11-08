package app

import (
	"cmp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Task dispatch on the board (internal/session/task_dispatch.go).
//
// The board's header names the dispatch of one board: the project of the
// focused pane's folder, as the capture sheet picks it, or the session's own
// cards when the folder has none. It says Manual, Pull or Auto (with the
// agents auto runs and the cards that wait), and Paused,
// Stopped or Quiet hours when a switch holds dispatch. ctrl+t turns the
// board from manual to pull to auto, and ctrl+x pauses all dispatch or starts it
// again. Both are chords: a printable key never changes what agents are
// given. Turning dispatch on, or starting it again, is the person's: a key
// that send-keys or a tape typed is refused. Pause is open to anyone.

// ckDispatchMsg is a get-task-dispatch or set-task-dispatch reply. project
// is the project the board is for, "" for the session's own cards.
type ckDispatchMsg struct {
	st      session.DispatchStatus
	project string
	err     error
	// set says the reply is to a change the person made.
	set bool
}

// cockpitDispatchCmd reads the dispatch of the board the header names.
func (m *OS) cockpitDispatchCmd() tea.Cmd {
	if m.AttachedHost != "" || m.SessionName == "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	call, sess := m.inboxCaller(), m.SessionName
	dir := ""
	if !m.BrowserClient && (!m.RemoteClient || m.SSHIsLoopback) {
		dir = m.spawnFolder()
	}
	return func() tea.Msg {
		project := dispatchProjectOf(dir)
		params := map[string]any{"session": sess}
		if project != "" {
			params = map[string]any{"project": project}
		}
		raw, err := call("get-task-dispatch", params, 5*time.Second)
		msg := ckDispatchMsg{project: project, err: err}
		if err == nil {
			msg.err = jsonx.Unmarshal(raw, &msg.st)
		}
		return msg
	}
}

// dispatchProjectOf is the project of dir, "" for a folder with no origin
// this client can read.
func dispatchProjectOf(dir string) string {
	if dir == "" {
		return ""
	}
	url, err := worktree.OriginURL(dir)
	if n := worktree.NormalizeRemote(url); err != nil || n == "" || strings.HasPrefix(n, "file:") {
		return ""
	}
	return url
}

// applyDispatch takes a dispatch reply.
func (m *OS) applyDispatch(msg ckDispatchMsg) {
	ck := &m.Inbox.ck
	if msg.set {
		ck.dispatchBusy = false
	}
	if msg.err != nil {
		if msg.set {
			m.ShowNotification("Dispatch did not change: "+printableTitle(msg.err.Error()), "error", m.Settings.NotificationDuration)
		}
		return
	}
	st := msg.st
	if b := st.Board; msg.set && b != nil && b.Preset != nil && b.Mode == session.DispatchAuto {
		m.ShowNotification("Auto starts "+printableTitle(b.Preset.Agent)+" for each open card. ctrl+x pauses it.", "info", m.Settings.NotificationDuration)
	}
	ck.dispatch = &st
	if st.Board != nil {
		ck.dispatchBoard = st.Board.Board
	}
	m.cockpitTouch()
}

// applyDispatchEvent takes a task-dispatch event: the switches and modes as
// they are now. A stop dispatch made on its own is said as a notice.
func (m *OS) applyDispatchEvent(action, reason string, st *session.DispatchStatus) {
	ck := &m.Inbox.ck
	next := *st
	next.Board = nil
	if ck.dispatchBoard != "" {
		row := session.DispatchBoardRow{Board: ck.dispatchBoard, Mode: session.DispatchManual}
		if ck.dispatch != nil && ck.dispatch.Board != nil {
			row = *ck.dispatch.Board
			row.Mode, row.Failures, row.Stopped = session.DispatchManual, 0, ""
		}
		for _, b := range st.Boards {
			if b.Board == ck.dispatchBoard {
				row = b
			}
		}
		next.Board = &row
	}
	ck.dispatch = &next
	if action == "stopped" && reason != "" {
		m.ShowNotification("Task dispatch stopped: "+printableTitle(reason)+". Press ctrl+x on the board to start it again.", "warning", m.Settings.NotificationDuration*2)
	}
	m.cockpitTouch()
}

// dispatchCounts is an auto board's "2 running, 1 waiting", "" for any
// other board.
func dispatchCounts(b *session.DispatchBoardRow) string {
	if b == nil || b.Mode != session.DispatchAuto {
		return ""
	}
	out := strconv.Itoa(b.Running) + " running"
	if b.Waiting > 0 {
		out += ", " + strconv.Itoa(b.Waiting) + " waiting for a free machine"
	}
	return out
}

// dispatchHeld is why dispatch gives no card on the shown board, "" when
// nothing holds it.
func dispatchHeld(st *session.DispatchStatus) string {
	switch {
	case st == nil:
		return ""
	case st.Paused:
		return "Paused"
	case st.Stopped != "":
		return "Stopped: " + st.Stopped
	case st.Board != nil && st.Board.Stopped != "":
		return "Stopped: " + st.Board.Stopped
	case st.QuietNow:
		return "Quiet hours"
	}
	return ""
}

// setDispatch sends one set-task-dispatch change.
func (m *OS) setDispatch(params map[string]any, nonce string) tea.Cmd {
	ck := &m.Inbox.ck
	ck.dispatchBusy = true
	params["human_nonce"] = nonce
	call := m.inboxCaller()
	project := ""
	if ck.dispatch != nil && ck.dispatch.Board != nil {
		project = ck.dispatch.Board.Project
	}
	return func() tea.Msg {
		raw, err := call("set-task-dispatch", params, 5*time.Second)
		msg := ckDispatchMsg{project: project, err: err, set: true}
		if err == nil {
			msg.err = jsonx.Unmarshal(raw, &msg.st)
		}
		return msg
	}
}

// dispatchReady reports whether the board shows a dispatch the person may
// change now.
func (m *OS) dispatchReady() bool {
	ck := &m.Inbox.ck
	if !ck.board || ck.dispatchBusy {
		return false
	}
	if ck.dispatch == nil || ck.dispatch.Board == nil {
		m.ShowNotification("This tuios cannot dispatch task cards, or the board is not read yet.", "info", m.Settings.NotificationDuration)
		return false
	}
	return true
}

// CockpitDispatchMode is ctrl+t on the board: the shown board from manual to
// pull, to auto, and back to manual. Auto starts the board's preset agent for
// each open card; a board with no preset yet takes the New agent sheet's
// saved preset (ctrl+s there), else Claude Code on this machine, with the
// agent's own permission mode.
func (m *OS) CockpitDispatchMode() tea.Cmd {
	if !m.dispatchReady() {
		return nil
	}
	nonce, ok := m.tmlNonce()
	if !ok {
		return nil
	}
	b := m.Inbox.ck.dispatch.Board
	mode := session.DispatchPull
	switch b.Mode {
	case session.DispatchPull:
		mode = session.DispatchAuto
	case session.DispatchAuto:
		mode = session.DispatchManual
	}
	params := map[string]any{"action": "mode", "mode": mode}
	if mode == session.DispatchAuto && b.Preset == nil {
		params["agent"] = "claude"
		if pr := m.Inbox.ck.spawnPreset; pr != nil {
			params["agent"], params["model"], params["effort"], params["permission_mode"], params["machine"] = cmp.Or(pr.Agent, "claude"), pr.Model, pr.Effort, pr.Permission, pr.Host
		}
	}
	if b.Project != "" {
		params["project"] = b.Project
	} else {
		params["session"] = b.Session
	}
	return m.setDispatch(params, nonce)
}

// CockpitDispatchPause is ctrl+x on the board, and the palette's row: pause
// all dispatch, or start it again when a pause or a stop holds it.
func (m *OS) CockpitDispatchPause() tea.Cmd {
	ck := &m.Inbox.ck
	if ck.dispatchBusy {
		return nil
	}
	resume := dispatchHeld(ck.dispatch) != "" && dispatchHeld(ck.dispatch) != "Quiet hours"
	var nonce string
	var ok bool
	if resume {
		nonce, ok = m.tmlNonce()
	} else {
		nonce, ok = m.signedNonce()
	}
	if !ok {
		return nil
	}
	action := "pause"
	if resume {
		action = "resume"
	}
	return m.setDispatch(map[string]any{"action": action}, nonce)
}

// DispatchSet is the palette's pause and resume rows. They act from
// anywhere, so they do not need the board open.
func (m *OS) DispatchSet(action string) tea.Cmd {
	if m.SessionName == "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		m.ShowNotification("Task dispatch needs a daemon session.", "info", m.Settings.NotificationDuration)
		return nil
	}
	nonce, ok := m.signedNonce()
	if action == "resume" {
		nonce, ok = m.tmlNonce()
	}
	if !ok {
		return nil
	}
	cmd := m.setDispatch(map[string]any{"action": action}, nonce)
	word := map[string]string{"pause": "paused", "resume": "on again"}[action]
	m.ShowNotification("Task dispatch is "+word+".", "info", m.Settings.NotificationDuration)
	return cmd
}
