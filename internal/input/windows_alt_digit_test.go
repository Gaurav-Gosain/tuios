package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The reported bug: on Windows Terminal, Alt+2 switched workspace in terminal
// mode and did nothing in window mode. tuios asks the host for every key as an
// escape code with its text while it reads keys itself, and Windows Terminal
// then sends Alt+2 as CSI 50;3;50u: the text "2" rides along. Bubble Tea spells
// that key "2", which window mode binds to select_window_2.
//
// The e2e suite cannot run Windows Terminal, so this holds the input boundary.
// Each row is what one Windows path decodes to for Alt+2, and every one has to
// switch workspace in both modes.
func TestWindowsAltDigitSwitchesWorkspaceInBothModes(t *testing.T) {
	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
	}{
		{"win32 input record", tea.KeyPressMsg{Code: '2', BaseCode: '2', Mod: tea.ModAlt}},
		{"esc-prefixed", tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt}},
		{"kitty", tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt}},
		{"kitty with text", tea.KeyPressMsg{Code: '2', Text: "2", Mod: tea.ModAlt}},
		{"kitty with text and num lock", tea.KeyPressMsg{Code: '2', Text: "2", Mod: tea.ModAlt | tea.ModNumLock}},
	} {
		for name, mode := range map[string]app.Mode{"terminal": app.TerminalMode, "window": app.WindowManagementMode} {
			o := osWithBindings(t, func(*config.KeybindingsConfig) {})
			o.Windows = append(o.Windows, &terminal.Window{ID: "w1", Workspace: o.CurrentWorkspace})
			o.FocusedWindow = 0
			o.Mode = mode
			if got, _ := HandleKeyPress(tc.msg, o); got.CurrentWorkspace != 2 {
				t.Errorf("%s in %s mode: workspace is %d, want 2", tc.what, name, got.CurrentWorkspace)
			}
		}
	}
}
