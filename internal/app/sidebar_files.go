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

// # Why the file view is a mode and not a fourth section
//
// render_sidebar.go already carries a written refusal to add a fourth section,
// for workspaces, and two of its four reasons apply here word for word. The
// budget's floors claim ten lines of chrome before a single row of content, and
// a fourth floor takes that to thirteen against a rail that has already once
// overrun its region; and a file listing, like a workspace list, would be the
// second per-session list, competing with the terminals section for the lines
// the user actually works in. A section is therefore the wrong shape.
//
// The other two reasons do not apply, which is why this exists at all. A
// listing restates nothing that is already on screen, and there is no other
// surface that shows it.
//
// A mode answers both objections rather than arguing with them. Off, it costs
// the rail nothing: no header, no floor, no place in the give-up ladder, and
// the three sections lay out exactly as they did. On, it takes the whole rail,
// because the user has just asked for it and a listing squeezed into a third of
// twenty rows is not worth having. And it says the honest thing about what it
// is: sessions, terminals and agents all answer "what is running", and this
// answers "what is here", which is a different question you go into and come
// out of rather than one you keep an eye on.
//
// # What it is not
//
// It is not a file manager. It lists, it walks in and out, it hands a path to
// the clipboard and a directory to a shell. It does not create, rename, delete
// or move anything: yeetui does all of that far better than twenty-eight
// columns ever will, and it runs in a pane.
//
// # Cost
//
// The listing is read on an explicit act and at no other time: opening the
// view, walking into or out of a directory, pressing refresh, and the origin
// pane reporting a new directory while the view is open. There is no watcher,
// no poll and no tick, so an idle client with the view open does exactly the
// work an idle client with it closed does, which is none.

// fileEntry is one row of a listing. Only what the rail draws and what a click
// needs is kept: the rest of an os.DirEntry costs a stat per file and answers
// questions twenty-eight columns cannot ask.
type fileEntry struct {
	Name string
	Dir  bool
}

// fileViewState is the rail's file view. Runtime only: it is a place the user
// has navigated to, not a preference, so it is not saved and a fresh client
// starts on the pane's own directory.
type fileViewState struct {
	// Open is whether the rail is showing the listing instead of its sections.
	Open bool
	// Dir is the directory being listed, absolute and cleaned.
	Dir string
	// Origin is the window the view was opened from, or empty when it was
	// opened from a link. Only an origin pane can be told to change directory,
	// because only it is the one the user meant.
	Origin string
	// Entries is the listing, directories first and then files, each group
	// sorted the way a person reads a name rather than the way a byte sorts.
	Entries []fileEntry
	// Err is why the listing is empty, when that is the reason.
	Err string
	// Scroll is the first row drawn.
	Scroll int
	// Gen bumps on every reload. The rail's render cache folds it in, so a
	// listing that changed under an unchanged path still repaints.
	Gen uint64
	// Capped says the directory held more names than were read, so the listing
	// is the first fileViewMaxEntries of it and not the whole thing. The rail
	// says so where it would otherwise count the rows below the fold.
	Capped bool
}

// fileViewMaxEntries bounds one listing.
//
// A directory read is one syscall loop on the goroutine that also runs Update,
// and a build tree, a node_modules or a maildir can hold six figures of names.
// Reading all of them costs the loop the whole read and then a sort of the
// result, and the rail can show about thirty at a time, so the last ninety-nine
// thousand are paid for and never looked at.
//
// The number is far past what anyone scrolls a twenty-eight column rail through
// and far short of what stalls the loop.
const fileViewMaxEntries = 2000

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

// FileViewOpen reports whether the rail is in its file view.
func (m *OS) FileViewOpen() bool { return m.filesView.Open }

// FileViewDir is the directory being listed, for tests and for anything that
// needs to say where the view is.
func (m *OS) FileViewDir() string { return m.filesView.Dir }

// recordWindowCwd stores a pane's reported directory and refreshes the view if
// it is the one being shown.
//
// It is called from the cwd-change handler, which is driven by OSC 7 and so
// runs only when a shell actually changes directory. Nothing polls it.
func (m *OS) recordWindowCwd(windowID, raw string) {
	dir, ok := localCwdPath(raw)
	if !ok {
		return
	}
	var window *terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.ID == windowID {
			window = w
			break
		}
	}
	if window == nil || window.Cwd == dir {
		return
	}
	// The pane's previous directory, captured before it is overwritten. It is
	// what decides whether the view was following the pane or had been steered
	// somewhere else, and reading it back off the window afterwards is exactly
	// the bug this line exists to not have: by then it is the new directory and
	// the test can never fail.
	was := window.Cwd
	window.Cwd = dir

	// The view follows the pane it was opened from, but only while it is still
	// showing that pane's own directory. A user who has walked somewhere else in
	// the listing is not dragged back by a cd in the terminal, because the
	// listing is then answering a question they asked and the pane's is not.
	if m.filesView.Open && m.filesView.Origin == windowID && m.filesView.Dir == was {
		m.loadFileView(dir)
	}
}

