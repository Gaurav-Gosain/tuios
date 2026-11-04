package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

const paneGap = 8 // between panes, where the divider is

// paneRef returns the handle a pane's terminal binds, which its focus
// follows.
func (a *App) paneRef(id string) *ui.Handle {
	if a.refs == nil {
		a.refs = map[string]*ui.Handle{}
	}
	h := a.refs[id]
	if h == nil {
		h = &ui.Handle{}
		a.refs[id] = h
	}
	return h
}

// focusOf returns the pane of a session the user is in.
func (a *App) focusOf(s *proto.Session) string {
	if id := a.focus[s.ID]; id != "" && s.PaneByID(id) != nil {
		return id
	}
	if p := s.FocusPane(); p != nil {
		return p.ID
	}
	return ""
}

// shownPanes returns the panes of a session the window shows, as panes
// lays them out: the zoomed one alone, or the one the user is in without
// a layout.
func shownPanes(s *proto.Session) []string {
	p := s.PaneByID(s.Zoomed)
	if p == nil && (s.Layout != nil || s.Arrange != nil) && len(s.Panes) > 1 {
		ids := make([]string, len(s.Panes))
		for i, p := range s.Panes {
			ids[i] = p.ID
		}
		return ids
	}
	if p == nil {
		p = s.FocusPane()
	}
	if p == nil {
		return nil
	}
	return []string{p.ID}
}

// setFocus moves the user into a pane, and tells kerod, which remembers it.
func (a *App) setFocus(s *proto.Session, pane string) {
	if a.focus == nil {
		a.focus = map[string]string{}
	}
	if a.focus[s.ID] == pane && s.Focus == pane {
		return
	}
	a.focus[s.ID] = pane
	s.Focus = pane
	if pane != a.wantFocus {
		// The person chose another pane before a new one was drawn.
		a.wantFocus = ""
	}
	if s.Zoomed != "" {
		s.Zoomed = pane
	}
	a.ensureTerm(pane)
	a.touch(pane)
	sid, scrolling := s.ID, s.LayoutMode == "scrolling"
	a.call(s.ID, "focus", proto.FocusParams{Session: s.ID, Pane: pane}, func(err error) {
		if err == nil && scrolling {
			// The strip brings the focused column on screen.
			a.arrange(a.session(sid), "scroll.reveal", func(p *proto.ArrangeParams) { p.Pane = pane })
		}
	})
}

// focusTerminal gives the keyboard to the pane the user is in.
func (a *App) focusTerminal() {
	if s := a.session(a.selected); s != nil {
		if id := a.focusOf(s); id != "" {
			a.paneRef(id).Focus()
		}
		a.claimShown()
	}
}

// panes shows a session's panes in their layout: the zoomed one alone.
// right tells that they reach the window's right edge, which the side
// panel takes while it shows.
func (a *App) panes(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, right bool) {
	if len(s.Panes) == 0 {
		a.emptyWorkspace(c, pal, s)
		return
	}
	// A divider dragged moves the panes under the pointer, not after it.
	view, sig := s.ID+"/"+strconv.Itoa(s.Workspace), layoutSig(s)
	if a.layoutDrag.split != "" {
		c.DisableAnimations()
		// The frames of a drag resize the panes as they go.
		a.motion.layout = sig
	}
	a.watchPanes(view, sig)
	// One parent for each session and workspace: another one shown comes
	// at once, while panes opened, closed or moved within it animate
	// (paneTransition).
	a.reportGrid(c, s)
	ui.Column(c.Key("ws-" + view)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Bind(a.paneRef("ws-area")).Children(func() {
		if s.PaneByID(s.Zoomed) == nil && s.Arrange != nil && len(s.Panes) > 1 {
			// Master-stack, scrolling and floating, and panes floating
			// over the split tree: by their rectangles.
			a.arranged(c, pal, bar, shown, s, right)
			return
		}
		a.tiled(c, pal, bar, shown, s, right)
	})
}

// layoutSig is what lays out the panes a session shows: the zoomed pane,
// or the split tree with its ratios. A change animates them.
func layoutSig(s *proto.Session) string {
	var b strings.Builder
	b.WriteString(s.Zoomed)
	if z := s.PaneByID(s.Zoomed); z == nil && s.Layout != nil && len(s.Panes) > 1 {
		var walk func(l *proto.Layout)
		walk = func(l *proto.Layout) {
			if l.Leaf() {
				b.WriteString(" " + l.Pane)
				return
			}
			fmt.Fprintf(&b, " (%t %.3f", l.Row, l.Ratio)
			walk(l.A)
			walk(l.B)
			b.WriteString(")")
		}
		walk(s.Layout)
	} else if p := s.FocusPane(); z == nil && p != nil {
		b.WriteString(" " + p.ID)
	}
	if ar := s.Arrange; ar != nil && s.PaneByID(s.Zoomed) == nil {
		// The layouts placed by rectangles change with them.
		for _, p := range s.Panes {
			if r, ok := ar.Rects[p.ID]; ok {
				fmt.Fprintf(&b, " %s %d %d %d %d", p.ID, r.X, r.Y, r.W, r.H)
			}
		}
	}
	return b.String()
}

// divider is the gap between two panes, at r in the tiles' area, which a
// drag moves. w and h are the split's size.
func (a *App) divider(c *ui.Context, pal *palette, s *proto.Session, l *proto.Layout, key string, r ui.Rect, w, h float32) {
	// A drag leaves the keyboard in the pane the user is in.
	d := ui.Box(c.Key("div-" + key)).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).KeepFocus()
	if l.Row {
		d.Cursor(ui.CursorResizeColumn)
	} else {
		d.Cursor(ui.CursorResizeRow)
	}
	if d.Hovered() || d.Pressed() {
		// A short grip, so the gap shows it moves.
		d.Draw(func(p *ui.Painter, r ui.Rect) {
			if l.Row {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, pal.textFaint.Alpha(0.7), 1.5)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, pal.textFaint.Alpha(0.7), 1.5)
			}
		})
	}
	if dx, dy, ok := d.Dragged(); ok {
		// The ratio shares out the room the panes take, the split's less
		// the divider's.
		size, delta := w, dx
		if !l.Row {
			size, delta = h, dy
		}
		if size > 0 {
			// The ratio goes on past its bounds, unclamped, so the divider
			// comes back as the pointer comes back to it.
			if a.layoutDrag.split != key {
				r := l.Ratio
				if r <= 0 || r >= 1 {
					r = 0.5
				}
				a.layoutDrag.session, a.layoutDrag.split, a.layoutDrag.ratio = s.ID, key, r
			}
			a.layoutDrag.ratio += float64(delta / size)
			l.Ratio = math.Min(math.Max(a.layoutDrag.ratio, 0.12), 0.88)
		}
	} else if a.layoutDrag.split == key && !d.Pressed() {
		// Only the divider dragged saves the layout: another of the same
		// session would save it every frame of the drag.
		a.layoutDrag.session, a.layoutDrag.split = "", ""
		a.call(s.ID, "layout", proto.LayoutParams{Session: s.ID, Layout: s.Layout.Clone()}, nil)
	}
	if d.DoubleClicked() {
		l.Ratio = 0.5
		a.call(s.ID, "layout", proto.LayoutParams{Session: s.ID, Layout: s.Layout.Clone()}, nil)
	}
}

