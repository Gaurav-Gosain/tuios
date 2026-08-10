package session

import (
	"slices"
	"testing"
	"time"
)

// TestDeleteSessionRemovesAndTerminates is the manager-level guarantee behind a
// kill: the session leaves the manager and its PTY process is actually killed,
// not merely detached. A session that stays in the manager or whose shell keeps
// running is the daemon-side half of a kill that "did not remove it properly".
func TestDeleteSessionRemovesAndTerminates(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "doomed")

	ids := sess.ListPTYIDs()
	if len(ids) == 0 {
		t.Fatal("precondition: session has no PTY to terminate")
	}
	pty := sess.GetPTY(ids[0])
	if pty == nil {
		t.Fatal("precondition: could not get the session PTY")
	}

	if err := d.manager.DeleteSession("doomed"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if d.manager.GetSession("doomed") != nil {
		t.Error("session still present in the manager after DeleteSession")
	}
	if d.manager.GetSessionByID(sess.ID) != nil {
		t.Error("session still present in the ID index after DeleteSession")
	}

	// The shell process is killed asynchronously (Process.Kill then Wait), so poll.
	deadline := time.Now().Add(3 * time.Second)
	for !pty.IsExited() {
		if time.Now().After(deadline) {
			t.Fatal("the session PTY process was not terminated after DeleteSession")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestKillSessionByNameRefreshesCache is the regression test for a killed session
// lingering in the UI: the list a client reads to draw the session switcher and
// sidebar (AvailableSessionNames) must drop a session the moment the client kills
// it, without a separate refresh call. Before this, KillSessionByName sent the
// kill fire-and-forget and never touched the cache, so the dead session stayed on
// screen until something else happened to refresh the list.
func TestKillSessionByNameRefreshesCache(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "keep")
	makeSessionWithWindow(t, d, "victim")

	// Attach after both sessions exist so the welcome seeds the cache with both.
	client := attachTestClient(t, "keep")

	before := client.AvailableSessionNames()
	if !slices.Contains(before, "victim") {
		t.Fatalf("precondition: cache should list victim before the kill, got %v", before)
	}

	if err := client.KillSessionByName("victim"); err != nil {
		t.Fatalf("KillSessionByName: %v", err)
	}

	// The daemon really removed it.
	if d.manager.GetSession("victim") != nil {
		t.Fatal("daemon still holds the killed session")
	}

	// And the cache the UI reads reflects that without a separate RefreshSessionList.
	after := client.AvailableSessionNames()
	if slices.Contains(after, "victim") {
		t.Errorf("killed session still lingers in the client cache the UI reads: %v", after)
	}
	if !slices.Contains(after, "keep") {
		t.Errorf("kill dropped an unrelated session from the cache: %v", after)
	}
}

// TestKillSessionByNameReportsDaemonError proves the client learns when a kill did
// not happen rather than reporting a phantom success: killing a name the daemon
// does not know must return an error, not swallow the daemon's rejection.
func TestKillSessionByNameReportsDaemonError(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "keep")

	client := attachTestClient(t, "keep")

	if err := client.KillSessionByName("ghost"); err == nil {
		t.Fatal("killing an unknown session reported success")
	}
}
