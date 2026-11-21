package text

import sketch "tuios.dev/sketch/internal/engine"

func drawC4(d *sketch.Diagram, l *sketch.Layout, opt Options, ro sketch.RenderOptions, squeeze bool) (*canvas, error) {
	c, err := drawFlowchart(l, opt, squeeze)
	if err != ErrTooWide || ro.MaxWidth == nil || opt.Cols <= 0 {
		return c, err
	}
	for _, units := range []float64{5, 3, 2} {
		w := float64(opt.Cols) * units
		narrow, lerr := d.Layout(sketch.RenderOptions{MaxWidth: &w})
		if lerr != nil {
			return nil, lerr
		}
		if c, err = drawFlowchart(narrow, opt, squeeze); err != ErrTooWide {
			return c, err
		}
	}
	return nil, ErrTooWide
}
