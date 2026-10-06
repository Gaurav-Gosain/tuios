package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The navigator's frame: one panel as wide and as tall as the screen allows,
// holding three framed boxes. Search runs across the top, Panes lists the
// tree (or the flat list, or the cards) on the left, and Preview shows the
// highlighted pane's screen in its own colours on the right.
//
// The colours carry the hierarchy, so the rows need no headings: a session
// is bold in its own colour (the one the rail gives it), a workspace is the
// secondary ink, a pane's name is the primary ink with its running command in
// the informational one, and the folder, the breadcrumb and the counts are
// quiet. A search lights the characters it matched in the accent.

const (
	// navigatorWidth is the panel width asked for. The screen narrows it.
	navigatorWidth = 160
	// navigatorRowsWanted is the list lines asked for. The screen shortens
	// it.
	navigatorRowsWanted = 40
	// navigatorListMin and navigatorListMax bound the list box's inner width.
	navigatorListMin = 28
	navigatorListMax = 64
	// navigatorPreviewMin is the narrowest preview worth drawing. Below it
	// the list takes the whole panel.
	navigatorPreviewMin = 24
	// navigatorChrome is the body lines that are not list lines: the search
	// box's three and the list box's two borders.
	navigatorChrome = 5
	// navigatorListTop is the body line the first list line is on: under the
	// search box and the list box's top border.
	navigatorListTop = 4
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
	hints := []overlay.Hint{
		{Key: overlay.EnterGlyph, Label: "go"},
		{Key: "/", Label: "search"},
		{Key: "j/k", Label: "move"},
	}
	if !m.navFlat() {
		hints = append(hints, overlay.Hint{Key: "h/l", Label: "fold"})
	}
	return append(hints,
		overlay.Hint{Key: "v", Label: "layout"},
		overlay.Hint{Key: "esc", Label: "close"},
	)
}

// navigatorGeometry is the panel's inner width, the list box's and the
// preview box's inner widths (0 when there is no room for a preview), and
// the list lines the screen has room for.
func (m *OS) navigatorGeometry() (width, listW, previewW, lines int, hints []overlay.Hint) {
	width = m.panelWidth(navigatorWidth)
	lines, hints = m.panelBody(navigatorRowsWanted, navigatorChrome, width, nil, m.navigatorHints())
	listW = max(width-2, 1)
	// Two boxes side by side cost five cells: two borders each and the gap.
	if width-5-navigatorListMin >= navigatorPreviewMin {
		listW = min(max(width*2/5, navigatorListMin), navigatorListMax)
		previewW = width - listW - 5
	}
	return width, listW, previewW, lines, hints
}

// navItemLines is how many list lines one row takes.
func (m *OS) navItemLines() int {
	if m.navCards() {
		return 2
	}
	return 1
}

// navigatorVisibleRows is how many list rows the frame draws.
func (m *OS) navigatorVisibleRows() int {
	_, _, _, lines, _ := m.navigatorGeometry()
	return max(lines/m.navItemLines(), 1)
}

