// Package roles makes the colour roles of the guios chrome from a terminal
// theme: the text tiers, the fills and their ink, the veil over a pane the
// person is not in, and the rest of DESIGN.md section 1. It holds the
// contrast rules, so the tuioslint gate can check every theme against
// them without the GUI toolkit. cmd/guios turns the roles into its own
// colours.
package roles

import "math"

// Palette is the roles of guios's chrome, for one theme in one
// appearance (DESIGN.md section 1).
type Palette struct {
	Dark bool

	Sidebar      Color // under the sidebar where no material shows
	Text         Color
	TextMuted    Color // project headers, times
	TextFaint    Color // counts, hints
	RowHover     Color
	RowSelected  Color
	RowSelBorder Color
	RowShadow    Color

	Card        Color // the terminal's card
	CardBorder  Color
	CardShadow  Color
	CardShadow2 Color
	FloatShadow Color
	CardDim     Color // a pane the user is not in
	// edge and edgeHigh are the hairlines around surfaces in the dark:
	// cards and rows, and what floats or takes a click.
	Edge     Color
	EdgeHigh Color
	// veil fades a pane the user is not in, its text too, as Ghostty's
	// unfocused-split-opacity.
	Veil     Color
	ButtonBg Color
	// Controls' faces: buttons, fields, selects, a segmented control's
	// choice. Light ones stand on a soft shadow; dark ones on their
	// lighter fill alone, with a hint of light along their top, as macOS
	// draws them.
	Control       Color
	ControlHover  Color
	ControlShadow bool
	ControlLight  Color
	Divider       Color
	Chip          Color

	Attention Color // needs you: marks, edges, counts' pills
	AttnSoft  Color
	// focusLine is the focused card's edge (line.focus): ink 16 % in
	// the light, 11 % in the dark.
	FocusLine Color
	// attnText is needs-you as text: the amber made deep enough (light) or
	// bright enough (dark) to read at 4.5:1 on every surface it sits on,
	// its soft chip fill included. The bright amber stays for marks.
	AttnText Color
	// attnChip is needs-you as text on its own soft fill, the pane
	// header's chip, at 5:1 or more on an unfocused card and 4.5:1 on the
	// focused one (council round 3, visual M1).
	AttnChip Color
	Done     Color // the filled check
	OnDone   Color
	Danger   Color
	// dangerText is the danger color as text, at 4.5:1 on every surface;
	// dangerFill is a destructive button's fill, under white text at 4.5:1.
	DangerText Color
	DangerFill Color
	Accent     Color
	AccentText Color // the accent as text on its own 16 % chip
	// accentFill is the accent as a control's fill (the primary button,
	// the selected item of a menu, a selected chip), and accentInk the
	// text on it: dark ink or white, whichever reads, at 5:1 or more.
	// A fill neither reaches it on goes deeper until white does (council
	// round 5, visual M1). attnFill and attnInk are the same for a
	// needs-you count's badge.
	AccentFill, AccentInk Color
	AttnFill, AttnInk     Color
	// work marks what works: an agent's turn, a program's progress. The
	// accent means only what is selected or primary (DESIGN.md section 1).
	Work Color

	Overlay   Color // behind dialogs
	Popover   Color
	Kbd       Color
	KbdBorder Color
}

