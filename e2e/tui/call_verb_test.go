package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// tuios call sends one verb with its params and prints the daemon's reply as
// JSON, for a program that cannot write to the socket itself, such as a
// Tern plugin, which can only start processes.
//
// What it must do, and how each check could pass wrongly:
//   - Print the result object exactly as the daemon sends it. The check reads
//     a field only the daemon's list-sessions result has (window_count), so
//     a wrapper that printed its own summary fails it.
//   - Pass the params through. new-window with a name, then list-windows
//     finds that name, so params dropped on the way fail it.
//   - Print a daemon error as JSON with its stable code, and exit non-zero.
//     The code is the daemon's, not text the CLI made up.
//   - Refuse the verbs that turn the connection into a stream, which a
//     one-shot call would leave hanging. The verbs are read from list-verbs,
//     not restated here, so a list that missed a verb would pass with it: every
//     verb whose description says it turns this connection into something must
//     be marked, and a verb that streams only with one param is still called
//     when the param is left out.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "tuios call"):
//   - with the params left out of the call, TestCallVerb fails at "params did
//     not reach the daemon".
func TestCallVerb(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "callme", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "call", "list-sessions")
	if err != nil {
		t.Fatalf("ASSERTION: tuios call list-sessions failed: %v: %s", err, out)
	}
	var sessions struct {
		Type     string `json:"type"`
		Sessions []struct {
			Name        string `json:"name"`
			WindowCount int    `json:"window_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("ASSERTION: the output is not the daemon's JSON: %v: %s", err, out)
	}
	if sessions.Type != "session_list" || len(sessions.Sessions) != 1 || sessions.Sessions[0].Name != "callme" || sessions.Sessions[0].WindowCount != 1 {
		t.Fatalf("ASSERTION: list-sessions through call is not the daemon's result: %s", out)
	}

	// list-routes answers with this machine's addresses, never loopback.
	if out, err := tuiosCLI(t, base, "call", "list-routes"); err != nil {
		t.Fatalf("ASSERTION: list-routes failed: %v: %s", err, out)
	} else if !strings.Contains(out, `"route_list"`) || strings.Contains(out, "127.0.0.1") {
		t.Fatalf("ASSERTION: list-routes answered %s", out)
	}

	if out, err := tuiosCLI(t, base, "call", "new-window", `{"session":"callme","name":"through-call"}`); err != nil {
		t.Fatalf("ASSERTION: new-window through call failed: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "call", "list-windows", `{"session":"callme"}`)
	if err != nil {
		t.Fatalf("list-windows: %v: %s", err, out)
	}
	if !strings.Contains(out, `"through-call"`) {
		t.Fatalf("ASSERTION: params did not reach the daemon: no window named through-call in %s", out)
	}

	out, err = tuiosCLI(t, base, "call", "list-windows", `{"session":"no-such-session"}`)
	if err == nil {
		t.Fatalf("ASSERTION: a verb error exited zero: %s", out)
	}
	var verr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &verr); jerr != nil || verr.Error.Code != "session_not_found" {
		t.Fatalf("ASSERTION: a verb error is not printed as JSON with the daemon's code: %s", out)
	}

	checkStreamVerbsRefused(t, base)

	// Bad input is 2, a verb error 1, and a daemon that is not there 3.
	if _, err := tuiosCLI(t, base, "call", "list-windows", `not json`); exitCodeOf(err) != 2 {
		t.Fatalf("ASSERTION: bad params exited %d, want 2", exitCodeOf(err))
	}
	if _, err := tuiosCLI(t, base, "call", "list-windows", `{"session":"no-such-session"}`); exitCodeOf(err) != 1 {
		t.Fatalf("ASSERTION: a verb error exited %d, want 1", exitCodeOf(err))
	}
}

// streamVerbDoc is a verb as list-verbs describes it, with what this test reads.
type streamVerbDoc struct {
	Verb        string `json:"verb"`
	Description string `json:"description"`
	Stream      *struct {
		Param  string   `json:"param"`
		Values []string `json:"values"`
	} `json:"stream"`
}

// checkStreamVerbsRefused calls every verb list-verbs marks as keeping the
// connection, with the params that make it keep it, and wants each refused
// with status 2. The marks are checked against the descriptions: a verb that
// says it turns this connection into something must be marked.
func checkStreamVerbsRefused(t *testing.T, base string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "list-verbs")
	if err != nil {
		t.Fatalf("list-verbs: %v: %s", err, out)
	}
	var list struct {
		Verbs []streamVerbDoc `json:"verbs"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Verbs) == 0 {
		t.Fatalf("list-verbs is not a verb list: %v: %.300s", err, out)
	}
	var marked []string
	for _, v := range list.Verbs {
		says := strings.Contains(strings.ToLower(v.Description), "this connection into")
		if says && v.Stream == nil {
			t.Errorf("ASSERTION: %s says it turns the connection into something, and list-verbs does not mark it stream: %.160s", v.Verb, v.Description)
		}
		if v.Stream == nil {
			continue
		}
		marked = append(marked, v.Verb)
		params := `{}`
		if v.Stream.Param != "" {
			if len(v.Stream.Values) == 0 {
				t.Errorf("ASSERTION: %s streams with param %s and names no value", v.Verb, v.Stream.Param)
				continue
			}
			val, _ := json.Marshal(v.Stream.Values[0])
			if v.Stream.Values[0] == "true" {
				val = []byte("true")
			}
			params = fmt.Sprintf(`{%q:%s}`, v.Stream.Param, val)
		}
		out, err := tuiosCLI(t, base, "call", v.Verb, params)
		if code := exitCodeOf(err); code != 2 || !strings.Contains(out, "holds the connection open") {
			t.Errorf("ASSERTION: call %s %s was not refused as a stream verb with status 2: status %d: %s", v.Verb, params, code, out)
		}
		// Only commands that exist are named.
		if strings.Contains(out, "tuios transfers") || strings.Contains(out, "tuios pane-view") {
			t.Errorf("ASSERTION: the refusal of %s names a command that does not exist: %s", v.Verb, out)
		}
	}
	// open-tree-stream and pane-kitty-medium were missing from the list call
	// kept by hand.
	for _, want := range []string{"subscribe", "open-tree-stream", "pane-kitty-medium"} {
		if !slices.Contains(marked, want) {
			t.Errorf("ASSERTION: list-verbs does not mark %s as keeping the connection; marked: %v", want, marked)
		}
	}
	// The positive half: a verb that streams only with a param is called when
	// the param says not to. speed-test answers a ping once.
	if out, err := tuiosCLI(t, base, "call", "speed-test", `{"mode":"ping"}`); err != nil || !strings.Contains(out, `"ping"`) {
		t.Errorf("ASSERTION: call speed-test with mode ping was not sent: %v: %s", err, out)
	}
}

