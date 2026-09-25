package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

	m := &OS{Settings: config.Global}
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
