package vis

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// A fuzzer-generated payload is hostile by construction: a Guest action's text
// is an escape sequence aimed at a terminal. Echoing one unlaundered would let
// the system under test drive the instrument that is measuring it.
func TestGeneratedPayloadsCannotDriveTheTerminal(t *testing.T) {
	overlay.SetASCII(false)
	d := New(Options{
		Width: 100, Height: 30,
		Rules: []fuzz.RuleInfo{{Name: "panic", Family: "process", Doc: "Update swallowed a panic"}},
	})
	d.Start(0x9f3ac41d7e22b100, 4182)
	d.Step(0, fuzz.Action{Kind: fuzz.Guest, S: "\x1b[2J\x1b[?1049h\x1b]0;pwned\x07"}, nil)
	d.Step(1, fuzz.Action{Kind: fuzz.Rename, S: "\x1b[31mred"}, nil)
	frame := d.Frame()
	// The display's own escapes are colour and cursor control; the payload's
	// were a screen clear, an alt-screen switch and a title set.
	for _, forbidden := range []string{"\x1b[2J", "\x1b[?1049h", "\x1b]0;"} {
		if strings.Contains(frame, forbidden) {
			t.Errorf("a generated payload put %q into the frame", forbidden)
		}
	}
	if !strings.Contains(ansi.Strip(frame), "guest") {
		t.Error("the ledger dropped the action instead of laundering it")
	}
}
