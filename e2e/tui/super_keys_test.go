package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const kittyHyperK = "\x1b[107;17u"

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
