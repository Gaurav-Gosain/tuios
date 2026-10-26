// Package tmldraw draws a TML document into text the way an attached client
// draws a surface: with loom, the chrome palette mapped onto loom's roles,
// and the margin a TML window keeps around its document.
//
// The client draws a live surface with it, the daemon draws a surface for
// capture-pane, and tuios tml render draws a file. All three share these
// functions, so what a program reads back is what the person sees.
package tmldraw

import (
	"cmp"
	"encoding/json"
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/loom"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Inset is where a surface sits in a TML window's content box of cw by ch
// cells: inside a margin, two cells at the sides and a row above and below
// when the box has the room for it. A small box keeps its rows and cells for
// the document. A popup and a pane have the same margin, so a document looks
// the same in both.
func Inset(cw, ch int) (dx, dy, iw, ih int) {
	cw, ch = max(cw, 1), max(ch, 1)
	switch {
	case cw >= 40:
		dx = 2
	case cw >= 16:
		dx = 1
	}
	if ch >= 12 {
		dy = 1
	}
	return dx, dy, cw - 2*dx, ch - 2*dy
}

// Pad puts the surface's lines into the content box with the margin of
// Inset, on the pane's own ground.
func Pad(body string, dx, dy, cw, ch int) string {
	if dx == 0 && dy == 0 {
		return body
	}
	side := strings.Repeat(" ", dx)
	blank := strings.Repeat(" ", cw)
	lines := strings.Split(body, "\n")
	out := make([]string, 0, ch)
	for range dy {
		out = append(out, blank)
	}
	inner := cw - 2*dx
	for _, l := range lines {
		if len(out) >= ch-dy {
			break
		}
		if w := ansi.StringWidth(l); w < inner {
			l += strings.Repeat(" ", inner-w)
		}
		out = append(out, side+l+side)
	}
	for len(out) < ch {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

// Palette maps a chrome palette onto loom's roles, so a surface looks like
// the rest of tuios and keeps its contrast floors. At 16 colours the grounds
// are the terminal's own, so they are left out and loom draws the cursor row
// and a primary button in reverse video. ascii picks loom's ASCII glyphs.
func Palette(pal overlay.Palette, ascii bool) *loom.Palette {
	depth := loom.TrueColor
	switch pal.Depth {
	case overlay.Depth256:
		depth = loom.Color256
	case overlay.Depth16:
		depth = loom.Color16
	}
	if ascii {
		depth = loom.ASCII
	}
	roles := map[string]color.Color{
		"fg": pal.Fg, "dim": pal.FgDim, "mute": pal.FgMute,
		"accent": pal.Accent, "accent-bright": pal.AccentBright,
		"success": pal.Success, "warning": pal.Warning, "error": pal.Warn, "info": pal.Info,
		"attention": pal.Attention, "busy": pal.Busy, "edge": pal.Edge,
		"canvas": pal.Canvas, "panel": pal.Panel, "surface": pal.Surface, "card": pal.Card,
		"accent-tint": pal.AccentTint, "success-tint": pal.SuccessTint,
		"warning-tint": pal.WarningTint, "error-tint": pal.WarnTint,
		loom.RoleCursor: pal.RowSel, loom.RolePill: pal.PillFg,
	}
	maps.Copy(roles, chartRoles(pal))
	for k, c := range roles {
		if c == nil {
			delete(roles, k)
			continue
		}
		if _, none := c.(lipgloss.NoColor); none {
			delete(roles, k)
		}
	}
	return loom.NewPalette(depth, roles)
}

// chartRoles gives the roles plot draws with that the chrome palette has no
// name for: track, the empty part of a gauge and the land of a map, and the
// heat ramp of a heatmap and a map's shades. Left to loom's fallbacks, heat2
// and heat3 are both info, and at 16 colours the land, heat1 and three of
// the shades share a slot, so a map's six tones read as three. Here all six
// differ at every depth: at 16 colours by hue, above it as a ramp from the
// panel through info to the bright accent.
func chartRoles(pal overlay.Palette) map[string]color.Color {
	if pal.Depth == overlay.Depth16 {
		return map[string]color.Color{
			loom.RoleTrack: overlay.Slot(8),
			// Five hues, so no two shades can be the same colour in any
			// palette: blue, cyan, green, yellow and bright magenta. Slot
			// 12 is the accent and info, and 4 and 12 look alike in many
			// palettes.
			loom.RoleHeat1: overlay.Slot(4), loom.RoleHeat2: overlay.Slot(6),
			loom.RoleHeat3: overlay.Slot(2), loom.RoleHeat4: overlay.Slot(3),
			loom.RoleHeat5: overlay.Slot(13),
		}
	}
	if pal.Panel == nil || pal.Info == nil || pal.AccentBright == nil || pal.FgMute == nil {
		return nil
	}
	out := map[string]color.Color{
		loom.RoleTrack: overlay.MixColors(pal.Panel, pal.FgMute, 0.35),
		loom.RoleHeat1: overlay.MixColors(pal.Panel, pal.Info, 0.35),
		loom.RoleHeat2: overlay.MixColors(pal.Panel, pal.Info, 0.65),
		loom.RoleHeat3: pal.Info,
		loom.RoleHeat4: overlay.MixColors(pal.Info, pal.AccentBright, 0.5),
		loom.RoleHeat5: pal.AccentBright,
	}
	if pal.Depth == overlay.Depth256 {
		for k, c := range out {
			out[k] = overlay.To256(c)
		}
	}
	return out
}

// Options says how Draw draws a document.
type Options struct {
	// Width and Height are the box in cells. With Inset they are the
	// window's content box, and the document gets the box less the margin.
	Width, Height int
	// Inset keeps the margin a TML window keeps around its document.
	Inset bool
	// Placement is where the surface is shown, "pane" or "popup". A
	// document reads it as view.placement.
	Placement string
	// State replaces the document's state, as the daemon's store holds it.
	// Nil keeps the state the document carries.
	State json.RawMessage
	// View is a client's view state: the open tabs, cursors, scroll
	// places, sorts and folds. Nil draws the view a new client has.
	View *loom.View
	// Tab opens the tab with this id, in whichever tabs holds it.
	Tab string
	// Palette gives the colours and glyphs. Nil means loom's default.
	Palette *loom.Palette
	// Styled keeps the SGR styling. Otherwise the text is plain, and each
	// line loses the spaces at its end.
	Styled bool
}

// Draw draws doc as text, o.Width by o.Height cells.
func Draw(doc *tml.Node, o Options) (string, error) {
	doc = doc.Clone()
	if len(o.State) > 0 {
		doc.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: o.State})
	}
	lim := tml.PersonLimits()
	s, err := loom.New(doc, &loom.Options{Palette: o.Palette, Ground: loom.GroundNone, Limits: &lim, Placement: o.Placement})
	if err != nil {
		return "", err
	}
	view := o.View
	if o.Tab != "" {
		if view, err = openTab(s, doc, view, o.Tab); err != nil {
			return "", err
		}
	}
	if view != nil {
		s.SetView(view)
	}
	w, h := max(o.Width, 1), max(o.Height, 1)
	dx, dy, iw, ih := 0, 0, w, h
	if o.Inset {
		dx, dy, iw, ih = Inset(w, h)
		iw, ih = max(iw, 1), max(ih, 1)
	}
	buf := s.Render(iw, ih)
	var body string
	if o.Styled {
		body = buf.Render()
	} else {
		body = buf.String()
	}
	out := Pad(body, dx, dy, w, h)
	if !o.Styled {
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " ")
		}
		out = strings.Join(lines, "\n")
	}
	return out, nil
}

