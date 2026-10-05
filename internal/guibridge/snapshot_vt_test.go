package guibridge

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The snapshot a GUI pane is primed from has to rebuild the same screen in a
// fresh emulator: same cells, styles, cursor, modes and history. Anything it
// drops shows up as a pane that differs from the TUI's copy until the guest
// redraws, which for an idle shell is never.
func TestSnapshotVTRebuildsTheScreen(t *testing.T) {
	cases := map[string]string{
		"plain":  "hello\r\nworld",
		"styles": "\x1b[1;31mred\x1b[0m \x1b[4:3;38;2;10;20;30mcurly\x1b[0m \x1b[7mrev \x1b[0m\x1b[48;5;202m bg \x1b[0m",
		"wide":   "漢字 and 👍 end",
		"history": func() string {
			s := ""
			for i := range 40 {
				s += fmt.Sprintf("line %d\r\n", i)
			}
			return s + "$ "
		}(),
		"wrap":       "0123456789012345678901234567890123456789abcdef",
		"modes":      "\x1b[?1h\x1b[?2004h\x1b[?1000h\x1b[?1006h\x1b[?25l\x1b[>1u\x1b[5 qtext",
		"alt-screen": "shell prompt\r\n\x1b[?1049h\x1b[2;3Hin vim\x1b[5;1H~",
		"margins":    "\x1b[2;8rinside\x1b[5;5H",
		"pen":        "abc\x1b[1;32m",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			a := vt.New(40, 10)
			_, _ = a.Write([]byte(input))
			want := session.TerminalStateOf(a, 40, 10, 0, 0)

			b := vt.New(40, 10)
			_, _ = b.Write(SnapshotVT(want))
			got := session.TerminalStateOf(b, 40, 10, 0, 0)

			if !reflect.DeepEqual(want.Screen, got.Screen) {
				t.Errorf("screen differs\nwant %v\n got %v", want.Screen, got.Screen)
			}
			if !reflect.DeepEqual(want.Scrollback, got.Scrollback) {
				t.Errorf("scrollback differs: want %d rows, got %d", len(want.Scrollback), len(got.Scrollback))
			}
			if !reflect.DeepEqual(want.MainScreen, got.MainScreen) {
				t.Errorf("main screen under the alternate one differs")
			}
			if want.CursorX != got.CursorX || want.CursorY != got.CursorY {
				t.Errorf("cursor: want %d,%d got %d,%d", want.CursorX, want.CursorY, got.CursorX, got.CursorY)
			}
			if want.IsAltScreen != got.IsAltScreen || want.CursorShape != got.CursorShape {
				t.Errorf("alt %v/%v shape %d/%d", want.IsAltScreen, got.IsAltScreen, want.CursorShape, got.CursorShape)
			}
			if !reflect.DeepEqual(want.Modes, got.Modes) {
				t.Errorf("modes: want %v got %v", want.Modes, got.Modes)
			}
			if !reflect.DeepEqual(want.KittyKbdStack, got.KittyKbdStack) {
				t.Errorf("kitty stack: want %v got %v", want.KittyKbdStack, got.KittyKbdStack)
			}
			if !reflect.DeepEqual(want.Margins, got.Margins) {
				t.Errorf("margins: want %v got %v", want.Margins, got.Margins)
			}
			if !reflect.DeepEqual(want.Pen, got.Pen) {
				t.Errorf("pen: want %+v got %+v", want.Pen, got.Pen)
			}
		})
	}
}
