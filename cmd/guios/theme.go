package main

import (
	"math"
	"sync/atomic"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
)

// palette is kero's colors, for the light and the dark appearance.
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
	// focusLine is the focused card's edge (line.focus): ink 16 % in
	// the light, 11 % in the dark.
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
	// work marks what works: an agent's turn, a program's progress. The
	// accent means only what is selected or primary (DESIGN.md section 1).
	work ui.Color

	overlay   ui.Color // behind dialogs
	popover   ui.Color
	kbd       ui.Color
	kbdBorder ui.Color
}

var lightPalette = palette{
	sidebar:      ui.Hex("#eceef4"),
	text:         ui.Hex("#1d1d1f"),
	textMuted:    ui.Hex("#6e6e78"),
	textFaint:    ui.Hex("#a3a3ad"),
	rowHover:     ui.RGBA(0, 0, 0, 0.04),
	rowSelected:  ui.Hex("#ffffff"),
	rowSelBorder: ui.RGBA(0, 0, 0, 0.05),
	rowShadow:    ui.RGBA(20, 20, 50, 0.08),

	card:          ui.Hex("#ffffff"),
	cardBorder:    ui.RGBA(0, 0, 0, 0.07),
	cardShadow:    ui.RGBA(0, 0, 0, 0.07),
	cardShadow2:   ui.RGBA(0, 0, 0, 0.035),
	floatShadow:   ui.RGBA(20, 20, 40, 0.14),
	cardDim:       ui.RGBA(255, 255, 255, 0.58),
	veil:          ui.RGBA(250, 248, 252, 0.28),
	buttonBg:      ui.Hex("#ffffff"),
	control:       ui.Hex("#ffffff"),
	controlHover:  ui.Hex("#f7f7f9"),
	controlShadow: true,
	divider:       ui.RGBA(0, 0, 0, 0.06),
	chip:          ui.RGBA(0, 0, 0, 0.045),

	attention: ui.Hex("#c27b0f"),
	attnSoft:  ui.RGBA(232, 154, 40, 0.13),
	attnChip:  ui.RGB(150, 90, 20),
	done:      ui.Hex("#1d1d1f"),
	onDone:    ui.Hex("#ffffff"),
	danger:    ui.Hex("#d93a35"),
	accent:    ui.Hex("#3b6cf6"),
	work:      ui.Hex("#2f5bd8"),

	overlay:   ui.RGBA(10, 10, 30, 0.12),
	popover:   ui.Hex("#ffffff"),
	kbd:       ui.RGBA(0, 0, 0, 0.04),
	kbdBorder: ui.RGBA(0, 0, 0, 0.09),
}

var darkPalette = palette{
	dark:         true,
	sidebar:      ui.Hex("#141418"),
	text:         ui.Hex("#ececf0"),
	textMuted:    ui.Hex("#a9a9b4"),
	textFaint:    ui.Hex("#83838f"),
	rowHover:     ui.RGBA(255, 255, 255, 0.05),
	rowSelected:  ui.RGBA(255, 255, 255, 0.10),
	rowSelBorder: ui.RGBA(255, 255, 255, 0.06),
	rowShadow:    ui.RGBA(0, 0, 0, 0.25),

	card:         ui.Hex("#232328"),
	cardBorder:   ui.RGBA(255, 255, 255, 0.08),
	cardShadow:   ui.RGBA(0, 0, 0, 0.35),
	cardShadow2:  ui.RGBA(0, 0, 0, 0.2),
	floatShadow:  ui.RGBA(0, 0, 0, 0.45),
	cardDim:      ui.RGBA(35, 35, 40, 0.5),
	veil:         ui.RGBA(14, 13, 18, 0.42),
	edge:         ui.RGBA(255, 255, 255, 0.075),
	edgeHigh:     ui.RGBA(255, 255, 255, 0.11),
	buttonBg:     ui.Hex("#404047"),
	control:      ui.Hex("#404047"),
	controlHover: ui.Hex("#4a4a52"),
	controlLight: ui.RGBA(255, 255, 255, 0.09),
	divider:      ui.RGBA(255, 255, 255, 0.07),
	chip:         ui.RGBA(255, 255, 255, 0.07),

	attention: ui.Hex("#f0a33a"),
	attnSoft:  ui.RGBA(240, 163, 58, 0.15),
	attnChip:  ui.RGB(200, 140, 60),
	done:      ui.Hex("#ececf0"),
	onDone:    ui.Hex("#161618"),
	danger:    ui.Hex("#ff6961"),
	accent:    ui.Hex("#6b93ff"),
	work:      ui.Hex("#6b9bff"),

	overlay:   ui.RGBA(0, 0, 0, 0.35),
	popover:   ui.Hex("#232327"),
	kbd:       ui.RGBA(255, 255, 255, 0.06),
	kbdBorder: ui.RGBA(255, 255, 255, 0.1),
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
		return paperLook(dark)
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
	return paperLook(dark)
}

