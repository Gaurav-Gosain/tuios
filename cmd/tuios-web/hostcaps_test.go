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

// TestSipDefaultThemeIsRead pins the parse of the palette sip's page draws with
// when it is sent none. A sip upgrade that moves or reshapes the block fails
// here instead of quietly sending captures back to the xterm guess.
func TestSipDefaultThemeIsRead(t *testing.T) {
	th := sipDefaultTheme()
	if th.Foreground == "" || th.Background == "" {
		t.Fatalf("sip's default foreground and background were not found: %+v", th)
	}
	for i, c := range th.ANSI() {
		if _, ok := packHex(c); !ok {
			t.Errorf("ANSI slot %d of sip's default palette is %q, which does not parse", i, c)
		}
	}
}

// TestBrowserPaletteFollowsTheSentTheme checks the overlay: a colour the theme
// sends wins, and one it leaves empty keeps sip's.
func TestBrowserPaletteFollowsTheSentTheme(t *testing.T) {
	base := sipDefaultTheme()
	p := newBrowserPalette(sip.Theme{Red: "#123456"})

	if p.mask != 0xffff || !p.hasFg || !p.hasBg {
		t.Fatalf("the palette is missing slots: mask=%04x fg=%t bg=%t", p.mask, p.hasFg, p.hasBg)
	}
	if p.ansi[1] != 0x123456 {
		t.Errorf("slot 1 is %06x, want the theme's 123456", p.ansi[1])
	}
	if want, _ := packHex(base.Blue); p.ansi[4] != want {
		t.Errorf("slot 4 is %06x, want sip's own %06x", p.ansi[4], want)
	}
	if want, _ := packHex(base.Background); p.bg != want {
		t.Errorf("the background is %06x, want sip's own %06x", p.bg, want)
	}
}

// TestWebHostCapsCarriesThePalette is the known gap this closes: a capture of
// an unthemed web session resolves palette indices the way the browser does.
func TestWebHostCapsCarriesThePalette(t *testing.T) {
	saved := webPalette
	t.Cleanup(func() { webPalette = saved })
	webPalette = newBrowserPalette(sip.Theme{})

	caps := webHostCaps(10, 20)
	if caps.ANSIMask != 0xffff || !caps.HasFg || !caps.HasBg {
		t.Fatalf("webHostCaps reports no palette: mask=%04x fg=%t bg=%t", caps.ANSIMask, caps.HasFg, caps.HasBg)
	}
	if want, _ := packHex(sipDefaultTheme().Blue); caps.ANSI[4] != want {
		t.Errorf("slot 4 is %06x, want sip's %06x", caps.ANSI[4], want)
	}
}

func TestPackHex(t *testing.T) {
	tests := []struct {
		in   sip.Color
		want uint32
		ok   bool
	}{
		{"#89b4fa", 0x89b4fa, true},
		{"#89B4FA80", 0x89b4fa, true},
		{"#f00", 0xff0000, true},
		{"#f008", 0xff0000, true},
		{"", 0, false},
		{"89b4fa", 0, false},
		{"#zzzzzz", 0, false},
		{"#12345", 0, false},
	}
	for _, tt := range tests {
		got, ok := packHex(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("packHex(%q) = %06x, %t, want %06x, %t", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
