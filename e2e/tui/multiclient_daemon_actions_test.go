package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Parity gap 1, step 4, batch A: the keymap actions that are an op on
// session state (select_window_N, last_pane, send_to_workspace_N,
// move_and_follow_N, break_pane, close_workspace, move_workspace_left and
// right, minimize_window, restore_all; not last_pane) run in the daemon even with a TUI
// attached. Before, run-command routed them to the TUI, which changed its
// own copy and pushed it, the third class of issue #162: the daemon owned
// the field for run-command callers while a push still owned it for keys.

// daState is the part of list-windows the batch reads.
type daState struct {
	Current int    `json:"current_workspace"`
	Focused string `json:"focused_window_id"`
	Windows []struct {
		ID        string `json:"window_id"`
		Workspace int    `json:"workspace"`
		Minimized bool   `json:"minimized"`
	} `json:"windows"`
}

func daRead(t *testing.T, base, sess string) daState {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var s daState
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("read list-windows: %v\n%s", err, out)
	}
	return s
}

func (s daState) win(id string) (ws int, min bool) {
	for _, w := range s.Windows {
		if w.ID == id {
			return w.Workspace, w.Minimized
		}
	}
	return 0, false
}

func (s daState) has(id string) (int, bool) {
	for i, w := range s.Windows {
		if w.ID == id {
			return i, true
		}
	}
	return 0, false
}

