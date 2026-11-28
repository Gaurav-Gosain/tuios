package app

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// # Drag and drop in the Files view
//
// A press on a row arms a drag. Past the drag threshold the rows are held:
// the row pressed, or every marked row when the row pressed is marked. The
// label at the pointer says the verb, the count, the size and the folder a
// release puts them in, or why it would not.
//
// A drop on a folder row goes into that folder, on a file row or empty space
// into the side's folder, and on a crumb into that folder. On one machine a
// drop moves, between machines it copies, and alt flips the two. A folder
// row held filesSpringWait opens, and so does a tab or a side's name on the
// top bar, so a deep target needs no keys.
//
// While a drag is held the footer is the drop shelf: the pane the view came
// from, the other panes of the workspace and the agents of the session. A
// drop on a pane types the paths there, quoted for its program, by the rules
// of file_drop.go. A drop on an agent opens its reply with the files in it.
//
// When the pointer rests filesCheckRest on a folder, one file-check asks
// whether the names are there already, and the label says what the copy will
// do with them.
//
// Every drop has a key: c, m, p, P, I and the reply.

// filesSpringWait is how long a folder or a tab is held before it opens, and
// filesCheckRest how long the pointer rests on a folder before the check.
const (
	filesSpringWait = 700 * time.Millisecond
	filesCheckRest  = 150 * time.Millisecond
)

// filesDropKind is what a drop lands on.
type filesDropKind uint8

const (
	filesDropNone filesDropKind = iota
	// filesDropFolder is a folder of a side: a folder row, a crumb, or the
	// side's own folder.
	filesDropFolder
	// filesDropTab is a tab on the top bar, and filesDropSide the name of
	// a side drawn alone. Held, either goes on screen.
	filesDropTab
	filesDropSide
	// filesDropPane and filesDropAgent are the shelf's panes and agents.
	filesDropPane
	filesDropAgent
)

// filesDropTarget is where a release would put the held rows.
type filesDropTarget struct {
	kind filesDropKind
	// side is the side of a folder, dir the folder, row the screen row of
	// the folder row under the pointer (-1 for none) and crumb the crumb
	// (-1 for none). open is the folder row's name, which a hold opens.
	side  uint64
	dir   string
	row   int
	crumb int
	open  string
	// tab is the tab or the side on the top bar.
	tab int
	// window is the pane or the agent on the shelf.
	window string
	// refusal says why a release does nothing, and here that the target is
	// the folder the rows are in, which is said without a warning.
	refusal string
	here    bool
}

// same reports whether a and b are one target, so the check and the hold
// start once per target and not once per cell.
func (a filesDropTarget) same(b filesDropTarget) bool {
	return a.kind == b.kind && a.side == b.side && a.dir == b.dir && a.tab == b.tab && a.window == b.window && a.crumb == b.crumb && a.row == b.row
}

// filesDrag is a press on a row and the drag it may become.
type filesDrag struct {
	pressed, dragging bool
	px, py            int
	// from is the side the rows are on: its machine, its folder, whether
	// that is the disk of this client, and the rows by name.
	from  uint64
	host  string
	dir   string
	local bool
	names []string
	dirs  map[string]bool
	size  int64
	// alt is whether alt was held at the last motion.
	alt bool
	at  filesDropTarget
	// gen moves with each new target, so a hold or a check for an old one
	// does nothing.
	gen uint64
	// check is the answer of the check on at, once it came.
	check *filesDropCheck
}

// filesDropCheck counts the held files that are in the target folder
// already: with the same bytes, and with other bytes.
type filesDropCheck struct{ same, differ int }

// filesShelfHit is one pane or agent on the drop shelf and its columns.
type filesShelfHit struct {
	window string
	agent  bool
	x0, x1 int
}

// filesDragTickMsg is the end of a hold (spring) or of a rest (check) on
// the target of gen.
type filesDragTickMsg struct {
	gen    uint64
	spring bool
}

// filesDropCheckMsg is the answer of the check on the target of gen.
type filesDropCheckMsg struct {
	gen   uint64
	check filesDropCheck
	ok    bool
}

// FilesDragActive reports whether a press on a row of the Files view owns
// the pointer.
func (m *OS) FilesDragActive() bool { return m.explorer.open && m.explorer.drag.pressed }

