package app

import (
	"cmp"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// # The Files view
//
// A full screen view of the folders of every machine this client reaches:
// the focused pane's machine, this machine, and every linked host. Ctrl+B f
// opens it at the focused pane's folder. It is an overlay kind like the agents
// view: it fills the screen between the dock's rows and owns the keyboard.
//
// A side is one machine and one folder. It holds the folder as a fileListing,
// read the way every other view reads a folder (file_listing.go), with the
// details of each row beside it: size, time and mode from file-list. The
// cursor, the sort, the filter and the hidden switch are the view's own.
//
// Quick look is the view's preview. Space puts it beside the list, where it
// follows the cursor as it follows the rail's, with all its views, its folder
// walk and its zoom. See quicklook.go: the panel is placed in the view's box
// while the view owns it.
//
// Nothing runs while the view is at rest: a folder is read when the person
// goes to it, and no timer runs while no read is out.

const overlayKindFiles = "files"

// filesReadingAfter is how long a read runs before the machine row says so.
// A local read lands well inside it, so the row does not blink.
const filesReadingAfter = 150 * time.Millisecond

// filesCallTimeout bounds one file-list call to another machine.
const filesCallTimeout = 10 * time.Second

// filesSort is the order of a side's rows. Folders are always first.
type filesSort uint8

const (
	filesSortName filesSort = iota
	filesSortSize
	filesSortTime
)

// String is the sort's word in the crumbs row.
func (s filesSort) String() string {
	switch s {
	case filesSortSize:
		return "size"
	case filesSortTime:
		return "time"
	}
	return "name"
}

// filesPrompt is the line being typed at the foot of the view.
type filesPrompt uint8

const (
	filesNoPrompt filesPrompt = iota
	filesPromptFilter
	filesPromptGoTo
	// filesPromptRename edits the cursor row's name in its row, and
	// filesPromptNew asks for a new name. filesPromptCopy and
	// filesPromptMove ask where to send the marks with one side on screen.
	filesPromptRename
	filesPromptNew
	filesPromptCopy
	filesPromptMove
)

// filesSide is one machine and one folder.
type filesSide struct {
	// host is the machine, "" for the machine of this client's daemon.
	host string
	// fileListing is the folder on screen. Dir is empty until a read lands.
	fileListing
	// infos is the details of each entry, by its index in Entries. A read
	// with no details leaves it short, and a row past it draws no size.
	infos []session.FileInfo
	// home is the home folder on the side's machine, for the crumbs.
	home string
	// want is the folder asked for, and gen stamps the read out for it.
	want string
	gen  uint64
	// loading says a read is out, since loadingAt.
	loading   bool
	loadingAt time.Time
	// pick is the name the cursor goes to when the read lands.
	pick string
	// rows is the entries on screen, as indexes into Entries, after the
	// hidden switch, the filter and the sort.
	rows   []int
	cursor int
	scroll int
	// id names the side to a read that lands after a tab closed.
	id uint64
	// sort, reverse, hidden and filter are the side's own: two sides of
	// one tab often want different orders.
	sort    filesSort
	reverse bool
	hidden  bool
	filter  string
	// marks is the marked rows, by name.
	marks map[string]bool
	// homeIfMissing reads the home folder when the read out now fails: a
	// side moved to another machine tries the same folder there first.
	homeIfMissing bool
	// landed is the copies that landed here, by name, with the time the
	// listing first had them: their rows say "checked" for a few seconds.
	// settle counts the reads again for a copy that ended before its file
	// was in the listing.
	landed map[string]time.Time
	settle int
	// rowsTop is the screen row of the first listing row, rowsN the rows
	// that fit, box the side on the screen, crumbsY the crumbs row and
	// crumbs their click targets, all as last drawn.
	rowsTop, rowsN int
	box            filesBox
	crumbsY        int
	crumbs         []filesCrumb
}

// filesTab is one tab of the view: one side, or two side by side.
type filesTab struct {
	sides [2]*filesSide
	// two says both sides are on screen, and focus is the side with the
	// keys.
	two   bool
	focus int
	// ratio is the left side's share of the width, in percent. zoom shows
	// the focused side alone, and keeps the other for the next Z.
	ratio int
	zoom  bool
}

// filesRatioDefault is the divider's place: half way.
const filesRatioDefault = 50

// cur is the side with the keys.
func (t *filesTab) cur() *filesSide { return t.sides[t.focus] }

// shown reports whether both sides are drawn.
func (t *filesTab) shown() bool { return t.two && !t.zoom }

// short is dir with the side's home folder written as ~, the way a shell
// prompt writes it.
func (s *filesSide) short(dir string) string {
	h := s.home
	switch {
	case h == "" || h == "/":
		return dir
	case dir == h:
		return "~"
	case strings.HasPrefix(dir, h+"/"):
		return "~" + dir[len(h):]
	}
	return dir
}

// explorerState is the Files view.
type explorerState struct {
	open bool
	// tabs is the view's tabs, and tab the one on screen.
	tabs []*filesTab
	tab  int
	// ids hands out side ids.
	ids uint64
	// origin is the pane the view was opened from. I types into it.
	origin string
	// sort, reverse and hidden are what a new side starts with: the last
	// a person chose.
	sort    filesSort
	reverse bool
	hidden  bool
	// prompt is the line being typed, and input its text. renaming is the
	// name a rename started from.
	prompt   filesPrompt
	input    string
	renaming string
	// clip is what y took, and undo the changes u can take back, newest
	// last. See files_view_ops.go.
	clip *filesClip
	undo []filesUndo
	// jobs is the copies the view started, a job per action, jobSeq the last
	// job id, and jobOpen the jobs whose files the Copies list shows. They
	// outlive the view, so a job that ends after it closed still tells its
	// end once. copyHits is the Copies list's click targets as last drawn.
	// See files_view_copies.go.
	jobs     []*filesJob
	jobSeq   int
	jobOpen  map[int]bool
	copyHits []filesCopyHit
	// machineFor is the side the machine picker was opened for, and
	// waitHost a side that opens on a machine once its link is up. See
	// files_view_machine.go.
	machineFor uint64
	waitHost   filesWaitHost
	// look says quick look is beside the list.
	look bool
	// gen moves when anything the view draws changes, for its frame cache.
	gen uint64
	// frame is the last frame drawn and what it was drawn for.
	frame filesFrame
	// lookBox is the box quick look is drawn in, tabHits the tabs' click
	// targets on the top bar, and divX the divider's column, all as last
	// drawn. divX is -1 with one side.
	lookBox filesBox
	tabHits []filesTabHit
	divX    int
	// segHits is the two machine names of the tab on screen when one side
	// is drawn at a time: a click on one shows it.
	segHits [2]filesTabHit
	// dividerDrag says a press on the divider is held: the motion moves it.
	// clickDiv says the last click was on the divider, for a double click.
	dividerDrag, clickDiv bool
	// tabsY is the screen row of the top bar.
	tabsY int
	// clickAt is when the cursor row was last clicked, for a double click.
	clickAt time.Time
	// hints is the footer's key hints as last drawn, on the screen row
	// hintsY. A click on one runs it.
	hints  []filesHint
	hintsY int
}

// filesBox is a rectangle on the screen.
type filesBox struct{ x, y, w, h int }

// contains reports whether the cell x, y is in the box.
func (b filesBox) contains(x, y int) bool {
	return x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// filesTabHit is one tab on the top bar and the columns it was drawn on.
type filesTabHit struct{ x0, x1 int }

// filesCrumb is one folder of the crumbs row and the columns it was drawn on.
type filesCrumb struct {
	dir    string
	x0, x1 int
}

// filesListMsg is a finished read of a side's folder.
type filesListMsg struct {
	id    uint64
	gen   uint64
	l     fileListing
	infos []session.FileInfo
	home  string
}

// filesReadingMsg asks for a frame once a read has run filesReadingAfter.
type filesReadingMsg struct{ id, gen uint64 }

// FilesViewOpen reports whether the Files view is up.
func (m *OS) FilesViewOpen() bool { return m.explorer.open }

// FilesViewPrompt reports whether a line is being typed in the view, where
// every printable key is text.
func (m *OS) FilesViewPrompt() bool { return m.explorer.open && m.explorer.prompt != filesNoPrompt }

// OpenFilesView opens the view at the focused pane's folder, on the pane's
// machine. With no pane it opens at the home folder of this machine.
func (m *OS) OpenFilesView() tea.Cmd {
	if m.explorer.open {
		return nil
	}
	if m.learnOff(learnNoteFiles) {
		return nil
	}
	dir, host, origin := "~", m.AttachedHost, ""
	if w := m.GetFocusedWindow(); w != nil {
		origin = w.ID
		if w.CwdHost == "" {
			if d := m.paneDir(w); d != "" {
				dir = d
			}
		}
		if w.Host != "" {
			host = w.Host
		}
	}
	return m.openFilesViewAt(host, dir, origin)
}

// openFilesViewAt opens the view on dir of host.
func (m *OS) openFilesViewAt(host, dir, origin string) tea.Cmd {
	e := &m.explorer
	m.filesGitStop()
	*e = explorerState{open: true, origin: origin, sort: e.sort, reverse: e.reverse, hidden: e.hidden, gen: e.gen + 1, ids: e.ids, divX: -1, undo: e.undo, clip: e.clip, jobs: e.jobs, jobSeq: e.jobSeq, jobOpen: e.jobOpen}
	t := &filesTab{ratio: filesRatioDefault}
	t.sides[0] = m.newFilesSide(host)
	e.tabs = []*filesTab{t}
	if m.quickLook.open {
		m.dropQuickLook()
	}
	m.reconcileOverlayZOrder()
	m.raiseOverlay(overlayKindFiles)
	return m.filesRead(dir, "")
}

// CloseFilesView takes the view down. A read still out lands and is dropped.
func (m *OS) CloseFilesView() {
	e := &m.explorer
	if !e.open {
		return
	}
	if m.quickLook.open && m.quickLook.files {
		m.dropQuickLook()
	}
	m.filesGitStop()
	*e = explorerState{sort: e.sort, reverse: e.reverse, hidden: e.hidden, gen: e.gen + 1, ids: e.ids, divX: -1, undo: e.undo, clip: e.clip, jobs: e.jobs, jobSeq: e.jobSeq, jobOpen: e.jobOpen}
}

// filesCaller is how the view asks a machine for a listing, or nil for a
// client with no daemon, which reads its own disk.
func (m *OS) filesCaller() func(host, verb string, params map[string]any) (json.RawMessage, error) {
	if m.DaemonClient == nil && m.transfers.call == nil {
		return nil
	}
	return m.verbOnHostCallerFor(nil, filesCallTimeout)
}

// filesRead asks for dir on the side's machine. pick is the row to put the
// cursor on when it lands. The old rows stay up while it runs.
func (m *OS) filesRead(dir, pick string) tea.Cmd {
	return m.filesReadSide(m.filesCur(), dir, pick)
}

// filesReadSide asks for dir on side s's machine.
func (m *OS) filesReadSide(s *filesSide, dir, pick string) tea.Cmd {
	return m.filesReadSideAfter(s, dir, pick, 0)
}

// filesReadSideAfter is filesReadSide with the read put off by wait.
func (m *OS) filesReadSideAfter(s *filesSide, dir, pick string, wait time.Duration) tea.Cmd {
	s.gen++
	s.want, s.pick = dir, pick
	s.loading, s.loadingAt = true, time.Now()
	id, gen, host := s.id, s.gen, s.host
	call := m.filesCaller()
	read := func() tea.Msg {
		l, infos, home := readListingDetails(call, host, dir)
		return filesListMsg{id: id, gen: gen, l: l, infos: infos, home: home}
	}
	tick := tea.Tick(wait+filesReadingAfter, func(time.Time) tea.Msg { return filesReadingMsg{id: id, gen: gen} })
	if wait > 0 {
		return tea.Batch(tea.Tick(wait, func(time.Time) tea.Msg { return read() }), tick)
	}
	return tea.Batch(read, tick)
}

// HandleFilesList applies a finished read, or drops a stale one. A read of
// the folder on screen keeps the cursor on its row. A read of another folder
// starts at the top, or on the row the read was asked to pick, and drops the
// filter, which was for the folder left.
func (m *OS) HandleFilesList(msg filesListMsg) tea.Cmd {
	e := &m.explorer
	s := m.filesSideByID(msg.id)
	if !e.open || s == nil || msg.gen != s.gen {
		return nil
	}
	if s.homeIfMissing {
		s.homeIfMissing = false
		if msg.l.Err != "" {
			return m.filesReadSide(s, "~", "")
		}
	}
	s.loading = false
	e.gen++
	if msg.l.Err != "" && len(s.Entries) > 0 {
		// A folder that cannot be read leaves the one on screen, and says
		// why in the machine row.
		s.Err = msg.l.Err
		return nil
	}
	keep := ""
	if en, _, ok := filesSideEntry(s); ok {
		keep = en.Name
	}
	moved := s.Dir != msg.l.Dir
	s.fileListing, s.infos = msg.l, msg.infos
	if s.Dir == "" {
		s.Dir = s.want
	}
	if msg.home != "" {
		s.home = msg.home
	}
	pick := s.pick
	s.pick = ""
	if moved {
		s.cursor, s.scroll = 0, 0
		s.filter = ""
		s.marks = nil
		s.landed = nil
	} else if pick == "" {
		pick = keep
	}
	m.filesRebuildSide(s)
	filesPickSide(s, pick)
	return m.filesSettleLanded(s)
}

// HandleFilesReading redraws the machine row once a read has run long
// enough to say so.
func (m *OS) HandleFilesReading(msg filesReadingMsg) {
	if s := m.filesSideByID(msg.id); m.explorer.open && s != nil && s.loading && msg.gen == s.gen {
		m.explorer.gen++
	}
}

// filesRebuild works out the rows on screen from the listing, the hidden
// switch, the filter and the sort. It runs when one of them changes.
func (m *OS) filesRebuild() { m.filesRebuildSide(m.filesCur()) }

// filesRebuildSide is filesRebuild for side s.
func (m *OS) filesRebuildSide(s *filesSide) {
	e := &m.explorer
	s.rows = s.rows[:0]
	filter := strings.ToLower(s.filter)
	for i, en := range s.Entries {
		if !s.hidden && strings.HasPrefix(en.Name, ".") {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(en.Name), filter) {
			continue
		}
		s.rows = append(s.rows, i)
	}
	info := func(i int) session.FileInfo {
		if i < len(s.infos) {
			return s.infos[i]
		}
		return session.FileInfo{}
	}
	if s.sort != filesSortName || s.reverse {
		slices.SortStableFunc(s.rows, func(a, b int) int {
			ea, eb := s.Entries[a], s.Entries[b]
			if ea.Dir != eb.Dir {
				if ea.Dir {
					return -1
				}
				return 1
			}
			var c int
			switch s.sort {
			case filesSortSize:
				c = cmp.Compare(info(b).Size, info(a).Size)
			case filesSortTime:
				c = cmp.Compare(info(b).MTime, info(a).MTime)
			}
			if c == 0 {
				// The listing is in name order already.
				c = cmp.Compare(a, b)
			}
			if s.reverse {
				c = -c
			}
			return c
		})
	}
	s.cursor = max(min(s.cursor, len(s.rows)-1), 0)
	e.gen++
}

// filesPick puts the cursor on the row named name, when it is on screen.
func (m *OS) filesPick(name string) { filesPickSide(m.filesCur(), name) }

// filesPickSide is filesPick for side s.
func filesPickSide(s *filesSide, name string) {
	if name == "" {
		return
	}
	for i, r := range s.rows {
		if s.Entries[r].Name == name {
			s.cursor = i
			return
		}
	}
}

// filesCursorEntry is the entry under the cursor.
func (m *OS) filesCursorEntry() (fileEntry, session.FileInfo, bool) {
	return filesSideEntry(m.filesCur())
}

// filesSideEntry is the entry under side s's cursor.
func filesSideEntry(s *filesSide) (fileEntry, session.FileInfo, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return fileEntry{}, session.FileInfo{}, false
	}
	i := s.rows[s.cursor]
	var info session.FileInfo
	if i < len(s.infos) {
		info = s.infos[i]
	}
	return s.Entries[i], info, true
}

// filesJoin joins a name to the side's folder, in the side's machine's form.
func (m *OS) filesJoin(name string) string { return m.filesJoinSide(m.filesCur(), name) }

// filesJoinSide is filesJoin for side s.
func (m *OS) filesJoinSide(s *filesSide, name string) string {
	if s.host != "" || m.AttachedHost != "" {
		// A far machine's path is in its own form. Every machine tuios
		// links to but Windows uses slashes, and path.Join keeps them.
		if strings.Contains(s.Dir, "\\") {
			return s.Dir + "\\" + name
		}
		return path.Join(s.Dir, name)
	}
	return filepath.Join(s.Dir, name)
}

// FilesMove moves the cursor by n rows, held to the rows.
func (m *OS) FilesMove(n int) {
	s := m.filesCur()
	if len(s.rows) == 0 {
		return
	}
	s.cursor = max(min(s.cursor+n, len(s.rows)-1), 0)
	m.explorer.gen++
}

// FilesPage moves the cursor by a screen of rows.
func (m *OS) FilesPage(down bool) {
	n := max(m.filesCur().rowsN-1, 1)
	if !down {
		n = -n
	}
	m.FilesMove(n)
}

// FilesEdge moves the cursor to the first or the last row.
func (m *OS) FilesEdge(last bool) {
	if last {
		m.FilesMove(len(m.filesCur().rows))
		return
	}
	m.FilesMove(-len(m.filesCur().rows))
}

// FilesOpen goes into the folder under the cursor. On a file it opens quick
// look beside the list, or takes the keyboard into it when it is open.
func (m *OS) FilesOpen() tea.Cmd {
	en, info, ok := m.filesCursorEntry()
	if !ok {
		return nil
	}
	if en.Dir || info.LinkKind == "dir" {
		return m.filesRead(m.filesJoin(en.Name), "")
	}
	if !m.explorer.look {
		return m.FilesToggleLook()
	}
	m.QuickLookGoInside()
	return nil
}

// FilesParent goes to the folder above, with the cursor on the folder left.
func (m *OS) FilesParent() tea.Cmd {
	s := m.filesCur()
	dir := s.Dir
	if dir == "" {
		return nil
	}
	parent, name := path.Dir(dir), path.Base(dir)
	if s.host == "" && m.AttachedHost == "" {
		parent, name = filepath.Dir(dir), filepath.Base(dir)
	}
	if parent == dir {
		return nil
	}
	return m.filesRead(parent, name)
}

// FilesHome goes to the home folder of the side's machine.
func (m *OS) FilesHome() tea.Cmd { return m.filesRead("~", "") }

// FilesFind searches below the side's folder, in the palette over the view.
// The search reads through the pane the view came from, so it finds on that
// pane's machine only.
func (m *OS) FilesFind() tea.Cmd {
	s := m.filesCur()
	root := cmp.Or(s.Dir, s.want)
	paneHost := m.AttachedHost
	if w := m.windowByID(m.explorer.origin); w != nil && w.Host != "" {
		paneHost = w.Host
	}
	if s.host != paneHost || root == "" || root == "~" {
		m.ShowNotification("Find works in the folders of the pane's machine. Go there first.", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.openFileSearchAt(root, m.explorer.origin, true)
}

// FilesToggleHidden shows or hides the names that start with a dot.
func (m *OS) FilesToggleHidden() {
	e := &m.explorer
	s := m.filesCur()
	en, _, ok := m.filesCursorEntry()
	s.hidden = !s.hidden
	e.hidden = s.hidden
	m.filesRebuild()
	if ok {
		m.filesPick(en.Name)
	}
}

// FilesSort takes the next order: name, size, time. reverse turns the
// order round instead.
func (m *OS) FilesSort(reverse bool) {
	e := &m.explorer
	s := m.filesCur()
	en, _, ok := m.filesCursorEntry()
	if reverse {
		s.reverse = !s.reverse
	} else {
		s.sort = (s.sort + 1) % 3
	}
	e.sort, e.reverse = s.sort, s.reverse
	m.filesRebuild()
	if ok {
		m.filesPick(en.Name)
	}
}

// FilesStartPrompt opens the filter line or the go to line.
func (m *OS) FilesStartPrompt(goTo bool) {
	e := &m.explorer
	e.prompt, e.input = filesPromptFilter, m.filesCur().filter
	if goTo {
		e.prompt, e.input = filesPromptGoTo, ""
	}
	e.gen++
}

// FilesPromptType adds text to the line being typed. The filter filters as
// it is typed.
func (m *OS) FilesPromptType(s string) {
	e := &m.explorer
	e.input += s
	m.filesPromptChanged()
}

// FilesPromptBackspace takes the last character off the line.
func (m *OS) FilesPromptBackspace() {
	e := &m.explorer
	if e.input == "" {
		return
	}
	r := []rune(e.input)
	e.input = string(r[:len(r)-1])
	m.filesPromptChanged()
}

func (m *OS) filesPromptChanged() {
	e := &m.explorer
	if e.prompt == filesPromptFilter {
		s := m.filesCur()
		s.filter = e.input
		s.cursor = 0
		m.filesRebuild()
	}
	e.gen++
}

// FilesPromptCancel drops the line. A filter goes with it.
func (m *OS) FilesPromptCancel() {
	e := &m.explorer
	if e.prompt == filesPromptFilter {
		en, _, ok := m.filesCursorEntry()
		m.filesCur().filter = ""
		m.filesRebuild()
		if ok {
			m.filesPick(en.Name)
		}
	}
	e.prompt, e.input, e.renaming = filesNoPrompt, "", ""
	e.gen++
}

// FilesPromptSubmit ends the line. A filter stays in effect; go to goes.
func (m *OS) FilesPromptSubmit() tea.Cmd {
	e := &m.explorer
	p, text := e.prompt, strings.TrimSpace(e.input)
	e.prompt, e.input = filesNoPrompt, ""
	e.gen++
	if p != filesPromptGoTo && p != filesPromptFilter {
		return m.filesSubmitChange(p, text)
	}
	if p != filesPromptGoTo || text == "" {
		return nil
	}
	t := m.filesTabCur()
	host, dir := filesParseGoTo(text, t.cur().host)
	if host != t.cur().host {
		// Another machine is another side: nothing of the old one holds.
		t.sides[t.focus] = m.newFilesSide(host)
	}
	return m.filesRead(dir, "")
}

// filesParseGoTo reads "host:path", "host:" or a path. A path alone stays
// on the side's machine. A Windows drive letter is a path, not a host.
func filesParseGoTo(text, host string) (string, string) {
	h, p, ok := strings.Cut(text, ":")
	if !ok || len(h) == 1 || strings.ContainsAny(h, "/\\~") {
		return host, text
	}
	if h == "local" || h == localHostName() {
		h = ""
	}
	if p == "" {
		p = "~"
	}
	return h, p
}

// FilesEscape clears the line or the filter first, then quick look, and
// closes the view when there is nothing left to clear.
func (m *OS) FilesEscape() {
	e := &m.explorer
	switch {
	case e.prompt != filesNoPrompt:
		m.FilesPromptCancel()
	case m.filesClearMarks(m.filesCur()):
	case m.filesCur().filter != "":
		en, _, ok := m.filesCursorEntry()
		m.filesCur().filter = ""
		m.filesRebuild()
		if ok {
			m.filesPick(en.Name)
		}
	case e.look:
		m.FilesToggleLook()
	default:
		m.CloseFilesView()
	}
}

// filesSideMachine is the machine name the view shows for the side, and
// whether it is this client's own machine.
func (m *OS) filesSideMachine() (string, bool) { return m.filesMachineOf(m.filesCur()) }

// filesMachineOf is filesSideMachine for side s.
func (m *OS) filesMachineOf(s *filesSide) (string, bool) {
	host := s.host
	if host == "" {
		host = m.AttachedHost
	}
	if host == "" {
		return thisMachineShort(), true
	}
	return host, false
}

// filesCopyPath is the path Y copies: host:path for another machine.
func (m *OS) filesCopyPath(full string) string {
	if name, here := m.filesSideMachine(); !here {
		return name + ":" + full
	}
	return full
}

// FilesCopyPath copies the path of the row under the cursor.
func (m *OS) FilesCopyPath() tea.Cmd {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return nil
	}
	return m.copyFilePath(m.filesCopyPath(m.filesJoin(en.Name)))
}

// FilesInsertPath types the path of the row under the cursor into the pane
// the view was opened from, and goes back to that pane.
func (m *OS) FilesInsertPath() {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return
	}
	w := m.windowByID(m.explorer.origin)
	if w == nil {
		m.ShowNotification("The pane the view came from is gone.", "warning", m.Settings.NotificationDuration)
		return
	}
	src := dropSource{Path: m.filesJoin(en.Name), Name: en.Name, Dir: en.Dir}
	if name, here := m.filesSideMachine(); !here && m.filesCur().host != "" && m.filesCur().host != w.Host {
		src.Host = name
	}
	verdict := m.judgeDrop(w, src)
	if verdict.Refusal == "" {
		m.CloseFilesView()
	}
	m.deliverDrop(verdict, src, false)
}

// FilesEdit opens the file under the cursor in an editor pane on its
// machine, as the rail's edit does. A folder is gone into instead.
func (m *OS) FilesEdit() tea.Cmd {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return nil
	}
	if en.Dir {
		return m.filesRead(m.filesJoin(en.Name), "")
	}
	full := m.filesJoin(en.Name)
	editor := m.Settings.SidebarEditor
	host := m.filesCur().host
	if host != "" || m.AttachedHost != "" {
		other := host != "" && host != m.AttachedHost
		msg := remoteEditTarget(full, editor, false, other)
		if msg.Note == "" {
			m.CloseFilesView()
		}
		return m.handleFileEdit(msg)
	}
	return func() tea.Msg { return filesEditMsg{inspectEditTarget(full, editor)} }
}

