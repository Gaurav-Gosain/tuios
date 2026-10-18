package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Widths of the cockpit's columns, by the width of the screen. See
// DESIGN.md 2.6: under ckOneColumn the fleet fills the screen and the focus
// opens in its place; under ckTwoLine the fleet's rows are one line.
const (
	ckOneColumn = 80
	ckTwoLine   = 120
	ckWide      = 170
)

// ckLook is the cockpit's palette: the chrome tokens and the few grounds the
// cockpit derives from them. Every colour here comes from theme.UI().
type ckLook struct {
	pal                    overlay.Palette
	canvas, panel, cursor  color.Color
	hover, edge, edgePanel color.Color
	add, del               color.Color
	attnTint, busyTint     color.Color
	okTint, warnTint       color.Color
	nerd, ascii            bool
}

// ckLookKey is what a look was built from.
type ckLookKey struct {
	theme        uint64
	depth        overlay.Depth
	ascii, plain bool
	// The palette's grounds and inks: with theme none they follow the host
	// terminal's colours, which change with no new theme generation.
	canvas, panel, fg, accent color.Color
}

// cockpitLook is the look for the depth in force, built again only when the
// theme, the depth or the glyph set changed: the blends cost more than a
// cached frame.
func (m *OS) cockpitLook() ckLook {
	ck := &m.Inbox.ck
	pal := theme.UI()
	key := ckLookKey{theme: theme.Generation(), depth: pal.Depth, ascii: overlay.UseASCII(), plain: m.Settings.NoNerdFont,
		canvas: pal.Canvas, panel: pal.Panel, fg: pal.Fg, accent: pal.Accent}
	if ck.lookAt != key || ck.look.pal.Canvas == nil {
		ck.look, ck.lookAt = m.buildCockpitLook(pal), key
		ck.lookGen++
	}
	return ck.look
}

// cockpitLookGen is the generation of the look in force, built now if the
// palette changed, for a key of something drawn with it.
func (m *OS) cockpitLookGen() uint64 {
	m.cockpitLook()
	return m.Inbox.ck.lookGen
}

// buildCockpitLook builds the look. At 256 colours every derived ground is
// moved off the grey it sits on; at 16 there are no grounds, and the cursor
// and the pointer are attributes (see ckCanvas.mark).
func (m *OS) buildCockpitLook(pal overlay.Palette) ckLook {
	l := ckLook{pal: pal, canvas: pal.Canvas, panel: pal.Panel, cursor: pal.Surface}
	l.ascii = overlay.UseASCII()
	l.nerd = !l.ascii && !m.Settings.NoNerdFont
	l.hover = overlay.MixColors(pal.Panel, pal.Fg, 0.06)
	l.edge = overlay.Shown(overlay.Structure(pal.Canvas))
	l.edgePanel = overlay.Shown(overlay.Structure(pal.Panel))
	l.add = overlay.MixColors(pal.Success, pal.FgDim, 0.4)
	l.del = overlay.MixColors(pal.Warn, pal.FgDim, 0.4)
	tint := func(c color.Color) color.Color { return overlay.MixColors(c, pal.Canvas, 0.8) }
	l.attnTint, l.busyTint, l.okTint, l.warnTint = tint(pal.Attention), tint(pal.Busy), tint(pal.Success), tint(pal.Warn)
	switch pal.Depth {
	case overlay.Depth256:
		l.cursor = overlay.Apart256(l.cursor, pal.Panel)
		l.hover = overlay.Apart256(l.hover, pal.Panel)
		l.add, l.del = overlay.To256(l.add), overlay.To256(l.del)
		l.attnTint, l.busyTint = overlay.To256(l.attnTint), overlay.To256(l.busyTint)
		l.okTint, l.warnTint = overlay.To256(l.okTint), overlay.To256(l.warnTint)
	case overlay.Depth16:
		l.cursor, l.hover = overlay.NoColor, overlay.NoColor
		l.attnTint, l.busyTint, l.okTint, l.warnTint = overlay.NoColor, overlay.NoColor, overlay.NoColor, overlay.NoColor
		l.add, l.del = pal.Success, pal.Warn
		l.edge, l.edgePanel = pal.Edge, pal.Edge
	}
	p := &l.pal
	for _, c := range []*color.Color{&l.canvas, &l.panel, &l.cursor, &l.hover, &l.edge, &l.edgePanel, &l.add, &l.del,
		&l.attnTint, &l.busyTint, &l.okTint, &l.warnTint, &p.Canvas, &p.Panel, &p.Surface, &p.Card, &p.Fg, &p.FgDim,
		&p.FgMute, &p.Accent, &p.AccentBright, &p.PillFg, &p.Warn, &p.Success, &p.Warning, &p.Attention, &p.Busy, &p.Edge} {
		*c = ckFlat(*c)
	}
	return l
}

