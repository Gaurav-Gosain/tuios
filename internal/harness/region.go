package harness

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Regions: the part of what a pane shows that a rule reads.
//
// Most rules read the whole tail, which is right for a prompt that says what it
// is in words. Others need less. What proves an agent is at rest is the shape
// of the screen, an empty input box waiting at the bottom, and a rule reading
// the whole tail cannot tell a prompt character inside that box from the same
// character in the transcript above it. An answered prompt that is still in
// the tail keeps a whole-tail rule matching after the question is gone, and a
// rule reading only what sits under the last horizontal rule does not. So a
// rule can name the part it reads, and a screen without that part gives it
// nothing to match.
//
// The names follow herdr's manifests where the meaning is the same, so a rule
// ported from there keeps its region. Screen regions:
//
//	tail (or whole_recent, or empty)   the whole tail the manifest reads
//	bottom_non_empty_lines(N)          the last N lines of the tail
//	prompt_box (or prompt_box_body)    between the last two border lines
//	above_prompt_box                   everything above that box
//	last_non_empty_above_prompt_box    the one line just above the box
//	after_last_horizontal_rule         everything under the last border line
//
// Title rules read osc_title, the pane's window title, unless they name
// osc_progress, the last OSC 9;4 progress report written as "4;<state>" for
// the states that carry no percentage and "4;<state>;<percent>" for the ones
// that do. Notify rules read the notification and take no region.

// Screen region names, as they are stored after loading.
const (
	RegionTail                   = "tail"
	RegionPromptBox              = "prompt_box"
	RegionAbovePromptBox         = "above_prompt_box"
	RegionLastAbovePromptBox     = "last_non_empty_above_prompt_box"
	RegionAfterLastRule          = "after_last_horizontal_rule"
	RegionOSCTitle               = "osc_title"
	RegionOSCProgress            = "osc_progress"
	regionBottomPrefix           = "bottom_non_empty_lines("
	regionBottomSuffix           = ")"
	maxRegionLines               = 200
	screenRegionNamesForMessages = "tail, bottom_non_empty_lines(N), prompt_box, above_prompt_box, last_non_empty_above_prompt_box or after_last_horizontal_rule"
)

// screenRegionAliases maps the names a manifest may write to the name stored.
// The whole tail is stored as "" so the common case needs no lookup.
var screenRegionAliases = map[string]string{
	"":                       "",
	RegionTail:               "",
	"whole_recent":           "",
	RegionPromptBox:          RegionPromptBox,
	"prompt_box_body":        RegionPromptBox,
	RegionAbovePromptBox:     RegionAbovePromptBox,
	RegionLastAbovePromptBox: RegionLastAbovePromptBox,
	RegionAfterLastRule:      RegionAfterLastRule,
}

// normalizeScreenRegion returns the stored form of a screen region name and,
// for bottom_non_empty_lines(N), the N it reads.
func normalizeScreenRegion(name string) (region string, bottom int, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if r, ok := screenRegionAliases[name]; ok {
		return r, 0, nil
	}
	if n, ok := parseBottomRegion(name); ok {
		return regionBottomPrefix + strconv.Itoa(n) + regionBottomSuffix, n, nil
	}
	if name == RegionOSCTitle || name == RegionOSCProgress {
		return "", 0, fmt.Errorf("region %q is read by a title rule, not a screen rule", name)
	}
	return "", 0, fmt.Errorf("unknown region %q (%s)", name, screenRegionNamesForMessages)
}

// parseBottomRegion reads N out of bottom_non_empty_lines(N). N is a plain
// decimal between 1 and maxRegionLines.
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

// normalizeTitleRegion returns the stored form of a title rule's region: "" for
// the title, or osc_progress.
func normalizeTitleRegion(name string) (string, error) {
	switch name = strings.ToLower(strings.TrimSpace(name)); name {
	case "", RegionOSCTitle:
		return "", nil
	case RegionOSCProgress:
		return RegionOSCProgress, nil
	}
	return "", fmt.Errorf("region %q: a title rule reads osc_title or osc_progress", name)
}

// boxCorners are the runes that may open a border line before its dashes: the
// corners and tees a TUI frames an input box with.
const boxCorners = "╭╰┌└├┏┗┣"

// isBoxBorder reports whether a line is a horizontal rule: a run of at least
// three box-drawing dashes, optionally opened by a corner, with anything after
// the run. Claude Code draws its prompt between two bare dash rules, Gemini CLI
// inside a rounded box, and both pass. It is the one definition of a border
// every region uses.
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
// after_last_horizontal_rule on a screen with no rule is the whole tail, as it
// is in herdr: with no rule on screen, nothing marks older output as settled.
func regionLines(tail []string, region string) []string {
	lo, hi, ok := regionBounds(tail, region)
	if !ok {
		return nil
	}
	return tail[lo:hi]
}

