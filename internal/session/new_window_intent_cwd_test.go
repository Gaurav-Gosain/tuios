package session

import (
	"path/filepath"
	"testing"
	"time"
)

// A client asks for a new window in a directory by naming it on the intent,
// and the daemon starts the shell there. Loading a layout uses this, so no cd
// has to be typed into the new pane.
func TestNewWindowIntentStartsInTheNamedDirectory(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "intent-cwd")
	tui := attachTestClient(t, "intent-cwd")

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := tui.SendIntentIn(dir, "NewWindow", "in-dir"); err != nil {
		t.Fatalf("send intent: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, w := range sess.GetState().Windows {
			if w.CustomName == "in-dir" {
				if got := cwdOfWindow(t, sess, w.ID); got != dir {
					t.Fatalf("new window started in %q, want %q", got, dir)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the daemon never made the window")
}