var lightPalette = Palette{
	Sidebar:      Hex("#eceef4"),
	Text:         Hex("#1d1d1f"),
	TextMuted:    Hex("#6e6e78"),
	TextFaint:    Hex("#a3a3ad"),
	RowHover:     RGBA(0, 0, 0, 0.04),
	RowSelected:  Hex("#ffffff"),
	RowSelBorder: RGBA(0, 0, 0, 0.05),
	RowShadow:    RGBA(20, 20, 50, 0.08),

	Card:          Hex("#ffffff"),
	CardBorder:    RGBA(0, 0, 0, 0.07),
	CardShadow:    RGBA(0, 0, 0, 0.07),
	CardShadow2:   RGBA(0, 0, 0, 0.035),
	FloatShadow:   RGBA(20, 20, 40, 0.14),
	CardDim:       RGBA(255, 255, 255, 0.58),
	Veil:          RGBA(250, 248, 252, 0.28),
	ButtonBg:      Hex("#ffffff"),
	Control:       Hex("#ffffff"),
	ControlHover:  Hex("#f7f7f9"),
	ControlShadow: true,
	Divider:       RGBA(0, 0, 0, 0.06),
	Chip:          RGBA(0, 0, 0, 0.045),

	Attention: Hex("#c27b0f"),
	AttnSoft:  RGBA(232, 154, 40, 0.13),
	AttnChip:  RGB(150, 90, 20),
	Done:      Hex("#1d1d1f"),
	OnDone:    Hex("#ffffff"),
	Danger:    Hex("#d93a35"),
	Accent:    Hex("#3b6cf6"),
	Work:      Hex("#2f5bd8"),

	Overlay:   RGBA(10, 10, 30, 0.12),
	Popover:   Hex("#ffffff"),
	Kbd:       RGBA(0, 0, 0, 0.04),
	KbdBorder: RGBA(0, 0, 0, 0.09),
}

var darkPalette = Palette{
	Dark:         true,
	Sidebar:      Hex("#141418"),
	Text:         Hex("#ececf0"),
	TextMuted:    Hex("#a9a9b4"),
	TextFaint:    Hex("#83838f"),
	RowHover:     RGBA(255, 255, 255, 0.05),
	RowSelected:  RGBA(255, 255, 255, 0.10),
	RowSelBorder: RGBA(255, 255, 255, 0.06),
	RowShadow:    RGBA(0, 0, 0, 0.25),

	Card:         Hex("#232328"),
	CardBorder:   RGBA(255, 255, 255, 0.08),
	CardShadow:   RGBA(0, 0, 0, 0.35),
	CardShadow2:  RGBA(0, 0, 0, 0.2),
	FloatShadow:  RGBA(0, 0, 0, 0.45),
	CardDim:      RGBA(35, 35, 40, 0.5),
	Veil:         RGBA(14, 13, 18, 0.42),
	Edge:         RGBA(255, 255, 255, 0.075),
	EdgeHigh:     RGBA(255, 255, 255, 0.11),
	ButtonBg:     Hex("#404047"),
	Control:      Hex("#404047"),
	ControlHover: Hex("#4a4a52"),
	ControlLight: RGBA(255, 255, 255, 0.09),
	Divider:      RGBA(255, 255, 255, 0.07),
	Chip:         RGBA(255, 255, 255, 0.07),

	Attention: Hex("#f0a33a"),
	AttnSoft:  RGBA(240, 163, 58, 0.15),
	AttnChip:  RGB(200, 140, 60),
	Done:      Hex("#ececf0"),
	OnDone:    Hex("#161618"),
	Danger:    Hex("#ff6961"),
	Accent:    Hex("#6b93ff"),
	Work:      Hex("#6b9bff"),

	Overlay:   RGBA(0, 0, 0, 0.35),
	Popover:   Hex("#232327"),
	Kbd:       RGBA(255, 255, 255, 0.06),
	KbdBorder: RGBA(255, 255, 255, 0.1),
}

// Tinted is kero's Palette for the light or the dark, as a terminal
// theme's background is, on the theme's colors: the cards its background,
// the text its foreground, with the accent.
func Tinted(th *Term, accent Color) *Palette {
	p := lightPalette
	if l, _, _ := ToOklch(th.Background); l < 0.5 {
		p = darkPalette
	}
	bg, fg, white := th.Background, th.Foreground, Hex("#ffffff")
	p.Card = bg
	p.CardDim = bg.Alpha(float32(p.CardDim.A) / 255)
	p.Text, p.TextMuted, p.TextFaint = fg, fg.Mix(bg, 0.38), fg.Mix(bg, 0.56)
	// The text tiers keep their contrast on the cards whatever the theme:
	// 4.5:1 for the second, 3:1 for the third (DESIGN.md section 1). A
	// low-contrast theme moves them toward its foreground until they do.
	p.TextMuted = legible(p.TextMuted, fg, bg, 4.5)
	p.TextFaint = legible(p.TextFaint, fg, bg, 3)
	p.Done, p.OnDone = fg, bg
	p.Accent = accent
	// The states take the theme's own blue and red, so a theme colors
	// them too, where they read on its cards: the bright one in the dark
	// when the plain one is too dim there.
	p.Work = readable(th.Palette[4], th.Palette[12], bg, p.Dark)
	p.Danger = readable(th.Palette[1], th.Palette[9], bg, p.Dark)
	if p.Dark {
		p.Popover = bg.Mix(white, 0.04)
	} else {
		p.RowSelected = bg.Mix(white, 0.5)
		p.Popover = bg.Mix(white, 0.6)
	}
	p.finish(fg, bg)
	return &p
}

