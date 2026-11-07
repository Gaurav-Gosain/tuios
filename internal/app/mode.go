package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// The client's input mode has two kinds of change, and this file holds the
// only code that writes OS.Mode for either of them.
//
// A choice: the person, or an action they took, picks a mode. i, enter, esc,
// a leader mode key, a click into a pane, a tape's mode command, the last pane
// closing. A choice stands until the next one, and the tape recorder hears it.
//
// A borrow: something takes a mode for as long as it lasts and owes the mode
// back. The hold key, a pointer gesture, a ctrl drag, the rail. A borrow is
// not something a replay can do, so the recorder does not hear it.
//
// A borrow that is a lease (borrowMode, returnMode) does not save the mode it
// sees, which can be a mode another borrow took. The first lease to open saves
// the choice beneath every borrow, and a choice made while leases are open
// replaces it and spends them: the choice stands when they end. The mode in
// effect is the newest unspent lease's, else the choice. The borrowers that
// are not leases yet write through SetBorrowedMode.
//
// Before this file, 28 sites assigned Mode directly. Some told the recorder
// and some did not, and only the two switch methods ended nest mode. The
// source check in internal/lint (TestOnlyModeGoWritesTheClientMode) keeps
// every write here.

// ModeCause says why a choice was made. The debug log names it.
type ModeCause uint8

const (
	// ModeCauseKey is a key the person pressed: i, enter, esc, a leader key.
	ModeCauseKey ModeCause = iota
	// ModeCauseClick is a click: into a pane, onto a minimized pane.
	ModeCauseClick
	// ModeCauseTape is a tape's TerminalMode or WindowManagementMode command.
	ModeCauseTape
	// ModeCauseRecording is the start of a tape recording.
	ModeCauseRecording
	// ModeCauseRestore is a session restored with a pane in focus.
	ModeCauseRestore
	// ModeCauseNoPane is terminal mode with no pane left to type into.
	ModeCauseNoPane
	// ModeCauseAction is an action that has a mode of its own, such as a rename.
	ModeCauseAction
)

var modeCauseNames = [...]string{"key", "click", "tape", "recording", "restore", "no pane", "action"}

// modeLogNames are the names the debug log gives a choice.
func modeLogNames(from, to Mode, cause ModeCause) (string, string, string) {
	names := [...]string{"window", "terminal"}
	why := "unknown"
	if int(cause) < len(modeCauseNames) {
		why = modeCauseNames[cause]
	}
	return names[from&1], names[to&1], why
}

// modeOwner names a borrower that holds a lease on the mode.
type modeOwner uint8

const (
	// leaseHold is the hold key, held down.
	leaseHold modeOwner = iota + 1
	// leaseGesture is a pointer gesture: a resize, a divider drag, an alt-drag.
	leaseGesture
	// leaseCtrlDrag is a committed ctrl-drag of a pane.
	leaseCtrlDrag
)

// modeLease is one borrow open now.
type modeLease struct {
	owner modeOwner
	want  Mode
	// spent is set by a choice made while the lease was open. A spent lease
	// stays open until its owner ends it, but no longer sets the mode.
	spent bool
}

// modeState is the choice beneath the leases open now. With no lease open
// the choice is OS.Mode itself, so base is read only while leases is not
// empty. A test that stages a mode by setting OS.Mode stays valid that way.
type modeState struct {
	base   Mode
	leases []modeLease
}

// chosenMode is the mode the person chose, beneath every borrow open now.
func (m *OS) chosenMode() Mode {
	if len(m.mode.leases) > 0 {
		return m.mode.base
	}
	return m.Mode
}

// borrowMode opens owner's lease on want. A second lease of the same owner
// replaces the first.
func (m *OS) borrowMode(owner modeOwner, want Mode) {
	if len(m.mode.leases) == 0 {
		m.mode.base = m.chosenMode()
	}
	m.dropLease(owner)
	m.mode.leases = append(m.mode.leases, modeLease{owner: owner, want: want})
	m.settleMode()
}

// returnMode ends owner's lease. Idempotent: every path a borrow can end by
// runs it. When the last lease ends on terminal mode with no pane in focus,
// that is a choice of window management, the mode that can go find a pane:
// the pane the borrow began over closed while it was open.
func (m *OS) returnMode(owner modeOwner) {
	if !m.dropLease(owner) {
		return
	}
	m.settleMode()
	if len(m.mode.leases) == 0 && m.Mode == TerminalMode && m.GetFocusedWindow() == nil {
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	}
}

// dropLease removes owner's lease and reports whether it had one.
func (m *OS) dropLease(owner modeOwner) bool {
	for i, lease := range m.mode.leases {
		if lease.owner == owner {
			m.mode.leases = append(m.mode.leases[:i], m.mode.leases[i+1:]...)
			return true
		}
	}
	return false
}

// settleMode sets OS.Mode to the mode in effect: the newest unspent lease's,
// else the choice.
func (m *OS) settleMode() {
	for i := len(m.mode.leases) - 1; i >= 0; i-- {
		if lease := m.mode.leases[i]; !lease.spent {
			m.Mode = lease.want
			return
		}
	}
	m.Mode = m.mode.base
}

// ChooseMode makes a choice of mode. The tape recorder hears it when it
// changes the mode, so a replay ends where the recording did. A choice of
// window management ends nest mode, which is valid only in terminal mode.
func (m *OS) ChooseMode(to Mode, cause ModeCause) {
	if from := m.chosenMode(); to != from {
		if verboseLog() {
			fromName, toName, why := modeLogNames(from, to, cause)
			m.LogInfo("mode: %s to %s (%s)", fromName, toName, why)
		}
		if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
			m.TapeRecorder.RecordModeSwitch(modeCommand(to))
		}
	}
	m.Mode = to
	if len(m.mode.leases) > 0 {
		m.mode.base = to
		for i := range m.mode.leases {
			m.mode.leases[i].spent = true
		}
	}
	if to == WindowManagementMode && m.nest {
		m.nest = false
		m.MarkAllDirty()
	}
}

// SetBorrowedMode is the write a borrower that is not a lease makes when it
// takes a mode and when it hands one back: the rail, and a press that grabs a
// pane's title bar or focuses it. It is not a choice: the recorder does not
// hear it, and nest mode waits for the borrow to end (see settleNest). While a
// lease is open the lease decides the mode, and its end hands back the choice
// beneath both.
func (m *OS) SetBorrowedMode(to Mode) {
	if len(m.mode.leases) > 0 {
		m.settleMode()
		return
	}
	m.Mode = to
}

// modeCommand is the tape command that makes a choice of mode.
func modeCommand(mode Mode) tape.CommandType {
	if mode == TerminalMode {
		return tape.CommandTypeTerminalMode
	}
	return tape.CommandTypeWindowManagementMode
}

// EnterTerminalMode is the choice of terminal mode by a key or an action.
// It returns no command; the result is kept for the callers that chain one.
func (m *OS) EnterTerminalMode() tea.Cmd {
	m.ChooseMode(TerminalMode, ModeCauseKey)
	return nil
}

// ExitTerminalMode is the choice of window management by a key or an action.
func (m *OS) ExitTerminalMode() tea.Cmd {
	m.ChooseMode(WindowManagementMode, ModeCauseKey)
	return nil
}
