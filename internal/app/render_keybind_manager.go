package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Keybind manager layout. Preferred sizes; a smaller screen gets a fitted
// panel (see overlay_fit.go).
const (
	keybindInnerWidth  = 74
	keybindVisibleRows = 11
	// keybindDetailRows is the fixed-height box under the list that explains the
	// selected row. Fixed so the panel does not change height as the selection
	// moves, which is the same reason the settings panel fixes its own.
	keybindDetailRows = 4
	// keybindKeyColumn is how much of a row the chord gets before the
	// description starts. Wide enough for "ctrl+b shift+tab" unabbreviated,
	// because a truncated chord is not a chord.
	keybindKeyColumn = 20
	// keybindScopeColumn is the recorder's scope column. Two cells wider than
	// the longest scope name ("workspace prefix"), which ran straight into the
	// text beside it at its natural width.
	keybindScopeColumn = 18
)

// keybindHints is the footer, shared by the renderer and the sizing helper so
// both measure the same panel.
var keybindHints = []overlay.Hint{
	{Key: "tab", Label: "section"},
	{Key: "↑↓", Label: "move"},
	{Key: "/", Label: "filter"},
	{Key: "ctrl+r", Label: "record"},
	{Key: "ctrl+d", Label: "unbind"},
	{Key: "esc", Label: "close"},
}

// keybindMarks are the non-colour signals. A conflict has to survive a
// monochrome terminal and a colour-blind reader, so severity is carried by a
// glyph and by a word in the text; the colour is the third channel, not the
// only one.
type keybindMarks struct {
	dead string // a binding that never fires
	// unbound is an action the config deliberately left with no key. Its own
	// glyph rather than dead's: a dead binding is a mistake to fix, an unbound
	// action is a choice already made, and one mark for both would report the
	// user's own decision back to them as a problem.
	unbound string
	clash   string // a key a guest program wants
	ok      string // nothing wrong with this row
	live    string // the guest program actually running right now
	bullet  string
}

func keybindGlyphs() keybindMarks {
	if overlay.UseASCII() {
		return keybindMarks{dead: "x", unbound: "-", clash: "!", ok: " ", live: "<<", bullet: "-"}
	}
	return keybindMarks{dead: "✕", unbound: "○", clash: "▲", ok: " ", live: "◀", bullet: "·"}
}

// keybindChrome is which of the body's non-row lines a given screen has room
// for. Every field is a line count so the renderer and the fitter agree on the
// panel's height by construction rather than by two matching constants.
type keybindChrome struct {
	// header is the filter line and its rule: 2, or 0 when they are shed.
	header int
	// count is the "n of m" line: 1, or 0.
	count int
	// detail is the explanation box under the list, plus the rule above it when
	// it is present.
	detail int
}

// lines is how many body lines this chrome costs.
func (c keybindChrome) lines() int {
	n := c.header + c.count + c.detail
	if c.detail > 0 {
		n++ // the rule above the box
	}
	return n
}

// keybindShedOrder is every layout the panel will accept, most generous first.
//
// The order is what the panel gives up as the screen shrinks, and it is an
// argument about what the surface is for rather than an arbitrary sequence. The
// footer goes first, handled by panelBody: the keys it names are still on the
// keyboard. Then the detail box, a line at a time and then entirely, because it
// restates the row above it. Then the count, which is a convenience. The filter
// line goes last, because on a list of a few hundred bindings it is the only way
// to reach most of them.
var keybindShedOrder = []keybindChrome{
	{header: 2, count: 1, detail: 4},
	{header: 2, count: 1, detail: 3},
	{header: 2, count: 1, detail: 2},
	{header: 2, count: 1, detail: 1},
	{header: 2, count: 1, detail: 0},
	{header: 2, count: 0, detail: 0},
	{header: 0, count: 0, detail: 0},
}

// keybindLayout returns the fitted inner width, the list row count, the footer,
// and which body lines survived.
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
	// Nothing left to shed: the last entry is a bare list, and a panel that
	// still does not fit is one the screen cannot hold at all.
	last := keybindShedOrder[len(keybindShedOrder)-1]
	rows, hints = m.panelBody(keybindVisibleRows, last.lines(), width, KeybindTabNames, keybindHints)
	return width, rows, hints, last
}

