package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
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

// TestRunReturnsExitCodeAndOutput is the verb end to end: it types at the
// prompt, waits for the shell to say the command finished, and hands back the
// status and exactly what the command printed. The same facts then show in
// list-windows, in capture-pane's last-command-output, in a wait-for that
// started after the command finished, and in the after-command-finished hook.
func TestRunReturnsExitCodeAndOutput(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterCommandFinished)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	openFakeShell(t, c, "build")

	res := result(t, c.call(t, `{"id":1,"verb":"run","params":{"session":"work","window":"build","command":"echo hello; echo world; exit 3","timeout":8000}}`))
	if res["type"] != "command_result" || res["exit_code"] != float64(3) {
		t.Fatalf("run = %v, want command_result with exit_code 3", res)
	}
	if res["output"] != "hello\nworld" {
		t.Fatalf("output = %q, want %q", res["output"], "hello\nworld")
	}
	if res["cmdline"] != "echo hello; echo world; exit 3" || res["command_seq"] != float64(1) {
		t.Fatalf("run = %v, want the command line and command_seq 1", res)
	}

	w := waitForWindowField(t, c, "build", "command_seq", float64(1))
	if w["at_prompt"] != true || w["last_exit_code"] != float64(3) || w["last_cmdline"] != "echo hello; echo world; exit 3" {
		t.Fatalf("list-windows entry = %v, want at_prompt, last_exit_code 3 and the command", w)
	}

	cap := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","window":"build","source":"last-command-output"}}`))
	if cap["content"] != "hello\nworld" || cap["exit_code"] != float64(3) {
		t.Fatalf("capture last-command-output = %v, want the output and exit_code 3", cap)
	}

	// The command already finished: a wait that names the count it read
	// before must still see it.
	wait := result(t, c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"command-finished","session":"work","window":"build","command_seq":0,"timeout":2000}}`))
	if wait["matched"] != true || wait["exit_code"] != float64(3) {
		t.Fatalf("wait-for command-finished = %v, want a match with exit_code 3", wait)
	}

	fired := rec.await(t, hooks.AfterCommandFinished, 1)
	if fired[0].ExitCode != "3" || fired[0].Command != "echo hello; echo world; exit 3" {
		t.Fatalf("hook context = %+v, want exit 3 and the command", fired[0])
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

// TestRunRefusesAPaneWithoutIntegration checks the pane that never marks a
// command: run cannot tell where one starts or ends there, so it types
// nothing and says why.
func TestRunRefusesAPaneWithoutIntegration(t *testing.T) {
	prev := runFirstPromptWait
	runFirstPromptWait = 200 * time.Millisecond
	t.Cleanup(func() { runFirstPromptWait = prev })

	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	result(t, c.call(t, `{"id":1,"verb":"new-window","params":{"session":"work","name":"plain","command":["cat"],"focus":false}}`))

	resp := c.call(t, `{"id":1,"verb":"run","params":{"session":"work","window":"plain","command":"echo typed","timeout":2000}}`)
	if code := errCode(t, resp); code != ErrVerbNoShellIntegration {
		t.Fatalf("run on a pane with no marks: code %q, want %q", code, ErrVerbNoShellIntegration)
	}
	resp = c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","window":"plain","source":"last-command-output"}}`)
	if code := errCode(t, resp); code != ErrVerbNoShellIntegration {
		t.Fatalf("last-command-output on a pane with no marks: code %q, want %q", code, ErrVerbNoShellIntegration)
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
