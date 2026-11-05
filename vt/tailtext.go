package vt

import "unsafe"

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
// It makes one pass and reads each cell once. The rows are appended to one
// byte buffer, trailing blanks are cut by moving its length back (a blank row
// rewinds to its start and costs nothing), and the buffer is regrown between
// rows in sized steps rather than by doubling. The returned rows are substrings
// of that buffer, cut with unsafe.String, which is safe here: the buffer is not
// written after the rows are cut and is not kept, so the strings stay valid
// and unchanged for as long as the caller holds them.
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

	m := min(n, h)
	var endsStack [64]int
	ends := endsStack[:0]
	if m > len(endsStack) {
		ends = make([]int, 0, m)
	}
	buf := make([]byte, 0, m*w)
	last := w
	for y := h - 1; y >= 0 && len(ends) < n; y-- {
		start := len(buf)
		if cap(buf)-start < last {
			nb := make([]byte, start, start+min(n-len(ends), y+1)*last)
			copy(nb, buf)
			buf = nb
		}
		for x := range w {
			if c := e.CellAt(x, y); c != nil {
				buf = append(buf, CellText(c.Content)...)
			}
		}
		last = max(last, len(buf)-start)
		i := len(buf)
		for i > start && (buf[i-1] == ' ' || buf[i-1] == '\t') {
			i--
		}
		buf = buf[:i]
		if i > start {
			ends = append(ends, i)
		}
	}

	// Handed back top-down: a rule is written against what the pane looks like,
	// and that is the order someone reads it in.
	k := len(ends)
	out := make([]string, k)
	if k == 0 {
		return out
	}
	s := unsafe.String(unsafe.SliceData(buf), len(buf))
	prev := 0
	for i, end := range ends {
		out[k-1-i] = s[prev:end]
		prev = end
	}
	return out
}
