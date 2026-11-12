//go:build !js

package app

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/hostsetup"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The add sheet's drawing: a card over the dimmed screen on the spawn
// sheet's canvas, tones and frame. It has three parts: the step strip, one
// list of steps per machine (hostsetup.Flow), and one drawer for the prompt
// that waits. The steps' words and marks are hostsetup's. It is laid out for
// 120 columns and more, and at 80 it cuts what does not fit.

// msStepNames is the strip of the five steps.
var msStepNames = []string{"Pick", "Test", "Set up tuios", "Both ways", "Done"}

// msPainter draws the sheet: x and w are the inner column, y the next row,
// and bottom the row of the keys.
type msPainter struct {
	c          *ckCanvas
	l          ckLook
	pal        overlay.Palette
	t          spawnTone
	s          *machineSheet
	x, w, y    int
	bottom     int
	hints      [][2]string
	sec, prime string
}

// renderMachineSheet draws the sheet.
func (m *OS) renderMachineSheet() (string, overlay.Geometry) {
	// The width is the room that clears the rail, as panelWidth's is.
	cw, ch := m.modalRoomWidth(), m.GetRenderHeight()
	w0 := min(128, cw-4)
	if cw < 100 {
		w0 = cw - 2
	}
	w0, h0 := max(w0, min(cw, 40)), max(min(36, ch-2), min(ch, 12))
	l := m.cockpitLook()
	t := spawnTones(l)
	m.msCanvas = m.msCanvas.reset(w0, h0, t.sheet)
	pad := 4
	if w0 < 100 {
		pad = 2
	}
	p := &msPainter{c: m.msCanvas, l: l, pal: l.pal, t: t, s: m.machineSheet, x: pad, w: w0 - 2*pad, y: 1, bottom: h0 - 2}
	p.head()
	switch p.s.view {
	case msViewPick:
		p.pickView()
	case msViewRun:
		p.runView()
	default:
		p.doneView()
	}
	// The content stops above the keys: on a short screen it is cut.
	p.c.fill(0, p.bottom-1, w0, h0-p.bottom+1, t.sheet)
	if l.pal.Depth == overlay.Depth16 {
		spawnFrame(p.c, l, 0, 0, w0, h0)
	}
	p.footer()
	return p.c.render(), overlay.Geometry{Width: w0, Height: h0, InnerWidth: w0, TitleBar: overlay.Rect{X1: w0, Y1: 1}}
}

func seg(t string, fg color.Color) ckSeg  { return ckSeg{t: t, fg: fg} }
func bold(t string, fg color.Color) ckSeg { return ckSeg{t: t, fg: fg, attrs: ckBold} }

// put draws t at x, y in fg, bold when b, cut at the right edge, and returns
// the column after it.
func (p *msPainter) put(x, y int, fg color.Color, b bool, t string) int {
	var a uint8
	if b {
		a = ckBold
	}
	return p.c.text(x, y, p.x+p.w, ckSeg{t: t, fg: fg, attrs: a})
}

// wrap draws t wrapped from x to the right edge, and returns the row under it.
func (p *msPainter) wrap(x, y int, t string, fg color.Color, b bool) int {
	if t == "" {
		return y
	}
	for _, line := range strings.Split(ansi.Wordwrap(t, p.x+p.w-x, ""), "\n") { //nolint:modernize // a range func costs a closure here
		if y < p.bottom-1 {
			p.put(x, y, fg, b, line)
		}
		y++
	}
	return y
}

// at is the step of the strip the sheet is on.
func (s *machineSheet) at() int {
	switch {
	case s.view == msViewPick:
		return 0
	case s.view == msViewDone:
		return 4
	case len(s.flows) > 1:
		return 2
	}
	at := 1
	for _, ln := range s.flows[0].Lines {
		if ln.Step == hostsetup.StepReverse {
			at = 3
		} else if ln.Step != hostsetup.StepProbe && ln.Step != hostsetup.StepFix {
			at = max(at, 2)
		}
	}
	if a := s.msAsk(); a != nil && a.Kind != hostsetup.AskFix {
		at = max(at, 2)
		if a.Question != nil && a.Question.ID == "both_ways" {
			at = 3
		}
	}
	return at
}

