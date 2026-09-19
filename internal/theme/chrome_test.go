package theme

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/exp/charmtone"
	tint "github.com/lrstanley/bubbletint/v2"
)

// useTheme registers a loaded theme and makes it current, restoring the
// previous state when the test ends.
func useTheme(t *testing.T, id string) func() {
	t.Helper()
	EnsureRegistry()
	wasEnabled := enabled
	prev := ""
	if cur := tint.Current(); cur != nil {
		prev = cur.ID
	}
	enabled = true
	if !tint.SetTintID(id) {
		t.Fatalf("theme %q did not register", id)
	}
	return func() {
		if prev != "" {
			tint.SetTintID(prev)
		}
		enabled = wasEnabled
	}
}

// writeThemeFile writes a theme JSON and loads it, returning its id.
func writeThemeFile(t *testing.T, body map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "amber.json")
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tn, err := LoadCustomThemeFile(path)
	if err != nil {
		t.Fatalf("LoadCustomThemeFile: %v", err)
	}
	EnsureRegistry()
	tint.Register(tn)
	t.Cleanup(func() { registerChrome(tn.ID, nil) })
	return tn.ID
}

func hexOf(t *testing.T, c color.Color) string {
	t.Helper()
	if c == nil {
		return "<nil>"
	}
	r, g, b, _ := c.RGBA()
	const hex = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+i*2] = hex[(v>>4)&0xf]
		out[2+i*2] = hex[v&0xf]
	}
	return string(out)
}

// TestAThemeNamesItsOwnAccent is the feature from #186: a palette whose accent
// is amber should get amber chrome without amber landing in bright_blue, where
// it would recolour every bold blue a program prints inside a pane.
//
// Negative control: dropping the chrome arm from UI() left the accent at the
// theme's bright_blue and this failed.
func TestAThemeNamesItsOwnAccent(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "amber",
		"bright_blue": "#5c5cff",
		"chrome":      map[string]string{"accent": "#ffb454"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, UI().Accent); got != "#ffb454" {
		t.Errorf("UI().Accent = %s, want the named #ffb454", got)
	}
	if got := hexOf(t, DockColorWindow()); got != "#ffb454" {
		t.Errorf("DockColorWindow() = %s, want the named #ffb454", got)
	}
	// The pane palette is untouched: that is the whole point.
	if got := hexOf(t, Current().BrightBlue); got != "#5c5cff" {
		t.Errorf("bright_blue = %s, want the theme's own #5c5cff", got)
	}
}

// TestAnAbsentChromeFieldStillDerives keeps every theme written before this
// existed rendering exactly as it did.
func TestAnAbsentChromeFieldStillDerives(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":           "partial",
		"bright_blue":  "#5c5cff",
		"bright_green": "#00ff00",
		"yellow":       "#cdcd00",
		"chrome":       map[string]string{"accent": "#ffb454"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, DockColorTerminal()); got != "#00ff00" {
		t.Errorf("DockColorTerminal() = %s, want the derived bright_green", got)
	}
	if got := hexOf(t, DockColorCopy()); got != "#cdcd00" {
		t.Errorf("DockColorCopy() = %s, want the derived yellow", got)
	}
}

// TestAThemeWithNoChromeObjectIsUnchanged is the same guarantee one level up:
// a file with no chrome at all registers none, so every role derives.
func TestAThemeWithNoChromeObjectIsUnchanged(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "plain",
		"bright_blue": "#5c5cff",
	})
	restore := useTheme(t, id)
	defer restore()

	if c := CurrentChrome(); c != nil {
		t.Fatalf("a theme with no chrome object registered %+v", c)
	}
	if got := hexOf(t, UI().Accent); got != "#5c5cff" {
		t.Errorf("UI().Accent = %s, want the derived bright_blue", got)
	}
}

// TestABadChromeColourCostsOnlyThatColour keeps a typo cheap. A theme is a file
// someone edits by hand, and losing the whole palette over one bad string is a
// worse answer than losing one role to the derivation it already had.
func TestABadChromeColourCostsOnlyThatColour(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "typo",
		"bright_blue": "#5c5cff",
		"yellow":      "#cdcd00",
		"chrome":      map[string]string{"accent": "#ffb454", "warning": "not a colour"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, UI().Accent); got != "#ffb454" {
		t.Errorf("UI().Accent = %s, want the good #ffb454", got)
	}
	if got := hexOf(t, DockColorCopy()); got != "#cdcd00" {
		t.Errorf("DockColorCopy() = %s, want the derived yellow after a bad warning", got)
	}
}

