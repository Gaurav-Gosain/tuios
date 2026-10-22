package text

import (
	"math"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A wardley map is drawn as its grid: visibility up, evolution across, the stages as columns
// named under the axis. A component is a dot with its name beside it, an anchor a diamond, and a
// pipeline a square over its components. Links are lines of braille dots under the names, and an evolving
// component has a dashed line to an open circle where it is heading. Labelled links and notes are
// listed under the map. Under 40 columns the map is a table of its components and links.

func drawWardley(l *sketch.Layout, opt Options) (*canvas, error) {
	wm := l.Wardley
	if wm == nil {
		return nil, ErrUnsupported
	}
	if opt.Cols > 0 && opt.Cols < 40 {
		return wardleyTable(wm, opt)
	}
	plotW := 100
	if opt.Cols > 0 {
		plotW = min(plotW, opt.Cols-2)
	}
	plotH := max(12, min(30, 2*len(wm.Nodes)+4))
	// The plot starts at column 2, after the axis, and row top, under the title and the axis name.
	title := wm.Title
	if widest(title) > plotW+2 {
		title = wrapCells(strings.Join(title, " "), plotW+2, 1000)
	}
	var notes []string
	for _, link := range wm.Links {
		if link.Label != "" {
			notes = append(notes, wm.Nodes[link.From].Label+" → "+wm.Nodes[link.To].Label+": "+link.Label)
		}
	}
	notes = append(notes, wm.Notes...)
	var noteLines []string
	for _, n := range notes {
		noteLines = append(noteLines, wrapCells(n, plotW+2, 1000)...)
	}
	top := len(title) + 1
	w := plotW + 2
	h := top + plotH + 3 + len(noteLines)
	if len(noteLines) > 0 {
		h++
	}
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}
	for y, line := range title {
		c.text((w-width(line))/2, y, line, roleTitle)
	}
	c.text(0, top-1, "↑ Visibility", roleHead)
	axisY := top + plotH
	c.vline(1, top, axisY, light, roleFrame)
	c.hline(1, w-1, axisY, light, roleFrame)
	px := func(evolution float64) int {
		return 2 + int(math.Round(math.Max(0, math.Min(100, evolution))/100*float64(plotW-1)))
	}
	py := func(visibility float64) int {
		return top + int(math.Round((100-math.Max(0, math.Min(100, visibility)))/100*float64(plotH-1)))
	}
	// Stages: a dashed line at each boundary and the name under the middle of its column.
	n := len(wm.Stages)
	edge := func(k int) float64 {
		if len(wm.Boundaries) == n && k > 0 {
			return math.Max(0, math.Min(1, wm.Boundaries[k-1]))
		}
		return float64(k) / float64(max(n, 1))
	}
	stageY := axisY + 1
	for k, stage := range wm.Stages {
		x0, x1 := 2+int(edge(k)*float64(plotW-1)), 2+int(edge(k+1)*float64(plotW-1))
		if k > 0 {
			for y := top; y < axisY; y++ {
				c.stroke(x0, y, up|down, dotted, roleFrame)
			}
		}
		name := cut(stage, max(1, x1-x0-1))
		c.text(max(x0, (x0+x1-width(name))/2), stageY, name, roleNote)
	}
	evolution := "Evolution →"
	c.text(w-width(evolution), stageY+1, evolution, roleHead)

	at := make([][2]int, len(wm.Nodes))
	for i, node := range wm.Nodes {
		at[i] = [2]int{px(node.Evolution), py(node.Visibility)}
		// A pipeline sits on the row over its components.
		if node.Kind == "pipeline" && at[i][1] > top {
			at[i][1]--
		}
	}
	// Links are lines of braille dots from mark to mark, drawn last into the cells that
	// names, marks and stage lines leave free.
	links := newDotGrid(c.w, c.h)
	linkLayer := links.layer(roleLine)
	for _, link := range wm.Links {
		a, b := at[link.From], at[link.To]
		links.line(linkLayer, a[0]*2+1, a[1]*4+2, b[0]*2+1, b[1]*4+2, 1)
	}
	for _, trend := range wm.Trends {
		i := int(trend[0])
		if i < 0 || i >= len(at) {
			continue
		}
		from, to := at[i][0], px(trend[1])
		y := at[i][1]
		step := 1
		if to < from {
			step = -1
		}
		for x := from + step; x != to; x += step {
			c.put(x, y, '┄', roleNote)
		}
		c.put(to, y, '○', roleNote)
	}
	for i, node := range wm.Nodes {
		mark, role := '●', uint8(roleBox)
		switch node.Kind {
		case "anchor":
			mark, role = '◆', roleTitle
		case "pipeline":
			mark = '■'
		}
		c.put(at[i][0], at[i][1], mark, role)
	}
	// Names beside their marks: on the right where that is free, else on the left, else on the
	// next free row.
	free := func(x, y, n int) bool {
		if x < 2 || x+n > w || y < top || y >= axisY {
			return false
		}
		for k := 0; k < n; k++ {
			if p := c.at(x+k, y); p == nil || p.r != 0 {
				return false
			}
		}
		return true
	}
	for i, node := range wm.Nodes {
		label := cut(node.Label, plotW/2)
		lw := width(label)
		x, y := at[i][0], at[i][1]
		role := uint8(roleText)
		if node.Kind == "anchor" {
			role = roleTitle
		}
		placed := false
		rows := []int{0, -1, 1, -2, 2}
		if node.Kind == "pipeline-component" {
			// Under the mark, as the SVG writes it, so that the pipeline's own name has the row above.
			rows = []int{1, 0, 2, -1}
		}
		for _, dy := range rows {
			for _, x0 := range []int{x + 2, x - 1 - lw} {
				if free(x0-1, y+dy, lw+2) || (free(x0, y+dy, lw) && x0 == x+2) {
					c.text(x0, y+dy, label, role)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			c.text(min(x+2, w-lw), y, label, role)
		}
	}
	links.paint(c, 0, 0)
	for k, line := range noteLines {
		c.text(0, axisY+4+k, line, roleNote)
	}
	return c, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// wardleyTable lists the components with their visibility and evolution, then the links.
func wardleyTable(wm *types.GeoWardley, opt Options) (*canvas, error) {
	w := opt.Cols
	var lines []styled
	add := func(s string, role uint8, indent int) {
		for _, part := range wrapCells(s, max(1, w-indent), 1000) {
			lines = append(lines, styled{strings.Repeat(" ", indent) + part, role})
		}
	}
	for _, t := range wm.Title {
		add(t, roleTitle, 0)
	}
	add("Components", roleHead, 0)
	for _, node := range wm.Nodes {
		add(node.Label, roleBox, 2)
		v, e := "visibility "+vennSize(node.Visibility), "evolution "+vennSize(node.Evolution)
		if both := v + ", " + e; width(both)+4 <= w {
			add(both, roleNote, 4)
		} else {
			add(v, roleNote, 4)
			add(e, roleNote, 4)
		}
	}
	if len(wm.Links) > 0 {
		add("Links", roleHead, 0)
		for _, link := range wm.Links {
			s := wm.Nodes[link.From].Label + " → " + wm.Nodes[link.To].Label
			if link.Label != "" {
				s += ": " + link.Label
			}
			add(s, roleText, 2)
		}
	}
	for _, n := range wm.Notes {
		add(n, roleNote, 0)
	}
	cw := styledWidth(lines)
	if cw > w {
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
