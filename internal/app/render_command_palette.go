package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	paletteInnerWidth = 64
	paletteMaxVisible = 10

	paletteCategoryWidth = 46
)

var paletteHints = []overlay.Hint{
	{Key: hintListMove, Label: "move"},
	{Key: overlay.EnterGlyph, Label: "run"},
	{Key: PaletteKeybindToken, Label: "actions"},
	{Key: "@", Label: "state"},
	{Key: "esc", Label: "close"},
}

const (
	paletteActionsHint = 2
	paletteStateHint   = 3
)

func (m *OS) paletteFooter() []overlay.Hint {
	if m.prompt.req != nil {
		return m.promptFooter()
	}
	hints := slices.Clone(paletteHints)
	if !m.agentsSeen() {
		hints = slices.Delete(hints, paletteStateHint, paletteStateHint+1)
	}
	switch keybinds, _ := splitPaletteKeybinds(m.paletteList.Query.String()); {
	case m.fileSearch:
		hints = slices.Delete(hints, paletteActionsHint, paletteActionsHint+1)
	case keybinds:
		hints[paletteActionsHint] = overlay.Hint{Key: overlay.ShortKey(PaletteRebindKey), Label: "rebind"}
	}
	return hints
}

func (m *OS) paletteLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(paletteInnerWidth)
	preferred := paletteMaxVisible
	if req := m.prompt.req; req != nil {
		preferred = min(paletteMaxVisible, max(len(req.Items), 1))
		if req.AllowOther {
			preferred = min(paletteMaxVisible, len(req.Items)+1)
		}
	}
	rows, hints = m.panelBody(preferred, 2+len(m.promptHead(width)), width, nil, m.paletteFooter())
	return width, rows, hints
}

type paletteLine struct {
	item   int
	header string
}

func paletteLines(filtered []CommandPaletteItem, grouped bool) []paletteLine {
	lines := make([]paletteLine, 0, len(filtered)+16)
	for i, it := range filtered {
		if grouped && (i == 0 || it.Category != filtered[i-1].Category) {
			lines = append(lines, paletteLine{item: -1, header: it.Category})
		}
		lines = append(lines, paletteLine{item: i})
	}
	return lines
}

func paletteScroll(scroll int, lines []paletteLine, selected, visible int) int {
	n := len(lines)
	if visible <= 0 || n <= visible {
		return 0
	}
	row := slices.IndexFunc(lines, func(l paletteLine) bool { return l.item == selected })
	scroll = min(max(scroll, 0), n-visible)
	if row < 0 {
		return scroll
	}
	top := row
	if top > 0 && lines[top-1].item < 0 {
		top--
	}
	if top < scroll {
		scroll = top
	}
	if row >= scroll+visible {
		scroll = row - visible + 1
	}
	return min(max(scroll, 0), n-visible)
}

func paletteMetaWidth(filtered []CommandPaletteItem, grouped bool, width int) int {
	if grouped || width < paletteCategoryWidth {
		return 0
	}
	w := 0
	for _, it := range filtered {
		w = max(w, lipgloss.Width(it.Category))
	}
	return w
}