// filesArmDrag arms a drag of row at on side s, pressed at x, y.
func (m *OS) filesArmDrag(s *filesSide, x, y, at int) {
	if at < 0 || at >= len(s.rows) || s.Dir == "" {
		return
	}
	i := s.rows[at]
	pressed := s.Entries[i].Name
	d := filesDrag{pressed: true, px: x, py: y, from: s.id, host: m.filesHostOf(s), dir: filesSideDir(s), local: m.filesLocal(s), dirs: map[string]bool{}}
	add := func(i int) {
		en := s.Entries[i]
		d.names = append(d.names, en.Name)
		if en.Dir {
			d.dirs[en.Name] = true
		} else if i < len(s.infos) {
			d.size += s.infos[i].Size
		}
	}
	if s.marked(pressed) {
		for _, j := range s.rows {
			if s.marks[s.Entries[j].Name] {
				add(j)
			}
		}
	} else {
		add(i)
	}
	m.explorer.drag = d
}

// FilesDragMotion follows the pointer with the rows held. It answers the
// hold and the check for a new target.
func (m *OS) FilesDragMotion(x, y int, alt bool) tea.Cmd {
	e := &m.explorer
	d := &e.drag
	if !d.dragging {
		if abs(x-d.px)+abs(y-d.py) < m.DragThreshold() {
			return nil
		}
		d.dragging = true
		e.gen++
	}
	if d.alt != alt {
		d.alt = alt
		d.at.refusal, d.at.here = m.filesDropRefusal(d.at)
		e.gen++
	}
	at := m.filesDropAt(x, y)
	if at.same(d.at) {
		m.filesDropPointer()
		return nil
	}
	d.at, d.check = at, nil
	d.gen++
	e.gen++
	m.filesDropPointer()
	gen := d.gen
	var cmds []tea.Cmd
	if at.kind == filesDropTab || at.kind == filesDropSide || at.open != "" && at.refusal == "" {
		cmds = append(cmds, tea.Tick(filesSpringWait, func(time.Time) tea.Msg { return filesDragTickMsg{gen: gen, spring: true} }))
	}
	if at.kind == filesDropFolder && at.refusal == "" {
		cmds = append(cmds, tea.Tick(filesCheckRest, func(time.Time) tea.Msg { return filesDragTickMsg{gen: gen} }))
	}
	return tea.Batch(cmds...)
}

// filesDropPointer sets the pointer for the target: not allowed on a
// refusal, copy on a target that takes the rows.
func (m *OS) filesDropPointer() {
	at := m.explorer.drag.at
	switch {
	case at.kind == filesDropNone || at.here:
		m.SetPointerShape(PointerGrabbing)
	case at.refusal != "":
		m.SetPointerShape(PointerNotAllowed)
	default:
		m.SetPointerShape(PointerCopy)
	}
}

// filesDropAt is the target under the screen cell x, y.
func (m *OS) filesDropAt(x, y int) filesDropTarget {
	e := &m.explorer
	at := filesDropTarget{row: -1, crumb: -1}
	switch {
	case y == e.hintsY:
		for _, h := range e.shelf {
			if x >= h.x0 && x < h.x1 {
				at.kind, at.window = filesDropPane, h.window
				if h.agent {
					at.kind = filesDropAgent
				}
				at.refusal, at.here = m.filesDropRefusal(at)
				return at
			}
		}
		return at
	case y == e.tabsY:
		for i, h := range e.segHits {
			if h.x1 > h.x0 && x >= h.x0 && x < h.x1 && i != m.filesTabCur().focus {
				at.kind, at.tab = filesDropSide, i
				return at
			}
		}
		for i, h := range e.tabHits {
			if x >= h.x0 && x < h.x1 && i != e.tab {
				at.kind, at.tab = filesDropTab, i
				return at
			}
		}
		return at
	}
	i := m.filesSideAt(x, y)
	if i < 0 {
		return at
	}
	s := m.filesTabCur().sides[i]
	if s.Dir == "" {
		return at
	}
	at.kind, at.side, at.dir = filesDropFolder, s.id, s.Dir
	switch {
	case y == s.crumbsY:
		for j, c := range s.crumbs {
			if x >= c.x0 && x < c.x1 {
				at.dir, at.crumb = c.dir, j
			}
		}
	case y >= s.rowsTop && y < s.rowsTop+s.rowsN:
		if r := s.scroll + y - s.rowsTop; r < len(s.rows) {
			if en := s.Entries[s.rows[r]]; en.Dir {
				at.dir, at.row, at.open = m.filesJoinSide(s, en.Name), y, en.Name
			}
		}
	}
	at.refusal, at.here = m.filesDropRefusal(at)
	return at
}

