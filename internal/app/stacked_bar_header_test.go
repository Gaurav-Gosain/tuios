package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The stacked layout's collapsed bars used to be plain text labels. The bar is
// the pane's own header — the control pill, the rule, the title badge — so the
// stack reads as full panes with their bodies taken away. The frame has to
// show the pane's title and the pill on the bar's row.
func TestAStackedBarWearsTheOrdinaryHeader(t *testing.T) {
	prev := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = prev })

	r := newRig(t, 2)
	r.m.AutoTiling = true
	r.m.ApplyLayoutModeName(config.LayoutModeStacked)
	r.m.TileAllWindows()

	var bar, active = -1, -1
	for i, w := range r.m.visibleTiledWindows() {
		if w.Height == 1 && bar < 0 {
			bar = i
		} else if w.Height > 1 {
			active = i
		}
	}
	if bar < 0 || active < 0 {
		t.Fatalf("setup: stacked tiling of two panes gave heights %v", func() []int {
			hs := []int{}
			for _, w := range r.m.visibleTiledWindows() {
				hs = append(hs, w.Height)
			}
			return hs
		}())
	}

	w := r.m.visibleTiledWindows()[bar]
	w.CustomName = "stackbar probe"

	layers := r.m.stackedBarTitleLayers()
	if len(layers) != 1 {
		t.Fatalf("the stack drew %d bar layers, want 1", len(layers))
	}

	rows := strings.Split(r.m.View().Content, "\n")
	barRow := w.Y
	if barRow >= len(rows) {
		t.Fatalf("the frame is %d rows, the bar sits on %d", len(rows), barRow)
	}
	line := rows[barRow]
	if !strings.Contains(line, "stackbar probe") {
		t.Errorf("the bar row %q does not name the pane", line)
	}
	// The dots style idles as discs; the close mark only shows on hover.
	if !strings.Contains(line, "●") {
		t.Errorf("the bar row %q does not carry the control pill", line)
	}
}
