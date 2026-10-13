package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// spaceList is the part of the list-spaces result these tests read.
type spaceList struct {
	Type   string `json:"type"`
	Spaces []struct {
		Name     string `json:"name"`
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	} `json:"spaces"`
}

// listSpaces calls list-spaces and returns each space with its sessions'
// names, failing the test on a reply that is not the daemon's.
func listSpaces(t *testing.T, base string) map[string][]string {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "list-spaces")
	if err != nil {
		t.Fatalf("list-spaces: %v: %s", err, out)
	}
	var list spaceList
	if err := json.Unmarshal([]byte(out), &list); err != nil || list.Type != "space_list" {
		t.Fatalf("ASSERTION: list-spaces did not answer with a space_list: %v: %s", err, out)
	}
	got := map[string][]string{}
	for _, sp := range list.Spaces {
		for _, s := range sp.Sessions {
			got[sp.Name] = append(got[sp.Name], s.Name)
		}
	}
	return got
}

// TestSpaceSurvivesTheDaemon files one session under a space, leaves another in
// none, restarts the daemon, and checks the space is back with only its member.
//
// How it could pass wrongly, and what stops it:
//   - set-session-space accepted and ignored: list-spaces is read before the
//     restart too, so a space that was never recorded fails there.
//   - the space saved but dropped on restore (the restore's client sync keeps
//     the new session's own value): the restored session must be listed under
//     the space after the restart, which only the restore can put back.
//   - every session listed under every space: the session left in no space
//     must be absent.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space persistence"):
//   - with the restore not passing the saved space to the new session, this
//     fails at "after a restart".
func TestSpaceSurvivesTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"e2e-spaced", "e2e-loose"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-spaced","space":"bad/name"}`)
	if err == nil || !strings.Contains(out, "invalid_params") {
		t.Fatalf("ASSERTION: a space name holding a slash was not refused as invalid_params: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-spaced","space":"payments"}`); err != nil {
		t.Fatalf("ASSERTION: set-session-space failed: %v: %s", err, out)
	}

	check := func(when string) {
		t.Helper()
		got := listSpaces(t, base)
		if len(got) != 1 || len(got["payments"]) != 1 || got["payments"][0] != "e2e-spaced" {
			t.Fatalf("ASSERTION: %s, list-spaces is %v, want only payments holding e2e-spaced", when, got)
		}
	}
	check("before a restart")

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
	waitForSessionInfo(t, base, "e2e-loose")
	check("after a restart")

	// An empty space takes the session out, and an emptied space is gone.
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-spaced","space":""}`); err != nil {
		t.Fatalf("ASSERTION: clearing the space failed: %v: %s", err, out)
	}
	if got := listSpaces(t, base); len(got) != 0 {
		t.Fatalf("ASSERTION: after the last session left, list-spaces is %v, want none", got)
	}
}
