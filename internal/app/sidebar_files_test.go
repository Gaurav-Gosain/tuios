package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// fileViewTree builds a directory with a known shape: two folders and three
// files, deliberately named so that a plain lexical sort and a folders-first
// sort disagree, and so that case-insensitive and byte order disagree too.
//
//	Zeta/      README.md
//	apple/     beta.txt
//	           Alpha.go
//
// Folders first, then each group case-insensitively, is:
//
//	apple, Zeta, Alpha.go, beta.txt, README.md
//
// which is written out below and is not derived from the code under test.
func fileViewTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"Zeta", "apple"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"README.md", "beta.txt", "Alpha.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var wantFileOrder = []string{"apple", "Zeta", "Alpha.go", "beta.txt", "README.md"}

// loadFileViewNow reads dir into the files section and waits for the answer.
//
// It goes through the real command and the real handler rather than filling the
// model in: the read runs on its own goroutine in the app, so a test that wrote
// the entries directly would prove nothing about the path that actually runs,
// including the generation guard that decides whether a reply is applied at all.
func (m *OS) loadFileViewNow(t *testing.T, dir string) {
	t.Helper()
	m.filesView.Show = 1
	cmd := m.requestFileList(dir, m.filesView.Origin, true)
	if cmd == nil {
		t.Fatalf("no read was scheduled for %q", dir)
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
}

// TestFileViewOrdersFoldersFirst.
//
// Negative control, both confirmed red: with the sort's IsDir clause removed
// the order comes out Alpha.go, apple, beta.txt, README.md, Zeta; with
// strings.ToLower dropped, Zeta sorts before apple.
func TestFileViewOrdersFoldersFirst(t *testing.T) {
	m := &OS{}
	m.loadFileViewNow(t, fileViewTree(t))

	if m.filesView.Err != "" {
		t.Fatalf("reading the tree failed: %s", m.filesView.Err)
	}
	got := make([]string, len(m.filesView.Entries))
	for i, e := range m.filesView.Entries {
		got[i] = e.Name
	}
	if strings.Join(got, ",") != strings.Join(wantFileOrder, ",") {
		t.Errorf("order = %v, want %v", got, wantFileOrder)
	}
	for i, e := range m.filesView.Entries {
		wantDir := i < 2
		if e.Dir != wantDir {
			t.Errorf("%s reported Dir=%v, want %v", e.Name, e.Dir, wantDir)
		}
	}
}

// TestFileViewNavigatesWithoutTouchingAnyPane is the safe half of "clicking a
// folder navigates to it": the listing moves and nothing is typed anywhere.
//
// Negative control: with FileViewEnter sending a cd for a directory instead of
// reloading, the directory assertion fails and the pane below receives input.
func TestFileViewNavigatesWithoutTouchingAnyPane(t *testing.T) {
	root := fileViewTree(t)
	m := &OS{}
	m.loadFileViewNow(t, root)

	// A pane that would notice being typed into. Nothing here may reach it.
	win, typedInto := cdProbe(t, "aaaaaaaa1111")
	m.Windows = []*terminal.Window{win}
	m.filesView.Origin = win.ID

	// "apple" is the first entry, per the order pinned above. Walking into it
	// answers with the command that reads the folder, and with nothing else.
	cmd := m.FileViewEnter(0)
	if cmd == nil {
		t.Fatal("walking into a folder scheduled no read")
	}
	m.HandleFileList(cmd().(fileListMsg))
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Fatalf("after entering the folder the view is at %q, want %q", got, want)
	}
	if typed := typedInto(); typed != "" {
		t.Errorf("walking into a folder typed %q into the pane", typed)
	}

	if cmd := m.FileViewUp(); cmd != nil {
		m.HandleFileList(cmd().(fileListMsg))
	}
	if got := m.FileViewDir(); got != root {
		t.Fatalf("after going up the view is at %q, want %q", got, root)
	}
	if typed := typedInto(); typed != "" {
		t.Errorf("walking out of a folder typed %q into the pane", typed)
	}

	// A file answers with a clipboard write and leaves the listing where it is.
	// "Alpha.go" is index 2.
	before := m.FileViewDir()
	if cmd := m.FileViewEnter(2); cmd == nil {
		t.Error("a file produced no clipboard write")
	}
	if m.FileViewDir() != before {
		t.Errorf("clicking a file moved the listing to %q", m.FileViewDir())
	}
}