// filesEditMsg is a local edit target checked off the loop.
type filesEditMsg struct{ fileEditMsg }

// HandleFilesEdit opens the editor pane the view asked for.
func (m *OS) HandleFilesEdit(msg filesEditMsg) tea.Cmd {
	if msg.Note == "" {
		m.CloseFilesView()
	}
	return m.handleFileEdit(msg.fileEditMsg)
}

// FilesOpenSystem opens the row under the cursor with a program on the
// desktop. A file on another machine is offered as a copy first, as in the
// rail.
func (m *OS) FilesOpenSystem() tea.Cmd {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return nil
	}
	full := m.filesJoin(en.Name)
	if m.IsRemoteClient() {
		m.ShowNotification("tuios did not open the file. This client is remote.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	if name, here := m.filesSideMachine(); !here {
		ask := farOpenAsk{host: name, path: full, name: en.Name, dir: en.Dir}
		m.offerFarOpen(ask, farOpenOfferText(ask))
		return nil
	}
	var rules config.FilesConfig
	if m.UserConfig != nil {
		rules = m.UserConfig.Files
	}
	return func() tea.Msg { return startFileOpener(full, rules) }
}

// FilesToggleLook shows quick look beside the list, or takes it away.
func (m *OS) FilesToggleLook() tea.Cmd {
	e := &m.explorer
	e.gen++
	if e.look {
		e.look = false
		if m.quickLook.open && m.quickLook.files {
			m.dropQuickLook()
		}
		return nil
	}
	e.look = true
	if m.quickLook.open {
		m.dropQuickLook()
	}
	m.quickLook.open, m.quickLook.files = true, true
	t, ok := m.filesLookTarget()
	if !ok {
		return nil
	}
	return m.quickLookShow(t)
}

