package app

import "testing"

// TestAPaneThatArrivesZoomedGetsTheLayoutsBorder is the pane a peer's sync
// creates with the zoom already on. Such a pane has never been tiled, so it
// holds the bordered default, and the zoom box used to be put around it without
// looking at that: under shared borders its guest ran two columns and two rows
// smaller than on every client that had seen the pane tiled before the zoom,
// and each client announced its own size for the same PTY.
// TestMultiClientConvergence failed 12 of 30 runs on its first seed this way.
//
// NEGATIVE CONTROL: without the border allowance in applyZoomRectAnimated the
// shared-borders case keeps Tiled false and a guest two cells short each way.
func TestAPaneThatArrivesZoomedGetsTheLayoutsBorder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		shared     bool
		borderless bool
	}{
		{name: "shared borders", shared: true, borderless: true},
		{name: "own borders", shared: false, borderless: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "arrived-zoomed", 30, 10)
			m := newTestOS(win)
			m.Width, m.Height = 100, 40
			m.AutoTiling = true
			m.SharedBorders = tc.shared
			// What createWindowFromSync leaves: the flag adopted, never tiled.
			win.Tiled = false
			win.Zoomed = true

			m.applyZoomRect(win, false)

			if win.Tiled != tc.borderless {
				t.Fatalf("the zoomed pane has Tiled=%v, want %v", win.Tiled, tc.borderless)
			}
			wantW, wantH := win.Width, win.Height
			if !tc.borderless {
				wantW, wantH = win.Width-2, win.Height-2
			}
			if win.ContentWidth() != wantW || win.ContentHeight() != wantH {
				t.Fatalf("a %dx%d zoom box gives the guest %dx%d, want %dx%d",
					win.Width, win.Height, win.ContentWidth(), win.ContentHeight(), wantW, wantH)
			}
		})
	}
}
