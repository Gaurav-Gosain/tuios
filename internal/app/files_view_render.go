package app

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
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
	files, here, host, crumb, rule, div string
}

func (m *OS) filesGlyphs() filesGlyphs {
	if overlay.UseASCII() {
		return filesGlyphs{files: "#", here: "*", host: "@", crumb: ">", rule: "=", div: "|"}
	}
	g := filesGlyphs{files: "", here: "", host: "", crumb: "›", rule: "━", div: "│"}
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
	s := &e.side
	reading := s.loading && time.Since(s.loadingAt) >= filesReadingAfter
	key := filesFrame{ok: true, gen: e.gen, w: w, h: h, top: top, theme: theme.Generation(), depth: pal.Depth,
		ascii: overlay.UseASCII(), nerd: !m.Settings.NoNerdFont, look: m.quickLook.open && m.quickLook.files, reading: reading}
	f := e.frame
	f.content = ""
	if f == key && e.frame.content != "" {
		return e.frame.content, top, w, h
	}
	c := (*ckCanvas)(nil).reset(w, h, ckColor(pal.Canvas))
	m.drawFilesView(c, pal, w, h, top, reading)
	key.content = c.render()
	e.frame = key
	return key.content, top, w, h
}

// drawFilesView draws the whole view on c.
func (m *OS) drawFilesView(c *ckCanvas, pal overlay.Palette, w, h, top int, reading bool) {
	e := &m.explorer
	g := m.filesGlyphs()
	m.drawFilesTopBar(c, pal, g, w)
	hintsY := h - 1
	body := filesBox{x: 0, y: 2, w: w, h: max(hintsY-2, 0)}
	look := e.look && m.quickLook.open && m.quickLook.files
	list := body
	e.lookBox = filesBox{}
	if look {
		if w >= filesLookSplitW {
			list.w = max(w*2/5, filesSizeMin)
			e.lookBox = filesBox{x: list.w + 1, y: top + body.y, w: w - list.w - 1, h: body.h}
			for y := body.y; y < body.y+body.h; y++ {
				c.text(list.w, y, 0, ckSeg{t: g.div, fg: pal.FgMute})
			}
		} else {
			e.lookBox = filesBox{x: 0, y: top + body.y, w: w, h: body.h}
			list.h = 0
		}
	}
	if list.h > 0 {
		m.drawFilesSide(c, pal, g, list, top, reading)
	}
	m.drawFilesHints(c, pal, hintsY, w)
}

// drawFilesTopBar draws the Files mark, the side's tab and the switches.
func (m *OS) drawFilesTopBar(c *ckCanvas, pal overlay.Palette, g filesGlyphs, w int) {
	e := &m.explorer
	c.fill(0, 0, w, 1, ckColor(pal.Panel))
	x := c.text(1, 0, 0, ckSeg{t: g.files + " ", fg: pal.AccentBright}, ckSeg{t: "Files", fg: pal.Fg, attrs: ckBold})
	name, _ := m.filesSideMachine()
	tab := " " + name + " " + shortenHome(e.side.Dir) + " "
	if e.side.host != "" || m.AttachedHost != "" {
		tab = " " + name + ":" + e.side.Dir + " "
	}
	x += 3
	right := "sort " + e.sort.String()
	if e.reverse {
		right += " reversed"
	}
	if e.hidden {
		right += " · hidden on"
	} else {
		right += " · hidden off"
	}
	rx := w - 1 - ansi.StringWidth(right)
	tab = ckTrunc(tab, max(rx-x-2, 0))
	c.text(x, 0, rx-1, ckSeg{t: tab, fg: pal.Fg, bg: pal.Surface})
	c.textR(w-1, 0, ckSeg{t: right, fg: pal.FgDim})
}

// drawFilesSide draws one side in box b. top is the screen row of the
// canvas, for the click targets.
func (m *OS) drawFilesSide(c *ckCanvas, pal overlay.Palette, g filesGlyphs, b filesBox, top int, reading bool) {
	e := &m.explorer
	s := &e.side
	x0, right := b.x+1, b.x+b.w-1
	y := b.y
	// The machine row.
	c.fill(b.x, y, b.w, 2, ckColor(pal.Panel))
	name, here := m.filesSideMachine()
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
	e.crumbsY = top + y
	e.crumbs = e.crumbs[:0]
	sortW := c.textR(right, y, ckSeg{t: e.sort.String(), fg: pal.FgDim})
	m.drawFilesCrumbs(c, pal, g, x0, y, sortW-2)
	// The rule: the side with the keys.
	y++
	c.hline(b.x, y, b.w, g.rule, pal.Accent)
	y += 2
	// The columns.
	nameX := x0 + 1
	sizeR, modR := right, right
	showMod := b.w >= filesModifiedMin
	showSize := b.w >= filesSizeMin
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
	e.rowsTop, e.rowsN, e.listX, e.listW = top+y, rows, b.x, b.w
	nameW := right - nameX
	if showSize {
		nameW = sizeR - filesSizeW - 1 - nameX
	}
	m.drawFilesRows(c, pal, b, y, rows, nameX, nameW, sizeR, modR, showSize, showMod)
	m.drawFilesFooter(c, pal, x0, right, footY)
}

