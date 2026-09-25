package session

import (
	"testing"
)

// TestSessionInfoWindows checks that Info() carries per-window summaries with a
// display title (CustomName, else Title, else "shell") and the window's agent
// state, so a listing client can expand a non-attached session.
func TestSessionInfoWindows(t *testing.T) {
	sess, _ := NewSession("info-windows", nil, 80, 24)
	defer sess.Stop()
	sess.UpdateState(&SessionState{
		Name: "info-windows",
		Windows: []WindowState{
			{ID: "w1", Title: "live-title"},
			{ID: "w2", CustomName: "build", Title: "ignored", AgentState: AgentStateWorking},
			{ID: "w3"},
		},
	})

	info := sess.Info()
	if info.WindowCount != 3 {
		t.Fatalf("WindowCount = %d, want 3", info.WindowCount)
	}
	if len(info.Windows) != 3 {
		t.Fatalf("Windows = %d, want 3", len(info.Windows))
	}

	want := []WindowSummary{
		{ID: "w1", Title: "live-title"},
		{ID: "w2", Title: "build", AgentState: string(AgentStateWorking)},
		{ID: "w3", Title: "shell"},
	}
	for i, w := range want {
		if !windowSummariesAgree(info.Windows[i], w) {
			t.Errorf("Windows[%d] = %+v, want %+v", i, info.Windows[i], w)
		}
	}
}
