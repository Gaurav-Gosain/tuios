package app

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The spawn sheet's drawing: a card over the dimmed cockpit, laid out as
// frame d. At 120 columns and wider the machines sit beside the fields; on a
// narrower screen they follow them, one line each.

// ckGlyphExpand marks a control that opens a list.
var ckGlyphExpand = ckGlyph{"", "v", "v"}

// spawnTone is the sheet's grounds, from the cockpit's look.
type spawnTone struct {
	sheet, field, pick color.Color
}

func spawnTones(l ckLook) spawnTone {
	pal := l.pal
	t := spawnTone{sheet: pal.Surface, field: l.panel, pick: ckFlat(pal.AccentTint)}
	switch pal.Depth {
	case overlay.Depth256:
		t.field = overlay.Apart256(t.field, pal.Surface)
		t.pick = overlay.Apart256(overlay.To256(t.pick), pal.Surface)
	case overlay.Depth16:
		t.pick = overlay.NoColor
	}
	return t
}

// spawnLayout is where the sheet's parts go for one screen.
type spawnLayout struct {
	x0, y0, w0, h0 int
	// x and w are the inner column; wide puts the machines beside the
	// fields at mx, mw.
	x, w    int
	wide    bool
	lw      int
	mx, mw  int
	gap     int
	taskH   int
	shown   int
	twoLine bool
}

// spawnPlan picks the roomiest layout that fits the screen.
func spawnPlan(s *spawnSheet, cw, ch int) spawnLayout {
	p := spawnLayout{w0: min(112, cw-4), wide: cw >= 120}
	if cw < 64 {
		p.w0 = cw
	}
	p.x0 = (cw - p.w0) / 2
	pad := 4
	if !p.wide {
		pad = 2
	}
	p.x, p.w = p.x0+pad, p.w0-2*pad
	p.lw = p.w
	if p.wide {
		p.lw = p.w/2 - 2
		p.mx, p.mw = p.x+p.lw+4, p.w-p.lw-4
	}
	chips := 0
	if s.attempts > 1 {
		chips = 2
	}
	for _, try := range []struct{ gap, task int }{{1, 3}, {0, 3}, {0, 2}, {0, 1}} {
		p.gap, p.taskH = try.gap, try.task
		p.shown, p.twoLine = len(s.machines), p.wide
		fields := 10 + 4*p.gap
		machines := func() int {
			if p.twoLine {
				return 1 + p.shown*(2+p.gap) + 2
			}
			return 1 + p.shown
		}
		body := max(fields, machines())
		if !p.wide {
			body = fields + 1 + machines()
		}
		// Title, task, columns, attempts, notice, footer and the pads.
		p.h0 = 3 + (p.taskH + 2) + 1 + body + 1 + 2 + chips + 3
		if p.h0 <= ch-2 {
			break
		}
		for p.shown > 1 && p.h0 > ch-2 {
			p.shown--
			body = max(fields, machines())
			if !p.wide {
				body = fields + 1 + machines()
			}
			p.h0 = 3 + (p.taskH + 2) + 1 + body + 1 + 2 + chips + 3
		}
		if p.h0 <= ch-2 {
			break
		}
	}
	p.h0 = min(p.h0, ch)
	p.y0 = max((ch-p.h0)/2, 0)
	return p
}

