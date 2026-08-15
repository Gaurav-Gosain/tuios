package tuie2e

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The Kitty keyboard protocol encodings this file drives. tuitest writes them
// verbatim to the PTY, so these are byte-for-byte what a terminal that has
// negotiated the protocol sends, and bubbletea decodes them whether or not the
// host under the test agreed to anything.
const (
	// kittyAltTilde is Option+n as Ghostty and kitty report it on macOS when
	// Option is left to compose: the Alt bit is set, but the key code is the
	// tilde the OS composed (U+02DC, 732), with the PC-101 base code 110 ('n')
	// alongside it. CSI 732::110 ; 3 u.
	kittyAltTilde = "\x1b[732::110;3u"

	// kittyLeftAltPress and kittyLeftAltRelease are the Option key itself, which
	// a terminal only reports as a key of its own in report-all-keys mode.
	// 57443 is the protocol's code for the left alt key; :3 in the second
	// parameter is the release event type.
	kittyLeftAltPress   = "\x1b[57443;3u"
	kittyLeftAltRelease = "\x1b[57443;3:3u"

	// kittyAltQuestion is '?' struck while Option is held: what a held trigger
	// puts on every other key.
	kittyAltQuestion = "\x1b[63;3u"
)

// holdModeConfig binds the momentary window mode to the left Option/Alt key.
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

	// A marker the second pane's shell computes, so the assertion cannot pass on
	// an echo of the keystrokes.
	runInShell(t, term, "echo pane-$((20+2))", "pane-22", shellTimeout)

	if err := term.SendKeys(tuitest.Key(kittyAltTilde)); err != nil {
		t.Fatalf("send the composed alt+n: %v", err)
	}

	// The focus moved if the other pane's shell answers the next command, and
	// the composed tilde was not typed into either shell.
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

	// Holding the trigger borrows window mode, and says so: a momentary mode
	// that looked like the permanent one would leave the user guessing.
	if err := term.SendKeys(tuitest.Key(kittyLeftAltPress)); err != nil {
		t.Fatalf("send the held key: %v", err)
	}
	if err := term.WaitForText("HOLD", uiTimeout); err != nil {
		t.Fatalf("holding the key did not show the momentary mode: %v\n%s", err, term.Snapshot())
	}

	// A chord struck while it is held carries the trigger's own modifier and has
	// to run the window-mode action bound to the key that was tapped.
	if err := term.SendKeys(tuitest.Key(kittyAltQuestion)); err != nil {
		t.Fatalf("send the held chord: %v", err)
	}
	if err := term.WaitForText(helpTitle, uiTimeout); err != nil {
		t.Fatalf("the held chord did not run the window-mode action: %v\n%s", err, term.Snapshot())
	}
	// Nothing leaked into the pane: the shell never saw a '?' to answer for.
	if text := term.Screen().Text(); strings.Contains(text, "?: command not found") {
		t.Fatalf("the held chord was typed into the pane:\n%s", term.Snapshot())
	}

	// Close the overlay with the same held chord, then let go.
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

	// Back in the mode the hold interrupted, with the same pane focused: the
	// shell answers again with no mode switch of any kind in between.
	time.Sleep(insertGuard)
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
	time.Sleep(insertGuard)
	if text := term.Screen().Text(); strings.Contains(text, "HOLD") {
		t.Fatalf("an unbound hold key still borrowed window mode:\n%s", term.Snapshot())
	}
	// Still in terminal mode, still the same pane.
	runInShell(t, term, "echo still-$((4+5))", "still-9", shellTimeout)
}

// TestKeyboardEnhancementsAreRequested pins the negotiation itself. tuios has to
// ask the host for alternate-key reporting, which is what carries the key behind
// a composed or non-US-layout character; nothing else can turn one back into the
// chord the user struck.
func TestKeyboardEnhancementsAreRequested(t *testing.T) {
	out := &syncBuffer{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out})
	waitBoot(t, term)

	// CSI = 5 ; 1 u: disambiguate (1) plus alternate keys (4), set exactly.
	const want = "\x1b[=5;1u"
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tuios never asked the terminal for alternate-key reporting (%q)\n%s",
		want, term.Snapshot())
}

// syncBuffer collects the raw bytes tuios writes to its terminal. The harness
// mirrors the PTY into it from its own goroutine, so it needs a lock.
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
