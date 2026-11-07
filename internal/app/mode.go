package app

import (
	"slices"

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
// effect is the newest unspent lease's, else the choice. The rail takes the
// keyboard and no mode: its lease saves the choice and hands it back when it
// ends, so esc undoes a browse. The borrowers that are not leases write
// through SetBorrowedMode.
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
	// ModeCauseRail is the rail handing back the choice it saved.
	ModeCauseRail
)

var modeCauseNames = [...]string{"key", "click", "tape", "recording", "restore", "no pane", "action", "rail"}

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
	// leaseRail is the rail holding the keyboard.
	leaseRail
)

// modeAny is the want of a lease that takes no mode: the rail's.
const modeAny Mode = -1

// modeLease is one borrow open now.
type modeLease struct {
	owner modeOwner
	want  Mode
	// spent is set by a choice made while the lease was open. A spent lease
	// stays open until its owner ends it, but no longer sets the mode.
	spent bool
	// owes is set on a lease that hands back saved when it ends, whatever was
	// chosen while it was open. Only cancelLease ends it without that.
	owes  bool
	saved Mode
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
	m.openLease(modeLease{owner: owner, want: want})
}

// borrowKeyboard opens owner's lease on the keyboard alone. It takes no mode,
// and hands back the choice beneath it when it ends.
func (m *OS) borrowKeyboard(owner modeOwner) {
	m.openLease(modeLease{owner: owner, want: modeAny, owes: true, saved: m.chosenMode()})
}

func (m *OS) openLease(lease modeLease) {
	if len(m.mode.leases) == 0 {
		m.mode.base = m.chosenMode()
	}
	if old, had := m.takeLease(lease.owner); had && old.owes {
		lease.saved = old.saved
	}
	m.mode.leases = append(m.mode.leases, lease)
	m.settleMode()
}

// returnMode ends owner's lease. Idempotent: every path a borrow can end by
// runs it. A lease that owes its saved choice hands it back. When the last
// lease ends on terminal mode with no pane in focus, that is a choice of
// window management, the mode that can go find a pane: the pane the borrow
// began over closed while it was open.
func (m *OS) returnMode(owner modeOwner) {
	lease, had := m.takeLease(owner)
	if !had {
		return
	}
	if lease.owes {
		m.setChoice(lease.saved, ModeCauseRail, false)
	}
	m.settleMode()
	if len(m.mode.leases) == 0 && m.Mode == TerminalMode && m.GetFocusedWindow() == nil {
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	}
}

// cancelLease ends owner's lease without handing anything back: what was
// chosen while it was open stands.
func (m *OS) cancelLease(owner modeOwner) {
	if _, had := m.takeLease(owner); had {
		m.settleMode()
	}
}

// modeLeased reports whether owner holds a lease now.
func (m *OS) modeLeased(owner modeOwner) bool {
	for _, lease := range m.mode.leases {
		if lease.owner == owner {
			return true
		}
	}
	return false
}

// takeLease removes owner's lease and returns it.
func (m *OS) takeLease(owner modeOwner) (modeLease, bool) {
	for i, lease := range m.mode.leases {
		if lease.owner == owner {
			m.mode.leases = append(m.mode.leases[:i], m.mode.leases[i+1:]...)
			return lease, true
		}
	}
	return modeLease{}, false
}

// settleMode sets OS.Mode to the mode in effect: the newest unspent lease's,
// else the choice.
func (m *OS) settleMode() {
	for _, lease := range slices.Backward(m.mode.leases) {
		if !lease.spent && lease.want != modeAny {
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
	m.setChoice(to, cause, true)
}

// setChoice makes to the choice. A choice spends the open leases, so it
// stands when they end. A lease handing back its saved choice does not spend
// the others: a hold still held keeps window management in effect.
func (m *OS) setChoice(to Mode, cause ModeCause, spend bool) {
	if from := m.chosenMode(); to != from {
		if verboseLog() {
			fromName, toName, why := modeLogNames(from, to, cause)
			m.LogInfo("mode: %s to %s (%s)", fromName, toName, why)
		}
		if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
			m.TapeRecorder.RecordModeSwitch(modeCommand(to))
		}
	}
	m.mode.base = to
	if spend {
		for i := range m.mode.leases {
			m.mode.leases[i].spent = true
		}
	}
	m.settleMode()
	if to == WindowManagementMode && m.nest {
		m.nest = false
		m.MarkAllDirty()
	}
}

// SetBorrowedMode is the write a borrower that is not a lease makes when it
// takes a mode: a press that grabs a pane's title bar or focuses it. It is not a choice: the recorder does not
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