// paperLooks are guios's own colors, light and dark: white paper on a
// desk of light gray, in ink, flat and quiet. They show where tuios
// names no theme.
var paperLooks = func() [2]*look {
	var both [2]*look
	for i, ground := range []string{"#f6f6f7", "#141416"} {
		// One flat ground: a gradient of one level steps in bands across
		// the sidebar (council round 1, visual 21).
		g := ui.Hex(ground)
		l := &look{base: ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{{Color: g, At: 0}, {Color: g, At: 1}}}, glow: glowOf(g.Alpha(0))}
		var accent ui.Color
		l.term, accent = toneTheme(tone{260, 0, 0.9, 262}, i == 1)
		l.pal = tinted(l.term, accent)
		both[i] = l
	}
	return both
}()

// paperLook is guios's own look for windows of an appearance.
func paperLook(dark bool) *look { return paperLooks[b2i(dark)] }

func glowOf(c ui.Color) ui.LinearGradient {
	return ui.LinearGradient{Angle: 225, Oklab: true, Stops: []ui.GradientStop{{Color: c, At: 0}, {Color: c.Alpha(0), At: 0.5}}}
}

// ghosttyLook washes the window in a terminal theme's background, a shade
// under its cards, with its cursor's color as its accent, unless that is
// a gray, as many themes' cursors are, when blue is.
func ghosttyLook(th *terminal.Theme) *look {
	bl, bc, bh := oklch(th.Background)
	accent := th.Cursor
	if accent == (ui.Color{}) {
		accent = th.Foreground
	}
	if _, c, _ := oklch(accent); c < 0.05 {
		accent = th.Palette[4]
	}
	l := &look{term: th, pal: tinted(th, accent)}
	shade := float32(0.03)
	if l.pal.dark {
		shade = 0.04
	}
	// One flat ground, as DESIGN.md section 1 asks: a gradient this
	// shallow steps in visible bands.
	ground := ui.Oklch(max(bl-shade, 0), bc, bh)
	l.base = ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{{Color: ground, At: 0}, {Color: ground, At: 1}}}
	// No glow: a theme's ground is one surface, so the right sidebar
	// sits on the same ground as the left (DESIGN.md principle 1).
	l.glow = glowOf(accent.Alpha(0))
	return l
}

