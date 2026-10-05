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

// The pane labels' frame is a pass over the canvas right after each pane is
// drawn, as hints mode's is (hints_render.go). A pane drawn above another
// covers that pane's label, the way it covers the pane.
//
// A label is drawn as large as the pane allows: in the block font below at
// double width, then at single width, then as one row of text.

// paneLabelFont is a block font five rows high for the keys a label can
// hold. A '#' is a filled cell.
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

// paneLabelFontRows is the height of the block font.
const paneLabelFontRows = 5

// paneLabelGlyphs renders a label in the block font, each font cell scale
// columns wide, one blank font cell between keys. It returns nil when a key
// has no glyph.
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

// paneLabelInks are the styles a label is drawn in.
//
// At 16 colours the terminal's own slots are all there is, and a theme can
// put two of them close together: on a light theme the slot PillFg takes
// sits on the accent at well under 3:1. So the box there is the accent in
// reverse video, and the glyphs and the name are holes in it, drawn in the
// terminal's own background. That reads on any terminal, light or dark.
type paneLabelInks struct {
	accent, ink, typed color.Color
	// reverse is the 16-colour form above.
	reverse bool
	// list is the ground of the hidden panes' list, with its inks.
	listGround, listInk, listKey color.Color
}

// paneLabelColors works the inks out from the chrome palette.
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

// box is the style of an empty cell of the label's box.
func (k paneLabelInks) box() uv.Style {
	if k.reverse {
		return uv.Style{Fg: k.accent, Attrs: uv.AttrReverse}
	}
	return uv.Style{Bg: k.accent}
}

// glyph is a filled cell of the block font: a key typed already, or one
// still to type.
func (k paneLabelInks) glyph(typed bool) (string, uv.Style) {
	switch {
	case k.reverse && typed:
		// A shade of the accent, between the box and a hole.
		return "▒", uv.Style{Fg: k.accent}
	case k.reverse:
		return " ", uv.Style{}
	case typed:
		return "█", uv.Style{Fg: k.typed, Bg: k.accent}
	}
	return "█", uv.Style{Fg: k.ink, Bg: k.accent}
}

// text is the style of text in the box.
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

// applyPaneLabels draws the label of the pane whose layer id is id.
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
	rect := paneLabelRect(w)
	area := canvas.Bounds()
	// In a view of a larger session the pane is drawn shifted and clipped,
	// and its label goes with it. See pane_view.go.
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

// paneLabelDraw writes cells into the canvas inside clip.
type paneLabelDraw struct {
	canvas *frameCanvas
	clip   image.Rectangle
}

// set writes one cell, when it is inside the clip.
func (d *paneLabelDraw) set(x, y int, content string, width int, style uv.Style) {
	if !image.Pt(x, y).In(d.clip) || y >= len(d.canvas.Lines) || x >= len(d.canvas.Lines[y]) {
		return
	}
	d.canvas.Lines[y][x] = uv.Cell{Content: content, Width: width, Style: style}
}

// fill paints a box of blank cells in style.
func (d *paneLabelDraw) fill(r image.Rectangle, style uv.Style) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			d.set(x, y, " ", 1, style)
		}
	}
}

// text writes s from (x, y), at most width columns, and returns the column
// after it.
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

// repair fixes the wide glyphs of the pane's own text the label cut in two.
func (d *paneLabelDraw) repair(r image.Rectangle) {
	for y := max(r.Min.Y, d.clip.Min.Y); y < min(r.Max.Y, d.clip.Max.Y) && y < len(d.canvas.Lines); y++ {
		fixHintsWideCells(d.canvas.Lines[y], max(r.Min.X-1, d.clip.Min.X), min(r.Max.X+1, d.clip.Max.X))
	}
}

// label draws the label box in the middle of rect, as large as fits, and
// returns the row under it. roomBelow asks for a row to be left under the
// box for the hidden panes' list.
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
		// The keys already typed are quieter, so the next key to press is
		// the loud one. typedEnd is the column their glyphs end at.
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
	// Too small for the block font: one row, the label and the name.
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

// list draws the panes the frame does not draw, one row each, under the
// label at row top. A row is the label and the pane's name. What does not
// fit is counted on the last row.
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

// paneLabelsMore says how many hidden panes the list has no room for.
func paneLabelsMore(n int) string {
	if n == 1 {
		return "1 more pane"
	}
	return fmt.Sprintf("%d more panes", n)
}

// centeredBox is a w by h box in the middle of r. reserve rows are kept free
// under it, so the box moves up by half of them.
func centeredBox(r image.Rectangle, w, h, reserve int) image.Rectangle {
	x := r.Min.X + (r.Dx()-w)/2
	y := r.Min.Y + max((r.Dy()-h-reserve)/2, 0)
	return image.Rect(x, y, x+w, y+h)
}
