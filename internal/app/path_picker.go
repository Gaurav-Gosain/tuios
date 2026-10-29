package app

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// # The path picker
//
// A small list anchored at the focused pane's cursor that types a path into
// the pane. It lists the pane's folder, filters it as the user types, walks
// into a folder on Tab and out again on shift+Tab, and on Enter pastes the
// path it built at the cursor. It is what a shell's completion does for one
// path, with the folder visible and a fuzzy filter, and it works the same in
// any program that takes typed text: a shell, an agent's prompt, an editor.
//
// It is anchored like the context menu, not stacked like the panels: it
// belongs to one cell of one pane, is dismissed by the next click elsewhere,
// and has no use for a drag offset or a place in the click-to-raise order.
//
// # Where the folder comes from
//
// The pane's shell's own working directory, as the kernel holds it, the same
// source the files section uses for a local pane. An OSC 7 report is taken
// only for its spelling when it names that same folder: any program in the
// pane can print one, and a picker that listed whatever a pane claimed would
// type paths into a folder the shell is not in. A pane on another machine has
// only its daemon's answer, which that daemon has already judged.
//
// # How a folder is read
//
// Through readListing, the files section's reader: the daemon that holds the
// pane lists it, with the link's read rules, and a pane on another machine is
// listed by the daemon on that machine. The read is a command, stamped with a
// generation, so a slow mount never holds the client and a late answer for a
// folder the user has left is dropped. A listing is bounded by the reader's
// cap, and only the rows that fit are drawn.

// pathPickerRows is the most rows the list shows at once.
const pathPickerRows = 10

// pathPicker is the open picker's state. nil on the OS when it is closed.
type pathPicker struct {
	// windowID is the pane the path goes to.
	windowID string
	// base is the pane's folder when the picker opened, absolute. A relative
	// path is relative to it.
	base string
	// origin is how the path starts: "" for relative to base, "/" for the
	// root, "~" for the home folder of the pane's machine.
	origin string
	// segs is the path walked so far below origin, one name per step. ".."
	// is a step up.
	segs []string
	// query is the filter text typed after the path.
	query string
	// hidden lists the names that start with a dot.
	hidden bool
	// home is the home folder on the pane's machine, empty when it is not
	// known. The title shows a folder under it as ~/...
	home string

	// dir is the folder the entries belong to, as it was asked for.
	dir     string
	entries []fileEntry
	err     string
	// loaded is set once the first listing arrived. Nothing is drawn before
	// it, so the picker never opens as an empty box that then grows.
	loaded bool
	// loading is set while a read is out. The previous rows stay up until it
	// answers, so walking into a folder does not flash an empty list.
	loading bool
	gen     uint64

	// matches holds indexes into entries, best first.
	matches []int
	matcher fuzzy.Matcher
	cursor  int
	scroll  int
	hover   int
	// nameWidth is the widest name in entries, measured once per listing so
	// the box does not change width as the filter narrows the list.
	nameWidth int

	// Where the box landed on the last frame, for the mouse.
	boundsX, boundsY, boundsW, boundsH int
	// rowY maps a screen row to the match it shows, -1 for none.
	rowTop  int
	rowIdx  []int
	visible bool
}

// pathPickerListMsg is one finished folder read for the picker.
type pathPickerListMsg struct {
	gen     uint64
	dir     string
	base    string
	home    string
	entries []fileEntry
	err     string
}

// PathPickerOpen reports whether the path picker is up.
func (m *OS) PathPickerOpen() bool { return m.pathPicker != nil }

// ClosePathPicker closes the picker without typing anything.
func (m *OS) ClosePathPicker() { m.pathPicker = nil }

