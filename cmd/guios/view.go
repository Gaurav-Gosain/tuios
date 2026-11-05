package main

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

const (
	// barMid is the line the title bar's contents center on: the traffic
	// lights, the sidebar's buttons, the panes' headers. topBar is the
	// sidebar's title bar, centered on it, and headerH a header of a card,
	// which starts cardGap down, centered on it too.
	barMid  = 30
	topBar  = 2 * barMid
	headerH = 2 * (barMid - cardGap)
	cardGap = 8 // around the card
	rowH    = hRow
	rowH2   = hRow2 // a detailed row, of two lines
	// rowInset is where a sidebar row's text starts: a session's name and
	// its group's.
	rowInset = 12
)

// chrome is where the sidebar's animation puts things, in whole pixels so
// that edges stay crisp. Each is rounded once, from one progress quantized
// to the sidebar's pixels, so that they move together: rounded apart,
// they would step at different frames, and a title, their sum, would
// shake by a pixel as the spring settles.
type chrome struct {
	shown     float32 // the sidebar's progress, 0 hidden to 1 shown
	sideW     float32 // the sidebar's width as shown
	mainPad   float32 // the left padding of the cards' column
	headerPad float32 // the left padding of the header at the left edge
}

// buttonsLeft places the sidebar's buttons that the header holds while
// the sidebar hides, in the header's coordinates, so that they stay where
// they are in the window, after the traffic lights, as the card slides.
func (ch chrome) buttonsLeft(bar ui.TitleBar) float32 {
	return lead(bar) - (ch.sideW + ch.mainPad)
}

// lead is where the sidebar's buttons start in the window, with the sidebar
// shown or not: after the traffic lights on macOS; elsewhere inside the
// card's edge, which they come over as the sidebar hides.
func lead(bar ui.TitleBar) float32 {
	if ownControls {
		return bar.Left + 2*cardGap
	}
	return bar.Left + cardGap
}

// sideSlide is the left sidebar's slide: 0 hidden, 1 shown. guios eases
// it itself, not with AnimateTo, so that it moves in the frame that takes
// the key (AnimateTo's first frame shows the value it starts from), and
// stops in the frame whose width rounds to the target.
type sideSlide struct {
	from, cur, to float32
	start         time.Time
	set           bool
}

// sideLead is how far the slide is already on its way in the frame that
// starts it: half a frame, as the key came between frames.
const sideLead = 8 * time.Millisecond

// step returns how far the sidebar shows in this frame, on its way to
// target, for a sidebar w DIPs wide.
func (s *sideSlide) step(c *ui.Context, target, w float32) float32 {
	now := c.Now()
	switch {
	case !s.set:
		s.cur, s.to, s.set = target, target, true
	case target != s.to:
		s.from, s.to, s.start = s.cur, target, now.Add(-sideLead)
	}
	if s.cur == s.to {
		return s.cur
	}
	k := float32(now.Sub(s.start)) / float32(sidebarMotion.Duration)
	if k >= 1 || c.Instant() || c.Preferences().ReduceMotion {
		s.cur = s.to
		return s.cur
	}
	s.cur = s.from + (s.to-s.from)*sidebarMotion.Ease(max(k, 0))
	if d := s.to - s.cur; d*w < 0.5 && d*w > -0.5 {
		s.cur = s.to
		return s.cur
	}
	c.AnimationFrame()
	return s.cur
}

// sidebarMotion slides the sidebars in and out: motionLayout's length,
// on a curve that settles without a long tail.
var sidebarMotion = ui.Animation{Duration: motionLayout.Duration, Ease: ui.CubicBezier(0.25, 1, 0.5, 1)}

