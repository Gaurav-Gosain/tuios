//go:build ghostty

package vt

import "fmt"

// CompareHistoryRows reads every history row of t both ways, the formatter
// path and the per-cell read, and reports how many rows the fast path called
// exact, how many it handed to the fallback, and every exact row whose text
// or widths are not the per-cell read's, cell for cell. For
// ghostty_history_rows_test.go.
func CompareHistoryRows(t *GhosttyTerminal) (exact, fallback int, mismatches []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	src, from, end := t.historyRangeLocked(0, t.scrollbackLenLocked())
	if src == nil {
		return 0, 0, nil
	}
	lines, ok := t.historyTextFast(src, from, end, t.width)
	for i := range lines {
		if !ok[i] {
			fallback++
			continue
		}
		exact++
		want := t.readHistoryLineLocked(src, from+i)
		got := lines[i]
		if len(got) != len(want) {
			mismatches = append(mismatches, fmt.Sprintf("row %d: %d cells, want %d", from+i, len(got), len(want)))
			continue
		}
		for x := range want {
			g, w := got[x], want[x]
			if g.Content != w.Content || g.Width != w.Width {
				mismatches = append(mismatches, fmt.Sprintf("row %d col %d: got %q w=%d, want %q w=%d",
					from+i, x, g.Content, g.Width, w.Content, w.Width))
				break
			}
		}
	}
	return exact, fallback, mismatches
}
