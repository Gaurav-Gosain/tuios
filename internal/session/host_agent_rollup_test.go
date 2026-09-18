package session

import "testing"

// A session row in an aggregated listing is one row, so the panes under it have
// to become one answer. The rail asks whether anything in there wants a person,
// and the ranking that decides is the one the rail draws with.

func TestTheMostUrgentPaneSpeaksForTheSession(t *testing.T) {
	for _, c := range []struct {
		name   string
		states []string
		want   string
	}{
		{"nothing running", []string{"", ""}, ""},
		{"one working pane", []string{"", "working"}, "working"},
		{"waiting outranks working", []string{"working", "needs_input"}, "needs_input"},
		{"errored outranks waiting", []string{"needs_input", "errored"}, "errored"},
		{"finished outranks working", []string{"working", "done"}, "done"},
		{"waiting outranks finished", []string{"done", "needs_input"}, "needs_input"},
		{"idle is not nothing but loses to all", []string{"idle", "working"}, "working"},
		{"a session with no panes", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			windows := make([]WindowSummary, 0, len(c.states))
			for _, st := range c.states {
				windows = append(windows, WindowSummary{AgentState: st})
			}
			if got := rollUpAgentState(windows); got != c.want {
				t.Errorf("states %v rolled up to %q, want %q", c.states, got, c.want)
			}
		})
	}
}

// TestAFinishedPaneRollsUpUnread.
//
// The unread bit is a fact about a viewer: it says whether the person looking
// has seen that pane. The daemon answering a listing has no idea what the
// person reading it has looked at, and it is answering a client on another
// machine that cannot have looked at anything here. Ranking every pane as
// unseen is the honest reading, and it is also the useful one, since a
// finished agent nobody has seen is the case worth surfacing.
//
// Negative control: ranking with doneSeen true drops done below working and
// this fails.
func TestAFinishedPaneRollsUpUnread(t *testing.T) {
	got := rollUpAgentState([]WindowSummary{
		{AgentState: "working"},
		{AgentState: "done"},
	})
	if got != "done" {
		t.Errorf("a finished pane rolled up as %q, want done: it was ranked as already seen", got)
	}
}