func chromeFor(progress, width float32, bar ui.TitleBar) chrome {
	round := func(v float32) float32 { return float32(math.Round(float64(v))) }
	w := round(width * min(max(progress, 0), 1))
	s := w / width
	pad := round(cardGap * (1 - s))
	// Where the header's title starts in the window, rounded once: after
	// the traffic lights and the sidebar's buttons with the sidebar
	// hidden, 16 into the card with it shown.
	hidden, shown := lead(bar)+16+60, width+16
	x := round(hidden + (shown-hidden)*s)
	return chrome{shown: s, sideW: w, mainPad: pad, headerPad: x - w - pad}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func (a *App) view(c *ui.Context) {
	if a.dbg.on {
		t0 := time.Now()
		defer func() { a.debugFrame(time.Since(t0)) }()
	}
	if a.calm {
		// Calm mode: every frame is a still one.
		c.DisableAnimations()
	}
	agoDue = time.Time{}
	if auditFrame != nil {
		defer auditFrame(a, c)
	}
	a.syncUsage()
	defer func() {
		// A frame when the first "ago" label shown changes, and none while
		// no label would.
		if !agoDue.IsZero() {
			// One frame a minute at most, on the minute, for every label
			// due in it, as the TUI's minute tick: the agents view shows
			// many ages, each of which would cost a frame of its own.
			due := agoDue
			if t := due.Truncate(time.Minute); !t.Equal(due) {
				due = t.Add(time.Minute)
			}
			c.After(due.Sub(c.Now()))
		}
	}()
	a.looks()
	pal := paletteFor(c)
	c.SetTheme(borderless(c, pal))

	if a.pendingToast != "" {
		c.Toast(a.pendingToast)
		a.pendingToast = ""
	}
	a.shortcuts(c)
	a.keysShortcut(c)

	bar := c.TitleBar()
	row := ui.Row(c).Fill().AlignItems(ui.Stretch)
	backdrop(c, row)
	// A curve, not a spring: a spring's long tail crawls under a pixel a
	// frame, which whole pixels turn into hops and holds, a shake as it
	// settles. This one lands in 200 ms, moving in every frame.
	winW0, _ := c.Size()
	// In a narrow window the sidebar gets out of the panes' way, as the
	// right one does, until the person shows it there (toggleSidebar).
	a.leftCrowded = winW0 > 0 && winW0 < narrowWindow
	if !a.leftCrowded {
		a.leftForced = false
	}
	progress := a.sidebarSlide.step(c, b2f(a.sidebarShown()), a.prefs.SidebarWidth)
	a.chrome = chromeFor(progress, a.prefs.SidebarWidth, bar)
	shown := a.chrome.shown
	// The right sidebar, beside a session's panes, comes and goes as the
	// left one does, in whole pixels, as the user shows or hides it: it
	// shows at once as the app opens with it shown, without a session for
	// a moment, and goes at once with the panes.
	work := a.workSession()
	rightW := a.rightWidth()
	// In a window too narrow for it beside the cards, it gets out of their
	// way, coming back as the window widens, unless shown anyway.
	winW, _ := c.Size()
	leftW := float32(0)
	if a.sidebarShown() {
		leftW = a.prefs.SidebarWidth
	}
	a.side.crowded = winW > 0 && winW-leftW-rightW < minCards
	if !a.side.crowded {
		a.side.forced = false
	}
	right := float32(0)
	// The agents view takes the whole of the panes' place: the right
	// sidebar, which is about the session's panes, steps aside for it.
	p := row.AnimateTo("right", b2f(a.rightShown() && !a.ck.open), sidebarMotion)
	if work != nil {
		right = float32(math.Round(float64(rightW * min(max(p, 0), 1))))
	}
	// The panes' terminals keep their grids while a sidebar slides, and
	// take the new size once, as it lands.
	if moving := func(v float32) bool { return v > 0 && v < 1 }; moving(progress) || moving(p) {
		holdLeases(80 * time.Millisecond)
	}
	a.side.barW = right
	// The gaps around the cards move the window too; the right one closes
	// as the sidebar comes beside the cards.
	rightPad := float32(math.Round(float64(cardGap * (1 - right/rightW))))
	a.side.edge = rightPad
	// The cards' row's width in this frame, which the viewer's comes of:
	// the last frame's would lag a frame as the window resizes, and shake.
	a.side.rowW = winW - a.chrome.sideW - right - a.chrome.mainPad - rightPad
	a.side.cf = nil
	a.side.files.showed, a.side.files.shown = a.side.files.shown, false
	row.Children(func() {
		w := a.chrome.sideW
		side := ui.Row(c).Width(w).Shrink(0).AlignItems(ui.Stretch).ClipX()
		if h := a.local(); h.conn == connReady && h.cl != nil && a.sidebarShown() && !a.pal.open && !a.picker.open && (a.confirm == nil || !a.confirm.open) {
			if files := side.DroppedFiles(); len(files) > 0 {
				go a.openProjectDirs(h, h.cl, slices.Clone(files), true)
			}
			if side.FileDragOver() {
				side.Background(pal.accent.Alpha(0.08)).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Stroke(ui.Rect{X: r.X + 4, Y: r.Y + 4, W: r.W - 8, H: r.H - 8}, pal.accent.Alpha(0.55), 8, 1)
				})
			}
		}
		side.Children(func() {
			ui.Column(c).Width(a.prefs.SidebarWidth).Shrink(0).Margin(0, 0, 0, w-a.prefs.SidebarWidth).
				Opacity(0.4 + 0.6*shown).Children(func() { a.sidebar(c, pal, bar) })
		})
		main := ui.Column(c).Grow(1).MinWidth(0).Padding(cardGap, rightPad, cardGap, a.chrome.mainPad).DragWindow()
		main.Children(func() {
			if shown > 0.01 {
				a.resizeHandle(c, pal)
			}
			a.card(c, pal, bar, shown)
		})
		if right > 0 && work != nil {
			// Its content stays at its width as it slides in from the
			// window's edge.
			ui.Row(c.Key("right")).Width(right).Shrink(0).AlignItems(ui.Stretch).ClipX().Children(func() {
				ui.Column(c).Width(rightW).Shrink(0).AlignItems(ui.Stretch).Opacity(0.4 + 0.6*right/rightW).
					Children(func() { a.rightSidebar(c, pal, work) })
			})
			// Over the cards and the sidebar both, which would take the
			// pointer from it on either side of their edge.
			a.rightHandle(c, right)
		}
	})
	a.keepKeyboard(row)
	a.mainControls(c, pal, rightPad)
	// A page staged for a screenshot takes the window's size, under its
	// content, which hides it: at no opacity, it paints no hole there.
	for id, e := range a.webs {
		if e.staged && e.view != nil {
			ui.Box(c.Key("staged-" + id)).Absolute().Left(0).Top(0).Right(0).Bottom(0).AlignItems(ui.Stretch).Opacity(0).Children(func() {
				ui.WebView(c.Key("web"), e.view).Grow(1)
			})
		}
	}
	keepOverlay(c)
	toastsAt(c, a.toastBox())
	a.spawnView(c, pal)
	a.compareView(c, pal)
	a.paletteView(c, pal)
	a.warmPalette(c)
	a.helpView(c, pal)
	a.transfersView(c, pal)
	a.sidebarKeysView(c)
	a.debugView(c, pal)
	a.scrollbackView(c, pal)
	a.promptView(c, pal)
	a.pickerView(c, pal)
	a.confirmView(c, pal)
	a.worktreeView(c, pal)
	a.keysCard(c, pal)
	// A preview that showed no more fades in anew next time.
	if !a.previewShown {
		a.previewKey = ""
	}
	a.previewShown = false
	// An image previewed no more closes, and lets go of its bitmap.
	if !a.peek.shown {
		a.closePeek()
		a.peek = peek{}
	}
	a.peek.shown = false
	a.backupEdits(c)
	if later := a.later; len(later) > 0 {
		a.later = nil
		for _, fn := range later {
			fn()
		}
		c.Invalidate()
	}
}

// resizeHandle is the sidebar's edge, which a drag moves.
func (a *App) resizeHandle(c *ui.Context, pal *palette) {
	// A drag leaves the keyboard in the pane the user is in.
	h := ui.Box(c.Key("resize")).Absolute().Left(-4).Top(0).Bottom(0).Width(9).Cursor(ui.CursorResizeColumn).KeepFocus()
	if dx, _, ok := h.Dragged(); ok {
		if a.sideDrag == 0 {
			a.sideDrag = a.prefs.SidebarWidth
		}
		a.sideDrag += dx
		a.prefs.SidebarWidth = min(max(a.sideDrag, minSidebar), maxSidebar)
	} else if a.sideDrag != 0 && !h.Pressed() {
		a.sideDrag = 0
		a.savePrefs()
	}
	if h.DoubleClicked() {
		a.prefs.SidebarWidth = defaultSidebar
		a.savePrefs()
	}
}

// iconButton is a quiet square button with an icon, as in a toolbar.
func iconButton(c *ui.Context, key string, svg *ui.SVG, label string, pal *palette, fn func()) {
	b := ui.ButtonBase(c.Key(key)).Size(hControl, 26).Radius(rRow).Center().TextColor(pal.textMuted).Label(label).Tooltip(label)
	if b.Pressed() {
		b.Background(pal.rowHover.Alpha(0.12))
	} else if b.Hovered() {
		b.Background(pal.rowHover)
		b.TextColor(pal.text)
	}
	b.Children(func() { ui.Icon(c, svg).Size(16, 16) })
	if b.Clicked() {
		fn()
	}
}

