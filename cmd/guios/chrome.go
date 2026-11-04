package main

import (
	_ "embed"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The chrome is direction A, Native (chrome-mocks/TOKENS.md): a sidebar the
// full height of the window, a band over the stage on the same ground with
// no rule, and each pane a card on the shell with a 30 px header.

const (
	bandH     = 40
	sidebarW  = 248
	headerH   = 30
	paneInset = 6                       // a card's edge inside its slot
	panePad   = paneInset + 12          // the text's room from a slot's side
	paneTop   = paneInset + headerH + 2 // the text's room from a slot's top
	cardR     = 10
)

var (
	//go:embed icons/inbox.svg
	svgInbox []byte
	//go:embed icons/columns-2.svg
	svgSplitRight []byte
	//go:embed icons/rows-2.svg
	svgSplitDown []byte
	//go:embed icons/maximize-2.svg
	svgZoom []byte
	//go:embed icons/x.svg
	svgClose []byte
	//go:embed icons/win-minus.svg
	svgWinMin []byte
	//go:embed icons/win-square.svg
	svgWinMax []byte
	//go:embed icons/win-close.svg
	svgWinClose []byte
	//go:embed icons/chevron-down.svg
	svgChevronDown []byte
	//go:embed icons/state-needs.svg
	svgNeeds []byte
	//go:embed icons/state-ring.svg
	svgRing []byte
	//go:embed icons/state-arc.svg
	svgArc []byte
	//go:embed icons/state-dot.svg
	svgDot []byte
	//go:embed icons/mark-check.svg
	svgCheck []byte
	//go:embed icons/mark-x.svg
	svgX []byte

	icInbox, icSplitRight, icSplitDown, icZoom, icClose = ui.MustParseSVG(svgInbox), ui.MustParseSVG(svgSplitRight), ui.MustParseSVG(svgSplitDown), ui.MustParseSVG(svgZoom), ui.MustParseSVG(svgClose)
	icWinMin, icWinMax, icWinClose, icChevron           = ui.MustParseSVG(svgWinMin), ui.MustParseSVG(svgWinMax), ui.MustParseSVG(svgWinClose), ui.MustParseSVG(svgChevronDown)
	icNeeds, icRing, icArc, icDot, icCheck, icX         = ui.MustParseSVG(svgNeeds), ui.MustParseSVG(svgRing), ui.MustParseSVG(svgArc), ui.MustParseSVG(svgDot), ui.MustParseSVG(svgCheck), ui.MustParseSVG(svgX)
)

// geo is the stage's grid in device pixels. A pane's slot is its cells plus
// the gap after it: half a gap column on each side and the gap row above,
// which holds the header. The model lays the panes out in this grid.
type geo struct {
	scale      float32
	gx, gy     float32
	cw, ch     float32
	half       float32
	cols, rows int
}

// stageGeo is the grid of a window w by h DIPs at a scale.
func stageGeo(w, h, scale float32, m metrics) geo {
	sx, sy := float32(sidebarW), float32(bandH)
	sw := float32(math.Round(float64((w - sx) * scale)))
	sh := float32(math.Round(float64((h - sy) * scale)))
	cw, ch := float32(m.cellW), float32(m.cellH)
	cols := max(int(sw/cw)-1, 1)
	rows := max(int(sh/ch)-1, 1)
	half := float32(math.Floor(float64(cw / 2)))
	gx := float32(math.Round(float64(sx*scale))) + float32(math.Floor(float64((sw-float32(cols+1)*cw)/2))) + half
	gy := float32(math.Round(float64(sy*scale))) + float32(math.Floor(float64((sh-float32(rows+1)*ch)/2))) + ch
	return geo{scale: scale, gx: gx, gy: gy, cw: cw, ch: ch, half: half, cols: cols, rows: rows}
}

// paneRects are one pane's boxes, in DIPs.
type paneRects struct {
	card, header, content ui.Rect
}

func (g geo) rects(p paneSnap, full bool) paneRects {
	x, y, c, r := p.X+p.Border, p.Y+p.Border, max(p.W-2*p.Border, 1), max(p.H-2*p.Border, 1)
	if full {
		x, y, c, r = 0, 0, g.cols, g.rows
	}
	s := g.scale
	sl := g.gx + float32(x)*g.cw - g.half
	st := g.gy + float32(y-1)*g.ch
	sr := sl + float32(c+1)*g.cw
	sb := st + float32(r+1)*g.ch
	d := func(v float32) float32 { return float32(math.Round(float64(v * s))) }
	in := d(paneInset)
	il, it := d(panePad), d(paneTop)
	// The room a slot leaves under its whole cells goes half on each side.
	bal := float32(math.Floor(float64(math.Mod(float64(g.ch-float32(math.Mod(float64(it+il), float64(g.ch)))), float64(g.ch)) / 2)))
	balx := float32(math.Floor(float64(math.Mod(float64(g.cw-float32(math.Mod(float64(2*il), float64(g.cw)))), float64(g.cw)) / 2)))
	b := func(x0, y0, x1, y1 float32) ui.Rect {
		return ui.Rect{X: x0 / s, Y: y0 / s, W: max(x1-x0, 0) / s, H: max(y1-y0, 0) / s}
	}
	return paneRects{
		card:    b(sl+in, st+in, sr-in, sb-in),
		header:  b(sl+in, st+in, sr-in, st+in+d(headerH)),
		content: b(sl+il+balx, st+it+bal, sr-il, sb-il),
	}
}

func text(c *ui.Context, s string, size float32, weight int, col ui.Color) ui.Element {
	return ui.Text(c, s).FontSize(size).FontWeight(weight).TextColor(col).SingleLine()
}

// view builds the window for one frame.
func (a *guiApp) view(c *ui.Context) {
	r := &a.roles
	t := ui.DarkTheme()
	if r.light {
		t = ui.LightTheme()
	}
	t.Font, t.FontSize = uiFamily, 13
	t.Background, t.Text, t.TextMuted, t.Accent, t.Border = r.shell, r.text, r.text3, r.accent, r.hairline
	c.SetTheme(t)
	sv := c.Services()
	a.services = &sv

	snap := a.cm.m.Snap()
	w, h := c.Size()
	scale := a.shaper.m.scale
	if scale == 0 {
		scale = 1
	}
	m := a.shaper.at(scale)
	g := stageGeo(w, h, scale, m)
	a.geo = g
	if grid := [4]int{g.cols, g.rows, m.cellW, m.cellH}; grid != a.grid {
		a.grid = grid
		a.cm.resize(g.cols, g.rows, m.cellW, m.cellH, g.insets(m))
	}

	root := ui.Row(c).Fill().Background(r.shell).Focusable().FocusRing(false).AutoFocus().
		HandleInput(a.input).TextCaret(a.caret)
	root.Children(func() {
		a.sidebar(c, snap)
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			a.band(c, snap)
			a.stage(c, snap)
		})
	})
	a.dropUnused()
}

