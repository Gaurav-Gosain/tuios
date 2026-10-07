//go:build ghostty

package vt

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
)

// TestGhosttyDiffProgramStatus feeds the same OSC 7501 input to the pure
// emulator and to libghostty, and wants the same reports and the same
// screen from both. The cases are the control bytes inside the string, where
// the two parsers used to differ: the scanner kept C0 controls in the
// payload, read past CAN and SUB, and dropped a report ended by an ESC that
// did not start ST.
func TestGhosttyDiffProgramStatus(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"plain", "\x1b]7501;state=working:app=cargo\x1b\\after"},
		{"C0 inside", "\x1b]7501;state=wor\nki\x01ng:app=car\tgo\x07after"},
		{"CAN inside", "\x1b]7501;state=working\x18:app=x\x1b\\after"},
		{"SUB inside", "\x1b]7501;state=working\x1aafter\x07"},
		{"ESC [ inside", "\x1b]7501;state=working\x1b[1mbold:app=x\x1b\\after"},
		{"ESC ESC inside", "\x1b]7501;state=done\x1b\x1b\\after"},
		{"DEL inside", "\x1b]7501;state=id\x7fle\x1b\\"},
		{"query with CAN", "\x1b]7501;?\x18rest\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDiffPair(t, 40, 5)
			var pure, gh []progstatus.Event
			p.pure.SetCallbacks(Callbacks{ProgramStatus: func(ev progstatus.Event) { pure = append(pure, ev) }})
			p.gh.SetCallbacks(Callbacks{ProgramStatus: func(ev progstatus.Event) { gh = append(gh, ev) }})
			p.write(t, []byte(tc.in))
			if !slices.Equal(pure, gh) {
				t.Errorf("reports differ: pure %+v, ghostty %+v", pure, gh)
			}
			p.compareScreens(t, tc.name)
		})
	}
}
