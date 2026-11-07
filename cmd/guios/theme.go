package main

import (
	"sync/atomic"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/roles"
)

// palette is the chrome's colours for one theme in one appearance: the
// roles internal/guios/roles makes, as MyGo colours (fromRoles).
type palette struct {
	dark bool

	sidebar      ui.Color // under the sidebar where no material shows
	text         ui.Color
	textMuted    ui.Color // project headers, times
	textFaint    ui.Color // counts, hints
	rowHover     ui.Color
	rowSelected  ui.Color
	rowSelBorder ui.Color
	rowShadow    ui.Color

	card        ui.Color // the terminal's card
	cardBorder  ui.Color
	cardShadow  ui.Color
	cardShadow2 ui.Color
	floatShadow ui.Color
	cardDim     ui.Color // a pane the user is not in
	// edge and edgeHigh are the hairlines around surfaces in the dark:
	// cards and rows, and what floats or takes a click.
	edge     ui.Color
	edgeHigh ui.Color
	// veil fades a pane the user is not in, its text too, as Ghostty's
	// unfocused-split-opacity.
	veil     ui.Color
	buttonBg ui.Color
	// Controls' faces: buttons, fields, selects, a segmented control's
	// choice. Light ones stand on a soft shadow; dark ones on their
	// lighter fill alone, with a hint of light along their top, as macOS
	// draws them.
	control       ui.Color
	controlHover  ui.Color
	controlShadow bool
	controlLight  ui.Color
	divider       ui.Color
	chip          ui.Color

	attention ui.Color // needs you: marks, edges, counts' pills
	attnSoft  ui.Color
	// focusLine is the focused card's edge (line.focus): the accent at
	// 80 % in the light, 70 % in the dark.
	focusLine ui.Color
	// attnText is needs-you as text: the amber made deep enough (light) or
	// bright enough (dark) to read at 4.5:1 on every surface it sits on,
	// its soft chip fill included. The bright amber stays for marks.
	attnText ui.Color
	// attnChip is needs-you as text on its own soft fill, the pane
	// header's chip, at 5:1 or more on an unfocused card and 4.5:1 on the
	// focused one (council round 3, visual M1).
	attnChip ui.Color
	done     ui.Color // the filled check
	onDone   ui.Color
	danger   ui.Color
	// dangerText is the danger color as text, at 4.5:1 on every surface;
	// dangerFill is a destructive button's fill, under white text at 4.5:1.
	dangerText ui.Color
	dangerFill ui.Color
	accent     ui.Color
	accentText ui.Color // the accent as text on its own 16 % chip
	// accentFill is the accent as a control's fill (the primary button,
	// the selected item of a menu, a selected chip), and accentInk the
	// text on it: dark ink or white, whichever reads, at 5:1 or more.
	// A fill neither reaches it on goes deeper until white does (council
	// round 5, visual M1). attnFill and attnInk are the same for a
	// needs-you count's badge.
	accentFill, accentInk ui.Color
	attnFill, attnInk     ui.Color
	// work marks what works: an agent's turn, a program's progress. The
	// accent means only what is selected or primary (DESIGN.md section 1).
	work ui.Color

	overlay   ui.Color // behind dialogs
	popover   ui.Color
	kbd       ui.Color
	kbdBorder ui.Color
}

// fromRoles is the roles as the views take them.
func fromRoles(r *roles.Palette) *palette {
	return &palette{
		dark:          r.Dark,
		sidebar:       toUI(r.Sidebar),
		text:          toUI(r.Text),
		textMuted:     toUI(r.TextMuted),
		textFaint:     toUI(r.TextFaint),
		rowHover:      toUI(r.RowHover),
		rowSelected:   toUI(r.RowSelected),
		rowSelBorder:  toUI(r.RowSelBorder),
		rowShadow:     toUI(r.RowShadow),
		card:          toUI(r.Card),
		cardBorder:    toUI(r.CardBorder),
		cardShadow:    toUI(r.CardShadow),
		cardShadow2:   toUI(r.CardShadow2),
		floatShadow:   toUI(r.FloatShadow),
		cardDim:       toUI(r.CardDim),
		edge:          toUI(r.Edge),
		edgeHigh:      toUI(r.EdgeHigh),
		veil:          toUI(r.Veil),
		buttonBg:      toUI(r.ButtonBg),
		control:       toUI(r.Control),
		controlHover:  toUI(r.ControlHover),
		controlShadow: r.ControlShadow,
		controlLight:  toUI(r.ControlLight),
		divider:       toUI(r.Divider),
		chip:          toUI(r.Chip),
		attention:     toUI(r.Attention),
		attnSoft:      toUI(r.AttnSoft),
		focusLine:     toUI(r.FocusLine),
		attnText:      toUI(r.AttnText),
		attnChip:      toUI(r.AttnChip),
		done:          toUI(r.Done),
		onDone:        toUI(r.OnDone),
		danger:        toUI(r.Danger),
		dangerText:    toUI(r.DangerText),
		dangerFill:    toUI(r.DangerFill),
		accent:        toUI(r.Accent),
		accentText:    toUI(r.AccentText),
		accentFill:    toUI(r.AccentFill),
		accentInk:     toUI(r.AccentInk),
		attnFill:      toUI(r.AttnFill),
		attnInk:       toUI(r.AttnInk),
		work:          toUI(r.Work),
		overlay:       toUI(r.Overlay),
		popover:       toUI(r.Popover),
		kbd:           toUI(r.Kbd),
		kbdBorder:     toUI(r.KbdBorder),
	}
}

