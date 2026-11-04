package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The task board: b in the agents view groups the fleet column by task stage
// instead of by agent state. These tests add cards with the real CLI, hold
// them from fake agent panes, and read the board the way a person does.

// boardCards are the cards the board test adds, t1 first.
var boardCards = []string{
	"wire the retry jitter",
	"port the cache to lru",
	"split the auth middleware",
	"draft the rollout notes",
	"bump the go toolchain",
	"write the changelog",
}

// startTaskBoard starts a daemon with session S, three fake agent panes and
// six cards: t1 and t2 held, t3 free, t4 in verify, t5 done and t6 blocked.
// The blocked card is the newer of the two in Todo, so blocked-first is not
// oldest-first.
func startTaskBoard(t *testing.T, base string) {
	t.Helper()
	killDaemon(t, base)
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	for _, a := range []struct{ name, state, msg string }{
		{"api-1", "working", "editing files"},
		{"api-2", "needs_input", ""},
		{"docs-1", "done", ""},
	} {
		mustCLI(t, base, "new-window", a.name, "-s", "S", "--", "/bin/sh", "-c", "sleep 900")
		args := []string{"set-agent-state", a.state, "-s", "S", "-w", a.name, "--harness", "claude-code"}
		if a.msg != "" {
			args = append(args, "-m", a.msg)
		}
		mustCLI(t, base, args...)
	}
	for _, title := range boardCards {
		mustCLI(t, base, "task", "add", "-s", "S", title)
	}
	mustCLI(t, base, "task", "claim", "-s", "S", "t1", "-w", "api-1")
	mustCLI(t, base, "task", "claim", "-s", "S", "t2", "-w", "api-2")
	mustCLI(t, base, "task", "block", "-s", "S", "t6", "waits on t1")
	mustCLI(t, base, "task", "stage", "-s", "S", "t4", "verify")
	mustCLI(t, base, "task", "done", "-s", "S", "t5")
}

// openBoard opens the agents view and its board.
func openBoard(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "Needs you")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
}

// fleetLine is the fleet column, the first 56 cells, of row r.
func fleetLine(s tuitest.Screen, r int) string {
	line := []rune(s.Line(r))
	return string(line[:min(len(line), 56)])
}

