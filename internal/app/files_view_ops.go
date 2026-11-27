package app

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// # Changes from the Files view
//
// x marks rows, and the keys below act on the marks, or on the row under
// the cursor when nothing is marked.
//
//   - c copies to the other side's folder, and m moves there. Between two
//     machines that is a copy of the transfer engine: checked by sha256,
//     resumed after a drop, and a move takes the source away only after the
//     copy is checked. On one machine it is a copy on that machine.
//   - y takes the rows, p pastes a copy of them into the side with the keys,
//     and P moves them there.
//   - r renames the row in place, a makes a file, or a folder for a name
//     that ends in /, and d moves the rows to the trash.
//   - u undoes the last rename, move on one machine, trash, or new folder,
//     once it has checked that the thing is still as the change left it.
//
// Rename, new, trash and undo act on this machine's disk. A side on another
// machine takes a rename and a new folder through its daemon, and says so
// for the rest: its trash comes with the far machine's own verb.

// filesClip is what y took: the machine, the folder and the names.
type filesClip struct {
	host, dir string
	local     bool
	names     []string
}

// filesUndo is one change u can take back.
type filesUndo struct {
	// kind is rename, move, trash or mkdir.
	kind string
	// pairs is each path the change moved: from, then to. A trash pair is
	// the path, then its place in the trash, with its info file in info.
	pairs [][2]string
	info  []string
	// at is each moved path's size and time when the change ended, so undo
	// can tell that it is still as the change left it.
	at []fileStamp
}

// fileStamp is a path's size and change time.
type fileStamp struct {
	size  int64
	mtime time.Time
}

// stampOf reads p's size and time, or the zero stamp.
func stampOf(p string) fileStamp {
	fi, err := os.Lstat(p)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: fi.Size(), mtime: fi.ModTime()}
}

// filesOpMsg is a change that ended, off the loop.
type filesOpMsg struct {
	note, warn string
	undo       *filesUndo
	// undone says the change was an undo, which is not pushed again.
	undone bool
	// dirs is the folders the change touched, on host. Sides on them read
	// again.
	host string
	dirs []string
}

// filesHostOf is the machine of side s as the daemon names it: "" for the
// machine of this client's daemon.
func (m *OS) filesHostOf(s *filesSide) string {
	if s.host != "" {
		return s.host
	}
	return m.AttachedHost
}

// filesLocal reports whether side s is on the disk this client process
// runs on: the machine of a session that is not attached from another one.
func (m *OS) filesLocal(s *filesSide) bool {
	return s.host == "" && m.AttachedHost == ""
}

// marked reports whether the row named name is marked.
func (s *filesSide) marked(name string) bool { return s.marks[name] }

// filesTargets is what a change acts on: the marked rows in the order they
// are drawn, or the row under the cursor.
func filesTargets(s *filesSide) []fileEntry {
	var out []fileEntry
	if len(s.marks) > 0 {
		for _, i := range s.rows {
			if s.marks[s.Entries[i].Name] {
				out = append(out, s.Entries[i])
			}
		}
		return out
	}
	if en, _, ok := filesSideEntry(s); ok {
		out = append(out, en)
	}
	return out
}

// filesMarkedSize is the count and the bytes of the marked files.
func filesMarkedSize(s *filesSide) (int, int64) {
	n, size := 0, int64(0)
	for _, i := range s.rows {
		if s.marks[s.Entries[i].Name] {
			n++
			if i < len(s.infos) {
				size += s.infos[i].Size
			}
		}
	}
	return n, size
}

// FilesMark marks the row under the cursor, or takes the mark off, and
// moves the cursor down.
func (m *OS) FilesMark() {
	s := m.filesCur()
	en, _, ok := filesSideEntry(s)
	if !ok {
		return
	}
	if s.marks == nil {
		s.marks = map[string]bool{}
	}
	if s.marks[en.Name] {
		delete(s.marks, en.Name)
	} else {
		s.marks[en.Name] = true
	}
	m.FilesMove(1)
	m.explorer.gen++
}