// sidebar lists the agents that need the person, then every session with
// its panes and their agent state.
func (a *guiApp) sidebar(c *ui.Context, snap *snapshot) {
	r := &a.roles
	top := float32(bandH)
	ui.Column(c).Width(sidebarW).FillHeight().Background(r.shell).
		BorderWidth(0, 1, 0, 0).BorderColor(r.hairline).Children(func() {
		// The traffic lights sit in this strip on macOS.
		ui.Row(c).Height(top).FillWidth().DragWindow().PaddingX(16)
		ui.Column(c).Grow(1).ClipY().PaddingX(8).Gap(2).Children(func() {
			if snap == nil {
				return
			}
			var needs []sessiontree.Node
			var needSession []string
			for _, s := range snap.Tree {
				for _, wn := range s.Children {
					if wn.AgentState == "needs_input" {
						needs = append(needs, wn)
						needSession = append(needSession, s.ID)
					}
				}
			}
			if len(needs) > 0 {
				a.sectionLabel(c, "Needs you", len(needs))
				for i, wn := range needs {
					a.paneRow(c, snap, needSession[i], wn, true)
				}
				ui.Box(c).Height(10)
			}
			for _, s := range snap.Tree {
				if s.Kind != sessiontree.KindSession {
					continue
				}
				a.sessionRow(c, snap, s)
				if s.IsCurrent || s.Title == snap.Session || s.ID == snap.Session {
					for _, wn := range s.Children {
						a.paneRow(c, snap, s.ID, wn, false)
					}
				}
			}
		})
	})
}