// ckFlat turns a truecolor value into a color.RGBA. A theme colour is a type
// whose RGBA looks itself up, and the line renderer compares every cell's
// colours by RGBA: flat values took most of a frame's cost away. An indexed,
// basic or absent colour keeps its type, which is what makes it draw at 256
// and 16 colours.
func ckFlat(c color.Color) color.Color {
	switch c.(type) {
	case nil, ansi.BasicColor, ansi.IndexedColor, color.RGBA:
		return c
	}
	if noColor(c) {
		return c
	}
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

// ckGlyph is a state's mark: Nerd Font, Unicode and ASCII forms. The
// Unicode forms are the ones a monospace font without Nerd Font glyphs has,
// GeistMono Mono included: no ◐, ✓, ✗, ▸ or ▾, which it draws as a box.
type ckGlyph struct{ nerd, uni, ascii string }

var (
	ckGlyphNeeds   = ckGlyph{"●", "●", "!"}
	ckGlyphWorking = ckGlyph{"", "»", "~"}
	ckGlyphReview  = ckGlyph{"", "√", "v"}
	ckGlyphFailed  = ckGlyph{"", "×", "x"}
	ckGlyphIdle    = ckGlyph{"○", "○", "o"}
	ckGlyphHost    = ckGlyph{"\U000f048b", "@", "@"}
	ckGlyphBranch  = ckGlyph{"", "", "br"}
	ckGlyphFold    = ckGlyph{"", "›", ">"}
	ckGlyphMarked  = ckGlyph{"\uf14a", "•", "*"}
	ckGlyphResume  = ckGlyph{"\uf01e", "»", ">"}
	ckGlyphImage   = ckGlyph{"\U000f02e9", "[img]", "[img]"}
)

// g picks the form the terminal can draw.
func (l ckLook) g(gl ckGlyph) string {
	switch {
	case l.ascii:
		return gl.ascii
	case l.nerd:
		return gl.nerd
	}
	return gl.uni
}

// ckMark is the mark and colour of an agent state, by the design's table.
func (l ckLook) ckMark(state string, doneSeen bool) (string, color.Color) {
	switch state {
	case "needs_input":
		return l.g(ckGlyphNeeds), l.pal.Attention
	case "errored":
		return l.g(ckGlyphFailed), l.pal.Warn
	case "working":
		return l.g(ckGlyphWorking), l.pal.Busy
	case "done":
		if !doneSeen {
			return l.g(ckGlyphReview), l.pal.Success
		}
	}
	return l.g(ckGlyphIdle), l.pal.FgMute
}

// ckItemMark is the mark and colour of an Inbox item.
func (l ckLook) ckItemMark(it session.AttentionItem) (string, color.Color) {
	switch it.Kind {
	case session.AttentionMail:
		return sidebarMailGlyph(), l.pal.AccentBright
	case session.AttentionErrored:
		return l.g(ckGlyphFailed), l.pal.Warn
	case session.AttentionFinished:
		return l.g(ckGlyphReview), l.pal.Success
	case session.AttentionResume:
		return l.g(ckGlyphResume), l.pal.AccentBright
	case session.AttentionOutbox:
		return inboxKindGlyph(it.Kind), l.pal.AccentBright
	}
	return l.g(ckGlyphNeeds), l.pal.Attention
}

// ckFrameKey is what a frame was drawn from.
type ckFrameKey struct {
	w, h, top, bottom    int
	gen, inbox, mail     uint64
	liveGen              uint64
	marks, look          uint64
	selectErr            string
	list                 agentsListKey
	minute               int64
	theme                uint64
	thread, editor       uint64
	live, open, daemon   bool
	keys                 *config.KeybindRegistry
	hosts                uint64
	depth                overlay.Depth
	ascii, nerd, snoozes bool
}

// renderCockpit draws the cockpit over the screen between the dock's rows,
// and returns it with its top row.
func (m *OS) renderCockpit() (string, int, int, int) {
	st := &m.Inbox
	ck := &st.ck
	res := m.viewReserve()
	w, top := m.GetRenderWidth(), res.Top
	h := m.GetRenderHeight() - res.Top - res.Bottom
	if w <= 0 || h <= 0 {
		return "", 0, 0, 0
	}
	l := m.cockpitLook()
	now := time.Now()
	m.cockpitRows()
	// The agents list cannot change while the frame is drawn, so every read
	// of it from here on takes the list as built, without hashing the rail's
	// signature again.
	st.agents.inFrame = true
	defer func() { st.agents.inFrame = false }()
	key := ckFrameKey{
		marks: st.agents.marksGen, selectErr: st.selectErr, look: ck.lookGen,
		w: w, h: h, top: top, bottom: res.Bottom, gen: ck.gen, inbox: st.Gen, mail: m.AgentMail.Gen,
		list: st.agents.listAt, minute: now.Unix() / 60, theme: theme.Generation(), live: st.Live, liveGen: ck.liveGen,
		open: st.threads.open, daemon: m.IsDaemonSession, keys: m.KeybindRegistry, hosts: m.federationGen,
		depth: l.pal.Depth, ascii: l.ascii, nerd: l.nerd, snoozes: st.life.ShowSnoozed,
	}
	if e, ok := m.cockpitAgent(); ok {
		if t := st.threads.byKey[agentKey(e)]; t != nil {
			key.thread = t.gen + 1
		}
	}
	if ed := st.reply.editor; ed != nil {
		// At the width the last frame drew it, so the composer's own cache
		// answers, and Drawn moves only when the draft or the cursor did.
		ed.comp.Render(max(ck.edW-4, 8), composerStyles(l.pal), l.panel)
		key.editor = ed.comp.Drawn() + 1
	}
	// A state that moves with the clock or the keyboard is drawn afresh.
	// The reply editor is not one: what it draws is in the key.
	transient := st.ck.img != nil || st.Peek != nil || st.selectEditing || m.InboxSnoozePicking() || st.agents.sheet != nil ||
		st.approvals.reason != nil || st.approvals.armed.key != ""
	if !transient && ck.frame != "" && ck.frameAt == key {
		return ck.frame, top, w, h
	}
	m.tickStats.Cockpit++
	ck.canvas = ck.canvas.reset(w, h, l.canvas)
	c := ck.canvas
	ck.hits = ck.hits[:0]
	ck.fleetW = 0
	ck.liveShown = false
	m.drawCockpit(c, l, top, now)
	ck.frame, ck.frameAt = c.render(), key
	return ck.frame, top, w, h
}

// drawCockpit lays the cockpit out on c. top is c's row on the screen, which
// the click targets add.
func (m *OS) drawCockpit(c *ckCanvas, l ckLook, top int, now time.Time) {
	st := &m.Inbox
	ck := &st.ck
	hdrY := 0
	if c.h >= 32 {
		hdrY = 1
	}
	colTop, colBot, hintsY := hdrY+2, c.h-2, c.h-1
	if c.h < 16 {
		colTop, colBot = hdrY+1, c.h-1
	}
	if st.ck.img != nil {
		m.drawImageView(c, l, top)
		return
	}
	rows := m.cockpitRows()
	agents := 0
	for _, r := range rows {
		if r.selectable() {
			agents++
		}
	}
	m.drawCockpitHeader(c, l, hdrY, top)

	if empty := m.cockpitEmptyLines(rows); empty != nil {
		m.drawCockpitEmpty(c, l, colTop, colBot, top, empty)
		m.drawCockpitHints(c, l, hintsY, now)
		return
	}

	// The layout by width: the fleet beside the focus, or one of them.
	fleetW := 56
	switch {
	case c.w < ckOneColumn:
		fleetW = c.w
	case c.w < ckTwoLine:
		fleetW = 38
	case c.w < ckWide:
		fleetW = 48
	}
	strip := c.w >= ckOneColumn && agents <= 2 && !ck.needsOnly && st.Filter == "" && st.Select == "" && st.agents.query == ""
	focusX, focusW := fleetW+3, c.w-fleetW-5
	switch {
	case strip:
		focusX, focusW = 2, c.w-4
	case c.w < ckOneColumn && st.threads.open:
		focusX, focusW = 2, c.w-4
	case c.w < ckOneColumn:
		focusW = 0
	}
	if !strip && (c.w >= ckOneColumn || !st.threads.open) {
		m.drawFleet(c, l, fleetW, colTop, colBot, top)
	}
	// At 170 columns and wider the side column sits beside the focus.
	if focusW > 0 && c.w >= ckWide {
		if e, ok := m.cockpitAgent(); ok {
			focusW -= ckSideWidth + 1
			m.drawSide(c, l, e, c.w-ckSideWidth, colTop, ckSideWidth, colBot)
		}
	}
	if focusW > 0 {
		fy := colTop + 1
		if strip {
			fy = m.drawCockpitStrip(c, l, colTop, top) + 1
		}
		if c.w < ckOneColumn {
			x := c.text(2, fy, 0, ckSeg{t: overlay.ArrowLeft() + " back", fg: l.pal.AccentBright, attrs: ckBold})
			ck.hits = append(ck.hits, ckHit{x0: 2, y0: top + fy, x1: x, y1: top + fy + 1, kind: ckHitAction, action: "back"})
			fy += 2
		}
		m.drawFocus(c, l, focusX, fy, focusW, colBot, top, now)
	}
	m.drawCockpitHints(c, l, hintsY, now)
}

// drawCockpitHeader draws the title row: the counts, the search and the New
// agent button.
func (m *OS) drawCockpitHeader(c *ckCanvas, l ckLook, y, top int) {
	st := &m.Inbox
	ck := &st.ck
	pal := l.pal
	x := c.text(2, y, 0, ckSeg{t: "Agents", fg: pal.Fg, attrs: ckBold}) + 3
	var counts [ckGroups]int
	for _, r := range ck.rows {
		if r.kind == ckRowHead {
			counts[r.group] = r.count
		}
	}
	type cnt struct {
		g     ckGlyph
		ink   color.Color
		n     int
		words string
	}
	cnts := []cnt{
		{ckGlyphNeeds, pal.Attention, counts[ckNeeds], "needs you"},
		{ckGlyphWorking, pal.Busy, counts[ckWorking], "working"},
		{ckGlyphReview, pal.Success, counts[ckReview], "to review"},
		{ckGlyphIdle, pal.FgMute, counts[ckIdle] + counts[ckEarlier], "idle"},
	}
	// The counts say their words when the row has room for them beside the
	// search and the button, and only their marks otherwise.
	full := x
	for i, k := range cnts {
		if k.n > 0 || i == 0 {
			full += ckWidth(ckSeg{t: l.g(k.g) + " " + strconv.Itoa(k.n) + " " + k.words}) + 3
		}
	}
	compact := full > c.w-2-ckWidth(ckSeg{t: " + New agent "})-16
	for i, k := range cnts {
		if k.n == 0 && i > 0 {
			continue
		}
		segs := []ckSeg{{t: l.g(k.g) + " ", fg: k.ink}, {t: strconv.Itoa(k.n), fg: pal.Fg, attrs: ckBold}}
		if !compact {
			segs = append(segs, ckSeg{t: " " + k.words, fg: pal.FgDim})
		}
		x0 := x
		x = c.text(x, y, 0, segs...) + 3
		if i == 0 {
			ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: x - 3, y1: top + y + 1, kind: ckHitAction, action: "needs"})
		}
	}
	if ck.needsOnly || st.Filter != "" {
		what := "items only"
		if st.Filter != "" {
			what = strings.ToLower(inboxGroupTitle(st.Filter)) + " only"
		}
		x = c.text(x, y, 0, ckSeg{t: what, fg: pal.FgDim}) + 3
	}
	if n := len(m.markedAgents()); n > 0 {
		x = c.text(x, y, 0, ckSeg{t: strconv.Itoa(n) + " marked", fg: pal.AccentBright, attrs: ckBold}) + 3
	}
	if !st.Live {
		x = c.text(x, y, 0, ckSeg{t: "not connected", fg: pal.Warning}) + 3
	}
	right := c.w - 2
	btn := " + New agent "
	if l.ascii {
		btn = " + New agent "
	}
	bx := c.textR(right, y, ckSeg{t: btn, fg: pal.PillFg, bg: pal.Accent, attrs: ckBold})
	c.mark(pal, bx, y, right-bx, 1, ckOn)
	ck.hits = append(ck.hits, ckHit{x0: bx, y0: top + y, x1: right, y1: top + y + 1, kind: ckHitAction, action: "new"})
	sw := min(38, bx-x-2)
	if sw < 12 {
		return
	}
	sx := bx - 2 - sw
	c.fill(sx, y, sw, 1, l.panel)
	switch {
	case st.selectErr != "":
		// The line was not applied: say why where the line is.
		c.text(sx+1, y, sx+sw, ckSeg{t: ckTrunc(printableTitle(st.selectErr), sw-2), fg: pal.Warning})
	case st.selectEditing:
		c.draw(sx+1, y, sw-1, m.selectLine(sw-1))
	case st.Select != "" || st.agents.query != "":
		q := strings.TrimSpace(st.Select + " " + st.agents.query)
		c.text(sx+1, y, sx+sw, ckSeg{t: "/  ", fg: pal.FgMute}, ckSeg{t: ckTrunc(q, sw-5), fg: pal.Fg})
	default:
		words := "Search agents, sessions, machines"
		if sw < 36 {
			words = "Search"
		}
		c.text(sx+1, y, sx+sw, ckSeg{t: "/  ", fg: pal.FgMute}, ckSeg{t: words, fg: pal.FgMute})
	}
	ck.hits = append(ck.hits, ckHit{x0: sx, y0: top + y, x1: sx + sw, y1: top + y + 1, kind: ckHitAction, action: "search"})
}

