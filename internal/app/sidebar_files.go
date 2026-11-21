package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pathroot"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type fileEntry struct {
	Name string
	Dir  bool
	Icon fileIcon
}

type fileViewState struct {
	Show      int8
	Dir       string
	Want      string
	Loading   bool
	QuietReq  uint64
	Origin    string
	Host      string
	Pinned    bool
	Entries   []fileEntry
	Err       string
	ErrAt     time.Time
	Gen       uint64
	Capped    bool
	Spoofed   bool
	Elsewhere string
}

const fileRetryInterval = time.Second

const fileViewMaxEntries = 2000

type fileListMsg struct {
	Gen     uint64
	Quiet   uint64
	Dir     string
	Entries []fileEntry
	Capped  bool
	Err     string
	Spoofed bool
}

var readDirFunc = session.ReadDirCapped

func (m *OS) queueSidebarCmd(cmd tea.Cmd) {
	if cmd != nil {
		m.sidebarPendingCmd = cmd
	}
}

// TakeSidebarCmd returns and clears whatever the last rail gesture produced.
func (m *OS) TakeSidebarCmd() tea.Cmd {
	cmd := m.sidebarPendingCmd
	m.sidebarPendingCmd = nil
	return cmd
}

// FileViewOpen reports whether the files section is on.
func (m *OS) FileViewOpen() bool { return m.filesOn() }

func (m *OS) filesOn() bool {
	return m.filesView.Show > 0 || sidebarLayoutHas(sidebarSectionFiles, &m.Settings)
}

// FileViewDir is the directory being listed, for tests and for anything that
// needs to say where the section is.
func (m *OS) FileViewDir() string { return m.filesView.Dir }

func (m *OS) filesSectionEnabled() bool {
	if (!m.Settings.SidebarEnabled && !m.sidebarDrawing) || !m.filesOn() {
		return false
	}
	w := m.GetSidebarWidth()
	return w > 0 && sidebarVariant(w) != sidebarVariantGlyph
}

func (m *OS) filesWantDir() string {
	window := m.GetFocusedWindow()
	if window == nil {
		return ""
	}
	if m.filesView.Pinned && m.filesView.Origin == window.ID {
		return m.filesView.Want
	}
	return m.paneDir(window)
}

func (m *OS) paneIsLocal(w *terminal.Window) bool {
	return w != nil && w.Host == "" && m.AttachedHost == ""
}

func (m *OS) paneDir(w *terminal.Window) string {
	if w != nil && !m.paneIsLocal(w) {
		return w.Cwd
	}
	return paneDir(w)
}

func paneShellDir(w *terminal.Window, fresh bool) (string, bool) {
	if w == nil {
		return "", false
	}
	var dir string
	if fresh {
		dir, _ = terminal.ShellCWD(w.ShellPgid)
	} else {
		dir = w.CWD()
	}
	if dir == "" {
		return "", false
	}
	if w.Cwd != "" && sameDir(dir, w.Cwd) {
		return w.Cwd, true
	}
	return dir, true
}

func paneDir(w *terminal.Window) string {
	if w == nil {
		return ""
	}
	if w.Cwd != "" {
		return w.Cwd
	}
	return w.CWD()
}

// FilesSyncCmd is the one place the section decides it needs a new listing. It
// is called once per message from Update, after the handler has run, so every
// path that can move the focus or change a pane's directory is covered by one
// comparison rather than by a hook in each of them.
//
// It answers nil, allocating nothing, for a client with no rail and for a
// section already showing the right directory, which is every message on an
// idle client.
func (m *OS) FilesSyncCmd() tea.Cmd {
	root := m.filesSyncRoot()
	tree := m.syncFileTree()
	switch {
	case tree == nil:
		return root
	case root == nil:
		return tree
	}
	return tea.Batch(root, tree)
}

func (m *OS) filesSyncRoot() tea.Cmd {
	if !m.filesSectionEnabled() {
		return nil
	}
	if w := m.GetFocusedWindow(); w != nil && w.CwdHost != "" {
		filesClearElsewhere(w)
		if w.CwdHost != "" {
			m.showElsewhere(w)
			return nil
		}
	}
	want := m.filesWantDir()
	if want == "" {
		if m.filesView.Want != "" && !m.fileViewFromLink() {
			m.clearFileView()
		}
		return nil
	}
	window := m.GetFocusedWindow()
	origin, host := "", ""
	if window != nil {
		origin, host = window.ID, window.Host
	}
	if want == m.filesView.Want && host == m.filesView.Host && !filesShouldRetry(m.filesView, want) {
		return nil
	}
	return m.requestFileList(want, origin, false)
}