// daRun runs action by run-command and returns whether a client ran it.
func daRun(t *testing.T, base, sess, action string) (routed bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"session": sess, "command": action})
	out, err := tuiosCLI(t, base, "call", "run-command", string(params))
	if err != nil {
		t.Fatalf("run-command %s: %v\n%s", action, err, out)
	}
	var res struct {
		Routed bool `json:"routed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("read run-command %s: %v\n%s", action, err, out)
	}
	return res.Routed
}

// TestDaemonActionRunsOnceWithATUIAttached (T1): with one TUI client on the
// session, run-command runs each batch A action in the daemon (routed
// false), the daemon's state changes by exactly that step, and the TUI draws
// it. The transcript goes to daemon-actions-t1.txt.
//
// How this could pass wrongly, written down first:
//   - The TUI could run the action and push, and the state still change: the
//     check is routed false, which only the daemon's own path answers.
//   - A step could already hold before it ran: each names a state the step
//     before did not have.
func TestDaemonActionRunsOnceWithATUIAttached(t *testing.T) {
	const sess = "da-t1"
	base := t.TempDir()
	writeConfig(t, base, "\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	term := attachIn(t, base, sess, startOpts{})
	newWindow(t, term)
	waitWindowCount(t, term, 2, "the second pane")
	var log strings.Builder
	s := daRead(t, base, sess)
	first, second := s.Windows[0].ID, s.Windows[1].ID
	fmt.Fprintf(&log, "start: current %d focused %s\n", s.Current, s.Focused)

	step := func(action, what string, ok func(daState) bool) {
		t.Helper()
		if daRun(t, base, sess, action) {
			t.Fatalf("%s: run-command answered routed true; the TUI ran it, not the daemon", action)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			s := daRead(t, base, sess)
			if ok(s) {
				fmt.Fprintf(&log, "%s: routed false, %s: current %d focused %s\n", action, what, s.Current, s.Focused)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %s did not hold: current %d focused %s %+v", action, what, s.Current, s.Focused, s.Windows)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	step("select_window_1", "the first pane is focused", func(s daState) bool { return s.Focused == first })
	step("select_window_2", "the second pane is focused", func(s daState) bool { return s.Focused == second })
	step("minimize_window", "the second pane is minimized", func(s daState) bool { _, m := s.win(second); return m })
	step("restore_all", "the second pane is shown again", func(s daState) bool { _, m := s.win(second); return !m })
	step("select_window_2", "the second pane is focused again", func(s daState) bool { return s.Focused == second })
	step("send_to_workspace_2", "the second pane went to workspace 2, the view stayed", func(s daState) bool {
		ws, _ := s.win(second)
		return ws == 2 && s.Current == 1
	})
	waitWindowCount(t, term, 1, "the TUI after send_to_workspace_2")
	// Workspace 1 empties, and the daemon numbers workspaces with no gap, so
	// the one both panes are on is 1 again.
	step("move_and_follow_2", "the first pane joined the second and the view followed", func(s daState) bool {
		ws, _ := s.win(first)
		ws2, _ := s.win(second)
		return ws == ws2 && s.Current == ws && s.Focused == first
	})
	waitWindowCount(t, term, 2, "the TUI on workspace 2")
	// last_pane stays the TUI's: it goes back across workspaces from the
	// TUI's own record. See daemon_actions.go.
	if !daRun(t, base, sess, "last_pane") {
		t.Fatalf("last_pane answered routed false; it stays the TUI's")
	}
	fmt.Fprintf(&log, "last_pane: routed true, the TUI's\n")
	step("select_window_2", "the second pane is focused here", func(s daState) bool { return s.Focused == second })
	step("break_pane", "the second pane is alone on a workspace of its own, shown", func(s daState) bool {
		ws1, _ := s.win(first)
		ws2, _ := s.win(second)
		return ws2 != ws1 && s.Current == ws2 && s.Focused == second
	})
	before := daRead(t, base, sess)
	was, _ := before.win(second)
	step("move_workspace_left", "the shown workspace moved left, past the first pane's", func(s daState) bool {
		ws1, _ := s.win(first)
		ws2, _ := s.win(second)
		return ws2 == was-1 && ws1 == was && s.Current == ws2
	})
	step("move_workspace_right", "and back right", func(s daState) bool {
		ws1, _ := s.win(first)
		ws2, _ := s.win(second)
		return ws2 == was && ws1 == was-1 && s.Current == ws2
	})
	step("close_workspace", "the second pane is closed, the first kept", func(s daState) bool {
		_, ok := s.has(second)
		return !ok && len(s.Windows) == 1
	})
	waitWindowCount(t, term, 1, "the TUI after close_workspace")

	if err := os.WriteFile(filepath.Join(artifactDir(t), "daemon-actions-t1.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
	alive(t, term, "after the daemon ran the batch")
}

// TestDaemonActionHeadlessThenAttach (T5): with no client, run-command runs
// send_to_workspace_2 in the daemon. A TUI that attaches after it draws the
// result in its first frames, and a push of its own (a new pane) does not
// put the moved pane back.
//
// How this could pass wrongly, written down first:
//   - The TUI could draw both panes and the count still read 1 if the second
//     were off screen: the daemon's list-windows names its workspace too.
//   - The TUI's push could undo the move after the check: the check runs
//     again after the TUI opened a pane and pushed.
func TestDaemonActionHeadlessThenAttach(t *testing.T) {
	const sess = "da-t5"
	base := t.TempDir()
	writeConfig(t, base, "\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "second"); err != nil {
		t.Fatalf("new-window: %v: %s", err, out)
	}
	second := windowIDByName(t, base, sess, "second")
	if daRun(t, base, sess, "send_to_workspace_2") {
		t.Fatalf("send_to_workspace_2 with no client answered routed true")
	}
	check := func(when string) {
		t.Helper()
		if ws, _ := daRead(t, base, sess).win(second); ws != 2 {
			t.Fatalf("%s: the second pane is on workspace %d, want 2", when, ws)
		}
	}
	check("after the headless send")
	term := attachIn(t, base, sess, startOpts{})
	waitWindowCount(t, term, 1, "the TUI's first frames: one pane on workspace 1")
	newWindow(t, term)
	waitWindowCount(t, term, 2, "a pane the TUI opened")
	check("after the TUI pushed")
	alive(t, term, "after the headless send")
}

// TestDaemonActionFromAPaneKeepsItsGrants (T6): a pane without the respond
// grant may not run a keymap action through run-command (refuseTapeTyping),
// and the promotion to the daemon must not open a way past that check. A
// pane with admin and not respond calls the run-command verb with select_window_1
// and send_to_workspace_2 while a TUI is attached: both are refused, and
// nothing changed.
//
// How this could pass wrongly, written down first:
//   - The command could fail for another reason (not run at all): the output
//     must name the refusal.
//   - The refusal could come after the daemon ran it: the daemon's focus and
//     the pane's workspace are read after.
func TestDaemonActionFromAPaneKeepsItsGrants(t *testing.T) {
	const sess = "da-t6"
	base := t.TempDir()
	writeConfig(t, base, "\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "reader", "--grants", "admin"); err != nil {
		t.Fatalf("new-window reader: %v: %s", err, out)
	}
	reader := windowIDByName(t, base, sess, "reader")
	term := attachIn(t, base, sess, startOpts{})
	waitWindowCount(t, term, 2, "both panes")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", sess, reader); err != nil {
		t.Fatalf("focus-window: %v: %s", err, out)
	}
	for i, action := range []string{"select_window_1", "send_to_workspace_2"} {
		outFile, done := filepath.Join(base, fmt.Sprint("t6-", i)), filepath.Join(base, fmt.Sprint("t6-", i, ".done"))
		// admin lets the pane call run-command and does not imply respond,
		// so only the respond check stands between the action and the
		// client.
		line := fmt.Sprintf("%s call run-command '{\"session\":\"%s\",\"command\":\"%s\"}' > %s 2>&1; echo done > %s\n", tuiosBin, sess, action, outFile, done)
		if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", reader, line); err != nil {
			t.Fatalf("send-text: %v: %s", err, out)
		}
		deadline := time.Now().Add(shellTimeout)
		for {
			if _, err := os.Stat(done); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run-command %s in the pane never finished", action)
			}
			time.Sleep(150 * time.Millisecond)
		}
		got, _ := os.ReadFile(outFile)
		t.Logf("%s from the pane: %s", action, got)
		if !strings.Contains(string(got), "refused for this pane") {
			t.Fatalf("run-command %s from a pane without respond was not refused:\n%s", action, got)
		}
		s := daRead(t, base, sess)
		ws, _ := s.win(reader)
		if s.Focused != reader || ws != 1 {
			t.Fatalf("after the refused %s: focused %s, reader on workspace %d; want the reader focused on 1", action, s.Focused, ws)
		}
	}
	alive(t, term, "after the refused actions")
}

// daKeys binds the batch A actions with no free default key in window
// management mode.
const daKeys = "[keybindings.window_management]\nsend_to_workspace_2 = [\"alt+x\"]\nresize_pane_right_5 = [\"alt+r\"]\nadd_master = [\"alt+a\"]\nremove_master = [\"alt+d\"]\n"

// daSession is treeOpsSession with daKeys and n tiled panes: a detached
// session, two TUI clients of one size.
func daSession(t *testing.T, name string, n int) (string, *tuitest.Terminal, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, daKeys)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	a := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows})
	for i := 1; i < n; i++ {
		newWindow(t, a)
	}
	waitWindowCount(t, a, n, "the panes on the first client")
	enableTiling(t, a)
	b := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows})
	waitForSettledGeometryIn(t, base, name, n)
	return base, a, b
}

// daWait polls the daemon's state until ok holds.
func daWait(t *testing.T, base, sess, what string, ok func(daState) bool) daState {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		s := daRead(t, base, sess)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not hold: current %d focused %s %+v", what, s.Current, s.Focused, s.Windows)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s daState) minimized() int {
	n := 0
	for _, w := range s.Windows {
		if w.Minimized {
			n++
		}
	}
	return n
}

// TestDaemonActionFromAKeyReachesThePeer (T2): a batch A key pressed on one
// client changes the daemon's state, the other client draws the same, and a
// push from the other client does not undo it.
//
// How this could pass wrongly, written down first:
//   - Both screens could match with the change on neither: each step reads
//     the daemon's state for the value it set.
//   - The peer's later push could undo it after the check: requireOneLayout
//     makes both clients push, and the state is read again after.
func TestDaemonActionFromAKeyReachesThePeer(t *testing.T) {
	const name = "da-t2"
	base, a, b := daSession(t, name, 3)
	start := daRead(t, base, name)
	moved := start.Focused

	if err := a.SendKeys("m"); err != nil {
		t.Fatal(err)
	}
	daWait(t, base, name, "minimize_window from the first client", func(s daState) bool { return s.minimized() == 1 })
	requireSameScreens(t, a, b, "after minimize_window")
	if err := a.SendKeys("M"); err != nil {
		t.Fatal(err)
	}
	daWait(t, base, name, "restore_all from the first client", func(s daState) bool { return s.minimized() == 0 })
	requireOneLayout(t, base, name, a, b, 3, "after restore_all")
	if s := daRead(t, base, name); s.minimized() != 0 {
		t.Fatalf("a push brought a minimized pane back: %+v", s.Windows)
	}

	if out, err := tuiosCLI(t, base, "focus-window", "-s", name, moved); err != nil {
		t.Fatalf("focus-window: %v: %s", err, out)
	}
	daWait(t, base, name, "the focus on the pane to move", func(s daState) bool { return s.Focused == moved })
	if err := a.SendKeys(tuitest.Alt("x")); err != nil {
		t.Fatal(err)
	}
	daWait(t, base, name, "send_to_workspace_2 from the first client", func(s daState) bool {
		ws, _ := s.win(moved)
		return ws == 2 && s.Current == 1
	})
	waitWindowCount(t, b, 2, "the second client after send_to_workspace_2")
	requireOneLayout(t, base, name, a, b, 3, "after send_to_workspace_2")
	if ws, _ := daRead(t, base, name).win(moved); ws != 2 {
		t.Fatalf("a push put the moved pane back on workspace %d", ws)
	}
	alive(t, a, "after the keys")
	alive(t, b, "after the keys")
}

// TestDaemonActionsFromTwoClientsAtOnce (T3): both clients press
// minimize_window at the same moment, then restore_all, for ten rounds. The
// daemon runs each press in turn on the pane focused then, so every round
// minimizes two panes and restores them; both clients draw one layout.
//
// How this could pass wrongly, written down first:
//   - The two presses could land one after the other by chance and both
//     count even on a path that loses one: ten rounds, each sent from two
//     goroutines at once, and every one must minimize two.
//   - A press could be lost and a later one make the count up: the count is
//     checked after every round, not only at the end.
func TestDaemonActionsFromTwoClientsAtOnce(t *testing.T) {
	const name = "da-t3"
	base, a, b := daSession(t, name, 4)
	var log strings.Builder
	for round := range 10 {
		together(t, a, "m", b, "m")
		s := daWait(t, base, name, fmt.Sprintf("round %d: two presses of minimize_window", round), func(s daState) bool { return s.minimized() >= 2 })
		time.Sleep(300 * time.Millisecond)
		if s = daRead(t, base, name); s.minimized() != 2 {
			t.Fatalf("round %d: %d panes minimized after two presses at once, want 2: %+v", round, s.minimized(), s.Windows)
		}
		requireSameScreens(t, a, b, fmt.Sprintf("round %d minimized", round))
		together(t, a, "M", b, "M")
		daWait(t, base, name, fmt.Sprintf("round %d: restore_all", round), func(s daState) bool { return s.minimized() == 0 })
		requireSameScreens(t, a, b, fmt.Sprintf("round %d restored", round))
		fmt.Fprintf(&log, "round %d: two minimized, all restored\n", round)
	}
	requireOneLayout(t, base, name, a, b, 4, "after every round")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "daemon-actions-t3.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// daGeom is the daemon's rectangles by window id, once they settle.
func daGeom(t *testing.T, base, sess string, n int) map[string]winRect {
	t.Helper()
	m := map[string]winRect{}
	for _, r := range waitForSettledGeometryIn(t, base, sess, n) {
		m[r.ID] = r
	}
	return m
}

// TestDaemonDirectionActionsRunWithATUIAttached (T1, batch B): with a TUI on
// a tiled BSP session of two panes side by side, run-command runs
// terminal_focus_right, swap_left and resize_pane_right_5 in the daemon (routed
// false), each changing the daemon's state by its step, and the TUI draws
// the same layout.
//
// How this could pass wrongly, written down first:
//   - The TUI could run it and push: routed false is the daemon's answer.
//   - A step could hold already: the focus move starts on the left pane, swap
//     needs the pane order to change, resize needs the width to grow.
func TestDaemonDirectionActionsRunWithATUIAttached(t *testing.T) {
	const name = "da-b1"
	base, a, b := daSession(t, name, 2)
	g := daGeom(t, base, name, 2)
	var left, right string
	for id, r := range g {
		if left == "" || r.X < g[left].X {
			left = id
		}
	}
	for id := range g {
		if id != left {
			right = id
		}
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", name, left); err != nil {
		t.Fatalf("focus-window: %v: %s", err, out)
	}
	daWait(t, base, name, "the focus on the left pane", func(s daState) bool { return s.Focused == left })
	run := func(action string) {
		t.Helper()
		if daRun(t, base, name, action) {
			t.Fatalf("%s: run-command answered routed true; the TUI ran it, not the daemon", action)
		}
	}
	run("terminal_focus_right")
	daWait(t, base, name, "terminal_focus_right", func(s daState) bool { return s.Focused == right })
	run("swap_left")
	deadline := time.Now().Add(uiTimeout)
	for g = daGeom(t, base, name, 2); g[right].X >= g[left].X; g = daGeom(t, base, name, 2) {
		if time.Now().After(deadline) {
			t.Fatalf("swap_left: the right pane did not move left: %v", g)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The pane now on the left is focused: resize_pane_right_5 grows it.
	w0 := g[right].Width
	run("resize_pane_right_5")
	deadline = time.Now().Add(uiTimeout)
	for g = daGeom(t, base, name, 2); g[right].Width < w0+4; g = daGeom(t, base, name, 2) {
		if time.Now().After(deadline) {
			t.Fatalf("resize_pane_right_5: the focused pane is %d wide, was %d", g[right].Width, w0)
		}
		time.Sleep(100 * time.Millisecond)
	}
	requireOneLayout(t, base, name, a, b, 2, "after the direction actions")

	// In the scrolling layout a side is a column, which the client works
	// out: the same action is routed to it.
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"mode","mode":"scrolling"}`); err != nil {
		t.Fatalf("arrange mode scrolling: %v: %s", err, out)
	}
	if !daRun(t, base, name, "swap_left") {
		t.Fatalf("swap_left in the scrolling layout answered routed false; the daemon has no columns to swap")
	}
}