// FilesMarkAll marks every row on screen, or none when every one is marked.
func (m *OS) FilesMarkAll() {
	s := m.filesCur()
	if n, _ := filesMarkedSize(s); n == len(s.rows) && n > 0 {
		s.marks = nil
		m.explorer.gen++
		return
	}
	s.marks = map[string]bool{}
	for _, i := range s.rows {
		s.marks[s.Entries[i].Name] = true
	}
	m.explorer.gen++
}

// filesClearMarks takes every mark off side s, and reports whether there
// was one.
func (m *OS) filesClearMarks(s *filesSide) bool {
	if len(s.marks) == 0 {
		return false
	}
	s.marks = nil
	m.explorer.gen++
	return true
}

// filesOther is the other side of the tab on screen, when both are on.
func (m *OS) filesOther() *filesSide {
	t := m.filesTabCur()
	if !t.two {
		return nil
	}
	return t.sides[1-t.focus]
}

// FilesCopy copies the targets to the other side's folder, or moves them
// there. With one side it asks where, as : does.
func (m *OS) FilesCopy(move bool) tea.Cmd {
	s := m.filesCur()
	names := filesNames(filesTargets(s))
	if len(names) == 0 {
		return nil
	}
	o := m.filesOther()
	if o == nil || filesSideDir(o) == "" {
		e := &m.explorer
		e.prompt, e.input = filesPromptCopy, ""
		if move {
			e.prompt = filesPromptMove
		}
		e.gen++
		return nil
	}
	m.filesClearMarks(s)
	return m.filesSend(m.filesHostOf(s), filesSideDir(s), m.filesLocal(s), names, m.filesHostOf(o), filesSideDir(o), m.filesLocal(o), move)
}

// filesNames is the names of entries.
func filesNames(entries []fileEntry) []string {
	out := make([]string, len(entries))
	for i, en := range entries {
		out[i] = en.Name
	}
	return out
}

