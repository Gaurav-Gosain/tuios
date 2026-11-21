package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestCopySelectionTextIsReadable selects a word in copy mode and measures
// each selected glyph against the selection ground. With catppuccin_latte the
// pane's text is dark and the default selection ground (#45475A) is dark
// too, so the selected text could not be read. The text must meet the
// contrast floor there, and stay as it was on the dark default, where it
// already did.
//
// Negative control: see NEGATIVE_CONTROLS.md, "Copy mode selection".
func TestCopySelectionTextIsReadable(t *testing.T) {
	for _, look := range chromeLooks {
		t.Run(look.name, func(t *testing.T) {
			base := t.TempDir()
			conf := copyCursorConfig
			if look.theme != "" {
				conf = "[appearance]\ntheme = \"" + look.theme + "\"\n\n" + conf
			}
			writeConfig(t, base, conf)
			term := startIn(t, base, startOpts{env: copyColorOpts.env, cols: 100, rows: 30})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			if err := term.SendKeys("clear; printf 'SELECT%sME plain words here\\n' -\r"); err != nil {
				t.Fatalf("type: %v", err)
			}
			row := -1
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				_, rows := s.Size()
				for r := range rows {
					if strings.Contains(s.Line(r), "SELECT-ME plain words here") {
						row = r
						return true
					}
				}
				return false
			}, uiTimeout); err != nil {
				t.Fatalf("the line never printed: %v\n%s", err, term.Snapshot())
			}
			copySearch(t, term, "SELECT-ME", row)
			sendEach(t, term, "v", "$")
			sel := tuitest.Color{Kind: tuitest.ColorRGB, R: 0x45, G: 0x47, B: 0x5a}
			var shot tuitest.Screen
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				cols, _ := s.Size()
				n := 0
				for c := range cols {
					if s.Cell(c, row).Bg == sel {
						n++
					}
				}
				shot = s
				return n >= len("SELECT-ME plain words here")-1
			}, uiTimeout); err != nil {
				t.Fatalf("the selection never covered the line: %v\n%s", err, term.SnapshotStyled())
			}
			host := hostPalette(t, look.theme)
			savePNG(t, shot, host, artifactDir(t), "copy-selection-"+look.name)
			cols, _ := shot.Size()
			measured := 0
			for c := range cols {
				cell := shot.Cell(c, row)
				if cell.Bg != sel || strings.TrimSpace(cell.Content) == "" {
					continue
				}
				fg, bg := cellColour(cell.Fg, host, host.FG), cellColour(cell.Bg, host, host.BG)
				if r := overlay.ContrastRatio(fg, bg); r < overlay.ContrastFloor {
					t.Fatalf("ASSERTION: selected %q at column %d is %.2f:1 (%v on %v), under %.1f\n%s",
						cell.Content, c, r, fg, bg, overlay.ContrastFloor, term.SnapshotStyled())
				}
				measured++
			}
			if measured == 0 {
				t.Fatalf("no selected glyph to measure\n%s", term.SnapshotStyled())
			}
		})
	}
}
