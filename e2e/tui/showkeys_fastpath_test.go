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

	newWindow(t, term)
	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("send zoom 'z': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "z")
	}, uiTimeout); err != nil {
		t.Fatalf("keycast never showed the zoom 'z' keypress: %v\n%s", err, term.Snapshot())
	}

	time.Sleep(3500 * time.Millisecond)

	enterTerminalMode(t, term)

	if err := term.SendKeys("Q"); err != nil {
		t.Fatalf("send 'Q': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "Q")
	}, uiTimeout); err != nil {
		t.Fatalf("keycast never showed 'Q' typed into a fullscreen window: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after fullscreen keycast check")
}