// filesDropMove reports whether a drop on side o moves the rows: on one
// machine it does, between machines it copies, and alt flips it.
func (m *OS) filesDropMove(o *filesSide) bool {
	d := &m.explorer.drag
	return (m.filesHostOf(o) == d.host) != d.alt
}

// filesDropRefusal says why a release on at would do nothing. here is set
// when at is the folder the rows are in.
func (m *OS) filesDropRefusal(at filesDropTarget) (refusal string, here bool) {
	d := &m.explorer.drag
	switch at.kind {
	case filesDropFolder:
		o := m.filesSideByID(at.side)
		if o == nil {
			return "", false
		}
		host := m.filesHostOf(o)
		name, _ := m.filesMachineOf(o)
		switch {
		case o.host != "" && !m.hostIsUp(o.host):
			return printableTitle(name) + " is not linked.", false
		case host == d.host && at.dir == d.dir:
			return "That is the folder they are in.", true
		case host == d.host && !d.local && !m.filesDropMove(o):
			return "tuios cannot copy inside another machine yet. Let go of alt to move.", false
		}
		if host == d.host {
			for name := range d.dirs {
				src := filesJoinOn(d.dir, name)
				if at.dir == src || strings.HasPrefix(at.dir, src+"/") {
					return "A folder cannot go into itself.", false
				}
			}
		}
	case filesDropPane, filesDropAgent:
		w := m.windowByID(at.window)
		if w == nil {
			return "The pane closed.", false
		}
		if at.kind == filesDropAgent && (!d.local || !m.paneIsLocal(w)) {
			return "The files and the agent are on different machines. Copy the files there first (c).", false
		}
		if !d.local && !m.filesOnPaneHost(w) {
			return "The files are on " + printableTitle(d.host) + ". Copy them to the pane's machine first (c).", false
		}
		if d.local {
			if _, refusal := m.paneTakesDrop(w); refusal != "" && m.paneIsLocal(w) {
				return refusal, false
			}
		}
	}
	return "", false
}

// filesOnPaneHost reports whether the held rows are on the machine of pane
// w, so their paths name files there.
func (m *OS) filesOnPaneHost(w *terminal.Window) bool {
	d := &m.explorer.drag
	host := w.Host
	if host == "" {
		host = m.AttachedHost
	}
	return host == d.host && w.CwdHost == ""
}

// filesDragPaths is the paths of the held rows, a folder with a slash at
// its end.
func (m *OS) filesDragPaths() []string {
	d := &m.explorer.drag
	out := make([]string, 0, len(d.names))
	for _, name := range d.names {
		p := filesJoinOn(d.dir, name)
		if d.local {
			p = filepath.Join(d.dir, name)
		}
		if d.dirs[name] && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		out = append(out, p)
	}
	return out
}

// HandleFilesDragTick opens the folder or the tab held, or checks the
// folder the pointer rests on.
func (m *OS) HandleFilesDragTick(msg filesDragTickMsg) tea.Cmd {
	e := &m.explorer
	d := &e.drag
	if !e.open || !d.dragging || msg.gen != d.gen {
		return nil
	}
	at := d.at
	if !msg.spring {
		return m.filesDropCheckCmd(at, d.gen)
	}
	// The target is gone from under the pointer once it opens. The next
	// motion finds the new one.
	d.at = filesDropTarget{row: -1, crumb: -1}
	d.gen++
	e.gen++
	switch at.kind {
	case filesDropTab:
		m.FilesGoTab(at.tab)
	case filesDropSide:
		m.filesFocusSide(at.tab)
	case filesDropFolder:
		if s := m.filesSideByID(at.side); s != nil && at.open != "" {
			// The held rows keep their folder: the drag carries it.
			s.marks, s.filter = nil, ""
			return m.filesReadSide(s, at.dir, "")
		}
	}
	return nil
}

