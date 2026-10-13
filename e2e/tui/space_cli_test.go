package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The space CLI: tuios new --space, rename-space, kill-space, switch-space and
// the grouped tuios ls. The data model and list-spaces are space_test.go's.

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

// wantSpaces fails unless list-spaces holds exactly want, members sorted.
func wantSpaces(t *testing.T, base, when string, want map[string][]string) {
	t.Helper()
	got := listSpaces(t, base)
	for _, members := range got {
		slices.Sort(members)
	}
	equal := len(got) == len(want)
	for space, members := range want {
		equal = equal && slices.Equal(got[space], members)
	}
	if !equal {
		t.Fatalf("ASSERTION: %s, list-spaces is %v, want %v", when, got, want)
	}
}

// liveSessions is the names ls --json lists, sorted. It also checks that the
// listing is still a flat array of session rows, which every script reading
// it expects.
func liveSessions(t *testing.T, base string) []string {
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
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names
}

// TestNewSpaceFilesTheSession: tuios new --space files the session under the
// space as it is made, detached and attached, and a name the space rules
// refuse makes no session at all.
//
// How it could pass wrongly, and what stops it:
//   - the flag parsed and dropped: list-spaces must hold the sessions, which
//     only the daemon's own record can say.
//   - the space set on the detached path only: the attached form is checked
//     too, through a real client.
//   - a bad space refused after the session was made: the listing must not
//     hold the session afterwards.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space CLI").
func TestNewSpaceFilesTheSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-api", "payments")
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	wantSpaces(t, base, "after tuios new --space --detach", map[string][]string{"payments": {"e2e-api"}})

	out, err := tuiosCLI(t, base, "new", "e2e-bad", "--detach", "--space", "bad/name")
	if err == nil {
		t.Fatalf("ASSERTION: tuios new --space bad/name succeeded: %s", out)
	}
	if slices.Contains(liveSessions(t, base), "e2e-bad") {
		t.Fatalf("ASSERTION: a refused space still made the session e2e-bad")
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-glob", "--global", "--space", "payments"); err == nil {
		t.Fatalf("ASSERTION: tuios new --global --space succeeded, but the global path does not take a space: %s", out)
	}

	// An attached tuios new makes the session empty and the client opens its
	// first window, as the option says. --space keeps that.
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	term := startIn(t, base, startOpts{args: []string{"new", "e2e-live", "--space", "payments"}})
	waitWindowCount(t, term, 1, "the attached space session's first window")
	clientShows(t, base, "e2e-live")
	wantSpaces(t, base, "after an attached tuios new --space", map[string][]string{"payments": {"e2e-api", "e2e-live"}})
	saveFrame(t, term, "new-space-attached")
}

