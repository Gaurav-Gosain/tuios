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

const pathPickerRows = 10

type pathPicker struct {
	windowID string
	base     string
	origin   string
	segs     []string
	query    textedit.Line
	hidden   bool
	home     string

	dir     string
	entries []fileEntry
	err     string
	loaded  bool
	loading bool
	gen     uint64

	matches   []int
	matcher   fuzzy.Matcher
	cursor    int
	scroll    int
	hover     int
	nameWidth int

	boundsX, boundsY, boundsW, boundsH int
	rowTop                             int
	rowIdx                             []int
	visible                            bool
}

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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

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

func (p *pathPicker) refilter() {
	p.matches = p.matches[:0]
	showHidden := p.hidden || strings.HasPrefix(p.query.String(), ".")
	keep := func(e fileEntry) bool { return showHidden || !strings.HasPrefix(e.Name, ".") }
	if p.query.String() == "" {
		for i, e := range p.entries {
			if keep(e) {
				p.matches = append(p.matches, i)
			}
		}
	} else {
		hits := p.matcher.FilterIndex(p.query.String(), len(p.entries), func(i int) string { return p.entries[i].Name })
		for _, h := range hits {
			if keep(p.entries[h.Index]) {
				p.matches = append(p.matches, h.Index)
			}
		}
	}
	p.cursor = clampInt(p.cursor, 0, max(len(p.matches)-1, 0))
}

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

// PathPickerType types text at the filter's caret. A slash ends a name: after
// a folder's name, or after "..", it walks there, and at the start of the line
// it starts an absolute path, as "~/" starts one under the home folder. The
// slash reads the whole filter, wherever the caret is.
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
			p.query.Insert(string(r))
			continue
		}
		q := p.query.String()
		switch {
		case q == "" && p.origin == "" && len(p.segs) == 0:
			p.origin = "/"
			cmd = m.pathPickerRead()
		case q == "~" && p.origin == "" && len(p.segs) == 0:
			p.origin = "~"
			p.query.Set("")
			cmd = m.pathPickerRead()
		case q == "..":
			p.query.Set("")
			cmd = m.pathPickerUp()
		case q == "." || q == "":
			p.query.Set("")
		default:
			if c := m.pathPickerDescend(); c != nil {
				cmd = c
			}
		}
	}
	p.refilter()
	return cmd
}

// PathPickerBackspace takes back the character before the caret, or one step
// of the path when the filter is empty.
func (m *OS) PathPickerBackspace() tea.Cmd {
	p := m.pathPicker
	if p == nil {
		return nil
	}
	if p.query.String() == "" {
		return m.pathPickerUp()
	}
	if changed, _ := p.query.Edit("backspace", "", textedit.LineOpts{}); changed {
		p.refilter()
	}
	return nil
}

// PathPickerKey applies a caret or word key to the filter (textedit.Line):
// left and right, alt+left and alt+right, ctrl+a, ctrl+e, ctrl+k and the word
// keys. On an empty filter the word keys do nothing: backspace there goes up
// a folder, and a word key has no folder to take back. It reports whether the
// key was the filter's. A key that types text is not: it goes to
// PathPickerType, which reads a slash as a step of the path. Line.Edit would
// type it into the filter as it is, and "~/", "/" and "../" would not walk.
func (m *OS) PathPickerKey(key string) bool {
	p := m.pathPicker
	if p == nil {
		return false
	}
	if key == "space" || (len(key) == 1 && key[0] >= ' ' && key[0] <= '~') {
		return false
	}
	if textedit.IsWordDeleteKey(key) && p.query.String() == "" {
		return true
	}
	changed, consumed := p.query.Edit(key, "", textedit.LineOpts{})
	if changed {
		p.refilter()
	}
	return consumed
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
		p.query.Set(e.Name)
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

func (m *OS) pathPickerDescend() tea.Cmd {
	p := m.pathPicker
	e, ok := p.selected()
	if !ok || !e.Dir {
		return nil
	}
	p.segs = append(p.segs, e.Name)
	p.query.Set("")
	return m.pathPickerRead()
}

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
	p.query.Set("")
	return m.pathPickerRead()
}

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
	tail := p.query.String()
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
		case dialectPOSIX, dialectUnknown, dialectFish, dialectCsh:
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
	if p.query.String() != "" {
		p.query.Set("")
		p.refilter()
		return nil
	}
	if p.origin == "" && len(p.segs) == 0 {
		return nil
	}
	p.origin, p.segs = "", nil
	return m.pathPickerRead()
}