// OpenPathPicker opens the picker on the focused pane. The folder is read by
// the command it returns, and the picker draws once that answers.
func (m *OS) OpenPathPicker() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil {
		m.ShowNotification("No pane is focused.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	switch {
	case w.CwdHost != "":
		m.ShowNotification("The shell in this pane is on "+w.CwdHost+". tuios cannot list its files.", "warning", m.Settings.NotificationDuration*2)
		return nil
	case w.CopyModeVisible():
		m.ShowNotification("Cannot pick a path in copy mode. Exit copy mode first.", "warning", m.Settings.NotificationDuration)
		return nil
	case m.DrawsTML(w):
		m.ShowNotification("This pane does not take typed text.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	hidden := m.UserConfig != nil && m.UserConfig.PathPicker.ShowHidden
	var gen uint64 = 1
	if m.pathPicker != nil {
		gen = m.pathPicker.gen + 1
	}
	p := &pathPicker{windowID: w.ID, hidden: hidden, gen: gen, hover: -1, loading: true}
	m.pathPicker = p

	local := m.paneIsLocal(w)
	pgid, cwd, cached := w.ShellPgid, w.Cwd, ""
	if local {
		cached = w.CWD()
	}
	client, host := m.DaemonClient, m.AttachedHost
	origin := w.ID
	return func() tea.Msg {
		homeCh := make(chan string, 1)
		go func() { homeCh <- pathPickerHome(local, client, origin) }()
		base := pathPickerBase(local, pgid, cwd, cached)
		if base == "" {
			return pathPickerListMsg{gen: gen, err: "tuios cannot tell which folder this pane is in."}
		}
		msg := readListing(client, host, origin, base, true, 0, false)
		return pathPickerListMsg{gen: gen, dir: base, base: base, home: <-homeCh, entries: msg.Entries, err: msg.Err}
	}
}

// pathPickerHome is the home folder on the pane's machine. A pane on another
// machine asks its daemon to list "~" with room for one name: the daemon
// expands it to its own home and names the folder it listed. A daemon too old
// to expand it names "~" back, and the title then keeps the full path.
func pathPickerHome(local bool, client *session.TUIClient, origin string) string {
	if local {
		home, _ := os.UserHomeDir()
		return home
	}
	if client == nil {
		return ""
	}
	listing, err := client.ReadDir(origin, "~", 1, true)
	if err != nil || listing == nil || !filepath.IsAbs(listing.Dir) {
		return ""
	}
	return filepath.Clean(listing.Dir)
}

// homeShort writes a path under home as ~/..., the way a shell prompt shows
// it. With no home it is the path as it is.
func homeShort(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// pathPickerBase is the folder the picker opens on. For a local pane it is
// the shell's folder as the kernel holds it, spelled the way the pane's OSC 7
// report spells it when the two are the same folder. It runs off the update
// goroutine, because it reads the process table.
func pathPickerBase(local bool, pgid int, cwd, cached string) string {
	if !local {
		return cwd
	}
	if dir, ok := terminal.ShellCWD(pgid); ok && dir != "" {
		if cwd != "" && sameDir(dir, cwd) {
			return cwd
		}
		return dir
	}
	if cached != "" {
		return cached
	}
	return cwd
}

// HandlePathPickerList applies a finished read, or drops it.
func (m *OS) HandlePathPickerList(msg pathPickerListMsg) {
	p := m.pathPicker
	if p == nil {
		return
	}
	// The first answer carries the pane's folder. It is kept even when a
	// key typed before it arrived has asked for another folder since,
	// because every relative step is relative to it.
	if msg.base != "" && p.base == "" {
		p.base = msg.base
	}
	if msg.home != "" {
		p.home = msg.home
	}
	if msg.gen != p.gen {
		return
	}
	if !p.loaded && msg.err != "" && msg.dir == "" {
		m.pathPicker = nil
		m.ShowNotification(msg.err, "warning", m.Settings.NotificationDuration)
		return
	}
	p.loaded, p.loading = true, false
	p.dir, p.err = msg.dir, msg.err
	p.entries = msg.entries
	p.nameWidth = 0
	for _, e := range p.entries {
		p.nameWidth = max(p.nameWidth, len(e.Name)+boolInt(e.Dir))
	}
	p.cursor, p.scroll = 0, 0
	p.refilter()
}

// boolInt is 1 for true.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// listDir is the folder the walked path names, in the form the reader takes.
func (p *pathPicker) listDir() string {
	switch p.origin {
	case "/":
		return "/" + filepath.Join(p.segs...)
	case "~":
		if len(p.segs) == 0 {
			return "~"
		}
		return "~/" + filepath.Join(p.segs...)
	}
	return filepath.Join(append([]string{p.base}, p.segs...)...)
}

// prefix is the path walked so far as the filter line shows it: "Apps/rex/",
// "~/", "/etc/", or "" at the pane's folder.
func (p *pathPicker) prefix() string {
	rel := strings.Join(p.segs, "/")
	if rel != "" {
		rel += "/"
	}
	switch p.origin {
	case "/":
		return "/" + rel
	case "~":
		return "~/" + rel
	}
	return rel
}

// refilter rebuilds the matches for the query and keeps the cursor on them.
func (p *pathPicker) refilter() {
	p.matches = p.matches[:0]
	showHidden := p.hidden || strings.HasPrefix(p.query, ".")
	keep := func(e fileEntry) bool { return showHidden || !strings.HasPrefix(e.Name, ".") }
	if p.query == "" {
		for i, e := range p.entries {
			if keep(e) {
				p.matches = append(p.matches, i)
			}
		}
	} else {
		hits := p.matcher.FilterIndex(p.query, len(p.entries), func(i int) string { return p.entries[i].Name })
		for _, h := range hits {
			if keep(p.entries[h.Index]) {
				p.matches = append(p.matches, h.Index)
			}
		}
	}
	p.cursor = clampInt(p.cursor, 0, max(len(p.matches)-1, 0))
}

// selected is the entry under the cursor.
func (p *pathPicker) selected() (fileEntry, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return fileEntry{}, false
	}
	return p.entries[p.matches[p.cursor]], true
}

// PathPickerMove moves the cursor by delta matches, wrapping at the ends the
// way every list does when it is asked to.
func (m *OS) PathPickerMove(delta int) {
	p := m.pathPicker
	if p == nil || len(p.matches) == 0 {
		return
	}
	n := len(p.matches)
	next := p.cursor + delta
	switch {
	case next < 0 && p.cursor == 0 && m.Settings.WrapLists:
		next = n - 1
	case next >= n && p.cursor == n-1 && m.Settings.WrapLists:
		next = 0
	}
	p.cursor = clampInt(next, 0, n-1)
}

// PathPickerType adds typed text to the filter. A slash ends a name: after a
// folder's name, or after "..", it walks there, and at the start of the line
// it starts an absolute path, as "~/" starts one under the home folder.
func (m *OS) PathPickerType(s string) tea.Cmd {
	p := m.pathPicker
	if p == nil {
		return nil
	}
	var cmd tea.Cmd
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			continue
		}
		if r != '/' {
			p.query += string(r)
			continue
		}
		switch {
		case p.query == "" && p.origin == "" && len(p.segs) == 0:
			p.origin = "/"
			cmd = m.pathPickerRead()
		case p.query == "~" && p.origin == "" && len(p.segs) == 0:
			p.origin, p.query = "~", ""
			cmd = m.pathPickerRead()
		case p.query == "..":
			p.query = ""
			cmd = m.pathPickerUp()
		case p.query == "." || p.query == "":
			p.query = ""
		default:
			if c := m.pathPickerDescend(); c != nil {
				cmd = c
			}
		}
	}
	p.refilter()
	return cmd
}