func (a *guiApp) sectionLabel(c *ui.Context, label string, n int) {
	r := &a.roles
	ui.Row(c).Height(28).PaddingX(8).Gap(8).AlignItems(ui.Center).Children(func() {
		text(c, label, 12, 600, r.text2)
		if n > 0 {
			ui.Box(c).PaddingX(6).Height(16).Radius(8).Background(r.control).Center().Children(func() {
				text(c, strconv.Itoa(n), 11, 600, r.text2).FontFeatures("tnum")
			})
		}
	})
}

// sessionName is the name a session row shows.
func sessionName(s sessiontree.Node) string {
	if s.Title != "" {
		return s.Title
	}
	return s.ID
}

func (a *guiApp) sessionRow(c *ui.Context, snap *snapshot, s sessiontree.Node) {
	r := &a.roles
	name := sessionName(s)
	current := s.IsCurrent
	row := ui.Row(c.Key("s:" + s.ID)).Height(28).PaddingX(8).Gap(8).Radius(8).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(r.hover)
	}
	row.Children(func() {
		ui.Icon(c, icChevron).FontSize(12).TextColor(r.text3)
		col := r.text2
		if current {
			col = r.text
		}
		text(c, name, 13, 600, col).Grow(1).Shrink(1).MinWidth(0).Ellipsis("…")
		if n := s.WindowCount; n > 0 && !current {
			text(c, strconv.Itoa(n), 12, 400, r.text3).FontFeatures("tnum")
		}
	})
	if row.Clicked() && !current {
		target := s.ID
		a.cm.do(func(m *model) tea.Cmd {
			_ = m.os.SwitchToSession(target)
			return nil
		})
	}
}

// stateIcon is the mark of an agent state.
func (a *guiApp) stateIcon(c *ui.Context, state string, size float32) {
	r := &a.roles
	var ic *ui.SVG
	switch state {
	case "needs_input":
		ic = icNeeds
	case "working":
		ic = icArc
	case "done":
		ic = icCheck
	case "errored":
		ic = icX
	case "idle":
		ic = icRing
	default:
		ic = icDot
	}
	ui.Icon(c, ic).Size(size, size).TextColor(r.stateColor(state))
}

// paneRow is one pane under its session: its mark, its title and, for an
// agent, what it last said.
func (a *guiApp) paneRow(c *ui.Context, snap *snapshot, sessionID string, wn sessiontree.Node, inNeeds bool) {
	r := &a.roles
	key := "w:" + sessionID + ":" + wn.ID
	if inNeeds {
		key = "n:" + key
	}
	focused := !inNeeds && snap.Focused == wn.ID
	twoLine := wn.AgentState != "" && (wn.Message != "" || wn.Harness != "")
	h := float32(28)
	if twoLine {
		h = 44
	}
	row := ui.Row(c.Key(key)).Height(h).PaddingX(8).Gap(10).Radius(8).AlignItems(ui.Center)
	switch {
	case focused:
		row.Background(r.selected)
	case row.Hovered():
		row.Background(r.hover)
	}
	row.Children(func() {
		ui.Box(c).Size(16, 16).Center().Children(func() { a.stateIcon(c, wn.AgentState, 14) })
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			title := wn.Title
			if title == "" {
				title = wn.ID
			}
			text(c, title, 13, 500, r.text).FillWidth().Ellipsis("…")
			if twoLine {
				detail := wn.Harness
				if wn.Message != "" {
					if detail != "" {
						detail += " · "
					}
					detail += wn.Message
				}
				text(c, detail, 12, 400, r.text3).FillWidth().Ellipsis("…")
			}
		})
	})
	if row.Clicked() {
		win, sess := wn.ID, sessionID
		a.cm.do(func(m *model) tea.Cmd {
			if m.os.SessionName != sess {
				if err := m.os.SwitchToSession(sess); err != nil {
					return nil
				}
			}
			_ = m.os.FocusPane(win)
			return nil
		})
	}
}

// band is the strip over the stage: the session, the workspaces, the Inbox
// count, and on Linux the window buttons.
func (a *guiApp) band(c *ui.Context, snap *snapshot) {
	r := &a.roles
	ui.Row(c).Height(bandH).FillWidth().PaddingX(12).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		if snap == nil {
			return
		}
		text(c, snap.Session, 13, 600, r.text)
		a.workspaces(c, snap)
		ui.Spacer(c)
		if snap.Needs > 0 {
			ui.Row(c).Height(28).PaddingX(8).Gap(6).Radius(8).Background(r.control).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icInbox).Size(14, 14).TextColor(r.text2)
				text(c, strconv.Itoa(snap.Needs), 12, 600, r.need).FontFeatures("tnum")
			})
		}
		if !isMac && a.win != nil {
			a.windowButtons(c)
		}
	})
}

