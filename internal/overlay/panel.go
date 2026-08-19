package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Hint is one key/label pair shown in a panel footer.
type Hint struct {
	Key   string
	Label string
}

// Panel is a borderless floating panel: a solid surface fill with an inset
// accent title chip, an optional tab row, a body, and a muted footer of key
// hints. Width is the inner content width; the rendered block is Width+4 cells
// wide (a two-cell pad on each side).
type Panel struct {
	Glyph     string // optional leading glyph for the title chip
	Title     string
	Width     int
	Tabs      []string
	ActiveTab int
	Body      string // pre-styled, multi-line; each line is surface-filled
	Hints     []Hint
}

// sidePad is the number of surface cells padding each side of the content.
const sidePad = 2

// MinPanelWidth is the narrowest inner content width a panel is asked to lay
// itself out at. Below this the rows have nowhere to put a marker and a label,
// so a screen this narrow gets a panel as wide as the screen and nothing more.
const MinPanelWidth = 12

// FitWidth returns the inner content width for a panel that would prefer
// preferred columns on a screen screenW columns wide. A panel is Width+2*sidePad
// cells across, so anything wider than the screen minus that padding draws
// outside the screen with its right-hand side simply missing. Callers pass the
// width they want and get the width they can have.
func FitWidth(preferred, screenW int) int {
	if screenW <= 0 {
		return preferred // size not known yet; the caller's preference stands
	}
	avail := screenW - 2*sidePad
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
// the screen height before they build the body.
func HintRowCount(hints []Hint, width int) int {
	if len(hints) == 0 {
		return 0
	}
	rows, curW := 1, 0
	for _, h := range hints {
		w := hintWidth(h)
		if curW > 0 && curW+3+w > width {
			rows++
			curW = 0
		}
		if curW > 0 {
			curW += 3
		}
		curW += w
	}
	return rows
}

// hintWidth is the rendered width of one hint: the key, a space, and the label.
func hintWidth(h Hint) int {
	return lipgloss.Width(h.Key) + 1 + lipgloss.Width(h.Label)
}

// tabGap is the single bg-colored column between two tabs.
const tabGap = 1

// tabArrowWidth is the span one overflow gutter holds: the arrow glyph and the
// column after it. Both gutters are held open for as long as the strip scrolls,
// whether or not there is anything that way, so an arrow appearing does not
// shift the tab under the pointer.
const tabArrowWidth = 2

// tabRun is the strip as this frame will draw it: the run of tabs that fits on
// one row, and whether there are more either side of it.
//
// The strip scrolls rather than wraps. A second row of tabs reads as a broken
// panel, and it breaks the row-indexed addressing the body underneath relies on:
// every body rect is derived from the row the tabs left free.
type tabRun struct {
	First, Count        int
	MoreLeft, MoreRight bool
	// Scrolls is set once the tabs stopped fitting: both gutters are then held
	// open and Inner is the fixed span the tabs are drawn into.
	Scrolls bool
	Inner   int
}

// tabWidths measures each tab as it will be drawn. Only the active tab is a
// padded pill, so the widths depend on which tab is active.
func tabWidths(tabs []string, active int) []int {
	w := make([]int, len(tabs))
	for i, name := range tabs {
		w[i] = lipgloss.Width(name)
		if i == active {
			w[i] += 2 // Padding(0, 1)
		}
	}
	return w
}

// tabsSpan is the width of widths[from:to) laid out with their gaps.
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

// tabsFitting is how many whole tabs from first fit in width cells. A tab drawn
// past the viewport it was measured into leaves a recorded rectangle over cells
// the panel then truncates away.
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

// planTabRun decides which tabs one row shows. The offset is derived from the
// active tab rather than remembered, which is what keeps the active tab in view
// however it was changed: the smallest offset that shows it is the answer to
// both a click and a keyboard switch, and there is no stale scroll state to go
// wrong between frames.
func planTabRun(tabs []string, active, width int) tabRun {
	widths := tabWidths(tabs, active)
	if tabsSpan(widths, 0, len(widths)) <= width {
		return tabRun{Count: len(tabs)}
	}

	inner := width - 2*tabArrowWidth
	if inner < 1 {
		// No room for the gutters and anything between them. The strip keeps the
		// active tab, shortened by the renderer to the width there is, because a
		// strip that says nothing is worse than one that says only where you are.
		return tabRun{First: active, Count: 1}
	}
	first := 0
	// Walk forward only while the active tab is off the right of the run, so a
	// tab that already fits from the left end does not scroll at all.
	for first < active && first+tabsFitting(widths, first, inner) <= active {
		first++
	}
	count := tabsFitting(widths, first, inner)
	if count == 0 {
		// Not even the active tab fits whole between the gutters. It is drawn
		// anyway, shortened to the viewport: the arrows are the only sign the
		// other sections exist at this width, so they stay.
		first, count = active, 1
	}
	return tabRun{
		First: first, Count: count,
		MoreLeft: first > 0, MoreRight: first+count < len(tabs),
		Scrolls: true, Inner: inner,
	}
}

// tabArrows returns the overflow glyphs, honoring ASCII mode.
func tabArrows() (string, string) {
	if UseASCII() {
		return "<", ">"
	}
	return "‹", "›"
}

// glyphPrefix returns the glyph plus a trailing space, honoring ASCII mode.
func glyphPrefix(glyph string) string {
	if UseASCII() || glyph == "" {
		return ""
	}
	return glyph + " "
}

// tabsRow renders the section tab strip on exactly one row: a left gutter, the
// run of tabs that fits, a right gutter. The active tab is an accent pill, the
// rest muted. It also returns the panel-relative rect of each drawn tab and of
// each live arrow, given the strip's top-left origin (originX, originY). A tab
// scrolled out of the run gets the zero rect, which hit-tests as nothing.
func tabsRow(tabs []string, active int, bg color.Color, pal Palette, originX, originY, width int) (string, []Rect, Rect, Rect) {
	// Only the active pill is padded. It carries a background, so the padding is
	// the gap between its colour and its label; on the others it is invisible
	// spacing, and paying two columns per tab for it is what pushed the strip
	// wider than the rule beneath it. The inactive tabs are separated by a single
	// space instead, which costs one column per gap rather than two.
	pill := func(name string, isActive bool) string {
		if isActive {
			return lipgloss.NewStyle().
				Background(pal.Accent).Foreground(pal.PillFg).
				Bold(true).Padding(0, 1).Render(name)
		}
		return Style(bg).Foreground(pal.FgDim).Render(name)
	}

	run := planTabRun(tabs, active, width)
	// FgDim, the colour the inactive labels already carry on this surface: the
	// arrow is furniture beside them, not quieter than them.
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
			b.WriteString(arrowStyle.Render(glyph) + Style(bg).Render(" "))
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
		// The separator belongs to the tab that follows it, so the run never ends
		// on a trailing space and the hit area has no dead column between tabs: a
		// click that lands in the gap picks the tab it precedes.
		hitX := x
		if i > run.First {
			b.WriteString(Style(bg).Render(strings.Repeat(" ", tabGap)))
			x, drawn = x+tabGap, drawn+tabGap
		}
		// A tab wider than the span it is drawn into is shortened rather than let
		// to hang off the edge, which on a scrolling strip means over the arrow.
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
	// A scrolling strip holds its viewport open, so the right-hand arrow stays
	// put as the tabs move under it.
	if run.Scrolls && drawn < run.Inner {
		b.WriteString(Style(bg).Render(strings.Repeat(" ", run.Inner-drawn)))
		x += run.Inner - drawn
	}

	next = gutter(right, run.MoreRight)
	return b.String(), rects, prev, next
}

// footerRows renders the muted key-hint strip, wrapping onto further rows when
// the hints do not fit across one.
func footerRows(hints []Hint, bg color.Color, pal Palette, width int) []string {
	keyStyle := Style(bg).Foreground(Readable(pal.AccentBright, bg)).Bold(true)
	// FgDim, not FgMute: the footer is the only place a panel says what its keys
	// do, and FgMute is a furniture token picked to disappear against the canvas.
	// On the panel's lighter Surface it measured 1.81:1 and did disappear.
	labelStyle := Style(bg).Foreground(pal.FgDim)
	sep := Style(bg).Render("   ")
	const sepW = 3

	var rows []string
	var cur []string
	curW := 0
	for _, h := range hints {
		part := keyStyle.Render(h.Key) + labelStyle.Render(" "+h.Label)
		w := lipgloss.Width(part)
		if curW > 0 && curW+sepW+w > width {
			rows = append(rows, strings.Join(cur, sep))
			cur = cur[:0]
			curW = 0
		}
		if curW > 0 {
			curW += sepW
		}
		cur = append(cur, part)
		curW += w
	}
	if len(cur) > 0 {
		rows = append(rows, strings.Join(cur, sep))
	}
	return rows
}

// Render assembles the panel and returns the rendered string plus the geometry
// of its interactive regions in panel-relative coordinates.
func (p Panel) Render(pal Palette) (string, Geometry) {
	bg := pal.Surface
	totalW := p.Width + 2*sidePad
	pad := Style(bg).Render(strings.Repeat(" ", sidePad))
	blank := Style(bg).Render(strings.Repeat(" ", totalW))

	// Every row is padded out to the panel width and, as a backstop, cut down to
	// it. A row that measures wider than the panel is a row drawn outside the
	// panel's own box, which on a narrow screen means outside the screen.
	line := func(content string) string {
		s := pad + content
		if lipgloss.Width(s) > totalW {
			s = ansi.Truncate(s, totalW, "")
		}
		return Fill(s, totalW, bg)
	}

	var lines []string
	geo := Geometry{Width: totalW, InnerWidth: p.Width, BodyX: sidePad}

	lines = append(lines, blank) // 0: top pad

	// 1: title chip. The whole row is a drag handle.
	chip := Chip(Truncate(glyphPrefix(p.Glyph)+p.Title, max(p.Width-2, 1)), pal.Accent, pal.PillFg)
	lines = append(lines, line(chip))
	geo.TitleBar = Rect{X0: 0, Y0: len(lines) - 1, X1: totalW, Y1: len(lines)}
	lines = append(lines, blank) // 2: blank

	if len(p.Tabs) > 0 {
		row, rects, prev, next := tabsRow(p.Tabs, p.ActiveTab, bg, pal, sidePad, len(lines), p.Width)
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
	lines = append(lines, blank) // bottom pad

	geo.Height = len(lines)
	return strings.Join(lines, "\n"), geo
}
