package app

import tea "github.com/charmbracelet/bubbletea/v2"

// SwitchToRawInputMsg signals that the application should switch to raw input mode.
// This is sent when entering terminal mode where input bypasses Bubbletea.
type SwitchToRawInputMsg struct{}

// SwitchToBubbletteaInputMsg signals that the application should switch back to Bubbletea input mode.
// This is sent when exiting terminal mode and returning to window management mode.
type SwitchToBubbletteaInputMsg struct{}

// RawInputMsg contains raw bytes read from /dev/tty.
// Empty bytes slice signals Ctrl+B Esc (exit terminal mode).
type RawInputMsg struct {
	Bytes []byte
}

// SwitchToRawInputCmd returns a command that sends SwitchToRawInputMsg.
func SwitchToRawInputCmd() tea.Cmd {
	return func() tea.Msg {
		return SwitchToRawInputMsg{}
	}
}

// SwitchToBubbletteaInputCmd returns a command that sends SwitchToBubbletteaInputMsg.
func SwitchToBubbletteaInputCmd() tea.Cmd {
	return func() tea.Msg {
		return SwitchToBubbletteaInputMsg{}
	}
}

// WaitForRawInputCmd creates a command that waits for raw input from a channel.
// This is used to continuously read raw bytes and send them as messages.
func WaitForRawInputCmd(ch <-chan []byte) tea.Cmd {
	return func() tea.Msg {
		bytes := <-ch
		return RawInputMsg{Bytes: bytes}
	}
}
