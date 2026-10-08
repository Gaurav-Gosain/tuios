package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A snapshot taken the moment a guest has filled a row to its last column.
//
// The cursor then sits on that last cell with a wrap pending: the next printed
// character goes to the start of the row below, not over the cell the cursor is
// on. The pending wrap is not a cell and not a cursor position, so a snapshot
// that carries only the position restores a cursor that overwrites the last
// cell instead, and every byte the stream delivers after it lands one column
// out. TestRehydrationMatrix/reattach/mid-output found it: a reattach that
// caught the shell echoing a command longer than the pane came back with the
// last character of the wrapped row replaced by the first one of the next.
//
// Ways this can go wrong, each a case below:
//   - the flag is lost, so the next character overwrites the last column;
//   - the flag is invented for a cursor a guest moved to the last column
//     itself, so the next character wraps where the guest asked it not to;
//   - the flag is restored but a cursor move or a carriage return after it
//     does not clear it;
//   - the restore arms it by printing the last cell again, and that print
//     comes out wrong: a wide glyph split, a glyph translated a second time by
//     the line-drawing set, the pen of the cell left in force, or the row
//     addressed outside the scroll region under origin mode, top or left;
//   - the flag survives the wire in one form of the snapshot and not the other.
var pendingWrapCases = []struct {
	name   string
	before string // output before the snapshot
	after  string // output after it, arriving on the stream
}{
	{"pending", "$ " + strings.Repeat("x", fidelityCols-3) + "Z", "NEXT\r\n"},
	{"parked-by-cup", strings.Repeat("x", fidelityCols) + "\r\n\x1b[2;40H", "Q\r\n"},
	{"pending-then-moved", strings.Repeat("x", fidelityCols), "\x1b[1;1HHOME"},
	{"pending-then-cr", strings.Repeat("x", fidelityCols), "\rCR"},
	{"pending-no-autowrap", "\x1b[?7l" + strings.Repeat("x", fidelityCols), "NOWRAP\r\n"},
	{"pending-wide", strings.Repeat("x", fidelityCols-2) + "日", "NEXT"},
	{"pending-line-drawing", "\x1b(0" + strings.Repeat("q", fidelityCols), "xx\x1b(Bok"},
	{"pending-under-pen", "\x1b[1;31m" + strings.Repeat("r", fidelityCols-1) + "\x1b[32mG\x1b[4;35m", "pen"},
	{"pending-in-origin-mode", "\x1b[3;6r\x1b[?6h\x1b[2;1H" + strings.Repeat("o", fidelityCols), "NEXT"},
	// Left and right margins under origin mode, where a column is addressed
	// from the left margin. A restore that addressed it from the screen edge
	// put the cursor, and the reprint that arms the wrap, margin columns too
	// far left: the wide glyph wrapped, its first copy was erased, and the
	// wrap was lost.
	{"pending-wide-in-left-margin", "\x1b[?69h\x1b[5;20s\x1b[?6h" + strings.Repeat("x", 14) + "日", "N"},
	{"cursor-in-left-margin", "\x1b[?69h\x1b[5;20s\x1b[?6h\x1b[1;6H", "N"},
	{"pending-on-last-row", "\x1b[" + "8;1H" + strings.Repeat("b", fidelityCols), "SCROLLED"},
}

func TestWireCarriesThePendingWrap(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runPendingWrap(t, newPure, newPure)
}

// runPendingWrap feeds each case's output to a daemon emulator, restores its
// snapshot into a client, feeds both the output that follows, and compares.
func runPendingWrap(t *testing.T, newDaemon, newClient func() vt.Terminal) {
	for _, form := range wireForms {
		for _, c := range pendingWrapCases {
			t.Run(form.name+"/"+c.name, func(t *testing.T) {
				daemon := newDaemon()
				defer closeEmulator(daemon)
				client := newClient()
				defer closeEmulator(client)

				if _, err := daemon.Write([]byte(c.before)); err != nil {
					t.Fatalf("feed the daemon: %v", err)
				}
				state := TerminalStateOf(daemon, fidelityCols, fidelityRows, 100, 0)
				ApplyTerminalState(client, throughWire(t, state, form.packed))
				if got, want := client.CursorPendingWrap(), daemon.CursorPendingWrap(); got != want {
					t.Errorf("pending wrap after the restore: client %v, daemon %v", got, want)
				}

				for _, e := range []vt.Terminal{daemon, client} {
					if _, err := e.Write([]byte(c.after)); err != nil {
						t.Fatalf("feed the stream: %v", err)
					}
				}
				compareEmulators(t, daemon, client)
			})
		}
	}
}

func closeEmulator(term vt.Terminal) {
	if c, ok := term.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}