// finish makes the Palette's text colors legible on every surface they
// sit on, not only on the card (council round 1, visual 3 to 5): the
// selected row, a control's fill (the reply box), a chip and the ground.
// The worst of those for each tier is the one checked: in the dark the
// lightest, in the light the darkest. text2 keeps 4.5:1 and text3 3:1
// there (DESIGN.md section 1), and needs-you and danger text 4.5:1.
func (p *Palette) finish(fg, bg Color) {
	// The terminals keep the theme's own foreground: the veil reads on it.
	termFg := fg
	if p.Dark {
		p.FocusLine = fg.Alpha(0.11)
	} else {
		p.FocusLine = fg.Alpha(0.16)
	}
	worst := p.worstSurface(bg)
	// text3 carries information (ages, labels, hints, counts) on the
	// cards, the lists and the sidebar, on the ground a shade under the
	// cards: every tier keeps 4.5:1 there, in every theme (DESIGN.md
	// section 1). The tiers aim at 5:1: a small regular face draws its
	// strokes' cores a little lighter than the token, so 5:1 on the token
	// reads 4.5:1 on the frame's pixels (council round 3, visual M2).
	grounds := []Color{bg, Darker(bg, 0.04), Darker(bg, 0.03)}
	if !p.Dark {
		grounds = append(grounds, bg.Mix(p.Sidebar, 0.6))
	}
	least := func(c Color) float64 {
		m := math.Inf(1)
		for _, g := range grounds {
			m = min(m, Contrast(c, g))
		}
		return m
	}
	// A theme whose own foreground falls under that gets a text a step
	// further from its background; the terminals keep the theme's.
	for _, g := range append(grounds, worst) {
		p.Text = apart(p.Text, g, 5)
	}
	fg = p.Text
	p.TextMuted = legible(fg.Mix(bg, 0.38), fg, worst, 4.5)
	p.TextFaint = legible(fg.Mix(bg, 0.56), fg, worst, 3)
	// Short of 5:1, a tier moves toward text until it has it, or is text.
	for i := 0; i < 60 && least(p.TextFaint) < 5; i++ {
		p.TextFaint = p.TextFaint.Mix(fg, 0.06)
	}
	// text2 stays a step stronger than text3, so the tiers hold.
	for i := 0; i < 60 && (least(p.TextMuted) < 5 || Contrast(p.TextMuted, bg) < Contrast(p.TextFaint, bg)+1); i++ {
		p.TextMuted = p.TextMuted.Mix(fg, 0.06)
	}
	p.AccentFill, p.AccentInk = InkFill(p.Accent)
	p.AttnFill, p.AttnInk = InkFill(p.Attention)
	p.Veil = dimVeil(termFg, bg, p.CardDim.Over(Darker(bg, 0.04)), p.Veil.A)
	// An attempt's letter on its accent chip keeps 5:1 (visual M2).
	p.AccentText = deepen(p.Accent, p.Accent.Alpha(0.16).Over(worst), 5)
	attnWorst := worst
	if s := p.AttnSoft.Over(bg); p.Dark == (Luminance(s) > Luminance(attnWorst)) {
		attnWorst = s
	}
	p.AttnText = deepen(p.Attention, attnWorst, 4.5)
	// On the focused card's ground, or a theme's own, the chip's amber
	// keeps 4.5:1: it goes deeper (or brighter) until it does.
	p.AttnChip = deepen(p.AttnChip, p.AttnSoft.Over(bg), 4.5)
	dangerWorst := worst
	if s := p.Danger.Alpha(0.1).Over(bg); p.Dark == (Luminance(s) > Luminance(dangerWorst)) {
		dangerWorst = s
	}
	p.DangerText = deepen(p.Danger, dangerWorst, 4.5)
	// White on the fill: the fill goes darker until white reads on it.
	p.DangerFill = deepen(p.Danger, Hex("#ffffff"), 4.5)
}

