//go:build !slim && !windows

package session

import "testing"

func TestAskAgentPastesAndSubmitsWithCR(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "paste")
	id, _ := rawReaderPane(t, d, sess)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"paste","window":"`+id+`","text":"line one\nline two\n","settle":700,"timeout":8000}}`))
	got, ok := gotBytes(t, res["reply"].(string))
	if !ok {
		t.Fatalf("the pane never saw a carriage return; settled_by %v, reply %q", res["settled_by"], res["reply"])
	}
	if want := "\x1b[200~line one\nline two\x1b[201~\r"; got != want {
		t.Errorf("the pane read %q, want %q", got, want)
	}
}
