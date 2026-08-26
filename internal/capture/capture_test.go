package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestPaletteFallsBackToXTermWithNoTheme is the palette decision written down:
// with no theme set the render happens anyway, in the xterm reference defaults,
// and it says so. Refusing to render would fail the "works with no setup" bar,
// and every alternative to the guess is also a guess.
//
// Negative control: making Palette return the theme's colours for an unknown
// id left warn empty and this failed the notice assertion.
func TestPaletteFallsBackToXTermWithNoTheme(t *testing.T) {
	p, warn := Palette("")
	if warn != XTermNotice {
		t.Errorf("no theme gave warning %q, want the notice", warn)
	}
	if p.FG != shot.XTermFg || p.BG != shot.XTermBg {
		t.Errorf("fallback is %v on %v, want the xterm defaults", p.FG, p.BG)
	}
	if p.ANSI[1] == p.ANSI[2] {
		t.Error("the fallback palette has no distinct basic colours")
	}
	// An id nobody installed is the same case, not an error.
	if _, warn := Palette("no-such-theme-exists"); warn != XTermNotice {
		t.Errorf("an unknown theme gave %q, want the notice", warn)
	}
}

// TestPaletteResolvesAnInstalledTheme checks a themed session really renders in
// its own colours, with no notice.
//
// Negative control: making theme.Colors always report not-found sent every
// session through the fallback and failed both assertions.
func TestPaletteResolvesAnInstalledTheme(t *testing.T) {
	const id = "catppuccin_mocha"
	if !theme.Exists(id) {
		t.Skipf("%s is not installed in this build", id)
	}
	p, warn := Palette(id)
	if warn != "" {
		t.Errorf("a themed session warned %q", warn)
	}
	if p.BG == shot.XTermBg {
		t.Error("the themed background is the xterm default, so the theme was not read")
	}
	if p.ANSI[4] == shot.XTermPalette().ANSI[4] {
		t.Error("the themed blue is the xterm blue, so the palette was not read")
	}
}

// TestFrameFollowsTheConfigSection checks every knob in the section reaches the
// renderer, so none of the sixteen options is inert.
//
// Negative control: dropping the Padding line from Frame's spec left the frame
// at 0 padding and failed.
func TestFrameFollowsTheConfigSection(t *testing.T) {
	cfg := config.ScreenshotConfig{
		Format:      "svg",
		Frame:       "window",
		Background:  "#101020..#202040",
		Controls:    "macos",
		TitleFormat: "{title}",
		FontFamily:  "Fira Code, monospace",
	}
	pad, radius, scale := 31, 7, 3
	no := false
	cfg.Padding, cfg.Radius, cfg.Scale, cfg.Shadow = &pad, &radius, &scale, &no

	s := SettingsFrom(cfg, "", "")
	if s.Format != shot.FormatSVG {
		t.Errorf("format resolved to %q", s.Format)
	}
	p, _ := Palette("")
	f, warnings := Frame(s, p, false)
	if len(warnings) != 0 {
		t.Errorf("a complete config produced warnings: %v", warnings)
	}
	if f.Mode != shot.FrameWindow {
		t.Errorf("frame mode is %v, want the window card", f.Mode)
	}
	if f.Padding != pad || f.Radius != radius || f.Scale != scale {
		t.Errorf("geometry is pad=%d radius=%d scale=%d, want %d %d %d",
			f.Padding, f.Radius, f.Scale, pad, radius, scale)
	}
	if f.Shadow {
		t.Error("shadow = false still drew a shadow")
	}
	if f.Controls != shot.ControlsMacOS {
		t.Errorf("controls resolved to %v, want the macOS lights", f.Controls)
	}
	if f.WashStart != shot.RGB(0x10, 0x10, 0x20) || f.WashEnd != shot.RGB(0x20, 0x20, 0x40) {
		t.Errorf("the explicit gradient came out %v..%v", f.WashStart, f.WashEnd)
	}
	if f.FontFamily != cfg.FontFamily {
		t.Errorf("font family is %q", f.FontFamily)
	}
}

