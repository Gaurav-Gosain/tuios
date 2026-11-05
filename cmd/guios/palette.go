package main

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// paletteState is the command palette's: a field that finds sessions,
// panes, conversations and commands as one types.
type paletteState struct {
	open   bool
	query  string
	index  int
	scroll smoothScroll
	// list builds only the rows in view. It keeps the heights it measured
	// from one opening to the next.
	list   ui.ListState
	reveal bool // the chosen item scrolls into view, once
	// convs are the conversations the hosts' kerods remember, the last to
	// run first, as they come; only shows them alone (Resume
	// Conversation…), and gen tells the palette they came for.
	convs []hostConversation
	only  bool
	gen   int
	// custom, when set, are the only items, under customLabel: a list to
	// choose from, as the paste buffers.
	custom      []paletteItem
	customLabel string
}

// hostConversation is a conversation a host's kerod remembers.
type hostConversation struct {
	host *host
	proto.Conversation
}

// recentConversations is how many conversations the palette shows before
// the user types.
const recentConversations = 5

// paletteItem is a session, a pane, a conversation or a command the
// palette offers.
type paletteItem struct {
	session *proto.Session
	pane    *proto.Pane // with session, the session's pane
	conv    *hostConversation
	title   string
	detail  string
	// keys are what runs it, as the menus show keys: guios's shortcut,
	// then the tuios prefix keys; "" for none.
	keys  [2]string
	run   func()
	score int
	hits  []int  // the title's bytes the query matched
	words string // what finds it besides its title and detail, unshown
	top   bool   // what the query asks for outright, before any match
	// danger marks a command that ends or deletes something: its title
	// is in the danger colour.
	danger bool
}

// paletteDanger are the commands that end or delete something.
var paletteDanger = map[string]bool{"app.quitAll": true, "session.close": true}

func (a *App) openPalette() {
	a.pal = paletteState{open: true, reveal: true, gen: a.pal.gen + 1, list: a.pal.list}
	a.loadConversations()
}

// openConversations opens the palette on the conversations alone.
func (a *App) openConversations() {
	a.openPalette()
	a.pal.only = true
}

// loadConversations asks every host's kerod for the conversations it
// remembers. An older one knows of none.
func (a *App) loadConversations() {
	gen := a.pal.gen
	for _, h := range a.hosts {
		cl := h.cl
		if cl == nil {
			continue
		}
		go func() {
			var list []proto.Conversation
			if cl.Call("conversations", nil, &list) != nil {
				return
			}
			a.update(func() {
				if a.pal.gen != gen || !a.pal.open {
					return
				}
				for _, cv := range list {
					a.pal.convs = append(a.pal.convs, hostConversation{h, cv})
				}
				slices.SortStableFunc(a.pal.convs, func(x, y hostConversation) int { return cmp.Compare(y.Updated, x.Updated) })
			})
		}()
	}
}

// conversationItems are the palette's conversations: those no pane runs,
// as a session the palette has already shows them; all of them with only.
func (a *App) conversationItems() []paletteItem {
	var items []paletteItem
	names := map[string]string{}
	for _, p := range a.st.Projects {
		names[p.ID] = p.Name
	}
	now := time.Now()
	for i := range a.pal.convs {
		cv := &a.pal.convs[i]
		if cv.Session != "" && !a.pal.only {
			continue
		}
		detail := cmp.Or(proto.AgentNames[cv.Agent], cv.Agent)
		if cv.Name != "" && cv.Title != "" {
			detail = cv.Title + " · " + detail
		}
		if n := names[cv.Project]; n != "" {
			detail += " · " + n
		}
		if cv.host.remote() {
			detail = cv.host.name + " · " + detail
		}
		if cv.Session != "" {
			detail += " · " + locales.PaletteConversationRunning()
		} else {
			detail += " · " + ago(cv.Updated, now)
		}
		items = append(items, paletteItem{conv: cv, title: cv.Label(), detail: detail,
			words: cv.Title + " " + cv.Name + " " + cv.Dir + " " + cv.Agent + " " + cv.ID,
			run:   func() { a.resumeConversation(cv.host, cv.Conversation) }})
	}
	return items
}

