package text

import (
	"strings"

	"tuios.dev/sketch"
)

// An info diagram is its line of text, under its title when it has one. A title wider than the
// columns wraps.
func drawInfo(l *sketch.Layout, opt Options) (*canvas, error) {
	info := l.Info
	if info == nil {
		return nil, ErrUnsupported
	}
	title := info.Title
	if opt.Cols > 0 && widest(title) > opt.Cols {
		title = wrapCells(strings.Join(title, " "), opt.Cols, 1000)
	}
	w := max(widest(title), width(info.Text))
	if opt.Cols > 0 && w > opt.Cols {
		return nil, ErrTooWide
	}
	h := len(title) + 1
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}
	for y, line := range title {
		c.text((w-width(line))/2, y, line, roleTitle)
	}
	c.text((w-width(info.Text))/2, len(title), info.Text, roleNote)
	return c, nil
}