// tinted is kero's palette for the light or the dark, as a terminal
// theme's background is, on the theme's colors: the cards its background,
// the text its foreground, with the accent.
func tinted(th *terminal.Theme, accent ui.Color) *palette {
	p := lightPalette
	if l, _, _ := oklch(th.Background); l < 0.5 {
		p = darkPalette
	}
	bg, fg, white := th.Background, th.Foreground, ui.Hex("#ffffff")
	p.card = bg
	p.cardDim = bg.Alpha(float32(p.cardDim.A) / 255)
	p.text, p.textMuted, p.textFaint = fg, fg.Mix(bg, 0.38), fg.Mix(bg, 0.56)
	// The text tiers keep their contrast on the cards whatever the theme:
	// 4.5:1 for the second, 3:1 for the third (DESIGN.md section 1). A
	// low-contrast theme moves them toward its foreground until they do.
	p.textMuted = legible(p.textMuted, fg, bg, 4.5)
	p.textFaint = legible(p.textFaint, fg, bg, 3)
	p.done, p.onDone = fg, bg
	p.accent = accent
	// The states take the theme's own blue and red, so a theme colors
	// them too, where they read on its cards: the bright one in the dark
	// when the plain one is too dim there.
	p.work = readable(th.Palette[4], th.Palette[12], bg, p.dark)
	p.danger = readable(th.Palette[1], th.Palette[9], bg, p.dark)
	if p.dark {
		p.popover = bg.Mix(white, 0.04)
	} else {
		p.rowSelected = bg.Mix(white, 0.5)
		p.popover = bg.Mix(white, 0.6)
	}
	p.finish(fg, bg)
	return &p
}

// finish makes the palette's text colors legible on every surface they
// sit on, not only on the card (council round 1, visual 3 to 5): the
// selected row, a control's fill (the reply box), a chip and the ground.
// The worst of those for each tier is the one checked: in the dark the
// lightest, in the light the darkest. text2 keeps 4.5:1 and text3 3:1
// there (DESIGN.md section 1), and needs-you and danger text 4.5:1.
func (p *palette) finish(fg, bg ui.Color) {
	if p.dark {
		p.focusLine = fg.Alpha(0.11)
	} else {
		p.focusLine = fg.Alpha(0.16)
	}
	worst := p.worstSurface(bg)
	// text3 carries information (ages, labels, hints, counts) on the
	// cards, the lists and the sidebar, on the ground a shade under the
	// cards: every tier keeps 4.5:1 there, in every theme (DESIGN.md
	// section 1). The tiers aim at 5:1: a small regular face draws its
	// strokes' cores a little lighter than the token, so 5:1 on the token
	// reads 4.5:1 on the frame's pixels (council round 3, visual M2).
	grounds := []ui.Color{bg, darker(bg, 0.04), darker(bg, 0.03)}
	if !p.dark {
		grounds = append(grounds, bg.Mix(p.sidebar, 0.6))
	}
	least := func(c ui.Color) float64 {
		m := math.Inf(1)
		for _, g := range grounds {
			m = min(m, contrast(c, g))
		}
		return m
	}
	// A theme whose own foreground falls under that gets a text a step
	// further from its background; the terminals keep the theme's.
	for _, g := range append(grounds, worst) {
		p.text = apart(p.text, g, 5)
	}
	fg = p.text
	p.textMuted = legible(fg.Mix(bg, 0.38), fg, worst, 4.5)
	p.textFaint = legible(fg.Mix(bg, 0.56), fg, worst, 3)
	// Short of 5:1, a tier moves toward text until it has it, or is text.
	for i := 0; i < 60 && least(p.textFaint) < 5; i++ {
		p.textFaint = p.textFaint.Mix(fg, 0.06)
	}
	// text2 stays a step stronger than text3, so the tiers hold.
	for i := 0; i < 60 && (least(p.textMuted) < 5 || contrast(p.textMuted, bg) < contrast(p.textFaint, bg)+1); i++ {
		p.textMuted = p.textMuted.Mix(fg, 0.06)
	}
	// An attempt's letter on its accent chip keeps 5:1 (visual M2).
	p.accentText = deepen(p.accent, p.accent.Alpha(0.16).Over(worst), 5)
	attnWorst := worst
	if s := p.attnSoft.Over(bg); p.dark == (luminance(s) > luminance(attnWorst)) {
		attnWorst = s
	}
	p.attnText = deepen(p.attention, attnWorst, 4.5)
	// On the focused card's ground, or a theme's own, the chip's amber
	// keeps 4.5:1: it goes deeper (or brighter) until it does.
	p.attnChip = deepen(p.attnChip, p.attnSoft.Over(bg), 4.5)
	dangerWorst := worst
	if s := p.danger.Alpha(0.1).Over(bg); p.dark == (luminance(s) > luminance(dangerWorst)) {
		dangerWorst = s
	}
	p.dangerText = deepen(p.danger, dangerWorst, 4.5)
	// White on the fill: the fill goes darker until white reads on it.
	p.dangerFill = deepen(p.danger, ui.Hex("#ffffff"), 4.5)
}

