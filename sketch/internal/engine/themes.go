// Not a file of pele: named themes for the --pele-* tokens.
//
// pele leaves colours to the page. Its site and its launch video set them from
// a few palettes (website/src/styles/pele-theme.scss and
// website/src/lib/video-themes.ts); those are copied here with their values.
// The rest are popular terminal themes mapped the same way the video maps its
// palettes: lines in the text colour, the alternate surface equal to the
// border, the accent the blue series colour and critical the red one.

package engine

import (
	"image/color"
	"strings"
)

// NamedTheme is a theme with the name a user types for it.
type NamedTheme struct {
	// Name is the key: lower case, words joined with "-".
	Name string
	// Title is the name for people, such as "Rosé Pine".
	Title string
	// Dark says the background is dark.
	Dark  bool
	Theme Theme
}

// themeSpec is one named theme as plain values. The table below is static
// data, built by the linker, so it costs nothing at start; a Theme is built
// from it when a caller asks for one.
type themeSpec struct {
	Name, Title string
	Dark        bool
	// Video marks a palette mapped as pele's launch video maps its palettes:
	// Line is Fg, SurfaceAlt is Border, Accent is Series[5], Critical is
	// Series[0] and Radius is 6, so those fields are left out of the entry.
	Video                                                              bool
	Radius                                                             float64
	Bg, Fg, Muted, Line, Surface, SurfaceAlt, Border, Accent, Critical uint32
	// Series runs red, orange, yellow, green, cyan, blue, purple, pink.
	Series [8]uint32
}