func (a *guiApp) workspaces(c *ui.Context, snap *snapshot) {
	r := &a.roles
	last := snap.Workspace
	for ws := range snap.Occupied {
		last = max(last, ws)
	}
	last = max(last, 1)
	ui.Row(c).Height(28).Padding(2).Gap(2).Radius(8).Background(r.control).AlignItems(ui.Center).Children(func() {
		for ws := 1; ws <= last; ws++ {
			seg := ui.Row(c.Key(ws)).Height(24).PaddingX(10).Gap(6).Radius(6).AlignItems(ui.Center)
			active := ws == snap.Workspace
			switch {
			case active:
				seg.Background(r.active)
			case seg.Hovered():
				seg.Background(r.hover)
			}
			seg.Children(func() {
				text(c, strconv.Itoa(ws), 11, 500, r.text3).FontFeatures("tnum")
				name := snap.WSNames[ws]
				col := r.text2
				if active {
					col = r.text
				}
				if name != "" {
					text(c, name, 13, 500, col)
				} else if !snap.Occupied[ws] && !active {
					text(c, "empty", 12, 400, r.text3)
				}
			})
			if seg.Clicked() && !active {
				n := ws
				a.cm.do(func(m *model) tea.Cmd { _ = m.os.SwitchWorkspace(n); return nil })
			}
		}
	})
}

func (a *guiApp) windowButtons(c *ui.Context) {
	r := &a.roles
	btn := func(key string, ic *ui.SVG, fn func()) {
		b := ui.Box(c.Key(key)).Size(28, 28).Radius(6).Center()
		if b.Hovered() {
			b.Background(r.hover)
		}
		b.Children(func() { ui.Icon(c, ic).Size(14, 14).TextColor(r.text2) })
		if b.Clicked() {
			fn()
		}
	}
	ui.Row(c).Gap(4).Children(func() {
		btn("min", icWinMin, a.win.Minimize)
		btn("max", icWinMax, func() {
			if a.win.IsMaximized() {
				a.win.Unmaximize()
			} else {
				a.win.Maximize()
			}
		})
		btn("close", icWinClose, a.win.Close)
	})
}

// stage draws the panes as cards where the model laid them out.
func (a *guiApp) stage(c *ui.Context, snap *snapshot) {
	r := &a.roles
	ui.Box(c).Grow(1).FillWidth().Children(func() {
		if snap == nil {
			return
		}
		for _, p := range snap.Panes {
			if snap.Zoomed != "" && p.ID != snap.Zoomed {
				continue
			}
			a.card(c, snap, p, r)
		}
	})
}

func (a *guiApp) card(c *ui.Context, snap *snapshot, p paneSnap, r *roles) {
	rects := a.geo.rects(p, p.ID == snap.Zoomed)
	focused := p.ID == snap.Focused
	needs := p.Agent == "needs_input"
	// The stage box starts under the band and right of the sidebar.
	ox, oy := float32(sidebarW), float32(bandH)
	card := ui.Column(c.Key(p.ID)).Absolute().Left(rects.card.X-ox).Top(rects.card.Y-oy).
		Size(rects.card.W, rects.card.H).Radius(cardR).Background(r.stage)
	switch {
	case needs:
		card.Border(1, r.needEdge).Shadow(0, 0, 0, 4, r.needHalo)
	case focused:
		card.Border(1, r.lineFocus).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, .22)).Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, .22))
	default:
		card.Border(1, r.lineCard)
	}
	hovered := card.Hovered()
	card.Children(func() {
		ui.Row(c).Height(headerH).FillWidth().Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(14, 14).Center().Children(func() {
				st := p.Agent
				if st == "" {
					st = "shell"
				}
				a.stateIcon(c, st, 14)
			})
			text(c, p.Title, 13, 500, r.text).Ellipsis("…").Shrink(1)
			detail := p.Harness
			if p.AgentMsg != "" {
				if detail != "" {
					detail += " · "
				}
				detail += p.AgentMsg
			} else if p.Cwd != "" {
				detail = tildePath(p.Cwd)
			}
			if detail != "" {
				text(c, detail, 12, 400, r.text3).Ellipsis("…").Shrink(1).Grow(1)
			} else {
				ui.Spacer(c)
			}
			// The actions keep their room and show on hover, so nothing
			// moves (TOKENS.md 3, header.actions).
			actions := ui.Row(c).Gap(2)
			if !hovered {
				actions.Opacity(0)
			}
			actions.Children(func() {
				a.headerAction(c, p.ID+":split", icSplitRight, "Split right", "split_vertical", p.ID)
				a.headerAction(c, p.ID+":zoom", icZoom, "Zoom", "toggle_zoom", p.ID)
				a.headerAction(c, p.ID+":close", icClose, "Close", "close_window", p.ID)
			})
		})
		body := ui.Box(c).Grow(1).FillWidth().Draw(func(pt *ui.Painter, _ ui.Rect) {
			a.paintBody(pt, p, rects, focused)
		})
		if body.Clicked() && !focused {
			id := p.ID
			a.cm.do(func(m *model) tea.Cmd { _ = m.os.FocusPane(id); return nil })
		}
	})
}

