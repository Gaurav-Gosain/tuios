package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The colour a file icon burns, checked where it lands rather than where it is
// looked up. The claims are the three the option makes: the colour is on the
// glyph and nowhere else, it is off cleanly rather than by coincidence, and it
// is legible on the ground the row actually draws on.

// colorRunRE splits a rendered row into the truecolor runs it is painted in.
var colorRunRE = regexp.MustCompile(`\x1b\[(?:1;)?38;2;(\d+);(\d+);(\d+)m([^\x1b]*)`)

// rowInk returns the colour the run holding want is painted in, and whether the
// row painted it at all. It reads the rendered row, so a colour the renderer
// never emitted cannot pass.
func rowInk(line, want string) (color.RGBA, bool) {
	for _, m := range colorRunRE.FindAllStringSubmatch(line, -1) {
		if !strings.Contains(m[4], want) {
			continue
		}
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xFF}, true
	}
	return color.RGBA{}, false
}

// colorTestDir is a listing with one name per colour family the table knows,
// plus two folders, so a rail drawn on it exercises the whole column.
func colorTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{
		"main.go", "lib.rs", "app.py", "index.ts", "style.css", "README.md",
		"config.toml", "notes.txt", "photo.png", "run.sh", "gem.rb", "data.json",
		"archive.zst", "Makefile", ".gitignore", "LICENSE", "unknown.zzz",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

// colorRailLines renders the rail over colorTestDir with its styling left on.
func colorRailLines(t *testing.T, dir string) []string {
	t.Helper()
	m := sidebarTestOS(t, 120, 44, "left")
	openFilesOn(t, m, dir)
	lines, w := m.sidebarPanelLines()
	if w <= 0 || lines == nil {
		t.Fatal("the rail reserved no columns")
	}
	return lines
}

// lineHolding returns the rendered row whose text holds want.
func lineHolding(t *testing.T, lines []string, want string) string {
	t.Helper()
	for _, ln := range lines {
		if strings.Contains(ln, want) {
			return ln
		}
	}
	t.Fatalf("no rendered row holds %q", want)
	return ""
}

// TestFileIconColorsPaintTheGlyphAndNothingElse. The colour is one cell of the
// row. The name beside it keeps the row's own ink, which is what says "this is
// a directory" and "this is a file" on a terminal with no icons at all, and two
// file types that the table gives different colours have to come out different.
//
// Negative controls, both confirmed red: return ink unchanged for the glyph in
// sidebarFileRow, and every type paints the row ink; drop the Hex from the
// fileIcon the listing stores, and the same.
func TestFileIconColorsPaintTheGlyphAndNothingElse(t *testing.T) {
	lines := colorRailLines(t, colorTestDir(t))

	// The two file rows below are drawn in the same row ink, so a difference in
	// the glyph column can only have come from the icon table.
	goLine := lineHolding(t, lines, "main.go")
	rsLine := lineHolding(t, lines, "lib.rs")

	goName, ok := rowInk(goLine, "main.go")
	if !ok {
		t.Fatalf("the go row painted no name: %q", goLine)
	}
	rsName, ok := rowInk(rsLine, "lib.rs")
	if !ok {
		t.Fatalf("the rust row painted no name: %q", rsLine)
	}
	if goName != rsName {
		t.Errorf("two file rows wear different name inks (%v, %v); the colour has leaked off the glyph", goName, rsName)
	}

	goGlyph, ok := rowInk(goLine, fileIconFor("main.go", false).Glyph)
	if !ok {
		t.Fatalf("the go row painted no glyph: %q", goLine)
	}
	rsGlyph, ok := rowInk(rsLine, fileIconFor("lib.rs", false).Glyph)
	if !ok {
		t.Fatalf("the rust row painted no glyph: %q", rsLine)
	}
	if goGlyph == rsGlyph {
		t.Errorf("go and rust drew the same glyph ink %v; the column is not coloured by type", goGlyph)
	}
	if goGlyph == goName {
		t.Errorf("the go glyph wears the row's own ink %v with colours on", goGlyph)
	}
}

// TestFileIconColorsOffIsACleanPath. Off, the glyph wears the row's own ink,
// which is a colour it is given rather than one it happens to match: the check
// is that the glyph and the name beside it are painted the same, on a row whose
// icon the table has a strong opinion about.
//
// Negative control, confirmed red: drop the config.SidebarFileIconColors term
// from fileIconColorsOn, and the off case paints the go glyph blue.
func TestFileIconColorsOffIsACleanPath(t *testing.T) {
	prev := config.Global.SidebarFileIconColors
	config.Global.SidebarFileIconColors = false
	t.Cleanup(func() { config.Global.SidebarFileIconColors = prev })

	lines := colorRailLines(t, colorTestDir(t))
	for _, name := range []string{"main.go", "lib.rs", "app.py", "internal"} {
		line := lineHolding(t, lines, name)
		glyph, ok := rowInk(line, fileIconFor(name, name == "internal").Glyph)
		if !ok {
			t.Fatalf("the %s row painted no glyph: %q", name, line)
		}
		text, ok := rowInk(line, name)
		if !ok {
			t.Fatalf("the %s row painted no name: %q", name, line)
		}
		if glyph != text {
			t.Errorf("with colours off %s drew its glyph in %v and its name in %v", name, glyph, text)
		}
	}
}

// TestFileIconColorsNeedTheIconsUnderThem. The colour has no shape to carry it
// without the nerd font layer, so both switches that take the icons away take
// the colour with them, and the row falls back to the glyph set's own marks in
// the row's own ink.
//
// Negative control, confirmed red: drop the fileIconsOn term from
// fileIconColorsOn, and the ASCII case paints a coloured ">" that no font
// problem explains.
func TestFileIconColorsNeedTheIconsUnderThem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		glyph string
	}{
		{"icons off", func(t *testing.T) {
			prev := config.Global.SidebarFileIcons
			config.Global.SidebarFileIcons = false
			t.Cleanup(func() { config.Global.SidebarFileIcons = prev })
		}, "·"},
		{"ascii", func(t *testing.T) {
			prev := config.Global.UseASCIIOnly
			config.Global.UseASCIIOnly = true
			overlay.SetASCII(true)
			t.Cleanup(func() {
				config.Global.UseASCIIOnly = prev
				overlay.SetASCII(false)
			})
		}, "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			if fileIconColorsOn(&config.Global) {
				t.Error("the colour layer says it is on with the icons off")
			}
			line := lineHolding(t, colorRailLines(t, colorTestDir(t)), "main.go")
			if !strings.Contains(line, tc.glyph+"\x1b") && !strings.Contains(line, tc.glyph+" ") {
				t.Errorf("the go row does not wear the fallback mark %q: %q", tc.glyph, line)
			}
			glyph, ok := rowInk(line, tc.glyph)
			if !ok {
				t.Fatalf("the go row painted no glyph: %q", line)
			}
			text, ok := rowInk(line, "main.go")
			if !ok {
				t.Fatalf("the go row painted no name: %q", line)
			}
			if glyph != text {
				t.Errorf("the fallback mark drew %v and its name drew %v; a colour is still on it", glyph, text)
			}
		})
	}
}