// resumeConversation shows the session running a conversation of a
// host's, or starts one resuming it, beside the session shown.
func (a *App) resumeConversation(h *host, cv proto.Conversation) {
	cl := h.cl
	if cl == nil {
		a.toast(locales.HostNotConnected(h.name))
		return
	}
	p := proto.ConversationParams{Agent: cv.Agent, ID: cv.ID}
	if cur := a.session(a.selected); cur != nil && a.hostOf(cur.ID) == h {
		p.Cols, p.Rows = cur.Cols, cur.Rows
		if cur.Project == cv.Project {
			p.After = cur.ID
		}
	}
	go func() {
		var s proto.Session
		err := cl.Call("conversation.resume", p, &s)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			delete(a.prefs.Collapsed, s.Project)
			a.addPending(h, s)
			a.selectSession(s.ID)
		})
	}()
}

func (a *App) paletteItems() []paletteItem {
	if a.pal.custom != nil {
		return a.pal.custom
	}
	if a.pal.only {
		return a.conversationItems()
	}
	var items []paletteItem
	names := map[string]string{}
	for _, p := range a.st.Projects {
		names[p.ID] = p.Name
	}
	// ⌘1 to ⌘9 (Ctrl elsewhere) show the sidebar's first nine rows.
	order := a.visibleOrder()
	for _, g := range a.groups() {
		for _, s := range g.sessions {
			id := s.ID
			// The project's name, unless it is the group's own word
			// ("Sessions"), which the header over the rows says already.
			detail := names[s.Project]
			if detail == paletteGroups[paletteSession]() {
				detail = ""
			}
			if s.Process != "" {
				detail += " · " + s.Process
			}
			h := a.hostOf(id)
			if h.remote() {
				detail = h.name + " · " + detail
			}
			if detail != "" {
				detail += " · "
			}
			it := paletteItem{session: s, title: a.railName(s), detail: detail + h.shortDir(s.WorkDir()) + paletteMark(a.railMark(s)),
				run: func() { delete(a.prefs.Collapsed, s.Project); a.selectSession(id) }}
			if i := slices.Index(order, id); i >= 0 && i < 9 {
				it.keys[0] = actions.Glyphs("CmdOrCtrl+" + itoa(i+1))
			}
			items = append(items, it)
		}
	}
	items = append(items, a.paneItems()...)
	convs := a.conversationItems()
	if a.pal.query == "" && len(convs) > recentConversations {
		convs = convs[:recentConversations]
	}
	items = append(items, convs...)
	return append(items, a.commandItems()...)
}

// paneItems are the panes to go to: every pane of each session that has
// more than one, on any of its workspaces. The selected session's shown
// panes carry the tuios keys that go to them (the prefix, then 1 to 9).
func (a *App) paneItems() []paletteItem {
	var items []paletteItem
	for _, g := range a.groups() {
		for _, s := range g.sessions {
			if len(s.Panes)+len(s.Others) < 2 {
				continue
			}
			sid, name, h := s.ID, a.railName(s), a.hostOf(s.ID)
			multiWS := len(s.Workspaces) > 1
			add := func(p *proto.Pane, ws int, keys string) {
				detail := name
				if multiWS {
					detail += " · " + locales.PaletteWorkspace(itoa(ws))
				}
				if dir := h.shortDir(p.WorkDir()); dir != "" && dir != name && dir != p.PaneName() {
					detail += " · " + dir
				}
				pane := p.ID
				items = append(items, paletteItem{session: s, pane: p, title: p.PaneName(), detail: detail,
					keys: [2]string{"", keys}, words: name + " " + p.Process + " " + p.Agent + " pane window",
					run: func() { a.jumpToPane(sid, pane) }})
			}
			for i := range s.Panes {
				keys := ""
				if s.ID == a.selected && i < 10 {
					keys = a.prefixKeys("prefix_select_" + itoa((i+1)%10))
				}
				add(&s.Panes[i], s.Workspace, keys)
			}
			for i := range s.Others {
				add(&s.Others[i], s.Others[i].Workspace, "")
			}
		}
	}
	return items
}

