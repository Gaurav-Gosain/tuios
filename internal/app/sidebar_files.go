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

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
// matching the one on screen, and when the kernel says the listed folder's
// entries changed (sidebar_files_watch.go). A client sitting on an open rail
// over a folder nobody touches does no filesystem work at all.
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
	// QuietReq stamps the latest quiet read: a re-read because the folder
	// changed on disk. Such a read leaves Gen and Loading alone, so it draws
	// no "loading" row and the rail is not rebuilt for it. The user asked for
	// nothing, and a row that blinks on every file a build writes is noise.
	QuietReq uint64
	// Origin is the window the listing is tied to, or empty when it was opened
	// from a link. Only an origin pane can be told to change directory, because
	// only it is the one the user meant.
	Origin string
	// Host is the machine the listing was read from, empty for this one.
	//
	// It is part of what identifies a listing, alongside the directory. Two
	// panes on two machines are very often in the same directory, because
	// /home/ubuntu is /home/ubuntu everywhere, and comparing the path alone
	// made moving between them leave the first machine's files on screen.
	Host string
	// Pinned says the user steered the listing somewhere of their own, so it
	// stops following the origin pane's cwd. Cleared when the focus moves to
	// another pane, because the listing is then about a different pane.
	Pinned bool
	// Entries is the listing, directories first and then files, each group
	// sorted the way a person reads a name rather than the way a byte sorts.
	Entries []fileEntry
	// Err is why the listing is empty, when that is the reason.
	Err string
	// ErrAt is when that failure came back, so a retry is paced rather than run
	// on every message. See FilesSyncCmd.
	ErrAt time.Time
	// Gen stamps the outstanding request. The reply carries it back and is
	// dropped when it does not match, and the rail's render cache folds it in
	// so a listing that changed under an unchanged path still repaints.
	Gen uint64
	// Capped says the directory held more names than were read, so the listing
	// is the first fileViewMaxEntries of it and not the whole thing.
	Capped bool
	// Spoofed says the pane named this folder over OSC 7 and /proc disagreed.
	// The listing stays; the file actions do not. See cwdIsSpoofed.
	Spoofed bool
	// Elsewhere names the machine the origin pane's shell is on when it is not
	// the machine the pane runs on: the pane is running ssh. Nothing is listed
	// then, because the folder is on a disk no daemon here can read, and a
	// folder of the same name on this disk is a different folder.
	Elsewhere string
}

// fileRetryInterval paces retrying a directory that could not be read. Short
// enough that a pane whose session has just been created fills in while the
// person is still looking at it, long enough that a directory which stays
// unreadable costs one ask a second rather than one per message.
const fileRetryInterval = time.Second

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
	Gen uint64
	// Quiet is the QuietReq of a quiet read, zero for a read the user asked
	// for.
	Quiet   uint64
	Dir     string
	Entries []fileEntry
	Capped  bool
	Err     string
	// Spoofed is the corroboration verdict for a listing the pane steered. It
	// is worked out in the command, beside the read, because it costs a /proc
	// readlink and two stats and neither belongs on the update goroutine.
	Spoofed bool
}

// readDirFunc is the reader the file command calls. It is a variable so a test
// can hand it a directory that never answers and check that the client keeps
// drawing, which is the whole claim this design makes and the one thing a
// synchronous read could not pass.
var readDirFunc = session.ReadDirCapped

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
	return m.paneDir(window)
}

// paneIsLocal reports whether a pane's process runs on this client's machine.
// Only then can the client read the process, or judge the host an OSC 7 report
// names against its own.
func (m *OS) paneIsLocal(w *terminal.Window) bool {
	return w != nil && w.Host == "" && m.AttachedHost == ""
}

// paneDir is the directory a pane of this client's session is in. A pane on
// another machine has only the daemon's answer: the pid the client holds for
// it is a pid on that machine, and reading it here reads some other process or
// none.
func (m *OS) paneDir(w *terminal.Window) string {
	if w != nil && !m.paneIsLocal(w) {
		return w.Cwd
	}
	return paneDir(w)
}

