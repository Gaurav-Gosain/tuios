package text

import (
	"strconv"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A Cynefin diagram is drawn as its 2×2 grid: complex and complicated above, chaotic and clear
// below, each domain with its name, its decision model, its kind of practice and its items.
// The line between chaotic and clear is heavy, for the cliff. Confusion is a box over the middle
// of the grid with its first three items, and the transitions are listed under the grid. When
// two columns of at least 14 cells do not fit, the domains are listed one after another.

// styled is one line of text in a role.
type styled struct {
	text string
	role uint8
}

// cynefinLines are a domain's lines at a width: its name, its description, and its items,
// each wrapped. With most set, it shows that many items and counts the rest.
func cynefinLines(d types.GeoDomain, w, most int) []styled {
	var out []styled
	add := func(s string, role uint8, indent string) {
		for k, part := range wrapCells(s, max(1, w-width(indent)), 1000) {
			if k > 0 {
				part = strings.Repeat(" ", width(indent)) + part
			} else {
				part = indent + part
			}
			out = append(out, styled{part, role})
		}
	}
	add(d.Name, roleTitle, "")
	for _, s := range []string{d.Model, d.Practice} {
		if s != "" {
			add(s, roleNote, "")
		}
	}
	items := d.Items
	if most > 0 && len(items) > most {
		items = items[:most]
	}
	for _, item := range items {
		add(item, roleText, "• ")
	}
	if len(items) < len(d.Items) {
		add("+"+strconv.Itoa(len(d.Items)-len(items))+" more", roleNote, "")
	}
	return out
}

func styledWidth(lines []styled) int {
	w := 0
	for _, l := range lines {
		w = max(w, width(l.text))
	}
	return w
}

func drawCynefin(l *sketch.Layout, opt Options) (*canvas, error) {
	cy := l.Cynefin
	if cy == nil || len(cy.Domains) != 5 {
		return nil, ErrUnsupported
	}
	var transitions []string
	for _, t := range cy.Transitions {
		line := t.From + " → " + t.To
		if t.Label != "" {
			line += ": " + t.Label
		}
		transitions = append(transitions, line)
	}
	title := cy.Title
	if opt.Cols > 0 && widest(title) > opt.Cols {
		title = wrapCells(strings.Join(title, " "), opt.Cols, 1000)
	}
	// Each half of the grid is as wide as its widest domain, or half the columns.
	natural := 0
	for _, d := range cy.Domains[:4] {
		natural = max(natural, styledWidth(cynefinLines(d, 1<<20, 0)))
	}
	half := max(natural+2, 14)
	if opt.Cols > 0 {
		half = min(half, (opt.Cols-3)/2)
	}
	if half < 14 {
		return cynefinList(cy, title, transitions, opt)
	}
	text := half - 2
	quad := make([][]styled, 4)
	for i, d := range cy.Domains[:4] {
		quad[i] = cynefinLines(d, text, 0)
	}
	confusion := cynefinLines(cy.Domains[4], min(2*half-6, max(natural, styledWidth(cynefinLines(cy.Domains[4], 1<<20, 3)))), 3)
	boxW := styledWidth(confusion) + 4
	boxH := len(confusion) + 2
	// Blank rows on each side of the middle line leave room for the confusion box.
	gap := (boxH + 1) / 2
	topH := max(len(quad[0]), len(quad[1])) + gap
	bottomH := max(len(quad[2]), len(quad[3])) + gap
	w := 2*half + 3
	h := len(title) + 1 + topH + 1 + bottomH + 1
	var tlines []string
	for _, t := range transitions {
		tlines = append(tlines, wrapCells(t, max(1, w), 1000)...)
	}
	h += len(tlines)
	if len(tlines) > 0 {
		h++
	}
	if opt.Cols > 0 && max(w, widest(title)) > opt.Cols {
		return cynefinList(cy, title, transitions, opt)
	}
	w = max(w, widest(title))
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}
	y := 0
	for _, line := range title {
		c.text((w-width(line))/2, y, line, roleTitle)
		y++
	}
	top := y
	mid := top + 1 + topH
	bottom := mid + 1 + bottomH
	edge := 2*half + 2
	c.hline(0, edge, top, light, roleFrame)
	c.hline(0, edge, mid, light, roleFrame)
	c.hline(0, edge, bottom, light, roleFrame)
	c.vline(0, top, bottom, light, roleFrame)
	c.vline(edge, top, bottom, light, roleFrame)
	c.vline(half+1, top, mid, light, roleFrame)
	// The cliff between chaotic and clear.
	c.vline(half+1, mid, bottom, heavy, roleLine)
	for i, lines := range quad {
		x := 2 + (i%2)*(half+1)
		y0 := top + 1
		if i >= 2 {
			y0 = mid + 1 + gap
		}
		for k, line := range lines {
			c.text(x, y0+k, line.text, line.role)
		}
	}
	bx := half + 1 - boxW/2
	by := mid - boxH/2
	for yy := by; yy < by+boxH; yy++ {
		for xx := bx; xx < bx+boxW; xx++ {
			c.clear(xx, yy)
		}
	}
	c.box(bx, by, boxW, boxH, nil, roleBox, roleText)
	// Join the grid's lines to the box.
	c.stroke(bx, mid, left, light, roleFrame)
	c.stroke(bx+boxW-1, mid, right, light, roleFrame)
	c.stroke(half+1, by, up, light, roleFrame)
	c.stroke(half+1, by+boxH-1, down, light, roleFrame)
	for k, line := range confusion {
		c.text(bx+2, by+1+k, line.text, line.role)
	}
	y = bottom + 2
	for _, line := range tlines {
		c.text(0, y, line, roleNote)
		y++
	}
	return c, nil
}

// cynefinList lists the domains one after another, for a width that has no room for the grid.
func cynefinList(cy *types.GeoCynefin, title, transitions []string, opt Options) (*canvas, error) {
	w := 1 << 20
	if opt.Cols > 0 {
		w = opt.Cols
	}
	var lines []styled
	for _, t := range title {
		lines = append(lines, styled{t, roleTitle})
	}
	for i, d := range cy.Domains {
		if i > 0 || len(title) > 0 {
			lines = append(lines, styled{})
		}
		for k, line := range cynefinLines(d, w-2, 0) {
			if k > 0 {
				line.text = "  " + line.text
			}
			lines = append(lines, line)
		}
	}
	if len(transitions) > 0 {
		lines = append(lines, styled{})
		for _, t := range transitions {
			for _, part := range wrapCells(t, w, 1000) {
				lines = append(lines, styled{part, roleNote})
			}
		}
	}
	cw := styledWidth(lines)
	if opt.Cols > 0 && cw > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(max(cw, 1), max(len(lines), 1))
	if err != nil {
		return nil, err
	}
	for y, line := range lines {
		c.text(0, y, line.text, line.role)
	}
	return c, nil
}
