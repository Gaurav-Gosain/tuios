package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// The border box may skip lipgloss's word wrap only when the pane body is
// already exactly the pane's rectangle. Getting that wrong does not slow the
// screen down, it corrupts it, and the ways a cell grid disagrees with a byte
// or a rune count are all Unicode: a wide rune covering two columns, a
// combining mark covering none, a joined emoji covering two across many runes.
// Every case here is one of those, and three of them sit the awkward glyph at
// the last column of the grid, where a pane has the least room to be wrong.
var preShapedCases = []struct {
	name string
	text string
}{
	{"ascii", "hello world"},
	{"cjk", "你好世界 日本語テキスト"},
	{"combining", "éàôüñ café"},
	{"zwj-family", "👨‍👩‍👧‍👦 🏳️‍🌈"},
	{"emoji", "😀🚀💩 abc"},
	{"blocks", strings.Repeat("█▓▒░", 8)},
	{"regional-indicators", "🇺🇸🇯🇵"},
	{"wide-rune-at-wrap-column", strings.Repeat("a", 59) + "你好"},
	{"wide-rune-at-last-column", "\x1b[1;60H你好"},
	{"combining-at-last-column", "\x1b[1;60Hé"},
	{"zwj-at-last-column", "\x1b[1;59H👨‍👩"},
	{"styled-runs", "\x1b[31mred\x1b[0m \x1b[42;1mgreen\x1b[0m \x1b[4munder\x1b[0m 你好"},
	{"styled-to-end-of-line", "\x1b[41m" + strings.Repeat("x", 60)},
}

const (
	preShapedCols = 60
	preShapedRows = 8
)

// preShapedWindow builds a bordered pane whose emulator is exactly its content
// rectangle, painted with the given text.
func preShapedWindow(t *testing.T, id, text string) *terminal.Window {
	t.Helper()
	win := newTestWindow(t, id, preShapedCols+2, preShapedRows+2)
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[H" + text))
	win.UnlockIO()
	return win
}

// withoutPreShaping runs fn with the border box forced back through the wrap,
// which is what TUIOS_NO_PRESHAPED does for a running client.
func withoutPreShaping(t *testing.T, fn func()) {
	t.Helper()
	preShapedDisabled = true
	defer func() { preShapedDisabled = false }()
	fn()
}

// TestPreShapedBoxMatchesTheWrappedBox is the differential test: for every case
// the fast path accepts, the frame it draws is byte for byte the frame the wrap
// drew. It covers the bordered box and the zen one, which reserves the same
// cells with a hidden frame.
func TestPreShapedBoxMatchesTheWrappedBox(t *testing.T) {
	for _, tc := range preShapedCases {
		t.Run(tc.name, func(t *testing.T) {
			win := preShapedWindow(t, "box-"+tc.name, tc.text)
			m := newTestOS(win)
			m.Mode = TerminalMode
			border := lipgloss.Color("62")

			render := func(focused bool) string {
				win.InvalidateCache()
				win.MarkContentDirty()
				return m.renderWindowBox(win, 0, focused, border)
			}

			for _, focused := range []bool{false, true} {
				fast := render(focused)
				var slow string
				withoutPreShaping(t, func() { slow = render(focused) })
				if fast != slow {
					t.Errorf("focused=%v: bordered box differs\n fast %q\n slow %q",
						focused, ansi.Strip(fast), ansi.Strip(slow))
				}
			}
		})
	}
}

// TestPreShapedSurvivesAPaneWhoseGridHasNotCaughtUp covers the case that made
// fitToContentBox necessary in the first place: a snap animation moves a pane's
// rectangle every tick and deliberately leaves the emulator at the size it had,
// so the body is a rectangle the pane no longer is. The grid is packed with
// wide runes here, so shrinking the pane by an odd number of columns also cuts
// the loop off between a lead cell and its continuation.
//
// Whatever the fast path decides, the frame has to come out exactly the pane's
// rectangle and exactly what the wrap would have drawn.
func TestPreShapedSurvivesAPaneWhoseGridHasNotCaughtUp(t *testing.T) {
	for _, delta := range []int{-7, -6, -1, 1, 6, 7} {
		win := preShapedWindow(t, "stale", strings.Repeat("你好", 40))
		m := newTestOS(win)
		m.Mode = TerminalMode
		win.Width += delta
		win.Height += delta

		render := func(focused bool) string {
			win.InvalidateCache()
			win.MarkContentDirty()
			return m.renderWindowBox(win, 0, focused, lipgloss.Color("62"))
		}
		for _, focused := range []bool{false, true} {
			fast := render(focused)
			var slow string
			withoutPreShaping(t, func() { slow = render(focused) })
			if fast != slow {
				t.Errorf("delta=%d focused=%v: frame differs from the wrapped one\n fast %q\n slow %q",
					delta, focused, ansi.Strip(fast), ansi.Strip(slow))
			}
			for i, line := range strings.Split(fast, "\n") {
				if got := ansi.StringWidth(line); got != win.Width {
					t.Errorf("delta=%d focused=%v: frame line %d is %d columns, want %d",
						delta, focused, i, got, win.Width)
				}
			}
		}
	}
}

// TestPreShapedDeclinesWhenTheGridOutgrowsThePane pins the guard itself: an
// emulator wider or taller than the pane's content box is a body the renderer
// clipped, so it must vouch for nothing and the wrap must run.
func TestPreShapedDeclinesWhenTheGridOutgrowsThePane(t *testing.T) {
	win := preShapedWindow(t, "outgrown", "hello")
	m := newTestOS(win)
	m.Mode = TerminalMode
	win.Width -= 4
	win.Height -= 4

	win.InvalidateCache()
	win.MarkContentDirty()
	_ = m.renderTerminal(win, false, false)
	if win.RenderedCols == win.ContentWidth() && win.RenderedRows == win.ContentHeight() {
		t.Fatalf("renderer vouched for a %dx%d grid served into a %dx%d pane",
			win.Terminal.Width(), win.Terminal.Height(), win.ContentWidth(), win.ContentHeight())
	}
}