func (a *guiApp) headerAction(c *ui.Context, key string, ic *ui.SVG, label, action, pane string) {
	r := &a.roles
	b := ui.Box(c.Key(key)).Size(24, 24).Radius(6).Center().Tooltip(label)
	if b.Hovered() {
		b.Background(r.hover)
	}
	b.Children(func() { ui.Icon(c, ic).Size(14, 14).TextColor(r.text2) })
	if b.Clicked() {
		a.cm.do(func(m *model) tea.Cmd {
			_ = m.os.FocusPane(pane)
			cmd, _ := m.os.RunActionCmd(action)
			return cmd
		})
	}
}

// paintBody draws a pane's text, then the veil over an unfocused one.
func (a *guiApp) paintBody(pt *ui.Painter, p paneSnap, rects paneRects, focused bool) {
	if s := pt.Scale(); s != a.shaper.m.scale {
		a.shaper.at(s)
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	preedit := ""
	if focused {
		preedit = a.preedit
	}
	a.frames.Add(1)
	cur := a.paintPane(pt, rects.content, p, focused, preedit)
	if focused {
		a.caret = cur
		return
	}
	// The veil over an unfocused body: the stage colour, so the ground
	// stays and only the text recedes.
	body := rects.card
	body.Y = rects.header.Y + rects.header.H
	body.H = rects.card.Y + rects.card.H - body.Y
	pt.Clip(rects.card, cardR, func() { pt.Fill(body, a.roles.veil, 0) })
}

// input takes keys and text for the focused pane.
func (a *guiApp) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputKeyDown:
		if a.shortcut(ev) {
			return true
		}
		k, ok := keyMsg(ev)
		if !ok {
			return false
		}
		a.sendKey(k)
		return true
	case ui.InputText:
		a.preedit = ""
		for _, k := range textKeys(ev.Text) {
			a.sendKey(k)
		}
		return true
	case ui.InputCompose:
		a.preedit = ev.Text
		if a.win != nil {
			a.win.Invalidate()
		}
		return true
	case ui.InputCommand:
		if ev.Text == "paste" {
			a.paste()
			return true
		}
	}
	return false
}

func (a *guiApp) sendKey(k tea.KeyPressMsg) {
	keySent.CompareAndSwap(0, nowNano())
	a.cm.key(k)
}

// shortcut handles the keys guios keeps: paste.
func (a *guiApp) shortcut(ev ui.InputEvent) bool {
	paste := ev.Key == ui.KeyV && (ev.Mods == ui.Ctrl|ui.Shift || isMac && ev.Mods == ui.Super)
	if paste {
		a.paste()
		return true
	}
	return false
}

// paste sends the clipboard to the focused pane through the model, which
// brackets it when the program asked.
func (a *guiApp) paste() {
	if a.services == nil {
		return
	}
	s := a.services.ReadClipboard()
	if s == "" {
		return
	}
	a.cm.program.Send(tea.PasteMsg{Content: s})
}

// tildePath shortens a path under the home folder.
func tildePath(p string) string {
	if home := homeDir; home != "" && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	if strings.Count(p, string(filepath.Separator)) > 4 {
		parts := strings.Split(p, string(filepath.Separator))
		p = filepath.Join(parts[0], "…", filepath.Join(parts[len(parts)-2:]...))
	}
	return p
}
