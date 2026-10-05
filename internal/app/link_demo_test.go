package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// demoPanes builds the origin pane the bar paints into and the pane a sync
// would bring, the shape spendPendingLinkDemo meets.
func demoPanes(t *testing.T) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	var windows []*terminal.Window
	for i, id := range []string{"origin-0001", "demo-00001"} {
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		t.Cleanup(func() { close(done) })
		win := terminal.NewDaemonWindow(id, "test", i*50, 0, 40, 10, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(func() { win.Close() })
		windows = append(windows, win)
	}
	o := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		FocusedWindow:    1,
		Windows:          windows,
	}
	windows[0].Workspace = 1
	windows[1].Workspace = 1
	return o, windows[0], windows[1]
}

// TestSpendPendingLinkDemoPaintsTheBar proves the demo's two promises: focus
// returns to the pane the user was on, and that pane's buffer carries a
// tuios:// link to the demo pane with the client's own mark, which is what
// the plain-click path acts on. The paint is delayed past the shell redraw a
// spawn's retile causes, so the test runs it with a token delay and waits it
// out.
func TestSpendPendingLinkDemoPaintsTheBar(t *testing.T) {
	o, origin, demo := demoPanes(t)
	o.pendingLinkDemoOrigin = origin.ID

	prev := jumpLinkBarDelay
	jumpLinkBarDelay = time.Millisecond
	t.Cleanup(func() { jumpLinkBarDelay = prev })

	o.spendPendingLinkDemo([]*terminal.Window{demo})
	time.Sleep(50 * time.Millisecond)

	if o.FocusedWindow != 0 {
		t.Fatalf("focus = %d, want the origin pane refocused", o.FocusedWindow)
	}
	if o.pendingLinkDemoOrigin != "" {
		t.Fatal("demo intent survived the spend")
	}

	found := false
	for col := 0; col < 40; col++ {
		cell := paneCellAt(origin, col, 1)
		if cell != nil && cell.Link.URL == "tuios://window/"+demo.ID {
			if !o.LinkIsOurs(cell.Link.Params) {
				t.Fatal("the bar's link carries no client mark; a plain click would ignore it")
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no tuios:// link to the demo pane in the origin pane's buffer")
	}
}

// TestSpendPendingLinkDemoNeedsOneWindow checks the guard: a sync that did not
// bring exactly the pane the demo asked for paints nothing, whatever it did
// bring.
func TestSpendPendingLinkDemoNeedsOneWindow(t *testing.T) {
	o, origin, demo := demoPanes(t)

	painted := func() bool {
		for col := 0; col < 40; col++ {
			if cell := paneCellAt(origin, col, 1); cell != nil && cell.Link.URL != "" {
				return true
			}
		}
		return false
	}

	o.pendingLinkDemoOrigin = origin.ID
	o.spendPendingLinkDemo(nil)
	if painted() {
		t.Fatal("an empty sync painted the bar")
	}

	o.pendingLinkDemoOrigin = origin.ID
	o.spendPendingLinkDemo([]*terminal.Window{demo, demo})
	if painted() {
		t.Fatal("an ambiguous sync painted the bar")
	}
	if o.pendingLinkDemoOrigin != "" {
		t.Fatal("demo intent survived an ambiguous sync")
	}
}
