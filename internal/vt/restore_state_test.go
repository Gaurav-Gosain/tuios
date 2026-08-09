package vt

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRestoreModesRefreshesMouseCaches is the regression test for mouse input
// dying after a daemon reattach. RestoreModes wrote the modes map directly but
// left the atomic caches behind HasMouseMode and HasAllMotionMode stale, so a
// reattached client's input layer saw no mouse mode and routed wheel, motion
// and click to scrollback and copy mode instead of the pane.
func TestRestoreModesRefreshesMouseCaches(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()

	e.RestoreModes(map[int]bool{
		int(ansi.ModeMouseAnyEvent): true, // ?1003
		int(ansi.ModeMouseExtSgr):   true, // ?1006
	})

	if !e.HasMouseMode() {
		t.Fatal("HasMouseMode() = false after RestoreModes set ?1003: the atomic cache was not refreshed")
	}
	if !e.HasAllMotionMode() {
		t.Fatal("HasAllMotionMode() = false after RestoreModes set ?1003: the atomic cache was not refreshed")
	}

	e.RestoreModes(map[int]bool{
		int(ansi.ModeMouseAnyEvent): false,
		int(ansi.ModeMouseExtSgr):   false,
	})
	if e.HasMouseMode() {
		t.Fatal("HasMouseMode() = true after RestoreModes reset ?1003")
	}
}

// TestRestoreModesAppliesCursorVisibility pins the DECTCEM side effect: a
// guest that hid its cursor must not get it back on reattach, because the
// hide sequence is long gone from the daemon's bounded output buffer.
func TestRestoreModesAppliesCursorVisibility(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()

	e.RestoreModes(map[int]bool{int(ansi.ModeTextCursorEnable): false})
	if !e.IsCursorHidden() {
		t.Fatal("cursor visible after restoring DECTCEM reset")
	}

	e.RestoreModes(map[int]bool{int(ansi.ModeTextCursorEnable): true})
	if e.IsCursorHidden() {
		t.Fatal("cursor hidden after restoring DECTCEM set")
	}
}

// TestGetModesCapturesAllStickyDECModes proves the capture side ships the
// full DEC mode set a guest enabled once at startup, not a hand-picked list.
// SGR-pixel (?1016) is the canary: it was missing from the old whitelist, so
// a browser pane's hover reports silently downgraded from pixels to cells
// after every reattach.
func TestGetModesCapturesAllStickyDECModes(t *testing.T) {
	src := NewEmulator(80, 24)
	defer func() { _ = src.Close() }()

	// The modes a browser-class guest enables once at startup, plus the other
	// sticky input/rendering modes.
	if _, err := src.Write([]byte("\x1b[?1h\x1b[?1003h\x1b[?1006h\x1b[?1016h\x1b[?1004h\x1b[?2004h")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	modes := src.GetModes()
	for _, m := range []ansi.DECMode{
		ansi.ModeCursorKeys,       // ?1
		ansi.ModeMouseAnyEvent,    // ?1003
		ansi.ModeMouseExtSgr,      // ?1006
		ansi.ModeMouseExtSgrPixel, // ?1016
		ansi.ModeFocusEvent,       // ?1004
		ansi.ModeBracketedPaste,   // ?2004
	} {
		if !modes[int(m)] {
			t.Errorf("GetModes() lost DEC mode ?%d", int(m))
		}
	}

	// Round trip into a fresh emulator, the way a reattaching client rebuilds
	// its per-window emulator from the daemon's snapshot.
	dst := NewEmulator(80, 24)
	defer func() { _ = dst.Close() }()
	dst.RestoreModes(modes)

	if !dst.HasMouseMode() {
		t.Fatal("restored emulator reports no mouse mode")
	}
	if !dst.SupportsMotionEvents() {
		t.Fatal("restored emulator reports no motion support")
	}
	if !dst.ApplicationCursorKeys() {
		t.Fatal("restored emulator lost DECCKM")
	}
	// SGR-pixel drives encodeMouseReport directly: with 1016 restored, a
	// report must carry pixel coordinates, not cell indices.
	dst.SetCellSize(10, 20)
	report := dst.EncodeMouseEvent(MouseClick{Button: MouseLeft, X: 4, Y: 2})
	if report != "\x1b[<0;46;51M" {
		t.Fatalf("mouse report = %q, want pixel coordinates %q (?1016 lost in round trip)", report, "\x1b[<0;46;51M")
	}
}

// TestKittyKeyboardStackRoundTrip pins that the kitty keyboard protocol
// state, negotiated once by the guest, survives capture and restore.
func TestKittyKeyboardStackRoundTrip(t *testing.T) {
	src := NewEmulator(80, 24)
	defer func() { _ = src.Close() }()

	// Push disambiguate, then push the full flag set awrit uses.
	if _, err := src.Write([]byte("\x1b[>1u\x1b[>31u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := src.KittyKeyboardFlags(); got != 31 {
		t.Fatalf("source flags = %d, want 31", got)
	}

	stack := src.KittyKeyboardStack()

	dst := NewEmulator(80, 24)
	defer func() { _ = dst.Close() }()
	dst.RestoreKittyKeyboardState(stack)

	if got := dst.KittyKeyboardFlags(); got != 31 {
		t.Fatalf("restored flags = %d, want 31", got)
	}
	// The stack itself must survive so a later pop lands on the pushed entry.
	if _, err := dst.Write([]byte("\x1b[<1u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := dst.KittyKeyboardFlags(); got != 1 {
		t.Fatalf("flags after pop = %d, want 1 (stack depth lost in round trip)", got)
	}

	// An empty snapshot (state from an older daemon) must leave the default
	// state untouched rather than clearing the stack invariant.
	fresh := NewEmulator(80, 24)
	defer func() { _ = fresh.Close() }()
	fresh.RestoreKittyKeyboardState(nil)
	if got := fresh.KittyKeyboardFlags(); got != 0 {
		t.Fatalf("flags after nil restore = %d, want 0", got)
	}
}