// ToggleFileView opens the rail's file view on the focused pane's directory, or
// closes it if it is already open.
func (m *OS) ToggleFileView() {
	if m.filesView.Open {
		m.CloseFileView()
		return
	}
	window := m.GetFocusedWindow()
	if window == nil {
		m.ShowNotification("There is no pane to show files for.", "info", config.NotificationDuration)
		return
	}
	if window.Cwd == "" {
		m.ShowNotification(
			"tuios does not know where that pane is. The shell has to report its directory.",
			"info", config.NotificationDuration)
		return
	}
	if !m.OpenFileView(window.Cwd) {
		return
	}
	m.filesView.Origin = window.ID
}

// OpenFileView shows dir in the rail and reports whether it could.
//
// It refuses rather than half-works. The rail has to be on screen and wide
// enough to draw a path in, or the view would be a mode the user cannot see and
// cannot get out of. The caller says what to do instead; a directory link falls
// back to the clipboard.
func (m *OS) OpenFileView(dir string) bool {
	if !m.SidebarActive() || sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
		return false
	}
	m.filesView.Open = true
	m.filesView.Origin = ""
	m.loadFileView(dir)
	return true
}

// CloseFileView puts the rail back on its sections and drops the listing, which
// is the only state here worth any memory.
func (m *OS) CloseFileView() {
	m.filesView = fileViewState{}
}

// loadFileView reads a directory and makes it the view's.
//
// This is the only place a directory is read. Every caller is an act the user
// performed, so a listing is never one render behind and never one render's
// worth of syscalls either.
func (m *OS) loadFileView(dir string) {
	dir = filepath.Clean(dir)
	m.filesView.Dir = dir
	m.filesView.Scroll = 0
	m.filesView.Entries = m.filesView.Entries[:0]
	m.filesView.Err = ""
	m.filesView.Capped = false
	m.filesView.Gen++

	items, capped, err := readDirCapped(dir, fileViewMaxEntries)
	if err != nil {
		m.filesView.Err = fileViewError(err)
		return
	}
	m.filesView.Capped = capped
	for _, it := range items {
		m.filesView.Entries = append(m.filesView.Entries, fileEntry{
			Name: it.Name(),
			// Type() is what the directory read already returned, so this costs
			// no stat. A symlink to a directory therefore reads as a file, which
			// is the price of not stat'ing every name in a large tree.
			Dir: it.IsDir(),
		})
	}
	sort.Slice(m.filesView.Entries, func(i, j int) bool {
		a, b := m.filesView.Entries[i], m.filesView.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
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

// RefreshFileView re-reads the current directory. It is the answer to a listing
// going stale, and it is a control rather than a timer for the reason at the top
// of this file.
func (m *OS) RefreshFileView() {
	if m.filesView.Open {
		m.loadFileView(m.filesView.Dir)
	}
}

// FileViewUp walks to the parent directory. At the root there is no parent and
// nothing happens, which is why the row is not drawn there.
func (m *OS) FileViewUp() {
	if !m.filesView.Open {
		return
	}
	parent := filepath.Dir(m.filesView.Dir)
	if parent == m.filesView.Dir {
		return
	}
	m.loadFileView(parent)
}

// FileViewEnter acts on one row of the listing.
//
// A directory walks the view into it. That is the safe half of what "clicking a
// folder navigates to it" can mean, and it involves no program: the rail moves,
// nothing is typed anywhere, and a pane running a build is not touched.
//
// A file puts its path on the clipboard. The rail sits next to a terminal, and
// the thing you want from a listing next to a terminal is the path, so you can
// paste it into the command you were already writing. Opening it instead would
// mean spawning an editor from a single click on a narrow list, which is a
// heavier act than a click on a name looks like it should be.
func (m *OS) FileViewEnter(index int) tea.Cmd {
	if !m.filesView.Open || index < 0 || index >= len(m.filesView.Entries) {
		return nil
	}
	entry := m.filesView.Entries[index]
	full := filepath.Join(m.filesView.Dir, entry.Name)
	if entry.Dir {
		m.loadFileView(full)
		return nil
	}
	m.ShowNotification("Copied the path.", "success", config.NotificationDuration)
	return tea.SetClipboard(full)
}

// FileViewCd sends a cd to the pane the view was opened from.
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
func (m *OS) FileViewCd() tea.Cmd {
	if !m.filesView.Open {
		return nil
	}
	window := m.fileViewOriginWindow()
	if window == nil {
		m.ShowNotification("This listing is not tied to a pane.", "info", config.NotificationDuration)
		return nil
	}
	if why, ok := paneBusyReason(window); !ok {
		m.ShowNotification(why, "warning", config.NotificationDuration)
		return nil
	}
	line := "cd " + shellQuote(m.filesView.Dir) + "\r"
	if err := window.SendInput([]byte(line)); err != nil {
		m.LogError("Failed to send cd to window %s: %v", window.ID, err)
		m.ShowNotification("Could not write to that pane.", "error", config.NotificationDuration)
		return nil
	}
	return nil
}

// fileViewOriginWindow is the pane the view was opened from, or nil.
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
