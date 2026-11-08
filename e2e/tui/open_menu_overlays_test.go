package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// openMenuOverlaysConfig binds open_menu in the Inbox and in the rail, the
// way issue #1590 asks: a key of the overlay opens a menu, and the menu's
// keys run the overlay's own actions.
const openMenuOverlaysConfig = `[keybindings.inbox]
"open_menu:inbox_x" = ["x"]

[keybindings.menus.inbox_x]
title = "Inbox x"

[keybindings.menus.inbox_x.keys]
inbox_close = ["c"]

[keybindings.sidebar]
"open_menu:rail_g" = ["z"]

[keybindings.menus.rail_g]
title = "Rail g"

[keybindings.menus.rail_g.keys]
new_window = ["w"]
`

// TestOpenMenuInTheInboxAndTheRail: open_menu bound in [keybindings.inbox]
// and [keybindings.sidebar] opens its menu over the overlay, with the menu's
// panel on screen (which-key draws a title in lower case), and a key of the menu runs the overlay's own action: c
// closes the Inbox (inbox_close), w opens a window from the rail
// (new_window). Esc closes a menu and leaves the overlay as it was, and the
// same key pressed then does what the overlay binds it to (c is the Inbox's
// compare, w nothing in the rail), so no action is left waiting for a later
// key.
//
// How it could pass wrongly: the key could close the Inbox by itself (an
// unbound key closing the overlay), so the Inbox must stay open while the menu
// is up and after Esc, and close only on the menu's key.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestOpenMenuInTheInboxAndTheRail,
// TestOpenMenuInTheReview).
func TestOpenMenuInTheInboxAndTheRail(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, openMenuOverlaysConfig)
	const session = "e2e-menus"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("not in window mode: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: go test ./..."); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	press := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("press %v: %v", keys, err)
		}
		time.Sleep(insertGuard)
	}
	inboxOpen := func() bool { return strings.Contains(term.Screen().Text(), "1 needs you") }
	openInbox := func() {
		t.Helper()
		press(tuitest.Ctrl('b'), "i")
		if err := term.WaitForText("1 needs you", uiTimeout); err != nil {
			t.Fatalf("the Inbox never opened: %v\n%s", err, term.Snapshot())
		}
		_ = term.WaitStable(uiTimeout)
	}

	// The Inbox: x opens the menu over it.
	openInbox()
	press("x")
	if err := term.WaitForText("inbox x", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x in the Inbox did not open the menu: %v\n%s", err, term.Snapshot())
	}
	if !inboxOpen() {
		t.Fatalf("ASSERTION: opening the menu closed the Inbox\n%s", term.Snapshot())
	}
	saveFrame(t, term, "inbox-menu")
	// Esc closes the menu, and the Inbox stays.
	press(tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "inbox x") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the menu: %v\n%s", err, term.Snapshot())
	}
	if !inboxOpen() {
		t.Fatalf("ASSERTION: esc out of the menu closed the Inbox\n%s", term.Snapshot())
	}
	// c now is the Inbox's own key (compare), not the menu's inbox_close.
	press("c")
	_ = term.WaitStable(uiTimeout)
	if !inboxOpen() {
		t.Fatalf("ASSERTION: c after esc out of the menu closed the Inbox: a menu action was left waiting\n%s", term.Snapshot())
	}
	press(tuitest.Esc)
	_ = term.WaitStable(uiTimeout)
	if !inboxOpen() {
		openInbox()
	}
	// x then c: the menu's inbox_close.
	press("x")
	if err := term.WaitForText("inbox x", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x did not open the menu a second time: %v\n%s", err, term.Snapshot())
	}
	press("c")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "1 needs you") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x c did not close the Inbox: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-closed-by-menu")
	// The hand-back was used by that one key: c in the Inbox opened again is
	// compare once more, and leaves the Inbox open.
	openInbox()
	press("c")
	_ = term.WaitStable(uiTimeout)
	if !inboxOpen() {
		t.Fatalf("ASSERTION: c in the Inbox opened again closed it: the menu's action answered a later key\n%s", term.Snapshot())
	}
	press(tuitest.Esc)
	_ = term.WaitStable(uiTimeout)
	if inboxOpen() {
		press(tuitest.Esc)
		_ = term.WaitStable(uiTimeout)
	}

	// The rail: z opens its menu, w opens a window.
	windows := func() int {
		out := mustCLI(t, base, "list-windows", "-s", session, "--json")
		var res struct {
			Windows []json.RawMessage `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		return len(res.Windows)
	}
	before := windows()
	press(tuitest.Ctrl('b'), "e")
	_ = term.WaitStable(uiTimeout)
	press("z")
	if err := term.WaitForText("rail g", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: z in the rail did not open the menu: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-menu")
	press(tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "rail g") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the rail's menu: %v\n%s", err, term.Snapshot())
	}
	press("w")
	_ = term.WaitStable(uiTimeout)
	if n := windows(); n != before {
		t.Fatalf("ASSERTION: w after esc out of the rail's menu opened a window (%d, was %d): a menu action was left waiting", n, before)
	}
	// The esc may have left the rail; focus it again if so.
	press("z")
	if err := term.WaitForText("rail g", uiTimeout); err != nil {
		press(tuitest.Ctrl('b'), "e")
		_ = term.WaitStable(uiTimeout)
		press("z")
		if err := term.WaitForText("rail g", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: z did not open the rail's menu a second time: %v\n%s", err, term.Snapshot())
		}
	}
	press("w")
	deadline := time.Now().Add(uiTimeout)
	for windows() != before+1 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if n := windows(); n != before+1 {
		t.Fatalf("ASSERTION: z w in the rail did not open a window (%d, was %d)\n%s", n, before, term.Snapshot())
	}
	saveFrame(t, term, "rail-window-by-menu")
}

// openMenuReviewConfig binds open_menu in the review, which reads keys
// ahead of the menus that every other overlay leaves to them.
const openMenuReviewConfig = `[keybindings.review]
"open_menu:stage" = ["m"]

[keybindings.menus.stage]
title = "Quick stage"

[keybindings.menus.stage.keys]
review_stage_file = ["y"]
`

// TestOpenMenuInTheReview: open_menu in [keybindings.review] opens its menu
// over the review, and the menu's y stages the file (review_stage_file). esc
// closes the menu and leaves the review open, and y pressed then, a key the
// review does not bind, stages nothing.
//
// How it could pass wrongly: the review reads its keys before the menus do,
// so m could open the menu while the review takes y as its own. The index is
// read with git, and must be empty after esc and y, and hold the file after
// m y.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestOpenMenuInTheReview).
func TestOpenMenuInTheReview(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, openMenuReviewConfig)
	repo := testutil.GitRepo(t)
	file := filepath.Join(repo, "list.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "list.txt")
	testutil.Git(t, repo, "commit", "-q", "-m", "list")
	if err := os.WriteFile(file, []byte("one\nTWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := func() string { return strings.TrimSpace(testutil.Git(t, repo, "diff", "--cached", "--name-only")) }
	if out, err := tuiosCLIIn(t, base, repo, "new", "rs", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	term := attachIn(t, base, "rs", startOpts{})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "list.txt", "esc close")
	_ = term.WaitStable(uiTimeout)

	sendKeys(t, term, "m")
	if err := term.WaitForText("quick stage", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: m in the review did not open the menu: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "review-menu")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "quick stage") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the review's menu: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "esc close") {
		t.Fatalf("ASSERTION: esc out of the menu closed the review\n%s", term.Snapshot())
	}
	sendKeys(t, term, "y")
	_ = term.WaitStable(uiTimeout)
	if got := staged(); got != "" {
		t.Fatalf("ASSERTION: y after esc out of the menu staged %q: a menu action was left waiting", got)
	}
	sendKeys(t, term, "m", "y")
	deadline := time.Now().Add(uiTimeout)
	for staged() != "list.txt" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if got := staged(); got != "list.txt" {
		t.Fatalf("ASSERTION: m y in the review did not stage the file (index holds %q)\n%s", got, term.Snapshot())
	}
	saveFrame(t, term, "review-staged-by-menu")
}
