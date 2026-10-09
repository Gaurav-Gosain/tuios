package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// reviewHighlight fills in the syntax colour (HL) and the changed part
// (Changed) of every line of the diff, for review-diff's highlight. It reads
// each hunk the way the TUI's review does (internal/app/review_look.go): the
// old side (context and removed lines) and the new side (context and added
// lines) are each tokenised as one text, so a comment that spans lines keeps
// its colour, and a context line takes its colour from the new side. A
// truncated or binary file has no lines to colour, and diffview leaves a text
// past its own byte limits plain.
func reviewHighlight(files []review.File) {
	for fi := range files {
		f := &files[fi]
		if f.Truncated || f.Binary {
			continue
		}
		oldPath := f.Path
		if f.OldPath != "" {
			oldPath = f.OldPath
		}
		for hi := range f.Hunks {
			lines := f.Hunks[hi].Lines
			kinds := make([]diffview.Kind, len(lines))
			for i, ln := range lines {
				switch ln.Op {
				case review.OpAdd:
					kinds[i] = diffview.Add
				case review.OpDelete:
					kinds[i] = diffview.Delete
				default:
					kinds[i] = diffview.Context
				}
			}
			for _, p := range diffview.Pairs(kinds) {
				if p.Left < 0 || p.Right < 0 || p.Left == p.Right {
					continue
				}
				o, n := diffview.Changed(lines[p.Left].Text, lines[p.Right].Text)
				if !o.Empty() {
					lines[p.Left].Changed = []int{o.Start, o.End}
				}
				if !n.Empty() {
					lines[p.Right].Changed = []int{n.Start, n.End}
				}
			}
			if !diffview.Enabled {
				continue
			}
			for _, side := range []struct {
				path string
				skip diffview.Kind
			}{{oldPath, diffview.Add}, {f.Path, diffview.Delete}} {
				var idx []int
				var text []string
				for i, ln := range lines {
					if kinds[i] != side.skip {
						idx = append(idx, i)
						text = append(text, ln.Text)
					}
				}
				for j, spans := range diffview.Highlight(side.path, text) {
					lines[idx[j]].HL = reviewSpans(spans)
				}
			}
		}
	}
}

// reviewSpans are spans as review-diff sends them, nil for a line that is
// plain all through.
func reviewSpans(spans []diffview.Span) [][3]int {
	var out [][3]int
	for _, s := range spans {
		if s.Class == diffview.Plain || s.End <= s.Start {
			continue
		}
		out = append(out, [3]int{s.Start, s.End, int(s.Class)})
	}
	return out
}
