package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestShowKeysOverlayFullscreenWindow proves the keycast overlay appears on the
// first keypress even when a single window fills the whole content area, which is
// the case the fullscreen fast path (buildFullscreenFrame) handles by bypassing
// the compositor. That path used to skip renderOverlays entirely, so a user
// typing into a zoomed/maximized shell saw no keycast until some unrelated redraw
// (a scrollbar appearing, a second window, an overlay opening) disqualified the
// fast path, which is the multi-second, many-keypress lag this guards against.
func TestShowKeysOverlayFullscreenWindow(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--show-keys"}})
	waitBoot(t, term)

	// One window, then zoom it so it fills the entire content area (X=0, full
	// width and height): exactly the fullscreenFastWindow eligibility.
	newWindow(t, term)
	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("send zoom 'z': %v", err)
	}
	// The zoom press itself must land in the keycast while still in window mode.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "z")
	}, uiTimeout); err != nil {
		t.Fatalf("keycast never showed the zoom 'z' keypress: %v\n%s", err, term.Snapshot())
	}

	// Let the keycast for 'z' expire (3s CleanupExpiredKeys) so the next assertion
	// cannot pass on a stale row, and so the fullscreen window is settled.
	time.Sleep(3500 * time.Millisecond)

	enterTerminalMode(t, term)

	// A single distinctive key typed into the now-fullscreen shell. It must show in
	// the keycast promptly. A lone keystroke produces no scrollback and no other
	// redraw trigger, so on the buggy fast path the overlay never appears here.
	if err := term.SendKeys("Q"); err != nil {
		t.Fatalf("send 'Q': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "Q")
	}, 4*time.Second); err != nil {
		t.Fatalf("keycast never showed 'Q' typed into a fullscreen window: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after fullscreen keycast check")
}
