//go:build ghostty

package vt

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
			n := (gen*37 + row*11) % 256
			b = append(b, fmt.Sprintf("\x1b[%d;1H\x1b[38;5;%dmname-%02d-%d", row+1, n, gen, row)...)
			b = append(b, fmt.Sprintf(" \x1b[38;2;%d;%d;%dmrgb\x1b[0m", 20+gen*9, 40+row*13, (gen*row*7)%256)...)
		}
		p.write(t, b)
		ctx := fmt.Sprintf("churn gen %d", gen)
		p.compareScreens(t, ctx)
		p.compareRender(t, ctx)
	}
}
