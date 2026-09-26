package tuie2e

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inkDistance is how far a pixel is from the background, summed over the
// three channels.
func inkDistance(c, bg color.Color) int {
	r1, g1, b1, _ := c.RGBA()
	r2, g2, b2, _ := bg.RGBA()
	abs := func(a, b uint32) int {
		if a > b {
			return int(a-b) >> 8
		}
		return int(b-a) >> 8
	}
	return abs(r1, r2) + abs(g1, g2) + abs(b1, b2)
}

// TestScreenshotDrawsAStraightBorderWithoutNotches renders a column of
// U+2502 through `tuios screenshot` and reads the PNG back. A straight line
// has to be one even stroke. It used to be drawn as two half-cell arms that
// met in the middle of each cell, each anti-aliased at its own end, so where
// the middle fell between two pixels the stroke lost about a fifth of its ink
// there: every pane border in a capture had a faint notch in every row.
//
// How this could pass wrongly, written down first:
//   - The column could be the wrong one, an anti-aliased edge of the stroke
//     where some unevenness is expected. The column read is the one with the
//     most ink, which is inside the stroke.
//   - The run could be too short to cross a cell's middle, or be the prompt.
//     Six lines of the glyph are printed and the run read must be six cells
//     long.
//   - The ends of the run are the first and last cells' own ends, so a
//     quarter of a cell is left off each end.
//
// The PNG is saved under artifactDir.
//
// Negative control: with drawArms drawing U+2502 as two arms again, the
// check fails on a pixel in the middle of a cell.
func TestScreenshotDrawsAStraightBorderWithoutNotches(t *testing.T) {
	const session = "e2e-box"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-l",
		"clear; for i in 1 2 3 4 5 6; do printf '\\342\\224\\202\\n'; done\r"); err != nil {
		t.Fatalf("print the column: %v\n%s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		if strings.Count(pane, "│") >= 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed the column:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}

	out := filepath.Join(artifactDir(t), "border-column.png")
	if cliOut, err := tuiosCLI(t, base, "screenshot", "-s", session, "--format", "png",
		"--frame", "none", "--theme", "catppuccin_mocha", "--out", out, "--no-copy"); err != nil {
		t.Fatalf("tuios screenshot: %v\n%s", err, cliOut)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	bounds := img.Bounds()
	bg := img.At(bounds.Max.X-1, bounds.Max.Y-1)
	// The glyph is in the first column, so the stroke is in the first few
	// percent of the picture's width.
	best, bestInk := -1, 0
	for x := bounds.Min.X; x < bounds.Min.X+bounds.Dx()/20; x++ {
		ink := 0
		for y := bounds.Min.Y; y < bounds.Min.Y+bounds.Dy()/3; y++ {
			ink += inkDistance(img.At(x, y), bg)
		}
		if ink > bestInk {
			best, bestInk = x, ink
		}
	}
	if best < 0 {
		t.Fatalf("no stroke in the first columns of %s", out)
	}
	top, bottom := -1, -1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if inkDistance(img.At(best, y), bg) > 30 {
			if top < 0 {
				top = y
			}
			bottom = y
		} else if top >= 0 {
			break
		}
	}
	// Six cells of a picture no more than sixty rows tall.
	if top < 0 || bottom-top < 6*bounds.Dy()/60 {
		t.Fatalf("the stroke at x=%d runs from %d to %d, too short for six cells; see %s", best, top, bottom, out)
	}
	// A quarter of a cell, which is never under a sixtieth of the picture
	// over four.
	quarter := bounds.Dy() / 240
	full := 0
	for y := top; y <= bottom; y++ {
		full = max(full, inkDistance(img.At(best, y), bg))
	}
	for y := top + quarter; y <= bottom-quarter; y++ {
		if ink := inkDistance(img.At(best, y), bg); ink*100 < full*95 {
			t.Fatalf("the stroke at x=%d loses ink at y=%d: %d against %d along the rest of it; see %s",
				best, y, ink, full, out)
		}
	}
}
