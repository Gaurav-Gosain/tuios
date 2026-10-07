package tuie2e

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/mosaic"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/Gaurav-Gosain/tuitest"
)

// These tests cover a pane's image on a host terminal that draws no graphics,
// such as the Linux console under kmscon. tuios draws the picture there as
// block glyphs, one glyph with two colours per image cell
// (appearance.image_symbols).
//
// The check reads the picture back off the host's screen. Each image cell's
// glyph is turned back into its shape, by geometry for the blocks of
// U+2580..U+259F and by the Unicode numbering for sextants and octants, and
// the shape's two colours are compared with the source picture averaged over
// the same sub-cells. So the test fails if a glyph is the wrong shape, a
// colour is wrong, or the picture is in the wrong place.
//
// How this could pass wrongly, written down first:
//   - A flat colour per cell would match a smooth picture well. The picture
//     has hard edges and a checker, and the glyphs must beat a flat cell by a
//     margin on the cells that have an edge in them.
//   - The cells read could be a different part of the picture. The origin is
//     found from the line printed above the picture, not searched for.
//   - The pane could be told no sixel, and the program print nothing. The
//     pane's DA1 must list sixel, and image glyphs must be on screen.
//   - Graphics could still be sent to the host. The host stream must hold no
//     sixel, no kitty graphics and no marker text.

const (
	symPicCols = 40
	symPicRows = 12
)

// symbolPicture is the test picture: sky, sun, hills, a red band and a
// black and white checker, at cellW by cellH pixels a cell, quantised to a
// fixed 252-colour cube so it fits in a sixel's registers.
func symbolPicture() *vt.SixelImage {
	w, h := symPicCols*cellW, symPicRows*cellH
	img := &vt.SixelImage{Width: w, Height: h, Pix: make([]uint16, w*h), Palette: make([]color.RGBA, vt.SixelMaxRegisters)}
	const lr, lg, lb = 6, 7, 6
	level := func(v float64, n int) int { return min(n-1, max(0, int(v/255*float64(n-1)+0.5))) }
	for r := range lr {
		for g := range lg {
			for b := range lb {
				img.Palette[(r*lg+g)*lb+b] = color.RGBA{uint8(r * 255 / (lr - 1)), uint8(g * 255 / (lg - 1)), uint8(b * 255 / (lb - 1)), 255}
			}
		}
	}
	for py := range h {
		for px := range w {
			x, y := float64(px)/float64(w), float64(py)/float64(h)
			// Sky.
			r, g, b := 40+140*y, 90+120*y, 200+55*y
			switch {
			case math.Hypot((x-0.75)*float64(w)/float64(h), y-0.3) < 0.16:
				r, g, b = 255, 210, 60
			case y > 0.65+0.08*math.Sin(6*x):
				r, g, b = 40, 150-60*(y-0.65), 60
			}
			if math.Abs(x-0.6*y-0.1) < 0.03 {
				r, g, b = 220, 40, 40
			}
			if x < 0.2 && y > 0.8 {
				if (px/4+py/4)%2 == 0 {
					r, g, b = 0, 0, 0
				} else {
					r, g, b = 255, 255, 255
				}
			}
			idx := (level(r, lr)*lg+level(g, lg))*lb + level(b, lb)
			img.Pix[py*w+px] = uint16(idx + 1)
		}
	}
	return img
}

// writeSymbolPicture writes the picture as a sixel file.
func writeSymbolPicture(t *testing.T, dir string) (*vt.SixelImage, string) {
	t.Helper()
	img := symbolPicture()
	seq := vt.EncodeSixel(img, image.Rect(0, 0, img.Width, img.Height), img.Width, img.Height)
	path := filepath.Join(dir, "picture.six")
	if err := os.WriteFile(path, seq, 0o600); err != nil {
		t.Fatal(err)
	}
	return img, path
}

// glyphGrid is the sub-cell grid of each glyph set.
func glyphGrid(kind string) (gx, gy int) {
	switch kind {
	case "half":
		return 1, 2
	case "quadrant":
		return 2, 2
	case "sextant":
		return 2, 3
	}
	return 2, 4
}

