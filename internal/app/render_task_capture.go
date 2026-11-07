package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// renderTaskCapture draws the task capture sheet: the title, the editor, the
// pills, a notice when there is one, and the keys.
func (m *OS) renderTaskCapture() (string, overlay.Geometry) {
	s := m.taskCapture
	cw, ch := m.GetRenderWidth(), m.GetRenderHeight()
	w0 := min(96, cw-4)
	if cw < 64 {
		w0 = cw
	}
	rows := min(tcTaskRows, max(ch-14, 1))
	h0 := min(rows+12, ch)
	l := m.cockpitLook()
	pal := l.pal
	t := spawnTones(l)
	m.tcCanvas = m.tcCanvas.reset(w0, h0, t.sheet)
	c := m.tcCanvas
	pad := 4
	if w0 < 72 {
		pad = 2
	}
	x, w, right := pad, w0-2*pad, w0-pad
	y := 1

	ex := c.textR(right, y, ckSeg{t: "esc", fg: t.key, attrs: ckBold}, ckSeg{t: " keep and close", fg: pal.FgDim})
	c.text(x, y, ex-2, ckSeg{t: "New task", fg: pal.Fg, attrs: ckBold}, ckSeg{t: "   the first line is its title", fg: pal.FgMute})
	y += 2

	// The editor, on the field's ground.
	box := rows + 1
	c.fill(x, y, w, box, t.field)
	if s.field == tcTask {
		lines := s.ed.Render(w-2, spawnStyles(pal, t.field), t.field)
		cut := max(len(lines)-rows, 0)
		for i, ln := range lines[cut:] {
			c.draw(x+1, y+i, w-2, ln)
		}
		at := &s.wires.at
		at.x, at.y, at.ok, at.drawn, at.top = x+1, y-cut, cut == 0, true, cut
	} else {
		s.wires.at.ok, s.wires.at.drawn = false, false
		text, ink := s.ed.Text(), pal.Fg
		if text == "" {
			text, ink = "What is the task? The first line is its title.", pal.FgDim
		}
		// Every line shows, wrapped, as the editor would show it.
		var lines []string
		for para := range strings.SplitSeq(text, "\n") {
			lines = append(lines, strings.Split(ansi.Wordwrap(para, w-6, ""), "\n")...)
		}
		for i, ln := range lines[:min(len(lines), rows)] {
			c.text(x+3, y+i, right-2, ckSeg{t: ln, fg: ink})
		}
	}
	c.textR(right-2, y+box-1, ckSeg{t: "ctrl+j", fg: overlay.ReadableAt(pal.AccentBright, t.field, overlay.MarkFloor), attrs: ckBold},
		ckSeg{t: " new line", fg: overlay.ReadableAt(pal.FgDim, t.field, overlay.ContrastFloor)})
	if pal.Depth == overlay.Depth16 {
		spawnBrackets(c, l, x, y, w, box)
	}
	y += box + 1

	// The pills: where the card goes, its stage, and the agent ctrl+enter
	// starts. Each steps with the arrows.
	px := x
	stage := "Todo"
	if tcStages[s.stage] != tcStages[0] {
		stage = "Plan first"
	}
	agent := "none installed"
	if s.agent < len(s.agents) {
		agent = s.agents[s.agent].cmd
	}
	sess := ""
	if s.sess < len(s.sessions) {
		sess = s.sessions[s.sess]
	}
	for _, p := range []struct {
		f            tcField
		label, value string
	}{{tcSession, "Session", sess}, {tcStage, "Stage", stage}, {tcAgent, "Agent to start", agent}} {
		ink := pal.FgDim
		if s.field == p.f {
			ink = t.key
		}
		c.text(px, y, right, ckSeg{t: p.label, fg: ink, attrs: ckBold})
		x0 := px
		end := c.text(px, y+1, right, ckSeg{t: " ‹ " + ckTrunc(p.value, 24) + " › ", fg: pal.Fg, bg: t.field})
		if s.field == p.f {
			c.mark(pal, x0, y+1, end-x0, 1, overlay.RowState{Cursor: true, Focused: true})
		}
		px = max(end, x0+ckWidth(ckSeg{t: p.label})) + 3
	}
	y += 3

	if s.notice != "" {
		c.text(x, y, right, ckSeg{t: ckTrunc(s.notice, w), fg: overlay.ReadableAt(pal.Warning, t.sheet, overlay.MarkFloor)})
	}
	if pal.Depth == overlay.Depth16 {
		spawnFrame(c, l, 0, 0, w0, h0)
	}

	fy := h0 - 2
	keys := []ckSeg{{t: overlay.EnterKey(), fg: t.key, attrs: ckBold}, {t: " add   ", fg: pal.FgDim},
		{t: "ctrl+" + overlay.EnterKey(), fg: t.key, attrs: ckBold}, {t: " add and start   ", fg: pal.FgDim},
		{t: "tab", fg: t.key, attrs: ckBold}, {t: " next   ", fg: pal.FgDim},
		{t: "←→", fg: t.key, attrs: ckBold}, {t: " change", fg: pal.FgDim}}
	c.text(x, fy, right, ckFitHints(keys, w)...)
	return c.render(), overlay.Geometry{Width: w0, Height: h0, InnerWidth: w0, TitleBar: overlay.Rect{X1: w0, Y1: 1}}
}
