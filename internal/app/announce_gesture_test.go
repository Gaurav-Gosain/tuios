package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The announcement hold a pointer gesture opens must never outlive the gesture.
// A pane that holds for ever is a pane that never learns its size again, which
// is worse than the SIGWINCH the hold exists to withhold. These pin the two
// backstops that make the hold's end unconditional: the button coming up, and
// the pointer going silent with a button still believed down.

// heldPane arms the gesture hold on a two-pane model and hands back a recorder
// of the sizes the first pane is told, plus the size it is waiting to be told.
func heldPane(t *testing.T, m *OS) (told *[][2]int, wantW, wantH int) {
	t.Helper()
	win := m.Windows[0]
	// Announce the settled layout once, which is the state a session is in
	// before a gesture starts, and only then start counting.
	win.Resize(win.Width, win.Height)
	var got [][2]int
	win.DaemonResizeFunc = func(w, h int) error {
		got = append(got, [2]int{w, h})
		return nil
	}

	// A press arms the hold. Everything after it is held back.
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

// TestTheGestureHoldEndsWithoutARelease covers both backstops. A tick during
// the gesture leaves the hold alone; the tick after the release goes missing
// ends it and tells the pane its size exactly once.
//
// "release lost": the button is up by the next maintenance tick, however the
// release went astray. "pointer silent": the release is lost outside the
// surface the events come from, so the press's "a button is down" is never
// corrected and no further motion arrives to correct it.
func TestTheGestureHoldEndsWithoutARelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		// during keeps the gesture alive for one tick, lose then ends it
		// without a release.
		during, lose func(m *OS)
	}{
		{
			name:   "release lost",
			during: func(*OS) {},
			lose:   func(m *OS) { m.pointerDown = false },
		},
		{
			name:   "pointer silent",
			during: func(m *OS) { m.lastPointerAt = time.Now() },
			lose:   func(m *OS) { m.lastPointerAt = time.Now().Add(-announceHoldTimeout - time.Second) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDeferralOS(t, 120, 40, 2)
			told, wantW, wantH := heldPane(t, m)

			tc.during(m)
			m.Update(TickerMsg(time.Now()))
			if len(*told) != 0 {
				t.Fatalf("a tick during the gesture told the pane %v, want nothing", *told)
			}

			tc.lose(m)
			m.Update(TickerMsg(time.Now()))
			if m.announceGestureHeld {
				t.Error("the hold survived the gesture")
			}
			if len(*told) != 1 || (*told)[0] != [2]int{wantW, wantH} {
				t.Errorf("the pane was told %v, want exactly one %dx%d: a hold that never ends "+
					"is a pane that never learns its size again", *told, wantW, wantH)
			}
		})
	}
}
