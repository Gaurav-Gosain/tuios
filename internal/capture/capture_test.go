package capture

import (
	"strings"
	"testing"
	"time"

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
