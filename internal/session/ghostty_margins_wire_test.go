//go:build ghostty

package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestGhosttyReattachKeepsTheGuestsMargins is the reattach of a pane on the
// ghostty backend whose guest changed its margins in a way the Go-side copy of
// them used to get wrong. The snapshot carries that copy, so a wrong copy
// gives the client margins the guest does not have.
//
// The clients are compared by behaviour, not by ScrollRegion: after the
// restore the same probe goes to the daemon's terminal and to the client, and
// the two screens must agree. Comparing the region alone would compare the
// copy with itself.
func TestGhosttyReattachKeepsTheGuestsMargins(t *testing.T) {
	var fill strings.Builder
	for i := 1; i <= 12; i++ {
		if i > 1 {
			fill.WriteString("\r\n")
		}
		fmt.Fprintf(&fill, "L%d", i)
	}
	cases := []struct {
		name, stream, probe string
	}{
		{
			// The library gives the columns back on ?69l. The copy kept 5..20,
			// and the client wrapped the probe at column 20.
			name:   "declrmm-off-and-on",
			stream: "\x1b[?69h\x1b[5;20s\x1b[?69l\x1b[?69h",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			// The library ignores DECSTR and keeps rows 3 to 10. The copy said
			// the full page, and the client's line feed on row 10 scrolled
			// nothing.
			name:   "decstr",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10r\x1b[!p",
			probe:  "\x1b[10;1H\nnew",
		},
		{
			// Margins the guest still has. The restore sent the library only
			// DECSTBM, so a ghostty client wrapped the probe at the full
			// width while the daemon wrapped it at column 20.
			name:   "declrmm-margins-kept",
			stream: "\x1b[?69h\x1b[5;20s",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			// DECALN resets every margin in the library. The copy kept them.
			name:   "decaln",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10r\x1b[?69h\x1b[5;20s\x1b#8",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30) + "\x1b[10;1H\nnew",
		},
		{
			// XTRESTORE puts DECLRMM back to off, which gives the columns
			// back in the library. The copy kept them.
			name:   "xtrestore-declrmm-off",
			stream: "\x1b[?69s\x1b[?69h\x1b[5;20s\x1b[?69r",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			// The library ignores DECSTBM with three parameters. The copy
			// took the first two.
			name:   "decstbm-three-parameters",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10;5r",
			probe:  "\x1b[10;1H\nnew",
		},
		{
			// The same for DECSLRM.
			name:   "decslrm-three-parameters",
			stream: "\x1b[?69h\x1b[5;20;7s",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
	}
	clients := map[string]func() vt.Terminal{
		"ghostty-client": func() vt.Terminal { return vt.NewGhosttyTerminal(40, 12) },
		"pure-client":    func() vt.Terminal { return vt.NewEmulator(40, 12) },
	}
	for _, tc := range cases {
		for clientName, newClient := range clients {
			t.Run(tc.name+"/"+clientName, func(t *testing.T) {
				daemon := vt.NewGhosttyTerminal(40, 12)
				client := newClient()
				defer closeTerminal(daemon)
				defer closeTerminal(client)
				if _, err := daemon.Write([]byte(tc.stream)); err != nil {
					t.Fatal(err)
				}
				ApplyTerminalState(client, TerminalStateOf(daemon, 40, 12, 200, 0))
				for _, term := range []vt.Terminal{daemon, client} {
					if _, err := term.Write([]byte(tc.probe)); err != nil {
						t.Fatal(err)
					}
				}
				compareScreenText(t, tc.name, daemon, client)
			})
		}
	}
}
