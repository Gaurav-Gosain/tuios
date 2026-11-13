package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitSpawnJJBookmarks opens the New agent sheet on a jj repository
// with no ".git" and opens the Base list: it offers the local bookmarks, the
// newest commit's first. git finds no branches there at all.
//
// The working copy is a commit on main with no bookmark. The rail names it by
// its description, "wip", which is no revision, so the sheet must not offer it
// as the base, as it offers no base on git's detached HEAD.
//
// Negative control: with spawnBranches asking git for-each-ref alone, the
// Base list has nothing to open. With Detached left unset in jjLabel, the
// sheet shows "wip" as the base.
func TestCockpitSpawnJJBookmarks(t *testing.T) {
	base, _ := fanFixture(t)
	// Only the fake claude: an agent installed on this machine must never
	// start from a test.
	jjBin := t.TempDir()
	if err := os.Symlink(jjPath(t), filepath.Join(jjBin, "jj")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]+string(os.PathListSeparator)+"/usr/bin:/bin:"+jjBin)
	repo := testutil.JJRepo(t, false)
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.JJ(t, repo, "commit", "-m", name)
		testutil.JJ(t, repo, "bookmark", "create", "feat/"+name, "-r", "@-")
	}
	testutil.JJ(t, repo, "new", "main")
	if err := os.WriteFile(filepath.Join(repo, "w"), []byte("w\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// describe also snapshots the file, so @ is not the empty commit the
	// label skips.
	testutil.JJ(t, repo, "describe", "-m", "wip")
	if out, err := tuiosCLI(t, base, "new", "home", "--detach", "--cwd", repo); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the cockpit", "Agents", "New agent")
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the spawn sheet", "Task", "Where it works", "Start agent")
	waitText(t, term, "the folder read", "from "+filepath.Base(repo))
	if text := term.Snapshot(); strings.Contains(text, "wip") {
		t.Fatalf("ASSERTION: the sheet offers the description \"wip\" as the base:\n%s", text)
	}

	// Task, Agent, Model, Effort, Permission, Where, Branch, Base: down on
	// Base opens its list.
	for range 7 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.SendKeys(tuitest.Down); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		b, a, m := strings.Index(text, "feat/b"), strings.Index(text, "feat/a"), strings.LastIndex(text, "main")
		return b >= 0 && a > b && m > a
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Base list never offered feat/b, feat/a and main, newest first: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "spawn-jj-bookmarks")
	alive(t, term, "after listing jj bookmarks")
}

// jjPath is the jj on PATH, for a test that narrows PATH.
func jjPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("jj")
	if err != nil {
		t.Skip("jj is not installed")
	}
	return p
}
