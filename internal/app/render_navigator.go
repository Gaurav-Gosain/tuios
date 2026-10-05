package app

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The navigator's frame: one panel as wide and as tall as the screen allows,
// with the search line on top, the tree on the left and the preview of the
// highlighted row on the right.

const (
	// navigatorWidth is the panel width asked for. The screen narrows it.
	navigatorWidth = 160
	// navigatorRowsWanted is the list rows asked for. The screen shortens it.
	navigatorRowsWanted = 40
	// navigatorListMin and navigatorListMax bound the list column.
	navigatorListMin = 28
	navigatorListMax = 64
	// navigatorPreviewMin is the narrowest preview worth drawing. Below it
	// the list takes the whole panel.
	navigatorPreviewMin = 24
)

// navigatorHints are the footer's keys, in the order of their worth.
func (m *OS) navigatorHints() []overlay.Hint {
	if m.navigator.searching {
		return []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "go"},
			{Key: "↑↓", Label: "move"},
			{Key: "esc", Label: "stop search"},
		}
	}
	return []overlay.Hint{
		{Key: overlay.EnterGlyph, Label: "go"},
		{Key: "/", Label: "search"},
		{Key: "j/k", Label: "move"},
		{Key: "h/l", Label: "fold"},
		{Key: "esc", Label: "close"},
	}
}

// navigatorLayout is the panel's inner width, the list column's width and
// the list rows the screen has room for.
func (m *OS) navigatorLayout() (width, listW, rows int, hints []overlay.Hint) {
	width = m.panelWidth(navigatorWidth)
	// The search line, its rule, and the position line under the list.
	rows, hints = m.panelBody(navigatorRowsWanted, 3, width, nil, m.navigatorHints())
	listW = width
	if width-navigatorListMin-3 >= navigatorPreviewMin {
		listW = min(max(width*2/5, navigatorListMin), navigatorListMax)
	}
	return width, listW, rows, hints
}

// navigatorVisibleRows is how many list rows the frame draws.
func (m *OS) navigatorVisibleRows() int {
	_, _, rows, _ := m.navigatorLayout()
	return rows
}