// paneCard is a pane: a card with a header and the terminal. top, left and
// right tell that it touches the window's edges, where the header drags
// the window, keeps clear of the traffic lights on macOS, and keeps room
// for the window controls elsewhere (controlsRoom).
func (a *App) paneCard(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, p *proto.Pane, top, left, right, multi bool) {
	focused := p.ID == a.focusOf(s)
	bg := pal.card
	// A browser's page stays opaque, so its toolbar does too, and neither
	// fades: a page is read, not typed into, from beside it.
	if multi && !focused && !p.Browser() && a.set.DimUnfocused {
		bg = pal.cardDim
	}
	// The spotlight (the prefix and B): the other panes all but go.
	dim := float32(1)
	if a.spotlight && multi && !focused {
		dim = 0.28
	}
	card := ui.Column(c.Key("pane-" + p.ID)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).Radius(rPane).Clip().Bind(a.paneRef("card-" + p.ID))
	// The tiles move their panes themselves (tiles.go); the panes placed
	// by rectangles move from where they were shown (paneTransition).
	if !a.motion.placed[p.ID] {
		card.Transition(paneTransition(p.ID))
	}
	a.motion.notePane(p.ID)
	if dim < 1 {
		card.Opacity(dim)
	}
	paned(card.Background(card.AnimateColor("bg", bg, ui.Animation{Duration: dFade})), pal)
	hovered := card.Hovered()
	if a.set.FocusFollowsMouse && multi {
		a.followMouse(c, s, p, card, hovered, focused)
	}
	// As the window controls over its header were its own (underControls).
	if !hovered && top && right {
		hovered = underControls(card, a.paneRef("card-"+p.ID).Bounds(c).W)
	}
	// The card takes presses: one on its padding, beside the terminal,
	// focuses the pane, rather than going on to what drags the window.
	if card.Pressed() && !focused {
		a.setFocus(s, p.ID)
		a.paneRef(p.ID).Focus()
	}

	// Another pane dragged over this one: where it would go.
	zone := zoneNone
	if multi {
		if d, over := ui.DragOver[paneDrag](card); over && d.session == s.ID && d.pane != p.ID {
			if x, y, in := card.PointerPosition(); in {
				b := a.paneRef("card-" + p.ID).Bounds(c)
				zone = zoneAt(x/max(b.W, 1), y/max(b.H, 1))
			}
		}
		if d, ok := ui.Drop[paneDrag](card); ok && d.session == s.ID && d.pane != p.ID {
			sid, target := s.ID, p.ID
			drop := a.lastZone
			a.defer_(func() { a.movePane(sid, d.pane, target, drop) })
		}
	}
	if zone != zoneNone {
		a.lastZone = zone
	}

	dragging := false
	card.Children(func() {
		if p.Browser() {
			// A browser's address bar is its header.
			dragging = a.paneBrowser(c, pal, bar, shown, s, p, top, left, right, multi, focused, hovered)
		} else {
			dragging = a.paneHeader(c, pal, bar, shown, s, p, top, left, right, multi, focused, hovered)
			a.paneTerminal(c, pal, s, p)
		}
		if multi && !p.Browser() && a.set.DimUnfocused {
			// The terminals the user is not in fade, text and all.
			v := ui.Box(c.Key("veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Background(pal.veil)
			v.Opacity(v.Animate("on", b2f(!focused), dFade))
		}
		if zone != zoneNone {
			a.dropPreview(c, pal, p.ID, zone)
		}
	})

	// The pane dragged sinks back where it was; one just moved springs in
	// at its new place.
	pose := ui.Pose{}
	switch {
	case dragging:
		pose = ui.Pose{Scale: 0.985, Fade: 0.45}
	case a.arrived[p.ID]:
		pose = ui.Pose{Scale: 0.94, Fade: 0.7}
		delete(a.arrived, p.ID)
		c.Invalidate()
	}
	card.AnimatePose(pose, ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.18}})
}