// tipsAt are where pointerTooltip's tips show, by key: where the pointer
// rested as each came, which they keep while they show.
var tipsAt = map[string]ui.Point{}

// pointerTooltip shows s as el's tooltip, as Element.Tooltip does, but
// where macOS shows its tooltips: below the pointer where it came to rest,
// clear of the arrow, rather than centered above el, which a long row
// puts far from where the user looks.
func pointerTooltip(c *ui.Context, el ui.Element, key, s string) {
	el.Description(s)
	const below = 18 // the arrow's height, from its tip
	px, py, over := el.PointerPosition()
	at, showing := tipsAt[key]
	if !showing {
		b := el.Bounds()
		at = ui.Point{X: b.X + px, Y: b.Y + py + below}
	}
	var spot ui.Element
	if over {
		ui.Overlay(c, func() {
			spot = ui.Box(c.Key(key+"-at")).Absolute().Left(at.X).Top(at.Y).Size(0, 0).PassThrough()
		})
	}
	shown := false
	ui.TooltipBase(c, el, func(tip ui.Element) {
		shown = true
		th := c.Theme()
		fill, text := th.Inverse, th.InverseText
		if fill == ui.Transparent {
			fill = th.Text
		}
		if text == ui.Transparent {
			text = th.Background
		}
		tip.MaxWidth(th.Space(80)).Padding(th.Space(1.25), th.Space(2)).Radius(th.Space(1.25)).
			Background(fill).TextColor(text).FontSize(th.FontSize-1).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.2))
		tip.Children(func() { ui.Text(c, s) })
		if over {
			tip.AttachTo(spot, ui.AnchorTopLeft, ui.AnchorTopLeft)
		}
	})
	if shown {
		tipsAt[key] = at
	} else {
		delete(tipsAt, key)
	}
}

// renameInput is a name edited in place, as a field: the control's fill,
// the text in the text colour, and a 2 px accent ring.
func renameInput(in ui.Element, pal *palette) ui.Element {
	return in.Height(hControlSm).Padding(0, 6).Radius(rRow).Background(pal.control).TextColor(pal.text).Border(2, pal.accent).FocusRing(false)
}

// rowTooltip is a list row's tooltip: beside the row, to its right, so
// that it covers neither the row's own text nor the rows around it
// (QA-062). Each line is one line: a path cut at its start, so that its
// last folders stay (DESIGN.md section 7, rule 4).
func rowTooltip(c *ui.Context, el ui.Element, key, s string) {
	el.Description(s)
	ui.TooltipBase(c, el, func(tip ui.Element) {
		th := c.Theme()
		fill, text := th.Inverse, th.InverseText
		if fill == ui.Transparent {
			fill = th.Text
		}
		if text == ui.Transparent {
			text = th.Background
		}
		size := th.FontSize - 1
		tip.Padding(th.Space(1.25), th.Space(2)).Radius(th.Space(1.25)).Margin(0, 0, 0, sp2).
			Background(fill).TextColor(text).FontSize(size).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.2)).
			AttachTo(el, ui.AnchorRight, ui.AnchorLeft)
		tip.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				for _, l := range strings.Split(s, "\n") {
					cut := l
					if strings.ContainsRune(l, '/') {
						cut = startCut(c, l, size, rowTipMax)
					}
					ui.Text(c, cut).FontSize(size).SingleLine().Ellipsis("…").MaxWidth(rowTipMax)
				}
			})
		})
	})
}

// rowTipMax is the widest line of a row's tooltip.
const rowTipMax = 360

// kbd is a key cap: 18 high, at least as wide, in a ring.
func kbd(c *ui.Context, pal *palette, keys string) {
	ui.Row(c).Height(hKbd).MinWidth(hKbd).Padding(0, 5).Radius(rChip).Background(pal.kbd).Border(1, pal.kbdBorder).
		AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, keys).FontSize(fsCaption).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine()
	})
}

// --- the sidebar ---

func (a *App) sidebar(c *ui.Context, pal *palette, bar ui.TitleBar) {
	// What needs the person, once a frame, for the agent rows.
	a.sbNeeds = nil
	if a.ck.loaded {
		a.sbNeeds = a.ck.needs()
	}
	ui.Row(c).Height(topBar).Shrink(0).AlignItems(ui.Center).Padding(0, 12, 0, lead(bar)).Gap(2).DragWindow().Children(func() {
		// After the traffic lights on macOS, at the right; elsewhere the
		// window controls are at the right, and the buttons at the left,
		// where they stay as the sidebar hides (chrome.buttonsLeft).
		if runtime.GOOS == "darwin" {
			ui.Spacer(c)
		}
		iconButton(c, "hide-sidebar", sidebarIcon, a.tip(locales.SidebarHide(), "sidebar.toggle"), pal, a.toggleSidebar)
		iconButton(c, "new-session", plusIcon, a.tip(locales.ActionNewSession(), "session.new"), pal, func() { a.newSession("") })
	})
	// The space between and below the rows moves the window, as the top
	// bar does.
	ui.Scroll(c.Key("sessions")).Grow(1).Padding(0, 10, 16, 10).DragWindow().Children(func() {
		a.agentsRow(c, pal)
		n := 0  // the rows shown, for ⌘1–9 (Ctrl+1–9 elsewhere)
		gi := 0 // the groups shown, the first of which sits higher
		// This machine's projects first, unlabeled; then each host's,
		// under its name.
		if local := a.local(); local.loaded {
			for _, g := range a.hostGroups(local) {
				a.groupView(c, pal, g, gi, &n)
				gi++
			}
			if len(a.st.Sessions) == 0 && local.conn == connReady && len(a.hosts) == 1 {
				ui.Text(c, locales.SidebarNoSessions()).FontSize(fsSmall).TextColor(pal.textFaint).Padding(8, 10).MaxLines(2)
			}
		}
		// Then the machines this machine's daemon links, each with its
		// sessions under it.
		a.machinesSection(c, pal, &gi, &n)
		if a.set.SessionHistory {
			a.endedSections(c, pal)
		}
	})
	// The copies between machines stay in view under the list.
	ui.Column(c.Key("xfers")).Padding(0, 10, 0, 10).Shrink(0).Children(func() { a.transfersSection(c, pal) })
	a.usagePanel(c, pal)
	a.sidebarFooter(c, pal)
}

