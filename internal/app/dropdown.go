package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

var ddHover = overlay.RowState{Hover: true}

type ddItem struct {
	label, note    string
	key            string
	current, muted bool
}

type dropdown struct {
	items          []ddItem
	list           listnav.State
	hover          int
	x0, y0, x1, y1 int
	rowY0, rows    int
}

func newDropdown(items []ddItem) *dropdown {
	d := &dropdown{items: items, hover: -1}
	n := 0
	for i := range d.items {
		if d.items[i].current {
			d.list.Cursor = i
		}
		if d.items[i].key == "" && n < 9 {
			n++
			d.items[i].key = strconv.Itoa(n)
		}
	}
	return d
}

type ddKey int

const (
	ddKeyMoved ddKey = iota
	ddKeyPicked
	ddKeyClosed
)

func (d *dropdown) Key(key string, closeOthers bool, keys *listnav.Keymap) (int, ddKey) {
	n := len(d.items)
	if n == 0 {
		return -1, ddKeyClosed
	}
	if delta, ok := listDelta(keys, key, listOverlayRows); ok {
		d.list.Move(delta, n, n, false, nil)
		d.hover = -1
		return -1, ddKeyMoved
	}
	switch key {
	case "enter", "space":
		return d.list.Cursor, ddKeyPicked
	case "esc":
		return -1, ddKeyClosed
	}
	for i, it := range d.items {
		if it.key == key {
			return i, ddKeyPicked
		}
	}
	if closeOthers {
		return -1, ddKeyClosed
	}
	return -1, ddKeyMoved
}

func (d *dropdown) RowAt(x, y int) (int, bool) {
	if d == nil || x < d.x0 || x >= d.x1 || y < d.y0 || y >= d.y1 {
		return -1, false
	}
	if r := y - d.rowY0; r >= 0 && r < d.rows {
		return d.list.Scroll + r, true
	}
	return -1, true
}

func (d *dropdown) Hover(x, y int) bool {
	row, _ := d.RowAt(x, y)
	if row == d.hover {
		return false
	}
	d.hover = row
	if row >= 0 {
		d.list.Cursor = row
	}
	return true
}

func drawDropdown(c *ckCanvas, l ckLook, d *dropdown, x, y, w, top0, bottom, top int) {
	pal := l.pal
	mark := l.g(ckGlyphPicked)
	labelW, noteW, keyW := 0, 0, 0
	for _, it := range d.items {
		labelW = max(labelW, ckWidth(ckSeg{t: it.label}))
		noteW = max(noteW, ckWidth(ckSeg{t: it.note}))
		keyW = max(keyW, ckWidth(ckSeg{t: it.key}))
	}
	inner := 2 + ckWidth(ckSeg{t: mark}) + 1 + labelW + 2 + keyW + 2
	if noteW > 0 {
		inner += noteW + 2
	}
	w = min(max(w, inner), c.w)

	below := bottom - (y + 1) - 2
	above := y - top0 - 2
	rows := min(len(d.items), max(below, above))
	if rows <= 0 {
		d.x0, d.y0, d.x1, d.y1, d.rows = 0, 0, 0, 0, 0
		return
	}
	x, y0, _ := overlay.Place(overlay.Rect{X0: x, Y0: y, X1: x + w, Y1: y + 1}, w, rows+2,
		overlay.Rect{X0: 0, Y0: top0, X1: c.w, Y1: bottom}, overlay.Placement{Side: overlay.SideBelow})
	d.list.Scroll = min(max(d.list.Cursor-rows/2, 0), len(d.items)-rows)
	c.fill(x, y0, w, rows+2, pal.Card)
	c.hline(x, y0, w, hzGlyph(), l.edge)
	c.hline(x, y0+rows+1, w, hzGlyph(), l.edge)
	for r := range rows {
		i := d.list.Scroll + r
		it := d.items[i]
		yy := y0 + 1 + r
		ink, tone, keyInk, ground := pal.Fg, pal.FgDim, pal.Accent, pal.Card
		if it.muted {
			ink, tone = pal.FgDim, pal.FgMute
		}
		switch {
		case i == d.list.Cursor:
			ink, tone, keyInk, ground = pal.PillFg, pal.PillFg, pal.PillFg, pal.Accent
		case i == d.hover:
			ground = pal.Ground(ddHover, pal.Card)
		}
		c.fill(x, yy, w, 1, ground)
		lead := " "
		if it.current {
			lead = mark
		}
		segs := []ckSeg{{t: lead + " ", fg: keyInk}, {t: ckPad(it.label, labelW) + "  ", fg: ink, attrs: ckBold}}
		if it.note != "" {
			segs = append(segs, ckSeg{t: it.note, fg: tone})
		}
		c.text(x+2, yy, x+w-2-keyW-1, segs...)
		c.textR(x+w-2, yy, ckSeg{t: it.key, fg: keyInk, attrs: ckBold})
		switch {
		case i == d.list.Cursor:
			c.mark(pal, x, yy, w, 1, ckOn)
		case i == d.hover:
			c.mark(pal, x, yy, w, 1, ddHover)
		}
	}
	d.x0, d.y0, d.x1, d.y1 = x, top+y0, x+w, top+y0+rows+2
	d.rowY0, d.rows = top+y0+1, rows
}