// drawFleet draws the fleet column: the groups, then the machines.
func (m *OS) drawFleet(c *ckCanvas, l ckLook, w, y0, y1, top int) {
	st := &m.Inbox
	ck := &st.ck
	pal := l.pal
	c.fill(0, y0, w, y1-y0, l.panel)
	ck.fleetW = w
	listTop, listBot := y0+1, y1
	colH := max(listBot-listTop, 1)
	oneLine := w < 48
	if !oneLine {
		heads := 0
		for _, r := range ck.rows {
			if !r.selectable() && r.kind != ckRowChild {
				heads++
			}
		}
		oneLine = ck.tall+heads > colH
	}
	if colH != ck.colH || oneLine != ck.oneLine {
		ck.colH, ck.oneLine = colH, oneLine
		m.cockpitTouch()
	}
	rows := m.cockpitRows()
	// Keep the cursor's row in view.
	start, total := 0, 0
	for i, r := range rows {
		if i == ck.idx {
			start = total
		}
		total += r.lines
	}
	cur := 2
	if r, ok := m.cockpitSelected(); ok {
		cur = r.lines
	}
	ck.scroll = min(ck.scroll, max(total-colH, 0))
	if ck.follow && start < ck.scroll {
		ck.scroll = start
	}
	if ck.follow && start+cur > ck.scroll+colH {
		ck.scroll = start + cur - colH
	}
	ck.scroll = max(ck.scroll, 0)
	line := 0
	sel, _ := m.cockpitSelected()
	for _, r := range rows {
		if line+r.lines <= ck.scroll {
			line += r.lines
			continue
		}
		y := listTop + line - ck.scroll
		if y+r.lines > listBot {
			break
		}
		line += r.lines
		m.drawFleetRow(c, l, r, r.key == sel.key && r.selectable(), r.key == ck.hover, 0, y, w, top)
	}
	// At 16 colours the columns have no grounds, so a hairline parts them.
	if pal.Depth == overlay.Depth16 && w < c.w {
		for y := y0; y < y1; y++ {
			c.text(w+1, y, 0, ckSeg{t: vtGlyph(), fg: l.edge})
		}
	}
}

