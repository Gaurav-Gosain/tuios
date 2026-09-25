package session

import (
	"net"
	"sync"
	"testing"
	"time"
)

// TestAStateChangeDuringAnAttachReachesTheAttachingClient lands a state push
// from one client inside another client's attach, between the snapshot the
// attach reply carries and the reply itself. The push's broadcast skips the
// attaching client, which is not in the broadcast set until the reply goes, so
// the reply is the only way the change can reach it, and the reply was built
// before the change.
//
// This was an internal/app focus-switch test (the shared_borders_disagree case)
// failing in CI: the first client pushed its pane geometry as the second
// attached, and the second kept its own geometry for good, so the two ran the
// same PTYs at different sizes. e2e TestGeometryConfigDisagreementDoesNotMovePanes
// now covers the two clients with disagreeing geometry settings.
//
// NEGATIVE CONTROL: without the state check after the reply in handleAttach
// the change never reaches the attaching client.
func TestAStateChangeDuringAnAttachReachesTheAttachingClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "geo")

	conn, err := net.DialTimeout("unix", sp, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	first := NewTUIClient()
	first.conn = conn
	if err := first.handshake("test", 80, 24, nil); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	state, err := first.AttachSession("geo", false, 80, 24)
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	if state.PaneGeometry != nil && state.PaneGeometry.SharedBorders {
		t.Fatal("the session already has shared borders, so the push below changes nothing")
	}

	// The hook runs on the daemon's connection goroutine and uses first, state
	// and t. The atomic store orders what the test wrote before it, and
	// hookDone orders what the hook did before the cleanups below close first
	// and end the test. The socket orders neither for the race detector.
	hookDone := make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() {
			defer close(hookDone)
			pushed := *state
			pushed.PaneGeometry = &PaneGeometryState{SharedBorders: true}
			pushed.BaseVersion = state.Version
			if err := first.UpdateState(&pushed); err != nil {
				t.Errorf("push: %v", err)
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				pg := d.manager.GetSession("geo").GetState().PaneGeometry
				if pg != nil && pg.SharedBorders {
					return
				}
				if time.Now().After(deadline) {
					t.Errorf("the daemon never applied the push")
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	attachSnapshotTaken.Store(&hook)
	t.Cleanup(func() {
		attachSnapshotTaken.Store(nil)
		select {
		case <-hookDone:
		case <-time.After(10 * time.Second):
			t.Error("the attach hook never finished")
		}
	})

	second := attachTUI(t, sp, "geo")
	got := make(chan bool, 16)
	second.OnStateSync(func(s *SessionState, _, _ string) {
		got <- s.PaneGeometry != nil && s.PaneGeometry.SharedBorders
	})
	second.StartReadLoop()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case shared := <-got:
			if shared {
				return
			}
		case <-deadline:
			t.Fatal("a state pushed while this client was attaching never reached it")
		}
	}
}
