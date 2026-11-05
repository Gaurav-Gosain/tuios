package main

import (
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The scratch terminal is the TUI's: toggle_scratch (the prefix and g)
// shows the session's scratch group, a pane on a scratch workspace that no
// list, pill or session key reaches, over the workspace the user is on. The
// daemon makes it, started in the focused pane's folder, and focuses it.
// The focus on a scratch pane is what tells every client to show it, so a
// TUI on the same session shows the same scratch, and guios shows its
// dropdown as a TUI shows the scratch: guios follows the daemon's focus.
// Hiding it gives the focus back to the pane the user was in.
//
// guios shows it two ways: as a dropdown over the panes in the main window
// (the prefix and g), and as the quick terminal, a panel that drops down
// from the top of the screen as its global shortcut is pressed, over every
// app, as Ghostty's quick terminal and Quake's console do. Both show the
// scratch of the session the window shows.

const (
	quickAction = "quickTerminal.toggle"
	quickHeight = 0.4 // of the work area of the display with the pointer
	quickSlide  = 180 * time.Millisecond
)

// quickTerm is the scratch's state, on the main thread.
type quickTerm struct {
	win   *mygo.Window // the panel, made as it first shows
	shown bool         // the panel shows, or slides down
	// drop says the dropdown shows over the panes of session, and
	// focusDrop that its terminal takes the keyboard once it is built.
	drop, focusDrop bool
	session         string
	// back is the pane the focus goes back to as the scratch hides.
	back string
	// creating says the daemon makes the session's scratch, until its pane
	// shows in the state.
	creating bool
	// seen is the session the last state showed, and seenFocus whether its
	// focus was on its scratch: a change of it is the daemon's, as a TUI
	// shows or hides the scratch.
	seen      string
	seenFocus bool
	// route sends what is typed to the scratch from the frame of the key
	// that shows it, and back to the pane from the key that hides it, until
	// the terminal that should have the keyboard has it (since routeAt).
	route   keyRoute
	routeAt time.Time
	pane    string     // the pane the panel's terminal shows
	e       *termEntry // that terminal, attached
	ref     ui.Handle
	err     string
	// keys is the global shortcut registered, and asked what the settings
	// asked for last, which failed when it is not keys.
	keys, asked string
	slides      atomic.Int64 // counts slides: the last one moves the panel
}

// applyGlobalShortcuts registers the quick terminal's shortcut as the
// settings say: none while the user types a shortcut in Settings, which
// would else toggle the terminal rather than record.
func (a *App) applyGlobalShortcuts() {
	q := &a.quick
	want := ""
	if a.ss.recording == "" {
		act, _ := actions.Find(quickAction)
		want = actions.Keys(act, a.set.Shortcuts)
	}
	if want == q.asked {
		return
	}
	q.asked = want
	if q.keys != "" {
		mygo.GlobalShortcut.Unregister(q.keys)
		q.keys = ""
	}
	if want == "" {
		return
	}
	if err := mygo.GlobalShortcut.Register(want, a.toggleQuick); err != nil {
		log.Println("kero: quick terminal:", err)
		a.toast(locales.ToastGlobalShortcutFailed(actions.Glyphs(want), err.Error()))
		return
	}
	q.keys = want
}

func (a *App) toggleQuick() {
	if a.quick.shown {
		a.hideQuick()
	} else {
		a.showQuick()
	}
}

// keyRoute is where the terminals' typed bytes go for a moment: the
// keyboard moves in the frame of a key, and a view that is not built yet
// cannot take it. gated.Write asks it, from any goroutine.
type keyRoute struct {
	mu   sync.Mutex
	on   bool
	to   *client.Stream // nil keeps the bytes until it attaches
	keep []byte
}

// routeKeep bounds the bytes a route keeps for a terminal still attaching.
const routeKeep = 4096

// set sends typed bytes to to, nil to keep them until a later set names
// a stream, which then gets them first.
func (r *keyRoute) set(to *client.Stream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.on, r.to = true, to
	if to != nil && len(r.keep) > 0 {
		_, _ = to.Write(r.keep)
		r.keep = nil
	}
}

// off ends the route, and drops what no stream took.
func (r *keyRoute) off() {
	r.mu.Lock()
	r.on, r.to, r.keep = false, nil, nil
	r.mu.Unlock()
}

// active reports whether the route is on, and whether it keeps the bytes.
func (r *keyRoute) active() (on, keeping bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.on, r.on && r.to == nil
}

// take sends p, which the terminal of from wrote, where the route goes,
// and reports whether it did. The terminal the route goes to writes as
// it would.
func (r *keyRoute) take(from *client.Stream, p []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.on || r.to == from {
		return false
	}
	if r.to == nil {
		if len(r.keep)+len(p) <= routeKeep {
			r.keep = append(r.keep, p...)
		}
		return true
	}
	_, _ = r.to.Write(p)
	return true
}

// routeTo starts a route to a pane's terminal in the main window, keeping
// the bytes while it attaches.
func (a *App) routeTo(pane string) {
	var to *client.Stream
	if e := a.terms[pane]; e != nil {
		to = e.stream
	}
	a.quick.route.set(to)
	a.quick.routeAt = time.Now()
}

// scratchPane is the pane of a session's scratch to show: the one with the
// focus, else its first, nil for none.
func scratchPane(s *proto.Session) *proto.Pane {
	if s == nil || len(s.Scratch) == 0 {
		return nil
	}
	for i := range s.Scratch {
		if s.Scratch[i].ID == s.Focus {
			return &s.Scratch[i]
		}
	}
	return &s.Scratch[0]
}

// scratchFocused reports whether the daemon's focus is on the session's
// scratch, which every client then shows.
func scratchFocused(s *proto.Session) bool {
	if s == nil {
		return false
	}
	for _, p := range s.Scratch {
		if p.ID == s.Focus {
			return true
		}
	}
	return false
}

// toggleScratch is the TUI's scratch key (toggle_scratch).
//
// It follows the daemon: the scratch hides while the shown session's focus
// is on it, or the dropdown shows it, and shows otherwise. So a toggle
// never falls out of step with what the daemon and a TUI show.
func (a *App) toggleScratch() {
	if a.quick.drop || scratchFocused(a.session(a.selected)) {
		a.hideDrop()
	} else {
		a.showDrop()
	}
}

// showDrop shows the scratch dropdown over the panes, starting the scratch
// first if the session has none.
func (a *App) showDrop() {
	q := &a.quick
	s := a.session(a.selected)
	if s == nil {
		a.toast(locales.ScratchNoSession())
		return
	}
	if q.shown {
		a.hideQuick()
	}
	q.drop, q.focusDrop, q.session = true, true, s.ID
	a.openScratch(s)
	// The keys go to the scratch from this frame on, though its view is
	// built only in the next one.
	if p := scratchPane(s); p != nil {
		a.routeTo(p.ID)
	} else {
		a.quick.route.set(nil)
		a.quick.routeAt = time.Now()
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// hideDrop hides the scratch dropdown, and the keyboard goes back to the
// pane the user was in.
func (a *App) hideDrop() {
	q := &a.quick
	s := a.session(q.session)
	if s == nil || s.ID != a.selected {
		s = a.session(a.selected)
	}
	if !q.drop && !scratchFocused(s) {
		return
	}
	q.drop = false
	if s != nil {
		if back := a.backPane(s); back != "" {
			a.routeTo(back)
		} else {
			q.route.off()
		}
	}
	a.leaveScratch(s)
	a.defer_(a.focusTerminal)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// openScratch puts the daemon's focus on the session's scratch, or has the
// daemon start it, in the focused pane's folder, as the TUI's scratchDir.
// It remembers the pane to go back to.
func (a *App) openScratch(s *proto.Session) {
	q := &a.quick
	if !scratchFocused(s) {
		q.back = a.focusOf(s)
	}
	if p := scratchPane(s); p != nil {
		a.call(s.ID, "focus", proto.FocusParams{Session: s.ID, Pane: p.ID}, nil)
		return
	}
	if q.creating {
		return
	}
	dir, from := s.Pane.Dir, ""
	if p := s.PaneByID(a.backPane(s)); p != nil {
		dir, from = p.Dir, p.ID
	}
	q.creating = true
	a.call(s.ID, "scratch", proto.ScratchParams{Session: s.ID, Dir: dir, From: from}, func(err error) {
		if err != nil {
			q.creating = false
			q.drop = false
			a.hidePanel()
			a.toast(err.Error())
		}
	})
}

// leaveScratch gives the daemon's focus back from the session's scratch to
// the pane the user was in, which hides it in every client.
//
// On a workspace with no pane there is none to go back to: selecting the
// workspace the session shows takes the focus off the scratch, as the
// daemon's select-workspace does.
func (a *App) leaveScratch(s *proto.Session) {
	if !scratchFocused(s) {
		return
	}
	if back := a.backPane(s); back != "" {
		a.call(s.ID, "focus", proto.FocusParams{Session: s.ID, Pane: back}, nil)
		return
	}
	a.call(s.ID, "workspace", proto.WorkspaceParams{Session: s.ID, Workspace: s.Workspace}, nil)
}

// backPane is the pane the keyboard goes back to as the scratch hides: the
// one the user was in, if it is still on the workspace, "" for none.
func (a *App) backPane(s *proto.Session) string {
	if s.PaneByID(a.quick.back) != nil {
		return a.quick.back
	}
	return a.focusOf(s)
}

// quickChanged follows a state of the daemon: the dropdown shows while the
// shown session's focus is on its scratch, as a TUI shows it, and the
// panel follows the scratch's pane.
func (a *App) quickChanged() {
	q := &a.quick
	s := a.session(a.selected)
	f := scratchFocused(s)
	if s != nil && scratchPane(s) != nil {
		q.creating = false
	}
	switch {
	case s == nil || s.ID != q.seen:
		// Another session: its scratch shows if its focus is there.
		q.seen, q.seenFocus = "", f
		if s != nil {
			q.seen = s.ID
		}
		if !q.shown && q.drop != f {
			q.drop, q.focusDrop = f, f
		}
		if s != nil {
			q.session = s.ID
		}
	case f != q.seenFocus:
		q.seenFocus = f
		if !q.shown && q.drop != f {
			q.drop, q.focusDrop, q.session = f, f, s.ID
			if !f {
				a.defer_(a.focusTerminal)
			}
		}
	}
	if q.drop && scratchPane(s) == nil && !q.creating {
		// The scratch's shell exited.
		q.drop = false
		a.defer_(a.focusTerminal)
	}
	if q.shown {
		a.attachQuick()
	}
}

// showQuick drops the quick terminal down on the display with the pointer,
// starting the scratch first if the session has none.
func (a *App) showQuick() {
	q := &a.quick
	if q.drop {
		q.drop = false // the panel shows it now; the focus stays on it
	}
	if q.win == nil {
		a.makeQuick()
	}
	q.err = ""
	if s := a.session(a.selected); s != nil {
		a.openScratch(s)
	}
	a.attachQuick()
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
	r := mygo.Rectangle{X: wa.X, Y: wa.Y, Width: wa.Width, Height: int(float64(wa.Height) * quickHeight)}
	q.shown = true
	q.ref.Focus()
	a.slideQuick(r, wa.Y-r.Height, wa.Y, nil)
	q.win.Show()
	q.win.Focus()
}

// hideQuick slides the quick terminal back up, which gives the keyboard
// back to the app the user was in, and the daemon's focus back to the pane
// the user was in.
func (a *App) hideQuick() {
	if !a.quick.shown {
		return
	}
	a.hidePanel()
	a.leaveScratch(a.session(a.selected))
}

// hidePanel slides the panel up, and leaves the daemon's focus be.
func (a *App) hidePanel() {
	q := &a.quick
	if !q.shown {
		return
	}
	q.shown = false
	r := q.win.Bounds()
	a.slideQuick(r, r.Y, r.Y-r.Height, q.win.Hide)
}

// slideQuick moves the panel, as big as r, from the top edge from to to,
// then runs done: at once where the desktop asks for less motion.
func (a *App) slideQuick(r mygo.Rectangle, from, to int, done func()) {
	q := &a.quick
	n := q.slides.Add(1)
	if mygo.Theme.Preferences().ReduceMotion {
		r.Y = to
		q.win.SetBounds(r)
		if done != nil {
			done()
		}
		return
	}
	r.Y = from
	q.win.SetBounds(r)
	start := time.Now()
	go func() {
		tick := time.NewTicker(time.Second / 120)
		defer tick.Stop()
		for range tick.C {
			if q.slides.Load() != n {
				return // another slide took over
			}
			p := min(float64(time.Since(start))/float64(quickSlide), 1)
			ease := 1 - (1-p)*(1-p)*(1-p)
			q.win.SetPosition(r.X, from+int(math.Round(float64(to-from)*ease)))
			if p == 1 {
				if done != nil {
					q.win.Update(func() {
						if q.slides.Load() == n {
							done()
						}
					})
				}
				return
			}
		}
	}()
}

// makeQuick makes the quick terminal's panel, hidden.
func (a *App) makeQuick() {
	q := &a.quick
	q.win = mygo.NewWindow(inspectable(mygo.WindowOptions{
		Title:         locales.ActionQuickTerminal(),
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		DisableResize: true,
		Width:         960,
		Height:        400,
		Content:       ui.View(a.quickView),
	}))
	// Closing it, as with ⌘W, hides it; quitting destroys it. A quit
	// closes every window, and a close this prevents cancels the quit, so
	// it only hides while the app is not quitting.
	q.win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.hideQuick()
	})
	// It goes as the user goes elsewhere, as Ghostty's does.
	q.win.OnBlur(a.hideQuick)
}

// attachQuick attaches the panel's terminal to the scratch pane of the
// session the window shows.
func (a *App) attachQuick() {
	q := &a.quick
	s := a.session(a.selected)
	p := scratchPane(s)
	if p == nil {
		a.dropQuick()
		return
	}
	if q.e != nil && q.pane == p.ID && q.e.err == "" {
		return
	}
	a.dropQuick()
	e := &termEntry{started: time.Now()}
	q.e, q.pane = e, p.ID
	id := p.ID
	dialer := a.hostOf(s.ID).dialer()
	post := func(fn func()) {
		if q.win != nil {
			q.win.Update(fn)
		}
	}
	go func() {
		stream, err := client.AttachVia(dialer, id)
		if err != nil {
			post(func() {
				if q.e == e {
					e.err = err.Error()
				}
			})
			return
		}
		g := &gated{Stream: stream, ready: make(chan struct{}), app: a}
		o := a.termOptions(id, g)
		o.ResizeByConn = true
		// No card under it: the terminal paints its background.
		o.Transparent = false
		o.Padding = terminal.Padding{X: 16, Y: 10}
		o.OnCopy = nil // the window's toast is not in sight
		term, err := terminal.New(o)
		if err != nil {
			stream.Close()
			post(func() {
				if q.e == e {
					e.err = err.Error()
				}
			})
			return
		}
		term.Resize(stream.Welcome.Cols, stream.Welcome.Rows)
		stream.OnResized = term.Resize
		close(g.ready)
		post(func() {
			if q.e != e {
				term.Close()
				stream.Close()
				return
			}
			e.term, e.stream = term, stream
			q.ref.Focus()
		})
		// The pane ended, as its shell exited: the panel goes up.
		<-term.Done()
		post(func() {
			if q.e == e {
				a.dropQuick()
				a.hidePanel()
			}
		})
	}()
}

// dropQuick lets go of the panel's terminal.
func (a *App) dropQuick() {
	q := &a.quick
	if q.e == nil {
		return
	}
	if q.e.term != nil {
		q.e.term.Close()
	}
	if q.e.stream != nil {
		q.e.stream.Close()
	}
	q.e, q.pane = nil, ""
}

// quickView is the panel's content: the terminal, or what keeps it.
func (a *App) quickView(c *ui.Context) {
	q := &a.quick
	a.looks()
	pal := paletteFor(c)
	c.Root().Background(lookFor(c).term.Background)
	switch {
	case q.e != nil && q.e.term != nil:
		terminal.View(c.Key("term"), q.e.term).Fill().Bind(&q.ref)
	case q.e != nil && q.e.err != "":
		a.message(c, pal, locales.PaneCantShow(), q.e.err, locales.CommonTryAgain(), func() {
			a.dropQuick()
			a.attachQuick()
		})
	case a.session(a.selected) == nil:
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, locales.ScratchNoSession()).FontSize(fsBody).TextColor(pal.textMuted)
		})
	default:
		ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c) })
	}
}

