package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// withoutFastBox runs fn with the fused box path off, which is what
// TUIOS_NO_FASTBOX does for a running client.
func withoutFastBox(t *testing.T, fn func()) {
	t.Helper()
	fastBoxDisabled = true
	defer func() { fastBoxDisabled = false }()
	fn()
}

// TestFastWindowBoxMatchesLipgloss is the whole argument for the fused box: the
// frame it draws is byte for byte the frame lipgloss drew. Anything less and
// this is not an optimisation, it is a rendering bug that only shows up on the
// maintainer's screen.
//
// Every pre-shaped case runs, because the passes being skipped are the ones
// that measure, and a wide rune, a combining mark or a joined emoji is where a
// column count and a byte count part company. Both focus states run, since the
// focused pane draws a cursor the unfocused one does not.
func TestFastWindowBoxMatchesLipgloss(t *testing.T) {
	for _, tc := range preShapedCases {
		t.Run(tc.name, func(t *testing.T) {
			win := preShapedWindow(t, "fast-"+tc.name, tc.text)
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
				withoutFastBox(t, func() { slow = render(focused) })
				if fast != slow {
					t.Errorf("focused=%v: the fused box differs from the lipgloss one\n fast %q\n slow %q",
						focused, ansi.Strip(fast), ansi.Strip(slow))
				}
			}
		})
	}
}

// TestFastWindowBoxMatchesAcrossSizesAndChrome runs the same equality over the
// shapes and the chrome a pane actually varies in: its size, where the title
// sits, where the controls sit, and whether it carries a name at all. The
// chrome rows are shared code, so what this is really pinning is that the fused
// path splices them in at the same places the spliced-apart box did.
func TestFastWindowBoxMatchesAcrossSizesAndChrome(t *testing.T) {
	prevTitle, prevButton := config.Global.WindowTitlePosition, config.Global.WindowButtonPosition
	t.Cleanup(func() {
		config.Global.WindowTitlePosition, config.Global.WindowButtonPosition = prevTitle, prevButton
	})

	for _, titlePos := range []string{"top", "bottom", "hidden"} {
		for _, buttonPos := range config.WindowButtonPositions {
			config.Global.WindowTitlePosition = titlePos
			config.Global.WindowButtonPosition = buttonPos
			for _, sz := range [][2]int{{3, 3}, {4, 4}, {10, 6}, {41, 13}, {80, 24}, {160, 42}} {
				for _, name := range []string{"", "editor"} {
					win := newTestWindow(t, fmt.Sprintf("chrome-%dx%d", sz[0], sz[1]), sz[0], sz[1])
					win.CustomName = name
					m := newTestOS(win)
					m.Mode = TerminalMode
					win.LockIO()
					_, _ = win.Terminal.Write([]byte("\x1b[H\x1b[31mhello\x1b[0m 你好"))
					win.UnlockIO()

					render := func() string {
						win.InvalidateCache()
						win.MarkContentDirty()
						return m.renderWindowBox(win, 0, true, lipgloss.Color("62"))
					}
					fast := render()
					var slow string
					withoutFastBox(t, func() { slow = render() })
					if fast != slow {
						t.Errorf("title=%s buttons=%s %dx%d name=%q: frames differ\n fast %q\n slow %q",
							titlePos, buttonPos, sz[0], sz[1], name, ansi.Strip(fast), ansi.Strip(slow))
					}
				}
			}
		}
	}
}

// TestFastWindowBoxMatchesUnderAFlood is the equality at the size and the style
// density the speedup was measured at. The small cases above are where Unicode
// goes wrong; this is where volume does, with a full-screen pane repainted from
// a colour ramp so nearly every cell carries a style change.
//
// It also checks the frame against the path that predates both optimisations,
// the one that puts the body through lipgloss's wrap, so the two fast paths
// cannot agree with each other while both being wrong.
func TestFastWindowBoxMatchesUnderAFlood(t *testing.T) {
	win := floodWindow(t, "flood-equal", floodCols, floodRows)
	win.CustomName = "editor"
	m := newTestOS(win)
	m.Mode = TerminalMode

	render := func() string {
		win.InvalidateCache()
		win.MarkContentDirty()
		return m.renderWindowBox(win, 0, true, lipgloss.Color("62"))
	}

	fast := render()
	var noFastBox, noEither string
	withoutFastBox(t, func() {
		noFastBox = render()
		withoutPreShaping(t, func() { noEither = render() })
	})

	if fast != noFastBox {
		t.Errorf("the fused box differs from the lipgloss box under a flood")
	}
	if fast != noEither {
		t.Errorf("the fused box differs from the wrapped box under a flood")
	}
	if got := len(strings.Split(fast, "\n")); got != win.Height {
		t.Errorf("the frame is %d rows, the pane is %d", got, win.Height)
	}
	for i, line := range strings.Split(fast, "\n") {
		if got := ansi.StringWidth(line); got != win.Width {
			t.Errorf("frame row %d is %d columns, the pane is %d", i, got, win.Width)
		}
	}
}

