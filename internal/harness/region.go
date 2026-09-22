package harness

import "strings"

// Screen regions: the part of a pane's tail a rule reads.
//
// Most rules read the whole tail, which is right for a prompt that says what it
// is in words. An idle rule is different. What proves an agent is at rest is
// the shape of the screen, an empty input box waiting at the bottom, and a rule
// reading the whole tail cannot tell a prompt character inside that box from
// the same character in the transcript above it. So a rule can name the box
// and read only what is inside it, and a screen with no box gives it nothing
// to match.

// boxCorners are the runes that may open a border line before its dashes: the
// corners and tees a TUI frames an input box with.
const boxCorners = "╭╰┌└├┏┗┣"

// isBoxBorder reports whether a line is the top or bottom edge of an input box:
// a run of at least three box-drawing dashes, optionally opened by a corner,
// with anything after the run. Claude Code draws its prompt between two bare
// dash rules, Gemini CLI inside a rounded box, and both pass.
func isBoxBorder(line string) bool {
	t := strings.TrimLeft(strings.TrimSpace(line), boxCorners)
	n := 0
	for _, r := range t {
		if r != '─' && r != '━' {
			break
		}
		n++
	}
	return n >= 3
}

// promptBox finds the input box at the bottom of tail: the lines between the
// last two border lines. ok is false when the tail holds fewer than two.
func promptBox(tail []string) (top, bottom int, ok bool) {
	bottom = -1
	for i := len(tail) - 1; i >= 0; i-- {
		if !isBoxBorder(tail[i]) {
			continue
		}
		if bottom < 0 {
			bottom = i
			continue
		}
		return i, bottom, true
	}
	return -1, -1, false
}

// regionLines returns the lines of tail a region covers. A box region on a
// screen with no box is nil, so a rule reading it matches nothing.
func regionLines(tail []string, region string) []string {
	switch region {
	case RegionPromptBox:
		top, bottom, ok := promptBox(tail)
		if !ok {
			return nil
		}
		return tail[top+1 : bottom]
	case RegionAbovePromptBox:
		top, _, ok := promptBox(tail)
		if !ok {
			return nil
		}
		return tail[:top]
	default:
		return tail
	}
}

// regionText is the joined and, when asked, lowercased text of each region,
// built once per scan and only for the regions a manifest's rules read.
type regionText struct {
	tail     []string
	foldCase bool
	cache    map[string][2]string
}

func newRegionText(tail []string, foldCase bool) *regionText {
	return &regionText{tail: tail, foldCase: foldCase}
}

// get returns the plain and folded haystack for a region, and false when the
// region is empty on this screen.
func (t *regionText) get(region string) (hay, folded string, ok bool) {
	if region == RegionTail {
		region = ""
	}
	if c, hit := t.cache[region]; hit {
		return c[0], c[1], c[0] != ""
	}
	hay = strings.Join(regionLines(t.tail, region), "\n")
	folded = hay
	if t.foldCase {
		folded = strings.ToLower(hay)
	}
	if t.cache == nil {
		t.cache = make(map[string][2]string, 2)
	}
	t.cache[region] = [2]string{hay, folded}
	return hay, folded, hay != ""
}