// paneDir is the directory a pane is in.
//
// A shell announces it over OSC 7, and that is the answer when there is one: it
// is what the shell believes, it arrives the moment the directory changes, and
// it is right for a shell on the far side of an ssh session where no local
// process could be read.
//
// A shell that never announces one is not a pane with no directory, though,
// and treating it as one is why the files section spent half its time refusing
// to open. Shell integration is not installed everywhere, and even where it is
// the first announcement comes with the first prompt, so a pane that had not
// been cd'd in yet had nothing to show. The process the pane is running has a
// working directory whether or not anybody announced it, so that is the
// fallback, and it is the same read the window title already uses.
//
// The empty string still means unknown: no pgid, a platform that cannot read
// one, or a process that is gone.
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
	window := m.GetFocusedWindow()
	origin, host := "", ""
	if window != nil {
		origin, host = window.ID, window.Host
	}
	// The directory and the machine together. The same path on two machines is
	// two different directories, and the section following the focus between
	// them has to ask again rather than keep the answer it has.
	if want == m.filesView.Want && host == m.filesView.Host && !filesShouldRetry(m.filesView, want) {
		return nil
	}
	return m.requestFileList(want, origin, false)
}

// showElsewhere puts the section in its "shell is on another machine" state for
// w. It writes nothing when the section already says that about w, because the
// sync runs once per message.
func (m *OS) showElsewhere(w *terminal.Window) {
	v := m.filesView
	if v.Elsewhere == w.CwdHost && v.Origin == w.ID {
		return
	}
	m.filesView = fileViewState{
		Show: v.Show,
		Gen:  v.Gen + 1,
		// Want is what the next sync compares with, so the section asks for
		// a listing again the moment the pane is back on its own machine. It
		// is no path, so it never matches one.
		Want:      "elsewhere:" + w.CwdHost,
		Origin:    w.ID,
		Host:      w.Host,
		Elsewhere: w.CwdHost,
	}
	m.syncFileWatch()
}

// filesShouldRetry reports whether a directory already asked for is worth
// asking for again.
//
// Want is set when a read is asked for, not when one succeeds. So a listing
// that failed leaves Want pointing at the directory it could not read, and a
// sync comparing on Want alone answers "already asked for that" forever: the
// section stays empty until something else moves the focus, which is why
// switching sessions away and back appeared to fix it.
//
// A failure is a reason to try again. Most of them are temporary, and the one
// that prompted this is the most temporary of all: a session just created on
// another machine has a pane whose daemon has not been asked for its directory
// yet. Paced off when the failure came back, because the alternative is a
// request to the daemon on every message for as long as the directory stays
// unreadable, and this runs once per message.
func filesShouldRetry(v fileViewState, want string) bool {
	if v.Want != want {
		return true // a different directory is always worth asking for
	}
	if v.Loading || v.Err == "" {
		return false // in flight, or already on screen
	}
	return time.Since(v.ErrAt) >= fileRetryInterval
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
	m.syncFileWatch()
}

// requestFileList stamps a new request and returns the command that answers it.
// The read, the cap and the sort all run in the command's own goroutine; this
// only writes down what was asked for.
//
// The corroboration goes in the same command, for the same reason the read
// does: it costs a readlink and two stats, and a client with the rail open asks
// for a listing every time a shell cds. What is taken here on the loop is one
// int off the window, which is a field written once when the pane was spawned.
func (m *OS) requestFileList(dir, origin string, pinned bool) tea.Cmd {
	// A listing the user asked for starts at its top. A quiet re-read of the
	// same folder goes through readFileList and keeps the scroll.
	m.SidebarScrollF = 0
	return m.readFileList(dir, origin, pinned)
}

// readFileList is requestFileList without the reset of the scroll position.
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

