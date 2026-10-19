package loom

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/sfnt"
	"tuios.dev/shot"
)

// TestFrames renders every golden case to PNG through tuios.dev/shot for a
// person to review, at the four depths. It runs only when LOOM_FRAMES names
// a folder for the files. LOOM_FONT_DIR and LOOM_FONT (the regular file;
// the bold file has Bold for Regular) pick the font, JetBrainsMono Nerd
// Font Mono by default. A rune the font's own cmap lacks fails the test, so
// no frame falls back to shot's built-in face.
func TestFrames(t *testing.T) {
	out := os.Getenv("LOOM_FRAMES")
	if out == "" {
		t.Skip("set LOOM_FRAMES to a folder to render the frames")
	}
	dir := os.Getenv("LOOM_FONT_DIR")
	if dir == "" {
		dir = "/usr/share/fonts/TTF"
	}
	name := os.Getenv("LOOM_FONT")
	if name == "" {
		name = "JetBrainsMonoNerdFontMono-Regular.ttf"
	}
	regular, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Skipf("no font: %v", err)
	}
	boldName := name[:len(name)-len("Regular.ttf")] + "Bold.ttf"
	if filepath.Ext(name) == ".otf" {
		boldName = name[:len(name)-len("Regular.otf")] + "Bold.otf"
	}
	bold, err := os.ReadFile(filepath.Join(dir, boldName))
	if err != nil {
		t.Fatal(err)
	}
	face, err := sfnt.Parse(regular)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := append(loadCases(t, "testdata/examples-golden"), loadCases(t, "testdata/components")...)
	for _, c := range cases {
		if c.name == "text-wide" {
			// CJK and emoji are not in the font, and a frame never falls
			// back to shot's built-in face. The text golden covers it.
			continue
		}
		for _, d := range Depths() {
			buf := c.render(t, d)
			grid := toGrid(buf)
			if miss := cmapMissing(face, grid); len(miss) > 0 {
				t.Errorf("%s at %s: the font has no glyph for %q", c.name, d, string(miss))
				continue
			}
			f := shot.BuildFrame(shot.FrameSpec{
				Frame: "window", Title: c.name + " · " + d.String(), Padding: 24, Radius: 10, Shadow: true, Scale: 1,
				Font: shot.Font{Data: regular}, BoldFont: shot.Font{Data: bold}, Controls: "macos",
			}, shot.FrameInputs{Palette: shotPalette, Accents: []shot.Color{rgb(0x6B50FF), rgb(0x00A4FF)}})
			png, err := shot.Render(shot.FormatPNG, grid, f)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(out, fmt.Sprintf("%s-%s.png", c.name, d))
			if err := os.WriteFile(path, png, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// shotPalette is the terminal the frames picture: the tuios canvas and
// text, and at 16 colours a common dark theme (Tomorrow Night), which is
// what the mock frames used.
var shotPalette = &shot.Palette{
	FG: rgb(0xFFFAF1), BG: rgb(0x201F26),
	ANSI: [16]shot.Color{
		rgb(0x1D1F21), rgb(0xCC6666), rgb(0xB5BD68), rgb(0xF0C674), rgb(0x81A2BE), rgb(0xB294BB), rgb(0x8ABEB7), rgb(0xC5C8C6),
		rgb(0x666666), rgb(0xD54E53), rgb(0xB9CA4A), rgb(0xE7C547), rgb(0x7AA6DA), rgb(0xC397D8), rgb(0x70C0B1), rgb(0xEAEAEA),
	},
}

func toGrid(buf *Buffer) *shot.Grid {
	g := shot.NewGrid(buf.Width(), buf.Height(), shotPalette.FG, shotPalette.BG)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if c := buf.b.CellAt(x, y); c != nil {
				g.Cells[y][x] = shot.MakeCell(c.Content, c.Width, c.Style, c.Link, shotPalette)
			}
		}
	}
	return g
}

func cmapMissing(f *sfnt.Font, g *shot.Grid) []rune {
	seen := map[rune]bool{}
	var miss []rune
	var b sfnt.Buffer
	for _, row := range g.Cells {
		for _, c := range row {
			for _, r := range c.Cluster {
				if r <= ' ' || seen[r] {
					continue
				}
				seen[r] = true
				if i, err := f.GlyphIndex(&b, r); err != nil || i == 0 {
					miss = append(miss, r)
				}
			}
		}
	}
	return miss
}
