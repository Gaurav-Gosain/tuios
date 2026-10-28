package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"

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
// stages. The stage heads come in TaskStages order with their counts, Done
// folded; a held card names its holder and the holder's harness, a blocked
// card says why. A claim made from the CLI while the board is open moves the
// row. t takes the free card for the focused pane, n opens the New agent
// sheet with a card's title as the task, and at a narrow size the rows are one
// line. The Card tab shows the selected card's history. With [tasks] stages
// the hidden stages have no head.
//
// How it could pass wrongly: the CLI listings are read before the board, so a
// card the daemon never stored fails there and not on the screen; the take is
// checked in the daemon's listing, not on the screen.
//
// Negative controls, in NEGATIVE_CONTROLS.md: with the task events left off
// the Inbox subscription the CLI claim never shows on the board; with the
// order inside a stage cut the blocked card is not first; with [tasks] stages
// ignored the Verify head shows; with list-tasks' all_sessions cut the CLI
// listing fails.
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

	// Narrow and short, the rows are one line: the holder beside the title.
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
	if txt := other.Screen().Text(); strings.Contains(txt, "Verify  1") || strings.Contains(txt, "Done  1") {
		t.Errorf("ASSERTION: [tasks] stages = [doing, todo] still shows Verify or Done\n%s", other.Snapshot())
	}
	// The stage order stays fixed whatever order the list gives.
	if sc := other.Screen(); rowWith(sc, "Todo  1") > rowWith(sc, "Doing  3") {
		t.Errorf("ASSERTION: [tasks] stages = [doing, todo] put Doing above Todo, want the fixed stage order\n%s", other.Snapshot())
	}
	saveCockpitFrames(t, other, "task-board-stages-200x55")
	alive(t, other, "after the board with [tasks] stages")
}