func (a *App) groupView(c *ui.Context, pal *palette, g group, gi int, n *int) {
	id, key := g.project.ID, g.key()
	collapsed := a.prefs.Collapsed[key]
	// The agents that need the person, as the Agents badge counts them;
	// the sessions that wait until the daemon's lists are read.
	needs := 0
	for _, s := range g.sessions {
		switch {
		case a.ck.loaded && !g.host.remote():
			needs += a.sessionNeeds(s)
		case s.Status == proto.Waiting:
			needs++
		}
	}
	top := float32(18)
	if gi == 0 {
		top = 2
	}
	// Its name starts where its sessions' names do (sessionRow), past the
	// edge a row keeps in dark mode.
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	hdr := ui.Row(c.Key("project-"+key)).Height(hControl).Margin(top, 0, 2, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Radius(rRow)
	hovered := hdr.Hovered()
	renaming := id != "" && a.renamingProject == id
	if hdr.Clicked() && !renaming {
		a.toggleProject(key)
	}
	if !renaming {
		pointerTooltip(c, hdr, "tip-project-"+key, g.host.shortDir(g.project.Dir))
	}
	hdr.ContextMenu(func(m *ui.Menu) {
		if m.Item(locales.ActionNewSession()).Chosen() {
			a.newSessionOn(g.host, id)
		}
		label := locales.SidebarCollapse()
		if collapsed {
			label = locales.SidebarExpand()
		}
		if m.Item(label).Chosen() {
			a.toggleProject(key)
		}
		if m.Item(locales.SessionRename()).Disabled(id == "").Chosen() {
			a.startProjectRename(id)
		}
		m.Separator()
		// A remote group's menu has its host's, as the host has no header.
		if g.host.remote() {
			m.Submenu(g.host.name, func(m *ui.Menu) { a.hostMenu(m, g.host) })
		}
		// Close All Sessions ends the sessions the project holds, not the
		// project itself, asking first as closing the project does: what
		// runs in them ends with them.
		if m.Item(locales.SidebarCloseAllSessions()).Disabled(len(g.sessions) == 0).Chosen() {
			name, count := g.project.Name, len(g.sessions)
			a.confirm = &confirmation{
				open:    true,
				title:   locales.ConfirmCloseAllSessionsTitle(name),
				detail:  locales.ConfirmCloseAllSessionsDetail(count),
				action:  locales.SidebarCloseAllSessions(),
				destroy: true,
				run:     func() { a.closeAllSessions(g) },
			}
		}
		if m.Item(locales.SidebarCloseProjectMenu()).Disabled(id == "").Chosen() {
			name, count := g.project.Name, len(g.sessions)
			a.confirm = &confirmation{
				open:    true,
				title:   locales.ConfirmCloseTitle(name),
				detail:  locales.ConfirmCloseProjectDetail(count),
				action:  locales.SidebarCloseProject(),
				destroy: true,
				run:     func() { a.call(id, "project.close", proto.IDParams{ID: id}, nil) },
			}
		}
	})
	hdr.Children(func() {
		if renaming {
			in := renameInput(ui.TextInputBase(c.Key("rename"), &a.renameBuf).Grow(1).MinWidth(0).FontSize(fsSmall).FontWeight(wMedium).AutoFocus(), pal)
			if in.Shortcut(0, ui.KeyEscape) {
				a.renamingProject = ""
				a.focusTerminal()
			} else if in.Submitted() || (!in.Focused() && !in.Pressed() && in.Valid() && a.renameStarted(c)) {
				a.commitProjectRename()
			}
			return
		}
		oneLine(c, g.project.Name, fsSmall, wMedium).TextColor(pal.textMuted)
		if g.host.remote() {
			a.hostTag(c, pal, g.host)
		}
		ui.Textf(c, "%d", len(g.sessions)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").Shrink(0)
		chev := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
		turn := chev.AnimateTo("turn", 90*(1-b2f(collapsed)), ui.Snappy)
		chev.Rotate(turn).Opacity(chev.Animate("shown", b2f(hovered || collapsed), dHoverOut))
		ui.Spacer(c)
		// A machine's first group says how its link stands while it is
		// not up.
		if g.first && g.host.remote() {
			a.hostState(c, pal, g.host)
		}
		if needs > 0 {
			ui.Text(c, locales.SidebarNeedsYou(needs)).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.attnText).FontFeatures("tnum").SingleLine().Shrink(0)
		}
		if hovered {
			b := ui.ButtonBase(c.Key("add")).Size(20, 20).Radius(rRow).Center().Shrink(0).TextColor(pal.textMuted).Label(locales.NewSessionIn(g.project.Name)).Tooltip(locales.ActionNewSession())
			if b.Hovered() {
				b.Background(pal.rowHover).TextColor(pal.text)
			}
			b.Children(func() { ui.Icon(c, plusIcon).Size(14, 14) })
			if b.Clicked() {
				a.newSessionOn(g.host, id)
			}
		}
	})
	if collapsed {
		// The session shown stays in view.
		for _, s := range g.sessions {
			if s.ID == a.selected {
				a.sessionRow(c, pal, s, n)
			}
		}
		return
	}
	for _, s := range g.sessions {
		a.sessionRow(c, pal, s, n)
	}
}

func (a *App) sessionRow(c *ui.Context, pal *palette, s *proto.Session, n *int) {
	*n++
	index := *n
	selected := s.ID == a.selected
	detailed := a.set.SidebarRows != "compact"
	h := float32(rowH)
	if detailed {
		h = rowH2
	}
	row := ui.Row(c.Key(s.ID)).Height(h).Padding(0, 10, 0, rowInset).Gap(10).AlignItems(ui.Center).Radius(rRow).Margin(0, 0, 1, 0)
	hovered := row.Hovered()
	if pal.dark {
		// Room for the chosen row's edge in every row, so that choosing
		// one moves no text.
		row.Border(1, ui.Transparent)
	}
	bg := ui.Transparent
	if a.sb.on && a.sb.cursor == s.ID {
		// The sidebar's keyboard cursor (the prefix and e).
		row.Border(1.5, pal.accent)
	}
	switch {
	case selected:
		bg = pal.rowSelected
		chosen(row, pal)
	case hovered:
		bg = pal.rowHover
	}
	row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: dHoverOut}))
	// A session of a host the app lost shows as it was, faded, until the
	// host is back.
	if h := a.hostOf(s.ID); h.remote() && h.conn != connReady {
		row.Opacity(0.55)
	}
	if (row.Pressed() || row.Clicked()) && !selected {
		a.selectSession(s.ID)
	}
	if row.DoubleClicked() {
		a.startRename(s.ID)
	}
	tip := a.hostOf(s.ID).shortDir(s.WorkDir())
	if s.Process != "" {
		tip = s.Process + " · " + tip
	}
	if s.Message != "" {
		tip = s.Message + "\n" + tip
	}
	g := sessionGit(s)
	if g != nil {
		tip += "\n" + gitSummary(g)
	}
	if a.renaming != s.ID {
		// No tooltip over the name while it is edited.
		rowTooltip(c, row, "tip-"+s.ID, tip)
	}
	row.ContextMenu(func(m *ui.Menu) { a.sessionMenu(m, s) })
	row.Children(func() {
		if a.renaming == s.ID {
			// An edited name is a field: the focus ring, and the name in
			// the text colour, so that it does not read as a placeholder
			// (council round 1, visual 27).
			in := renameInput(ui.TextInputBase(c.Key("rename"), &a.renameBuf).Grow(1).MinWidth(0).FontSize(fsBody).AutoFocus().
				Placeholder(s.DisplayName(a.set.SessionName)), pal)
			if in.Submitted() || in.Shortcut(0, ui.KeyEscape) {
				if in.Shortcut(0, ui.KeyEscape) {
					a.renaming = ""
					a.focusTerminal()
				} else {
					a.commitRename()
				}
			} else if !in.Focused() && !in.Pressed() && in.Valid() && a.renameStarted(c) {
				a.commitRename()
			}
			return
		}
		name := func() {
			name := ui.Text(c, s.DisplayName(a.set.SessionName)).FontSize(fsBody).FontWeight(wMedium).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).TextColor(pal.text)
			if s.Unread || selected {
				name.FontWeight(wSemibold)
			}
			if s.Status == proto.Exited && !s.Unread {
				name.TextColor(pal.textMuted)
			}
		}
		when := func() {
			if index <= 9 && a.cmdHeld(c) {
				ui.Text(c, actions.Glyphs(fmt.Sprintf("CmdOrCtrl+%d", index))).FontSize(fsSmall).TextColor(pal.textMuted).FontFeatures("tnum").SingleLine().Shrink(0)
			} else {
				// The name gives way to these, never they to the name.
				ui.Text(c, ago(s.Since, c.Now())).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Shrink(0)
			}
		}
		if detailed {
			// The name, the time and the status over what the session is
			// at, on a line of the whole row's width, so that a long
			// branch reads as far as it can.
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
					name()
					ui.Spacer(c)
					when()
					statusIconP(c, pal, s.Status, s.Unread, s.Progress, 14)
				})
				line, msg := rowDetail(s, g, a.hostOf(s.ID).shortDir(s.WorkDir()))
				if f := s.FocusPane(); !msg && f != nil && f.Shell != "" && line == s.DisplayName(a.set.SessionName) {
					// A session named after its folder says its shell
					// under the name, not the folder again.
					line = f.Shell
				}
				// A detail line is text2, 4.5:1 on every ground (DESIGN.md
				// section 6): a message reads stronger by its weight.
				t := ui.Text(c, flat(line)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
				if msg {
					t.FontWeight(wMedium)
				}
			})
		} else {
			name()
			// A branch of its own, as an agent's in a worktree, by the name.
			if b := offBranch(g); b != "" {
				ui.Text(c, b).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(3).MinWidth(24).MaxWidth(120)
			}
			ui.Spacer(c)
			when()
			statusIconP(c, pal, s.Status, s.Unread, s.Progress, 14)
		}
	})
	a.workspaceBar(c, pal, s)
	a.agentRows(c, pal, s, a.sbNeeds)
}