// drawFilesCrumbs draws the folder as crumbs from x, up to limit, and keeps
// each crumb's columns for a click. A long path loses its first crumbs.
func (m *OS) drawFilesCrumbs(c *ckCanvas, pal overlay.Palette, g filesGlyphs, x, y, limit int) {
	e := &m.explorer
	s := &e.side
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
		e.crumbs = append(e.crumbs, filesCrumb{dir: parts[i].dir, x0: x0, x1: x})
	}
}

// drawFilesRows draws the rows of the listing, or the one row that says why
// there are none.
func (m *OS) drawFilesRows(c *ckCanvas, pal overlay.Palette, b filesBox, y, rows, nameX, nameW, sizeR, modR int, showSize, showMod bool) {
	e := &m.explorer
	s := &e.side
	note := func(text string, warn bool) {
		ink := pal.FgMute
		if warn {
			ink = pal.Warning
		}
		c.text(nameX, y, b.x+b.w-1, ckSeg{t: text, fg: ink})
	}
	switch {
	case s.Err != "" && len(s.Entries) == 0:
		note(s.Err, true)
		return
	case s.Dir == "":
		name, _ := m.filesSideMachine()
		note("reading "+name+":"+s.want, false)
		return
	case len(s.rows) == 0 && e.filter != "":
		note("No name has "+e.filter+" in it.", false)
		return
	case len(s.rows) == 0:
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
	for i := 0; i < rows && s.scroll+i < n; i++ {
		at := s.scroll + i
		idx := s.rows[at]
		en := s.Entries[idx]
		ry := y + i
		st := sidebarRowState{Cursor: at == s.cursor}
		if st.Cursor {
			c.fill(b.x, ry, b.w, 1, ckColor(pal.RowSel))
		}
		c.draw(nameX-1, ry, nameW+1, m.sidebarFileRow(fileRowSpec{Kind: sidebarRowFileEntry, Name: en.Name, Dir: en.Dir, Icon: en.Icon}, nameW+1, pal, st))
		if idx >= len(s.infos) {
			continue
		}
		in := s.infos[idx]
		if (showSize || st.Cursor) && !en.Dir && in.Kind != "" {
			c.textR(sizeR, ry, ckSeg{t: units.Bytes(in.Size), fg: pal.FgDim})
		}
		if showMod && in.MTime > 0 {
			c.textR(modR, ry, ckSeg{t: agentElapsed("file", in.MTime*int64(time.Millisecond), now) + " ago", fg: pal.FgMute})
		}
	}
}

// drawFilesFooter draws the count, the filter or the line being typed.
func (m *OS) drawFilesFooter(c *ckCanvas, pal overlay.Palette, x0, right, y int) {
	e := &m.explorer
	s := &e.side
	switch e.prompt {
	case filesPromptFilter:
		c.text(x0, y, right, ckSeg{t: "/ ", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: e.input, fg: pal.Fg}, ckSeg{t: "▏", fg: pal.Accent})
		return
	case filesPromptGoTo:
		c.text(x0, y, right, ckSeg{t: "go to ", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: e.input, fg: pal.Fg}, ckSeg{t: "▏", fg: pal.Accent})
		c.textR(right, y, ckSeg{t: "host:path or a path", fg: pal.FgMute})
		return
	}
	left := plural.Count(len(s.rows), "item")
	if e.filter != "" {
		left += " · filter " + e.filter
	}
	c.text(x0, y, right, ckSeg{t: left, fg: pal.FgDim})
	if s.Capped {
		c.textR(right, y, ckSeg{t: "first " + plural.Count(len(s.Entries), "name") + " of a large folder: / narrows it", fg: pal.FgMute})
	}
}

// drawFilesHints draws the keys of the view on its last row.
func (m *OS) drawFilesHints(c *ckCanvas, pal overlay.Palette, y, w int) {
	c.fill(0, y, w, 1, ckColor(pal.Panel))
	e := &m.explorer
	look := "look"
	if e.look {
		look = "close look"
	}
	pairs := [][2]string{
		{config.ActionFilesQuickLook, look},
		{config.ActionFilesOpen, "open"},
		{config.ActionFilesEdit, "edit in a pane"},
		{config.ActionFilesOpenSystem, "open with system"},
		{config.ActionFilesInsertPath, "type path"},
		{config.ActionFilesCopyPath, "copy path"},
		{config.ActionFilesFilter, "filter"},
		{config.ActionFilesGoTo, "go to"},
		{config.ActionFilesHidden, "hidden"},
		{config.ActionFilesSort, "sort"},
		{config.ActionFilesClose, "close"},
	}
	x := 1
	for _, p := range pairs {
		k := m.filesKey(p[0])
		if k == "" {
			continue
		}
		need := ansi.StringWidth(k) + 1 + ansi.StringWidth(p[1])
		if x+need > w-1 {
			break
		}
		x = c.text(x, y, w-1, ckSeg{t: k, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + p[1], fg: pal.FgDim})
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