// renderNavigator draws the navigator and returns the panel, its geometry and
// the list rows' hit areas.
func (m *OS) renderNavigator() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	nav := &m.navigator
	width, listW, previewW, lines, hints := m.navigatorGeometry()
	itemH := m.navItemLines()
	visible := max(lines/itemH, 1)
	rows := m.navigatorRows()
	if len(rows) > 0 {
		nav.cursor = clampInt(nav.cursor, 0, len(rows)-1)
	} else {
		nav.cursor = 0
	}
	nav.scroll = scrollWindow(nav.scroll, nav.cursor, len(rows), visible)

	m.navigatorSessionColors()

	// The list box.
	list := make([]string, 0, lines)
	end := min(nav.scroll+visible, len(rows))
	for i := nav.scroll; i < end; i++ {
		st := overlay.RowState{Cursor: i == nav.cursor, Focused: !nav.searching}
		for _, l := range m.navigatorItem(rows[i], st, listW, pal) {
			list = append(list, pal.Row(l, listW, st, bg))
		}
	}
	if len(rows) == 0 {
		msg := "No pane matches"
		if !m.navSearch() {
			msg = "No sessions"
		}
		empty := overlay.Empty{Message: msg, Hint: overlay.Hint{Key: "esc", Label: "close"}}
		list = append(list, empty.Lines(listW, lines, bg, pal)...)
	}
	for len(list) < lines {
		list = append(list, overlay.Style(bg).Render(strings.Repeat(" ", listW)))
	}
	list = list[:lines]
	footer := ""
	if nav.loading {
		footer = "Reading the other sessions…"
	}
	listBox := overlay.Box{
		Title: "Panes", Note: nav.layout, Footer: footer,
		Width: listW, Lines: list, Active: !nav.searching,
	}.Render(bg, pal)

	body := []string{}
	body = append(body, overlay.Box{
		Title: "Search", Note: m.navigatorCount(rows),
		Width: width - 2, Lines: []string{m.navigatorSearchLine(width-2, bg, pal)},
		Active: nav.searching,
	}.Render(bg, pal)...)

	if previewW > 0 {
		var preview []string
		if len(rows) > 0 {
			preview = m.navigatorPreview(rows[nav.cursor], previewW, lines, bg, pal)
		}
		for len(preview) < lines {
			preview = append(preview, "")
		}
		previewBox := overlay.Box{Title: "Preview", Width: previewW, Lines: preview[:lines]}.Render(bg, pal)
		gap := overlay.Style(bg).Render(" ")
		for i := range listBox {
			body = append(body, listBox[i]+gap+previewBox[i])
		}
	} else {
		body = append(body, listBox...)
	}

	panel := overlay.Panel{
		Title: "Navigator",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	hits := make([]overlayRowHit, 0, end-nav.scroll)
	for i := nav.scroll; i < end; i++ {
		y := geo.BodyY + navigatorListTop + (i-nav.scroll)*itemH
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.BodyX + 1 + listW, Y1: y + itemH},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// navigatorSessionColors settles the session colours for the sessions the
// navigator lists, as the rail does for its own, so a session wears the
// same colour in both.
func (m *OS) navigatorSessionColors() {
	names := make([]string, 0, len(m.navigator.sessions))
	for _, s := range m.navigator.sessions {
		if s.Host == "" {
			names = append(names, s.Name)
		}
	}
	m.refreshSessionColors(names)
}

// navigatorCount is the search box's count: the panes listed over the panes
// known, as in "3/7".
func (m *OS) navigatorCount(rows []navRow) string {
	total := 0
	for _, s := range m.navigator.sessions {
		total += max(len(s.Panes), s.Count)
	}
	if !m.navSearch() {
		return fmt.Sprintf("%d/%d", total, total)
	}
	n := 0
	for _, r := range rows {
		if r.Kind == navRowPane {
			n++
		} else {
			n += m.navigator.sessions[r.Session].Count
		}
	}
	return fmt.Sprintf("%d/%d", n, total)
}

// navigatorSearchLine is the search line: the query and a cursor while the
// keyboard is in it, else what / searches.
func (m *OS) navigatorSearchLine(width int, bg color.Color, pal overlay.Palette) string {
	nav := &m.navigator
	st := overlay.Style(bg)
	sigil := st.Render(" ") + st.Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).Render(overlay.Sigil())
	room := max(width-3, 1)
	switch {
	case nav.searching && nav.query == "":
		return sigil + overlay.Cursor(" ", bg, pal.Fg) + st.Foreground(pal.FgMute).Render(overlay.Truncate("Type a name, a folder, a command or screen text", max(room-1, 1)))
	case nav.searching:
		return sigil + st.Foreground(pal.Fg).Render(overlay.Truncate(nav.query, max(room-1, 1))) + overlay.Cursor(" ", bg, pal.Fg)
	case nav.query != "":
		return sigil + st.Foreground(pal.Fg).Render(overlay.Truncate(nav.query, room))
	}
	return sigil + st.Foreground(pal.FgMute).Render(overlay.Truncate("Press / to search names, folders, commands and screen text", room))
}

// navInks are the inks of one list row, each measured on the row's ground.
type navInks struct {
	ground                   color.Color
	fg, dim, mute, edge      color.Color
	command, current, accent color.Color
	hit                      lipgloss.Style
}

