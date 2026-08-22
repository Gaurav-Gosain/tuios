//go:build ghostty

package vt

// Style identity across render generations. Ghostty interns styles and
// recycles style IDs once their cells are gone, so any state keyed by style
// ID must not outlive a render snapshot. The screen bug this pins: after a
// clear, recycled IDs served stale cached conversions and filenames rendered
// on another style's background. The differential harness missed it because
// it compared once, after all writes: one sync, one ID generation.

import (
	"fmt"
	"testing"
)

// TestGhosttyDiffStyleChurn interleaves reads with restyled repaints, the
// way a live shell session does, and compares after every generation.
func TestGhosttyDiffStyleChurn(t *testing.T) {
	p := newDiffPair(t, 40, 8)
	for gen := 0; gen < 12; gen++ {
		var b []byte
		b = append(b, "\x1b[2J\x1b[H"...)
		for row := 0; row < 6; row++ {
			// A different palette of styles each generation, mixing
			// foreground-only 256-color and truecolor runs: the shape of
			// colorized ls output.
			n := (gen*37 + row*11) % 256
			b = append(b, fmt.Sprintf("\x1b[%d;1H\x1b[38;5;%dmname-%02d-%d", row+1, n, gen, row)...)
			b = append(b, fmt.Sprintf(" \x1b[38;2;%d;%d;%dmrgb\x1b[0m", 20+gen*9, 40+row*13, (gen*row*7)%256)...)
		}
		p.write(t, b)
		ctx := fmt.Sprintf("churn gen %d", gen)
		// Reading between generations is the point: it forces a sync and
		// populates any per-ID state before the next generation recycles
		// the IDs.
		p.compareScreens(t, ctx)
		p.compareRender(t, ctx)
	}
}
