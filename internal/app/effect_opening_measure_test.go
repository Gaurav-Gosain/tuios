package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
)

// This is how effectOpeningFrames was measured, kept so it can be measured
// again. TestEffectOpeningTableCoversEveryEffect fails when the engine gains an
// effect, and the person it fails on needs a number rather than a guess.
//
// Run it and paste the map it prints over the one in effect_picker.go:
//
//	TUIOS_MEASURE=1 go test ./internal/app -run TestMeasureEffectOpenings -v
//
// It is skipped otherwise. It builds every effect five times over an 80x24
// screen, which is two seconds nobody needs on an ordinary run.

// TestMeasureEffectOpenings measures how long each effect hides the screen.
//
// The metric is the first frame at which nine cells in ten of the capture's
// glyph cells carry their captured symbol, in their captured place, in their
// captured foreground colour. Colour is in it because of burn: burn holds every
// character in its final position from the first frame, so a symbols-only
// metric calls it instant while what is on screen is a flat grey sheet for
// eleven seconds. The dark column is a coarser question, the leading run of
// frames showing under a tenth of the screen's glyphs, which is what "it cuts
// to black" means for sweep and slide.
func TestMeasureEffectOpenings(t *testing.T) {
	if os.Getenv("TUIOS_MEASURE") == "" {
		t.Skip("set TUIOS_MEASURE=1 to measure")
	}
	const cols, rows, runs, maxFrames = 80, 24, 5, 4000

	win := newTestWindow(t, "measure-0001", cols, rows)
	m := newTestOS(win)
	m.Width, m.Height = cols, rows
	m.EffectiveWidth, m.EffectiveHeight = cols, rows

	// A screen with the shape of a working one: a prompt, a run of output, and
	// colour on some of it but not all.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[32m~/dev/tuios\x1b[0m on \x1b[35mmain\x1b[0m\r\n$ go test ./internal/app\r\n"))
	for i := range 12 {
		_, _ = win.Terminal.Write(fmt.Appendf(nil,
			"ok  \tgithub.com/Gaurav-Gosain/tuios/internal/pkg%02d\t\x1b[33m0.0%02ds\x1b[0m\r\n", i, i))
	}
	_, _ = win.Terminal.Write([]byte("$ "))
	win.UnlockIO()
	win.MarkContentDirty()

	grid := m.composedGrid(0, 0, cols, rows)
	if grid == nil {
		t.Fatal("no composed screen to measure over")
	}
	capture := screensaverCells(grid)

	type coord struct{ x, y int }
	type look struct {
		symbol string
		fg     tfx.Color
	}
	want := map[coord]look{}
	for y, row := range capture {
		for x, c := range row {
			if c.Symbol != "" && c.Symbol != " " {
				want[coord{x, y}] = look{c.Symbol, c.Fg}
			}
		}
	}
	t.Logf("capture %dx%d, %d glyph cells", grid.Cols, grid.Rows, len(want))

	opening := map[string][]int{}
	dark := map[string][]int{}
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
			open, black, frame := -1, -1, 0
			for frame < maxFrames {
				if open < 0 || black < 0 {
					match, visible := 0, 0
					for y, line := range engine.FrameRows() {
						for x, v := range line {
							if v == nil || v.Symbol == "" || v.Symbol == " " {
								continue
							}
							visible++
							if w, ok := want[coord{x, y}]; ok && v.Symbol == w.symbol &&
								v.Colors.HasFg && v.Colors.Fg == w.fg {
								match++
							}
						}
					}
					if open < 0 && float64(match) >= 0.9*float64(len(want)) {
						open = frame
					}
					if black < 0 && float64(visible) >= 0.1*float64(len(want)) {
						black = frame
					}
				}
				if !effect.Advance(engine) {
					break
				}
				frame++
			}
			opening[name] = append(opening[name], max(open, 0))
			dark[name] = append(dark[name], max(black, 0))
			length[name] = append(length[name], frame)
		}
	}

	names := tfx.Names()
	t.Log("name | opening frames | dark frames | whole run | opening seconds at 60fps")
	for _, name := range names {
		t.Logf("%s | %d | %d | %d | %.1f", name,
			median(opening[name]), median(dark[name]), median(length[name]),
			float64(median(opening[name]))/60)
	}

	var b strings.Builder
	b.WriteString("\nvar effectOpeningFrames = map[string]int{\n")
	for _, name := range names {
		fmt.Fprintf(&b, "\t%q: %d,\n", name, median(opening[name]))
	}
	b.WriteString("}")
	t.Log("paste over effectOpeningFrames:" + b.String())
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
