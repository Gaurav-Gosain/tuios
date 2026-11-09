package tuie2e

import (
	"runtime"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitSendNowKeys sends a reply to a working agent with each byte
// sequence a terminal sends for Option+Enter or Ctrl+Enter, and reads where
// the reply went: typed into the pane now (send now) or queued for the rest.
//
//	ESC CR        Option+Enter as Alt: Ghostty with macos-option-as-alt,
//	              kitty with macos_option_as_alt, iTerm2 Esc+, Terminal.app
//	              Use Option as Meta, and Alt+Enter on Linux
//	CSI 13;3u     the same with the kitty keyboard protocol
//	CSI 13;5u     Ctrl+Enter with the kitty keyboard protocol (Ghostty, kitty,
//	              iTerm2 3.5, WezTerm)
//	ESC LF        Option+Enter from a terminal that sends LF for Enter
//	CR            Option+Enter where Option is not Alt, the default in all of
//	              them: a plain enter, which queues
//
// The ways it can go wrong, each an assertion: a form sends nothing, or queues
// where it should send now; plain CR sends now, so enter could not queue; the
// hint names alt+enter on a Mac, where the default terminal cannot send it.
func TestCockpitSendNowKeys(t *testing.T) {
	base, _ := echoThreadBase(t, "")
	term := startEchoClient(t, base)
	if err := term.SendKeys("r"); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	text := term.Screen().Text()
	switch {
	case runtime.GOOS == "darwin" && (strings.Contains(text, "alt+↵ send now") || !strings.Contains(text, "↵ send now")):
		t.Fatalf("ASSERTION: on a Mac the hint does not name ⌥↵ or ⌃↵ for send now\n%s", term.Snapshot())
	case runtime.GOOS != "darwin" && !strings.Contains(text, "alt+↵ send now"):
		t.Fatalf("ASSERTION: the hint does not name alt+↵ for send now\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "send-now-hint")

	forms := []struct{ name, seq string }{
		{"esc-cr", "\x1b\r"},
		{"kitty-alt", "\x1b[13;3u"},
		{"kitty-ctrl", "\x1b[13;5u"},
		{"esc-lf", "\x1b\n"},
	}
	for _, f := range forms {
		word := "now-" + f.name
		sendWith(t, term, word, f.seq)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), word) && strings.Contains(s.Text(), "Sent, waiting for")
		}, uiTimeout); err != nil {
			saveCockpitFrames(t, term, "send-now-"+f.name)
			out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "api")
			t.Fatalf("ASSERTION: %s (%q) did not send the reply now: %v\nqueue: %s\n%s", f.name, f.seq, err, out, term.Snapshot())
		}
		if out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "api"); strings.Contains(out, word) {
			t.Fatalf("ASSERTION: %s queued the reply instead of sending it now:\n%s", f.name, out)
		}
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", "api", "-w", "header-fix")
		if !strings.Contains(pane, word) {
			t.Fatalf("ASSERTION: %s: the reply is not in the agent's pane:\n%s", f.name, pane)
		}
	}
	saveCockpitFrames(t, term, "send-now-sent")

	// A plain CR is enter: the reply is queued for the rest.
	sendWith(t, term, "queued-cr", "\r")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "queued-cr") && strings.Contains(s.Text(), "Queued, typed when it rests")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a plain CR did not queue the reply: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "send-now-cr-queued")
	alive(t, term, "after the send-now keys")
}

// sendWith opens the reply area, types text and sends seq, the bytes a
// terminal sends for the key.
func sendWith(t *testing.T, term *tuitest.Terminal, text, seq string) {
	t.Helper()
	for _, k := range []any{"r", text, seq} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
}

// sendNowHint is the reply area's send-now hint as the platform names it: ⌥↵
// or ⌃↵ on a Mac, by whether the host speaks the kitty keyboard protocol,
// and alt+↵ elsewhere. On a Mac it is the part both share.
func sendNowHint() string {
	if runtime.GOOS == "darwin" {
		return "↵ send now"
	}
	return "alt+↵ send now"
}
