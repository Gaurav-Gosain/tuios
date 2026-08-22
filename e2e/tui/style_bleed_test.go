package tuie2e

// Style bleed guard, asserted on what actually reaches the host. A colorized
// ls emits foreground-only SGR (38;5;N and 38;2;R;G;B); no listing cell may
// arrive with a background. The ghostty backend once failed exactly this:
// its style-conversion cache outlived the library's style-ID recycling, and
// after a screen clear the recycled IDs served another style's background,
// painting filename-shaped blocks. The differential harness compared
// internal grids after a single write and missed it; this test watches the
// styled cells of the rendered frame across the write-read-clear-write shape
// that triggers recycling.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func TestLsStyleStreamHasNoBackgroundBleed(t *testing.T) {

	term, _ := start(t, startOpts{cols: 120, rows: 36})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// A powerline-ish prompt: heavy background segments, the styles whose
	// IDs get recycled.
	prompt := `printf '\033[48;2;255;105;180m\033[38;5;231m SEG-A \033[48;5;61m SEG-B \033[0m\n'`
	if err := term.SendKeys(prompt, tuitest.Enter); err != nil {
		t.Fatalf("prompt paint: %v", err)
	}
	if err := term.WaitForText("SEG-B", shellTimeout); err != nil {
		t.Fatalf("prompt never rendered: %v\n%s", err, term.Snapshot())
	}
	// A rendered frame between the generations is the trigger: it caches
	// conversions for style IDs the clear below frees.
	time.Sleep(400 * time.Millisecond)

	files := []string{"file-alpha.md", "notes-beta.txt", "image-gamma.png"}
	listing := fmt.Sprintf(
		`clear; printf '\033[38;5;105m%s\033[0m\n\033[38;5;71m%s\033[0m\n\033[38;2;250;189;47m%s\033[0m\nLSDONE\n'`,
		files[0], files[1], files[2])
	if err := term.SendKeys(listing, tuitest.Enter); err != nil {
		t.Fatalf("listing: %v", err)
	}
	if err := term.WaitForText("LSDONE", shellTimeout); err != nil {
		t.Fatalf("listing never rendered: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)

	screen := term.Screen()
	cols, rows := screen.Size()
	for _, name := range files {
		col, row, ok := findCellSpan(screen, cols, rows, name)
		if !ok {
			t.Errorf("%q not on screen:\n%s", name, term.Snapshot())
			continue
		}
		for i := range len(name) {
			cell := screen.Cell(col+i, row)
			if cell.Bg.Kind != tuitest.ColorDefault {
				t.Errorf("%q cell %d (%q) reached the host with background %+v; the stream set only foregrounds",
					name, i, cell.Content, cell.Bg)
			}
			if cell.Fg.Kind == tuitest.ColorDefault {
				t.Errorf("%q cell %d (%q) lost its foreground color", name, i, cell.Content)
			}
		}
	}
}

// findCellSpan locates a string as a run of screen cells, walking cells
// rather than Line: string indexes do not map to columns once zero-width
// cells are in play. Rows containing "printf" are the command echo, which
// also holds the filenames, and are skipped.
func findCellSpan(screen tuitest.Screen, cols, rows int, name string) (int, int, bool) {
	runes := []rune(name)
	for row := 0; row < rows; row++ {
		if strings.Contains(screen.Line(row), "printf") {
			continue
		}
		for col := 0; col+len(runes) <= cols; col++ {
			match := true
			for i, r := range runes {
				if screen.Cell(col+i, row).Content != string(r) {
					match = false
					break
				}
			}
			if match {
				return col, row, true
			}
		}
	}
	return 0, 0, false
}