// followMouse focuses the pane the pointer moves into. Only the pointer
// moving does: a pane that comes under a pointer standing still, as another
// session shows or the layout changes, leaves the focus where the keyboard
// put it. And only going in does, so the keyboard can take the focus from
// the pane the pointer rests in.
func (a *App) followMouse(c *ui.Context, s *proto.Session, p *proto.Pane, card ui.Element, hovered, focused bool) {
	x, y, over := card.PointerPosition()
	if !over {
		if a.hoverPane == p.ID {
			a.hoverPane = ""
		}
		return
	}
	b := a.paneRef("card-" + p.ID).Bounds(c)
	at := [2]float32{b.X + x, b.Y + y}
	moved := at != a.hoverAt
	a.hoverAt = at
	// A press begun in another pane, as a selection dragged on past its
	// edge, does not hover this one.
	if !hovered || a.hoverPane == p.ID {
		return
	}
	a.hoverPane = p.ID
	if moved && !focused {
		a.setFocus(s, p.ID)
		a.paneRef(p.ID).Focus()
	}
}

// paneDrag is a pane dragged by its title to another place.
type paneDrag struct{ session, pane string }

// dropZone is where a pane dropped on another goes: beside it, on a side,
// or in its place, which the other takes (swap).
type dropZone int

const (
	zoneNone dropZone = iota
	zoneLeft
	zoneRight
	zoneUp
	zoneDown
	zoneSwap
)

// zoneAt returns the zone under the pointer, at u, v of a pane (0 to 1):
// its middle swaps, else the nearest edge.
func zoneAt(u, v float32) dropZone {
	if u > 0.3 && u < 0.7 && v > 0.3 && v < 0.7 {
		return zoneSwap
	}
	best, zone := u, zoneLeft
	for _, e := range []struct {
		d float32
		z dropZone
	}{{1 - u, zoneRight}, {v, zoneUp}, {1 - v, zoneDown}} {
		if e.d < best {
			best, zone = e.d, e.z
		}
	}
	return zone
}

func (z dropZone) side() string {
	return [...]string{"", "left", "right", "up", "down", ""}[z]
}

// dropPreview shows, over the pane under the pointer, the room the
// dragged pane would take, gliding from zone to zone, with what dropping
// does.
func (a *App) dropPreview(c *ui.Context, pal *palette, target string, zone dropZone) {
	key := target
	a.previewShown = true
	if a.previewKey != key {
		a.previewKey, a.previewSince = key, c.Now()
	}
	// left, top, width, height in percent of the pane
	r := [...][4]float32{
		zoneLeft:  {0, 0, 50, 100},
		zoneRight: {50, 0, 50, 100},
		zoneUp:    {0, 0, 100, 50},
		zoneDown:  {0, 50, 100, 50},
		zoneSwap:  {0, 0, 100, 100},
	}[zone]
	label, glyph := map[dropZone]string{
		zoneLeft: locales.PaneSplitLeft(), zoneRight: locales.ActionSplitRight(), zoneUp: locales.PaneSplitUp(), zoneDown: locales.ActionSplitDown(), zoneSwap: locales.PaneSwap(),
	}[zone], splitRightIcon
	if zone == zoneUp || zone == zoneDown {
		glyph = splitDownIcon
	}
	if zone == zoneSwap {
		glyph = swapIcon
	}
	fadeIn := min(float32(c.Now().Sub(a.previewSince))/float32(140*time.Millisecond), 1)
	if fadeIn < 1 {
		c.AnimationFrame()
	}
	spring := ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.12}}
	o := ui.Column(c.Key("drop")).Absolute().PassThrough().Padding(8).Opacity(fadeIn)
	o.LeftPercent(o.AnimateTo("l", r[0], spring)).TopPercent(o.AnimateTo("t", r[1], spring)).
		WidthPercent(o.AnimateTo("w", r[2], spring)).HeightPercent(o.AnimateTo("h", r[3], spring))
	o.Children(func() {
		room := ui.Column(c).Grow(1).Radius(10).AlignItems(ui.Center).
			Background(pal.accent.Alpha(0.10)).InsetShadow(0, 0, 28, 0, pal.accent.Alpha(0.22))
		room.Children(func() {
			// The label sits high in the room, clear of the title the
			// pointer carries through its middle.
			ui.Box(c).HeightPercent(22).Shrink(0)
			pill := ui.Row(c).Height(30).Padding(0, 12).Gap(7).Radius(15).AlignItems(ui.Center).
				Background(pal.accent).TextColor(onAccent(pal))
			floating(pill, pal).Children(func() {
				ui.Icon(c, glyph).Size(14, 14)
				ui.Text(c, label).FontSize(fsBody).FontWeight(wSemibold).SingleLine()
			})
			pill.AnimatePose(ui.Pose{Scale: 0.9 + 0.1*fadeIn}, ui.Snappy)
		})
	})
}

// movePane puts a pane dropped on another beside it, or in its place.
func (a *App) movePane(sid, pane, target string, zone dropZone) {
	s := a.session(sid)
	if s == nil || zone == zoneNone {
		return
	}
	if s.Layout == nil || s.Arrange != nil && s.Arrange.Rects[pane] != (proto.Rect{}) {
		// The layouts placed by rectangles take panes in an order: the
		// dropped pane and the one it lands on change places.
		if s.Arrange != nil {
			a.arrived[pane], a.arrived[target] = true, true
			a.arrange(s, "swap", func(p *proto.ArrangeParams) { p.Pane, p.Target = pane, target })
		}
		return
	}
	if zone == zoneSwap {
		s.Layout = s.Layout.Swap(pane, target)
		a.arrived[target] = true
	} else {
		s.Layout = s.Layout.Move(pane, target, zone.side())
	}
	a.arrived[pane] = true
	s.Zoomed = ""
	a.setFocus(s, pane)
	a.paneRef(pane).Focus()
	a.call(sid, "layout", proto.LayoutParams{Session: sid, Layout: s.Layout.Clone()}, nil)
}