func TestParseChromeColorSpellings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"#ffb454", true},
		{"ffb454", true},
		{"#fb4", true},
		{"", false},
		{"   ", false},
		{"not a colour", false},
		{"#ggg", false},
		{"#ffb4544", false},
	} {
		got := parseChromeColor(tc.in) != nil
		if got != tc.want {
			t.Errorf("parseChromeColor(%q) parsed = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestAThemeNamesItsOwnSurface is the follow-up to #186: the dialogs and the
// which-key window are filled with one colour, and a theme may now name it.
// The rest of the ramp and the ink tiers come out of it at the spacings the
// constant palette has, because the five neutrals are one ramp and the three
// inks are a hierarchy of ratios on the surface they are written on.
//
// Negative control: dropping the neutral arm from UI() left Surface at the
// constant Char and this failed.
func TestAThemeNamesItsOwnSurface(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":     "walnut",
		"chrome": map[string]string{"surface": "#2b2118"},
	})
	restore := useTheme(t, id)
	defer restore()

	pal := UI()
	if got := hexOf(t, pal.Surface); got != "#2b2118" {
		t.Fatalf("UI().Surface = %s, want the named #2b2118", got)
	}
	if got := hexOf(t, pal.RowSel); got != hexOf(t, pal.Panel) {
		t.Errorf("RowSel = %s, want the panel step %s it has always shared", got, hexOf(t, pal.Panel))
	}

	// The ramp is held as the contrast ratios between its steps. The canvas
	// step is the one exception here: 1.44:1 below a surface this dark runs
	// past black and clamps, so it is checked for order rather than spacing.
	const tolerance = 0.02
	steps := []struct {
		name string
		got  float64
		want float64
	}{
		{"panel below surface", ContrastRatio(pal.Surface, pal.Panel), chromeRamp.panel},
		{"card above surface", ContrastRatio(pal.Card, pal.Surface), chromeRamp.card},
	}
	for _, s := range steps {
		if s.got < s.want-tolerance || s.got > s.want+tolerance {
			t.Errorf("%s measures %.3f:1, want %.3f:1", s.name, s.got, s.want)
		}
	}
	if !(lum(pal.Canvas) < lum(pal.Panel) && lum(pal.Panel) < lum(pal.Surface) && lum(pal.Surface) < lum(pal.Card)) {
		t.Errorf("ramp out of order: canvas %s panel %s surface %s card %s",
			hexOf(t, pal.Canvas), hexOf(t, pal.Panel), hexOf(t, pal.Surface), hexOf(t, pal.Card))
	}

	// The inks are re-derived rather than left where the constant ramp put
	// them, and they hold the constant palette's tiers on the new surface.
	for _, ink := range []struct {
		name string
		c    color.Color
		want float64
	}{
		{"Fg", pal.Fg, chromeRamp.fg},
		{"FgDim", pal.FgDim, chromeRamp.fgDim},
		{"FgMute", pal.FgMute, chromeRamp.fgMute},
	} {
		got := ContrastRatio(ink.c, pal.Surface)
		if got < ink.want-0.1 || got > ink.want+0.1 {
			t.Errorf("%s measures %.2f:1 on the named surface, want %.2f:1", ink.name, got, ink.want)
		}
	}
}

// TestALightSurfaceGetsDarkInk is what makes the field safe to open at all.
// Every ink in the constant palette was picked against a dark ground, so a
// naive version of this feature that only moved the ground would write
// near-white on cream. The tiers are chosen by measurement, so a light surface
// gets them in dark ink, and the floors the constant palette clears on each
// step of its ramp are cleared on the light one too.
func TestALightSurfaceGetsDarkInk(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":     "cream",
		"chrome": map[string]string{"surface": "#f4ecd8"},
	})
	restore := useTheme(t, id)
	defer restore()

	pal := UI()
	if lum(pal.Fg) > lum(pal.Surface) {
		t.Fatalf("Fg %s is lighter than the light surface %s", hexOf(t, pal.Fg), hexOf(t, pal.Surface))
	}
	if !(lum(pal.Fg) < lum(pal.FgDim) && lum(pal.FgDim) < lum(pal.FgMute)) {
		t.Errorf("ink tiers out of order: fg %s dim %s mute %s", hexOf(t, pal.Fg), hexOf(t, pal.FgDim), hexOf(t, pal.FgMute))
	}
	if !(lum(pal.Canvas) < lum(pal.Panel) && lum(pal.Panel) < lum(pal.Surface) && lum(pal.Surface) < lum(pal.Card)) {
		t.Errorf("ramp out of order: canvas %s panel %s surface %s card %s",
			hexOf(t, pal.Canvas), hexOf(t, pal.Panel), hexOf(t, pal.Surface), hexOf(t, pal.Card))
	}

	// The same floors TestQuietInkClearsItsGrounds holds the constant palette
	// to. On a light ramp the dark inks measure worst on the canvas rather
	// than the card, which is the case the derivation floors for.
	grounds := []struct {
		name string
		bg   color.Color
	}{{"canvas", pal.Canvas}, {"panel", pal.Panel}, {"surface", pal.Surface}, {"card", pal.Card}}
	for _, g := range grounds {
		for _, ink := range []struct {
			name string
			c    color.Color
		}{{"Fg", pal.Fg}, {"FgDim", pal.FgDim}} {
			if got := ContrastRatio(ink.c, g.bg); got < ContrastFloor {
				t.Errorf("%s on %s measures %.2f:1, want at least %.1f:1", ink.name, g.name, got, ContrastFloor)
			}
		}
	}
	for _, g := range grounds[:3] {
		if got := ContrastRatio(pal.FgMute, g.bg); got < MarkFloor {
			t.Errorf("FgMute on %s measures %.2f:1, want at least %.1f:1", g.name, got, MarkFloor)
		}
	}
	// The pill foreground is picked against the accent, not the surface, and
	// the accent still derives from the theme's bright_blue.
	if got := hexOf(t, pal.PillFg); got != hexOf(t, ContrastText(pal.Accent)) {
		t.Errorf("PillFg = %s, want the ink measured on the accent", got)
	}
}