// fileListCmd is the command that reads dir and answers with a fileListMsg
// stamped with the current Gen and quiet. It changes no state, so a quiet
// read can send it without touching what the rail draws.
func (m *OS) fileListCmd(dir, origin string, pinned bool, quiet uint64) tea.Cmd {
	// Only a listing the pane steered is checked. A folder the user walked into
	// by hand is a folder they named, so it is theirs whatever the pane says,
	// and a pinned listing keeps the verdict of the pane-driven listing it was
	// reached from: walking into a subfolder of a spoofed directory does not
	// launder it.
	pgid, wasSpoofed := 0, m.filesView.Spoofed
	if !pinned && origin != "" {
		wasSpoofed = false
		if w := m.windowByID(origin); w != nil && spoofCheckApplies(m.AttachedHost) {
			pgid = w.ShellPgid
		}
	}
	gen := m.filesView.Gen

	// The listing is asked of the daemon that holds the session, because that is
	// the daemon that holds the disk the pane is on. Reading it here was right
	// for as long as a pane could only be on this machine; a session attached on
	// another host made this client list its own disk and report that the pane's
	// directory did not exist.
	//
	// It goes through the daemon for a local session too. One path answers for
	// both, so there is nothing for the two to disagree about, and the daemon is
	// also the only side that can say whether the pane announced a directory its
	// shell is not in.
	client, host := m.DaemonClient, m.AttachedHost
	read := func() fileListMsg {
		if client != nil {
			listing, err := client.ReadDir(origin, dir, fileViewMaxEntries, pinned)
			switch {
			case err == nil && listing.Err != "":
				return fileListMsg{Dir: dir, Err: listing.Err, Spoofed: wasSpoofed || listing.Spoofed}
			case err == nil:
				entries := make([]fileEntry, 0, len(listing.Entries))
				for _, e := range listing.Entries {
					// Already in the daemon's order: directories first, then
					// names. Sorting again here would be sorting a capped
					// listing, which is an arbitrary subset of the directory.
					entries = append(entries, fileEntry{
						Name: e.Name, Dir: e.IsDir, Icon: fileIconFor(e.Name, e.IsDir),
					})
				}
				return fileListMsg{
					Dir: dir, Entries: entries,
					Capped: listing.Capped, Spoofed: wasSpoofed || listing.Spoofed,
				}
			case host != "":
				// A daemon on another machine that cannot answer is the end of
				// it. Falling through to read this machine's disk would list a
				// directory with nothing to do with the pane, which is the bug
				// this replaced rather than a fallback.
				return fileListMsg{Dir: dir, Err: "That machine could not list it."}
			}
			// A local daemon that could not answer falls through: a build older
			// than this message answers with an error, and reading the disk here
			// is the same disk it would have read.
		}
		spoofed := wasSpoofed || cwdIsSpoofed(pgid, dir)
		items, capped, err := readDirFunc(dir, fileViewMaxEntries)
		if err != nil {
			return fileListMsg{Dir: dir, Err: session.DirReadError(err), Spoofed: spoofed}
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
		return fileListMsg{Dir: dir, Entries: entries, Capped: capped, Spoofed: spoofed}
	}
	return func() tea.Msg {
		msg := read()
		msg.Gen, msg.Quiet = gen, quiet
		return msg
	}
}

// spoofCheckApplies reports whether this client can honestly run the spoof
// check on a pane of the session it is attached to.
//
// A window's ShellPgid is filled from the daemon's WindowState.ShellPID for
// every pane, including one attached from another machine. On such a pane that
// number is a pid on that machine, and reading it here asks the local operating
// system about whatever process happens to hold the same number. On Linux that
// is usually nothing, which is harmless. On macOS, where the read goes through
// the process table rather than /proc, it can be an unrelated live process, and
// the answer is then a comparison between a remote shell's announced directory
// and some local program's working directory.
//
// A check like this is allowed to answer "no evidence"; cwdIsSpoofed is built
// around that and says so. It is not allowed to answer using a fact from the
// wrong computer. So the check is for panes this client's own daemon owns, and
// a session attached from another machine simply does not get it.
//
// The protection is not lost, it is misplaced: the daemon that owns the pane
// holds both the announcement and the kernel's answer, so that is where this
// belongs for a remote pane. Until it is asked for there, an attached session
// on another machine is treated as unverified rather than as verified-clean.
func spoofCheckApplies(attachedHost string) bool { return attachedHost == "" }

// cwdIsSpoofed reports whether /proc says the pane's shell is somewhere other
// than the folder the pane named over OSC 7.
//
// # Why this is asked at all
//
// OSC 7 is a string any program in a pane can print, and the rail's delete,
// rename and paste act on the folder it names. A program that prints one naming
// a folder it is not in steers a destructive action at that folder. The pane's
// shell has a working directory the kernel knows, and that is the one fact in
// this the pane cannot write.
//
// # Only a positive disagreement counts
//
// The answer is false whenever there is no evidence. There is no /proc on
// macOS, a pane whose PTY has gone has no process to read, and a daemon older
// than WindowState.ShellPID sends no pid at all. Treating any of those as a
// disagreement would take the file actions away from users who did nothing
// wrong. Unknown is unknown, and it leaves the feature exactly as it was.
//
// Where the number comes from is the difference between a feature and a
// decoration. A pane this client spawned records its own shell at spawn time;
// a daemon-backed pane, which is every pane in the default deployment, is sent
// its shell's pid by the daemon that holds the process (see
// session.WindowState.ShellPID). Before the daemon sent it this check had
// nothing to compare against on any shipped pane and passed every spoof.
//
// # How the two paths are compared
//
// Not as strings, past the first try. /proc hands back a path the kernel has
// already resolved; a shell prints $PWD, which keeps whatever symlink the user
// walked in through. "/home/g/work" and "/mnt/big/work" are then the same
// folder spelled two ways, and a string compare would call every such pane a
// liar. So a mismatch is confirmed by stat'ing both and comparing the device
// and inode, which is the only comparison that answers "the same directory"
// rather than "the same spelling", and which takes symlinks, bind mounts and a
// ".." through a symlink together.
//
// A folder that cannot be stat'ed is a disagreement, because /proc has already
// named a directory that exists and this is not it.
//
// # What it costs to be wrong
//
// A false positive is a read-only listing: the names, the navigation and the
// path copy all still work, and only the six file actions go. A file manager
// running in a pane and reporting its own browsing over OSC 7 is the one honest
// program this will refuse, and refusing it is the same answer as for the
// dishonest one, because from here they are the same event.
func cwdIsSpoofed(pgid int, dir string) bool {
	procDir, ok := terminal.ShellCWD(pgid)
	if !ok {
		return false
	}
	return !sameDir(procDir, dir)
}

// sameDir reports whether two paths name one directory.
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
			// A newer quiet read, or a read the user asked for, is in flight.
			return
		}
		if msg.Dir == v.Dir && msg.Err == v.Err && msg.Capped == v.Capped &&
			msg.Spoofed == v.Spoofed && slices.Equal(msg.Entries, v.Entries) {
			// The folder changed and changed back, or changed in a way the
			// listing does not show: nothing to draw again.
			return
		}
		// Gen is what the rail's cache sees of a new listing. Nothing else is
		// stamped with it now, so moving it drops no reply.
		v.Gen++
	}
	m.filesView.Loading = false
	m.filesView.Dir = msg.Dir
	m.filesView.Err = msg.Err
	m.filesView.Entries = msg.Entries
	m.filesView.Capped = msg.Capped
	m.filesView.Spoofed = msg.Spoofed
	// Stamped only on a failure, and only when it is a new one. FilesSyncCmd
	// paces the retry off this, so leaving it unset would make every retry
	// immediate and turn a directory that stays unreadable into a request per
	// message. Restamping an unchanged error would push the next retry out
	// forever, which is the opposite mistake.
	if msg.Err != "" {
		m.filesView.ErrAt = time.Now()
	} else {
		m.filesView.ErrAt = time.Time{}
	}
	m.syncFileWatch()
}

