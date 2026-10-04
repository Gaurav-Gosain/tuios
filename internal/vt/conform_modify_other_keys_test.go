package vt_test

// Conformance for xterm's modifyOtherKeys: XTMODKEYS (CSI > 4 ; Pv m) sets the
// level, CSI > 4 n turns it off, XTQMODKEYS (CSI ? 4 m) reports it as
// CSI > 4 ; Pv m, and EncodeModifyOtherKeys sends a modified key as
// CSI 27 ; modifier ; code ~ where xterm would. The expected encodings are
// xterm's (input.c, ModifyOtherKeys), level 1 and level 2.

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func TestConform_XTMODKEYS(t *testing.T) {
	const q = "\x1b[?4m"
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"off by default", q, "\x1b[>4;0m"},
		{"level 2", "\x1b[>4;2m" + q, "\x1b[>4;2m"},
		{"level 1", "\x1b[>4;1m" + q, "\x1b[>4;1m"},
		{"no value resets it", "\x1b[>4;2m\x1b[>4m" + q, "\x1b[>4;0m"},
		{"CSI > m resets every resource", "\x1b[>4;2m\x1b[>m" + q, "\x1b[>4;0m"},
		{"CSI > 4 n turns it off", "\x1b[>4;2m\x1b[>4n" + q, "\x1b[>4;0m"},
		{"RIS turns it off", "\x1b[>4;2m\x1bc" + q, "\x1b[>4;0m"},
		// It is not part of the screen, so the alternate screen shares it.
		{"the alternate screen keeps it", "\x1b[>4;2m\x1b[?1049h" + q, "\x1b[>4;2m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyChecked(t, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}

	// The other resources change encodings this emulator has no choice of,
	// so setting one is not recognised, and does not touch this one.
	emu := vt.NewEmulator(10, 3)
	log := &unhandledLog{}
	emu.SetLogger(log)
	_, _ = emu.WriteString("\x1b[>4;2m\x1b[>1;2m")
	if len(log.lines) != 1 {
		t.Errorf("modifyCursorKeys (CSI > 1 ; 2 m) logged %v, want it reported unrecognised once", log.lines)
	}
	if got := emu.ModifyOtherKeys(); got != 2 {
		t.Errorf("after another resource was set, ModifyOtherKeys() = %d, want 2", got)
	}
}

func TestConform_ModifyOtherKeysEncoding(t *testing.T) {
	key := func(code rune, mod vt.KeyMod) vt.KeyPressEvent {
		return vt.KeyPressEvent{Code: code, Mod: mod}
	}
	const (
		shift = vt.ModShift
		alt   = vt.ModAlt
		ctrl  = vt.ModCtrl
	)
	for _, tc := range []struct {
		name   string
		key    vt.KeyPressEvent
		level1 string // "" means the ordinary encoding
		level2 string
	}{
		{"an unmodified letter", key('a', 0), "", ""},
		{"a shifted letter is the letter", vt.KeyPressEvent{Code: 'a', Mod: shift, Text: "A"}, "", ""},
		{"Ctrl+A", key('a', ctrl), "", "\x1b[27;5;97~"},
		{"Ctrl+Shift+A", key('a', ctrl|shift), "\x1b[27;6;65~", "\x1b[27;6;65~"},
		{"Ctrl+Alt+A", key('a', ctrl|alt), "\x1b[27;7;97~", "\x1b[27;7;97~"},
		{"Alt+A", key('a', alt), "", "\x1b[27;3;97~"},
		{"Ctrl+1 has no control character", key('1', ctrl), "\x1b[27;5;49~", "\x1b[27;5;49~"},
		{"Ctrl+Shift+2 reports the shifted key", vt.KeyPressEvent{Code: '2', Mod: ctrl | shift, ShiftedCode: '@'},
			"\x1b[27;6;64~", "\x1b[27;6;64~"},
		{"Ctrl+Space", key(vt.KeySpace, ctrl), "", "\x1b[27;5;32~"},
		{"Shift+Space", key(vt.KeySpace, shift), "", "\x1b[27;2;32~"},
		{"Ctrl+Enter", key(vt.KeyEnter, ctrl), "\x1b[27;5;13~", "\x1b[27;5;13~"},
		{"Shift+Enter", key(vt.KeyEnter, shift), "\x1b[27;2;13~", "\x1b[27;2;13~"},
		{"Ctrl+Tab", key(vt.KeyTab, ctrl), "\x1b[27;5;9~", "\x1b[27;5;9~"},
		{"Shift+Tab stays back-tab", key(vt.KeyTab, shift), "", ""},
		{"Ctrl+Shift+Tab", key(vt.KeyTab, ctrl|shift), "\x1b[27;6;9~", "\x1b[27;6;9~"},
		{"Ctrl+Backspace", key(vt.KeyBackspace, ctrl), "", "\x1b[27;5;127~"},
		{"Alt+Escape", key(vt.KeyEscape, alt), "\x1b[27;3;27~", "\x1b[27;3;27~"},
		{"an arrow keeps its own sequence", key(vt.KeyUp, ctrl), "", ""},
		{"a function key keeps its own sequence", key(vt.KeyF5, shift), "", ""},
		{"a keypad key keeps its own sequence", key(vt.KeyKp5, ctrl), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := vt.EncodeModifyOtherKeys(tc.key, 0); got != "" {
				t.Errorf("level 0 = %q, want the ordinary encoding", got)
			}
			if got := vt.EncodeModifyOtherKeys(tc.key, 1); got != tc.level1 {
				t.Errorf("level 1 = %q, want %q", got, tc.level1)
			}
			if got := vt.EncodeModifyOtherKeys(tc.key, 2); got != tc.level2 {
				t.Errorf("level 2 = %q, want %q", got, tc.level2)
			}
		})
	}
}
