package server

import (
	"testing"

	"charm.land/ssh"
)

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
