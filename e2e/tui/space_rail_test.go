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

// The rail's space headings: the sessions section draws a heading per space
// over that space's sessions, the way tuios ls groups its tables, and the
// sessions in no space after every space with no heading. Every claim is read
// off a real client's screen, and the screens are saved under artifactDir.

// spaceRailConfig opens the rail from the start, at the width railLine reads.
const spaceRailConfig = "[appearance.sidebar]\nenabled = true\nwidth = 28\n"

// spaceRailFixture makes detached sessions in a fresh daemon, in the order
// given, each filed under its space ("" for none).
func spaceRailFixture(t *testing.T, base string, sessions ...[2]string) {
	t.Helper()
	killDaemon(t, base)
	for _, s := range sessions {
		args := []string{"new", s[0], "--detach"}
		if s[1] != "" {
			args = append(args, "--space", s[1])
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("create %s: %v: %s", s[0], err, out)
		}
	}
}

// attachSpaceRail attaches a 120x40 client to session and waits for its first
// pane and for the daemon to list the client on it.
func attachSpaceRail(t *testing.T, base, session string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to %s: %v\n%s", session, err, term.Snapshot())
	}
	clientShows(t, base, session)
	return term
}

// spaceRailRows is the rail row of each needle, -1 for one not drawn.
func spaceRailRows(s tuitest.Screen, needles ...string) []int {
	out := make([]int, len(needles))
	for i, n := range needles {
		out[i] = railRowOf(s, n)
	}
	return out
}

// railRowsIncreasing reports whether every row is drawn and each is below the
// one before it.
func railRowsIncreasing(rows []int) bool {
	for i, r := range rows {
		if r < 0 || (i > 0 && r <= rows[i-1]) {
			return false
		}
	}
	return true
}

// railRuledFrom reports whether the rail's rule glyph runs on row after
// column fromCol, inside the rail. A heading draws it out to the spine, and no
// session row draws anything there.
func railRuledFrom(s tuitest.Screen, row, fromCol int) bool {
	for col := fromCol; col < railWidth; col++ {
		if s.Cell(col, row).Content == "─" {
			return true
		}
	}
	return false
}

