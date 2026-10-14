package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The space surfaces a person sees: the rail's space headers and their fold,
// the session switcher's grouping and its space/session query, and the dock's
// "space > session" label. Every claim is read off a real client's screen, and
// every screen it reads is saved under artifactDir.

// spaceUIRail is the rail config these tests draw with: open from the start,
// at the width railLine reads.
const spaceUIRail = "[appearance.sidebar]\nenabled = true\nwidth = 28\n"

// spaceUINoRail keeps the rail shut, for the switcher and dock tests, whose
// rows would otherwise be matched in the rail too.
const spaceUINoRail = "[appearance.sidebar]\nenabled = false\n"

// spaceUIFixture makes four detached sessions in a fresh daemon: e2e-loose in
// no space, made first, then e2e-b1 in beta and e2e-a1 and e2e-a2 in alpha.
// With spaced false the same four sessions are made in no space at all.
//
// The order is the point. The ungrouped session is made first, so a surface
// that shows it last is grouping, not echoing the daemon's order; and beta's
// session is made before alpha's, so the spaces come in their first member's
// order rather than sorted.
func spaceUIFixture(t *testing.T, base string, spaced bool) {
	t.Helper()
	killDaemon(t, base)
	sessions := []struct{ name, space string }{
		{"e2e-loose", ""}, {"e2e-b1", "beta"}, {"e2e-a1", "alpha"}, {"e2e-a2", "alpha"},
	}
	for _, s := range sessions {
		args := []string{"new", s.name, "--detach"}
		if spaced && s.space != "" {
			args = append(args, "--space", s.space)
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("create %s: %v: %s", s.name, err, out)
		}
	}
}

// attachSpaceUI attaches a client to session and waits for its first pane.
// env is layered over the harness's, for a client at another colour depth.
func attachSpaceUI(t *testing.T, base, session string, cols, rows int, env ...string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{cols: cols, rows: rows, args: []string{"attach", session}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to %s: %v\n%s", session, err, term.Snapshot())
	}
	clientShows(t, base, session)
	return term
}

// setSpace files session under space through the daemon verb, "" for none.
func setSpace(t *testing.T, base, session, space string) {
	t.Helper()
	params, _ := json.Marshal(map[string]string{"session": session, "space": space})
	if out, err := tuiosCLI(t, base, "call", "set-session-space", string(params)); err != nil {
		t.Fatalf("set-session-space %s %q: %v: %s", session, space, err, out)
	}
}

// spaceRailRows is the rail's screen row of each needle, -1 for one not drawn.
func spaceRailRows(s tuitest.Screen, needles ...string) []int {
	out := make([]int, len(needles))
	for i, n := range needles {
		out[i] = railRowOf(s, n)
	}
	return out
}

// strictlyIncreasing reports whether every row is drawn and each is below the
// one before it.
func strictlyIncreasing(rows []int) bool {
	for i, r := range rows {
		if r < 0 || (i > 0 && r <= rows[i-1]) {
			return false
		}
	}
	return true
}

