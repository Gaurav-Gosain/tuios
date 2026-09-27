package theme

import (
	"image/color"
	"os"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/colorprofile"
)

// colorProfile is the colour profile the frame reaches the terminal with.
// Zero is colorprofile.Unknown, which is what "not learned yet" looks like.
// Atomic because every session's render goroutine reads it.
var colorProfile atomic.Uint32

// SetColorProfile records the colour profile the frame is written through, and
// with it the depth the chrome is drawn for (see overlay.Depth). A client sets
// it from Bubble Tea's ColorProfileMsg, which is the same answer the frame
// writer steps colours down by; the servers set it from their session's
// environment.
//
// It is process-wide, like the theme. Under the ssh server that means the most
// recent connection's terminal decides the chrome for every connection, the
// same limit the theme and the border overrides have.
func SetColorProfile(p colorprofile.Profile) {
	colorProfile.Store(uint32(p))
	overlay.SetDepth(overlay.DepthOf(p))
}

// ColorProfile is the colour profile the frame is written through. Before a
// client has learned it, it is detected once from this process's stdout and
// environment, which is where the frame writer looks too.
func ColorProfile() colorprofile.Profile {
	if v := colorProfile.Load(); v != 0 {
		return colorprofile.Profile(v)
	}
	p := colorprofile.Detect(os.Stdout, os.Environ())
	if p == colorprofile.Unknown {
		p = colorprofile.NoTTY
	}
	SetColorProfile(p)
	return p
}

// Depth is the colour depth the chrome is drawn for.
func Depth() overlay.Depth {
	ColorProfile()
	return overlay.CurrentDepth()
}

// slotAt16 returns ANSI slot i at 16 colours and c at every other depth. It is
// for a chrome colour that stands for one of the theme's sixteen: at 16 colours
// the terminal paints the slot from its own palette, which is the user's
// colour, where c stepped down would be a guess from the xterm defaults.
func slotAt16(i int, c color.Color) color.Color {
	if Depth() == overlay.Depth16 {
		return overlay.Slot(i)
	}
	return c
}
