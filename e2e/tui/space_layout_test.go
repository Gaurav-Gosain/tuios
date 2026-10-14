package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's three space layouts, picked by appearance.sidebar.spaces: grouped
// (headings inside the sessions section, covered by space_ui_test.go), split (a
// spaces section above the sessions section, which then lists only the current
// space) and off (one flat list). Every claim is read off a real client's
// screen, and every screen it reads is saved under artifactDir.

// spaceLayoutRail is the rail config with the space layout named, "" for none.
// It names the rail's edge, which a config reload applies afresh: left, where
// railLine reads it.
func spaceLayoutRail(mode string) string {
	rail := spaceUIRail + "position = 'left'\n"
	if mode == "" {
		return rail
	}
	return rail + "spaces = '" + mode + "'\n"
}

// railHeaderRow is the screen row of a section's header: the row whose first
// word is the label, where a session or space row starts with its glyph. -1
// when not drawn.
func railHeaderRow(s tuitest.Screen, label string) int {
	_, rows := s.Size()
	for r := range rows {
		if f := strings.Fields(railRowText(s, r)); len(f) > 0 && f[0] == label {
			return r
		}
	}
	return -1
}

// railFocusMarked reports whether a rail row wears the focus mark in its
// gutter.
func railFocusMarked(s tuitest.Screen, row int) bool {
	return row >= 0 && strings.HasPrefix(railLine(s, row), "▎")
}

// railRowText is a rail row with the gutter, the edge rule and the padding
// taken off.
func railRowText(s tuitest.Screen, row int) string {
	line := strings.TrimSuffix(strings.TrimRight(railLine(s, row), " "), "│")
	return strings.TrimSpace(strings.TrimPrefix(line, "▎"))
}

