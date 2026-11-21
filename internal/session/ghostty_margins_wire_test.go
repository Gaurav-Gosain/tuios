//go:build ghostty

package session

import (
	"fmt"
	"strings"
	"testing"

	"tuios.dev/vt"
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
			name:   "declrmm-off-and-on",
			stream: "\x1b[?69h\x1b[5;20s\x1b[?69l\x1b[?69h",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			name:   "decstr",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10r\x1b[!p",
			probe:  "\x1b[10;1H\nnew",
		},
		{
			name:   "declrmm-margins-kept",
			stream: "\x1b[?69h\x1b[5;20s",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			name:   "decaln",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10r\x1b[?69h\x1b[5;20s\x1b#8",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30) + "\x1b[10;1H\nnew",
		},
		{
			name:   "xtrestore-declrmm-off",
			stream: "\x1b[?69s\x1b[?69h\x1b[5;20s\x1b[?69r",
			probe:  "\x1b[1;7H" + strings.Repeat("x", 30),
		},
		{
			name:   "decstbm-three-parameters",
			stream: "\x1b[H" + fill.String() + "\x1b[3;10;5r",
			probe:  "\x1b[10;1H\nnew",
		},
		{
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