// TestFileViewUpStopsAtTheRoot: the parent of "/" is "/", so walking up there
// must not reload forever or blank the view.
func TestFileViewUpStopsAtTheRoot(t *testing.T) {
	m := &OS{}
	m.loadFileViewNow(t, string(filepath.Separator))
	gen := m.filesView.Gen
	m.FileViewUp()
	if m.filesView.Gen != gen {
		t.Error("going up from the root re-read the directory")
	}
	if m.FileViewDir() != string(filepath.Separator) {
		t.Errorf("the view left the root: %q", m.FileViewDir())
	}
}

// TestUnreadableDirectoryIsReported: a listing that failed says so instead of
// showing an empty folder, which is a different and misleading fact.
func TestUnreadableDirectoryIsReported(t *testing.T) {
	m := &OS{}
	m.loadFileViewNow(t, filepath.Join(t.TempDir(), "not-there"))
	if m.filesView.Err == "" {
		t.Fatal("a missing directory reported no error")
	}
	if len(m.filesView.Entries) != 0 {
		t.Errorf("a failed read left %d entries behind", len(m.filesView.Entries))
	}
}

// TestPaneBusyReasonRefusesWhenSomethingIsRunning is the guard on the one action
// that types into somebody else's program.
//
// What is on the other end of a pane is not known to be a shell. "cd /x\r" typed
// into vim is a series of editing commands and into a REPL a syntax error, so
// the pane has to be at a prompt and tuios has to be able to see that it is.
//
// Negative control, both confirmed red: with the alt-screen test removed from
// paneBusyReason the first case passes the guard, and with the ForegroundCmd
// test removed the second does.
func TestPaneBusyReasonRefusesWhenSomethingIsRunning(t *testing.T) {
	win := newTestWindow(t, "aaaaaaaa1111", 40, 10)

	if why, ok := paneBusyReason(win); !ok {
		t.Fatalf("an idle pane was refused: %s", why)
	}

	// A full-screen program. The emulator knows this on every platform, which
	// is why it is the first test.
	win.WriteOutput([]byte("\x1b[?1049h"))
	if !win.Terminal.IsAltScreen() {
		t.Fatal("the emulator did not record the alternate screen")
	}
	why, ok := paneBusyReason(win)
	if ok {
		t.Error("a pane on the alternate screen passed the guard")
	}
	if !strings.Contains(why, "full-screen") {
		t.Errorf("the refusal does not say why: %q", why)
	}
	win.WriteOutput([]byte("\x1b[?1049l"))

	// The daemon's own observation of the foreground process, which is what an
	// attached, SSH or web client has instead of a local PTY to ask.
	win.ForegroundCmd = "nvim"
	why, ok = paneBusyReason(win)
	if ok {
		t.Error("a pane running nvim passed the guard")
	}
	if !strings.Contains(why, "nvim") {
		t.Errorf("the refusal does not name the program: %q", why)
	}
}

// TestFileViewCdRefusesWithoutAnOrigin: a view opened from a folder link is not
// tied to any pane, so there is no pane the cd could mean.
func TestFileViewCdRefusesWithoutAnOrigin(t *testing.T) {
	m := &OS{}
	m.loadFileViewNow(t, t.TempDir())
	m.FileViewCd()
	if len(m.Notifications) == 0 {
		t.Error("a cd with no origin pane said nothing")
	}
}