// rowDetail is the second line of a session's detailed row: its program's
// status message, else its worktree's branch and changes, else its folder,
// dir. msg tells it is a message, which reads stronger than the others.
func rowDetail(s *proto.Session, g *proto.GitInfo, dir string) (line string, msg bool) {
	if s.Message != "" {
		// A notification's text can run over lines: the row has one.
		return strings.Join(strings.Fields(s.Message), " "), true
	}
	if g != nil {
		return gitSummary(g), false
	}
	return dir, false
}

// renameStarted keeps a rename from ending in the frame it began in,
// before its input took the focus.
func (a *App) renameStarted(c *ui.Context) bool {
	if a.renameFrames < 3 {
		a.renameFrames++
		c.Invalidate()
		return false
	}
	return true
}

func (a *App) sessionMenu(m *ui.Menu, s *proto.Session) {
	id := s.ID
	if a.menuShortcut(m.Item(locales.SessionRename()), "session.rename").Chosen() {
		a.startRename(id)
	}
	if s.Status == proto.Exited {
		if m.Item(locales.CommonRestart()).Chosen() {
			a.restartSession(id)
		}
	}
	if s.Unread {
		if m.Item(locales.SessionMarkRead()).Chosen() {
			a.call(id, "view", proto.ViewParams{ID: id}, func(error) { a.hostOf(id).viewSent = nil; a.reportView() })
		}
	}
	h := a.hostOf(id)
	// A pane whose program runs on a linked machine, in this session.
	if !h.remote() && len(a.hosts) > 1 {
		for _, o := range a.hosts[1:] {
			if m.Item(locales.NewPaneOn(o.name)).Disabled(o.conn != connReady).Chosen() {
				name := o.name
				a.defer_(func() { a.newPaneOn(a.session(id), name) })
			}
		}
	}
	m.Submenu(locales.SessionMoveToProject(), func(m *ui.Menu) {
		// A session moves among its host's projects alone.
		for _, p := range a.projectsOf(h) {
			if m.Item(p.Name).Checked(p.ID == s.Project).Disabled(p.ID == s.Project).Chosen() {
				pid := p.ID
				a.defer_(func() { a.moveSession(id, pid) })
			}
		}
	})
	m.Separator()
	if m.Item(locales.SessionCopyId()).Chosen() {
		a.copyText = id
	}
	if m.Item(locales.SessionCopyDirectory()).Disabled(s.WorkDir() == "").Chosen() {
		a.copyText = s.WorkDir()
	}
	if m.Item(locales.SessionRevealIn(fileManager())).Disabled(s.WorkDir() == "" || h.remote()).Chosen() {
		a.openLink(id, s.WorkDir())
	}
	m.Separator()
	if a.menuShortcut(m.Item(locales.ActionCloseSession()), "session.close").Chosen() {
		a.defer_(func() { a.closeSession(id, true) })
	}
}