// jumpToPane shows a pane of a session and puts the user in it. A pane on
// another workspace is shown by focusing it, which shows its workspace
// (focus-window), for every client of the session.
func (a *App) jumpToPane(sid, pane string) {
	s := a.session(sid)
	if s == nil {
		return
	}
	delete(a.prefs.Collapsed, s.Project)
	if a.selected != sid {
		a.selectSession(sid)
	}
	if s.PaneByID(pane) != nil {
		a.setFocus(s, pane)
		a.defer_(func() { a.paneRef(pane).Focus() })
		return
	}
	if a.focus == nil {
		a.focus = map[string]string{}
	}
	a.focus[sid] = pane
	a.call(sid, "focus", proto.FocusParams{Session: sid, Pane: pane}, nil)
}

// paletteTitles are the titles the palette gives the actions whose menu
// title says too little without its menu around it.
var paletteTitles = map[string]func() string{
	"pane.close":    locales.PaneClose,
	"text.bigger":   locales.PaletteTextBigger,
	"text.smaller":  locales.PaletteTextSmaller,
	"text.reset":    locales.PaletteTextReset,
	"agents.toggle": locales.PaletteAgents,
	"agents.new":    locales.PaletteNewAgent,
}

// commandItems are the commands: guios's actions with their shortcut and
// the tuios prefix keys of the same action, the tuios keymap's actions
// guios has no action of its own for, Settings' pages, and the rest.
func (a *App) commandItems() []paletteItem {
	var cmds []paletteItem
	// Commands are found by their English names too, in any language.
	en := locales.In("en")
	// An address typed opens in a browser beside the pane the user is in.
	if q := strings.TrimSpace(a.pal.query); proto.IsAddress(q) && a.session(a.selected) != nil {
		cmds = append(cmds, paletteItem{title: locales.PaletteOpenInBrowser(), detail: proto.BrowserURL(q), top: true,
			run: func() { a.openBrowser(q) }})
	}
	tuiosOf := map[string]string{}
	for _, r := range keyRows {
		if r.kero != "" {
			tuiosOf[r.kero] = r.action
		}
	}
	first := len(cmds)
	for _, act := range actions.All {
		id := act.ID
		if id == "palette.open" || id == "pane.restart" {
			continue // this, and what tuios does not do
		}
		title := strings.TrimSuffix(actionTitle(id), "…")
		if t := paletteTitles[id]; t != nil {
			title = t()
		}
		ta := tuiosOf[id]
		cmds = append(cmds, paletteItem{title: title, danger: paletteDanger[id],
			keys:  [2]string{actions.Glyphs(actions.Keys(act, a.set.Shortcuts)), a.prefixKeys(ta)},
			words: act.Title + " " + actionWords[id] + " " + ta + " " + config.ActionDescriptions[ta],
			run:   func() { a.run(id, nil) }})
	}
	// The keymap's own actions; those only a TUI runs while one shows the
	// session.
	s := a.session(a.selected)
	for _, r := range keyRows {
		if r.kero != "" || r.tui && (s == nil || !s.TUI) {
			continue
		}
		action, detail := r.action, ""
		if r.tui {
			detail = locales.PaletteViaTui()
		}
		cmds = append(cmds, paletteItem{title: r.title(), detail: detail, keys: [2]string{"", a.prefixKeys(action)},
			words: action + " " + config.ActionDescriptions[action],
			run:   func() { a.runKeyAction(action) }})
	}
	for _, page := range settingsPages {
		id := page.id
		it := paletteItem{title: locales.PaletteSettingsPage(page.title()), words: "settings preferences " + id + " " + pageWords[id], run: func() { a.openSettings(id) }}
		if id == "keyboard" {
			it.keys[1] = a.prefixKeys("prefix_keybinds")
		}
		cmds = append(cmds, it)
	}
	cmds = append(cmds, paletteItem{title: locales.PaletteRestartService(), detail: locales.PaletteRestartServiceDetail(),
		words: en.PaletteRestartService() + " " + en.PaletteRestartServiceDetail(), run: a.restartDaemon})
	for _, p := range a.st.Projects {
		id := p.ID
		h := a.hostOf(id)
		detail := h.shortDir(p.Dir)
		if h.remote() && detail != "" {
			detail = h.name + " · " + detail
		} else if h.remote() {
			detail = h.name
		}
		cmds = append(cmds, paletteItem{title: locales.NewSessionIn(p.Name), detail: detail, words: en.NewSessionIn(p.Name), run: func() { a.newSession(id) }})
	}
	for _, h := range a.hosts[1:] {
		cmds = append(cmds,
			paletteItem{title: locales.NewSessionIn(h.name), detail: h.addr(), words: en.NewSessionIn(h.name), run: func() { a.newSessionOn(h, "") }},
			paletteItem{title: locales.PickerTitle(h.name), detail: h.addr(), words: en.PickerTitle(h.name), run: func() { a.openProjectOn(h) }},
			paletteItem{title: locales.HostReconnectTo(h.name), detail: h.addr(), words: en.HostReconnectTo(h.name), run: func() { a.reconnectHost(h) }})
		if s := a.session(a.selected); s != nil && !a.hostOf(s.ID).remote() {
			sid, name := s.ID, h.name
			cmds = append(cmds, paletteItem{title: locales.NewPaneOn(name), detail: h.addr(), words: en.NewPaneOn(name), run: func() { a.newPaneOn(a.session(sid), name) }})
		}
	}
	cmds = append(cmds, paletteItem{title: locales.MachineAdd(), words: en.MachineAdd(), run: func() { a.openSettings("hosts") }})
	// What ends something comes last, after New session and the rest
	// (council round 3, visual n1).
	slices.SortStableFunc(cmds[first:], func(x, y paletteItem) int {
		switch {
		case x.danger == y.danger:
			return 0
		case x.danger:
			return 1
		}
		return -1
	})
	return cmds
}

