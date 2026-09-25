package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func sidebarSides() []string { return []string{"left", "right", ""} }

// TestDividerCellsStayInTheStylesOwnGlyphs walks every style the settings page
// offers against every shape the content region takes, so a style added to the
// list is asked the same question without anyone rewriting this.
func TestDividerCellsStayInTheStylesOwnGlyphs(t *testing.T) {
	for _, style := range config.BorderStyles {
		for _, dock := range []string{"top", "bottom", "hidden"} {
			for _, side := range sidebarSides() {
				t.Run(fmt.Sprintf("%s/%s-dock/%s", style, dock, sidebarName(side)), func(t *testing.T) {
					m := extentOSStyled(t, 4, dock, side, style)
					own := styleGlyphs(config.Global.GetBorderForStyle())
					g := frameCells(t, m)
					for _, c := range dividerCells(m) {
						if got := cellAt(g, c.X, c.Y); !strings.ContainsRune(own, got) {
							t.Errorf("the divider cell (%d,%d) is %q, which is not one of this style's own glyphs %q",
								c.X, c.Y, string(got), own)
						}
					}
				})
			}
		}
	}
}
