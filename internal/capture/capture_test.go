package capture

import (
	"os"
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

// TestSaveNeverShowsAPartFile pins that a reader who finds the file finds all
// of it, and that nothing but the file is left in the directory.
//
// Negative control: with Save as a plain os.WriteFile, the reader found the
// file empty or short in most runs, which is the e2e flake where the preview
// test read a capture that existed and held nothing.
func TestSaveNeverShowsAPartFile(t *testing.T) {
	data := []byte(strings.Repeat("0123456789abcdef", 1<<16)) // 1 MiB
	for round := range 20 {
		dir := filepath.Join(t.TempDir(), "shots")
		path := filepath.Join(dir, "capture.txt")
		short := make(chan int, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				got, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				if len(got) != len(data) {
					short <- len(got)
				}
				return
			}
		}()
		if err := Save(path, data); err != nil {
			t.Fatalf("save: %v", err)
		}
		<-done
		select {
		case n := <-short:
			t.Fatalf("round %d: a reader found %d of %d bytes", round, n, len(data))
		default:
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "capture.txt" {
			t.Fatalf("round %d: the directory holds %v, want only capture.txt", round, entries)
		}
	}
}

// TestAClaimedNameIsNotAnEmptyFile pins that choosing a capture's name puts
// nothing under that name. The capture appears there whole, from Save, and a
// second capture in the same second still gets a name of its own.
//
// Negative control: with the claim taken on the name itself, ResolvePath left
// an empty file under it, which the e2e preview test read as an empty capture.
func TestAClaimedNameIsNotAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	first, err := ResolvePath("", dir, "pane", shot.FormatText, at)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := os.Lstat(first); err == nil {
		t.Fatalf("%s exists before the capture is saved", first)
	}
	second, err := ResolvePath("", dir, "pane", shot.FormatText, at)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if second == first {
		t.Fatalf("two captures in one second both got %s", first)
	}
	for _, p := range []string{first, second} {
		if err := Save(p, []byte("x\n")); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	third, err := ResolvePath("", dir, "pane", shot.FormatText, at)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if third == first || third == second {
		t.Fatalf("a third capture got %s, which a saved capture holds", third)
	}
	if err := Save(third, []byte("x\n")); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 3 {
		t.Fatalf("the directory holds %v, want the three captures and nothing else", names)
	}
}
