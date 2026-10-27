package app

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

const (
	// pathPickerMinWidth and pathPickerMaxWidth bound the box's inner width.
	// It is a dropdown at a prompt: wide enough for a name and its icon, not
	// so wide it covers the pane.
	pathPickerMinWidth = 30
	pathPickerMaxWidth = 56
	// pathPickerChrome is the rows that are not list rows: the top border,
	// the rule, the filter line and the bottom border.
	pathPickerChrome = 4
	// pathPickerSearchGlyph is the Nerd Font magnifier on the filter line.
	pathPickerSearchGlyph = ""
)

// pathPickerAnchor is the focused pane's cursor on screen, or the first cell
// of its last row when the cursor is not on screen.
func (m *OS) pathPickerAnchor(w *terminal.Window) (int, int) {
	border := 1
	if w.Tiled {
		border = 0
	}
	x, y := w.X+border, w.Y+border+max(w.ContentHeight()-1, 0)
	if pos, _, ok := w.GuestCursor(); ok &&
		pos.X >= 0 && pos.X < w.ContentWidth() && pos.Y >= 0 && pos.Y < w.ContentHeight() {
		x, y = w.X+border+pos.X, w.Y+border+pos.Y
	}
	if v := m.sessionView; v.on {
		x, y = v.toScreen(x, y)
		x = clampInt(x, v.clip.Min.X, max(v.clip.Max.X-1, v.clip.Min.X))
		y = clampInt(y, v.clip.Min.Y, max(v.clip.Max.Y-1, v.clip.Min.Y))
	}
	return x, y
}

// pathPickerFloor is the smallest box the picker keeps inside the pane: this
// inner width and this many list rows. A pane smaller than that gets the box
// fitted to the screen instead, which is better than a box too small to read.
const (
	pathPickerFloorWidth = 16
	pathPickerFloorRows  = 3
)

// pathPickerRoom is the part of the screen the box must stay in: the focused
// pane's content area, as the screen shows it. A box fitted only to the screen
// ran over the rail and over the next pane's status line. ok is false when the
// pane is too small to hold the smallest box.
func (m *OS) pathPickerRoom(w *terminal.Window) (image.Rectangle, bool) {
	border := 1
	if w.Tiled {
		border = 0
	}
	x0, y0 := w.X+border, w.Y+border
	r := image.Rect(x0, y0, x0+w.ContentWidth(), y0+w.ContentHeight())
	if v := m.sessionView; v.on {
		ax, ay := v.toScreen(r.Min.X, r.Min.Y)
		r = r.Add(image.Pt(ax-r.Min.X, ay-r.Min.Y)).Intersect(v.clip)
	}
	r = r.Intersect(image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight()))
	ok := r.Dx() >= pathPickerFloorWidth+2 && r.Dy() >= pathPickerFloorRows+pathPickerChrome+1
	return r, ok
}

// placePathPicker draws the picker as a layer at the pane's cursor and records
// where it landed, so the mouse handlers can map a cell to a row.
func (m *OS) placePathPicker(layers []*lipgloss.Layer) []*lipgloss.Layer {
	p := m.pathPicker
	if p == nil {
		return layers
	}
	p.visible = false
	if !p.loaded {
		return layers
	}
	w := m.windowByID(p.windowID)
	if w == nil {
		m.pathPicker = nil
		return layers
	}
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	if rw <= 0 || rh <= 0 {
		return layers
	}
	ax, ay := m.pathPickerAnchor(w)
	room, ok := m.pathPickerRoom(w)
	if !ok {
		room = image.Rect(0, 0, rw, rh)
	}

	line := p.prefix() + p.query
	width := max(p.nameWidth+6, lipgloss.Width(line)+6, pathPickerMinWidth)
	width = min(width, pathPickerMaxWidth, max(room.Dx()-2, overlay.MinDialogWidth))

	// Above the cursor when the box fits there, as a shell's own completion
	// menu sits next to the line being typed; below it when it does not; and
	// on the roomier side, with fewer rows, when it fits on neither.
	rows := max(min(len(p.matches), pathPickerRows), 1)
	above, below := ay-room.Min.Y, room.Max.Y-ay-1
	y := 0
	switch need := rows + pathPickerChrome; {
	case above >= need:
		y = ay - need
	case below >= need:
		y = ay + 1
	case above >= below:
		rows = max(above-pathPickerChrome, 1)
		y = ay - rows - pathPickerChrome
	default:
		rows = max(below-pathPickerChrome, 1)
		y = ay + 1
	}

	content, geo := m.renderPathPicker(p, width, rows)
	// The filter text starts over the cursor's column: the border, the
	// magnifier and its space come first.
	x := clampInt(ax-3, room.Min.X, max(room.Max.X-geo.Width, room.Min.X))
	y = clampInt(y, room.Min.Y, max(room.Max.Y-geo.Height, room.Min.Y))

	p.visible = true
	p.boundsX, p.boundsY, p.boundsW, p.boundsH = x, y, geo.Width, geo.Height
	p.rowTop = y + 1
	return append(layers, lipgloss.NewLayer(content).
		X(x).Y(y).Z(config.ZIndexPathPicker).ID("path-picker"))
}

