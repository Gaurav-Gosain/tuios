//go:build unix

package app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/charmbracelet/x/ansi"
)

// solidPNG writes a size by size image of one colour, half of it transparent,
// so a scale can be checked for keeping both.
func solidPNG(t *testing.T, path string, size int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			if x < size/2 {
				img.Set(x, y, c)
				continue
			}
			img.Set(x, y, color.RGBA{})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestFitSquareKeepsAlpha is the reason the scale does not composite against a
// colour: the row under an icon changes colour when it is selected, so the
// transparency has to survive to the terminal for it to blend correctly.
func TestFitSquareKeepsAlpha(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := range 48 {
		for x := range 48 {
			if x < 24 {
				src.Set(x, y, color.RGBA{255, 0, 0, 255})
				continue
			}
			src.Set(x, y, color.RGBA{})
		}
	}

	got := fitSquare(src, 20, 20)
	if got == nil {
		t.Fatal("fitSquare returned nothing for a valid image")
	}
	if b := got.Bounds(); b.Dx() != 20 || b.Dy() != 20 {
		t.Fatalf("bounds = %v, want a 20x20 box", b)
	}
	if _, _, _, a := got.At(4, 10).RGBA(); a == 0 {
		t.Error("the opaque half came out transparent")
	}
	if _, _, _, a := got.At(16, 10).RGBA(); a != 0 {
		t.Errorf("the transparent half came out with alpha %d, so it was composited away", a)
	}
}

// TestFitSquareCentresInAWiderBox keeps a square icon square. The box is two
// cells wide and one tall, which is not square, so the icon is fitted to the
// shorter side and centred rather than stretched.
func TestFitSquareCentresInAWiderBox(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.Set(x, y, color.RGBA{0, 255, 0, 255})
		}
	}
	got := fitSquare(src, 40, 20)
	if got == nil {
		t.Fatal("fitSquare returned nothing")
	}
	// The icon is 20 wide inside a 40 wide box, so the first and last columns
	// are outside it and untouched.
	if _, _, _, a := got.At(2, 10).RGBA(); a != 0 {
		t.Error("the padding beside the icon was painted")
	}
	if _, _, _, a := got.At(20, 10).RGBA(); a == 0 {
		t.Error("the centred icon did not land in the middle of the box")
	}
}

// TestLoadIconDecodesThroughTheFinder is the whole pipeline below the render:
// a themed name to a file to pixels at the size a row can hold.
func TestLoadIconDecodesThroughTheFinder(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "icons", "hicolor", "32x32", "apps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	solidPNG(t, filepath.Join(dir, "probe.png"), 32, color.RGBA{0, 0, 255, 255})
	if err := os.WriteFile(filepath.Join(base, "icons", "hicolor", "index.theme"),
		[]byte("[Icon Theme]\nDirectories=32x32/apps\n\n[32x32/apps]\nSize=32\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", base)
	t.Setenv("XDG_DATA_DIRS", base)

	f := applist.NewIconFinder("hicolor")
	f.RasterOnly = true
	if got := loadIcon(f, "probe", 20, 20); got == nil {
		t.Fatal("a themed PNG did not reach the launcher as pixels")
	}
	if got := loadIcon(f, "nothing-of-the-sort", 20, 20); got != nil {
		t.Error("a name with no icon produced an image")
	}
}

// TestNoGraphicsMeansNoIconColumn is the degraded state that matters most.
// Over ssh on a plain terminal, in tuios-web without an image addon, or on a
// host that never answered the probe, the column is not drawn at all: a strip
// of blank cells down the left of every row would be worse than the list this
// replaced.
func TestNoGraphicsMeansNoIconColumn(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "ripgrep")

	// runTestOS has no PostRenderWriter, which is one of the two things
	// launcherGraphicsReady insists on, so this is the no-graphics host.
	if m.launcherGraphicsReady() {
		t.Fatal("a host with nowhere to write graphics reported itself ready")
	}
	if got := m.launcherIconWidth(); got != 0 {
		t.Fatalf("icon column = %d cells, want none", got)
	}
	if got := m.LauncherVisibleIcons(); got != nil {
		t.Errorf("icons were asked for anyway: %v", got)
	}
	if got := m.LauncherIconWork(); got != nil {
		t.Error("a decode was queued on a host that cannot draw one")
	}

	out, _, _ := m.renderLauncher()
	row := ansi.Strip(launcherRowContaining(t, out, "ripgrep"))
	// The name follows the two-cell marker with nothing between them, so no
	// cells were held back for a picture that is never coming.
	if !strings.Contains(row, "› ripgrep") {
		t.Errorf("the row reserved space for an icon that cannot be drawn: %q", row)
	}
}

// TestApplyLauncherIconsRemembersAMiss keeps a name that resolved to nothing
// from being looked up again. A miss is the common case, and re-walking every
// theme directory for it is the expensive one.
func TestApplyLauncherIconsRemembersAMiss(t *testing.T) {
	m := runTestOS(t)
	m.applyLauncherIcons(launcherIconsMsg{pixels: map[string]*image.RGBA{"absent": nil}})

	st := m.launcherIconState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.pixels["absent"]; !ok {
		t.Fatal("a miss was not recorded, so it will be looked up again")
	}
}