// waitSpaceRail waits for every needle on the rail and for the frame to settle.
func waitSpaceRail(t *testing.T, term *tuitest.Terminal, what string, needles ...string) tuitest.Screen {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, n := range needles {
			if railRowOf(s, n) < 0 {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the rail never drew %v: %v\n%s", what, needles, err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	return term.Screen()
}

// sidebarState reads the client's sidebar.json.
func sidebarState(t *testing.T, base string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sidebar.json"))
	if err != nil {
		t.Fatalf("read sidebar.json: %v", err)
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("sidebar.json is not JSON: %v\n%s", err, data)
	}
	return st
}

// TestSpaceRailHeadersFoldAndPersist: the rail draws a header per space over
// its sessions, the spaces in their first member's order and the session in no
// space after them all; a click folds a space and leaves its count; the fold
// is in sidebar.json under its own key and is still there after a reattach.
//
// How it could pass wrongly, and what stops it:
//   - the names appear but ungrouped (the daemon's order): e2e-loose is made
//     first and must be drawn last, and beta's header must sit above alpha's.
//   - a header drawn as one more session row: it has to be bold, and its
//     sessions have to step in two columns under it while e2e-loose does not.
//   - the fold hiding the rows for this client only: the client is detached
//     and a new one attached, and the space must come back folded.
//   - the fold stored as a repository fold (a collision with a repository of
//     the same name): sidebar.json must carry spaces_collapsed and no
//     repos_collapsed.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space UI").
func TestSpaceRailHeadersFoldAndPersist(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUIRail)
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	s := waitSpaceRail(t, term, "the grouped rail", "beta", "alpha", "e2e-b1", "e2e-a1", "e2e-a2", "e2e-loose")
	saveArtifact(t, term, dir, "rail-grouped")

	order := spaceRailRows(s, "beta", "e2e-b1", "alpha", "e2e-a1", "e2e-a2", "e2e-loose")
	if !strictlyIncreasing(order) {
		t.Fatalf("ASSERTION: the rail rows beta, e2e-b1, alpha, e2e-a1, e2e-a2, e2e-loose are on %v, want that order top to bottom\n%s", order, term.Snapshot())
	}
	alphaCol := nameColOf(s, order[2], "alpha")
	betaCol := nameColOf(s, order[0], "beta")
	if alphaCol < 0 || betaCol != alphaCol {
		t.Fatalf("ASSERTION: the space headers start on columns %d and %d, want one column\n%s", betaCol, alphaCol, term.Snapshot())
	}
	for _, name := range []string{"beta", "alpha"} {
		row := railRowOf(s, name)
		if !boldAt(s, nameColOf(s, row, name), row) {
			t.Fatalf("ASSERTION: the %s header is not bold, so it reads as one more session\n%s", name, term.SnapshotStyled())
		}
	}
	// The fold mark, not weight alone, is what tells a space's header from a
	// session in no space at the same indent.
	for _, name := range []string{"beta", "alpha"} {
		if mark := spaceFoldMark(s, name); mark != "▾" {
			t.Fatalf("ASSERTION: the open %s header carries %q before its name, want the open fold mark ▾\n%s", name, mark, term.Snapshot())
		}
	}
	if mark := spaceFoldMark(s, "e2e-loose"); mark == "▾" || mark == "▸" {
		t.Fatalf("ASSERTION: e2e-loose, a session in no space, carries the fold mark %q, like a header\n%s", mark, term.Snapshot())
	}
	for i, name := range []string{"e2e-b1", "", "e2e-a1", "e2e-a2"} {
		if name == "" {
			continue
		}
		if col := nameColOf(s, order[i+1], name); col != alphaCol+2 {
			t.Fatalf("ASSERTION: %s starts on column %d, want %d, two in from its space's header\n%s", name, col, alphaCol+2, term.Snapshot())
		}
	}
	if col := nameColOf(s, order[5], "e2e-loose"); col != alphaCol {
		t.Fatalf("ASSERTION: e2e-loose, in no space, starts on column %d, want the headers' column %d\n%s", col, alphaCol, term.Snapshot())
	}
	// Every session has one window, so the rail's window-count column is off,
	// as it is with no spaces. A header's member count must not turn it on.
	if line := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(railLine(s, order[5]), " "), "│"), " "); !strings.HasSuffix(line, "e2e-loose") {
		t.Fatalf("ASSERTION: the e2e-loose row carries a figure after its name, %q: a header's count turned the window counts on\n%s", line, term.Snapshot())
	}
	if nameColOf(s, order[5], "e2e-loose") >= 0 && boldAt(s, alphaCol, order[5]) {
		t.Fatalf("ASSERTION: e2e-loose is drawn bold, like a header\n%s", term.SnapshotStyled())
	}

	// A click on beta's header folds it: its session goes, and the header
	// says how many it holds.
	col, row := findOnScreen(t, term, "beta")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, "beta")
		return railRowOf(s, "e2e-b1") < 0 && r >= 0 && strings.Contains(strings.TrimSpace(railLine(s, r)), "1") &&
			railRowOf(s, "e2e-a1") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the click did not fold beta into a header carrying its count: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "rail-beta-folded")
	if s := term.Screen(); spaceFoldMark(s, "beta") != "▸" || spaceFoldMark(s, "alpha") != "▾" {
		t.Fatalf("ASSERTION: after folding beta the marks are beta %q and alpha %q, want ▸ and ▾\n%s",
			spaceFoldMark(s, "beta"), spaceFoldMark(s, "alpha"), term.Snapshot())
	}

	st := sidebarState(t, base)
	got, _ := st["spaces_collapsed"].([]any)
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("ASSERTION: sidebar.json spaces_collapsed is %v, want [beta]", st["spaces_collapsed"])
	}
	if _, ok := st["repos_collapsed"]; ok {
		t.Fatalf("ASSERTION: folding a space wrote repos_collapsed %v", st["repos_collapsed"])
	}

	// Detach, and attach a new client: the fold is still there. The new client
	// runs at 16 colours, where a header has no ground or ink of its own to
	// lean on, and it still has to read as a header: bold, with its sessions
	// stepped in under it.
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	waitExit(t, term, "after the detach")
	again := attachSpaceUI(t, base, "e2e-a1", 120, 40, "TERM=xterm", "COLORTERM=")
	s = waitSpaceRail(t, again, "the reattached rail", "beta", "alpha", "e2e-a1", "e2e-loose")
	saveArtifact(t, again, dir, "rail-reattached-16-colours")
	if railRowOf(s, "e2e-b1") >= 0 {
		t.Fatalf("ASSERTION: beta came back open after a reattach\n%s", again.Snapshot())
	}
	for _, name := range []string{"beta", "alpha"} {
		row := railRowOf(s, name)
		if !boldAt(s, nameColOf(s, row, name), row) {
			t.Fatalf("ASSERTION: at 16 colours the %s header is not bold\n%s", name, again.SnapshotStyled())
		}
	}
	if a, h := nameColOf(s, railRowOf(s, "e2e-a1"), "e2e-a1"), nameColOf(s, railRowOf(s, "alpha"), "alpha"); a != h+2 {
		t.Fatalf("ASSERTION: at 16 colours e2e-a1 starts on column %d, want %d\n%s", a, h+2, again.Snapshot())
	}
	alive(t, again, "after reattaching to a folded space")
}