// regionBounds is regionLines as indices into tail, so the same span can be
// cut from a lowercased copy of the tail. ok is false for a region that is not
// on the screen.
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
	case RegionAfterLastRule:
		for i := len(tail) - 1; i >= 0; i-- {
			if isBoxBorder(tail[i]) {
				return i + 1, len(tail), true
			}
		}
		return 0, len(tail), true
	}
	if n, ok := parseBottomRegion(region); ok {
		return max(len(tail)-n, 0), len(tail), true
	}
	return 0, 0, false
}

// regionText is the joined and, when asked, lowercased text of each region,
// built once per scan and only for the regions a manifest's rules read. Each
// line is lowercased at most once per scan however many regions share it, and
// only for a rule that has a substring to look for: lowering a screen of box
// drawing costs more than most patterns.
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

// get returns the plain and folded haystack for a region, and false when the
// region is empty on this screen. The folded haystack is the plain one when
// the manifest does not fold case or needFolded is false.
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
	if t.foldCase && needFolded && !e.hasFolded && e.hay != "" {
		if t.lowered == nil {
			t.lowered = make([]string, len(t.tail))
			for i, line := range t.tail {
				t.lowered[i] = foldLine(line)
			}
		}
		e.folded = strings.Join(t.lowered[e.lo:e.hi], "\n")
		e.hasFolded = true
	}
	return e.hay, e.folded, e.hay != ""
}

// foldLine lowercases a line, returning it as it is when nothing in it has a
// lower case, which is most of a TUI's frame: rules, borders and spinners.
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

// Dialogs: the box a harness draws its prompt in over its transcript.
//
// Crush draws its permission prompt as a bordered box in the middle of the
// pane, with the chat still showing on either side of it. The lines a rule
// reads are whole screen rows, so a peek of them shows the chat cut through
// by the box. dialogBoxes finds each box and cuts it out: its rows, each cut
// to the columns between its side borders.

// Corners and sides of a box, in the light, heavy, double and rounded sets.
const (
	dialogTopLeft     = "╭┌┏╔"
	dialogTopRight    = "╮┐┓╗"
	dialogBottomLeft  = "╰└┗╚"
	dialogBottomRight = "╯┘┛╝"
	dialogSides       = "│┃║"
)

// maxDialogLines bounds the rows a dialog may span, so a box drawn around a
// whole screen is not taken for a prompt.
const maxDialogLines = 60

// cells splits a line into terminal cells: a wide rune takes its cell and
// leaves the next one empty, so a column in one line is the same column in
// the next.
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

// cellAt is the rune at column col of a line split into cells, 0 when the
// line is shorter.
func cellAt(c []rune, col int) rune {
	if col < 0 || col >= len(c) {
		return 0
	}
	return c[col]
}

// dialogBox is one bordered box on the screen: the rows of its borders, the
// columns of its sides, and its inside as dialogInside cuts it.
type dialogBox struct {
	top, bottom, left, right int
	inside                   []string
}

// within reports whether box b lies wholly inside box o.
func (b dialogBox) within(o dialogBox) bool {
	return b.top > o.top && b.bottom < o.bottom && b.left > o.left && b.right < o.right
}

// dialogBoxes finds every bordered box in lines: a top border whose corners
// stand over a side on every row down to a bottom border with corners in the
// same columns. Boxes are listed bottom first, since a live dialog is drawn
// over the transcript and so below what scrolled past.
func dialogBoxes(lines []string) []dialogBox {
	grid := make([][]rune, len(lines))
	for i, l := range lines {
		grid[i] = cells(l)
	}
	var out []dialogBox
	for top := len(grid) - 1; top >= 0; top-- {
		for left, r := range grid[top] {
			if !strings.ContainsRune(dialogTopLeft, r) {
				continue
			}
			right := left + 1
			for right < len(grid[top]) && !strings.ContainsRune(dialogTopRight, grid[top][right]) {
				right++
			}
			if right >= len(grid[top]) {
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

// dialogsHolding is the boxes of lines whose inside holds every one of
// strs, innermost only: a box around another box that holds them is left
// out, since the inner one is the dialog. Bottom first.
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

// ruleDialog is the one dialog a show = "dialog" rule reads on lines: the
// innermost box that holds every all[] string of the rule. ok is false when
// no box holds them, or when more than one does: a box quoted in the
// transcript, or drawn by something else, must never be taken for the live
// dialog, and two candidates cannot be told apart.
func ruleDialog(m *Manifest, rl *ScreenRule, lines []string) (dialogBox, bool) {
	boxes := dialogsHolding(lines, rl.All, m.Screen.FoldCase)
	if len(boxes) != 1 {
		return dialogBox{}, false
	}
	return boxes[0], true
}

// dialogInside cuts each row to the columns between left and right, trims
// the edges, and keeps one blank row of each run.
func dialogInside(rows [][]rune, left, right int) []string {
	var out []string
	blank := true // drops the blank rows at the top
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
