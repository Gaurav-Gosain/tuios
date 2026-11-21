package overlay

import (
	"fmt"
	"sync"
	"sync/atomic"

	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Hint is one key/label pair shown in a panel footer. Priority says what a
// strip too narrow for every hint gives up first (see fitHints).
type Hint struct {
	Key      string
	Label    string
	Priority HintPriority
}

// Panel is a borderless floating panel: a solid surface fill with an inset
// accent title chip, an optional tab row, a body, and a muted footer of key
// hints. Width is the inner content width; the rendered block is Width+4 cells
// wide (a two-cell pad on each side).
type Panel struct {
	// Title is the chip's whole content. There is no icon in front of it: an
	// icon per panel drifts into a mix of Nerd Font icons on some panels and
	// none on most, which reads as inconsistent and draws as boxes without a
	// Nerd Font.
	Title     string
	Width     int
	Tabs      []string
	ActiveTab int
	Body      string // pre-styled, multi-line; each line is surface-filled
	Hints     []Hint
}

// DefaultPanelPadding is the surface cells padding each side of a panel's
// content, and MaxPanelPadding is as wide as it is allowed to get: past this
// the padding is wider than the narrowest panel the family will draw, so the
// frame would be mostly margin.
const (
	DefaultPanelPadding = 2
	MaxPanelPadding     = 6
)

var panelPadding atomic.Int64

// SetPanelPadding sets the cells of surface padding each side of a panel's
// content. Out-of-range and zero take the default, so a config that has never
// mentioned it draws what it always drew.
func SetPanelPadding(cells int) {
	if cells <= 0 || cells > MaxPanelPadding {
		cells = DefaultPanelPadding
	}
	panelPadding.Store(int64(cells))
}

func sidePad() int {
	if v := panelPadding.Load(); v > 0 {
		return int(v)
	}
	return DefaultPanelPadding
}

// MinPanelWidth is the narrowest inner content width a panel is asked to lay
// itself out at. Below this the rows have nowhere to put a marker and a label,
// so a screen this narrow gets a panel as wide as the screen and nothing more.
const MinPanelWidth = 12

// FitWidth returns the inner content width for a panel that would prefer
// preferred columns on a screen screenW columns wide. A panel is Width plus
// twice its padding cells across, so anything wider than the screen minus that
// padding draws
// outside the screen with its right-hand side simply missing. Callers pass the
// width they want and get the width they can have.
func FitWidth(preferred, screenW int) int {
	if screenW <= 0 {
		return preferred
	}
	avail := screenW - 2*sidePad()
	if avail < preferred {
		preferred = avail
	}
	return max(preferred, 1)
}

// TabRowCount reports how many rows the tab strip of a panel of the given inner
// width needs. Hosts use it to budget the body's row count against the screen
// height before they build the body. The strip scrolls rather than wraps, so
// the answer is one row for as many tabs as a panel cares to carry.
func TabRowCount(tabs []string, _ int) int {
	if len(tabs) == 0 {
		return 0
	}
	return 1
}

// HintRowCount reports how many rows the footer hint strip of a panel of the
// given inner width needs. Hosts use it to budget the body's row count against
// the screen height before they build the body. The strip shortens in tiers
// rather than wrapping (see fitHints), so it is one row for any hints at all.
func HintRowCount(hints []Hint, _ int) int {
	if len(hints) == 0 {
		return 0
	}
	return 1
}

func hintWidth(h Hint) int {
	w := lipgloss.Width(hintKey(h.Key))
	if h.Label != "" {
		w += 1 + lipgloss.Width(h.Label)
	}
	return w
}

const tabGap = 1

const tabArrowWidth = 2

type tabRun struct {
	First, Count        int
	MoreLeft, MoreRight bool
	Scrolls             bool
	Inner               int
}

func tabWidths(tabs []string, active int) []int {
	w := make([]int, len(tabs))
	for i, name := range tabs {
		w[i] = lipgloss.Width(name)
		if i == active {
			w[i] += 2
		}
	}
	return w
}

func tabsSpan(widths []int, from, to int) int {
	w := 0
	for i := from; i < to; i++ {
		if i > from {
			w += tabGap
		}
		w += widths[i]
	}
	return w
}

func tabsFitting(widths []int, first, width int) int {
	n := 0
	for i := first; i < len(widths); i++ {
		if tabsSpan(widths, first, i+1) > width {
			break
		}
		n++
	}
	return n
}

func planTabRun(tabs []string, active, width int) tabRun {
	widths := tabWidths(tabs, active)
	if tabsSpan(widths, 0, len(widths)) <= width {
		return tabRun{Count: len(tabs)}
	}

	inner := width - 2*tabArrowWidth
	if inner < 1 {
		return tabRun{First: active, Count: 1}
	}
	first := 0
	for first < active && first+tabsFitting(widths, first, inner) <= active {
		first++
	}
	count := tabsFitting(widths, first, inner)
	if count == 0 {
		first, count = active, 1
	}
	return tabRun{
		First: first, Count: count,
		MoreLeft: first > 0, MoreRight: first+count < len(tabs),
		Scrolls: true, Inner: inner,
	}
}

func tabArrows() (string, string) { return ArrowLeft(), ArrowRight() }

func tabsRow(tabs []string, active int, bg color.Color, pal Palette, originX, originY, width int) (string, []Rect, Rect, Rect) {
	pill := func(name string, isActive bool) string {
		if isActive {
			return Chip(name, pal.Accent, pal.PillFg)
		}
		return Style(bg).Foreground(pal.FgDim).Render(name)
	}

	run := planTabRun(tabs, active, width)
	arrowStyle := Style(bg).Foreground(pal.FgDim)
	left, right := tabArrows()

	var b strings.Builder
	rects := make([]Rect, len(tabs))
	x := originX
	var prev, next Rect

	gutter := func(glyph string, live bool) Rect {
		if !run.Scrolls {
			return Rect{}
		}
		r := Rect{}
		if live {
			b.WriteString(arrowStyle.Render(glyph))
			b.WriteString(Style(bg).Render(" "))
			r = Rect{X0: x, Y0: originY, X1: x + tabArrowWidth, Y1: originY + 1}
		} else {
			b.WriteString(Style(bg).Render(strings.Repeat(" ", tabArrowWidth)))
		}
		x += tabArrowWidth
		return r
	}

	prev = gutter(left, run.MoreLeft)

	drawn := 0
	for i := run.First; i < run.First+run.Count && i < len(tabs); i++ {
		hitX := x
		if i > run.First {
			b.WriteString(Style(bg).Render(strings.Repeat(" ", tabGap)))
			x, drawn = x+tabGap, drawn+tabGap
		}
		span := width
		if run.Scrolls {
			span = run.Inner
		}
		name := tabs[i]
		p := pill(name, i == active)
		if lipgloss.Width(p) > span {
			p = pill(Truncate(name, max(span-2, 1)), i == active)
		}
		w := lipgloss.Width(p)
		b.WriteString(p)
		rects[i] = Rect{X0: hitX, Y0: originY, X1: x + w, Y1: originY + 1}
		x, drawn = x+w, drawn+w
	}
	if run.Scrolls && drawn < run.Inner {
		b.WriteString(Style(bg).Render(strings.Repeat(" ", run.Inner-drawn)))
		x += run.Inner - drawn
	}

	next = gutter(right, run.MoreRight)
	return b.String(), rects, prev, next
}

func footerRows(hints []Hint, bg color.Color, pal Palette, width int) []string {
	key := footerKey(hints, bg, pal, width)
	footerMemo.Lock()
	strip, ok := footerMemo.m[key]
	footerMemo.Unlock()
	if !ok {
		strip, _ = renderHints(fitHints(hints, width, footerSep), footerSep, bg, pal)
		footerMemo.Lock()
		if footerMemo.m == nil || len(footerMemo.m) >= footerMemoMax {
			footerMemo.m = make(map[string]string, footerMemoMax)
		}
		footerMemo.m[key] = strip
		footerMemo.Unlock()
	}
	return []string{strip}
}

var footerMemo struct {
	sync.Mutex
	m map[string]string
}

const footerMemoMax = 64

func footerKey(hints []Hint, bg color.Color, pal Palette, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%d|%t|%s|%s|%T%v|%T%v|%T%v|", width, CurrentDepth(), UseASCII(), Ellipsis(), EnterKey(),
		bg, bg, pal.AccentBright, pal.AccentBright, pal.FgDim, pal.FgDim)
	for _, h := range hints {
		b.WriteString(h.Key)
		b.WriteByte(0)
		b.WriteString(h.Label)
		b.WriteByte(0)
	}
	return b.String()
}

