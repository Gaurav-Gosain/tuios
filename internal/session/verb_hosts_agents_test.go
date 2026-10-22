package session

import (
	"strconv"
	"strings"
	"testing"
)

// A host is untrusted: the agent panes a listing passes on from it are held
// to their bounds whatever it sends. Security boundary test.
func TestHostAgentPanesAreBounded(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var windows []WindowSummary
	for i := range 40 {
		w := WindowSummary{ID: "w" + strconv.Itoa(i), Title: long, AgentState: "working", AgentHarness: long, AgentMessage: long}
		for j := range 20 {
			w.AgentMeta = append(w.AgentMeta, AgentMetaToken{Key: long, Value: long + strconv.Itoa(j)})
		}
		windows = append(windows, w)
	}
	// IDs a window cannot have: escape codes, a bidi override, too long.
	windows = append([]WindowSummary{
		{ID: "\x1b[2Jbad", AgentState: "working"},
		{ID: "a\u202eb", AgentState: "working"},
		{ID: strings.Repeat("a", 65), AgentState: "working"},
		{ID: "", AgentState: "working"},
	}, windows...)
	panes := agentPanes(windows)
	if len(panes) != remoteAgentPanesMax {
		t.Fatalf("a session passed %d panes, want %d", len(panes), remoteAgentPanesMax)
	}
	for _, p := range panes {
		if !plausibleWindowID(p.ID) || !strings.HasPrefix(p.ID, "w") {
			t.Fatalf("a pane with ID %q was passed on", p.ID)
		}
		for _, s := range []string{p.Title, p.Harness, p.Message} {
			if len(s) > remoteAgentTextMax {
				t.Fatalf("a string of %d bytes was passed on", len(s))
			}
		}
		if len(p.Meta) > remoteAgentMetaMax {
			t.Fatalf("%d meta tokens were passed on", len(p.Meta))
		}
		for _, m := range p.Meta {
			if len(m.Key) > remoteAgentTextMax || len(m.Value) > remoteAgentTextMax {
				t.Fatalf("a meta token of %d and %d bytes was passed on", len(m.Key), len(m.Value))
			}
		}
	}

	// Over every session of a host, at most remoteAgentHostMax panes.
	var rows []remoteSessionListRow
	for i := range 40 {
		rows = append(rows, remoteSessionListRow{remoteSessionRow: remoteSessionRow{Name: "s" + strconv.Itoa(i)}, Windows: windows})
	}
	total := 0
	for _, r := range sessionRowsOut(rows) {
		total += len(r.Agents)
	}
	if total != remoteAgentHostMax {
		t.Fatalf("a host passed %d agent panes, want %d", total, remoteAgentHostMax)
	}
}
