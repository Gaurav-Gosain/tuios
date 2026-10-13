// Package linediff computes the line edit script between two texts by longest
// common subsequence. It is pure Go with no imports from tuios, so the browser
// build uses it too.
package linediff

import (
	"math"
	"strings"
)

// Op is one line of an edit script: Op is ' ' for a kept line, '-' for a line
// removed from a, '+' for a line added from b.
type Op struct {
	Op   byte
	Line string
}

// Split is s as lines, without a final empty line for a trailing newline. An
// empty s has no lines.
func Split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// LCS is the edit script from a to b by a longest common subsequence table.
// The table has (len(a)+1)*(len(b)+1) cells, so a caller bounds that product
// before it calls; Replace is the answer for two texts too large to compare.
//
// The lines are numbered first, equal lines the same number, so the table
// compares integers, not strings. The table is one block of uint16, which
// holds any length on a side shorter than 65536 lines. Past that the script is
// Replace.
func LCS(a, b []string) []Op {
	n, m := len(a), len(b)
	if min(n, m) > math.MaxUint16-1 {
		return Replace(a, b)
	}
	ids := make(map[string]int32, n+m)
	number := func(lines []string) []int32 {
		out := make([]int32, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = int32(len(ids))
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	ha, hb := number(a), number(b)
	w := m + 1
	table := make([]uint16, (n+1)*w)
	at := func(i, j int) uint16 { return table[i*w+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if ha[i] == hb[j] {
				table[i*w+j] = at(i+1, j+1) + 1
			} else {
				table[i*w+j] = max(at(i+1, j), at(i, j+1))
			}
		}
	}
	ops := make([]Op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case ha[i] == hb[j]:
			ops = append(ops, Op{' ', a[i]})
			i++
			j++
		case at(i+1, j) >= at(i, j+1):
			ops = append(ops, Op{'-', a[i]})
			i++
		default:
			ops = append(ops, Op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, Op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, Op{'+', b[j]})
	}
	return ops
}

// Replace is the script that removes every line of a and then adds every line
// of b. It is a correct diff of any two texts, for when LCS would cost too
// much.
func Replace(a, b []string) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, Op{'-', l})
	}
	for _, l := range b {
		ops = append(ops, Op{'+', l})
	}
	return ops
}