// What a palette's item is, which its group in the list goes by.
const (
	paletteSession = iota
	palettePane
	paletteConversation
	paletteCommand
)

func (it *paletteItem) kind() int {
	switch {
	case it.pane != nil:
		return palettePane
	case it.session != nil:
		return paletteSession
	case it.conv != nil:
		return paletteConversation
	}
	return paletteCommand
}

// paletteGroups are the headers of the kinds, in their order.
var paletteGroups = [...]func() string{locales.PaletteSessions, locales.PalettePanes, locales.PaletteConversations, locales.PaletteCommands}

// paletteMatch scores how well the query finds an item, as fzf does
// (pkg/fuzzy): higher is better, ok false no match. The title found as
// letters in order counts most. Otherwise each word of the query must be
// in the title as letters in order, or start a word of the detail or the
// words. hits are the title's bytes it matched.
func paletteMatch(m *fuzzy.Matcher, query string, it *paletteItem) (score int, hits []int, ok bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil, true
	}
	if r, ok := m.Find(query, it.title); ok {
		return r.Score * 4, slices.Clone(r.Positions), true
	}
	more := it.detail + " " + it.words
	for _, t := range strings.Fields(query) {
		if r, ok := m.Find(t, it.title); ok {
			score += r.Score * 2
			hits = append(hits, r.Positions...)
			continue
		}
		if !startsWord(more, t) {
			return 0, nil, false
		}
		score += 4 * len(t)
	}
	slices.Sort(hits)
	return score, slices.Compact(hits), true
}

// startsWord reports whether a word of s starts with t, in any case. Words
// part at anything but letters and digits, as a path's names.
func startsWord(s, t string) bool {
	t = strings.ToLower(t)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(w, t) {
			return true
		}
	}
	return false
}

// paletteSpans are a title's runs, the bytes the query matched in the
// accent color.
func paletteSpans(title string, hits []int, pal *palette) []ui.Span {
	if len(hits) == 0 {
		return []ui.Span{{Text: title}}
	}
	var spans []ui.Span
	hit := map[int]bool{}
	for _, h := range hits {
		hit[h] = true
	}
	start, on := 0, hit[0]
	for i := range title {
		if hit[i] == on {
			continue
		}
		spans = append(spans, paletteSpan(title[start:i], on, pal))
		start, on = i, hit[i]
	}
	return append(spans, paletteSpan(title[start:], on, pal))
}