// renderKeybindManager draws the overlay and returns the panel, its geometry,
// and the per-row hit rects the renderer recorded as it drew them.
func (m *OS) renderKeybindManager() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	width, visible, hints, chrome := m.keybindLayout()

	var lines []string
	var rows []overlayRowHit
	// rowYOffset is how far the first list row sits below the body origin. It is
	// counted from the lines actually emitted above it rather than from a
	// constant, so a tab that draws a different header cannot silently shift
	// every hit rect by one.
	rowYOffset := 0

	switch m.KeybindTab {
	case KeybindTabRecord:
		lines = m.keybindRecordBody(pal, width, visible, chrome)
	default:
		lines, rows, rowYOffset = m.keybindListBody(pal, width, visible, chrome)
	}

	panel := overlay.Panel{
		// Written as an escape so the codepoint survives tooling that does not
		// carry private-use glyphs. The panel drops it in ASCII mode itself.
		Glyph:     "", // keyboard
		Title:     "Keybinds",
		Width:     width,
		Tabs:      KeybindTabNames,
		ActiveTab: m.KeybindTab,
		Body:      strings.Join(lines, "\n"),
		Hints:     hints,
	}
	content, geo := panel.Render(pal)

	// The hit rects were built in panel-relative row order while the body was
	// laid out; only now is the body's origin known, so they are offset here
	// rather than recomputed.
	for i := range rows {
		rows[i].Rect.Y0 += geo.BodyY + rowYOffset
		rows[i].Rect.Y1 += geo.BodyY + rowYOffset
		rows[i].Rect.X1 = geo.Width
	}
	return content, geo, rows
}

// keybindListBody lays out the three list tabs: a filter line, the rows, a
// count, and a detail box for the selected row.
func (m *OS) keybindListBody(pal overlay.Palette, width, visible int, chrome keybindChrome) ([]string, []overlayRowHit, int) {
	bg := pal.Surface
	var lines []string

	// Filter line. Shown on every list tab so the control is in one place,
	// rather than appearing and disappearing as the user moves between them.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	query := m.KeybindQuery()
	prompt := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ")
	if m.KeybindTab != KeybindTabBindings {
		// The other two tabs are short enough to read whole, so the filter is
		// there for consistency of layout, not of function; say so rather than
		// offering a control that does nothing.
		prompt = overlay.Style(bg).Foreground(pal.FgMute).Render("  ")
		query = m.keybindTabSubtitle()
		cursor = ""
	}
	if chrome.header > 0 {
		lines = append(lines,
			prompt+overlay.Style(bg).Foreground(pal.Fg).Render(query)+cursor,
			overlay.Rule(width, bg, pal),
		)
	}
	rowYOffset := len(lines)

	count := m.keybindRowCount()
	selected := clampInt(m.KeybindSelected(), 0, max(count-1, 0))
	m.keybinds.selected = selected
	m.keybinds.scroll = scrollWindow(m.keybinds.scroll, selected, count, visible)
	start := m.keybinds.scroll
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
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
			Render("  "+m.keybindEmptyMessage()))
		shown++
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

	// Detail box for the selected row, when the screen left room for one.
	if chrome.detail > 0 {
		lines = append(lines, overlay.Rule(width, bg, pal))
		lines = append(lines, settingsDescription(m.keybindDetail(selected), width, chrome.detail, pal)...)
	}

	return lines, rows, rowYOffset
}

// keybindTabSubtitle is the one-line summary that stands in for the filter box
// on the tabs that do not filter.
func (m *OS) keybindTabSubtitle() string {
	rep := m.KeybindReport()
	switch m.KeybindTab {
	case KeybindTabConflicts:
		if len(rep.Collisions) == 0 {
			return "No key is used twice in the same scope"
		}
		return "Keys used twice. Each row names the one that runs."
	case KeybindTabGuests:
		if len(rep.GuestClashes) == 0 {
			return "No program needs a key that tuios uses"
		}
		return "Keys tuios uses that a program also wants"
	}
	return ""
}

// keybindEmptyMessage is what an empty list says. Each tab's emptiness means
// something different, and "no results" would flatten all three.
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