// TestANamedStepOverridesItsDerivation is for the theme that wants an exact
// ramp: a step it names is taken as written, and the ones it leaves out are
// still derived from its surface.
func TestANamedStepOverridesItsDerivation(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":     "exact",
		"chrome": map[string]string{"surface": "#2b2118", "card": "#5a4a3a", "canvas": "#0a0805"},
	})
	restore := useTheme(t, id)
	defer restore()

	pal := UI()
	if got := hexOf(t, pal.Card); got != "#5a4a3a" {
		t.Errorf("Card = %s, want the named #5a4a3a", got)
	}
	if got := hexOf(t, pal.Canvas); got != "#0a0805" {
		t.Errorf("Canvas = %s, want the named #0a0805", got)
	}
	if got := ContrastRatio(pal.Surface, pal.Panel); got < chromeRamp.panel-0.02 || got > chromeRamp.panel+0.02 {
		t.Errorf("the unnamed panel step measures %.3f:1 below the surface, want the derived %.3f:1", got, chromeRamp.panel)
	}
}

// TestAThemeWithoutASurfaceKeepsTheConstantRamp is the contract the other six
// fields already have, for the four new ones: a theme that names an accent and
// nothing else renders on exactly the ramp and inks it did before.
func TestAThemeWithoutASurfaceKeepsTheConstantRamp(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":     "accent-only",
		"chrome": map[string]string{"accent": "#ffb454", "surface": "not a colour"},
	})
	restore := useTheme(t, id)
	defer restore()

	got := UI()
	_ = Initialize("")
	want := UI()
	for _, pair := range []struct {
		name     string
		got, ref color.Color
	}{
		{"Canvas", got.Canvas, want.Canvas}, {"Panel", got.Panel, want.Panel},
		{"Surface", got.Surface, want.Surface}, {"RowSel", got.RowSel, want.RowSel},
		{"Card", got.Card, want.Card}, {"Fg", got.Fg, want.Fg},
		{"FgDim", got.FgDim, want.FgDim}, {"FgMute", got.FgMute, want.FgMute},
	} {
		if hexOf(t, pair.got) != hexOf(t, pair.ref) {
			t.Errorf("%s = %s with no surface named, want the constant %s", pair.name, hexOf(t, pair.got), hexOf(t, pair.ref))
		}
	}
}

// TestTheConstantRampDerivesItself pins the derivation to the palette it was
// measured from: asked to build a ramp on the constant surface, it hands back
// the constant ramp to within a step of colour rounding. This is what makes
// "the ratios are the design" a checkable claim rather than a comment.
func TestTheConstantRampDerivesItself(t *testing.T) {
	c := &Chrome{Surface: charmtone.Char}
	c.deriveRamp()
	for _, step := range []struct {
		name      string
		got, want color.Color
	}{
		{"canvas", c.Canvas, charmtone.Pepper},
		{"panel", c.Panel, charmtone.BBQ},
		{"card", c.Card, charmtone.Iron},
	} {
		gr, gg, gb, _ := step.got.RGBA()
		wr, wg, wb, _ := step.want.RGBA()
		for i, d := range []int{int(gr>>8) - int(wr>>8), int(gg>>8) - int(wg>>8), int(gb>>8) - int(wb>>8)} {
			if d < -2 || d > 2 {
				t.Errorf("%s channel %d: derived %s, constant %s", step.name, i, hexOf(t, step.got), hexOf(t, step.want))
			}
		}
	}
	for _, ink := range []struct {
		name string
		got  color.Color
		want float64
	}{{"fg", c.fg, chromeRamp.fg}, {"dim", c.fgDim, chromeRamp.fgDim}, {"mute", c.fgMute, chromeRamp.fgMute}} {
		if got := ContrastRatio(ink.got, charmtone.Char); got < ink.want-0.05 || got > ink.want+0.05 {
			t.Errorf("%s measures %.2f:1 on the constant surface, want %.2f:1", ink.name, got, ink.want)
		}
	}
}

// lum is the WCAG relative luminance, read through the contrast ratio against
// black so the test does not need the overlay package's private function.
func lum(c color.Color) float64 {
	return ContrastRatio(c, color.Black)*0.05 - 0.05
}