// worstSurface is the surface text has the least contrast on: the
// lightest of them in the dark, the darkest in the light.
func (p *palette) worstSurface(bg ui.Color) ui.Color {
	surfaces := []ui.Color{bg, p.rowSelected.Over(bg), p.chip.Over(bg), p.rowHover.Over(bg), p.sidebar}
	if p.dark {
		surfaces = append(surfaces, p.control.Over(bg), p.rowSelected.Over(p.rowHover.Over(bg)))
	} else {
		// The ground is a shade under the cards (ghosttyLook), and a chip
		// sits on it too.
		ground := darker(bg, 0.03)
		surfaces = append(surfaces, ground, p.chip.Over(ground), p.rowHover.Over(ground))
	}
	worst := surfaces[0]
	for _, s := range surfaces[1:] {
		if p.dark == (luminance(s) > luminance(worst)) {
			worst = s
		}
	}
	return worst
}

// darker is c with its OKLCH lightness lowered by d.
func darker(c ui.Color, d float32) ui.Color {
	l, ch, h := oklch(c)
	return ui.Oklch(max(l-d, 0), ch, h)
}

// deepen is c with its hue and chroma kept and its lightness moved away
// from bg, in small steps, until it has ratio of contrast on bg: darker on
// a light ground, lighter on a dark one.
func deepen(c, bg ui.Color, ratio float64) ui.Color {
	l, ch, h := oklch(c)
	down := luminance(bg) > 0.18
	for i := 0; i < 60 && contrast(c, bg) < ratio; i++ {
		if down {
			l -= 0.01
		} else {
			l += 0.01
		}
		if l <= 0 || l >= 1 {
			break
		}
		c = ui.Oklch(l, ch, h)
	}
	return c
}

// apart is c with its hue and chroma kept and its lightness moved away
// from bg, the way c already lies from it, until it has ratio of contrast
// on bg.
func apart(c, bg ui.Color, ratio float64) ui.Color {
	l, ch, h := oklch(c)
	lb, _, _ := oklch(bg)
	step := float32(0.01)
	if l < lb {
		step = -step
	}
	for i := 0; i < 100 && contrast(c, bg) < ratio; i++ {
		if l += step; l <= 0 || l >= 1 {
			break
		}
		c = ui.Oklch(l, ch, h)
	}
	return c
}

// legible is c moved toward fg, in steps, until it has at least ratio of
// contrast on bg, or fg itself.
func legible(c, fg, bg ui.Color, ratio float64) ui.Color {
	for i := 0; i < 10 && contrast(c, bg) < ratio; i++ {
		c = c.Mix(fg, 0.25)
	}
	return c
}