// openTab opens the tab with the given id: through the state when its tabs
// bind a state path, as a click does, else in a copy of view, which it
// returns.
func openTab(s *loom.Surface, doc *tml.Node, view *loom.View, id string) (*loom.View, error) {
	var ids []string
	var tabs *tml.Node
	key := ""
	doc.Walk(func(node *tml.Node, ptr string) bool {
		if tabs != nil || node.IsText() || node.Type != "tabs" {
			return tabs == nil
		}
		for _, c := range node.Children {
			if c.IsText() || c.Type != "tab" {
				continue
			}
			ids = append(ids, c.ID())
			if c.ID() == id {
				tabs, key = node, cmp.Or(node.ID(), ptr)
			}
		}
		return true
	})
	if tabs == nil {
		if len(ids) == 0 {
			return nil, fmt.Errorf("the document has no tabs, so it has no tab %q", id)
		}
		slices.Sort(ids)
		return nil, fmt.Errorf("the document has no tab %q. The tabs are: %s", id, strings.Join(slices.Compact(ids), ", "))
	}
	if path := tabs.AttrString("value"); path != "" {
		if err := s.Set(path, id); err != nil {
			return nil, fmt.Errorf("open tab %q: %w", id, err)
		}
		return view, nil
	}
	if view != nil {
		// SetView copies it, so the caller's view stays as it was.
		s.SetView(view)
	}
	view = s.View()
	view.Tabs[key] = id
	return view, nil
}
