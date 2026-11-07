package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTheBoardShowsTheLinkedMachinesProjectCards opens the board on home,
// which links to build. build owns a project card held by a pane on home, a
// free project card and a card of its session only.
//
// How this could pass wrongly, written down first:
//   - The far card could be home's own card of the same id: home has a t1
//     too, so each row is found by its title and must say "on build".
//   - The session card of build must not show: only project cards leave
//     their machine.
//   - t on a far card could claim home's t1, which has the same session-less
//     id: the test checks home's t1 stays free and the toast says how to
//     take the far one.
//   - L on a far card could move a card of this machine: set-task-stage
//     names a session and an id, not a machine. Both machines' cards are
//     read back after it.
func TestTheBoardShowsTheLinkedMachinesProjectCards(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	remote := remoteMachine(t)
	startLinkedPair(t, base, remote, "far")
	mustCLI(t, remote, "task", "add", "-s", "far", "--project", "git@example.com:acme/api.git", "Fix the retry")
	mustCLI(t, remote, "task", "add", "-s", "far", "--project", "git@example.com:acme/api.git", "Write the docs")
	mustCLI(t, remote, "task", "add", "-s", "far", "Only on build")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", "git@example.com:acme/api.git", "Home card")
	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "worker", "--no-focus", "--print-id"))
	if out, code := cliInPane(t, base, "home", worker, "claim", "task", "claim", "t1", "-s", "build:far"); code != 0 {
		t.Fatalf("claim build's card from home: %s", out)
	}

	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})
	// No agent runs here, so the view has no "Needs you" group yet.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board", "Home card")
	// The columns, the wide layout, name the far machine too.
	waitText(t, term, "the far cards in the columns", "free on build", "worker on build")
	saveCockpitFrames(t, term, "board-far-columns-160x45")
	if err := term.SendKeys("B"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the far cards on the board", "Fix the retry", "Write the docs", "Home card")
	_ = term.WaitStable(uiTimeout)
	s := term.Screen()
	saveCockpitFrames(t, term, "board-far-cards-160x45")
	if fleetRowWith(s, "Only on build") >= 0 {
		t.Errorf("ASSERTION: build's session card shows on home's board\n%s", term.Snapshot())
	}
	for _, c := range []struct{ title, want string }{{"Fix the retry", "worker"}, {"Write the docs", "free on build"}} {
		r := fleetRowWith(s, c.title)
		if r < 0 {
			t.Fatalf("ASSERTION: no row for %q\n%s", c.title, term.Snapshot())
		}
		line := fleetLine(s, r+1)
		if !strings.Contains(line, c.want) || !strings.Contains(line+fleetLine(s, r), "build") {
			t.Errorf("ASSERTION: %q reads %q, want %q and build\n%s", c.title, line, c.want, term.Snapshot())
		}
	}
	if r := fleetRowWith(s, "Home card"); r < 0 || strings.Contains(fleetLine(s, r+1), "build") {
		t.Errorf("ASSERTION: home's own card is missing or says build\n%s", term.Snapshot())
	}

	// t on the free far card says how to take it, and takes nothing here.
	for range 8 {
		if r := fleetRowWith(term.Screen(), "Write the docs"); r >= 0 && strings.Contains(term.Screen().Line(r), "▌") {
			break
		}
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the toast for a far card", "is on build", "-s build:far")
	saveCockpitFrames(t, term, "board-far-card-take-160x45")
	for _, tk := range listTasks(t, base, "home").Tasks {
		if tk.ID == "t1" && tk.Holder != nil {
			t.Errorf("ASSERTION: t on build's card claimed home's t1 for %s", tk.Holder.Window)
		}
	}

	// L on the far card moves no card on either machine.
	if err := term.SendKeys("L"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the toast for a far move", "Move it on that machine")
	for _, m := range []struct{ root, sess string }{{base, "home"}, {remote, "far"}} {
		for _, tk := range listTasks(t, m.root, m.sess).Tasks {
			if want := map[bool]string{true: "doing", false: "todo"}[m.sess == "far" && tk.ID == "t1"]; tk.Stage != want {
				t.Errorf("ASSERTION: L on build's t2 left %s %s in %s, want %s", m.sess, tk.ID, tk.Stage, want)
			}
		}
	}
}
