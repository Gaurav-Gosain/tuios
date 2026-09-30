package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The which-key panel lays the prefix menu out in sections and columns. The
// leader's menu is about forty lines, and as one column it was taller than a
// laptop terminal, so the eye had to run down a list to find a key. In columns
// it is a dozen or so rows, read across.
//
// A section is kept whole in one column when that costs little height, which
// keeps each heading over all of its keys. When the screen is short, sections
// flow on into the next column and the heading is repeated at the top of it,
// so no column starts with keys nobody has named.

const (
	// whichKeyGap is the cells between two columns.
	whichKeyGap = 4
	// whichKeyKeyGap is the cells between a key and what it does.
	whichKeyKeyGap = 2
	// whichKeySplitCost is what one section split across two columns costs
	// against the panel's height, in rows (see layoutWhichKey).
	whichKeySplitCost = 3
	// whichKeyMinDesc is the fewest cells a description is cut to before the
	// panel gives up a column instead.
	whichKeyMinDesc = 10
)

// wkLine is one line of a which-key column.
type wkLine struct {
	header string             // a section heading, or "" for a key line or a blank
	bind   *config.Keybinding // the key line, nil for a heading or a blank
}

// wkColumn is one column: its lines and the widths its key and description
// columns take.
type wkColumn struct {
	lines        []wkLine
	keyW, descW  int
	headerW      int
	truncateDesc int // the width descriptions are cut to, 0 for none
}

func (c *wkColumn) width() int {
	w := c.headerW
	if c.keyW > 0 {
		d := c.descW
		if c.truncateDesc > 0 {
			d = min(d, c.truncateDesc)
		}
		w = max(w, c.keyW+whichKeyKeyGap+d)
	}
	return w
}

// wkDescWidth is a line's description as drawn: a submenu carries a leading +.
func wkDescWidth(b *config.Keybinding) int {
	w := lipgloss.Width(b.Description)
	if b.Submenu {
		w++
	}
	return w
}

// measure fills a column's widths from its lines.
func (c *wkColumn) measure() {
	c.keyW, c.descW, c.headerW = 0, 0, 0
	for _, l := range c.lines {
		switch {
		case l.bind != nil:
			c.keyW = max(c.keyW, lipgloss.Width(l.bind.Key))
			c.descW = max(c.descW, wkDescWidth(l.bind))
		case l.header != "":
			c.headerW = max(c.headerW, lipgloss.Width(l.header))
		}
	}
}

// wkBlock is a section as lines: its heading, if it has one, then its keys.
func wkBlock(g config.KeybindingGroup) []wkLine {
	lines := make([]wkLine, 0, len(g.Bindings)+1)
	if g.Title != "" {
		lines = append(lines, wkLine{header: g.Title})
	}
	for i := range g.Bindings {
		lines = append(lines, wkLine{bind: &g.Bindings[i]})
	}
	return lines
}

// packWhole lays the sections into columns of at most height lines each,
// keeping every section whole and in order, a blank line between two in one
// column. It reports false when that takes more than cols columns.
func packWhole(groups []config.KeybindingGroup, cols, height int) ([]wkColumn, bool) {
	var out []wkColumn
	var cur []wkLine
	for _, g := range groups {
		block := wkBlock(g)
		if len(block) > height {
			return nil, false
		}
		need := len(block)
		if len(cur) > 0 {
			need++
		}
		if len(cur)+need > height {
			out = append(out, wkColumn{lines: cur})
			cur = nil
		}
		if len(cur) > 0 {
			cur = append(cur, wkLine{})
		}
		cur = append(cur, block...)
	}
	if len(cur) > 0 {
		out = append(out, wkColumn{lines: cur})
	}
	return out, len(out) <= cols
}

// packFlow lays the sections into columns of at most height lines, letting a
// section run on into the next column under its heading repeated. A column
// never ends on a heading or a blank, and never starts with a blank.
func packFlow(groups []config.KeybindingGroup, cols, height int) ([]wkColumn, bool) {
	if height < 2 {
		return nil, false
	}
	var out []wkColumn
	var cur []wkLine
	flush := func() {
		for len(cur) > 0 && cur[len(cur)-1].bind == nil {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			out = append(out, wkColumn{lines: cur})
		}
		cur = nil
	}
	for _, g := range groups {
		if len(cur) > 0 {
			// A new section needs its blank, its heading and two keys here, or
			// it starts the next column: one key under a heading at the foot
			// of a column reads as a section of one.
			need := 1 + min(len(g.Bindings), 2)
			if g.Title != "" {
				need++
			}
			if len(cur)+need > height {
				flush()
			} else {
				cur = append(cur, wkLine{})
			}
		}
		if g.Title != "" {
			cur = append(cur, wkLine{header: g.Title})
		}
		for i := range g.Bindings {
			// Nor does a column start with the last key of a section alone
			// under its repeated heading: the key before it goes too.
			orphan := len(cur) == height-1 && len(g.Bindings)-i == 2 && i > 0
			if len(cur) >= height || orphan {
				flush()
				if g.Title != "" {
					cur = append(cur, wkLine{header: g.Title})
				}
			}
			cur = append(cur, wkLine{bind: &g.Bindings[i]})
		}
	}
	flush()
	return out, len(out) <= cols
}