// glyphLit reports whether the glyph r covers the point (fx, fy) of its
// cell, both in [0,1). ok is false for a rune that is not a block glyph.
func glyphLit(r rune, fx, fy float64) (lit, ok bool) {
	left, top := fx < 0.5, fy < 0.5
	switch r {
	case ' ':
		return false, true
	case '█':
		return true, true
	case '▀':
		return top, true
	case '▄':
		return !top, true
	case '▌':
		return left, true
	case '▐':
		return !left, true
	case '▂':
		return fy >= 0.75, true
	case '▆':
		return fy >= 0.25, true
	case '▘':
		return left && top, true
	case '▝':
		return !left && top, true
	case '▖':
		return left && !top, true
	case '▗':
		return !left && !top, true
	case '▚':
		return left == top, true
	case '▞':
		return left != top, true
	case '▛':
		return !(!left && !top), true
	case '▜':
		return !(left && !top), true
	case '▙':
		return !(!left && top), true
	case '▟':
		return !(left && top), true
	}
	gx, gy, mask, ok := mosaic.Shape(r)
	if !ok {
		return false, false
	}
	i := int(fy*float64(gy))*gx + int(fx*float64(gx))
	return mask&(1<<i) != 0, true
}

// symbolScore compares the host's cells with the picture, its top-left cell
// at origin. Only rows from minY down are read: the pane's own rows. It
// returns the mean error per channel over all sub-cells, the same for a flat
// cell of the sub-cells' mean colour on the cells that have an edge, the
// glyphs' error on those cells, and how many image cells were read.
type symbolScore struct {
	err, edgeErr, flatEdgeErr float64
	// meanErr is the error of each cell's mean colour, which is what the
	// eye sees from a distance and what dithering is for.
	meanErr      float64
	cells, edges int
	bad          []string
}

func scoreSymbols(s tuitest.Screen, img *vt.SixelImage, origin image.Point, minY int, kind string) symbolScore {
	gx, gy := glyphGrid(kind)
	cols, rows := s.Size()
	var sc symbolScore
	var sum, edgeSum, flatSum, meanSum float64
	n, edgeN := 0, 0
	for r := range symPicRows {
		for c := range symPicCols {
			x, y := origin.X+c, origin.Y+r
			if y < minY || y >= rows || x >= cols {
				continue
			}
			cell := s.Cell(x, y)
			rs := []rune(cell.Content)
			if len(rs) == 0 {
				rs = []rune{' '}
			}
			fg, fgOK := rgbOf(cell.Fg)
			bg, bgOK := rgbOf(cell.Bg)
			// Expected sub-cell colours, a plain sRGB average of the
			// picture's pixels under each.
			want := make([][3]float64, 0, gx*gy)
			var lits []bool
			for sy := range gy {
				for sx := range gx {
					x0, x1 := c*cellW+sx*cellW/gx, c*cellW+(sx+1)*cellW/gx
					y0, y1 := r*cellH+sy*cellH/gy, r*cellH+(sy+1)*cellH/gy
					var a [3]float64
					k := 0
					for py := y0; py < y1; py++ {
						for px := x0; px < x1; px++ {
							if v, ok := img.At(px, py); ok {
								a[0] += float64(v.R)
								a[1] += float64(v.G)
								a[2] += float64(v.B)
								k++
							}
						}
					}
					for i := range a {
						a[i] /= float64(max(k, 1))
					}
					want = append(want, a)
					lit, ok := glyphLit(rs[0], (float64(sx)+0.5)/float64(gx), (float64(sy)+0.5)/float64(gy))
					if !ok {
						if len(sc.bad) < 8 {
							sc.bad = append(sc.bad, fmt.Sprintf("%d,%d holds %q, not a %s glyph", x, y, cell.Content, kind))
						}
					}
					lits = append(lits, lit)
				}
			}
			sc.cells++
			var cellErr, flatErr float64
			var mean [3]float64
			for _, w := range want {
				for i := range 3 {
					mean[i] += w[i] / float64(len(want))
				}
			}
			spread := 0.0
			var shown [3]float64
			for i, w := range want {
				got, ok := bg, bgOK
				if lits[i] {
					got, ok = fg, fgOK
				}
				if !ok {
					// A default colour where the picture is opaque.
					got = [3]float64{-255, -255, -255}
				}
				for ch := range 3 {
					shown[ch] += got[ch] / float64(len(want))
					cellErr += math.Abs(got[ch] - w[ch])
					flatErr += math.Abs(mean[ch] - w[ch])
					spread = math.Max(spread, math.Abs(mean[ch]-w[ch]))
				}
			}
			k := float64(3 * len(want))
			sum += cellErr / k
			meanSum += (math.Abs(shown[0]-mean[0]) + math.Abs(shown[1]-mean[1]) + math.Abs(shown[2]-mean[2])) / 3
			n++
			if spread > 40 {
				edgeSum += cellErr / k
				flatSum += flatErr / k
				edgeN++
			}
		}
	}
	if n > 0 {
		sc.err = sum / float64(n)
		sc.meanErr = meanSum / float64(n)
	}
	if edgeN > 0 {
		sc.edgeErr, sc.flatEdgeErr = edgeSum/float64(edgeN), flatSum/float64(edgeN)
	}
	sc.edges = edgeN
	return sc
}

