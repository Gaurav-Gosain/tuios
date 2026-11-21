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

const (
	whichKeyGap       = 4
	whichKeyKeyGap    = 2
	whichKeySplitCost = 3
	whichKeyMinDesc   = 10
)

type wkLine struct {
	header string
	bind   *config.Keybinding
}

type wkColumn struct {
	lines        []wkLine
	keyW, descW  int
	headerW      int
	truncateDesc int
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

func wkDescWidth(b *config.Keybinding) int {
	w := lipgloss.Width(b.Description)
	if b.Submenu {
		w++
	}
	return w
}

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

func wkHeight(cols []wkColumn) int {
	h := 0
	for _, c := range cols {
		h = max(h, len(c.lines))
	}
	return h
}

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

func layoutWhichKey(groups []config.KeybindingGroup, avail, maxRows int) ([]wkColumn, int) {
	lines, keys := 0, 0
	for _, g := range groups {
		lines += len(wkBlock(g)) + 1
		keys += len(g.Bindings)
	}

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

type whichKeyCache struct {
	key   uint64
	cols  []wkColumn
	more  int
	valid bool
}

func (m *OS) whichKeyLayout(title string, groups []config.KeybindingGroup, maxRows int) ([]wkColumn, int) {
	full := max(m.GetRenderWidth()-8, 1)
	beside := full
	if cw := m.ViewContentWidth(); cw > 0 && cw < m.GetRenderWidth() {
		beside = max(cw-8, 1)
	}
	if m.Settings.WhichKeyPosition == "center" {
		beside = max(min(beside, m.modalRoomWidth()-8), 1)
	}
	key := whichKeySignature(title, groups, full, beside, maxRows)
	if c := &m.whichKeyCache; c.valid && c.key == key {
		return c.cols, c.more
	}
	cols, more := layoutWhichKey(groups, beside, maxRows)
	if beside < full && (more > 0 || wkCut(cols)) {
		cols, more = layoutWhichKey(groups, full, maxRows)
	}
	m.whichKeyCache = whichKeyCache{key: key, cols: cols, more: more, valid: true}
	return cols, more
}

func wkCut(cols []wkColumn) bool {
	for _, c := range cols {
		if c.truncateDesc > 0 {
			return true
		}
	}
	return false
}

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

func (m *OS) whichKeyMenu() (string, []config.KeybindingGroup) {
	menu := func(prefixType string) []config.KeybindingGroup {
		return config.PrefixMenuGroups(m.KeybindRegistry, prefixType, config.MenuState{Daemon: m.IsDaemonSession, Minimized: -1})
	}
	switch {
	case m.Menu != "":
		var kb *config.KeybindingsConfig
		if m.KeybindRegistry != nil && m.KeybindRegistry.GetConfig() != nil {
			kb = &m.KeybindRegistry.GetConfig().Keybindings
		}
		if kb == nil {
			return m.Menu, nil
		}
		return kb.MenuTitle(m.Menu), config.MenuKeybindingGroups(kb, m.Menu)
	case m.SubMenu == SubMenuMinimize:
		minimizedCount := 0
		for _, win := range m.Windows {
			if win.Minimized && win.Workspace == m.CurrentWorkspace && !win.IsScratch {
				minimizedCount++
			}
		}
		sub := subMenus[SubMenuMinimize]
		return sub.title, config.PrefixMenuGroups(m.KeybindRegistry, sub.name, config.MenuState{Minimized: minimizedCount})
	case m.SubMenu != SubMenuNone:
		sub := subMenus[m.SubMenu]
		return sub.title, menu(sub.name)
	}
	return "Prefix", m.prefixMenuGroups()
}

func (m *OS) renderWhichKey() (string, int, int) {
	title, groups := m.whichKeyMenu()
	if m.LockHeld {
		title, groups = m.heldWhichKey(title, groups)
	}

	maxRows := 1 << 20
	if room := m.GetRenderHeight() - m.viewReserve().Top; room > 0 {
		maxRows = max(room-5, 1)
	} else if m.GetRenderHeight() > 0 {
		maxRows = 1
	}
	cols, more := m.whichKeyLayout(title, groups, maxRows)

	pal := m.overlayUI()
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
				mark := chevron(&m.Settings)
				desc := overlay.Truncate(l.bind.Description, max(descW-1, 1))
				pad := max(descW-lipgloss.Width(desc)-lipgloss.Width(mark), 0)
				return overlay.Fill(key+subStyle.Render(desc)+overlay.Style(bg).Render(strings.Repeat(" ", pad))+subStyle.Render(mark), w, bg)
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
		x, y := 0, 0
		if last := len(cols) - 1; last >= 0 {
			for i := range last {
				x += cols[i].width() + whichKeyGap
			}
			y = len(cols[last].lines)
		}
		note := overlay.Style(bg).Foreground(pal.FgMute).Render(fmt.Sprintf("+%d more", more))
		if y >= len(body) {
			body = append(body, overlay.Fill("", contentWidth, bg))
			y = len(body) - 1
		}
		row := body[y]
		body[y] = overlay.Fill(ansi.Truncate(ansi.Truncate(row, x, "")+note, contentWidth, ""), contentWidth, bg)
	}

	pad := overlay.Style(bg).Render("  ")
	blank := overlay.Style(bg).Render(strings.Repeat(" ", contentWidth+4))
	lines := []string{blank,
		pad + overlay.Fill(overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(whichKeyTitle(title, m.Menu != ""), contentWidth)), contentWidth, bg) + pad,
		pad + overlay.Rule(contentWidth, bg, pal) + pad,
	}
	for _, l := range body {
		lines = append(lines, pad+l+pad)
	}
	lines = append(lines, blank)
	lines = overlay.FrameBlock(lines, contentWidth+4, bg, pal)
	return strings.Join(lines, "\n"), contentWidth + 4, len(lines)
}

func whichKeyTitle(title string, declared bool) string {
	if declared {
		return title
	}
	return strings.ToLower(title)
}