// drawFleetRow draws one row of the fleet at x, y, w cells wide.
func (m *OS) drawFleetRow(c *ckCanvas, l ckLook, r ckRow, cursor, hover bool, x, y, w, top int) {
	ck := &m.Inbox.ck
	pal := l.pal
	right := x + w - 2
	switch r.kind {
	case ckRowGap:
		return
	case ckRowNote:
		c.text(x+2, y, right, ckSeg{t: r.note, fg: pal.FgMute})
		return
	case ckRowHead:
		mark := ""
		if ck.folded[r.group] {
			mark = l.g(ckGlyphFold) + " "
		}
		title := ckGroupTitles[r.group]
		if f := m.Inbox.Filter; f != "" && r.group == ckNeeds {
			// Narrowed to one kind, as ctrl+b M opens on mail: the heading
			// names the kind.
			title = inboxGroupTitle(f)
		}
		end := c.text(x+2, y, right, ckSeg{t: mark + title, fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(r.count), fg: pal.FgMute})
		c.hline(end+2, y, right-end-2, hzGlyph(), l.edgePanel)
		ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitHead, group: r.group})
		return
	case ckRowMore:
		words := "+" + strconv.Itoa(r.count) + " more"
		if r.group >= 0 {
			words += " " + strings.ToLower(ckGroupTitles[r.group])
			ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitMore, group: r.group})
		}
		c.text(x+5, y, right, ckSeg{t: words, fg: pal.FgMute})
		return
	case ckRowChild:
		tree := "├ "
		if r.last {
			tree = "└ "
		}
		if l.ascii {
			tree = "| "
		}
		g, ink := l.g(ckGlyphWorking), pal.Busy
		if !r.child.Running() {
			g, ink = l.g(ckGlyphReview), pal.FgMute
		}
		what := r.child.Now
		if what == "" {
			what = r.child.Description
		}
		c.text(x+5, y, right, ckSeg{t: tree, fg: l.edgePanel}, ckSeg{t: g + " ", fg: ink},
			ckSeg{t: printableTitle(firstNonEmptyStr(r.child.Type, "agent")), fg: pal.FgDim}, ckSeg{t: "  " + printableTitle(what), fg: pal.FgMute})
		return
	}

	ground := l.panel
	switch {
	case cursor:
		ground = l.cursor
	case hover:
		ground = l.hover
	}
	c.fill(x, y, w, r.lines, ground)
	// At 16 colours the cursor is reverse video and the pointer an
	// underline, applied once the row is drawn.
	defer c.mark(pal, x+1, y, w-1, r.lines, overlay.RowState{Cursor: cursor, Focused: cursor, Hover: hover})
	if cursor {
		for i := range r.lines {
			c.text(x, y+i, 0, ckSeg{t: "▌", fg: pal.Accent})
		}
	}
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = ck.agents[r.agent]
		// A marked agent shows its mark beside the cursor bar, and a click
		// there marks or unmarks it.
		if m.Inbox.agents.marks[agentKey(e)] {
			c.text(x+1, y, 0, ckSeg{t: l.g(ckGlyphMarked), fg: pal.AccentBright, attrs: ckBold})
		}
		ck.hits = append(ck.hits, ckHit{x0: x + 1, y0: top + y, x1: x + 2, y1: top + y + 1, kind: ckHitMark, key: r.key})
	}
	ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + r.lines, kind: ckHitRow, key: r.key})
	name, harness, place, wait := m.ckRowFacts(r, e)
	g, gInk := l.ckMark(e.State, e.DoneSeen)
	if r.hasItem && (r.agent < 0 || r.group == ckNeeds || r.group == ckSnoozed) {
		g, gInk = l.ckItemMark(r.item)
	}
	nameInk := pal.Fg
	down := e.Host != "" && m.ckLinkDown(e.Host)
	if r.group == ckIdle || r.group == ckEarlier || r.group == ckSnoozed || r.item.Stale || down {
		nameInk = pal.FgDim
	}
	if down {
		// The machine's link is down: the row is its last known state.
		place += sepWord() + "link down"
		wait = ""
	}
	ink := func(c color.Color) color.Color { return overlay.Readable(c, ground) }
	nameW := 15
	if w < 50 {
		nameW = 13
	}
	c.text(x+2, y, right, ckSeg{t: g, fg: gInk})
	nx := x + 5
	if r.lines == 1 && w < 48 {
		nx = x + 4
	}
	c.text(nx, y, nx+nameW-1, ckSeg{t: ckTrunc(name, nameW-1), fg: ink(nameInk), attrs: ckBold})
	rx := c.textR(right, y, ckSeg{t: wait, fg: ink(pal.FgMute)})
	if r.lines == 1 && w < 48 {
		c.text(nx+nameW, y, rx-1, ckSeg{t: ckTrunc(place, rx-1-nx-nameW), fg: ink(pal.FgMute)})
		return
	}
	hx := nx + nameW
	if diff := ckDiffSegs(l, e); diff != nil && rx-2-ckWidth(diff...) > hx+18 {
		rx = c.textR(rx-2, y, diff...)
	}
	hx = c.text(hx, y, rx-1, ckSeg{t: ckPad(harness, 9), fg: ink(pal.FgDim)})
	c.text(hx, y, rx-1, ckSeg{t: ckTrunc(place, rx-1-hx), fg: ink(pal.FgMute)})
	if r.lines < 2 {
		return
	}
	verb, rest, verbInk, rightSegs := m.ckRowLine2(l, r, e)
	rx2 := right
	if rightSegs != nil {
		rx2 = c.textR(right, y+1, rightSegs...) - 2
	}
	lx := c.text(nx, y+1, rx2, ckSeg{t: verb, fg: ink(verbInk), attrs: ckBold})
	if rest != "" {
		if verb != "" {
			lx += 2
		}
		c.text(lx, y+1, rx2, ckSeg{t: ckTrunc(rest, rx2-lx), fg: ink(pal.FgDim)})
	}
}

// ckOn is the state of a button and a pill: at 16 colours, reverse video.
var ckOn = overlay.RowState{Cursor: true, Focused: true}

// ckLinkDown reports whether the link to a machine is down.
func (m *OS) ckLinkDown(host string) bool {
	for _, h := range m.FederationHosts {
		if h.Name == host {
			return h.Status != "up"
		}
	}
	return false
}

// ckRowFacts are a row's name, harness, place and age.
func (m *OS) ckRowFacts(r ckRow, e sidebarAgentEntry) (name, harness, place, wait string) {
	now := time.Now()
	if r.agent >= 0 {
		name = printableTitle(e.Title)
		if name == "" {
			name = shortWindowLabel(e.WindowID)
		}
		harness = ckHarness(e.Harness)
		place = m.ckPlace(e)
		if e.StateAt > 0 {
			wait = inboxWait(e.StateAt, now)
		}
	}
	if r.hasItem {
		it := r.item
		if r.agent < 0 {
			name, harness = inboxWho(it), ckHarness(it.Harness)
			place = m.inboxWhere(it)
		}
		wait = inboxWait(it.Since, now)
		switch {
		case it.Stale:
			wait = inboxSeen(it.SeenAt, now)
		case it.SnoozedUntil != 0:
			wait = inboxSnoozedWhen(it.SnoozedUntil, now)
		}
		if it.Kind == session.AttentionMail && it.Seen > 0 {
			// The agent it was for has read it. It is said beside the age,
			// which a long name never cuts.
			wait = "seen " + wait
		}
	}
	return name, harness, place, wait
}

// ckHarness is a harness id as a row says it: claude for claude-code.
func ckHarness(h string) string {
	h = strings.TrimSuffix(strings.TrimSuffix(h, "-code"), "-cli")
	return printableTitle(h)
}

// ckPlace is where an agent runs, as its row says it: the session, with the
// machine in front for an agent on another one ("build:api").
func (m *OS) ckPlace(e sidebarAgentEntry) string {
	s := printableTitle(firstNonEmptyStr(e.SessionLabel, e.SessionID))
	if e.Host != "" {
		return m.cockpitLook().g(ckGlyphHost) + " " + printableTitle(e.Host) + ":" + s
	}
	return s
}

// ckMeta is the value of a meta key the agent reported, empty when none.
func ckMeta(e sidebarAgentEntry, key string) string {
	for _, t := range e.Meta {
		if t.Key == key {
			return printableTitle(t.Value)
		}
	}
	return ""
}

// ckDiffSegs is an agent's diff size, from the diff meta key ("+214/-37").
func ckDiffSegs(l ckLook, e sidebarAgentEntry) []ckSeg {
	d := ckMeta(e, "diff")
	if d == "" {
		return nil
	}
	add, del, _ := strings.Cut(d, "/")
	var segs []ckSeg
	if add != "" && add != "+0" {
		segs = append(segs, ckSeg{t: add, fg: l.add})
	}
	if del != "" && del != "-0" {
		if segs != nil {
			segs = append(segs, ckSeg{t: " "})
		}
		segs = append(segs, ckSeg{t: strings.Replace(del, "-", "−", 1), fg: l.del})
	}
	return segs
}

// ckRowLine2 is the second line of a row: the one thing worth reading. For
// an item, the verb of what it asks in the attention ink and its text. For a
// working agent, what it does now and its context. For one ready for review,
// its branch and pull request.
func (m *OS) ckRowLine2(l ckLook, r ckRow, e sidebarAgentEntry) (verb, rest string, ink color.Color, right []ckSeg) {
	pal := l.pal
	ink = pal.Attention
	if r.hasItem && r.group != ckReview {
		verb, rest = ckItemVerb(r.item)
		if r.item.Kind == session.AttentionErrored {
			ink = pal.Warn
		}
		if r.item.Kind == session.AttentionMail || r.item.Kind == session.AttentionResume {
			ink = pal.AccentBright
		}
		return verb, rest, ink, nil
	}
	ink = pal.FgDim
	if ctx := ckMeta(e, "context"); ctx != "" {
		right = []ckSeg{{t: "ctx " + ctx, fg: pal.FgMute}}
	}
	switch {
	case e.State == "working":
		if now := ckMeta(e, "now"); now != "" {
			if tool, target, ok := strings.Cut(now, ": "); ok {
				return tool, target, ink, right
			}
			return now, "", ink, right
		}
		if e.Message != "" {
			tool, target, ok := strings.Cut(printableTitle(e.Message), " ")
			if ok && tool != "" && tool[0] >= 'A' && tool[0] <= 'Z' {
				return tool, target, ink, right
			}
			return "", printableTitle(e.Message), ink, right
		}
		if e.Subagents > 0 {
			return "Task", subagentsWaitText(e.Subagents), ink, right
		}
		return "", "working", ink, right
	case sidebarAttention(e.State):
		return capitalize(agentRowStateWords(e)), printableTitle(e.Message), pal.Attention, right
	}
	br := m.ckBranch(e)
	if e.PR != "" {
		right = []ckSeg{{t: printableTitle(e.PR), fg: pal.FgDim}}
	}
	if br != "" {
		return "", l.g(ckGlyphBranch) + " " + br, ink, right
	}
	if r.hasItem {
		return "", printableTitle(r.item.Summary), ink, right
	}
	return "", printableTitle(e.Message), ink, right
}