// filesDropCheckCmd asks whether the held names are in the folder of at,
// and with what bytes. A move inside a far machine is a rename, which
// leaves a name that is there alone, so it asks nothing.
func (m *OS) filesDropCheckCmd(at filesDropTarget, gen uint64) tea.Cmd {
	d := m.explorer.drag
	o := m.filesSideByID(at.side)
	if o == nil || m.filesCaller() == nil {
		return nil
	}
	dst := m.filesHostOf(o)
	if dst == d.host && !d.local {
		return nil
	}
	call := m.verbOnHostCaller()
	names := d.names[:min(len(d.names), 512)]
	return func() tea.Msg {
		ask := func(host, root string) ([]session.FileCheck, bool) {
			raw, err := call(host, "file-check", map[string]any{"root": root, "rels": names, "hash": true})
			if err != nil {
				return nil, false
			}
			var r struct {
				Entries []session.FileCheck `json:"entries"`
			}
			if jsonx.Unmarshal(raw, &r) != nil || len(r.Entries) != len(names) {
				return nil, false
			}
			return r.Entries, true
		}
		there, ok := ask(dst, at.dir)
		if !ok {
			return filesDropCheckMsg{gen: gen}
		}
		var src []session.FileCheck
		for _, c := range there {
			if c.Exists {
				src, ok = ask(d.host, d.dir)
				break
			}
		}
		if !ok {
			return filesDropCheckMsg{gen: gen}
		}
		var out filesDropCheck
		for i, c := range there {
			switch {
			case !c.Exists:
			case src != nil && c.SHA256 != "" && c.SHA256 == src[i].SHA256:
				out.same++
			default:
				out.differ++
			}
		}
		return filesDropCheckMsg{gen: gen, check: out, ok: true}
	}
}

// HandleFilesDropCheck keeps the answer of a check for the label.
func (m *OS) HandleFilesDropCheck(msg filesDropCheckMsg) {
	d := &m.explorer.drag
	if !d.dragging || msg.gen != d.gen || !msg.ok {
		return
	}
	c := msg.check
	d.check = &c
	m.explorer.gen++
}

// FilesDragRelease drops the held rows on the target under x, y. A press
// that never became a drag was a click, which the press already did.
func (m *OS) FilesDragRelease(x, y int, alt bool) tea.Cmd {
	e := &m.explorer
	e.gen++
	m.ResetPointerShape()
	defer func() { e.drag = filesDrag{} }()
	if !e.drag.dragging {
		return nil
	}
	e.drag.alt = alt
	d := e.drag
	at := m.filesDropAt(x, y)
	if at.refusal != "" {
		if !at.here {
			m.ShowNotification(at.refusal, "warning", m.Settings.NotificationDuration*2)
		}
		return nil
	}
	switch at.kind {
	case filesDropFolder:
		o := m.filesSideByID(at.side)
		if o == nil {
			return nil
		}
		if s := m.filesSideByID(d.from); s != nil {
			m.filesClearMarks(s)
		}
		return m.filesSend(d.host, d.dir, d.local, d.names, m.filesHostOf(o), at.dir, m.filesLocal(o), m.filesDropMove(o))
	case filesDropPane:
		return m.filesDropOnPane(at.window)
	case filesDropAgent:
		return m.filesDropOnAgent(at.window)
	}
	return nil
}

// CancelFilesDrag is esc while rows are held. Nothing is dropped.
func (m *OS) CancelFilesDrag() {
	m.explorer.drag = filesDrag{}
	m.explorer.gen++
	m.ResetPointerShape()
}

// filesDropOnPane types the held paths into a pane, quoted for its
// program, and goes back to the pane. Files on this machine go to a pane on
// another one through drop-files, which copies them there first.
func (m *OS) filesDropOnPane(id string) tea.Cmd {
	w := m.windowByID(id)
	if w == nil {
		return nil
	}
	d := m.explorer.drag
	paths := m.filesDragPaths()
	if d.local && !m.paneIsLocal(w) {
		host := w.Host
		if host == "" {
			host = m.AttachedHost
		}
		ask := dropAsk{window: w.ID, host: host, text: strings.Join(paths, " "), paths: paths}
		for _, p := range paths {
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				ask.size += fi.Size()
			}
		}
		m.CloseFilesView()
		m.openCopyDialog(&copyDialog{kind: copyDialogDrop, drop: &ask})
		return nil
	}
	v := dropVerdict{Window: w.ID, Pane: pipPaneName(w)}
	if d.local {
		v = m.judgeDropPaths(w, paths)
	} else {
		dialect := pasteDialectOf(w.ForegroundCommand())
		quoted := make([]string, 0, len(paths))
		for _, p := range paths {
			text, refusal := quoteDropPath(dialect, p)
			if refusal != "" {
				v.Refusal = refusal
				break
			}
			quoted = append(quoted, text)
		}
		if v.Refusal == "" {
			v.Text = strings.Join(quoted, " ") + " "
		}
	}
	if v.Refusal == "" {
		m.CloseFilesView()
	}
	m.deliverDrop(v, dropSource{Path: paths[0], Name: path.Base(strings.TrimSuffix(paths[0], "/"))}, false)
	return nil
}

