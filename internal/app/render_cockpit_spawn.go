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

// The sheet's glyphs: a control that opens a list, the picked radio and
// machine, the bar beside the picked machine, and a load cell full and
// empty.
var (
	ckGlyphExpand = ckGlyph{"", "v", "v"}
	ckGlyphPicked = ckGlyph{"●", "●", "*"}
	ckGlyphBar    = ckGlyph{"▌", "▌", "|"}
	ckGlyphLoad   = ckGlyph{"■", "■", "#"}
	ckGlyphUnload = ckGlyph{"■", "■", "-"}
)

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
		// The fields take what frame d gives them and the machines the
		// rest, so a machine's second line has room.
		p.lw = min(p.w/2-2, 44)
		p.mx, p.mw = p.x+p.lw+4, p.w-p.lw-4
	}
	chips := 0
	if s.attempts > 1 {
		chips = 2
	}
	height := func() int {
		fields := 10 + 4*p.gap
		machines := 1 + p.shown
		if p.twoLine {
			machines = 1 + p.shown*(2+p.gap) + 2
		}
		body := max(fields, machines)
		if !p.wide {
			body = fields + 1 + machines
		}
		// Title, task, columns, attempts, notice, footer and the pads.
		return 3 + (p.taskH + 2) + 1 + body + 1 + 2 + chips + 3
	}
	for _, try := range []struct{ gap, task int }{{1, 3}, {0, 3}, {0, 2}, {0, 1}} {
		p.gap, p.taskH = try.gap, try.task
		p.shown, p.twoLine = len(s.machines), p.wide
		for p.shown > 1 && height() > ch-2 {
			p.shown--
		}
		if p.h0 = height(); p.h0 <= ch-2 {
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
	s.shown = p.shown
	if s.machine >= p.shown {
		s.machine = 0
	}
	hit := func(x0, y0, x1, y1 int, action string) {
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y0, x1: x1, y1: top + y1, kind: ckHitAction, action: action})
	}
	c.fill(p.x0, p.y0, p.w0, p.h0, t.sheet)
	if pal.Depth == overlay.Depth16 {
		// No ground sets the sheet apart from the dimmed cockpit: a frame
		// does.
		spawnFrame(c, l, p.x0, p.y0, p.w0, p.h0)
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
	if pal.Depth == overlay.Depth16 {
		spawnBrackets(c, l, x, y+1, w, box)
	}
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
		m.drawSpawnChips(c, l, t, s, ax, y, right, top)
	}

	// Why Start did nothing, or what the sheet still waits for.
	ny := p.y0 + p.h0 - 4
	switch {
	case s.notice != "":
		c.text(x, ny, right, ckSeg{t: s.notice, fg: pal.Warning})
	case s.starting:
		c.text(x, ny, right, ckSeg{t: "Starting. The result shows in a notice.", fg: pal.FgMute})
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
	startInk, startGround := pal.PillFg, pal.Accent
	if !s.read || s.starting {
		// Start waits for the folder and the agents to be read.
		startInk, startGround = pal.FgMute, pal.Card
	}
	sx := c.textR(right, fy, btn(label, spStart, startInk, startGround, ckBold))
	c.mark(pal, sx, fy, right-sx, 1, ckOn)
	hit(sx, fy, right, fy+1, "start")
	cx := c.textR(sx-2, fy, btn("Cancel", spCancel, pal.Fg, pal.Card, 0))
	c.mark(pal, cx, fy, sx-2-cx, 1, overlay.RowState{Cursor: true})
	hit(cx, fy, sx-2, fy+1, "cancel")
	keys := []ckSeg{{t: overlay.EnterKey(), fg: pal.AccentBright, attrs: ckBold}, {t: " start   ", fg: pal.FgMute},
		{t: "tab", fg: pal.AccentBright, attrs: ckBold}, {t: " next field   ", fg: pal.FgMute}}
	for _, k := range [][2]string{{"ctrl+s", " save as preset   "}, {"esc", " close"}} {
		if ckWidth(keys...)+ckWidth(ckSeg{t: k[0] + k[1]}) < cx-x-2 {
			keys = append(keys, ckSeg{t: k[0], fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: k[1], fg: pal.FgMute})
		}
	}
	c.text(x, fy, cx-2, keys...)

	if s.list {
		m.drawSpawnList(c, l, s.atX[s.listFor], s.at[s.listFor], p.lw, p.y0+1, p.y0+p.h0-1, top)
	}
}