// ckItemVerb splits an item into the verb of what it asks and its text:
// "Allow Bash" and "go test ./...".
func ckItemVerb(it session.AttentionItem) (verb, rest string) {
	sum := printableTitle(it.Summary)
	switch it.Kind {
	case session.AttentionApproval:
		if tool, text := risk.ParseSummary(it.Summary); tool != "" {
			return "Allow " + printableTitle(tool), printableTitle(text)
		}
		return "Allow", sum
	case session.AttentionPlan:
		return "Plan", inboxPlanRowText(it)
	case session.AttentionQuestion, session.AttentionAsk:
		return "Asks", sum
	case session.AttentionMail:
		if it.HeldID != 0 {
			return "Held mail", sum
		}
		return "Mail", sum
	case session.AttentionErrored:
		return "Error", sum
	case session.AttentionResume:
		return "Resume", sum
	case session.AttentionOutbox:
		return "To send", sum
	}
	return capitalize(inboxKindWords(it)), sum
}

// ckBranch is the branch of the agent's worktree session, empty when it is
// not in one.
func (m *OS) ckBranch(e sidebarAgentEntry) string {
	return m.Inbox.ck.branches[e.Host+"\x00"+e.SessionID]
}

// ckBranches reads every worktree session's branch once per rebuild of the
// rows, so a row does not walk the session tree.
func (m *OS) ckBranches() map[string]string {
	out := map[string]string{}
	for _, s := range m.BuildSessionTree().Sessions {
		if s.Worktree != nil && s.Worktree.Branch != "" {
			out[s.Host+"\x00"+s.ID] = printableTitle(s.Worktree.Branch)
		}
	}
	return out
}

// ckHasLine2 reports whether a row has a second line worth drawing. A row
// with nothing to say there is one line.
func (m *OS) ckHasLine2(r ckRow, e sidebarAgentEntry) bool {
	switch {
	case r.hasItem && r.group != ckReview:
		return true
	case r.agent < 0:
		return r.hasItem && r.item.Summary != ""
	case e.State == "working":
		return ckMeta(e, "now") != "" || e.Message != "" || e.Subagents > 0
	case sidebarAttention(e.State):
		return true
	}
	return m.ckBranch(e) != "" || e.PR != "" || e.Message != "" || (r.hasItem && r.item.Summary != "")
}

// drawCockpitStrip draws the one or two agents as name tabs over the focus,
// in place of the fleet column. It returns the row under it.
func (m *OS) drawCockpitStrip(c *ckCanvas, l ckLook, y, top int) int {
	ck := &m.Inbox.ck
	pal := l.pal
	sel, _ := m.cockpitSelected()
	x := 2
	for _, r := range ck.rows {
		if !r.selectable() {
			continue
		}
		var e sidebarAgentEntry
		if r.agent >= 0 {
			e = ck.agents[r.agent]
		}
		name, _, _, _ := m.ckRowFacts(r, e)
		g, gInk := l.ckMark(e.State, e.DoneSeen)
		if r.hasItem {
			g, gInk = l.ckItemMark(r.item)
		}
		bg, nameInk := l.panel, pal.FgDim
		if r.key == sel.key {
			bg, nameInk = l.cursor, pal.Fg
		}
		x0 := x
		x = c.text(x, y, 0, ckSeg{t: " " + g + " ", fg: gInk, bg: bg}, ckSeg{t: name + " ", fg: overlay.Readable(nameInk, bg), bg: bg, attrs: ckBold})
		c.mark(pal, x0, y, x-x0, 1, overlay.RowState{Cursor: r.key == sel.key, Focused: true})
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitRow, key: r.key})
		x += 2
	}
	return y + 1
}

// cockpitEmptyLines is what the cockpit says when it has no rows, nil when it
// has some.
func (m *OS) cockpitEmptyLines(rows []ckRow) []string {
	st := &m.Inbox
	for _, r := range rows {
		if r.selectable() {
			return nil
		}
	}
	switch {
	case st.Unsupported:
		return []string{"This daemon is too old for the agents view.", "Restart it with a newer tuios: tuios kill-server"}
	case !m.IsDaemonSession:
		return []string{"The agents view needs the daemon.", "Start a daemon session with: tuios new"}
	case st.Filter == session.AttentionMail:
		return []string{"No unread mail for you.", "m opens the mailbox, with every thread between agents too."}
	case st.Select != "" || st.agents.query != "":
		return []string{"No agent matches the search.", "/ changes the search. Clear the line and press enter to show everything."}
	case st.Filter != "" || st.ck.needsOnly:
		return []string{"Nothing needs you now.", "f shows every agent."}
	}
	if len(st.life.Snoozed) > 0 && !st.life.ShowSnoozed {
		return []string{"Nothing needs you now.", m.inboxSnoozedNote()}
	}
	lines := []string{"No agents yet.",
		"Start one with n, or run claude, codex or another agent in any pane. tuios finds it.",
		"Agents on other machines show here after tuios hosts add."}
	return lines
}

// drawCockpitEmpty draws the empty state, centred, with its two buttons.
func (m *OS) drawCockpitEmpty(c *ckCanvas, l ckLook, y0, y1, top int, lines []string) {
	ck := &m.Inbox.ck
	pal := l.pal
	w := min(c.w-4, 76)
	x := (c.w - w) / 2
	var wrapped [][]string
	n := 0
	for _, s := range lines {
		ws := wrapPlain(s, w)
		wrapped = append(wrapped, ws)
		n += len(ws)
	}
	y := max(y0, y0+(y1-y0-n-3)/2)
	for i, ws := range wrapped {
		for _, s := range ws {
			ink, attrs := pal.FgDim, uint8(0)
			if i == 0 {
				ink, attrs = pal.Fg, ckBold
			}
			c.text(x, y, x+w, ckSeg{t: s, fg: ink, attrs: attrs})
			y++
		}
	}
	if lines[0] != "No agents yet." {
		return
	}
	y++
	bx := x
	for _, b := range []struct{ key, label, action string }{{"n", "New agent", "new"}, {"", "Supported agents", "agents"}} {
		x0 := bx
		if b.key != "" {
			bx = c.text(bx, y, 0, ckSeg{t: " " + b.key + " ", fg: pal.PillFg, bg: pal.Accent, attrs: ckBold})
		}
		bx = c.text(bx, y, 0, ckSeg{t: " " + b.label + " ", fg: overlay.Readable(pal.Fg, pal.Card), bg: pal.Card})
		c.mark(pal, x0, y, bx-x0, 1, ckOn)
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: bx, y1: top + y + 1, kind: ckHitAction, action: b.action})
		bx += 3
	}
}

