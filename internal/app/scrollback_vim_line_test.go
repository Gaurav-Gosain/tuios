package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
)

func vimLineState(lines []string) *scrollback.VimState {
	return &scrollback.VimState{Lines: lines}
}

// TestASearchMatchIsReadable. It was drawn with foreground and background the
// same colour, so every match was a solid block with the text invisible.
//
// Negative control: setting the foreground back to searchBg fails here.
func TestASearchMatchIsReadable(t *testing.T) {
	searchBg := lipgloss.Color("#887722")
	searchFg := lipgloss.Color("#221100")

	vim := vimLineState([]string{"find me here"})
	vim.Search.Set("me")
	vim.CursorY = 5
	vim.SearchMatches = []scrollback.VimSearchMatch{{Line: 0, StartX: 5, EndX: 7}}

	out := renderVimLine(vim, 0, 40,
		lipgloss.Color("#112233"), lipgloss.Color("#cccccc"), lipgloss.Color("#445566"), lipgloss.Color("#cccccc"), searchBg, searchFg,
		lipgloss.Color("#cccccc"))

	if !strings.Contains(out, "48;2;136;119;34") {
		t.Fatal("ASSERTION: the match is not painted, so there is nothing to read")
	}
	if strings.Contains(out, "38;2;136;119;34;48;2;136;119;34") {
		t.Error("the match is drawn with its text in its own background colour, so it is invisible")
	}
	if !strings.Contains(out, "38;2;34;17;0") {
		t.Error("the match is not drawn in the configured match text colour")
	}
}