// worstSurface is the surface text has the least contrast on: the
// lightest of them in the dark, the darkest in the light.
func (p *Palette) worstSurface(bg Color) Color {
	surfaces := []Color{bg, p.RowSelected.Over(bg), p.Chip.Over(bg), p.RowHover.Over(bg), p.Sidebar}
	if p.Dark {
		surfaces = append(surfaces, p.Control.Over(bg), p.RowSelected.Over(p.RowHover.Over(bg)))
	} else {
		// The ground is a shade under the cards (ghosttyLook), and a chip
		// sits on it too.
		ground := Darker(bg, 0.03)
		surfaces = append(surfaces, ground, p.Chip.Over(ground), p.RowHover.Over(ground))
	}
	worst := surfaces[0]
	for _, s := range surfaces[1:] {
		if p.Dark == (Luminance(s) > Luminance(worst)) {
			worst = s
		}
	}
	return worst
}

// Darker is c with its OKLCH lightness lowered by d.
func Darker(c Color, d float32) Color {
	l, ch, h := ToOklch(c)
	return Oklch(max(l-d, 0), ch, h)
}

// deepen is c with its hue and chroma kept and its lightness moved away
// from bg, in small steps, until it has ratio of contrast on bg: darker on
// a light ground, lighter on a dark one.
func deepen(c, bg Color, ratio float64) Color {
	l, ch, h := ToOklch(c)
	down := Luminance(bg) > 0.18
	for i := 0; i < 60 && Contrast(c, bg) < ratio; i++ {
		if down {
			l -= 0.01
		} else {
			l += 0.01
		}
		if l <= 0 || l >= 1 {
			break
		}
		c = Oklch(l, ch, h)
	}
	return c
}

// apart is c with its hue and chroma kept and its lightness moved away
// from bg, the way c already lies from it, until it has ratio of contrast
// on bg.
func apart(c, bg Color, ratio float64) Color {
	l, ch, h := ToOklch(c)
	lb, _, _ := ToOklch(bg)
	step := float32(0.01)
	if l < lb {
		step = -step
	}
	for i := 0; i < 100 && Contrast(c, bg) < ratio; i++ {
		if l += step; l <= 0 || l >= 1 {
			break
		}
		c = Oklch(l, ch, h)
	}
	return c
}

// legible is c moved toward fg, in steps, until it has at least ratio of
// contrast on bg, or fg itself.
func legible(c, fg, bg Color, ratio float64) Color {
	for i := 0; i < 10 && Contrast(c, bg) < ratio; i++ {
		c = c.Mix(fg, 0.25)
	}
	return c
}

