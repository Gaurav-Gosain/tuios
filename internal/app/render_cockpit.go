package app

import (
	"image/color"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const (
	ckOneColumn = 80
	ckTwoLine   = 120
	ckWide      = 170
)

type ckLook struct {
	pal                    overlay.Palette
	canvas, panel, cursor  color.Color
	hover, edge, edgePanel color.Color
	add, del               color.Color
	attnTint, busyTint     color.Color
	okTint, warnTint       color.Color
	accTint                color.Color
	infoTint               color.Color
	nerd, ascii            bool
}

type ckLookKey struct {
	theme                     uint64
	depth                     overlay.Depth
	ascii, plain              bool
	canvas, panel, fg, accent color.Color
	glyphs                    *theme.GlyphSet
}

func (m *OS) cockpitLook() ckLook {
	ck := &m.Inbox.ck
	pal := m.cockpitPalette()
	key := ckLookKey{theme: theme.Generation(), depth: pal.Depth, ascii: overlay.UseASCII(), plain: m.Settings.NoNerdFont,
		canvas: pal.Canvas, panel: pal.Panel, fg: pal.Fg, accent: pal.Accent, glyphs: theme.Glyphs()}
	if ck.lookAt != key || ck.look.pal.Canvas == nil {
		ck.look, ck.lookAt = m.buildCockpitLook(pal), key
		ck.lookGen++
	}
	return ck.look
}

func (m *OS) cockpitPalette() overlay.Palette {
	if theme.Current() == nil && m.host.bg != nil && m.host.light {
		ck := &m.Inbox.ck
		p := m.groundUI()
		key := ckHostPalKey{host: m.host.gen, theme: theme.Generation(), depth: p.Depth, panel: p.Panel}
		if hp := &ck.hostPal; hp.ok && hp.at == key {
			return hp.pal
		}
		for _, c := range []*color.Color{&p.Success, &p.Warn, &p.Warning, &p.Attention, &p.Busy, &p.Info, &p.Accent, &p.AccentBright, &p.Selected} {
			if *c != nil {
				*c = overlay.ReadableAt(*c, p.Panel, overlay.MarkFloor)
			}
		}
		ck.hostPal = ckHostPal{pal: p, at: key, ok: true}
		return p
	}
	return theme.UI()
}

type ckHostPal struct {
	pal overlay.Palette
	at  ckHostPalKey
	ok  bool
}

type ckHostPalKey struct {
	host, theme uint64
	depth       overlay.Depth
	panel       color.Color
}

func (m *OS) cockpitLookGen() uint64 {
	m.cockpitLook()
	return m.Inbox.ck.lookGen
}

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
	l.accTint, l.infoTint = tint(pal.Accent), tint(pal.Info)
	switch pal.Depth {
	case overlay.Depth256:
		l.cursor = overlay.Apart256(l.cursor, pal.Panel)
		l.hover = overlay.Apart256(l.hover, pal.Panel)
		l.add, l.del = overlay.To256(l.add), overlay.To256(l.del)
		l.attnTint, l.busyTint = overlay.To256(l.attnTint), overlay.To256(l.busyTint)
		l.okTint, l.warnTint = overlay.To256(l.okTint), overlay.To256(l.warnTint)
		l.accTint, l.infoTint = overlay.To256(l.accTint), overlay.To256(l.infoTint)
	case overlay.Depth16:
		l.cursor, l.hover = overlay.NoColor, overlay.NoColor
		l.attnTint, l.busyTint, l.okTint, l.warnTint = overlay.NoColor, overlay.NoColor, overlay.NoColor, overlay.NoColor
		l.accTint, l.infoTint = overlay.NoColor, overlay.NoColor
		l.add, l.del = pal.Success, pal.Warn
		l.edge, l.edgePanel = pal.Edge, pal.Edge
	}
	p := &l.pal
	for _, c := range []*color.Color{&l.canvas, &l.panel, &l.cursor, &l.hover, &l.edge, &l.edgePanel, &l.add, &l.del,
		&l.attnTint, &l.busyTint, &l.okTint, &l.warnTint, &l.accTint, &p.Canvas, &p.Panel, &p.Surface, &p.Card, &p.Fg, &p.FgDim,
		&p.FgMute, &p.Accent, &p.AccentBright, &p.PillFg, &p.Warn, &p.Success, &p.Warning, &p.Attention, &p.Busy, &p.Edge} {
		*c = ckFlat(*c)
	}
	return l
}

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

type ckGlyph struct{ nerd, uni, ascii string }

var (
	ckGlyphNeeds   = ckGlyph{"●", "●", "!"}
	ckGlyphWorking = ckGlyph{"", "»", "~"}
	ckGlyphReview  = ckGlyph{"", "√", "v"}
	ckGlyphFailed  = ckGlyph{"", "×", "x"}
	ckGlyphIdle    = ckGlyph{"○", "○", "o"}
	ckGlyphLimit   = ckGlyph{"\U000f051f", "▼", "="}
	ckGlyphHost    = ckGlyph{"\U000f048b", "@", "@"}
	ckGlyphBranch  = ckGlyph{"", "", "br"}
	ckGlyphFold    = ckGlyph{"\ueab6", "˃", ">"}
	ckGlyphMarked  = ckGlyph{"\uf14a", "•", "*"}
	ckGlyphResume  = ckGlyph{"\uf01e", "»", ">"}
	ckGlyphImage   = ckGlyph{"\U000f02e9", "[img]", "[img]"}
	ckGlyphPR      = ckGlyph{"\uf407", "PR", "PR"}
	ckGlyphFan     = ckGlyph{"\uf192", "+", "+"}
	ckGlyphUnseen  = ckGlyph{"•", "•", "*"}
	ckGlyphPin     = ckGlyph{"\uf435", "↑", "^"}
)

func (l ckLook) g(gl ckGlyph) string {
	switch {
	case l.ascii:
		return gl.ascii
	case l.nerd:
		return gl.nerd
	}
	return gl.uni
}

func (l ckLook) ckStateMark(state string, def ckGlyph) string {
	if g := config.SetAgentStateMark(state, l.ascii); g != "" {
		return g
	}
	return l.g(def)
}

func (l ckLook) ckMark(state string, doneSeen bool) (string, color.Color) {
	switch state {
	case "needs_input":
		return l.ckStateMark(state, ckGlyphNeeds), l.pal.Attention
	case "errored":
		return l.ckStateMark(state, ckGlyphFailed), l.pal.Warn
	case "limit_reached":
		return l.ckStateMark(state, ckGlyphLimit), l.pal.Warn
	case "working":
		return l.ckStateMark(state, ckGlyphWorking), l.pal.Busy
	case "done":
		if !doneSeen {
			return l.ckStateMark(state, ckGlyphReview), l.pal.Success
		}
	case "unknown":
		if g := config.SetAgentStateMark(state, l.ascii); g != "" {
			return g, l.pal.FgMute
		}
	}
	return l.ckStateMark("idle", ckGlyphIdle), l.pal.FgMute
}

func (l ckLook) ckItemMark(it session.AttentionItem) (string, color.Color) {
	switch it.Kind {
	case session.AttentionMail:
		return sidebarMailGlyph(), l.pal.AccentBright
	case session.AttentionErrored:
		return l.ckStateMark("errored", ckGlyphFailed), l.pal.Warn
	case session.AttentionFinished:
		return l.ckStateMark("done", ckGlyphReview), l.pal.Success
	case session.AttentionResume:
		if len(it.RiskyFlags) > 0 {
			return l.g(ckGlyphResume), l.pal.Warn
		}
		return l.g(ckGlyphResume), l.pal.AccentBright
	case session.AttentionOutbox, session.AttentionHeldMail:
		return inboxKindGlyph(it.Kind), l.pal.AccentBright
	}
	return l.ckStateMark("needs_input", ckGlyphNeeds), l.pal.Attention
}

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
	tml, art             uint64
	link                 ckLinkRun
}

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
	st.agents.inFrame = true
	defer func() { st.agents.inFrame = false }()
	key := ckFrameKey{
		marks: st.agents.marksGen, selectErr: st.selectErr, look: ck.lookGen,
		w: w, h: h, top: top, bottom: res.Bottom, gen: ck.gen, inbox: st.Gen, mail: m.AgentMail.Gen,
		list: st.agents.listAt, minute: now.Unix() / 60, theme: theme.Generation(), live: st.Live, liveGen: ck.liveGen,
		open: st.threads.open, daemon: m.IsDaemonSession, keys: m.KeybindRegistry, hosts: m.federationGen,
		depth: l.pal.Depth, ascii: l.ascii, nerd: l.nerd, snoozes: st.life.ShowSnoozed,
		tml: m.tml.gen + m.tml.setGen<<32, art: ck.art.gen,
		link: ck.linkHover,
	}
	if e, ok := m.cockpitAgent(); ok {
		if t := st.threads.byKey[agentKey(e)]; t != nil {
			key.thread = t.gen + 1
		}
	}
	if ed := st.reply.editor; ed != nil {
		ed.comp.Render(max(ck.edW-4, 8), replyStyles(l), l.panel)
		key.editor = ed.comp.Drawn() + 1
	}
	transient := m.CockpitTop().transient() || ck.kb.drag != nil || st.approvals.answer.step == answerArmed
	if !transient && ck.frame != "" && ck.frameAt == key {
		return ck.frame, top, w, h
	}
	m.tickStats.Cockpit++
	ck.canvas = ck.canvas.reset(w, h, l.canvas)
	ck.canvasTop = top
	c := ck.canvas
	ck.hits = ck.hits[:0]
	ck.code = ckCodeView{}
	ck.fleetW = 0
	ck.liveShown = false
	if ck.spawn != nil {
		m.drawCockpitUnderSheet(c, l, top, now, key)
	} else {
		m.drawCockpit(c, l, top, now)
		m.drawCockpitMenu(c, l, top)
		m.drawCockpitLinkHover(c, top)
	}
	ck.frame, ck.frameAt = c.render(), key
	if transient {
		ck.frameAt = ckFrameKey{}
	}
	return ck.frame, top, w, h
}

func (m *OS) drawCockpitUnderSheet(c *ckCanvas, l ckLook, top int, now time.Time, key ckFrameKey) {
	ck := &m.Inbox.ck
	if ck.spawnBg == nil || ck.spawnBgAt != key || len(ck.spawnBg) != c.h {
		m.drawCockpit(c, l, top, now)
		if ck.spawnFade == nil || ck.spawnBgAt.look != key.look {
			ck.spawnFade = map[color.Color]color.Color{}
		}
		c.dim(l, ck.spawnFade)
		ck.spawnBg = ck.spawnBg[:0]
		for y := range c.h {
			ck.spawnBg = append(ck.spawnBg, slices.Clone(c.buf.Lines[y]))
		}
		ck.spawnBgAt = key
	} else {
		for y := range c.h {
			copy(c.buf.Lines[y], ck.spawnBg[y])
		}
	}
	ck.hits = ck.hits[:0]
	ck.code = ckCodeView{}
	m.drawSpawnSheet(c, l, top)
}

