package app

import tea "github.com/charmbracelet/bubbletea/v2"

// EnterTerminalMode switches from window management to terminal mode.
// In terminal mode, raw input bypasses Bubbletea and goes directly to the PTY.
func (m *OS) EnterTerminalMode() tea.Cmd {
	m.Mode = TerminalMode

	// Signal to main program to start raw input reader
	return SwitchToRawInputCmd()
}

// ExitTerminalMode switches from terminal to window management mode.
// In window management mode, Bubbletea handles input parsing.
func (m *OS) ExitTerminalMode() tea.Cmd {
	m.Mode = WindowManagementMode

	// Signal to main program to stop raw input reader
	return SwitchToBubbletteaInputCmd()
}
