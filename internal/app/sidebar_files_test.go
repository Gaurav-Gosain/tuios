package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestFileViewOrdersFoldersFirst.
//
// Negative control: with the sort's IsDir clause removed, the order comes out
// Alpha.go, apple, beta.txt, README.md, Zeta and this fails; with
// strings.ToLower dropped, Zeta sorts before apple and it fails too. NOT YET
// CONFIRMED RED.
func TestFileViewOrdersFoldersFirst(t *testing.T) {
	m := &OS{}
	m.loadFileView(fileViewTree(t))

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
	m.filesView.Open = true
	m.loadFileView(root)

	// "apple" is the first entry, per the order pinned above.
	if cmd := m.FileViewEnter(0); cmd != nil {
		t.Error("walking into a folder produced a command; it should only move the listing")
	}
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Fatalf("after entering the folder the view is at %q, want %q", got, want)
	}

	m.FileViewUp()
	if got := m.FileViewDir(); got != root {
		t.Fatalf("after going up the view is at %q, want %q", got, root)
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
	m.filesView.Open = true
	m.loadFileView(string(filepath.Separator))
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
	m.loadFileView(filepath.Join(t.TempDir(), "not-there"))
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
// Negative control: with the alt-screen test removed from paneBusyReason the
// first case passes the guard, and with the ForegroundCmd test removed the
// second does. NOT YET CONFIRMED RED.
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
	m.filesView.Open = true
	m.loadFileView(t.TempDir())
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

// TestFileViewFollowsThePaneItWasOpenedFrom, and only while it is still showing
// that pane's own directory: a user who has walked somewhere else is not dragged
// back by a cd in the terminal.
func TestFileViewFollowsThePaneItWasOpenedFrom(t *testing.T) {
	root := fileViewTree(t)
	sub := filepath.Join(root, "apple")

	win := &terminal.Window{ID: "aaaaaaaa1111", Cwd: root}
	m := &OS{Windows: []*terminal.Window{win}}
	m.filesView.Open = true
	m.filesView.Origin = win.ID
	m.loadFileView(root)

	m.recordWindowCwd(win.ID, "file://"+sub)
	if got := m.FileViewDir(); got != sub {
		t.Errorf("the view did not follow the pane: at %q, want %q", got, sub)
	}
	if win.Cwd != sub {
		t.Errorf("the window's own cwd was not recorded: %q", win.Cwd)
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
