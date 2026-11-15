package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// cellLight reports whether the cell's ground is light: a colour the cell
// sets whose luminance is over half, or the terminal's own ground, which the
// harness's terminal draws light under a light theme only through a set
// colour.
func cellLight(c tuitest.Cell) (bool, string) {
	switch c.Bg.Kind {
	case tuitest.ColorRGB:
		y := 0.2126*float64(c.Bg.R) + 0.7152*float64(c.Bg.G) + 0.0722*float64(c.Bg.B)
		return y > 128, fmt.Sprintf("rgb(%d,%d,%d)", c.Bg.R, c.Bg.G, c.Bg.B)
	case tuitest.ColorIndexed:
		i := int(c.Bg.Index)
		switch {
		case i >= 232:
			return i >= 244, fmt.Sprintf("index %d", i)
		case i >= 16:
			v := i - 16
			r, g, b := v/36, v/6%6, v%6
			return r+g+b >= 9, fmt.Sprintf("index %d", i)
		}
		return i == 7 || i == 15, fmt.Sprintf("index %d", i)
	}
	return false, "default"
}

// TestCockpitFollowsALightTheme opens the agents view at 256 colours and in
// truecolor in two light setups: a light theme, and no theme on a light
// terminal, which the rail and the dock already follow. The list, the focus
// and the card are light grounds in both.
//
// Negative control: on main, with no theme on a light terminal, the agents
// view draws its dark ramp under the light dock.
func TestCockpitFollowsALightTheme(t *testing.T) {
	host := buildHostTerm(t)
	for _, d := range []chromeDepth{chromeDepths[1], chromeDepths[2]} {
		for _, look := range []struct{ name, conf string }{
			{"theme", "[appearance]\ntheme = \"catppuccin_latte\"\n"},
			{"host", ""},
		} {
			t.Run(d.name+"-"+look.name, func(t *testing.T) {
				_, base, _, _ := startCockpitFleetWith(t, 120, 40, d.env, look.conf)
				opts := startOpts{args: []string{"attach", "home"}, cols: 120, rows: 40, shippedLooks: true, env: d.env}
				if look.name == "host" {
					// A light terminal that answers OSC 11 with its ground.
					opts.wrap = []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a", "--"}
				}
				term := startIn(t, base, opts)
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
					t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
				}
				windowManagementMode(t, term)
				sendKeys(t, term, tuitest.Ctrl('b'), "i")
				waitText(t, term, "the agents view", "Ready for review", "Checkpoints", "Allow Bash?")
				checkCockpitLight(t, term, d.name+"-"+look.name)
			})
		}
	}
}

// checkCockpitLight checks that the agents view's grounds are light beside
// three words it draws: the list's group heading, the focus's tab and the
// card's question. It saves the frame as name.
func checkCockpitLight(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	_ = term.WaitStable(uiTimeout)
	s := term.Screen()
	savePNG(t, s, hostGroundPalette("#2a2a2a", "#fdf6e3"), artifactDir(t), "cockpit-light-"+name)
	saveFrame(t, term, "cockpit-light-"+name)
	for _, word := range []string{"Ready for review", "Checkpoints", "Allow Bash?"} {
		x, y := -1, -1
		for row, line := range strings.Split(s.Text(), "\n") {
			if i := strings.Index(line, word); i >= 0 {
				x, y = len([]rune(line[:i])), row
				break
			}
		}
		if y < 0 {
			t.Fatalf("%q is not on screen:\n%s", word, term.Snapshot())
		}
		if light, what := cellLight(s.Cell(x+len(word)+1, y)); !light {
			t.Errorf("ASSERTION: the ground beside %q is %s, not light, at %s", word, what, name)
		}
	}
}
