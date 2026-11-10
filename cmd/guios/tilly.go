package main

import (
	_ "embed"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// Tilly is tuios's mascot: a lilac CRT whose screen is a tiled layout
// (~/dev/tuios/assets/brand/mark.svg). guios draws her in its empty
// states. Her screen shows the layout of the session she stands for, as
// tiny tiles, and a tile whose pane needs the person is amber (`need`).
// She does not move while nothing changes. When a pane newly needs the
// person, its tile blinks twice in steps of 250 ms, and not at all when
// the desktop asks for less motion.

//go:embed brand/tilly.svg
var tillyFullSVG []byte

//go:embed brand/tilly-body.svg
var tillyBodySVG []byte

var (
	// tillyFull is the mark as it is, for no session: her own face on
	// her screen.
	tillyFull = ui.MustParseSVG(tillyFullSVG)
	// tillyBody is the mark with an empty screen, which tillyScreen fills.
	tillyBody = ui.MustParseSVG(tillyBodySVG)
)

// The screen's tile area in the mark's 512 box: the mark's inner units
// are 3.5 to the box's, its screen's tiles at (30, 48) to (90, 92) of
// them, offset by (46, 32).
const (
	tillyUnit  = 3.5 / 512
	tillyAreaX = (46 + 3.5*30) / 512
	tillyAreaY = (32 + 3.5*48) / 512
	tillyAreaW = 3.5 * 60 / 512
	tillyAreaH = 3.5 * 44 / 512
	tillyStep  = 250 * time.Millisecond
)

// tillyState is what Tilly saw last: the panes that needed the person,
// and when one began to, for its blink.
type tillyState struct {
	seen    bool
	waiting map[string]bool
	blinkAt time.Time
	blinkID string
}

// tillyTile is one tile of her screen.
type tillyTile struct {
	r      frac
	focus  bool
	needs  bool
	paneID string
}

// tilly draws Tilly at size (a square, in DIPs) for the session s, or with
// her own face for none.
func (a *App) tilly(c *ui.Context, pal *palette, key string, size float32, s *proto.Session) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size).Shrink(0)
	if s == nil {
		box.Children(func() { ui.Image(c, tillyFull).Size(size, size) })
		return box
	}
	tiles := tillyTiles(s)
	now := c.Now()
	a.tillyNotice(c, tiles, now)
	blinkID, since := a.tillyS.blinkID, now.Sub(a.tillyS.blinkAt)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Image(tillyBody, r, ui.Contain)
		u := r.W * tillyUnit
		area := ui.Rect{X: r.X + r.W*tillyAreaX, Y: r.Y + r.H*tillyAreaY, W: r.W * tillyAreaW, H: r.H * tillyAreaH}
		gap := max(3*u, 1)
		radius := max(4*u, 1)
		line := max(1.6*u, 0.75)
		for _, t := range tiles {
			tr := ui.Rect{X: area.X + float32(t.r.x)*(area.W+gap), Y: area.Y + float32(t.r.y)*(area.H+gap),
				W: float32(t.r.w)*(area.W+gap) - gap, H: float32(t.r.h)*(area.H+gap) - gap}
			if tr.W <= 0 || tr.H <= 0 {
				continue
			}
			lit := t.needs
			if t.needs && t.paneID == blinkID && since < 3*tillyStep {
				// Off, on, off in steps of 250 ms, then on for good.
				lit = int(since/tillyStep)%2 == 1
				p.After(tillyStep - since%tillyStep)
			}
			switch {
			case lit:
				p.Fill(tr, pal.attention, radius)
			default:
				p.Fill(tr, tillyTileFill, radius)
				edge, w := tillyTileEdge, line
				if t.focus {
					edge, w = tillyFocusEdge, line*1.25
				}
				p.Stroke(tr, edge, radius, w)
			}
		}
	})
	box.Label(s.Name)
	return box
}

// tillyNotice keeps which tiles need the person, and starts a blink for
// one that began to since the last frame.
func (a *App) tillyNotice(c *ui.Context, tiles []tillyTile, now time.Time) {
	st := &a.tillyS
	waiting := map[string]bool{}
	for _, t := range tiles {
		if t.needs {
			waiting[t.paneID] = true
			if st.seen && !st.waiting[t.paneID] && !c.Preferences().ReduceMotion && !c.Instant() {
				st.blinkAt, st.blinkID = now, t.paneID
			}
		}
	}
	st.seen, st.waiting = true, waiting
}

// tillyTiles is the tiles of s's screen: its split tree's panes where it
// has one, else its panes side by side, and the zoomed pane alone.
func tillyTiles(s *proto.Session) []tillyTile {
	byID := map[string]*proto.Pane{}
	for i := range s.Panes {
		byID[s.Panes[i].ID] = &s.Panes[i]
	}
	rects := map[string]frac{}
	switch {
	case s.Zoomed != "" && byID[s.Zoomed] != nil:
		rects[s.Zoomed] = frac{0, 0, 1, 1}
	case s.Layout != nil:
		var splits []fracSplit
		layoutFracs(s.Layout, frac{0, 0, 1, 1}, "", func(string) bool { return false }, rects, map[string]frac{}, &splits)
	default:
		n := float64(len(s.Panes))
		for i, p := range s.Panes {
			rects[p.ID] = frac{float64(i) / n, 0, 1 / n, 1}
		}
	}
	var out []tillyTile
	for _, p := range s.Panes {
		r, ok := rects[p.ID]
		if !ok {
			continue
		}
		out = append(out, tillyTile{r: r, focus: p.ID == s.Focus, needs: p.Status == proto.Waiting, paneID: p.ID})
	}
	return out
}