// spaceFoldMark is the mark drawn nearest left of a rail row's name: the fold
// mark on a space's header, a session's glyph on a session row, or "" when the
// name is not on the rail.
func spaceFoldMark(s tuitest.Screen, name string) string {
	row := railRowOf(s, name)
	col := nameColOf(s, row, name)
	if col < 0 {
		return ""
	}
	runes := []rune(railLine(s, row))
	for i := col - 1; i >= 0; i-- {
		if runes[i] != ' ' {
			return string(runes[i])
		}
	}
	return ""
}

// switcherRow is the screen row of the switcher's row for a session, found by
// the pane count every session row carries and no header does, or -1.
func switcherRow(s tuitest.Screen, name string) int {
	_, rows := s.Size()
	for r := 0; r < rows; r++ {
		line := s.Line(r)
		if strings.Contains(line, name+" ") && strings.Contains(line, " pane") {
			return r
		}
	}
	return -1
}

// switcherHeader is the screen row of a space's heading in the switcher: its
// name and count, on a row with no pane count, or -1.
func switcherHeader(s tuitest.Screen, space string, count string) int {
	_, rows := s.Size()
	for r := 0; r < rows; r++ {
		line := s.Line(r)
		if strings.TrimSpace(line) == space+" "+count {
			return r
		}
	}
	return -1
}