// wkHeight is the tallest column.
func wkHeight(cols []wkColumn) int {
	h := 0
	for _, c := range cols {
		h = max(h, len(c.lines))
	}
	return h
}

// wkWidth is the columns laid side by side.
func wkWidth(cols []wkColumn) int {
	w := 0
	for i := range cols {
		if i > 0 {
			w += whichKeyGap
		}
		w += cols[i].width()
	}
	return w
}

// layoutWhichKey picks the columns for groups on a screen that gives the panel
// avail cells across and maxRows lines of keys. It returns the columns and how
// many key lines had to be left out.
func layoutWhichKey(groups []config.KeybindingGroup, avail, maxRows int) ([]wkColumn, int) {
	lines, keys := 0, 0
	for _, g := range groups {
		lines += len(wkBlock(g)) + 1
		keys += len(g.Bindings)
	}

	// Every height is tried with both packings, as many columns as they take,
	// and the one that fits across with the lowest cost wins: its height,
	// plus whichKeySplitCost for each section that runs on into another
	// column. A split costs a repeated heading and a section read in two
	// places, so the panel takes a few more rows rather than split one, and
	// splits only when the screen is too short or too narrow for that. Ties
	// go to the fewer splits, then to the shorter panel.
	var pick []wkColumn
	pickCost, pickSplits := 0, 0
	for h := 1; h <= min(lines, maxRows); h++ {
		for _, packer := range []func([]config.KeybindingGroup, int, int) ([]wkColumn, bool){packWhole, packFlow} {
			cols, _ := packer(groups, keys, h)
			if cols == nil {
				continue
			}
			for i := range cols {
				cols[i].measure()
			}
			if wkWidth(cols) > avail {
				continue
			}
			splits := wkSplits(cols)
			cost := wkHeight(cols) + whichKeySplitCost*splits
			if pick == nil || cost < pickCost || (cost == pickCost && splits < pickSplits) {
				pick, pickCost, pickSplits = cols, cost, splits
			}
		}
	}
	if pick != nil {
		return pick, 0
	}

	// Nothing fits as it is. Descriptions are cut to make room for more
	// columns, as far as whichKeyMinDesc, and then the lines that still do
	// not fit are left out with a count.
	// Of those, the one that cuts the fewest cells wins, then the cheapest.
	pickCut := 0
	for h := 1; h <= min(lines, maxRows); h++ {
		cols, _ := packFlow(groups, keys, h)
		if cols == nil {
			continue
		}
		for i := range cols {
			cols[i].measure()
		}
		cutTo(cols, avail)
		if wkWidth(cols) > avail {
			continue
		}
		cut, splits := 0, wkSplits(cols)
		for _, c := range cols {
			if c.truncateDesc > 0 {
				cut += (c.descW - c.truncateDesc) * len(c.lines)
			}
		}
		cost := wkHeight(cols) + whichKeySplitCost*splits
		if pick == nil || cut < pickCut || (cut == pickCut && cost < pickCost) {
			pick, pickCut, pickCost = cols, cut, cost
		}
	}
	if pick != nil {
		return pick, 0
	}
	return clipWhichKey(groups, avail, max(maxRows, 2))
}

// wkSplits counts the sections that run on from one column into the next: a
// column that starts with a key, or with the heading the column before it
// ended under.
func wkSplits(cols []wkColumn) int {
	n := 0
	last := ""
	for i, c := range cols {
		if i > 0 && len(c.lines) > 0 {
			first := c.lines[0]
			if first.bind != nil || (first.header != "" && first.header == last) {
				n++
			}
		}
		for _, l := range c.lines {
			if l.header != "" {
				last = l.header
			}
		}
	}
	return n
}

// cutTo shortens the descriptions of cols, evenly, until they fit avail or
// reach whichKeyMinDesc.
func cutTo(cols []wkColumn, avail int) {
	for wkWidth(cols) > avail {
		widest, at := 0, -1
		for i := range cols {
			d := cols[i].descW
			if cols[i].truncateDesc > 0 {
				d = min(d, cols[i].truncateDesc)
			}
			if d > widest {
				widest, at = d, i
			}
		}
		if at < 0 || widest <= whichKeyMinDesc {
			return
		}
		cols[at].truncateDesc = widest - 1
	}
}

