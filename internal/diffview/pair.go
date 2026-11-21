package diffview

// Pair is one row of the side by side layout: the index of the line on the
// left (old) side and on the right (new) side, -1 for a side with no line. A
// line both sides have is on both.
type Pair struct {
	Left, Right int
}

// Pairs lays a hunk's lines out side by side. kinds holds each line's kind,
// Context, Add or Delete. A run of removed lines followed by a run of added
// ones is read as the removed lines replaced, and the n-th removed line sits
// beside the n-th added one; what is left of the longer run sits beside
// nothing.
func Pairs(kinds []Kind) []Pair {
	out := make([]Pair, 0, len(kinds))
	for i := 0; i < len(kinds); {
		switch kinds[i] {
		case Delete:
			del := i
			for i < len(kinds) && kinds[i] == Delete {
				i++
			}
			add := i
			for i < len(kinds) && kinds[i] == Add {
				i++
			}
			nd, na := add-del, i-add
			for j := range max(nd, na) {
				p := Pair{Left: -1, Right: -1}
				if j < nd {
					p.Left = del + j
				}
				if j < na {
					p.Right = add + j
				}
				out = append(out, p)
			}
		case Add:
			out = append(out, Pair{Left: -1, Right: i})
			i++
		default:
			out = append(out, Pair{Left: i, Right: i})
			i++
		}
	}
	return out
}

// Range is a run of bytes in a line, empty when Start == End.
type Range struct {
	Start, End int
}

// Empty reports whether the range covers nothing.
func (r Range) Empty() bool { return r.End <= r.Start }

// Changed finds the part of a line that changed between old and new: what
// is left once the prefix and suffix they share are taken off, widened to
// whole words so a renamed identifier is marked as a whole. Both ranges are
// empty when the lines have too little in common for the mark to mean
// anything, which is when a line was rewritten rather than edited: the
// line's own ground already says it changed.
func Changed(old, new string) (Range, Range) {
	pre := 0
	for pre < len(old) && pre < len(new) && old[pre] == new[pre] {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(new)-pre && old[len(old)-1-suf] == new[len(new)-1-suf] {
		suf++
	}
	for pre > 0 && (splits(old, pre) || splits(new, pre)) {
		pre--
	}
	for suf > 0 && (splits(old, len(old)-suf) || splits(new, len(new)-suf)) {
		suf--
	}
	oe, ne := len(old)-suf, len(new)-suf
	indent := 0
	for indent < pre && (old[indent] == ' ' || old[indent] == '\t') {
		indent++
	}
	shorter := min(len(old), len(new)) - indent
	if shorter <= 0 || (pre-indent+suf)*3 < shorter {
		return Range{}, Range{}
	}
	return Range{pre, oe}, Range{pre, ne}
}

func splits(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return false
	}
	return continuation(s[i]) || wordByte(s[i-1]) && wordByte(s[i])
}

func wordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func continuation(b byte) bool { return b&0xC0 == 0x80 }
