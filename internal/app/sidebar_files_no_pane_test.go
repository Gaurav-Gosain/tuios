package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// TestFilesSectionEmptiesWhenTheLastPaneCloses is the reported bug.
//
// The section followed the focused pane's directory, the pane went away, and
// the listing stayed on the rail naming a folder that belonged to nothing on
// screen.
//
// Negative control, confirmed red: restore the `want == ""` arm of the early
// return in FilesSyncCmd, and with no panes left the rail still draws the
// folder name and all five of its entries.
func TestFilesSectionEmptiesWhenTheLastPaneCloses(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOSWithCwd(t, dir)

	// The header names the folder by its last component, and the rows name what
	// is in it. The word "files" itself is not checked either way: the rail's
	// footer carries a control with that label whether the section is drawn or
	// not.
	here := filepath.Base(dir)
	before := strings.Join(railLines(t, m), "\n")
	for _, want := range []string{here, "apple/", "README.md"} {
		if !strings.Contains(before, want) {
			t.Fatalf("the rail is not listing the pane's directory to begin with, no %q:\n%s", want, before)
		}
	}
	t.Logf("with a pane:\n%s", before)

	closeEveryPane(t, m)

	after := strings.Join(railLines(t, m), "\n")
	t.Logf("after the last pane closed:\n%s", after)
	// "loading" is in the list because a half cleared state draws it forever:
	// a Want with no Dir behind it is the section saying an answer is coming
	// for a read nobody asked for.
	for _, gone := range []string{here, "apple/", "Zeta/", "README.md", "Alpha.go", "beta.txt", "loading"} {
		if strings.Contains(after, gone) {
			t.Errorf("with no pane left the rail still draws %q:\n%s", gone, after)
		}
	}
	if m.FileViewDir() != "" {
		t.Errorf("the section still holds %q with no pane to hold it for", m.FileViewDir())
	}
	if m.FileActionsOn() {
		t.Error("file actions are still live on a section that is listing nothing")
	}
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

// TestClosingTheLastPaneLeavesTheSwitchAlone.
//
// Show is the user's own on and off control for the section, not part of the
// listing. Closing a pane clears what is being listed; it must not decide
// whether the section is on, in either direction.
//
// Negative control, confirmed red on the "switched on" case: clear the whole
// state with `m.filesView = fileViewState{Gen: gen}` and a section the user
// forced on drops back to zero, which means "follow the layout", so it goes off
// for anyone whose layout does not name files.
//
// The "switched off" case passes under that control too, and it should: a
// section switched off is not enabled, so the sync returns before it reaches
// the clear at all. It is here because the claim is about the field and not
// about one path to it.
func TestClosingTheLastPaneLeavesTheSwitchAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		show int8
		want int8
	}{
		{"switched off by the user", -1, -1},
		{"switched on by the user", 1, 1},
		{"following the layout", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fileViewTree(t)
			m := filesOSWithCwd(t, dir)
			m.filesView.Show = tc.show

			closeEveryPane(t, m)

			if m.filesView.Show != tc.want {
				t.Errorf("the section's switch went from %d to %d when the last pane closed",
					tc.show, m.filesView.Show)
			}
		})
	}
}

// TestFilesSectionEmptiesOnAPaneThatWillNotSayWhereItIs is the sibling state: a
// pane is focused, but its shell never reported a directory, so there is still
// nothing to list. It takes the same path as no pane at all, because the
// section's answer is the same either way.
//
// Negative control, confirmed red: restore the `want == ""` arm and focusing
// the silent pane leaves the first pane's listing on the rail, labelled as if
// it were the focused pane's folder.
func TestFilesSectionEmptiesOnAPaneThatWillNotSayWhereItIs(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOSWithCwd(t, dir)

	// The second pane runs something that does not report its directory.
	m.Windows[1].Cwd = ""
	m.FocusedWindow = 1
	pumpFiles(t, m, TickerMsg(time.Now()))

	lines := strings.Join(railLines(t, m), "\n")
	if strings.Contains(lines, "README.md") {
		t.Errorf("the section is showing another pane's folder as if it were this one's:\n%s", lines)
	}
	if m.FileViewDir() != "" {
		t.Errorf("the section holds %q for a pane that never said where it is", m.FileViewDir())
	}

	// And back to the pane that does report one: the section fills again.
	m.FocusedWindow = 0
	pumpFiles(t, m, TickerMsg(time.Now()))
	if m.FileViewDir() != dir {
		t.Errorf("returning to the pane that reports a directory left the section on %q", m.FileViewDir())
	}
}

