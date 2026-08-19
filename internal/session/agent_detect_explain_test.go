package session

import (
	"strings"
	"testing"
)

// TestExplainAgentDetectVerbSaysWhatItRead is the counterpart to the screen
// diagnostic's test: it exercises the whole path over the real socket, so a pane
// running an ordinary shell gets a usable answer rather than an error.
//
// Detection shipped broken because it was unfalsifiable from outside. This is the
// check that it can be interrogated at all.
func TestExplainAgentDetectVerbSaysWhatItRead(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	res := result(t, c.call(t,
		`{"id":1,"verb":"explain-agent-detect","params":{"session":"work","window":"Window"}}`))

	if res["type"] != "agent_detect" {
		t.Fatalf("type = %v, want agent_detect", res["type"])
	}
	if res["matched"] != false {
		t.Fatalf("a pane at its shell prompt matched as an agent: %v", res)
	}

	if res["running"] != true {
		// No foreground process could be read, which is the honest answer on a
		// platform with neither procfs nor the darwin sysctls. Nothing further is
		// assertable, but the verb still has to answer rather than fail.
		if res["reason"] == nil {
			t.Fatal("not running and no reason given")
		}
		return
	}

	proc, ok := res["process"].(map[string]any)
	if !ok {
		t.Fatalf("process = %v, want what the detector read", res["process"])
	}
	if proc["comm"] == "" && proc["exe"] == "" {
		t.Fatalf("the detector reported reading nothing at all: %v", proc)
	}

	// Every manifest is reported, and a refusal names what it compared against.
	// That is the half that makes a rule that should have matched fixable.
	manifests, ok := res["manifests"].([]any)
	if !ok || len(manifests) == 0 {
		t.Fatalf("manifests = %v, want one entry per manifest", res["manifests"])
	}
	for _, m := range manifests {
		e := m.(map[string]any)
		if e["matched"] == true {
			t.Fatalf("manifest %v claimed a shell: %v", e["id"], e)
		}
		reason, _ := e["reason"].(string)
		if strings.TrimSpace(reason) == "" {
			t.Errorf("manifest %v refused without saying why", e["id"])
		}
	}
}