func rgbOf(c tuitest.Color) ([3]float64, bool) {
	switch c.Kind {
	case tuitest.ColorRGB:
		return [3]float64{float64(c.R), float64(c.G), float64(c.B)}, true
	case tuitest.ColorIndexed:
		v := shot.XTerm256(int(c.Index))
		return [3]float64{float64(v.R), float64(v.G), float64(v.B)}, true
	}
	return [3]float64{}, false
}

// imageGlyphCells counts the cells in rows [y0, y1) that hold a glyph only
// the image draws with: a sextant, an octant, or a quadrant other than the
// halves the chrome uses.
func imageGlyphCells(s tuitest.Screen, y0, y1 int) int {
	cols, _ := s.Size()
	n := 0
	for y := y0; y < y1; y++ {
		for x := range cols {
			rs := []rune(s.Cell(x, y).Content)
			if len(rs) == 0 {
				continue
			}
			if _, _, _, ok := mosaic.Shape(rs[0]); ok || strings.ContainsRune("▘▝▖▗▚▞▛▜▙▟", rs[0]) {
				n++
			}
		}
	}
	return n
}

// startSymbolPane boots tuios on a host without graphics, with
// appearance.image_symbols set to kind ("" leaves the default) and COLORTERM
// set to colorterm, and opens a pane at a shell prompt.
func startSymbolPane(t *testing.T, host *sixelHost, kind, colorterm string, daemon bool, extra ...string) (*tuitest.Terminal, string) {
	t.Helper()
	env := append([]string{"TUIOS_CELL_SIZE=10x20", "TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_GRAPHICS=0", "COLORTERM=" + colorterm}, extra...)
	if kind != "" {
		home := t.TempDir()
		body := "[appearance]\nimage_symbols = \"" + kind + "\"\n"
		if daemon {
			// A config of its own has no startup.daemon, which the
			// first-run file turns on.
			body += "\n[startup]\ndaemon = true\n"
		}
		writeConfigIn(t, home, body)
		env = append(env, "XDG_CONFIG_HOME="+home)
	}
	term, base := start(t, startOpts{cols: 120, rows: 40, out: host, daemonDefault: daemon, env: env})
	if daemon {
		t.Cleanup(func() { killDaemon(t, base) })
	}
	host.answer(term)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo RE''ADY", "READY", shellTimeout)
	return term, base
}