func (m *OS) showElsewhere(w *terminal.Window) {
	v := m.filesView
	if v.Elsewhere == w.CwdHost && v.Origin == w.ID {
		return
	}
	m.filesView = fileViewState{
		Show:      v.Show,
		Gen:       v.Gen + 1,
		Want:      "elsewhere:" + w.CwdHost,
		Origin:    w.ID,
		Host:      w.Host,
		Elsewhere: w.CwdHost,
	}
	m.syncFileWatch()
}

func filesShouldRetry(v fileViewState, want string) bool {
	if v.Want != want {
		return true
	}
	if v.Loading || v.Err == "" {
		return false
	}
	return time.Since(v.ErrAt) >= fileRetryInterval
}

func (m *OS) fileViewFromLink() bool {
	return m.filesView.Pinned && m.filesView.Origin == ""
}

func (m *OS) clearFileView() {
	m.filesView = fileViewState{Show: m.filesView.Show, Gen: m.filesView.Gen + 1}
	m.syncFileWatch()
}

func (m *OS) requestFileList(dir, origin string, pinned bool) tea.Cmd {
	m.SidebarScroll[sidebarSectionFiles] = 0
	return m.readFileList(dir, origin, pinned)
}

func (m *OS) readFileList(dir, origin string, pinned bool) tea.Cmd {
	dir = filepath.Clean(dir)
	m.filesView.Want = dir
	m.filesView.Origin = origin
	m.filesView.Host = ""
	if w := m.windowByID(origin); w != nil {
		m.filesView.Host = w.Host
	}
	m.filesView.Pinned = pinned
	m.filesView.Elsewhere = ""
	m.filesView.Loading = true
	m.filesView.Err = ""
	m.filesView.Gen++
	return m.fileListCmd(dir, origin, pinned, 0)
}

func (m *OS) fileListCmd(dir, origin string, pinned bool, quiet uint64) tea.Cmd {
	pgid, wasSpoofed := 0, m.filesView.Spoofed
	if !pinned && origin != "" {
		wasSpoofed = false
		if w := m.windowByID(origin); w != nil && spoofCheckApplies(m.AttachedHost) {
			pgid = w.ShellPgid
		}
	}
	gen := m.filesView.Gen

	client, host := m.DaemonClient, m.AttachedHost
	read := func() fileListMsg {
		return readListing(client, host, origin, dir, pinned, pgid, wasSpoofed)
	}
	return func() tea.Msg {
		msg := read()
		msg.Gen, msg.Quiet = gen, quiet
		return msg
	}
}