// waitSpaceRail waits until ok holds for the rail and the frame settles, and
// returns that frame.
func waitSpaceRail(t *testing.T, term *tuitest.Terminal, what string, ok func(tuitest.Screen) bool) tuitest.Screen {
	t.Helper()
	if err := term.WaitFor(ok, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never showed %s: %v\n%s", what, err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	s := term.Screen()
	if !ok(s) {
		t.Fatalf("ASSERTION: the rail showed %s and then moved away from it\n%s", what, term.Snapshot())
	}
	return s
}

// railDraws reports whether every needle is on the rail.
func railDraws(needles ...string) func(tuitest.Screen) bool {
	return func(s tuitest.Screen) bool {
		for _, n := range needles {
			if railRowOf(s, n) < 0 {
				return false
			}
		}
		return true
	}
}

// underHeading reports whether the rail draws heading with members on the
// rows right below it, each stepped two columns in from the heading.
func underHeading(heading string, members ...string) func(tuitest.Screen) bool {
	return func(s tuitest.Screen) bool {
		h := railRowOf(s, heading)
		if h < 0 {
			return false
		}
		col := nameColOf(s, h, heading)
		for i, name := range members {
			if nameColOf(s, h+1+i, name) != col+2 {
				return false
			}
		}
		return true
	}
}

// setSessionSpace files session under space with the command a person runs,
// "" to take it out.
func setSessionSpace(t *testing.T, base, session, space string) {
	t.Helper()
	args := []string{"set-session-space", "-s", session}
	if space != "" {
		args = append(args, space)
	}
	if out, err := tuiosCLI(t, base, args...); err != nil {
		t.Fatalf("set-session-space %s %q: %v: %s", session, space, err, out)
	}
}

// TestSpaceRailHeadings: the rail draws one heading per space, in name order,
// each right above its own sessions, which step two columns in under it, and
// the session in no space after every space, at the headings' column, with no
// heading. A heading reads as a heading (no bold, the rule out to the spine)
// and is not a row the cursor stops on.
//
// How it could pass wrongly, and what stops it:
//   - the daemon's creation order mistaken for the grouping: e2e-loose is made
//     first and must be drawn last, and beta's session is made before
//     alpha's, so the spaces must come in name order, not in creation order.
//   - a heading over the wrong rows: each heading must sit on the row right
//     above its own sessions.
//   - the sessions in no space read as the last space's: e2e-loose must start
//     at the headings' column, not at the members' one.
//   - a heading drawn as one more session row: it must carry the rule, which
//     no session row does, and the session row must not.
//   - a heading the cursor stops on: one j from e2e-a2 must reach e2e-b1, past
//     beta's heading, which enter then opens.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space headings in the
// rail").
func TestSpaceRailHeadings(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceRailConfig)
	spaceRailFixture(t, base,
		[2]string{"e2e-loose", ""}, [2]string{"e2e-b1", "beta"},
		[2]string{"e2e-a1", "alpha"}, [2]string{"e2e-a2", "alpha"})

	term := attachSpaceRail(t, base, "e2e-a2")
	s := waitSpaceRail(t, term, "the alpha and beta headings with every session", railDraws("alpha", "beta", "e2e-a1", "e2e-a2", "e2e-b1", "e2e-loose"))
	saveArtifact(t, term, dir, "rail-spaces")

	order := spaceRailRows(s, "alpha", "e2e-a1", "e2e-a2", "beta", "e2e-b1", "e2e-loose")
	if !railRowsIncreasing(order) {
		t.Fatalf("ASSERTION: the rail rows alpha, e2e-a1, e2e-a2, beta, e2e-b1, e2e-loose are on %v, want that order top to bottom\n%s", order, term.Snapshot())
	}
	for _, g := range []struct {
		heading string
		members []string
	}{{"alpha", []string{"e2e-a1", "e2e-a2"}}, {"beta", []string{"e2e-b1"}}} {
		if !underHeading(g.heading, g.members...)(s) {
			t.Fatalf("ASSERTION: %v are not on the rows right under the %s heading, two columns in\n%s", g.members, g.heading, term.Snapshot())
		}
	}
	alphaCol := nameColOf(s, order[0], "alpha")
	if betaCol := nameColOf(s, order[3], "beta"); betaCol != alphaCol {
		t.Fatalf("ASSERTION: the headings start on columns %d and %d, want one column\n%s", alphaCol, betaCol, term.Snapshot())
	}
	if col := nameColOf(s, order[5], "e2e-loose"); col != alphaCol {
		t.Fatalf("ASSERTION: e2e-loose, in no space, starts on column %d, want the headings' column %d\n%s", col, alphaCol, term.Snapshot())
	}
	for _, h := range []struct {
		name string
		row  int
	}{{"alpha", order[0]}, {"beta", order[3]}} {
		if boldAt(s, alphaCol, h.row) {
			t.Fatalf("ASSERTION: the %s heading is bold, the weight the rail keeps for a row that wants a person\n%s", h.name, term.SnapshotStyled())
		}
		if !railRuledFrom(s, h.row, alphaCol+len(h.name)) {
			t.Fatalf("ASSERTION: the %s heading has no rule after its name, so it reads as one more session\n%s", h.name, term.Snapshot())
		}
	}
	if railRuledFrom(s, order[5], alphaCol+len("e2e-loose")) {
		t.Fatalf("ASSERTION: the e2e-loose session row carries a heading's rule\n%s", term.Snapshot())
	}

	// The cursor passes over a heading.
	windowManagementMode(t, term)
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter the rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(500 * time.Millisecond)
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("move the cursor: %v", err)
	}
	_ = term.WaitStable(500 * time.Millisecond)
	saveArtifact(t, term, dir, "rail-cursor-past-beta")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the cursor row: %v", err)
	}
	clientShows(t, base, "e2e-b1")
}

// TestSpaceRailHeadingsFollowTheLabel: filing a session under a space, or
// taking it out, moves its row under the heading or back out while a client
// is attached, for the attached session and for another one, and the heading
// goes when its space is empty.
//
// How it could pass wrongly, and what stops it:
//   - only the other sessions follow, read off the listing: the attached
//     session's space comes down on the session state, so it is moved first,
//     on its own.
//   - a heading left behind with nobody under it: once both sessions are out,
//     the heading must be gone.
//   - a heading drawn with no space at all: before any space is set, work is
//     not on the rail and both sessions start on one column.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space headings in the
// rail").
func TestSpaceRailHeadingsFollowTheLabel(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceRailConfig)
	spaceRailFixture(t, base, [2]string{"e2e-here", ""}, [2]string{"e2e-there", ""})

	term := attachSpaceRail(t, base, "e2e-here")
	s := waitSpaceRail(t, term, "both sessions", railDraws("e2e-here", "e2e-there"))
	saveArtifact(t, term, dir, "rail-no-space")
	here, there := railRowOf(s, "e2e-here"), railRowOf(s, "e2e-there")
	flat := nameColOf(s, here, "e2e-here")
	if railRowOf(s, "work") >= 0 || nameColOf(s, there, "e2e-there") != flat {
		t.Fatalf("ASSERTION: with no session in a space the rail is not one flat list\n%s", term.Snapshot())
	}

	setSessionSpace(t, base, "e2e-here", "work")
	s = waitSpaceRail(t, term, "e2e-here, the attached session, under a work heading", func(s tuitest.Screen) bool {
		return underHeading("work", "e2e-here")(s) && nameColOf(s, railRowOf(s, "e2e-there"), "e2e-there") == flat
	})
	saveArtifact(t, term, dir, "rail-attached-in-work")
	if railRowOf(s, "e2e-there") < railRowOf(s, "e2e-here") {
		t.Fatalf("ASSERTION: e2e-there, in no space, is above the work space\n%s", term.Snapshot())
	}

	setSessionSpace(t, base, "e2e-there", "work")
	waitSpaceRail(t, term, "e2e-here and e2e-there under the work heading", underHeading("work", "e2e-here", "e2e-there"))
	saveArtifact(t, term, dir, "rail-both-in-work")

	setSessionSpace(t, base, "e2e-here", "")
	waitSpaceRail(t, term, "e2e-here back out of work, after it, at the section's column", func(s tuitest.Screen) bool {
		r := railRowOf(s, "e2e-here")
		return underHeading("work", "e2e-there")(s) && r > railRowOf(s, "e2e-there") && nameColOf(s, r, "e2e-here") == flat
	})
	saveArtifact(t, term, dir, "rail-attached-out")

	setSessionSpace(t, base, "e2e-there", "")
	waitSpaceRail(t, term, "no work heading once both sessions left it", func(s tuitest.Screen) bool {
		return railRowOf(s, "work") < 0 &&
			nameColOf(s, railRowOf(s, "e2e-here"), "e2e-here") == flat &&
			nameColOf(s, railRowOf(s, "e2e-there"), "e2e-there") == flat
	})
	saveArtifact(t, term, dir, "rail-work-gone")
}