func (m *OS) renderCommandPalette() (string, overlay.Geometry, []overlayRowHit) {
	items := m.allPaletteItems()
	filtered := m.filteredPaletteItems()
	grouped := paletteGrouped(m.paletteList.Query.String()) && !m.fileSearch && m.prompt.req == nil

	pal := m.overlayUI()
	bg := pal.Surface

	width, visible, hints := m.paletteLayout()
	list := paletteLines(filtered, grouped)
	m.paletteList.Scroll = paletteScroll(m.paletteList.Scroll, list, m.paletteList.Cursor, visible)
	metaW := paletteMetaWidth(filtered, grouped, width)

	var lines []string
	head := m.promptHead(width)
	for _, l := range head {
		lines = append(lines, overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(l))
	}

	search := palettePicker().searchLine(m, width)
	if req := m.prompt.req; req != nil && req.Placeholder != "" && m.paletteList.Query.String() == "" {
		search += overlay.Style(bg).Foreground(pal.FgMute).Render(overlay.Truncate(printableTitle(req.Placeholder), max(width-lipgloss.Width(search), 0)))
	}
	count := ""
	if len(filtered) > 0 && len(list) > visible {
		kind := "commands"
		switch {
		case m.fileSearch:
			kind = "files"
		case m.prompt.req != nil:
			kind = "items"
		}
		total := paletteReachable(items, m.paletteList.Query.String())
		if m.fileSearch || m.prompt.req != nil {
			total = len(items)
		}
		count = fmt.Sprintf("%d of %d %s", len(filtered), total, kind)
	}
	lines = append(lines, searchWithCount(search, count, width, bg, pal))
	if req := m.prompt.req; req != nil && req.Kind == session.PromptInput {
		content, geo := overlay.Panel{Title: promptPanelTitle(req), Width: width, Body: strings.Join(lines, "\n"), Hints: hints}.Render(pal)
		return content, geo, nil
	}
	lines = append(lines, overlay.Rule(width, bg, pal))

	start := m.paletteList.Scroll
	end := min(start+visible, len(list))
	if len(filtered) == 0 {
		empty := overlay.Empty{Message: "No matching commands", Hint: overlay.Hint{Key: "esc", Label: "close"}}
		if m.prompt.req != nil {
			empty = overlay.Empty{Message: "No matching items", Hint: overlay.Hint{Key: "esc", Label: "cancel"}}
		}
		if m.fileSearch {
			switch {
			case m.fileSearchScanning:
				empty.Message = "Searching files..."
			case m.fileSearchErr != "":
				empty.Message = m.fileSearchErr
			default:
				empty.Message = "No matching files"
			}
		}
		if keybinds, rest := splitPaletteKeybinds(m.paletteList.Query.String()); keybinds && !m.fileSearch && m.prompt.req == nil {
			empty.Message = keybindPaletteHint(rest)
		}
		lines = append(lines, empty.Lines(width, visible, bg, pal)...)
	} else {
		headerStyle := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true)
		for _, l := range list[start:end] {
			if l.item < 0 {
				lines = append(lines, headerStyle.Render(overlay.Truncate(l.header, width)))
				continue
			}
			st := overlay.RowState{Cursor: l.item == m.paletteList.Cursor, Focused: true}
			lines = append(lines, pal.Row(paletteRow(filtered[l.item], st.Cursor, pal, width, metaW), width, st, bg))
		}
		for len(lines) < visible+2+len(head) {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Title: "Command Palette",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	if m.prompt.req != nil {
		panel.Title = promptPanelTitle(m.prompt.req)
	}
	if m.fileSearch {
		panel.Title = "Search files"
		if m.fileSearchTruncated {
			panel.Title = "Search files (partial)"
		}
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	if len(filtered) > 0 {
		for i, l := range list[start:end] {
			if l.item < 0 {
				continue
			}
			rowY := geo.BodyY + i + 2 + len(head)
			rows = append(rows, overlayRowHit{
				Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
				Idx:  l.item,
			})
		}
	}
	return content, geo, rows
}

func paletteRow(item CommandPaletteItem, selected bool, pal overlay.Palette, width, metaW int) string {
	bg := pal.Ground(overlay.RowState{Cursor: selected, Focused: true}, pal.Surface)
	nameColor := pal.FgDim
	if selected {
		nameColor = pal.Fg
	}

	right := ""
	rightW := 0
	if item.Shortcut != "" {
		right = overlay.Style(bg).Foreground(pal.FgDim).Render(item.Shortcut)
		rightW = lipgloss.Width(item.Shortcut)
	}
	if metaW > 0 {
		cat := overlay.Truncate(item.Category, metaW)
		right += overlay.Style(bg).Render("  ") +
			overlay.Style(bg).Foreground(pal.FgMute).Render(cat) +
			overlay.Style(bg).Render(strings.Repeat(" ", metaW-lipgloss.Width(cat)))
		rightW += 2 + metaW
	}

	marker := "  "
	if selected {
		marker = overlay.Sigil()
	}
	mark, markW := "", 0
	if item.promptMark != promptMarkNone {
		on, off := promptMarks()
		text, ink := off+" ", pal.FgMute
		if item.promptMark == promptMarkOn {
			text, ink = on+" ", theme.Readable(pal.Accent, bg)
		}
		mark, markW = overlay.Style(bg).Foreground(ink).Bold(true).Render(text), lipgloss.Width(text)
	}
	name := overlay.Truncate(printableTitle(item.Name), max(width-2-markW-rightW-1, 1))
	left := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true).Render(marker) + mark +
		paletteRowName(name, item.AgentState, item.AgentSeen, item.AgentSubagents, item.Match, bg, nameColor, selected, pal)

	gap := max(width-lipgloss.Width(left)-rightW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

func paletteRowName(name, agentState string, doneSeen bool, running int, match []int, bg, nameColor color.Color, selected bool, pal overlay.Palette) string {
	nameStyle := overlay.Style(bg).Foreground(nameColor).Bold(selected)
	glyph, glyphColor := agentMark(agentState, doneSeen, running, pal)
	glyphAt := -1
	if glyph != "" {
		glyphAt = strings.Index(name, glyph)
	}
	if glyphAt < 0 && len(match) == 0 {
		return nameStyle.Render(name)
	}

	hitStyle := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true).Underline(pal.Depth == overlay.Depth16)
	var out, run strings.Builder
	hot := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if hot {
			out.WriteString(hitStyle.Render(run.String()))
		} else {
			out.WriteString(nameStyle.Render(run.String()))
		}
		run.Reset()
	}

	mi := 0
	for i := 0; i < len(name); {
		for mi < len(match) && match[mi] < i {
			mi++
		}
		if i == glyphAt {
			flush()
			hot = false
			out.WriteString(overlay.Style(bg).Foreground(glyphColor).Bold(true).Render(glyph))
			i += len(glyph)
			continue
		}
		_, size := utf8.DecodeRuneInString(name[i:])
		lit := mi < len(match) && match[mi] == i
		if lit != hot {
			flush()
			hot = lit
		}
		run.WriteString(name[i : i+size])
		i += size
	}
	flush()
	return out.String()
}

func searchWithCount(search, count string, width int, bg color.Color, pal overlay.Palette) string {
	sw, cw := lipgloss.Width(search), lipgloss.Width(count)
	if count == "" || sw+2+cw > width {
		return search
	}
	return search + overlay.Style(bg).Render(strings.Repeat(" ", width-sw-cw)) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(count)
}
