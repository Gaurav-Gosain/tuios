package tuie2e

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestGotoActsOnASessionOnAHost deletes and renames a session on a linked
// machine, alpha, from the go to list (prefix S) of a client attached to a
// session on this machine (#2416). The session is not the attached one, so
// nothing asks the person to switch to alpha first.
//
// The ways it can go wrong, each an assertion: ctrl+d or ctrl+r says "Switch
// to alpha"; the delete asks and then goes to this machine's daemon, which
// has no such session, so alpha still lists it; the rename does the same.
func TestGotoActsOnASessionOnAHost(t *testing.T) {
	f := startGlobalFleet(t)
	for _, name := range []string{"spare", "old"} {
		if out, err := cliWithin(t, f.alpha, f.env, "new", name, "--detach"); err != nil {
			t.Fatalf("start %s on alpha: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached home: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	// Delete spare on alpha.
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open go to: %v", err)
	}
	waitText(t, term, "the go to list", "Go to")
	if err := term.SendKeys("spare"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "alpha's session spare", "spare @ alpha")
	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Delete session spare @ alpha? y yes, n no", uiTimeout); err != nil {
		saveCockpitFrames(t, term, "goto-delete-refused")
		t.Fatalf("ASSERTION: ctrl+d on alpha's session asked nothing: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "goto-delete-ask")
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(_ tuitest.Screen) bool { return !slices.Contains(alphaSessions(t, f), "spare") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: alpha still lists spare after the delete: %v\n%s", err, term.Snapshot())
	}
	waitText(t, term, "the delete's result", "Killed session")
	saveCockpitFrames(t, term, "goto-delete-done")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)

	// Rename old on alpha to old-two.
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open go to: %v", err)
	}
	waitText(t, term, "the go to list", "Go to")
	if err := term.SendKeys("old"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "alpha's session old", "old @ alpha")
	if err := term.SendKeys(tuitest.Ctrl('r')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("rename session old on alpha", uiTimeout); err != nil {
		saveCockpitFrames(t, term, "goto-rename-refused")
		t.Fatalf("ASSERTION: ctrl+r on alpha's session opened no rename: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("-two", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(_ tuitest.Screen) bool {
		names := alphaSessions(t, f)
		return slices.Contains(names, "old-two") && !slices.Contains(names, "old")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: alpha does not list old-two after the rename: %v\n%v\n%s", err, alphaSessions(t, f), term.Snapshot())
	}
	if !slices.Contains(attachedSessions(f.here), "home") {
		t.Fatalf("ASSERTION: the client left home on this machine")
	}
	alive(t, term, "after the delete and rename on alpha")
}

// TestGotoNamesTheGrantAHostRefuses renames and deletes a session on alpha
// from go to when alpha's link policy for this machine allows only list. The
// session stays as it was, and each refusal names the grant and where to add
// it.
//
// The ways it can go wrong, each an assertion: the refusal is the bare error
// of the call, which names no grant; the rename or the delete went through.
func TestGotoNamesTheGrantAHostRefuses(t *testing.T) {
	f := startGlobalFleetWith(t, map[string]string{"beta": "someone@betabox"}, "[hosts.\"*\"]\nallow = [\"list\"]\n")
	if out, err := cliWithin(t, f.alpha, f.env, "new", "old", "--detach"); err != nil {
		t.Fatalf("start old on alpha: %v\n%s", err, out)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached home: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	openOld := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
			t.Fatalf("open go to: %v", err)
		}
		waitText(t, term, "the go to list", "Go to")
		if err := term.SendKeys("old"); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, "alpha's session old", "old @ alpha")
	}

	openOld()
	if err := term.SendKeys(tuitest.Ctrl('r')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the rename", "rename session old on alpha")
	if err := term.SendKeys("-two", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Rename failed: Host alpha does not allow", uiTimeout); err != nil {
		saveCockpitFrames(t, term, "goto-rename-refusal-bare")
		t.Fatalf("ASSERTION: the refused rename does not name the grant: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "goto-rename-refused")
	if !slices.Contains(alphaSessions(t, f), "old") {
		t.Fatalf("ASSERTION: alpha renamed old though its policy refuses: %v", alphaSessions(t, f))
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)

	openOld()
	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the delete question", "Delete session old @ alpha? y yes, n no")
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Kill failed: Host alpha does not allow", uiTimeout); err != nil {
		saveCockpitFrames(t, term, "goto-delete-refusal-bare")
		t.Fatalf("ASSERTION: the refused delete does not name the grant: %v\n%s", err, term.Snapshot())
	}
	if !slices.Contains(alphaSessions(t, f), "old") {
		t.Fatalf("ASSERTION: alpha deleted old though its policy refuses: %v", alphaSessions(t, f))
	}
	alive(t, term, "after the refused rename and delete")
}

// alphaSessions is the names of alpha's sessions, nil when they cannot be read.
func alphaSessions(t *testing.T, f *globalFleet) []string {
	t.Helper()
	out, err := cliWithin(t, f.alpha, f.env, "ls", "--json")
	if err != nil {
		return nil
	}
	var sessions []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(out), &sessions) != nil {
		return nil
	}
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	return names
}