// filesSend copies or moves names from the folder srcDir on srcHost into
// dstDir on dstHost.
func (m *OS) filesSend(srcHost, srcDir string, srcLocal bool, names []string, dstHost, dstDir string, dstLocal bool, move bool) tea.Cmd {
	if srcHost == dstHost && srcDir == dstDir {
		m.ShowNotification("That is the folder they are in.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if srcHost == dstHost {
		if srcLocal && dstLocal {
			return m.filesLocalPaste(srcDir, names, dstDir, move)
		}
		if move {
			return m.filesFarMove(srcHost, srcDir, names, dstDir)
		}
		m.ShowNotification("tuios cannot copy inside another machine yet. m moves there.", "info", m.Settings.NotificationDuration)
		return nil
	}
	job := m.filesNewJob(srcHost, srcDir, names, dstHost, dstDir, move)
	jobID := job.id
	cmds := make([]tea.Cmd, 0, len(names))
	call := m.transferCaller()
	for _, name := range names {
		params := map[string]any{
			"src": map[string]any{"host": srcHost, "path": filesJoinOn(srcDir, name)},
			// The whole path of each copy: copies into one folder are one
			// destination each, and the engine runs them side by side.
			"dst":      map[string]any{"host": dstHost, "path": filesJoinOn(dstDir, name)},
			"conflict": "ask",
			"each":     "ask",
			"move":     move,
		}
		name := name
		cmds = append(cmds, func() tea.Msg {
			raw, err := call("transfer-start", params, transferCallTimeout)
			msg := transferCallMsg{Verb: "transfer-start", Name: name, Err: err}
			if err == nil {
				var row session.TransferRow
				if jsonx.Unmarshal(raw, &row) == nil && row.ID != "" {
					msg.Row = &row
				}
			}
			return filesJobCallMsg{job: jobID, call: msg}
		})
	}
	// One after another: the calls share the client's connection to its
	// daemon.
	return tea.Sequence(cmds...)
}

// filesJoinOn joins name to dir in the form of dir's machine.
func filesJoinOn(dir, name string) string {
	if strings.Contains(dir, "\\") && !strings.Contains(dir, "/") {
		return dir + "\\" + name
	}
	return path.Join(dir, name)
}

// filesLocalPaste copies or moves names on this machine, off the loop. A
// move can be undone.
func (m *OS) filesLocalPaste(srcDir string, names []string, dstDir string, move bool) tea.Cmd {
	return func() tea.Msg {
		var renamed int
		undo := &filesUndo{kind: "move"}
		var firstErr error
		done := 0
		for _, name := range names {
			src := filepath.Join(srcDir, name)
			dst, err := pasteOne(dstDir, src, move, &renamed)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			done++
			undo.pairs = append(undo.pairs, [2]string{src, dst})
			undo.at = append(undo.at, stampOf(dst))
		}
		msg := filesOpMsg{dirs: []string{srcDir, dstDir}}
		verb := "Copied"
		if move {
			verb = "Moved"
			msg.undo = undo
		}
		if done > 0 {
			msg.note = verb + " " + countOf(done, "item") + "."
		}
		if firstErr != nil {
			msg.warn = "tuios could not paste everything: " + fileOpError(firstErr)
		}
		return msg
	}
}

// filesFarCall runs one change on another machine's daemon, and reads dir
// again after it. note is the notice when it worked, and fail starts the
// warning when it did not.
func (m *OS) filesFarCall(host, dir, verb string, params map[string]any, note, fail string) tea.Cmd {
	call := m.verbOnHostCaller()
	return func() tea.Msg {
		if _, err := call(host, verb, params); err != nil {
			return filesOpMsg{warn: fail + verbErrText(err)}
		}
		return filesOpMsg{note: note, host: host, dirs: []string{dir}}
	}
}

// filesFarMove moves names inside another machine, through its daemon.
func (m *OS) filesFarMove(host, srcDir string, names []string, dstDir string) tea.Cmd {
	call := m.verbOnHostCaller()
	return func() tea.Msg {
		done := 0
		var firstErr error
		for _, name := range names {
			_, err := call(host, "file-rename", map[string]any{"from": filesJoinOn(srcDir, name), "to": filesJoinOn(dstDir, name)})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			done++
		}
		msg := filesOpMsg{host: host, dirs: []string{srcDir, dstDir}}
		if done > 0 {
			msg.note = "Moved " + countOf(done, "item") + " on " + host + "."
		}
		if firstErr != nil {
			msg.warn = "tuios could not move everything: " + verbErrText(firstErr)
		}
		return msg
	}
}

// FilesYank takes the targets for a paste.
func (m *OS) FilesYank() {
	s := m.filesCur()
	names := filesNames(filesTargets(s))
	if len(names) == 0 {
		return
	}
	m.explorer.clip = &filesClip{host: m.filesHostOf(s), dir: filesSideDir(s), local: m.filesLocal(s), names: names}
	m.filesClearMarks(s)
	m.ShowNotification("Took "+countOf(len(names), "item")+". p pastes a copy here, P moves them here.", "info", m.Settings.NotificationDuration)
}

// FilesPaste pastes what y took into the side with the keys: a copy, or
// with move the items themselves.
func (m *OS) FilesPaste(move bool) tea.Cmd {
	c := m.explorer.clip
	if c == nil {
		m.ShowNotification("Nothing is taken. y takes the marked rows.", "info", m.Settings.NotificationDuration)
		return nil
	}
	s := m.filesCur()
	if move {
		m.explorer.clip = nil
	}
	return m.filesSend(c.host, c.dir, c.local, c.names, m.filesHostOf(s), filesSideDir(s), m.filesLocal(s), move)
}

// FilesRename starts a rename of the row under the cursor, in the row.
func (m *OS) FilesRename() {
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return
	}
	e := &m.explorer
	e.prompt, e.input, e.renaming = filesPromptRename, en.Name, en.Name
	e.gen++
}

