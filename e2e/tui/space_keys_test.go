package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The keys that walk the sessions of a space: ctrl+b > and ctrl+b < (the
// next_session_in_space and prev_session_in_space actions). Every claim is read
// off a real client attached to a real daemon: which session the daemon says
// the one client shows, and what the client's screen says. The screens are
// saved under artifactDir.
//
// The fixture is spaceUIFixture's: e2e-loose in no space, e2e-b1 in beta,
// e2e-a1 and e2e-a2 in alpha, and here e2e-loose2 in no space made last.
// next_session and prev_session walk them in the order they were made, so
// next_session from e2e-a2 goes to e2e-loose2, prev_session from e2e-a1 to
// e2e-b1, and next_session from e2e-loose to e2e-b1. The steps below include
// each of those, so a key that walked every session lands somewhere else.

// soleClientOn waits until the one attached client shows want, and fails if
// it settles anywhere else. A CLI connection shows no session and is not
// counted.
func soleClientOn(t *testing.T, base, want string) {
	t.Helper()
	var got []string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ := tuiosCLI(t, base, "list-clients", "--json")
		var rows []struct {
			Session string `json:"session"`
		}
		got = got[:0]
		if json.Unmarshal([]byte(out), &rows) == nil {
			for _, r := range rows {
				if r.Session != "" {
					got = append(got, r.Session)
				}
			}
		}
		if slices.Equal(got, []string{want}) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the attached clients show %v, want exactly one on %s", got, want)
}

// pressSpaceKey presses the prefix and key, and waits for the client to land
// on want and settle there.
func pressSpaceKey(t *testing.T, term *tuitest.Terminal, base, key, want string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), key)
	soleClientOn(t, base, want)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, uiTimeout); err != nil {
		t.Fatalf("%s never settled at one window after ctrl+b %s: %v\n%s", want, key, err, term.Snapshot())
	}
	// A switch re-arms input handling, and the first keys after it are dropped
	// without this (see switchSession).
	time.Sleep(insertGuard + 150*time.Millisecond)
}

// TestSpaceKeysCycleInsideTheSpace: > and < walk alpha's two sessions and wrap,
// and never leave alpha. From a session in no space they walk only the
// sessions in no space.
//
// How it could pass wrongly, and what stops it:
//   - the keys bound to nothing: the client would stay put, and every step
//     must move it.
//   - the keys running next_session and prev_session: > from e2e-a2 would go
//     to e2e-loose2 and < from e2e-a1 to e2e-b1; both are asserted to stay in
//     alpha.
//   - the client asserted by a toast alone: each landing is read from the
//     daemon's own list of clients, which must hold exactly one.
//   - the no-space walk being every session's walk: > from e2e-loose must go
//     to e2e-loose2, where next_session would go to e2e-b1. The wrap back and
//     the < after it are walked too, though next_session agrees with them.
func TestSpaceKeysCycleInsideTheSpace(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, true)
	if out, err := tuiosCLI(t, base, "new", "e2e-loose2", "--detach"); err != nil {
		t.Fatalf("create e2e-loose2: %v: %s", err, out)
	}

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)

	pressSpaceKey(t, term, base, ">", "e2e-a2")
	saveArtifact(t, term, dir, "next-a2")
	pressSpaceKey(t, term, base, ">", "e2e-a1")
	saveArtifact(t, term, dir, "next-wraps-a1")
	pressSpaceKey(t, term, base, "<", "e2e-a2")
	pressSpaceKey(t, term, base, "<", "e2e-a1")
	saveArtifact(t, term, dir, "prev-wraps-a1")

	if out, err := tuiosCLI(t, base, "switch-session", "e2e-loose"); err != nil {
		t.Fatalf("switch-session e2e-loose: %v: %s", err, out)
	}
	soleClientOn(t, base, "e2e-loose")
	time.Sleep(insertGuard + 150*time.Millisecond)

	pressSpaceKey(t, term, base, ">", "e2e-loose2")
	pressSpaceKey(t, term, base, ">", "e2e-loose")
	saveArtifact(t, term, dir, "no-space-wraps")
	pressSpaceKey(t, term, base, "<", "e2e-loose2")
	alive(t, term, "after walking the spaces")
}

