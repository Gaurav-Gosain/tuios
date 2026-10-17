package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// paneRow is one window of list-windows --json, with the workspace it is on.
type paneRow struct {
	ID        string `json:"window_id"`
	Workspace int    `json:"workspace"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

func listPanes(t *testing.T, base, session string) map[string]paneRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Windows []paneRow `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse list-windows: %v\n%s", err, out)
	}
	rows := make(map[string]paneRow, len(payload.Windows))
	for _, w := range payload.Windows {
		rows[w.ID] = w
	}
	return rows
}

// waitPane waits until ok holds for pane id, and fails with what it last saw.
func waitPane(t *testing.T, base, session, id, what string, ok func(paneRow) bool) paneRow {
	t.Helper()
	var last paneRow
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		last = listPanes(t, base, session)[id]
		if ok(last) {
			return last
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s: pane %s is %+v", what, id, last)
	return last
}

func panesSession(t *testing.T, name, config string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n"+config)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", name, err, out)
	}
	term := attachIn(t, base, name, startOpts{cols: 120, rows: 40})
	clientShows(t, base, name)
	return term, base
}

// TestMarkAndJoinPane marks the pane that sits right of a, focuses a, and joins
// the marked pane to a's left: the pane ends up left of a with both in the same
// row. Three panes in BSP are a | b over c, and c is the marked one.
//
// The two halves around it: join_marked_left before any mark moves nothing,
// and the pane really starts right of a, so the move is what the test sees.
//
// Negative control: delete the d.Register("mark_pane", ...) call, or the
// d.Register("join_marked_"+name, ...) call, in
// internal/input/prefix_tree_actions.go. With no mark_pane the "Pane marked"
// notification never shows; with no join the marked pane stays right of a and
// the wait for it to sit left of a times out.
func TestMarkAndJoinPane(t *testing.T) {
	term, base := panesSession(t, "joiner", `
[keybindings.workspaces]
mark_pane = ["alt+m"]
join_marked_left = ["alt+u"]
`)
	a := waitForSettledGeometryIn(t, base, "joiner", 1)[0].ID
	newWindowIn(t, base, "joiner")
	c := newWindowIn(t, base, "joiner")
	waitForSettledGeometryIn(t, base, "joiner", 3)

	// c is right of a before anything is pressed.
	rows := listPanes(t, base, "joiner")
	if rows[c].X < rows[a].X+rows[a].Width {
		t.Fatalf("setup: c %+v is not right of a %+v", rows[c], rows[a])
	}

	// Positive half's counterpart: with nothing marked, joining moves nothing.
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "joiner", a); err != nil {
		t.Fatalf("focus a: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('u')); err != nil {
		t.Fatalf("press alt+u: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := listPanes(t, base, "joiner")[c]; got.X != rows[c].X || got.Y != rows[c].Y {
		t.Fatalf("join_marked_left with no mark moved c from %+v to %+v", rows[c], got)
	}

	// Mark c, go back to a, join.
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "joiner", c); err != nil {
		t.Fatalf("focus c: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('m')); err != nil {
		t.Fatalf("press alt+m: %v", err)
	}
	if err := term.WaitForText("Pane marked", uiTimeout); err != nil {
		t.Fatalf("mark_pane showed no notification: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "joiner", a); err != nil {
		t.Fatalf("focus a: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('u')); err != nil {
		t.Fatalf("press alt+u: %v", err)
	}
	waitPane(t, base, "joiner", c, "c should end up left of a, beside it", func(p paneRow) bool {
		ar := listPanes(t, base, "joiner")[a]
		return p.X+p.Width <= ar.X && p.Y < ar.Y+ar.Height && ar.Y < p.Y+p.Height
	})
	waitMarkBadges(t, term, 0, "the join takes the mark off c")
}

// TestJoinMarkedPaneFromAnotherWorkspace marks a pane that lives on workspace 2
// and joins it right of the focused pane on workspace 1: the pane comes back to
// workspace 1 and sits beside a. This is the cross-workspace half of join-pane,
// where the pane leaves one workspace's layout for another's.
//
// The positive half is the setup: the pane really is on workspace 2 before the
// join, so its being on workspace 1 afterwards is the join.
//
// Negative control: delete the `m.MoveWindowToWorkspace(markedIdx, ...)` call in
// JoinMarkedPane (internal/app/prefix_tree.go). The pane stays on workspace 2.
func TestJoinMarkedPaneFromAnotherWorkspace(t *testing.T) {
	term, base := panesSession(t, "crossjoin", `
[keybindings.workspaces]
mark_pane = ["alt+m"]
join_marked_right = ["alt+u"]
send_to_workspace_2 = ["alt+i"]
`)
	a := waitForSettledGeometryIn(t, base, "crossjoin", 1)[0].ID
	b := newWindowIn(t, base, "crossjoin")
	waitForSettledGeometryIn(t, base, "crossjoin", 2)

	// b is focused, so it is the one sent to workspace 2.
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "crossjoin", b); err != nil {
		t.Fatalf("focus b: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('i')); err != nil {
		t.Fatalf("press alt+i: %v", err)
	}
	waitPane(t, base, "crossjoin", b, "b is sent to workspace 2", func(p paneRow) bool { return p.Workspace == 2 })

	// On workspace 2 b is the only pane: mark it, and come back.
	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	waitWorkspace(t, base, "crossjoin", 2)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "crossjoin", b); err != nil {
		t.Fatalf("focus b on workspace 2: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('m')); err != nil {
		t.Fatalf("press alt+m: %v", err)
	}
	if err := term.WaitForText("Pane marked", uiTimeout); err != nil {
		t.Fatalf("mark_pane showed no notification: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1: %v", err)
	}
	waitWorkspace(t, base, "crossjoin", 1)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "crossjoin", a); err != nil {
		t.Fatalf("focus a: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Alt('u')); err != nil {
		t.Fatalf("press alt+u: %v", err)
	}
	waitPane(t, base, "crossjoin", b, "b joins right of a on workspace 1", func(p paneRow) bool {
		ar := listPanes(t, base, "crossjoin")[a]
		return p.Workspace == 1 && p.X >= ar.X+ar.Width && p.Y < ar.Y+ar.Height && ar.Y < p.Y+p.Height
	})
}

// TestMoveWorkspaceAndSendPane binds move_workspace_right and
// send_to_workspace_3 and presses them: the pane's workspace follows the swap
// with workspace 2 and the view follows it, then a send puts the pane on
// workspace 3 while the view stays where it is. Workspace 2 holds a pane of its
// own: a workspace's number is its position, so the only workspace there is has
// nothing to swap with.
//
// Negative control: delete the Register("move_workspace_right", ...) line, or
// the send_to_workspace_ loop, in internal/input/prefix_tree_actions.go. The
// pane stays on its workspace and the wait for the new one times out.
func TestMoveWorkspaceAndSendPane(t *testing.T) {
	term, base := panesSession(t, "mover", `
[keybindings.workspaces]
move_workspace_right = ["alt+o"]
send_to_workspace_3 = ["alt+i"]
`)
	pane := waitForSettledGeometryIn(t, base, "mover", 1)[0].ID
	if ws := listPanes(t, base, "mover")[pane].Workspace; ws != 1 {
		t.Fatalf("setup: the pane is on workspace %d, want 1", ws)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "mover", "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("setup: a pane on workspace 2: %v\n%s", err, out)
	}

	if err := term.SendKeys(tuitest.Alt('o')); err != nil {
		t.Fatalf("press alt+o: %v", err)
	}
	waitPane(t, base, "mover", pane, "workspace 1 swapped with 2 takes its pane along", func(p paneRow) bool { return p.Workspace == 2 })
	waitWorkspace(t, base, "mover", 2)

	if err := term.SendKeys(tuitest.Alt('i')); err != nil {
		t.Fatalf("press alt+i: %v", err)
	}
	waitPane(t, base, "mover", pane, "send_to_workspace_3 moves the pane to 3", func(p paneRow) bool { return p.Workspace == 3 })
	if info := sessionInfoOf(t, base, "mover"); info.CurrentWorkspace != 2 {
		t.Errorf("the view moved to workspace %d with the pane, want it to stay on 2", info.CurrentWorkspace)
	}
}

// markBadge is the text the title bar of the marked pane carries.
const markBadge = "[marked]"

// waitMarkBadges waits until the screen shows the mark badge n times.
func waitMarkBadges(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), markBadge) == n
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: want %d %q on the screen, have %d: %v\n%s",
			what, n, markBadge, strings.Count(term.Screen().Text(), markBadge), err, term.Snapshot())
	}
}

