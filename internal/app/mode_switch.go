package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// EnterTerminalMode switches from window management to terminal mode.
// In terminal mode, raw input bypasses Bubbletea and goes directly to the PTY.
func (m *OS) EnterTerminalMode() tea.Cmd {
	// Record mode switch for tape recording
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		m.TapeRecorder.RecordModeSwitch(tape.CommandTypeTerminalMode)
	}

	m.Mode = TerminalMode

	// Raw reader disabled - Bubbletea handles all input correctly including:
	// - Bracketed paste for Cmd+V (via PasteMsg)
	// - OSC 52 clipboard reading for Ctrl+V (via ClipboardMsg)
	// - All key events properly parsed
	// Raw reader conflicts with Bubbletea in modern terminals using CSI u encoding
	return nil
}

// ExitTerminalMode switches from terminal to window management mode.
// In window management mode, Bubbletea handles input parsing.
func (m *OS) ExitTerminalMode() tea.Cmd {
	// Record mode switch for tape recording
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		m.TapeRecorder.RecordModeSwitch(tape.CommandTypeWindowManagementMode)
	}

	m.Mode = WindowManagementMode

	// Raw reader disabled - Bubbletea handles all input correctly
	return nil
}