func (m *OS) drawCockpit(c *ckCanvas, l ckLook, top int, now time.Time) {
	st := &m.Inbox
	ck := &st.ck
	ck.stripShown = false
	ck.regionBox = [ckRegions]ckRegionBox{}
	ck.div.x0, ck.div.x1 = 0, 0
	ck.sideRoom, ck.focusShown = true, true
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
	agents, folded := 0, false
	for _, r := range rows {
		if r.selectable() {
			agents++
		}
		folded = folded || r.folded
	}
	m.drawCockpitHeader(c, l, hdrY, top)

	if m.kanbanOn() && m.kanbanDrawable() {
		pv := &ck.kb.pv
		pv.box, pv.edge.x0, pv.edge.x1 = overlay.Rect{}, 0, 0
		pw, side := m.kbPreviewSide(c.w)
		kw := c.w
		if side {
			kw = c.w - pw - 1
		}
		m.drawKanban(c, l, colTop, colBot, top, kw)
		if m.kbPreviewShown() {
			sheet := !side
			if pv.zoom {
				m.drawKanbanPreview(c, l, 1, c.w-1, colTop, colBot, top, false, now)
				pv.edge.x0, pv.edge.x1 = 0, 0
			} else {
				m.drawKanbanPreview(c, l, kw+1, pw, colTop, colBot, top, sheet, now)
			}
		}
		m.drawCockpitHints(c, l, hintsY, now)
		return
	}
	if empty := m.cockpitEmptyLines(rows); empty != nil {
		m.drawCockpitEmpty(c, l, colTop, colBot, top, empty)
		m.drawCockpitHints(c, l, hintsY, now)
		return
	}

	fleetW := m.cockpitFleetWidth(c.w)
	strip := c.w >= ckOneColumn && !folded && !ck.board && !ck.needsOnly && ck.chip == 0 && st.Filter == "" && st.Select == "" && st.agents.query == ""
	switch m.ckListMode() {
	case config.AgentsViewListStrip:
		strip = strip && m.ckStripWidth(l) <= c.w-4
	case config.AgentsViewListAuto:
		strip = strip && agents <= 2
	default:
		strip = false
	}
	ck.stripShown = strip
	focusX, focusW := fleetW+3, c.w-fleetW-5
	switch {
	case strip:
		focusX, focusW = 2, c.w-4
	case c.w < ckOneColumn && st.threads.open:
		focusX, focusW = 2, c.w-4
	case c.w < ckOneColumn:
		focusW = 0
	}
	ck.sideRoom, ck.focusShown = cockpitSideRoom(c.w, focusW), focusW > 0
	zoom := ckRegionList
	if ck.zoom && c.w >= ckOneColumn && !strip {
		zoom = ck.region
		if m.InboxReplyOpen() {
			zoom = ckRegionReply
		}
		switch zoom {
		case ckRegionList:
			fleetW, focusW = c.w, 0
		case ckRegionSide:
			if e, ok := m.cockpitAgent(); ok {
				m.drawSide(c, l, e, 2, colTop, c.w-4, colBot)
				ck.regionBox[ckRegionSide] = ckRegionBox{2, c.w - 2, top + colTop, top + colBot}
			}
			m.drawCockpitHints(c, l, hintsY, now)
			return
		default:
			focusX, focusW = 2, c.w-4
		}
	}
	zoomed := ck.zoom && zoom != ckRegionList
	if !strip && !zoomed && (c.w >= ckOneColumn || !st.threads.open) {
		m.drawFleet(c, l, fleetW, colTop, colBot, top)
		ck.regionBox[ckRegionList] = ckRegionBox{0, fleetW, top + colTop, top + colBot}
		if focusW > 0 && c.w >= ckOneColumn {
			m.drawCockpitDivider(c, l, fleetW, colTop, colBot, top)
		}
	}
	if ck.cmp != nil && c.w >= ckOneColumn && !strip {
		m.drawCompare(c, l, focusX, colTop+1, c.w-2-focusX, colBot, top, now)
		m.drawCockpitHints(c, l, hintsY, now)
		return
	}
	if focusW > 0 && !zoomed && m.cockpitSideShown(c.w, focusW) {
		if e, ok := m.cockpitAgent(); ok {
			focusW -= ckSideWidth + 1
			m.drawSide(c, l, e, c.w-ckSideWidth, colTop, ckSideWidth, colBot)
			ck.regionBox[ckRegionSide] = ckRegionBox{c.w - ckSideWidth, c.w, top + colTop, top + colBot}
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
		ck.regionBox[ckRegionFocus] = ckRegionBox{focusX, focusX + focusW, top + colTop, top + colBot}
	}
	m.drawRegionMark(c, l, colTop-1, hdrY, top)
	m.drawCockpitHints(c, l, hintsY, now)
}

func (m *OS) drawRegionMark(c *ckCanvas, l ckLook, y, hdrY, top int) {
	ck := &m.Inbox.ck
	if y <= hdrY || !ck.regionBox[ckRegionFocus].shown() {
		return
	}
	r := m.CockpitRegion()
	if r == ckRegionReply {
		r = ckRegionFocus
	}
	b := ck.regionBox[r]
	if !b.shown() {
		return
	}
	c.text(b.x0, y, b.x1, ckSeg{t: strings.Repeat("▁", b.x1-b.x0), fg: l.pal.Accent})
}

func (m *OS) drawCockpitHeader(c *ckCanvas, l ckLook, y, top int) {
	st := &m.Inbox
	ck := &st.ck
	pal := l.pal
	x := c.text(2, y, 0, ckSeg{t: "Agents", fg: pal.Fg, attrs: ckBold})
	if m.AttachedHost != "" {
		x = c.text(x, y, 0, ckSeg{t: " on " + ckHostLabel(m.AttachedHost), fg: pal.Fg, attrs: ckBold})
	}
	x += 3
	clear(ck.anchors)
	if ck.anchors == nil {
		ck.anchors = map[string]ckAnchor{}
	}
	var counts [ckGroups]int
	if ck.board {
		counts = m.ckAgentCounts()
	} else {
		counts = ck.groupN
	}
	type cnt struct {
		g     string
		ink   color.Color
		n     int
		words string
		group int
	}
	cnts := []cnt{
		{l.ckStateMark("needs_input", ckGlyphNeeds), pal.Attention, counts[ckNeeds], "needs you", ckNeeds},
		{l.ckStateMark("working", ckGlyphWorking), pal.Busy, counts[ckWorking], "working", ckWorking},
		{l.ckStateMark("done", ckGlyphReview), pal.Success, counts[ckReview], "to review", ckReview},
		{l.ckStateMark("idle", ckGlyphIdle), pal.FgMute, counts[ckIdle] + counts[ckSettled], "idle", ckIdle},
	}
	btnW := ckWidth(ckSeg{t: " + New agent "})
	sw := min(38, max(18, (c.w-2-btnW)/3))
	sx := c.w - 2 - btnW - 2 - sw
	fits := func(segs ...ckSeg) bool { return x+ckWidth(segs...) <= sx-3 }
	full := x
	for i, k := range cnts {
		if k.n > 0 || i == 0 {
			full += ckWidth(ckSeg{t: k.g + " " + strconv.Itoa(k.n) + " " + k.words}) + 3
		}
	}
	compact := full > sx-3
	for i, k := range cnts {
		on := !ck.board && ck.chip == k.group+1
		if k.n == 0 && i > 0 && !on {
			continue
		}
		segs := []ckSeg{{t: k.g + " ", fg: k.ink}, {t: strconv.Itoa(k.n), fg: pal.Fg, attrs: ckBold}}
		if !compact || i == 0 {
			segs = append(segs, ckSeg{t: " " + k.words, fg: pal.FgDim})
		}
		x0 := x
		if on {
			w := ckWidth(segs...) + 2
			c.fill(x0-1, y, w, 1, pal.Ground(ckOn, l.panel))
			c.mark(pal, x0-1, y, w, 1, ckOn)
		}
		x = c.text(x, y, 0, segs...) + 3
		action := "chip:" + strconv.Itoa(k.group)
		if ck.board {
			if i > 0 {
				continue
			}
			action = "needs"
		}
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: x - 3, y1: top + y + 1, kind: ckHitAction, action: action})
	}
	if m.cockpitSearched() {
		count := "Filtered"
		if st.agents.total > 0 {
			count = strconv.Itoa(len(ck.agents)) + " of " + strconv.Itoa(st.agents.total) + " agents"
		}
		segs := []ckSeg{
			{t: count, fg: pal.Attention, attrs: ckBold},
			{t: " · esc shows all", fg: pal.FgDim},
		}
		if l.ascii {
			segs[1].t = " - esc shows all"
		}
		q := strings.TrimSpace(st.Select + " " + st.agents.query)
		rest := ckWidth(ckSeg{t: "Board  "}) + 3 + ckWidth(ckSeg{t: " + New agent "}) + 2 + min(38, ckWidth(ckSeg{t: q})+5)
		if !ck.board {
			rest += ckWidth(ckSeg{t: "Group: " + m.ckGroupingName() + "  "}) + 3
		}
		if x+ckWidth(segs...)+3+rest > c.w-2 {
			segs = segs[:1]
		}
		x0 := x
		w := ckWidth(segs...) + 2
		c.fill(x0-1, y, w, 1, l.attnTint)
		c.mark(pal, x0-1, y, w, 1, ckOn)
		x = c.text(x, y, 0, segs...) + 3
		ck.hits = append(ck.hits, ckHit{x0: x0 - 1, y0: top + y, x1: x0 - 1 + w, y1: top + y + 1, kind: ckHitAction, action: "show-all"})
	}
	if ck.needsOnly || st.Filter != "" {
		what := "items only"
		if st.Filter != "" {
			what = strings.ToLower(inboxGroupTitle(st.Filter)) + " only"
		}
		x = c.text(x, y, sx-3, ckSeg{t: what, fg: pal.FgDim}) + 3
	}
	if n := m.markedShown(); n > 0 && fits(ckSeg{t: strconv.Itoa(n) + " marked"}) {
		x = c.text(x, y, 0, ckSeg{t: strconv.Itoa(n) + " marked", fg: pal.AccentBright, attrs: ckBold}) + 3
	}
	view := "Board"
	if ck.board {
		view = "Agents"
	}
	if viewSegs := []ckSeg{{t: view + " ", fg: pal.FgDim}, {t: l.g(ckGlyphExpand), fg: pal.FgMute}}; fits(viewSegs...) {
		bx0 := x
		x = c.text(x, y, 0, viewSegs...)
		ck.hits = append(ck.hits, ckHit{x0: bx0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "board"})
		ck.anchors["board"] = ckAnchor{x: bx0, y: y, w: x - bx0}
		x += 3
	}
	if groupSegs := []ckSeg{{t: "Group: " + m.ckGroupingName() + " ", fg: pal.FgDim}, {t: l.g(ckGlyphExpand), fg: pal.FgMute}}; !ck.board && fits(groupSegs...) {
		gx0 := x
		x = c.text(x, y, 0, groupSegs...)
		ck.hits = append(ck.hits, ckHit{x0: gx0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "group"})
		ck.anchors["group"] = ckAnchor{x: gx0, y: y, w: x - gx0}
		x += 3
	}
	if ck.board && ck.dispatch != nil && ck.dispatch.Board != nil && fits(ckSeg{t: "Dispatch Manual"}) {
		mode, ink := "Manual", pal.FgDim
		switch ck.dispatch.Board.Mode {
		case session.DispatchPull:
			mode, ink = "Pull", pal.AccentBright
		case session.DispatchAuto:
			mode, ink = "Auto", pal.AccentBright
		}
		mx0 := x
		x = c.text(x, y, 0, ckSeg{t: "Dispatch ", fg: pal.FgMute}, ckSeg{t: mode, fg: ink, attrs: ckBold})
		ck.hits = append(ck.hits, ckHit{x0: mx0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "dispatch-mode"})
		if counts := dispatchCounts(ck.dispatch.Board); counts != "" {
			x = c.text(x+2, y, 0, ckSeg{t: counts, fg: pal.FgDim})
		}
		if held := dispatchHeld(ck.dispatch); held != "" {
			x += 2
			hx0 := x
			x = c.text(x, y, 0, ckSeg{t: ckTrunc(printableTitle(held), 28), fg: pal.Warning, attrs: ckBold})
			ck.hits = append(ck.hits, ckHit{x0: hx0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "dispatch-pause"})
		}
		x += 3
	}
	if !st.Live {
		x = c.text(min(x, sx-3-ckWidth(ckSeg{t: "not connected"})), y, sx-3, ckSeg{t: "not connected", fg: pal.Warning}) + 3
	}
	right := c.w - 2
	btn := " + New agent "
	if l.ascii {
		btn = " + New agent "
	}
	bx := c.textR(right, y, ckSeg{t: btn, fg: pal.PillFg, bg: pal.Accent, attrs: ckBold})
	c.mark(pal, bx, y, right-bx, 1, ckOn)
	ck.hits = append(ck.hits, ckHit{x0: bx, y0: top + y, x1: right, y1: top + y + 1, kind: ckHitAction, action: "new"})
	c.fill(sx, y, sw, 1, l.panel)
	switch {
	case st.selectErr != "":
		c.text(sx+1, y, sx+sw, ckSeg{t: ckTrunc(printableTitle(st.selectErr), sw-2), fg: pal.Warning})
	case st.selectEditing:
		c.draw(sx+1, y, sw-1, m.selectLine(sw-1))
	case st.Select != "" || st.agents.query != "":
		q := strings.TrimSpace(st.Select + " " + st.agents.query)
		c.text(sx+1, y, sx+sw, ckSeg{t: "/  ", fg: pal.FgMute}, ckSeg{t: ckTrunc(q, sw-5), fg: pal.Fg})
	default:
		words := "Search agents, sessions, machines"
		switch {
		case sw < 18:
			words = "Search"
		case sw < 36:
			words = "Search agents"
		}
		c.text(sx+1, y, sx+sw, ckSeg{t: "/  ", fg: pal.FgMute}, ckSeg{t: words, fg: pal.FgMute})
	}
	ck.hits = append(ck.hits, ckHit{x0: sx, y0: top + y, x1: sx + sw, y1: top + y + 1, kind: ckHitAction, action: "search"})
	if len(m.FederationHosts) == 0 || len(m.ckMachines()) < 2 {
		return
	}
	label := "All machines"
	switch h := m.ckScope(); h {
	case "":
	case federation.LocalHostName:
		label = m.ckMachine("")
	default:
		label = ckHostLabel(h)
	}
	segs := []ckSeg{{t: label + " ", fg: pal.FgDim}, {t: l.g(ckGlyphExpand), fg: pal.FgMute}}
	if mx := sx - 3 - ckWidth(segs...); mx > x {
		c.text(mx, y, sx-3, segs...)
		ck.hits = append(ck.hits, ckHit{x0: mx, y0: top + y, x1: sx - 3, y1: top + y + 1, kind: ckHitAction, action: "scope"})
		ck.anchors["scope"] = ckAnchor{x: mx, y: y, w: sx - 3 - mx}
	}
}