// FilesNew asks for the name of a new file or folder.
func (m *OS) FilesNew() {
	e := &m.explorer
	e.prompt, e.input = filesPromptNew, ""
	e.gen++
}

// filesSubmitChange runs the rename, the new item, or the copy to a typed
// place, when its line ends.
func (m *OS) filesSubmitChange(p filesPrompt, text string) tea.Cmd {
	s := m.filesCur()
	dir := filesSideDir(s)
	switch p {
	case filesPromptRename:
		old := m.explorer.renaming
		m.explorer.renaming = ""
		if text == "" || text == old {
			return nil
		}
		if m.filesLocal(s) {
			return func() tea.Msg {
				said, err := renameEntry(dir, old, text)
				if err != nil {
					return filesOpMsg{warn: "tuios could not rename " + old + ": " + fileOpError(err)}
				}
				from, to := filepath.Join(dir, old), filepath.Join(dir, filepath.Clean(text))
				return filesOpMsg{note: said + ".", dirs: []string{dir},
					undo: &filesUndo{kind: "rename", pairs: [][2]string{{from, to}}, at: []fileStamp{stampOf(to)}}}
			}
		}
		return m.filesFarCall(m.filesHostOf(s), dir, "file-rename", map[string]any{"from": filesJoinOn(dir, old), "to": filesJoinOn(dir, text)},
			"Renamed "+old+".", "tuios could not rename "+old+": ")
	case filesPromptNew:
		if text == "" {
			return nil
		}
		folder := strings.HasSuffix(text, "/")
		if m.filesLocal(s) {
			return func() tea.Msg {
				said, err := createPath(dir, text)
				if err != nil {
					return filesOpMsg{warn: "tuios could not make " + text + ": " + fileOpError(err)}
				}
				msg := filesOpMsg{note: said + ".", dirs: []string{dir}}
				if folder {
					made := filepath.Join(dir, filepath.Clean(strings.TrimSuffix(text, "/")))
					msg.undo = &filesUndo{kind: "mkdir", pairs: [][2]string{{made, made}}}
				}
				return msg
			}
		}
		if !folder {
			m.ShowNotification("tuios can make a folder on another machine, not a file. End the name with /.", "info", m.Settings.NotificationDuration)
			return nil
		}
		return m.filesFarCall(m.filesHostOf(s), dir, "file-mkdir", map[string]any{"path": filesJoinOn(dir, strings.TrimSuffix(text, "/"))},
			"Made "+text+".", "tuios could not make "+text+": ")
	case filesPromptCopy, filesPromptMove:
		if text == "" {
			return nil
		}
		host, to := filesParseGoTo(text, s.host)
		if host == "" {
			host = m.AttachedHost
		}
		names := filesNames(filesTargets(s))
		m.filesClearMarks(s)
		local := host == "" && m.AttachedHost == ""
		if local {
			to = expandHomeLocal(to)
		}
		return m.filesSend(m.filesHostOf(s), dir, m.filesLocal(s), names, host, to, local, p == filesPromptMove)
	}
	return nil
}

