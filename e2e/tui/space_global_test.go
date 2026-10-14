package tuie2e

import (
	"slices"
	"strings"
	"testing"
)

// TestAGlobalSessionIsInNoSpace: a global session is drawn above the
// machines, never inside a space, so set-session-space refuses to file one,
// and the verbs that kill a space's sessions never reach it. A space name is
// capped at 64 bytes.
//
// The steps:
//
//  1. set-session-space on the global session e2e-g is refused, and a space
//     name longer than 64 bytes is refused for any session, by the verb and
//     by tuios new --space.
//  2. kill-space --force s kills e2e-a, the space's one member, and not e2e-g.
//  3. herdr's workspace.close with close_group on e2e-b, filed under s after
//     that, kills e2e-b alone, and on e2e-g kills e2e-g alone, with e2e-c in
//     s by then: herdr's group of a global session is the session itself.
//
// How it could pass wrongly, and what stops it:
//   - the refusal reported but the session filed anyway: list-spaces must
//     hold only e2e-a afterwards.
//   - kill-space or close_group killing nothing at all: each must take the
//     space's member out of the listing.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A global session is in no space").
func TestAGlobalSessionIsInNoSpace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-a", "s")
	if out, err := tuiosCLI(t, base, "new", "e2e-g", "--global"); err != nil {
		t.Fatalf("create the global session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-g"); err != nil {
		t.Fatalf("open a window in the global session: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-g","space":"s"}`)
	if err == nil || !strings.Contains(out, "global") {
		t.Fatalf("ASSERTION: set-session-space filed a global session under a space: %v: %s", err, out)
	}
	long := strings.Repeat("x", 65)
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-a","space":"`+long+`"}`); err == nil || !strings.Contains(out, "64 bytes") {
		t.Fatalf("ASSERTION: set-session-space took a 65-byte space name: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-long", "--detach", "--space", long); err == nil {
		t.Fatalf("ASSERTION: tuios new --space took a 65-byte space name: %s", out)
	}
	wantSpaces(t, base, "after the refusals", map[string][]string{"s": {"e2e-a"}})

	if out, err := tuiosCLI(t, base, "kill-space", "--force", "s"); err != nil {
		t.Fatalf("kill-space --force s: %v: %s", err, out)
	}
	if got, want := liveSessions(t, base), []string{"e2e-g"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after kill-space s the sessions are %v, want %v", got, want)
	}

	newInSpace(t, base, "e2e-b", "s")
	ids := herdrWorkspaceIDs(t, base)
	herdrCall(t, base, "workspace.close", map[string]any{"workspace_id": ids["e2e-b"], "close_group": true})
	if got, want := liveSessions(t, base), []string{"e2e-g"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after close_group on e2e-b the sessions are %v, want %v", got, want)
	}
	newInSpace(t, base, "e2e-c", "s")
	herdrCall(t, base, "workspace.close", map[string]any{"workspace_id": ids["e2e-g"], "close_group": true})
	if got, want := liveSessions(t, base), []string{"e2e-c"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after close_group on the global session the sessions are %v, want %v", got, want)
	}
}
