package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const wsCloseSession = "wc"

func startTwoWorkspaces(t *testing.T, base string, far int) *tuitest.Terminal {
	t.Helper()
	term := startHome(t, base, wsCloseSession)
	for range far {
		if out, err := tuiosCLI(t, base, "new-window", "--session", wsCloseSession, "--workspace", "2", "--no-focus"); err != nil {
			t.Fatalf("new-window on 2: %v\n%s", err, out)
		}
	}
	waitPanesOn(t, term, base, wsCloseSession, 2, far, "the panes on workspace 2")
	for ws, name := range map[string]string{"1": "homews", "2": "farws"} {
		if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", wsCloseSession, ws, name); err != nil {
			t.Fatalf("name workspace %s: %v\n%s", ws, err, out)
		}
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return dockCellOK(s, "homews") && dockCellOK(s, "farws") && strings.Contains(s.Text(), "Window management mode")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never showed both named tabs: %v\n%s", err, term.Snapshot())
	}
	if ws := currentWorkspace(t, base, wsCloseSession); ws != 1 {
		t.Fatalf("the session shows workspace %d, want 1", ws)
	}
	return term
}

func dockCell(s tuitest.Screen, needle string) (col, row int, ok bool) {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		line := s.Line(r)
		if b := strings.Index(line, needle); b >= 0 {
			return len([]rune(line[:b])), r, true
		}
	}
	return 0, 0, false
}

func dockCellOK(s tuitest.Screen, needle string) bool {
	_, _, ok := dockCell(s, needle)
	return ok
}

func screenCell(t *testing.T, term *tuitest.Terminal, needle string) (col, row int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := 0; r < rows-1; r++ {
		line := s.Line(r)
		if b := strings.Index(line, needle); b >= 0 {
			return len([]rune(line[:b])), r
		}
	}
	t.Fatalf("%q is not on screen\n%s", needle, term.Snapshot())
	return 0, 0
}

func openTabClose(t *testing.T, term *tuitest.Terminal, tab, question string) {
	t.Helper()
	col, row, ok := dockCell(term.Screen(), tab)
	if !ok {
		t.Fatalf("no dock tab %q\n%s", tab, term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseRight, 0)
	waitMenu(t, term, "the "+tab+" tab", "Switch to", "Rename", "Close")
	col, row = screenCell(t, term, "Close")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText(question, uiTimeout); err != nil {
		t.Fatalf("the confirmation did not ask %q: %v\n%s", question, err, term.Snapshot())
	}
}

func workspaceNamesOf(t *testing.T, base, sess string) map[string]string {
	t.Helper()
	info, err := daemonJSON[struct {
		WorkspaceNames map[string]string `json:"workspace_names"`
	}](base, "session-info", "-s", sess)
	if err != nil {
		t.Fatalf("session-info: %v", err)
	}
	return info.WorkspaceNames
}

func confirmClose(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	col, row := screenCell(t, term, "Close workspace")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
}

// TestWorkspaceTabCloseClosesThatTabsWorkspace is the feature: the client is on
// workspace 1, and the menu of workspace 2's tab closes workspace 2's panes and
// nothing else. Cancel, the default answer, closes nothing first.
func TestWorkspaceTabCloseClosesThatTabsWorkspace(t *testing.T) {
	base := t.TempDir()
	term := startTwoWorkspaces(t, base, 2)
	const question = "Close every pane on workspace 2 (farws)?"

	openTabClose(t, term, "farws", question)
	text := term.Screen().Text()
	if !strings.Contains(text, "2 panes") || strings.Contains(text, "1 pane,") {
		t.Fatalf("the dialog does not count workspace 2's two panes\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("answer the dialog: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), question)
	}, uiTimeout); err != nil {
		t.Fatalf("Cancel did not dismiss the dialog: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	if n1, n2 := panesOn(t, base, wsCloseSession, 1), panesOn(t, base, wsCloseSession, 2); n1 != 1 || n2 != 2 {
		t.Fatalf("Cancel closed panes: workspace 1 has %d, workspace 2 has %d, want 1 and 2\n%s", n1, n2, term.Snapshot())
	}

	openTabClose(t, term, "farws", question)
	confirmClose(t, term)
	waitPanesOn(t, term, base, wsCloseSession, 2, 0, "closing workspace 2 from its tab")
	if n := panesOn(t, base, wsCloseSession, 1); n != 1 {
		t.Fatalf("closing workspace 2 from its tab left workspace 1 with %d panes, want 1\n%s", n, term.Snapshot())
	}
	waitShowing(t, base, wsCloseSession, 1, "HOME-42", term)
	alive(t, term, "after closing workspace 2 from its tab")
}

