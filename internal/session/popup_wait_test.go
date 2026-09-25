package session

import (
	"runtime"
	"testing"
)

// TestPopupWaitReturnsExitCodeAndStdout is popup used the way a script uses a
// command: the call stays open until the program exits, and returns its status
// and what it printed to standard output, while what it wrote to the terminal
// stays in the popup.
func TestPopupWaitReturnsExitCodeAndStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("capture_stdout is not supported on Windows")
	}
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	attachTUI(t, sp, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"popup","params":{"session":"work","command":["sh","-c","echo drawn >&2; echo picked; exit 4"],"wait":true,"capture_stdout":true,"timeout":10000}}`))
	if res["type"] != "popup_result" || res["exit_code"] != float64(4) {
		t.Fatalf("popup wait = %v, want popup_result with exit_code 4", res)
	}
	if out, _ := res["stdout"].(string); out != "picked\n" {
		t.Fatalf("captured stdout = %q, want %q: only standard output, not what was drawn", out, "picked\n")
	}

	res = result(t, c.call(t, `{"id":1,"verb":"popup","params":{"session":"work","command":["sh","-c","exit 0"],"wait":true,"timeout":10000}}`))
	if res["exit_code"] != float64(0) {
		t.Fatalf("popup wait = %v, want exit_code 0", res)
	}
	if _, captured := res["stdout"]; captured {
		t.Fatalf("a popup that did not ask for capture returned stdout: %v", res)
	}
}