// TestFastWindowBoxRecordsTheSameButtons pins the side effect rather than the
// string. The chrome rows record where a pane's controls landed as they are
// drawn, and the fused path draws them through the same call; a frame that
// looked right but registered its close button on the wrong cells would pass
// every equality test above and still be broken to click on.
func TestFastWindowBoxRecordsTheSameButtons(t *testing.T) {
	win := newTestWindow(t, "buttons", 60, 12)
	win.CustomName = "editor"
	m := newTestOS(win)
	m.Mode = TerminalMode

	win.InvalidateCache()
	win.MarkContentDirty()
	_ = m.renderWindowBox(win, 0, true, lipgloss.Color("62"))
	fast := append([]WindowButtonRect(nil), m.windowButtonRects[win.ID]...)

	withoutFastBox(t, func() {
		win.InvalidateCache()
		win.MarkContentDirty()
		_ = m.renderWindowBox(win, 0, true, lipgloss.Color("62"))
	})
	slow := m.windowButtonRects[win.ID]

	if len(fast) == 0 {
		t.Fatal("the frame recorded no window controls at all")
	}
	if len(fast) != len(slow) {
		t.Fatalf("the fused box recorded %d controls, the lipgloss box %d", len(fast), len(slow))
	}
	for i := range fast {
		if fast[i] != slow[i] {
			t.Errorf("control %d landed at %+v in the fused box and %+v in the lipgloss one",
				i, fast[i], slow[i])
		}
	}
}

// TestFastWindowBoxDeclinesABorderItCannotDraw pins the guard. A border side of
// more than one rune is cycled down the rows by lipgloss, which this path does
// not do, so it must hand the frame back rather than draw the first rune of it
// on every row.
func TestFastWindowBoxDeclinesABorderItCannotDraw(t *testing.T) {
	if _, _, ok := borderSideCells(lipgloss.Color("62"), &config.Global); !ok {
		t.Fatal("the default border was declined")
	}
	if _, _, ok := borderSideCells(nil, &config.Global); ok {
		t.Error("a frame with no border colour was accepted")
	}
	for _, s := range []string{"", "ab", "│┃"} {
		if isSingleRune(s) {
			t.Errorf("%q was accepted as a single-rune border side", s)
		}
	}
	for _, s := range []string{"│", "你", "█"} {
		if !isSingleRune(s) {
			t.Errorf("%q was rejected as a single-rune border side", s)
		}
	}
}

// TestFastWindowBoxDeclinesATabbedBody covers the one thing lipgloss does to a
// body before laying it out that this path does not: expanding tabs. A body
// carrying one has to go back through lipgloss, or the two paths would draw
// different numbers of columns.
func TestFastWindowBoxDeclinesATabbedBody(t *testing.T) {
	win := newTestWindow(t, "tabbed", 40, 10)
	m := newTestOS(win)
	for _, body := range []string{"a\tb", "a\rb"} {
		if _, ok := m.fastWindowBox(strings.Repeat(body, 3), win, lipgloss.Color("62"), 1, false); ok {
			t.Errorf("a body carrying %q was accepted", body)
		}
	}
	if _, ok := m.fastWindowBox("plain", win, lipgloss.Color("62"), 1, false); !ok {
		t.Error("a plain body was declined")
	}
}

// TestFastWindowBoxDeclinesAPaneTooSmallToHaveABody covers the clamp: below
// three cells the content box stops being the pane minus its frame, so the
// vertical padding lipgloss adds is no longer nothing.
func TestFastWindowBoxDeclinesAPaneTooSmallToHaveABody(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {2, 2}, {2, 10}, {10, 2}} {
		win := &terminal.Window{ID: "tiny", Width: sz[0], Height: sz[1], Workspace: 1}
		m := &OS{Settings: config.Global}
		if _, ok := m.fastWindowBox("x", win, lipgloss.Color("62"), 1, false); ok {
			t.Errorf("a %dx%d pane was accepted", sz[0], sz[1])
		}
	}
}
