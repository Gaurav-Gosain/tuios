package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Spaces as a label: SessionState.Space, set by tuios new --space and tuios
// set-session-space (the new-session and set-session-space verbs), kept
// across a daemon restart, and grouped by tuios ls.

// globalSpaceRefusal is session.GlobalSpaceRefusal. tuios new --global
// --space and set-session-space on a global session both say it.
const globalSpaceRefusal = "a global session cannot be in a space"

// ansiEscape matches the styling ls puts on a heading, so the text can be
// read line by line.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]`)

// sessionSpaces is every live session and the space ls --json files it under,
// "" for none. It also checks that the listing is still a flat array of
// session rows, which every script reading it expects.
func sessionSpaces(t *testing.T, base string) map[string]string {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v: %s", err, out)
	}
	var rows []struct {
		Name  string `json:"name"`
		Space string `json:"space"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ASSERTION: ls --json is not a flat list of sessions: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Space
	}
	return got
}

// wantSessionSpaces fails unless the live sessions and their spaces are
// exactly want.
func wantSessionSpaces(t *testing.T, base, when string, want map[string]string) {
	t.Helper()
	got := sessionSpaces(t, base)
	equal := len(got) == len(want)
	for name, space := range want {
		sp, ok := got[name]
		equal = equal && ok && sp == space
	}
	if !equal {
		t.Fatalf("ASSERTION: %s, the sessions and their spaces are %v, want %v", when, got, want)
	}
}

// newInSpace makes a detached session filed under space with tuios new.
func newInSpace(t *testing.T, base, name, space string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "new", name, "--detach", "--space", space)
	if err != nil {
		t.Fatalf("tuios new %s --space %s: %v: %s", name, space, err, out)
	}
	if !strings.Contains(out, "in space '"+space+"'") {
		t.Fatalf("ASSERTION: tuios new --space did not say the session is in space %s: %s", space, out)
	}
}

// TestNewSpaceFilesTheSession: tuios new --space files the session under the
// space as it is made, detached and attached, and a name the space rules
// refuse makes no session at all.
//
// How it could pass wrongly, and what stops it:
//   - the flag parsed and dropped: the daemon's own listing must carry the
//     space, which only its record can say.
//   - the space set on the detached path only: the attached form is checked
//     too, through a real client.
//   - a bad space refused after the session was made: the listing must not
//     hold the session afterwards.
//   - every session filed under the space: a session made without --space
//     must stay in none.
//   - an attached create with no terminal leaving its session behind: the
//     listing must not hold it after the refusal.
//   - the attached pane given the daemon's fallback terminal: the caller's
//     TERM is one the daemon never falls back to.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestNewSpaceFilesTheSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-api", "payments")
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after tuios new --space --detach", map[string]string{"e2e-api": "payments", "e2e-loose": ""})

	if out, err := tuiosCLI(t, base, "new", "e2e-bad", "--detach", "--space", "bad/name"); err == nil {
		t.Fatalf("ASSERTION: tuios new --space bad/name succeeded: %s", out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-long", "--detach", "--space", strings.Repeat("x", 65)); err == nil || !strings.Contains(out, "64 bytes") {
		t.Fatalf("ASSERTION: tuios new --space took a 65-byte space name: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-glob", "--global", "--space", "payments"); err == nil || !strings.Contains(out, globalSpaceRefusal) {
		t.Fatalf("ASSERTION: tuios new --global --space was not refused with %q: %v: %s", globalSpaceRefusal, err, out)
	}
	wantSessionSpaces(t, base, "after the refused creates", map[string]string{"e2e-api": "payments", "e2e-loose": ""})

	// An attached tuios new from a caller with no terminal fails before it
	// makes anything, as plain tuios new does. tuiosCLI runs without a tty.
	if out, err := tuiosCLI(t, base, "new", "e2e-notty", "--space", "payments"); err == nil {
		t.Fatalf("ASSERTION: an attached tuios new --space with no terminal succeeded: %s", out)
	}
	wantSessionSpaces(t, base, "after an attached tuios new --space with no terminal", map[string]string{"e2e-api": "payments", "e2e-loose": ""})

	// An attached tuios new makes the session empty and the client opens its
	// first window, as the option says. --space keeps that.
	//
	// --space makes the session over the verb connection, not the path plain
	// tuios new takes, so its pane must still get the caller's terminal. The
	// caller's TERM is not a fallback the daemon has, so a match is the
	// caller's value.
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-live", "--space", "payments"},
		env:  []string{"TERM=screen-256color", "COLORTERM=truecolor"},
	})
	waitWindowCount(t, term, 1, "the attached space session's first window")
	clientShows(t, base, "e2e-live")
	wantSessionSpaces(t, base, "after an attached tuios new --space",
		map[string]string{"e2e-api": "payments", "e2e-loose": "", "e2e-live": "payments"})
	if got, pane := paneTerms(t, base, "e2e-live"); got != "TERM=screen-256color COLORTERM=truecolor" {
		t.Fatalf("ASSERTION: an attached tuios new --space gave its pane %q, want the caller's \"TERM=screen-256color COLORTERM=truecolor\"\n%s", got, pane)
	}
	saveFrame(t, term, "new-space-attached")
}

// TestSetSessionSpaceSetsAndClears: tuios set-session-space files a session
// under a space, moves it to another, and with no space takes it out,
// touching no other session. Surrounding whitespace is trimmed, as the
// sibling set-session-* verbs trim, and a name the space rules refuse changes
// nothing. With no -s it acts on the most recently active session.
//
// How it could pass wrongly, and what stops it:
//   - the verb answering success and recording nothing: the listing is read
//     from the daemon after every step.
//   - clearing that leaves the old space: the session must read as in none.
//   - the wrong session moved: a second session in a space of its own must
//     keep it through every step.
//   - a refused name applied anyway: the listing must be unchanged after it.
//   - padding kept, or refused instead of trimmed: the padded name must be
//     listed as the bare name.
//   - -s ignored: e2e-work is named with -s while e2e-other is the most
//     recently active session, so the wrong target moves the wrong row.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestSetSessionSpaceSetsAndClears(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-work", "--detach"); err != nil {
		t.Fatalf("create e2e-work: %v: %s", err, out)
	}
	newInSpace(t, base, "e2e-other", "search")

	out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-work", "payments")
	if err != nil || !strings.Contains(out, "in space payments") {
		t.Fatalf("ASSERTION: set-session-space -s e2e-work payments: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after set-session-space payments", map[string]string{"e2e-work": "payments", "e2e-other": "search"})

	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-work", "  billing\t"); err != nil || !strings.Contains(out, "in space billing.") {
		t.Fatalf("ASSERTION: set-session-space did not trim \"  billing\\t\" to billing: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after set-session-space with a padded billing", map[string]string{"e2e-work": "billing", "e2e-other": "search"})

	for _, bad := range []string{"bad/name", "a\\b", "ctl\x01", strings.Repeat("x", 65)} {
		out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-work", bad)
		if err == nil || !strings.Contains(out, "space") {
			t.Fatalf("ASSERTION: set-session-space took the space name %q: %v: %s", bad, err, out)
		}
	}
	out, err = tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-work","space":"`+strings.Repeat("x", 65)+`"}`)
	if err == nil || !strings.Contains(out, "invalid_params") || !strings.Contains(out, "64 bytes") {
		t.Fatalf("ASSERTION: set-session-space did not refuse a 65-byte space name as invalid_params: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after the refused names", map[string]string{"e2e-work": "billing", "e2e-other": "search"})

	out, err = tuiosCLI(t, base, "set-session-space", "-s", "e2e-work")
	if err != nil || !strings.Contains(out, "in no space") {
		t.Fatalf("ASSERTION: set-session-space -s e2e-work with no space: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after clearing the space", map[string]string{"e2e-work": "", "e2e-other": "search"})

	// The verb's own clearing forms: an empty space, and space left out.
	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-work", "billing"); err != nil {
		t.Fatalf("set-session-space -s e2e-work billing: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-work","space":"   "}`); err != nil || !strings.Contains(out, "session_space_set") {
		t.Fatalf("ASSERTION: set-session-space with a blank space did not clear: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after a blank space", map[string]string{"e2e-work": "", "e2e-other": "search"})
	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-work", "billing"); err != nil {
		t.Fatalf("set-session-space -s e2e-work billing: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-work"}`); err != nil || !strings.Contains(out, "session_space_set") {
		t.Fatalf("ASSERTION: set-session-space with no space param did not clear: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after a missing space", map[string]string{"e2e-work": "", "e2e-other": "search"})
}

// TestAGlobalSessionIsInNoSpace: a global session holds panes from more than
// one machine and is never in a space, so set-session-space refuses to file
// one, and tuios new refuses --space with --global, both with the same
// message, while an ordinary session beside it is filed as usual.
//
// How it could pass wrongly, and what stops it:
//   - the refusal reported but the session filed anyway: the listing must
//     show the global session in no space afterwards.
//   - the verb refusing everything: the ordinary session is filed in the
//     same fixture (the positive half).
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestAGlobalSessionIsInNoSpace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-g", "--global"); err != nil {
		t.Fatalf("create the global session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-a", "--detach"); err != nil {
		t.Fatalf("create e2e-a: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-g", "s")
	if err == nil || !strings.Contains(out, globalSpaceRefusal) {
		t.Fatalf("ASSERTION: set-session-space filed a global session under a space, or refused it without %q: %v: %s", globalSpaceRefusal, err, out)
	}
	out, err = tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-g","space":"s"}`)
	if err == nil || !strings.Contains(out, "invalid_params") {
		t.Fatalf("ASSERTION: set-session-space did not refuse a global session as invalid_params: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "new", "e2e-g2", "--global", "--space", "s")
	if err == nil || !strings.Contains(out, globalSpaceRefusal) {
		t.Fatalf("ASSERTION: tuios new --global --space was not refused with %q: %v: %s", globalSpaceRefusal, err, out)
	}
	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-a", "s"); err != nil {
		t.Fatalf("ASSERTION: set-session-space -s e2e-a s, beside the global session: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after the refusals", map[string]string{"e2e-g": "", "e2e-a": "s"})

	// Taking a global session out of any space asks for nothing it cannot
	// be, so it is not refused.
	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-g"); err != nil {
		t.Fatalf("ASSERTION: set-session-space with no space on the global session: %v: %s", err, out)
	}
}

// TestSpaceSurvivesTheDaemon files one session under a space with
// set-session-space and makes another in a space with tuios new --space,
// leaves a third in none, restarts the daemon, and reads the spaces back from
// tuios ls --json.
//
// How it could pass wrongly, and what stops it:
//   - set-session-space accepted and ignored: the listing is read before the
//     restart too, so a space that was never recorded fails there.
//   - the space saved but dropped on restore (the restore's client sync keeps
//     the new session's own value): the restored session must be listed in
//     the space after the restart, which only the restore can put back.
//   - the sessions read from a daemon that never restarted: the session must
//     be listed as restored.
//   - every session listed in the space: the one left in none must stay so.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestSpaceSurvivesTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"e2e-spaced", "e2e-loose"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-session-space", "-s", "e2e-spaced", "payments"); err != nil {
		t.Fatalf("ASSERTION: set-session-space failed: %v: %s", err, out)
	}
	newInSpace(t, base, "e2e-made", "ledger")
	want := map[string]string{"e2e-spaced": "payments", "e2e-made": "ledger", "e2e-loose": ""}
	wantSessionSpaces(t, base, "before a restart", want)

	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}
	if restored := waitForSessionInfo(t, base, "e2e-spaced"); !restored.Restored {
		t.Fatalf("e2e-spaced is listed but not as restored, so the restart did not happen")
	}
	waitForSessionInfo(t, base, "e2e-made")
	waitForSessionInfo(t, base, "e2e-loose")
	want["e2e-trigger"] = ""
	wantSessionSpaces(t, base, "after a restart", want)
}

