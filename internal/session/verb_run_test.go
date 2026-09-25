package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// fakeIntegratedShell is a shell with OSC 133 prompt integration in a few lines
// of sh: it marks its prompt, reads a line, marks the command running, runs it
// and marks it finished with its status. It is what zsh, fish or an injected
// bash script send, without depending on which shell the test machine has or
// how it is configured.
const fakeIntegratedShell = `prompt() { printf '\033]133;A\007$ \033]133;B\007'; }
prompt
while IFS= read -r line; do
  printf '\033]133;C\007'
  sh -c "$line"
  rc=$?
  printf '\033]133;D;%s\007' "$rc"
  prompt
done`

// openFakeShell opens a window running fakeIntegratedShell and waits for its
// first prompt to reach the daemon.
func openFakeShell(t *testing.T, c *verbConn, name string) {
	t.Helper()
	cmd, _ := json.Marshal([]string{"sh", "-c", fakeIntegratedShell})
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"new-window","params":{"session":"work","name":%q,"command":%s,"focus":false}}`, name, cmd)))
	waitForWindowField(t, c, name, "at_prompt", true)
}

// waitForWindowField polls list-windows until the named window reports want
// under key.
func waitForWindowField(t *testing.T, c *verbConn, name, key string, want any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		res := result(t, c.call(t, `{"id":1,"verb":"list-windows","params":{"session":"work"}}`))
		windows, _ := res["windows"].([]any)
		for _, w := range windows {
			m, _ := w.(map[string]any)
			if m["custom_name"] == name && m[key] == want {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %q never reported %s=%v: %v", name, key, want, windows)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunRefusesARunningPane holds run to its one promise: it never types into
// a program that is running. The pane is busy with a command started by hand,
// so run refuses with not_at_prompt, names the command, and types nothing.
func TestRunRefusesARunningPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	openFakeShell(t, c, "build")

	result(t, c.call(t, `{"id":1,"verb":"send-text","params":{"session":"work","window":"build","text":"sleep 5\n"}}`))
	waitForWindowField(t, c, "build", "at_prompt", false)

	resp := c.call(t, `{"id":1,"verb":"run","params":{"session":"work","window":"build","command":"echo typed","timeout":2000}}`)
	if code := errCode(t, resp); code != ErrVerbNotAtPrompt {
		t.Fatalf("run on a busy pane: code %q, want %q (%v)", code, ErrVerbNotAtPrompt, resp)
	}
	res := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","window":"build"}}`))
	if content, _ := res["content"].(string); containsLine(content, "echo typed") {
		t.Fatalf("run typed into a running command:\n%s", content)
	}
}

// TestRunOneAtATimeInAPane starts two runs in one pane at once. Both would
// pass the prompt check before either command starts, and the shell would read
// one line made of both. One run holds the pane, and the other is refused
// with not_at_prompt and types nothing.
func TestRunOneAtATimeInAPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	openFakeShell(t, c, "build")

	commands := []string{"echo first", "echo second"}
	resps := make(chan map[string]any, len(commands))
	start := make(chan struct{})
	for _, cmd := range commands {
		conn := dialVerb(t, sp)
		go func() {
			<-start
			_ = conn.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, _ = fmt.Fprintf(conn.conn, `{"id":1,"verb":"run","params":{"session":"work","window":"build","command":%q,"timeout":8000}}`+"\n", cmd)
			_ = conn.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			line, err := conn.r.ReadBytes('\n')
			if err != nil {
				resps <- map[string]any{"read_error": err.Error()}
				return
			}
			var resp map[string]any
			_ = json.Unmarshal(line, &resp)
			resps <- resp
		}()
	}
	close(start)

	var ok, refused []map[string]any
	for range commands {
		resp := <-resps
		if e, _ := resp["error"].(map[string]any); e != nil {
			if e["code"] != ErrVerbNotAtPrompt {
				t.Fatalf("the refused run: %v, want %q", resp, ErrVerbNotAtPrompt)
			}
			refused = append(refused, resp)
			continue
		}
		ok = append(ok, result(t, resp))
	}
	if len(ok) != 1 || len(refused) != 1 {
		t.Fatalf("two runs at once: %d ran and %d were refused, want one each (ran %v)", len(ok), len(refused), ok)
	}
	res := ok[0]
	want := map[string]string{"echo first": "first", "echo second": "second"}[res["cmdline"].(string)]
	if want == "" || res["output"] != want || res["command_seq"] != float64(1) {
		t.Fatalf("the run that held the pane = %v, want one command with its own output", res)
	}
}

// TestRunRejectsAMultiLineCommand keeps a command to one line. A newline
// typed at a prompt is Enter, so a second line would run as a second command
// the result says nothing about.
func TestRunRejectsAMultiLineCommand(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"run","params":{"session":"work","command":"make\nrm -rf x"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("multi-line command: code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// containsLine reports whether any line of s contains sub.
func containsLine(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
