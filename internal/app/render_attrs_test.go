package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// attrsLine carries one run of each text attribute the pane renderer is
// expected to keep: underline in two styles and with a colour, bold, italic,
// faint, blink, reverse and strikethrough.
const attrsLine = "\x1b[4munder\x1b[0m \x1b[1mbold\x1b[0m \x1b[3mital\x1b[0m " +
	"\x1b[4:3;58;5;196mcurl\x1b[0m \x1b[2mfaint\x1b[0m \x1b[5mblink\x1b[0m " +
	"\x1b[7mrev\x1b[0m \x1b[9mstrike\x1b[0m \x1b[1;3;4mall\x1b[0m"

// attrsOnly is the part of a style that no render path is allowed to change.
// Colours are left out because the dim changes them on purpose.
func attrsOnly(s uv.Style) uv.Style {
	return uv.Style{Attrs: s.Attrs, Underline: s.Underline, UnderlineColor: s.UnderlineColor}
}

// checkRowAttrs parses frame back into cells and checks that every cell of
// row y carries the attributes of want, the emulator's own cells for that row.
func checkRowAttrs(t *testing.T, path, frame string, cols, rows, y int, want uv.Line) {
	t.Helper()
	canvas := &frameCanvas{}
	canvas.Resize(cols, rows)
	canvas.Clear()
	uv.NewStyledString(frame).Draw(canvas, canvas.Bounds())
	for x := 0; x < len(want) && x < cols; x++ {
		w := &want[x]
		if w.Width == 0 {
			continue
		}
		got := canvas.CellAt(x, y)
		wantStyle, gotStyle := attrsOnly(w.Style), attrsOnly(got.Style)
		if got.Content != w.Content || !gotStyle.Equal(&wantStyle) {
			t.Errorf("%s: cell (%d,%d) rendered as %q %+v, emulator holds %q %+v",
				path, x, y, got.Content, gotStyle, w.Content, wantStyle)
			return
		}
	}
}

func rowCells(win *terminal.Window, y int) uv.Line {
	line := make(uv.Line, win.Terminal.Width())
	for x := range line {
		if c := win.Terminal.CellAt(x, y); c != nil {
			line[x] = *c
		}
	}
	return line
}

// TestEveryRenderPathKeepsTextAttributes renders one line of SGR attributes
// through each way a pane can be drawn and checks that all of them emit the
// same attributes. The unfocused fast path hands the grid to the emulator's own
// renderer and always kept them; the cell loop that draws a focused, dimmed,
// copy-mode or scrolled pane dropped underline everywhere and, for an unfocused
// pane outside terminal mode, every attribute but colour. The same pane then
// changed how it looked when it gained or lost focus.
func TestEveryRenderPathKeepsTextAttributes(t *testing.T) {
	const cols, rows = 80, 6

	setup := func(t *testing.T) (*terminal.Window, *OS) {
		win := newTestWindow(t, "attrs", cols, rows)
		m := newTestOS(win)
		win.LockIO()
		// The cursor is hidden so no cell wears the fake cursor's colours.
		_, _ = win.Terminal.Write([]byte("\x1b[?25l" + attrsLine + "\r\n"))
		win.UnlockIO()
		win.MarkContentDirty()
		// The line has to reach the renderer as the attributes it asks for,
		// or the comparison below proves nothing.
		if c := win.Terminal.CellAt(0, 0); c == nil || c.Style.Underline != uv.UnderlineSingle {
			t.Fatalf("emulator did not underline the first cell: %+v", c)
		}
		if c := win.Terminal.CellAt(16, 0); c == nil || c.Style.Underline != uv.UnderlineCurly || c.Style.UnderlineColor == nil {
			t.Fatalf("emulator did not give the curl cell a coloured curly underline: %+v", c)
		}
		return win, m
	}

	render := func(m *OS, win *terminal.Window, focused, terminalMode bool) string {
		win.MarkContentDirty()
		win.InvalidateCache()
		return m.renderTerminal(win, focused, terminalMode)
	}

	type path struct {
		name                  string
		focused, terminalMode bool
		dim, copyMode         bool
	}
	paths := []path{
		{name: "focused", focused: true, terminalMode: true},
		{name: "focused window mode", focused: true},
		{name: "unfocused fast path", terminalMode: true},
		{name: "unfocused fast path window mode"},
		{name: "unfocused dimmed", terminalMode: true, dim: true},
		{name: "unfocused dimmed window mode", dim: true},
		{name: "focused copy mode", focused: true, copyMode: true},
		{name: "unfocused copy mode", copyMode: true},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			if p.dim {
				withDim(t, 50)
			} else {
				prev := config.Global.DimUnfocused
				config.Global.DimUnfocused = 0
				t.Cleanup(func() { config.Global.DimUnfocused = prev })
			}
			win, m := setup(t)
			m.Settings = config.Global
			if p.copyMode {
				win.EnterCopyMode()
				if win.CopyMode.CursorY == 0 {
					t.Fatal("the copy cursor sits on the row under test")
				}
			}
			frame := render(m, win, p.focused, p.terminalMode)
			checkRowAttrs(t, p.name, frame, cols, rows, 0, rowCells(win, 0))
		})
	}

	// A pane scrolled back reads the row from scrollback rather than from the
	// grid, so it is its own path.
	for _, focused := range []bool{true, false} {
		name := "unfocused scrollback"
		if focused {
			name = "focused scrollback"
		}
		t.Run(name, func(t *testing.T) {
			prev := config.Global.DimUnfocused
			config.Global.DimUnfocused = 0
			t.Cleanup(func() { config.Global.DimUnfocused = prev })
			win, m := setup(t)
			m.Settings = config.Global
			win.LockIO()
			for range rows * 2 {
				_, _ = win.Terminal.Write([]byte("filler\r\n"))
			}
			win.UnlockIO()
			sbLen := win.ScrollbackLenSync()
			if sbLen == 0 {
				t.Fatal("the attribute line did not reach scrollback")
			}
			// Scroll so the oldest scrollback line, the attribute line, is
			// the top row of the view.
			scrollBack(t, win, sbLen)
			want := win.ScrollbackLine(0)
			if len(want) == 0 || want[0].Style.Underline != uv.UnderlineSingle {
				t.Fatalf("scrollback line 0 is not the attribute line: %+v", want)
			}
			frame := render(m, win, focused, false)
			checkRowAttrs(t, name, frame, cols, rows, 0, want)
		})
	}
}