// TestLsGroupsSessionsBySpace: with any session in a space, tuios ls draws
// one table per space in name order, each under the space's name, and the
// sessions in no space last with no heading. ls --json stays one flat list,
// with space on each row that has one.
//
// How it could pass wrongly, and what stops it:
//   - headings printed over one table of every session: the listing is read
//     as blocks, and each block must hold only its own sessions.
//   - a heading drawn over the sessions in no space: the last table's first
//     line must be the table's border.
//   - the daemon's order mistaken for the grouping: the sessions are made out
//     of name order, with the loose one first.
//   - the grouping applied to --json too: the JSON must decode as a flat
//     array of rows.
//   - headings with no spaces at all: ls is read once before any session is
//     in a space. TestLsWithoutSpacesIsUnchanged holds that case to a build
//     without spaces.
//
// The grouped listing is written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestLsGroupsSessionsBySpace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("ls: %v: %s", err, out)
	}
	if blocks := lsBlocks(out); len(blocks) != 2 || !strings.HasPrefix(blocks[0], "╭") {
		t.Fatalf("ASSERTION: with no session in a space, ls is not one table with no heading:\n%s", out)
	}

	newInSpace(t, base, "e2e-zulu", "zeta")
	newInSpace(t, base, "e2e-alpha-1", "alpha")
	newInSpace(t, base, "e2e-alpha-2", "alpha")

	out, err = tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("ls: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "ls-grouped.txt"), []byte(out), 0o644); err != nil {
		t.Logf("could not save the listing: %v", err)
	}
	// Blocks: alpha, zeta, the sessions in no space, and the count line.
	blocks := lsBlocks(out)
	if len(blocks) != 4 {
		t.Fatalf("ASSERTION: ls is not three tables and the count, it has %d blocks:\n%s", len(blocks), out)
	}
	groups := []struct {
		heading string
		members []string
	}{
		{"alpha", []string{"e2e-alpha-1", "e2e-alpha-2"}},
		{"zeta", []string{"e2e-zulu"}},
		{"", []string{"e2e-loose"}},
	}
	all := []string{"e2e-alpha-1", "e2e-alpha-2", "e2e-zulu", "e2e-loose"}
	for i, g := range groups {
		block := blocks[i]
		first, _, _ := strings.Cut(block, "\n")
		if g.heading == "" && !strings.HasPrefix(first, "╭") {
			t.Fatalf("ASSERTION: the sessions in no space have a heading %q:\n%s", first, out)
		}
		if g.heading != "" && strings.TrimSpace(first) != g.heading {
			t.Fatalf("ASSERTION: block %d is headed %q, want %q:\n%s", i, first, g.heading, out)
		}
		for _, name := range all {
			if strings.Contains(block, name) != slices.Contains(g.members, name) {
				t.Fatalf("ASSERTION: block %q holds %s wrongly, want exactly %v:\n%s", g.heading, name, g.members, out)
			}
		}
	}

	wantSessionSpaces(t, base, "in ls --json",
		map[string]string{"e2e-loose": "", "e2e-zulu": "zeta", "e2e-alpha-1": "alpha", "e2e-alpha-2": "alpha"})
}