// navRowInks are the inks for a row drawn on ground.
func navRowInks(ground color.Color, pal overlay.Palette) navInks {
	return navInks{
		ground: ground,
		// The cursor row's ground is tinted past the palette's own steps
		// (see navGround), so the inks are measured again on it.
		fg:   overlay.ReadableAt(pal.Fg, ground, overlay.ContrastFloor),
		dim:  overlay.ReadableAt(pal.FgDim, ground, overlay.ContrastFloor),
		mute: overlay.ReadableAt(pal.FgMute, ground, overlay.MarkFloor),
		// The guides are structure, but they are read: a row's place in the
		// tree is what they say. The quiet ink, not the frame's.
		edge:    overlay.ReadableAt(pal.FgMute, ground, overlay.MarkFloor),
		command: theme.Readable(pal.Info, ground),
		current: theme.Readable(pal.Success, ground),
		accent:  theme.Readable(pal.Accent, ground),
		// At 16 colours the accent may be a slot close to the name's own
		// ink, so a match is underlined as well as bold there.
		hit: overlay.Style(ground).Foreground(theme.Readable(pal.AccentBright, ground)).Bold(true).Underline(pal.Depth == overlay.Depth16),
	}
}

// navCursorTint is how far the cursor row's ground is carried toward the
// accent: the palette's cursor step alone is a few percent of lightness,
// which on a dark theme's surface read as no cursor at all. A list without
// the keyboard keeps a quieter tint.
const (
	navCursorTint      = 0.22
	navCursorTintQuiet = 0.10
)

// navGround is the ground of a list row in state st: the palette's ground,
// and on the cursor row that ground tinted toward the accent. At 16 colours
// the row is the palette's (pal.Row makes the cursor reverse video there).
func navGround(st overlay.RowState, pal overlay.Palette) color.Color {
	g := pal.Ground(st, pal.Surface)
	if pal.Depth == overlay.Depth16 || !st.Cursor {
		return g
	}
	w := navCursorTint
	if !st.Focused {
		w = navCursorTintQuiet
	}
	c := overlay.MixColors(g, pal.Accent, w)
	if pal.Depth == overlay.Depth256 {
		c = overlay.Apart256(c, pal.Surface)
	}
	return c
}

// ink is a style in c on the row's ground.
func (k navInks) ink(c color.Color) lipgloss.Style { return overlay.Style(k.ground).Foreground(c) }

// lit draws text in base with the bytes at match in the hit style.
func (k navInks) lit(text string, base lipgloss.Style, match []int) string {
	if len(match) == 0 {
		return base.Render(text)
	}
	var out, run strings.Builder
	hot := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if hot {
			out.WriteString(k.hit.Render(run.String()))
		} else {
			out.WriteString(base.Render(run.String()))
		}
		run.Reset()
	}
	mi := 0
	for i, r := range text {
		for mi < len(match) && match[mi] < i {
			mi++
		}
		on := mi < len(match) && match[mi] == i
		if on != hot {
			flush()
			hot = on
		}
		run.WriteRune(r)
	}
	flush()
	return out.String()
}

// navMatch is where the search lights text: its fuzzy match, or for screen
// text, every place it holds the query whole.
func (m *OS) navMatch(text string, whole bool) []int {
	if !m.navSearch() || text == "" {
		return nil
	}
	q := strings.TrimSpace(m.navigator.query)
	if whole {
		lower := strings.ToLower(text)
		lq := strings.ToLower(q)
		if len(lower) != len(text) || lq == "" {
			return nil
		}
		var out []int
		for from := 0; ; {
			i := strings.Index(lower[from:], lq)
			if i < 0 {
				return out
			}
			for b := from + i; b < from+i+len(lq); b++ {
				out = append(out, b)
			}
			from += i + len(lq)
		}
	}
	if r, ok := fuzzy.Find(q, text); ok {
		return r.Positions
	}
	return nil
}

// navTreeGlyphs are the tree's guides: the branch, the last branch, the
// line that carries a branch past a row, and the blank as wide.
func (m *OS) navTreeGlyphs() (branch, last, pipe, blank string) {
	branch, last = m.Settings.GetRailTreeBranch(), m.Settings.GetRailTreeLast()
	w := lipgloss.Width(branch)
	line := "│"
	if r := []rune(branch); len(r) > 0 {
		switch r[0] {
		case '├', '└':
		case '┣', '┗':
			line = "┃"
		default:
			line = "|"
		}
	}
	if overlay.UseASCII() {
		line = "|"
	}
	pipe = line + strings.Repeat(" ", max(w-1, 0))
	blank = strings.Repeat(" ", w)
	return branch, last, pipe, blank
}

