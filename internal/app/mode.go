package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

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

func modeLogNames(from, to Mode, cause ModeCause) (string, string, string) {
	names := [...]string{"window", "terminal"}
	why := "unknown"
	if int(cause) < len(modeCauseNames) {
		why = modeCauseNames[cause]
	}
	return names[from&1], names[to&1], why
}

type modeOwner uint8

const (
	leaseHold modeOwner = iota + 1
	leaseGesture
	leaseCtrlDrag
	leaseRail
)

const modeAny Mode = -1

type modeLease struct {
	owner modeOwner
	want  Mode
	spent bool
	owes  bool
	saved Mode
}

type modeState struct {
	base   Mode
	leases []modeLease
}

func (m *OS) chosenMode() Mode {
	if len(m.mode.leases) > 0 {
		return m.mode.base
	}
	return m.Mode
}

func (m *OS) borrowMode(owner modeOwner, want Mode) {
	m.openLease(modeLease{owner: owner, want: want})
}

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

func (m *OS) returnMode(owner modeOwner) {
	lease, had := m.takeLease(owner)
	if !had {
		return
	}
	if lease.owes && !lease.spent {
		m.setChoice(lease.saved, ModeCauseRail, false)
	}
	m.settleMode()
	if len(m.mode.leases) == 0 && m.Mode == TerminalMode && m.GetFocusedWindow() == nil {
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	}
}

func (m *OS) cancelLease(owner modeOwner) {
	if _, had := m.takeLease(owner); had {
		m.settleMode()
	}
}

func (m *OS) modeLeased(owner modeOwner) bool {
	for _, lease := range m.mode.leases {
		if lease.owner == owner {
			return true
		}
	}
	return false
}

func (m *OS) takeLease(owner modeOwner) (modeLease, bool) {
	for i, lease := range m.mode.leases {
		if lease.owner == owner {
			m.mode.leases = append(m.mode.leases[:i], m.mode.leases[i+1:]...)
			return lease, true
		}
	}
	return modeLease{}, false
}

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
//
// With window mode off and a pane in focus, a choice of window management is
// a choice of terminal mode. See window_mode_off.go.
func (m *OS) ChooseMode(to Mode, cause ModeCause) {
	if to == WindowManagementMode && m.windowModeRefused() {
		to = TerminalMode
	}
	m.setChoice(to, cause, true)
}

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
