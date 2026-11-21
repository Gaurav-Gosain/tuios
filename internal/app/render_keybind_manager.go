package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	keybindInnerWidth  = 74
	keybindVisibleRows = 11
	keybindKeyColumn   = 20
	keybindScopeColumn = 18
)

var keybindHints = []overlay.Hint{
	{Key: "tab", Label: "section"},
	{Key: hintListMove, Label: "move"},
	{Key: "/", Label: "filter"},
	{Key: "ctrl+r", Label: "record"},
	{Key: "ctrl+d", Label: "unbind"},
	{Key: "esc", Label: "close"},
}

type keybindMarks struct {
	dead    string
	unbound string
	clash   string
	ok      string
	live    string
	bullet  string
}

func keybindGlyphs() keybindMarks {
	if overlay.UseASCII() {
		return keybindMarks{dead: "x", unbound: "-", clash: "!", ok: " ", live: "<<", bullet: "-"}
	}
	return keybindMarks{dead: "✕", unbound: "○", clash: "▲", ok: " ", live: "◂", bullet: "·"}
}

type keybindChrome struct {
	header int
	count  int
	detail int
}

func (c keybindChrome) lines() int {
	n := c.header + c.count + c.detail
	if c.detail > 0 {
		n++
	}
	return n
}

var keybindShedOrder = []keybindChrome{
	{header: 2, count: 1, detail: 4},
	{header: 2, count: 1, detail: 3},
	{header: 2, count: 1, detail: 2},
	{header: 2, count: 1, detail: 1},
	{header: 2, count: 1, detail: 0},
	{header: 2, count: 0, detail: 0},
	{header: 0, count: 0, detail: 0},
}

func (m *OS) keybindLayout() (width, rows int, hints []overlay.Hint, chrome keybindChrome) {
	width = m.panelWidth(keybindInnerWidth)
	rh := m.GetRenderHeight()
	for _, c := range keybindShedOrder {
		extra := c.lines()
		rows, hints = m.panelBody(keybindVisibleRows, extra, width, KeybindTabNames, keybindHints)
		if rh <= 0 || rows+extra+panelChrome(0, width, KeybindTabNames, hints) <= rh {
			return width, rows, hints, c
		}
	}
	last := keybindShedOrder[len(keybindShedOrder)-1]
	rows, hints = m.panelBody(keybindVisibleRows, last.lines(), width, KeybindTabNames, keybindHints)
	return width, rows, hints, last
}

func (m *OS) renderKeybindManager() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	width, visible, hints, chrome := m.keybindLayout()

	var lines []string
	var rows []overlayRowHit
	rowYOffset := 0

	switch m.KeybindTab {
	case KeybindTabRecord:
		lines = m.keybindRecordBody(pal, width, visible, chrome)
	default:
		lines, rows, rowYOffset = m.keybindListBody(pal, width, visible, chrome)
	}

	panel := overlay.Panel{
		Title:     "Keybinds",
		Width:     width,
		Tabs:      KeybindTabNames,
		ActiveTab: m.KeybindTab,
		Body:      strings.Join(lines, "\n"),
		Hints:     hints,
	}
	content, geo := panel.Render(pal)

	for i := range rows {
		rows[i].Rect.Y0 += geo.BodyY + rowYOffset
		rows[i].Rect.Y1 += geo.BodyY + rowYOffset
		rows[i].Rect.X1 = geo.Width
	}
	return content, geo, rows
}

