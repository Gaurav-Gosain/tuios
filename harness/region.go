package harness

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Screen region names, as they are stored after loading.
const (
	RegionTail                   = "tail"
	RegionPromptBox              = "prompt_box"
	RegionAbovePromptBox         = "above_prompt_box"
	RegionLastAbovePromptBox     = "last_non_empty_above_prompt_box"
	RegionFramedAbovePromptBox   = "framed_above_prompt_box"
	RegionAfterLastRule          = "after_last_horizontal_rule"
	RegionOSCTitle               = "osc_title"
	RegionOSCProgress            = "osc_progress"
	regionBottomPrefix           = "bottom_non_empty_lines("
	regionBottomSuffix           = ")"
	maxRegionLines               = 200
	screenRegionNamesForMessages = "tail, bottom_non_empty_lines(N), prompt_box, above_prompt_box, last_non_empty_above_prompt_box, framed_above_prompt_box or after_last_horizontal_rule"
)

var screenRegionAliases = map[string]string{
	"":                       "",
	RegionTail:               "",
	"whole_recent":           "",
	RegionPromptBox:          RegionPromptBox,
	"prompt_box_body":        RegionPromptBox,
	RegionAbovePromptBox:     RegionAbovePromptBox,
	RegionLastAbovePromptBox: RegionLastAbovePromptBox,
	RegionAfterLastRule:      RegionAfterLastRule,

	RegionFramedAbovePromptBox: RegionFramedAbovePromptBox,
}

func normalizeScreenRegion(name string) (region string, bottom int, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if r, ok := screenRegionAliases[name]; ok {
		return r, 0, nil
	}
	if n, ok := parseBottomRegion(name); ok {
		return regionBottomPrefix + strconv.Itoa(n) + regionBottomSuffix, n, nil
	}
	if name == RegionOSCTitle || name == RegionOSCProgress {
		return "", 0, errorf("region %q is read by a title rule, not a screen rule", name)
	}
	return "", 0, errorf("unknown region %q (%s)", name, screenRegionNamesForMessages)
}

func parseBottomRegion(name string) (int, bool) {
	inner, ok := strings.CutPrefix(name, regionBottomPrefix)
	if !ok {
		return 0, false
	}
	inner, ok = strings.CutSuffix(inner, regionBottomSuffix)
	if !ok || inner == "" || inner[0] == '0' || inner[0] == '+' {
		return 0, false
	}
	n, err := strconv.Atoi(inner)
	if err != nil || n < 1 || n > maxRegionLines {
		return 0, false
	}
	return n, true
}

func normalizeTitleRegion(name string) (string, error) {
	switch name = strings.ToLower(strings.TrimSpace(name)); name {
	case "", RegionOSCTitle:
		return "", nil
	case RegionOSCProgress:
		return RegionOSCProgress, nil
	}
	return "", errorf("region %q: a title rule reads osc_title or osc_progress", name)
}

const boxCorners = "╭╰┌└├┏┗┣"

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

func isTitledRule(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.ContainsRune(boxCorners, []rune(t)[0]) {
		return false
	}
	for _, r := range strings.TrimLeft(t, "─━") {
		switch {
		case unicode.IsSpace(r), r >= 0x2500 && r <= 0x259F,
			unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
			continue
		}
		return true
	}
	return false
}

func blankFrom(tail []string, i int) bool {
	for _, t := range tail[i:] {
		if strings.TrimSpace(t) != "" {
			return false
		}
	}
	return true
}

