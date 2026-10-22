package text

import (
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A tree view is drawn as the tree command draws a folder: each entry on its own line, under its
// parent, joined to it by ├── and └── branches. A folder name ends in a slash and is bold. Descriptions line
// up in a column after the names. When that does not fit, a description follows its name, then
// the branches grow narrower, and last names and descriptions wrap under themselves.

// treeLine is one line of the drawing: the branches before the text, and the text.
type treeLine struct {
	prefix string
	text   string
	role   uint8
	// desc is a description that follows the text on the same line.
	desc string
	// depth is the depth of the row the line is part of.
	depth int
}

func drawTreeView(l *sketch.Layout, opt Options) (*canvas, error) {
	tv := l.TreeView
	if tv == nil {
		return nil, ErrUnsupported
	}
	// A folder's name ends in a slash.
	rows := make([]types.GeoTreeRow, len(tv.Rows))
	for i, row := range tv.Rows {
		rows[i] = row
		if row.Directory {
			rows[i].Name += "/"
		}
	}
	last, deepest := treeLast(rows)
	// Every line carries the branches of its depth.
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

// treeLines lays the rows out with branches `unit` cells wide, a directory's name in bold. With
// aligned set, descriptions start in one column. With cols set, a name or description wider than
// the room left wraps, and a description goes on the lines under its name.
func treeLines(rows []types.GeoTreeRow, last []bool, unit, cols int, aligned bool) []treeLine {
	branch := "├" + strings.Repeat("─", unit-2) + " "
	corner := "└" + strings.Repeat("─", unit-2) + " "
	pipe := "│" + strings.Repeat(" ", unit-1)
	blank := strings.Repeat(" ", unit)
	var out []treeLine
	// open[k]: the entry at depth k on the way down has a sibling still to come.
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
		// Lines under the name keep the guide down to the children.
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

// treeLast reports for each row whether no later sibling follows it, and the deepest depth. seen[d]
// is the latest row at depth d under the current parent; a row at depth d gives it a later sibling.
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
