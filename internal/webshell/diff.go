package webshell

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/linediff"
)

// diffOp is one line of a line diff: kept, removed from a, or added from b.
type diffOp = linediff.Op

// maxDiffCells caps the longest common subsequence table of one diff. The
// files in the fake repository are a few dozen lines, so the cap is never
// reached by them. A page that writes a large file gets a diff that removes
// every old line and adds every new one, which is still correct, instead of a
// table that takes the tab's memory.
const maxDiffCells = 1 << 20

// lineDiff is the longest common subsequence diff of two texts, by line.
func lineDiff(a, b string) []diffOp {
	return linediff.Bounded(linediff.Split(a), linediff.Split(b), maxDiffCells)
}

// unifiedHunks renders a diff as unified hunks with three lines of context,
// coloured the way git colours them. It returns "" when nothing changed.
func unifiedHunks(ops []diffOp) string {
	var b strings.Builder
	line := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
		}
		b.WriteString("\r\n")
	}
	for _, h := range linediff.Hunks(ops, 3) {
		line(cyan, h.Header(), reset)
		for _, op := range h.Ops {
			switch op.Op {
			case '-':
				line(red, "-", op.Line, reset)
			case '+':
				line(green, "+", op.Line, reset)
			default:
				line(" ", op.Line)
			}
		}
	}
	return b.String()
}
