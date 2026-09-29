package app

import (
	"regexp"
	"strconv"
	"testing"
)

// cupRow matches a cursor position (CUP) and captures its 1-based row.
var cupRow = regexp.MustCompile(`\x1b\[(\d+);(\d+)H`)

// TestTextSizingProbeLeavesTitleBarAlone is the regression test for opencode
// breaking its pane's top border.
//
// opencode probes for text sizing on start-up: it homes the cursor and prints
// OSC 66 with a single space, then asks where the cursor went. The passthrough
// replays that sequence to the host at the pane's first content row and, to
// tidy up wrapped command text, also blanked the row above it. The row above
// the first content row is the title bar (or the shared border), and the blank
// went straight to the host, so the renderer never repainted it: the border
// and the title badge stayed wiped.
//
// Every row the passthrough writes must be inside the content area.
func TestTextSizingProbeLeavesTitleBarAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tiled bool
	}{
		{"own border", false},
		{"shared border", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "textsize-0001", 60, 20)
			win.X, win.Y = 0, 2
			win.Tiled = tc.tiled
			m := newTestOS(win)
			m.Width, m.Height = 100, 40
			m.setupTextSizingPassthrough(win)

			// opencode's probe, byte for byte as captured from opencode 1.x.
			win.LockIO()
			_, _ = win.Terminal.Write([]byte("\x1b[H\x1b]66;w=1; \x1b\\"))
			win.UnlockIO()

			m.RefreshTextSizing()
			out := string(m.TextSizingState.pendingOutput)
			if out == "" {
				t.Fatal("setup: the probe produced no passthrough output")
			}

			contentTop := win.Y + win.BorderOffset() // 0-based
			for _, mm := range cupRow.FindAllStringSubmatch(out, -1) {
				row, _ := strconv.Atoi(mm[1])
				if row-1 < contentTop {
					t.Fatalf("passthrough wrote row %d (0-based), above the content area that starts at row %d; "+
						"that row is the title bar\n%q", row-1, contentTop, out)
				}
			}
		})
	}
}