// TestFileIconColorsClearTheMarkFloor is the claim the whole adaptation exists
// for. The hexes are absolute and were chosen against an editor's dark ground,
// so on some theme or other every one of them is a colour drawn on top of
// itself. Every colour the section can paint has to clear MarkFloor against the
// ground the row draws on, on a dark theme and on a light one.
//
// The raw hex is measured beside the adapted one so the test also says what it
// bought: a run that measured nothing but the adapted value would pass against
// code that adapted nothing, if the palette happened to suit the ground.
//
// Negative control, confirmed red: return c unchanged from ReadableAt, and both
// grounds fail naming the colours that fall through.
func TestFileIconColorsClearTheMarkFloor(t *testing.T) {
	names := []string{
		"main.go", "lib.rs", "app.py", "index.ts", "style.css", "README.md",
		"config.toml", "notes.txt", "photo.png", "run.sh", "gem.rb", "data.json",
		"archive.zst", "Makefile", ".gitignore", "LICENSE", "unknown.zzz", "src",
		"a.log", "a.svg", "a.pdf", "a.mp4", "a.so", "a.env", "a.lock", "a.scss",
		"a.java", "a.php", "a.zig", "a.lua", "a.vim", "a.sql", "a.html",
	}
	for _, ground := range []struct{ id, hex string }{
		{"catppuccin_mocha", "#1E1E2E"},
		{"catppuccin_latte", "#EFF1F5"},
	} {
		t.Run(ground.id, func(t *testing.T) {
			if err := theme.Initialize(ground.id); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = theme.Initialize("") })
			bg := theme.RailGround()
			if got := iconHexOf(bg); got != ground.hex {
				t.Fatalf("the %s ground is %s, not the %s this case is written for", ground.id, got, ground.hex)
			}

			raw, worstRaw, worstName := 0, 99.0, ""
			for _, name := range names {
				icon := fileIconFor(name, name == "src")
				mark := fileRowMark(icon, name == "src", false, &config.Global)
				if mark.Hex == "" {
					t.Fatalf("%q drew no colour at all", name)
				}
				if r := overlay.ContrastRatio(lipgloss.Color(mark.Hex), bg); r < overlay.MarkFloor {
					raw++
					if r < worstRaw {
						worstRaw, worstName = r, name
					}
				}
				got := overlay.ContrastRatio(theme.FileIconInk(mark.Hex), bg)
				if got < overlay.MarkFloor {
					t.Errorf("%s draws %s at %.2f:1 on %s, under the %.1f:1 floor",
						name, mark.Hex, got, ground.hex, overlay.MarkFloor)
				}
			}
			if raw == 0 {
				t.Errorf("no raw hex was under the floor on %s, so this ground proves nothing about the adaptation", ground.id)
			} else {
				t.Logf("%s: %d of %d raw hexes were under the floor, worst %s at %.2f:1",
					ground.id, raw, len(names), worstName, worstRaw)
			}
		})
	}
}

