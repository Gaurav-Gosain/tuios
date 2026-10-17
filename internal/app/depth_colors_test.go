package app

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

// A theme colour stepped down to 256 or 16 colours has to stay the kind of
// colour it was. This walks every bundled theme through the paths that put a
// theme colour on screen: a pane's sixteen ANSI colours as its emulator
// resolves them, the painted grounds and their text colour, and the chrome
// palette's roles. What it checks is the colour the terminal is finally sent:
// the cell's colour after the frame writer's own step-down
// (colorprofile.Convert), because that is the step that turned nord's
// #2e3440 into the teal 23 and Latte's red into the grey 241.
//
// The rules, in OKLab:
//   - a neutral colour (chroma below neutralChroma) lands on a neutral entry:
//     the grey ramp, black, white or a cube grey at 256, and the terminal's
//     default or one of its four neutral slots at 16;
//   - a saturated colour (chroma at least saturatedChroma) at 256 lands on an
//     entry that is not neutral, with its hue within hueTolerance;
//   - at 16, a pane's ANSI slot i is sent as slot i, which the terminal paints
//     from its own palette, and any other saturated colour lands on a slot
//     that is not neutral.
//
// Colours between the two chroma bounds are not checked: the palette has no
// entry that is clearly the answer for them.
const (
	neutralChroma   = 0.035
	saturatedChroma = 0.06
	// The 256-colour cube has six steps per channel, so a dark or a pale
	// colour can be a long way in hue from every cube entry near it in
	// lightness. 40 degrees keeps the hue family (a red is never sent as an
	// orange-brown or a magenta) and passes every entry a careful choice
	// can reach.
	hueTolerance = 40.0
)

func TestThemeColorsKeepTheirKindAtEveryDepth(t *testing.T) {
	was := theme.ColorProfile()
	wasID := theme.CurrentThemeID()
	t.Cleanup(func() {
		theme.SetColorProfile(was)
		if wasID == "" {
			_ = theme.Initialize(theme.NoneID)
		} else {
			_ = theme.Initialize(wasID)
		}
	})

	ids := theme.AvailableThemes()
	if len(ids) < 50 {
		t.Fatalf("only %d bundled themes; the registry did not load", len(ids))
	}
	for _, p := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI} {
		t.Run(overlay.DepthOf(p).String(), func(t *testing.T) {
			var failures []string
			for _, id := range ids {
				failures = append(failures, depthFailures(t, id, p)...)
			}
			if len(failures) > 0 {
				n := len(failures)
				if n > 40 {
					failures = append(failures[:40], fmt.Sprintf("... and %d more", n-40))
				}
				t.Fatalf("%d theme colours changed kind at %s colours:\n%s",
					n, overlay.DepthOf(p), strings.Join(failures, "\n"))
			}
		})
	}
}

// depthFailures checks one theme at one profile and returns what failed.
func depthFailures(t *testing.T, id string, p colorprofile.Profile) []string {
	t.Helper()
	if err := theme.Initialize(id); err != nil {
		t.Fatalf("theme %s: %v", id, err)
	}
	cur := theme.Current()
	if cur == nil || cur.ID != id {
		t.Fatalf("theme %s did not become current", id)
	}

	// The roles as the theme designs them, before any step-down.
	theme.SetColorProfile(colorprofile.TrueColor)
	designed := theme.UI()
	theme.SetColorProfile(p)
	depth := overlay.DepthOf(p)

	var out []string
	check := func(what string, src, sent color.Color, slot int) {
		if src == nil {
			return
		}
		if msg := kindChange(src, p.Convert(sent), depth, slot); msg != "" {
			out = append(out, fmt.Sprintf("%s %s: %s", id, what, msg))
		}
	}

	// A pane's sixteen, as the emulator resolves SGR 30-37 and 90-97.
	w := &terminal.Window{Terminal: vt.New(16, 1)}
	w.UpdateThemeColors()
	var seq strings.Builder
	for i := range 16 {
		code := 30 + i
		if i >= 8 {
			code = 90 + i - 8
		}
		fmt.Fprintf(&seq, "\x1b[%dmx", code)
	}
	_, _ = w.Terminal.Write([]byte(seq.String()))
	for i, src := range theme.ANSIOrder(cur) {
		if src == nil {
			continue
		}
		cell := w.Terminal.CellAt(i, 0)
		if cell == nil {
			t.Fatalf("theme %s: no cell at %d", id, i)
		}
		check(fmt.Sprintf("pane slot %d %s", i, colorHexOf(src)), src, cell.Style.Fg, i)
	}

	// The painted grounds: the theme's background, and its foreground on it.
	g := resolveGround(config.BackgroundTheme, id)
	if g.on() {
		check("ground "+colorHexOf(theme.TerminalBg()), theme.TerminalBg(), g.bg, -1)
		if g.fg != nil {
			check("ground text "+colorHexOf(theme.TerminalFg()), theme.TerminalFg(), g.fg, -1)
		}
	}

	// The chrome roles that carry a colour rather than a ground the palette
	// designs for each depth by hand. A light theme's are left out: there each
	// ink is moved until it reads on the light rows (liftOnLight), in
	// truecolor by darkening, which drains its chroma, and at 256 by picking
	// the nearest hue that reads. Neither is a step-down of the other, so
	// there is no source to compare with.
	shown := theme.UI()
	roles := []struct {
		name      string
		src, sent color.Color
	}{
		{"accent", designed.Accent, shown.Accent},
		{"accent_bright", designed.AccentBright, shown.AccentBright},
		{"warn", designed.Warn, shown.Warn},
		{"success", designed.Success, shown.Success},
		{"info", designed.Info, shown.Info},
		{"warning", designed.Warning, shown.Warning},
	}
	if depth == overlay.Depth256 && !theme.GroundIsLight(cur.Bg) {
		for _, r := range roles {
			if overlay.IsNoColor(r.src) {
				continue
			}
			check("role "+r.name+" "+colorHexOf(r.src), r.src, r.sent, -1)
		}
	}
	return out
}

