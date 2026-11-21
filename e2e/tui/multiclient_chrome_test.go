package tuie2e

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func isPaneCorner(r rune) bool {
	switch r {
	case '╭', '┌', '┏', '╔':
		return true
	}
	return false
}

func paneStarts(s tuitest.Screen) []string {
	cols, rows := s.Size()
	var starts []string
	for r := range rows {
		line := []rune(s.Line(r))
		for c := range min(len(line), cols) {
			if isPaneCorner(line[c]) {
				starts = append(starts, fmt.Sprintf("%d,%d", r, c))
			}
		}
	}
	slices.Sort(starts)
	return starts
}

func paneBox(s tuitest.Screen) string {
	return fmt.Sprintf("panes %v right %d", paneStarts(s), paneSpanRight(s))
}

func waitPaneBox(t *testing.T, term *tuitest.Terminal, want, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneBox(s) == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the panes are at %s, want %s\n%s", what, paneBox(term.Screen()), want, term.Snapshot())
	}
}