// sidebarFooter is the sidebar's foot: the buttons for projects, the
// palette and Settings; then, at the right, what across the sessions
// wants a look, each a button going to the next session of it, and
// nothing while nothing does, or the connection to kerod while there is
// none.
func (a *App) sidebarFooter(c *ui.Context, pal *palette) {
	ui.Row(c).Height(36).Shrink(0).Padding(0, 12, 4, 12).Gap(2).AlignItems(ui.Center).DragWindow().Children(func() {
		iconButton(c, "open-project", folderIcon, a.tip(locales.ActionOpenProject(), "project.open"), pal, a.openProject)
		iconButton(c, "palette", searchIcon, a.tip(locales.ActionGoToSession(), "palette.open"), pal, a.openPalette)
		iconButton(c, "settings", gearIcon, a.tip(locales.SettingsTitle(), "settings.open"), pal, func() { a.openSettings("") })
		usageTip := locales.UsageShow()
		if a.usageOpen {
			usageTip = locales.UsageHide()
		}
		iconButton(c, "usage", usageIcon, usageTip, pal, a.toggleUsage)
		ui.Spacer(c)
		switch a.local().conn {
		case connReady:
			if a.daemonStale() {
				a.restartChip(c, pal)
			}
			waiting := func(s *proto.Session) bool { return s.Status == proto.Waiting }
			working := func(s *proto.Session) bool { return s.Status == proto.Working }
			done := func(s *proto.Session) bool { return s.Status == proto.Idle && s.Unread }
			a.footerChip(c, pal, proto.Waiting, false, waiting, pal.attnText, locales.FooterNeedsYou, a.tip(locales.FooterGoToIt(), "session.attention"), a.jumpToAttention)
			a.footerChip(c, pal, proto.Working, false, working, pal.textMuted, locales.FooterWorking, locales.FooterGoToIt(), nil)
			a.footerChip(c, pal, proto.Idle, true, done, pal.textMuted, locales.FooterDone, locales.FooterGoToIt(), nil)
		case connConnecting:
			a.footerNote(c, pal, pal.textFaint, locales.StatusConnecting())
		default:
			a.footerNote(c, pal, pal.danger, locales.StatusDisconnected())
		}
	})
}

// footerChip counts the sessions that are, by is, as their rows' status
// icon and the count, in fg; a click goes to the next of them, by jump,
// or else after the one shown. one and many word its label and tooltip,
// as "%d session needs you". It shows nothing while none is.
func (a *App) footerChip(c *ui.Context, pal *palette, st proto.Status, unread bool, is func(*proto.Session) bool, fg ui.Color, say func(int) string, hint string, jump func()) {
	n := 0
	for i := range a.st.Sessions {
		// A host the app lost says nothing new, as for the Dock's badge.
		if h := a.hostOf(a.st.Sessions[i].ID); h.remote() && h.conn != connReady {
			continue
		}
		if is(&a.st.Sessions[i]) {
			n++
		}
	}
	words := ""
	if n > 0 {
		words = say(n)
	}
	if st == proto.Waiting && a.ck.loaded {
		// Agents, as the Agents badge counts them; the sessions in the
		// tooltip.
		agents := a.needsCount()
		if agents > 0 {
			if words != "" {
				words = locales.FooterAgentsNeedYou(agents) + " · " + words
			} else {
				words = locales.FooterAgentsNeedYou(agents)
			}
		}
		n = agents
	}
	if n == 0 {
		return
	}
	b := ui.ButtonBase(c.Key("chip-"+string(st))).Height(hControlSm).Padding(0, 8, 0, 6).Gap(5).Radius(rRow).AlignItems(ui.Center).
		Label(words).Tooltip(words + " · " + hint)
	if b.Hovered() {
		b.Background(pal.rowHover)
	}
	b.Children(func() {
		statusIcon(c, pal, st, unread, 12)
		ui.Textf(c, "%d", n).FontSize(fsSmall).FontWeight(wMedium).TextColor(fg).FontFeatures("tnum")
	})
	if b.Clicked() {
		if jump == nil {
			jump = func() { a.jumpTo(is) }
		}
		jump()
	}
}

// restartChip offers to restart kerod into this version of Kero, while it
// runs another.
func (a *App) restartChip(c *ui.Context, pal *palette) {
	b := ui.ButtonBase(c.Key("chip-restart")).Height(hControlSm).Padding(0, 8, 0, 8).Gap(6).Radius(rRow).AlignItems(ui.Center).
		Label(locales.CommonRestartService()).Tooltip(locales.UpdateRestartTip())
	if b.Hovered() {
		b.Background(pal.rowHover)
	}
	b.Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(pal.attention)
		ui.Text(c, locales.CommonRestartService()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.attnText).SingleLine()
	})
	if b.Clicked() {
		a.offerRestart()
	}
}

// footerNote says how the connection to kerod is, by a dot.
func (a *App) footerNote(c *ui.Context, pal *palette, dot ui.Color, label string) {
	ui.Row(c).Gap(7).Padding(0, 6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(dot)
		ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().
			Tooltip(locales.FooterServiceTip())
	})
}

// ago is how long ago a time in Unix milliseconds was, shortly, as
// proto.Ago writes it, in the language the app shows.
//
// It moves agoDue to when the label changes, if that is sooner.
func ago(ms int64, now time.Time) string {
	if ms == 0 {
		return ""
	}
	t := time.UnixMilli(ms)
	d := now.Sub(t)
	unit := 7 * 24 * time.Hour
	switch {
	case d < time.Hour:
		unit = time.Minute
	case d < 24*time.Hour:
		unit = time.Hour
	case d < 7*24*time.Hour:
		unit = 24 * time.Hour
	}
	if due := t.Add((max(d, 0)/unit + 1) * unit); agoDue.IsZero() || due.Before(agoDue) {
		agoDue = due
	}
	switch {
	case d < time.Minute:
		return locales.AgoNow()
	case d < time.Hour:
		return locales.AgoMinutes(int(d / time.Minute))
	case d < 24*time.Hour:
		return locales.AgoHours(int(d / time.Hour))
	case d < 7*24*time.Hour:
		return locales.AgoDays(int(d / (24 * time.Hour)))
	}
	return locales.AgoWeeks(int(d / (7 * 24 * time.Hour)))
}

// agoDue is when the soonest "ago" label of the frame being built changes,
// zero when it shows none. Only the main thread, which builds frames,
// touches it.
var agoDue time.Time

