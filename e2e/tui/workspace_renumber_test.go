package tuie2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A workspace's number is its position. Moving one, by the move keys or by
// dragging its dock pill, renumbers it: the pill's {index}, opt+N, the daemon's
// numbering and a second client all follow the place it was moved to. A herdr
// tab id is the one thing that does not follow the position: it names the tab,
// so an id read before a move names the same tab after it.
//
// The session under test holds three workspaces with different pane counts, so
// the dock's "<workspace>:<count>" status says which one is on screen without
// reading a name: EDIT holds one pane, REVW two and DPLY three.

const renumberSession = "renum"

// renumberConfig shows the number on every pill, the way the report's config
// does, and binds the move keys under the workspace prefix.
const renumberConfig = `[appearance]
dock_workspace_tab_format = '{index}·{name}'

[keybindings.workspace_prefix]
move_workspace_left = ['<']
move_workspace_right = ['>']
`

// renumberPanes is how many panes each named workspace holds.
var renumberPanes = map[string]int{"EDIT": 1, "REVW": 2, "DPLY": 3}

// renumberSetup builds the three workspaces through the daemon and attaches a
// client to workspace 1.
func renumberSetup(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, renumberConfig)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renumberSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	for _, ws := range []struct {
		num, name string
	}{{"1", "EDIT"}, {"2", "REVW"}, {"3", "DPLY"}} {
		have := 0
		if ws.num == "1" {
			have = 1
		}
		for i := have; i < renumberPanes[ws.name]; i++ {
			if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", ws.num, "--no-focus"); err != nil {
				t.Fatalf("new-window on %s: %v: %s", ws.num, err, out)
			}
		}
		if out, err := tuiosCLI(t, base, "set-workspace-name", ws.num, ws.name, "--session", renumberSession); err != nil {
			t.Fatalf("name workspace %s: %v: %s", ws.num, err, out)
		}
	}
	term := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY")
	return term, base
}

// waitPills waits for the dock to draw every one of the labels.
func waitPills(t *testing.T, term *tuitest.Terminal, labels ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, labels...)
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never drew %v: %v\n%s", labels, err, term.Snapshot())
	}
}

// renumberInfo is what session-info says about the numbering.
type renumberInfo struct {
	CurrentWorkspace int               `json:"current_workspace"`
	WorkspaceNames   map[string]string `json:"workspace_names"`
	WorkspaceOrder   []int             `json:"workspace_order"`
}

// wantNumbering checks the daemon's numbering: the name each number holds, and
// that no display order is left over beside it.
func wantNumbering(t *testing.T, base string, want map[string]string) renumberInfo {
	t.Helper()
	info, err := daemonJSON[renumberInfo](base, "session-info", "-s", renumberSession)
	if err != nil {
		t.Fatalf("session-info: %v", err)
	}
	for num, name := range want {
		if info.WorkspaceNames[num] != name {
			t.Fatalf("the daemon has workspace %s named %q, want %q (names %v)", num, info.WorkspaceNames[num], name, info.WorkspaceNames)
		}
	}
	if len(info.WorkspaceOrder) > 0 {
		t.Fatalf("the daemon kept a display order %v beside the numbering", info.WorkspaceOrder)
	}
	return info
}