// navTreePlace says where a row sits among its siblings: whether its
// workspace is the session's last, and whether the pane is its workspace's
// last.
func navTreePlace(s *navSession, r navRow) (wsLast, paneLast bool) {
	ws := s.workspaces()
	wsLast = len(ws) > 0 && ws[len(ws)-1] == r.Workspace
	if r.Kind != navRowPane {
		return wsLast, false
	}
	paneLast = true
	for pi := r.Pane + 1; pi < len(s.Panes); pi++ {
		if s.Panes[pi].Workspace == r.Workspace {
			paneLast = false
			break
		}
	}
	return wsLast, paneLast
}

// navigatorItem draws one row of the list as its lines: one, or two for a
// card. Each line is drawn on the row's ground, the whole width across, and
// left for pal.Row to finish.
//
// The tree is drawn the way tree(1) draws it. A session starts at the left
// edge, its workspaces hang from a trunk under its first cell, and a
// workspace's panes hang from a trunk under its own first cell. A trunk runs
// down past a row only while a later sibling follows it.
func (m *OS) navigatorItem(r navRow, st overlay.RowState, width int, pal overlay.Palette) []string {
	k := navRowInks(navGround(st, pal), pal)
	bar := k.ink(k.accent).Render(" ")
	if st.Cursor {
		bar = k.ink(k.accent).Render(m.Settings.GetRailFocusMark())
	}
	bar += k.ink(k.fg).Render(" ")
	s := &m.navigator.sessions[r.Session]
	switch {
	case r.Kind == navRowSession:
		left, right := m.navSessionSpans(s, k)
		if m.navCards() {
			return []string{
				navSpans(width, bar+left, "", k),
				navSpans(width, bar+k.ink(k.mute).Render(m.navSessionFacts(s)), "", k),
			}
		}
		return []string{navSpans(width, bar+left, right, k)}
	case r.Kind == navRowWorkspace:
		branch, last, _, _ := m.navTreeGlyphs()
		wsLast, _ := navTreePlace(s, r)
		guide := branch
		if wsLast {
			guide = last
		}
		n := 0
		for _, p := range s.Panes {
			if p.Workspace == r.Workspace {
				n++
			}
		}
		left := k.ink(k.edge).Render(guide) + m.navWorkspaceLabel(s, r.Workspace, k)
		return []string{navSpans(width, bar+left, k.ink(k.mute).Render(strconv.Itoa(n)), k)}
	}

	p := &s.Panes[r.Pane]
	lead := ""
	if !m.navFlat() {
		branch, last, pipe, blank := m.navTreeGlyphs()
		wsLast, paneLast := navTreePlace(s, r)
		outer, inner := pipe, branch
		if wsLast {
			outer = blank
		}
		if paneLast {
			inner = last
		}
		lead = k.ink(k.edge).Render(outer + inner)
	}
	name, byFolder := m.navPaneName(s, p, st.Cursor, k)
	right := ""
	switch {
	case r.Snippet != "":
		snip := overlay.Truncate(printableTitle(r.Snippet), max(width/2, 8))
		right = k.lit(snip, k.ink(k.dim).Italic(true), m.navMatch(snip, true))
	case p.Focused && s.Current:
		right = k.ink(k.current).Render("current")
	}
	folder := ""
	if p.Cwd != "" && !byFolder {
		folder = k.ink(k.mute).Render("  " + printableTitle(navFolder(p.Cwd)))
	}
	if m.navCards() {
		second := k.ink(k.fg).Render("  ") + m.navBreadcrumb(s, p.Workspace, k) + folder
		return []string{
			navSpans(width, bar+name, right, k),
			navSpans(width, bar+second, "", k),
		}
	}
	left := bar + lead + name
	if m.navFlat() {
		left += k.ink(k.mute).Render("  ") + m.navBreadcrumb(s, p.Workspace, k)
	} else {
		left += folder
	}
	return []string{navSpans(width, left, right, k)}
}