// drawSpawnSheet draws the sheet over c, whose top row is top on the screen.
func (m *OS) drawSpawnSheet(c *ckCanvas, l ckLook, top int) {
	s := m.Inbox.ck.spawn
	pal := l.pal
	t := spawnTones(l)
	s.hits = s.hits[:0]
	p := spawnPlan(s, c.w, c.h)
	hit := func(x0, y0, x1, y1 int, action string) {
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y0, x1: x1, y1: top + y1, kind: ckHitAction, action: action})
	}
	c.fill(p.x0, p.y0, p.w0, p.h0, t.sheet)
	if pal.Depth == overlay.Depth16 {
		// No ground sets the sheet apart from the dimmed cockpit: rules do.
		c.hline(p.x0, p.y0, p.w0, hzGlyph(), pal.Edge)
		c.hline(p.x0, p.y0+p.h0-1, p.w0, hzGlyph(), pal.Edge)
		for y := p.y0; y < p.y0+p.h0; y++ {
			c.text(p.x0, y, 0, ckSeg{t: vtGlyph(), fg: pal.Edge})
			c.text(p.x0+p.w0-1, y, 0, ckSeg{t: vtGlyph(), fg: pal.Edge})
		}
	}
	x, w, right := p.x, p.w, p.x+p.w
	y := p.y0 + 1

	// The title: where the agent starts from, and how to close.
	from := ""
	switch {
	case s.repo != "" && s.head != "":
		from = "from " + s.repo + " / " + s.head
	case s.repo != "":
		from = "from " + s.repo
	case s.folder != "":
		from = "in " + s.folder
	}
	ex := c.textR(right, y, ckSeg{t: "esc", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " close", fg: pal.FgMute})
	hit(ex, y, right, y+1, "close")
	c.text(x, y, ex-2, ckSeg{t: "New agent", fg: pal.Fg, attrs: ckBold}, ckSeg{t: "   " + ckTrunc(from, ex-x-16), fg: pal.FgMute})
	y += 2

	// The task.
	m.spawnLabel(c, l, s, spTask, x, y, "Task")
	box := p.taskH + 1
	c.fill(x, y+1, w, box, t.field)
	hit(x, y, right, y+1+box, "field:"+strconv.Itoa(int(spTask)))
	if s.field == spTask {
		lines := s.task.Render(w-2, spawnStyles(pal, t.field), t.field)
		lines = lines[max(len(lines)-p.taskH, 0):]
		for i, ln := range lines {
			c.draw(x+1, y+1+i, w-2, ln)
		}
	} else {
		text, ink := s.task.Text(), pal.Fg
		if strings.TrimSpace(text) == "" {
			text, ink = "What should the agent do?", pal.FgMute
		}
		var rows []string
		for _, para := range strings.Split(text, "\n") {
			rows = append(rows, strings.Split(ansi.Wordwrap(para, w-6, ""), "\n")...)
		}
		for i, r := range rows[:min(len(rows), p.taskH)] {
			c.text(x+3, y+1+i, right-2, ckSeg{t: r, fg: ink})
		}
	}
	c.textR(right-2, y+box, ckSeg{t: "ctrl+j", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " new line", fg: pal.FgMute})
	y += box + 2

	// The fields, then the machines beside them or under them.
	colTop := y
	y = m.drawSpawnFields(c, l, t, p, colTop, top)
	my, mx, mw := colTop, p.mx, p.mw
	if !p.wide {
		my, mx, mw = y+1, x, w
	}
	y = max(y, m.drawSpawnMachines(c, l, t, p, mx, my, mw, top))

	// The attempts.
	y++
	c.hline(x, y, w, hzGlyph(), l.edge)
	y++
	m.spawnLabel(c, l, s, spAttempts, x, y, "Attempts")
	ax := x + 12
	fx := c.text(ax, y, 0, ckSeg{t: " - ", fg: pal.FgDim, bg: pal.Card})
	hit(ax, y, fx, y+1, "fewer")
	nx := c.text(fx, y, 0, ckSeg{t: "  " + strconv.Itoa(s.attempts) + "  ", fg: pal.Fg, bg: t.field, attrs: ckBold})
	px := c.text(nx, y, 0, ckSeg{t: " + ", fg: pal.FgDim, bg: pal.Card})
	hit(nx, y, px, y+1, "more-attempts")
	hit(x, y, ax, y+1, "field:"+strconv.Itoa(int(spAttempts)))
	words := "One agent works on the task."
	if s.attempts > 1 {
		words = strconv.Itoa(s.attempts) + " agents work on the task, each in its own worktree and branch."
	}
	c.text(px+3, y, right, ckSeg{t: words, fg: pal.FgMute})
	if s.attempts > 1 && len(s.agents) > 0 {
		y += 2
		cx := ax
		for i := range s.attempts {
			name := s.agents[s.attemptAgent(i)].cmd
			if cx+ckWidth(ckSeg{t: name})+8 > right-8 {
				c.text(cx, y, right, ckSeg{t: "+" + strconv.Itoa(s.attempts-i), fg: pal.FgMute})
				cx = right
				break
			}
			x0 := cx
			cx = c.text(cx, y, 0, ckSeg{t: " " + string(rune('A'+i)) + " ", fg: pal.AccentBright, bg: pal.Card, attrs: ckBold},
				ckSeg{t: " " + name + " ", fg: pal.Fg, bg: t.field})
			hit(x0, y, cx, y+1, "chip:"+strconv.Itoa(i))
			cx += 2
		}
		if cx < right-6 {
			vx := c.text(cx, y, 0, ckSeg{t: "+ vary", fg: pal.FgDim})
			hit(cx, y, vx, y+1, "vary")
		}
	}

	// Why Start did nothing, or what the sheet still waits for.
	ny := p.y0 + p.h0 - 4
	switch {
	case s.notice != "":
		c.text(x, ny, right, ckSeg{t: s.notice, fg: pal.Warning})
	case !s.read:
		c.text(x, ny, right, ckSeg{t: "Reading the folder and the agents.", fg: pal.FgMute})
	}

	// The footer: the keys, then Cancel and Start.
	fy := p.y0 + p.h0 - 2
	label := "Start agent"
	if s.attempts > 1 {
		label = "Start " + strconv.Itoa(s.attempts) + " agents"
	}
	btn := func(word string, f spawnField, ink, ground color.Color, bold uint8) ckSeg {
		if s.field == f {
			return ckSeg{t: "[ " + word + " ]", fg: ink, bg: ground, attrs: bold}
		}
		return ckSeg{t: "  " + word + "  ", fg: ink, bg: ground, attrs: bold}
	}
	sx := c.textR(right, fy, btn(label, spStart, pal.PillFg, pal.Accent, ckBold))
	c.mark(pal, sx, fy, right-sx, 1, ckOn)
	hit(sx, fy, right, fy+1, "start")
	cx := c.textR(sx-2, fy, btn("Cancel", spCancel, pal.Fg, pal.Card, 0))
	c.mark(pal, cx, fy, sx-2-cx, 1, overlay.RowState{Cursor: true})
	hit(cx, fy, sx-2, fy+1, "cancel")
	keys := []ckSeg{{t: overlay.EnterKey(), fg: pal.AccentBright, attrs: ckBold}, {t: " start   ", fg: pal.FgMute},
		{t: "tab", fg: pal.AccentBright, attrs: ckBold}, {t: " next field   ", fg: pal.FgMute}}
	if ckWidth(keys...)+ckWidth(ckSeg{t: "esc close"}) < cx-x-2 {
		keys = append(keys, ckSeg{t: "esc", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " close", fg: pal.FgMute})
	}
	c.text(x, fy, cx-2, keys...)

	if s.list {
		m.drawSpawnList(c, l, t, x, colTop+2, p.lw, p.y0+p.h0-3, top)
	}
}