// wantPanesOn checks how many panes the daemon holds on each named workspace,
// so a renumber is seen to have moved the panes and not only the names.
func wantPanesOn(t *testing.T, base string, want map[int]string) {
	t.Helper()
	out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var payload struct {
		WorkspaceWindows []int `json:"workspace_windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	got := payload.WorkspaceWindows
	for num, name := range want {
		if num > len(got) || got[num-1] != renumberPanes[name] {
			t.Fatalf("workspace %d holds the wrong panes, want %s's %d (by workspace %v)", num, name, renumberPanes[name], got)
		}
	}
}

// altReaches presses opt+N and checks the workspace that comes up is the one
// called name, by its pane count.
func altReaches(t *testing.T, term *tuitest.Terminal, n rune, name string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(n)); err != nil {
		t.Fatalf("press alt+%c: %v", n, err)
	}
	status := fmt.Sprintf("%c:%d", n, renumberPanes[name])
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == status
	}, uiTimeout); err != nil {
		t.Fatalf("alt+%c did not reach %s (%d panes): %v\n%s", n, name, renumberPanes[name], err, term.Snapshot())
	}
}

// renumberDockStatus is the dock's "<workspace>:<count>" status field, read the way
// countWindows reads it, or nil when the dock is not on screen.
func renumberDockStatus(s tuitest.Screen) []string {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		if m := dockStatus.FindStringSubmatch(s.Line(r)); m != nil {
			return m
		}
	}
	return nil
}

// moveWorkspaceKey presses the bound move key under the workspace prefix.
func moveWorkspaceKey(t *testing.T, term *tuitest.Terminal, key string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", key); err != nil {
		t.Fatalf("press the move key %s: %v", key, err)
	}
}

// dragPill drags the pill labelled from onto the pill labelled onto.
func dragPill(t *testing.T, term *tuitest.Terminal, from, onto string, all ...string) {
	t.Helper()
	row, line := dockPillRow(t, term, all...)
	mouseDrag(t, term, pillColumn(line, from)+1, row, pillColumn(line, onto)+1, row, tuitest.MouseLeft, 0)
}

// herdrTabIDByLabel is the herdr tab id of the tab labelled label.
func herdrTabIDByLabel(t *testing.T, base, label string) string {
	t.Helper()
	tabs := herdrCall(t, base, "tab.list", map[string]any{})["tabs"].([]any)
	for _, raw := range tabs {
		if tab := raw.(map[string]any); tab["label"] == label {
			return tab["tab_id"].(string)
		}
	}
	t.Fatalf("no herdr tab labelled %s in %v", label, tabs)
	return ""
}

// wantHerdrTab checks that a tab id still names the tab labelled label.
func wantHerdrTab(t *testing.T, base, id, label string) {
	t.Helper()
	tab := herdrCall(t, base, "tab.get", map[string]any{"tab_id": id})["tab"].(map[string]any)
	if tab["label"] != label {
		t.Fatalf("herdr tab %s names %v after the move, want %s", id, tab["label"], label)
	}
}

// TestWorkspaceKeyMoveRenumbers is the move key: moving EDIT right swaps it
// with REVW, the pills read 1·REVW 2·EDIT, opt+N reaches each one by its new
// number, and the herdr tab ids read before the move still name the same tabs.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceKeyMoveRenumbers(t *testing.T) {
	term, base := renumberSetup(t)
	editID := herdrTabIDByLabel(t, base, "EDIT")
	revwID := herdrTabIDByLabel(t, base, "REVW")

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	info := wantNumbering(t, base, map[string]string{"1": "REVW", "2": "EDIT", "3": "DPLY"})
	if info.CurrentWorkspace != 2 {
		t.Fatalf("the moved workspace is not followed: current is %d, want 2", info.CurrentWorkspace)
	}
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "EDIT", 3: "DPLY"})
	wantHerdrTab(t, base, editID, "EDIT")
	wantHerdrTab(t, base, revwID, "REVW")

	altReaches(t, term, '1', "REVW")
	altReaches(t, term, '2', "EDIT")
	altReaches(t, term, '3', "DPLY")

	// And back, from the other end: DPLY to the left swaps it with EDIT.
	moveWorkspaceKey(t, term, "<")
	waitPills(t, term, "1·REVW", "2·DPLY", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "REVW", "2": "DPLY", "3": "EDIT"})
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "DPLY", 3: "EDIT"})
	altReaches(t, term, '3', "EDIT")
	wantHerdrTab(t, base, editID, "EDIT")
	alive(t, term, "after moving workspaces by key")
}

// TestWorkspaceDragLeftRenumbers drags DPLY's pill onto EDIT's, the first one:
// DPLY becomes workspace 1 and the two it passed move up one.
func TestWorkspaceDragLeftRenumbers(t *testing.T) {
	term, base := renumberSetup(t)
	dpltID := herdrTabIDByLabel(t, base, "DPLY")

	dragPill(t, term, "3·DPLY", "1·EDIT", "1·EDIT", "2·REVW", "3·DPLY")
	waitPills(t, term, "1·DPLY", "2·EDIT", "3·REVW")
	wantNumbering(t, base, map[string]string{"1": "DPLY", "2": "EDIT", "3": "REVW"})
	wantPanesOn(t, base, map[int]string{1: "DPLY", 2: "EDIT", 3: "REVW"})
	wantHerdrTab(t, base, dpltID, "DPLY")

	altReaches(t, term, '1', "DPLY")
	altReaches(t, term, '2', "EDIT")
	altReaches(t, term, '3', "REVW")
	alive(t, term, "after dragging a pill left")
}

// TestWorkspaceDragThenKeyMove drags first and moves by key second: the key
// moves the workspace past the neighbour drawn beside it.
func TestWorkspaceDragThenKeyMove(t *testing.T) {
	term, base := renumberSetup(t)

	dragPill(t, term, "3·DPLY", "1·EDIT", "1·EDIT", "2·REVW", "3·DPLY")
	waitPills(t, term, "1·DPLY", "2·EDIT", "3·REVW")

	// EDIT is on screen, now as workspace 2, and the pill left of it is DPLY's.
	altReaches(t, term, '2', "EDIT")
	moveWorkspaceKey(t, term, "<")
	waitPills(t, term, "1·EDIT", "2·DPLY", "3·REVW")
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY", "3": "REVW"})
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "DPLY", 3: "REVW"})
	altReaches(t, term, '2', "DPLY")
	altReaches(t, term, '1', "EDIT")
	alive(t, term, "after a drag and a key move")
}

// TestWorkspaceRenumberReachesEveryClient moves by key and by drag with a
// second client attached, then detaches and attaches again: both clients, and
// the client that comes back, draw the numbering the daemon holds.
func TestWorkspaceRenumberReachesEveryClient(t *testing.T) {
	term, base := renumberSetup(t)
	second := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, second, "1·EDIT", "2·REVW", "3·DPLY")

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	waitPills(t, second, "1·REVW", "2·EDIT", "3·DPLY")

	dragPill(t, second, "3·DPLY", "1·REVW", "1·REVW", "2·EDIT", "3·DPLY")
	waitPills(t, second, "1·DPLY", "2·REVW", "3·EDIT")
	waitPills(t, term, "1·DPLY", "2·REVW", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "DPLY", "2": "REVW", "3": "EDIT"})
	altReaches(t, second, '1', "DPLY")
	altReaches(t, term, '3', "EDIT")

	// The first client goes away and a new one comes back to the session.
	term.Close()
	again := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, again, "1·DPLY", "2·REVW", "3·EDIT")
	wantPanesOn(t, base, map[int]string{1: "DPLY", 2: "REVW", 3: "EDIT"})
	altReaches(t, again, '2', "REVW")
	alive(t, again, "after attaching again")
	alive(t, second, "beside the client that came back")
}

// TestHerdrTabMoveKeepsTabIDs moves a tab through herdr's tab.move: the tab
// list comes back in the new order with the ids it had, the tuios numbering
// follows the move, and a tab id read before the move still names its tab.
func TestHerdrTabMoveKeepsTabIDs(t *testing.T) {
	term, base := renumberSetup(t)
	ids := map[string]string{}
	for name := range renumberPanes {
		ids[name] = herdrTabIDByLabel(t, base, name)
	}

	res := herdrCall(t, base, "tab.move", map[string]any{"tab_id": ids["EDIT"], "insert_index": 3})
	var order []string
	for _, raw := range res["tabs"].([]any) {
		tab := raw.(map[string]any)
		if want := ids[tab["label"].(string)]; tab["tab_id"] != want {
			t.Fatalf("tab %v came back as %v, want the id it had, %s", tab["label"], tab["tab_id"], want)
		}
		order = append(order, tab["label"].(string))
	}
	if strings.Join(order, " ") != "REVW DPLY EDIT" {
		t.Fatalf("tab.move left the tabs in order %v, want REVW DPLY EDIT", order)
	}
	waitPills(t, term, "1·REVW", "2·DPLY", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "REVW", "2": "DPLY", "3": "EDIT"})
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "DPLY", 3: "EDIT"})
	for name, id := range ids {
		wantHerdrTab(t, base, id, name)
	}
	if _, err := tuiosCLI(t, base, "list-windows", "-s", renumberSession); err != nil {
		t.Fatalf("list-windows after tab.move: %v", err)
	}
	alive(t, term, "after herdr's tab.move")
}

// TestWorkspaceMoveKeyTwiceQuickly presses the move key twice in one burst.
// The second press reads the numbering from before the first move lands, and
// sent as it stands it would swap the same two workspaces back. EDIT must end
// up moved, never back on workspace 1, with the pills and the daemon agreeing.
func TestWorkspaceMoveKeyTwiceQuickly(t *testing.T) {
	term, base := renumberSetup(t)
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", ">", tuitest.Ctrl('b'), "w", ">"); err != nil {
		t.Fatalf("press the move key twice: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !screenHas(s, "1·EDIT") && strings.Contains(s.Text(), "·EDIT")
	}, uiTimeout); err != nil {
		t.Fatalf("EDIT never left workspace 1: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	info, err := daemonJSON[renumberInfo](base, "session-info", "-s", renumberSession)
	if err != nil {
		t.Fatalf("session-info: %v", err)
	}
	at := 0
	for num, name := range info.WorkspaceNames {
		if name == "EDIT" {
			fmt.Sscan(num, &at)
		}
	}
	if at != 2 && at != 3 {
		t.Fatalf("EDIT is workspace %d after two moves right, want 2 (second press dropped) or 3 (both taken): names %v", at, info.WorkspaceNames)
	}
	if !screenHas(term.Screen(), fmt.Sprintf("%d·EDIT", at)) {
		t.Fatalf("the daemon has EDIT on %d and the dock does not\n%s", at, term.Snapshot())
	}
	alive(t, term, "after two quick moves")
}

// TestWorkspaceCloseRenumbersTheRest closes a workspace in the middle, from its
// dock tab, and one at the start, through the close-workspace verb: each time
// the workspaces above it move down one, so the numbers stay 1 to the count, the
// pills and opt+N agree with the daemon, and a herdr tab id read before the
// close still names the same tab.
func TestWorkspaceCloseRenumbersTheRest(t *testing.T) {
	term, base := renumberSetup(t)
	dplyID := herdrTabIDByLabel(t, base, "DPLY")

	openTabClose(t, term, "2·REVW", "Close every pane on workspace 2 (REVW)?")
	confirmClose(t, term)
	waitPills(t, term, "1·EDIT", "2·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "3·") && !screenHas(s, "REVW") }, uiTimeout); err != nil {
		t.Fatalf("the closed workspace or its old number is still on the dock: %v\n%s", err, term.Snapshot())
	}
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY"})
	if len(info.WorkspaceNames) != 2 {
		t.Fatalf("names after the close are %v, want EDIT and DPLY only", info.WorkspaceNames)
	}
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "DPLY"})
	wantHerdrTab(t, base, dplyID, "DPLY")
	altReaches(t, term, '2', "DPLY")
	altReaches(t, term, '1', "EDIT")

	// The verb, on the workspace that is not in view: EDIT goes, DPLY is 1.
	altReaches(t, term, '2', "DPLY")
	if out, err := tuiosCLI(t, base, "close-workspace", "1", "-s", renumberSession); err != nil {
		t.Fatalf("close-workspace 1: %v: %s", err, out)
	}
	waitPills(t, term, "1·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "2·") && !screenHas(s, "EDIT") }, uiTimeout); err != nil {
		t.Fatalf("the dock still shows a second workspace: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "DPLY"})
	wantPanesOn(t, base, map[int]string{1: "DPLY"})
	wantHerdrTab(t, base, dplyID, "DPLY")
	alive(t, term, "after closing workspaces")
}

// TestWorkspaceSwitchPastTheLastOpensTheNext presses opt+5 with three
// workspaces: it opens workspace 4, a pane made there stays on 4, and an empty
// workspace left behind goes away without moving the others.
func TestWorkspaceSwitchPastTheLastOpensTheNext(t *testing.T) {
	term, base := renumberSetup(t)

	if err := term.SendKeys(tuitest.Alt('5')); err != nil {
		t.Fatalf("press alt+5: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "4:0"
	}, uiTimeout); err != nil {
		t.Fatalf("opt+5 with three workspaces did not open workspace 4: %v\n%s", err, term.Snapshot())
	}
	newWindow(t, term)
	out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var payload struct {
		WorkspaceWindows []int `json:"workspace_windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.WorkspaceWindows) < 5 {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	if got := payload.WorkspaceWindows; got[3] != 1 || got[4] != 0 {
		t.Fatalf("panes by workspace are %v, want the new pane on 4 and none on 5", got)
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})

	// An empty workspace that is left stops existing, and nothing moves.
	if err := term.SendKeys(tuitest.Alt('7')); err != nil {
		t.Fatalf("press alt+7: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "5:0"
	}, uiTimeout); err != nil {
		t.Fatalf("opt+7 with four workspaces did not open workspace 5: %v\n%s", err, term.Snapshot())
	}
	altReaches(t, term, '1', "EDIT")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "5·") }, uiTimeout); err != nil {
		t.Fatalf("the empty workspace 5 stayed on the dock after it was left: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})
	alive(t, term, "after opening workspaces past the last")
}

// TestWorkspaceSwitchFromAnEmptyOneStaysOnIt presses opt+5 with three
// workspaces, which opens workspace 4, and then opt+6 from that empty 4: the
// empty workspace on screen is the new one, so the switch stays on 4 rather
// than opening 5 and leaving the dock at 1, 2, 3, 5.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceSwitchFromAnEmptyOneStaysOnIt(t *testing.T) {
	term, base := renumberSetup(t)

	for _, key := range []rune{'5', '6'} {
		if err := term.SendKeys(tuitest.Alt(key)); err != nil {
			t.Fatalf("press alt+%c: %v", key, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			m := renumberDockStatus(s)
			return m != nil && m[0] == "4:0"
		}, uiTimeout); err != nil {
			t.Fatalf("alt+%c did not show workspace 4: %v\n%s", key, err, term.Snapshot())
		}
	}
	// The status read 4:0 before opt+6 too, so the switch is given time to
	// land before the dock is read again.
	time.Sleep(time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	if m := renumberDockStatus(term.Screen()); m == nil || m[0] != "4:0" || screenHas(term.Screen(), "5·") {
		t.Fatalf("opt+6 from the empty workspace 4 left it, want it to stay on 4 (status %v)\n%s", m, term.Snapshot())
	}
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})
	if info.CurrentWorkspace != 4 {
		t.Fatalf("the daemon shows workspace %d after opt+6 from the empty 4, want 4", info.CurrentWorkspace)
	}

	newWindow(t, term)
	out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var payload struct {
		WorkspaceWindows []int `json:"workspace_windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.WorkspaceWindows) < 4 {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	if got := payload.WorkspaceWindows; got[3] != 1 || (len(got) > 4 && got[4] != 0) {
		t.Fatalf("panes by workspace are %v, want the new pane on 4 and none past it", got)
	}
	alive(t, term, "after switching from an empty workspace")
}

// TestWorkspaceSwitchFromTheSplashStaysOnIt presses opt+2 in a session with
// no pane, then n: the splash of workspace 1 is the empty workspace on screen,
// so the switch stays on 1 and the new pane opens there. A pane opened on 2
// would never close up into 1.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceSwitchFromTheSplashStaysOnIt(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, renumberConfig)
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", renumberSession}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	// Keys are handled in order, so the pane n opens shows where opt+2 left
	// the session.
	newWindow(t, term)
	if ws := currentWorkspace(t, base, renumberSession); ws != 1 {
		t.Fatalf("opt+2 from the splash went to workspace %d, want it to stay on 1\n%s", ws, term.Snapshot())
	}
	if n := panesOn(t, base, renumberSession, 1); n != 1 {
		t.Fatalf("workspace 1 holds %d panes after opt+2 and n from the splash, want 1\n%s", n, term.Snapshot())
	}
	alive(t, term, "after opt+2 from the splash")
}

// renumberExitSetup is renumberSetup with REVW's two panes running a command
// that exits once release is created, one of them with close_on_exit: the
// daemon closes that one, and the attached client closes the other when its
// process ends. The client is left on DPLY, workspace 3.
func renumberExitSetup(t *testing.T) (term *tuitest.Terminal, base, release string) {
	t.Helper()
	base = t.TempDir()
	release = filepath.Join(base, "release")
	writeConfig(t, base, renumberConfig)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renumberSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	verbs := dialVerbs(t, filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock"))
	wait := []string{"/bin/sh", "-c", fmt.Sprintf("while [ ! -e %q ]; do sleep 0.1; done", release)}
	for _, closeOnExit := range []bool{true, false} {
		if _, verr := verbs.call("new-window", map[string]any{
			"session": renumberSession, "workspace": 2, "focus": false,
			"command": wait, "close_on_exit": closeOnExit,
		}); verr != "" {
			t.Fatalf("new-window on 2 (close_on_exit %v): %s", closeOnExit, verr)
		}
	}
	for range renumberPanes["DPLY"] {
		if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", "3", "--no-focus"); err != nil {
			t.Fatalf("new-window on 3: %v: %s", err, out)
		}
	}
	for num, name := range map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"} {
		if out, err := tuiosCLI(t, base, "set-workspace-name", num, name, "--session", renumberSession); err != nil {
			t.Fatalf("name workspace %s: %v: %s", num, err, out)
		}
	}
	term = attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY")
	altReaches(t, term, '3', "DPLY")
	return term, base, release
}

// subscribeLines streams `tuios subscribe` with args into a buffer until the
// test ends. It returns once the subscription is acknowledged, so nothing that
// happens after it can be missed, and read returns the event lines so far.
func subscribeLines(t *testing.T, base string, args ...string) (read func() []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pinPreV080Looks(t, base)
	cmd := exec.CommandContext(ctx, tuiosBin, append([]string{"subscribe"}, args...)...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	lines := bufio.NewScanner(pipe)
	if !lines.Scan() || !strings.Contains(lines.Text(), "subscribed") {
		t.Fatalf("subscribe %v did not acknowledge: %q", args, lines.Text())
	}
	var mu sync.Mutex
	var got []string
	go func() {
		for lines.Scan() {
			mu.Lock()
			got = append(got, lines.Text())
			mu.Unlock()
		}
	}()
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// waitPaneCount waits until the daemon holds n panes in the session.
func waitPaneCount(t *testing.T, base string, n int) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var last []int
	for time.Now().Before(deadline) {
		out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
		var payload struct {
			WorkspaceWindows []int `json:"workspace_windows"`
		}
		if err == nil && json.Unmarshal([]byte(out), &payload) == nil {
			last = payload.WorkspaceWindows
			total := 0
			for _, c := range last {
				total += c
			}
			if total == n {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the session never came down to %d panes (by workspace %v)", n, last)
}

// TestWorkspaceSelfExitLeavesTheNumbers lets REVW's panes, below the workspace
// on screen, exit on their own. Nobody closed anything, so the numbers stay:
// DPLY is still workspace 3 on the dock and in the daemon, no window-moved
// event goes out for its panes, and no workspace-switched fires for a switch
// nobody made. Closing a pane is a person's act, and it closes the gap up.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceSelfExitLeavesTheNumbers(t *testing.T) {
	term, base, release := renumberExitSetup(t)
	events := subscribeLines(t, base, "-s", renumberSession, "--types", "window-moved,workspace-switched")

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitPaneCount(t, base, renumberPanes["EDIT"]+renumberPanes["DPLY"])
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "REVW") }, uiTimeout); err != nil {
		t.Fatalf("REVW stayed on the dock after its panes exited: %v\n%s", err, term.Snapshot())
	}
	// Long enough for a renumber, which is one state push away, to land.
	time.Sleep(2 * time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "3": "DPLY"})
	if info.CurrentWorkspace != 3 {
		t.Fatalf("the workspace on screen became %d when panes below it exited, want 3", info.CurrentWorkspace)
	}
	wantPanesOn(t, base, map[int]string{1: "EDIT", 3: "DPLY"})
	if !screenHas(term.Screen(), "1·EDIT", "3·DPLY") {
		t.Fatalf("the dock renumbered when panes exited on their own, want 1·EDIT and 3·DPLY\n%s", term.Snapshot())
	}
	if m := renumberDockStatus(term.Screen()); m == nil || m[0] != "3:3" {
		t.Fatalf("the dock status is %v, want 3:3\n%s", m, term.Snapshot())
	}
	if got := events(); len(got) > 0 {
		t.Fatalf("panes exiting on their own sent %d window-moved or workspace-switched events, want none:\n%s", len(got), strings.Join(got, "\n"))
	}

	// opt+N past the last workspace opens the one after it, not the number
	// of workspaces plus one, which with a gap is DPLY's.
	if err := term.SendKeys(tuitest.Alt('5')); err != nil {
		t.Fatalf("press alt+5: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "4:0"
	}, uiTimeout); err != nil {
		t.Fatalf("opt+5 with a gap at 2 did not open workspace 4: %v\n%s", err, term.Snapshot())
	}
	altReaches(t, term, '3', "DPLY")
	wantNumbering(t, base, map[string]string{"1": "EDIT", "3": "DPLY"})

	// A pane the person closes closes the gap: DPLY becomes 2.
	if err := term.SendKeys(tuitest.Ctrl('b'), "x"); err != nil {
		t.Fatalf("close a pane: %v", err)
	}
	waitPills(t, term, "1·EDIT", "2·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "2:2" && !screenHas(s, "3·")
	}, uiTimeout); err != nil {
		t.Fatalf("closing a pane did not close the gap up: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY"})
	alive(t, term, "after panes exited and a pane was closed")
}

// TestWorkspaceShellExitClosesUp types exit in the only pane of workspace 2,
// a shell, with workspaces 1 to 3 in use. A person typed it, so it is a
// person's close: the numbers close up and DPLY becomes 2, where the panes
// that exit on their own in TestWorkspaceSelfExitLeavesTheNumbers leave a gap.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceShellExitClosesUp(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, renumberConfig)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renumberSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("new-window on 2: %v: %s", err, out)
	}
	for range renumberPanes["DPLY"] {
		if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", "3", "--no-focus"); err != nil {
			t.Fatalf("new-window on 3: %v: %s", err, out)
		}
	}
	for num, name := range map[string]string{"1": "EDIT", "2": "SHEL", "3": "DPLY"} {
		if out, err := tuiosCLI(t, base, "set-workspace-name", num, name, "--session", renumberSession); err != nil {
			t.Fatalf("name workspace %s: %v: %s", num, err, out)
		}
	}
	term := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "2·SHEL", "3·DPLY")

	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "2:1"
	}, uiTimeout); err != nil {
		t.Fatalf("alt+2 did not reach SHEL: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys("exit", tuitest.Enter); err != nil {
		t.Fatalf("type exit: %v", err)
	}
	waitPaneCount(t, base, renumberPanes["EDIT"]+renumberPanes["DPLY"])
	waitPills(t, term, "1·EDIT", "2·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "3·") }, uiTimeout); err != nil {
		t.Fatalf("typing exit in the last shell of workspace 2 left a gap, want DPLY on 2: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY"})
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "DPLY"})
	alive(t, term, "after typing exit in the last shell of a workspace")
}

// attentionWorkspace is the workspace list-attention gives the item of the
// pane named window, or 0 when it lists none.
func attentionWorkspace(t *testing.T, base, window string) int {
	t.Helper()
	out, err := tuiosOut(base, "list-attention", "--json")
	if err != nil {
		t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var payload struct {
		Items []struct {
			Name      string `json:"name"`
			Workspace int    `json:"workspace"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-attention output: %v\n%s", err, out)
	}
	for _, it := range payload.Items {
		if it.Name == window {
			return it.Workspace
		}
	}
	return 0
}