// sessionsListed is which of the fixture's sessions the sessions section lists,
// top to bottom: the rows below its header and above the next header.
func sessionsListed(s tuitest.Screen) []string {
	top := railHeaderRow(s, "sessions")
	if top < 0 {
		return nil
	}
	end := railHeaderRow(s, "terminals")
	if end < 0 {
		_, end = s.Size()
	}
	var out []string
	for r := top + 1; r < end; r++ {
		line := railLine(s, r)
		for _, name := range []string{"e2e-loose", "e2e-b1", "e2e-a1", "e2e-a2", "alpha", "beta"} {
			if strings.Contains(line, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// spaceRowsListed is the spaces section's rows, each as its text, top to
// bottom.
func spaceRowsListed(s tuitest.Screen) []string {
	top := railHeaderRow(s, "spaces")
	if top < 0 {
		return nil
	}
	var out []string
	for r := top + 1; r != railHeaderRow(s, "sessions"); r++ {
		text := railRowText(s, r)
		if text == "" {
			break
		}
		out = append(out, text)
	}
	return out
}

// useSession attaches a client to session, types into its pane, which is the
// person using it, and detaches. A use is recorded in whole seconds on the
// wire, so it waits past the second it happened in.
func useSession(t *testing.T, base, session string) {
	t.Helper()
	term := attachSpaceUI(t, base, session, 120, 40)
	if err := term.SendKeys("echo used", tuitest.Enter); err != nil {
		t.Fatalf("type into %s: %v", session, err)
	}
	if err := term.WaitForText("used", uiTimeout); err != nil {
		t.Fatalf("typing into %s never reached its pane: %v\n%s", session, err, term.Snapshot())
	}
	// The client reports a use at most once a second; give the report time to
	// land before the client goes.
	time.Sleep(1500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach from %s: %v", session, err)
	}
	waitExit(t, term, "after using "+session)
	time.Sleep(1100 * time.Millisecond)
}

// TestSpaceSplitLayout: with spaces = split the rail draws a spaces section
// directly above the sessions section, one row per space in its first
// member's order and a none row last, each with its session count, the
// attached mark on the space a client is in and the focus mark on the current
// one; the sessions section lists only the current space's sessions, with no
// headings. A click on a space's row and Enter on another switch the client to
// that space's session the person used last, and the sessions section follows.
//
// How it could pass wrongly, and what stops it:
//   - the spaces drawn as headings inside sessions (grouped): the spaces
//     header must be its own section header, on the row right above the
//     sessions header's block, and the sessions section must hold no space
//     name at all.
//   - the sessions section unfiltered: from e2e-loose it must list e2e-loose
//     alone, and after the switch e2e-a1 and e2e-a2 alone.
//   - the switch landing on a space's first or newest session, or on the one
//     with the latest activity, rather than the one used last: e2e-a1 is used
//     and is the older of alpha's two, and e2e-a2 is then given later activity
//     with no use (a window the CLI opens in it), so only the use rule picks
//     e2e-a1. The client is asserted on through list-clients, not the screen.
//   - the focus mark drawn on the first row whatever the space: it must be on
//     none from e2e-loose, then on alpha, then on beta.
//   - keyboard reaching the row only because the cursor happened to start
//     there: the cursor starts on the attached session in the sessions
//     section, and h has to step it up into the spaces section.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space layouts").
func TestSpaceSplitLayout(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail("split"))
	spaceUIFixture(t, base, true)

	useSession(t, base, "e2e-a1")
	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-a2", "--no-focus"); err != nil {
		t.Fatalf("open a window in e2e-a2: %v: %s", err, out)
	}
	time.Sleep(1100 * time.Millisecond)

	term := attachSpaceUI(t, base, "e2e-loose", 120, 40)
	s := waitSpaceRail(t, term, "the split rail", "spaces", "beta", "alpha", "none", "e2e-loose")
	saveArtifact(t, term, dir, "split-from-none")

	spacesHdr, sessionsHdr := railHeaderRow(s, "spaces"), railHeaderRow(s, "sessions")
	rows := spaceRowsListed(s)
	if spacesHdr < 0 || sessionsHdr != spacesHdr+len(rows)+1 {
		t.Fatalf("ASSERTION: the spaces header is on row %d with %d rows and the sessions header on %d, want sessions right under the spaces section\n%s",
			spacesHdr, len(rows), sessionsHdr, term.Snapshot())
	}
	want := []string{"beta", "alpha", "none"}
	if len(rows) != len(want) {
		t.Fatalf("ASSERTION: the spaces section has rows %q, want beta, alpha, none\n%s", rows, term.Snapshot())
	}
	counts := []string{"1", "2", "1"}
	for i, text := range rows {
		if !strings.Contains(text, want[i]) || !strings.HasSuffix(text, " "+counts[i]) {
			t.Fatalf("ASSERTION: spaces row %d is %q, want %s with %s sessions\n%s", i, text, want[i], counts[i], term.Snapshot())
		}
	}
	if !strings.Contains(rows[2], "attached") || strings.Contains(rows[0], "attached") || strings.Contains(rows[1], "attached") {
		t.Fatalf("ASSERTION: the attached mark is on %q, want it on none alone, the space this client is in\n%s", rows, term.Snapshot())
	}
	focus := []bool{railFocusMarked(s, spacesHdr+1), railFocusMarked(s, spacesHdr+2), railFocusMarked(s, spacesHdr+3)}
	if focus[0] || focus[1] || !focus[2] {
		t.Fatalf("ASSERTION: the focus marks on beta, alpha, none are %v, want none alone\n%s", focus, term.Snapshot())
	}
	if got := sessionsListed(s); len(got) != 1 || got[0] != "e2e-loose" {
		t.Fatalf("ASSERTION: from e2e-loose the sessions section lists %q, want e2e-loose alone\n%s", got, term.Snapshot())
	}

	// A click on alpha's row lands on e2e-a1, the session of alpha used last.
	mouseClick(t, term, 5, spacesHdr+2, tuitest.MouseLeft, 0)
	clientShows(t, base, "e2e-a1")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		got := sessionsListed(s)
		return len(got) == 2 && got[0] == "e2e-a1" && got[1] == "e2e-a2" && railFocusMarked(s, railHeaderRow(s, "spaces")+2)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after the click the sessions section is not alpha's two, with alpha's row marked: %v\n%s", err, term.Snapshot())
	}
	// The attached mark moves with the client, sooner than the listing's
	// three-second poll would bring it.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		rows := spaceRowsListed(s)
		return len(rows) == 3 && strings.Contains(rows[1], "attached") && !strings.Contains(rows[2], "attached")
	}, 2500*time.Millisecond); err != nil {
		t.Fatalf("ASSERTION: after the switch the attached mark is not on alpha alone: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "split-alpha-clicked")
	if out, _ := tuiosCLI(t, base, "list-clients", "--json"); strings.Contains(out, `"e2e-a2"`) {
		t.Fatalf("ASSERTION: the click switched a client to e2e-a2, the session with the later activity, not the one used last\n%s", out)
	}

	// Enter on beta, reached from the sessions section by stepping up a
	// section, lands on e2e-b1.
	windowManagementMode(t, term)
	sendKeys(t, term, "s")
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "h")
	saveArtifact(t, term, dir, "split-cursor-in-spaces")
	sendKeys(t, term, tuitest.Enter)
	clientShows(t, base, "e2e-b1")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		got := sessionsListed(s)
		return len(got) == 1 && got[0] == "e2e-b1" && railFocusMarked(s, railHeaderRow(s, "spaces")+1)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Enter on beta did not leave the sessions section on e2e-b1 with beta's row marked: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "split-beta-entered")
	alive(t, term, "after switching spaces from the rail")
}