// switcherSessions is the sessions the switcher lists, top to bottom.
func switcherSessions(s tuitest.Screen) []string {
	var out []string
	for _, name := range []string{"e2e-loose", "e2e-b1", "e2e-a1", "e2e-a2"} {
		if switcherRow(s, name) >= 0 {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return switcherRow(s, a) - switcherRow(s, b) })
	return out
}

// TestSpaceSwitcherGroupsAndNarrows: the switcher lists each space under a
// heading and the session in no space last; "space/" narrows to one space,
// "space/name" to one session in it, and Enter on that switches to it; plain
// typing still filters by name.
//
// How it could pass wrongly, and what stops it:
//   - headings drawn but the rows in the daemon's order: the rows are read in
//     screen order and must be e2e-b1, e2e-a1, e2e-a2, e2e-loose, each space's
//     under its heading.
//   - "alpha/" matched as plain text (no session is called that, so the list
//     would be empty, or every row would stay): exactly alpha's two must show.
//   - the selection left on a heading, so Enter does nothing or creates a
//     session called "alpha/e2e-a2": Enter must land the client on e2e-a2.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space UI").
func TestSpaceSwitcherGroupsAndNarrows(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "S")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(switcherSessions(s)) == 4 && switcherHeader(s, "alpha", "2") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the switcher never listed the four sessions under their headings: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	s := term.Screen()
	saveArtifact(t, term, dir, "switcher-grouped")

	rows := []int{
		switcherHeader(s, "beta", "1"), switcherRow(s, "e2e-b1"),
		switcherHeader(s, "alpha", "2"), switcherRow(s, "e2e-a1"), switcherRow(s, "e2e-a2"),
		switcherRow(s, "e2e-loose"),
	}
	if !strictlyIncreasing(rows) {
		t.Fatalf("ASSERTION: beta's heading, e2e-b1, alpha's heading, e2e-a1, e2e-a2, e2e-loose are on rows %v, want that order\n%s", rows, term.Snapshot())
	}

	sendKeys(t, term, "alpha/")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return slices.Equal(switcherSessions(s), []string{"e2e-a1", "e2e-a2"})
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: alpha/ did not narrow the switcher to alpha's two sessions: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "switcher-alpha")

	sendKeys(t, term, "e2e-a2")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return slices.Equal(switcherSessions(s), []string{"e2e-a2"})
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: alpha/e2e-a2 did not narrow the switcher to e2e-a2: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "switcher-alpha-a2")
	sendKeys(t, term, tuitest.Enter)
	clientShows(t, base, "e2e-a2")
	if err := term.WaitForText("alpha > e2e-a2", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after Enter the dock does not say alpha > e2e-a2: %v\n%s", err, term.Snapshot())
	}

	// Plain typing is the filter it always was.
	sendKeys(t, term, tuitest.Ctrl('b'), "S")
	if err := term.WaitForText("Sessions", uiTimeout); err != nil {
		t.Fatalf("the switcher did not open again: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "loose")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return slices.Equal(switcherSessions(s), []string{"e2e-loose"})
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: plain typing did not filter to e2e-loose: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "switcher-plain")
	sendKeys(t, term, tuitest.Enter)
	clientShows(t, base, "e2e-loose")
	alive(t, term, "after switching through the grouped switcher")
}

// spaceDockLeft is a dock config whose left list puts a probe cell straight
// after where the space label goes, with the label listed or not. The probe's
// column is what shows whether an empty label took any room: the default list
// ends the left block in a space the layout absorbs, so a stray padding cell
// there moves nothing a person can see, and nothing a test can read either.
func spaceDockLeft(withSpace bool) string {
	left := "'mode', 'workspaces', 'trail', 'custom/probe', 'tape'"
	if withSpace {
		left = "'mode', 'workspaces', 'trail', 'space', 'custom/probe', 'tape'"
	}
	return spaceUINoRail + "[dock]\nleft = [" + left + "]\n" +
		"[dock.custom.probe]\ncommand = \"printf PROBE\"\nrefresh = 'once'\n"
}

