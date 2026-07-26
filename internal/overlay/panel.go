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
// height before they build the body.
func TabRowCount(tabs []string, width int) int {
	if len(tabs) == 0 {
		return 0
	}
	rows := 1
	curW := 0
	wrapW := tabWrapWidth(width)
	for _, name := range tabs {
		w := lipgloss.Width(name) + 2 // Padding(0, 1)
		if w > wrapW {
			w = wrapW
		}
		if curW > 0 && curW+w > wrapW {
			rows++
			curW = 0
		}
		curW += w
	}
	return rows
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

// tabWrapWidth is the width the tab strip lays out against, which is the inner
// width every other row uses. Letting the strip spill into the right-hand pad
// buys one more tab per row and costs the panel its symmetry: the first tab sits
// on the left pad while the last runs past the rule underneath it, which reads
// as the strip being shoved rightwards. A tab that no longer fits belongs on the
// next row, which the strip already wraps onto.
func tabWrapWidth(width int) int {
	return width
}

// glyphPrefix returns the glyph plus a trailing space, honoring ASCII mode.
func glyphPrefix(glyph string) string {
	if ASCII || glyph == "" {
		return ""
	}
	return glyph + " "
}

// tabsRows renders the section tab strip, wrapping onto further rows when the
// pills do not fit across one. The active tab is an accent pill, the rest muted.
// It also returns the panel-relative rect of each tab, given the strip's
// top-left origin (originX, originY).
func tabsRows(tabs []string, active int, bg color.Color, pal Palette, originX, originY, width int) ([]string, []Rect) {
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
	gap := Style(bg).Render(" ")

	wrapW := tabWrapWidth(width)
	var rows []string
	rects := make([]Rect, len(tabs))
	var cur []string
	curW, y := 0, originY

	flush := func() {
		if len(cur) == 0 {
			return
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cur...))
		cur = cur[:0]
		curW = 0
		y++
	}

	for i, name := range tabs {
		p := pill(name, i == active)
		w := lipgloss.Width(p)
		if w > wrapW {
			// A single pill wider than the strip: shorten the label rather than
			// let it hang off the edge.
			p = pill(Truncate(name, max(wrapW-2, 1)), i == active)
			w = lipgloss.Width(p)
		}
		// The separator belongs to the tab that follows it, so a row never ends
		// on a trailing space and the width test counts what will actually be
		// drawn.
		lead := 0
		if curW > 0 {
			lead = 1
		}
		if curW > 0 && curW+lead+w > wrapW {
			flush()
			lead = 0
		}
		// The hit area starts at the separator rather than at the label, so the
		// strip has no dead column between tabs: a click that lands in the gap
		// picks the tab it precedes.
		hitX := curW
		if lead > 0 {
			cur = append(cur, gap)
			curW += lead
		}
		rects[i] = Rect{X0: originX + hitX, Y0: y, X1: originX + curW + w, Y1: y + 1}
		cur = append(cur, p)
		curW += w
	}
	flush()
	return rows, rects
}

// footerRows renders the muted key-hint strip, wrapping onto further rows when
// the hints do not fit across one.
func footerRows(hints []Hint, bg color.Color, pal Palette, width int) []string {
	keyStyle := Style(bg).Foreground(pal.AccentBright).Bold(true)
	labelStyle := Style(bg).Foreground(pal.FgMute)
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
		tabRows, rects := tabsRows(p.Tabs, p.ActiveTab, bg, pal, sidePad, len(lines), p.Width)
		for _, r := range tabRows {
			lines = append(lines, line(r))
		}
		geo.Tabs = rects
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
