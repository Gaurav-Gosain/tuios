package main

import (
	"math"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// A workspace switch slides: the panes of the workspace left go out to one
// side as those of the one shown come in from the other, toward the left
// for a higher number, on motionLayout's curve.
//
// A switch to the workspace last left starts in the frame of the key: its
// panes come in as they last showed (wsLeft keeps them and their
// terminals), and the daemon's answer puts the live panes in their place
// on the way. A switch to another workspace waits for the daemon: the hold
// keeps the last frame until its panes have attached (workspaces.go), and
// the slide starts as they show.
//
// The panes not live are drawn as they were, without their buttons, as
// one cached bitmap clipped to the panes' area: they take no input, never
// draw over the sidebar, and never resize their panes (gated.frozen).

// wsSlide is a workspace switch on its way.
type wsSlide struct {
	from  proto.Session // the session as it showed before the switch
	to    int           // the workspace shown
	dir   float32       // 1 when the panes go left
	w, h  float32       // the panes' area as it showed, in DIPs
	since time.Time     // when the switch came
	shown time.Time     // when the slide began
	// incoming is the workspace asked for, as it last showed, while the
	// daemon has not answered: nil for a slide that waited for it.
	incoming *proto.Session
}

// slideLead is how far a slide is on its way in its first frame: half a
// frame, as the key came between frames.
const slideLead = 8 * time.Millisecond

// leaveWorkspace keeps old, the workspace the session leaves, and its
// terminals, so that a switch back to it can start at once.
func (a *App) leaveWorkspace(old proto.Session) {
	if len(old.Panes) == 0 {
		return
	}
	if a.wsLeft == nil {
		a.wsLeft = map[string]proto.Session{}
	}
	a.wsLeft[old.ID] = old
}

// slideNow starts the switch of s to workspace n in this frame, if the
// session left n last: its panes come in as they showed.
func (a *App) slideNow(s *proto.Session, n int) {
	left, ok := a.wsLeft[s.ID]
	if !ok || left.Workspace != n || a.calm || len(s.Panes) == 0 || a.area.W <= 0 {
		return
	}
	if a.slides == nil {
		a.slides = map[string]*wsSlide{}
	}
	dir := float32(1)
	if n < s.Workspace {
		dir = -1
	}
	from := *s
	from.Panes = slices.Clone(s.Panes)
	from.Layout = s.Layout.Clone()
	now := time.Now()
	a.slides[s.ID] = &wsSlide{from: from, to: n, dir: dir, w: a.area.W + cardGap, h: a.area.H, since: now,
		shown: now.Add(-slideLead), incoming: &left}
	holdLeases(motionLayout.Duration)
}

// startSlide notes a switch of old's session to workspace to.
func (a *App) startSlide(old proto.Session, to int) {
	if a.calm || len(old.Panes) == 0 {
		return
	}
	if sl := a.slides[old.ID]; sl != nil && sl.to == to && sl.incoming != nil {
		// It began with the key: the daemon's answer goes on with it.
		return
	}
	if a.slides == nil {
		a.slides = map[string]*wsSlide{}
	}
	dir := float32(1)
	if to < old.Workspace {
		dir = -1
	}
	// The area as the last frame laid it out, of the workspace left.
	a.slides[old.ID] = &wsSlide{from: old, to: to, dir: dir, w: a.area.W + cardGap, h: a.area.H, since: time.Now()}
}

// slideOf returns the slide of session s this frame shows, and how far it
// is, 0 to 1; nil while the old workspace still shows, and once it is
// over.
func (a *App) slideOf(c *ui.Context, s *proto.Session) (*wsSlide, float32) {
	sl := a.slides[s.ID]
	if sl == nil {
		return nil, 1
	}
	if s.Workspace != sl.to && sl.incoming == nil {
		// The hold shows the workspace left.
		if time.Since(sl.since) > holdMax+time.Second {
			delete(a.slides, s.ID) // another switch came first
		}
		return nil, 1
	}
	if sl.incoming != nil && s.Workspace != sl.to && time.Since(sl.since) > holdMax {
		// The daemon never answered: the panes shown stay.
		delete(a.slides, s.ID)
		return nil, 1
	}
	if sl.w <= cardGap || c.Preferences().ReduceMotion || c.Instant() {
		delete(a.slides, s.ID)
		return nil, 1
	}
	if sl.shown.IsZero() {
		sl.shown = c.Now()
		// The panes keep their grids while they slide.
		holdLeases(motionLayout.Duration)
	}
	t := float32(c.Now().Sub(sl.shown)) / float32(motionLayout.Duration)
	// Done once the panes are within half a DIP of their place: no frame
	// after the last that moves them.
	k := easeOut(min(max(t, 0), 1))
	if (t >= 1 || (1-k)*sl.w < 0.5) && s.Workspace == sl.to {
		delete(a.slides, s.ID)
		return nil, 1
	}
	if t >= 1 {
		// There, before the daemon's answer: the panes wait in place.
		return sl, 1
	}
	c.AnimationFrame()
	return sl, k
}

// frozenPanes draws the panes of s as they last showed, at x across the
// area, w x h DIPs with the gap after it: no buttons, no input, one cached
// bitmap, and no size of theirs goes to the daemon.
func (a *App) frozenPanes(c *ui.Context, pal *palette, s *proto.Session, key string, x, w, h float32) {
	out := ui.Box(c.Key(key)).Absolute().Left(0).Top(0).Width(w-cardGap).Height(h).
		Translate(x, 0).Cache().PassThrough()
	ew, eh := float64(w), float64(h)+paneGap
	rects := map[string]frac{}
	whole := frac{0, 0, 1, 1}
	switch z := s.PaneByID(s.Zoomed); {
	case z != nil:
		rects[z.ID] = whole
	case s.Arrange != nil && len(s.Arrange.Rects) > 0:
		box := s.Arrange.Box
		bw, bh := float64(max(box.W, 1)), float64(max(box.H, 1))
		for id, r := range s.Arrange.Rects {
			rects[id] = frac{float64(r.X-box.X) / bw, float64(r.Y-box.Y) / bh, float64(r.W) / bw, float64(r.H) / bh}
		}
	case s.Layout != nil && len(s.Panes) > 1:
		var splits []fracSplit
		layoutFracs(s.Layout, whole, "", func(string) bool { return false }, rects, map[string]frac{}, &splits)
	default:
		if p := s.FocusPane(); p != nil {
			rects[p.ID] = whole
		}
	}
	out.Children(func() {
		for i := range s.Panes {
			p := &s.Panes[i]
			r, ok := rects[p.ID]
			if !ok {
				continue
			}
			x0, y0 := math.Round(r.x*ew), math.Round(r.y*eh)
			x1, y1 := math.Round((r.x+r.w)*ew-paneGap), math.Round((r.y+r.h)*eh-paneGap)
			card := ui.Column(c.Key("out-" + p.ID)).Absolute().Left(float32(x0)).Top(float32(y0)).
				Width(float32(max(x1-x0, 0))).Height(float32(max(y1-y0, 0))).Radius(rPane).Clip().Background(pal.card)
			paned(card, pal).Children(func() {
				ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(sp2).Children(func() {
					ui.Icon(c, programIcon(p)).Size(14, 14).TextColor(pal.textFaint)
					oneLine(c, p.PaneName(), fsBody, wSemibold).TextColor(pal.text)
				})
				if e := a.terms[p.ID]; e != nil && e.term != nil {
					if auditPane != nil {
						auditPane(p.ID, "copy", e.term)
					}
					ui.Box(c).Grow(1).MinHeight(0).Children(func() {
						terminal.View(c.Key("term"), e.term).Fill()
					})
				}
			})
		}
	})
}
