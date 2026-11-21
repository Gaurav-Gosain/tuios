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

var (
	ckGlyphExpand = ckGlyph{"\ueab4", "˅", "v"}
	ckGlyphPicked = ckGlyph{"●", "●", "*"}
	ckGlyphBar    = ckGlyph{"▌", "▌", "|"}
	ckGlyphLoad   = ckGlyph{"■", "■", "#"}
	ckGlyphUnload = ckGlyph{"■", "■", "-"}
)

type spawnTone struct {
	sheet, field, pick color.Color
	key                color.Color
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
	t.key = overlay.ReadableAt(pal.AccentBright, t.sheet, overlay.MarkFloor)
	return t
}

type spawnLayout struct {
	x0, y0, w0, h0 int
	x, w           int
	wide           bool
	lw             int
	mx, mw         int
	gap            int
	taskH          int
	shown          int
	twoLine        bool
}

func spawnPlan(s *spawnSheet, cw, ch, taskRows int) spawnLayout {
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
		p.lw = min(p.w/2-2, 44)
		p.mx, p.mw = p.x+p.lw+4, p.w-p.lw-4
	}
	chips := 0
	if s.attempts > 1 {
		chips = 2
	}
	height := func() int {
		fields := 12 + 5*p.gap
		machines := 1 + p.shown
		if p.twoLine {
			machines = 1 + p.shown*(2+p.gap) + 2
		}
		body := max(fields, machines)
		if !p.wide {
			body = fields + 1 + machines
		}
		return 3 + (p.taskH + 2) + 1 + body + 1 + 2 + chips + 3
	}
	want := max(taskRows, 1)
	tries := []struct{ gap, task int }{{1, want}, {0, want}}
	for n := want - 1; n >= 1; n-- {
		tries = append(tries, struct{ gap, task int }{0, n})
	}
	for _, try := range tries {
		p.gap, p.taskH = try.gap, try.task
		p.shown, p.twoLine = len(s.machines), p.wide
		for p.shown > 1 && height() > ch-2 {
			p.shown--
		}
		if p.h0 = height(); p.h0 <= ch-2 {
			break
		}
	}
	if s.task != nil && s.task.Full() {
		for height() < ch-2 {
			p.taskH++
		}
		p.h0 = height()
	}
	p.h0 = min(p.h0, ch)
	p.y0 = max((ch-p.h0)/2, 0)
	return p
}

