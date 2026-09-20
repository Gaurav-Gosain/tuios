package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestToggleZoomResizesDaemonTerminal is the regression for TASK 3: zoom moved
// the window box but resized only the local emulator and PTY, so a daemon-hosted
// pane (Pty == nil, resized through DaemonResizeFunc) kept its old size and the
// app never reflowed. Both zoom-in and zoom-out must push the new size to the
// daemon.
//
// Run with the zoom slide off and on. With it off the pane is put in the box and
// the guest is told in the same breath. With it on the snap walks it there and
// the guest is told once at the destination, which is the bargain every other
// pane movement makes: telling it up front would reflow the app to a size the
// pane does not have yet and draw it wrong for the length of the slide. Either
// way the guest ends up at the size the pane ended up at, which is the promise
// this test is here for.
func TestToggleZoomResizesDaemonTerminal(t *testing.T) {
	for _, animate := range []bool{false, true} {
		name := "instant"
		if animate {
			name = "animated"
		}
		t.Run(name, func(t *testing.T) {
			win := newTestWindow(t, "zoom-000000000000000000000000000001", 40, 20)
			win.X, win.Y, win.Width, win.Height = 10, 5, 40, 20
			win.Tiled = false

			var mu sync.Mutex
			var lastW, lastH, calls int
			win.DaemonResizeFunc = func(w, h int) error {
				mu.Lock()
				lastW, lastH, calls = w, h, calls+1
				mu.Unlock()
				return nil
			}

			m := &OS{
				Settings:       config.Global,
				Windows:        []*terminal.Window{win},
				FocusedWindow:  0,
				WorkspaceFocus: map[int]int{},
				NumWorkspaces:  9,
				Width:          120,
				Height:         40,
			}
			m.Settings.ZoomAnimation = animate

			read := func() (int, int, int) {
				mu.Lock()
				defer mu.Unlock()
				return lastW, lastH, calls
			}
			// settle stands in for the frames a user would sit through.
			settle := func() { m.CompleteAllAnimations() }

			// Zoom in: the daemon must be told the enlarged content size.
			m.ToggleZoom()
			if !win.Zoomed {
				t.Fatal("window did not zoom")
			}
			if animate && len(m.Animations) == 0 {
				t.Fatal("the zoom did not slide, so the animated half of this test checks nothing")
			}
			settle()
			zw, zh, c1 := read()
			if c1 == 0 {
				t.Fatal("zoom-in never resized the daemon terminal")
			}
			wantW, wantH := win.ContentWidth(), win.ContentHeight()
			if zw != wantW || zh != wantH {
				t.Fatalf("zoom-in resized daemon to %dx%d, want %dx%d", zw, zh, wantW, wantH)
			}

			// Zoom out: the daemon must be told the restored content size
			// (38x18: the 40x20 box less its non-tiled border).
			m.ToggleZoom()
			if win.Zoomed {
				t.Fatal("window did not restore")
			}
			settle()
			rw, rh, c2 := read()
			if c2 == c1 {
				t.Fatal("zoom-out never resized the daemon terminal")
			}
			if rw != 38 || rh != 18 {
				t.Fatalf("zoom-out resized daemon to %dx%d, want 38x18", rw, rh)
			}
		})
	}
}