// PathPickerBackspace takes back one character of the filter, or one step of
// the path when the filter is empty.
func (m *OS) PathPickerBackspace() tea.Cmd {
	p := m.pathPicker
	if p == nil {
		return nil
	}
	if p.query != "" {
		r := []rune(p.query)
		p.query = string(r[:len(r)-1])
		p.refilter()
		return nil
	}
	return m.pathPickerUp()
}

// PathPickerDeleteWord drops the last word of the query, by the rule of
// DropLastWord. On an empty query it does nothing: backspace there goes up a
// folder, and a word key has no folder to take back.
func (m *OS) PathPickerDeleteWord() tea.Cmd {
	p := m.pathPicker
	if p == nil || p.query == "" {
		return nil
	}
	p.query = textedit.DropLastWord(p.query)
	p.refilter()
	return nil
}

// PathPickerTab walks into the folder under the cursor. On a file it completes
// the name into the filter, so Enter then types it.
func (m *OS) PathPickerTab() tea.Cmd {
	p := m.pathPicker
	if p == nil {
		return nil
	}
	e, ok := p.selected()
	if !ok {
		return nil
	}
	if !e.Dir {
		p.query = e.Name
		p.refilter()
		return nil
	}
	return m.pathPickerDescend()
}

// PathPickerUp goes one step up the path.
func (m *OS) PathPickerUp() tea.Cmd {
	if m.pathPicker == nil {
		return nil
	}
	return m.pathPickerUp()
}

// pathPickerDescend walks into the folder under the cursor.
func (m *OS) pathPickerDescend() tea.Cmd {
	p := m.pathPicker
	e, ok := p.selected()
	if !ok || !e.Dir {
		return nil
	}
	p.segs = append(p.segs, e.Name)
	p.query = ""
	return m.pathPickerRead()
}

// pathPickerUp takes the last step off the path. Past the pane's folder it
// walks up with "..". At the root or the home folder it goes back to the
// pane's folder, which is the same as erasing the "/" or "~/" typed.
func (m *OS) pathPickerUp() tea.Cmd {
	p := m.pathPicker
	switch {
	case len(p.segs) > 0 && p.segs[len(p.segs)-1] != "..":
		p.segs = p.segs[:len(p.segs)-1]
	case p.origin != "":
		p.origin, p.segs = "", nil
	case p.base == "":
		return nil
	case filepath.Dir(p.listDir()) == p.listDir():
		return nil
	default:
		p.segs = append(p.segs, "..")
	}
	p.query = ""
	return m.pathPickerRead()
}

// pathPickerRead asks for the folder the path now names.
func (m *OS) pathPickerRead() tea.Cmd {
	p := m.pathPicker
	p.gen++
	p.loading = true
	p.hover = -1
	dir, gen, origin := p.listDir(), p.gen, p.windowID
	client, host := m.DaemonClient, m.AttachedHost
	return func() tea.Msg {
		msg := readListing(client, host, origin, dir, true, 0, false)
		return pathPickerListMsg{gen: gen, dir: dir, entries: msg.Entries, err: msg.Err}
	}
}