func (m *OS) drawSpawnSheet(c *ckCanvas, l ckLook, top int) {
	s := m.Inbox.ck.spawn
	pal := l.pal
	t := spawnTones(l)
	s.hits = s.hits[:0]
	p := spawnPlan(s, c.w, c.h, m.composerRows(spawnTaskBoxRows))
	s.shown = p.shown
	if s.machine >= p.shown {
		s.machine = 0
	}
	hit := func(x0, y0, x1, y1 int, action string) {
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y0, x1: x1, y1: top + y1, kind: ckHitAction, action: action})
	}
	c.fill(p.x0, p.y0, p.w0, p.h0, t.sheet)
	if pal.Depth == overlay.Depth16 {
		spawnFrame(c, l, p.x0, p.y0, p.w0, p.h0)
	}
	x, w, right := p.x, p.w, p.x+p.w
	y := p.y0 + 1

	from := ""
	switch {
	case s.repo != "" && s.head != "":
		from = "from " + s.repo + " / " + s.head
	case s.repo != "":
		from = "from " + s.repo
	case s.folder != "":
		from = "in " + s.folder
	}
	ex := c.textR(right, y, ckSeg{t: "esc", fg: t.key, attrs: ckBold}, ckSeg{t: " close", fg: pal.FgMute})
	hit(ex, y, right, y+1, "close")
	c.text(x, y, ex-2, ckSeg{t: "New agent", fg: pal.Fg, attrs: ckBold}, ckSeg{t: "   " + ckTrunc(from, ex-x-16), fg: pal.FgMute})
	y += 2

	m.spawnLabel(c, l, s, spTask, x, y, "Task")
	box := p.taskH + 1
	c.fill(x, y+1, w, box, t.field)
	hit(x, y, right, y+1+box, "field:"+strconv.Itoa(int(spTask)))
	if s.field == spTask {
		s.task.SetMaxRows(max(m.composerRows(spawnTaskRows), p.taskH))
		s.task.SetRoom(p.taskH + 1)
		lines := s.task.Render(w-2, spawnStyles(pal, t.field), t.field)
		cut := max(len(lines)-p.taskH, 0)
		for i, ln := range lines[cut:] {
			c.draw(x+1, y+1+i, w-2, ln)
		}
		at := &s.taskWires.at
		at.x, at.y, at.ok, at.drawn, at.top = x+1, y+1-cut, cut == 0, true, cut
	} else {
		s.taskWires.at.ok, s.taskWires.at.drawn = false, false
		text, ink := s.task.Text(), pal.Fg
		if strings.TrimSpace(text) == "" {
			text, ink = "What should the agent do?", pal.FgDim
		}
		var rows []string
		for para := range strings.SplitSeq(text, "\n") {
			rows = append(rows, strings.Split(ansi.Wordwrap(para, w-6, ""), "\n")...)
		}
		for i, r := range rows[:min(len(rows), p.taskH)] {
			c.text(x+3, y+1+i, right-2, ckSeg{t: r, fg: ink})
		}
	}
	c.textR(right-2, y+box, ckSeg{t: "ctrl+j", fg: t.key, attrs: ckBold}, ckSeg{t: " new line", fg: pal.FgMute})
	if pal.Depth == overlay.Depth16 {
		spawnBrackets(c, l, x, y+1, w, box)
	}
	y += box + 2

	colTop := y
	y = m.drawSpawnFields(c, l, t, p, colTop, top)
	my, mx, mw := colTop, p.mx, p.mw
	if !p.wide {
		my, mx, mw = y+1, x, w
	}
	y = max(y, m.drawSpawnMachines(c, l, t, p, mx, my, mw, top))

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

	ny := p.y0 + p.h0 - 4
	switch {
	case s.notice != "":
		c.text(x, ny, right, ckSeg{t: s.notice, fg: pal.Warning})
	case s.starting:
		c.text(x, ny, right, ckSeg{t: "Starting. The result shows in a notice.", fg: pal.FgMute})
	case !s.read:
		c.text(x, ny, right, ckSeg{t: "Reading the folder and the agents.", fg: pal.FgMute})
	}

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
		startInk, startGround = pal.FgMute, pal.Card
	}
	sx := c.textR(right, fy, btn(label, spStart, startInk, startGround, ckBold))
	c.mark(pal, sx, fy, right-sx, 1, ckOn)
	hit(sx, fy, right, fy+1, "start")
	cx := c.textR(sx-2, fy, btn("Cancel", spCancel, pal.Fg, pal.Card, 0))
	c.mark(pal, cx, fy, sx-2-cx, 1, overlay.RowState{Cursor: true})
	hit(cx, fy, sx-2, fy+1, "cancel")
	keys := []ckSeg{{t: overlay.EnterKey(), fg: t.key, attrs: ckBold}, {t: " start   ", fg: pal.FgMute},
		{t: "tab", fg: t.key, attrs: ckBold}, {t: " next field   ", fg: pal.FgMute}}
	for _, k := range [][2]string{{"ctrl+s", " save as preset   "}, {"esc", " close"}} {
		if ckWidth(keys...)+ckWidth(ckSeg{t: k[0] + k[1]}) < cx-x-2 {
			keys = append(keys, ckSeg{t: k[0], fg: t.key, attrs: ckBold}, ckSeg{t: k[1], fg: pal.FgMute})
		}
	}
	c.text(x, fy, cx-2, keys...)

	if s.list {
		m.drawSpawnList(c, l, s.atX[s.listFor], s.at[s.listFor], p.lw, p.y0+1, p.y0+p.h0-1, top)
	}
}

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
		segs := []ckSeg{{t: " " + string(rune('A'+i)) + " ", fg: overlay.ReadableAt(pal.AccentBright, pal.Card, overlay.MarkFloor), bg: pal.Card, attrs: ckBold},
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

func spawnBrackets(c *ckCanvas, l ckLook, x, y, w, h int) {
	for j := y; j < y+h; j++ {
		c.text(x, j, 0, ckSeg{t: "[", fg: l.pal.Edge})
		c.text(x+w-1, j, 0, ckSeg{t: "]", fg: l.pal.Edge})
	}
}

func spawnStyles(pal overlay.Palette, field color.Color) composer.Styles {
	st := composerStyles(pal)
	st.Field = field
	st.Sigil = " "
	return st
}

func (m *OS) spawnLabel(c *ckCanvas, l ckLook, s *spawnSheet, f spawnField, x, y int, text string) {
	ink := l.pal.FgDim
	if s.field == f {
		ink = overlay.ReadableAt(l.pal.AccentBright, l.pal.Surface, overlay.MarkFloor)
	}
	c.text(x, y, 0, ckSeg{t: text, fg: ink, attrs: ckBold})
}

func (m *OS) drawSpawnFields(c *ckCanvas, l ckLook, t spawnTone, p spawnLayout, y, top int) int {
	s := m.Inbox.ck.spawn
	pal := l.pal
	x, w := p.x, p.lw
	hit := func(x0, y0, x1, y1 int, action string) {
		s.hits = append(s.hits, ckHit{x0: x0, y0: top + y0, x1: x1, y1: top + y1, kind: ckHitAction, action: action})
	}
	step := 2 + p.gap
	expand := l.g(ckGlyphExpand)

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
			seg.fg = overlay.ReadableAt(pal.FgMute, pal.Card, overlay.MarkFloor)
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

	launch := s.launch()
	m.spawnLabel(c, l, s, spModel, x, y, "Model")
	iw := min(28, w)
	models := len(s.listItems(spModel)) > 0
	if launch.ModelFlag == "" && s.read {
		c.text(x, y+1, x+w, ckSeg{t: "This agent takes no model choice.", fg: pal.FgMute})
	} else {
		m.spawnInput(c, l, t, s, spModel, s.model, x, y+1, iw, "The agent's default", "", true, models, top)
		if hintW := x + w - (x + iw + 2); hintW >= 17 && launch.ModelFlag != "" {
			c.text(x+iw+2, y+1, x+w, ckSeg{t: "passed as " + launch.ModelFlag, fg: pal.FgMute})
		}
	}
	y += step

	px := x
	for _, f := range []spawnField{spEffort, spPermission} {
		label, flag := "Effort", launch.EffortFlag
		if f == spPermission {
			label, flag = "Permissions", launch.PermissionFlag
		}
		if flag == "" {
			continue
		}
		m.spawnLabel(c, l, s, f, px, y, label)
		v := s.choice(f)
		if f == spPermission && launch.RiskyPermission(v) {
			c.text(px+len(label)+2, y, x+w, ckSeg{t: "approvals off", fg: overlay.ReadableAt(pal.Warning, t.sheet, overlay.MarkFloor), attrs: ckBold})
		}
		shown := v
		if shown == "" {
			shown = "default"
		}
		seg := ckSeg{t: " " + shown + " " + expand + " ", fg: pal.Fg, bg: t.field}
		x0 := px
		s.at[f], s.atX[f] = y+1, px
		px = c.text(px, y+1, x+w, seg)
		if s.field == f {
			c.mark(pal, x0, y+1, px-x0, 1, overlay.RowState{Cursor: true, Focused: true})
		}
		hit(x0, y, px, y+1, "field:"+strconv.Itoa(int(f)))
		hit(x0, y+1, px, y+2, "list:"+strconv.Itoa(int(f)))
		px = max(px+3, x0+len(label)+3)
	}
	if px == x && s.read && s.agent < len(s.agents) {
		c.text(x, y+1, x+w, ckSeg{t: "This agent takes no effort or permission choice.", fg: pal.FgMute})
	}
	y += step

	m.spawnLabel(c, l, s, spWhere, x, y, "Where it works")
	hit(x, y, x+w, y+1, "field:"+strconv.Itoa(int(spWhere)))
	rx := x
	for i, opt := range []string{"New worktree", "This folder"} {
		on := s.worktree == (i == 0)
		mark, mc, ink := l.g(ckGlyphIdle), pal.FgMute, pal.FgDim
		if on {
			mark, mc, ink = l.g(ckGlyphPicked), t.key, pal.Fg
		}
		if (i == 0 && !s.spawnCanWorktree() && s.read) || (i == 1 && s.host() != "") {
			mc, ink = pal.FgMute, pal.FgMute
		}
		x0 := rx
		rx = c.text(rx, y+1, x+w, ckSeg{t: mark, fg: mc}, ckSeg{t: " " + opt, fg: ink})
		hit(x0, y+1, rx, y+2, "where:"+strconv.Itoa(i))
		rx += 4
	}
	if n := m.ckFolderAgents(s.folder); n > 0 && s.host() == "" {
		words := strconv.Itoa(n) + " agents work here. A worktree is safer."
		if n == 1 {
			words = "1 agent works here. A worktree is safer."
		}
		if p.gap > 0 {
			c.text(x, y+2, x+w, ckSeg{t: words, fg: pal.Warn})
		} else {
			c.text(rx, y+1, x+w, ckSeg{t: strconv.Itoa(n) + " at work here", fg: pal.Warn})
		}
	}
	y += step

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
		sw = ckWidth(ckSeg{t: suffix}) + 3
		c.textR(x+w-2, y, ckSeg{t: suffix, fg: pal.FgMute})
	}
	switch {
	case live && s.field == f:
		if lines := ed.Render(w-sw, spawnStyles(pal, t.field), t.field); len(lines) > 0 {
			c.draw(x, y, w-sw, lines[0])
		}
	case strings.TrimSpace(ed.Text()) == "":
		c.text(x+2, y, x+w-sw, ckSeg{t: placeholder, fg: pal.FgDim})
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
		s.hits = append(s.hits, ckHit{x0: x, y0: top + y - 1, x1: x + w - sw, y1: top + y + 1, kind: ckHitAction, action: "field:" + strconv.Itoa(int(f))})
	}
}

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
	missing := ""
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
		quiet := overlay.ReadableAt(pal.FgMute, ground, overlay.MarkFloor)
		mark, mk, ink := l.g(ckGlyphIdle), quiet, pal.Fg
		if picked {
			mark, mk = l.g(ckGlyphPicked), overlay.ReadableAt(pal.AccentBright, ground, overlay.MarkFloor)
		}
		if !mc.up {
			ink = quiet
		}
		if mc.any {
			c.text(x+2, y, 0, ckSeg{t: mark, fg: mk}, ckSeg{t: "  " + l.g(ckGlyphHost) + " ", fg: quiet},
				ckSeg{t: mc.name, fg: ink, attrs: ckBold})
			pick, why, warn := spawnAnyWords(s)
			if picked && warn {
				missing = why
			}
			pickInk := overlay.ReadableAt(pal.FgDim, ground, overlay.ContrastFloor)
			if warn {
				pickInk = overlay.ReadableAt(pal.Warning, ground, overlay.MarkFloor)
			}
			if p.twoLine {
				c.textR(right-2, y, ckSeg{t: pick, fg: pickInk})
				c.text(x+5, y+1, right-2, ckSeg{t: ckTrunc(why, right-2-x-5), fg: quiet})
			} else {
				c.textR(right-2, y, ckSeg{t: pick, fg: pickInk})
			}
			if picked {
				c.mark(pal, x, y, w, rows, ckOn)
			}
			s.hits = append(s.hits, ckHit{x0: x, y0: top + y, x1: right, y1: top + y + rows, kind: ckHitAction, action: "machine:" + strconv.Itoa(i)})
			y += rows
			if p.twoLine {
				y += p.gap
			}
			continue
		}
		nx := c.text(x+2, y, 0, ckSeg{t: mark, fg: mk}, ckSeg{t: "  " + l.g(ckGlyphHost) + " ", fg: quiet},
			ckSeg{t: ckTrunc(mc.name, 16), fg: ink, attrs: ckBold})
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
				empty = "·"
			}
			load = []ckSeg{{t: strings.Repeat(l.g(ckGlyphLoad), n), fg: bar}, {t: strings.Repeat(empty, 10-n), fg: l.edgePanel},
				{t: " " + strconv.Itoa(int(math.Round(frac*100))) + "%", fg: pal.FgDim}}
		} else {
			load = []ckSeg{{t: "no load report", fg: quiet}}
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
			if agent != "" && mc.up {
				words, ok := spawnReadyWords(mc, s.agents[s.agent].id, agent)
				ink := overlay.ReadableAt(pal.FgDim, ground, overlay.ContrastFloor)
				if !ok {
					ink = overlay.ReadableAt(pal.Warning, ground, overlay.MarkFloor)
					if picked {
						missing = words
					}
				}
				end = c.textR(right-2, y+1, ckSeg{t: words, fg: ink})
			}
			c.text(x+5, y+1, end-2, ckSeg{t: ckTrunc(sub, end-2-x-5), fg: quiet})
		} else {
			lx := c.text(nx+2, y, right, load...)
			c.text(lx+2, y, right, ckSeg{t: strconv.Itoa(mc.working) + " working", fg: quiet})
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
		c.text(x, y, right, ckSeg{t: "Most free cores first, then fewest agents working.", fg: pal.FgMute})

		second, ink := "The order holds still while this sheet is open.", pal.FgMute
		if len(s.machines) > 0 && s.machines[0].any {
			second = "Any machine picks at Start, by the same rule."
		}
		switch {
		case missing != "" && s.anyPicked():
			second, ink = missing, pal.Warning
		case missing != "" && s.machine < len(s.machines):
			second, ink = s.machines[s.machine].name+": "+missing+".", pal.Warning
		}
		c.text(x, y+1, right, ckSeg{t: second, fg: ink})
		y += 2
	}
	return y
}

func (m *OS) drawSpawnList(c *ckCanvas, l ckLook, x, y, w, top0, bottom, top int) {
	if s := m.Inbox.ck.spawn; s.dd != nil {
		drawDropdown(c, l, s.dd, x, y, w, top0, bottom, top)
	}
}

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

func spawnAnyWords(s *spawnSheet) (pick, why string, warn bool) {
	switch {
	case s.anyHere():
		return "this machine", "An agent in this folder works on this machine.", false
	case s.placeErr != "":
		return "no machine fits", s.placeErr, true
	case s.place != nil:
		return s.place.Name, s.place.Why, false
	}
	return "finding", "Asking each machine for its room.", false
}
