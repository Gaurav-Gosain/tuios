package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// eraseBgMarker is the text the program writes after its clear.
const eraseBgMarker = "BCEMARK"

// A program that clears the screen with a background set owns every cell of
// the pane in that colour, not just the cells it later writes text into.
// neovim clears this way with its colour scheme's background and then draws
// only the text of each line, so a clear that dropped the colour left every
// cell past the end of a line on the host's background (#624).
//
// The program here does what neovim does on startup: set a truecolor
// background, ED 2, write one word, and wait. The cells away from the word
// were painted by the clear alone, so they show whether it kept the colour.
func TestEraseDisplayKeepsTheProgramBackground(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	enterTerminalMode(t, term)
	// The sleep keeps the shell from drawing its prompt over the screen the
	// clear left, the way neovim holds the screen while it runs.
	runInShell(t, term,
		`printf '\033[48;2;36;40;59m\033[2J\033[HBCE%s\033[0m' MARK; sleep 60`,
		eraseBgMarker, 10*time.Second)

	want := tuitest.Color{Kind: tuitest.ColorRGB, R: 36, G: 40, B: 59}
	s := term.Screen()
	row := findRow(s, eraseBgMarker)
	if row < 0 {
		t.Fatalf("the marker is not on screen\n%s", term.Snapshot())
	}
	line := s.Line(row)
	col := len([]rune(line[:strings.Index(line, eraseBgMarker)]))

	word := s.Cell(col, row)
	if word.Bg != want {
		t.Fatalf("the marker's own cell has bg %+v, want %+v; the program's colour does not reach the screen at all\n%s",
			word.Bg, want, term.Snapshot())
	}
	// Past the word on its own row, and on rows the program never wrote.
	probes := [][2]int{
		{col + len(eraseBgMarker) + 4, row},
		{col, row + 2},
		{col + 10, row + 5},
	}
	var bad []string
	for _, p := range probes {
		c := s.Cell(p[0], p[1])
		if c.Bg != want {
			bad = append(bad, fmt.Sprintf("(%d,%d) bg %+v", p[0], p[1], c.Bg))
		}
	}
	saveArtifact(t, term, artifactDir(t), "erase-background")
	if len(bad) > 0 {
		t.Fatalf("cells the clear painted lost the program's background, want rgb(36,40,59):\n%s\n%s",
			strings.Join(bad, "\n"), term.Snapshot())
	}
}