// spawnStyles are the composer's styles on the sheet's field ground.
func spawnStyles(pal overlay.Palette, field color.Color) composer.Styles {
	st := composerStyles(pal)
	st.Field = field
	return st
}

// spawnLabel draws a field's label, lit while the field has the keys.
func (m *OS) spawnLabel(c *ckCanvas, l ckLook, s *spawnSheet, f spawnField, x, y int, text string) {
	ink := l.pal.FgDim
	if s.field == f {
		ink = l.pal.AccentBright
	}
	c.text(x, y, 0, ckSeg{t: text, fg: ink, attrs: ckBold})
}

// drawSpawnFields draws the agent, the model, where it works, the branch and
// the base, and returns the row under them.
func (m *OS) drawSpawnFields(c *ckCanvas, l ckLook, t spawnTone, p spawnLayout, y, top int) int {
	s := m.Inbox.ck.spawn
	pal := l.pal
	x, w := p.x, p.lw
	hit := func(x0, y0, x1, y1 int, action string) {
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y0, x1: x1, y1: top + y1, kind: ckHitAction, action: action})
	}
	step := 2 + p.gap

	// The agent: the running and installed ones, then "more" for the rest.
	m.spawnLabel(c, l, s, spAgent, x, y, "Agent")
	more := " more " + l.g(ckGlyphExpand)
	limit := x + w - ckWidth(ckSeg{t: more}) - 2
	var shown []int
	for i, a := range s.agents {
		if a.seen > 0 || a.installed || !s.read {
			shown = append(shown, i)
		}
	}
	if len(shown) == 0 {
		for i := range min(len(s.agents), 4) {
			shown = append(shown, i)
		}
	}
	// The picked agent is always one of the segments.
	width := func(ix []int) int {
		n := 0
		for _, i := range ix {
			n += ckWidth(ckSeg{t: s.agents[i].cmd}) + 3
		}
		return n
	}
	for len(shown) > 1 && x+width(shown) > limit {
		shown = shown[:len(shown)-1]
	}
	if s.agent < len(s.agents) && !slices.Contains(shown, s.agent) && len(shown) > 0 {
		shown[len(shown)-1] = s.agent
	}
	sx := x
	for _, i := range shown {
		a := s.agents[i]
		seg := ckSeg{t: " " + a.cmd + " ", fg: pal.FgDim, bg: pal.Card}
		if i == s.agent {
			seg = ckSeg{t: " " + a.cmd + " ", fg: pal.PillFg, bg: pal.Accent, attrs: ckBold}
		}
		x0 := sx
		sx = c.text(sx, y+1, limit, seg)
		if i == s.agent {
			c.mark(pal, x0, y+1, sx-x0, 1, ckOn)
		}
		hit(x0, y+1, sx, y+2, "agent:"+strconv.Itoa(i))
		sx++
	}
	if len(s.agents) == 0 && s.read {
		c.text(x, y+1, x+w, ckSeg{t: "No agent found. Install one, such as claude or codex.", fg: pal.Warning})
	} else {
		mx := c.text(sx+1, y+1, 0, ckSeg{t: more, fg: pal.FgMute})
		hit(sx+1, y+1, mx, y+2, "more")
	}
	hit(x, y, x+w, y+1, "field:"+strconv.Itoa(int(spAgent)))
	y += step

	// The model.
	m.spawnLabel(c, l, s, spModel, x, y, "Model")
	iw := min(30, w)
	m.spawnInput(c, l, t, s, spModel, s.model, x, y+1, iw, "The agent's default", "", true, top)
	if w-iw > 14 {
		c.text(x+iw+2, y+1, x+w, ckSeg{t: "passed as --model", fg: pal.FgMute})
	}
	y += step

	// Where it works.
	m.spawnLabel(c, l, s, spWhere, x, y, "Where it works")
	hit(x, y, x+w, y+1, "field:"+strconv.Itoa(int(spWhere)))
	rx := x
	for i, opt := range []string{"New worktree", "This folder"} {
		on := s.worktree == (i == 0)
		mark, mc, ink := l.g(ckGlyphIdle), pal.FgMute, pal.FgDim
		if on {
			mark, mc, ink = "●", pal.AccentBright, pal.Fg
		}
		if (i == 0 && !s.spawnCanWorktree() && s.read) || (i == 1 && s.host() != "") {
			mc, ink = pal.FgMute, pal.FgMute
		}
		x0 := rx
		rx = c.text(rx, y+1, x+w, ckSeg{t: mark, fg: mc}, ckSeg{t: " " + opt, fg: ink})
		hit(x0, y+1, rx, y+2, "where:"+strconv.Itoa(i))
		rx += 4
	}
	y += step

	// The branch and the base, for a new worktree.
	m.spawnLabel(c, l, s, spBranch, x, y, "Branch")
	suffix := ""
	if s.worktree && s.branchAuto != "" && strings.TrimSpace(s.branch.Text()) == s.branchAuto {
		suffix = "named after the task"
	}
	m.spawnInput(c, l, t, s, spBranch, s.branch, x, y+1, w, "agent/name", suffix, s.worktree, top)
	y += step
	m.spawnLabel(c, l, s, spBase, x, y, "Base")
	bw := min(20, w)
	m.spawnInput(c, l, t, s, spBase, s.base, x, y+1, bw, "HEAD", "", s.worktree, top)
	if s.worktree && s.dirty > 0 && w-bw > 10 {
		words := strconv.Itoa(s.dirty) + " uncommitted files stay in this folder."
		if s.dirty == 1 {
			words = "1 uncommitted file stays in this folder."
		}
		c.text(x+bw+2, y+1, x+w, ckSeg{t: words, fg: pal.FgMute})
	}
	return y + 2
}