// exitCodeOf is the status a tuiosCLI error carries, 0 for none.
func exitCodeOf(err error) int {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// TestCallVerbTransport: a daemon that is not there, or does not answer in
// time, exits 3; with --json, as JSON with the code transport. A verb that
// waits longer than the default 30 seconds is waited for: wait-for with a
// timeout of 32 seconds answers with its own timeout code, not a transport
// error at 30 seconds. With a fixed 30 second wait it fails.
func TestCallVerbTransport(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "calltx", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}

	start := time.Now()
	out, err := tuiosCLI(t, base, "call", "wait-for", `{"session":"calltx","condition":"window-output","pattern":"NEVER-PRINTED","timeout":32000}`)
	if code := exitCodeOf(err); code != 1 || !strings.Contains(out, `"timeout"`) || time.Since(start) < 31*time.Second {
		t.Fatalf("ASSERTION: a 32 s wait-for exited %d after %s, want the verb's own timeout after 32 s: %s", code, time.Since(start), out)
	}

	out, err = tuiosCLI(t, base, "call", "--timeout", "1s", "--json", "wait-for", `{"session":"calltx","condition":"window-output","pattern":"NEVER-PRINTED","timeout":20000}`)
	if code := exitCodeOf(err); code != 3 || !strings.Contains(out, `"transport"`) {
		t.Fatalf("ASSERTION: a call past --timeout exited %d, want 3 with a transport error as JSON: %s", code, out)
	}

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "call", "--json", "list-sessions")
	if code := exitCodeOf(err); code != 3 || !strings.Contains(out, `"transport"`) {
		t.Fatalf("ASSERTION: call with no daemon exited %d, want 3 with a transport error as JSON: %s", code, out)
	}
}

// TestCallVerbFromAPane: call is no way around the person's checks. From
// inside a pane, attach-presence is refused (forbidden), and so is respond
// (not_human), as they are for any connection from a pane.
func TestCallVerbFromAPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "callpane", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	win := firstWindow(t, base, "callpane").ID
	file := filepath.Join(base, "call-from-pane.txt")
	line := fmt.Sprintf(`%[1]s call attach-presence > %[2]s 2>&1; echo "STATUS=$?" >> %[2]s; %[1]s call respond '{"window":"%[3]s","action":"approve"}' >> %[2]s 2>&1; echo "STATUS=$?" >> %[2]s; echo DONE >> %[2]s`, tuiosBin, file, win)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "callpane", "-w", win, line+"\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	var got string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), "DONE") {
			got = string(b)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == "" {
		t.Fatalf("the pane never ran the calls")
	}
	if !strings.Contains(got, `"forbidden"`) || !strings.Contains(got, `"not_human"`) || strings.Count(got, "STATUS=1") != 2 {
		t.Fatalf("ASSERTION: from a pane, attach-presence must be forbidden and respond not_human, each exiting 1:\n%s", got)
	}
}
