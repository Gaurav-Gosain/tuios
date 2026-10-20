package text

import "tuios.dev/sketch"

// narrower draws a graph with draw, and when that is too wide for opt.Cols, asks
// for layouts narrower than the one Draw asked for, so that a graph that runs
// across turns to run down, and tightens. A box in cells is often wider for its
// text than the layout made it, so a layout that fits cols*8 units can still be
// too wide.
func narrower(d *sketch.Diagram, l *sketch.Layout, opt Options, squeeze bool, draw func(*sketch.Layout, Options, bool) (*canvas, error)) (*canvas, error) {
	c, err := draw(l, opt, squeeze)
	if err != ErrTooWide || opt.Cols <= 0 {
		return c, err
	}
	for _, units := range []float64{6, 5, 4} {
		w := float64(opt.Cols) * units
		turn := true
		breakpoint := w + 1
		l, lerr := d.Layout(sketch.RenderOptions{MaxWidth: &w, AutoDirection: &turn, DirectionBreakpoint: &breakpoint})
		if lerr != nil {
			return nil, lerr
		}
		if c, err = draw(l, opt, squeeze); err != ErrTooWide {
			return c, err
		}
	}
	return c, err
}
