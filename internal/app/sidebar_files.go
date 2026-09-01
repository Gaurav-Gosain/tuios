package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// # The files section
//
// One of the rail's stacked sections, beside sessions, terminals and agents,
// listing what is in the focused pane's directory. It used to be a mode that
// took the whole rail instead; it is a section now because "what is here" is a
// question the user keeps an eye on while working, not one they go into and
// come out of, and because a rail that hid its sessions to show a folder made
// the two mutually exclusive for no reason the user asked for.
//
// # Why the read is a command and not a call
//
// A mode could read the directory on the gesture that opened it, because there
// was exactly one such gesture and the user had just made it. A section is on
// screen the whole time and follows the focused pane, so the read now happens
// whenever a shell cds, whenever the focus moves, and whenever a client
// attaches. Doing that on the goroutine that runs Update is the bug this
// codebase has already shipped three times: a clipboard call that held the UI
// for thirty seconds, a screenshot stall, and a config write on the update
// path. A hung NFS or sshfs mount would freeze every pane in the client until
// the kernel gave up on it.
//
// So the read is a tea.Cmd. requestFileList stamps the request with a
// generation, the command does the open, the bounded read and the sort in its
// own goroutine, and fileListMsg carries the generation back. A reply whose
// generation is not the current one is dropped: a slow answer for a directory
// the user has already left cannot overwrite the one they are looking at, and
// the "loading" row stays up for the request that is actually outstanding.
//
// Nothing polls. The listing is read when the focused pane's directory stops
// matching the one on screen, and at no other time, so a client sitting on an
// open rail does no filesystem work at all. That is also the whole of what the
// section knows how to notice: a file written into the directory by something
// else does not appear until the listing is asked for again.
//
// # What it is not
//
// It is not a file manager. It lists, it walks in and out, it hands a path to
// the clipboard and a directory to a shell, and it does the six file actions
// named in sidebar_file_ops.go: create, rename, delete, copy, cut and paste.
// That is the whole set. There is no multi-select, no tree, no filter and no
// drag and drop, because yeetui does all of that far better than twenty-six
// columns ever will, and it runs in a pane.

// fileEntry is one row of a listing. Only what the rail draws and what a click
// needs is kept: the rest of an os.DirEntry costs a stat per file and answers
// questions twenty-eight columns cannot ask.
type fileEntry struct {
	Name string
	Dir  bool
	// Icon is the file type's mark and colour, resolved here rather than when a
	// row is drawn. It depends only on the name, and the name does not change
	// between frames, so resolving it per drawn row would repeat the same map
	// lookups on every rail rebuild for as long as the listing is up.
	Icon fileIcon
}

// fileViewState is the files section's runtime state. It is a place the user
// has navigated to, not a preference, so it is not saved and a fresh client
// starts on the focused pane's own directory.
type fileViewState struct {
	// Show is this client's own switch for the section: zero follows the
	// layout, positive forces it on, negative forces it off. Three states
	// rather than a bool because the layout already decides whether the
	// section is there, and the footer control has to be able to disagree with
	// it in both directions without writing to the config file.
	Show int8
	// Dir is the directory the entries below belong to, absolute and cleaned.
	// Empty until the first reply lands.
	Dir string
	// Want is the directory that has been asked for. It leads Dir while a read
	// is in flight, and it is what the sync compares against so one directory
	// is never asked for twice.
	Want string
	// Loading is whether a read is outstanding for Want.
	Loading bool
	// Origin is the window the listing is tied to, or empty when it was opened
	// from a link. Only an origin pane can be told to change directory, because
	// only it is the one the user meant.
	Origin string
	// Pinned says the user steered the listing somewhere of their own, so it
	// stops following the origin pane's cwd. Cleared when the focus moves to
	// another pane, because the listing is then about a different pane.
	Pinned bool
	// Entries is the listing, directories first and then files, each group
	// sorted the way a person reads a name rather than the way a byte sorts.
	Entries []fileEntry
	// Err is why the listing is empty, when that is the reason.
	Err string
	// Gen stamps the outstanding request. The reply carries it back and is
	// dropped when it does not match, and the rail's render cache folds it in
	// so a listing that changed under an unchanged path still repaints.
	Gen uint64
	// Capped says the directory held more names than were read, so the listing
	// is the first fileViewMaxEntries of it and not the whole thing.
	Capped bool
}

