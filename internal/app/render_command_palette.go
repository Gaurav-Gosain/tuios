package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Command palette layout constants. These are the preferred sizes; a narrower
// or shorter screen gets a palette fitted to it (see overlay_fit.go).
const (
	paletteInnerWidth = 64
	paletteMaxVisible = 10

	// paletteCategoryWidth is the narrowest inner width that still shows a
	// row's category in the meta column while a query is typed. Below it the
	// category is dropped so the name and its shortcut keep the room.
	paletteCategoryWidth = 46
)

// paletteHints is the palette footer, shared by the renderer and the sizing
// helper so both measure the same panel.
var paletteHints = []overlay.Hint{
	{Key: "↑↓", Label: "move"},
	{Key: overlay.EnterGlyph, Label: "run"},
	// The state filter is a token typed into the search field rather than a key,
	// so the footer is the only place it can announce itself.
	{Key: "@", Label: "state"},
	{Key: "esc", Label: "close"},
}

// paletteStateHint is the index of the "@ state" hint in paletteHints.
const paletteStateHint = 2

// paletteFooter is the palette's footer for this client: the "@ state" hint
// waits until an agent has been seen, since the filter it announces lists
// agent panes and there are none. Typing "@" works either way.
func (m *OS) paletteFooter() []overlay.Hint {
	if m.agentsSeen() {
		return paletteHints
	}
	return slices.Delete(slices.Clone(paletteHints), paletteStateHint, paletteStateHint+1)
}

// paletteLayout returns the palette's fitted inner width and visible row count.
// The keyboard navigation uses the same numbers as the renderer so the
// selection cannot scroll out of the rows actually drawn.
func (m *OS) paletteLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(paletteInnerWidth)
	// Body lines that are not command rows: the search input, its rule, and the
	// match count.
	rows, hints = m.panelBody(paletteMaxVisible, 3, width, nil, m.paletteFooter())
	return width, rows, hints
}

// paletteLine is one row of the palette's list: a command, or the header of
// the category the commands under it belong to.
type paletteLine struct {
	item   int    // index into the filtered items; -1 for a header
	header string // the category a header names
}

// paletteLines lays filtered out as the rows the list draws. Grouped, each
// category's run gets a header row; ungrouped, it is one row per command.
//
// There is no blank row above a header. The palette shows ten rows, and a
// blank before each header left five of them for commands under the first
// few one-command categories. The header is set left of the command names in
// the accent, which divides the list well enough on its own.
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

// paletteScroll keeps the selected command in view, and with it the header of
// its category when it is the first command under one, so moving up onto a
// category shows its name rather than leaving it one row above the list.
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

// paletteMetaWidth is the width of the category column while a query is
// typed: the widest category in the list, so the column does not move as the
// list scrolls. Zero when the column is not drawn.
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