// drawCockpitHints draws the key hints along the foot, and how many agents
// on the right.
func (m *OS) drawCockpitHints(c *ckCanvas, l ckLook, y int, now time.Time) {
	pal := l.pal
	hints := m.cockpitHints(now)
	right := ""
	if n := len(m.Inbox.ck.agents); n > 0 {
		right = strconv.Itoa(n) + " agent"
		if n != 1 {
			right += "s"
		}
		hosts := map[string]bool{}
		for _, e := range m.Inbox.ck.agents {
			hosts[e.Host] = true
		}
		if len(hosts) > 1 {
			right += " on " + strconv.Itoa(len(hosts)) + " machines"
		}
	}
	room := c.w - 4
	if right != "" {
		room -= ansi.StringWidth(right) + 3
	}
	width := func(hs []overlay.Hint) int {
		n := 0
		for i, h := range hs {
			if i > 0 {
				n += 3
			}
			n += ansi.StringWidth(h.Key) + 1 + ansi.StringWidth(h.Label)
		}
		return n
	}
	for len(hints) > 1 && width(hints) > room {
		hints = slices.Delete(hints, len(hints)-2, len(hints)-1)
	}
	x := 2
	for _, h := range hints {
		x = c.text(x, y, 2+room, ckSeg{t: h.Key, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + h.Label, fg: pal.FgMute}) + 3
	}
	if right != "" {
		c.textR(c.w-2, y, ckSeg{t: right, fg: pal.FgMute})
	}
}

// cockpitHints are the keys for the row under the cursor, then the keys that
// work anywhere. esc stays last, so it is never dropped.
func (m *OS) cockpitHints(now time.Time) []overlay.Hint {
	st := &m.Inbox
	switch {
	case st.selectEditing:
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: "apply"}, {Key: "esc", Label: "cancel"}}
	case m.InboxSnoozePicking():
		return inboxSnoozeHints()
	case st.agents.sheet != nil:
		return m.bulkSheetHints()
	case len(st.agents.marks) > 0:
		return m.bulkHints()
	case st.Peek != nil:
		return nil
	}
	if hints, ok := m.inboxReplyHints(); ok {
		return hints
	}
	if m.InboxReasonOpen() {
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	var hints []overlay.Hint
	r, ok := m.cockpitSelected()
	if ok {
		if it, held := m.cockpitHeldItem(); held && !m.inboxCut(it) {
			words := [4]string{"allow", "always", "reject", "say why"}
			if it.Kind == session.AttentionPlan {
				words = [4]string{"approve", "accept edits", "keep planning", "say why"}
			}
			if slices.Contains(it.Options, session.ApprovalOnce) {
				hints = append(hints, m.keyHints(config.ActionInboxAllow, words[0])...)
			}
			if slices.Contains(it.Options, session.ApprovalAlways) {
				hints = append(hints, m.keyHints(config.ActionInboxAllowAlways, words[1])...)
			}
			if slices.Contains(it.Options, session.ApprovalDeny) {
				hints = append(hints, m.keyHints(config.ActionInboxReply, words[2])...)
			}
			if it.DenyMessage {
				hints = append(hints, m.keyHints(config.ActionInboxDenyReason, words[3])...)
			}
		}
		// Dismiss comes before going to the pane: a narrow footer gives
		// up the pane, which enter offers anyway, first.
		if r.hasItem {
			hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss")...)
		}
		if r.hasItem && r.item.Kind == session.AttentionAsk && r.item.RequestID != "" {
			hints = append(hints, overlay.Hint{Key: inboxAskKeys(r.item), Label: "answer"})
		}
		goLabel := "open pane"
		if r.hasItem && r.item.Kind == session.AttentionMail {
			goLabel = "open"
		}
		hints = append(hints, m.keyHints(config.ActionInboxGo, goLabel)...)
		if r.hasItem && inboxPeekable(r.item) && r.item.RequestID == "" {
			hints = append(hints, m.keyHints(config.ActionInboxPeek, "peek")...)
		}
		blocked := r.agent >= 0 && m.Inbox.ck.agents[r.agent].State == "needs_input"
		if _, held := m.cockpitHeldItem(); !held && (!blocked || r.item.Kind == session.AttentionMail) && m.inboxReplySupported() {
			hints = append(hints, m.keyHints(config.ActionInboxReply, "reply")...)
		}
		if r.hasItem {
			switch {
			case r.item.HeldID != 0:
				hints = append(hints, m.keyHints(config.ActionInboxPassOn, "pass on")...)
			case r.item.Kind == session.AttentionResume:
				hints = append(hints, m.keyHints(config.ActionInboxResume, "resume")...)
			}
			switch {
			case !m.inboxMarkSupported():
			case r.item.SnoozedUntil != 0:
				hints = append(hints, m.keyHints(config.ActionInboxSnooze, "wake")...)
			case inboxSnoozeRefusal(r.item) == "":
				hints = append(hints, m.keyHints(config.ActionInboxSnooze, "snooze")...)
			}
		}
		if r.hasItem && r.item.Kind == session.AttentionMail {
			hints = append(hints, m.keyHints(config.ActionInboxMailbox, "mailbox")...)
		}
	}
	hints = append(hints, m.keyHints(config.ActionInboxNewAgent, "new agent", config.ActionInboxReview, "review",
		config.ActionInboxFilter, "filter", config.ActionInboxSelect, "search")...)
	hints = append(hints, overlay.Hint{Key: "?", Label: "all keys"})
	return append(hints, m.keyHints(config.ActionInboxClose, "close")...)
}

