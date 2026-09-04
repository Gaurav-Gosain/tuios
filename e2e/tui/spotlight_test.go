package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The spotlight is a colour transform, so the only place it can honestly be
// checked is the colour a host terminal was given. These read Cell.Fg off the
// real screen a real tuios wrote to, at one place inside the beam and one
// outside it.
//
// The two places are the pane's own top and bottom border corners, not text the
// shell printed. A corner sits at a fixed cell for a given geometry and carries
// a colour of its own, while shell output scrolls and is mostly at the terminal
// default, which made an earlier version of this read a cell that had moved.

const (
	spotlightTopCorner    = "╭"
	spotlightBottomCorner = "╰"
)

// spotlightConfigFile writes a config.toml into an isolated XDG root and
// returns the root.
func spotlightConfigFile(t *testing.T, body string) string {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return base
}

// spotlightProbe boots tuios with the given config, prints two markers far
// apart in the pane, and returns three cells: the lower marker, which is beside
// the cursor and so under the beam; the upper marker, which is far above it;
// and the pane's top border.
//
// Both markers are text the shell printed with no colour of its own, which is
// what most of a real screen is. tuios emits no colour for such a cell, so a
// pass that only touched cells already carrying one would dim the syntax
// highlighting and leave everything else at full brightness.
//
// A blank cell is not used for this. A space with a foreground looks exactly
// like a space without one, so the renderer is entitled to drop the colour on
// the way out and an assertion about it would be an assertion about nothing.
func spotlightProbe(t *testing.T, body string) (lit, far, farBorder tuitest.Cell) {
	t.Helper()
	term, at := spotlightMarkers(t, body)
	s := term.Screen()
	return s.Cell(at.botCol, at.botRow), s.Cell(at.topCol, at.topRow), s.Cell(at.topCol, at.borderRow)
}

// spotlightMarks is where spotlightMarkers found the three cells, so a test
// that has to act on the screen before it reads it can aim at them.
type spotlightMarks struct {
	topCol, topRow int
	botCol, botRow int
	borderRow      int
}

// spotlightMarkers is spotlightProbe up to the point of reading a cell: it
// boots tuios, prints the two markers, and returns the running terminal with
// the places they landed.
func spotlightMarkers(t *testing.T, body string) (*tuitest.Terminal, spotlightMarks) {
	t.Helper()
	base := spotlightConfigFile(t, body)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	// The markers are assembled by the shell, so the command line the harness
	// typed does not carry them and the rows found below are real output.
	runInShell(t, term,
		`printf '%sTOP\n' "$(echo INK)"; printf '\n%.0s' $(seq 1 12); printf '%sBOT\n' "$(echo INK)"`,
		"INKBOT", shellTimeout)

	s := term.Screen()
	_, rows := s.Size()
	topRow, topCol, botRow, botCol, borderRow := -1, -1, -1, -1, -1
	for row := range rows {
		if c := strings.Index(s.Line(row), "INKTOP"); c >= 0 {
			topRow, topCol = row, c
		}
		if c := strings.Index(s.Line(row), "INKBOT"); c >= 0 {
			botRow, botCol = row, c
		}
		// The pane's top border, which is above both markers and so further from
		// the cursor than either. The bottom border is beside the cursor and is
		// inside the light.
		if strings.Contains(s.Line(row), spotlightTopCorner) && borderRow < 0 {
			borderRow = row
		}
	}
	if topRow < 0 || botRow < 0 || borderRow < 0 {
		t.Fatalf("a marker or the pane's top border is not on screen\n%s", term.Snapshot())
	}
	// The beam is five rows. The two markers have to be further apart than that
	// or this measures one place twice.
	if botRow-topRow < 10 {
		t.Fatalf("the two markers are %d rows apart; too close to be one inside the beam "+
			"and one outside\n%s", botRow-topRow, term.Snapshot())
	}
	return term, spotlightMarks{
		topCol: topCol, topRow: topRow,
		botCol: botCol, botRow: botRow,
		borderRow: borderRow,
	}
}

const spotlightTheme = "[appearance]\ntheme = \"catppuccin_mocha\"\n"