func promptBox(tail []string) (top, bottom int, ok bool) {
	bottom = -1
	for i, t := range slices.Backward(tail) {
		if !isBoxBorder(t) {
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

func regionLines(tail []string, region string) []string {
	lo, hi, ok := regionBounds(tail, region)
	if !ok {
		return nil
	}
	return tail[lo:hi]
}

func regionBounds(tail []string, region string) (lo, hi int, ok bool) {
	switch region {
	case "", RegionTail:
		return 0, len(tail), true
	case RegionPromptBox:
		top, bottom, ok := promptBox(tail)
		if !ok {
			return 0, 0, false
		}
		return top + 1, bottom, true
	case RegionAbovePromptBox:
		top, _, ok := promptBox(tail)
		if !ok {
			return 0, 0, false
		}
		return 0, top, true
	case RegionLastAbovePromptBox:
		top, _, ok := promptBox(tail)
		if !ok {
			return 0, 0, false
		}
		for i := top - 1; i >= 0; i-- {
			if strings.TrimSpace(tail[i]) != "" {
				return i, i + 1, true
			}
		}
		return 0, 0, false
	case RegionFramedAbovePromptBox:
		top, _, ok := promptBox(tail)
		if !ok {
			return 0, 0, false
		}
		for i := top - 1; i >= 0; i-- {
			if isBoxBorder(tail[i]) {
				return i + 1, top, true
			}
		}
		return 0, top, true
	case RegionAfterLastRule:
		end := len(tail)
		for i, t := range slices.Backward(tail) {
			if !isBoxBorder(t) {
				continue
			}
			if end == len(tail) && isTitledRule(t) && blankFrom(tail, i+1) {
				end = i
				continue
			}
			return i + 1, end, true
		}
		if end < len(tail) {
			return end, end, true
		}
		return 0, len(tail), true
	}
	if n, ok := parseBottomRegion(region); ok {
		return max(len(tail)-n, 0), len(tail), true
	}
	return 0, 0, false
}

type regionText struct {
	tail     []string
	lowered  []string
	foldCase bool
	cache    map[string]*regionEntry
}

type regionEntry struct {
	lo, hi      int
	hay, folded string
	hasFolded   bool
}

func newRegionText(tail []string, foldCase bool) *regionText {
	return &regionText{tail: tail, foldCase: foldCase}
}

func (t *regionText) get(region string, needFolded bool) (hay, folded string, ok bool) {
	if region == RegionTail {
		region = ""
	}
	e, hit := t.cache[region]
	if !hit {
		e = &regionEntry{}
		if lo, hi, ok := regionBounds(t.tail, region); ok {
			e.lo, e.hi = lo, hi
			e.hay = strings.Join(t.tail[lo:hi], "\n")
		}
		e.folded = e.hay
		if t.cache == nil {
			t.cache = make(map[string]*regionEntry, 2)
		}
		t.cache[region] = e
	}
	wrap := region == RegionAfterLastRule
	if needFolded && !e.hasFolded && e.hay != "" && (t.foldCase || wrap) {
		lines := t.tail[e.lo:e.hi]
		if t.foldCase {
			if t.lowered == nil {
				t.lowered = make([]string, len(t.tail))
				for i, line := range t.tail {
					t.lowered[i] = foldLine(line)
				}
			}
			lines = t.lowered[e.lo:e.hi]
		}
		if wrap {
			e.folded = substringText(lines)
		} else {
			e.folded = strings.Join(lines, "\n")
		}
		e.hasFolded = true
	}
	return e.hay, e.folded, e.hay != ""
}

func substringText(lines []string) string {
	var b strings.Builder
	n := 1
	for _, line := range lines {
		n += 2*len(line) + 2
	}
	b.Grow(n)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	b.WriteByte(0)
	first := true
	for _, line := range lines {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteString(line)
		first = false
	}
	return b.String()
}

func foldLine(s string) string {
	for _, r := range s {
		if r < utf8.RuneSelf {
			if 'A' <= r && r <= 'Z' {
				return strings.ToLower(s)
			}
			continue
		}
		if unicode.IsUpper(r) || unicode.IsTitle(r) {
			return strings.ToLower(s)
		}
	}
	return s
}

const (
	dialogTopLeft     = "╭┌┏╔"
	dialogTopRight    = "╮┐┓╗"
	dialogBottomLeft  = "╰└┗╚"
	dialogBottomRight = "╯┘┛╝"
	dialogSides       = "│┃║"
)

const maxDialogLines = 60

func cells(line string) []rune {
	out := make([]rune, 0, len(line))
	for _, r := range line {
		out = append(out, r)
		if r >= 0x1100 && ansi.StringWidth(string(r)) == 2 {
			out = append(out, 0)
		}
	}
	return out
}

func cellAt(c []rune, col int) rune {
	if col < 0 || col >= len(c) {
		return 0
	}
	return c[col]
}

type dialogBox struct {
	top, bottom, left, right int
	inside                   []string
}

func (b dialogBox) within(o dialogBox) bool {
	return b.top > o.top && b.bottom < o.bottom && b.left > o.left && b.right < o.right
}

func dialogBoxes(lines []string) []dialogBox {
	grid := make([][]rune, len(lines))
	for i, l := range lines {
		grid[i] = cells(l)
	}
	var out []dialogBox
	for top, g := range slices.Backward(grid) {
		for left, r := range g {
			if !strings.ContainsRune(dialogTopLeft, r) {
				continue
			}
			right := left + 1
			for right < len(g) && !strings.ContainsRune(dialogTopRight, g[right]) {
				right++
			}
			if right >= len(g) {
				continue
			}
			for row := top + 1; row < len(grid) && row-top < maxDialogLines; row++ {
				l, rr := cellAt(grid[row], left), cellAt(grid[row], right)
				if strings.ContainsRune(dialogBottomLeft, l) && strings.ContainsRune(dialogBottomRight, rr) {
					out = append(out, dialogBox{top: top, bottom: row, left: left, right: right,
						inside: dialogInside(grid[top+1:row], left, right)})
					break
				}
				if !strings.ContainsRune(dialogSides, l) || !strings.ContainsRune(dialogSides, rr) {
					break
				}
			}
		}
	}
	return out
}

func dialogsHolding(lines []string, strs []string, foldCase bool) []dialogBox {
	var hits []dialogBox
	for _, b := range dialogBoxes(lines) {
		hay := strings.Join(b.inside, "\n")
		if foldCase {
			hay = strings.ToLower(hay)
		}
		ok := true
		for _, s := range strs {
			if s != "" && !strings.Contains(hay, s) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, b)
		}
	}
	var out []dialogBox
	for i, b := range hits {
		outer := false
		for j, o := range hits {
			if i != j && o.within(b) {
				outer = true
				break
			}
		}
		if !outer {
			out = append(out, b)
		}
	}
	return out
}

func ruleDialog(m *Manifest, rl *ScreenRule, lines []string) (dialogBox, bool) {
	boxes := dialogsHolding(lines, rl.All, m.Screen.FoldCase)
	if len(boxes) != 1 {
		return dialogBox{}, false
	}
	return boxes[0], true
}

func dialogInside(rows [][]rune, left, right int) []string {
	var out []string
	blank := true
	for _, c := range rows {
		var b strings.Builder
		for col := left + 1; col < right && col < len(c); col++ {
			if c[col] != 0 {
				b.WriteRune(c[col])
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if strings.TrimSpace(line) == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	if n := len(out); n > 0 && out[n-1] == "" {
		out = out[:n-1]
	}
	return out
}