// paneHeader builds a pane's header, and reports whether its title is
// being dragged.
func (a *App) paneHeader(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, p *proto.Pane, top, left, right, multi, focused, hovered bool) (dragging bool) {
	pad := float32(16)
	if top && left {
		// With the sidebar hidden, the header at the window's top left
		// starts after the traffic lights and the sidebar's buttons,
		// which come here.
		pad = a.chrome.headerPad
	}
	// A lone pane is titled as split ones are, by what runs in it: the
	// sidebar names the session (QA-037).
	title := p.PaneName()
	hostOf := a.hostOf(p.ID)
	if p.Host != "" && !hostOf.remote() {
		// A pane of this session whose program runs on a linked
		// machine is that machine's, as its paths are.
		if o := a.hostNamed(p.Host); o != nil {
			hostOf = o
		} else {
			hostOf = &host{name: p.Host}
		}
	}
	dir := hostOf.shortDir(p.WorkDir())
	showDir := dir != title || hostOf.remote()
	plan := a.planHeader(c, p, title, dir, pad, multi, top && right)
	h := ui.Row(c.Key("hdr")).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, pad).Gap(sp2)
	if h.Pressed() && !focused {
		a.setFocus(s, p.ID)
		a.paneRef(p.ID).Focus()
	}
	h.Children(func() {
		if top && left {
			a.sidebarButtons(c, pal, bar, shown)
		}
		// The title is the pane's handle: dragged, it takes the pane to
		// another place in the session.
		gripMin := plan.titleMin + 2*sp2 + 14 + sp2
		grip := ui.Row(c.Key("grip")).Gap(sp2).AlignItems(ui.Center).Shrink(1).MinWidth(gripMin).Height(hControl).Padding(0, sp2).Margin(0, 0, 0, -sp2).Radius(rControl)
		floats := a.floatHere[p.ID] && s.Arrange != nil
		if floats {
			// A floating pane's header moves it, under the pointer as it
			// is: it does not fade as a pane carried to a drop does.
			grip.Cursor(ui.CursorMove)
			a.dragFloat(c, "grip", grip, s, p, 0)
		} else if multi {
			grip.Drag(paneDrag{s.ID, p.ID}).Tooltip(locales.PaneDragTip())
			dragging = grip.Dragging()
			switch {
			case dragging:
				// What follows the pointer is a copy of the grip: a chip.
				floating(grip.Background(pal.popover), pal)
			case grip.Hovered():
				grip.Background(pal.rowHover).Cursor(ui.CursorGrab)
			}
		}
		grip.Children(func() {
			if p.Status != proto.Idle || p.Unread {
				statusIconP(c, pal, p.Status, p.Unread, p.Progress, 14)
			} else {
				ui.Icon(c, programIcon(p)).Size(14, 14).TextColor(pal.textFaint)
			}
			// The title gives way last: it keeps its first titleKeep DIPs
			// while the chips and the folder go before it.
			name := oneLine(c, title, fsBody, wSemibold).TextColor(pal.text).MinWidth(plan.titleMin)
			if multi && !focused && !dragging {
				name.TextColor(pal.textMuted)
			}
			// The home directory names itself "~", which the title may
			// already say; another machine's, as scp writes it: devbox:~/api.
			if showDir && plan.folderW > 0 {
				if !hostOf.remote() {
					// Cut at its start, so its last folder stays.
					cut := startCut(c, dir, fsSmall, plan.folderW)
					t := ui.Text(c, cut).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Shrink(0)
					if cut != dir {
						t.Tooltip(dir)
					}
				} else {
					// The host's name in its color, as the sidebar's strip
					// marks it: no pane passes for another machine's.
					ui.Row(c.Key("where")).Shrink(3).MinWidth(0).AlignItems(ui.Center).Clip().Children(func() {
						ui.Text(c, hostOf.name+":").FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.hostColor(hostOf)).SingleLine().Ellipsis("…").Shrink(0.5).MinWidth(0)
						oneLine(c, dir, fsSmall, 0).TextColor(pal.textFaint)
					})
				}
			}
		})
		if p.Git != nil && plan.git {
			a.gitChip(c, pal, s, p)
		}
		// The header's empty stretch moves the window, as a title bar
		// does, and zooms it on a double click: the header around it
		// takes clicks, which focus the pane.
		gap := ui.Box(c.Key("drag")).Grow(1).AlignSelf(ui.Stretch)
		if floats {
			gap.Cursor(ui.CursorMove)
			a.dragFloat(c, "gap", gap, s, p, 0)
		} else if top {
			gap.DragWindow()
		}
		a.paneChips(c, pal, p, multi, plan)
		// The button that shows the right sidebar sits by the pane's own,
		// as one row of buttons.
		ui.Row(c.Key("buttons")).AlignItems(ui.Center).Shrink(0).Children(func() {
			if plan.controls {
				a.paneControls(c, pal, s, p, hovered && !dragging, multi)
			}
			if top && right {
				a.showRightButton(c, pal)
			}
		})
		if top && right {
			a.controlsRoom(c, 8)
		}
	})
	return dragging
}

// sidebarButtons are the sidebar's buttons, which the header at the
// window's top left holds while the sidebar hides.
func (a *App) sidebarButtons(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32) {
	if shown >= 0.5 {
		return
	}
	ui.Row(c).Absolute().Left(a.chrome.buttonsLeft(bar)).Top(0).Bottom(0).AlignItems(ui.Center).Gap(2).Opacity(1 - 2*shown).Children(func() {
		iconButton(c, "show-sidebar", sidebarIcon, a.tip(locales.SidebarShow(), "sidebar.toggle"), pal, a.toggleSidebar)
		iconButton(c, "new-session-2", plusIcon, a.tip(locales.ActionNewSession(), "session.new"), pal, func() { a.newSession("") })
	})
}