// follow is pinned rather than left at the default. The beam follows the mouse
// unless told otherwise, and no test using this config moves a pointer, so a
// beam left at the default would sit where it was seeded and every assertion
// about the cursor would be about a beam that is not on it.
// TestSpotlightFollowsTheMouse is where the default is driven instead.
const spotlightOn = spotlightTheme + "\n[spotlight]\nenabled = true\nradius = 5\nfollow = \"cursor\"\n"

// TestSpotlightDimsAwayFromTheCursor is the on-screen form of the whole
// feature. The beam sits on the cursor at the foot of the pane, so the border
// at the head of it has to reach the host in a colour it does not have with the
// beam off.
func TestSpotlightDimsAwayFromTheCursor(t *testing.T) {
	_, _, plainBorder := spotlightProbe(t, spotlightTheme)
	_, _, beamBorder := spotlightProbe(t, spotlightOn)

	if plainBorder.Fg == beamBorder.Fg {
		t.Errorf("the far border was painted %+v with the beam off and %+v with it on; "+
			"nothing outside the light was dimmed", plainBorder.Fg, beamBorder.Fg)
	}
}

// TestSpotlightDimsTextLeftAtTheTerminalDefault. Most of what is on a real
// screen carries no colour of its own, and tuios emits none for it, so this is
// the case a fixture full of explicit SGR hides. The first version of the pass
// followed dim_unfocused's rule, which leaves a colourless cell alone, and so
// dimmed the syntax highlighting and left everything else at full brightness.
// Every unit test passed. This one failed on its first run.
//
// Negative control, run and confirmed failing: put that rule back (skip a cell
// whose foreground and background are both nil) and this fails while the rest
// of the suite passes.
func TestSpotlightDimsTextLeftAtTheTerminalDefault(t *testing.T) {
	_, plainFar, _ := spotlightProbe(t, spotlightTheme)
	_, beamFar, _ := spotlightProbe(t, spotlightOn)

	if plainFar.Fg.Kind != 0 {
		t.Fatalf("the marker already carries a colour with the beam off (%+v); "+
			"it cannot show what the beam did", plainFar.Fg)
	}
	if beamFar.Fg.Kind == 0 {
		t.Error("text at the terminal default outside the beam was left undimmed")
	}
}

// TestSpotlightTurnsTheBackgroundDownOnScreen is the maintainer's report, read
// off a real screen.
//
// The first version of the pass carried every colour toward the theme's own
// ground, and a cell that named no background was given none at all. So the
// text outside the beam went dark while the screen it sat on stayed exactly as
// bright as the screen inside the beam, at any setting up to the maximum. The
// beam did not read as a light, because nothing around it had been turned down.
//
// This reads the background of a marker the shell printed, which carries no
// colour of its own, and requires it to come back darker than the ground the
// theme paints it with.
func TestSpotlightTurnsTheBackgroundDownOnScreen(t *testing.T) {
	_, plainFar, _ := spotlightProbe(t, spotlightTheme)
	_, beamFar, _ := spotlightProbe(t, spotlightOn)

	if plainFar.Bg.Kind != 0 {
		t.Fatalf("the marker already carries a background with the beam off (%+v); "+
			"it cannot show what the beam did", plainFar.Bg)
	}
	if beamFar.Bg.Kind == 0 {
		t.Fatal("the marker outside the beam came back with no background at all; the " +
			"light was never turned down on it")
	}
	// catppuccin_mocha's base, which is what the host paints a cell that names
	// no background of its own. Half of it is the bar: the default leaves an
	// unlit cell at a quarter of its light, and the room between the two is
	// what the 256-colour palette this harness runs on costs.
	const ground = 30 + 30 + 46
	if got := spotlightLight(t, beamFar.Bg); got > ground/2 {
		t.Errorf("the background outside the beam carries %d of light against the "+
			"ground's %d; the screen still reads as lit", got, ground)
	}
}