// daRootRatio is the split ratio at the root of the current workspace's
// tree, as layout-tree reports it.
func daRootRatio(t *testing.T, base, sess string) float64 {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "layout-tree", `{"session":"`+sess+`"}`)
	if err != nil {
		t.Fatalf("layout-tree: %v\n%s", err, out)
	}
	var lt struct {
		Tree struct {
			Root struct {
				Ratio float64 `json:"split_ratio"`
			} `json:"root"`
		} `json:"tree"`
	}
	if err := json.Unmarshal([]byte(out), &lt); err != nil {
		t.Fatalf("read layout-tree: %v\n%s", err, out)
	}
	return lt.Tree.Root.Ratio
}

// TestDaemonResizeFromTwoClientsAtOnce (T3, batch B): both clients press
// resize_pane_right_5 at the same moment on the left pane of two, for three
// rounds. Every press must land in the session's tree, which every client
// lays out from: the root split moves by two steps of 5 cells in 120 a
// round, and the two clients draw one layout.
//
// The tree is read, not list-windows: with a client attached the daemon's
// rectangles are the ones a client last pushed, which can lag the tree until
// the next push. requireOneLayout at the end has both clients push and
// checks the rectangles agree.
//
// How this could pass wrongly, written down first:
//   - The presses could land one after the other by chance: each round sends
//     both at once, and the ratio is checked after every round.
//   - The pane could reach the layout's limit and stop for that reason: three
//     rounds of ten cells stay inside the 120 columns.
func TestDaemonResizeFromTwoClientsAtOnce(t *testing.T) {
	const name = "da-b3"
	base, a, b := daSession(t, name, 2)
	g := daGeom(t, base, name, 2)
	left := ""
	for id, r := range g {
		if left == "" || r.X < g[left].X {
			left = id
		}
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", name, left); err != nil {
		t.Fatalf("focus-window: %v: %s", err, out)
	}
	daWait(t, base, name, "the focus on the left pane", func(s daState) bool { return s.Focused == left })
	var log strings.Builder
	const step = 5.0 / bigCols
	ratio := daRootRatio(t, base, name)
	for round := range 3 {
		together(t, a, string(tuitest.Alt("r")), b, string(tuitest.Alt("r")))
		want := ratio + 2*step
		deadline := time.Now().Add(uiTimeout)
		now := daRootRatio(t, base, name)
		for now < want-step/2 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			now = daRootRatio(t, base, name)
		}
		time.Sleep(300 * time.Millisecond)
		now = daRootRatio(t, base, name)
		fmt.Fprintf(&log, "round %d: root ratio %.4f -> %.4f (want %.4f)\n", round, ratio, now, want)
		if now < want-step/2 || now > want+step/2 {
			t.Fatalf("round %d: two presses of resize_pane_right_5 moved the root ratio from %.4f to %.4f, want %.4f: a press was lost\n%s", round, ratio, now, want, log.String())
		}
		requireSameScreens(t, a, b, fmt.Sprintf("round %d", round))
		ratio = now
	}
	requireOneLayout(t, base, name, a, b, 2, "after every round")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "daemon-actions-b3.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// daLayout is the part of layout-tree batch C reads.