// TestMarkedPaneShowsABadge: the marked pane says so in its title bar, so the
// mark can be seen before a join uses it. The badge is on one pane at a time:
// marking another pane moves it, marking the marked pane again takes it off,
// and closing the marked pane takes it with it.
//
// The positive half is the first wait, no badge before any mark; the badge
// counts are exact, so a badge left behind on the old pane fails them.
//
// Negative control: delete the `if marked {` block that appends markedPaneBadge
// in windowBorderRows (internal/app/render_helpers.go): the badge never shows.
// Replace setMarkedPane's InvalidateCache loop with nothing: the badge shows
// late or stays on the pane that lost the mark (the count of 1 fails).
func TestMarkedPaneShowsABadge(t *testing.T) {
	term, base := panesSession(t, "badge", `
[keybindings.workspaces]
mark_pane = ["alt+m"]
`)
	a := waitForSettledGeometryIn(t, base, "badge", 1)[0].ID
	b := newWindowIn(t, base, "badge")
	waitForSettledGeometryIn(t, base, "badge", 2)
	focus := func(id string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "badge", id); err != nil {
			t.Fatalf("focus %s: %v\n%s", id, err, out)
		}
	}
	mark := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Alt('m')); err != nil {
			t.Fatalf("press alt+m: %v", err)
		}
	}
	waitMarkBadges(t, term, 0, "before any mark")

	focus(b)
	mark()
	waitMarkBadges(t, term, 1, "b marked")
	mark()
	waitMarkBadges(t, term, 0, "b unmarked by marking it again")

	mark()
	waitMarkBadges(t, term, 1, "b marked again")
	focus(a)
	mark()
	// The mark moved: one badge, not two.
	waitMarkBadges(t, term, 1, "the mark moved to a")

	// Close the marked pane: its badge goes with it.
	if out, err := tuiosCLI(t, base, "close-window", "-s", "badge", a); err != nil {
		t.Fatalf("close a: %v\n%s", err, out)
	}
	waitMarkBadges(t, term, 0, "the marked pane is closed")
}
