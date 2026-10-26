package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// taskListing is tuios task ls --json as the test reads it.
type taskListing struct {
	Tasks []struct {
		ID     string `json:"id"`
		Stage  string `json:"stage"`
		Holder *struct {
			Window string `json:"window"`
		} `json:"holder"`
	} `json:"tasks"`
}

func listTasks(t *testing.T, base, session string) taskListing {
	t.Helper()
	var l taskListing
	out := mustCLI(t, base, "task", "ls", "-s", session, "--json")
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, out)
	}
	return l
}

// TestTaskCardsAreClaimedOnceAndSurviveARestart: two panes claim the next card
// at the same moment through the CLI, each from its own shell, and each gets a
// different card. One finishes its card. After restart-server the cards are
// still there, the finished one done, and no card is held, since no pane's
// agent survives a restart.
//
// How it could pass wrongly: both claims could fail, or the second could
// never run, so each pane's exit status and output are read back; the
// listing before the restart must show two holders, so the restart has a
// holder to let go.
//
// Negative controls: with the daemon's restore not putting the cards back
// after its state push (restoreTasks in daemon_resurrect.go), the listing
// after the restart is empty. With claim-task's pick made outside the state
// change, the two claims can take the same card.
func TestTaskCardsAreClaimedOnceAndSurviveARestart(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tasks"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)

	mustCLI(t, base, "task", "add", "-s", sess, "-p", "first prompt", "first card")
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "second prompt", "second card")

	gate := filepath.Join(base, "go")
	bin := tuiosBin
	panes := map[string]string{}
	for _, name := range []string{"worker-a", "worker-b"} {
		id := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, name, "--no-focus", "--print-id"))
		out := filepath.Join(base, name+".out")
		panes[id] = out
		// Each pane waits for the gate, then claims for itself.
		script := "while [ ! -e " + gate + " ]; do sleep 0.05; done; " +
			bin + " task claim --next > " + out + ".tmp 2>&1; echo EXIT=$? >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
		mustCLI(t, base, "send-text", "-s", sess, "-w", id, script)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	claimed := map[string]string{}
	var report strings.Builder
	for id, out := range panes {
		got := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "EXIT=") },
			"the pane never finished its claim")
		fmt.Fprintf(&report, "%s:\n%s\n", filepath.Base(out), got)
		if !strings.Contains(got, "EXIT=0") {
			t.Fatalf("ASSERTION: a claim from pane %s failed:\n%s", id, got)
		}
		card, _, _ := strings.Cut(strings.TrimSpace(got), " ")
		claimed[id] = card
	}
	cards := map[string]bool{}
	for _, card := range claimed {
		cards[card] = true
	}
	if !cards["t1"] || !cards["t2"] {
		t.Fatalf("ASSERTION: the two panes claimed %v, want t1 and t2 one each", claimed)
	}

	before := listTasks(t, base, sess)
	held := 0
	for _, tk := range before.Tasks {
		if tk.Holder != nil {
			held++
			if claimed[tk.Holder.Window] != tk.ID {
				t.Errorf("ASSERTION: %s is held by %s, which claimed %s", tk.ID, tk.Holder.Window, claimed[tk.Holder.Window])
			}
		}
	}
	if held != 2 {
		t.Fatalf("ASSERTION: before the restart %d cards are held, want 2: %+v", held, before)
	}

	mustCLI(t, base, "task", "done", "-s", sess, "t1")

	mustCLI(t, base, "restart-server", "--yes")
	mustCLI(t, base, "new", "e2e-tasks-other", "--detach")
	waitForSessionInfo(t, base, sess)

	after := listTasks(t, base, sess)
	raw, _ := json.MarshalIndent(after, "", "  ")
	fmt.Fprintf(&report, "after the restart:\n%s\n", raw)
	saveText(t, "tasks-claim-and-restart", report.String())
	if len(after.Tasks) != 2 {
		t.Fatalf("ASSERTION: after the restart the session holds %d cards, want 2:\n%s", len(after.Tasks), raw)
	}
	for _, tk := range after.Tasks {
		if tk.Holder != nil {
			t.Errorf("ASSERTION: %s is still held by %s after the restart", tk.ID, tk.Holder.Window)
		}
	}
	if after.Tasks[0].Stage != "done" || after.Tasks[1].Stage != "doing" {
		t.Errorf("ASSERTION: after the restart the stages are %s and %s, want done and doing", after.Tasks[0].Stage, after.Tasks[1].Stage)
	}
	if got := mustCLI(t, base, "task", "get", "-s", sess, "t2"); !strings.Contains(got, "released") || !strings.Contains(got, "restart") {
		t.Errorf("ASSERTION: t2's history does not say it was released for the restart:\n%s", got)
	}
}

