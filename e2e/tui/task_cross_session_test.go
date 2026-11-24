package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// crossCard is a card of a list-tasks row, with its holder's session.
type crossCard struct {
	ID     string `json:"id"`
	Holder *struct {
		Window  string `json:"window"`
		Name    string `json:"name"`
		Session string `json:"session"`
	} `json:"holder"`
}

// crossCards are the cards of session, by id.
func crossCards(t *testing.T, base, session string) (map[string]crossCard, string) {
	t.Helper()
	out := mustCLI(t, base, "task", "ls", "-s", session, "--json")
	var l struct {
		Tasks []crossCard `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, out)
	}
	m := map[string]crossCard{}
	for _, c := range l.Tasks {
		m[c.ID] = c
	}
	return m, out
}

// lastRelease is the reason of the newest released entry in the history of
// card id of session, "" for none.
func lastRelease(t *testing.T, base, session, id string) string {
	t.Helper()
	out := mustCLI(t, base, "task", "get", id, "-s", session, "--json")
	var d struct {
		History []struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("task get --json: %v\n%s", err, out)
	}
	for _, e := range slices.Backward(d.History) {
		if e.Action == "released" {
			return e.Reason
		}
	}
	return ""
}

// TestAPaneOfAnotherSessionHoldsAProjectCard: a card with a project is added
// in session lead, and a pane of session crew on the same daemon claims it
// from inside the pane. The card's holder is that pane, and the row names
// crew. The board of lead shows the holder as worker in crew. When the pane
// closes, the card is let go with reason pane closed. A second pane of crew
// claims it again, and when its agent leaves, the card is let go with reason
// agent left. A card without a project refuses a pane of crew.
//
// How it could pass wrongly: the claim could land for another pane, so the
// holder's window id is checked against the pane's own id. The release could
// come from somewhere else, so the history's reason is read.
//
// Negative control, in NEGATIVE_CONTROLS.md: on a build of main the claim
// from crew fails with "no window found".
func TestAPaneOfAnotherSessionHoldsAProjectCard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, s := range []string{"lead", "crew"} {
		mustCLI(t, base, "new", s, "--detach")
		waitForSessionInfo(t, base, s)
	}
	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "crew", "worker", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", "crew", "-w", worker, "working", "--harness", "claude-code", "-m", "on it")
	mustCLI(t, base, "task", "add", "-s", "lead", "--project", "git@github.com:acme/api.git", "port the parser")
	mustCLI(t, base, "task", "add", "-s", "lead", "write the notes")

	// A card without a project stays in its session.
	if out := inPane(t, base, "crew", worker, "task claim t2 -s lead"); strings.Contains(out, "EXIT=0") || !strings.Contains(out, "project") {
		t.Fatalf("ASSERTION: a pane of crew claimed t2, a card without a project, or the refusal does not say why:\n%s", out)
	}
	// A project card: the pane of crew takes it.
	if out := inPane(t, base, "crew", worker, "task claim t1 -s lead"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: the pane of crew could not claim t1 of lead:\n%s", out)
	}
	cards, raw := crossCards(t, base, "lead")
	if h := cards["t1"].Holder; h == nil || h.Window != worker || h.Session != "crew" || h.Name != "worker" {
		t.Fatalf("ASSERTION: t1's holder is not the pane worker %s of crew:\n%s", worker, raw)
	}

	term := attachIn(t, base, "lead", startOpts{cols: 160, rows: 45, shippedLooks: true})
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the holder in another session", "port the parser", "in crew")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-cross-session-160x45")

	// The pane closes: the card is let go.
	mustCLI(t, base, "close-window", "-s", "crew", worker)
	waitUntil(t, "t1 was not let go when its pane in crew closed", uiTimeout, func() (bool, string) {
		cards, raw := crossCards(t, base, "lead")
		return cards["t1"].Holder == nil, raw
	})
	if got := lastRelease(t, base, "lead", "t1"); got != "pane closed" {
		t.Errorf("ASSERTION: t1 was let go with reason %q, want pane closed", got)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "in crew") }, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the board still shows the holder in crew after its pane closed: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-cross-session-let-go-160x45")

	// A second pane of crew takes it, and its agent leaves.
	other := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "crew", "other", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", "crew", "-w", other, "working", "--harness", "claude-code")
	if out := inPane(t, base, "crew", other, "task claim t1 -s lead"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: the second pane of crew could not claim t1:\n%s", out)
	}
	mustCLI(t, base, "set-agent-state", "-s", "crew", "-w", other, "none")
	waitUntil(t, "t1 was not let go when the agent left its pane in crew", 10*time.Second, func() (bool, string) {
		cards, raw := crossCards(t, base, "lead")
		return cards["t1"].Holder == nil, raw
	})
	if got := lastRelease(t, base, "lead", "t1"); got != "agent left" {
		t.Errorf("ASSERTION: t1 was let go with reason %q, want agent left", got)
	}
}
