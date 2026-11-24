package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestReviewWhileAttachedThroughAHost opens the review (prefix v) on a pane of
// a session on a linked machine, alpha, from a client attached there (#2426).
// The pane's repository is on alpha, so alpha's daemon reads the diff.
//
// The ways it can go wrong, each an assertion: the review refuses because the
// session is on another machine; the diff is asked of this machine's daemon,
// which has no such pane and says so; the overlay opens without the change.
func TestReviewWhileAttachedThroughAHost(t *testing.T) {
	f := startGlobalFleet(t)
	repo := filepath.Join(f.alpha, "farproj")
	lifeRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "far_note.txt"), []byte("a change made on alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "new-window", "-s", "far", "--cwd", repo, "far-repo"); err != nil {
		t.Fatalf("open a pane in the repository on alpha: %v\n%s", err, out)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if out, err := cliWithin(t, f.alpha, f.env, "focus-window", "-s", "far", "far-repo"); err != nil {
		t.Fatalf("focus far-repo on alpha: %v\n%s", err, out)
	}
	repoPane := alphaWindowID(t, f, "far-repo")
	if err := term.WaitFor(func(_ tuitest.Screen) bool { return alphaFocused(t, f) == repoPane }, uiTimeout); err != nil {
		t.Fatalf("far-repo never took the focus: %v", err)
	}
	_ = term.WaitStable(uiTimeout)

	if err := term.SendKeys(tuitest.Ctrl('b'), "v"); err != nil {
		t.Fatalf("press ctrl+b v: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "far_note.txt") && strings.Contains(text, "a change made on alpha")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "attached-review-missing")
		t.Fatalf("ASSERTION: the review of the pane on alpha did not show its change: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "Review it on that machine") || strings.Contains(text, "No git repository") {
		t.Fatalf("ASSERTION: the review was refused or read on this machine:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "attached-review-open")

	// A note goes to alpha's daemon too, as the person, over the link.
	if err := term.SendKeys("j", "c"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the note line", "save", "drop")
	if err := term.SendKeys("say where it runs"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the note under its line", "note: say where it runs", "1 note")
	if out, _ := cliWithin(t, f.alpha, f.env, "review", "-s", "far", "-w", "far-repo"); !strings.Contains(out, "say where it runs") {
		t.Fatalf("ASSERTION: alpha's daemon has no note from the review:\n%s", out)
	}
	saveCockpitFrames(t, term, "attached-review-note")

	// The open review keeps one connection to alpha and calls nothing while
	// nothing changes: no new client comes over the link while it sits open.
	// A call over the link is a client of the link's process on alpha. The
	// CLI that lists the clients is one too, of its own process, so only the
	// link's processes count.
	before := alphaClients(t, f)
	link := map[int]bool{}
	for _, pid := range before {
		link[pid] = true
	}
	stayTrue(t, "the open review dials alpha again while nothing changes", 3*time.Second, func() (bool, string) {
		for id, pid := range alphaClients(t, f) {
			if _, seen := before[id]; !seen && link[pid] {
				return false, "new client over the link: " + id
			}
		}
		return true, ""
	})
	alive(t, term, "after the review on alpha")
}

// alphaClients is alpha's clients, each id with its process id.
func alphaClients(t *testing.T, f *globalFleet) map[string]int {
	t.Helper()
	out, err := cliWithin(t, f.alpha, f.env, "list-clients", "--json")
	if err != nil {
		t.Fatalf("list-clients on alpha: %v\n%s", err, out)
	}
	var list []struct {
		ID  string `json:"client_id"`
		PID int    `json:"pid"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		var wrapped struct {
			Clients []struct {
				ID  string `json:"client_id"`
				PID int    `json:"pid"`
			} `json:"clients"`
		}
		if json.Unmarshal([]byte(out), &wrapped) != nil {
			t.Fatalf("list-clients --json on alpha printed %q", out)
		}
		list = wrapped.Clients
	}
	clients := map[string]int{}
	for _, c := range list {
		clients[c.ID] = c.PID
	}
	return clients
}
