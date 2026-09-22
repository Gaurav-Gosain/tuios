package session

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

// The CLI reference showed list-windows with id and focused_id, and
// session-info with total_windows and theme, long after the daemon stopped
// sending them. A script written from the page read null. These tests hold the
// page's JSON samples to the maps the daemon builds.

// cliEnvelope is what the CLI adds around a verb result when it prints it.
var cliEnvelope = []string{"message", "success"}

// cliReferenceJSON returns the first ```json block after heading in
// docs/CLI_REFERENCE.md, decoded.
func cliReferenceJSON(t *testing.T, heading string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../docs/CLI_REFERENCE.md")
	if err != nil {
		t.Fatalf("read CLI_REFERENCE.md: %v", err)
	}
	doc := string(data)
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("CLI_REFERENCE.md has no heading %q", heading)
	}
	doc = doc[start:]
	open := strings.Index(doc, "```json\n")
	if open < 0 {
		t.Fatalf("no json block under %q", heading)
	}
	doc = doc[open+len("```json\n"):]
	body, _, ok := strings.Cut(doc, "```")
	if !ok {
		t.Fatalf("unterminated json block under %q", heading)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("json block under %q does not parse: %v", heading, err)
	}
	return out
}

// docKeys returns a map's keys, sorted, with the CLI envelope left out.
func docKeys(m map[string]any) []string {
	keys := slices.Sorted(maps.Keys(m))
	return slices.DeleteFunc(keys, func(k string) bool { return slices.Contains(cliEnvelope, k) })
}

func TestCLIReferenceSessionInfoShape(t *testing.T) {
	sess := newTestSession(t)
	want := docKeys(buildSessionInfoData(sess, sess.GetState(), true))
	got := docKeys(cliReferenceJSON(t, "### `tuios session-info`"))
	if !slices.Equal(got, want) {
		t.Errorf("CLI_REFERENCE session-info sample has fields\n  %v\nthe daemon sends\n  %v", got, want)
	}
}

func TestCLIReferenceListWindowsShape(t *testing.T) {
	sess := newTestSession(t)
	st := sess.GetState()
	st.Windows = append(st.Windows, WindowState{
		ID: "w1", Title: "build", CustomName: "build", Workspace: 1, PTYID: "p1", Cwd: "/src",
	})
	st.FocusedWindowID = "w1"
	sess.UpdateState(st)
	data := buildWindowListData(sess.GetState())

	doc := cliReferenceJSON(t, "### `tuios list-windows`")
	if got, want := docKeys(doc), docKeys(data); !slices.Equal(got, want) {
		t.Errorf("CLI_REFERENCE list-windows sample has fields\n  %v\nthe daemon sends\n  %v", got, want)
	}

	windows, _ := doc["windows"].([]any)
	if len(windows) == 0 {
		t.Fatal("the list-windows sample has no windows")
	}
	wantWindow := docKeys(data["windows"].([]map[string]any)[0])
	for i, w := range windows {
		entry, _ := w.(map[string]any)
		if got := docKeys(entry); !slices.Equal(got, wantWindow) {
			t.Errorf("sample window %d has fields\n  %v\nthe daemon sends\n  %v", i, got, wantWindow)
		}
	}
}