// TestFrameDropsTheTitleBarForARegion checks a region or full-screen capture
// gets a plain card. It is not a window, so a window title bar would be a claim
// about it that is not true.
//
// Negative control: making Frame ignore its plain argument left the title bar
// on a region and failed.
func TestFrameDropsTheTitleBarForARegion(t *testing.T) {
	s := SettingsFrom(config.ScreenshotConfig{Frame: "window"}, "", "")
	p, _ := Palette("")
	if f, _ := Frame(s, p, false); f.Mode != shot.FrameWindow {
		t.Errorf("a window capture got mode %v", f.Mode)
	}
	if f, _ := Frame(s, p, true); f.Mode != shot.FramePlain {
		t.Errorf("a region capture got mode %v, want the plain card", f.Mode)
	}
	// frame = none stays none: an explicit choice is not overridden.
	bare := SettingsFrom(config.ScreenshotConfig{Frame: "none"}, "", "")
	if f, _ := Frame(bare, p, true); f.Mode != shot.FrameNone {
		t.Errorf("frame = none came out as %v", f.Mode)
	}
}

// TestUnreadableFontFileWarnsInsteadOfFailing checks a bad screenshot.font_file
// degrades to the built-in font and says so, rather than losing the capture.
//
// Negative control: making Frame return the read error instead of a warning
// produced no frame at all and failed.
func TestUnreadableFontFileWarnsInsteadOfFailing(t *testing.T) {
	s := SettingsFrom(config.ScreenshotConfig{FontFile: filepath.Join(t.TempDir(), "nope.ttf")}, "", "")
	// The font file is the only choice offered here. Leaving the configured
	// family in place would make this test read whichever fonts the machine
	// running it happens to have installed.
	s.FontFamily, s.HostFontFamily = "", ""
	p, _ := Palette("")
	f, warnings := Frame(s, p, false)
	if f == nil {
		t.Fatal("a missing font file lost the frame")
	}
	if len(f.FontData) != 0 {
		t.Error("a missing font file still produced font data")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "font file") {
		t.Errorf("warnings are %v, want one naming the font file", warnings)
	}
}

// TestFileNameIsSortableAndSafe pins the generated name: a slug a filesystem
// accepts, a timestamp that sorts, and the format's own extension.
//
// Negative control: dropping cleanLabel put "My Build / v2" straight into the
// name, which carries a path separator, and failed.
func TestFileNameIsSortableAndSafe(t *testing.T) {
	at := time.Date(2026, 8, 25, 20, 40, 3, 0, time.UTC)
	got := FileName("My Build / v2", shot.FormatSVG, at)
	if want := "tuios-my-build-v2-2026-08-25-204003.svg"; got != want {
		t.Errorf("name is %q, want %q", got, want)
	}
	if strings.ContainsAny(got, `/\:`) {
		t.Errorf("%q carries a path separator", got)
	}
	// A label that cleans away to nothing leaves a name that still works.
	if got := FileName("///", shot.FormatPNG, at); got != "tuios-2026-08-25-204003.png" {
		t.Errorf("an empty label gave %q", got)
	}
	// Two captures a second apart sort in the order they were taken.
	first := FileName("a", shot.FormatPNG, at)
	second := FileName("a", shot.FormatPNG, at.Add(time.Second))
	if first >= second {
		t.Errorf("%q does not sort before %q", first, second)
	}
	// ANSI keeps its own extension.
	if got := FileName("x", shot.FormatANSI, at); !strings.HasSuffix(got, ".ans") {
		t.Errorf("ansi name is %q, want a .ans file", got)
	}
}