// TestImageSymbolsOnAHostWithoutGraphics draws a sixel picture in a pane on a
// host with no graphics, once per glyph set and in both modes, and reads it
// back off the host's screen. It then scrolls the picture partly out of the
// pane, and clears it.
//
// The PNGs under artifactDir are the host's frames drawn through
// internal/shot, which draws every glyph set itself.
func TestImageSymbolsOnAHostWithoutGraphics(t *testing.T) {
	// The ceilings are mean error per channel, out of 255. Measured: see
	// the log line each run prints.
	for _, tc := range []struct {
		kind      string
		daemon    bool
		colorterm string
		ceiling   float64
		env       []string
	}{
		{"octant", false, "truecolor", 10, nil},
		{"sextant", false, "truecolor", 10, nil},
		{"quadrant", false, "truecolor", 10, nil},
		{"half", false, "truecolor", 10, nil},
		{"octant", true, "truecolor", 10, nil},
		// A host with 256 colours. The palette's own gaps dominate the
		// error (the cube has no level between 0 and 95), so the ceiling
		// is higher and the shapes are not compared with a flat cell.
		{"octant", false, "", 35, nil},
		// What kmscon 10 sets: TERM=kmscon, COLORTERM=truecolor.
		{"octant", false, "truecolor", 10, []string{"TERM=kmscon"}},
	} {
		name := tc.kind + map[bool]string{false: "-standalone", true: "-daemon"}[tc.daemon]
		if tc.colorterm == "" {
			name += "-256"
		}
		if len(tc.env) > 0 {
			name += "-kmscon"
		}
		t.Run(name, func(t *testing.T) {
			host := newSixelHost(false, false)
			term, _ := startSymbolPane(t, host, tc.kind, tc.colorterm, tc.daemon, tc.env...)
			dir := t.TempDir()
			da1 := paneDA1(t, term, dir)
			if !strings.Contains(";"+da1+";", ";4;") {
				t.Fatalf("pane DA1 = %q: sixel not listed, so programs would not send the picture", da1)
			}

			img, path := writeSymbolPicture(t, dir)
			before := len(host.bytes())
			typeLine(t, term, "clear; printf '%s\\n' 'IMG''TOP'; cat "+path+"; echo; echo SH''OWN")
			if err := term.WaitForText("SHOWN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print: %v\n%s", err, term.Snapshot())
			}
			origin := imageOrigin(t, term, "IMGTOP")
			paneTop := origin.Y - 1
			var sc symbolScore
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				sc = scoreSymbols(s, img, origin, paneTop, tc.kind)
				return len(sc.bad) == 0 && sc.err < tc.ceiling
			}, uiTimeout); err != nil {
				t.Errorf("the picture on the host: error %.1f (ceiling %.1f), %d cells; %s\n%s",
					sc.err, tc.ceiling, sc.cells, strings.Join(sc.bad, "; "), term.Screen().Text())
			}
			t.Logf("%s: mean error %.2f over %d cells, of the cell means %.2f; on %d edge cells %.2f, a flat cell %.2f",
				name, sc.err, sc.cells, sc.meanErr, sc.edges, sc.edgeErr, sc.flatEdgeErr)
			if tc.kind != "half" && tc.colorterm != "" && sc.edgeErr > 0.8*sc.flatEdgeErr {
				t.Errorf("on edge cells the glyphs (%.2f) do not beat a flat cell (%.2f): the shapes are wrong", sc.edgeErr, sc.flatEdgeErr)
			}
			savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "shown")

			out := host.bytes()[before:]
			if sixelDCS.Match(out) || bytes.Contains(out, []byte("\x1b_G")) {
				t.Errorf("a host with no graphics was sent graphics")
			}
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}

			// Scroll the picture up by printing lines under it, until its
			// top rows have left the pane. Where it went is read from the
			// SHOWN line, which sits a fixed distance under it.
			_, rows := term.Screen().Size()
			shownAt := func(s tuitest.Screen) int {
				at := -1
				for y := range rows {
					if l := s.Line(y); strings.Contains(l, "SHOWN") && !strings.Contains(l, "echo") {
						at = y
					}
				}
				return at
			}
			gap := shownAt(term.Screen()) - origin.Y
			if gap != symPicRows+1 {
				t.Fatalf("SHOWN is %d rows under the picture's top, want %d\n%s", gap, symPicRows+1, term.Screen().Text())
			}
			// The pane's last row is the one above its bottom border.
			paneBottom := rows - 1
			for y := origin.Y; y < rows; y++ {
				if c := term.Screen().Cell(origin.X-1, y).Content; c == "╰" || c == "└" {
					paneBottom = y - 1
					break
				}
			}
			// Enough lines to fill the pane under the prompt, and five more.
			typeLine(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo L$i; done; echo SC''ROLLED", paneBottom-(origin.Y+gap+1)+5))
			if err := term.WaitForText("SCROLLED", shellTimeout); err != nil {
				t.Fatalf("the lines did not print: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			s := term.Screen()
			shownY := shownAt(s)
			if shownY < 0 {
				t.Fatalf("SHOWN scrolled out of the pane; print fewer lines\n%s", s.Text())
			}
			moved := image.Pt(origin.X, shownY-gap)
			if moved.Y >= paneTop {
				t.Fatalf("the picture's top did not leave the pane: origin %v, now %v\n%s", origin, moved, s.Text())
			}
			sc = scoreSymbols(s, img, moved, paneTop, tc.kind)
			if len(sc.bad) > 0 || sc.err >= tc.ceiling || sc.cells == 0 {
				t.Errorf("the scrolled picture: error %.1f over %d cells; %s\n%s", sc.err, sc.cells, strings.Join(sc.bad, "; "), s.Text())
			}
			if tc.kind != "half" {
				if n := imageGlyphCells(s, 0, paneTop); n > 0 {
					t.Errorf("%d image cells drawn above the pane", n)
				}
			}
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "scrolled")

			// Positive half of the clear check: show it again, then clear.
			typeLine(t, term, "clear; cat "+path+"; echo; echo AG''AIN")
			if err := term.WaitForText("AGAIN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print again: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return imageGlyphCells(s, 0, rows) > 20 || tc.kind == "half" }, uiTimeout); err != nil {
				t.Fatalf("the picture is not on screen before the clear\n%s", term.Screen().Text())
			}
			typeLine(t, term, "clear; echo CL''EARED")
			if err := term.WaitForText("CLEARED", shellTimeout); err != nil {
				t.Fatalf("clear did not run: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			if n := imageGlyphCells(term.Screen(), 0, rows); n > 0 {
				t.Errorf("%d image cells left after clear\n%s", n, term.Screen().Text())
			}
			if tc.kind == "half" {
				// Half blocks are also chrome glyphs; check the colours.
				if sc := scoreSymbols(term.Screen(), img, origin, paneTop, tc.kind); sc.err < 2*tc.ceiling {
					t.Errorf("the picture is still on screen after clear (error %.1f)", sc.err)
				}
			}
		})
	}
}