// TestWorkspaceTabCloseOnTheCurrentTab closes the workspace in view from its
// own tab, which is what close_workspace from the palette does.
func TestWorkspaceTabCloseOnTheCurrentTab(t *testing.T) {
	base := t.TempDir()
	term := startTwoWorkspaces(t, base, 1)

	openTabClose(t, term, "homews", "Close every pane on workspace 1 (homews)?")
	confirmClose(t, term)
	waitPanesOn(t, term, base, wsCloseSession, 2, 0, "closing workspace 1 from its tab")
	if n := panesOn(t, base, wsCloseSession, 1); n != 1 {
		t.Fatalf("closing workspace 1 from its tab left workspace 1 with %d panes, want farws's 1\n%s", n, term.Snapshot())
	}
	if names := workspaceNamesOf(t, base, wsCloseSession); names["1"] != "farws" || len(names) != 1 {
		t.Fatalf("closing workspace 1 from its tab left the names %v, want farws on 1 alone", names)
	}
	alive(t, term, "after closing workspace 1 from its tab")
}

// TestCloseWorkspaceFromThePaletteClosesTheCurrentOne is close_workspace with
// no menu behind it: the palette row closes the workspace in view, not the one
// whose tab was last right-clicked.
func TestCloseWorkspaceFromThePaletteClosesTheCurrentOne(t *testing.T) {
	base := t.TempDir()
	term := startTwoWorkspaces(t, base, 1)

	col, row, ok := dockCell(term.Screen(), "farws")
	if !ok {
		t.Fatalf("no dock tab farws\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseRight, 0)
	waitMenu(t, term, "the farws tab", "Switch to", "Close")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("dismiss the menu: %v", err)
	}
	waitMenuGone(t, term, "Switch to", "the dismissed tab menu")

	runPaletteRow(t, term, "close works", "Close workspace", "Close every pane on workspace 1 (homews)?")
	confirmClose(t, term)
	waitPanesOn(t, term, base, wsCloseSession, 2, 0, "closing workspace 1 from the palette")
	if n := panesOn(t, base, wsCloseSession, 1); n != 1 {
		t.Fatalf("closing workspace 1 from the palette left workspace 1 with %d panes, want farws's 1\n%s", n, term.Snapshot())
	}
	if names := workspaceNamesOf(t, base, wsCloseSession); names["1"] != "farws" || len(names) != 1 {
		t.Fatalf("closing workspace 1 from the palette left the names %v, want farws on 1 alone", names)
	}
	alive(t, term, "after closing workspace 1 from the palette")
}

// TestCloseWorkspaceFromAKeyClosesTheCurrentOne is close_workspace bound the
// way docs/KEYBINDINGS.md shows it, Ctrl+B Alt+X: the same action as the tab's
// Close row, and with no menu behind it, the workspace in view.
func TestCloseWorkspaceFromAKeyClosesTheCurrentOne(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.prefix_mode]\nclose_workspace = [\"alt+x\"]\n")
	term := startTwoWorkspaces(t, base, 1)

	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Alt("x")); err != nil {
		t.Fatalf("press ctrl+b alt+x: %v", err)
	}
	if err := term.WaitForText("Close every pane on workspace 1 (homews)?", uiTimeout); err != nil {
		t.Fatalf("ctrl+b alt+x did not ask to close workspace 1: %v\n%s", err, term.Snapshot())
	}
	confirmClose(t, term)
	waitPanesOn(t, term, base, wsCloseSession, 2, 0, "closing workspace 1 from the key")
	if n := panesOn(t, base, wsCloseSession, 1); n != 1 {
		t.Fatalf("closing workspace 1 from the key left workspace 1 with %d panes, want farws's 1\n%s", n, term.Snapshot())
	}
	if names := workspaceNamesOf(t, base, wsCloseSession); names["1"] != "farws" || len(names) != 1 {
		t.Fatalf("closing workspace 1 from the key left the names %v, want farws on 1 alone", names)
	}
	alive(t, term, "after closing workspace 1 from the key")
}