// luminance is a color's WCAG relative luminance.
func luminance(c ui.Color) float64 {
	ch := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// contrast is the WCAG contrast ratio of two colors.
func contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// readable is c, or alt where c is too close to the background bg to
// read as a mark: below a lightness gap of 0.3 in OKLCH.
func readable(c, alt, bg ui.Color, dark bool) ui.Color {
	l, _, _ := oklch(c)
	lb, _, _ := oklch(bg)
	if abs(l-lb) >= 0.3 {
		return c
	}
	if la, _, _ := oklch(alt); abs(la-lb) > abs(l-lb) {
		return alt
	}
	return c
}

// tone is the hue of a kero theme, how much of it its cards and text
// take, how vivid the programs' colors are, which lean to its hue, and
// the hue of its accent: the cursor, selection and controls.
type tone struct{ hue, tint, vivid, accent float32 }

// toneTheme is a kero theme's terminal colors, and its accent: its cards
// and text tinted with its hue, and the programs' colors muted to sit
// with them, the cursor and selection in its hue.
func toneTheme(t tone, dark bool) (*terminal.Theme, ui.Color) {
	tint := func(l, c float32) ui.Color { return ui.Oklch(l, c*t.tint, t.hue) }
	// Red, green, yellow, blue, magenta and cyan.
	hues := [6]float32{28, 145, 85, 255, 325, 195}
	ls := [6]float32{0.56, 0.56, 0.62, 0.54, 0.54, 0.56}
	cs := [6]float32{0.15, 0.13, 0.12, 0.13, 0.14, 0.09}
	bright := float32(0.05)
	th := &terminal.Theme{}
	var accent ui.Color
	if dark {
		ls = [6]float32{0.70, 0.74, 0.80, 0.70, 0.72, 0.76}
		cs = [6]float32{0.14, 0.13, 0.12, 0.12, 0.13, 0.10}
		bright = 0.06
		th.Background, th.Foreground = tint(0.255, 0.02), tint(0.92, 0.015)
		th.Palette[0], th.Palette[7], th.Palette[8], th.Palette[15] = tint(0.36, 0.02), tint(0.82, 0.012), tint(0.5, 0.02), tint(0.96, 0.01)
		th.Selection = ui.Oklch(0.38, 0.06, t.accent)
		accent = ui.Oklch(0.74, 0.13, t.accent)
	} else {
		// The less tinted, the whiter the paper: an untinted theme's
		// cards are white.
		paper := 0.978 + 0.022*max(0, 1-t.tint/0.5)
		th.Background, th.Foreground = tint(paper, 0.012), tint(0.28, 0.03)
		th.Palette[0], th.Palette[7], th.Palette[8], th.Palette[15] = th.Foreground, tint(0.7, 0.02), tint(0.52, 0.02), tint(0.8, 0.015)
		th.Selection = ui.Oklch(0.9, 0.045, t.accent)
		accent = ui.Oklch(0.55, 0.13, t.accent)
	}
	for i := range 6 {
		// Toward the theme's hue, the less the farther it is, as a far
		// color turned is a muddy one, and more vivid the nearer to it: a
		// theme's own color stands out of its palette.
		d := float32(math.Remainder(float64(t.hue-hues[i]), 360))
		h := hues[i] + d*0.35*(1-abs(d)/180)
		c := cs[i] * t.vivid * (1 + 0.45*max(0, 1-abs(d)/60))
		th.Palette[1+i] = ui.Oklch(ls[i], c, h)
		th.Palette[9+i] = ui.Oklch(ls[i]+bright, c+0.01, h)
	}
	th.Cursor, th.CursorText, th.SelectionText = accent, th.Background, th.Foreground
	return th, accent
}

// oklch returns a color's lightness, chroma and hue, as CSS's oklch().
func oklch(c ui.Color) (l, ch, h float32) {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, b := lin(c.R), lin(c.G), lin(c.B)
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	L := 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	A := 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	B := 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	hue := math.Mod(math.Atan2(B, A)*180/math.Pi+360, 360)
	return float32(L), float32(math.Hypot(A, B)), float32(hue)
}

// onAttention is the ink of a count on the needs-you amber: the dark ink
// or white, whichever reads better on it. White on the light theme's amber
// was 3.42:1 (council round 4, S1); the dark ink is 5:1.
func onAttention(pal *palette) ui.Color {
	dark, white := ui.RGB(44, 44, 46), ui.Hex("#ffffff")
	if contrast(white, pal.attention) > contrast(dark, pal.attention) {
		return white
	}
	return dark
}

// pressedQuiet is the ground of a quiet button (a header view, a tab) while
// the pointer holds it down: a step past its hover ground.
func pressedQuiet(pal *palette) ui.Color { return pal.rowHover.Mix(pal.text, 0.1) }
