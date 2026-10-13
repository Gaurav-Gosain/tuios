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

// unifiedHunks renders a diff as unified hunks with three lines of context,
// coloured the way git colours them. It returns "" when nothing changed.
func unifiedHunks(ops []diffOp) string {
	const context = 3
	changed := make([]bool, len(ops))
	someChange := false
	for i, op := range ops {
		if op.Op != ' ' {
			changed[i] = true
			someChange = true
		}
	}
	if !someChange {
		return ""
	}
	// Mark every op within context of a change as shown.
	show := make([]bool, len(ops))
	for i := range ops {
		if !changed[i] {
			continue
		}
		for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
			show[k] = true
		}
	}
	var b strings.Builder
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
		start := i
		aStart, bStart := aLine, bLine
		aCount, bCount := 0, 0
		for i < len(ops) && show[i] {
			if ops[i].Op != '+' {
				aCount++
				aLine++
			}
			if ops[i].Op != '-' {
				bCount++
				bLine++
			}
			i++
		}
		fmt.Fprintf(&b, "%s@@ -%d,%d +%d,%d @@%s\r\n", cyan, aStart, aCount, bStart, bCount, reset)
		for _, op := range ops[start:i] {
			switch op.Op {
			case '-':
				b.WriteString(red + "-" + op.Line + reset + "\r\n")
			case '+':
				b.WriteString(green + "+" + op.Line + reset + "\r\n")
			default:
				b.WriteString(" " + op.Line + "\r\n")
			}
		}
	}
	return b.String()
}
