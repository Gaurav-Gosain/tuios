package app

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLayerOverTheDirectPaneIsDrawn opens a layer over a lone pane beside
// the rail while the pane is drawn from its emulator (pane_cells.go): the
// label a rail file drag carries, the label a pane's drag carries, and the
// quick look card. Each sits above the pane, so it must be on the canvas over
// the pane's cells, on the frame it opens and on a frame that only types into
// the pane under it. The ways it could fail: the pane's cells copied after
// every layer, so a layer over the pane is drawn under them; a kept canvas
// whose pane copy writes over such a layer; and the frame check not looking
// inside the pane's rectangle, so neither shows in a test.
func TestLayerOverTheDirectPaneIsDrawn(t *testing.T) {
	report := t.TempDir() + "/frame-check.log"
	prev := frameCheckPath
	frameCheckPath = report
	t.Cleanup(func() { frameCheckPath = prev })

	cases := []struct {
		name string
		open func(m *OS)
		want string
	}{
		{"rail file drag label", func(m *OS) {
			m.fileDrag = fileDragState{Pressed: true, Dragging: true, Src: dropSource{Name: "dragged-file.txt"}}
		}, "dragged-file.txt"},
		{"quick look card", func(m *OS) {
			m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
			m.quickLook.open = true
			m.quickLook.target = quickLookTarget{path: "/tmp/quick-look-name.go", name: "quick-look-name.go"}
		}, "quick-look-name.go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(report)
			m := keyPaneOS(t, io.Discard)
			m.MarkAllDirty()
			keyFrame(m)
			typeKey(m, 0)
			keyFrame(m)
			if !m.canvasKept {
				t.Fatal("a typed key did not keep the canvas, so the direct pane is not under test")
			}
			pane := paneContentRect(m.Windows[0])
			m.LastMouseX, m.LastMouseY = pane.Min.X+10, pane.Min.Y+3
			c.open(m)
			for i := range 4 {
				if i > 0 {
					typeKey(m, i)
				}
				m.renderSkipped = false
				keyFrame(m)
				if b, err := os.ReadFile(report); err == nil && len(b) > 0 {
					t.Fatalf("frame %d: %s", i, b)
				}
				if !canvasHasText(m.renderCanvas, c.want) {
					t.Fatalf("frame %d (kept %v): %q is not on the canvas", i, m.canvasKept, c.want)
				}
			}
		})
	}
}

// canvasHasText reports whether a row of the canvas holds text.
func canvasHasText(c *frameCanvas, text string) bool {
	for y := range c.Lines {
		var b strings.Builder
		for x := range c.Lines[y] {
			b.WriteString(c.Lines[y][x].Content)
		}
		if strings.Contains(b.String(), text) {
			return true
		}
	}
	return false
}
