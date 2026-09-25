package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The files section says what is in the focused pane's directory. When there is
// no focused pane it has nothing to say, and these are the claims about what it
// does then. They drive the real sequence: a pane reports a directory, the sync
// reads it, the shell exits, and the rail is looked at afterwards.

// pumpFiles runs one message through Update and applies the directory read it
// asks for, if it asks for one.
//
// It goes through Update rather than calling FilesSyncCmd, because the whole
// bug is about which message the sync sees and what it does with it. A test
// that called the sync by hand would be testing the function and not the loop
// that calls it. The reply is fed back through Update too, so the generation
// guard in the real handler is the one that decides.
func pumpFiles(t *testing.T, m *OS, msg tea.Msg) {
	t.Helper()
	gen := m.filesView.Gen
	_, cmd := m.Update(msg)
	// A read was scheduled only if the sync stamped a new generation for it.
	// Asking the state rather than the command tree keeps this from having to
	// run every command Update batched, which is how the loop itself decides
	// nothing: bubbletea runs them, and some of them block on a channel.
	if m.filesView.Gen == gen || !m.filesView.Loading {
		return
	}
	reply, ok := awaitFileList(cmd)
	if !ok {
		t.Fatalf("the section asked for %q and nothing came back", m.filesView.Want)
	}
	m.Update(reply)
}

// awaitFileList runs a command tree the way the program does, each command on
// its own goroutine, and returns the directory listing one of them produces.
//
// Running them in line does not work: Update batches the sync with a tick that
// sleeps and with a listener that blocks on a channel, so the first call would
// never return. This is what bubbletea does with the same batch.
func awaitFileList(cmd tea.Cmd) (fileListMsg, bool) {
	out := make(chan fileListMsg, 8)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case fileListMsg:
				out <- msg
			case tea.BatchMsg:
				for _, sub := range msg {
					run(sub)
				}
			}
		}()
	}
	run(cmd)
	select {
	case msg := <-out:
		return msg, true
	case <-time.After(30 * time.Second):
		return fileListMsg{}, false
	}
}

// filesOSWithCwd is a rail with three panes, the focused one reporting dir, and
// the files section already listing it. It is the state every test below starts
// from and the state a user is in when they close their last pane.
func filesOSWithCwd(t *testing.T, dir string) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	// A closed exit channel so the listener the exit handler re-arms answers at
	// once instead of parking a goroutine for the length of the run.
	exits := make(chan string)
	close(exits)
	m.WindowExitChan = exits
	m.Windows[0].Cwd = dir
	pumpFiles(t, m, TickerMsg(time.Now()))
	if m.FileViewDir() != dir {
		t.Fatalf("the section never listed the focused pane's directory: Dir=%q", m.FileViewDir())
	}
	return m
}

// closeEveryPane exits every pane's shell, one WindowExitMsg at a time, which
// is the message the client gets when a shell ends on its own.
func closeEveryPane(t *testing.T, m *OS) {
	t.Helper()
	for range 16 {
		if len(m.Windows) == 0 {
			return
		}
		pumpFiles(t, m, WindowExitMsg{WindowID: m.Windows[0].ID})
	}
	t.Fatalf("%d panes are still open after sixteen exits", len(m.Windows))
}

// TestFilesSectionDropsALateReplyAfterTheLastPane is the in-flight case.
//
// A read can be outstanding when the last pane closes: the shell cds, the sync
// asks for the new directory, and the shell exits before the answer comes back.
// The reply must be dropped, or it refills a section that has nothing to be
// about.
//
// Negative control, confirmed red: take the generation bump out of
// clearFileView and the late reply is accepted, leaving the section holding the
// dead pane's directory with its five names in it.
func TestFilesSectionDropsALateReplyAfterTheLastPane(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOSWithCwd(t, dir)

	// The pane cds. The sync asks for the new directory and the answer is held
	// back, so the read is in flight for the rest of this test.
	other := fileViewTree(t)
	m.Windows[0].Cwd = other
	gen := m.filesView.Gen
	_, cmd := m.Update(TickerMsg(time.Now()))
	if m.filesView.Gen == gen {
		t.Fatal("the pane changed directory and the section asked for nothing")
	}

	closeEveryPane(t, m)

	// And now the read finally answers.
	reply, ok := awaitFileList(cmd)
	if !ok {
		t.Fatal("the outstanding read produced no listing")
	}
	if reply.Dir != other {
		t.Fatalf("the outstanding read was for %q, want %q", reply.Dir, other)
	}
	m.Update(reply)

	if m.FileViewDir() != "" {
		t.Errorf("a late reply refilled the section with %q", m.FileViewDir())
	}
	if len(m.filesView.Entries) != 0 {
		t.Errorf("a late reply put %d names back into a section with no pane", len(m.filesView.Entries))
	}
	if m.FileActionsOn() {
		t.Error("a late reply switched file actions back on with no pane to act for")
	}
	lines := strings.Join(railLines(t, m), "\n")
	if strings.Contains(lines, filepath.Base(other)) || strings.Contains(lines, "README.md") {
		t.Errorf("a late reply drew the section again:\n%s", lines)
	}
}

// TestNoPaneSyncCostsNothingPerMessage.
//
// FilesSyncCmd runs once per message from Update, so the no-pane state is a
// state a client can sit in for hours. Clearing is a write, and a write on
// every message would invalidate the rail's render cache on every message and
// put the rail's whole rebuild back on the idle path. It happens once.
//
// Negative control, confirmed red: drop the `m.filesView.Want != ""` guard in
// FilesSyncCmd and the generation climbs by one per message forever.
func TestNoPaneSyncCostsNothingPerMessage(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOSWithCwd(t, dir)
	closeEveryPane(t, m)

	gen := m.filesView.Gen
	sig := m.sidebarSignature()
	for range 50 {
		if cmd := m.FilesSyncCmd(); cmd != nil {
			t.Fatal("the sync asked for a read with no pane to read for")
		}
	}
	if m.filesView.Gen != gen {
		t.Errorf("the generation climbed %d times over 50 idle messages", m.filesView.Gen-gen)
	}
	if m.sidebarSignature() != sig {
		t.Error("an idle message with no pane changed the rail's render signature")
	}
}