// TestSpaceRailHeadingsSurviveTheDaemon: after the daemon restarts and
// restores its sessions, a client attached to a restored session draws the
// same headings, its own session under its space included.
//
// How it could pass wrongly, and what stops it:
//   - no restart at all: e2e-spaced must be listed as restored.
//   - the attached session's row drawn from a value the restore did not set:
//     the client attaches to the spaced session itself, which reads its space
//     off the restored session state, not off the listing.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space headings in the
// rail").
func TestSpaceRailHeadingsSurviveTheDaemon(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceRailConfig)
	spaceRailFixture(t, base, [2]string{"e2e-loose", ""}, [2]string{"e2e-spaced", "ops"})

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}
	if restored := waitForSessionInfo(t, base, "e2e-spaced"); !restored.Restored {
		t.Fatalf("e2e-spaced is listed but not as restored, so the restart did not happen")
	}
	waitForSessionInfo(t, base, "e2e-loose")

	term := attachSpaceRail(t, base, "e2e-spaced")
	s := waitSpaceRail(t, term, "e2e-spaced under an ops heading after the restart", func(s tuitest.Screen) bool {
		return underHeading("ops", "e2e-spaced")(s) && railDraws("e2e-loose", "e2e-trigger")(s)
	})
	saveArtifact(t, term, dir, "rail-after-restart")
	col := nameColOf(s, railRowOf(s, "ops"), "ops")
	for _, name := range []string{"e2e-loose", "e2e-trigger"} {
		r := railRowOf(s, name)
		if r < railRowOf(s, "e2e-spaced") || nameColOf(s, r, name) != col {
			t.Fatalf("ASSERTION: %s, in no space, is not after the ops space at the heading's column\n%s", name, term.Snapshot())
		}
	}
}

// TestSpaceRailCursorPassesASplitRepository: one repository's worktree
// sessions filed in different spaces give the rail one repository header in
// each, both named after the repository. The cursor must step onto the second
// header and past it to the session under it, and enter there opens that
// session.
//
// The rows, top to bottom: the ops heading, repo, feat/one (in ops), plain (no
// space, attached), repo, feat/two (no space). Entering the rail puts the
// cursor on plain, so two j are the second header and then feat/two.
//
// How it could pass wrongly, and what stops it:
//   - the keys handled before a frame re-anchors the cursor, which is where
//     the cursor fell back to the first header: the test waits for the rail
//     to settle after each key.
//   - one header for the repository, so there is nothing to confuse: the rail
//     must show the repository's header on two rows.
//   - enter landing on feat/two by some other route: the cursor starts on
//     plain, and the client must end on feat/two.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space headings in the
// rail").
func TestSpaceRailCursorPassesASplitRepository(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	killDaemon(t, base)
	writeConfig(t, base, spaceRailConfig)
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
	setSessionSpace(t, base, names["feat/one"], "ops")

	term := attachSpaceRail(t, base, "plain")
	s := waitSpaceRail(t, term, "the ops heading, feat/one, plain and feat/two in that order", func(s tuitest.Screen) bool {
		return railDraws("ops", "feat/one", "feat/two", "plain")(s) &&
			railRowOf(s, "ops") < railRowOf(s, "feat/one") &&
			railRowOf(s, "feat/one") < railRowOf(s, "plain") &&
			railRowOf(s, "plain") < railRowOf(s, "feat/two")
	})
	saveArtifact(t, term, dir, "rail-split-repo")
	headers := 0
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(railLine(s, r), "· repo") {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("ASSERTION: the rail draws %d repository headers, want one in ops and one outside it\n%s", headers, term.Snapshot())
	}

	windowManagementMode(t, term)
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter the rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(500 * time.Millisecond)
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