func (m *OS) drawFleet(c *ckCanvas, l ckLook, w, y0, y1, top int) {
	st := &m.Inbox
	ck := &st.ck
	pal := l.pal
	c.fill(0, y0, w, y1-y0, l.panel)
	ck.fleetW = w
	listTop, listBot := y0+1, y1
	if ms := m.ckMachines(); len(ms) > 1 && y1-y0 >= len(ms)+12 {
		my := y1 - len(ms) - 2
		m.drawMachines(c, l, ms, w, my, top)
		listBot = my - 1
	}
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
		m.drawFleetRow(c, l, r, r.key == sel.key && r.cursorable(), r.key == ck.hover, 0, y, w, top)
	}
	if pal.Depth == overlay.Depth16 && w < c.w {
		for y := y0; y < y1; y++ {
			c.text(w+1, y, 0, ckSeg{t: vtGlyph(), fg: l.edge})
		}
	}
}

type ckMachineRow struct {
	host, name string
	agents     int
	load       float64
	loadOK     bool
	up         bool
	status     string
	rtt        int64
}

func (m *OS) ckMachines() []ckMachineRow {
	ck := &m.Inbox.ck
	var out []ckMachineRow
	var attached ckMachineRow
	for _, h := range m.FederationHosts {
		if h.Name == federation.LocalHostName {
			continue
		}
		mr := ckMachineRow{host: h.Name, name: ckHostLabel(h.Name), agents: ck.machines[h.Name],
			up: h.Status == string(federation.StatusUp), status: h.Status, rtt: h.RTTMillis}
		if h.LoadOK {
			mr.load, mr.loadOK = ckLoadShare(h.Load1, h.Cores)
		}
		if h.Name == m.AttachedHost {
			attached = mr
			continue
		}
		out = append(out, mr)
	}
	if len(out) == 0 {
		return nil
	}
	local := ckMachineRow{name: m.ckMachine(""), agents: ck.machines[""], up: true}
	if m.AttachedHost != "" {
		local.load, local.loadOK, local.rtt = attached.load, attached.loadOK, attached.rtt
	} else {
		if m.LocalLoad.Known() {
			local.load, local.loadOK = ckLoadShare(m.LocalLoad.Load1, m.LocalLoad.Cores)
		}
	}
	return append([]ckMachineRow{local}, out...)
}

func ckLoadShare(load1 float64, cores int) (float64, bool) {
	if cores <= 0 {
		return 0, false
	}
	return min(max(load1/float64(cores), 0), 1), true
}

func (m *OS) drawMachines(c *ckCanvas, l ckLook, ms []ckMachineRow, w, y, top int) {
	ck := &m.Inbox.ck
	pal := l.pal
	right := w - 2
	end := c.text(2, y, right, ckSeg{t: "Machines", fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(len(ms)), fg: pal.FgMute})
	c.hline(end+2, y, right-end-2, hzGlyph(), l.edgePanel)
	scope := m.ckScope()
	for i, mr := range ms {
		yy := y + 1 + i
		nameInk := pal.Fg
		if !mr.up {
			nameInk = pal.FgDim
		}
		if mr.host == scope && scope != "" || mr.host == "" && scope == federation.LocalHostName {
			nameInk = pal.AccentBright
		}
		agents := strconv.Itoa(mr.agents) + " agent"
		if mr.agents != 1 {
			agents += "s"
		}
		c.text(2, yy, right, ckSeg{t: l.g(ckGlyphHost), fg: pal.FgMute}, ckSeg{t: "  " + ckPad(ckTruncMid(mr.name, 9), 10), fg: nameInk},
			ckSeg{t: agents, fg: pal.FgDim})
		link := "local"
		if mr.host != "" {
			link = "link up"
			if mr.rtt > 0 {
				link = strconv.FormatInt(mr.rtt, 10) + " ms"
			}
			if !mr.up {
				link = "link " + strings.ReplaceAll(printableTitle(mr.status), "_", " ")
			}
		}
		lx := c.textR(right, yy, ckSeg{t: link, fg: pal.FgMute})
		if mr.loadOK && lx-2 > 34 {
			load := min(max(mr.load, 0), 1)
			filled := int(load*8 + 0.5)
			barInk := pal.Busy
			if load >= 0.7 {
				barInk = pal.Warning
			}
			on, off := "■", "■"
			if l.ascii {
				on, off = "#", "."
			}
			c.text(26, yy, lx-2, ckSeg{t: strings.Repeat(on, filled), fg: barInk}, ckSeg{t: strings.Repeat(off, 8-filled), fg: l.edgePanel},
				ckSeg{t: " " + ckPadLeft(strconv.Itoa(int(load*100+0.5))+"%", 4), fg: pal.FgMute})
		}
		target := mr.host
		if target == "" {
			target = federation.LocalHostName
		}
		ck.hits = append(ck.hits, ckHit{x0: 0, y0: top + yy, x1: w, y1: top + yy + 1, kind: ckHitAction, action: "scope:" + target})
	}
}