// navSessionSpans is a session row: its name bold in its own colour and its
// machine, and on the right what is known of it.
func (m *OS) navSessionSpans(s *navSession, k navInks) (left, right string) {
	tint := k.fg
	if c := m.sessionTint(navTintKey(s), k.ground); c != nil {
		tint = c
	}
	title := printableTitle(s.Title)
	left = k.lit(title, k.ink(tint).Bold(true), m.navMatch(title, false))
	if s.Host != "" {
		left += k.ink(k.mute).Render(" @ ") + k.ink(k.dim).Render(printableTitle(s.Host))
	}
	switch {
	case s.Note != "":
		right = k.ink(k.mute).Italic(true).Render(s.Note)
	case s.Current:
		right = k.ink(k.current).Render("current") + k.ink(k.mute).Render("  "+strconv.Itoa(s.Count))
	default:
		right = k.ink(k.mute).Render(strconv.Itoa(s.Count))
	}
	return left, right
}

// navSessionFacts is a session's second card line: why its panes are not
// listed, or how many it has.
func (m *OS) navSessionFacts(s *navSession) string {
	if s.Note != "" {
		return "  " + s.Note
	}
	return "  " + panePlural(s.Count)
}

// navWorkspaceLabel is "workspace 2" and the workspace's name, if it has one.
func (m *OS) navWorkspaceLabel(s *navSession, ws int, k navInks) string {
	out := k.ink(k.mute).Render("workspace ") + k.ink(k.dim).Bold(true).Render(strconv.Itoa(ws))
	if name := printableTitle(s.WorkspaceNames[ws]); name != "" {
		out += k.ink(k.mute).Render(" · ") + k.ink(k.dim).Render(name)
	}
	return out
}

// navPaneLabel is what a pane's row calls it. A name the person gave it or a
// title its program set comes first. A pane with only the name tuios made
// up ("Terminal 1a2b3c4d") is called by its folder instead, and byFolder
// says so, so the row does not show the folder twice. With no folder known
// the made-up name stands, and quiet says to draw it in the quiet ink.
func navPaneLabel(p *navPane) (label string, byFolder, quiet bool) {
	name := printableTitle(p.Name)
	if name != "" && !isDefaultTitle(p.Name, p.ID) {
		return name, false, false
	}
	if t := printableTitle(p.Title); t != "" && !isDefaultTitle(p.Title, p.ID) {
		return t, false, false
	}
	if p.Cwd != "" {
		return printableTitle(navFolder(p.Cwd)), true, false
	}
	if name == "" {
		name = "pane"
	}
	return name, false, true
}

// navPaneCommand is what a pane is running, for its row: the foreground
// command, or the session's shell at a prompt. An ssh session says where it
// went when the pane's shell reported the command line.
func navPaneCommand(s *navSession, p *navPane) string {
	cmd := printableTitle(p.Command)
	if cmd == "" && p.AgentState == "" {
		cmd = printableTitle(s.Shell)
	}
	if cmd == "ssh" {
		if host := navSSHTarget(p.Cmdline); host != "" {
			cmd += " " + printableTitle(host)
		}
	}
	return cmd
}

// navSSHTarget is the destination of an ssh command line: its first word
// after ssh that is not an option or an option's value.
func navSSHTarget(cmdline string) string {
	f := strings.Fields(cmdline)
	if len(f) == 0 || filepath.Base(f[0]) != "ssh" {
		return ""
	}
	// The options of ssh(1) that take a value.
	const valued = "BbcDEeFIiJLlmOopQRSWw"
	for i := 1; i < len(f); i++ {
		a := f[i]
		if strings.HasPrefix(a, "-") {
			if len(a) == 2 && strings.ContainsRune(valued, rune(a[1])) {
				i++
			}
			continue
		}
		if at := strings.LastIndex(a, "@"); at >= 0 {
			a = a[at+1:]
		}
		return a
	}
	return ""
}