// TestRenameSpaceMovesEveryMember: rename-space moves every session of a
// space and no other, and running it again, whole or after a rename that
// stopped part way, finishes it.
//
// How it could pass wrongly, and what stops it:
//   - only the first member moved: the space has two, and both must move.
//   - every session moved: a session in another space and one in none are
//     checked to stay where they were.
//   - a re-run answered with an error, or quietly undoing the rename: the
//     second run must succeed and leave the listing as it was.
//   - a split left by a crash not repaired: one member is put back under the
//     old name by hand, and the same rename must move it.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space CLI").
func TestRenameSpaceMovesEveryMember(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-a", "payments")
	newInSpace(t, base, "e2e-b", "payments")
	newInSpace(t, base, "e2e-c", "search")
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	after := map[string][]string{"billing": {"e2e-a", "e2e-b"}, "search": {"e2e-c"}}

	if out, err := tuiosCLI(t, base, "rename-space", "payments", "bad/name"); err == nil {
		t.Fatalf("ASSERTION: rename-space to a name holding a slash succeeded: %s", out)
	}
	if out, err := tuiosCLI(t, base, "rename-space", "nowhere", "billing"); err == nil || !strings.Contains(out, "payments") {
		t.Fatalf("ASSERTION: rename-space of a missing space was not refused with the spaces there are: %v: %s", err, out)
	}
	wantSpaces(t, base, "after the refused renames", map[string][]string{"payments": {"e2e-a", "e2e-b"}, "search": {"e2e-c"}})

	out, err := tuiosCLI(t, base, "rename-space", "payments", "billing")
	if err != nil {
		t.Fatalf("ASSERTION: rename-space failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Fatalf("ASSERTION: rename-space did not report moving 2 sessions: %s", out)
	}
	wantSpaces(t, base, "after the rename", after)

	out, err = tuiosCLI(t, base, "rename-space", "payments", "billing")
	if err != nil {
		t.Fatalf("ASSERTION: running the same rename again failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "already") {
		t.Fatalf("ASSERTION: the second rename did not say it was done already: %s", out)
	}
	wantSpaces(t, base, "after the same rename ran again", after)

	// What a crash part way through leaves: one member under each name.
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-a","space":"payments"}`); err != nil {
		t.Fatalf("split the space: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "rename-space", "payments", "billing"); err != nil {
		t.Fatalf("ASSERTION: the rename that repairs a split failed: %v: %s", err, out)
	}
	wantSpaces(t, base, "after the rename repaired a split", after)
}

// TestKillSpaceNeedsForceAndCascades: kill-space without --force kills
// nothing and says how many sessions it would kill, and with --force kills
// every session of the space and no other.
//
// How it could pass wrongly, and what stops it:
//   - the refusal made only by the CLI: the verb is also called directly
//     without force, and must refuse too.
//   - a refusal that killed anyway: the listing is read after each refusal.
//   - only the first member killed: the space has two.
//   - other sessions killed: one in another space and one in none must stay.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space CLI").
func TestKillSpaceNeedsForceAndCascades(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-doomed-1", "doomed")
	newInSpace(t, base, "e2e-doomed-2", "doomed")
	newInSpace(t, base, "e2e-kept", "kept")
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	all := []string{"e2e-doomed-1", "e2e-doomed-2", "e2e-kept", "e2e-loose"}

	out, err := tuiosCLI(t, base, "kill-space", "doomed")
	if err == nil {
		t.Fatalf("ASSERTION: kill-space without --force succeeded: %s", out)
	}
	for _, want := range []string{"2 sessions", "e2e-doomed-1", "e2e-doomed-2", "--force"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASSERTION: the refusal does not name %q: %s", want, out)
		}
	}
	if got := liveSessions(t, base); !slices.Equal(got, all) {
		t.Fatalf("ASSERTION: after a refused kill-space the sessions are %v, want %v", got, all)
	}

	out, err = tuiosCLI(t, base, "call", "kill-space", `{"space":"doomed"}`)
	if err == nil || !strings.Contains(out, "invalid_params") {
		t.Fatalf("ASSERTION: the kill-space verb without force was not refused as invalid_params: %v: %s", err, out)
	}
	if got := liveSessions(t, base); !slices.Equal(got, all) {
		t.Fatalf("ASSERTION: after the verb refused, the sessions are %v, want %v", got, all)
	}

	out, err = tuiosCLI(t, base, "kill-space", "doomed", "--force")
	if err != nil {
		t.Fatalf("ASSERTION: kill-space --force failed: %v: %s", err, out)
	}
	if got, want := liveSessions(t, base), []string{"e2e-kept", "e2e-loose"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after kill-space --force the sessions are %v, want %v", got, want)
	}
	wantSpaces(t, base, "after kill-space --force", map[string][]string{"kept": {"e2e-kept"}})

	if out, err := tuiosCLI(t, base, "kill-space", "doomed", "--force"); err == nil {
		t.Fatalf("ASSERTION: kill-space of a space with no sessions left succeeded: %s", out)
	}
}

// TestSwitchSpaceLandsOnTheLastUsedSession: switch-space moves the attached
// client to the session of the space the person used last.
//
// The space holds three sessions. The person types in e2e-a and then in e2e-b,
// and then windows are opened in e2e-c from the CLI, which bumps its activity
// but is not the person. So the session used last is e2e-b: not the first by
// name, not the last by name, and not the latest by activity.
//
// How it could pass wrongly, and what stops it:
//   - the first or last member picked: e2e-b is neither.
//   - the pick by activity: e2e-c is the latest by activity.
//   - a client that was on e2e-b already: the client sits on a session in
//     no space when switch-space runs.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space CLI").
func TestSwitchSpaceLandsOnTheLastUsedSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-home", "--detach"); err != nil {
		t.Fatalf("create e2e-home: %v: %s", err, out)
	}
	for _, name := range []string{"e2e-a", "e2e-b", "e2e-c"} {
		newInSpace(t, base, name, "payments")
	}
	typeInSession(t, base, "e2e-a", 0)
	// Uses are compared at full precision, but a whole second apart they
	// differ in the seconds list-spaces reports too.
	time.Sleep(1100 * time.Millisecond)
	typeInSession(t, base, "e2e-b", 1)
	for range 2 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-c"); err != nil {
			t.Fatalf("new-window in e2e-c: %v: %s", err, out)
		}
	}

	// A client on a session in no space, never typed in.
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	clientShows(t, base, "e2e-home")

	if out, err := tuiosCLI(t, base, "switch-space", "nowhere"); err == nil || !strings.Contains(out, "payments") {
		t.Fatalf("ASSERTION: switch-space to a missing space was not refused with the spaces there are: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "switch-space", "-s", "e2e-home", "payments")
	if err != nil {
		t.Fatalf("ASSERTION: switch-space failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "session e2e-b of space payments") {
		t.Fatalf("ASSERTION: switch-space did not go to e2e-b, the session used last: %s", out)
	}
	if err := term.WaitForText("Session: e2e-b", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never showed e2e-b: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "e2e-b")
	saveFrame(t, term, "switch-space")
}

// TestLsGroupsSessionsBySpace: tuios ls draws one table per space, in name
// order, and the sessions in no space last. ls --json stays a flat list with
// each row's space, and with no space anywhere ls has no headings at all.
//
// How it could pass wrongly, and what stops it:
//   - headings printed over one table of every session: each session must
//     come after its own heading and before the next one.
//   - the grouping applied to --json too: the JSON must decode as a flat
//     array of rows.
//   - headings with no spaces at all, which changes every existing listing:
//     ls is read once before any session is in a space.
//
// The grouped listing is written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space CLI").
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
	if strings.Contains(out, "Space:") || strings.Contains(out, "No space") {
		t.Fatalf("ASSERTION: with no session in a space, ls shows space headings:\n%s", out)
	}

	// Made out of name order, so the listing's order is ls's own.
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
	// Each line in the order it must appear. A session name must sit between
	// its own heading and the next.
	order := []string{"Space: alpha", "e2e-alpha-1", "e2e-alpha-2", "Space: zeta", "e2e-zulu", "No space", "e2e-loose"}
	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("ASSERTION: ls does not list %q after what comes before it in %v:\n%s", want, order, out)
		}
		at += i + len(want)
	}
	if strings.Count(out, "e2e-zulu") != 1 || strings.Count(out, "e2e-loose") != 1 {
		t.Fatalf("ASSERTION: a session is listed more than once:\n%s", out)
	}

	raw, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v: %s", err, raw)
	}
	var rows []struct {
		Name  string `json:"name"`
		Space string `json:"space"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("ASSERTION: ls --json is not a flat list of sessions: %v\n%s", err, raw)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Space
	}
	want := map[string]string{"e2e-loose": "", "e2e-zulu": "zeta", "e2e-alpha-1": "alpha", "e2e-alpha-2": "alpha"}
	if len(got) != len(want) {
		t.Fatalf("ASSERTION: ls --json lists %v, want %v", got, want)
	}
	for name, space := range want {
		if sp, ok := got[name]; !ok || sp != space {
			t.Fatalf("ASSERTION: ls --json lists %v, want %v", got, want)
		}
	}
}