// paneControls are a header's buttons acting on its pane, which show while
// the pointer is over it.
func (a *App) paneControls(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane, show, multi bool) {
	ctl := ui.Row(c.Key("controls")).Gap(0).AlignItems(ui.Center).Shrink(0)
	// They keep their room while hidden, so that nothing in the header
	// moves as the pointer comes (DESIGN.md principle 3).
	if !show {
		ctl.Invisible()
	}
	ctl.Opacity(ctl.Animate("shown", b2f(show), dHoverOut)).Children(func() {
		iconButton(c, "split-r", splitRightIcon, a.tip(locales.ActionSplitRight(), "pane.split.right"), pal, func() { a.splitPane(s, p.ID, "right") })
		iconButton(c, "split-d", splitDownIcon, a.tip(locales.ActionSplitDown(), "pane.split.down"), pal, func() { a.splitPane(s, p.ID, "down") })
		if multi {
			sid, pid := s.ID, p.ID
			zoom := func() { a.defer_(func() { a.zoomPane(a.session(sid), pid) }) }
			if s.Zoomed == p.ID {
				iconButton(c, "zoom", unzoomIcon, a.tip(locales.PaneShowAll(), "pane.zoom"), pal, zoom)
			} else {
				iconButton(c, "zoom", zoomIcon, a.tip(locales.ActionZoomPane(), "pane.zoom"), pal, zoom)
			}
			iconButton(c, "close", closeIcon, a.tip(locales.PaneClose(), "pane.close"), pal, func() { a.defer_(func() { a.closePane(a.session(sid), pid, true) }) })
		}
	})
}

// paneChips says what the pane is doing. The title leads with the status
// mark, so the chips go without one.
func (a *App) paneChips(c *ui.Context, pal *palette, p *proto.Pane, multi bool, plan headerPlan) {
	if plan.state {
		a.stateChip(c, pal, p, multi)
	}
	if plan.process {
		chip(c, pal, pal.chip, pal.textMuted, func() {
			ui.Text(c, p.Process).FontSize(fsSmall).SingleLine()
		}).Shrink(0).Tooltip(locales.PaneForegroundTip())
	}
}

// stateChip says what the pane is doing, one line, in its state's color.
func (a *App) stateChip(c *ui.Context, pal *palette, p *proto.Pane, multi bool) {
	switch p.Status {
	case proto.Waiting:
		msg := p.Message
		if msg == "" {
			msg = map[string]string{"permission": locales.PaneNeedsPermission(), "question": locales.PaneHasQuestion(), "auth": locales.PaneNeedsSignIn()}[p.Kind]
		}
		if msg == "" {
			msg = locales.StatusNeedsYou()
		}
		chip(c, pal, pal.attnSoft, pal.attention, func() {
			oneLine(c, msg, fsSmall, wMedium)
		}).MinWidth(80).MaxWidth(320)
	case proto.Working:
		chip(c, pal, pal.chip, pal.textMuted, func() {
			label := ago(p.Since, c.Now())
			if p.Progress >= 0 {
				label = itoa(p.Progress) + "%"
			}
			if p.Message != "" {
				label = p.Message + " · " + label
			}
			oneLine(c, label, fsSmall, 0).FontFeatures("tnum")
		}).MinWidth(80)
	case proto.Failed:
		chip(c, pal, pal.danger.Alpha(0.1), pal.danger, func() {
			msg := p.Message
			if msg == "" {
				msg = locales.StatusFailed()
			}
			oneLine(c, msg, fsSmall, wMedium)
		}).MinWidth(80)
	case proto.Exited:
		// A pane that stayed after its program exited (keep_open). It
		// gives up its room in a narrow header before the buttons do.
		chip(c, pal, pal.danger.Alpha(0.1), pal.danger, func() {
			ui.Text(c, locales.PaneExited(itoa(p.ExitCode))).FontSize(fsSmall).FontWeight(wMedium).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}).Shrink(1).MinWidth(0)
		// tuios starts no program again in a pane: it closes. With
		// several panes the header's own close button does it.
		if !multi {
			id := p.ID
			iconButton(c, "close-"+id, closeIcon, locales.PaneClose(), pal, func() {
				a.defer_(func() { a.closePane(a.sessionOfPane(id), id, false) })
			})
		}
	case proto.Idle:
		if p.Message != "" {
			chip(c, pal, pal.chip, pal.textMuted, func() {
				oneLine(c, p.Message, fsSmall, 0)
			}).MinWidth(80)
		}
	}
}

// headerPlan is what a pane's header shows, from the room its card had in
// the last frame: the title keeps its first titleKeep DIPs, then the
// state chip comes, then the folder, the branch and the process, each
// whole or not at all (DESIGN.md section 7, rule 5).
type headerPlan struct {
	titleMin, folderW             float32
	state, git, process, controls bool
}

