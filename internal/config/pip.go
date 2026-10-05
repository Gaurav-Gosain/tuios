package config

import "slices"

// PiPConfig is the [pip] section: the picture-in-picture view, a small live
// copy of one pane drawn in a corner of the screen while another pane has the
// focus.
//
// Nothing here is session state. The view is what this client's screen shows,
// like the spotlight, so a second client attached to the same session keeps
// its own view (or none).
type PiPConfig struct {
	// Width and Height are the size of the whole box, border included, in
	// cells (default: 40x12). The box shrinks to fit a smaller screen.
	Width  int `toml:"width"`
	Height int `toml:"height"`
	// Corner is where the box goes first (default: bottom-right). When the
	// focused pane's cursor enters the box, the box moves to another corner.
	Corner string `toml:"corner"`
	// X and Y pin the box to an exact spot, in cells from the screen's top
	// left, when both are set. Values outside the pane region are clamped at
	// draw. nil leaves the box in corner mode, where the cursor dodge keeps
	// it off the cursor. A right-press drag of the box sets them, and the
	// corner key puts the box back in corner mode.
	X *int `toml:"x"`
	Y *int `toml:"y"`
}

// The four corners, in the spelling [pip] corner takes.
const (
	PiPCornerBottomRight = "bottom-right"
	PiPCornerBottomLeft  = "bottom-left"
	PiPCornerTopRight    = "top-right"
	PiPCornerTopLeft     = "top-left"
)

// PiPCorners is what pip.corner accepts, shared by the registry, the validator
// and the settings page.
var PiPCorners = []string{PiPCornerBottomRight, PiPCornerBottomLeft, PiPCornerTopRight, PiPCornerTopLeft}

// PiP defaults and bounds. The minimum leaves a one-row, ten-column view inside
// the border, which is the least that still shows a line of output.
const (
	PiPDefaultWidth  = 40
	PiPDefaultHeight = 12
	PiPMinWidth      = 12
	PiPMaxWidth      = 200
	PiPMinHeight     = 3
	PiPMaxHeight     = 100
)

// defaultPiPConfig returns the section DefaultConfig carries.
func defaultPiPConfig() PiPConfig {
	return PiPConfig{Width: PiPDefaultWidth, Height: PiPDefaultHeight, Corner: PiPCornerBottomRight}
}

// fillMissingPiP fills absent and out-of-range values with the defaults.
func fillMissingPiP(cfg, defaultCfg *UserConfig) {
	p, d := &cfg.PiP, &defaultCfg.PiP
	if p.Width <= 0 {
		p.Width = d.Width
	}
	if p.Height <= 0 {
		p.Height = d.Height
	}
	p.Width = min(max(p.Width, PiPMinWidth), PiPMaxWidth)
	p.Height = min(max(p.Height, PiPMinHeight), PiPMaxHeight)
	if !validPiPCorner(p.Corner) {
		p.Corner = d.Corner
	}
}

// CornerName is the corner the box goes to first, defaulting to bottom-right
// for an empty or unknown value.
func (p PiPConfig) CornerName() string {
	if validPiPCorner(p.Corner) {
		return p.Corner
	}
	return PiPCornerBottomRight
}

// Size is the box size, border included, with the defaults and bounds
// applied, so a config that was never filled still draws a sane box.
func (p PiPConfig) Size() (w, h int) {
	w, h = p.Width, p.Height
	if w <= 0 {
		w = PiPDefaultWidth
	}
	if h <= 0 {
		h = PiPDefaultHeight
	}
	return min(max(w, PiPMinWidth), PiPMaxWidth), min(max(h, PiPMinHeight), PiPMaxHeight)
}

func validPiPCorner(c string) bool { return slices.Contains(PiPCorners, c) }
