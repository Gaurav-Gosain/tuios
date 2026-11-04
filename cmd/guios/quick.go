package main

import (
	"log"
	"math"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The quick terminal drops down from the top of the screen as its global
// shortcut is pressed, over every app, full-screen ones included, and goes
// back up as it is pressed again or as the user goes elsewhere, as
// Ghostty's quick terminal and Quake's console do. It shows a session of
// this machine's kerod, as the window does, so that it keeps running as it
// hides, as Kero quits and as it updates; the sidebar lists it with the
// others.

const (
	quickAction = "quickTerminal.toggle"
	quickHeight = 0.4 // of the work area of the display with the pointer
	quickSlide  = 180 * time.Millisecond
)

// quickTerm is the quick terminal's state, on the main thread.
type quickTerm struct {
	win      *mygo.Window // the panel, made as it first shows
	shown    bool         // shown, or sliding down
	pane     string       // the pane its terminal shows
	e        *termEntry   // that terminal, attached
	ref      ui.Handle
	creating bool // its session is being made
	err      string
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

// quickSession returns the session the quick terminal shows, nil while it
// has none: kerod lost it, or the user closed it.
func (a *App) quickSession() *proto.Session {
	s := a.session(a.prefs.QuickSession)
	if s == nil || a.hostOf(s.ID) != a.local() {
		return nil
	}
	return s
}

// showQuick drops the quick terminal down on the display with the pointer,
// starting its session first if it has none.
func (a *App) showQuick() {
	q := &a.quick
	if q.win == nil {
		a.makeQuick()
	}
	q.err = ""
	a.attachQuick()
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
	r := mygo.Rectangle{X: wa.X, Y: wa.Y, Width: wa.Width, Height: int(float64(wa.Height) * quickHeight)}
	q.shown = true
	q.ref.Focus()
	a.slideQuick(r, wa.Y-r.Height, wa.Y, nil)
	q.win.Show()
	q.win.Focus()
	a.reportView()
}

// hideQuick slides the quick terminal back up, which gives the keyboard
// back to the app the user was in.
func (a *App) hideQuick() {
	q := &a.quick
	if !q.shown {
		return
	}
	q.shown = false
	r := q.win.Bounds()
	a.slideQuick(r, r.Y, r.Y-r.Height, q.win.Hide)
	a.reportView()
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
			ease := 1 - math.Pow(1-p, 3)
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
	q.win = mygo.NewWindow(mygo.WindowOptions{
		Title:         locales.ActionQuickTerminal(),
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		DisableResize: true,
		Width:         960,
		Height:        400,
		Content:       ui.View(a.quickView),
	})
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

// attachQuick attaches the quick terminal to its session's pane, the one
// the user was in, making the session first if there is none.
func (a *App) attachQuick() {
	q := &a.quick
	h := a.local()
	s := a.quickSession()
	if s == nil {
		a.dropQuick()
		// Until kerod tells its sessions, the one of the last run may be
		// among them: the panel waits, and attaches as they come.
		if q.creating || h.cl == nil || h.conn != connReady || !h.loaded {
			return
		}
		q.creating = true
		cl := h.cl
		go func() {
			var s proto.Session
			err := cl.Call("create", proto.CreateParams{Dir: proto.Home(), Name: locales.ActionQuickTerminal()}, &s)
			q.win.Update(func() {
				q.creating = false
				if err != nil {
					q.err = err.Error()
					return
				}
				a.addPending(h, s)
				a.prefs.QuickSession = s.ID
				a.savePrefs()
				a.attachQuick()
				a.update(func() {}) // the sidebar shows it
			})
		}()
		return
	}
	p := s.FocusPane()
	if p == nil || p.Browser() {
		return
	}
	if q.e != nil && q.pane == p.ID && q.e.err == "" {
		return
	}
	a.dropQuick()
	e := &termEntry{started: time.Now()}
	q.e, q.pane = e, p.ID
	id := p.ID
	dialer := h.dialer()
	go func() {
		stream, err := client.AttachVia(dialer, id)
		if err != nil {
			q.win.Update(func() {
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
			q.win.Update(func() {
				if q.e == e {
					e.err = err.Error()
				}
			})
			return
		}
		term.Resize(stream.Welcome.Cols, stream.Welcome.Rows)
		stream.OnResized = term.Resize
		close(g.ready)
		q.win.Update(func() {
			if q.e != e {
				term.Close()
				stream.Close()
				return
			}
			e.term, e.stream = term, stream
			// It takes the panel's size, which another window may have
			// given another.
			go stream.Claim()
			q.ref.Focus()
		})
		// The pane ended, as its shell exited: the next show starts
		// another, or shows the session's next pane.
		<-term.Done()
		q.win.Update(func() {
			if q.e == e {
				a.dropQuick()
				a.hideQuick()
			}
		})
	}()
}

// dropQuick lets go of the quick terminal's terminal.
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

// quickChanged follows a state of kerod: the quick terminal attaches
// again when its pane is gone, as to the session's next one.
func (a *App) quickChanged() {
	q := &a.quick
	if q.e == nil {
		if q.shown && q.err == "" {
			a.attachQuick()
		}
		return
	}
	s := a.quickSession()
	if s == nil || s.PaneByID(q.pane) == nil {
		a.dropQuick()
		if q.shown {
			a.attachQuick()
		}
	}
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
	case q.err != "" || q.e != nil && q.e.err != "":
		detail := q.err
		if q.e != nil {
			detail = q.e.err
		}
		a.message(c, pal, locales.PaneCantShow(), detail, locales.CommonTryAgain(), func() {
			q.err = ""
			a.dropQuick()
			a.attachQuick()
		})
	default:
		ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c) })
	}
}

// quickTermFor returns the quick terminal's terminal when it shows a pane,
// else nil.
func (a *App) quickTermFor(id string) *terminal.Terminal {
	if q := &a.quick; q.pane == id && q.e != nil {
		return q.e.term
	}
	return nil
}

// termFor returns the terminal of a pane, the window's or the quick
// terminal's, else nil.
func (a *App) termFor(id string) *terminal.Terminal {
	if e := a.terms[id]; e != nil && e.term != nil {
		return e.term
	}
	return a.quickTermFor(id)
}
