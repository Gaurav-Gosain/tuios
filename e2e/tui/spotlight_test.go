package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	spotlightTopCorner    = "╭"
	spotlightBottomCorner = "╰"
)

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

func spotlightProbe(t *testing.T, body string) (lit, far, farBorder tuitest.Cell) {
	t.Helper()
	term, at := spotlightMarkers(t, body)
	s := term.Screen()
	return s.Cell(at.botCol, at.botRow), s.Cell(at.topCol, at.topRow), s.Cell(at.topCol, at.borderRow)
}

type spotlightMarks struct {
	topCol, topRow int
	botCol, botRow int
	borderRow      int
}

func spotlightMarkers(t *testing.T, body string) (*tuitest.Terminal, spotlightMarks) {
	t.Helper()
	base := spotlightConfigFile(t, body)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf '%sTOP\n' "$(echo INK)"; printf '\n%.0s' $(seq 1 12); printf '%sBOT\n' "$(echo INK)"`,
		"INKBOT", shellTimeout)

	return term, findSpotlightMarks(t, term)
}

func findSpotlightMarks(t *testing.T, term *tuitest.Terminal) spotlightMarks {
	t.Helper()
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
		if strings.Contains(s.Line(row), spotlightTopCorner) && borderRow < 0 {
			borderRow = row
		}
	}
	if topRow < 0 || botRow < 0 || borderRow < 0 {
		t.Fatalf("a marker or the pane's top border is not on screen\n%s", term.Snapshot())
	}
	if botRow-topRow < 10 {
		t.Fatalf("the two markers are %d rows apart; too close to be one inside the beam "+
			"and one outside\n%s", botRow-topRow, term.Snapshot())
	}
	return spotlightMarks{
		topCol: topCol, topRow: topRow,
		botCol: botCol, botRow: botRow,
		borderRow: borderRow,
	}
}

const spotlightTheme = "[appearance]\ntheme = \"catppuccin_mocha\"\n"

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
	const ground = 30 + 30 + 46
	if got := spotlightLight(t, beamFar.Bg); got > ground/2 {
		t.Errorf("the background outside the beam carries %d of light against the "+
			"ground's %d; the screen still reads as lit", got, ground)
	}
}

func spotlightLight(t *testing.T, c tuitest.Color) int {
	t.Helper()
	switch c.Kind {
	case tuitest.ColorRGB:
		return int(c.R) + int(c.G) + int(c.B)
	case tuitest.ColorIndexed:
		switch {
		case c.Index >= 232:
			return 3 * (8 + 10*int(c.Index-232))
		case c.Index >= 16:
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
// It is B, for beam, in window mode: the beam is switched on while somebody is
// watching the screen, and a three-keystroke chord is the wrong shape for that.
// Capital, because a lower case b started words typed into window mode by
// mistake.
//
// Pressed through the real terminal rather than read off the table, because a
// bound key and a reachable key are different claims. The second press proves
// it is a toggle and not a one-way switch.
func TestSpotlightTogglesFromOneKeyInWindowMode(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B: %v", err)
	}
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("pressing B in window mode did not turn the spotlight on: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B again: %v", err)
	}
	if err := term.WaitForText(spotlightOffMessage, uiTimeout); err != nil {
		t.Fatalf("pressing B again did not turn the spotlight off: %v\n%s", err, term.Snapshot())
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
	if err := term.WaitForText("Spotlight is on", uiTimeout); err != nil {
		t.Fatalf("the palette row did not turn the spotlight on: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the palette toggled the spotlight")
}

const spotlightNoTheme = "[appearance]\ntheme = \"\"\n"

func spotlightNoThemeBeam(dim int) string {
	return spotlightNoTheme + "\n[spotlight]\nenabled = true\nradius = 5\nfollow = \"cursor\"\ndim = " +
		strconv.Itoa(dim) + "\n"
}

// TestSpotlightDimsAThemelessScreenByTheSetting is the maintainer's report read
// off a real screen with no theme set.
//
// The pane border carries a colour tuios chose and can therefore scale. It has
// to reach the host darker with the beam on, and darker again at the higher
// setting. The second half is the report itself: the two settings used to draw
// the same frame.
//
// The low end is 25, not 10. The test terminal has 256 colours, and one step
// of the colour cube is about 16% of the light. A 10% dim of the terminal mode
// border #91e592 lands on the same cube colour, 114, as the border itself.
func TestSpotlightDimsAThemelessScreenByTheSetting(t *testing.T) {
	_, _, plain := spotlightProbe(t, spotlightNoTheme)
	_, _, low := spotlightProbe(t, spotlightNoThemeBeam(25))
	_, _, high := spotlightProbe(t, spotlightNoThemeBeam(95))

	if plain.Fg == low.Fg {
		t.Errorf("with no theme the far border was painted %+v with the beam off and %+v "+
			"with it on; the chrome outside the light was not dimmed", plain.Fg, low.Fg)
	}
	plainLight := spotlightLight(t, plain.Fg)
	lowLight := spotlightLight(t, low.Fg)
	highLight := spotlightLight(t, high.Fg)
	if lowLight >= plainLight {
		t.Errorf("dim 25 left the far border at %d against %d with the beam off; "+
			"the light did not go down", lowLight, plainLight)
	}
	if highLight >= lowLight {
		t.Errorf("dim 25 left the far border at %d and dim 95 at %d; the setting draws "+
			"the same frame at both ends", lowLight, highLight)
	}
}

// TestSpotlightGoesFaintOnWhatItCannotResolve is the other half of the mixed
// screen, and the reason "mixed" is the honest answer rather than a compromise.
//
// The marker is text the shell printed with no colour of its own, which is most
// of a real screen. With no theme there is nothing to stand in for the colour
// the host paints it with, so no darker version of it can be written. SGR 2 is
// what a terminal can do instead, and it is what the cell has to come back
// carrying.
func TestSpotlightGoesFaintOnWhatItCannotResolve(t *testing.T) {
	_, plainFar, _ := spotlightProbe(t, spotlightNoTheme)
	_, beamFar, _ := spotlightProbe(t, spotlightNoThemeBeam(95))

	if plainFar.Faint {
		t.Fatalf("the marker is already faint with the beam off; it cannot show what the beam did")
	}
	if !beamFar.Faint {
		t.Error("with no theme a cell carrying no colour was left at full brightness outside the beam")
	}
	if beamFar.Fg.Kind != 0 {
		t.Errorf("the pass invented a colour for a cell it cannot resolve: %+v", beamFar.Fg)
	}
}

// TestSpotlightKeepsAThemedScreenOffTheFaintPath is the no-regression half. A
// theme means tuios owns the sixteen and knows the ground, so every cell is
// scaled and nothing falls back to SGR 2.
func TestSpotlightKeepsAThemedScreenOffTheFaintPath(t *testing.T) {
	_, beamFar, beamBorder := spotlightProbe(t, spotlightOn)

	if beamFar.Faint {
		t.Error("a themed cell outside the beam was put on SGR 2 rather than dimmed")
	}
	if beamBorder.Faint {
		t.Error("themed chrome outside the beam was put on SGR 2 rather than dimmed")
	}
	if beamFar.Fg.Kind == 0 || beamFar.Bg.Kind == 0 {
		t.Errorf("a themed cell outside the beam came back with no colour: fg %+v bg %+v",
			beamFar.Fg, beamFar.Bg)
	}
}

const shakeConfig = spotlightTheme + "\n[spotlight]\nradius = 5\nshake = true\n"

func shakePointer(t *testing.T, term *tuitest.Terminal, turns int) {
	t.Helper()
	const row, mid, leg = 20, 60, 20
	at, dir := mid, 1
	mouseHover(t, term, at, row)
	for range turns + 1 {
		at += dir * leg
		dir = -dir
		mouseHover(t, term, at, row)
	}
}

// TestShakingTheMouseTogglesTheSpotlight is the gesture, end to end. The
// notification is the assertion because it is what a person sees, and it is the
// same line the key shows.
func TestShakingTheMouseTogglesTheSpotlight(t *testing.T) {
	base := spotlightConfigFile(t, shakeConfig)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	shakePointer(t, term, 12)
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("shaking the pointer did not turn the beam on: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after shaking the spotlight on")
}

const noShakeBudget = 2 * time.Second

// TestSweepingTheMouseLeavesTheSpotlightAlone is the negative half, driven the
// same way. Crossing the screen and coming back is the pointer movement people
// make all day, and it is as fast and as wide as a shake.
func TestSweepingTheMouseLeavesTheSpotlightAlone(t *testing.T) {
	base := spotlightConfigFile(t, shakeConfig)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	for col := 10; col <= 110; col += 10 {
		mouseHover(t, term, col, 20)
	}
	for col := 110; col >= 10; col -= 10 {
		mouseHover(t, term, col, 20)
	}
	if err := term.WaitForText("Spotlight is", noShakeBudget); err == nil {
		t.Errorf("a sweep across the screen and back toggled the beam\n%s", term.Snapshot())
	}

	shakePointer(t, term, 12)
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("the pointer never reached the program at all: %v\n%s", err, term.Snapshot())
	}
}

// TestShakingTheMouseWorksWithLinkHoverOff is what pins the filter clause.
//
// The whitelist has a clause for link hover that passes bare motion over pane
// content, and with links on (the default), that clause carries the
// shake by accident. Turn links off, as anyone who wants the CPU guard does,
// and the gesture has nothing left but its own clause.
func TestShakingTheMouseWorksWithLinkHoverOff(t *testing.T) {
	const noLinks = spotlightTheme + "links = \"off\"\n" +
		"\n[spotlight]\nradius = 5\nshake = true\n"
	base := spotlightConfigFile(t, noLinks)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	shakePointer(t, term, 12)
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("shaking the pointer with links off did not turn the beam on; "+
			"the motion filter is dropping every event the gesture needs: %v\n%s",
			err, term.Snapshot())
	}
}

// TestTheShakeGestureIsOffByDefault. It ships off, and the same shake against a
// client that did not ask for it must leave the screen alone.
func TestTheShakeGestureIsOffByDefault(t *testing.T) {
	base := spotlightConfigFile(t, spotlightTheme)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	shakePointer(t, term, 12)
	if err := term.WaitForText("Spotlight is", noShakeBudget); err == nil {
		t.Errorf("the shake fired with spotlight.shake unset, which is how it ships\n%s",
			term.Snapshot())
	}

	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B: %v", err)
	}
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("the key did not turn the beam on: %v\n%s", err, term.Snapshot())
	}
}