func ckPadLeft(s string, n int) string {
	if d := n - ansi.StringWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func (m *OS) ckScope() string {
	sel := m.Inbox.Select
	if h, ok := strings.CutPrefix(sel, "host:"); ok && !strings.ContainsAny(h, " *?[") {
		return h
	}
	return ""
}

// CockpitScope narrows the fleet to one machine, by host:NAME, or shows
// every machine again for "". Any other selector is replaced; the search
// stays.
func (m *OS) CockpitScope(host string) {
	st := &m.Inbox
	if host == "" {
		m.setInboxSelector(nil)
	} else {
		home, _ := os.UserHomeDir()
		sel, err := session.ParseSelector("host:"+host, home)
		if err != nil {
			return
		}
		m.setInboxSelector(sel)
	}
	st.ck.scroll, st.ck.follow = 0, true
	m.cockpitTouchRows()
}

func (m *OS) ckRowMark(l ckLook, r ckRow, e sidebarAgentEntry) (string, color.Color) {
	if m.ckArtFormRow(r, e) != "" {
		return l.g(ckGlyphNeeds), l.pal.Attention
	}
	if r.hasItem && (r.agent < 0 || r.group == ckNeeds || r.group == ckSnoozed) {
		return l.ckItemMark(r.item)
	}
	return l.ckMark(e.State, e.DoneSeen)
}

func (m *OS) drawFoldMarks(c *ckCanvas, l ckLook, r ckRow, x, y, right int) int {
	end := x - 2
	for i, h := range r.hidden {
		if h.kind == ckRowCard {
			break
		}
		var e sidebarAgentEntry
		if h.agent >= 0 {
			e = m.Inbox.ck.agents[h.agent]
		}
		g, ink := m.ckRowMark(l, h, e)
		left := len(r.hidden) - i
		if x+ckWidth(ckSeg{t: g})+ckWidth(ckSeg{t: " +" + strconv.Itoa(left)})+2 > right {
			end = c.text(x, y, right, ckSeg{t: "+" + strconv.Itoa(left), fg: l.pal.FgMute})
			break
		}
		end = c.text(x, y, right, ckSeg{t: g, fg: ink})
		x = end + 1
	}
	return end
}

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
		if r.folded {
			mark = l.g(ckGlyphFold) + " "
			if cursor {
				c.fill(x, y, w, 1, l.cursor)
				c.text(x, y, 0, ckSeg{t: "▌", fg: pal.Accent})
				defer c.mark(pal, x+1, y, w-1, 1, overlay.RowState{Cursor: true, Focused: true})
			}
		}
		var title string
		if ck.board {
			title = ckStageTitle(r.group)
		} else {
			title = ckGroupTitles[r.group]
		}
		if f := m.Inbox.Filter; !ck.board && f != "" && r.group == ckNeeds {
			title = inboxGroupTitle(f)
		}
		end := c.text(x+2, y, right, ckSeg{t: mark + title, fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(r.count), fg: pal.FgMute})
		if r.folded {
			end = m.drawFoldMarks(c, l, r, end+2, y, right)
		}
		c.hline(end+2, y, right-end-2, hzGlyph(), l.edgePanel)
		ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitHead, group: r.group})
		return
	case ckRowBand:
		end := c.text(x+2, y, right, ckSeg{t: r.title, fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(r.count), fg: pal.FgMute})
		c.hline(end+2, y, right-end-2, hzGlyph(), l.edgePanel)
		return
	case ckRowMore:
		words := "+" + strconv.Itoa(r.count) + " more"
		if r.group >= 0 {
			if ck.board {
				words += " in " + strings.ToLower(ckStageTitle(r.group))
			} else {
				words += " " + strings.ToLower(ckGroupTitles[r.group])
			}
			ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitMore, group: r.group})
		}
		c.text(x+5, y, right, ckSeg{t: words, fg: pal.FgMute})
		return
	case ckRowChild:
		g, ink := l.ckStateMark("working", ckGlyphWorking), overlay.MixColors(pal.Busy, l.panel, 0.3)
		if !r.child.Running() {
			g, ink = l.ckStateMark("done", ckGlyphReview), pal.FgMute
		}
		what := r.child.Now
		if what == "" {
			what = r.child.Description
		}
		c.text(x+4, y, right, ckSeg{t: ckTree(l, r.last), fg: l.edgePanel}, ckSeg{t: g + agentMarkGap(g), fg: ink},
			ckSeg{t: printableTitle(firstNonEmptyStr(r.child.Type, "agent")), fg: pal.FgDim}, ckSeg{t: "  " + printableTitle(what), fg: pal.FgMute})
		return
	case ckRowFan:
		e := ck.agents[r.agent]
		wt := m.ckWorktreeOf(e)
		nr := right
		if note := m.ckFanNote(e.Host, wt); note != "" {
			nr = c.textR(right, y, ckSeg{t: note, fg: pal.FgMute}) - 2
		}
		c.text(x+2, y, nr, ckSeg{t: l.g(ckGlyphFan), fg: pal.AccentBright}, ckSeg{t: "  " + wt.group, fg: pal.Fg, attrs: ckBold},
			ckSeg{t: "  fan of " + strconv.Itoa(r.count), fg: pal.FgDim})
		if r.lines > 1 {
			c.text(x+5, y+1, right, ckSeg{t: ckTrunc(wt.prompt, right-x-5), fg: pal.FgMute})
		}
		return
	case ckRowCard:
		m.drawCardRow(c, l, r, cursor, hover, x, y, w, top)
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
	defer c.mark(pal, x+1, y, w-1, r.lines, overlay.RowState{Cursor: cursor, Focused: cursor, Hover: hover})
	if cursor {
		for i := range r.lines {
			c.text(x, y+i, 0, ckSeg{t: "▌", fg: pal.Accent})
		}
	}
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = ck.agents[r.agent]
	}
	if k, ok := m.ckMarkKey(r); ok {
		if m.Inbox.agents.marks[k] {
			c.text(x+1, y, 0, ckSeg{t: l.g(ckGlyphMarked), fg: pal.AccentBright, attrs: ckBold})
		}
		ck.hits = append(ck.hits, ckHit{x0: x + 1, y0: top + y, x1: x + 2, y1: top + y + 1, kind: ckHitMark, key: r.key})
	}
	ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + r.lines, kind: ckHitRow, key: r.key})
	name, harness, place, wait := m.ckRowFacts(r, e)
	g, gInk := m.ckRowMark(l, r, e)
	nameInk := pal.Fg
	down := e.Host != "" && m.ckLinkDown(e.Host)
	if r.group == ckIdle || r.group == ckSettled || r.group == ckSnoozed || r.item.Stale || down {
		nameInk = pal.FgDim
	}
	if down {
		place += sepWord() + "link down"
		wait = ""
	}
	ink := func(c color.Color) color.Color { return overlay.Readable(c, ground) }
	if r.attempt {
		ax := c.text(x+2, y, right, ckSeg{t: ckTree(l, r.last), fg: l.edgePanel}, ckSeg{t: g + agentMarkGap(g), fg: gInk})
		ax = c.text(ax, y, right, ckSeg{t: ckPad(ckTrunc(name, 11), 12), fg: ink(nameInk)})
		rx := right
		if diff := ckDiffSegs(l, e); diff != nil {
			rx = c.textR(right, y, diff...) - 2
		}
		ax = c.text(ax, y, rx, ckSeg{t: ckPad(harness, 9), fg: ink(pal.FgDim)})
		c.text(ax, y, rx, ckSeg{t: ckTrunc(place, rx-ax), fg: ink(pal.FgMute)})
		return
	}
	nameW := 15
	if w < 50 {
		nameW = 13
	}
	c.text(x+2, y, right, ckSeg{t: g, fg: gInk})
	nx := x + 2 + agentMarkLead()
	if r.lines != 1 || w >= 48 {
		nx = max(nx, x+5)
	}
	var after []ckSeg
	if r.agent >= 0 && m.agentRowUnseen(e) {
		after = append(after, ckSeg{t: l.g(ckGlyphUnseen), fg: ink(pal.AccentBright), attrs: ckBold})
	}
	if ck.ckPinned(r, ck.agents) {
		after = append(after, ckSeg{t: l.g(ckGlyphPin), fg: ink(pal.FgDim)})
	}
	ex := c.text(nx, y, nx+nameW-1, ckSeg{t: ckTrunc(name, nameW-1-2*len(after)), fg: ink(nameInk), attrs: ckBold})
	for _, s := range after {
		ex = c.text(ex+1, y, nx+nameW-1, s)
	}
	rx := c.textR(right, y, ckSeg{t: wait, fg: ink(pal.FgMute)})
	if r.agent >= 0 && m.ckArtifactNew(e, false) {
		cx := c.textR(rx-2, y, ckSeg{t: l.g(ckGlyphNeeds) + " " + ckArtChip, fg: ink(pal.Attention), attrs: ckBold})
		ck.hits = append([]ckHit{{x0: cx, y0: top + y, x1: rx - 2, y1: top + y + 1, kind: ckHitAction, action: "artrow:" + r.key}}, ck.hits...)
		rx = cx
	}
	if r.inline {
		m.drawRowLine2(c, l, r, e, nx+nameW, y, rx-1, ink)
		return
	}
	if r.lines == 1 && w < 48 {
		if ctx := ckContextSegs(l, e, true); ctx != nil && rx-2-ckWidth(ctx...) > nx+nameW {
			rx = c.textR(rx-2, y, ctx...)
		}
		c.text(nx+nameW, y, rx-1, ckSeg{t: ckTrunc(place, rx-1-nx-nameW), fg: ink(pal.FgMute)})
		return
	}
	hx := nx + nameW
	if ctx := ckContextSegs(l, e, true); ctx != nil && rx-2-ckWidth(ctx...) > hx+9 {
		rx = c.textR(rx-2, y, ctx...)
	}
	if diff := ckDiffSegs(l, e); diff != nil && rx-2-ckWidth(diff...) > hx+18 {
		rx = c.textR(rx-2, y, diff...)
	}
	hx = c.text(hx, y, rx-1, ckSeg{t: ckPad(harness, 9), fg: ink(pal.FgDim)})
	if room := rx - 1 - hx; room >= 6 || ansi.StringWidth(place) <= room {
		c.text(hx, y, rx-1, ckSeg{t: ckTrunc(place, room), fg: ink(pal.FgMute)})
	}
	if r.lines < 2 {
		return
	}
	m.drawRowLine2(c, l, r, e, nx, y+1, right, ink)
}

func (m *OS) drawRowLine2(c *ckCanvas, l ckLook, r ckRow, e sidebarAgentEntry, x, y, right int, ink func(color.Color) color.Color) {
	pal := l.pal
	verb, rest, verbInk, rightSegs := m.ckRowLine2(l, r, e)
	rx2 := right
	if rightSegs != nil && right-ckWidth(rightSegs...)-2-x >= ckLine2Need(verb, rest) {
		rx2 = c.textR(right, y, rightSegs...) - 2
	}
	lx := c.text(x, y, rx2, ckSeg{t: verb, fg: ink(verbInk), attrs: ckBold})
	if rest != "" {
		if verb != "" {
			lx += 2
		}
		cut := ckTrunc(rest, rx2-lx)
		if strings.Contains(rest, "/") && !strings.ContainsAny(rest, " \t") {
			cut = ckTruncPath(rest, rx2-lx)
		}
		c.text(lx, y, rx2, ckSeg{t: cut, fg: ink(pal.FgDim)})
	}
}

const ckLine2MinRest = 16

func ckLine2Need(verb, rest string) int {
	n := ansi.StringWidth(verb)
	if rest != "" {
		if verb != "" {
			n += 2
		}
		n += min(ansi.StringWidth(rest), ckLine2MinRest)
	}
	return n
}

func ckRuleWords(rule string) string {
	rule = printableTitle(rule)
	head, _, _ := strings.Cut(rule, " in ")
	if i := strings.IndexByte(head, '('); i > 0 && strings.HasSuffix(head, ")") {
		inner := strings.TrimSuffix(strings.TrimSuffix(head[i+1:len(head)-1], "*"), ":")
		if inner = strings.TrimSpace(inner); inner != "" {
			return inner
		}
		return head[:i]
	}
	return head
}

