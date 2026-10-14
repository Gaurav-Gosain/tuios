package tuie2e

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestSpaceRailCursorPassesASplitRepository: one repository's worktree
// sessions filed in different spaces give the rail one repository header in
// each, both named after the repository. The keyboard cursor must step onto
// the second header and past it to the session under it, and enter there
// switches to that session.
//
// The rows, top to bottom: space s, repo, feat/one (in s), plain (no space,
// attached), repo, feat/two (no space). Entering the rail puts the cursor on
// plain, so two j are the second header and then feat/two.
//
// How it could pass wrongly, and what stops it:
//   - the two keys handled before a frame re-anchors the cursor, which is
//     where the cursor fell back to the first header: the test waits for the
//     rail to settle after each key.
//   - the rail drawing one header for the repository, so there is nothing to
//     confuse: the screen must show the repository's name on two rows.
//   - enter landing on feat/two by some other route: the cursor starts on
//     plain, and feat/one, the session the fallback reaches, must not be the
//     one the client shows.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A repository split across spaces").
func TestSpaceRailCursorPassesASplitRepository(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	killDaemon(t, base)
	writeConfig(t, base, spaceUIRail)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	created := regexp.MustCompile(`Created session '([^']+)'`)
	names := map[string]string{}
	for _, branch := range []string{"feat/one", "feat/two"} {
		out, err := tuiosCLI(t, base, "worktree", "new", branch, "--repo", repo, "--detach")
		m := created.FindStringSubmatch(out)
		if err != nil || m == nil {
			t.Fatalf("worktree new %s: %v: %s", branch, err, out)
		}
		names[branch] = m[1]
	}
	setSpace(t, base, names["feat/one"], "s")

	term := attachSpaceUI(t, base, "plain", 120, 40)
	windowManagementMode(t, term)
	s := waitSpaceRail(t, term, "the split repository", "feat/one", "feat/two", "plain")
	saveArtifact(t, term, dir, "rail-split-repo")
	headers := 0
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(railLine(s, r), "· repo") {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("the rail draws %d repository headers, want one in space s and one outside it\n%s", headers, term.Snapshot())
	}

	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter the rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
	for range 2 {
		if err := term.SendKeys("j"); err != nil {
			t.Fatalf("move the cursor: %v", err)
		}
		_ = term.WaitStable(500 * time.Millisecond)
	}
	saveArtifact(t, term, dir, "rail-cursor-on-feat-two")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the cursor row: %v", err)
	}
	clientShows(t, base, names["feat/two"])
}