type daLayout struct {
	Mode   string `json:"mode"`
	Tiling bool   `json:"tiling"`
	Master struct {
		Count    int     `json:"count"`
		Position string  `json:"position"`
		Ratio    float64 `json:"ratio"`
	} `json:"master"`
	Tree json.RawMessage `json:"tree"`
}

func daReadLayout(t *testing.T, base, sess string) daLayout {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "layout-tree", `{"session":"`+sess+`"}`)
	if err != nil {
		t.Fatalf("layout-tree: %v\n%s", err, out)
	}
	var l daLayout
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("read layout-tree: %v\n%s", err, out)
	}
	return l
}

// TestDaemonLayoutActionsRunWithATUIAttached (T1, batch C1): with a TUI on
// a tiled session of four panes, run-command runs the layout shape actions
// in the daemon (routed false), each changing layout-tree by its step, and
// the TUI keeps drawing. The transcript goes to daemon-actions-c1.txt.
//
// How this could pass wrongly, written down first:
//   - The TUI could run each and push: routed false is the daemon's answer.
//   - A step could already hold: each check names a value the step before
//     did not have (a new mode, count, position, tree or tiling flag).
func TestDaemonLayoutActionsRunWithATUIAttached(t *testing.T) {
	const name = "da-c1"
	base, a, b := daSession(t, name, 4)
	var log strings.Builder
	step := func(action, what string, ok func(before, now daLayout) bool) {
		t.Helper()
		before := daReadLayout(t, base, name)
		if daRun(t, base, name, action) {
			t.Fatalf("%s: run-command answered routed true; the TUI ran it, not the daemon", action)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			now := daReadLayout(t, base, name)
			if ok(before, now) {
				fmt.Fprintf(&log, "%s: routed false, %s: mode %s tiling %v masters %d at %s\n", action, what, now.Mode, now.Tiling, now.Master.Count, now.Master.Position)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %s did not hold: before %+v now %+v", action, what, before, now)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	step("rotate_split", "the tree changed", func(b, n daLayout) bool { return string(n.Tree) != string(b.Tree) })
	step("equalize_splits", "the tree is even", func(_, n daLayout) bool {
		return !strings.Contains(string(n.Tree), `"split_ratio":0.`) || strings.Count(string(n.Tree), `"split_ratio":0.5,`) >= 3
	})
	step("cycle_layout", "the layout is master-stack", func(_, n daLayout) bool { return n.Mode == "master-stack" })
	step("add_master", "one master more", func(b, n daLayout) bool { return n.Master.Count == max(b.Master.Count, 1)+1 })
	step("remove_master", "one master fewer", func(b, n daLayout) bool { return n.Master.Count == b.Master.Count-1 })
	step("cycle_master_position", "the masters moved", func(b, n daLayout) bool { return n.Master.Position != b.Master.Position })
	step("set_master_position_top", "the masters are on top", func(_, n daLayout) bool { return n.Master.Position == "top" })
	step("toggle_tiling", "tiling is off", func(_, n daLayout) bool { return !n.Tiling })
	step("toggle_tiling", "tiling is on again", func(_, n daLayout) bool { return n.Tiling })
	requireSameScreens(t, a, b, "after the layout actions")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "daemon-actions-c1.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
	alive(t, a, "after the layout actions")
}

// TestDaemonMastersFromTwoClientsAtOnce (T3, batch C1): in master-stack on
// six panes, both clients press add_master at the same moment, then
// remove_master, for five rounds. Every press must land: the count goes up
// by two and back down by two each round, and the two clients draw one
// layout.
//
// How this could pass wrongly, written down first:
//   - The presses could land one after the other by chance: each is sent
//     from two goroutines at once, and the count is read after each.
//   - The count could reach its limit and stop for that reason: it moves
//     between 1 and 3 of six panes.
func TestDaemonMastersFromTwoClientsAtOnce(t *testing.T) {
	const name = "da-c3"
	base, a, b := daSession(t, name, 6)
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"mode","mode":"master-stack"}`); err != nil {
		t.Fatalf("arrange mode master-stack: %v: %s", err, out)
	}
	count := func() int { return max(daReadLayout(t, base, name).Master.Count, 1) }
	waitCount := func(what string, want int) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for count() != want && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		if got := count(); got != want {
			t.Fatalf("%s: %d masters, want %d: a press was lost", what, got, want)
		}
	}
	var log strings.Builder
	n := count()
	for round := range 5 {
		together(t, a, string(tuitest.Alt("a")), b, string(tuitest.Alt("a")))
		waitCount(fmt.Sprintf("round %d, two add_master at once", round), n+2)
		requireSameScreens(t, a, b, fmt.Sprintf("round %d added", round))
		together(t, a, string(tuitest.Alt("d")), b, string(tuitest.Alt("d")))
		waitCount(fmt.Sprintf("round %d, two remove_master at once", round), n)
		requireSameScreens(t, a, b, fmt.Sprintf("round %d removed", round))
		fmt.Fprintf(&log, "round %d: %d -> %d -> %d masters\n", round, n, n+2, n)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "daemon-actions-c3.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}