// fileViewMaxEntries bounds one listing.
//
// The read is off the update goroutine now, so the cap is no longer about
// stalling the loop. It is about memory and about the answer being useful: a
// build tree, a node_modules or a maildir can hold six figures of names, the
// rail can show about thirty at a time, and every name past the cap is one
// nobody scrolls to.
const fileViewMaxEntries = 2000

// fileListMsg is one finished directory read on its way back to the loop.
type fileListMsg struct {
	Gen     uint64
	Dir     string
	Entries []fileEntry
	Capped  bool
	Err     string
}

// readDirFunc is the reader the file command calls. It is a variable so a test
// can hand it a directory that never answers and check that the client keeps
// drawing, which is the whole claim this design makes and the one thing a
// synchronous read could not pass.
var readDirFunc = readDirCapped

// queueSidebarCmd parks a command a rail row produced.
//
// SidebarClick answers a bool, not a command, and it is called from a dozen
// tests as well as from the click handler, so widening it would be a change to
// every one of them for the sake of two rows. The click handler drains this on
// its way out instead, which is the same shape the guest's own clipboard writes
// already take through the model.
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

// filesOn folds this client's switch together with the rail's layout.
func (m *OS) filesOn() bool {
	switch {
	case m.filesView.Show > 0:
		return true
	case m.filesView.Show < 0:
		return false
	default:
		return sidebarLayoutHas(sidebarSectionFiles, &m.Settings)
	}
}

// FileViewDir is the directory being listed, for tests and for anything that
// needs to say where the section is.
func (m *OS) FileViewDir() string { return m.filesView.Dir }

// filesSectionEnabled reports whether the rail would draw a files section at
// all: the layout has to name it, the user must not have switched it off, and
// the rail has to be wide enough to draw a listing in.
func (m *OS) filesSectionEnabled() bool {
	// The plain bool first, and deliberately. This is asked once per message
	// from Update, so it is on the idle path of every client; SidebarEnabled is
	// false for most of them and answering there costs a load and a branch,
	// where filesOn takes the layout mutex.
	if !m.Settings.SidebarEnabled || !m.filesOn() {
		return false
	}
	w := m.GetSidebarWidth()
	return w > 0 && sidebarVariant(w) != sidebarVariantGlyph
}

// filesWantDir is the directory the section should be showing.
//
// It follows the focused pane, except while the user has steered the listing
// somewhere of their own and the focus has not moved off the pane it was tied
// to. A user who walked into a subfolder is not dragged back out by a cd in the
// terminal, because the listing is then answering a question they asked and the
// pane's directory is not.
func (m *OS) filesWantDir() string {
	window := m.GetFocusedWindow()
	if window == nil {
		return ""
	}
	if m.filesView.Pinned && m.filesView.Origin == window.ID {
		return m.filesView.Want
	}
	return window.Cwd
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
	if !m.filesSectionEnabled() {
		return nil
	}
	want := m.filesWantDir()
	if want == "" {
		// There is nothing for the section to be about: no pane is focused, or
		// the focused one has never said where it is. Either way the listing on
		// screen belongs to a directory that is not the answer to the question
		// the section asks, so it goes.
		//
		// The comparison below cannot take this case. An empty want never
		// matches a directory that was asked for, so it would fall through and
		// ask for "" on every message forever. The guard is on the state rather
		// than on the answer for the same reason: this runs once per message,
		// and a client sitting with no pane must write nothing after the first
		// time.
		if m.filesView.Want != "" && !m.fileViewFromLink() {
			m.clearFileView()
		}
		return nil
	}
	if want == m.filesView.Want {
		return nil
	}
	window := m.GetFocusedWindow()
	origin := ""
	if window != nil {
		origin = window.ID
	}
	return m.requestFileList(want, origin, false)
}