// TestImageSymbolsWithChafa runs chafa in a pane on a host without graphics
// and lets it pick its own output from the pane's DA1. It picks sixel, since
// tuios shows the picture, and the picture is on the host as octants. With
// image_symbols off, the pane is told no sixel and chafa prints its own text,
// saved as a frame to compare.
func TestImageSymbolsWithChafa(t *testing.T) {
	if _, err := exec.LookPath("chafa"); err != nil {
		t.Skip("chafa is not installed")
	}
	dir := t.TempDir()
	pic := filepath.Join(dir, "scene.png")
	src := symbolPicture()
	rgba := image.NewRGBA(image.Rect(0, 0, src.Width, src.Height))
	for y := range src.Height {
		for x := range src.Width {
			c, _ := src.At(x, y)
			rgba.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(pic)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, rgba); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	for _, kind := range []string{"octant", "off"} {
		t.Run(kind, func(t *testing.T) {
			host := newSixelHost(false, false)
			term, _ := startSymbolPane(t, host, kind, "truecolor", false)
			typeLine(t, term, "clear; chafa -s 40x12 "+pic+"; echo DO''NE")
			if err := term.WaitForText("DONE", shellTimeout); err != nil {
				t.Fatalf("chafa did not finish: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)
			s := term.Screen()
			_, rows := s.Size()
			n := imageGlyphCells(s, 0, rows)
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "chafa")
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}
			// With symbols off the frame is chafa's own text, kept to set
			// beside tuios's: it may use octants too, so it is not checked.
			if kind == "octant" && n < 100 {
				t.Errorf("chafa did not draw through tuios's octants (%d glyph cells)\n%s", n, s.Text())
			}
			t.Logf("%s: %d octant or sextant cells on the host", kind, n)
		})
	}
}

// vgaPalette is the Linux console's default palette, which the sixteen-colour
// cells are chosen against.
var vgaPalette = [16][3]float64{
	{0, 0, 0}, {170, 0, 0}, {0, 170, 0}, {170, 85, 0}, {0, 0, 170}, {170, 0, 170}, {0, 170, 170}, {170, 170, 170},
	{85, 85, 85}, {255, 85, 85}, {85, 255, 85}, {255, 255, 85}, {85, 85, 255}, {255, 85, 255}, {85, 255, 255}, {255, 255, 255},
}

// scoreANSI16 checks the picture drawn at sixteen colours with half blocks:
// every image cell holds a half-block glyph, every colour is an ANSI index,
// and no background is bright (the Linux console gives bright backgrounds to
// blink). It returns the correlation between the picture's lightness and the
// cells' lightness over 2x2-cell blocks, with the cells read in the VGA
// palette, and the problems found.
func scoreANSI16(s tuitest.Screen, img *vt.SixelImage, origin image.Point) (float64, []string) {
	var bad []string
	luma := func(c [3]float64) float64 { return (0.299*c[0] + 0.587*c[1] + 0.114*c[2]) / 255 }
	src := make([]float64, symPicRows*symPicCols)
	shown := make([]float64, symPicRows*symPicCols)
	for r := range symPicRows {
		for c := range symPicCols {
			var sum float64
			n := 0
			for py := r * cellH; py < (r+1)*cellH; py++ {
				for px := c * cellW; px < (c+1)*cellW; px++ {
					if v, ok := img.At(px, py); ok {
						sum += luma([3]float64{float64(v.R), float64(v.G), float64(v.B)})
					}
					n++
				}
			}
			src[r*symPicCols+c] = sum / float64(n)
			cell := s.Cell(origin.X+c, origin.Y+r)
			idx := func(col tuitest.Color, what string) [3]float64 {
				switch {
				case col.Kind == tuitest.ColorDefault:
					return [3]float64{}
				case col.Kind == tuitest.ColorIndexed && col.Index < 16:
					return vgaPalette[col.Index]
				}
				if len(bad) < 8 {
					bad = append(bad, fmt.Sprintf("%d,%d %s is %+v, not an ANSI index", origin.X+c, origin.Y+r, what, col))
				}
				return [3]float64{}
			}
			fg, bg := idx(cell.Fg, "fg"), idx(cell.Bg, "bg")
			if cell.Bg.Kind == tuitest.ColorIndexed && cell.Bg.Index >= 8 && len(bad) < 8 {
				bad = append(bad, fmt.Sprintf("%d,%d has a bright background %d", origin.X+c, origin.Y+r, cell.Bg.Index))
			}
			var f float64
			switch cell.Content {
			case " ", "":
			case "▀", "▄":
				f = 0.5
			case "█":
				f = 1
			default:
				if len(bad) < 8 {
					bad = append(bad, fmt.Sprintf("%d,%d holds %q, not a half block", origin.X+c, origin.Y+r, cell.Content))
				}
			}
			shown[r*symPicCols+c] = f*luma(fg) + (1-f)*luma(bg)
		}
	}
	block := func(v []float64) []float64 {
		var out []float64
		for r := range symPicRows - 1 {
			for c := range symPicCols - 1 {
				i := r*symPicCols + c
				out = append(out, (v[i]+v[i+1]+v[i+symPicCols]+v[i+symPicCols+1])/4)
			}
		}
		return out
	}
	a, b := block(src), block(shown)
	var ma, mb float64
	for i := range a {
		ma += a[i] / float64(len(a))
		mb += b[i] / float64(len(b))
	}
	var ab, aa, bb float64
	for i := range a {
		ab += (a[i] - ma) * (b[i] - mb)
		aa += (a[i] - ma) * (a[i] - ma)
		bb += (b[i] - mb) * (b[i] - mb)
	}
	if aa == 0 || bb == 0 {
		return 0, bad
	}
	return ab / math.Sqrt(aa*bb), bad
}

// planeOneGlyphs counts the image cells that hold a sextant or octant, the
// glyphs only those two sets use.
func planeOneGlyphs(s tuitest.Screen, origin image.Point) (sextants, octants int) {
	for r := range symPicRows {
		for c := range symPicCols {
			rs := []rune(s.Cell(origin.X+c, origin.Y+r).Content)
			if len(rs) == 0 {
				continue
			}
			switch {
			case rs[0] >= 0x1FB00 && rs[0] <= 0x1FB3B:
				sextants++
			case rs[0] >= 0x1CD00 && rs[0] <= 0x1CEAF:
				octants++
			}
		}
	}
	return sextants, octants
}

// TestImageSymbolsAutoPicksByTerminal leaves appearance.image_symbols at auto
// and checks the glyph set tuios picks from TERM: octants on kmscon, half
// blocks on the Linux console (in sixteen colours unless COLORTERM says
// more), and quadrants elsewhere.
// A host that answers for sixel gets sixel and no glyphs, with TERM=kmscon
// too.
func TestImageSymbolsAutoPicksByTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, term, colorterm string
		sixel                 bool
	}{
		{"kmscon", "kmscon", "truecolor", false},
		{"linux", "linux", "", false},
		// The kernel console with COLORTERM set: still half blocks, the
		// font has nothing finer, but in 24-bit colour.
		{"linux-truecolor", "linux", "truecolor", false},
		{"other", "xterm-256color", "truecolor", false},
		{"kmscon-with-sixel", "kmscon", "truecolor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newSixelHost(tc.sixel, false)
			env := []string{"TERM=" + tc.term}
			if tc.sixel {
				// startSymbolPane pins the host to no graphics; this
				// row says sixel instead.
				env = append(env, "TUIOS_SIXEL_GRAPHICS=1")
			}
			term, _ := startSymbolPane(t, host, "", tc.colorterm, false, env...)
			dir := t.TempDir()
			da1 := paneDA1(t, term, dir)
			if !strings.Contains(";"+da1+";", ";4;") {
				t.Fatalf("pane DA1 = %q: sixel not listed", da1)
			}
			img, path := writeSymbolPicture(t, dir)
			before := len(host.bytes())
			typeLine(t, term, "clear; printf '%s\\n' 'IMG''TOP'; cat "+path+"; echo; echo SH''OWN")
			if err := term.WaitForText("SHOWN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)
			origin := imageOrigin(t, term, "IMGTOP")
			s := term.Screen()
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "auto-"+tc.name)
			sextants, octants := planeOneGlyphs(s, origin)
			out := host.bytes()[before:]
			switch tc.name {
			case "kmscon":
				sc := scoreSymbols(s, img, origin, origin.Y-1, "octant")
				t.Logf("kmscon: error %.2f, %d octant cells, %d sextant cells", sc.err, octants, sextants)
				if octants == 0 || sextants > 0 || len(sc.bad) > 0 || sc.err >= 10 {
					t.Errorf("TERM=kmscon: want octants; error %.1f, %d octant and %d sextant cells; %s", sc.err, octants, sextants, strings.Join(sc.bad, "; "))
				}
			case "linux-truecolor":
				sc := scoreSymbols(s, img, origin, origin.Y-1, "half")
				t.Logf("linux-truecolor: error %.2f", sc.err)
				if octants > 0 || sextants > 0 || imageGlyphCells(s, 0, 40) > 0 || len(sc.bad) > 0 || sc.err >= 10 {
					t.Errorf("TERM=linux with COLORTERM: want half blocks; error %.1f; %s", sc.err, strings.Join(sc.bad, "; "))
				}
			case "other":
				sc := scoreSymbols(s, img, origin, origin.Y-1, "quadrant")
				t.Logf("other: error %.2f", sc.err)
				if octants > 0 || sextants > 0 || len(sc.bad) > 0 || sc.err >= 10 {
					t.Errorf("TERM=%s: want quadrants; error %.1f, %d octant and %d sextant cells; %s", tc.term, sc.err, octants, sextants, strings.Join(sc.bad, "; "))
				}
			case "linux":
				corr, bad := scoreANSI16(s, img, origin)
				t.Logf("linux: lightness correlation %.2f", corr)
				if len(bad) > 0 || corr < 0.8 {
					t.Errorf("TERM=linux: want half blocks in sixteen colours; correlation %.2f; %s", corr, strings.Join(bad, "; "))
				}
				if box := strings.Contains(s.Text(), "image"); box {
					t.Errorf("TERM=linux: the box is shown, not the picture")
				}
			case "kmscon-with-sixel":
				if !sixelDCS.Match(out) {
					t.Errorf("a sixel host was sent no sixel")
				}
				if n := imageGlyphCells(s, 0, 40); n > 0 || octants > 0 {
					t.Errorf("a sixel host got %d glyph cells", n+octants)
				}
			}
			if !tc.sixel && (sixelDCS.Match(out) || bytes.Contains(out, []byte("\x1b_G"))) {
				t.Errorf("a host with no graphics was sent graphics")
			}
		})
	}
}
