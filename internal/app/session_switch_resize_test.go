package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSyncDaemonPTYDimensionsSkipsUnchanged pins N9 at the reattach seam: the
// PTY-dimension sync that runs on every session switch must issue zero resizes
// for panes whose size the daemon PTY already carries. Each resize is a SIGWINCH
// that repaints the shell's prompt, so a same-size switch that sends even one
// leaves a stacked prompt.
func TestSyncDaemonPTYDimensionsSkipsUnchanged(t *testing.T) {
	orig := config.Global.SharedBorders
	config.Global.SharedBorders = true
	t.Cleanup(func() { config.Global.SharedBorders = orig })

	m := &OS{Settings: config.Global, CurrentWorkspace: 1, Width: 120, Height: 40}

	var calls int
	mk := func(id string, w, h int) *terminal.Window {
		win := &terminal.Window{ID: id, Workspace: 1, Width: w, Height: h, Tiled: true, DaemonMode: true}
		win.DaemonResizeFunc = func(int, int) error { calls++; return nil }
		// The reattach path seeds the size the daemon PTY already carries.
		win.SeedAnnouncedSize(win.ContentWidth(), win.ContentHeight())
		return win
	}
	m.Windows = []*terminal.Window{mk("window-aaaa", 60, 40), mk("window-bbbb", 60, 40)}

	m.SyncDaemonPTYDimensions()
	if calls != 0 {
		t.Fatalf("same-size reattach issued %d PTY resizes, want 0", calls)
	}

	// A pane that genuinely lands at a new size still resizes, exactly once.
	m.Windows[0].Width = 80
	m.SyncDaemonPTYDimensions()
	if calls != 1 {
		t.Fatalf("changed-size pane issued %d PTY resizes, want 1", calls)
	}

	// And a second sync at the settled size announces nothing further.
	m.SyncDaemonPTYDimensions()
	if calls != 1 {
		t.Fatalf("settled sync re-announced (%d), want 1", calls)
	}
}
