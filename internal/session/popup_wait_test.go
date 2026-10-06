package session

import (
	"runtime"
	"testing"
	"time"
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

// TestPopupWaitAfterTheDaemonClosedIt is a race regression. The daemon closes
// a popup when its command exits, and the close removes the PTY that holds the
// exit status. A caller that waits must still get the status when the close
// comes first. The hook holds the wait until the PTY is gone, so the order is
// the same on every run.
func TestPopupWaitAfterTheDaemonClosedIt(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	attachTUI(t, sp, "work")
	c := dialVerb(t, sp)

	closed := false
	popupBeforeWaitHook = func(sess *Session, win WindowState) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if sess.GetPTY(win.PTYID) == nil {
				closed = true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Cleanup(func() { popupBeforeWaitHook = nil })

	res := result(t, c.call(t, `{"id":1,"verb":"popup","params":{"session":"work","command":["sh","-c","exit 4"],"wait":true,"timeout":10000}}`))
	if !closed {
		t.Fatalf("the daemon never closed the popup after its command exited")
	}
	if res["exit_code"] != float64(4) {
		t.Fatalf("popup wait = %v, want exit_code 4 after the daemon closed the popup", res)
	}
}