// TestFileIconColorIsMeasuredOnTheRowsOwnGround. The row under the pointer
// paints a band of its own, and on a light theme that band is a dark one. A
// colour lifted for the theme's pale ground would be lifted the wrong way for
// the band, so the ground the ink is measured against is the one the row
// actually draws.
//
// Negative control, confirmed red: return theme.RailGround() unconditionally
// from sidebarGroundOr, and the two grounds hand back the same ink.
func TestFileIconColorIsMeasuredOnTheRowsOwnGround(t *testing.T) {
	if err := theme.Initialize("catppuccin_latte"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = theme.Initialize("") })

	pal := theme.UI()
	const hex = "#DDDDDD" // the markdown grey: pale, so the two grounds disagree hard
	resting := theme.FileIconInkOn(hex, sidebarGroundOr(nil))
	banded := theme.FileIconInkOn(hex, sidebarGroundOr(pal.Surface))
	if iconHexOf(resting) == iconHexOf(banded) {
		t.Fatalf("the pale ground and the band both drew %s", iconHexOf(resting))
	}
	if got := overlay.ContrastRatio(resting, theme.RailGround()); got < overlay.MarkFloor {
		t.Errorf("the resting ink measures %.2f:1 on the theme's ground", got)
	}
	if got := overlay.ContrastRatio(banded, pal.Surface); got < overlay.MarkFloor {
		t.Errorf("the band's ink measures %.2f:1 on the band", got)
	}
}

// iconHexOf spells a colour the way the icon table does.
func iconHexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}
