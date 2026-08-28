package app

import (
	"image/color"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// fastBoxDisabled sends every pane box back through lipgloss (TUIOS_NO_FASTBOX=1)
// so the two compositions can be compared for output and for cost on one
// binary, the same way TUIOS_NO_PRESHAPED does for the wrap.
var fastBoxDisabled = os.Getenv("TUIOS_NO_FASTBOX") == "1"

// fastWindowBox draws a pane's frame around a body that is already exactly the
// pane's rectangle, without measuring the body, or reports false and leaves the
// job to lipgloss.
//
// A CPU profile of the client under a DOOM-fire flood put 50% of all samples in
// the one Style.Render this replaces, and on a pre-shaped body every pass it
// makes over the content is a no-op it pays full price for:
//
//   - the core-text loop styles each line with an empty style, which x/ansi
//     returns unchanged;
//   - alignTextVertical pads to a height the body already is;
//   - alignTextHorizontal measures every line twice, once to find the widest
//     and once in the loop, to pad lines that are already the widest;
//   - applyBorder measures a third time to size a bottom edge that addToBorder
//     then throws away, and walks that edge a column at a time asking each
//     glyph how wide it is;
//   - applyMargins measures a fourth time, at zero margin, to size a run of
//     spaces it never uses.
//
// What survives the no-ops is one border cell on each end of each row. That is
// what this writes, in one pass, with the two chrome rows the frame carries
// spliced in as it goes rather than a box built and then cut apart.
//
// The output is byte for byte what the lipgloss path produces.
// TestFastWindowBoxMatchesLipgloss is the proof, run over every Unicode case
// the pre-shaped path already carries plus a size matrix.
func (m *OS) fastWindowBox(content string, window *terminal.Window, borderColorObj color.Color, position int, isTiling bool) (string, bool) {
	if fastBoxDisabled {
		return "", false
	}
	// Below 3x3 the content box clamps rather than shrinks, so the body is no
	// longer the pane minus its frame and the vertical padding lipgloss would
	// add is not zero.
	if window.Width < 3 || window.Height < 3 {
		return "", false
	}
	// lipgloss expands tabs and folds CRLF before it lays anything out. Neither
	// appears in a body the emulator drew cell by cell, but a body that carried
	// one would come out of the two paths differently, and IndexByte is the
	// cheapest question in the frame.
	if strings.IndexByte(content, '\t') >= 0 || strings.IndexByte(content, '\r') >= 0 {
		return "", false
	}

	left, right, ok := borderSideCells(borderColorObj, &m.Settings)
	if !ok {
		return "", false
	}

	top, bottom := m.windowBorderRows(window.ContentWidth(), borderColorObj, window, position, isTiling)

	var b strings.Builder
	b.Grow(len(top) + len(bottom) + len(content) + (len(left)+len(right)+1)*(window.Height-1) + 1)
	b.WriteString(top)
	for line := range strings.SplitSeq(content, "\n") {
		b.WriteByte('\n')
		b.WriteString(left)
		b.WriteString(line)
		b.WriteString(right)
	}
	b.WriteByte('\n')
	b.WriteString(bottom)
	return b.String(), true
}

// borderSideCells returns the left and right border cells of a pane frame,
// already styled, or false when the configured border has sides this path
// cannot reproduce.
//
// lipgloss cycles a multi-rune border side down the rows and substitutes a
// space for an empty one. Both are reproducible, but neither is a border any
// style in the registry uses, so they are declined rather than duplicated.
func borderSideCells(borderColorObj color.Color, s *config.Settings) (left, right string, ok bool) {
	border := getBorder(s)
	if !isSingleRune(border.Left) || !isSingleRune(border.Right) {
		return "", "", false
	}
	// The same two calls lipgloss's styleBorder makes for a side with a
	// foreground and no background, which is what BorderForeground sets.
	var style ansi.Style
	if borderColorObj == nil {
		return "", "", false
	}
	style = style.ForegroundColor(ansi.Color(borderColorObj))
	return style.Styled(border.Left), style.Styled(border.Right), true
}

// isSingleRune reports whether s is exactly one rune, which is the only shape
// of border side fastWindowBox draws.
func isSingleRune(s string) bool {
	if s == "" {
		return false
	}
	n := 0
	for range s {
		n++
		if n > 1 {
			return false
		}
	}
	return true
}
