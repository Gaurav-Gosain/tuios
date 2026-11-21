package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

var paneLabelFont = map[rune][5]string{
	'0': {"###", "# #", "# #", "# #", "###"},
	'1': {" # ", "## ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'4': {"# #", "# #", "###", "  #", "  #"},
	'5': {"###", "#  ", "###", "  #", "###"},
	'6': {"###", "#  ", "###", "# #", "###"},
	'7': {"###", "  #", "  #", "  #", "  #"},
	'8': {"###", "# #", "###", "# #", "###"},
	'9': {"###", "# #", "###", "  #", "###"},
	'a': {"###", "# #", "###", "# #", "# #"},
	'b': {"## ", "# #", "## ", "# #", "## "},
	'c': {"###", "#  ", "#  ", "#  ", "###"},
	'd': {"## ", "# #", "# #", "# #", "## "},
	'e': {"###", "#  ", "## ", "#  ", "###"},
	'f': {"###", "#  ", "## ", "#  ", "#  "},
	'g': {"###", "#  ", "# #", "# #", "###"},
	'h': {"# #", "# #", "###", "# #", "# #"},
	'i': {"###", " # ", " # ", " # ", "###"},
	'j': {"  #", "  #", "  #", "# #", "###"},
	'k': {"# #", "# #", "## ", "# #", "# #"},
	'l': {"#  ", "#  ", "#  ", "#  ", "###"},
	'm': {"#   #", "## ##", "# # #", "#   #", "#   #"},
	'n': {"#  #", "## #", "# ##", "#  #", "#  #"},
	'o': {" # ", "# #", "# #", "# #", " # "},
	'p': {"###", "# #", "###", "#  ", "#  "},
	'q': {"###", "# #", "# #", "###", "  #"},
	'r': {"## ", "# #", "## ", "# #", "# #"},
	's': {"###", "#  ", "###", "  #", "###"},
	't': {"###", " # ", " # ", " # ", " # "},
	'u': {"# #", "# #", "# #", "# #", "###"},
	'v': {"# #", "# #", "# #", "# #", " # "},
	'w': {"#   #", "#   #", "# # #", "## ##", "#   #"},
	'x': {"# #", "# #", " # ", "# #", "# #"},
	'y': {"# #", "# #", " # ", " # ", " # "},
	'z': {"###", "  #", " # ", "#  ", "###"},
}

const paneLabelFontRows = 5

func paneLabelGlyphs(label string, scale int) []string {
	rows := make([]string, paneLabelFontRows)
	for i, r := range label {
		g, ok := paneLabelFont[r]
		if !ok {
			return nil
		}
		for y := range paneLabelFontRows {
			if i > 0 {
				rows[y] += strings.Repeat(" ", scale)
			}
			for _, c := range g[y] {
				rows[y] += strings.Repeat(string(c), scale)
			}
		}
	}
	return rows
}

type paneLabelInks struct {
	accent, ink, typed           color.Color
	reverse                      bool
	listGround, listInk, listKey color.Color
}

func paneLabelColors() paneLabelInks {
	pal := theme.UI()
	if theme.Depth() == overlay.Depth16 {
		return paneLabelInks{accent: pal.Accent, reverse: true, listKey: pal.Accent}
	}
	return paneLabelInks{
		accent:     pal.Accent,
		ink:        pal.PillFg,
		typed:      overlay.MixColors(pal.PillFg, pal.Accent, 0.6),
		listGround: pal.Surface,
		listInk:    pal.Fg,
		listKey:    overlay.Readable(pal.Accent, pal.Surface),
	}
}

func (k paneLabelInks) box() uv.Style {
	if k.reverse {
		return uv.Style{Fg: k.accent, Attrs: uv.AttrReverse}
	}
	return uv.Style{Bg: k.accent}
}

func (k paneLabelInks) glyph(typed bool) (string, uv.Style) {
	switch {
	case k.reverse && typed:
		return "▒", uv.Style{Fg: k.accent}
	case k.reverse:
		return " ", uv.Style{}
	case typed:
		return "█", uv.Style{Fg: k.typed, Bg: k.accent}
	}
	return "█", uv.Style{Fg: k.ink, Bg: k.accent}
}

func (k paneLabelInks) text(bold, typed bool) uv.Style {
	var st uv.Style
	if k.reverse {
		st = uv.Style{Fg: k.accent, Attrs: uv.AttrReverse}
		if typed {
			st.Attrs |= uv.AttrFaint
		}
	} else {
		st = uv.Style{Fg: k.ink, Bg: k.accent}
		if typed {
			st.Fg = k.typed
		}
	}
	if bold && !typed {
		st.Attrs |= uv.AttrBold
	}
	return st
}

func (m *OS) applyPaneLabels(canvas *frameCanvas, id string) {
	s := m.paneLabels
	if s == nil {
		return
	}
	p := s.paneLabelFor(id)
	if p == nil || !p.shown {
		return
	}
	w := m.windowByID(id)
	if w == nil {
		return
	}
	rect := m.paneLabelVisible(w)
	area := canvas.Bounds()
	if v := m.sessionView; v.on {
		rect = rect.Add(image.Pt(v.dx, v.dy))
		area = area.Intersect(v.clip)
	}
	clip := rect.Intersect(area)
	if clip.Empty() {
		return
	}
	inks := paneLabelColors()
	var hidden []paneLabel
	if s.listOn == id {
		hidden = s.hiddenPanes()
	}
	d := &paneLabelDraw{canvas: canvas, clip: clip}
	matches := s.typed == "" || strings.HasPrefix(p.label, s.typed)
	bottom := rect.Min.Y + rect.Dy()/2
	if matches {
		bottom = d.label(rect, p, s.typed, inks, len(hidden) > 0)
	}
	if len(hidden) > 0 {
		d.list(rect, bottom, hidden, s.typed, inks)
	}
}

type paneLabelDraw struct {
	canvas *frameCanvas
	clip   image.Rectangle
}

func (d *paneLabelDraw) set(x, y int, content string, width int, style uv.Style) {
	if !image.Pt(x, y).In(d.clip) || y >= len(d.canvas.Lines) || x >= len(d.canvas.Lines[y]) {
		return
	}
	d.canvas.Lines[y][x] = uv.Cell{Content: content, Width: width, Style: style}
}

func (d *paneLabelDraw) fill(r image.Rectangle, style uv.Style) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			d.set(x, y, " ", 1, style)
		}
	}
}