// recordWindowCwd stores a pane's reported directory.
//
// It is called from the cwd-change handler, which is driven by OSC 7 and so
// runs only when a shell actually changes directory. Nothing polls it. Whether
// the section follows the new directory is FilesSyncCmd's decision, made once
// per message against the focused pane, so this does not have to know anything
// about the rail.
//
// Only for a pane on this client's machine. The parse judges the host in the
// report against this machine's name, which is the wrong machine for a pane
// that runs on another: a report from the pane's own machine reads as a shell
// that went elsewhere and is dropped, and one that happens to share this
// machine's name is taken as a folder here. The daemon that runs such a pane
// judges the report against its own name and sends the answer; see
// takeDaemonCwd.
//
// A pane whose PTY this client holds itself has no daemon to judge a report
// that names another machine, so it is judged here: CwdHost is that machine
// until a report from this one, or the shell taking the terminal back, clears
// it (filesClearElsewhere).
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
	}
	if host == "" {
		w.Cwd = dir
		w.CwdAnnounced = true
	}
}

// filesClearElsewhere clears what an OSC 7 report from another machine left on
// a pane this client runs itself, once the pane's own shell holds the terminal
// again: the program that was on the other machine has ended. A daemon pane is
// left alone, because its daemon makes the same check and sends the answer.
func filesClearElsewhere(w *terminal.Window) {
	if w != nil && w.CwdHost != "" && w.Pty != nil && w.ShellAtPrompt() {
		w.CwdHost = ""
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
	dir := m.paneDir(window)
	if dir == "" {
		m.ShowNotification(
			"tuios cannot read that pane's directory.",
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.filesView.Show = 1
	return m.requestFileList(dir, window.ID, false)
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
	m.syncFileWatch()
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
	cmd := m.requestFileList(parent, m.filesView.Origin, true)
	// Land on the folder just left, the way a file manager does, so walking in
	// and back out returns the cursor to where it started.
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
			// The first row of the new listing, since nothing in it is the row
			// the cursor was on.
			m.followFileRow("")
		}
		if m.Settings.SidebarFolderClick != config.SidebarFolderClickNavigate {
			m.sendCdToOrigin(full)
		}
		return cmd
	}
	m.ShowNotification("Copied the path.", "success", m.Settings.NotificationDuration)
	return tea.SetClipboard(full)
}

// followFileRow asks the next nav build, once the listing being requested has
// arrived, to put the cursor on the named entry of the files section, or on the
// section's first row when the name is empty or not in the listing.
//
// Called after requestFileList, which has already bumped the generation.
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
	if _, ok := cdLine(dir); !ok {
		m.ShowNotification(cdRefusedMessage, "warning", m.Settings.NotificationDuration)
		return
	}
	// The foreground command a daemon pane reports can be empty or stale, so
	// it is not proof of a prompt. tuios types only where the shell is seen
	// to hold the terminal, the rule layout load uses.
	m.cdAtPrompt(window, dir, false, cdUnseenMessage)
}

