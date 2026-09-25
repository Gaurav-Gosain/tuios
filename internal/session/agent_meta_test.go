package session

import (
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

// TestApplyAgentMetaPerPaneLimit: the per-pane cap is what bounds what one
// pane makes every client store.
func TestApplyAgentMetaPerPaneLimit(t *testing.T) {
	var cur []AgentMetaToken
	for i := range AgentMetaMaxPerPane {
		var err error
		cur, _, err = applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"k" + string(rune('a'+i%26)) + string(rune('a'+i/26))}, Values: []*string{strp("v")}}, 1)
		if err != nil {
			t.Fatalf("key %d refused: %v", i, err)
		}
	}
	if _, _, err := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"one-more"}, Values: []*string{strp("v")}}, 1); err == nil {
		t.Error("a pane took more than the per-pane limit")
	}
}

// TestCleanAgentMetaValue: a value is drawn on the rail, so an escape in it
// would be a terminal sequence the pane gets to write into chrome.
func TestCleanAgentMetaValue(t *testing.T) {
	got, cut := CleanAgentMetaValue("  fix\x1b[31m the\n\tbug  ")
	if got != "fix [31m the bug" || cut {
		t.Errorf("CleanAgentMetaValue = %q (cut %v)", got, cut)
	}
	long := strings.Repeat("é", AgentMetaMaxValue+5)
	got, cut = CleanAgentMetaValue(long)
	if !cut || len([]rune(got)) != AgentMetaMaxValue {
		t.Errorf("a long value came back %d characters, cut %v", len([]rune(got)), cut)
	}
}