func (a *App) planHeader(c *ui.Context, p *proto.Pane, title, dir string, pad float32, multi, corner bool) headerPlan {
	// A process named as the title says nothing more.
	process := p.Process != "" && p.Status != proto.Exited && !strings.EqualFold(p.Process, title)
	all := headerPlan{titleMin: min(natural(c, title, fsBody, wSemibold), titleKeep), folderW: natural(c, dir, fsSmall, 0) + 1, state: true, git: true, process: process, controls: true}
	w := a.paneRef("card-" + p.ID).Bounds(c).W
	if w <= 0 {
		return all
	}
	plan := headerPlan{titleMin: all.titleMin}
	room := w - pad - 10
	// The pane's buttons keep their room only where a pane has it: below
	// 260 DIPs they leave the header, and the Pane menu, the pane's own
	// right-click menu and the keys still reach them.
	buttons := float32(2)
	if multi {
		buttons = 4
	}
	if w >= 260 {
		plan.controls = true
		room -= buttons * hControl
	}
	if corner {
		room -= controlsW + 6 + 8 + hControl
	}
	room -= 14 + sp2 + plan.titleMin + sp2
	take := func(need float32) bool {
		if need <= room {
			room -= need + sp2
			return true
		}
		return false
	}
	switch p.Status {
	case proto.Waiting, proto.Working, proto.Failed, proto.Exited:
		plan.state = take(80 + 14)
	case proto.Idle:
		plan.state = p.Message == "" || take(80)
	}
	if rest := natural(c, title, fsBody, wSemibold) - plan.titleMin; rest > 0 {
		room -= min(rest, max(room, 0))
	}
	plan.git = w >= 320 && take(96)
	plan.process = process && take(natural(c, p.Process, fsSmall, 0)+14)
	// The folder takes what is left, cut at its start, down to 48 DIPs.
	if dw := natural(c, dir, fsSmall, 0) + 1; room >= 48 {
		plan.folderW = min(dw, room)
	}
	return plan
}

func (a *App) paneTerminal(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane) {
	e := a.terms[p.ID]
	if e == nil {
		a.ensureTerm(p.ID)
		e = a.terms[p.ID]
	}
	// The terminal fills the card under the header: its own padding
	// takes the colors of the cells beside it, so that a full-screen
	// program's background reaches the card's edges.
	ui.Box(c.Key("body")).Grow(1).MinHeight(0).Children(func() {
		switch {
		case e != nil && e.term != nil && a.cm != nil && a.cm.pane == p.ID:
			a.copyView(c, pal)
		case e != nil && e.term != nil:
			ref := a.paneRef(p.ID)
			terminal.View(c.Key("term"), e.term).Fill().Bind(ref)
			if a.labelsShown() {
				a.paneLabel(c, pal, s, p.ID)
			}
			if link, isPath, at := e.term.HoveredLink(); isPath {
				a.imagePeek(p.ID, link, at, ref.Bounds(c))
			}
			if a.wantFocus == p.ID {
				// The keyboard goes to a pane made a moment ago once its
				// terminal is here to take it.
				ref.Focus()
				a.wantFocus = ""
			} else if ref.Focused(c) && a.focusOf(s) != p.ID && s.PaneByID(a.wantFocus) == nil {
				a.setFocus(s, p.ID)
			}
			if a.find.pane == p.ID {
				a.findBar(c, pal, p, e.term)
			}
			a.linkBadge(c, pal, p.ID, e)
		case e == nil && a.hostOf(p.ID).remote() && a.hostOf(p.ID).conn != connReady:
			// Shown as it was, until its host is back.
			a.pendingInput(c, p.ID)
			h := a.hostOf(p.ID)
			switch {
			case h.conn == connConnecting || h.busy != "":
				ui.Column(c).Fill().Center().Gap(10).Children(func() {
					ui.Spinner(c)
					ui.Text(c, locales.HostReconnectingTo(h.name)).FontSize(fsSmall).TextColor(pal.textFaint)
				})
			default:
				a.message(c, pal, a.hostProblem(h), h.err, locales.CommonTryAgain(), func() { a.reconnectHost(h) })
			}
		case e != nil && e.err != "":
			a.message(c, pal, locales.PaneCantShow(), e.err, locales.CommonTryAgain(), func() {
				a.dropTerm(p.ID)
				a.ensureTerm(p.ID)
			})
		default:
			a.pendingInput(c, p.ID)
			// Attaching takes a moment for a long scrollback: a spinner
			// only once it is a noticeable one.
			if e != nil && time.Since(e.started) > 250*time.Millisecond {
				ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c).Label(locales.PaneAttaching()) })
			} else {
				c.After(260 * time.Millisecond)
			}
		}
	})
}

