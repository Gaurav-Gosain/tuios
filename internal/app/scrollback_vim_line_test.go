package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/charmbracelet/x/ansi"
)

// What the preview pane draws over the output it shows.
//
// Three faults were visible in one screenshot: a line selection that stopped
// at the last character of each line, so a block of selected text had a ragged
// right edge and did not read as a selection; a search match drawn with its
// text in its own background colour, so the thing you searched for was
// invisible inside a solid block; and a panel fill that survived only where
// nothing had been drawn.

func vimLineState(lines []string) *scrollback.VimState {
	return &scrollback.VimState{Lines: lines}
}

// TestALineSelectionCoversTheLine.
//
// Negative control: highlighting only the runes of the line leaves the padding
// unstyled and this fails.
func TestALineSelectionCoversTheLine(t *testing.T) {
	const width = 40
	vim := vimLineState([]string{"short", "also short"})
	vim.Mode = scrollback.VimVisualLine
	vim.VisualStartY = 0
	vim.CursorY = 1
	vim.CursorX = 0

	visual := lipgloss.Color("#445566")
	out := renderVimLine(vim, 0, width,
		lipgloss.Color("#112233"), visual, lipgloss.Color("#887722"),
		lipgloss.Color("#000000"), lipgloss.Color("#cccccc"))

	if got := ansi.StringWidth(out); got != width {
		t.Fatalf("the line is %d columns, want %d", got, width)
	}
	// The selection colour has to appear after the text ends, which is what
	// makes the right edge straight.
	textEnd := strings.Index(out, "short")
	if textEnd < 0 {
		t.Fatal("ASSERTION: the line does not contain its own text")
	}
	// lipgloss writes a background as 48;2;r;g;b.
	if !strings.Contains(out[textEnd:], "48;2;68;85;102") {
		t.Error("the selection stops at the end of the text, so the block has a ragged edge")
	}
}

// TestAnUnselectedLineIsNotPainted. The other half of the rule: the padding
// carries the selection only when the line is in it.
func TestAnUnselectedLineIsNotPainted(t *testing.T) {
	const width = 40
	vim := vimLineState([]string{"first", "second"})
	vim.Mode = scrollback.VimVisualLine
	vim.VisualStartY = 0
	vim.CursorY = 0

	visual := lipgloss.Color("#445566")
	out := renderVimLine(vim, 1, width,
		lipgloss.Color("#112233"), visual, lipgloss.Color("#887722"),
		lipgloss.Color("#000000"), lipgloss.Color("#cccccc"))

	if strings.Contains(out, "48;2;68;85;102") {
		t.Error("a line outside the selection was painted with it")
	}
	if got := ansi.StringWidth(out); got != width {
		t.Errorf("the line is %d columns, want %d", got, width)
	}
}

// TestASearchMatchIsReadable. It was drawn with foreground and background the
// same colour, so every match was a solid block with the text invisible.
//
// Negative control: setting the foreground back to searchBg fails here.
func TestASearchMatchIsReadable(t *testing.T) {
	searchBg := lipgloss.Color("#887722")
	searchFg := lipgloss.Color("#221100")

	vim := vimLineState([]string{"find me here"})
	vim.SearchQuery = "me"
	// The cursor is parked off this line, so the only mark on it is the
	// match: a cursor cell would split the run and make the check vacuous.
	vim.CursorY = 5
	vim.SearchMatches = []scrollback.VimSearchMatch{{Line: 0, StartX: 5, EndX: 7}}

	out := renderVimLine(vim, 0, 40,
		lipgloss.Color("#112233"), lipgloss.Color("#445566"), searchBg, searchFg,
		lipgloss.Color("#cccccc"))

	// The match has to be painted at all, or this proves nothing.
	if !strings.Contains(out, "48;2;136;119;34") {
		t.Fatal("ASSERTION: the match is not painted, so there is nothing to read")
	}
	// And its text must not be its own background colour.
	if strings.Contains(out, "38;2;136;119;34;48;2;136;119;34") {
		t.Error("the match is drawn with its text in its own background colour, so it is invisible")
	}
	if !strings.Contains(out, "38;2;34;17;0") {
		t.Error("the match is not drawn in the configured match text colour")
	}
}