// drawSpawnChips draws one chip per attempt: its letter, agent, model and
// machine, then "+ vary". A click on a chip gives it the next agent.
func (m *OS) drawSpawnChips(c *ckCanvas, l ckLook, t spawnTone, s *spawnSheet, x, y, right, top int) {
	pal := l.pal
	host := ""
	if s.machine < len(s.machines) {
		host = s.machines[s.machine].name
	}
	names := make([]string, s.attempts)
	for i := range names {
		names[i] = s.agents[s.attemptAgent(i)].cmd
		if mod := s.modelOf(i); mod != "" {
			names[i] += " " + mod
		}
	}
	// The machine is named on each chip when every chip has room for it,
	// and "+ vary" always keeps its place at the end.
	where := l.g(ckGlyphHost) + " " + host + " "
	room := right - 8 - x
	need := 0
	for _, n := range names {
		need += ckWidth(ckSeg{t: " A  " + n + " " + where}) + 2
	}
	if need > room {
		where = ""
	}
	cx := x
	for i, name := range names {
		if cx+ckWidth(ckSeg{t: " A  " + name + " " + where})+6 > right-8 {
			cx = c.text(cx, y, right, ckSeg{t: "+" + strconv.Itoa(s.attempts-i), fg: pal.FgMute}) + 2
			break
		}
		x0 := cx
		segs := []ckSeg{{t: " " + string(rune('A'+i)) + " ", fg: pal.AccentBright, bg: pal.Card, attrs: ckBold},
			{t: " " + name + " ", fg: pal.Fg, bg: t.field}}
		if where != "" {
			segs = append(segs, ckSeg{t: where, fg: pal.FgMute, bg: t.field})
		}
		cx = c.text(cx, y, 0, segs...)
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y, x1: cx, y1: top + y + 1, kind: ckHitAction, action: "chip:" + strconv.Itoa(i)})
		cx += 2
	}
	vx := c.text(cx, y, right, ckSeg{t: "+ vary", fg: pal.FgDim})
	s.hits = append(s.hits, ckHit{x0: cx, y0: top + y, x1: vx, y1: top + y + 1, kind: ckHitAction, action: "vary"})
}

// spawnFrame draws a box at 16 colours, where no ground marks the sheet.
func spawnFrame(c *ckCanvas, l ckLook, x, y, w, h int) {
	edge := l.pal.Edge
	c.hline(x, y, w, hzGlyph(), edge)
	c.hline(x, y+h-1, w, hzGlyph(), edge)
	for j := y; j < y+h; j++ {
		c.text(x, j, 0, ckSeg{t: vtGlyph(), fg: edge})
		c.text(x+w-1, j, 0, ckSeg{t: vtGlyph(), fg: edge})
	}
	corners := [4]string{"╭", "╮", "╰", "╯"}
	if l.ascii {
		corners = [4]string{"+", "+", "+", "+"}
	}
	c.text(x, y, 0, ckSeg{t: corners[0], fg: edge})
	c.text(x+w-1, y, 0, ckSeg{t: corners[1], fg: edge})
	c.text(x, y+h-1, 0, ckSeg{t: corners[2], fg: edge})
	c.text(x+w-1, y+h-1, 0, ckSeg{t: corners[3], fg: edge})
}

// spawnBrackets marks a field's ends at 16 colours, where it has no ground.
func spawnBrackets(c *ckCanvas, l ckLook, x, y, w, h int) {
	for j := y; j < y+h; j++ {
		c.text(x, j, 0, ckSeg{t: "[", fg: l.pal.Edge})
		c.text(x+w-1, j, 0, ckSeg{t: "]", fg: l.pal.Edge})
	}
}

