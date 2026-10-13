package agentproto

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/linediff"
)

// Diffs in the transcript. Codex sends a unified diff, which is shown as it is;
// ACP sends the old and new text, which is diffed here by lines, with three
// lines of context. Either way a line keeps its +, - or space, so the diff
// reads without colour, and at most maxDiffLines lines of one call are shown.

// diffKey identifies a set of diffs, so the same diff is not shown twice.
func diffKey(diffs []Diff) string {
	if len(diffs) == 0 {
		return ""
	}
	h := fnv.New64a()
	for _, d := range diffs {
		_, _ = h.Write([]byte(d.Path))
		_, _ = h.Write([]byte{0})
		if d.Old != nil {
			_, _ = h.Write([]byte(*d.Old))
		}
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(d.New))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(d.Unified))
		_, _ = h.Write([]byte{1})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// renderDiffs is the diffs as transcript lines, with a final line break.
func renderDiffs(diffs []Diff) string {
	var lines []string
	for _, d := range diffs {
		lines = append(lines, diffLines(d)...)
	}
	var b strings.Builder
	shown := lines
	if len(shown) > maxDiffLines {
		shown = shown[:maxDiffLines]
	}
	for _, l := range shown {
		b.WriteString(colourDiffLine(l) + "\n")
	}
	if more := len(lines) - len(shown); more > 0 {
		b.WriteString(sgrDim + fmt.Sprintf("(%d more diff lines)", more) + sgrReset + "\n")
	}
	return b.String()
}

// diffLines is one file's diff as plain lines, headers first.
func diffLines(d Diff) []string {
	path := oneLine(d.Path)
	if d.Unified != "" {
		body := strings.Split(strings.TrimRight(clean(d.Unified), "\n"), "\n")
		if len(body) > 0 && strings.HasPrefix(body[0], "--- ") {
			return body
		}
		return append([]string{"--- " + path, "+++ " + path}, body...)
	}
	oldText := ""
	oldName := path
	if d.Old == nil {
		oldName = "/dev/null"
	} else {
		oldText = clean(*d.Old)
	}
	out := []string{"--- " + oldName, "+++ " + path}
	return append(out, unifiedHunks(linediff.Split(oldText), linediff.Split(clean(d.New)), 3)...)
}

// colourDiffLine colours a diff line by its first character, which it keeps.
func colourDiffLine(l string) string {
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		return sgrBold + l + sgrReset
	case strings.HasPrefix(l, "@@"):
		return sgrCyan + l + sgrReset
	case strings.HasPrefix(l, "+"):
		return sgrGreen + l + sgrReset
	case strings.HasPrefix(l, "-"):
		return sgrRed + l + sgrReset
	}
	return l
}

// diffOp is one line of an edit script: ' ' kept, '-' removed, '+' added.
type diffOp = linediff.Op

// lineDiff is the edit script from a to b by longest common subsequence. Two
// texts too large to compare in bounded time are shown as all removed and all
// added, which is still a correct diff.
func lineDiff(a, b []string) []diffOp {
	// Common prefix and suffix are kept without the table.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{Op: ' ', Line: l})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma)*len(mb) > maxLineDiffOps {
		ops = append(ops, linediff.Replace(ma, mb)...)
	} else {
		ops = append(ops, linediff.LCS(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{Op: ' ', Line: l})
	}
	return ops
}

// unifiedHunks is the diff of a and b as unified hunks with ctx lines of
// context.
func unifiedHunks(a, b []string, ctx int) []string {
	ops := lineDiff(a, b)
	var out []string
	for start := 0; start < len(ops); {
		// Find the next change.
		first := start
		for first < len(ops) && ops[first].Op == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		// Extend the hunk while changes are within 2*ctx of each other.
		last := first
		for k := first; k < len(ops); k++ {
			if ops[k].Op != ' ' {
				last = k
				continue
			}
			if k-last > 2*ctx {
				break
			}
		}
		lo := max(first-ctx, start)
		hi := min(last+ctx+1, len(ops))
		// Line numbers at lo.
		oldLine, newLine := 1, 1
		for _, o := range ops[:lo] {
			if o.Op != '+' {
				oldLine++
			}
			if o.Op != '-' {
				newLine++
			}
		}
		oldCount, newCount := 0, 0
		var body []string
		for _, o := range ops[lo:hi] {
			if o.Op != '+' {
				oldCount++
			}
			if o.Op != '-' {
				newCount++
			}
			body = append(body, string(o.Op)+o.Line)
		}
		if oldCount == 0 {
			oldLine--
		}
		if newCount == 0 {
			newLine--
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldLine, oldCount, newLine, newCount))
		out = append(out, body...)
		start = hi
	}
	return out
}
