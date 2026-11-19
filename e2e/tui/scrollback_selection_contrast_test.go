package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestScrollbackBrowserSelectionIsReadable selects a line in the scrollback
// browser's output view and measures each selected glyph against the
// selection ground (#45475A by default), and the cursor cell after them
// against its own ground. The browser draws selected text in
// the panel's dim text colour, which on catppuccin_latte is dark: the same
// bug as copy mode's selection. The cursor cell had it too. The text must meet the contrast floor under
// the dark default and under catppuccin_latte.
//
// Negative control: see NEGATIVE_CONTROLS.md, "Scrollback browser selection".
func TestScrollbackBrowserSelectionIsReadable(t *testing.T) {
	sel := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x45, G: 0x47, B: 0x5a}
	for _, look := range chromeLooks {
		t.Run(look.name, func(t *testing.T) {
			appearance := "border_style = \"rounded\"\n"
			if look.theme != "" {
				appearance = "theme = \"" + look.theme + "\"\n" + appearance
			}
			term, _ := startOverlaySession(t, appearance, truecolor)
			enterTerminalMode(t, term)
			runInShell(t, term, `printf '\033]133;A\007$ \033]133;B\007words\n\033]133;C\007'; printf 'PICK%sME plain words here\n' -; printf '\033]133;D;0\007'`, "PICK-ME plain words here", shellTimeout)
			sendKeys(t, term, tuitest.Ctrl('b'), "s")
			if err := term.WaitForText(browserTitle, uiTimeout); err != nil {
				t.Fatalf("the scrollback browser did not open: %v\n%s", err, term.Snapshot())
			}
			// Output mode, then a visual selection of the whole line.
			sendKeys(t, term, "l", "v", "$")
			var shot tuitest.Screen
			row := -1
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				cols, rows := s.Size()
				for r := range rows {
					n := 0
					for c := range cols {
						if s.Cell(c, r).Bg == sel && strings.TrimSpace(s.Cell(c, r).Content) != "" {
							n++
						}
					}
					if n >= 5 {
						shot, row = s, r
						return true
					}
				}
				return false
			}, uiTimeout); err != nil {
				t.Fatalf("no selection on screen: %v\n%s", err, term.SnapshotStyled())
			}
			host := hostPalette(t, look.theme)
			savePNG(t, shot, host, artifactDir(t), "scrollback-selection-"+look.name)
			cols, _ := shot.Size()
			// The selected cells, and the cursor cell right after them,
			// which has a ground of its own.
			last := -1
			for c := range cols {
				if shot.Cell(c, row).Bg == sel {
					last = c
				}
			}
			for c := range cols {
				cell := shot.Cell(c, row)
				cursor := c == last+1 && cell.Bg.Kind == tuitest.ColorRGB
				if (cell.Bg != sel && !cursor) || strings.TrimSpace(cell.Content) == "" {
					continue
				}
				fg, bg := cellColour(cell.Fg, host, host.FG), cellColour(cell.Bg, host, host.BG)
				if r := overlay.ContrastRatio(fg, bg); r < overlay.ContrastFloor {
					t.Fatalf("ASSERTION: selected %q at column %d is %.2f:1 (%v on %v), under %.1f\n%s",
						cell.Content, c, r, fg, bg, overlay.ContrastFloor, term.SnapshotStyled())
				}
			}
		})
	}
}
