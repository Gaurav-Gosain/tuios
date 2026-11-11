package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The one dropdown of the TUI chrome. The New agent sheet's fields and the
// agents view's header controls open the same list, drawn and driven here,
// so they look and behave the same:
//
//   - It opens under the row of its control when it fits there, and above it
//     otherwise, and scrolls to keep the cursor's row in view.
//   - Each row is the choice's label, a note in a quieter ink, and the key
//     that picks it at the right. The choice in force has the picked mark
//     before it, the one the sheet's radios use. The cursor's row is drawn
//     in the accent, and the row under the pointer on the hover ground.
//   - Up and down move, enter and space pick, a row's key picks it, and esc
//     closes. A click on a row picks it. A click outside the list closes it
//     and does nothing else: the click is spent on the dismissal.
//
// The host keeps a dropdown, fills its items when it opens, draws it last
// with drawDropdown, and asks it about keys, clicks and the pointer.

// ddHover is the state of the row under the pointer.
var ddHover = overlay.RowState{Hover: true}

// ddItem is one choice.
type ddItem struct {
	label, note string
	// key picks the row. Empty takes the next digit, up to nine.
	key string
	// current marks the choice in force; muted draws a choice that cannot
	// be used now in the quiet ink.
	current, muted bool
}

// dropdown is an open list and where it was last drawn.
type dropdown struct {
	items  []ddItem
	cursor int
	hover  int
	first  int
	// The box as last drawn, in screen cells, and the screen row of its
	// first item.
	x0, y0, x1, y1 int
	rowY0, rows    int
}

// newDropdown is an open list of items, with the cursor on the current one.
// It hands out the digit keys.
func newDropdown(items []ddItem) *dropdown {
	d := &dropdown{items: items, hover: -1}
	n := 0
	for i := range d.items {
		if d.items[i].current {
			d.cursor = i
		}
		if d.items[i].key == "" && n < 9 {
			n++
			d.items[i].key = strconv.Itoa(n)
		}
	}
	return d
}

// ddKey is what a key did to the list.
type ddKey int

const (
	// ddKeyMoved moved the cursor.
	ddKeyMoved ddKey = iota
	// ddKeyPicked picked the row Key returned.
	ddKeyPicked
	// ddKeyClosed closed the list with nothing picked.
	ddKeyClosed
)

// Key takes a key: up and down move, enter and space pick the cursor's row,
// a row's key picks it, esc closes. With closeOthers any other key closes the
// list too; without it, the key is the host's to handle (ddKeyMoved with no
// move is returned and the list stays).
//
// The movement keys are the lists' of [keybindings.lists] (keys). The list
// stops at its ends.
func (d *dropdown) Key(key string, closeOthers bool, keys *listnav.Keymap) (int, ddKey) {
	n := len(d.items)
	if n == 0 {
		return -1, ddKeyClosed
	}
	if delta, ok := listDelta(keys, key, listOverlayRows); ok {
		d.cursor, d.hover = min(max(d.cursor+delta, 0), n-1), -1
		return -1, ddKeyMoved
	}
	switch key {
	case "enter", "space":
		return d.cursor, ddKeyPicked
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

// RowAt is the row at screen cell x, y, and whether the cell is inside the
// list's box at all.
func (d *dropdown) RowAt(x, y int) (int, bool) {
	if d == nil || x < d.x0 || x >= d.x1 || y < d.y0 || y >= d.y1 {
		return -1, false
	}
	if r := y - d.rowY0; r >= 0 && r < d.rows {
		return d.first + r, true
	}
	return -1, true
}

// Hover lights the row under the pointer, moving the cursor to it, and
// reports whether the list changed.
func (d *dropdown) Hover(x, y int) bool {
	row, _ := d.RowAt(x, y)
	if row == d.hover {
		return false
	}
	d.hover = row
	if row >= 0 {
		d.cursor = row
	}
	return true
}

// drawDropdown draws d under the control at canvas column x, row y, w cells
// wide at the least: below it when the rows fit above bottom, above it
// otherwise, never above top0. top is the canvas's first row on the screen.
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
	x = max(min(x, c.w-w), 0)

	below := bottom - (y + 1) - 2
	above := y - top0 - 2
	rows := min(len(d.items), max(below, above))
	if rows <= 0 {
		d.x0, d.y0, d.x1, d.y1, d.rows = 0, 0, 0, 0, 0
		return
	}
	y0 := y + 1
	if rows > below {
		y0 = y - rows - 2
	}
	// The window keeps the cursor in view, centred when it can.
	d.first = min(max(d.cursor-rows/2, 0), len(d.items)-rows)
	c.fill(x, y0, w, rows+2, pal.Card)
	c.hline(x, y0, w, hzGlyph(), l.edge)
	c.hline(x, y0+rows+1, w, hzGlyph(), l.edge)
	for r := range rows {
		i := d.first + r
		it := d.items[i]
		yy := y0 + 1 + r
		ink, tone, keyInk, ground := pal.Fg, pal.FgDim, pal.Accent, pal.Card
		if it.muted {
			ink, tone = pal.FgDim, pal.FgMute
		}
		switch {
		case i == d.cursor:
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
		case i == d.cursor:
			c.mark(pal, x, yy, w, 1, ckOn)
		case i == d.hover:
			c.mark(pal, x, yy, w, 1, ddHover)
		}
	}
	d.x0, d.y0, d.x1, d.y1 = x, top+y0, x+w, top+y0+rows+2
	d.rowY0, d.rows = top+y0+1, rows
}