// TestSpaceOffLayout: with spaces = off the sessions section is one flat list
// in the daemon's order, with no space heading and no spaces section, while
// the dock still says which space the session is in.
//
// How it could pass wrongly, and what stops it:
//   - headings dropped but the rows still grouped: e2e-loose, made first,
//     must be drawn first, above the spaced sessions.
//   - spaced rows still stepped in: every session must start on one column.
//   - the setting reaching the dock as well: the dock must still say
//     alpha > e2e-a1, since the setting is the rail's alone.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space layouts").
func TestSpaceOffLayout(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail("off"))
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	s := waitSpaceRail(t, term, "the flat rail", "e2e-loose", "e2e-b1", "e2e-a1", "e2e-a2")
	saveArtifact(t, term, dir, "off")

	if got := sessionsListed(s); strings.Join(got, ",") != "e2e-loose,e2e-b1,e2e-a1,e2e-a2" {
		t.Fatalf("ASSERTION: with spaces off the sessions section lists %q, want e2e-loose, e2e-b1, e2e-a1, e2e-a2 and no heading\n%s", got, term.Snapshot())
	}
	if railHeaderRow(s, "spaces") >= 0 {
		t.Fatalf("ASSERTION: with spaces off the rail draws a spaces section\n%s", term.Snapshot())
	}
	col := nameColOf(s, railRowOf(s, "e2e-loose"), "e2e-loose")
	for _, name := range []string{"e2e-b1", "e2e-a1", "e2e-a2"} {
		if c := nameColOf(s, railRowOf(s, name), name); c != col {
			t.Fatalf("ASSERTION: %s starts on column %d and e2e-loose on %d: a spaced row is stepped in\n%s", name, c, col, term.Snapshot())
		}
	}
	if !strings.Contains(dockRow(s), "alpha > e2e-a1") {
		t.Fatalf("ASSERTION: with the rail's spaces off the dock no longer says alpha > e2e-a1: %q", dockRow(s))
	}
}

// TestSpaceLayoutLiveSwitch: a config saved while attached changes the rail's
// space layout without a reattach, grouped to split and back.
//
// How it could pass wrongly, and what stops it:
//   - a reload that never re-renders the rail (a cached frame): the same
//     client must draw the spaces section after the save and lose it again
//     after the second save, and the sessions section must change with it.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space layouts").
func TestSpaceLayoutLiveSwitch(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail(""))
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	s := waitSpaceRail(t, term, "the grouped rail", "beta", "alpha", "e2e-b1", "e2e-loose")
	saveArtifact(t, term, dir, "live-grouped")
	if railHeaderRow(s, "spaces") >= 0 {
		t.Fatalf("ASSERTION: the default layout draws a spaces section\n%s", term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, spaceLayoutRail("split"))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		got := sessionsListed(s)
		return railHeaderRow(s, "spaces") >= 0 && len(got) == 2 && got[0] == "e2e-a1" && got[1] == "e2e-a2"
	}, configWatchTimeout); err != nil {
		t.Fatalf("ASSERTION: saving spaces = split did not give the attached client the split rail: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "live-split")

	saveConfigLikeAnEditor(t, base, spaceLayoutRail("grouped"))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railHeaderRow(s, "spaces") < 0 && railRowOf(s, "e2e-b1") >= 0 && railRowOf(s, "e2e-loose") >= 0
	}, configWatchTimeout); err != nil {
		t.Fatalf("ASSERTION: saving spaces = grouped did not put the grouped rail back: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "live-grouped-again")
	alive(t, term, "after two layout reloads")
}

// settingsSpacesRow is the screen line of the settings page's Spaces row, or
// "" when the page shows none.
func settingsSpacesRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if i := strings.Index(line, " Spaces "); i >= 0 && strings.Contains(line[i:], "‹") {
			return line
		}
	}
	return ""
}

