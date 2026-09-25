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