func (m *OS) keybindListBody(pal overlay.Palette, width, visible int, chrome keybindChrome) ([]string, []overlayRowHit, int) {
	bg := pal.Surface
	var lines []string

	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	query := m.KeybindQuery()
	prompt := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ")
	if m.KeybindTab != KeybindTabBindings {
		prompt = overlay.Style(bg).Foreground(pal.FgMute).Render("  ")
		query = m.keybindTabSubtitle()
		cursor = ""
	}
	if chrome.header > 0 {
		filter := overlay.Style(bg).Foreground(pal.Fg).Render(query) + cursor
		if m.KeybindTab == KeybindTabBindings {
			before, at, after := m.keybinds.lists[KeybindTabBindings].Query.Parts()
			filter = overlay.FieldLine(before, at, after, max(width-lipgloss.Width(prompt), 1), bg, pal.Fg)
		}
		lines = append(lines,
			prompt+filter,
			overlay.Rule(width, bg, pal),
		)
	}
	rowYOffset := len(lines)

	count := m.keybindRowCount()
	list := m.keybindList()
	list.Fit(count, visible)
	selected, start := list.Cursor, list.Scroll
	end := min(start+visible, count)

	var rows []overlayRowHit
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.keybindRow(i, i == selected, pal, width))
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: len(lines) - 1 - rowYOffset, X1: width, Y1: len(lines) - rowYOffset},
			Idx:  i,
		})
		shown++
	}
	if count == 0 {
		lines = append(lines, overlay.Empty{Message: m.keybindEmptyMessage(), Hint: overlay.Hint{Key: "esc", Label: "close"}}.Lines(width, visible, bg, pal)...)
		shown = visible
	}
	for shown < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	if chrome.count > 0 {
		countLine := " "
		if count > 0 {
			countLine = "  " + lipgloss.Sprintf("%d of %d", selected+1, count)
		}
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render(countLine))
	}

	if chrome.detail > 0 {
		lines = append(lines, overlay.Rule(width, bg, pal))
		lines = append(lines, settingsDescription(m.keybindDetail(selected), width, chrome.detail, pal)...)
	}

	return lines, rows, rowYOffset
}

func (m *OS) keybindTabSubtitle() string {
	rep := m.KeybindReport()
	switch m.KeybindTab {
	case KeybindTabConflicts:
		if len(rep.Collisions) == 0 {
			return "No key is used twice in the same scope"
		}
		return "Keys used twice. ctrl+d removes the bindings that never run."
	case KeybindTabGuests:
		if len(rep.GuestClashes) == 0 {
			return "No program needs a key that tuios uses"
		}
		return "Keys tuios uses that a program also wants"
	}
	return ""
}

func (m *OS) keybindEmptyMessage() string {
	switch m.KeybindTab {
	case KeybindTabConflicts:
		return "No conflicts. Each key does one thing."
	case KeybindTabGuests:
		return "No program needs a key that tuios uses."
	}
	if m.KeybindQuery() != "" {
		return "No binding matches that filter"
	}
	return "No bindings configured"
}

func (m *OS) keybindRow(i int, selected bool, pal overlay.Palette, width int) string {
	var row string
	switch m.KeybindTab {
	case KeybindTabConflicts:
		row = m.keybindConflictRow(i, selected, pal, width)
	case KeybindTabGuests:
		row = m.keybindGuestRow(i, selected, pal, width)
	default:
		row = m.keybindBindingRow(i, selected, pal, width)
	}
	return pal.Row(row, width, overlay.RowState{Cursor: selected, Focused: true}, pal.Surface)
}

func keybindRowChrome(selected bool, mark string, markFg color.Color, pal overlay.Palette) (color.Color, string) {
	bg := pal.Ground(overlay.RowState{Cursor: selected, Focused: true}, pal.Surface)
	marker := "  "
	if selected {
		marker = "› "
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker)
	left += overlay.Style(bg).Foreground(overlay.ReadableAt(markFg, bg, overlay.MarkFloor)).
		Bold(true).Render(overlay.Fill(mark, 2, bg))
	return bg, left
}

