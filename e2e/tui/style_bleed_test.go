package tuie2e

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

	prompt := `printf '\033[48;2;255;105;180m\033[38;5;231m SEG-A \033[48;5;61m SEG-B \033[0m\n'`
	if err := term.SendKeys(prompt, tuitest.Enter); err != nil {
		t.Fatalf("prompt paint: %v", err)
	}
	if err := term.WaitForText("SEG-B", shellTimeout); err != nil {
		t.Fatalf("prompt never rendered: %v\n%s", err, term.Snapshot())
	}
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

func findCellSpan(screen tuitest.Screen, cols, rows int, name string) (int, int, bool) {
	runes := []rune(name)
	for row := range rows {
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