func ckHomePath(p string) string {
	if home := ckHome(); home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

var ckHome = sync.OnceValue(func() string {
	home, _ := os.UserHomeDir()
	return home
})

func ckTree(l ckLook, last bool) string {
	switch {
	case l.ascii:
		return "| "
	case last:
		return "└ "
	}
	return "├ "
}

var ckOn = overlay.RowState{Cursor: true, Focused: true}

func (m *OS) ckLinkDown(host string) bool {
	for _, h := range m.FederationHosts {
		if h.Name == host {
			return h.Status != "up"
		}
	}
	return false
}

func (m *OS) ckRowFacts(r ckRow, e sidebarAgentEntry) (name, harness, place, wait string) {
	now := time.Now()
	if r.agent >= 0 {
		name = printableTitle(e.Title)
		if name == "" {
			name = printableTitle(shortWindowLabel(e.WindowID))
		}
		harness = ckHarness(e.Harness)
		place = m.ckPlace(e)
		if e.StateAt > 0 {
			wait = units.Waited(e.StateAt, now)
		}
	}
	if r.hasItem {
		it := r.item
		if r.agent < 0 {
			name, harness = inboxWho(it), ckHarness(it.Harness)
			place = m.inboxWhere(it)
		}
		wait = units.Waited(it.Since, now)
		switch {
		case it.Stale:
			wait = inboxSeen(it.SeenAt, now)
		case it.SnoozedUntil != 0:
			wait = inboxSnoozedWhen(it.SnoozedUntil, now)
		}
		if it.Kind == session.AttentionMail && it.Seen > 0 {
			wait = "seen " + wait
		}
	}
	return name, harness, place, wait
}

func ckHarness(h string) string {
	h = strings.TrimSuffix(strings.TrimSuffix(h, "-code"), "-cli")
	return printableTitle(h)
}

func (m *OS) ckPlace(e sidebarAgentEntry) string {
	sess := printableTitle(firstNonEmptyStr(e.SessionLabel, e.SessionID))
	if e.Host == "" || !m.ckManyMachines() {
		return sess
	}
	chip := m.cockpitLook().g(ckGlyphHost) + " " + m.ckMachine(e.Host)
	if sess == "" {
		return chip
	}
	return chip + "  " + sess
}

func (m *OS) ckManyMachines() bool {
	for _, h := range m.FederationHosts {
		if h.Name != federation.LocalHostName {
			return true
		}
	}
	return false
}

func (m *OS) ckMachine(host string) string {
	if host == "" {
		host = m.AttachedHost
	}
	if host != "" {
		return ckHostLabel(host)
	}
	return m.ckLocalLabel()
}

func (m *OS) ckLocalLabel() string {
	if m.LearnMode && m.learnSpawn != nil {
		if n := m.learnSpawn().Machine; n != "" {
			return n
		}
	}
	return ckLocalName()
}

func ckHostLabel(name string) string {
	label := printableTitle(name)
	if strings.TrimSpace(label) == "" {
		label = strings.Trim(strconv.QuoteToASCII(name), `"`)
	}
	return ckTruncMid(label, ckHostLabelMax)
}

const ckHostLabelMax = 24

func ckTruncMid(s string, n int) string {
	if ansi.StringWidth(s) <= n || n < 5 {
		return ckTrunc(s, n)
	}
	tail := (n - 1) / 2
	head := n - 1 - tail
	end := ansi.TruncateLeft(s, ansi.StringWidth(s)-tail, "")
	return ansi.Truncate(s, head, "") + overlay.Ellipsis() + end
}

func ckTruncPath(p string, n int) string {
	if ansi.StringWidth(p) <= n {
		return p
	}
	i := strings.LastIndexByte(p, '/')
	base := p[i+1:]
	tail := overlay.Ellipsis() + "/" + base
	room := n - ansi.StringWidth(tail)
	if i < 0 || room < 0 {
		return ckTrunc(base, n)
	}
	if room < 4 {
		return tail
	}
	return ansi.Truncate(p[:i], room, "") + tail
}

var ckLocalName = sync.OnceValue(func() string {
	name, _ := os.Hostname()
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	if name = printableTitle(name); name == "" {
		name = "this machine"
	}
	return name
})

func ckMeta(e sidebarAgentEntry, key string) string {
	for _, t := range e.Meta {
		if t.Key == key {
			return printableTitle(t.Value)
		}
	}
	return ""
}

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

func ckContextSegs(l ckLook, e sidebarAgentEntry, short bool) []ckSeg {
	tokens, _ := strconv.ParseInt(ckMeta(e, "context_tokens"), 10, 64)
	window, _ := strconv.ParseInt(ckMeta(e, "context_window"), 10, 64)
	pct, havePct := sidebarContextPercent(ckMeta(e, "context"))
	text := ""
	switch {
	case tokens > 0 && window > 0:
		text = "ctx " + ckTokenCount(tokens) + "/" + ckTokenCount(window)
		pct, havePct = float64(tokens)*100/float64(window), true
	case tokens > 0:
		text = "ctx " + ckTokenCount(tokens)
	case havePct:
		text = "ctx " + strconv.Itoa(int(pct+0.5)) + "%"
	default:
		return nil
	}
	if short {
		text = strings.TrimPrefix(text, "ctx ")
	}
	if havePct && pct >= config.SidebarContextWarnAt {
		return []ckSeg{{t: text + " near", fg: l.pal.Warn}}
	}
	return []ckSeg{{t: text, fg: l.pal.FgMute}}
}

func ckTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		m := float64(n) / 1_000_000
		if n%1_000_000 == 0 || m >= 10 {
			return strconv.FormatInt(int64(m), 10) + "M"
		}
		return strconv.FormatFloat(m, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt(n/1000, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

func (m *OS) ckRowLine2(l ckLook, r ckRow, e sidebarAgentEntry) (verb, rest string, ink color.Color, right []ckSeg) {
	pal := l.pal
	ink = pal.Attention
	if r.hasItem && inboxWithParent(r.item) {
		return ckWithParentVerb, printableTitle(r.item.Summary), pal.FgDim, nil
	}
	if r.agent >= 0 {
		if words := childrenWaitingWords(m.childrenWaiting(e)); words != "" {
			defer func() { right = append([]ckSeg{{t: words, fg: pal.Attention}}, right...) }()
		}
		if m.ckSharesFolder(e) {
			defer func() { right = append([]ckSeg{{t: "shared folder", fg: pal.Warn}}, right...) }()
		}
	}
	if r.hasItem && r.group != ckReview {
		verb, rest = ckItemVerb(r.item)
		if r.item.Kind == session.AttentionErrored {
			ink = pal.Warn
		}
		if r.item.Kind == session.AttentionMail || r.item.Kind == session.AttentionResume {
			ink = pal.AccentBright
		}
		if r.item.Kind == session.AttentionResume && len(r.item.RiskyFlags) > 0 {
			ink = pal.Warn
		}
		if r.item.Kind == session.AttentionResume && m.Inbox.agents.resuming[r.item.ID] {
			verb, ink = "Resuming", pal.FgDim
		}
		return verb, rest, ink, nil
	}
	if title := m.ckArtFormRow(r, e); title != "" {
		return "Form waiting", printableTitle(title), pal.Attention, nil
	}
	ink = pal.FgDim
	switch {
	case e.Reason == session.AgentReasonSilent:
		return "", silentNote(e.StateAt, time.Now()), pal.Warn, right
	case e.State == "working":
		if now := ckMeta(e, "now"); now != "" {
			if tool, target, ok := strings.Cut(toolLineLabel(now), ": "); ok {
				return tool, ckRelTarget(m.ckAgentRoot(e), tool, target), ink, right
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
		rest := printableTitle(e.Message)
		if e.Quiet {
			rest = strings.TrimSuffix("quiet, not in the Inbox. "+rest, " ")
		}
		return capitalize(agentRowStateWords(e)), rest, pal.Attention, right
	}
	if e.State == "limit_reached" {
		return "Limit reached", printableTitle(e.Message), pal.Warn, right
	}
	if e.DraftHeld {
		return ckDraftVerb, session.DraftHeldSummary, pal.Attention, right
	}
	wt := m.ckWorktreeOf(e)
	br := m.ckBranch(e)
	if pr := ckPRSegs(l, e.PR, wt.failed); pr != nil {
		right = pr
	}
	if br != "" {
		return "", l.g(ckGlyphBranch) + " " + br, ink, right
	}
	if r.hasItem {
		return "", printableTitle(r.item.Summary), ink, right
	}
	return "", printableTitle(e.Message), ink, right
}

func ckPRSegs(l ckLook, badge string, failed int) []ckSeg {
	f := strings.Fields(badge)
	if len(f) < 3 || f[0] != "PR" {
		return nil
	}
	pal := l.pal
	segs := []ckSeg{{t: l.g(ckGlyphPR) + " " + printableTitle(f[1]), fg: pal.FgDim}}
	if f[2] != "open" {
		return append(segs, ckSeg{t: " " + printableTitle(f[2]), fg: pal.FgMute})
	}
	if len(f) < 4 {
		return segs
	}
	switch f[3] {
	case "pass":
		segs = append(segs, ckSeg{t: " " + l.g(ckGlyphReview), fg: pal.Success})
	case "fail":
		words := " checks fail"
		switch {
		case failed == 1:
			words = " 1 check"
		case failed > 1:
			words = " " + strconv.Itoa(failed) + " checks"
		}
		segs = append(segs, ckSeg{t: " " + l.g(ckGlyphFailed) + words, fg: pal.Warn})
	case "pending":
		segs = append(segs, ckSeg{t: " " + l.g(ckGlyphWorking), fg: pal.Busy})
	}
	return segs
}

func ckItemVerb(it session.AttentionItem) (verb, rest string) {
	sum := printableTitle(it.Summary)
	switch it.Kind {
	case session.AttentionApproval:
		if tool, text := risk.ParseSummary(it.Summary); tool != "" {
			return "Allow " + printableTitle(tool), printableTitle(ckToolTarget(tool, text))
		}
		return "Allow", sum
	case session.AttentionPlan:
		return "Plan", inboxPlanRowText(it)
	case session.AttentionQuestion, session.AttentionAsk:
		if it.Summary == session.DraftHeldSummary {
			return ckDraftVerb, sum
		}
		return "Asks", sum
	case session.AttentionMail:
		if it.HeldID != 0 {
			return "Held mail", sum
		}
		return "Mail", sum
	case session.AttentionErrored:
		if inboxDaemonNotice(it) {
			return "Notice", sum
		}
		return "Error", sum
	case session.AttentionResume:
		if warn := session.ResumeWarning(it.RiskyFlags); warn != "" {
			return printableTitle(warn), sum
		}
		return "Resume", sum
	case session.AttentionOutbox:
		return "To send", sum
	case session.AttentionHeldMail:
		return "Held there", sum
	}
	return capitalize(inboxKindWords(it)), sum
}

const ckDraftVerb = "Waits:"

type ckWorktree struct {
	branch, group, path, prompt, repo string
	fanID                             string
	failed                            int
	pick, judgeWindow                 string
	verdictAt                         int64
	name                              string
	main                              bool
}

func ckFanKey(host string, w ckWorktree) string {
	if w.group == "" {
		return ""
	}
	return host + "\x00" + w.repo + "\x00" + w.group + "\x00" + w.fanID
}

func (m *OS) ckJudgeWindows() map[string]bool {
	var out map[string]bool
	for k, w := range m.Inbox.ck.branches {
		if w.judgeWindow == "" {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		h, _, _ := strings.Cut(k, "\x00")
		out[h+"\x00"+w.judgeWindow] = true
	}
	return out
}

func (m *OS) ckFanNote(host string, wt ckWorktree) string {
	if wt.pick == "" {
		if wt.judgeWindow != "" {
			return "judge is reading"
		}
		return ""
	}
	key := ckFanKey(host, wt)
	var names []string
	for k, w := range m.Inbox.ck.branches {
		if h, _, _ := strings.Cut(k, "\x00"); ckFanKey(h, w) == key {
			names = append(names, w.name)
		}
	}
	slices.SortFunc(names, ckFanOrder)
	if i := slices.Index(names, wt.pick); i >= 0 {
		return "judge says " + ckLetter(i)
	}
	return ""
}

func (m *OS) ckBranch(e sidebarAgentEntry) string {
	if wt := m.ckWorktreeOf(e); !wt.main {
		return wt.branch
	}
	return ""
}

//go:noinline
func (m *OS) ckWorktreeOf(e sidebarAgentEntry) ckWorktree {
	if m.LearnMode && e.Host == "" {
		if w, ok := m.Inbox.ck.branches["\x00"+e.SessionID+"\x00"+e.WindowID]; ok {
			return w
		}
	}
	return m.Inbox.ck.branches[e.Host+"\x00"+e.SessionID]
}

func (m *OS) ckAgentSession(e sidebarAgentEntry) string {
	if m.LearnMode {
		if n := m.ckWorktreeOf(e).name; n != "" {
			return n
		}
	}
	return e.SessionID
}

func (m *OS) ckBranches() map[string]ckWorktree {
	out := map[string]ckWorktree{}
	for _, s := range m.BuildSessionTree().Sessions {
		if w := s.Worktree; w != nil && w.Branch != "" {
			out[s.Host+"\x00"+s.ID] = ckWorktree{branch: printableTitle(w.Branch), group: w.Group, fanID: w.FanID,
				path: printableTitle(w.Path), prompt: printableTitle(w.Prompt), failed: w.PRFailed, repo: w.RepoRoot,
				pick: w.Pick, judgeWindow: w.JudgeWindow, verdictAt: w.VerdictAt, name: s.ID, main: w.Main}
		}
	}
	if m.LearnMode {
		for win, w := range m.learnFans {
			if m.windowByID(win) != nil {
				out["\x00"+m.sidebarCurrentSessionID()+"\x00"+win] = w
			}
		}
	}
	return out
}

func (m *OS) ckHasLine2(r ckRow, e sidebarAgentEntry) bool {
	switch {
	case r.agent >= 0 && m.childrenWaiting(e) > 0:
		return true
	case r.hasItem && r.group != ckReview:
		return true
	case r.agent < 0:
		return r.hasItem && r.item.Summary != ""
	case m.ckArtFormRow(r, e) != "":
		return true
	case e.State == "working":
		return ckMeta(e, "now") != "" || e.Message != "" || e.Subagents > 0
	case sidebarAttention(e.State), e.DraftHeld:
		return true
	}
	return m.ckBranch(e) != "" || e.PR != "" || e.Message != "" || (r.hasItem && r.item.Summary != "")
}

func (m *OS) drawCockpitStrip(c *ckCanvas, l ckLook, y, top int) int {
	ck := &m.Inbox.ck
	pal := l.pal
	sel, _ := m.cockpitSelected()
	x := 2
	for _, r := range ck.rows {
		if !r.selectable() {
			continue
		}
		chip, art := m.ckStripChip(l, r, r.key == sel.key)
		x0 := x
		x = c.text(x, y, 0, chip...)
		c.mark(pal, x0, y, x-x0, 1, overlay.RowState{Cursor: r.key == sel.key, Focused: true})
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitRow, key: r.key})
		if art != nil {
			cx := x + 1
			x = c.text(cx, y, 0, art...)
			ck.hits = append([]ckHit{{x0: cx, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "artrow:" + r.key}}, ck.hits...)
		}
		x += 2
	}
	return y + 1
}

func (m *OS) ckStripChip(l ckLook, r ckRow, cursor bool) (chip, art []ckSeg) {
	ck := &m.Inbox.ck
	pal := l.pal
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = ck.agents[r.agent]
	}
	name, _, _, _ := m.ckRowFacts(r, e)
	g, gInk := l.ckMark(e.State, e.DoneSeen)
	if r.hasItem {
		g, gInk = l.ckItemMark(r.item)
	}
	if m.ckArtFormRow(r, e) != "" {
		g, gInk = l.g(ckGlyphNeeds), pal.Attention
	}
	bg, nameInk := l.panel, pal.FgDim
	if cursor {
		bg, nameInk = l.cursor, pal.Fg
	}
	chip = []ckSeg{{t: " " + g + " ", fg: gInk, bg: bg}, {t: name + " ", fg: overlay.Readable(nameInk, bg), bg: bg, attrs: ckBold}}
	if r.agent >= 0 && m.ckArtifactNew(e, false) {
		art = []ckSeg{{t: l.g(ckGlyphNeeds) + " " + ckArtChip, fg: pal.Attention, attrs: ckBold}}
	}
	return chip, art
}

func (m *OS) ckStripWidth(l ckLook) int {
	w := 0
	for _, r := range m.Inbox.ck.rows {
		if !r.selectable() {
			continue
		}
		chip, art := m.ckStripChip(l, r, false)
		w += ckWidth(chip...) + 2
		if art != nil {
			w += 1 + ckWidth(art...)
		}
	}
	return max(w-2, 0)
}

func (m *OS) cockpitEmptyLines(rows []ckRow) []string {
	st := &m.Inbox
	for _, r := range rows {
		if r.selectable() || r.kind == ckRowHead && r.folded {
			return nil
		}
	}
	if st.ck.board && !st.Unsupported && m.IsDaemonSession {
		return m.ckBoardEmptyLines()
	}
	switch {
	case st.Unsupported:
		return []string{"This daemon is too old for the agents view.", m.restartHint()}
	case !m.IsDaemonSession && !m.LearnMode:
		return []string{"The agents view needs the daemon.", "Start a daemon session with: tuios new"}
	case st.Filter == session.AttentionMail:
		return []string{"No unread mail for you.", "m opens the mailbox, with every thread between agents too."}
	case st.Select != "" || st.agents.query != "":
		return []string{"No agent matches the search.", "/ changes the search. Clear the line and press enter to show everything."}
	case st.ck.chip > 0:
		return []string{"No agent is in this state now.", "Click the count again, or press esc, to show every agent."}
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

func (m *OS) drawCockpitHints(c *ckCanvas, l ckLook, y int, now time.Time) {
	pal := l.pal
	hints := m.cockpitLayoutHints()
	if m.CockpitTop() == CockpitBase {
		if note := m.cockpitLayoutNote(); note.Label != "" {
			hints = append(hints, note)
		}
	}
	hints = append(hints, m.cockpitHints(now)...)
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
	if m.Inbox.ck.cmp != nil {
		right = "Keeping one removes the other worktrees. Their branches stay."
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
	fit := func(hs []overlay.Hint, room int) []overlay.Hint {
		hs = slices.Clone(hs)
		for len(hs) > 1 && width(hs) > room {
			hs = slices.Delete(hs, len(hs)-2, len(hs)-1)
		}
		return hs
	}
	required := func(hs []overlay.Hint) int {
		n := 0
		for _, h := range hs {
			if h.Priority != overlay.HintOptional {
				n++
			}
		}
		return n
	}
	name := ""
	if m.Inbox.ck.regionBox[ckRegionFocus].shown() {
		name = ckRegionNames[m.CockpitRegion()]
		if nw := ansi.StringWidth(name) + 5; required(fit(hints, room-nw)) < required(fit(hints, room)) {
			name = ""
		}
	}
	if name != "" {
		room -= ansi.StringWidth(name) + 5
	}
	hints = fit(hints, room)
	x := 2
	if name != "" {
		x = c.text(x, y, 2+room+ansi.StringWidth(name)+5, ckSeg{t: name, fg: pal.Fg, attrs: ckBold}, ckSeg{t: " ›", fg: pal.FgDim}) + 3
		room += ansi.StringWidth(name) + 5
	}
	for _, h := range hints {
		x = c.text(x, y, 2+room, ckSeg{t: h.Key, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + h.Label, fg: pal.FgMute}) + 3
	}
	if right != "" {
		c.textR(c.w-2, y, ckSeg{t: right, fg: pal.FgMute})
	}
}

func (m *OS) cockpitHints(now time.Time) []overlay.Hint {
	st := &m.Inbox
	switch m.CockpitTop() {
	case CockpitLayerArtifact:
		return []overlay.Hint{{Key: "esc", Label: "back to the agents", Priority: overlay.HintEssential}}
	case CockpitLayerCompare:
		n := "1"
		if res := st.ck.cmp.res; res != nil && len(res.Rows) > 1 {
			n = "1-" + strconv.Itoa(min(len(res.Rows), 9))
		}
		hints := []overlay.Hint{{Key: "K", Label: "keep"}, {Key: "J", Label: "judge"}, {Key: n, Label: "focus"},
			{Key: overlay.EnterKey(), Label: "open pane"}, {Key: "r", Label: "read again"}, {Key: "esc", Label: "back"}}
		if st.ck.cmp.armed != "" {
			hints = append([]overlay.Hint{{Key: "up down", Label: "scroll the list"}}, hints...)
		}
		return hints
	case CockpitLayerConfirm:
		return inboxConfirmHints(st.approvals.answer.confirm.decision)
	case CockpitLayerPeek:
		return nil
	case CockpitLayerReason:
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	case CockpitLayerSelect:
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: "apply"}, {Key: "esc", Label: "cancel"}}
	case CockpitLayerMenu:
		return m.cockpitMenuHints()
	case CockpitLayerShip:
		return m.cockpitShipHints()
	case CockpitLayerSnooze:
		return inboxSnoozeHints()
	case CockpitLayerBulkSheet:
		return m.bulkSheetHints()
	}
	if len(st.agents.marks) > 0 {
		return m.bulkHints()
	}
	if hints, ok := m.inboxReplyHints(); ok {
		return hints
	}
	switch m.CockpitRegion() {
	case ckRegionFocus:
		return m.cockpitFocusHints()
	case ckRegionSide:
		return m.cockpitSideHints()
	}
	hints := m.resumeAllHint()
	if m.kanbanOn() && m.kanbanDrawable() {
		return append(hints, m.kanbanHints()...)
	}
	r, ok := m.cockpitSelected()
	if ok && r.kind == ckRowHead {
		hints = append(hints, m.keyHints(config.ActionInboxFold, "open group")...)
	} else if ok && r.kind == ckRowCard {
		hints = m.ckCardHints(st.ck.cards[r.card])
	} else if ok {
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
				hints = append(hints, m.keyHints(config.ActionInboxReject, words[2])...)
			}
			if it.DenyMessage {
				hints = append(hints, m.keyHints(config.ActionInboxDenyReason, words[3])...)
			}
		}
		if r.hasItem {
			hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss")...)
		}
		if r.hasItem && r.item.Kind == session.AttentionAsk && r.item.RequestID != "" {
			hints = append(hints, overlay.Hint{Key: inboxAskKeys(r.item), Label: "answer"})
		}
		if r.hasItem {
			if opts := m.inboxQuestionOptions(r.item); len(opts) > 0 {
				hints = append(hints, overlay.Hint{Key: "1-" + strconv.Itoa(min(len(opts), 9)), Label: "choose"})
			}
		}
		goLabel := "open pane"
		if r.hasItem && r.item.Kind == session.AttentionMail {
			goLabel = "open"
		}
		if t := m.cockpitThread(); t != nil && t.pill != "" {
			goLabel = "open agent"
		}
		hints = append(hints, m.keyHints(config.ActionInboxGo, goLabel)...)
		if label := m.settleLabel(r); label != "" {
			hints = append(hints, m.keyHints(config.ActionInboxSettle, label)...)
		}
		if r.hasItem && inboxPeekable(r.item) && r.item.RequestID == "" {
			hints = append(hints, m.keyHints(config.ActionInboxPeek, "peek")...)
		}
		blocked := r.agent >= 0 && m.Inbox.ck.agents[r.agent].State == "needs_input"
		if _, held := m.cockpitHeldItem(); !held && (!blocked || r.item.Kind == session.AttentionMail) {
			hints = append(hints, m.keyHints(config.ActionInboxReply, "reply")...)
		}
		if r.hasItem {
			switch {
			case r.item.HeldID != 0:
				hints = append(hints, m.keyHints(config.ActionInboxPassOn, "pass on")...)
			case r.item.Kind == session.AttentionResume && !m.Inbox.agents.resuming[r.item.ID]:
				hints = append(hints, m.keyHints(config.ActionInboxResume, "resume")...)
			}
			switch {
			case r.item.SnoozedUntil != 0:
				hints = append(hints, m.keyHints(config.ActionInboxSnooze, "wake")...)
			case inboxSnoozeRefusal(r.item) == "":
				hints = append(hints, m.keyHints(config.ActionInboxSnooze, "snooze")...)
			}
		}
		if r.hasItem && r.item.Kind == session.AttentionMail {
			hints = append(hints, m.keyHints(config.ActionInboxMailbox, "mailbox")...)
		}
		if m.ckFanSession() != "" {
			hints = append(hints, m.keyHints(config.ActionInboxCompare, "compare")...)
		}
		hints = append(hints, m.ckChildHints()...)
		hints = append(hints, m.ckJumpHints()...)
	}
	board := "board"
	if st.ck.board {
		board = "agents"
	}
	hints = append(hints, m.keyHints(config.ActionInboxNewAgent, "new agent", config.ActionInboxBoard, board)...)
	if st.ck.board && m.GetRenderWidth() >= ckOneColumn {
		hints = append(hints, m.keyHints(config.ActionInboxBoardLayout, "columns")...)
	}
	if !st.ck.board && m.GetRenderWidth() >= ckOneColumn {
		list := "strip"
		if st.ck.stripShown {
			list = "list"
		}
		hints = append(hints, overlay.Optional(m.keyHints(config.ActionInboxViewList, list))...)
	}
	hints = append(hints, m.keyHints(config.ActionInboxReview, "review", config.ActionInboxFilter, "filter", config.ActionInboxSelect, "search")...)
	if ok && r.kind != ckRowHead {
		hints = append(hints, overlay.Optional(m.keyHints(config.ActionInboxFold, "fold group"))...)
	}
	if ok && r.kind == ckRowAgent && !st.ck.board {
		pin := "pin"
		if st.ck.ckPinned(r, st.ck.agents) {
			pin = "unpin"
		}
		hints = append(hints, overlay.Optional(m.keyHints(config.ActionInboxPin, pin))...)
	}
	if m.cockpitNeedsCount() > 1 {
		hints = append(hints, overlay.Optional(m.keyHints(config.ActionInboxNextNeeds, "next needs you"))...)
	}
	if m.Inbox.ck.regionBox[ckRegionFocus].shown() {
		hints = append(hints, overlay.Optional(m.keyHints(config.ActionInboxRegionNext, "next region"))...)
	}
	hints = append(hints, overlay.Hint{Key: "?", Label: "all keys"})
	return append(hints, m.cockpitEscHints()...)
}

func (m *OS) cockpitNote(r ckRow, e sidebarAgentEntry) string {
	sessionName, windowID, host := e.SessionID, e.WindowID, e.Host
	if r.agent < 0 {
		if !r.hasItem {
			return ""
		}
		sessionName, windowID, host = r.item.Session, r.item.Window, r.item.Host
	}
	if sessionName == "" || host != "" {
		return ""
	}
	return m.paneNote(sessionName, windowID)
}

func (m *OS) drawFoldFocus(c *ckCanvas, l ckLook, r ckRow, x, y, w, y1 int) {
	ck := &m.Inbox.ck
	pal := l.pal
	right := x + w
	var title string
	if ck.board {
		title = ckStageTitle(r.group)
	} else {
		title = ckGroupTitles[r.group]
	}
	c.text(x, y, right, ckSeg{t: title, fg: pal.Fg, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(r.count), fg: pal.FgMute})
	words := "The group is folded."
	if key := m.inboxKey(config.ActionInboxFold); key != "" {
		words += " Press " + key + " to open it."
	}
	c.text(x, y+1, right, ckSeg{t: words, fg: pal.FgDim})
	line := y + 3
	for i, h := range r.hidden {
		if line >= y1 {
			break
		}
		if line == y1-1 && i < len(r.hidden)-1 {
			c.text(x, line, right, ckSeg{t: "+" + strconv.Itoa(len(r.hidden)-i) + " more", fg: pal.FgMute})
			break
		}
		var e sidebarAgentEntry
		if h.agent >= 0 {
			e = ck.agents[h.agent]
		}
		var segs []ckSeg
		place := ""
		if h.kind == ckRowCard {
			segs = []ckSeg{{t: printableTitle(ck.cards[h.card].Title), fg: pal.Fg}}
		} else {
			var name string
			name, _, place, _ = m.ckRowFacts(h, e)
			g, ink := m.ckRowMark(l, h, e)
			segs = []ckSeg{{t: g + " ", fg: ink}, {t: name, fg: pal.Fg}}
		}
		if place != "" {
			segs = append(segs, ckSeg{t: "  " + place, fg: pal.FgDim})
		}
		c.text(x, line, right, segs...)
		line++
	}
}

func ckSameAsBranch(sessionName, branch string) bool {
	if sessionName == "" || branch == "" {
		return false
	}
	dashed := strings.ReplaceAll(branch, "/", "-")
	return sessionName == branch || sessionName == dashed || strings.HasSuffix(sessionName, "-"+dashed) ||
		sessionName == branch[strings.LastIndex(branch, "/")+1:]
}

func (m *OS) drawFocus(c *ckCanvas, l ckLook, x, y, w, y1, top int, now time.Time) {
	st := &m.Inbox
	pal := l.pal
	r, ok := m.cockpitSelected()
	if !ok {
		return
	}
	if r.kind == ckRowHead {
		m.drawFoldFocus(c, l, r, x, y, w, y1)
		return
	}
	if r.kind == ckRowCard && (r.agent < 0 || m.cockpitTab() == "Card") {
		m.drawCardFocus(c, l, r, st.ck.cards[r.card], x, y, w, y1, top, now)
		return
	}
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = st.ck.agents[r.agent]
	}
	right := x + w
	name, harness, place, _ := m.ckRowFacts(r, e)
	if r.agent >= 0 && e.Quiet {
		harness = strings.TrimPrefix(harness+sepWord()+"quiet", sepWord())
	}
	hx := c.text(x, y, right, ckSeg{t: name, fg: pal.Fg, attrs: ckBold})
	if harness != "" {
		hx = c.text(hx, y, right, ckSeg{t: "   " + harness, fg: pal.FgDim})
	}
	if model := ckMeta(e, "model"); model != "" {
		hx = c.text(hx, y, right, ckSeg{t: " · " + model, fg: pal.FgMute})
	}
	pill := m.drawStatePill(c, l, r, e, right, y)
	m.drawSettleButton(c, l, r, hx, pill, y, top)
	var groups [][]ckSeg
	wt := m.ckWorktreeOf(e)
	if wt.branch != "" {
		groups = append(groups, []ckSeg{{t: l.g(ckGlyphBranch) + " " + wt.branch, fg: pal.FgDim}})
	}
	if (r.agent >= 0 || place != "") && !ckSameAsBranch(place, wt.branch) {
		groups = append(groups, []ckSeg{{t: place, fg: pal.FgDim}})
	}
	pathAt := -1
	if wt.path != "" {
		pathAt = len(groups)
		groups = append(groups, []ckSeg{{t: ckHomePath(wt.path), fg: pal.FgMute}})
	}
	if d := ckDiffSegs(l, e); d != nil {
		groups = append(groups, d)
	}
	if ctx := ckContextSegs(l, e, false); ctx != nil {
		groups = append(groups, ctx)
	}
	if v := ckMeta(e, "cost"); v != "" {
		groups = append(groups, []ckSeg{{t: v, fg: pal.FgMute}})
	}
	const gap = 3
	width := func() int {
		n := 0
		for i, g := range groups {
			if i > 0 {
				n += gap
			}
			n += ckWidth(g...)
		}
		return n
	}
	if over := width() - w; over > 0 && pathAt >= 0 {
		p := groups[pathAt][0].t
		keep := max(ansi.StringWidth(p)-over-1, 8)
		groups[pathAt][0].t = overlay.Ellipsis() + ansi.TruncateLeft(p, ansi.StringWidth(p)-keep, "")
	}
	for len(groups) > 1 && width() > w {
		groups = groups[:len(groups)-1]
	}
	mx := x
	for i, g := range groups {
		if i > 0 {
			mx += gap
		}
		mx = c.text(mx, y+1, right, g...)
	}
	rule := y + 3
	if c.h < 30 {
		rule = y + 2
	}
	line := y + 2
	if note := m.cockpitNote(r, e); note != "" {
		c.text(x, y+2, right, ckSeg{t: overlay.Truncate(printableTitle(note), max(w, 1)), fg: pal.FgDim})
		rule = max(rule, y+3)
		if c.h >= 30 {
			rule = y + 4
		}
		line = y + 3
	}
	if r.agent >= 0 && m.drawTalkedWith(c, l, e, x, line, w, top) {
		rule = max(rule, line+1)
		if c.h >= 30 {
			rule = max(rule, line+2)
		}
	}
	rule = m.drawFocusTabs(c, l, m.cockpitTabs(e, r), x, w, rule, top)
	bodyTop := rule + 2
	if c.h < 30 {
		bodyTop = rule + 1
	}

	foot := y1
	fold := m.ckTabFolds()
	if st.reply.editor != nil || (r.agent >= 0 && e.State != "needs_input") || (r.hasItem && r.item.Kind == session.AttentionMail) {
		foot = m.drawReplyArea(c, l, e, name, x, w, bodyTop, y1, top, fold)
	}
	bodyBot := foot - 1
	if foot == y1 {
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
	cardTop := bodyBot
	if r.hasItem {
		cardTop = m.drawItemCard(c, l, r, x, w, bodyTop, bodyBot, top, now, fold)
	}
	switch m.cockpitTab() {
	case ckArtTab:
		if cardTop-1-bodyTop >= 2 {
			m.drawArtifacts(c, l, e, x, bodyTop, w, cardTop-1, top, now)
		}
		return
	case "Checkpoints":
		if room := cardTop - 1 - bodyTop; room >= 2 {
			m.drawCheckpoints(c, l, e, x, bodyTop, w, max((room-1)/2, 1))
		}
		return
	case "Pane":
		if room := cardTop - 1 - bodyTop; room >= 3 {
			m.drawLiveBox(c, l, e, x, bodyTop, w, room)
		}
		return
	}
	if r.agent >= 0 && cardTop-bodyTop > 1 {
		if pin := m.ThreadPinnedLines(e, w, cardTop-bodyTop); len(pin) > 0 {
			for i, s := range pin {
				c.draw(x, bodyTop+i, w, s)
			}
			bodyTop += len(pin)
		}
		room := cardTop - bodyTop - 1
		t := st.threads.byKey[agentKey(e)]
		up := 0
		if t != nil {
			up = max(t.follow.Up, 0)
		}
		base := cardTop - 1
		act, acting := m.threadActivityLine(l, e, t, w, now)
		if m.drawThreadCopy(c, t, x, w, base-room, room, top) {
			return
		}
		lines := m.threadBody(e, w, room+up, now, r.hasItem && m.inboxRecapFor(r.item))
		var hits []string
		if t != nil && len(t.hits) == len(lines) {
			hits = t.hits
		}
		if t != nil {
			t.actRow = acting
		}
		if acting {
			lines = append(slices.Clip(lines), act)
			if hits != nil {
				hits = append(slices.Clip(hits), "")
			}
		}
		start, end := max(len(lines)-room, 0), len(lines)
		if t != nil && (t.read || t.recap != nil) {
			t.follow.Apply(threadLayout(t, len(lines), room))
			if t.fromTop && m.cockpitTab() == "Changes" {
				t.fromTop = false
				t.follow.Scroll(len(lines))
			}
			start, end = t.follow.Window()
		}
		if t != nil && hits != nil {
			st.ck.code = ckCodeView{t: t, x0: x, x1: right, top: top + base - end, start: start, end: min(end, len(lines))}
		}
		for i := start; i < min(end, len(lines)); i++ {
			y := base - (end - i)
			c.draw(x, y, w, lines[i])
			if hits != nil && hits[i] != "" {
				st.ck.hits = ckLineHits(st.ck.hits, hits[i], x, right, x+w, top+y)
			}
		}
		if t != nil {
			m.drawThreadUnseen(c, l, t, x, w, base-1, top)
			drawThreadRuler(c, l, t, x+w-1, base-room, room, len(lines), start)
		}
	}
}

func (m *OS) drawFocusTabs(c *ckCanvas, l ckLook, tabs []string, x, w, rule, top int) int {
	pal := l.pal
	right := x + w
	var under func()
	if len(tabs) > 0 {
		rule++
		ty, tx := rule-1, x
		for i, tab := range tabs {
			x0 := tx
			if i == m.cockpitTabIndex() {
				tx = c.text(tx, ty, right, ckSeg{t: tab, fg: pal.Fg, attrs: ckBold})
			} else {
				name, count, _ := strings.Cut(tab, " ")
				segs := []ckSeg{{t: name, fg: pal.FgMute}}
				if count != "" {
					segs = append(segs, ckSeg{t: " " + count, fg: overlay.Shown(overlay.MixColors(pal.FgMute, l.canvas, 0.25))})
				}
				tx = c.text(tx, ty, right, segs...)
			}
			m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: x0, y0: top + ty, x1: tx, y1: top + ty + 1, kind: ckHitTab, group: i})
			if i == m.cockpitTabIndex() {
				x1 := tx
				under = func() { c.hline(x0, rule, x1-x0, "━", pal.Accent) }
			}
			tx += 4
		}
	}
	c.hline(x, rule, w, hzGlyph(), l.edge)
	if under != nil {
		under()
	}
	return rule
}