// statusIcon draws a session's status, as the sidebar shows it: a ring
// turning while it works, an amber ring when it needs the user, a filled
// check when it finished unseen, a faint check once seen.
func statusIcon(c *ui.Context, pal *palette, st proto.Status, unread bool, size float32) ui.Element {
	return statusIconP(c, pal, st, unread, -1, size)
}

// statusIconP is statusIcon with a program's progress (OSC 7501), 0–100,
// which a working ring fills instead of turning; -1 for none.
func statusIconP(c *ui.Context, pal *palette, st proto.Status, unread bool, progress int, size float32) ui.Element {
	box := ui.Box(c).Size(size, size).Shrink(0)
	switch {
	case st == proto.Working:
		box.Label(locales.StatusWorking())
	case st == proto.Waiting:
		box.Label(locales.StatusNeedsYou())
	case st == proto.Exited:
		box.Label(locales.StatusExited())
	case st == proto.Failed:
		box.Label(locales.StatusFailed())
	case unread:
		box.Label(locales.StatusDone())
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := min(r.W, r.H)/2 - 1
		switch {
		case st == proto.Working && progress >= 0:
			var track ui.Path
			track.Circle(cx, cy, rad)
			p.StrokePath(&track, 1.7, pal.work.Alpha(0.25))
			// From the top, clockwise: the circle starts at 3 o'clock.
			p.Transform(ui.Rotation(-90), cx, cy, func() {
				var arc ui.Path
				arc.Circle(cx, cy, rad)
				p.StrokePath(arc.Trim(0, max(0.02, float32(progress)/100)), 1.7, pal.work)
			})
		case st == proto.Working:
			var track ui.Path
			track.Circle(cx, cy, rad)
			p.StrokePath(&track, 1.7, pal.work.Alpha(0.25))
			// A still arc, as the agents view draws: a turning one asked
			// for a frame at the display's rate, about 2,400 wakeups a
			// second for as long as any agent worked.
			p.Transform(ui.Rotation(-90), cx, cy, func() {
				var arc ui.Path
				arc.Circle(cx, cy, rad)
				p.StrokePath(arc.Trim(0, 0.35), 1.7, pal.work)
			})
		case st == proto.Waiting:
			var ring ui.Path
			ring.Circle(cx, cy, rad)
			p.StrokePath(&ring, 1.7, pal.attention)
			// Still, as in the agents view: a pulse cost a frame at the
			// display's rate while anything waited.
			var dot ui.Path
			dot.Circle(cx, cy, rad*0.42)
			p.FillPath(&dot, pal.attention)
		case st == proto.Failed:
			var disc ui.Path
			disc.Circle(cx, cy, rad+0.5)
			col := pal.danger
			if !unread {
				col = pal.danger.Alpha(0.75)
			}
			p.FillPath(&disc, col)
			p.Line(cx, cy-rad*0.45, cx, cy+rad*0.08, 1.7, pal.onDone)
			var dot ui.Path
			dot.Circle(cx, cy+rad*0.42, 1)
			p.FillPath(&dot, pal.onDone)
		case st == proto.Exited:
			col := pal.textFaint
			if unread {
				col = pal.danger
			}
			var ring ui.Path
			ring.Circle(cx, cy, rad)
			p.StrokePath(&ring, 1.5, col)
			k := rad * 0.38
			p.Line(cx-k, cy-k, cx+k, cy+k, 1.5, col)
			p.Line(cx+k, cy-k, cx-k, cy+k, 1.5, col)
		case unread:
			var disc ui.Path
			disc.Circle(cx, cy, rad+0.5)
			p.FillPath(&disc, pal.done)
			check := (&ui.Path{}).MoveTo(cx-rad*0.42, cy+0.2).LineTo(cx-rad*0.08, cy+rad*0.36).LineTo(cx+rad*0.46, cy-rad*0.34)
			p.StrokePath(check, 1.6, pal.onDone)
		default:
			// Idle is a hollow dot: the check means done, to review
			// (council round 1, visual 24).
			var ring ui.Path
			ring.Circle(cx, cy, max(rad*0.5, 3)) // a 6 px ring at least (council round 2, S7)
			p.StrokePath(&ring, 1.5, pal.textFaint)
		}
	})
	return box
}

// --- the card ---

// workSession is the session whose panes the window shows, nil for none:
// a message shows in their place.
func (a *App) workSession() *proto.Session {
	s := a.session(a.selected)
	if s == nil || a.hostOf(s.ID).conn == connVersion || !a.loaded {
		return nil
	}
	return s
}

// card is the main card: the session's panes, or the agents view over
// them. The agents view rises over the panes as it opens and sinks as it
// closes, and the panes stay built under it while it moves: no frame shows
// the empty canvas. At rest only one of the two is built, so the panes
// cost nothing while the agents view shows.
func (a *App) card(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32) {
	on := a.ck.open && a.loaded
	stack := ui.Box(c.Key("card-stack")).Grow(1).MinHeight(0)
	move := motionQuick
	if on {
		move = motionBase
	}
	k := stack.AnimateTo("agents", b2f(on), move)
	h := stack.Bounds().H
	stack.Children(func() {
		if k < 1 {
			ui.Column(c.Key("work")).Absolute().Left(0).Top(0).Right(0).Bottom(0).AlignItems(ui.Stretch).Children(func() {
				a.workCard(c, pal, bar, shown)
			})
		}
		if k > 0 {
			over := ui.Column(c.Key("agents")).Absolute().Left(0).Top(0).Right(0).Bottom(0).AlignItems(ui.Stretch)
			if k < 1 {
				// One cached bitmap fades and moves, not the view's
				// layers: frames stay as cheap as at rest.
				over.Cache().Opacity(k).Translate(0, 8*(1-k))
				if !on {
					over.PassThrough()
				}
			}
			over.Children(func() { a.cockpitCard(c, pal, bar, shown) })
		}
		a.dropView(c, pal, stack, h)
	})
}