func rgb(v uint32) color.Color {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

// named builds the NamedTheme from a copy of the spec. A video palette is
// palette() of pele's website/src/lib/video-themes.ts.
func (p *themeSpec) named() NamedTheme {
	s := *p
	if s.Video {
		s.Line, s.SurfaceAlt, s.Accent, s.Critical, s.Radius = s.Fg, s.Border, s.Series[5], s.Series[0], 6
	}
	t := Theme{
		Bg: rgb(s.Bg), Fg: rgb(s.Fg), Muted: rgb(s.Muted), Line: rgb(s.Line),
		Surface: rgb(s.Surface), SurfaceAlt: rgb(s.SurfaceAlt), Border: rgb(s.Border),
		Accent: rgb(s.Accent), Critical: rgb(s.Critical), Radius: s.Radius,
	}
	for i, v := range s.Series {
		t.Series[i] = rgb(v)
	}
	return NamedTheme{Name: s.Name, Title: s.Title, Dark: s.Dark, Theme: t}
}

var namedThemes = [...]themeSpec{
	// pele's fallbacks, written out: src/svg/theme.ts.
	{Name: "pele", Title: "pele", Dark: false, Radius: 0,
		Bg: 0xffffff, Fg: 0x1f1f1f, Muted: 0x6e6e6e, Line: 0x1f1f1f, Surface: 0xf3f3f3, SurfaceAlt: 0xe6e6e6, Border: 0x8a8a8a, Accent: 0x5b7bd5, Critical: 0xe45756,
		Series: [8]uint32{0x4c78a8, 0xf58518, 0x54a24b, 0xe45756, 0x72b7b2, 0xeeca3b, 0xb279a2, 0x9d755d}},
	// pele.run, light and dark: website/src/styles/pele-theme.scss (Flexoki).
	{Name: "flexoki", Title: "Flexoki", Dark: false, Radius: 6,
		Bg: 0xfffcf0, Fg: 0x100f0f, Muted: 0x6f6e69, Line: 0x100f0f, Surface: 0xf2f0e5, SurfaceAlt: 0xe6e4d9, Border: 0xcecdc3, Accent: 0x205ea6, Critical: 0xaf3029,
		Series: [8]uint32{0xaf3029, 0xbc5215, 0xad8301, 0x66800b, 0x24837b, 0x205ea6, 0x5e409d, 0xa02f6f}},
	{Name: "flexoki-dark", Title: "Flexoki dark", Dark: true, Radius: 6,
		Bg: 0x100f0f, Fg: 0xcecdc3, Muted: 0x878580, Line: 0xcecdc3, Surface: 0x1c1b1a, SurfaceAlt: 0x282726, Border: 0x403e3c, Accent: 0x4385be, Critical: 0xd14d41,
		Series: [8]uint32{0xd14d41, 0xda702c, 0xd0a215, 0x879a39, 0x3aa99f, 0x4385be, 0x8b7ec8, 0xce5d97}},
	// pele's launch video: website/src/lib/video-themes.ts.
	{Name: "nord", Title: "Nord", Dark: true, Video: true,
		Bg: 0x2e3440, Fg: 0xd8dee9, Muted: 0x9eafcc, Surface: 0x3b4252, Border: 0x434c5e,
		Series: [8]uint32{0xbf616a, 0xd08770, 0xebcb8b, 0xa3be8c, 0x88c0d0, 0x81a1c1, 0xb48ead, 0xb48ead}},
	{Name: "everforest", Title: "Everforest", Dark: true, Video: true,
		Bg: 0x2d353b, Fg: 0xd3c6aa, Muted: 0x9da9a0, Surface: 0x232a2e, Border: 0x475258,
		Series: [8]uint32{0xe67e80, 0xe69875, 0xdbbc7f, 0xa7c080, 0x83c092, 0x7fbbb3, 0xd699b6, 0xd699b6}},
	{Name: "gruvbox", Title: "Gruvbox dark", Dark: true, Video: true,
		Bg: 0x282828, Fg: 0xfbf1c7, Muted: 0xbdae93, Surface: 0x1e2021, Border: 0x3c3836,
		Series: [8]uint32{0xcc241d, 0xd65d0e, 0xd79921, 0x98971a, 0x689d6a, 0x458588, 0xb16286, 0xb16286}},
	{Name: "rose-pine", Title: "Rosé Pine", Dark: true, Video: true,
		Bg: 0x1f1d2e, Fg: 0xe0def4, Muted: 0x908caa, Surface: 0x191724, Border: 0x312f41,
		Series: [8]uint32{0xeb6f92, 0xea9a97, 0xf6c177, 0x31748f, 0x9ccfd8, 0x9ccfd8, 0xc4a7e7, 0xc4a7e7}},
	// Popular terminal themes, from each project's published palette.
	{Name: "catppuccin-mocha", Title: "Catppuccin Mocha", Dark: true, Video: true,
		Bg: 0x1e1e2e, Fg: 0xcdd6f4, Muted: 0xa6adc8, Surface: 0x313244, Border: 0x45475a,
		Series: [8]uint32{0xf38ba8, 0xfab387, 0xf9e2af, 0xa6e3a1, 0x94e2d5, 0x89b4fa, 0xcba6f7, 0xf5c2e7}},
	{Name: "catppuccin-latte", Title: "Catppuccin Latte", Dark: false, Video: true,
		Bg: 0xeff1f5, Fg: 0x4c4f69, Muted: 0x5c5f77, Surface: 0xe6e9ef, Border: 0xccd0da,
		Series: [8]uint32{0xd20f39, 0xfe640b, 0xdf8e1d, 0x40a02b, 0x179299, 0x1e66f5, 0x8839ef, 0xea76cb}},
	{Name: "tokyo-night", Title: "Tokyo Night", Dark: true, Video: true,
		Bg: 0x1a1b26, Fg: 0xc0caf5, Muted: 0xa9b1d6, Surface: 0x24283b, Border: 0x414868,
		Series: [8]uint32{0xf7768e, 0xff9e64, 0xe0af68, 0x9ece6a, 0x7dcfff, 0x7aa2f7, 0xbb9af7, 0xff007c}},
	{Name: "dracula", Title: "Dracula", Dark: true, Video: true,
		Bg: 0x282a36, Fg: 0xf8f8f2, Muted: 0xa4b0d8, Surface: 0x343746, Border: 0x44475a,
		Series: [8]uint32{0xff5555, 0xffb86c, 0xf1fa8c, 0x50fa7b, 0x8be9fd, 0xbd93f9, 0xff79c6, 0xff79c6}},
	{Name: "solarized-light", Title: "Solarized light", Dark: false, Video: true,
		Bg: 0xfdf6e3, Fg: 0x073642, Muted: 0x586e75, Surface: 0xeee8d5, Border: 0xd3cbb7,
		Series: [8]uint32{0xdc322f, 0xcb4b16, 0xb58900, 0x859900, 0x2aa198, 0x268bd2, 0x6c71c4, 0xd33682}},
	{Name: "kanagawa", Title: "Kanagawa", Dark: true, Video: true,
		Bg: 0x1f1f28, Fg: 0xdcd7ba, Muted: 0xa6a69c, Surface: 0x2a2a37, Border: 0x363646,
		Series: [8]uint32{0xe46876, 0xffa066, 0xe6c384, 0x98bb6c, 0x7aa89f, 0x7e9cd8, 0x957fb8, 0xd27e99}},
}

// Themes lists the named themes: pele's own first, then terminal themes.
func Themes() []NamedTheme {
	out := make([]NamedTheme, len(namedThemes))
	for i := range namedThemes {
		out[i] = namedThemes[i].named()
	}
	return out
}

// ThemeNamed finds a named theme. Case, spaces and "_" do not matter.
func ThemeNamed(name string) (*Theme, bool) {
	key := strings.NewReplacer(" ", "-", "_", "-", "é", "e").Replace(strings.ToLower(strings.TrimSpace(name)))
	for i := range namedThemes {
		if namedThemes[i].Name == key {
			th := namedThemes[i].named().Theme
			return &th, true
		}
	}
	return nil, false
}
