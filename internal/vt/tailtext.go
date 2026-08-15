package vt

import "strings"

// TailText returns the last n rows of the active screen that carry text, in
// reading order.
//
// Blank rows are skipped rather than counted. An agent draws its live state in a
// box at the bottom of the pane and pads below it, so counting the padding would
// push the box out of the window a rule can see.
//
// It reads the active screen, so a harness drawing in the alternate screen is
// still readable. Reading further up than a handful of rows finds transcript
// history, which says what the agent did rather than what it is doing.
//
// NOT REENTRANT. The caller holds the terminal lock, the same as
// GetTerminalState.
func (e *Emulator) TailText(n int) []string {
	if n <= 0 {
		return nil
	}
	w, h := e.Width(), e.Height()
	if w <= 0 || h <= 0 {
		return nil
	}

	out := make([]string, 0, n)
	var b strings.Builder
	for y := h - 1; y >= 0 && len(out) < n; y-- {
		b.Reset()
		for x := 0; x < w; x++ {
			if c := e.CellAt(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
		if line := strings.TrimRight(b.String(), " \t"); line != "" {
			out = append(out, line)
		}
	}

	// Collected bottom-up, handed back top-down: a rule is written against what
	// the pane looks like, and that is the order someone reads it in.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
