package tape

import (
	"bytes"
	"testing"
)

func TestConvertKeyComboShift(t *testing.T) {
	tests := []struct {
		name  string
		combo string
		want  []byte
	}{
		{"Shift+Tab back-tab", "Shift+Tab", []byte{0x1b, '[', 'Z'}},
		{"Shift+letter uppercases", "Shift+a", []byte{'A'}},
		{"Shift+letter uppercases z", "Shift+z", []byte{'Z'}},
		{"Ctrl still wins", "Ctrl+a", []byte{0x01}},
		{"Alt still prefixes ESC", "Alt+a", []byte{0x1b, 'a'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertKeyComboToBytes(tt.combo)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("convertKeyComboToBytes(%q) = %v, want %v", tt.combo, got, tt.want)
			}
		})
	}
}

func TestRepeatCount(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, 1},
		{"positive count", []string{"5"}, 5},
		{"non-numeric", []string{"abc"}, 1},
		{"zero", []string{"0"}, 1},
		{"negative", []string{"-3"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repeatCount(&Command{Args: tt.args})
			if got != tt.want {
				t.Errorf("repeatCount(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}