// TestWorkspaceMoveRenumbersInboxItems finishes an agent turn on EDIT, which
// opens an Inbox item that names EDIT's workspace, and moves EDIT right: the
// item names the workspace EDIT moved to, not the number it left.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceMoveRenumbersInboxItems(t *testing.T) {
	term, base := renumberSetup(t)
	if out, err := tuiosCLI(t, base, "new-window", "AGENTPANE", "-s", renumberSession, "--workspace", "1", "--no-focus"); err != nil {
		t.Fatalf("new-window AGENTPANE: %v: %s", err, out)
	}
	for _, state := range []string{"working", "done"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", state, "-s", renumberSession, "-w", "AGENTPANE", "--harness", "claude-code"); err != nil {
			t.Fatalf("set-agent-state %s: %v: %s", state, err, out)
		}
	}
	deadline := time.Now().Add(uiTimeout)
	for attentionWorkspace(t, base, "AGENTPANE") != 1 {
		if time.Now().After(deadline) {
			out, err := tuiosOut(base, "list-attention", "--json")
			win, _ := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
			t.Fatalf("the finished turn opened no Inbox item on workspace 1 (%v):\n%s\n%s", err, out, win)
		}
		time.Sleep(100 * time.Millisecond)
	}

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	deadline = time.Now().Add(uiTimeout)
	for got := attentionWorkspace(t, base, "AGENTPANE"); got != 2; got = attentionWorkspace(t, base, "AGENTPANE") {
		if time.Now().After(deadline) {
			t.Fatalf("the Inbox item of a pane on EDIT names workspace %d after EDIT moved to 2", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after moving a workspace with an Inbox item")
}
