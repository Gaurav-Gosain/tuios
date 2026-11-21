package tuie2e

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	kittyAltTilde = "\x1b[732::110;3u"

	kittyLeftAltPress   = "\x1b[57443;3u"
	kittyLeftAltRelease = "\x1b[57443;3:3u"

	kittyAltQuestion = "\x1b[63;3u"
)

const holdModeConfig = "[keybindings.mode_control]\nhold_window_mode = [\"leftalt\"]\n"

// TestKittyAltChordSwitchesPaneFromTerminalMode drives the reported bug's own
// encoding. On macOS the Option key composes a character instead of setting
// Alt, so alt+n arrives as the composed tilde and the pane never switched. The
// base-layout code says which key it really was, and the binding has to answer
// to it.
func TestKittyAltChordSwitchesPaneFromTerminalMode(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)

	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes to switch between")
	enterTerminalMode(t, term)

	runInShell(t, term, "echo pane-$((20+2))", "pane-22", shellTimeout)

	if err := term.SendKeys(tuitest.Key(kittyAltTilde)); err != nil {
		t.Fatalf("send the composed alt+n: %v", err)
	}

	runInShell(t, term, "echo moved-$((20+3))", "moved-23", shellTimeout)
	if text := term.Screen().Text(); strings.Contains(text, "˜") {
		t.Fatalf("the composed character was typed into a pane:\n%s", term.Snapshot())
	}
}

// TestHoldKeyBorrowsWindowMode is report 2 end to end: hold the key and tuios is
// in window mode, act while holding, let go and the pane has the keyboard back.
func TestHoldKeyBorrowsWindowMode(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, holdModeConfig)

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "a pane to type into")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)

	if err := term.SendKeys(tuitest.Key(kittyLeftAltPress)); err != nil {
		t.Fatalf("send the held key: %v", err)
	}
	if err := term.WaitForText("HOLD", uiTimeout); err != nil {
		t.Fatalf("holding the key did not show the momentary mode: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Key(kittyAltQuestion)); err != nil {
		t.Fatalf("send the held chord: %v", err)
	}
	if err := term.WaitForText(helpTitle, uiTimeout); err != nil {
		t.Fatalf("the held chord did not run the window-mode action: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "?: command not found") {
		t.Fatalf("the held chord was typed into the pane:\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Key(kittyAltQuestion)); err != nil {
		t.Fatalf("close help with the held chord: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), helpTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the overlay did not close: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Key(kittyLeftAltRelease)); err != nil {
		t.Fatalf("release the held key: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "HOLD")
	}, uiTimeout); err != nil {
		t.Fatalf("releasing the key did not leave the momentary mode: %v\n%s", err, term.Snapshot())
	}

	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}

// TestHoldKeyIsInertWhenUnbound is the default. Nothing is bound to a held key,
// so the same sequences must change nothing: a terminal that reports modifier
// keys must not put an unconfigured tuios into a mode the user did not ask for.
func TestHoldKeyIsInertWhenUnbound(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "a pane to type into")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo idle-$((4+4))", "idle-8", shellTimeout)

	if err := term.SendKeys(tuitest.Key(kittyLeftAltPress)); err != nil {
		t.Fatalf("send the modifier key press: %v", err)
	}
	if text := term.Screen().Text(); strings.Contains(text, "HOLD") {
		t.Fatalf("an unbound hold key still borrowed window mode:\n%s", term.Snapshot())
	}
	runInShell(t, term, "echo still-$((4+5))", "still-9", shellTimeout)
}

// TestKeyboardEnhancementsAreRequested pins the negotiation itself. tuios has to
// ask the host for alternate-key reporting, which is what carries the key behind
// a composed or non-US-layout character; nothing else can turn one back into the
// chord the user struck.
//
// Window mode reads keys against bindings, so it also asks for every key as an
// escape code with its text alongside (CSI = 29 ; 1 u): alternate keys only
// reach a key the host already sends as an escape code. Terminal mode gives the
// pane text as text again (CSI = 5 ; 1 u). See TestLeaderAsksForLayoutKeys.
func TestKeyboardEnhancementsAreRequested(t *testing.T) {
	out := &syncBuffer{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out})
	waitBoot(t, term)

	waitForRequest(t, term, out, 0, kittyWindowModePush)

	newWindow(t, term)
	mark := len(out.String())
	enterTerminalMode(t, term)
	waitForRequest(t, term, out, mark, kittyPaneFlags)
}

const (
	kittyWindowModePush  = "\x1b[>29u"
	kittyWindowModeFlags = "\x1b[=29;1u"
	kittyPaneFlags       = "\x1b[=5;1u"
)

func waitForRequest(t *testing.T, term *tuitest.Terminal, out *syncBuffer, from int, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String()[from:], want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tuios never asked the terminal for %q\n%s", want, term.Snapshot())
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestHoldKeyIsIgnoredInWindowMode is issue 1744. The hold key borrows window
// mode from terminal mode. Already in window mode it has nothing to borrow,
// and it must stay a plain modifier: a hold armed there takes Alt off every
// chord, so alt+n ran new-window instead of next-window.
func TestHoldKeyIsIgnoredInWindowMode(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, holdModeConfig)

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes to switch between")
	enterTerminalMode(t, term)
	leaveTerminalMode(t, term)

	if err := term.SendKeys(tuitest.Key(kittyLeftAltPress)); err != nil {
		t.Fatalf("send the held key: %v", err)
	}
	if err := term.SendKeys(tuitest.Key(kittyAltN)); err != nil {
		t.Fatalf("send alt+n: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := term.Screen()
		if strings.Contains(s.Text(), "HOLD") {
			t.Fatalf("the hold key borrowed window mode while already in it:\n%s", term.Snapshot())
		}
		if n := countWindows(s); n != 2 {
			t.Fatalf("alt+n in window mode lost its Alt: %d panes, want 2\n%s", n, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := term.SendKeys(tuitest.Key(kittyLeftAltRelease)); err != nil {
		t.Fatalf("release the held key: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("releasing the key left window mode: %v\n%s", err, term.Snapshot())
	}
}