// renderPathPicker draws the box: the folder in the top border, the matches
// with the best one at the bottom next to the filter line, a rule, the filter
// line, and the keys in the bottom border.
func (m *OS) renderPathPicker(p *pathPicker, width, rows int) (string, overlay.Geometry) {
	pal := m.overlayUI()
	bg := pal.Canvas

	p.scroll = scrollWindow(p.scroll, p.cursor, len(p.matches), rows)
	p.rowIdx = p.rowIdx[:0]
	lines := make([]string, 0, rows+2)
	if len(p.matches) == 0 {
		msg := "Nothing matches"
		switch {
		case p.err != "":
			msg = p.err
		case p.loading:
			msg = "Reading the folder"
		case p.query == "" && len(p.entries) == 0:
			msg = "This folder is empty"
		case p.query == "":
			msg = "Only hidden files. Press alt+h to show them"
		}
		for range rows - 1 {
			lines = append(lines, overlay.Style(bg).Render(" "))
			p.rowIdx = append(p.rowIdx, -1)
		}
		ink := pal.FgDim
		if p.err != "" {
			ink = pal.Warning
		}
		lines = append(lines, overlay.Style(bg).Foreground(ink).Render("  "+overlay.Truncate(msg, width-2)))
		p.rowIdx = append(p.rowIdx, -1)
	} else {
		shown := min(rows, len(p.matches)-p.scroll)
		for range rows - shown {
			lines = append(lines, overlay.Style(bg).Render(" "))
			p.rowIdx = append(p.rowIdx, -1)
		}
		for i := p.scroll + shown - 1; i >= p.scroll; i-- {
			lines = append(lines, m.pathPickerRow(p, i, width, pal))
			p.rowIdx = append(p.rowIdx, i)
		}
	}
	lines = append(lines, overlay.DashRule(width, bg, pal), m.pathPickerFilterLine(p, width, pal))

	dialog := overlay.Dialog{
		Title: m.pathPickerTitle(p, width-3),
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: []overlay.Hint{
			{Key: "tab", Label: "open"},
			{Key: "⇧tab", Label: "up", Priority: overlay.HintOptional},
			{Key: overlay.EnterGlyph, Label: "type"},
			{Key: "esc", Label: "close"},
		},
	}
	return dialog.Render(pal)
}

// pathPickerTitle is the folder being listed, under the home folder of the
// pane's machine as ~/..., cut from the left so the end of the path, the part
// that tells folders apart, stays.
func (m *OS) pathPickerTitle(p *pathPicker, room int) string {
	dir := printableTitle(homeShort(p.dir, p.home))
	if p.loading {
		dir += " " + overlay.Ellipsis()
	}
	return truncateLeft(dir, room)
}

// truncateLeft cuts s to width cells from the left, with an ellipsis in front.
func truncateLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	ell := overlay.Ellipsis()
	keep := width - ansi.StringWidth(ell)
	if keep <= 0 {
		return ell
	}
	r := []rune(s)
	for len(r) > 0 && ansi.StringWidth(string(r)) > keep {
		r = r[1:]
	}
	return ell + string(r)
}

// pathPickerRow draws match i: the mark, the file type's icon, and the name,
// with a slash after a folder.
func (m *OS) pathPickerRow(p *pathPicker, i, width int, pal overlay.Palette) string {
	base := pal.Canvas
	e := p.entries[p.matches[i]]
	st := overlay.RowState{Cursor: i == p.cursor, Focused: true, Hover: i == p.hover}
	ground := pal.Ground(st, base)

	marker := "  "
	if st.Cursor {
		marker = overlay.Sigil()
	}
	out := overlay.Style(ground).Foreground(pal.Accent).Bold(true).Render(marker)

	s := &m.Settings
	if fileIconsOn(s) {
		mark := fileRowMark(e.Icon, e.Dir, false, s)
		ink := pal.FgDim
		if e.Dir {
			ink = pal.AccentBright
		}
		if mark.Hex != "" {
			ink = theme.FileIconInkOn(mark.Hex, ground)
		}
		out += overlay.Style(ground).Foreground(ink).Render(mark.Glyph) + overlay.Style(ground).Render(" ")
	}

	ink := pal.Fg
	if !st.Cursor && !st.Hover {
		ink = pal.FgDim
		if e.Dir {
			ink = pal.Fg
		}
	}
	name := printableTitle(e.Name)
	room := max(width-lipgloss.Width(out)-boolInt(e.Dir), 1)
	out += overlay.Style(ground).Foreground(ink).Bold(st.Cursor).Render(overlay.Truncate(name, room))
	if e.Dir {
		out += overlay.Style(ground).Foreground(pal.FgMute).Render("/")
	}
	return pal.Row(out, width, st, base)
}

// pathPickerFilterLine is the magnifier, the path walked so far, the filter
// text and the caret. A line longer than the box keeps its end.
func (m *OS) pathPickerFilterLine(p *pathPicker, width int, pal overlay.Palette) string {
	base := pal.Canvas
	glyph := overlay.SigilMark()
	if !m.Settings.NerdFontsOff() && !overlay.UseASCII() {
		glyph = pathPickerSearchGlyph
	}
	lead := overlay.Style(base).Foreground(pal.AccentBright).Render(glyph) + overlay.Style(base).Render(" ")
	room := max(width-lipgloss.Width(lead)-1, 1)
	prefix, query := printableTitle(p.prefix()), printableTitle(p.query)
	if ansi.StringWidth(prefix+query) > room {
		cut := truncateLeft(prefix+query, room)
		if ansi.StringWidth(query) >= room {
			prefix, query = "", cut
		} else {
			prefix = strings.TrimSuffix(cut, query)
		}
	}
	return lead +
		overlay.Style(base).Foreground(pal.Accent).Bold(true).Render(prefix) +
		overlay.Style(base).Foreground(pal.Fg).Render(query) +
		overlay.Cursor(" ", base, pal.Fg)
}
