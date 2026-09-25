package main

import "testing"

// TestAgentMetaTokensJSONKeepsArgumentOrder: the rail draws metadata in the
// order it first arrived, so the CLI has to send the arguments in the order
// they were written, which a Go map would not.
func TestAgentMetaTokensJSONKeepsArgumentOrder(t *testing.T) {
	got, err := agentMetaTokensJSON([]string{"zeta=1", "model=opus 4", "summary=", `quote="x"`, "eq=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"zeta":"1","model":"opus 4","summary":null,"quote":"\"x\"","eq":"a=b"}`
	if string(got) != want {
		t.Errorf("tokens = %s, want %s", got, want)
	}
	for _, bad := range []string{"model", "=opus"} {
		if _, err := agentMetaTokensJSON([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