// head draws the title, the close key, the step strip and the rule.
func (p *msPainter) head() {
	pal, x, y := p.pal, p.x, p.y
	ex := p.c.textR(p.x+p.w, y, bold("esc", pal.AccentBright), seg(" close", pal.FgMute))
	p.c.text(x, y, ex-2, bold("Add a machine", pal.Fg))
	y += 2
	at := p.s.at()
	for i, n := range msStepNames {
		num, ink := ckSeg{t: " " + strconv.Itoa(i+1) + " ", fg: pal.FgMute, bg: pal.Card}, pal.FgMute
		switch {
		case i < at:
			num, ink = ckSeg{t: hostsetup.Mark(hostsetup.StateOK), fg: pal.Success}, pal.FgDim
		case i == at:
			num, ink = ckSeg{t: num.t, fg: pal.PillFg, bg: pal.Accent, attrs: ckBold}, pal.Fg
		}
		x = p.put(p.c.text(x, y, 0, num), y, ink, i == at, " "+n)
		if i < 4 {
			x = p.put(x+2, y, p.l.edge, false, hzGlyph()+hzGlyph()) + 2
		}
	}
	p.c.hline(p.x, y+2, p.w, hzGlyph(), p.l.edge)
	p.y = y + 4
}

// keys sets the footer: the hints, and the buttons.
func (p *msPainter) keys(hints [][2]string, secondary, primary string) {
	p.hints, p.sec, p.prime = hints, secondary, primary
}

// footer draws the hints on the left and the buttons on the right.
func (p *msPainter) footer() {
	pal, fy, r := p.pal, p.bottom, p.x+p.w
	for i, label := range []string{p.prime, p.sec} {
		if label == "" {
			continue
		}
		st := ckSeg{t: " " + label + " ", fg: pal.Fg, bg: pal.Card}
		if i == 0 {
			st = ckSeg{t: st.t, fg: pal.PillFg, bg: pal.Accent, attrs: ckBold}
		}
		if pal.Depth == overlay.Depth16 {
			st = ckSeg{t: "[" + label + "]", fg: pal.Fg, attrs: st.attrs}
		}
		r = p.c.textR(r, fy, st) - 2
	}
	x := p.x
	for _, h := range p.hints {
		k := strings.Replace(h[0], "enter", overlay.EnterKey(), 1)
		if x+ansi.StringWidth(k+h[1])+1 > r-1 {
			break
		}
		x = p.put(p.put(x, fy, pal.AccentBright, true, k)+1, fy, pal.FgMute, false, h[1]) + 3
	}
}

// field draws a labelled one-line field, with a cursor when it has the keys.
func (p *msPainter) field(y int, label, text, placeholder string, focused bool) {
	pal, w := p.pal, min(p.w, 72)
	p.put(p.x, y, pal.FgDim, true, label)
	p.c.fill(p.x, y+1, w, 1, p.t.field)
	if pal.Depth == overlay.Depth16 {
		spawnBrackets(p.c, p.l, p.x, y+1, w, 1)
	}
	x := p.put(p.x+1, y+1, pal.Fg, false, text)
	if focused {
		x = p.c.text(x, y+1, 0, ckSeg{t: " ", bg: pal.Fg}) + 1
	}
	if text == "" {
		p.c.text(x, y+1, p.x+w-1, seg(placeholder, pal.FgMute))
	}
}

// row draws the cursor's ground on row y when cur, and returns the column
// its text starts at.
func (p *msPainter) row(y int, cur bool) int {
	if cur {
		st := overlay.RowState{Cursor: true, Focused: true}
		p.c.fill(p.x, y, p.w, 1, p.pal.Ground(st, p.t.pick))
		p.c.mark(p.pal, p.x, y, p.w, 1, st)
		p.put(p.x, y, p.pal.Accent, false, p.l.g(ckGlyphBar))
	}
	return p.x + 2
}

func (p *msPainter) pickView() {
	pal, pk := p.pal, &p.s.pick
	p.c.hline(p.x, p.y+1, p.w, hzGlyph(), p.l.edge)
	tx := p.x
	for i, t := range []string{"Address", "Tailnet", "ssh config"} {
		ink := pal.FgMute
		if i == pk.tab {
			ink = pal.Fg
		}
		end := p.put(tx, p.y, ink, i == pk.tab, " "+t+" ")
		if i == pk.tab {
			p.c.hline(tx, p.y+1, end-tx, "━", pal.Accent)
		}
		tx = end + 3
	}
	p.y += 3
	switch pk.tab {
	case msTabAddress:
		name := cmpOr(strings.TrimSpace(pk.name), msNameOf(strings.TrimSpace(pk.addr)))
		p.field(p.y, "Address", pk.addr, "user@host, or a name in your ssh config", pk.field == 0)
		p.field(p.y+3, "Name", pk.name, cmpOr(name, "the name here"), pk.field == 1)
		p.field(p.y+6, "This machine's address for it", pk.back, "optional. tuios tries the tailnet name, then the host name", pk.field == 2)
		p.wrap(p.x, p.y+9, "Next, tuios tests the connection. It installs nothing without asking.", pal.FgDim, false)
		p.keys([][2]string{{"enter", "test and add"}, {"tab", "next field"}}, "Cancel", strings.TrimSpace("Test "+name))
	case msTabSSH:
		p.keys([][2]string{{"enter", "test and add"}, {"ctrl+r", "look again"}}, "Cancel", "Test")
		if len(pk.aliases) == 0 {
			p.put(p.x, p.y, pal.FgMute, false, "Your ssh config names no machines.")
		}
		for i := max(pk.aliasList.Cursor-(p.bottom-p.y-3), 0); i < len(pk.aliases) && p.y < p.bottom-2; i++ {
			p.put(p.row(p.y, i == pk.aliasList.Cursor), p.y, pal.Fg, false, p.l.g(ckGlyphHost)+"  "+pk.aliases[i])
			p.y++
		}
	case msTabTailnet:
		p.tailnetTab()
	}
}

