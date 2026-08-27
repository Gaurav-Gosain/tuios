package app

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
)

// This is how the frames column of effectOpenings was measured, kept so it can
// be measured
// again. TestEffectOpeningTableCoversEveryEffect fails when the engine gains an
// effect, and the person it fails on needs a number rather than a guess.
//
// Run it and paste the map it prints over the one in effect_picker.go:
//
//	TUIOS_MEASURE=1 go test ./internal/app -run TestMeasureEffectOpenings -v
//
// It is skipped otherwise. It builds every effect five times over an 80x24
// screen, which is a few seconds nobody needs on an ordinary run.

// measureReferenceScreen writes the reference screen the table is measured
// over: a prompt, a run of output, and colour on some of it but not all. It is
// one screen at one size, and the picker only takes a band from it. See the
// comment on effectOpenings.
func measureReferenceScreen(t testing.TB, win interface{ Write([]byte) (int, error) }) {
	t.Helper()
	_, _ = win.Write([]byte("\x1b[32m~/dev/tuios\x1b[0m on \x1b[35mmain\x1b[0m\r\n$ go test ./internal/app\r\n"))
	for i := range 12 {
		_, _ = win.Write(fmt.Appendf(nil,
			"ok  \tgithub.com/Gaurav-Gosain/tuios/internal/pkg%02d\t\x1b[33m0.0%02ds\x1b[0m\r\n", i, i))
	}
	_, _ = win.Write([]byte("$ "))
}

// measureCoord and measureLook are one captured cell: what it showed and what
// colour it showed it in.
type measureCoord struct{ x, y int }
type measureLook struct {
	symbol string
	fg     tfx.Color
	bg     tfx.Color
	hasBg  bool
}

