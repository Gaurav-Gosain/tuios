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

func measureReferenceScreen(t testing.TB, win interface{ Write([]byte) (int, error) }) {
	t.Helper()
	_, _ = win.Write([]byte("\x1b[32m~/dev/tuios\x1b[0m on \x1b[35mmain\x1b[0m\r\n$ go test ./internal/app\r\n"))
	for i := range 12 {
		_, _ = win.Write(fmt.Appendf(nil,
			"ok  \tgithub.com/Gaurav-Gosain/tuios/internal/pkg%02d\t\x1b[33m0.0%02ds\x1b[0m\r\n", i, i))
	}
	_, _ = win.Write([]byte("$ "))
}

type measureCoord struct{ x, y int }
type measureLook struct {
	symbol string
	fg     tfx.Color
	bg     tfx.Color
	hasBg  bool
}

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

func measureContrast(fg, bg tfx.Color) float64 {
	lighter, darker := measureLuminance(fg), measureLuminance(bg)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

const measureReadableContrast = 3.0

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
			engine, ok := screensaverBuild(capture, grid.Cols, grid.Rows, effect, d.NeedsFillCharacters, defaultSaverSettings())
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

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[len(s)/2]
}