// renderCommandPalette draws the command palette on the shared panel grammar: a
// search input, a scrolling list of matching commands with their shortcuts,
// and a highlight bar on the selection. With nothing typed the commands sit
// under category headers; while a query is typed they are ranked, and each
// row names its category in a quiet column on the right.
func (m *OS) renderCommandPalette() (string, overlay.Geometry, []overlayRowHit) {
	items := m.allPaletteItems()
	filtered := FilterCommandPalette(items, m.CommandPaletteQuery)
	grouped := paletteGrouped(m.CommandPaletteQuery)

	pal := theme.UI()
	bg := pal.Surface

	width, visible, hints := m.paletteLayout()
	list := paletteLines(filtered, grouped)
	m.CommandPaletteScroll = paletteScroll(m.CommandPaletteScroll, list, m.CommandPaletteSelected, visible)
	metaW := paletteMetaWidth(filtered, grouped, width)

	var lines []string

	// Search input.
	// The accent is the theme's and the panel's ground is not, so both are
	// measured against it. The cursor is a block, so the mark floor is enough.
	cursor := overlay.Style(bg).Foreground(theme.ReadableAt(pal.Accent, bg, theme.MarkFloor)).Render("█")
	search := overlay.Style(bg).Foreground(theme.Readable(pal.AccentBright, bg)).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.CommandPaletteQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	start := m.CommandPaletteScroll
	end := min(start+visible, len(list))
	if len(filtered) == 0 {
		empty := overlay.Empty{Message: "No matching commands", Hint: overlay.Hint{Key: "esc", Label: "close"}}
		// A "#" search that found nothing needs to say it was a search over
		// actions. "No matching commands" under a token the user has just been
		// taught reads as the token not working.
		if keybinds, rest := splitPaletteKeybinds(m.CommandPaletteQuery); keybinds {
			empty.Message = keybindPaletteHint(rest)
		}
		// The count row stays blank, so the empty state takes the rows the
		// list would have and the panel keeps its height.
		lines = append(lines, empty.Lines(width, visible, bg, pal)...)
		lines = append(lines, overlay.Style(bg).Render(" "))
	} else {
		headerStyle := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true)
		for _, l := range list[start:end] {
			if l.item < 0 {
				lines = append(lines, headerStyle.Render(overlay.Truncate(l.header, width)))
				continue
			}
			st := overlay.RowState{Cursor: l.item == m.CommandPaletteSelected, Focused: true}
			lines = append(lines, pal.Row(paletteRow(filtered[l.item], st.Cursor, pal, width, metaW), width, st, bg))
		}
		for len(lines) < visible+2 {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
		if len(list) > visible {
			// Counted against the rows this query could reach, not against every
			// row in the model. The action rows are hidden without the "#" token
			// and the command rows are hidden with it, so a denominator that
			// added both would say "65 of 289" over a list that can only ever
			// hold 65.
			info := fmt.Sprintf("%d of %d commands", len(filtered), paletteReachable(items, m.CommandPaletteQuery))
			lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+info))
		} else {
			lines = append(lines, overlay.Style(bg).Render(" "))
		}
	}

	panel := overlay.Panel{
		Title: "Command Palette",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	// Hit rows for the visible commands. Header lines are not rows: a click on
	// one lands on nothing rather than on the command under it.
	var rows []overlayRowHit
	if len(filtered) > 0 {
		for i, l := range list[start:end] {
			if l.item < 0 {
				continue
			}
			rowY := geo.BodyY + i + 2 // +2 for the search line and rule
			rows = append(rows, overlayRowHit{
				Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
				Idx:  l.item,
			})
		}
	}
	return content, geo, rows
}

// paletteRow renders one command row: the name, then on the right its
// shortcut and, while a query is typed, its category in a column metaW wide.
// Every name starts on the same column, which is what makes the list read as a
// menu rather than a log.
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
		marker = "› "
	}
	name := overlay.Truncate(printableTitle(item.Name), max(width-2-rightW-1, 1))
	left := overlay.Style(bg).Foreground(theme.Readable(pal.Accent, bg)).Bold(true).Render(marker) +
		paletteRowName(name, item.AgentState, item.AgentSeen, item.Match, bg, nameColor, selected, pal)

	gap := max(width-lipgloss.Width(left)-rightW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

// paletteRowName renders a row's name: the characters the query matched in the
// accent, and an agent-state glyph in its state's color. Both are spliced in by
// byte offset, which is the whole reason Name is kept free of ANSI.
//
// match holds offsets into the untruncated name, so any that fall past the
// truncation simply never come up.
func paletteRowName(name, agentState string, doneSeen bool, match []int, bg, nameColor color.Color, selected bool, pal overlay.Palette) string {
	nameStyle := overlay.Style(bg).Foreground(nameColor).Bold(selected)
	glyph, glyphColor := agentMark(agentState, doneSeen, pal)
	glyphAt := -1
	if glyph != "" {
		glyphAt = strings.Index(name, glyph)
	}
	if glyphAt < 0 && len(match) == 0 {
		return nameStyle.Render(name)
	}

	// At 16 colours the accent may be a slot close to the name's own ink, so a
	// match is underlined as well as bold there.
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
