package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func adviceModel() *OS {
	return &OS{Settings: config.DefaultSettings(), KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
}

// TestLocalClientGetsTheNamedAdvice is the control: a client in the terminal it
// detects gets the one step for that terminal.
func TestLocalClientGetsTheNamedAdvice(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM", "xterm-ghostty")

	m := adviceModel()
	m.NoteComposedOptionChord("alt+n")
	if len(m.Notifications) != 1 {
		t.Fatalf("a local client was told nothing about a chord that did not arrive")
	}
	if msg := m.Notifications[0].Message; !strings.Contains(msg, "ghostty/config") {
		t.Fatalf("the advice does not name the detected terminal: %q", msg)
	}
}

// TestBrowserClientGetsNoTerminalAdvice checks the browser case.
//
// The advice is one line of a terminal config file. A browser tab has no such
// file, and macOS composes the character before the page ever sees the key, so
// there is nothing the user can do with the advice and it is not shown.
func TestBrowserClientGetsNoTerminalAdvice(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")

	m := adviceModel()
	m.BrowserClient = true
	m.RemoteClient = true
	m.NoteComposedOptionChord("alt+n")
	if len(m.Notifications) != 0 {
		t.Fatalf("a browser client was told to edit a terminal config file it has no way to reach: %q",
			m.Notifications[0].Message)
	}
	if m.optionAdviceShown {
		t.Fatalf("the browser client spent its one advice on a message it never showed")
	}
}

// TestSSHClientGetsGenericAdvice checks the SSH case. The environment this
// process reads describes the server, so naming a product from it names the
// wrong machine's terminal.
func TestSSHClientGetsGenericAdvice(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM", "xterm-ghostty")

	m := adviceModel()
	m.IsSSHMode = true
	m.RemoteClient = true
	m.NoteComposedOptionChord("alt+n")
	if len(m.Notifications) != 1 {
		t.Fatalf("an SSH client was told nothing about a chord that did not arrive")
	}
	msg := m.Notifications[0].Message
	if strings.Contains(msg, "ghostty") {
		t.Fatalf("the SSH client was given the server terminal's setting: %q", msg)
	}
	if !strings.Contains(msg, "Option as Meta/Alt") {
		t.Fatalf("the SSH client lost the step that is true of every terminal: %q", msg)
	}
}

// TestOptionArrowGetsTheAdviceToo.
//
// This is the case the advice was written for and could never reach. The
// composed-glyph path fires on a character macOS made out of a chord;
// Option+Left arrives as alt+b, with a real Alt modifier and no glyph, so
// pressing it produced no action and nothing to read. On Ghostty it needs a
// second line of config beyond the Option setting, which the message says.
//
// Negative control: checking alt+b for a binding instead of alt+left returns
// early, because alt+b is bound to nothing and is not supposed to be, and this
// fails with no notification at all.
func TestOptionArrowGetsTheAdviceToo(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM", "xterm-ghostty")

	m := adviceModel()
	m.NoteRewrittenAltArrow("alt+b", "alt+left")

	if len(m.Notifications) != 1 {
		t.Fatal("pressing Option+Left said nothing about why nothing happened")
	}
	msg := m.Notifications[0].Message
	for _, want := range []string{"alt+left", "alt+b", "ghostty/config", "unbind"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the advice does not mention %q: %q", want, msg)
		}
	}
}

// TestTheAdviceIsShownOnce, whichever way the subject comes up. It is one
// setting, and a warning repeating on every keypress is worse than the
// keypress missing.
func TestTheAdviceIsShownOnce(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")

	m := adviceModel()
	m.NoteRewrittenAltArrow("alt+b", "alt+left")
	m.NoteRewrittenAltArrow("alt+f", "alt+right")
	m.NoteComposedOptionChord("alt+n")

	if len(m.Notifications) != 1 {
		t.Errorf("the advice was shown %d times", len(m.Notifications))
	}
}