// waitForTaskText polls tuios task get until the card's text has every want,
// and returns it.
func waitForTaskText(t *testing.T, base, session, id string, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var got string
	for time.Now().Before(deadline) {
		got = mustCLI(t, base, "task", "get", "-s", session, id)
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(got, w)
		}
		if ok {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s never read %q:\n%s", id, want, got)
	return got
}

// agentWindows is list-agents --json's windows, by id.
func agentWindows(t *testing.T, base string, args ...string) []string {
	t.Helper()
	out := mustCLI(t, base, append([]string{"list-agents", "--json"}, args...)...)
	var res struct {
		Agents []struct {
			WindowID string `json:"window_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-agents --json: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(res.Agents))
	for _, a := range res.Agents {
		ids = append(ids, a.WindowID)
	}
	return ids
}

// TestTaskCardsAreReleasedWithTheirPane: a pane holds two cards, and task:
// names it by either. When the pane closes both cards are let go with "pane
// closed" in their history. A third card, held by a pane whose agent then
// reports none, is let go with "agent left". A client that attaches and pushes
// its state leaves every card in place.
//
// How it could pass wrongly: a card never claimed is also unheld, so each
// claim's holder is checked before the pane goes; the history must name the
// reason, not just show no holder.
//
// Negative controls: with the release not run inside the state change that
// closed the pane (releasedTaskEvents in session.go), the cards stay held.
// With the client sync not keeping the daemon's cards (state_merge.go), the
// listing after the attach is empty. With the selector reading no cards
// (paneSelectorTarget), task:t1 lists no pane.
func TestTaskCardsAreReleasedWithTheirPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tasks-release"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	for _, title := range []string{"one", "two", "three"} {
		mustCLI(t, base, "task", "add", "-s", sess, title)
	}
	closer := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "closer", "--no-focus", "--print-id"))
	agent := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent", "--no-focus", "--print-id"))
	mustCLI(t, base, "task", "claim", "-s", sess, "t1", "-w", closer)
	mustCLI(t, base, "task", "claim", "-s", sess, "t2", "-w", closer)
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", agent, "working", "--harness", "claude-code")
	mustCLI(t, base, "task", "claim", "-s", sess, "t3", "-w", agent)

	before := listTasks(t, base, sess)
	for i, want := range []string{closer, closer, agent} {
		if before.Tasks[i].Holder == nil || before.Tasks[i].Holder.Window != want {
			t.Fatalf("ASSERTION: %s is not held by %s before the pane goes: %+v", before.Tasks[i].ID, want, before.Tasks[i])
		}
	}
	// The selector names the pane by any card it holds, not only its newest.
	for _, id := range []string{"t1", "t2"} {
		if got := agentWindows(t, base, "-s", sess, "--all", "--select", "task:"+id); len(got) != 1 || got[0] != closer {
			t.Fatalf("ASSERTION: list-agents --select task:%s = %v, want the one pane %s", id, got, closer)
		}
	}
	if got := agentWindows(t, base, "-s", sess, "--all", "--select", "task:*"); len(got) != 2 {
		t.Fatalf("ASSERTION: list-agents --select task:* = %v, want the two holders", got)
	}

	mustCLI(t, base, "close-window", "-s", sess, closer)
	var report strings.Builder
	for _, id := range []string{"t1", "t2"} {
		fmt.Fprintf(&report, "%s after close-window:\n%s\n", id, waitForTaskText(t, base, sess, id, "released", "pane closed"))
	}
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", agent, "none")
	fmt.Fprintf(&report, "t3 after the agent left:\n%s\n", waitForTaskText(t, base, sess, "t3", "released", "agent left"))

	// A client pushes its own picture of the session, which carries no cards.
	term := attachIn(t, base, sess, startOpts{})
	newWindow(t, term)
	waitWindowCount(t, term, 3, "the attached client's new window")
	after := listTasks(t, base, sess)
	raw, _ := json.MarshalIndent(after, "", "  ")
	fmt.Fprintf(&report, "after a client sync:\n%s\n", raw)
	saveText(t, "tasks-release-with-pane", report.String())
	if len(after.Tasks) != 3 {
		t.Fatalf("ASSERTION: after a client sync the session holds %d cards, want 3:\n%s", len(after.Tasks), raw)
	}
	for _, tk := range after.Tasks {
		if tk.Holder != nil || tk.Stage != "doing" {
			t.Errorf("ASSERTION: %s after its pane went = %+v, want doing with no holder", tk.ID, tk)
		}
	}
}
