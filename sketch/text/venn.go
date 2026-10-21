package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch"
)

// A venn diagram is drawn as a table, since circles of a few cells lose what is written in
// them: the sets, then the overlaps the source states, each with its label, its relative size,
// and the texts written in it. When a row does not fit the columns, its label and texts go on
// the lines under its name, and long text wraps.

// vennSize writes a relative size with at most three decimals.
func vennSize(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) >= 1e15 {
		return number(v)
	}
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}

// piece is a piece of a line of text at a column.
type piece struct {
	x    int
	text string
	role uint8
}

func drawVenn(l *sketch.Layout, opt Options) (*canvas, error) {
	vn := l.Venn
	if vn == nil {
		return nil, ErrUnsupported
	}
	type row struct {
		name, label, size string
		texts             []string
	}
	var sets, overlaps []row
	nameW, labelW, sizeW := 0, 0, len("Size")
	for _, r := range vn.Regions {
		rw := row{name: strings.Join(r.Sets, " ∩ "), label: r.Label, size: vennSize(r.Size), texts: r.Texts}
		if len(r.Sets) == 1 && rw.label == r.Sets[0] {
			rw.label = ""
		}
		nameW = max(nameW, width(rw.name))
		labelW = max(labelW, width(rw.label))
		sizeW = max(sizeW, width(rw.size))
		if len(r.Sets) == 1 {
			sets = append(sets, rw)
		} else {
			overlaps = append(overlaps, rw)
		}
	}
	// A row is two cells of indent, the name, the label and the size, two cells between each.
	labelX := 2 + nameW + 2
	sizeEnd := labelX + sizeW
	if labelW > 0 {
		sizeEnd = labelX + labelW + 2 + sizeW
	}
	inline := opt.Cols <= 0 || sizeEnd <= opt.Cols
	room := 1 << 20
	if opt.Cols > 0 {
		room = opt.Cols
	}
	var lines [][]piece
	wrapped := func(x int, s string, role uint8) {
		for _, part := range wrapCells(s, max(1, room-x), 1000) {
			lines = append(lines, []piece{{x, part, role}})
		}
	}
	for _, t := range vn.Title {
		wrapped(0, t, roleTitle)
	}
	section := func(head string, rows []row) {
		if len(rows) == 0 {
			return
		}
		if inline {
			lines = append(lines, []piece{{0, head, roleHead}, {sizeEnd - len("Size"), "Size", roleHead}})
		} else {
			lines = append(lines, []piece{{0, head, roleHead}})
		}
		for _, r := range rows {
			if inline {
				line := []piece{{2, r.name, roleBox}, {sizeEnd - width(r.size), r.size, roleLine}}
				if r.label != "" {
					line = append(line, piece{labelX, r.label, roleNote})
				}
				lines = append(lines, line)
			} else {
				wrapped(2, r.name+" ("+r.size+")", roleBox)
				if r.label != "" {
					wrapped(4, r.label, roleNote)
				}
			}
			for _, t := range r.texts {
				wrapped(4, "· "+t, roleText)
			}
		}
	}
	section("Sets", sets)
	section("Overlaps", overlaps)
	cw := 0
	for _, line := range lines {
		for _, s := range line {
			cw = max(cw, s.x+width(s.text))
		}
	}
	if opt.Cols > 0 && cw > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(max(cw, 1), max(len(lines), 1))
	if err != nil {
		return nil, err
	}
	for y, line := range lines {
		for _, s := range line {
			c.text(s.x, y, s.text, s.role)
		}
	}
	return c, nil
}