// fileViewOriginWindow is the pane the listing is tied to, or nil.
func (m *OS) fileViewOriginWindow() *terminal.Window {
	if m.filesView.Origin == "" {
		return nil
	}
	return m.windowByID(m.filesView.Origin)
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
// The wire's ForegroundCmd is empty when the foreground process is the login
// shell, which is the prompt. A local PTY names the shell there instead, so
// its name is used only when the foreground group is not the shell's.
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
	// A local PTY's foreground command is the shell's own name at a prompt, so
	// it is only a reason once the foreground group is not the shell's.
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

// shellQuote wraps a path in single quotes for a POSIX shell, escaping any
// single quote in it the way a shell requires: close, escape, reopen.
//
// The path came off a filesystem, so it may hold a space, a quote, a newline or
// a semicolon, and the string is about to be typed at a prompt. Quoting is
// what keeps a directory called "; rm -rf ~" from being two commands.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cdRefusedMessage is what the dock says when cdLine refuses a folder.
const cdRefusedMessage = "tuios did not type a cd. The folder name holds a quote, a backslash or a control character."

// cdAtPrompt types a cd to dir into window only where its shell is seen at
// the prompt, and otherwise shows refused on the dock (nothing when it is
// empty). clear adds "&& clear" after it.
//
// A pane this client spawned is checked here, with the kernel. A daemon pane
// has no PTY on this side, so the daemon, which owns it, checks and writes in
// one step (session.TUIClient.CdAtPrompt), and builds the line itself. It
// fails closed: no daemon, a daemon too old to answer, or no answer at all
// types nothing. The daemon's answer arrives off the Update goroutine, so a
// refusal reaches the dock through the notification channel.
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
		// The error needs no report of its own: an older daemon and a
		// daemon that found no prompt both mean the same thing here.
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

// cdUnseenMessage is what the dock says when tuios cannot see that a pane's
// shell is at its prompt.
const cdUnseenMessage = "tuios did not type a cd. It can not see that the shell in that pane is at a prompt."

// cdLine is the cd command tuios types to move a shell to dir, or false when
// dir must not be typed at all.
//
// shellQuote is correct for a POSIX shell, and the shell in a pane is not
// always one. fish reads \' inside single quotes as an escaped quote, so the
// POSIX escape for a quote ends the quoting there, and the rest of the name
// runs as commands. No single quoting is right for every shell, so a folder
// whose name holds a quote or a backslash is not typed. Such names are rare,
// and the user can still cd there by hand.
//
// The rule lives in session.CdLine, which the daemon uses too when it types
// the cd into a daemon pane.
func cdLine(dir string) (string, bool) {
	return session.CdLine(dir)
}

// adoptWindowCwd takes the directory the daemon reports for a pane.
//
// A client learns a directory two ways on its own, and neither works for a pane
// attached from another machine. Its emulator sees one only when the shell
// announces over OSC 7, and most shells are not configured to announce. Reading
// the pane's process works only where the process is, and a pane on another
// machine is not on this computer at all. So the daemon's answer, which it fills
// from the process it owns, is the only one such a pane can have, and it is the
// better one everywhere.
//
// It is a fallback and not an override. A client that has seen OSC 7 on this
// pane already has the fresher answer: it parses the stream as it arrives, where
// the daemon's copy reaches it on the next state sync, so adopting over the top
// put a directory the pane had already left back on the window.
//
// That is not only stale, it is unsafe. cwdIsSpoofed earns its keep by comparing
// the folder a pane announced against the one the kernel reports for its shell,
// and a stale announcement disagrees with a current process for a pane that did
// nothing wrong. Overwriting here turned every cd into a spoof warning and took
// the file actions away with it.
func adoptWindowCwd(w *terminal.Window, cwd string) {
	if w != nil && cwd != "" && w.Cwd == "" {
		w.Cwd = cwd
	}
}

// takeDaemonCwd takes what the daemon reports about where a pane is.
//
// A pane whose shell this client has heard announce over OSC 7 gets
// adoptWindowCwd: the client parsed the report as it arrived, so its answer is
// at least as fresh as the daemon's, and the daemon's only fills a gap.
//
// Every other pane takes each new answer the daemon sends. A pane on another
// machine has nothing else: its reports name a host this client cannot judge,
// and its pid is a pid over there. Taking only the first answer is what kept
// the files list on the folder such a pane started in (issue #313). A shell
// that never announces is the same on this machine once the daemon has said
// where it is, because a directory in the window is preferred to reading the
// process. The same answer sent again is not taken, so a value the window
// holds is replaced only by a newer one and never by a copy of an older one.
//
// CwdHost is the daemon's alone, for every pane: only the daemon sees both the
// report and the process that holds the terminal.
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