// quickDropH is the dropdown's height, of the panes' room, and the least
// it takes.
const (
	quickDropH   = 0.45
	quickDropMin = 180
)

// dropView is the scratch dropdown over the panes, h DIPs high: it slides
// down from the top of the card as it shows and back up as it hides. At
// rest while hidden it builds nothing. Its terminal is the main window's
// terminal of the scratch pane, as a pane's is.
func (a *App) dropView(c *ui.Context, pal *palette, stack ui.Element, h float32) {
	q := &a.quick
	p := scratchPane(a.session(q.session))
	a.followRoute(c, p)
	k := stack.AnimateTo("scratch", b2f(q.drop), motionBase)
	if k <= 0 {
		return
	}
	dh := max(h*quickDropH, quickDropMin)
	// It slides opaque, with its shadow, from under the card's top edge,
	// which clips it: no frame shows the panes through it. The clip leaves
	// room for the shadow, and takes no pointer itself.
	shadow := float32(48)
	clip := ui.Box(c.Key("scratch-clip")).Absolute().Left(-shadow).Top(0).Right(-shadow).Height(dh + shadow).Clip().PassThrough()
	clip.Children(func() {
		a.dropPanel(c, pal, p, k, dh, shadow)
	})
}

// dropPanel is the dropdown itself, k of the way down.
func (a *App) dropPanel(c *ui.Context, pal *palette, p *proto.Pane, k, dh, shadow float32) {
	q := &a.quick
	panel := floating(ui.Column(c.Key("scratch")).Absolute().Left(shadow).Top(0).Right(shadow).Height(dh).Radius(rPane).Clip().
		Background(lookFor(c).term.Background).AlignItems(ui.Stretch).Label(locales.ScratchTerminal()), pal)
	if k < 1 {
		// One cached bitmap moves, as the agents view does.
		panel.Cache().Translate(0, -(dh+shadow)*(1-k))
	}
	if !q.drop {
		panel.PassThrough()
	}
	panel.Children(func() {
		// A header as a pane's: what it is, and the key that hides it.
		ui.Row(c.Key("scratch-head")).Height(headerH).Shrink(0).AlignItems(ui.Center).Gap(8).Padding(0, 10, 0, 16).Children(func() {
			ui.Icon(c, terminalIcon).Size(14, 14).TextColor(pal.textMuted).Shrink(0)
			ui.Text(c, locales.ScratchTerminal()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).SingleLine()
			if p != nil && p.Dir != "" {
				ui.Text(c, proto.ShortDir(p.Dir)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
			}
			if keys := a.scratchKeys(); keys != "" {
				// A key cap and its word, as every other hint (council
				// round 3, visual n3).
				keyHint(c, pal, keys, locales.ScratchHide())
			}
			ui.Spacer(c)
			a.controlsRoom(c, 0)
		})
		ui.Column(c.Key("scratch-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			var e *termEntry
			if p != nil {
				a.ensureTerm(p.ID)
				e = a.terms[p.ID]
			}
			switch {
			case e != nil && e.term != nil:
				ref := a.paneRef(p.ID)
				terminal.View(c.Key("term-"+p.ID), e.term).Fill().Bind(ref)
				if q.focusDrop && q.drop {
					// The keyboard comes here in the first frame that
					// builds the view; until then the route takes the keys.
					q.focusDrop = false
					a.touch(p.ID)
					ref.Focus()
				}
			case e != nil && e.err != "":
				a.message(c, pal, locales.PaneCantShow(), e.err, "", nil)
			default:
				ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c) })
			}
		})
	})
}