// workCard is the session's panes, or what the app has to say without
// one.
func (a *App) workCard(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32) {
	s := a.session(a.selected)
	h := a.local()
	if s != nil {
		h = a.hostOf(s.ID)
	}
	if s := a.workSession(); s != nil {
		s = a.shownSession(c, s)
		v := a.viewOf(s)
		if v == nil {
			a.panes(c, pal, bar, shown, s, true)
			return
		}
		// The panes, then the viewer.
		row := ui.Row(c.Key("with-side")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Bind(a.paneRef("with-side"))
		w := a.side.rowW
		row.Children(func() {
			ui.Column(c.Key("panes")).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
				a.panes(c, pal, bar, shown, s, v.full)
			})
			if v.full {
				// Quick look: the viewer floats over the panes as a panel,
				// and the panes keep their size, so no terminal rewraps
				// under it.
				pw := min(max(w-2*lookInset, 320), lookPanelMax)
				// A veil over the panes sets the panel apart; a click on
				// it closes quick look, as a click outside a popover does.
				veil := ui.Box(c.Key("look-veil")).Absolute().Left(0).Top(0).Bottom(0).Width(w).Radius(14).Background(pal.overlay.Alpha(0.5))
				if veil.Clicked() {
					sid := s.ID
					a.defer_(func() { a.closeView(a.session(sid)) })
				}
				ui.Row(c.Key("look-over")).Absolute().Left((w - pw) / 2).Top(lookInset / 2).Bottom(lookInset / 2).Width(pw).AlignItems(ui.Stretch).Children(func() {
					a.viewerCard(c, pal, s, v, pw)
				})
				return
			}
			// The viewer (Changes, or a file from Files) is a drawer over the
			// panes' right side: the panes keep their size, so no terminal
			// rewraps or shrinks to a sliver while it shows (council round
			// 1, visual 7; QA-051). Its left edge still sizes it.
			dw := a.drawerWidth(w)
			// Esc closes the drawer from wherever the keyboard is, a pane
			// aside, which takes its own Esc: a click on a file in Files
			// leaves the keyboard with no one.
			if pane := a.paneRef(a.focusOf(s)); !pane.Focused(c) && c.Shortcut(0, ui.KeyEscape) {
				sid := s.ID
				a.defer_(func() {
					a.closeView(a.session(sid))
					a.focusTerminal()
				})
			}
			ui.Row(c.Key("drawer")).Absolute().Right(0).Top(0).Bottom(0).Width(dw + paneGap).AlignItems(ui.Stretch).Children(func() {
				a.sideEdge(c, pal, w)
				a.viewerCard(c, pal, s, v, dw)
			})
		})
		return
	}
	// No session: one card, for what the app has to say.
	card := paned(ui.Column(c).Grow(1).MinHeight(0).Radius(rPane).Background(pal.card), pal).Clip()
	card.Children(func() {
		left := a.chrome.headerPad
		ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, left).DragWindow().Children(func() {
			if shown < 0.5 {
				ui.Row(c).Absolute().Left(a.chrome.buttonsLeft(bar)).Top(0).Bottom(0).AlignItems(ui.Center).Gap(2).Opacity(1 - 2*shown).Children(func() {
					iconButton(c, "show-sidebar", sidebarIcon, a.tip(locales.SidebarShow(), "sidebar.toggle"), pal, a.toggleSidebar)
					iconButton(c, "new-session-2", plusIcon, a.tip(locales.ActionNewSession(), "session.new"), pal, func() { a.newSession("") })
				})
			}
			ui.Text(c, "guios").FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).SingleLine()
			ui.Spacer(c)
			a.controlsRoom(c, 0)
		})
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			switch {
			case h.conn == connVersion && h.remote():
				a.message(c, pal, locales.HostVersionTitle(h.name), a.hostVersionDetail(h), locales.HostUpdateAction(h.name), func() { a.offerHostUpdate(h) })
			case h.conn == connVersion:
				a.message(c, pal, locales.UpdatedTitle(), locales.UpdatedDetail(), locales.CommonRestartService(), a.restartDaemon)
			case h.conn == connFailed && h.cl == nil && !h.remote():
				a.message(c, pal, locales.ConnCantReach(), h.err, locales.CommonTryAgain(), func() { h.retries = 0; h.conn = connConnecting; a.connect() })
			case !a.loaded:
				ui.Column(c).Fill().Center().Children(func() {
					ui.Spinner(c).Label(locales.StatusConnectingLabel())
				})
			default:
				a.emptyState(c, pal)
			}
		})
	})
}

func (a *App) message(c *ui.Context, pal *palette, title, detail, action string, fn func()) {
	var actions func()
	if action != "" {
		actions = func() {
			if button(c, "msg-action", action, "", true, pal) {
				fn()
			}
		}
	}
	emptyBlock(c, pal, nil, title, detail, true, actions)
}

func (a *App) emptyState(c *ui.Context, pal *palette) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Column(c).Shrink(0).AlignItems(ui.Center).Children(func() {
			emptyBlock(c, pal, terminalIcon, locales.EmptyTitle(), locales.EmptyDetail(), true, func() {
				if button(c, "empty-new", locales.ActionNewSession(), a.keys("session.new"), true, pal) {
					a.newSession("")
				}
				if button(c, "empty-open", locales.ActionOpenProject(), a.keys("project.open"), false, pal) {
					a.openProject()
				}
			})
		})
		ui.Column(c).Margin(sp6, sp6, 0, sp6).Padding(sp3, sp4).Gap(6).Radius(rCard).Background(pal.chip).MaxWidth(measure).Children(func() {
			ui.Text(c, locales.EmptyForAgents()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine()
			ui.Text(c, "tuios start-agent claude\ntuios send-text \"run the tests and fix them\"\ntuios list-agents").
				Font(a.termFamily()).FontSize(fsCode).TextColor(pal.text).LineHeight(1.5).NoWrap().Selectable()
		})
	})
}

// cmdHintDelay is how long Cmd (Ctrl elsewhere) must be held alone before
// the sidebar shows the keys that go to its rows. A chord pressed at once,
// as Ctrl+D, then does not flash them for a frame.
const cmdHintDelay = 350 * time.Millisecond

// cmdHeld reports whether Cmd has been held alone for cmdHintDelay.
func (a *App) cmdHeld(c *ui.Context) bool {
	if c.Modifiers() != ui.Cmd {
		a.cmdAt = time.Time{}
		return false
	}
	if a.cmdAt.IsZero() {
		a.cmdAt = c.Now()
	}
	if d := c.Now().Sub(a.cmdAt); d < cmdHintDelay {
		c.After(cmdHintDelay - d)
		return false
	}
	return true
}

// toastBox is where toasts show: over the foot of the agents list, or of
// the Mail view's list, else at the bottom centre.
func (a *App) toastBox() *ui.Rect {
	ck := &a.ck
	if !ck.open || ck.view == viewBoard || ck.stacked || ck.fleetRect.W <= 0 {
		return nil
	}
	r := ck.fleetRect
	return &r
}
