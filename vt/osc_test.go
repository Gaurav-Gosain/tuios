package vt

import (
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestED2_RemovesOnScreenSemanticMarkers verifies that CSI 2J (clear) removes
// semantic markers referencing on-screen content so stale prompt/command
// markers do not survive a clear.
func TestED2_RemovesOnScreenSemanticMarkers(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Emit an on-screen prompt marker (AbsLine = scrollbackLen + cursorY = 0).
	e.Write([]byte("\x1b]133;A\x1b\\"))
	if e.semanticMarkers.Len() == 0 {
		t.Fatal("expected a semantic marker after OSC 133;A")
	}

	e.Write([]byte("\x1b[2J"))

	if got := e.semanticMarkers.Len(); got != 0 {
		t.Errorf("on-screen markers = %d after CSI 2J, want 0", got)
	}
}

// TestThemedSGR_UnderlineSubparamNoLeak verifies that an out-of-range underline
// subparameter (4:7) is consumed instead of leaking as a separate SGR 7
// (reverse video) on the themed SGR path.
func TestThemedSGR_UnderlineSubparamNoLeak(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Activate the themed path.
	var pal [16]color.Color
	for i := range pal {
		pal[i] = color.RGBA{R: uint8(i * 16), G: 0, B: 0, A: 255}
	}
	e.SetThemeColors(color.White, color.Black, color.White, pal)
	if !e.hasThemeColors() {
		t.Fatal("theme colors not active; test would exercise the wrong path")
	}

	e.Write([]byte("\x1b[4:7mX"))

	c := e.CellAt(0, 0)
	if c == nil {
		t.Fatal("no cell at (0,0)")
	}
	if c.Style.Attrs&uv.AttrReverse != 0 {
		t.Error("SGR 4:7 leaked a stray reverse-video attribute")
	}
	if c.Style.Underline != ansi.UnderlineNone {
		t.Errorf("SGR 4:7 unknown style should leave underline unset, got %v", c.Style.Underline)
	}
}

// TestThemedSGR_ColourShapesMatchReadStyle pins the colour forms the themed
// reader used to decide for itself. Every SGR now goes through it, so a shape
// it reads differently from uv.ReadStyle and xterm shows up in every pane.
//
// "38;0" is the implementation defined colour type: xterm, tmux and
// uv.ReadStyle consume the 0 as the type and leave the default colour. The
// themed reader skipped nothing when the colour came back nil, so the 0 was
// read as SGR 0 and wiped the bold set just before it.
//
// "38:5;7" mixes separators, which ansi.ReadStyleColor and ghostty take as no
// colour at all, so the 7 is SGR 7. The themed reader took any "38 5 n" with n
// under 16 as a palette colour whatever the separators.
func TestThemedSGR_ColourShapesMatchReadStyle(t *testing.T) {
	var pal [16]color.Color
	for i := range pal {
		pal[i] = color.RGBA{R: uint8(i * 16), A: 255}
	}
	cases := []struct {
		name  string
		in    string
		attrs uint8
		fg    color.Color
	}{
		{"38;0 consumes the colour type", "\x1b[1;38;0mX", uv.AttrBold, nil},
		{"48;0 consumes the colour type", "\x1b[1;48;0mX", uv.AttrBold, nil},
		{"mixed separators are not an indexed colour", "\x1b[38:5;7mX", uv.AttrReverse | uv.AttrBlink, nil},
		{"a palette index still resolves through the theme", "\x1b[38;5;3mX", 0, pal[3]},
		{"a colon palette index still resolves through the theme", "\x1b[38:5:3mX", 0, pal[3]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(8, 2)
			defer e.Close()
			e.SetThemeColors(color.White, color.Black, color.White, pal)
			if !e.hasThemeColors() {
				t.Fatal("theme colors not active; test would exercise the wrong path")
			}
			e.Write([]byte(tc.in))
			c := e.CellAt(0, 0)
			if c.Style.Attrs != tc.attrs {
				t.Errorf("attrs = %08b, want %08b", c.Style.Attrs, tc.attrs)
			}
			if c.Style.Fg != tc.fg {
				t.Errorf("fg = %v, want %v", c.Style.Fg, tc.fg)
			}
		})
	}
}

func TestOSC4_PaletteQuery(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Set a custom color for index 1
	e.Write([]byte("\x1b]4;1;rgb:ff/00/00\x1b\\"))

	// Query color index 1
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		responseChan <- string(buf[:n])
	}()

	e.Write([]byte("\x1b]4;1;?\x1b\\"))

	select {
	case response := <-responseChan:
		// Should contain a color response with index 1
		if !strings.Contains(response, "4;1;") {
			t.Errorf("expected response containing '4;1;', got %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for OSC 4 response")
	}
}
