package webshell

import (
	"fmt"
	"strings"
)

// diffOp is one line of a line diff: kept, removed from a, or added from b.
type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// lineDiff is the longest common subsequence diff of two texts, by line. The
// files in the fake repository are a few dozen lines, so the quadratic table
// costs nothing.
func lineDiff(a, b string) []diffOp {
	al, bl := splitLines(a), splitLines(b)
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case al[i] == bl[j]:
			ops = append(ops, diffOp{' ', al[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', al[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', bl[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', al[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', bl[j]})
	}
	return ops
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// unifiedHunks renders a diff as unified hunks with three lines of context,
// coloured the way git colours them. It returns "" when nothing changed.
func unifiedHunks(ops []diffOp) string {
	const context = 3
	changed := make([]bool, len(ops))
	someChange := false
	for i, op := range ops {
		if op.kind != ' ' {
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
			if ops[i].kind != '+' {
				aLine++
			}
			if ops[i].kind != '-' {
				bLine++
			}
			i++
			continue
		}
		start := i
		aStart, bStart := aLine, bLine
		aCount, bCount := 0, 0
		for i < len(ops) && show[i] {
			if ops[i].kind != '+' {
				aCount++
				aLine++
			}
			if ops[i].kind != '-' {
				bCount++
				bLine++
			}
			i++
		}
		fmt.Fprintf(&b, "%s@@ -%d,%d +%d,%d @@%s\r\n", cyan, aStart, aCount, bStart, bCount, reset)
		for _, op := range ops[start:i] {
			switch op.kind {
			case '-':
				b.WriteString(red + "-" + op.text + reset + "\r\n")
			case '+':
				b.WriteString(green + "+" + op.text + reset + "\r\n")
			default:
				b.WriteString(" " + op.text + "\r\n")
			}
		}
	}
	return b.String()
}
