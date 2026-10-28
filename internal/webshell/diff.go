package webshell

import (
	"fmt"
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
	al, bl := linediff.Split(a), linediff.Split(b)
	if (len(al)+1)*(len(bl)+1) > maxDiffCells {
		return linediff.Replace(al, bl)
	}
	return linediff.LCS(al, bl)
}

// opHunk is one hunk of a line diff: where it starts on each side, how many
// lines it covers there, and its lines.
type opHunk struct {
	aStart, aCount, bStart, bCount int
	ops                            []diffOp
}

// groupHunks splits a diff into hunks with context lines of context around
// every change, the way git does. It returns nil when nothing changed.
func groupHunks(ops []diffOp, context int) []opHunk {
	// Mark every op within context of a change as shown.
	show := make([]bool, len(ops))
	someChange := false
	for i, op := range ops {
		if op.Op == ' ' {
			continue
		}
		someChange = true
		for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
			show[k] = true
		}
	}
	if !someChange {
		return nil
	}
	var out []opHunk
	aLine, bLine := 1, 1
	for i := 0; i < len(ops); {
		if !show[i] {
			if ops[i].Op != '+' {
				aLine++
			}
			if ops[i].Op != '-' {
				bLine++
			}
			i++
			continue
		}
		h := opHunk{aStart: aLine, bStart: bLine}
		start := i
		for i < len(ops) && show[i] {
			if ops[i].Op != '+' {
				h.aCount++
				aLine++
			}
			if ops[i].Op != '-' {
				h.bCount++
				bLine++
			}
			i++
		}
		h.ops = ops[start:i]
		out = append(out, h)
	}
	return out
}

// header is the hunk's @@ line. A side with no lines starts at the line
// before it, as git writes it.
func (h opHunk) header() string {
	a, b := h.aStart, h.bStart
	if h.aCount == 0 {
		a--
	}
	if h.bCount == 0 {
		b--
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", a, h.aCount, b, h.bCount)
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
	for _, h := range groupHunks(ops, 3) {
		line(cyan, h.header(), reset)
		for _, op := range h.ops {
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