// clipWhichKey is the last resort on a screen too small for the menu: the
// columns that fit, filled to maxRows, with the last line saying how many
// keys were left out.
func clipWhichKey(groups []config.KeybindingGroup, avail, maxRows int) ([]wkColumn, int) {
	total := 0
	for _, g := range groups {
		total += len(g.Bindings)
	}
	for keep := total - 1; keep > 0; keep-- {
		trimmed := trimGroups(groups, keep)
		for n := 1; ; n++ {
			cols, _ := packFlow(trimmed, n, maxRows-1)
			if cols == nil {
				break
			}
			for i := range cols {
				cols[i].measure()
			}
			cutTo(cols, avail)
			if wkWidth(cols) > avail {
				break
			}
			if len(cols) <= n {
				return cols, total - keep
			}
		}
	}
	return nil, total
}

// trimGroups is groups cut to their first keep bindings.
func trimGroups(groups []config.KeybindingGroup, keep int) []config.KeybindingGroup {
	var out []config.KeybindingGroup
	for _, g := range groups {
		if keep <= 0 {
			break
		}
		if len(g.Bindings) > keep {
			g.Bindings = g.Bindings[:keep]
		}
		keep -= len(g.Bindings)
		out = append(out, g)
	}
	return out
}

// whichKeyCache holds the last layout. The menu is drawn on every frame while
// the prefix is held, and the layout tries every height with both packings,
// which is work worth doing once per menu and screen size rather than per
// frame.
type whichKeyCache struct {
	key  uint64
	cols []wkColumn
	more int
}

// whichKeyLayout is layoutWhichKey for the screen in force, cached. The panel
// is laid out in the room beside the rail first, so it does not cover it; only
// when the menu would have to be cut there does it take the whole width.
func (m *OS) whichKeyLayout(title string, groups []config.KeybindingGroup, maxRows int) ([]wkColumn, int) {
	// Two cells of padding each side, and two cells in from the screen edge.
	full := max(m.GetRenderWidth()-8, 1)
	beside := full
	if cw := m.GetContentWidth(); cw > 0 && cw < m.GetRenderWidth() {
		beside = max(cw-8, 1)
	}
	key := whichKeySignature(title, groups, full, beside, maxRows)
	if c := &m.whichKeyCache; c.key == key && c.cols != nil {
		return c.cols, c.more
	}
	cols, more := layoutWhichKey(groups, beside, maxRows)
	if beside < full && (more > 0 || wkCut(cols)) {
		cols, more = layoutWhichKey(groups, full, maxRows)
	}
	m.whichKeyCache = whichKeyCache{key: key, cols: cols, more: more}
	return cols, more
}

// wkCut reports whether any column had its descriptions shortened.
func wkCut(cols []wkColumn) bool {
	for _, c := range cols {
		if c.truncateDesc > 0 {
			return true
		}
	}
	return false
}

// whichKeySignature is an FNV-1a hash of everything the layout depends on.
func whichKeySignature(title string, groups []config.KeybindingGroup, sizes ...int) uint64 {
	h := uint64(14695981039346656037)
	mix := func(str string) {
		for i := 0; i < len(str); i++ {
			h ^= uint64(str[i])
			h *= 1099511628211
		}
		h ^= 0xff
		h *= 1099511628211
	}
	mix(title)
	for _, g := range groups {
		mix(g.Title)
		for _, b := range g.Bindings {
			mix(b.Key)
			mix(b.Description)
			if b.Submenu {
				mix("+")
			}
		}
	}
	for _, n := range sizes {
		h ^= uint64(n)
		h *= 1099511628211
	}
	return h
}

// whichKeyMenu is the title and sections of the prefix menu in force.
func (m *OS) whichKeyMenu() (string, []config.KeybindingGroup) {
	switch {
	case m.WorkspacePrefixActive:
		return "Workspace", config.GetPrefixKeybindingGroups("workspace")
	case m.MinimizePrefixActive:
		groups := config.GetPrefixKeybindingGroups("minimize")
		minimizedCount := 0
		for _, win := range m.Windows {
			if win.Minimized && win.Workspace == m.CurrentWorkspace && !win.IsScratch {
				minimizedCount++
			}
		}
		for gi := range groups {
			for i := range groups[gi].Bindings {
				if groups[gi].Bindings[i].Key == "1-9" {
					groups[gi].Bindings[i].Description = fmt.Sprintf("Restore window (%d minimized)", minimizedCount)
				}
			}
		}
		return "Minimize", groups
	case m.TilingPrefixActive:
		return "Window", config.GetPrefixKeybindingGroups("window")
	case m.DebugPrefixActive:
		return "Debug", config.GetPrefixKeybindingGroups("debug")
	case m.TapePrefixActive:
		return "Tape", config.GetPrefixKeybindingGroups("tape")
	case m.LayoutPrefixActive:
		return "Layout", config.GetPrefixKeybindingGroups("layout")
	}
	return "Prefix", m.prefixMenuGroups()
}

