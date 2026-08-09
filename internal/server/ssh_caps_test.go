package server

import (
	"testing"

	"github.com/charmbracelet/ssh"
)

func TestBuildClientCapabilities_KittyFromTerm(t *testing.T) {
	// A kitty client forwards TERM=xterm-kitty in the pty-req even when no other
	// environment is forwarded. That alone must enable kitty graphics.
	caps := buildClientCapabilities("xterm-kitty", nil, ssh.Window{Width: 80, Height: 24})
	if !caps.KittyGraphics {
		t.Fatalf("expected kitty graphics enabled for TERM=xterm-kitty, got caps=%+v", caps)
	}
	if caps.TerminalName != "kitty" {
		t.Errorf("expected terminal name kitty, got %q", caps.TerminalName)
	}
}

func TestBuildClientCapabilities_GhosttyFromTerm(t *testing.T) {
	caps := buildClientCapabilities("xterm-ghostty", nil, ssh.Window{Width: 80, Height: 24})
	if !caps.KittyGraphics {
		t.Fatalf("expected kitty graphics enabled for ghostty, got caps=%+v", caps)
	}
}

func TestBuildClientCapabilities_WeztermFromTermProgram(t *testing.T) {
	// WezTerm often reports TERM=xterm-256color, so it can only be told apart by
	// a forwarded TERM_PROGRAM. It supports both kitty and sixel.
	env := []string{"TERM_PROGRAM=WezTerm"}
	caps := buildClientCapabilities("xterm-256color", env, ssh.Window{Width: 80, Height: 24})
	if !caps.KittyGraphics {
		t.Fatalf("expected kitty graphics enabled for WezTerm, got caps=%+v", caps)
	}
	if !caps.SixelGraphics {
		t.Errorf("expected sixel graphics enabled for WezTerm, got caps=%+v", caps)
	}
}

func TestBuildClientCapabilities_PlainXtermNoGraphics(t *testing.T) {
	// A plain xterm-256color client with nothing forwarded must NOT get kitty
	// graphics: forwarding APCs to a terminal that cannot render them corrupts
	// the screen. This is the case the old server-capabilities code got right
	// only by accident (headless server) and wrong when the server itself ran
	// in a graphics terminal.
	caps := buildClientCapabilities("xterm-256color", nil, ssh.Window{Width: 80, Height: 24})
	if caps.KittyGraphics {
		t.Fatalf("expected kitty graphics disabled for plain xterm, got caps=%+v", caps)
	}
}

func TestBuildClientCapabilities_EnvOverride(t *testing.T) {
	// An explicit client override wins over identity-based detection, both ways.
	on := buildClientCapabilities("xterm-256color", []string{"TUIOS_KITTY_GRAPHICS=1"}, ssh.Window{})
	if !on.KittyGraphics {
		t.Errorf("expected override to enable kitty, got %+v", on)
	}
	off := buildClientCapabilities("xterm-kitty", []string{"TUIOS_KITTY_GRAPHICS=0"}, ssh.Window{})
	if off.KittyGraphics {
		t.Errorf("expected override to disable kitty, got %+v", off)
	}
}

func TestBuildClientCapabilities_CellSizeFromPixels(t *testing.T) {
	// When the client sends pixel dimensions in the pty-req, the cell pixel size
	// must be derived from them. The pixel-mouse (DEC 1016) path depends on this
	// coming from the client, not the server.
	win := ssh.Window{Width: 80, Height: 24, WidthPixels: 800, HeightPixels: 480}
	caps := buildClientCapabilities("xterm-kitty", nil, win)
	if caps.CellWidth != 10 {
		t.Errorf("expected cell width 10 (800/80), got %d", caps.CellWidth)
	}
	if caps.CellHeight != 20 {
		t.Errorf("expected cell height 20 (480/24), got %d", caps.CellHeight)
	}
	if caps.PixelWidth != 800 || caps.PixelHeight != 480 {
		t.Errorf("expected pixel dims 800x480, got %dx%d", caps.PixelWidth, caps.PixelHeight)
	}
}

func TestBuildClientCapabilities_NoPixelsNoCellSize(t *testing.T) {
	// Most SSH clients send no pixel dimensions. We must not invent a cell size;
	// leaving it zero lets the daemon fall back to cell-based mouse reporting.
	caps := buildClientCapabilities("xterm-kitty", nil, ssh.Window{Width: 80, Height: 24})
	if caps.CellWidth != 0 || caps.CellHeight != 0 {
		t.Errorf("expected zero cell size without pixel dims, got %dx%d", caps.CellWidth, caps.CellHeight)
	}
}

func TestClientToHostCapabilities_NeverForwardsFiles(t *testing.T) {
	// The client is remote, so it cannot read a server-local file path. The host
	// capabilities projected for the passthrough must always report
	// KittyFileTransfer=false so file-medium transmissions are re-encoded as
	// direct data.
	in := buildClientCapabilities("xterm-kitty", nil, ssh.Window{Width: 80, Height: 24, WidthPixels: 800, HeightPixels: 480})
	host := clientToHostCapabilities(in)
	if host == nil {
		t.Fatal("expected non-nil host capabilities")
	}
	if host.KittyFileTransfer {
		t.Error("expected KittyFileTransfer=false for a remote SSH client")
	}
	if !host.KittyGraphics {
		t.Error("expected KittyGraphics carried through")
	}
	if host.CellWidth != 10 || host.CellHeight != 20 {
		t.Errorf("expected cell size carried through, got %dx%d", host.CellWidth, host.CellHeight)
	}
}

func TestClientToHostCapabilities_Nil(t *testing.T) {
	if clientToHostCapabilities(nil) != nil {
		t.Error("expected nil in, nil out")
	}
}
