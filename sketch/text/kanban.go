package text

import (
	"strconv"
	"strings"

	"tuios.dev/sketch"
)

// A kanban board is drawn from its lists: each column is a frame with its name
// and the count of its cards at the top, and the cards in boxes under it. The
// columns run across, and fold into more rows of columns when they are wider
// than the columns of the terminal.

func drawKanban(l *sketch.Layout, opt Options) (*canvas, error) {
	k := l.Kanban
	if k == nil {
		return nil, ErrUnsupported
	}
	type card struct {
		lines []string
		// label is the count of lines of the label; a line of ticket, assignee and priority may follow.
		label int
	}
	type column struct {
		head  []string
		cards []card
		w, h  int
	}
	cols := make([]column, len(k.Columns))
	for i, c := range k.Columns {
		col := column{head: append([]string{}, c.Label...)}
		count := " (" + strconv.Itoa(len(c.Cards)) + ")"
		if len(col.head) == 0 {
			col.head = []string{""}
		}
		col.head[len(col.head)-1] += count
		// The frame holds the cards one cell in from each side.
		w := widest(col.head) + 4
		h := len(col.head) + 2
		for _, item := range c.Cards {
			lines := append([]string{}, item.Label...)
			var meta []string
			for _, s := range []string{item.Ticket, item.Assigned, item.Priority} {
				if s != "" {
					meta = append(meta, s)
				}
			}
			if len(meta) > 0 {
				lines = append(lines, strings.Join(meta, " · "))
			}
			col.cards = append(col.cards, card{lines, len(item.Label)})
			w = max(w, widest(lines)+8)
			h += len(lines) + 2
		}
		col.w, col.h = w, h+1
		cols[i] = col
	}
	// Columns go into rows from the left while they fit.
	type place struct{ x, row int }
	at := make([]place, len(cols))
	x, row, wd := 0, 0, widest(k.Title)
	for i, col := range cols {
		if x > 0 && opt.Cols > 0 && x+col.w > opt.Cols {
			x, row = 0, row+1
		}
		at[i] = place{x, row}
		x += col.w + 1
		wd = max(wd, x-1)
	}
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}
	rowH := make([]int, row+1)
	for i, col := range cols {
		rowH[at[i].row] = max(rowH[at[i].row], col.h)
	}
	titleH := 0
	if len(k.Title) > 0 {
		titleH = len(k.Title) + 1
	}
	rowY := make([]int, row+1)
	height := titleH
	for r := range rowH {
		rowY[r] = height
		height += rowH[r] + 1
	}
	c, err := newCanvas(max(wd, 1), max(height, 1))
	if err != nil {
		return nil, err
	}
	for i, line := range k.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	for i, col := range cols {
		x0, y0 := at[i].x, rowY[at[i].row]
		c.box(x0, y0, col.w, rowH[at[i].row], nil, roleFrame, roleHead)
		for j, line := range col.head {
			c.text(x0+2, y0+1+j, line, roleHead)
		}
		y := y0 + len(col.head) + 2
		for _, cd := range col.cards {
			c.box(x0+2, y, col.w-4, len(cd.lines)+2, nil, roleBox, roleText)
			for j, line := range cd.lines {
				role := uint8(roleText)
				if j >= cd.label {
					role = roleNote
				}
				c.text(x0+4, y+1+j, line, role)
			}
			y += len(cd.lines) + 2
		}
	}
	return c, nil
}
