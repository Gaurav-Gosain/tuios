package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// herdrEventStream is an events.subscribe stream of global events.
type herdrEventStream struct {
	conn net.Conn
	r    *bufio.Reader
	log  strings.Builder
}

func subscribeHerdrEvents(t *testing.T, base string, types ...string) *herdrEventStream {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	subs := make([]any, 0, len(types))
	for _, typ := range types {
		subs = append(subs, map[string]any{"type": typ})
	}
	req, _ := json.Marshal(map[string]any{"id": "es", "method": "events.subscribe", "params": map[string]any{"subscriptions": subs}})
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	s := &herdrEventStream{conn: conn, r: bufio.NewReader(conn)}
	if ack, _ := s.r.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}
	return s
}

// next waits for the stream's next event and fails unless it is kind. It
// returns the event's data.
func (s *herdrEventStream) next(t *testing.T, kind, why string) map[string]any {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(uiTimeout))
	line, err := s.r.ReadString('\n')
	if err != nil {
		t.Fatalf("%s: no %s arrived: %v\nstream so far:\n%s", why, kind, err, s.log.String())
	}
	s.log.WriteString(line)
	var ev struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("%s: event line %q: %v", why, line, err)
	}
	if ev.Event != kind || ev.Data["type"] != kind {
		t.Fatalf("%s: got %s, want %s\nstream so far:\n%s", why, strings.TrimSpace(line), kind, s.log.String())
	}
	return ev.Data
}

// herdrTabByLabel is the tab labelled label in tab.list.
func herdrTabByLabel(t *testing.T, base, label string) map[string]any {
	t.Helper()
	for _, tb := range herdrCall(t, base, "tab.list", map[string]any{})["tabs"].([]any) {
		if m := tb.(map[string]any); m["label"] == label {
			return m
		}
	}
	t.Fatalf("no tab labelled %s in tab.list", label)
	return nil
}

// wantTabRenamed checks a tab_renamed against herdr's TabRenamed: the tab's
// id and workspace, and label, which tab.get reads back afterwards.
func wantTabRenamed(t *testing.T, base string, data, tab map[string]any, label, why string) {
	t.Helper()
	if data["tab_id"] != tab["tab_id"] || data["workspace_id"] != tab["workspace_id"] || data["label"] != label {
		t.Fatalf("%s: tab_renamed %v, want tab_id %v, workspace_id %v, label %q", why, data, tab["tab_id"], tab["workspace_id"], label)
	}
	got := herdrCall(t, base, "tab.get", map[string]any{"tab_id": tab["tab_id"]})["tab"].(map[string]any)
	if got["label"] != label {
		t.Fatalf("%s: tab.get reads label %v after tab_renamed said %q", why, got["label"], label)
	}
}

// TestHerdrTabRenamed holds the herdr socket to herdr 0.9.5's tab.renamed: a
// herdr tab is a tuios workspace, and naming one sends tab_renamed with the
// tab's id, its workspace's id and the new label, whether the name came from
// tuios (set-workspace-name) or from herdr (tab.rename). herdr's label is
// always present, so clearing a name sends the label the tab falls back to,
// the workspace number. An empty workspace in the background has had no
// tab_created yet: naming it sends nothing, and its name arrives with the
// tab_created of its first window.
//
// Negative controls (e2e/tui/NEGATIVE_CONTROLS.md):
//   - main without the fix: fails at "set-workspace-name names workspace 1:
//     got tab_created, want tab_renamed", the first rename never sent.
//   - the t.renamed call cut from herdrTranslator.translate: fails at the
//     same place.
//   - the empty-workspace check cut from renamed: fails at "naming an empty
//     workspace in the background: got tab_renamed, want tab_created".
func TestHerdrTabRenamed(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "herdr-tabs"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "second", "-s", session, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("new-window on workspace 2: %v\n%s", err, out)
	}
	first := herdrTabByLabel(t, base, "1")
	second := herdrTabByLabel(t, base, "2")
	stream := subscribeHerdrEvents(t, base, "tab.renamed", "tab.created")

	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", session, "1", "from-tuios"); err != nil {
		t.Fatalf("set-workspace-name: %v\n%s", err, out)
	}
	// Each step after a rename opens a tab, so a rename that sent nothing
	// shows up as that tab_created arriving first.
	sentinel := func(ws, name string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", session, "--workspace", ws, "--no-focus"); err != nil {
			t.Fatalf("new-window on workspace %s: %v\n%s", ws, err, out)
		}
	}
	sentinel("4", "after-tuios")
	wantTabRenamed(t, base, stream.next(t, "tab_renamed", "set-workspace-name names workspace 1"), first, "from-tuios", "set-workspace-name names workspace 1")
	stream.next(t, "tab_created", "the tab opened after naming workspace 1")

	if _, e := herdrTry(t, base, "tab.rename", map[string]any{"tab_id": second["tab_id"], "label": "from-herdr"}); e != nil {
		t.Fatalf("tab.rename: %v", e)
	}
	sentinel("5", "after-herdr")
	wantTabRenamed(t, base, stream.next(t, "tab_renamed", "tab.rename names workspace 2"), second, "from-herdr", "tab.rename names workspace 2")
	stream.next(t, "tab_created", "the tab opened after tab.rename")

	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", session, "1"); err != nil {
		t.Fatalf("clear the name of workspace 1: %v\n%s", err, out)
	}
	sentinel("6", "after-clear")
	wantTabRenamed(t, base, stream.next(t, "tab_renamed", "clearing the name of workspace 1"), first, "1", "clearing the name of workspace 1")
	stream.next(t, "tab_created", "the tab opened after clearing the name")

	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", session, "3", "named-empty"); err != nil {
		t.Fatalf("name the empty workspace 3: %v\n%s", err, out)
	}
	sentinel("3", "first-on-3")
	created := stream.next(t, "tab_created", "naming an empty workspace in the background")
	if tab := created["tab"].(map[string]any); tab["label"] != "named-empty" {
		t.Fatalf("the first window on a named workspace: tab_created label %v, want named-empty", tab["label"])
	}
	saveText(t, "herdr-tab-renamed-stream", stream.log.String())
}