// focusScratch gives the keyboard to the scratch while the dropdown shows,
// and reports whether it did: what gives the keyboard back to the pane the
// user is in, as a state that ends a workspace hold, gives it here then.
func (a *App) focusScratch() bool {
	q := &a.quick
	if !q.drop {
		return false
	}
	p := scratchPane(a.session(q.session))
	if p == nil {
		q.focusDrop = true // once its view is built
		return true
	}
	a.paneRef(p.ID).Focus()
	return true
}

// followRoute keeps the route of typed bytes in step: it names the
// scratch's stream once its terminal attaches, and ends once the terminal
// that should have the keyboard has it, or after a second at most.
func (a *App) followRoute(c *ui.Context, p *proto.Pane) {
	q := &a.quick
	on, keeping := q.route.active()
	if !on {
		return
	}
	switch {
	case time.Since(q.routeAt) > time.Second && !keeping, time.Since(q.routeAt) > 5*time.Second:
		// A scratch the daemon still starts keeps its bytes longer.
		q.route.off()
	case q.drop && p != nil:
		if keeping {
			if e := a.terms[p.ID]; e != nil && e.stream != nil {
				q.route.set(e.stream)
			}
		}
		if a.paneRef(p.ID).Focused(c) {
			q.route.off()
		}
	case !q.drop:
		if s := a.session(a.selected); s != nil {
			if id := a.focusOf(s); id != "" && a.paneRef(id).Focused(c) {
				q.route.off()
			}
		}
	}
	if on, _ := q.route.active(); on {
		c.After(16 * time.Millisecond)
	}
}

// scratchKeys are the keys that show and hide the scratch, as "Ctrl+B g",
// "" while the tuios keys are off or the key is not bound.
func (a *App) scratchKeys() string {
	tk := &a.tk
	if !a.keysOn() || tk.reg == nil {
		return ""
	}
	ks := tk.reg.GetKeys("toggle_scratch")
	if len(ks) == 0 {
		return ""
	}
	return chordLabel(tk.leader) + " " + ks[0]
}

// quickTermFor returns the panel's terminal when it shows a pane, else nil.
func (a *App) quickTermFor(id string) *terminal.Terminal {
	if q := &a.quick; q.pane == id && q.e != nil {
		return q.e.term
	}
	return nil
}

// termFor returns the terminal of a pane, the window's or the panel's,
// else nil.
func (a *App) termFor(id string) *terminal.Terminal {
	if e := a.terms[id]; e != nil && e.term != nil {
		return e.term
	}
	return a.quickTermFor(id)
}
