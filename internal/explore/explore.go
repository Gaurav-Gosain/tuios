// Package explore is the small full-screen browser behind tuios's opt-in
// explorers: tuios keybinds browse, tuios config browse and tuios help -i.
//
// An explorer is a searchable list with a detail pane under it. Its rows come
// from the same data the plain command prints with --json, so the explorer
// can never show something a script cannot read. It opens only when the
// person asks for it by flag or subcommand. Nothing here checks for a TTY to
// decide that: an agent in a pane has a TTY too, and must never be trapped in
// a program it did not ask for.
//
// The look follows the overlay family: one Panel filling the screen, its
// colours from theme.UI, the list rows through Palette.Row, and the filter
// groups as the panel's tabs.
package explore

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// Item is one row of an explorer.
type Item struct {
	// Name is the row's first column: a key, an option path, a command.
	Name string
	// Note is the row's second column, drawn dimmer.
	Note string
	// Group is the filter tab the row belongs to. Empty is in no tab but All.
	Group string
	// Detail is the detail pane for the row, one string per paragraph. A
	// paragraph of the form "Label: value" draws its label dimmer.
	Detail []string
	// Search is extra text the search matches besides Name, Note and Detail.
	Search string
	// Key identifies the row to Edit.
	Key string
	// Indent is the row's depth in a tree, drawn as two cells a level.
	Indent int
	// FullName replaces Name, and the indent goes, while a search is open:
	// a match out of its tree needs its whole path to be read.
	FullName string
}

// Edit sets a value from the explorer. Start returns the text the input
// starts with, or ok false when the row cannot be set. Apply sets it and
// returns the row as it is now and a line to show. An error is shown as it
// is, and the row stays as it was.
type Edit struct {
	Start func(it Item) (value string, ok bool)
	Apply func(it Item, value string) (Item, string, error)
}

// Config describes one explorer.
type Config struct {
	Title string
	Items []Item
	// Groups are the filter tabs after All, in order. Empty means no tabs.
	Groups []string
	// NameWidth caps the first column. Zero is a third of the width.
	NameWidth int
	// Edit, when set, makes enter on a row open an input that sets it.
	Edit *Edit
	// Query starts the search with this text.
	Query string
}

// Run opens the explorer and returns when the person leaves it.
func Run(cfg Config) error {
	m := newModel(cfg)
	_, err := tea.NewProgram(m).Run()
	return err
}

type focus int

const (
	focusList focus = iota
	focusSearch
	focusEdit
)

type model struct {
	cfg    Config
	w, h   int
	group  int // 0 is All
	query  string
	focus  focus
	shown  []int // indexes into cfg.Items that pass the filter
	cursor int   // index into shown
	top    int   // first shown row on screen
	hover  int   // shown index under the pointer, -1 for none
	detail int   // first detail line on screen
	input  string
	status string
	failed bool

	// Where the last frame drew things, for the mouse.
	geo      overlay.Geometry
	listY    int
	listRows int
	detailY  int
	detailN  int
}

func newModel(cfg Config) *model {
	m := &model{cfg: cfg, w: 80, h: 24, hover: -1, query: cfg.Query}
	m.refilter()
	return m
}

func (m *model) Init() tea.Cmd { return nil }

// tabs is the tab strip: All, then the groups.
func (m *model) tabs() []string {
	if len(m.cfg.Groups) == 0 {
		return nil
	}
	return append([]string{"All"}, m.cfg.Groups...)
}

