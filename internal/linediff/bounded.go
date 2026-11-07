package linediff

import "strconv"

// Bounded is the edit script from a to b. The lines a and b share at the
// start and the end are kept without the table. The rest is compared by LCS
// when its table has at most maxCells cells, and is Replace otherwise, which
// is still a correct diff.
func Bounded(a, b []string, maxCells int) []Op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]Op, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		ops = append(ops, Op{Op: ' ', Line: l})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if (len(ma)+1)*(len(mb)+1) > maxCells {
		ops = append(ops, Replace(ma, mb)...)
	} else {
		ops = append(ops, LCS(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, Op{Op: ' ', Line: l})
	}
	return ops
}

// Hunk is one hunk of an edit script: the line it starts on in each text,
// counted from 1, how many lines of each text it covers, and its lines.
type Hunk struct {
	AStart, ACount, BStart, BCount int
	Ops                            []Op
}

// Header is the hunk's @@ line. A side with no lines starts at the line
// before it, as git writes it.
func (h Hunk) Header() string {
	a, b := h.AStart, h.BStart
	if h.ACount == 0 {
		a--
	}
	if h.BCount == 0 {
		b--
	}
	return "@@ -" + strconv.Itoa(a) + "," + strconv.Itoa(h.ACount) +
		" +" + strconv.Itoa(b) + "," + strconv.Itoa(h.BCount) + " @@"
}

// Hunks splits an edit script into hunks with context lines of context
// around every change, as git does: two changes with at most 2*context kept
// lines between them share a hunk. It returns nil when nothing changed.
func Hunks(ops []Op, context int) []Hunk {
	show := make([]bool, len(ops))
	changed := false
	for i, op := range ops {
		if op.Op == ' ' {
			continue
		}
		changed = true
		for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
			show[k] = true
		}
	}
	if !changed {
		return nil
	}
	var out []Hunk
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
		h := Hunk{AStart: aLine, BStart: bLine}
		start := i
		for i < len(ops) && show[i] {
			if ops[i].Op != '+' {
				h.ACount++
				aLine++
			}
			if ops[i].Op != '-' {
				h.BCount++
				bLine++
			}
			i++
		}
		h.Ops = ops[start:i]
		out = append(out, h)
	}
	return out
}
