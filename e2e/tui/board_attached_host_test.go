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

// TestTheBoardShowsTheAttachedHostsCards opens the board on a client
// attached to session far on alpha through a host link (#2306). The board is
// far's: alpha's session card and project card show, and staging a card or
// turning dispatch on acts on alpha's daemon. The focused pane on alpha is in
// a checkout of the project card's repository, so the board's dispatch is
// that project's, as it is for tuios run on alpha.
//
// How this could pass wrongly, written down first:
//   - The board could show this machine's cards: home has a card of its own,
//     which must not show, and alpha's cards are found by their titles.
//   - L could move this machine's t1, which has the same id as alpha's: both
//     machines' cards are read back after it, each with the CLI on its own
//     machine.
//   - The dispatch header could change with nothing on alpha: tuios task
//     dispatch on alpha reads the mode back, for the project's board and not
//     the session's, which would never dispatch the project card.
//
// Negative control (NEGATIVE_CONTROLS.md): with the board's acts sent to
// this machine's daemon, L leaves alpha's t1 in Todo.
func TestTheBoardShowsTheAttachedHostsCards(t *testing.T) {
	f := startGlobalFleet(t)
	repo := filepath.Join(f.alpha, "api")
	lifeRepo(t, repo)
	gitIn(t, repo, "remote", "add", "origin", "git@example.com:acme/api.git")
	for _, args := range [][]string{
		{"new-window", "-s", "far", "--cwd", repo, "api"},
		{"task", "add", "-s", "far", "Far session card"},
		{"task", "add", "-s", "far", "--project", "git@example.com:acme/api.git", "Far project card"},
	} {
		if out, err := cliWithin(t, f.alpha, f.env, args...); err != nil {
			t.Fatalf("%v on alpha: %v\n%s", args, err, out)
		}
	}
	if out, err := cliWithin(t, f.here, f.env, "task", "add", "-s", "home", "Home only card"); err != nil {
		t.Fatalf("add home's card: %v\n%s", err, out)
	}

	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 160, rows: 45,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Far session card", "Far project card", "Dispatch Manual")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "board-attached-host-empty-160x45")
		t.Fatalf("ASSERTION: the board does not show far's cards on alpha and its dispatch: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-attached-host-columns-160x45")
	if strings.Contains(term.Screen().Text(), "Home only card") {
		t.Fatalf("ASSERTION: the board attached to alpha shows this machine's card\n%s", term.Snapshot())
	}

	// The list layout, with the cursor on alpha's session card.
	if err := term.SendKeys("B"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board as a list", "Far session card")
	for range 8 {
		if r := fleetRowWith(term.Screen(), "Far session card"); r >= 0 && strings.Contains(term.Screen().Line(r), "▌") {
			break
		}
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	if err := term.SendKeys("L"); err != nil {
		t.Fatal(err)
	}
	stages := func(base, sess string) map[string]string {
		out, err := cliWithin(t, base, f.env, "task", "ls", "-s", sess, "--json")
		if err != nil {
			return nil
		}
		var l taskListing
		if json.Unmarshal([]byte(out), &l) != nil {
			return nil
		}
		got := map[string]string{}
		for _, tk := range l.Tasks {
			got[tk.ID] = tk.Stage
		}
		return got
	}
	waitUntil(t, "L on alpha's card did not move it on alpha", uiTimeout, func() (bool, string) {
		got := stages(f.alpha, "far")
		return got["t1"] == "plan", "alpha's stages " + toJSON(got) + "\n" + term.Snapshot()
	})
	if got := stages(f.here, "home"); got["t1"] != "todo" {
		t.Fatalf("ASSERTION: L on alpha's card moved this machine's card: %v", got)
	}
	saveCockpitFrames(t, term, "board-attached-host-moved-160x45")

	// ctrl+t turns far's board on alpha to pull.
	if err := term.SendKeys(tuitest.Ctrl('t')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the board in pull mode", "Dispatch Pull")
	if out, _ := cliWithin(t, f.alpha, f.env, "task", "dispatch"); !strings.Contains(out, "pull   example.com/acme/api") || strings.Contains(out, "pull   session:far") {
		t.Fatalf("ASSERTION: ctrl+t did not set the project's board on alpha to pull, and only it:\n%s", out)
	}
	alive(t, term, "after acting on alpha's board")
}

// TestTheBoardFollowsTheClientToAnotherHost opens the board on this
// machine's session, which shows home's card and the linked machines'
// project cards, then moves the client to far on alpha from the rail, and
// back. On alpha the board is alpha's cards alone, with alpha's dispatch;
// back home it is home's card and the linked machines' project cards again.
//
// How this could pass wrongly, written down first:
//   - The cards the board read on home could stay: alpha's project card
//     would then show twice, once as alpha's own and once as a far card on
//     alpha, and beta's project card would show while attached to alpha.
//     Each is counted.
//   - The board could keep home's dispatch: home's daemon is paused first,
//     so the header on alpha must not say Paused.
func TestTheBoardFollowsTheClientToAnotherHost(t *testing.T) {
	f := startGlobalFleet(t)
	for _, c := range []struct{ base, sess, project, title string }{
		{f.alpha, "far", "git@example.com:acme/api.git", "Alpha project card"},
		{f.beta, "far", "git@example.com:acme/api.git", "Beta project card"},
		{f.here, "home", "", "Home only card"},
	} {
		args := []string{"task", "add", "-s", c.sess, c.title}
		if c.project != "" {
			args = []string{"task", "add", "-s", c.sess, "--project", c.project, c.title}
		}
		if out, err := cliWithin(t, c.base, f.env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := cliWithin(t, f.here, f.env, "task", "dispatch", "pause"); err != nil {
		t.Fatalf("pause home's dispatch: %v\n%s", err, out)
	}

	term := startIn(t, f.here, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to home: %v\n%s", err, term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "alpha")
	openBoard := func(what string, want ...string) {
		t.Helper()
		windowManagementMode(t, term)
		if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return containsAll(s.Text(), want...) }, uiTimeout); err != nil {
			saveCockpitFrames(t, term, "board-host-switch-"+strings.ReplaceAll(what, " ", "-"))
			t.Fatalf("ASSERTION: %s does not show %q: %v\n%s", what, want, err, term.Snapshot())
		}
		_ = term.WaitStable(uiTimeout)
	}
	closeView := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Dispatch ") }, uiTimeout); err != nil {
			t.Fatalf("esc did not close the agents view: %v\n%s", err, term.Snapshot())
		}
	}

	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "home's board", "Home only card", "Alpha project card", "Beta project card", "Paused")
	saveCockpitFrames(t, term, "board-host-switch-home-160x45")
	closeView()

	clickRailSessionUnder(t, term, "alpha", "far")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railMarksUnder(s, "alpha", "far") }, uiTimeout); err != nil {
		t.Fatalf("the client never moved to far on alpha: %v\n%s", err, term.Snapshot())
	}
	openBoard("the board on alpha", "Alpha project card", "Dispatch Manual")
	s := term.Screen().Text()
	saveCockpitFrames(t, term, "board-host-switch-alpha-160x45")
	if n := strings.Count(s, "Alpha project card"); n != 1 || strings.Contains(s, "Beta project card") || strings.Contains(s, "Home only card") || strings.Contains(s, "Paused") {
		t.Fatalf("ASSERTION: the board on alpha shows alpha's card %d times (want 1), a card of beta or home, or home's pause\n%s", n, term.Snapshot())
	}
	closeView()

	clickRailSessionUnder(t, term, "local", "home")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railMarksUnder(s, "local", "home") }, uiTimeout); err != nil {
		t.Fatalf("the client never came back to home: %v\n%s", err, term.Snapshot())
	}
	openBoard("the board back home", "Home only card", "Alpha project card", "Beta project card", "Paused")
	saveCockpitFrames(t, term, "board-host-switch-back-160x45")
	alive(t, term, "after the board followed the client between machines")
}

