package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// fakeSipSession is the part of a browser connection the model builder reads.
type fakeSipSession struct {
	pty   sip.Pty
	slave *os.File
}

func (f *fakeSipSession) Pty() sip.Pty                         { return f.pty }
func (f *fakeSipSession) Context() context.Context             { return context.Background() }
func (f *fakeSipSession) Read([]byte) (int, error)             { return 0, nil }
func (f *fakeSipSession) Write(p []byte) (int, error)          { return len(p), nil }
func (f *fakeSipSession) Fd() uintptr                          { return 0 }
func (f *fakeSipSession) PtySlave() *os.File                   { return f.slave }
func (f *fakeSipSession) WindowChanges() <-chan sip.WindowSize { return nil }

// TestEphemeralWebSessionGetsTheBrowsersCell holds the ephemeral path to the
// rule the daemon path already kept: the session's capabilities describe the
// browser that connected, measured from its canvas, and not the placeholder
// installed at startup before any browser existed.
func TestEphemeralWebSessionGetsTheBrowsersCell(t *testing.T) {
	saved := webServerConfig
	t.Cleanup(func() { webServerConfig = saved })
	webServerConfig.ephemeral = true

	slave, err := os.Create(filepath.Join(t.TempDir(), "slave"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })

	// 15x24 cells, a size no fallback uses.
	sess := &fakeSipSession{pty: sip.Pty{Width: 80, Height: 24, WidthPx: 1200, HeightPx: 576}, slave: slave}
	model, ok := createTUIOSHandler(sess).(*app.OS)
	if !ok || model == nil {
		t.Fatal("the handler built no *app.OS")
	}
	t.Cleanup(model.Cleanup)

	if model.Caps == nil {
		t.Fatal("the ephemeral web session carries no capabilities of its own")
	}
	if model.Caps.CellWidth != 15 || model.Caps.CellHeight != 24 {
		t.Errorf("the ephemeral web session has a %dx%d cell, want the browser's 15x24",
			model.Caps.CellWidth, model.Caps.CellHeight)
	}
	if !model.Caps.KittyGraphics || model.Caps.TerminalName != "tuios-web" {
		t.Errorf("the ephemeral web session does not describe the browser terminal: %+v", model.Caps)
	}
}