// spotlightLight is how much light a cell colour carries, as the sum of its
// three channels.
//
// It resolves an indexed colour itself because this harness runs on
// TERM=xterm-256color with no COLORTERM, so tuios downsamples the frame and a
// dimmed colour reaches the screen as a palette entry. Reading the number
// through the downsample is the point: a 256-colour terminal is what a lot of
// people are on, and the beam has to work there too.
func spotlightLight(t *testing.T, c tuitest.Color) int {
	t.Helper()
	switch c.Kind {
	case tuitest.ColorRGB:
		return int(c.R) + int(c.G) + int(c.B)
	case tuitest.ColorIndexed:
		switch {
		case c.Index >= 232: // the 24-step greyscale ramp
			return 3 * (8 + 10*int(c.Index-232))
		case c.Index >= 16: // the 6x6x6 cube
			levels := [6]int{0, 95, 135, 175, 215, 255}
			i := int(c.Index - 16)
			return levels[i/36] + levels[(i/6)%6] + levels[i%6]
		default:
			t.Fatalf("colour %+v is one of the sixteen the host's own palette owns, so "+
				"there is no honest number for it", c)
		}
	}
	t.Fatalf("colour %+v carries no channels", c)
	return 0
}

// TestSpotlightFollowsTheMouse drives the default anchor.
//
// The beam follows the pointer unless the config says otherwise, so this sets
// nothing but enabled and radius, moves the pointer to the marker at the head
// of the pane, and requires that marker to be lit while the cursor at the foot
// of the pane, which the beam was seeded on, has gone dark.
func TestSpotlightFollowsTheMouse(t *testing.T) {
	const mouseBeam = spotlightTheme + "\n[spotlight]\nenabled = true\nradius = 5\n"
	term, at := spotlightMarkers(t, mouseBeam)

	mouseHover(t, term, at.topCol, at.topRow)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(at.topCol, at.topRow).Fg.Kind == 0
	}, shellTimeout); err != nil {
		t.Fatalf("the marker at (%d,%d) never came back to full brightness after the "+
			"pointer moved onto it: %v\n%s", at.topCol, at.topRow, err, term.Snapshot())
	}
	if bot := term.Screen().Cell(at.botCol, at.botRow); bot.Fg.Kind == 0 {
		t.Errorf("the marker at the cursor is still lit with the pointer %d rows above it; "+
			"the beam did not move", at.botRow-at.topRow)
	}
}

// TestSpotlightKeepsTheLightUndimmed is the half that stops the tests above
// passing for a pass that darkens the whole screen.
func TestSpotlightKeepsTheLightUndimmed(t *testing.T) {
	plainLit, _, _ := spotlightProbe(t, spotlightTheme)
	beamLit, _, _ := spotlightProbe(t, spotlightOn)

	if plainLit.Fg != beamLit.Fg || plainLit.Faint != beamLit.Faint {
		t.Errorf("the marker under the beam was painted %+v with the beam off and %+v with "+
			"it on; the light is dimming its own middle", plainLit.Fg, beamLit.Fg)
	}
}

// TestSpotlightTogglesFromOneKeyInWindowMode drives the key a person presses.
// It is b, for beam, in window mode: the beam is switched on while somebody is
// watching the screen, and a three-keystroke chord is the wrong shape for that.
//
// Pressed through the real terminal rather than read off the table, because a
// bound key and a reachable key are different claims. The second press proves
// it is a toggle and not a one-way switch.
func TestSpotlightTogglesFromOneKeyInWindowMode(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	if err := term.WaitForText("Spotlight: ON", uiTimeout); err != nil {
		t.Fatalf("pressing b in window mode did not turn the spotlight on: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b again: %v", err)
	}
	if err := term.WaitForText("Spotlight: OFF", uiTimeout); err != nil {
		t.Fatalf("pressing b again did not turn the spotlight off: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after toggling the spotlight")
}

// TestSpotlightIsInTheCommandPalette. The key is not guessable, so the palette
// is how anyone finds this. The row has to name the feature and the key.
func TestSpotlightIsInTheCommandPalette(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("the palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("spotlight"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitForText("Toggle spotlight", uiTimeout); err != nil {
		t.Fatalf("the palette does not offer the spotlight: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the palette row: %v", err)
	}
	if err := term.WaitForText("Spotlight on", uiTimeout); err != nil {
		t.Fatalf("the palette row did not turn the spotlight on: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the palette toggled the spotlight")
}
