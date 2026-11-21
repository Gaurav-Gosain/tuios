package text

import (
	"strings"

	sketch "tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/types"
)

type treeLine struct {
	prefix string
	text   string
	role   uint8
	desc   string
	depth  int
}

func drawTreeView(l *sketch.Layout, opt Options) (*canvas, error) {
	tv := l.TreeView
	if tv == nil {
		return nil, ErrUnsupported
	}
	rows := make([]types.GeoTreeRow, len(tv.Rows))
	for i, row := range tv.Rows {
		rows[i] = row
		if row.Directory {
			rows[i].Name += "/"
		}
	}
	last, deepest := treeLast(rows)
	if int64(deepest+1)*4*int64(len(rows)) > MaxCells {
		return nil, ErrTooLarge
	}
	title := tv.Title
	if opt.Cols > 0 && widest(title) > opt.Cols {
		title = wrapCells(strings.Join(title, " "), opt.Cols, 1000)
	}
	var lines []treeLine
	fits := func(lines []treeLine) bool {
		return opt.Cols <= 0 || treeWidth(lines, title) <= opt.Cols
	}
	for _, unit := range []int{4, 3, 2} {
		if lines = treeLines(rows, last, unit, 0, true); fits(lines) {
			break
		}
		if lines = treeLines(rows, last, unit, 0, false); fits(lines) {
			break
		}
	}
	if !fits(lines) {
		lines = treeLines(rows, last, 2, opt.Cols, false)
		if !fits(lines) {
			return nil, ErrTooWide
		}
	}
	w := treeWidth(lines, title)
	if int64(w)*int64(len(lines)+len(title)) > MaxCells {
		return nil, ErrTooLarge
	}
	c, err := newCanvas(max(w, 1), max(len(title)+len(lines), 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for _, line := range title {
		c.text(0, y, line, roleTitle)
		y++
	}
	for _, line := range lines {
		c.text(0, y, line.prefix, roleLine)
		x := width(line.prefix)
		c.text(x, y, line.text, line.role)
		if line.desc != "" {
			c.text(x+width(line.text), y, line.desc, roleNote)
		}
		y++
	}
	return c, nil
}

func treeWidth(lines []treeLine, title []string) int {
	w := widest(title)
	for _, line := range lines {
		w = max(w, width(line.prefix)+width(line.text)+width(line.desc))
	}
	return w
}

func treeLines(rows []types.GeoTreeRow, last []bool, unit, cols int, aligned bool) []treeLine {
	branch := "├" + strings.Repeat("─", unit-2) + " "
	corner := "└" + strings.Repeat("─", unit-2) + " "
	pipe := "│" + strings.Repeat(" ", unit-1)
	blank := strings.Repeat(" ", unit)
	var out []treeLine
	var open []bool
	descCol := 0
	start := len(out)
	for i, row := range rows {
		open = append(open[:min(row.Depth, len(open))], !last[i])
		var ancestors strings.Builder
		for k := 1; k < row.Depth; k++ {
			if open[k] {
				ancestors.WriteString(pipe)
			} else {
				ancestors.WriteString(blank)
			}
		}
		prefix, under := ancestors.String(), ancestors.String()
		if row.Depth > 0 {
			if last[i] {
				prefix += corner
				under += blank
			} else {
				prefix += branch
				under += pipe
			}
		}
		if i+1 < len(rows) && rows[i+1].Depth > row.Depth {
			under += pipe
		} else {
			under += blank
		}
		name := row.Name
		role := uint8(roleText)
		if row.Directory {
			role = roleTitle
		}
		if row.Highlight {
			role = roleBox
		}
		if cols <= 0 {
			line := treeLine{prefix: prefix, text: name, role: role, depth: row.Depth}
			if row.Description != "" {
				line.desc = "  " + row.Description
			}
			out = append(out, line)
			descCol = max(descCol, width(prefix)+width(name))
			continue
		}
		room := max(1, cols-width(prefix))
		parts := wrapCells(name, room, 1000)
		if len(parts) == 0 {
			parts = []string{""}
		}
		for k, part := range parts {
			p := prefix
			if k > 0 {
				p = under
			}
			out = append(out, treeLine{prefix: p, text: part, role: role, depth: row.Depth})
		}
		if row.Description != "" {
			for _, part := range wrapCells(row.Description, max(1, cols-width(under)), 1000) {
				out = append(out, treeLine{prefix: under, text: part, role: roleNote, depth: row.Depth})
			}
		}
	}
	if aligned && cols <= 0 {
		for k := start; k < len(out); k++ {
			if line := &out[k]; line.desc != "" {
				line.desc = strings.Repeat(" ", descCol+2-width(line.prefix)-width(line.text)) + line.desc[2:]
			}
		}
	}
	return out
}

func treeLast(rows []types.GeoTreeRow) ([]bool, int) {
	last := make([]bool, len(rows))
	var seen []int
	deepest := 0
	for i, row := range rows {
		last[i] = true
		d := min(row.Depth, len(seen))
		if d < len(seen) {
			last[seen[d]] = false
		}
		seen = append(seen[:d], i)
		deepest = max(deepest, d)
	}
	return last, deepest
}
