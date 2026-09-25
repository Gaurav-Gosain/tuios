package capture

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

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