func (m *OS) keybindBindingRow(i int, selected bool, pal overlay.Palette, width int) string {
	all := m.FilteredKeybindRows()
	if i < 0 || i >= len(all) {
		return overlay.Style(pal.Surface).Render(" ")
	}
	b := all[i]
	glyphs := keybindGlyphs()

	mark, markFg := glyphs.ok, pal.FgMute
	switch {
	case b.Shadowed:
		mark, markFg = glyphs.dead, pal.Warn
	case b.Unbound:
		mark, markFg = glyphs.unbound, pal.FgMute
	}
	bg, left := keybindRowChrome(selected, mark, markFg, pal)

	keyFg := pal.AccentBright
	descFg := pal.FgDim
	if selected {
		descFg = pal.Fg
	}
	if b.Shadowed || b.Unbound {
		keyFg, descFg = pal.FgMute, pal.FgMute
	}

	press := b.Press
	if b.Unbound {
		press = "unbound"
	}
	keyCol := min(keybindKeyColumn, max(width/3, 8))
	key := overlay.Style(bg).Foreground(overlay.Readable(keyFg, bg)).Bold(true).
		Render(overlay.Fill(overlay.Truncate(press, keyCol-1), keyCol, bg))

	scope := scopeShortName(b.Scope)
	scopeW := lipgloss.Width(scope) + 1
	descW := max(width-lipgloss.Width(left)-keyCol-scopeW, 1)
	desc := overlay.Style(bg).Foreground(descFg).Render(overlay.Truncate(b.Desc, descW))

	row := left + key + desc
	gap := max(width-lipgloss.Width(row)-lipgloss.Width(scope), 0)
	return row + overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
		overlay.Style(bg).Foreground(overlay.Structure(bg)).Render(scope)
}

func (m *OS) keybindConflictRow(i int, selected bool, pal overlay.Palette, width int) string {
	all := m.KeybindReport().Collisions
	if i < 0 || i >= len(all) {
		return overlay.Style(pal.Surface).Render(" ")
	}
	c := all[i]
	glyphs := keybindGlyphs()

	bg, left := keybindRowChrome(selected, glyphs.dead, pal.Warn, pal)

	keyCol := min(keybindKeyColumn, max(width/3, 8))
	key := overlay.Style(bg).Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).
		Render(overlay.Fill(overlay.Truncate(c.Press, keyCol-1), keyCol, bg))

	text := lipgloss.Sprintf("runs %s %s %d dead", c.Winner, glyphs.bullet, len(c.Losers))
	if c.CrossSection {
		text += lipgloss.Sprintf(" %s 2 tables", glyphs.bullet)
	}
	scope := scopeShortName(c.Scope)
	scopeW := lipgloss.Width(scope) + 1
	descW := max(width-lipgloss.Width(left)-keyCol-scopeW, 1)
	descFg := pal.FgDim
	if selected {
		descFg = pal.Fg
	}
	desc := overlay.Style(bg).Foreground(descFg).Render(overlay.Truncate(text, descW))

	row := left + key + desc
	gap := max(width-lipgloss.Width(row)-lipgloss.Width(scope), 0)
	return row + overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
		overlay.Style(bg).Foreground(overlay.Structure(bg)).Render(scope)
}

func (m *OS) keybindGuestRow(i int, selected bool, pal overlay.Palette, width int) string {
	all := m.KeybindReport().GuestClashes
	if i < 0 || i >= len(all) {
		return overlay.Style(pal.Surface).Render(" ")
	}
	c := all[i]
	glyphs := keybindGlyphs()

	bg, left := keybindRowChrome(selected, glyphs.clash, pal.Warning, pal)

	keyCol := min(keybindKeyColumn, max(width/3, 8))
	key := overlay.Style(bg).Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).
		Render(overlay.Fill(overlay.Truncate(c.Key, keyCol-1), keyCol, bg))

	tail := c.Program
	if c.Running {
		tail = c.Program + " " + glyphs.live + " now"
	}
	tailW := lipgloss.Width(tail) + 1
	descW := max(width-lipgloss.Width(left)-keyCol-tailW, 1)
	descFg := pal.FgDim
	if selected {
		descFg = pal.Fg
	}
	desc := overlay.Style(bg).Foreground(descFg).Render(overlay.Truncate(c.ProgramUse, descW))

	tailFg := overlay.Structure(bg)
	if c.Running {
		tailFg = overlay.ReadableAt(pal.Warning, bg, overlay.MarkFloor)
	}
	row := left + key + desc
	gap := max(width-lipgloss.Width(row)-lipgloss.Width(tail), 0)
	return row + overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
		overlay.Style(bg).Foreground(tailFg).Render(tail)
}