// filesDropOnAgent opens the reply to an agent with the held paths in it.
// An image is attached as a picture, as a pasted path to one is.
func (m *OS) filesDropOnAgent(id string) tea.Cmd {
	w := m.windowByID(id)
	if w == nil {
		return nil
	}
	paths := m.filesDragPaths()
	m.CloseFilesView()
	m.SidebarAgentReply(m.sidebarCurrentSessionID(), w.ID)
	ed := m.Inbox.reply.editor
	if ed == nil || ed.window != w.ID {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(paths))
	for _, p := range paths {
		text, refusal := quoteDropPath(pasteDialectOf(""), p)
		if refusal != "" {
			text = p
		}
		cmds = append(cmds, m.composerPasteCmd(ed.comp, w.ID, text+" "))
	}
	return tea.Sequence(cmds...)
}

// filesShelfPanes is what the drop shelf offers: the pane the view came
// from, the other panes of the workspace, then the agents of the session in
// the other workspaces.
func (m *OS) filesShelfPanes() []*terminal.Window {
	var out []*terminal.Window
	seen := map[string]bool{}
	addIf := func(w *terminal.Window) {
		if w != nil && !seen[w.ID] {
			seen[w.ID] = true
			out = append(out, w)
		}
	}
	addIf(m.windowByID(m.explorer.origin))
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace {
			addIf(w)
		}
	}
	for _, w := range m.Windows {
		if w.AgentState != "" {
			addIf(w)
		}
	}
	return out
}

// drawFilesShelf draws the drop shelf on the footer's row while rows are
// held, and keeps each pane's columns.
func (m *OS) drawFilesShelf(c *ckCanvas, pal overlay.Palette, y, w, top int) {
	e := &m.explorer
	e.hintsY = top + y
	e.hints = e.hints[:0]
	e.shelf = e.shelf[:0]
	c.fill(0, y, w, 1, ckColor(pal.Panel))
	x := c.text(1, y, w-1, ckSeg{t: "Drop on a pane:", fg: pal.FgDim})
	for _, p := range m.filesShelfPanes() {
		name := pipPaneName(p)
		segs := []ckSeg{{t: " " + ckTrunc(name, 24) + " ", fg: pal.Fg}}
		if p.AgentState != "" {
			glyph := sidebarGlyph(p.AgentState, false, 0, pal.Panel, pal, &m.Settings)
			segs = []ckSeg{{t: " " + glyph + " ", fg: agentGlyphColor(p.AgentState, pal)}, {t: ckTrunc(name, 24) + " ", fg: pal.Fg}}
		}
		x0 := x + 1
		if x0+2 >= w {
			break
		}
		x = c.text(x0, y, w-1, segs...)
		hit := filesShelfHit{window: p.ID, agent: p.AgentState != "", x0: x0, x1: x}
		e.shelf = append(e.shelf, hit)
		at := e.drag.at
		if (at.kind == filesDropPane || at.kind == filesDropAgent) && at.window == p.ID {
			m.filesDropMarkCells(c, pal, x0, y, x-x0, at.refusal != "")
		}
	}
}

// drawFilesDropMark draws where a release would put the held rows: the
// folder row, the crumb, or a bar down both edges of the side for its own
// folder. A refused target is drawn in the warning colour.
func (m *OS) drawFilesDropMark(c *ckCanvas, pal overlay.Palette, top int) {
	d := &m.explorer.drag
	at := d.at
	if !d.dragging || at.kind != filesDropFolder {
		return
	}
	s := m.filesSideByID(at.side)
	if s == nil || s.box.w == 0 {
		return
	}
	refused := at.refusal != "" && !at.here
	// The bars run down the side for its own folder, and along the one row
	// of a folder row or a crumb.
	y0, y1 := s.box.y-top, s.box.y-top+s.box.h
	switch {
	case at.row >= 0:
		m.filesDropMarkCells(c, pal, s.box.x+1, at.row-top, s.box.w-2, refused)
		y0, y1 = at.row-top, at.row-top+1
	case at.crumb >= 0 && at.crumb < len(s.crumbs):
		cr := s.crumbs[at.crumb]
		m.filesDropMarkCells(c, pal, cr.x0, s.crumbsY-top, cr.x1-cr.x0, refused)
		y0, y1 = s.crumbsY-top, s.crumbsY-top+1
	case at.here:
		return
	}
	ink := pal.Accent
	if refused {
		ink = pal.Warning
	}
	bar := "┃"
	if overlay.UseASCII() {
		bar = "|"
	}
	for y := y0; y < y1; y++ {
		c.text(s.box.x, y, 0, ckSeg{t: bar, fg: ink})
		c.text(s.box.x+s.box.w-1, y, 0, ckSeg{t: bar, fg: ink})
	}
}