func paletteSpan(text string, hit bool, pal *palette) ui.Span {
	if hit {
		return ui.Span{Text: text, Color: pal.accent, Weight: 650}
	}
	return ui.Span{Text: text}
}

// paletteStep moves the palette's choice by d rows: past either end it
// goes round to the other, a page at a time it stops there.
func (a *App) paletteStep(d, n int) {
	if n == 0 {
		return
	}
	i := a.pal.index + d
	switch {
	case d == 1 || d == -1:
		i = (i + n) % n
	default:
		i = min(max(i, 0), n-1)
	}
	a.pal.index, a.pal.reveal = i, true
}

// palettePage is how many rows PageUp and PageDown move.
const palettePage = 8

func (a *App) paletteView(c *ui.Context, pal *palette) {
	if !a.pal.open {
		return
	}
	var items []paletteItem
	var m fuzzy.Matcher
	for _, it := range a.paletteItems() {
		sc, hits, ok := paletteMatch(&m, a.pal.query, &it)
		if it.top {
			sc, ok = math.MaxInt32, true
		}
		if !ok {
			continue
		}
		it.score, it.hits = sc, hits
		items = append(items, it)
	}
	if strings.TrimSpace(a.pal.query) != "" {
		slices.SortStableFunc(items, func(x, y paletteItem) int { return cmp.Compare(y.score, x.score) })
	}
	a.pal.index = min(max(a.pal.index, 0), max(len(items)-1, 0))

	var chosen func()
	label, placeholder, none := locales.PaletteLabel(), locales.PalettePlaceholder(), locales.PaletteNoMatches()
	if a.pal.custom != nil {
		label, placeholder = a.pal.customLabel, a.pal.customLabel
	}
	if a.pal.only {
		label, placeholder = locales.PaletteConversationsLabel(), locales.PaletteConversationsPlaceholder()
		if a.pal.query == "" {
			none = locales.PaletteNoConversations()
		}
	}
	// As wide as the window lets it be, up to paletteW, at 12 % of its
	// height.
	ww, wh := c.Size()
	width := float32(paletteW)
	if ww > 0 {
		width = min(width, ww-2*sp4)
	}
	ui.DialogBase(c.Key("palette"), &a.pal.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		backdrop.Justify(ui.Start).Padding(max(wh*0.12, 40), 0, 0, 0)
		floating(panel.Width(width).MaxHeight(min(480, wh*0.7)).Radius(rSheet).Background(pal.popover), pal).Clip().
			Label(label)
		ui.Row(c).Height(44).Shrink(0).Padding(0, sp4).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, searchIcon).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
			in := ui.TextInputBase(c.Key("q"), &a.pal.query).Grow(1).MinWidth(0).FontSize(fsProse).Placeholder(placeholder).AutoFocus()
			if in.Changed() {
				a.pal.index, a.pal.reveal = 0, true
			}
			switch {
			case c.Shortcut(0, ui.KeyDown) || c.Shortcut(ui.Ctrl, ui.KeyN):
				a.paletteStep(1, len(items))
			case c.Shortcut(0, ui.KeyUp) || c.Shortcut(ui.Ctrl, ui.KeyP):
				a.paletteStep(-1, len(items))
			case c.Shortcut(0, ui.KeyPageDown):
				a.paletteStep(palettePage, len(items))
			case c.Shortcut(0, ui.KeyPageUp):
				a.paletteStep(-palettePage, len(items))
			}
			if (in.Submitted() || c.Shortcut(0, ui.KeyEnter)) && len(items) > 0 {
				chosen = items[a.pal.index].run
			}
		})
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		// The rows: a group's header over its first item while nothing is
		// typed (a ranked list would split into runs under them), then
		// the items. Only those in view are built.
		type palRow struct{ head, item int }
		rows := make([]palRow, 0, len(items)+len(paletteGroups))
		lastKind := -1
		chosenRow := 0
		for i := range items {
			kind := items[i].kind()
			if kind != lastKind && a.pal.query == "" && !a.pal.only && a.pal.custom == nil {
				rows = append(rows, palRow{head: kind, item: -1})
			}
			lastKind = kind
			if i == a.pal.index {
				chosenRow = len(rows)
			}
			rows = append(rows, palRow{head: -1, item: i})
		}
		ls := &a.pal.list
		if a.pal.reveal {
			if a.pal.index == 0 {
				ls.ScrollTo(0, ui.Start)
			} else {
				ls.ScrollIntoView(chosenRow)
			}
		}
		// One title column for every row, so that the details start at one
		// x: as wide as the widest title in view, at most 40 % of the row.
		rowW := width - 2*6 - 2*10
		titleW := float32(0)
		for i := range min(len(items), 60) {
			titleW = max(titleW, natural(c, items[i].title, fsBody, 0)+1)
		}
		titleW = min(titleW, rowW*0.4)
		a.pal.reveal = false
		if len(items) == 0 {
			ui.Text(c, none).FontSize(fsBody).TextColor(pal.textFaint).Padding(18, 18).LineHeight(1.4).MaxLines(3)
			return
		}
		list := ui.List(c.Key("results"), ls, len(rows), func(r int) {
			if h := rows[r].head; h >= 0 {
				ui.Text(c, paletteGroups[h]()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textFaint).
					SingleLine().Padding(10, 10, 4, 10)
				return
			}
			i := rows[r].item
			it := items[i]
			kind := it.kind()
			row := ui.Row(c.Key(i)).Height(hRow+2).Padding(0, 10).Gap(10).AlignItems(ui.Center).Radius(rRow)
			if i == a.pal.index {
				row.Background(pal.accent.Alpha(0.15))
			} else if row.Hovered() {
				row.Background(pal.rowHover)
			}
			if row.Clicked() {
				chosen = it.run
			}
			row.Children(func() {
				switch kind {
				case paletteSession:
					statusIcon(c, pal, it.session.Status, it.session.Unread, 14)
				case palettePane:
					if p := it.pane; p.Status != proto.Idle || p.Unread {
						statusIcon(c, pal, p.Status, p.Unread, 14)
					} else {
						ui.Icon(c, programIcon(p)).Size(14, 14).TextColor(pal.textFaint)
					}
				case paletteConversation:
					ui.Icon(c, programIcon(&proto.Pane{Agent: it.conv.Agent})).Size(14, 14).TextColor(pal.textFaint)
				default:
					// A command has no icon of its own: the slot stays, so
					// that every title starts at one x.
					ui.Box(c).Size(14, 14).Shrink(0)
				}
				// One line each: the title in its column, the detail after
				// it, giving way to the keys, at most 45 % of the row (T3
				// Code's palette caps it so).
				col := pal.text
				if it.danger {
					col = pal.dangerText
				}
				ui.RichText(c, paletteSpans(it.title, it.hits, pal)...).FontSize(fsBody).TextColor(col).
					SingleLine().Ellipsis("…").Width(titleW).Shrink(1).MinWidth(48)
				oneLine(c, it.detail, fsSmall, 0).TextColor(pal.textFaint).Shrink(4).MaxWidthPercent(45)
				ui.Spacer(c)
				for _, k := range it.keys {
					if k != "" {
						kbd(c, pal, k)
					}
				}
			})
		})
		a.pal.scroll.track(c, list).Grow(1).MinHeight(0).Padding(6, 6, 18, 6)
		// The list fades out at the panel's foot rather than end flush on
		// its edge.
		ui.Box(c.Key("foot-fade")).Absolute().Left(0).Right(0).Bottom(0).Height(12).PassThrough().
			LinearGradient(ui.LinearGradient{Angle: 180, From: pal.popover.Alpha(0), To: pal.popover})
	})
	if chosen != nil {
		a.pal.open = false
		chosen()
	}
	if !a.pal.open && chosen == nil {
		a.focusTerminal()
	}
}

