package session

import (
	"fmt"
	"maps"
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// SnapshotVT is the one encoder for the SNAP frames of stream-pane and of
// tuios gui-bridge: the bytes that rebuild a pane in a fresh emulator of the
// pane's size. A conformance corpus: each case is input to an emulator, its
// state, the bytes SnapshotVT makes of that state written to a second fresh
// emulator, and that one's state, which must match the first.
//
// Ways it can go wrong, each covered by a case:
//   - cells, styles, wide characters, history and soft wraps differ;
//   - the alternate screen comes back as 1049 when the guest used 1047 or
//     47, so leaving it does not do what the guest expects;
//   - the cursor lands elsewhere under origin mode, which counts rows from
//     the top margin;
//   - a mouse mode set before another is reset and turns reports off in an
//     emulator where the tracking modes share one state;
//   - the tab stops, the title, a pending wrap or a half-parsed sequence are
//     dropped (the gui-bridge's own encoder dropped all four);
//   - the kitty keyboard stack, the margins or the pen are dropped.
func TestSnapshotVTRebuildsThePane(t *testing.T) {
	history := ""
	for i := range 40 {
		history += fmt.Sprintf("line %d\r\n", i)
	}
	cases := map[string]string{
		"plain":       "hello\r\nworld",
		"styles":      "\x1b[1;31mred\x1b[0m \x1b[4:3;38;2;10;20;30mcurly\x1b[0m \x1b[7mrev \x1b[0m\x1b[48;5;202m bg \x1b[0m",
		"wide":        "漢字 and 👍 end",
		"history":     history + "$ ",
		"wrap":        "0123456789012345678901234567890123456789abcdef",
		"modes":       "\x1b[?1h\x1b[?2004h\x1b[?1000h\x1b[?1006h\x1b[?25l\x1b[>1u\x1b[5 qtext",
		"mouse-order": "\x1b[?1002h\x1b[?1002l\x1b[?1000h\x1b[?1006h",
		"alt-1049":    "shell prompt\r\n\x1b[?1049h\x1b[2;3Hin vim\x1b[5;1H~",
		"alt-1047":    "shell prompt\r\n\x1b[?1047h\x1b[2;3Hin less",
		"margins":     "\x1b[2;8rinside\x1b[5;5H",
		"origin":      "\x1b[3;9r\x1b[?6h\x1b[2;4Hhere",
		"pen":         "abc\x1b[1;32m",
		"tabs":        "\x1b[3g\x1b[1;5H\x1bH\x1b[1;17H\x1bH\x1b[2;1H",
		"title":       "\x1b]2;a title\x07$ ",
		"pending":     "\x1b[1;35H123456",
		"half-parsed": "before\x1b[3",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			a := vt.New(40, 10)
			_, _ = a.Write([]byte(input))
			want := TerminalStateOf(a, 40, 10, 0, 0)

			b := vt.New(40, 10)
			_, _ = b.Write(SnapshotVT(want))
			got := TerminalStateOf(b, 40, 10, 0, 0)

			check := func(what string, w, g any) {
				t.Helper()
				if !reflect.DeepEqual(w, g) {
					t.Errorf("%s differs\nwant %v\n got %v", what, w, g)
				}
			}
			check("screen", want.Screen, got.Screen)
			check("scrollback", want.Scrollback, got.Scrollback)
			check("main screen under the alternate one", want.MainScreen, got.MainScreen)
			check("cursor", [2]int{want.CursorX, want.CursorY}, [2]int{got.CursorX, got.CursorY})
			check("alternate screen", want.IsAltScreen, got.IsAltScreen)
			check("cursor shape", want.CursorShape, got.CursorShape)
			check("kitty stack", want.KittyKbdStack, got.KittyKbdStack)
			check("margins", want.Margins, got.Margins)
			check("pen", want.Pen, got.Pen)
			check("tab stops", want.TabStops, got.TabStops)
			check("title", want.Title, got.Title)
			check("pending wrap", want.PendingWrap, got.PendingWrap)
			check("pending input", want.PendingInput, got.PendingInput)
			// The modes SnapshotVT writes, and the screen switch.
			for _, m := range append([]int{6, 47, 1047, 1049}, snapshotModes...) {
				if want.Modes[m] != got.Modes[m] {
					t.Errorf("mode %d: want %v got %v (want %v)", m, want.Modes[m], got.Modes[m], maps.Clone(want.Modes))
				}
			}
		})
	}
}
