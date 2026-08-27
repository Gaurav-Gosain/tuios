package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestTwoSessionsKeepTheirOwnTerminal is the per-connection guarantee for
// capabilities: one server process holding two sessions must decide each
// session's graphics from the terminal that session is on.
//
// Before OS.Caps every one of these reads went to a process global that the
// last connection to arrive had overwritten, so an SSH client in kitty and one
// in xterm shared whichever terminal connected second. The third store below is
// that later connection, and nothing either session reads may move with it.
func TestTwoSessionsKeepTheirOwnTerminal(t *testing.T) {
	withClientCaps(t, &HostCapabilities{TerminalName: "seed"})

	kitty := &HostCapabilities{KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 22}
	plain := &HostCapabilities{KittyGraphics: false, TerminalName: "xterm", CellWidth: 7, CellHeight: 15}

	a := NewOS(OSOptions{Caps: kitty, UserConfig: config.DefaultConfig()})
	b := NewOS(OSOptions{Caps: plain, UserConfig: config.DefaultConfig()})
	t.Cleanup(func() { a.Cleanup(); b.Cleanup() })

	// A third client connects and installs its terminal process-wide, exactly
	// as the SSH handler does on every new connection.
	clientCapabilities.Store(&HostCapabilities{TerminalName: "dumb", CellWidth: 1, CellHeight: 1})

	if got := a.hostCaps().TerminalName; got != "kitty" {
		t.Errorf("session A reports terminal %q, want kitty", got)
	}
	if got := b.hostCaps().TerminalName; got != "xterm" {
		t.Errorf("session B reports terminal %q, want xterm", got)
	}

	// Cell size drives every measurement of an image against its pane, so a
	// session reading another's cell draws at the wrong scale.
	if w, h := a.hostCellSize(); w != 10 || h != 22 {
		t.Errorf("session A cell is %dx%d, want 10x22", w, h)
	}
	if w, h := b.hostCellSize(); w != 7 || h != 15 {
		t.Errorf("session B cell is %dx%d, want 7x15", w, h)
	}

	// The user-visible consequence: whether the screenshot preview may use the
	// pixel tier at all.
	a.PostRenderWriter = NewPostRenderWriter(os.Stdout)
	b.PostRenderWriter = NewPostRenderWriter(os.Stdout)
	if !a.screenshotGraphicsReady() {
		t.Error("session A is on kitty and must get the pixel tier")
	}
	if b.screenshotGraphicsReady() {
		t.Error("session B is on xterm and must not get the pixel tier")
	}

	// The passthroughs are built per session too, so their enable decision
	// cannot be re-made by a later connection either.
	if !a.KittyPassthrough.IsEnabled() {
		t.Error("session A's kitty passthrough must be enabled")
	}
	if b.KittyPassthrough.IsEnabled() {
		t.Error("session B's kitty passthrough must stay disabled")
	}
}