func (p *msPainter) tailnetTab() {
	pal, pk := p.pal, &p.s.pick
	if pk.peers == nil {
		p.wrap(p.x, p.y, cmpOr(pk.peersErr, "Reading your tailnet. Each machine gets one short test."), pal.FgDim, false)
		return
	}
	p.keys(pk.peers.Hints(), "Cancel", "Set up "+msCount(len(pk.peers.Picked()), "machine", "machines"))
	answers := "Each ticked machine gets this version of tuios. It starts at boot (b). It is added both ways (w)."
	if pk.noBoot {
		answers = strings.Replace(answers, "starts", "does not start", 1)
	}
	if pk.oneWay {
		answers = strings.Replace(answers, "is added", "is not added", 1)
	}
	nw := 18
	for i, peer := range pk.peers.Peers {
		if p.y >= p.bottom-5 {
			break
		}
		box, ink := "   ", pal.FgMute
		if peer.CanSetUp {
			box, ink = "[ ]", pal.Fg
			if pk.peers.Ticked[i] {
				box = "[x]"
			}
		}
		x := p.put(p.row(p.y, i == pk.peers.Cursor), p.y, pal.AccentBright, true, box)
		p.put(x+2, p.y, ink, false, ckPad(peer.Name, nw)+" "+ckPad(peer.OS, 8)+" "+peer.Why)
		p.y++
	}
	p.wrap(p.x, p.y+1, answers, pal.FgDim, false)
}

// markInk is the ink of a step state's mark.
func (p *msPainter) markInk(state string) color.Color {
	switch state {
	case hostsetup.StateOK:
		return p.pal.Success
	case hostsetup.StateFailed:
		return p.pal.Warn
	case hostsetup.StateRun:
		return p.pal.Info
	case hostsetup.StatePerson:
		return p.pal.Attention
	}
	return p.pal.FgMute
}

// cmd draws a command, tagged with where it runs, on the field ground.
func (p *msPainter) cmd(x, y int, c hostsetup.Command) {
	p.c.fill(x, y, p.x+p.w-x, 1, p.t.field)
	p.put(p.put(x+1, y, p.pal.FgMute, false, ckPad(c.Where, 9)+" "), y, p.pal.Fg, false, ckTrunc(c.Line, p.x+p.w-x-12))
}

func (p *msPainter) runView() {
	s, pal := p.s, p.pal
	p.lines()
	switch ask := s.msAsk(); {
	case s.err != "":
		p.wrap(p.x, p.y+1, s.err, pal.Warn, true)
		p.keys([][2]string{{"esc", "close"}}, "", "Close")
	case ask != nil:
		p.prompt(ask)
	default:
		p.keys([][2]string{{"esc", "stop"}}, "Stop", "")
	}
}

// lines draws the steps of each machine, from its Flow.
func (p *msPainter) lines() {
	s, pal := p.s, p.pal
	bulk := len(s.flows) > 1
	for _, f := range s.flows {
		if bulk {
			p.put(p.x, p.y, pal.Fg, true, f.Name)
			p.y++
		}
		for _, ln := range f.Lines {
			if p.y >= p.bottom-2 || bulk && ln.State == hostsetup.StateOK && ln.Step != hostsetup.StepProbe {
				continue
			}
			p.put(p.x+2, p.y, p.markInk(ln.State), false, hostsetup.Mark(ln.State))
			p.put(p.x+5, p.y, pal.Fg, true, ln.Label)
			p.y = max(p.wrap(p.x+28, p.y, ln.Text, pal.FgDim, false), p.y+1)
			if ln.State == hostsetup.StateRun && ln.Total > 0 {
				bw := min(40, p.w/3)
				lit := int(int64(bw) * ln.Done / ln.Total)
				p.put(p.x+5, p.y, pal.AccentBright, false, "["+strings.Repeat("#", lit)+strings.Repeat("-", bw-lit)+"]  "+
					strconv.FormatInt(ln.Done>>20, 10)+" of "+strconv.FormatInt(ln.Total>>20, 10)+" MB")
				p.y++
			}
			if ln.State == hostsetup.StateFailed {
				p.y = p.wrap(p.x+5, p.y, ln.Detail, pal.FgMute, false)
			}
		}
		if bulk {
			p.y++
		}
	}
}