func (a *App) confirmView(c *ui.Context, pal *palette) {
	if a.confirm != nil && confirmDialog(c, pal, &a.confirm) {
		a.focusTerminal()
	}
}

// confirmDialog asks *pending's question in c's window, and clears it once
// answered, which it reports.
func confirmDialog(c *ui.Context, pal *palette, pending **confirmation) bool {
	cf := *pending
	run, alt := false, false
	ui.DialogBase(c.Key("confirm"), &cf.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		width := float32(dialogW)
		if cf.alt != "" {
			width = 480 // room for a third button
		}
		ww, _ := c.Size()
		floating(panel.Width(min(width, ww-2*sp4)).Padding(sp5).Gap(sp2).Radius(rSheet).Background(pal.popover), pal).Label(cf.title)
		ui.Text(c, cf.title).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).MaxLines(2)
		ui.Text(c, cf.detail).FontSize(fsBody).TextColor(pal.textMuted).LineHeight(1.45).MaxLines(5)
		ui.Row(c).Gap(sp2).Justify(ui.End).Margin(sp3, 0, 0, 0).Wrap().Children(func() {
			if button(c, "cancel", locales.CommonCancel(), "esc", false, pal) {
				cf.open = false
			}
			if cf.alt != "" && button(c, "alt", cf.alt, "", false, pal) {
				alt = true
			}
			kind := btnPrimary
			if cf.destroy {
				kind = btnDanger
			}
			if kindButton(c, "ok", cf.action, "↵", kind, hControl, rControl, pal, true) || c.Shortcut(0, ui.KeyEnter) {
				run = true
			}
		})
	})
	if run {
		cf.open = false
		cf.run()
	} else if alt {
		cf.open = false
		cf.altRun()
	}
	if cf.open {
		return false
	}
	*pending = nil
	return true
}