// Luminance is a color's WCAG relative luminance.
func Luminance(c Color) float64 {
	ch := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// Contrast is the WCAG contrast ratio of two colors.
func Contrast(a, b Color) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// readable is c, or alt where c is too close to the background bg to
// read as a mark: below a lightness gap of 0.3 in OKLCH.
func readable(c, alt, bg Color, dark bool) Color {
	l, _, _ := ToOklch(c)
	lb, _, _ := ToOklch(bg)
	if abs(l-lb) >= 0.3 {
		return c
	}
	if la, _, _ := ToOklch(alt); abs(la-lb) > abs(l-lb) {
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
func toneTheme(t tone, dark bool) (*Term, Color) {
	tint := func(l, c float32) Color { return Oklch(l, c*t.tint, t.hue) }
	// Red, green, yellow, blue, magenta and cyan.
	hues := [6]float32{28, 145, 85, 255, 325, 195}
	ls := [6]float32{0.56, 0.56, 0.62, 0.54, 0.54, 0.56}
	cs := [6]float32{0.15, 0.13, 0.12, 0.13, 0.14, 0.09}
	bright := float32(0.05)
	th := &Term{}
	var accent Color
	if dark {
		ls = [6]float32{0.70, 0.74, 0.80, 0.70, 0.72, 0.76}
		cs = [6]float32{0.14, 0.13, 0.12, 0.12, 0.13, 0.10}
		bright = 0.06
		th.Background, th.Foreground = tint(0.255, 0.02), tint(0.92, 0.015)
		th.Palette[0], th.Palette[7], th.Palette[8], th.Palette[15] = tint(0.36, 0.02), tint(0.82, 0.012), tint(0.5, 0.02), tint(0.96, 0.01)
		th.Selection = Oklch(0.38, 0.06, t.accent)
		accent = Oklch(0.74, 0.13, t.accent)
	} else {
		// The less tinted, the whiter the paper: an untinted theme's
		// cards are white.
		paper := 0.978 + 0.022*max(0, 1-t.tint/0.5)
		th.Background, th.Foreground = tint(paper, 0.012), tint(0.28, 0.03)
		th.Palette[0], th.Palette[7], th.Palette[8], th.Palette[15] = th.Foreground, tint(0.7, 0.02), tint(0.52, 0.02), tint(0.8, 0.015)
		th.Selection = Oklch(0.9, 0.045, t.accent)
		accent = Oklch(0.55, 0.13, t.accent)
	}
	for i := range 6 {
		// Toward the theme's hue, the less the farther it is, as a far
		// color turned is a muddy one, and more vivid the nearer to it: a
		// theme's own color stands out of its Palette.
		d := float32(math.Remainder(float64(t.hue-hues[i]), 360))
		h := hues[i] + d*0.35*(1-abs(d)/180)
		c := cs[i] * t.vivid * (1 + 0.45*max(0, 1-abs(d)/60))
		th.Palette[1+i] = Oklch(ls[i], c, h)
		th.Palette[9+i] = Oklch(ls[i]+bright, c+0.01, h)
	}
	th.Cursor, th.CursorText, th.SelectionText = accent, th.Background, th.Foreground
	return th, accent
}

// ToOklch returns a color's lightness, chroma and hue, as CSS's ToOklch().
func ToOklch(c Color) (l, ch, h float32) {
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

// InkFill is a fill for a control and the ink of its text: dark ink or
// white, whichever has more contrast on it. When neither reaches 5:1 on
// the token, which reads 4.5:1 on the frame's pixels, the fill goes
// deeper in OKLCH, its hue kept, until white does.
func InkFill(fill Color) (Color, Color) {
	dark, white := RGB(23, 23, 23), Hex("#ffffff")
	for range 60 {
		ink := dark
		if Contrast(white, fill) > Contrast(dark, fill) {
			ink = white
		}
		if Contrast(ink, fill) >= 5 {
			return fill, ink
		}
		fill = Darker(fill, 0.01)
	}
	return fill, white
}

// HoverStep and PressStep are how far a filled control's fill moves away
// from its ink under the pointer and while it is held (AwayFrom).
const (
	HoverStep = 0.08
	PressStep = 0.14
)

// AwayFrom is a fill moved by f away from its ink, for hover and press:
// the text keeps the contrast the fill gave it, or gains.
func AwayFrom(fill, ink Color, f float32) Color {
	if Luminance(ink) > Luminance(fill) {
		return fill.Mix(Hex("#000000"), f)
	}
	return fill.Mix(Hex("#ffffff"), f)
}

// dimVeil is the veil over a pane the person is not in: the theme's own
// background at up to alpha, no more than keeps the theme's foreground at
// 4.8:1 on ground under the veil, which reads 4.5:1 on the frame's pixels
// (council round 5, visual M3). Dimming toward the theme's background keeps the
// pane in the theme's hue; a fixed grey turned gruvbox cool.
func dimVeil(fg, bg, ground Color, alpha uint8) Color {
	for a := float32(alpha) / 255; a > 0; a -= 0.01 {
		v := bg.Alpha(a)
		if Contrast(v.Over(fg), v.Over(ground)) >= 4.8 {
			return v
		}
	}
	return bg.Alpha(0)
}
