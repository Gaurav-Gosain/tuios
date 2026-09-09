//go:build linux

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExplainAgentDetectOnRealPanes runs the whole path against real processes
// in real panes: a shell script in a checkout named after an agent, which must
// not be an agent and must be explained; and an agent-named program behind a
// shell that does not exec, which must be found and attributed through the
// wrapper. It is the proof a fixture cannot give, because the fixture is the
// thing under suspicion.
func TestExplainAgentDetectOnRealPanes(t *testing.T) {
	if _, err := os.Stat("/proc/self/task"); err != nil {
		t.Skip("no procfs")
	}
	root := t.TempDir()
	// The false positive: a script under a directory named crush.
	scriptDir := filepath.Join(root, "dev", "crush", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDir, "build.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	// The true positive behind a wrapper: a program named crush, which is enough
	// for the crush manifest, run by a shell that keeps running above it.
	binDir := filepath.Join(root, "bin")
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

	winID := sess.GetState().Windows[0].ID
	typeCommand(t, c, winID, script)
	res := explainUntil(t, c, winID, func(res map[string]any) bool {
		proc, _ := res["process"].(map[string]any)
		return proc != nil && strings.Contains(proc["comm"].(string), "build")
	})
	if res["matched"] != false {
		t.Fatalf("a script in a checkout named crush was called an agent: %v", res["verdict"])
	}
	ignored, _ := res["ignored"].([]any)
	if len(ignored) == 0 || !strings.Contains(ignored[0].(string), `"crush"`) {
		t.Fatalf("the explanation did not name the word that did not count: %v", res["ignored"])
	}
	verdict, _ := res["verdict"].(string)
	if !strings.HasPrefix(verdict, "This pane does not run an agent.") {
		t.Fatalf("verdict = %q", verdict)
	}

	// Detection has had a chance to run by now, and must not have claimed it.
	if st := sess.GetState().Windows[0].AgentState; st != AgentStateNone {
		t.Fatalf("state after a script in a crush checkout = %q, want none", st)
	}

	// Interrupt it and start the wrapped agent. The interrupt is the raw byte,
	// written to the pane the way a terminal would, so no key name has to be
	// right for this test to be about detection.
	sendText(t, c, winID, "\x03")
	explainUntil(t, c, winID, func(res map[string]any) bool { return res["matched"] == false && res["group"] == nil })
	typeCommand(t, c, winID, "sh -c '"+fake+" 60; true'")
	res = explainUntil(t, c, winID, func(res map[string]any) bool {
		return res["matched"] == true
	})
	if res["matched_harness"] != "crush" {
		t.Fatalf("matched_harness = %v, want crush; full answer: %v", res["matched_harness"], res)
	}
	via, _ := res["matched_via"].([]any)
	if len(via) != 1 || via[0] != "sh" {
		t.Fatalf("matched_via = %v, want [sh]", res["matched_via"])
	}
	verdict, _ = res["verdict"].(string)
	if verdict != "This pane runs crush behind sh." {
		t.Fatalf("verdict = %q", verdict)
	}
	if res["confidence"] != "strong" || res["identity"] != "manifest" {
		t.Fatalf("confidence = %v identity = %v, want strong from a manifest", res["confidence"], res["identity"])
	}
	sendText(t, c, winID, "\x03")
}

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
