//go:build linux

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func typeCommand(t *testing.T, c *verbConn, winID, cmd string) {
	t.Helper()
	sendText(t, c, winID, cmd+"\n")
}

func sendText(t *testing.T, c *verbConn, winID, text string) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"session": "work", "window": winID, "text": text})
	result(t, c.call(t, `{"id":1,"verb":"send-text","params":`+string(params)+`}`))
}

// explainUntil polls explain-agent-detect until ready accepts the answer or the
// deadline passes, then returns the last answer for the assertions.
func explainUntil(t *testing.T, c *verbConn, winID string, ready func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var res map[string]any
	for time.Now().Before(deadline) {
		res = result(t, c.call(t, `{"id":1,"verb":"explain-agent-detect","params":{"session":"work","window":"`+winID+`"}}`))
		if ready(res) {
			return res
		}
		time.Sleep(100 * time.Millisecond)
	}
	return res
}

// TestTranscriptIdentityReadsTheAgentBehindAWrapper pins that the transcript
// join checks a candidate file against the agent's own executable, not the
// shell that launched it: a wrapper's build is "bash", and no transcript was
// ever written by bash.
func TestTranscriptIdentityReadsTheAgentBehindAWrapper(t *testing.T) {
	if _, err := os.Stat("/proc/self/task"); err != nil {
		t.Skip("no procfs")
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "crush")
	if err := os.Symlink("/usr/bin/sleep", fake); err != nil {
		t.Fatal(err)
	}
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	w := sess.GetState().Windows[0]
	typeCommand(t, c, w.ID, "sh -c '"+fake+" 60; true'")
	explainUntil(t, c, w.ID, func(res map[string]any) bool { return res["matched"] == true })

	_, version := d.paneAgentIdentifier(sess)(w.PTYID)
	if version != "sleep" {
		t.Fatalf("version = %q, want the agent's own executable name (sleep), not the wrapper's", version)
	}
	sendText(t, c, w.ID, "\x03")
}