// PathPickerToggleHidden shows or hides the names that start with a dot.
func (m *OS) PathPickerToggleHidden() {
	if p := m.pathPicker; p != nil {
		p.hidden = !p.hidden
		p.refilter()
	}
}

// PathPickerAccept types the path under the cursor into the pane and closes
// the picker. With no match it types the path walked and the filter as typed,
// so a name that does not exist yet can still be typed.
func (m *OS) PathPickerAccept() {
	p := m.pathPicker
	if p == nil {
		return
	}
	m.pathPicker = nil
	tail := p.query
	if e, ok := p.selected(); ok {
		tail = e.Name
		if e.Dir {
			tail += "/"
		}
	}
	w := m.windowByID(p.windowID)
	if w == nil {
		m.ShowNotification("The pane closed before the path was typed.", "warning", m.Settings.NotificationDuration)
		return
	}
	text, refusal := pathPickerText(m.paneDialect(w), p.origin, strings.Join(p.segs, "/"), tail)
	if refusal != "" {
		m.ShowNotification(refusal, "warning", m.Settings.NotificationDuration*2)
		return
	}
	if text == "" {
		return
	}
	if err := w.Paste(text); err != nil {
		m.ShowNotification("The path was not typed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
	}
}

// pathPickerText is the text typed for a path: the walked steps and the last
// name below origin, quoted the way the pane's shell reads it back, by the
// rules a dropped file follows (quoteDropPath). Under "~" the tilde stays
// outside the quotes in the shells that expand it only there. refusal says
// why the path cannot be typed at all.
func pathPickerText(d pasteDialect, origin, walked, tail string) (text, refusal string) {
	rel := walked
	if tail != "" {
		if rel != "" {
			rel += "/"
		}
		rel += tail
	}
	switch origin {
	case "/":
		return quoteDropPath(d, "/"+rel)
	case "~":
		if rel == "" {
			return "~/", ""
		}
		switch d {
		case dialectPOSIX, dialectFish, dialectCsh:
			text, refusal = quoteDropPath(d, rel)
			if refusal != "" {
				return "", refusal
			}
			return "~/" + text, ""
		}
		return quoteDropPath(d, "~/"+rel)
	}
	if rel == "" {
		return "", ""
	}
	return quoteDropPath(d, rel)
}

// PathPickerClick handles a press at a screen cell. A press on a row picks it,
// a press anywhere else on the box does nothing, and a press outside closes
// the picker. It reports whether the press was the picker's.
func (m *OS) PathPickerClick(x, y int) bool {
	p := m.pathPicker
	if p == nil {
		return false
	}
	if !p.visible || !p.contains(x, y) {
		m.pathPicker = nil
		return true
	}
	if i := p.matchAt(y); i >= 0 {
		p.cursor = i
		m.PathPickerAccept()
	}
	return true
}

// PathPickerHover marks the row under the pointer.
func (m *OS) PathPickerHover(x, y int) {
	p := m.pathPicker
	if p == nil {
		return
	}
	p.hover = -1
	if p.visible && p.contains(x, y) {
		p.hover = p.matchAt(y)
	}
}

// PathPickerWheel moves the list under the pointer one row. It reports whether
// the wheel was over the picker.
func (m *OS) PathPickerWheel(x, y int, up bool) bool {
	p := m.pathPicker
	if p == nil || !p.visible || !p.contains(x, y) {
		return false
	}
	// The best match is drawn at the bottom, next to the filter line, so the
	// wheel up shows the matches further down the order.
	if up {
		m.PathPickerMove(1)
	} else {
		m.PathPickerMove(-1)
	}
	return true
}

func (p *pathPicker) contains(x, y int) bool {
	return x >= p.boundsX && x < p.boundsX+p.boundsW && y >= p.boundsY && y < p.boundsY+p.boundsH
}

// matchAt is the match a screen row shows, or -1.
func (p *pathPicker) matchAt(y int) int {
	i := y - p.rowTop
	if i < 0 || i >= len(p.rowIdx) {
		return -1
	}
	return p.rowIdx[i]
}

// PathPickerClearQuery empties the filter, and the path with it when the
// filter is already empty, as ctrl+u clears a shell's line.
func (m *OS) PathPickerClearQuery() tea.Cmd {
	p := m.pathPicker
	if p == nil {
		return nil
	}
	if p.query != "" {
		p.query = ""
		p.refilter()
		return nil
	}
	if p.origin == "" && len(p.segs) == 0 {
		return nil
	}
	p.origin, p.segs = "", nil
	return m.pathPickerRead()
}
