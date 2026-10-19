package loom

import (
	"image/color"
	"math"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/plot"
	"tuios.dev/tml"
)

// Depth is the colour depth of the terminal a surface draws for.
type Depth uint8

// The depths, richest first. ASCII has no colour and draws loom's own marks
// in printable ASCII.
const (
	TrueColor Depth = iota
	Color256
	Color16
	ASCII
)

func (d Depth) String() string {
	switch d {
	case TrueColor:
		return "truecolor"
	case Color256:
		return "256"
	case Color16:
		return "16"
	}
	return "ascii"
}

// Depths lists the depths, richest first.
func Depths() []Depth { return []Depth{TrueColor, Color256, Color16, ASCII} }

// The roles loom adds to the TML roles. A document cannot name them; loom
// draws its own marks with them.
//
// Roles are plain strings in loom, the names plot.Role holds; Track and
// the heat ramp are plot's own.
const (
	RoleTrack  = string(plot.Track) // the empty part of a gauge or a bar
	RoleCursor = "cursor"           // the ground of the cursor row
	RolePill   = "pill"             // text on an accent ground
)

// The heat ramp of a heatmap, low to high. tuios.dev/plot names them.
const (
	RoleHeat1 = string(plot.Heat1)
	RoleHeat2 = string(plot.Heat2)
	RoleHeat3 = string(plot.Heat3)
	RoleHeat4 = string(plot.Heat4)
	RoleHeat5 = string(plot.Heat5)
)

// roleNames is every role a palette holds: the TML roles, then loom's own.
var roleNames = append(tml.Roles(), RoleTrack, RoleCursor, RolePill, RoleHeat1, RoleHeat2, RoleHeat3, RoleHeat4, RoleHeat5)

func roleIndex(name string) int {
	for i, r := range roleNames {
		if r == name {
			return i
		}
	}
	return -1
}

// Palette maps each role to a colour at one depth. A nil colour means none:
// a ground with no colour leaves the host's ground, and a state that a
// ground would show (the cursor row, a primary button) is drawn in reverse
// video instead.
//
// Every colour loom draws comes from a palette, so the host keeps one look
// and its contrast rules across every surface.
type Palette struct {
	Depth  Depth
	colors []color.Color
}

// NewPalette returns a palette at depth d with the given colours by role. A
// role left out takes a colour derived from the others: track and edge fall
// back to mute, cursor to surface, pill to fg, the tints and the series to
// the status colours. At ASCII depth every colour is dropped.
func NewPalette(d Depth, roles map[string]color.Color) *Palette {
	p := &Palette{Depth: d, colors: make([]color.Color, len(roleNames))}
	if d == ASCII {
		return p
	}
	for name, c := range roles {
		if i := roleIndex(name); i >= 0 {
			p.colors[i] = c
		}
	}
	fallback := [][2]string{
		{"dim", "fg"}, {"mute", "dim"}, {"edge", "mute"}, {RoleTrack, "edge"},
		{"accent-bright", "accent"}, {"attention", "warning"}, {"busy", "info"},
		{RoleCursor, "surface"}, {RolePill, "fg"},
		{RoleHeat1, "mute"}, {RoleHeat2, "info"}, {RoleHeat3, "info"}, {RoleHeat4, "accent"}, {RoleHeat5, "accent-bright"},
		{"series1", "accent-bright"}, {"series2", "info"}, {"series3", "success"},
		{"series4", "warning"}, {"series5", "error"}, {"series6", "attention"},
	}
	for _, f := range fallback {
		if i := roleIndex(f[0]); p.colors[i] == nil {
			p.colors[i] = p.colors[roleIndex(f[1])]
		}
	}
	return p
}

// Color returns the colour of a role, or nil.
func (p *Palette) Color(role string) color.Color {
	if p == nil {
		return nil
	}
	if i := roleIndex(role); i >= 0 {
		return p.colors[i]
	}
	return nil
}

// grounded reports whether the palette paints grounds. At 16 colours and in
// ASCII it does not.
func (p *Palette) grounded() bool { return p.Color("panel") != nil }

func rgb(v uint32) color.RGBA {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// mixRGB blends a toward b by t in OKLab, as the tuios chrome does.
func mixRGB(a, b color.RGBA, t float64) color.RGBA {
	al, aa, ab := toOKLab(a)
	bl, ba, bb := toOKLab(b)
	return fromOKLab(al+(bl-al)*t, aa+(ba-aa)*t, ab+(bb-ab)*t)
}

func toLinear(c uint8) float64 {
	f := float64(c) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

func fromLinear(f float64) uint8 {
	if f <= 0.0031308 {
		f *= 12.92
	} else {
		f = 1.055*math.Pow(f, 1/2.4) - 0.055
	}
	return uint8(math.Round(math.Max(0, math.Min(1, f)) * 255))
}

func toOKLab(c color.RGBA) (l, a, b float64) {
	r, g, bl := toLinear(c.R), toLinear(c.G), toLinear(c.B)
	lm := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*bl)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*bl)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*bl)
	return 0.2104542553*lm + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*lm - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*lm + 0.7827717662*m - 0.8086757660*s
}