// TestSpaceKeysStayPutWithoutSpaces: with no session in any space, > and <
// leave the client where it is, say why, and leave it working.
//
// How it could pass wrongly, and what stops it:
//   - the key unbound: an unbound key also leaves the client put. The dock must
//     say "No spaces", which only the action shows.
//   - the client wedged by the key: ) must still walk to the next session
//     afterwards.
func TestSpaceKeysStayPutWithoutSpaces(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, false)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)

	for _, key := range []string{">", "<"} {
		sendKeys(t, term, tuitest.Ctrl('b'), key)
		if err := term.WaitForText("No spaces", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: ctrl+b %s with no spaces never said so: %v\n%s", key, err, term.Snapshot())
		}
		saveArtifact(t, term, dir, "no-spaces-"+map[string]string{">": "next", "<": "prev"}[key])
		time.Sleep(time.Second)
		soleClientOn(t, base, "e2e-a1")
	}

	// With no spaces the rail keeps the order the sessions were made in.
	pressSpaceKey(t, term, base, ")", "e2e-a2")
	alive(t, term, "after the space keys with no spaces")
}

// TestSpaceKeysListedInThePalette: the command palette's key list carries
// both actions on their prefix keys.
//
// How it could pass wrongly, and what stops it:
//   - the actions registered with no key, which the palette lists as
//     "unbound": each row must carry its ctrl+b key.
//   - one row matched twice: the rows are found by their descriptions, and
//     neither contains the other. (The action name is cut short at this
//     width, so it cannot be matched.)
func TestSpaceKeysListedInThePalette(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "P")
	waitPaletteOpen(t, term, "for the space keys")
	sendKeys(t, term, "#session_in_space")

	row := func(s tuitest.Screen, action string) string {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if strings.Contains(line, action) {
				return line
			}
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(row(s, "Next session in this space"), "ctrl+b >") &&
			strings.Contains(row(s, "Previous session in this space"), "ctrl+b <")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the palette never listed next_session_in_space on ctrl+b > and prev_session_in_space on ctrl+b <: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "palette")
	closePalette(t, term, "after the space keys")
}

// TestSpaceKeysYieldToTheUsersPrefixKeys: a config that binds > after the
// prefix to something else keeps it, and the in-space action is left with no
// prefix key; a prefix key the config does not touch still gets its new
// default.
//
// How it could pass wrongly, and what stops it:
//   - the user's action winning the key by name order alone: a key two actions
//     in one table claim goes to the first by name, so a user action named
//     before next_session_in_space (launcher) keeps > even when the default
//     takes it. prefix_help is named after it, and keeps > only because the
//     default yields.
//   - the whole table replaced by the user's: prev_session_in_space must
//     still be on <.
func TestSpaceKeysYieldToTheUsersPrefixKeys(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.prefix_mode]\nprefix_help = [\">\"]\n")

	out, err := tuiosCLI(t, base, "keybinds", "list", "--json")
	if err != nil {
		t.Fatalf("keybinds list --json: %v\n%s", err, out)
	}
	var rows []struct {
		Scope  string   `json:"scope"`
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("keybinds list --json is not JSON: %v\n%s", err, out)
	}
	keys := func(action string) []string {
		var all []string
		for _, r := range rows {
			if r.Action == action && strings.HasPrefix(r.Scope, "prefix") {
				all = append(all, r.Keys...)
			}
		}
		return all
	}
	if got := keys("prefix_help"); !slices.Equal(got, []string{"ctrl+b >"}) {
		t.Errorf("ASSERTION: prefix_help has prefix keys %v, want [ctrl+b >] kept", got)
	}
	if got := keys("next_session_in_space"); len(got) != 0 {
		t.Errorf("ASSERTION: next_session_in_space has prefix keys %v, want none while the user holds >", got)
	}
	if got := keys("prev_session_in_space"); !slices.Equal(got, []string{"ctrl+b <"}) {
		t.Errorf("ASSERTION: prev_session_in_space has prefix keys %v, want [ctrl+b <]", got)
	}
}
