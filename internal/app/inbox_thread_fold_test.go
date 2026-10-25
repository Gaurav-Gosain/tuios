package app

import "testing"

// A call can draw as several cards that share one tool id (a Codex apply_patch
// touching two files). When its result arrives on a later page, every card of
// the call must finish, not only the last one, and a result for another call
// must leave them alone.
func TestThreadPageFoldsALaterResultIntoEveryCardOfACall(t *testing.T) {
	m := &OS{}
	tr := &agentThread{key: "k", entries: []threadEntry{
		{ID: "file-a", Kind: "tool_call", ToolID: "patch-1", Status: "running"},
		{ID: "file-b", Kind: "tool_call", ToolID: "patch-1", Status: "running"},
		{ID: "other", Kind: "tool_call", ToolID: "other-1", Status: "running"},
	}}
	m.Inbox.threads.byKey = map[string]*agentThread{"k": tr}

	m.applyThreadPage(threadPageMsg{key: "k", after: true, entries: []threadEntry{
		{ID: "result", Kind: "tool_result", ToolID: "patch-1", Status: "error"},
	}})

	want := map[string]string{"file-a": "error", "file-b": "error", "other": "running"}
	for _, e := range tr.entries {
		if w, ok := want[e.ID]; ok && e.Status != w {
			t.Errorf("card %s is %q, want %q", e.ID, e.Status, w)
		}
	}
}

// Control for the test above: a call with one card still finishes, so the
// multi-card case fails for the grouping and not because folding is broken.
func TestThreadPageFoldsALaterResultIntoASingleCard(t *testing.T) {
	m := &OS{}
	tr := &agentThread{key: "k", entries: []threadEntry{
		{ID: "only", Kind: "tool_call", ToolID: "call-1", Status: "running"},
	}}
	m.Inbox.threads.byKey = map[string]*agentThread{"k": tr}

	m.applyThreadPage(threadPageMsg{key: "k", after: true, entries: []threadEntry{
		{ID: "result", Kind: "tool_result", ToolID: "call-1", Status: "error"},
	}})

	if got := tr.entries[0].Status; got != "error" {
		t.Fatalf("single card is %q, want error", got)
	}
}