// linkBadge marks a pane of another machine whose link is down: its last
// frame stays, faded, with a badge that says the app reconnects. The new
// frame paints over it in place once the link is back.
func (a *App) linkBadge(c *ui.Context, pal *palette, id string, e *termEntry) {
	h := a.hostOf(id)
	if !h.remote() || e.stream == nil {
		return
	}
	lost := e.stream.Lost()
	if !lost && h.conn == connReady {
		return
	}
	ui.Box(c.Key("lost-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Background(pal.veil)
	label := locales.HostPaneReconnecting(h.name)
	if !lost && h.conn == connFailed {
		label = locales.HostPaneWaiting(h.name)
	}
	ui.Row(c.Key("lost")).Absolute().Top(8).Right(12).Height(28).Padding(0, 10, 0, 8).Gap(7).AlignItems(ui.Center).
		Radius(14).Background(pal.popover).Border(1, pal.edgeHigh).Label(label).Children(func() {
		if lost || h.conn == connConnecting {
			ui.Spinner(c).Size(12, 12)
		} else {
			ui.Box(c).Size(7, 7).Radius(3.5).Background(a.hostDotColor(pal, h))
		}
		ui.Text(c, label).FontSize(12).TextColor(pal.textMuted).SingleLine()
	})
}

// --- pane actions ---

func (a *App) splitPane(s *proto.Session, at, direction string) {
	if s == nil {
		return
	}
	if at == "" && a.splitting[s.ID] {
		// The focused pane's split is on its way, and the pane it makes
		// takes the focus: this one splits that pane, once it is here,
		// as a second new window in the TUI splits the first.
		sid := s.ID
		a.splitQueue[sid] = append(a.splitQueue[sid], func() { a.splitPane(a.session(sid), "", direction) })
		return
	}
	if at == "" {
		at = a.focusOf(s)
	}
	if at == "" {
		// An empty workspace: the session itself, so the new pane opens
		// alone in the workspace it shows, where the session works.
		a.split(s, proto.CreateParams{Split: s.ID, Direction: direction, Dir: s.WorkDir()})
		return
	}
	a.split(s, proto.CreateParams{Split: at, Direction: direction})
}

// split makes a pane beside another, as p asks, on the session's host,
// and puts the user in it.
func (a *App) split(s *proto.Session, p proto.CreateParams) {
	if s == nil {
		return
	}
	h := a.hostOf(s.ID)
	if pane := s.PaneByID(p.Split); pane != nil && !pane.Browser() {
		// Two halves too small to read a prompt help no one: the pane
		// stays whole, and the toast says what to do instead.
		// The pane's size is its terminal's grid, what the person sees.
		// Pane.Cols and Rows are the rectangle the layout last gave the
		// window in the session's cells, which a session guios made sets
		// from the pane it came from, as small as 20 by 12.
		cols, rows := pane.Cols, pane.Rows
		if t := a.termFor(pane.ID); t != nil {
			if c, r := t.Size(); c > 0 && r > 0 {
				cols, rows = c, r
			}
		}
		down := p.Direction == "down" || p.Direction == "up"
		if cols > 0 && rows > 0 && (down && rows/2 < minPaneRows || !down && cols/2 < minPaneCols) {
			a.toast(locales.ToastPaneTooSmall())
			return
		}
		p.Cols, p.Rows = cols, rows
		if p.Direction == "down" || p.Direction == "up" {
			p.Rows = max(p.Rows/2, 4)
		} else {
			p.Cols = max(p.Cols/2, 10)
		}
	}
	cl := h.cl
	if cl == nil {
		return
	}
	if a.splitting == nil {
		a.splitting, a.splitQueue = map[string]bool{}, map[string][]func(){}
	}
	sid := s.ID
	a.splitting[sid] = true
	go func() {
		var ns proto.Session
		err := cl.Call("split", p, &ns)
		a.update(func() {
			a.splitting[sid] = false
			if q := a.splitQueue[sid]; len(q) > 0 {
				a.splitQueue[sid] = q[1:]
				defer q[0]()
			}
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.replaceSession(h, ns)
			if cur := a.session(ns.ID); cur != nil {
				if a.focus == nil {
					a.focus = map[string]string{}
				}
				a.focus[ns.ID] = ns.Focus
				a.ensureTerm(ns.Focus)
				a.touch(ns.Focus)
				if !a.isBrowser(ns.Focus) {
					// A browser takes the keyboard in its address bar, or
					// its page once it has one. A terminal takes it once it
					// is drawn: until then the pane that had the keyboard
					// keeps it, and must not take the focus back.
					a.paneRef(ns.Focus).Focus()
					a.wantFocus = ns.Focus
				}
			}
		})
	}()
}

// replaceSession puts a session as kerod answered into the state, before
// the state that has it comes.
func (a *App) replaceSession(h *host, ns proto.Session) {
	// Its new pane is the host's from now, as its terminal attaches there.
	a.own(h, ns)
	for i := range a.st.Sessions {
		if a.st.Sessions[i].ID == ns.ID {
			a.st.Sessions[i] = ns
			return
		}
	}
	a.addPending(h, ns)
}

func (a *App) closePane(s *proto.Session, pane string, ask bool) {
	if s == nil {
		return
	}
	if len(s.Panes) == 0 {
		return
	}
	// The last pane of a session ends it, unless another workspace still
	// holds panes.
	if len(s.Panes) == 1 && !otherWorkspacePanes(s) {
		a.closeSession(s.ID, ask)
		return
	}
	p := s.PaneByID(pane)
	if p == nil {
		return
	}
	if ask && a.set.ConfirmClose && p.Status != proto.Exited && p.Process != "" {
		sid := s.ID
		a.confirm = &confirmation{
			open:    true,
			title:   locales.ConfirmClosePaneTitle(),
			detail:  locales.ConfirmClosePaneDetail(p.Process),
			action:  locales.PaneClose(),
			destroy: true,
			run: func() {
				if s := a.session(sid); s != nil {
					a.closePane(s, pane, false)
				}
			},
		}
		return
	}
	// The neighbor taking the room takes the focus, as kerod does.
	if a.focusOf(s) == pane && s.Layout != nil {
		for _, dir := range []string{"left", "up", "right", "down"} {
			if n := s.Layout.Neighbor(pane, dir); n != "" {
				a.focus[s.ID] = n
				break
			}
		}
	}
	s.Layout = s.Layout.Remove(pane)
	if s.Layout.Leaf() {
		s.Layout = nil
	}
	if s.Zoomed == pane {
		s.Zoomed = ""
	}
	for i := range s.Panes {
		if s.Panes[i].ID == pane {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			break
		}
	}
	a.dropTerm(pane)
	a.dropWeb(pane)
	a.call(pane, "pane.close", proto.IDParams{ID: pane}, nil)
	// Once the panes left are built in their new layout: a pane that was
	// in a split is a new element alone, which a focus given now misses.
	a.defer_(a.focusTerminal)
}

func (a *App) zoomPane(s *proto.Session, pane string) {
	if s == nil || len(s.Panes) < 2 {
		return
	}
	if pane == "" {
		pane = a.focusOf(s)
	}
	if s.Zoomed == pane {
		s.Zoomed = ""
	} else {
		s.Zoomed = pane
		a.setFocus(s, pane)
	}
	a.call(s.ID, "zoom", proto.FocusParams{Session: s.ID, Pane: pane}, nil)
	a.paneRef(pane).Focus()
}

// focusDirection moves into the pane next to the focused one.
func (a *App) focusDirection(dir string) {
	s := a.session(a.selected)
	if s == nil || s.Zoomed != "" {
		return
	}
	n := ""
	switch {
	case s.Arrange != nil:
		n = arrangedNeighbour(s, a.focusOf(s), dir)
		if n == "" && s.Layout != nil {
			n = s.Layout.Neighbor(a.focusOf(s), dir)
		}
	case s.Layout != nil:
		n = s.Layout.Neighbor(a.focusOf(s), dir)
	}
	if n != "" {
		a.setFocus(s, n)
		a.paneRef(n).Focus()
	}
}

// cyclePane moves to the next or previous pane, in the layout's order.
func (a *App) cyclePane(d int) {
	s := a.session(a.selected)
	if s == nil || len(s.Panes) < 2 {
		return
	}
	order := paneOrder(s)
	i := 0
	for j, id := range order {
		if id == a.focusOf(s) {
			i = j
		}
	}
	n := order[(i+d+len(order))%len(order)]
	a.setFocus(s, n)
	a.paneRef(n).Focus()
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// defer_ runs fn once the view is built: for actions that change the
// panes or sessions being built.
func (a *App) defer_(fn func()) { a.later = append(a.later, fn) }

func chip(c *ui.Context, pal *palette, bg, fg ui.Color, fn func()) ui.Element {
	return ui.Row(c).Height(hChip).Padding(0, 7).Gap(5).Radius(rRow).AlignItems(ui.Center).MinWidth(0).Shrink(1).MaxWidth(240).
		Background(bg).TextColor(fg).Children(fn)
}

// closeFocused closes what the user is in: the viewer while it has the
// keyboard, else the pane, or the session with its last pane. A program
// asking through kero do means the pane, wherever the keyboard happens to be.
func (a *App) closeFocused() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	if a.side.focused && a.from == nil && a.viewOf(s) != nil {
		a.closeView(s)
		return
	}
	a.closePane(s, a.focusOf(s), true)
}

// otherWorkspacePanes tells that a workspace the session does not show
// holds panes.
func otherWorkspacePanes(s *proto.Session) bool {
	for _, w := range s.Workspaces {
		if w.N != s.Workspace && w.Panes > 0 {
			return true
		}
	}
	return false
}

// emptyWorkspace fills the pane area of a workspace that holds no pane:
// a card as a pane's, which says so and opens one.
func (a *App) emptyWorkspace(c *ui.Context, pal *palette, s *proto.Session) {
	card := paned(ui.Column(c.Key("ws-empty")).Grow(1).MinHeight(0).Radius(rPane).Background(pal.card), pal).Clip()
	card.Children(func() {
		ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, a.chrome.headerPad).DragWindow().Children(func() {
			oneLine(c, s.DisplayName(a.set.SessionName), fsBody, wSemibold).TextColor(pal.text)
			ui.Spacer(c)
			a.controlsRoom(c, 0)
		})
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			emptyBlock(c, pal, terminalIcon, locales.WorkspaceEmptyTitle(s.Workspace), locales.WorkspaceEmptyDetail(), false, func() {
				if button(c, "ws-empty-new", locales.WorkspaceNewPane(), a.keys("pane.new"), true, pal) {
					a.splitPane(s, "", "")
				}
			})
		})
	})
}

