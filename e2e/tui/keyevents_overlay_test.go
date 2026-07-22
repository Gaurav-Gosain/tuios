package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// keyEventsTitle is the header the key-events diagnostic overlay renders.
const keyEventsTitle = "Key events"

// TestKeyEventsOverlayRendersBothModes proves the --show-keys diagnostic overlay
// is on screen and shows decoded key events in both window-management and
// terminal mode on the real binary. It asserts on the overlay's rendered text
// (the title plus a decoded "code=" field), not on a state flag.
func TestKeyEventsOverlayRendersBothModes(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--show-keys"}})
	waitBoot(t, term)

	// Window-management mode: creating a window is a keypress, so the overlay
	// must capture it and show the decoded event.
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, keyEventsTitle) && strings.Contains(text, "code=")
	}, uiTimeout); err != nil {
		t.Fatalf("key-events overlay not visible in window mode: %v\n%s", err, term.Snapshot())
	}

	// Terminal mode: the overlay captures keys here too. Enter terminal mode and
	// type a character; the overlay must still be present and decoding.
	enterTerminalMode(t, term)
	if err := term.SendKeys("x"); err != nil {
		t.Fatalf("send x: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, keyEventsTitle) && strings.Contains(text, "code=")
	}, uiTimeout); err != nil {
		t.Fatalf("key-events overlay not visible in terminal mode: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after key-events overlay checks")
}

// TestKeyEventsOverlayShowsCtrlP is the combined Task A + Task B check the owner
// asked for: with the overlay on, pressing Ctrl+P must both show the decoded
// ctrl+p event in the overlay AND open the command palette.
func TestKeyEventsOverlayShowsCtrlP(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--show-keys"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// Legacy control byte form.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on ctrl+p with overlay on")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, keyEventsTitle) && strings.Contains(text, "ctrl+p")
	}, uiTimeout); err != nil {
		t.Fatalf("overlay did not show the ctrl+p event: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after ctrl+p overlay check")
}