// waitProbe waits for the dock's probe cell and for the frame to settle.
func waitProbe(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(dockRow(s), "PROBE") }, uiTimeout); err != nil {
		t.Fatalf("%s: the dock never drew the probe cell: %v\n%s", what, err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
}

// TestSpaceDockLabel: the dock says "space > session" for a session in a space
// and nothing for one in none, and taking the session out of its space puts
// the dock back exactly as it was, cell for cell.
//
// How it could pass wrongly, and what stops it:
//   - a label drawn whatever the space: the dock row of a session in no space
//     must not carry " > ".
//   - an empty component that still pads or separates: the dock row of a
//     session in no space must be the row of a dock that does not list the
//     component at all, with a probe cell right after it to show any column
//     it took; and after the space is cleared the whole screen must equal the
//     one before it was set.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space UI").
func TestSpaceDockLabel(t *testing.T) {
	dir := artifactDir(t)

	refBase := t.TempDir()
	writeConfig(t, refBase, spaceDockLeft(false))
	spaceUIFixture(t, refBase, false)
	ref := attachSpaceUI(t, refBase, "e2e-a1", 120, 40)
	waitProbe(t, ref, "the dock without the space component")
	refDock := dockRow(ref.Screen())
	saveArtifact(t, ref, dir, "dock-unlisted")

	base := t.TempDir()
	writeConfig(t, base, spaceDockLeft(true))
	spaceUIFixture(t, base, false)
	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	waitProbe(t, term, "the dock with the space component")
	before := term.Snapshot()
	beforeDock := dockRow(term.Screen())
	saveArtifact(t, term, dir, "dock-no-space")
	if strings.Contains(beforeDock, " > ") {
		t.Fatalf("ASSERTION: the dock carries a space label for a session in no space: %q", beforeDock)
	}
	if beforeDock != refDock {
		t.Fatalf("ASSERTION: with the session in no space, listing the space component changed the dock\nunlisted: %q\nlisted:   %q", refDock, beforeDock)
	}

	setSpace(t, base, "e2e-a1", "alpha")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := dockRow(s)
		label := strings.Index(row, " alpha > e2e-a1 ")
		return label >= 0 && label < strings.Index(row, "PROBE")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock never said alpha > e2e-a1 where the list puts it, before the probe: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "dock-in-space")

	setSpace(t, base, "e2e-a1", "")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(dockRow(s), "alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the label stayed after the session left its space: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "dock-space-cleared")
	if after := term.Snapshot(); after != before {
		t.Fatalf("ASSERTION: the screen after leaving the space differs from the one before it\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestSpaceDockLabelNarrow: on a bar too narrow for the whole label the space
// is cut and the session name kept; on one too narrow for the session name the
// label is not drawn at all, and the dock is the dock of a session in no space.
//
// How it could pass wrongly, and what stops it:
//   - the session name cut instead of the space: the full name must be on the
//     row, after a cut space.
//   - a partial label on the narrowest bar: the dock row there must equal the
//     row the same client draws once the space is cleared.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space UI").
func TestSpaceDockLabelNarrow(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, false)
	setSpace(t, base, "e2e-a1", "payments-platform")

	term := attachSpaceUI(t, base, "e2e-a1", 100, 30)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := dockRow(s)
		return strings.Contains(row, "payments-p") && strings.Contains(row, " > e2e-a1 ")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: at 100 columns the dock did not cut the space and keep e2e-a1: %v\n%s", err, term.Snapshot())
	}
	if row := dockRow(term.Screen()); strings.Contains(row, "payments-platform") {
		t.Fatalf("ASSERTION: at 100 columns the space was not cut: %q", row)
	}
	saveArtifact(t, term, dir, "dock-100-cols")

	if err := term.Resize(60, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		w, _ := s.Size()
		return w == 60 && !strings.Contains(dockRow(s), "e2e-a1") && countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: at 60 columns the dock still drew part of the label: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	narrow := dockRow(term.Screen())
	saveArtifact(t, term, dir, "dock-60-cols")

	setSpace(t, base, "e2e-a1", "")
	time.Sleep(time.Second)
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "dock-60-cols-no-space")
	if plain := dockRow(term.Screen()); plain != narrow {
		t.Fatalf("ASSERTION: at 60 columns the dock of a spaced session differs from a session in no space\nspaced:   %q\nno space: %q", narrow, plain)
	}
}

// spaceUIScreens drives one client through what the invisibility test
// compares: the rail with every session listed, then the switcher open over
// it. Run once per binary.
func spaceUIScreens(t *testing.T, base, label string) (rail, switcher string) {
	t.Helper()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUIRail)
	spaceUIFixture(t, base, false)
	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	waitSpaceRail(t, term, label+" rail", "e2e-loose", "e2e-b1", "e2e-a1", "e2e-a2")
	rail = term.Snapshot()
	saveArtifact(t, term, dir, label+"-rail")

	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "S")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return len(switcherSessions(s)) == 4 }, uiTimeout); err != nil {
		t.Fatalf("%s: the switcher never listed the four sessions: %v\n%s", label, err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	switcher = term.Snapshot()
	saveArtifact(t, term, dir, label+"-switcher")
	return rail, switcher
}

// runIdentity is what differs between two runs of one build: a pane's
// generated window ID in its default title, and the run's temporary directory
// in the files section's header. No space code draws either.
var runIdentity = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`Terminal [0-9a-f]{6,}`), "Terminal <id>"},
	{regexp.MustCompile(`…\S*/cwd`), "…<tmp>/cwd"},
}