// prompt draws the question that waits, and its keys.
func (p *msPainter) prompt(ask *hostsetup.Prompt) {
	pal, y := p.pal, p.y+1
	who := ""
	if len(p.s.flows) > 1 {
		who = ask.Machine + ": "
	}
	p.keys(ask.Hints(), "", "")
	switch ask.Kind {
	case hostsetup.AskPick:
		y = p.wrap(p.x, y, who+"Pick how to install tuios on "+p.s.name()+".", pal.Fg, true) + 1
		for i, o := range ask.Options {
			mark, ink, note := "○ ", pal.FgMute, o.WhyNot
			if o.Available {
				ink, note = pal.Fg, o.Note
			}
			if i == ask.Cursor {
				mark = p.l.g(ckGlyphPicked) + " "
			}
			x := p.put(p.put(p.row(y, i == ask.Cursor), y, pal.AccentBright, false, mark), y, ink, true, o.Label)
			if ansi.StringWidth(note) < p.x+p.w-x {
				p.put(x+1, y, pal.FgMute, false, note)
			}
			y++
			for j, c := range o.Commands {
				if o.Available && (j == 0 || ask.All) && y < p.bottom-2 {
					p.cmd(p.x+4, y, c)
					y++
				}
			}
		}
		p.prime = "Install on " + p.s.name()
	case hostsetup.AskConfirm:
		q := ask.Question
		y = p.wrap(p.x, y, who+q.Text, pal.Fg, true)
		y = p.wrap(p.x, y, q.Note, pal.FgMute, false)
		for _, c := range q.Commands {
			y++
			p.cmd(p.x+2, y, c)
		}
		p.sec, p.prime = "No", "Yes"
		if !ask.Yes {
			p.sec, p.prime = "Yes", "No"
		}
	case hostsetup.AskFix:
		f := ask.Failure
		y = p.wrap(p.x, y, who+f.Reason, pal.Attention, true)
		y = p.wrap(p.x, y, f.Fix, pal.Fg, false)
		if f.FixCommand != "" {
			p.cmd(p.x+2, y+1, hostsetup.Command{Where: "here", Line: f.FixCommand})
			y += 2
		}
		p.wrap(p.x, y+1, f.SSHSaid, pal.FgMute, false)
	case hostsetup.AskPerson:
		ps := ask.Person
		y = p.wrap(p.x, y, who+ps.Why, pal.Attention, true)
		if ps.URL != "" {
			p.wrap(p.x, y, "Press enter to open the sign-in page. When you signed in, press t to test again.", pal.Fg, false)
			p.keys(append([][2]string{{"enter", "open the page"}}, ask.Hints()...), "", "Open the sign-in page")
			return
		}
		p.wrap(p.x, y, "This step runs in a new pane, where you can answer it. The setup goes on when it ends.", pal.FgDim, false)
		p.cmd(p.x+2, y+2, hostsetup.Command{Where: "here", Line: ps.Line})
		p.keys([][2]string{{"enter", "open a pane"}, {"esc", "stop"}}, "", "Open a pane")
	}
}

// doneView is the end of the run: what it came to, over the steps.
func (p *msPainter) doneView() {
	s, pal := p.s, p.pal
	head := ""
	if b := s.bulk; s.res == nil {
		sm := b.Summary
		head = msCount(sm.Ready, "machine is ready.", "machines are ready.")
		if sm.NeedYou+sm.Failed > 0 {
			head += " " + strconv.Itoa(sm.NeedYou+sm.Failed) + " did not finish."
		}
		p.keys([][2]string{{"esc", "done"}}, "", "Done")
	} else if r := s.res; r != nil {
		head = r.Name + " is ready. Its agents reach your Inbox."
		p.keys([][2]string{{"enter", "open a session"}, {"esc", "close"}}, "Close", "Open a session on "+r.Name)
	}
	p.put(p.put(p.x, p.y, pal.Success, false, hostsetup.Mark(hostsetup.StateOK)+"  "), p.y, pal.Fg, true, head)
	p.y += 2
	p.lines()
}

// msCount is n with the words for one or for more.
func msCount(n int, one, more string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + more
}

// cmpOr is the first of a and b that is not empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
