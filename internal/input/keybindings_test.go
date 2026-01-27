package input

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestGetModParam(t *testing.T) {
	tests := []struct {
		name     string
		mod      tea.KeyMod
		expected int
	}{
		{"no modifier", 0, 1},
		{"shift only", tea.ModShift, 2},
		{"alt only", tea.ModAlt, 3},
		{"ctrl only", tea.ModCtrl, 5},
		{"shift+alt", tea.ModShift | tea.ModAlt, 4},
		{"shift+ctrl", tea.ModShift | tea.ModCtrl, 6},
		{"alt+ctrl", tea.ModAlt | tea.ModCtrl, 7},
		{"shift+alt+ctrl", tea.ModShift | tea.ModAlt | tea.ModCtrl, 8},
		// Ensure high bits are masked off
		{"with extra bits", tea.ModShift | tea.KeyMod(128), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getModParam(tt.mod)
			if result != tt.expected {
				t.Errorf("getModParam(%v) = %d, want %d", tt.mod, result, tt.expected)
			}
		})
	}
}

func TestGetCursorSequence(t *testing.T) {
	tests := []struct {
		name     string
		code     rune
		expected []byte
	}{
		{"up arrow", tea.KeyUp, []byte{0x1b, '[', 'A'}},
		{"down arrow", tea.KeyDown, []byte{0x1b, '[', 'B'}},
		{"right arrow", tea.KeyRight, []byte{0x1b, '[', 'C'}},
		{"left arrow", tea.KeyLeft, []byte{0x1b, '[', 'D'}},
		{"home", tea.KeyHome, []byte{0x1b, '[', 'H'}},
		{"end", tea.KeyEnd, []byte{0x1b, '[', 'F'}},
		{"unknown key", 'x', nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCursorSequence(tt.code)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("getCursorSequence(%v) = %v, want %v", tt.code, result, tt.expected)
			}
		})
	}
}

func TestBuildCSISequence(t *testing.T) {
	tests := []struct {
		name     string
		num      int
		modParam int
		expected []byte
	}{
		{"single digit no mod", 5, 1, []byte{0x1b, '[', '5', '~'}},
		{"double digit no mod", 15, 1, []byte{0x1b, '[', '1', '5', '~'}},
		{"single digit with mod", 5, 2, []byte{0x1b, '[', '5', ';', '2', '~'}},
		{"double digit with mod", 15, 3, []byte{0x1b, '[', '1', '5', ';', '3', '~'}},
		{"F9 no mod", 20, 1, []byte{0x1b, '[', '2', '0', '~'}},
		{"F9 with shift", 20, 2, []byte{0x1b, '[', '2', '0', ';', '2', '~'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCSISequence(tt.num, tt.modParam)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("buildCSISequence(%d, %d) = %v, want %v", tt.num, tt.modParam, result, tt.expected)
			}
		})
	}
}

func TestGetFunctionKeySequence(t *testing.T) {
	tests := []struct {
		name     string
		code     rune
		modParam int
		expected []byte
	}{
		{"F1 no mod", tea.KeyF1, 1, []byte{0x1b, 'O', 'P'}},
		{"F2 no mod", tea.KeyF2, 1, []byte{0x1b, 'O', 'Q'}},
		{"F3 no mod", tea.KeyF3, 1, []byte{0x1b, 'O', 'R'}},
		{"F4 no mod", tea.KeyF4, 1, []byte{0x1b, 'O', 'S'}},
		{"F5 no mod", tea.KeyF5, 1, []byte{0x1b, '[', '1', '5', '~'}},
		{"F12 no mod", tea.KeyF12, 1, []byte{0x1b, '[', '2', '4', '~'}},
		{"F1 with shift", tea.KeyF1, 2, []byte{0x1b, '[', '1', ';', '2', 'P'}},
		{"F5 with shift", tea.KeyF5, 2, []byte{0x1b, '[', '1', '5', ';', '2', '~'}},
		{"unknown key", 'x', 1, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFunctionKeySequence(tt.code, tt.modParam)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("getFunctionKeySequence(%v, %d) = %v, want %v", tt.code, tt.modParam, result, tt.expected)
			}
		})
	}
}

func TestIsMacOSOptionKey(t *testing.T) {
	tests := []struct {
		name          string
		r             rune
		expectedDigit int
		expectedOK    bool
	}{
		{"option+1", '¡', 1, true},
		{"option+2", '™', 2, true},
		{"option+3", '£', 3, true},
		{"option+4", '¢', 4, true},
		{"option+5", '∞', 5, true},
		{"option+6", '§', 6, true},
		{"option+7", '¶', 7, true},
		{"option+8", '•', 8, true},
		{"option+9", 'ª', 9, true},
		{"regular digit", '5', 0, false},
		{"letter", 'a', 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			digit, ok := IsMacOSOptionKey(tt.r)
			if digit != tt.expectedDigit || ok != tt.expectedOK {
				t.Errorf("IsMacOSOptionKey(%q) = (%d, %v), want (%d, %v)",
					tt.r, digit, ok, tt.expectedDigit, tt.expectedOK)
			}
		})
	}
}

func TestIsMacOSOptionShiftKey(t *testing.T) {
	tests := []struct {
		name          string
		r             rune
		expectedDigit int
		expectedOK    bool
	}{
		{"option+shift+1", '⁄', 1, true},
		{"option+shift+2", '€', 2, true},
		{"option+shift+3", '‹', 3, true},
		{"option+shift+4", '›', 4, true},
		{"option+shift+5", 'ﬁ', 5, true},
		{"option+shift+6", 'ﬂ', 6, true},
		{"option+shift+7", '‡', 7, true},
		{"option+shift+8", '°', 8, true},
		{"option+shift+9", '·', 9, true},
		{"regular digit", '5', 0, false},
		{"option+5", '∞', 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			digit, ok := IsMacOSOptionShiftKey(tt.r)
			if digit != tt.expectedDigit || ok != tt.expectedOK {
				t.Errorf("IsMacOSOptionShiftKey(%q) = (%d, %v), want (%d, %v)",
					tt.r, digit, ok, tt.expectedDigit, tt.expectedOK)
			}
		})
	}
}

func TestIsMacOSOptionTab(t *testing.T) {
	tests := []struct {
		name     string
		r        rune
		expected string
	}{
		{"option+tab", '⇥', "next"},
		{"option+shift+tab", '⇤', "prev"},
		{"regular tab", '\t', ""},
		{"letter", 'a', ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMacOSOptionTab(tt.r)
			if result != tt.expected {
				t.Errorf("IsMacOSOptionTab(%q) = %q, want %q", tt.r, result, tt.expected)
			}
		})
	}
}