// keybindRow renders row i of the active tab.
func (m *OS) keybindRow(i int, selected bool, pal overlay.Palette, width int) string {
	switch m.KeybindTab {
	case KeybindTabConflicts:
		return m.keybindConflictRow(i, selected, pal, width)
	case KeybindTabGuests:
		return m.keybindGuestRow(i, selected, pal, width)
	}
	return m.keybindBindingRow(i, selected, pal, width)
}

// keybindRowChrome is the shared start of every row: the background, the
// selection marker, and the mark column.
func keybindRowChrome(selected bool, mark string, markFg color.Color, pal overlay.Palette) (color.Color, string) {
	bg := pal.Surface
	marker := "  "
	if selected {
		bg = pal.RowSel
		marker = "› "
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker)
	// The mark is held to MarkFloor rather than to text contrast: it carries its
	// meaning in its shape as much as its colour, and lifting a warning colour
	// all the way to text contrast is what turns a theme's red into pink.
	left += overlay.Style(bg).Foreground(overlay.ReadableAt(markFg, bg, overlay.MarkFloor)).
		Bold(true).Render(overlay.Fill(mark, 2, bg))
	return bg, left
}

// keybindBindingRow is one binding: what to press, what it does, and where.
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
		// A dead binding is drawn quiet as well as marked. Quiet alone would be
		// a colour-only signal; the mark and the "dead" word in the detail box
		// carry it without the colour. An unbound action is quiet for the same
		// reason and says "unbound" where its chord would be.
		keyFg, descFg = pal.FgMute, pal.FgMute
	}

	// The word rather than an empty column: a blank there reads as a rendering
	// fault, and the row has to say what it is without the detail box, which a
	// short screen sheds.
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
	// The scope is structure, not content: it repeats down the column and the
	// row is readable without it.
	return row + overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
		overlay.Style(bg).Foreground(overlay.Structure(bg)).Render(scope)
}

// keybindConflictRow is one contested key: what wins, and how many lost.
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

	// The word "dead" is what makes this legible without colour: the count of
	// losers alone reads as a quantity, not as a problem.
	// Kept short enough to survive the description column: the detail box under
	// the list carries the whole sentence, and a row that truncates its own
	// verdict is a row that says nothing.
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

// keybindGuestRow is one key tuios withholds that a curated program wants.
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

	// The running program's row is marked with a glyph and with the word "now",
	// not by being a different colour.
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

// keybindDetail is the sentence in the box under the list, for the selected
// row. It is where a finding says which tier it came from, because the row
// above it has no room to and a finding without its tier is the one thing this
// overlay must not print.
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
		return detail
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
	detail := b.Desc + ". Bound in [" + b.Section + "]. ctrl+d unbinds it; ctrl+x takes " +
		b.Key + " off every action."
	for _, s := range rep.Swallowed {
		if strings.EqualFold(s.Key, b.Key) {
			detail += " Terminal mode takes this key, so the program in the pane never sees it."
			break
		}
	}
	if v := config.AmbiguityVerdict(b.Key, rep.Pane.HostDisambiguates); v != "" {
		detail += " " + v
	}
	return detail
}

// scopeShortName is the scope label for a row's right edge.
func scopeShortName(id string) string {
	for _, s := range config.Scopes("") {
		if s.ID == id {
			return strings.ToLower(s.Name)
		}
	}
	return id
}

// keybindRecordBody draws the recorder: what was pressed, what tuios does with
// it, what a terminal can and cannot tell apart, and who else wants it.
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
				Render("  ⏎ bind it to " + bindAction + "   esc  leave it alone"))
		case !fate.Free:
			// The offer only appears for a key tuios actually holds. Printing it
			// for a free key would invite a gesture that does nothing and read
			// as though the key had been taken.
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
				continue // already covered by the ambiguity block above
			}
			// Wrapped, not truncated: these are sentences that carry a fact
			// about the pane, and half of one says only that something was cut.
			for i, l := range wrapPlain(o.Detail, max(width-6, 10)) {
				lead := "  " + glyphs.bullet + " "
				if i > 0 {
					lead = "    "
				}
				add(head.Render(lead + l))
			}
		}
	}

	// The panel keeps a fixed height whatever the recorder is showing, so it
	// does not jump around the screen between one key and the next.
	target := visible + chrome.lines()
	for len(lines) < target {
		add(overlay.Style(bg).Render(" "))
	}
	return lines[:target]
}

// keybindFateHeadline is the one line beside a captured key.
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