// openSidebarTab opens the settings page on its Sidebar tab, the third, and
// waits for that tab's own rows. The client must be in window management mode.
func openSidebarTab(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openSettings(t, term)
	sendKeys(t, term, "3")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Header case") && strings.Contains(s.Text(), "Sections")
	}, uiTimeout); err != nil {
		t.Fatalf("3 never showed the Sidebar tab: %v\n%s", err, term.Snapshot())
	}
}

// TestSpaceLayoutSettingsRow: the settings page offers the Spaces row on its
// Sidebar tab only once a session is in a space; choosing a value there
// changes the rail at once and writes the value to config.toml.
//
// How it could pass wrongly, and what stops it:
//   - a row that is always there: the same page, with no space anywhere, must
//     not carry it, and must after a session is filed under one.
//   - a row that changes the panel and not the rail: the rail must draw the
//     spaces section once the value reads split, with the page still open.
//   - a value applied and not persisted: config.toml must hold the value.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space layouts").
func TestSpaceLayoutSettingsRow(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail(""))
	spaceUIFixture(t, base, false)

	term := attachSpaceUI(t, base, "e2e-a1", 140, 50)
	waitSpaceRail(t, term, "the rail", "e2e-loose", "e2e-a1")
	windowManagementMode(t, term)
	openSidebarTab(t, term)
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "settings-no-space")
	if row := settingsSpacesRow(term.Screen()); row != "" {
		t.Fatalf("ASSERTION: with no session in a space the Sidebar tab carries the Spaces row %q\n%s", row, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Header case") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the settings page: %v\n%s", err, term.Snapshot())
	}

	setSpace(t, base, "e2e-b1", "beta")
	setSpace(t, base, "e2e-a1", "alpha")
	waitSpaceRail(t, term, "the grouped rail", "beta", "alpha")
	openSidebarTab(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(settingsSpacesRow(s), "grouped")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: with a session in a space the Sidebar tab has no Spaces row reading grouped: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "settings-spaces-row")

	// The tab opens on its first row; the Spaces row sits under Sections.
	for range 4 {
		sendKeys(t, term, tuitest.Down)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return selectedSettingsRow(s, "Spaces") != "" }, uiTimeout); err != nil {
		t.Fatalf("four rows down did not put the Spaces row under the cursor: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Right)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(selectedSettingsRow(s, "Spaces"), "split") && railHeaderRow(s, "spaces") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: → on the Spaces row did not read split and draw the spaces section: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "settings-split")

	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(configPathIn(base))
		if strings.Contains(string(data), "spaces") && strings.Contains(string(data), "split") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: config.toml never recorded spaces = split:\n%s", data)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after choosing a space layout in settings")
}

// spaceLayoutScreen draws the rail with no session in any space under one
// space layout, "" for a config that names none.
func spaceLayoutScreen(t *testing.T, mode string) string {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, spaceLayoutRail(mode))
	spaceUIFixture(t, base, false)
	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	waitSpaceRail(t, term, "the rail", "e2e-loose", "e2e-b1", "e2e-a1", "e2e-a2")
	label := mode
	if label == "" {
		label = "baseline"
	}
	saveArtifact(t, term, artifactDir(t), "no-space-"+label)
	// Styled, so a mark or an ink that moved fails it as well as a cell.
	return maskRunIdentity(term.SnapshotStyled())
}

// TestSpaceLayoutsInvisibleWithoutSpaces: with no session in any space, the
// rail every space layout draws is the rail of a build without spaces, screen
// for screen.
//
// It needs that build, one of upstream/main, which has no space UI: name it
// with TUIOS_E2E_BASELINE_BIN. Without it the
// test is skipped and says so. The baseline gets the config with no spaces
// key, which it does not know; each layout gets its own.
//
// How it could pass wrongly, and what stops it:
//   - comparing this build against itself: the baseline is another binary.
//   - comparing only that nothing about spaces is drawn: the whole screen is
//     compared, so an empty spaces section that still cost a header or a
//     line, or a pinned block that moved, fails it.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space layouts").
func TestSpaceLayoutsInvisibleWithoutSpaces(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream/main, which has no space UI, and name it there")
	}
	prev := tuiosBin
	tuiosBin = baseline
	restore := func() { tuiosBin = prev }
	// A fatal step inside must not leave every later test on the baseline.
	defer restore()
	then := spaceLayoutScreen(t, "")
	restore()

	for _, mode := range []string{"grouped", "split", "off"} {
		if now := spaceLayoutScreen(t, mode); now != then {
			t.Errorf("ASSERTION: with no spaces and spaces = %s the rail differs from the baseline's\nthis build:\n%s\nbaseline:\n%s", mode, now, then)
		}
	}
}
