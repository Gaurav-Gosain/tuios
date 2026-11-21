package vt

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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

	if _, err := src.Write([]byte("\x1b[?1h\x1b[?1003h\x1b[?1006h\x1b[?1016h\x1b[?1004h\x1b[?2004h")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	modes := src.GetModes()
	for _, m := range []ansi.DECMode{
		ansi.ModeCursorKeys,
		ansi.ModeMouseAnyEvent,
		ansi.ModeMouseExtSgr,
		ansi.ModeMouseExtSgrPixel,
		ansi.ModeFocusEvent,
		ansi.ModeBracketedPaste,
	} {
		if !modes[int(m)] {
			t.Errorf("GetModes() lost DEC mode ?%d", int(m))
		}
	}

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
	if _, err := dst.Write([]byte("\x1b[<1u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := dst.KittyKeyboardFlags(); got != 1 {
		t.Fatalf("flags after pop = %d, want 1 (stack depth lost in round trip)", got)
	}

	fresh := NewEmulator(80, 24)
	defer func() { _ = fresh.Close() }()
	fresh.RestoreKittyKeyboardState(nil)
	if got := fresh.KittyKeyboardFlags(); got != 0 {
		t.Fatalf("flags after nil restore = %d, want 0", got)
	}
}
