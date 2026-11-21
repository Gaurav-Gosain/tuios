package text

import (
	"strings"

	sketch "tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/types"
)

type fishBlock struct {
	lines []treeLine
	w     int
	names int
}

func drawIshikawa(l *sketch.Layout, opt Options) (*canvas, error) {
	ik := l.Ishikawa
	if ik == nil {
		return nil, ErrUnsupported
	}
	if c := ishikawaBones(ik, opt); c != nil {
		return c, nil
	}
	return ishikawaTree(ik, opt)
}

func fishCauses(causes []types.GeoCause, above bool, cols int) []treeLine {
	rows := make([]types.GeoTreeRow, 0, len(causes)+2)
	rows = append(rows, types.GeoTreeRow{})
	for _, cause := range causes {
		rows = append(rows, types.GeoTreeRow{Depth: cause.Depth, Name: cause.Label})
	}
	if above {
		rows = append(rows, types.GeoTreeRow{Depth: 1})
	}
	last, _ := treeLast(rows)
	lines := treeLines(rows, last, 2, cols, false)[1:]
	if above {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		if lines[i].depth > 1 {
			lines[i].role = roleNote
		}
	}
	return lines
}

func ishikawaBones(ik *types.GeoIshikawa, opt Options) *canvas {
	if c := fishbone(ik, opt, 0); c != nil {
		return c
	}
	n := len(ik.Categories)
	if opt.Cols <= 0 || n == 0 {
		return nil
	}
	pairs := (n + 1) / 2
	room := (opt.Cols-2-2-width(ik.Effect)-3)/pairs - 3
	if room < 10 {
		return nil
	}
	return fishbone(ik, opt, room)
}

func fishbone(ik *types.GeoIshikawa, opt Options, room int) *canvas {
	n := len(ik.Categories)
	pairs := (n + 1) / 2
	blocks := make([]fishBlock, n)
	for i, cat := range ik.Categories {
		above := i%2 == 0
		var lines []treeLine
		names := []string{cat.Label}
		if room > 0 {
			names = wrapCells(cat.Label, room, 1000)
		}
		for _, name := range names {
			lines = append(lines, treeLine{text: name, role: seriesRole(i)})
		}
		lines = append(lines, fishCauses(cat.Causes, above, room)...)
		w := 0
		for _, line := range lines {
			w = max(w, width(line.prefix)+width(line.text))
		}
		blocks[i] = fishBlock{lines, w, len(names)}
	}
	upH, downH := 0, 0
	for i, b := range blocks {
		if i%2 == 0 {
			upH = max(upH, len(b.lines))
		} else {
			downH = max(downH, len(b.lines))
		}
	}
	colW := make([]int, pairs)
	for p := range pairs {
		for side := range 2 {
			if k := 2*p + side; k < n {
				colW[p] = max(colW[p], blocks[k].w)
			}
		}
		colW[p] += 3
	}
	x := 2
	colX := make([]int, pairs)
	for p := pairs - 1; p >= 0; p-- {
		colX[p] = x
		x += colW[p]
	}
	effect := " " + ik.Effect + " "
	spineEnd := x
	w := max(spineEnd+2+width(effect)+1, widest(ik.Title))
	if opt.Cols > 0 && w > opt.Cols {
		return nil
	}
	h := len(ik.Title) + upH + 1 + downH
	if int64(w)*int64(h) > MaxCells {
		return nil
	}
	c, err := newCanvas(w, h)
	if err != nil {
		return nil
	}
	for y, line := range ik.Title {
		c.text((w-width(line))/2, y, line, roleTitle)
	}
	spine := len(ik.Title) + upH
	c.hline(0, spineEnd, spine, heavy, roleLine)
	c.put(spineEnd+1, spine, '▶', roleLine)
	c.text(spineEnd+2, spine, effect, roleTitle)
	for i, b := range blocks {
		bx := colX[i/2]
		bone := seriesRole(i)
		if i%2 == 0 {
			top := spine - len(b.lines)
			for k, line := range b.lines {
				c.text(bx+width(line.prefix), top+k, line.text, line.role)
				if line.prefix != "" {
					c.text(bx, top+k, line.prefix, bone)
				}
			}
			for y := top + b.names; y < spine; y++ {
				if p := c.at(bx, y); p != nil && p.r == 0 {
					c.vline(bx, y, y+1, light, bone)
				}
			}
			c.stroke(bx, spine, up, light, bone)
		} else {
			top := spine + 1
			for k, line := range b.lines {
				c.text(bx+width(line.prefix), top+k, line.text, line.role)
				if line.prefix != "" {
					c.text(bx, top+k, line.prefix, bone)
				}
			}
			c.stroke(bx, spine, down, light, bone)
		}
	}
	return c
}

func ishikawaTree(ik *types.GeoIshikawa, opt Options) (*canvas, error) {
	rows := []types.GeoTreeRow{{Depth: 0, Name: ik.Effect, Directory: true}}
	for _, cat := range ik.Categories {
		rows = append(rows, types.GeoTreeRow{Depth: 1, Name: cat.Label, Directory: true})
		for _, cause := range cat.Causes {
			rows = append(rows, types.GeoTreeRow{Depth: cause.Depth + 1, Name: cause.Label})
		}
	}
	last, deepest := treeLast(rows)
	if int64(deepest+1)*4*int64(len(rows)) > MaxCells {
		return nil, ErrTooLarge
	}
	title := ik.Title
	if opt.Cols > 0 && widest(title) > opt.Cols {
		title = wrapCells(strings.Join(title, " "), opt.Cols, 1000)
	}
	lines := treeLines(rows, last, 2, 0, false)
	if opt.Cols > 0 && treeWidth(lines, title) > opt.Cols {
		lines = treeLines(rows, last, 2, opt.Cols, false)
		if treeWidth(lines, title) > opt.Cols {
			return nil, ErrTooWide
		}
	}
	c, err := newCanvas(max(treeWidth(lines, title), 1), len(title)+len(lines))
	if err != nil {
		return nil, err
	}
	for y, line := range title {
		c.text(0, y, line, roleTitle)
	}
	for k, line := range lines {
		y := len(title) + k
		c.text(0, y, line.prefix, roleLine)
		c.text(width(line.prefix), y, line.text, line.role)
	}
	return c, nil
}
