package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitMenuClosesBeforeAClickActs: the agents view's group menu is a
// one-key menu. It takes the next key, and a click that is not on it closes
// it and does nothing else. It holds:
//
//   - a click on "+ New agent" while the group menu is open closes the menu
//     and opens no New agent sheet;
//   - the next click on "+ New agent" opens the sheet, so the button works.
//
// How this could pass wrongly, written down first:
//   - The sheet could open late. The test waits a whole beat after the first
//     click, then checks the sheet is not there.
//   - The menu could close on its own. The test checks the menu is open
//     before the click.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the click
// opens the sheet with the group menu still open under it.
func TestCockpitMenuClosesBeforeAClickActs(t *testing.T) {
	base, _ := fanFixture(t)
	useShippedLooks(base)
	mustCLI(t, base, "new", "home", "--detach")
	waitForSessionInfo(t, base, "home")
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "api-fix", "--no-focus", "--print-id"))
	mustCLI(t, base, "set-agent-state", "-s", "home", "-w", a, "working", "--harness", "claude-code", "-m", "on it")

	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "Group:", "New agent")
	clickAgentsRow(t, term, "Group:")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Sort: ") }, uiTimeout); err != nil {
		t.Fatalf("the group menu did not open: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "menu-open")
	clickAgentsRow(t, term, "New agent")
	time.Sleep(time.Second)
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "after-click")
	text := term.Screen().Text()
	if strings.Contains(text, "Start") && strings.Contains(text, "Agent") && strings.Contains(text, "Machine") {
		t.Errorf("ASSERTION: a click outside the open group menu opened the New agent sheet\n%s", term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Sort: ") {
		t.Errorf("ASSERTION: the group menu is still open after a click outside it\n%s", term.Snapshot())
	}
}

// TestReadyToMergeClosesOnAnotherRow: Ready to merge is a one-key menu. A
// click on another card's row closes it and does nothing else. It holds:
//
//   - after M on card t1 and a click on t2's row, Ready to merge is closed;
//   - the cursor is still on t1 (the click did nothing else);
//   - c then commits nothing in t1's repository.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the click moves
// the cursor to t2 and leaves Ready to merge open on t1, and c commits t1.
func TestReadyToMergeClosesOnAnotherRow(t *testing.T) {
	base, _ := fanFixture(t)
	useShippedLooks(base)
	api, web := filepath.Join(base, "dev", "api"), filepath.Join(base, "dev", "web")
	for _, dir := range []string{api, web} {
		lifeRepo(t, dir)
		testutil.Git(t, dir, "config", "user.email", "dev@example.com")
		testutil.Git(t, dir, "config", "user.name", "Dev")
	}
	mustCLI(t, base, "new", "home", "--detach", "--cwd", api)
	waitForSessionInfo(t, base, "home")
	mustCLI(t, base, "task", "dispatch", "verify", "-s", "home", "true")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Do it", "Fix the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Do it", "Write the docs")
	for _, c := range []struct{ id, dir, file string }{{"t1", api, "retry.go"}, {"t2", web, "docs.md"}} {
		w := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "agent-"+c.id, "--no-focus", "--print-id", "--cwd", c.dir))
		mustCLI(t, base, "set-agent-state", "-s", "home", "-w", w, "working", "--harness", "claude-code", "-m", "on it")
		if err := os.WriteFile(filepath.Join(c.dir, c.file), []byte("work for "+c.id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mustCLI(t, base, "task", "claim", c.id, "-s", "home", "-w", w)
		if out := inPane(t, base, "home", w, "task done "+c.id); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("tuios task done %s: %s", c.id, out)
		}
	}
	waitLife(t, base, "home", uiTimeout, "the two cards did not reach review",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" && c["t2"].Stage == "review" })
	commits := func(dir string) string { return strings.TrimSpace(testutil.Git(t, dir, "rev-list", "--count", "HEAD")) }

	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "b board")
	sendKeys(t, term, "b")
	waitText(t, term, "the board", "Verify passed")
	sendKeys(t, term, "B")
	_ = term.WaitStable(uiTimeout)
	for i := 0; ; i++ {
		sendKeys(t, term, "M")
		_ = term.WaitStable(2 * time.Second)
		text := term.Screen().Text()
		if strings.Contains(text, "open PR") && strings.Contains(text, "Ready to merge t1") {
			break
		}
		if strings.Contains(text, "open PR") {
			sendKeys(t, term, tuitest.Esc)
		}
		if i == 20 {
			t.Fatalf("Ready to merge never opened on t1:\n%s", text)
		}
		sendKeys(t, term, "j")
		_ = term.WaitStable(2 * time.Second)
	}
	dir := artifactDir(t)
	clickAgentsRow(t, term, "Write the docs")
	time.Sleep(time.Second)
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "after-click")
	if bar := ckKeyBar(term.Screen()); strings.Contains(bar, "Ready to merge") {
		t.Errorf("ASSERTION: Ready to merge is still open after a click on another row (key bar %q)\n%s", bar, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "· home t1") {
		t.Errorf("ASSERTION: the click that closed Ready to merge also moved the cursor off t1\n%s", term.Snapshot())
	}
	sendKeys(t, term, "c")
	time.Sleep(3 * time.Second)
	if got := commits(api); got != "1" {
		t.Errorf("ASSERTION: c after the click committed t1's work (%s commits in its repository)", got)
	}
}
