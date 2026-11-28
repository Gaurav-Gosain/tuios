package app

import (
	"cmp"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The Files view's layout, top to bottom: the top bar, a blank row, the side
// (machine row, crumbs, the rule that says the side has the keys, the column
// names and the rows), the footer and the key hints. Quick look takes the
// right part of the body beside the side, or the whole body under 100
// columns. See file-explorer-design.md in the notes for the mocks.

const (
	// filesLookSplitW is the narrowest view that draws quick look beside the
	// list. Below it quick look takes the body.
	filesLookSplitW = 100
	// filesModifiedMin is the narrowest side that draws the Modified column,
	// and filesSizeMin the narrowest that draws the Size column on every row.
	filesModifiedMin = 60
	filesSizeMin     = 40
	filesSizeW       = 9
	filesModifiedW   = 9
)

// filesFrame is what the last frame was drawn for, and the frame.
type filesFrame struct {
	ok                bool
	gen               uint64
	w, h, top         int
	theme             uint64
	depth             overlay.Depth
	ascii, nerd, look bool
	reading           bool
	content           string
}

// filesGlyphs are the marks the view draws, in nerd font or plain form.
type filesGlyphs struct {
	files, here, host, crumb, rule, div, arrow string
}

func (m *OS) filesGlyphs() filesGlyphs {
	if overlay.UseASCII() {
		return filesGlyphs{files: "#", here: "*", host: "@", crumb: ">", rule: "=", div: "|", arrow: "<>"}
	}
	g := filesGlyphs{files: "", here: "", host: "", crumb: "›", rule: "━", div: "│", arrow: "←→"}
	if m.Settings.NoNerdFont {
		g.files, g.here, g.host = "■", "■", "■"
	}
	return g
}

// renderFilesView draws the view between the dock's rows. It answers the
// content, its screen row and its size.
func (m *OS) renderFilesView() (string, int, int, int) {
	e := &m.explorer
	res := m.viewReserve()
	w, top := m.GetRenderWidth(), res.Top
	h := m.GetRenderHeight() - res.Top - res.Bottom
	if w <= 0 || h <= 0 {
		return "", 0, 0, 0
	}
	pal := m.overlayUI()
	reading := false
	for _, s := range m.filesTabCur().sides {
		if s != nil && s.loading && time.Since(s.loadingAt) >= filesReadingAfter {
			reading = true
		}
	}
	key := filesFrame{ok: true, gen: e.gen, w: w, h: h, top: top, theme: theme.Generation(), depth: pal.Depth,
		ascii: overlay.UseASCII(), nerd: !m.Settings.NoNerdFont, look: m.quickLook.open && m.quickLook.files, reading: reading}
	f := e.frame
	f.content = ""
	if f == key && e.frame.content != "" {
		return e.frame.content, top, w, h
	}
	c := (*ckCanvas)(nil).reset(w, h, ckColor(pal.Canvas))
	m.drawFilesView(c, pal, w, h, top)
	key.content = c.render()
	e.frame = key
	return key.content, top, w, h
}

// drawFilesView draws the whole view on c: the top bar, the sides of the
// tab on screen and quick look, and the footer.
//
// Two sides are drawn side by side from filesLookSplitW columns. Below it
// the side with the keys is drawn alone, and tab flips to the other. Quick
// look takes the other side's place, or the whole body below
// filesLookSplitW.
func (m *OS) drawFilesView(c *ckCanvas, pal overlay.Palette, w, h, top int) {
	e := &m.explorer
	t := m.filesTabCur()
	g := m.filesGlyphs()
	e.tabsY = top
	m.drawFilesTopBar(c, pal, g, w, top)
	hintsY := h - 1
	copies := m.filesCopiesRows(h)
	body := filesBox{x: 0, y: 2, w: w, h: max(hintsY-2-copies, 0)}
	if copies > 0 {
		m.drawFilesCopies(c, pal, hintsY-copies, w, copies, top)
	}
	look := e.look && m.quickLook.open && m.quickLook.files
	for _, s := range t.sides {
		if s != nil {
			s.box = filesBox{}
		}
	}
	e.lookBox, e.divX = filesBox{}, -1
	split := w >= filesLookSplitW
	cur := t.cur()
	switch {
	case look && !split:
		e.lookBox = filesBox{x: 0, y: top + body.y, w: w, h: body.h}
	case look || (t.shown() && split):
		lw := w * t.ratio / 100
		if !t.shown() {
			lw = max(w*2/5, filesSizeMin)
		}
		left := filesBox{x: 0, y: body.y, w: lw, h: body.h}
		right := filesBox{x: lw + 1, y: body.y, w: w - lw - 1, h: body.h}
		for y := body.y; y < body.y+body.h; y++ {
			c.text(lw, y, 0, ckSeg{t: g.div, fg: pal.FgMute})
		}
		e.divX = lw
		switch {
		case look && t.shown():
			// Quick look takes the other side's place.
			mine, other := left, right
			if t.focus == 1 {
				mine, other = right, left
			}
			m.drawFilesSide(c, pal, g, cur, mine, top, true)
			e.lookBox = filesBox{x: other.x, y: top + other.y, w: other.w, h: other.h}
		case look:
			m.drawFilesSide(c, pal, g, cur, left, top, true)
			e.lookBox = filesBox{x: right.x, y: top + right.y, w: right.w, h: right.h}
		default:
			m.drawFilesSide(c, pal, g, t.sides[0], left, top, t.focus == 0)
			m.drawFilesSide(c, pal, g, t.sides[1], right, top, t.focus == 1)
		}
	default:
		m.drawFilesSide(c, pal, g, cur, body, top, true)
	}
	m.drawFilesDropMark(c, pal, top)
	if e.drag.dragging {
		m.drawFilesShelf(c, pal, hintsY, w, top)
		return
	}
	m.drawFilesHints(c, pal, hintsY, w, top)
}

// drawFilesTopBar draws the Files mark, the tabs and the switches of the
// side with the keys. It keeps each tab's columns for a click.
func (m *OS) drawFilesTopBar(c *ckCanvas, pal overlay.Palette, g filesGlyphs, w, top int) {
	e := &m.explorer
	s := m.filesCur()
	c.fill(0, 0, w, 1, ckColor(pal.Panel))
	x := c.text(1, 0, 0, ckSeg{t: g.files + " ", fg: pal.AccentBright}, ckSeg{t: "Files", fg: pal.Fg, attrs: ckBold})
	right := "sort " + s.sort.String()
	if s.reverse {
		right += " reversed"
	}
	if s.hidden {
		right += " · hidden on"
	} else {
		right += " · hidden off"
	}
	ink := pal.FgDim
	if sum := m.filesCopiesSummary(); sum != "" {
		// While copies move, the top bar says how many and how fast.
		right, ink = transferArrow()+" "+sum, pal.AccentBright
	}
	rx := w - 1 - ansi.StringWidth(right)
	c.textR(w-1, 0, ckSeg{t: right, fg: ink})
	x += 3
	e.tabHits = e.tabHits[:0]
	e.segHits = [2]filesTabHit{}
	split := w >= filesLookSplitW
	for i, t := range e.tabs {
		if i == e.tab && t.two && !split && t.sides[1] != nil {
			// One side is drawn at a time: the tab names both machines, and
			// the one on screen is lit. A click on the other one flips.
			x0 := x
			x = c.text(x, 0, rx-1, ckSeg{t: " " + itoaFiles(i+1) + "  ", fg: pal.Fg, bg: pal.Surface})
			for j := range 2 {
				if j == 1 {
					x = c.text(x, 0, rx-1, ckSeg{t: " " + g.arrow + " ", fg: pal.FgMute, bg: pal.Surface})
				}
				name, _ := m.filesMachineOf(t.sides[j])
				ink, attrs := pal.FgDim, uint8(0)
				if j == t.focus {
					ink, attrs = pal.Fg, ckBold
				}
				s0 := x
				x = c.text(x, 0, rx-1, ckSeg{t: name, fg: ink, bg: pal.Surface, attrs: attrs})
				e.segHits[j] = filesTabHit{x0: s0, x1: x}
			}
			x = c.text(x, 0, rx-1, ckSeg{t: " ", bg: pal.Surface})
			e.tabHits = append(e.tabHits, filesTabHit{x0: x0, x1: x})
			x++
			continue
		}
		label := " " + itoaFiles(i+1) + "  " + m.filesTabName(t, g.arrow) + " "
		label = ckTrunc(label, max(rx-x-2, 0))
		if label == "" {
			break
		}
		ink, bg := pal.FgDim, ckColor(nil)
		if i == e.tab {
			ink, bg = pal.Fg, pal.Surface
		}
		x0 := x
		x = c.text(x, 0, rx-1, ckSeg{t: label, fg: ink, bg: bg})
		e.tabHits = append(e.tabHits, filesTabHit{x0: x0, x1: x})
		x++
	}
}

// itoaFiles is n in decimal, for the tab numbers.
func itoaFiles(n int) string {
	if n >= 0 && n < 10 {
		return string(rune('0' + n))
	}
	return strconv.Itoa(n)
}

// drawFilesSide draws side s in box b. top is the screen row of the canvas,
// for the click targets. keys says the side has the keys: its rule is in
// the accent and its cursor row in the cursor's ground, and the other side
// keeps its cursor in the quieter ground.
func (m *OS) drawFilesSide(c *ckCanvas, pal overlay.Palette, g filesGlyphs, s *filesSide, b filesBox, top int, keys bool) {
	if s == nil {
		return
	}
	s.box = filesBox{x: b.x, y: top + b.y, w: b.w, h: b.h}
	x0, right := b.x+1, b.x+b.w-1
	y := b.y
	reading := s.loading && time.Since(s.loadingAt) >= filesReadingAfter
	// The machine row.
	c.fill(b.x, y, b.w, 2, ckColor(pal.Panel))
	name, here := m.filesMachineOf(s)
	glyph, where := g.host, "linked"
	if here {
		glyph, where = g.here, "this machine"
	}
	x := c.text(x0, y, right, ckSeg{t: glyph + " ", fg: pal.AccentBright}, ckSeg{t: name, fg: pal.Fg, attrs: ckBold})
	c.text(x+1, y, right, ckSeg{t: where, fg: pal.FgDim})
	switch {
	case reading:
		c.textR(right, y, ckSeg{t: "reading", fg: pal.FgDim})
	case s.Err != "" && s.Dir != "" && len(s.Entries) > 0:
		c.textR(right, y, ckSeg{t: ckTrunc(s.Err, max(b.w/2, 10)), fg: pal.Warning})
	}
	// The crumbs.
	y++
	s.crumbsY = top + y
	s.crumbs = s.crumbs[:0]
	sortW := c.textR(right, y, ckSeg{t: s.sort.String(), fg: pal.FgDim})
	m.drawFilesCrumbs(c, pal, g, s, x0, y, sortW-2)
	// The rule: the side with the keys has the accent.
	y++
	rule := pal.Accent
	if !keys {
		rule = pal.FgMute
	}
	c.hline(b.x, y, b.w, g.rule, rule)
	y++
	// The columns. Under 80 columns the view draws the names alone, and the
	// size of the cursor row only.
	narrow := m.GetRenderWidth() < 80
	nameX := x0 + 1
	sizeR, modR := right, right
	showMod := b.w >= filesModifiedMin && !narrow
	showSize := b.w >= filesSizeMin && !narrow
	if showMod {
		sizeR = right - filesModifiedW - 2
		c.textR(modR, y, ckSeg{t: "Modified", fg: pal.FgMute})
	}
	if showSize {
		c.textR(sizeR, y, ckSeg{t: "Size", fg: pal.FgMute})
	}
	c.text(nameX, y, 0, ckSeg{t: "Name", fg: pal.FgMute})
	y++
	footY := b.y + b.h - 1
	rows := max(footY-1-y, 0)
	s.rowsTop, s.rowsN = top+y, rows
	nameW := right - nameX
	if showSize {
		nameW = sizeR - filesSizeW - 1 - nameX
	}
	// The git marks take the end of the name column. See files_view_git.go.
	nameW -= m.filesGitMarkW(s)
	m.drawFilesRows(c, pal, s, b, y, rows, nameX, nameW, sizeR, modR, showSize, showMod, keys)
	m.drawFilesFooter(c, pal, s, x0, right, footY, keys)
}

// drawFilesCrumbs draws the folder as crumbs from x, up to limit, and keeps
// each crumb's columns for a click. A long path loses its first crumbs.
func (m *OS) drawFilesCrumbs(c *ckCanvas, pal overlay.Palette, g filesGlyphs, s *filesSide, x, y, limit int) {
	dir := s.Dir
	if dir == "" {
		dir = s.want
	}
	type crumb struct{ label, dir string }
	var parts []crumb
	home := s.home
	rest := dir
	if home != "" && (dir == home || strings.HasPrefix(dir, home+"/")) {
		parts = append(parts, crumb{"~", home})
		rest = strings.TrimPrefix(dir[len(home):], "/")
	} else if strings.HasPrefix(dir, "/") {
		parts = append(parts, crumb{"/", "/"})
		rest = strings.TrimPrefix(dir, "/")
	}
	at := strings.TrimSuffix(dir, rest)
	if rest != "" {
		for p := range strings.SplitSeq(rest, "/") {
			if p == "" {
				continue
			}
			at = strings.TrimSuffix(at, "/") + "/" + p
			if at[0] != '/' && len(parts) == 0 {
				at = p
			}
			parts = append(parts, crumb{p, at})
		}
	}
	sep := " " + g.crumb + " "
	width := func(from int) int {
		n := 0
		for i := from; i < len(parts); i++ {
			if i > from {
				n += ansi.StringWidth(sep)
			}
			n += ansi.StringWidth(parts[i].label)
		}
		return n
	}
	from := 0
	for from < len(parts)-1 && x+width(from)+2 > limit {
		from++
	}
	if from > 0 {
		x = c.text(x, y, limit, ckSeg{t: overlay.Ellipsis() + " ", fg: pal.FgMute})
	}
	for i := from; i < len(parts); i++ {
		if i > from {
			x = c.text(x, y, limit, ckSeg{t: sep, fg: pal.FgMute})
		}
		ink, attrs := pal.FgDim, uint8(0)
		if i == len(parts)-1 {
			ink, attrs = pal.Fg, ckBold
		}
		x0 := x
		x = c.text(x, y, limit, ckSeg{t: parts[i].label, fg: ink, attrs: attrs})
		s.crumbs = append(s.crumbs, filesCrumb{dir: parts[i].dir, x0: x0, x1: x})
	}
}

// drawFilesRows draws the rows of the listing and, after them, the copies
// on their way into the folder; or the one row that says why there are no
// rows.
func (m *OS) drawFilesRows(c *ckCanvas, pal overlay.Palette, s *filesSide, b filesBox, y, rows, nameX, nameW, sizeR, modR int, showSize, showMod, keys bool) {
	note := func(text string, warn bool) {
		ink := pal.FgMute
		if warn {
			ink = pal.Warning
		}
		c.text(nameX, y, b.x+b.w-1, ckSeg{t: text, fg: ink})
	}
	arriving := m.filesArrivals(s)
	switch {
	case s.Err != "" && len(s.Entries) == 0:
		note(s.Err, true)
		return
	case s.Dir == "":
		name, _ := m.filesMachineOf(s)
		note("reading "+name+":"+s.short(s.want), false)
		return
	case len(s.rows) == 0 && s.filter != "":
		note("No name has "+s.filter+" in it.", false)
		return
	case len(s.rows) == 0 && len(arriving) == 0:
		note("Nothing here", false)
		return
	}
	n := len(s.rows)
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+rows {
		s.scroll = s.cursor - rows + 1
	}
	s.scroll = max(min(s.scroll, n-rows), 0)
	now := time.Now()
	e := &m.explorer
	renaming := keys && e.prompt == filesPromptRename
	drawn := 0
	for i := 0; i < rows && s.scroll+i < n; i++ {
		at := s.scroll + i
		idx := s.rows[at]
		en := s.Entries[idx]
		ry := y + i
		drawn++
		// The side without the keys keeps its cursor in the quieter ground,
		// drawn here under a row that draws none of its own.
		st := sidebarRowState{Cursor: at == s.cursor && keys}
		switch {
		case st.Cursor:
			c.fill(b.x, ry, b.w, 1, ckColor(pal.RowSel))
		case at == s.cursor:
			c.fill(b.x, ry, b.w, 1, ckColor(cmp.Or[color.Color](pal.RowSelQuiet, pal.RowSel)))
		}
		if renaming && at == s.cursor {
			// The name is edited in its row.
			c.text(nameX+1, ry, nameX+nameW, ckSeg{t: e.input, fg: pal.Fg, attrs: ckBold}, ckSeg{t: "▏", fg: pal.Accent})
		} else {
			c.draw(nameX-1, ry, nameW+1, m.sidebarFileRow(fileRowSpec{Kind: sidebarRowFileEntry, Name: en.Name, Dir: en.Dir, Icon: en.Icon}, nameW+1, pal, st))
		}
		m.filesGitDrawMark(c, pal, s, en, nameX+nameW, ry)
		if s.marked(en.Name) {
			// A marked row has the accent bar in the gutter and its name in
			// the accent.
			c.text(b.x, ry, b.x+1, ckSeg{t: "▌", fg: pal.Accent})
			c.restyle(nameX+1, ry, nameW, 1, func(cell *uv.Cell) { cell.Style.Fg = ckColor(pal.AccentBright) })
		}
		if idx >= len(s.infos) {
			continue
		}
		in := s.infos[idx]
		if (showSize || at == s.cursor) && !en.Dir && in.Kind != "" {
			c.textR(sizeR, ry, ckSeg{t: units.Bytes(in.Size), fg: pal.FgDim})
		}
		if _, ok := s.landed[en.Name]; ok && showMod {
			c.textR(modR, ry, ckSeg{t: filesChecked(), fg: pal.Success})
		} else if showMod && in.MTime > 0 {
			c.textR(modR, ry, ckSeg{t: agentElapsed("file", in.MTime*int64(time.Millisecond), now) + " ago", fg: pal.FgMute})
		}
	}
	// The copies on their way here, in the dim italic ink, with their bar
	// where the size goes and their rate where the time goes.
	for i, r := range arriving {
		ry := y + drawn + i
		if drawn+i >= rows {
			break
		}
		name := printableTitle(r.Name)
		if r.Kind == "dir" {
			name += "/"
		}
		// The file's own mark, muted: it is not here yet.
		x := nameX - 1
		if icon := fileIconFor(r.Name, r.Kind == "dir"); icon.Glyph != "" && !m.Settings.NoNerdFont && !overlay.UseASCII() {
			c.text(x+1, ry, nameX+nameW, ckSeg{t: icon.Glyph, fg: pal.FgMute})
		}
		c.text(nameX+2, ry, nameX+nameW, ckSeg{t: name, fg: pal.FgDim, attrs: ckItalic})
		pct := float64(transferPercent(r.Done, r.Size))
		switch {
		case r.State == "done":
			if showSize {
				c.textR(sizeR, ry, ckSeg{t: units.Bytes(r.Size), fg: pal.FgDim})
			}
			if showMod {
				c.textR(modR, ry, ckSeg{t: filesChecked(), fg: pal.Success})
			}
		case showSize:
			pctText := " " + strconv.Itoa(int(pct)) + "%"
			bx := sizeR - 10 - len(pctText)
			filesBar(c, pal, bx, ry, 10, pct, pal.Accent)
			c.textR(sizeR, ry, ckSeg{t: pctText, fg: pal.FgDim})
		}
		if showMod && r.State == "running" && r.Rate > 0 {
			c.textR(modR, ry, ckSeg{t: units.Bytes(int64(r.Rate)) + "/s", fg: pal.FgMute})
		} else if showMod && r.State != "running" && r.State != "done" {
			c.textR(modR, ry, ckSeg{t: r.State, fg: pal.Warning})
		}
	}
}

// drawFilesFooter draws the count, the marks, the filter or the line being
// typed.
func (m *OS) drawFilesFooter(c *ckCanvas, pal overlay.Palette, s *filesSide, x0, right, y int, keys bool) {
	e := &m.explorer
	prompt := e.prompt
	if !keys {
		prompt = filesNoPrompt
	}
	line := func(label, hint string) {
		c.text(x0, y, right, ckSeg{t: label + " ", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: e.input, fg: pal.Fg}, ckSeg{t: "▏", fg: pal.Accent})
		if hint != "" {
			c.textR(right, y, ckSeg{t: hint, fg: pal.FgMute})
		}
	}
	switch prompt {
	case filesPromptFilter:
		line("/", "")
		return
	case filesPromptGoTo:
		line("go to", "host:path or a path")
		return
	case filesPromptNew:
		line("new", "a name that ends in / makes a folder")
		return
	case filesPromptCopy:
		line("copy to", "host:path or a path")
		return
	case filesPromptMove:
		line("move to", "host:path or a path")
		return
	case filesPromptRename:
		c.text(x0, y, right, ckSeg{t: "rename", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: "  enter keeps it, esc drops it", fg: pal.FgMute})
		return
	}
	if n, size := filesMarkedSize(s); n > 0 {
		x := c.text(x0, y, right, ckSeg{t: strconv.Itoa(n) + " marked", fg: pal.AccentBright, attrs: ckBold})
		c.text(x, y, right, ckSeg{t: " · " + filesSizeText(size) + " · " + plural.Count(len(s.rows), "item"), fg: pal.FgDim})
	} else {
		left := plural.Count(len(s.rows), "item")
		if s.filter != "" {
			left += " · filter " + s.filter
		}
		x := c.text(x0, y, right, ckSeg{t: left, fg: pal.FgDim})
		if n := len(m.filesArrivals(s)); n > 0 {
			c.text(x, y, right, ckSeg{t: " · ", fg: pal.FgMute}, ckSeg{t: strconv.Itoa(n) + " arriving", fg: pal.AccentBright})
		}
	}
	if s.Capped {
		c.textR(right, y, ckSeg{t: "first " + plural.Count(len(s.Entries), "name") + " of a large folder: / narrows it", fg: pal.FgMute})
	}
}

// filesHint is one key hint of the footer: the key, its word, and what a
// click on it runs.
type filesHint struct {
	key, label string
	run        func(m *OS) tea.Cmd
	x0, x1     int
}

// filesHints is the footer's hints: quick look's keys first while it is
// beside the list, then the view's.
func (m *OS) filesHints() []filesHint {
	e := &m.explorer
	var out []filesHint
	act := func(action, label string) {
		if k := m.filesKey(action); k != "" {
			out = append(out, filesHint{key: k, label: label, run: func(m *OS) tea.Cmd { return m.FilesAction(action) }})
		}
	}
	look := func(table config.Table, action, label string, run func(m *OS) tea.Cmd) {
		if m.KeybindRegistry == nil {
			return
		}
		if keys := m.KeybindRegistry.Keys(table, action); len(keys) > 0 {
			out = append(out, filesHint{key: keys[0], label: label, run: run})
		}
	}
	// The footer holds what fits at 160 columns, in the order of the mock.
	// Every other key is in the keys list (?), which-key and keybinds.
	if e.look {
		act(config.ActionFilesQuickLook, "close look")
		look(config.TableQuickLook, "go_inside", "read inside", func(m *OS) tea.Cmd { m.QuickLookGoInside(); return nil })
		act(config.ActionFilesFill, "fill")
	} else {
		act(config.ActionFilesQuickLook, "look")
	}
	act(config.ActionFilesOpen, "open")
	if !e.look && m.filesCursorChanged() {
		// With quick look beside the list, its own v steps its views.
		act(config.ActionFilesReview, "review")
	}
	act(config.ActionFilesEdit, "edit in a pane")
	act(config.ActionFilesMark, "mark")
	if m.filesTabCur().two {
		act(config.ActionFilesCopy, "copy")
		act(config.ActionFilesMove, "move")
	} else {
		act(config.ActionFilesCopy, "copy to")
	}
	act(config.ActionFilesRename, "rename")
	act(config.ActionFilesTrash, "trash")
	if len(e.undo) > 0 {
		act(config.ActionFilesUndo, "undo")
	}
	act(config.ActionFilesFilter, "filter")
	act(config.ActionFilesHidden, "hidden")
	if m.filesTabCur().two {
		act(config.ActionFilesOtherSide, "other side")
	} else {
		act(config.ActionFilesTwoSides, "two sides")
	}
	act(config.ActionFilesGoTo, "go to")
	act(config.ActionFilesClose, "close")
	return out
}

// drawFilesHints draws the keys of the view on its last row, and keeps
// where each went: a click on a hint runs it.
func (m *OS) drawFilesHints(c *ckCanvas, pal overlay.Palette, y, w, top int) {
	c.fill(0, y, w, 1, ckColor(pal.Panel))
	e := &m.explorer
	e.hints = e.hints[:0]
	e.hintsY = top + y
	x := 1
	for _, h := range m.filesHints() {
		need := ansi.StringWidth(h.key) + 1 + ansi.StringWidth(h.label)
		if x+need > w-1 {
			break
		}
		h.x0 = x
		x = c.text(x, y, w-1, ckSeg{t: h.key, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + h.label, fg: pal.FgDim})
		h.x1 = x
		e.hints = append(e.hints, h)
		x += 3
	}
}

// filesKey is the first key bound to a Files action, or "".
func (m *OS) filesKey(action string) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	if keys := m.KeybindRegistry.Keys(config.TableFiles, action); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// filesChecked is the word a copy that landed and passed its check shows in
// the Modified column.
func filesChecked() string {
	if overlay.UseASCII() {
		return "+ checked"
	}
	return "✓ checked"
}
