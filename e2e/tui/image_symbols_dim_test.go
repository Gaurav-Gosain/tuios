package tuie2e

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestImageSymbolsDimEvenly draws a white disc as glyphs in a pane with a
// theme and dim_unfocused on, then moves the focus to a second pane. The disc
// must darken as one area: every sub-block of its inside stays close to every
// other, and darker than it was while the pane had the focus.
//
// The disc is white with a sparse light-grey speckle, so most glyph cells
// carry two near-white colours, as chafa's dithered output does. The bug this
// covers dimmed a glyph's foreground toward the cell's own background, which
// is near white too, while the background went to the pane's ground. The disc
// then broke into white and grey sub-blocks.
//
// How this could pass wrongly, written down first:
//   - The read could miss the disc. The origin comes from the line printed
//     above the picture, and the inside must hold enough cells to compare.
//   - The pane could not be dimmed at all, which also keeps the sub-blocks
//     even. The disc must be darker unfocused than focused.
//   - The cells could hold one colour each, so the old dim had nothing to
//     split. The focused read must show cells with two visible colours.
func TestImageSymbolsDimEvenly(t *testing.T) {
	for _, tc := range []struct {
		name, kind, colorterm string
		env                   []string
	}{
		{"octant-truecolor", "octant", "truecolor", nil},
		{"quadrant-truecolor", "quadrant", "truecolor", nil},
		{"octant-256", "octant", "", nil},
		// TERM=linux with no COLORTERM: sixteen colours and half blocks.
		{"half-16", "", "", []string{"TERM=linux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newSixelHost(false, false)
			cfg := symbolConfig{kind: tc.kind, tiled: true, appearance: "theme = \"dracula\"\ndim_unfocused = 50\n"}
			term, _ := startSymbolPaneWith(t, host, cfg, tc.colorterm, tc.env...)
			path := writeDiscPicture(t, t.TempDir())
			typeLine(t, term, "clear; printf '%s\\n' 'IMG''TOP'; cat "+path+"; echo; echo SH''OWN")
			if err := term.WaitForText("SHOWN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("screen never settled: %v", err)
			}
			art := artifactDir(t)
			focused := term.Screen()
			savePNG(t, focused, shot.XTermPalette(), art, "focused")
			was := discSubBlocks(focused, symbolOriginIn(t, focused))

			newWindow(t, term)
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("screen never settled unfocused: %v", err)
			}
			unfocused := term.Screen()
			savePNG(t, unfocused, shot.XTermPalette(), art, "unfocused")
			now := discSubBlocks(unfocused, symbolOriginIn(t, unfocused))

			t.Logf("%s: focused %s; unfocused %s", tc.name, was, now)
			if was.cells < 60 || now.cells < 60 {
				t.Fatalf("the disc's inside holds %d then %d cells: the picture is not where it was looked for\n%s", was.cells, now.cells, unfocused.Text())
			}
			if tc.colorterm == "truecolor" && was.split < 10 {
				t.Fatalf("only %d focused cells show two colours: the picture has nothing for a dim to split", was.split)
			}
			if now.meanLuma > 0.85*was.meanLuma {
				t.Errorf("the disc is not darker unfocused (luma %.0f, focused %.0f): the dim was not applied", now.meanLuma, was.meanLuma)
			}
			if now.spread > was.spread+12 {
				t.Errorf("the unfocused disc's sub-blocks are %.0f apart (focused %.0f): the disc streaks, %v next to %v",
					now.spread, was.spread, now.lightest, now.darkest)
			}
		})
	}
}

// discCX, discCY and discR place the disc in a picture of symPicCols by symPicRows
// cells: its centre, and its radius in pixels.
const (
	discCX, discCY = symPicCols * cellW / 2, symPicRows * cellH / 2
	discR          = 110
)

// writeDiscPicture writes a white disc with a light-grey speckle on black as
// a sixel file.
func writeDiscPicture(t *testing.T, dir string) string {
	t.Helper()
	w, h := symPicCols*cellW, symPicRows*cellH
	img := &vt.SixelImage{Width: w, Height: h, Pix: make([]uint16, w*h), Palette: make([]color.RGBA, vt.SixelMaxRegisters)}
	img.Palette[0] = color.RGBA{0, 0, 0, 255}
	img.Palette[1] = color.RGBA{255, 255, 255, 255}
	img.Palette[2] = color.RGBA{228, 228, 228, 255}
	for py := range h {
		for px := range w {
			v := uint16(1)
			if math.Hypot(float64(px-discCX), float64(py-discCY)) < discR {
				v = 2
				// About one pixel in seven, scattered, so most cells
				// hold some.
				if (px*7+py*13+px*py)%7 == 0 {
					v = 3
				}
			}
			img.Pix[py*w+px] = v
		}
	}
	seq := vt.EncodeSixel(img, image.Rect(0, 0, w, h), w, h)
	path := filepath.Join(dir, "disc.six")
	if err := os.WriteFile(path, seq, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// discRead is what the inside of the disc shows on a screen.
type discRead struct {
	cells, split      int
	meanLuma, spread  float64
	lightest, darkest [3]float64
}

func (d discRead) String() string {
	return fmt.Sprintf("%d cells, %d with two colours, mean luma %.0f, spread %.0f (%v to %v)",
		d.cells, d.split, d.meanLuma, d.spread, d.lightest, d.darkest)
}

// discSubBlocks reads every visible colour of the image cells wholly inside
// the disc, with origin the picture's top-left cell. A glyph cell shows its
// foreground and its background, a blank shows only its background and a
// full block only its foreground. The spread is the largest distance between
// any two of those colours.
func discSubBlocks(s tuitest.Screen, origin image.Point) discRead {
	var d discRead
	var seen [][3]float64
	var lumaSum float64
	for r := range symPicRows {
		for c := range symPicCols {
			inside := true
			for _, p := range []image.Point{{c * cellW, r * cellH}, {(c + 1) * cellW, r * cellH}, {c * cellW, (r + 1) * cellH}, {(c + 1) * cellW, (r + 1) * cellH}} {
				if math.Hypot(float64(p.X-discCX), float64(p.Y-discCY)) >= discR-2 {
					inside = false
				}
			}
			if !inside {
				continue
			}
			cell := s.Cell(origin.X+c, origin.Y+r)
			var shown [][3]float64
			content := strings.TrimSpace(cell.Content)
			if content != "█" {
				if bg, ok := rgbOf(cell.Bg); ok {
					shown = append(shown, bg)
				}
			}
			if content != "" {
				if fg, ok := rgbOf(cell.Fg); ok {
					shown = append(shown, fg)
				}
			}
			if len(shown) == 0 {
				continue
			}
			d.cells++
			if len(shown) == 2 {
				d.split++
			}
			for _, v := range shown {
				seen = append(seen, v)
				lumaSum += rgbLuma(v)
			}
		}
	}
	if len(seen) == 0 {
		return d
	}
	d.meanLuma = lumaSum / float64(len(seen))
	for i := range seen {
		for j := i + 1; j < len(seen); j++ {
			a, b := seen[i], seen[j]
			if dist := math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2])); dist > d.spread {
				d.spread = dist
				if rgbLuma(a) >= rgbLuma(b) {
					d.lightest, d.darkest = a, b
				} else {
					d.lightest, d.darkest = b, a
				}
			}
		}
	}
	return d
}