// spawnInput draws a one-line field: the editor while it has the keys, its
// text or its placeholder otherwise. A field that does not apply is drawn
// muted and takes no click.
func (m *OS) spawnInput(c *ckCanvas, l ckLook, t spawnTone, s *spawnSheet, f spawnField, ed *composer.Model, x, y, w int, placeholder, suffix string, live bool, top int) {
	pal := l.pal
	c.fill(x, y, w, 1, t.field)
	sw := 0
	if suffix != "" {
		sw = ckWidth(ckSeg{t: suffix}) + 2
		c.textR(x+w-1, y, ckSeg{t: suffix, fg: pal.FgMute})
	}
	switch {
	case live && s.field == f:
		if lines := ed.Render(w-sw, spawnStyles(pal, t.field), t.field); len(lines) > 0 {
			c.draw(x, y, w-sw, lines[0])
		}
	case strings.TrimSpace(ed.Text()) == "":
		c.text(x+2, y, x+w-sw, ckSeg{t: placeholder, fg: pal.FgMute})
	default:
		ink := pal.Fg
		if !live {
			ink = pal.FgMute
		}
		c.text(x+2, y, x+w-sw, ckSeg{t: ckTrunc(ed.Text(), w-sw-3), fg: ink})
	}
	if pal.Depth == overlay.Depth16 {
		c.mark(pal, x, y, w-sw, 1, overlay.RowState{Cursor: true})
	}
	if live {
		s.hits = append(s.hits, ckHit{x0: x, y0: top + y - 1, x1: x + w, y1: top + y + 1, kind: ckHitAction, action: "field:" + strconv.Itoa(int(f))})
	}
}

