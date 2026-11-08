package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kittyHyperK is hyper+k in the kitty keyboard protocol: modifier 17 is
// 1 + hyper 16.
const kittyHyperK = "\x1b[107;17u"

// How TestSuperKeysTypeNothingInAPlainPane could pass wrongly, written down
// first:
//   - The key could be dropped for every pane, also for one that asked for
//     kitty keys and reads super+k itself. The second pane turns the protocol
//     on and must get CSI 107 ; 9 u.
//   - The k could arrive late, after the B, and the line still read AB at a
//     glance. The test reads the output line exactly.
//   - Hyper is the other modifier a legacy pane cannot see. It is sent too.
//   - The bound super+k (clear_scrollback in the docs) is not part of this:
//     a binding takes the key before a pane does, see TestClearScrollback.

// TestSuperKeysTypeNothingInAPlainPane sends super+k and hyper+k, as a kitty
// host sends Cmd+K, to a shell that did not ask for kitty keys. The legacy
// encoding has no way to say Super or Hyper, so the pane must get nothing:
// before the fix it got a bare k, and "echo A<super+k>B" printed AkB.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with paneKeyBytes
// sending the legacy bytes of a Super key again, the output line is AkkB.
func TestSuperKeysTypeNothingInAPlainPane(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	time.Sleep(300 * time.Millisecond)

	for _, k := range []string{"echo A", kittySuperK, kittyHyperK, "B"} {
		if err := term.Type(k); err != nil {
			t.Fatalf("send %q: %v", k, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send Enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneLineIs(s, "AB") || paneLineIs(s, "AkkB") || paneLineIs(s, "AkB")
	}, uiTimeout); err != nil {
		t.Fatalf("the echo never ran: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "super-keys-plain")
	if !paneLineIs(term.Screen(), "AB") {
		t.Fatalf("ASSERTION: super+k or hyper+k typed a k into a plain shell\n%s", term.Snapshot())
	}

	// A pane that asked for kitty keys gets super+k as kitty sends it.
	if err := term.Type(`printf '\033[>1u'; cat -v` + "\r"); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.Type(kittySuperK); err != nil {
		t.Fatalf("send super+k: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send Enter: %v", err)
	}
	if err := term.WaitForText("^[[107;9u", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a pane that asked for kitty keys did not get super+k: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "super-keys-kitty")
}

// paneLineIs reports whether a line of the screen, inside the pane's border,
// is exactly want.
func paneLineIs(s tuitest.Screen, want string) bool {
	_, rows := s.Size()
	for r := range rows {
		line := strings.Trim(s.Line(r), " │┃|")
		if line == want {
			return true
		}
	}
	return false
}