// minPaneCols and minPaneRows are the least a split leaves each half.
const (
	minPaneCols = 24
	minPaneRows = 5
)

// reportGrid tells the session's host the pane area's size in cells, when
// it changed: the box tuios's layout engine lays the panes out in while no
// TUI shows the session. A cell is measured once per area size and text
// size, from the focused terminal: its box changes on every frame of a pane
// animation while its grid does not, and a grid measured then would change
// on every frame and rebuild the state each time.
func (a *App) reportGrid(c *ui.Context, s *proto.Session) {
	area := a.paneRef("ws-area").Bounds(c)
	if area.W <= 0 || area.H <= 0 {
		return
	}
	key := [3]float32{area.W, area.H, a.set.FontSize}
	if key == a.gridKey {
		return
	}
	id := a.focusOf(s)
	t := a.termFor(id)
	if t == nil || panesMoving() {
		return
	}
	cols, rows := t.Size()
	tb := a.paneRef(id).Bounds(c)
	if cols <= 0 || rows <= 0 || tb.W <= 0 || tb.H <= 0 {
		return
	}
	a.gridKey = key
	g := [2]int{int(area.W * float32(cols) / tb.W), int(area.H * float32(rows) / tb.H)}
	if g == a.grid {
		return
	}
	a.grid = g
	a.call(s.ID, "grid", proto.GridParams{Cols: g[0], Rows: g[1]}, nil)
}

// panesMoving reports whether a pane animation holds the leases now: the
// terminals' boxes are between two sizes.
func panesMoving() bool {
	leaseHold.mu.Lock()
	defer leaseHold.mu.Unlock()
	return time.Now().Before(leaseHold.until)
}

// paneLabel is a pane's number over it while the labels show (the prefix
// and Q): 1 for the first pane, 0 for the tenth.
func (a *App) paneLabel(c *ui.Context, pal *palette, s *proto.Session, id string) {
	n := -1
	for i := range s.Panes {
		if s.Panes[i].ID == id {
			n = (i + 1) % 10
		}
	}
	if n < 0 || len(s.Panes) > 10 {
		return
	}
	ui.Box(c.Key("label")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Children(func() {
		ui.Text(c, strconv.Itoa(n)).FontSize(64).FontWeight(700).TextColor(pal.popover).
			Padding(4, 28).Radius(18).Background(pal.accent)
	})
}