// drawSpawnMachines draws the machines at x, y, w, best fit first, and
// returns the row under them.
func (m *OS) drawSpawnMachines(c *ckCanvas, l ckLook, t spawnTone, p spawnLayout, x, y, w, top int) int {
	s := m.Inbox.ck.spawn
	pal := l.pal
	right := x + w
	m.spawnLabel(c, l, s, spMachine, x, y, "Machine")
	c.textR(right, y, ckSeg{t: "best fit first", fg: pal.FgMute})
	y++
	agent := ""
	if s.agent < len(s.agents) {
		agent = s.agents[s.agent].cmd
	}
	missing := false
	for i, mc := range s.machines[:p.shown] {
		picked := i == s.machine
		ground := t.field
		if picked {
			ground = t.pick
		}
		rows := 1
		if p.twoLine {
			rows = 2
		}
		c.fill(x, y, w, rows, ground)
		if picked {
			for r := range rows {
				c.text(x, y+r, 0, ckSeg{t: "▌", fg: pal.Accent})
			}
		}
		mark, mk, ink := l.g(ckGlyphIdle), pal.FgMute, pal.Fg
		if picked {
			mark, mk = "●", pal.AccentBright
		}
		if !mc.up {
			ink = pal.FgMute
		}
		nx := c.text(x+2, y, 0, ckSeg{t: mark, fg: mk}, ckSeg{t: "  " + l.g(ckGlyphHost) + " ", fg: pal.FgMute},
			ckSeg{t: ckTrunc(mc.name, 16), fg: ink, attrs: ckBold})
		// The load: a bar of ten, or a word when the machine does not say.
		var load []ckSeg
		if mc.loadOK && mc.cores > 0 {
			frac := min(max(mc.load/float64(mc.cores), 0), 1)
			n := int(math.Round(frac * 10))
			bar := pal.Busy
			if frac >= 0.6 {
				bar = pal.Warning
			}
			cell := "■"
			if l.ascii {
				cell = "#"
			}
			load = []ckSeg{{t: strings.Repeat(cell, n), fg: bar}, {t: strings.Repeat(cell, 10-n), fg: l.edgePanel},
				{t: " " + strconv.Itoa(int(math.Round(frac*100))) + "%", fg: pal.FgDim}}
		} else {
			load = []ckSeg{{t: "no load report", fg: pal.FgMute}}
		}
		sub := strconv.Itoa(mc.working) + " working"
		if mc.cores > 0 {
			sub = strconv.Itoa(mc.cores) + " cores" + sepWord() + sub
		}
		switch {
		case !mc.up:
			sub += sepWord() + "not connected"
		case mc.host == "":
			sub += sepWord() + "this machine"
		}
		if p.twoLine {
			c.textR(right-2, y, load...)
			end := right - 2
			if mc.host == "" && agent != "" && s.read {
				if s.agents[s.agent].installed {
					end = c.textR(right-2, y+1, ckSeg{t: agent + " ready", fg: pal.FgDim})
				} else {
					end = c.textR(right-2, y+1, ckSeg{t: agent + " not found", fg: pal.Warning})
					missing = true
				}
			}
			c.text(x+5, y+1, end-2, ckSeg{t: ckTrunc(sub, end-2-x-5), fg: pal.FgMute})
		} else {
			lx := c.text(nx+2, y, right, load...)
			c.text(lx+2, y, right, ckSeg{t: strconv.Itoa(mc.working) + " working", fg: pal.FgMute})
		}
		if picked {
			c.mark(pal, x, y, w, rows, ckOn)
		}
		if mc.up {
			s.hits = append(s.hits, ckHit{x0: x, y0: top + y, x1: right, y1: top + y + rows, kind: ckHitAction, action: "machine:" + strconv.Itoa(i)})
		}
		y += rows
		if p.twoLine {
			y += p.gap
		}
	}
	if p.twoLine {
		c.text(x, y, right, ckSeg{t: "The order holds still while this sheet is open.", fg: pal.FgMute})
		if missing {
			c.text(x, y+1, right, ckSeg{t: s.machines[0].name + ": " + agent + " is not on this PATH.", fg: pal.FgMute})
		}
		y += 2
	}
	return y
}