// TestResolvePathHonoursAnExplicitOut checks --out wins over the configured
// directory and comes back absolute, so the caller is never handed a path that
// means something different from where it runs.
//
// Negative control: making ResolvePath ignore out put every capture in the
// configured directory and failed.
func TestResolvePathHonoursAnExplicitOut(t *testing.T) {
	dir := t.TempDir()
	at := time.Now()

	got, err := ResolvePath("", dir, "pane", shot.FormatPNG, at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != dir {
		t.Errorf("a generated name landed in %s, want %s", filepath.Dir(got), dir)
	}

	out := filepath.Join(dir, "explicit.svg")
	got, err = ResolvePath(out, dir, "pane", shot.FormatPNG, at)
	if err != nil {
		t.Fatal(err)
	}
	if got != out {
		t.Errorf("--out gave %q, want %q", got, out)
	}
	// A relative --out resolves against this process's own directory, which is
	// what a person at a shell means by it.
	got, err = ResolvePath("rel.png", dir, "pane", shot.FormatPNG, at)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != "rel.png" {
		t.Errorf("a relative --out gave %q", got)
	}
}

// TestSaveCreatesTheDirectory checks a first capture into a directory that does
// not exist yet works, because the default one never does on a fresh install.
//
// Negative control: dropping the MkdirAll from Save failed with "no such file
// or directory".
func TestSaveCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", "shot.png")
	if err := Save(path, []byte("bytes")); err != nil {
		t.Fatalf("save: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "bytes" {
		t.Errorf("wrote %q", body)
	}
	if err := Save("", []byte("x")); err == nil {
		t.Error("an empty path was accepted instead of refused")
	}
}

// TestResolvePathDoesNotOverwriteAnEarlierCapture pins the collision. The
// generated name carries a timestamp with one-second resolution, so two
// captures inside one second resolve to the same name and the second used to
// destroy the first with nothing said.
//
// Negative control: with freeName removed from ResolvePath, both calls return
// the same path and the first assertion fires.
func TestResolvePathDoesNotOverwriteAnEarlierCapture(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 25, 22, 34, 9, 0, time.UTC)

	first, err := ResolvePath("", dir, "region", shot.FormatPNG, now)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := Save(first, []byte("one")); err != nil {
		t.Fatalf("save first: %v", err)
	}

	second, err := ResolvePath("", dir, "region", shot.FormatPNG, now)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if second == first {
		t.Fatalf("the second capture of the same second resolved to %s, the file the first one wrote", second)
	}
	if err := Save(second, []byte("two")); err != nil {
		t.Fatalf("save second: %v", err)
	}
	if body, err := os.ReadFile(first); err != nil || string(body) != "one" {
		t.Errorf("the first capture is now %q (%v), so the second overwrote it", body, err)
	}
	if filepath.Ext(second) != ".png" {
		t.Errorf("the second capture lost its extension: %s", second)
	}

	// A third lands on its own name too, rather than back on the second's.
	third, err := ResolvePath("", dir, "region", shot.FormatPNG, now)
	if err != nil {
		t.Fatalf("third resolve: %v", err)
	}
	if third == first || third == second {
		t.Errorf("the third capture resolved to %s, which is already taken", third)
	}
}

// TestResolvePathKeepsAnExplicitOutThatAlreadyExists checks the collision guard
// leaves --out alone. That path is an instruction from the caller, and a verb
// told to write one file has to write that file, existing or not.
//
// Negative control: applying freeName to the --out branch returns a -2 name
// here and this fails.
func TestResolvePathKeepsAnExplicitOutThatAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fixed.png")
	if err := Save(out, []byte("existing")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ResolvePath(out, dir, "region", shot.FormatPNG, time.Now())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != out {
		t.Errorf("an explicit --out was moved to %s", got)
	}
}

// TestImageRouteIsHonestAboutItself checks the clipboard probe either offers a
// tool or gives a reason, never both and never neither. A route with no reason
// is how an inert copy key gets drawn.
//
// This control passes both ways on a machine with a display: its value is the
// invariant, which a new platform arm added without a Reason would break.
func TestImageRouteIsHonestAboutItself(t *testing.T) {
	r := ImageRoute()
	if r.Available && r.Tool == "" {
		t.Error("an available route names no tool")
	}
	if !r.Available && r.Reason == "" {
		t.Error("an unavailable route gives no reason, so the panel has nothing to say")
	}
	if r.Reason != "" && !strings.HasSuffix(r.Reason, ".") {
		t.Errorf("the reason %q is not a sentence", r.Reason)
	}
}