func renderHints(f fittedHints, sep int, bg color.Color, pal Palette) (string, int) {
	keyStyle := Style(bg).Foreground(Readable(pal.AccentBright, bg)).Bold(true)
	labelStyle := Style(bg).Foreground(pal.FgDim)
	gap := labelStyle.Render(strings.Repeat(" ", sep))
	var b strings.Builder
	for i, h := range f.Hints {
		if i > 0 {
			b.WriteString(gap)
		}
		b.WriteString(keyStyle.Render(hintKey(h.Key)))
		if h.Label != "" {
			b.WriteString(labelStyle.Render(" " + h.Label))
		}
	}
	if f.Truncated {
		if len(f.Hints) > 0 {
			b.WriteString(gap)
		}
		b.WriteString(labelStyle.Render(Ellipsis()))
	}
	return b.String(), hintsWidth(f.Hints, sep, f.Truncated)
}

// Render assembles the panel and returns the rendered string plus the geometry
// of its interactive regions in panel-relative coordinates.
func (p Panel) Render(pal Palette) (string, Geometry) {
	bg := pal.Surface
	totalW := p.Width + 2*sidePad()
	pad := Style(bg).Render(strings.Repeat(" ", sidePad()))
	blank := Style(bg).Render(strings.Repeat(" ", totalW))

	pads := map[int]string{}
	fill := func(s string, width int) string {
		w := lipgloss.Width(s)
		if w > width {
			s = ansi.Truncate(s, width, "")
			w = lipgloss.Width(s)
		}
		if w >= width {
			return s
		}
		p, ok := pads[width-w]
		if !ok {
			p = Style(bg).Render(strings.Repeat(" ", width-w))
			pads[width-w] = p
		}
		return s + p
	}
	line := func(content string) string {
		return fill(pad+content, totalW)
	}
	top, bottom := blank, blank

	if pal.Framed && sidePad() >= 1 {
		tl, tr, bl, br, h, v := panelFrame(pal)
		edge := Style(bg).Foreground(pal.frameInk(pal.Edge))
		side := edge.Render(v)
		inner := totalW - 2
		innerPad := Style(bg).Render(strings.Repeat(" ", sidePad()-1))
		line = func(content string) string {
			return side + fill(innerPad+content, inner) + side
		}
		blank = line("")
		top = edge.Render(tl + strings.Repeat(h, inner) + tr)
		bottom = edge.Render(bl + strings.Repeat(h, inner) + br)
	}

	var lines []string
	geo := Geometry{Width: totalW, InnerWidth: p.Width, BodyX: sidePad()}

	lines = append(lines, top)

	chip := Chip(Truncate(p.Title, max(p.Width-2, 1)), pal.Accent, pal.PillFg)
	lines = append(lines, line(chip))
	geo.TitleBar = Rect{X0: 0, Y0: len(lines) - 1, X1: totalW, Y1: len(lines)}
	lines = append(lines, blank)

	if len(p.Tabs) > 0 {
		row, rects, prev, next := tabsRow(p.Tabs, p.ActiveTab, bg, pal, sidePad(), len(lines), p.Width)
		lines = append(lines, line(row))
		geo.Tabs, geo.TabPrev, geo.TabNext = rects, prev, next
		lines = append(lines, line(Rule(p.Width, bg, pal)))
		lines = append(lines, blank)
	}

	geo.BodyY = len(lines)
	for bodyLine := range strings.SplitSeq(p.Body, "\n") {
		lines = append(lines, line(bodyLine))
	}

	if len(p.Hints) > 0 {
		lines = append(lines, blank)
		lines = append(lines, line(Rule(p.Width, bg, pal)))
		for _, r := range footerRows(p.Hints, bg, pal, p.Width) {
			lines = append(lines, line(r))
		}
	}
	lines = append(lines, bottom)

	geo.Height = len(lines)
	return strings.Join(lines, "\n"), geo
}

// FrameBlock gives a block drawn outside Panel the frame a Panel draws when the
// palette asks for one (Palette.Framed): rows is the block, each totalW cells
// wide with at least one blank cell of padding on every side, and the frame
// takes that outermost cell, so the block keeps its size. On a palette that
// needs no frame the rows come back as they are.
func FrameBlock(rows []string, totalW int, bg color.Color, pal Palette) []string {
	if !pal.Framed || len(rows) < 2 || totalW < 2 {
		return rows
	}
	tl, tr, bl, br, h, v := panelFrame(pal)
	edge := Style(bg).Foreground(pal.frameInk(pal.Edge))
	side := edge.Render(v)
	out := make([]string, len(rows))
	out[0] = edge.Render(tl + strings.Repeat(h, totalW-2) + tr)
	for i := 1; i < len(rows)-1; i++ {
		out[i] = side + Fill(ansi.Cut(rows[i], 1, totalW-1), totalW-2, bg) + side
	}
	out[len(rows)-1] = edge.Render(bl + strings.Repeat(h, totalW-2) + br)
	return out
}
