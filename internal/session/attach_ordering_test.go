package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAttachIsNeverOutrunByABroadcast attaches clients while a peer is
// announcing chrome as fast as it can. Every announcement that moves the
// session's reserve is a broadcast to the whole session, so if the broadcast
// set includes a client mid-attach, that client reads the broadcast as its
// attach reply and the attach fails.
//
// NEGATIVE CONTROL: measured. Without the attached gate in broadcastToSession
// this fails within a few rounds, with "unexpected response: 49", message 49
// being MsgSessionResize. The same fault was reaching internal/app as an
// intermittent failure of TestFloatingPanesAreClampedWhenAnotherClientShrinks
// TheSession at roughly six runs in a hundred; here it is deterministic enough
// to see in one.
func TestAttachIsNeverOutrunByABroadcast(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "busy")

	peer := attachTestClient(t, "busy")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			peer.SetOwnLayoutReserve(LayoutReserve{Left: 10 + i%2})
			if err := peer.NotifyTerminalSize(80, 24); err != nil {
				return
			}
		}
	})
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})

	for range 60 {
		c := NewTUIClient()
		if err := c.Connect("test", 80, 24); err != nil {
			t.Fatalf("connect: %v", err)
		}
		if _, err := c.AttachSession("busy", false, 80, 24); err != nil {
			_ = c.Close()
			t.Fatalf("attach while the session was being announced at: %v", err)
		}
		if err := c.Detach(); err != nil {
			_ = c.Close()
			t.Fatalf("detach: %v", err)
		}
		_ = c.Close()
	}
}

// TestAttachReplyCarriesWhatTheSessionSettledOn is the other half of the rule.
// Holding a broadcast back from a client that is attaching is only correct if
// the reply tells it the same thing the broadcast would have: the size and the
// chrome reserve the session settled on once this client was counted.
//
// NEGATIVE CONTROL: none, deliberately: a passes-both-ways control. It states
// what the reply owes, which no other test in this package reads off the reply
// itself, and it would catch a fix for the ordering that bought it by leaving
// the joiner uninformed.
func TestAttachReplyCarriesWhatTheSessionSettledOn(t *testing.T) {
	d, _ := startTestDaemonSized(t, config.WindowSizeSmallest)
	sess := makeSessionWithWindow(t, d, "settled")

	wide := NewTUIClient()
	if err := wide.Connect("test", 120, 40); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = wide.Close() })
	wide.SetOwnLayoutReserve(LayoutReserve{Left: 24})
	if _, err := wide.AttachSession("settled", false, 120, 40); err != nil {
		t.Fatalf("attach the wide client: %v", err)
	}
	wide.StartReadLoop()
	if err := wide.NotifyTerminalSize(120, 40); err != nil {
		t.Fatalf("announce the wide client's chrome: %v", err)
	}
	waitForLayout(t, "the session to take the rail", func() bool {
		return sess.LayoutReserve().Left == 24
	})

	joiner := NewTUIClient()
	if err := joiner.Connect("test", 60, 18); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = joiner.Close() })
	if _, err := joiner.AttachSession("settled", false, 60, 18); err != nil {
		t.Fatalf("attach the joiner: %v", err)
	}
	joiner.StartReadLoop()

	if got := joiner.SessionLayoutReserve(); got.Left != 24 {
		t.Errorf("the attach reply gave the joiner a reserve of %+v, want the session's Left:24", got)
	}
	if w, h := sess.Size(); w != 60 || h != 18 {
		t.Errorf("the session settled at %dx%d, want the joiner's 60x18", w, h)
	}
}

func waitForLayout(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestAClientAttachingIsStillToldItsSessionDied is the other end of the same
// rule. Holding broadcasts back until the reply is written is only safe if
// "written" and "may now be spoken to" are one step: a session killed between
// the two would be announced to everybody except the one client that had just
// been told it was attached to it, which then sits in a dead session forever.
//
// NEGATIVE CONTROL: measured, for one of the two things that make it pass.
// Removing the post-reply check in handleAttach (the one that asks whether the
// session survived the handshake and tells this client itself if it did not)
// fails this three times in two hundred runs. With the check, none in two
// hundred. That hole was there before any of this work and is what the test
// found: a session deleted after the client registered on it but before the
// reply put the client in the broadcast set reached neither route, and the
// client sat attached to something that no longer existed.
//
// It is deliberately NOT a control for the other half, the flag being set under
// the same send lock as the reply. That window is a few instructions wide and
// this test does not reach it: sixty runs plain and twenty under -race with the
// mark moved back after the write, and not one failure. That ordering is here
// because it is right, not because anything caught it.
func TestAClientAttachingIsStillToldItsSessionDied(t *testing.T) {
	d, _ := startTestDaemon(t)

	for round := range 40 {
		name := fmt.Sprintf("dying-%d", round)
		if _, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}

		c := NewTUIClient()
		if err := c.Connect("test", 80, 24); err != nil {
			t.Fatalf("connect: %v", err)
		}
		ended := make(chan string, 4)
		c.OnSessionEnded(func(n, _ string) { ended <- n })

		killed := make(chan struct{})
		deleted := make(chan struct{})
		go func() {
			defer close(deleted)
			close(killed)
			_ = d.manager.DeleteSession(name)
		}()

		if _, err := c.AttachSession(name, false, 80, 24); err != nil {
			<-killed
			<-deleted
			_ = c.Close()
			continue
		}
		c.StartReadLoop()

		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			_ = c.Close()
			<-deleted
			t.Fatalf("round %d: attached to a session that died without telling it", round)
		}
		<-deleted
		_ = c.Close()
	}
}
