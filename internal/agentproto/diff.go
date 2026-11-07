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
		b.WriteString(colourDiffLine(l))
		b.WriteString("\n")
	}
	if more := len(lines) - len(shown); more > 0 {
		b.WriteString(sgrDim)
		fmt.Fprintf(&b, "(%d more diff lines)", more)
		b.WriteString(sgrReset)
		b.WriteString("\n")
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

// unifiedHunks is the diff of a and b as unified hunks with ctx lines of
// context. Two texts too large to compare in bounded time are shown as all
// removed and all added, which is still a correct diff.
func unifiedHunks(a, b []string, ctx int) []string {
	var out []string
	for _, h := range linediff.Hunks(linediff.Bounded(a, b, maxLineDiffOps), ctx) {
		out = append(out, h.Header())
		for _, o := range h.Ops {
			out = append(out, string(o.Op)+o.Line)
		}
	}
	return out
}
