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

// ChooseMode makes a choice of mode. The tape recorder hears it when it
// changes the mode, so a replay ends where the recording did. A choice of
// window management ends nest mode, which is valid only in terminal mode.
func (m *OS) ChooseMode(to Mode, cause ModeCause) {
	if to != m.Mode {
		if verboseLog() {
			from, into, why := modeLogNames(m.Mode, to, cause)
			m.LogInfo("mode: %s to %s (%s)", from, into, why)
		}
		if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
			m.TapeRecorder.RecordModeSwitch(modeCommand(to))
		}
	}
	m.Mode = to
	if to == WindowManagementMode && m.nest {
		m.nest = false
		m.MarkAllDirty()
	}
}

// SetBorrowedMode is the write a borrower makes when it takes a mode and when
// it hands one back. It is not a choice: the recorder does not hear it, and
// nest mode waits for the borrow to end (see settleNest).
func (m *OS) SetBorrowedMode(to Mode) {
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