// filesLookTarget is what quick look shows for the row under the cursor.
func (m *OS) filesLookTarget() (quickLookTarget, bool) {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return quickLookTarget{}, false
	}
	t := quickLookTarget{path: m.filesJoin(en.Name), name: en.Name, dir: en.Dir}
	host := m.filesCur().host
	switch {
	case host != "" && m.AttachedHost != "" && host != m.AttachedHost:
		t.refuse = "tuios cannot show a file on a host that you reach through another host."
	case host != "":
		t.host = host
	case m.AttachedHost != "":
		t.host = m.AttachedHost
	}
	return t, true
}

// readListingDetails reads dir on host with the details of each name, for
// the Files view. call is nil for a client with no daemon, which reads its
// own disk. It runs off the update goroutine.
func readListingDetails(call func(host, verb string, params map[string]any) (json.RawMessage, error), host, dir string) (fileListing, []session.FileInfo, string) {
	if call == nil {
		return readLocalDetails(dir)
	}
	raw, err := call(host, "file-list", map[string]any{"dir": dir, "limit": fileListPage, "hidden": true})
	if err != nil {
		return fileListing{Dir: dir, Err: filesReadError(host, err)}, nil, ""
	}
	var reply struct {
		Dir     string             `json:"dir"`
		Entries []session.FileInfo `json:"entries"`
		Total   int                `json:"total"`
		Capped  bool               `json:"capped"`
		Home    string             `json:"home"`
	}
	if err := jsonx.Unmarshal(raw, &reply); err != nil {
		return fileListing{Dir: dir, Err: "tuios could not read the answer."}, nil, ""
	}
	l := fileListing{Dir: cmp.Or(reply.Dir, dir), Capped: reply.Capped || reply.Total > len(reply.Entries)}
	l.Entries = make([]fileEntry, len(reply.Entries))
	for i, in := range reply.Entries {
		isDir := in.Kind == "dir"
		l.Entries[i] = fileEntry{Name: in.Name, Dir: isDir, Icon: fileIconFor(in.Name, isDir)}
	}
	return l, reply.Entries, reply.Home
}

// fileListPage is the most rows one read of a folder asks for. A folder
// with more says so, and the filter narrows it.
const fileListPage = 5000

// readLocalDetails is readListingDetails on this client's own disk.
func readLocalDetails(dir string) (fileListing, []session.FileInfo, string) {
	home, _ := os.UserHomeDir()
	full := pathroot.ExpandHome(dir)
	l := readListing(nil, "", "", full, true, 0, false)
	if l.Err != "" {
		return l, nil, home
	}
	names := make([]string, len(l.Entries))
	for i, en := range l.Entries {
		names[i] = en.Name
	}
	got := session.StatNames(full, names, nil)
	by := make(map[string]session.FileInfo, len(got))
	for _, in := range got {
		by[in.Name] = in
	}
	infos := make([]session.FileInfo, len(names))
	for i, n := range names {
		infos[i] = by[n]
	}
	return l, infos, home
}

// filesReadError is the words for a read of host that failed.
func filesReadError(host string, err error) string {
	if host == "" {
		return verbErrText(err)
	}
	return host + " could not list it: " + verbErrText(err)
}