// spawnStyles are the composer's styles on the sheet's field ground.
func spawnStyles(pal overlay.Palette, field color.Color) composer.Styles {
	st := composerStyles(pal)
	st.Field = field
	// The label names the field: no prompt mark in front of the text.
	st.Sigil = " "
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
	expand := l.g(ckGlyphExpand)

	// The agent: the running, the installed and the common ones, then
	// "more" for every manifest.
	m.spawnLabel(c, l, s, spAgent, x, y, "Agent")
	s.at[spAgent], s.atX[spAgent] = y+1, x
	more := " more " + expand
	limit := x + w - ckWidth(ckSeg{t: more}) - 2
	var shown []int
	for i, a := range s.agents {
		if a.seen > 0 || a.installed || !s.read || slices.Contains(spawnCommon, a.id) {
			shown = append(shown, i)
		}
	}
	if len(shown) == 0 {
		for i := range min(len(s.agents), 4) {
			shown = append(shown, i)
		}
	}
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
	// The picked agent is always one of the segments.
	if s.agent < len(s.agents) && !slices.Contains(shown, s.agent) && len(shown) > 0 {
		shown[len(shown)-1] = s.agent
	}
	sx := x
	for _, i := range shown {
		a := s.agents[i]
		seg := ckSeg{t: " " + a.cmd + " ", fg: pal.FgDim, bg: pal.Card}
		switch {
		case i == s.agent:
			seg = ckSeg{t: " " + a.cmd + " ", fg: pal.PillFg, bg: pal.Accent, attrs: ckBold}
		case s.read && a.seen == 0 && !a.installed:
			seg.fg = pal.FgMute
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
		hit(sx+1, y+1, mx, y+2, "list:"+strconv.Itoa(int(spAgent)))
	}
	hit(x, y, x+w, y+1, "field:"+strconv.Itoa(int(spAgent)))
	y += step

	// The model: typed, or picked from the models running agents report.
	m.spawnLabel(c, l, s, spModel, x, y, "Model")
	iw := min(28, w)
	models := len(s.listItems(spModel)) > 0
	m.spawnInput(c, l, t, s, spModel, s.model, x, y+1, iw, "The agent's default", "", true, models, top)
	hintW := x + w - (x + iw + 2)
	switch {
	case models && hintW >= 14:
		c.text(x+iw+2, y+1, x+w, ckSeg{t: "from the agent", fg: pal.FgMute})
	case hintW >= 17:
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
			mark, mc, ink = l.g(ckGlyphPicked), pal.AccentBright, pal.Fg
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
		suffix = "from the task"
	}
	m.spawnInput(c, l, t, s, spBranch, s.branch, x, y+1, w, "agent/name", suffix, s.worktree, false, top)
	y += step
	m.spawnLabel(c, l, s, spBase, x, y, "Base")
	bw := min(20, w)
	m.spawnInput(c, l, t, s, spBase, s.base, x, y+1, bw, "HEAD", "", s.worktree, len(s.branches) > 0, top)
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
// text or its placeholder otherwise. A field with a list ends in its mark,
// which opens the list. A field that does not apply is drawn muted and
// takes no click.
func (m *OS) spawnInput(c *ckCanvas, l ckLook, t spawnTone, s *spawnSheet, f spawnField, ed *composer.Model, x, y, w int, placeholder, suffix string, live, list bool, top int) {
	pal := l.pal
	s.at[f], s.atX[f] = y, x
	c.fill(x, y, w, 1, t.field)
	sw := 0
	switch {
	case list && live:
		sw = 4
		c.text(x+w-3, y, 0, ckSeg{t: l.g(ckGlyphExpand), fg: pal.FgDim})
		s.hits = append(s.hits, ckHit{x0: x + w - 3, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitAction, action: "list:" + strconv.Itoa(int(f))})
	case suffix != "" && ckWidth(ckSeg{t: ed.Text() + suffix})+6 <= w:
		// The note goes when the text needs its room.
		sw = ckWidth(ckSeg{t: suffix}) + 3
		c.textR(x+w-2, y, ckSeg{t: suffix, fg: pal.FgMute})
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
		spawnBrackets(c, l, x, y, w, 1)
	}
	if live {
		// Behind the list mark's hit, which comes first.
		s.hits = append(s.hits, ckHit{x0: x, y0: top + y - 1, x1: x + w - sw, y1: top + y + 1, kind: ckHitAction, action: "field:" + strconv.Itoa(int(f))})
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
				c.text(x, y+r, 0, ckSeg{t: l.g(ckGlyphBar), fg: pal.Accent})
			}
		}
		mark, mk, ink := l.g(ckGlyphIdle), pal.FgMute, pal.Fg
		if picked {
			mark, mk = l.g(ckGlyphPicked), pal.AccentBright
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
			empty := l.g(ckGlyphUnload)
			if pal.Depth == overlay.Depth16 && !l.ascii {
				// At 16 colours the empty cells have no colour of their
				// own, and a picked row is reversed: a dot tells them
				// from the full ones.
				empty = "·"
			}
			load = []ckSeg{{t: strings.Repeat(l.g(ckGlyphLoad), n), fg: bar}, {t: strings.Repeat(empty, 10-n), fg: l.edgePanel},
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
			c.text(x, y+1, right, ckSeg{t: s.machines[0].name + ": " + agent + " is not installed there.", fg: pal.FgMute})
		}
		y += 2
	}
	return y
}

// drawSpawnList draws the open list under the row y of its field at x: below
// it when it fits, above it otherwise.
func (m *OS) drawSpawnList(c *ckCanvas, l ckLook, x, y, w, top0, bottom, top int) {
	s := m.Inbox.ck.spawn
	pal := l.pal
	items := s.listItems(s.listFor)
	below := bottom - (y + 1) - 2
	above := y - top0 - 2
	rows := min(len(items), max(below, above))
	if rows <= 0 {
		return
	}
	y0 := y + 1
	if rows > below {
		y0 = y - rows - 2
	}
	first := min(max(s.listAt-rows/2, 0), len(items)-rows)
	c.fill(x, y0, w, rows+2, pal.Card)
	c.hline(x, y0, w, hzGlyph(), l.edge)
	c.hline(x, y0+rows+1, w, hzGlyph(), l.edge)
	for r := range rows {
		i := first + r
		it := items[i]
		yy := y0 + 1 + r
		ink, tone, ground := pal.Fg, pal.FgDim, pal.Card
		if it.muted {
			ink, tone = pal.FgDim, pal.FgMute
		}
		if i == s.listAt {
			ink, tone, ground = pal.PillFg, pal.PillFg, pal.Accent
		}
		c.fill(x, yy, w, 1, ground)
		c.text(x+2, yy, x+w-2, ckSeg{t: ckPad(it.label, 20) + " ", fg: ink, attrs: ckBold}, ckSeg{t: it.note, fg: tone})
		if i == s.listAt {
			c.mark(pal, x, yy, w, 1, ckOn)
		}
		// The list's rows come last, so they win over the fields under
		// them.
		s.hits = append(s.hits, ckHit{x0: x, y0: top + yy, x1: x + w, y1: top + yy + 1, kind: ckHitAction, action: "item:" + strconv.Itoa(i)})
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