// expandHomeLocal writes a leading ~ of a path on this machine out.
func expandHomeLocal(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// FilesTrash moves the targets to this machine's trash.
func (m *OS) FilesTrash() tea.Cmd {
	s := m.filesCur()
	targets := filesTargets(s)
	if len(targets) == 0 {
		return nil
	}
	if !m.filesLocal(s) {
		name, _ := m.filesMachineOf(s)
		m.ShowNotification("tuios cannot move a file on "+name+" to the trash yet.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if !trashAvailable() {
		m.ShowNotification("This machine has no trash tuios can use.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	dir := filesSideDir(s)
	paths := make([]string, len(targets))
	for i, en := range targets {
		paths[i] = filepath.Join(dir, en.Name)
	}
	m.filesClearMarks(s)
	return func() tea.Msg {
		got, err := trashPathsUndoable(paths, time.Now())
		msg := filesOpMsg{dirs: []string{dir}}
		if len(got) > 0 {
			u := &filesUndo{kind: "trash"}
			for _, g := range got {
				u.pairs = append(u.pairs, [2]string{g.from, g.file})
				u.info = append(u.info, g.info)
				u.at = append(u.at, stampOf(g.file))
			}
			msg.undo = u
			msg.note = "Moved " + countOf(len(got), "item") + " to the trash. u brings them back."
		}
		if err != nil {
			msg.warn = "tuios could not trash everything: " + trashError(err)
		}
		return msg
	}
}

// filesUndoMax is how many changes u can take back.
const filesUndoMax = 50

// FilesUndo takes back the last change, newest first.
func (m *OS) FilesUndo() tea.Cmd {
	e := &m.explorer
	if len(e.undo) == 0 {
		m.ShowNotification("Nothing to undo.", "info", m.Settings.NotificationDuration)
		return nil
	}
	u := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	return func() tea.Msg { return undoFilesChange(u) }
}

// undoFilesChange takes back one change, when each path is still as the
// change left it.
func undoFilesChange(u filesUndo) filesOpMsg {
	msg := filesOpMsg{undone: true}
	for i, p := range u.pairs {
		msg.dirs = append(msg.dirs, filepath.Dir(p[0]), filepath.Dir(p[1]))
		if u.kind != "mkdir" && i < len(u.at) && stampOf(p[1]) != u.at[i] {
			msg.warn = filepath.Base(p[1]) + " changed since, so tuios left it as it is."
			continue
		}
		if _, err := os.Lstat(p[0]); u.kind != "mkdir" && err == nil {
			msg.warn = "A new " + filepath.Base(p[0]) + " is there now, so tuios left the old one where it is."
			continue
		}
		var err error
		switch u.kind {
		case "rename", "move":
			err = os.Rename(p[1], p[0])
			if errors.Is(err, syscall.EXDEV) {
				if err = copyTree(p[1], p[0]); err == nil {
					err = os.RemoveAll(p[1])
				}
			}
		case "trash":
			if err = os.Rename(p[1], p[0]); err == nil && i < len(u.info) {
				_ = os.Remove(u.info[i])
			}
		case "mkdir":
			err = os.Remove(p[0])
			if err != nil {
				msg.warn = filepath.Base(p[0]) + " is not empty now, so tuios left it."
				continue
			}
		}
		if err != nil {
			msg.warn = "tuios could not undo it: " + fileOpError(err)
		}
	}
	if msg.warn == "" {
		msg.note = map[string]string{"rename": "The rename is undone.", "move": "The move is undone.",
			"trash": "Brought it back from the trash.", "mkdir": "The new folder is gone."}[u.kind]
	}
	return msg
}

// HandleFilesOp shows how a change went, keeps it for u, and reads the
// folders it touched again.
func (m *OS) HandleFilesOp(msg filesOpMsg) tea.Cmd {
	e := &m.explorer
	if msg.undo != nil && !msg.undone && len(msg.undo.pairs) > 0 {
		e.undo = append(e.undo, *msg.undo)
		if len(e.undo) > filesUndoMax {
			e.undo = e.undo[len(e.undo)-filesUndoMax:]
		}
	}
	switch {
	case msg.warn != "":
		m.ShowNotification(msg.warn, "warning", m.Settings.NotificationDuration)
	case msg.note != "":
		m.ShowNotification(msg.note, "success", m.Settings.NotificationDuration)
	}
	return m.filesRereadDirs(msg.host, msg.dirs)
}

// filesRereadDirs reads every side again that shows one of dirs on host.
func (m *OS) filesRereadDirs(host string, dirs []string) tea.Cmd {
	if !m.explorer.open {
		return nil
	}
	var cmds []tea.Cmd
	for _, t := range m.explorer.tabs {
		for _, s := range t.sides {
			if s == nil || m.filesHostOf(s) != host {
				continue
			}
			for _, d := range dirs {
				if d != "" && filepath.Clean(d) == filepath.Clean(filesSideDir(s)) {
					cmds = append(cmds, m.filesReadSide(s, filesSideDir(s), ""))
					break
				}
			}
		}
	}
	return tea.Batch(cmds...)
}

// filesTransferLanded reads the sides again that show the two ends of a
// copy that ended, so the file shows where it landed and goes where it
// left after a move.
func (m *OS) filesTransferLanded(row session.TransferRow) tea.Cmd {
	if !m.explorer.open || !transferEnded(row.State) {
		return nil
	}
	return tea.Batch(m.filesRereadDirs(row.Dst.Host, []string{row.Dst.Path, path.Dir(row.Dst.Path)}),
		m.filesRereadDirs(row.Src.Host, []string{path.Dir(row.Src.Path)}))
}

// filesArrivals is the copies on their way into side s's folder that have
// not landed: they show in the listing before they are there.
func (m *OS) filesArrivals(s *filesSide) []session.TransferRow {
	host, dir := m.filesHostOf(s), filesSideDir(s)
	var out []session.TransferRow
	for _, r := range m.transfers.rows {
		if r.Dst.Host != host || !filesLandsIn(r, dir) || r.State == "cancelled" || r.State == "failed" {
			continue
		}
		if r.State == "done" {
			// Shown until the listing has the file: the read is on its way.
			if filesHasName(s, r.Name) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// filesSettleLanded runs after a read of side s. It notes the copies the
// listing now has, so their rows say "checked" for a few seconds. A copy
// that ended while its file is not in the listing yet gets the folder read
// again, a few times at most.
func (m *OS) filesSettleLanded(s *filesSide) tea.Cmd {
	now := time.Now()
	for name, at := range s.landed {
		if now.Sub(at) > filesLandedFor {
			delete(s.landed, name)
		}
	}
	host, dir := m.filesHostOf(s), filesSideDir(s)
	missing := false
	for _, r := range m.transfers.rows {
		if r.State != "done" || r.Dst.Host != host || !filesLandsIn(r, dir) {
			continue
		}
		if !filesHasName(s, r.Name) {
			missing = true
			continue
		}
		if _, ok := s.landed[r.Name]; !ok && now.Sub(time.UnixMilli(r.Ended)) < filesLandedFor {
			if s.landed == nil {
				s.landed = map[string]time.Time{}
			}
			s.landed[r.Name] = now
		}
	}
	if !missing || s.settle >= filesSettleReads {
		s.settle = 0
		return nil
	}
	s.settle++
	return m.filesReadSideAfter(s, dir, "", filesSettleWait)
}

// filesLandedFor is how long a landed copy's row says "checked".
// filesSettleReads and filesSettleWait bound the reads again for a copy
// whose file the listing does not have yet.
const (
	filesLandedFor   = 5 * time.Second
	filesSettleReads = 5
	filesSettleWait  = 400 * time.Millisecond
)

// filesLandsIn reports whether copy r lands in the folder dir: its path is
// the folder, put into it, or a path in the folder.
func filesLandsIn(r session.TransferRow, dir string) bool {
	d := path.Clean(dir)
	return path.Clean(r.Dst.Path) == d || path.Dir(path.Clean(r.Dst.Path)) == d
}

// filesHasName reports whether side s lists name.
func filesHasName(s *filesSide, name string) bool {
	for _, en := range s.Entries {
		if en.Name == name {
			return true
		}
	}
	return false
}

// filesSizeText is a size for the footer.
func filesSizeText(n int64) string { return units.Bytes(n) }

// filesShortOn writes dir on host with the home folder as ~, when a side of
// the view knows that machine's home.
func (m *OS) filesShortOn(host, dir string) string {
	for _, t := range m.explorer.tabs {
		for _, s := range t.sides {
			if s != nil && s.home != "" && m.filesHostOf(s) == host {
				return s.short(dir)
			}
		}
	}
	if host == "" && m.AttachedHost == "" {
		return shortenHome(dir)
	}
	return dir
}