// refilter recomputes the rows that pass the group and the search.
func (m *model) refilter() {
	var keep string
	if m.cursor < len(m.shown) {
		keep = m.cfg.Items[m.shown[m.cursor]].Name + "\x00" + m.cfg.Items[m.shown[m.cursor]].Group
	}
	terms := strings.Fields(strings.ToLower(m.query))
	m.shown = m.shown[:0]
	for i, it := range m.cfg.Items {
		if m.group > 0 && it.Group != m.cfg.Groups[m.group-1] {
			continue
		}
		if len(terms) > 0 {
			hay := strings.ToLower(it.Name + " " + it.Note + " " + it.Group + " " + it.Search + " " + strings.Join(it.Detail, " "))
			ok := true
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		m.shown = append(m.shown, i)
	}
	if len(terms) > 0 {
		// The best matches first: a name that is the search, then one that
		// starts with it, then one that has it, then the rest.
		q := strings.ToLower(strings.TrimSpace(m.query))
		rank := func(i int) int {
			it := m.cfg.Items[i]
			for _, n := range []string{it.Name, it.FullName} {
				n = strings.ToLower(n)
				if n == "" {
					continue
				}
				if n == q || strings.HasSuffix(n, " "+q) || strings.HasSuffix(n, "."+q) {
					return 0
				}
			}
			n := strings.ToLower(it.Name)
			switch {
			case strings.HasPrefix(n, q):
				return 1
			case strings.Contains(n, q) || strings.Contains(strings.ToLower(it.FullName), q):
				return 2
			}
			return 3
		}
		slices.SortStableFunc(m.shown, func(a, b int) int { return rank(a) - rank(b) })
	}
	m.cursor = 0
	for j, i := range m.shown {
		if m.cfg.Items[i].Name+"\x00"+m.cfg.Items[i].Group == keep {
			m.cursor = j
			break
		}
	}
	m.detail = 0
	m.status = ""
	m.clamp()
}

func (m *model) clamp() {
	if m.cursor >= len(m.shown) {
		m.cursor = len(m.shown) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	rows := max(m.listRows, 1)
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+rows {
		m.top = m.cursor - rows + 1
	}
	m.top = max(min(m.top, len(m.shown)-rows), 0)
}

func (m *model) move(d int) {
	m.cursor += d
	m.detail = 0
	m.status = ""
	m.clamp()
}

func (m *model) setGroup(g int) {
	n := len(m.tabs())
	if n == 0 {
		return
	}
	m.group = (g%n + n) % n
	m.refilter()
}

// current is the row under the cursor.
func (m *model) current() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.shown) {
		return Item{}, false
	}
	return m.cfg.Items[m.shown[m.cursor]], true
}

func (m *model) startEdit() {
	it, ok := m.current()
	if !ok || m.cfg.Edit == nil {
		return
	}
	v, ok := m.cfg.Edit.Start(it)
	if !ok {
		m.status, m.failed = "This option cannot be set here. Set it in config.toml.", true
		return
	}
	m.input, m.focus, m.status, m.failed = v, focusEdit, "", false
}

func (m *model) applyEdit() {
	it, ok := m.current()
	m.focus = focusList
	if !ok {
		return
	}
	updated, msg, err := m.cfg.Edit.Apply(it, m.input)
	if err != nil {
		m.status, m.failed = err.Error(), true
		return
	}
	m.cfg.Items[m.shown[m.cursor]] = updated
	m.status, m.failed = msg, false
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		m.clamp()
	case tea.ColorProfileMsg:
		theme.SetColorProfile(msg.Profile)
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		switch m.focus {
		case focusSearch:
			m.query += oneLine(msg.Content)
			m.refilter()
		case focusEdit:
			m.input += oneLine(msg.Content)
		}
	case tea.MouseClickMsg:
		m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		mo := msg.Mouse()
		d := 3
		if mo.Button == tea.MouseWheelUp {
			d = -3
		}
		if m.detailN > 0 && mo.Y >= m.detailY && mo.Y < m.detailY+m.detailN {
			m.detail = max(m.detail+d, 0)
		} else {
			m.move(d)
		}
	case tea.MouseMotionMsg:
		mo := msg.Mouse()
		m.hover = -1
		if mo.Y >= m.listY && mo.Y < m.listY+m.listRows {
			if i := m.top + mo.Y - m.listY; i < len(m.shown) {
				m.hover = i
			}
		}
	}
	return m, nil
}

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func (m *model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	switch m.focus {
	case focusSearch:
		switch s {
		case "esc":
			m.focus = focusList
			if m.query != "" {
				m.query = ""
				m.refilter()
			}
		case "enter", "down", "tab":
			m.focus = focusList
		case "backspace":
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
				m.refilter()
			}
		case "ctrl+u":
			m.query = ""
			m.refilter()
		case "ctrl+c":
			return m, tea.Quit
		default:
			if t := k.Key().Text; t != "" {
				m.query += t
				m.refilter()
			}
		}
		return m, nil
	case focusEdit:
		switch s {
		case "esc":
			m.focus, m.status = focusList, "Nothing was changed."
			m.failed = false
		case "enter":
			m.applyEdit()
		case "backspace":
			if r := []rune(m.input); len(r) > 0 {
				m.input = string(r[:len(r)-1])
			}
		case "ctrl+u":
			m.input = ""
		case "ctrl+c":
			return m, tea.Quit
		default:
			if t := k.Key().Text; t != "" {
				m.input += t
			}
		}
		return m, nil
	}
	switch s {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "/":
		m.focus = focusSearch
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+b":
		m.move(-max(m.listRows-1, 1))
	case "pgdown", "ctrl+f", "space":
		m.move(max(m.listRows-1, 1))
	case "home", "g":
		m.move(-len(m.shown))
	case "end", "G":
		m.move(len(m.shown))
	case "tab", "right", "l":
		m.setGroup(m.group + 1)
	case "shift+tab", "left", "h":
		m.setGroup(m.group - 1)
	case "shift+down", "J":
		m.detail++
	case "shift+up", "K":
		m.detail = max(m.detail-1, 0)
	case "enter", "e":
		m.startEdit()
	}
	return m, nil
}

