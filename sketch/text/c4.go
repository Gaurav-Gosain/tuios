package text

import "tuios.dev/sketch"

// drawC4 draws a C4 diagram as a flowchart: people and systems as boxes, boundaries as
// groups, relations as routed lines. When it does not fit, it asks the layout for ever
// narrower widths: pele then stacks the shapes in fewer columns and wraps their text in
// narrower boxes.
func drawC4(d *sketch.Diagram, l *sketch.Layout, opt Options, ro sketch.RenderOptions, squeeze bool) (*canvas, error) {
	c, err := drawFlowchart(l, opt, squeeze)
	if err != ErrTooWide || ro.MaxWidth == nil || opt.Cols <= 0 {
		return c, err
	}
	// A cell is about 8 units of the layout; Draw tried 8 already.
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