func readListing(client *session.TUIClient, host, origin, dir string, pinned bool, pgid int, wasSpoofed bool) fileListMsg {
	if client != nil {
		listing, err := client.ReadDir(origin, dir, fileViewMaxEntries, pinned)
		switch {
		case err == nil && listing.Err != "":
			return fileListMsg{Dir: dir, Err: listing.Err, Spoofed: wasSpoofed || listing.Spoofed}
		case err == nil:
			entries := make([]fileEntry, 0, len(listing.Entries))
			for _, e := range listing.Entries {
				entries = append(entries, fileEntry{
					Name: e.Name, Dir: e.IsDir, Icon: fileIconFor(e.Name, e.IsDir),
				})
			}
			return fileListMsg{
				Dir: dir, Entries: entries,
				Capped: listing.Capped, Spoofed: wasSpoofed || listing.Spoofed,
			}
		case host != "":
			return fileListMsg{Dir: dir, Err: "That machine could not list it."}
		}
	}
	spoofed := wasSpoofed || cwdIsSpoofed(pgid, dir)
	items, capped, err := readDirFunc(pathroot.ExpandHome(dir), fileViewMaxEntries)
	if err != nil {
		return fileListMsg{Dir: dir, Err: session.DirReadError(err), Spoofed: spoofed}
	}
	entries := make([]fileEntry, 0, len(items))
	for _, it := range items {
		dir := it.IsDir()
		entries = append(entries, fileEntry{
			Name: it.Name(),
			Dir:  dir,
			Icon: fileIconFor(it.Name(), dir),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return fileListMsg{Dir: dir, Entries: entries, Capped: capped, Spoofed: spoofed}
}

func spoofCheckApplies(attachedHost string) bool { return attachedHost == "" }

func cwdIsSpoofed(pgid int, dir string) bool {
	procDir, ok := terminal.ShellCWD(pgid)
	if !ok {
		return false
	}
	return !sameDir(procDir, dir)
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// HandleFileList applies a finished read, or drops it.
//
// The generation is the whole guard. A read of a directory on a mount that has
// stopped answering can come back minutes later, long after the user moved on,
// and applying it would replace the listing they are looking at with one they
// left. Comparing the path instead of the generation is not enough: walking out
// of a folder and straight back into it is the same path twice.
func (m *OS) HandleFileList(msg fileListMsg) {
	if msg.Gen != m.filesView.Gen {
		return
	}
	if msg.Quiet != 0 {
		v := &m.filesView
		if msg.Quiet != v.QuietReq || v.Loading {
			return
		}
		if msg.Dir == v.Dir && msg.Err == v.Err && msg.Capped == v.Capped &&
			msg.Spoofed == v.Spoofed && slices.Equal(msg.Entries, v.Entries) {
			return
		}
		v.Gen++
	}
	m.filesView.Loading = false
	m.filesView.Dir = msg.Dir
	m.filesView.Err = msg.Err
	m.filesView.Entries = msg.Entries
	m.filesView.Capped = msg.Capped
	m.filesView.Spoofed = msg.Spoofed
	if msg.Err != "" {
		m.filesView.ErrAt = time.Now()
	} else {
		m.filesView.ErrAt = time.Time{}
	}
	m.syncFileWatch()
}

func (m *OS) recordWindowCwd(windowID, raw string) {
	w := m.windowByID(windowID)
	if w == nil || !m.paneIsLocal(w) {
		return
	}
	dir, host, ok := session.ParseCwdAnnouncement(raw)
	if !ok {
		return
	}
	if w.Pty != nil {
		w.CwdHost = host
		if host != "" {
			w.CwdElsewhereDir = dir
		}
	}
	if host == "" {
		w.Cwd = dir
		w.CwdAnnounced = true
	}
}

func filesClearElsewhere(w *terminal.Window) {
	if w != nil && w.CwdHost != "" && w.Pty != nil && w.ShellAtPrompt() {
		w.CwdHost = ""
	}
}

// ToggleFileView is the files switch. It puts the section in this rail's saved
// layout or takes it out, so the choice holds on every client and after a
// restart. A section that a link or a file search put up for the moment is
// taken down, and the saved layout is left as it is.
func (m *OS) ToggleFileView() tea.Cmd {
	inLayout := sidebarLayoutHas(sidebarSectionFiles, &m.Settings)
	if m.filesView.Show > 0 && !inLayout {
		m.CloseFileView()
		return nil
	}
	if inLayout {
		if sectionCount(config.ParseSidebarSections(m.Settings.SidebarSections), m.agentsOn()) <= 1 {
			m.ShowNotification("The rail keeps one section. Add another first.",
				"info", m.Settings.NotificationDuration)
			return nil
		}
		m.CloseFileView()
		return m.saveFilesInLayout(false)
	}
	if !m.SidebarActive() || sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
		return nil
	}
	window := m.GetFocusedWindow()
	if window == nil {
		m.ShowNotification("There is no pane to show files for.", "info", m.Settings.NotificationDuration)
		return nil
	}
	dir := m.paneDir(window)
	if dir == "" {
		m.ShowNotification(
			"tuios cannot read that pane's directory.",
			"info", m.Settings.NotificationDuration)
		return nil
	}
	return tea.Batch(m.saveFilesInLayout(true), m.requestFileList(dir, window.ID, false))
}

func (m *OS) saveFilesInLayout(on bool) tea.Cmd {
	layout := config.SidebarSectionsWithout(m.Settings.SidebarSections, "files")
	if on {
		layout = sidebarLayoutWithFiles(m.Settings.SidebarSections)
	}
	if m.sidebarDrawing && m.sidebarDrawingEdge != m.legacySidebarEdge() {
		m.editableSidebarEdge(m.sidebarDrawingEdge).Sections = layout
		m.Settings.SidebarSections = layout
		m.MarkAllDirty()
		return m.persistSettings()
	}
	return m.commitSectionString(layout)
}

// OpenFileView shows dir in the files section and reports whether it could.
//
// It refuses rather than half-works. The rail has to be on screen and wide
// enough to draw a path in, or the section would be one the user cannot see.
// The caller says what to do instead; a directory link falls back to the
// clipboard.
func (m *OS) OpenFileView(dir string) bool {
	if !m.SidebarActive() || sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
		return false
	}
	m.filesView.Show = 1
	m.queueSidebarCmd(m.requestFileList(dir, "", true))
	return true
}

// CloseFileView drops the listing, which is the only state here worth any
// memory, and ends a show that a link or a file search started. A layout that
// names the section still draws it: the files switch takes it out of the
// layout.
func (m *OS) CloseFileView() {
	m.filesView = fileViewState{}
	m.syncFileWatch()
	m.closeFilePrompt()
}

// RefreshFileView re-reads the current directory. It is the answer to a listing
// going stale, and it is a call rather than a timer for the reason at the top of
// this file: a watcher or a poll would put filesystem work back on a client that
// is doing nothing.
//
// Nothing on the rail calls it yet. It is here because it is the shape a refresh
// control has to have now that the read is a command, and because the alternative
// to a control is a timer.
func (m *OS) RefreshFileView() tea.Cmd {
	if !m.filesOn() || m.filesView.Want == "" {
		return nil
	}
	if m.filesTreeOn() {
		return tea.Batch(m.readFileList(m.filesView.Want, m.filesView.Origin, m.filesView.Pinned), m.refreshTreeAll())
	}
	return m.requestFileList(m.filesView.Want, m.filesView.Origin, m.filesView.Pinned)
}

const (
	fileBackLabel = "back to pane folder"
	fileBackKey   = "\x00back"
)

func (m *OS) fileViewAway() bool {
	if !m.filesView.Pinned || m.filesView.Dir == "" || m.filesView.Elsewhere != "" {
		return false
	}
	w := m.GetFocusedWindow()
	if w == nil || w.CwdHost != "" {
		return false
	}
	dir := m.paneDir(w)
	return dir != "" && !sameDir(dir, m.filesView.Dir)
}

// FileViewBack takes the listing back to the focused pane's folder, and the
// listing follows the pane again from there.
func (m *OS) FileViewBack() tea.Cmd {
	w := m.GetFocusedWindow()
	if !m.filesOn() || w == nil {
		return nil
	}
	dir := m.paneDir(w)
	if dir == "" {
		return nil
	}
	cmd := m.requestFileList(dir, w.ID, false)
	m.followFileRow("")
	return cmd
}

// FileViewUp walks to the parent directory. At the root there is no parent and
// nothing happens, which is why the row is not drawn there.
func (m *OS) FileViewUp() tea.Cmd { return m.fileViewUpFrom(m.filesView.Dir) }

func (m *OS) fileViewUpFrom(dir string) tea.Cmd {
	if !m.filesOn() || dir == "" {
		return nil
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return nil
	}
	cmd := m.requestFileList(parent, m.filesView.Origin, true)
	m.followFileRow(filepath.Base(dir))
	return cmd
}

// FileViewEnter acts on one row of the listing.
//
// A folder does whatever appearance.sidebar.folder_click says: walk the listing
// into it, tell the pane to cd there, or both. Navigate is the default because
// it is the only one that touches no program at all.
//
// A file puts its path on the clipboard. The rail sits next to a terminal, and
// the thing you want from a listing next to a terminal is the path, so you can
// paste it into the command you were already writing. Opening it instead would
// mean spawning an editor from a single click on a narrow list, which is a
// heavier act than a click on a name looks like it should be.
func (m *OS) FileViewEnter(index int) tea.Cmd {
	if !m.filesOn() || index < 0 || index >= len(m.filesView.Entries) {
		return nil
	}
	entry := m.filesView.Entries[index]
	return m.fileViewOpen(m.filesView.Dir, entry.Name, entry.Dir)
}

func (m *OS) fileViewOpen(dir, name string, isDir bool) tea.Cmd {
	if !m.filesOn() || dir == "" || name == "" {
		return nil
	}
	full := filepath.Join(dir, name)
	if isDir && m.filesTreeOn() && m.fileTreeUnderRoot(full) {
		return m.fileTreeTake(full)
	}
	if isDir {
		var cmd tea.Cmd
		if m.Settings.SidebarFolderClick != config.SidebarFolderClickCd {
			cmd = m.requestFileList(full, m.filesView.Origin, true)
			m.followFileRow("")
		}
		if m.Settings.SidebarFolderClick != config.SidebarFolderClickNavigate {
			m.sendCdToOrigin(full)
		}
		return cmd
	}
	if m.learnFiles() {
		m.learnOpenFile(full, false)
		return nil
	}
	return m.copyFilePath(full)
}

func (m *OS) copyFilePath(full string) tea.Cmd {
	m.ShowNotification("Copied the path.", "success", m.Settings.NotificationDuration)
	return tea.SetClipboard(full)
}

func (m *OS) followFileRow(name string) {
	m.sidebarFollowFile = true
	m.sidebarFollowFileName = name
	m.sidebarFollowFileGen = m.filesView.Gen
}

// FileViewCd sends a cd to the pane the section was opened from, for the
// directory the listing is showing. It is the header's control.
func (m *OS) FileViewCd() {
	if !m.filesOn() || m.filesView.Dir == "" {
		return
	}
	m.sendCdToOrigin(m.filesView.Dir)
}

func (m *OS) sendCdToOrigin(dir string) {
	window := m.fileViewOriginWindow()
	if window == nil {
		m.ShowNotification("This listing is not tied to a pane.", "info", m.Settings.NotificationDuration)
		return
	}
	if why, ok := paneBusyReason(window); !ok {
		m.ShowNotification(why, "warning", m.Settings.NotificationDuration)
		return
	}
	if _, ok := cdLine(dir); !ok {
		m.ShowNotification(cdRefusedMessage, "warning", m.Settings.NotificationDuration)
		return
	}
	m.cdAtPrompt(window, dir, false, cdUnseenMessage)
}

func (m *OS) fileViewOriginWindow() *terminal.Window {
	if m.filesView.Origin == "" {
		return nil
	}
	return m.windowByID(m.filesView.Origin)
}

func paneBusyReason(window *terminal.Window) (string, bool) {
	if window.Terminal != nil && window.Terminal.IsAltScreen() {
		return "That pane is running a full-screen program.", false
	}
	if runtime.GOOS == "windows" {
		return "tuios can not see what runs in that pane on this system.", false
	}
	if window.HasForegroundProcess() {
		if cmd := window.ForegroundCommand(); cmd != "" {
			return fmt.Sprintf("%s is running in that pane.", cmd), false
		}
		return "Something is running in that pane.", false
	}
	if window.ForegroundCmd != "" {
		return fmt.Sprintf("%s is running in that pane.", window.ForegroundCmd), false
	}
	return "", true
}

const cdRefusedMessage = "tuios did not type a cd. The folder name holds a quote, a backslash or a control character."

func (m *OS) cdAtPrompt(window *terminal.Window, dir string, clear bool, refused string) {
	line, ok := cdLine(dir)
	if !ok {
		return
	}
	if clear {
		line += " && clear"
	}
	if !window.DaemonMode {
		if !window.ShellAtPrompt() {
			if refused != "" {
				m.ShowNotification(refused, "warning", m.Settings.NotificationDuration)
			}
			return
		}
		if err := window.SendInput([]byte(line + "\r")); err != nil {
			m.LogError("Failed to type into window %s: %v", window.ID, err)
		}
		return
	}
	client := m.DaemonClient
	if client == nil || window.PTYID == "" {
		if refused != "" {
			m.ShowNotification(refused, "warning", m.Settings.NotificationDuration)
		}
		return
	}
	ch := m.ensureNotificationChan()
	dur := m.Settings.NotificationDuration
	ptyID, windowID := window.PTYID, window.ID
	go func() {
		typed, _ := client.CdAtPrompt(ptyID, dir, clear)
		if typed || refused == "" {
			return
		}
		select {
		case ch <- NotificationMsg{Message: refused, Type: "warning", Duration: dur, WindowID: windowID}:
		default:
		}
	}()
}

const cdUnseenMessage = "tuios did not type a cd. It can not see that the shell in that pane is at a prompt."

func cdLine(dir string) (string, bool) {
	return session.CdLine(dir)
}

func adoptWindowCwd(w *terminal.Window, cwd string) {
	if w != nil && cwd != "" && w.Cwd == "" {
		w.Cwd = cwd
	}
}

func (m *OS) takeDaemonCwd(w *terminal.Window, ws *session.WindowState) {
	if w == nil || ws == nil {
		return
	}
	w.CwdHost = ws.CwdHost
	if w.CwdAnnounced && m.paneIsLocal(w) {
		adoptWindowCwd(w, ws.Cwd)
		return
	}
	if ws.Cwd != "" && ws.Cwd != w.DaemonCwd {
		w.DaemonCwd = ws.Cwd
		w.Cwd = ws.Cwd
	}
}
