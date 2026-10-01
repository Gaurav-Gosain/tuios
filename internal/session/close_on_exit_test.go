package session

import (
	"testing"
	"time"
)

// new-window with close_on_exit closes the window when its process exits,
// with no client attached. Without it, a detached session keeps the window.
func TestNewWindowCloseOnExitWithoutAClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "w")
	c := dialVerb(t, sp)
	open := func(close bool) string {
		t.Helper()
		got := result(t, callP(c, t, "new-window", map[string]any{
			"session": "w", "command": []string{"sh", "-c", "exit 0"}, "close_on_exit": close, "focus": false,
		}))
		id, _ := got["window_id"].(string)
		if id == "" {
			t.Fatalf("new-window = %v", got)
		}
		return id
	}
	has := func(id string) bool {
		for _, w := range sess.GetState().Windows {
			if w.ID == id {
				return true
			}
		}
		return false
	}
	closing := open(true)
	kept := open(false)
	deadline := time.Now().Add(5 * time.Second)
	for has(closing) {
		if time.Now().After(deadline) {
			t.Fatal("the close_on_exit window stayed after its process exited")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if !has(kept) {
		t.Fatal("a window without close_on_exit closed on a detached session")
	}
}
