package session

import (
	"strings"
	"testing"
)

// ringHas reports whether any entry in the log ring contains sub.
func ringHas(sub string) bool {
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// ringDump renders the ring for a failure message.
func ringDump() string {
	var b strings.Builder
	for _, e := range GetLogEntries(0) {
		b.WriteString("  [" + e.Level + "] " + e.Message + "\n")
	}
	return b.String()
}

// TestVerbRefusalLineHoldsNoMessage keeps the level boundary. A refusal message
// quotes what the caller sent, which for a path or a title is content, and
// basic records identifiers and codes only.
func TestVerbRefusalLineHoldsNoMessage(t *testing.T) {
	restoreLevel(t, DebugOff)
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "quiet")
	c := dialVerb(t, sp)

	ClearLogBuffer()
	resp := c.call(t, `{"id":1,"verb":"stash-put","params":{"session":"quiet","path":"/home/ada/private/keys.txt"}}`)
	// The message the caller gets does name the path. That is the point: the
	// caller may see it and the daemon's own log may not.
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("stash-put of a missing file was not refused: %v", resp)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "/home/ada/private") {
		t.Fatalf("the refusal message does not name the path, so this test proves nothing: %q", msg)
	}

	if ringHas("/home/ada/private") {
		t.Fatalf("the refusal line quoted a caller path:\n%s", ringDump())
	}
	if !ringHas("Verb stash-put refused for client ") {
		t.Fatalf("no refusal line at all:\n%s", ringDump())
	}
}