// renderNavigator draws the navigator and returns the panel, its geometry and
// the list rows' hit areas.
func (m *OS) renderNavigator() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	nav := &m.navigator
	width, listW, visible, hints := m.navigatorLayout()
	rows := m.navigatorRows()
	if len(rows) > 0 {
		nav.cursor = clampInt(nav.cursor, 0, len(rows)-1)
	} else {
		nav.cursor = 0
	}
	nav.scroll = scrollWindow(nav.scroll, nav.cursor, len(rows), visible)

	var lines []string
	lines = append(lines, m.navigatorSearchLine(width, bg, pal), overlay.Rule(width, bg, pal))

	// The list column, one string a row.
	list := make([]string, 0, visible)
	end := min(nav.scroll+visible, len(rows))
	for i := nav.scroll; i < end; i++ {
		st := overlay.RowState{Cursor: i == nav.cursor, Focused: true}
		rowBg := pal.Ground(st, bg)
		list = append(list, pal.Row(m.navigatorRow(rows[i], i == nav.cursor, rowBg, pal, listW), listW, st, bg))
	}
	if len(rows) == 0 {
		msg := "No pane matches"
		if !m.navSearch() {
			msg = "No sessions"
		}
		empty := overlay.Empty{Message: msg, Hint: overlay.Hint{Key: "esc", Label: "close"}}
		list = append(list, empty.Lines(listW, visible, bg, pal)...)
	}
	for len(list) < visible {
		list = append(list, overlay.Style(bg).Render(strings.Repeat(" ", listW)))
	}
	list = list[:visible]

	previewW := width - listW - 3
	if previewW >= navigatorPreviewMin {
		var preview []string
		if len(rows) > 0 {
			preview = m.navigatorPreview(rows[nav.cursor], previewW, visible, bg, pal)
		}
		sep := overlay.Style(bg).Foreground(pal.Edge).Render(" │ ")
		for i := range list {
			p := ""
			if i < len(preview) {
				p = preview[i]
			}
			pad := max(previewW-lipgloss.Width(p), 0)
			list[i] = list[i] + sep + p + overlay.Style(bg).Render(strings.Repeat(" ", pad))
		}
	}
	lines = append(lines, list...)

	status := ""
	switch {
	case nav.loading:
		status = "Reading the other sessions…"
	case len(rows) > 0:
		status = fmt.Sprintf("%d of %d", nav.cursor+1, len(rows))
	}
	lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+status))

	panel := overlay.Panel{
		Title: "Panes",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	hits := make([]overlayRowHit, 0, end-nav.scroll)
	for i := nav.scroll; i < end; i++ {
		y := geo.BodyY + 2 + (i - nav.scroll)
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.BodyX + listW, Y1: y + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// navigatorSearchLine is the search line: the query and a cursor while the
// keyboard is in it, else what / searches.
func (m *OS) navigatorSearchLine(width int, bg color.Color, pal overlay.Palette) string {
	nav := &m.navigator
	sigil := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())
	if nav.searching {
		return sigil + overlay.Style(bg).Foreground(pal.Fg).Render(nav.query) + overlay.Cursor(" ", bg, pal.Fg)
	}
	if nav.query != "" {
		return sigil + overlay.Style(bg).Foreground(pal.Fg).Render(nav.query)
	}
	hint := "Press / to search names, folders, commands and screen text"
	return sigil + overlay.Style(bg).Foreground(pal.FgMute).Render(overlay.Truncate(hint, max(width-3, 1)))
}

// navigatorRow draws one list row.
func (m *OS) navigatorRow(r navRow, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	s := &m.navigator.sessions[r.Session]
	st := overlay.Style(rowBg)
	fold := func(open bool) string {
		if open {
			return m.Settings.GetRailFoldOpenGlyph()
		}
		return m.Settings.GetRailFoldShutGlyph()
	}
	labelInk := pal.FgDim
	if selected {
		labelInk = pal.Fg
	}
	switch r.Kind {
	case navRowSession:
		name := printableTitle(s.Title)
		if s.Host != "" {
			name += " @ " + s.Host
		}
		left := name
		if !m.navSearch() {
			left = fold(m.navExpanded(s.key(), s.Current)) + " " + name
		}
		var right string
		switch {
		case s.Note != "":
			right = st.Foreground(pal.FgMute).Render(s.Note)
		case s.Current:
			right = st.Foreground(pal.Success).Render("current  ") + st.Foreground(pal.FgMute).Render(panePlural(s.Count))
		default:
			right = st.Foreground(pal.FgMute).Render(panePlural(s.Count))
		}
		return listRowSpans(width, listRowMarker(selected), st.Foreground(pal.Fg).Bold(true).Render(left), right, rowBg, pal)
	case navRowWorkspace:
		key := s.key() + "\x00ws" + strconv.Itoa(r.Workspace)
		left := "  " + fold(m.navExpanded(key, true)) + " " + st.Foreground(pal.FgMute).Render("workspace ")
		n := 0
		for _, p := range s.Panes {
			if p.Workspace == r.Workspace {
				n++
			}
		}
		return listRowSpans(width, listRowMarker(selected),
			st.Foreground(labelInk).Render(left)+st.Foreground(labelInk).Bold(true).Render(printableTitle(s.workspaceLabel(r.Workspace))),
			st.Foreground(pal.FgMute).Render(panePlural(n)), rowBg, pal)
	}
	p := &s.Panes[r.Pane]
	indent := "      "
	if m.navSearch() {
		indent = ""
	}
	mark := st.Render("  ")
	if p.Focused && s.Current {
		mark = st.Foreground(pal.Success).Render("● ")
	}
	left := st.Render(indent) + mark + st.Foreground(labelInk).Bold(selected).Render(printableTitle(p.Name))
	if m.navSearch() {
		// A search lists panes from every session, so a row says whose.
		where := printableTitle(s.Title)
		if s.Host != "" {
			where += " @ " + s.Host
		}
		left += st.Foreground(pal.FgMute).Render(" · " + where)
	}
	var right string
	switch {
	case r.Snippet != "":
		right = st.Foreground(pal.FgDim).Italic(true).Render(overlay.Truncate(printableTitle(r.Snippet), max(width/2, 8)))
	case m.navSearch():
		right = st.Foreground(pal.FgMute).Render(printableTitle(s.workspaceLabel(p.Workspace)))
	case p.Command != "" && p.Command != p.Name:
		right = st.Foreground(pal.FgMute).Render(printableTitle(p.Command))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

// navigatorPreview is the preview column for a row: a header naming what it
// shows, a rule, then the pane's screen or the session's panes.
func (m *OS) navigatorPreview(r navRow, width, height int, bg color.Color, pal overlay.Palette) []string {
	s := &m.navigator.sessions[r.Session]
	st := overlay.Style(bg)
	line := func(text string, ink color.Color) string {
		return st.Foreground(ink).Render(overlay.Truncate(text, width))
	}
	var out []string
	switch r.Kind {
	case navRowSession, navRowWorkspace:
		title := printableTitle(s.Title)
		if s.Host != "" {
			title += " @ " + s.Host
		}
		if r.Kind == navRowWorkspace {
			title += " · workspace " + printableTitle(s.workspaceLabel(r.Workspace))
		}
		out = append(out, st.Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(title, width)), overlay.Rule(width, bg, pal))
		if s.Note != "" {
			out = append(out, line(s.Note+".", pal.FgDim))
		}
		if len(s.Panes) == 0 && s.Note == "" {
			if m.navigator.loading {
				out = append(out, line("Reading its panes…", pal.FgMute))
			} else {
				out = append(out, line(panePlural(s.Count)+".", pal.FgDim))
			}
		}
		for _, p := range s.Panes {
			if r.Kind == navRowWorkspace && p.Workspace != r.Workspace {
				continue
			}
			if len(out) >= height {
				break
			}
			label := printableTitle(p.Name)
			if p.Command != "" && p.Command != p.Name {
				label += "  " + printableTitle(p.Command)
			}
			out = append(out, st.Foreground(pal.FgDim).Render(overlay.Truncate(strconv.Itoa(p.Workspace)+"  "+label, width)))
		}
		return out
	}
	p := &s.Panes[r.Pane]
	head := printableTitle(p.Name)
	if p.Command != "" && p.Command != p.Name {
		head += " · " + printableTitle(p.Command)
	}
	out = append(out, st.Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(head, width)))
	where := printableTitle(s.Title)
	if s.Host != "" {
		where += " @ " + s.Host
	}
	where += " · workspace " + printableTitle(s.workspaceLabel(p.Workspace))
	if p.Cwd != "" {
		where += " · " + printableTitle(navShortPath(p.Cwd))
	}
	out = append(out, line(where, pal.FgMute), overlay.Rule(width, bg, pal))

	text := p.Text
	// A pane this client draws is read live, so the preview follows it.
	if s.Current {
		if i := m.windowIndexByID(p.ID); i >= 0 {
			text = navScreenText(m.Windows[i], navTextLines)
		}
	}
	room := height - len(out)
	if len(text) == 0 {
		msg := "Nothing on the screen yet."
		switch {
		case p.TextSkipped:
			msg = fmt.Sprintf("Not read. The navigator reads the screens of the first %d panes.", navMaxCapturesInForce())
		case m.navigator.loading && !s.Current:
			msg = "Reading the screen…"
		}
		return append(out, line(msg, pal.FgMute))
	}
	if len(text) > room {
		text = text[len(text)-room:]
	}
	for _, t := range text {
		out = append(out, st.Foreground(pal.FgDim).Render(ansi.Truncate(printableRunes(t), width, "")))
	}
	return out
}

// navShortPath writes the home folder as ~. The home folder has to be the
// whole first part of the path: /home/al is not ~ in /home/alex.
func navShortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/"); ok {
		return "~/" + rest
	}
	return p
}