func (m *OS) cockpitTabs(e sidebarAgentEntry, r ckRow) []string {
	if r.kind == ckRowCard {
		tabs := []string{"Card"}
		if r.agent >= 0 {
			r.kind = ckRowAgent
			tabs = append(tabs, m.cockpitTabs(e, r)...)
		}
		return tabs
	}
	if r.agent < 0 || e.Host != "" {
		return nil
	}
	checkpoints := "Checkpoints"
	if cp := m.Inbox.ck.cps[agentKey(e)]; cp != nil && len(cp.list) > 0 {
		checkpoints += " " + strconv.Itoa(len(cp.list))
	}
	side := []string{checkpoints, "Pane"}
	if arts := m.ckArtTabLabel(e); arts != "" {
		side = append([]string{arts}, side...)
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	if !m.threadReadsTranscript(e) || t == nil || !t.read {
		return append([]string{"Thread"}, side...)
	}
	n := 0
	root := m.ckAgentRoot(e)
	for _, en := range slices.Backward(t.entries) {
		if en.Kind == "text" && en.Role == "user" {
			break
		}
		if en.Kind == "tool_call" && en.Diff != nil && ckInRoot(root, en.Diff.File) {
			n++
		}
	}
	changes := "Changes"
	if n > 0 {
		changes += " " + strconv.Itoa(n)
	}
	return append([]string{"Thread", changes}, side...)
}

func (m *OS) ckApprovalLines(it session.AttentionItem, where string, width int, now time.Time) (inset, detail []string) {
	tool, text := risk.ParseSummary(it.Summary)
	if tool == "" {
		text = it.Summary
	} else if tool != "Bash" {
		text = tool + "  " + text
	}
	inset = wrapPlain(printableTitle(text), max(width-6, 8))
	detail = append(detail, "  in "+where)
	if slices.Contains(it.Options, session.ApprovalAlways) && len(it.AlwaysScope) > 0 {
		for _, rule := range it.AlwaysScope {
			for _, l := range wrapPlain("Always also allows "+printableTitle(rule), max(width-2, 8)) {
				detail = append(detail, "  "+l)
			}
		}
	}
	detail = append(detail, inboxRiskLines(it, width)...)
	detail = append(detail, m.inboxRefusalLines(it, width)...)
	if line, ok := m.inboxConfirmLine(it); ok {
		for _, l := range wrapPlain(line, width) {
			detail = append(detail, "  "+l)
		}
	}
	if key, armed := m.inboxArmedFor(it, now); armed {
		press := m.Inbox.approvals.answer.armed.press
		if press == "" {
			press = inboxDecisionKey(key)
		}
		for _, l := range wrapPlain("Press "+press+" again "+m.inboxArmedUntil()+" to allow "+printableTitle(inboxRiskTarget(it)), width) {
			detail = append(detail, "  "+l)
		}
	}
	return inset, detail
}

func (m *OS) drawStatePill(c *ckCanvas, l ckLook, r ckRow, e sidebarAgentEntry, right, y int) int {
	pal := l.pal
	g, ink := l.ckMark(e.State, e.DoneSeen)
	words, bg := sidebarStateWords(e.State), l.panel
	switch {
	case m.ckArtFormRow(r, e) != "":
		g, ink = l.g(ckGlyphNeeds), pal.Attention
		words, bg = "form waiting", l.attnTint
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
	case e.State == "limit_reached":
		bg = l.warnTint
		if at := harness.LimitReset(e.Message); at != "" {
			words = "limit reached, resets " + at
		}
	}
	if r.agent < 0 && !r.hasItem {
		return right
	}
	if e.Settled && r.group == ckSettled {
		words = "settled"
	}
	x := c.textR(right, y, ckSeg{t: " " + g + " " + words + " ", fg: overlay.Readable(ink, bg), bg: bg, attrs: ckBold})
	c.mark(pal, x, y, right-x, 1, ckOn)
	return x
}

func (m *OS) drawItemCard(c *ckCanvas, l ckLook, r ckRow, x, w, y0, y1, top int, now time.Time, fold bool) int {
	st := &m.Inbox
	pal := l.pal
	it := r.item
	held := inboxHeld(it) || (it.Kind == session.AttentionAsk && it.RequestID != "")
	inner := w - 6
	base := 3
	if c.h < 30 {
		base = 1
	}
	st.ck.cardRoom = max(y1-y0-base-2, 0)
	var detail, inset []string
	plain := it.Kind == session.AttentionApproval && st.approvals.answer.reason == nil && inboxShowsWhole(it)
	cut := false
	if plain {
		where := m.inboxWhere(it)
		if r.agent >= 0 {
			e := st.ck.agents[r.agent]
			p := m.ckWorktreeOf(e).path
			switch {
			case m.ckManyMachines() && p != "":
				where = m.ckMachine(e.Host) + ":" + ckHomePath(p)
			case m.ckManyMachines() && e.Host == "":
				where = m.ckMachine("") + ":" + where
			case p != "":
				where = ckHomePath(p)
			}
		}
		inset, detail = m.ckApprovalLines(it, where, inner, now)
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
		if warn := session.ResumeWarning(it.RiskyFlags); warn != "" {
			for _, l := range wrapPlain(printableTitle(warn), inner) {
				detail = append(detail, "  "+l)
			}
		}
	}
	type btn struct{ key, label, action string }
	var btns []btn
	qopts := m.inboxQuestionOptions(it)
	switch {
	case st.approvals.answer.reason != nil:
	case len(qopts) > 0:
		for _, o := range qopts {
			if o.N < 1 || o.N > 9 {
				continue
			}
			btns = append(btns, btn{strconv.Itoa(o.N), ckTrunc(printableTitle(o.Label), 24), strconv.Itoa(o.N)})
		}
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
			labels[1] = "Always for " + ckTrunc(ckRuleWords(it.AlwaysScope[0]), 20)
		}
		for i, d := range [3]string{session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny} {
			if !slices.Contains(it.Options, d) {
				continue
			}
			action := [3]string{"a", "A", "X"}[i]
			key := m.inboxKeyOr([3]string{config.ActionInboxAllow, config.ActionInboxAllowAlways, config.ActionInboxReject}[i], action)
			btns = append(btns, btn{key, labels[i], action})
		}
		if it.DenyMessage {
			btns = append(btns, btn{m.inboxKeyOr(config.ActionInboxDenyReason, "R"), labels[3], "R"})
		}
	}
	if fold && !held && len(qopts) == 0 && !(it.Kind == session.AttentionAsk && it.RequestID != "") {
		detail, inset, btns, base = nil, nil, nil, 1
	}
	size := func() int {
		h := base + len(detail)
		if len(inset) > 0 {
			h += len(inset)
		}
		if len(btns) > 0 {
			h += 2
		}
		return h
	}
	h := size()
	if over := h - (y1 - y0); over > 0 && len(detail) > 0 {
		if len(btns) > 0 && over > len(detail)-1 {
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
	typing, _ := m.inboxTypingHeld(it, now)
	typing = typing && answerable && !cut
	m.noteInboxShown(it, held && inboxShowsWhole(it) && !(cut && answerable) && !typing, now)
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
	if it.Kind == session.AttentionResume {
		verb = "Resume"
	}
	if it.Kind == session.AttentionResume && m.Inbox.agents.resuming[it.ID] {
		verb = "Resuming"
	}
	title := verb
	switch {
	case held && it.Kind == session.AttentionPlan:
		title = "Approve the plan?"
	case held && it.Kind != session.AttentionAsk:
		title += "?"
	}
	g, gInk := l.ckItemMark(it)
	ty, by := cy+1, cy+3
	if base == 1 || y1-cy < h {
		ty, by = cy, cy+1
	}
	tx := c.text(x+3, ty, x+w-2, ckSeg{t: g + " ", fg: gInk}, ckSeg{t: title, fg: pal.Fg, attrs: ckBold})
	when := " waits " + units.Waited(it.Since, now)
	if !held {
		when = sepWord() + units.Waited(it.Since, now)
	}
	switch {
	case it.Stale:
		when = ", " + inboxSeen(it.SeenAt, now)
	case it.SnoozedUntil != 0:
		when = ", snoozed " + inboxSnoozedWhen(it.SnoozedUntil, now)
	}
	c.text(tx, ty, x+w-14, ckSeg{t: "   " + inboxWho(it) + when, fg: pal.FgMute})
	if inboxRisky(it) || len(it.RiskyFlags) > 0 {
		c.textR(x+w-2, ty, ckSeg{t: " risky ", fg: overlay.Readable(pal.Warn, l.warnTint), bg: l.warnTint, attrs: ckBold})
	} else if !answerable && !(it.Kind == session.AttentionAsk && it.RequestID != "") {
		cross := "×"
		if overlay.UseASCII() {
			cross = "x"
		}
		key := m.inboxKeyOr(config.ActionInboxDismiss, "d")
		dx := c.textR(x+w-2, ty, ckSeg{t: key, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " dismiss ", fg: pal.FgMute},
			ckSeg{t: " " + cross + " ", fg: pal.Fg, bg: l.cursor, attrs: ckBold})
		c.mark(pal, x+w-5, ty, 3, 1, ckOn)
		st.ck.hits = append(st.ck.hits, ckHit{x0: dx, y0: top + ty, x1: x + w - 2, y1: top + ty + 1, kind: ckHitAction, action: "dismiss"})
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
	reason := st.approvals.answer.reason
	reasonOpen := reason != nil && reason.ed != nil && m.InboxReasonOpen()
	reasonFrom := 0
	if reasonOpen {
		_, n := reason.ed.FrameSize()
		reasonFrom = len(detail) - n
		reason.wires.at.drawn, reason.wires.at.ok = false, false
	}
	for i, s := range detail {
		if yy >= y1 {
			break
		}
		if reasonOpen && i == max(reasonFrom, 0) {
			top := max(-reasonFrom, 0)
			at := &reason.wires.at
			at.x, at.y, at.top, at.drawn = x+3, yy-top, top, true
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
	if typing && len(btns) > 0 && yy+1 < y1 {
		bx := c.text(x+3, yy+1, x+w-2, ckSeg{t: " " + inboxTypingWords + " ", fg: overlay.Readable(pal.Attention, l.cursor), bg: l.cursor, attrs: ckBold})
		c.text(bx+2, yy+1, x+w-2, ckSeg{t: "Stop typing to answer.", fg: pal.FgMute})
		btns = nil
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
