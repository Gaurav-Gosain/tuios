package session

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestListingPushReplacesThePoll is the session listing push
// (daemon_listing_push.go). A client that reads MsgSessionsChanged hears that
// a window in another session was renamed, without asking. A daemon with
// nothing happening looks at the listing no more and sends nothing.
//
// NEGATIVE CONTROL: with the listing latch unable to wake the notifier (its
// channel never made), the rename is not pushed and the test fails.
func TestListingPushReplacesThePoll(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "mine")
	other := makeSessionWithWindow(t, d, "other")

	c := dialTUI(t, sp)
	if !c.ListingPush() {
		t.Fatal("the daemon's welcome did not offer ListingPush")
	}
	var pushes atomic.Int64
	c.OnSessionsChanged(func() { pushes.Add(1) })
	if _, err := c.AttachSession("mine", false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()

	// Let the shells print their prompts and the attach settle.
	waitIdleSettled(t, []idleCounters{{"listing", &d.wakes.listingTicks}}, 2*listingPushGap, 30*time.Second)
	before, looks := pushes.Load(), d.wakes.listingTicks.Load()
	time.Sleep(listingPushGap + time.Second)
	if n := d.wakes.listingTicks.Load() - looks; n != 0 {
		t.Errorf("the listing notifier looked %d times with nothing happening, want 0", n)
	}
	if n := pushes.Load() - before; n != 0 {
		t.Errorf("the daemon pushed %d listing changes with nothing happening, want 0", n)
	}

	win := other.GetState().Windows[0].ID
	if err := other.RenameDaemonWindow(win, "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	deadline := time.Now().Add(listingPushGap + 2*time.Second)
	for pushes.Load() == before {
		if time.Now().After(deadline) {
			t.Fatal("a window renamed in another session was not pushed to the client")
		}
		time.Sleep(20 * time.Millisecond)
	}
	infos, err := c.RefreshSessionList()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, info := range infos {
		for _, w := range info.Windows {
			if w.ID == win && w.Title == "renamed" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the listing after the push does not show the new name: %+v", infos)
	}
}