// TestClosingAPinnedPaneReleasesThePin.
//
// A pinned listing is tied to one pane by Origin. When that pane is the one
// that closes there is nothing left to honour the pin against, so it is
// released: with another pane still on screen the section follows that pane,
// and with none left it empties.
//
// Negative control, confirmed red: carry Want, Origin and Pinned across the
// clear and the section holds the dead pane's folder after every pane has gone.
// The same control turns the main test red too, on a "loading" row that never
// finishes: a Want with the directory behind it cleared is the section saying
// an answer is coming for a read nobody asked for.
func TestClosingAPinnedPaneReleasesThePin(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOSWithCwd(t, dir)

	// The user steers the listing into a subfolder of the focused pane's
	// directory, which is what pins it.
	sub := filepath.Join(dir, "apple")
	cmd := m.requestFileList(sub, m.Windows[0].ID, true)
	reply, ok := awaitFileList(cmd)
	if !ok {
		t.Fatal("pinning the listing to a subfolder read nothing")
	}
	m.Update(reply)
	if !m.filesView.Pinned || m.filesView.Origin != m.Windows[0].ID {
		t.Fatalf("the listing did not pin to the focused pane: pinned=%v origin=%q",
			m.filesView.Pinned, m.filesView.Origin)
	}

	// The pinned pane's shell exits. Two panes are left, and the section is
	// about the focused one of those now.
	other := fileViewTree(t)
	m.Windows[1].Cwd = other
	pumpFiles(t, m, WindowExitMsg{WindowID: m.Windows[0].ID})
	if m.filesView.Pinned {
		t.Error("the listing is still pinned to a pane that has gone")
	}
	if m.FileViewDir() != other {
		t.Errorf("the section is on %q, want the surviving focused pane's %q", m.FileViewDir(), other)
	}

	// And when the rest go, it empties like any other listing.
	closeEveryPane(t, m)
	if m.FileViewDir() != "" || m.filesView.Want != "" {
		t.Errorf("the section held on to %q after every pane closed", m.filesView.Want)
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

// TestALinkedListingSurvivesWithNoPane.
//
// A listing opened from a directory link is pinned with no origin window. It
// names a folder the user asked for by hand, so it is the one listing that is
// not about a pane and the one that having no pane is not a reason to drop.
//
// Negative control, confirmed red: take the fileViewFromLink guard out of
// FilesSyncCmd and the section empties on the next message, because the panes
// in this fixture never report a directory. Six tests in internal/input fail
// with it as well, all of them on a link-opened section.
func TestALinkedListingSurvivesWithNoPane(t *testing.T) {
	dir := fileViewTree(t)
	m := sidebarTestOS(t, 120, 40, "left")
	exits := make(chan string)
	close(exits)
	m.WindowExitChan = exits

	if !m.OpenFileView(dir) {
		t.Fatal("the rail refused to open a link's folder")
	}
	reply, ok := awaitFileList(m.TakeSidebarCmd())
	if !ok {
		t.Fatal("opening a link's folder read nothing")
	}
	m.Update(reply)

	closeEveryPane(t, m)

	if m.FileViewDir() != dir {
		t.Errorf("closing every pane took away a folder the user opened from a link: Dir=%q",
			m.FileViewDir())
	}
	if !strings.Contains(strings.Join(railLines(t, m), "\n"), "README.md") {
		t.Error("the rail stopped drawing a link's listing when the last pane closed")
	}

	// And a pane that opens afterwards and says where it is takes the section
	// back, the same way any other pane does.
	m.Windows = append(m.Windows, &terminal.Window{
		ID: "dddddddd4444", CustomName: "new", Width: 40, Height: 20,
		Workspace: m.CurrentWorkspace, Cwd: t.TempDir(),
	})
	m.FocusedWindow = 0
	pumpFiles(t, m, TickerMsg(time.Now()))
	if m.FileViewDir() == dir {
		t.Error("a new pane reporting a directory did not take the section back from the link")
	}
}