// TestLsWithoutSpacesIsUnchanged: with no session in a space, tuios ls prints
// exactly what a build without spaces prints for the same sessions, byte for
// byte. The same fixture is made on a daemon of each build, and each build's
// own ls reads its own daemon.
//
// It needs that other build: name it with TUIOS_E2E_BASELINE_BIN, a tuios
// built from upstream main. Without it the test is skipped.
//
// How it could pass wrongly, and what stops it:
//   - the two fixtures differing, so a change in ls hides behind a change in
//     the data: both are made by the same steps, and the listing names every
//     session with its window count, status and display name.
//   - a fixture too plain to show a difference: it holds a session with
//     several windows and one with a display name, which ls draws beside the
//     name.
//
// Both listings are written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces as a session
// label").
func TestLsWithoutSpacesIsUnchanged(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream main, which has no spaces, and name it there")
	}
	listing := func(bin string) string {
		base := t.TempDir()
		run := func(args ...string) string {
			t.Helper()
			out, err := tuiosCLIWith(t, bin, base, args...)
			if err != nil {
				t.Fatalf("%s %v: %v: %s", filepath.Base(bin), args, err, out)
			}
			return out
		}
		t.Cleanup(func() { _, _ = tuiosCLIWith(t, bin, base, "kill-server") })
		run("new", "e2e-alpha", "--detach")
		run("new", "e2e-beta", "--detach")
		run("new-window", "-s", "e2e-beta", "--no-focus")
		run("set-session-name", "-s", "e2e-beta", "Beta API")
		return run("ls")
	}
	got := listing(tuiosBin)
	want := listing(baseline)
	dir := artifactDir(t)
	_ = os.WriteFile(filepath.Join(dir, "ls-this-build.txt"), []byte(got), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "ls-baseline.txt"), []byte(want), 0o644)
	if !strings.Contains(got, "e2e-alpha") || !strings.Contains(got, "e2e-beta (Beta API)") || !strings.Contains(got, "2 sessions") {
		t.Fatalf("the fixture's listing does not hold its sessions:\n%s", got)
	}
	if got != want {
		t.Fatalf("ASSERTION: with no session in a space, ls differs from the baseline build's\nthis build:\n%s\nbaseline:\n%s", got, want)
	}
}

// lsBlocks splits tuios ls output, styling stripped, into its blank-line
// separated blocks.
func lsBlocks(out string) []string {
	return strings.Split(strings.TrimSpace(ansiEscape.ReplaceAllString(out, "")), "\n\n")
}

// tuiosCLIWith is tuiosCLI with another tuios binary.
func tuiosCLIWith(t *testing.T, bin, base string, args ...string) (string, error) {
	t.Helper()
	return tuiosCLIBinEnv(t, bin, base, nil, args...)
}