// toUI is a roles colour as a MyGo one.
func toUI(c roles.Color) ui.Color { return ui.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

// fromUI is a MyGo colour as a roles one: its sRGB part.
func fromUI(c ui.Color) roles.Color { return roles.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

// toTerm is a roles terminal theme as MyGo's.
func toTerm(t *roles.Term) *terminal.Theme {
	th := &terminal.Theme{Foreground: toUI(t.Foreground), Background: toUI(t.Background), Cursor: toUI(t.Cursor),
		CursorText: toUI(t.CursorText), Selection: toUI(t.Selection), SelectionText: toUI(t.SelectionText)}
	for i, c := range t.Palette {
		th.Palette[i] = toUI(c)
	}
	return th
}

// fromTerm is MyGo's terminal theme as a roles one.
func fromTerm(th *terminal.Theme) *roles.Term {
	t := &roles.Term{Foreground: fromUI(th.Foreground), Background: fromUI(th.Background), Cursor: fromUI(th.Cursor),
		CursorText: fromUI(th.CursorText), Selection: fromUI(th.Selection), SelectionText: fromUI(th.SelectionText)}
	for i, c := range th.Palette {
		t.Palette[i] = fromUI(c)
	}
	return t
}

// contrast is the WCAG contrast ratio of two colours.
func contrast(a, b ui.Color) float64 { return roles.Contrast(fromUI(a), fromUI(b)) }

// awayFrom is a fill moved by f away from its ink (roles.AwayFrom).
func awayFrom(fill, ink ui.Color, f float32) ui.Color {
	return toUI(roles.AwayFrom(fromUI(fill), fromUI(ink), f))
}

// paletteFor is the colors of the window c draws, from its theme.
func paletteFor(c *ui.Context) *palette { return lookFor(c).pal }

// backdrop paints the window's wash of color, under the sidebar and
// between the cards: the base on the root, a glow from the top right on
// row, which holds everything else. It lies over the window's material,
// which shows through a little.
func backdrop(c *ui.Context, row ui.Element) {
	l := lookFor(c)
	base := l.base
	if c.Vibrancy() {
		stops := make([]ui.GradientStop, len(base.Stops))
		for i, st := range base.Stops {
			stops[i] = ui.GradientStop{Color: st.Color.Alpha(0.95), At: st.At}
		}
		base.Stops = stops
	}
	c.Root().LinearGradient(base)
	row.LinearGradient(l.glow)
}

// A look is a theme in one appearance, which colors the whole window: a
// terminal theme, whose background the panes' cards take and whose
// foreground their text, with an accent and the wash under the cards: a
// tuios theme washes the window in its own background (ghosttyLook).
type look struct {
	term       *terminal.Theme
	pal        *palette
	base, glow ui.LinearGradient
}

// looks are the window's in the light and in the dark, as the settings
// key names them.
type looks struct {
	key         string
	light, dark *look
}

var currentLooks atomic.Pointer[looks]

// looks returns the window's looks, as the settings choose them: the
// tuios theme of each appearance (themeKey).
func (a *App) looks() *looks {
	key := a.themeKey()
	if l := currentLooks.Load(); l != nil && l.key == key {
		return l
	}
	l := &looks{key: key, light: lookNamed(a.set.LightTheme, false), dark: lookNamed(a.set.DarkTheme, true)}
	currentLooks.Store(l)
	return l
}

// lookFor is the look of the window c draws, by its appearance.
func lookFor(c *ui.Context) *look {
	dark := c.Theme().Dark
	l := currentLooks.Load()
	switch {
	case l == nil:
		return ownLook(dark)
	case dark:
		return l.dark
	}
	return l.light
}

// lookNamed is the look of a guios theme setting: the tuios theme it
// shows (themeID), else guios's own where tuios names none.
func lookNamed(name string, dark bool) *look {
	if l := tuiosLook(themeID(name, dark)); l != nil {
		return l
	}
	return ownLook(dark)
}

// ownLooks are guios's own colors, light and dark (roles.Tilly). They
// show where tuios names no theme.
var ownLooks = func() [2]*look {
	var both [2]*look
	for i := range both {
		th, p, ground := roles.Tilly(i == 1)
		// One flat ground: a gradient of one level steps in bands across
		// the sidebar (council round 1, visual 21).
		g := toUI(ground)
		both[i] = &look{term: toTerm(th), pal: fromRoles(p), glow: glowOf(g.Alpha(0)),
			base: ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{{Color: g, At: 0}, {Color: g, At: 1}}}}
	}
	return both
}()

// ownLook is guios's own look for windows of an appearance.
func ownLook(dark bool) *look { return ownLooks[b2i(dark)] }

func glowOf(c ui.Color) ui.LinearGradient {
	return ui.LinearGradient{Angle: 225, Oklab: true, Stops: []ui.GradientStop{{Color: c, At: 0}, {Color: c.Alpha(0), At: 0.5}}}
}

// ghosttyLook washes the window in a terminal theme's background, a shade
// under its cards, with the accent roles.Look picks.
func ghosttyLook(th *terminal.Theme) *look {
	p, accent, ground := roles.Look(fromTerm(th))
	g := toUI(ground)
	return &look{term: th, pal: fromRoles(p),
		// One flat ground, as DESIGN.md section 1 asks: a gradient this
		// shallow steps in visible bands.
		base: ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{{Color: g, At: 0}, {Color: g, At: 1}}},
		// No glow: a theme's ground is one surface, so the right sidebar
		// sits on the same ground as the left (DESIGN.md principle 1).
		glow: glowOf(toUI(accent).Alpha(0)),
	}
}

// pressedQuiet is the ground of a quiet button (a header view, a tab) while
// the pointer holds it down: a step past its hover ground.
func pressedQuiet(pal *palette) ui.Color { return pal.rowHover.Mix(pal.text, 0.1) }