// fileViewFromLink reports whether the listing was opened from a directory link
// rather than from a pane: pinned, with no origin window. OpenFileView is the
// one place that makes such a listing, and it names a folder the user asked for
// by hand.
//
// It is the one listing that survives having no pane, because it was never
// about a pane. The section is answering "what is in the folder you clicked",
// and the answer to that does not change when a shell exits. A pane that opens
// afterwards and reports a directory takes the section back, through the same
// comparison every other pane goes through.
func (m *OS) fileViewFromLink() bool {
	return m.filesView.Pinned && m.filesView.Origin == ""
}

// clearFileView drops the listing and everything that describes it, for when
// there is nothing to list it for.
//
// Show survives. It is the user's own on and off switch for the section and not
// part of the listing, so a pane exiting must not answer it. Zeroing it would:
// zero means "follow the layout", so a section the user had forced on would go
// off for anyone whose layout does not name files. A section the user had
// switched off never reaches here, because a switched off section is not
// enabled and the sync returns above, but the field is kept for the switch's
// sake either way. The scroll offset survives too: a section drawing no rows
// cannot be scrolled, and the next listing zeroes it anyway.
//
// The generation is bumped and never reset. A read can be in flight when the
// last pane closes, and its reply carries the generation it was stamped with;
// bumping means that reply no longer matches and HandleFileList drops it.
// Resetting to zero would be worse than leaving it: the next request would
// stamp a number that has already been handed out, and the stale reply would
// land on it.
//
// Origin and Pinned go with the rest. A pin is a pin to one pane's listing, and
// a pane that has closed cannot be the one the user meant.
func (m *OS) clearFileView() {
	m.filesView = fileViewState{Show: m.filesView.Show, Gen: m.filesView.Gen + 1}
}

