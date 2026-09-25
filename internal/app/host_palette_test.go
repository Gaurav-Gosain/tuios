package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// A palette index cannot say what colour it is: only the terminal drawing it
// can. Everything tuios renders unthemed comes out as indices, so anything that
// turns a finished frame back into cells of its own has to ask the host or
// guess. It guessed with the xterm defaults, where index 4 is a navy so dark it
// is hard to read, and the screen saver over an unthemed session redrew every
// directory name in a blue the terminal had never shown.

// TestTheHostPaletteIsReadOutOfAProbe pins the parse against the reply shapes
// terminals actually send.
func TestTheHostPaletteIsReadOutOfAProbe(t *testing.T) {
	// Four hex digits per channel, string terminator: what xterm and ghostty
	// answer. Two digits and a BEL are both in use as well.
	response := "\x1b]4;0;rgb:0000/0000/0000\x1b\\" +
		"\x1b]4;4;rgb:3b3b/7878/ffff\x1b\\" +
		"\x1b]4;15;rgb:ff/ff/ff\x07" +
		"\x1b]10;rgb:c0c0/c8c8/d0d0\x1b\\" +
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x07"

	var caps HostCapabilities
	parseHostPalette(&caps, response)

	if caps.ANSIMask == 0 {
		t.Fatal("the probe read no palette at all")
	}
	if got, want := caps.ANSI[4], uint32(0x3b78ff); got != want {
		t.Errorf("index 4 came out %#06x, want %#06x", got, want)
	}
	if got, want := caps.ANSI[15], uint32(0xffffff); got != want {
		t.Errorf("index 15 came out %#06x, want %#06x: a two digit channel was misread", got, want)
	}
	if got, want := caps.ANSI[0], uint32(0x000000); got != want {
		t.Errorf("index 0 came out %#06x, want %#06x", got, want)
	}
	if !caps.HasFg || caps.Fg != 0xc0c8d0 {
		t.Errorf("the default foreground came out %#06x (set=%t), want %#06x", caps.Fg, caps.HasFg, 0xc0c8d0)
	}
	if !caps.HasBg || caps.Bg != 0x1e1e2e {
		t.Errorf("the default background came out %#06x (set=%t), want %#06x", caps.Bg, caps.HasBg, 0x1e1e2e)
	}
}

// TestAChannelIsScaledNotTruncated is the trap in the X11 colour format: the
// channels are as many hex digits as the terminal felt like sending, and
// rgb:f/0/0 is pure red. Reading the top two digits of that gives 0x0f, a red
// so dark it reads as black.
func TestAChannelIsScaledNotTruncated(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint8
	}{
		{"f", 255}, {"0", 0}, {"8", 136},
		{"ff", 255}, {"00", 0}, {"80", 128},
		{"ffff", 255}, {"0000", 0}, {"8080", 128},
	} {
		if got := scaleOSCChannel(c.in); got != c.want {
			t.Errorf("%q scaled to %d, want %d", c.in, got, c.want)
		}
	}
}

// TestASilentTerminalKeepsTheFallback pins that a host which answers nothing
// leaves the guess alone rather than painting the frame black.
func TestASilentTerminalKeepsTheFallback(t *testing.T) {
	fallback := shot.XTermPalette()
	var caps HostCapabilities
	parseHostPalette(&caps, "\x1b[?62;4c")

	got := hostPalette(&caps, fallback)
	if got != fallback {
		t.Error("a silent terminal replaced the fallback palette")
	}
}
