package text

import sketch "tuios.dev/sketch/internal/engine"

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
