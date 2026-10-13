package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// herdrWorkspaceIDs maps each session's name to its herdr workspace id, read
// from workspace.list, where a workspace's label is the session's name.
func herdrWorkspaceIDs(t *testing.T, base string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, w := range herdrCall(t, base, "workspace.list", map[string]any{})["workspaces"].([]any) {
		m := w.(map[string]any)
		ids[m["label"].(string)] = m["workspace_id"].(string)
	}
	return ids
}

// TestHerdrWorkspaceCloseGroupKillsTheSpace: herdr's workspace.close with
// close_group closes every session of the target's space, and a plain close
// of a session in a space closes that session alone.
//
// Sessions e2e-a and e2e-b are in space s, e2e-d and e2e-e in space t, and
// e2e-c in none. The steps:
//
//  1. close_group on e2e-a kills e2e-a and e2e-b and nothing else, and a herdr
//     subscriber sees workspace_closed for both.
//  2. A plain close on e2e-d kills e2e-d and leaves e2e-e: tuios does not
//     refuse it, as herdr refuses a plain close of a grouped workspace.
//  3. close_group on e2e-c, in no space, kills e2e-c and leaves e2e-e.
//
// How it could pass wrongly, and what stops it:
//   - close_group not decoded (an unknown field is dropped silently), so only
//     the target dies: step 1 needs e2e-b gone too.
//   - close_group killing more than the space: e2e-c and the space t pair
//     must outlive step 1.
//   - a plain close cascading, or refused: step 2 needs exactly e2e-d gone,
//     and herdrCall fails the test on an error answer.
//   - close_group on a session in no space reaching kill-space with an empty
//     space, which refuses: herdrCall fails on the error, and e2e-e must stay.
//   - the events read from a stream that would report any close: the two
//     workspace ids are matched exactly, and both must arrive.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "herdr workspace.close close_group").
func TestHerdrWorkspaceCloseGroupKillsTheSpace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-a", "s")
	newInSpace(t, base, "e2e-b", "s")
	if out, err := tuiosCLI(t, base, "new", "e2e-c", "--detach"); err != nil {
		t.Fatalf("create e2e-c: %v: %s", err, out)
	}
	newInSpace(t, base, "e2e-d", "t")
	newInSpace(t, base, "e2e-e", "t")

	ids := herdrWorkspaceIDs(t, base)
	for _, name := range []string{"e2e-a", "e2e-b", "e2e-c", "e2e-d", "e2e-e"} {
		if ids[name] == "" {
			t.Fatalf("workspace.list has no workspace labelled %s: %v", name, ids)
		}
	}

	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	_, _ = conn.Write([]byte(`{"id":"es","method":"events.subscribe","params":{"subscriptions":[{"type":"workspace.closed"}]}}` + "\n"))
	stream := bufio.NewReader(conn)
	if ack, _ := stream.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}

	herdrCall(t, base, "workspace.close", map[string]any{"workspace_id": ids["e2e-a"], "close_group": true})
	if got, want := liveSessions(t, base), []string{"e2e-c", "e2e-d", "e2e-e"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after close_group on e2e-a the sessions are %v, want %v", got, want)
	}

	closed := map[string]bool{}
	for !closed[ids["e2e-a"]] || !closed[ids["e2e-b"]] {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("ASSERTION: the stream ended before workspace_closed for e2e-a (%s) and e2e-b (%s): %v; saw %v", ids["e2e-a"], ids["e2e-b"], err, closed)
		}
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "workspace_closed" {
			closed[ev.Data.WorkspaceID] = true
		}
	}

	herdrCall(t, base, "workspace.close", map[string]any{"workspace_id": ids["e2e-d"]})
	if got, want := liveSessions(t, base), []string{"e2e-c", "e2e-e"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after a plain close of e2e-d the sessions are %v, want %v", got, want)
	}

	herdrCall(t, base, "workspace.close", map[string]any{"workspace_id": ids["e2e-c"], "close_group": true})
	if got, want := liveSessions(t, base), []string{"e2e-e"}; !slices.Equal(got, want) {
		t.Fatalf("ASSERTION: after close_group on e2e-c, in no space, the sessions are %v, want %v", got, want)
	}
}