// filesDropMarkCells marks a target's cells: as the cursor row is marked,
// or in the warning colour for a refusal.
func (m *OS) filesDropMarkCells(c *ckCanvas, pal overlay.Palette, x, y, w int, refused bool) {
	ink := pal.AccentBright
	if refused {
		ink = pal.Warning
	} else {
		c.mark(pal, x, y, w, 1, overlay.RowState{Cursor: true, Focused: true})
	}
	c.restyle(x, y, w, 1, func(cell *uv.Cell) {
		cell.Style.Fg = ink
		cell.Style.Attrs |= uv.AttrBold
	})
}

// filesDragLabel is the label at the pointer.
func (m *OS) filesDragLabel() string {
	d := &m.explorer.drag
	what := countOf(len(d.names), "item")
	if len(d.names) == 1 {
		what = d.names[0]
		if d.dirs[what] {
			what += "/"
		}
	}
	if d.size > 0 {
		what += " (" + filesSizeText(d.size) + ")"
	}
	at := d.at
	if at.refusal != "" {
		return what + "  " + at.refusal
	}
	switch at.kind {
	case filesDropTab:
		return what + "  Hold here to open the tab."
	case filesDropSide:
		return what + "  Hold here to show the side."
	case filesDropPane:
		if w := m.windowByID(at.window); w != nil {
			return what + m.dropArrow() + pipPaneName(w)
		}
	case filesDropAgent:
		if w := m.windowByID(at.window); w != nil {
			return what + m.dropArrow() + "a reply to " + pipPaneName(w)
		}
	case filesDropFolder:
		return m.filesDropFolderLabel(what, at)
	}
	return what + "  Drop on a folder, a side or a pane. Esc cancels."
}

// filesDropFolderLabel is the label for a folder: the verb, the rows, the
// machine and the folder, and what the check found there.
func (m *OS) filesDropFolderLabel(what string, at filesDropTarget) string {
	d := &m.explorer.drag
	o := m.filesSideByID(at.side)
	if o == nil {
		return what
	}
	move := m.filesDropMove(o)
	verb, flip := "Copy ", "Hold alt to move."
	if move {
		verb, flip = "Move ", "Hold alt to copy."
	}
	if m.filesHostOf(o) == d.host && !d.local {
		flip = ""
	}
	name, _ := m.filesMachineOf(o)
	text := verb + what + " into " + printableTitle(name) + " " + o.short(at.dir) + "."
	if c := d.check; c != nil {
		local := m.filesHostOf(o) == d.host
		switch {
		case local && c.same+c.differ > 0:
			text += " " + filesThere(c.same+c.differ) + ": tuios keeps both and gives the new one another name."
		case !local:
			if c.same > 0 {
				text += " " + filesThere(c.same) + " with the same bytes: left as is."
			}
			if c.differ > 0 {
				text += " " + filesThere(c.differ) + " with other bytes: tuios asks."
			}
		}
	}
	if flip != "" {
		text += "  " + flip
	}
	return text
}

// filesThere is "1 is there" or "3 are there".
func filesThere(n int) string {
	if n == 1 {
		return "1 is there"
	}
	return itoaFiles(n) + " are there"
}

// renderFilesDragLabel is the label's layer, or nil when no rows are held.
func (m *OS) renderFilesDragLabel() *lipgloss.Layer {
	d := &m.explorer.drag
	if !m.explorer.open || !d.dragging {
		return nil
	}
	label := m.renderDropLabel(m.filesDragLabel(), d.at.refusal != "" && !d.at.here, "files-drag-label")
	// Above the view and quick look beside it.
	return label.Z(m.overlayZ(overlayKindFiles) + 2)
}