func (m *OS) keybindDetail(selected int) string {
	rep := m.KeybindReport()
	switch m.KeybindTab {
	case KeybindTabConflicts:
		if selected < 0 || selected >= len(rep.Collisions) {
			return ""
		}
		c := rep.Collisions[selected]
		var dead []string
		for _, l := range c.Losers {
			dead = append(dead, l.Action+" ["+l.Section+"]")
		}
		detail := "In " + c.ScopeName + ", " + c.Press + " runs " + c.Winner +
			". Never runs: " + strings.Join(dead, ", ") + "."
		if c.CrossSection {
			detail += " The bindings sit in different tables in config.toml. The later table wins."
		}
		return detail + " ctrl+d removes the dead ones, which changes no key. ctrl+x frees " + c.Key + "."
	case KeybindTabGuests:
		if selected < 0 || selected >= len(rep.GuestClashes) {
			return ""
		}
		c := rep.GuestClashes[selected]
		lead := "From the program list, not from this pane. "
		if c.Running {
			lead = "From the program list, and " + rep.Pane.Command + " runs in this pane now. "
		}
		return lead + "tuios takes " + c.Key + " (" + c.TuiosDesc + "), so " +
			c.Program + " never sees it. " + c.Program + " wants it for " + c.ProgramUse + ". " + c.Note
	}

	all := m.FilteredKeybindRows()
	if selected < 0 || selected >= len(all) {
		return ""
	}
	b := all[selected]
	if b.Unbound {
		return b.Desc + " has no key. config.toml says " + b.Action +
			" = [] in [" + b.Section + "], so the default stays off. Press ctrl+r to record one."
	}
	if b.Shadowed {
		return "This key never works: " + b.ShadowedBy +
			" already has " + b.Key + " in this scope. Pressing it runs that instead. " +
			"ctrl+d takes the dead binding off " + b.Action + "."
	}
	var detail strings.Builder
	detail.WriteString(b.Desc)
	detail.WriteString(". Bound in [")
	detail.WriteString(b.Section)
	detail.WriteString("]. ctrl+d unbinds it; ctrl+x takes ")
	detail.WriteString(b.Key)
	detail.WriteString(" off every action.")
	for _, s := range rep.Swallowed {
		if config.SameKey(s.Key, b.Key) {
			detail.WriteString(" Terminal mode takes this key, so the program in the pane never sees it.")
			break
		}
	}
	if v := config.AmbiguityVerdict(b.Key, rep.Pane.HostDisambiguates); v != "" {
		detail.WriteString(" ")
		detail.WriteString(v)
	}
	return detail.String()
}

func scopeShortName(id string) string {
	for _, s := range config.Scopes("") {
		if s.ID == id {
			return strings.ToLower(s.Name)
		}
	}
	return id
}