// shortcuts are the window's keys that no menu item has.
func (a *App) shortcuts(c *ui.Context) {
	// What a dialog or a mode copied goes to the clipboard, open or not.
	if a.copyText != "" {
		c.WriteClipboard(a.copyText)
		c.Toast(locales.ToastCopied())
		a.copyText = ""
	}
	if a.pal.open || a.help.open || a.xp.open || a.sbr.open || a.sb.on || a.prompt != nil || a.picker.open || a.confirm != nil || a.wtAsk != nil {
		return
	}
	a.paletteShortcuts(c)
	keys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	for i, k := range keys {
		if c.Shortcut(ui.Cmd, k) {
			ids := a.visibleOrder()
			if i < len(ids) {
				a.selectSession(ids[i])
			}
		}
	}
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.selectOffset(1)
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.selectOffset(-1)
	}
}

// paletteWarmAfter is how long after the app has loaded the palette's
// text is measured ahead (warmPalette): late enough to stay out of the
// start, early enough to come before most first opens.
const paletteWarmAfter = 1500 * time.Millisecond

// warmPalette measures, once, the text the palette's first rows show, at
// their sizes and weights, in a frame of its own while nothing else
// happens. The first open then finds the fonts loaded and the text
// shaped, and its first frame lays out as fast as a later one's.
func (a *App) warmPalette(c *ui.Context) {
	w := &a.palWarm
	if w.done || !a.loaded || a.pal.open {
		return
	}
	if w.at.IsZero() {
		w.at = c.Now()
	}
	if d := c.Now().Sub(w.at); d < paletteWarmAfter {
		c.After(paletteWarmAfter - d)
		return
	}
	w.done = true
	measure := func(s string, size float32, weight int) {
		if s != "" {
			c.MeasureText(0, ui.Span{Text: s, Size: size, Weight: weight})
		}
	}
	measure(locales.PalettePlaceholder(), fsProse, 0)
	for _, g := range paletteGroups {
		measure(g(), fsSmall, wMedium)
	}
	for i, it := range a.paletteItems() {
		if i == 24 {
			break
		}
		measure(it.title, fsBody, 0)
		measure(flat(it.detail), fsSmall, 0)
		for _, k := range it.keys {
			measure(k, fsCaption, wMedium)
		}
	}
}

// paletteMark is a session's same-name mark in the palette's detail.
func paletteMark(m string) string {
	if m == "" {
		return ""
	}
	return " · " + m
}
