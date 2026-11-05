package lint

import (
	"fmt"
	"math"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/guios/roles"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The floors of the theme contrast lint, as WCAG contrast ratios. A tier
// and the unfocused pane's text are designed for 5:1 and 4.8:1 on the
// token, and hold 4.5:1, the AA floor, on the frame's pixels. A fill's ink
// is designed for 5:1 and reaches it on every theme (roles.InkFill).
const (
	floorText = 4.5
	floorInk  = 5.0
)

// TestThemesKeepTheirContrast is the theme contrast lint. It builds the
// colour roles of every look guios draws, every tuios theme and Paper in
// the light and the dark, and checks:
//
//   - text, text2 and text3 on the card and on the window's ground, at
//     floorText;
//   - the ink on an accent fill, at rest, under the pointer and held, and
//     on the needs-you badge, at floorInk;
//   - the theme's foreground in a pane the person is not in, under the
//     veil, at floorText, or at the theme's own contrast where the theme
//     itself is lower.
//
// A failure is one line: the theme, the pair and the ratio.
func TestThemesKeepTheirContrast(t *testing.T) {
	type look struct {
		name   string
		term   *roles.Term
		pal    *roles.Palette
		ground roles.Color
	}
	var looks []look
	for _, id := range theme.AvailableThemes() {
		th, ok := roles.FromTheme(id)
		if !ok {
			continue
		}
		p, _, ground := roles.Look(th)
		looks = append(looks, look{id, th, p, ground})
	}
	for _, dark := range []bool{false, true} {
		th, p, ground := roles.Paper(dark)
		name := "paper light"
		if dark {
			name = "paper dark"
		}
		looks = append(looks, look{name, th, p, ground})
	}
	if len(looks) < 300 {
		t.Fatalf("built %d looks: the theme registry is not the one guios offers", len(looks))
	}
	for _, l := range looks {
		p, card := l.pal, l.term.Background
		check := func(pair string, fg, bg roles.Color, floor float64) {
			if r := roles.Contrast(fg, bg); r < floor-1e-9 {
				t.Errorf("%s: %s is %.2f:1, under %.1f:1", l.name, pair, r, floor)
			}
		}
		for _, tier := range []struct {
			name string
			c    roles.Color
		}{{"text", p.Text}, {"text2", p.TextMuted}, {"text3", p.TextFaint}} {
			check(tier.name+" on the card", tier.c, card, floorText)
			check(tier.name+" on the ground", tier.c, l.ground, floorText)
		}
		check("ink on the accent fill", p.AccentInk, p.AccentFill, floorInk)
		check("ink on the accent fill under the pointer", p.AccentInk, roles.AwayFrom(p.AccentFill, p.AccentInk, roles.HoverStep), floorInk)
		check("ink on the accent fill held down", p.AccentInk, roles.AwayFrom(p.AccentFill, p.AccentInk, roles.PressStep), floorInk)
		check("ink on the needs-you badge", p.AttnInk, p.AttnFill, floorInk)
		dim := p.CardDim.Over(l.ground)
		own := roles.Contrast(l.term.Foreground, dim)
		check("text in an unfocused pane", p.Veil.Over(l.term.Foreground), p.Veil.Over(dim), math.Min(floorText, own))
	}
	if testing.Verbose() {
		fmt.Printf("checked %d looks\n", len(looks))
	}
}
