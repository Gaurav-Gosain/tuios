package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

// An agent ricing a session cannot see the screen. It can set a palette and
// read the option back, and until this existed that was the whole of the
// feedback: the value it had just sent, echoed. Whether the green it chose is
// legible on the background it chose was not answerable from outside the
// process, and a theme whose colours sit on top of each other looks exactly
// like a theme that failed to apply.
//
// Describe answers both halves. The colours are what the theme actually holds,
// so a palette that did not apply reads back as the old one; the ratios are
// measured against the theme's own background, so an unreadable choice is
// reported as unreadable before anyone has to look at it.

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

// ansiNames are the sixteen in index order, under the names every terminal
// theme format already uses for them.
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
		Cursor:      ColorToString(t.Cursor),
	}

	// The foreground is prose and the cursor is a block, so they are held to
	// different floors. Both are listed alongside the sixteen rather than
	// measured separately, because a caller fixing a palette wants one list.
	add := func(name string, c color.Color, floor float64) {
		s := Swatch{Name: name, Hex: ColorToString(c), Floor: floor}
		s.Ratio = roundRatio(ContrastRatio(c, bg))
		s.Passes = s.Ratio >= floor
		if !s.Passes {
			p.Illegible = append(p.Illegible, name)
		}
		p.Swatches = append(p.Swatches, s)
	}

	add("fg", t.Fg, ContrastFloor)
	add("cursor", t.Cursor, MarkFloor)
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
	t, found := tint.GetTint(id)
	if !found || t == nil {
		return palette, nil, nil, nil, false
	}
	return paletteOf(t), t.Fg, t.Bg, t.Cursor, true
}

// paletteOf is GetANSIPalette for a theme that is not the active one.
func paletteOf(t *tint.Tint) [16]color.Color {
	return [16]color.Color{
		t.Black, t.Red, t.Green, t.Yellow,
		t.Blue, t.Purple, t.Cyan, t.White,
		t.BrightBlack, t.BrightRed, t.BrightGreen, t.BrightYellow,
		t.BrightBlue, t.BrightPurple, t.BrightCyan, t.BrightWhite,
	}
}

// roundRatio trims a contrast ratio to two decimals. The third digit is noise
// against a floor quoted to one, and a stable number is one a caller can diff
// between two calls.
func roundRatio(r float64) float64 {
	return float64(int(r*100+0.5)) / 100
}
