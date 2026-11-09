package tuie2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// ckHeadingRow is the first fleet row that starts with heading, or -1.
func ckHeadingRow(s tuitest.Screen, heading string) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.HasPrefix(strings.TrimSpace(fleetCol(s, r)), heading+"  ") {
			return r
		}
	}
	return -1
}

// TestCockpitGroupBy groups the agents view's list by session at 200x50: s
// opens the picker, which names the groupings, and 2 chooses session. Needs
// you stays on top with the held approval, and below it each session has its
// heading, in name order, with its own agents under it; the header says
// Group: session and still counts the agents by state. A new client keeps the grouping. 3 groups by machine, 4 by
// project (these agents have no repository), and 1 goes back to the state
// groups. A click on the header's Group control opens the Group menu too.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the bands never
// built, the session headings are missing; with the header counting only
// the state headings, the counts check fails; with the grouping not read
// from the state file, the new client check fails.
func TestCockpitGroupBy(t *testing.T) {
	term, base, _, _ := startCockpitFleet(t, 200, 50, nil)
	saveCockpitFrames(t, term, "groupby-state")
	ckPress(t, term, "s")
	groupMenuOpen := func(s tuitest.Screen) bool {
		text := s.Text()
		for _, re := range []string{`state\s+1`, `session\s+2`, `machine\s+3`, `project\s+4`, `Sort\s+(needs you|name|recent|session)\s+s`} {
			if !regexp.MustCompile(re).MatchString(text) {
				return false
			}
		}
		return true
	}
	if err := term.WaitFor(groupMenuOpen, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: s did not open the Group menu: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "groupby-picker")
	ckPress(t, term, "2")
	bySession := func(s tuitest.Screen) bool {
		needs, api, cache, docs, misc := ckHeadingRow(s, "Needs you"), ckHeadingRow(s, "api"), ckHeadingRow(s, "cache"), ckHeadingRow(s, "docs"), ckHeadingRow(s, "misc")
		auth, ws, cli := fleetRowWith(s, "api-auth"), fleetRowWith(s, "ws-reconnect"), fleetRowWith(s, "docs-cli")
		return strings.Contains(s.Text(), "Group: session") && ckHeadingRow(s, "Working") < 0 &&
			containsAll(s.Text(), "6 working", "3 to review", "2 idle") &&
			needs >= 0 && needs < auth && auth < api && api < ws && ws < cache && cache < docs && docs < cli && docs < misc
	}
	if err := term.WaitFor(bySession, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: 2 did not group the list by session under Needs you: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "groupby-session")

	// A new client keeps the grouping.
	ckPress(t, term, "q")
	again := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 50, shippedLooks: true})
	if err := again.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, again.Snapshot())
	}
	windowManagementMode(t, again)
	ckPress(t, again, tuitest.Ctrl('b'), "i")
	if err := again.WaitFor(bySession, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a new client did not keep the session grouping: %v\n%s", err, again.Snapshot())
	}

	// Machine, then project, then back to state.
	ckPress(t, again, "s", "3")
	waitText(t, again, "the machine grouping", "Group: machine")
	ckPress(t, again, "s", "4")
	if err := again.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Group: project") && ckHeadingRow(s, "No repository") > ckHeadingRow(s, "Needs you")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: 4 did not group the list by project: %v\n%s", err, again.Snapshot())
	}
	ckPress(t, again, "s", "1")
	if err := again.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Group: state") && ckHeadingRow(s, "Working") > 0 && ckHeadingRow(s, "No repository") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: 1 did not give the state groups back: %v\n%s", err, again.Snapshot())
	}

	// The header's control opens the menu.
	row, col := findText(t, again, "Group: state")
	mouseClick(t, again, col, row, tuitest.MouseLeft, 0)
	if err := again.WaitFor(groupMenuOpen, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on Group did not open the menu: %v\n%s", err, again.Snapshot())
	}
	ckPress(t, again, tuitest.Esc)
	alive(t, again, "after the groupings")
}
