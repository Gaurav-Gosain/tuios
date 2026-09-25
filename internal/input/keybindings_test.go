package input

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestLegacyKeyEncoding pins the xterm-style bytes the legacy encoder sends a
// pane: the modifier parameter, cursor keys, CSI tilde keys and function keys.
func TestLegacyKeyEncoding(t *testing.T) {
	modParam := func(mod tea.KeyMod) []byte { return []byte{byte('0' + getModParam(mod))} }
	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"mod none", modParam(0), []byte("1")},
		{"mod shift", modParam(tea.ModShift), []byte("2")},
		{"mod alt", modParam(tea.ModAlt), []byte("3")},
		{"mod shift+alt", modParam(tea.ModShift | tea.ModAlt), []byte("4")},
		{"mod ctrl", modParam(tea.ModCtrl), []byte("5")},
		{"mod shift+ctrl", modParam(tea.ModShift | tea.ModCtrl), []byte("6")},
		{"mod alt+ctrl", modParam(tea.ModAlt | tea.ModCtrl), []byte("7")},
		{"mod shift+alt+ctrl", modParam(tea.ModShift | tea.ModAlt | tea.ModCtrl), []byte("8")},
		// High bits are masked off.
		{"mod with extra bits", modParam(tea.ModShift | tea.KeyMod(128)), []byte("2")},

		{"up", getCursorSequence(tea.KeyUp), []byte("\x1b[A")},
		{"down", getCursorSequence(tea.KeyDown), []byte("\x1b[B")},
		{"right", getCursorSequence(tea.KeyRight), []byte("\x1b[C")},
		{"left", getCursorSequence(tea.KeyLeft), []byte("\x1b[D")},
		{"home", getCursorSequence(tea.KeyHome), []byte("\x1b[H")},
		{"end", getCursorSequence(tea.KeyEnd), []byte("\x1b[F")},
		{"cursor unknown", getCursorSequence('x'), nil},

		{"csi single digit", buildCSISequence(5, 1), []byte("\x1b[5~")},
		{"csi double digit", buildCSISequence(15, 1), []byte("\x1b[15~")},
		{"csi single digit with mod", buildCSISequence(5, 2), []byte("\x1b[5;2~")},
		{"csi double digit with mod", buildCSISequence(15, 3), []byte("\x1b[15;3~")},
		{"csi F9", buildCSISequence(20, 1), []byte("\x1b[20~")},
		{"csi shift+F9", buildCSISequence(20, 2), []byte("\x1b[20;2~")},

		{"F1", getFunctionKeySequence(tea.KeyF1, 1), []byte("\x1bOP")},
		{"F2", getFunctionKeySequence(tea.KeyF2, 1), []byte("\x1bOQ")},
		{"F3", getFunctionKeySequence(tea.KeyF3, 1), []byte("\x1bOR")},
		{"F4", getFunctionKeySequence(tea.KeyF4, 1), []byte("\x1bOS")},
		{"F5", getFunctionKeySequence(tea.KeyF5, 1), []byte("\x1b[15~")},
		{"F12", getFunctionKeySequence(tea.KeyF12, 1), []byte("\x1b[24~")},
		{"shift+F1", getFunctionKeySequence(tea.KeyF1, 2), []byte("\x1b[1;2P")},
		{"shift+F5", getFunctionKeySequence(tea.KeyF5, 2), []byte("\x1b[15;2~")},
		{"function unknown", getFunctionKeySequence('x', 1), nil},
	}
	for _, tt := range tests {
		if !bytes.Equal(tt.got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}