// measureLuminance is the WCAG relative luminance of a colour.
func measureLuminance(c tfx.Color) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// measureContrast is the WCAG contrast ratio between two colours, from 1 (the
// same colour) to 21 (black against white).
func measureContrast(fg, bg tfx.Color) float64 {
	lighter, darker := measureLuminance(fg), measureLuminance(bg)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// measureReadableContrast is the ratio at which a character is taken to be
// readable. 3:1 is the WCAG floor for large text, and it is the level that
// separates burn from highlight: see TestMeasureEffectOpenings.
const measureReadableContrast = 3.0

// measureReadable counts the captured glyphs a frame gives back: the ones
// showing their captured symbol, in their captured place, in a colour that
// stands off the background they are drawn on.
func measureReadable(engine *tfx.Engine, want map[measureCoord]measureLook) int {
	readable := 0
	for y, line := range engine.FrameRows() {
		for x, cell := range line {
			if cell == nil || cell.Symbol == "" || cell.Symbol == " " {
				continue
			}
			captured, hit := want[measureCoord{x, y}]
			if !hit || cell.Symbol != captured.symbol {
				continue
			}
			fg, bg := captured.fg, tfx.Color{}
			if cell.Colors.HasFg {
				fg = cell.Colors.Fg
			}
			switch {
			case cell.Colors.HasBg:
				bg = cell.Colors.Bg
			case captured.hasBg:
				bg = captured.bg
			}
			if measureContrast(fg, bg) >= measureReadableContrast {
				readable++
			}
		}
	}
	return readable
}

// TestMeasureEffectOpenings measures how long each effect hides the screen.
//
// The metric is the last frame at which fewer than nine cells in ten of the
// capture's glyph cells are readable, plus one: the frame the screen is back
// and stays back. A cell is readable when it shows its captured symbol, in its
// captured place, in a colour that stands off the background it is drawn on.
//
// Both halves of that are load bearing, and each one alone gets an effect
// wrong.
//
// Symbols alone call burn instant. burn holds every character in its final
// position from the first frame and darkens it towards the background, so what
// is on screen is a flat sheet for eleven seconds that a symbols-only metric
// scores as a perfect screen.
//
// Colour equality overcorrects. highlight never moves a character and never
// hides one: it only brightens the colour each is already drawn in. Requiring
// the captured colour back scored it as a screen you cannot read for eighty
// frames, and thunderstorm, which also only recolours, the same way for nine
// hundred. Contrast separates the two, because highlight and thunderstorm keep
// their characters legible against what is behind them and burn does not.
//
// The first readable frame is not the metric either. rings, vhstape and waves
// all start from the untouched screen and take it away afterwards, so the first
// frame that reads well is frame 0 for all three, while the screen is in fact
// gone for eleven, twelve and seven seconds. The last unreadable frame is what
// a user waits for.
//
// The numbers are the median of five runs; the effects that place characters at
// random vary by a few percent between runs, and the rest are identical every
// time.
func TestMeasureEffectOpenings(t *testing.T) {
	if os.Getenv("TUIOS_MEASURE") == "" {
		t.Skip("set TUIOS_MEASURE=1 to measure")
	}
	const cols, rows, runs, maxFrames = 80, 24, 5, 8000

	win := newTestWindow(t, "measure-0001", cols, rows)
	m := newTestOS(win)
	m.Width, m.Height = cols, rows
	m.EffectiveWidth, m.EffectiveHeight = cols, rows

	win.LockIO()
	measureReferenceScreen(t, win.Terminal)
	win.UnlockIO()
	win.MarkContentDirty()

	grid := m.composedGrid(0, 0, cols, rows)
	if grid == nil {
		t.Fatal("no composed screen to measure over")
	}
	capture := screensaverCells(grid)

	want := map[measureCoord]measureLook{}
	for y, row := range capture {
		for x, c := range row {
			if c.Symbol != "" && c.Symbol != " " {
				want[measureCoord{x, y}] = measureLook{c.Symbol, c.Fg, c.Bg, c.HasBg}
			}
		}
	}
	t.Logf("capture %dx%d, %d glyph cells", grid.Cols, grid.Rows, len(want))

	opening := map[string][]int{}
	floor := map[string][]int{}
	length := map[string][]int{}
	for range runs {
		for _, name := range tfx.Names() {
			d, _ := tfx.Lookup(name)
			effect := d.New()
			engine, ok := screensaverBuild(capture, grid.Cols, grid.Rows, effect, d.NeedsFillCharacters)
			if !ok {
				t.Errorf("%s will not build", name)
				continue
			}
			settle, frame, worst := 0, 0, len(want)
			for frame < maxFrames {
				readable := measureReadable(engine, want)
				worst = min(worst, readable)
				if float64(readable) < 0.9*float64(len(want)) {
					settle = frame + 1
				}
				if !effect.Advance(engine) {
					break
				}
				frame++
			}
			opening[name] = append(opening[name], settle)
			floor[name] = append(floor[name], 100*worst/len(want))
			length[name] = append(length[name], frame)
		}
	}

	names := tfx.Names()
	t.Log("name | opening frames | lowest readable share | whole run | opening seconds at 60fps")
	for _, name := range names {
		t.Logf("%s | %d | %d%% | %d | %.1f", name,
			median(opening[name]), median(floor[name]), median(length[name]),
			float64(median(opening[name]))/60)
	}

	// keepsScreen is not measured here. It is a claim about every screen, not
	// this one, so TestEffectsWithNoOpeningNeverHideTheScreen is what settles
	// it, and it is left alone by a re-measurement.
	var b strings.Builder
	b.WriteString("\nvar effectOpenings = map[string]effectOpening{\n")
	for _, name := range names {
		keeps := ""
		if effectOpenings[name].keepsScreen {
			keeps = ", keepsScreen: true"
		}
		fmt.Fprintf(&b, "\t%q: {frames: %d%s},\n", name, median(opening[name]), keeps)
	}
	b.WriteString("}")
	t.Log("paste over effectOpenings:" + b.String())
}

// median is the middle of a set of runs. The effects that place characters at
// random vary by a few percent between runs and the rest are identical, so the
// middle is a stabler number than any one run.
func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[len(s)/2]
}
