package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's worktree groups, end to end: a throwaway repository, two
// worktree sessions made by the command a person runs, a real daemon, and a
// real client drawing the rail. Nothing here touches any repository but the
// one testutil.GitRepo makes under the test.

// tuiosCLIIn is tuiosCLI with a working directory of its own. It exists for
// the one command whose directory decides what the daemon records: this suite
// runs inside a git worktree of tuios itself, so a session created from here
// is a worktree session and cannot be the control.
func tuiosCLIIn(t *testing.T, base, dir string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// railLineWith is the screen row holding needle, or -1. The rail is at the top
// of the screen, so the topmost match is the rail's row.
func railLineWith(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), needle) {
			return r
		}
	}
	return -1
}

// railCellColumn is the screen column of needle on its topmost line, or -1. It
// counts runes because the pill's cap and the tree bars take several bytes and
// one cell each.
func railCellColumn(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if c := strings.Index(line, needle); c >= 0 {
			return utf8.RuneCountInString(line[:c])
		}
	}
	return -1
}

// TestSidebarGroupsWorktrees drives the whole promise the way a person meets
// it: two worktree sessions of one repository appear under one row named after
// the repository, labelled by their branches, and a click on that row folds
// them away and says how many it is holding.
//
// The name is short on purpose. The harness roots XDG_RUNTIME_DIR at
// t.TempDir(), whose path carries the test's name, and the daemon's unix
// socket under it has 108 bytes to live in.
func TestSidebarGroupsWorktrees(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	// The worktrees land under the test's own directory. tuiosCLI passes this
	// process's environment through, so the daemon it starts sees it too.
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// A session that is not a worktree, which is the control on screen and the
	// one this client attaches to.
	// It is created from a directory that is in no repository, and it starts
	// the daemon, so the daemon's own directory is that one too.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	for _, branch := range []string{"feat/one", "feat/two"} {
		out, err := tuiosCLI(t, base, "worktree", "new", branch, "--repo", repo, "--detach")
		if err != nil {
			t.Fatalf("worktree new %s: %v: %s", branch, err, out)
		}
		if !strings.Contains(out, "Created session '") {
			t.Fatalf("worktree new %s did not report a session:\n%s", branch, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	toggleSidebarViaPalette(t, term)

	// The repository row and both branches under it. The wait is what makes
	// this a test of the daemon's record rather than of the CLI: the rows only
	// appear once the client's listing carries the worktree of each session.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "· repo") &&
			strings.Contains(text, "├─ feat/one") &&
			strings.Contains(text, "└─ feat/two")
	}, bootTimeout); err != nil {
		t.Fatalf("the rail never grouped the worktree sessions: %v\n%s", err, term.Snapshot())
	}

	// The parent is above its members, and the plain session keeps its own row.
	screen := term.Screen()
	parent := railLineWith(screen, "· repo")
	first := railLineWith(screen, "├─ feat/one")
	if parent < 0 || first < 0 || parent >= first {
		t.Errorf("the repository row is on line %d and its first branch on %d, want the row above\n%s",
			parent, first, term.Snapshot())
	}
	if railLineWith(screen, "plain") < 0 {
		t.Errorf("the session that is not a worktree lost its row:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "rail-worktrees")

	// A session a person named, which then walks into a worktree (#1245). It
	// joins the group, and keeps its name with the branch after it: the
	// promise is that a session with a custom name keeps it. Before the fix
	// the row took the branch as its whole label.
	var wtPath string
	for _, r := range worktreeRows(t, base) {
		if r["session"] == "repo-feat-one" {
			wtPath, _ = r["path"].(string)
		}
	}
	if wtPath == "" {
		t.Fatalf("worktree ls has no path for repo-feat-one: %v", worktreeRows(t, base))
	}
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "mine", "--detach"); err != nil {
		t.Fatalf("create the named session: %v: %s", err, out)
	}
	// /bin/sh reports no directory on cd, so the report fish sends is printed
	// by hand. The daemon reads the bytes either way.
	cd := "cd '" + wtPath + "' && printf '\\033]7;file://localhost%s\\007' \"$PWD\"\n"
	if err := paneSend(base, "mine", "0", cd); err != nil {
		t.Fatalf("move the named session into the worktree: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		line := railLineWith(s, "─ mine")
		return line >= 0 && strings.Contains(s.Line(line), "feat/one")
	}, 15*time.Second); err != nil {
		t.Fatalf("the named session in a worktree lost its name to the branch: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "rail-worktree-named")

	// A display name wins the same way.
	if out, err := tuiosCLI(t, base, "set-session-name", "-s", "mine", "My label"); err != nil {
		t.Fatalf("set-session-name: %v: %s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railLineWith(s, "─ My label") >= 0
	}, 15*time.Second); err != nil {
		t.Fatalf("the display name never reached the worktree row: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "rail-worktree-display-name")

	// A click on the repository row folds the group.
	col, row := findOnScreen(t, term, "· repo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Contains(text, "feat/one") || strings.Contains(text, "feat/two") || strings.Contains(text, "My label") {
			return false
		}
		line := railLineWith(s, "repo")
		return line >= 0 && strings.Contains(s.Line(line), "3")
	}, uiTimeout); err != nil {
		t.Fatalf("the click did not fold the group into a row carrying its count: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-worktrees-collapsed")

	alive(t, term, "after folding a worktree group")
}

// TestSidebarPillKeepsTreeBars attaches to a worktree session with the pill row
// style (#1399). The pill goes around the name only, so the tree bars of the
// group stay in one column and the names under them stay in another.
//
// Negative control: before the fix the pill wrapped the tree mark too, and its
// left cap pushed the current row's bar one column right.
func TestSidebarPillKeepsTreeBars(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nrow_style = \"pill\"\n")

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	for _, branch := range []string{"feat/one", "feat/two"} {
		if out, err := tuiosCLI(t, base, "worktree", "new", branch, "--repo", repo, "--detach"); err != nil {
			t.Fatalf("worktree new %s: %v: %s", branch, err, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "repo-feat-two"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "├─ feat/one") && strings.Contains(text, "feat/two")
	}, bootTimeout); err != nil {
		t.Fatalf("the rail never grouped the worktree sessions: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the rail never settled: %v", err)
	}
	screen := term.Screen()
	if branch, last := railCellColumn(screen, "├─"), railCellColumn(screen, "└─"); branch < 0 || branch != last {
		t.Errorf("the current row's bar is in column %d and its sibling's in column %d\n%s", last, branch, term.Snapshot())
	}
	if one, two := railCellColumn(screen, "feat/one"), railCellColumn(screen, "feat/two"); one < 0 || one != two {
		t.Errorf("the current row's name is in column %d and its sibling's in column %d\n%s", two, one, term.Snapshot())
	}
	railShot(t, term, "rail-pill-tree-bars")
}

// TestSidebarGroupsMainCheckout drives the same grouping with the main
// checkout gathered in: with group_main on, the session whose directory is the
// repository itself sits under the repo row with the worktrees, a click on
// the header opens that session instead of folding, and the fold gesture
// lives on the header's right-click menu.
func TestSidebarGroupsMainCheckout(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	writeConfig(t, base, "[appearance.sidebar]\ngroup_main = true\n")

	// The control that starts the daemon and the session this client attaches
	// to, then the main checkout's own session, made from the repository
	// itself, and one worktree of the same repository.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, repo, "new", "mainsess", "--detach"); err != nil {
		t.Fatalf("create the main checkout's session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}

	// Every consumer must distinguish the main checkout from a linked
	// worktree, whether or not the sidebar grouping is enabled.
	rows := worktreeRows(t, base)
	for session, wantMain := range map[string]bool{"mainsess": true, "repo-feat-one": false} {
		var row map[string]any
		for _, candidate := range rows {
			if candidate["session"] == session {
				row = candidate
				break
			}
		}
		if row == nil {
			t.Errorf("worktree ls has no row for %s: %v", session, rows)
			continue
		}
		if got, ok := row["main"].(bool); !ok || got != wantMain {
			t.Errorf("worktree ls --json main for %s = %v, want %t", session, row["main"], wantMain)
		}
	}

	out, err := tuiosCLI(t, base, "__complete", "worktree", "rm", "")
	if err != nil {
		t.Fatalf("complete worktree rm: %v: %s", err, out)
	}
	completions := make(map[string]bool)
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, ":") {
			break
		}
		completions[line] = true
	}
	if completions["mainsess"] {
		t.Errorf("worktree rm completion offers the main checkout: %s", out)
	}
	if !completions["repo-feat-one"] {
		t.Errorf("worktree rm completion omits the linked worktree: %s", out)
	}

	listed := herdrCall(t, base, "workspace.list", map[string]any{})
	for session, wantLinked := range map[string]bool{"mainsess": false, "repo-feat-one": true} {
		found := false
		for _, raw := range listed["workspaces"].([]any) {
			workspace := raw.(map[string]any)
			if workspace["label"] != session {
				continue
			}
			found = true
			worktree, ok := workspace["worktree"].(map[string]any)
			if !ok {
				t.Errorf("herdr workspace %s has no worktree record: %v", session, workspace)
				break
			}
			if got := worktree["is_linked_worktree"]; got != wantLinked {
				t.Errorf("herdr is_linked_worktree for %s = %v, want %t", session, got, wantLinked)
			}
		}
		if !found {
			t.Errorf("herdr workspace.list has no %s workspace: %v", session, listed)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)

	// The repo row holds both the worktree and the main checkout, each labelled
	// by its branch.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railLineWith(s, "· repo") >= 0 &&
			railLineWith(s, "feat/one") >= 0 &&
			railLineWith(s, "─ main") >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("the rail never grouped the main checkout with the worktrees: %v\n%s", err, term.Snapshot())
	}
	header := railLineWith(term.Screen(), "· repo")
	if main := railLineWith(term.Screen(), "─ main"); main <= header {
		t.Errorf("the main checkout's row is not under the repo header\n%s", term.Snapshot())
	}

	// group_main points the header's activation at the main checkout's
	// session: the click opens it and leaves the group on screen.
	col, row := findOnScreen(t, term, "· repo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: mainsess", uiTimeout); err != nil {
		t.Fatalf("the header click did not open the main checkout's session: %v\n%s", err, term.Snapshot())
	}
	if railLineWith(term.Screen(), "feat/one") < 0 {
		t.Errorf("opening the main checkout's session folded the group\n%s", term.Snapshot())
	}

	// The fold gesture moved to the header's right-click menu.
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Fold group", uiTimeout); err != nil {
		t.Fatalf("the header's menu offered no fold: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "Fold group")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Contains(text, "feat/one") || strings.Contains(text, "─ main") {
			return false
		}
		line := railLineWith(s, "repo")
		return line >= 0 && strings.Contains(s.Line(line), "2")
	}, uiTimeout); err != nil {
		t.Fatalf("the menu's fold did not fold the group into a row carrying its count: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-group-main")

	alive(t, term, "after grouping the main checkout")
}

// TestRailRevealsGroupedRow is #1386: a switch scrolls the sessions section to
// the attached session's own row, counted in the rows the section draws. The
// rows hold space and repository headings that the session list does not, so
// the list's index and length are the wrong numbers.
//
// The rail draws nine rows for six sessions: aa, top-a, bb, top-b, first, other,
// repo, feat/one, feat/two. The controls are in NEGATIVE_CONTROLS.md.
func TestRailRevealsGroupedRow(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nwidth = 28\nsections = \"sessions:50,terminals:50\"\n")
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	plain := t.TempDir()
	for _, args := range [][]string{
		{"new", "top-a", "--detach", "--space", "aa"},
		{"new", "top-b", "--detach", "--space", "bb"},
		{"new", "first", "--detach"},
		{"new", "other", "--detach"},
		{"worktree", "new", "feat/one", "--repo", repo, "--detach"},
		{"worktree", "new", "feat/two", "--repo", repo, "--detach"},
	} {
		if out, err := tuiosCLIIn(t, base, plain, args...); err != nil {
			t.Fatalf("tuios %v: %v: %s", args, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 15, args: []string{"attach", "first"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	// inSessions reports whether needle is on a rail row of the sessions section.
	inSessions := func(s tuitest.Screen, needle string) bool {
		row := railRowOf(s, needle)
		return row >= 0 && row < railRowOf(s, "terminals")
	}
	clientShows(t, base, "first")
	current := "first"
	switchTo := func(session string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "switch-session", "-s", current, session); err != nil {
			t.Fatalf("switch-session %s: %v: %s", session, err, out)
		}
		clientShows(t, base, session)
		current = session
	}
	shows := func(what, needle string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return inSessions(s, needle) }, uiTimeout); err != nil {
			railShot(t, term, "reveal-failed")
			t.Fatalf("%s: the sessions section does not show %s: %v\n%s", what, needle, err, term.Snapshot())
		}
	}

	// The six sessions fit this section and the nine rows do not, so the list's
	// length said there was nothing to scroll.
	if err := term.WaitFor(func(s tuitest.Screen) bool { return inSessions(s, "· repo") }, bootTimeout); err != nil {
		t.Fatalf("the rail never grouped the sessions: %v\n%s", err, term.Snapshot())
	}
	if inSessions(term.Screen(), "feat/two") {
		t.Fatalf("feat/two is already on the rail, so the rail is too tall to test a reveal\n%s", term.Snapshot())
	}
	railShot(t, term, "reveal-before")
	switchTo("repo-feat-two")
	shows("a switch to the last row", "feat/two")
	railShot(t, term, "reveal-last-row")

	// The sessions overflow this section too, so the old reveal did scroll, by
	// the session's place in the list and not its row.
	if err := term.Resize(120, 12); err != nil {
		t.Fatalf("resize: %v", err)
	}
	switchTo("top-a")
	shows("a switch to the top", "top-a")
	switchTo("other")
	shows("a switch to the middle", "other")
	railShot(t, term, "reveal-middle-row")

	// A folded repository draws no row for the session, so the repository's row
	// stands in for it, and the fold stays as the person left it.
	switchTo("repo-feat-two")
	shows("a switch back to the last row", "feat/two")
	col, row := findOnScreen(t, term, "· repo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railRowOf(s, "feat/two") < 0 }, uiTimeout); err != nil {
		t.Fatalf("the click did not fold the repository: %v\n%s", err, term.Snapshot())
	}
	switchTo("top-a")
	shows("a switch away from the folded repository", "top-a")
	if inSessions(term.Screen(), "· repo") {
		t.Fatalf("the repository's row is still on screen, so the switch back tests nothing\n%s", term.Snapshot())
	}
	switchTo("repo-feat-two")
	shows("a switch into the folded repository", "· repo")
	if railRowOf(term.Screen(), "feat/two") >= 0 {
		t.Errorf("the switch opened the folded repository\n%s", term.Snapshot())
	}
	railShot(t, term, "reveal-folded")

	// A session that a switch creates. The client's list has not heard of it
	// yet, so its first frame draws it as the last row.
	if out, err := tuiosCLIIn(t, base, plain, "switch-session", "-s", current, "--create", "--cwd", plain, "newest"); err != nil {
		t.Fatalf("switch-session --create: %v: %s", err, out)
	}
	clientShows(t, base, "newest")
	shows("a switch to a new session", "newest")
	railShot(t, term, "reveal-new-session")
}