// TestTheBoardStartsAnAgentOnTheAttachedHostsCard presses enter on a free
// card of the board of a client attached to far on alpha, and starts the
// agent the sheet offers. The card is alpha's, so the sheet picks alpha, the
// agent starts in far there, and it holds the card.
//
// How this could pass wrongly, written down first:
//   - The agent could start on this machine, which has no card t1: the
//     holder is read back on alpha and must be a window of far on alpha.
//   - The card could be claimed by something else than the start: no other
//     pane runs an agent, and the holder must be the agent's window.
func TestTheBoardStartsAnAgentOnTheAttachedHostsCard(t *testing.T) {
	requireReal(t, "the agent's program is found on the PATH of alpha's daemon, a process of its own")
	fake := t.TempDir()
	mark, argv, prompt := filepath.Join(fake, "signed-in"), filepath.Join(fake, "argv"), filepath.Join(fake, "prompt")
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("__MARK__", mark, "__ARGV__", argv, "__PROMPT__", prompt, "__TUIOS__", tuiosBin).Replace(fakeSheetClaude)
	if err := os.WriteFile(filepath.Join(fake, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Every daemon of the fleet starts with this PATH, alpha's too.
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := startGlobalFleet(t)
	if out, err := cliWithin(t, f.alpha, f.env, "task", "add", "-s", "far", "-p", "Make the far retry back off.", "Far session card"); err != nil {
		t.Fatalf("add alpha's card: %v\n%s", err, out)
	}

	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 160, rows: 45,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "alpha's card on the board", "Far session card")
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the sheet with the agent ready", "Start agent", "claude ready")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-attached-host-sheet-160x45")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}

	holder := ""
	waitUntil(t, "the agent started from the board never held alpha's t1", 30*time.Second, func() (bool, string) {
		out, err := cliWithin(t, f.alpha, f.env, "task", "ls", "-s", "far", "--json")
		if err != nil {
			return false, out
		}
		var l taskListing
		if json.Unmarshal([]byte(out), &l) != nil {
			return false, out
		}
		for _, tk := range l.Tasks {
			if tk.ID == "t1" && tk.Holder != nil {
				holder = tk.Holder.Window
			}
		}
		return holder != "", out + "\n" + term.Snapshot()
	})
	hosts := sessionWindowHosts(t, f.alpha, f.env, "far")
	if _, ok := hosts[holder]; !ok {
		t.Fatalf("ASSERTION: t1 is held by %s, not a window of far on alpha: %v", holder, hosts)
	}
	waitText(t, term, "the card in Doing, held by the new agent", "Doing  1", "far-retry")
	saveCockpitFrames(t, term, "board-attached-host-started-160x45")
	alive(t, term, "after starting an agent on alpha's card")
}