// TestShellQuoteSurvivesAHostileName. The path came off a filesystem and is
// about to be typed at a prompt, so a folder named "; rm -rf ~" must arrive as
// one argument.
//
// The expected strings are written out by hand from the POSIX rule (close the
// quote, escape, reopen), not produced by the function under test.
func TestShellQuoteSurvivesAHostileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/plain", "'/tmp/plain'"},
		{"/tmp/with space", "'/tmp/with space'"},
		{"/tmp/; rm -rf ~", "'/tmp/; rm -rf ~'"},
		{"/tmp/it's", `'/tmp/it'\''s'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// runSync drives the one comparison Update makes after every message: does the
// files section still agree with the focused pane's directory. It runs the
// command it produces and applies the reply, which is what the loop does one
// message later.
func (m *OS) runSync(t *testing.T) bool {
	t.Helper()
	cmd := m.FilesSyncCmd()
	if cmd == nil {
		return false
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the sync read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
	return true
}

// TestFilesSectionFollowsTheFocusedPane. The section is not a mode any more, so
// it has no gesture that says which directory it is about: it follows whatever
// pane has the focus, and a cd in that pane moves it.
//
// Negative control, confirmed red: return the focused window's ID rather than
// its Cwd from filesWantDir, so the section asks for a window id as a path, and
// the listing lands on an unreadable directory instead of the pane's.
func TestFilesSectionFollowsTheFocusedPane(t *testing.T) {
	root := fileViewTree(t)
	sub := filepath.Join(root, "apple")

	m := sidebarTestOS(t, 120, 40, "left")
	m.filesView.Show = 1
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0

	if !m.runSync(t) {
		t.Fatal("the section asked for nothing with a focused pane in a known directory")
	}
	if got := m.FileViewDir(); got != root {
		t.Fatalf("the section is at %q, want the pane's own directory %q", got, root)
	}
	if m.runSync(t) {
		t.Error("the section asked for the same directory twice")
	}

	// The pane cds. The next message the loop sees carries the section with it.
	m.onCwdChange(CwdChangedMsg{WindowID: m.Windows[0].ID, Cwd: "file://" + sub})
	if !m.runSync(t) {
		t.Fatal("a cd in the focused pane did not move the section")
	}
	if got := m.FileViewDir(); got != sub {
		t.Errorf("the section did not follow the pane: at %q, want %q", got, sub)
	}
	if m.Windows[0].Cwd != sub {
		t.Errorf("the window's own cwd was not recorded: %q", m.Windows[0].Cwd)
	}

	// And the focus moving to a pane in another directory takes it there.
	m.Windows[1].Cwd = root
	m.FocusedWindow = 1
	if !m.runSync(t) {
		t.Fatal("moving the focus to a pane elsewhere did not move the section")
	}
	if got := m.FileViewDir(); got != root {
		t.Errorf("after the focus moved the section is at %q, want %q", got, root)
	}
}

// TestFilesSectionDoesNotDragTheUserBack is the other half of the rule above,
// and the half that is easy to get wrong: once the user has steered the listing
// somewhere of their own, a cd in the terminal must leave it there. The listing
// is then answering a question they asked and the pane's directory is not.
//
// Negative controls, both confirmed red: drop the Pinned clause from
// filesWantDir, and the cd drags the listing back to the pane's directory; drop
// the Origin comparison beside it, and the listing stays pinned after the focus
// has moved to a pane the user never steered.
func TestFilesSectionDoesNotDragTheUserBack(t *testing.T) {
	root := fileViewTree(t)
	sub := filepath.Join(root, "apple")
	elsewhere := filepath.Join(root, "Zeta")

	m := sidebarTestOS(t, 120, 40, "left")
	m.filesView.Show = 1
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0
	m.runSync(t)

	// The user walks the listing into Zeta. The pane is still in root.
	if cmd := m.requestFileList(elsewhere, m.filesView.Origin, true); cmd != nil {
		m.HandleFileList(cmd().(fileListMsg))
	}

	// The pane now cds to apple. The listing must stay where the user put it.
	m.onCwdChange(CwdChangedMsg{WindowID: m.Windows[0].ID, Cwd: "file://" + sub})
	if m.runSync(t) {
		t.Error("a cd in the pane moved a listing the user had steered")
	}
	if got := m.FileViewDir(); got != elsewhere {
		t.Errorf("a cd in the pane dragged the listing to %q; it should have stayed at %q", got, elsewhere)
	}
	if m.Windows[0].Cwd != sub {
		t.Errorf("the pane's own cwd was not recorded: %q", m.Windows[0].Cwd)
	}

	// The pin is about one pane. Focusing another drops it, because the listing
	// is then about a pane the user has steered nothing in.
	m.Windows[1].Cwd = root
	m.FocusedWindow = 1
	if !m.runSync(t) {
		t.Fatal("the pin outlived the pane it was made in")
	}
	if got := m.FileViewDir(); got != root {
		t.Errorf("after the focus moved the section is at %q, want %q", got, root)
	}
}

// TestFileReadRunsOffTheUpdateGoroutine is the claim this whole design rests on:
// a directory that never answers must not hold the client.
//
// The reader is replaced with one that blocks on a channel, which is what a
// hung NFS or sshfs mount does to a real one. Update is then driven with the
// same messages the loop sees, and it has to keep returning, keep drawing, and
// keep the "loading" row up rather than the wrong listing.
//
// Negative control, confirmed red: call readDirFunc from requestFileList itself
// instead of from inside the returned command, which is what the section did
// while it was a mode, and Update never returns.
func TestFileReadRunsOffTheUpdateGoroutine(t *testing.T) {
	root := fileViewTree(t)

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	restore := readDirFunc
	readDirFunc = func(dir string, limit int) ([]os.DirEntry, bool, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return restore(dir, limit)
	}
	t.Cleanup(func() {
		readDirFunc = restore
		close(release)
	})

	m := sidebarTestOS(t, 120, 40, "left")
	m.filesView.Show = 1
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0

	cmd := m.FilesSyncCmd()
	if cmd == nil {
		t.Fatal("the section scheduled no read")
	}
	go func() {
		if msg := cmd(); msg != nil {
			// The reply is thrown away: this test is about the loop staying
			// alive while the read is stuck, not about what comes back.
			_ = msg
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the read never started")
	}

	// The read is now stuck in the kernel. Everything the loop does next has to
	// come back anyway.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			m.Update(TickerMsg(time.Now()))
		}
		railLines(t, m)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the client stopped answering while a directory read was stuck")
	}

	// And the section says a read is running rather than showing a listing it
	// does not have.
	if out := strings.Join(railLines(t, m), "\n"); !strings.Contains(out, "loading") {
		t.Errorf("a section waiting on a stuck read does not say so:\n%s", out)
	}
}

// TestCwdIsRecordedWithTapeAutorunOff. The cwd handler's own gates are the tape
// detector's and they are narrow on purpose; folding the recording into them is
// how the file view would have come out empty for anyone with autorun off.
//
// Negative control: move recordWindowCwd below the tapeAutorunEnabled gate in
// onCwdChange and this fails.
func TestCwdIsRecordedWithTapeAutorunOff(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tape.Autorun = config.TapeAutorunOff

	win := &terminal.Window{ID: "aaaaaaaa1111"}
	m := &OS{Windows: []*terminal.Window{win}, UserConfig: cfg}
	m.onCwdChange(CwdChangedMsg{WindowID: win.ID, Cwd: "file:///tmp"})

	if win.Cwd != "/tmp" {
		t.Errorf("with tape autorun off the pane's cwd was not recorded: %q", win.Cwd)
	}
}

// TestALargeDirectoryIsBounded. A listing is a syscall loop on the goroutine
// that also runs Update, and a build tree or a maildir holds six figures of
// names. The read stops at a fixed number of them, and the rail says that it
// did rather than counting rows that were never read.
//
// The directory built here is one name past the cap, which is the smallest tree
// that tells a bounded read from an unbounded one.
//
// Negative control, confirmed red: with readDirCapped replaced by os.ReadDir,
// every name is read and both assertions fail.
func TestALargeDirectoryIsBounded(t *testing.T) {
	dir := t.TempDir()
	for i := range fileViewMaxEntries + 1 {
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := &OS{}
	m.loadFileViewNow(t, dir)
	if m.filesView.Err != "" {
		t.Fatalf("reading the tree failed: %s", m.filesView.Err)
	}
	if got := len(m.filesView.Entries); got != fileViewMaxEntries {
		t.Errorf("read %d names from a directory of %d, want the cap of %d",
			got, fileViewMaxEntries+1, fileViewMaxEntries)
	}
	if !m.filesView.Capped {
		t.Error("a listing that was cut short did not say so")
	}

	// And a directory under the cap is not marked, so the note only ever
	// appears where it is true.
	m.loadFileViewNow(t, fileViewTree(t))
	if m.filesView.Capped {
		t.Error("a five-name directory reported itself as cut short")
	}
	if got := len(m.filesView.Entries); got != len(wantFileOrder) {
		t.Errorf("read %d names from the small tree, want %d", got, len(wantFileOrder))
	}
}