// focusedWindow is the id and name of the focused window of session S.
func focusedWindow(t *testing.T, base string) (string, string) {
	t.Helper()
	out := mustCLI(t, base, "list-agents", "-s", "S", "--all", "--json")
	var res struct {
		Agents []struct {
			WindowID string `json:"window_id"`
			Name     string `json:"name"`
			Focused  bool   `json:"focused"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-agents --json: %v\n%s", err, out)
	}
	for _, a := range res.Agents {
		if a.Focused {
			return a.WindowID, a.Name
		}
	}
	t.Fatalf("no focused window in S:\n%s", out)
	return "", ""
}

// TestTaskBoardGroupsCardsByStage opens the board on six cards in four
// stages. The board opens as columns, every stage side by side in order; B
// shows the list and B again the columns. On the columns L and H move a card
// a stage and back, h then shift+right and shift+left do the same for the
// first card of the column before, and a drag of a card onto another column
// moves it there and back. Under 80 columns the board is the list. On the
// list the stage heads come in TaskStages order with their counts, Done
// folded; a held card names its holder and the holder's harness, a blocked
// card says why. A claim made from the CLI while the board is open moves the
// row. t takes the free card for the focused pane, n opens the New agent
// sheet with a card's title as the task, and at a narrow size the rows are one
// line. The Card tab shows the selected card's history. The cursor on the
// folded Done heading shows the fold in the focus, with Done's title. With
// [tasks] stages the hidden stages have no head.
//
// How it could pass wrongly: the CLI listings are read before the board, so a
// card the daemon never stored fails there and not on the screen; the take is
// checked in the daemon's listing, not on the screen.
//
// Negative controls, in NEGATIVE_CONTROLS.md: with the task events left off
// the Inbox subscription the CLI claim never shows on the board; with the
// order inside a stage cut the blocked card is not first; with [tasks] stages
// ignored the Verify head shows; with list-tasks' all_sessions cut the CLI
// listing fails; with drawFoldFocus reading the group titles first the Done
// heading shows the crash screen; with kanbanStep moving nothing L leaves t4
// in verify; with the drop moving nothing the drag leaves t6 in todo.
//
// The test ends by saving the columns at 160x45 and 100x30, dark, light and
// at 16 colours, as frames for a person to look at.
func TestTaskBoardGroupsCardsByStage(t *testing.T) {
	base := t.TempDir()
	startTaskBoard(t, base)

	// The daemon side: every session's cards, and the stage selector.
	var all struct {
		AllSessions bool `json:"all_sessions"`
		Tasks       []struct {
			ID      string `json:"id"`
			Session string `json:"session"`
		} `json:"tasks"`
	}
	out := mustCLI(t, base, "task", "ls", "--all-sessions", "--json")
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatalf("task ls --all-sessions --json: %v\n%s", err, out)
	}
	if !all.AllSessions || len(all.Tasks) != len(boardCards) {
		t.Fatalf("ASSERTION: task ls --all-sessions listed %d cards (all_sessions %v), want %d\n%s", len(all.Tasks), all.AllSessions, len(boardCards), out)
	}
	for _, tk := range all.Tasks {
		if tk.Session != "S" {
			t.Fatalf("ASSERTION: card %s names session %q, want S", tk.ID, tk.Session)
		}
	}
	if got := agentWindows(t, base, "--all", "--select", "stage:doing"); len(got) != 2 {
		t.Fatalf("ASSERTION: list-agents --select stage:doing = %v, want api-1 and api-2", got)
	}

	term := attachIn(t, base, "S", startOpts{cols: 200, rows: 55, shippedLooks: true})
	openBoard(t, term)
	// The board opens as columns: every stage side by side, in stage order.
	waitText(t, term, "the board's columns", "Todo  2", "Plan  0", "Doing  2", "Verify  1", "Review  0", "Done  1")
	checkColumns(t, term, "Todo  2", "Plan  0", "Doing  2", "Verify  1", "Review  0", "Done  1")
	saveCockpitFrames(t, term, "task-board-columns-200x55")
	// B shows the list.
	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return rowWith(s, "Todo  2") >= 0 && rowWith(s, "Doing  2") > rowWith(s, "Todo  2")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: B did not show the board as a list: %v\n%s", err, term.Snapshot())
	}
	waitText(t, term, "the board", "Todo  2", "Doing  2", "Verify  1", "Done  1")
	s := term.Screen()
	last := -1
	for _, head := range []string{"Todo  2", "Doing  2", "Verify  1", "Done  1"} {
		row := rowWith(s, head)
		if row <= last {
			t.Fatalf("ASSERTION: %q is on row %d, not below the head before it (row %d)\n%s", head, row, last, term.Snapshot())
		}
		last = row
	}
	if fleetRowWith(s, boardCards[4]) >= 0 {
		t.Errorf("ASSERTION: the folded Done stage shows its card\n%s", term.Snapshot())
	}
	if rowWith(s, "Plan  ") >= 0 {
		t.Errorf("ASSERTION: the empty Plan stage has a head\n%s", term.Snapshot())
	}
	// The blocked card is first in Todo, and says why.
	blocked := fleetRowWith(s, boardCards[5])
	if blocked != rowWith(s, "Todo  2")+1 {
		t.Errorf("ASSERTION: the blocked card is not the first row of Todo\n%s", term.Snapshot())
	}
	if l := fleetLine(s, blocked+1); !strings.Contains(l, "Blocked") || !strings.Contains(l, "waits on t1") {
		t.Errorf("ASSERTION: the blocked card's second line is %q, want Blocked and the reason\n%s", l, term.Snapshot())
	}
	// A held card names its holder and the holder's harness.
	held := fleetRowWith(s, boardCards[0])
	if l := fleetLine(s, held+1); !strings.Contains(l, "api-1") || !strings.Contains(l, "claude") {
		t.Errorf("ASSERTION: t1's second line is %q, want api-1 and claude\n%s", l, term.Snapshot())
	}
	// A holder that needs the person comes before one that works.
	if fleetRowWith(s, boardCards[1]) > held {
		t.Errorf("ASSERTION: t2, held by an agent that needs you, is below t1\n%s", term.Snapshot())
	}
	// The Card tab reads the card's history: t6 was added, then blocked.
	waitText(t, term, "the Card tab", "History", "blocked, waits on t1", "added")
	saveCockpitFrames(t, term, "task-board-200x55")

	// A claim from the CLI reaches the open board through the event stream.
	mustCLI(t, base, "task", "claim", "-s", "S", "t4", "-w", "docs-1")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := fleetRowWith(s, boardCards[3])
		return r >= 0 && strings.Contains(fleetLine(s, r+1), "docs-1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the CLI claim of t4 never showed on the board: %v\n%s", err, term.Snapshot())
	}

	// t takes the free Todo card, t3 on the second row, for the focused pane.
	// The claim moves it to Doing.
	if err := term.SendKeys("j", "t"); err != nil {
		t.Fatalf("press j t: %v", err)
	}
	wantID, wantName := focusedWindow(t, base)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := fleetRowWith(s, boardCards[2])
		return r >= 0 && strings.Contains(fleetLine(s, r+1), wantName)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: t never showed %s holding t3: %v\n%s", wantName, err, term.Snapshot())
	}
	for _, tk := range listTasks(t, base, "S").Tasks {
		if tk.ID == "t3" && (tk.Holder == nil || tk.Holder.Window != wantID) {
			t.Fatalf("ASSERTION: after t, t3 is held by %+v, want the focused pane %s", tk.Holder, wantID)
		}
	}

	// n on a card opens the New agent sheet with its title as the task.
	row, col := findText(t, term, boardCards[3])
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("press n: %v", err)
	}
	waitText(t, term, "the spawn sheet", "Where it works", "Start agent")
	if strings.Contains(term.Screen().Text(), "What should the agent do?") {
		t.Errorf("ASSERTION: the sheet's task is empty, want the card's title\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "task-board-spawn-200x55")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitText(t, term, "the board after the sheet", "Doing  3")

	// The cursor is on t4, the row above the folded Done heading. Done is the
	// seventh stage and there are six agent groups, so the focus must name it
	// from the stages. The heads show under the sheet too, so wait for the
	// sheet to close before the key.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Where it works")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the spawn sheet: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("press j: %v", err)
	}
	waitText(t, term, "the focus of the folded Done stage", "The group is folded", boardCards[4])
	s = term.Screen()
	if strings.Contains(s.Text(), "tuios hit a bug") {
		t.Fatalf("ASSERTION: the Done heading shows the crash screen\n%s", term.Snapshot())
	}
	folded := []rune(s.Line(rowWith(s, "The group is folded") - 1))
	if focus := string(folded[min(len(folded), 56):]); !strings.Contains(focus, "Done  1") {
		t.Errorf("ASSERTION: the focus title is %q, want Done and its count\n%s", focus, term.Snapshot())
	}
	saveCockpitFrames(t, term, "task-board-done-focus-200x55")
	if err := term.SendKeys("k"); err != nil {
		t.Fatalf("press k: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "The group is folded")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: k left the Done heading in the focus: %v\n%s", err, term.Snapshot())
	}

	// B again shows the columns, with the cursor still on t4.
	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B: %v", err)
	}
	waitText(t, term, "the columns again", "Review  0", "Plan  0")
	checkColumns(t, term, "Todo  1", "Doing  3", "Verify  1", "Review  0")

	// L moves the card under the cursor to the next stage, and H back.
	if err := term.SendKeys("L"); err != nil {
		t.Fatalf("press L: %v", err)
	}
	waitStage(t, term, base, "t4", "review", "L on t4")
	waitText(t, term, "t4 in Review", "Review  1", "Verify  0")
	saveCockpitFrames(t, term, "task-board-columns-moved-200x55")
	if err := term.SendKeys("H"); err != nil {
		t.Fatalf("press H: %v", err)
	}
	waitStage(t, term, base, "t4", "verify", "H on t4")
	waitText(t, term, "t4 back in Verify", "Review  0", "Verify  1")
	// h goes to the Doing column, on its first card: t2, whose holder needs
	// you. shift+right moves it on, and shift+left back.
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("press h: %v", err)
	}
	if err := term.SendKeys(tuitest.Key("\x1b[1;2C")); err != nil {
		t.Fatalf("press shift+right: %v", err)
	}
	waitStage(t, term, base, "t2", "verify", "h then shift+right")
	if err := term.SendKeys(tuitest.Key("\x1b[1;2D")); err != nil {
		t.Fatalf("press shift+left: %v", err)
	}
	waitStage(t, term, base, "t2", "doing", "shift+left on t2")

	// A card dragged onto another column moves to its stage: the blocked
	// t6 to the empty Review column, and back to Todo.
	row, col = findText(t, term, boardCards[5])
	_, review := findText(t, term, "Review  0")
	mousePress(t, term, col, row, tuitest.MouseLeft, 0)
	for x := col + 1; x <= review+2; x += 4 {
		mouseMotion(t, term, x, row, tuitest.MouseLeft, 0)
	}
	mouseMotion(t, term, review+2, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the dragged card under the pointer", " "+boardCards[5]+" ")
	saveCockpitFrames(t, term, "task-board-columns-drag-200x55")
	mouseRelease(t, term, review+2, row, tuitest.MouseLeft, 0)
	waitStage(t, term, base, "t6", "review", "the drag of t6 onto Review")
	waitText(t, term, "t6 in Review", "Review  1", "Todo  0")
	row, col = findText(t, term, boardCards[5])
	_, todo := findText(t, term, "Todo  0")
	mouseDrag(t, term, col, row, todo+2, row, tuitest.MouseLeft, 0)
	waitStage(t, term, base, "t6", "todo", "the drag of t6 back onto Todo")
	waitText(t, term, "t6 back in Todo", "Todo  1", "Review  0")

	// Narrow and short, the board is the list again, and its rows are one
	// line: the holder beside the title.
	if err := term.Resize(70, 16); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := rowWith(s, "wire the retry")
		return r >= 0 && strings.Contains(s.Line(r), "api-1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: at 70x16 t1's row is not one line with its holder: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "task-board-70x16")
	alive(t, term, "after the board")

	// [tasks] stages hides the stages it leaves out.
	writeConfig(t, base, "[tasks]\nstages = [\"doing\", \"todo\"]\n")
	other := attachIn(t, base, "S", startOpts{cols: 200, rows: 55, shippedLooks: true})
	openBoard(t, other)
	waitText(t, other, "the board with [tasks] stages", "Todo  1", "Doing  3")
	if txt := other.Screen().Text(); strings.Contains(txt, "Verify  1") || strings.Contains(txt, "Done  1") || strings.Contains(txt, "Plan  0") {
		t.Errorf("ASSERTION: [tasks] stages = [doing, todo] still shows Plan, Verify or Done\n%s", other.Snapshot())
	}
	// The stage order stays fixed whatever order the list gives.
	checkColumns(t, other, "Todo  1", "Doing  3")
	saveCockpitFrames(t, other, "task-board-stages-200x55")
	alive(t, other, "after the board with [tasks] stages")

	// The columns at two sizes, on a dark and a light theme and at 16
	// colours, for a person to look at.
	for _, look := range []struct {
		name, theme string
		env         []string
	}{
		{"dark", "", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{"light", "catppuccin_latte", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{"16", "", []string{"TERM=xterm", "COLORTERM="}},
	} {
		cfg := ""
		if look.theme != "" {
			cfg = "[appearance]\ntheme = \"" + look.theme + "\"\n"
		}
		writeConfig(t, base, cfg)
		view := attachIn(t, base, "S", startOpts{cols: 160, rows: 45, shippedLooks: true, env: look.env})
		openBoard(t, view)
		for _, size := range [][2]int{{160, 45}, {100, 30}} {
			name := fmt.Sprintf("task-board-columns-%dx%d-%s", size[0], size[1], look.name)
			if err := view.Resize(size[0], size[1]); err != nil {
				t.Fatalf("resize: %v", err)
			}
			waitText(t, view, name, "Todo  1", "Doing  3")
			if err := view.WaitStable(uiTimeout); err != nil {
				t.Fatalf("%s never settled: %v", name, err)
			}
			dir := artifactDir(t)
			saveArtifact(t, view, dir, name)
			savePNG(t, view.Screen(), hostPalette(t, look.theme), dir, name)
		}
		alive(t, view, look.name)
		// A client left at 100 columns would hold the session that narrow
		// for the next one, whose dock then has no room for the mode.
		if err := view.Resize(160, 45); err != nil {
			t.Fatalf("resize: %v", err)
		}
		waitText(t, view, "the board at 160x45 again", "Todo  1")
		_ = view.Close()
	}
}

// checkColumns asserts the heads are on one row, left to right in the order
// given.
func checkColumns(t *testing.T, term *tuitest.Terminal, heads ...string) {
	t.Helper()
	s := term.Screen()
	row, last := -1, -1
	for _, h := range heads {
		r := rowWith(s, h)
		if r < 0 || row >= 0 && r != row {
			t.Fatalf("ASSERTION: %q is on row %d, want the heads on one row (%d)\n%s", h, r, row, term.Snapshot())
		}
		row = r
		before, _, _ := strings.Cut(s.Line(r), h)
		col := len([]rune(before))
		if col <= last {
			t.Fatalf("ASSERTION: %q is at column %d, not right of the head before it (%d)\n%s", h, col, last, term.Snapshot())
		}
		last = col
	}
}

// waitStage waits for the daemon to list card id in stage.
func waitStage(t *testing.T, term *tuitest.Terminal, base, id, stage, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		for _, tk := range listTasks(t, base, "S").Tasks {
			if tk.ID == id {
				got = tk.Stage
			}
		}
		if got == stage {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: after %s, %s is in %q, want %q\n%s", what, id, got, stage, term.Snapshot())
}