func (m *model) click(mo tea.Mouse) {
	if mo.Button != tea.MouseLeft {
		return
	}
	for i, r := range m.geo.Tabs {
		if r.Contains(mo.X, mo.Y) {
			m.setGroup(i)
			return
		}
	}
	if m.geo.TabPrev.Contains(mo.X, mo.Y) {
		m.setGroup(m.group - 1)
		return
	}
	if m.geo.TabNext.Contains(mo.X, mo.Y) {
		m.setGroup(m.group + 1)
		return
	}
	if mo.Y == m.geo.BodyY {
		if m.focus != focusEdit {
			m.focus = focusSearch
		}
		return
	}
	if mo.Y >= m.listY && mo.Y < m.listY+m.listRows {
		i := m.top + mo.Y - m.listY
		if i >= len(m.shown) {
			return
		}
		if m.focus == focusEdit {
			m.focus = focusList
		}
		if i == m.cursor && m.cfg.Edit != nil {
			m.startEdit()
			return
		}
		m.cursor, m.detail, m.status = i, 0, ""
		m.focus = focusList
		m.clamp()
	}
}

// Fixed rows of the panel around its body: top pad, title, blank, the tab
// strip and its rule and blank, then blank, rule and the hint rows, and the
// bottom pad.
func (m *model) chromeRows() int {
	n := 1 + 1 + 1 + 1
	if len(m.tabs()) > 0 {
		n += 3
	}
	n += 2 + overlay.HintRowCount(m.hints(), m.innerWidth())
	return n
}

func (m *model) innerWidth() int {
	return max(m.w-2*overlay.DefaultPanelPadding, 10)
}

// layout splits the body: the search row, a blank, the list, a rule and the
// detail pane.
func (m *model) layout() {
	body := max(m.h-m.chromeRows(), 4)
	detail := min(max(body/3, 3), 12)
	if body-detail-3 < 3 {
		detail = max(body-6, 1)
	}
	m.listRows = max(body-detail-3, 1)
	m.detailN = detail
}

func (m *model) hints() []overlay.Hint {
	switch m.focus {
	case focusSearch:
		return []overlay.Hint{{Key: "enter", Label: "done"}, {Key: "esc", Label: "clear"}}
	case focusEdit:
		return []overlay.Hint{{Key: "enter", Label: "set"}, {Key: "esc", Label: "cancel"}}
	}
	h := []overlay.Hint{{Key: "/", Label: "search"}, {Key: "up/down", Label: "move"}}
	if len(m.tabs()) > 0 {
		h = append(h, overlay.Hint{Key: "tab", Label: "filter"})
	}
	if m.cfg.Edit != nil {
		h = append(h, overlay.Hint{Key: "enter", Label: "set"})
	}
	h = append(h, overlay.Hint{Key: "J/K", Label: "scroll detail", Priority: overlay.HintOptional})
	return append(h, overlay.Hint{Key: "q", Label: "quit", Priority: overlay.HintEssential})
}