func (d *paneLabelDraw) text(x, y, width int, s string, style uv.Style) int {
	s = ansi.Truncate(s, width, "…")
	for _, r := range s {
		rw := max(ansi.StringWidth(string(r)), 0)
		if rw == 0 {
			continue
		}
		if x+rw > d.clip.Max.X {
			break
		}
		d.set(x, y, string(r), rw, style)
		for k := 1; k < rw; k++ {
			d.set(x+k, y, "", 0, style)
		}
		x += rw
	}
	return x
}

func (d *paneLabelDraw) repair(r image.Rectangle) {
	for y := max(r.Min.Y, d.clip.Min.Y); y < min(r.Max.Y, d.clip.Max.Y) && y < len(d.canvas.Lines); y++ {
		fixHintsWideCells(d.canvas.Lines[y], max(r.Min.X-1, d.clip.Min.X), min(r.Max.X+1, d.clip.Max.X))
	}
}

func (d *paneLabelDraw) label(rect image.Rectangle, p *paneLabel, typed string, inks paneLabelInks, roomBelow bool) int {
	name := strings.TrimSpace(p.name)
	reserve := 0
	if roomBelow {
		reserve = 2
	}
	for _, scale := range []int{2, 1} {
		glyphs := paneLabelGlyphs(p.label, scale)
		if glyphs == nil {
			break
		}
		gw := len(glyphs[0])
		bw, bh := gw+4, paneLabelFontRows+2
		withName := name != "" && rect.Dy() >= bh+2+reserve
		if withName {
			bh += 2
		}
		if bw > rect.Dx() || bh+reserve > rect.Dy() {
			continue
		}
		if withName {
			bw = min(max(bw, ansi.StringWidth(name)+4), rect.Dx())
		}
		box := centeredBox(rect, bw, bh, reserve)
		d.fill(box, inks.box())
		gx := box.Min.X + (bw-gw)/2
		typedEnd := 0
		if typed != "" && len(typed) < len(p.label) {
			typedEnd = len(paneLabelGlyphs(p.label[:len(typed)], scale)[0]) + scale
		}
		for y, row := range glyphs {
			for col, c := range []byte(row) {
				if c != '#' {
					continue
				}
				content, style := inks.glyph(col < typedEnd)
				d.set(gx+col, box.Min.Y+1+y, content, 1, style)
			}
		}
		if withName {
			nw := min(ansi.StringWidth(name), bw-4)
			d.text(box.Min.X+(bw-nw)/2, box.Max.Y-2, nw, name, inks.text(false, false))
		}
		d.repair(box)
		return box.Max.Y
	}
	line := " " + p.label + " "
	if name != "" {
		line += " " + name + " "
	}
	bw := min(ansi.StringWidth(line), rect.Dx())
	box := centeredBox(rect, bw, 1, min(reserve, max(rect.Dy()-1, 0)))
	d.fill(box, inks.box())
	x := box.Min.X + 1
	for i, r := range p.label {
		x = d.text(x, box.Min.Y, box.Max.X-x, string(r), inks.text(true, i < len(typed)))
	}
	if name != "" {
		d.text(x, box.Min.Y, box.Max.X-x, "  "+name, inks.text(false, false))
	}
	d.repair(box)
	return box.Max.Y
}

