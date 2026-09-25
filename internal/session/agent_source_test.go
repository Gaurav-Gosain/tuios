package session

import (
	"testing"
)

// TestParseAgentSource checks the wire-name mapping and, most of all, that an
// omitted source is accepted and means report: that default is what keeps every
// caller written before this field existed working exactly as it did.
func TestParseAgentSource(t *testing.T) {
	cases := map[string]struct {
		want AgentSource
		ok   bool
	}{
		"":       {AgentSourceReport, true},
		"report": {AgentSourceReport, true},
		"osc":    {AgentSourceOSC, true},
		"screen": {AgentSourceScreen, true},
		"stall":  {AgentSourceStall, true},
		"bogus":  {"", false},
		// The detector's own source is daemon-internal, not something a caller
		// reports, so it is not accepted from the wire. Nor is the transcript
		// reader's, for the same reason: both are the daemon looking at the
		// machine, and a caller naming one has looked at nothing.
		"detect":     {"", false},
		"transcript": {"", false},
	}
	for in, want := range cases {
		got, ok := ParseAgentSource(in)
		if ok != want.ok || (ok && got != want.want) {
			t.Errorf("ParseAgentSource(%q) = (%q, %v), want (%q, %v)", in, got, ok, want.want, want.ok)
		}
	}
	if AgentSource("").Name() != "report" {
		t.Errorf("unset source names itself %q, want report", AgentSource("").Name())
	}
}
