package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

// Swatch is one named colour of a theme, measured against the ground it will be
// drawn on.
type Swatch struct {
	Name  string  `json:"name"`
	Hex   string  `json:"hex"`
	Ratio float64 `json:"ratio"`
	// Floor is what this colour's class has to clear: the text floor for the
	// foreground, the mark floor for the cursor and the sixteen, which are drawn
	// as glyphs and blocks rather than read as prose.
	Floor  float64 `json:"floor"`
	Passes bool    `json:"passes"`
}

// Palette is a theme resolved to hex, with the contrast of everything drawn on
// its background.
type Palette struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Dark        bool   `json:"dark"`
	Bg          string `json:"bg"`
	Fg          string `json:"fg"`
	Cursor      string `json:"cursor"`
	// Swatches is the foreground, the cursor and then the sixteen in index
	// order, so the last sixteen run black, red, ... bright_white.
	Swatches []Swatch `json:"swatches"`
	// Illegible names the swatches that did not clear their floor. It is the
	// actionable half of the report and is empty for a palette with nothing
	// wrong with it, so a caller can branch on it without walking the rows.
	Illegible []string `json:"illegible,omitempty"`
}

var ansiNames = [16]string{
	"black", "red", "green", "yellow",
	"blue", "purple", "cyan", "white",
	"bright_black", "bright_red", "bright_green", "bright_yellow",
	"bright_blue", "bright_purple", "bright_cyan", "bright_white",
}

// Describe resolves a theme id to its colours and measures them.
//
// It reads the registry rather than the active tint, so it answers for a theme
// nobody has selected yet: that is what lets a caller look before it leaps, and
// what lets it compare the theme it asked for against the one in effect.
func Describe(id string) (Palette, bool) {
	if id == "" || !Exists(id) {
		return Palette{}, false
	}
	if id == OmarchyID {
		_, _ = refreshOmarchy()
	}
	t, ok := tint.GetTint(id)
	if !ok || t == nil {
		return Palette{}, false
	}

	bg := t.Bg
	p := Palette{
		ID:          t.ID,
		DisplayName: t.DisplayName,
		Dark:        t.Dark,
		Bg:          ColorToString(bg),
		Fg:          ColorToString(t.Fg),
	}
	if t.Cursor != nil {
		p.Cursor = ColorToString(t.Cursor)
	}

	add := func(name string, c color.Color, floor float64) {
		if c == nil {
			return
		}
		s := Swatch{Name: name, Hex: ColorToString(c), Floor: floor}
		s.Ratio = roundRatio(ContrastRatio(c, bg))
		s.Passes = s.Ratio >= floor
		if !s.Passes {
			p.Illegible = append(p.Illegible, name)
		}
		p.Swatches = append(p.Swatches, s)
	}

	add("fg", t.Fg, ContrastFloor)
	add("cursor", AsColor(t.Cursor), MarkFloor)
	for i, c := range paletteOf(t) {
		add(ansiNames[i], c, MarkFloor)
	}

	return p, true
}

// Colors resolves a theme id to concrete colours: the sixteen ANSI entries
// plus the foreground, background and cursor.
//
// Describe answers the same question in hex strings for a person to read;
// this one answers it in color.Color for code that has to draw. The daemon is
// the caller that needs it: it never selects a tint of its own, so Current()
// and GetANSIPalette() answer for nothing there, and a screenshot rendered
// daemon-side has to resolve the session's theme by name.
func Colors(id string) (palette [16]color.Color, fg, bg, cursor color.Color, ok bool) {
	if id == "" || !Exists(id) {
		return palette, nil, nil, nil, false
	}
	if id == OmarchyID {
		_, _ = refreshOmarchy()
	}
	t, found := tint.GetTint(id)
	if !found || t == nil {
		return palette, nil, nil, nil, false
	}
	return paletteOf(t), AsColor(t.Fg), AsColor(t.Bg), AsColor(t.Cursor), true
}

func paletteOf(t *tint.Tint) [16]color.Color {
	var pal [16]color.Color
	for i, c := range ANSIOrder(t) {
		pal[i] = AsColor(c)
	}
	return pal
}

func roundRatio(r float64) float64 {
	return float64(int(r*100+0.5)) / 100
}