func (m *model) View() tea.View {
	m.layout()
	m.clamp()
	pal := theme.UI()
	bg := pal.Surface
	iw := m.innerWidth()
	st := func() lipgloss.Style { return overlay.Style(bg) }

	var b []string

	// The search row, with the count at its right end.
	count := st().Foreground(pal.FgMute).Render(itoa(len(m.shown)) + " of " + itoa(len(m.cfg.Items)))
	var search string
	switch {
	case m.focus == focusSearch:
		search = st().Foreground(pal.Accent).Render("Search: ") + st().Foreground(pal.Fg).Render(m.query) + st().Foreground(pal.Accent).Reverse(true).Render(" ")
	case m.query != "":
		search = st().Foreground(pal.FgDim).Render("Search: ") + st().Foreground(pal.Fg).Render(m.query)
	default:
		search = st().Foreground(pal.FgDim).Render("Press / to search")
	}
	gap := iw - lipgloss.Width(search) - lipgloss.Width(count)
	if gap < 1 {
		search = ansi.Truncate(search, max(iw-lipgloss.Width(count)-1, 0), "")
		gap = max(iw-lipgloss.Width(search)-lipgloss.Width(count), 0)
	}
	b = append(b, search+st().Render(strings.Repeat(" ", gap))+count)
	b = append(b, "")

	// The list.
	nameW := m.cfg.NameWidth
	if nameW == 0 || nameW > iw/2 {
		nameW = max(iw/3, 8)
	}
	for r := 0; r < m.listRows; r++ {
		i := m.top + r
		if i >= len(m.shown) {
			if r == 0 && len(m.shown) == 0 {
				b = append(b, st().Foreground(pal.FgDim).Render("No match. Press esc to clear the search."))
				continue
			}
			b = append(b, "")
			continue
		}
		it := m.cfg.Items[m.shown[i]]
		rs := overlay.RowState{Cursor: i == m.cursor, Focused: m.focus == focusList, Hover: i == m.hover}
		g := pal.Ground(rs, bg)
		rowSt := func() lipgloss.Style { return overlay.Style(g) }
		name := strings.Repeat("  ", it.Indent) + it.Name
		if m.query != "" && it.FullName != "" {
			name = it.FullName
		}
		nameInk := pal.Fg
		if rs.Cursor {
			nameInk = pal.AccentBright
		}
		cell := rowSt().Foreground(nameInk).Bold(rs.Cursor).Render(" " + overlay.Truncate(name, nameW-1))
		cell = overlay.Fill(cell, nameW+1, g)
		note := rowSt().Foreground(pal.FgDim).Render(overlay.Truncate(it.Note, max(iw-nameW-3, 0)))
		row := cell + rowSt().Render(" ") + note
		if lipgloss.Width(row) > iw {
			row = ansi.Truncate(row, iw, "")
		}
		b = append(b, pal.Row(row, iw, rs, bg))
	}

	b = append(b, overlay.Rule(iw, bg, pal))

	// The detail pane, or the input and the status line.
	var lines []string
	it, ok := m.current()
	if m.focus == focusEdit {
		lines = append(lines, st().Foreground(pal.Accent).Render("Set "+it.Name+" to: ")+st().Foreground(pal.Fg).Render(m.input)+st().Foreground(pal.Accent).Reverse(true).Render(" "))
	} else if m.status != "" {
		ink := pal.Success
		if m.failed {
			ink = pal.Warn
		}
		for _, l := range wrap(m.status, iw) {
			lines = append(lines, st().Foreground(ink).Render(l))
		}
	}
	if ok {
		for _, para := range it.Detail {
			label, rest, hasLabel := strings.Cut(para, ": ")
			if !hasLabel || strings.Contains(label, " ") && len(label) > 20 {
				for _, l := range wrap(para, iw) {
					lines = append(lines, st().Foreground(pal.Fg).Render(l))
				}
				continue
			}
			for j, l := range wrap(para, iw) {
				if j == 0 && strings.HasPrefix(l, label+": ") {
					lines = append(lines, st().Foreground(pal.FgDim).Render(label+": ")+st().Foreground(pal.Fg).Render(strings.TrimPrefix(l, label+": ")))
					continue
				}
				lines = append(lines, st().Foreground(pal.Fg).Render(l))
			}
			_ = rest
		}
	}
	m.detail = min(m.detail, max(len(lines)-m.detailN, 0))
	for r := 0; r < m.detailN; r++ {
		if j := m.detail + r; j < len(lines) {
			b = append(b, lines[j])
		} else {
			b = append(b, "")
		}
	}

	p := overlay.Panel{
		Title:     m.cfg.Title,
		Width:     iw,
		Tabs:      m.tabs(),
		ActiveTab: m.group,
		Body:      strings.Join(b, "\n"),
		Hints:     m.hints(),
	}
	out, geo := p.Render(pal)
	m.geo = geo
	m.listY = geo.BodyY + 2
	m.detailY = m.listY + m.listRows + 1

	v := tea.NewView(out)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.BackgroundColor = bg
	v.WindowTitle = m.cfg.Title
	return v
}

// wrap breaks s into lines of at most width cells, at spaces where it can.
func wrap(s string, width int) []string {
	if width < 4 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		w := lipgloss.Wrap(para, width, " ")
		out = append(out, strings.Split(w, "\n")...)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
