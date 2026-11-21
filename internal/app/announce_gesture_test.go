package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func heldPane(t *testing.T, m *OS) (told *[][2]int, wantW, wantH int) {
	t.Helper()
	win := m.Windows[0]
	win.Resize(win.Width, win.Height)
	var got [][2]int
	win.DaemonResizeFunc = func(w, h int) error {
		got = append(got, [2]int{w, h})
		return nil
	}

	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 10})
	if !m.announceGestureHeld {
		t.Fatal("a press did not arm the gesture's announcement hold")
	}
	win.Resize(win.Width, win.Height-6)
	if len(got) != 0 {
		t.Fatalf("the pane was told %v during the gesture, want nothing", got)
	}
	w, h := win.AnnouncedSize()
	return &got, w, h
}

// TestAReleaseThatWentMissingStillEndsTheHold is the first backstop: the button
// is up by the next maintenance tick, however the release went astray.
func TestAReleaseThatWentMissingStillEndsTheHold(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)
	told, wantW, wantH := heldPane(t, m)

	m.Update(TickerMsg(time.Now()))
	if len(*told) != 0 {
		t.Fatalf("a tick during the gesture told the pane %v; the hold is the whole point", *told)
	}

	m.pointerDown = false
	m.Update(TickerMsg(time.Now()))

	if m.announceGestureHeld {
		t.Error("the hold survived a tick with no button held")
	}
	if len(*told) != 1 || (*told)[0] != [2]int{wantW, wantH} {
		t.Errorf("the pane was told %v, want exactly one %dx%d: a hold that never ends "+
			"is a pane that never learns its size again", *told, wantW, wantH)
	}
}

// TestAPointerGoneSilentStillEndsTheHold is the second backstop, and the only
// one left for the case the first cannot see: the release is lost outside the
// surface the events come from, so the press's "a button is down" is never
// corrected and no further motion arrives to correct it.
func TestAPointerGoneSilentStillEndsTheHold(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)
	told, wantW, wantH := heldPane(t, m)

	m.lastPointerAt = time.Now()
	m.Update(TickerMsg(time.Now()))
	if len(*told) != 0 {
		t.Fatalf("a pause in the middle of a drag told the pane %v, want nothing", *told)
	}

	m.lastPointerAt = time.Now().Add(-announceHoldTimeout - time.Second)
	m.Update(TickerMsg(time.Now()))

	if m.announceGestureHeld {
		t.Error("the hold survived a pointer that stopped reporting altogether")
	}
	if len(*told) != 1 || (*told)[0] != [2]int{wantW, wantH} {
		t.Errorf("the pane was told %v, want exactly one %dx%d", *told, wantW, wantH)
	}
}

// TestALayoutUpdateInsideAGestureDoesNotEndItsHold is why the hold is a depth
// count and not a flag. A gesture holds across many messages and every retile
// inside it holds again for the length of one call; if the inner release
// reached the guest, the drop's own retile would announce the size the drag was
// passing through and the gesture's hold would guard nothing.
func TestALayoutUpdateInsideAGestureDoesNotEndItsHold(t *testing.T) {
	m := newDeferralOS(t, 120, 40, 2)
	told, _, _ := heldPane(t, m)

	m.settleSizes(func() {
		win := m.Windows[0]
		win.Resize(win.Width, win.Height-2)
	})

	if len(*told) != 0 {
		t.Errorf("a layout update inside the gesture told the pane %v; the gesture's "+
			"hold must outlive it", *told)
	}
	if !m.announceGestureHeld {
		t.Error("a layout update inside the gesture cleared the gesture's own hold")
	}
}