// maskRunIdentity replaces the run-specific parts of a screen, so two runs of
// one scenario compare equal when they draw the same thing.
func maskRunIdentity(screen string) string {
	for _, r := range runIdentity {
		screen = r.re.ReplaceAllString(screen, r.with)
	}
	return screen
}

// TestSpacesInvisibleWithoutSpaces: with no session in any space, the rail,
// the switcher and the dock this build draws are the ones a build without
// spaces draws, screen for screen.
//
// It needs that build, which this suite does not make: name it with
// TUIOS_E2E_BASELINE_BIN (a tuios built from upstream/main, which has no space
// UI).
// Without it the test is skipped and says so.
//
// How it could pass wrongly, and what stops it:
//   - comparing this build against itself: the baseline is a different
//     binary, run against its own daemon.
//   - comparing only that no header is drawn: the whole screen is compared,
//     dock row included, so an indent, a padding cell or a separator that
//     moved fails it. Only a pane's generated ID and the run's temporary path
//     are masked (maskRunIdentity).
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space UI").
func TestSpacesInvisibleWithoutSpaces(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream/main, which has no space UI, and name it there")
	}
	railNow, switcherNow := spaceUIScreens(t, t.TempDir(), "this-build")

	prev := tuiosBin
	tuiosBin = baseline
	restore := func() { tuiosBin = prev }
	// A fatal step inside must not leave every later test on the baseline.
	defer restore()
	railThen, switcherThen := spaceUIScreens(t, t.TempDir(), "baseline")
	restore()

	railNow, railThen = maskRunIdentity(railNow), maskRunIdentity(railThen)
	switcherNow, switcherThen = maskRunIdentity(switcherNow), maskRunIdentity(switcherThen)
	if railNow != railThen {
		t.Errorf("ASSERTION: with no spaces the rail differs from the baseline's\nthis build:\n%s\nbaseline:\n%s", railNow, railThen)
	}
	if switcherNow != switcherThen {
		t.Errorf("ASSERTION: with no spaces the switcher differs from the baseline's\nthis build:\n%s\nbaseline:\n%s", switcherNow, switcherThen)
	}
}
