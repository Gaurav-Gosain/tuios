package session

import (
	"strings"
	"testing"

	"tuios.dev/vt"
)

var pendingWrapCases = []seamCase{
	{"pending", "$ " + strings.Repeat("x", fidelityCols-3) + "Z", "NEXT\r\n"},
	{"parked-by-cup", strings.Repeat("x", fidelityCols) + "\r\n\x1b[2;40H", "Q\r\n"},
	{"pending-then-moved", strings.Repeat("x", fidelityCols), "\x1b[1;1HHOME"},
	{"pending-then-cr", strings.Repeat("x", fidelityCols), "\rCR"},
	{"pending-no-autowrap", "\x1b[?7l" + strings.Repeat("x", fidelityCols), "NOWRAP\r\n"},
	{"pending-wide", strings.Repeat("x", fidelityCols-2) + "日", "NEXT"},
	{"pending-line-drawing", "\x1b(0" + strings.Repeat("q", fidelityCols), "xx\x1b(Bok"},
	{"pending-under-pen", "\x1b[1;31m" + strings.Repeat("r", fidelityCols-1) + "\x1b[32mG\x1b[4;35m", "pen"},
	{"pending-in-origin-mode", "\x1b[3;6r\x1b[?6h\x1b[2;1H" + strings.Repeat("o", fidelityCols), "NEXT"},
	{"pending-wide-in-left-margin", "\x1b[?69h\x1b[5;20s\x1b[?6h" + strings.Repeat("x", 14) + "日", "N"},
	{"cursor-in-left-margin", "\x1b[?69h\x1b[5;20s\x1b[?6h\x1b[1;6H", "N"},
	{"pending-on-last-row", "\x1b[" + "8;1H" + strings.Repeat("b", fidelityCols), "SCROLLED"},
}

func TestWireCarriesThePendingWrap(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runPendingWrap(t, newPure, newPure)
}

type seamCase struct {
	name   string
	before string
	after  string
}

func runPendingWrap(t *testing.T, newDaemon, newClient func() vt.Terminal) {
	runSeamCases(t, pendingWrapCases, newDaemon, newClient)
}

func runSeamCases(t *testing.T, cases []seamCase, newDaemon, newClient func() vt.Terminal) {
	for _, form := range wireForms {
		for _, c := range cases {
			t.Run(form.name+"/"+c.name, func(t *testing.T) {
				daemon := newDaemon()
				defer closeEmulator(daemon)
				client := newClient()
				defer closeEmulator(client)

				if _, err := daemon.Write([]byte(c.before)); err != nil {
					t.Fatalf("feed the daemon: %v", err)
				}
				state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
				ApplyTerminalState(client, throughWire(t, state, form.packed))
				if got, want := client.CursorPendingWrap(), daemon.CursorPendingWrap(); got != want {
					t.Errorf("pending wrap after the restore: client %v, daemon %v", got, want)
				}
				if gw, gh, ww, wh := client.Width(), client.Height(), daemon.Width(), daemon.Height(); gw != ww || gh != wh {
					t.Errorf("size after the restore: client %dx%d, daemon %dx%d", gw, gh, ww, wh)
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