// renderWhichKey draws the which-key panel and returns it with its position.
func (m *OS) renderWhichKey() (string, int, int) {
	title, groups := m.whichKeyMenu()

	// The panel spends four rows on a blank pad above and below, the title
	// and its rule, and keeps a row clear of the screen's edge. The room
	// starts under a dock at the top: measured from the top of the screen, a
	// long menu ran up over that dock and cut its notice in half.
	maxRows := 1 << 20
	if room := m.GetRenderHeight() - m.GetTopMargin(); room > 0 {
		maxRows = max(room-5, 1)
	}
	cols, more := m.whichKeyLayout(title, groups, maxRows)

	pal := theme.UI()
	bg := pal.Surface
	keyStyle := overlay.Style(bg).Foreground(theme.Readable(pal.AccentBright, bg)).Bold(true)
	descStyle := overlay.Style(bg).Foreground(pal.FgDim)
	subStyle := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg))
	headStyle := overlay.Style(bg).Foreground(pal.FgMute).Bold(true)

	height := wkHeight(cols)
	contentWidth := max(wkWidth(cols), lipgloss.Width(title))
	if more > 0 {
		contentWidth = max(contentWidth, lipgloss.Width(fmt.Sprintf("+%d more", more)))
	}
	contentWidth = min(contentWidth, max(m.GetRenderWidth()-8, 1))

	cell := func(c *wkColumn, l wkLine) string {
		w := c.width()
		switch {
		case l.bind != nil:
			descW := max(w-c.keyW-whichKeyKeyGap, 1)
			key := keyStyle.Render(l.bind.Key) +
				overlay.Style(bg).Render(strings.Repeat(" ", c.keyW-lipgloss.Width(l.bind.Key)+whichKeyKeyGap))
			if l.bind.Submenu {
				return overlay.Fill(key+subStyle.Render(overlay.Truncate("+"+l.bind.Description, descW)), w, bg)
			}
			return overlay.Fill(key+descStyle.Render(overlay.Truncate(l.bind.Description, descW)), w, bg)
		case l.header != "":
			return overlay.Fill(headStyle.Render(overlay.Truncate(l.header, w)), w, bg)
		}
		return overlay.Fill("", w, bg)
	}

	var body []string
	gap := overlay.Style(bg).Render(strings.Repeat(" ", whichKeyGap))
	for y := range height {
		var b strings.Builder
		for i := range cols {
			if i > 0 {
				b.WriteString(gap)
			}
			if y < len(cols[i].lines) {
				b.WriteString(cell(&cols[i], cols[i].lines[y]))
			} else {
				b.WriteString(overlay.Fill("", cols[i].width(), bg))
			}
		}
		body = append(body, overlay.Fill(b.String(), contentWidth, bg))
	}
	if more > 0 {
		// The count takes the last line of the last column, which clipWhichKey
		// left free for it.
		last := len(cols) - 1
		x := 0
		for i := range last {
			x += cols[i].width() + whichKeyGap
		}
		y := len(cols[last].lines)
		note := overlay.Style(bg).Foreground(pal.FgMute).Render(fmt.Sprintf("+%d more", more))
		if y >= len(body) {
			body = append(body, overlay.Fill("", contentWidth, bg))
			y = len(body) - 1
		}
		row := body[y]
		body[y] = overlay.Fill(ansi.Truncate(row, x, "")+note, contentWidth, bg)
	}

	pad := overlay.Style(bg).Render("  ")
	blank := overlay.Style(bg).Render(strings.Repeat(" ", contentWidth+4))
	lines := []string{blank,
		pad + overlay.Fill(overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(strings.ToLower(title), contentWidth)), contentWidth, bg) + pad,
		pad + overlay.Rule(contentWidth, bg, pal) + pad,
	}
	for _, l := range body {
		lines = append(lines, pad+l+pad)
	}
	lines = append(lines, blank)
	// Where the surface would vanish into the ground, at 16 colours, the
	// panel keeps a hairline edge the way every Panel does.
	lines = overlay.FrameBlock(lines, contentWidth+4, bg, pal)
	return strings.Join(lines, "\n"), contentWidth + 4, len(lines)
}