func (d *paneLabelDraw) list(rect image.Rectangle, top int, hidden []paneLabel, typed string, inks paneLabelInks) {
	top = max(top+1, rect.Min.Y)
	room := rect.Max.Y - top
	if room <= 0 {
		return
	}
	labelW := 0
	for _, p := range hidden {
		labelW = max(labelW, len(p.label))
	}
	rows := make([]paneLabel, 0, len(hidden))
	for _, p := range hidden {
		if typed == "" || strings.HasPrefix(p.label, typed) {
			rows = append(rows, p)
		}
	}
	if len(rows) == 0 {
		return
	}
	more := 0
	if len(rows) > room {
		more = len(rows) - (room - 1)
		rows = rows[:room-1]
	}
	width := 0
	for _, p := range rows {
		width = max(width, labelW+2+ansi.StringWidth(p.name))
	}
	if more > 0 {
		width = max(width, ansi.StringWidth(paneLabelsMore(more)))
	}
	width = min(width+4, rect.Dx())
	height := len(rows)
	if more > 0 {
		height++
	}
	x0 := rect.Min.X + (rect.Dx()-width)/2
	box := image.Rect(x0, top, x0+width, top+height)
	d.fill(box, uv.Style{Bg: inks.listGround})
	for i, p := range rows {
		y := box.Min.Y + i
		x := box.Min.X + 2
		key := p.label + strings.Repeat(" ", labelW-len(p.label))
		d.text(x, y, box.Max.X-2-x, key, uv.Style{Fg: inks.listKey, Bg: inks.listGround, Attrs: uv.AttrBold})
		x += labelW + 2
		d.text(x, y, box.Max.X-2-x, p.name, uv.Style{Fg: inks.listInk, Bg: inks.listGround})
	}
	if more > 0 {
		d.text(box.Min.X+2, box.Max.Y-1, width-4, paneLabelsMore(more), uv.Style{Fg: inks.listInk, Bg: inks.listGround, Attrs: uv.AttrFaint})
	}
	d.repair(box)
}

func paneLabelsMore(n int) string {
	if n == 1 {
		return "1 more pane"
	}
	return fmt.Sprintf("%d more panes", n)
}

func centeredBox(r image.Rectangle, w, h, reserve int) image.Rectangle {
	x := r.Min.X + (r.Dx()-w)/2
	y := r.Min.Y + max((r.Dy()-h-reserve)/2, 0)
	return image.Rect(x, y, x+w, y+h)
}