// drawFocus draws the selected row's work: its header, the thread, the card
// of what it asks, and the reply line.
func (m *OS) drawFocus(c *ckCanvas, l ckLook, x, y, w, y1, top int, now time.Time) {
	st := &m.Inbox
	pal := l.pal
	r, ok := m.cockpitSelected()
	if !ok {
		return
	}
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = st.ck.agents[r.agent]
	}
	right := x + w
	name, harness, place, _ := m.ckRowFacts(r, e)
	hx := c.text(x, y, right, ckSeg{t: name, fg: pal.Fg, attrs: ckBold})
	if harness != "" {
		hx = c.text(hx, y, right, ckSeg{t: "   " + harness, fg: pal.FgDim})
	}
	if model := ckMeta(e, "model"); model != "" {
		c.text(hx, y, right, ckSeg{t: " · " + model, fg: pal.FgMute})
	}
	m.drawStatePill(c, l, r, e, right, y)
	meta := []ckSeg{}
	if br := m.ckBranch(e); br != "" {
		meta = append(meta, ckSeg{t: l.g(ckGlyphBranch) + " " + br + "   ", fg: pal.FgDim})
	}
	meta = append(meta, ckSeg{t: place + "   ", fg: pal.FgMute})
	if d := ckDiffSegs(l, e); d != nil {
		meta = append(append(meta, d...), ckSeg{t: "   "})
	}
	for _, k := range []string{"context", "cost"} {
		if v := ckMeta(e, k); v != "" {
			if k == "context" {
				v = "ctx " + v
			}
			meta = append(meta, ckSeg{t: v + "   ", fg: pal.FgMute})
		}
	}
	if r.agent >= 0 && e.StateAt > 0 {
		meta = append(meta, ckSeg{t: sidebarStateWords(e.State) + " " + inboxWait(e.StateAt, now), fg: pal.FgMute})
	}
	c.text(x, y+1, right, meta...)
	// Under 30 rows the header gives its gaps to the body.
	rule := y + 3
	if c.h < 30 {
		rule = y + 2
	}
	tabs := m.cockpitTabs(e, r)
	if len(tabs) > 0 {
		rule++
		ty, tx := rule-1, x
		for i, tab := range tabs {
			x0 := tx
			if i == m.cockpitTabIndex() {
				tx = c.text(tx, ty, right, ckSeg{t: tab, fg: pal.Fg, attrs: ckBold})
			} else {
				tx = c.text(tx, ty, right, ckSeg{t: tab, fg: pal.FgMute})
			}
			st.ck.hits = append(st.ck.hits, ckHit{x0: x0, y0: top + ty, x1: tx, y1: top + ty + 1, kind: ckHitTab, group: i})
			if i == m.cockpitTabIndex() {
				defer c.hline(x0, rule, tx-x0, "━", pal.Accent)
			}
			tx += 4
		}
	}
	c.hline(x, rule, w, hzGlyph(), l.edge)
	bodyTop := rule + 2
	if c.h < 30 {
		bodyTop = rule + 1
	}

	// The foot: the reply area, open or closed. A reply to an agent on a
	// prompt is refused, so the area is not drawn for one: the card answers
	// the prompt.
	foot := y1
	if st.reply.editor != nil || (r.agent >= 0 && e.State != "needs_input") || (r.hasItem && r.item.Kind == session.AttentionMail) {
		foot = m.drawReplyArea(c, l, e, name, x, w, bodyTop, y1, top)
	}
	bodyBot := foot - 1
	if foot == y1 {
		// No reply line: the body runs to the foot.
		bodyBot = foot
	}

	switch {
	case st.Peek != nil:
		content, _, _ := m.renderInboxPeek(st.Peek, now)
		for i, s := range strings.Split(content, "\n") {
			if bodyTop+i >= bodyBot {
				break
			}
			c.draw(x, bodyTop+i, w, s)
		}
		return
	case st.agents.sheet != nil:
		for i, s := range m.bulkSheetLines(w, bodyBot-bodyTop) {
			c.draw(x, bodyTop+i, w, s)
		}
		return
	}
	// The card of what the row asks is drawn on every tab: a tab never hides
	// an approval the keys still answer.
	cardTop := bodyBot
	if r.hasItem {
		cardTop = m.drawItemCard(c, l, r, x, w, bodyTop, bodyBot, top, now)
	}
	// The side column's pieces as focus tabs, narrower than it, above the
	// card.
	switch m.cockpitTab() {
	case "Checkpoints":
		if room := cardTop - 1 - bodyTop; room >= 2 {
			m.drawCheckpoints(c, l, e, x, bodyTop, w, max((room-1)/2, 1))
		}
		return
	case "Live":
		if room := cardTop - 1 - bodyTop; room >= 3 {
			m.drawLiveBox(c, l, e, x, bodyTop, w, room)
		}
		return
	}
	if r.agent >= 0 && cardTop-bodyTop > 1 {
		room := cardTop - bodyTop - 1
		t := st.threads.byKey[agentKey(e)]
		scroll := 0
		if t != nil {
			scroll = t.scroll
		}
		lines := m.threadBody(e, w, room+scroll, now)
		var hits []string
		if t != nil && len(t.hits) == len(lines) {
			hits = t.hits
		}
		if t != nil {
			// The thread scrolls no further than its first line.
			t.scroll = min(scroll, max(len(lines)-room, 0))
			scroll = t.scroll
		}
		end := len(lines) - scroll
		start := max(end-room, 0)
		ty := cardTop - 1 - (end - start)
		for i := start; i < end; i++ {
			c.draw(x, ty+i-start, w, lines[i])
			if hits != nil && hits[i] != "" {
				st.ck.hits = append(st.ck.hits, ckHit{x0: x, y0: top + ty + i - start, x1: right, y1: top + ty + i - start + 1, kind: ckHitAction, action: hits[i]})
			}
		}
	}
}

// cockpitTabs are the focus tabs for the selected row: the thread and the
// last turn's changes, for an agent whose transcript tuios reads.
func (m *OS) cockpitTabs(e sidebarAgentEntry, r ckRow) []string {
	if r.agent < 0 || e.Host != "" {
		return nil
	}
	// Narrower than the side column, its pieces are tabs.
	var side []string
	if m.GetRenderWidth() < ckWide {
		side = []string{"Checkpoints", "Live"}
	}
	if !m.threadReadsTranscript(e) {
		if side == nil {
			return nil
		}
		return append([]string{"Thread"}, side...)
	}
	n := 0
	if t := m.Inbox.threads.byKey[agentKey(e)]; t != nil {
		for i := len(t.entries) - 1; i >= 0; i-- {
			en := t.entries[i]
			if en.Kind == "text" && en.Role == "user" {
				break
			}
			if en.Kind == "tool_call" && en.Diff != nil {
				n++
			}
		}
	}
	changes := "Changes"
	if n > 0 {
		changes += " " + strconv.Itoa(n)
	}
	return append([]string{"Thread", changes}, side...)
}

// ckApprovalLines are a held approval's command, wrapped for the inset, and
// the lines under it: where it runs, what always adds, the risk rules that
// matched, and the second press a risky allow waits for.
func (m *OS) ckApprovalLines(it session.AttentionItem, width int, now time.Time) (inset, detail []string) {
	tool, text := risk.ParseSummary(it.Summary)
	if tool == "" {
		text = it.Summary
	} else if tool != "Bash" {
		text = tool + "  " + text
	}
	inset = wrapPlain(printableTitle(text), max(width-6, 8))
	detail = append(detail, "  in "+m.inboxWhere(it))
	if slices.Contains(it.Options, session.ApprovalAlways) && len(it.AlwaysScope) > 0 {
		detail = append(detail, "  Always also allows from now on:")
		for _, rule := range it.AlwaysScope {
			detail = append(detail, wrapPlain("    "+printableTitle(rule), width)...)
		}
	}
	detail = append(detail, inboxRiskLines(it, width)...)
	if key, armed := m.inboxArmedFor(it, now); armed {
		press := m.Inbox.approvals.armed.press
		if press == "" {
			press = inboxDecisionKey(key)
		}
		for _, l := range wrapPlain("Press "+press+" again "+m.inboxArmedUntil()+" to allow "+printableTitle(inboxRiskTarget(it)), width) {
			detail = append(detail, "  "+l)
		}
	}
	return inset, detail
}

// drawStatePill draws the focus header's state, its word beside its colour.
func (m *OS) drawStatePill(c *ckCanvas, l ckLook, r ckRow, e sidebarAgentEntry, right, y int) {
	pal := l.pal
	g, ink := l.ckMark(e.State, e.DoneSeen)
	words, bg := sidebarStateWords(e.State), l.panel
	switch {
	case r.hasItem && r.group == ckNeeds:
		g, ink = l.ckItemMark(r.item)
		words, bg = "needs you", l.attnTint
		if r.item.Kind == session.AttentionMail {
			words, bg = "mail", l.panel
		}
	case e.State == "needs_input":
		words, bg = "needs you", l.attnTint
	case e.State == "working":
		bg = l.busyTint
	case e.State == "done" && !e.DoneSeen:
		words, bg = "ready for review", l.okTint
	case e.State == "errored":
		words, bg = "error", l.warnTint
	}
	if r.agent < 0 && !r.hasItem {
		return
	}
	x := c.textR(right, y, ckSeg{t: " " + g + " " + words + " ", fg: overlay.Readable(ink, bg), bg: bg, attrs: ckBold})
	c.mark(pal, x, y, right-x, 1, ckOn)
}