// navPaneName is a pane's mark, label and command: the agent state's mark in
// its colour (a blank cell when there is no agent), the label, and the
// running command after a rule in the informational ink. byFolder is
// navPaneLabel's.
func (m *OS) navPaneName(s *navSession, p *navPane, selected bool, k navInks) (string, bool) {
	mark := k.ink(k.fg).Render("  ")
	if glyph, c := agentMark(p.AgentState, p.DoneSeen, theme.UI()); glyph != "" {
		mark = k.ink(theme.Readable(c, k.ground)).Bold(sidebarAttention(p.AgentState)).Render(glyph) + k.ink(k.fg).Render(" ")
	}
	label, byFolder, quiet := navPaneLabel(p)
	ink := k.fg
	if quiet {
		ink = k.mute
	}
	out := mark + k.lit(label, k.ink(ink).Bold(selected && !quiet), m.navMatch(label, false))
	if cmd := navPaneCommand(s, p); cmd != "" && !strings.EqualFold(cmd, label) {
		out += k.ink(k.edge).Render(" │ ") + k.lit(cmd, k.ink(k.command), m.navMatch(cmd, false))
	}
	return out, byFolder
}

// navBreadcrumb is where a pane is: its session in the session's colour, its
// machine, and its workspace, as "work › 2 logs".
func (m *OS) navBreadcrumb(s *navSession, ws int, k navInks) string {
	tint := k.dim
	if c := m.sessionTint(navTintKey(s), k.ground); c != nil {
		tint = c
	}
	title := printableTitle(s.Title)
	out := k.lit(title, k.ink(tint), m.navMatch(title, false))
	if s.Host != "" {
		out += k.ink(k.mute).Render(" @ ") + k.ink(k.dim).Render(printableTitle(s.Host))
	}
	label := printableTitle(s.workspaceLabel(ws))
	return out + k.ink(k.mute).Render(" › ") + k.lit(label, k.ink(k.mute), m.navMatch(label, false))
}

// navSpans lays a row out to width: the left part, then the right part
// against the right edge. The left part gives way first: the right part is
// the row's facts.
func navSpans(width int, left, right string, k navInks) string {
	rw := lipgloss.Width(right)
	if rw > 0 && rw > width/2 {
		right = ansi.Truncate(right, width/2, overlay.Ellipsis())
		rw = lipgloss.Width(right)
	}
	avail := width - rw - 1
	if rw == 0 {
		avail = width
	}
	if lipgloss.Width(left) > avail {
		ell := k.ink(k.mute).Render(overlay.Ellipsis())
		left = ansi.Truncate(left, max(avail-lipgloss.Width(ell), 0), "") + ell
	}
	gap := max(width-lipgloss.Width(left)-rw, 0)
	return left + k.ink(k.fg).Render(strings.Repeat(" ", gap)) + right
}

// navigatorPreview is the preview box's lines for a row: a pane's screen
// under a header that names it, or a session's or a workspace's panes as a
// tree.
func (m *OS) navigatorPreview(r navRow, width, height int, bg color.Color, pal overlay.Palette) []string {
	s := &m.navigator.sessions[r.Session]
	k := navRowInks(bg, pal)
	pad := k.ink(k.fg).Render(" ")
	line := func(text string, ink color.Color) string {
		return pad + k.ink(ink).Render(overlay.Truncate(text, max(width-2, 1)))
	}
	var out []string
	if r.Kind != navRowPane {
		left, right := m.navSessionSpans(s, k)
		if r.Kind == navRowWorkspace {
			left += k.ink(k.mute).Render(" › ") + m.navWorkspaceLabel(s, r.Workspace, k)
		}
		out = append(out, pad+navSpans(width-2, left, right, k), pad+overlay.DashRule(width-2, bg, pal))
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
		// The session's panes as the tree draws them, under their
		// workspaces.
		for _, ws := range s.workspaces() {
			if r.Kind == navRowWorkspace && ws != r.Workspace {
				continue
			}
			if len(out) >= height {
				break
			}
			if r.Kind == navRowSession {
				out = append(out, pad+navSpans(width-2, m.navWorkspaceLabel(s, ws, k), "", k))
			}
			for pi, p := range s.Panes {
				if p.Workspace != ws || len(out) >= height {
					continue
				}
				item := m.navigatorPreviewPane(navRow{Kind: navRowPane, Session: r.Session, Workspace: ws, Pane: pi}, width-2, pal)
				out = append(out, pad+item)
			}
		}
		return out
	}

	p := &s.Panes[r.Pane]
	head, _ := m.navPaneName(s, p, true, k)
	out = append(out, pad+navSpans(width-2, head, m.navBreadcrumb(s, p.Workspace, k), k))
	where := ""
	if p.Cwd != "" {
		where = printableTitle(navShortPath(p.Cwd))
	}
	facts := ""
	if p.Focused && s.Current {
		facts = k.ink(k.current).Render("current")
	}
	out = append(out, pad+navSpans(width-2, k.ink(k.mute).Render("  "+where), facts, k))
	out = append(out, pad+overlay.DashRule(width-2, bg, pal))

	room := height - len(out)
	screen := m.navigatorScreen(s, p, width-2, room)
	if len(screen) == 0 {
		msg := "Nothing on the screen yet."
		switch {
		case p.TextSkipped:
			msg = fmt.Sprintf("Not read. The navigator reads the screens of the first %d panes.", navMaxCapturesInForce())
		case m.navigator.loading && !s.Current:
			msg = "Reading the screen…"
		}
		return append(out, line(msg, pal.FgMute))
	}
	// The screen runs to the foot of the box, so it reads as a screen and
	// not as a few lines of text.
	for len(screen) < room {
		screen = append(screen, navScreenLine("", width-2))
	}
	for _, l := range screen {
		out = append(out, pad+l+pad)
	}
	return out
}

