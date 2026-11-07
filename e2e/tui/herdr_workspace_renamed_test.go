package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// herdrWorkspaceStream is an events.subscribe stream of herdr's workspace
// lifecycle events: created, renamed and closed.
type herdrWorkspaceStream struct {
	conn net.Conn
	r    *bufio.Reader
	log  strings.Builder
}

// herdrWorkspaceEvent is one line of the stream.
type herdrWorkspaceEvent struct {
	Event string `json:"event"`
	Data  struct {
		Type        string `json:"type"`
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
		Workspace   struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspace"`
	} `json:"data"`
}

func subscribeHerdrWorkspaces(t *testing.T, base string) *herdrWorkspaceStream {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var subs []any
	for _, kind := range []string{"workspace.created", "workspace.renamed", "workspace.closed"} {
		subs = append(subs, map[string]any{"type": kind})
	}
	req, _ := json.Marshal(map[string]any{"id": "ws", "method": "events.subscribe", "params": map[string]any{"subscriptions": subs}})
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	s := &herdrWorkspaceStream{conn: conn, r: bufio.NewReader(conn)}
	if ack, _ := s.r.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}
	return s
}

// next reads the stream's next event, failing with why when none arrives.
func (s *herdrWorkspaceStream) next(t *testing.T, why string) herdrWorkspaceEvent {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(uiTimeout))
	line, err := s.r.ReadString('\n')
	if err != nil {
		t.Fatalf("%s: no event arrived: %v\nstream so far:\n%s", why, err, s.log.String())
	}
	s.log.WriteString(line)
	var ev herdrWorkspaceEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("%s: event line %q: %v", why, line, err)
	}
	return ev
}

// renamed reads the next event and fails unless it is the one
// workspace_renamed for workspace, with label.
func (s *herdrWorkspaceStream) renamed(t *testing.T, workspace, label, why string) {
	t.Helper()
	ev := s.next(t, why)
	if ev.Event != "workspace_renamed" || ev.Data.Type != "workspace_renamed" || ev.Data.WorkspaceID != workspace || ev.Data.Label != label {
		t.Fatalf("%s: got %s for %q labelled %q, want workspace_renamed for %s labelled %q\nstream so far:\n%s",
			why, ev.Event, ev.Data.WorkspaceID+ev.Data.Workspace.WorkspaceID, ev.Data.Label+ev.Data.Workspace.Label, workspace, label, s.log.String())
	}
}

// herdrWorkspaceLabel is a workspace's label in session.snapshot.
func herdrWorkspaceLabel(t *testing.T, base, workspace string) string {
	t.Helper()
	snap := herdrCall(t, base, "session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	for _, w := range snap["workspaces"].([]any) {
		if m := w.(map[string]any); m["workspace_id"] == workspace {
			return m["label"].(string)
		}
	}
	t.Fatalf("no workspace %s in the snapshot", workspace)
	return ""
}

// herdrWorkspaceByLabel is the id of the workspace labelled label.
func herdrWorkspaceByLabel(t *testing.T, base, label string) string {
	t.Helper()
	snap := herdrCall(t, base, "session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	for _, w := range snap["workspaces"].([]any) {
		if m := w.(map[string]any); m["label"] == label {
			return m["workspace_id"].(string)
		}
	}
	t.Fatalf("no workspace labelled %s in the snapshot", label)
	return ""
}

// TestHerdrWorkspaceRenamed holds the herdr event stream to herdr's
// workspace.renamed at the version tuios follows: a workspace whose label
// changes sends one workspace_renamed with its id and the label the snapshot
// then shows, and nothing else. A herdr workspace is a tuios session, and its
// label is the session's display name, or its name when it has none, so two
// tuios actions change it:
//
//   - rename-session, which tuios publishes as the old name closing and the
//     new one opening. That is one workspace_renamed, not a
//     workspace_created for a workspace the client already has.
//   - herdr's workspace.rename, which sets the display name (set-session-name)
//     and so changes the label without any session closing or opening.
//
// The stream's next event after each rename is read, so an extra event, or
// a workspace_created in place of the rename, fails the test. A new session
// at the end must then arrive as workspace_created, which shows the same
// stream does carry that event when it is due.
//
// Negative controls (e2e/tui/NEGATIVE_CONTROLS.md):
//   - upstream/main: rename-session fails at "rename-session sends
//     workspace_renamed" with a workspace_created.
//   - the forget of a renamed name moved back to the session-closed case of
//     herdrTranslator.translate: the same failure as on main.
//   - the session-labelled case cut from herdrTranslator.translate:
//     workspace.rename fails at "workspace.rename sends workspace_renamed":
//     no event arrives.
func TestHerdrWorkspaceRenamed(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session, renamed, label = "herdr-ren", "herdr-ren-two", "Payments API"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	workspace := herdrWorkspaceByLabel(t, base, session)
	stream := subscribeHerdrWorkspaces(t, base)

	if out, err := tuiosCLI(t, base, "rename-session", session, renamed); err != nil {
		t.Fatalf("rename-session: %v\n%s", err, out)
	}
	stream.renamed(t, workspace, renamed, "rename-session sends workspace_renamed")
	if got := herdrWorkspaceLabel(t, base, workspace); got != renamed {
		t.Fatalf("after rename-session the snapshot labels the workspace %q, want %q", got, renamed)
	}

	herdrCall(t, base, "workspace.rename", map[string]any{"workspace_id": workspace, "label": label})
	stream.renamed(t, workspace, label, "workspace.rename sends workspace_renamed")
	if got := herdrWorkspaceLabel(t, base, workspace); got != label {
		t.Fatalf("after workspace.rename the snapshot labels the workspace %q, want %q", got, label)
	}

	const other = "herdr-ren-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create the other session: %v\n%s", err, out)
	}
	ev := stream.next(t, "a new session sends workspace_created")
	if ev.Event != "workspace_created" || ev.Data.Workspace.Label != other {
		t.Fatalf("after the renames the next event is %s labelled %q, want workspace_created for %s: a rename sent more than one event\nstream so far:\n%s",
			ev.Event, ev.Data.Label+ev.Data.Workspace.Label, other, stream.log.String())
	}
	saveText(t, "herdr-workspace-renamed-stream", stream.log.String())
}