func fromOKLab(l, a, b float64) color.RGBA {
	lm := l + 0.3963377774*a + 0.2158037573*b
	m := l - 0.1055613458*a - 0.0638541728*b
	s := l - 0.0894841775*a - 1.2914855480*b
	lm, m, s = lm*lm*lm, m*m*m, s*s*s
	return color.RGBA{
		R: fromLinear(4.0767416621*lm - 3.3077115913*m + 0.2309699292*s),
		G: fromLinear(-1.2684380046*lm + 2.6097574011*m - 0.3413193965*s),
		B: fromLinear(-0.0041960863*lm - 0.7034186147*m + 1.7076147010*s),
		A: 255,
	}
}

// The default roles: the tuios chrome palette with no theme (charmtone), for
// a surface on the panel ground.
var defaultRGB = func() map[string]color.RGBA {
	canvas, panel, surface, card := rgb(0x201F26), rgb(0x2D2C36), rgb(0x3A3943), rgb(0x4D4C57)
	fg, dim, mute := rgb(0xFFFAF1), rgb(0xBFBCC8), rgb(0x858392)
	accent, bright := rgb(0x6B50FF), rgb(0x8B75FF)
	errC, success, info, warning := rgb(0xFF388B), rgb(0x00FFB2), rgb(0x00A4FF), rgb(0xFF985A)
	return map[string]color.RGBA{
		"canvas": canvas, "panel": panel, "surface": surface, "card": card,
		"fg": fg, "dim": dim, "mute": mute,
		"accent": accent, "accent-bright": bright,
		"success": success, "warning": warning, "error": errC, "info": info,
		"attention": warning, "busy": info,
		"edge":         mixRGB(panel, mute, 0.40),
		"accent-tint":  mixRGB(accent, panel, 0.62),
		"success-tint": mixRGB(success, panel, 0.84),
		"warning-tint": mixRGB(warning, panel, 0.80),
		"error-tint":   mixRGB(errC, panel, 0.80),
		"series1":      bright, "series2": info, "series3": mixRGB(success, dim, 0.25),
		"series4": warning, "series5": errC, "series6": rgb(0xE5C07B),
		RoleTrack:  mixRGB(panel, mute, 0.30),
		RoleCursor: surface,
		RolePill:   fg,
		// From the ground through the info blue to the accent.
		RoleHeat1: mixRGB(panel, info, 0.22),
		RoleHeat2: mixRGB(panel, info, 0.48),
		RoleHeat3: info,
		RoleHeat4: mixRGB(info, bright, 0.55),
		RoleHeat5: bright,
	}
}()

// The default roles at 16 colours: ANSI slots only, and no grounds.
var default16 = map[string]int{
	"fg": 15, "dim": 7, "mute": 8, "edge": 8, RoleTrack: 8,
	"accent": 5, "accent-bright": 13, RolePill: 0,
	"success": 10, "error": 9, "warning": 11, "info": 12, "attention": 11, "busy": 12,
	"series1": 13, "series2": 12, "series3": 10, "series4": 11, "series5": 9, "series6": 14,
	RoleHeat1: 4, RoleHeat2: 12, RoleHeat3: 6, RoleHeat4: 5, RoleHeat5: 13,
}

// DefaultPalette returns the tuios chrome palette with no theme at depth d.
// At 256 colours each colour is the nearest of the xterm 256. At 16 colours
// the roles are ANSI slots and there are no grounds. A host with a theme
// builds its own with NewPalette.
func DefaultPalette(d Depth) *Palette {
	roles := map[string]color.Color{}
	switch d {
	case TrueColor:
		for k, v := range defaultRGB {
			roles[k] = v
		}
	case Color256:
		for k, v := range defaultRGB {
			roles[k] = ansi.IndexedColor(nearest256(v))
		}
	case Color16:
		for k, v := range default16 {
			roles[k] = ansi.BasicColor(v)
		}
	}
	return NewPalette(d, roles)
}

// nearest256 returns the xterm 256 colour nearest to c, from the colour cube
// and the grey ramp, by distance in OKLab.
func nearest256(c color.RGBA) uint8 {
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	l, a, b := toOKLab(c)
	best, bestD := 16, math.Inf(1)
	try := func(i int, x color.RGBA) {
		xl, xa, xb := toOKLab(x)
		d := (l-xl)*(l-xl) + (a-xa)*(a-xa) + (b-xb)*(b-xb)
		if d < bestD {
			best, bestD = i, d
		}
	}
	for i := 0; i < 216; i++ {
		try(16+i, color.RGBA{levels[i/36], levels[i/6%6], levels[i%6], 255})
	}
	for i := 0; i < 24; i++ {
		v := uint8(8 + 10*i)
		try(232+i, color.RGBA{v, v, v, 255})
	}
	return uint8(best)
}