// drawItemCard draws the card of what the row's item asks, at the bottom of
// the body, and returns its top row. A held approval or plan is answered from
// its buttons, a question from its numbered answers.
func (m *OS) drawItemCard(c *ckCanvas, l ckLook, r ckRow, x, w, y0, y1, top int, now time.Time) int {
	st := &m.Inbox
	pal := l.pal
	it := r.item
	held := inboxHeld(it) || (it.Kind == session.AttentionAsk && it.RequestID != "")
	inner := w - 6
	// Under 30 rows the card gives its gaps around the title to the detail.
	base := 3
	if c.h < 30 {
		base = 1
	}
	// The room the card's detail has: the body less the card's title, its
	// gaps and its buttons.
	st.ck.cardRoom = max(y1-y0-base-2, 0)
	var detail, inset []string
	plain := it.Kind == session.AttentionApproval && st.approvals.reason == nil && inboxShowsWhole(it)
	// cut is whether any of what the card asks about is left off. A held
	// approval or question shown cut is never counted as read.
	cut := false
	if plain {
		inset, detail = m.ckApprovalLines(it, inner, now)
		cut = len(inset) > 4
		if cut {
			inset = append(inset[:3], overlay.Ellipsis())
		}
	} else if fn, ok := m.inboxDetailExtra(it); ok {
		detail = fn(inner + 2)
	} else if it.Kind == session.AttentionAsk && it.RequestID != "" {
		detail = inboxAskDetail(it, inner+2)
	} else if held {
		detail = inboxApprovalDetail(it, inner+2)
	} else if fn, ok := m.inboxRecapDetail(it); ok {
		detail = fn(inner + 2)
	} else {
		for _, l := range wrapPlain(printableTitle(it.Summary), inner) {
			detail = append(detail, "  "+l)
		}
	}
	type btn struct{ key, label, action string }
	var btns []btn
	switch {
	case st.approvals.reason != nil:
	case it.Kind == session.AttentionAsk && it.RequestID != "":
		for i, o := range it.Options {
			if i == 9 {
				break
			}
			btns = append(btns, btn{strconv.Itoa(i + 1), ckTrunc(printableTitle(o), 24), strconv.Itoa(i + 1)})
		}
	case held:
		labels := [4]string{"Allow once", "Always", "Reject", "Reject and say why"}
		if it.Kind == session.AttentionPlan {
			labels = [4]string{"Approve", "Approve, accept edits", "Keep planning", "Keep planning, say why"}
		} else if len(it.AlwaysScope) > 0 {
			labels[1] = "Always for " + ckTrunc(printableTitle(it.AlwaysScope[0]), 20)
		}
		for i, d := range [3]string{session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny} {
			if !slices.Contains(it.Options, d) {
				continue
			}
			// The button names the key the config binds; its action is the
			// letter the click answers with.
			action := [3]string{"a", "A", "r"}[i]
			key := m.inboxKeyOr([3]string{config.ActionInboxAllow, config.ActionInboxAllowAlways, config.ActionInboxReply}[i], action)
			btns = append(btns, btn{key, labels[i], action})
		}
		if it.DenyMessage {
			btns = append(btns, btn{m.inboxKeyOr(config.ActionInboxDenyReason, "R"), labels[3], "R"})
		}
	}
	size := func() int {
		h := base + len(detail)
		if len(inset) > 0 {
			h += len(inset) + 1
		}
		if len(btns) > 0 {
			h += 2
		}
		return h
	}
	h := size()
	// A card taller than the body keeps the end of its detail, which is
	// where a cut plan says so.
	if over := h - (y1 - y0); over > 0 && len(detail) > 0 {
		if len(btns) > 0 && over > len(detail)-1 {
			// Too short for the buttons too: the keys still answer.
			btns, h, over = nil, h-2, over-2
		}
		keep := max(len(detail)-max(over, 0), 1)
		cut = cut || keep < len(detail)
		h -= len(detail) - keep
		detail = detail[len(detail)-keep:]
	}
	cut = cut || h > y1-y0
	answerable := held
	if cut && answerable {
		// What the card asks is not all on screen, so it is not answered
		// here: the pane shows it whole.
		inset, btns = nil, nil
		key := m.inboxKeyOr(config.ActionInboxGo, "enter")
		if key == overlay.EnterGlyph {
			key = "enter"
		}
		words := "Too long to show here. Press " + key + " to answer in the pane."
		if it.Kind == session.AttentionPlan {
			words = "The screen is too short to show the whole plan. Enter answers it in the pane."
		}
		detail = wrapPlain(words, max(inner, 8))
		for i := range detail {
			detail[i] = "  " + detail[i]
		}
		h = size()
	}
	st.ck.cutKey = ""
	if cut && answerable {
		st.ck.cutKey = inboxShownKey(it)
	}
	m.noteInboxShown(it, held && inboxShowsWhole(it) && !(cut && answerable), now)
	cy := max(y1-h, y0)
	c.fill(x, cy, w, y1-cy, l.panel)
	barInk := pal.Attention
	if !held {
		_, barInk = l.ckItemMark(it)
	}
	for i := cy; i < y1; i++ {
		c.text(x, i, 0, ckSeg{t: "▌", fg: barInk})
	}
	verb, _ := ckItemVerb(it)
	title := verb
	switch {
	case held && it.Kind == session.AttentionPlan:
		title = "Approve the plan?"
	case held && it.Kind != session.AttentionAsk:
		title += "?"
	}
	g, gInk := l.ckItemMark(it)
	// On a body too short for the card's gaps, the title takes the top row
	// and the detail the rows under it.
	ty, by := cy+1, cy+3
	if base == 1 || y1-cy < h {
		ty, by = cy, cy+1
	}
	tx := c.text(x+3, ty, x+w-2, ckSeg{t: g + " ", fg: gInk}, ckSeg{t: title, fg: pal.Fg, attrs: ckBold})
	when := " waits " + inboxWait(it.Since, now)
	if !held {
		when = sepWord() + inboxWait(it.Since, now)
	}
	switch {
	case it.Stale:
		when = ", " + inboxSeen(it.SeenAt, now)
	case it.SnoozedUntil != 0:
		when = ", snoozed " + inboxSnoozedWhen(it.SnoozedUntil, now)
	}
	c.text(tx, ty, x+w-14, ckSeg{t: "   " + inboxWho(it) + when, fg: pal.FgMute})
	if inboxRisky(it) {
		c.textR(x+w-2, ty, ckSeg{t: " risky ", fg: overlay.Readable(pal.Warn, l.warnTint), bg: l.warnTint, attrs: ckBold})
	}
	yy := by
	for i, s := range inset {
		if yy >= y1 {
			break
		}
		c.fill(x+3, yy, w-6, 1, l.canvas)
		lead := "  "
		if i == 0 {
			lead = "$ "
			if tool, _ := risk.ParseSummary(it.Summary); tool != "Bash" {
				lead = ""
			}
		}
		c.text(x+4, yy, x+w-4, ckSeg{t: lead, fg: pal.FgMute}, ckSeg{t: s, fg: pal.Fg})
		yy++
	}
	if len(inset) > 0 {
		yy++
	}
	for _, s := range detail {
		if yy >= y1 {
			break
		}
		if strings.Contains(s, "\x1b") {
			c.draw(x+1, yy, w-2, s)
		} else {
			ink := pal.Fg
			if plain {
				ink = pal.FgDim
			}
			c.text(x+1, yy, x+w-2, ckSeg{t: s, fg: ink})
		}
		yy++
	}
	if len(btns) > 0 && yy+1 < y1 {
		bx := x + 3
		for _, b := range btns {
			x0 := bx
			keyBg, keyInk, labelBg := pal.Card, pal.AccentBright, l.cursor
			if b.action == "a" || b.action == "1" {
				keyBg, keyInk, labelBg = pal.Accent, pal.PillFg, pal.Accent
			}
			labelInk := pal.Fg
			if labelBg == pal.Accent {
				labelInk = pal.PillFg
			}
			if bx+ckWidth(ckSeg{t: " " + b.key + "  " + b.label + " "}) > x+w-2 {
				break
			}
			bx = c.text(bx, yy+1, 0, ckSeg{t: " " + b.key + " ", fg: overlay.Readable(keyInk, keyBg), bg: keyBg, attrs: ckBold},
				ckSeg{t: " " + b.label + " ", fg: overlay.Readable(labelInk, labelBg), bg: labelBg})
			c.mark(pal, x0, yy+1, bx-x0, 1, ckOn)
			st.ck.hits = append(st.ck.hits, ckHit{x0: x0, y0: top + yy + 1, x1: bx, y1: top + yy + 2, kind: ckHitAction, action: b.action})
			bx += 2
		}
	}
	return cy
}