// requestFileList stamps a new request and returns the command that answers it.
// The read, the cap and the sort all run in the command's own goroutine; this
// only writes down what was asked for.
func (m *OS) requestFileList(dir, origin string, pinned bool) tea.Cmd {
	dir = filepath.Clean(dir)
	m.filesView.Want = dir
	m.filesView.Origin = origin
	m.filesView.Pinned = pinned
	m.filesView.Loading = true
	m.filesView.Err = ""
	m.SidebarScrollF = 0
	m.filesView.Gen++
	gen := m.filesView.Gen
	return func() tea.Msg {
		items, capped, err := readDirFunc(dir, fileViewMaxEntries)
		if err != nil {
			return fileListMsg{Gen: gen, Dir: dir, Err: fileViewError(err)}
		}
		entries := make([]fileEntry, 0, len(items))
		for _, it := range items {
			// Type() is what the directory read already returned, so this costs
			// no stat. A symlink to a directory therefore reads as a file, which
			// is the price of not stat'ing every name in a large tree. The icon
			// is looked up off the same two facts and for the same reason.
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
		return fileListMsg{Gen: gen, Dir: dir, Entries: entries, Capped: capped}
	}
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
	m.filesView.Loading = false
	m.filesView.Dir = msg.Dir
	m.filesView.Err = msg.Err
	m.filesView.Entries = msg.Entries
	m.filesView.Capped = msg.Capped
}

// recordWindowCwd stores a pane's reported directory.
//
// It is called from the cwd-change handler, which is driven by OSC 7 and so
// runs only when a shell actually changes directory. Nothing polls it. Whether
// the section follows the new directory is FilesSyncCmd's decision, made once
// per message against the focused pane, so this does not have to know anything
// about the rail.
func (m *OS) recordWindowCwd(windowID, raw string) {
	dir, ok := localCwdPath(raw)
	if !ok {
		return
	}
	for _, w := range m.Windows {
		if w != nil && w.ID == windowID {
			w.Cwd = dir
			return
		}
	}
}

// ToggleFileView turns the files section on or off for this client.
func (m *OS) ToggleFileView() tea.Cmd {
	if m.filesOn() {
		m.CloseFileView()
		return nil
	}
	if !m.SidebarActive() || sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
		return nil
	}
	window := m.GetFocusedWindow()
	if window == nil {
		m.ShowNotification("There is no pane to show files for.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if window.Cwd == "" {
		m.ShowNotification(
			"tuios does not know where that pane is. The shell has to report its directory.",
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.filesView.Show = 1
	return m.requestFileList(window.Cwd, window.ID, false)
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
	// A link names a directory of its own, so the listing is pinned there
	// rather than snapping back to the focused pane on the next message.
	m.queueSidebarCmd(m.requestFileList(dir, "", true))
	return true
}

// CloseFileView takes the section off the rail and drops the listing, which is
// the only state here worth any memory. The switch is left at "off" rather than
// at "follow the layout", or a rail whose layout names the section would draw
// it again on the next frame and the control would look broken.
func (m *OS) CloseFileView() {
	m.filesView = fileViewState{Show: -1}
	// A dialog asking about a file in a listing that is no longer on screen has
	// nothing to point at. It is dropped with the listing, and an operation
	// already running is left to finish and report. See sidebar_file_ops.go.
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
	return m.requestFileList(m.filesView.Want, m.filesView.Origin, m.filesView.Pinned)
}

// readDirCapped reads at most limit names from dir and says whether there were
// more.
//
// os.ReadDir is the obvious call and the wrong one here: it reads the whole
// directory and sorts it before returning, so its cost is the directory's size
// and there is no point at which a caller can stop it. This opens the directory
// and takes one bounded batch instead, then asks for one more name only to find
// out whether the listing is complete.
//
// The batch arrives in whatever order the filesystem hands it over, unlike
// os.ReadDir's sorted result. The caller sorts it either way, so the only thing
// that changes is which names a capped listing holds, and on a directory that
// large there is no ordering that makes the cut the right one.
func readDirCapped(dir string, limit int) (entries []os.DirEntry, capped bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	// io.EOF is how a directory with fewer than limit names left in it reports
	// that it is finished, so it is the expected end and not a failure.
	entries, err = f.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if len(entries) < limit {
		return entries, false, nil
	}
	more, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		// The batch above is good and the only thing this second read decides is
		// a note on one row, so a failure here loses the note rather than the
		// listing.
		return entries, false, nil
	}
	return entries, len(more) > 0, nil
}

// fileViewError turns a read failure into one short sentence the rail can draw.
// The wrapped error carries the whole path, which is already on the header row
// above it and does not fit twice.
func fileViewError(err error) string {
	switch {
	case os.IsNotExist(err):
		return "That folder is gone."
	case os.IsPermission(err):
		return "You can not read that folder."
	default:
		return "Could not read that folder."
	}
}

// FileViewUp walks to the parent directory. At the root there is no parent and
// nothing happens, which is why the row is not drawn there.
func (m *OS) FileViewUp() tea.Cmd { return m.fileViewUpFrom(m.filesView.Dir) }

// fileViewUpFrom is FileViewUp for a folder named by the caller, which is what
// the files menu's "Go up" row needs: the menu carries the folder it was opened
// over, and that is the folder the row means whatever the listing has done
// since.
func (m *OS) fileViewUpFrom(dir string) tea.Cmd {
	if !m.filesOn() || dir == "" {
		return nil
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return nil
	}
	return m.requestFileList(parent, m.filesView.Origin, true)
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

// fileViewOpen is what a row of the listing does when it is taken, addressed by
// name rather than by an index into the listing on screen.
//
// The menu's Open row goes through here too, with the folder and the name it
// was opened on. One body, so the two ways of taking a row can never come to
// mean different things, and so the folder_click setting is read in one place.
func (m *OS) fileViewOpen(dir, name string, isDir bool) tea.Cmd {
	if !m.filesOn() || dir == "" || name == "" {
		return nil
	}
	full := filepath.Join(dir, name)
	if isDir {
		var cmd tea.Cmd
		if m.Settings.SidebarFolderClick != config.SidebarFolderClickCd {
			cmd = m.requestFileList(full, m.filesView.Origin, true)
		}
		if m.Settings.SidebarFolderClick != config.SidebarFolderClickNavigate {
			m.sendCdToOrigin(full)
		}
		return cmd
	}
	m.ShowNotification("Copied the path.", "success", m.Settings.NotificationDuration)
	return m.WriteClipboard(full)
}

// FileViewCd sends a cd to the pane the section was opened from, for the
// directory the listing is showing. It is the header's control.
func (m *OS) FileViewCd() {
	if !m.filesOn() || m.filesView.Dir == "" {
		return
	}
	m.sendCdToOrigin(m.filesView.Dir)
}

// sendCdToOrigin types a cd into the pane the listing is tied to.
//
// This is the one action here that types into somebody else's program, and the
// guard matters more than the action. What is on the other end of a pane is not
// known to be a shell: it is whatever the user last ran, and "cd /x\r" typed
// into vim is a series of editing commands, into a REPL a syntax error, and
// into a database client a query. So the pane has to be at a prompt, and tuios
// has to be able to see that it is, and both are checked before anything is
// written.
//
// It refuses with a reason rather than guessing. A refusal that names the
// program in the way is something the user can act on; a cd that silently went
// somewhere else is not.
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
	line := "cd " + shellQuote(dir) + "\r"
	if err := window.SendInput([]byte(line)); err != nil {
		m.LogError("Failed to send cd to window %s: %v", window.ID, err)
		m.ShowNotification("Could not write to that pane.", "error", m.Settings.NotificationDuration)
	}
}

// fileViewOriginWindow is the pane the listing is tied to, or nil.
func (m *OS) fileViewOriginWindow() *terminal.Window {
	if m.filesView.Origin == "" {
		return nil
	}
	for _, w := range m.Windows {
		if w != nil && w.ID == m.filesView.Origin {
			return w
		}
	}
	return nil
}

// paneBusyReason reports whether a pane is at a shell prompt, and when it is
// not, one sentence saying why tuios will not type into it.
//
// Three tests, in order of how sure they are.
//
// The alternate screen is the surest and the cheapest: a program that switched
// to it has taken the whole screen and is not a prompt, and the emulator knows
// that on every platform without asking the operating system anything.
//
// The foreground command is the real answer. tuios already has it twice over:
// a local pane's own PTY reports its foreground process group, and a daemon
// pane gets the same observation on the wire, at most one poll interval stale.
// Empty means the foreground process is the login shell, which is the prompt.
//
// And a platform that cannot answer refuses. On Windows neither reader is
// implemented, so an empty command means "nobody looked" rather than "nothing
// is running", and treating the two the same is how a cd ends up inside an
// editor. Failing closed costs the feature on that platform and costs nothing
// anywhere else.
func paneBusyReason(window *terminal.Window) (string, bool) {
	if window.Terminal != nil && window.Terminal.IsAltScreen() {
		return "That pane is running a full-screen program.", false
	}
	if runtime.GOOS == "windows" {
		return "tuios can not see what runs in that pane on this system.", false
	}
	if cmd := window.ForegroundCommand(); cmd != "" {
		return fmt.Sprintf("%s is running in that pane.", cmd), false
	}
	if window.ForegroundCmd != "" {
		return fmt.Sprintf("%s is running in that pane.", window.ForegroundCmd), false
	}
	if window.HasForegroundProcess() {
		return "Something is running in that pane.", false
	}
	return "", true
}

// shellQuote wraps a path in single quotes for a POSIX shell, escaping any
// single quote in it the way a shell requires: close, escape, reopen.
//
// The path came off a filesystem, so it may hold a space, a quote, a newline or
// a semicolon, and the string is about to be typed at a prompt. Quoting is
// what keeps a directory called "; rm -rf ~" from being two commands.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