func (m *OS) keybindRecordBody(pal overlay.Palette, width, visible int, chrome keybindChrome) []string {
	bg := pal.Surface
	glyphs := keybindGlyphs()
	rep := m.KeybindReport()
	key, fate := m.KeybindCaptured()

	head := overlay.Style(bg).Foreground(pal.FgDim)
	label := overlay.Style(bg).Foreground(overlay.Structure(bg))
	body := overlay.Style(bg).Foreground(pal.Fg)

	var lines []string
	add := func(s string) { lines = append(lines, s) }
	section := func(title string) {
		add(overlay.Style(bg).Render(" "))
		add(label.Render("  " + strings.ToUpper(title)))
	}

	_, bindAction := m.KeybindBindTarget()

	switch {
	case m.KeybindArmed():
		add(overlay.Style(bg).Render(" "))
		add(overlay.Style(bg).Foreground(overlay.Readable(pal.Accent, bg)).Bold(true).
			Render("  Press any key. It will be recorded, not run."))
		armed := "  tuios records 1 key. The next key works as normal."
		if bindAction != "" {
			armed = "  tuios will ask to bind it to " + bindAction + "."
		}
		add(head.Render(armed))
	case key == "":
		add(overlay.Style(bg).Render(" "))
		add(head.Render("  Press ctrl+r to record a key and see what tuios already does with it."))
		add(head.Render("  To bind one, pick an action on the Bindings tab and press ctrl+r there."))
	default:
		add(overlay.Style(bg).Render(" "))
		add(overlay.KeyBadge(key, pal) + overlay.Style(bg).Render("  ") +
			head.Render(keybindFateHeadline(fate)))
		switch {
		case m.KeybindBound() == key:
			add(overlay.Style(bg).Foreground(overlay.Readable(pal.Success, bg)).
				Render("  " + glyphs.ok + " written to config as " + bindAction))
		case bindAction != "":
			add(overlay.Style(bg).Foreground(overlay.Readable(pal.Accent, bg)).
				Render("  " + overlay.EnterKey() + " bind it to " + bindAction + "   esc  leave it alone"))
		case !fate.Free:
			add(overlay.Style(bg).Foreground(overlay.Readable(pal.Accent, bg)).
				Render("  ctrl+d  take " + key + " off every action, so the pane gets it"))
		}
	}

	if key != "" && !m.KeybindArmed() {
		section("in tuios")
		if len(fate.Acts) == 0 {
			add(body.Render("  " + glyphs.ok + " tuios does not use this key in any scope"))
		}
		for _, a := range fate.Acts {
			mark := glyphs.ok
			text := a.Desc
			if a.Shadowed {
				mark = glyphs.dead
				text = a.Desc + " (dead: " + a.ShadowedBy + " has the key)"
			}
			add(body.Render("  "+mark+" ") +
				label.Render(overlay.Fill(scopeShortName(a.Scope), keybindScopeColumn, bg)) +
				body.Render(overlay.Truncate(text, max(width-keybindScopeColumn-4, 8))))
		}
		if fate.SwallowedInTerminal {
			add(overlay.Style(bg).Foreground(overlay.ReadableAt(pal.Warning, bg, overlay.MarkFloor)).
				Render("  "+glyphs.clash+" ") +
				label.Render(overlay.Fill("pane", keybindScopeColumn, bg)) +
				body.Render(overlay.Truncate("taken by tuios: "+fate.SwallowReason, max(width-keybindScopeColumn-4, 8))))
		} else {
			add(body.Render("  "+glyphs.ok+" ") +
				label.Render(overlay.Fill("pane", keybindScopeColumn, bg)) +
				head.Render("goes to the program in this pane"))
		}

		if fate.Ambiguity != "" {
			section("what a terminal can tell apart")
			for _, l := range wrapPlain(fate.Ambiguity, max(width-4, 10)) {
				add(head.Render("  " + l))
			}
		}

		if len(fate.GuestWants) > 0 {
			section("wanted by (program list, not this pane)")
			for _, g := range fate.GuestWants {
				tail := ""
				if g.Running {
					tail = " " + glyphs.live + " runs here now"
				}
				add(overlay.Style(bg).Foreground(overlay.ReadableAt(pal.Warning, bg, overlay.MarkFloor)).
					Render("  "+glyphs.clash+" ") +
					label.Render(overlay.Fill(overlay.Truncate(g.Program, 22), 24, bg)) +
					body.Render(overlay.Truncate(g.ProgramUse+tail, max(width-28, 8))))
			}
		}
	}

	if rep.Pane.Command != "" || rep.Pane.AltScreen {
		section("seen in this pane")
		for _, o := range rep.Observations {
			if o.What == "Host keyboard" {
				continue
			}
			for i, l := range wrapPlain(o.Detail, max(width-6, 10)) {
				lead := "  " + glyphs.bullet + " "
				if i > 0 {
					lead = "    "
				}
				add(head.Render(lead + l))
			}
		}
	}

	target := visible + chrome.lines()
	for len(lines) < target {
		add(overlay.Style(bg).Render(" "))
	}
	return lines[:target]
}

func keybindFateHeadline(fate config.KeyFate) string {
	switch {
	case fate.Free:
		return "free: tuios does not use it, so the pane gets it"
	case len(fate.Acts) == 0 && fate.SwallowedInTerminal:
		return "tuios takes it before the pane sees it"
	case len(fate.Acts) == 1:
		return "bound in 1 scope"
	default:
		return lipgloss.Sprintf("bound in %d scopes", len(fate.Acts))
	}
}
