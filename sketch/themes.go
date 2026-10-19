// Not a file of pele: named themes for the --pele-* tokens.
//
// pele leaves colours to the page. Its site and its launch video set them from
// a few palettes (website/src/styles/pele-theme.scss and
// website/src/lib/video-themes.ts); those are copied here with their values.
// The rest are popular terminal themes mapped the same way the video maps its
// palettes: lines in the text colour, the alternate surface equal to the
// border, the accent the blue series colour and critical the red one.

package sketch

import (
	"image/color"
	"strconv"
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

func hex(s string) color.Color {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		panic("sketch: bad colour " + s)
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

// videoPalette is palette() of pele's website/src/lib/video-themes.ts. series
// runs red, orange, yellow, green, cyan, blue, purple, pink.
func videoPalette(bg, fg, muted, surface, border string, series [8]string) Theme {
	t := Theme{
		Bg: hex(bg), Fg: hex(fg), Muted: hex(muted), Line: hex(fg),
		Surface: hex(surface), SurfaceAlt: hex(border), Border: hex(border),
		Accent: hex(series[5]), Critical: hex(series[0]),
		Radius: 6,
	}
	for i, s := range series {
		t.Series[i] = hex(s)
	}
	return t
}

// full sets every token, for the palettes pele's site writes out in full.
func full(bg, fg, muted, line, surface, surfaceAlt, border, accent, critical string, series [8]string, radius float64) Theme {
	t := Theme{
		Bg: hex(bg), Fg: hex(fg), Muted: hex(muted), Line: hex(line),
		Surface: hex(surface), SurfaceAlt: hex(surfaceAlt), Border: hex(border),
		Accent: hex(accent), Critical: hex(critical), Radius: radius,
	}
	for i, s := range series {
		t.Series[i] = hex(s)
	}
	return t
}

var namedThemes = []NamedTheme{
	// pele's fallbacks, written out: src/svg/theme.ts.
	{"pele", "pele", false, full("#ffffff", "#1f1f1f", "#6e6e6e", "#1f1f1f", "#f3f3f3", "#e6e6e6", "#8a8a8a", "#5b7bd5", "#e45756",
		[8]string{"#4c78a8", "#f58518", "#54a24b", "#e45756", "#72b7b2", "#eeca3b", "#b279a2", "#9d755d"}, 0)},
	// pele.run, light and dark: website/src/styles/pele-theme.scss (Flexoki).
	{"flexoki", "Flexoki", false, full("#fffcf0", "#100f0f", "#6f6e69", "#100f0f", "#f2f0e5", "#e6e4d9", "#cecdc3", "#205ea6", "#af3029",
		[8]string{"#af3029", "#bc5215", "#ad8301", "#66800b", "#24837b", "#205ea6", "#5e409d", "#a02f6f"}, 6)},
	{"flexoki-dark", "Flexoki dark", true, full("#100f0f", "#cecdc3", "#878580", "#cecdc3", "#1c1b1a", "#282726", "#403e3c", "#4385be", "#d14d41",
		[8]string{"#d14d41", "#da702c", "#d0a215", "#879a39", "#3aa99f", "#4385be", "#8b7ec8", "#ce5d97"}, 6)},
	// pele's launch video: website/src/lib/video-themes.ts.
	{"nord", "Nord", true, videoPalette("#2e3440", "#d8dee9", "#9eafcc", "#3b4252", "#434c5e",
		[8]string{"#bf616a", "#d08770", "#ebcb8b", "#a3be8c", "#88c0d0", "#81a1c1", "#b48ead", "#b48ead"})},
	{"everforest", "Everforest", true, videoPalette("#2d353b", "#d3c6aa", "#9da9a0", "#232a2e", "#475258",
		[8]string{"#e67e80", "#e69875", "#dbbc7f", "#a7c080", "#83c092", "#7fbbb3", "#d699b6", "#d699b6"})},
	{"gruvbox", "Gruvbox dark", true, videoPalette("#282828", "#fbf1c7", "#bdae93", "#1e2021", "#3c3836",
		[8]string{"#cc241d", "#d65d0e", "#d79921", "#98971a", "#689d6a", "#458588", "#b16286", "#b16286"})},
	{"rose-pine", "Rosé Pine", true, videoPalette("#1f1d2e", "#e0def4", "#908caa", "#191724", "#312f41",
		[8]string{"#eb6f92", "#ea9a97", "#f6c177", "#31748f", "#9ccfd8", "#9ccfd8", "#c4a7e7", "#c4a7e7"})},
	// Popular terminal themes, from each project's published palette.
	{"catppuccin-mocha", "Catppuccin Mocha", true, videoPalette("#1e1e2e", "#cdd6f4", "#a6adc8", "#313244", "#45475a",
		[8]string{"#f38ba8", "#fab387", "#f9e2af", "#a6e3a1", "#94e2d5", "#89b4fa", "#cba6f7", "#f5c2e7"})},
	{"catppuccin-latte", "Catppuccin Latte", false, videoPalette("#eff1f5", "#4c4f69", "#5c5f77", "#e6e9ef", "#ccd0da",
		[8]string{"#d20f39", "#fe640b", "#df8e1d", "#40a02b", "#179299", "#1e66f5", "#8839ef", "#ea76cb"})},
	{"tokyo-night", "Tokyo Night", true, videoPalette("#1a1b26", "#c0caf5", "#a9b1d6", "#24283b", "#414868",
		[8]string{"#f7768e", "#ff9e64", "#e0af68", "#9ece6a", "#7dcfff", "#7aa2f7", "#bb9af7", "#ff007c"})},
	{"dracula", "Dracula", true, videoPalette("#282a36", "#f8f8f2", "#a4b0d8", "#343746", "#44475a",
		[8]string{"#ff5555", "#ffb86c", "#f1fa8c", "#50fa7b", "#8be9fd", "#bd93f9", "#ff79c6", "#ff79c6"})},
	{"solarized-light", "Solarized light", false, videoPalette("#fdf6e3", "#073642", "#586e75", "#eee8d5", "#d3cbb7",
		[8]string{"#dc322f", "#cb4b16", "#b58900", "#859900", "#2aa198", "#268bd2", "#6c71c4", "#d33682"})},
	{"kanagawa", "Kanagawa", true, videoPalette("#1f1f28", "#dcd7ba", "#a6a69c", "#2a2a37", "#363646",
		[8]string{"#e46876", "#ffa066", "#e6c384", "#98bb6c", "#7aa89f", "#7e9cd8", "#957fb8", "#d27e99"})},
}

// Themes lists the named themes: pele's own first, then terminal themes.
func Themes() []NamedTheme {
	return append([]NamedTheme(nil), namedThemes...)
}

// ThemeNamed finds a named theme. Case, spaces and "_" do not matter.
func ThemeNamed(name string) (*Theme, bool) {
	key := strings.NewReplacer(" ", "-", "_", "-", "é", "e").Replace(strings.ToLower(strings.TrimSpace(name)))
	for _, t := range namedThemes {
		if t.Name == key {
			th := t.Theme
			return &th, true
		}
	}
	return nil, false
}
