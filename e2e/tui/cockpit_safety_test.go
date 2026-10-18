package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The agents view's safety rules, each read off the screen the way a person
// reads it: a bulk key acts only on marks the person can see, and an
// approval whose command the card cannot show whole is not answered there.

// TestCockpitBulkActsOnlyOnSeenMarks marks the agent that holds an approval.
// The mark shows on its row and the title counts it. With its group folded
// the row is gone from the list, and 3, the bulk reject, rejects nothing: the
// hook still holds the approval.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the mark not
// drawn, the wait for it fails; with markedAgents reading the whole agents
// list instead of the rows the fleet shows, 3 rejects the folded agent's
// approval and the hook prints the reject.
func TestCockpitBulkActsOnlyOnSeenMarks(t *testing.T) {
	// The mark in the Nerd Font set, which the client draws by default.
	const markGlyph = "\uf14a"
	term, base, hookOut, _ := startCockpitFleet(t, 200, 55, nil)
	// The cursor opens on the oldest wait, api-auth's approval.
	if err := term.SendKeys("x"); err != nil {
		t.Fatalf("x: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := fleetRowWith(s, "api-auth")
		return row >= 0 && strings.Contains(s.Line(row), markGlyph) && strings.Contains(s.Text(), "1 marked")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the mark is not on api-auth's row, or the title does not count it: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-marked")

	// A click on the Needs you heading folds the group, and the marked row
	// with it.
	head := fleetRowWith(term.Screen(), "Needs you")
	mouseClick(t, term, 4, head, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return fleetRowWith(s, "api-auth") < 0 }, uiTimeout); err != nil {
		t.Fatalf("the fold did not hide the marked row: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(answerSettle)
	if err := term.SendKeys("3"); err != nil {
		t.Fatalf("3: %v", err)
	}
	if err := term.WaitForText("No marked agent waits on an approval", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: 3 did not say no marked agent it can see waits: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(answerSettle)
	if out := hookOut.String(); strings.Contains(out, "deny") {
		t.Fatalf("ASSERTION: the bulk reject reached an approval whose row was folded away: %s", out)
	}
	if n := heldApprovals(t, base, "api"); n != 1 {
		t.Fatalf("ASSERTION: the hidden approval is not held any more: %d held", n)
	}
	if strings.Contains(term.Screen().Text(), "1 marked") {
		t.Fatalf("ASSERTION: the title counts a mark the list does not show\n%s", term.Snapshot())
	}
	alive(t, term, "after a bulk key with every mark out of sight")
}

// TestCockpitCutApprovalIsNotAnswered holds an approval on a screen too
// short for its card: the command and where it runs do not fit. The card
// says it is too long to show, no answer keys are offered, a and 1 answer
// nothing, and the hints row under the card is still drawn whole. The
// daemon holds only a command its summary carries whole, so the command is
// short and the screen is what cuts it.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with the card counting
// a cut command as shown, a allows the approval and the held count drops.
func TestCockpitCutApprovalIsNotAnswered(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	payload := `{"hook_event_name":"PermissionRequest","session_id":"e2e-cut","tool_name":"Bash","tool_input":{"command":` +
		`"go test ./internal/auth/... ./internal/session/... -run TokenRefresh -count=1"}}`
	startHookIn(t, base, "api", "0", payload)

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 100, rows: 12, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "1 needs you", "Too long to show here", "esc close")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the card does not say the command is too long to show: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	// The card stays inside the body: its last line is above the hints row.
	if cut, hints := rowWith(s, "Too long to show here"), rowWith(s, "esc close"); cut < 0 || hints <= cut {
		t.Fatalf("ASSERTION: the card runs into the hints row (card %d, hints %d)\n%s", cut, hints, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-cut-approval")
	time.Sleep(answerSettle)
	for _, key := range []string{"a", "1"} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		time.Sleep(answerSettle)
		if n := heldApprovals(t, base, "api"); n != 1 {
			t.Fatalf("ASSERTION: %s answered an approval whose command was cut: %d held", key, n)
		}
		if err := term.WaitForText("cannot be shown whole here", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s on a cut approval did not say why it does nothing: %v\n%s", key, err, term.Snapshot())
		}
	}
	if text := term.Screen().Text(); strings.Contains(text, "a allow") || strings.Contains(text, "Allow once") {
		t.Fatalf("ASSERTION: a cut approval still offers its answers\n%s", term.Snapshot())
	}
	alive(t, term, "after keys on a cut approval")
}

// TestCockpitApprovalCardOnEveryTab holds an approval on a screen narrower
// than the side column, where checkpoints and the live pane are focus tabs.
// On the Checkpoints tab and on the Live tab the approval's card is still
// drawn with its buttons, and a answers it from there.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with the tabs drawn
// in place of the card, the card is gone on the Checkpoints tab.
func TestCockpitApprovalCardOnEveryTab(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	startHookIn(t, base, "api", "0", `{"hook_event_name":"PermissionRequest","session_id":"e2e-tabs","tool_name":"Bash","tool_input":{"command":"go test ./internal/auth/... -run Token"}}`)

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 100, rows: 30, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the approval on the Thread tab", "Allow Bash?", "Allow once", "Checkpoints", "Live")
	// ] steps the tabs: Thread, Changes, Checkpoints, Live. Each tab's own
	// body says it is drawn, and the card is under it.
	for _, tab := range []struct{ name, body string }{
		{"Checkpoints", "Checkpoints need the pane in a git work tree."},
		{"Live", "╭"},
	} {
		found := false
		for range 4 {
			if err := term.SendKeys("]"); err != nil {
				t.Fatal(err)
			}
			if term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), tab.body) }, 2*time.Second) == nil {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the %s tab never drew\n%s", tab.name, term.Snapshot())
		}
		if s := term.Screen(); !screenHas(s, "Allow Bash?", "Allow once", "$ go test") {
			t.Fatalf("ASSERTION: the %s tab hides the approval's card\n%s", tab.name, term.Snapshot())
		}
	}
	saveCockpitFrames(t, term, "approval-on-live-tab")
	time.Sleep(answerSettle)
	if err := term.SendKeys("a"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for heldApprovals(t, base, "api") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: a on the Live tab did not answer the approval its card shows\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after answering from the Live tab")
}