// drawSpawnList draws the list of every agent under the agent field.
func (m *OS) drawSpawnList(c *ckCanvas, l ckLook, t spawnTone, x, y, w, bottom, top int) {
	s := m.Inbox.ck.spawn
	pal := l.pal
	rows := min(len(s.agents), bottom-y-2)
	if rows <= 0 {
		return
	}
	first := min(max(s.listAt-rows/2, 0), len(s.agents)-rows)
	c.fill(x, y, w, rows+2, pal.Card)
	c.hline(x, y, w, hzGlyph(), l.edge)
	c.hline(x, y+rows+1, w, hzGlyph(), l.edge)
	for r := range rows {
		i := first + r
		a := s.agents[i]
		yy := y + 1 + r
		ink, ground := pal.Fg, pal.Card
		if i == s.listAt {
			ink, ground = pal.PillFg, pal.Accent
		}
		c.fill(x, yy, w, 1, ground)
		what, tone := "not found on this machine", pal.FgMute
		switch {
		case a.seen > 0:
			what, tone = strconv.Itoa(a.seen)+" running", pal.FgDim
		case a.installed:
			what, tone = "installed", pal.FgDim
		}
		if i == s.listAt {
			tone = pal.PillFg
		}
		c.text(x+2, yy, x+w-2, ckSeg{t: ckPad(a.cmd, 16), fg: ink, attrs: ckBold}, ckSeg{t: what, fg: tone})
		if i == s.listAt {
			c.mark(pal, x, yy, w, 1, ckOn)
		}
		s.hits = append(s.hits, ckHit{x0: x, y0: top + yy, x1: x + w, y1: top + yy + 1, kind: ckHitAction, action: "agent:" + strconv.Itoa(i)})
	}
}

// dim fades every cell toward the canvas, so the sheet stands out from the
// cockpit under it. At 16 colours the cells are drawn faint, without grounds.
// cache holds the faded colours, which a frame repeats many times.
func (c *ckCanvas) dim(l ckLook, cache map[color.Color]color.Color) {
	pal := l.pal
	fade := func(col color.Color) color.Color {
		if out, ok := cache[col]; ok {
			return out
		}
		out := overlay.MixColors(col, l.canvas, 0.6)
		if pal.Depth == overlay.Depth256 {
			out = overlay.To256(out)
		} else {
			out = ckFlat(out)
		}
		cache[col] = out
		return out
	}
	for y := range c.h {
		line := c.buf.Lines[y]
		for x := range line {
			cell := &line[x]
			if pal.Depth == overlay.Depth16 {
				cell.Style.Bg = nil
				cell.Style.Attrs = cell.Style.Attrs&^uv.AttrReverse | uv.AttrFaint
				continue
			}
			fg := cell.Style.Fg
			if fg == nil {
				fg = pal.Fg
			}
			cell.Style.Fg = fade(fg)
			if cell.Style.Bg != nil {
				cell.Style.Bg = fade(cell.Style.Bg)
			}
		}
	}
}