// kindChange describes how sent fails to keep src's kind, or is empty when it
// keeps it. slot is the ANSI slot src was drawn as, or -1.
func kindChange(src, sent color.Color, depth overlay.Depth, slot int) string {
	c := okLab(src)
	chroma := math.Hypot(c[1], c[2])
	if depth == overlay.Depth16 {
		if slot >= 0 {
			if b, ok := sent.(ansi.BasicColor); !ok || int(b) != slot {
				return fmt.Sprintf("sent as %s, not as slot %d", describe(sent), slot)
			}
			return ""
		}
		switch {
		case chroma < neutralChroma && !neutral16(sent):
			return fmt.Sprintf("neutral (chroma %.3f) sent as %s", chroma, describe(sent))
		case chroma >= saturatedChroma && neutral16(sent):
			return fmt.Sprintf("saturated (chroma %.3f) sent as neutral %s", chroma, describe(sent))
		}
		return ""
	}
	if sent == nil {
		return "sent as no colour"
	}
	idx, ok := sent.(ansi.IndexedColor)
	if !ok {
		return fmt.Sprintf("sent as %T at 256 colours", sent)
	}
	switch {
	case chroma < neutralChroma && !neutral256(idx):
		return fmt.Sprintf("neutral (chroma %.3f) sent as %d %s", chroma, idx, colorHexOf(idx))
	case chroma >= saturatedChroma:
		if neutral256(idx) {
			return fmt.Sprintf("saturated (chroma %.3f) sent as neutral %d %s", chroma, idx, colorHexOf(idx))
		}
		e := okLab(idx)
		if d := hueDistance(c, e); d > hueTolerance {
			return fmt.Sprintf("hue moved %.0f degrees: sent as %d %s", d, idx, colorHexOf(idx))
		}
	}
	return ""
}

// neutral256 reports whether a 256-colour index is a grey: the ramp, or a cube
// entry with three equal channels (black and white among them).
func neutral256(i ansi.IndexedColor) bool {
	if i >= 232 {
		return true
	}
	if i < 16 {
		return i == 0 || i == 7 || i == 8 || i == 15
	}
	n := int(i) - 16
	return n/36 == (n/6)%6 && (n/6)%6 == n%6
}

// neutral16 reports whether a 16-colour value is the terminal's default or one
// of its four neutral slots.
func neutral16(c color.Color) bool {
	if c == nil || overlay.IsNoColor(c) {
		return true
	}
	b, ok := c.(ansi.BasicColor)
	if !ok {
		return false
	}
	return b == 0 || b == 7 || b == 8 || b == 15
}

func hueDistance(a, b [3]float64) float64 {
	d := math.Abs(math.Atan2(a[2], a[1])-math.Atan2(b[2], b[1])) * 180 / math.Pi
	if d > 180 {
		d = 360 - d
	}
	return d
}

func describe(c color.Color) string {
	switch v := c.(type) {
	case nil:
		return "no colour"
	case ansi.BasicColor:
		return fmt.Sprintf("slot %d", v)
	case ansi.IndexedColor:
		return fmt.Sprintf("index %d", v)
	}
	if overlay.IsNoColor(c) {
		return "no colour"
	}
	return colorHexOf(c)
}

func colorHexOf(c color.Color) string {
	if c == nil {
		return "nil"
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// okLab is c in OKLab (L, a, b), written out here so the test does not share
// the conversion it is checking.
func okLab(c color.Color) [3]float64 {
	r8, g8, b8, _ := c.RGBA()
	lin := func(v uint32) float64 {
		x := float64(v>>8) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, b := lin(r8), lin(g8), lin(b8)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return [3]float64{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}