// navigatorPreviewPane is one pane of a session's preview, drawn as a tree
// row with its guides.
func (m *OS) navigatorPreviewPane(r navRow, width int, pal overlay.Palette) string {
	s := &m.navigator.sessions[r.Session]
	p := &s.Panes[r.Pane]
	k := navRowInks(pal.Surface, pal)
	branch, last, _, _ := m.navTreeGlyphs()
	_, paneLast := navTreePlace(s, r)
	guide := branch
	if paneLast {
		guide = last
	}
	name, byFolder := m.navPaneName(s, p, false, k)
	left := k.ink(k.edge).Render(guide) + name
	if p.Cwd != "" && !byFolder {
		left += k.ink(k.mute).Render("  " + printableTitle(navFolder(p.Cwd)))
	}
	right := ""
	if p.Focused && s.Current {
		right = k.ink(k.current).Render("current")
	}
	return navSpans(width, left, right, k)
}

// navigatorScreen is the pane's screen for the preview, rows lines of width
// cells at most, each on the pane's own ground: read live from this client's
// emulator for a pane it draws, else the styled text the load read.
func (m *OS) navigatorScreen(s *navSession, p *navPane, width, rows int) []string {
	if rows <= 0 {
		return nil
	}
	if s.Current {
		if i := m.windowIndexByID(p.ID); i >= 0 {
			w := m.Windows[i]
			// The lock is not waited for: a pane flooding output holds it,
			// and the frame that waits is the one carrying the keystrokes.
			// The text read when the navigator opened stands in.
			if w.Terminal != nil && w.TryRLockIO() {
				body := pipCells(w.Terminal, width, min(rows, w.Terminal.Height()))
				w.RUnlockIO()
				lines := strings.Split(body, "\n")
				for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
					lines = lines[:len(lines)-1]
				}
				for i, l := range lines {
					lines[i] = navScreenLine(l, width)
				}
				return lines
			}
		}
	}
	text := p.Styled
	if len(text) == 0 {
		// A plain row is drawn as styled text with no styling, so it takes
		// the same path. Text holds no escape: it was read from cells.
		text = p.Text
	}
	if len(text) > rows {
		text = text[len(text)-rows:]
	}
	out := make([]string, len(text))
	for i, t := range text {
		out[i] = navScreenLine(t, width)
	}
	return out
}

// navTintKey is the name a session's colour is kept under: its name, or for
// a session on another machine the id the rail gives its row, so the rail and
// the navigator give a session the same colour.
func navTintKey(s *navSession) string {
	if s.Host == "" {
		return s.Name
	}
	return hostNodeID(s.Host) + ":" + s.Name
}

// navFolder is a folder short enough for a list row: the home folder as ~,
// and only the last two parts of a deep path.
func navFolder(p string) string {
	p = navShortPath(p)
	parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
	if len(parts) <= 3 {
		return p
	}
	return overlay.Ellipsis() + "/" + strings.Join(parts[len(parts)-2:], "/")
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
